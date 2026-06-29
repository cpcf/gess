package engine

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestListPatternBindsSegmentsForActionsAndActivationIdentity(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tags", Kind: ValueList, Required: true},
		},
	})
	var captured []Value
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			value, ok := ctx.BindingValue("middle")
			if !ok {
				return ErrMatcher
			}
			captured = append(captured, value)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "vip-active",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{
				ListPattern(Path("tags"),
					ListElem(ConstExpr{Value: "vip"}),
					ListSegment("middle"),
					ListElem(ConstExpr{Value: "active"}),
				),
			}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	first, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{
		"id":   "e1",
		"tags": []any{"vip", "blue", "gold", "active"},
	}))
	if err != nil {
		t.Fatalf("AssertTemplate first: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{
		"id":   "e2",
		"tags": []any{"vip", "active"},
	})); err != nil {
		t.Fatalf("AssertTemplate second: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{
		"id":   "e3",
		"tags": []any{"vip", "blue"},
	})); err != nil {
		t.Fatalf("AssertTemplate miss: %v", err)
	}

	pending := session.agenda.pendingActivations()
	if len(pending) != 2 {
		t.Fatalf("pending activations = %d, want 2", len(pending))
	}
	before := activationForFactID(t, pending, first.Fact.ID()).id

	if _, err := session.Modify(ctx, first.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{
		"tags": []any{"vip", "green", "active"},
	})}); err != nil {
		t.Fatalf("Modify first tags: %v", err)
	}
	afterPending := session.agenda.pendingActivations()
	after := activationForFactID(t, afterPending, first.Fact.ID()).id
	if before == after {
		t.Fatalf("activation ID did not change after segment binding value changed: %q", before)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("fired = %d, want 2", result.Fired)
	}
	assertCapturedListValue(t, captured, []any{"green"})
	assertCapturedListValue(t, captured, []any{})
}

func TestListPatternModifyUnobservedSlotRefreshesActivation(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tags", Kind: ValueList, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	var captured []Value
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		BindingReads: &ActionBindingReadSetSpec{Reads: []ActionBindingReadSpec{
			{Binding: "middle"},
		}},
		Fn: func(ctx ActionContext) error {
			value, ok := ctx.BindingValue("middle")
			if !ok {
				return ErrMatcher
			}
			captured = append(captured, value)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "vip-active",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{
				ListPattern(Path("tags"),
					ListElem(ConstExpr{Value: "vip"}),
					ListSegment("middle"),
					ListElem(ConstExpr{Value: "active"}),
				),
			}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "list-pattern-unobserved-modify-session")
	inserted, err := session.AssertTemplate(ctx, event.Key(), mustFields(t, map[string]any{
		"id":   "e1",
		"tags": []any{"vip", "blue", "gold", "active"},
		"note": "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate event: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	beforeActivationID := pending[0].activationID()

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
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
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply note delta: %v", err)
	} else if !ok {
		t.Fatal("apply note delta unexpectedly skipped")
	}
	pending = session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after note modify = %d, want %d", got, want)
	}
	if got := pending[0].activationID(); got != beforeActivationID {
		t.Fatalf("activation ID after note modify = %q, want %q", got, beforeActivationID)
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips after note modify = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after note modify = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"tags": []any{"vip", "green", "active"}}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate tags: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("tags modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after tags modify = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal additions after tags modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply tags delta: %v", err)
	} else if !ok {
		t.Fatal("apply tags delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := len(captured), 1; got != want {
		t.Fatalf("captured rows = %d, want %d", got, want)
	}
	if !captured[0].Equal(mustValue(t, []any{"green"})) {
		t.Fatalf("capture = %v, want [green]", captured[0])
	}
	snapshot = session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after tags modify = %d, want 0", got)
	}
}

