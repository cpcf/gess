package gess

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type unsupportedExpressionSpec struct{}

func (unsupportedExpressionSpec) expressionSpecNode() {}

func TestWorkspaceCompilesRulesIntoImmutableRevision(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:        "classify-adult",
		Description: "labels an adult",
		Tags:        []string{"age", "adult"},
		Salience:    10,
		Conditions: []RuleConditionSpec{
			{Binding: "p", Name: "person"},
		},
		Actions: []RuleActionSpec{
			{Name: "mark"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	rule, ok := revision.Rule("classify-adult")
	if !ok {
		t.Fatal("compiled revision did not contain classify-adult rule")
	}
	if rule.ID().IsZero() {
		t.Fatal("rule ID is zero")
	}
	if rule.RevisionID().IsZero() {
		t.Fatal("rule revision ID is zero")
	}
	if rule.Name() != "classify-adult" {
		t.Fatalf("rule name = %q, want classify-adult", rule.Name())
	}
	if rule.Description() != "labels an adult" {
		t.Fatalf("rule description = %q, want labels an adult", rule.Description())
	}
	if got := rule.DeclarationOrder(); got != 0 {
		t.Fatalf("rule declaration order = %d, want 0", got)
	}
	if got := rule.Salience(); got != 10 {
		t.Fatalf("rule salience = %d, want 10", got)
	}

	conditions := rule.Conditions()
	if len(conditions) != 1 {
		t.Fatalf("compiled conditions = %#v, want 1 condition", conditions)
	}
	if conditions[0].Binding() != "p" || conditions[0].Name() != "person" {
		t.Fatalf("condition = %#v, want binding p and name person", conditions[0])
	}
	if conditions[0].ID().IsZero() {
		t.Fatal("condition ID is zero")
	}
	if got := conditions[0].DeclarationOrder(); got != 0 {
		t.Fatalf("condition declaration order = %d, want 0", got)
	}

	actions := rule.Actions()
	if len(actions) != 1 {
		t.Fatalf("compiled actions = %#v, want 1 action", actions)
	}
	if actions[0].Name() != "mark" {
		t.Fatalf("action name = %q, want mark", actions[0].Name())
	}
	if got := actions[0].DeclarationOrder(); got != 0 {
		t.Fatalf("action declaration order = %d, want 0", got)
	}

	listedRules := revision.Rules()
	if len(listedRules) != 1 || listedRules[0].Name() != "classify-adult" {
		t.Fatalf("rules listing = %#v, want one classify-adult rule", listedRules)
	}
	if listedRules[0].RevisionID() != rule.RevisionID() {
		t.Fatalf("listed rule revision ID = %q, want %q", listedRules[0].RevisionID(), rule.RevisionID())
	}
	if byRevision, ok := revision.RuleByRevisionID(rule.RevisionID()); !ok || byRevision.Name() != rule.Name() {
		t.Fatalf("RuleByRevisionID(%q) = (%#v, %v), want classify-adult", rule.RevisionID(), byRevision, ok)
	}

	registeredActions := revision.Actions()
	if len(registeredActions) != 1 || registeredActions[0].Name() != "mark" {
		t.Fatalf("action listing = %#v, want one mark action", registeredActions)
	}
	if byName, ok := revision.Action("mark"); !ok || byName.Name() != "mark" {
		t.Fatalf("Action(%q) = (%#v, %v), want mark", "mark", byName, ok)
	}

	conditions[0] = RuleCondition{}
	actions[0] = RuleAction{}
	if again, ok := revision.Rule("classify-adult"); !ok {
		t.Fatal("compiled rule missing on second lookup")
	} else if again.Conditions()[0].Binding() != "p" || again.Actions()[0].Name() != "mark" {
		t.Fatalf("compiled rule leaked mutable state after caller mutation: %#v", again)
	}
}

func TestRuleRevisionIdentitySurvivesUnrelatedWorkspaceEdits(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "classify-adult",
		Conditions: []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(classify-adult): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "other",
		Conditions: []RuleConditionSpec{{Binding: "q", Name: "person"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(other): %v", err)
	}

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	rule1, ok := revision1.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 1 missing classify-adult")
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "unused",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(unused): %v", err)
	}

	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 2 missing classify-adult")
	}
	if rule1.RevisionID() != rule2.RevisionID() {
		t.Fatalf("rule revision ID changed across unrelated edit: %q vs %q", rule1.RevisionID(), rule2.RevisionID())
	}
	if got1, got2 := rule1.Conditions()[0].ID(), rule2.Conditions()[0].ID(); got1 != got2 {
		t.Fatalf("condition ID changed across unrelated edit: %q vs %q", got1, got2)
	}
	if revision1.ID() == revision2.ID() {
		t.Fatalf("ruleset ID should change after adding an action, but both revisions used %q", revision1.ID())
	}
}

func TestConditionTreeNormalizesPositiveMatches(t *testing.T) {
	flatRevision := mustCompileConditionTreeCompatibilityRevision(t, false)
	treeRevision := mustCompileConditionTreeCompatibilityRevision(t, true)

	flatRule, ok := flatRevision.Rule("condition-tree-compatible")
	if !ok {
		t.Fatal("flat revision missing rule")
	}
	treeRule, ok := treeRevision.Rule("condition-tree-compatible")
	if !ok {
		t.Fatal("tree revision missing rule")
	}
	if flatRule.RevisionID() != treeRule.RevisionID() {
		t.Fatalf("tree revision ID = %q, want flat-compatible %q", treeRule.RevisionID(), flatRule.RevisionID())
	}

	flatConditions := flatRule.Conditions()
	treeConditions := treeRule.Conditions()
	if len(flatConditions) != 2 || len(treeConditions) != 2 {
		t.Fatalf("conditions = flat %d tree %d, want 2 each", len(flatConditions), len(treeConditions))
	}
	for i := range flatConditions {
		if flatConditions[i].ID() != treeConditions[i].ID() {
			t.Fatalf("condition %d ID = %q, want %q", i, treeConditions[i].ID(), flatConditions[i].ID())
		}
		if treeConditions[i].DeclarationOrder() != i {
			t.Fatalf("condition %d declaration order = %d, want %d", i, treeConditions[i].DeclarationOrder(), i)
		}
	}

	tree := treeRule.ConditionTree()
	if tree.Kind() != ConditionTreeKindAnd {
		t.Fatalf("condition tree kind = %q, want %q", tree.Kind(), ConditionTreeKindAnd)
	}
	children := tree.Children()
	if len(children) != 2 {
		t.Fatalf("condition tree children = %d, want 2", len(children))
	}
	firstMatch, ok := children[0].Match()
	if !ok {
		t.Fatal("first condition tree child is not a match")
	}
	if firstMatch.Binding() != "person" {
		t.Fatalf("first match binding = %q, want person", firstMatch.Binding())
	}

	children[0] = RuleConditionTree{}
	again := treeRule.ConditionTree().Children()
	if got := again[0].Kind(); got != ConditionTreeKindMatch {
		t.Fatalf("condition tree children leaked mutable state: first kind = %q", got)
	}
}

func TestConditionTreeAndFlatRulesProduceEquivalentActivations(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})

	var fired []RuleID
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			fired = append(fired, ctx.RuleID())
			return nil
		},
	})
	conditions := conditionTreeCompatibilityConditions(person.Key(), department.Key())
	mustAddRule(t, workspace, RuleSpec{
		Name:       "flat-rule",
		Conditions: conditions,
		Actions:    []RuleActionSpec{{Name: "record"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "tree-rule",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(conditions[0]),
			Match(conditions[1]),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("condition-tree-activation-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{"name": "Ada", "dept": "engineering"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), department.Key(), mustFields(t, map[string]any{"id": "engineering"})); err != nil {
		t.Fatalf("AssertTemplate(department): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []RuleID{"flat-rule", "tree-rule"}
	if len(fired) != len(want) {
		t.Fatalf("fired rules = %#v, want %#v", fired, want)
	}
	for i := range want {
		if fired[i] != want[i] {
			t.Fatalf("fired rules = %#v, want %#v", fired, want)
		}
	}
}

func TestConditionTreeNotCompilesAsLocalUnsupportedCondition(t *testing.T) {
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
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	note := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "note",
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "customer-without-block",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "customer", TemplateKey: customer.Key()},
			Not{Condition: Match{
				Binding:     "block",
				TemplateKey: block.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
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
	rule, ok := revision.Rule("customer-without-block")
	if !ok {
		t.Fatal("compiled rule missing")
	}
	conditions := rule.Conditions()
	if got, want := len(conditions), 2; got != want {
		t.Fatalf("public conditions = %d, want %d", got, want)
	}
	if conditions[0].Binding() != "customer" {
		t.Fatalf("public condition binding = %q, want customer", conditions[0].Binding())
	}
	if conditions[1].Binding() != "note" {
		t.Fatalf("second public condition binding = %q, want note", conditions[1].Binding())
	}
	tree := rule.ConditionTree()
	children := tree.Children()
	if tree.Kind() != ConditionTreeKindAnd || len(children) != 3 {
		t.Fatalf("condition tree = %q with %d children, want and with 3 children", tree.Kind(), len(children))
	}
	notTree := children[1]
	if notTree.Kind() != ConditionTreeKindNot {
		t.Fatalf("second condition tree child kind = %q, want %q", notTree.Kind(), ConditionTreeKindNot)
	}
	notChildren := notTree.Children()
	if len(notChildren) != 1 {
		t.Fatalf("not children = %d, want 1", len(notChildren))
	}
	notMatch, ok := notChildren[0].Match()
	if !ok {
		t.Fatal("not child is not a match")
	}
	if notMatch.Binding() != "block" {
		t.Fatalf("not match binding = %q, want block", notMatch.Binding())
	}

	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if !runtime.supportsGraphBeta() {
		t.Fatalf("runtime does not support graph beta for not condition: %#v", runtime.plan.unsupported)
	}
	if got, want := len(runtime.plan.unsupported), 0; got != want {
		t.Fatalf("unsupported reasons = %d, want %d: %#v", got, want, runtime.plan.unsupported)
	}
	if err := runtime.validateExecutableGraphBetaRuntime(); err != nil {
		t.Fatalf("validateExecutableGraphBetaRuntime: %v", err)
	}

	session, err := NewSession(
		revision,
		WithSessionID("not-unsupported-session"),
		WithInitialFacts(
			SessionInitialFact{TemplateKey: customer.Key(), Fields: mustFields(t, map[string]any{"id": "c-1"})},
			SessionInitialFact{TemplateKey: note.Key(), Fields: mustFields(t, map[string]any{"customer_id": "c-1"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestConditionTreeNotBindingScopeValidation(t *testing.T) {
	tests := []struct {
		name       string
		rule       func(customerKey, blockKey TemplateKey) RuleSpec
		wantReason string
	}{
		{
			name: "not first",
			rule: func(customerKey, blockKey TemplateKey) RuleSpec {
				return RuleSpec{
					Name: "broken",
					ConditionTree: And{Conditions: []ConditionSpec{
						Not{Condition: Match{Binding: "block", TemplateKey: blockKey}},
					}},
					Actions: []RuleActionSpec{{Name: "mark"}},
				}
			},
			wantReason: "not condition requires an earlier positive condition",
		},
		{
			name: "later condition references negated binding",
			rule: func(customerKey, blockKey TemplateKey) RuleSpec {
				return RuleSpec{
					Name: "broken",
					ConditionTree: And{Conditions: []ConditionSpec{
						Match{Binding: "customer", TemplateKey: customerKey},
						Not{Condition: Match{Binding: "block", TemplateKey: blockKey}},
						Match{
							Binding:     "later",
							TemplateKey: blockKey,
							JoinConstraints: []JoinConstraintSpec{
								{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "block", Field: "customer_id"}},
							},
						},
					}},
					Actions: []RuleActionSpec{{Name: "mark"}},
				}
			},
			wantReason: "join binding reference must refer to an earlier condition",
		},
		{
			name: "not child is not match",
			rule: func(customerKey, blockKey TemplateKey) RuleSpec {
				return RuleSpec{
					Name: "broken",
					ConditionTree: And{Conditions: []ConditionSpec{
						Match{Binding: "customer", TemplateKey: customerKey},
						Not{Condition: And{Conditions: []ConditionSpec{
							Match{Binding: "block", TemplateKey: blockKey},
						}}},
					}},
					Actions: []RuleActionSpec{{Name: "mark"}},
				}
			},
			wantReason: "not condition currently supports a single match child",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
			mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, tc.rule(customer.Key(), block.Key()))

			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatal("Compile succeeded, want validation failure")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected ValidationError, got %T: %v", err, err)
			}
			if validation.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", validation.Reason, tc.wantReason)
			}
		})
	}
}

func TestConditionTreeOrSingleBranchCompilesForInspection(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error {
		fired++
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "single-branch-or",
		ConditionTree: Or{Conditions: []ConditionSpec{
			And{Conditions: []ConditionSpec{
				Match{Binding: "person", TemplateKey: person.Key()},
				Match{
					Binding:     "department",
					TemplateKey: department.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
					},
				},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("single-branch-or")
	if !ok {
		t.Fatal("compiled revision missing single-branch-or")
	}
	tree := rule.ConditionTree()
	if tree.Kind() != ConditionTreeKindOr {
		t.Fatalf("condition tree kind = %q, want %q", tree.Kind(), ConditionTreeKindOr)
	}
	children := tree.Children()
	if got, want := len(children), 1; got != want {
		t.Fatalf("or children = %d, want %d", got, want)
	}
	if children[0].Kind() != ConditionTreeKindAnd {
		t.Fatalf("or child kind = %q, want %q", children[0].Kind(), ConditionTreeKindAnd)
	}
	children[0] = RuleConditionTree{}
	if got := rule.ConditionTree().Children()[0].Kind(); got != ConditionTreeKindAnd {
		t.Fatalf("condition tree inspection leaked mutable state: child kind = %q", got)
	}

	session, err := NewSession(revision, WithSessionID("single-branch-or-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{"name": "Ada", "dept": "engineering"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), department.Key(), mustFields(t, map[string]any{"id": "engineering"})); err != nil {
		t.Fatalf("AssertTemplate(department): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired = %d, want 1", fired)
	}
}

func TestConditionTreeOrBranchInspectionExpandsSourcePaths(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	marker := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "marker",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "nested-or",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "person", TemplateKey: person.Key()},
			Or{Conditions: []ConditionSpec{
				Match{
					Binding:     "marker",
					TemplateKey: marker.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
					},
				},
				Or{Conditions: []ConditionSpec{
					Match{
						Binding:     "marker",
						TemplateKey: marker.Key(),
						FieldConstraints: []FieldConstraintSpec{
							{Field: "status", Operator: FieldConstraintEqual, Value: "probation"},
						},
					},
					Match{
						Binding:     "marker",
						TemplateKey: marker.Key(),
						FieldConstraints: []FieldConstraintSpec{
							{Field: "status", Operator: FieldConstraintEqual, Value: "contractor"},
						},
					},
				}},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("nested-or")
	if !ok {
		t.Fatal("compiled revision missing nested-or")
	}
	branches := rule.ConditionBranches()
	if got, want := len(branches), 3; got != want {
		t.Fatalf("branch count = %d, want %d", got, want)
	}
	wantPaths := [][][]int{
		{{0}, {1, 0}},
		{{0}, {1, 1, 0}},
		{{0}, {1, 1, 1}},
	}
	for branchIndex, branch := range branches {
		if branch.ID() != branchIndex {
			t.Fatalf("branch %d ID = %d, want %d", branchIndex, branch.ID(), branchIndex)
		}
		conditions := branch.Conditions()
		if got, want := len(conditions), 2; got != want {
			t.Fatalf("branch %d condition count = %d, want %d", branchIndex, got, want)
		}
		for conditionIndex, condition := range conditions {
			if !condition.Visible() || condition.Negated() {
				t.Fatalf("branch %d condition %d visibility/negation = (%v, %v), want visible positive", branchIndex, conditionIndex, condition.Visible(), condition.Negated())
			}
			if got, want := condition.Path(), wantPaths[branchIndex][conditionIndex]; !reflect.DeepEqual(got, want) {
				t.Fatalf("branch %d condition %d path = %#v, want %#v", branchIndex, conditionIndex, got, want)
			}
		}
	}

	branches[0] = RuleConditionBranch{}
	if got := rule.ConditionBranches()[0].ID(); got != 0 {
		t.Fatalf("branch inspection leaked mutable branch slice: ID = %d, want 0", got)
	}
	firstCondition := rule.ConditionBranches()[0].Conditions()[0]
	path := firstCondition.Path()
	path[0] = 99
	if got, want := rule.ConditionBranches()[0].Conditions()[0].Path(), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("branch condition path leaked mutable state: got %#v want %#v", got, want)
	}

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.RuleBranchPlans), 3; got != want {
		t.Fatalf("debug branch plan count = %d, want %d", got, want)
	}
	for branchIndex, plan := range summary.RuleBranchPlans {
		if plan.ruleRevisionID != rule.RevisionID() {
			t.Fatalf("debug branch %d rule revision = %q, want %q", branchIndex, plan.ruleRevisionID, rule.RevisionID())
		}
		if plan.branchID != branchIndex {
			t.Fatalf("debug branch %d ID = %d, want %d", branchIndex, plan.branchID, branchIndex)
		}
		if got, want := len(plan.conditions), 2; got != want {
			t.Fatalf("debug branch %d condition count = %d, want %d", branchIndex, got, want)
		}
		for conditionIndex, condition := range plan.conditions {
			if got, want := condition.Path(), wantPaths[branchIndex][conditionIndex]; !reflect.DeepEqual(got, want) {
				t.Fatalf("debug branch %d condition %d path = %#v, want %#v", branchIndex, conditionIndex, got, want)
			}
		}
	}
}

func TestConditionTreeOrValidation(t *testing.T) {
	tests := []struct {
		name       string
		rule       func(personKey TemplateKey) RuleSpec
		wantReason string
	}{
		{
			name: "empty or",
			rule: func(personKey TemplateKey) RuleSpec {
				return RuleSpec{
					Name:          "broken",
					ConditionTree: Or{},
					Actions:       []RuleActionSpec{{Name: "mark"}},
				}
			},
			wantReason: "or condition requires at least one branch",
		},
		{
			name: "branch-specific binding",
			rule: func(personKey TemplateKey) RuleSpec {
				return RuleSpec{
					Name: "broken",
					ConditionTree: Or{Conditions: []ConditionSpec{
						Match{Binding: "first", TemplateKey: personKey},
						Match{Binding: "second", TemplateKey: personKey},
					}},
					Actions: []RuleActionSpec{{Name: "mark"}},
				}
			},
			wantReason: "or branches must expose compatible bindings",
		},
		{
			name: "or under not",
			rule: func(personKey TemplateKey) RuleSpec {
				return RuleSpec{
					Name: "broken",
					ConditionTree: And{Conditions: []ConditionSpec{
						Match{Binding: "person", TemplateKey: personKey},
						Not{Condition: Or{Conditions: []ConditionSpec{
							Match{Binding: "blocked", TemplateKey: personKey},
						}}},
					}},
					Actions: []RuleActionSpec{{Name: "mark"}},
				}
			},
			wantReason: "or condition is not supported under not",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			person := mustAddTemplate(t, workspace, TemplateSpec{
				Name: "person",
				Fields: []FieldSpec{
					{Name: "name", Kind: ValueString, Required: true},
				},
			})
			mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, tc.rule(person.Key()))

			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatal("Compile succeeded, want validation failure")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected ValidationError, got %T: %v", err, err)
			}
			if validation.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", validation.Reason, tc.wantReason)
			}
		})
	}
}

func TestExpressionPredicatesCompileAndClassify(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})

	alphaPredicate := CompareExpr{
		Operator: ExpressionCompareGreaterOrEqual,
		Left:     CurrentFieldExpr{Field: "age"},
		Right:    ConstExpr{Value: 18},
	}
	betaPredicate := CompareExpr{
		Operator: ExpressionCompareGreaterThan,
		Left:     CurrentFieldExpr{Field: "score"},
		Right:    BindingFieldExpr{Binding: "threshold", Field: "score"},
	}
	mustAddRule(t, workspace, RuleSpec{
		Name: "expression-classification",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "threshold",
				TemplateKey: threshold.Key(),
			},
			{
				Binding:     "candidate",
				TemplateKey: person.Key(),
				Predicates: []ExpressionSpec{
					alphaPredicate,
					betaPredicate,
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("expression-classification")
	if !ok {
		t.Fatal("compiled revision missing expression-classification")
	}
	conditions := rule.Conditions()
	if got, want := len(conditions), 2; got != want {
		t.Fatalf("conditions = %d, want %d", got, want)
	}
	predicates := conditions[1].Predicates()
	if got, want := len(predicates), 2; got != want {
		t.Fatalf("predicates = %d, want %d", got, want)
	}
	if got := predicates[0].Placement(); got != ExpressionPredicatePlacementAlpha {
		t.Fatalf("first predicate placement = %q, want alpha", got)
	}
	if got := predicates[1].Placement(); got != ExpressionPredicatePlacementBetaResidual {
		t.Fatalf("second predicate placement = %q, want beta residual", got)
	}
	if got := predicates[1].DeclarationOrder(); got != 1 {
		t.Fatalf("second predicate order = %d, want 1", got)
	}
	predicates[0] = ExpressionPredicate{}
	if got := rule.Conditions()[1].Predicates()[0].Placement(); got != ExpressionPredicatePlacementAlpha {
		t.Fatalf("predicate inspection leaked mutable state: placement = %q", got)
	}

	compiled := revision.rules["expression-classification"]
	plan := compiled.conditionPlans[1]
	if got, want := len(plan.predicates), 2; got != want {
		t.Fatalf("compiled predicates = %d, want %d", got, want)
	}
	if got := plan.predicates[0].placement; got != ExpressionPredicatePlacementAlpha {
		t.Fatalf("compiled first predicate placement = %q, want alpha", got)
	}
	if got := plan.predicates[1].placement; got != ExpressionPredicatePlacementBetaResidual {
		t.Fatalf("compiled second predicate placement = %q, want beta residual", got)
	}

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.AlphaNodes), 2; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	var alphaConstraints, alphaPredicates, betaPredicates int
	for _, node := range summary.AlphaNodes {
		alphaConstraints += len(node.constraints)
		alphaPredicates += len(node.predicates)
	}
	for _, node := range summary.BetaNodes {
		betaPredicates += len(node.predicates)
	}
	if alphaConstraints != 1 {
		t.Fatalf("alpha constraint count = %d, want 1", alphaConstraints)
	}
	if alphaPredicates != 0 {
		t.Fatalf("alpha expression predicate count = %d, want 0", alphaPredicates)
	}
	if betaPredicates != 1 {
		t.Fatalf("beta residual expression predicate count = %d, want 1", betaPredicates)
	}
}

func TestExpressionPredicatesAffectRuleIdentity(t *testing.T) {
	build := func(value int) *Ruleset {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
		mustAddRule(t, workspace, RuleSpec{
			Name: "age-rule",
			Conditions: []RuleConditionSpec{{
				Binding:     "person",
				TemplateKey: person.Key(),
				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     CurrentFieldExpr{Field: "age"},
					Right:    ConstExpr{Value: value},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		return mustCompileWorkspace(t, workspace)
	}

	rule18, ok := build(18).Rule("age-rule")
	if !ok {
		t.Fatal("first revision missing age-rule")
	}
	rule21, ok := build(21).Rule("age-rule")
	if !ok {
		t.Fatal("second revision missing age-rule")
	}
	if rule18.RevisionID() == rule21.RevisionID() {
		t.Fatalf("rule revision ID did not change after expression edit: %q", rule18.RevisionID())
	}
	if rule18.Conditions()[0].ID() == rule21.Conditions()[0].ID() {
		t.Fatalf("condition ID did not change after expression edit: %q", rule18.Conditions()[0].ID())
	}
}

func TestDisjunctiveLiteralPredicateCompilesToAlphaMembershipConstraint(t *testing.T) {
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	var fired []string
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ctx ActionContext) error {
		fact, ok := ctx.Binding("event")
		if !ok {
			return fmt.Errorf("missing event binding")
		}
		status, ok := fact.Field("status")
		if !ok {
			return fmt.Errorf("missing status")
		}
		text, _ := status.AsString()
		fired = append(fired, text)
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "status-membership",
		Conditions: []RuleConditionSpec{{
			Binding: "event",
			Name:    event.Name(),
			Predicates: []ExpressionSpec{BooleanExpr{
				Operator: ExpressionBoolOr,
				Operands: []ExpressionSpec{
					CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "status"}, Right: ConstExpr{Value: "open"}},
					CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "status"}, Right: ConstExpr{Value: "pending"}},
				},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("status-membership")
	if !ok {
		t.Fatal("compiled revision missing status-membership")
	}
	if _, ok := rule.Conditions()[0].Predicates()[0].Expression().(BooleanExpr); !ok {
		t.Fatalf("public predicate expression type = %T, want BooleanExpr", rule.Conditions()[0].Predicates()[0].Expression())
	}

	summary := revision.reteGraphDebugSummary()
	var membershipConstraints, alphaPredicates int
	for _, node := range summary.AlphaNodes {
		alphaPredicates += len(node.predicates)
		for _, constraint := range node.constraints {
			if constraint.operator == fieldConstraintOpIn {
				membershipConstraints++
				if got, want := len(constraint.values), 2; got != want {
					t.Fatalf("membership values = %d, want %d", got, want)
				}
			}
		}
	}
	if membershipConstraints != 1 {
		t.Fatalf("membership alpha constraints = %d, want 1", membershipConstraints)
	}
	if alphaPredicates != 0 {
		t.Fatalf("alpha expression predicates = %d, want 0", alphaPredicates)
	}

	session := mustSession(t, revision, "disjunctive-literal-membership")
	ctx := context.Background()
	for _, status := range []string{"open", "closed", "pending"} {
		if _, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{"status": status})); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", status, err)
		}
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("fired = %d, want 2", result.Fired)
	}
	slices.Sort(fired)
	if !slices.Equal(fired, []string{"open", "pending"}) {
		t.Fatalf("fired statuses = %#v, want open and pending", fired)
	}
}

func TestDisjunctiveJoinPredicateExpandsToHashJoinBranches(t *testing.T) {
	workspace := NewWorkspace()
	system := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "system",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	finding := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "finding",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "primary-system", Kind: ValueString, Required: true},
			{Name: "secondary-system", Kind: ValueString, Required: true},
		},
	})
	var fired []string
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ctx ActionContext) error {
		fact, ok := ctx.Binding("finding")
		if !ok {
			return fmt.Errorf("missing finding binding")
		}
		id, ok := fact.Field("id")
		if !ok {
			return fmt.Errorf("missing id")
		}
		text, _ := id.AsString()
		fired = append(fired, text)
		return nil
	}})
	predicate := BooleanExpr{
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
	}
	mustAddRule(t, workspace, RuleSpec{
		Name: "system-finding",
		Conditions: []RuleConditionSpec{
			{Binding: "system", TemplateKey: system.Key()},
			{Binding: "finding", TemplateKey: finding.Key(), Predicates: []ExpressionSpec{predicate}},
		},
		Actions: []RuleActionSpec{{Name: "record"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("system-finding")
	if !ok {
		t.Fatal("compiled revision missing system-finding")
	}
	if got := len(rule.ConditionBranches()); got != 1 {
		t.Fatalf("public condition branches = %d, want 1", got)
	}
	if _, ok := rule.Conditions()[1].Predicates()[0].Expression().(BooleanExpr); !ok {
		t.Fatalf("public predicate expression type = %T, want BooleanExpr", rule.Conditions()[1].Predicates()[0].Expression())
	}

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.Plan.Branches), 2; got != want {
		t.Fatalf("graph branches = %d, want %d", got, want)
	}
	var hashJoins, betaPredicates int
	for _, node := range summary.BetaNodes {
		hashJoins += len(node.hashJoins)
		betaPredicates += len(node.predicates)
	}
	if hashJoins != 2 {
		t.Fatalf("hash joins = %d, want 2", hashJoins)
	}
	if betaPredicates != 2 {
		t.Fatalf("beta residual predicates = %d, want 2", betaPredicates)
	}

	session := mustSession(t, revision, "disjunctive-join-branches")
	ctx := context.Background()
	if _, err := session.AssertTemplate(ctx, system.Key(), mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate(system): %v", err)
	}
	for _, row := range []map[string]any{
		{"id": "primary", "primary-system": "s-1", "secondary-system": "none"},
		{"id": "secondary", "primary-system": "none", "secondary-system": "s-1"},
		{"id": "both", "primary-system": "s-1", "secondary-system": "s-1"},
		{"id": "neither", "primary-system": "none", "secondary-system": "none"},
	} {
		if _, err := session.AssertTemplate(ctx, finding.Key(), mustFields(t, row)); err != nil {
			t.Fatalf("AssertTemplate(finding): %v", err)
		}
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 3 {
		t.Fatalf("fired = %d, want 3", result.Fired)
	}
	slices.Sort(fired)
	if !slices.Equal(fired, []string{"both", "primary", "secondary"}) {
		t.Fatalf("fired findings = %#v, want both, primary, secondary", fired)
	}
}

func TestConditionTreeMatchPreservesExpressionPredicates(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	condition := RuleConditionSpec{
		Binding:     "person",
		TemplateKey: person.Key(),
		Predicates: []ExpressionSpec{CompareExpr{
			Operator: ExpressionCompareGreaterOrEqual,
			Left:     CurrentFieldExpr{Field: "age"},
			Right:    ConstExpr{Value: 18},
		}},
	}
	mustAddRule(t, workspace, RuleSpec{
		Name: "tree-expression",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(condition),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("tree-expression")
	if !ok {
		t.Fatal("compiled revision missing tree-expression")
	}
	conditions := rule.Conditions()
	if got, want := len(conditions), 1; got != want {
		t.Fatalf("conditions = %d, want %d", got, want)
	}
	predicates := conditions[0].Predicates()
	if got, want := len(predicates), 1; got != want {
		t.Fatalf("predicates = %d, want %d", got, want)
	}
	if got := predicates[0].Placement(); got != ExpressionPredicatePlacementAlpha {
		t.Fatalf("predicate placement = %q, want alpha", got)
	}
	tree := rule.ConditionTree()
	treeMatch, ok := tree.Children()[0].Match()
	if !ok {
		t.Fatal("condition tree child is not a match")
	}
	if got := treeMatch.Predicates()[0].Placement(); got != ExpressionPredicatePlacementAlpha {
		t.Fatalf("tree match predicate placement = %q, want alpha", got)
	}
}

func TestExpressionPredicateInspectionIsImmutable(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	predicate := BooleanExpr{
		Operator: ExpressionBoolAnd,
		Operands: []ExpressionSpec{
			CurrentFieldExpr{Field: "active"},
			CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     CurrentFieldExpr{Field: "age"},
				Right:    ConstExpr{Value: 18},
			},
		},
	}
	mustAddRule(t, workspace, RuleSpec{
		Name: "immutable-expression",
		Conditions: []RuleConditionSpec{{
			Binding:     "person",
			TemplateKey: person.Key(),
			Predicates:  []ExpressionSpec{predicate},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	predicate.Operands[0] = ConstExpr{Value: true}

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("immutable-expression")
	if !ok {
		t.Fatal("compiled revision missing immutable-expression")
	}
	expression, ok := rule.Conditions()[0].Predicates()[0].Expression().(BooleanExpr)
	if !ok {
		t.Fatalf("predicate expression type = %T, want BooleanExpr", rule.Conditions()[0].Predicates()[0].Expression())
	}
	if _, ok := expression.Operands[0].(CurrentFieldExpr); !ok {
		t.Fatalf("first operand type = %T, want CurrentFieldExpr", expression.Operands[0])
	}
	expression.Operands[0] = ConstExpr{Value: true}
	again := rule.Conditions()[0].Predicates()[0].Expression().(BooleanExpr)
	if _, ok := again.Operands[0].(CurrentFieldExpr); !ok {
		t.Fatalf("predicate expression leaked mutable operands: first operand type = %T", again.Operands[0])
	}
}

func TestExpressionPredicatesSplitConjunctiveCompiledPlan(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "conjunctive-expression",
		Conditions: []RuleConditionSpec{
			{Binding: "left", TemplateKey: left.Key()},
			{
				Binding:     "right",
				TemplateKey: right.Key(),
				Predicates: []ExpressionSpec{BooleanExpr{
					Operator: ExpressionBoolAnd,
					Operands: []ExpressionSpec{
						CompareExpr{
							Operator: ExpressionCompareGreaterOrEqual,
							Left:     CurrentFieldExpr{Field: "score"},
							Right:    ConstExpr{Value: 90},
						},
						CompareExpr{
							Operator: ExpressionCompareEqual,
							Left:     CurrentFieldExpr{Field: "group"},
							Right:    BindingFieldExpr{Binding: "left", Field: "group"},
						},
					},
				}},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("conjunctive-expression")
	if !ok {
		t.Fatal("compiled revision missing conjunctive-expression")
	}
	if got, want := len(rule.Conditions()[1].Predicates()), 1; got != want {
		t.Fatalf("public predicates = %d, want %d", got, want)
	}
	if _, ok := rule.Conditions()[1].Predicates()[0].Expression().(BooleanExpr); !ok {
		t.Fatalf("public predicate expression type = %T, want BooleanExpr", rule.Conditions()[1].Predicates()[0].Expression())
	}
	compiled := revision.rules["conjunctive-expression"]
	plan := compiled.conditionPlans[1]
	if got, want := len(plan.predicates), 2; got != want {
		t.Fatalf("compiled predicates = %d, want %d", got, want)
	}
	if got := plan.predicates[0].placement; got != ExpressionPredicatePlacementAlpha {
		t.Fatalf("compiled first predicate placement = %q, want alpha", got)
	}
	if got := plan.predicates[1].placement; got != ExpressionPredicatePlacementBetaResidual {
		t.Fatalf("compiled second predicate placement = %q, want beta residual", got)
	}
}

func TestExpressionPredicatesInvertGuaranteedNegatedComparisons(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "negated-comparison",
		Conditions: []RuleConditionSpec{{
			Binding:     "person",
			TemplateKey: person.Key(),
			Predicates: []ExpressionSpec{BooleanExpr{
				Operator: ExpressionBoolNot,
				Operands: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     CurrentFieldExpr{Field: "age"},
					Right:    ConstExpr{Value: 18},
				}},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("negated-comparison")
	if !ok {
		t.Fatal("compiled revision missing negated-comparison")
	}
	if _, ok := rule.Conditions()[0].Predicates()[0].Expression().(BooleanExpr); !ok {
		t.Fatalf("public predicate expression type = %T, want BooleanExpr", rule.Conditions()[0].Predicates()[0].Expression())
	}
	compiled := revision.rules["negated-comparison"]
	plan := compiled.conditionPlans[0]
	if got, want := len(plan.predicates), 1; got != want {
		t.Fatalf("compiled predicates = %d, want %d", got, want)
	}
	expression := plan.predicates[0].expression
	if expression.kind != expressionNodeCompare || expression.compareOp != ExpressionCompareLessThan {
		t.Fatalf("compiled expression = (%s, %s), want compare lt", expression.kind, expression.compareOp)
	}
}

func TestWorkspaceCompileRejectsInvalidExpressionPredicates(t *testing.T) {
	for _, tc := range []struct {
		name            string
		conditions      func(person Template) []RuleConditionSpec
		wantReason      string
		wantField       string
		wantCondition   int
		wantPredicate   int
		wantErrContains string
	}{
		{
			name: "future binding",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{
					{
						Binding:     "person",
						TemplateKey: person.Key(),
						Predicates: []ExpressionSpec{CompareExpr{
							Operator: ExpressionCompareEqual,
							Left:     CurrentFieldExpr{Field: "dept"},
							Right:    BindingFieldExpr{Binding: "future", Field: "dept"},
						}},
					},
					{Binding: "future", TemplateKey: person.Key()},
				}
			},
			wantReason:    "binding field expression must refer to an earlier condition",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "unknown binding",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "dept"},
						Right:    BindingFieldExpr{Binding: "missing", Field: "dept"},
					}},
				}}
			},
			wantReason:    "binding field expression must refer to an earlier condition",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "unknown current field",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "missing"},
						Right:    ConstExpr{Value: "engineering"},
					}},
				}}
			},
			wantReason:    "unknown field",
			wantField:     "missing",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "unknown binding field",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{
					{Binding: "left", TemplateKey: person.Key()},
					{
						Binding:     "right",
						TemplateKey: person.Key(),
						Predicates: []ExpressionSpec{CompareExpr{
							Operator: ExpressionCompareEqual,
							Left:     CurrentFieldExpr{Field: "dept"},
							Right:    BindingFieldExpr{Binding: "left", Field: "missing"},
						}},
					},
				}
			},
			wantReason:    "unknown field",
			wantField:     "missing",
			wantCondition: 1,
			wantPredicate: 0,
		},
		{
			name: "missing operand",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "dept"},
					}},
				}}
			},
			wantReason:    "comparison expression requires left and right operands",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "invalid operator",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareUnknown,
						Left:     CurrentFieldExpr{Field: "dept"},
						Right:    ConstExpr{Value: "engineering"},
					}},
				}}
			},
			wantReason:    "invalid expression comparison operator",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "type mismatch",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareGreaterThan,
						Left:     CurrentFieldExpr{Field: "dept"},
						Right:    ConstExpr{Value: 10},
					}},
				}}
			},
			wantReason:    "expression operands have incompatible types",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "unsupported node",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates:  []ExpressionSpec{unsupportedExpressionSpec{}},
				}}
			},
			wantReason:    "unsupported expression node",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "predicate not bool",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates:  []ExpressionSpec{CurrentFieldExpr{Field: "dept"}},
				}}
			},
			wantReason:    "expression predicate must produce a bool",
			wantCondition: 0,
			wantPredicate: 0,
		},
		{
			name: "boolean operand not bool",
			conditions: func(person Template) []RuleConditionSpec {
				return []RuleConditionSpec{{
					Binding:     "person",
					TemplateKey: person.Key(),
					Predicates: []ExpressionSpec{BooleanExpr{
						Operator: ExpressionBoolAnd,
						Operands: []ExpressionSpec{CurrentFieldExpr{Field: "dept"}},
					}},
				}}
			},
			wantReason:    "boolean expression operands must produce bool values",
			wantCondition: 0,
			wantPredicate: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			person := mustAddTemplate(t, workspace, TemplateSpec{
				Name: "person",
				Fields: []FieldSpec{
					{Name: "dept", Kind: ValueString, Required: true},
					{Name: "age", Kind: ValueInt, Required: true},
					{Name: "active", Kind: ValueBool, Required: true},
				},
			})
			mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, RuleSpec{
				Name:       "broken",
				Conditions: tc.conditions(person),
				Actions:    []RuleActionSpec{{Name: "mark"}},
			})

			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatal("Compile should reject invalid expression predicates")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected ValidationError, got %T: %v", err, err)
			}
			if validation.RuleName != "broken" {
				t.Fatalf("rule name = %q, want broken", validation.RuleName)
			}
			if validation.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", validation.Reason, tc.wantReason)
			}
			if validation.FieldName != tc.wantField {
				t.Fatalf("field name = %q, want %q", validation.FieldName, tc.wantField)
			}
			if !validation.HasConditionIndex || validation.ConditionIndex != tc.wantCondition {
				t.Fatalf("condition index = (%v, %d), want (true, %d)", validation.HasConditionIndex, validation.ConditionIndex, tc.wantCondition)
			}
			if !validation.HasPredicateIndex || validation.PredicateIndex != tc.wantPredicate {
				t.Fatalf("predicate index = (%v, %d), want (true, %d)", validation.HasPredicateIndex, validation.PredicateIndex, tc.wantPredicate)
			}
			if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrContains)
			}
		})
	}
}

