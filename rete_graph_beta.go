package gess

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

type reteGraphBetaMemory struct {
	revision            *Ruleset
	graph               *reteGraph
	nodes               []*reteGraphBetaNodeMemory
	aggregates          []*reteGraphAggregateNodeMemory
	terminals           []*reteGraphTerminalMemory
	alphaFacts          []reteGraphAlphaFactSet
	alphaConditions     [][]ConditionID
	alphaFactCounts     map[ConditionID]int
	facts               []FactSnapshot
	factIndexes         map[FactID]int
	factsByName         map[string][]FactSnapshot
	factsByTemplate     map[TemplateKey][]FactSnapshot
	factNameIndexes     map[FactID]int
	factTemplateIndexes map[FactID]int
	arena               *tokenArena
	queryArena          *tokenArena
	terminalTokenDeltas []reteTerminalTokenDelta
	alphaRouteScratch   []reteGraphAlphaNodeID
	alphaRouteSeen      map[reteGraphAlphaNodeID]uint64
	alphaRouteEpoch     uint64
}

type reteGraphBetaNodeMemory struct {
	left  tokenHashMemory
	right tokenHashMemory
}

type reteGraphAggregateNodeMemory struct {
	buckets map[graphTokenIdentityKey]*reteGraphAggregateBucket
}

type reteGraphAggregateBucket struct {
	parent    tokenRef
	members   map[graphTokenIdentityKey]reteGraphAggregateMember
	count     int64
	intSums   []int64
	floatSums []float64
	floaty    []bool
	extrema   []reteGraphAggregateExtremum
	collects  [][]reteGraphAggregateCollectEntry
	token     tokenRef
	values    []Value
	hasValue  bool
}

type reteGraphAggregateExtremum struct {
	values  map[string]reteGraphAggregateExtremumValue
	current Value
	have    bool
}

type reteGraphAggregateExtremumValue struct {
	value Value
	count int64
}

type reteGraphAggregateCollectEntry struct {
	key    graphTokenIdentityKey
	factID FactID
	value  Value
}

type reteGraphAggregateMember struct {
	match  conditionMatch
	token  tokenRef
	values []Value
}

type reteGraphTerminalMemory struct {
	rows tokenHashMemory
}

type reteGraphAlphaFactSet struct {
	facts map[FactID]struct{}
}

type reteGraphBetaMemoryStats struct {
	TokenMemories           int
	BetaTokenMemories       int
	TerminalTokenMemories   int
	TokenRows               int
	TokenRowCapacity        int
	TokenRowReserve         int
	TokenRowCapacityMax     int
	TokenRowReserveMax      int
	JoinIndexKeys           int
	JoinIndexReserve        int
	JoinIndexKeysMax        int
	JoinIndexReserveMax     int
	IdentityIndexKeys       int
	IdentityIndexReserve    int
	IdentityIndexKeysMax    int
	IdentityIndexReserveMax int
	FactIndexKeys           int
	FactIndexReserve        int
	FactIndexKeysMax        int
	FactIndexReserveMax     int
}

type tokenHashMemory struct {
	rows                 []graphTokenRow
	indexes              map[betaJoinKey]graphTokenRowIDBucket
	identityRows         map[graphTokenIdentityKey]graphTokenRowIDBucket
	factRows             map[FactID]graphTokenRowIDBucket
	rowReserve           int
	joinIndexReserve     int
	identityIndexReserve int
	factIndexReserve     int
}

type graphTokenRowID int

type graphTokenRow struct {
	id               graphTokenRowID
	token            tokenRef
	joinKey          betaJoinKey
	identity         graphTokenIdentityKey
	terminalIdentity candidateIdentity
	supportCount     int
	branchSupports   []terminalBranchSupport
}

type terminalBranchSupport struct {
	branchID int
	count    int
}

func (r *graphTokenRow) addTerminalBranchSupport(branchID int) {
	if r == nil || branchID < 0 {
		return
	}
	for i := range r.branchSupports {
		if r.branchSupports[i].branchID == branchID {
			r.branchSupports[i].count++
			return
		}
	}
	r.branchSupports = append(r.branchSupports, terminalBranchSupport{branchID: branchID, count: 1})
}

func (r graphTokenRow) hasTerminalBranchSupport(branchID int) bool {
	if branchID < 0 {
		return false
	}
	for _, support := range r.branchSupports {
		if support.branchID == branchID && support.count > 0 {
			return true
		}
	}
	return false
}

func (r *graphTokenRow) removeTerminalBranchSupport(branchID int) {
	if r == nil || branchID < 0 {
		return
	}
	for i := range r.branchSupports {
		if r.branchSupports[i].branchID != branchID {
			continue
		}
		r.branchSupports[i].count--
		if r.branchSupports[i].count > 0 {
			return
		}
		copy(r.branchSupports[i:], r.branchSupports[i+1:])
		r.branchSupports[len(r.branchSupports)-1] = terminalBranchSupport{}
		r.branchSupports = r.branchSupports[:len(r.branchSupports)-1]
		return
	}
}

func (r graphTokenRow) terminalBranchIDs() []int {
	if len(r.branchSupports) == 0 {
		return nil
	}
	out := make([]int, 0, len(r.branchSupports))
	for _, support := range r.branchSupports {
		if support.count <= 0 {
			continue
		}
		out = append(out, support.branchID)
	}
	return out
}

type graphTokenIdentityKey struct {
	size          int
	generation    Generation
	identityState uint64
}

type graphTokenRowIDBucket struct {
	first graphTokenRowID
	rest  []graphTokenRowID
	count int
}

func (b graphTokenRowIDBucket) len() int {
	return b.count
}

func (b graphTokenRowIDBucket) at(index int) (graphTokenRowID, bool) {
	if index < 0 || index >= b.count {
		return 0, false
	}
	if index == 0 {
		return b.first, true
	}
	index--
	if index >= len(b.rest) {
		return 0, false
	}
	return b.rest[index], true
}

func (b *graphTokenRowIDBucket) append(id graphTokenRowID) {
	if b.count == 0 {
		b.first = id
		b.count = 1
		return
	}
	b.rest = append(b.rest, id)
	b.count++
}

func (b *graphTokenRowIDBucket) remove(id graphTokenRowID) bool {
	if b.count == 0 {
		return false
	}
	if b.first == id {
		last := b.count - 1
		if last == 0 {
			b.first = 0
			b.count = 0
			return true
		}
		b.first = b.rest[last-1]
		b.rest[last-1] = 0
		b.rest = b.rest[:last-1]
		b.count--
		return true
	}
	for i, current := range b.rest {
		if current != id {
			continue
		}
		last := len(b.rest) - 1
		b.rest[i] = b.rest[last]
		b.rest[last] = 0
		b.rest = b.rest[:last]
		b.count--
		return true
	}
	return false
}

func (b *graphTokenRowIDBucket) replace(oldID, newID graphTokenRowID) bool {
	if b.count == 0 {
		return false
	}
	if b.first == oldID {
		b.first = newID
		return true
	}
	for i, current := range b.rest {
		if current == oldID {
			b.rest[i] = newID
			return true
		}
	}
	return false
}

func (b graphTokenRowIDBucket) forEach(fn func(graphTokenRowID) bool) {
	if b.count == 0 {
		return
	}
	if !fn(b.first) {
		return
	}
	for i := 0; i < b.count-1 && i < len(b.rest); i++ {
		if !fn(b.rest[i]) {
			return
		}
	}
}

func (b graphTokenRowIDBucket) reset() graphTokenRowIDBucket {
	for i := range b.rest {
		b.rest[i] = 0
	}
	b.first = 0
	b.rest = b.rest[:0]
	b.count = 0
	return b
}

func newReteGraphBetaMemory(revision *Ruleset, graph *reteGraph, facts []FactSnapshot) *reteGraphBetaMemory {
	if revision == nil || graph == nil {
		return nil
	}
	rowCapacity := graphBetaTokenMemoryCapacity(revision, len(facts))
	arenaCapacity := graphBetaTokenArenaCapacity(revision, len(facts))
	memory := &reteGraphBetaMemory{
		revision:            revision,
		graph:               graph,
		nodes:               make([]*reteGraphBetaNodeMemory, len(graph.betaNodes)+1),
		aggregates:          make([]*reteGraphAggregateNodeMemory, len(graph.aggregateNodes)+1),
		terminals:           make([]*reteGraphTerminalMemory, len(graph.terminalNodes)+1),
		arena:               newTokenArena(),
		terminalTokenDeltas: make([]reteTerminalTokenDelta, 0, revision.estimatedRunFactCapacity(len(facts))),
	}
	memory.arena.reserve(arenaCapacity)
	memory.reserveMemories(rowCapacity)
	memory.reserveAlphaFacts(graphBetaAlphaFactCapacity(revision, graph, len(facts)))
	memory.resetFacts(facts)
	return memory
}

func graphBetaTokenMemoryCapacity(revision *Ruleset, initialFacts int) int {
	capacity := max(8, initialFacts)
	if revision != nil {
		capacity = max(capacity, len(revision.ruleOrder)*2)
	}
	return capacity
}

func graphBetaTokenArenaCapacity(revision *Ruleset, initialFacts int) int {
	if revision == nil {
		return max(0, initialFacts)
	}
	return revision.estimatedRunFactCapacity(initialFacts) * 2
}

func graphBetaAlphaFactCapacity(revision *Ruleset, graph *reteGraph, initialFacts int) int {
	if graph == nil || len(graph.alphaNodes) == 0 {
		return 0
	}
	capacity := max(1, initialFacts)
	if revision != nil {
		capacity = max(capacity, revision.estimatedRunFactCapacity(initialFacts))
	}
	return max(1, (capacity+len(graph.alphaNodes)-1)/len(graph.alphaNodes))
}

func (m *reteGraphBetaMemory) reserveMemories(rowCapacity int) {
	if m == nil || m.graph == nil || rowCapacity <= 0 {
		return
	}
	for _, graphNode := range m.graph.betaNodes {
		node := m.nodeMemory(graphNode.id)
		node.left.reserveBeta(rowCapacity, graphBetaFactIndexReserve(rowCapacity, m.graph.stageTokenWidth(graphNode.left)))
		node.right.reserveBeta(rowCapacity, graphBetaFactIndexReserve(rowCapacity, m.graph.stageTokenWidth(graphNode.right)))
	}
	for _, terminalNode := range m.graph.terminalNodes {
		terminal := m.terminal(terminalNode.id)
		terminal.rows.reserveTerminal(rowCapacity, graphBetaFactIndexReserve(rowCapacity, m.graph.stageTokenWidth(terminalNode.input)))
	}
}

func graphBetaFactIndexReserve(rowCapacity, tokenWidth int) int {
	if rowCapacity <= 0 {
		return 0
	}
	if tokenWidth == 1 {
		return rowCapacity
	}
	return rowCapacity * 2
}

func (m *reteGraphBetaMemory) reserveAlphaFacts(factCapacity int) {
	if m == nil || m.graph == nil {
		return
	}
	size := len(m.graph.alphaNodes) + 1
	if cap(m.alphaFacts) < size {
		m.alphaFacts = make([]reteGraphAlphaFactSet, size)
	} else {
		m.alphaFacts = m.alphaFacts[:size]
	}
	if factCapacity > 0 {
		for i := 1; i < size; i++ {
			m.alphaFacts[i].reserve(factCapacity)
		}
	}
	m.alphaConditions = make([][]ConditionID, size)
	for _, node := range m.graph.alphaNodes {
		index := int(node.id)
		if index <= 0 || index >= size {
			continue
		}
		for _, consumer := range node.consumers {
			m.appendAlphaCondition(index, consumer.conditionID)
		}
		if len(m.alphaConditions[index]) == 0 && node.entry.conditionID != "" {
			m.appendAlphaCondition(index, node.entry.conditionID)
		}
	}
	conditionCount := 0
	for _, conditions := range m.alphaConditions {
		conditionCount += len(conditions)
	}
	if m.alphaFactCounts == nil {
		m.alphaFactCounts = make(map[ConditionID]int, conditionCount)
	} else {
		clear(m.alphaFactCounts)
	}
}

