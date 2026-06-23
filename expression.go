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
	Path  PathSpec
}

func (CurrentFieldExpr) expressionSpecNode() {}

func (s CurrentFieldExpr) clone() CurrentFieldExpr {
	s.Field = strings.TrimSpace(s.Field)
	s.Path = pathOrField(s.Path, s.Field)
	s.Field = s.Path.root()
	return s
}

type BindingFieldExpr struct {
	Binding string
	Field   string
	Path    PathSpec
}

func (BindingFieldExpr) expressionSpecNode() {}

func (s BindingFieldExpr) clone() BindingFieldExpr {
	s.Binding = strings.TrimSpace(s.Binding)
	s.Field = strings.TrimSpace(s.Field)
	s.Path = pathOrField(s.Path, s.Field)
	s.Field = s.Path.root()
	return s
}

type HasPathExpr struct {
	Path PathSpec
}

func (HasPathExpr) expressionSpecNode() {}

func (s HasPathExpr) clone() HasPathExpr {
	s.Path = s.Path.clone()
	return s
}

func CurrentPath(path PathSpec) CurrentFieldExpr {
	return CurrentFieldExpr{Path: path.clone()}
}

func BindingPath(binding string, path PathSpec) BindingFieldExpr {
	return BindingFieldExpr{Binding: binding, Path: path.clone()}
}

func HasPath(path PathSpec) HasPathExpr {
	return HasPathExpr{Path: path.clone()}
}

type BindingValueExpr struct {
	Binding string
}

func (BindingValueExpr) expressionSpecNode() {}

func (s BindingValueExpr) clone() BindingValueExpr {
	s.Binding = strings.TrimSpace(s.Binding)
	return s
}

// ParamExpr references a named query parameter. It is valid only inside query
// predicates and query return expressions.
type ParamExpr struct {
	Name string
}

func (ParamExpr) expressionSpecNode() {}

func (s ParamExpr) clone() ParamExpr {
	s.Name = strings.TrimSpace(s.Name)
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
	case HasPathExpr:
		return expression.clone()
	case *HasPathExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case BindingValueExpr:
		return expression.clone()
	case *BindingValueExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case ParamExpr:
		return expression.clone()
	case *ParamExpr:
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
	expressionNodeBindingValue expressionNodeKind = "binding-value"
	expressionNodeHasPath      expressionNodeKind = "has-path"
	expressionNodeParam        expressionNodeKind = "param"
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
	access      compiledPathAccess
	binding     string
	bindingSlot int
	paramName   string
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
	return compileExpressionPredicateSpecWithParams(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, nil)
}

