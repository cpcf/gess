package engine

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
)

func TestStructuralDuplicateSuppressionByFields(t *testing.T) {
	session := mustSession(t, mustCompile(t), "dup-session")

	first, err := session.insertFact("person", "", mustFields(t, map[string]any{
		"name":  "Ada",
		"age":   30,
		"roles": []any{"admin", "owner"},
	}))
	if err != nil {
		t.Fatalf("insert first fact: %v", err)
	}
	if !first.Inserted() {
		t.Fatalf("first fact insertion returned status %v", first.Status)
	}

	second, err := session.insertFact("person", "", mustFields(t, map[string]any{
		"roles": []any{"admin", "owner"},
		"age":   30,
		"name":  "Ada",
	}))
	if err != nil {
		t.Fatalf("insert second fact: %v", err)
	}
	if second.Inserted() {
		t.Fatalf("expected duplicate assertion result, got inserted")
	}
	if got, want := second.Fact.ID(), first.Fact.ID(); got != want {
		t.Fatalf("duplicate fact ID = %q, want %q", got, want)
	}

	dupKey := makeDuplicateKey("person", "", first.Fact.Fields())
	if byKey, ok := session.factIDForDuplicateKey(dupKey); !ok || byKey != first.Fact.ID() {
		t.Fatalf("fact by duplicate key = (%v, %t), want (%q, true)", byKey, ok, first.Fact.ID())
	}

	if _, ok := session.factByID(first.Fact.ID()); !ok {
		t.Fatalf("O(1) by-id lookup could not find inserted fact %q", first.Fact.ID())
	}

	existing := mustWorkingFactByID(t, session, first.Fact.ID())
	existing.version = 99
	existing.recency = 101

	repeated, err := session.insertFact("person", "", mustFields(t, map[string]any{
		"name":  "Ada",
		"age":   30,
		"roles": []any{"admin", "owner"},
	}))
	if err != nil {
		t.Fatalf("insert third fact: %v", err)
	}
	if repeated.Fact.ID() != first.Fact.ID() {
		t.Fatalf("metadata changes changed duplicate detection: got %q, want %q", repeated.Fact.ID(), first.Fact.ID())
	}
}

func TestValueImmutabilityAgainstCallerMapsAndPointers(t *testing.T) {
	session := mustSession(t, mustCompile(t), "immutability-session")

	nested := map[string]any{"count": 1, "tags": []string{"alpha", "beta"}}
	nestedPtr := &nested
	fields := mustFields(t, map[string]any{
		"payload": nestedPtr,
	})

	_, err := session.insertFact("event", "", fields)
	if err != nil {
		t.Fatalf("add fact: %v", err)
	}

	nested["count"] = 9
	nested["tags"] = []string{"mutated"}
	*nestedPtr = map[string]any{"count": 100}

	stored := mustSnapshot(t, context.Background(), session).Facts()[0]
	storedFields := stored.Fields()
	storedPayload := storedFields["payload"]
	storedPayloadMap, _ := storedPayload.AsMap()

	if got := valueInt64(storedPayloadMap["count"]); got != 1 {
		t.Fatalf("stored map count = %d, want %d", got, 1)
	}
	storedTags, _ := storedPayloadMap["tags"].AsList()
	if got := valueString(storedTags[0]); got != "alpha" {
		t.Fatalf("stored tags[0] = %q, want %q", got, "alpha")
	}
}

func TestValueScalarAccessors(t *testing.T) {
	tests := []struct {
		name       string
		value      Value
		asBool     bool
		wantBool   bool
		asInt      int64
		wantInt    bool
		asFloat    float64
		wantFloat  bool
		asString   string
		wantString bool
	}{
		{name: "bool", value: mustValue(t, true), asBool: true, wantBool: true},
		{name: "int", value: mustValue(t, int64(42)), asInt: 42, wantInt: true},
		{name: "float", value: mustValue(t, 9.8), asFloat: 9.8, wantFloat: true},
		{name: "string", value: mustValue(t, "critical"), asString: "critical", wantString: true},
		{name: "null", value: NullValue()},
		{name: "list", value: mustValue(t, []string{"critical"})},
		{name: "map", value: mustValue(t, map[string]any{"severity": "critical"})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotBool, ok := tc.value.AsBool()
			if ok != tc.wantBool || gotBool != tc.asBool {
				t.Fatalf("AsBool = (%v, %v), want (%v, %v)", gotBool, ok, tc.asBool, tc.wantBool)
			}

			gotInt, ok := tc.value.AsInt64()
			if ok != tc.wantInt || gotInt != tc.asInt {
				t.Fatalf("AsInt64 = (%v, %v), want (%v, %v)", gotInt, ok, tc.asInt, tc.wantInt)
			}

			gotFloat, ok := tc.value.AsFloat64()
			if ok != tc.wantFloat || gotFloat != tc.asFloat {
				t.Fatalf("AsFloat64 = (%v, %v), want (%v, %v)", gotFloat, ok, tc.asFloat, tc.wantFloat)
			}

			gotString, ok := tc.value.AsString()
			if ok != tc.wantString || gotString != tc.asString {
				t.Fatalf("AsString = (%q, %v), want (%q, %v)", gotString, ok, tc.asString, tc.wantString)
			}
		})
	}
}

