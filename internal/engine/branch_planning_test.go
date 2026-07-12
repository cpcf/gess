package engine

import (
	"reflect"
	"testing"
)

func TestBranchPlanningIRComputesDependenciesAndBarriers(t *testing.T) {
	ir := newBranchPlanningIR(0, []normalizedRuleCondition{
		{
			spec: RuleConditionSpec{
				Binding: "root", Target: DynamicFact("root"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "event",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     BindingFieldExpr{Binding: "root", Field: "group"},
						Right:    ConstExpr{Value: "target"},
					},
				}, Target: DynamicFact("event"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "block",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
				}, Target: DynamicFact("block"),
			},
			negated: true,
		},
		{
			isAggregate: true,
			aggregate: Accumulate(Match{
				Binding: "line",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
				}, Target: DynamicFact("line"),
			}, Count().As("line_count")),
			visible: true,
		},
	})

	assertBranchPlanningNode(t, ir.nodes[0], []string{"root"}, nil, true, branchPlanningBarrierNone)
	assertBranchPlanningNode(t, ir.nodes[1], []string{"event"}, []string{"root"}, true, branchPlanningBarrierNone)
	assertBranchPlanningNode(t, ir.nodes[2], nil, []string{"event"}, false, branchPlanningBarrierNegation)
	assertBranchPlanningNode(t, ir.nodes[3], []string{"line_count"}, []string{"event"}, false, branchPlanningBarrierAggregate)
}

func TestBranchPlanningIRReordersAndCanonicalizesJoins(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		{
			spec: RuleConditionSpec{
				Binding: "event",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
				}, Target: DynamicFact("event"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "root",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Value: "target"},
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "root"}},
				}, Target: DynamicFact("root"),
			},
			visible: true,
		},
	})

	planned := ir.normalizedConditions()
	if got, want := len(planned), 2; got != want {
		t.Fatalf("planned conditions = %d, want %d", got, want)
	}
	if got, want := planned[0].spec.Binding, "root"; got != want {
		t.Fatalf("planned first binding = %q, want %q", got, want)
	}
	if got, want := len(planned[0].spec.JoinConstraints), 0; got != want {
		t.Fatalf("first condition joins = %d, want %d", got, want)
	}
	if got, want := planned[1].spec.Binding, "event"; got != want {
		t.Fatalf("planned second binding = %q, want %q", got, want)
	}
	if got, want := len(planned[1].spec.JoinConstraints), 1; got != want {
		t.Fatalf("second condition joins = %d, want %d", got, want)
	}
	join := planned[1].spec.JoinConstraints[0]
	if pathDisplay(join.Path) != "root" || join.Operator != FieldConstraintEqual || join.Ref.Binding != "root" || pathDisplay(join.Ref.Path) != "id" {
		t.Fatalf("planned join = %#v, want event.root == root.id", join)
	}
}

func TestBranchPlanningIRKeepsStarJoinConnectedToCurrentToken(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		{
			spec: RuleConditionSpec{
				Binding: "root",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Value: "target"},
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				}, Target: DynamicFact("root"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "event",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
				}, Target: DynamicFact("event"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "detail",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "code", Operator: FieldConstraintEqual, Value: "selected"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
				}, Target: DynamicFact("detail"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "tag",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "label", Operator: FieldConstraintEqual, Value: "priority"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
				}, Target: DynamicFact("tag"),
			},
			visible: true,
		},
	})

	planned := ir.normalizedConditions()
	if got, want := conditionBindings(planned), []string{"root", "event", "detail", "tag"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRPrefersStrongerJoinToCurrentToken(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		{
			spec: RuleConditionSpec{
				Binding: "root",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				}, Target: DynamicFact("root"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "event",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
				}, Target: DynamicFact("event"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "grant",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
					{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "region"}},
				}, Target: DynamicFact("grant"),
			},
			visible: true,
		},
	})

	planned := ir.normalizedConditions()
	if got, want := conditionBindings(planned), []string{"root", "grant", "event"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
	if got, want := len(planned[1].spec.JoinConstraints), 2; got != want {
		t.Fatalf("grant joins = %d, want %d", got, want)
	}
	if got, want := len(planned[2].spec.JoinConstraints), 1; got != want {
		t.Fatalf("event joins = %d, want %d", got, want)
	}
}

