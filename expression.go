package gess

import (
	"fmt"
	"strings"
)

// ExpressionSpec is a deterministic rule predicate expression tree node.
type ExpressionSpec interface {
	expressionSpecNode()
}

type ExpressionComparisonOperator string

const (
	ExpressionCompareUnknown        ExpressionComparisonOperator = ""
	ExpressionCompareEqual          ExpressionComparisonOperator = "eq"
	ExpressionCompareNotEqual       ExpressionComparisonOperator = "neq"
	ExpressionCompareLessThan       ExpressionComparisonOperator = "lt"
	ExpressionCompareLessOrEqual    ExpressionComparisonOperator = "lte"
	ExpressionCompareGreaterThan    ExpressionComparisonOperator = "gt"
	ExpressionCompareGreaterOrEqual ExpressionComparisonOperator = "gte"
)

type ExpressionBooleanOperator string

const (
	ExpressionBoolUnknown ExpressionBooleanOperator = ""
	ExpressionBoolAnd     ExpressionBooleanOperator = "and"
	ExpressionBoolOr      ExpressionBooleanOperator = "or"
	ExpressionBoolNot     ExpressionBooleanOperator = "not"
)

type ConstExpr struct {
	Value any
}

func (ConstExpr) expressionSpecNode() {}

func (s ConstExpr) clone() ConstExpr {
	return ConstExpr{Value: cloneSpecValue(s.Value)}
}

type CurrentFieldExpr struct {
	Field string
}

func (CurrentFieldExpr) expressionSpecNode() {}

func (s CurrentFieldExpr) clone() CurrentFieldExpr {
	s.Field = strings.TrimSpace(s.Field)
	return s
}

type BindingFieldExpr struct {
	Binding string
	Field   string
}

func (BindingFieldExpr) expressionSpecNode() {}

func (s BindingFieldExpr) clone() BindingFieldExpr {
	s.Binding = strings.TrimSpace(s.Binding)
	s.Field = strings.TrimSpace(s.Field)
	return s
}

type CompareExpr struct {
	Operator ExpressionComparisonOperator
	Left     ExpressionSpec
	Right    ExpressionSpec
}

func (CompareExpr) expressionSpecNode() {}

func (s CompareExpr) clone() CompareExpr {
	s.Left = cloneExpressionSpec(s.Left)
	s.Right = cloneExpressionSpec(s.Right)
	return s
}

type BooleanExpr struct {
	Operator ExpressionBooleanOperator
	Operands []ExpressionSpec
}

func (BooleanExpr) expressionSpecNode() {}

func (s BooleanExpr) clone() BooleanExpr {
	operands := s.Operands
	s.Operands = make([]ExpressionSpec, len(operands))
	for i, operand := range operands {
		s.Operands[i] = cloneExpressionSpec(operand)
	}
	return s
}

func cloneExpressionSpec(spec ExpressionSpec) ExpressionSpec {
	switch expression := spec.(type) {
	case nil:
		return nil
	case ConstExpr:
		return expression.clone()
	case *ConstExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case CurrentFieldExpr:
		return expression.clone()
	case *CurrentFieldExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case BindingFieldExpr:
		return expression.clone()
	case *BindingFieldExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case CompareExpr:
		return expression.clone()
	case *CompareExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case BooleanExpr:
		return expression.clone()
	case *BooleanExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	default:
		return spec
	}
}

type ExpressionPredicatePlacement string

const (
	ExpressionPredicatePlacementUnknown      ExpressionPredicatePlacement = ""
	ExpressionPredicatePlacementAlpha        ExpressionPredicatePlacement = "alpha"
	ExpressionPredicatePlacementBetaResidual ExpressionPredicatePlacement = "beta-residual"
	ExpressionPredicatePlacementUnsupported  ExpressionPredicatePlacement = "unsupported"
)

type ExpressionPredicate struct {
	expression ExpressionSpec
	placement  ExpressionPredicatePlacement
	order      int
}

func (p ExpressionPredicate) Expression() ExpressionSpec {
	return cloneExpressionSpec(p.expression)
}

func (p ExpressionPredicate) Placement() ExpressionPredicatePlacement {
	return p.placement
}

func (p ExpressionPredicate) DeclarationOrder() int {
	return p.order
}

func (p ExpressionPredicate) clone() ExpressionPredicate {
	p.expression = cloneExpressionSpec(p.expression)
	return p
}

type expressionNodeKind string

