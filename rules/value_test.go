package rules

import (
	"math"
	"testing"
)

func TestValueEqualNumericEquivalence(t *testing.T) {
	if !IntValue(1).Equal(FloatValue(1)) {
		t.Fatalf("int and equivalent float should compare equal")
	}
	negativeZero := FloatValue(math.Copysign(0, -1))
	positiveZero := FloatValue(0)
	if !negativeZero.Equal(positiveZero) {
		t.Fatalf("-0.0 and 0.0 should compare equal")
	}
	if negativeZero.CanonicalKey() != positiveZero.CanonicalKey() {
		t.Fatalf("-0.0 canonical key = %q, want %q", negativeZero.CanonicalKey(), positiveZero.CanonicalKey())
	}
}

func TestValueCanonicalAndDuplicateKeysAreStable(t *testing.T) {
	left := mustRulesValue(t, map[string]any{
		"b": "two",
		"a": 1,
	})
	right := mustRulesValue(t, map[string]any{
		"a": 1,
		"b": "two",
	})
	wantValueKey := `map{a=number:1,b=string:"two"}`
	if got := left.CanonicalKey(); got != wantValueKey {
		t.Fatalf("CanonicalKey() = %q, want %q", got, wantValueKey)
	}
	if left.CanonicalKey() != right.CanonicalKey() {
		t.Fatalf("map canonical keys differ: %q vs %q", left.CanonicalKey(), right.CanonicalKey())
	}

	leftFields := MustFields("b", "two", "a", 1)
	rightFields := MustFields("a", 1, "b", "two")
	wantDuplicateKey := `a=number:1;b=string:"two";`
	if got := leftFields.DuplicateKey(); got != wantDuplicateKey {
		t.Fatalf("DuplicateKey() = %q, want %q", got, wantDuplicateKey)
	}
	if leftFields.DuplicateKey() != rightFields.DuplicateKey() {
		t.Fatalf("field duplicate keys differ: %q vs %q", leftFields.DuplicateKey(), rightFields.DuplicateKey())
	}
}

func TestCloneValueAndCloneFieldsDeepCopy(t *testing.T) {
	original := mustRulesValue(t, map[string]any{
		"items": []any{"a", "b"},
	})
	cloned := CloneValue(original)
	clonedMap, _ := cloned.AsMapShared()
	clonedItems, _ := clonedMap["items"].AsListShared()
	clonedItems[0] = StringValue("mutated")

	originalMap, _ := original.AsMapShared()
	originalItems, _ := originalMap["items"].AsListShared()
	if !originalItems[0].Equal(StringValue("a")) {
		t.Fatalf("CloneValue did not deep-copy nested list")
	}

	fields := MustFields("payload", map[string]any{"items": []any{1, 2}})
	clonedFields := CloneFields(fields)
	clonedPayload, _ := clonedFields["payload"].AsMapShared()
	clonedPayloadItems, _ := clonedPayload["items"].AsListShared()
	clonedPayloadItems[0] = IntValue(99)

	originalPayload, _ := fields["payload"].AsMapShared()
	originalPayloadItems, _ := originalPayload["items"].AsListShared()
	if !originalPayloadItems[0].Equal(IntValue(1)) {
		t.Fatalf("CloneFields did not deep-copy nested list")
	}
}

func TestValueDefensiveAndSharedAccessors(t *testing.T) {
	list := mustRulesValue(t, []any{"a", "b"})
	listCopy, _ := list.AsList()
	listCopy[0] = StringValue("copy")
	rereadList, _ := list.AsList()
	if !rereadList[0].Equal(StringValue("a")) {
		t.Fatalf("AsList returned aliased storage")
	}
	listShared, _ := list.AsListShared()
	listShared[0] = StringValue("shared")
	rereadSharedList, _ := list.AsListShared()
	if !rereadSharedList[0].Equal(StringValue("shared")) {
		t.Fatalf("AsListShared did not alias backing storage")
	}

	mapped := mustRulesValue(t, map[string]any{"name": "Ada"})
	mapCopy, _ := mapped.AsMap()
	mapCopy["name"] = StringValue("copy")
	rereadMap, _ := mapped.AsMap()
	if !rereadMap["name"].Equal(StringValue("Ada")) {
		t.Fatalf("AsMap returned aliased storage")
	}
	mapShared, _ := mapped.AsMapShared()
	mapShared["name"] = StringValue("shared")
	rereadSharedMap, _ := mapped.AsMapShared()
	if !rereadSharedMap["name"].Equal(StringValue("shared")) {
		t.Fatalf("AsMapShared did not alias backing storage")
	}
}

func TestScalarValuesUseTypedStorage(t *testing.T) {
	cases := []struct {
		name string
		raw  any
	}{
		{name: "bool", raw: true},
		{name: "int", raw: 1},
		{name: "float", raw: 1.5},
		{name: "string", raw: "Ada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mustRulesValue(t, tc.raw)
			if value.data != nil {
				t.Fatalf("scalar value data = %#v, want nil", value.data)
			}
		})
	}

	list := mustRulesValue(t, []any{true, 1, 1.5, "Ada"})
	values, _ := list.AsListShared()
	for i, value := range values {
		if value.data != nil {
			t.Fatalf("list scalar value %d data = %#v, want nil", i, value.data)
		}
	}
}

func mustRulesValue(t *testing.T, raw any) Value {
	t.Helper()
	value, err := NewValue(raw)
	if err != nil {
		t.Fatalf("NewValue(%#v): %v", raw, err)
	}
	return value
}
