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

func TestReteGraphPropagationEventCarriesClearMetadata(t *testing.T) {
	origin := mutationOrigin{RuleID: "reset-rule", RuleRevisionID: "reset-revision"}

	event := newReteGraphClearEvent(Generation(9), origin, nil)

	if event.tag != reteGraphPropagationClear {
		t.Fatalf("event tag = %d, want clear", event.tag)
	}
	if event.sourceGeneration != 9 {
		t.Fatalf("source generation = %d, want 9", event.sourceGeneration)
	}
	if event.origin != origin {
		t.Fatalf("origin = %#v, want %#v", event.origin, origin)
	}
}

func TestReteGraphClearEventClearsRetainedMemory(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-clear-event-seed-session")
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	facts := mustSnapshot(t, ctx, session).Facts()
	memory, err := newReteGraphBetaMemoryForGeneration(ctx, revision, revision.graph, facts, session.Generation())
	if err != nil {
		t.Fatalf("newReteGraphBetaMemoryForGeneration: %v", err)
	}
	if got := memory.memoryStats().TokenRows; got == 0 {
		t.Fatal("token rows before clear = 0, want retained graph memory")
	}

	delta, err := memory.propagateEvent(ctx, newReteGraphClearEvent(Generation(9), mutationOrigin{}, nil))
	if err != nil {
		t.Fatalf("propagate clear event: %v", err)
	}
	if !delta.supported {
		t.Fatal("clear event delta unsupported")
	}
	if got := memory.memoryStats().TokenRows; got != 0 {
		t.Fatalf("token rows after clear = %d, want 0", got)
	}
	if got := len(memory.alphaFactCounts); got != 0 {
		t.Fatalf("alpha fact counts after clear = %d, want 0", got)
	}
	if !memory.rootToken.isZero() {
		t.Fatalf("root token after clear = %#v, want zero", memory.rootToken)
	}
}

func TestReteGraphResetFactsClearsThroughEventAndReassertsFacts(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-reset-event-seed-session")
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	facts := mustSnapshot(t, ctx, session).Facts()
	memory, err := newReteGraphBetaMemoryForGeneration(ctx, revision, revision.graph, facts, session.Generation())
	if err != nil {
		t.Fatalf("newReteGraphBetaMemoryForGeneration: %v", err)
	}

	if err := memory.resetFactsForGeneration(ctx, nil, Generation(9)); err != nil {
		t.Fatalf("resetFactsForGeneration(empty): %v", err)
	}
	if got := memory.memoryStats().TokenRows; got != 0 {
		t.Fatalf("token rows after empty reset = %d, want 0", got)
	}
	if got := len(memory.facts); got != 0 {
		t.Fatalf("source facts after empty reset = %d, want 0", got)
	}

	if err := memory.resetFactsForGeneration(ctx, facts, Generation(10)); err != nil {
		t.Fatalf("resetFactsForGeneration(facts): %v", err)
	}
	deltas, ok, err := memory.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok {
		t.Fatal("currentTerminalTokenDeltas unavailable")
	}
	if got, want := len(deltas), 1; got != want {
		t.Fatalf("terminal deltas after reassert reset = %d, want %d", got, want)
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
