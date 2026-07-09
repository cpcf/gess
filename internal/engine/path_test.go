package engine

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

			Predicates: []ExpressionSpec{
				HasPath(Path("payload", MapKey("risk"))),
				CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     CurrentPath(Path("payload", MapKey("risk"))),
					Right:    ConstExpr{Value: 90},
				},
			}, Target: DynamicFact("event"),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "guarded-missing-not-equal-does-not-match",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			Predicates: []ExpressionSpec{
				HasPath(Path("payload", MapKey("missing"))),
				CompareExpr{
					Operator: ExpressionCompareNotEqual,
					Left:     CurrentPath(Path("payload", MapKey("missing"))),
					Right:    ConstExpr{Value: "anything"},
				},
			}, Target: DynamicFact("event"),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	ctx := context.Background()
	if _, err := session.assertByName(ctx, "event", mustFields(t, map[string]any{"payload": map[string]any{"risk": 95}})); err != nil {
		t.Fatalf("Assert matching event: %v", err)
	}
	if _, err := session.assertByName(ctx, "event", mustFields(t, map[string]any{"payload": map[string]any{"risk": 70}})); err != nil {
		t.Fatalf("Assert low-risk event: %v", err)
	}
	if _, err := session.assertByName(ctx, "event", mustFields(t, map[string]any{"payload": map[string]any{"status": "missing-risk"}})); err != nil {
		t.Fatalf("Assert missing-risk event: %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fired = %d, want 1", result.Fired)
	}
	counters := session.propagationCounterSnapshot()
	if counters.Totals.NestedPathEvaluations == 0 {
		t.Fatalf("nested path evaluations = 0, want path diagnostics")
	}
	if counters.Totals.NestedPathMisses == 0 {
		t.Fatalf("nested path misses = 0, want missing-path diagnostics")
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
			{Binding: "l", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "r",

				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentPath(Path("meta", MapKey("id"))),
					Right:    BindingPath("l", Path("payload", MapKey("id"))),
				}}, Target: TemplateKeyFact(right.Key()),
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
	if _, err := session.Assert(ctx, left.Key(), mustFields(t, map[string]any{"payload": map[string]any{"id": "a"}})); err != nil {
		t.Fatalf("Assert left: %v", err)
	}
	if _, err := session.Assert(ctx, right.Key(), mustFields(t, map[string]any{"meta": map[string]any{"id": "a"}})); err != nil {
		t.Fatalf("Assert right matching: %v", err)
	}
	if _, err := session.Assert(ctx, right.Key(), mustFields(t, map[string]any{"meta": map[string]any{"id": "b"}})); err != nil {
		t.Fatalf("Assert right nonmatching: %v", err)
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
			Binding: "p",

			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareEqual,
				Left:     CurrentPath(Path("age", MapKey("value"))),
				Right:    ConstExpr{Value: 1},
			}}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	_, err := workspace.Compile(context.Background())
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Compile error = %v, want ErrInvalidPath", err)
	}
}

func TestRejectsAmbiguousFieldAndPathSpecs(t *testing.T) {
	cases := []struct {
		name string
		rule RuleSpec
	}{
		{
			name: "field constraint",
			rule: RuleSpec{
				Name: "ambiguous-field-constraint",
				Conditions: []RuleConditionSpec{{
					Binding: "event",

					FieldConstraints: []FieldConstraintSpec{{
						Field:    "payload",
						Path:     Path("payload", MapKey("risk")),
						Operator: FieldConstraintEqual,
						Value:    90,
					}}, Target: DynamicFact("event"),
				}},
				Actions: []RuleActionSpec{{Name: "mark"}},
			},
		},
		{
			name: "join left",
			rule: RuleSpec{
				Name: "ambiguous-join-left",
				Conditions: []RuleConditionSpec{
					{Binding: "left", Target: DynamicFact("left")},
					{
						Binding: "right",

						JoinConstraints: []JoinConstraintSpec{{
							Field:    "payload",
							Path:     Path("payload", MapKey("id")),
							Operator: FieldConstraintEqual,
							Ref:      FieldRef{Binding: "left", Field: "id"},
						}}, Target: DynamicFact("right"),
					},
				},
				Actions: []RuleActionSpec{{Name: "mark"}},
			},
		},
		{
			name: "join reference",
			rule: RuleSpec{
				Name: "ambiguous-join-ref",
				Conditions: []RuleConditionSpec{
					{Binding: "left", Target: DynamicFact("left")},
					{
						Binding: "right",

						JoinConstraints: []JoinConstraintSpec{{
							Field:    "id",
							Operator: FieldConstraintEqual,
							Ref: FieldRef{
								Binding: "left",
								Field:   "payload",
								Path:    Path("payload", MapKey("id")),
							},
						}}, Target: DynamicFact("right"),
					},
				},
				Actions: []RuleActionSpec{{Name: "mark"}},
			},
		},
		{
			name: "current field expression",
			rule: RuleSpec{
				Name: "ambiguous-current-expression",
				Conditions: []RuleConditionSpec{{
					Binding: "event",

					Predicates: []ExpressionSpec{CompareExpr{
						Operator: ExpressionCompareEqual,
						Left: CurrentFieldExpr{
							Field: "payload",
							Path:  Path("payload", MapKey("risk")),
						},
						Right: ConstExpr{Value: 90},
					}}, Target: DynamicFact("event"),
				}},
				Actions: []RuleActionSpec{{Name: "mark"}},
			},
		},
		{
			name: "binding field expression",
			rule: RuleSpec{
				Name: "ambiguous-binding-expression",
				Conditions: []RuleConditionSpec{
					{Binding: "left", Target: DynamicFact("left")},
					{
						Binding: "right",

						Predicates: []ExpressionSpec{CompareExpr{
							Operator: ExpressionCompareEqual,
							Left:     CurrentFieldExpr{Field: "id"},
							Right: BindingFieldExpr{
								Binding: "left",
								Field:   "payload",
								Path:    Path("payload", MapKey("id")),
							},
						}}, Target: DynamicFact("right"),
					},
				},
				Actions: []RuleActionSpec{{Name: "mark"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, tc.rule)
			_, err := workspace.Compile(context.Background())
			if !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("Compile error = %v, want ErrInvalidPath", err)
			}
		})
	}
}
