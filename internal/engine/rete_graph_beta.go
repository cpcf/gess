package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

type reteGraphBetaMemory struct {
	revision                 *Ruleset
	graph                    *reteGraph
	compactSlotStore         *factCompactSlotStore
	evalCtx                  context.Context
	nodes                    []*reteGraphBetaNodeMemory
	aggregates               []*reteGraphAggregateNodeMemory
	terminals                []*reteGraphTerminalMemory
	terminalsByRule          map[RuleRevisionID][]*reteGraphTerminalMemory
	alpha                    reteGraphAlphaMemory
	sourceGenerationValue    Generation
	arena                    *tokenArena
	terminalTokenDeltas      []reteTerminalTokenDelta
	terminalRemovedDeltas    []reteTerminalTokenDelta
	initialAgenda            *agenda
	initialAgendaErr         error
	backchainDemandRecords   []backchainDemandRecord
	backchainDemandSlots     []factSlot
	backchainDemandSupport   []backchainDemandSupportFact
	backchainDemandDeltaIDs  []backchainDemandID
	backchainDemandOwners    []backchainDemandOwnerKey
	nextBackchainDemandID    backchainDemandID
	alphaRouteScratch        []reteGraphAlphaNodeID
	alphaRouteSeen           map[reteGraphAlphaNodeID]uint64
	alphaRouteEpoch          uint64
	removalTokenScratch      []tokenRef
	rootToken                tokenRef
	deferAggregateOutputs    bool
	deferredAggregateOutputs map[deferredAggregateOutputKey]struct{}
	deferredAggregateOrder   []deferredAggregateOutputKey
	suppressTerminalDeltas   bool
	rightPredicateScratch    []conditionMatch
	tokenRefreshCache        map[tokenHandle]tokenRef
	modifyRouteScope         reteModifyRouteScope
}

type reteModifyRouteScope struct {
	stageQueue     []reteGraphStageRef
	stages         []reteGraphStageRef
	betaNodes      []reteGraphBetaNodeID
	aggregateNodes []reteGraphAggregateNodeID
	terminalNodes  []reteGraphTerminalNodeID
}

type reteGraphBetaNodeMemory struct {
	left     betaSideMemory
	right    betaSideMemory
	negative reteGraphNegativeBetaNodeMemory
}

type reteGraphAlphaMemory struct {
	facts               []reteGraphAlphaFactSet
	conditions          [][]ConditionID
	factOwnership       map[FactID]alphaFactOwnershipRow
	factOwnershipIDs    []FactID
	factRouteStorage    []reteGraphAlphaNodeID
	factTerminalStorage []generatedTerminalRowHandle
	factCounts          map[ConditionID]int
}

type reteGraphAggregateNodeMemory struct {
	buckets reteGraphAggregateBucketTable
	numeric reteGraphAggregateNumericState
}

type reteGraphAggregateBucketID int

type reteGraphAggregateBucketTable struct {
	rows []reteGraphAggregateBucket
	ids  map[graphTokenIdentityKey]reteGraphAggregateBucketID
	free []reteGraphAggregateBucketID
	live int
}

type reteGraphAggregateBucket struct {
	id              reteGraphAggregateBucketID
	parent          tokenRef
	members         map[graphTokenIdentityKey]reteGraphAggregateMember
	countOnlyFirst  tokenRef
	countOnlySecond tokenRef
	countOnlyRest   []tokenRef
	count           int64
	extrema         []reteGraphAggregateExtremum
	collects        [][]reteGraphAggregateCollectEntry
	token           tokenRef
	values          []Value
	hasValue        bool
}

type reteGraphAggregateNumericState struct {
	specCount int
	intSums   []int64
	floatSums []float64
	floaty    []bool
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
	token  tokenRef
	factID FactID
}

type deferredAggregateOutputKey struct {
	id     reteGraphAggregateNodeID
	parent graphTokenIdentityKey
}

type reteGraphTerminalMemory struct {
	rows                  terminalTokenMemory
	queryRows             queryTerminalMemory
	kind                  reteGraphTerminalKind
	ruleRevisionID        RuleRevisionID
	rule                  compiledRule
	ruleOK                bool
	ruleConditionCount    int
	ruleIdentityScopeHash uint64
	branchCount           int
	singleBranchID        int
}

func (m *reteGraphTerminalMemory) singleBranch() bool {
	return m != nil && m.branchCount == 1
}

func (m *reteGraphTerminalMemory) needsBranchSupport() bool {
	return m != nil && m.branchCount > 1
}

type reteGraphAlphaFactSet struct {
	inline   [4]FactID
	overflow []FactID
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

type betaSideMemory struct {
	indexes          betaJoinBucketTable
	rowReserve       int
	joinIndexReserve int
}

type graphTokenRowID int

type graphTokenRowHandleID uint32

type graphTokenRowHandle struct {
	id         graphTokenRowHandleID
	generation uint32
}

func (h graphTokenRowHandle) isZero() bool {
	return h.id == 0
}

func (h graphTokenRowHandle) index() int {
	if h.id == 0 {
		return -1
	}
	return int(h.id - 1)
}

type graphTokenRowHandleEntry struct {
	rowID      graphTokenRowID
	generation uint32
	live       bool
}

type graphTokenRow struct {
	handle         graphTokenRowHandle
	token          tokenRef
	joinKey        betaJoinKey
	identity       graphTokenIdentityKey
	identityNext   graphTokenRowID
	candidate      candidateIdentity
	supportCount   int
	branchSupport  terminalBranchSupport
	branchOverflow *terminalBranchSupportOverflow
	branchCount    int
}

type betaTokenRow struct {
	token   tokenRef
	joinKey betaJoinKey
}

type generatedTerminalRowHandle struct {
	alphaNodeID reteGraphAlphaNodeID
	terminalID  reteGraphTerminalNodeID
	branchID    int
	handle      graphTokenRowHandle
}

type alphaFactOwnershipRow struct {
	routes       []reteGraphAlphaNodeID
	terminalRows []generatedTerminalRowHandle
}

type terminalBranchSupport struct {
	branchID int
	count    int
}

type terminalBranchSupportOverflow struct {
	items []terminalBranchSupport
}

func (r graphTokenRow) terminalBranchOverflowItems() []terminalBranchSupport {
	if r.branchOverflow == nil {
		return nil
	}
	return r.branchOverflow.items
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
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		if overflow[i].branchID == branchID {
			overflow[i].count++
			return
		}
	}
	if r.branchOverflow == nil {
		r.branchOverflow = &terminalBranchSupportOverflow{}
	}
	r.branchOverflow.items = append(r.branchOverflow.items, terminalBranchSupport{branchID: branchID, count: 1})
	r.branchCount++
}

func (r graphTokenRow) hasTerminalBranchSupport(branchID int) bool {
	if branchID < 0 || r.branchCount == 0 {
		return false
	}
	if r.branchSupport.branchID == branchID && r.branchSupport.count > 0 {
		return true
	}
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		support := overflow[i]
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
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		if overflow[i].branchID != branchID {
			continue
		}
		overflow[i].count--
		if overflow[i].count > 0 {
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
	overflow := r.terminalBranchOverflowItems()
	overflowCount := min(r.branchCount-1, len(overflow))
	if r.branchCount == 1 || overflowCount == 0 {
		r.branchSupport = terminalBranchSupport{}
		r.branchCount = 0
		r.branchOverflow = nil
		return
	}
	if index > overflowCount {
		return
	}
	lastOverflow := overflowCount - 1
	last := overflow[lastOverflow]
	overflow[lastOverflow] = terminalBranchSupport{}
	overflow = overflow[:lastOverflow]
	if index == 0 {
		r.branchSupport = last
	} else if index-1 < len(overflow) {
		overflow[index-1] = last
	}
	r.branchCount = overflowCount
	if len(overflow) == 0 {
		r.branchOverflow = nil
	} else {
		r.branchOverflow.items = overflow
	}
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
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		fn(overflow[i])
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
	memory, _, err := newReteGraphBetaMemoryForGenerationWithDelta(ctx, revision, graph, facts, generation)
	return memory, err
}

func newReteGraphBetaMemoryForGenerationWithDelta(ctx context.Context, revision *Ruleset, graph *reteGraph, facts []FactSnapshot, generation Generation) (*reteGraphBetaMemory, reteAgendaDelta, error) {
	memory := newEmptyReteGraphBetaMemory(revision, graph, len(facts))
	if memory == nil {
		return nil, reteAgendaDelta{supported: true}, nil
	}
	delta, err := memory.resetFactsForGenerationWithDelta(ctx, facts, generation)
	if err != nil {
		return nil, delta, err
	}
	return memory, delta, nil
}

func newReteGraphBetaMemoryForGenerationWithInitialAgenda(ctx context.Context, revision *Ruleset, graph *reteGraph, facts []FactSnapshot, generation Generation, agenda *agenda) (*reteGraphBetaMemory, reteAgendaDelta, error) {
	memory := newEmptyReteGraphBetaMemory(revision, graph, len(facts))
	if memory == nil {
		return nil, reteAgendaDelta{supported: true}, nil
	}
	delta, err := memory.resetFactsForGenerationWithInitialAgenda(ctx, facts, generation, agenda)
	if err != nil {
		return nil, delta, err
	}
	return memory, delta, nil
}

func newReteGraphBetaMemoryForWorkspaceWithDelta(ctx context.Context, revision *Ruleset, graph *reteGraph, facts *factWorkspace, generation Generation) (*reteGraphBetaMemory, reteAgendaDelta, error) {
	memory := newEmptyReteGraphBetaMemory(revision, graph, facts.factCount())
	if memory == nil {
		return nil, reteAgendaDelta{supported: true}, nil
	}
	delta, err := memory.resetFactWorkspaceForGenerationWithDelta(ctx, facts, generation)
	if err != nil {
		return nil, delta, err
	}
	return memory, delta, nil
}

func newReteGraphBetaMemoryForWorkspaceWithInitialAgenda(ctx context.Context, revision *Ruleset, graph *reteGraph, facts *factWorkspace, generation Generation, agenda *agenda) (*reteGraphBetaMemory, reteAgendaDelta, error) {
	memory := newEmptyReteGraphBetaMemory(revision, graph, facts.factCount())
	if memory == nil {
		return nil, reteAgendaDelta{supported: true}, nil
	}
	delta, err := memory.resetFactWorkspaceForGenerationWithInitialAgenda(ctx, facts, generation, agenda)
	if err != nil {
		return nil, delta, err
	}
	return memory, delta, nil
}

func newEmptyReteGraphBetaMemory(revision *Ruleset, graph *reteGraph, factCount int) *reteGraphBetaMemory {
	if revision == nil || graph == nil {
		return nil
	}
	memory := &reteGraphBetaMemory{
		revision:   revision,
		graph:      graph,
		nodes:      make([]*reteGraphBetaNodeMemory, len(graph.betaNodes)+1),
		aggregates: make([]*reteGraphAggregateNodeMemory, len(graph.aggregateNodes)+1),
		terminals:  make([]*reteGraphTerminalMemory, len(graph.terminalNodes)+1),
		arena:      newTokenArena(),
	}
	memory.indexRuleTerminals()
	memory.reserveAlphaFacts(0)
	return memory
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

func (m *reteGraphBetaMemory) initializeTerminalMemory(id reteGraphTerminalNodeID, terminal *reteGraphTerminalMemory) {
	if m == nil || terminal == nil {
		return
	}
	node := m.terminalNode(id)
	if node == nil {
		return
	}
	terminal.kind = node.kind
	terminal.ruleRevisionID = node.ruleRevisionID
	terminal.branchCount = node.branchCount
	terminal.singleBranchID = node.singleBranchID
	if node.kind != reteGraphTerminalRule || node.ruleRevisionID == "" || m.revision == nil {
		return
	}
	rule, ok := m.revision.rulesByRevisionID[node.ruleRevisionID]
	if !ok {
		return
	}
	terminal.rule = rule
	terminal.ruleOK = true
	terminal.ruleConditionCount = len(rule.conditions)
	terminal.ruleIdentityScopeHash = rule.identityScopeHash
	if terminal.ruleIdentityScopeHash == 0 {
		terminal.ruleIdentityScopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
	}
}

func (m *reteGraphBetaMemory) reserveAlphaFacts(factCapacity int) {
	if m == nil || m.graph == nil {
		return
	}
	size := len(m.graph.alphaNodes) + 1
	if cap(m.alpha.facts) < size {
		m.alpha.facts = make([]reteGraphAlphaFactSet, size)
	} else {
		m.alpha.facts = m.alpha.facts[:size]
	}
	if factCapacity > 0 {
		for i := 1; i < size; i++ {
			m.alpha.facts[i].reserve(factCapacity)
		}
	}
	m.alpha.conditions = make([][]ConditionID, size)
	for _, node := range m.graph.alphaNodes {
		index := int(node.id)
		if index <= 0 || index >= size {
			continue
		}
		for _, consumer := range node.consumers {
			m.appendAlphaCondition(index, consumer.conditionID)
		}
		if len(m.alpha.conditions[index]) == 0 && node.entry.conditionID != "" {
			m.appendAlphaCondition(index, node.entry.conditionID)
		}
	}
	conditionCount := 0
	for _, conditions := range m.alpha.conditions {
		conditionCount += len(conditions)
	}
	if m.alpha.factCounts == nil {
		m.alpha.factCounts = make(map[ConditionID]int, conditionCount)
	} else {
		clear(m.alpha.factCounts)
	}
	if m.alpha.factOwnership == nil {
		m.alpha.factOwnership = make(map[FactID]alphaFactOwnershipRow, factCapacity)
	} else {
		clear(m.alpha.factOwnership)
	}
	if factCapacity > cap(m.alpha.factOwnershipIDs) {
		m.alpha.factOwnershipIDs = make([]FactID, 0, factCapacity)
	} else {
		m.alpha.factOwnershipIDs = m.alpha.factOwnershipIDs[:0]
	}
	if factCapacity > cap(m.alpha.factTerminalStorage) {
		m.alpha.factTerminalStorage = make([]generatedTerminalRowHandle, 0, factCapacity)
	} else {
		clear(m.alpha.factTerminalStorage)
		m.alpha.factTerminalStorage = m.alpha.factTerminalStorage[:0]
	}
}

func (m *reteGraphBetaMemory) appendAlphaCondition(index int, conditionID ConditionID) {
	if m == nil || conditionID == "" || index <= 0 || index >= len(m.alpha.conditions) {
		return
	}
	if slices.Contains(m.alpha.conditions[index], conditionID) {
		return
	}
	m.alpha.conditions[index] = append(m.alpha.conditions[index], conditionID)
}

func (m *betaSideMemory) clear() {
	if m == nil {
		return
	}
	m.indexes.clear()
}

func (m *betaSideMemory) len() int {
	if m == nil {
		return 0
	}
	return m.indexes.len()
}

func (m *betaSideMemory) rowCapacity() int {
	if m == nil {
		return 0
	}
	capacity := 0
	for bucketIndex := range m.indexes.buckets {
		capacity += cap(m.indexes.buckets[bucketIndex].rows)
	}
	return capacity
}

func (s *reteGraphAlphaFactSet) reserve(capacity int) {
}

func (s *reteGraphAlphaFactSet) insert(id FactID) bool {
	if s == nil || id.IsZero() {
		return false
	}
	if s.contains(id) {
		return false
	}
	for i, existing := range s.inline {
		if existing.IsZero() {
			s.inline[i] = id
			return true
		}
	}
	s.overflow = append(s.overflow, id)
	return true
}

func (s *reteGraphAlphaFactSet) remove(id FactID) bool {
	if s == nil || id.IsZero() {
		return false
	}
	for i, existing := range s.inline {
		if existing != id {
			continue
		}
		s.inline[i] = FactID{}
		if len(s.overflow) > 0 {
			last := len(s.overflow) - 1
			s.inline[i] = s.overflow[last]
			s.overflow[last] = FactID{}
			s.overflow = s.overflow[:last]
		}
		return true
	}
	if s.overflow == nil {
		return false
	}
	for i, existing := range s.overflow {
		if existing != id {
			continue
		}
		last := len(s.overflow) - 1
		s.overflow[i] = s.overflow[last]
		s.overflow[last] = FactID{}
		s.overflow = s.overflow[:last]
		return true
	}
	return false
}

func (s *reteGraphAlphaFactSet) contains(id FactID) bool {
	if s == nil || id.IsZero() {
		return false
	}
	for _, existing := range s.inline {
		if existing == id {
			return true
		}
	}
	if s.overflow == nil {
		return false
	}
	return slices.Contains(s.overflow, id)
}

func (s *reteGraphAlphaFactSet) clear() {
	if s == nil {
		return
	}
	s.inline = [4]FactID{}
	clear(s.overflow)
	s.overflow = s.overflow[:0]
}

func (m *betaSideMemory) joinRowCount(key betaJoinKey) int {
	count := 0
	m.forEachJoinRow(key, func(graphTokenRowID, *betaTokenRow) bool {
		count++
		return true
	})
	return count
}

func (m *betaSideMemory) forEachJoinRow(key betaJoinKey, fn func(graphTokenRowID, *betaTokenRow) bool) {
	if m == nil || fn == nil || m.indexes.isEmpty() {
		return
	}
	bucket := m.indexes.bucket(key)
	if bucket == nil {
		return
	}
	for rowIndex := range bucket.rows {
		row := &bucket.rows[rowIndex]
		if row == nil || row.joinKey != key {
			continue
		}
		if !fn(graphTokenRowID(rowIndex), row) {
			return
		}
	}
}

func (m *betaSideMemory) forEachRow(fn func(*betaTokenRow) bool) {
	if m == nil || fn == nil {
		return
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			if !fn(&bucket.rows[rowIndex]) {
				return
			}
		}
	}
}

func (m *betaSideMemory) insert(token tokenRef, joinKey betaJoinKey) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.indexes.reserve(max(8, m.indexes.len()+1)) {
		m.joinIndexReserve = max(m.joinIndexReserve, len(m.indexes.buckets))
	}
	bucket := m.indexes.bucket(joinKey)
	if bucket == nil {
		return false
	}
	slot := m.indexes.slot(joinKey)
	if len(bucket.rows) == 0 {
		m.indexes.touched = append(m.indexes.touched, slot)
		m.indexes.slotCount++
	}
	bucket.rows = append(bucket.rows, betaTokenRow{
		token:   token,
		joinKey: joinKey,
	})
	m.indexes.rowCount++
	m.rowReserve = max(m.rowReserve, m.indexes.len())
	return true
}

