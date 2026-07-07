package engine

import (
	"context"
	"slices"
	"sort"
	"strconv"
)

type LogicalSupportEdge struct {
	SupportID       SupportID
	FactID          FactID
	RuleID          RuleID
	RuleRevisionID  RuleRevisionID
	ActivationID    ActivationID
	Generation      Generation
	SupportingFacts []FactID
}

func (e LogicalSupportEdge) clone() LogicalSupportEdge {
	out := e
	if len(e.SupportingFacts) > 0 {
		out.SupportingFacts = cloneFactIDs(e.SupportingFacts)
	}
	return out
}

type LogicalSupportCounters struct {
	CurrentLogicalFacts          int
	CurrentStatedAndLogicalFacts int
	CurrentSupportEdges          int
	LogicalFactsAsserted         int
	LogicalFactsRetracted        int
	SupportEdgesAdded            int
	SupportEdgesRemoved          int
	MetadataOnlyTransitions      int
	CascadeRetractions           int
	CascadeBreadthMax            int
	CascadeDepthMax              int
}

type SupportGraph struct {
	Generation Generation
	Edges      []LogicalSupportEdge
	Counters   LogicalSupportCounters
}

func (g SupportGraph) clone() SupportGraph {
	out := g
	if len(g.Edges) > 0 {
		out.Edges = make([]LogicalSupportEdge, len(g.Edges))
		for i, edge := range g.Edges {
			out.Edges[i] = edge.clone()
		}
	}
	return out
}

type logicalSupportSourceKey struct {
	generation     Generation
	ruleRevisionID RuleRevisionID
	identityKey    candidateIdentityKey
}

type logicalSupportEdgeRecord struct {
	edge   LogicalSupportEdge
	source logicalSupportSourceKey
}

type logicalSupportState struct {
	edges    map[SupportID]logicalSupportEdgeRecord
	bySource map[logicalSupportSourceKey]map[SupportID]struct{}
	byFact   map[FactID]map[SupportID]struct{}
	counters LogicalSupportCounters
}

type logicalSupportMemory struct {
	session *Session
}

func (s *Session) logicalSupportMemory() logicalSupportMemory {
	return logicalSupportMemory{session: s}
}

func logicalSupportID(source logicalSupportSourceKey, factID FactID) SupportID {
	if source.identityKey == (candidateIdentityKey{}) || factID.IsZero() {
		return ""
	}
	return SupportID("support:v2:" + source.ruleRevisionID.String() + ":" + strconv.FormatUint(source.identityKey.scopeHash, 10) + ":" + strconv.FormatUint(source.identityKey.hash, 10) + ":" + factID.String())
}

func logicalSupportSourceFromOrigin(origin mutationOrigin, generation Generation) (logicalSupportSourceKey, bool) {
	if origin.activationIdentityKey == (candidateIdentityKey{}) || origin.RuleRevisionID.IsZero() {
		return logicalSupportSourceKey{}, false
	}
	return logicalSupportSourceKey{
		generation:     generation,
		ruleRevisionID: origin.RuleRevisionID,
		identityKey:    origin.activationIdentityKey,
	}, true
}

func logicalSupportSourceFromPropagationEvent(event reteGraphPropagationEvent) (logicalSupportSourceKey, bool) {
	return logicalSupportSourceFromOrigin(event.origin, event.sourceGeneration)
}

func logicalSupportSourceFromActivation(activation activation) logicalSupportSourceKey {
	return logicalSupportSourceKey{
		generation:     activation.Generation(),
		ruleRevisionID: activation.ruleRevisionID,
		identityKey:    activation.identityKey,
	}
}

func (s *Session) ensureLogicalSupportMaps() {
	s.logicalSupportMemory().ensure()
}

func (m logicalSupportMemory) ensure() {
	s := m.session
	if s == nil {
		return
	}
	if s.logicalSupportEdges == nil {
		s.logicalSupportEdges = make(map[SupportID]logicalSupportEdgeRecord)
	}
	if s.logicalSupportBySource == nil {
		s.logicalSupportBySource = make(map[logicalSupportSourceKey]map[SupportID]struct{})
	}
	if s.logicalSupportByFact == nil {
		s.logicalSupportByFact = make(map[FactID]map[SupportID]struct{})
	}
}

