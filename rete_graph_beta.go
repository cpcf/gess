package gess

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

type reteGraphBetaMemory struct {
	revision               *Ruleset
	graph                  *reteGraph
	evalCtx                context.Context
	nodes                  []*reteGraphBetaNodeMemory
	aggregates             []*reteGraphAggregateNodeMemory
	terminals              []*reteGraphTerminalMemory
	terminalsByRule        map[RuleRevisionID][]*reteGraphTerminalMemory
	alphaFacts             []reteGraphAlphaFactSet
	alphaConditions        [][]ConditionID
	alphaFactCounts        map[ConditionID]int
	facts                  []FactSnapshot
	factIndexes            map[FactID]int
	factIndexReserve       int
	factsByName            map[string][]FactSnapshot
	factsByTemplate        map[TemplateKey][]FactSnapshot
	factNameIndexes        map[FactID]int
	factTemplateIndexes    map[FactID]int
	factFieldEqualIndexes  map[factFieldEqualKey][]FactSnapshot
	factTargetIndexesDirty bool
	factFieldIndexesDirty  bool
	arena                  *tokenArena
	queryArena             *tokenArena
	terminalTokenDeltas    []reteTerminalTokenDelta
	alphaRouteScratch      []reteGraphAlphaNodeID
	alphaRouteSeen         map[reteGraphAlphaNodeID]uint64
	alphaRouteEpoch        uint64
	removalTokenScratch    []tokenRef
	rootToken              tokenRef
	deferNegativeOutputs   bool
	suppressTerminalDeltas bool
	rightPredicateScratch  []conditionMatch
	tokenRefreshCache      map[tokenHandle]tokenRef
	modifyRouteScope       reteModifyRouteScope
}

type reteModifyRouteScope struct {
	stageQueue     []reteGraphStageRef
	stages         []reteGraphStageRef
	betaNodes      []reteGraphBetaNodeID
	aggregateNodes []reteGraphAggregateNodeID
	terminalNodes  []reteGraphTerminalNodeID
}

type reteGraphBetaNodeMemory struct {
	left  tokenHashMemory
	right tokenHashMemory
}

type reteGraphAggregateNodeMemory struct {
	buckets     map[graphTokenIdentityKey]*reteGraphAggregateBucket
	freeBuckets []*reteGraphAggregateBucket
}

