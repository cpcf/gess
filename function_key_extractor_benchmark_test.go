package gess

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

var benchmarkFunctionKeyExtractorRunResult RunResult

type functionKeyExtractorBenchmarkCase struct {
	systems int
	indexed bool
}

func TestFunctionKeyExtractorBenchmarkFixtureMatchesContract(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		tc := functionKeyExtractorBenchmarkCase{systems: 64, indexed: indexed}
		revision := mustCompileFunctionKeyExtractorRuleset(t, indexed)
		session := mustFunctionKeyExtractorSession(t, revision, tc)
		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run indexed=%v: %v", indexed, err)
		}
		assertFunctionKeyExtractorBenchmarkResult(t, session, result, tc)
	}
}

func BenchmarkGessFunctionKeyExtractorResetRun(b *testing.B) {
	for _, tc := range functionKeyExtractorBenchmarkCases() {
		name := fmt.Sprintf("indexed=%v/systems=%d/initial-facts=%d/fired=%d", tc.indexed, tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileFunctionKeyExtractorRuleset(b, tc.indexed)
			session := mustFunctionKeyExtractorSession(b, revision, tc)
			reportFunctionKeyExtractorBenchmarkMetrics(b, tc)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				result, err := session.Run(ctx)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				assertFunctionKeyExtractorBenchmarkResult(b, session, result, tc)
				benchmarkFunctionKeyExtractorRunResult = result
			}
			b.StopTimer()
			reportFunctionKeyExtractorPropagationMetrics(b, revision, tc)
		})
	}
}

func BenchmarkGessFunctionKeyExtractorRunOnly(b *testing.B) {
	for _, tc := range functionKeyExtractorBenchmarkCases() {
		name := fmt.Sprintf("indexed=%v/systems=%d/initial-facts=%d/fired=%d", tc.indexed, tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileFunctionKeyExtractorRuleset(b, tc.indexed)
			session := mustFunctionKeyExtractorSession(b, revision, tc)
			reportFunctionKeyExtractorBenchmarkMetrics(b, tc)
			b.ResetTimer()
			b.StopTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				b.StartTimer()
				result, err := session.Run(ctx)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				assertFunctionKeyExtractorBenchmarkResult(b, session, result, tc)
				benchmarkFunctionKeyExtractorRunResult = result
			}
			reportFunctionKeyExtractorPropagationMetrics(b, revision, tc)
		})
	}
}

func functionKeyExtractorBenchmarkCases() []functionKeyExtractorBenchmarkCase {
	return []functionKeyExtractorBenchmarkCase{
		{systems: 128, indexed: false},
		{systems: 128, indexed: true},
		{systems: 512, indexed: false},
		{systems: 512, indexed: true},
		{systems: 2048, indexed: false},
		{systems: 2048, indexed: true},
	}
}

func mustCompileFunctionKeyExtractorRuleset(t testing.TB, indexed bool) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	system := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "fk-system",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	finding := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "fk-finding",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "system-id", Kind: ValueString, Required: true},
		},
	})
	alert := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "fk-alert",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:              "fk-fold-key",
		Args:              []ValueKind{ValueString},
		Return:            ValueString,
		IndexKeyExtractor: indexed,
		Func1: func(_ context.Context, value Value) (Value, error) {
			text, _ := value.AsString()
			return NewValue(strings.ToLower(text))
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-fk-alert",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: alert.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "finding", Field: "id"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "function-key-extractor",
		Conditions: []RuleConditionSpec{
			{Binding: "system", Target: TemplateKeyFact(system.Key())},
			{
				Binding: "finding",

				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     Call("fk-fold-key", CurrentFieldExpr{Field: "system-id"}),
					Right:    Call("fk-fold-key", BindingFieldExpr{Binding: "system", Field: "id"}),
				}}, Target: TemplateKeyFact(finding.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "assert-fk-alert"}},
	})
	return mustCompileWorkspace(t, workspace)
}

func mustFunctionKeyExtractorSession(t testing.TB, revision *Ruleset, tc functionKeyExtractorBenchmarkCase) *Session {
	t.Helper()
	session, err := NewSession(revision, WithInitialFacts(functionKeyExtractorInitialFacts(tc)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func functionKeyExtractorInitialFacts(tc functionKeyExtractorBenchmarkCase) []SessionInitialFact {
	initials := make([]SessionInitialFact, 0, tc.initialFacts())
	for i := 0; i < tc.systems; i++ {
		systemID := fmt.Sprintf("S-%06d", i)
		findingID := fmt.Sprintf("F-%06d", i)
		initials = append(initials,
			SessionInitialFact{
				TemplateKey: TemplateKey("fk-system"),
				Fields: Fields{
					"id": newStringValue(systemID),
				},
			},
			SessionInitialFact{
				TemplateKey: TemplateKey("fk-finding"),
				Fields: Fields{
					"id":        newStringValue(findingID),
					"system-id": newStringValue(strings.ToLower(systemID)),
				},
			},
		)
	}
	return initials
}

func (tc functionKeyExtractorBenchmarkCase) initialFacts() int { return tc.systems * 2 }
func (tc functionKeyExtractorBenchmarkCase) finalFacts() int {
	return tc.initialFacts() + tc.firedCount()
}
func (tc functionKeyExtractorBenchmarkCase) firedCount() int { return tc.systems }

func assertFunctionKeyExtractorBenchmarkResult(t testing.TB, session *Session, result RunResult, tc functionKeyExtractorBenchmarkCase) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != tc.firedCount() {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, tc.firedCount())
	}
	if got := len(session.factsByID); got != tc.finalFacts() {
		t.Fatalf("final facts = %d, want %d", got, tc.finalFacts())
	}
}

func reportFunctionKeyExtractorBenchmarkMetrics(b *testing.B, tc functionKeyExtractorBenchmarkCase) {
	b.Helper()
	b.ReportAllocs()
	indexed := 0
	if tc.indexed {
		indexed = 1
	}
	b.ReportMetric(float64(indexed), "indexed")
	b.ReportMetric(float64(tc.systems), "systems")
	b.ReportMetric(float64(tc.initialFacts()), "initial-facts")
	b.ReportMetric(float64(tc.finalFacts()), "final-facts")
	b.ReportMetric(float64(tc.firedCount()), "fired/run")
}

func reportFunctionKeyExtractorPropagationMetrics(b *testing.B, revision *Ruleset, tc functionKeyExtractorBenchmarkCase) {
	b.Helper()
	propagation := collectFunctionKeyExtractorPropagationCounters(b, revision, tc)
	propagation.reportMetrics(func(name string, value float64) {
		b.ReportMetric(value, name)
	})
}

func collectFunctionKeyExtractorPropagationCounters(t testing.TB, revision *Ruleset, tc functionKeyExtractorBenchmarkCase) propagationCounterSnapshot {
	t.Helper()
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	for _, fact := range functionKeyExtractorInitialFacts(tc) {
		if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertFunctionKeyExtractorBenchmarkResult(t, session, result, tc)
	return session.propagationCounterSnapshot()
}
