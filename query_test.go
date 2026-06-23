package gess

import (
	"context"
	"errors"
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
