package engine

import (
	"context"
	"errors"
	"testing"
)

func TestExistsEmitsOneActivationForMultipleContributors(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "hit",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "exists-ready",
		ConditionTree: Exists(And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{
				Binding: "open",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
				}, Target: TemplateKeyFact(item.Key()),
			}),
			Match(RuleConditionSpec{
				Binding: "ready",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "ready"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "open", Field: "group"}},
				}, Target: TemplateKeyFact(item.Key()),
			}),
		}}),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "exists-multiple-session")
	mustAssert(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "status": mustValue(t, "open")})
	mustAssert(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "status": mustValue(t, "ready")})
	mustAssert(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "status": mustValue(t, "ready")})

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("Run fired/action count = %d/%d, want 1/1", result.Fired, fired)
	}
}

func TestForallUsesCounterexamplesAndVacuousTruth(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "hit",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "all-good",
		ConditionTree: Forall(
			Match(RuleConditionSpec{Binding: "item", Target: TemplateKeyFact(item.Key())}),
			Test{Expression: CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     BindingPath("item", Path("score")),
				Right:    ConstExpr{Value: 10},
			}},
		),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "forall-session")

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("empty Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("empty Run fired/action count = %d/%d, want vacuous 1/1", result.Fired, fired)
	}

	mustAssert(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "score": mustValue(t, 12)})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("passing Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("passing Run fired/action count = %d/%d, want unchanged 0/1", result.Fired, fired)
	}

	bad := mustAssert(t, session, item.Key(), Fields{"group": mustValue(t, "b"), "score": mustValue(t, 3)})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("counterexample Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("counterexample Run fired/action count = %d/%d, want 0/1", result.Fired, fired)
	}

	if _, err := session.Retract(context.Background(), bad.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("restored Run: %v", err)
	}
	if result.Fired != 1 || fired != 2 {
		t.Fatalf("restored Run fired/action count = %d/%d, want 1/2", result.Fired, fired)
	}
}

func TestForallRequirementMatchUsesAbsenceCounterexamples(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	order := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order",
		Fields: []FieldSpec{
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "hit",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "all-customers-have-ready-order",
		ConditionTree: Forall(
			Match(RuleConditionSpec{Binding: "customer", Target: TemplateKeyFact(customer.Key())}),
			Match(RuleConditionSpec{
				Binding: "order",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "ready"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(order.Key()),
			}),
		),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "forall-requirement-match-session")
	if got := len(session.propagation.runtime.graph.aggregateNodes); got != 0 {
		t.Fatalf("aggregate node count = %d, want requirement match lowered to negatives", got)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("empty Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("empty Run fired/action count = %d/%d, want vacuous 1/1", result.Fired, fired)
	}
	mustAssert(t, session, customer.Key(), Fields{"id": mustValue(t, "c1")})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("missing requirement Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("missing requirement Run fired/action count = %d/%d, want 0/1", result.Fired, fired)
	}
	mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "other"), "status": mustValue(t, "ready")})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("unrelated requirement Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("unrelated requirement Run fired/action count = %d/%d, want 0/1", result.Fired, fired)
	}
	ready := mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c1"), "status": mustValue(t, "ready")})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("satisfied requirement Run: %v", err)
	}
	if result.Fired != 1 || fired != 2 {
		t.Fatalf("satisfied requirement Run fired/action count = %d/%d, want 1/2", result.Fired, fired)
	}
	if _, err := session.Retract(context.Background(), ready.Fact.ID()); err != nil {
		t.Fatalf("Retract ready order: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("missing again Run: %v", err)
	}
	if result.Fired != 0 || fired != 2 {
		t.Fatalf("missing again Run fired/action count = %d/%d, want 0/2", result.Fired, fired)
	}
}

