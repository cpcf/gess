package gess

import (
	"context"
	"errors"
	"testing"
)

func TestSessionAssertDynamicAndTemplateFact(t *testing.T) {
	session := mustSession(t, mustCompile(t), "dynamic-template-assert-session")

	dynamic, err := session.Assert(context.Background(), "order", mustFields(t, map[string]any{
		"status": "pending",
		"count":  3,
	}))
	if err != nil {
		t.Fatalf("Assert dynamic fact: %v", err)
	}
	if !dynamic.Inserted() {
		t.Fatalf("dynamic assert status = %v, want inserted", dynamic.Status)
	}
	if dynamic.Fact.ID().IsZero() {
		t.Fatalf("dynamic fact ID is zero")
	}
	if got, want := dynamic.Fact.Version(), FactVersion(1); got != want {
		t.Fatalf("dynamic fact version = %d, want %d", got, want)
	}
	if got, want := dynamic.Fact.Recency(), Recency(1); got != want {
		t.Fatalf("dynamic fact recency = %d, want %d", got, want)
	}
	if dynamic.DuplicateKey == "" {
		t.Fatalf("dynamic assert returned empty duplicate key")
	}
	if dynamic.Delta == nil || dynamic.Delta.Kind != MutationAssert {
		t.Fatalf("dynamic assert missing mutation delta")
	}
	if got := len(session.factsByID[dynamic.Fact.ID()].fieldSlots); got != 0 {
		t.Fatalf("dynamic field slots = %d, want zero", got)
	}
	if session.factsByID[dynamic.Fact.ID()].fields == nil {
		t.Fatal("dynamic fact should remain map-backed")
	}

	revision := mustCompile(t, TemplateSpec{
		Name:            "event",
		Fields:          []FieldSpec{{Name: "name", Kind: ValueString}, {Name: "status", Kind: ValueString}},
		DuplicatePolicy: DuplicateAllow,
	})
	session = mustSession(t, revision, "template-assert-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	templateResult, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":   "boot",
		"status": "ok",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if !templateResult.Inserted() {
		t.Fatalf("template assert status = %v, want inserted", templateResult.Status)
	}
	if templateResult.Fact.Name() != "event" {
		t.Fatalf("template fact name = %q, want %q", templateResult.Fact.Name(), "event")
	}
	if templateResult.Fact.TemplateKey() != template.Key() {
		t.Fatalf("template key = %q, want %q", templateResult.Fact.TemplateKey(), template.Key())
	}
}

func TestSessionAssertSlotBackedClosedTemplateUsesSlotsAndPublicAccessors(t *testing.T) {
	workspace := NewWorkspace()
	targeted := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "targeted",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active", AllowedValues: []any{"active", "pending"}},
			{Name: "tag", Kind: ValueString},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "targeted-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "fact", TemplateKey: targeted.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("targeted-slot-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{
		"tag": "blue",
		"id":  "evt-1",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	internal := session.factsByID[inserted.Fact.ID()]
	if internal.fields != nil {
		t.Fatal("targeted closed fact should not keep canonical fields")
	}
	if got := len(internal.fieldSlots); got == 0 {
		t.Fatal("targeted closed fact should have slot storage")
	}
	if internal.fieldPresence != nil {
		t.Fatal("targeted closed fact should store presence in slots")
	}

	fields := inserted.Fact.Fields()
	if got, ok := fields["status"]; !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("defaulted status = (%v, %v), want active", got, ok)
	}
	fields["status"] = mustValue(t, "mutated")
	if got, ok := inserted.Fact.Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("fact fields map was not defensive: (%v, %v)", got, ok)
	}
	if got, ok := inserted.Fact.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("status presence = (%v, %v), want default", got, ok)
	}
	if got, ok := inserted.Fact.FieldPresence("tag"); !ok || got != FieldPresenceExplicit {
		t.Fatalf("tag presence = (%v, %v), want explicit", got, ok)
	}
	presence := inserted.Fact.FieldPresenceMap()
	presence["status"] = FieldPresenceExplicit
	if got, ok := inserted.Fact.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("field presence map was not defensive: (%v, %v)", got, ok)
	}
	rendered := inserted.Fact.String()
	if rendered != inserted.Fact.String() {
		t.Fatalf("slot-backed fact rendering changed between reads: %q != %q", rendered, inserted.Fact.String())
	}

	duplicate, err := session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "active",
		"tag":    "blue",
	}))
	if err != nil {
		t.Fatalf("duplicate assert: %v", err)
	}
	if duplicate.Status != AssertExisting {
		t.Fatalf("duplicate assert status = %v, want %v", duplicate.Status, AssertExisting)
	}
	if duplicate.DuplicateKey != inserted.DuplicateKey {
		t.Fatalf("duplicate key changed: %q != %q", duplicate.DuplicateKey, inserted.DuplicateKey)
	}

	_, err = session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{
		"status": "active",
		"tag":    "blue",
	}))
	if err == nil {
		t.Fatal("missing required field should fail for slot-backed template")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for missing required field, got %T: %v", err, err)
	}
	if validation.FieldName != "id" {
		t.Fatalf("required field validation error = %q, want id", validation.FieldName)
	}

	_, err = session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{
		"id":     "evt-2",
		"status": "blocked",
	}))
	if err == nil {
		t.Fatal("disallowed value should fail for slot-backed template")
	}
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for disallowed value, got %T: %v", err, err)
	}
	if validation.FieldName != "status" {
		t.Fatalf("disallowed field validation error = %q, want status", validation.FieldName)
	}
	if got := mustSnapshot(t, context.Background(), session).Len(); got != 1 {
		t.Fatalf("snapshot length after validation failures = %d, want 1", got)
	}
}

