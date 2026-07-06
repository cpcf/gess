package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAccumulateBuiltInsBindValuesAndUpdateAcrossMutations(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			row := Fields{}
			for _, name := range []string{"count", "sum", "min", "max", "collected"} {
				value, ok := ctx.BindingValue(name)
				if !ok {
					return errors.New("missing aggregate binding " + name)
				}
				row[name] = value
			}
			observed = append(observed, row)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "totals",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("sum"),
			Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
			Max(BindingFieldExpr{Binding: "item", Field: "amount"}).As("max"),
			Collect(BindingFieldExpr{Binding: "item", Field: "amount"}).As("collected"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-session")

	first, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertAggregateRow(t, observed[len(observed)-1], 2, 8, 3, 5, []Value{mustValue(t, 3), mustValue(t, 5)})

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 1})}); err != nil {
		t.Fatalf("modify second: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("second Run fired = %d, want 1", result.Fired)
	}
	assertAggregateRow(t, observed[len(observed)-1], 2, 4, 1, 3, []Value{mustValue(t, 3), mustValue(t, 1)})

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertAggregateRow(t, observed[len(observed)-1], 1, 1, 1, 1, []Value{mustValue(t, 1)})
}

func TestAccumulateEmptyCountSumCollectAndMinMaxNoContinuation(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "amount", Kind: ValueInt},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			fired++
			count, ok := ctx.BindingValue("count")
			if !ok || !count.Equal(mustValue(t, 0)) {
				return errors.New("count did not bind zero")
			}
			sum, ok := ctx.BindingValue("sum")
			if !ok || !sum.Equal(mustValue(t, 0)) {
				return errors.New("sum did not bind zero")
			}
			collected, ok := ctx.BindingValue("collected")
			if !ok || collected.Kind() != ValueList || len(collected.data.([]Value)) != 0 {
				return errors.New("collect did not bind empty list")
			}
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "empty-count-sum-collect",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("sum"),
			Collect(BindingFieldExpr{Binding: "item", Field: "amount"}).As("collected"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "empty-min-suppresses",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-empty-session")
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("fired = result %d action %d, want 1", result.Fired, fired)
	}
}

func TestAccumulateCountAndSumUseIncrementalAgendaDeltas(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, Fields{"count": count, "total": total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "total",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-incremental-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental aggregate agenda support", session.rete)
	}

	first, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 2)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 8)) {
		t.Fatalf("first Run fired/row = %d/%v, want 1/count=2 total=8", result.Fired, observed[len(observed)-1])
	}

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 1})}); err != nil {
		t.Fatalf("modify second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 2)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 4)) {
		t.Fatalf("second Run fired/row = %d/%v, want 1/count=2 total=4", result.Fired, observed[len(observed)-1])
	}

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 1)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 1)) {
		t.Fatalf("third Run fired/row = %d/%v, want 1/count=1 total=1", result.Fired, observed[len(observed)-1])
	}
}

func TestAccumulateGraphMatchMaterializesFromSourceBoundary(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "total",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-terminal-match-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental aggregate agenda support", session.rete)
	}

	if _, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3})); err != nil {
		t.Fatalf("assert first: %v", err)
	}
	if _, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5})); err != nil {
		t.Fatalf("assert second: %v", err)
	}

	results, err := session.rete.graphBeta.match(ctx, mustSnapshot(t, ctx, session))
	if err != nil {
		t.Fatalf("graph beta match: %v", err)
	}
	if got, want := len(results), 1; got != want {
		t.Fatalf("match results = %d, want %d", got, want)
	}
	if got, want := len(results[0].candidates), 1; got != want {
		t.Fatalf("aggregate terminal candidates = %d, want %d", got, want)
	}
}

