package engine

import (
	"context"
	"slices"
	"testing"
)

func TestSessionAlphaLiteralEqualityIndexInvalidatesAcrossModify(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-literal-equality-index-session")

	cold, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1}))
	if err != nil {
		t.Fatalf("Assert(cold): %v", err)
	}
	hot, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}

	session.attachPropagationCounters()
	assertAlphaLiteralEqualityCandidates(t, revision, session, ruleName, hot.Fact.ID())
	if _, err := session.Modify(ctx, hot.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "cold"})}); err != nil {
		t.Fatalf("Modify(hot -> cold): %v", err)
	}
	assertAlphaLiteralEqualityCandidates(t, revision, session, ruleName)
	if _, err := session.Modify(ctx, cold.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "hot"})}); err != nil {
		t.Fatalf("Modify(cold -> hot): %v", err)
	}
	assertAlphaLiteralEqualityCandidates(t, revision, session, ruleName, cold.Fact.ID())

	counters := session.propagationCounterSnapshot().Totals
	if got, want := counters.AlphaIndexProbes, 3; got != want {
		t.Fatalf("alpha index probes = %d, want %d", got, want)
	}
	if got, want := counters.AlphaIndexHits, 2; got != want {
		t.Fatalf("alpha index hits = %d, want %d", got, want)
	}
	if got, want := counters.AlphaIndexMisses, 1; got != want {
		t.Fatalf("alpha index misses = %d, want %d", got, want)
	}
	if got := counters.AlphaIndexFallbackScans; got != 0 {
		t.Fatalf("alpha index fallback scans = %d, want 0", got)
	}
}

func TestGraphBetaAlphaFactRoutesTrackModifyAndRetract(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-fact-route-index-session")

	cold, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1}))
	if err != nil {
		t.Fatalf("Assert(cold): %v", err)
	}
	hot, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}
	memory := session.rete.graphBeta
	if memory == nil {
		t.Fatal("graph beta memory is nil")
	}
	assertAlphaFactRouteIndex(t, memory, hot.Fact.ID(), 1)
	assertAlphaFactRouteIndex(t, memory, cold.Fact.ID(), 0)
	if got, want := len(memory.alpha.factOwnership), 1; got != want {
		t.Fatalf("alpha fact route index keys = %d, want %d", got, want)
	}

	if _, err := session.Modify(ctx, hot.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "cold"})}); err != nil {
		t.Fatalf("Modify(hot -> cold): %v", err)
	}
	assertAlphaFactRouteIndex(t, memory, hot.Fact.ID(), 0)
	if got := len(memory.alpha.factOwnership); got != 0 {
		t.Fatalf("alpha fact route index keys after modify out = %d, want 0", got)
	}

	if _, err := session.Modify(ctx, cold.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "hot"})}); err != nil {
		t.Fatalf("Modify(cold -> hot): %v", err)
	}
	assertAlphaFactRouteIndex(t, memory, cold.Fact.ID(), 1)
	if got, want := len(memory.alpha.factOwnership), 1; got != want {
		t.Fatalf("alpha fact route index keys after modify in = %d, want %d", got, want)
	}

	if _, err := session.Retract(ctx, cold.Fact.ID()); err != nil {
		t.Fatalf("Retract(cold): %v", err)
	}
	assertAlphaFactRouteIndex(t, memory, cold.Fact.ID(), 0)
	if got := len(memory.alpha.factOwnership); got != 0 {
		t.Fatalf("alpha fact route index keys after retract = %d, want 0", got)
	}
}

func TestGraphBetaAlphaFactRoutesClearTouchedKeysOnReset(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-fact-route-reset-session")

	hot, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}
	memory := session.rete.graphBeta
	if memory == nil {
		t.Fatal("graph beta memory is nil")
	}
	assertAlphaFactRouteIndex(t, memory, hot.Fact.ID(), 1)
	if got := len(memory.alpha.factOwnershipIDs); got == 0 {
		t.Fatal("alpha fact route touched keys were not recorded")
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := len(memory.alpha.factOwnership); got != 0 {
		t.Fatalf("alpha fact route index keys after reset = %d, want 0", got)
	}
	if got := len(memory.alpha.factOwnershipIDs); got != 0 {
		t.Fatalf("alpha fact route touched keys after reset = %d, want 0", got)
	}
}