const (
	expressionNodeConst        expressionNodeKind = "const"
	expressionNodeCurrentField expressionNodeKind = "current-field"
	expressionNodeBindingField expressionNodeKind = "binding-field"
	expressionNodeCompare      expressionNodeKind = "compare"
	expressionNodeBoolean      expressionNodeKind = "boolean"
)

type compiledExpressionPredicate struct {
	path       []int
	expression compiledExpression
	placement  ExpressionPredicatePlacement
	order      int
}

type compiledExpression struct {
	kind        expressionNodeKind
	resultKind  ValueKind
	value       Value
	field       string
	fieldSlot   int
	binding     string
	bindingSlot int
	compareOp   ExpressionComparisonOperator
	boolOp      ExpressionBooleanOperator
	operands    []compiledExpression
}

func compileExpressionPredicateSpec(
	spec ExpressionSpec,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
) (ExpressionPredicate, compiledExpressionPredicate, error) {
	if spec == nil {
		return ExpressionPredicate{}, compiledExpressionPredicate{}, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression predicate is required", nil)
	}
	expression, referencesEarlierBinding, err := compileExpressionSpec(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	if err != nil {
		return ExpressionPredicate{}, compiledExpressionPredicate{}, err
	}
	if expression.resultKind != ValueBool {
		return ExpressionPredicate{}, compiledExpressionPredicate{}, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression predicate must produce a bool", nil)
	}

	placement := ExpressionPredicatePlacementAlpha
	if referencesEarlierBinding {
		placement = ExpressionPredicatePlacementBetaResidual
	}
	return ExpressionPredicate{
			expression: cloneExpressionSpec(spec),
			placement:  placement,
			order:      predicateIndex,
		}, compiledExpressionPredicate{
			path:       []int{conditionIndex, predicateIndex},
			expression: expression,
			placement:  placement,
			order:      predicateIndex,
		}, nil
}

func compileExpressionSpec(
	spec ExpressionSpec,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
) (compiledExpression, bool, error) {
	switch expression := spec.(type) {
	case ConstExpr:
		return compileConstExpression(expression, ruleName, conditionIndex, predicateIndex)
	case *ConstExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileConstExpression(*expression, ruleName, conditionIndex, predicateIndex)
	case CurrentFieldExpr:
		return compileCurrentFieldExpression(expression, ruleName, conditionIndex, predicateIndex, template)
	case *CurrentFieldExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileCurrentFieldExpression(*expression, ruleName, conditionIndex, predicateIndex, template)
	case BindingFieldExpr:
		return compileBindingFieldExpression(expression, ruleName, conditionIndex, predicateIndex, conditions, bindingSlots, templatesByKey)
	case *BindingFieldExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileBindingFieldExpression(*expression, ruleName, conditionIndex, predicateIndex, conditions, bindingSlots, templatesByKey)
	case CompareExpr:
		return compileCompareExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	case *CompareExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileCompareExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	case BooleanExpr:
		return compileBooleanExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	case *BooleanExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileBooleanExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	default:
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "unsupported expression node", nil)
	}
}

func compileConstExpression(spec ConstExpr, ruleName string, conditionIndex, predicateIndex int) (compiledExpression, bool, error) {
	value, err := canonicalValue(spec.Value)
	if err != nil {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "invalid expression constant", err)
	}
	return compiledExpression{
		kind:       expressionNodeConst,
		resultKind: value.Kind(),
		value:      value,
		fieldSlot:  -1,
	}, false, nil
}

func compileCurrentFieldExpression(spec CurrentFieldExpr, ruleName string, conditionIndex, predicateIndex int, template *Template) (compiledExpression, bool, error) {
	normalized := spec.clone()
	if normalized.Field == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "current field expression requires a field", nil)
	}
	fieldSlot, kind, err := compileExpressionFieldRef(ruleName, conditionIndex, predicateIndex, template, normalized.Field)
	if err != nil {
		return compiledExpression{}, false, err
	}
	return compiledExpression{
		kind:       expressionNodeCurrentField,
		resultKind: kind,
		field:      normalized.Field,
		fieldSlot:  fieldSlot,
	}, false, nil
}

