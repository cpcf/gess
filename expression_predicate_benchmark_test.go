package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkExpressionPredicateRunResult RunResult
var benchmarkExpressionPredicateResetResult ResetResult

type expressionPredicateBenchmarkCase struct {
	systems int
}

func TestExpressionPredicateBenchmarkFixtureMatchesContract(t *testing.T) {
	tc := expressionPredicateBenchmarkCase{systems: 32}
	revision := mustCompileExpressionPredicateRuleset(t)
	session := mustExpressionPredicateSession(t, revision, tc)

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertExpressionPredicateBenchmarkResult(t, session, result, tc)
}

func BenchmarkGessExpressionPredicatesResetRun(b *testing.B) {
	for _, tc := range expressionPredicateBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileExpressionPredicateRuleset(b)
			session := mustExpressionPredicateSession(b, revision, tc)

			reportExpressionPredicateBenchmarkMetrics(b, tc)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				result, err := session.Run(ctx)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				assertExpressionPredicateBenchmarkResult(b, session, result, tc)
				benchmarkExpressionPredicateRunResult = result
			}
			reportExpressionPredicatePropagationMetrics(b, revision, tc)
		})
	}
}

func BenchmarkGessExpressionPredicatesResetOnly(b *testing.B) {
	for _, tc := range expressionPredicateBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileExpressionPredicateRuleset(b)
			session := mustExpressionPredicateSession(b, revision, tc)

			reportExpressionPredicateBenchmarkMetrics(b, tc)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := session.Reset(ctx)
				if err != nil {
					b.Fatalf("Reset: %v", err)
				}
				if result.Status != ResetApplied {
					b.Fatalf("reset status = %v, want %v", result.Status, ResetApplied)
				}
				if got := len(session.factsByID); got != tc.initialFacts() {
					b.Fatalf("fact count after reset = %d, want %d", got, tc.initialFacts())
				}
				benchmarkExpressionPredicateResetResult = result
			}
		})
	}
}

func BenchmarkGessExpressionPredicatesRunOnly(b *testing.B) {
	for _, tc := range expressionPredicateBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileExpressionPredicateRuleset(b)
			session := mustExpressionPredicateSession(b, revision, tc)

			reportExpressionPredicateBenchmarkMetrics(b, tc)
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
				assertExpressionPredicateBenchmarkResult(b, session, result, tc)
				benchmarkExpressionPredicateRunResult = result
			}
			reportExpressionPredicatePropagationMetrics(b, revision, tc)
		})
	}
}

func expressionPredicateBenchmarkCases() []expressionPredicateBenchmarkCase {
	return []expressionPredicateBenchmarkCase{
		{systems: 128},
		{systems: 512},
		{systems: 2048},
	}
}

func reportExpressionPredicateBenchmarkMetrics(b *testing.B, tc expressionPredicateBenchmarkCase) {
	b.Helper()
	b.ReportAllocs()
	b.ReportMetric(float64(tc.systems), "systems")
	b.ReportMetric(float64(tc.initialFacts()), "initial-facts")
	b.ReportMetric(float64(tc.finalFacts()), "final-facts")
	b.ReportMetric(float64(tc.firedCount()), "fired/run")
}

func reportExpressionPredicatePropagationMetrics(b *testing.B, revision *Ruleset, tc expressionPredicateBenchmarkCase) {
	b.Helper()
	propagation := collectExpressionPredicatePropagationCounters(b, revision, tc)
	propagation.reportMetrics(func(name string, value float64) {
		b.ReportMetric(value, name)
	})
}

