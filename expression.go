package gess

import (
	"context"
	"errors"
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
	s.Path = s.Path.clone()
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
	s.Path = s.Path.clone()
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

type CallExpr struct {
	Name string
	Args []ExpressionSpec
}

func (CallExpr) expressionSpecNode() {}

func Call(name string, args ...ExpressionSpec) CallExpr {
	out := CallExpr{Name: strings.TrimSpace(name), Args: make([]ExpressionSpec, len(args))}
	for i, arg := range args {
		out.Args[i] = cloneExpressionSpec(arg)
	}
	return out
}

func (s CallExpr) clone() CallExpr {
	s.Name = strings.TrimSpace(s.Name)
	args := s.Args
	s.Args = make([]ExpressionSpec, len(args))
	for i, arg := range args {
		s.Args[i] = cloneExpressionSpec(arg)
	}
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
	case CallExpr:
		return expression.clone()
	case *CallExpr:
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
	expressionNodeCall         expressionNodeKind = "call"
	expressionNodeCompare      expressionNodeKind = "compare"
	expressionNodeBoolean      expressionNodeKind = "boolean"
)

type compiledExpressionPredicate struct {
	path       []int
	ruleName   string
	expression compiledExpression
	placement  ExpressionPredicatePlacement
	order      int
}

type compiledExpression struct {
	kind        expressionNodeKind
	resultKind  ValueKind
	value       Value
	access      compiledPathAccess
	binding     string
	bindingSlot int
	paramName   string
	function    compiledPureFunction
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
	return compileExpressionPredicateSpecWithParams(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, nil, nil)
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
	functions map[string]compiledPureFunction,
) (ExpressionPredicate, compiledExpressionPredicate, error) {
	if spec == nil {
		return ExpressionPredicate{}, compiledExpressionPredicate{}, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression predicate is required", nil)
	}
	expression, referencesEarlierBinding, err := compileExpressionSpecWithParams(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
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
			ruleName:   ruleName,
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
	return compileExpressionSpecWithParams(spec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, nil, nil)
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
	functions map[string]compiledPureFunction,
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
	case CallExpr:
		return compileCallExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
	case *CallExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileCallExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
	case CompareExpr:
		return compileCompareExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
	case *CompareExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileCompareExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
	case BooleanExpr:
		return compileBooleanExpression(expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
	case *BooleanExpr:
		if expression == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "expression node is required", nil)
		}
		return compileBooleanExpression(*expression, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
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
	}, false, nil
}

func compileCurrentFieldExpression(spec CurrentFieldExpr, ruleName string, conditionIndex, predicateIndex int, template *Template) (compiledExpression, bool, error) {
	if hasAmbiguousFieldAndPath(spec.Field, spec.Path) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, strings.TrimSpace(spec.Field), "current field expression cannot set both field and path", ErrInvalidPath)
	}
	normalized := spec.clone()
	normalized.Path = pathOrField(normalized.Path, normalized.Field)
	if normalized.Path.isZero() {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "current path expression requires a path", ErrInvalidPath)
	}
	normalized.Field = normalized.Path.root()
	access, kind, err := compileExpressionPathRef(ruleName, conditionIndex, predicateIndex, template, normalized.Path)
	if err != nil {
		return compiledExpression{}, false, err
	}
	return compiledExpression{
		kind:       expressionNodeCurrentField,
		resultKind: kind,
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
	if hasAmbiguousFieldAndPath(spec.Field, spec.Path) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, strings.TrimSpace(spec.Field), "binding field expression cannot set both field and path", ErrInvalidPath)
	}
	normalized := spec.clone()
	normalized.Path = pathOrField(normalized.Path, normalized.Field)
	if normalized.Binding == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding field expression requires a binding", nil)
	}
	if normalized.Path.isZero() {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "binding path expression requires a path", ErrInvalidPath)
	}
	normalized.Field = normalized.Path.root()
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
	}, false, nil
}

