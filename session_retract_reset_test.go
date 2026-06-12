package gess

import (
	"context"
	"errors"
	"testing"
)

func TestSessionRetractExistingRemovesSnapshotAndIndexes(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:            "person",
		DuplicatePolicy: DuplicateUniqueKey,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
		DuplicateKeyNames: []string{"id"},
	})
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected template person")
	}
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("retract-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "person-1", "status": "active"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if got, want := len(mustSnapshot(t, context.Background(), session).Facts()), 1; got != want {
		t.Fatalf("snapshot length = %d, want %d", got, want)
	}

	key := makeDuplicateKeyForTemplate(template.Name(), template, inserted.Fact.Fields())
	if existing, ok := session.factIDForDuplicateKey(key); !ok || existing != inserted.Fact.ID() {
		t.Fatalf("duplicate key %q maps to (%q, %t), want (%q, true)", key, existing, ok, inserted.Fact.ID())
	}
	beforeEvents := len(collector.Events())
	if beforeEvents != 1 {
		t.Fatalf("initial assert emitted %d events, want 1", beforeEvents)
	}

	result, err := session.Retract(context.Background(), inserted.Fact.ID())
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if result.Status != RetractRemoved {
		t.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
	}
	events := collector.Events()
	if len(events) != beforeEvents+1 {
		t.Fatalf("events after retract = %d, want %d", len(events), beforeEvents+1)
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventFactRetracted {
		t.Fatalf("event type = %v, want %v", lastEvent.Type, EventFactRetracted)
	}
	if got, want := lastEvent.FactIDs[0], inserted.Fact.ID(); got != want {
		t.Fatalf("retract event fact id = %q, want %q", got, want)
	}
	if lastEvent.Delta == nil || lastEvent.Delta.Before == nil {
		t.Fatal("retract event missing before snapshot")
	}

	if got := mustSnapshot(t, context.Background(), session).Len(); got != 0 {
		t.Fatalf("snapshot length after retract = %d, want 0", got)
	}
	if _, ok := session.factIDForDuplicateKey(key); ok {
		t.Fatal("duplicate index retained after retract")
	}
	if _, ok := session.factByID(inserted.Fact.ID()); ok {
		t.Fatal("retracted fact still available by ID")
	}
}

func TestSessionRetractImmediatelyDeactivatesPendingActivation(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: template.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("retract-agenda-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if got := len(collector.Events()); got != 2 {
		t.Fatalf("events after assert = %d, want 2", got)
	}

	result, err := session.Retract(context.Background(), inserted.Fact.ID())
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if result.Status != RetractRemoved {
		t.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
	if events[0].Type != EventFactAsserted || events[1].Type != EventRuleActivated || events[2].Type != EventFactRetracted || events[3].Type != EventRuleDeactivated {
		t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	}
	if got := mustSnapshot(t, context.Background(), session).Len(); got != 0 {
		t.Fatalf("snapshot length after retract = %d, want 0", got)
	}
}

func TestSessionRetractMissingReturnsNoopResultWithoutEvent(t *testing.T) {
	collector := &testEventCollector{}
	session, err := NewSession(mustCompile(t), WithSessionID("retract-missing-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Retract(context.Background(), FactID{generation: 1, sequence: 1}); !errors.Is(err, ErrFactNotFound) {
		t.Fatalf("expected ErrFactNotFound, got %v", err)
	}
	if got := len(collector.Events()); got != 0 {
		t.Fatalf("missing retract emitted %d events", got)
	}
}

func TestSessionRetractStaleReturnsNoopResultWithoutEvent(t *testing.T) {
	collector := &testEventCollector{}
	session, err := NewSession(mustCompile(t), WithSessionID("retract-stale-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	session.resetWorkingMemory()
	beforeEvents := len(collector.Events())
	result, err := session.Retract(context.Background(), inserted.Fact.ID())
	if !errors.Is(err, ErrStaleFactID) {
		t.Fatalf("expected ErrStaleFactID, got %v", err)
	}
	if result.Status != RetractStale {
		t.Fatalf("retract stale status = %v, want %v", result.Status, RetractStale)
	}
	if got := len(collector.Events()); got != beforeEvents {
		t.Fatalf("stale retract emitted %d events", got)
	}
}

func TestSessionRetractClosedStatus(t *testing.T) {
	session := mustSession(t, mustCompile(t), "retract-closed-session")
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	result, err := session.Retract(context.Background(), FactID{generation: 1, sequence: 1})
	if !errors.Is(err, ErrClosedSession) {
		t.Fatalf("retract closed error = %v, want ErrClosedSession", err)
	}
	if result.Status != RetractClosed {
		t.Fatalf("retract closed status = %v, want %v", result.Status, RetractClosed)
	}
}

func TestSessionResetAppliesInitialFactsAndReordersEvents(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}, {Name: "status", Kind: ValueString, Default: "active"}},
	})
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected template person")
	}
	initialTemplate := SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"name": "Ada"})}
	initialDynamic := SessionInitialFact{Name: "meta", Fields: mustFields(t, map[string]any{"version": 1})}

	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("reset-initialized-session"),
		WithInitialFacts(initialTemplate, initialDynamic),
		WithEventListener(collector),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if got := session.Generation(); got != 1 {
		t.Fatalf("initial generation = %d, want 1", got)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 2 {
		t.Fatalf("initial snapshot length = %d, want 2", snapshot.Len())
	}

	inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Bob"}))
	if err != nil {
		t.Fatalf("Assert pre-reset fact: %v", err)
	}

	assertDelta := collector.Events()[0]
	if assertDelta.Type != EventFactAsserted {
		t.Fatalf("pre-reset event type = %v, want %v", assertDelta.Type, EventFactAsserted)
	}

	result, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if result.Generation != 2 {
		t.Fatalf("reset generation = %d, want 2", result.Generation)
	}
	if result.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", result.Status, ResetApplied)
	}
	if result.Delta.Generation != 2 || result.Delta.OldGeneration != 1 {
		t.Fatalf("reset delta generation mismatch: %+v", result.Delta.Generation)
	}
	if result.Before.Len() != 3 {
		t.Fatalf("reset before snapshot length = %d, want 3", result.Before.Len())
	}

	events := collector.Events()
	if len(events) != 2 {
		t.Fatalf("events after reset = %d, want 2", len(events))
	}
	if events[1].Type != EventReset {
		t.Fatalf("post-reset event order: %v, %v", events[0].Type, events[1].Type)
	}
	if events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("event sequences after reset = %d, %d; want 1, 2", events[0].Sequence, events[1].Sequence)
	}
	if events[1].Delta == nil || events[1].Delta.Generation != 2 {
		t.Fatal("reset event missing or wrong generation")
	}

	if got := session.Generation(); got != 2 {
		t.Fatalf("session generation after reset = %d, want 2", got)
	}

	snapshot = mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 2 {
		t.Fatalf("snapshot after reset = %d, want 2", snapshot.Len())
	}
	if got, want := snapshot.Facts()[0].Generation(), Generation(2); got != want {
		t.Fatalf("initial fact generation = %d, want %d", got, want)
	}
	if _, ok := session.factIDForDuplicateKey(makeDuplicateKeyForTemplate(template.Name(), template, inserted.Fact.Fields())); ok {
		t.Fatal("stale duplicate index retained for pre-reset fact")
	}
	if _, err := session.Retract(context.Background(), inserted.Fact.ID()); !errors.Is(err, ErrStaleFactID) {
		t.Fatalf("expected stale error for pre-reset fact id %q", inserted.Fact.ID())
	}

	resultAfter, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Carol"}))
	if err != nil {
		t.Fatalf("Assert post-reset fact: %v", err)
	}
	events = collector.Events()
	if len(events) != 3 {
		t.Fatalf("events after post-reset assert = %d, want 3", len(events))
	}
	if events[2].Type != EventFactAsserted {
		t.Fatalf("expected third event type %v, got %v", EventFactAsserted, events[2].Type)
	}
	if events[2].Sequence != 3 {
		t.Fatalf("post-reset assert event sequence = %d, want 3", events[2].Sequence)
	}
	if resultAfter.Fact.Generation() != 2 {
		t.Fatalf("post-reset fact generation = %d, want 2", resultAfter.Fact.Generation())
	}
}