func (m *reteGraphBetaMemory) appendAlphaCondition(index int, conditionID ConditionID) {
	if m == nil || conditionID == "" || index <= 0 || index >= len(m.alphaConditions) {
		return
	}
	if slices.Contains(m.alphaConditions[index], conditionID) {
		return
	}
	m.alphaConditions[index] = append(m.alphaConditions[index], conditionID)
}

func (m *tokenHashMemory) reserveBeta(rowCapacity, factCapacity int) {
	if m == nil || rowCapacity <= 0 {
		return
	}
	m.reserveRows(rowCapacity)
	m.reserveIndexes(rowCapacity, rowCapacity, factCapacity)
}

func (m *tokenHashMemory) reserveTerminal(rowCapacity, factCapacity int) {
	if m == nil || rowCapacity <= 0 {
		return
	}
	m.reserveRows(rowCapacity)
	m.reserveIndexes(0, rowCapacity, factCapacity)
}

func (m *tokenHashMemory) reserveRows(rowCapacity int) {
	if m == nil || rowCapacity <= cap(m.rows) {
		return
	}
	rows := make([]graphTokenRow, len(m.rows), rowCapacity)
	copy(rows, m.rows)
	m.rows = rows
	m.rowReserve = max(m.rowReserve, rowCapacity)
}

func (m *tokenHashMemory) reserveIndexes(joinCapacity, identityCapacity, factCapacity int) {
	if m == nil {
		return
	}
	if joinCapacity > 0 && m.indexes == nil {
		m.indexes = make(map[betaJoinKey]graphTokenRowIDBucket, joinCapacity)
		m.joinIndexReserve = max(m.joinIndexReserve, joinCapacity)
	}
	if identityCapacity > 0 && m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket, identityCapacity)
		m.identityIndexReserve = max(m.identityIndexReserve, identityCapacity)
	}
	if factCapacity > 0 && m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket, factCapacity)
		m.factIndexReserve = max(m.factIndexReserve, factCapacity)
	}
}

func (m *tokenHashMemory) clear() {
	if m == nil {
		return
	}
	for i := range m.rows {
		m.rows[i] = graphTokenRow{}
	}
	m.rows = m.rows[:0]
	clear(m.indexes)
	clear(m.identityRows)
	clear(m.factRows)
}

func (m *tokenHashMemory) len() int {
	if m == nil {
		return 0
	}
	return len(m.rows)
}

func (s *reteGraphAlphaFactSet) reserve(capacity int) {
	if s == nil || capacity <= 0 || s.facts != nil {
		return
	}
	s.facts = make(map[FactID]struct{}, capacity)
}

func (s *reteGraphAlphaFactSet) insert(id FactID) bool {
	if s == nil || id.IsZero() {
		return false
	}
	if s.facts == nil {
		s.facts = make(map[FactID]struct{}, 1)
	}
	if _, ok := s.facts[id]; ok {
		return false
	}
	s.facts[id] = struct{}{}
	return true
}

func (s *reteGraphAlphaFactSet) remove(id FactID) bool {
	if s == nil || id.IsZero() || s.facts == nil {
		return false
	}
	if _, ok := s.facts[id]; !ok {
		return false
	}
	delete(s.facts, id)
	return true
}

func (s *reteGraphAlphaFactSet) contains(id FactID) bool {
	if s == nil || id.IsZero() || s.facts == nil {
		return false
	}
	_, ok := s.facts[id]
	return ok
}

func (s *reteGraphAlphaFactSet) clear() {
	if s == nil || s.facts == nil {
		return
	}
	clear(s.facts)
}

func (m *tokenHashMemory) bucketForKey(key betaJoinKey) graphTokenRowIDBucket {
	if m == nil || len(m.indexes) == 0 {
		return graphTokenRowIDBucket{}
	}
	return m.indexes[key]
}

func (m *tokenHashMemory) row(rowID graphTokenRowID) *graphTokenRow {
	if m == nil || rowID < 0 {
		return nil
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return nil
	}
	return &m.rows[index]
}

func (m *tokenHashMemory) insert(token tokenRef, joinKey betaJoinKey) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
	}
	if m.indexes == nil {
		m.indexes = make(map[betaJoinKey]graphTokenRowIDBucket)
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket)
	}

	identity := tokenRefKey(token)
	bucket := m.identityRows[identity]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		row := m.row(rowID)
		if row != nil && tokenRefEqual(row.token, token) {
			return false
		}
	}

	rowID := graphTokenRowID(len(m.rows))
	m.rows = append(m.rows, graphTokenRow{
		id:       rowID,
		token:    token,
		joinKey:  joinKey,
		identity: identity,
	})
	joinBucket := m.indexes[joinKey]
	joinBucket.append(rowID)
	m.indexes[joinKey] = joinBucket
	identityBucket := m.identityRows[identity]
	identityBucket.append(rowID)
	m.identityRows[identity] = identityBucket
	m.indexTokenFacts(token, rowID)
	return true
}

func (m *tokenHashMemory) insertTerminal(token tokenRef, terminalIdentity candidateIdentity, branchID int) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket)
	}

	identity := tokenRefKey(token)
	bucket := m.identityRows[identity]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		row := m.row(rowID)
		if row != nil && tokenRefEqual(row.token, token) {
			if row.token.handle == token.handle {
				if !row.hasTerminalBranchSupport(branchID) {
					row.addTerminalBranchSupport(branchID)
				}
				return false
			}
			row.supportCount++
			row.addTerminalBranchSupport(branchID)
			return false
		}
	}

	branchSupports := []terminalBranchSupport(nil)
	if branchID >= 0 {
		branchSupports = []terminalBranchSupport{{branchID: branchID, count: 1}}
	}
	rowID := graphTokenRowID(len(m.rows))
	m.rows = append(m.rows, graphTokenRow{
		id:               rowID,
		token:            token,
		identity:         identity,
		terminalIdentity: terminalIdentity,
		supportCount:     1,
		branchSupports:   branchSupports,
	})
	identityBucket := m.identityRows[identity]
	identityBucket.append(rowID)
	m.identityRows[identity] = identityBucket
	m.indexTokenFacts(token, rowID)
	return true
}

func (m *tokenHashMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows[id]
		if !ok || bucket.len() == 0 {
			return removed
		}
		rowID, ok := bucket.at(bucket.len() - 1)
		if !ok {
			return removed
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		m.removeRow(rowID, counters)
		removed++
	}
}

func (m *tokenHashMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(graphTokenRow)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows[id]
		if !ok || bucket.len() == 0 {
			return removed
		}
		rowID, ok := bucket.at(bucket.len() - 1)
		if !ok {
			return removed
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		row := m.row(rowID)
		if row != nil && !row.token.isZero() && row.token.containsFact(id) {
			fn(*row)
		}
		m.removeRow(rowID, counters)
		removed++
	}
}

func (m *tokenHashMemory) removeToken(token tokenRef, counters *propagationCounterLedger, branchIDs ...int) (graphTokenRow, bool) {
	if m == nil || token.isZero() {
		return graphTokenRow{}, false
	}
	branchID := -1
	if len(branchIDs) > 0 {
		branchID = branchIDs[0]
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	identity := tokenRefKey(token)
	bucket, ok := m.identityRows[identity]
	if !ok || bucket.len() == 0 {
		return graphTokenRow{}, false
	}
	for i := 0; i < bucket.len(); i++ {
		rowID, ok := bucket.at(i)
		if !ok {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		row := m.row(rowID)
		if row == nil || !tokenRefEqual(row.token, token) {
			continue
		}
		if row.supportCount > 1 {
			row.supportCount--
			row.removeTerminalBranchSupport(branchID)
			return graphTokenRow{}, false
		}
		removed := *row
		m.removeRow(rowID, counters)
		return removed, true
	}
	return graphTokenRow{}, false
}

func (m *tokenHashMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(graphTokenRow)) {
	if m == nil || id.IsZero() {
		return
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket, ok := m.factRows[id]
	if !ok || bucket.len() == 0 {
		return
	}
	var previous graphTokenRowID
	havePrevious := false
	for i := 0; i < bucket.len(); i++ {
		rowID, ok := bucket.at(i)
		if !ok || (havePrevious && rowID == previous) {
			continue
		}
		havePrevious = true
		previous = rowID
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		row := m.row(rowID)
		if row == nil || row.token.isZero() || !row.token.containsFact(id) {
			continue
		}
		fn(*row)
	}
}

func (m *tokenHashMemory) removeRow(rowID graphTokenRowID, counters *propagationCounterLedger) {
	if m == nil || rowID < 0 {
		return
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return
	}
	removed := m.rows[index]
	if bucket, ok := m.indexes[removed.joinKey]; ok {
		if bucket.remove(rowID) {
			if bucket.len() == 0 {
				delete(m.indexes, removed.joinKey)
			} else {
				m.indexes[removed.joinKey] = bucket
			}
		}
	}
	if bucket, ok := m.identityRows[removed.identity]; ok {
		if bucket.remove(rowID) {
			if bucket.len() == 0 {
				delete(m.identityRows, removed.identity)
			} else {
				m.identityRows[removed.identity] = bucket
			}
		}
	}
	m.removeTokenFacts(removed.token, rowID)
	last := len(m.rows) - 1
	if index != last {
		moved := m.rows[last]
		moved.id = rowID
		m.rows[index] = moved
		if bucket, ok := m.indexes[moved.joinKey]; ok {
			if bucket.replace(graphTokenRowID(last), rowID) {
				m.indexes[moved.joinKey] = bucket
			}
		}
		if bucket, ok := m.identityRows[moved.identity]; ok {
			if bucket.replace(graphTokenRowID(last), rowID) {
				m.identityRows[moved.identity] = bucket
			}
		}
		m.replaceTokenFactRows(moved.token, graphTokenRowID(last), rowID)
	}
	m.rows[last] = graphTokenRow{}
	m.rows = m.rows[:last]
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m *tokenHashMemory) indexTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || token.isZero() {
		return
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket)
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket := m.factRows[id]
			bucket.append(rowID)
			m.factRows[id] = bucket
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		id := row.match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket := m.factRows[id]
		bucket.append(rowID)
		m.factRows[id] = bucket
	}
}

func (m *tokenHashMemory) removeTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || len(m.factRows) == 0 || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket, ok := m.factRows[id]
			if !ok || !bucket.remove(rowID) {
				continue
			}
			if bucket.len() == 0 {
				delete(m.factRows, id)
			} else {
				m.factRows[id] = bucket
			}
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		id := row.match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket, ok := m.factRows[id]
		if !ok || !bucket.remove(rowID) {
			continue
		}
		if bucket.len() == 0 {
			delete(m.factRows, id)
		} else {
			m.factRows[id] = bucket
		}
	}
}

func (m *tokenHashMemory) replaceTokenFactRows(token tokenRef, oldID, newID graphTokenRowID) {
	if m == nil || len(m.factRows) == 0 || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket, ok := m.factRows[id]
			if ok && bucket.replace(oldID, newID) {
				m.factRows[id] = bucket
			}
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		id := row.match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket, ok := m.factRows[id]
		if ok && bucket.replace(oldID, newID) {
			m.factRows[id] = bucket
		}
	}
}

func (m *reteGraphBetaMemory) resetFacts(facts []FactSnapshot) {
	if m == nil || m.graph == nil {
		return
	}
	if len(m.alphaFacts) != len(m.graph.alphaNodes)+1 || len(m.alphaConditions) != len(m.graph.alphaNodes)+1 {
		m.reserveAlphaFacts(graphBetaAlphaFactCapacity(m.revision, m.graph, len(facts)))
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	} else {
		m.arena.reset()
	}
	m.clearMemories()
	m.setFacts(facts)
	m.initializeAggregateOutputs()
	for _, fact := range facts {
		m.insertFact(fact, nil)
	}
}

