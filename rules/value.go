package rules

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Value is an immutable, typed field or expression value.
type Value struct {
	kind        ValueKind
	boolValue   bool
	intValue    int64
	floatValue  float64
	stringValue string
	data        any
}

const maxExactFloatInt = int64(1 << 53)

func NullValue() Value {
	return Value{kind: ValueNull}
}

func newBoolValue(value bool) Value {
	return Value{kind: ValueBool, boolValue: value}
}

func newIntValue(value int64) Value {
	return Value{kind: ValueInt, intValue: value}
}

func newFloatValue(value float64) Value {
	return Value{kind: ValueFloat, floatValue: value}
}

func newStringValue(value string) Value {
	return Value{kind: ValueString, stringValue: value}
}

// BoolValue returns a bool value.
func BoolValue(value bool) Value {
	return newBoolValue(value)
}

// IntValue returns an integer value.
func IntValue(value int64) Value {
	return newIntValue(value)
}

// FloatValue returns a float value.
func FloatValue(value float64) Value {
	return newFloatValue(value)
}

// StringValue returns a string value.
func StringValue(value string) Value {
	return newStringValue(value)
}

func (v Value) Kind() ValueKind {
	if v.kind == valueKindUnknown {
		return ValueNull
	}
	return v.kind
}

// AsBool returns the stored bool when the value kind is ValueBool.
func (v Value) AsBool() (bool, bool) {
	if v.Kind() != ValueBool {
		return false, false
	}
	return v.boolValue, true
}

// AsInt64 returns the stored integer when the value kind is ValueInt.
func (v Value) AsInt64() (int64, bool) {
	if v.Kind() != ValueInt {
		return 0, false
	}
	return v.intValue, true
}

// AsFloat64 returns the stored float when the value kind is ValueFloat.
func (v Value) AsFloat64() (float64, bool) {
	if v.Kind() != ValueFloat {
		return 0, false
	}
	return v.floatValue, true
}

// AsString returns the stored string when the value kind is ValueString.
func (v Value) AsString() (string, bool) {
	if v.Kind() != ValueString {
		return "", false
	}
	return v.stringValue, true
}

// AsList returns a defensive copy of the stored list when the value kind is
// ValueList.
func (v Value) AsList() ([]Value, bool) {
	if v.Kind() != ValueList {
		return nil, false
	}
	return cloneValueSlice(v.data.([]Value)), true
}

// AsMap returns a defensive copy of the stored map when the value kind is
// ValueMap.
func (v Value) AsMap() (map[string]Value, bool) {
	if v.Kind() != ValueMap {
		return nil, false
	}
	values := v.data.(map[string]Value)
	out := make(map[string]Value, len(values))
	for key, value := range values {
		out[key] = cloneValue(value)
	}
	return out, true
}

// ListLen returns the list length when the value kind is ValueList.
func (v Value) ListLen() (int, bool) {
	if v.Kind() != ValueList {
		return 0, false
	}
	return len(v.data.([]Value)), true
}

// ListAt returns one list item when the value kind is ValueList.
func (v Value) ListAt(index int) (Value, bool) {
	if v.Kind() != ValueList {
		return Value{}, false
	}
	values := v.data.([]Value)
	if index < 0 || index >= len(values) {
		return Value{}, false
	}
	return cloneValue(values[index]), true
}

// MapGet returns one map value when the value kind is ValueMap.
func (v Value) MapGet(key string) (Value, bool) {
	if v.Kind() != ValueMap {
		return Value{}, false
	}
	value, ok := v.data.(map[string]Value)[key]
	if !ok {
		return Value{}, false
	}
	return cloneValue(value), true
}

// RangeList visits list items when the value kind is ValueList.
func (v Value) RangeList(fn func(int, Value) bool) bool {
	if v.Kind() != ValueList {
		return false
	}
	for i, value := range v.data.([]Value) {
		if !fn(i, cloneValue(value)) {
			break
		}
	}
	return true
}

// RangeMap visits map entries when the value kind is ValueMap.
func (v Value) RangeMap(fn func(string, Value) bool) bool {
	if v.Kind() != ValueMap {
		return false
	}
	for key, value := range v.data.(map[string]Value) {
		if !fn(key, cloneValue(value)) {
			break
		}
	}
	return true
}

func NewValue(raw any) (Value, error) {
	return canonicalValue(raw)
}