func TestExpressionPredicatesAreExecutableByGraphRuntime(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-rule",
		Conditions: []RuleConditionSpec{{
			Binding:     "person",
			TemplateKey: person.Key(),
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     CurrentFieldExpr{Field: "age"},
				Right:    ConstExpr{Value: 18},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if !runtime.supportsGraphBeta() {
		t.Fatalf("runtime should support graph beta execution for expression predicates: %#v", runtime.plan.unsupported)
	}
	if got := runtime.plan.stats.unsupportedConditions; got != 0 {
		t.Fatalf("unsupported conditions = %d, want 0", got)
	}
	if err := runtime.validateExecutableGraphBetaRuntime(); err != nil {
		t.Fatalf("validateExecutableGraphBetaRuntime: %v", err)
	}

	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{"age": 20})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations = %d, want 1", got)
	}
}

func TestReplacingRuleBySameNamePreservesIdentity(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}

	baseRule := RuleSpec{
		Name:        "classify-adult",
		Description: "initial",
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}
	if err := workspace.AddRule(baseRule); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	rule1, ok := revision1.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 1 missing classify-adult")
	}

	if err := workspace.ReplaceRule(baseRule); err != nil {
		t.Fatalf("ReplaceRule with identical content: %v", err)
	}
	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 2 missing classify-adult")
	}
	if rule1.ID() != rule2.ID() {
		t.Fatalf("rule ID changed after identical replace: %q vs %q", rule1.ID(), rule2.ID())
	}
	if rule1.RevisionID() != rule2.RevisionID() {
		t.Fatalf("rule revision ID changed after identical replace: %q vs %q", rule1.RevisionID(), rule2.RevisionID())
	}
	if got1, got2 := rule1.Conditions()[0].ID(), rule2.Conditions()[0].ID(); got1 != got2 {
		t.Fatalf("condition ID changed after identical replace: %q vs %q", got1, got2)
	}

	if err := workspace.ReplaceRule(RuleSpec{
		Name:        "classify-adult",
		Description: "updated",
		Salience:    50,
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("ReplaceRule with changed content: %v", err)
	}
	revision3, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 3: %v", err)
	}
	rule3, ok := revision3.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 3 missing classify-adult")
	}
	if rule1.ID() != rule3.ID() {
		t.Fatalf("rule ID changed after semantic edit: %q vs %q", rule1.ID(), rule3.ID())
	}
	if got1, got3 := rule1.Conditions()[0].ID(), rule3.Conditions()[0].ID(); got1 != got3 {
		t.Fatalf("condition ID changed when condition content did not: %q vs %q", got1, got3)
	}
	if rule1.RevisionID() == rule3.RevisionID() {
		t.Fatalf("rule revision ID did not change after semantic edit: %q", rule1.RevisionID())
	}
}

