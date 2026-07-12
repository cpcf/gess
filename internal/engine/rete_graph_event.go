package engine

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
	tag               reteGraphPropagationTag
	fact              FactSnapshot
	workingFact       *workingFact
	before            FactSnapshot
	beforeWorkingFact *workingFact
	after             FactSnapshot
	afterWorkingFact  *workingFact
	changes           []FieldChange
	changedSlots      []int
	duplicateChanged  bool
	nameChanged       bool
	templateChanged   bool
	generated         bool
	sourceGeneration  Generation
	updateSource      bool
	transient         bool
	origin            mutationOrigin
	span              *propagationCounterSpan
	counters          *propagationCounterLedger
	allocationSource  propagationAllocationSource
}

func newReteGraphAssertEvent(fact FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             fact,
		after:            fact,
		sourceGeneration: fact.Generation(),
		updateSource:     true,
		origin:           origin,
		span:             span,
		allocationSource: propagationAllocationSource{templateKey: fact.TemplateKey(), kind: propagationMutationAssert},
	}
}

func newReteGraphWorkingAssertEvent(fact *workingFact, snapshot FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             snapshot,
		workingFact:      fact,
		after:            snapshot,
		sourceGeneration: snapshot.Generation(),
		updateSource:     true,
		origin:           origin,
		span:             span,
		allocationSource: propagationAllocationSource{templateKey: snapshot.TemplateKey(), kind: propagationMutationAssert},
	}
}

func newReteGraphResetAssertEvent(fact FactSnapshot) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             fact,
		after:            fact,
		sourceGeneration: fact.Generation(),
		allocationSource: propagationAllocationSource{templateKey: fact.TemplateKey(), kind: propagationMutationAssert},
	}
}

func newReteGraphResetWorkingAssertEvent(fact *workingFact, snapshot FactSnapshot) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             snapshot,
		workingFact:      fact,
		after:            snapshot,
		sourceGeneration: snapshot.Generation(),
		allocationSource: propagationAllocationSource{templateKey: snapshot.TemplateKey(), kind: propagationMutationAssert},
	}
}

func newReteGraphGeneratedAssertEvent(fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) reteGraphPropagationEvent {
	var generation Generation
	if fact != nil {
		generation = fact.id.Generation()
	}
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		workingFact:      fact,
		generated:        true,
		sourceGeneration: generation,
		origin:           origin,
		span:             span,
		allocationSource: propagationAllocationSource{templateKey: workingFactTemplateKey(fact), kind: propagationMutationAssert},
	}
}

func newReteGraphQueryTriggerEvent(fact FactSnapshot) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationAdd,
		fact:             fact,
		after:            fact,
		sourceGeneration: fact.Generation(),
		transient:        true,
		allocationSource: propagationAllocationSource{templateKey: fact.TemplateKey(), kind: propagationMutationAssert},
	}
}

func newReteGraphQueryTriggerRemoveEvent(fact FactSnapshot) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationRemove,
		fact:             fact,
		before:           fact,
		sourceGeneration: fact.Generation(),
		transient:        true,
		allocationSource: propagationAllocationSource{templateKey: fact.TemplateKey(), kind: propagationMutationRetract},
	}
}

func newReteGraphWorkingRetractEvent(fact *workingFact, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	var generation Generation
	if fact != nil {
		generation = fact.id.Generation()
	}
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationRemove,
		workingFact:      fact,
		sourceGeneration: generation,
		origin:           origin,
		counters:         counters,
		allocationSource: propagationAllocationSource{templateKey: workingFactTemplateKey(fact), kind: propagationMutationRetract},
	}
}

func newReteGraphGeneratedRetractEvent(fact *workingFact, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	event := newReteGraphWorkingRetractEvent(fact, origin, counters)
	event.generated = true
	return event
}

func newReteGraphRetractEvent(fact FactSnapshot, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationRemove,
		fact:             fact,
		before:           fact,
		sourceGeneration: fact.Generation(),
		origin:           origin,
		counters:         counters,
		allocationSource: propagationAllocationSource{templateKey: fact.TemplateKey(), kind: propagationMutationRetract},
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
		allocationSource: propagationAllocationSource{templateKey: after.TemplateKey(), kind: propagationMutationModify},
	}
}

func newReteGraphWorkingModifyEvent(revision *Ruleset, before FactSnapshot, beforeFact *workingFact, afterFact *workingFact, after FactSnapshot, changes []FieldChange, duplicateChanged bool, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	event := newReteGraphModifyEvent(revision, before, after, changes, duplicateChanged, origin, counters)
	event.beforeWorkingFact = beforeFact
	event.afterWorkingFact = afterFact
	return event
}

func newReteGraphModifyRemoveEvent(event reteGraphPropagationEvent) reteGraphPropagationEvent {
	event.tag = reteGraphPropagationModifyRemove
	event.fact = event.before
	event.workingFact = event.beforeWorkingFact
	event.allocationSource = propagationAllocationSource{templateKey: event.before.TemplateKey(), kind: propagationMutationModify}
	return event
}

func newReteGraphModifyAddEvent(event reteGraphPropagationEvent) reteGraphPropagationEvent {
	event.tag = reteGraphPropagationModifyAdd
	event.fact = event.after
	event.workingFact = event.afterWorkingFact
	event.allocationSource = propagationAllocationSource{templateKey: event.after.TemplateKey(), kind: propagationMutationModify}
	return event
}

func workingFactTemplateKey(fact *workingFact) TemplateKey {
	if fact == nil {
		return ""
	}
	return fact.storedTemplateKey()
}

func newReteGraphClearEvent(generation Generation, origin mutationOrigin, counters *propagationCounterLedger) reteGraphPropagationEvent {
	return reteGraphPropagationEvent{
		tag:              reteGraphPropagationClear,
		sourceGeneration: generation,
		origin:           origin,
		counters:         counters,
	}
}

func reteGraphFactsGeneration(facts []FactSnapshot) Generation {
	for _, fact := range facts {
		if !fact.ID().IsZero() {
			return fact.Generation()
		}
	}
	return 0
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
