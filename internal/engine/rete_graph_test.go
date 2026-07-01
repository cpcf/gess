package engine

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestReteGraphPlanInspectionExplainsRuleAndQueryShape(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "floor", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "eligible-person",
		Conditions: []RuleConditionSpec{
			{
				Binding: "p",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				}, Target: TemplateKeyFact(person.Key()),
			},
			{
				Binding: "d",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "p", Field: "dept"}},
					{Field: "floor", Operator: FieldConstraintLessOrEqual, Ref: FieldRef{Binding: "p", Field: "score"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "people-by-region",
		Parameters: []QueryParameterSpec{{Name: "region", Kind: ValueString}},
		Conditions: []RuleConditionSpec{
			{
				Binding: "d",

				Predicates: []ExpressionSpec{
					CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "region"}, Right: ParamExpr{Name: "region"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
			{
				Binding: "p",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "dept", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "d", Field: "id"}},
				}, Target: TemplateKeyFact(person.Key()),
			},
		},
		Returns: []QueryReturnSpec{ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"})},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	plan := summary.Plan
	if got, want := len(plan.AlphaNodes), 4; got != want {
		t.Fatalf("plan alpha nodes = %d, want %d", got, want)
	}
	if got, want := len(plan.TerminalNodes), 2; got != want {
		t.Fatalf("plan terminal nodes = %d, want %d", got, want)
	}
	if got, want := len(plan.Branches), 2; got != want {
		t.Fatalf("plan branches = %d, want %d", got, want)
	}
	for _, alpha := range plan.AlphaNodes {
		if alpha.MemoryKind != reteGraphMemoryAlphaFactSet {
			t.Fatalf("alpha %d memory kind = %q, want %q", alpha.ID, alpha.MemoryKind, reteGraphMemoryAlphaFactSet)
		}
	}
	for _, terminal := range plan.TerminalNodes {
		if terminal.MemoryKind != reteGraphMemoryTerminalTokens {
			t.Fatalf("terminal %d memory kind = %q, want %q", terminal.ID, terminal.MemoryKind, reteGraphMemoryTerminalTokens)
		}
		if terminal.TokenWidth == 0 {
			t.Fatalf("terminal %d token width = 0, want positive", terminal.ID)
		}
	}

	var residualFilter reteGraphBetaNodeInspection
	for _, beta := range plan.BetaNodes {
		if beta.MemoryKind != reteGraphMemoryBetaTokenHash {
			t.Fatalf("beta %d memory kind = %q, want %q", beta.ID, beta.MemoryKind, reteGraphMemoryBetaTokenHash)
		}
		if beta.Kind == reteGraphBetaNodeResidualFilter && len(beta.ResidualJoins) == 1 {
			residualFilter = beta
		}
	}
	if residualFilter.ID == 0 {
		t.Fatalf("missing residual filter node with one residual join: %#v", plan.BetaNodes)
	}
	mixedJoin := findPlanInspectionBetaNode(t, plan.BetaNodes, reteGraphBetaNodeID(residualFilter.Left.id))
	if got, want := len(mixedJoin.HashJoins), 1; got != want {
		t.Fatalf("mixed join hash joins = %d, want %d", got, want)
	}
	if got := len(mixedJoin.ResidualJoins); got != 0 {
		t.Fatalf("mixed join residual joins = %d, want 0", got)
	}
	if got, want := mixedJoin.TokenWidth, 2; got != want {
		t.Fatalf("mixed join token width = %d, want %d", got, want)
	}
	if got, want := residualFilter.TokenWidth, 2; got != want {
		t.Fatalf("residual filter token width = %d, want %d", got, want)
	}

	ruleBranch := findPlanInspectionBranch(t, plan.Branches, reteGraphBranchOwnerRule, "eligible-person", "")
	if got, want := len(ruleBranch.AuthoredOrder), 2; got != want {
		t.Fatalf("rule authored conditions = %d, want %d", got, want)
	}
	if got, want := len(ruleBranch.PlannedOrder), 2; got != want {
		t.Fatalf("rule planned conditions = %d, want %d", got, want)
	}
	if got, want := ruleBranch.AuthoredOrder[0].Binding, "p"; got != want {
		t.Fatalf("rule authored first binding = %q, want %q", got, want)
	}
	if got, want := ruleBranch.PlannedOrder[1].Binding, "d"; got != want {
		t.Fatalf("rule planned second binding = %q, want %q", got, want)
	}
	if ruleBranch.TerminalID == 0 {
		t.Fatalf("rule terminal ID is zero")
	}

	queryBranch := findPlanInspectionBranch(t, plan.Branches, reteGraphBranchOwnerQuery, "", "people-by-region")
	if got, want := len(queryBranch.AuthoredOrder), 2; got != want {
		t.Fatalf("query authored conditions = %d, want %d", got, want)
	}
	if got, want := len(queryBranch.PlannedOrder), 3; got != want {
		t.Fatalf("query planned conditions = %d, want hidden trigger plus authored conditions (%d)", got, want)
	}
	if got, want := queryBranch.PlannedOrder[0].Binding, internalQueryTriggerBinding; got != want {
		t.Fatalf("query planned first binding = %q, want %q", got, want)
	}
	if got, want := queryBranch.PlannedOrder[0].Target.name, internalQueryTriggerName("people-by-region"); got != want {
		t.Fatalf("query trigger target = %q, want %q", got, want)
	}
	if queryBranch.TerminalID == 0 {
		t.Fatalf("query terminal ID is zero")
	}
	if got, want := len(queryBranch.Projections), 1; got != want {
		t.Fatalf("query projections = %d, want %d", got, want)
	}
	if got, want := queryBranch.Projections[0].Kind, reteGraphTerminalProjectionQueryField; got != want {
		t.Fatalf("query projection kind = %q, want %q", got, want)
	}
	if got, want := queryBranch.Projections[0].Alias, "id"; got != want {
		t.Fatalf("query projection alias = %q, want %q", got, want)
	}
	if got, want := queryBranch.Projections[0].BindingSlot, 0; got != want {
		t.Fatalf("query projection binding slot = %d, want %d", got, want)
	}
	if got, want := queryBranch.Projections[0].Field, "id"; got != want {
		t.Fatalf("query projection field = %q, want %q", got, want)
	}

	if len(summary.Plan.Branches[0].AuthoredOrder[0].Path) == 0 {
		t.Fatalf("expected authored condition path for immutability check")
	}
	summary.Plan.Branches[0].AuthoredOrder[0].Path[0] = 99
	queryBranch.Projections[0].Path.Segments[0].Key = "mutated"
	again := revision.reteGraphDebugSummary()
	if got := again.Plan.Branches[0].AuthoredOrder[0].Path[0]; got == 99 {
		t.Fatalf("plan inspection leaked mutable condition path")
	}
	againQueryBranch := findPlanInspectionBranch(t, again.Plan.Branches, reteGraphBranchOwnerQuery, "", "people-by-region")
	if got := againQueryBranch.Projections[0].Path.Segments[0].Key; got == "mutated" {
		t.Fatalf("plan inspection leaked mutable projection path")
	}
}

func findPlanInspectionBranch(t *testing.T, branches []reteGraphBranchInspection, owner reteGraphBranchOwnerKind, ruleName string, queryName string) reteGraphBranchInspection {
	t.Helper()
	for _, branch := range branches {
		if branch.OwnerKind != owner {
			continue
		}
		switch owner {
		case reteGraphBranchOwnerRule:
			if branch.RuleName == ruleName {
				return branch
			}
		case reteGraphBranchOwnerQuery:
			if branch.QueryName == queryName {
				return branch
			}
		}
	}
	t.Fatalf("missing plan branch owner=%q rule=%q query=%q in %#v", owner, ruleName, queryName, branches)
	return reteGraphBranchInspection{}
}

func TestReteGraphCompilesGeneratedAlphaOpsFromStageEdges(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "single-left",
		Conditions: []RuleConditionSpec{{
			Binding: "l",
			Target:  TemplateKeyFact(left.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "joined",
		Conditions: []RuleConditionSpec{
			{
				Binding: "l",
				Target:  TemplateKeyFact(left.Key()),
			},
			{
				Binding: "r",
				JoinConstraints: []JoinConstraintSpec{{
					Field:    "id",
					Operator: FieldConstraintEqual,
					Ref:      FieldRef{Binding: "l", Field: "id"},
				}},
				Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	var leftOps, rightOps []reteGraphGeneratedAlphaOp
	for i := range revision.graph.alphaNodes {
		node := &revision.graph.alphaNodes[i]
		switch node.target.templateKey {
		case left.Key():
			leftOps = node.generatedOps
		case right.Key():
			rightOps = node.generatedOps
		}
	}
	assertGeneratedAlphaOpKinds(t, leftOps, []reteGraphGeneratedAlphaOpKind{
		reteGraphGeneratedAlphaOpTerminal,
		reteGraphGeneratedAlphaOpBetaLeft,
	})
	assertGeneratedAlphaOpKinds(t, rightOps, []reteGraphGeneratedAlphaOpKind{
		reteGraphGeneratedAlphaOpBetaRight,
	})
	assertGeneratedAlphaOpEntry(t, leftOps[0].entry, "l", 0)
	assertGeneratedAlphaOpEntry(t, leftOps[1].entry, "l", 0)
	assertGeneratedAlphaOpEntry(t, leftOps[1].betaEntry, "r", 1)
	assertGeneratedAlphaOpEntry(t, rightOps[0].entry, "r", 1)
	assertGeneratedAlphaOpEntry(t, rightOps[0].betaEntry, "r", 1)
}

func assertGeneratedAlphaOpKinds(t *testing.T, ops []reteGraphGeneratedAlphaOp, want []reteGraphGeneratedAlphaOpKind) {
	t.Helper()
	got := make([]reteGraphGeneratedAlphaOpKind, len(ops))
	for i, op := range ops {
		got[i] = op.kind
	}
	if !slices.Equal(got, want) {
		t.Fatalf("generated alpha op kinds = %v, want %v", got, want)
	}
}

func assertGeneratedAlphaOpEntry(t *testing.T, entry bindingTupleEntry, binding string, bindingSlot int) {
	t.Helper()
	if entry.binding != binding || entry.bindingSlot != bindingSlot || entry.conditionID == "" {
		t.Fatalf("generated alpha op entry = %#v, want binding=%q slot=%d with condition ID", entry, binding, bindingSlot)
	}
}

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
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				}, Target: TemplateKeyFact(person.Key()),
			},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-b",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				}, Target: TemplateKeyFact(person.Key()),
			},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-c",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				}, Target: TemplateKeyFact(person.Key()),
			},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "managerDept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-department-d",
		Conditions: []RuleConditionSpec{
			{
				Binding: "p",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				}, Target: TemplateKeyFact(person.Key()),
			},
			{
				Binding: "d",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "p", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
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
			Match{Binding: "customer", Target: TemplateKeyFact(customer.Key())},
			Not{Condition: Match{
				Binding: "block",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(block.Key()),
			}},
			Match{
				Binding: "note",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(note.Key()),
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
	if !runtime.supportsGraphBeta() {
		t.Fatalf("runtime does not support graph beta for negated graph: %#v", runtime.plan.unsupported)
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
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "left", Field: "score"}},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 2; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	joinNode := summary.BetaNodes[0]
	residualNode := summary.BetaNodes[1]
	if joinNode.kind != reteGraphBetaNodeJoin {
		t.Fatalf("first beta node kind = %v, want join", joinNode.kind)
	}
	if residualNode.kind != reteGraphBetaNodeResidualFilter {
		t.Fatalf("second beta node kind = %v, want residual filter", residualNode.kind)
	}
	if residualNode.left != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(joinNode.id)}) {
		t.Fatalf("residual filter input = %#v, want join node %d", residualNode.left, joinNode.id)
	}
	if got, want := len(joinNode.joins), 1; got != want {
		t.Fatalf("joins = %d, want %d", got, want)
	}
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.residualJoins); got != 0 {
		t.Fatalf("join residual joins = %d, want 0", got)
	}
	if got, want := len(residualNode.residualJoins), 1; got != want {
		t.Fatalf("residual joins = %d, want %d", got, want)
	}
	if joinNode.hashJoins[0].operator != FieldConstraintEqual {
		t.Fatalf("hash join operator = %v, want %v", joinNode.hashJoins[0].operator, FieldConstraintEqual)
	}
	if residualNode.residualJoins[0].operator != FieldConstraintGreaterThan {
		t.Fatalf("residual join operator = %v, want %v", residualNode.residualJoins[0].operator, FieldConstraintGreaterThan)
	}
}