func compileCallExpression(
	spec CallExpr,
	ruleName string,
	conditionIndex, predicateIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
	params map[string]ValueKind,
	functions map[string]compiledPureFunction,
) (compiledExpression, bool, error) {
	normalized := spec.clone()
	if normalized.Name == "" {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "function call requires a name", ErrFunctionValidation)
	}
	function, ok := functions[normalized.Name]
	if !ok {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "unknown function", ErrFunctionValidation)
	}
	if len(normalized.Args) != len(function.args) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "function call arity mismatch", ErrFunctionValidation)
	}
	operands := make([]compiledExpression, 0, len(normalized.Args))
	referencesEarlier := false
	for i, argSpec := range normalized.Args {
		if argSpec == nil {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "function call argument is required", ErrFunctionValidation)
		}
		arg, argReferencesEarlier, err := compileExpressionSpecWithParams(argSpec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
		if err != nil {
			return compiledExpression{}, false, err
		}
		if !expressionKindAssignable(function.args[i], arg.resultKind) {
			return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "function call argument has incompatible type", ErrFunctionValidation)
		}
		operands = append(operands, arg)
		referencesEarlier = referencesEarlier || argReferencesEarlier
	}
	return compiledExpression{
		kind:       expressionNodeCall,
		resultKind: function.ret,
		function:   function,
		operands:   operands,
	}, referencesEarlier, nil
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
	functions map[string]compiledPureFunction,
) (compiledExpression, bool, error) {
	if !validExpressionComparisonOperator(spec.Operator) {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "invalid expression comparison operator", nil)
	}
	if spec.Left == nil || spec.Right == nil {
		return compiledExpression{}, false, expressionValidationError(ruleName, conditionIndex, predicateIndex, "", "comparison expression requires left and right operands", nil)
	}
	left, leftReferencesEarlier, err := compileExpressionSpecWithParams(spec.Left, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
	if err != nil {
		return compiledExpression{}, false, err
	}
	right, rightReferencesEarlier, err := compileExpressionSpecWithParams(spec.Right, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
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
	functions map[string]compiledPureFunction,
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
		operand, operandReferencesEarlier, err := compileExpressionSpecWithParams(operandSpec, ruleName, conditionIndex, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
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

func expressionKindAssignable(want, got ValueKind) bool {
	if want == "" || want == ValueAny || got == ValueAny {
		return true
	}
	if want == got {
		return true
	}
	return expressionKindsNumeric(want, got)
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
		return e.access.root != "" && e.resultKind != ""
	case expressionNodeBindingField:
		return e.binding != "" && e.access.root != "" && e.bindingSlot >= 0 && e.resultKind != ""
	case expressionNodeBindingValue:
		return e.binding != "" && e.bindingSlot >= 0 && e.resultKind != ""
	case expressionNodeHasPath:
		return e.access.root != "" && e.resultKind == ValueBool
	case expressionNodeParam:
		return e.paramName != "" && e.resultKind != ""
	case expressionNodeCall:
		if e.function.name == "" || e.function.fn == nil || e.resultKind == "" || len(e.operands) != len(e.function.args) {
			return false
		}
		for i, operand := range e.operands {
			if !expressionKindAssignable(e.function.args[i], operand.resultKind) {
				return false
			}
		}
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

func (e compiledExpression) containsFunctionCall() bool {
	if e.kind == expressionNodeCall {
		return true
	}
	for _, operand := range e.operands {
		if operand.containsFunctionCall() {
			return true
		}
	}
	return false
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
	return expressionPredicatesMatchWithContextAndCounters(context.Background(), predicates, fact, bindings, nil)
}

func expressionPredicatesMatchWithContextAndCounters(ctx context.Context, predicates []compiledExpressionPredicate, fact conditionFactRef, bindings []conditionMatch, span *propagationCounterSpan) (bool, error) {
	for _, predicate := range predicates {
		if span != nil {
			span.recordExpressionPredicateTest()
		}
		ok, err := predicate.matchesWithContextParamsAndCounters(ctx, fact, bindings, nil, span)
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

func expressionPredicatesMatchToken(predicates []compiledExpressionPredicate, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	return expressionPredicatesMatchTokenWithContext(context.Background(), predicates, fact, bindings, span)
}

func expressionPredicatesMatchTokenWithContext(ctx context.Context, predicates []compiledExpressionPredicate, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	for _, predicate := range predicates {
		if span != nil {
			span.recordExpressionPredicateTest()
		}
		ok, err := predicate.matchesTokenWithContextAndCounters(ctx, fact, bindings, span)
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
	return p.matchesWithContextParamsAndCounters(context.Background(), fact, bindings, params, nil)
}

func (p compiledExpressionPredicate) matchesWithContextParams(ctx context.Context, fact conditionFactRef, bindings []conditionMatch, params map[string]Value) (bool, error) {
	return p.matchesWithContextParamsAndCounters(ctx, fact, bindings, params, nil)
}

func (p compiledExpressionPredicate) matchesWithContextParamsAndCounters(ctx context.Context, fact conditionFactRef, bindings []conditionMatch, params map[string]Value, span *propagationCounterSpan) (bool, error) {
	value, ok, err := p.expression.evaluateWithContextParamsAndCounters(ctx, fact, bindings, params, p.functionEvaluationMeta(), span)
	if err != nil || !ok {
		return false, err
	}
	if value.Kind() != ValueBool {
		return false, nil
	}
	return value.boolValue, nil
}

func (p compiledExpressionPredicate) matchesWithCounters(fact conditionFactRef, bindings []conditionMatch, span *propagationCounterSpan) (bool, error) {
	return p.matchesWithContextParamsAndCounters(context.Background(), fact, bindings, nil, span)
}

func (p compiledExpressionPredicate) matchesToken(fact conditionFactRef, bindings tokenRef) (bool, error) {
	return p.matchesTokenWithCounters(fact, bindings, nil)
}

func (p compiledExpressionPredicate) matchesTokenWithCounters(fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	return p.matchesTokenWithContextAndCounters(context.Background(), fact, bindings, span)
}

func (p compiledExpressionPredicate) matchesTokenWithContextAndCounters(ctx context.Context, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	value, ok, err := p.expression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, fact, bindings, nil, 0, p.functionEvaluationMeta(), span)
	if err != nil || !ok {
		return false, err
	}
	if value.Kind() != ValueBool {
		return false, nil
	}
	return value.boolValue, nil
}

func (p compiledExpressionPredicate) functionEvaluationMeta() *FunctionEvaluationError {
	meta := &FunctionEvaluationError{
		RuleName:       p.ruleName,
		ConditionIndex: -1,
		PredicateIndex: p.order,
	}
	if len(p.path) > 0 {
		meta.ConditionIndex = p.path[0]
	}
	if len(p.path) > 1 {
		meta.PredicateIndex = p.path[1]
	}
	return meta
}

func (e compiledExpression) evaluate(fact conditionFactRef, bindings []conditionMatch) (Value, bool, error) {
	return e.evaluateWithParams(fact, bindings, nil)
}

func (e compiledExpression) evaluateWithParams(fact conditionFactRef, bindings []conditionMatch, params map[string]Value) (Value, bool, error) {
	return e.evaluateWithParamsAndCounters(fact, bindings, params, nil)
}

func (e compiledExpression) evaluateWithParamsAndCounters(fact conditionFactRef, bindings []conditionMatch, params map[string]Value, span *propagationCounterSpan) (Value, bool, error) {
	return e.evaluateWithContextParamsAndCounters(context.Background(), fact, bindings, params, nil, span)
}

func (e compiledExpression) evaluateWithContextParams(ctx context.Context, fact conditionFactRef, bindings []conditionMatch, params map[string]Value) (Value, bool, error) {
	return e.evaluateWithContextParamsAndCounters(ctx, fact, bindings, params, nil, nil)
}

func (e compiledExpression) evaluateWithContextParamsAndCounters(ctx context.Context, fact conditionFactRef, bindings []conditionMatch, params map[string]Value, meta *FunctionEvaluationError, span *propagationCounterSpan) (Value, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch e.kind {
	case expressionNodeConst:
		return e.value, true, nil
	case expressionNodeCurrentField:
		value, ok := e.currentValueFromFactWithCounters(fact, span)
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
		value, ok := e.bindingValueFromFactWithCounters(bindings[e.bindingSlot].fact, span)
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
		_, ok := e.access.valueFromFactWithCounters(fact, span)
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
	case expressionNodeCall:
		return e.evaluateCall(ctx, meta, span, func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateWithContextParamsAndCounters(ctx, fact, bindings, params, meta, span)
		})
	case expressionNodeCompare:
		return e.evaluateCompare(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateWithContextParamsAndCounters(ctx, fact, bindings, params, meta, span)
		})
	case expressionNodeBoolean:
		return e.evaluateBoolean(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateWithContextParamsAndCounters(ctx, fact, bindings, params, meta, span)
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
	return e.evaluateTokenWithParamsAndOffsetAndCounters(fact, bindings, params, bindingSlotOffset, nil)
}

func (e compiledExpression) evaluateTokenWithParamsAndOffsetAndCounters(fact conditionFactRef, bindings tokenRef, params map[string]Value, bindingSlotOffset int, span *propagationCounterSpan) (Value, bool, error) {
	return e.evaluateTokenWithContextParamsOffsetAndCounters(context.Background(), fact, bindings, params, bindingSlotOffset, nil, span)
}

func (e compiledExpression) evaluateTokenWithContextParamsOffset(ctx context.Context, fact conditionFactRef, bindings tokenRef, params map[string]Value, bindingSlotOffset int) (Value, bool, error) {
	return e.evaluateTokenWithContextParamsOffsetAndCounters(ctx, fact, bindings, params, bindingSlotOffset, nil, nil)
}

func (e compiledExpression) evaluateTokenWithContextParamsOffsetAndCounters(ctx context.Context, fact conditionFactRef, bindings tokenRef, params map[string]Value, bindingSlotOffset int, meta *FunctionEvaluationError, span *propagationCounterSpan) (Value, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch e.kind {
	case expressionNodeConst:
		return e.value, true, nil
	case expressionNodeCurrentField:
		value, ok := e.currentValueFromFactWithCounters(fact, span)
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
		value, ok := e.bindingValueFromFactWithCounters(match.fact, span)
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
		_, ok := e.access.valueFromFactWithCounters(fact, span)
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
	case expressionNodeCall:
		return e.evaluateCall(ctx, meta, span, func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateTokenWithContextParamsOffsetAndCounters(ctx, fact, bindings, params, bindingSlotOffset, meta, span)
		})
	case expressionNodeCompare:
		return e.evaluateCompare(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateTokenWithContextParamsOffsetAndCounters(ctx, fact, bindings, params, bindingSlotOffset, meta, span)
		})
	case expressionNodeBoolean:
		return e.evaluateBoolean(func(operand compiledExpression) (Value, bool, error) {
			return operand.evaluateTokenWithContextParamsOffsetAndCounters(ctx, fact, bindings, params, bindingSlotOffset, meta, span)
		})
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported expression node %q", ErrMatcher, e.kind)
	}
}

func (e compiledExpression) currentValueFromFact(fact conditionFactRef) (Value, bool) {
	return e.access.valueFromFact(fact)
}

func (e compiledExpression) currentValueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	return e.access.valueFromFactWithCounters(fact, span)
}

func (e compiledExpression) bindingValueFromFact(fact conditionFactRef) (Value, bool) {
	return e.access.valueFromFact(fact)
}

func (e compiledExpression) bindingValueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	return e.access.valueFromFactWithCounters(fact, span)
}

func (e compiledExpression) evaluateCall(ctx context.Context, meta *FunctionEvaluationError, span *propagationCounterSpan, eval func(compiledExpression) (Value, bool, error)) (value Value, ok bool, err error) {
	if e.function.name == "" || e.function.fn == nil {
		return Value{}, false, fmt.Errorf("%w: malformed function call", ErrFunctionEvaluation)
	}
	if span != nil {
		span.recordFunctionCall()
	}
	if len(e.operands) != len(e.function.args) {
		return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, fmt.Errorf("arity mismatch: got %d args, want %d", len(e.operands), len(e.function.args)))
	}
	if err := ctx.Err(); err != nil {
		return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, err)
	}
	args := make([]Value, len(e.operands))
	for i, operand := range e.operands {
		arg, argOK, err := eval(operand)
		if err != nil || !argOK {
			return Value{}, false, err
		}
		if !expressionKindAssignable(e.function.args[i], arg.Kind()) {
			return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, fmt.Errorf("argument %d has kind %s, want %s", i, arg.Kind(), e.function.args[i]))
		}
		args[i] = cloneValue(arg)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			value = Value{}
			ok = false
			err = recordFunctionEvaluationError(span, meta, e.function.name, fmt.Errorf("panic: %v", recovered))
		}
	}()
	value, err = e.function.fn(ctx, args)
	if err != nil {
		return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, err)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, err)
	}
	value = cloneValue(value)
	if value.Kind() == "" {
		value = NullValue()
	}
	if !expressionKindAssignable(e.function.ret, value.Kind()) {
		return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, fmt.Errorf("return has kind %s, want %s", value.Kind(), e.function.ret))
	}
	canonical, err := canonicalValue(value)
	if err != nil {
		return Value{}, false, recordFunctionEvaluationError(span, meta, e.function.name, err)
	}
	return canonical, true, nil
}