func TestRuleMetadataChangesDoNotChangeRuleRevisionIdentity(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:        "classify",
		Description: "initial",
		Tags:        []string{"old"},
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	rule1, ok := revision1.Rule("classify")
	if !ok {
		t.Fatal("revision 1 missing classify")
	}

	if err := workspace.ReplaceRule(RuleSpec{
		Name:        "classify",
		Description: "updated",
		Tags:        []string{"new"},
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("ReplaceRule: %v", err)
	}

	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("classify")
	if !ok {
		t.Fatal("revision 2 missing classify")
	}
	if rule1.RevisionID() != rule2.RevisionID() {
		t.Fatalf("metadata-only edit changed rule revision ID: %q vs %q", rule1.RevisionID(), rule2.RevisionID())
	}
	if revision1.ID() == revision2.ID() {
		t.Fatalf("ruleset ID did not change after metadata edit: %q", revision1.ID())
	}
}

func TestWorkspaceCompileRejectsDuplicateRules(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "classify-adult",
		Conditions: []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	workspace.rules = append(workspace.rules, workspace.rules[0])
	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile should reject duplicate rule names")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for duplicate rules, got %T: %v", err, err)
	}
	if validation.RuleName != "classify-adult" {
		t.Fatalf("duplicate rule validation name = %q, want classify-adult", validation.RuleName)
	}

	workspace.rules = workspace.rules[:1]
	duplicateID := workspace.rules[0]
	duplicateID.Name = "other"
	workspace.rules = append(workspace.rules, duplicateID)
	_, err = workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile should reject duplicate rule IDs")
	}
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for duplicate rule IDs, got %T: %v", err, err)
	}
	if validation.RuleName != "other" {
		t.Fatalf("duplicate rule-id validation name = %q, want other", validation.RuleName)
	}
}