func TestMissingNullAndZeroDistinction(t *testing.T) {
	session := mustSession(t, mustCompile(t), "value-presence-session")

	missingResult, err := session.insertFact("fact", "", mustFields(t, map[string]any{
		"name": "present",
	}))
	if err != nil {
		t.Fatalf("insert missing fact: %v", err)
	}

	nullResult, err := session.insertFact("fact", "", mustFields(t, map[string]any{
		"name":  "present",
		"count": nil,
	}))
	if err != nil {
		t.Fatalf("insert null fact: %v", err)
	}

	zeroResult, err := session.insertFact("fact", "", mustFields(t, map[string]any{
		"name":  "present",
		"count": 0,
	}))
	if err != nil {
		t.Fatalf("insert zero fact: %v", err)
	}

	if missingResult.Fact.ID() == nullResult.Fact.ID() {
		t.Fatalf("missing and explicit null should be distinct: both %q", missingResult.Fact.ID())
	}
	if missingResult.Fact.ID() == zeroResult.Fact.ID() {
		t.Fatalf("missing and explicit zero should be distinct: both %q", missingResult.Fact.ID())
	}
	if nullResult.Fact.ID() == zeroResult.Fact.ID() {
		t.Fatalf("null and zero should be distinct: both %q", nullResult.Fact.ID())
	}

	nullFact := nullResult.Fact.Fields()
	zeroFact := zeroResult.Fact.Fields()
	if got := nullFact["count"].Kind(); got != ValueNull {
		t.Fatalf("null fact kind = %q, want %q", got, ValueNull)
	}
	if got := zeroFact["count"].Kind(); got != ValueInt {
		t.Fatalf("zero fact kind = %q, want %q", got, ValueInt)
	}
}

func TestTemplateAndNameIndexesAndResetGenerations(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{Name: "person", Fields: []FieldSpec{{Name: "name", Kind: ValueString}}})
	session := mustSession(t, revision, "index-session")
	template, ok := revision.compiledTemplate("person")
	if !ok {
		t.Fatal("expected template person to exist")
	}

	person, err := session.insertFact("person", template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("insert template fact: %v", err)
	}
	_, err = session.insertFact("person", "", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("insert dynamic fact: %v", err)
	}

	if !containsFactID(session.factIDsByTemplate(template.Key()), person.Fact.ID()) {
		t.Fatalf("template index for %q missing fact %q", template.Key(), person.Fact.ID())
	}
	if len(session.factIDsByName("person")) != 2 {
		t.Fatalf("name index for person = %d, want 2", len(session.factIDsByName("person")))
	}

	if len(session.factIDsByTemplate(template.Key())) != 1 {
		t.Fatalf("template index for %q = %d, want 1", template.Key(), len(session.factIDsByTemplate(template.Key())))
	}
	if len(session.factIDsByName("person")) != 2 {
		t.Fatalf("name index for person = %d, want 2", len(session.factIDsByName("person")))
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	snapshotFacts := snapshot.Facts()
	if snapshotFacts[0].ID() != person.Fact.ID() {
		t.Fatalf("snapshot order changed: first=%q want %q", snapshotFacts[0].ID(), person.Fact.ID())
	}

	staleID := person.Fact.ID()
	session.resetWorkingMemory()
	if _, ok := session.factByID(staleID); ok {
		t.Fatalf("stale fact ID %q unexpectedly still resolved after reset", staleID)
	}

	newPerson, err := session.insertFact("person", template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("insert after reset: %v", err)
	}
	if newPerson.Fact.ID().Generation() == staleID.Generation() {
		t.Fatalf("reset did not advance generation for new fact IDs: %q", newPerson.Fact.ID())
	}

	if len(session.factIDsByTemplate(template.Key())) != 1 {
		t.Fatalf("template index after reset = %d, want 1", len(session.factIDsByTemplate(template.Key())))
	}
}

