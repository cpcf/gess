package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDerivationJSONEnvelopeAndStructure(t *testing.T) {
	raw, err := json.Marshal(sampleDerivation())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Deterministic: repeated marshaling is byte-identical.
	raw2, _ := json.Marshal(sampleDerivation())
	if !bytes.Equal(raw, raw2) {
		t.Fatalf("Derivation JSON not deterministic")
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc["gessExplainSchema"] != float64(ExplainSchemaVersion) {
		t.Fatalf("schema = %v, want %d", doc["gessExplainSchema"], ExplainSchemaVersion)
	}
	if doc["kind"] != "derivation" {
		t.Fatalf("kind = %v, want derivation", doc["kind"])
	}
	fact := doc["fact"].(map[string]any)
	if fact["id"] != "fact:g1:2" {
		t.Fatalf("fact id = %v, want fact:g1:2", fact["id"])
	}
	if fact["support"] != string(FactSupportStatedAndLogical) {
		t.Fatalf("fact support = %v", fact["support"])
	}
	producedBy := doc["producedBy"].(map[string]any)
	if producedBy["action"] != `(modify ?r (set (status "active")))` {
		t.Fatalf("action = %v", producedBy["action"])
	}
	if producedBy["bindingsPartial"] != true {
		t.Fatalf("bindingsPartial = %v, want true", producedBy["bindingsPartial"])
	}
	if _, ok := doc["dependsOn"]; !ok {
		t.Fatalf("dependsOn missing")
	}
}

func TestDerivationJSONOmitsLineageWhenAbsent(t *testing.T) {
	// A Tier-1 stated fact: no ProducedBy, no History.
	d := Derivation{
		Fact:    renderTestFact(newFactID(1, 1), "finding", FactSupportStated),
		Support: FactSupportStated,
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"producedBy", "history", "dependsOn"} {
		if _, ok := doc[key]; ok {
			t.Fatalf("key %q should be omitted for a Tier-1 stated fact", key)
		}
	}
	if doc["truncated"] != false {
		t.Fatalf("truncated should always be present")
	}
}

func TestValueJSONEncoding(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want any
	}{
		{"bool", true, true},
		{"int", int64(42), float64(42)},
		{"float", 3.5, 3.5},
		{"string", "hi", "hi"},
		{"list", []any{int64(1), "two"}, []any{float64(1), "two"}},
		{"nestedListOfMap", []any{map[string]any{"k": int64(7)}}, []any{map[string]any{"k": float64(7)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := mustValue(t, tc.raw)
			encoded := valueToJSON(value)
			roundTripped := jsonRoundTrip(t, encoded)
			if !jsonEqual(roundTripped, tc.want) {
				t.Fatalf("value %v encoded to %v, want %v", tc.raw, roundTripped, tc.want)
			}
		})
	}
}

func TestValueJSONInt64Boundary(t *testing.T) {
	// int64 encodes as a JSON number; the exact digits are preserved in the
	// document even beyond 2^53 (consumers parsing as float64 may lose them).
	big := int64(9007199254740993) // 2^53 + 1
	raw, err := json.Marshal(map[string]any{"n": valueToJSON(mustValue(t, big))})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("int64 not preserved verbatim: %s", raw)
	}
}

func TestWhyNotReportJSON(t *testing.T) {
	report := WhyNotReport{
		RuleName: "escalate",
		Outcome:  WhyNotBlocked,
		Branches: []WhyNotBranch{{
			BranchID:     0,
			FirstFailing: 1,
			Conditions: []WhyNotCondition{
				{Order: 0, Binding: "a", Satisfied: true, AlphaMatches: 1},
				{Order: 1, Binding: "alert", Negated: true, Reason: WhyNotReasonNegationBlocked, Blockers: []FactID{newFactID(1, 12)}, BlockerCount: 1},
			},
		}},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc["gessExplainSchema"] != float64(ExplainSchemaVersion) || doc["kind"] != "whynot" {
		t.Fatalf("envelope = %v/%v", doc["gessExplainSchema"], doc["kind"])
	}
	if doc["outcome"] != string(WhyNotBlocked) {
		t.Fatalf("outcome = %v", doc["outcome"])
	}
	branch := doc["branches"].([]any)[0].(map[string]any)
	failing := branch["conditions"].([]any)[1].(map[string]any)
	if failing["reason"] != string(WhyNotReasonNegationBlocked) {
		t.Fatalf("failing reason = %v", failing["reason"])
	}
	if failing["blockers"].([]any)[0] != "fact:g1:12" {
		t.Fatalf("blocker id = %v", failing["blockers"])
	}
}

func TestWhatIfReportJSON(t *testing.T) {
	report := WhatIfReport{
		Run: RunResult{Status: RunCompleted, Fired: 2},
		Firings: []WhatIfFiring{
			{RuleName: "derive", FactIDs: []FactID{newFactID(1, 1)}, Sequence: 3},
		},
		Diff: SnapshotDiff{
			Added: []FactSnapshot{renderTestFact(newFactID(1, 2), "derived", FactSupportLogical)},
		},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc["kind"] != "whatif" {
		t.Fatalf("kind = %v", doc["kind"])
	}
	run := doc["run"].(map[string]any)
	if run["status"] != string(RunCompleted) || run["fired"] != float64(2) {
		t.Fatalf("run = %v", run)
	}
	added := doc["diff"].(map[string]any)["added"].([]any)
	if len(added) != 1 || added[0].(map[string]any)["name"] != "derived" {
		t.Fatalf("diff added = %v", added)
	}
}

func jsonRoundTrip(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return out
}

func jsonEqual(a, b any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return bytes.Equal(ra, rb)
}