func TestReteGraphPlansCompoundEqualityKeysAndResidualJoins(t *testing.T) {
	revision, thresholdKey, candidateKey := mustCompoundEqualityResidualJoinBenchmarkRuleset(t)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 2; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	joinNode := summary.BetaNodes[0]
	residualNode := summary.BetaNodes[1]
	if joinNode.kind != reteGraphBetaNodeJoin {
		t.Fatalf("first beta node kind = %v, want join", joinNode.kind)
	}
	if residualNode.kind != reteGraphBetaNodeResidualFilter {
		t.Fatalf("second beta node kind = %v, want residual filter", residualNode.kind)
	}
	if got, want := len(joinNode.hashJoins), 2; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.residualJoins); got != 0 {
		t.Fatalf("join residual joins = %d, want 0", got)
	}
	if got, want := len(residualNode.residualJoins), 2; got != want {
		t.Fatalf("residual joins = %d, want %d", got, want)
	}
	if got, want := joinNode.hashJoins[0].access.root, "group"; got != want {
		t.Fatalf("first hash join root = %q, want %q", got, want)
	}
	if got, want := joinNode.hashJoins[1].access.root, "region"; got != want {
		t.Fatalf("second hash join root = %q, want %q", got, want)
	}
	if got, want := residualNode.residualJoins[0].access.display(), `meta."id"`; got != want {
		t.Fatalf("first residual join path = %q, want %q", got, want)
	}
	if got, want := residualNode.residualJoins[1].operator, FieldConstraintGreaterThan; got != want {
		t.Fatalf("second residual join operator = %v, want %v", got, want)
	}

	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if !runtime.supportsGraphBeta() {
		t.Fatalf("runtime does not support graph beta for compound residual joins: %#v", runtime.plan.unsupported)
	}

	const thresholds = 8
	session := mustCompoundEqualityResidualJoinBenchmarkSession(t, revision, thresholdKey, thresholds)
	session.attachPropagationCounters()
	ctx := context.Background()
	_, err = session.AssertTemplate(ctx, candidateKey, mustFields(t, map[string]any{
		"group":  "A",
		"region": "R007",
		"meta":   map[string]any{"id": "T007"},
		"score":  10,
	}))
	if err != nil {
		t.Fatalf("AssertTemplate candidate: %v", err)
	}
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := snapshot.Totals.BetaCandidateRowsScanned, 1; got != want {
		t.Fatalf("beta candidate rows scanned = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaResidualTests, 2; got != want {
		t.Fatalf("beta residual tests = %d, want %d", got, want)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fired = %d, want 1", result.Fired)
	}
}

