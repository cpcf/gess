package gess

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSnapshotQueryReturnsDeterministicParameterizedRows(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustQueryRevision(t)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})},
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p2", "dept": "sales", "age": 41})},
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p3", "dept": "engineering", "age": 17})},
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p4", "dept": "engineering", "age": 29})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	snapshot := mustSnapshot(t, ctx, session)

	rows, err := snapshot.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	assertQueryRowStringValue(t, rows[0], "id", "p1")
	assertQueryRowStringValue(t, rows[1], "id", "p4")
	assertQueryRowStringValue(t, rows[0], "requested_dept", "engineering")
	if fact, ok := rows[0].Fact("person"); !ok || fact.TemplateKey() != personKey {
		t.Fatalf("row fact = (%#v, %v), want person fact", fact, ok)
	}
	if aliases := rows[0].Aliases(); len(aliases) != 3 || aliases[0] != "person" || aliases[1] != "id" || aliases[2] != "requested_dept" {
		t.Fatalf("aliases = %#v", aliases)
	}
}

func TestSessionQueryDoesNotFireRulesOrEmitFactEvents(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error {
		fired++
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "ordinary-rule",
		Conditions: []RuleConditionSpec{{
			Binding:     "p",
			TemplateKey: person.Key(),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddAdultQuery(t, workspace, person.Key())
	revision := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(revision,
		WithEventListener(collector),
		WithInitialFacts(SessionInitialFact{TemplateKey: person.Key(), Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if fired != 0 {
		t.Fatalf("rule action fired during query: %d", fired)
	}
	if events := collector.Events(); len(events) != 0 {
		t.Fatalf("query emitted events: %#v", events)
	}
}

func TestSessionQueryInitializesGraphTerminalMemoryForQueryOnlyRuleset(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustQueryRevision(t)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatal("query-only session did not initialize graph beta memory")
	}
	if got := len(revision.graph.queryTerminalIDs["adults-by-dept"]); got == 0 {
		t.Fatal("query graph terminal was not compiled")
	}

	rows, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

func TestSessionJoinedQueryModifyUnobservedSlotRefreshesGraphMemory(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey := mustJoinedQueryModifyRevision(t)
	session := mustSession(t, revision, "joined-query-modify-fast-path-session")
	employee, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{
		"name": "Ada",
		"dept": "Engineering",
		"note": "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate department: %v", err)
	}
	rows, err := session.QueryAll(ctx, "employees-by-dept", QueryArgs{"dept": "Engineering"})
	if err != nil {
		t.Fatalf("QueryAll before modify: %v", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("rows before modify = %d, want %d", got, want)
	}
	assertQueryRowStringValue(t, rows[0], "note", "old")

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, employee.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got := len(delta.updated); got != 0 {
		t.Fatalf("rule terminal token updates = %d, want 0 for query terminal", got)
	}
	rows, err = session.QueryAll(ctx, "employees-by-dept", QueryArgs{"dept": "Engineering"})
	if err != nil {
		t.Fatalf("QueryAll after note modify: %v", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("rows after note modify = %d, want %d", got, want)
	}
	assertQueryRowStringValue(t, rows[0], "note", "new")

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
}

func TestSessionJoinedQueryModifyJoinKeyFallsBackAndRetractsRow(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey := mustJoinedQueryModifyRevision(t)
	session := mustSession(t, revision, "joined-query-modify-join-key-session")
	employee, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{
		"name": "Ada",
		"dept": "Engineering",
		"note": "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate department: %v", err)
	}
	rows, err := session.QueryAll(ctx, "employees-by-dept", QueryArgs{"dept": "Engineering"})
	if err != nil {
		t.Fatalf("QueryAll before modify: %v", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("rows before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, _, err := session.modifyImmediate(ctx, employee.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"dept": "Research"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate dept: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	rows, err = session.QueryAll(ctx, "employees-by-dept", QueryArgs{"dept": "Engineering"})
	if err != nil {
		t.Fatalf("QueryAll after dept modify: %v", err)
	}
	if got := len(rows); got != 0 {
		t.Fatalf("rows after join-key modify = %d, want 0", got)
	}

	snapshot := session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathSkips; got != 0 {
		t.Fatalf("modify fast-path skips = %d, want 0", got)
	}
	if got, want := snapshot.Totals.ModifyFastPathFallbacks, 1; got != want {
		t.Fatalf("modify fast-path fallbacks = %d, want %d", got, want)
	}
}

func TestQueryIteratorCancellationReturnsNoPartialAllResults(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustQueryRevision(t)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	iterator, err := session.Query(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err := iterator.All(canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("All error = %v, want context.Canceled", err)
	}
	if rows != nil {
		t.Fatalf("rows = %#v, want nil on cancellation", rows)
	}
}

func TestQueryRetainsDuplicateReturnValuesFromDistinctBranchTokens(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "matching-people",
		ConditionTree: Or{Conditions: []ConditionSpec{
			Match{
				Binding:     "p",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
			},
			Match{
				Binding:     "p",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "dept", Operator: FieldConstraintEqual, Value: "engineering"},
				},
			},
		}},
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: person.Key(), Fields: mustFields(t, map[string]any{"id": "p1", "active": true, "dept": "engineering"})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "matching-people", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want duplicate branch rows retained", len(rows))
	}
	assertQueryRowStringValue(t, rows[0], "id", "p1")
	assertQueryRowStringValue(t, rows[1], "id", "p1")
}

func TestSessionQueryValueOnlyRowsUseProjectedValueStorageAndRemainStable(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "people-values",
		ConditionTree: Match{
			Binding:     "p",
			TemplateKey: person.Key(),
		},
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
			ReturnValue("dept", BindingFieldExpr{Binding: "p", Field: "dept"}),
			ReturnValue("age", BindingFieldExpr{Binding: "p", Field: "age"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	query, ok := revision.query("people-values")
	if !ok {
		t.Fatal("compiled query missing")
	}
	for _, ret := range query.returns {
		if ret.fact || ret.projection.kind != compiledQueryReturnProjectionBindingField {
			t.Fatalf("return %q projection = (%v, fact %v), want binding-field value projection", ret.alias, ret.projection.kind, ret.fact)
		}
	}

	initials := make([]SessionInitialFact, 150)
	for i := range initials {
		initials[i] = SessionInitialFact{
			TemplateKey: person.Key(),
			Fields: mustFields(t, map[string]any{
				"id":   fmt.Sprintf("p-%03d", i),
				"dept": "engineering",
				"age":  20 + i,
			}),
		}
	}
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "people-values", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if got, want := len(rows), len(initials); got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if session.rete == nil || session.rete.graphBeta == nil || session.rete.graphBeta.queryArena == nil {
		t.Fatal("query did not use graph beta terminal memory")
	}
	if got := session.rete.graphBeta.queryArena.rowCount(); got >= len(rows) {
		t.Fatalf("query arena rows = %d, result rows = %d; want terminal projection without per-result query token copies", got, len(rows))
	}
	if session.rete.graphBeta.queryArena.keepFactSpans || cap(session.rete.graphBeta.queryArena.factIDs) != 0 || cap(session.rete.graphBeta.queryArena.factVersions) != 0 {
		t.Fatal("query arena should use compact parent-linked rows without fact span caches")
	}
	if len(rows[0].items) != 0 || len(rows[0].valueItems) != 3 {
		t.Fatalf("row storage = items %d valueItems %d, want value-only projected storage", len(rows[0].items), len(rows[0].valueItems))
	}
	if _, ok := rows[0].Fact("id"); ok {
		t.Fatal("value return resolved as fact")
	}
	assertQueryRowStringValue(t, rows[0], "id", "p-000")
	assertQueryRowStringValue(t, rows[149], "id", "p-149")

	again, err := session.QueryAll(ctx, "people-values", nil)
	if err != nil {
		t.Fatalf("second QueryAll: %v", err)
	}
	if got, want := len(again), len(initials); got != want {
		t.Fatalf("second rows = %d, want %d", got, want)
	}
	assertQueryRowStringValue(t, rows[0], "id", "p-000")
	assertQueryRowStringValue(t, again[0], "id", "p-000")
}

func TestQueryAggregateReturnsParameterizedValuesAndTracksUpdates(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "item-total-by-dept",
		Parameters: []QueryParameterSpec{{Name: "dept", Kind: ValueString}},
		ConditionTree: Accumulate(
			Match{
				Binding:     "item",
				TemplateKey: item.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentPath(Path("dept")),
						Right:    ParamExpr{Name: "dept"},
					},
				},
			},
			Count().As("count"),
			Sum(BindingPath("item", Path("amount"))).As("total"),
		),
		Returns: []QueryReturnSpec{
			ReturnValue("count", BindingValueExpr{Binding: "count"}),
			ReturnValue("total", BindingValueExpr{Binding: "total"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "i1", "dept": "engineering", "amount": 2})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "i2", "dept": "engineering", "amount": 3})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "i3", "dept": "sales", "amount": 7})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "item-total-by-dept", QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("QueryAll engineering: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("engineering rows = %d, want 1", len(rows))
	}
	assertQueryRowIntValue(t, rows[0], "count", 2)
	assertQueryRowIntValue(t, rows[0], "total", 5)

	snapshot := mustSnapshot(t, ctx, session)
	snapshotRows, err := snapshot.QueryAll(ctx, "item-total-by-dept", QueryArgs{"dept": "sales"})
	if err != nil {
		t.Fatalf("snapshot QueryAll sales: %v", err)
	}
	if len(snapshotRows) != 1 {
		t.Fatalf("sales snapshot rows = %d, want 1", len(snapshotRows))
	}
	assertQueryRowIntValue(t, snapshotRows[0], "count", 1)
	assertQueryRowIntValue(t, snapshotRows[0], "total", 7)

	var target FactID
	for _, fact := range snapshot.FactsByName("item") {
		if id, _ := fact.Field("id"); id.Equal(mustValue(t, "i2")) {
			target = fact.ID()
			break
		}
	}
	if target.IsZero() {
		t.Fatal("missing target item")
	}
	if _, err := session.Modify(ctx, target, FactPatch{Set: mustFields(t, map[string]any{"amount": 6})}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	rows, err = session.QueryAll(ctx, "item-total-by-dept", QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("QueryAll engineering after modify: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("engineering rows after modify = %d, want 1", len(rows))
	}
	assertQueryRowIntValue(t, rows[0], "count", 2)
	assertQueryRowIntValue(t, rows[0], "total", 8)
}

func TestQueryAggregateCountReturnsEmptyParameterizedBucket(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "item",
		Fields: []FieldSpec{{Name: "dept", Kind: ValueString, Required: true}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "item-count-by-dept",
		Parameters: []QueryParameterSpec{{Name: "dept", Kind: ValueString}},
		ConditionTree: Accumulate(
			Match{
				Binding:     "item",
				TemplateKey: item.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentPath(Path("dept")),
						Right:    ParamExpr{Name: "dept"},
					},
				},
			},
			Count().As("count"),
		),
		Returns: []QueryReturnSpec{
			ReturnValue("count", BindingValueExpr{Binding: "count"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "empty-query-aggregate-session")
	rows, err := session.QueryAll(ctx, "item-count-by-dept", QueryArgs{"dept": "support"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 empty aggregate bucket", len(rows))
	}
	assertQueryRowIntValue(t, rows[0], "count", 0)
}

func TestQueryAggregateGroupsByOuterBinding(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	group := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "group",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "kind", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "group-id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:       "totals-by-kind",
		Parameters: []QueryParameterSpec{{Name: "kind", Kind: ValueString}},
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding:     "group",
				TemplateKey: group.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentPath(Path("kind")),
						Right:    ParamExpr{Name: "kind"},
					},
				},
			},
			Accumulate(
				Match{
					Binding:     "item",
					TemplateKey: item.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "group-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					},
				},
				Count().As("count"),
				Sum(BindingPath("item", Path("amount"))).As("total"),
			),
		}},
		Returns: []QueryReturnSpec{
			ReturnValue("group_id", BindingPath("group", Path("id"))),
			ReturnValue("count", BindingValueExpr{Binding: "count"}),
			ReturnValue("total", BindingValueExpr{Binding: "total"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: group.Key(), Fields: mustFields(t, map[string]any{"id": "g1", "kind": "active"})},
		SessionInitialFact{TemplateKey: group.Key(), Fields: mustFields(t, map[string]any{"id": "g2", "kind": "active"})},
		SessionInitialFact{TemplateKey: group.Key(), Fields: mustFields(t, map[string]any{"id": "g3", "kind": "archived"})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"group-id": "g1", "amount": 2})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"group-id": "g1", "amount": 3})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"group-id": "g2", "amount": 7})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"group-id": "g3", "amount": 11})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "totals-by-kind", QueryArgs{"kind": "active"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 grouped aggregate rows", len(rows))
	}
	assertQueryAggregateGroupRow(t, rows, "g1", 2, 5)
	assertQueryAggregateGroupRow(t, rows, "g2", 1, 7)
}

