package engine

// sessionTMSStore owns the mutable truth-maintenance indexes and counters for
// one session. It is intentionally a named Session member so fork and rollback
// ownership cannot be hidden among unrelated session state.
type sessionTMSStore struct {
	logicalSupportEdges    map[SupportID]logicalSupportEdgeRecord
	logicalSupportBySource map[logicalSupportSourceKey]map[SupportID]struct{}
	logicalSupportByFact   map[FactID]map[SupportID]struct{}
	logicalSupportCounters LogicalSupportCounters
}

func (s sessionTMSStore) cloneForFork() sessionTMSStore {
	return sessionTMSStore{
		logicalSupportEdges:    cloneLogicalSupportEdges(s.logicalSupportEdges),
		logicalSupportBySource: cloneLogicalSupportSourceIndex(s.logicalSupportBySource),
		logicalSupportByFact:   cloneLogicalSupportFactIndex(s.logicalSupportByFact),
		logicalSupportCounters: s.logicalSupportCounters,
	}
}

func (s *sessionTMSStore) clear() {
	if s == nil {
		return
	}
	clear(s.logicalSupportEdges)
	clear(s.logicalSupportBySource)
	clear(s.logicalSupportByFact)
	s.logicalSupportCounters.CurrentLogicalFacts = 0
	s.logicalSupportCounters.CurrentStatedAndLogicalFacts = 0
	s.logicalSupportCounters.CurrentSupportEdges = 0
}

func (s sessionTMSStore) capture() logicalSupportState {
	return logicalSupportState{
		edges:    cloneLogicalSupportEdges(s.logicalSupportEdges),
		bySource: cloneLogicalSupportSourceIndex(s.logicalSupportBySource),
		byFact:   cloneLogicalSupportFactIndex(s.logicalSupportByFact),
		counters: s.logicalSupportCounters,
	}
}

func (s *sessionTMSStore) restore(state logicalSupportState) {
	if s == nil {
		return
	}
	s.logicalSupportEdges = state.edges
	s.logicalSupportBySource = state.bySource
	s.logicalSupportByFact = state.byFact
	s.logicalSupportCounters = state.counters
}
