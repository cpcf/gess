package engine

import (
	"math"
	"math/big"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type ValueKind = gessrules.ValueKind

const (
	valueKindUnknown ValueKind = 0
	ValueAny         ValueKind = gessrules.ValueAny
	ValueNull        ValueKind = gessrules.ValueNull
	ValueBool        ValueKind = gessrules.ValueBool
	ValueInt         ValueKind = gessrules.ValueInt
	ValueFloat       ValueKind = gessrules.ValueFloat
	ValueString      ValueKind = gessrules.ValueString
	ValueList        ValueKind = gessrules.ValueList
	ValueMap         ValueKind = gessrules.ValueMap
	valueKindInvalid ValueKind = 255
)

const maxExactFloatInt = int64(1 << 53)

type Value = gessrules.Value
type Fields = gessrules.Fields

var ErrUnsupportedValue = gessrules.ErrUnsupportedValue

func parseValueKind(kind string) ValueKind {
	switch strings.ToLower(kind) {
	case "any":
		return ValueAny
	case "null":
		return ValueNull
	case "bool", "boolean":
		return ValueBool
	case "int", "integer", "number":
		return ValueInt
	case "float":
		return ValueFloat
	case "string", "symbol":
		return ValueString
	case "list":
		return ValueList
	case "map":
		return ValueMap
	default:
		return valueKindInvalid
	}
}

func NullValue() Value {
	return gessrules.NullValue()
}

func newBoolValue(value bool) Value {
	return gessrules.BoolValue(value)
}

func newIntValue(value int64) Value {
	return gessrules.IntValue(value)
}

func newFloatValue(value float64) Value {
	return gessrules.FloatValue(value)
}

func newStringValue(value string) Value {
	return gessrules.StringValue(value)
}

func NewValue(raw any) (Value, error) {
	return gessrules.NewValue(raw)
}

func NewFields(raw map[string]any) (Fields, error) {
	return gessrules.NewFields(raw)
}

func NewFieldsFromPairs(pairs ...any) (Fields, error) {
	return gessrules.NewFieldsFromPairs(pairs...)
}

func MustFields(pairs ...any) Fields {
	return gessrules.MustFields(pairs...)
}

func canonicalValue(raw any) (Value, error) {
	return gessrules.CanonicalValue(raw)
}

func canonicalFloat(value float64) (Value, error) {
	return gessrules.NewValue(value)
}

func cloneValue(v Value) Value {
	return gessrules.CloneValue(v)
}

func cloneValueSlice(in []Value) []Value {
	return gessrules.CloneValueSlice(in)
}

func cloneFields(in Fields) Fields {
	return gessrules.CloneFields(in)
}

func normalizeFields(fields Fields) Fields {
	return gessrules.NormalizeFields(fields)
}

func valueShareable(v Value) bool {
	return gessrules.ValueShareable(v)
}

func fieldsShareable(fields Fields) bool {
	return gessrules.FieldsShareable(fields)
}

func factSlotsShareable(slots []factSlot) bool {
	for _, slot := range slots {
		if !valueShareable(slot.value) {
			return false
		}
	}
	return true
}

func compareValues(left, right Value) (int, bool) {
	return gessrules.CompareValues(left, right)
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
	comparison, _ := compareValues(left, right)
	return comparison
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
	leftValue := new(big.Float).SetPrec(256).SetInt64(integer)
	rightValue := new(big.Float).SetPrec(256).SetFloat64(floating)
	return leftValue.Cmp(rightValue)
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

func encodeValueForDuplicateKey(b *strings.Builder, value Value) {
	gessrules.EncodeValueForDuplicateKey(b, value)
}

func duplicateKeyValueCapacity(value Value) int {
	return gessrules.DuplicateKeyValueCapacity(value)
}

func int64Len(value int64) int {
	return gessrules.Int64Len(value)
}

func valueBool(value Value) bool {
	out, _ := value.AsBool()
	return out
}

func valueInt64(value Value) int64 {
	out, _ := value.AsInt64()
	return out
}

func valueFloat64(value Value) float64 {
	out, _ := value.AsFloat64()
	return out
}

func valueString(value Value) string {
	out, _ := value.AsString()
	return out
}

func valueList(value Value) []Value {
	out, _ := value.AsList()
	return out
}

func valueMap(value Value) map[string]Value {
	out, _ := value.AsMap()
	return out
}
