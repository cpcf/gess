package engine

import (
	"context"
	"reflect"
	"strconv"
	"testing"
)

func TestAgendaBirthRankScratchOrdersValueTokensAcrossSeenWords(t *testing.T) {
	const conditionCount = 65
	rule := compiledRule{
		id:               "wide-rule",
		revisionID:       "wide-rule-revision",
		conditions:       make([]RuleCondition, conditionCount),
		declarationOrder: 1,
	}
	for slot := range rule.conditions {
		rule.conditions[slot] = RuleCondition{
			IDValue:     ConditionID("condition-" + strconv.Itoa(slot)),
			BindingName: "binding",
			Order:       slot,
		}
	}
	revision := &Ruleset{rulesByRevisionID: map[RuleRevisionID]compiledRule{rule.revisionID: rule}}
	agenda := newAgendaWithStrategy(StrategyBreadth)
	agenda.revision = revision

	first := &activation{
		ruleRevisionID:   rule.revisionID,
		token:            wideBirthRankToken(conditionCount, newStringValue("alpha"), false),
		declarationOrder: rule.declarationOrder,
		status:           activationStatusPending,
		key:              activationKey{ordinal: 2},
	}
	second := &activation{
		ruleRevisionID:   rule.revisionID,
		token:            wideBirthRankToken(conditionCount, newStringValue("beta"), true),
		declarationOrder: rule.declarationOrder,
		status:           activationStatusPending,
		key:              activationKey{ordinal: 1},
	}
	activations := []*activation{second, first}
	agenda.assignCanonicalBirthRanks(1, activations)

	if got, want := first.birthRank, uint64(1); got != want {
		t.Fatalf("alpha birth rank = %d, want %d", got, want)
	}
	if got, want := second.birthRank, uint64(2); got != want {
		t.Fatalf("beta birth rank = %d, want %d", got, want)
	}
	if cap(agenda.birthSeenScratch) < 2 {
		t.Fatalf("seen scratch capacity = %d, want at least 2 words", cap(agenda.birthSeenScratch))
	}
	if len(agenda.birthRecordScratch) != 0 || len(agenda.birthEntryScratch) != 0 || len(agenda.birthSeenScratch) != 0 {
		t.Fatal("birth rank scratch should be logically empty after ranking")
	}
}

func TestAgendaResetClearsAndRetainsBirthRankScratch(t *testing.T) {
	agenda := newAgendaWithStrategy(StrategyBreadth)
	baseHighWater := agendaMemoryHighWater(agenda)
	baseBytes := agendaMemoryRetainedBytes(agenda)
	act := &activation{module: "retained"}
	agenda.birthActivationScratch = append(make([]*activation, 0, 8), act)
	agenda.birthRecordScratch = append(make([]activationBirthRankRecord, 0, 8), activationBirthRankRecord{act: act})
	agenda.birthEntryScratch = append(make([]activationBirthSortEntry, 0, 8), activationBirthSortEntry{
		entry:             bindingTupleEntry{binding: "retained", value: newStringValue("retained"), hasValue: true},
		canonicalValueKey: "retained",
	})
	agenda.birthSeenScratch = append(make([]uint64, 0, 4), 1)
	if got, want := agendaMemoryHighWater(agenda)-baseHighWater, 28; got != want {
		t.Fatalf("birth scratch high-water contribution = %d, want %d", got, want)
	}
	wantBytes := sliceBytes[*activation](8) +
		sliceBytes[activationBirthRankRecord](8) +
		sliceBytes[activationBirthSortEntry](8) +
		sliceBytes[uint64](4)
	if got := agendaMemoryRetainedBytes(agenda) - baseBytes; got != wantBytes {
		t.Fatalf("birth scratch retained bytes contribution = %d, want %d", got, wantBytes)
	}

	agenda.reset()

	if cap(agenda.birthActivationScratch) != 8 || cap(agenda.birthRecordScratch) != 8 || cap(agenda.birthEntryScratch) != 8 || cap(agenda.birthSeenScratch) != 4 {
		t.Fatalf("reset changed birth scratch capacities: activations=%d records=%d entries=%d seen=%d",
			cap(agenda.birthActivationScratch), cap(agenda.birthRecordScratch), cap(agenda.birthEntryScratch), cap(agenda.birthSeenScratch))
	}
	if agenda.birthActivationScratch[:1][0] != nil {
		t.Fatal("reset retained activation pointer in birth scratch")
	}
	if got := agenda.birthRecordScratch[:1][0]; got != (activationBirthRankRecord{}) {
		t.Fatalf("reset retained birth record: %#v", got)
	}
	if got := agenda.birthEntryScratch[:1][0]; !reflect.DeepEqual(got, activationBirthSortEntry{}) {
		t.Fatalf("reset retained birth entry: %#v", got)
	}
	if agenda.birthSeenScratch[:1][0] != 0 {
		t.Fatal("reset retained seen bits")
	}
}

