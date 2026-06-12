package gess

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type ValueKind string

const (
	ValueAny    ValueKind = "any"
	ValueNull   ValueKind = "null"
	ValueBool   ValueKind = "bool"
	ValueInt    ValueKind = "int"
	ValueFloat  ValueKind = "float"
	ValueString ValueKind = "string"
	ValueList   ValueKind = "list"
	ValueMap    ValueKind = "map"
)

type Value struct {
	kind ValueKind
	data any
}

var ErrUnsupportedValue = errors.New("gess: unsupported value")

const maxExactFloatInt = int64(1 << 53)

func NullValue() Value {
	return Value{kind: ValueNull}
}

func (v Value) Kind() ValueKind {
	if v.kind == "" {
		return ValueNull
	}
	return v.kind
}

func NewValue(raw any) (Value, error) {
	return canonicalValue(raw)
}

func (v Value) Equal(other Value) bool {
	switch v.Kind() {
	case ValueNull:
		return other.Kind() == ValueNull
	case ValueBool:
		if other.Kind() != ValueBool {
			return false
		}
		return v.data.(bool) == other.data.(bool)
	case ValueInt:
		return numericValuesEqual(v, other)
	case ValueFloat:
		return numericValuesEqual(v, other)
	case ValueString:
		if other.Kind() != ValueString {
			return false
		}
		return v.data.(string) == other.data.(string)
	case ValueList:
		if other.Kind() != ValueList {
			return false
		}
		left := v.data.([]Value)
		right := other.data.([]Value)
		if len(left) != len(right) {
			return false
		}
		for i := range left {
			if !left[i].Equal(right[i]) {
				return false
			}
		}
		return true
	case ValueMap:
		if other.Kind() != ValueMap {
			return false
		}
		left := v.data.(map[string]Value)
		right := other.data.(map[string]Value)
		if len(left) != len(right) {
			return false
		}
		for key, leftValue := range left {
			rightValue, ok := right[key]
			if !ok || !leftValue.Equal(rightValue) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (v Value) canonicalKey() string {
	var b strings.Builder
	encodeValueForDuplicateKey(&b, v)
	return b.String()
}

func NewFields(raw map[string]any) (Fields, error) {
	out := make(Fields, len(raw))
	for key, value := range raw {
		canonical, err := canonicalValue(value)
		if err != nil {
			return nil, err
		}
		out[key] = canonical
	}
	return out, nil
}

type Fields map[string]Value

func (f Fields) Equal(other Fields) bool {
	if len(f) != len(other) {
		return false
	}
	for key, value := range f {
		otherValue, ok := other[key]
		if !ok || !value.Equal(otherValue) {
			return false
		}
	}
	return true
}

func cloneFields(in Fields) Fields {
	if len(in) == 0 {
		return nil
	}
	out := make(Fields, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}

func normalizeFields(fields Fields) Fields {
	return cloneFields(fields)
}

func (f Fields) duplicateKey() string {
	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		encodeValueForDuplicateKey(&b, f[key])
		b.WriteByte(';')
	}
	return b.String()
}

func canonicalValue(raw any) (Value, error) {
	switch value := raw.(type) {
	case Value:
		return cloneValue(value), nil
	case nil:
		return NullValue(), nil
	case bool:
		return Value{kind: ValueBool, data: value}, nil
	case string:
		return Value{kind: ValueString, data: value}, nil
	case int:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case int8:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case int16:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case int32:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case int64:
		return Value{kind: ValueInt, data: value}, nil
	case uint:
		if uint64(value) > uint64(math.MaxInt64) {
			return Value{}, fmt.Errorf("%w: unsigned integer overflow: %v", ErrUnsupportedValue, value)
		}
		return Value{kind: ValueInt, data: int64(value)}, nil
	case uint8:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case uint16:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case uint32:
		return Value{kind: ValueInt, data: int64(value)}, nil
	case uint64:
		if value > uint64(math.MaxInt64) {
			return Value{}, fmt.Errorf("%w: unsigned integer overflow: %v", ErrUnsupportedValue, value)
		}
		return Value{kind: ValueInt, data: int64(value)}, nil
	case float32:
		return canonicalFloat(float64(value))
	case float64:
		return canonicalFloat(value)
	case map[string]Value:
		return canonicalizeValueMap(value)
	case map[string]any:
		converted := make(map[string]Value, len(value))
		for key, nested := range value {
			next, err := canonicalValue(nested)
			if err != nil {
				return Value{}, err
			}
			converted[key] = next
		}
		return Value{kind: ValueMap, data: converted}, nil
	case []Value:
		out := make([]Value, len(value))
		for i, item := range value {
			canonical, err := canonicalValue(item)
			if err != nil {
				return Value{}, err
			}
			out[i] = canonical
		}
		return Value{kind: ValueList, data: out}, nil
	case []any:
		return canonicalizeSlice(value)
	case []bool:
		out := make([]Value, 0, len(value))
		for _, item := range value {
			out = append(out, Value{kind: ValueBool, data: item})
		}
		return Value{kind: ValueList, data: out}, nil
	case []string:
		out := make([]Value, 0, len(value))
		for _, item := range value {
			out = append(out, Value{kind: ValueString, data: item})
		}
		return Value{kind: ValueList, data: out}, nil
	case []int:
		out := make([]Value, 0, len(value))
		for _, item := range value {
			out = append(out, Value{kind: ValueInt, data: int64(item)})
		}
		return Value{kind: ValueList, data: out}, nil
	}

	reflected := reflect.ValueOf(raw)
	if !reflected.IsValid() {
		return NullValue(), nil
	}
	for reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return NullValue(), nil
		}
		reflected = reflected.Elem()
	}

	switch reflected.Kind() {
	case reflect.Bool:
		return Value{kind: ValueBool, data: reflected.Bool()}, nil
	case reflect.String:
		return Value{kind: ValueString, data: reflected.String()}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Value{kind: ValueInt, data: reflected.Int()}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value := reflected.Uint()
		if value > uint64(math.MaxInt64) {
			return Value{}, fmt.Errorf("%w: unsigned integer overflow: %v", ErrUnsupportedValue, value)
		}
		return Value{kind: ValueInt, data: int64(value)}, nil
	case reflect.Float32, reflect.Float64:
		return canonicalFloat(reflected.Float())
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return Value{}, fmt.Errorf("%w: only string map keys supported, got %s", ErrUnsupportedValue, reflected.Type())
		}
		converted := make(map[string]Value, reflected.Len())
		for _, mapKey := range reflected.MapKeys() {
			rawValue := reflected.MapIndex(mapKey).Interface()
			canonicalValue, err := canonicalValue(rawValue)
			if err != nil {
				return Value{}, err
			}
			converted[mapKey.String()] = canonicalValue
		}
		return Value{kind: ValueMap, data: converted}, nil
	case reflect.Slice, reflect.Array:
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			return Value{kind: ValueList, data: []Value{}}, nil
		}
		converted := make([]Value, 0, reflected.Len())
		for i := 0; i < reflected.Len(); i++ {
			rawValue := reflected.Index(i).Interface()
			canonicalValue, err := canonicalValue(rawValue)
			if err != nil {
				return Value{}, err
			}
			converted = append(converted, canonicalValue)
		}
		return Value{kind: ValueList, data: converted}, nil
	}

	return Value{}, fmt.Errorf("%w: unsupported type %T", ErrUnsupportedValue, raw)
}

