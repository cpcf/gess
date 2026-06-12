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
