package gess

import (
	"context"
	"errors"
	"testing"
)

func TestSessionModifyMissingAndStaleFactID(t *testing.T) {
	session := mustSession(t, mustCompile(t), "modify-missing-session")
	result, err := session.Modify(context.Background(), FactID{generation: 1, sequence: 99}, FactPatch{})
	if !errors.Is(err, ErrFactNotFound) {
		t.Fatalf("expected ErrFactNotFound, got %v", err)
	}
	if result.Status != ModifyMissing {
		t.Fatalf("missing status = %v, want %v", result.Status, ModifyMissing)
	}

	inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("insert fact for stale check: %v", err)
	}
	session.resetWorkingMemory()
	result, err = session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{})
	if !errors.Is(err, ErrStaleFactID) {
		t.Fatalf("expected ErrStaleFactID, got %v", err)
	}
	if result.Status != ModifyStale {
		t.Fatalf("stale status = %v, want %v", result.Status, ModifyStale)
	}
}

func TestSessionModifyNoOpReturnsWithoutMutation(t *testing.T) {
	collector := &testEventCollector{}
	session, err := NewSession(mustCompile(t), WithSessionID("modify-noop-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	baseline, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{
		"name":  "Ada",
		"count": 10,
	}))
	if err != nil {
		t.Fatalf("Assert baseline: %v", err)
	}

	result, err := session.Modify(context.Background(), baseline.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"count": 10}),
	})
	if err != nil {
		t.Fatalf("Modify no-op: %v", err)
	}
	if result.Status != ModifyNoOp {
		t.Fatalf("no-op status = %v, want %v", result.Status, ModifyNoOp)
	}
	if result.Fact.Version() != baseline.Fact.Version() {
		t.Fatalf("no-op version = %d, want %d", result.Fact.Version(), baseline.Fact.Version())
	}
	if result.Fact.Recency() != baseline.Fact.Recency() {
		t.Fatalf("no-op recency = %d, want %d", result.Fact.Recency(), baseline.Fact.Recency())
	}
	if got := len(collector.Events()); got != 1 {
		t.Fatalf("no-op modify emitted %d events", got)
	}
}

func TestSessionModifyNoOpDoesNotCreateAgendaNoise(t *testing.T) {
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
	session, err := NewSession(revision, WithSessionID("modify-noise-session"), WithEventListener(collector))
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

	result, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Ada"}),
	})
	if err != nil {
		t.Fatalf("Modify no-op: %v", err)
	}
	if result.Status != ModifyNoOp {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyNoOp)
	}
	if got := len(collector.Events()); got != 2 {
		t.Fatalf("no-op modify emitted %d events, want 2", got)
	}
}

func TestSessionModifyReconcilesAgendaForChangedAndDroppedMatches(t *testing.T) {
	t.Run("still matches", func(t *testing.T) {
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
		session, err := NewSession(revision, WithSessionID("modify-still-matches-session"), WithEventListener(collector))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
		if err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}

		modified, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
			Set: mustFields(t, map[string]any{"name": "Grace"}),
		})
		if err != nil {
			t.Fatalf("Modify: %v", err)
		}
		if modified.Status != ModifyChanged {
			t.Fatalf("modify status = %v, want %v", modified.Status, ModifyChanged)
		}

		events := collector.Events()
		if got, want := len(events), 5; got != want {
			t.Fatalf("events = %d, want %d", got, want)
		}
		if events[0].Type != EventFactAsserted || events[1].Type != EventRuleActivated || events[2].Type != EventFactModified || events[3].Type != EventRuleDeactivated || events[4].Type != EventRuleActivated {
			t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type, events[4].Type})
		}
		if events[1].ActivationID == events[4].ActivationID {
			t.Fatalf("modify reused activation ID %q", events[4].ActivationID)
		}
	})

	t.Run("stops matching", func(t *testing.T) {
		workspace := NewWorkspace()
		template := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "status", Kind: ValueString, Required: true}},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "pending-only",
			Conditions: []RuleConditionSpec{
				{Binding: "person", TemplateKey: template.Key(), FieldConstraints: []FieldConstraintSpec{{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")}}},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		collector := &testEventCollector{}
		session, err := NewSession(revision, WithSessionID("modify-stops-matching-session"), WithEventListener(collector))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"status": "pending"}))
		if err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}

		modified, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
			Set: mustFields(t, map[string]any{"status": "done"}),
		})
		if err != nil {
			t.Fatalf("Modify: %v", err)
		}
		if modified.Status != ModifyChanged {
			t.Fatalf("modify status = %v, want %v", modified.Status, ModifyChanged)
		}

		events := collector.Events()
		if got, want := len(events), 4; got != want {
			t.Fatalf("events = %d, want %d", got, want)
		}
		if events[0].Type != EventFactAsserted || events[1].Type != EventRuleActivated || events[2].Type != EventFactModified || events[3].Type != EventRuleDeactivated {
			t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
		}
	})
}

