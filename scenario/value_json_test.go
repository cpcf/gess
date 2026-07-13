package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/cpcf/gess/rules"
)

func TestValueJSONRoundTripsEveryKindCanonically(t *testing.T) {
	t.Parallel()

	negativeZero := math.Copysign(0, -1)
	nestedList := mustRulesValue(t, []rules.Value{
		rules.NullValue(),
		rules.FloatValue(negativeZero),
	})
	nestedMap := mustRulesValue(t, map[string]rules.Value{
		"é": rules.StringValue("snow ☃"),
		"a": nestedList,
		"":  rules.BoolValue(false),
	})

	tests := []struct {
		name  string
		value rules.Value
		want  string
	}{
		{name: "null", value: rules.NullValue(), want: `{"kind":"null"}`},
		{name: "zero value is null", value: rules.Value{}, want: `{"kind":"null"}`},
		{name: "bool false", value: rules.BoolValue(false), want: `{"kind":"bool","bool":false}`},
		{name: "bool true", value: rules.BoolValue(true), want: `{"kind":"bool","bool":true}`},
		{name: "int zero", value: rules.IntValue(0), want: `{"kind":"int","int":"0"}`},
		{name: "int minimum", value: rules.IntValue(math.MinInt64), want: `{"kind":"int","int":"-9223372036854775808"}`},
		{name: "int maximum", value: rules.IntValue(math.MaxInt64), want: `{"kind":"int","int":"9223372036854775807"}`},
		{name: "float integer", value: rules.FloatValue(1), want: `{"kind":"float","float":"1"}`},
		{name: "float fraction", value: rules.FloatValue(-12.5), want: `{"kind":"float","float":"-12.5"}`},
		{name: "float negative zero", value: rules.FloatValue(negativeZero), want: `{"kind":"float","float":"-0"}`},
		{name: "float smallest", value: rules.FloatValue(math.SmallestNonzeroFloat64), want: `{"kind":"float","float":"5e-324"}`},
		{name: "float maximum", value: rules.FloatValue(math.MaxFloat64), want: `{"kind":"float","float":"1.7976931348623157e+308"}`},
		{name: "string", value: rules.StringValue("line\n\"<&>"), want: `{"kind":"string","string":"line\n\"\u003c\u0026\u003e"}`},
		{name: "empty list", value: mustRulesValue(t, []rules.Value{}), want: `{"kind":"list","list":[]}`},
		{name: "nested list", value: nestedList, want: `{"kind":"list","list":[{"kind":"null"},{"kind":"float","float":"-0"}]}`},
		{name: "empty map", value: mustRulesValue(t, map[string]rules.Value{}), want: `{"kind":"map","map":[]}`},
		{
			name:  "nested sorted map",
			value: nestedMap,
			want:  `{"kind":"map","map":[{"key":"","value":{"kind":"bool","bool":false}},{"key":"a","value":{"kind":"list","list":[{"kind":"null"},{"kind":"float","float":"-0"}]}},{"key":"é","value":{"kind":"string","string":"snow ☃"}}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := MarshalValue(test.value)
			if err != nil {
				t.Fatalf("MarshalValue() error = %v", err)
			}
			if string(encoded) != test.want {
				t.Fatalf("MarshalValue() = %s, want %s", encoded, test.want)
			}

			decoded, err := UnmarshalValue(encoded)
			if err != nil {
				t.Fatalf("UnmarshalValue() error = %v", err)
			}
			assertSameRulesValue(t, decoded, test.value)

			reencoded, err := MarshalValue(decoded)
			if err != nil {
				t.Fatalf("MarshalValue(decoded) error = %v", err)
			}
			if !bytes.Equal(reencoded, encoded) {
				t.Fatalf("canonical re-encoding = %s, want %s", reencoded, encoded)
			}
		})
	}
}

func TestValueJSONPreservesNumericKindAndNegativeZero(t *testing.T) {
	t.Parallel()

	intJSON, err := MarshalValue(rules.IntValue(1))
	if err != nil {
		t.Fatal(err)
	}
	floatJSON, err := MarshalValue(rules.FloatValue(1))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(intJSON, floatJSON) {
		t.Fatalf("int and float encodings unexpectedly match: %s", intJSON)
	}

	decodedInt, err := UnmarshalValue(intJSON)
	if err != nil {
		t.Fatal(err)
	}
	decodedFloat, err := UnmarshalValue(floatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if decodedInt.Kind() != rules.ValueInt || decodedFloat.Kind() != rules.ValueFloat {
		t.Fatalf("decoded kinds = %s and %s, want int and float", decodedInt.Kind(), decodedFloat.Kind())
	}

	decodedNegativeZero, err := UnmarshalValue([]byte(`{"kind":"float","float":"-0"}`))
	if err != nil {
		t.Fatal(err)
	}
	zero, ok := decodedNegativeZero.AsFloat64()
	if !ok || !math.Signbit(zero) {
		t.Fatalf("decoded -0 = %v (float = %t, signbit = %t)", zero, ok, math.Signbit(zero))
	}
	encodedNegativeZero, err := MarshalValue(decodedNegativeZero)
	if err != nil {
		t.Fatal(err)
	}
	if string(encodedNegativeZero) != `{"kind":"float","float":"-0"}` {
		t.Fatalf("re-encoded -0 = %s", encodedNegativeZero)
	}
}

func TestValueJSONMapEncodingIsDeterministic(t *testing.T) {
	t.Parallel()

	leftRaw := make(map[string]rules.Value)
	leftRaw["z"] = rules.IntValue(3)
	leftRaw[""] = rules.IntValue(1)
	leftRaw["a"] = rules.IntValue(2)
	rightRaw := make(map[string]rules.Value)
	rightRaw["a"] = rules.IntValue(2)
	rightRaw[""] = rules.IntValue(1)
	rightRaw["z"] = rules.IntValue(3)

	left := mustRulesValue(t, leftRaw)
	right := mustRulesValue(t, rightRaw)
	want := `{"kind":"map","map":[{"key":"","value":{"kind":"int","int":"1"}},{"key":"a","value":{"kind":"int","int":"2"}},{"key":"z","value":{"kind":"int","int":"3"}}]}`

	leftJSON, err := MarshalValue(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := MarshalValue(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != want || !bytes.Equal(leftJSON, rightJSON) {
		t.Fatalf("map encodings = %s and %s, want identical %s", leftJSON, rightJSON, want)
	}
	for i := range 100 {
		again, err := MarshalValue(left)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, leftJSON) {
			t.Fatalf("encoding %d = %s, first encoding = %s", i, again, leftJSON)
		}
	}
}

func TestValueJSONWrapperImplementsJSONInterfaces(t *testing.T) {
	t.Parallel()

	type document struct {
		Payload Value `json:"payload"`
	}

	want := mustRulesValue(t, map[string]rules.Value{
		"answer": rules.IntValue(42),
	})
	encoded, err := json.Marshal(document{Payload: NewValue(want)})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	const wantJSON = `{"payload":{"kind":"map","map":[{"key":"answer","value":{"kind":"int","int":"42"}}]}}`
	if string(encoded) != wantJSON {
		t.Fatalf("json.Marshal() = %s, want %s", encoded, wantJSON)
	}

	var decoded document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertSameRulesValue(t, decoded.Payload.RulesValue(), want)

	zeroJSON, err := json.Marshal(Value{})
	if err != nil {
		t.Fatal(err)
	}
	if string(zeroJSON) != `{"kind":"null"}` {
		t.Fatalf("zero Value JSON = %s", zeroJSON)
	}

	unchanged := NewValue(rules.StringValue("before"))
	err = json.Unmarshal([]byte(`{"kind":"int","int":"01"}`), &unchanged)
	if !errors.Is(err, ErrInvalidValueJSON) {
		t.Fatalf("invalid wrapper error = %v, want ErrInvalidValueJSON", err)
	}
	assertSameRulesValue(t, unchanged.RulesValue(), rules.StringValue("before"))

	var nilValue *Value
	err = nilValue.UnmarshalJSON([]byte(`{"kind":"null"}`))
	if !errors.Is(err, ErrInvalidValueJSON) {
		t.Fatalf("nil receiver error = %v, want ErrInvalidValueJSON", err)
	}
}

func TestValueJSONWrapperDefensivelyCopiesContainers(t *testing.T) {
	t.Parallel()

	sourceList := mustRulesValue(t, []rules.Value{rules.StringValue("before")})
	source := mustRulesValue(t, map[string]rules.Value{"nested": sourceList})
	wrapper := NewValue(source)

	sourceMap, _ := source.AsMapShared()
	sourceNested, _ := sourceMap["nested"].AsListShared()
	sourceNested[0] = rules.StringValue("source changed")
	sourceMap["extra"] = rules.BoolValue(true)

	want := mustRulesValue(t, map[string]rules.Value{
		"nested": mustRulesValue(t, []rules.Value{rules.StringValue("before")}),
	})
	first := wrapper.RulesValue()
	assertSameRulesValue(t, first, want)

	firstMap, _ := first.AsMapShared()
	firstNested, _ := firstMap["nested"].AsListShared()
	firstNested[0] = rules.StringValue("returned copy changed")
	firstMap["another"] = rules.NullValue()
	assertSameRulesValue(t, wrapper.RulesValue(), want)

	input := []byte(`{"kind":"map","map":[{"key":"key","value":{"kind":"string","string":"original"}}]}`)
	decoded, err := UnmarshalValue(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = 'x'
	}
	decodedMap, _ := decoded.AsMap()
	decodedString, _ := decodedMap["key"].AsString()
	if decodedString != "original" {
		t.Fatalf("decoded string changed with input buffer: %q", decodedString)
	}
}

func TestMarshalValueRejectsInvalidRulesValues(t *testing.T) {
	t.Parallel()

	invalidString := string([]byte{0xff})
	invalidKeyMap := mustRulesValue(t, map[string]rules.Value{invalidString: rules.NullValue()})
	nestedNonFinite := mustRulesValue(t, []rules.Value{rules.FloatValue(math.Inf(1))})
	tests := []struct {
		name  string
		value rules.Value
	}{
		{name: "NaN", value: rules.FloatValue(math.NaN())},
		{name: "positive infinity", value: rules.FloatValue(math.Inf(1))},
		{name: "negative infinity", value: rules.FloatValue(math.Inf(-1))},
		{name: "invalid UTF-8 string", value: rules.StringValue(invalidString)},
		{name: "invalid UTF-8 map key", value: invalidKeyMap},
		{name: "nested non-finite float", value: nestedNonFinite},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := MarshalValue(test.value)
			if !errors.Is(err, ErrInvalidValueJSON) {
				t.Fatalf("MarshalValue() error = %v, want ErrInvalidValueJSON", err)
			}
			if encoded != nil {
				t.Fatalf("MarshalValue() bytes = %s, want nil", encoded)
			}
		})
	}
}

func TestUnmarshalValueAcceptsWhitespaceAndObjectMemberOrder(t *testing.T) {
	t.Parallel()

	decoded, err := UnmarshalValue([]byte(" \n\t{\"int\":\"42\",\"kind\":\"int\"}\r\n"))
	if err != nil {
		t.Fatalf("UnmarshalValue() error = %v", err)
	}
	assertSameRulesValue(t, decoded, rules.IntValue(42))

	mapJSON := []byte(`{"map":[{"value":{"kind":"null"},"key":""}],"kind":"map"}`)
	decoded, err = UnmarshalValue(mapJSON)
	if err != nil {
		t.Fatalf("UnmarshalValue(reordered map) error = %v", err)
	}
	assertSameRulesValue(t, decoded, mustRulesValue(t, map[string]rules.Value{"": rules.NullValue()}))

	decoded, err = UnmarshalValue([]byte(`{"kind":"string","string":"\ud83d\ude00"}`))
	if err != nil {
		t.Fatalf("UnmarshalValue(surrogate pair) error = %v", err)
	}
	assertSameRulesValue(t, decoded, rules.StringValue("😀"))
}

func TestUnmarshalValueRejectsInvalidCorpus(t *testing.T) {
	t.Parallel()

	validNull := `{"kind":"null"}`
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "whitespace only", data: []byte(" \n\t")},
		{name: "literal null", data: []byte(`null`)},
		{name: "literal bool", data: []byte(`true`)},
		{name: "literal number", data: []byte(`1`)},
		{name: "literal string", data: []byte(`"x"`)},
		{name: "top-level array", data: []byte(`[]`)},
		{name: "empty object", data: []byte(`{}`)},
		{name: "missing kind", data: []byte(`{"int":"1"}`)},
		{name: "non-string kind null", data: []byte(`{"kind":null}`)},
		{name: "non-string kind number", data: []byte(`{"kind":1}`)},
		{name: "unknown kind", data: []byte(`{"kind":"decimal","decimal":"1"}`)},
		{name: "null extra payload", data: []byte(`{"kind":"null","null":null}`)},
		{name: "null ambiguous payload", data: []byte(`{"kind":"null","bool":false}`)},
		{name: "duplicate kind", data: []byte(`{"kind":"null","kind":"null"}`)},
		{name: "duplicate semantic kind", data: []byte(`{"kind":"null","k\u0069nd":"null"}`)},
		{name: "bool missing payload", data: []byte(`{"kind":"bool"}`)},
		{name: "bool null payload", data: []byte(`{"kind":"bool","bool":null}`)},
		{name: "bool string payload", data: []byte(`{"kind":"bool","bool":"true"}`)},
		{name: "bool ambiguous payload", data: []byte(`{"kind":"bool","bool":true,"int":"1"}`)},
		{name: "bool duplicate payload", data: []byte(`{"kind":"bool","bool":true,"bool":false}`)},
		{name: "int missing payload", data: []byte(`{"kind":"int"}`)},
		{name: "int number payload", data: []byte(`{"kind":"int","int":1}`)},
		{name: "int null payload", data: []byte(`{"kind":"int","int":null}`)},
		{name: "int empty", data: []byte(`{"kind":"int","int":""}`)},
		{name: "int plus", data: []byte(`{"kind":"int","int":"+1"}`)},
		{name: "int leading zero", data: []byte(`{"kind":"int","int":"01"}`)},
		{name: "int leading zero example", data: []byte(`{"kind":"int","int":"030"}`)},
		{name: "int negative zero", data: []byte(`{"kind":"int","int":"-0"}`)},
		{name: "int decimal", data: []byte(`{"kind":"int","int":"1.0"}`)},
		{name: "int whitespace", data: []byte(`{"kind":"int","int":" 1"}`)},
		{name: "int positive overflow", data: []byte(`{"kind":"int","int":"9223372036854775808"}`)},
		{name: "int negative overflow", data: []byte(`{"kind":"int","int":"-9223372036854775809"}`)},
		{name: "float missing payload", data: []byte(`{"kind":"float"}`)},
		{name: "float number payload", data: []byte(`{"kind":"float","float":1}`)},
		{name: "float NaN", data: []byte(`{"kind":"float","float":"NaN"}`)},
		{name: "float positive infinity", data: []byte(`{"kind":"float","float":"+Inf"}`)},
		{name: "float negative infinity", data: []byte(`{"kind":"float","float":"-Inf"}`)},
		{name: "float long zero", data: []byte(`{"kind":"float","float":"0.0"}`)},
		{name: "float long negative zero", data: []byte(`{"kind":"float","float":"-0.0"}`)},
		{name: "float redundant fraction", data: []byte(`{"kind":"float","float":"1.0"}`)},
		{name: "float redundant exponent", data: []byte(`{"kind":"float","float":"1e0"}`)},
		{name: "float noncanonical exponent", data: []byte(`{"kind":"float","float":"1e6"}`)},
		{name: "float uppercase exponent", data: []byte(`{"kind":"float","float":"1E+06"}`)},
		{name: "float hex", data: []byte(`{"kind":"float","float":"0x1p+0"}`)},
		{name: "float overflow", data: []byte(`{"kind":"float","float":"1e+999"}`)},
		{name: "float underflow", data: []byte(`{"kind":"float","float":"1e-999"}`)},
		{name: "string missing payload", data: []byte(`{"kind":"string"}`)},
		{name: "string number payload", data: []byte(`{"kind":"string","string":1}`)},
		{name: "string lone high surrogate", data: []byte(`{"kind":"string","string":"\ud800"}`)},
		{name: "string lone low surrogate", data: []byte(`{"kind":"string","string":"\udc00"}`)},
		{name: "string mismatched surrogate", data: []byte(`{"kind":"string","string":"\ud800\u0041"}`)},
		{name: "list missing payload", data: []byte(`{"kind":"list"}`)},
		{name: "list object payload", data: []byte(`{"kind":"list","list":{}}`)},
		{name: "list untagged scalar child", data: []byte(`{"kind":"list","list":[1]}`)},
		{name: "list untagged object child", data: []byte(`{"kind":"list","list":[{"int":"1"}]}`)},
		{name: "list child extra member", data: []byte(`{"kind":"list","list":[{"kind":"null","extra":0}]}`)},
		{name: "map missing payload", data: []byte(`{"kind":"map"}`)},
		{name: "map object payload", data: []byte(`{"kind":"map","map":{}}`)},
		{name: "map scalar entry", data: []byte(`{"kind":"map","map":[1]}`)},
		{name: "map array entry", data: []byte(`{"kind":"map","map":[[]]}`)},
		{name: "map entry missing key", data: []byte(`{"kind":"map","map":[{"value":` + validNull + `}]}`)},
		{name: "map entry missing value", data: []byte(`{"kind":"map","map":[{"key":"a"}]}`)},
		{name: "map entry extra member", data: []byte(`{"kind":"map","map":[{"key":"a","value":` + validNull + `,"extra":0}]}`)},
		{name: "map entry duplicate key member", data: []byte(`{"kind":"map","map":[{"key":"a","key":"b","value":` + validNull + `}]}`)},
		{name: "map entry non-string key", data: []byte(`{"kind":"map","map":[{"key":1,"value":` + validNull + `}]}`)},
		{name: "map entry untagged value", data: []byte(`{"kind":"map","map":[{"key":"a","value":null}]}`)},
		{name: "map duplicate keys", data: []byte(`{"kind":"map","map":[{"key":"a","value":` + validNull + `},{"key":"a","value":` + validNull + `}]}`)},
		{name: "map semantic duplicate keys", data: []byte(`{"kind":"map","map":[{"key":"a","value":` + validNull + `},{"key":"\u0061","value":` + validNull + `}]}`)},
		{name: "map unsorted keys", data: []byte(`{"kind":"map","map":[{"key":"b","value":` + validNull + `},{"key":"a","value":` + validNull + `}]}`)},
		{name: "map empty key after nonempty", data: []byte(`{"kind":"map","map":[{"key":"a","value":` + validNull + `},{"key":"","value":` + validNull + `}]}`)},
		{name: "trailing object", data: []byte(validNull + `{}`)},
		{name: "trailing scalar", data: []byte(validNull + ` true`)},
		{name: "trailing comma", data: []byte(`{"kind":"null",}`)},
		{name: "truncated object", data: []byte(`{"kind":"null"`)},
		{name: "invalid escape", data: []byte(`{"kind":"string","string":"\q"}`)},
		{name: "invalid unicode escape", data: []byte(`{"kind":"string","string":"\uZZZZ"}`)},
		{name: "invalid UTF-8 payload", data: invalidUTF8ValueJSON()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := UnmarshalValue(test.data)
			if !errors.Is(err, ErrInvalidValueJSON) {
				t.Fatalf("UnmarshalValue(%q) = %#v, %v; want ErrInvalidValueJSON", test.data, value, err)
			}
		})
	}
}

func TestUnmarshalValueNoncanonicalIntError(t *testing.T) {
	t.Parallel()

	_, err := UnmarshalValue([]byte(`{"kind":"int","int":"030"}`))
	if !errors.Is(err, ErrInvalidValueJSON) || !strings.Contains(err.Error(), "non-canonical int") {
		t.Fatalf("error = %v, want ErrInvalidValueJSON containing non-canonical int", err)
	}
}

func FuzzUnmarshalValue(f *testing.F) {
	f.Add([]byte(`{"kind":"null"}`))
	f.Add([]byte(`{"kind":"int","int":"-9223372036854775808"}`))
	f.Add([]byte(`{"kind":"float","float":"-0"}`))
	f.Add([]byte(`{"kind":"list","list":[{"kind":"string","string":"x"}]}`))
	f.Add([]byte(`{"kind":"map","map":[{"key":"","value":{"kind":"bool","bool":true}}]}`))
	f.Add([]byte(`{"kind":"int","int":"01"}`))
	f.Add(invalidUTF8ValueJSON())
	f.Add([]byte(`{"kind":"null"} trailing`))

	f.Fuzz(func(t *testing.T, data []byte) {
		value, err := UnmarshalValue(data)
		if err != nil {
			if !errors.Is(err, ErrInvalidValueJSON) {
				t.Fatalf("error = %v, want ErrInvalidValueJSON", err)
			}
			return
		}

		first, err := MarshalValue(value)
		if err != nil {
			t.Fatalf("MarshalValue(decoded) error = %v", err)
		}
		roundTrip, err := UnmarshalValue(first)
		if err != nil {
			t.Fatalf("UnmarshalValue(canonical) error = %v", err)
		}
		assertSameRulesValue(t, roundTrip, value)
		second, err := MarshalValue(roundTrip)
		if err != nil {
			t.Fatalf("MarshalValue(roundTrip) error = %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("canonical encodings differ: %s != %s", first, second)
		}
	})
}

func invalidUTF8ValueJSON() []byte {
	data := append([]byte(nil), `{"kind":"string","string":"`...)
	data = append(data, 0xff)
	return append(data, '"', '}')
}

func mustRulesValue(t testing.TB, raw any) rules.Value {
	t.Helper()
	value, err := rules.NewValue(raw)
	if err != nil {
		t.Fatalf("rules.NewValue(%T) error = %v", raw, err)
	}
	return value
}

func assertSameRulesValue(t testing.TB, got, want rules.Value) {
	t.Helper()
	if got.Kind() != want.Kind() {
		t.Fatalf("value kind = %s, want %s", got.Kind(), want.Kind())
	}
	switch want.Kind() {
	case rules.ValueNull:
		return
	case rules.ValueBool:
		gotValue, gotOK := got.AsBool()
		wantValue, wantOK := want.AsBool()
		if !gotOK || !wantOK || gotValue != wantValue {
			t.Fatalf("bool value = %v, %t; want %v, %t", gotValue, gotOK, wantValue, wantOK)
		}
	case rules.ValueInt:
		gotValue, gotOK := got.AsInt64()
		wantValue, wantOK := want.AsInt64()
		if !gotOK || !wantOK || gotValue != wantValue {
			t.Fatalf("int value = %v, %t; want %v, %t", gotValue, gotOK, wantValue, wantOK)
		}
	case rules.ValueFloat:
		gotValue, gotOK := got.AsFloat64()
		wantValue, wantOK := want.AsFloat64()
		if !gotOK || !wantOK || math.Float64bits(gotValue) != math.Float64bits(wantValue) {
			t.Fatalf("float bits = %x, %t; want %x, %t", math.Float64bits(gotValue), gotOK, math.Float64bits(wantValue), wantOK)
		}
	case rules.ValueString:
		gotValue, gotOK := got.AsString()
		wantValue, wantOK := want.AsString()
		if !gotOK || !wantOK || gotValue != wantValue {
			t.Fatalf("string value = %q, %t; want %q, %t", gotValue, gotOK, wantValue, wantOK)
		}
	case rules.ValueList:
		gotValues, gotOK := got.AsList()
		wantValues, wantOK := want.AsList()
		if !gotOK || !wantOK || len(gotValues) != len(wantValues) {
			t.Fatalf("list length = %d, %t; want %d, %t", len(gotValues), gotOK, len(wantValues), wantOK)
		}
		for i := range wantValues {
			assertSameRulesValue(t, gotValues[i], wantValues[i])
		}
	case rules.ValueMap:
		gotValues, gotOK := got.AsMap()
		wantValues, wantOK := want.AsMap()
		if !gotOK || !wantOK || len(gotValues) != len(wantValues) {
			t.Fatalf("map length = %d, %t; want %d, %t", len(gotValues), gotOK, len(wantValues), wantOK)
		}
		for key, wantValue := range wantValues {
			gotValue, ok := gotValues[key]
			if !ok {
				t.Fatalf("map is missing key %q", key)
			}
			assertSameRulesValue(t, gotValue, wantValue)
		}
	default:
		t.Fatalf("unsupported test value kind %s", want.Kind())
	}
}