func TestReteGraphCompoundEqualityJoinOrderProducesEquivalentHashPlan(t *testing.T) {
	forward := mustCompoundEqualityOrderRuleset(t, []JoinConstraintSpec{
		{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
		{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "region"}},
	})
	reversed := mustCompoundEqualityOrderRuleset(t, []JoinConstraintSpec{
		{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "region"}},
		{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
	})
	forwardKeys := compoundHashJoinSortKeys(t, forward)
	reversedKeys := compoundHashJoinSortKeys(t, reversed)
	if !slices.Equal(forwardKeys, reversedKeys) {
		t.Fatalf("hash join keys differ:\nforward=%#v\nreversed=%#v", forwardKeys, reversedKeys)
	}
}

func TestReteGraphPlansMixedDeclaredAndExpressionEqualityHashKeys(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "mixed-equality-keys",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "left", Field: "score"}},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "region"},
						Right:    BindingFieldExpr{Binding: "left", Field: "region"},
					},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 2; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	joinNode := summary.BetaNodes[0]
	residualNode := summary.BetaNodes[1]
	if joinNode.kind != reteGraphBetaNodeJoin {
		t.Fatalf("first beta node kind = %v, want join", joinNode.kind)
	}
	if residualNode.kind != reteGraphBetaNodeResidualFilter {
		t.Fatalf("second beta node kind = %v, want residual filter", residualNode.kind)
	}
	if got, want := len(joinNode.hashJoins), 2; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got, want := len(residualNode.residualJoins), 1; got != want {
		t.Fatalf("residual joins = %d, want %d", got, want)
	}
	if got, want := residualNode.residualJoins[0].operator, FieldConstraintGreaterThan; got != want {
		t.Fatalf("residual join operator = %v, want %v", got, want)
	}
	if got, want := []string{joinNode.hashJoins[0].access.root, joinNode.hashJoins[1].access.root}, []string{"group", "region"}; !slices.Equal(got, want) {
		t.Fatalf("hash join roots = %#v, want %#v", got, want)
	}
}

