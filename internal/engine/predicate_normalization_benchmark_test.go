package engine

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkPredicateNormalizationRunResult RunResult

type predicateNormalizationBenchmarkCase struct {
	systems int
}

func TestPredicateNormalizationBenchmarkFixtureMatchesContract(t *testing.T) {
	tc := predicateNormalizationBenchmarkCase{systems: 32}
	revision := mustCompilePredicateNormalizationRuleset(t)
	session := mustPredicateNormalizationSession(t, revision, tc)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertPredicateNormalizationBenchmarkResult(t, session, result, tc)
}

func BenchmarkGessPredicateNormalizationResetRun(b *testing.B) {
	for _, tc := range predicateNormalizationBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompilePredicateNormalizationRuleset(b)
			session := mustPredicateNormalizationSession(b, revision, tc)
			reportPredicateNormalizationBenchmarkMetrics(b, tc)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				result, err := session.Run(ctx)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				assertPredicateNormalizationBenchmarkResult(b, session, result, tc)
				benchmarkPredicateNormalizationRunResult = result
			}
			b.StopTimer()
			reportPredicateNormalizationPropagationMetrics(b, revision, tc)
		})
	}
}

func BenchmarkGessPredicateNormalizationRunOnly(b *testing.B) {
	for _, tc := range predicateNormalizationBenchmarkCases() {
		name := fmt.Sprintf("systems=%d/initial-facts=%d/fired=%d", tc.systems, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompilePredicateNormalizationRuleset(b)
			session := mustPredicateNormalizationSession(b, revision, tc)
			reportPredicateNormalizationBenchmarkMetrics(b, tc)
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
				assertPredicateNormalizationBenchmarkResult(b, session, result, tc)
				benchmarkPredicateNormalizationRunResult = result
			}
			reportPredicateNormalizationPropagationMetrics(b, revision, tc)
		})
	}
}

func predicateNormalizationBenchmarkCases() []predicateNormalizationBenchmarkCase {
	return []predicateNormalizationBenchmarkCase{{systems: 128}, {systems: 512}, {systems: 2048}}
}

func reportPredicateNormalizationBenchmarkMetrics(b *testing.B, tc predicateNormalizationBenchmarkCase) {
	b.Helper()
	b.ReportAllocs()
	b.ReportMetric(float64(tc.systems), "systems")
	b.ReportMetric(float64(tc.initialFacts()), "initial-facts")
	b.ReportMetric(float64(tc.finalFacts()), "final-facts")
	b.ReportMetric(float64(tc.firedCount()), "fired/run")
}

func reportPredicateNormalizationPropagationMetrics(b *testing.B, revision *Ruleset, tc predicateNormalizationBenchmarkCase) {
	b.Helper()
	propagation := collectPredicateNormalizationPropagationCounters(b, revision, tc)
	propagation.reportMetrics(func(name string, value float64) {
		b.ReportMetric(value, name)
	})
}

