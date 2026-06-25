package gess

import (
	"context"
	"testing"
)

var benchmarkJoinStrengthTerminalDeltas []reteTerminalTokenDelta

type joinStrengthBenchmarkTemplates struct {
	root  TemplateKey
	event TemplateKey
	grant TemplateKey
}

func BenchmarkGraphRuleJoinStrengthPlanning(b *testing.B) {
	const (
		roots         = 64
		eventsPerRoot = 128
	)
	revision, templates := mustCompileJoinStrengthBenchmark(b)
	facts := joinStrengthFactSnapshots(b, revision, templates, roots, eventsPerRoot)
	expected := roots * eventsPerRoot
	snapshot := collectJoinStrengthReplaySnapshot(b, revision, facts)

	b.ReportAllocs()
	defer func() {
		b.ReportMetric(float64(expected), "terminal-rows/replay")
		b.ReportMetric(float64(snapshot.Counters.Totals.BetaJoinedTokensProduced), "beta-joined-tokens/replay")
		b.ReportMetric(float64(snapshot.Counters.GraphBetaMemory.TokenRows), "graph-token-rows-retained")
		b.ReportMetric(float64(snapshot.Diagnostics.MaxBetaRows), "max-beta-node-token-rows")
	}()

	memory, err := newReteGraphBetaMemory(context.Background(), revision, revision.graph, nil)
	if err != nil {
		b.Fatalf("newReteGraphBetaMemory: %v", err)
	}
	if memory == nil {
		b.Fatal("newReteGraphBetaMemory returned nil")
	}

	b.ResetTimer()
	for b.Loop() {
		if err := memory.resetFacts(context.Background(), nil); err != nil {
			b.Fatalf("resetFacts: %v", err)
		}
		for _, fact := range facts {
			if _, err := memory.insertFact(context.Background(), fact, nil); err != nil {
				b.Fatalf("insertFact: %v", err)
			}
		}
		deltas, ok, err := memory.currentTerminalTokenDeltas(context.Background())
		if err != nil {
			b.Fatalf("currentTerminalTokenDeltas: %v", err)
		}
		if !ok {
			b.Fatal("currentTerminalTokenDeltas unavailable")
		}
		if len(deltas) != expected {
			b.Fatalf("terminal deltas = %d, want %d", len(deltas), expected)
		}
		benchmarkJoinStrengthTerminalDeltas = deltas
	}
}

func TestGraphRuleJoinStrengthPlanningReducesIntermediateTokens(t *testing.T) {
	const (
		roots         = 64
		eventsPerRoot = 128
	)
	revision, templates := mustCompileJoinStrengthBenchmark(t)
	facts := joinStrengthFactSnapshots(t, revision, templates, roots, eventsPerRoot)
	snapshot := collectJoinStrengthReplaySnapshot(t, revision, facts)
	expectedTerminalRows := roots * eventsPerRoot
	if got := snapshot.Counters.TerminalRowsRetained; got != expectedTerminalRows {
		t.Fatalf("terminal rows retained = %d, want %d", got, expectedTerminalRows)
	}
	if got, want := snapshot.Counters.Totals.BetaJoinedTokensProduced, expectedTerminalRows+roots; got != want {
		t.Fatalf("beta joined tokens produced = %d, want %d", got, want)
	}
	if got, want := snapshot.Counters.GraphBetaMemory.TokenRows, expectedTerminalRows*2+roots*3; got != want {
		t.Fatalf("graph token rows retained = %d, want %d", got, want)
	}
}

func TestGraphBetaArenaSkipsCopiedFactSpans(t *testing.T) {
	const (
		roots         = 4
		eventsPerRoot = 8
	)
	revision, templates := mustCompileJoinStrengthBenchmark(t)
	facts := joinStrengthFactSnapshots(t, revision, templates, roots, eventsPerRoot)
	memory, err := newReteGraphBetaMemory(context.Background(), revision, revision.graph, nil)
	if err != nil {
		t.Fatalf("newReteGraphBetaMemory: %v", err)
	}
	if memory == nil {
		t.Fatal("newReteGraphBetaMemory returned nil")
	}
	if err := memory.resetFacts(context.Background(), nil); err != nil {
		t.Fatalf("resetFacts: %v", err)
	}
	for _, fact := range facts {
		if _, err := memory.insertFact(context.Background(), fact, nil); err != nil {
			t.Fatalf("insertFact: %v", err)
		}
	}
	deltas, ok, err := memory.currentTerminalTokenDeltas(context.Background())
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok {
		t.Fatal("currentTerminalTokenDeltas unavailable")
	}
	if got, want := len(deltas), roots*eventsPerRoot; got != want {
		t.Fatalf("terminal deltas = %d, want %d", got, want)
	}
	if memory.arena == nil || memory.arena.rowCount() == 0 {
		t.Fatalf("graph beta arena row count = %d, want retained token rows", memory.arena.rowCount())
	}
	if got := len(memory.arena.factIDs); got != 0 {
		t.Fatalf("graph beta arena fact id spans = %d, want 0", got)
	}
	if got := len(memory.arena.factVersions); got != 0 {
		t.Fatalf("graph beta arena fact version spans = %d, want 0", got)
	}
}

