package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkPureFunctionPredicateRunResult RunResult

type pureFunctionPredicateBenchmarkCase struct {
	systems int
}

func TestPureFunctionPredicateBenchmarkFixtureMatchesContract(t *testing.T) {
	tc := pureFunctionPredicateBenchmarkCase{systems: 32}
	revision := mustCompilePureFunctionPredicateRuleset(t)
	session := mustPureFunctionPredicateSession(t, revision, tc)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertPureFunctionPredicateBenchmarkResult(t, session, result, tc)
}

func BenchmarkGessPureFunctionPredicatesResetRun(b *testing.B) {
	for _, tc := range pureFunctionPredicateBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompilePureFunctionPredicateRuleset(b)
			session := mustPureFunctionPredicateSession(b, revision, tc)
			reportPureFunctionPredicateBenchmarkMetrics(b, tc)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				result, err := session.Run(ctx)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				assertPureFunctionPredicateBenchmarkResult(b, session, result, tc)
				benchmarkPureFunctionPredicateRunResult = result
			}
			reportPureFunctionPredicatePropagationMetrics(b, revision, tc)
		})
	}
}

func BenchmarkGessPureFunctionPredicatesRunOnly(b *testing.B) {
	for _, tc := range pureFunctionPredicateBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompilePureFunctionPredicateRuleset(b)
			session := mustPureFunctionPredicateSession(b, revision, tc)
			reportPureFunctionPredicateBenchmarkMetrics(b, tc)
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
				assertPureFunctionPredicateBenchmarkResult(b, session, result, tc)
				benchmarkPureFunctionPredicateRunResult = result
			}
			reportPureFunctionPredicatePropagationMetrics(b, revision, tc)
		})
	}
}

func pureFunctionPredicateBenchmarkCases() []pureFunctionPredicateBenchmarkCase {
	return []pureFunctionPredicateBenchmarkCase{{systems: 128}, {systems: 512}, {systems: 2048}}
}

func reportPureFunctionPredicateBenchmarkMetrics(b *testing.B, tc pureFunctionPredicateBenchmarkCase) {
	b.Helper()
	b.ReportAllocs()
	b.ReportMetric(float64(tc.systems), "systems")
	b.ReportMetric(float64(tc.initialFacts()), "initial-facts")
	b.ReportMetric(float64(tc.finalFacts()), "final-facts")
	b.ReportMetric(float64(tc.firedCount()), "fired/run")
}

func reportPureFunctionPredicatePropagationMetrics(b *testing.B, revision *Ruleset, tc pureFunctionPredicateBenchmarkCase) {
	b.Helper()
	propagation := collectPureFunctionPredicatePropagationCounters(b, revision, tc)
	propagation.reportMetrics(func(name string, value float64) {
		b.ReportMetric(value, name)
	})
}

func mustCompilePureFunctionPredicateRuleset(t testing.TB) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	system := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "pf-system",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "environment", Kind: ValueString, Required: true},
		},
	})
	finding := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "pf-finding",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "system-id", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	alert := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "pf-alert",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"system-id"},
		Fields: []FieldSpec{
			{Name: "system-id", Kind: ValueString, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "pf-high-score",
		Args:   []ValueKind{ValueInt},
		Return: ValueBool,
		Func1: func(_ context.Context, arg Value) (Value, error) {
			score, _ := arg.AsInt64()
			return NewValue(score >= 90)
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:               "pf-same-id",
		Args:               []ValueKind{ValueString, ValueString},
		Return:             ValueBool,
		EqualityComparator: true,
		Func2: func(_ context.Context, arg0, arg1 Value) (Value, error) {
			left, _ := arg0.AsString()
			right, _ := arg1.AsString()
			return NewValue(left == right)
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-pf-alert",
		Fn: func(ctx ActionContext) error {
			systemID, ok := ctx.BindingScalarValue("system", "id")
			if !ok {
				return fmt.Errorf("system id binding is unavailable")
			}
			return ctx.AssertTemplateValues(alert.Key(), systemID)
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "pure-function-hit",
		Conditions: []RuleConditionSpec{
			{Binding: "system", Target: TemplateKeyFact(system.Key())},
			{
				Binding: "finding",

				Predicates: []ExpressionSpec{
					Call("pf-high-score", CurrentFieldExpr{Field: "score"}),
					Call("pf-same-id", CurrentFieldExpr{Field: "system-id"}, BindingFieldExpr{Binding: "system", Field: "id"}),
				}, Target: TemplateKeyFact(finding.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "assert-pf-alert"}},
	})
	return mustCompileWorkspace(t, workspace)
}

func mustPureFunctionPredicateSession(t testing.TB, revision *Ruleset, tc pureFunctionPredicateBenchmarkCase) *Session {
	t.Helper()
	session, err := NewSession(revision, WithInitialFacts(pureFunctionPredicateInitialFacts(tc)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func pureFunctionPredicateInitialFacts(tc pureFunctionPredicateBenchmarkCase) []SessionInitialFact {
	initials := make([]SessionInitialFact, 0, tc.initialFacts())
	for i := 0; i < tc.systems; i++ {
		match := i%4 == 0
		systemID := fmt.Sprintf("S-%06d", i)
		score := int64(95)
		if !match {
			score = 40
		}
		findingSystemID := systemID
		if i%4 == 2 {
			findingSystemID = fmt.Sprintf("S-miss-%06d", i)
			score = 95
		}
		initials = append(initials,
			SessionInitialFact{TemplateKey: TemplateKey("pf-system"), Fields: Fields{"id": newStringValue(systemID), "environment": newStringValue("prod")}},
			SessionInitialFact{TemplateKey: TemplateKey("pf-finding"), Fields: Fields{"id": newStringValue(fmt.Sprintf("F-%06d", i)), "system-id": newStringValue(findingSystemID), "score": newIntValue(score)}},
		)
	}
	return initials
}

func (tc pureFunctionPredicateBenchmarkCase) initialFacts() int { return tc.systems * 2 }
func (tc pureFunctionPredicateBenchmarkCase) finalFacts() int {
	return tc.initialFacts() + tc.firedCount()
}
func (tc pureFunctionPredicateBenchmarkCase) firedCount() int { return (tc.systems + 3) / 4 }

func assertPureFunctionPredicateBenchmarkResult(t testing.TB, session *Session, result RunResult, tc pureFunctionPredicateBenchmarkCase) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != tc.firedCount() {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, tc.firedCount())
	}
	if got := len(session.facts); got != tc.finalFacts() {
		t.Fatalf("final facts = %d, want %d", got, tc.finalFacts())
	}
}

func collectPureFunctionPredicatePropagationCounters(t testing.TB, revision *Ruleset, tc pureFunctionPredicateBenchmarkCase) propagationCounterSnapshot {
	t.Helper()
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	for _, fact := range pureFunctionPredicateInitialFacts(tc) {
		if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertPureFunctionPredicateBenchmarkResult(t, session, result, tc)
	return session.propagationCounterSnapshot()
}
