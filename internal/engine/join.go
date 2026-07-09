package engine

import (
	"fmt"

	gessrules "github.com/cpcf/gess/rules"
)

type FieldRef = gessrules.FieldRef

func cloneFieldRef(r FieldRef) FieldRef {
	return gessrules.CloneFieldRef(r)
}

type JoinConstraintSpec = gessrules.JoinConstraintSpec

func cloneJoinConstraintSpec(s JoinConstraintSpec) JoinConstraintSpec {
	return gessrules.CloneJoinConstraintSpec(s)
}

type JoinConstraint = gessrules.JoinConstraint

func cloneJoinConstraint(c JoinConstraint) JoinConstraint {
	return JoinConstraint{
		Field:    c.Field,
		Path:     clonePathSpec(c.Path),
		Operator: c.Operator,
		Ref:      cloneFieldRef(c.Ref),
	}
}

type joinIndexKind uint8

const (
	joinIndexUnknown joinIndexKind = iota
	joinIndexEquality
	joinIndexNumericComparison
	joinIndexInequality
)

type compiledJoinConstraint struct {
	path                  []int
	source                SourceSpan
	bindingSlot           int
	access                compiledPathAccess
	leftKeyExpression     compiledExpression
	hasLeftKeyExpression  bool
	operator              FieldConstraintOperator
	refBinding            string
	refBindingSlot        int
	refAccess             compiledPathAccess
	rightKeyExpression    compiledExpression
	hasRightKeyExpression bool
	indexable             bool
	indexKind             joinIndexKind
	evalMeta              *FunctionEvaluationError
}

func (c compiledJoinConstraint) isHashJoin() bool {
	return c.indexable && c.indexKind == joinIndexEquality
}

func (c compiledJoinConstraint) matchesTokenWithCounters(fact *conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	if c.refBindingSlot < 0 {
		return false, fmt.Errorf("%w: malformed join binding slot %d", ErrMatcher, c.refBindingSlot)
	}
	if refFact, found, direct := tokenFactPtrAtSlot(bindings, c.refBindingSlot); direct {
		if !found {
			return false, nil
		}
		if leftSlot, leftOK, leftDirect := c.leftKeySlotFromFactRef(fact); leftDirect {
			if rightSlot, rightOK, rightDirect := c.rightKeySlotFromFactRef(refFact); rightDirect {
				if !leftOK || !rightOK {
					return false, nil
				}
				return matchJoinKeySlots(c.operator, leftSlot, rightSlot)
			}
		}
	}
	match, ok := tokenRefAtSlot(bindings, c.refBindingSlot)
	if !ok {
		return false, nil
	}

	left, ok := c.leftValueFromFactWithCounters(*fact, span)
	if !ok {
		return false, nil
	}

	right, ok := c.rightValueFromFactWithCounters(match.fact, span)
	if !ok {
		return false, nil
	}

	switch c.operator {
	case FieldConstraintOpEqual:
		return valuesComparableForEquality(left, right) && left.Equal(right), nil
	case FieldConstraintOpNotEqual:
		return valuesComparableForEquality(left, right) && !left.Equal(right), nil
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if !isNumericValue(left) || !isNumericValue(right) {
			return false, nil
		}
		comparison := compareNumericValues(left, right)
		switch c.operator {
		case FieldConstraintOpLessThan:
			return comparison < 0, nil
		case FieldConstraintOpLessOrEqual:
			return comparison <= 0, nil
		case FieldConstraintOpGreaterThan:
			return comparison > 0, nil
		case FieldConstraintOpGreaterOrEqual:
			return comparison >= 0, nil
		}
	}

	return false, nil
}

// matchJoinKeySlots applies a join operator to two folded key slots with the
// same semantics as the Value-based comparisons in matchesTokenWithCounters.
func matchJoinKeySlots(operator FieldConstraintOperator, left, right betaJoinKeySlot) (bool, error) {
	switch operator {
	case FieldConstraintOpEqual:
		return betaJoinKeySlotsComparableForEquality(left, right) && betaJoinKeySlotsEqual(left, right), nil
	case FieldConstraintOpNotEqual:
		return betaJoinKeySlotsComparableForEquality(left, right) && !betaJoinKeySlotsEqual(left, right), nil
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		comparison, ok := compareBetaJoinKeySlots(left, right)
		if !ok {
			return false, nil
		}
		switch operator {
		case FieldConstraintOpLessThan:
			return comparison < 0, nil
		case FieldConstraintOpLessOrEqual:
			return comparison <= 0, nil
		case FieldConstraintOpGreaterThan:
			return comparison > 0, nil
		case FieldConstraintOpGreaterOrEqual:
			return comparison >= 0, nil
		}
	}
	return false, nil
}

func validJoinOperator(operator FieldConstraintOperator) bool {
	switch operator {
	case FieldConstraintOpEqual, FieldConstraintOpNotEqual, FieldConstraintOpLessThan,
		FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		return true
	default:
		return false
	}
}