type reteGraphAggregateBucket struct {
	parent          tokenRef
	members         map[graphTokenIdentityKey]reteGraphAggregateMember
	countOnlyFirst  tokenRef
	countOnlySecond tokenRef
	countOnlyRest   []tokenRef
	count           int64
	intSums         []int64
	floatSums       []float64
	floaty          []bool
	extrema         []reteGraphAggregateExtremum
	collects        [][]reteGraphAggregateCollectEntry
	token           tokenRef
	values          []Value
	hasValue        bool
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

type reteGraphBetaMemoryDiagnostics struct {
	BetaNodes                    []reteGraphBetaNodeMemoryDiagnostics
	Terminals                    []reteGraphTerminalMemoryDiagnostics
	MaxBetaRows                  int
	MaxBetaLeftRows              int
	MaxBetaRightRows             int
	MaxBetaJoinIndexKeys         int
	MaxBetaJoinBucketDepth       int
	MaxTerminalRows              int
	TotalTerminalBranchRows      int
	MaxTerminalBranchRows        int
	WidestRetainedBetaTokenWidth int
}

type reteGraphBetaNodeMemoryDiagnostics struct {
	ID                   reteGraphBetaNodeID
	Kind                 reteGraphBetaNodeKind
	Left                 reteGraphStageRef
	Right                reteGraphStageRef
	TokenWidth           int
	LeftRows             int
	RightRows            int
	TotalRows            int
	LeftJoinIndexKeys    int
	RightJoinIndexKeys   int
	TotalJoinIndexKeys   int
	LeftJoinBucketDepth  int
	RightJoinBucketDepth int
	MaxJoinBucketDepth   int
	LeftJoinBucketTotal  int
	RightJoinBucketTotal int
	TotalJoinBucketDepth int
	IdentityIndexKeys    int
	FactIndexKeys        int
}

type reteGraphTerminalMemoryDiagnostics struct {
	ID             reteGraphTerminalNodeID
	Kind           reteGraphTerminalKind
	RuleRevisionID RuleRevisionID
	QueryName      string
	Input          reteGraphStageRef
	TokenWidth     int
	Rows           int
	BranchRows     map[int]int
}

type reteGraphTokenMemoryDiagnostics struct {
	Rows                 int
	JoinIndexKeys        int
	JoinBucketDepthTotal int
	JoinBucketDepthMax   int
	IdentityIndexKeys    int
	FactIndexKeys        int
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
	factRowsDirty        bool
	bucketRestFree       [][]graphTokenRowID
}

type graphTokenRowID int

type graphTokenRow struct {
	id               graphTokenRowID
	token            tokenRef
	joinKey          betaJoinKey
	identity         graphTokenIdentityKey
	terminalIdentity candidateIdentity
	supportCount     int
	branchSupport    terminalBranchSupport
	branchOverflow   []terminalBranchSupport
	branchCount      int
}

// Negative beta rows reuse supportCount for their blocker count; terminal rows
// are distinguished by terminalIdentity and use supportCount for duplicate support.
func (r graphTokenRow) negativeBlockerCount() int {
	return r.supportCount
}

func (r graphTokenRow) isTerminal() bool {
	return r.terminalIdentity.count > 0
}

func (r *graphTokenRow) incrementNegativeBlockerCount() int {
	if r == nil {
		return 0
	}
	r.supportCount++
	return r.supportCount
}

func (r *graphTokenRow) decrementNegativeBlockerCount() int {
	if r == nil || r.supportCount <= 0 {
		return 0
	}
	r.supportCount--
	return r.supportCount
}

type terminalBranchSupport struct {
	branchID int
	count    int
}

func (r *graphTokenRow) addTerminalBranchSupport(branchID int) {
	if r == nil || branchID < 0 {
		return
	}
	if r.branchCount == 0 {
		r.branchSupport = terminalBranchSupport{branchID: branchID, count: 1}
		r.branchCount = 1
		return
	}
	if r.branchSupport.branchID == branchID {
		r.branchSupport.count++
		return
	}
	for i := 0; i < r.branchCount-1 && i < len(r.branchOverflow); i++ {
		if r.branchOverflow[i].branchID == branchID {
			r.branchOverflow[i].count++
			return
		}
	}
	r.branchOverflow = append(r.branchOverflow, terminalBranchSupport{branchID: branchID, count: 1})
	r.branchCount++
}

func (r graphTokenRow) hasTerminalBranchSupport(branchID int) bool {
	if branchID < 0 || r.branchCount == 0 {
		return false
	}
	if r.branchSupport.branchID == branchID && r.branchSupport.count > 0 {
		return true
	}
	for i := 0; i < r.branchCount-1 && i < len(r.branchOverflow); i++ {
		support := r.branchOverflow[i]
		if support.branchID == branchID && support.count > 0 {
			return true
		}
	}
	return false
}

func (r *graphTokenRow) removeTerminalBranchSupport(branchID int) {
	if r == nil || branchID < 0 || r.branchCount == 0 {
		return
	}
	if r.branchSupport.branchID == branchID {
		r.branchSupport.count--
		if r.branchSupport.count > 0 {
			return
		}
		r.removeTerminalBranchSupportAt(0)
		return
	}
	for i := 0; i < r.branchCount-1 && i < len(r.branchOverflow); i++ {
		if r.branchOverflow[i].branchID != branchID {
			continue
		}
		r.branchOverflow[i].count--
		if r.branchOverflow[i].count > 0 {
			return
		}
		r.removeTerminalBranchSupportAt(i + 1)
		return
	}
}

func (r *graphTokenRow) removeTerminalBranchSupportAt(index int) {
	if r == nil || index < 0 || index >= r.branchCount {
		return
	}
	overflowCount := min(r.branchCount-1, len(r.branchOverflow))
	if r.branchCount == 1 || overflowCount == 0 {
		r.branchSupport = terminalBranchSupport{}
		r.branchCount = 0
		r.branchOverflow = r.branchOverflow[:0]
		return
	}
	if index > overflowCount {
		return
	}
	lastOverflow := overflowCount - 1
	last := r.branchOverflow[lastOverflow]
	r.branchOverflow[lastOverflow] = terminalBranchSupport{}
	r.branchOverflow = r.branchOverflow[:lastOverflow]
	if index == 0 {
		r.branchSupport = last
	} else if index-1 < len(r.branchOverflow) {
		r.branchOverflow[index-1] = last
	}
	r.branchCount = overflowCount
}

func (r graphTokenRow) terminalBranchIDs() []int {
	if r.branchCount == 0 {
		return nil
	}
	out := make([]int, 0, r.branchCount)
	r.forEachTerminalBranchSupport(func(support terminalBranchSupport) {
		if support.count <= 0 {
			return
		}
		out = append(out, support.branchID)
	})
	return out
}

func (r graphTokenRow) forEachTerminalBranchSupport(fn func(terminalBranchSupport)) {
	if r.branchCount == 0 || fn == nil {
		return
	}
	fn(r.branchSupport)
	for i := 0; i < r.branchCount-1 && i < len(r.branchOverflow); i++ {
		fn(r.branchOverflow[i])
	}
}

type graphTokenIdentityKey struct {
	size          int
	generation    Generation
	identityState uint64
}

type graphTokenRowIDBucket struct {
	first  graphTokenRowID
	second graphTokenRowID
	rest   []graphTokenRowID
	count  int
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
	if index == 1 {
		return b.second, true
	}
	index -= 2
	if index >= len(b.rest) {
		return 0, false
	}
	return b.rest[index], true
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
		if last == 1 {
			b.first = b.second
			b.second = 0
			b.count--
			return true
		}
		restLast := last - 2
		b.first = b.rest[restLast]
		b.rest[restLast] = 0
		b.rest = b.rest[:restLast]
		b.count--
		return true
	}
	if b.count > 1 && b.second == id {
		last := b.count - 1
		if last == 1 {
			b.second = 0
			b.count--
			return true
		}
		restLast := last - 2
		b.second = b.rest[restLast]
		b.rest[restLast] = 0
		b.rest = b.rest[:restLast]
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
	if b.count > 1 && b.second == oldID {
		b.second = newID
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
	if b.count > 1 && !fn(b.second) {
		return
	}
	for i := 0; i < b.count-2 && i < len(b.rest); i++ {
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
	b.second = 0
	b.rest = b.rest[:0]
	b.count = 0
	return b
}

func newReteGraphBetaMemory(ctx context.Context, revision *Ruleset, graph *reteGraph, facts []FactSnapshot) (*reteGraphBetaMemory, error) {
	return newReteGraphBetaMemoryForGeneration(ctx, revision, graph, facts, reteGraphFactsGeneration(facts))
}

func newReteGraphBetaMemoryForGeneration(ctx context.Context, revision *Ruleset, graph *reteGraph, facts []FactSnapshot, generation Generation) (*reteGraphBetaMemory, error) {
	if revision == nil || graph == nil {
		return nil, nil
	}
	rowCapacity := graphBetaTokenMemoryCapacity(revision, len(facts))
	arenaCapacity := graphBetaTokenArenaCapacity(revision, len(facts))
	memory := &reteGraphBetaMemory{
		revision:            revision,
		graph:               graph,
		nodes:               make([]*reteGraphBetaNodeMemory, len(graph.betaNodes)+1),
		aggregates:          make([]*reteGraphAggregateNodeMemory, len(graph.aggregateNodes)+1),
		terminals:           make([]*reteGraphTerminalMemory, len(graph.terminalNodes)+1),
		arena:               newTokenArenaWithoutFactSpans(),
		terminalTokenDeltas: make([]reteTerminalTokenDelta, 0, revision.estimatedRunFactCapacity(len(facts))),
	}
	memory.arena.reserve(arenaCapacity)
	memory.reserveMemories(rowCapacity)
	memory.indexRuleTerminals()
	memory.reserveAlphaFacts(graphBetaAlphaFactCapacity(revision, graph, len(facts)))
	if err := memory.resetFactsForGeneration(ctx, facts, generation); err != nil {
		return nil, err
	}
	return memory, nil
}

func (m *reteGraphBetaMemory) pushEvalContext(ctx context.Context) func() {
	if m == nil {
		return func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	previous := m.evalCtx
	m.evalCtx = ctx
	return func() {
		m.evalCtx = previous
	}
}

func (m *reteGraphBetaMemory) context() context.Context {
	if m != nil && m.evalCtx != nil {
		return m.evalCtx
	}
	return context.Background()
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
	return revision.estimatedRunFactCapacity(initialFacts) * 8
}

func graphBetaAlphaFactCapacity(revision *Ruleset, graph *reteGraph, initialFacts int) int {
	if graph == nil || len(graph.alphaNodes) == 0 {
		return 0
	}
	capacity := max(1, initialFacts)
	if revision != nil {
		capacity = max(capacity, revision.estimatedRunFactCapacity(initialFacts))
	}
	perAlpha := (capacity + len(graph.alphaNodes) - 1) / len(graph.alphaNodes)
	return max(max(1, perAlpha), runFactReservePerRule*2)
}

func (m *reteGraphBetaMemory) reserveMemories(rowCapacity int) {
	if m == nil || m.graph == nil || rowCapacity <= 0 {
		return
	}
	for _, graphNode := range m.graph.betaNodes {
		node := m.nodeMemory(graphNode.id)
		nodeRowCapacity := rowCapacity
		if graphNode.rightHasLeftPrefix {
			nodeRowCapacity = max(8, rowCapacity/4)
		}
		node.left.reserveBeta(nodeRowCapacity, graphBetaFactIndexReserve(nodeRowCapacity, m.graph.stageTokenWidth(graphNode.left)))
		node.right.reserveBeta(nodeRowCapacity, graphBetaFactIndexReserve(nodeRowCapacity, m.graph.stageTokenWidth(graphNode.right)))
	}
	for _, terminalNode := range m.graph.terminalNodes {
		terminal := m.terminal(terminalNode.id)
		terminal.rows.reserveTerminal(rowCapacity, graphBetaFactIndexReserve(rowCapacity, m.graph.stageTokenWidth(terminalNode.input)))
	}
}

func (m *reteGraphBetaMemory) indexRuleTerminals() {
	if m == nil || m.graph == nil {
		return
	}
	for _, terminalNode := range m.graph.terminalNodes {
		if terminalNode.kind != reteGraphTerminalRule {
			continue
		}
		terminal := m.terminal(terminalNode.id)
		if terminal == nil {
			continue
		}
		if m.terminalsByRule == nil {
			m.terminalsByRule = make(map[RuleRevisionID][]*reteGraphTerminalMemory)
		}
		m.terminalsByRule[terminalNode.ruleRevisionID] = append(m.terminalsByRule[terminalNode.ruleRevisionID], terminal)
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

func (m *tokenHashMemory) ensureRowCapacity(rowCapacity int) {
	if m == nil || rowCapacity <= cap(m.rows) {
		return
	}
	nextCapacity := max(8, cap(m.rows)*2)
	for nextCapacity < rowCapacity {
		nextCapacity *= 2
	}
	rows := make([]graphTokenRow, len(m.rows), nextCapacity)
	copy(rows, m.rows)
	m.rows = rows
	m.rowReserve = max(m.rowReserve, nextCapacity)
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
	if factCapacity > 0 {
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
	m.recycleBucketRests(m.indexes)
	m.recycleIdentityBucketRests(m.identityRows)
	m.recycleFactBucketRests(m.factRows)
	clear(m.indexes)
	clear(m.identityRows)
	clear(m.factRows)
	m.factRowsDirty = false
}

func (m *tokenHashMemory) appendBucketRow(bucket *graphTokenRowIDBucket, id graphTokenRowID) {
	if m == nil || bucket == nil {
		return
	}
	if bucket.count == 0 {
		bucket.first = id
		bucket.count = 1
		return
	}
	if bucket.count == 1 {
		bucket.second = id
		bucket.count = 2
		return
	}
	if bucket.rest == nil {
		bucket.rest = m.takeBucketRest()
	}
	bucket.rest = append(bucket.rest, id)
	bucket.count++
}

func (m *tokenHashMemory) takeBucketRest() []graphTokenRowID {
	if m == nil || len(m.bucketRestFree) == 0 {
		return make([]graphTokenRowID, 0, 8)
	}
	last := len(m.bucketRestFree) - 1
	rest := m.bucketRestFree[last]
	m.bucketRestFree[last] = nil
	m.bucketRestFree = m.bucketRestFree[:last]
	return rest[:0]
}

func (m *tokenHashMemory) recycleBucketRests(buckets map[betaJoinKey]graphTokenRowIDBucket) {
	if m == nil || len(buckets) == 0 {
		return
	}
	for _, bucket := range buckets {
		m.recycleBucketRest(bucket.rest)
	}
}

func (m *tokenHashMemory) recycleIdentityBucketRests(buckets map[graphTokenIdentityKey]graphTokenRowIDBucket) {
	if m == nil || len(buckets) == 0 {
		return
	}
	for _, bucket := range buckets {
		m.recycleBucketRest(bucket.rest)
	}
}

func (m *tokenHashMemory) recycleFactBucketRests(buckets map[FactID]graphTokenRowIDBucket) {
	if m == nil || len(buckets) == 0 {
		return
	}
	for _, bucket := range buckets {
		m.recycleBucketRest(bucket.rest)
	}
}

func (m *tokenHashMemory) recycleBucketRest(rest []graphTokenRowID) {
	if m == nil || cap(rest) == 0 {
		return
	}
	clear(rest)
	m.bucketRestFree = append(m.bucketRestFree, rest[:0])
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
	return m.insertWithNegativeBlockerCount(token, joinKey, 0)
}

func (m *tokenHashMemory) insertWithNegativeBlockerCount(token tokenRef, joinKey betaJoinKey, negativeBlockerCount int) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
	}
	if m.indexes == nil {
		m.indexes = make(map[betaJoinKey]graphTokenRowIDBucket)
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
	m.ensureRowCapacity(int(rowID) + 1)
	m.rows = m.rows[:int(rowID)+1]
	m.rows[rowID] = graphTokenRow{
		id:           rowID,
		token:        token,
		joinKey:      joinKey,
		identity:     identity,
		supportCount: negativeBlockerCount,
	}
	joinBucket := m.indexes[joinKey]
	m.appendBucketRow(&joinBucket, rowID)
	m.indexes[joinKey] = joinBucket
	identityBucket := m.identityRows[identity]
	m.appendBucketRow(&identityBucket, rowID)
	m.identityRows[identity] = identityBucket
	m.markFactRowsDirty()
	return true
}

func (m *tokenHashMemory) insertTerminal(token tokenRef, terminalIdentity candidateIdentity, branchID int) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
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

	rowID := graphTokenRowID(len(m.rows))
	m.ensureRowCapacity(int(rowID) + 1)
	m.rows = m.rows[:int(rowID)+1]
	row := graphTokenRow{
		id:               rowID,
		token:            token,
		identity:         identity,
		terminalIdentity: terminalIdentity,
		supportCount:     1,
	}
	row.addTerminalBranchSupport(branchID)
	m.rows[rowID] = row
	identityBucket := m.identityRows[identity]
	m.appendBucketRow(&identityBucket, rowID)
	m.identityRows[identity] = identityBucket
	m.markFactRowsDirty()
	return true
}

func (m *tokenHashMemory) containsExactToken(token tokenRef) bool {
	if m == nil || token.isZero() || len(m.identityRows) == 0 {
		return false
	}
	if _, ok := token.resolve(); !ok {
		return false
	}
	bucket, ok := m.identityRows[tokenRefKey(token)]
	if !ok || bucket.len() == 0 {
		return false
	}
	for i := 0; i < bucket.len(); i++ {
		rowID, ok := bucket.at(i)
		if !ok {
			continue
		}
		row := m.row(rowID)
		if row != nil && row.token.handle == token.handle {
			return true
		}
	}
	return false
}

func (m *tokenHashMemory) refreshTerminalTokensContainingFact(id FactID, updates []reteTerminalTokenUpdate, collectUpdates bool, refresh func(graphTokenRow) (tokenRef, bool)) []reteTerminalTokenUpdate {
	if m == nil || id.IsZero() || refresh == nil {
		return updates
	}
	m.ensureFactRows()
	bucket, ok := m.factRows[id]
	if !ok || bucket.len() == 0 {
		return updates
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
		row := m.row(rowID)
		if row == nil || row.token.isZero() || !row.isTerminal() || !row.token.containsFact(id) {
			continue
		}
		next, ok := refresh(*row)
		if !ok || next.isZero() {
			continue
		}
		before := row.token
		identity := row.terminalIdentity
		m.replaceRowToken(rowID, next)
		if collectUpdates {
			updates = append(updates, reteTerminalTokenUpdate{
				before:   before,
				after:    next,
				identity: identity,
			})
		}
	}
	return updates
}

func (m *tokenHashMemory) refreshTokensContainingFact(id FactID, refresh func(graphTokenRow) (tokenRef, bool)) bool {
	if m == nil || id.IsZero() || refresh == nil {
		return true
	}
	m.ensureFactRows()
	bucket, ok := m.factRows[id]
	if !ok || bucket.len() == 0 {
		return true
	}
	rowIDs := make([]graphTokenRowID, 0, bucket.len())
	var previous graphTokenRowID
	havePrevious := false
	for i := 0; i < bucket.len(); i++ {
		rowID, ok := bucket.at(i)
		if !ok || (havePrevious && rowID == previous) {
			continue
		}
		havePrevious = true
		previous = rowID
		rowIDs = append(rowIDs, rowID)
	}
	for _, rowID := range rowIDs {
		row := m.row(rowID)
		if row == nil || row.token.isZero() || !row.token.containsFact(id) {
			continue
		}
		next, ok := refresh(*row)
		if !ok || next.isZero() {
			return false
		}
		m.replaceRowToken(rowID, next)
	}
	return true
}

func (m *tokenHashMemory) replaceRowToken(rowID graphTokenRowID, token tokenRef) {
	if m == nil || rowID < 0 || token.isZero() {
		return
	}
	row := m.row(rowID)
	if row == nil || row.token.isZero() {
		return
	}
	nextIdentity := tokenRefKey(token)
	if row.identity != nextIdentity {
		if bucket, ok := m.identityRows[row.identity]; ok {
			if bucket.remove(rowID) {
				if bucket.len() == 0 {
					m.recycleBucketRest(bucket.rest)
					delete(m.identityRows, row.identity)
				} else {
					m.identityRows[row.identity] = bucket
				}
			}
		}
		if m.identityRows == nil {
			m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
		}
		bucket := m.identityRows[nextIdentity]
		m.appendBucketRow(&bucket, rowID)
		m.identityRows[nextIdentity] = bucket
		row.identity = nextIdentity
	}
	row.token = token
	m.markFactRowsDirty()
}

func (m *tokenHashMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	m.ensureFactRows()
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
	m.ensureFactRows()
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
		if row.isTerminal() && row.supportCount > 1 {
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
	m.ensureFactRows()
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
				m.recycleBucketRest(bucket.rest)
				delete(m.indexes, removed.joinKey)
			} else {
				m.indexes[removed.joinKey] = bucket
			}
		}
	}
	if bucket, ok := m.identityRows[removed.identity]; ok {
		if bucket.remove(rowID) {
			if bucket.len() == 0 {
				m.recycleBucketRest(bucket.rest)
				delete(m.identityRows, removed.identity)
			} else {
				m.identityRows[removed.identity] = bucket
			}
		}
	}
	if !m.factRowsDirty {
		m.removeTokenFacts(removed.token, rowID)
	}
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
		if !m.factRowsDirty {
			m.replaceTokenFactRows(moved.token, graphTokenRowID(last), rowID)
		}
	}
	m.rows[last] = graphTokenRow{}
	m.rows = m.rows[:last]
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m *tokenHashMemory) markFactRowsDirty() {
	if m == nil {
		return
	}
	m.factRowsDirty = true
}

func (m *tokenHashMemory) ensureFactRows() {
	if m == nil || !m.factRowsDirty {
		return
	}
	m.rebuildFactRows()
}

func (m *tokenHashMemory) rebuildFactRows() {
	if m == nil {
		return
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket, max(m.factIndexReserve, len(m.rows)))
	} else {
		clear(m.factRows)
	}
	for index, row := range m.rows {
		if row.token.isZero() {
			continue
		}
		m.indexTokenFacts(row.token, graphTokenRowID(index))
	}
	m.factRowsDirty = false
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
			m.appendBucketRow(&bucket, rowID)
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
		m.appendBucketRow(&bucket, rowID)
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
				m.recycleBucketRest(bucket.rest)
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
			m.recycleBucketRest(bucket.rest)
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

func (m *reteGraphBetaMemory) resetFacts(ctx context.Context, facts []FactSnapshot) error {
	return m.resetFactsForGeneration(ctx, facts, reteGraphFactsGeneration(facts))
}

func (m *reteGraphBetaMemory) resetFactsForGeneration(ctx context.Context, facts []FactSnapshot, generation Generation) error {
	if m == nil || m.graph == nil {
		return nil
	}
	if len(m.alphaFacts) != len(m.graph.alphaNodes)+1 || len(m.alphaConditions) != len(m.graph.alphaNodes)+1 {
		m.reserveAlphaFacts(graphBetaAlphaFactCapacity(m.revision, m.graph, len(facts)))
	}
	if m.arena == nil {
		m.arena = newTokenArenaWithoutFactSpans()
	} else {
		m.arena.reset()
	}
	if _, err := m.propagateEvent(ctx, newReteGraphClearEvent(generation, mutationOrigin{}, nil)); err != nil {
		return err
	}
	m.setFacts(facts)
	m.deferNegativeOutputs = true
	m.suppressTerminalDeltas = true
	m.initializeAggregateOutputs()
	if err := m.initializeRootStage(nil); err != nil {
		m.deferNegativeOutputs = false
		m.suppressTerminalDeltas = false
		return err
	}
	for _, fact := range facts {
		if _, err := m.insertFactInternal(ctx, fact, nil, false); err != nil {
			m.deferNegativeOutputs = false
			m.suppressTerminalDeltas = false
			return err
		}
	}
	if err := m.finalizeDeferredNegativeOutputs(nil); err != nil {
		m.deferNegativeOutputs = false
		m.suppressTerminalDeltas = false
		return err
	}
	m.deferNegativeOutputs = false
	m.suppressTerminalDeltas = false
	return nil
}

func (m *reteGraphBetaMemory) initializeRootStage(span *propagationCounterSpan) error {
	if m == nil || m.graph == nil {
		return nil
	}
	root := reteGraphStageRef{kind: reteGraphStageRoot}
	if len(m.graph.successorsByStage[root]) == 0 && len(m.graph.terminalsByStage[root]) == 0 && len(m.graph.aggregatesByStage[root]) == 0 && len(m.graph.aggregateOuters[root]) == 0 {
		m.rootToken = tokenRef{}
		return nil
	}
	m.rootToken = m.newRootTokenRef(m.aggregateGeneration(), span)
	if m.rootToken.isZero() {
		return nil
	}
	delta := &reteAgendaDelta{supported: true}
	return m.propagateFromStage(root, m.rootToken, span, delta)
}

func (m *reteGraphBetaMemory) setFacts(facts []FactSnapshot) {
	if m == nil {
		return
	}
	capacity := len(facts)
	if m.revision != nil {
		capacity = max(capacity, m.revision.estimatedRunFactCapacity(len(facts)))
	}
	if cap(m.facts) < capacity {
		m.facts = make([]FactSnapshot, len(facts), capacity)
	} else {
		clear(m.facts)
		m.facts = m.facts[:len(facts)]
	}
	copy(m.facts, facts)
	if m.factIndexes == nil || capacity > m.factIndexReserve {
		m.factIndexes = make(map[FactID]int, capacity)
		m.factIndexReserve = capacity
	} else {
		clear(m.factIndexes)
	}
	for i, fact := range m.facts {
		m.factIndexes[fact.ID()] = i
	}
	m.markFactTargetIndexesDirty()
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
	if m.factFieldEqualIndexes == nil {
		m.factFieldEqualIndexes = make(map[factFieldEqualKey][]FactSnapshot)
	} else {
		clear(m.factFieldEqualIndexes)
	}
	m.initializeFactFieldEqualIndexKeys()
	for _, fact := range m.facts {
		m.addFactTargetIndexes(fact)
	}
	m.factTargetIndexesDirty = false
	m.factFieldIndexesDirty = false
}

func (m *reteGraphBetaMemory) initializeFactFieldEqualIndexKeys() {
	if m == nil || m.graph == nil || m.factFieldEqualIndexes == nil {
		return
	}
	for templateKey, table := range m.graph.alphaRouteTables {
		if table == nil || len(table.indexed) == 0 {
			continue
		}
		target := conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey}
		for routeKey := range table.indexed {
			key := newFactFieldEqualKey(target, routeKey.fieldSlot, routeKey.value)
			if _, ok := m.factFieldEqualIndexes[key]; !ok {
				m.factFieldEqualIndexes[key] = nil
			}
		}
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
	m.rootToken = tokenRef{}
}

func (m *reteGraphBetaMemory) initializeAggregateOutputs() {
	if m == nil || m.graph == nil {
		return
	}
	delta := reteAgendaDelta{supported: true}
	for _, node := range m.graph.aggregateNodes {
		if node.outer.kind != reteGraphStageUnknown {
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
			m.freeBuckets = append(m.freeBuckets, bucket)
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
	m.countOnlyFirst = tokenRef{}
	m.countOnlySecond = tokenRef{}
	clear(m.countOnlyRest)
	m.countOnlyRest = m.countOnlyRest[:0]
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

func (m *reteGraphBetaMemory) propagateEvent(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, error) {
	switch event.tag {
	case reteGraphPropagationAdd:
		if event.workingFact != nil {
			return m.insertFactGenerated(ctx, event.workingFact, event.span)
		}
		return m.insertFact(ctx, event.fact, event.span)
	case reteGraphPropagationRemove:
		return m.removeFact(ctx, event.fact, event.counters)
	case reteGraphPropagationUpdate:
		return m.updateFact(ctx, event)
	case reteGraphPropagationClear:
		m.clearMemories()
		return reteAgendaDelta{supported: true}, nil
	case reteGraphPropagationModifyAdd, reteGraphPropagationModifyRemove:
		return reteAgendaDelta{}, ErrUnsupportedRuntime
	default:
		return reteAgendaDelta{}, ErrUnsupportedRuntime
	}
}

func (m *reteGraphBetaMemory) insertFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, error) {
	return m.insertFactInternal(ctx, fact, span, true)
}

func (m *reteGraphBetaMemory) insertFactInternal(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan, updateSource bool) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	if updateSource {
		m.upsertFactSource(fact)
	}
	routeIDs := m.snapshotAlphaRouteIDsForFactInsert(fact, span)
	if len(routeIDs) == 0 {
		return reteAgendaDelta{supported: true}, nil
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
		ok, err := node.matchesSnapshotWithContextAndCounters(ctx, fact, span)
		if err != nil {
			return delta, err
		}
		if !ok {
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
		m.recordAlphaFact(nodeID, match.fact)
		ok, err = m.insertAlphaMatch(nodeID, match, span, &delta)
		if err != nil {
			return delta, err
		}
		if !ok {
			delta.supported = false
		}
	}
	return m.finishTerminalTokenDelta(delta), nil
}

func (m *reteGraphBetaMemory) upsertFactSource(fact FactSnapshot) {
	if m == nil || fact.ID().IsZero() {
		return
	}
	if m.factIndexes == nil {
		m.factIndexes = make(map[FactID]int)
	}
	if index, ok := m.factIndexes[fact.ID()]; ok && index >= 0 && index < len(m.facts) {
		m.facts[index] = fact
		m.markFactTargetIndexesDirty()
		return
	}
	m.factIndexes[fact.ID()] = len(m.facts)
	m.facts = append(m.facts, fact)
	m.markFactTargetIndexesDirty()
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
	m.markFactTargetIndexesDirty()
}

func (m *reteGraphBetaMemory) markFactTargetIndexesDirty() {
	if m == nil {
		return
	}
	m.factTargetIndexesDirty = true
	m.factFieldIndexesDirty = true
	m.clearFactFieldEqualIndexes()
}

func (m *reteGraphBetaMemory) clearFactFieldEqualIndexes() {
	if m == nil || len(m.factFieldEqualIndexes) == 0 {
		return
	}
	clear(m.factFieldEqualIndexes)
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
	m.addFactFieldEqualIndexes(fact)
}

func (m *reteGraphBetaMemory) addFactFieldEqualIndexes(fact FactSnapshot) {
	if m == nil || m.graph == nil || fact.ID().IsZero() || fact.TemplateKey() == "" || m.factFieldEqualIndexes == nil {
		return
	}
	table := m.graph.alphaRouteTables[fact.TemplateKey()]
	if table == nil || len(table.indexed) == 0 {
		return
	}
	target := conditionTarget{kind: conditionTargetTemplateKey, templateKey: fact.TemplateKey()}
	for routeKey := range table.indexed {
		if !factSnapshotMatchesFieldEqualIndex(fact, routeKey.fieldSlot, routeKey.value) {
			continue
		}
		key := newFactFieldEqualKey(target, routeKey.fieldSlot, routeKey.value)
		m.factFieldEqualIndexes[key] = append(m.factFieldEqualIndexes[key], fact)
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

func (m *reteGraphBetaMemory) insertFactGenerated(ctx context.Context, fact *workingFact, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	routeIDs := m.workingAlphaRouteIDsForFact(fact, span)
	if len(routeIDs) == 0 {
		return reteAgendaDelta{supported: true}, nil
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
		ok, err := node.matchesWorkingWithContextAndCounters(ctx, fact, span)
		if err != nil {
			return delta, err
		}
		if !ok {
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
		ok, err = m.insertAlphaMatchGenerated(nodeID, match, span, &delta)
		if err != nil {
			return delta, err
		}
		if !ok {
			delta.supported = false
		}
	}
	return m.finishTerminalTokenDelta(delta), nil
}

func (m *reteGraphBetaMemory) snapshotAlphaRouteIDsForFact(fact FactSnapshot) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil {
		return nil
	}
	templateKey := fact.TemplateKey()
	templateIDs := m.graph.routesByTemplateKey[templateKey]
	if len(templateIDs) > 3 {
		templateIDs = m.snapshotAlphaRouteIDs(templateKey, templateIDs, fact, nil)
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

func (m *reteGraphBetaMemory) snapshotAlphaRouteIDsForFactInsert(fact FactSnapshot, span *propagationCounterSpan) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil {
		return nil
	}
	templateKey := fact.TemplateKey()
	templateIDs := m.graph.routesByTemplateKey[templateKey]
	if len(templateIDs) > 3 || (len(templateIDs) == 1 && m.canUseSingleIndexedAlphaRoute(templateKey)) {
		templateIDs = m.snapshotAlphaRouteIDs(templateKey, templateIDs, fact, span)
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

func (m *reteGraphBetaMemory) workingAlphaRouteIDsForFact(fact *workingFact, span *propagationCounterSpan) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil || fact == nil {
		return nil
	}
	templateIDs := m.graph.routesByTemplateKey[fact.templateKey]
	if len(templateIDs) > 3 {
		templateIDs = m.workingAlphaRouteIDs(fact.templateKey, templateIDs, fact, span)
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

func (m *reteGraphBetaMemory) canUseSingleIndexedAlphaRoute(templateKey TemplateKey) bool {
	if m == nil || m.graph == nil {
		return false
	}
	table := m.graph.alphaRouteTables[templateKey]
	if table == nil || len(table.indexed) != 1 {
		return false
	}
	_, ok := table.singleIndexedField()
	return ok
}

func (m *reteGraphBetaMemory) snapshotAlphaRouteIDs(templateKey TemplateKey, nodeIDs []reteGraphAlphaNodeID, fact FactSnapshot, span *propagationCounterSpan) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil {
		return nil
	}
	table := m.graph.alphaRouteTables[templateKey]
	if table == nil || len(table.indexed) == 0 {
		if span != nil {
			span.recordAlphaIndexFallbackScan()
		}
		return nodeIDs
	}
	if fieldSlot, ok := table.singleIndexedField(); ok {
		value, valueOK := m.snapshotAlphaRouteFieldValue(templateKey, fact, fieldSlot)
		if !valueOK {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			return nil
		}
		routeValue, routeOK := reteGraphAlphaRouteValueFromValue(value)
		if !routeOK {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			return nil
		}
		bucket := table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}]
		if span != nil {
			span.recordAlphaIndexProbe(len(bucket) > 0)
		}
		return bucket
	}
	m.resetAlphaRouteScratch()
	for _, id := range table.unindexed {
		m.appendAlphaRouteCandidate(id)
	}
	if span != nil && len(table.unindexed) > 0 {
		span.recordAlphaIndexFallbackScan()
	}
	for _, fieldSlot := range table.indexedFields {
		value, ok := m.snapshotAlphaRouteFieldValue(templateKey, fact, fieldSlot)
		if !ok {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			continue
		}
		routeValue, ok := reteGraphAlphaRouteValueFromValue(value)
		if !ok {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			continue
		}
		bucket := table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}]
		if span != nil {
			span.recordAlphaIndexProbe(len(bucket) > 0)
		}
		m.appendAlphaRouteBucket(bucket)
	}
	m.sortAlphaRouteScratch()
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) workingAlphaRouteIDs(templateKey TemplateKey, nodeIDs []reteGraphAlphaNodeID, fact *workingFact, span *propagationCounterSpan) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil || fact == nil {
		return nil
	}
	table := m.graph.alphaRouteTables[templateKey]
	if table == nil || len(table.indexed) == 0 {
		if span != nil {
			span.recordAlphaIndexFallbackScan()
		}
		return nodeIDs
	}
	if fieldSlot, ok := table.singleIndexedField(); ok {
		value, valueOK := m.workingAlphaRouteFieldValue(templateKey, fact, fieldSlot)
		if !valueOK {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			return nil
		}
		routeValue, routeOK := reteGraphAlphaRouteValueFromValue(value)
		if !routeOK {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			return nil
		}
		bucket := table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}]
		if span != nil {
			span.recordAlphaIndexProbe(len(bucket) > 0)
		}
		return bucket
	}
	m.resetAlphaRouteScratch()
	for _, id := range table.unindexed {
		m.appendAlphaRouteCandidate(id)
	}
	if span != nil && len(table.unindexed) > 0 {
		span.recordAlphaIndexFallbackScan()
	}
	for _, fieldSlot := range table.indexedFields {
		value, ok := m.workingAlphaRouteFieldValue(templateKey, fact, fieldSlot)
		if !ok {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			continue
		}
		routeValue, ok := reteGraphAlphaRouteValueFromValue(value)
		if !ok {
			if span != nil {
				span.recordAlphaIndexProbe(false)
			}
			continue
		}
		bucket := table.indexed[reteGraphAlphaRouteKey{
			fieldSlot: fieldSlot,
			value:     routeValue,
		}]
		if span != nil {
			span.recordAlphaIndexProbe(len(bucket) > 0)
		}
		m.appendAlphaRouteBucket(bucket)
	}
	m.sortAlphaRouteScratch()
	return m.alphaRouteScratch
}

func (m *reteGraphBetaMemory) snapshotAlphaRouteFieldValue(templateKey TemplateKey, fact FactSnapshot, fieldSlot int) (Value, bool) {
	field := m.alphaRouteFieldName(templateKey, fieldSlot)
	return fact.compiledFieldValue(field, fieldSlot)
}

func (m *reteGraphBetaMemory) workingAlphaRouteFieldValue(templateKey TemplateKey, fact *workingFact, fieldSlot int) (Value, bool) {
	field := m.alphaRouteFieldName(templateKey, fieldSlot)
	return fact.compiledFieldValue(field, fieldSlot)
}

func (m *reteGraphBetaMemory) alphaRouteFieldName(templateKey TemplateKey, fieldSlot int) string {
	if m == nil || m.revision == nil || fieldSlot < 0 {
		return ""
	}
	template, ok := m.revision.templateByKey(templateKey)
	if !ok || fieldSlot >= len(template.fields) {
		return ""
	}
	return template.fields[fieldSlot].Name
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

func (m *reteGraphBetaMemory) resetRemovalTokens() []tokenRef {
	if m == nil {
		return nil
	}
	m.removalTokenScratch = m.removalTokenScratch[:0]
	return m.removalTokenScratch
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

func (m *reteGraphBetaMemory) insertAlphaMatch(nodeID reteGraphAlphaNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil {
		return false, nil
	}
	node := m.graph.alphaNode(nodeID)
	if node == nil {
		return false, nil
	}
	if err := m.propagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, span, delta); err != nil {
		return false, err
	}
	return true, nil
}

func (m *reteGraphBetaMemory) insertAlphaMatchGenerated(nodeID reteGraphAlphaNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil {
		return false, nil
	}
	node := m.graph.alphaNode(nodeID)
	if node == nil {
		return false, nil
	}
	if err := m.propagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, span, delta); err != nil {
		return false, err
	}
	return true, nil
}

func (m *reteGraphBetaMemory) propagateAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) error {
	if m == nil || delta == nil {
		return nil
	}
	alphaNodeID := reteGraphAlphaNodeID(0)
	if source.kind == reteGraphStageAlpha {
		alphaNodeID = reteGraphAlphaNodeID(source.id)
	}
	captures, capturesOK := m.alphaListPatternCaptures(source, match)
	if !capturesOK {
		delta.supported = false
		return nil
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
		token := m.newAlphaTokenRef(entry, match, captures, span)
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
			token := m.newAlphaTokenRef(entry, match, captures, span)
			if token.isZero() {
				delta.supported = false
				continue
			}
			ok, err := m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta)
			if err != nil {
				return err
			}
			if !ok {
				delta.supported = false
			}
		case reteGraphBetaInputRight:
			m.recordAlphaFact(alphaNodeID, match.fact)
			edgeMatch := conditionMatch{
				conditionID: successor.entry.conditionID,
				bindingSlot: successor.entry.bindingSlot,
				fact:        match.fact,
			}
			token := m.newAlphaTokenRef(successor.entry, edgeMatch, captures, span)
			if token.isZero() {
				delta.supported = false
				continue
			}
			ok, err := m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta)
			if err != nil {
				return err
			}
			if !ok {
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
		token := m.newAlphaTokenRef(entry, match, captures, span)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.openAggregateBucket(aggregateID, token, span, delta)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		m.recordAlphaFact(alphaNodeID, match.fact)
		m.insertAggregateInput(aggregateID, match, span, delta)
	}
	return nil
}

