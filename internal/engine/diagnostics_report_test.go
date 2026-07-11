package engine

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestSessionDiagnosticsFactDetailsAreOptIn(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustRuntimeGuardRuleset(t)
	session := mustSession(t, revision, "diagnostics-fact-option")
	if _, err := session.Assert(ctx, personKey, mustFields(t, map[string]any{
		"age": 42, "dept": "engineering", "id": "ada", "note": "ready", "status": "active",
	})); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	compactPayloadBefore := session.factStore.compactFacts.payloads
	report, err := session.Diagnostics(ctx)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if report.Facts != nil {
		t.Fatalf("default fact details = %#v, want nil", report.Facts)
	}
	if report.Session.FactCount != 1 {
		t.Fatalf("fact count = %d, want 1", report.Session.FactCount)
	}
	if len(compactPayloadBefore) != len(session.factStore.compactFacts.payloads) {
		t.Fatal("default diagnostics changed compact fact payload materialization")
	}

	report, err = session.Diagnostics(ctx, WithDiagnosticsFacts())
	if err != nil {
		t.Fatalf("Diagnostics(with facts): %v", err)
	}
	if len(report.Facts) != 1 {
		t.Fatalf("fact details = %d, want 1", len(report.Facts))
	}
	if got := report.Facts[0].Fields["id"]; got != "ada" {
		t.Fatalf("fact id field = %#v, want ada", got)
	}
}

func TestSessionDiagnosticsRepresentativeGolden(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustRuntimeGuardRuleset(t)
	session := mustSession(t, revision, "diagnostics-golden")
	if _, err := session.Assert(ctx, personKey, mustFields(t, map[string]any{
		"age": 42, "dept": "engineering", "id": "ada", "note": "ready", "status": "active",
	})); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	report, err := session.Diagnostics(ctx, WithDiagnosticsFacts())
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if report.Schema != DiagnosticsSchemaVersion {
		t.Fatalf("schema = %d, want %d", report.Schema, DiagnosticsSchemaVersion)
	}
	if report.Graph.Runtime != "rete" || report.Graph.AlphaNodes == 0 || report.Graph.RuleTerminals == 0 || report.Graph.QueryTerminals == 0 {
		t.Fatalf("graph diagnostics incomplete: %#v", report.Graph)
	}
	if report.Agenda.Pending != 1 {
		t.Fatalf("pending agenda = %d, want 1", report.Agenda.Pending)
	}
	if report.Queries.Definitions != 1 {
		t.Fatalf("query definitions = %d, want 1", report.Queries.Definitions)
	}

	// Retained byte and capacity estimates are explicitly debug-only. Keep the
	// stable owner identities in the golden without coupling it to struct sizes.
	for i := range report.Memory {
		report.Memory[i].Rows = 0
		report.Memory[i].Buckets = 0
		report.Memory[i].Indexes = 0
		report.Memory[i].Tombstones = 0
		report.Memory[i].Bytes = 0
		report.Memory[i].HighWater = 0
	}
	report.Aggregates = DiagnosticsAggregates{Nodes: report.Aggregates.Nodes}

	got, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile("testdata/diagnostics/session-v1.json")
	if err != nil {
		t.Fatalf("read golden: %v\nactual:\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Fatalf("diagnostics golden mismatch\nactual:\n%s\nwant:\n%s", got, want)
	}
}