func TestQueryAggregateResultFeedsDownstreamCondition(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "item",
		Fields: []FieldSpec{{Name: "amount", Kind: ValueInt, Required: true}},
	})
	gate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "gate",
		Fields: []FieldSpec{{Name: "count", Kind: ValueInt, Required: true}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "count-gates",
		ConditionTree: And{Conditions: []ConditionSpec{
			Accumulate(
				Match{Binding: "item", TemplateKey: item.Key()},
				Count().As("count"),
			),
			Match{
				Binding:     "gate",
				TemplateKey: gate.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentPath(Path("count")),
						Right:    BindingValueExpr{Binding: "count"},
					},
				},
			},
		}},
		Returns: []QueryReturnSpec{
			ReturnValue("count", BindingValueExpr{Binding: "count"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session, err := NewSession(mustCompileWorkspace(t, workspace), WithInitialFacts(
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"amount": 1})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"amount": 2})},
		SessionInitialFact{TemplateKey: gate.Key(), Fields: mustFields(t, map[string]any{"count": 1})},
		SessionInitialFact{TemplateKey: gate.Key(), Fields: mustFields(t, map[string]any{"count": 2})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "count-gates", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 downstream aggregate row", len(rows))
	}
	assertQueryRowIntValue(t, rows[0], "count", 2)
}

