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

	assertAlphaLiteralEqualityCandidates(t, revision, session, ruleName, hot.Fact.ID())
	if _, err := session.Modify(ctx, hot.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "cold"})}); err != nil {
		t.Fatalf("Modify(hot -> cold): %v", err)
	}
	assertAlphaLiteralEqualityCandidates(t, revision, session, ruleName)
	if _, err := session.Modify(ctx, cold.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"category": "hot"})}); err != nil {
		t.Fatalf("Modify(cold -> hot): %v", err)
	}
	assertAlphaLiteralEqualityCandidates(t, revision, session, ruleName, cold.Fact.ID())
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
		routeIDs := runtime.graphBeta.snapshotAlphaRouteIDsForFactInsert(fact)
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