func TestSessionAssertDuplicateKeyParityForSlotBackedAndMapBackedFacts(t *testing.T) {
	type duplicateParityCase struct {
		name            string
		duplicatePolicy  DuplicatePolicy
		duplicateKey    []string
	}

	cases := []duplicateParityCase{
		{
			name:           "structural",
			duplicatePolicy: DuplicateStructural,
		},
		{
			name:           "unique-key",
			duplicatePolicy: DuplicateUniqueKey,
			duplicateKey:   []string{"id"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseSpec := TemplateSpec{
				Name:            "event",
				DuplicatePolicy: tc.duplicatePolicy,
				DuplicateKeyNames: tc.duplicateKey,
				Fields: []FieldSpec{
					{Name: "id", Kind: ValueString, Required: true},
					{Name: "status", Kind: ValueString, Default: "active"},
				},
			}

			mapRevision := mustCompile(t, baseSpec)
			mapSession := mustSession(t, mapRevision, "map-duplicate-parity-session")
			mapTemplate, ok := mapRevision.Template("event")
			if !ok {
				t.Fatal("expected map-backed event template")
			}

			slotWorkspace := NewWorkspace()
			if err := slotWorkspace.AddTemplate(TemplateSpec{
				Name:            "gate",
				Fields:          []FieldSpec{{Name: "id", Kind: ValueString}},
				DuplicatePolicy: DuplicateAllow,
			}); err != nil {
				t.Fatalf("AddTemplate(gate): %v", err)
			}
			if err := slotWorkspace.AddTemplate(TemplateSpec{
				Name:             "event",
				Closed:           true,
				DuplicatePolicy:  tc.duplicatePolicy,
				DuplicateKeyNames: tc.duplicateKey,
				Fields:           baseSpec.Fields,
			}); err != nil {
				t.Fatalf("AddTemplate(event): %v", err)
			}
			if err := slotWorkspace.AddAction(ActionSpec{
				Name: "mark",
				Fn:   func(ActionContext) error { return nil },
			}); err != nil {
				t.Fatalf("AddAction(mark): %v", err)
			}
			if err := slotWorkspace.AddRule(RuleSpec{
				Name: "slot-event-rule",
				Conditions: []RuleConditionSpec{
					{Binding: "event", TemplateKey: TemplateKey("event")},
					{Binding: "gate", TemplateKey: TemplateKey("gate")},
				},
				Actions: []RuleActionSpec{{Name: "mark"}},
			}); err != nil {
				t.Fatalf("AddRule(slot-event-rule): %v", err)
			}
			slotRevision, err := slotWorkspace.Compile(context.Background())
			if err != nil {
				t.Fatalf("Compile(slot revision): %v", err)
			}
			slotSession := mustSession(t, slotRevision, "slot-duplicate-parity-session")
			slotTemplate, ok := slotRevision.Template("event")
			if !ok {
				t.Fatal("expected slot-backed event template")
			}

			fields := mustFields(t, map[string]any{"id": "evt-1"})
			mapResult, err := mapSession.AssertTemplate(context.Background(), mapTemplate.Key(), fields)
			if err != nil {
				t.Fatalf("map-backed AssertTemplate: %v", err)
			}
			slotResult, err := slotSession.AssertTemplate(context.Background(), slotTemplate.Key(), fields)
			if err != nil {
				t.Fatalf("slot-backed AssertTemplate: %v", err)
			}

			if mapResult.DuplicateKey != slotResult.DuplicateKey {
				t.Fatalf("duplicate key mismatch: map=%q slot=%q", mapResult.DuplicateKey, slotResult.DuplicateKey)
			}
			if got, want := mapResult.Fact.Fields()["status"], slotResult.Fact.Fields()["status"]; !got.Equal(want) {
				t.Fatalf("defaulted status mismatch: map=%v slot=%v", got, want)
			}
			if tc.duplicatePolicy == DuplicateUniqueKey && mapResult.DuplicateKey == "" {
				t.Fatal("expected unique-key duplicate key to be set")
			}
		})
	}
}

