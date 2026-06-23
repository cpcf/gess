package gess

import "testing"

func TestQueryGraphBranchPlanningIRLowersTriggerAndParameters(t *testing.T) {
	branch := []normalizedRuleCondition{{
		spec: RuleConditionSpec{
			Binding: "person",
			Name:    "person",
			Predicates: []ExpressionSpec{
				CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: "dept"},
					Right:    ParamExpr{Name: "dept"},
				},
			},
		},
		path:    []int{3},
		visible: true,
	}}

	ir, ok := newQueryGraphBranchPlanningIR("people-by-dept", 2, branch, map[string]ValueKind{"dept": ValueString})
	if !ok {
		t.Fatal("query graph branch planning IR rejected non-aggregate branch")
	}
	if got, want := ir.id, 2; got != want {
		t.Fatalf("branch ID = %d, want %d", got, want)
	}

	conditions := ir.normalizedConditions()
	if got, want := len(conditions), 2; got != want {
		t.Fatalf("condition count = %d, want %d", got, want)
	}
	trigger := conditions[0].spec
	if trigger.Binding != internalQueryTriggerBinding {
		t.Fatalf("trigger binding = %q, want %q", trigger.Binding, internalQueryTriggerBinding)
	}
	if trigger.Name != internalQueryTriggerName("people-by-dept") {
		t.Fatalf("trigger name = %q, want %q", trigger.Name, internalQueryTriggerName("people-by-dept"))
	}

	lowered := conditions[1].spec
	if got, want := lowered.Binding, "person"; got != want {
		t.Fatalf("lowered binding = %q, want %q", got, want)
	}
	if got, want := len(lowered.Predicates), 0; got != want {
		t.Fatalf("lowered predicates = %d, want %d", got, want)
	}
	if got, want := len(lowered.JoinConstraints), 1; got != want {
		t.Fatalf("lowered joins = %d, want %d", got, want)
	}
	join := lowered.JoinConstraints[0]
	if join.Field != "dept" || join.Operator != FieldConstraintEqual || join.Ref.Binding != internalQueryTriggerBinding || join.Ref.Field != "dept" {
		t.Fatalf("lowered join = %#v, want dept == query trigger dept", join)
	}

	conditions[1].spec.Binding = "mutated"
	cloned := ir.normalizedConditions()
	if got, want := cloned[1].spec.Binding, "person"; got != want {
		t.Fatalf("IR condition clone alias = %q, want %q", got, want)
	}
}

func TestQueryGraphBranchPlanningIRRejectsAggregates(t *testing.T) {
	_, ok := newQueryGraphBranchPlanningIR("aggregate-query", 0, []normalizedRuleCondition{{
		isAggregate: true,
		aggregate: Accumulate(Match{
			Binding: "person",
			Name:    "person",
		}, Count().As("count")),
		visible: true,
	}}, nil)
	if ok {
		t.Fatal("query graph branch planning IR accepted aggregate branch")
	}
}