func compileExpressionPredicateSpecWithParams(
	spec ExpressionSpec,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
	params map[string]ValueKind,
) (ExpressionPredicate, compiledExpressionPredicate, error) {
	if spec == nil {
		return ExpressionPredicate{}, compiledExpressionPredicate{}, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression predicate is required", nil)
	}
	expression, referencesEarlierBinding, err := compileExpressionSpecWithParams(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
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
	return compileExpressionSpecWithParams(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, nil)
}

func compileExpressionSpecWithParams(
	spec ExpressionSpec,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
	params map[string]ValueKind,
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
	case HasPathExpr:
		return compileHasPathExpression(expression, ruleName, conditionIndex, predicateIndex, template)
	case *HasPathExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileHasPathExpression(*expression, ruleName, conditionIndex, predicateIndex, template)
	case BindingValueExpr:
		return compileBindingValueExpression(expression, ruleName, conditionIndex, predicateIndex, conditions, bindingSlots)
	case *BindingValueExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileBindingValueExpression(*expression, ruleName, conditionIndex, predicateIndex, conditions, bindingSlots)
	case ParamExpr:
		return compileParamExpression(expression, ruleName, conditionIndex, predicateIndex, params)
	case *ParamExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileParamExpression(*expression, ruleName, conditionIndex, predicateIndex, params)
	case CompareExpr:
		return compileCompareExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
	case *CompareExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileCompareExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
	case BooleanExpr:
		return compileBooleanExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
	case *BooleanExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileBooleanExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
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
	if normalized.Path.isZero() {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "current path expression requires a path", ErrInvalidPath)
	}
	access, kind, err := compileExpressionPathRef(ruleName, conditionIndex, predicateIndex, template, normalized.Path)
	if err != nil {
		return compiledExpression{}, false, err
	}
	return compiledExpression{
		kind:       expressionNodeCurrentField,
		resultKind: kind,
		field:      access.root,
		fieldSlot:  access.rootSlot,
		access:     access,
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
	if normalized.Path.isZero() {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding path expression requires a path", ErrInvalidPath)
	}
	refSlot, ok := bindingSlots[normalized.Binding]
	if !ok {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding field expression must refer to an earlier condition", nil)
	}
	if refSlot < 0 || refSlot >= len(conditions) {
		return compiledExpression{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, refSlot)
	}

	refCondition := conditions[refSlot]
	access := compiledPathAccess{path: normalized.Path.clone(), root: normalized.Path.root(), rootSlot: -1}
	kind := ValueAny
	if refCondition.templateKey != "" {
		refTemplate, ok := templatesByKey[refCondition.templateKey]
		if !ok {
			return compiledExpression{}, false, fmt.Errorf("%w: missing template for expression binding %q", ErrMatcher, normalized.Binding)
		}
		var err error
		access, kind, err = compileExpressionPathRef(ruleName, conditionIndex, predicateIndex, &refTemplate, normalized.Path)
		if err != nil {
			return compiledExpression{}, false, err
		}
	} else if err := normalized.Path.validate(); err != nil {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, normalized.Path.root(), "invalid path", err)
	}

	return compiledExpression{
		kind:        expressionNodeBindingField,
		resultKind:  kind,
		field:       access.root,
		fieldSlot:   access.rootSlot,
		access:      access,
		binding:     normalized.Binding,
		bindingSlot: refSlot,
	}, true, nil
}

func compileHasPathExpression(spec HasPathExpr, ruleName string, conditionIndex, predicateIndex int, template *Template) (compiledExpression, bool, error) {
	normalized := spec.clone()
	access, _, err := compileExpressionPathRef(ruleName, conditionIndex, predicateIndex, template, normalized.Path)
	if err != nil {
		return compiledExpression{}, false, err
	}
	return compiledExpression{
		kind:       expressionNodeHasPath,
		resultKind: ValueBool,
		field:      access.root,
		fieldSlot:  access.rootSlot,
		access:     access,
	}, false, nil
}

func compileBindingValueExpression(
	spec BindingValueExpr,
	ruleName string,
	conditionIndex, predicateIndex int,
	conditions []RuleCondition,
	bindingSlots map[string]int,
) (compiledExpression, bool, error) {
	normalized := spec.clone()
	if normalized.Binding == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding value expression requires a binding", nil)
	}
	refSlot, ok := bindingSlots[normalized.Binding]
	if !ok {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding value expression must refer to an earlier condition", nil)
	}
	if refSlot < 0 || refSlot >= len(conditions) {
		return compiledExpression{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, refSlot)
	}
	return compiledExpression{
		kind:        expressionNodeBindingValue,
		resultKind:  ValueAny,
		binding:     normalized.Binding,
		bindingSlot: refSlot,
		fieldSlot:   -1,
	}, true, nil
}

func compileParamExpression(spec ParamExpr, ruleName string, conditionIndex, predicateIndex int, params map[string]ValueKind) (compiledExpression, bool, error) {
	normalized := spec.clone()
	if normalized.Name == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "query parameter expression requires a name", nil)
	}
	if params == nil {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "query parameter expression is only supported in queries", ErrQueryValidation)
	}
	kind, ok := params[normalized.Name]
	if !ok {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "unknown query parameter", ErrQueryValidation)
	}
	if kind == "" {
		kind = ValueAny
	}
	return compiledExpression{
		kind:       expressionNodeParam,
		resultKind: kind,
		paramName:  normalized.Name,
		fieldSlot:  -1,
	}, false, nil
}

