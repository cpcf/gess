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