func TestWorkspaceCompileRejectsInvalidRuleDefinitions(t *testing.T) {
	t.Run("missing conditions", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name:    "broken",
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject a rule without conditions")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
	})

	t.Run("flat conditions and condition tree", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name:          "broken",
			Conditions:    []RuleConditionSpec{{Binding: "p", Name: "person"}},
			ConditionTree: And{Conditions: []ConditionSpec{Match{Binding: "q", Name: "person"}}},
			Actions:       []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject ambiguous condition definitions")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if validation.Reason != "rule cannot define both flat conditions and a condition tree" {
			t.Fatalf("reason = %q, want ambiguous condition definition", validation.Reason)
		}
	})

	t.Run("empty and condition tree", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name:          "broken",
			ConditionTree: And{},
			Actions:       []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject empty and condition trees")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if validation.Reason != "and condition requires at least one child" {
			t.Fatalf("reason = %q, want empty and validation failure", validation.Reason)
		}
	})

	t.Run("duplicate binding", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}); err != nil {
			t.Fatalf("AddTemplate: %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "p", Name: "person"},
				{Binding: "p", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject duplicate bindings")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 1 {
			t.Fatalf("condition index = (%v, %d), want (true, 1)", validation.HasConditionIndex, validation.ConditionIndex)
		}
	})

	t.Run("missing binding", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}); err != nil {
			t.Fatalf("AddTemplate: %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: " ", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject missing bindings")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 0 {
			t.Fatalf("condition index = (%v, %d), want (true, 0)", validation.HasConditionIndex, validation.ConditionIndex)
		}
		if validation.Reason != "condition binding is required" {
			t.Fatalf("reason = %q, want condition binding is required", validation.Reason)
		}
	})

	t.Run("invalid binding name", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}); err != nil {
			t.Fatalf("AddTemplate: %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "1person", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject invalid binding names")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 0 {
			t.Fatalf("condition index = (%v, %d), want (true, 0)", validation.HasConditionIndex, validation.ConditionIndex)
		}
		if validation.Reason != "invalid binding name" {
			t.Fatalf("reason = %q, want invalid binding name", validation.Reason)
		}
	})

	t.Run("invalid condition target", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			condition RuleConditionSpec
		}{
			{
				name:      "missing",
				condition: RuleConditionSpec{Binding: "p"},
			},
			{
				name:      "conflicting",
				condition: RuleConditionSpec{Binding: "p", Name: "person", TemplateKey: TemplateKey("person")},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				workspace := NewWorkspace()
				if err := workspace.AddTemplate(TemplateSpec{
					Name:   "person",
					Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
				}); err != nil {
					t.Fatalf("AddTemplate: %v", err)
				}
				if err := workspace.AddAction(ActionSpec{
					Name: "mark",
					Fn:   func(ActionContext) error { return nil },
				}); err != nil {
					t.Fatalf("AddAction: %v", err)
				}
				if err := workspace.AddRule(RuleSpec{
					Name:       "broken",
					Conditions: []RuleConditionSpec{tc.condition},
					Actions:    []RuleActionSpec{{Name: "mark"}},
				}); err != nil {
					t.Fatalf("AddRule: %v", err)
				}

				_, err := workspace.Compile(context.Background())
				if err == nil {
					t.Fatal("Compile should reject invalid condition targets")
				}
				var validation *ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("expected ValidationError, got %T: %v", err, err)
				}
				if validation.RuleName != "broken" {
					t.Fatalf("rule name = %q, want broken", validation.RuleName)
				}
				if !validation.HasConditionIndex || validation.ConditionIndex != 0 {
					t.Fatalf("condition index = (%v, %d), want (true, 0)", validation.HasConditionIndex, validation.ConditionIndex)
				}
				if validation.Reason != "condition target must be either a name or a template key" {
					t.Fatalf("reason = %q, want target validation failure", validation.Reason)
				}
			})
		}
	})

	t.Run("unknown template key", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "p", TemplateKey: TemplateKey("person:v1")},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject unknown template keys")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 0 {
			t.Fatalf("condition index = (%v, %d), want (true, 0)", validation.HasConditionIndex, validation.ConditionIndex)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}); err != nil {
			t.Fatalf("AddTemplate: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "p", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "missing"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject missing actions")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasActionIndex || validation.ActionIndex != 0 {
			t.Fatalf("action index = (%v, %d), want (true, 0)", validation.HasActionIndex, validation.ActionIndex)
		}
	})
}