func TestSessionAssertSlotBackedUniqueKeyPolicy(t *testing.T) {
	workspace := NewWorkspace()
	targeted := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "event",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "targeted-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "fact", TemplateKey: targeted.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("unique-slot-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	first, err := session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{
		"status": "open",
		"id":     "evt-1",
	}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "closed",
	}))
	if err != nil {
		t.Fatalf("duplicate assert: %v", err)
	}
	if second.Status != AssertExisting {
		t.Fatalf("duplicate status = %v, want %v", second.Status, AssertExisting)
	}
	if second.Fact.ID() != first.Fact.ID() {
		t.Fatalf("duplicate fact id = %q, want %q", second.Fact.ID(), first.Fact.ID())
	}
	if second.DuplicateKey == "" {
		t.Fatal("expected unique-key duplicate key")
	}
}

func TestSessionAssertSkipsSlotsForUntargetedClosedTemplate(t *testing.T) {
	workspace := NewWorkspace()
	targeted := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "targeted",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	untargeted := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "untargeted",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "targeted-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "fact", TemplateKey: targeted.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("untargeted-slot-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	targetedResult, err := session.AssertTemplate(context.Background(), targeted.Key(), mustFields(t, map[string]any{"id": "a"}))
	if err != nil {
		t.Fatalf("AssertTemplate targeted: %v", err)
	}
	if got := len(session.factsByID[targetedResult.Fact.ID()].fieldSlots); got == 0 {
		t.Fatalf("targeted field slots = %d, want non-zero", got)
	}

	untargetedResult, err := session.AssertTemplate(context.Background(), untargeted.Key(), mustFields(t, map[string]any{"id": "b"}))
	if err != nil {
		t.Fatalf("AssertTemplate untargeted: %v", err)
	}
	if got := len(session.factsByID[untargetedResult.Fact.ID()].fieldSlots); got != 0 {
		t.Fatalf("untargeted field slots = %d, want zero", got)
	}
	if session.factsByID[untargetedResult.Fact.ID()].fieldPresence == nil {
		t.Fatal("untargeted fact should remain map-backed for presence")
	}
}