func mustCompileJoinStrengthBenchmark(t testing.TB) (*Ruleset, joinStrengthBenchmarkTemplates) {
	t.Helper()
	workspace := NewWorkspace()
	root := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "join-strength-root",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "join-strength-event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "root", Kind: ValueInt, Required: true},
		},
	})
	grant := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "join-strength-grant",
		Fields: []FieldSpec{
			{Name: "root", Kind: ValueInt, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
		},
	})
	templates := joinStrengthBenchmarkTemplates{
		root:  root.Key(),
		event: event.Key(),
		grant: grant.Key(),
	}
	mustAddAction(t, workspace, ActionSpec{
		Name: "record-join-strength",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "join-strength-rule",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding:     "root",
				TemplateKey: templates.root,
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
			},
			Match{
				Binding:     "event",
				TemplateKey: templates.event,
				JoinConstraints: []JoinConstraintSpec{
					{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
				},
			},
			Match{
				Binding:     "grant",
				TemplateKey: templates.grant,
				JoinConstraints: []JoinConstraintSpec{
					{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
					{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "region"}},
				},
			},
		}},
		Actions: []RuleActionSpec{{Name: "record-join-strength"}},
	})
	return mustCompileWorkspace(t, workspace), templates
}

func joinStrengthFactSnapshots(t testing.TB, revision *Ruleset, templates joinStrengthBenchmarkTemplates, roots int, eventsPerRoot int) []FactSnapshot {
	t.Helper()
	session := mustSession(t, revision, "join-strength-facts")
	ctx := context.Background()
	for rootID := range roots {
		region := joinStrengthRegion(rootID)
		if _, err := session.AssertTemplate(ctx, templates.root, Fields{
			"id":     newIntValue(int64(rootID)),
			"region": newStringValue(region),
			"active": newBoolValue(true),
		}); err != nil {
			t.Fatalf("AssertTemplate(root): %v", err)
		}
		if _, err := session.AssertTemplate(ctx, templates.grant, Fields{
			"root":   newIntValue(int64(rootID)),
			"region": newStringValue(region),
		}); err != nil {
			t.Fatalf("AssertTemplate(grant): %v", err)
		}
		for eventIndex := range eventsPerRoot {
			if _, err := session.AssertTemplate(ctx, templates.event, Fields{
				"id":   newIntValue(int64(rootID*eventsPerRoot + eventIndex)),
				"root": newIntValue(int64(rootID)),
			}); err != nil {
				t.Fatalf("AssertTemplate(event): %v", err)
			}
		}
	}
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snapshot.Facts()
}

func collectJoinStrengthReplaySnapshot(t testing.TB, revision *Ruleset, facts []FactSnapshot) graphRuleNetworkReplaySnapshot {
	t.Helper()
	memory, err := newReteGraphBetaMemory(context.Background(), revision, revision.graph, nil)
	if err != nil {
		t.Fatalf("newReteGraphBetaMemory: %v", err)
	}
	if memory == nil {
		t.Fatal("newReteGraphBetaMemory returned nil")
	}
	ledger := newPropagationCounterLedger()
	if err := memory.resetFacts(context.Background(), nil); err != nil {
		t.Fatalf("resetFacts: %v", err)
	}
	for _, fact := range facts {
		span := ledger.beginAssert(fact.TemplateKey(), mutationOrigin{})
		if _, err := memory.insertFact(context.Background(), fact, &span); err != nil {
			t.Fatalf("insertFact: %v", err)
		}
		span.finish()
	}
	ledger.setTerminalRowsRetained(memory.terminalRowCount())
	ledger.setBranchRowsRetained(memory.terminalRowsRetainedByBranch())
	ledger.setGraphBetaMemoryStats(memory.memoryStats())
	return graphRuleNetworkReplaySnapshot{
		Counters:    ledger.snapshot(),
		Diagnostics: memory.diagnostics(),
	}
}

func joinStrengthRegion(rootID int) string {
	return "region-" + string(rune('A'+rootID%4))
}
