package engine

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

func TestReteGraphModifySummaryConsumesEventChangedSlots(t *testing.T) {
	event := reteGraphPropagationEvent{
		changes: []FieldChange{
			{Field: "note", Old: mustValue(t, "old"), New: mustValue(t, "new")},
		},
	}

	summary := newFactModifySummaryFromPropagationEvent(event)
	if summary.knownSlotChange() {
		t.Fatal("summary with missing changed slots reported known slot change")
	}

	event.changedSlots = []int{3, 1, 3}
	summary = newFactModifySummaryFromPropagationEvent(event)
	if !summary.knownSlotChange() {
		t.Fatal("summary with event changed slots did not report known slot change")
	}
	if got, want := summary.changedSlotCount, 2; got != want {
		t.Fatalf("changed slot count = %d, want %d", got, want)
	}
	if got, want := summary.changedSlots[0], 1; got != want {
		t.Fatalf("first changed slot = %d, want %d", got, want)
	}
	if got, want := summary.changedSlots[1], 3; got != want {
		t.Fatalf("second changed slot = %d, want %d", got, want)
	}
}

func TestReteGraphModifyAddRemoveEventsPropagateWithoutFactSourceMutation(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-modify-add-remove-event-session")
	asserted, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	before, ok := mustSnapshot(t, ctx, session).Fact(asserted.Fact.ID())
	if !ok {
		t.Fatalf("before fact %q not found", asserted.Fact.ID())
	}
	beforeState := session.activeFactWorkspace()
	beforeFact, ok := beforeState.workingFactByID(asserted.Fact.ID())
	if !ok {
		t.Fatalf("before working fact %q not found", asserted.Fact.ID())
	}
	if _, err := session.Modify(ctx, asserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"note": "new"})}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	after, ok := mustSnapshot(t, ctx, session).Fact(asserted.Fact.ID())
	if !ok {
		t.Fatalf("after fact %q not found", asserted.Fact.ID())
	}
	state := session.activeFactWorkspace()
	afterFact, ok := state.workingFactByID(asserted.Fact.ID())
	if !ok {
		t.Fatalf("after working fact %q not found", asserted.Fact.ID())
	}
	memory, err := newReteGraphBetaMemoryForGeneration(ctx, revision, revision.graph, []FactSnapshot{before}, session.Generation(), nil)
	if err != nil {
		t.Fatalf("newReteGraphBetaMemoryForGeneration: %v", err)
	}
	memory.compactSlotStore = state.compactSlotStore
	event := newReteGraphWorkingModifyEvent(revision, before, beforeFact, afterFact, after, []FieldChange{
		{Field: "note", Old: mustValue(t, "old"), New: mustValue(t, "new")},
	}, false, mutationOrigin{}, nil)

	removed, err := memory.propagateEvent(ctx, newReteGraphModifyRemoveEvent(event))
	if err != nil {
		t.Fatalf("propagate modify-remove: %v", err)
	}
	if got, want := len(removed.removed), 1; got != want {
		t.Fatalf("modify-remove terminal removals = %d, want %d", got, want)
	}
	if got, want := memory.sourceGeneration(), before.Generation(); got != want {
		t.Fatalf("source generation after modify-remove = %d, want %d", got, want)
	}

	added, err := memory.propagateEvent(ctx, newReteGraphModifyAddEvent(event))
	if err != nil {
		t.Fatalf("propagate modify-add: %v", err)
	}
	if got, want := len(added.added), 1; got != want {
		t.Fatalf("modify-add terminal additions = %d, want %d", got, want)
	}
	if got, want := memory.sourceGeneration(), after.Generation(); got != want {
		t.Fatalf("source generation after modify-add = %d, want %d", got, want)
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

func TestReteGraphPropagationEventCarriesQueryTriggerMetadata(t *testing.T) {
	revision, _ := mustQueryRevision(t)
	query, ok := revision.query("adults-by-dept")
	if !ok {
		t.Fatal("query adults-by-dept not found")
	}
	args, err := query.compileArgs(QueryArgs{"dept": "engineering"})
	if err != nil {
		t.Fatalf("compileArgs: %v", err)
	}
	trigger := snapshotQueryTriggerFact(Generation(11), query, &args)

	event := newReteGraphQueryTriggerEvent(trigger)

	if event.tag != reteGraphPropagationAdd {
		t.Fatalf("event tag = %d, want add", event.tag)
	}
	if !event.transient {
		t.Fatal("query trigger event transient = false, want true")
	}
	if event.fact.ID() != trigger.ID() || event.after.ID() != trigger.ID() {
		t.Fatalf("event trigger IDs = (%#v, %#v), want %#v", event.fact.ID(), event.after.ID(), trigger.ID())
	}
	if event.sourceGeneration != trigger.Generation() {
		t.Fatalf("source generation = %d, want %d", event.sourceGeneration, trigger.Generation())
	}
	if event.fact.Name() != internalQueryTriggerName("adults-by-dept") {
		t.Fatalf("trigger fact name = %q, want query trigger name", event.fact.Name())
	}

	remove := newReteGraphQueryTriggerRemoveEvent(trigger)
	if remove.tag != reteGraphPropagationRemove {
		t.Fatalf("remove event tag = %d, want remove", remove.tag)
	}
	if !remove.transient {
		t.Fatal("query trigger remove event transient = false, want true")
	}
	if remove.fact.ID() != trigger.ID() || remove.before.ID() != trigger.ID() {
		t.Fatalf("remove event trigger IDs = (%#v, %#v), want %#v", remove.fact.ID(), remove.before.ID(), trigger.ID())
	}
}

func TestReteGraphPropagationEventCarriesGeneratedMetadata(t *testing.T) {
	revision, templateKey := mustCompileGeneratedFactInsertRuleset(t)
	templateID, ok := revision.templateIDByKey(templateKey)
	if !ok {
		t.Fatalf("generated template %q missing id", templateKey)
	}
	fact := &workingFact{
		id:           newFactID(12, 34),
		version:      2,
		recency:      3,
		supportState: factSupportCodeFromState(FactSupportLogical),
	}
	fact.setTemplateIdentity(templateKey, templateID)
	fact.setName("generated-fact")
	slots := []factSlot{
		{value: mustValue(t, "kind-a"), ok: true},
		{value: mustValue(t, 7), ok: true},
		{value: mustValue(t, "stream-a"), ok: true},
	}
	fact.setFieldSlots(slots)

	event := newReteGraphGeneratedAssertEvent(fact, mutationOrigin{}, nil)

	if event.tag != reteGraphPropagationAdd {
		t.Fatalf("event tag = %d, want add", event.tag)
	}
	if !event.generated {
		t.Fatal("generated assert event generated = false, want true")
	}
	if event.workingFact != fact {
		t.Fatal("generated assert event lost working fact handle")
	}
	if !event.fact.ID().IsZero() || !event.after.ID().IsZero() {
		t.Fatalf("generated event materialized public snapshots (%#v, %#v), want none", event.fact.ID(), event.after.ID())
	}
	if event.sourceGeneration != fact.id.Generation() {
		t.Fatalf("generated event source generation = %d, want %d", event.sourceGeneration, fact.id.Generation())
	}

	remove := newReteGraphGeneratedRetractEvent(fact, mutationOrigin{}, nil)
	if remove.tag != reteGraphPropagationRemove {
		t.Fatalf("remove event tag = %d, want remove", remove.tag)
	}
	if !remove.generated {
		t.Fatal("generated retract event generated = false, want true")
	}
	if remove.workingFact != fact {
		t.Fatal("generated retract event lost working fact handle")
	}
}

func TestReteGraphClearEventClearsRetainedMemory(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-clear-event-seed-session")
	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	facts := mustSnapshot(t, ctx, session).Facts()
	memory, err := newReteGraphBetaMemoryForGeneration(ctx, revision, revision.graph, facts, session.Generation(), nil)
	if err != nil {
		t.Fatalf("newReteGraphBetaMemoryForGeneration: %v", err)
	}
	if got := len(memory.alpha.factCounts); got == 0 {
		t.Fatal("alpha fact counts before clear = 0, want retained graph memory")
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
	if got := len(memory.alpha.factCounts); got != 0 {
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
	if _, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	facts := mustSnapshot(t, ctx, session).Facts()
	memory, err := newReteGraphBetaMemoryForGeneration(ctx, revision, revision.graph, facts, session.Generation(), nil)
	if err != nil {
		t.Fatalf("newReteGraphBetaMemoryForGeneration: %v", err)
	}

	if err := memory.resetFactsForGeneration(ctx, nil, Generation(9)); err != nil {
		t.Fatalf("resetFactsForGeneration(empty): %v", err)
	}
	if got := memory.memoryStats().TokenRows; got != 0 {
		t.Fatalf("token rows after empty reset = %d, want 0", got)
	}
	if got := memory.sourceGeneration(); got != 9 {
		t.Fatalf("source generation after empty reset = %d, want 9", got)
	}

	if err := memory.resetFactsForGeneration(ctx, facts, Generation(10)); err != nil {
		t.Fatalf("resetFactsForGeneration(facts): %v", err)
	}
	if got := memory.sourceGeneration(); got != 10 {
		t.Fatalf("source generation after reassert reset = %d, want 10", got)
	}
}

func TestReteRuntimePropagatesAddRemoveAndUpdateEvents(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-propagation-event-session")
	session.attachPropagationCounters()

	asserted, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{
		"age":    32,
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if got := session.propagationCounterSnapshot().Totals.TerminalDeltasEmitted; got != 1 {
		t.Fatalf("terminal deltas after add event = %d, want 1", got)
	}
	unmatched, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{
		"age":    41,
		"note":   "old",
		"status": "inactive",
	}))
	if err != nil {
		t.Fatalf("Assert(unmatched): %v", err)
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
