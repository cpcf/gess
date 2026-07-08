package rules

import (
	"context"
	"strings"
)

// PureFunction is a deterministic, side-effect-free function implementation
// available to condition and query expressions.
type PureFunction func(context.Context, []Value) (Value, error)

// PureFunction0 is a fixed-arity PureFunction taking no arguments.
type PureFunction0 func(context.Context) (Value, error)

// PureFunction1 is a fixed-arity PureFunction taking one argument.
type PureFunction1 func(context.Context, Value) (Value, error)

// PureFunction2 is a fixed-arity PureFunction taking two arguments.
type PureFunction2 func(context.Context, Value, Value) (Value, error)

// PureFunction3 is a fixed-arity PureFunction taking three arguments.
type PureFunction3 func(context.Context, Value, Value, Value) (Value, error)

// PureFunctionSpec registers a pure function by Name, its Args and Return
// value kinds, and exactly one implementation function.
type PureFunctionSpec struct {
	Name               string
	Args               []ValueKind
	Return             ValueKind
	Func               PureFunction
	Func0              PureFunction0
	Func1              PureFunction1
	Func2              PureFunction2
	Func3              PureFunction3
	EqualityComparator bool
	IndexKeyExtractor  bool
}

// ClonePureFunctionSpec returns a defensive copy of s.
func ClonePureFunctionSpec(s PureFunctionSpec) PureFunctionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	if out.Return == valueKindUnknown {
		out.Return = ValueAny
	}
	out.Args = append([]ValueKind(nil), s.Args...)
	for i, kind := range out.Args {
		if kind == valueKindUnknown {
			out.Args[i] = ValueAny
		}
	}
	return out
}

// PureFunctionDefinition is the compiled, inspectable form of a
// PureFunctionSpec or ExpressionFunctionSpec.
type PureFunctionDefinition struct {
	NameValue               string
	ParamNamesValue         []string
	ArgsValue               []ValueKind
	ReturnKind              ValueKind
	DescriptionText         string
	ExpressionSpec          ExpressionSpec
	ExpressionBackedValue   bool
	Order                   int
	EqualityComparatorValue bool
	IndexKeyExtractorValue  bool
}

func (f PureFunctionDefinition) Name() string {
	return f.NameValue
}

func (f PureFunctionDefinition) Args() []ValueKind {
	return append([]ValueKind(nil), f.ArgsValue...)
}

func (f PureFunctionDefinition) ParamNames() []string {
	return append([]string(nil), f.ParamNamesValue...)
}

func (f PureFunctionDefinition) Return() ValueKind {
	return f.ReturnKind
}

func (f PureFunctionDefinition) Description() string {
	return f.DescriptionText
}

func (f PureFunctionDefinition) Expression() (ExpressionSpec, bool) {
	if !f.ExpressionBackedValue {
		return nil, false
	}
	return CloneExpressionSpec(f.ExpressionSpec), true
}

func (f PureFunctionDefinition) ExpressionBacked() bool {
	return f.ExpressionBackedValue
}

func (f PureFunctionDefinition) DeclarationOrder() int {
	return f.Order
}

func (f PureFunctionDefinition) EqualityComparator() bool {
	return f.EqualityComparatorValue
}

func (f PureFunctionDefinition) IndexKeyExtractor() bool {
	return f.IndexKeyExtractorValue
}

// ClonePureFunctionDefinition returns a defensive copy of f.
func ClonePureFunctionDefinition(f PureFunctionDefinition) PureFunctionDefinition {
	out := f
	out.ParamNamesValue = append([]string(nil), f.ParamNamesValue...)
	out.ArgsValue = append([]ValueKind(nil), f.ArgsValue...)
	out.ExpressionSpec = CloneExpressionSpec(f.ExpressionSpec)
	return out
}

// ExpressionFunctionParamSpec declares one named, typed parameter of an
// expression-backed function.
type ExpressionFunctionParamSpec struct {
	Name string
	Kind ValueKind
}

// ExpressionFunctionSpec defines a pure function whose body is an expression.
type ExpressionFunctionSpec struct {
	Name        string
	Params      []ExpressionFunctionParamSpec
	Return      ValueKind
	Expression  ExpressionSpec
	Description string
	Source      SourceSpan
}

// CloneExpressionFunctionSpec returns a defensive copy of s.
func CloneExpressionFunctionSpec(s ExpressionFunctionSpec) ExpressionFunctionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	out.Description = strings.TrimSpace(out.Description)
	if out.Return == valueKindUnknown {
		out.Return = ValueAny
	}
	out.Params = make([]ExpressionFunctionParamSpec, len(s.Params))
	for i, param := range s.Params {
		out.Params[i] = ExpressionFunctionParamSpec{
			Name: strings.TrimSpace(param.Name),
			Kind: param.Kind,
		}
		if out.Params[i].Kind == valueKindUnknown {
			out.Params[i].Kind = ValueAny
		}
	}
	out.Expression = CloneExpressionSpec(s.Expression)
	return out
}