func canonicalizeSlice(values []any) (Value, error) {
	out := make([]Value, len(values))
	for i, item := range values {
		canonical, err := canonicalValue(item)
		if err != nil {
			return Value{}, err
		}
		out[i] = canonical
	}
	return Value{kind: ValueList, data: out}, nil
}

func canonicalizeValueMap(values map[string]Value) (Value, error) {
	out := make(map[string]Value, len(values))
	for key, value := range values {
		canonical, err := canonicalValue(value)
		if err != nil {
			return Value{}, err
		}
		out[key] = canonical
	}
	return Value{kind: ValueMap, data: out}, nil
}

func canonicalFloat(value float64) (Value, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return Value{}, fmt.Errorf("%w: non-finite float: %v", ErrUnsupportedValue, value)
	}
	if value == 0 {
		value = 0
	}
	return Value{kind: ValueFloat, data: value}, nil
}

func cloneValue(v Value) Value {
	switch v.kind {
	case ValueList:
		values := v.data.([]Value)
		out := make([]Value, len(values))
		for i, item := range values {
			out[i] = cloneValue(item)
		}
		return Value{kind: v.kind, data: out}
	case ValueMap:
		values := v.data.(map[string]Value)
		out := make(map[string]Value, len(values))
		for key, item := range values {
			out[key] = cloneValue(item)
		}
		return Value{kind: v.kind, data: out}
	default:
		return v
	}
}

func encodeValueForDuplicateKey(b *strings.Builder, value Value) {
	switch value.Kind() {
	case ValueNull:
		b.WriteString("null")
	case ValueBool:
		b.WriteString("bool:")
		if value.data.(bool) {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case ValueInt:
		b.WriteString(numericDuplicateKey(value))
	case ValueFloat:
		b.WriteString(numericDuplicateKey(value))
	case ValueString:
		b.WriteString("string:")
		b.WriteString(strconv.Quote(value.data.(string)))
	case ValueList:
		list := value.data.([]Value)
		b.WriteString("list[")
		for i, item := range list {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeValueForDuplicateKey(b, item)
		}
		b.WriteString("]")
	case ValueMap:
		rawMap := value.data.(map[string]Value)
		keys := make([]string, 0, len(rawMap))
		for key := range rawMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteString("map{")
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(key)
			b.WriteByte('=')
			encodeValueForDuplicateKey(b, rawMap[key])
		}
		b.WriteString("}")
	default:
		b.WriteString("any")
	}
}

func numericValuesEqual(left, right Value) bool {
	switch {
	case left.Kind() == ValueInt && right.Kind() == ValueInt:
		return left.data.(int64) == right.data.(int64)
	case left.Kind() == ValueFloat && right.Kind() == ValueFloat:
		return left.data.(float64) == right.data.(float64)
	case left.Kind() == ValueInt && right.Kind() == ValueFloat:
		return intEqualsFloat(left.data.(int64), right.data.(float64))
	case left.Kind() == ValueFloat && right.Kind() == ValueInt:
		return intEqualsFloat(right.data.(int64), left.data.(float64))
	default:
		return false
	}
}

func intEqualsFloat(integer int64, floating float64) bool {
	if integer > maxExactFloatInt || integer < -maxExactFloatInt {
		return false
	}
	if floating > float64(maxExactFloatInt) || floating < float64(-maxExactFloatInt) {
		return false
	}
	return math.Trunc(floating) == floating && float64(integer) == floating
}

func numericDuplicateKey(value Value) string {
	switch value.Kind() {
	case ValueInt:
		return "number:" + strconv.FormatInt(value.data.(int64), 10)
	case ValueFloat:
		floating := value.data.(float64)
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return "number:" + strconv.FormatInt(int64(floating), 10)
		}
		return "number:" + strconv.FormatFloat(floating, 'g', -1, 64)
	default:
		return "number:invalid"
	}
}

func (v Value) String() string {
	var b strings.Builder
	encodeValueForDuplicateKey(&b, v)
	return b.String()
}