func compileJoinConstraintSpec(
	spec JoinConstraintSpec,
	ruleName string,
	conditionIndex, joinIndex int,
	template *compiledTemplate, conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]compiledTemplate) (JoinConstraint, compiledJoinConstraint, error) {
	return compileJoinConstraintSpecWithSource(spec, SourceSpan{}, ruleName, conditionIndex, joinIndex, template, conditions, bindingSlots, templatesByKey)
}

func compileJoinConstraintSpecWithSource(
	spec JoinConstraintSpec,
	source SourceSpan,
	ruleName string,
	conditionIndex, joinIndex int,
	template *compiledTemplate, conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]compiledTemplate) (JoinConstraint, compiledJoinConstraint, error) {
	compiled, plan, err := compileJoinConstraintSpecWithSourceInternal(spec, source, ruleName, conditionIndex, joinIndex, template, conditions, bindingSlots, templatesByKey)
	return compiled, plan, attachValidationErrorSource(err, source)
}

func compileJoinConstraintSpecWithSourceInternal(
	spec JoinConstraintSpec,
	source SourceSpan,
	ruleName string,
	conditionIndex, joinIndex int,
	template *compiledTemplate, conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]compiledTemplate) (JoinConstraint, compiledJoinConstraint, error) {
	if hasAmbiguousFieldAndPath(spec.Field, spec.Path) {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join constraint cannot set both field and path",
			Err:               ErrInvalidPath,
		}
	}
	if hasAmbiguousFieldAndPath(spec.Ref.Field, spec.Ref.Path) {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join reference cannot set both field and path",
			Err:               ErrInvalidPath,
		}
	}
	normalized := cloneJoinConstraintSpec(spec)
	normalized.Path = pathOrField(normalized.Path, normalized.Field)
	normalized.Ref.Path = pathOrField(normalized.Ref.Path, normalized.Ref.Field)
	if pathIsZero(normalized.Path) {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join path is required",
			Err:               ErrInvalidPath,
		}
	}
	normalized.Field = pathRoot(normalized.Path)
	if normalized.Ref.Binding == "" {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join binding reference is required",
		}
	}
	if pathIsZero(normalized.Ref.Path) {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join path reference is required",
			Err:               ErrInvalidPath,
		}
	}
	normalized.Ref.Field = pathRoot(normalized.Ref.Path)
	if !validJoinOperator(normalized.Operator) {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "invalid join operator",
		}
	}

	access, err := compileJoinPathAccess(normalized.Path, ruleName, conditionIndex, joinIndex, template)
	if err != nil {
		return JoinConstraint{}, compiledJoinConstraint{}, err
	}

	refSlot, ok := bindingSlots[normalized.Ref.Binding]
	if !ok {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join binding reference must refer to an earlier condition",
		}
	}
	if refSlot < 0 || refSlot >= len(conditions) {
		return JoinConstraint{}, compiledJoinConstraint{}, fmt.Errorf("%w: malformed join binding slot %d", ErrMatcher, refSlot)
	}

	refCondition := conditions[refSlot]
	refAccess := compiledPathAccess{path: clonePathSpec(normalized.Ref.Path), root: pathRoot(normalized.Ref.Path), rootSlot: -1}
	if err := validatePathSpec(normalized.Ref.Path); err != nil {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			FieldName:         pathRoot(normalized.Ref.Path),
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "invalid path",
			Err:               err,
		}
	}
	if refCondition.TemplateKeyValue != "" {
		refTemplate, ok := templatesByKey[refCondition.TemplateKeyValue]
		if !ok {
			return JoinConstraint{}, compiledJoinConstraint{}, fmt.Errorf("%w: missing template for join binding %q", ErrMatcher, normalized.Ref.Binding)
		}
		refAccess, err = compileJoinPathAccess(normalized.Ref.Path, ruleName, conditionIndex, joinIndex, &refTemplate)
		if err != nil {
			return JoinConstraint{}, compiledJoinConstraint{}, err
		}
	}

	indexKind := joinIndexEquality
	switch normalized.Operator {
	case FieldConstraintOpNotEqual:
		indexKind = joinIndexInequality
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		indexKind = joinIndexNumericComparison
	}

	indexable := access.topLevel() && refAccess.topLevel()

	return JoinConstraint{
			Field:    normalized.Field,
			Path:     clonePathSpec(normalized.Path),
			Operator: normalized.Operator,
			Ref:      cloneFieldRef(normalized.Ref),
		}, newCompiledJoinConstraint(compiledJoinConstraint{
			path:           []int{conditionIndex, joinIndex},
			source:         source,
			bindingSlot:    conditionIndex,
			access:         access,
			operator:       normalized.Operator,
			refBinding:     normalized.Ref.Binding,
			refBindingSlot: refSlot,
			refAccess:      refAccess,
			indexable:      indexable,
			indexKind:      indexKind,
		}), nil
}