func TestReteGraphDedupesEquivalentHashJoinExtractors(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "duplicate-equality-keys",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "group"},
						Right:    BindingFieldExpr{Binding: "left", Field: "group"},
					},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want duplicate equality extractor deduped to %d", got, want)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("predicates = %d, want original expression predicate retained for residual validation", got)
	}
}

func TestReteGraphQueryPlansCompoundEqualityHashKeys(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "compound-query",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "region"},
						Right:    BindingFieldExpr{Binding: "left", Field: "region"},
					},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Returns: []QueryReturnSpec{ReturnValue("id", BindingFieldExpr{Binding: "right", Field: "id"})},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)
	node := graphBetaNodeWithHashJoinCount(t, revision, 2)
	if got, want := len(node.hashJoins), 2; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got, want := []string{node.hashJoins[0].access.root, node.hashJoins[1].access.root}, []string{"group", "region"}; !slices.Equal(got, want) {
		t.Fatalf("hash join roots = %#v, want %#v", got, want)
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
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "group"},
						Right:    BindingFieldExpr{Binding: "left", Field: "group"},
					},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.joins), 1; got != want {
		t.Fatalf("join keys = %d, want %d", got, want)
	}
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("residual predicates = %d, want %d", got, want)
	}
	hashJoin := joinNode.hashJoins[0]
	if hashJoin.access.root != "group" || hashJoin.refBinding != "left" || hashJoin.refAccess.root != "group" {
		t.Fatalf("hash join = %#v, want right.group == left.group", hashJoin)
	}
	if hashJoin.operator != FieldConstraintEqual {
		t.Fatalf("hash join operator = %v, want %v", hashJoin.operator, FieldConstraintEqual)
	}
	if residualNode.predicates[0].placement != ExpressionPredicatePlacementBetaResidual {
		t.Fatalf("predicate placement = %v, want beta residual", residualNode.predicates[0].placement)
	}
}