func (m *reteGraphBetaMemory) propagateRemoveAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	if counters != nil {
		counters.recordNegativePropagationEvent()
	}
	captures, capturesOK := m.alphaListPatternCaptures(source, match)
	if !capturesOK {
		delta.supported = false
		return
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
		m.removeTerminalTokenContainingFact(terminal.terminalID, terminal.branchID, match.fact.ID(), counters, delta)
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
			token := m.newAlphaTokenRef(entry, match, captures, nil)
			if token.isZero() || !m.removeBetaInputToken(successor.betaNodeID, successor.side, token, counters, delta) {
				delta.supported = false
			}
		case reteGraphBetaInputRight:
			edgeMatch := conditionMatch{
				conditionID: successor.entry.conditionID,
				bindingSlot: successor.entry.bindingSlot,
				fact:        match.fact,
			}
			token := m.newAlphaTokenRef(successor.entry, edgeMatch, captures, nil)
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
		token := m.newAlphaTokenRef(entry, match, captures, nil)
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

func (m *reteGraphBetaMemory) alphaListPatternCaptures(source reteGraphStageRef, match conditionMatch) ([]listPatternCapture, bool) {
	if m == nil || m.graph == nil || source.kind != reteGraphStageAlpha {
		return nil, true
	}
	node := m.graph.alphaNode(reteGraphAlphaNodeID(source.id))
	if node == nil {
		return nil, false
	}
	return node.listPatternCaptures(match.fact, tokenRef{})
}

func (m *reteGraphBetaMemory) newAlphaTokenRef(entry bindingTupleEntry, match conditionMatch, captures []listPatternCapture, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	token := m.newTokenRef(tokenRef{}, entry, conditionMatchForEntry(match, entry), match.fact.Recency(), match.fact.Generation(), span)
	if token.isZero() {
		return tokenRef{}
	}
	for _, capture := range captures {
		captureEntry := bindingTupleEntry{
			binding:        capture.binding,
			bindingSlot:    capture.bindingSlot,
			conditionOrder: capture.bindingSlot,
			conditionID:    entry.conditionID,
			conditionPath:  cloneIntPath(entry.conditionPath),
		}
		captureMatch := conditionMatch{
			conditionID: entry.conditionID,
			bindingSlot: capture.bindingSlot,
			value:       cloneValue(capture.value),
			hasValue:    true,
		}
		token = m.newTokenRef(token, captureEntry, captureMatch, 0, match.fact.Generation(), span)
		if token.isZero() {
			return tokenRef{}
		}
	}
	return token
}

func (m *reteGraphBetaMemory) newAlphaTokenRefWithRetainedCaptures(entry bindingTupleEntry, match conditionMatch, retained tokenRef, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	token := m.newTokenRef(tokenRef{}, entry, conditionMatchForEntry(match, entry), match.fact.Recency(), match.fact.Generation(), span)
	if token.isZero() {
		return tokenRef{}
	}
	var retainedRefs [4]tokenRef
	var retainedCount int
	var overflow []tokenRef
	for current := retained; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return tokenRef{}
		}
		if !row.match.hasValue || row.match.conditionID != entry.conditionID {
			continue
		}
		if len(overflow) != 0 {
			overflow = append(overflow, current)
			continue
		}
		if retainedCount < len(retainedRefs) {
			retainedRefs[retainedCount] = current
			retainedCount++
			continue
		}
		overflow = append(overflow, retainedRefs[:]...)
		overflow = append(overflow, current)
	}
	appendRetained := func(ref tokenRef) bool {
		row, ok := ref.resolve()
		if !ok {
			return false
		}
		token = m.newTokenRef(token, row.entry, row.match, 0, match.fact.Generation(), span)
		return !token.isZero()
	}
	if len(overflow) != 0 {
		for i := len(overflow) - 1; i >= 0; i-- {
			if !appendRetained(overflow[i]) {
				return tokenRef{}
			}
		}
		return token
	}
	for i := retainedCount - 1; i >= 0; i-- {
		if !appendRetained(retainedRefs[i]) {
			return tokenRef{}
		}
	}
	return token
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
	if node == nil || memory == nil {
		delta.supported = false
		return
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		bucket := memory.bucketForParent(m.aggregateParentToken(node, token))
		if bucket == nil {
			delta.supported = false
			return
		}
		if bucket.addCountOnlyMember(token) {
			m.refreshAggregateOutputInternal(id, bucket, span, nil, delta)
		}
		return
	}
	match, ok := tokenFactMatchForBindingSlot(token, node.inputEntry.bindingSlot)
	if !ok {
		delta.supported = false
		return
	}
	bucket := memory.bucketForParent(m.aggregateParentToken(node, token))
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
	bucket, ok := memory.bucketForParentIfExists(m.aggregateParentToken(node, token))
	if !ok {
		return
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		if bucket.removeCountOnlyMember(token) {
			m.refreshAggregateOutputInternal(id, bucket, nil, counters, delta)
		}
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
	delete(memory.buckets, tokenRefKey(parent))
	memory.recycleBucket(bucket)
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
		delete(memory.buckets, key)
		memory.recycleBucket(bucket)
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
		if bucket == nil {
			continue
		}
		changed := false
		if !aggregateSpecsNeedInputValues(node.specs) {
			kept := 0
			count := bucket.countOnlyMemberCount()
			for i := range count {
				token := bucket.countOnlyMemberAt(i)
				if !token.containsFact(factID) {
					bucket.setCountOnlyMemberAt(kept, token)
					kept++
					continue
				}
				changed = true
			}
			bucket.truncateCountOnlyMembers(kept)
		} else if len(bucket.members) > 0 {
			for key, member := range bucket.members {
				if !member.token.containsFact(factID) {
					continue
				}
				delete(bucket.members, key)
				bucket.removeMember(node, member)
				changed = true
			}
		}
		if changed {
			m.refreshAggregateOutputInternal(id, bucket, nil, counters, delta)
		}
	}
}