func TestAccumulateUnsupportedGraphShapeReturnsRuntimeError(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "two-aggregates",
		ConditionTree: And{Conditions: []ConditionSpec{
			Accumulate(
				Match{Binding: "left", Target: TemplateKeyFact(item.Key())},
				Count().As("left_count"),
			),
			Accumulate(
				Match{Binding: "right", Target: TemplateKeyFact(item.Key())},
				Sum(BindingFieldExpr{Binding: "right", Field: "amount"}).As("right_total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if runtime.supportsGraphBeta() {
		t.Fatal("runtime supports graph beta for unsupported aggregate shape")
	}
	err = runtime.validateExecutableGraphBetaRuntime()
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("validateExecutableGraphBetaRuntime error = %v, want ErrUnsupportedRuntime", err)
	}
	for _, want := range []string{"aggregate", `rule="two-aggregates"`, "multiple aggregate conditions"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unsupported runtime error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestAccumulateModifyUnobservedMemberSlotRefreshesAggregateMemory(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "record",
		BindingReads: &ActionBindingReadSetSpec{},
		Fn: func(ctx ActionContext) error {
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, Fields{"count": count, "total": total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "total",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-unobserved-member-modify-session")
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3, "note": "first"})); err != nil {
		t.Fatalf("assert first: %v", err)
	}
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5, "note": "old"}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(context.Background()); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("note modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal removals after note modify = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal additions after note modify = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal updates after note modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply note delta: %v", err)
	} else if !ok {
		t.Fatal("apply note delta unexpectedly skipped")
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 0; got != want {
		t.Fatalf("modify fast-path skips after note modify = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after note modify = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"amount": 1}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate amount: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("amount modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after amount modify = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal additions after amount modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply amount delta: %v", err)
	} else if !ok {
		t.Fatal("apply amount delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	run, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 2)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 4)) {
		t.Fatalf("run fired/row = %d/%v, want 1/count=2 total=4", run.Fired, observed[len(observed)-1])
	}

	snapshot = session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after amount modify = %d, want 0", got)
	}
}

func TestAccumulateMinAndMaxUseIncrementalAgendaDeltas(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			row := Fields{}
			for _, name := range []string{"min", "max"} {
				value, ok := ctx.BindingValue(name)
				if !ok {
					return errors.New("missing aggregate binding " + name)
				}
				row[name] = value
			}
			observed = append(observed, row)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "extrema",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
			Max(BindingFieldExpr{Binding: "item", Field: "amount"}).As("max"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-extrema-incremental-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental min/max aggregate agenda support", session.rete)
	}

	first, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	third, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "c", "amount": 1}))
	if err != nil {
		t.Fatalf("assert third: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 1, 5)

	if _, err := session.Modify(context.Background(), third.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 7})}); err != nil {
		t.Fatalf("modify third: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("second Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 3, 7)

	if _, err := session.Retract(context.Background(), third.Fact.ID()); err != nil {
		t.Fatalf("retract third: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 3, 5)

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("fourth Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fourth Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 5, 5)

	if _, err := session.Retract(context.Background(), second.Fact.ID()); err != nil {
		t.Fatalf("retract second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestAccumulateCollectUsesIncrementalAgendaDeltas(t *testing.T) {
	var observed []Value
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			collected, ok := ctx.BindingValue("collected")
			if !ok {
				return errors.New("missing collected binding")
			}
			observed = append(observed, collected)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "collected",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Collect(BindingFieldExpr{Binding: "item", Field: "amount"}).As("collected"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-collect-incremental-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental collect aggregate agenda support", session.rete)
	}

	first, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{mustValue(t, 3), mustValue(t, 5)})

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 1})}); err != nil {
		t.Fatalf("modify second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("second Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{mustValue(t, 3), mustValue(t, 1)})

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{mustValue(t, 1)})

	if _, err := session.Retract(context.Background(), second.Fact.ID()); err != nil {
		t.Fatalf("retract second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("fourth Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fourth Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{})
}

func TestAccumulateAfterOuterBindingUsesBucketedIncrementalAgenda(t *testing.T) {
	var observed []bucketedAggregateRow
	workspace := NewWorkspace()
	group := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			groupID, ok := ctx.BindingScalarValue("group", "id")
			if !ok {
				return errors.New("missing group id")
			}
			groupName, ok := groupID.AsString()
			if !ok {
				return errors.New("group id is not a string")
			}
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, bucketedAggregateRow{group: groupName, count: count, total: total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "group-totals",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group.Key())},
			Accumulate(
				Match{
					Binding: "item",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}, Target: TemplateKeyFact(item.Key()),
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "bucketed-aggregate-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want bucketed aggregate incremental agenda support", session.rete)
	}

	if _, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("assert group a: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert group b: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	first, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i1", "group": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert item i1: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i2", "group": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert item i2: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	start := len(observed)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("first Run fired = %d, want 2", result.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"a": {1, 3}, "b": {1, 5}})

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"group": "a", "amount": 2})}); err != nil {
		t.Fatalf("modify item i2: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract item i1: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	start = len(observed)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("second Run fired = %d, want 2", result.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"a": {1, 2}, "b": {0, 0}})
}

func TestAccumulateBucketReuseClearsNumericState(t *testing.T) {
	var observed []bucketedAggregateRow
	workspace := NewWorkspace()
	group := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			groupID, ok := ctx.BindingScalarValue("group", "id")
			if !ok {
				return errors.New("missing group id")
			}
			groupName, ok := groupID.AsString()
			if !ok {
				return errors.New("group id is not a string")
			}
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, bucketedAggregateRow{group: groupName, count: count, total: total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "group-totals",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group.Key())},
			Accumulate(
				Match{
					Binding: "item",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}, Target: TemplateKeyFact(item.Key()),
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "bucketed-aggregate-reuse-numeric-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want bucketed aggregate incremental agenda support", session.rete)
	}

	firstGroup, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "a"}))
	if err != nil {
		t.Fatalf("assert group a: %v", err)
	}
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i1", "group": "a", "amount": 10})); err != nil {
		t.Fatalf("assert item i1: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertBucketedAggregateRows(t, observed, map[string][2]int64{"a": {1, 10}})

	retract, delta, err := session.retractImmediate(context.Background(), firstGroup.Fact.ID(), mutationOrigin{})
	if err != nil {
		t.Fatalf("retract group a: %v", err)
	}
	if retract.Status != RetractRemoved {
		t.Fatalf("retract group a status = %v, want %v", retract.Status, RetractRemoved)
	}
	if got := len(delta.removed); got == 0 {
		t.Fatal("retract group a terminal removals = 0, want at least one")
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("retract group a terminal additions = %d, want 0", got)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply retract delta: %v", err)
	} else if !ok {
		t.Fatal("apply retract delta unexpectedly skipped")
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 0 {
		t.Fatalf("second Run fired = %d, want 0", result.Fired)
	}

	if _, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert group b: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	start := len(observed)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"b": {0, 0}})
}