func TestReteGraphRoutesAlphaExpressionPredicateConstraints(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "expression-alpha",
		Conditions: []RuleConditionSpec{{
			Binding: "item",

			Predicates: []ExpressionSpec{
				CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: "status"},
					Right:    ConstExpr{Value: "open"},
				},
			}, Target: TemplateKeyFact(item.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.Plan.AlphaNodes), 1; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	route := summary.Plan.AlphaNodes[0].Route
	if !route.enabled {
		t.Fatal("alpha route is disabled, want expression predicate equality route")
	}
	statusSlot, ok := item.fieldSlot("status")
	if !ok {
		t.Fatal("missing status field slot")
	}
	if route.fieldSlot != statusSlot {
		t.Fatalf("route field slot = %d, want %d", route.fieldSlot, statusSlot)
	}
	if route.value.kind != ValueString || route.value.text != "open" {
		t.Fatalf("route value = %#v, want string open", route.value)
	}
}

func TestReteGraphIndexesEqualityComparatorFunctionPredicates(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:               "same-group",
		Args:               []ValueKind{ValueString, ValueString},
		Return:             ValueBool,
		EqualityComparator: true,
		Func: func(_ context.Context, args []Value) (Value, error) {
			left, _ := args[0].AsString()
			right, _ := args[1].AsString()
			return NewValue(left == right)
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "function-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{
					Call("same-group", CurrentFieldExpr{Field: "group"}, BindingFieldExpr{Binding: "left", Field: "group"}),
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("residual predicates = %d, want %d", got, want)
	}
	hashJoin := joinNode.hashJoins[0]
	if hashJoin.access.root != "group" || hashJoin.refBinding != "left" || hashJoin.refAccess.root != "group" {
		t.Fatalf("hash join = %#v, want right.group == left.group", hashJoin)
	}
}