func mustCompilePredicateNormalizationRuleset(t testing.TB) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	system := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "pn-system",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "risk", Kind: ValueInt, Required: true},
			{Name: "environment", Kind: ValueString, Required: true},
		},
	})
	finding := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "pn-finding",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "primary-system", Kind: ValueString, Required: true},
			{Name: "secondary-system", Kind: ValueString, Required: true},
			{Name: "risk-score", Kind: ValueInt, Required: true},
			{Name: "age-days", Kind: ValueInt, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
			{Name: "band", Kind: ValueString, Required: true},
		},
	})
	alert := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "pn-alert",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"rule", "id"},
		Fields: []FieldSpec{
			{Name: "rule", Kind: ValueString, Required: true},
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "pn-risk-band",
		Args:   []ValueKind{ValueInt},
		Return: ValueString,
		Func: func(_ context.Context, args []Value) (Value, error) {
			risk, _ := args[0].AsInt64()
			if risk >= 90 {
				return NewValue("high")
			}
			return NewValue("low")
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "pn-complex-ok",
		Args:   []ValueKind{ValueInt, ValueInt},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			score, _ := args[0].AsInt64()
			age, _ := args[1].AsInt64()
			mix := score + age
			for i := range int64(8) {
				mix = (mix*31 + score - age + i) % 997
			}
			return NewValue(score >= 90 && age >= 7 && mix >= 0)
		},
	})
	addPredicateNormalizationAlertAction(t, workspace, alert.Key(), "assert-pn-normalized-alert", "normalized")
	addPredicateNormalizationAlertAction(t, workspace, alert.Key(), "assert-pn-prefilter-alert", "prefilter")
	addPredicateNormalizationAlertAction(t, workspace, alert.Key(), "assert-pn-residual-alert", "residual")

	mustAddRule(t, workspace, RuleSpec{
		Name: "predicate-normalized-hit",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding: "system",

				FieldConstraints: []FieldConstraintSpec{{
					Field:    "environment",
					Operator: FieldConstraintEqual,
					Value:    "prod",
				}}, Target: TemplateKeyFact(system.Key()),
			},
			Match{
				Binding: "finding",

				FieldConstraints: []FieldConstraintSpec{{
					Field:    "band",
					Operator: FieldConstraintEqual,
					Value:    Call("pn-risk-band", BindingFieldExpr{Binding: "system", Field: "risk"}),
				}},
				Predicates: []ExpressionSpec{
					BooleanExpr{
						Operator: ExpressionBoolAnd,
						Operands: []ExpressionSpec{
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
						},
					},
					BooleanExpr{
						Operator: ExpressionBoolNot,
						Operands: []ExpressionSpec{CompareExpr{
							Operator: ExpressionCompareEqual,
							Left:     CurrentFieldExpr{Field: "status"},
							Right:    ConstExpr{Value: "closed"},
						}},
					},
					BooleanExpr{
						Operator: ExpressionBoolOr,
						Operands: []ExpressionSpec{
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "status"},
								Right:    ConstExpr{Value: "open"},
							},
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "status"},
								Right:    ConstExpr{Value: "pending"},
							},
						},
					},
					BooleanExpr{
						Operator: ExpressionBoolOr,
						Operands: []ExpressionSpec{
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "primary-system"},
								Right:    BindingFieldExpr{Binding: "system", Field: "id"},
							},
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "secondary-system"},
								Right:    BindingFieldExpr{Binding: "system", Field: "id"},
							},
						},
					},
				}, Target: TemplateKeyFact(finding.Key()),
			},
			Test{Expression: CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     BindingFieldExpr{Binding: "system", Field: "risk"},
				Right:    ConstExpr{Value: 90},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "assert-pn-normalized-alert"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "predicate-complex-prefilter",
		Conditions: []RuleConditionSpec{
			{
				Binding: "system",

				FieldConstraints: []FieldConstraintSpec{{
					Field:    "environment",
					Operator: FieldConstraintEqual,
					Value:    "prod",
				}}, Target: TemplateKeyFact(system.Key()),
			},
			{
				Binding: "finding",

				JoinConstraints: []JoinConstraintSpec{{
					Field:    "primary-system",
					Operator: FieldConstraintEqual,
					Ref:      FieldRef{Binding: "system", Field: "id"},
				}},
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentFieldExpr{Field: "risk-score"},
						Right:    ConstExpr{Value: 90},
					},
					Call("pn-complex-ok", CurrentFieldExpr{Field: "risk-score"}, CurrentFieldExpr{Field: "age-days"}),
				}, Target: TemplateKeyFact(finding.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "assert-pn-prefilter-alert"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "predicate-complex-residual",
		Conditions: []RuleConditionSpec{
			{
				Binding: "system",

				FieldConstraints: []FieldConstraintSpec{{
					Field:    "environment",
					Operator: FieldConstraintEqual,
					Value:    "prod",
				}}, Target: TemplateKeyFact(system.Key()),
			},
			{
				Binding: "finding",

				JoinConstraints: []JoinConstraintSpec{{
					Field:    "primary-system",
					Operator: FieldConstraintEqual,
					Ref:      FieldRef{Binding: "system", Field: "id"},
				}},
				Predicates: []ExpressionSpec{
					Call("pn-complex-ok", CurrentFieldExpr{Field: "risk-score"}, CurrentFieldExpr{Field: "age-days"}),
				}, Target: TemplateKeyFact(finding.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "assert-pn-residual-alert"}},
	})
	return mustCompileWorkspace(t, workspace)
}

