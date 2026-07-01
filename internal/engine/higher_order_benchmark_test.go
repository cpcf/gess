package engine

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkHigherOrderRunResult RunResult

type higherOrderBenchmarkCase struct {
	customers         int
	ordersPerCustomer int
}

func TestHigherOrderBenchmarkFixtureMatchesContract(t *testing.T) {
	tc := higherOrderBenchmarkCase{customers: 64, ordersPerCustomer: 4}
	revision := mustCompileHigherOrderRuleset(t)
	session := mustHigherOrderSession(t, revision, tc)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertHigherOrderBenchmarkResult(t, session, result, tc)
}

func BenchmarkGessHigherOrderResetRun(b *testing.B) {
	for _, tc := range higherOrderBenchmarkCases() {
		name := fmt.Sprintf("customers=%d/orders-per-customer=%d/initial-facts=%d/fired=%d", tc.customers, tc.ordersPerCustomer, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileHigherOrderRuleset(b)
			session := mustHigherOrderSession(b, revision, tc)
			b.ReportMetric(float64(tc.initialFacts()), "initial_facts")
			b.ReportMetric(float64(tc.firedCount()), "fired")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				result, err := session.Run(ctx)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				assertHigherOrderBenchmarkResult(b, session, result, tc)
				benchmarkHigherOrderRunResult = result
			}
		})
	}
}

func BenchmarkGessHigherOrderRunOnly(b *testing.B) {
	for _, tc := range higherOrderBenchmarkCases() {
		name := fmt.Sprintf("customers=%d/orders-per-customer=%d/initial-facts=%d/fired=%d", tc.customers, tc.ordersPerCustomer, tc.initialFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileHigherOrderRuleset(b)
			session := mustHigherOrderSession(b, revision, tc)
			b.ReportMetric(float64(tc.initialFacts()), "initial_facts")
			b.ReportMetric(float64(tc.firedCount()), "fired")
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
				assertHigherOrderBenchmarkResult(b, session, result, tc)
				benchmarkHigherOrderRunResult = result
			}
		})
	}
}

func higherOrderBenchmarkCases() []higherOrderBenchmarkCase {
	return []higherOrderBenchmarkCase{
		{customers: 128, ordersPerCustomer: 4},
		{customers: 512, ordersPerCustomer: 4},
		{customers: 2048, ordersPerCustomer: 4},
	}
}

func mustCompileHigherOrderRuleset(t testing.TB) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "ho-customer",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	order := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "ho-order",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	hit := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "ho-hit",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-exists-hit",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: hit.Key(),
			Values: []ExpressionSpec{
				Call("ho-prefix", ConstExpr{Value: "exists-"}, BindingFieldExpr{Binding: "customer", Field: "id"}),
			},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-forall-hit",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: hit.Key(),
			Values: []ExpressionSpec{
				Call("ho-prefix", ConstExpr{Value: "forall-"}, BindingFieldExpr{Binding: "customer", Field: "id"}),
			},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "ho-prefix",
		Args:   []ValueKind{ValueString, ValueString},
		Return: ValueString,
		Func2: func(_ context.Context, prefix Value, id Value) (Value, error) {
			p, _ := prefix.AsString()
			text, _ := id.AsString()
			return NewValue(p + text)
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "higher-order-exists",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{Binding: "customer", Target: TemplateKeyFact(customer.Key())}),
			Exists(Match(RuleConditionSpec{
				Binding: "order",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(order.Key()),
			})),
		}},
		Actions: []RuleActionSpec{{Name: "assert-exists-hit"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "higher-order-forall",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{Binding: "customer", Target: TemplateKeyFact(customer.Key())}),
			Forall(
				Match(RuleConditionSpec{
					Binding: "order",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "customer-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
					}, Target: TemplateKeyFact(order.Key()),
				}),
				Test{Expression: CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     BindingFieldExpr{Binding: "order", Field: "amount"},
					Right:    ConstExpr{Value: 10},
				}},
			),
		}},
		Actions: []RuleActionSpec{{Name: "assert-forall-hit"}},
	})
	return mustCompileWorkspace(t, workspace)
}

func mustHigherOrderSession(t testing.TB, revision *Ruleset, tc higherOrderBenchmarkCase) *Session {
	t.Helper()
	session, err := NewSession(revision, WithResetBeforeSnapshot(false), WithInitialFacts(higherOrderInitialFacts(tc)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func higherOrderInitialFacts(tc higherOrderBenchmarkCase) []SessionInitialFact {
	initials := make([]SessionInitialFact, 0, tc.initialFacts())
	for customerIndex := 0; customerIndex < tc.customers; customerIndex++ {
		customerID := fmt.Sprintf("C-%06d", customerIndex)
		initials = append(initials, SessionInitialFact{
			TemplateKey: TemplateKey("ho-customer"),
			Fields: Fields{
				"id": newStringValue(customerID),
			},
		})
		for orderIndex := 0; orderIndex < tc.ordersPerCustomer; orderIndex++ {
			status := "closed"
			if customerIndex%2 == 0 && orderIndex == 0 {
				status = "open"
			}
			amount := int64(12)
			if customerIndex%4 == 1 && orderIndex == 0 {
				amount = 3
			}
			initials = append(initials, SessionInitialFact{
				TemplateKey: TemplateKey("ho-order"),
				Fields: Fields{
					"id":          newStringValue(fmt.Sprintf("O-%06d-%02d", customerIndex, orderIndex)),
					"customer-id": newStringValue(customerID),
					"status":      newStringValue(status),
					"amount":      newIntValue(amount),
				},
			})
		}
	}
	return initials
}

func (tc higherOrderBenchmarkCase) initialFacts() int {
	return tc.customers * (tc.ordersPerCustomer + 1)
}

func (tc higherOrderBenchmarkCase) firedCount() int {
	return tc.existsHits() + tc.forallHits()
}

func (tc higherOrderBenchmarkCase) existsHits() int {
	return (tc.customers + 1) / 2
}

func (tc higherOrderBenchmarkCase) forallHits() int {
	return tc.customers - tc.customers/4
}

func assertHigherOrderBenchmarkResult(t testing.TB, session *Session, result RunResult, tc higherOrderBenchmarkCase) {
	t.Helper()
	if result.Fired != tc.firedCount() {
		t.Fatalf("Run fired = %d, want %d", result.Fired, tc.firedCount())
	}
	if got, want := session.factCount(), tc.initialFacts(); got != want {
		t.Fatalf("fact count = %d, want %d", got, want)
	}
}