func (m *reteGraphBetaMemory) setFacts(facts []FactSnapshot) {
	if m == nil {
		return
	}
	m.facts = append(m.facts[:0], facts...)
	if m.factIndexes == nil {
		m.factIndexes = make(map[FactID]int, len(facts))
	} else {
		clear(m.factIndexes)
	}
	for i, fact := range m.facts {
		m.factIndexes[fact.ID()] = i
	}
	m.rebuildFactTargetIndexes()
}

func (m *reteGraphBetaMemory) rebuildFactTargetIndexes() {
	if m == nil {
		return
	}
	if m.factsByName == nil {
		m.factsByName = make(map[string][]FactSnapshot)
	} else {
		clear(m.factsByName)
	}
	if m.factsByTemplate == nil {
		m.factsByTemplate = make(map[TemplateKey][]FactSnapshot)
	} else {
		clear(m.factsByTemplate)
	}
	if m.factNameIndexes == nil {
		m.factNameIndexes = make(map[FactID]int, len(m.facts))
	} else {
		clear(m.factNameIndexes)
	}
	if m.factTemplateIndexes == nil {
		m.factTemplateIndexes = make(map[FactID]int, len(m.facts))
	} else {
		clear(m.factTemplateIndexes)
	}
	for _, fact := range m.facts {
		m.addFactTargetIndexes(fact)
	}
}

func (m *reteGraphBetaMemory) clearMemories() {
	if m == nil {
		return
	}
	for _, node := range m.nodes {
		if node != nil {
			node.left.clear()
			node.right.clear()
		}
	}
	for _, terminal := range m.terminals {
		if terminal != nil {
			terminal.rows.clear()
		}
	}
	for _, aggregate := range m.aggregates {
		if aggregate != nil {
			aggregate.clear()
		}
	}
	for i := range m.alphaFacts {
		m.alphaFacts[i].clear()
	}
	if m.alphaFactCounts != nil {
		clear(m.alphaFactCounts)
	}
	clear(m.terminalTokenDeltas)
	m.terminalTokenDeltas = m.terminalTokenDeltas[:0]
}

func (m *reteGraphBetaMemory) initializeAggregateOutputs() {
	if m == nil || m.graph == nil {
		return
	}
	delta := reteAgendaDelta{supported: true}
	for _, node := range m.graph.aggregateNodes {
		if node.input.kind != reteGraphStageAlpha {
			continue
		}
		memory := m.aggregateMemory(node.id)
		if memory == nil {
			delta.supported = false
			continue
		}
		bucket := memory.bucketForParent(tokenRef{})
		m.refreshAggregateOutputInternal(node.id, bucket, nil, nil, &delta)
	}
}

func (m *reteGraphAggregateNodeMemory) clear() {
	if m == nil {
		return
	}
	for _, bucket := range m.buckets {
		if bucket != nil {
			bucket.clear()
		}
	}
	if m.buckets != nil {
		clear(m.buckets)
	}
}

func (m *reteGraphAggregateBucket) clear() {
	if m == nil {
		return
	}
	if m.members != nil {
		clear(m.members)
	}
	m.parent = tokenRef{}
	m.count = 0
	clear(m.intSums)
	m.intSums = m.intSums[:0]
	clear(m.floatSums)
	m.floatSums = m.floatSums[:0]
	clear(m.floaty)
	m.floaty = m.floaty[:0]
	for i := range m.extrema {
		m.extrema[i].clear()
	}
	clear(m.extrema)
	m.extrema = m.extrema[:0]
	for i := range m.collects {
		clear(m.collects[i])
		m.collects[i] = m.collects[i][:0]
	}
	clear(m.collects)
	m.collects = m.collects[:0]
	m.token = tokenRef{}
	clear(m.values)
	m.values = m.values[:0]
	m.hasValue = false
}

func (m *reteGraphBetaMemory) insertFact(fact FactSnapshot, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	m.upsertFactSource(fact)
	routeIDs := m.snapshotAlphaRouteIDsForFact(fact)
	if len(routeIDs) == 0 {
		return reteAgendaDelta{}
	}

	delta := m.beginTerminalTokenDelta()
	for _, nodeID := range routeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if span != nil {
			span.recordConditionsTested()
		}
		if !node.matchesSnapshotWithCounters(fact, span) {
			continue
		}
		if span != nil {
			span.recordAlphaMatchAdded()
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromSnapshot(fact),
		}
		if !m.insertAlphaMatch(nodeID, match, span, &delta) {
			delta.supported = false
		}
	}
	return m.finishTerminalTokenDelta(delta)
}

func (m *reteGraphBetaMemory) upsertFactSource(fact FactSnapshot) {
	if m == nil || fact.ID().IsZero() {
		return
	}
	if m.factIndexes == nil {
		m.factIndexes = make(map[FactID]int)
	}
	if index, ok := m.factIndexes[fact.ID()]; ok && index >= 0 && index < len(m.facts) {
		m.removeFactTargetIndexes(m.facts[index])
		m.facts[index] = fact
		m.addFactTargetIndexes(fact)
		return
	}
	m.factIndexes[fact.ID()] = len(m.facts)
	m.facts = append(m.facts, fact)
	m.addFactTargetIndexes(fact)
}

func (m *reteGraphBetaMemory) removeFactSource(id FactID) {
	if m == nil || id.IsZero() || m.factIndexes == nil {
		return
	}
	index, ok := m.factIndexes[id]
	if !ok || index < 0 || index >= len(m.facts) {
		return
	}
	m.removeFactTargetIndexes(m.facts[index])
	last := len(m.facts) - 1
	if index != last {
		m.facts[index] = m.facts[last]
		m.factIndexes[m.facts[index].ID()] = index
	}
	m.facts[last] = FactSnapshot{}
	m.facts = m.facts[:last]
	delete(m.factIndexes, id)
}

func (m *reteGraphBetaMemory) addFactTargetIndexes(fact FactSnapshot) {
	if m == nil || fact.ID().IsZero() {
		return
	}
	if fact.Name() != "" {
		if m.factsByName == nil {
			m.factsByName = make(map[string][]FactSnapshot)
		}
		if m.factNameIndexes == nil {
			m.factNameIndexes = make(map[FactID]int)
		}
		m.factNameIndexes[fact.ID()] = len(m.factsByName[fact.Name()])
		m.factsByName[fact.Name()] = append(m.factsByName[fact.Name()], fact)
	}
	if fact.TemplateKey() != "" {
		if m.factsByTemplate == nil {
			m.factsByTemplate = make(map[TemplateKey][]FactSnapshot)
		}
		if m.factTemplateIndexes == nil {
			m.factTemplateIndexes = make(map[FactID]int)
		}
		m.factTemplateIndexes[fact.ID()] = len(m.factsByTemplate[fact.TemplateKey()])
		m.factsByTemplate[fact.TemplateKey()] = append(m.factsByTemplate[fact.TemplateKey()], fact)
	}
}

func (m *reteGraphBetaMemory) removeFactTargetIndexes(fact FactSnapshot) {
	if m == nil || fact.ID().IsZero() {
		return
	}
	if fact.Name() != "" && m.factsByName != nil && m.factNameIndexes != nil {
		index, ok := m.factNameIndexes[fact.ID()]
		facts := m.factsByName[fact.Name()]
		if ok && index >= 0 && index < len(facts) {
			last := len(facts) - 1
			if index != last {
				facts[index] = facts[last]
				m.factNameIndexes[facts[index].ID()] = index
			}
			facts[last] = FactSnapshot{}
			facts = facts[:last]
			if len(facts) == 0 {
				delete(m.factsByName, fact.Name())
			} else {
				m.factsByName[fact.Name()] = facts
			}
		}
		delete(m.factNameIndexes, fact.ID())
	}
	if fact.TemplateKey() != "" && m.factsByTemplate != nil && m.factTemplateIndexes != nil {
		index, ok := m.factTemplateIndexes[fact.ID()]
		facts := m.factsByTemplate[fact.TemplateKey()]
		if ok && index >= 0 && index < len(facts) {
			last := len(facts) - 1
			if index != last {
				facts[index] = facts[last]
				m.factTemplateIndexes[facts[index].ID()] = index
			}
			facts[last] = FactSnapshot{}
			facts = facts[:last]
			if len(facts) == 0 {
				delete(m.factsByTemplate, fact.TemplateKey())
			} else {
				m.factsByTemplate[fact.TemplateKey()] = facts
			}
		}
		delete(m.factTemplateIndexes, fact.ID())
	}
}

func (m *reteGraphBetaMemory) insertFactGenerated(fact *workingFact, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}
	}
	routeIDs := m.workingAlphaRouteIDsForFact(fact)
	if len(routeIDs) == 0 {
		return reteAgendaDelta{}
	}

	delta := m.beginTerminalTokenDelta()
	for _, nodeID := range routeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if span != nil {
			span.recordConditionsTested()
		}
		if !node.matchesWorkingWithCounters(fact, span) {
			continue
		}
		if span != nil {
			span.recordAlphaMatchAdded()
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromWorkingFact(fact),
		}
		if !m.insertAlphaMatchGenerated(nodeID, match, span, &delta) {
			delta.supported = false
		}
	}
	return m.finishTerminalTokenDelta(delta)
}

func (m *reteGraphBetaMemory) snapshotAlphaRouteIDsForFact(fact FactSnapshot) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil {
		return nil
	}
	templateKey := fact.TemplateKey()
	templateIDs := m.graph.routesByTemplateKey[templateKey]
	if len(templateIDs) > 3 {
		templateIDs = m.snapshotAlphaRouteIDs(templateKey, templateIDs, fact)
	}
	nameIDs := m.graph.routesByName[fact.Name()]
	if len(templateIDs) == 0 {
		return nameIDs
	}
	if len(nameIDs) == 0 {
		return templateIDs
	}
	m.resetAlphaRouteScratch()
	m.appendAlphaRouteBucket(templateIDs)
	m.appendAlphaRouteBucket(nameIDs)
	m.sortAlphaRouteScratch()
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) workingAlphaRouteIDsForFact(fact *workingFact) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil || fact == nil {
		return nil
	}
	templateIDs := m.graph.routesByTemplateKey[fact.templateKey]
	if len(templateIDs) > 3 {
		templateIDs = m.workingAlphaRouteIDs(fact.templateKey, templateIDs, fact)
	}
	nameIDs := m.graph.routesByName[fact.name]
	if len(templateIDs) == 0 {
		return nameIDs
	}
	if len(nameIDs) == 0 {
		return templateIDs
	}
	m.resetAlphaRouteScratch()
	m.appendAlphaRouteBucket(templateIDs)
	m.appendAlphaRouteBucket(nameIDs)
	m.sortAlphaRouteScratch()
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) snapshotAlphaRouteIDs(templateKey TemplateKey, nodeIDs []reteGraphAlphaNodeID, fact FactSnapshot) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil {
		return nil
	}
	table := m.graph.alphaRouteTables[templateKey]
	if table == nil || len(table.indexed) == 0 {
		return nodeIDs
	}
	if fieldSlot, ok := table.singleIndexedField(); ok {
		value, valueOK := fact.compiledFieldValue("", fieldSlot)
		if !valueOK {
			return nil
		}
		routeValue, routeOK := reteGraphAlphaRouteValueFromValue(value)
		if !routeOK {
			return nil
		}
		return table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}]
	}
	m.resetAlphaRouteScratch()
	for _, id := range table.unindexed {
		m.appendAlphaRouteCandidate(id)
	}
	for _, fieldSlot := range table.indexedFields {
		value, ok := fact.compiledFieldValue("", fieldSlot)
		if !ok {
			continue
		}
		routeValue, ok := reteGraphAlphaRouteValueFromValue(value)
		if !ok {
			continue
		}
		m.appendAlphaRouteBucket(table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}])
	}
	m.sortAlphaRouteScratch()
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) workingAlphaRouteIDs(templateKey TemplateKey, nodeIDs []reteGraphAlphaNodeID, fact *workingFact) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil || fact == nil {
		return nil
	}
	table := m.graph.alphaRouteTables[templateKey]
	if table == nil || len(table.indexed) == 0 {
		return nodeIDs
	}
	if fieldSlot, ok := table.singleIndexedField(); ok {
		value, valueOK := fact.compiledFieldValue("", fieldSlot)
		if !valueOK {
			return nil
		}
		routeValue, routeOK := reteGraphAlphaRouteValueFromValue(value)
		if !routeOK {
			return nil
		}
		return table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}]
	}
	m.resetAlphaRouteScratch()
	for _, id := range table.unindexed {
		m.appendAlphaRouteCandidate(id)
	}
	for _, fieldSlot := range table.indexedFields {
		value, ok := fact.compiledFieldValue("", fieldSlot)
		if !ok {
			continue
		}
		routeValue, ok := reteGraphAlphaRouteValueFromValue(value)
		if !ok {
			continue
		}
		m.appendAlphaRouteBucket(table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}])
	}
	m.sortAlphaRouteScratch()
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) resetAlphaRouteScratch() {
	if m == nil {
		return
	}
	m.alphaRouteScratch = m.alphaRouteScratch[:0]
	if m.alphaRouteSeen == nil {
		m.alphaRouteSeen = make(map[reteGraphAlphaNodeID]uint64)
	}
	m.alphaRouteEpoch++
	if m.alphaRouteEpoch != 0 {
		return
	}
	clear(m.alphaRouteSeen)
	m.alphaRouteEpoch = 1
}

