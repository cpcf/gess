package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkNestedPathRunResult RunResult
var benchmarkNestedPathResetResult ResetResult

type nestedPathBenchmarkCase struct {
	events int
}

func BenchmarkGessNestedPathPredicatesRunOnly(b *testing.B) {
	for _, tc := range nestedPathBenchmarkCases() {
		name := fmt.Sprintf("events=%d/initial-facts=%d/fired=%d", tc.events, tc.events, tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileNestedPathBenchmarkRuleset(b)
			session := mustNestedPathBenchmarkSession(b, revision, tc)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.events), "events")
			b.ReportMetric(float64(tc.firedCount()), "fired/run")
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
				if result.Fired != tc.firedCount() {
					b.Fatalf("fired = %d, want %d", result.Fired, tc.firedCount())
				}
				benchmarkNestedPathRunResult = result
			}
			propagation := collectNestedPathBenchmarkPropagationCounters(b, revision, tc)
			propagation.reportMetrics(func(name string, value float64) {
				b.ReportMetric(value, name)
			})
		})
	}
}

func BenchmarkGessNestedPathPredicatesResetOnly(b *testing.B) {
	for _, tc := range nestedPathBenchmarkCases() {
		name := fmt.Sprintf("events=%d/initial-facts=%d/fired=%d", tc.events, tc.events, tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileNestedPathBenchmarkRuleset(b)
			session := mustNestedPathBenchmarkSession(b, revision, tc)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.events), "events")
			b.ReportMetric(float64(tc.firedCount()), "fired/run")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := session.Reset(ctx)
				if err != nil {
					b.Fatalf("Reset: %v", err)
				}
				if result.Status != ResetApplied {
					b.Fatalf("reset status = %v, want %v", result.Status, ResetApplied)
				}
				if got := len(session.facts); got != tc.events {
					b.Fatalf("fact count after reset = %d, want %d", got, tc.events)
				}
				benchmarkNestedPathResetResult = result
			}
			propagation := collectNestedPathBenchmarkPropagationCounters(b, revision, tc)
			propagation.reportMetrics(func(name string, value float64) {
				b.ReportMetric(value, name)
			})
		})
	}
}

func nestedPathBenchmarkCases() []nestedPathBenchmarkCase {
	return []nestedPathBenchmarkCase{
		{events: 128},
		{events: 512},
		{events: 2048},
	}
}

func (tc nestedPathBenchmarkCase) firedCount() int {
	count := 0
	for i := 0; i < tc.events; i++ {
		if nestedPathBenchmarkMatches(i) {
			count++
		}
	}
	return count
}

func nestedPathBenchmarkMatches(i int) bool {
	return i%4 == 0 || i%4 == 1 || (i%4 == 2 && i%3 == 0)
}

func mustCompileNestedPathBenchmarkRuleset(t testing.TB) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "nested-risk-event",
		ConditionTree: Or{Conditions: []ConditionSpec{
			Match{
				Binding: "event",

				FieldConstraints: []FieldConstraintSpec{
					{
						Path:     Path("payload", MapKey("risk"), MapKey("score")),
						Operator: FieldConstraintGreaterOrEqual,
						Value:    90,
					},
					{
						Path:     Path("payload", MapKey("evidence"), MapKey("runtime"), MapKey("observed")),
						Operator: FieldConstraintEqual,
						Value:    true,
					},
				}, Target: DynamicFact("event"),
			},
			Match{
				Binding: "event",

				Predicates: []ExpressionSpec{
					HasPath(Path("payload", MapKey("risk"), MapKey("score"))),
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentPath(Path("payload", MapKey("risk"), MapKey("score"))),
						Right:    ConstExpr{Value: 80},
					},
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentPath(Path("payload", MapKey("source"))),
						Right:    ConstExpr{Value: "runtime"},
					},
				}, Target: DynamicFact("event"),
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace)
}

func mustNestedPathBenchmarkSession(t testing.TB, revision *Ruleset, tc nestedPathBenchmarkCase) *Session {
	t.Helper()

	session, err := NewSession(revision, WithInitialFacts(nestedPathBenchmarkInitialFacts(t, tc)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func nestedPathBenchmarkInitialFacts(t testing.TB, tc nestedPathBenchmarkCase) []SessionInitialFact {
	t.Helper()

	initials := make([]SessionInitialFact, 0, tc.events)
	for i := 0; i < tc.events; i++ {
		risk := 70
		observed := false
		source := "batch"
		switch i % 4 {
		case 0:
			risk = 95
			observed = true
			source = "scanner"
		case 1:
			risk = 85
			source = "runtime"
		case 2:
			risk = 92
			source = "batch"
		}
		if i%3 == 0 {
			source = "runtime"
		}
		initials = append(initials, SessionInitialFact{
			Name: "event",
			Fields: mustFields(t, map[string]any{
				"id": fmt.Sprintf("event-%06d", i),
				"payload": map[string]any{
					"risk": map[string]any{
						"score": risk,
					},
					"evidence": map[string]any{
						"runtime": map[string]any{
							"observed": observed,
						},
					},
					"source": source,
				},
			}),
		})
	}
	return initials
}

func collectNestedPathBenchmarkPropagationCounters(t testing.TB, revision *Ruleset, tc nestedPathBenchmarkCase) propagationCounterSnapshot {
	t.Helper()

	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	ctx := context.Background()
	for _, fact := range nestedPathBenchmarkInitialFacts(t, tc) {
		if _, err := session.Assert(ctx, fact.Name, fact.Fields); err != nil {
			t.Fatalf("Assert(%q): %v", fact.Name, err)
		}
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != tc.firedCount() {
		t.Fatalf("fired = %d, want %d", result.Fired, tc.firedCount())
	}
	return session.propagationCounterSnapshot()
}