func (m logicalSupportMemory) addEdge(source logicalSupportSourceKey, edge LogicalSupportEdge) bool {
	s := m.session
	if s == nil || edge.SupportID.IsZero() || edge.FactID.IsZero() {
		return false
	}
	m.ensure()
	if _, exists := s.logicalSupportEdges[edge.SupportID]; exists {
		return false
	}
	s.logicalSupportEdges[edge.SupportID] = logicalSupportEdgeRecord{edge: edge, source: source}
	sourceEdges := s.logicalSupportBySource[source]
	if sourceEdges == nil {
		sourceEdges = make(map[SupportID]struct{})
		s.logicalSupportBySource[source] = sourceEdges
	}
	sourceEdges[edge.SupportID] = struct{}{}
	factEdges := s.logicalSupportByFact[edge.FactID]
	if factEdges == nil {
		factEdges = make(map[SupportID]struct{})
		s.logicalSupportByFact[edge.FactID] = factEdges
	}
	factEdges[edge.SupportID] = struct{}{}
	s.logicalSupportCounters.SupportEdgesAdded++
	return true
}

func (m logicalSupportMemory) countForFact(factID FactID) int {
	s := m.session
	if s == nil || s.logicalSupportByFact == nil {
		return 0
	}
	return len(s.logicalSupportByFact[factID])
}

func (m logicalSupportMemory) removeSource(ctx context.Context, source logicalSupportSourceKey) []FactID {
	s := m.session
	if s == nil || len(s.logicalSupportBySource) == 0 {
		return nil
	}
	ids := s.logicalSupportBySource[source]
	if len(ids) == 0 {
		return nil
	}
	supportIDs := make([]SupportID, 0, len(ids))
	for supportID := range ids {
		supportIDs = append(supportIDs, supportID)
	}
	slices.Sort(supportIDs)

	affected := make([]FactID, 0, len(supportIDs))
	for _, supportID := range supportIDs {
		record, ok := s.logicalSupportEdges[supportID]
		if !ok {
			continue
		}
		delete(s.logicalSupportEdges, supportID)
		if byFact := s.logicalSupportByFact[record.edge.FactID]; byFact != nil {
			delete(byFact, supportID)
			if len(byFact) == 0 {
				delete(s.logicalSupportByFact, record.edge.FactID)
			}
		}
		s.logicalSupportCounters.SupportEdgesRemoved++
		s.emitLogicalSupportEvent(ctx, EventLogicalSupportRemoved, record.edge)
		affected = append(affected, record.edge.FactID)
	}
	delete(s.logicalSupportBySource, source)
	return affected
}

func (s *Session) addLogicalSupportForPropagationEvent(ctx context.Context, fact *workingFact, event reteGraphPropagationEvent, supportingFacts []FactID) (bool, error) {
	if s == nil || fact == nil {
		return false, ErrFactNotFound
	}
	source, ok := logicalSupportSourceFromPropagationEvent(event)
	if !ok {
		return false, ErrLogicalSupportUnavailable
	}
	s.ensureLogicalSupportMaps()
	supportID := logicalSupportID(source, fact.id)
	if supportID.IsZero() {
		return false, ErrLogicalSupportUnavailable
	}

	edge := LogicalSupportEdge{
		SupportID:       supportID,
		FactID:          fact.id,
		RuleID:          event.origin.RuleID,
		RuleRevisionID:  event.origin.RuleRevisionID,
		ActivationID:    event.origin.activationID(),
		Generation:      event.sourceGeneration,
		SupportingFacts: cloneFactIDs(supportingFacts),
	}
	if !s.logicalSupportMemory().addEdge(source, edge) {
		return false, nil
	}
	s.updateFactSupportState(fact)
	s.emitLogicalSupportEvent(ctx, EventLogicalSupportAdded, edge)
	return true, nil
}

func (s *Session) logicalSupportCount(factID FactID) int {
	return s.logicalSupportMemory().countForFact(factID)
}

func (s *Session) updateFactSupportState(fact *workingFact) {
	if s == nil || fact == nil {
		return
	}
	before := fact.resolvedSupportState()
	logical := s.logicalSupportCount(fact.id) > 0
	switch {
	case before == FactSupportStated || before == FactSupportStatedAndLogical:
		if logical {
			fact.setSupportState(FactSupportStatedAndLogical)
		} else {
			fact.setSupportState(FactSupportStated)
		}
	default:
		if logical {
			fact.setSupportState(FactSupportLogical)
		} else {
			fact.setSupportState(FactSupportLogical)
		}
	}
	if before != fact.resolvedSupportState() {
		s.logicalSupportCounters.MetadataOnlyTransitions++
	}
}

func (s *Session) makeFactLogicalOnly(fact *workingFact) {
	if fact != nil {
		fact.setSupportState(FactSupportLogical)
	}
}

func (s *Session) addStatedSupportToFact(fact *workingFact) bool {
	if s == nil || fact == nil || fact.resolvedSupportState() != FactSupportLogical {
		return false
	}
	fact.setSupportState(FactSupportStatedAndLogical)
	s.logicalSupportCounters.MetadataOnlyTransitions++
	return true
}