func compileCompareExpression(
	spec CompareExpr,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
	params map[string]ValueKind,
) (compiledExpression, bool, error) {
	if !validExpressionComparisonOperator(spec.Operator) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "invalid expression comparison operator", nil)
	}
	if spec.Left == nil || spec.Right == nil {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "comparison expression requires left and right operands", nil)
	}
	left, leftReferencesEarlier, err := compileExpressionSpecWithParams(spec.Left, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
	if err != nil {
		return compiledExpression{}, false, err
	}
	right, rightReferencesEarlier, err := compileExpressionSpecWithParams(spec.Right, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
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
	params map[string]ValueKind,
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
		operand, operandReferencesEarlier, err := compileExpressionSpecWithParams(operandSpec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params)
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

func compileExpressionPathRef(ruleName string, conditionIndex, predicateIndex int, template *Template, path PathSpec) (compiledPathAccess, ValueKind, error) {
	if template != nil && template.closed && path.root() != "" {
		if _, ok := template.fieldSlot(path.root()); !ok {
			return compiledPathAccess{}, "", expressionValidationError(ruleName, conditionIndex, predicateIndex, path.root(), "unknown field", nil)
		}
	}
	access, kind, err := compilePathAccess(path, template)
	if err != nil {
		return compiledPathAccess{}, "", expressionValidationError(ruleName, conditionIndex, predicateIndex, path.root(), "invalid path", err)
	}
	return access, kind, nil
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

func (p compiledExpressionPredicate) graphExecutable() bool {
	switch p.placement {
	case ExpressionPredicatePlacementAlpha, ExpressionPredicatePlacementBetaResidual:
	default:
		return false
	}
	if !p.expression.graphExecutable() {
		return false
	}
	referencesBinding := p.expression.referencesBinding()
	switch p.placement {
	case ExpressionPredicatePlacementAlpha:
		return !referencesBinding
	case ExpressionPredicatePlacementBetaResidual:
		return referencesBinding
	default:
		return false
	}
}

func (e compiledExpression) graphExecutable() bool {
	switch e.kind {
	case expressionNodeConst:
		return e.resultKind != ""
	case expressionNodeCurrentField:
		return (e.access.root != "" || e.field != "") && e.resultKind != ""
	case expressionNodeBindingField:
		return e.binding != "" && (e.access.root != "" || e.field != "") && e.bindingSlot >= 0 && e.resultKind != ""
	case expressionNodeBindingValue:
		return e.binding != "" && e.bindingSlot >= 0 && e.resultKind != ""
	case expressionNodeHasPath:
		return e.access.root != "" && e.resultKind == ValueBool
	case expressionNodeParam:
		return e.paramName != "" && e.resultKind != ""
	case expressionNodeCompare:
		if !validExpressionComparisonOperator(e.compareOp) || len(e.operands) != 2 || e.resultKind != ValueBool {
			return false
		}
		if !expressionOperandsComparable(e.compareOp, e.operands[0].resultKind, e.operands[1].resultKind) {
			return false
		}
	case expressionNodeBoolean:
		if !validExpressionBooleanOperator(e.boolOp) || e.resultKind != ValueBool {
			return false
		}
		if e.boolOp == ExpressionBoolNot && len(e.operands) != 1 {
			return false
		}
		if e.boolOp != ExpressionBoolNot && len(e.operands) == 0 {
			return false
		}
		for _, operand := range e.operands {
			if operand.resultKind != ValueAny && operand.resultKind != ValueBool {
				return false
			}
		}
	default:
		return false
	}
	for _, operand := range e.operands {
		if !operand.graphExecutable() {
			return false
		}
	}
	return true
}

func (e compiledExpression) referencesBinding() bool {
	switch e.kind {
	case expressionNodeBindingField:
		return true
	case expressionNodeBindingValue:
		return true
	default:
		for _, operand := range e.operands {
			if operand.referencesBinding() {
				return true
			}
		}
		return false
	}
}

func expressionPredicatesMatch(predicates []compiledExpressionPredicate, fact conditionFactRef, bindings []conditionMatch) (bool, error) {
	for _, predicate := range predicates {
		ok, err := predicate.matches(fact, bindings)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func expressionPredicatesMatchToken(predicates []compiledExpressionPredicate, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	for _, predicate := range predicates {
		if span != nil {
			span.recordExpressionPredicateTest()
		}
		ok, err := predicate.matchesToken(fact, bindings)
		if err != nil {
			if span != nil {
				span.recordExpressionPredicateError()
			}
			return false, err
		}
		if !ok {
			if span != nil {
				span.recordExpressionPredicateFailure()
			}
			return false, nil
		}
	}
	return true, nil
}

func (p compiledExpressionPredicate) matches(fact conditionFactRef, bindings []conditionMatch) (bool, error) {
	return p.matchesWithParams(fact, bindings, nil)
}

func (p compiledExpressionPredicate) matchesWithParams(fact conditionFactRef, bindings []conditionMatch, params map[string]Value) (bool, error) {
	value, ok, err := p.expression.evaluateWithParams(fact, bindings, params)
	if err != nil || !ok {
		return false, err
	}
	if value.Kind() != ValueBool {
		return false, nil
	}
	return value.boolValue, nil
}

func (p compiledExpressionPredicate) matchesToken(fact conditionFactRef, bindings tokenRef) (bool, error) {
	value, ok, err := p.expression.evaluateToken(fact, bindings)
	if err != nil || !ok {
		return false, err
	}
	if value.Kind() != ValueBool {
		return false, nil
	}
	return value.boolValue, nil
}

func (e compiledExpression) evaluate(fact conditionFactRef, bindings []conditionMatch) (Value, bool, error) {
	return e.evaluateWithParams(fact, bindings, nil)
}

func (e compiledExpression) evaluateWithParams(fact conditionFactRef, bindings []conditionMatch, params map[string]Value) (Value, bool, error) {
	switch e.kind {
	case expressionNodeConst:
		return e.value, true, nil
	case expressionNodeCurrentField:
		value, ok := e.currentValueFromFact(fact)
		return value, ok, nil
	case expressionNodeBindingField:
		if e.bindingSlot < 0 {
			return Value{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, e.bindingSlot)
		}
		if e.bindingSlot >= len(bindings) {
			return Value{}, false, nil
		}
		if bindings[e.bindingSlot].hasValue {
			return Value{}, false, nil
		}
		value, ok := e.bindingValueFromFact(bindings[e.bindingSlot].fact)
		return value, ok, nil
	case expressionNodeBindingValue:
		if e.bindingSlot < 0 {
			return Value{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, e.bindingSlot)
		}
		if e.bindingSlot >= len(bindings) || !bindings[e.bindingSlot].hasValue {
			return Value{}, false, nil
		}
		return bindings[e.bindingSlot].value, true, nil
	case expressionNodeHasPath:
		_, ok := e.access.valueFromFact(fact)
		return newBoolValue(ok), true, nil
	case expressionNodeParam:
		if e.paramName == "" {
			return Value{}, false, fmt.Errorf("%w: malformed query parameter expression", ErrMatcher)
		}
		value, ok := params[e.paramName]
		if !ok {
			return Value{}, false, fmt.Errorf("%w: missing query argument %q", ErrQueryArgument, e.paramName)
		}
		return value, true, nil
	case expressionNodeCompare:
		return e.evaluateCompare(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateWithParams(fact, bindings, params)
		})
	case expressionNodeBoolean:
		return e.evaluateBoolean(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateWithParams(fact, bindings, params)
		})
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported expression node %q", ErrMatcher, e.kind)
	}
}

func (e compiledExpression) evaluateToken(fact conditionFactRef, bindings tokenRef) (Value, bool, error) {
	return e.evaluateTokenWithParamsAndOffset(fact, bindings, nil, 0)
}

func (e compiledExpression) evaluateTokenWithParams(fact conditionFactRef, bindings tokenRef, params map[string]Value) (Value, bool, error) {
	return e.evaluateTokenWithParamsAndOffset(fact, bindings, params, 0)
}

func (e compiledExpression) evaluateTokenWithParamsAndOffset(fact conditionFactRef, bindings tokenRef, params map[string]Value, bindingSlotOffset int) (Value, bool, error) {
	switch e.kind {
	case expressionNodeConst:
		return e.value, true, nil
	case expressionNodeCurrentField:
		value, ok := e.currentValueFromFact(fact)
		return value, ok, nil
	case expressionNodeBindingField:
		if e.bindingSlot < 0 {
			return Value{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, e.bindingSlot)
		}
		match, ok := tokenRefAtSlot(bindings, e.bindingSlot+bindingSlotOffset)
		if !ok {
			return Value{}, false, nil
		}
		if match.hasValue {
			return Value{}, false, nil
		}
		value, ok := e.bindingValueFromFact(match.fact)
		return value, ok, nil
	case expressionNodeBindingValue:
		if e.bindingSlot < 0 {
			return Value{}, false, fmt.Errorf("%w: malformed expression binding slot %d", ErrMatcher, e.bindingSlot)
		}
		match, ok := tokenRefAtSlot(bindings, e.bindingSlot+bindingSlotOffset)
		if !ok || !match.hasValue {
			return Value{}, false, nil
		}
		return match.value, true, nil
	case expressionNodeHasPath:
		_, ok := e.access.valueFromFact(fact)
		return newBoolValue(ok), true, nil
	case expressionNodeParam:
		if e.paramName == "" {
			return Value{}, false, fmt.Errorf("%w: malformed query parameter expression", ErrMatcher)
		}
		value, ok := params[e.paramName]
		if !ok {
			return Value{}, false, fmt.Errorf("%w: missing query argument %q", ErrQueryArgument, e.paramName)
		}
		return value, true, nil
	case expressionNodeCompare:
		return e.evaluateCompare(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateTokenWithParamsAndOffset(fact, bindings, params, bindingSlotOffset)
		})
	case expressionNodeBoolean:
		return e.evaluateBoolean(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateTokenWithParamsAndOffset(fact, bindings, params, bindingSlotOffset)
		})
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported expression node %q", ErrMatcher, e.kind)
	}
}

func (e compiledExpression) currentValueFromFact(fact conditionFactRef) (Value, bool) {
	if !e.access.path.isZero() {
		return e.access.valueFromFact(fact)
	}
	return fact.compiledFieldValue(e.field, e.fieldSlot)
}

func (e compiledExpression) bindingValueFromFact(fact conditionFactRef) (Value, bool) {
	if !e.access.path.isZero() {
		return e.access.valueFromFact(fact)
	}
	return fact.compiledFieldValue(e.field, e.fieldSlot)
}

func (e compiledExpression) evaluateCompare(eval func(compiledExpression) (Value, bool, error)) (Value, bool, error) {
	if len(e.operands) != 2 {
		return Value{}, false, fmt.Errorf("%w: malformed comparison expression operand count %d", ErrMatcher, len(e.operands))
	}
	left, leftOK, err := eval(e.operands[0])
	if err != nil {
		return Value{}, false, err
	}
	right, rightOK, err := eval(e.operands[1])
	if err != nil {
		return Value{}, false, err
	}
	if !leftOK || !rightOK {
		return newBoolValue(false), true, nil
	}
	var matched bool
	switch e.compareOp {
	case ExpressionCompareEqual:
		matched = valuesComparableForEquality(left, right) && left.Equal(right)
	case ExpressionCompareNotEqual:
		matched = valuesComparableForEquality(left, right) && !left.Equal(right)
	case ExpressionCompareLessThan, ExpressionCompareLessOrEqual, ExpressionCompareGreaterThan, ExpressionCompareGreaterOrEqual:
		comparison, comparable := compareValues(left, right)
		if !comparable {
			return newBoolValue(false), true, nil
		}
		switch e.compareOp {
		case ExpressionCompareLessThan:
			matched = comparison < 0
		case ExpressionCompareLessOrEqual:
			matched = comparison <= 0
		case ExpressionCompareGreaterThan:
			matched = comparison > 0
		case ExpressionCompareGreaterOrEqual:
			matched = comparison >= 0
		}
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported expression comparison operator %q", ErrMatcher, e.compareOp)
	}
	return newBoolValue(matched), true, nil
}

func (e compiledExpression) evaluateBoolean(eval func(compiledExpression) (Value, bool, error)) (Value, bool, error) {
	boolValue := func(operand compiledExpression) (bool, error) {
		value, ok, err := eval(operand)
		if err != nil || !ok || value.Kind() != ValueBool {
			return false, err
		}
		return value.boolValue, nil
	}

	switch e.boolOp {
	case ExpressionBoolAnd:
		if len(e.operands) == 0 {
			return Value{}, false, fmt.Errorf("%w: malformed and expression operand count 0", ErrMatcher)
		}
		for _, operand := range e.operands {
			value, err := boolValue(operand)
			if err != nil {
				return Value{}, false, err
			}
			if !value {
				return newBoolValue(false), true, nil
			}
		}
		return newBoolValue(true), true, nil
	case ExpressionBoolOr:
		if len(e.operands) == 0 {
			return Value{}, false, fmt.Errorf("%w: malformed or expression operand count 0", ErrMatcher)
		}
		for _, operand := range e.operands {
			value, err := boolValue(operand)
			if err != nil {
				return Value{}, false, err
			}
			if value {
				return newBoolValue(true), true, nil
			}
		}
		return newBoolValue(false), true, nil
	case ExpressionBoolNot:
		if len(e.operands) != 1 {
			return Value{}, false, fmt.Errorf("%w: malformed not expression operand count %d", ErrMatcher, len(e.operands))
		}
		value, err := boolValue(e.operands[0])
		if err != nil {
			return Value{}, false, err
		}
		return newBoolValue(!value), true, nil
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported expression boolean operator %q", ErrMatcher, e.boolOp)
	}
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
	e.access = e.access.clone()
	operands := e.operands
	e.operands = make([]compiledExpression, len(operands))
	for i, operand := range operands {
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
		b.WriteString(",path=")
		b.WriteString(expression.access.display())
	case expressionNodeBindingField:
		b.WriteString(",binding=")
		b.WriteString(expression.binding)
		b.WriteString(",binding-slot=")
		b.WriteString(fmt.Sprint(expression.bindingSlot))
		b.WriteString(",field=")
		b.WriteString(expression.field)
		b.WriteString(",field-slot=")
		b.WriteString(fmt.Sprint(expression.fieldSlot))
		b.WriteString(",path=")
		b.WriteString(expression.access.display())
	case expressionNodeBindingValue:
		b.WriteString(",binding=")
		b.WriteString(expression.binding)
		b.WriteString(",binding-slot=")
		b.WriteString(fmt.Sprint(expression.bindingSlot))
	case expressionNodeParam:
		b.WriteString(",param=")
		b.WriteString(expression.paramName)
	case expressionNodeHasPath:
		b.WriteString(",path=")
		b.WriteString(expression.access.display())
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
