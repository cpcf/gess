package gess

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestPureFunctionPredicatesExecuteAlphaAndBeta(t *testing.T) {
	workspace := NewWorkspace()
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "risk-band",
		Args:   []ValueKind{ValueInt},
		Return: ValueString,
		Func: func(_ context.Context, args []Value) (Value, error) {
			score, _ := args[0].AsInt64()
			if score >= 90 {
				return NewValue("high")
			}
			return NewValue("low")
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "same-id",
		Args:   []ValueKind{ValueString, ValueString},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			left, _ := args[0].AsString()
			right, _ := args[1].AsString()
			return NewValue(left == right)
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "alpha-call",
		Conditions: []RuleConditionSpec{{
			Binding: "finding",
			Name:    "finding",
			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: Call("risk-band", CurrentFieldExpr{Field: "score"}), Right: ConstExpr{Value: "high"}},
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "beta-call",
		Conditions: []RuleConditionSpec{
			{Binding: "system", Name: "system"},
			{
				Binding: "finding",
				Name:    "finding",
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     Call("same-id", CurrentFieldExpr{Field: "system-id"}, BindingFieldExpr{Binding: "system", Field: "id"}),
						Right:    ConstExpr{Value: true},
					},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "pure-function-predicate-session")

	ctx := context.Background()
	if _, err := session.Assert(ctx, "system", mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("Assert(system): %v", err)
	}
	if _, err := session.Assert(ctx, "finding", mustFields(t, map[string]any{"system-id": "s-1", "score": 95})); err != nil {
		t.Fatalf("Assert(finding high): %v", err)
	}
	if _, err := session.Assert(ctx, "finding", mustFields(t, map[string]any{"system-id": "s-2", "score": 20})); err != nil {
		t.Fatalf("Assert(finding low): %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}
}

func TestPureFunctionCompileValidation(t *testing.T) {
	workspace := NewWorkspace()
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "unknown-call",
		Conditions: []RuleConditionSpec{{
			Binding: "event",
			Name:    "event",
			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: Call("missing", CurrentFieldExpr{Field: "value"}), Right: ConstExpr{Value: true}},
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	if _, err := workspace.Compile(context.Background()); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("Compile unknown function error = %v, want ErrFunctionValidation", err)
	}

	workspace = NewWorkspace()
	mustAddPureFunction(t, workspace, PureFunctionSpec{Name: "positive", Args: []ValueKind{ValueInt}, Return: ValueBool, Func: func(context.Context, []Value) (Value, error) {
		return NewValue(true)
	}})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "bad-arity",
		Conditions: []RuleConditionSpec{{
			Binding:    "event",
			Name:       "event",
			Predicates: []ExpressionSpec{Call("positive")},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	if _, err := workspace.Compile(context.Background()); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("Compile arity error = %v, want ErrFunctionValidation", err)
	}
}

func TestPureFunctionEvaluationErrorsAreStructured(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   PureFunction
		want error
	}{
		{
			name: "returned-error",
			fn: func(ctx context.Context, _ []Value) (Value, error) {
				if ctx == nil {
					return Value{}, fmt.Errorf("missing context")
				}
				return Value{}, context.Canceled
			},
			want: context.Canceled,
		},
		{
			name: "panic",
			fn: func(context.Context, []Value) (Value, error) {
				panic("boom")
			},
			want: ErrFunctionEvaluation,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			mustAddPureFunction(t, workspace, PureFunctionSpec{Name: "fail", Return: ValueBool, Func: tc.fn})
			mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, RuleSpec{
				Name: "failing-call",
				Conditions: []RuleConditionSpec{{
					Binding:    "event",
					Name:       "event",
					Predicates: []ExpressionSpec{Call("fail")},
				}},
				Actions: []RuleActionSpec{{Name: "noop"}},
			})
			session := mustSession(t, mustCompileWorkspace(t, workspace), SessionID("pure-function-"+tc.name))

			_, err := session.Assert(context.Background(), "event", Fields{})
			if !errors.Is(err, ErrFunctionEvaluation) {
				t.Fatalf("Assert error = %v, want ErrFunctionEvaluation", err)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Assert error = %v, want wrapped %v", err, tc.want)
			}
			var evalErr *FunctionEvaluationError
			if !errors.As(err, &evalErr) || evalErr.FunctionName != "fail" || evalErr.RuleName != "failing-call" {
				t.Fatalf("FunctionEvaluationError = %#v", evalErr)
			}
		})
	}
}

func TestPureFunctionPredicateAssertFailureRollsBackFact(t *testing.T) {
	revision := mustPureFunctionFailureRuleset(t)
	session := mustSession(t, revision, "pure-function-assert-rollback")

	_, err := session.Assert(context.Background(), "event", mustFields(t, map[string]any{"status": "bad"}))
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("Assert error = %v, want ErrFunctionEvaluation", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	if got := len(snapshot.FactsByName("event")); got != 0 {
		t.Fatalf("facts after failed assert = %d, want 0", got)
	}
}

func TestPureFunctionPredicateModifyFailureRollsBackFact(t *testing.T) {
	ctx := context.Background()
	revision := mustPureFunctionFailureRuleset(t)
	session := mustSession(t, revision, "pure-function-modify-rollback")

	inserted, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"status": "good"}))
	if err != nil {
		t.Fatalf("Assert good: %v", err)
	}
	result, err := session.Modify(ctx, inserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"status": "bad"})})
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("Modify error = %v, want ErrFunctionEvaluation", err)
	}
	if result.Status != ModifyValidationFailure {
		t.Fatalf("Modify status = %v, want %v", result.Status, ModifyValidationFailure)
	}
	snapshot := mustSnapshot(t, ctx, session)
	fact, ok := snapshot.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatal("fact missing after failed modify")
	}
	status, ok := fact.Field("status")
	if !ok || !status.Equal(mustValue(t, "good")) {
		t.Fatalf("status after failed modify = %v/%v, want good/true", status, ok)
	}
}

