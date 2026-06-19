package gess

import "testing"

func TestReteGraphSharesEquivalentAlphaAndBetaStages(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "managerDept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Closed: true,
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

func TestReteGraphCompilesUnsupportedTargetsWithoutFailing(t *testing.T) {
	workspace := NewWorkspace()
	openTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Closed: false,
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
		Name:       "open-template",
		Conditions: []RuleConditionSpec{{Binding: "event", TemplateKey: openTemplate.Key()}},
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
	if got, want := len(summary.RoutesByTemplateKey[openTemplate.Key()]), 0; got != want {
		t.Fatalf("open template routes = %d, want %d", got, want)
	}
	if _, ok := summary.RoutesByTemplateKey[TemplateKey("matched-by-name")]; ok {
		t.Fatalf("name-target rule should not route by template key: %#v", summary.RoutesByTemplateKey)
	}
	for _, node := range summary.AlphaNodes {
		if len(node.consumers) != 0 {
			t.Fatalf("unsupported alpha node has consumers: %#v", node)
		}
	}
}

func TestReteGraphSharesAlphaConstraintsIndependentOfDeclarationOrder(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
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