func TestSessionModifyDynamicFactsAdvanceVersionRecencyAndEmitDelta(t *testing.T) {
	collector := &testEventCollector{}
	session, err := NewSession(mustCompile(t), WithSessionID("modify-dynamic-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	baseline, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{
		"name":  "Ada",
		"count": 1,
	}))
	if err != nil {
		t.Fatalf("Assert baseline: %v", err)
	}

	result, err := session.Modify(context.Background(), baseline.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"count": 2}),
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if result.Fact.ID() != baseline.Fact.ID() {
		t.Fatalf("fact ID changed from %q to %q", baseline.Fact.ID(), result.Fact.ID())
	}
	if result.Fact.Version() != baseline.Fact.Version()+1 {
		t.Fatalf("version = %d, want %d", result.Fact.Version(), baseline.Fact.Version()+1)
	}
	if result.Fact.Recency() != baseline.Fact.Recency()+1 {
		t.Fatalf("recency = %d, want %d", result.Fact.Recency(), baseline.Fact.Recency()+1)
	}

	if result.Delta == nil {
		t.Fatalf("delta is nil")
	}
	if result.Delta.Kind != MutationModify {
		t.Fatalf("delta kind = %q, want %q", result.Delta.Kind, MutationModify)
	}
	if result.Delta.OldVersion != baseline.Fact.Version() {
		t.Fatalf("delta old version = %d, want %d", result.Delta.OldVersion, baseline.Fact.Version())
	}
	if result.Delta.NewVersion != result.Fact.Version() {
		t.Fatalf("delta new version = %d, want %d", result.Delta.NewVersion, result.Fact.Version())
	}
	if result.Delta.OldDuplicate == "" || result.Delta.NewDuplicate == "" {
		t.Fatalf("delta duplicate keys missing: old=%q new=%q", result.Delta.OldDuplicate, result.Delta.NewDuplicate)
	}
	if len(result.Delta.ChangedFields) != 1 || result.Delta.ChangedFields[0].Field != "count" {
		t.Fatalf("changed fields: %#v", result.Delta.ChangedFields)
	}
	if !result.Delta.ChangedFields[0].Old.Equal(mustValue(t, 1)) {
		t.Fatalf("changed old field = %v, want int(1)", result.Delta.ChangedFields[0].Old)
	}
	if !result.Delta.ChangedFields[0].New.Equal(mustValue(t, 2)) {
		t.Fatalf("changed new field = %v, want int(2)", result.Delta.ChangedFields[0].New)
	}

	events := collector.Events()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Type != EventFactAsserted || events[1].Type != EventFactModified {
		t.Fatalf("events = %#v", events)
	}
	if events[1].Type != EventFactModified {
		t.Fatalf("event type = %v, want %v", events[1].Type, EventFactModified)
	}
	if events[1].Delta == nil || events[1].Delta.Recency != result.Fact.Recency() {
		t.Fatalf("event delta recency missing or mismatch")
	}
}

func TestSessionModifyRebuildsClosedTemplateSlots(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-21",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: template.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintEqual, Value: 21},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	planConstraint := revision.rules["age-21"].conditionPlans[0].constraints[0]
	if planConstraint.fieldSlot < 0 {
		t.Fatalf("field slot = %d, want non-negative", planConstraint.fieldSlot)
	}

	session, err := NewSession(revision, WithSessionID("modify-slot-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"age":  18,
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	snapshot := session.indexedSnapshotLocked()
	matches, err := revision.rules["age-21"].scanCondition(context.Background(), snapshot, 0)
	if err != nil {
		t.Fatalf("scanCondition before modify: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("pre-modify matches = %#v, want none", matches)
	}

	modified, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"age": 21}),
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if modified.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", modified.Status, ModifyChanged)
	}

	snapshot = session.indexedSnapshotLocked()
	matches, err = revision.rules["age-21"].scanCondition(context.Background(), snapshot, 0)
	if err != nil {
		t.Fatalf("scanCondition after modify: %v", err)
	}
	if got, want := len(matches), 1; got != want {
		t.Fatalf("post-modify matches = %d, want %d", got, want)
	}
	if matches[0].fact.ID() != inserted.Fact.ID() {
		t.Fatalf("matched fact = %q, want %q", matches[0].fact.ID(), inserted.Fact.ID())
	}
}

