package gess

import "testing"

func TestReteGraphSharesEquivalentAlphaAndBetaStages(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "managerDept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})

	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-a",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				},
			},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-b",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				},
			},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-c",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				},
			},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "managerDept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-d",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				},
			},
			{
				Binding:     "d",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "p", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()

	if got, want := len(summary.AlphaNodes), 2; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.BetaNodes), 2; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.TerminalNodes), 4; got != want {
		t.Fatalf("terminal nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.AlphaNodes[0].consumers), 4; got != want {
		t.Fatalf("person alpha consumers = %d, want %d", got, want)
	}
	if got, want := len(summary.AlphaNodes[1].consumers), 4; got != want {
		t.Fatalf("department alpha consumers = %d, want %d", got, want)
	}

	personRoutes := summary.RoutesByTemplateKey[person.Key()]
	if got, want := len(personRoutes), 1; got != want {
		t.Fatalf("person alpha routes = %d, want %d", got, want)
	}
	departmentRoutes := summary.RoutesByTemplateKey[department.Key()]
	if got, want := len(departmentRoutes), 1; got != want {
		t.Fatalf("department alpha routes = %d, want %d", got, want)
	}

	if personRoutes[0] != summary.AlphaNodes[0].id {
		t.Fatalf("person route alpha id = %d, want %d", personRoutes[0], summary.AlphaNodes[0].id)
	}
	if departmentRoutes[0] != summary.AlphaNodes[1].id {
		t.Fatalf("department route alpha id = %d, want %d", departmentRoutes[0], summary.AlphaNodes[1].id)
	}

	if got, want := summary.BetaNodes[0].left, (reteGraphStageRef{kind: reteGraphStageAlpha, id: int(summary.AlphaNodes[0].id)}); got != want {
		t.Fatalf("first beta left input = %#v, want %#v", got, want)
	}
	if got, want := summary.BetaNodes[0].right, (reteGraphStageRef{kind: reteGraphStageAlpha, id: int(summary.AlphaNodes[1].id)}); got != want {
		t.Fatalf("first beta right input = %#v, want %#v", got, want)
	}
	if got, want := summary.BetaNodes[1].left, (reteGraphStageRef{kind: reteGraphStageAlpha, id: int(summary.AlphaNodes[0].id)}); got != want {
		t.Fatalf("second beta left input = %#v, want %#v", got, want)
	}
	if got, want := summary.BetaNodes[1].right, (reteGraphStageRef{kind: reteGraphStageAlpha, id: int(summary.AlphaNodes[1].id)}); got != want {
		t.Fatalf("second beta right input = %#v, want %#v", got, want)
	}
	if summary.TerminalNodes[0].input != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(summary.BetaNodes[0].id)}) {
		t.Fatalf("terminal 1 input = %#v, want shared beta %#v", summary.TerminalNodes[0].input, summary.BetaNodes[0].id)
	}
	if summary.TerminalNodes[1].input != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(summary.BetaNodes[0].id)}) {
		t.Fatalf("terminal 2 input = %#v, want shared beta %#v", summary.TerminalNodes[1].input, summary.BetaNodes[0].id)
	}
	if summary.TerminalNodes[2].input != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(summary.BetaNodes[1].id)}) {
		t.Fatalf("terminal 3 input = %#v, want distinct beta %#v", summary.TerminalNodes[2].input, summary.BetaNodes[1].id)
	}
	if summary.TerminalNodes[3].input != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(summary.BetaNodes[0].id)}) {
		t.Fatalf("terminal 4 input = %#v, want shared beta %#v", summary.TerminalNodes[3].input, summary.BetaNodes[0].id)
	}
}

func TestReteGraphTreatsFlatAndTreeConditionsEquivalently(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	conditions := conditionTreeCompatibilityConditions(person.Key(), department.Key())
	mustAddRule(t, workspace, RuleSpec{
		Name:       "flat",
		Conditions: conditions,
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "tree",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(conditions[0]),
			Match(conditions[1]),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()

	if got, want := len(summary.AlphaNodes), 2; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.BetaNodes), 1; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.TerminalNodes), 2; got != want {
		t.Fatalf("terminal nodes = %d, want %d", got, want)
	}
	if summary.TerminalNodes[0].input != summary.TerminalNodes[1].input {
		t.Fatalf("terminal inputs = %#v and %#v, want shared graph plan", summary.TerminalNodes[0].input, summary.TerminalNodes[1].input)
	}
}