func TestBranchPlanningIRDefersIndependentPayloadPastConsecutiveNegations(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		branchPlanningMatch("anchor", "anchor"),
		branchPlanningJoinedMatch("payload", "payload", "anchor"),
		branchPlanningNegation("first-blocker", "anchor"),
		branchPlanningNegation("second-blocker", "anchor"),
	})

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"anchor", "first-blocker", "second-blocker", "payload"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRKeepsPayloadBeforeDependentNegation(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		branchPlanningMatch("anchor", "anchor"),
		branchPlanningJoinedMatch("payload", "payload", "anchor"),
		branchPlanningNegation("blocker", "payload"),
	})

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"anchor", "payload", "blocker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRKeepsPayloadConsumedByLaterCondition(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		branchPlanningMatch("anchor", "anchor"),
		branchPlanningJoinedMatch("payload", "payload", "anchor"),
		branchPlanningNegation("blocker", "anchor"),
		branchPlanningJoinedMatch("consumer", "consumer", "payload"),
	})

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"anchor", "payload", "blocker", "consumer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRRetainsPositiveAnchorBeforeNegation(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		branchPlanningMatch("anchor", "anchor"),
		branchPlanningMatch("payload", "payload"),
		branchPlanningNegation("blocker", ""),
	})

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"anchor", "blocker", "payload"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRDoesNotUseInvisiblePositiveAsNegationAnchor(t *testing.T) {
	hidden := branchPlanningMatch("hidden", "hidden")
	hidden.visible = false
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		hidden,
		branchPlanningMatch("payload", "payload"),
		branchPlanningNegation("blocker", ""),
	})

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"hidden", "payload", "blocker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRPreservesDeferredPayloadOrder(t *testing.T) {
	ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
		branchPlanningMatch("anchor", "anchor"),
		branchPlanningJoinedMatch("first-payload", "first-payload", "anchor"),
		branchPlanningJoinedMatch("second-payload", "second-payload", "anchor"),
		branchPlanningNegation("blocker", "anchor"),
	})

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"anchor", "blocker", "first-payload", "second-payload"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned bindings = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningIRDoesNotCrossPinnedConditions(t *testing.T) {
	tests := []struct {
		name   string
		pinned normalizedRuleCondition
	}{
		{name: "test", pinned: normalizedRuleCondition{isTest: true, test: ConstExpr{Value: true}}},
		{name: "aggregate", pinned: normalizedRuleCondition{isAggregate: true, aggregate: Accumulate(Match{Binding: "item", Target: DynamicFact("item")}, Count().As("count")), visible: true}},
		{name: "higher-order", pinned: normalizedRuleCondition{higherOrder: compiledHigherOrderConditionSpec{kind: conditionHigherOrderExists, input: Match{Binding: "item", Target: DynamicFact("item")}}}},
		{name: "query-trigger", pinned: branchPlanningMatch(internalQueryTriggerBinding, "query-trigger")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ir := newReorderedBranchPlanningIR(0, []normalizedRuleCondition{
				branchPlanningMatch("anchor", "anchor"),
				branchPlanningJoinedMatch("payload", "payload", "anchor"),
				tt.pinned,
				branchPlanningNegation("blocker", "anchor"),
			})

			if got := conditionBindings(ir.normalizedConditions()); got[1] != "payload" {
				t.Fatalf("planned bindings = %#v, want payload retained before pinned condition", got)
			}
		})
	}
}

func TestBranchPlanningIRPreservesJoinsWithoutReordering(t *testing.T) {
	ir := newBranchPlanningIR(0, []normalizedRuleCondition{
		{
			spec: RuleConditionSpec{
				Binding: "event", Target: DynamicFact("event"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "root",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "root"}},
				}, Target: DynamicFact("root"),
			},
			visible: true,
		},
	})

	planned := ir.normalizedConditions()
	if got, want := planned[0].spec.Binding, "event"; got != want {
		t.Fatalf("planned first binding = %q, want %q", got, want)
	}
	if got, want := len(planned[0].spec.JoinConstraints), 0; got != want {
		t.Fatalf("first condition joins = %d, want %d", got, want)
	}
	if got, want := planned[1].spec.Binding, "root"; got != want {
		t.Fatalf("planned second binding = %q, want %q", got, want)
	}
	join := planned[1].spec.JoinConstraints[0]
	if pathDisplay(pathOrField(join.Path, join.Field)) != "id" || join.Operator != FieldConstraintEqual || join.Ref.Binding != "event" || pathDisplay(pathOrField(join.Ref.Path, join.Ref.Field)) != "root" {
		t.Fatalf("preserved join = %#v, want root.id == event.root", join)
	}
}