func addPredicateNormalizationAlertAction(t testing.TB, workspace *Workspace, alertTemplateKey TemplateKey, actionName string, ruleName string) {
	t.Helper()
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: actionName,
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: alertTemplateKey,
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "finding", Field: "id"},
				ConstExpr{Value: ruleName},
			},
		},
	})
}

func mustPredicateNormalizationSession(t testing.TB, revision *Ruleset, tc predicateNormalizationBenchmarkCase) *Session {
	t.Helper()
	session, err := NewSession(revision, WithInitialFacts(predicateNormalizationInitialFacts(tc)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func predicateNormalizationInitialFacts(tc predicateNormalizationBenchmarkCase) []SessionInitialFact {
	initials := make([]SessionInitialFact, 0, tc.initialFacts())
	for i := 0; i < tc.systems; i++ {
		systemID := fmt.Sprintf("S-%06d", i)
		findingID := fmt.Sprintf("F-%06d", i)
		systemRisk := int64(95)
		findingRisk := int64(95)
		ageDays := int64(14)
		status := "open"
		band := "high"
		primarySystem := systemID
		secondarySystem := "none"

		switch i % 4 {
		case 1:
			systemRisk = 50
			status = "closed"
			band = "low"
		case 2:
			findingRisk = 80
			primarySystem = fmt.Sprintf("S-miss-%06d", i)
			secondarySystem = systemID
		case 3:
			systemRisk = 50
			ageDays = 3
			status = "pending"
			band = "low"
		}

		initials = append(initials,
			SessionInitialFact{
				TemplateKey: TemplateKey("pn-system"),
				Fields: Fields{
					"id":          newStringValue(systemID),
					"risk":        newIntValue(systemRisk),
					"environment": newStringValue("prod"),
				},
			},
			SessionInitialFact{
				TemplateKey: TemplateKey("pn-finding"),
				Fields: Fields{
					"id":               newStringValue(findingID),
					"primary-system":   newStringValue(primarySystem),
					"secondary-system": newStringValue(secondarySystem),
					"risk-score":       newIntValue(findingRisk),
					"age-days":         newIntValue(ageDays),
					"status":           newStringValue(status),
					"band":             newStringValue(band),
				},
			},
		)
	}
	return initials
}

func (tc predicateNormalizationBenchmarkCase) initialFacts() int { return tc.systems * 2 }
func (tc predicateNormalizationBenchmarkCase) finalFacts() int {
	return tc.initialFacts()
}
func (tc predicateNormalizationBenchmarkCase) firedCount() int {
	mod0 := (tc.systems + 3) / 4
	mod1 := (tc.systems + 2) / 4
	return mod0*3 + mod1*2
}

func assertPredicateNormalizationBenchmarkResult(t testing.TB, session *Session, result RunResult, tc predicateNormalizationBenchmarkCase) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != tc.firedCount() {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, tc.firedCount())
	}
	if got := session.factCount(); got != tc.finalFacts() {
		t.Fatalf("final facts = %d, want %d", got, tc.finalFacts())
	}
}

func collectPredicateNormalizationPropagationCounters(t testing.TB, revision *Ruleset, tc predicateNormalizationBenchmarkCase) propagationCounterSnapshot {
	t.Helper()
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	for _, fact := range predicateNormalizationInitialFacts(tc) {
		if _, err := session.Assert(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("Assert(%s): %v", fact.TemplateKey, err)
		}
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertPredicateNormalizationBenchmarkResult(t, session, result, tc)
	return session.propagationCounterSnapshot()
}