// newCompiledJoinConstraint precomputes the evaluation-error metadata so join
// evaluation never allocates it per probe.
func newCompiledJoinConstraint(join compiledJoinConstraint) compiledJoinConstraint {
	join.evalMeta = buildJoinFunctionEvaluationMeta(join)
	return join
}

func compileJoinPathAccess(path PathSpec, ruleName string, conditionIndex, joinIndex int, template *compiledTemplate) (compiledPathAccess, error) {
	if template != nil && template.closed && pathRoot(path) != "" {
		if _, ok := template.fieldSlot(pathRoot(path)); !ok {
			return compiledPathAccess{}, &ValidationError{
				RuleName:          ruleName,
				TemplateName:      template.name,
				FieldName:         pathRoot(path),
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				JoinIndex:         joinIndex,
				HasJoinIndex:      true,
				Reason:            "unknown field",
			}
		}
	}
	access, _, err := compilePathAccess(path, template)
	if err != nil {
		validation := &ValidationError{
			RuleName:          ruleName,
			FieldName:         pathRoot(path),
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "invalid path",
			Err:               err,
		}
		if template != nil {
			validation.TemplateName = template.name
		}
		return compiledPathAccess{}, validation
	}
	return access, nil
}

func (c compiledJoinConstraint) matches(fact conditionFactRef, bindings []conditionMatch) (bool, error) {
	if c.refBindingSlot < 0 {
		return false, fmt.Errorf("%w: malformed join binding slot %d", ErrMatcher, c.refBindingSlot)
	}
	if c.refBindingSlot >= len(bindings) {
		return false, nil
	}

	left, ok := c.leftValueFromFact(fact)
	if !ok {
		return false, nil
	}

	rightFact := bindings[c.refBindingSlot].fact
	right, ok := c.rightValueFromFact(rightFact)
	if !ok {
		return false, nil
	}

	switch c.operator {
	case FieldConstraintOpEqual:
		return valuesComparableForEquality(left, right) && left.Equal(right), nil
	case FieldConstraintOpNotEqual:
		return valuesComparableForEquality(left, right) && !left.Equal(right), nil
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if !isNumericValue(left) || !isNumericValue(right) {
			return false, nil
		}
		comparison := compareNumericValues(left, right)
		switch c.operator {
		case FieldConstraintOpLessThan:
			return comparison < 0, nil
		case FieldConstraintOpLessOrEqual:
			return comparison <= 0, nil
		case FieldConstraintOpGreaterThan:
			return comparison > 0, nil
		case FieldConstraintOpGreaterOrEqual:
			return comparison >= 0, nil
		}
	}

	return false, nil
}

func (c compiledJoinConstraint) leftValueFromFact(fact conditionFactRef) (Value, bool) {
	return c.access.valueFromFact(fact)
}

func (c compiledJoinConstraint) leftValueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	return c.access.valueFromFactWithCounters(fact, span)
}

func (c compiledJoinConstraint) rightValueFromFact(fact conditionFactRef) (Value, bool) {
	return c.refAccess.valueFromFact(fact)
}

func (c compiledJoinConstraint) rightValueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	return c.refAccess.valueFromFactWithCounters(fact, span)
}

// rightKeySlotFromFactRef extracts the hash-join key slot for the referenced
// binding's fact directly from slot storage. direct=false means the access
// shape or storage form is not supported and the caller must use the generic
// Value path; direct=true with ok=false means the value is authoritatively
// absent, matching valueFromFact.
func (c compiledJoinConstraint) rightKeySlotFromFactRef(fact *conditionFactRef) (slot betaJoinKeySlot, ok bool, direct bool) {
	return joinKeySlotForAccess(c.refAccess, fact)
}

// leftKeySlotFromFactRef is rightKeySlotFromFactRef for the arriving fact's
// own field access.
func (c compiledJoinConstraint) leftKeySlotFromFactRef(fact *conditionFactRef) (slot betaJoinKeySlot, ok bool, direct bool) {
	return joinKeySlotForAccess(c.access, fact)
}

func joinKeySlotForAccess(access compiledPathAccess, fact *conditionFactRef) (betaJoinKeySlot, bool, bool) {
	if fact == nil || len(access.path.Segments) != 1 {
		return betaJoinKeySlot{}, false, false
	}
	slot := access.rootSlot
	if slot >= 0 && slot < len(fact.fieldSlots) {
		resolved := fact.fieldSlots[slot]
		if !resolved.ok {
			return betaJoinKeySlot{}, false, true
		}
		keySlot, ok := betaJoinKeySlotFromValue(resolved.value)
		if !ok {
			return betaJoinKeySlot{}, false, false
		}
		return keySlot, true, true
	}
	if slot >= 0 && slot < len(fact.compactFieldSlots) {
		compact := fact.compactFieldSlots[slot]
		if !compact.ok {
			return betaJoinKeySlot{}, false, true
		}
		keySlot, ok := betaJoinKeySlotFromCompact(compact)
		if !ok {
			return betaJoinKeySlot{}, false, false
		}
		return keySlot, true, true
	}
	return betaJoinKeySlot{}, false, false
}