func TestReteGraphLeavesUncertifiedFunctionPredicatesResidual(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "same-group",
		Args:   []ValueKind{ValueString, ValueString},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			left, _ := args[0].AsString()
			right, _ := args[1].AsString()
			return NewValue(left == right)
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "function-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{
					Call("same-group", CurrentFieldExpr{Field: "group"}, BindingFieldExpr{Binding: "left", Field: "group"}),
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got := len(joinNode.hashJoins); got != 0 {
		t.Fatalf("hash joins = %d, want 0", got)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("residual predicates = %d, want %d", got, want)
	}
}

func TestReteGraphIndexesCertifiedKeyExtractorFunctionPredicates(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:              "fold-key",
		Args:              []ValueKind{ValueString},
		Return:            ValueString,
		IndexKeyExtractor: true,
		Func1: func(_ context.Context, value Value) (Value, error) {
			text, _ := value.AsString()
			return NewValue(strings.ToLower(text))
		},
	})
	var fired []string
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn: func(ctx ActionContext) error {
			fact, ok := ctx.Binding("right")
			if !ok {
				t.Fatal("missing right binding")
			}
			id, _ := fact.Field("id")
			text, _ := id.AsString()
			fired = append(fired, text)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "folded-function-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     Call("fold-key", CurrentFieldExpr{Field: "group"}),
					Right:    Call("fold-key", BindingFieldExpr{Binding: "left", Field: "group"}),
				}}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("residual predicates = %d, want %d", got, want)
	}
	hashJoin := joinNode.hashJoins[0]
	if !hashJoin.hasLeftKeyExpression || !hashJoin.hasRightKeyExpression {
		t.Fatalf("hash join key expressions = (%v, %v), want both present", hashJoin.hasLeftKeyExpression, hashJoin.hasRightKeyExpression)
	}
	if hashJoin.access.root != "group" || hashJoin.refBinding != "left" || hashJoin.refAccess.root != "group" {
		t.Fatalf("hash join = %#v, want right.group extractor == left.group extractor", hashJoin)
	}

	session := mustSession(t, revision, "key-extractor-function-join")
	ctx := context.Background()
	if _, err := session.AssertTemplate(ctx, left.Key(), mustFields(t, map[string]any{"id": "left", "group": "Prod"})); err != nil {
		t.Fatalf("AssertTemplate(left): %v", err)
	}
	for _, row := range []map[string]any{
		{"id": "case-match", "group": "prod"},
		{"id": "miss", "group": "dev"},
	} {
		if _, err := session.AssertTemplate(ctx, right.Key(), mustFields(t, row)); err != nil {
			t.Fatalf("AssertTemplate(right): %v", err)
		}
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}
	if !slices.Equal(fired, []string{"case-match"}) {
		t.Fatalf("fired = %#v, want case-match", fired)
	}
}

func TestReteGraphLeavesUncertifiedKeyExtractorCallsResidual(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "fold-key",
		Args:   []ValueKind{ValueString},
		Return: ValueString,
		Func1: func(_ context.Context, value Value) (Value, error) {
			text, _ := value.AsString()
			return NewValue(strings.ToLower(text))
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "residual-function-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     Call("fold-key", CurrentFieldExpr{Field: "group"}),
					Right:    Call("fold-key", BindingFieldExpr{Binding: "left", Field: "group"}),
				}}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got := len(joinNode.hashJoins); got != 0 {
		t.Fatalf("hash joins = %d, want 0", got)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("residual predicates = %d, want %d", got, want)
	}
}