func TestAccumulateBucketedModifyUnobservedMemberSlotRefreshesAggregateMemory(t *testing.T) {
	var observed []bucketedAggregateRow
	workspace := NewWorkspace()
	group := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		BindingReads: &ActionBindingReadSetSpec{Reads: []ActionBindingReadSpec{
			{Binding: "group", Field: "id"},
			{Binding: "count"},
			{Binding: "total"},
		}},
		Fn: func(ctx ActionContext) error {
			groupID, ok := ctx.BindingScalarValue("group", "id")
			if !ok {
				return errors.New("missing group id")
			}
			groupName, ok := groupID.AsString()
			if !ok {
				return errors.New("group id is not a string")
			}
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, bucketedAggregateRow{group: groupName, count: count, total: total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "group-totals",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group.Key())},
			Accumulate(
				Match{
					Binding: "item",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}, Target: TemplateKeyFact(item.Key()),
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "bucketed-aggregate-unobserved-member-modify-session")
	if _, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("assert group a: %v", err)
	}
	if _, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert group b: %v", err)
	}
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i1", "group": "a", "amount": 3, "note": "first"})); err != nil {
		t.Fatalf("assert item i1: %v", err)
	}
	second, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i2", "group": "b", "amount": 5, "note": "old"}))
	if err != nil {
		t.Fatalf("assert item i2: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(context.Background()); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("note modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal removals after note modify = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal additions after note modify = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal updates after note modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply note delta: %v", err)
	} else if !ok {
		t.Fatal("apply note delta unexpectedly skipped")
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 0; got != want {
		t.Fatalf("modify fast-path skips after note modify = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after note modify = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"amount": 1}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate amount: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("amount modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after amount modify = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal additions after amount modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply amount delta: %v", err)
	} else if !ok {
		t.Fatal("apply amount delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	start := len(observed)
	run, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if run.Fired != 2 {
		t.Fatalf("second Run fired = %d, want 2", run.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"a": {1, 3}, "b": {1, 1}})

	snapshot = session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after amount modify = %d, want 0", got)
	}
}

func TestAccumulateBucketedModifyUnobservedOuterSlotRefreshesAggregateMemory(t *testing.T) {
	var observed []bucketedAggregateRow
	workspace := NewWorkspace()
	group := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		BindingReads: &ActionBindingReadSetSpec{Reads: []ActionBindingReadSpec{
			{Binding: "group", Field: "id"},
			{Binding: "count"},
			{Binding: "total"},
		}},
		Fn: func(ctx ActionContext) error {
			groupID, ok := ctx.BindingScalarValue("group", "id")
			if !ok {
				return errors.New("missing group id")
			}
			groupName, ok := groupID.AsString()
			if !ok {
				return errors.New("group id is not a string")
			}
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, bucketedAggregateRow{group: groupName, count: count, total: total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "group-totals",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group.Key())},
			Accumulate(
				Match{
					Binding: "item",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}, Target: TemplateKeyFact(item.Key()),
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "bucketed-aggregate-unobserved-outer-modify-session")
	if _, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "a", "note": "first"})); err != nil {
		t.Fatalf("assert group a: %v", err)
	}
	secondGroup, err := session.Assert(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "b", "note": "old"}))
	if err != nil {
		t.Fatalf("assert group b: %v", err)
	}
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i1", "group": "a", "amount": 3})); err != nil {
		t.Fatalf("assert item i1: %v", err)
	}
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i2", "group": "b", "amount": 5})); err != nil {
		t.Fatalf("assert item i2: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(context.Background()); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(context.Background(), secondGroup.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("note modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after note modify = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal additions after note modify = %d, want %d", got, want)
	}
	if got, want := len(delta.updated), 0; got != want {
		t.Fatalf("terminal updates after note modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply note delta: %v", err)
	} else if !ok {
		t.Fatal("apply note delta unexpectedly skipped")
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations after note modify = %d, want %d", got, want)
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 0; got != want {
		t.Fatalf("modify fast-path skips after note modify = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after note modify = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(context.Background(), secondGroup.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"id": "c"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate id: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("id modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after id modify = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal additions after id modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(context.Background(), delta); err != nil {
		t.Fatalf("apply id delta: %v", err)
	} else if !ok {
		t.Fatal("apply id delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	start := len(observed)
	run, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Fired != 2 {
		t.Fatalf("Run fired = %d, want 2", run.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"a": {1, 3}, "c": {0, 0}})

	snapshot = session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after id modify = %d, want 0", got)
	}
}

func TestAccumulateResultFeedsDownstreamJoinIncrementally(t *testing.T) {
	var observed []int64
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	gate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "gate",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "expected", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			observed = append(observed, count.intValue)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "count-gate",
		ConditionTree: And{Conditions: []ConditionSpec{
			Accumulate(
				Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
				Count().As("count"),
			),
			Match{
				Binding: "gate",

				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "expected"},
						Right:    BindingValueExpr{Binding: "count"},
					},
				}, Target: TemplateKeyFact(gate.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-downstream-join-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want downstream aggregate incremental agenda support", session.rete)
	}

	if _, err := session.Assert(context.Background(), gate.Key(), mustFields(t, map[string]any{"expected": 2})); err != nil {
		t.Fatalf("assert gate 2: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("assert item a: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert item b: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 || observed[len(observed)-1] != 2 {
		t.Fatalf("first Run fired/observed = %d/%v, want 1/2", result.Fired, observed)
	}

	if _, err := session.Assert(context.Background(), gate.Key(), mustFields(t, map[string]any{"expected": 3})); err != nil {
		t.Fatalf("assert gate 3: %v", err)
	}
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "c"})); err != nil {
		t.Fatalf("assert item c: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 || observed[len(observed)-1] != 3 {
		t.Fatalf("second Run fired/observed = %d/%v, want 1/3", result.Fired, observed)
	}
}

func TestAccumulateSharedInputRulesUseIncrementalAgendaDeltas(t *testing.T) {
	var observed []string
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	for _, name := range []string{"record-a", "record-b"} {
		actionName := name
		mustAddAction(t, workspace, ActionSpec{
			Name: actionName,
			Fn: func(ctx ActionContext) error {
				count, ok := ctx.BindingValue("count")
				if !ok {
					return errors.New("missing count binding")
				}
				observed = append(observed, actionName+":"+count.String())
				return nil
			},
		})
	}
	for _, spec := range []struct {
		rule   string
		action string
	}{
		{rule: "shared-a", action: "record-a"},
		{rule: "shared-b", action: "record-b"},
	} {
		mustAddRule(t, workspace, RuleSpec{
			Name: spec.rule,
			ConditionTree: Accumulate(
				Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
				Count().As("count"),
			),
			Actions: []RuleActionSpec{{Name: spec.action}},
		})
	}
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-shared-input-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want shared aggregate incremental agenda support", session.rete)
	}

	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("assert item a: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert item b: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 || len(observed) != 2 {
		t.Fatalf("Run fired/observed = %d/%v, want 2/two rows", result.Fired, observed)
	}
}

func TestAccumulateSumHandlesDuplicateFactsAndNumericTransitionsIncrementally(t *testing.T) {
	var observed []Value
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueAny, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, total)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "sum",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-numeric-transition-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want numeric aggregate incremental agenda support", session.rete)
	}

	first, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 2}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	duplicate, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 2}))
	if err != nil {
		t.Fatalf("assert duplicate: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	floaty, err := session.Assert(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "c", "amount": 1.5}))
	if err != nil {
		t.Fatalf("assert floaty: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1].Equal(mustValue(t, 5.5)) {
		t.Fatalf("first Run fired/total = %d/%v, want 1/5.5", result.Fired, observed[len(observed)-1])
	}

	if _, err := session.Modify(context.Background(), floaty.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 3})}); err != nil {
		t.Fatalf("modify floaty: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1].Equal(mustValue(t, 7)) {
		t.Fatalf("second Run fired/total = %d/%v, want 1/7", result.Fired, observed[len(observed)-1])
	}

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.Retract(context.Background(), duplicate.Fact.ID()); err != nil {
		t.Fatalf("retract duplicate: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1].Equal(mustValue(t, 3)) {
		t.Fatalf("third Run fired/total = %d/%v, want 1/3", result.Fired, observed[len(observed)-1])
	}
}

func TestAccumulateValidationRejectsUnsupportedShapesAndCollisions(t *testing.T) {
	item := TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}}
	cases := []struct {
		name string
		tree ConditionSpec
	}{
		{
			name: "or input",
			tree: Accumulate(Or{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(TemplateKey("item"))},
				Match{Binding: "b", Target: TemplateKeyFact(TemplateKey("item"))},
			}}, Count().As("count")),
		},
		{
			name: "result collision",
			tree: And{Conditions: []ConditionSpec{
				Match{Binding: "count", Target: TemplateKeyFact(TemplateKey("item"))},
				Accumulate(Match{Binding: "item", Target: TemplateKeyFact(TemplateKey("item"))}, Count().As("count")),
			}},
		},
		{
			name: "accumulate inside or",
			tree: Or{Conditions: []ConditionSpec{
				Accumulate(Match{Binding: "item", Target: TemplateKeyFact(TemplateKey("item"))}, Count().As("count")),
				Match{Binding: "other", Target: TemplateKeyFact(TemplateKey("item"))},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			mustAddTemplate(t, workspace, item)
			mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, RuleSpec{
				Name:          "bad",
				ConditionTree: tc.tree,
				Actions:       []RuleActionSpec{{Name: "record"}},
			})
			_, err := workspace.Compile(context.Background())
			if !errors.Is(err, ErrAggregateValidation) {
				t.Fatalf("Compile error = %v, want ErrAggregateValidation", err)
			}
		})
	}
}