func TestForallRequirementMatchAndTestUsesQualifiedAbsence(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	order := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order",
		Fields: []FieldSpec{
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "hit",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "all-customers-have-large-order",
		ConditionTree: Forall(
			Match(RuleConditionSpec{Binding: "customer", Target: TemplateKeyFact(customer.Key())}),
			And{Conditions: []ConditionSpec{
				Match(RuleConditionSpec{
					Binding: "order",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "customer-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
					}, Target: TemplateKeyFact(order.Key()),
				}),
				Test{Expression: CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     BindingPath("order", Path("amount")),
					Right:    ConstExpr{Value: 10},
				}},
			}},
		),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "forall-requirement-match-test-session")
	if got := len(session.propagation.runtime.graph.aggregateNodes); got != 0 {
		t.Fatalf("aggregate node count = %d, want qualified requirement lowered to negatives", got)
	}

	mustAssert(t, session, customer.Key(), Fields{"id": mustValue(t, "c1")})
	low := mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c1"), "amount": mustValue(t, 4)})
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("low order Run: %v", err)
	}
	if result.Fired != 0 || fired != 0 {
		t.Fatalf("low order Run fired/action count = %d/%d, want 0/0", result.Fired, fired)
	}
	if _, err := session.Modify(context.Background(), low.Fact.ID(), FactPatch{Set: Fields{"amount": mustValue(t, 12)}}); err != nil {
		t.Fatalf("Modify low order to high: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("high order Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("high order Run fired/action count = %d/%d, want 1/1", result.Fired, fired)
	}
	if _, err := session.Modify(context.Background(), low.Fact.ID(), FactPatch{Set: Fields{"amount": mustValue(t, 5)}}); err != nil {
		t.Fatalf("Modify high order to low: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("low again Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("low again Run fired/action count = %d/%d, want 0/1", result.Fired, fired)
	}
}

func TestExistsContributorReplacementDoesNotChurnWhenTruthUnchanged(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "exists-open",
		ConditionTree: Exists(Match(RuleConditionSpec{
			Binding: "item",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
			}, Target: TemplateKeyFact(item.Key()),
		})),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "exists-replacement-session")
	first := mustAssert(t, session, item.Key(), Fields{"status": mustValue(t, "open")})
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}

	mustAssert(t, session, item.Key(), Fields{"status": mustValue(t, "open")})
	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("replacement Run: %v", err)
	}
	if result.Fired != 0 {
		t.Fatalf("replacement Run fired = %d, want no churn", result.Fired)
	}
}

func TestScopedExistsLoweringTracksContributorsPerOuterToken(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	order := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order",
		Fields: []FieldSpec{
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error {
		fired++
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "customer-with-open-order",
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
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "scoped-exists-prefix-session")
	if got := len(session.propagation.runtime.graph.aggregateNodes); got != 0 {
		t.Fatalf("aggregate node count = %d, want scoped exists lowered to negatives", got)
	}

	mustAssert(t, session, customer.Key(), Fields{"id": mustValue(t, "c1")})
	mustAssert(t, session, customer.Key(), Fields{"id": mustValue(t, "c2")})
	first := mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c1"), "status": mustValue(t, "open")})
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after first c1 order = %d, want %d", got, want)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("first Run fired/action count = %d/%d, want 1/1", result.Fired, fired)
	}

	mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c1"), "status": mustValue(t, "open")})
	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("Retract first c1 order: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("replacement Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("replacement Run fired/action count = %d/%d, want unchanged 0/1", result.Fired, fired)
	}

	mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c2"), "status": mustValue(t, "open")})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("c2 Run: %v", err)
	}
	if result.Fired != 1 || fired != 2 {
		t.Fatalf("c2 Run fired/action count = %d/%d, want 1/2", result.Fired, fired)
	}
}

func TestScopedForallLoweringTracksCounterexamplesPerOuterToken(t *testing.T) {
	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	order := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order",
		Fields: []FieldSpec{
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "customer-all-orders-large",
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
					Left:     BindingPath("order", Path("amount")),
					Right:    ConstExpr{Value: 10},
				}},
			),
		}},
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "scoped-forall-prefix-session")
	if got := len(session.propagation.runtime.graph.aggregateNodes); got != 0 {
		t.Fatalf("aggregate node count = %d, want scoped forall lowered to negatives", got)
	}

	mustAssert(t, session, customer.Key(), Fields{"id": mustValue(t, "c1")})
	mustAssert(t, session, customer.Key(), Fields{"id": mustValue(t, "c2")})
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations with vacuous truth = %d, want %d", got, want)
	}
	bad := mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c1"), "amount": mustValue(t, 3)})
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after c1 counterexample = %d, want %d", got, want)
	}
	if _, err := session.Modify(context.Background(), bad.Fact.ID(), FactPatch{Set: Fields{"amount": mustValue(t, 12)}}); err != nil {
		t.Fatalf("Modify counterexample passing: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations after c1 counterexample repaired = %d, want %d", got, want)
	}
	if _, err := session.Modify(context.Background(), bad.Fact.ID(), FactPatch{Set: Fields{"amount": mustValue(t, 5)}}); err != nil {
		t.Fatalf("Modify counterexample failing: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after c1 counterexample returns = %d, want %d", got, want)
	}
	secondBad := mustAssert(t, session, order.Key(), Fields{"customer-id": mustValue(t, "c1"), "amount": mustValue(t, 4)})
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after second c1 counterexample = %d, want %d", got, want)
	}
	if _, err := session.Retract(context.Background(), bad.Fact.ID()); err != nil {
		t.Fatalf("Retract first counterexample: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after first counterexample retract = %d, want %d", got, want)
	}
	if _, err := session.Retract(context.Background(), secondBad.Fact.ID()); err != nil {
		t.Fatalf("Retract second counterexample: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations after last counterexample retract = %d, want %d", got, want)
	}
}

