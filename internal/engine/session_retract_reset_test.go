package engine

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
	template, ok := revision.compiledTemplate("person")
	if !ok {
		t.Fatal("expected template person")
	}
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("retract-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "person-1", "status": "active"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if internal := mustWorkingFactByID(t, session, inserted.Fact.ID()); internal.duplicateIndexForRevision(session.revision, session.compactSlotStore).kind != duplicateIndexSingleScalar {
		t.Fatalf("retract setup duplicate index kind = %v, want %v", internal.duplicateIndexForRevision(session.revision, session.compactSlotStore).kind, duplicateIndexSingleScalar)
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
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
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

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
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

	if _, err := session.Retract(context.Background(), newFactID(1, 1)); !errors.Is(err, ErrFactNotFound) {
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

	inserted, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
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
	result, err := session.Retract(context.Background(), newFactID(1, 1))
	if !errors.Is(err, ErrClosedSession) {
		t.Fatalf("retract closed error = %v, want ErrClosedSession", err)
	}
	if result.Status != RetractClosed {
		t.Fatalf("retract closed status = %v, want %v", result.Status, RetractClosed)
	}
}

func TestSessionResetAppliesInitialFactsAndReordersEvents(t *testing.T) {
	revision := mustCompile(t,
		TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}, {Name: "status", Kind: ValueString, Default: "active"}},
		},
		TemplateSpec{
			Name:   "meta",
			Fields: []FieldSpec{{Name: "version", Kind: ValueInt, Required: true}},
		},
	)
	template, ok := revision.compiledTemplate("person")
	if !ok {
		t.Fatal("expected template person")
	}
	metaTemplate, ok := revision.compiledTemplate("meta")
	if !ok {
		t.Fatal("expected template meta")
	}
	initialTemplate := SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"name": "Ada"})}
	initialDynamic := SessionInitialFact{TemplateKey: metaTemplate.Key(), Fields: mustFields(t, map[string]any{"version": 1})}

	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("reset-initialized-session"),
		WithResetBeforeSnapshot(true),
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

	inserted, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{"name": "Bob"}))
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

	resultAfter, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{"name": "Carol"}))
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
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
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
	if pending[0].Generation() != 2 {
		t.Fatalf("pending activation generation = %d, want 2", pending[0].Generation())
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
	template, ok := revision.compiledTemplate("person")
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

func TestSessionResetFailureAfterReuseLeavesStateIntact(t *testing.T) {
	revision := mustCompile(t,
		TemplateSpec{
			Name:              "person",
			DuplicatePolicy:   DuplicateUniqueKey,
			DuplicateKeyNames: []string{"id"},
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString},
			},
		},
		TemplateSpec{
			Name:   "note",
			Fields: []FieldSpec{{Name: "value", Kind: ValueString, Required: true}},
		},
	)
	template, ok := revision.compiledTemplate("person")
	if !ok {
		t.Fatal("expected template person")
	}
	noteTemplate, ok := revision.compiledTemplate("note")
	if !ok {
		t.Fatal("expected template note")
	}

	session, err := NewSession(
		revision,
		WithSessionID("reset-reuse-failure-session"),
		WithInitialFacts(
			SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"id": "person-1", "status": "active"})},
			SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"id": "person-2", "status": "inactive"})},
			SessionInitialFact{TemplateKey: noteTemplate.Key(), Fields: mustFields(t, map[string]any{"value": "baseline"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("initial Reset: %v", err)
	}
	baseline := mustSnapshot(t, context.Background(), session)
	if baseline.Len() != 3 {
		t.Fatalf("baseline length after reused reset = %d, want 3", baseline.Len())
	}

	session.initials = append(session.initials, SessionInitialFact{
		TemplateKey: template.Key(),
		Fields:      mustFields(t, map[string]any{}),
	})

	result, err := session.Reset(context.Background())
	if err == nil {
		t.Fatal("expected reset failure")
	}
	if result.Status != ResetValidationFailure {
		t.Fatalf("reset failure status = %v, want %v", result.Status, ResetValidationFailure)
	}

	after := mustSnapshot(t, context.Background(), session)
	if after.String() != baseline.String() {
		t.Fatalf("snapshot changed after failed reused reset: before=%q after=%q", baseline, after)
	}
	if got, want := session.Generation(), Generation(2); got != want {
		t.Fatalf("session generation after failed reused reset = %d, want %d", got, want)
	}
	if got, want := session.factCount(), baseline.Len(); got != want {
		t.Fatalf("working fact len after failed reused reset = %d, want %d", got, want)
	}
	if got, want := len(session.insertionOrder), baseline.Len(); got != want {
		t.Fatalf("insertion order len after failed reused reset = %d, want %d", got, want)
	}
	if got := len(session.factIDsByTemplate(template.Key())); got != 2 {
		t.Fatalf("template index len after failed reused reset = %d, want 2", got)
	}
	if got := len(session.factIDsByName(template.Name())); got != 2 {
		t.Fatalf("name index len after failed reused reset = %d, want 2", got)
	}
	if got := len(session.factIDsByName("note")); got != 1 {
		t.Fatalf("dynamic name index len after failed reused reset = %d, want 1", got)
	}
	first := baseline.Facts()[0]
	key := makeDuplicateKeyForTemplate(template.Name(), template, first.Fields())
	if got, ok := session.factIDForDuplicateKey(key); !ok || got != first.ID() {
		t.Fatalf("duplicate key after failed reused reset = (%q, %t), want (%q, true)", got, ok, first.ID())
	}
}

func TestSessionResetShrinkingInitialFactsClearsStaleIndexes(t *testing.T) {
	revision := mustCompile(t,
		TemplateSpec{
			Name:              "person",
			DuplicatePolicy:   DuplicateUniqueKey,
			DuplicateKeyNames: []string{"id"},
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString},
			},
		},
		TemplateSpec{
			Name:   "note",
			Fields: []FieldSpec{{Name: "value", Kind: ValueString, Required: true}},
		},
	)
	template, ok := revision.compiledTemplate("person")
	if !ok {
		t.Fatal("expected template person")
	}
	noteTemplate, ok := revision.compiledTemplate("note")
	if !ok {
		t.Fatal("expected template note")
	}

	session, err := NewSession(
		revision,
		WithSessionID("reset-shrink-session"),
		WithInitialFacts(
			SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"id": "person-1", "status": "active"})},
			SessionInitialFact{TemplateKey: template.Key(), Fields: mustFields(t, map[string]any{"id": "person-2", "status": "inactive"})},
			SessionInitialFact{TemplateKey: noteTemplate.Key(), Fields: mustFields(t, map[string]any{"value": "baseline"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("initial Reset: %v", err)
	}
	firstReset := mustSnapshot(t, context.Background(), session)

	session.initials = session.initials[:1]
	result, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset with fewer initials: %v", err)
	}
	if result.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", result.Status, ResetApplied)
	}
	if got, want := session.Generation(), Generation(3); got != want {
		t.Fatalf("session generation after shrinking reset = %d, want %d", got, want)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if got, want := snapshot.Len(), 1; got != want {
		t.Fatalf("snapshot length after shrinking reset = %d, want %d", got, want)
	}
	remaining := snapshot.Facts()[0]
	if got, ok := remaining.Field("id"); !ok || !got.Equal(mustValue(t, "person-1")) {
		t.Fatalf("remaining fact id = (%v, %v), want person-1", got, ok)
	}

	if got, want := session.factCount(), 1; got != want {
		t.Fatalf("working fact len after shrinking reset = %d, want %d", got, want)
	}
	if got, want := len(session.insertionOrder), 1; got != want {
		t.Fatalf("insertion order len after shrinking reset = %d, want %d", got, want)
	}
	if got, want := len(session.factIDsByTemplate(template.Key())), 1; got != want {
		t.Fatalf("template index len after shrinking reset = %d, want %d", got, want)
	}
	if got, want := len(session.factIDsByName(template.Name())), 1; got != want {
		t.Fatalf("name index len after shrinking reset = %d, want %d", got, want)
	}
	if got := len(session.factIDsByName("note")); got != 0 {
		t.Fatalf("stale dynamic name index len after shrinking reset = %d, want 0", got)
	}
	if got, want := session.factsByDuplicate.len(), 1; got != want {
		t.Fatalf("duplicate index len after shrinking reset = %d, want %d", got, want)
	}
	for _, fact := range firstReset.Facts() {
		if _, ok := session.factByID(fact.ID()); ok {
			t.Fatalf("stale fact id retained after shrinking reset: %q", fact.ID())
		}
	}
}

