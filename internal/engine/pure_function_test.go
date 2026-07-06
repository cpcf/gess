package engine

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

			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: Call("risk-band", CurrentFieldExpr{Field: "score"}), Right: ConstExpr{Value: "high"}},
			}, Target: DynamicFact("finding"),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "beta-call",
		Conditions: []RuleConditionSpec{
			{Binding: "system", Target: DynamicFact("system")},
			{
				Binding: "finding",

				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     Call("same-id", CurrentFieldExpr{Field: "system-id"}, BindingFieldExpr{Binding: "system", Field: "id"}),
						Right:    ConstExpr{Value: true},
					},
				}, Target: DynamicFact("finding"),
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "pure-function-predicate-session")

	ctx := context.Background()
	if _, err := session.assertByName(ctx, "system", mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("Assert(system): %v", err)
	}
	if _, err := session.assertByName(ctx, "finding", mustFields(t, map[string]any{"system-id": "s-1", "score": 95})); err != nil {
		t.Fatalf("Assert(finding high): %v", err)
	}
	if _, err := session.assertByName(ctx, "finding", mustFields(t, map[string]any{"system-id": "s-2", "score": 20})); err != nil {
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

func TestPureFunctionPredicatesExecuteFixedArityCalls(t *testing.T) {
	workspace := NewWorkspace()
	for _, spec := range []PureFunctionSpec{
		{
			Name:   "zero",
			Return: ValueBool,
			Func0: func(context.Context) (Value, error) {
				return NewValue(true)
			},
		},
		{
			Name:   "one",
			Args:   []ValueKind{ValueInt},
			Return: ValueBool,
			Func1: func(_ context.Context, arg Value) (Value, error) {
				value, _ := arg.AsInt64()
				return NewValue(value == 1)
			},
		},
		{
			Name:   "two",
			Args:   []ValueKind{ValueInt, ValueInt},
			Return: ValueBool,
			Func2: func(_ context.Context, arg0, arg1 Value) (Value, error) {
				left, _ := arg0.AsInt64()
				right, _ := arg1.AsInt64()
				return NewValue(left+right == 3)
			},
		},
		{
			Name:   "three",
			Args:   []ValueKind{ValueInt, ValueInt, ValueInt},
			Return: ValueBool,
			Func3: func(_ context.Context, arg0, arg1, arg2 Value) (Value, error) {
				first, _ := arg0.AsInt64()
				second, _ := arg1.AsInt64()
				third, _ := arg2.AsInt64()
				return NewValue(first+second+third == 6)
			},
		},
	} {
		mustAddPureFunction(t, workspace, spec)
	}
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "fixed-arity-calls",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			Predicates: []ExpressionSpec{
				Call("zero"),
				Call("one", CurrentFieldExpr{Field: "a"}),
				Call("two", CurrentFieldExpr{Field: "a"}, CurrentFieldExpr{Field: "b"}),
				Call("three", CurrentFieldExpr{Field: "a"}, CurrentFieldExpr{Field: "b"}, CurrentFieldExpr{Field: "c"}),
			}, Target: DynamicFact("event"),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "pure-function-fixed-arity-session")

	if _, err := session.assertByName(context.Background(), "event", mustFields(t, map[string]any{"a": 1, "b": 2, "c": 3})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}
}

func TestPureFunctionCompileValidation(t *testing.T) {
	workspace := NewWorkspace()
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "unknown-call",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: Call("missing", CurrentFieldExpr{Field: "value"}), Right: ConstExpr{Value: true}},
			}, Target: DynamicFact("event"),
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
			Binding: "event",

			Predicates: []ExpressionSpec{Call("positive")}, Target: DynamicFact("event"),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	if _, err := workspace.Compile(context.Background()); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("Compile arity error = %v, want ErrFunctionValidation", err)
	}

	workspace = NewWorkspace()
	if err := workspace.AddFunction(PureFunctionSpec{
		Name:  "fixed-mismatch",
		Args:  []ValueKind{ValueInt, ValueInt},
		Func1: func(context.Context, Value) (Value, error) { return NewValue(true) },
	}); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("AddFunction fixed arity mismatch error = %v, want ErrFunctionValidation", err)
	}

	workspace = NewWorkspace()
	if err := workspace.AddFunction(PureFunctionSpec{
		Name:  "duplicate-impl",
		Func:  func(context.Context, []Value) (Value, error) { return NewValue(true) },
		Func0: func(context.Context) (Value, error) { return NewValue(true) },
	}); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("AddFunction duplicate implementation error = %v, want ErrFunctionValidation", err)
	}
}

