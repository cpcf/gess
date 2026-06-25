package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkAlphaFieldIndexCandidates []matchCandidate

func BenchmarkAlphaLiteralEqualityCandidateScan(b *testing.B) {
	ctx := context.Background()
	const factCount = 4096
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(b)
	session := mustSession(b, revision, "alpha-literal-equality-candidate-scan-session")
	for i := range factCount {
		category := "cold"
		if i == factCount/2 {
			category = "hot"
		}
		if _, err := session.AssertTemplate(ctx, templateKey, Fields{
			"category": newStringValue(category),
			"score":    newIntValue(int64(i)),
		}); err != nil {
			b.Fatalf("AssertTemplate(%d): %v", i, err)
		}
	}
	rule := revision.rules[ruleName]

	b.ReportAllocs()
	b.ReportMetric(float64(factCount), "facts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates, err := rule.matchCandidates(ctx, session)
		if err != nil {
			b.Fatalf("matchCandidates: %v", err)
		}
		if len(candidates) != 1 {
			b.Fatalf("candidate count = %d, want 1", len(candidates))
		}
		benchmarkAlphaFieldIndexCandidates = candidates
	}
}

func mustCompileAlphaLiteralEqualityRuleset(t testing.TB) (*Ruleset, TemplateKey, string) {
	t.Helper()

	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "alpha-literal-event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "category", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	ruleName := fmt.Sprintf("%s-rule", event.Name())
	mustAddRule(t, workspace, RuleSpec{
		Name: ruleName,
		Conditions: []RuleConditionSpec{
			{
				Binding:     "event",
				TemplateKey: event.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "category", Operator: FieldConstraintEqual, Value: "hot"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	return mustCompileWorkspace(t, workspace), event.Key(), ruleName
}
