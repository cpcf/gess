package gess

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
				Binding:     "open",
				TemplateKey: item.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
				},
			}),
			Match(RuleConditionSpec{
				Binding:     "ready",
				TemplateKey: item.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "ready"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "open", Field: "group"}},
				},
			}),
		}}),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "exists-multiple-session")
	mustAssertTemplate(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "status": mustValue(t, "open")})
	mustAssertTemplate(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "status": mustValue(t, "ready")})
	mustAssertTemplate(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "status": mustValue(t, "ready")})

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
			Match(RuleConditionSpec{Binding: "item", TemplateKey: item.Key()}),
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

	mustAssertTemplate(t, session, item.Key(), Fields{"group": mustValue(t, "a"), "score": mustValue(t, 12)})
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("passing Run: %v", err)
	}
	if result.Fired != 0 || fired != 1 {
		t.Fatalf("passing Run fired/action count = %d/%d, want unchanged 0/1", result.Fired, fired)
	}

	bad := mustAssertTemplate(t, session, item.Key(), Fields{"group": mustValue(t, "b"), "score": mustValue(t, 3)})
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
			Binding:     "item",
			TemplateKey: item.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
			},
		})),
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "exists-replacement-session")
	first := mustAssertTemplate(t, session, item.Key(), Fields{"status": mustValue(t, "open")})
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}

	mustAssertTemplate(t, session, item.Key(), Fields{"status": mustValue(t, "open")})
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

func TestHigherOrderRejectsUnsupportedShapes(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{Name: "item"})
	mustAddAction(t, workspace, ActionSpec{Name: "hit", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "bad-exists",
		ConditionTree: Or{Conditions: []ConditionSpec{
			Exists(Match(RuleConditionSpec{Binding: "item", TemplateKey: item.Key()})),
			Match(RuleConditionSpec{Binding: "other", TemplateKey: item.Key()}),
		}},
		Actions: []RuleActionSpec{{Name: "hit"}},
	})
	_, err := workspace.Compile(context.Background())
	if !errors.Is(err, ErrInvalidHigherOrderCondition) {
		t.Fatalf("Compile error = %v, want ErrInvalidHigherOrderCondition", err)
	}
}

func mustAssertTemplate(t testing.TB, session *Session, key TemplateKey, fields Fields) AssertResult {
	t.Helper()
	result, err := session.AssertTemplate(context.Background(), key, fields)
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	return result
}
