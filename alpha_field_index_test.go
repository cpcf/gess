package gess

import (
	"context"
	"testing"
)

func TestSessionAlphaLiteralEqualityIndexInvalidatesAcrossModify(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-literal-equality-index-session")

	cold, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1}))
	if err != nil {
		t.Fatalf("AssertTemplate(cold): %v", err)
	}
	hot, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("AssertTemplate(hot): %v", err)
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

	cold, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1}))
	if err != nil {
		t.Fatalf("AssertTemplate(cold): %v", err)
	}
	hot, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("AssertTemplate(hot): %v", err)
	}
	memory := session.rete.graphBeta
	if memory == nil {
		t.Fatal("graph beta memory is nil")
	}
	assertAlphaFactRouteIndex(t, memory, hot.Fact.ID(), 1)
	assertAlphaFactRouteIndex(t, memory, cold.Fact.ID(), 0)
	if got, want := len(memory.alphaFactOwnership), 1; got != want {
		t.Fatalf("alpha fact route index keys = %d, want %d", got, want)
	}

	if _, err := session.Modify(ctx, hot.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "cold"})}); err != nil {
		t.Fatalf("Modify(hot -> cold): %v", err)
	}
	assertAlphaFactRouteIndex(t, memory, hot.Fact.ID(), 0)
	if got := len(memory.alphaFactOwnership); got != 0 {
		t.Fatalf("alpha fact route index keys after modify out = %d, want 0", got)
	}

	if _, err := session.Modify(ctx, cold.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "hot"})}); err != nil {
		t.Fatalf("Modify(cold -> hot): %v", err)
	}
	assertAlphaFactRouteIndex(t, memory, cold.Fact.ID(), 1)
	if got, want := len(memory.alphaFactOwnership), 1; got != want {
		t.Fatalf("alpha fact route index keys after modify in = %d, want %d", got, want)
	}

	if _, err := session.Retract(ctx, cold.Fact.ID()); err != nil {
		t.Fatalf("Retract(cold): %v", err)
	}
	assertAlphaFactRouteIndex(t, memory, cold.Fact.ID(), 0)
	if got := len(memory.alphaFactOwnership); got != 0 {
		t.Fatalf("alpha fact route index keys after retract = %d, want 0", got)
	}
}

func TestGraphBetaAlphaFactRoutesClearTouchedKeysOnReset(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-fact-route-reset-session")

	hot, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2}))
	if err != nil {
		t.Fatalf("AssertTemplate(hot): %v", err)
	}
	memory := session.rete.graphBeta
	if memory == nil {
		t.Fatal("graph beta memory is nil")
	}
	assertAlphaFactRouteIndex(t, memory, hot.Fact.ID(), 1)
	if got := len(memory.alphaFactOwnershipIDs); got == 0 {
		t.Fatal("alpha fact route touched keys were not recorded")
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := len(memory.alphaFactOwnership); got != 0 {
		t.Fatalf("alpha fact route index keys after reset = %d, want 0", got)
	}
	if got := len(memory.alphaFactOwnershipIDs); got != 0 {
		t.Fatalf("alpha fact route touched keys after reset = %d, want 0", got)
	}
}

func TestGraphBetaAlphaLiteralEqualityIndexRecordsRouteCounters(t *testing.T) {
	ctx := context.Background()
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(t)
	session := mustSession(t, revision, "alpha-literal-equality-route-counters-session")
	session.attachPropagationCounters()

	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "cold", "score": 1})); err != nil {
		t.Fatalf("AssertTemplate(cold): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"category": "hot", "score": 2})); err != nil {
		t.Fatalf("AssertTemplate(hot): %v", err)
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
	rows, handled, err := runtime.queryRows(ctx, query, args, newReteGraphQueryTriggerEvent(snapshotQueryTriggerFact(snapshot.Generation(), query, args)), snapshot)
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
		case category.Kind() == ValueString && category.stringValue == "hot":
			if got, want := len(routeIDs), 1; got != want {
				t.Fatalf("hot fact route IDs = %d, want %d", got, want)
			}
		case category.Kind() == ValueString && category.stringValue == "cold":
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
	indexedFacts, ok := runtime.graphBeta.factsForTargetFieldEqual(conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey}, fieldSlot, value)
	if !ok {
		t.Fatal("factsForTargetFieldEqual returned !ok")
	}
	if got, want := len(indexedFacts), 1; got != want {
		t.Fatalf("factsForTargetFieldEqual facts = %d, want %d", got, want)
	}
	key := newFactFieldEqualKey(conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey}, fieldSlot, value)
	facts, ok := runtime.graphBeta.factFieldEqualIndexes[key]
	if !ok {
		t.Fatalf("graph field equality index missing key %#v", key)
	}
	if got, want := len(facts), 1; got != want {
		t.Fatalf("indexed facts = %d, want %d", got, want)
	}
	category, ok := facts[0].Field("category")
	if !ok || category.Kind() != ValueString || category.stringValue != "hot" {
		t.Fatalf("indexed fact category = %#v, want hot", category)
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
	routes := memory.alphaFactOwnership[factID].routes
	if got := len(routes); got != wantRoutes {
		t.Fatalf("alpha fact routes for %s = %d, want %d: %#v", factID, got, wantRoutes, routes)
	}
	if got := len(memory.matchedAlphaRouteIDsForFact(factID)); got != wantRoutes {
		t.Fatalf("matched alpha routes for %s = %d, want %d", factID, got, wantRoutes)
	}
}
