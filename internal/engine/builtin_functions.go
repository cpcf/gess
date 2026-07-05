package engine

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
)

// builtinFunctionSpecs is the curated, deterministic set of expression
// functions always available to .gess sources without host registration. They
// are installed before user functions and cannot be shadowed. No function here
// reads wall-clock time or randomness: the engine's ordering and reproducibility
// contract depends on determinism.
//
// The pure-function arity model is fixed-arity, so the variadic CLIPS/Jess
// operators (+, *, str-cat, min, max) are exposed as two-argument forms; author
// nested calls to combine more than two operands.
func builtinFunctionSpecs() []PureFunctionSpec {
	return []PureFunctionSpec{
		// Arithmetic. Numeric args accept int or float; a float operand
		// promotes the result to float.
		numericBinary("+", func(a, b float64) float64 { return a + b }, func(a, b int64) int64 { return a + b }),
		numericBinary("-", func(a, b float64) float64 { return a - b }, func(a, b int64) int64 { return a - b }),
		numericBinary("*", func(a, b float64) float64 { return a * b }, func(a, b int64) int64 { return a * b }),
		{Name: "/", Args: []ValueKind{ValueAny, ValueAny}, Return: ValueFloat, Func2: builtinDivide},
		{Name: "mod", Args: []ValueKind{ValueAny, ValueAny}, Return: ValueInt, Func2: builtinMod},
		{Name: "abs", Args: []ValueKind{ValueAny}, Return: ValueAny, Func1: builtinAbs},
		numericBinary("min", math.Min, func(a, b int64) int64 {
			if a < b {
				return a
			}
			return b
		}),
		numericBinary("max", math.Max, func(a, b int64) int64 {
			if a > b {
				return a
			}
			return b
		}),

		// Numeric conversions.
		{Name: "integer", Args: []ValueKind{ValueAny}, Return: ValueInt, Func1: builtinInteger},
		{Name: "float", Args: []ValueKind{ValueAny}, Return: ValueFloat, Func1: builtinFloat},

		// Numeric predicates.
		{Name: "numberp", Args: []ValueKind{ValueAny}, Return: ValueBool, Func1: builtinKindPredicate(ValueInt, ValueFloat)},
		{Name: "integerp", Args: []ValueKind{ValueAny}, Return: ValueBool, Func1: builtinKindPredicate(ValueInt)},
		{Name: "floatp", Args: []ValueKind{ValueAny}, Return: ValueBool, Func1: builtinKindPredicate(ValueFloat)},

		// Strings.
		{Name: "str-cat", Args: []ValueKind{ValueString, ValueString}, Return: ValueString, Func2: builtinStrCat},
		{Name: "str-length", Args: []ValueKind{ValueString}, Return: ValueInt, Func1: builtinStrLength},
		{Name: "sub-string", Args: []ValueKind{ValueString, ValueInt, ValueInt}, Return: ValueString, Func3: builtinSubString},
		{Name: "upcase", Args: []ValueKind{ValueString}, Return: ValueString, Func1: builtinCase(strings.ToUpper)},
		{Name: "lowcase", Args: []ValueKind{ValueString}, Return: ValueString, Func1: builtinCase(strings.ToLower)},

		// Type predicates.
		{Name: "stringp", Args: []ValueKind{ValueAny}, Return: ValueBool, Func1: builtinKindPredicate(ValueString)},
		{Name: "booleanp", Args: []ValueKind{ValueAny}, Return: ValueBool, Func1: builtinKindPredicate(ValueBool)},
		{Name: "nullp", Args: []ValueKind{ValueAny}, Return: ValueBool, Func1: builtinKindPredicate(ValueNull)},
	}
}

func numericArg(value Value, name string) (float64, int64, bool, error) {
	switch value.Kind() {
	case ValueInt:
		v, _ := value.AsInt64()
		return float64(v), v, false, nil
	case ValueFloat:
		v, _ := value.AsFloat64()
		return v, 0, true, nil
	default:
		return 0, 0, false, fmt.Errorf("%w: %s expects a number, got %s", ErrBuiltinArgument, name, value.Kind())
	}
}

