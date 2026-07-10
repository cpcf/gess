package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplainJSONSchemaGoldens(t *testing.T) {
	// Changing one of these documents requires an explicit decision about
	// whether ExplainSchemaVersion must change. The version-derived filenames
	// also make a version bump fail until a complete new golden set is added.
	tests := []struct {
		kind   string
		report any
	}{
		{kind: "derivation", report: goldenDerivationReport()},
		{kind: "whynot", report: goldenWhyNotReport()},
		{kind: "whatif", report: goldenWhatIfReport()},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			got, err := json.Marshal(test.report)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "explain", fmt.Sprintf("%s-v%d.json", test.kind, ExplainSchemaVersion))
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", path, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s schema golden mismatch\ngot:\n%s\nwant:\n%s", test.kind, got, want)
			}
		})
	}
}

func goldenDerivationReport() Derivation {
	producer := &Firing{
		RuleID:          RuleID("rule:advance"),
		RuleName:        "advance",
		RuleRevisionID:  RuleRevisionID("rule-revision:advance-v1"),
		ActivationID:    ActivationID("activation:advance-1"),
		Generation:      1,
		Action:          `(modify ?record (set (status "active")))`,
		Bindings:        []BindingValue{{Name: "record", Value: newStringValue("R-1"), FromFact: newFactID(1, 2)}, {Name: "attempt", Value: newIntValue(2)}},
		SupportingFacts: []FactID{newFactID(1, 1)},
	}
	return Derivation{
		Fact: FactSnapshot{
			id:          newFactID(1, 2),
			name:        "record",
			templateKey: TemplateKey("record"),
			version:     3,
			generation:  1,
			fields:      Fields{"attempt": newIntValue(2), "status": newStringValue("active")},
			support:     FactSupportProvenance{State: FactSupportStatedAndLogical},
		},
		Support:    FactSupportStatedAndLogical,
		ProducedBy: producer,
		DependsOn:  []Derivation{{Fact: renderTestFact(newFactID(1, 1), "trigger", FactSupportStated), Support: FactSupportStated}},
		History: []MutationRecord{
			{Kind: MutationAssert, Firing: producer, Sequence: 7},
			{Kind: MutationModify, Firing: producer, ChangedFields: []FieldChange{{Field: "status", Old: newStringValue("pending"), New: newStringValue("active")}}, Sequence: 8},
		},
	}
}

func goldenWhyNotReport() WhyNotReport {
	return WhyNotReport{
		RuleID:         RuleID("rule:escalate"),
		RuleName:       "escalate",
		RuleRevisionID: RuleRevisionID("rule-revision:escalate-v1"),
		Outcome:        WhyNotBlocked,
		Truncated:      true,
		Activations: []AgendaActivation{{
			activationID:   ActivationID("activation:escalate-1"),
			ruleID:         RuleID("rule:escalate"),
			ruleRevisionID: RuleRevisionID("rule-revision:escalate-v1"),
			ruleName:       "escalate",
			module:         MainModule,
			salience:       50,
			factIDs:        []FactID{newFactID(1, 3)},
		}},
		Branches: []WhyNotBranch{{
			BranchID:     2,
			FirstFailing: 1,
			Conditions: []WhyNotCondition{
				{Order: 0, PlannedOrder: 1, Binding: "account", Source: SourceSpan{Name: "rules.gess", StartLine: 4, StartColumn: 3}, AlphaMatches: 2, Satisfied: true},
				{Order: 1, PlannedOrder: 0, Binding: "alert", Negated: true, Source: SourceSpan{Name: "rules.gess", StartLine: 5, StartColumn: 3}, Reason: WhyNotReasonNegationBlocked, RejectingSpan: SourceSpan{Name: "rules.gess", StartLine: 5, StartColumn: 18}, Blockers: []FactID{newFactID(1, 12)}, BlockerCount: 1},
			},
			PartialMatches: []WhyNotPartialMatch{{
				Facts:          []FactID{newFactID(1, 3)},
				Bindings:       []BindingValue{{Name: "account", Value: newStringValue("A-1"), FromFact: newFactID(1, 3)}},
				Satisfied:      1,
				RejectedBySpan: SourceSpan{Name: "rules.gess", StartLine: 5, StartColumn: 18},
			}},
		}},
	}
}

func goldenWhatIfReport() WhatIfReport {
	before := FactSnapshot{id: newFactID(1, 4), name: "ticket", templateKey: TemplateKey("ticket"), version: 1, generation: 1, fields: Fields{"status": newStringValue("open")}, support: FactSupportProvenance{State: FactSupportStated}}
	after := before
	after.version = 2
	after.fields = Fields{"status": newStringValue("closed")}
	return WhatIfReport{
		Run: RunResult{RunID: 3, Status: RunCompleted, Fired: 2},
		Firings: []WhatIfFiring{{
			RuleID:         RuleID("rule:derive"),
			RuleName:       "derive",
			RuleRevisionID: RuleRevisionID("rule-revision:derive-v1"),
			ActivationID:   ActivationID("activation:derive-1"),
			FactIDs:        []FactID{newFactID(1, 1)},
			Sequence:       9,
		}},
		Diff: SnapshotDiff{
			Added:     []FactSnapshot{{id: newFactID(1, 5), name: "derived", templateKey: TemplateKey("derived"), version: 1, generation: 1, fields: Fields{"score": newFloatValue(3.5)}, support: FactSupportProvenance{State: FactSupportLogical}}},
			Retracted: []FactSnapshot{{id: newFactID(1, 6), name: "obsolete", templateKey: TemplateKey("obsolete"), version: 1, generation: 1, support: FactSupportProvenance{State: FactSupportStated}}},
			Modified:  []FactModification{{Before: before, After: after, ChangedFields: []FieldChange{{Field: "status", Old: newStringValue("open"), New: newStringValue("closed")}}, SupportBefore: FactSupportStated, SupportAfter: FactSupportStatedAndLogical}},
		},
		AgendaBefore: Agenda{activations: []AgendaActivation{{activationID: ActivationID("activation:before")}}},
		AgendaAfter:  Agenda{activations: []AgendaActivation{{activationID: ActivationID("activation:after-1")}, {activationID: ActivationID("activation:after-2")}}},
		Derivations:  []Derivation{goldenDerivationReport()},
	}
}

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
