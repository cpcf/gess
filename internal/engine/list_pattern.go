package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type ListPatternElementKind = gessrules.ListPatternElementKind

const (
	ListPatternElementUnknown      = gessrules.ListPatternElementUnknown
	ListPatternElementValue        = gessrules.ListPatternElementValue
	ListPatternElementWildcard     = gessrules.ListPatternElementWildcard
	ListPatternElementSegment      = gessrules.ListPatternElementSegment
	ListPatternElementRestWildcard = gessrules.ListPatternElementRestWildcard
)

type ListPatternElementSpec = gessrules.ListPatternElementSpec

func cloneListPatternElementSpec(s ListPatternElementSpec) ListPatternElementSpec {
	return gessrules.CloneListPatternElementSpec(s)
}

type ListPatternSpec = gessrules.ListPatternSpec

func cloneListPatternSpec(s ListPatternSpec) ListPatternSpec {
	return gessrules.CloneListPatternSpec(s)
}

func ListPattern(path PathSpec, elements ...ListPatternElementSpec) ListPatternSpec {
	return gessrules.ListPattern(path, elements...)
}

func ListElem(expression ExpressionSpec) ListPatternElementSpec {
	return gessrules.ListElem(expression)
}

func ListWildcard() ListPatternElementSpec {
	return gessrules.ListWildcard()
}

func ListSegment(binding string) ListPatternElementSpec {
	return gessrules.ListSegment(binding)
}

func ListRestWildcard() ListPatternElementSpec {
	return gessrules.ListRestWildcard()
}

func listPatternsHaveSegment(patterns []ListPatternSpec) bool {
	for _, pattern := range patterns {
		for _, element := range pattern.Elements {
			if element.Kind == ListPatternElementSegment {
				return true
			}
		}
	}
	return false
}

type ListPatternElement = gessrules.ListPatternElement

func cloneListPatternElement(e ListPatternElement) ListPatternElement {
	return gessrules.CloneListPatternElement(e)
}

type RuleListPattern = gessrules.RuleListPattern

func cloneRuleListPattern(p RuleListPattern) RuleListPattern {
	return gessrules.CloneRuleListPattern(p)
}

type compiledListPatternElement struct {
	kind        ListPatternElementKind
	expression  compiledExpression
	binding     string
	bindingSlot int
}

type compiledListPattern struct {
	path     compiledPathAccess
	elements []compiledListPatternElement
	raw      RuleListPattern
}

type listPatternCapture struct {
	binding     string
	bindingSlot int
	value       Value
}

