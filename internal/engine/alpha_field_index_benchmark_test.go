package engine

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkAlphaFieldIndexCandidates []matchCandidate
var benchmarkAlphaFieldIndexRuntime *reteRuntime

func BenchmarkAlphaLiteralEqualityCandidateScan(b *testing.B) {
	ctx := context.Background()
	const factCount = 4096
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(b)
	diagnosticMetrics := collectAlphaLiteralEqualityCandidateMetrics(b, ctx, revision, templateKey, ruleName, factCount)
	session := mustAlphaLiteralEqualitySession(b, ctx, revision, templateKey, factCount)
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
	reportAlphaLiteralEqualityCandidateMetrics(b, diagnosticMetrics)
}

func BenchmarkAlphaLiteralEqualityQueryGraphReset(b *testing.B) {
	ctx := context.Background()
	const factCount = 4096
	revision, templateKey, queryName := mustCompileAlphaLiteralEqualityQueryRuleset(b)
	session := mustAlphaLiteralEqualitySession(b, ctx, revision, templateKey, factCount)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		b.Fatalf("Snapshot: %v", err)
	}
	facts := snapshot.Facts()
	runtime, err := newReteRuntime(revision)
	if err != nil {
		b.Fatalf("newReteRuntime: %v", err)
	}
	if _, ok := revision.query(queryName); !ok {
		b.Fatalf("query %q missing", queryName)
	}

	b.ReportAllocs()
	b.ReportMetric(float64(factCount), "facts")
	b.ResetTimer()
	for b.Loop() {
		if err := runtime.resetGraphBeta(ctx, facts); err != nil {
			b.Fatalf("resetGraphBeta: %v", err)
		}
		benchmarkAlphaFieldIndexRuntime = runtime
	}
}

func BenchmarkAlphaLiteralEqualityGraphReset(b *testing.B) {
	ctx := context.Background()
	const factCount = 4096
	revision, templateKey, _ := mustCompileAlphaLiteralEqualityRuleset(b)
	session := mustAlphaLiteralEqualitySession(b, ctx, revision, templateKey, factCount)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		b.Fatalf("Snapshot: %v", err)
	}
	facts := snapshot.Facts()
	runtime, err := newReteRuntime(revision)
	if err != nil {
		b.Fatalf("newReteRuntime: %v", err)
	}

	b.ReportAllocs()
	b.ReportMetric(float64(factCount), "facts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runtime.resetGraphBeta(ctx, facts); err != nil {
			b.Fatalf("resetGraphBeta: %v", err)
		}
		benchmarkAlphaFieldIndexRuntime = runtime
	}
}

func BenchmarkAlphaLiteralEqualityGraphResetAndCandidateScan(b *testing.B) {
	ctx := context.Background()
	const factCount = 4096
	revision, templateKey, ruleName := mustCompileAlphaLiteralEqualityRuleset(b)
	session := mustAlphaLiteralEqualitySession(b, ctx, revision, templateKey, factCount)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		b.Fatalf("Snapshot: %v", err)
	}
	facts := snapshot.Facts()
	rule := revision.rules[ruleName]
	runtime, err := newReteRuntime(revision)
	if err != nil {
		b.Fatalf("newReteRuntime: %v", err)
	}

	b.ReportAllocs()
	b.ReportMetric(float64(factCount), "facts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runtime.resetGraphBeta(ctx, facts); err != nil {
			b.Fatalf("resetGraphBeta: %v", err)
		}
		candidates, err := rule.matchCandidates(ctx, runtime.graphBeta)
		if err != nil {
			b.Fatalf("matchCandidates: %v", err)
		}
		if len(candidates) != 1 {
			b.Fatalf("candidate count = %d, want 1", len(candidates))
		}
		benchmarkAlphaFieldIndexCandidates = candidates
		benchmarkAlphaFieldIndexRuntime = runtime
	}
}

func collectAlphaLiteralEqualityCandidateMetrics(b *testing.B, ctx context.Context, revision *Ruleset, templateKey TemplateKey, ruleName string, factCount int) propagationCounterSnapshot {
	b.Helper()

	session := mustAlphaLiteralEqualitySession(b, ctx, revision, templateKey, factCount)
	session.attachPropagationCounters()
	rule := revision.rules[ruleName]
	candidates, err := rule.matchCandidates(ctx, session)
	if err != nil {
		b.Fatalf("diagnostic matchCandidates: %v", err)
	}
	if len(candidates) != 1 {
		b.Fatalf("diagnostic candidate count = %d, want 1", len(candidates))
	}
	return session.propagationCounterSnapshot()
}

func reportAlphaLiteralEqualityCandidateMetrics(b *testing.B, snapshot propagationCounterSnapshot) {
	b.Helper()

	b.ReportMetric(float64(snapshot.Totals.AlphaIndexProbes), "propagation-alpha-index-probes")
	b.ReportMetric(float64(snapshot.Totals.AlphaIndexHits), "propagation-alpha-index-hits")
	b.ReportMetric(float64(snapshot.Totals.AlphaIndexMisses), "propagation-alpha-index-misses")
	b.ReportMetric(float64(snapshot.Totals.AlphaIndexFallbackScans), "propagation-alpha-index-fallback-scans")
}

func mustAlphaLiteralEqualitySession(t testing.TB, ctx context.Context, revision *Ruleset, templateKey TemplateKey, factCount int) *Session {
	t.Helper()

	session := mustSession(t, revision, SessionID(fmt.Sprintf("alpha-literal-equality-session-%d", factCount)))
	for i := range factCount {
		category := "cold"
		if i == factCount/2 {
			category = "hot"
		}
		if _, err := session.AssertTemplate(ctx, templateKey, Fields{
			"category": newStringValue(category),
			"score":    newIntValue(int64(i)),
		}); err != nil {
			t.Fatalf("AssertTemplate(%d): %v", i, err)
		}
	}
	return session
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
				Binding: "event",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "category", Operator: FieldConstraintEqual, Value: "hot"},
				}, Target: TemplateKeyFact(event.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	return mustCompileWorkspace(t, workspace), event.Key(), ruleName
}

func mustCompileAlphaLiteralEqualityQueryRuleset(t testing.TB) (*Ruleset, TemplateKey, string) {
	t.Helper()

	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "alpha-literal-query-event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "category", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	queryName := "hot-alpha-literal-events"
	if err := workspace.AddQuery(QuerySpec{
		Name:       queryName,
		Parameters: []QueryParameterSpec{{Name: "min_score", Kind: ValueInt}},
		ConditionTree: Match{
			Binding: "e",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "category", Operator: FieldConstraintEqual, Value: "hot"},
			},
			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareGreaterOrEqual, Left: CurrentFieldExpr{Field: "score"}, Right: ParamExpr{Name: "min_score"}},
			}, Target: TemplateKeyFact(event.Key()),
		},
		Returns: []QueryReturnSpec{
			ReturnValue("score", BindingFieldExpr{Binding: "e", Field: "score"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	return mustCompileWorkspace(t, workspace), event.Key(), queryName
}