func TestSessionResetRebuildsAgendaForInitialFacts(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: template.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("reset-agenda-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: template.Key(),
			Fields:      mustFields(t, map[string]any{"name": "Ada"}),
		}),
		WithEventListener(collector),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("run fired = %d, want 1", result.Fired)
	}

	resetResult, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if resetResult.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", resetResult.Status, ResetApplied)
	}
	if got, want := session.Generation(), Generation(2); got != want {
		t.Fatalf("generation after reset = %d, want %d", got, want)
	}

	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	if pending[0].generation != 2 {
		t.Fatalf("pending activation generation = %d, want 2", pending[0].generation)
	}

	events := collector.Events()
	if len(events) < 4 {
		t.Fatalf("events = %d, want at least 4", len(events))
	}
	if events[len(events)-2].Type != EventReset || events[len(events)-1].Type != EventRuleActivated {
		t.Fatalf("tail event order = %#v", []EventType{events[len(events)-2].Type, events[len(events)-1].Type})
	}
}

func TestSessionResetFailureLeavesStateIntact(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "person",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields:            []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected template person")
	}

	session, err := NewSession(
		revision,
		WithSessionID("reset-atomic-session"),
		WithInitialFacts(SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"id": "person-1"})}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	baseline := mustSnapshot(t, context.Background(), session)
	if baseline.Len() != 1 {
		t.Fatalf("baseline length = %d, want 1", baseline.Len())
	}

	session.initials = append(session.initials, SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{})})

	result, err := session.Reset(context.Background())
	if err == nil {
		t.Fatal("expected reset failure")
	}
	if result.Status != ResetValidationFailure {
		t.Fatalf("reset failure status = %v, want %v", result.Status, ResetValidationFailure)
	}

	after := mustSnapshot(t, context.Background(), session)
	if after.Len() != 1 {
		t.Fatalf("snapshot length after failed reset = %d, want 1", after.Len())
	}
	if got, want := session.Generation(), Generation(1); got != want {
		t.Fatalf("session generation after failed reset = %d, want %d", got, want)
	}
}

func TestSessionResetClosedStatus(t *testing.T) {
	session := mustSession(t, mustCompile(t), "reset-closed-session")
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	result, err := session.Reset(context.Background())
	if !errors.Is(err, ErrClosedSession) {
		t.Fatalf("reset closed error = %v, want ErrClosedSession", err)
	}
	if result.Status != ResetClosed {
		t.Fatalf("reset closed status = %v, want %v", result.Status, ResetClosed)
	}
}

func TestSessionResetDoesNotReemitInitializersAsAsserts(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	session, err := NewSession(
		revision,
		WithInitialFacts(
			SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"name": "startup"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 1 {
		t.Fatalf("snapshot length after reset = %d, want 1", snapshot.Len())
	}
}