func TestValueAndSnapshotImmutabilityNested(t *testing.T) {
	session := mustSession(t, mustCompile(t), "nested-snapshot-session")
	result, err := session.insertFact("order", "", mustFields(t, map[string]any{
		"outer": map[string]any{"inner": []any{1, 2}},
	}))
	if err != nil {
		t.Fatalf("insert fact: %v", err)
	}

	facts := mustSnapshot(t, context.Background(), session).Facts()
	if len(facts) != 1 {
		t.Fatalf("snapshot length = %d, want 1", len(facts))
	}

	returned := facts[0].Fields()
	outerValue := returned["outer"]
	returnedOuter, _ := outerValue.AsMap()
	innerValue := returnedOuter["inner"]
	returnedOuter["inner"] = mustValue(t, []any{"mutated"})
	rereadOuter, _ := outerValue.AsMap()
	if !rereadOuter["inner"].Equal(mustValue(t, []any{1, 2})) {
		t.Fatalf("AsMap returned aliased storage")
	}
	returnInner, _ := innerValue.AsList()
	returnInner[0] = newStringValue("mutated")
	rereadInner, _ := innerValue.AsList()
	if !rereadInner[0].Equal(mustValue(t, 1)) {
		t.Fatalf("AsList returned aliased storage")
	}

	facts = mustSnapshot(t, context.Background(), session).Facts()
	actualOuter, _ := facts[0].Fields()["outer"].AsMap()
	actual, _ := actualOuter["inner"].AsList()
	resultOuter, _ := result.Fact.Fields()["outer"].AsMap()
	resultInner, _ := resultOuter["inner"].AsList()
	if !actual[0].Equal(resultInner[0]) {
		t.Fatalf("snapshot mutation leaked into session snapshot")
	}
}

func TestNumericValuesAndUnsupportedTypes(t *testing.T) {
	intValue, err := NewValue(1)
	if err != nil {
		t.Fatalf("NewValue int: %v", err)
	}
	floatValue, err := NewValue(1.0)
	if err != nil {
		t.Fatalf("NewValue float: %v", err)
	}
	if !intValue.Equal(floatValue) {
		t.Fatalf("numeric equality should match equal int and float values")
	}
	if intValue.CanonicalKey() != floatValue.CanonicalKey() {
		t.Fatalf("numeric duplicate keys differ: %q vs %q", intValue.CanonicalKey(), floatValue.CanonicalKey())
	}

	type customInt int
	aliasValue, err := NewValue(customInt(1))
	if err != nil {
		t.Fatalf("NewValue alias int: %v", err)
	}
	if !aliasValue.Equal(intValue) {
		t.Fatalf("alias numeric value should equal canonical int")
	}

	_, err = NewValue(math.NaN())
	if !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("NewValue NaN error = %v, want ErrUnsupportedValue", err)
	}

	_, err = NewValue(struct{ Name string }{Name: "unsupported"})
	if !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("NewValue struct error = %v, want ErrUnsupportedValue", err)
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
			value, err := NewValue(tc.raw)
			if err != nil {
				t.Fatalf("NewValue: %v", err)
			}
			if _, ok := value.AsList(); ok {
				t.Fatalf("scalar value exposed list storage")
			}
			if _, ok := value.AsMap(); ok {
				t.Fatalf("scalar value exposed map storage")
			}
		})
	}

	list, err := NewValue([]any{"Ada", 1})
	if err != nil {
		t.Fatalf("NewValue list: %v", err)
	}
	values, _ := list.AsList()
	for i, value := range values {
		if _, ok := value.AsList(); ok {
			t.Fatalf("list scalar value %d exposed list storage", i)
		}
		if _, ok := value.AsMap(); ok {
			t.Fatalf("list scalar value %d exposed map storage", i)
		}
	}
}

func TestNewFieldsFromPairsBuildsFields(t *testing.T) {
	fields, err := NewFieldsFromPairs("name", "Ada", "age", 37, "active", true)
	if err != nil {
		t.Fatalf("NewFieldsFromPairs: %v", err)
	}
	if got, want := fields["name"], mustValue(t, "Ada"); !got.Equal(want) {
		t.Fatalf("name = %#v, want %#v", got, want)
	}
	if got, want := fields["age"], mustValue(t, 37); !got.Equal(want) {
		t.Fatalf("age = %#v, want %#v", got, want)
	}
	if got, want := fields["active"], mustValue(t, true); !got.Equal(want) {
		t.Fatalf("active = %#v, want %#v", got, want)
	}
}

func TestNewFieldsFromPairsRejectsMalformedInputs(t *testing.T) {
	cases := []struct {
		name  string
		pairs []any
	}{
		{name: "odd argument count", pairs: []any{"name"}},
		{name: "non string key", pairs: []any{1, "Ada"}},
		{name: "empty key", pairs: []any{"", "Ada"}},
		{name: "unsupported value", pairs: []any{"value", struct{ Name string }{"Ada"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewFieldsFromPairs(tc.pairs...); err == nil {
				t.Fatal("NewFieldsFromPairs succeeded, want error")
			}
		})
	}
}

func TestMustFieldsPanicsOnMalformedInputs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustFields did not panic")
		}
	}()
	_ = MustFields("name")
}

func mustFields(t testing.TB, raw map[string]any) Fields {
	t.Helper()
	fields, err := NewFields(raw)
	if err != nil {
		t.Fatalf("NewFields: %v", err)
	}
	return fields
}

func containsFactID(ids []FactID, target FactID) bool {
	return slices.Contains(ids, target)
}
