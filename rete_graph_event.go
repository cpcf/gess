package gess

import "slices"

type reteGraphPropagationTag uint8

const (
	reteGraphPropagationAdd reteGraphPropagationTag = iota + 1
	reteGraphPropagationRemove
	reteGraphPropagationUpdate
	reteGraphPropagationModifyAdd
	reteGraphPropagationModifyRemove
	reteGraphPropagationClear
)

type reteGraphPropagationEvent struct {
	tag              reteGraphPropagationTag
	fact             FactSnapshot
	workingFact      *workingFact
	before           FactSnapshot
	after            FactSnapshot
	changes          []FieldChange
	changedSlots     []int
	duplicateChanged bool
	nameChanged      bool
	templateChanged  bool
	sourceGeneration Generation
	origin           mutationOrigin
	span             *propagationCounterSpan
	counters         *propagationCounterLedger
}

func newReteGraphAssertEvent(fact FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             fact,
		after:            fact,
		sourceGeneration: fact.Generation(),
		origin:           origin,
		span:             span,
	}
}

func newReteGraphGeneratedAssertEvent(fact *workingFact, revision *Ruleset, origin mutationOrigin, span *propagationCounterSpan) reteGraphPropagationEvent {
	var snapshot FactSnapshot
	if fact != nil {
		snapshot = fact.snapshotForRevision(revision)
	}
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             snapshot,
		workingFact:      fact,
		after:            snapshot,
		sourceGeneration: snapshot.Generation(),
		origin:           origin,
		span:             span,
	}
}

func newReteGraphRetractEvent(fact FactSnapshot, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationRemove,
		fact:             fact,
		before:           fact,
		sourceGeneration: fact.Generation(),
		origin:           origin,
		counters:         counters,
	}
}

func newReteGraphModifyEvent(revision *Ruleset, before, after FactSnapshot, changes []FieldChange, duplicateChanged bool, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationUpdate,
		fact:             after,
		before:           before,
		after:            after,
		changes:          cloneFieldChanges(changes),
		changedSlots:     changedFieldSlotsForPropagationEvent(revision, after.TemplateKey(), changes),
		duplicateChanged: duplicateChanged,
		nameChanged:      before.Name() != after.Name(),
		templateChanged:  before.TemplateKey() != after.TemplateKey(),
		sourceGeneration: after.Generation(),
		origin:           origin,
		counters:         counters,
	}
}

func newReteGraphClearEvent(generation Generation, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationClear,
		sourceGeneration: generation,
		origin:           origin,
		counters:         counters,
	}
}

func cloneFieldChanges(in []FieldChange) []FieldChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]FieldChange, len(in))
	for i, change := range in {
		out[i] = FieldChange{
			Field: change.Field,
			Old:   cloneValue(change.Old),
			New:   cloneValue(change.New),
		}
	}
	return out
}

func changedFieldSlotsForPropagationEvent(revision *Ruleset, templateKey TemplateKey, changes []FieldChange) []int {
	if revision == nil || templateKey == "" || len(changes) == 0 {
		return nil
	}
	template, ok := revision.templateByKey(templateKey)
	if !ok {
		return nil
	}
	slots := make([]int, 0, len(changes))
	for _, change := range changes {
		slot, ok := template.fieldSlot(change.Field)
		if !ok || slot < 0 || intSliceContains(slots, slot) {
			continue
		}
		slots = append(slots, slot)
	}
	return slots
}

func intSliceContains(values []int, value int) bool {
	return slices.Contains(values, value)
}
