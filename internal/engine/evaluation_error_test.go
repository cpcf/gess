package engine

import (
	"context"
	"errors"
	"testing"
)

func TestExpressionEvaluationErrorsPropagateFromAssert(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression ExpressionSpec
		fields     map[string]any
	}{
		{
			name: "missing comparison operand",
			expression: CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "value"},
				Right:    ConstExpr{Value: 0},
			},
			fields: map[string]any{"id": "missing"},
		},
		{
			name: "incomparable comparison operands",
			expression: CompareExpr{
				Operator: ExpressionCompareNotEqual,
				Left:     CurrentFieldExpr{Field: "value"},
				Right:    ConstExpr{Value: 0},
			},
			fields: map[string]any{"id": "wrong-kind", "value": "zero"},
		},
		{
			name: "boolean kind mismatch under not",
			expression: BooleanExpr{
				Operator: ExpressionBoolNot,
				Operands: []ExpressionSpec{CurrentFieldExpr{Field: "value"}},
			},
			fields: map[string]any{"id": "not-wrong-kind", "value": "true"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			event := mustAddTemplate(t, workspace, TemplateSpec{
				Name: "event",
				Fields: []FieldSpec{
					{Name: "id", Kind: ValueString, Required: true},
					{Name: "value", Kind: ValueAny},
				},
			})
			mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, RuleSpec{
				Name: "guard",
				Conditions: []RuleConditionSpec{{
					Binding:    "event",
					Predicates: []ExpressionSpec{tc.expression},
					Target:     TemplateKeyFact(event.Key()),
				}},
				Actions: []RuleActionSpec{{Name: "noop"}},
			})
			session := mustSession(t, mustCompileWorkspace(t, workspace), SessionID("evaluation-error-"+tc.name))

			_, err := session.Assert(context.Background(), event.Key(), mustFields(t, tc.fields))
			if !errors.Is(err, ErrMatcher) {
				t.Fatalf("Assert error = %v, want ErrMatcher", err)
			}
		})
	}
}

func TestFieldConstraintEvaluationErrorsPropagateFromAssert(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
	}{
		{name: "missing operand", fields: map[string]any{"id": "missing"}},
		{name: "non-comparable kinds", fields: map[string]any{"id": "wrong-kind", "value": "ten"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			event := mustAddTemplate(t, workspace, TemplateSpec{
				Name: "event",
				Fields: []FieldSpec{
					{Name: "id", Kind: ValueString, Required: true},
					{Name: "value", Kind: ValueAny},
				},
			})
			mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, RuleSpec{
				Name: "guard",
				Conditions: []RuleConditionSpec{{
					Binding: "event",
					FieldConstraints: []FieldConstraintSpec{{
						Field: "value", Operator: FieldConstraintGreaterThan, Value: 10,
					}},
					Target: TemplateKeyFact(event.Key()),
				}},
				Actions: []RuleActionSpec{{Name: "noop"}},
			})
			session := mustSession(t, mustCompileWorkspace(t, workspace), SessionID("constraint-error-"+tc.name))

			_, err := session.Assert(context.Background(), event.Key(), mustFields(t, tc.fields))
			if !errors.Is(err, ErrMatcher) {
				t.Fatalf("Assert error = %v, want ErrMatcher", err)
			}
		})
	}
}