func (m *reteGraphBetaMemory) appendAlphaRouteBucket(ids []reteGraphAlphaNodeID) {
	for _, id := range ids {
		m.appendAlphaRouteCandidate(id)
	}
}

func (m *reteGraphBetaMemory) appendAlphaRouteCandidate(id reteGraphAlphaNodeID) {
	if m == nil || id <= 0 {
		return
	}
	if m.alphaRouteSeen[id] == m.alphaRouteEpoch {
		return
	}
	m.alphaRouteSeen[id] = m.alphaRouteEpoch
	m.alphaRouteScratch = append(m.alphaRouteScratch, id)
}

func (m *reteGraphBetaMemory) sortAlphaRouteScratch() {
	if len(m.alphaRouteScratch) < 2 {
		return
	}
	slices.Sort(m.alphaRouteScratch)
}

func (m *reteGraphBetaMemory) matchedAlphaRouteIDsForFact(id FactID) []reteGraphAlphaNodeID {
	if m == nil || id.IsZero() {
		return nil
	}
	m.resetAlphaRouteScratch()
	for index := 1; index < len(m.alphaFacts); index++ {
		if m.alphaFacts[index].contains(id) {
			m.appendAlphaRouteCandidate(reteGraphAlphaNodeID(index))
		}
	}
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) insertAlphaMatch(nodeID reteGraphAlphaNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil {
		return false
	}
	node := m.graph.alphaNode(nodeID)
	if node == nil {
		return false
	}
	m.propagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, span, delta)
	return true
}

func (m *reteGraphBetaMemory) insertAlphaMatchGenerated(nodeID reteGraphAlphaNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil {
		return false
	}
	node := m.graph.alphaNode(nodeID)
	if node == nil {
		return false
	}
	m.propagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, span, delta)
	return true
}

func (m *reteGraphBetaMemory) propagateAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	alphaNodeID := reteGraphAlphaNodeID(0)
	if source.kind == reteGraphStageAlpha {
		alphaNodeID = reteGraphAlphaNodeID(source.id)
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		entry := terminal.entry
		if entry.conditionID == "" {
			entry = sourceEntry
		}
		if entry.conditionID == "" {
			delta.supported = false
			continue
		}
		m.recordAlphaFact(alphaNodeID, match.fact)
		token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), span)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.insertTerminalToken(terminal.terminalID, terminal.branchID, token, delta, span)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		switch successor.side {
		case reteGraphBetaInputLeft:
			entry := successor.entry
			if entry.conditionID == "" {
				entry = sourceEntry
			}
			if entry.conditionID == "" {
				delta.supported = false
				continue
			}
			m.recordAlphaFact(alphaNodeID, match.fact)
			token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), span)
			if token.isZero() || !m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta) {
				delta.supported = false
			}
		case reteGraphBetaInputRight:
			m.recordAlphaFact(alphaNodeID, match.fact)
			edgeMatch := conditionMatch{
				conditionID: successor.entry.conditionID,
				bindingSlot: successor.entry.bindingSlot,
				fact:        match.fact,
			}
			token := m.newTokenRef(tokenRef{}, successor.entry, edgeMatch, match.fact.Recency(), match.fact.Generation(), span)
			if token.isZero() || !m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta) {
				delta.supported = false
			}
		default:
			delta.supported = false
		}
	}
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		entry := sourceEntry
		if entry.conditionID == "" {
			delta.supported = false
			continue
		}
		m.recordAlphaFact(alphaNodeID, match.fact)
		token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), span)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.openAggregateBucket(aggregateID, token, span, delta)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		m.insertAggregateInput(aggregateID, match, span, delta)
	}
}

func (m *reteGraphBetaMemory) propagateRemoveAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	if counters != nil {
		counters.recordNegativePropagationEvent()
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		entry := terminal.entry
		if entry.conditionID == "" {
			entry = sourceEntry
		}
		if entry.conditionID == "" {
			delta.supported = false
			continue
		}
		token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), nil)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.removeTerminalToken(terminal.terminalID, terminal.branchID, token, counters, delta)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		switch successor.side {
		case reteGraphBetaInputLeft:
			entry := successor.entry
			if entry.conditionID == "" {
				entry = sourceEntry
			}
			if entry.conditionID == "" {
				delta.supported = false
				continue
			}
			token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), nil)
			if token.isZero() || !m.removeBetaInputToken(successor.betaNodeID, successor.side, token, counters, delta) {
				delta.supported = false
			}
		case reteGraphBetaInputRight:
			edgeMatch := conditionMatch{
				conditionID: successor.entry.conditionID,
				bindingSlot: successor.entry.bindingSlot,
				fact:        match.fact,
			}
			token := m.newTokenRef(tokenRef{}, successor.entry, edgeMatch, match.fact.Recency(), match.fact.Generation(), nil)
			if token.isZero() || !m.removeBetaInputToken(successor.betaNodeID, successor.side, token, counters, delta) {
				delta.supported = false
			}
		default:
			delta.supported = false
		}
	}
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		entry := sourceEntry
		if entry.conditionID == "" {
			delta.supported = false
			continue
		}
		token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), nil)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.removeAggregateBucket(aggregateID, token, counters, delta)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		m.removeAggregateInput(aggregateID, match, counters, delta)
	}
}

func (m *reteGraphBetaMemory) insertAggregateInput(id reteGraphAggregateNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil || match.fact.ID().IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	node := m.graph.aggregateNode(id)
	if node == nil {
		delta.supported = false
		return
	}
	token := m.newTokenRef(tokenRef{}, node.inputEntry, match, match.fact.Recency(), match.fact.Generation(), span)
	m.insertAggregateToken(id, token, span, delta)
}

func (m *reteGraphBetaMemory) removeAggregateInput(id reteGraphAggregateNodeID, match conditionMatch, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || match.fact.ID().IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	node := m.graph.aggregateNode(id)
	if node == nil {
		delta.supported = false
		return
	}
	token := m.newTokenRef(tokenRef{}, node.inputEntry, match, match.fact.Recency(), match.fact.Generation(), nil)
	m.removeAggregateToken(id, token, counters, delta)
}

func (m *reteGraphBetaMemory) insertAggregateToken(id reteGraphAggregateNodeID, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil || token.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	node := m.graph.aggregateNode(id)
	memory := m.aggregateMemory(id)
	match, ok := tokenLastMatch(token)
	if node == nil || memory == nil || !ok {
		delta.supported = false
		return
	}
	bucket := memory.bucketForParent(token.parent())
	memberKey := tokenRefKey(token)
	if existing, ok := bucket.members[memberKey]; ok {
		bucket.removeMember(node, existing)
	}
	member, ok := m.aggregateMember(node, token, match)
	if !ok {
		delta.supported = false
		return
	}
	if bucket.members == nil {
		bucket.members = make(map[graphTokenIdentityKey]reteGraphAggregateMember)
	}
	bucket.members[memberKey] = member
	if err := bucket.addMember(node, member); err != nil {
		delta.supported = false
		return
	}
	m.refreshAggregateOutputInternal(id, bucket, span, nil, delta)
}

func (m *reteGraphBetaMemory) removeAggregateToken(id reteGraphAggregateNodeID, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || token.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	node := m.graph.aggregateNode(id)
	memory := m.aggregateMemory(id)
	if node == nil || memory == nil {
		delta.supported = false
		return
	}
	bucket, ok := memory.bucketForParentIfExists(token.parent())
	if !ok {
		return
	}
	memberKey := tokenRefKey(token)
	member, ok := bucket.members[memberKey]
	if !ok {
		return
	}
	delete(bucket.members, memberKey)
	bucket.removeMember(node, member)
	m.refreshAggregateOutputInternal(id, bucket, nil, counters, delta)
}

func (m *reteGraphBetaMemory) openAggregateBucket(id reteGraphAggregateNodeID, parent tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil || parent.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	memory := m.aggregateMemory(id)
	if memory == nil {
		delta.supported = false
		return
	}
	bucket := memory.bucketForParent(parent)
	if bucket == nil {
		delta.supported = false
		return
	}
	if bucket.hasValue {
		return
	}
	m.refreshAggregateOutputInternal(id, bucket, span, nil, delta)
}

func (m *reteGraphBetaMemory) removeAggregateBucket(id reteGraphAggregateNodeID, parent tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || parent.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	memory := m.aggregateMemory(id)
	if memory == nil {
		delta.supported = false
		return
	}
	bucket, ok := memory.bucketForParentIfExists(parent)
	if !ok {
		return
	}
	if !bucket.token.isZero() {
		stage := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(id)}
		m.propagateRemoveFromStage(stage, bucket.token, counters, delta)
	}
	bucket.clear()
	delete(memory.buckets, tokenRefKey(parent))
}

func (m *reteGraphBetaMemory) removeAggregateBucketsContainingFact(id reteGraphAggregateNodeID, factID FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || factID.IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	memory := m.aggregateMemory(id)
	if memory == nil || len(memory.buckets) == 0 {
		return
	}
	for key, bucket := range memory.buckets {
		if bucket == nil || !bucket.parent.containsFact(factID) {
			continue
		}
		if !bucket.token.isZero() {
			stage := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(id)}
			m.propagateRemoveFromStage(stage, bucket.token, counters, delta)
		}
		bucket.clear()
		delete(memory.buckets, key)
	}
}

func (m *reteGraphBetaMemory) removeAggregateMembersContainingFact(id reteGraphAggregateNodeID, factID FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || factID.IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	node := m.graph.aggregateNode(id)
	memory := m.aggregateMemory(id)
	if node == nil || memory == nil || len(memory.buckets) == 0 {
		return
	}
	for _, bucket := range memory.buckets {
		if bucket == nil || len(bucket.members) == 0 {
			continue
		}
		changed := false
		for key, member := range bucket.members {
			if !member.token.containsFact(factID) {
				continue
			}
			delete(bucket.members, key)
			bucket.removeMember(node, member)
			changed = true
		}
		if changed {
			m.refreshAggregateOutputInternal(id, bucket, nil, counters, delta)
		}
	}
}

func (m *reteGraphBetaMemory) aggregateMember(node *reteGraphAggregateNode, token tokenRef, match conditionMatch) (reteGraphAggregateMember, bool) {
	if node == nil {
		return reteGraphAggregateMember{}, false
	}
	member := reteGraphAggregateMember{match: match, token: token}
	if len(node.specs) > 0 {
		member.values = make([]Value, len(node.specs))
	}
	bindings, ok := tokenConditionMatches(token)
	if !ok {
		return reteGraphAggregateMember{}, false
	}
	for i, spec := range node.specs {
		if spec.kind == AggregateCount {
			continue
		}
		value, ok, err := spec.expression.evaluate(match.fact, bindings)
		if err != nil || !ok {
			return reteGraphAggregateMember{}, false
		}
		member.values[i] = value
	}
	return member, true
}