func TestSessionResetContainerInitialFactsDoNotShareCompiledStorage(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "settings",
		Fields: []FieldSpec{
			{Name: "labels", Kind: ValueList, Required: true},
			{Name: "meta", Kind: ValueMap, Required: true},
		},
	})
	settingsTemplate, ok := revision.compiledTemplate("settings")
	if !ok {
		t.Fatal("expected template settings")
	}
	session, err := NewSession(
		revision,
		WithInitialFacts(SessionInitialFact{
			TemplateKey: settingsTemplate.Key(),
			Fields: mustFields(t, map[string]any{
				"labels": []any{"stable"},
				"meta":   map[string]any{"tier": "gold"},
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	firstSnapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	resetFact := mustOnlyFact(t, session)
	if got := len(session.facts); got != 0 {
		t.Fatalf("broad fact storage after reset = %d, want zero", got)
	}
	if got := session.compactFacts.len(); got != 1 {
		t.Fatalf("compact fact storage after reset = %d, want 1", got)
	}
	resetFields := resetFact.fieldsMap()
	resetLabels, _ := resetFields["labels"].AsList()
	resetLabels[0] = mustValue(t, "mutated")
	resetFields["labels"] = mustValue(t, resetLabels)
	resetMeta, _ := resetFields["meta"].AsMap()
	resetMeta["tier"] = mustValue(t, "mutated")
	resetFields["meta"] = mustValue(t, resetMeta)
	state := session.activeFactWorkspace()
	state.replaceWorkingFact(resetFact)
	session.commitFactWorkspace(state)

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("second Reset: %v", err)
	}
	nextFact := mustOnlyFact(t, session)
	nextLabels, _ := nextFact.fieldsMap()["labels"].AsList()
	if got, want := valueString(nextLabels[0]), "stable"; got != want {
		t.Fatalf("compiled list initial aliased reset fact = %q, want %q", got, want)
	}
	nextMeta, _ := nextFact.fieldsMap()["meta"].AsMap()
	if got, want := valueString(nextMeta["tier"]), "gold"; got != want {
		t.Fatalf("compiled map initial aliased reset fact = %q, want %q", got, want)
	}
	if got := session.resetWorkspace.compactFacts.len(); got == 0 {
		t.Fatal("reset workspace did not retain previous compact fact storage")
	}

	snapshotFact := firstSnapshot.Facts()[0]
	snapshotLabels, _ := snapshotFact.Fields()["labels"].AsList()
	if got, want := valueString(snapshotLabels[0]), "stable"; got != want {
		t.Fatalf("pre-reset snapshot list changed = %q, want %q", got, want)
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
	template, ok := revision.compiledTemplate("event")
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

func TestSessionResetSlotBackedDeclaredTemplateUsesSlotsAndPublicAccessors(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "settings",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "labels", Kind: ValueList},
			{Name: "meta", Kind: ValueMap},
			{Name: "status", Kind: ValueString, Default: "active", AllowedValues: []any{"active", "inactive"}},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-settings",
		Conditions: []RuleConditionSpec{
			{Binding: "settings", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(
		revision,
		WithSessionID("reset-slot-session"),
		WithInitialFacts(
			SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"id":     "settings-1",
					"labels": []any{"stable"},
					"meta":   map[string]any{"tier": "gold"},
				}),
			},
			SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"id":     "settings-1",
					"labels": []any{"duplicate"},
					"meta":   map[string]any{"tier": "silver"},
					"status": "inactive",
				}),
			},
			SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"id":     "settings-2",
					"labels": []any{"beta"},
					"meta":   map[string]any{"tier": "silver"},
					"status": "inactive",
				}),
			},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if got, want := len(session.compiledInitials), 2; got != want {
		t.Fatalf("compiled initial facts = %d, want %d", got, want)
	}
	for i, compiled := range session.compiledInitials {
		if len(compiled.fieldSlots) == 0 {
			t.Fatalf("compiled initial %d missing slot storage", i)
		}
		if compiled.fields != nil || compiled.fieldPresence != nil {
			t.Fatalf("compiled initial %d retained map-backed storage: fields=%v presence=%v", i, compiled.fields, compiled.fieldPresence)
		}
		if compiled.duplicateIndex.kind != duplicateIndexSingleScalar {
			t.Fatalf("compiled initial %d duplicate index kind = %v, want %v", i, compiled.duplicateIndex.kind, duplicateIndexSingleScalar)
		}
	}

	if got := len(mustSnapshot(t, context.Background(), session).FactsByTemplateKey(template.Key())); got != 2 {
		t.Fatalf("snapshot facts by template = %d, want 2", got)
	}

	resetFact := func(id string) *workingFact {
		t.Helper()
		for _, factID := range session.insertionOrder {
			fact, ok := session.workingFactByID(factID)
			if !ok {
				continue
			}
			if value, ok := fact.snapshotForRevision(session.revision, session.compactSlotStore).Field("id"); ok && valueString(value) == id {
				return fact
			}
		}
		t.Fatalf("missing fact with id %q", id)
		return nil
	}

	firstFact := resetFact("settings-1")
	if firstFact.duplicateIndexForRevision(session.revision, session.compactSlotStore).kind != duplicateIndexSingleScalar {
		t.Fatalf("reset fact duplicate index kind = %v, want %v", firstFact.duplicateIndexForRevision(session.revision, session.compactSlotStore).kind, duplicateIndexSingleScalar)
	}
	labelsSlot := -1
	metaSlot := -1
	for i, spec := range template.fields {
		switch spec.Name {
		case "labels":
			labelsSlot = i
		case "meta":
			metaSlot = i
		}
	}
	if labelsSlot < 0 || metaSlot < 0 {
		t.Fatalf("missing labels/meta slots: labels=%d meta=%d", labelsSlot, metaSlot)
	}
	firstSlots := firstFact.fieldSlotSlice()
	firstSlots[labelsSlot].value = mustValue(t, []any{"mutated"})
	firstSlots[metaSlot].value = mustValue(t, map[string]any{"tier": "mutated"})
	state := session.activeFactWorkspace()
	state.replaceWorkingFact(firstFact)
	session.commitFactWorkspace(state)

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset after mutation: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	byID := make(map[string]FactSnapshot, snapshot.Len())
	for _, fact := range snapshot.Facts() {
		id, ok := fact.Field("id")
		if !ok {
			t.Fatal("reset snapshot fact missing id")
		}
		byID[valueString(id)] = fact
	}

	first, ok := byID["settings-1"]
	if !ok {
		t.Fatal("reset snapshot missing first fact")
	}
	if got, ok := first.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("default status presence = (%v, %v), want default", got, ok)
	}
	if got, ok := first.Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("default status value = (%v, %v), want active", got, ok)
	}
	if got, ok := first.FieldPresence("labels"); !ok || got != FieldPresenceExplicit {
		t.Fatalf("labels presence = (%v, %v), want explicit", got, ok)
	}
	if got, ok := first.Field("labels"); !ok || !got.Equal(mustValue(t, []any{"stable"})) {
		t.Fatalf("labels value = (%v, %v), want stable", got, ok)
	}
	if got, ok := first.Field("meta"); !ok || !got.Equal(mustValue(t, map[string]any{"tier": "gold"})) {
		t.Fatalf("meta value = (%v, %v), want gold", got, ok)
	}
	fields := first.Fields()
	labelsValue := fields["labels"]
	labels, _ := labelsValue.AsList()
	labels[0] = mustValue(t, "changed")
	rereadLabels, _ := labelsValue.AsList()
	if !rereadLabels[0].Equal(mustValue(t, "stable")) {
		t.Fatalf("labels AsList returned aliased storage")
	}
	metaValue := fields["meta"]
	meta, _ := metaValue.AsMap()
	meta["tier"] = mustValue(t, "changed")
	rereadMeta, _ := metaValue.AsMap()
	if !rereadMeta["tier"].Equal(mustValue(t, "gold")) {
		t.Fatalf("meta AsMap returned aliased storage")
	}
	if got, ok := first.Field("labels"); !ok || !got.Equal(mustValue(t, []any{"stable"})) {
		t.Fatalf("labels accessor was not defensive: (%v, %v)", got, ok)
	}
	if got, ok := first.Field("meta"); !ok || !got.Equal(mustValue(t, map[string]any{"tier": "gold"})) {
		t.Fatalf("meta accessor was not defensive: (%v, %v)", got, ok)
	}

	second, ok := byID["settings-2"]
	if !ok {
		t.Fatal("reset snapshot missing second fact")
	}
	if got, ok := second.FieldPresence("status"); !ok || got != FieldPresenceExplicit {
		t.Fatalf("explicit status presence = (%v, %v), want explicit", got, ok)
	}
	if got, ok := second.Field("status"); !ok || !got.Equal(mustValue(t, "inactive")) {
		t.Fatalf("explicit status value = (%v, %v), want inactive", got, ok)
	}
}