func (m *reteGraphBetaMemory) refreshAggregateMembersContainingFact(id reteGraphAggregateNodeID, factID FactID, after conditionFactRef, cache map[tokenHandle]tokenRef, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || factID.IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return false
	}
	node := m.graph.aggregateNode(id)
	memory := m.aggregateMemory(id)
	if node == nil || memory == nil || len(memory.buckets) == 0 {
		return true
	}
	for _, bucket := range memory.buckets {
		if bucket == nil {
			continue
		}
		changed := false
		if !aggregateSpecsNeedInputValues(node.specs) {
			count := bucket.countOnlyMemberCount()
			for i := range count {
				token := bucket.countOnlyMemberAt(i)
				if token.isZero() || !token.containsFact(factID) {
					continue
				}
				next, ok := m.refreshTokenFactRefInPlaceCached(token, factID, after, cache)
				if !ok || next.isZero() {
					delta.supported = false
					return false
				}
				bucket.setCountOnlyMemberAt(i, next)
				changed = true
			}
		} else if len(bucket.members) > 0 {
			updates := make([]reteGraphAggregateMember, 0, 1)
			for _, member := range bucket.members {
				if !member.token.containsFact(factID) {
					continue
				}
				updates = append(updates, member)
			}
			for _, member := range updates {
				oldKey := tokenRefKey(member.token)
				next, ok := m.refreshTokenFactRefInPlaceCached(member.token, factID, after, cache)
				if !ok || next.isZero() {
					delta.supported = false
					return false
				}
				nextMatch, ok := tokenFactMatchForBindingSlot(next, node.inputEntry.bindingSlot)
				if !ok {
					delta.supported = false
					return false
				}
				delete(bucket.members, oldKey)
				bucket.removeMemberWithCollectKey(node, member, oldKey)
				member.token = next
				member.match = nextMatch
				if bucket.members == nil {
					bucket.members = make(map[graphTokenIdentityKey]reteGraphAggregateMember)
				}
				bucket.members[tokenRefKey(next)] = member
				if err := bucket.addMember(node, member); err != nil {
					delta.supported = false
					return false
				}
				changed = true
			}
		}
		if changed {
			m.refreshAggregateOutputInternal(id, bucket, nil, nil, delta)
		}
	}
	return delta.supported
}

func (m *reteGraphBetaMemory) refreshAggregateParentsContainingFact(id reteGraphAggregateNodeID, factID FactID, after conditionFactRef, cache map[tokenHandle]tokenRef, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || factID.IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return false
	}
	memory := m.aggregateMemory(id)
	if memory == nil || len(memory.buckets) == 0 {
		return true
	}
	type aggregateParentRefresh struct {
		oldKey graphTokenIdentityKey
		bucket *reteGraphAggregateBucket
	}
	updates := make([]aggregateParentRefresh, 0, 1)
	for key, bucket := range memory.buckets {
		if bucket == nil || !bucket.parent.containsFact(factID) {
			continue
		}
		updates = append(updates, aggregateParentRefresh{oldKey: key, bucket: bucket})
	}
	for _, update := range updates {
		bucket := update.bucket
		nextParent, ok := m.refreshTokenFactRefInPlaceCached(bucket.parent, factID, after, cache)
		if !ok || nextParent.isZero() {
			delta.supported = false
			return false
		}
		nextKey := tokenRefKey(nextParent)
		if update.oldKey != nextKey {
			if existing := memory.buckets[nextKey]; existing != nil && existing != bucket {
				delta.supported = false
				return false
			}
			delete(memory.buckets, update.oldKey)
			memory.buckets[nextKey] = bucket
		}
		bucket.parent = nextParent
		if bucket.token.isZero() {
			continue
		}
		nextToken, ok := m.refreshTokenFactRefInPlaceCached(bucket.token, factID, after, cache)
		if !ok || nextToken.isZero() {
			delta.supported = false
			return false
		}
		bucket.token = nextToken
	}
	return delta.supported
}

func (m *reteGraphBetaMemory) aggregateMember(node *reteGraphAggregateNode, token tokenRef, match conditionMatch) (reteGraphAggregateMember, bool) {
	if node == nil {
		return reteGraphAggregateMember{}, false
	}
	member := reteGraphAggregateMember{match: match, token: token}
	if !aggregateSpecsNeedInputValues(node.specs) {
		return member, true
	}
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

func aggregateSpecsNeedInputValues(specs []compiledAggregateSpec) bool {
	for _, spec := range specs {
		switch spec.kind {
		case AggregateCount:
			continue
		default:
			return true
		}
	}
	return false
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

func (m *reteGraphBetaMemory) aggregateParentToken(node *reteGraphAggregateNode, token tokenRef) tokenRef {
	if m == nil || node == nil || token.isZero() || node.outer.kind == reteGraphStageUnknown {
		return tokenRef{}
	}
	width := m.graph.stageTokenWidth(node.outer)
	if width <= 0 {
		return tokenRef{}
	}
	for !token.isZero() && token.size() > width {
		token = token.parent()
	}
	if token.size() == width {
		return token
	}
	return tokenRef{}
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
	values, ok := bucket.results(node)
	if ok && len(values) == len(node.entries) && bucket.hasValue && aggregateValuesEqual(bucket.values, values) {
		return
	}
	if !bucket.token.isZero() {
		m.propagateRemoveFromStage(stage, bucket.token, counters, delta)
		bucket.token = tokenRef{}
		bucket.hasValue = false
	}
	if !ok || len(values) != len(node.entries) {
		bucket.values = bucket.values[:0]
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
	if err := m.propagateFromStage(stage, token, span, delta); err != nil {
		delta.supported = false
	}
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

func aggregateValuesEqual(left, right []Value) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !left[i].Equal(right[i]) {
			return false
		}
	}
	return true
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
	bucket = m.reuseBucket(parent)
	m.buckets[key] = bucket
	return bucket
}

func (m *reteGraphAggregateNodeMemory) reuseBucket(parent tokenRef) *reteGraphAggregateBucket {
	if m == nil {
		return nil
	}
	last := len(m.freeBuckets) - 1
	if last >= 0 {
		bucket := m.freeBuckets[last]
		m.freeBuckets[last] = nil
		m.freeBuckets = m.freeBuckets[:last]
		bucket.parent = parent
		return bucket
	}
	return &reteGraphAggregateBucket{parent: parent}
}

func (m *reteGraphAggregateNodeMemory) recycleBucket(bucket *reteGraphAggregateBucket) {
	if m == nil || bucket == nil {
		return
	}
	bucket.clear()
	m.freeBuckets = append(m.freeBuckets, bucket)
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
	m.count++
	if !aggregateSpecsNeedInputValues(node.specs) {
		return nil
	}
	m.ensureSpecState(len(node.specs))
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

func (m *reteGraphAggregateBucket) addCountOnlyMember(token tokenRef) bool {
	if m == nil || token.isZero() {
		return false
	}
	count := m.countOnlyMemberCount()
	for i := range count {
		current := m.countOnlyMemberAt(i)
		if tokenRefsSameIdentity(current, token) {
			m.setCountOnlyMemberAt(i, token)
			return false
		}
	}
	m.setCountOnlyMemberAt(count, token)
	m.count = int64(count + 1)
	return true
}

func (m *reteGraphAggregateBucket) removeCountOnlyMember(token tokenRef) bool {
	if m == nil || token.isZero() {
		return false
	}
	count := m.countOnlyMemberCount()
	for i := range count {
		current := m.countOnlyMemberAt(i)
		if !tokenRefsSameIdentity(current, token) {
			continue
		}
		last := count - 1
		m.setCountOnlyMemberAt(i, m.countOnlyMemberAt(last))
		m.truncateCountOnlyMembers(last)
		return true
	}
	return false
}

func (m *reteGraphAggregateBucket) countOnlyMemberCount() int {
	if m == nil || m.count <= 0 {
		return 0
	}
	return int(m.count)
}

func (m *reteGraphAggregateBucket) countOnlyMemberAt(index int) tokenRef {
	if m == nil || index < 0 || index >= m.countOnlyMemberCount() {
		return tokenRef{}
	}
	switch index {
	case 0:
		return m.countOnlyFirst
	case 1:
		return m.countOnlySecond
	default:
		restIndex := index - 2
		if restIndex < 0 || restIndex >= len(m.countOnlyRest) {
			return tokenRef{}
		}
		return m.countOnlyRest[restIndex]
	}
}

func (m *reteGraphAggregateBucket) setCountOnlyMemberAt(index int, token tokenRef) {
	if m == nil || index < 0 {
		return
	}
	switch index {
	case 0:
		m.countOnlyFirst = token
	case 1:
		m.countOnlySecond = token
	default:
		restIndex := index - 2
		if restIndex >= len(m.countOnlyRest) {
			m.countOnlyRest = append(m.countOnlyRest, make([]tokenRef, restIndex-len(m.countOnlyRest)+1)...)
		}
		m.countOnlyRest[restIndex] = token
	}
}

func (m *reteGraphAggregateBucket) truncateCountOnlyMembers(length int) {
	if m == nil {
		return
	}
	if length < 0 {
		length = 0
	}
	old := m.countOnlyMemberCount()
	for i := length; i < old; i++ {
		m.setCountOnlyMemberAt(i, tokenRef{})
	}
	if length < 2 {
		clear(m.countOnlyRest)
		m.countOnlyRest = m.countOnlyRest[:0]
	} else {
		restLength := length - 2
		clear(m.countOnlyRest[restLength:])
		m.countOnlyRest = m.countOnlyRest[:restLength]
	}
	m.count = int64(length)
}

func (m *reteGraphAggregateBucket) removeMember(node *reteGraphAggregateNode, member reteGraphAggregateMember) {
	m.removeMemberWithCollectKey(node, member, tokenRefKey(member.token))
}

func (m *reteGraphAggregateBucket) removeMemberWithCollectKey(node *reteGraphAggregateNode, member reteGraphAggregateMember, collectKey graphTokenIdentityKey) {
	if m == nil || node == nil {
		return
	}
	if m.count > 0 {
		m.count--
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		return
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
			m.removeCollectByKey(i, collectKey)
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
	m.removeCollectByKey(index, tokenRefKey(member.token))
}

func (m *reteGraphAggregateBucket) removeCollectByKey(index int, key graphTokenIdentityKey) {
	if m == nil || index < 0 || index >= len(m.collects) {
		return
	}
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

func (m *reteGraphBetaMemory) propagateFromStage(source reteGraphStageRef, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) error {
	if m == nil || delta == nil {
		return nil
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
		ok, err := m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta)
		if err != nil {
			return err
		}
		if !ok {
			delta.supported = false
		}
	}
	return nil
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

func (m *reteGraphBetaMemory) finalizeDeferredNegativeOutputs(span *propagationCounterSpan) error {
	if m == nil || m.graph == nil {
		return nil
	}
	delta := &reteAgendaDelta{supported: true}
	for _, node := range m.graph.betaNodes {
		if node.kind != reteGraphBetaNodeNot {
			continue
		}
		nodeMemory := m.nodeMemory(node.id)
		if nodeMemory == nil {
			continue
		}
		source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(node.id)}
		for i := range nodeMemory.left.rows {
			row := nodeMemory.left.rows[i]
			if row.token.isZero() || row.negativeBlockerCount() != 0 {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			if err := m.propagateFromStage(source, row.token, span, delta); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *reteGraphBetaMemory) insertBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, entry bindingTupleEntry, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil || token.isZero() {
		return false, nil
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false, nil
	}
	if node.kind == reteGraphBetaNodeFilter {
		return m.insertFilterBetaInput(nodeID, side, node, token, span, delta)
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
		var err error
		joinKey, ok, err = graphBetaJoinKeyForLeftTokenWithContext(m.context(), node, token, span)
		if err != nil || !ok {
			return false, err
		}
		inserted = nodeMemory.left.insert(token, joinKey)
	case reteGraphBetaInputRight:
		var err error
		joinKey, ok, err = graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, span)
		if err != nil || !ok {
			return false, err
		}
		inserted = nodeMemory.right.insert(token, joinKey)
	default:
		return false, nil
	}
	if !inserted {
		return true, nil
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
			rightMatch, ok := tokenFactMatchForBindingSlot(rightRow.token, node.entry.bindingSlot)
			if !ok {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, span); err != nil {
				return false, err
			} else if !ok {
				continue
			}
			output := m.appendTokenRows(token, rightRow.token, span)
			if output.isZero() {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			if err := m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta); err != nil {
				return false, err
			}
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenFactMatchForBindingSlot(token, node.entry.bindingSlot)
		if !ok {
			return false, nil
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
				return false, err
			} else if !ok {
				continue
			}
			output := m.appendTokenRows(leftRow.token, token, span)
			if output.isZero() {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			if err := m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func (m *reteGraphBetaMemory) insertFilterBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil || node == nil || token.isZero() {
		return false, nil
	}
	if side != reteGraphBetaInputLeft {
		return false, nil
	}
	ok, err := m.filterTokenMatches(node, token, span)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	nodeMemory := m.nodeMemory(nodeID)
	inserted := nodeMemory.left.insert(token, betaJoinKey{})
	if !inserted {
		return true, nil
	}
	if span != nil {
		span.recordBetaInputInsert(side)
		span.recordBetaJoinedTokenProduced()
	}
	if err := m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, token, span, delta); err != nil {
		return false, err
	}
	return true, nil
}

func (m *reteGraphBetaMemory) insertNegativeBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil || node == nil || token.isZero() {
		return false, nil
	}
	nodeMemory := m.nodeMemory(nodeID)
	negativeMemory := m.negativeBetaMemory(nodeID, node)
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok, err := graphBetaJoinKeyForLeftTokenWithContext(m.context(), node, token, span)
		if err != nil || !ok {
			return false, err
		}
		count, ok := negativeMemory.blockerCountForLeft(joinKey, token, span)
		if !ok {
			return false, nil
		}
		inserted := nodeMemory.left.insertWithNegativeBlockerCount(token, joinKey, count)
		if !inserted {
			return true, nil
		}
		if span != nil {
			span.recordBetaInputInsert(side)
		}
		if count == 0 && !m.deferNegativeOutputs {
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			if err := m.propagateFromStage(source, token, span, delta); err != nil {
				return false, err
			}
		}
	case reteGraphBetaInputRight:
		joinKey, ok, err := graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, span)
		if err != nil || !ok {
			return false, err
		}
		inserted := nodeMemory.right.insert(token, joinKey)
		if !inserted {
			return true, nil
		}
		if span != nil {
			span.recordBetaInputInsert(side)
		}
		var currentMatch conditionMatch
		if len(node.residualJoins) != 0 || len(node.predicates) != 0 || len(node.rightPredicates) != 0 {
			currentMatch, ok = tokenLastMatch(token)
			if !ok {
				return false, nil
			}
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
			if node.rightHasLeftPrefix && !tokenRefHasPrefix(token, leftRow.token) {
				continue
			}
			if len(node.residualJoins) != 0 || len(node.predicates) != 0 {
				if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, span); err != nil {
					return false, err
				} else if !ok {
					continue
				}
			}
			if ok, err := m.rightPredicatesMatch(node, currentMatch, leftRow.token, span); err != nil {
				return false, err
			} else if !ok {
				continue
			}
			if leftRow.incrementNegativeBlockerCount() == 1 && !m.deferNegativeOutputs {
				m.propagateRemoveFromStage(source, leftRow.token, nil, delta)
			}
		}
	default:
		return false, nil
	}
	return true, nil
}

func (m *reteGraphBetaMemory) removeBetaInputToken(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || token.isZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	if node.kind == reteGraphBetaNodeFilter {
		return m.removeFilterBetaInputToken(nodeID, side, node, token, counters, delta)
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
		var err error
		joinKey, ok, err = graphBetaJoinKeyForLeftTokenWithContext(m.context(), node, token, nil)
		if err != nil || !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.left.removeToken(token, counters)
		removed, ok = removedRow.token, removedOK
	case reteGraphBetaInputRight:
		var err error
		joinKey, ok, err = graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, nil)
		if err != nil || !ok {
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

func (m *reteGraphBetaMemory) removeFilterBetaInputToken(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || node == nil || token.isZero() {
		return false
	}
	if side != reteGraphBetaInputLeft {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	removedRow, removedOK := nodeMemory.left.removeToken(token, counters)
	if !removedOK {
		return true
	}
	if counters != nil {
		counters.recordNegativeRowRemoved()
	}
	m.propagateRemoveFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, removedRow.token, counters, delta)
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
		removedRow, removedOK := nodeMemory.left.removeToken(token, counters)
		if !removedOK {
			return true
		}
		if counters != nil {
			counters.recordNegativeRowRemoved()
		}
		if removedRow.negativeBlockerCount() == 0 {
			m.propagateRemoveFromStage(source, removedRow.token, counters, delta)
		}
	case reteGraphBetaInputRight:
		joinKey, ok, err := graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, nil)
		if err != nil || !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.right.removeToken(token, counters)
		if !removedOK {
			return true
		}
		if counters != nil {
			counters.recordNegativeRowRemoved()
		}
		var currentMatch conditionMatch
		if len(node.residualJoins) != 0 || len(node.predicates) != 0 || len(node.rightPredicates) != 0 {
			currentMatch, ok = tokenLastMatch(removedRow.token)
			if !ok {
				return false
			}
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if node.rightHasLeftPrefix && !tokenRefHasPrefix(removedRow.token, leftRow.token) {
				continue
			}
			if len(node.residualJoins) != 0 || len(node.predicates) != 0 {
				if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, nil); err != nil {
					delta.supported = false
				} else if !ok {
					continue
				}
			}
			if ok, err := m.rightPredicatesMatch(node, currentMatch, leftRow.token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				continue
			}
			if leftRow.negativeBlockerCount() <= 0 {
				delta.supported = false
				continue
			}
			if leftRow.decrementNegativeBlockerCount() == 0 {
				if err := m.propagateFromStage(source, leftRow.token, nil, delta); err != nil {
					delta.supported = false
				}
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
			rightMatch, ok := tokenFactMatchForBindingSlot(rightRow.token, node.entry.bindingSlot)
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
	if node.kind == reteGraphBetaNodeFilter {
		return m.removeFilterBetaInputContainingFact(nodeID, side, node, id, counters, delta)
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

func (m *reteGraphBetaMemory) removeFilterBetaInputContainingFact(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || node == nil || id.IsZero() {
		return false
	}
	if side != reteGraphBetaInputLeft {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	removed := nodeMemory.left.removeContainingFact(id, counters)
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
			if row.negativeBlockerCount() == 0 {
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

func (m *reteGraphBetaMemory) removeFact(ctx context.Context, fact FactSnapshot, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	delta := reteAgendaDelta{supported: true}
	id := fact.ID()
	defer m.removeFactSource(id)
	nodeIDs := m.matchedAlphaRouteIDsForFact(id)
	if len(nodeIDs) == 0 {
		nodeIDs = m.snapshotAlphaRouteIDsForFact(fact)
		if len(nodeIDs) == 0 {
			m.removeAlphaFact(id)
			return delta, nil
		}
	}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		ok, err := node.matchesSnapshotWithContextAndCounters(ctx, fact, nil)
		if err != nil {
			return delta, err
		}
		if !ok {
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
	return delta, nil
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
				for _, branchID := range row.terminalBranchIDs() {
					if key, ok := m.terminalBranchKey(terminalNode.id, branchID); ok {
						counters.recordTerminalDeltaRemovedForBranch(key)
						counters.recordTerminalRowRemovedForBranch(key)
					}
				}
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

func (m *reteGraphBetaMemory) updateFact(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, error) {
	if m == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	if m.canSkipUnmatchedModifyPropagation(event) {
		m.upsertFactSource(event.after)
		if event.counters != nil {
			event.counters.recordModifyFastPathSkip()
		}
		return reteAgendaDelta{supported: true}, nil
	}
	if delta, ok := m.refreshDirectTerminalModify(ctx, event); ok {
		m.upsertFactSource(event.after)
		if event.counters != nil {
			event.counters.recordModifyFastPathSkip()
		}
		return delta, nil
	}
	if delta, ok := m.refreshPositiveBetaModify(ctx, event); ok {
		m.upsertFactSource(event.after)
		if event.counters != nil {
			event.counters.recordModifyFastPathSkip()
		}
		return delta, nil
	}
	if delta, ok := m.refreshAggregateModify(ctx, event); ok {
		m.upsertFactSource(event.after)
		if event.counters != nil {
			event.counters.recordModifyFastPathSkip()
		}
		return delta, nil
	}
	if event.counters != nil {
		event.counters.recordModifyFastPathFallback()
	}
	removed, err := m.removeFact(ctx, event.before, event.counters)
	if err != nil {
		return removed, err
	}
	added, err := m.insertFact(ctx, event.after, nil)
	if err != nil {
		return added, err
	}
	addedTokens, removedTokens := coalesceTerminalTokenDeltas(m.revision, append(removed.added, added.added...), append(removed.removed, added.removed...))
	return reteAgendaDelta{
		supported: removed.supported && added.supported,
		added:     addedTokens,
		removed:   removedTokens,
	}, nil
}

func (m *reteGraphBetaMemory) canSkipUnmatchedModifyPropagation(event reteGraphPropagationEvent) bool {
	if m == nil || m.graph == nil || len(event.changes) == 0 {
		return false
	}
	before, after := event.before, event.after
	if before.ID() != after.ID() || before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() || event.templateChanged || event.nameChanged {
		return false
	}
	if event.duplicateChanged {
		return false
	}
	if len(m.matchedAlphaRouteIDsForFact(before.ID())) != 0 {
		return false
	}
	summary := newFactModifySummaryFromPropagationEvent(event)
	if !summary.knownSlotChange() {
		return false
	}
	return !m.graph.alphaRoutesMayObserveModify(before, after, summary)
}

func (m *reteGraphBetaMemory) refreshDirectTerminalModify(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, bool) {
	if m == nil || m.graph == nil || m.revision == nil || len(event.changes) == 0 {
		return reteAgendaDelta{}, false
	}
	before, after := event.before, event.after
	if before.ID() != after.ID() || before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() || event.templateChanged || event.nameChanged || event.duplicateChanged {
		return reteAgendaDelta{}, false
	}
	summary := newFactModifySummaryFromPropagationEvent(event)
	if !summary.knownSlotChange() || m.graph.alphaRoutesMayObserveModify(before, after, summary) {
		return reteAgendaDelta{}, false
	}
	nodeIDs := m.matchedAlphaRouteIDsForFact(before.ID())
	if len(nodeIDs) == 0 {
		nodeIDs = m.snapshotAlphaRouteIDsForFact(before)
		if len(nodeIDs) == 0 {
			return reteAgendaDelta{}, false
		}
	}
	for _, nodeID := range nodeIDs {
		source := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}
		if len(m.graph.successorsByStage[source]) != 0 || len(m.graph.aggregateOuters[source]) != 0 || len(m.graph.aggregatesByStage[source]) != 0 {
			return reteAgendaDelta{}, false
		}
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			return reteAgendaDelta{}, false
		}
		matchesAfter, err := node.matchesSnapshotWithContextAndCounters(ctx, after, nil)
		if err != nil || !matchesAfter {
			return reteAgendaDelta{}, false
		}
		if !m.terminalRuleActionsIgnoreModify(source, node.entry.bindingSlot, summary) {
			return reteAgendaDelta{}, false
		}
	}

	cache := m.resetTokenRefreshCache()
	delta := reteAgendaDelta{supported: true}
	for _, nodeID := range nodeIDs {
		source := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			return reteAgendaDelta{}, false
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromSnapshot(after),
		}
		for _, terminal := range m.graph.terminalsByStage[source] {
			entry := terminal.entry
			if entry.conditionID == "" {
				entry = node.entry
			}
			if entry.conditionID == "" {
				return reteAgendaDelta{}, false
			}
			terminalMemory := m.terminalAt(terminal.terminalID)
			if terminalMemory == nil {
				continue
			}
			terminalNode := m.terminalNode(terminal.terminalID)
			collectUpdates := terminalNode != nil && terminalNode.kind == reteGraphTerminalRule
			start := len(delta.updated)
			delta.updated = terminalMemory.rows.refreshTerminalTokensContainingFact(before.ID(), delta.updated, collectUpdates, func(row graphTokenRow) (tokenRef, bool) {
				return m.refreshTokenFactRefInPlaceCached(row.token, before.ID(), match.fact, cache)
			})
			if !collectUpdates {
				continue
			}
			for i := start; i < len(delta.updated); i++ {
				delta.updated[i].ruleRevisionID = terminalNode.ruleRevisionID
			}
		}
	}
	return delta, true
}

func (m *reteGraphBetaMemory) terminalRuleActionsIgnoreModify(source reteGraphStageRef, bindingSlot int, summary factModifySummary) bool {
	if m == nil || m.graph == nil || m.revision == nil {
		return false
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		terminalNode := m.terminalNode(terminal.terminalID)
		if terminalNode == nil {
			return false
		}
		if terminalNode.kind == reteGraphTerminalQuery {
			continue
		}
		if terminalNode.kind != reteGraphTerminalRule {
			return false
		}
		rule, ok := m.revision.rulesByRevisionID[terminalNode.ruleRevisionID]
		if !ok {
			return false
		}
		for _, action := range rule.actionExecutions {
			if action.bindingReads.observesModify(bindingSlot, summary) {
				return false
			}
		}
	}
	return true
}

func (m *reteGraphBetaMemory) modifyRouteScopeForAlphaRoutes(nodeIDs []reteGraphAlphaNodeID) *reteModifyRouteScope {
	if m == nil || m.graph == nil {
		return &reteModifyRouteScope{}
	}
	scope := &m.modifyRouteScope
	scope.reset()
	for _, nodeID := range nodeIDs {
		scope.appendStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)})
	}
	for head := 0; head < len(scope.stageQueue); head++ {
		stage := scope.stageQueue[head]
		for _, terminal := range m.graph.terminalsByStage[stage] {
			scope.appendTerminal(terminal.terminalID)
		}
		for _, successor := range m.graph.successorsByStage[stage] {
			scope.appendBeta(successor.betaNodeID)
			scope.appendStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(successor.betaNodeID)})
		}
		for _, aggregateID := range m.graph.aggregateOuters[stage] {
			scope.appendAggregate(aggregateID)
			scope.appendStage(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregateID)})
		}
		for _, aggregateID := range m.graph.aggregatesByStage[stage] {
			scope.appendAggregate(aggregateID)
			scope.appendStage(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregateID)})
		}
	}
	return scope
}