// numericBinary builds a two-argument arithmetic function that promotes to
// float when either operand is a float and stays integer otherwise.
func numericBinary(name string, float func(a, b float64) float64, integer func(a, b int64) int64) PureFunctionSpec {
	return PureFunctionSpec{
		Name:   name,
		Args:   []ValueKind{ValueAny, ValueAny},
		Return: ValueAny,
		Func2: func(_ context.Context, left, right Value) (Value, error) {
			lf, li, lFloat, err := numericArg(left, name)
			if err != nil {
				return Value{}, err
			}
			rf, ri, rFloat, err := numericArg(right, name)
			if err != nil {
				return Value{}, err
			}
			if lFloat || rFloat {
				return NewValue(float(lf, rf))
			}
			return NewValue(integer(li, ri))
		},
	}
}

func builtinDivide(_ context.Context, left, right Value) (Value, error) {
	lf, _, _, err := numericArg(left, "/")
	if err != nil {
		return Value{}, err
	}
	rf, _, _, err := numericArg(right, "/")
	if err != nil {
		return Value{}, err
	}
	if rf == 0 {
		return Value{}, ErrDivideByZero
	}
	return NewValue(lf / rf)
}

func builtinMod(_ context.Context, left, right Value) (Value, error) {
	if left.Kind() != ValueInt || right.Kind() != ValueInt {
		return Value{}, fmt.Errorf("%w: mod expects two integers", ErrBuiltinArgument)
	}
	l, _ := left.AsInt64()
	r, _ := right.AsInt64()
	if r == 0 {
		return Value{}, ErrDivideByZero
	}
	return NewValue(l % r)
}

func builtinAbs(_ context.Context, value Value) (Value, error) {
	switch value.Kind() {
	case ValueInt:
		v, _ := value.AsInt64()
		if v < 0 {
			v = -v
		}
		return NewValue(v)
	case ValueFloat:
		v, _ := value.AsFloat64()
		return NewValue(math.Abs(v))
	default:
		return Value{}, fmt.Errorf("%w: abs expects a number, got %s", ErrBuiltinArgument, value.Kind())
	}
}

func builtinInteger(_ context.Context, value Value) (Value, error) {
	switch value.Kind() {
	case ValueInt:
		return value, nil
	case ValueFloat:
		v, _ := value.AsFloat64()
		return NewValue(int64(math.Trunc(v)))
	default:
		return Value{}, fmt.Errorf("%w: integer expects a number, got %s", ErrBuiltinArgument, value.Kind())
	}
}

func builtinFloat(_ context.Context, value Value) (Value, error) {
	switch value.Kind() {
	case ValueFloat:
		return value, nil
	case ValueInt:
		v, _ := value.AsInt64()
		return NewValue(float64(v))
	default:
		return Value{}, fmt.Errorf("%w: float expects a number, got %s", ErrBuiltinArgument, value.Kind())
	}
}

func builtinKindPredicate(kinds ...ValueKind) PureFunction1 {
	return func(_ context.Context, value Value) (Value, error) {
		if slices.Contains(kinds, value.Kind()) {
			return NewValue(true)
		}
		return NewValue(false)
	}
}

func builtinStrCat(_ context.Context, left, right Value) (Value, error) {
	l, _ := left.AsString()
	r, _ := right.AsString()
	return NewValue(l + r)
}

func builtinStrLength(_ context.Context, value Value) (Value, error) {
	s, _ := value.AsString()
	return NewValue(int64(len([]rune(s))))
}

func builtinSubString(_ context.Context, value, start, end Value) (Value, error) {
	s, _ := value.AsString()
	runes := []rune(s)
	begin, _ := start.AsInt64()
	stop, _ := end.AsInt64()
	if begin < 0 || stop < begin || stop > int64(len(runes)) {
		return Value{}, fmt.Errorf("%w: sub-string range [%d,%d) out of bounds for length %d", ErrBuiltinArgument, begin, stop, len(runes))
	}
	return NewValue(string(runes[begin:stop]))
}

func builtinCase(transform func(string) string) PureFunction1 {
	return func(_ context.Context, value Value) (Value, error) {
		s, _ := value.AsString()
		return NewValue(transform(s))
	}
}