func compileListPatternSpecs(
	specs []ListPatternSpec,
	ruleName string,
	conditionIndex int,
	template *compiledTemplate, conditions []RuleCondition,
	bindingSlots map[string]int,
	params map[string]ValueKind,
	functions map[string]compiledPureFunction,
	globals map[string]compiledGlobal,
) ([]RuleListPattern, []compiledListPattern, []RuleCondition, error) {
	if len(specs) == 0 {
		return nil, nil, nil, nil
	}
	public := make([]RuleListPattern, 0, len(specs))
	compiled := make([]compiledListPattern, 0, len(specs))
	var bindings []RuleCondition
	seenSegmentBindings := make(map[string]struct{})
	for patternIndex, spec := range specs {
		spec = cloneListPatternSpec(spec)
		if pathIsZero(spec.Path) {
			return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list pattern requires a path", ErrInvalidPath)
		}
		access, kind, err := compileExpressionPathRef(ruleName, conditionIndex, patternIndex, template, spec.Path)
		if err != nil {
			return nil, nil, nil, markListPatternValidation(err)
		}
		if kind != ValueAny && kind != ValueList {
			return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list pattern path must resolve to a list", ErrInvalidListPattern)
		}
		if len(spec.Elements) == 0 {
			return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list pattern requires at least one element", ErrInvalidListPattern)
		}
		variableCount := 0
		elements := make([]compiledListPatternElement, 0, len(spec.Elements))
		publicElements := make([]ListPatternElement, 0, len(spec.Elements))
		for elementIndex, element := range spec.Elements {
			element = cloneListPatternElementSpec(element)
			publicElement := ListPatternElement{
				KindValue:      element.Kind,
				ExpressionSpec: cloneExpressionSpec(element.Expression),
				BindingName:    element.Binding,
				Order:          elementIndex,
			}
			compiledElement := compiledListPatternElement{kind: element.Kind, binding: element.Binding, bindingSlot: -1}
			switch element.Kind {
			case ListPatternElementValue:
				if element.Expression == nil {
					return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list element requires an expression", ErrInvalidListPattern)
				}
				expression, referencesEarlier, err := compileExpressionSpecWithParams(element.Expression, ruleName, conditionIndex, elementIndex, nil, conditions, bindingSlots, nil, params, functions, globals)
				if err != nil {
					return nil, nil, nil, markListPatternValidation(err)
				}
				if referencesEarlier || expression.kind != expressionNodeConst {
					return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list element expression must be a constant in this slice", ErrUnsupportedRuntime)
				}
				compiledElement.expression = expression
			case ListPatternElementWildcard:
			case ListPatternElementSegment:
				variableCount++
				if element.Binding == "" {
					return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list segment binding is required", ErrInvalidListPattern)
				}
				if !isValidBindingName(element.Binding) {
					return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "invalid list segment binding", ErrInvalidListPattern)
				}
				if _, exists := seenSegmentBindings[element.Binding]; exists {
					return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "duplicate list segment binding", ErrInvalidListPattern)
				}
				if _, exists := bindingSlots[element.Binding]; exists {
					return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list segment binding collides with an existing binding", ErrInvalidListPattern)
				}
				seenSegmentBindings[element.Binding] = struct{}{}
				compiledElement.bindingSlot = len(conditions) + len(bindings)
				bindings = append(bindings, RuleCondition{
					BindingName: element.Binding,
					Order:       compiledElement.bindingSlot,
				})
			case ListPatternElementRestWildcard:
				variableCount++
			default:
				return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "invalid list pattern element", ErrInvalidListPattern)
			}
			if variableCount > 1 {
				return nil, nil, nil, listPatternValidationError(ruleName, conditionIndex, patternIndex, "list pattern supports at most one variable-length element", ErrInvalidListPattern)
			}
			elements = append(elements, compiledElement)
			publicElements = append(publicElements, publicElement)
		}
		publicPattern := RuleListPattern{PathSpec: clonePathSpec(spec.Path), ElementsValue: publicElements, Order: patternIndex}
		public = append(public, publicPattern)
		compiled = append(compiled, compiledListPattern{path: access.clone(), elements: elements, raw: cloneRuleListPattern(publicPattern)})
	}
	return public, compiled, bindings, nil
}

func listPatternValidationError(ruleName string, conditionIndex, patternIndex int, reason string, err error) error {
	return &ValidationError{
		RuleName:           ruleName,
		ConditionIndex:     conditionIndex,
		HasConditionIndex:  conditionIndex >= 0,
		ConstraintIndex:    patternIndex,
		HasConstraintIndex: patternIndex >= 0,
		Reason:             reason,
		Err:                err,
	}
}

func markListPatternValidation(err error) error {
	var validation *ValidationError
	if err != nil && errors.As(err, &validation) {
		clone := *validation
		if clone.Err == nil || clone.Err == ErrValidation {
			clone.Err = ErrInvalidListPattern
		}
		return &clone
	}
	return err
}

func (p compiledListPattern) segmentBindingSlots() []int {
	var out []int
	for _, element := range p.elements {
		if element.kind == ListPatternElementSegment {
			out = append(out, element.bindingSlot)
		}
	}
	return out
}

func (p compiledListPattern) matchesFact(fact conditionFactRef, bindings tokenRef) ([]listPatternCapture, bool, error) {
	value, ok := p.path.valueFromFact(fact)
	if !ok || value.Kind() != ValueList {
		return nil, false, nil
	}
	items, _ := value.AsListShared()
	return p.matchItems(items, fact, bindings)
}

func (p compiledListPattern) matchesFactOnly(fact conditionFactRef, bindings tokenRef) (bool, error) {
	value, ok := p.path.valueFromFact(fact)
	if !ok || value.Kind() != ValueList {
		return false, nil
	}
	items, _ := value.AsListShared()
	return p.matchItemsOnly(items, fact, bindings)
}