func (s *reteModifyRouteScope) reset() {
	if s == nil {
		return
	}
	s.stageQueue = s.stageQueue[:0]
	s.stages = s.stages[:0]
	s.betaNodes = s.betaNodes[:0]
	s.aggregateNodes = s.aggregateNodes[:0]
	s.terminalNodes = s.terminalNodes[:0]
}

func (s *reteModifyRouteScope) appendStage(stage reteGraphStageRef) {
	if s == nil || stage.kind == reteGraphStageUnknown {
		return
	}
	if slices.Contains(s.stages, stage) {
		return
	}
	s.stages = append(s.stages, stage)
	s.stageQueue = append(s.stageQueue, stage)
}

func (s *reteModifyRouteScope) appendBeta(id reteGraphBetaNodeID) {
	if s == nil || id <= 0 || slices.Contains(s.betaNodes, id) {
		return
	}
	s.betaNodes = append(s.betaNodes, id)
}

func (s *reteModifyRouteScope) appendAggregate(id reteGraphAggregateNodeID) {
	if s == nil || id <= 0 || slices.Contains(s.aggregateNodes, id) {
		return
	}
	s.aggregateNodes = append(s.aggregateNodes, id)
}

func (s *reteModifyRouteScope) appendTerminal(id reteGraphTerminalNodeID) {
	if s == nil || id <= 0 || slices.Contains(s.terminalNodes, id) {
		return
	}
	s.terminalNodes = append(s.terminalNodes, id)
}

func (m *reteGraphBetaMemory) refreshPositiveBetaModify(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, bool) {
	if m == nil || m.graph == nil || m.revision == nil || len(event.changes) == 0 {
		return reteAgendaDelta{}, false
	}
	if len(m.graph.betaNodes) == 0 {
		return reteAgendaDelta{}, false
	}
	before, after := event.before, event.after
	if before.ID() != after.ID() || before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() || event.templateChanged || event.nameChanged || event.duplicateChanged {
		return reteAgendaDelta{}, false
	}
	summary := newFactModifySummaryFromPropagationEvent(event)
	if !summary.knownSlotChange() || m.graph.alphaRoutesMayObserveModify(before, after, summary) {
		return reteAgendaDelta{}, false
	}
	nodeIDs := m.matchedAlphaRouteIDsForFact(before.ID())
	if len(nodeIDs) == 0 {
		nodeIDs = m.snapshotAlphaRouteIDsForFact(before)
		if len(nodeIDs) == 0 {
			return reteAgendaDelta{}, false
		}
	}
	bindingSlots := make([]int, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			return reteAgendaDelta{}, false
		}
		matchesAfter, err := node.matchesSnapshotWithContextAndCounters(ctx, after, nil)
		if err != nil || !matchesAfter {
			return reteAgendaDelta{}, false
		}
		if !slices.Contains(bindingSlots, node.entry.bindingSlot) {
			bindingSlots = append(bindingSlots, node.entry.bindingSlot)
		}
	}
	scope := m.modifyRouteScopeForAlphaRoutes(nodeIDs)
	if len(scope.aggregateNodes) != 0 {
		return reteAgendaDelta{}, false
	}
	for _, betaNodeID := range scope.betaNodes {
		betaNode := m.graph.betaNode(betaNodeID)
		if betaNode == nil {
			return reteAgendaDelta{}, false
		}
		if betaNodeMayObserveModify(*betaNode, bindingSlots, summary) {
			return reteAgendaDelta{}, false
		}
	}
	for _, terminalID := range scope.terminalNodes {
		terminalNode := m.terminalNode(terminalID)
		if terminalNode == nil {
			return reteAgendaDelta{}, false
		}
		if terminalNode.kind == reteGraphTerminalQuery {
			continue
		}
		if terminalNode.kind != reteGraphTerminalRule {
			return reteAgendaDelta{}, false
		}
		rule, ok := m.revision.rulesByRevisionID[terminalNode.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		for _, bindingSlot := range bindingSlots {
			for _, action := range rule.actionExecutions {
				if action.bindingReads.observesModify(bindingSlot, summary) {
					return reteAgendaDelta{}, false
				}
			}
		}
	}

	afterRef := newConditionFactRefFromSnapshot(after)
	cache := m.resetTokenRefreshCache()
	refresh := func(row graphTokenRow) (tokenRef, bool) {
		return m.refreshTokenFactRefInPlaceCached(row.token, before.ID(), afterRef, cache)
	}
	for _, nodeID := range scope.betaNodes {
		nodeMemory := m.nodeMemory(nodeID)
		if nodeMemory == nil {
			continue
		}
		if !nodeMemory.left.refreshTokensContainingFact(before.ID(), refresh) {
			return reteAgendaDelta{}, false
		}
		if !nodeMemory.right.refreshTokensContainingFact(before.ID(), refresh) {
			return reteAgendaDelta{}, false
		}
	}
	delta := reteAgendaDelta{supported: true}
	for _, terminalID := range scope.terminalNodes {
		terminalNode := m.terminalNode(terminalID)
		if terminalNode == nil {
			return reteAgendaDelta{}, false
		}
		terminal := m.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		collectUpdates := terminalNode.kind == reteGraphTerminalRule
		start := len(delta.updated)
		delta.updated = terminal.rows.refreshTerminalTokensContainingFact(before.ID(), delta.updated, collectUpdates, refresh)
		if !collectUpdates {
			continue
		}
		for i := start; i < len(delta.updated); i++ {
			delta.updated[i].ruleRevisionID = terminalNode.ruleRevisionID
		}
	}
	return delta, true
}