func TestQueryGraphBranchPlanningIRPinsTriggerBeforeReorderedConditions(t *testing.T) {
	ir, ok := newQueryGraphBranchPlanningIR("events", 0, []normalizedRuleCondition{
		{
			spec: RuleConditionSpec{
				Binding: "event",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
				}, Target: DynamicFact("event"),
			},
			visible: true,
		},
		{
			spec: RuleConditionSpec{
				Binding: "root",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Value: "target"},
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "root"}},
				}, Target: DynamicFact("root"),
			},
			visible: true,
		},
	}, nil)
	if !ok {
		t.Fatal("query graph branch planning IR rejected non-aggregate branch")
	}

	planned := ir.normalizedConditions()
	if got, want := len(planned), 3; got != want {
		t.Fatalf("planned conditions = %d, want %d", got, want)
	}
	if got, want := planned[0].spec.Binding, internalQueryTriggerBinding; got != want {
		t.Fatalf("planned first binding = %q, want %q", got, want)
	}
	if got, want := planned[1].spec.Binding, "root"; got != want {
		t.Fatalf("planned second binding = %q, want %q", got, want)
	}
	if got, want := planned[2].spec.Binding, "event"; got != want {
		t.Fatalf("planned third binding = %q, want %q", got, want)
	}
}

func TestQueryGraphBranchPlanningIRLowersTriggerAndParameters(t *testing.T) {
	branch := []normalizedRuleCondition{{
		spec: RuleConditionSpec{
			Binding: "person",

			Predicates: []ExpressionSpec{
				CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: "dept"},
					Right:    ParamExpr{Name: "dept"},
				},
			}, Target: DynamicFact("person"),
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
	if got, want := trigger.Target.Kind(), FactTargetDynamic; got != want {
		t.Fatalf("trigger target kind = %v, want %v", got, want)
	}
	if got, want := trigger.Target.Ref().Name, internalQueryTriggerName("people-by-dept"); got != want {
		t.Fatalf("trigger target name = %q, want %q", got, want)
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
	if pathDisplay(join.Path) != "dept" || join.Operator != FieldConstraintEqual || join.Ref.Binding != internalQueryTriggerBinding || pathDisplay(join.Ref.Path) != "dept" {
		t.Fatalf("lowered join = %#v, want dept == query trigger dept", join)
	}

	conditions[1].spec.Binding = "mutated"
	cloned := ir.normalizedConditions()
	if got, want := cloned[1].spec.Binding, "person"; got != want {
		t.Fatalf("IR condition clone alias = %q, want %q", got, want)
	}
}

func TestQueryGraphBranchPlanningIRAcceptsAggregatesAsBarriers(t *testing.T) {
	ir, ok := newQueryGraphBranchPlanningIR("aggregate-query", 0, []normalizedRuleCondition{{
		isAggregate: true,
		aggregate: Accumulate(Match{
			Binding: "person", Target: DynamicFact("person"),
		}, Count().As("count")),
		visible: true,
	}}, nil)
	if !ok {
		t.Fatal("query graph branch planning IR rejected aggregate branch")
	}
	if got, want := len(ir.nodes), 2; got != want {
		t.Fatalf("node count = %d, want %d", got, want)
	}
	assertBranchPlanningNode(t, ir.nodes[1], []string{"count"}, nil, false, branchPlanningBarrierAggregate)
	conditions := ir.normalizedConditions()
	if !conditions[1].isAggregate {
		t.Fatal("normalized query branch lost aggregate condition")
	}
}

func assertBranchPlanningNode(t *testing.T, node branchPlanningNode, defines, dependsOn []string, movable bool, barrier branchPlanningBarrierKind) {
	t.Helper()
	if !reflect.DeepEqual(node.defines, defines) {
		t.Fatalf("defines = %#v, want %#v", node.defines, defines)
	}
	if !reflect.DeepEqual(node.dependsOn, dependsOn) {
		t.Fatalf("dependsOn = %#v, want %#v", node.dependsOn, dependsOn)
	}
	if node.movable != movable {
		t.Fatalf("movable = %v, want %v", node.movable, movable)
	}
	if node.barrier != barrier {
		t.Fatalf("barrier = %q, want %q", node.barrier, barrier)
	}
}

func conditionBindings(conditions []normalizedRuleCondition) []string {
	out := make([]string, len(conditions))
	for i, condition := range conditions {
		out[i] = condition.spec.Binding
	}
	return out
}

func branchPlanningMatch(binding, target string) normalizedRuleCondition {
	return normalizedRuleCondition{
		spec:    RuleConditionSpec{Binding: binding, Target: DynamicFact(target)},
		visible: true,
	}
}

func branchPlanningJoinedMatch(binding, target, dependency string) normalizedRuleCondition {
	condition := branchPlanningMatch(binding, target)
	condition.spec.JoinConstraints = []JoinConstraintSpec{{
		Field: "key", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: dependency, Field: "key"},
	}}
	return condition
}

func branchPlanningNegation(binding, dependency string) normalizedRuleCondition {
	condition := normalizedRuleCondition{
		spec:    RuleConditionSpec{Binding: binding, Target: DynamicFact(binding)},
		negated: true,
	}
	if dependency != "" {
		condition.spec.JoinConstraints = []JoinConstraintSpec{{
			Field: "key", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: dependency, Field: "key"},
		}}
	}
	return condition
}