func (s *Session) removeStatedSupportFromFact(fact *workingFact) bool {
	if s == nil || fact == nil || fact.resolvedSupportState() != FactSupportStatedAndLogical {
		return false
	}
	fact.setSupportState(FactSupportLogical)
	s.logicalSupportCounters.MetadataOnlyTransitions++
	return true
}

func (s *Session) factHasLogicalSupport(factID FactID) bool {
	return s.logicalSupportCount(factID) > 0
}

func (s *Session) removeLogicalSupportsForPropagationEventDelta(ctx context.Context, event reteGraphPropagationEvent, delta reteAgendaDelta) (reteAgendaDelta, error) {
	if s == nil || len(delta.removed) == 0 || len(s.logicalSupportBySource) == 0 {
		return reteAgendaDelta{supported: true}, nil
	}
	combined := reteAgendaDelta{supported: delta.supported}
	queue := make([]logicalSupportSourceKey, 0, len(delta.removed))
	for _, removed := range delta.removed {
		if removed.token.isZero() {
			continue
		}
		rule, ok := s.revision.rulesByRevisionID[removed.ruleRevisionID]
		if !ok {
			continue
		}
		activation, err := activationFromTerminalTokenWithIdentity(rule, removed.token, candidateIdentityForTerminalTokenDelta(s.revision, removed))
		if err != nil {
			return combined, err
		}
		queue = append(queue, logicalSupportSourceFromActivation(activation))
	}
	return s.removeLogicalSupportsForSources(ctx, queue, event.origin)
}

func (s *Session) removeLogicalSupportsForRuleRevisions(ctx context.Context, revisions map[RuleRevisionID]struct{}, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || len(revisions) == 0 || len(s.logicalSupportBySource) == 0 {
		return reteAgendaDelta{}, nil
	}
	sources := make([]logicalSupportSourceKey, 0)
	for source := range s.logicalSupportBySource {
		if _, ok := revisions[source.ruleRevisionID]; ok {
			sources = append(sources, source)
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].generation != sources[j].generation {
			return sources[i].generation < sources[j].generation
		}
		if sources[i].ruleRevisionID != sources[j].ruleRevisionID {
			return sources[i].ruleRevisionID < sources[j].ruleRevisionID
		}
		if sources[i].identityKey.scopeHash != sources[j].identityKey.scopeHash {
			return sources[i].identityKey.scopeHash < sources[j].identityKey.scopeHash
		}
		return sources[i].identityKey.hash < sources[j].identityKey.hash
	})
	return s.removeLogicalSupportsForSources(ctx, sources, origin)
}

func (s *Session) removeLogicalSupportsForSources(ctx context.Context, sources []logicalSupportSourceKey, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || len(sources) == 0 {
		return combined, nil
	}
	depth := 0
	for len(sources) > 0 {
		depth++
		if depth > s.logicalSupportCounters.CascadeDepthMax {
			s.logicalSupportCounters.CascadeDepthMax = depth
		}
		if len(sources) > s.logicalSupportCounters.CascadeBreadthMax {
			s.logicalSupportCounters.CascadeBreadthMax = len(sources)
		}
		nextSources := make([]logicalSupportSourceKey, 0)
		unsupportedFacts := make(map[FactID]struct{})
		for _, source := range sources {
			removedFacts := s.removeLogicalSupportSource(ctx, source)
			for _, factID := range removedFacts {
				unsupportedFacts[factID] = struct{}{}
			}
		}
		if len(unsupportedFacts) == 0 {
			break
		}
		factIDs := make([]FactID, 0, len(unsupportedFacts))
		for factID := range unsupportedFacts {
			factIDs = append(factIDs, factID)
		}
		sort.Slice(factIDs, func(i, j int) bool {
			if factIDs[i].Generation() != factIDs[j].Generation() {
				return factIDs[i].Generation() < factIDs[j].Generation()
			}
			return factIDs[i].Sequence() < factIDs[j].Sequence()
		})
		for _, factID := range factIDs {
			fact, ok := s.workingFactByID(factID)
			if !ok {
				continue
			}
			if fact.resolvedSupportState() == FactSupportLogical && !s.factHasLogicalSupport(factID) {
				_, delta, err := s.removeFactImmediate(ctx, factID, origin, true)
				if err != nil {
					return combined, err
				}
				combined = mergeReteAgendaDelta(combined, delta)
				for _, removed := range delta.removed {
					if removed.token.isZero() {
						continue
					}
					rule, ok := s.revision.rulesByRevisionID[removed.ruleRevisionID]
					if !ok {
						continue
					}
					activation, err := activationFromTerminalTokenWithIdentity(rule, removed.token, candidateIdentityForTerminalTokenDelta(s.revision, removed))
					if err != nil {
						return combined, err
					}
					nextSources = append(nextSources, logicalSupportSourceFromActivation(activation))
				}
			} else if fact.resolvedSupportState() == FactSupportStatedAndLogical && !s.factHasLogicalSupport(factID) {
				fact.setSupportState(FactSupportStated)
				state := s.activeFactWorkspace()
				state.replaceWorkingFact(fact)
				s.commitFactWorkspace(state)
				s.logicalSupportCounters.MetadataOnlyTransitions++
			}
		}
		sources = nextSources
	}
	return combined, nil
}