func TestIndexedEqualityConstraintDoesNotHideMissingOptionalOperand(t *testing.T) {
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "value", Kind: ValueInt},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "guard",
		Conditions: []RuleConditionSpec{{
			Binding: "event",
			FieldConstraints: []FieldConstraintSpec{{
				Field: "value", Operator: FieldConstraintEqual, Value: 10,
			}},
			Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	if node := revision.graph.alphaNode(1); node == nil || node.route.enabled {
		t.Fatalf("optional equality alpha route = %#v, want unindexed route", node)
	}
	session := mustSession(t, revision, "indexed-equality-missing-operand")

	_, err := session.Assert(context.Background(), event.Key(), mustFields(t, map[string]any{"id": "missing"}))
	if !errors.Is(err, ErrMatcher) {
		t.Fatalf("Assert error = %v, want ErrMatcher", err)
	}
}

func TestNegatedBetaEvaluationErrorPropagatesFromAssert(t *testing.T) {
	workspace := NewWorkspace()
	seed := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "seed",
		Fields: []FieldSpec{{Name: "limit", Kind: ValueInt, Required: true}},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "candidate",
		Fields: []FieldSpec{{Name: "value", Kind: ValueAny}},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "unblocked",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "seed", Target: TemplateKeyFact(seed.Key())},
			Not{Condition: Match{
				Binding: "candidate",
				Predicates: []ExpressionSpec{CompareExpr{
					Operator: ExpressionCompareGreaterThan,
					Left:     CurrentFieldExpr{Field: "value"},
					Right:    BindingFieldExpr{Binding: "seed", Field: "limit"},
				}},
				Target: TemplateKeyFact(candidate.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "negative-beta-evaluation-error")
	if _, err := session.Assert(context.Background(), candidate.Key(), mustFields(t, map[string]any{"value": "bad"})); err != nil {
		t.Fatalf("Assert(candidate): %v", err)
	}

	_, err := session.Assert(context.Background(), seed.Key(), mustFields(t, map[string]any{"limit": 10}))
	if !errors.Is(err, ErrMatcher) {
		t.Fatalf("Assert(seed) error = %v, want ErrMatcher", err)
	}
}

func TestExpressionEvaluationErrorPropagatesFromRun(t *testing.T) {
	workspace := NewWorkspace()
	trigger := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "trigger",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	derived := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "derived",
		Fields: []FieldSpec{{Name: "value", Kind: ValueAny}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "produce",
		Fn: func(ctx ActionContext) error {
			_, err := ctx.Assert(derived.Key(), Fields{})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "producer",
		Conditions: []RuleConditionSpec{{Binding: "trigger", Target: TemplateKeyFact(trigger.Key())}},
		Actions:    []RuleActionSpec{{Name: "produce"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "derived-guard",
		Conditions: []RuleConditionSpec{{
			Binding: "derived",
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "value"},
				Right:    ConstExpr{Value: 0},
			}},
			Target: TemplateKeyFact(derived.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "run-evaluation-error")
	if _, err := session.Assert(context.Background(), trigger.Key(), mustFields(t, map[string]any{"id": "go"})); err != nil {
		t.Fatalf("Assert(trigger): %v", err)
	}

	result, err := session.Run(context.Background())
	if !errors.Is(err, ErrMatcher) || !errors.Is(err, ErrActionFailed) {
		t.Fatalf("Run error = %v, want ErrActionFailed wrapping ErrMatcher", err)
	}
	if result.Status != RunActionFailed {
		t.Fatalf("Run status = %v, want %v", result.Status, RunActionFailed)
	}
}

func TestExpressionEvaluationErrorPropagatesFromQuery(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "value", Kind: ValueAny},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "broken-projection",
		ConditionTree: Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
		Returns: []QueryReturnSpec{ReturnValue("matches", BooleanExpr{
			Operator: ExpressionBoolNot,
			Operands: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     BindingFieldExpr{Binding: "item", Field: "value"},
				Right:    ConstExpr{Value: 0},
			}},
		})},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "query-evaluation-error")
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "missing"})); err != nil {
		t.Fatalf("Assert(item): %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)

	for _, tc := range []struct {
		name  string
		query func() ([]QueryRow, error)
	}{
		{name: "session", query: func() ([]QueryRow, error) {
			return session.QueryAll(context.Background(), "broken-projection", nil)
		}},
		{name: "snapshot", query: func() ([]QueryRow, error) {
			return snapshot.QueryAll(context.Background(), "broken-projection", nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.query()
			if !errors.Is(err, ErrMatcher) || !errors.Is(err, ErrQueryExecution) {
				t.Fatalf("QueryAll error = %v, want ErrQueryExecution wrapping ErrMatcher", err)
			}
		})
	}
}