func TestSessionRuntimeDiagnosticsReportsAlphaMemoryOwner(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-memory-diagnostics-session")

	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1})); err != nil {
		t.Fatalf("Assert(cold): %v", err)
	}
	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2})); err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	var alpha RuntimeMemoryOwnerDiagnostics
	for _, owner := range diagnostics.MemoryOwners {
		if owner.Owner == runtimeMemoryOwnerAlpha {
			alpha = owner
			break
		}
	}
	if alpha.Owner == "" {
		t.Fatalf("runtime diagnostics missing alpha owner: %#v", diagnostics.MemoryOwners)
	}
	if alpha.Rows == 0 {
		t.Fatalf("alpha rows = 0, want retained alpha rows: %#v", alpha)
	}
	if alpha.Indexes == 0 {
		t.Fatalf("alpha indexes = 0, want retained alpha indexes: %#v", alpha)
	}
	if alpha.Bytes == 0 {
		t.Fatalf("alpha bytes = 0, want retained byte estimate: %#v", alpha)
	}
	if alpha.HighWater == 0 {
		t.Fatalf("alpha high water = 0, want capacity estimate: %#v", alpha)
	}
}

func TestGraphBetaAlphaMemoryDiagnosticsIncludeRouteIndexes(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-memory-index-diagnostics-session")
	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1})); err != nil {
		t.Fatalf("Assert(cold): %v", err)
	}
	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2})); err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}
	memory := session.rete.graphBeta
	if memory == nil {
		t.Fatal("graph beta memory is nil")
	}
	rule := revision.rules[ruleName]
	fieldSlot, value, ok := rule.conditionPlans[0].literalEqualityFieldIndex()
	if !ok {
		t.Fatal("literal equality field index was not planned")
	}
	target := conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey}
	if _, ok := session.factsForTarget(target); !ok {
		t.Fatal("session factsForTarget returned !ok")
	}
	if _, ok := session.factsForTargetFieldEqual(target, fieldSlot, value); !ok {
		t.Fatal("session factsForTargetFieldEqual returned !ok")
	}
	if len(memory.alpha.factRouteStorage) == 0 {
		t.Fatal("alpha fact route storage was not built")
	}

	alpha := memory.alphaMemoryOwnerDiagnostics()
	if alpha.Rows < uint64(len(memory.alpha.factRouteStorage)) {
		t.Fatalf("alpha rows = %d, want at least route storage rows %d: %#v", alpha.Rows, len(memory.alpha.factRouteStorage), alpha)
	}
}

func TestGraphBetaAlphaLiteralEqualityIndexRecordsRouteCounters(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-literal-equality-route-counters-session")
	session.attachPropagationCounters()

	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1})); err != nil {
		t.Fatalf("Assert(cold): %v", err)
	}
	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2})); err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}

	counters := session.propagationCounterSnapshot().Totals
	if got, want := counters.AlphaIndexProbes, 2; got != want {
		t.Fatalf("alpha index probes = %d, want %d", got, want)
	}
	if got, want := counters.AlphaIndexHits, 1; got != want {
		t.Fatalf("alpha index hits = %d, want %d", got, want)
	}
	if got, want := counters.AlphaIndexMisses, 1; got != want {
		t.Fatalf("alpha index misses = %d, want %d", got, want)
	}
	if got := counters.AlphaIndexFallbackScans; got != 0 {
		t.Fatalf("alpha index fallback scans = %d, want 0", got)
	}
}