func assertAggregateRow(t *testing.T, row Fields, count, sum, min, max int64, collected []Value) {
	t.Helper()
	wants := map[string]Value{
		"count": mustValue(t, count),
		"sum":   mustValue(t, sum),
		"min":   mustValue(t, min),
		"max":   mustValue(t, max),
	}
	for key, want := range wants {
		if got := row[key]; !got.Equal(want) {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
	gotCollected := row["collected"]
	wantCollected := mustValue(t, collected)
	if !gotCollected.Equal(wantCollected) {
		t.Fatalf("collected = %v, want %v", gotCollected, wantCollected)
	}
}

func assertExtremaRow(t *testing.T, row Fields, min, max int64) {
	t.Helper()
	if got := row["min"]; !got.Equal(mustValue(t, min)) {
		t.Fatalf("min = %v, want %d", got, min)
	}
	if got := row["max"]; !got.Equal(mustValue(t, max)) {
		t.Fatalf("max = %v, want %d", got, max)
	}
}

func assertCollectedValue(t *testing.T, got Value, want []Value) {
	t.Helper()
	expected := mustValue(t, want)
	if !got.Equal(expected) {
		t.Fatalf("collected = %v, want %v", got, expected)
	}
}

func TestAggregateBucketTableRecyclesRows(t *testing.T) {
	firstKey := graphTokenIdentityKey{size: 1, generation: 1, identityState: 11}
	table := reteGraphAggregateBucketTable{
		rows: []reteGraphAggregateBucket{{id: 0}},
		ids:  map[graphTokenIdentityKey]reteGraphAggregateBucketID{firstKey: 0},
		live: 1,
	}

	bucket, ok := table.get(firstKey)
	if !ok {
		t.Fatal("initial bucket lookup failed")
	}
	removed, ok := table.remove(firstKey)
	if !ok || removed != bucket {
		t.Fatalf("remove = %#v, %v; want original bucket", removed, ok)
	}
	table.recycle(removed)
	if got, want := table.len(), 0; got != want {
		t.Fatalf("live buckets after recycle = %d, want %d", got, want)
	}
	reused := table.bucketForParent(tokenRef{})
	if reused != bucket {
		t.Fatalf("reused bucket = %#v, want original %#v", reused, bucket)
	}
	if got, want := table.len(), 1; got != want {
		t.Fatalf("live buckets after reuse = %d, want %d", got, want)
	}

	table.clear()
	if got, want := table.len(), 0; got != want {
		t.Fatalf("live buckets after clear = %d, want %d", got, want)
	}
	if got, want := len(table.free), len(table.rows); got != want {
		t.Fatalf("free rows after clear = %d, want %d", got, want)
	}
}

type bucketedAggregateRow struct {
	group string
	count Value
	total Value
}

func assertBucketedAggregateRows(t *testing.T, rows []bucketedAggregateRow, want map[string][2]int64) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %d rows", rows, len(want))
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		values, ok := want[row.group]
		if !ok {
			t.Fatalf("unexpected aggregate row for group %q: %v", row.group, row)
		}
		if seen[row.group] {
			t.Fatalf("duplicate aggregate row for group %q", row.group)
		}
		seen[row.group] = true
		if !row.count.Equal(mustValue(t, values[0])) || !row.total.Equal(mustValue(t, values[1])) {
			t.Fatalf("group %q row = count %v total %v, want count %d total %d", row.group, row.count, row.total, values[0], values[1])
		}
	}
	for group := range want {
		if !seen[group] {
			t.Fatalf("missing aggregate row for group %q", group)
		}
	}
}