func TestPureFunctionPredicateRetractFailureRollsBackFact(t *testing.T) {
	ctx := context.Background()
	fail := false
	revision := mustPureFunctionSwitchRuleset(t, &fail)
	session := mustSession(t, revision, "pure-function-retract-rollback")

	inserted, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"status": "good"}))
	if err != nil {
		t.Fatalf("Assert good: %v", err)
	}
	fail = true
	result, err := session.Retract(ctx, inserted.Fact.ID())
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("Retract error = %v, want ErrFunctionEvaluation", err)
	}
	if result.Status != RetractValidationFailure {
		t.Fatalf("Retract status = %v, want %v", result.Status, RetractValidationFailure)
	}
	snapshot := mustSnapshot(t, ctx, session)
	if _, ok := snapshot.Fact(inserted.Fact.ID()); !ok {
		t.Fatal("fact missing after failed retract")
	}
}

func TestPureFunctionPredicateInitialFactErrorsAreReturned(t *testing.T) {
	revision := mustPureFunctionFailureRuleset(t)
	_, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		Name:   "event",
		Fields: mustFields(t, map[string]any{"status": "bad"}),
	}))
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("NewSession error = %v, want ErrFunctionEvaluation", err)
	}
}

func TestPureFunctionPredicateClassicAlphaErrorsAreReturned(t *testing.T) {
	ctx := context.Background()
	baseWorkspace := NewWorkspace()
	mustAddAction(t, baseWorkspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	baseSession := mustSession(t, mustCompileWorkspace(t, baseWorkspace), "pure-function-classic-alpha-source")
	inserted, err := baseSession.Assert(ctx, "event", mustFields(t, map[string]any{"status": "bad"}))
	if err != nil {
		t.Fatalf("Assert source fact: %v", err)
	}

	revision := mustPureFunctionFailureRuleset(t)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	runtime.graph = nil
	runtime.graphAlpha = nil
	runtime.graphBeta = nil

	err = runtime.resetAlpha(ctx, []FactSnapshot{inserted.Fact})
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("resetAlpha error = %v, want ErrFunctionEvaluation", err)
	}
}

func TestPureFunctionPredicateApplyRulesetFailureRollsBackSessionState(t *testing.T) {
	ctx := context.Background()
	baseWorkspace := NewWorkspace()
	mustAddAction(t, baseWorkspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	baseRevision := mustCompileWorkspace(t, baseWorkspace)
	session := mustSession(t, baseRevision, "pure-function-apply-ruleset-rollback")
	inserted, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"status": "bad"}))
	if err != nil {
		t.Fatalf("Assert bad: %v", err)
	}

	nextRevision := mustPureFunctionFailureRuleset(t)
	_, err = session.ApplyRuleset(ctx, nextRevision)
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("ApplyRuleset error = %v, want ErrFunctionEvaluation", err)
	}
	if got := session.RulesetID(); got != baseRevision.ID() {
		t.Fatalf("ruleset after failed apply = %q, want %q", got, baseRevision.ID())
	}
	snapshot := mustSnapshot(t, ctx, session)
	if _, ok := snapshot.Fact(inserted.Fact.ID()); !ok {
		t.Fatal("fact missing after failed apply")
	}
}