func (m *betaSideMemory) containsExactToken(token tokenRef) bool {
	if m == nil || token.isZero() {
		return false
	}
	if _, ok := token.resolve(); !ok {
		return false
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			if bucket.rows[rowIndex].token.handle == token.handle {
				return true
			}
		}
	}
	return false
}

func (m *betaSideMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := 0; rowIndex < len(bucket.rows); {
			row := &bucket.rows[rowIndex]
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			if row.token.isZero() || !row.token.containsFact(id) {
				rowIndex++
				continue
			}
			m.removeBucketRow(bucket, rowIndex, counters)
			removed++
		}
	}
	return removed
}

func (m *betaSideMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(betaTokenRow)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := 0; rowIndex < len(bucket.rows); {
			row := &bucket.rows[rowIndex]
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			if row.token.isZero() || !row.token.containsFact(id) {
				rowIndex++
				continue
			}
			if fn != nil {
				fn(*row)
			}
			m.removeBucketRow(bucket, rowIndex, counters)
			removed++
		}
	}
	return removed
}

func (m *betaSideMemory) removeToken(token tokenRef, counters *propagationCounterLedger, branchIDs ...int) (betaTokenRow, bool) {
	if m == nil || token.isZero() {
		return betaTokenRow{}, false
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			row := &bucket.rows[rowIndex]
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			if !tokenRefEqual(row.token, token) {
				continue
			}
			removed := *row
			m.removeBucketRow(bucket, rowIndex, counters)
			return removed, true
		}
	}
	return betaTokenRow{}, false
}

func (m *betaSideMemory) removeTokenWithJoinKey(token tokenRef, joinKey betaJoinKey, counters *propagationCounterLedger) (betaTokenRow, bool) {
	if m == nil || token.isZero() {
		return betaTokenRow{}, false
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket := m.indexes.bucket(joinKey)
	if bucket == nil {
		return betaTokenRow{}, false
	}
	for rowIndex := range bucket.rows {
		row := &bucket.rows[rowIndex]
		if row.joinKey != joinKey {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		removed := *row
		m.removeBucketRow(bucket, rowIndex, counters)
		return removed, true
	}
	return betaTokenRow{}, false
}

func (m *betaSideMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(betaTokenRow)) {
	if m == nil || id.IsZero() {
		return
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			row := &bucket.rows[rowIndex]
			if row.token.isZero() || !row.token.containsFact(id) {
				continue
			}
			if fn != nil {
				fn(*row)
			}
		}
	}
}

func (m *betaSideMemory) removeBucketRow(bucket *betaJoinTokenBucket, rowIndex int, counters *propagationCounterLedger) {
	if m == nil || bucket == nil || rowIndex < 0 || rowIndex >= len(bucket.rows) {
		return
	}
	last := len(bucket.rows) - 1
	if rowIndex != last {
		bucket.rows[rowIndex] = bucket.rows[last]
		if counters != nil {
			counters.recordRemovalRowMoved()
		}
	}
	bucket.rows[last] = betaTokenRow{}
	bucket.rows = bucket.rows[:last]
	m.indexes.rowCount--
	if len(bucket.rows) == 0 {
		m.indexes.slotCount--
	}
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m *betaSideMemory) factRowCount(id FactID) int {
	if m == nil || id.IsZero() {
		return 0
	}
	count := 0
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			row := &bucket.rows[rowIndex]
			if row.token.isZero() || !row.token.containsFact(id) {
				continue
			}
			count++
		}
	}
	return count
}

func (m *reteGraphBetaMemory) resetFacts(ctx context.Context, facts []FactSnapshot) error {
	return m.resetFactsForGeneration(ctx, facts, reteGraphFactsGeneration(facts))
}

func (m *reteGraphBetaMemory) resetFactsForGeneration(ctx context.Context, facts []FactSnapshot, generation Generation) error {
	_, err := m.resetFactsForGenerationWithDelta(ctx, facts, generation)
	return err
}

func (m *reteGraphBetaMemory) resetFactsForGenerationWithDelta(ctx context.Context, facts []FactSnapshot, generation Generation) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if m == nil || m.graph == nil {
		return combined, nil
	}
	if len(m.alpha.facts) != len(m.graph.alphaNodes)+1 || len(m.alpha.conditions) != len(m.graph.alphaNodes)+1 {
		m.reserveAlphaFacts(graphBetaAlphaFactCapacity(m.revision, m.graph, len(facts)))
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	} else {
		m.arena.reset()
	}
	if _, err := m.propagateEvent(ctx, newReteGraphClearEvent(generation, mutationOrigin{}, nil)); err != nil {
		return combined, err
	}
	m.sourceGenerationValue = generation
	m.deferAggregateOutputs = true
	m.suppressTerminalDeltas = true
	defer func() {
		m.deferAggregateOutputs = false
		m.suppressTerminalDeltas = false
		m.clearDeferredAggregateOutputs()
	}()
	m.initializeAggregateOutputs()
	if err := m.initializeRootStage(nil); err != nil {
		return combined, err
	}
	for _, fact := range facts {
		delta, err := m.propagateEvent(ctx, newReteGraphResetAssertEvent(fact))
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, normalizeBackchainDemandNoopDelta(delta))
	}
	if err := m.finalizeDeferredAggregateOutputs(nil); err != nil {
		return combined, err
	}
	if err := m.finalizeDeferredAggregateOutputs(nil); err != nil {
		return combined, err
	}
	return combined, nil
}

func (m *reteGraphBetaMemory) resetFactWorkspaceForGenerationWithDelta(ctx context.Context, facts *factWorkspace, generation Generation) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if m == nil || m.graph == nil {
		return combined, nil
	}
	factCount := 0
	if facts != nil {
		factCount = facts.factCount()
	}
	if len(m.alpha.facts) != len(m.graph.alphaNodes)+1 || len(m.alpha.conditions) != len(m.graph.alphaNodes)+1 {
		m.reserveAlphaFacts(graphBetaAlphaFactCapacity(m.revision, m.graph, factCount))
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	} else {
		m.arena.reset()
	}
	if _, err := m.propagateEvent(ctx, newReteGraphClearEvent(generation, mutationOrigin{}, nil)); err != nil {
		return combined, err
	}
	if facts != nil {
		m.compactSlotStore = facts.compactSlotStore
	}
	m.sourceGenerationValue = generation
	m.deferAggregateOutputs = true
	m.suppressTerminalDeltas = true
	defer func() {
		m.deferAggregateOutputs = false
		m.suppressTerminalDeltas = false
		m.clearDeferredAggregateOutputs()
	}()
	m.initializeAggregateOutputs()
	if err := m.initializeRootStage(nil); err != nil {
		return combined, err
	}
	if facts != nil {
		for _, id := range facts.insertionOrder {
			fact, ok := facts.workingFactByID(id)
			if !ok {
				continue
			}
			snapshot := fact.detachedSnapshotForRevision(m.revision, facts.compactSlotStore)
			delta, err := m.propagateEvent(ctx, newReteGraphResetWorkingAssertEvent(fact, snapshot))
			if err != nil {
				return combined, err
			}
			combined = mergeReteAgendaDelta(combined, normalizeBackchainDemandNoopDelta(delta))
		}
	}
	if err := m.finalizeDeferredAggregateOutputs(nil); err != nil {
		return combined, err
	}
	if err := m.finalizeDeferredAggregateOutputs(nil); err != nil {
		return combined, err
	}
	return combined, nil
}

func (m *reteGraphBetaMemory) resetFactsForGenerationWithInitialAgenda(ctx context.Context, facts []FactSnapshot, generation Generation, agenda *agenda) (reteAgendaDelta, error) {
	if m == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	previousAgenda := m.initialAgenda
	previousErr := m.initialAgendaErr
	m.initialAgenda = agenda
	m.initialAgendaErr = nil
	delta, err := m.resetFactsForGenerationWithDelta(ctx, facts, generation)
	initialErr := m.initialAgendaErr
	m.initialAgenda = previousAgenda
	m.initialAgendaErr = previousErr
	if err != nil {
		return delta, err
	}
	if initialErr != nil {
		return delta, initialErr
	}
	return delta, nil
}

func (m *reteGraphBetaMemory) resetFactWorkspaceForGenerationWithInitialAgenda(ctx context.Context, facts *factWorkspace, generation Generation, agenda *agenda) (reteAgendaDelta, error) {
	if m == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	previousAgenda := m.initialAgenda
	previousErr := m.initialAgendaErr
	m.initialAgenda = agenda
	m.initialAgendaErr = nil
	delta, err := m.resetFactWorkspaceForGenerationWithDelta(ctx, facts, generation)
	initialErr := m.initialAgendaErr
	m.initialAgenda = previousAgenda
	m.initialAgendaErr = previousErr
	if err != nil {
		return delta, err
	}
	if initialErr != nil {
		return delta, initialErr
	}
	return delta, nil
}

func (m *reteGraphBetaMemory) initializeRootStage(span *propagationCounterSpan) error {
	if m == nil || m.graph == nil {
		return nil
	}
	root := reteGraphStageRef{kind: reteGraphStageRoot}
	if len(m.graph.stageSuccessors(root)) == 0 && len(m.graph.stageTerminals(root)) == 0 && len(m.graph.stageAggregateInputs(root)) == 0 && len(m.graph.stageAggregateOuters(root)) == 0 {
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

func (m *reteGraphBetaMemory) clearMemories() {
	if m == nil {
		return
	}
	for _, node := range m.nodes {
		if node != nil {
			node.left.clear()
			node.right.clear()
			node.negative.clear()
		}
	}
	for _, terminal := range m.terminals {
		if terminal != nil {
			terminal.rows.clear()
			terminal.queryRows.clear()
		}
	}
	for _, aggregate := range m.aggregates {
		if aggregate != nil {
			aggregate.clear()
		}
	}
	for i := range m.alpha.facts {
		m.alpha.facts[i].clear()
	}
	if m.alpha.factCounts != nil {
		clear(m.alpha.factCounts)
	}
	if m.alpha.factOwnership != nil {
		for _, id := range m.alpha.factOwnershipIDs {
			delete(m.alpha.factOwnership, id)
		}
	}
	m.alpha.factOwnershipIDs = m.alpha.factOwnershipIDs[:0]
	m.alpha.factRouteStorage = m.alpha.factRouteStorage[:0]
	clear(m.alpha.factTerminalStorage)
	m.alpha.factTerminalStorage = m.alpha.factTerminalStorage[:0]
	clear(m.terminalTokenDeltas)
	m.terminalTokenDeltas = m.terminalTokenDeltas[:0]
	clear(m.terminalRemovedDeltas)
	m.terminalRemovedDeltas = m.terminalRemovedDeltas[:0]
	m.clearDeferredAggregateOutputs()
	m.clearBackchainDemandRequests()
	m.rootToken = tokenRef{}
}

func (m *reteGraphBetaMemory) appendRemovedTerminalDelta(delta *reteAgendaDelta, removed reteTerminalTokenDelta) {
	if m == nil || delta == nil {
		return
	}
	if start, ok := m.terminalRemovedDeltaArenaStart(delta.removed); ok {
		m.terminalRemovedDeltas = append(m.terminalRemovedDeltas, removed)
		delta.removed = m.terminalRemovedDeltas[start:len(m.terminalRemovedDeltas)]
		return
	}
	delta.removed = append(delta.removed, removed)
}

func (m *reteGraphBetaMemory) terminalRemovedDeltaArenaStart(removed []reteTerminalTokenDelta) (int, bool) {
	if m == nil || len(removed) > len(m.terminalRemovedDeltas) {
		return 0, false
	}
	if len(removed) == 0 {
		return len(m.terminalRemovedDeltas), true
	}
	start := len(m.terminalRemovedDeltas) - len(removed)
	if start < 0 || start >= len(m.terminalRemovedDeltas) {
		return 0, false
	}
	return start, &removed[0] == &m.terminalRemovedDeltas[start]
}

func (m *reteGraphBetaMemory) appendAlphaFactRoute(routes []reteGraphAlphaNodeID, nodeID reteGraphAlphaNodeID) []reteGraphAlphaNodeID {
	if m == nil || nodeID <= 0 {
		return routes
	}
	if start, ok := m.alphaFactRouteArenaStart(routes); ok {
		m.alpha.factRouteStorage = append(m.alpha.factRouteStorage, nodeID)
		return m.alpha.factRouteStorage[start:len(m.alpha.factRouteStorage)]
	}
	return append(routes, nodeID)
}

func (m *reteGraphBetaMemory) appendAlphaFactRouteOrdered(routes []reteGraphAlphaNodeID, nodeID reteGraphAlphaNodeID) ([]reteGraphAlphaNodeID, bool) {
	if len(routes) == 0 {
		return m.appendAlphaFactRoute(routes, nodeID), true
	}
	index, found := slices.BinarySearch(routes, nodeID)
	if found {
		return routes, false
	}
	if index == len(routes) {
		return m.appendAlphaFactRoute(routes, nodeID), true
	}
	next := m.appendAlphaFactRoute(routes, nodeID)
	copy(next[index+1:], next[index:len(next)-1])
	next[index] = nodeID
	return next, true
}

func (m *reteGraphBetaMemory) alphaFactRouteArenaStart(routes []reteGraphAlphaNodeID) (int, bool) {
	if m == nil || len(routes) > len(m.alpha.factRouteStorage) {
		return 0, false
	}
	if len(routes) == 0 {
		return len(m.alpha.factRouteStorage), true
	}
	start := len(m.alpha.factRouteStorage) - len(routes)
	if start < 0 || start >= len(m.alpha.factRouteStorage) {
		return 0, false
	}
	return start, &routes[0] == &m.alpha.factRouteStorage[start]
}

func (m *reteGraphBetaMemory) appendGeneratedTerminalRow(rows []generatedTerminalRowHandle, row generatedTerminalRowHandle) []generatedTerminalRowHandle {
	if m == nil || row.handle.isZero() {
		return rows
	}
	if start, ok := m.generatedTerminalRowArenaStart(rows); ok {
		m.alpha.factTerminalStorage = append(m.alpha.factTerminalStorage, row)
		return m.alpha.factTerminalStorage[start:len(m.alpha.factTerminalStorage)]
	}
	return append(rows, row)
}

func (m *reteGraphBetaMemory) generatedTerminalRowArenaStart(rows []generatedTerminalRowHandle) (int, bool) {
	if m == nil || len(rows) > len(m.alpha.factTerminalStorage) {
		return 0, false
	}
	if len(rows) == 0 {
		return len(m.alpha.factTerminalStorage), true
	}
	start := len(m.alpha.factTerminalStorage) - len(rows)
	if start < 0 || start >= len(m.alpha.factTerminalStorage) {
		return 0, false
	}
	return start, &rows[0] == &m.alpha.factTerminalStorage[start]
}

func (m *reteGraphBetaMemory) clearBackchainDemandRequests() {
	if m == nil {
		return
	}
	clear(m.backchainDemandRecords)
	m.backchainDemandRecords = m.backchainDemandRecords[:0]
	for i := range m.backchainDemandSlots {
		m.backchainDemandSlots[i] = factSlot{}
	}
	m.backchainDemandSlots = m.backchainDemandSlots[:0]
	clear(m.backchainDemandSupport)
	m.backchainDemandSupport = m.backchainDemandSupport[:0]
	clear(m.backchainDemandDeltaIDs)
	m.backchainDemandDeltaIDs = m.backchainDemandDeltaIDs[:0]
	clear(m.backchainDemandOwners)
	m.backchainDemandOwners = m.backchainDemandOwners[:0]
	m.nextBackchainDemandID = 0
}

func (m *reteGraphBetaMemory) appendBackchainDemandDeltaID(ids []backchainDemandID, id backchainDemandID) []backchainDemandID {
	if m == nil || id == 0 {
		return ids
	}
	if start, ok := m.backchainDemandDeltaIDArenaStart(ids); ok {
		m.backchainDemandDeltaIDs = append(m.backchainDemandDeltaIDs, id)
		return m.backchainDemandDeltaIDs[start:len(m.backchainDemandDeltaIDs)]
	}
	return append(ids, id)
}

func (m *reteGraphBetaMemory) backchainDemandDeltaIDArenaStart(ids []backchainDemandID) (int, bool) {
	if m == nil || len(ids) > len(m.backchainDemandDeltaIDs) {
		return 0, false
	}
	if len(ids) == 0 {
		return len(m.backchainDemandDeltaIDs), true
	}
	start := len(m.backchainDemandDeltaIDs) - len(ids)
	if start < 0 || start >= len(m.backchainDemandDeltaIDs) {
		return 0, false
	}
	return start, &ids[0] == &m.backchainDemandDeltaIDs[start]
}

func (m *reteGraphBetaMemory) appendBackchainDemandOwnerKey(keys []backchainDemandOwnerKey, key backchainDemandOwnerKey) []backchainDemandOwnerKey {
	if m == nil || key.isZero() {
		return keys
	}
	if start, ok := m.backchainDemandOwnerArenaStart(keys); ok {
		m.backchainDemandOwners = append(m.backchainDemandOwners, key)
		return m.backchainDemandOwners[start:len(m.backchainDemandOwners)]
	}
	return append(keys, key)
}

func (m *reteGraphBetaMemory) backchainDemandOwnerArenaStart(keys []backchainDemandOwnerKey) (int, bool) {
	if m == nil || len(keys) > len(m.backchainDemandOwners) {
		return 0, false
	}
	if len(keys) == 0 {
		return len(m.backchainDemandOwners), true
	}
	start := len(m.backchainDemandOwners) - len(keys)
	if start < 0 || start >= len(m.backchainDemandOwners) {
		return 0, false
	}
	return start, &keys[0] == &m.backchainDemandOwners[start]
}

func (m *reteGraphBetaMemory) backchainDemandRequestByID(id backchainDemandID) (backchainDemandRequest, bool) {
	if m == nil || id == 0 {
		return backchainDemandRequest{}, false
	}
	index := int(id - 1)
	if index < 0 || index >= len(m.backchainDemandRecords) {
		return backchainDemandRequest{}, false
	}
	record := m.backchainDemandRecords[index]
	if record.id != id || record.templateKey == "" || record.slotStart < 0 || record.supportStart < 0 {
		return backchainDemandRequest{}, false
	}
	slotEnd := record.slotStart + record.slotCount
	supportEnd := record.supportStart + record.supportCount
	if slotEnd < record.slotStart || supportEnd < record.supportStart || slotEnd > len(m.backchainDemandSlots) || supportEnd > len(m.backchainDemandSupport) {
		return backchainDemandRequest{}, false
	}
	return backchainDemandRequest{
		templateKey:  record.templateKey,
		slots:        m.backchainDemandSlots[record.slotStart:slotEnd],
		supportFacts: m.backchainDemandSupport[record.supportStart:supportEnd],
		owner:        record.owner,
	}, true
}

func (m *reteGraphBetaMemory) nextBackchainDemandIDValue() backchainDemandID {
	m.nextBackchainDemandID++
	if m.nextBackchainDemandID == 0 {
		m.nextBackchainDemandID++
	}
	return m.nextBackchainDemandID
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
		m.refreshAggregateOutputDeferred(node.id, bucket, nil, nil, &delta)
	}
}

func (m *reteGraphAggregateNodeMemory) clear() {
	if m == nil {
		return
	}
	m.buckets.clear()
	m.numeric.clear()
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
			if event.generated {
				return m.insertFactGenerated(ctx, event.workingFact, event.span)
			}
			return m.insertWorkingFactInternal(ctx, event.workingFact, event.fact, event.span, event.updateSource)
		}
		if event.transient {
			return m.insertFactInternal(ctx, event.fact, event.span, false)
		}
		return m.insertFactInternal(ctx, event.fact, event.span, event.updateSource)
	case reteGraphPropagationRemove:
		if event.workingFact != nil {
			if event.generated {
				return m.removeGeneratedWorkingFact(ctx, event.workingFact, event.counters)
			}
			return m.removeWorkingFact(ctx, event.workingFact, event.counters)
		}
		if event.transient {
			return m.removeFactInternal(ctx, event.fact, event.counters, false)
		}
		return m.removeFact(ctx, event.fact, event.counters)
	case reteGraphPropagationUpdate:
		return m.updateFact(ctx, event)
	case reteGraphPropagationClear:
		m.clearMemories()
		return reteAgendaDelta{supported: true}, nil
	case reteGraphPropagationModifyAdd:
		if event.workingFact != nil {
			return m.insertWorkingFactInternal(ctx, event.workingFact, event.fact, event.span, false)
		}
		return m.insertFactInternal(ctx, event.fact, event.span, false)
	case reteGraphPropagationModifyRemove:
		if event.workingFact != nil {
			return m.removeWorkingFactForModifyInternal(ctx, event.workingFact, event.fact, event.counters, false)
		}
		return m.removeFactInternal(ctx, event.fact, event.counters, false)
	default:
		return reteAgendaDelta{}, ErrUnsupportedRuntime
	}
}