func (m *reteGraphBetaMemory) refreshAggregateModify(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, bool) {
	if m == nil || m.graph == nil || m.revision == nil || len(event.changes) == 0 {
		return reteAgendaDelta{}, false
	}
	if len(m.graph.aggregateNodes) == 0 {
		return reteAgendaDelta{}, false
	}
	before, after := event.before, event.after
	if before.ID() != after.ID() || before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() || event.templateChanged || event.nameChanged || event.duplicateChanged {
		return reteAgendaDelta{}, false
	}
	summary := newFactModifySummaryFromPropagationEvent(event)
	if !summary.knownSlotChange() || m.graph.alphaRoutesMayObserveModify(before, after, summary) {
		return reteAgendaDelta{}, false
	}
	nodeIDs := m.matchedAlphaRouteIDsForFact(before.ID())
	if len(nodeIDs) == 0 {
		nodeIDs = m.snapshotAlphaRouteIDsForFact(before)
		if len(nodeIDs) == 0 {
			return reteAgendaDelta{}, false
		}
	}
	bindingSlots := make([]int, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			return reteAgendaDelta{}, false
		}
		matchesAfter, err := node.matchesSnapshotWithContextAndCounters(ctx, after, nil)
		if err != nil || !matchesAfter {
			return reteAgendaDelta{}, false
		}
		if !slices.Contains(bindingSlots, node.entry.bindingSlot) {
			bindingSlots = append(bindingSlots, node.entry.bindingSlot)
		}
	}
	scope := m.modifyRouteScopeForAlphaRoutes(nodeIDs)
	for _, betaNodeID := range scope.betaNodes {
		betaNode := m.graph.betaNode(betaNodeID)
		if betaNode == nil {
			return reteAgendaDelta{}, false
		}
		if betaNodeMayObserveModify(*betaNode, bindingSlots, summary) {
			return reteAgendaDelta{}, false
		}
	}
	for _, aggregateNodeID := range scope.aggregateNodes {
		aggregateNode := m.graph.aggregateNode(aggregateNodeID)
		if aggregateNode == nil {
			return reteAgendaDelta{}, false
		}
		if aggregateNodeMayObserveModify(*aggregateNode, bindingSlots, summary) {
			return reteAgendaDelta{}, false
		}
	}
	for _, terminalID := range scope.terminalNodes {
		terminalNode := m.terminalNode(terminalID)
		if terminalNode == nil {
			return reteAgendaDelta{}, false
		}
		if terminalNode.kind == reteGraphTerminalQuery {
			continue
		}
		if terminalNode.kind != reteGraphTerminalRule {
			return reteAgendaDelta{}, false
		}
		rule, ok := m.revision.rulesByRevisionID[terminalNode.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		for _, bindingSlot := range bindingSlots {
			for _, action := range rule.actionExecutions {
				if action.bindingReads.observesModify(bindingSlot, summary) {
					return reteAgendaDelta{}, false
				}
			}
		}
	}

	afterRef := newConditionFactRefFromSnapshot(after)
	cache := m.resetTokenRefreshCache()
	refresh := func(row graphTokenRow) (tokenRef, bool) {
		return m.refreshTokenFactRefInPlaceCached(row.token, before.ID(), afterRef, cache)
	}
	for _, nodeID := range scope.betaNodes {
		nodeMemory := m.nodeMemory(nodeID)
		if nodeMemory == nil {
			continue
		}
		if !nodeMemory.left.refreshTokensContainingFact(before.ID(), refresh) {
			return reteAgendaDelta{}, false
		}
		if !nodeMemory.right.refreshTokensContainingFact(before.ID(), refresh) {
			return reteAgendaDelta{}, false
		}
	}
	delta := reteAgendaDelta{supported: true}
	for _, aggregateNodeID := range scope.aggregateNodes {
		if !m.refreshAggregateParentsContainingFact(aggregateNodeID, before.ID(), afterRef, cache, &delta) {
			return reteAgendaDelta{}, false
		}
		if !m.refreshAggregateMembersContainingFact(aggregateNodeID, before.ID(), afterRef, cache, &delta) {
			return reteAgendaDelta{}, false
		}
	}
	for _, terminalID := range scope.terminalNodes {
		terminalNode := m.terminalNode(terminalID)
		if terminalNode == nil {
			return reteAgendaDelta{}, false
		}
		terminal := m.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		collectUpdates := terminalNode.kind == reteGraphTerminalRule
		start := len(delta.updated)
		delta.updated = terminal.rows.refreshTerminalTokensContainingFact(before.ID(), delta.updated, collectUpdates, refresh)
		if !collectUpdates {
			continue
		}
		for i := start; i < len(delta.updated); i++ {
			delta.updated[i].ruleRevisionID = terminalNode.ruleRevisionID
		}
	}
	return delta, true
}

func (m *reteGraphBetaMemory) refreshTokenFactRef(token tokenRef, id FactID, after conditionFactRef) (tokenRef, bool) {
	return m.refreshTokenFactRefCached(token, id, after, nil)
}

func (m *reteGraphBetaMemory) refreshTokenFactRefInPlace(token tokenRef, id FactID, after conditionFactRef) (tokenRef, bool) {
	return m.refreshTokenFactRefInPlaceCached(token, id, after, nil)
}

func (m *reteGraphBetaMemory) refreshTokenFactRefInPlaceCached(token tokenRef, id FactID, after conditionFactRef, cache map[tokenHandle]tokenRef) (tokenRef, bool) {
	if token.isZero() {
		return tokenRef{}, true
	}
	if !token.containsFact(id) {
		return token, true
	}
	if cache != nil {
		if cached, ok := cache[token.handle]; ok {
			return cached, true
		}
	}
	if !m.refreshTokenFactRefInPlaceRow(token, id, after, cache) {
		return tokenRef{}, false
	}
	if cache != nil {
		cache[token.handle] = token
	}
	return token, true
}

func (m *reteGraphBetaMemory) refreshTokenFactRefInPlaceRow(token tokenRef, id FactID, after conditionFactRef, cache map[tokenHandle]tokenRef) bool {
	if token.isZero() {
		return true
	}
	if cache != nil {
		if _, ok := cache[token.handle]; ok {
			return true
		}
	}
	row, ok := token.resolve()
	if !ok {
		return false
	}
	parent := token.parent()
	if !m.refreshTokenFactRefInPlaceRow(parent, id, after, cache) {
		return false
	}
	parentRow, haveParent := parent.resolve()
	match := row.match
	recency := match.fact.Recency()
	if !match.hasValue && match.fact.ID() == id {
		match.fact = after
		recency = after.Recency()
		row.generation = after.Generation()
	}
	row.match = match
	row.refreshFactSpan(token.handle.arena, parentRow, match)
	if haveParent {
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.aggregateRecency = addRecency(parentRow.aggregateRecency, recency)
		row.identityState = parentRow.identityState
		if row.generation == 0 {
			row.generation = parentRow.generation
		}
	} else {
		row.maxRecency = recency
		row.aggregateRecency = recency
		row.identityState = candidateIdentityHashStart(row.generation)
	}
	identityEntry := row.entry
	identityEntry.value = match.value
	identityEntry.hasValue = match.hasValue
	if !match.hasValue {
		identityEntry.factID = match.fact.ID()
		identityEntry.factVersion = match.fact.Version()
	}
	row.entry = identityEntry
	row.identityState = candidateIdentityHashStep(row.identityState, identityEntry)
	if cache != nil {
		cache[token.handle] = token
	}
	return true
}

func (r *tokenRow) refreshFactSpan(arena *tokenArena, parent *tokenRow, match conditionMatch) {
	if r == nil || arena == nil || r.size <= 0 || r.factSpanStart < 0 {
		return
	}
	end := r.factSpanStart + r.size
	if end > len(arena.factIDs) || end > len(arena.factVersions) {
		return
	}
	if parent != nil {
		parentEnd := parent.factSpanStart + parent.size
		if parent.factSpanStart < 0 || parentEnd > len(arena.factIDs) || parentEnd > len(arena.factVersions) || parent.size > r.size-1 {
			return
		}
		copy(arena.factIDs[r.factSpanStart:r.factSpanStart+parent.size], arena.factIDs[parent.factSpanStart:parentEnd])
		copy(arena.factVersions[r.factSpanStart:r.factSpanStart+parent.size], arena.factVersions[parent.factSpanStart:parentEnd])
	}
	index := end - 1
	var id FactID
	var version FactVersion
	if !match.hasValue {
		id = match.fact.ID()
		version = match.fact.Version()
	}
	arena.factIDs[index] = id
	arena.factVersions[index] = version
}

func (m *reteGraphBetaMemory) resetTokenRefreshCache() map[tokenHandle]tokenRef {
	if m == nil {
		return nil
	}
	if m.tokenRefreshCache == nil {
		m.tokenRefreshCache = make(map[tokenHandle]tokenRef)
		return m.tokenRefreshCache
	}
	clear(m.tokenRefreshCache)
	return m.tokenRefreshCache
}

func (m *reteGraphBetaMemory) refreshTokenFactRefCached(token tokenRef, id FactID, after conditionFactRef, cache map[tokenHandle]tokenRef) (tokenRef, bool) {
	if token.isZero() {
		return tokenRef{}, true
	}
	if !token.containsFact(id) {
		return token, true
	}
	if cache != nil {
		if cached, ok := cache[token.handle]; ok {
			return cached, true
		}
	}
	row, ok := token.resolve()
	if !ok {
		return tokenRef{}, false
	}
	parent, ok := m.refreshTokenFactRefCached(token.parent(), id, after, cache)
	if !ok {
		return tokenRef{}, false
	}
	match := row.match
	recency := match.fact.Recency()
	generation := row.generation
	if !match.hasValue && match.fact.ID() == id {
		match.fact = after
		recency = after.Recency()
		generation = after.Generation()
	}
	next := m.newTokenRef(parent, row.entry, match, recency, generation, nil)
	if next.isZero() {
		return tokenRef{}, false
	}
	if cache != nil {
		cache[token.handle] = next
	}
	return next, true
}

func aggregateNodeMayObserveModify(node reteGraphAggregateNode, bindingSlots []int, summary factModifySummary) bool {
	for _, spec := range node.specs {
		if !spec.hasExpr {
			continue
		}
		for _, bindingSlot := range bindingSlots {
			if expressionMayObserveModify(node.inputEntry.bindingSlot, bindingSlot, spec.expression, summary) {
				return true
			}
		}
	}
	return false
}

func betaNodeMayObserveModify(node reteGraphBetaNode, bindingSlots []int, summary factModifySummary) bool {
	switch node.kind {
	case reteGraphBetaNodeJoin, reteGraphBetaNodeNot:
		if predicatesMayObserveModify(node.predicates, bindingSlots, summary) || predicatesMayObserveModify(node.rightPredicates, bindingSlots, summary) {
			return true
		}
	case reteGraphBetaNodeFilter:
		return predicatesMayObserveModify(node.predicates, bindingSlots, summary)
	default:
		return true
	}
	for _, join := range node.joins {
		for _, bindingSlot := range bindingSlots {
			if join.bindingSlot == bindingSlot && summary.observesAccess(join.access) {
				return true
			}
			if join.refBindingSlot == bindingSlot && summary.observesAccess(join.refAccess) {
				return true
			}
			if join.hasLeftKeyExpression && expressionMayObserveModify(join.bindingSlot, bindingSlot, join.leftKeyExpression, summary) {
				return true
			}
			if join.hasRightKeyExpression && expressionMayObserveModify(join.refBindingSlot, bindingSlot, join.rightKeyExpression, summary) {
				return true
			}
		}
	}
	return false
}

func predicatesMayObserveModify(predicates []compiledExpressionPredicate, bindingSlots []int, summary factModifySummary) bool {
	for _, predicate := range predicates {
		for _, bindingSlot := range bindingSlots {
			if expressionMayObserveModify(predicate.currentBindingSlot, bindingSlot, predicate.expression, summary) {
				return true
			}
		}
	}
	return false
}

func expressionMayObserveCurrentFactModify(expression compiledExpression, summary factModifySummary) bool {
	return expressionMayObserveModify(0, 0, expression, summary)
}

func expressionMayObserveModify(expressionBindingSlot, modifiedBindingSlot int, expression compiledExpression, summary factModifySummary) bool {
	switch expression.kind {
	case expressionNodeCurrentField, expressionNodeHasPath:
		if expressionBindingSlot == modifiedBindingSlot {
			return summary.observesAccess(expression.access)
		}
		return false
	case expressionNodeBindingField:
		if expression.bindingSlot == modifiedBindingSlot {
			return summary.observesAccess(expression.access)
		}
		return false
	case expressionNodeBindingValue:
		return expression.bindingSlot == modifiedBindingSlot
	case expressionNodeCall, expressionNodeCompare, expressionNodeBoolean:
		for _, operand := range expression.operands {
			if expressionMayObserveModify(expressionBindingSlot, modifiedBindingSlot, operand, summary) {
				return true
			}
		}
		return false
	default:
		return false
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
	branchKey, haveBranchKey := m.terminalBranchKey(terminalID, branchID)
	if !terminal.rows.insertTerminal(token, identity, branchID) {
		if span != nil {
			span.recordTerminalRowDeduped()
			if haveBranchKey {
				span.recordTerminalRowDedupedForBranch(branchKey)
			}
		}
		return
	}
	if span != nil {
		span.recordTerminalRowInserted()
		if haveBranchKey {
			span.recordTerminalRowInsertedForBranch(branchKey)
		}
	}
	if span != nil && !m.suppressTerminalDeltas {
		span.recordTerminalDeltaEmitted()
		if haveBranchKey {
			span.recordTerminalDeltaEmittedForBranch(branchKey)
		}
	}
	if m.suppressTerminalDeltas {
		return
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
			for _, branchID := range row.terminalBranchIDs() {
				if key, ok := m.terminalBranchKey(terminalID, branchID); ok {
					counters.recordTerminalDeltaRemovedForBranch(key)
					counters.recordTerminalRowRemovedForBranch(key)
				}
			}
		}
	})
}

func (m *reteGraphBetaMemory) removeTerminalTokenContainingFact(terminalID reteGraphTerminalNodeID, branchID int, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || id.IsZero() {
		return
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return
	}
	tokens := m.resetRemovalTokens()
	terminal.rows.forEachTokenContainingFact(id, nil, func(row graphTokenRow) {
		if row.token.isZero() || !row.hasTerminalBranchSupport(branchID) {
			return
		}
		tokens = append(tokens, row.token)
	})
	m.removalTokenScratch = tokens
	for _, token := range tokens {
		m.removeTerminalToken(terminalID, branchID, token, counters, delta)
	}
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
		if key, ok := m.terminalBranchKey(terminalID, branchID); ok {
			counters.recordTerminalDeltaRemovedForBranch(key)
			counters.recordTerminalRowRemovedForBranch(key)
		}
	}
}

func (m *reteGraphBetaMemory) retainsTerminalToken(ruleRevisionID RuleRevisionID, token tokenRef) bool {
	if m == nil || m.graph == nil || token.isZero() {
		return false
	}
	if len(m.terminalsByRule) > 0 {
		for _, terminal := range m.terminalsByRule[ruleRevisionID] {
			if terminal != nil && terminal.rows.containsExactToken(token) {
				return true
			}
		}
		return false
	}
	for _, terminalNode := range m.graph.terminalNodes {
		if terminalNode.ruleRevisionID != ruleRevisionID {
			continue
		}
		terminal := m.terminalAt(terminalNode.id)
		if terminal != nil && terminal.rows.containsExactToken(token) {
			return true
		}
	}
	return false
}

func (m *reteGraphBetaMemory) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	defer m.pushEvalContext(ctx)()
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

func (m *reteGraphBetaMemory) queryRows(ctx context.Context, query compiledQuery, args map[string]Value, event reteGraphPropagationEvent, source Snapshot) ([]QueryRow, bool, error) {
	defer m.pushEvalContext(ctx)()
	if m == nil || m.graph == nil {
		return nil, false, nil
	}
	if event.tag != reteGraphPropagationAdd {
		return nil, true, ErrUnsupportedRuntime
	}
	trigger := event.fact
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
		m.queryArena = newTokenArenaWithoutFactSpans()
	} else {
		m.queryArena.reset()
	}
	if source.revision == nil {
		source.revision = m.revision
	}
	rowCapacity := m.queryTerminalRowCapacity(terminalIDs)
	valueRows := query.valueReturnsOnly()
	collector := reteGraphQueryCollector{
		ctx:          ctx,
		query:        query,
		args:         args,
		triggerEvent: event,
		source:       source,
		terminal:     make(map[reteGraphTerminalNodeID]struct{}, len(terminalIDs)),
		tokenArena:   m.queryArena,
		valueRows:    valueRows,
	}
	if !valueRows {
		collector.rowOwner = newQueryRowOwner(source)
	}
	if rowCapacity > 0 {
		collector.rows = make([]QueryRow, 0, min(rowCapacity, 256))
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
	if err := m.queryFlushAggregateBuckets(&collector); err != nil {
		return nil, true, err
	}
	return collector.rows, true, nil
}

const (
	queryProjectionChunkRows      = 128
	queryMixedProjectionChunkRows = 512
	queryFactProjectionChunkRows  = 512
)

func (m *reteGraphBetaMemory) queryTerminalRowCapacity(terminalIDs []reteGraphTerminalNodeID) int {
	if m == nil {
		return 0
	}
	capacity := 0
	for _, terminalID := range terminalIDs {
		terminal := m.terminal(terminalID)
		if terminal == nil {
			continue
		}
		capacity += terminal.rows.len()
	}
	return capacity
}

type reteGraphQueryCollector struct {
	ctx          context.Context
	query        compiledQuery
	args         map[string]Value
	triggerEvent reteGraphPropagationEvent
	source       Snapshot
	terminal     map[reteGraphTerminalNodeID]struct{}
	tokenArena   *tokenArena
	rows         []QueryRow
	rowItems     []queryRowValue
	rowValues    []Value
	rowOwner     *queryRowOwner
	valueRows    bool
	aggregates   map[reteGraphAggregateNodeID]map[graphTokenIdentityKey]*reteGraphAggregateBucket
}