func (s *Session) removeLogicalSupportSource(ctx context.Context, source logicalSupportSourceKey) []FactID {
	return s.logicalSupportMemory().removeSource(ctx, source)
}

// purgeReceivedLogicalSupport removes every logical-support edge whose target is
// factID. It is used when a fact is fully removed outside the normal cascade
// (for example a unique-key replacement) so the fact leaves no dangling support
// edges behind. It is a no-op for stated facts, which receive no support.
func (s *Session) purgeReceivedLogicalSupport(ctx context.Context, factID FactID) {
	if s == nil || len(s.logicalSupportByFact) == 0 {
		return
	}
	edges := s.logicalSupportByFact[factID]
	if len(edges) == 0 {
		return
	}
	supportIDs := make([]SupportID, 0, len(edges))
	for supportID := range edges {
		supportIDs = append(supportIDs, supportID)
	}
	slices.Sort(supportIDs)
	for _, supportID := range supportIDs {
		record, ok := s.logicalSupportEdges[supportID]
		if !ok {
			continue
		}
		delete(s.logicalSupportEdges, supportID)
		if bySource := s.logicalSupportBySource[record.source]; bySource != nil {
			delete(bySource, supportID)
			if len(bySource) == 0 {
				delete(s.logicalSupportBySource, record.source)
			}
		}
		s.logicalSupportCounters.SupportEdgesRemoved++
		s.emitLogicalSupportEvent(ctx, EventLogicalSupportRemoved, record.edge)
	}
	delete(s.logicalSupportByFact, factID)
}

func (s *Session) clearLogicalSupports() {
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

func (s *Session) captureLogicalSupportState() logicalSupportState {
	if s == nil {
		return logicalSupportState{}
	}
	return logicalSupportState{
		edges:    cloneLogicalSupportEdges(s.logicalSupportEdges),
		bySource: cloneLogicalSupportSourceIndex(s.logicalSupportBySource),
		byFact:   cloneLogicalSupportFactIndex(s.logicalSupportByFact),
		counters: s.logicalSupportCounters,
	}
}

func (s *Session) restoreLogicalSupportState(state logicalSupportState) {
	if s == nil {
		return
	}
	s.logicalSupportEdges = state.edges
	s.logicalSupportBySource = state.bySource
	s.logicalSupportByFact = state.byFact
	s.logicalSupportCounters = state.counters
}

func cloneLogicalSupportEdges(in map[SupportID]logicalSupportEdgeRecord) map[SupportID]logicalSupportEdgeRecord {
	if len(in) == 0 {
		return nil
	}
	out := make(map[SupportID]logicalSupportEdgeRecord, len(in))
	for key, record := range in {
		record.edge = record.edge.clone()
		out[key] = record
	}
	return out
}

func cloneLogicalSupportSourceIndex(in map[logicalSupportSourceKey]map[SupportID]struct{}) map[logicalSupportSourceKey]map[SupportID]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[logicalSupportSourceKey]map[SupportID]struct{}, len(in))
	for key, ids := range in {
		out[key] = cloneSupportIDSet(ids)
	}
	return out
}

func cloneLogicalSupportFactIndex(in map[FactID]map[SupportID]struct{}) map[FactID]map[SupportID]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[FactID]map[SupportID]struct{}, len(in))
	for key, ids := range in {
		out[key] = cloneSupportIDSet(ids)
	}
	return out
}