func TestAgendaBirthActivationScratchClearsAfterDeltaError(t *testing.T) {
	rule := compiledRule{
		id:               "valid-rule",
		revisionID:       "valid-rule-revision",
		conditions:       []RuleCondition{{IDValue: "condition", BindingName: "fact"}},
		declarationOrder: 1,
	}
	revision := &Ruleset{rulesByRevisionID: map[RuleRevisionID]compiledRule{rule.revisionID: rule}}
	factID := newFactID(1, 1)
	entry := bindingTupleEntry{binding: "fact", bindingSlot: 0, factID: factID, factVersion: 1}
	valid := matchCandidate{
		ruleID:         rule.id,
		ruleRevisionID: rule.revisionID,
		identity:       candidateIdentityFor(rule.id, rule.revisionID, 0, 1, []bindingTupleEntry{entry}),
		bindingTuple:   []bindingTupleEntry{entry},
		factIDs:        []FactID{factID},
		factVersions:   []FactVersion{1},
		generation:     1,
	}
	invalid := matchCandidate{
		ruleID:         "missing-rule",
		ruleRevisionID: "missing-rule-revision",
		factIDs:        []FactID{newFactID(1, 2)},
		factVersions:   []FactVersion{1},
	}

	agenda := newAgendaWithStrategy(StrategyBreadth)
	agenda.birthActivationScratch = make([]*activation, 0, 2)
	if _, err := agenda.applyCandidateDeltas(context.Background(), revision, nil, []matchCandidate{invalid, valid}); err == nil {
		t.Fatal("applyCandidateDeltas error = nil, want unknown-rule error")
	}
	if len(agenda.birthActivationScratch) != 0 {
		t.Fatalf("birth activation scratch len = %d, want 0", len(agenda.birthActivationScratch))
	}
	for i, act := range agenda.birthActivationScratch[:cap(agenda.birthActivationScratch)] {
		if act != nil {
			t.Fatalf("birth activation scratch[%d] retained activation", i)
		}
	}
}

func wideBirthRankToken(conditionCount int, finalValue Value, reverse bool) tokenRef {
	arena := newTokenArena()
	addSlot := func(parent tokenRef, slot int) tokenRef {
		if slot == conditionCount-1 {
			match := conditionMatch{bindingSlot: slot, value: finalValue, hasValue: true}
			return arena.addCompact(parent, tokenRowEntry{bindingSlot: slot, value: finalValue, hasValue: true}, match, 0, 1)
		}
		fact := FactSnapshot{id: newFactID(1, uint64(slot+1)), version: 1, generation: 1}
		match := conditionMatch{bindingSlot: slot, fact: newConditionFactRefFromSnapshot(fact)}
		return arena.addCompact(parent, tokenRowEntry{bindingSlot: slot, factID: fact.ID(), factVersion: fact.Version()}, match, 0, 1)
	}
	var token tokenRef
	if reverse {
		for slot := conditionCount - 1; slot >= 0; slot-- {
			token = addSlot(token, slot)
		}
		return token
	}
	for slot := range conditionCount {
		token = addSlot(token, slot)
	}
	return token
}