func TestGraphBetaAlphaLiteralEqualityIndexRoutesSingleQueryTerminalNode(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityQueryRuleset(t)
	session := mustAlphaLiteralEqualitySession(t, ctx, revision, templateKey, 8)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}
	if runtime.graphBeta == nil {
		t.Fatal("graph beta memory is nil")
	}

	query, ok := revision.query("hot-alpha-literal-events")
	if !ok {
		t.Fatal("query missing")
	}
	args, err := query.compileArgs(QueryArgs{"min_score": 0})
	if err != nil {
		t.Fatalf("compileArgs: %v", err)
	}
	rows, handled, err := runtime.queryRows(ctx, query, &args, newReteGraphQueryTriggerEvent(snapshotQueryTriggerFact(snapshot.Generation(), query, &args)), snapshot)
	if err != nil {
		t.Fatalf("queryRows: %v", err)
	}
	if !handled {
		t.Fatal("queryRows handled = false")
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("query rows = %d, want %d", got, want)
	}
}

func TestGraphBetaAlphaLiteralEqualityIndexRebuildsFromSnapshot(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustAlphaLiteralEqualitySession(t, ctx, revision, templateKey, 8)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}
	if runtime.graphBeta == nil {
		t.Fatal("graph beta memory is nil")
	}
	for _, fact := range snapshot.Facts() {
		category, _ := fact.Field("category")
		routeIDs := runtime.graphBeta.snapshotAlphaRouteIDsForFactInsert(fact, nil)
		switch {
		case category.Kind() == ValueString && valueString(category) == "hot":
			if got, want := len(routeIDs), 1; got != want {
				t.Fatalf("hot fact route IDs = %d, want %d", got, want)
			}
		case category.Kind() == ValueString && valueString(category) == "cold":
			if got := len(routeIDs); got != 0 {
				t.Fatalf("cold fact route IDs = %d, want 0", got)
			}
		}
	}

	rule := revision.rules[ruleName]
	fieldSlot, value, ok := rule.conditionPlans[0].literalEqualityFieldIndex()
	if !ok {
		t.Fatal("literal equality field index was not planned")
	}
	indexedFacts, ok := snapshot.factsForTarget(conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey})
	if !ok {
		t.Fatal("snapshot factsForTarget returned !ok")
	}
	hot := 0
	for _, fact := range indexedFacts {
		if factSnapshotMatchesFieldEqualIndex(fact, fieldSlot, value) {
			hot++
		}
	}
	if got, want := hot, 1; got != want {
		t.Fatalf("hot indexed facts via snapshot source = %d, want %d", got, want)
	}
}

func TestGraphBetaAlphaLiteralEqualityIndexRebuildsAfterFactTableSwap(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-field-index-fact-table-swap-session")
	cold, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1}))
	if err != nil {
		t.Fatalf("Assert(cold): %v", err)
	}
	hot, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("Assert(hot): %v", err)
	}
	memory := session.rete.graphBeta
	if memory == nil {
		t.Fatal("graph beta memory is nil")
	}
	rule := revision.rules[ruleName]
	fieldSlot, value, ok := rule.conditionPlans[0].literalEqualityFieldIndex()
	if !ok {
		t.Fatal("literal equality field index was not planned")
	}
	target := conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey}
	facts, ok := session.factsForTargetFieldEqual(target, fieldSlot, value)
	if !ok {
		t.Fatal("session factsForTargetFieldEqual returned !ok before retract")
	}
	if got, want := len(facts), 1; got != want {
		t.Fatalf("facts before retract = %d, want %d", got, want)
	}
	if facts[0].ID() != hot.Fact.ID() {
		t.Fatalf("indexed fact before retract = %s, want %s", facts[0].ID(), hot.Fact.ID())
	}

	if _, err := session.Retract(ctx, cold.Fact.ID()); err != nil {
		t.Fatalf("Retract(cold): %v", err)
	}
	facts, ok = session.factsForTargetFieldEqual(target, fieldSlot, value)
	if !ok {
		t.Fatal("session factsForTargetFieldEqual returned !ok after retract")
	}
	if got, want := len(facts), 1; got != want {
		t.Fatalf("facts after retract = %d, want %d", got, want)
	}
	if facts[0].ID() != hot.Fact.ID() {
		t.Fatalf("indexed fact after retract = %s, want %s", facts[0].ID(), hot.Fact.ID())
	}
}