// CanonicalValue converts raw into a Value.
func CanonicalValue(raw any) (Value, error) {
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
		return v.boolValue == other.boolValue
	case ValueInt:
		return numericValuesEqual(v, other)
	case ValueFloat:
		return numericValuesEqual(v, other)
	case ValueString:
		if other.Kind() != ValueString {
			return false
		}
		return v.stringValue == other.stringValue
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

// CanonicalKey returns a deterministic key for duplicate detection and stable
// ordering.
func (v Value) CanonicalKey() string {
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

// NewFieldsFromPairs builds fields from alternating string keys and raw values.
func NewFieldsFromPairs(pairs ...any) (Fields, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("%w: fields require key/value pairs", ErrUnsupportedValue)
	}
	out := make(Fields, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok || key == "" {
			return nil, fmt.Errorf("%w: field key at argument %d must be a non-empty string", ErrUnsupportedValue, i)
		}
		value, err := canonicalValue(pairs[i+1])
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		out[key] = value
	}
	return out, nil
}

// MustFields builds fields from alternating string keys and raw values, panicking
// when the inputs cannot be converted.
func MustFields(pairs ...any) Fields {
	fields, err := NewFieldsFromPairs(pairs...)
	if err != nil {
		panic(err)
	}
	return fields
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

// CloneFields returns a defensive copy of fields.
func CloneFields(in Fields) Fields {
	if len(in) == 0 {
		return nil
	}
	out := make(Fields, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneFields(in Fields) Fields {
	return CloneFields(in)
}

// NormalizeFields returns fields in canonical immutable form.
func NormalizeFields(fields Fields) Fields {
	return cloneFields(fields)
}

func normalizeFields(fields Fields) Fields {
	return NormalizeFields(fields)
}

// DuplicateKey returns a deterministic duplicate key for fields.
func (f Fields) DuplicateKey() string {
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
		return newBoolValue(value), nil
	case string:
		return newStringValue(value), nil
	case int:
		return newIntValue(int64(value)), nil
	case int8:
		return newIntValue(int64(value)), nil
	case int16:
		return newIntValue(int64(value)), nil
	case int32:
		return newIntValue(int64(value)), nil
	case int64:
		return newIntValue(value), nil
	case uint:
		if uint64(value) > uint64(math.MaxInt64) {
			return Value{}, fmt.Errorf("%w: unsigned integer overflow: %v", ErrUnsupportedValue, value)
		}
		return newIntValue(int64(value)), nil
	case uint8:
		return newIntValue(int64(value)), nil
	case uint16:
		return newIntValue(int64(value)), nil
	case uint32:
		return newIntValue(int64(value)), nil
	case uint64:
		if value > uint64(math.MaxInt64) {
			return Value{}, fmt.Errorf("%w: unsigned integer overflow: %v", ErrUnsupportedValue, value)
		}
		return newIntValue(int64(value)), nil
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
			out = append(out, newBoolValue(item))
		}
		return Value{kind: ValueList, data: out}, nil
	case []string:
		out := make([]Value, 0, len(value))
		for _, item := range value {
			out = append(out, newStringValue(item))
		}
		return Value{kind: ValueList, data: out}, nil
	case []int:
		out := make([]Value, 0, len(value))
		for _, item := range value {
			out = append(out, newIntValue(int64(item)))
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
		return newBoolValue(reflected.Bool()), nil
	case reflect.String:
		return newStringValue(reflected.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return newIntValue(reflected.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value := reflected.Uint()
		if value > uint64(math.MaxInt64) {
			return Value{}, fmt.Errorf("%w: unsigned integer overflow: %v", ErrUnsupportedValue, value)
		}
		return newIntValue(int64(value)), nil
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
	return newFloatValue(value), nil
}

// CloneValue returns a defensive copy of v.
func CloneValue(v Value) Value {
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

func cloneValue(v Value) Value {
	return CloneValue(v)
}

// CloneValueSlice returns a defensive copy of in.
func CloneValueSlice(in []Value) []Value {
	if len(in) == 0 {
		return nil
	}
	out := make([]Value, len(in))
	for i, value := range in {
		out[i] = cloneValue(value)
	}
	return out
}

func cloneValueSlice(in []Value) []Value {
	return CloneValueSlice(in)
}

// ValueShareable reports whether v can be shared without defensive cloning.
func ValueShareable(v Value) bool {
	switch v.Kind() {
	case ValueList, ValueMap:
		return false
	default:
		return true
	}
}

func valueShareable(v Value) bool {
	return ValueShareable(v)
}

// FieldsShareable reports whether all field values can be shared without
// defensive cloning.
func FieldsShareable(fields Fields) bool {
	for _, value := range fields {
		if !valueShareable(value) {
			return false
		}
	}
	return true
}

func fieldsShareable(fields Fields) bool {
	return FieldsShareable(fields)
}

// EncodeValueForDuplicateKey appends value's deterministic duplicate-key
// representation.
func EncodeValueForDuplicateKey(b *strings.Builder, value Value) {
	switch value.Kind() {
	case ValueNull:
		b.WriteString("null")
	case ValueBool:
		b.WriteString("bool:")
		if value.boolValue {
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
		b.WriteString(strconv.Quote(value.stringValue))
	case ValueList:
		list := value.data.([]Value)
		b.WriteString("list[")
		for i, item := range list {
			if i > 0 {
				b.WriteByte(',')
			}
			EncodeValueForDuplicateKey(b, item)
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
			EncodeValueForDuplicateKey(b, rawMap[key])
		}
		b.WriteString("}")
	default:
		b.WriteString("any")
	}
}

func encodeValueForDuplicateKey(b *strings.Builder, value Value) {
	EncodeValueForDuplicateKey(b, value)
}

func numericValuesEqual(left, right Value) bool {
	switch {
	case left.Kind() == ValueInt && right.Kind() == ValueInt:
		return left.intValue == right.intValue
	case left.Kind() == ValueFloat && right.Kind() == ValueFloat:
		return left.floatValue == right.floatValue
	case left.Kind() == ValueInt && right.Kind() == ValueFloat:
		return intEqualsFloat(left.intValue, right.floatValue)
	case left.Kind() == ValueFloat && right.Kind() == ValueInt:
		return intEqualsFloat(right.intValue, left.floatValue)
	default:
		return false
	}
}

// CompareValues compares string or numeric values.
func CompareValues(left, right Value) (int, bool) {
	switch {
	case left.Kind() == ValueString && right.Kind() == ValueString:
		return strings.Compare(left.stringValue, right.stringValue), true
	case isNumericValue(left) && isNumericValue(right):
		return compareNumericValues(left, right), true
	default:
		return 0, false
	}
}

func compareValues(left, right Value) (int, bool) {
	return CompareValues(left, right)
}

func isNumericValue(value Value) bool {
	switch value.Kind() {
	case ValueInt, ValueFloat:
		return true
	default:
		return false
	}
}

func compareNumericValues(left, right Value) int {
	switch left.Kind() {
	case ValueInt:
		return compareNumericValuesWithInt(left, right)
	case ValueFloat:
		return compareNumericValuesWithFloat(left, right)
	}
	return 0
}

func compareNumericValuesWithInt(left, right Value) int {
	leftInt := left.intValue
	switch right.Kind() {
	case ValueInt:
		rightInt := right.intValue
		switch {
		case leftInt < rightInt:
			return -1
		case leftInt > rightInt:
			return 1
		default:
			return 0
		}
	case ValueFloat:
		return compareIntAndFloatValues(leftInt, right.floatValue)
	default:
		return 0
	}
}

func compareNumericValuesWithFloat(left, right Value) int {
	leftFloat := left.floatValue
	switch right.Kind() {
	case ValueInt:
		return -compareIntAndFloatValues(right.intValue, leftFloat)
	case ValueFloat:
		rightFloat := right.floatValue
		switch {
		case leftFloat < rightFloat:
			return -1
		case leftFloat > rightFloat:
			return 1
		default:
			return 0
		}
	default:
		return 0
	}
}

func compareIntAndFloatValues(integer int64, floating float64) int {
	if integer >= -maxExactFloatInt && integer <= maxExactFloatInt {
		exact := float64(integer)
		switch {
		case exact < floating:
			return -1
		case exact > floating:
			return 1
		default:
			return 0
		}
	}
	return compareNumericValuesSlow(integer, floating)
}

func compareNumericValuesSlow(integer int64, floating float64) int {
	leftValue := new(big.Float).SetPrec(256).SetInt64(integer)
	rightValue := new(big.Float).SetPrec(256).SetFloat64(floating)
	return leftValue.Cmp(rightValue)
}

func intEqualsFloat(integer int64, floating float64) bool {
	if math.IsNaN(floating) || math.IsInf(floating, 0) {
		return false
	}
	if integer >= -maxExactFloatInt && integer <= maxExactFloatInt {
		return math.Trunc(floating) == floating && float64(integer) == floating
	}
	return compareNumericValuesSlow(integer, floating) == 0
}

func numericDuplicateKey(value Value) string {
	switch value.Kind() {
	case ValueInt:
		return "number:" + strconv.FormatInt(value.intValue, 10)
	case ValueFloat:
		floating := value.floatValue
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

// DuplicateKeyValueCapacity returns the expected encoded capacity for value.
func DuplicateKeyValueCapacity(value Value) int {
	switch value.Kind() {
	case ValueNull:
		return len("null")
	case ValueBool:
		if value.boolValue {
			return len("bool:true")
		}
		return len("bool:false")
	case ValueInt:
		return len("number:") + int64Len(value.intValue)
	case ValueFloat:
		floating := value.floatValue
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return len("number:") + int64Len(int64(floating))
		}
		var buf [32]byte
		return len("number:") + len(strconv.AppendFloat(buf[:0], floating, 'g', -1, 64))
	case ValueString:
		return len("string:") + len(value.stringValue) + len(`""`) + len(`\u0000`)
	case ValueList:
		list := value.data.([]Value)
		size := len("list[")
		for i, item := range list {
			if i > 0 {
				size++
			}
			size += DuplicateKeyValueCapacity(item)
		}
		return size + len("]")
	case ValueMap:
		rawMap := value.data.(map[string]Value)
		size := len("map{")
		first := true
		for key, value := range rawMap {
			if !first {
				size++
			}
			first = false
			size += len(key) + 1 + DuplicateKeyValueCapacity(value)
		}
		return size + len("}")
	default:
		return len("any")
	}
}

func duplicateKeyValueCapacity(value Value) int {
	return DuplicateKeyValueCapacity(value)
}

// Int64Len returns the decimal byte length of value.
func Int64Len(value int64) int {
	var buf [20]byte
	return len(strconv.AppendInt(buf[:0], value, 10))
}

func int64Len(value int64) int {
	return Int64Len(value)
}

func (v Value) String() string {
	var b strings.Builder
	encodeValueForDuplicateKey(&b, v)
	return b.String()
}