func (m *reteGraphBetaMemory) insertFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, error) {
	return m.insertFactInternal(ctx, fact, span, true)
}

func (m *reteGraphBetaMemory) insertWorkingFact(ctx context.Context, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, error) {
	return m.insertWorkingFactInternal(ctx, fact, snapshot, span, true)
}

func (m *reteGraphBetaMemory) insertFactInternal(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan, updateSource bool) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	if updateSource {
		m.sourceGenerationValue = fact.Generation()
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

func (m *reteGraphBetaMemory) insertWorkingFactInternal(ctx context.Context, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan, updateSource bool) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	if updateSource {
		m.sourceGenerationValue = snapshot.Generation()
	}
	routeIDs := m.snapshotAlphaRouteIDsForFactInsert(snapshot, span)
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
		ok, err := m.workingFactMatchesAlphaNode(ctx, fact, snapshot, node, span)
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
			fact:        m.workingFactRefForAlphaNode(fact, snapshot, node),
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
		ok, err := node.matchesGeneratedWorkingWithContextAndCounters(ctx, fact, m.compactSlotStore, span)
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
			fact:        newConditionFactRefFromWorkingFactForTarget(fact, node.target, m.compactSlotStore),
		}
		ok, err = m.insertGeneratedAlphaOps(nodeID, node, match, span, &delta)
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
	templateIDs := m.graph.routesByTemplateID[fact.templateID]
	if len(templateIDs) > 3 {
		templateIDs = m.workingAlphaRouteIDs(fact.templateID, templateIDs, fact, span)
	}
	nameIDs := m.graph.routesByName[fact.storedName()]
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