func compileBindingFieldExpression(
	spec BindingFieldExpr,
	ruleName string,
	conditionIndex, predicateIndex int,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
) (compiledExpression, bool, error) {
	normalized := spec.clone()
	if normalized.Binding == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding field expression requires a binding", nil)
	}
	if normalized.Field == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding field expression requires a field", nil)
	}
	refSlot, ok := bindingSlots[normalized.Binding]
	if !ok {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding field expression must refer to an earlier condition", nil)
	}
	if refSlot < 0 || refSlot >= len(conditions) {
		return compiledExpression{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, refSlot)
	}

	refCondition := conditions[refSlot]
	fieldSlot := -1
	kind := ValueAny
	if refCondition.templateKey != "" {
		refTemplate, ok := templatesByKey[refCondition.templateKey]
		if !ok {
			return compiledExpression{}, false, fmt.Errorf("%w: missing template for expression binding %q", ErrMatcher, normalized.Binding)
		}
		var err error
		fieldSlot, kind, err = compileExpressionFieldRef(ruleName, conditionIndex, predicateIndex, &refTemplate, normalized.Field)
		if err != nil {
			return compiledExpression{}, false, err
		}
	}

	return compiledExpression{
		kind:        expressionNodeBindingField,
		resultKind:  kind,
		field:       normalized.Field,
		fieldSlot:   fieldSlot,
		binding:     normalized.Binding,
		bindingSlot: refSlot,
	}, true, nil
}

func compileCompareExpression(
	spec CompareExpr,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
) (compiledExpression, bool, error) {
	if !validExpressionComparisonOperator(spec.Operator) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "invalid expression comparison operator", nil)
	}
	if spec.Left == nil || spec.Right == nil {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "comparison expression requires left and right operands", nil)
	}
	left, leftReferencesEarlier, err := compileExpressionSpec(spec.Left, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	if err != nil {
		return compiledExpression{}, false, err
	}
	right, rightReferencesEarlier, err := compileExpressionSpec(spec.Right, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
	if err != nil {
		return compiledExpression{}, false, err
	}
	if !expressionOperandsComparable(spec.Operator, left.resultKind, right.resultKind) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression operands have incompatible types", nil)
	}
	return compiledExpression{
		kind:       expressionNodeCompare,
		resultKind: ValueBool,
		compareOp:  spec.Operator,
		fieldSlot:  -1,
		operands:   []compiledExpression{left, right},
	}, leftReferencesEarlier || rightReferencesEarlier, nil
}

func compileBooleanExpression(
	spec BooleanExpr,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
) (compiledExpression, bool, error) {
	if !validExpressionBooleanOperator(spec.Operator) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "invalid expression boolean operator", nil)
	}
	if spec.Operator == ExpressionBoolNot && len(spec.Operands) != 1 {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "not expression requires exactly one operand", nil)
	}
	if spec.Operator != ExpressionBoolNot && len(spec.Operands) == 0 {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "boolean expression requires at least one operand", nil)
	}

	operands := make([]compiledExpression, 0, len(spec.Operands))
	referencesEarlier := false
	for _, operandSpec := range spec.Operands {
		if operandSpec == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "boolean expression operand is required", nil)
		}
		operand, operandReferencesEarlier, err := compileExpressionSpec(operandSpec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey)
		if err != nil {
			return compiledExpression{}, false, err
		}
		if operand.resultKind != ValueAny && operand.resultKind != ValueBool {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "boolean expression operands must produce bool values", nil)
		}
		operands = append(operands, operand)
		referencesEarlier = referencesEarlier || operandReferencesEarlier
	}

	return compiledExpression{
		kind:       expressionNodeBoolean,
		resultKind: ValueBool,
		boolOp:     spec.Operator,
		fieldSlot:  -1,
		operands:   operands,
	}, referencesEarlier, nil
}

func compileExpressionFieldRef(ruleName string, conditionIndex, predicateIndex int, template *Template, field string) (int, ValueKind, error) {
	if template == nil || !template.closed {
		return -1, ValueAny, nil
	}
	slot, ok := template.fieldSlot(field)
	if !ok {
		return -1, "", expressionValidationError(ruleName, conditionIndex, predicateIndex, field, "unknown field", nil)
	}
	kind := ValueAny
	if spec, ok := template.fieldsByName[field]; ok {
		kind = spec.Kind
	}
	return slot, kind, nil
}

func validExpressionComparisonOperator(operator ExpressionComparisonOperator) bool {
	switch operator {
	case ExpressionCompareEqual, ExpressionCompareNotEqual, ExpressionCompareLessThan,
		ExpressionCompareLessOrEqual, ExpressionCompareGreaterThan, ExpressionCompareGreaterOrEqual:
		return true
	default:
		return false
	}
}