func TestHigherOrderGraphLowersScopedConditionsToNegativeNodes(t *testing.T) {
	revision := mustCompileHigherOrderRuleset(t)
	summary := revision.reteGraphDebugSummary()
	if got := len(summary.Plan.AggregateNodes); got != 0 {
		t.Fatalf("aggregate node count = %d, want higher-order graph lowering without aggregate nodes", got)
	}
	notNodes := 0
	rightPredicateNotNodes := 0
	for _, node := range summary.BetaNodes {
		if node.kind == reteGraphBetaNodeNot {
			notNodes++
			if len(node.rightPredicates) != 0 {
				rightPredicateNotNodes++
			}
		}
	}
	if notNodes < 3 {
		t.Fatalf("negative beta node count = %d, want at least 3 for scoped exists/forall lowering", notNodes)
	}
	if rightPredicateNotNodes != 1 {
		t.Fatalf("right-predicate negative beta node count = %d, want 1 for direct scoped forall counterexample lowering", rightPredicateNotNodes)
	}
}

func TestHigherOrderGraphKeepsGeneralAggregatesOnAggregateNodes(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:          "count-items",
		ConditionTree: Accumulate(Match(RuleConditionSpec{Binding: "item", Target: TemplateKeyFact(item.Key())}), Count().As("count")),
		Actions:       []RuleActionSpec{{Name: "hit"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.Plan.AggregateNodes), 1; got != want {
		t.Fatalf("aggregate node count = %d, want %d for general count aggregate", got, want)
	}
}

func TestHigherOrderGraphLowersRootConditionsToNegativeNodes(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "root-exists",
		ConditionTree: Exists(Match(RuleConditionSpec{
			Binding: "item", Target: TemplateKeyFact(item.Key()),
		})),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "root-forall",
		ConditionTree: Forall(
			Match(RuleConditionSpec{Binding: "item", Target: TemplateKeyFact(item.Key())}),
			Test{Expression: CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     BindingPath("item", Path("amount")),
				Right:    ConstExpr{Value: 10},
			}},
		),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "root-higher-order-session")
	summary := session.propagation.runtime.graph.debugSummary()
	if got := len(summary.Plan.AggregateNodes); got != 0 {
		t.Fatalf("aggregate node count = %d, want root higher-order lowered without aggregate nodes", got)
	}
	notNodes := 0
	for _, node := range summary.BetaNodes {
		if node.kind == reteGraphBetaNodeNot {
			notNodes++
		}
	}
	if notNodes < 3 {
		t.Fatalf("negative beta node count = %d, want at least 3 for root exists/forall lowering", notNodes)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("empty Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("empty Run fired = %d, want vacuous forall activation", result.Fired)
	}

	bad := mustAssert(t, session, item.Key(), Fields{"amount": mustValue(t, 3)})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("counterexample Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("counterexample Run fired = %d, want exists activation only", result.Fired)
	}
	if _, err := session.Retract(context.Background(), bad.Fact.ID()); err != nil {
		t.Fatalf("Retract failing item: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("restored vacuous Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("restored vacuous Run fired = %d, want forall activation", result.Fired)
	}

	good := mustAssert(t, session, item.Key(), Fields{"amount": mustValue(t, 12)})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("passing item Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("passing item Run fired = %d, want exists activation", result.Fired)
	}
	if _, err := session.Modify(context.Background(), good.Fact.ID(), FactPatch{Set: Fields{"amount": mustValue(t, 2)}}); err != nil {
		t.Fatalf("Modify passing item into counterexample: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("modified counterexample Run: %v", err)
	}
	if result.Fired != 0 {
		t.Fatalf("modified counterexample Run fired = %d, want no activation", result.Fired)
	}
	if _, err := session.Retract(context.Background(), good.Fact.ID()); err != nil {
		t.Fatalf("Retract modified counterexample: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("empty-again Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("empty-again Run fired = %d, want forall activation", result.Fired)
	}
}

func TestHigherOrderRejectsUnsupportedShapes(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{Name: "item"})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "bad-exists",
		ConditionTree: Or{Conditions: []ConditionSpec{
			Exists(Match(RuleConditionSpec{Binding: "item", Target: TemplateKeyFact(item.Key())})),
			Match(RuleConditionSpec{Binding: "other", Target: TemplateKeyFact(item.Key())}),
		}},
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	_, err := workspace.Compile(context.Background())
	if !errors.Is(err, ErrInvalidHigherOrderCondition) {
		t.Fatalf("Compile error = %v, want ErrInvalidHigherOrderCondition", err)
	}
}

func mustAssert(t testing.TB, session *Session, key TemplateKey, fields Fields) AssertResult {
	t.Helper()
	result, err := session.Assert(context.Background(), key, fields)
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	return result
}