func TestPureFunctionIndexKeyExtractorValidationAndInspection(t *testing.T) {
	workspace := NewWorkspace()
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:              "fold-key",
		Args:              []ValueKind{ValueString},
		Return:            ValueString,
		IndexKeyExtractor: true,
		Func1: func(_ context.Context, value Value) (Value, error) {
			return value, nil
		},
	})
	revision := mustCompileWorkspace(t, workspace)
	definition, ok := revision.Function("fold-key")
	if !ok {
		t.Fatal("compiled revision missing fold-key")
	}
	if !definition.IndexKeyExtractor() {
		t.Fatal("fold-key was not inspected as an index key extractor")
	}

	if err := NewWorkspace().AddFunction(PureFunctionSpec{
		Name:              "bad-key",
		Args:              []ValueKind{ValueString, ValueString},
		Return:            ValueString,
		IndexKeyExtractor: true,
		Func2: func(_ context.Context, left, right Value) (Value, error) {
			return left, nil
		},
	}); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("AddFunction bad-key error = %v, want ErrFunctionValidation", err)
	}
	if err := NewWorkspace().AddFunction(PureFunctionSpec{
		Name:              "list-key",
		Args:              []ValueKind{ValueString},
		Return:            ValueList,
		IndexKeyExtractor: true,
		Func1: func(_ context.Context, value Value) (Value, error) {
			return NewValue([]any{value.String()})
		},
	}); !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("AddFunction list-key error = %v, want ErrFunctionValidation", err)
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
					Binding: "event",

					Predicates: []ExpressionSpec{Call("fail")}, Target: DynamicFact("event"),
				}},
				Actions: []RuleActionSpec{{Name: "noop"}},
			})
			session := mustSession(t, mustCompileWorkspace(t, workspace), SessionID("pure-function-"+tc.name))

			_, err := session.assertByName(context.Background(), "event", Fields{})
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

	_, err := session.assertByName(context.Background(), "event", mustFields(t, map[string]any{"status": "bad"}))
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

	inserted, err := session.assertByName(ctx, "event", mustFields(t, map[string]any{"status": "good"}))
	if err != nil {
		t.Fatalf("Assert good: %v", err)
	}
	session.attachPropagationCounters()
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
	counters := session.propagationCounterSnapshot()
	if got := counters.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
}

func TestPureFunctionPredicateModifyFailureRollsBackDuplicateIndex(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "status-ok",
		Args:   []ValueKind{ValueString},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			status, _ := args[0].AsString()
			if status == "bad" {
				return Value{}, fmt.Errorf("status failed")
			}
			return NewValue(true)
		},
	})
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "status-rule",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			Predicates: []ExpressionSpec{Call("status-ok", CurrentFieldExpr{Field: "status"})}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "pure-function-modify-duplicate-index-rollback")

	inserted, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{"id": "a", "status": "good"}))
	if err != nil {
		t.Fatalf("Assert good: %v", err)
	}
	session.attachPropagationCounters()
	result, err := session.Modify(ctx, inserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"id": "b", "status": "bad"})})
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
	id, ok := fact.Field("id")
	if !ok || !id.Equal(mustValue(t, "a")) {
		t.Fatalf("id after failed modify = %v/%v, want a/true", id, ok)
	}
	if _, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{"id": "b", "status": "good"})); err != nil {
		t.Fatalf("Assert after failed modify should not see stale duplicate index: %v", err)
	}
	counters := session.propagationCounterSnapshot()
	if got := counters.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
}