func (m *reteGraphBetaMemory) refreshAggregateOutput(id reteGraphAggregateNodeID, span *propagationCounterSpan, delta *reteAgendaDelta) {
	memory := m.aggregateMemory(id)
	if memory == nil {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	for _, bucket := range memory.buckets {
		m.refreshAggregateOutputInternal(id, bucket, span, nil, delta)
	}
}

func (m *reteGraphBetaMemory) refreshAggregateOutputWithCounters(id reteGraphAggregateNodeID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	memory := m.aggregateMemory(id)
	if memory == nil {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	for _, bucket := range memory.buckets {
		m.refreshAggregateOutputInternal(id, bucket, nil, counters, delta)
	}
}

func (m *reteGraphBetaMemory) refreshAggregateOutputInternal(id reteGraphAggregateNodeID, bucket *reteGraphAggregateBucket, span *propagationCounterSpan, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	node := m.graph.aggregateNode(id)
	if node == nil || bucket == nil {
		delta.supported = false
		return
	}
	stage := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(id)}
	if !bucket.token.isZero() {
		m.propagateRemoveFromStage(stage, bucket.token, counters, delta)
		bucket.token = tokenRef{}
		bucket.hasValue = false
	}
	values, ok := bucket.results(node)
	if !ok || len(values) != len(node.entries) {
		return
	}
	token := bucket.parent
	generation := m.aggregateGeneration()
	for i, value := range values {
		entry := node.entries[i]
		entry.value = value
		entry.hasValue = true
		match := conditionMatch{
			conditionID: node.conditionID,
			bindingSlot: node.bindingSlot + i,
			value:       value,
			hasValue:    true,
		}
		token = m.newTokenRef(token, entry, match, 0, generation, span)
		if token.isZero() {
			delta.supported = false
			return
		}
	}
	bucket.token = token
	bucket.values = append(bucket.values[:0], values...)
	bucket.hasValue = true
	m.propagateFromStage(stage, token, span, delta)
}

func (m *reteGraphBetaMemory) aggregateGeneration() Generation {
	if m == nil {
		return 1
	}
	for _, fact := range m.facts {
		if !fact.ID().IsZero() {
			return fact.Generation()
		}
	}
	return 1
}

func (m *reteGraphAggregateNodeMemory) bucketForParent(parent tokenRef) *reteGraphAggregateBucket {
	if m == nil {
		return nil
	}
	if m.buckets == nil {
		m.buckets = make(map[graphTokenIdentityKey]*reteGraphAggregateBucket)
	}
	key := tokenRefKey(parent)
	bucket := m.buckets[key]
	if bucket != nil {
		return bucket
	}
	bucket = &reteGraphAggregateBucket{parent: parent}
	m.buckets[key] = bucket
	return bucket
}

func (m *reteGraphAggregateNodeMemory) bucketForParentIfExists(parent tokenRef) (*reteGraphAggregateBucket, bool) {
	if m == nil || m.buckets == nil {
		return nil, false
	}
	bucket := m.buckets[tokenRefKey(parent)]
	return bucket, bucket != nil
}

func (m *reteGraphAggregateBucket) addMember(node *reteGraphAggregateNode, member reteGraphAggregateMember) error {
	if m == nil || node == nil {
		return nil
	}
	m.ensureSpecState(len(node.specs))
	m.count++
	for i, spec := range node.specs {
		switch spec.kind {
		case AggregateCount:
			continue
		case AggregateSum:
			if err := m.addSum(i, member.values[i]); err != nil {
				return err
			}
		case AggregateMin:
			if err := m.addExtremum(i, member.values[i], true); err != nil {
				return err
			}
		case AggregateMax:
			if err := m.addExtremum(i, member.values[i], false); err != nil {
				return err
			}
		case AggregateCollect:
			m.addCollect(i, member)
		default:
			return fmt.Errorf("%w: unsupported aggregate kind %q", ErrAggregateEvaluation, spec.kind)
		}
	}
	return nil
}

func (m *reteGraphAggregateBucket) removeMember(node *reteGraphAggregateNode, member reteGraphAggregateMember) {
	if m == nil || node == nil {
		return
	}
	if m.count > 0 {
		m.count--
	}
	m.ensureSpecState(len(node.specs))
	for i, spec := range node.specs {
		switch spec.kind {
		case AggregateSum:
			value := member.values[i]
			switch value.Kind() {
			case ValueInt:
				if m.floaty[i] {
					m.floatSums[i] -= float64(value.intValue)
					continue
				}
				m.intSums[i] -= value.intValue
			case ValueFloat:
				m.recomputeSum(node, i)
			}
		case AggregateMin:
			m.removeExtremum(i, member.values[i], true)
		case AggregateMax:
			m.removeExtremum(i, member.values[i], false)
		case AggregateCollect:
			m.removeCollect(i, member)
		}
	}
}

func (m *reteGraphAggregateExtremum) clear() {
	if m == nil {
		return
	}
	if m.values != nil {
		clear(m.values)
	}
	m.current = Value{}
	m.have = false
}

func (m *reteGraphAggregateBucket) ensureSpecState(specs int) {
	if m == nil || specs <= 0 {
		return
	}
	for len(m.intSums) < specs {
		m.intSums = append(m.intSums, 0)
	}
	for len(m.floatSums) < specs {
		m.floatSums = append(m.floatSums, 0)
	}
	for len(m.floaty) < specs {
		m.floaty = append(m.floaty, false)
	}
	for len(m.extrema) < specs {
		m.extrema = append(m.extrema, reteGraphAggregateExtremum{})
	}
	for len(m.collects) < specs {
		m.collects = append(m.collects, nil)
	}
}

func (m *reteGraphAggregateBucket) addSum(index int, value Value) error {
	m.ensureSpecState(index + 1)
	switch value.Kind() {
	case ValueInt:
		if m.floaty[index] {
			m.floatSums[index] += float64(value.intValue)
			return nil
		}
		next, overflow := safeAddInt64(m.intSums[index], value.intValue)
		if overflow {
			return fmt.Errorf("%w: integer sum overflow", ErrAggregateEvaluation)
		}
		m.intSums[index] = next
	case ValueFloat:
		if !m.floaty[index] {
			m.floatSums[index] = float64(m.intSums[index])
			m.intSums[index] = 0
			m.floaty[index] = true
		}
		m.floatSums[index] += value.floatValue
	default:
		return fmt.Errorf("%w: sum input must be numeric", ErrAggregateEvaluation)
	}
	return nil
}

func (m *reteGraphAggregateBucket) addExtremum(index int, value Value, min bool) error {
	m.ensureSpecState(index + 1)
	extremum := &m.extrema[index]
	if extremum.values == nil {
		extremum.values = make(map[string]reteGraphAggregateExtremumValue)
	}
	key := value.canonicalKey()
	entry := extremum.values[key]
	if entry.count == 0 {
		if extremum.have {
			if _, ok := compareValues(value, extremum.current); !ok {
				return fmt.Errorf("%w: min/max input is not comparable", ErrAggregateEvaluation)
			}
		}
		entry.value = cloneValue(value)
	}
	entry.count++
	extremum.values[key] = entry
	if !extremum.have {
		extremum.current = cloneValue(value)
		extremum.have = true
		return nil
	}
	comparison, ok := compareValues(value, extremum.current)
	if !ok {
		return fmt.Errorf("%w: min/max input is not comparable", ErrAggregateEvaluation)
	}
	if (min && comparison < 0) || (!min && comparison > 0) {
		extremum.current = cloneValue(value)
	}
	return nil
}

func (m *reteGraphAggregateBucket) removeExtremum(index int, value Value, min bool) {
	if m == nil || index < 0 || index >= len(m.extrema) {
		return
	}
	extremum := &m.extrema[index]
	if len(extremum.values) == 0 {
		return
	}
	key := value.canonicalKey()
	entry, ok := extremum.values[key]
	if !ok {
		return
	}
	if entry.count > 1 {
		entry.count--
		extremum.values[key] = entry
		return
	}
	delete(extremum.values, key)
	if !extremum.have || !extremum.current.Equal(value) {
		return
	}
	extremum.current = Value{}
	extremum.have = false
	for _, candidate := range extremum.values {
		if candidate.count <= 0 {
			continue
		}
		if !extremum.have {
			extremum.current = cloneValue(candidate.value)
			extremum.have = true
			continue
		}
		comparison, ok := compareValues(candidate.value, extremum.current)
		if !ok {
			continue
		}
		if (min && comparison < 0) || (!min && comparison > 0) {
			extremum.current = cloneValue(candidate.value)
		}
	}
}

func (m *reteGraphAggregateBucket) addCollect(index int, member reteGraphAggregateMember) {
	m.ensureSpecState(index + 1)
	entry := reteGraphAggregateCollectEntry{
		key:    tokenRefKey(member.token),
		factID: member.match.fact.ID(),
		value:  cloneValue(member.values[index]),
	}
	entries := m.collects[index]
	insertAt := sort.Search(len(entries), func(i int) bool {
		return !collectEntryLess(entries[i], entry)
	})
	if insertAt < len(entries) && entries[insertAt].key == entry.key {
		entries[insertAt] = entry
		m.collects[index] = entries
		return
	}
	entries = append(entries, reteGraphAggregateCollectEntry{})
	copy(entries[insertAt+1:], entries[insertAt:])
	entries[insertAt] = entry
	m.collects[index] = entries
}

func (m *reteGraphAggregateBucket) removeCollect(index int, member reteGraphAggregateMember) {
	if m == nil || index < 0 || index >= len(m.collects) {
		return
	}
	key := tokenRefKey(member.token)
	entries := m.collects[index]
	for i, entry := range entries {
		if entry.key != key {
			continue
		}
		copy(entries[i:], entries[i+1:])
		entries[len(entries)-1] = reteGraphAggregateCollectEntry{}
		m.collects[index] = entries[:len(entries)-1]
		return
	}
}

func collectEntryLess(left, right reteGraphAggregateCollectEntry) bool {
	if factIDLess(left.factID, right.factID) {
		return true
	}
	if factIDLess(right.factID, left.factID) {
		return false
	}
	if left.key.generation != right.key.generation {
		return left.key.generation < right.key.generation
	}
	if left.key.size != right.key.size {
		return left.key.size < right.key.size
	}
	return left.key.identityState < right.key.identityState
}

func (m *reteGraphAggregateBucket) recomputeSum(node *reteGraphAggregateNode, index int) {
	if m == nil || node == nil {
		return
	}
	m.ensureSpecState(index + 1)
	m.intSums[index] = 0
	m.floatSums[index] = 0
	m.floaty[index] = false
	for _, member := range m.members {
		_ = m.addSum(index, member.values[index])
	}
}

func (m *reteGraphAggregateBucket) results(node *reteGraphAggregateNode) ([]Value, bool) {
	if m == nil || node == nil {
		return nil, false
	}
	m.ensureSpecState(len(node.specs))
	values := make([]Value, len(node.specs))
	for i, spec := range node.specs {
		switch spec.kind {
		case AggregateCount:
			values[i] = newIntValue(m.count)
		case AggregateSum:
			if m.floaty[i] {
				value, err := canonicalFloat(m.floatSums[i])
				if err != nil {
					return nil, false
				}
				values[i] = value
				continue
			}
			values[i] = newIntValue(m.intSums[i])
		case AggregateMin, AggregateMax:
			if i >= len(m.extrema) || !m.extrema[i].have {
				return nil, false
			}
			values[i] = cloneValue(m.extrema[i].current)
		case AggregateCollect:
			collected := make([]Value, len(m.collects[i]))
			for j, entry := range m.collects[i] {
				collected[j] = cloneValue(entry.value)
			}
			value, err := canonicalValue(collected)
			if err != nil {
				return nil, false
			}
			values[i] = value
		default:
			return nil, false
		}
	}
	return values, true
}

func (m *reteGraphBetaMemory) propagateFromStage(source reteGraphStageRef, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		m.insertTerminalToken(terminal.terminalID, terminal.branchID, token, delta, span)
	}
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		m.openAggregateBucket(aggregateID, token, span, delta)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		m.insertAggregateToken(aggregateID, token, span, delta)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if !m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) propagateRemoveFromStage(source reteGraphStageRef, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || token.isZero() {
		return
	}
	if counters != nil {
		counters.recordNegativePropagationEvent()
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		m.removeTerminalToken(terminal.terminalID, terminal.branchID, token, counters, delta)
	}
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		m.removeAggregateBucket(aggregateID, token, counters, delta)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		m.removeAggregateToken(aggregateID, token, counters, delta)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		if !m.removeBetaInputToken(successor.betaNodeID, successor.side, token, counters, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) propagateRemoveFactFromStage(source reteGraphStageRef, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		m.removeTerminalTokensContainingFact(terminal.terminalID, id, counters, delta)
	}
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		m.removeAggregateBucketsContainingFact(aggregateID, id, counters, delta)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		m.removeAggregateMembersContainingFact(aggregateID, id, counters, delta)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		if !m.removeBetaInputContainingFact(successor.betaNodeID, successor.side, id, counters, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) insertBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, entry bindingTupleEntry, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || token.isZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	if node.kind == reteGraphBetaNodeNot {
		return m.insertNegativeBetaInput(nodeID, side, node, token, span, delta)
	}
	nodeMemory := m.nodeMemory(nodeID)
	var inserted bool
	var joinKey betaJoinKey
	var ok bool
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok = graphBetaJoinKeyForLeftToken(node, token)
		if !ok {
			return false
		}
		inserted = nodeMemory.left.insert(token, joinKey)
	case reteGraphBetaInputRight:
		joinKey, ok = graphBetaJoinKeyForRightToken(node, token)
		if !ok {
			return false
		}
		inserted = nodeMemory.right.insert(token, joinKey)
	default:
		return false
	}
	if !inserted {
		return true
	}
	if span != nil {
		span.recordBetaInputInsert(side)
	}
	switch side {
	case reteGraphBetaInputLeft:
		bucket := nodeMemory.right.bucketForKey(joinKey)
		if span != nil {
			span.recordBetaBucketProbe(bucket.len())
		}
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			rightRow := nodeMemory.right.row(rowID)
			if rightRow == nil || rightRow.token.isZero() {
				continue
			}
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, span); err != nil {
				return false
			} else if !ok {
				continue
			}
			output := m.newTokenRef(token, entry, rightMatch, rightMatch.fact.Recency(), rightMatch.fact.Generation(), span)
			if output.isZero() {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta)
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			return false
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		if span != nil {
			span.recordBetaBucketProbe(bucket.len())
		}
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, span); err != nil {
				return false
			} else if !ok {
				continue
			}
			output := m.newTokenRef(leftRow.token, entry, currentMatch, currentMatch.fact.Recency(), currentMatch.fact.Generation(), span)
			if output.isZero() {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta)
		}
	}
	return true
}

