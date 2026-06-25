package gess

import (
	"context"
	"reflect"
	"testing"
)

func TestReteGraphPropagationEventCarriesModifyMetadata(t *testing.T) {
	revision, templateKey := mustModifyFastPathRuleset(t)
	before := FactSnapshot{
		id:          FactID{generation: 7, sequence: 1},
		name:        "person",
		templateKey: templateKey,
		generation:  7,
		fields: mustFields(t, map[string]any{
			"age":    32,
			"note":   "old",
			"status": "active",
		}),
	}
	after := FactSnapshot{
		id:          before.ID(),
		name:        "person",
		templateKey: templateKey,
		generation:  7,
		fields: mustFields(t, map[string]any{
			"age":    33,
			"note":   "new",
			"status": "active",
		}),
	}
	changes := []FieldChange{
		{Field: "note", Old: mustValue(t, "old"), New: mustValue(t, "new")},
		{Field: "age", Old: mustValue(t, 32), New: mustValue(t, 33)},
	}
	origin := mutationOrigin{RuleID: "origin-rule", RuleRevisionID: "origin-revision"}

	event := newReteGraphModifyEvent(revision, before, after, changes, true, origin, nil)

	if event.tag != reteGraphPropagationUpdate {
		t.Fatalf("event tag = %d, want update", event.tag)
	}
	if event.sourceGeneration != after.Generation() {
		t.Fatalf("source generation = %d, want %d", event.sourceGeneration, after.Generation())
	}
	if !event.duplicateChanged {
		t.Fatal("duplicateChanged = false, want true")
	}
	if event.nameChanged {
		t.Fatal("nameChanged = true, want false")
	}
	if event.templateChanged {
		t.Fatal("templateChanged = true, want false")
	}
	if event.origin != origin {
		t.Fatalf("origin = %#v, want %#v", event.origin, origin)
	}
	if got, want := event.changedSlots, []int{1, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changed slots = %#v, want %#v", got, want)
	}
	changes[0].Field = "mutated"
	if event.changes[0].Field != "note" {
		t.Fatalf("event changes alias caller changes: %#v", event.changes)
	}
}

func TestReteRuntimePropagatesAddRemoveAndUpdateEvents(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-propagation-event-session")
	session.attachPropagationCounters()

	asserted, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if got := session.propagationCounterSnapshot().Totals.TerminalDeltasEmitted; got != 1 {
		t.Fatalf("terminal deltas after add event = %d, want 1", got)
	}
	unmatched, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"age":    41,
		"note":   "old",
		"status": "inactive",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(unmatched): %v", err)
	}

	if _, err := session.Modify(ctx, unmatched.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"note": "new"})}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	counters := session.propagationCounterSnapshot().Totals
	if got := counters.ModifyFastPathSkips; got != 1 {
		t.Fatalf("modify fast-path skips after update event = %d, want 1", got)
	}
	if got := counters.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after update event = %d, want 0", got)
	}
	updated, ok := mustSnapshot(t, ctx, session).Fact(unmatched.Fact.ID())
	if !ok {
		t.Fatalf("modified fact %q not found", unmatched.Fact.ID())
	}
	note, ok := updated.Field("note")
	if !ok || !note.Equal(mustValue(t, "new")) {
		t.Fatalf("modified note = %v, ok=%t, want new", note, ok)
	}

	if _, err := session.Retract(ctx, asserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if got := session.propagationCounterSnapshot().Totals.TerminalDeltasRemoved; got != 1 {
		t.Fatalf("terminal deltas removed after remove event = %d, want 1", got)
	}
}
