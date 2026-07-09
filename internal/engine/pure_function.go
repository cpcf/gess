package engine

import (
	"fmt"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type PureFunction = gessrules.PureFunction
type PureFunction0 = gessrules.PureFunction0
type PureFunction1 = gessrules.PureFunction1
type PureFunction2 = gessrules.PureFunction2
type PureFunction3 = gessrules.PureFunction3
type PureFunctionSpec = gessrules.PureFunctionSpec
type ExpressionFunctionParamSpec = gessrules.ExpressionFunctionParamSpec
type ExpressionFunctionSpec = gessrules.ExpressionFunctionSpec

func clonePureFunctionSpec(s PureFunctionSpec) PureFunctionSpec {
	return gessrules.ClonePureFunctionSpec(s)
}

func cloneExpressionFunctionSpec(s ExpressionFunctionSpec) ExpressionFunctionSpec {
	return gessrules.CloneExpressionFunctionSpec(s)
}

type PureFunctionDefinition = gessrules.PureFunctionDefinition

type compiledPureFunction struct {
	name               string
	paramNames         []string
	args               []ValueKind
	ret                ValueKind
	fn                 PureFunction
	fn0                PureFunction0
	fn1                PureFunction1
	fn2                PureFunction2
	fn3                PureFunction3
	expression         *compiledExpression
	expressionSpec     ExpressionSpec
	description        string
	expressionBacked   bool
	order              int
	equalityComparator bool
	indexKeyExtractor  bool
}

func compilePureFunctionSpec(spec PureFunctionSpec, order int) (compiledPureFunction, error) {
	normalized := clonePureFunctionSpec(spec)
	if normalized.Name == "" {
		return compiledPureFunction{}, &ValidationError{
			Reason: "function name is required",
			Err:    ErrFunctionValidation,
		}
	}
	if !validPureFunctionName(normalized.Name) {
		return compiledPureFunction{}, &ValidationError{
			Reason: "invalid function name",
			Err:    ErrFunctionValidation,
		}
	}
	implementationCount := 0
	if normalized.Func != nil {
		implementationCount++
	}
	if normalized.Func0 != nil {
		implementationCount++
	}
	if normalized.Func1 != nil {
		implementationCount++
	}
	if normalized.Func2 != nil {
		implementationCount++
	}
	if normalized.Func3 != nil {
		implementationCount++
	}
	if implementationCount == 0 {
		return compiledPureFunction{}, &ValidationError{
			Reason: "function implementation is required",
			Err:    ErrFunctionValidation,
		}
	}
	if implementationCount > 1 {
		return compiledPureFunction{}, &ValidationError{
			Reason: "function must declare exactly one implementation",
			Err:    ErrFunctionValidation,
		}
	}
	if !validPureFunctionValueKind(normalized.Return) {
		return compiledPureFunction{}, &ValidationError{
			Reason: "invalid function return kind",
			Err:    ErrFunctionValidation,
		}
	}
	for _, kind := range normalized.Args {
		if !validPureFunctionValueKind(kind) {
			return compiledPureFunction{}, &ValidationError{
				Reason: "invalid function argument kind",
				Err:    ErrFunctionValidation,
			}
		}
	}
	if normalized.Func0 != nil && len(normalized.Args) != 0 {
		return compiledPureFunction{}, &ValidationError{
			Reason: "fixed-arity function implementation does not match argument count",
			Err:    ErrFunctionValidation,
		}
	}
	if normalized.Func1 != nil && len(normalized.Args) != 1 {
		return compiledPureFunction{}, &ValidationError{
			Reason: "fixed-arity function implementation does not match argument count",
			Err:    ErrFunctionValidation,
		}
	}
	if normalized.Func2 != nil && len(normalized.Args) != 2 {
		return compiledPureFunction{}, &ValidationError{
			Reason: "fixed-arity function implementation does not match argument count",
			Err:    ErrFunctionValidation,
		}
	}
	if normalized.Func3 != nil && len(normalized.Args) != 3 {
		return compiledPureFunction{}, &ValidationError{
			Reason: "fixed-arity function implementation does not match argument count",
			Err:    ErrFunctionValidation,
		}
	}
	if normalized.EqualityComparator {
		if len(normalized.Args) != 2 || normalized.Return != ValueBool {
			return compiledPureFunction{}, &ValidationError{
				Reason: "equality comparator function must accept two arguments and return bool",
				Err:    ErrFunctionValidation,
			}
		}
		if !expressionOperandsComparable(ExpressionCompareEqual, normalized.Args[0], normalized.Args[1]) {
			return compiledPureFunction{}, &ValidationError{
				Reason: "equality comparator function arguments are not comparable",
				Err:    ErrFunctionValidation,
			}
		}
	}
	if normalized.IndexKeyExtractor {
		if len(normalized.Args) != 1 {
			return compiledPureFunction{}, &ValidationError{
				Reason: "index key extractor function must accept one argument",
				Err:    ErrFunctionValidation,
			}
		}
		if !validPureFunctionIndexKeyKind(normalized.Return) {
			return compiledPureFunction{}, &ValidationError{
				Reason: "index key extractor function must return a scalar key kind",
				Err:    ErrFunctionValidation,
			}
		}
	}
	return compiledPureFunction{
		name:               normalized.Name,
		args:               append([]ValueKind(nil), normalized.Args...),
		ret:                normalized.Return,
		fn:                 normalized.Func,
		fn0:                normalized.Func0,
		fn1:                normalized.Func1,
		fn2:                normalized.Func2,
		fn3:                normalized.Func3,
		order:              order,
		equalityComparator: normalized.EqualityComparator,
		indexKeyExtractor:  normalized.IndexKeyExtractor,
	}, nil
}

func (f compiledPureFunction) hasImplementation() bool {
	return f.fn != nil || f.fn0 != nil || f.fn1 != nil || f.fn2 != nil || f.fn3 != nil || f.expression != nil
}

func compileExpressionFunctionSpec(spec ExpressionFunctionSpec, order int, functions map[string]compiledPureFunction) (compiledPureFunction, error) {
	compiled, err := compileExpressionFunctionSpecInternal(spec, order, functions)
	return compiled, attachValidationErrorSource(err, spec.Source)
}

func compileExpressionFunctionSpecInternal(spec ExpressionFunctionSpec, order int, functions map[string]compiledPureFunction) (compiledPureFunction, error) {
	normalized := cloneExpressionFunctionSpec(spec)
	if normalized.Name == "" {
		return compiledPureFunction{}, &ValidationError{
			Reason: "function name is required",
			Err:    ErrFunctionValidation,
		}
	}
	if !validPureFunctionName(normalized.Name) {
		return compiledPureFunction{}, &ValidationError{
			Reason: "invalid function name",
			Err:    ErrFunctionValidation,
		}
	}
	if !validPureFunctionValueKind(normalized.Return) {
		return compiledPureFunction{}, &ValidationError{
			Reason: "invalid function return kind",
			Err:    ErrFunctionValidation,
		}
	}
	if normalized.Expression == nil {
		return compiledPureFunction{}, &ValidationError{
			Reason: "function expression is required",
			Err:    ErrFunctionValidation,
		}
	}
	params := make(map[string]ValueKind, len(normalized.Params))
	paramNames := make([]string, 0, len(normalized.Params))
	args := make([]ValueKind, 0, len(normalized.Params))
	for _, param := range normalized.Params {
		if param.Name == "" {
			return compiledPureFunction{}, &ValidationError{
				FunctionName: normalized.Name,
				Source:       normalized.Source,
				Reason:       "function parameter name is required",
				Err:          ErrFunctionValidation,
			}
		}
		if !validPureFunctionValueKind(param.Kind) {
			return compiledPureFunction{}, &ValidationError{
				FunctionName: normalized.Name,
				Source:       normalized.Source,
				Reason:       "invalid function parameter kind",
				Err:          ErrFunctionValidation,
			}
		}
		if _, exists := params[param.Name]; exists {
			return compiledPureFunction{}, &ValidationError{
				FunctionName: normalized.Name,
				Source:       normalized.Source,
				Reason:       "duplicate function parameter",
				Err:          ErrFunctionValidation,
			}
		}
		params[param.Name] = param.Kind
		paramNames = append(paramNames, param.Name)
		args = append(args, param.Kind)
	}
	if err := validateExpressionFunctionBodySpec(normalized.Expression); err != nil {
		return compiledPureFunction{}, err
	}
	expression, _, err := compileExpressionSpecWithParams(normalized.Expression, normalized.Name, -1, -1, nil, nil, nil, nil, params, functions, nil)
	if err != nil {
		return compiledPureFunction{}, &ValidationError{
			FunctionName: normalized.Name,
			Source:       normalized.Source,
			Reason:       fmt.Sprintf("invalid function expression: %v", err),
			Err:          fmt.Errorf("%w: %w", ErrFunctionValidation, err),
		}
	}
	if !expressionKindAssignable(normalized.Return, expression.resultKind) {
		return compiledPureFunction{}, &ValidationError{
			FunctionName: normalized.Name,
			Source:       normalized.Source,
			Reason:       "function expression return has incompatible type",
			Err:          ErrFunctionValidation,
		}
	}
	return compiledPureFunction{
		name:             normalized.Name,
		paramNames:       paramNames,
		args:             args,
		ret:              normalized.Return,
		expression:       &expression,
		expressionSpec:   cloneExpressionSpec(normalized.Expression),
		description:      normalized.Description,
		expressionBacked: true,
		order:            order,
	}, nil
}

func validateExpressionFunctionBodySpec(spec ExpressionSpec) error {
	switch expression := spec.(type) {
	case nil:
		return &ValidationError{Reason: "function expression is required", Err: ErrFunctionValidation}
	case ConstExpr, *ConstExpr, ParamExpr, *ParamExpr:
		return nil
	case CallExpr:
		for _, arg := range expression.Args {
			if err := validateExpressionFunctionBodySpec(arg); err != nil {
				return err
			}
		}
		return nil
	case *CallExpr:
		if expression == nil {
			return &ValidationError{Reason: "function expression is required", Err: ErrFunctionValidation}
		}
		return validateExpressionFunctionBodySpec(CallExpr(*expression))
	case CompareExpr:
		if err := validateExpressionFunctionBodySpec(expression.Left); err != nil {
			return err
		}
		return validateExpressionFunctionBodySpec(expression.Right)
	case *CompareExpr:
		if expression == nil {
			return &ValidationError{Reason: "function expression is required", Err: ErrFunctionValidation}
		}
		return validateExpressionFunctionBodySpec(CompareExpr(*expression))
	case BooleanExpr:
		for _, operand := range expression.Operands {
			if err := validateExpressionFunctionBodySpec(operand); err != nil {
				return err
			}
		}
		return nil
	case *BooleanExpr:
		if expression == nil {
			return &ValidationError{Reason: "function expression is required", Err: ErrFunctionValidation}
		}
		return validateExpressionFunctionBodySpec(BooleanExpr(*expression))
	default:
		return &ValidationError{
			Reason: "function expression may only reference parameters, constants, and function calls",
			Err:    ErrFunctionValidation,
		}
	}
}

func validPureFunctionName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r <= ' ' {
			return false
		}
	}
	return true
}

func validPureFunctionValueKind(kind ValueKind) bool {
	switch kind {
	case ValueAny, ValueNull, ValueBool, ValueInt, ValueFloat, ValueString, ValueList, ValueMap:
		return true
	default:
		return false
	}
}

func validPureFunctionIndexKeyKind(kind ValueKind) bool {
	switch kind {
	case ValueNull, ValueBool, ValueInt, ValueFloat, ValueString:
		return true
	default:
		return false
	}
}

func (f compiledPureFunction) inspect() PureFunctionDefinition {
	return PureFunctionDefinition{
		NameValue:               f.name,
		ParamNamesValue:         append([]string(nil), f.paramNames...),
		ArgsValue:               append([]ValueKind(nil), f.args...),
		ReturnKind:              f.ret,
		DescriptionText:         f.description,
		ExpressionSpec:          cloneExpressionSpec(f.expressionSpec),
		ExpressionBackedValue:   f.expressionBacked,
		Order:                   f.order,
		EqualityComparatorValue: f.equalityComparator,
		IndexKeyExtractorValue:  f.indexKeyExtractor,
	}
}

type FunctionEvaluationError = gessrules.FunctionEvaluationError