func cloneSupportIDSet(in map[SupportID]struct{}) map[SupportID]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[SupportID]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func (s *Session) emitLogicalSupportEvent(ctx context.Context, eventType EventType, edge LogicalSupportEdge) {
	if s == nil {
		return
	}
	s.nextEventSequence++
	if !s.hasEventListenersFor(eventType) {
		return
	}
	rulesetID := RulesetID("")
	if s.revision != nil {
		rulesetID = s.revision.ID()
	}
	edgeClone := edge.clone()
	s.emitEvent(ctx, Event{
		SessionID:      s.id,
		RulesetID:      rulesetID,
		Sequence:       s.nextEventSequence,
		Timestamp:      s.eventClock(),
		Type:           eventType,
		Severity:       EventSeverityInfo,
		Generation:     edge.Generation,
		RuleID:         edge.RuleID,
		RuleRevisionID: edge.RuleRevisionID,
		ActivationID:   edge.ActivationID,
		FactIDs:        []FactID{edge.FactID},
		SupportEdge:    &edgeClone,
	})
}

func (s *Session) currentSupportGraph() SupportGraph {
	if s == nil {
		return SupportGraph{}
	}
	edges := make([]LogicalSupportEdge, 0, len(s.logicalSupportEdges))
	for _, record := range s.logicalSupportEdges {
		edges = append(edges, record.edge.clone())
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].SupportID < edges[j].SupportID })
	counters := s.logicalSupportCounters
	counters.CurrentSupportEdges = len(edges)
	for _, id := range s.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			continue
		}
		switch fact.resolvedSupportState() {
		case FactSupportLogical:
			counters.CurrentLogicalFacts++
		case FactSupportStatedAndLogical:
			counters.CurrentStatedAndLogicalFacts++
		}
	}
	return SupportGraph{
		Generation: s.generation,
		Edges:      edges,
		Counters:   counters,
	}
}

func mergeReteAgendaDelta(left, right reteAgendaDelta) reteAgendaDelta {
	if !left.supported || !right.supported {
		return reteAgendaDelta{}
	}
	if reteAgendaDeltaPayloadEmpty(left) {
		if right.owned {
			return right
		}
		return cloneRetainedReteAgendaDelta(right)
	}
	if reteAgendaDeltaPayloadEmpty(right) {
		if left.owned {
			return left
		}
		return cloneRetainedReteAgendaDelta(left)
	}
	if !left.owned {
		left = cloneRetainedReteAgendaDelta(left)
	}
	left.added = mergeReteAgendaDeltaSlice(left.added, right.added)
	left.removed = mergeReteAgendaDeltaSlice(left.removed, right.removed)
	left.updated = mergeReteAgendaDeltaSlice(left.updated, right.updated)
	left.demands = mergeReteAgendaDeltaSlice(left.demands, right.demands)
	left.resolvedDemands = mergeReteAgendaDeltaSlice(left.resolvedDemands, right.resolvedDemands)
	left.resolvedOwners = mergeReteAgendaDeltaSlice(left.resolvedOwners, right.resolvedOwners)
	return left
}

// mergeReteAgendaDeltaIfNeeded skips the ownership-forcing merge when the
// right delta carries nothing, keeping the left delta transient so callers
// that apply it immediately avoid a defensive clone.
func mergeReteAgendaDeltaIfNeeded(left, right reteAgendaDelta) reteAgendaDelta {
	if right.supported && reteAgendaDeltaPayloadEmpty(right) {
		return left
	}
	return mergeReteAgendaDelta(left, right)
}

func cloneRetainedReteAgendaDelta(delta reteAgendaDelta) reteAgendaDelta {
	delta.added = slices.Clone(delta.added)
	delta.removed = slices.Clone(delta.removed)
	delta.updated = slices.Clone(delta.updated)
	delta.demands = slices.Clone(delta.demands)
	delta.resolvedDemands = slices.Clone(delta.resolvedDemands)
	delta.resolvedOwners = slices.Clone(delta.resolvedOwners)
	delta.owned = true
	return delta
}

func coalesceReteAgendaDelta(revision *Ruleset, delta reteAgendaDelta) reteAgendaDelta {
	if len(delta.added) != 0 && len(delta.removed) != 0 {
		var updates []reteTerminalTokenUpdate
		delta.added, delta.removed, updates = coalesceTerminalTokenDeltas(revision, delta.added, delta.removed)
		if len(updates) != 0 {
			delta.updated = append(delta.updated, updates...)
		}
	}
	return delta
}

func mergeReteAgendaDeltaSlice[T any](left, right []T) []T {
	if len(right) == 0 {
		return left
	}
	if len(left) == 0 {
		return right
	}
	return append(left, right...)
}

func reteAgendaDeltaPayloadEmpty(delta reteAgendaDelta) bool {
	return len(delta.added) == 0 &&
		len(delta.removed) == 0 &&
		len(delta.updated) == 0 &&
		len(delta.demands) == 0 &&
		len(delta.resolvedDemands) == 0 &&
		len(delta.resolvedOwners) == 0
}