func TestZeroValueRuleIdentifiersAreInvalid(t *testing.T) {
	if !RuleID("").IsZero() {
		t.Fatal("empty RuleID should be zero")
	}
	if got := RuleID("").String(); got != "rule:zero" {
		t.Fatalf("zero RuleID string = %q, want rule:zero", got)
	}

	if !RuleRevisionID("").IsZero() {
		t.Fatal("empty RuleRevisionID should be zero")
	}
	if got := RuleRevisionID("").String(); got != "rule-revision:zero" {
		t.Fatalf("zero RuleRevisionID string = %q, want rule-revision:zero", got)
	}

	if !ActivationID("").IsZero() {
		t.Fatal("empty ActivationID should be zero")
	}
	if got := ActivationID("").String(); got != "activation:zero" {
		t.Fatalf("zero ActivationID string = %q, want activation:zero", got)
	}

	if !ConditionID("").IsZero() {
		t.Fatal("empty ConditionID should be zero")
	}
	if got := ConditionID("").String(); got != "condition:zero" {
		t.Fatalf("zero ConditionID string = %q, want condition:zero", got)
	}
}

func TestCompiledConditionScanMatchesFactsDeterministically(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person-by-name",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Name: "person"},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person-by-template",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: personTemplate.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	nameRule := revision.rules["match-person-by-name"]
	namePlan := nameRule.conditionPlans[0]
	if got, want := namePlan.binding, "person"; got != want {
		t.Fatalf("name plan binding = %q, want %q", got, want)
	}
	if got, want := namePlan.bindingSlot, 0; got != want {
		t.Fatalf("name plan binding slot = %d, want %d", got, want)
	}
	if got, want := namePlan.path, []int{0}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("name plan path = %#v, want %#v", got, want)
	}
	if !namePlan.indexable || namePlan.indexKind != conditionIndexName {
		t.Fatalf("name plan index metadata = (%v, %v), want (true, conditionIndexName)", namePlan.indexable, namePlan.indexKind)
	}
	if namePlan.target.kind != conditionTargetName || namePlan.target.name != "person" {
		t.Fatalf("name plan target = %#v, want name target person", namePlan.target)
	}

	templateRule := revision.rules["match-person-by-template"]
	templatePlan := templateRule.conditionPlans[0]
	if templatePlan.target.kind != conditionTargetTemplateKey || templatePlan.target.templateKey != personTemplate.Key() {
		t.Fatalf("template plan target = %#v, want template key %q", templatePlan.target, personTemplate.Key())
	}
	if !templatePlan.indexable || templatePlan.indexKind != conditionIndexTemplateKey {
		t.Fatalf("template plan index metadata = (%v, %v), want (true, conditionIndexTemplateKey)", templatePlan.indexable, templatePlan.indexKind)
	}

	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"kind": "dynamic"})); err != nil {
		t.Fatalf("Assert dynamic person: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.Assert(context.Background(), "other", mustFields(t, map[string]any{"kind": "noise"})); err != nil {
		t.Fatalf("Assert other: %v", err)
	}

	before := mustSnapshot(t, context.Background(), session)

	nameMatches, err := nameRule.scanCondition(context.Background(), before, 0)
	if err != nil {
		t.Fatalf("scanCondition(name): %v", err)
	}
	if got, want := len(nameMatches), 2; got != want {
		t.Fatalf("name matches = %d, want %d", got, want)
	}
	if nameMatches[0].bindingSlot != 0 || nameMatches[1].bindingSlot != 0 {
		t.Fatalf("name match binding slots = %#v, want all zero", nameMatches)
	}
	if nameMatches[0].conditionID != namePlan.id || nameMatches[1].conditionID != namePlan.id {
		t.Fatalf("name match condition IDs = %#v, want %q", nameMatches, namePlan.id)
	}
	if got, want := nameMatches[0].fact.Name(), "person"; got != want {
		t.Fatalf("first name match fact name = %q, want %q", got, want)
	}
	if got, want := nameMatches[1].fact.TemplateKey(), personTemplate.Key(); got != want {
		t.Fatalf("second name match template key = %q, want %q", got, want)
	}

	templateMatches, err := templateRule.scanCondition(context.Background(), before, 0)
	if err != nil {
		t.Fatalf("scanCondition(template): %v", err)
	}
	if got, want := len(templateMatches), 1; got != want {
		t.Fatalf("template matches = %d, want %d", got, want)
	}
	if templateMatches[0].bindingSlot != 0 {
		t.Fatalf("template match binding slot = %d, want 0", templateMatches[0].bindingSlot)
	}
	if templateMatches[0].conditionID != templatePlan.id {
		t.Fatalf("template match condition ID = %q, want %q", templateMatches[0].conditionID, templatePlan.id)
	}
	if got, want := templateMatches[0].fact.TemplateKey(), personTemplate.Key(); got != want {
		t.Fatalf("template match template key = %q, want %q", got, want)
	}

	after := mustSnapshot(t, context.Background(), session)
	if before.String() != after.String() {
		t.Fatalf("snapshot changed after scan: before %q after %q", before, after)
	}
}

