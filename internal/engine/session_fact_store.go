package engine

// sessionFactStore owns the mutable fact memory for one Session. Transactional
// operations project this state into a factWorkspace and commit or swap it as
// one unit.
type sessionFactStore struct {
	generation             Generation
	nextFactSequence       uint64
	nextRecency            Recency
	facts                  []workingFact
	compactFacts           compactFactStore
	factsByID              map[FactID]int
	factsBySequence        []int32
	factsByDuplicate       duplicateIndexes
	factsByTemplate        map[TemplateKey][]FactID
	factsByName            map[string][]FactID
	factFieldEqualIndexes  map[factFieldEqualKey][]FactSnapshot
	factTargetIndexesDirty bool
	insertionOrder         []FactID
	slotStorage            []factSlot
	compactSlotStore       *factCompactSlotStore
	resetWorkspace         factWorkspace
}

func newSessionFactStore(state *factWorkspace) sessionFactStore {
	if state == nil {
		return sessionFactStore{}
	}
	return sessionFactStore{
		generation:             state.generation,
		nextFactSequence:       state.nextFactSequence(),
		nextRecency:            state.nextRecency(),
		facts:                  state.facts,
		compactFacts:           state.compactFacts,
		factsByID:              state.factsByID,
		factsBySequence:        state.factsBySequence,
		factsByDuplicate:       state.factsByDuplicate,
		factsByTemplate:        state.factsByTemplate,
		factsByName:            state.factsByName,
		factTargetIndexesDirty: state.factTargetIndexesDirty,
		insertionOrder:         state.factsByInsertionOrder(),
		slotStorage:            state.slotStorage,
		compactSlotStore:       state.compactSlotStore,
	}
}

func (s *sessionFactStore) workspace() factWorkspace {
	if s == nil {
		return factWorkspace{}
	}
	return factWorkspace{
		generation:             s.generation,
		sequence:               s.nextFactSequence,
		recency:                s.nextRecency,
		facts:                  s.facts,
		compactFacts:           s.compactFacts,
		insertionOrder:         s.insertionOrder,
		factsByID:              s.factsByID,
		factsBySequence:        s.factsBySequence,
		factsByDuplicate:       s.factsByDuplicate,
		factsByTemplate:        s.factsByTemplate,
		factsByName:            s.factsByName,
		factTargetIndexesDirty: s.factTargetIndexesDirty,
		slotStorage:            s.slotStorage,
		compactSlotStore:       s.compactSlotStore,
	}
}

func (s *sessionFactStore) clonedWorkspace() factWorkspace {
	state := s.workspace()
	state.compactSlotStore = cloneFactCompactSlotStore(state.compactSlotStore)
	state.facts = cloneWorkingFacts(state.facts)
	state.compactFacts = cloneCompactFactStore(state.compactFacts)
	state.insertionOrder = cloneFactIDs(state.insertionOrder)
	state.factsByID = cloneFactIDIndex(state.factsByID)
	state.factsBySequence = cloneFactRowSequenceIndex(state.factsBySequence)
	state.factsByDuplicate = cloneDuplicateIndexes(state.factsByDuplicate)
	state.factsByTemplate = cloneFactIDSliceMap(state.factsByTemplate)
	state.factsByName = cloneStringFactIDSliceMap(state.factsByName)
	state.slotStorage = cloneFactSlots(state.slotStorage)
	return state
}

func (s *sessionFactStore) commit(state factWorkspace) {
	if s == nil {
		return
	}
	s.nextFactSequence = state.sequence
	s.nextRecency = state.recency
	s.facts = state.facts
	s.compactFacts = state.compactFacts
	s.factsByID = state.factsByID
	s.factsBySequence = state.factsBySequence
	s.factsByDuplicate = state.factsByDuplicate
	s.factsByTemplate = state.factsByTemplate
	s.factsByName = state.factsByName
	s.factTargetIndexesDirty = state.factTargetIndexesDirty
	s.factFieldEqualIndexes = nil
	s.insertionOrder = state.insertionOrder
	s.slotStorage = state.slotStorage
	s.compactSlotStore = state.compactSlotStore
}

// swap installs transactional fact memory while leaving generation ownership
// with the caller. Reset publishes its new generation only after the Rete and
// support transitions have succeeded.
func (s *sessionFactStore) swap(workspace *factWorkspace) {
	if s == nil || workspace == nil {
		return
	}
	s.nextFactSequence, workspace.sequence = workspace.sequence, s.nextFactSequence
	s.nextRecency, workspace.recency = workspace.recency, s.nextRecency
	s.facts, workspace.facts = workspace.facts, s.facts
	s.compactFacts, workspace.compactFacts = workspace.compactFacts, s.compactFacts
	s.factsByID, workspace.factsByID = workspace.factsByID, s.factsByID
	s.factsBySequence, workspace.factsBySequence = workspace.factsBySequence, s.factsBySequence
	s.factsByDuplicate, workspace.factsByDuplicate = workspace.factsByDuplicate, s.factsByDuplicate
	s.factsByTemplate, workspace.factsByTemplate = workspace.factsByTemplate, s.factsByTemplate
	s.factsByName, workspace.factsByName = workspace.factsByName, s.factsByName
	s.factTargetIndexesDirty, workspace.factTargetIndexesDirty = workspace.factTargetIndexesDirty, s.factTargetIndexesDirty
	s.factFieldEqualIndexes = nil
	s.insertionOrder, workspace.insertionOrder = workspace.insertionOrder, s.insertionOrder
	s.slotStorage, workspace.slotStorage = workspace.slotStorage, s.slotStorage
	s.compactSlotStore, workspace.compactSlotStore = workspace.compactSlotStore, s.compactSlotStore
}
