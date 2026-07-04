package engine

import (
	"context"
	"testing"
)

func TestSessionGlobalsValidateAtCompileAndSessionConstruction(t *testing.T) {
	workspace := NewWorkspace()
	mustAddGlobal(t, workspace, GlobalSpec{Name: "limit", Kind: ValueInt})
	mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Key: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "over-limit",
		Conditions: []RuleConditionSpec{{
			Binding: "item",
			Target:  TemplateFact("item"),
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "amount"},
				Right:    GlobalExpr{Name: "limit"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	if _, err := NewSession(revision); err == nil {
		t.Fatal("NewSession accepted missing required global")
	}
	if _, err := NewSession(revision, WithGlobals(map[string]any{"missing": 10})); err == nil {
		t.Fatal("NewSession accepted unknown global")
	}
	if _, err := NewSession(revision, WithGlobals(map[string]any{"limit": "high"})); err == nil {
		t.Fatal("NewSession accepted wrong global kind")
	}

	broken := NewWorkspace()
	mustAddTemplate(t, broken, TemplateSpec{Name: "item", Key: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}})
	mustAddAction(t, broken, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, broken, RuleSpec{
		Name: "unknown-global",
		Conditions: []RuleConditionSpec{{
			Binding:    "item",
			Target:     TemplateFact("item"),
			Predicates: []ExpressionSpec{CompareExpr{Operator: ExpressionCompareGreaterThan, Left: CurrentFieldExpr{Field: "amount"}, Right: GlobalExpr{Name: "limit"}}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	if _, err := broken.Compile(context.Background()); err == nil {
		t.Fatal("Compile accepted undeclared global reference")
	}
}

func TestSessionGlobalsArePerSessionAndSurviveReset(t *testing.T) {
	workspace := NewWorkspace()
	mustAddGlobal(t, workspace, GlobalSpec{Name: "limit", Kind: ValueInt})
	item := TemplateSpec{Name: "item", Key: "item", Fields: []FieldSpec{{Name: "id", Kind: ValueString}, {Name: "amount", Kind: ValueInt}}}
	mustAddTemplate(t, workspace, item)
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "over-limit",
		Conditions: []RuleConditionSpec{{
			Binding: "item",
			Target:  TemplateFact("item"),
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "amount"},
				Right:    GlobalExpr{Name: "limit"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initials := []SessionInitialFact{
		{TemplateKey: item.Key, Fields: mustFields(t, map[string]any{"id": "low", "amount": 50})},
		{TemplateKey: item.Key, Fields: mustFields(t, map[string]any{"id": "high", "amount": 150})},
	}
	lowLimit := mustNewSession(t, revision, WithGlobals(map[string]any{"limit": 10}), WithInitialFacts(initials...))
	highLimit := mustNewSession(t, revision, WithGlobals(map[string]any{"limit": 100}), WithInitialFacts(initials...))

	lowResult, err := lowLimit.Run(context.Background())
	if err != nil {
		t.Fatalf("Run low limit: %v", err)
	}
	if lowResult.Fired != 2 {
		t.Fatalf("low limit fired %d, want 2", lowResult.Fired)
	}
	highResult, err := highLimit.Run(context.Background())
	if err != nil {
		t.Fatalf("Run high limit: %v", err)
	}
	if highResult.Fired != 1 {
		t.Fatalf("high limit fired %d, want 1", highResult.Fired)
	}

	if _, err := highLimit.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	afterReset, err := highLimit.Run(context.Background())
	if err != nil {
		t.Fatalf("Run after reset: %v", err)
	}
	if afterReset.Fired != 1 {
		t.Fatalf("after reset fired %d, want 1", afterReset.Fired)
	}
}

func TestSessionGlobalsConcurrentSessionsUseIndependentValues(t *testing.T) {
	workspace := NewWorkspace()
	mustAddGlobal(t, workspace, GlobalSpec{Name: "limit", Kind: ValueInt})
	mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Key: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "over-limit",
		Conditions: []RuleConditionSpec{{
			Binding: "item",
			Target:  TemplateFact("item"),
			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "amount"},
				Right:    GlobalExpr{Name: "limit"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	newSession := func(limit int64) *Session {
		return mustNewSession(t, revision,
			WithGlobals(map[string]any{"limit": limit}),
			WithInitialFacts(
				SessionInitialFact{TemplateKey: "item", Fields: mustFields(t, map[string]any{"amount": 50})},
				SessionInitialFact{TemplateKey: "item", Fields: mustFields(t, map[string]any{"amount": 150})},
			),
		)
	}

	type outcome struct {
		fired int
		err   error
	}
	run := func(session *Session, ch chan<- outcome) {
		result, err := session.Run(context.Background())
		ch <- outcome{fired: result.Fired, err: err}
	}
	lowLimit := newSession(10)
	highLimit := newSession(100)
	lowCh := make(chan outcome, 1)
	highCh := make(chan outcome, 1)
	go run(lowLimit, lowCh)
	go run(highLimit, highCh)

	low := <-lowCh
	high := <-highCh
	if low.err != nil {
		t.Fatalf("low limit run: %v", low.err)
	}
	if high.err != nil {
		t.Fatalf("high limit run: %v", high.err)
	}
	if low.fired != 2 || high.fired != 1 {
		t.Fatalf("fired = (%d, %d), want (2, 1)", low.fired, high.fired)
	}
}

func TestSessionGlobalsInQueryReturnCallArgumentAndActionContext(t *testing.T) {
	var actionSawGlobal bool
	workspace := NewWorkspace()
	mustAddGlobal(t, workspace, GlobalSpec{Name: "limit", Kind: ValueInt, Default: 100, HasDefault: true})
	mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Key: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "above",
		Args:   []ValueKind{ValueInt, ValueInt},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			left, _ := args[0].AsInt64()
			right, _ := args[1].AsInt64()
			return NewValue(left > right)
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ctx ActionContext) error {
		value, ok := ctx.Global("limit")
		actionSawGlobal = ok && value.Kind() == ValueInt
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "call-global",
		Conditions: []RuleConditionSpec{{
			Binding:    "item",
			Target:     TemplateFact("item"),
			Predicates: []ExpressionSpec{Call("above", CurrentFieldExpr{Field: "amount"}, GlobalExpr{Name: "limit"})},
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	mustAddQuery(t, workspace, QuerySpec{
		Name:       "limits",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateFact("item")}},
		Returns:    []QueryReturnSpec{ReturnValue("limit", GlobalExpr{Name: "limit"})},
	})
	mustAddQuery(t, workspace, QuerySpec{
		Name: "global-aggregate",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateFact("item")},
			Sum(GlobalExpr{Name: "limit"}).As("total"),
		),
		Returns: []QueryReturnSpec{ReturnValue("total", BindingValueExpr{Binding: "total"})},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustNewSession(t, revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: "item",
		Fields:      mustFields(t, map[string]any{"amount": 150}),
	}))

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || !actionSawGlobal {
		t.Fatalf("run fired %d actionSawGlobal %t, want 1 true", result.Fired, actionSawGlobal)
	}
	iterator, err := session.Query(context.Background(), "limits", nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	rows, err := iterator.All(context.Background())
	if err != nil {
		t.Fatalf("Query All: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("query rows = %d, want 1", len(rows))
	}
	value, ok := rows[0].Value("limit")
	if !ok || !value.Equal(newIntValue(100)) {
		t.Fatalf("query limit = %v ok %t, want 100", value, ok)
	}
	aggregateIterator, err := session.Query(context.Background(), "global-aggregate", nil)
	if err != nil {
		t.Fatalf("aggregate query: %v", err)
	}
	aggregateRows, err := aggregateIterator.All(context.Background())
	if err != nil {
		t.Fatalf("aggregate query All: %v", err)
	}
	if len(aggregateRows) != 1 {
		t.Fatalf("aggregate query rows = %d, want 1", len(aggregateRows))
	}
	total, ok := aggregateRows[0].Value("total")
	if !ok || !total.Equal(newIntValue(100)) {
		t.Fatalf("aggregate total = %v ok %t, want 100", total, ok)
	}
}

func TestGessGlobalsCompileAndGenerate(t *testing.T) {
	source := []byte(`
(defglobal *limit* (type INT) (default 100))
(deftemplate item (slot amount (type INT)))
(defrule over-limit
  (item (amount ?amount))
  (test (> ?amount *limit*))
  =>
  (halt))
(defquery limits
  (item (amount ?amount))
  (return (limit *limit*)))`)
	revision, err := CompileGess(context.Background(), "globals.gess", source, DSLRegistry{})
	if err != nil {
		t.Fatalf("CompileGess: %v", err)
	}
	if _, ok := revision.Global("limit"); !ok {
		t.Fatal("compiled ruleset missing global")
	}
	if _, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "globals.gess", Source: source}}, GessGoGeneratorOptions{PackageName: "generated"}); err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}
}

func BenchmarkGlobalExpressionPredicate(b *testing.B) {
	fact := newConditionFactRefFromSnapshot(factSnapshotWithFields(map[string]Value{"amount": newIntValue(150)}))
	globals := map[string]compiledGlobal{"limit": {name: "limit", kind: ValueInt, slot: 0}}
	globalValues := []Value{newIntValue(100)}
	cases := []struct {
		name    string
		expr    ExpressionSpec
		globals []Value
	}{
		{
			name: "const",
			expr: CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "amount"},
				Right:    ConstExpr{Value: 100},
			},
		},
		{
			name: "global",
			expr: CompareExpr{
				Operator: ExpressionCompareGreaterThan,
				Left:     CurrentFieldExpr{Field: "amount"},
				Right:    GlobalExpr{Name: "limit"},
			},
			globals: globalValues,
		},
	}
	for _, tc := range cases {
		_, predicate, err := compileExpressionPredicateSpecWithParams(tc.expr, "bench", 0, 0, nil, nil, nil, nil, nil, nil, globals)
		if err != nil {
			b.Fatalf("compile %s: %v", tc.name, err)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ok, err := predicate.matchesWithContextParamsGlobalsAndCounters(context.Background(), fact, nil, nil, tc.globals, nil)
				if err != nil || !ok {
					b.Fatalf("predicate = (%t, %v), want true nil", ok, err)
				}
			}
		})
	}
}

func mustAddGlobal(t *testing.T, workspace *Workspace, spec GlobalSpec) {
	t.Helper()
	if err := workspace.AddGlobal(spec); err != nil {
		t.Fatalf("AddGlobal(%q): %v", spec.Name, err)
	}
}

func mustNewSession(t *testing.T, revision *Ruleset, opts ...SessionOption) *Session {
	t.Helper()
	session, err := NewSession(revision, opts...)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func mustAddQuery(t *testing.T, workspace *Workspace, spec QuerySpec) {
	t.Helper()
	if err := workspace.AddQuery(spec); err != nil {
		t.Fatalf("AddQuery(%q): %v", spec.Name, err)
	}
}