func recordFunctionEvaluationError(span *propagationCounterSpan, meta *FunctionEvaluationError, functionName string, err error) error {
	if span != nil {
		span.recordFunctionError()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			span.recordFunctionCancellation()
		}
	}
	return functionEvaluationError(meta, functionName, err)
}

func functionEvaluationError(meta *FunctionEvaluationError, functionName string, err error) error {
	out := &FunctionEvaluationError{FunctionName: functionName, Err: err}
	if meta != nil {
		out.RuleName = meta.RuleName
		out.QueryName = meta.QueryName
		out.ConditionIndex = meta.ConditionIndex
		out.PredicateIndex = meta.PredicateIndex
		if out.FunctionName == "" {
			out.FunctionName = meta.FunctionName
		}
	}
	return out
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

func splitCompiledExpressionPredicate(predicate compiledExpressionPredicate) []compiledExpressionPredicate {
	predicate.expression = optimizeCompiledPredicateExpression(predicate.expression)
	predicate.placement = expressionPredicatePlacementForExpression(predicate.expression)
	operands := flattenedAndExpressions(predicate.expression)
	if len(operands) <= 1 {
		return []compiledExpressionPredicate{predicate}
	}
	out := make([]compiledExpressionPredicate, 0, len(operands))
	for i, operand := range operands {
		next := predicate
		next.expression = optimizeCompiledPredicateExpression(operand)
		next.placement = expressionPredicatePlacementForExpression(next.expression)
		next.path = append(cloneIntPath(predicate.path), i)
		out = append(out, next)
	}
	return out
}

func expressionPredicatePlacementForExpression(expression compiledExpression) ExpressionPredicatePlacement {
	if expression.referencesBinding() {
		return ExpressionPredicatePlacementBetaResidual
	}
	return ExpressionPredicatePlacementAlpha
}

func optimizeCompiledPredicateExpression(expression compiledExpression) compiledExpression {
	expression = expression.clone()
	for i := range expression.operands {
		expression.operands[i] = optimizeCompiledPredicateExpression(expression.operands[i])
	}
	if expression.kind != expressionNodeBoolean || expression.boolOp != ExpressionBoolNot || len(expression.operands) != 1 {
		return expression
	}
	operand := expression.operands[0]
	if inverted, ok := invertCompiledComparisonExpression(operand); ok {
		return inverted
	}
	if operand.kind == expressionNodeConst && operand.resultKind == ValueBool {
		return compiledExpression{
			kind:       expressionNodeConst,
			resultKind: ValueBool,
			value:      newBoolValue(!operand.value.boolValue),
		}
	}
	return expression
}

func invertCompiledComparisonExpression(expression compiledExpression) (compiledExpression, bool) {
	if expression.kind != expressionNodeCompare || len(expression.operands) != 2 {
		return compiledExpression{}, false
	}
	operator, ok := invertExpressionComparisonOperator(expression.compareOp)
	if !ok || !expressionComparisonOperandsGuaranteed(expression.operands[0], expression.operands[1]) {
		return compiledExpression{}, false
	}
	out := expression.clone()
	out.compareOp = operator
	return out, true
}

func expressionComparisonOperandsGuaranteed(left, right compiledExpression) bool {
	if left.resultKind == ValueAny || right.resultKind == ValueAny || !expressionOperandsComparable(ExpressionCompareEqual, left.resultKind, right.resultKind) {
		return false
	}
	return expressionValueGuaranteed(left) && expressionValueGuaranteed(right)
}

func expressionValueGuaranteed(expression compiledExpression) bool {
	switch expression.kind {
	case expressionNodeConst:
		return true
	case expressionNodeCurrentField, expressionNodeBindingField:
		return expression.access.topLevel() && expression.access.presenceGuaranteed
	default:
		return false
	}
}

func invertExpressionComparisonOperator(operator ExpressionComparisonOperator) (ExpressionComparisonOperator, bool) {
	switch operator {
	case ExpressionCompareEqual:
		return ExpressionCompareNotEqual, true
	case ExpressionCompareNotEqual:
		return ExpressionCompareEqual, true
	case ExpressionCompareLessThan:
		return ExpressionCompareGreaterOrEqual, true
	case ExpressionCompareLessOrEqual:
		return ExpressionCompareGreaterThan, true
	case ExpressionCompareGreaterThan:
		return ExpressionCompareLessOrEqual, true
	case ExpressionCompareGreaterOrEqual:
		return ExpressionCompareLessThan, true
	default:
		return "", false
	}
}

func flattenedAndExpressions(expression compiledExpression) []compiledExpression {
	if expression.kind != expressionNodeBoolean || expression.boolOp != ExpressionBoolAnd {
		return []compiledExpression{expression}
	}
	out := make([]compiledExpression, 0, len(expression.operands))
	for _, operand := range expression.operands {
		out = append(out, flattenedAndExpressions(operand)...)
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
		b.WriteString(expression.access.root)
		b.WriteString(",field-slot=")
		b.WriteString(fmt.Sprint(expression.access.rootSlot))
		b.WriteString(",path=")
		b.WriteString(expression.access.display())
	case expressionNodeBindingField:
		b.WriteString(",binding=")
		b.WriteString(expression.binding)
		b.WriteString(",binding-slot=")
		b.WriteString(fmt.Sprint(expression.bindingSlot))
		b.WriteString(",field=")
		b.WriteString(expression.access.root)
		b.WriteString(",field-slot=")
		b.WriteString(fmt.Sprint(expression.access.rootSlot))
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
	case expressionNodeCall:
		b.WriteString(",function=")
		b.WriteString(expression.function.name)
		b.WriteString(",args=")
		for _, arg := range expression.function.args {
			b.WriteString(string(arg))
			b.WriteByte(',')
		}
	case expressionNodeHasPath:
		b.WriteString(",path=")
		b.WriteString(expression.access.display())
		b.WriteString(",field-slot=")
		b.WriteString(fmt.Sprint(expression.access.rootSlot))
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