func (m *reteGraphBetaMemory) workingAlphaRouteIDs(id templateID, nodeIDs []reteGraphAlphaNodeID, fact *workingFact, span *propagationCounterSpan) []reteGraphAlphaNodeID {
	if m == nil || m.graph == nil || fact == nil {
		return nil
	}
	table := m.graph.alphaRouteIDTables[id]
	if table == nil || len(table.indexed) == 0 {
		if span != nil {
			span.recordAlphaIndexFallbackScan()
		}
		return nodeIDs
	}
	if fieldSlot, ok := table.singleIndexedField(); ok {
		value, valueOK := m.workingAlphaRouteFieldValue(id, fact, fieldSlot)
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
		value, ok := m.workingAlphaRouteFieldValue(id, fact, fieldSlot)
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

func (m *reteGraphBetaMemory) workingAlphaRouteFieldValue(id templateID, fact *workingFact, fieldSlot int) (Value, bool) {
	field := m.alphaRouteFieldNameByID(id, fieldSlot)
	return fact.compiledFieldValue(field, fieldSlot, m.compactSlotStore)
}

func (m *reteGraphBetaMemory) alphaRouteFieldNameByID(id templateID, fieldSlot int) string {
	if m == nil || m.revision == nil || fieldSlot < 0 {
		return ""
	}
	template, ok := m.revision.templateByID(id)
	if !ok || fieldSlot >= len(template.fields) {
		return ""
	}
	return template.fields[fieldSlot].Name
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
	row := m.alpha.factOwnership[id]
	m.appendAlphaRouteBucket(row.routes)
	m.sortAlphaRouteScratch()
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

func (m *reteGraphBetaMemory) insertGeneratedAlphaOps(nodeID reteGraphAlphaNodeID, node *reteGraphAlphaNode, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || node == nil || delta == nil {
		return false, nil
	}
	if len(node.generatedOps) == 0 {
		return true, nil
	}
	var captures []listPatternCapture
	if len(node.listPatterns) != 0 {
		var capturesOK bool
		captures, capturesOK = node.listPatternCaptures(match.fact, tokenRef{})
		if !capturesOK {
			return false, nil
		}
	}
	m.recordAlphaFact(nodeID, match.fact)
	for _, op := range node.generatedOps {
		switch op.kind {
		case reteGraphGeneratedAlphaOpTerminal:
			if op.entry.conditionID == "" {
				delta.supported = false
				continue
			}
			token := m.newAlphaTokenRef(op.entry, match, captures, span)
			if token.isZero() {
				delta.supported = false
				continue
			}
			handle, inserted := m.insertTerminalToken(op.terminalID, op.branchID, token, delta, span)
			if inserted {
				m.recordGeneratedTerminalRow(match.fact.ID(), nodeID, op.terminalID, op.branchID, handle)
			}
		case reteGraphGeneratedAlphaOpBetaLeft:
			if op.entry.conditionID == "" {
				delta.supported = false
				continue
			}
			token := m.newAlphaTokenRef(op.entry, match, captures, span)
			if token.isZero() {
				delta.supported = false
				continue
			}
			var ok bool
			var err error
			if len(captures) == 0 {
				ok, err = m.insertGeneratedBetaLeftInput(op.betaNodeID, token, op.entry, match, span, delta)
			} else {
				ok, err = m.insertBetaInput(op.betaNodeID, op.side, token, op.betaEntry, span, delta)
			}
			if err != nil {
				return false, err
			}
			if !ok {
				delta.supported = false
				continue
			}
		case reteGraphGeneratedAlphaOpBetaRight:
			token := m.newAlphaTokenRef(op.entry, match, captures, span)
			if token.isZero() {
				delta.supported = false
				continue
			}
			var ok bool
			var err error
			if len(captures) == 0 {
				ok, err = m.insertGeneratedBetaRightInput(op.betaNodeID, token, op.entry, match, span, delta)
			} else {
				ok, err = m.insertBetaInput(op.betaNodeID, op.side, token, op.betaEntry, span, delta)
			}
			if err != nil {
				return false, err
			}
			if !ok {
				delta.supported = false
				continue
			}
		case reteGraphGeneratedAlphaOpAggregateOuter:
			if op.entry.conditionID == "" {
				delta.supported = false
				continue
			}
			token := m.newAlphaTokenRef(op.entry, match, captures, span)
			if token.isZero() {
				delta.supported = false
				continue
			}
			m.openAggregateBucket(op.aggregateID, token, span, delta)
		case reteGraphGeneratedAlphaOpAggregateInput:
			m.insertAggregateInput(op.aggregateID, match, span, delta)
		default:
			delta.supported = false
		}
	}
	return delta.supported, nil
}

func (m *reteGraphBetaMemory) removeGeneratedAlphaOps(nodeID reteGraphAlphaNodeID, node *reteGraphAlphaNode, match conditionMatch, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || node == nil || delta == nil {
		return
	}
	if counters != nil {
		counters.recordNegativePropagationEvent()
	}
	if len(node.generatedOps) == 0 {
		return
	}
	var captures []listPatternCapture
	if len(node.listPatterns) != 0 {
		var capturesOK bool
		captures, capturesOK = node.listPatternCaptures(match.fact, tokenRef{})
		if !capturesOK {
			delta.supported = false
			return
		}
	}
	for _, op := range node.generatedOps {
		switch op.kind {
		case reteGraphGeneratedAlphaOpTerminal:
			if op.entry.conditionID == "" {
				delta.supported = false
				continue
			}
			if handle, ok := m.takeGeneratedTerminalRow(match.fact.ID(), nodeID, op.terminalID, op.branchID); ok {
				if m.removeTerminalTokenByHandle(op.terminalID, op.branchID, handle, counters, delta) {
					continue
				}
			}
			token := m.newAlphaTokenRef(op.entry, match, captures, nil)
			if token.isZero() {
				delta.supported = false
				continue
			}
			m.removeTerminalToken(op.terminalID, op.branchID, token, counters, delta)
		case reteGraphGeneratedAlphaOpBetaLeft:
			if op.entry.conditionID == "" {
				delta.supported = false
				continue
			}
			token := m.newAlphaTokenRef(op.entry, match, captures, nil)
			if token.isZero() || !m.removeBetaInputToken(op.betaNodeID, op.side, token, counters, delta) {
				delta.supported = false
			}
		case reteGraphGeneratedAlphaOpBetaRight:
			token := m.newAlphaTokenRef(op.entry, match, captures, nil)
			if token.isZero() || !m.removeBetaInputToken(op.betaNodeID, op.side, token, counters, delta) {
				delta.supported = false
			}
		case reteGraphGeneratedAlphaOpAggregateOuter:
			if op.entry.conditionID == "" {
				delta.supported = false
				continue
			}
			token := m.newAlphaTokenRef(op.entry, match, captures, nil)
			if token.isZero() {
				delta.supported = false
				continue
			}
			m.removeAggregateBucket(op.aggregateID, token, counters, delta)
		case reteGraphGeneratedAlphaOpAggregateInput:
			m.removeAggregateInput(op.aggregateID, match, counters, delta)
		default:
			delta.supported = false
		}
	}
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
	for _, terminal := range m.graph.stageTerminals(source) {
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
		_, _ = m.insertTerminalToken(terminal.terminalID, terminal.branchID, token, delta, span)
	}
	for _, successor := range m.graph.stageSuccessors(source) {
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
	for _, aggregateID := range m.graph.stageAggregateOuters(source) {
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
	for _, aggregateID := range m.graph.stageAggregateInputs(source) {
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
	for _, terminal := range m.graph.stageTerminals(source) {
		entry := terminal.entry
		if entry.conditionID == "" {
			entry = sourceEntry
		}
		if entry.conditionID == "" {
			delta.supported = false
			continue
		}
		token := m.newAlphaTokenRef(entry, match, captures, nil)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.removeTerminalToken(terminal.terminalID, terminal.branchID, token, counters, delta)
	}
	for _, successor := range m.graph.stageSuccessors(source) {
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
	for _, aggregateID := range m.graph.stageAggregateOuters(source) {
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
	for _, aggregateID := range m.graph.stageAggregateInputs(source) {
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
	if len(captures) == 0 {
		return m.newAlphaSourceTokenRef(entry, match, span)
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

func (m *reteGraphBetaMemory) newAlphaSourceTokenRef(entry bindingTupleEntry, match conditionMatch, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	}
	return m.arena.addAlphaSource(entry, match, match.fact.Recency(), match.fact.Generation())
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
		retainedMatch, ok := row.conditionMatch()
		if !ok {
			return tokenRef{}
		}
		if !retainedMatch.hasValue || retainedMatch.bindingSlot != entry.bindingSlot {
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
		token = m.newTokenRowRefSource(token, ref, row, 0, match.fact.Generation(), span)
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
	m.graphAggregateMemory(id).insertInput(match, span, delta)
}

func (m *reteGraphBetaMemory) removeAggregateInput(id reteGraphAggregateNodeID, match conditionMatch, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	m.graphAggregateMemory(id).removeInput(match, counters, delta)
}

func (m *reteGraphBetaMemory) insertAggregateToken(id reteGraphAggregateNodeID, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	m.graphAggregateMemory(id).insertToken(token, span, delta)
}

func (m *reteGraphBetaMemory) removeAggregateToken(id reteGraphAggregateNodeID, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	m.graphAggregateMemory(id).removeToken(token, counters, delta)
}

func (m *reteGraphBetaMemory) openAggregateBucket(id reteGraphAggregateNodeID, parent tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	aggregate := m.graphAggregateMemory(id)
	aggregate.openBucket(m.aggregateParentToken(aggregate.node, parent), span, delta)
}

func (m *reteGraphBetaMemory) removeAggregateBucket(id reteGraphAggregateNodeID, parent tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	aggregate := m.graphAggregateMemory(id)
	aggregate.removeBucket(m.aggregateParentToken(aggregate.node, parent), counters, delta)
}

func (m *reteGraphBetaMemory) removeAggregateBucketsContainingFact(id reteGraphAggregateNodeID, factID FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	m.graphAggregateMemory(id).removeBucketsContainingFact(factID, counters, delta)
}

func (m *reteGraphBetaMemory) removeAggregateMembersContainingFact(id reteGraphAggregateNodeID, factID FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	m.graphAggregateMemory(id).removeMembersContainingFact(factID, counters, delta)
}

func (m *reteGraphBetaMemory) aggregateMember(node *reteGraphAggregateNode, token tokenRef, match conditionMatch) (reteGraphAggregateMember, bool) {
	if node == nil {
		return reteGraphAggregateMember{}, false
	}
	return reteGraphAggregateMember{token: token, factID: match.fact.ID()}, true
}

func aggregateMemberValues(node *reteGraphAggregateNode, token tokenRef) ([]Value, bool) {
	if node == nil {
		return nil, false
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		return nil, true
	}
	match, ok := tokenFactMatchForBindingSlot(token, node.inputEntry.bindingSlot)
	if !ok {
		return nil, false
	}
	bindings, ok := tokenConditionMatches(token)
	if !ok {
		return nil, false
	}
	return aggregateMemberValuesWithBindings(node, match, bindings)
}

func aggregateMemberValuesWithBindings(node *reteGraphAggregateNode, match conditionMatch, bindings []conditionMatch) ([]Value, bool) {
	if node == nil {
		return nil, false
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		return nil, true
	}
	values := make([]Value, len(node.specs))
	for i, spec := range node.specs {
		if spec.kind == AggregateCount {
			continue
		}
		value, ok, err := spec.expression.evaluate(match.fact, bindings)
		if err != nil || !ok {
			return nil, false
		}
		values[i] = value
	}
	return values, true
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
	memory.forEachBucket(func(bucket *reteGraphAggregateBucket) {
		m.refreshAggregateOutputInternal(id, bucket, span, nil, delta)
	})
}

func (m *reteGraphBetaMemory) refreshAggregateOutputWithCounters(id reteGraphAggregateNodeID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	memory := m.aggregateMemory(id)
	if memory == nil {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	memory.forEachBucket(func(bucket *reteGraphAggregateBucket) {
		m.refreshAggregateOutputInternal(id, bucket, nil, counters, delta)
	})
}

func (m *reteGraphBetaMemory) refreshAggregateOutputDeferred(id reteGraphAggregateNodeID, bucket *reteGraphAggregateBucket, span *propagationCounterSpan, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	if !m.deferAggregateOutputs {
		m.refreshAggregateOutputInternal(id, bucket, span, counters, delta)
		return
	}
	if bucket == nil {
		delta.supported = false
		return
	}
	key := deferredAggregateOutputKey{
		id:     id,
		parent: tokenRefKey(bucket.parent),
	}
	if m.deferredAggregateOutputs == nil {
		m.deferredAggregateOutputs = make(map[deferredAggregateOutputKey]struct{})
	}
	if _, exists := m.deferredAggregateOutputs[key]; !exists {
		m.deferredAggregateOrder = append(m.deferredAggregateOrder, key)
	}
	m.deferredAggregateOutputs[key] = struct{}{}
}

func (m *reteGraphBetaMemory) clearDeferredAggregateOutputs() {
	if m == nil {
		return
	}
	if m.deferredAggregateOutputs != nil {
		clear(m.deferredAggregateOutputs)
	}
	clear(m.deferredAggregateOrder)
	m.deferredAggregateOrder = m.deferredAggregateOrder[:0]
}

func (m *reteGraphBetaMemory) finalizeDeferredAggregateOutputs(span *propagationCounterSpan) error {
	if m == nil || len(m.deferredAggregateOrder) == 0 {
		return nil
	}
	order := make([]deferredAggregateOutputKey, 0, len(m.deferredAggregateOrder))
	for _, key := range m.deferredAggregateOrder {
		if _, ok := m.deferredAggregateOutputs[key]; ok {
			order = append(order, key)
		}
	}
	clear(m.deferredAggregateOutputs)
	m.deferredAggregateOrder = m.deferredAggregateOrder[:0]
	slices.SortFunc(order, func(left, right deferredAggregateOutputKey) int {
		if left.id != right.id {
			return int(left.id - right.id)
		}
		if left.parent.size != right.parent.size {
			return left.parent.size - right.parent.size
		}
		if left.parent.generation != right.parent.generation {
			if left.parent.generation < right.parent.generation {
				return -1
			}
			return 1
		}
		if left.parent.identityState < right.parent.identityState {
			return -1
		}
		if left.parent.identityState > right.parent.identityState {
			return 1
		}
		return 0
	})
	delta := &reteAgendaDelta{supported: true}
	for _, key := range order {
		memory := m.aggregateMemory(key.id)
		var bucket *reteGraphAggregateBucket
		if memory != nil {
			bucket, _ = memory.bucketByKey(key.parent)
		}
		if bucket == nil {
			continue
		}
		m.refreshAggregateOutputInternal(key.id, bucket, span, nil, delta)
	}
	if !delta.supported {
		return ErrUnsupportedRuntime
	}
	return nil
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
	if token.size() != width {
		return tokenRef{}
	}
	return tokenPublicTail(token)
}

func tokenPublicTail(token tokenRef) tokenRef {
	for !token.isZero() {
		row, ok := token.resolve()
		if !ok {
			return tokenRef{}
		}
		match, ok := row.conditionMatch()
		if !ok {
			return tokenRef{}
		}
		if match.bindingSlot >= 0 {
			return token
		}
		token = token.parent()
	}
	return tokenRef{}
}

func tokenPublicSize(token tokenRef) int {
	size := 0
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return 0
		}
		match, ok := row.conditionMatch()
		if !ok {
			return 0
		}
		if match.bindingSlot >= 0 {
			size++
		}
	}
	return size
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
	memory := m.aggregateMemory(id)
	if memory == nil {
		delta.supported = false
		return
	}
	stage := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(id)}
	values, ok := memory.bucketResults(node, bucket)
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
	if m.sourceGenerationValue != 0 {
		return m.sourceGenerationValue
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
	return m.buckets.bucketForParent(parent)
}

func (m *reteGraphAggregateNodeMemory) recycleBucket(bucket *reteGraphAggregateBucket) {
	if m == nil || bucket == nil {
		return
	}
	m.numeric.clearBucket(bucket.id)
	m.buckets.recycle(bucket)
}

func (m *reteGraphAggregateNodeMemory) bucketForParentIfExists(parent tokenRef) (*reteGraphAggregateBucket, bool) {
	if m == nil {
		return nil, false
	}
	return m.buckets.bucketForParentIfExists(parent)
}

func (m *reteGraphAggregateNodeMemory) removeBucketForParent(parent tokenRef) (*reteGraphAggregateBucket, bool) {
	if m == nil {
		return nil, false
	}
	return m.buckets.remove(tokenRefKey(parent))
}

func (m *reteGraphAggregateNodeMemory) removeBucketByKey(key graphTokenIdentityKey) (*reteGraphAggregateBucket, bool) {
	if m == nil {
		return nil, false
	}
	return m.buckets.remove(key)
}

func (m *reteGraphAggregateNodeMemory) bucketByKey(key graphTokenIdentityKey) (*reteGraphAggregateBucket, bool) {
	if m == nil {
		return nil, false
	}
	return m.buckets.get(key)
}

func (m *reteGraphAggregateNodeMemory) forEachBucket(fn func(*reteGraphAggregateBucket)) {
	if m == nil {
		return
	}
	m.buckets.forEach(fn)
}

func (m *reteGraphAggregateNodeMemory) forEachBucketWithKey(fn func(graphTokenIdentityKey, *reteGraphAggregateBucket)) {
	if m == nil {
		return
	}
	m.buckets.forEachKey(fn)
}

func (m *reteGraphAggregateNodeMemory) bucketCount() int {
	if m == nil {
		return 0
	}
	return m.buckets.len()
}

func (m *reteGraphAggregateNodeMemory) addMember(node *reteGraphAggregateNode, bucket *reteGraphAggregateBucket, member reteGraphAggregateMember) error {
	if m == nil || bucket == nil {
		return nil
	}
	return bucket.addMember(node, member, &m.numeric)
}

func (m *reteGraphAggregateNodeMemory) removeMember(node *reteGraphAggregateNode, bucket *reteGraphAggregateBucket, member reteGraphAggregateMember) bool {
	if m == nil || bucket == nil {
		return false
	}
	return bucket.removeMember(node, member, &m.numeric)
}

func (m *reteGraphAggregateNodeMemory) removeMemberWithCollectKey(node *reteGraphAggregateNode, bucket *reteGraphAggregateBucket, member reteGraphAggregateMember, collectKey graphTokenIdentityKey) bool {
	if m == nil || bucket == nil {
		return false
	}
	return bucket.removeMemberWithCollectKey(node, member, collectKey, &m.numeric)
}

func (m *reteGraphAggregateNodeMemory) bucketResults(node *reteGraphAggregateNode, bucket *reteGraphAggregateBucket) ([]Value, bool) {
	if m == nil || bucket == nil {
		return nil, false
	}
	return bucket.results(node, &m.numeric)
}

func (s *reteGraphAggregateNumericState) clear() {
	if s == nil {
		return
	}
	clear(s.intSums)
	s.intSums = s.intSums[:0]
	clear(s.floatSums)
	s.floatSums = s.floatSums[:0]
	clear(s.floaty)
	s.floaty = s.floaty[:0]
	s.specCount = 0
}

func (s *reteGraphAggregateNumericState) clearBucket(id reteGraphAggregateBucketID) {
	if s == nil || s.specCount <= 0 || id < 0 {
		return
	}
	start := int(id) * s.specCount
	end := start + s.specCount
	if start < 0 || end > len(s.intSums) || end > len(s.floatSums) || end > len(s.floaty) {
		return
	}
	clear(s.intSums[start:end])
	clear(s.floatSums[start:end])
	clear(s.floaty[start:end])
}

func (s *reteGraphAggregateNumericState) offset(id reteGraphAggregateBucketID, specs int, index int) (int, bool) {
	if s == nil || id < 0 || specs <= 0 || index < 0 || index >= specs {
		return 0, false
	}
	if s.specCount == 0 {
		s.specCount = specs
	}
	if s.specCount != specs {
		return 0, false
	}
	offset := int(id)*s.specCount + index
	need := offset + 1
	if need > len(s.intSums) {
		s.intSums = append(s.intSums, make([]int64, need-len(s.intSums))...)
	}
	if need > len(s.floatSums) {
		s.floatSums = append(s.floatSums, make([]float64, need-len(s.floatSums))...)
	}
	if need > len(s.floaty) {
		s.floaty = append(s.floaty, make([]bool, need-len(s.floaty))...)
	}
	return offset, true
}

func (s *reteGraphAggregateNumericState) addSum(id reteGraphAggregateBucketID, specs int, index int, value Value) error {
	offset, ok := s.offset(id, specs, index)
	if !ok {
		return fmt.Errorf("%w: aggregate numeric state unavailable", ErrAggregateEvaluation)
	}
	switch value.Kind() {
	case ValueInt:
		if s.floaty[offset] {
			s.floatSums[offset] += float64(value.intValue)
			return nil
		}
		next, overflow := safeAddInt64(s.intSums[offset], value.intValue)
		if overflow {
			return fmt.Errorf("%w: integer sum overflow", ErrAggregateEvaluation)
		}
		s.intSums[offset] = next
	case ValueFloat:
		if !s.floaty[offset] {
			s.floatSums[offset] = float64(s.intSums[offset])
			s.intSums[offset] = 0
			s.floaty[offset] = true
		}
		s.floatSums[offset] += value.floatValue
	default:
		return fmt.Errorf("%w: sum input must be numeric", ErrAggregateEvaluation)
	}
	return nil
}

func (s *reteGraphAggregateNumericState) removeSum(bucket *reteGraphAggregateBucket, node *reteGraphAggregateNode, index int, value Value) {
	if s == nil || bucket == nil || node == nil {
		return
	}
	offset, ok := s.offset(bucket.id, len(node.specs), index)
	if !ok {
		return
	}
	switch value.Kind() {
	case ValueInt:
		if s.floaty[offset] {
			s.floatSums[offset] -= float64(value.intValue)
			return
		}
		s.intSums[offset] -= value.intValue
	case ValueFloat:
		bucket.recomputeSum(node, index, s)
	}
}

func (s *reteGraphAggregateNumericState) resetSum(id reteGraphAggregateBucketID, specs int, index int) bool {
	offset, ok := s.offset(id, specs, index)
	if !ok {
		return false
	}
	s.intSums[offset] = 0
	s.floatSums[offset] = 0
	s.floaty[offset] = false
	return true
}

func (s *reteGraphAggregateNumericState) sumValue(id reteGraphAggregateBucketID, specs int, index int) (Value, bool) {
	offset, ok := s.offset(id, specs, index)
	if !ok {
		return Value{}, false
	}
	if s.floaty[offset] {
		value, err := canonicalFloat(s.floatSums[offset])
		if err != nil {
			return Value{}, false
		}
		return value, true
	}
	return newIntValue(s.intSums[offset]), true
}

func (t *reteGraphAggregateBucketTable) len() int {
	if t == nil {
		return 0
	}
	return t.live
}

func (t *reteGraphAggregateBucketTable) bucketForParent(parent tokenRef) *reteGraphAggregateBucket {
	if t == nil {
		return nil
	}
	key := tokenRefKey(parent)
	if bucket, ok := t.get(key); ok {
		return bucket
	}
	if t.ids == nil {
		t.ids = make(map[graphTokenIdentityKey]reteGraphAggregateBucketID)
	}
	id := t.allocate(parent)
	if id < 0 {
		return nil
	}
	t.ids[key] = id
	t.live++
	return t.bucketByID(id)
}

func (t *reteGraphAggregateBucketTable) allocate(parent tokenRef) reteGraphAggregateBucketID {
	if t == nil {
		return -1
	}
	last := len(t.free) - 1
	if last >= 0 {
		id := t.free[last]
		t.free[last] = 0
		t.free = t.free[:last]
		bucket := t.bucketByID(id)
		if bucket == nil {
			return t.allocate(parent)
		}
		bucket.clear()
		bucket.parent = parent
		return id
	}
	id := reteGraphAggregateBucketID(len(t.rows))
	t.rows = append(t.rows, reteGraphAggregateBucket{id: id, parent: parent})
	return id
}

func (t *reteGraphAggregateBucketTable) bucketForParentIfExists(parent tokenRef) (*reteGraphAggregateBucket, bool) {
	if t == nil {
		return nil, false
	}
	return t.get(tokenRefKey(parent))
}

func (t *reteGraphAggregateBucketTable) get(key graphTokenIdentityKey) (*reteGraphAggregateBucket, bool) {
	if t == nil || t.ids == nil {
		return nil, false
	}
	id, ok := t.ids[key]
	if !ok {
		return nil, false
	}
	bucket := t.bucketByID(id)
	if bucket == nil {
		return nil, false
	}
	return bucket, true
}

func (t *reteGraphAggregateBucketTable) bucketByID(id reteGraphAggregateBucketID) *reteGraphAggregateBucket {
	if t == nil || id < 0 || int(id) >= len(t.rows) {
		return nil
	}
	bucket := &t.rows[int(id)]
	if bucket.id != id {
		return nil
	}
	return bucket
}

func (t *reteGraphAggregateBucketTable) remove(key graphTokenIdentityKey) (*reteGraphAggregateBucket, bool) {
	if t == nil || t.ids == nil {
		return nil, false
	}
	id, ok := t.ids[key]
	if !ok {
		return nil, false
	}
	delete(t.ids, key)
	if t.live > 0 {
		t.live--
	}
	bucket := t.bucketByID(id)
	return bucket, bucket != nil
}

func (t *reteGraphAggregateBucketTable) recycle(bucket *reteGraphAggregateBucket) {
	if t == nil || bucket == nil {
		return
	}
	id := bucket.id
	if id < 0 || int(id) >= len(t.rows) || &t.rows[int(id)] != bucket {
		return
	}
	bucket.clear()
	t.free = append(t.free, id)
}

func (t *reteGraphAggregateBucketTable) clear() {
	if t == nil {
		return
	}
	t.free = t.free[:0]
	for i := range t.rows {
		t.rows[i].clear()
		t.rows[i].id = reteGraphAggregateBucketID(i)
		t.free = append(t.free, reteGraphAggregateBucketID(i))
	}
	if t.ids != nil {
		clear(t.ids)
	}
	t.live = 0
}

func (t *reteGraphAggregateBucketTable) forEach(fn func(*reteGraphAggregateBucket)) {
	if t == nil || fn == nil || t.ids == nil {
		return
	}
	for _, id := range t.ids {
		if bucket := t.bucketByID(id); bucket != nil {
			fn(bucket)
		}
	}
}

func (t *reteGraphAggregateBucketTable) forEachKey(fn func(graphTokenIdentityKey, *reteGraphAggregateBucket)) {
	if t == nil || fn == nil || t.ids == nil {
		return
	}
	for key, id := range t.ids {
		if bucket := t.bucketByID(id); bucket != nil {
			fn(key, bucket)
		}
	}
}

func (m *reteGraphAggregateBucket) addMember(node *reteGraphAggregateNode, member reteGraphAggregateMember, numeric *reteGraphAggregateNumericState) error {
	if m == nil || node == nil {
		return nil
	}
	m.count++
	if !aggregateSpecsNeedInputValues(node.specs) {
		return nil
	}
	values, ok := aggregateMemberValues(node, member.token)
	if !ok {
		return fmt.Errorf("%w: aggregate member values unavailable", ErrAggregateEvaluation)
	}
	m.ensureSpecState(len(node.specs))
	for i, spec := range node.specs {
		switch spec.kind {
		case AggregateCount:
			continue
		case AggregateSum:
			if err := numeric.addSum(m.id, len(node.specs), i, values[i]); err != nil {
				return err
			}
		case AggregateMin:
			if err := m.addExtremum(i, values[i], true); err != nil {
				return err
			}
		case AggregateMax:
			if err := m.addExtremum(i, values[i], false); err != nil {
				return err
			}
		case AggregateCollect:
			m.addCollect(i, member, values[i])
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

func (m *reteGraphAggregateBucket) removeMember(node *reteGraphAggregateNode, member reteGraphAggregateMember, numeric *reteGraphAggregateNumericState) bool {
	return m.removeMemberWithCollectKey(node, member, tokenRefKey(member.token), numeric)
}

func (m *reteGraphAggregateBucket) removeMemberWithCollectKey(node *reteGraphAggregateNode, member reteGraphAggregateMember, collectKey graphTokenIdentityKey, numeric *reteGraphAggregateNumericState) bool {
	if m == nil || node == nil {
		return false
	}
	var values []Value
	if aggregateSpecsNeedInputValues(node.specs) {
		var ok bool
		values, ok = aggregateMemberValues(node, member.token)
		if !ok {
			return false
		}
	}
	if m.count > 0 {
		m.count--
	}
	if !aggregateSpecsNeedInputValues(node.specs) {
		return true
	}
	m.ensureSpecState(len(node.specs))
	for i, spec := range node.specs {
		switch spec.kind {
		case AggregateSum:
			numeric.removeSum(m, node, i, values[i])
		case AggregateMin:
			m.removeExtremum(i, values[i], true)
		case AggregateMax:
			m.removeExtremum(i, values[i], false)
		case AggregateCollect:
			m.removeCollectByKey(i, collectKey)
		}
	}
	return true
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
	for len(m.extrema) < specs {
		m.extrema = append(m.extrema, reteGraphAggregateExtremum{})
	}
	for len(m.collects) < specs {
		m.collects = append(m.collects, nil)
	}
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

func (m *reteGraphAggregateBucket) addCollect(index int, member reteGraphAggregateMember, value Value) {
	m.ensureSpecState(index + 1)
	entry := reteGraphAggregateCollectEntry{
		key:    tokenRefKey(member.token),
		factID: member.factID,
		value:  cloneValue(value),
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

func (m *reteGraphAggregateBucket) recomputeSum(node *reteGraphAggregateNode, index int, numeric *reteGraphAggregateNumericState) {
	if m == nil || node == nil {
		return
	}
	if !numeric.resetSum(m.id, len(node.specs), index) {
		return
	}
	for _, member := range m.members {
		values, ok := aggregateMemberValues(node, member.token)
		if !ok || index >= len(values) {
			continue
		}
		_ = numeric.addSum(m.id, len(node.specs), index, values[index])
	}
}

func (m *reteGraphAggregateBucket) results(node *reteGraphAggregateNode, numeric *reteGraphAggregateNumericState) ([]Value, bool) {
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
			value, ok := numeric.sumValue(m.id, len(node.specs), i)
			if !ok {
				return nil, false
			}
			values[i] = value
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
	for _, terminal := range m.graph.stageTerminals(source) {
		_, _ = m.insertTerminalToken(terminal.terminalID, terminal.branchID, token, delta, span)
	}
	for _, aggregateID := range m.graph.stageAggregateOuters(source) {
		m.openAggregateBucket(aggregateID, token, span, delta)
	}
	for _, aggregateID := range m.graph.stageAggregateInputs(source) {
		m.insertAggregateToken(aggregateID, token, span, delta)
	}
	for _, successor := range m.graph.stageSuccessors(source) {
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

func (m *reteGraphBetaMemory) propagateFromBetaNode(node *reteGraphBetaNode, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) error {
	if m == nil || node == nil || delta == nil {
		return nil
	}
	edges := node.edges
	for _, terminal := range edges.terminals {
		_, _ = m.insertTerminalToken(terminal.terminalID, terminal.branchID, token, delta, span)
	}
	for _, aggregateID := range edges.aggregateOuters {
		m.openAggregateBucket(aggregateID, token, span, delta)
	}
	for _, aggregateID := range edges.aggregateInputs {
		m.insertAggregateToken(aggregateID, token, span, delta)
	}
	for _, successor := range edges.successors {
		next := m.graph.betaNode(successor.betaNodeID)
		if next == nil {
			delta.supported = false
			continue
		}
		ok, err := m.insertBetaInput(successor.betaNodeID, successor.side, token, next.entry, span, delta)
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
	for _, terminal := range m.graph.stageTerminals(source) {
		m.removeTerminalToken(terminal.terminalID, terminal.branchID, token, counters, delta)
	}
	for _, aggregateID := range m.graph.stageAggregateOuters(source) {
		m.removeAggregateBucket(aggregateID, token, counters, delta)
	}
	for _, aggregateID := range m.graph.stageAggregateInputs(source) {
		m.removeAggregateToken(aggregateID, token, counters, delta)
	}
	for _, successor := range m.graph.stageSuccessors(source) {
		if !m.removeBetaInputToken(successor.betaNodeID, successor.side, token, counters, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) propagateRemoveFactFromStage(source reteGraphStageRef, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	for _, terminal := range m.graph.stageTerminals(source) {
		m.removeTerminalTokensContainingFact(terminal.terminalID, id, counters, delta)
	}
	for _, aggregateID := range m.graph.stageAggregateOuters(source) {
		m.removeAggregateBucketsContainingFact(aggregateID, id, counters, delta)
	}
	for _, aggregateID := range m.graph.stageAggregateInputs(source) {
		m.removeAggregateMembersContainingFact(aggregateID, id, counters, delta)
	}
	for _, successor := range m.graph.stageSuccessors(source) {
		if !m.removeBetaInputContainingFact(successor.betaNodeID, successor.side, id, counters, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) insertBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, entry bindingTupleEntry, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil || token.isZero() {
		return false, nil
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false, nil
	}
	if node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter {
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
		depth := nodeMemory.right.joinRowCount(joinKey)
		if span != nil {
			span.recordBetaBucketProbe(depth)
		}
		matched := false
		var joinErr error
		nodeMemory.right.forEachJoinRow(joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			if rightRow == nil || rightRow.token.isZero() {
				return true
			}
			rightMatch, ok := tokenFactMatchForBindingSlot(rightRow.token, node.entry.bindingSlot)
			if !ok {
				return true
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, span); err != nil {
				joinErr = err
				return false
			} else if !ok {
				return true
			}
			matched = true
			m.appendBackchainDemandResolutions(node, reteGraphBetaInputRight, token, delta)
			m.appendBackchainDemandResolutions(node, reteGraphBetaInputLeft, rightRow.token, delta)
			output := m.appendTokenRows(token, rightRow.token, span)
			if output.isZero() {
				return true
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			if err := m.propagateFromBetaNode(node, output, span, delta); err != nil {
				joinErr = err
				return false
			}
			return true
		})
		if joinErr != nil {
			return false, joinErr
		}
		if !matched {
			m.appendBackchainDemandRequests(nodeID, node, reteGraphBetaInputRight, token, delta)
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenFactMatchForBindingSlot(token, node.entry.bindingSlot)
		if !ok {
			return false, nil
		}
		depth := nodeMemory.left.joinRowCount(joinKey)
		if span != nil {
			span.recordBetaBucketProbe(depth)
		}
		matched := false
		var joinErr error
		nodeMemory.left.forEachJoinRow(joinKey, func(_ graphTokenRowID, leftRow *betaTokenRow) bool {
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			if leftRow == nil || leftRow.token.isZero() {
				return true
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, span); err != nil {
				joinErr = err
				return false
			} else if !ok {
				return true
			}
			matched = true
			m.appendBackchainDemandResolutions(node, reteGraphBetaInputRight, leftRow.token, delta)
			m.appendBackchainDemandResolutions(node, reteGraphBetaInputLeft, token, delta)
			output := m.appendTokenRows(leftRow.token, token, span)
			if output.isZero() {
				return true
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			if err := m.propagateFromBetaNode(node, output, span, delta); err != nil {
				joinErr = err
				return false
			}
			return true
		})
		if joinErr != nil {
			return false, joinErr
		}
		if !matched {
			m.appendBackchainDemandRequests(nodeID, node, reteGraphBetaInputLeft, token, delta)
		}
	}
	return true, nil
}

func (m *reteGraphBetaMemory) insertGeneratedBetaRightInput(nodeID reteGraphBetaNodeID, token tokenRef, entry bindingTupleEntry, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil || token.isZero() {
		return false, nil
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false, nil
	}
	if node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter || node.kind == reteGraphBetaNodeNot || node.rightHasLeftPrefix || entry.bindingSlot != node.entry.bindingSlot {
		return m.insertBetaInput(nodeID, reteGraphBetaInputRight, token, entry, span, delta)
	}
	joinKey, ok, err := graphBetaJoinKeyForRightMatchWithContext(m.context(), node, match.fact, token, span)
	if err != nil {
		return false, err
	}
	if !ok {
		return m.insertBetaInput(nodeID, reteGraphBetaInputRight, token, entry, span, delta)
	}
	nodeMemory := m.nodeMemory(nodeID)
	inserted := nodeMemory.right.insert(token, joinKey)
	if !inserted {
		return false, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputRight)
	}
	depth := nodeMemory.left.joinRowCount(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(depth)
	}
	matched := false
	var joinErr error
	nodeMemory.left.forEachJoinRow(joinKey, func(_ graphTokenRowID, leftRow *betaTokenRow) bool {
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		if leftRow == nil || leftRow.token.isZero() {
			return true
		}
		if ok, err := m.residualJoinsMatch(node, match.fact, leftRow.token, span); err != nil {
			joinErr = err
			return false
		} else if !ok {
			return true
		}
		matched = true
		m.appendBackchainDemandResolutions(node, reteGraphBetaInputRight, leftRow.token, delta)
		m.appendBackchainDemandResolutions(node, reteGraphBetaInputLeft, token, delta)
		output := m.appendTokenRows(leftRow.token, token, span)
		if output.isZero() {
			return true
		}
		if span != nil {
			span.recordBetaJoinedTokenProduced()
		}
		if err := m.propagateFromBetaNode(node, output, span, delta); err != nil {
			joinErr = err
			return false
		}
		return true
	})
	if joinErr != nil {
		return false, joinErr
	}
	if !matched {
		m.appendBackchainDemandRequests(nodeID, node, reteGraphBetaInputLeft, token, delta)
	}
	return true, nil
}

func (m *reteGraphBetaMemory) insertGeneratedBetaLeftInput(nodeID reteGraphBetaNodeID, token tokenRef, entry bindingTupleEntry, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m == nil || delta == nil || token.isZero() {
		return false, nil
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false, nil
	}
	if node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter || node.kind == reteGraphBetaNodeNot || node.rightHasLeftPrefix {
		return m.insertBetaInput(nodeID, reteGraphBetaInputLeft, token, entry, span, delta)
	}
	joinKey, ok, err := graphBetaJoinKeyForLeftMatchWithContext(m.context(), node, entry.bindingSlot, match.fact, token, span)
	if err != nil {
		return false, err
	}
	if !ok {
		return m.insertBetaInput(nodeID, reteGraphBetaInputLeft, token, entry, span, delta)
	}
	nodeMemory := m.nodeMemory(nodeID)
	inserted := nodeMemory.left.insert(token, joinKey)
	if !inserted {
		return false, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputLeft)
	}
	depth := nodeMemory.right.joinRowCount(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(depth)
	}
	matched := false
	var joinErr error
	nodeMemory.right.forEachJoinRow(joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		if rightRow == nil || rightRow.token.isZero() {
			return true
		}
		rightMatch, ok := tokenFactMatchForBindingSlot(rightRow.token, node.entry.bindingSlot)
		if !ok {
			return true
		}
		if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, span); err != nil {
			joinErr = err
			return false
		} else if !ok {
			return true
		}
		matched = true
		m.appendBackchainDemandResolutions(node, reteGraphBetaInputRight, token, delta)
		m.appendBackchainDemandResolutions(node, reteGraphBetaInputLeft, rightRow.token, delta)
		output := m.appendTokenRows(token, rightRow.token, span)
		if output.isZero() {
			return true
		}
		if span != nil {
			span.recordBetaJoinedTokenProduced()
		}
		if err := m.propagateFromBetaNode(node, output, span, delta); err != nil {
			joinErr = err
			return false
		}
		return true
	})
	if joinErr != nil {
		return false, joinErr
	}
	if !matched {
		m.appendBackchainDemandRequests(nodeID, node, reteGraphBetaInputRight, token, delta)
	}
	return true, nil
}

func (m *reteGraphBetaMemory) appendBackchainDemandRequests(nodeID reteGraphBetaNodeID, node *reteGraphBetaNode, missingSide reteGraphBetaInputSide, context tokenRef, delta *reteAgendaDelta) {
	if m == nil || node == nil || delta == nil || context.isZero() || len(node.backchainDemands) == 0 {
		return
	}
	for planIndex, plan := range node.backchainDemands {
		if plan.side != missingSide {
			continue
		}
		id, ok := m.storeBackchainDemandRequest(nodeID, planIndex, plan, context)
		if !ok {
			delta.supported = false
			continue
		}
		delta.demands = m.appendBackchainDemandDeltaID(delta.demands, id)
	}
}

func (m *reteGraphBetaMemory) appendBackchainDemandResolutions(node *reteGraphBetaNode, side reteGraphBetaInputSide, context tokenRef, delta *reteAgendaDelta) {
	if m == nil || node == nil || delta == nil || context.isZero() || len(node.backchainDemands) == 0 {
		return
	}
	for planIndex, plan := range node.backchainDemands {
		if plan.side != side {
			continue
		}
		owner := backchainDemandOwnerKey{
			nodeID:    node.id,
			planIndex: planIndex,
			token:     context.handle,
		}
		if owner.isZero() {
			id, ok := m.storeBackchainDemandRequest(node.id, planIndex, plan, context)
			if !ok {
				delta.supported = false
				continue
			}
			delta.resolvedDemands = m.appendBackchainDemandDeltaID(delta.resolvedDemands, id)
			continue
		}
		delta.resolvedOwners = m.appendBackchainDemandOwnerKey(delta.resolvedOwners, owner)
	}
}

func (m *reteGraphBetaMemory) storeBackchainDemandRequest(nodeID reteGraphBetaNodeID, planIndex int, plan reteGraphBackchainDemandPlan, context tokenRef) (backchainDemandID, bool) {
	if m == nil || plan.templateKey == "" || plan.slotCount <= 0 {
		return 0, false
	}
	supportStart := len(m.backchainDemandSupport)
	if !m.appendBackchainDemandSupportFactsForToken(context) {
		m.backchainDemandSupport = m.backchainDemandSupport[:supportStart]
		return 0, false
	}
	supportCount := len(m.backchainDemandSupport) - supportStart
	slotStart := len(m.backchainDemandSlots)
	if len(plan.defaultSlots) == plan.slotCount {
		m.backchainDemandSlots = append(m.backchainDemandSlots, plan.defaultSlots...)
	} else {
		for i := 0; i < plan.slotCount; i++ {
			m.backchainDemandSlots = append(m.backchainDemandSlots, factSlot{
				value:    NullValue(),
				ok:       true,
				presence: fieldPresenceExplicit,
			})
		}
	}
	for _, slot := range plan.constSlots {
		if slot.slot < 0 || slot.slot >= plan.slotCount {
			continue
		}
		m.backchainDemandSlots[slotStart+slot.slot].value = cloneDemandSlotValue(slot.value)
	}
	for _, slot := range plan.joinSlots {
		if slot.slot < 0 || slot.slot >= plan.slotCount {
			continue
		}
		var match conditionMatch
		var ok bool
		if slot.last {
			match, ok := tokenLastMatch(context)
			if !ok || match.hasValue {
				continue
			}
			value, ok := slot.access.valueFromFact(match.fact)
			if ok {
				m.backchainDemandSlots[slotStart+slot.slot].value = cloneDemandSlotValue(value)
			}
			continue
		}
		match, ok = tokenRefAtSlot(context, slot.bindingSlot)
		if !ok || match.hasValue {
			continue
		}
		value, ok := slot.access.valueFromFact(match.fact)
		if ok {
			m.backchainDemandSlots[slotStart+slot.slot].value = cloneDemandSlotValue(value)
		}
	}
	id := m.nextBackchainDemandIDValue()
	m.backchainDemandRecords = append(m.backchainDemandRecords, backchainDemandRecord{
		id:           id,
		templateKey:  plan.templateKey,
		slotStart:    slotStart,
		slotCount:    plan.slotCount,
		supportStart: supportStart,
		supportCount: supportCount,
		owner: backchainDemandOwnerKey{
			nodeID:    nodeID,
			planIndex: planIndex,
			token:     context.handle,
		},
	})
	return id, true
}

func cloneDemandSlotValue(value Value) Value {
	if valueShareable(value) {
		return value
	}
	return cloneValue(value)
}

func (m *reteGraphBetaMemory) appendBackchainDemandSupportFactsForToken(token tokenRef) bool {
	if m == nil {
		return false
	}
	ids, idsOK := token.factIDs()
	versions, versionsOK := token.factVersions()
	if idsOK && versionsOK && len(ids) > 0 && len(ids) == len(versions) {
		start := len(m.backchainDemandSupport)
		for i, id := range ids {
			if id.IsZero() {
				continue
			}
			m.backchainDemandSupport = append(m.backchainDemandSupport, backchainDemandSupportFact{
				id:      id,
				version: versions[i],
			})
		}
		return len(m.backchainDemandSupport) > start
	}
	if _, ok := token.resolve(); !ok {
		return false
	}
	start := len(m.backchainDemandSupport)
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return false
		}
		entry := row.tokenRowEntry()
		if entry.hasValue || entry.factID.IsZero() {
			continue
		}
		m.backchainDemandSupport = append(m.backchainDemandSupport, backchainDemandSupportFact{
			id:      entry.factID,
			version: entry.factVersion,
		})
	}
	slices.SortFunc(m.backchainDemandSupport[start:], compareBackchainDemandSupportFacts)
	return len(m.backchainDemandSupport) > start
}

func compareBackchainDemandSupportFacts(left, right backchainDemandSupportFact) int {
	if left.id.generation != right.id.generation {
		return cmpUint64(uint64(left.id.generation), uint64(right.id.generation))
	}
	if left.id.sequence != right.id.sequence {
		return cmpUint64(left.id.sequence, right.id.sequence)
	}
	return cmpUint64(uint64(left.version), uint64(right.version))
}

func cmpUint64(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
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
	negativeMemory := m.negativeBetaMemory(nodeID, node)
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok, err := graphBetaJoinKeyForLeftTokenWithContext(m.context(), node, token, span)
		if err != nil || !ok {
			return false, err
		}
		return negativeMemory.insertLeft(joinKey, token, span, delta)
	case reteGraphBetaInputRight:
		joinKey, ok, err := graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, span)
		if err != nil || !ok {
			return false, err
		}
		return negativeMemory.insertRight(joinKey, token, span, delta)
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
	if node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter {
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
		removedRow, removedOK := nodeMemory.left.removeTokenWithJoinKey(token, joinKey, counters)
		removed, ok = removedRow.token, removedOK
	case reteGraphBetaInputRight:
		var err error
		joinKey, ok, err = graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, nil)
		if err != nil || !ok {
			return false
		}
		removedRow, removedOK := nodeMemory.right.removeTokenWithJoinKey(token, joinKey, counters)
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
	removedRow, removedOK := nodeMemory.left.removeTokenWithJoinKey(token, betaJoinKey{}, counters)
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
	negativeMemory := m.negativeBetaMemory(nodeID, node)
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok, err := graphBetaJoinKeyForLeftTokenWithContext(m.context(), node, token, nil)
		if err != nil || !ok {
			return false
		}
		return negativeMemory.removeLeft(joinKey, token, counters, delta)
	case reteGraphBetaInputRight:
		joinKey, ok, err := graphBetaJoinKeyForRightTokenWithContext(m.context(), node, token, nil)
		if err != nil || !ok {
			return false
		}
		return negativeMemory.removeRight(joinKey, token, counters, delta)
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
		nodeMemory.right.forEachJoinRow(joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
			if rightRow == nil || rightRow.token.isZero() {
				return true
			}
			rightMatch, ok := tokenFactMatchForBindingSlot(rightRow.token, node.entry.bindingSlot)
			if !ok {
				return true
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				return true
			}
			output := m.newTokenRef(token, node.entry, rightMatch, rightMatch.fact.Recency(), rightMatch.fact.Generation(), nil)
			if output.isZero() {
				delta.supported = false
				return true
			}
			m.propagateRemoveFromStage(source, output, counters, delta)
			if ok, supported := m.rightTokenHasLeftMatch(node, nodeMemory, joinKey, rightRow.token); !supported {
				delta.supported = false
			} else if !ok {
				m.appendBackchainDemandRequests(node.id, node, reteGraphBetaInputLeft, rightRow.token, delta)
			}
			return true
		})
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			delta.supported = false
			return
		}
		nodeMemory.left.forEachJoinRow(joinKey, func(_ graphTokenRowID, leftRow *betaTokenRow) bool {
			if leftRow == nil || leftRow.token.isZero() {
				return true
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				return true
			}
			output := m.newTokenRef(leftRow.token, node.entry, currentMatch, currentMatch.fact.Recency(), currentMatch.fact.Generation(), nil)
			if output.isZero() {
				delta.supported = false
				return true
			}
			m.propagateRemoveFromStage(source, output, counters, delta)
			if ok, supported := m.leftTokenHasRightMatch(node, nodeMemory, joinKey, leftRow.token); !supported {
				delta.supported = false
			} else if !ok {
				m.appendBackchainDemandRequests(node.id, node, reteGraphBetaInputRight, leftRow.token, delta)
			}
			return true
		})
	default:
		delta.supported = false
	}
}

func (m *reteGraphBetaMemory) rightTokenHasLeftMatch(node *reteGraphBetaNode, nodeMemory *reteGraphBetaNodeMemory, joinKey betaJoinKey, right tokenRef) (bool, bool) {
	if m == nil || node == nil || nodeMemory == nil || right.isZero() {
		return false, false
	}
	rightMatch, ok := tokenFactMatchForBindingSlot(right, node.entry.bindingSlot)
	if !ok {
		return false, false
	}
	matched := false
	supported := true
	nodeMemory.left.forEachJoinRow(joinKey, func(_ graphTokenRowID, leftRow *betaTokenRow) bool {
		if leftRow == nil || leftRow.token.isZero() {
			return true
		}
		ok, err := m.residualJoinsMatch(node, rightMatch.fact, leftRow.token, nil)
		if err != nil {
			supported = false
			return false
		}
		if ok {
			matched = true
			return false
		}
		return true
	})
	return matched, supported
}

func (m *reteGraphBetaMemory) leftTokenHasRightMatch(node *reteGraphBetaNode, nodeMemory *reteGraphBetaNodeMemory, joinKey betaJoinKey, left tokenRef) (bool, bool) {
	if m == nil || node == nil || nodeMemory == nil || left.isZero() {
		return false, false
	}
	matched := false
	supported := true
	nodeMemory.right.forEachJoinRow(joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
		if rightRow == nil || rightRow.token.isZero() {
			return true
		}
		rightMatch, ok := tokenFactMatchForBindingSlot(rightRow.token, node.entry.bindingSlot)
		if !ok {
			supported = false
			return false
		}
		ok, err := m.residualJoinsMatch(node, rightMatch.fact, left, nil)
		if err != nil {
			supported = false
			return false
		}
		if ok {
			matched = true
			return false
		}
		return true
	})
	return matched, supported
}

func (m *reteGraphBetaMemory) removeBetaInputContainingFact(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || id.IsZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	if node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter {
		return m.removeFilterBetaInputContainingFact(nodeID, side, node, id, counters, delta)
	}
	if node.kind == reteGraphBetaNodeNot {
		return m.negativeBetaMemory(nodeID, node).removeContainingFact(side, id, counters, delta)
	}
	nodeMemory := m.nodeMemory(nodeID)
	removeToken := func(row betaTokenRow) {
		m.propagateJoinedRemovals(nodeID, side, node, nodeMemory, row.joinKey, row.token, counters, delta)
	}
	switch side {
	case reteGraphBetaInputLeft:
		nodeMemory.left.removeTokensContainingFact(id, counters, removeToken)
	case reteGraphBetaInputRight:
		nodeMemory.right.removeTokensContainingFact(id, counters, removeToken)
	default:
		return false
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
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}
	nodeMemory.left.removeTokensContainingFact(id, counters, func(row betaTokenRow) {
		m.propagateRemoveFromStage(source, row.token, counters, delta)
	})
	return true
}

func (m *reteGraphBetaMemory) removeFact(ctx context.Context, fact FactSnapshot, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	return m.removeFactInternal(ctx, fact, counters, true)
}

func (m *reteGraphBetaMemory) removeWorkingFact(ctx context.Context, fact *workingFact, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	delta := reteAgendaDelta{supported: true}
	id := fact.id
	nodeIDs := m.workingAlphaRouteIDsForFact(fact, nil)
	if len(nodeIDs) == 0 {
		m.removeAlphaFact(id)
		return delta, nil
	}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		ok, err := node.matchesWorkingWithContextAndCounters(ctx, fact, m.compactSlotStore, nil)
		if err != nil {
			return delta, err
		}
		if !ok {
			continue
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromWorkingFactForTarget(fact, node.target, m.compactSlotStore),
		}
		m.propagateRemoveAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, counters, &delta)
	}
	m.removeAlphaFact(id)
	return delta, nil
}

func (m *reteGraphBetaMemory) removeGeneratedWorkingFact(ctx context.Context, fact *workingFact, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	delta := reteAgendaDelta{supported: true}
	id := fact.id
	if m.removeGeneratedTerminalOnlyFact(id, counters, &delta) {
		return delta, nil
	}
	nodeIDs := m.matchedAlphaRouteIDsForFact(id)
	if len(nodeIDs) == 0 {
		m.removeAlphaFact(id)
		return delta, nil
	}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromWorkingFactForTarget(fact, node.target, m.compactSlotStore),
		}
		m.removeGeneratedAlphaOps(nodeID, node, match, counters, &delta)
	}
	m.removeAlphaFact(id)
	return delta, nil
}

func (m *reteGraphBetaMemory) removeGeneratedTerminalOnlyFact(id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || m.graph == nil || delta == nil || id.IsZero() {
		return false
	}
	row := m.alpha.factOwnership[id]
	rows := row.terminalRows
	if len(rows) == 0 {
		return false
	}
	routes := row.routes
	if len(routes) == 0 {
		return false
	}
	terminalOps := 0
	for _, nodeID := range routes {
		node := m.graph.alphaNode(nodeID)
		if node == nil || len(node.generatedOps) == 0 || len(node.listPatterns) != 0 {
			return false
		}
		for _, op := range node.generatedOps {
			if op.kind != reteGraphGeneratedAlphaOpTerminal || op.entry.conditionID == "" {
				return false
			}
			terminalOps++
		}
	}
	if terminalOps != len(rows) {
		return false
	}
	for _, row := range rows {
		terminal := m.terminalAt(row.terminalID)
		if terminal == nil {
			return false
		}
		if _, ok := terminal.rows.rowIDByHandle(row.handle); !ok {
			return false
		}
	}
	for _, row := range rows {
		if !m.removeTerminalTokenByHandle(row.terminalID, row.branchID, row.handle, counters, delta) {
			return false
		}
	}
	m.removeAlphaFactRoutes(id, routes, true)
	return true
}

func (m *reteGraphBetaMemory) removeWorkingFactForModifyInternal(ctx context.Context, fact *workingFact, snapshot FactSnapshot, counters *propagationCounterLedger, updateSource bool) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	delta := reteAgendaDelta{supported: true}
	id := fact.id
	nodeIDs := m.matchedAlphaRouteIDsForFact(id)
	if len(nodeIDs) == 0 {
		m.removeAlphaFact(id)
		return delta, nil
	}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		ok, err := m.workingFactMatchesAlphaNode(ctx, fact, snapshot, node, nil)
		if err != nil {
			return delta, err
		}
		if !ok {
			continue
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        m.workingFactRefForAlphaNode(fact, snapshot, node),
		}
		m.propagateRemoveAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, counters, &delta)
	}
	m.removeAlphaFact(id)
	return delta, nil
}

func (m *reteGraphBetaMemory) removeFactInternal(ctx context.Context, fact FactSnapshot, counters *propagationCounterLedger, updateSource bool) (reteAgendaDelta, error) {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	delta := reteAgendaDelta{supported: true}
	id := fact.ID()
	nodeIDs := m.matchedAlphaRouteIDsForFact(id)
	if len(nodeIDs) == 0 {
		m.removeAlphaFact(id)
		return delta, nil
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

func (m *reteGraphBetaMemory) recordRemovedTerminalRowBranches(counters *propagationCounterLedger, terminalID reteGraphTerminalNodeID, terminal *reteGraphTerminalMemory, row graphTokenRow) {
	if counters == nil || terminal == nil {
		return
	}
	if terminal.needsBranchSupport() {
		row.forEachTerminalBranchSupport(func(support terminalBranchSupport) {
			if support.count <= 0 {
				return
			}
			if key, ok := m.terminalBranchKey(terminalID, support.branchID); ok {
				counters.recordTerminalDeltaRemovedForBranch(key)
				counters.recordTerminalRowRemovedForBranch(key)
			}
		})
		return
	}
	if !terminal.singleBranch() {
		return
	}
	if key, ok := m.terminalBranchKey(terminalID, terminal.singleBranchID); ok {
		counters.recordTerminalDeltaRemovedForBranch(key)
		counters.recordTerminalRowRemovedForBranch(key)
	}
}

func (m *reteGraphBetaMemory) removeFactByIndexes(id FactID, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
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
			m.appendRemovedTerminalDelta(&delta, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          row.token,
				identity:       terminal.rowCandidateIdentity(row),
				terminalID:     terminalNode.id,
				terminalRow:    row.handle,
			})
			if counters != nil {
				counters.recordTerminalDeltaRemoved()
				counters.recordTerminalRowRemoved()
				m.recordRemovedTerminalRowBranches(counters, terminalNode.id, terminal, row)
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
		node.negative.left.removeTokensContainingFact(id, counters, nil)
		node.negative.right.removeTokensContainingFact(id, counters, nil)
	}
	return delta
}

func (m *reteGraphBetaMemory) recordAlphaFact(nodeID reteGraphAlphaNodeID, fact conditionFactRef) {
	if m == nil || nodeID <= 0 || fact.ID().IsZero() {
		return
	}
	index := int(nodeID)
	if index <= 0 || index >= len(m.alpha.facts) {
		return
	}
	if !m.alpha.facts[index].insert(fact.ID()) {
		return
	}
	factID := fact.ID()
	if m.alpha.factOwnership == nil {
		m.alpha.factOwnership = make(map[FactID]alphaFactOwnershipRow)
	}
	row, exists := m.alpha.factOwnership[factID]
	if !exists {
		m.alpha.factOwnershipIDs = append(m.alpha.factOwnershipIDs, factID)
	}
	if next, inserted := m.appendAlphaFactRouteOrdered(row.routes, nodeID); inserted {
		row.routes = next
		m.alpha.factOwnership[factID] = row
	}
	if m.alpha.factCounts == nil {
		m.alpha.factCounts = make(map[ConditionID]int)
	}
	for _, conditionID := range m.alpha.conditions[index] {
		m.alpha.factCounts[conditionID]++
	}
}

func (m *reteGraphBetaMemory) recordGeneratedTerminalRow(factID FactID, nodeID reteGraphAlphaNodeID, terminalID reteGraphTerminalNodeID, branchID int, handle graphTokenRowHandle) {
	if m == nil || factID.IsZero() || nodeID <= 0 || terminalID <= 0 || handle.isZero() {
		return
	}
	if m.alpha.factOwnership == nil {
		m.alpha.factOwnership = make(map[FactID]alphaFactOwnershipRow)
	}
	row, exists := m.alpha.factOwnership[factID]
	if !exists {
		m.alpha.factOwnershipIDs = append(m.alpha.factOwnershipIDs, factID)
	}
	row.terminalRows = m.appendGeneratedTerminalRow(row.terminalRows, generatedTerminalRowHandle{
		alphaNodeID: nodeID,
		terminalID:  terminalID,
		branchID:    branchID,
		handle:      handle,
	})
	m.alpha.factOwnership[factID] = row
}

func (m *reteGraphBetaMemory) takeGeneratedTerminalRow(factID FactID, nodeID reteGraphAlphaNodeID, terminalID reteGraphTerminalNodeID, branchID int) (graphTokenRowHandle, bool) {
	if m == nil || factID.IsZero() {
		return graphTokenRowHandle{}, false
	}
	row := m.alpha.factOwnership[factID]
	rows := row.terminalRows
	for i, row := range rows {
		if row.alphaNodeID != nodeID || row.terminalID != terminalID || row.branchID != branchID {
			continue
		}
		last := len(rows) - 1
		handle := row.handle
		rows[i] = rows[last]
		rows[last] = generatedTerminalRowHandle{}
		rows = rows[:last]
		owner := m.alpha.factOwnership[factID]
		owner.terminalRows = rows
		if len(owner.routes) == 0 && len(owner.terminalRows) == 0 {
			delete(m.alpha.factOwnership, factID)
		} else {
			m.alpha.factOwnership[factID] = owner
		}
		return handle, true
	}
	return graphTokenRowHandle{}, false
}

func (m *reteGraphBetaMemory) removeAlphaFact(id FactID) {
	if m == nil || id.IsZero() {
		return
	}
	row := m.alpha.factOwnership[id]
	m.removeAlphaFactRoutes(id, row.routes, true)
}

func (m *reteGraphBetaMemory) removeAlphaFactRoutes(id FactID, routes []reteGraphAlphaNodeID, removeTerminalRows bool) {
	if m == nil || id.IsZero() {
		return
	}
	for _, nodeID := range routes {
		index := int(nodeID)
		if index <= 0 || index >= len(m.alpha.facts) || !m.alpha.facts[index].remove(id) {
			continue
		}
		for _, conditionID := range m.alpha.conditions[index] {
			if m.alpha.factCounts[conditionID] <= 1 {
				delete(m.alpha.factCounts, conditionID)
				continue
			}
			m.alpha.factCounts[conditionID]--
		}
	}
	if removeTerminalRows {
		delete(m.alpha.factOwnership, id)
		return
	}
	row := m.alpha.factOwnership[id]
	row.routes = nil
	if len(row.terminalRows) == 0 {
		delete(m.alpha.factOwnership, id)
		return
	}
	m.alpha.factOwnership[id] = row
}

func (m *reteGraphBetaMemory) alphaFactCount(conditionID ConditionID) int {
	if m == nil || conditionID == "" {
		return 0
	}
	return m.alpha.factCounts[conditionID]
}

func (m *reteGraphBetaMemory) updateFact(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, error) {
	if m == nil {
		return reteAgendaDelta{}, nil
	}
	defer m.pushEvalContext(ctx)()
	if m.canSkipUnroutedModifyPropagation(event) {
		m.sourceGenerationValue = event.after.Generation()
		if event.counters != nil {
			event.counters.recordModifyFastPathSkip()
		}
		return reteAgendaDelta{supported: true}, nil
	}
	if m.canSkipUnmatchedModifyPropagation(event) {
		m.sourceGenerationValue = event.after.Generation()
		if event.counters != nil {
			event.counters.recordModifyFastPathSkip()
		}
		return reteAgendaDelta{supported: true}, nil
	}
	if delta, ok, err := m.refreshRouteScopedModifyByEvents(ctx, event); ok {
		if err != nil {
			return delta, err
		}
		return delta, nil
	}
	if event.counters != nil {
		event.counters.recordModifyFastPathFallback()
	}
	return reteAgendaDelta{}, fmt.Errorf("%w: modify event is not supported by graph-native propagation", ErrUnsupportedRuntime)
}

func (m *reteGraphBetaMemory) refreshRouteScopedModifyByEvents(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, bool, error) {
	if m == nil || m.graph == nil || len(event.changes) == 0 {
		return reteAgendaDelta{}, false, nil
	}
	before, after := event.before, event.after
	if before.ID() != after.ID() || before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() || event.templateChanged || event.nameChanged {
		return reteAgendaDelta{}, false, nil
	}
	nodeIDs := slices.Clone(m.matchedAlphaRouteIDsForFact(before.ID()))
	for _, nodeID := range m.snapshotAlphaRouteIDsForFact(after) {
		if !slices.Contains(nodeIDs, nodeID) {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	if len(nodeIDs) == 0 {
		return reteAgendaDelta{}, false, nil
	}
	scope := m.modifyRouteScopeForAlphaRoutes(nodeIDs)
	for _, betaNodeID := range scope.betaNodes {
		betaNode := m.graph.betaNode(betaNodeID)
		if betaNode == nil {
			return reteAgendaDelta{}, false, nil
		}
	}
	if len(scope.betaNodes) == 0 && len(scope.aggregateNodes) == 0 && len(scope.terminalNodes) == 0 {
		return reteAgendaDelta{}, false, nil
	}
	removed, err := m.propagateEvent(ctx, newReteGraphModifyRemoveEvent(event))
	if err != nil {
		return removed, true, err
	}
	added, err := m.propagateEvent(ctx, newReteGraphModifyAddEvent(event))
	if err != nil {
		return added, true, err
	}
	m.sourceGenerationValue = after.Generation()
	addedTokens, removedTokens := coalesceTerminalTokenDeltas(m.revision, append(removed.added, added.added...), append(removed.removed, added.removed...))
	return reteAgendaDelta{
		supported:       removed.supported && added.supported,
		added:           addedTokens,
		removed:         removedTokens,
		updated:         append(removed.updated, added.updated...),
		demands:         append(removed.demands, added.demands...),
		resolvedDemands: append(removed.resolvedDemands, added.resolvedDemands...),
		resolvedOwners:  append(removed.resolvedOwners, added.resolvedOwners...),
	}, true, nil
}

func (m *reteGraphBetaMemory) canSkipUnroutedModifyPropagation(event reteGraphPropagationEvent) bool {
	if m == nil || m.graph == nil {
		return false
	}
	before, after := event.before, event.after
	if before.ID() != after.ID() || before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() || event.templateChanged || event.nameChanged {
		return false
	}
	if len(m.matchedAlphaRouteIDsForFact(before.ID())) != 0 {
		return false
	}
	return len(m.snapshotAlphaRouteIDsForFact(after)) == 0
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

func canUseWorkingFactForAlphaTarget(fact *workingFact, target conditionTarget) bool {
	if fact == nil {
		return false
	}
	switch target.kind {
	case conditionTargetTemplateKey:
		return fact.matchesTemplateTarget(target)
	case conditionTargetName:
		return fact.storedName() == target.name
	default:
		return false
	}
}

func (m *reteGraphBetaMemory) workingFactMatchesAlphaNode(ctx context.Context, fact *workingFact, snapshot FactSnapshot, node *reteGraphAlphaNode, span *propagationCounterSpan) (bool, error) {
	if m == nil || node == nil {
		return false, nil
	}
	if canUseWorkingFactForAlphaTarget(fact, node.target) {
		return node.matchesWorkingWithContextAndCounters(ctx, fact, m.compactSlotStore, span)
	}
	return node.matchesSnapshotWithContextAndCounters(ctx, snapshot, span)
}

func (m *reteGraphBetaMemory) workingFactRefForAlphaNode(fact *workingFact, snapshot FactSnapshot, node *reteGraphAlphaNode) conditionFactRef {
	if m != nil && node != nil && canUseWorkingFactForAlphaTarget(fact, node.target) {
		ref := newConditionFactRefFromWorkingFactForTarget(fact, node.target, m.compactSlotStore)
		if ref.name == "" {
			ref.name = snapshot.Name()
		}
		if ref.templateKey == "" {
			ref.templateKey = snapshot.TemplateKey()
		}
		if len(ref.fieldSpecs) == 0 {
			ref.fieldSpecs = snapshot.fieldSpecs
		}
		return ref
	}
	return newConditionFactRefFromSnapshot(snapshot)
}

func (m *reteGraphBetaMemory) modifyAfterMatchesAlphaNode(ctx context.Context, event reteGraphPropagationEvent, node *reteGraphAlphaNode) (bool, error) {
	return m.workingFactMatchesAlphaNode(ctx, event.afterWorkingFact, event.after, node, nil)
}

func (m *reteGraphBetaMemory) modifyAfterFactRefForAlphaNode(event reteGraphPropagationEvent, node *reteGraphAlphaNode) conditionFactRef {
	return m.workingFactRefForAlphaNode(event.afterWorkingFact, event.after, node)
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
		for _, terminal := range m.graph.stageTerminals(stage) {
			scope.appendTerminal(terminal.terminalID)
		}
		for _, successor := range m.graph.stageSuccessors(stage) {
			scope.appendBeta(successor.betaNodeID)
			scope.appendStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(successor.betaNodeID)})
		}
		for _, aggregateID := range m.graph.stageAggregateOuters(stage) {
			scope.appendAggregate(aggregateID)
			scope.appendStage(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregateID)})
		}
		for _, aggregateID := range m.graph.stageAggregateInputs(stage) {
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
			if terminalTokenDeltasCoalesceEqual(revision, add, remove) {
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

func terminalTokenDeltasCoalesceEqual(revision *Ruleset, left, right reteTerminalTokenDelta) bool {
	if terminalTokenDeltasEqual(revision, left, right) {
		return true
	}
	if left.ruleRevisionID != right.ruleRevisionID || left.terminalID != right.terminalID {
		return false
	}
	return left.token == right.token
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

func (m *reteGraphBetaMemory) insertTerminalToken(terminalID reteGraphTerminalNodeID, branchID int, token tokenRef, delta *reteAgendaDelta, span *propagationCounterSpan) (graphTokenRowHandle, bool) {
	if m == nil || delta == nil || token.isZero() {
		return graphTokenRowHandle{}, false
	}
	terminal := m.terminal(terminalID)
	if terminal == nil {
		delta.supported = false
		return graphTokenRowHandle{}, false
	}
	ruleRevisionID := terminal.ruleRevisionID
	ruleTerminal := ruleRevisionID != ""
	branchKey, haveBranchKey := m.terminalBranchKey(terminalID, branchID)
	if !ruleTerminal {
		inserted := terminal.queryRows.insertRow(token)
		if !inserted {
			if span != nil {
				span.recordTerminalRowDeduped()
				if haveBranchKey {
					span.recordTerminalRowDedupedForBranch(branchKey)
				}
			}
			return graphTokenRowHandle{}, false
		}
		if span != nil {
			span.recordTerminalRowInserted()
			if haveBranchKey {
				span.recordTerminalRowInsertedForBranch(branchKey)
			}
		}
		return graphTokenRowHandle{}, true
	}
	handle := graphTokenRowHandle{}
	inserted := false
	rowBranchID := branchID
	if !terminal.needsBranchSupport() {
		rowBranchID = -1
	}
	identity := candidateIdentity{}
	if ruleTerminal {
		identity = terminal.terminalTokenIdentity(token)
	}
	if m.initialAgenda != nil && terminal.singleBranch() {
		handle = terminal.rows.insertFreshTerminalRow(token, rowBranchID, identity)
		inserted = !handle.isZero()
	} else {
		handle, inserted = terminal.rows.insertTerminalRow(token, rowBranchID, identity)
	}
	if !inserted {
		if span != nil {
			span.recordTerminalRowDeduped()
			if haveBranchKey {
				span.recordTerminalRowDedupedForBranch(branchKey)
			}
		}
		return handle, false
	}
	if span != nil {
		span.recordTerminalRowInserted()
		if haveBranchKey {
			span.recordTerminalRowInsertedForBranch(branchKey)
		}
	}
	if span != nil && ruleTerminal && !m.suppressTerminalDeltas {
		span.recordTerminalDeltaEmitted()
		if haveBranchKey {
			span.recordTerminalDeltaEmittedForBranch(branchKey)
		}
	}
	added := reteTerminalTokenDelta{
		ruleRevisionID: ruleRevisionID,
		token:          token,
		identity:       identity,
		terminalID:     terminalID,
		terminalRow:    handle,
	}
	if m.initialAgenda != nil && m.initialAgendaErr == nil {
		_, err := m.initialAgenda.addInitialTerminalActivation(m.context(), m.revision, added)
		if err != nil {
			m.initialAgendaErr = err
			delta.supported = false
		}
	}
	if m.suppressTerminalDeltas {
		return handle, true
	}
	delta.added = append(delta.added, added)
	return handle, true
}

func (m *reteGraphBetaMemory) removeTerminalTokensContainingFact(terminalID reteGraphTerminalNodeID, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || id.IsZero() {
		return
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return
	}
	ruleRevisionID := terminal.ruleRevisionID
	ruleTerminal := ruleRevisionID != ""
	if !ruleTerminal {
		removed := terminal.queryRows.removeContainingFact(id, counters)
		if counters != nil {
			for range removed {
				counters.recordTerminalRowRemoved()
				if key, ok := m.terminalBranchKey(terminalID, -1); ok {
					counters.recordTerminalRowRemovedForBranch(key)
				}
			}
		}
		return
	}
	terminal.rows.removeTokensContainingFact(id, counters, func(row graphTokenRow) {
		m.appendRemovedTerminalDelta(delta, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          row.token,
			identity:       terminal.rowCandidateIdentity(row),
			terminalID:     terminalID,
			terminalRow:    row.handle,
		})
		if counters != nil {
			counters.recordTerminalRowRemoved()
			counters.recordTerminalDeltaRemoved()
			m.recordRemovedTerminalRowBranches(counters, terminalID, terminal, row)
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
	if terminal.ruleRevisionID == "" {
		terminal.queryRows.forEachTokenContainingFact(id, nil, func(token tokenRef) {
			if token.isZero() {
				return
			}
			tokens = append(tokens, token)
		})
	} else {
		needsBranchSupport := terminal.needsBranchSupport()
		terminal.rows.forEachTokenContainingFact(id, nil, func(row graphTokenRow) {
			if row.token.isZero() || (needsBranchSupport && !row.hasTerminalBranchSupport(branchID)) {
				return
			}
			tokens = append(tokens, row.token)
		})
	}
	m.removalTokenScratch = tokens
	for _, token := range tokens {
		m.removeTerminalToken(terminalID, branchID, token, counters, delta)
	}
}

func (m *reteGraphBetaMemory) removeQueryTerminalToken(terminalID reteGraphTerminalNodeID, branchID int, token tokenRef, counters *propagationCounterLedger) {
	if m == nil || token.isZero() {
		return
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return
	}
	removed := terminal.queryRows.removeToken(token, counters)
	if !removed {
		return
	}
	if counters != nil {
		counters.recordTerminalRowRemoved()
		if key, ok := m.terminalBranchKey(terminalID, branchID); ok {
			counters.recordTerminalRowRemovedForBranch(key)
		}
	}
}

func (m *reteGraphBetaMemory) removeRuleTerminalToken(terminalID reteGraphTerminalNodeID, branchID int, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || token.isZero() {
		return
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return
	}
	identity := terminal.terminalTokenIdentity(token)
	removed, ok := terminal.rows.removeTokenWithIdentity(token, identity, counters, branchID)
	if !ok {
		return
	}
	ruleRevisionID := terminal.ruleRevisionID
	m.appendRemovedTerminalDelta(delta, reteTerminalTokenDelta{
		ruleRevisionID: ruleRevisionID,
		token:          removed.token,
		identity:       terminal.rowCandidateIdentity(removed),
		terminalID:     terminalID,
		terminalRow:    removed.handle,
	})
	if counters != nil {
		counters.recordTerminalRowRemoved()
		counters.recordTerminalDeltaRemoved()
		counters.recordNegativeTerminalRowRemoved()
		if key, ok := m.terminalBranchKey(terminalID, branchID); ok {
			counters.recordTerminalRowRemovedForBranch(key)
			counters.recordTerminalDeltaRemovedForBranch(key)
		}
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
	if terminal.ruleRevisionID == "" {
		m.removeQueryTerminalToken(terminalID, branchID, token, counters)
		return
	}
	m.removeRuleTerminalToken(terminalID, branchID, token, counters, delta)
}

func (m *reteGraphBetaMemory) removeTerminalTokenByHandle(terminalID reteGraphTerminalNodeID, branchID int, handle graphTokenRowHandle, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || handle.isZero() {
		return false
	}
	terminal := m.terminalAt(terminalID)
	if terminal == nil {
		return false
	}
	removed, deleted, consumed := terminal.rows.removeTokenByHandle(handle, counters, branchID)
	if !consumed {
		return false
	}
	if !deleted {
		return true
	}
	ruleRevisionID := terminal.ruleRevisionID
	ruleTerminal := ruleRevisionID != ""
	if ruleTerminal {
		m.appendRemovedTerminalDelta(delta, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          removed.token,
			identity:       terminal.rowCandidateIdentity(removed),
			terminalID:     terminalID,
			terminalRow:    removed.handle,
		})
	}
	if counters != nil {
		counters.recordTerminalRowRemoved()
		if ruleTerminal {
			counters.recordTerminalDeltaRemoved()
			counters.recordNegativeTerminalRowRemoved()
		}
		if key, ok := m.terminalBranchKey(terminalID, branchID); ok {
			counters.recordTerminalRowRemovedForBranch(key)
			if ruleTerminal {
				counters.recordTerminalDeltaRemovedForBranch(key)
			}
		}
	}
	return true
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
		for rowIndex, row := range terminal.rows.rows {
			if row.token.isZero() {
				continue
			}
			token := terminal.rows.rowToken(row)
			handle := terminal.rows.rowHandle(graphTokenRowID(rowIndex), row)
			deltas = append(deltas, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          token,
				identity:       terminal.terminalTokenIdentity(token),
				terminalID:     terminalNode.id,
				terminalRow:    handle,
			})
		}
	}
	m.terminalTokenDeltas = deltas
	return deltas, true, nil
}

func (m *reteGraphBetaMemory) queryRows(ctx context.Context, query compiledQuery, args *compiledQueryArgs, event reteGraphPropagationEvent, source Snapshot) ([]QueryRow, bool, error) {
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
	if source.revision == nil {
		source.revision = m.revision
	}

	_, _ = m.propagateEvent(ctx, newReteGraphQueryTriggerRemoveEvent(trigger))
	delta, err := m.propagateEvent(ctx, event)
	if err != nil {
		return nil, true, err
	}
	defer func() {
		_, _ = m.propagateEvent(context.Background(), newReteGraphQueryTriggerRemoveEvent(trigger))
	}()
	if len(delta.demands) > 0 || len(delta.resolvedDemands) > 0 || len(delta.resolvedOwners) > 0 {
		return nil, true, fmt.Errorf("%w: query %q generated backchain demand facts; query-time backward chaining is not supported", ErrUnsupportedRuntime, query.name)
	}

	rows, err := m.materializeQueryTerminalRows(ctx, query, args, source, terminalIDs, trigger.ID())
	if err != nil {
		return nil, true, err
	}
	return rows, true, nil
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
		capacity += terminal.queryRows.len()
	}
	return capacity
}

type reteGraphQueryCollector struct {
	ctx         context.Context
	query       compiledQuery
	args        *compiledQueryArgs
	source      Snapshot
	rows        []QueryRow
	rowItems    []queryRowValue
	rowValues   []Value
	rowOwner    *queryRowOwner
	rowCapacity int
	valueRows   bool
}

func (m *reteGraphBetaMemory) materializeQueryTerminalRows(ctx context.Context, query compiledQuery, args *compiledQueryArgs, source Snapshot, terminalIDs []reteGraphTerminalNodeID, triggerID FactID) ([]QueryRow, error) {
	collector := reteGraphQueryCollector{
		ctx:       ctx,
		query:     query,
		args:      args,
		source:    source,
		valueRows: query.valueReturnsOnly(),
	}
	if !collector.valueRows {
		collector.rowOwner = newQueryRowOwner(source)
	}
	if capacity := m.queryTerminalRowCapacity(terminalIDs); capacity > 0 {
		collector.rowCapacity = capacity
		collector.rows = make([]QueryRow, 0, capacity)
	}
	for _, terminalID := range terminalIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		terminal := m.terminalAt(terminalID)
		if terminal == nil {
			continue
		}
		for _, terminalRow := range terminal.queryRows.rows {
			if terminalRow.token.isZero() || !terminalRow.token.containsFact(triggerID) {
				continue
			}
			if err := m.queryCollectTerminalToken(terminalRow.token, &collector); err != nil {
				return nil, err
			}
		}
	}
	return collector.rows, nil
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
		rows := chunkRows
		if c.rowCapacity > 0 {
			rows = c.rowCapacity
		}
		c.rowValues = make([]Value, 0, count*rows)
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
		rows := queryMixedProjectionChunkRows
		if c.rowCapacity > 0 {
			rows = c.rowCapacity
		}
		c.rowItems = make([]queryRowValue, 0, count*rows)
	}
	start := len(c.rowItems)
	end := start + count
	c.rowItems = c.rowItems[:end]
	return c.rowItems[start:end]
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

func (m *reteGraphBetaMemory) newNegativeOutputTokenRef(parent tokenRef, span *propagationCounterSpan) tokenRef {
	if m == nil || parent.isZero() {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	}
	return m.arena.addCompact(parent, tokenRowEntry{bindingSlot: -1}, conditionMatch{bindingSlot: -1}, 0, parent.generation())
}

func (m *reteGraphBetaMemory) newTokenRowRef(parent tokenRef, source tokenRef, row *tokenRow, match conditionMatch, recency Recency, generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil || row == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	}
	return m.arena.addCompact(parent, row.tokenRowEntry(), match, recency, generation)
}

func (m *reteGraphBetaMemory) newTokenRowRefSource(parent tokenRef, source tokenRef, row *tokenRow, recency Recency, generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil || row == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	}
	return m.arena.addCompactSource(parent, source, row.tokenRowEntry(), recency, generation)
}

func (m *reteGraphBetaMemory) newRootTokenRef(generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
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
	m.initializeTerminalMemory(id, terminal)
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
	if terminal := m.terminalAt(id); terminal != nil {
		return terminal.ruleRevisionID
	}
	node := m.terminalNode(id)
	if node == nil {
		return ""
	}
	return node.ruleRevisionID
}

func (t *reteGraphTerminalMemory) terminalTokenIdentity(token tokenRef) candidateIdentity {
	if t == nil || !t.ruleOK || token.isZero() {
		return candidateIdentity{}
	}
	if t.ruleConditionCount > 0 {
		if identity, ok := t.terminalTokenIdentitySmall(token); ok {
			return identity
		}
	}
	return candidateIdentityForTerminalToken(t.rule, token)
}

func (t *reteGraphTerminalMemory) rowCandidateIdentity(row graphTokenRow) candidateIdentity {
	if !row.candidate.isZero() {
		if row.candidate.key.scopeHash == 0 {
			row.candidate.key.scopeHash = t.ruleIdentityScopeHash
		}
		return row.candidate
	}
	return t.terminalTokenIdentity(row.token)
}

func (t *reteGraphTerminalMemory) terminalTokenIdentitySmall(token tokenRef) (candidateIdentity, bool) {
	if t == nil || token.isZero() || t.ruleConditionCount <= 0 || t.ruleConditionCount > 8 {
		return candidateIdentity{}, false
	}
	var factIDs [8]FactID
	var factVersions [8]FactVersion
	var valueEntries [8]tokenRowEntry
	var seen uint8
	var values uint8
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return candidateIdentity{}, false
		}
		slot := row.bindingSlot
		if slot < 0 {
			continue
		}
		if slot >= t.ruleConditionCount {
			return candidateIdentity{}, false
		}
		mask := uint8(1 << uint(slot))
		if seen&mask != 0 {
			return candidateIdentity{}, false
		}
		if row.hasValue {
			valueEntries[slot] = row.tokenRowEntry()
			values |= mask
		} else {
			if row.fact == nil {
				return candidateIdentity{}, false
			}
			factIDs[slot] = row.fact.ID()
			factVersions[slot] = row.fact.Version()
		}
		seen |= mask
	}
	if seen != uint8(1<<uint(t.ruleConditionCount))-1 {
		return candidateIdentity{}, false
	}
	generation := token.handle.arena.generation
	state := candidateIdentityHashStart(generation)
	count := 0
	for i := 0; i < t.ruleConditionCount; i++ {
		mask := uint8(1 << uint(i))
		if values&mask != 0 {
			state = candidateIdentityHashTokenEntryStep(state, valueEntries[i])
		} else {
			state = candidateIdentityHashFactStep(state, factIDs[i], factVersions[i])
		}
		count++
	}
	return candidateIdentity{
		generation: generation,
		count:      count,
		key: candidateIdentityKey{
			scopeHash: t.ruleIdentityScopeHash,
			hash:      candidateIdentityHashFinish(state, count),
		},
	}, true
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
	if row, ok := token.resolve(); ok {
		return graphTokenIdentityKey{
			size:          row.size,
			generation:    token.handle.arena.generation,
			identityState: row.identityState,
		}
	}
	return graphTokenIdentityKey{
		identityState: candidateIdentityHashStart(0),
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
	return row.conditionMatch()
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
		match, ok := row.conditionMatch()
		if !ok {
			return conditionMatch{}, false
		}
		if !match.hasValue {
			return match, true
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
	match, ok := row.conditionMatch()
	if !ok {
		return tokenRef{}
	}
	if !match.hasValue {
		recency = match.fact.Recency()
	}
	return m.newTokenRowRefSource(parent, token, row, recency, token.generation(), span)
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
	match, ok := row.conditionMatch()
	if !ok {
		return tokenRef{}
	}
	if !match.hasValue {
		recency = match.fact.Recency()
	}
	return arena.addCompactSource(parent, token, row.tokenRowEntry(), recency, token.generation())
}

func conditionMatchForEntry(match conditionMatch, entry bindingTupleEntry) conditionMatch {
	match.conditionID = entry.conditionID
	match.bindingSlot = entry.bindingSlot
	return match
}

func tokenConditionMatches(token tokenRef) ([]conditionMatch, bool) {
	size := tokenPublicSize(token)
	if size <= 0 {
		return nil, false
	}
	matches := make([]conditionMatch, size)
	seen := make([]bool, size)
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return nil, false
		}
		match, ok := row.conditionMatch()
		if !ok {
			return nil, false
		}
		slot := match.bindingSlot
		if slot < 0 {
			continue
		}
		if slot >= len(matches) || seen[slot] {
			return nil, false
		}
		matches[slot] = match
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
	joins := node.hashJoins
	if len(joins) == 0 {
		return betaJoinKey{}, true, nil
	}
	if len(joins) == 1 {
		join := joins[0]
		if join.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		if !join.hasRightKeyExpression {
			match, ok := tokenRefAtSlot(token, join.refBindingSlot)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			value, ok := join.rightValueFromFact(match.fact)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			key, ok := betaJoinKeyForSingleValue(value)
			return key, ok, nil
		}
	} else if len(joins) == 2 {
		firstJoin := joins[0]
		secondJoin := joins[1]
		if firstJoin.indexKind != joinIndexEquality || secondJoin.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		if !firstJoin.hasRightKeyExpression && !secondJoin.hasRightKeyExpression {
			firstMatch, ok := tokenRefAtSlot(token, firstJoin.refBindingSlot)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			firstValue, ok := firstJoin.rightValueFromFact(firstMatch.fact)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			secondMatch, ok := tokenRefAtSlot(token, secondJoin.refBindingSlot)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			secondValue, ok := secondJoin.rightValueFromFact(secondMatch.fact)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			if key, ok := betaJoinKeyForTwoValues(firstValue, secondValue); ok {
				return key, true, nil
			}
		}
	}
	return betaJoinKeyForPlanWithError(compiledConditionPlan{joins: joins}, func(join compiledJoinConstraint) (Value, bool, error) {
		return graphBetaLeftJoinValue(ctx, join, token, span)
	})
}

func graphBetaJoinKeyForLeftMatchWithContext(ctx context.Context, node *reteGraphBetaNode, bindingSlot int, fact conditionFactRef, token tokenRef, span *propagationCounterSpan) (betaJoinKey, bool, error) {
	if node == nil || node.rightHasLeftPrefix {
		return betaJoinKey{}, false, nil
	}
	joins := node.hashJoins
	if len(joins) == 0 {
		return betaJoinKey{}, true, nil
	}
	if len(joins) == 1 {
		join := joins[0]
		if join.indexKind != joinIndexEquality || join.hasRightKeyExpression || join.refBindingSlot != bindingSlot {
			return betaJoinKey{}, false, nil
		}
		value, ok := join.rightValueFromFact(fact)
		if !ok {
			return betaJoinKey{}, false, nil
		}
		key, ok := betaJoinKeyForSingleValue(value)
		return key, ok, nil
	}
	if len(joins) == 2 {
		firstJoin := joins[0]
		secondJoin := joins[1]
		if firstJoin.indexKind != joinIndexEquality || secondJoin.indexKind != joinIndexEquality ||
			firstJoin.hasRightKeyExpression || secondJoin.hasRightKeyExpression ||
			firstJoin.refBindingSlot != bindingSlot || secondJoin.refBindingSlot != bindingSlot {
			return betaJoinKey{}, false, nil
		}
		firstValue, ok := firstJoin.rightValueFromFact(fact)
		if !ok {
			return betaJoinKey{}, false, nil
		}
		secondValue, ok := secondJoin.rightValueFromFact(fact)
		if !ok {
			return betaJoinKey{}, false, nil
		}
		if key, ok := betaJoinKeyForTwoValues(firstValue, secondValue); ok {
			return key, true, nil
		}
	}
	return betaJoinKeyForPlanWithError(compiledConditionPlan{joins: joins}, func(join compiledJoinConstraint) (Value, bool, error) {
		if join.indexKind != joinIndexEquality || join.hasRightKeyExpression || join.refBindingSlot != bindingSlot {
			return Value{}, false, nil
		}
		value, ok := join.rightValueFromFact(fact)
		return value, ok, nil
	})
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
	return graphBetaJoinKeyForRightMatchWithContext(ctx, node, match.fact, token, span)
}

func graphBetaJoinKeyForRightMatchWithContext(ctx context.Context, node *reteGraphBetaNode, fact conditionFactRef, token tokenRef, span *propagationCounterSpan) (betaJoinKey, bool, error) {
	if node == nil {
		return betaJoinKey{}, false, nil
	}
	joins := node.hashJoins
	if len(joins) == 0 {
		return betaJoinKey{}, true, nil
	}
	if len(joins) == 1 {
		join := joins[0]
		if join.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		if !join.hasLeftKeyExpression {
			value, ok := join.leftValueFromFact(fact)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			key, ok := betaJoinKeyForSingleValue(value)
			return key, ok, nil
		}
	} else if len(joins) == 2 {
		firstJoin := joins[0]
		secondJoin := joins[1]
		if firstJoin.indexKind != joinIndexEquality || secondJoin.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		if !firstJoin.hasLeftKeyExpression && !secondJoin.hasLeftKeyExpression {
			firstValue, ok := firstJoin.leftValueFromFact(fact)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			secondValue, ok := secondJoin.leftValueFromFact(fact)
			if !ok {
				return betaJoinKey{}, false, nil
			}
			if key, ok := betaJoinKeyForTwoValues(firstValue, secondValue); ok {
				return key, true, nil
			}
		}
	}
	return betaJoinKeyForPlanWithError(compiledConditionPlan{joins: joins}, func(join compiledJoinConstraint) (Value, bool, error) {
		return graphBetaRightJoinValue(ctx, join, fact, token, span)
	})
}

func graphBetaLeftJoinValue(ctx context.Context, join compiledJoinConstraint, token tokenRef, span *propagationCounterSpan) (Value, bool, error) {
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
}

func graphBetaRightJoinValue(ctx context.Context, join compiledJoinConstraint, fact conditionFactRef, token tokenRef, span *propagationCounterSpan) (Value, bool, error) {
	if join.hasLeftKeyExpression {
		value, ok, err := join.leftKeyExpression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, fact, token, nil, 0, joinFunctionEvaluationMeta(join), span)
		return value, ok, err
	}
	value, ok := join.leftValueFromFact(fact)
	return value, ok, nil
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
	if node.kind == reteGraphBetaNodeResidualFilter {
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			return false, nil
		}
		return m.residualJoinsMatch(node, currentMatch.fact, token.parent(), span)
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
		match, ok := row.conditionMatch()
		if !ok {
			return false, nil
		}
		slot := match.bindingSlot
		if slot >= 0 && slot < len(bindings) {
			bindings[slot] = match
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
			total += node.left.len()
			total += node.right.len()
		}
	}
	for _, terminal := range m.terminals {
		if terminal != nil {
			total += terminal.rows.len()
			total += terminal.queryRows.len()
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
			total += terminal.rows.len()
			total += terminal.queryRows.len()
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
		if terminalNode.kind != reteGraphTerminalRule {
			continue
		}
		terminal := m.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		if terminal.singleBranch() {
			if key, ok := m.terminalBranchKey(terminalNode.id, terminal.singleBranchID); ok {
				retained[key] += terminal.rows.len()
			}
			continue
		}
		for rowIndex, row := range terminal.rows.rows {
			if row.token.isZero() {
				continue
			}
			terminal.rows.forEachTerminalBranchSupport(graphTokenRowID(rowIndex), func(support terminalBranchSupport) {
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
		stats.addNegativeBetaLeftMemory(node.negative.left)
		stats.BetaTokenMemories++
		stats.addNegativeBetaRightMemory(node.negative.right)
		stats.BetaTokenMemories++
	}
	for _, terminal := range m.terminals {
		if terminal == nil {
			continue
		}
		if terminal.kind == reteGraphTerminalQuery {
			stats.addQueryTerminalTokenMemory(terminal.queryRows)
		} else {
			stats.addTerminalTokenMemory(terminal.rows)
		}
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
			if node.kind == reteGraphBetaNodeNot {
				left = memory.negative.left.diagnostics()
				right = memory.negative.right.diagnostics()
			} else {
				left = memory.left.diagnostics()
				right = memory.right.diagnostics()
			}
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
			if node.kind == reteGraphTerminalQuery {
				diag.Rows = terminal.queryRows.len()
			} else {
				diag.Rows = terminal.rows.len()
				if terminal.singleBranch() {
					diag.BranchRows[terminal.singleBranchID] = terminal.rows.len()
				} else {
					for rowIndex, row := range terminal.rows.rows {
						if row.token.isZero() {
							continue
						}
						terminal.rows.forEachTerminalBranchSupport(graphTokenRowID(rowIndex), func(support terminalBranchSupport) {
							if support.count <= 0 {
								return
							}
							diag.BranchRows[support.branchID]++
						})
					}
				}
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

func (s *reteGraphBetaMemoryStats) addTokenMemory(memory betaSideMemory) {
	if s == nil {
		return
	}
	s.TokenMemories++
	rowCount := memory.len()
	rowCapacity := memory.rowCapacity()
	s.TokenRows += rowCount
	s.TokenRowCapacity += rowCapacity
	s.TokenRowReserve += memory.rowReserve
	s.TokenRowCapacityMax = max(s.TokenRowCapacityMax, rowCapacity)
	s.TokenRowReserveMax = max(s.TokenRowReserveMax, memory.rowReserve)

	joinKeys := memory.indexes.keyCount()
	s.JoinIndexKeys += joinKeys
	s.JoinIndexReserve += memory.joinIndexReserve
	s.JoinIndexKeysMax = max(s.JoinIndexKeysMax, joinKeys)
	s.JoinIndexReserveMax = max(s.JoinIndexReserveMax, memory.joinIndexReserve)

	factKeys := memory.factIndexKeyCount()
	s.FactIndexKeys += factKeys
	s.FactIndexKeysMax = max(s.FactIndexKeysMax, factKeys)
}

func (s *reteGraphBetaMemoryStats) addNegativeBetaLeftMemory(memory negativeBetaLeftMemory) {
	if s == nil {
		return
	}
	s.TokenMemories++
	rowCount := memory.len()
	rowCapacity := memory.rowCapacity()
	s.TokenRows += rowCount
	s.TokenRowCapacity += rowCapacity
	s.TokenRowReserve += memory.rowReserve
	s.TokenRowCapacityMax = max(s.TokenRowCapacityMax, rowCapacity)
	s.TokenRowReserveMax = max(s.TokenRowReserveMax, memory.rowReserve)

	joinKeys := memory.indexes.keyCount()
	s.JoinIndexKeys += joinKeys
	s.JoinIndexReserve += memory.joinIndexReserve
	s.JoinIndexKeysMax = max(s.JoinIndexKeysMax, joinKeys)
	s.JoinIndexReserveMax = max(s.JoinIndexReserveMax, memory.joinIndexReserve)
}

func (s *reteGraphBetaMemoryStats) addNegativeBetaRightMemory(memory negativeBetaRightMemory) {
	if s == nil {
		return
	}
	s.TokenMemories++
	rowCount := memory.len()
	rowCapacity := memory.rowCapacity()
	s.TokenRows += rowCount
	s.TokenRowCapacity += rowCapacity
	s.TokenRowReserve += memory.rowReserve
	s.TokenRowCapacityMax = max(s.TokenRowCapacityMax, rowCapacity)
	s.TokenRowReserveMax = max(s.TokenRowReserveMax, memory.rowReserve)

	joinKeys := memory.indexes.keyCount()
	s.JoinIndexKeys += joinKeys
	s.JoinIndexReserve += memory.joinIndexReserve
	s.JoinIndexKeysMax = max(s.JoinIndexKeysMax, joinKeys)
	s.JoinIndexReserveMax = max(s.JoinIndexReserveMax, memory.joinIndexReserve)
}

func (s *reteGraphBetaMemoryStats) addTerminalTokenMemory(memory terminalTokenMemory) {
	if s == nil {
		return
	}
	s.TokenMemories++
	rowCount := memory.len()
	rowCapacity := cap(memory.rows)
	s.TokenRows += rowCount
	s.TokenRowCapacity += rowCapacity
	s.TokenRowReserve += memory.rowReserve
	s.TokenRowCapacityMax = max(s.TokenRowCapacityMax, rowCapacity)
	s.TokenRowReserveMax = max(s.TokenRowReserveMax, memory.rowReserve)

	identityKeys := memory.identityRows.keyCount()
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

func (s *reteGraphBetaMemoryStats) addQueryTerminalTokenMemory(memory queryTerminalMemory) {
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

	identityKeys := memory.identityRows.keyCount()
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

func (m betaSideMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	diag := reteGraphTokenMemoryDiagnostics{
		Rows:          m.len(),
		JoinIndexKeys: m.indexes.keyCount(),
		FactIndexKeys: m.factIndexKeyCount(),
	}
	seen := make(map[betaJoinKey]struct{}, m.indexes.keyCount())
	m.forEachRow(func(row *betaTokenRow) bool {
		if row.token.isZero() {
			return true
		}
		if _, ok := seen[row.joinKey]; ok {
			return true
		}
		seen[row.joinKey] = struct{}{}
		depth := m.joinRowCount(row.joinKey)
		diag.JoinBucketDepthTotal += depth
		diag.JoinBucketDepthMax = max(diag.JoinBucketDepthMax, depth)
		return true
	})
	return diag
}

func (m betaSideMemory) factIndexKeyCount() int {
	return 0
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
		if terminal := m.terminalForRule(rule.revisionID); terminal != nil {
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
	return m.sourceGenerationValue
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
		candidate, err := buildMatchCandidateFromTokenRef(rule, terminal.rows.rowToken(row))
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
