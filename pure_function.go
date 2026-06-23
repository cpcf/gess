package gess

import (
	"context"
	"fmt"
	"strings"
)

// PureFunction is a deterministic, side-effect-free function implementation
// available to condition and query expressions.
type PureFunction func(context.Context, []Value) (Value, error)

type PureFunctionSpec struct {
	Name               string
	Args               []ValueKind
	Return             ValueKind
	Func               PureFunction
	EqualityComparator bool
}

func (s PureFunctionSpec) clone() PureFunctionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	if out.Return == "" {
		out.Return = ValueAny
	}
	out.Args = append([]ValueKind(nil), s.Args...)
	for i, kind := range out.Args {
		if kind == "" {
			out.Args[i] = ValueAny
		}
	}
	return out
}

type PureFunctionDefinition struct {
	name               string
	args               []ValueKind
	ret                ValueKind
	order              int
	equalityComparator bool
}

func (f PureFunctionDefinition) Name() string {
	return f.name
}

func (f PureFunctionDefinition) Args() []ValueKind {
	return append([]ValueKind(nil), f.args...)
}

func (f PureFunctionDefinition) Return() ValueKind {
	return f.ret
}

func (f PureFunctionDefinition) DeclarationOrder() int {
	return f.order
}

func (f PureFunctionDefinition) EqualityComparator() bool {
	return f.equalityComparator
}

type compiledPureFunction struct {
	name               string
	args               []ValueKind
	ret                ValueKind
	fn                 PureFunction
	order              int
	equalityComparator bool
}

func compilePureFunctionSpec(spec PureFunctionSpec, order int) (compiledPureFunction, error) {
	normalized := spec.clone()
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
	if normalized.Func == nil {
		return compiledPureFunction{}, &ValidationError{
			Reason: "function implementation is required",
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
	return compiledPureFunction{
		name:               normalized.Name,
		args:               append([]ValueKind(nil), normalized.Args...),
		ret:                normalized.Return,
		fn:                 normalized.Func,
		order:              order,
		equalityComparator: normalized.EqualityComparator,
	}, nil
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

func (f compiledPureFunction) inspect() PureFunctionDefinition {
	return PureFunctionDefinition{
		name:               f.name,
		args:               append([]ValueKind(nil), f.args...),
		ret:                f.ret,
		order:              f.order,
		equalityComparator: f.equalityComparator,
	}
}

type FunctionEvaluationError struct {
	RuleName       string
	QueryName      string
	ConditionIndex int
	PredicateIndex int
	FunctionName   string
	Err            error
}

func (e *FunctionEvaluationError) Error() string {
	if e == nil {
		return ErrFunctionEvaluation.Error()
	}
	msg := "gess: function evaluation failed"
	if e.RuleName != "" {
		msg += fmt.Sprintf(" for rule %q", e.RuleName)
	}
	if e.QueryName != "" {
		msg += fmt.Sprintf(" for query %q", e.QueryName)
	}
	if e.ConditionIndex >= 0 {
		msg += fmt.Sprintf(" condition %d", e.ConditionIndex)
	}
	if e.PredicateIndex >= 0 {
		msg += fmt.Sprintf(" predicate %d", e.PredicateIndex)
	}
	if e.FunctionName != "" {
		msg += fmt.Sprintf(" function %q", e.FunctionName)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *FunctionEvaluationError) Unwrap() error {
	if e != nil && e.Err != nil {
		return e.Err
	}
	return ErrFunctionEvaluation
}

func (e *FunctionEvaluationError) Is(target error) bool {
	return target == ErrFunctionEvaluation
}
