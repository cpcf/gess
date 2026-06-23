package gess

import (
	"context"
	"errors"
	"testing"
)

func TestFactSnapshotPathAccessSemantics(t *testing.T) {
	fields := mustFields(t, map[string]any{
		"profile": map[string]any{
			"name": "Ada",
			"tags": []any{"vip", nil},
			"meta": map[string]any{
				"": "empty-key",
			},
		},
		"nil-root": nil,
	})
	fact := FactSnapshot{fields: fields}

	value, ok, err := fact.Path(Path("profile", MapKey("name")))
	if err != nil {
		t.Fatalf("Path(profile.name): %v", err)
	}
	if !ok || !value.Equal(mustValue(t, "Ada")) {
		t.Fatalf("profile.name = (%v, %v), want Ada present", value, ok)
	}

	value, ok, err = fact.Path(Path("profile", MapKey("tags"), ListIndex(1)))
	if err != nil {
		t.Fatalf("Path(profile.tags[1]): %v", err)
	}
	if !ok || value.Kind() != ValueNull {
		t.Fatalf("profile.tags[1] = (%v, %v), want explicit null present", value, ok)
	}

	value, ok, err = fact.Path(Path("profile", MapKey("meta"), MapKey("")))
	if err != nil {
		t.Fatalf("Path(profile.meta.empty-key): %v", err)
	}
	if !ok || !value.Equal(mustValue(t, "empty-key")) {
		t.Fatalf("profile.meta[empty] = (%v, %v), want empty-key present", value, ok)
	}

	if _, ok, err = fact.Path(Path("profile", MapKey("tags"), ListIndex(2))); err != nil || ok {
		t.Fatalf("out-of-range index = ok %v err %v, want missing without error", ok, err)
	}
	if _, ok, err = fact.Path(Path("nil-root", MapKey("child"))); err != nil || ok {
		t.Fatalf("traverse through null = ok %v err %v, want missing without error", ok, err)
	}
	if _, _, err = fact.Path(Path("profile", ListIndex(-1))); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("negative index error = %v, want ErrInvalidPath", err)
	}
}

func TestNestedPathPredicatesMatchDynamicFacts(t *testing.T) {
	workspace := NewWorkspace()
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "high-risk",
		Conditions: []RuleConditionSpec{{
			Binding: "event",
			Name:    "event",
			Predicates: []ExpressionSpec{
				HasPath(Path("payload", MapKey("risk"))),
				CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     CurrentPath(Path("payload", MapKey("risk"))),
					Right:    ConstExpr{Value: 90},
				},
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "missing-not-equal-does-not-match",
		Conditions: []RuleConditionSpec{{
			Binding: "event",
			Name:    "event",
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareNotEqual,
				Left:     CurrentPath(Path("payload", MapKey("missing"))),
				Right:    ConstExpr{Value: "anything"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"payload": map[string]any{"risk": 95}})); err != nil {
		t.Fatalf("Assert matching event: %v", err)
	}
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"payload": map[string]any{"risk": 70}})); err != nil {
		t.Fatalf("Assert low-risk event: %v", err)
	}
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"payload": map[string]any{"status": "missing-risk"}})); err != nil {
		t.Fatalf("Assert missing-risk event: %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fired = %d, want 1", result.Fired)
	}

	summary := revision.reteGraphDebugSummary()
	if len(summary.AlphaNodes) == 0 {
		t.Fatal("expected graph alpha nodes")
	}
	if got := len(summary.AlphaNodes[0].predicates); got == 0 {
		t.Fatalf("nested path predicates were not retained as graph alpha residuals")
	}
}

func TestNestedPathBetaResidualJoin(t *testing.T) {
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "payload", Kind: ValueMap, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "meta", Kind: ValueMap, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "nested-join",
		Conditions: []RuleConditionSpec{
			{Binding: "l", TemplateKey: left.Key()},
			{
				Binding:     "r",
				TemplateKey: right.Key(),
				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentPath(Path("meta", MapKey("id"))),
					Right:    BindingPath("l", Path("payload", MapKey("id"))),
				}},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	if _, err := session.AssertTemplate(ctx, left.Key(), mustFields(t, map[string]any{"payload": map[string]any{"id": "a"}})); err != nil {
		t.Fatalf("AssertTemplate left: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, right.Key(), mustFields(t, map[string]any{"meta": map[string]any{"id": "a"}})); err != nil {
		t.Fatalf("AssertTemplate right matching: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, right.Key(), mustFields(t, map[string]any{"meta": map[string]any{"id": "b"}})); err != nil {
		t.Fatalf("AssertTemplate right nonmatching: %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fired = %d, want 1", result.Fired)
	}
}

func TestClosedTemplateRejectsImpossibleNestedPath(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "broken",
		Conditions: []RuleConditionSpec{{
			Binding:     "p",
			TemplateKey: person.Key(),
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareEqual,
				Left:     CurrentPath(Path("age", MapKey("value"))),
				Right:    ConstExpr{Value: 1},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	_, err := workspace.Compile(context.Background())
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Compile error = %v, want ErrInvalidPath", err)
	}
}