func TestReteGraphIndexesConjunctivePredicateTerms(t *testing.T) {
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
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "high-score-for-group",
		Args:   []ValueKind{ValueInt, ValueString},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			score, _ := args[0].AsInt64()
			group, _ := args[1].AsString()
			return NewValue(score >= 90 && group != "")
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "conjunctive-predicate-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{
					BooleanExpr{
						Operator: ExpressionBoolAnd,
						Operands: []ExpressionSpec{
							CompareExpr{
								Operator: ExpressionCompareGreaterOrEqual,
								Left:     CurrentFieldExpr{Field: "score"},
								Right:    ConstExpr{Value: 50},
							},
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "group"},
								Right:    BindingFieldExpr{Binding: "left", Field: "group"},
							},
							Call("high-score-for-group", CurrentFieldExpr{Field: "score"}, BindingFieldExpr{Binding: "left", Field: "group"}),
						},
					},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	var alphaConstraints int
	for _, node := range summary.AlphaNodes {
		alphaConstraints += len(node.constraints)
	}
	if alphaConstraints != 1 {
		t.Fatalf("alpha constraints = %d, want 1", alphaConstraints)
	}
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got, want := len(residualNode.predicates), 2; got != want {
		t.Fatalf("beta residual predicates = %d, want %d", got, want)
	}
}

func TestReteGraphIndexesNegatedComparisonPredicates(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "not-closed",
		Conditions: []RuleConditionSpec{{
			Binding: "person",

			Predicates: []ExpressionSpec{BooleanExpr{
				Operator: ExpressionBoolNot,
				Operands: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: "status"},
					Right:    ConstExpr{Value: "closed"},
				}},
			}}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	summary := revision.reteGraphDebugSummary()
	var alphaConstraints, alphaPredicates int
	for _, node := range summary.AlphaNodes {
		alphaConstraints += len(node.constraints)
		alphaPredicates += len(node.predicates)
	}
	if alphaConstraints != 1 {
		t.Fatalf("alpha constraints = %d, want 1", alphaConstraints)
	}
	if alphaPredicates != 0 {
		t.Fatalf("alpha predicates = %d, want 0", alphaPredicates)
	}
}

func TestReteGraphIndexesNegatedNotEqualJoinPredicates(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "not-not-equal-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				Predicates: []ExpressionSpec{BooleanExpr{
					Operator: ExpressionBoolNot,
					Operands: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareNotEqual,
						Left:     CurrentFieldExpr{Field: "group"},
						Right:    BindingFieldExpr{Binding: "left", Field: "group"},
					}},
				}}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got := len(joinNode.predicates); got != 0 {
		t.Fatalf("join predicates = %d, want 0", got)
	}
	if got := len(residualNode.predicates); got != 1 {
		t.Fatalf("beta residual predicates = %d, want 1 semantic predicate", got)
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
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact("matched-by-name")}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "template-target",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(eventTemplate.Key())}},
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
			Binding: "person",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
				{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
			}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-active-b",
		Conditions: []RuleConditionSpec{{
			Binding: "p",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			}, Target: TemplateKeyFact(person.Key()),
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
			Binding: "item",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "value", Operator: FieldConstraintEqual, Value: 1},
			}, Target: TemplateKeyFact(item.Key()),
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