func TestSessionModifyTemplateUnsetDefaultAndOptionalBehavior(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Default: "active"},
			{Name: "tag", Kind: ValueString},
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	session := mustSession(t, revision, "modify-template-unset-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"status": "inactive",
		"tag":    "open",
		"id":     "evt-1",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate baseline: %v", err)
	}

	result, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Unset: []string{"status"},
	})
	if err != nil {
		t.Fatalf("unset status: %v", err)
	}
	status, _ := result.Fact.Fields()["status"]
	if !status.Equal(mustValue(t, "active")) {
		t.Fatalf("status after unset = %v, want active", status)
	}
	if presence, ok := result.Fact.FieldPresence("status"); !ok || presence != FieldPresenceDefault {
		t.Fatalf("status presence = %q, want %q", presence, FieldPresenceDefault)
	}

	result, err = session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Unset: []string{"tag"},
	})
	if err != nil {
		t.Fatalf("unset optional field: %v", err)
	}
	if _, ok := result.Fact.Fields()["tag"]; ok {
		t.Fatalf("tag field should be removed after unset")
	}
	presence, hasPresence := result.Fact.FieldPresence("tag")
	if !hasPresence || presence != FieldPresenceOmitted {
		t.Fatalf("tag presence = %q, expected %q", presence, FieldPresenceOmitted)
	}
}

func TestSessionModifyTemplateUnsetRequiredFieldFailsAndLeavesWorkingMemory(t *testing.T) {
	collector := &testEventCollector{}
	revision := mustCompile(t, TemplateSpec{
		Name: "strict",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "name", Kind: ValueString},
		},
	})
	session, err := NewSession(revision, WithSessionID("modify-template-required-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	template, ok := revision.Template("strict")
	if !ok {
		t.Fatal("expected template strict")
	}

	inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "s-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	before := mustSnapshot(t, context.Background(), session)

	result, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Unset: []string{"id"},
	})
	if err == nil {
		t.Fatalf("expected validation error for unsetting required field")
	}
	if result.Status != ModifyValidationFailure {
		t.Fatalf("validation failure status = %v, want %v", result.Status, ModifyValidationFailure)
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}

	after := mustSnapshot(t, context.Background(), session)
	if after.Len() != before.Len() {
		t.Fatalf("snapshot length changed from %d to %d", before.Len(), after.Len())
	}
	got, ok := after.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatalf("strict fact missing after failed validation")
	}
	if got.ID() != inserted.Fact.ID() {
		t.Fatalf("unexpected fact after failed validation, got %q", got.ID())
	}
	if len(collector.Events()) != 1 {
		t.Fatalf("validation failure should not emit events: got %d", len(collector.Events()))
	}
}

func TestSessionModifyDuplicateCollisionIsAtomicAndLeavesIndexes(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	session := mustSession(t, revision, "modify-collision-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	first, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "evt-1", "status": "open"}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "evt-2", "status": "open"}))
	if err != nil {
		t.Fatalf("second assert: %v", err)
	}

	firstKey := makeDuplicateKeyForTemplate("event", template, first.Fact.Fields())
	secondKey := makeDuplicateKeyForTemplate("event", template, second.Fact.Fields())

	result, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"id": "evt-1"}),
	})
	if !errors.Is(err, ErrDuplicateFact) {
		t.Fatalf("expected duplicate collision error, got %v", err)
	}
	if result.Status != ModifyDuplicate {
		t.Fatalf("duplicate collision status = %v, want %v", result.Status, ModifyDuplicate)
	}

	if _, ok := session.factByID(second.Fact.ID()); !ok {
		t.Fatal("second fact missing after failed collision modify")
	}
	if _, ok := session.factIDForDuplicateKey(firstKey); !ok {
		t.Fatal("first duplicate key missing after failed collision")
	}
	if _, ok := session.factIDForDuplicateKey(secondKey); !ok {
		t.Fatal("second duplicate key missing after failed collision")
	}
	if len(mustSnapshot(t, context.Background(), session).Facts()) != 2 {
		t.Fatalf("snapshot length changed after failed collision modify")
	}
}