func (m *reteGraphBetaMemory) insertNegativeBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || node == nil || token.isZero() {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok := graphBetaJoinKeyForLeftToken(node, token)
		if !ok {
			return false
		}
		inserted := nodeMemory.left.insert(token, joinKey)
		if !inserted {
			return true
		}
		if span != nil {
			span.recordBetaInputInsert(side)
		}
		count, ok := m.negativeRightMatchCountForLeft(node, nodeMemory, joinKey, token, span)
		if !ok {
			return false
		}
		if count == 0 {
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			m.propagateFromStage(source, token, span, delta)
		}
	case reteGraphBetaInputRight:
		joinKey, ok := graphBetaJoinKeyForRightToken(node, token)
		if !ok {
			return false
		}
		inserted := nodeMemory.right.insert(token, joinKey)
		if !inserted {
			return true
		}
		if span != nil {
			span.recordBetaInputInsert(side)
		}
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			return false
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		if span != nil {
			span.recordBetaBucketProbe(bucket.len())
		}
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, span); err != nil {
				return false
			} else if !ok {
				continue
			}
			count, ok := m.negativeRightMatchCountForLeft(node, nodeMemory, joinKey, leftRow.token, nil)
			if !ok {
				return false
			}
			if count == 1 {
				m.propagateRemoveFromStage(source, leftRow.token, nil, delta)
			}
		}
	default:
		return false
	}
	return true
}

func (m *reteGraphBetaMemory) removeBetaInputToken(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || token.isZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	if node.kind == reteGraphBetaNodeNot {
		return m.removeNegativeBetaInputToken(nodeID, side, node, token, counters, delta)
	}
	nodeMemory := m.nodeMemory(nodeID)
	var joinKey betaJoinKey
	var ok bool
	var removed tokenRef
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok = graphBetaJoinKeyForLeftToken(node, token)
		if !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.left.removeToken(token, counters)
		removed, ok = removedRow.token, removedOK
	case reteGraphBetaInputRight:
		joinKey, ok = graphBetaJoinKeyForRightToken(node, token)
		if !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.right.removeToken(token, counters)
		removed, ok = removedRow.token, removedOK
	default:
		return false
	}
	if !ok {
		return true
	}
	if counters != nil {
		counters.recordNegativeRowRemoved()
	}
	m.propagateJoinedRemovals(nodeID, side, node, nodeMemory, joinKey, removed, counters, delta)
	return true
}

func (m *reteGraphBetaMemory) removeNegativeBetaInputToken(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || node == nil || token.isZero() {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok := graphBetaJoinKeyForLeftToken(node, token)
		if !ok {
			return false
		}
		count, ok := m.negativeRightMatchCountForLeft(node, nodeMemory, joinKey, token, nil)
		if !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.left.removeToken(token, counters)
		if !removedOK {
			return true
		}
		if counters != nil {
			counters.recordNegativeRowRemoved()
		}
		if count == 0 {
			m.propagateRemoveFromStage(source, removedRow.token, counters, delta)
		}
	case reteGraphBetaInputRight:
		joinKey, ok := graphBetaJoinKeyForRightToken(node, token)
		if !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.right.removeToken(token, counters)
		if !removedOK {
			return true
		}
		if counters != nil {
			counters.recordNegativeRowRemoved()
		}
		currentMatch, ok := tokenLastMatch(removedRow.token)
		if !ok {
			return false
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				continue
			}
			count, ok := m.negativeRightMatchCountForLeft(node, nodeMemory, joinKey, leftRow.token, nil)
			if !ok {
				return false
			}
			if count == 0 {
				m.propagateFromStage(source, leftRow.token, nil, delta)
			}
		}
	default:
		return false
	}
	return true
}

func (m *reteGraphBetaMemory) propagateJoinedRemovals(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, nodeMemory *reteGraphBetaNodeMemory, joinKey betaJoinKey, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || node == nil || nodeMemory == nil || token.isZero() {
		return
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
	switch side {
	case reteGraphBetaInputLeft:
		bucket := nodeMemory.right.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			rightRow := nodeMemory.right.row(rowID)
			if rightRow == nil || rightRow.token.isZero() {
				continue
			}
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				continue
			}
			output := m.newTokenRef(token, node.entry, rightMatch, rightMatch.fact.Recency(), rightMatch.fact.Generation(), nil)
			if output.isZero() {
				delta.supported = false
				continue
			}
			m.propagateRemoveFromStage(source, output, counters, delta)
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			delta.supported = false
			return
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				continue
			}
			output := m.newTokenRef(leftRow.token, node.entry, currentMatch, currentMatch.fact.Recency(), currentMatch.fact.Generation(), nil)
			if output.isZero() {
				delta.supported = false
				continue
			}
			m.propagateRemoveFromStage(source, output, counters, delta)
		}
	default:
		delta.supported = false
	}
}

func (m *reteGraphBetaMemory) removeBetaInputContainingFact(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || id.IsZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	if node.kind == reteGraphBetaNodeNot {
		return m.removeNegativeBetaInputContainingFact(nodeID, side, node, id, counters, delta)
	}
	nodeMemory := m.nodeMemory(nodeID)
	var removed int
	switch side {
	case reteGraphBetaInputLeft:
		removed = nodeMemory.left.removeContainingFact(id, counters)
	case reteGraphBetaInputRight:
		removed = nodeMemory.right.removeContainingFact(id, counters)
	default:
		return false
	}
	if removed > 0 {
		m.propagateRemoveFactFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, id, counters, delta)
	}
	return true
}

func (m *reteGraphBetaMemory) removeNegativeBetaInputContainingFact(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || node == nil || id.IsZero() {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
	switch side {
	case reteGraphBetaInputLeft:
		nodeMemory.left.removeTokensContainingFact(id, counters, func(row graphTokenRow) {
			joinKey, ok := graphBetaJoinKeyForLeftToken(node, row.token)
			if !ok {
				delta.supported = false
				return
			}
			count, ok := m.negativeRightMatchCountForLeft(node, nodeMemory, joinKey, row.token, nil)
			if !ok {
				delta.supported = false
				return
			}
			if count == 0 {
				m.propagateRemoveFromStage(source, row.token, counters, delta)
			}
		})
	case reteGraphBetaInputRight:
		var tokens []tokenRef
		nodeMemory.right.forEachTokenContainingFact(id, counters, func(row graphTokenRow) {
			if !row.token.isZero() {
				tokens = append(tokens, row.token)
			}
		})
		for _, token := range tokens {
			if !m.removeNegativeBetaInputToken(nodeID, side, node, token, counters, delta) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

func (m *reteGraphBetaMemory) removeFact(fact FactSnapshot, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
	id := fact.ID()
	defer m.removeFactSource(id)
	nodeIDs := m.matchedAlphaRouteIDsForFact(id)
	if len(nodeIDs) == 0 {
		nodeIDs = m.snapshotAlphaRouteIDsForFact(fact)
		if len(nodeIDs) == 0 {
			m.removeAlphaFact(id)
			return delta
		}
	}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if !node.matchesSnapshot(fact) {
			continue
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromSnapshot(fact),
		}
		m.propagateRemoveAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, counters, &delta)
	}
	m.removeAlphaFact(id)
	return delta
}

func (m *reteGraphBetaMemory) removeFactByIndexes(id FactID, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
	defer m.removeFactSource(id)
	m.removeAlphaFact(id)
	for _, terminalNode := range m.graph.terminalNodes {
		if terminalNode.kind != reteGraphTerminalRule {
			continue
		}
		terminal := m.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		terminal.rows.forEachTokenContainingFact(id, counters, func(row graphTokenRow) {
			delta.removed = append(delta.removed, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          row.token,
				identity:       row.terminalIdentity,
			})
			if counters != nil {
				counters.recordTerminalDeltaRemoved()
				counters.recordTerminalRowRemoved()
			}
		})
		terminal.rows.removeContainingFact(id, counters)
	}
	for _, node := range m.nodes {
		if node == nil {
			continue
		}
		node.left.removeContainingFact(id, counters)
		node.right.removeContainingFact(id, counters)
	}
	return delta
}

func (m *reteGraphBetaMemory) recordAlphaFact(nodeID reteGraphAlphaNodeID, fact conditionFactRef) {
	if m == nil || nodeID <= 0 || fact.ID().IsZero() {
		return
	}
	index := int(nodeID)
	if index <= 0 || index >= len(m.alphaFacts) {
		return
	}
	if !m.alphaFacts[index].insert(fact.ID()) {
		return
	}
	if m.alphaFactCounts == nil {
		m.alphaFactCounts = make(map[ConditionID]int)
	}
	for _, conditionID := range m.alphaConditions[index] {
		m.alphaFactCounts[conditionID]++
	}
}

func (m *reteGraphBetaMemory) removeAlphaFact(id FactID) {
	if m == nil || id.IsZero() {
		return
	}
	for index := range m.alphaFacts {
		if !m.alphaFacts[index].remove(id) {
			continue
		}
		for _, conditionID := range m.alphaConditions[index] {
			if m.alphaFactCounts[conditionID] <= 1 {
				delete(m.alphaFactCounts, conditionID)
				continue
			}
			m.alphaFactCounts[conditionID]--
		}
	}
}

func (m *reteGraphBetaMemory) alphaFactCount(conditionID ConditionID) int {
	if m == nil || conditionID == "" {
		return 0
	}
	return m.alphaFactCounts[conditionID]
}

func (m *reteGraphBetaMemory) updateFact(before, after FactSnapshot, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	removed := m.removeFact(before, counters)
	added := m.insertFact(after, nil)
	addedTokens, removedTokens := coalesceTerminalTokenDeltas(m.revision, append(removed.added, added.added...), append(removed.removed, added.removed...))
	return reteAgendaDelta{
		supported: removed.supported && added.supported,
		added:     addedTokens,
		removed:   removedTokens,
	}
}

func coalesceTerminalTokenDeltas(revision *Ruleset, added, removed []reteTerminalTokenDelta) ([]reteTerminalTokenDelta, []reteTerminalTokenDelta) {
	if len(added) == 0 || len(removed) == 0 {
		return added, removed
	}
	keptAdded := added[:0]
	for _, add := range added {
		match := -1
		for i, remove := range removed {
			if terminalTokenDeltasEqual(revision, add, remove) {
				match = i
				break
			}
		}
		if match < 0 {
			keptAdded = append(keptAdded, add)
			continue
		}
		copy(removed[match:], removed[match+1:])
		removed[len(removed)-1] = reteTerminalTokenDelta{}
		removed = removed[:len(removed)-1]
	}
	return keptAdded, removed
}

func (m *reteGraphBetaMemory) beginTerminalTokenDelta() reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	return reteAgendaDelta{
		supported: true,
		added:     m.terminalTokenDeltas[:0],
	}
}