func (m *reteGraphBetaMemory) queryPropagateAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, collector *reteGraphQueryCollector) error {
	if m == nil || collector == nil {
		return nil
	}
	captures, capturesOK := m.alphaListPatternCaptures(source, match)
	if !capturesOK {
		return fmt.Errorf("%w: malformed query list pattern captures", ErrQueryExecution)
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
		token := queryAlphaTokenRef(collector.tokenArena, entry, match, captures)
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
			token := queryAlphaTokenRef(collector.tokenArena, entry, match, captures)
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
			token := queryAlphaTokenRef(collector.tokenArena, successor.entry, edgeMatch, captures)
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
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		entry := sourceEntry
		if entry.conditionID == "" {
			return fmt.Errorf("%w: malformed query aggregate outer", ErrQueryExecution)
		}
		token := queryAlphaTokenRef(collector.tokenArena, entry, match, captures)
		if token.isZero() {
			return fmt.Errorf("%w: failed to create query aggregate outer token", ErrQueryExecution)
		}
		m.queryOpenAggregateBucket(aggregateID, token, collector)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		entry := sourceEntry
		if entry.conditionID == "" {
			return fmt.Errorf("%w: malformed query aggregate input", ErrQueryExecution)
		}
		token := queryAlphaTokenRef(collector.tokenArena, entry, match, captures)
		if token.isZero() {
			return fmt.Errorf("%w: failed to create query aggregate input token", ErrQueryExecution)
		}
		if err := m.queryInsertAggregateToken(aggregateID, token, collector); err != nil {
			return err
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
	for _, aggregateID := range m.graph.aggregateOuters[source] {
		m.queryOpenAggregateBucket(aggregateID, token, collector)
	}
	for _, aggregateID := range m.graph.aggregatesByStage[source] {
		if err := m.queryInsertAggregateToken(aggregateID, token, collector); err != nil {
			return err
		}
	}
	return nil
}

func queryAlphaTokenRef(arena *tokenArena, entry bindingTupleEntry, match conditionMatch, captures []listPatternCapture) tokenRef {
	if arena == nil {
		return tokenRef{}
	}
	token := arena.add(tokenRef{}, entry, conditionMatchForEntry(match, entry), match.fact.Recency(), match.fact.Generation())
	if token.isZero() {
		return tokenRef{}
	}
	for _, capture := range captures {
		captureEntry := bindingTupleEntry{
			binding:        capture.binding,
			bindingSlot:    capture.bindingSlot,
			conditionOrder: capture.bindingSlot,
			conditionID:    entry.conditionID,
			conditionPath:  cloneIntPath(entry.conditionPath),
		}
		captureMatch := conditionMatch{
			conditionID: entry.conditionID,
			bindingSlot: capture.bindingSlot,
			value:       cloneValue(capture.value),
			hasValue:    true,
		}
		token = arena.add(token, captureEntry, captureMatch, 0, match.fact.Generation())
		if token.isZero() {
			return tokenRef{}
		}
	}
	return token
}

func (m *reteGraphBetaMemory) queryOpenAggregateBucket(id reteGraphAggregateNodeID, parent tokenRef, collector *reteGraphQueryCollector) {
	if m == nil || collector == nil || parent.isZero() {
		return
	}
	collector.queryAggregateBucket(id, parent)
}

func (m *reteGraphBetaMemory) queryInsertAggregateToken(id reteGraphAggregateNodeID, token tokenRef, collector *reteGraphQueryCollector) error {
	if m == nil || collector == nil || token.isZero() {
		return nil
	}
	node := m.graph.aggregateNode(id)
	if node == nil {
		return fmt.Errorf("%w: malformed query aggregate node", ErrQueryExecution)
	}
	bucket := collector.queryAggregateBucket(id, m.aggregateParentToken(node, token))
	if bucket == nil {
		return fmt.Errorf("%w: malformed query aggregate bucket", ErrQueryExecution)
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		bucket.addCountOnlyMember(token)
		return nil
	}
	match, ok := tokenFactMatchForBindingSlot(token, node.inputEntry.bindingSlot)
	if !ok {
		return fmt.Errorf("%w: malformed query aggregate input token", ErrQueryExecution)
	}
	member, ok := m.aggregateMember(node, token, match)
	if !ok {
		return fmt.Errorf("%w: failed to evaluate query aggregate input", ErrQueryExecution)
	}
	memberKey := tokenRefKey(token)
	if existing, ok := bucket.members[memberKey]; ok {
		bucket.removeMember(node, existing)
	}
	if bucket.members == nil {
		bucket.members = make(map[graphTokenIdentityKey]reteGraphAggregateMember)
	}
	bucket.members[memberKey] = member
	if err := bucket.addMember(node, member); err != nil {
		return err
	}
	return nil
}

func (c *reteGraphQueryCollector) queryAggregateBucket(id reteGraphAggregateNodeID, parent tokenRef) *reteGraphAggregateBucket {
	if c == nil {
		return nil
	}
	if c.aggregates == nil {
		c.aggregates = make(map[reteGraphAggregateNodeID]map[graphTokenIdentityKey]*reteGraphAggregateBucket)
	}
	buckets := c.aggregates[id]
	if buckets == nil {
		buckets = make(map[graphTokenIdentityKey]*reteGraphAggregateBucket)
		c.aggregates[id] = buckets
	}
	key := tokenRefKey(parent)
	bucket := buckets[key]
	if bucket != nil {
		return bucket
	}
	bucket = &reteGraphAggregateBucket{parent: parent}
	buckets[key] = bucket
	return bucket
}

func (m *reteGraphBetaMemory) queryFlushAggregateBuckets(collector *reteGraphQueryCollector) error {
	if m == nil || collector == nil || len(collector.aggregates) == 0 {
		return nil
	}
	aggregateIDs := make([]reteGraphAggregateNodeID, 0, len(collector.aggregates))
	for id := range collector.aggregates {
		aggregateIDs = append(aggregateIDs, id)
	}
	slices.Sort(aggregateIDs)
	for _, aggregateID := range aggregateIDs {
		node := m.graph.aggregateNode(aggregateID)
		if node == nil {
			return fmt.Errorf("%w: malformed query aggregate node", ErrQueryExecution)
		}
		buckets := collector.aggregates[aggregateID]
		keys := make([]graphTokenIdentityKey, 0, len(buckets))
		for key := range buckets {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].generation != keys[j].generation {
				return keys[i].generation < keys[j].generation
			}
			if keys[i].size != keys[j].size {
				return keys[i].size < keys[j].size
			}
			return keys[i].identityState < keys[j].identityState
		})
		for _, key := range keys {
			bucket := buckets[key]
			values, ok := bucket.results(node)
			if !ok || len(values) != len(node.entries) {
				continue
			}
			token := bucket.parent
			generation := m.aggregateGeneration()
			if !token.isZero() {
				generation = token.generation()
			}
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
				token = collector.tokenArena.add(token, entry, match, 0, generation)
				if token.isZero() {
					return fmt.Errorf("%w: failed to create query aggregate output token", ErrQueryExecution)
				}
			}
			if err := m.queryPropagateFromStage(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregateID)}, token, collector); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *reteGraphBetaMemory) queryProbeBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, entry bindingTupleEntry, collector *reteGraphQueryCollector) error {
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return fmt.Errorf("%w: malformed query beta node", ErrQueryExecution)
	}
	if node.kind == reteGraphBetaNodeFilter {
		return m.queryProbeFilterBetaInput(nodeID, side, node, token, collector)
	}
	if node.kind == reteGraphBetaNodeNot {
		return m.queryProbeNegativeBetaInput(nodeID, side, node, token, collector)
	}
	nodeMemory := m.nodeMemory(nodeID)
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok, err := graphBetaJoinKeyForLeftTokenWithContext(collector.ctx, node, token, nil)
		if err != nil {
			return err
		}
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
			stage := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
			if collector.tokenOnlyTrigger(token) && m.queryStageTerminalOnly(stage, collector) {
				if err := m.queryCollectTerminalsFromStage(stage, rightRow.token, collector); err != nil {
					return err
				}
				continue
			}
			output := queryAppendTokenRows(collector.tokenArena, token, rightRow.token)
			if output.isZero() {
				return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
			}
			if err := m.queryPropagateFromStage(stage, output, collector); err != nil {
				return err
			}
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenFactMatchForBindingSlot(token, node.entry.bindingSlot)
		if !ok {
			return fmt.Errorf("%w: malformed query right token", ErrQueryExecution)
		}
		joinKey, ok, err := graphBetaJoinKeyForRightTokenWithContext(collector.ctx, node, token, nil)
		if err != nil {
			return err
		}
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
			stage := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
			if collector.tokenOnlyTrigger(token) && m.queryStageTerminalOnly(stage, collector) {
				if err := m.queryCollectTerminalsFromStage(stage, leftRow.token, collector); err != nil {
					return err
				}
				continue
			}
			output := queryAppendTokenRows(collector.tokenArena, leftRow.token, token)
			if output.isZero() {
				return fmt.Errorf("%w: failed to create query token", ErrQueryExecution)
			}
			if err := m.queryPropagateFromStage(stage, output, collector); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: malformed query beta side", ErrQueryExecution)
	}
	return nil
}

func (c *reteGraphQueryCollector) tokenOnlyTrigger(token tokenRef) bool {
	if c == nil || c.triggerEvent.tag != reteGraphPropagationAdd || c.triggerEvent.fact.ID().IsZero() {
		return false
	}
	if token.size() != 1 {
		return false
	}
	match, ok := token.matchAt(0)
	return ok && match.bindingSlot == 0 && match.fact.ID() == c.triggerEvent.fact.ID()
}

func (m *reteGraphBetaMemory) queryStageTerminalOnly(stage reteGraphStageRef, collector *reteGraphQueryCollector) bool {
	if m == nil || collector == nil || len(m.graph.successorsByStage[stage]) != 0 {
		return false
	}
	for _, terminal := range m.graph.terminalsByStage[stage] {
		if _, ok := collector.terminal[terminal.terminalID]; ok {
			return true
		}
	}
	return false
}

func (m *reteGraphBetaMemory) queryCollectTerminalsFromStage(stage reteGraphStageRef, token tokenRef, collector *reteGraphQueryCollector) error {
	if m == nil || collector == nil {
		return nil
	}
	for _, terminal := range m.graph.terminalsByStage[stage] {
		if _, ok := collector.terminal[terminal.terminalID]; !ok {
			continue
		}
		if err := m.queryCollectTerminalToken(token, collector); err != nil {
			return err
		}
	}
	return nil
}

func (m *reteGraphBetaMemory) queryProbeFilterBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, collector *reteGraphQueryCollector) error {
	if side != reteGraphBetaInputLeft {
		return nil
	}
	ok, err := m.filterTokenMatches(node, token, nil)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return m.queryPropagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, token, collector)
}

func (m *reteGraphBetaMemory) queryProbeNegativeBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, node *reteGraphBetaNode, token tokenRef, collector *reteGraphQueryCollector) error {
	if side != reteGraphBetaInputLeft {
		return nil
	}
	negativeMemory := m.negativeBetaMemory(nodeID, node)
	joinKey, ok, err := graphBetaJoinKeyForLeftTokenWithContext(collector.ctx, node, token, nil)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: malformed query negative join key", ErrQueryExecution)
	}
	count, ok := negativeMemory.blockerCountForLeft(joinKey, token, nil)
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
	var (
		row QueryRow
		err error
	)
	if collector.valueRows {
		row, err = collector.query.materializeTokenValueRowInto(collector.ctx, token, collector.args, 1, collector.nextRowValues())
	} else if collector.query.compactMixedReturns() {
		row, err = collector.query.materializeTokenCompactMixedRowInto(collector.ctx, token, collector.args, 1, collector.rowOwner, collector.nextRowItemsCount(collector.query.factReturnCount), collector.nextMixedRowValuesCount(collector.query.valueReturnCount))
	} else {
		row, err = collector.query.materializeTokenRowInto(collector.ctx, token, collector.args, 1, collector.rowOwner, collector.nextRowItems())
	}
	if err != nil {
		return err
	}
	collector.rows = append(collector.rows, row)
	return nil
}

func (c *reteGraphQueryCollector) nextRowValues() []Value {
	if c == nil {
		return nil
	}
	return c.nextRowValuesCount(len(c.query.returns))
}

func (c *reteGraphQueryCollector) nextRowValuesCount(count int) []Value {
	return c.nextRowValuesCountWithChunkRows(count, queryProjectionChunkRows)
}

func (c *reteGraphQueryCollector) nextMixedRowValuesCount(count int) []Value {
	return c.nextRowValuesCountWithChunkRows(count, queryMixedProjectionChunkRows)
}

func (c *reteGraphQueryCollector) nextRowValuesCountWithChunkRows(count int, chunkRows int) []Value {
	if c == nil || len(c.query.returns) == 0 {
		return nil
	}
	if count <= 0 {
		return nil
	}
	if len(c.rowValues)+count > cap(c.rowValues) {
		c.rowValues = make([]Value, 0, count*chunkRows)
	}
	start := len(c.rowValues)
	end := start + count
	c.rowValues = c.rowValues[:end]
	return c.rowValues[start:end]
}

func (c *reteGraphQueryCollector) nextRowItems() []queryRowValue {
	if c == nil {
		return nil
	}
	return c.nextRowItemsCount(len(c.query.returns))
}

func (c *reteGraphQueryCollector) nextRowItemsCount(count int) []queryRowValue {
	if c == nil || len(c.query.returns) == 0 {
		return nil
	}
	if count <= 0 {
		return nil
	}
	if len(c.rowItems)+count > cap(c.rowItems) {
		c.rowItems = make([]queryRowValue, 0, count*queryMixedProjectionChunkRows)
	}
	start := len(c.rowItems)
	end := start + count
	c.rowItems = c.rowItems[:end]
	return c.rowItems[start:end]
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
		m.arena = newTokenArenaWithoutFactSpans()
	}
	return m.arena.add(parent, entry, match, recency, generation)
}

func (m *reteGraphBetaMemory) newRootTokenRef(generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArenaWithoutFactSpans()
	}
	return m.arena.addSeed(generation)
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
	node := m.terminalNode(id)
	if node == nil {
		return ""
	}
	return node.ruleRevisionID
}

func (m *reteGraphBetaMemory) terminalBranchKey(id reteGraphTerminalNodeID, branchID int) (propagationBranchKey, bool) {
	node := m.terminalNode(id)
	if node == nil || branchID < 0 {
		return propagationBranchKey{}, false
	}
	var ownerKind reteGraphBranchOwnerKind
	switch node.kind {
	case reteGraphTerminalRule:
		ownerKind = reteGraphBranchOwnerRule
	case reteGraphTerminalQuery:
		ownerKind = reteGraphBranchOwnerQuery
	default:
		return propagationBranchKey{}, false
	}
	return propagationBranchKey{
		ownerKind:      ownerKind,
		ruleRevisionID: node.ruleRevisionID,
		queryName:      node.queryName,
		terminalID:     id,
		branchID:       branchID,
	}, true
}

func (m *reteGraphBetaMemory) terminalNode(id reteGraphTerminalNodeID) *reteGraphTerminalNode {
	if m == nil || m.graph == nil || id <= 0 {
		return nil
	}
	index := int(id) - 1
	if index < 0 || index >= len(m.graph.terminalNodes) {
		return nil
	}
	return &m.graph.terminalNodes[index]
}

func tokenRefKey(token tokenRef) graphTokenIdentityKey {
	return graphTokenIdentityKey{
		size:          token.size(),
		generation:    token.generation(),
		identityState: token.identityState(),
	}
}

func tokenRefsSameIdentity(left, right tokenRef) bool {
	return tokenRefKey(left) == tokenRefKey(right)
}

func tokenLastMatch(token tokenRef) (conditionMatch, bool) {
	row, ok := token.resolve()
	if !ok {
		return conditionMatch{}, false
	}
	return row.match, true
}

func tokenFactMatchForBindingSlot(token tokenRef, bindingSlot int) (conditionMatch, bool) {
	if bindingSlot >= 0 {
		if match, ok := tokenRefAtSlot(token, bindingSlot); ok && !match.hasValue {
			return match, true
		}
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return conditionMatch{}, false
		}
		if !row.match.hasValue {
			return row.match, true
		}
	}
	return conditionMatch{}, false
}