func TestReteGraphMarksNegatedBetaStages(t *testing.T) {
	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "block",
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
		},
	})
	note := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "note",
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "customer-without-block",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "customer", TemplateKey: customer.Key()},
			Not{Condition: Match{
				Binding:     "block",
				TemplateKey: block.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				},
			}},
			Match{
				Binding:     "note",
				TemplateKey: note.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				},
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.AlphaNodes), 3; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.BetaNodes), 2; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	notNode := summary.BetaNodes[0]
	if notNode.kind != reteGraphBetaNodeNot {
		t.Fatalf("first beta kind = %v, want not", notNode.kind)
	}
	if notNode.entry.conditionID != "" {
		t.Fatalf("not beta output entry = %#v, want no appended right binding", notNode.entry)
	}
	if got, want := revision.graph.stageTokenWidth(reteGraphStageRef{kind: reteGraphStageBeta, id: int(notNode.id)}), 1; got != want {
		t.Fatalf("not beta token width = %d, want %d", got, want)
	}
	joinNode := summary.BetaNodes[1]
	if joinNode.kind != reteGraphBetaNodeJoin {
		t.Fatalf("second beta kind = %v, want join", joinNode.kind)
	}
	if got, want := revision.graph.stageTokenWidth(reteGraphStageRef{kind: reteGraphStageBeta, id: int(joinNode.id)}), 2; got != want {
		t.Fatalf("join beta token width = %d, want %d", got, want)
	}
	if summary.TerminalNodes[0].input != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(joinNode.id)}) {
		t.Fatalf("terminal input = %#v, want final join beta %#v", summary.TerminalNodes[0].input, joinNode.id)
	}

	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if runtime.supportsGraphBeta() {
		t.Fatal("runtime supports graph beta for negated graph, want unsupported until negative beta runtime exists")
	}
}

func TestReteGraphSplitsMixedBetaJoinsIntoHashAndResidualGroups(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "mixed-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", TemplateKey: left.Key()},
			{
				Binding:     "right",
				TemplateKey: right.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "left", Field: "score"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 1; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	node := summary.BetaNodes[0]
	if got, want := len(node.joins), 2; got != want {
		t.Fatalf("joins = %d, want %d", got, want)
	}
	if got, want := len(node.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got, want := len(node.residualJoins), 1; got != want {
		t.Fatalf("residual joins = %d, want %d", got, want)
	}
	if node.hashJoins[0].operator != FieldConstraintEqual {
		t.Fatalf("hash join operator = %v, want %v", node.hashJoins[0].operator, FieldConstraintEqual)
	}
	if node.residualJoins[0].operator != FieldConstraintGreaterThan {
		t.Fatalf("residual join operator = %v, want %v", node.residualJoins[0].operator, FieldConstraintGreaterThan)
	}
}

func TestReteGraphIndexesEqualityExpressionPredicates(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "expression-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", TemplateKey: left.Key()},
			{
				Binding:     "right",
				TemplateKey: right.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "group"},
						Right:    BindingFieldExpr{Binding: "left", Field: "group"},
					},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 1; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	node := summary.BetaNodes[0]
	if got := len(node.joins); got != 0 {
		t.Fatalf("declared joins = %d, want 0", got)
	}
	if got, want := len(node.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got, want := len(node.predicates), 1; got != want {
		t.Fatalf("predicates = %d, want %d", got, want)
	}
	hashJoin := node.hashJoins[0]
	if hashJoin.field != "group" || hashJoin.refBinding != "left" || hashJoin.refField != "group" {
		t.Fatalf("hash join = %#v, want right.group == left.group", hashJoin)
	}
	if hashJoin.operator != FieldConstraintEqual {
		t.Fatalf("hash join operator = %v, want %v", hashJoin.operator, FieldConstraintEqual)
	}
	if node.predicates[0].placement != ExpressionPredicatePlacementBetaResidual {
		t.Fatalf("predicate placement = %v, want beta residual", node.predicates[0].placement)
	}
}

func TestReteGraphRoutesTemplateAndNameTargets(t *testing.T) {
	workspace := NewWorkspace()
	eventTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "kind", Kind: ValueString}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "name-target",
		Conditions: []RuleConditionSpec{{Binding: "event", Name: "matched-by-name"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "template-target",
		Conditions: []RuleConditionSpec{{Binding: "event", TemplateKey: eventTemplate.Key()}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()

	if got, want := len(summary.AlphaNodes), 2; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.TerminalNodes), 2; got != want {
		t.Fatalf("terminal nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.RoutesByTemplateKey[eventTemplate.Key()]), 1; got != want {
		t.Fatalf("template routes = %d, want %d", got, want)
	}
	if got, want := len(summary.RoutesByName["matched-by-name"]), 1; got != want {
		t.Fatalf("name routes = %d, want %d", got, want)
	}
}

func TestReteGraphSharesAlphaConstraintsIndependentOfDeclarationOrder(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-active-a",
		Conditions: []RuleConditionSpec{{
			Binding:     "person",
			TemplateKey: person.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-active-b",
		Conditions: []RuleConditionSpec{{
			Binding:     "p",
			TemplateKey: person.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()

	if got, want := len(summary.AlphaNodes), 1; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.TerminalNodes), 2; got != want {
		t.Fatalf("terminal nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.RoutesByTemplateKey[person.Key()]), 1; got != want {
		t.Fatalf("person alpha routes = %d, want %d", got, want)
	}
}

func TestReteGraphAlphaRouteSelectorRequiresTypedScalarField(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "value", Kind: ValueAny, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "numeric-any",
		Conditions: []RuleConditionSpec{{
			Binding:     "item",
			TemplateKey: item.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "value", Operator: FieldConstraintEqual, Value: 1},
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.AlphaNodes), 1; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if summary.AlphaNodes[0].route.enabled {
		t.Fatalf("alpha route selector enabled for any field: %#v", summary.AlphaNodes[0].route)
	}
}