func TestListPatternSupportsFixedAndRestWildcardMatches(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "tags", Kind: ValueList, Required: true}},
	})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error {
		fired++
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "fixed-and-rest",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{
				ListPattern(Path("tags"),
					ListElem(ConstExpr{Value: "vip"}),
					ListWildcard(),
					ListRestWildcard(),
				),
			}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision,
		WithInitialFacts(
			SessionInitialFact{TemplateKey: event.Key(), Fields: mustFields(t, map[string]any{"tags": []any{"vip", "one"}})},
			SessionInitialFact{TemplateKey: event.Key(), Fields: mustFields(t, map[string]any{"tags": []any{"vip", "one", "two"}})},
			SessionInitialFact{TemplateKey: event.Key(), Fields: mustFields(t, map[string]any{"tags": []any{"vip"}})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 || fired != 2 {
		t.Fatalf("fired = (%d, %d), want 2", result.Fired, fired)
	}
}

func TestQueryListPatternReturnsSegmentBindingValue(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tags", Kind: ValueList, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "vip-active-events",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{
				ListPattern(Path("tags"),
					ListElem(ConstExpr{Value: "vip"}),
					ListSegment("middle"),
					ListElem(ConstExpr{Value: "active"}),
				),
			}, Target: TemplateKeyFact(event.Key()),
		}},
		Returns: []QueryReturnSpec{
			ReturnValue("middle", BindingValueExpr{Binding: "middle"}),
			ReturnValue("id", BindingPath("event", Path("id"))),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: event.Key(),
		Fields: mustFields(t, map[string]any{
			"id":   "e1",
			"tags": []any{"vip", "blue", "active"},
		}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	rows, err := session.QueryAll(ctx, "vip-active-events", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	value, ok := rows[0].Value("middle")
	if !ok || !value.Equal(mustValue(t, []any{"blue"})) {
		t.Fatalf("middle value = (%v, %v), want [blue]", value, ok)
	}
	assertQueryRowStringValue(t, rows[0], "id", "e1")
}

func TestListPatternValidationRejectsAmbiguousOrCollidingSegments(t *testing.T) {
	t.Run("multiple variable elements", func(t *testing.T) {
		workspace, event := listPatternValidationWorkspace(t)
		mustAddRule(t, workspace, RuleSpec{
			Name: "bad-pattern",
			Conditions: []RuleConditionSpec{{
				Binding: "event",

				ListPatterns: []ListPatternSpec{
					ListPattern(Path("tags"), ListSegment("a"), ListRestWildcard()),
				}, Target: TemplateKeyFact(event.Key()),
			}},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		_, err := workspace.Compile(context.Background())
		if !errors.Is(err, ErrInvalidListPattern) {
			t.Fatalf("Compile error = %v, want ErrInvalidListPattern", err)
		}
	})

	t.Run("segment collides with fact binding", func(t *testing.T) {
		workspace, event := listPatternValidationWorkspace(t)
		mustAddRule(t, workspace, RuleSpec{
			Name: "bad-pattern",
			Conditions: []RuleConditionSpec{{
				Binding: "event",

				ListPatterns: []ListPatternSpec{
					ListPattern(Path("tags"), ListSegment("event")),
				}, Target: TemplateKeyFact(event.Key()),
			}},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		_, err := workspace.Compile(context.Background())
		if !errors.Is(err, ErrInvalidListPattern) {
			t.Fatalf("Compile error = %v, want ErrInvalidListPattern", err)
		}
	})
}

func listPatternValidationWorkspace(t testing.TB) (*Workspace, Template) {
	t.Helper()
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "tags", Kind: ValueList, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	return workspace, event
}

func activationForFactID(t testing.TB, activations []activation, id FactID) activation {
	t.Helper()
	for _, activation := range activations {
		if slices.Contains(activation.factIDs(), id) {
			return activation
		}
	}
	t.Fatalf("missing activation for fact %s in %#v", id, activations)
	return activation{}
}

func assertCapturedListValue(t testing.TB, values []Value, want []any) {
	t.Helper()
	expected := mustValue(t, want)
	for _, value := range values {
		if value.Equal(expected) {
			return
		}
	}
	t.Fatalf("captured values %#v did not include %v", values, expected)
}