func TestQueryAggregateMinMaxCollectReturnsValues(t *testing.T) {
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
	if err := workspace.AddQuery(QuerySpec{
		Name: "item-extrema",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Min(BindingPath("item", Path("amount"))).As("min"),
			Max(BindingPath("item", Path("amount"))).As("max"),
			Collect(BindingPath("item", Path("amount"))).As("collected"),
		),
		Returns: []QueryReturnSpec{
			ReturnValue("min", BindingValueExpr{Binding: "min"}),
			ReturnValue("max", BindingValueExpr{Binding: "max"}),
			ReturnValue("collected", BindingValueExpr{Binding: "collected"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session, err := NewSession(mustCompileWorkspace(t, workspace), WithInitialFacts(
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "a", "amount": 3})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "b", "amount": 1})},
		SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "c", "amount": 5})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "item-extrema", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 extrema aggregate row", len(rows))
	}
	assertQueryRowIntValue(t, rows[0], "min", 1)
	assertQueryRowIntValue(t, rows[0], "max", 5)
	assertQueryRowListValue(t, rows[0], "collected", []Value{mustValue(t, 3), mustValue(t, 1), mustValue(t, 5)})
}

func TestQueryAggregateValidationRejectsUnsupportedShapes(t *testing.T) {
	workspace := NewWorkspace()
	mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}})
	if err := workspace.AddQuery(QuerySpec{
		Name: "unsupported-aggregate-query",
		ConditionTree: Accumulate(Or{Conditions: []ConditionSpec{
			Match{Binding: "a", TemplateKey: TemplateKey("item")},
			Match{Binding: "b", TemplateKey: TemplateKey("item")},
		}}, Count().As("count")),
		Returns: []QueryReturnSpec{
			ReturnValue("count", BindingValueExpr{Binding: "count"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	if _, err := workspace.Compile(context.Background()); !errors.Is(err, ErrAggregateValidation) {
		t.Fatalf("Compile error = %v, want ErrAggregateValidation", err)
	}
}

func TestSessionQueryFactReturnRowsDetachFactsLazilyAndRemainStable(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "people-facts",
		ConditionTree: Match{
			Binding:     "p",
			TemplateKey: person.Key(),
		},
		Returns: []QueryReturnSpec{
			ReturnFact("person", "p"),
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: person.Key(),
		Fields:      mustFields(t, map[string]any{"id": "p-001", "dept": "engineering"}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "people-facts", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if len(rows[0].items) != 1 || len(rows[0].valueItems) != 1 || rows[0].items[0].fact == nil || rows[0].items[0].fact.ref.ID().IsZero() {
		t.Fatalf("row storage = %#v/%#v, want compact lazy fact and value items", rows[0].items, rows[0].valueItems)
	}
	owner := rows[0].items[0].fact.owner
	if owner == nil || owner.facts != nil {
		t.Fatalf("row owner cache before Fact = %#v, want empty lazy owner", owner)
	}
	assertQueryRowStringValue(t, rows[0], "id", "p-001")
	if owner.facts != nil {
		t.Fatalf("row owner cache after value read = %#v, want no fact materialization", owner.facts)
	}
	fact, ok := rows[0].Fact("person")
	if !ok {
		t.Fatal("Fact(person) did not resolve")
	}
	if got, ok := fact.Field("dept"); !ok || got.Kind() != ValueString || got.stringValue != "engineering" {
		t.Fatalf("Fact(person).dept = %#v, %v; want engineering", got, ok)
	}
	if owner.facts == nil {
		t.Fatal("row owner cache was not populated by Fact access")
	}

	if _, err := session.Modify(ctx, fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"dept": "sales"})}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	stable, ok := rows[0].Fact("person")
	if !ok {
		t.Fatal("Fact(person) after modify did not resolve")
	}
	if got, ok := stable.Field("dept"); !ok || got.Kind() != ValueString || got.stringValue != "engineering" {
		t.Fatalf("cached Fact(person).dept after modify = %#v, %v; want engineering", got, ok)
	}
}

func TestQueryValidationAndArgumentsFailPrecisely(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "bad-query",
		Parameters: []QueryParameterSpec{
			{Name: "missing", Kind: ValueString},
		},
		ConditionTree: Match{
			Binding:     "p",
			TemplateKey: person.Key(),
			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "id"}, Right: ParamExpr{Name: "unknown"}},
			},
		},
		Returns: []QueryReturnSpec{ReturnFact("person", "p")},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	if _, err := workspace.Compile(context.Background()); !errors.Is(err, ErrQueryValidation) {
		t.Fatalf("Compile error = %v, want ErrQueryValidation", err)
	}

	revision, personKey := mustQueryRevision(t)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.QueryAll(context.Background(), "missing-query", nil); !errors.Is(err, ErrQueryNotFound) {
		t.Fatalf("missing query error = %v, want ErrQueryNotFound", err)
	}
	if _, err := session.QueryAll(context.Background(), "adults-by-dept", QueryArgs{"dept": 1}); !errors.Is(err, ErrQueryArgument) {
		t.Fatalf("argument error = %v, want ErrQueryArgument", err)
	}
}

func TestSessionQueryDuringRunFailsConcurrencyMisuse(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	mustAddAction(t, workspace, ActionSpec{Name: "block", Fn: func(ActionContext) error {
		close(entered)
		<-release
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "blocking-rule",
		Conditions: []RuleConditionSpec{{
			Binding:     "p",
			TemplateKey: person.Key(),
		}},
		Actions: []RuleActionSpec{{Name: "block"}},
	})
	mustAddAdultQuery(t, workspace, person.Key())
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: person.Key(), Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, err := session.Run(ctx)
		runDone <- err
	}()
	<-entered
	if _, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"}); !errors.Is(err, ErrConcurrencyMisuse) {
		t.Fatalf("QueryAll during run error = %v, want ErrConcurrencyMisuse", err)
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func mustQueryRevision(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAdultQuery(t, workspace, person.Key())
	return mustCompileWorkspace(t, workspace), person.Key()
}

func mustAddAdultQuery(t testing.TB, workspace *Workspace, personKey TemplateKey) {
	t.Helper()
	if err := workspace.AddQuery(QuerySpec{
		Name: "adults-by-dept",
		Parameters: []QueryParameterSpec{
			{Name: "dept", Kind: ValueString},
		},
		ConditionTree: Match{
			Binding:     "p",
			TemplateKey: personKey,
			Predicates: []ExpressionSpec{
				CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: "dept"},
					Right:    ParamExpr{Name: "dept"},
				},
				CompareExpr{
					Operator: ExpressionCompareGreaterOrEqual,
					Left:     CurrentFieldExpr{Field: "age"},
					Right:    ConstExpr{Value: 18},
				},
			},
		},
		Returns: []QueryReturnSpec{
			ReturnFact("person", "p"),
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
			ReturnValue("requested_dept", ParamExpr{Name: "dept"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
}

func mustJoinedQueryModifyRevision(t testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "employee",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "employees-by-dept",
		Parameters: []QueryParameterSpec{
			{Name: "dept", Kind: ValueString},
		},
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding:     "employee",
				TemplateKey: employee.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "dept"},
						Right:    ParamExpr{Name: "dept"},
					},
				},
			},
			Match{
				Binding:     "department",
				TemplateKey: department.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "id"},
						Right:    BindingFieldExpr{Binding: "employee", Field: "dept"},
					},
				},
			},
		}},
		Returns: []QueryReturnSpec{
			ReturnFact("employee", "employee"),
			ReturnValue("name", BindingFieldExpr{Binding: "employee", Field: "name"}),
			ReturnValue("note", BindingFieldExpr{Binding: "employee", Field: "note"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	return mustCompileWorkspace(t, workspace), employee.Key(), department.Key()
}

func assertQueryRowStringValue(t testing.TB, row QueryRow, alias, want string) {
	t.Helper()
	value, ok := row.Value(alias)
	if !ok {
		t.Fatalf("row value %q missing", alias)
	}
	got, ok := value.AsString()
	if !ok || got != want {
		t.Fatalf("row value %q = (%q, %v), want %q", alias, got, ok, want)
	}
}

func assertQueryRowIntValue(t testing.TB, row QueryRow, alias string, want int64) {
	t.Helper()
	value, ok := row.Value(alias)
	if !ok {
		t.Fatalf("row value %q missing", alias)
	}
	got, ok := value.AsInt64()
	if !ok || got != want {
		t.Fatalf("row value %q = (%d, %v), want %d", alias, got, ok, want)
	}
}

func assertQueryAggregateGroupRow(t testing.TB, rows []QueryRow, groupID string, count, total int64) {
	t.Helper()
	for _, row := range rows {
		value, ok := row.Value("group_id")
		if !ok {
			t.Fatalf("row value group_id missing")
		}
		gotGroup, ok := value.AsString()
		if !ok {
			t.Fatalf("row group_id kind = %q, want string", value.Kind())
		}
		if gotGroup != groupID {
			continue
		}
		assertQueryRowIntValue(t, row, "count", count)
		assertQueryRowIntValue(t, row, "total", total)
		return
	}
	t.Fatalf("missing aggregate group row %q in %#v", groupID, rows)
}

func assertQueryRowListValue(t testing.TB, row QueryRow, alias string, want []Value) {
	t.Helper()
	value, ok := row.Value(alias)
	if !ok {
		t.Fatalf("row value %q missing", alias)
	}
	if value.Kind() != ValueList {
		t.Fatalf("row value %q kind = %q, want list", alias, value.Kind())
	}
	got := value.data.([]Value)
	if len(got) != len(want) {
		t.Fatalf("row value %q length = %d, want %d: %#v", alias, len(got), len(want), got)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatalf("row value %q[%d] = %v, want %v", alias, i, got[i], want[i])
		}
	}
}