func TestSessionModifyDynamicDuplicateCollisionIsAtomic(t *testing.T) {
	session := mustSession(t, mustCompile(t), "modify-dynamic-collision-session")
	first, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	second, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Bob"}))
	if err != nil {
		t.Fatalf("second assert: %v", err)
	}
	secondKey := makeDuplicateKey("person", "", second.Fact.Fields())

	result, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Ada"}),
	})
	if !errors.Is(err, ErrDuplicateFact) {
		t.Fatalf("expected dynamic duplicate collision, got %v", err)
	}
	if result.Status != ModifyDuplicate {
		t.Fatalf("dynamic duplicate status = %v, want %v", result.Status, ModifyDuplicate)
	}
	if result.Fact.ID() != second.Fact.ID() {
		t.Fatalf("duplicate failure returned fact ID %q, want %q", result.Fact.ID(), second.Fact.ID())
	}
	if got, ok := session.factIDForDuplicateKey(secondKey); !ok || got != second.Fact.ID() {
		t.Fatalf("second duplicate key mapping = (%q, %t), want (%q, true)", got, ok, second.Fact.ID())
	}
	if got := mustSnapshot(t, context.Background(), session).Len(); got != 2 {
		t.Fatalf("snapshot length after dynamic collision = %d, want 2", got)
	}
	if first.Fact.ID() == second.Fact.ID() {
		t.Fatalf("test setup created duplicate facts")
	}
}

func TestSessionModifyDuplicateIndexUpdatesOnRealKeyChange(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	session := mustSession(t, revision, "modify-duplicate-update-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	first, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "evt-1", "status": "open"}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "evt-2", "status": "open"}))
	if err != nil {
		t.Fatalf("second assert: %v", err)
	}

	firstKey := makeDuplicateKeyForTemplate("event", template, first.Fact.Fields())
	secondKey := makeDuplicateKeyForTemplate("event", template, second.Fact.Fields())
	if _, ok := session.factIDForDuplicateKey(firstKey); !ok {
		t.Fatal("first duplicate key missing before modify")
	}
	if _, ok := session.factIDForDuplicateKey(secondKey); !ok {
		t.Fatal("second duplicate key missing before modify")
	}

	result, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"id": "evt-3"}),
	})
	if err != nil {
		t.Fatalf("modify unique key: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if _, ok := session.factIDForDuplicateKey(secondKey); ok {
		t.Fatal("old duplicate key should be removed after key change")
	}

	updatedKey := makeDuplicateKeyForTemplate("event", template, result.Fact.Fields())
	if mapped, ok := session.factIDForDuplicateKey(updatedKey); !ok || mapped != second.Fact.ID() {
		if !ok {
			t.Fatal("updated duplicate key missing after key change")
		}
		t.Fatalf("updated duplicate key maps %q, want %q", mapped, second.Fact.ID())
	}
}

func TestSessionModifyClosedAndConcurrencyStatus(t *testing.T) {
	session := mustSession(t, mustCompile(t), "modify-closed-session")
	inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("assert baseline: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	result, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{})
	if !errors.Is(err, ErrClosedSession) {
		t.Fatalf("closed modify error = %v, want ErrClosedSession", err)
	}
	if result.Status != ModifyClosed {
		t.Fatalf("closed modify status = %v, want %v", result.Status, ModifyClosed)
	}

	review := make(chan struct{})
	release := make(chan struct{})
	collector := &testEventCollector{}
	active, err := NewSession(mustCompile(t), WithSessionID("modify-concurrency-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	activeFact, err := active.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("assert active baseline: %v", err)
	}
	collector.waitCh = review
	collector.block = release

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = active.Modify(context.Background(), activeFact.Fact.ID(), FactPatch{
			Set: mustFields(t, map[string]any{"name": "Grace"}),
		})
	}()
	<-review
	result, err = active.Modify(context.Background(), activeFact.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Katherine"}),
	})
	if !errors.Is(err, ErrConcurrencyMisuse) {
		t.Fatalf("concurrent modify error = %v, want ErrConcurrencyMisuse", err)
	}
	if result.Status != ModifyConcurrencyMisuse {
		t.Fatalf("concurrent modify status = %v, want %v", result.Status, ModifyConcurrencyMisuse)
	}
	close(release)
	<-done
}

func mustValue(t testing.TB, value any) Value {
	t.Helper()
	next, err := NewValue(value)
	if err != nil {
		t.Fatalf("NewValue: %v", err)
	}
	return next
}