func TestPureFunctionPredicateRetractFailureRollsBackFact(t *testing.T) {
	ctx := context.Background()
	fail := false
	revision := mustPureFunctionSwitchRuleset(t, &fail)
	session := mustSession(t, revision, "pure-function-retract-rollback")

	inserted, err := session.assertByName(ctx, "event", mustFields(t, map[string]any{"status": "good"}))
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

func TestPureFunctionPredicateMissingGraphReturnsUnsupportedRuntime(t *testing.T) {
	ctx := context.Background()
	baseWorkspace := NewWorkspace()
	mustAddAction(t, baseWorkspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	baseSession := mustSession(t, mustCompileWorkspace(t, baseWorkspace), "pure-function-classic-alpha-source")
	inserted, err := baseSession.assertByName(ctx, "event", mustFields(t, map[string]any{"status": "bad"}))
	if err != nil {
		t.Fatalf("Assert source fact: %v", err)
	}

	revision := mustPureFunctionFailureRuleset(t)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	runtime.graph = nil
	runtime.graphBeta = nil

	err = runtime.resetGraphBeta(ctx, []FactSnapshot{inserted.Fact})
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("resetGraphBeta error = %v, want ErrUnsupportedRuntime", err)
	}
}

func TestPureFunctionPredicateApplyRulesetFailureRollsBackSessionState(t *testing.T) {
	ctx := context.Background()
	baseWorkspace := NewWorkspace()
	mustAddAction(t, baseWorkspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	baseRevision := mustCompileWorkspace(t, baseWorkspace)
	session := mustSession(t, baseRevision, "pure-function-apply-ruleset-rollback")
	inserted, err := session.assertByName(ctx, "event", mustFields(t, map[string]any{"status": "bad"}))
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
		ConditionTree: Accumulate(Match(RuleConditionSpec{Binding: "item", Target: DynamicFact("item")}),
			Sum(Call("double", BindingFieldExpr{Binding: "item", Field: "amount"})).As("total")),
		Actions: []RuleActionSpec{{Name: "observe-total"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "item-values",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: DynamicFact("item")}},
		Returns: []QueryReturnSpec{
			ReturnValue("doubled", Call("double", BindingFieldExpr{Binding: "item", Field: "amount"})),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "pure-function-query-aggregate-session")

	ctx := context.Background()
	if _, err := session.assertByName(ctx, "item", mustFields(t, map[string]any{"amount": 2})); err != nil {
		t.Fatalf("Assert item 1: %v", err)
	}
	if _, err := session.assertByName(ctx, "item", mustFields(t, map[string]any{"amount": 3})); err != nil {
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

func TestExpressionFunctionPredicatesExecuteAlphaBetaAndTest(t *testing.T) {
	workspace := NewWorkspace()
	mustAddExpressionFunction(t, workspace, ExpressionFunctionSpec{
		Name:   "high-score",
		Params: []ExpressionFunctionParamSpec{{Name: "score", Kind: ValueInt}},
		Return: ValueBool,
		Expression: CompareExpr{
			Operator: ExpressionCompareGreaterOrEqual,
			Left:     ParamExpr{Name: "score"},
			Right:    ConstExpr{Value: 90},
		},
	})
	mustAddExpressionFunction(t, workspace, ExpressionFunctionSpec{
		Name:   "same-text",
		Params: []ExpressionFunctionParamSpec{{Name: "left", Kind: ValueString}, {Name: "right", Kind: ValueString}},
		Return: ValueBool,
		Expression: CompareExpr{
			Operator: ExpressionCompareEqual,
			Left:     ParamExpr{Name: "left"},
			Right:    ParamExpr{Name: "right"},
		},
	})
	mustAddExpressionFunction(t, workspace, ExpressionFunctionSpec{
		Name:       "composed-high-score",
		Params:     []ExpressionFunctionParamSpec{{Name: "score", Kind: ValueInt}},
		Return:     ValueBool,
		Expression: Call("high-score", ParamExpr{Name: "score"}),
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "alpha-expression-call",
		Conditions: []RuleConditionSpec{{
			Binding: "finding",
			Target:  DynamicFact("finding"),
			Predicates: []ExpressionSpec{
				Call("high-score", CurrentFieldExpr{Field: "score"}),
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "beta-expression-call",
		Conditions: []RuleConditionSpec{
			{Binding: "system", Target: DynamicFact("system")},
			{
				Binding: "finding",
				Target:  DynamicFact("finding"),
				Predicates: []ExpressionSpec{
					Call("same-text", CurrentFieldExpr{Field: "system-id"}, BindingFieldExpr{Binding: "system", Field: "id"}),
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "test-expression-call",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{Binding: "finding", Target: DynamicFact("finding")}),
			Test{Expression: Call("composed-high-score", BindingFieldExpr{Binding: "finding", Field: "score"})},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	definition, ok := revision.Function("high-score")
	if !ok || !definition.ExpressionBacked() {
		t.Fatalf("high-score inspection = (%v, %v), want expression-backed function", definition, ok)
	}
	if params := definition.ParamNames(); len(params) != 1 || params[0] != "score" {
		t.Fatalf("ParamNames = %#v, want [score]", params)
	}
	session := mustSession(t, revision, "expression-function-predicate-session")

	ctx := context.Background()
	if _, err := session.assertByName(ctx, "system", mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("Assert(system): %v", err)
	}
	if _, err := session.assertByName(ctx, "finding", mustFields(t, map[string]any{"system-id": "s-1", "score": 95})); err != nil {
		t.Fatalf("Assert(finding high): %v", err)
	}
	if _, err := session.assertByName(ctx, "finding", mustFields(t, map[string]any{"system-id": "s-2", "score": 20})); err != nil {
		t.Fatalf("Assert(finding low): %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 3 {
		t.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
	}
}

func TestExpressionFunctionCallsInQueryReturnsAndAggregates(t *testing.T) {
	workspace := NewWorkspace()
	mustAddExpressionFunction(t, workspace, ExpressionFunctionSpec{
		Name:       "identity-int",
		Params:     []ExpressionFunctionParamSpec{{Name: "value", Kind: ValueInt}},
		Return:     ValueInt,
		Expression: ParamExpr{Name: "value"},
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
		Name: "aggregate-expression-call",
		ConditionTree: Accumulate(Match(RuleConditionSpec{Binding: "item", Target: DynamicFact("item")}),
			Sum(Call("identity-int", BindingFieldExpr{Binding: "item", Field: "amount"})).As("total")),
		Actions: []RuleActionSpec{{Name: "observe-total"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "item-values",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: DynamicFact("item")}},
		Returns: []QueryReturnSpec{
			ReturnValue("amount", Call("identity-int", BindingFieldExpr{Binding: "item", Field: "amount"})),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "expression-function-query-aggregate-session")

	ctx := context.Background()
	if _, err := session.assertByName(ctx, "item", mustFields(t, map[string]any{"amount": 2})); err != nil {
		t.Fatalf("Assert item 1: %v", err)
	}
	if _, err := session.assertByName(ctx, "item", mustFields(t, map[string]any{"amount": 3})); err != nil {
		t.Fatalf("Assert item 2: %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || !observed.Equal(mustValue(t, 5)) {
		t.Fatalf("aggregate fired/total = %d/%v, want 1/5", result.Fired, observed)
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
		value, ok := row.Value("amount")
		if !ok {
			t.Fatal("missing amount value")
		}
		values[value.canonicalKey()] = true
	}
	if !values[mustValue(t, 2).canonicalKey()] || !values[mustValue(t, 3).canonicalKey()] {
		t.Fatalf("query amount values = %#v, want 2 and 3", values)
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
			Binding: "event",

			Predicates: []ExpressionSpec{Call("status-ok", CurrentFieldExpr{Field: "status"})}, Target: DynamicFact("event"),
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

func mustAddExpressionFunction(t testing.TB, workspace *Workspace, spec ExpressionFunctionSpec) {
	t.Helper()
	if err := workspace.AddExpressionFunction(spec); err != nil {
		t.Fatalf("AddExpressionFunction(%q): %v", spec.Name, err)
	}
}