func mustCompileExpressionPredicateRuleset(t testing.TB) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	system := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "system",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "criticality", Kind: ValueString, Required: true},
			{Name: "environment", Kind: ValueString, Required: true},
		},
	})
	finding := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "finding",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "system-id", Kind: ValueString, Required: true},
			{Name: "cve", Kind: ValueString, Required: true},
			{Name: "risk-score", Kind: ValueInt, Required: true},
			{Name: "age-days", Kind: ValueInt, Required: true},
		},
	})
	vulnerability := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "vulnerability",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"cve"},
		Fields: []FieldSpec{
			{Name: "cve", Kind: ValueString, Required: true},
			{Name: "known-exploited", Kind: ValueString, Required: true},
			{Name: "patch-available", Kind: ValueString, Required: true},
			{Name: "severity", Kind: ValueString, Required: true},
		},
	})
	alert := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "alert",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"system-id", "cve"},
		Fields: []FieldSpec{
			{Name: "system-id", Kind: ValueString, Required: true},
			{Name: "cve", Kind: ValueString, Required: true},
		},
	})

	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-alert",
		Fn: func(ctx ActionContext) error {
			cve, ok := ctx.BindingScalarValue("vulnerability", "cve")
			if !ok {
				return fmt.Errorf("vulnerability cve binding is unavailable")
			}
			systemID, ok := ctx.BindingScalarValue("system", "id")
			if !ok {
				return fmt.Errorf("system id binding is unavailable")
			}
			return ctx.AssertTemplateValues(alert.Key(), cve, systemID)
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "critical-unpatched-production-system",
		Conditions: []RuleConditionSpec{
			{
				Binding: "system",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "criticality", Operator: FieldConstraintEqual, Value: "critical"},
					{Field: "environment", Operator: FieldConstraintEqual, Value: "prod"},
				}, Target: TemplateKeyFact(system.Key()),
			},
			{
				Binding: "finding",

				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentFieldExpr{Field: "risk-score"},
						Right:    ConstExpr{Value: 90},
					},
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentFieldExpr{Field: "age-days"},
						Right:    ConstExpr{Value: 7},
					},
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "system-id"},
						Right:    BindingFieldExpr{Binding: "system", Field: "id"},
					},
				}, Target: TemplateKeyFact(finding.Key()),
			},
			{
				Binding: "vulnerability",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "known-exploited", Operator: FieldConstraintEqual, Value: "no"},
					{Field: "patch-available", Operator: FieldConstraintEqual, Value: "yes"},
					{Field: "severity", Operator: FieldConstraintEqual, Value: "critical"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "cve", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "finding", Field: "cve"}},
				}, Target: TemplateKeyFact(vulnerability.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "assert-alert"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}

func mustExpressionPredicateSession(t testing.TB, revision *Ruleset, tc expressionPredicateBenchmarkCase) *Session {
	t.Helper()

	session, err := NewSession(revision, WithInitialFacts(expressionPredicateInitialFacts(tc)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func expressionPredicateInitialFacts(tc expressionPredicateBenchmarkCase) []SessionInitialFact {
	initials := make([]SessionInitialFact, 0, tc.initialFacts())
	for i := 0; i < tc.systems; i++ {
		match := i%4 == 0
		systemID := fmt.Sprintf("S-%06d", i)
		cve := fmt.Sprintf("CVE-2026-%06d", i)

		criticality := "critical"
		environment := "prod"
		riskScore := int64(95)
		ageDays := int64(14)
		severity := "critical"
		if !match {
			switch i % 4 {
			case 1:
				criticality = "medium"
			case 2:
				riskScore = 80
			case 3:
				severity = "high"
			}
		}

		initials = append(initials,
			SessionInitialFact{
				TemplateKey: TemplateKey("system"),
				Fields: Fields{
					"id":          newStringValue(systemID),
					"criticality": newStringValue(criticality),
					"environment": newStringValue(environment),
				},
			},
			SessionInitialFact{
				TemplateKey: TemplateKey("finding"),
				Fields: Fields{
					"id":         newStringValue(fmt.Sprintf("F-%06d", i)),
					"system-id":  newStringValue(systemID),
					"cve":        newStringValue(cve),
					"risk-score": newIntValue(riskScore),
					"age-days":   newIntValue(ageDays),
				},
			},
			SessionInitialFact{
				TemplateKey: TemplateKey("vulnerability"),
				Fields: Fields{
					"cve":             newStringValue(cve),
					"known-exploited": newStringValue("no"),
					"patch-available": newStringValue("yes"),
					"severity":        newStringValue(severity),
				},
			},
		)
	}
	return initials
}

func assertExpressionPredicateBenchmarkResult(t testing.TB, session *Session, result RunResult, tc expressionPredicateBenchmarkCase) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != tc.firedCount() {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, tc.firedCount())
	}
	if got := len(session.factsByID); got != tc.finalFacts() {
		t.Fatalf("final fact count = %d, want %d", got, tc.finalFacts())
	}
}

func collectExpressionPredicatePropagationCounters(t testing.TB, revision *Ruleset, tc expressionPredicateBenchmarkCase) propagationCounterSnapshot {
	t.Helper()

	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	for _, fact := range expressionPredicateInitialFacts(tc) {
		if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertExpressionPredicateBenchmarkResult(t, session, result, tc)
	return session.propagationCounterSnapshot()
}

func (tc expressionPredicateBenchmarkCase) initialFacts() int {
	return tc.systems * 3
}

func (tc expressionPredicateBenchmarkCase) firedCount() int {
	return (tc.systems + 3) / 4
}

func (tc expressionPredicateBenchmarkCase) finalFacts() int {
	return tc.initialFacts() + tc.firedCount()
}