func validExpressionBooleanOperator(operator ExpressionBooleanOperator) bool {
	switch operator {
	case ExpressionBoolAnd, ExpressionBoolOr, ExpressionBoolNot:
		return true
	default:
		return false
	}
}

func expressionOperandsComparable(operator ExpressionComparisonOperator, left, right ValueKind) bool {
	if left == ValueAny || right == ValueAny {
		return true
	}
	switch operator {
	case ExpressionCompareEqual, ExpressionCompareNotEqual:
		return left == right || expressionKindsNumeric(left, right)
	case ExpressionCompareLessThan, ExpressionCompareLessOrEqual, ExpressionCompareGreaterThan, ExpressionCompareGreaterOrEqual:
		return expressionKindsNumeric(left, right) || (left == ValueString && right == ValueString)
	default:
		return false
	}
}

func expressionKindsNumeric(left, right ValueKind) bool {
	switch left {
	case ValueInt, ValueFloat:
		switch right {
		case ValueInt, ValueFloat:
			return true
		}
	}
	return false
}

func expressionValidationError(ruleName string, conditionIndex, predicateIndex int, fieldName, reason string, err error) *ValidationError {
	return &ValidationError{
		RuleName:          ruleName,
		FieldName:         fieldName,
		ConditionIndex:    conditionIndex,
		HasConditionIndex: true,
		PredicateIndex:    predicateIndex,
		HasPredicateIndex: true,
		Reason:            reason,
		Err:               err,
	}
}

func cloneExpressionPredicates(in []ExpressionPredicate) []ExpressionPredicate {
	if len(in) == 0 {
		return nil
	}
	out := make([]ExpressionPredicate, len(in))
	for i, predicate := range in {
		out[i] = predicate.clone()
	}
	return out
}

func cloneCompiledExpressionPredicates(in []compiledExpressionPredicate) []compiledExpressionPredicate {
	if len(in) == 0 {
		return nil
	}
	out := make([]compiledExpressionPredicate, len(in))
	for i, predicate := range in {
		out[i] = predicate
		out[i].path = cloneIntPath(predicate.path)
		out[i].expression = predicate.expression.clone()
	}
	return out
}

func (e compiledExpression) clone() compiledExpression {
	e.value = cloneValue(e.value)
	e.operands = make([]compiledExpression, len(e.operands))
	for i, operand := range e.operands {
		e.operands[i] = operand.clone()
	}
	return e
}

func serializeCompiledExpressionPredicates(predicates []compiledExpressionPredicate) string {
	if len(predicates) == 0 {
		return ""
	}
	var b strings.Builder
	for _, predicate := range predicates {
		b.WriteString("predicate:")
		b.WriteString(fmt.Sprint(predicate.order))
		b.WriteString(":")
		b.WriteString(string(predicate.placement))
		b.WriteString(":")
		b.WriteString(serializeCompiledExpression(predicate.expression))
		b.WriteString(";")
	}
	return b.String()
}

func serializeCompiledExpression(expression compiledExpression) string {
	var b strings.Builder
	b.WriteString(string(expression.kind))
	b.WriteString("{kind=")
	b.WriteString(string(expression.resultKind))
	switch expression.kind {
	case expressionNodeConst:
		b.WriteString(",value=")
		b.WriteString(expression.value.canonicalKey())
	case expressionNodeCurrentField:
		b.WriteString(",field=")
		b.WriteString(expression.field)
		b.WriteString(",field-slot=")
		b.WriteString(fmt.Sprint(expression.fieldSlot))
	case expressionNodeBindingField:
		b.WriteString(",binding=")
		b.WriteString(expression.binding)
		b.WriteString(",binding-slot=")
		b.WriteString(fmt.Sprint(expression.bindingSlot))
		b.WriteString(",field=")
		b.WriteString(expression.field)
		b.WriteString(",field-slot=")
		b.WriteString(fmt.Sprint(expression.fieldSlot))
	case expressionNodeCompare:
		b.WriteString(",op=")
		b.WriteString(string(expression.compareOp))
	case expressionNodeBoolean:
		b.WriteString(",op=")
		b.WriteString(string(expression.boolOp))
	}
	if len(expression.operands) > 0 {
		b.WriteString(",operands=[")
		for i, operand := range expression.operands {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(serializeCompiledExpression(operand))
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	return b.String()
}
