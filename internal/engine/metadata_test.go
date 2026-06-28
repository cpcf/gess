package engine

import (
	"context"
	"testing"
)

func TestFactsDefaultToStatedSupportMetadata(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	session := mustSession(t, revision, "support-state-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	dynamic, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("dynamic assert: %v", err)
	}
	if got := dynamic.Fact.Support().State; got != FactSupportStated {
		t.Fatalf("dynamic support state = %q, want %q", got, FactSupportStated)
	}
	if dynamic.Delta == nil {
		t.Fatal("dynamic delta is nil")
	}
	if got := dynamic.Delta.SupportAfter.State; got != FactSupportStated {
		t.Fatalf("dynamic delta support state = %q, want %q", got, FactSupportStated)
	}

	templated, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("template assert: %v", err)
	}
	if got := templated.Fact.Support().State; got != FactSupportStated {
		t.Fatalf("template fact support state = %q, want %q", got, FactSupportStated)
	}
	if templated.Delta == nil {
		t.Fatal("template delta is nil")
	}
	if got := templated.Delta.SupportAfter.State; got != FactSupportStated {
		t.Fatalf("template delta support state = %q, want %q", got, FactSupportStated)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	for _, fact := range []FactSnapshot{dynamic.Fact, templated.Fact} {
		stored, ok := snapshot.Fact(fact.ID())
		if !ok {
			t.Fatalf("snapshot missing fact %q", fact.ID())
		}
		if stored.Support().State != FactSupportStated {
			t.Fatalf("snapshot support state for %q = %q, want %q", fact.ID(), stored.Support().State, FactSupportStated)
		}
	}
}

func TestMutationDeltasCarryMatcherMetadataAndCopies(t *testing.T) {
	collector := &testEventCollector{}
	session, err := NewSession(mustCompile(t), WithSessionID("metadata-copy-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	asserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	modified, err := session.Modify(context.Background(), asserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	})
	if err != nil {
		t.Fatalf("modify: %v", err)
	}
	if modified.Delta == nil {
		t.Fatal("modify delta is nil")
	}
	if modified.Delta.Kind != MutationModify {
		t.Fatalf("delta kind = %q, want %q", modified.Delta.Kind, MutationModify)
	}
	if modified.Delta.Generation != session.Generation() {
		t.Fatalf("delta generation = %d, want %d", modified.Delta.Generation, session.Generation())
	}
	if modified.Delta.OldVersion != asserted.Fact.Version() {
		t.Fatalf("delta old version = %d, want %d", modified.Delta.OldVersion, asserted.Fact.Version())
	}
	if modified.Delta.NewVersion != modified.Fact.Version() {
		t.Fatalf("delta new version = %d, want %d", modified.Delta.NewVersion, modified.Fact.Version())
	}
	if modified.Delta.Recency != modified.Fact.Recency() {
		t.Fatalf("delta recency = %d, want %d", modified.Delta.Recency, modified.Fact.Recency())
	}
	if modified.Delta.SupportBefore.State != FactSupportStated {
		t.Fatalf("delta support before = %q, want %q", modified.Delta.SupportBefore.State, FactSupportStated)
	}
	if modified.Delta.SupportAfter.State != FactSupportStated {
		t.Fatalf("delta support after = %q, want %q", modified.Delta.SupportAfter.State, FactSupportStated)
	}
	if modified.Delta.ActivationID != "" || modified.Delta.RuleID != "" || modified.Delta.RuleRevisionID != "" {
		t.Fatalf("external modify delta carried origin metadata: %#v", modified.Delta)
	}

	changes := modified.Delta.FieldChanges()
	if len(changes) != 1 || changes[0].Field != "name" {
		t.Fatalf("changed fields = %#v", changes)
	}
	if !changes[0].New.Equal(mustValue(t, "Grace")) {
		t.Fatalf("unexpected changed field new value = %v", changes[0].New)
	}
	changes[0].New = mustValue(t, "Mutated")
	if got := modified.Delta.FieldChanges()[0].New; !got.Equal(mustValue(t, "Grace")) {
		t.Fatalf("changed-field metadata was not defensively copied")
	}

	retracted, err := session.Retract(context.Background(), asserted.Fact.ID())
	if err != nil {
		t.Fatalf("retract: %v", err)
	}
	if retracted.Delta == nil {
		t.Fatal("retract delta is nil")
	}
	if retracted.Delta.Generation != session.Generation() {
		t.Fatalf("retract delta generation = %d, want %d", retracted.Delta.Generation, session.Generation())
	}
	if retracted.Delta.OldVersion != modified.Fact.Version() {
		t.Fatalf("retract old version = %d, want %d", retracted.Delta.OldVersion, modified.Fact.Version())
	}
	if retracted.Delta.SupportBefore.State != FactSupportStated {
		t.Fatalf("retract before support state = %q, want %q", retracted.Delta.SupportBefore.State, FactSupportStated)
	}
	if retracted.Delta.ActivationID != "" || retracted.Delta.RuleID != "" || retracted.Delta.RuleRevisionID != "" {
		t.Fatalf("external retract delta carried origin metadata: %#v", retracted.Delta)
	}

	resetResult, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if resetResult.Delta.Generation != session.Generation() {
		t.Fatalf("reset delta generation = %d, want %d", resetResult.Delta.Generation, session.Generation())
	}
	if resetResult.Delta.OldGeneration != retracted.Fact.Generation() {
		t.Fatalf("reset old generation = %d, want %d", resetResult.Delta.OldGeneration, retracted.Fact.Generation())
	}
	if resetResult.Delta.ActivationID != "" || resetResult.Delta.RuleID != "" || resetResult.Delta.RuleRevisionID != "" {
		t.Fatalf("external reset delta carried origin metadata: %#v", resetResult.Delta)
	}

	if len(collector.Events()) != 4 {
		t.Fatalf("expected 4 events for assert/modify/retract/reset workflow, got %d", len(collector.Events()))
	}
	modifyEvent := Event{}
	for _, event := range collector.Events() {
		if event.Type == EventFactModified {
			modifyEvent = event
			break
		}
	}
	if modifyEvent.Type != EventFactModified {
		t.Fatal("modify event missing from collector output")
	}
	if modifyEvent.Delta == nil {
		t.Fatal("modify event missing delta")
	}
	modifyEvent.Delta.OldDuplicate = DuplicateKey("tamper")
	if got := modified.Delta.OldDuplicate; got == DuplicateKey("tamper") {
		t.Fatalf("modify result duplicate metadata was not defensively copied")
	}
	modifyEvent.Delta.Before = nil
	if got := modified.Delta.Before; got == nil {
		t.Fatalf("modify result before-snapshot metadata was not defensively copied")
	}
}
