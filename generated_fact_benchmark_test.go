package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkGeneratedFactSession *Session

func BenchmarkGeneratedFactTemplateValuesInsert(b *testing.B) {
	ctx := context.Background()
	revision, templateKey := mustCompileGeneratedFactInsertRuleset(b)
	values := []Value{newStringValue("alpha"), newIntValue(1), newStringValue("stream")}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustSession(b, revision, SessionID(fmt.Sprintf("generated-values-insert-%d", i)))
		if err := session.insertTemplateValuesWithContextAndOrigin(ctx, templateKey, values, mutationOrigin{}); err != nil {
			b.Fatalf("insertTemplateValuesWithContextAndOrigin: %v", err)
		}
		benchmarkGeneratedFactSession = session
	}
}

func BenchmarkGeneratedFactPreparedInsert3(b *testing.B) {
	ctx := context.Background()
	revision, templateKey := mustCompileGeneratedFactInsertRuleset(b)
	v0 := newStringValue("alpha")
	v1 := newIntValue(1)
	v2 := newStringValue("stream")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustSession(b, revision, SessionID(fmt.Sprintf("generated-prepared-insert-%d", i)))
		inserter, err := session.prepareTemplateValueInserter(templateKey)
		if err != nil {
			b.Fatalf("prepareTemplateValueInserter: %v", err)
		}
		if err := session.insertPreparedTemplateValuesBatchWithContext(ctx, func(batch *preparedTemplateValueBatch) error {
			return inserter.insert3(batch, v0, v1, v2)
		}); err != nil {
			b.Fatalf("insertPreparedTemplateValuesBatchWithContext: %v", err)
		}
		benchmarkGeneratedFactSession = session
	}
}

func BenchmarkGeneratedFactLargeSteadyStateRunOnly(b *testing.B) {
	ctx := context.Background()
	tc := largeSteadyStateScalingCase{streams: 8, limit: 128}
	revision := mustCompileLargeSteadyStateScalingRuleset(b, tc)

	b.ReportAllocs()
	b.ReportMetric(float64(tc.limit), "limit")
	b.ReportMetric(float64(largeSteadyStateRuleCount(tc)), "rules")
	b.ReportMetric(float64(largeSteadyStateFinalFacts(tc)), "final-facts")
	b.ReportMetric(float64(largeSteadyStateFiredCount(tc)), "fired/run")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		session := mustSeedLargeSteadyStateScalingSession(b, revision, tc)
		b.StartTimer()

		result, err := session.Run(ctx)
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != largeSteadyStateFiredCount(tc) {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, largeSteadyStateFiredCount(tc))
		}
		benchmarkLargeSteadyStateRunResult = result
		benchmarkGeneratedFactSession = session
	}
}

func BenchmarkGeneratedFactMixedCascadeRunOnly(b *testing.B) {
	ctx := context.Background()
	tc := mixedCascadeScalingCase{streams: 8, limit: 64}
	revision := mustCompileMixedCascadeScalingRuleset(b, tc)

	b.ReportAllocs()
	b.ReportMetric(float64(tc.limit), "limit")
	b.ReportMetric(float64(mixedCascadeRuleCount(tc)), "rules")
	b.ReportMetric(float64(mixedCascadeFinalFacts(tc)), "final-facts")
	b.ReportMetric(float64(mixedCascadeFiredCount(tc)), "fired/run")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		session := mustSeedMixedCascadeScalingSession(b, revision, tc)
		b.StartTimer()

		result, err := session.Run(ctx)
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != mixedCascadeFiredCount(tc) {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, mixedCascadeFiredCount(tc))
		}
		benchmarkMixedCascadeRunResult = result
		benchmarkGeneratedFactSession = session
	}
}

func mustCompileGeneratedFactInsertRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "generated-fact",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "step", Kind: ValueInt, Required: true},
			{Name: "stream", Kind: ValueString, Required: true},
		},
	})
	return mustCompileWorkspace(t, workspace), generated.Key()
}