func (p compiledListPattern) matchItems(items []Value, fact conditionFactRef, bindings tokenRef) ([]listPatternCapture, bool, error) {
	variableIndex, fixedCount := p.variableElementIndexAndFixedCount()
	if ok, err := p.matchItemsAroundVariable(items, fact, bindings, variableIndex, fixedCount); err != nil || !ok {
		return nil, ok, err
	}
	if variableIndex < 0 {
		return nil, true, nil
	}

	suffixCount := len(p.elements) - variableIndex - 1
	segmentStart := fixedCount - (len(p.elements) - variableIndex - 1)
	segmentEnd := len(items) - suffixCount
	variable := p.elements[variableIndex]
	if variable.kind != ListPatternElementSegment {
		return nil, true, nil
	}
	value, err := canonicalValue(cloneValueSlice(items[segmentStart:segmentEnd]))
	if err != nil {
		return nil, false, err
	}
	return []listPatternCapture{{binding: variable.binding, bindingSlot: variable.bindingSlot, value: value}}, true, nil
}

func (p compiledListPattern) matchItemsOnly(items []Value, fact conditionFactRef, bindings tokenRef) (bool, error) {
	variableIndex, fixedCount := p.variableElementIndexAndFixedCount()
	return p.matchItemsAroundVariable(items, fact, bindings, variableIndex, fixedCount)
}

func (p compiledListPattern) variableElementIndexAndFixedCount() (int, int) {
	var variableIndex = -1
	fixedCount := 0
	for i, element := range p.elements {
		switch element.kind {
		case ListPatternElementSegment, ListPatternElementRestWildcard:
			variableIndex = i
		default:
			fixedCount++
		}
	}
	return variableIndex, fixedCount
}

func (p compiledListPattern) matchItemsAroundVariable(items []Value, fact conditionFactRef, bindings tokenRef, variableIndex int, fixedCount int) (bool, error) {
	if variableIndex < 0 && len(items) != len(p.elements) {
		return false, nil
	}
	if variableIndex >= 0 && len(items) < fixedCount {
		return false, nil
	}

	itemIndex := 0
	for elementIndex, element := range p.elements {
		if elementIndex == variableIndex {
			break
		}
		ok, err := element.matchesItem(items[itemIndex], fact, bindings)
		if err != nil || !ok {
			return ok, err
		}
		itemIndex++
	}
	if variableIndex < 0 {
		return true, nil
	}

	suffixCount := len(p.elements) - variableIndex - 1
	segmentEnd := len(items) - suffixCount
	for elementIndex := len(p.elements) - 1; elementIndex > variableIndex; elementIndex-- {
		item := items[segmentEnd+(elementIndex-variableIndex-1)]
		ok, err := p.elements[elementIndex].matchesItem(item, fact, bindings)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func (e compiledListPatternElement) matchesItem(item Value, fact conditionFactRef, bindings tokenRef) (bool, error) {
	switch e.kind {
	case ListPatternElementValue:
		value, ok, err := e.expression.evaluateToken(fact, bindings)
		if err != nil || !ok {
			return false, err
		}
		return item.Equal(value), nil
	case ListPatternElementWildcard:
		return true, nil
	default:
		return false, nil
	}
}

func cloneCompiledListPatterns(in []compiledListPattern) []compiledListPattern {
	if len(in) == 0 {
		return nil
	}
	out := make([]compiledListPattern, len(in))
	for i, pattern := range in {
		out[i] = pattern
		out[i].path = pattern.path.clone()
		out[i].elements = make([]compiledListPatternElement, len(pattern.elements))
		copy(out[i].elements, pattern.elements)
		for j := range out[i].elements {
			out[i].elements[j].expression = out[i].elements[j].expression.clone()
		}
		out[i].raw = cloneRuleListPattern(pattern.raw)
	}
	return out
}

func serializeCompiledListPatterns(patterns []compiledListPattern) string {
	if len(patterns) == 0 {
		return ""
	}
	parts := make([]string, len(patterns))
	for i, pattern := range patterns {
		parts[i] = serializeCompiledListPattern(pattern)
	}
	sort.Strings(parts)
	sum := sha256.New()
	sum.Write([]byte("gess/rete-graph/list-pattern/v1\n"))
	for _, part := range parts {
		sum.Write(fmt.Appendf(nil, "pattern:%d:%s\n", len(part), part))
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

func serializeCompiledListPattern(pattern compiledListPattern) string {
	var b strings.Builder
	b.WriteString("path:")
	b.WriteString(pattern.path.display())
	for _, element := range pattern.elements {
		b.WriteString("\nelement:")
		b.WriteString(string(element.kind))
		b.WriteString(":")
		b.WriteString(element.binding)
		b.WriteString(":")
		b.WriteString(serializeCompiledExpression(element.expression))
	}
	return b.String()
}