func TestRuleRevisionIDIncludesActionFreezeSemantics(t *testing.T) {
	build := func(nonEscaping bool) *Ruleset {
		workspace := NewWorkspace()
		mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name:        "mark",
			NonEscaping: nonEscaping,
			Fn: func(ActionContext) error {
				return nil
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "person-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "person", TemplateKey: TemplateKey("person")},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		return mustCompileWorkspace(t, workspace)
	}

	freezingRule, ok := build(false).Rule("person-rule")
	if !ok {
		t.Fatal("freezing ruleset missing person-rule")
	}
	nonEscapingRule, ok := build(true).Rule("person-rule")
	if !ok {
		t.Fatal("non-escaping ruleset missing person-rule")
	}
	if freezingRule.RevisionID() == nonEscapingRule.RevisionID() {
		t.Fatalf("rule revision ID did not change for action freeze semantics: %q", freezingRule.RevisionID())
	}
}

func mustAddTemplate(t testing.TB, workspace *Workspace, spec TemplateSpec) Template {
	t.Helper()
	if err := workspace.AddTemplate(spec); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	compiled, err := compileTemplateSpec(workspace.templates[len(workspace.templates)-1])
	if err != nil {
		t.Fatalf("compileTemplateSpec: %v", err)
	}
	return compiled
}

func mustAddAction(t testing.TB, workspace *Workspace, spec ActionSpec) {
	t.Helper()
	if err := workspace.AddAction(spec); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
}

func mustAddInternalAction(t testing.TB, workspace *Workspace, spec ActionSpec) {
	t.Helper()
	spec.NonEscaping = true
	if err := workspace.AddAction(spec); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
}

func mustAddRule(t testing.TB, workspace *Workspace, spec RuleSpec) {
	t.Helper()
	if err := workspace.AddRule(spec); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
}

func mustCompileConditionTreeCompatibilityRevision(t testing.TB, tree bool) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
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
	spec := RuleSpec{
		Name:    "condition-tree-compatible",
		Actions: []RuleActionSpec{{Name: "mark"}},
	}
	if tree {
		spec.ConditionTree = And{Conditions: []ConditionSpec{
			Match(conditions[0]),
			Match(conditions[1]),
		}}
	} else {
		spec.Conditions = conditions
	}
	mustAddRule(t, workspace, spec)
	return mustCompileWorkspace(t, workspace)
}

func conditionTreeCompatibilityConditions(personKey, departmentKey TemplateKey) []RuleConditionSpec {
	return []RuleConditionSpec{
		{
			Binding:     "person",
			TemplateKey: personKey,
			FieldConstraints: []FieldConstraintSpec{
				{Field: "dept", Operator: FieldConstraintEqual, Value: "engineering"},
			},
		},
		{
			Binding:     "department",
			TemplateKey: departmentKey,
			JoinConstraints: []JoinConstraintSpec{
				{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
			},
		},
	}
}