func (m *reteGraphBetaMemory) appendTokenRows(parent tokenRef, token tokenRef, span *propagationCounterSpan) tokenRef {
	if m == nil || token.isZero() {
		return parent
	}
	row, ok := token.resolve()
	if !ok {
		return tokenRef{}
	}
	parent = m.appendTokenRows(parent, token.parent(), span)
	if parent.isZero() && !token.parent().isZero() {
		return tokenRef{}
	}
	recency := Recency(0)
	if !row.match.hasValue {
		recency = row.match.fact.Recency()
	}
	return m.newTokenRef(parent, row.entry, row.match, recency, row.generation, span)
}

func queryAppendTokenRows(arena *tokenArena, parent tokenRef, token tokenRef) tokenRef {
	if arena == nil || token.isZero() {
		return parent
	}
	row, ok := token.resolve()
	if !ok {
		return tokenRef{}
	}
	parent = queryAppendTokenRows(arena, parent, token.parent())
	if parent.isZero() && !token.parent().isZero() {
		return tokenRef{}
	}
	recency := Recency(0)
	if !row.match.hasValue {
		recency = row.match.fact.Recency()
	}
	return arena.add(parent, row.entry, row.match, recency, row.generation)
}

func conditionMatchForEntry(match conditionMatch, entry bindingTupleEntry) conditionMatch {
	match.conditionID = entry.conditionID
	match.bindingSlot = entry.bindingSlot
	return match
}

func tokenConditionMatches(token tokenRef) ([]conditionMatch, bool) {
	row, ok := token.resolve()
	if !ok {
		return nil, false
	}
	matches := make([]conditionMatch, row.size)
	seen := make([]bool, row.size)
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return nil, false
		}
		slot := row.match.bindingSlot
		if slot < 0 || slot >= len(matches) || seen[slot] {
			return nil, false
		}
		matches[slot] = row.match
		seen[slot] = true
	}
	for _, ok := range seen {
		if !ok {
			return nil, false
		}
	}
	return matches, true
}

func graphBetaJoinKeyForLeftToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	key, ok, _ := graphBetaJoinKeyForLeftTokenWithContext(context.Background(), node, token, nil)
	return key, ok
}

func graphBetaJoinKeyForLeftTokenWithContext(ctx context.Context, node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan) (betaJoinKey, bool, error) {
	if node == nil {
		return betaJoinKey{}, false, nil
	}
	if node.rightHasLeftPrefix {
		key, ok := betaJoinKeyForTokenIdentity(token)
		return key, ok, nil
	}
	if len(node.hashJoins) == 0 {
		return betaJoinKey{}, true, nil
	}
	key, ok, err := betaJoinKeyForPlanWithError(compiledConditionPlan{joins: node.hashJoins}, func(join compiledJoinConstraint) (Value, bool, error) {
		if join.hasRightKeyExpression {
			value, ok, err := join.rightKeyExpression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, conditionFactRef{}, token, nil, 0, joinFunctionEvaluationMeta(join), span)
			return value, ok, err
		}
		match, ok := tokenRefAtSlot(token, join.refBindingSlot)
		if !ok {
			return Value{}, false, nil
		}
		value, ok := join.rightValueFromFact(match.fact)
		return value, ok, nil
	})
	return key, ok, err
}

func graphBetaJoinKeyForRightToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	key, ok, _ := graphBetaJoinKeyForRightTokenWithContext(context.Background(), node, token, nil)
	return key, ok
}

func graphBetaJoinKeyForRightTokenWithContext(ctx context.Context, node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan) (betaJoinKey, bool, error) {
	if node == nil {
		return betaJoinKey{}, false, nil
	}
	if node.rightHasLeftPrefix {
		prefix := tokenRefPrefix(token, node.rightPrefixWidth)
		key, ok := betaJoinKeyForTokenIdentity(prefix)
		return key, ok, nil
	}
	match, ok := tokenLastMatch(token)
	if !ok {
		return betaJoinKey{}, false, nil
	}
	if len(node.hashJoins) == 0 {
		return betaJoinKey{}, true, nil
	}
	key, ok, err := betaJoinKeyForPlanWithError(compiledConditionPlan{joins: node.hashJoins}, func(join compiledJoinConstraint) (Value, bool, error) {
		if join.hasLeftKeyExpression {
			value, ok, err := join.leftKeyExpression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, match.fact, token, nil, 0, joinFunctionEvaluationMeta(join), span)
			return value, ok, err
		}
		value, ok := join.leftValueFromFact(match.fact)
		return value, ok, nil
	})
	return key, ok, err
}

func joinFunctionEvaluationMeta(join compiledJoinConstraint) *FunctionEvaluationError {
	meta := &FunctionEvaluationError{
		ConditionIndex: -1,
		PredicateIndex: -1,
	}
	if len(join.path) > 0 {
		meta.ConditionIndex = join.path[0]
	}
	if len(join.path) > 1 {
		meta.PredicateIndex = join.path[1]
	}
	return meta
}

func (m *reteGraphBetaMemory) residualJoinsMatch(node *reteGraphBetaNode, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	if m == nil || node == nil {
		return true, nil
	}
	for _, join := range node.residualJoins {
		if span != nil {
			span.recordBetaResidualTest()
		}
		ok, err := join.matchesTokenWithCounters(fact, bindings, span)
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
	ok, err := expressionPredicatesMatchTokenWithContext(m.context(), node.predicates, fact, bindings, span)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return true, nil
}

func (m *reteGraphBetaMemory) filterTokenMatches(node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan) (bool, error) {
	if m == nil || node == nil {
		return true, nil
	}
	ok, err := expressionPredicatesMatchTokenWithContext(m.context(), node.predicates, conditionFactRef{}, token, span)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (m *reteGraphBetaMemory) rightPredicatesMatch(node *reteGraphBetaNode, right conditionMatch, left tokenRef, span *propagationCounterSpan) (bool, error) {
	if m == nil || node == nil || len(node.rightPredicates) == 0 {
		return true, nil
	}
	size := max(left.size(), right.bindingSlot+1)
	if size <= 0 {
		return false, nil
	}
	if cap(m.rightPredicateScratch) < size {
		m.rightPredicateScratch = make([]conditionMatch, size)
	}
	bindings := m.rightPredicateScratch[:size]
	for i := range bindings {
		bindings[i] = conditionMatch{}
	}
	for current := left; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return false, nil
		}
		slot := row.match.bindingSlot
		if slot >= 0 && slot < len(bindings) {
			bindings[slot] = row.match
		}
	}
	if right.bindingSlot < 0 || right.bindingSlot >= len(bindings) {
		return false, nil
	}
	bindings[right.bindingSlot] = right
	for _, predicate := range node.rightPredicates {
		if span != nil {
			span.recordExpressionPredicateTest()
		}
		value, ok, err := predicate.expression.evaluateWithContextParamsAndCounters(m.context(), right.fact, bindings, nil, predicate.functionEvaluationMeta(), span)
		if err != nil {
			if span != nil {
				span.recordExpressionPredicateError()
			}
			return false, err
		}
		matched, ok := value.AsBool()
		if !ok || !matched {
			if span != nil {
				span.recordExpressionPredicateFailure()
			}
			return false, nil
		}
	}
	return true, nil
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

func (m *reteGraphBetaMemory) terminalRowsRetainedByBranch() map[propagationBranchKey]int {
	if m == nil || m.graph == nil {
		return nil
	}
	retained := make(map[propagationBranchKey]int)
	for _, terminalNode := range m.graph.terminalNodes {
		terminal := m.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		for _, row := range terminal.rows.rows {
			if row.token.isZero() {
				continue
			}
			row.forEachTerminalBranchSupport(func(support terminalBranchSupport) {
				if support.count <= 0 {
					return
				}
				key, ok := m.terminalBranchKey(terminalNode.id, support.branchID)
				if !ok {
					return
				}
				retained[key]++
			})
		}
	}
	if len(retained) == 0 {
		return nil
	}
	return retained
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

func (m *reteGraphBetaMemory) diagnostics() reteGraphBetaMemoryDiagnostics {
	if m == nil || m.graph == nil {
		return reteGraphBetaMemoryDiagnostics{}
	}
	out := reteGraphBetaMemoryDiagnostics{
		BetaNodes: make([]reteGraphBetaNodeMemoryDiagnostics, 0, len(m.graph.betaNodes)),
		Terminals: make([]reteGraphTerminalMemoryDiagnostics, 0, len(m.graph.terminalNodes)),
	}
	for _, node := range m.graph.betaNodes {
		memory := m.betaNodeMemoryAt(node.id)
		var left, right reteGraphTokenMemoryDiagnostics
		if memory != nil {
			left = memory.left.diagnostics()
			right = memory.right.diagnostics()
		}
		diag := reteGraphBetaNodeMemoryDiagnostics{
			ID:                   node.id,
			Kind:                 node.kind,
			Left:                 node.left,
			Right:                node.right,
			TokenWidth:           m.graph.stageTokenWidth(reteGraphStageRef{kind: reteGraphStageBeta, id: int(node.id)}),
			LeftRows:             left.Rows,
			RightRows:            right.Rows,
			TotalRows:            left.Rows + right.Rows,
			LeftJoinIndexKeys:    left.JoinIndexKeys,
			RightJoinIndexKeys:   right.JoinIndexKeys,
			TotalJoinIndexKeys:   left.JoinIndexKeys + right.JoinIndexKeys,
			LeftJoinBucketDepth:  left.JoinBucketDepthMax,
			RightJoinBucketDepth: right.JoinBucketDepthMax,
			MaxJoinBucketDepth:   max(left.JoinBucketDepthMax, right.JoinBucketDepthMax),
			LeftJoinBucketTotal:  left.JoinBucketDepthTotal,
			RightJoinBucketTotal: right.JoinBucketDepthTotal,
			TotalJoinBucketDepth: left.JoinBucketDepthTotal + right.JoinBucketDepthTotal,
			IdentityIndexKeys:    left.IdentityIndexKeys + right.IdentityIndexKeys,
			FactIndexKeys:        left.FactIndexKeys + right.FactIndexKeys,
		}
		if diag.TotalRows > 0 {
			out.WidestRetainedBetaTokenWidth = max(out.WidestRetainedBetaTokenWidth, diag.TokenWidth)
		}
		out.MaxBetaRows = max(out.MaxBetaRows, diag.TotalRows)
		out.MaxBetaLeftRows = max(out.MaxBetaLeftRows, diag.LeftRows)
		out.MaxBetaRightRows = max(out.MaxBetaRightRows, diag.RightRows)
		out.MaxBetaJoinIndexKeys = max(out.MaxBetaJoinIndexKeys, diag.TotalJoinIndexKeys)
		out.MaxBetaJoinBucketDepth = max(out.MaxBetaJoinBucketDepth, diag.MaxJoinBucketDepth)
		out.BetaNodes = append(out.BetaNodes, diag)
	}
	for _, node := range m.graph.terminalNodes {
		terminal := m.terminalAt(node.id)
		diag := reteGraphTerminalMemoryDiagnostics{
			ID:             node.id,
			Kind:           node.kind,
			RuleRevisionID: node.ruleRevisionID,
			QueryName:      node.queryName,
			Input:          node.input,
			TokenWidth:     m.graph.stageTokenWidth(node.input),
			BranchRows:     make(map[int]int),
		}
		if terminal != nil {
			diag.Rows = len(terminal.rows.rows)
			for _, row := range terminal.rows.rows {
				if row.token.isZero() {
					continue
				}
				row.forEachTerminalBranchSupport(func(support terminalBranchSupport) {
					if support.count <= 0 {
						return
					}
					diag.BranchRows[support.branchID]++
				})
			}
		}
		if len(diag.BranchRows) == 0 {
			diag.BranchRows = nil
		}
		out.MaxTerminalRows = max(out.MaxTerminalRows, diag.Rows)
		for _, rows := range diag.BranchRows {
			out.TotalTerminalBranchRows += rows
			out.MaxTerminalBranchRows = max(out.MaxTerminalBranchRows, rows)
		}
		out.Terminals = append(out.Terminals, diag)
	}
	return out
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

	factKeys := memory.factIndexKeyCount()
	s.FactIndexKeys += factKeys
	s.FactIndexReserve += memory.factIndexReserve
	s.FactIndexKeysMax = max(s.FactIndexKeysMax, factKeys)
	s.FactIndexReserveMax = max(s.FactIndexReserveMax, memory.factIndexReserve)
}

func (m *reteGraphBetaMemory) betaNodeMemoryAt(id reteGraphBetaNodeID) *reteGraphBetaNodeMemory {
	if m == nil || id <= 0 {
		return nil
	}
	index := int(id)
	if index < 0 || index >= len(m.nodes) {
		return nil
	}
	return m.nodes[index]
}

func (m tokenHashMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	diag := reteGraphTokenMemoryDiagnostics{
		Rows:              len(m.rows),
		JoinIndexKeys:     len(m.indexes),
		IdentityIndexKeys: len(m.identityRows),
		FactIndexKeys:     m.factIndexKeyCount(),
	}
	for _, bucket := range m.indexes {
		depth := bucket.len()
		diag.JoinBucketDepthTotal += depth
		diag.JoinBucketDepthMax = max(diag.JoinBucketDepthMax, depth)
	}
	return diag
}

func (m tokenHashMemory) factIndexKeyCount() int {
	if !m.factRowsDirty {
		return len(m.factRows)
	}
	if len(m.rows) == 0 {
		return 0
	}
	seen := make(map[FactID]struct{}, len(m.rows))
	for _, row := range m.rows {
		if row.token.isZero() {
			continue
		}
		if factIDs, ok := row.token.factIDs(); ok {
			for i, id := range factIDs {
				if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
					continue
				}
				seen[id] = struct{}{}
			}
			continue
		}
		for current := row.token; !current.isZero(); current = current.parent() {
			tokenRow, ok := current.resolve()
			if !ok {
				break
			}
			id := tokenRow.match.fact.ID()
			if id.IsZero() || current.parent().containsFact(id) {
				continue
			}
			seen[id] = struct{}{}
		}
	}
	return len(seen)
}

func (m *reteGraphBetaMemory) match(ctx context.Context, source factSource) ([]ruleMatchResult, error) {
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
	results, err := m.match(ctx, nil)
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
	m.ensureFactTargetIndexes()
	switch target.kind {
	case conditionTargetName:
		return m.factsByName[target.name], true
	case conditionTargetTemplateKey:
		return m.factsByTemplate[target.templateKey], true
	default:
		return nil, false
	}
}

func (m *reteGraphBetaMemory) factsForTargetFieldEqual(target conditionTarget, fieldSlot int, value reteGraphAlphaRouteValue) ([]FactSnapshot, bool) {
	if m == nil {
		return nil, false
	}
	key := newFactFieldEqualKey(target, fieldSlot, value)
	if target.kind == conditionTargetTemplateKey {
		m.ensureFactFieldEqualIndexes()
		if indexed, ok := m.factFieldEqualIndexes[key]; ok {
			return indexed, true
		}
	}
	facts, ok := m.factsForTarget(target)
	if !ok {
		return nil, false
	}
	if indexed, ok := m.factFieldEqualIndexes[key]; ok {
		return indexed, true
	}
	indexed := make([]FactSnapshot, 0)
	for _, fact := range facts {
		if factSnapshotMatchesFieldEqualIndex(fact, fieldSlot, value) {
			indexed = append(indexed, fact)
		}
	}
	if m.factFieldEqualIndexes == nil {
		m.factFieldEqualIndexes = make(map[factFieldEqualKey][]FactSnapshot)
	}
	m.factFieldEqualIndexes[key] = indexed
	return indexed, true
}

func (m *reteGraphBetaMemory) ensureFactFieldEqualIndexes() {
	if m == nil || (!m.factFieldIndexesDirty && m.factFieldEqualIndexes != nil) {
		return
	}
	if m.factFieldEqualIndexes == nil {
		m.factFieldEqualIndexes = make(map[factFieldEqualKey][]FactSnapshot)
	} else {
		clear(m.factFieldEqualIndexes)
	}
	m.initializeFactFieldEqualIndexKeys()
	for _, fact := range m.facts {
		m.addFactFieldEqualIndexes(fact)
	}
	m.factFieldIndexesDirty = false
}

func (m *reteGraphBetaMemory) ensureFactTargetIndexes() {
	if m == nil || !m.factTargetIndexesDirty {
		return
	}
	m.rebuildFactTargetIndexes()
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