func (m *reteGraphBetaMemory) finishTerminalTokenDelta(delta reteAgendaDelta) reteAgendaDelta {
	if m == nil {
		return delta
	}
	m.terminalTokenDeltas = delta.added
	return delta
}

func (m *reteGraphBetaMemory) insertTerminalToken(terminalID reteGraphTerminalNodeID, branchID int, token tokenRef, delta *reteAgendaDelta, span *propagationCounterSpan) {
	if m == nil || delta == nil || token.isZero() {
		return
	}
	terminal := m.terminal(terminalID)
	if terminal == nil {
		delta.supported = false
		return
	}
	ruleRevisionID := m.terminalRuleRevision(terminalID)
	identity := m.terminalTokenIdentity(ruleRevisionID, token)
	if !terminal.rows.insertTerminal(token, identity, branchID) {
		if span != nil {
			span.recordTerminalRowDeduped()
		}
		return
	}
	if span != nil {
		span.recordTerminalRowInserted()
	}
	if span != nil {
		span.recordTerminalDeltaEmitted()
	}
	delta.added = append(delta.added, reteTerminalTokenDelta{
		ruleRevisionID: ruleRevisionID,
		token:          token,
		identity:       identity,
	})
}

func (m *reteGraphBetaMemory) removeTerminalTokensContainingFact(terminalID reteGraphTerminalNodeID, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || id.IsZero() {
		return
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return
	}
	ruleRevisionID := m.terminalRuleRevision(terminalID)
	terminal.rows.removeTokensContainingFact(id, counters, func(row graphTokenRow) {
		delta.removed = append(delta.removed, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          row.token,
			identity:       row.terminalIdentity,
		})
		if counters != nil {
			counters.recordTerminalDeltaRemoved()
			counters.recordTerminalRowRemoved()
		}
	})
}

func (m *reteGraphBetaMemory) removeTerminalToken(terminalID reteGraphTerminalNodeID, branchID int, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || token.isZero() {
		return
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return
	}
	removed, ok := terminal.rows.removeToken(token, counters, branchID)
	if !ok {
		return
	}
	ruleRevisionID := m.terminalRuleRevision(terminalID)
	delta.removed = append(delta.removed, reteTerminalTokenDelta{
		ruleRevisionID: ruleRevisionID,
		token:          removed.token,
		identity:       removed.terminalIdentity,
	})
	if counters != nil {
		counters.recordTerminalDeltaRemoved()
		counters.recordTerminalRowRemoved()
		counters.recordNegativeTerminalRowRemoved()
	}
}

func (m *reteGraphBetaMemory) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	if m == nil || m.graph == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	deltas := m.terminalTokenDeltas[:0]
	for _, terminalNode := range m.graph.terminalNodes {
		if terminalNode.kind != reteGraphTerminalRule {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		terminal := m.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		for _, row := range terminal.rows.rows {
			if row.token.isZero() {
				continue
			}
			deltas = append(deltas, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          row.token,
				identity:       row.terminalIdentity,
			})
		}
	}
	m.terminalTokenDeltas = deltas
	return deltas, true, nil
}

func (m *reteGraphBetaMemory) queryRows(ctx context.Context, query compiledQuery, args map[string]Value, trigger FactSnapshot, source Snapshot) ([]QueryRow, bool, error) {
	if m == nil || m.graph == nil {
		return nil, false, nil
	}
	terminalIDs := m.graph.queryTerminalIDs[query.name]
	if len(terminalIDs) == 0 {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if m.queryArena == nil {
		m.queryArena = newTokenArena()
	} else {
		m.queryArena.reset()
	}
	if source.revision == nil {
		source.revision = m.revision
	}
	collector := reteGraphQueryCollector{
		ctx:        ctx,
		query:      query,
		args:       args,
		source:     source,
		terminal:   make(map[reteGraphTerminalNodeID]struct{}, len(terminalIDs)),
		tokenArena: m.queryArena,
	}
	for _, terminalID := range terminalIDs {
		collector.terminal[terminalID] = struct{}{}
	}
	routeIDs := m.snapshotAlphaRouteIDsForFact(trigger)
	for _, nodeID := range routeIDs {
		if err := ctx.Err(); err != nil {
			return nil, true, err
		}
		node := m.graph.alphaNode(nodeID)
		if node == nil || !node.matchesSnapshot(trigger) {
			continue
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromSnapshot(trigger),
		}
		if err := m.queryPropagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, &collector); err != nil {
			return nil, true, err
		}
	}
	return collector.rows, true, nil
}

type reteGraphQueryCollector struct {
	ctx        context.Context
	query      compiledQuery
	args       map[string]Value
	source     Snapshot
	terminal   map[reteGraphTerminalNodeID]struct{}
	tokenArena *tokenArena
	rows       []QueryRow
}

func (m *reteGraphBetaMemory) queryPropagateAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, collector *reteGraphQueryCollector) error {
	if m == nil || collector == nil {
		return nil
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		if _, ok := collector.terminal[terminal.terminalID]; !ok {
			continue
		}
		entry := terminal.entry
		if entry.conditionID == "" {
			entry = sourceEntry
		}
		if entry.conditionID == "" {
			return fmt.Errorf("%w: malformed query alpha terminal", ErrQueryExecution)
		}
		token := collector.tokenArena.add(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation())
		if token.isZero() {
			return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
		}
		if err := m.queryCollectTerminalToken(token, collector); err != nil {
			return err
		}
	}
	for _, successor := range m.graph.successorsByStage[source] {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			return fmt.Errorf("%w: malformed query beta successor", ErrQueryExecution)
		}
		switch successor.side {
		case reteGraphBetaInputLeft:
			entry := successor.entry
			if entry.conditionID == "" {
				entry = sourceEntry
			}
			if entry.conditionID == "" {
				return fmt.Errorf("%w: malformed query left input", ErrQueryExecution)
			}
			token := collector.tokenArena.add(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation())
			if token.isZero() {
				return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
			}
			if err := m.queryProbeBetaInput(successor.betaNodeID, successor.side, token, node.entry, collector); err != nil {
				return err
			}
		case reteGraphBetaInputRight:
			edgeMatch := conditionMatch{
				conditionID: successor.entry.conditionID,
				bindingSlot: successor.entry.bindingSlot,
				fact:        match.fact,
			}
			token := collector.tokenArena.add(tokenRef{}, successor.entry, edgeMatch, match.fact.Recency(), match.fact.Generation())
			if token.isZero() {
				return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
			}
			if err := m.queryProbeBetaInput(successor.betaNodeID, successor.side, token, node.entry, collector); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: malformed query beta side", ErrQueryExecution)
		}
	}
	return nil
}

func (m *reteGraphBetaMemory) queryPropagateFromStage(source reteGraphStageRef, token tokenRef, collector *reteGraphQueryCollector) error {
	if m == nil || collector == nil || token.isZero() {
		return nil
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		if _, ok := collector.terminal[terminal.terminalID]; !ok {
			continue
		}
		if err := m.queryCollectTerminalToken(token, collector); err != nil {
			return err
		}
	}
	for _, successor := range m.graph.successorsByStage[source] {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			return fmt.Errorf("%w: malformed query beta successor", ErrQueryExecution)
		}
		if err := m.queryProbeBetaInput(successor.betaNodeID, successor.side, token, node.entry, collector); err != nil {
			return err
		}
	}
	return nil
}

func (m *reteGraphBetaMemory) queryProbeBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, entry bindingTupleEntry, collector *reteGraphQueryCollector) error {
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return fmt.Errorf("%w: malformed query beta node", ErrQueryExecution)
	}
	if node.kind == reteGraphBetaNodeNot {
		return m.queryProbeNegativeBetaInput(nodeID, side, node, token, collector)
	}
	nodeMemory := m.nodeMemory(nodeID)
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok := graphBetaJoinKeyForLeftToken(node, token)
		if !ok {
			return fmt.Errorf("%w: malformed query left join key", ErrQueryExecution)
		}
		bucket := nodeMemory.right.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			rightRow := nodeMemory.right.row(rowID)
			if rightRow == nil || rightRow.token.isZero() {
				continue
			}
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, nil); err != nil {
				return err
			} else if !ok {
				continue
			}
			output := collector.tokenArena.add(token, entry, rightMatch, rightMatch.fact.Recency(), rightMatch.fact.Generation())
			if output.isZero() {
				return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
			}
			if err := m.queryPropagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, collector); err != nil {
				return err
			}
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			return fmt.Errorf("%w: malformed query right token", ErrQueryExecution)
		}
		joinKey, ok := graphBetaJoinKeyForRightToken(node, token)
		if !ok {
			return fmt.Errorf("%w: malformed query right join key", ErrQueryExecution)
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, nil); err != nil {
				return err
			} else if !ok {
				continue
			}
			output := collector.tokenArena.add(leftRow.token, entry, currentMatch, currentMatch.fact.Recency(), currentMatch.fact.Generation())
			if output.isZero() {
				return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
			}
			if err := m.queryPropagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, collector); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: malformed query beta side", ErrQueryExecution)
	}
	return nil
}

func (m *reteGraphBetaMemory) queryProbeNegativeBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, collector *reteGraphQueryCollector) error {
	if side != reteGraphBetaInputLeft {
		return nil
	}
	nodeMemory := m.nodeMemory(nodeID)
	joinKey, ok := graphBetaJoinKeyForLeftToken(node, token)
	if !ok {
		return fmt.Errorf("%w: malformed query negative join key", ErrQueryExecution)
	}
	count, ok := m.negativeRightMatchCountForLeft(node, nodeMemory, joinKey, token, nil)
	if !ok {
		return fmt.Errorf("%w: malformed query negative input", ErrQueryExecution)
	}
	if count != 0 {
		return nil
	}
	return m.queryPropagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, token, collector)
}

func (m *reteGraphBetaMemory) queryCollectTerminalToken(token tokenRef, collector *reteGraphQueryCollector) error {
	if token.isZero() || collector == nil {
		return nil
	}
	if token.size() == 0 {
		return fmt.Errorf("%w: malformed query terminal token", ErrQueryExecution)
	}
	row, err := collector.query.materializeTokenRow(collector.source, token, collector.args, 1)
	if err != nil {
		return err
	}
	collector.rows = append(collector.rows, row)
	return nil
}

func (m *reteGraphBetaMemory) terminalTokenIdentity(ruleRevisionID RuleRevisionID, token tokenRef) candidateIdentity {
	if m == nil || m.revision == nil || token.isZero() {
		return candidateIdentity{}
	}
	rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
	if !ok {
		return candidateIdentity{}
	}
	return candidateIdentityForTerminalToken(rule, token)
}