func TestSessionResetUntargetedDeclaredTemplateUsesCompactInitial(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "settings",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active"},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "dynamic-event",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact("event")}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	session, err := NewSession(
		revision,
		WithSessionID("reset-untargeted-closed-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: template.Key(),
			Fields:      mustFields(t, map[string]any{"id": "settings-1"}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if got, want := len(session.compiledInitials), 1; got != want {
		t.Fatalf("compiled initial facts = %d, want %d", got, want)
	}
	compiled := session.compiledInitials[0]
	if len(compiled.compactSlots) != len(template.fields) {
		t.Fatalf("untargeted scalar initial compact slots = %d, want %d", len(compiled.compactSlots), len(template.fields))
	}
	if len(compiled.fieldSlots) != 0 || compiled.fields != nil || compiled.fieldPresence != nil {
		t.Fatalf("untargeted scalar initial retained broad storage: fields=%#v slots=%#v presence=%#v", compiled.fields, compiled.fieldSlots, compiled.fieldPresence)
	}
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	fact := mustOnlyFact(t, session)
	if got := len(session.facts); got != 0 {
		t.Fatalf("untargeted scalar initial retained broad fact rows = %d, want 0", got)
	}
	if len(fact.fieldSlotSlice()) != 0 {
		t.Fatalf("untargeted scalar reset fact retained wide slots: %#v", fact.fieldSlotSlice())
	}
	if got, want := len(fact.compactFieldSlots(session.compactSlotStore)), len(template.fields); got != want {
		t.Fatalf("untargeted scalar reset compact slots = %d, want %d", got, want)
	}
	if got, ok := fact.snapshotForRevision(session.revision, session.compactSlotStore).Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("default status = (%v, %v), want active", got, ok)
	}
}

func TestSessionResetSlotBackedValidationFailureLeavesStateIntact(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "settings",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, AllowedValues: []any{"active", "inactive"}},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-settings",
		Conditions: []RuleConditionSpec{
			{Binding: "settings", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	cases := []struct {
		name        string
		initializer SessionInitialFact
	}{
		{
			name: "missing required",
			initializer: SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"status": "active",
				}),
			},
		},
		{
			name: "invalid kind",
			initializer: SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"id":     "settings-2",
					"status": 1,
				}),
			},
		},
		{
			name: "invalid allowed",
			initializer: SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"id":     "settings-3",
					"status": "pending",
				}),
			},
		},
		{
			name: "unknown field",
			initializer: SessionInitialFact{
				TemplateKey: template.Key(),
				Fields: mustFields(t, map[string]any{
					"id":      "settings-4",
					"status":  "active",
					"unknown": "value",
				}),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, err := NewSession(
				revision,
				WithSessionID(SessionID("reset-slot-failure-"+tc.name)),
				WithInitialFacts(SessionInitialFact{
					TemplateKey: template.Key(),
					Fields: mustFields(t, map[string]any{
						"id":     "baseline",
						"status": "active",
					}),
				}),
			)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}

			baseline := mustSnapshot(t, context.Background(), session)
			if baseline.Len() != 1 {
				t.Fatalf("baseline length = %d, want 1", baseline.Len())
			}

			session.initials = append(session.initials, tc.initializer)

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
			beforeFact := baseline.Facts()[0]
			afterFact := after.Facts()[0]
			if got, ok := afterFact.Field("id"); !ok || !got.Equal(beforeFact.Fields()["id"]) {
				t.Fatalf("snapshot fact changed after failed reset: (%v, %v)", got, ok)
			}
		})
	}
}

func TestSessionResetWithoutInitialFactsDefersDuplicateIndexReserve(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "dedupe",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "reset-empty-duplicate-reserve-session")

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := session.factsByDuplicate.len(); got != 0 {
		t.Fatalf("duplicate index length = %d, want 0 after empty reset", got)
	}

	if _, err := session.Assert(ctx, template.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("Assert first: %v", err)
	}
	if got := session.factsByDuplicate.len(); got != 1 {
		t.Fatalf("duplicate index length = %d, want 1 after first assert", got)
	}
	if result, err := session.Assert(ctx, template.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("Assert duplicate: %v", err)
	} else if result.Inserted() {
		t.Fatal("duplicate unique-key assert inserted a second fact")
	}
}

func mustOnlyFact(t testing.TB, session *Session) *workingFact {
	t.Helper()
	if session == nil {
		t.Fatal("session is nil")
	}
	if got, want := session.factCount(), 1; got != want {
		t.Fatalf("working facts = %d, want %d", got, want)
	}
	for _, id := range session.insertionOrder {
		fact, ok := session.workingFactByID(id)
		if ok {
			return fact
		}
	}
	t.Fatal("working fact missing")
	return nil
}