func TestSessionAssertDuplicateMetadataIsStable(t *testing.T) {
	session := mustSession(t, mustCompile(t), "duplicate-metadata-session")

	first, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{
		"name":  "Ada",
		"age":   30,
		"roles": []any{"admin", "owner"},
	}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}

	second, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{
		"roles": []any{"admin", "owner"},
		"age":   30,
		"name":  "Ada",
	}))
	if err != nil {
		t.Fatalf("duplicate assert: %v", err)
	}
	if second.Status != AssertExisting {
		t.Fatalf("duplicate assert status = %v, want %v", second.Status, AssertExisting)
	}
	if second.Fact.ID() != first.Fact.ID() {
		t.Fatalf("duplicate ID = %q, want %q", second.Fact.ID(), first.Fact.ID())
	}
	if got, want := second.Fact.Recency(), first.Fact.Recency(); got != want {
		t.Fatalf("duplicate recency = %d, want %d", got, want)
	}
	if got, want := second.Fact.Version(), first.Fact.Version(); got != want {
		t.Fatalf("duplicate version = %d, want %d", got, want)
	}
	if second.DuplicateKey == "" {
		t.Fatalf("duplicate key should be set for structural duplicate policy")
	}
	if second.Delta != nil {
		t.Fatalf("duplicate assertion should not return mutation delta: %#v", second.Delta)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 1 {
		t.Fatalf("snapshot length = %d, want 1", snapshot.Len())
	}
}

func TestSessionAssertDuplicateUniqueKeyPolicy(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"name"},
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	session := mustSession(t, revision, "unique-dup-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	first, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":   "evt-1",
		"status": "open",
	}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}

	existing, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":   "evt-1",
		"status": "closed",
	}))
	if err != nil {
		t.Fatalf("duplicate by unique key: %v", err)
	}
	if existing.Status != AssertExisting {
		t.Fatalf("expected duplicate-by-key assertion status %v, got %v", AssertExisting, existing.Status)
	}
	if existing.Fact.ID() != first.Fact.ID() {
		t.Fatalf("duplicate ID = %q, want %q", existing.Fact.ID(), first.Fact.ID())
	}
	if existing.Delta != nil {
		t.Fatalf("duplicate assertion should not return mutation delta: %#v", existing.Delta)
	}
	if existing.DuplicateKey == "" {
		t.Fatal("expected unique-key duplicate key in result")
	}

	_, err = session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":   "evt-2",
		"status": "open",
	}))
	if err != nil {
		t.Fatalf("non-duplicate unique key assert: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 2 {
		t.Fatalf("snapshot length = %d, want 2", snapshot.Len())
	}
}

func TestSessionAssertDuplicateAllowPolicyAllowsMultiplicity(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:            "event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	session := mustSession(t, revision, "allow-dup-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	first, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":   "evt-1",
		"status": "open",
	}))
	if err != nil {
		t.Fatalf("first allow-duplicates assert: %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":   "evt-1",
		"status": "open",
	}))
	if err != nil {
		t.Fatalf("second allow-duplicates assert: %v", err)
	}
	if !first.Inserted() || !second.Inserted() {
		t.Fatalf("allow-duplicates assertion status values: first %v second %v", first.Status, second.Status)
	}
	if second.Fact.ID() == first.Fact.ID() {
		t.Fatalf("allow duplicates reused fact ID %q", first.Fact.ID())
	}
	if second.Fact.Recency() <= first.Fact.Recency() {
		t.Fatalf("expected increasing recency, got %d then %d", first.Fact.Recency(), second.Fact.Recency())
	}
	if first.DuplicateKey != "" {
		t.Fatalf("duplicate allow should not retain duplicate key filter metadata")
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 2 {
		t.Fatalf("snapshot length = %d, want 2", snapshot.Len())
	}
}