func (m *reteGraphBetaMemory) newTokenRef(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	}
	return m.arena.add(parent, entry, match, recency, generation)
}

func (m *reteGraphBetaMemory) nodeMemory(id reteGraphBetaNodeID) *reteGraphBetaNodeMemory {
	if m == nil || id <= 0 {
		return nil
	}
	index := int(id)
	if index >= len(m.nodes) {
		next := make([]*reteGraphBetaNodeMemory, index+1)
		copy(next, m.nodes)
		m.nodes = next
	}
	node := m.nodes[index]
	if node != nil {
		return node
	}
	node = &reteGraphBetaNodeMemory{}
	m.nodes[index] = node
	return node
}

func (m *reteGraphBetaMemory) aggregateMemory(id reteGraphAggregateNodeID) *reteGraphAggregateNodeMemory {
	if m == nil || id <= 0 {
		return nil
	}
	index := int(id)
	if index >= len(m.aggregates) {
		next := make([]*reteGraphAggregateNodeMemory, index+1)
		copy(next, m.aggregates)
		m.aggregates = next
	}
	aggregate := m.aggregates[index]
	if aggregate != nil {
		return aggregate
	}
	aggregate = &reteGraphAggregateNodeMemory{}
	m.aggregates[index] = aggregate
	return aggregate
}

func (m *reteGraphBetaMemory) terminal(id reteGraphTerminalNodeID) *reteGraphTerminalMemory {
	if m == nil || id <= 0 {
		return nil
	}
	index := int(id)
	if index >= len(m.terminals) {
		next := make([]*reteGraphTerminalMemory, index+1)
		copy(next, m.terminals)
		m.terminals = next
	}
	terminal := m.terminals[index]
	if terminal != nil {
		return terminal
	}
	terminal = &reteGraphTerminalMemory{}
	m.terminals[index] = terminal
	return terminal
}

func (m *reteGraphBetaMemory) terminalAt(id reteGraphTerminalNodeID) *reteGraphTerminalMemory {
	if m == nil || id <= 0 {
		return nil
	}
	index := int(id)
	if index >= len(m.terminals) {
		return nil
	}
	return m.terminals[index]
}

func (m *reteGraphBetaMemory) terminalRuleRevision(id reteGraphTerminalNodeID) RuleRevisionID {
	if m == nil || m.graph == nil || id <= 0 {
		return ""
	}
	index := int(id) - 1
	if index < 0 || index >= len(m.graph.terminalNodes) {
		return ""
	}
	return m.graph.terminalNodes[index].ruleRevisionID
}

func tokenRefKey(token tokenRef) graphTokenIdentityKey {
	return graphTokenIdentityKey{
		size:          token.size(),
		generation:    token.generation(),
		identityState: token.identityState(),
	}
}

func tokenLastMatch(token tokenRef) (conditionMatch, bool) {
	row, ok := token.resolve()
	if !ok {
		return conditionMatch{}, false
	}
	return row.match, true
}

func tokenConditionMatches(token tokenRef) ([]conditionMatch, bool) {
	row, ok := token.resolve()
	if !ok {
		return nil, false
	}
	matches := make([]conditionMatch, row.size)
	for i := range matches {
		match, ok := token.matchAt(i)
		if !ok {
			return nil, false
		}
		matches[i] = match
	}
	return matches, true
}

func graphBetaJoinKeyForLeftToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	if node == nil {
		return betaJoinKey{}, false
	}
	if len(node.hashJoins) == 0 {
		return betaJoinKey{}, true
	}
	return betaJoinKeyForPlan(compiledConditionPlan{joins: node.hashJoins}, func(join compiledJoinConstraint) (Value, bool) {
		match, ok := tokenRefAtSlot(token, join.refBindingSlot)
		if !ok {
			return Value{}, false
		}
		return match.fact.compiledFieldValue(join.refField, join.refFieldSlot)
	})
}

func graphBetaJoinKeyForRightToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	if node == nil {
		return betaJoinKey{}, false
	}
	match, ok := tokenLastMatch(token)
	if !ok {
		return betaJoinKey{}, false
	}
	if len(node.hashJoins) == 0 {
		return betaJoinKey{}, true
	}
	return betaJoinKeyForPlan(compiledConditionPlan{joins: node.hashJoins}, func(join compiledJoinConstraint) (Value, bool) {
		return match.fact.compiledFieldValue(join.field, join.fieldSlot)
	})
}

func (m *reteGraphBetaMemory) residualJoinsMatch(node *reteGraphBetaNode, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	if m == nil || node == nil {
		return true, nil
	}
	for _, join := range node.residualJoins {
		if span != nil {
			span.recordBetaResidualTest()
		}
		ok, err := join.matchesToken(fact, bindings)
		if err != nil {
			return false, err
		}
		if !ok {
			if span != nil {
				span.recordBetaResidualFailure()
			}
			return false, nil
		}
	}
	ok, err := expressionPredicatesMatchToken(node.predicates, fact, bindings, span)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return true, nil
}

func (m *reteGraphBetaMemory) negativeRightMatchCountForLeft(node *reteGraphBetaNode, nodeMemory *reteGraphBetaNodeMemory, joinKey betaJoinKey, left tokenRef, span *propagationCounterSpan) (int, bool) {
	if m == nil || node == nil || nodeMemory == nil || left.isZero() {
		return 0, false
	}
	bucket := nodeMemory.right.bucketForKey(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(bucket.len())
	}
	count := 0
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		rightRow := nodeMemory.right.row(rowID)
		if rightRow == nil || rightRow.token.isZero() {
			continue
		}
		rightMatch, ok := tokenLastMatch(rightRow.token)
		if !ok {
			return count, false
		}
		ok, err := m.residualJoinsMatch(node, rightMatch.fact, left, span)
		if err != nil {
			return count, false
		}
		if ok {
			count++
		}
	}
	return count, true
}

func (m *reteGraphBetaMemory) rowCount() int {
	if m == nil {
		return 0
	}
	total := 0
	for _, node := range m.nodes {
		if node != nil {
			total += len(node.left.rows)
			total += len(node.right.rows)
		}
	}
	for _, terminal := range m.terminals {
		if terminal != nil {
			total += len(terminal.rows.rows)
		}
	}
	return total
}

func (m *reteGraphBetaMemory) terminalRowCount() int {
	if m == nil {
		return 0
	}
	total := 0
	for _, terminal := range m.terminals {
		if terminal != nil {
			total += len(terminal.rows.rows)
		}
	}
	return total
}

func (m *reteGraphBetaMemory) memoryStats() reteGraphBetaMemoryStats {
	if m == nil {
		return reteGraphBetaMemoryStats{}
	}
	var stats reteGraphBetaMemoryStats
	for _, node := range m.nodes {
		if node == nil {
			continue
		}
		stats.addTokenMemory(node.left)
		stats.BetaTokenMemories++
		stats.addTokenMemory(node.right)
		stats.BetaTokenMemories++
	}
	for _, terminal := range m.terminals {
		if terminal == nil {
			continue
		}
		stats.addTokenMemory(terminal.rows)
		stats.TerminalTokenMemories++
	}
	return stats
}

func (s *reteGraphBetaMemoryStats) addTokenMemory(memory tokenHashMemory) {
	if s == nil {
		return
	}
	s.TokenMemories++
	rowCount := len(memory.rows)
	rowCapacity := cap(memory.rows)
	s.TokenRows += rowCount
	s.TokenRowCapacity += rowCapacity
	s.TokenRowReserve += memory.rowReserve
	s.TokenRowCapacityMax = max(s.TokenRowCapacityMax, rowCapacity)
	s.TokenRowReserveMax = max(s.TokenRowReserveMax, memory.rowReserve)

	joinKeys := len(memory.indexes)
	s.JoinIndexKeys += joinKeys
	s.JoinIndexReserve += memory.joinIndexReserve
	s.JoinIndexKeysMax = max(s.JoinIndexKeysMax, joinKeys)
	s.JoinIndexReserveMax = max(s.JoinIndexReserveMax, memory.joinIndexReserve)

	identityKeys := len(memory.identityRows)
	s.IdentityIndexKeys += identityKeys
	s.IdentityIndexReserve += memory.identityIndexReserve
	s.IdentityIndexKeysMax = max(s.IdentityIndexKeysMax, identityKeys)
	s.IdentityIndexReserveMax = max(s.IdentityIndexReserveMax, memory.identityIndexReserve)

	factKeys := len(memory.factRows)
	s.FactIndexKeys += factKeys
	s.FactIndexReserve += memory.factIndexReserve
	s.FactIndexKeysMax = max(s.FactIndexKeysMax, factKeys)
	s.FactIndexReserveMax = max(s.FactIndexReserveMax, memory.factIndexReserve)
}

func (m *reteGraphBetaMemory) match(ctx context.Context, source factSource, alphaSource alphaFactSource) ([]ruleMatchResult, error) {
	if m == nil || m.revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	results := make([]ruleMatchResult, 0, len(m.revision.ruleOrder))
	for _, ruleName := range m.revision.ruleOrder {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := m.revision.rules[ruleName]
		if !ok {
			return nil, ErrMatcher
		}
		var candidates []matchCandidate
		if rule.hasAggregateConditions() {
			matchSource := source
			if matchSource == nil {
				matchSource = m
			}
			var err error
			candidates, err = rule.matchCandidates(ctx, matchSource)
			if err != nil {
				return nil, err
			}
		} else if terminal := m.terminalForRule(rule.revisionID); terminal != nil {
			var err error
			candidates, err = m.collectTerminalCandidates(ctx, rule, terminal)
			if err != nil {
				return nil, err
			}
		}
		results = append(results, ruleMatchResult{
			ruleID:           rule.id,
			ruleRevisionID:   rule.revisionID,
			salience:         rule.salience,
			declarationOrder: rule.declarationOrder,
			candidates:       candidates,
		})
	}
	return results, nil
}

func (m *reteGraphBetaMemory) matchWithoutSnapshot(ctx context.Context, generation Generation) ([]ruleMatchResult, bool, error) {
	results, err := m.match(ctx, nil, nil)
	if err != nil {
		return nil, false, err
	}
	return results, true, nil
}

func (m *reteGraphBetaMemory) sourceGeneration() Generation {
	if m == nil {
		return 0
	}
	for _, fact := range m.facts {
		if !fact.ID().IsZero() {
			return fact.ID().Generation()
		}
	}
	return 0
}

func (m *reteGraphBetaMemory) factsForTarget(target conditionTarget) ([]FactSnapshot, bool) {
	if m == nil {
		return nil, false
	}
	switch target.kind {
	case conditionTargetName:
		return m.factsByName[target.name], true
	case conditionTargetTemplateKey:
		return m.factsByTemplate[target.templateKey], true
	default:
		return nil, false
	}
}

func (m *reteGraphBetaMemory) collectTerminalCandidates(ctx context.Context, rule compiledRule, terminal *reteGraphTerminalMemory) ([]matchCandidate, error) {
	if terminal == nil || terminal.rows.len() == 0 {
		return nil, nil
	}
	candidates := make([]matchCandidate, 0, terminal.rows.len())
	seen := newCandidateSeenSet(terminal.rows.len())
	for _, row := range terminal.rows.rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.token.isZero() {
			continue
		}
		candidate, err := buildMatchCandidateFromTokenRef(rule, row.token)
		if err != nil {
			return nil, err
		}
		if seen.seen(candidates, candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sortMatchCandidates(nil, candidates)
	return candidates, nil
}

func (m *reteGraphBetaMemory) terminalForRule(ruleRevisionID RuleRevisionID) *reteGraphTerminalMemory {
	if m == nil || m.graph == nil {
		return nil
	}
	for _, terminalNode := range m.graph.terminalNodes {
		if terminalNode.ruleRevisionID != ruleRevisionID {
			continue
		}
		return m.terminalAt(terminalNode.id)
	}
	return nil
}