func TestPureFunctionPredicateResetFailureRollsBackFactWorkspace(t *testing.T) {
	ctx := context.Background()
	revision := mustPureFunctionFailureRuleset(t)
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		Name:   "event",
		Fields: mustFields(t, map[string]any{"status": "good"}),
	}))
	if err != nil {
		t.Fatalf("NewSession good: %v", err)
	}

	session.initials = append(session.initials, SessionInitialFact{
		Name:   "event",
		Fields: mustFields(t, map[string]any{"status": "bad"}),
	})
	result, err := session.Reset(ctx)
	if !errors.Is(err, ErrFunctionEvaluation) {
		t.Fatalf("Reset error = %v, want ErrFunctionEvaluation", err)
	}
	if result.Status != ResetValidationFailure {
		t.Fatalf("Reset status = %v, want %v", result.Status, ResetValidationFailure)
	}
	snapshot := mustSnapshot(t, ctx, session)
	facts := snapshot.FactsByName("event")
	if len(facts) != 1 {
		t.Fatalf("facts after failed reset = %d, want 1", len(facts))
	}
	status, ok := facts[0].Field("status")
	if !ok || !status.Equal(mustValue(t, "good")) {
		t.Fatalf("status after failed reset = %v/%v, want good/true", status, ok)
	}
}

func TestPureFunctionCallsInQueryReturnsAndAggregates(t *testing.T) {
	workspace := NewWorkspace()
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "double",
		Args:   []ValueKind{ValueInt},
		Return: ValueInt,
		Func: func(_ context.Context, args []Value) (Value, error) {
			value, _ := args[0].AsInt64()
			return NewValue(value * 2)
		},
	})
	var observed Value
	mustAddAction(t, workspace, ActionSpec{Name: "observe-total", Fn: func(ctx ActionContext) error {
		value, ok := ctx.BindingValue("total")
		if !ok {
			return fmt.Errorf("missing total")
		}
		observed = value
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "aggregate-call",
		ConditionTree: Accumulate(Match(RuleConditionSpec{Binding: "item", Name: "item"}),
			Sum(Call("double", BindingFieldExpr{Binding: "item", Field: "amount"})).As("total")),
		Actions: []RuleActionSpec{{Name: "observe-total"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "item-values",
		Conditions: []RuleConditionSpec{{Binding: "item", Name: "item"}},
		Returns: []QueryReturnSpec{
			ReturnValue("doubled", Call("double", BindingFieldExpr{Binding: "item", Field: "amount"})),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "pure-function-query-aggregate-session")

	ctx := context.Background()
	if _, err := session.Assert(ctx, "item", mustFields(t, map[string]any{"amount": 2})); err != nil {
		t.Fatalf("Assert item 1: %v", err)
	}
	if _, err := session.Assert(ctx, "item", mustFields(t, map[string]any{"amount": 3})); err != nil {
		t.Fatalf("Assert item 2: %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || !observed.Equal(mustValue(t, 10)) {
		t.Fatalf("aggregate fired/total = %d/%v, want 1/10", result.Fired, observed)
	}

	rows, err := session.QueryAll(ctx, "item-values", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("query rows = %d, want 2", len(rows))
	}
	values := map[string]bool{}
	for _, row := range rows {
		value, ok := row.Value("doubled")
		if !ok {
			t.Fatal("missing doubled value")
		}
		values[value.canonicalKey()] = true
	}
	if !values[mustValue(t, 4).canonicalKey()] || !values[mustValue(t, 6).canonicalKey()] {
		t.Fatalf("query doubled values = %#v, want 4 and 6", values)
	}
}

func mustPureFunctionFailureRuleset(t testing.TB) *Ruleset {
	t.Helper()
	fail := false
	return mustPureFunctionSwitchRuleset(t, &fail)
}

func mustPureFunctionSwitchRuleset(t testing.TB, fail *bool) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "status-ok",
		Args:   []ValueKind{ValueString},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			status, _ := args[0].AsString()
			if status == "bad" || fail != nil && *fail {
				return Value{}, fmt.Errorf("status failed")
			}
			return NewValue(true)
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "status-rule",
		Conditions: []RuleConditionSpec{{
			Binding:    "event",
			Name:       "event",
			Predicates: []ExpressionSpec{Call("status-ok", CurrentFieldExpr{Field: "status"})},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	return mustCompileWorkspace(t, workspace)
}

func mustAddPureFunction(t testing.TB, workspace *Workspace, spec PureFunctionSpec) {
	t.Helper()
	if err := workspace.AddFunction(spec); err != nil {
		t.Fatalf("AddFunction(%q): %v", spec.Name, err)
	}
}