func TestReteGraphGeneratedAlphaMatchCompilesMultipleEqualities(t *testing.T) {
	workspace := NewWorkspace()
	answer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "answer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString},
			{Name: "kind", Kind: ValueString},
			{Name: "value", Kind: ValueString},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "answer-hardware-provided",
		Conditions: []RuleConditionSpec{{
			Binding: "answer",
			Target:  TemplateKeyFact(answer.Key()),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "kind", Operator: FieldConstraintEqual, Value: "hardware"},
				{Field: "value", Operator: FieldConstraintEqual, Value: "provided"},
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	var node *reteGraphAlphaNode
	for _, nodeID := range revision.graph.routesByTemplateKey[answer.Key()] {
		candidate := revision.graph.alphaNode(nodeID)
		if candidate != nil && len(candidate.constraints) == 2 {
			node = candidate
			break
		}
	}
	if node == nil {
		t.Fatal("missing generated alpha node with two constraints")
	}
	if got, want := node.generatedMatch.kind, reteGraphAlphaGeneratedMatchSlotEqual; got != want {
		t.Fatalf("generated match kind = %v, want %v", got, want)
	}
	if got, want := len(node.generatedMatch.equalities), 2; got != want {
		t.Fatalf("generated equality count = %d, want %d", got, want)
	}

	matchingSlots, err := answer.buildValidatedFieldSlots(mustFields(t, map[string]any{
		"id":    "q1",
		"kind":  "hardware",
		"value": "provided",
	}))
	if err != nil {
		t.Fatalf("build matching slots: %v", err)
	}
	matching := &workingFact{name: answer.Name(), templateKey: answer.Key()}
	matching.setFieldSlots(matchingSlots)
	if !node.generatedMatch.matchesWorking(node.target, matching) {
		t.Fatal("generated match rejected matching fact")
	}

	mismatchSlots, err := answer.buildValidatedFieldSlots(mustFields(t, map[string]any{
		"id":    "q1",
		"kind":  "hardware",
		"value": "other",
	}))
	if err != nil {
		t.Fatalf("build mismatch slots: %v", err)
	}
	mismatch := &workingFact{name: answer.Name(), templateKey: answer.Key()}
	mismatch.setFieldSlots(mismatchSlots)
	if node.generatedMatch.matchesWorking(node.target, mismatch) {
		t.Fatal("generated match accepted fact with mismatched second equality")
	}
}

func mustCompoundEqualityOrderRuleset(tb testing.TB, joins []JoinConstraintSpec) *Ruleset {
	tb.Helper()

	workspace := NewWorkspace()
	left := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "compound-order",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				JoinConstraints: joins, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace)
}

func compoundHashJoinSortKeys(tb testing.TB, revision *Ruleset) []string {
	tb.Helper()

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 1; got != want {
		tb.Fatalf("beta nodes = %d, want %d", got, want)
	}
	node := summary.BetaNodes[0]
	if got, want := len(node.hashJoins), 2; got != want {
		tb.Fatalf("hash joins = %d, want %d", got, want)
	}
	out := make([]string, len(node.hashJoins))
	for i, join := range node.hashJoins {
		out[i] = compiledJoinHashKeySortKey(join)
	}
	return out
}

func singleGraphBetaNode(tb testing.TB, revision *Ruleset) reteGraphBetaNode {
	tb.Helper()

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 1; got != want {
		tb.Fatalf("beta nodes = %d, want %d", got, want)
	}
	return summary.BetaNodes[0]
}

func graphBetaNodeWithHashJoinCount(tb testing.TB, revision *Ruleset, count int) reteGraphBetaNode {
	tb.Helper()

	summary := revision.reteGraphDebugSummary()
	for _, node := range summary.BetaNodes {
		if len(node.hashJoins) == count {
			return node
		}
	}
	tb.Fatalf("missing beta node with %d hash joins: %#v", count, summary.BetaNodes)
	return reteGraphBetaNode{}
}

func graphSplitJoinAndResidualFilterNodes(tb testing.TB, revision *Ruleset) (reteGraphBetaNode, reteGraphBetaNode) {
	tb.Helper()

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.BetaNodes), 2; got != want {
		tb.Fatalf("beta nodes = %d, want %d", got, want)
	}
	joinNode := summary.BetaNodes[0]
	residualNode := summary.BetaNodes[1]
	if joinNode.kind != reteGraphBetaNodeJoin {
		tb.Fatalf("first beta node kind = %v, want join", joinNode.kind)
	}
	if residualNode.kind != reteGraphBetaNodeResidualFilter {
		tb.Fatalf("second beta node kind = %v, want residual filter", residualNode.kind)
	}
	if residualNode.left != (reteGraphStageRef{kind: reteGraphStageBeta, id: int(joinNode.id)}) {
		tb.Fatalf("residual filter input = %#v, want join node %d", residualNode.left, joinNode.id)
	}
	return joinNode, residualNode
}

func findPlanInspectionBetaNode(tb testing.TB, nodes []reteGraphBetaNodeInspection, id reteGraphBetaNodeID) reteGraphBetaNodeInspection {
	tb.Helper()

	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	tb.Fatalf("missing beta inspection node %d: %#v", id, nodes)
	return reteGraphBetaNodeInspection{}
}