func TestSessionAssertValidationFailureLeavesMemoryAndEventsUnchanged(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("validation-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected template person")
	}

	_, err = session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{
		"title": "Dr.",
	}))
	if err == nil {
		t.Fatal("expected validation failure")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}

	if len(collector.Events()) != 0 {
		t.Fatalf("validation failure emitted %d events", len(collector.Events()))
	}
	if got := mustSnapshot(t, context.Background(), session).Len(); got != 0 {
		t.Fatalf("snapshot length after validation failure = %d, want 0", got)
	}
}

func TestSessionAssertEmitsOnlyForInsertedFacts(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString}},
	})
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("events-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected template")
	}

	first, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	if got := len(collector.Events()); got != 1 {
		t.Fatalf("events after first insert = %d, want 1", got)
	}
	if got := collector.Events()[0].Type; got != EventFactAsserted {
		t.Fatalf("event type = %v, want %v", got, EventFactAsserted)
	}

	if _, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("duplicate assert: %v", err)
	}
	if got := len(collector.Events()); got != 1 {
		t.Fatalf("events after duplicate = %d, want 1", got)
	}

	_, err = session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": ""}))
	if err != nil {
		t.Fatalf("insert distinct fact: %v", err)
	}
	if got := len(collector.Events()); got != 2 {
		t.Fatalf("events after second distinct insert = %d, want 2", got)
	}

	if len(first.Delta.After.Fields()) == 0 {
		t.Fatalf("expected non-empty after snapshot in inserted delta")
	}
}

func TestSessionAssertImmediatelyReconcilesAgendaAndSkipsDuplicateNoise(t *testing.T) {
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
	session, err := NewSession(revision, WithSessionID("assert-agenda-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if !inserted.Inserted() {
		t.Fatalf("assert status = %v, want inserted", inserted.Status)
	}

	events := collector.Events()
	if len(events) != 2 {
		t.Fatalf("events after first assert = %d, want 2", len(events))
	}
	if events[0].Type != EventFactAsserted || events[1].Type != EventRuleActivated {
		t.Fatalf("event order after first assert = %#v", []EventType{events[0].Type, events[1].Type})
	}

	duplicate, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("duplicate assert: %v", err)
	}
	if duplicate.Status != AssertExisting {
		t.Fatalf("duplicate status = %v, want %v", duplicate.Status, AssertExisting)
	}
	if got := len(collector.Events()); got != 2 {
		t.Fatalf("duplicate assert emitted %d events, want 2", got)
	}
}

func TestSessionAssertClosedSessionAndConcurrencyMisuse(t *testing.T) {
	session := mustSession(t, mustCompile(t), "closed-session")
	if _, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("initial assert: %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Bob"}))
	if !errors.Is(err, ErrClosedSession) {
		t.Fatalf("expected closed session error, got (%v, %v)", result.Status, err)
	}
	if result.Status != AssertClosed {
		t.Fatalf("closed session assert status = %v, want %v", result.Status, AssertClosed)
	}
}

func TestSessionAssertReportsConcurrencyMisuse(t *testing.T) {
	review := make(chan struct{})
	release := make(chan struct{})
	collector := &testEventCollector{
		waitCh: review,
		block:  release,
	}
	revision := mustCompile(t)
	session, err := NewSession(revision, WithSessionID("concurrency-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		if _, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"})); err != nil {
			// allow concurrent misuse from this call to avoid flakes; it should be in progress only once.
		}
	}()

	<-review
	result, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if !errors.Is(err, ErrConcurrencyMisuse) {
		t.Fatalf("expected concurrency misuse error, got %v", err)
	}
	if result.Status != AssertConcurrencyMisuse {
		t.Fatalf("concurrency misuse assert status = %v, want %v", result.Status, AssertConcurrencyMisuse)
	}
	close(release)
	<-firstDone
}