func TestGraphBetaAlphaRouteIDsAreSortedAndStableAcrossIndexes(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "category", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
			{Name: "priority", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "event-name",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact(event.Name())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "event-category", field: "category", value: "hot"},
		{name: "event-score", field: "score", value: 2},
		{name: "event-priority", field: "priority", value: "high"},
		{name: "event-region", field: "region", value: "emea"},
	} {
		mustAddRule(t, workspace, RuleSpec{
			Name: tc.name,
			Conditions: []RuleConditionSpec{{
				Binding: "event",
				FieldConstraints: []FieldConstraintSpec{{
					Field:    tc.field,
					Operator: FieldConstraintEqual,
					Value:    tc.value,
				}},
				Target: TemplateKeyFact(event.Key()),
			}},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
	}
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "alpha-route-order-session")
	inserted, err := session.Assert(ctx, event.Key(), mustFields(t, map[string]any{
		"category": "hot",
		"score":    2,
		"priority": "high",
		"region":   "emea",
	}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	fact, ok := snapshot.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatalf("snapshot fact %s not found", inserted.Fact.ID())
	}
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}
	if runtime.graphBeta == nil {
		t.Fatal("graph beta memory is nil")
	}

	want := append([]reteGraphAlphaNodeID(nil), runtime.graphBeta.snapshotAlphaRouteIDsForFactInsert(fact, nil)...)
	if got, wantLen := len(want), 5; got != wantLen {
		t.Fatalf("alpha route IDs = %d, want %d: %#v", got, wantLen, want)
	}
	if !slices.IsSorted(want) {
		t.Fatalf("alpha route IDs are not sorted: %#v", want)
	}
	for i := range 5 {
		got := append([]reteGraphAlphaNodeID(nil), runtime.graphBeta.snapshotAlphaRouteIDsForFactInsert(fact, nil)...)
		if !slices.Equal(got, want) {
			t.Fatalf("alpha route IDs call %d = %#v, want %#v", i+1, got, want)
		}
	}
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta again: %v", err)
	}
	got := append([]reteGraphAlphaNodeID(nil), runtime.graphBeta.snapshotAlphaRouteIDsForFact(fact)...)
	if !slices.Equal(got, want) {
		t.Fatalf("alpha route IDs after reset = %#v, want %#v", got, want)
	}
}

func assertAlphaLiteralEqualityCandidates(t testing.TB, revision *Ruleset, session *Session, ruleName string, wantIDs ...FactID) {
	t.Helper()

	rule := revision.rules[ruleName]
	candidates, err := rule.matchCandidates(context.Background(), session)
	if err != nil {
		t.Fatalf("matchCandidates: %v", err)
	}
	if len(candidates) != len(wantIDs) {
		t.Fatalf("candidate count = %d, want %d", len(candidates), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if len(candidates[i].factIDs) != 1 || candidates[i].factIDs[0] != wantID {
			t.Fatalf("candidate %d fact IDs = %#v, want [%s]", i, candidates[i].factIDs, wantID)
		}
	}
}

func assertAlphaFactRouteIndex(t testing.TB, memory *reteGraphBetaMemory, factID FactID, wantRoutes int) {
	t.Helper()
	routes := memory.alpha.factOwnership[factID].routes
	if got := len(routes); got != wantRoutes {
		t.Fatalf("alpha fact routes for %s = %d, want %d: %#v", factID, got, wantRoutes, routes)
	}
	if got := len(memory.matchedAlphaRouteIDsForFact(factID)); got != wantRoutes {
		t.Fatalf("matched alpha routes for %s = %d, want %d", factID, got, wantRoutes)
	}
}
