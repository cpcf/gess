package gess

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type tokenHandle struct {
	row        *tokenRow
	generation uint64
}

func (h tokenHandle) isZero() bool {
	return h.row == nil || h.generation == 0
}

type tokenRef struct {
	handle tokenHandle
}

func (r tokenRef) isZero() bool {
	return r.handle.isZero()
}

type tokenRow struct {
	slotGeneration   uint64
	parent           tokenHandle
	match            conditionMatch
	size             int
	pathLen          int
	maxRecency       Recency
	aggregateRecency Recency
	identityState    uint64
	generation       Generation
}

type tokenArena struct {
	chunks         [][]tokenRow
	count          int
	nextGeneration uint64
}

func newTokenArena() *tokenArena {
	return &tokenArena{nextGeneration: 1}
}

func (a *tokenArena) reset() {
	if a == nil {
		return
	}
	for chunkIndex, chunk := range a.chunks {
		for i := range chunk {
			chunk[i] = tokenRow{}
		}
		a.chunks[chunkIndex] = chunk[:0]
	}
	a.count = 0
	if a.nextGeneration == 0 {
		a.nextGeneration = 1
	}
}

func (a *tokenArena) add(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	var parentRow *tokenRow
	var ok bool
	if !parent.isZero() {
		parentRow, ok = parent.resolve()
		if !ok {
			return tokenRef{}
		}
	}
	row := tokenRow{
		slotGeneration: a.nextGeneration,
		match:          match,
		generation:     generation,
	}
	a.nextGeneration++

	if parentRow != nil {
		row.parent = parent.handle
		row.size = parentRow.size + 1
		row.pathLen = parentRow.pathLen + len(entry.conditionPath)
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.aggregateRecency = addRecency(parentRow.aggregateRecency, recency)
		row.identityState = parentRow.identityState
		if row.generation == 0 {
			row.generation = parentRow.generation
		}
	} else {
		row.size = 1
		row.pathLen = len(entry.conditionPath)
		row.maxRecency = recency
		row.aggregateRecency = recency
		row.identityState = candidateIdentityHashStart(generation)
		if row.generation == 0 {
			row.generation = generation
		}
	}
	row.identityState = candidateIdentityHashStep(row.identityState, entry)

	chunkIndex := a.count / reteBetaMatchTokenChunkSize
	for len(a.chunks) <= chunkIndex {
		a.chunks = append(a.chunks, make([]tokenRow, 0, reteBetaMatchTokenChunkSize))
	}
	a.chunks[chunkIndex] = append(a.chunks[chunkIndex], row)
	rowPtr := &a.chunks[chunkIndex][len(a.chunks[chunkIndex])-1]
	a.count++
	handle := tokenHandle{row: rowPtr, generation: row.slotGeneration}
	return tokenRef{handle: handle}
}

func (a *tokenArena) resolve(handle tokenHandle) (*tokenRow, bool) {
	if a == nil || handle.isZero() {
		return nil, false
	}
	if handle.row.slotGeneration != handle.generation {
		return nil, false
	}
	return handle.row, true
}

func (a *tokenArena) rowCount() int {
	if a == nil {
		return 0
	}
	return a.count
}

func (r tokenRef) resolve() (*tokenRow, bool) {
	if r.handle.isZero() {
		return nil, false
	}
	if r.handle.row.slotGeneration != r.handle.generation {
		return nil, false
	}
	return r.handle.row, true
}

func (r tokenRef) parent() tokenRef {
	row, ok := r.resolve()
	if !ok || row.parent.isZero() {
		return tokenRef{}
	}
	return tokenRef{handle: row.parent}
}

func (r tokenRef) size() int {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.size
}

func (r tokenRef) pathLen() int {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.pathLen
}

func (r tokenRef) maxRecency() Recency {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.maxRecency
}

func (r tokenRef) aggregateRecency() Recency {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.aggregateRecency
}

func (r tokenRef) generation() Generation {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.generation
}

func (r tokenRef) identityState() uint64 {
	row, ok := r.resolve()
	if !ok {
		return candidateIdentityHashStart(0)
	}
	return row.identityState
}

func (r tokenRef) matchAt(index int) (conditionMatch, bool) {
	row, ok := r.resolve()
	if !ok || index < 0 || index >= row.size {
		return conditionMatch{}, false
	}
	if index == row.size-1 {
		return row.match, true
	}
	return r.parent().matchAt(index)
}

func (r tokenRef) containsFact(id FactID) bool {
	for current := r; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return false
		}
		if row.match.fact.ID() == id {
			return true
		}
	}
	return false
}

type reteBetaMemory struct {
	revision            *Ruleset
	rules               map[RuleRevisionID]*reteBetaRuleMemory
	terminalTokenDeltas []reteTerminalTokenDelta
}

type reteAgendaDelta struct {
	supported bool
	added     []reteTerminalTokenDelta
	removed   []reteTerminalTokenDelta
}

type reteTerminalTokenDelta struct {
	ruleRevisionID RuleRevisionID
	token          tokenRef
	identityKey    candidateIdentityKey
}

type reteBetaRuleMemory struct {
	rule             compiledRule
	conditionMatches [][]betaConditionMatchRow
	conditionIndexes []map[betaJoinKey]betaConditionMatchIndexBucket
	prefixes         [][]betaPrefixRow
	prefixIndexes    []map[betaJoinKey]betaPrefixIndexBucket
	tokenArena       *tokenArena
	lookupScratch    [][]conditionMatch
	prefixScratch    [][]conditionMatch
	candidateScratch candidateScratch
}

type betaConditionMatchRowID int

type betaConditionMatchIndexBucket struct {
	first betaConditionMatchRowID
	rest  []betaConditionMatchRowID
	count int
}

func (b betaConditionMatchIndexBucket) len() int {
	return b.count
}

func (b betaConditionMatchIndexBucket) at(index int) (betaConditionMatchRowID, bool) {
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

func (b *betaConditionMatchIndexBucket) append(id betaConditionMatchRowID) {
	if b.count == 0 {
		b.first = id
		b.count = 1
		return
	}
	b.rest = append(b.rest, id)
	b.count++
}

func (b *betaConditionMatchIndexBucket) remove(id betaConditionMatchRowID) bool {
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

func (b *betaConditionMatchIndexBucket) replace(oldID, newID betaConditionMatchRowID) bool {
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

func (b betaConditionMatchIndexBucket) forEach(fn func(betaConditionMatchRowID) bool) {
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

func (b betaConditionMatchIndexBucket) reset() betaConditionMatchIndexBucket {
	for i := range b.rest {
		b.rest[i] = 0
	}
	b.first = 0
	b.rest = b.rest[:0]
	b.count = 0
	return b
}

type betaConditionMatchRow struct {
	id    betaConditionMatchRowID
	match conditionMatch
}

type betaPrefix struct {
	token tokenRef
}

type betaPrefixRowID int

type betaPrefixIndexBucket struct {
	first betaPrefixRowID
	rest  []betaPrefixRowID
	count int
}

func (b betaPrefixIndexBucket) len() int {
	return b.count
}

func (b betaPrefixIndexBucket) at(index int) (betaPrefixRowID, bool) {
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

func (b *betaPrefixIndexBucket) append(id betaPrefixRowID) {
	if b.count == 0 {
		b.first = id
		b.count = 1
		return
	}
	b.rest = append(b.rest, id)
	b.count++
}

func (b *betaPrefixIndexBucket) remove(id betaPrefixRowID) bool {
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

func (b *betaPrefixIndexBucket) replace(oldID, newID betaPrefixRowID) bool {
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

func (b betaPrefixIndexBucket) forEach(fn func(betaPrefixRowID) bool) {
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

func (b betaPrefixIndexBucket) reset() betaPrefixIndexBucket {
	for i := range b.rest {
		b.rest[i] = 0
	}
	b.first = 0
	b.rest = b.rest[:0]
	b.count = 0
	return b
}

type betaPrefixRow struct {
	id     betaPrefixRowID
	prefix betaPrefix
}

type betaJoinKeyKind uint8

const (
	betaJoinKeyUnknown betaJoinKeyKind = iota
	betaJoinKeyNull
	betaJoinKeyBool
	betaJoinKeyInt
	betaJoinKeyFloat
	betaJoinKeyString
	betaJoinKeyFallback
)

type betaJoinKey struct {
	kind        betaJoinKeyKind
	boolValue   bool
	intValue    int64
	floatBits   uint64
	stringValue string
}

const reteBetaMatchTokenChunkSize = 64
const reteBetaMatchTokenChunkReserve = 2

func newReteBetaMemory(revision *Ruleset, plan reteNetworkPlan, facts []FactSnapshot) *reteBetaMemory {
	if revision == nil || !plan.betaSupported {
		return nil
	}

	memory := &reteBetaMemory{
		revision: revision,
		rules:    make(map[RuleRevisionID]*reteBetaRuleMemory, len(plan.rules)),
	}
	for _, rulePlan := range plan.rules {
		if !rulePlan.supported || !rulePlan.betaSupported {
			continue
		}
		rule, ok := revision.rulesByRevisionID[rulePlan.ruleRevisionID]
		if !ok {
			return nil
		}
		ruleMemory := newReteBetaRuleMemory(rule)
		ruleMemory.resetFacts(facts)
		memory.rules[rule.revisionID] = ruleMemory
	}

	return memory
}

func (m *reteBetaMemory) match(ctx context.Context, source factSource, alphaSource alphaFactSource) ([]ruleMatchResult, error) {
	if m == nil || m.revision == nil || source == nil {
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
			return nil, fmt.Errorf("%w: missing compiled rule %q", ErrMatcher, ruleName)
		}

		candidates, err := m.matchRuleCandidates(ctx, source, rule, alphaSource)
		if err != nil {
			return nil, err
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

func (m *reteBetaMemory) matchWithoutSnapshot(ctx context.Context, generation Generation) ([]ruleMatchResult, bool, error) {
	if m == nil || m.revision == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	results := make([]ruleMatchResult, 0, len(m.revision.ruleOrder))
	for _, ruleName := range m.revision.ruleOrder {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}

		rule, ok := m.revision.rules[ruleName]
		if !ok {
			return nil, false, nil
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return nil, false, nil
		}

		candidates, err := collectMatchCandidatesFromPrefixRows(ctx, rule, generation, ruleMemory.terminalPrefixRows(), &ruleMemory.candidateScratch)
		if err != nil {
			return nil, false, err
		}
		results = append(results, ruleMatchResult{
			ruleID:           rule.id,
			ruleRevisionID:   rule.revisionID,
			salience:         rule.salience,
			declarationOrder: rule.declarationOrder,
			candidates:       candidates,
		})
	}

	return results, true, nil
}

func (m *reteBetaMemory) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	if m == nil || m.revision == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	deltas := m.terminalTokenDeltas[:0]
	for _, ruleName := range m.revision.ruleOrder {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}

		rule, ok := m.revision.rules[ruleName]
		if !ok {
			return nil, false, nil
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return nil, false, nil
		}
		for _, row := range ruleMemory.terminalPrefixRows() {
			if row.prefix.token.isZero() {
				continue
			}
			deltas = append(deltas, reteTerminalTokenDelta{
				ruleRevisionID: rule.revisionID,
				token:          row.prefix.token,
				identityKey:    candidateIdentityForTerminalToken(rule, row.prefix.token).key,
			})
		}
	}

	m.terminalTokenDeltas = deltas
	return deltas, true, nil
}

func (m *reteBetaMemory) resetFacts(plan reteNetworkPlan, facts []FactSnapshot) {
	if m == nil || m.revision == nil {
		return
	}
	m.clearTerminalTokenDeltas()
	if m.rules == nil {
		m.rules = make(map[RuleRevisionID]*reteBetaRuleMemory, len(plan.rules))
	}
	for _, rulePlan := range plan.rules {
		if !rulePlan.supported || !rulePlan.betaSupported {
			delete(m.rules, rulePlan.ruleRevisionID)
			continue
		}
		rule, ok := m.revision.rulesByRevisionID[rulePlan.ruleRevisionID]
		if !ok {
			delete(m.rules, rulePlan.ruleRevisionID)
			continue
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			ruleMemory = newReteBetaRuleMemory(rule)
			m.rules[rule.revisionID] = ruleMemory
		}
		ruleMemory.resetFacts(facts)
	}
}

func (m *reteBetaMemory) clearTerminalTokenDeltas() {
	if m == nil {
		return
	}
	clear(m.terminalTokenDeltas)
	m.terminalTokenDeltas = m.terminalTokenDeltas[:0]
}

func (m *reteBetaMemory) beginTerminalTokenDelta() reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	return reteAgendaDelta{
		supported: true,
		added:     m.terminalTokenDeltas[:0],
	}
}

func (m *reteBetaMemory) finishTerminalTokenDelta(delta reteAgendaDelta) reteAgendaDelta {
	if m == nil {
		return delta
	}
	m.terminalTokenDeltas = delta.added
	return delta
}

func (m *reteBetaMemory) matchRuleCandidates(ctx context.Context, source factSource, rule compiledRule, alphaSource alphaFactSource) ([]matchCandidate, error) {
	if source == nil {
		return nil, ErrInvalidRuleset
	}
	ruleMemory := m.rules[rule.revisionID]
	if ruleMemory == nil {
		return rule.matchCandidatesWithAlpha(ctx, source, alphaSource)
	}
	return collectMatchCandidatesFromPrefixRows(ctx, rule, source.sourceGeneration(), ruleMemory.terminalPrefixRows(), &ruleMemory.candidateScratch)
}

func (m *reteBetaMemory) insertFact(fact FactSnapshot, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}
	}
	delta := m.beginTerminalTokenDelta()
	for _, ruleName := range m.revision.ruleOrder {
		if span != nil {
			span.recordRuleMemoryVisited()
		}
		rule, ok := m.revision.rules[ruleName]
		if !ok {
			delta.supported = false
			continue
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			delta.supported = false
			continue
		}
		delta.added = ruleMemory.appendInsertedFactDeltas(delta.added, rule.revisionID, fact, span)
	}
	return m.finishTerminalTokenDelta(delta)
}

func (m *reteBetaMemory) insertFactGenerated(fact *workingFact, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.revision == nil || fact == nil {
		return reteAgendaDelta{}
	}
	delta := m.beginTerminalTokenDelta()
	for _, ruleName := range m.revision.ruleOrder {
		if span != nil {
			span.recordRuleMemoryVisited()
		}
		rule, ok := m.revision.rules[ruleName]
		if !ok {
			delta.supported = false
			continue
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			delta.supported = false
			continue
		}
		delta.added = ruleMemory.appendInsertedFactDeltasGenerated(delta.added, rule.revisionID, fact, span)
	}
	return m.finishTerminalTokenDelta(delta)
}

func (m *reteBetaMemory) insertFactForRules(fact FactSnapshot, ruleRevisionIDs []RuleRevisionID, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}, false
	}
	delta := m.beginTerminalTokenDelta()
	for _, ruleRevisionID := range ruleRevisionIDs {
		rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}
	for _, ruleRevisionID := range ruleRevisionIDs {
		if span != nil {
			span.recordRuleMemoryVisited()
		}
		rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		delta.added = ruleMemory.appendInsertedFactDeltas(delta.added, rule.revisionID, fact, span)
	}
	return m.finishTerminalTokenDelta(delta), true
}

func (m *reteBetaMemory) insertFactForRulesGenerated(fact *workingFact, ruleRevisionIDs []RuleRevisionID, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil || fact == nil {
		return reteAgendaDelta{}, false
	}
	delta := m.beginTerminalTokenDelta()
	for _, ruleRevisionID := range ruleRevisionIDs {
		rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}
	for _, ruleRevisionID := range ruleRevisionIDs {
		if span != nil {
			span.recordRuleMemoryVisited()
		}
		rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		delta.added = ruleMemory.appendInsertedFactDeltasGenerated(delta.added, rule.revisionID, fact, span)
	}
	return m.finishTerminalTokenDelta(delta), true
}

func (m *reteBetaMemory) insertFactForConditionRoutes(fact FactSnapshot, routes []reteBetaConditionRoute, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}, false
	}
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if route.conditionIndex < 0 || route.conditionIndex >= len(rule.conditionPlans) {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}

	delta := m.beginTerminalTokenDelta()
	var lastVisited RuleRevisionID
	visited := false
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		if span != nil && (!visited || lastVisited != rule.revisionID) {
			span.recordRuleMemoryVisited()
			lastVisited = rule.revisionID
			visited = true
		}
		delta.added = ruleMemory.appendInsertedFactDeltaForCondition(delta.added, rule.revisionID, route.conditionIndex, fact, span)
	}
	return m.finishTerminalTokenDelta(delta), true
}

func (m *reteBetaMemory) insertFactForConditionRoutesGenerated(fact *workingFact, routes []reteBetaConditionRoute, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil || fact == nil {
		return reteAgendaDelta{}, false
	}
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if route.conditionIndex < 0 || route.conditionIndex >= len(rule.conditionPlans) {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}

	delta := m.beginTerminalTokenDelta()
	var lastVisited RuleRevisionID
	visited := false
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		if span != nil && (!visited || lastVisited != rule.revisionID) {
			span.recordRuleMemoryVisited()
			lastVisited = rule.revisionID
			visited = true
		}
		delta.added = ruleMemory.appendInsertedFactDeltaForConditionGenerated(delta.added, rule.revisionID, route.conditionIndex, fact, span)
	}
	return m.finishTerminalTokenDelta(delta), true
}

func (m *reteBetaMemory) insertFactForConditionConsumers(fact FactSnapshot, routes []reteBetaConditionRoute, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}, false
	}
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if route.conditionIndex < 0 || route.conditionIndex >= len(rule.conditionPlans) {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}

	delta := m.beginTerminalTokenDelta()
	var lastVisited RuleRevisionID
	visited := false
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		if span != nil && (!visited || lastVisited != rule.revisionID) {
			span.recordRuleMemoryVisited()
			lastVisited = rule.revisionID
			visited = true
		}
		delta.added = ruleMemory.appendInsertedFactDeltaForConditionMatch(delta.added, rule.revisionID, route, fact, span)
	}
	return m.finishTerminalTokenDelta(delta), true
}

func (m *reteBetaMemory) insertFactForConditionConsumersGenerated(fact *workingFact, routes []reteBetaConditionRoute, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil || fact == nil {
		return reteAgendaDelta{}, false
	}
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if route.conditionIndex < 0 || route.conditionIndex >= len(rule.conditionPlans) {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}

	delta := m.beginTerminalTokenDelta()
	var lastVisited RuleRevisionID
	visited := false
	for _, route := range routes {
		rule, ok := m.revision.rulesByRevisionID[route.ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		if span != nil && (!visited || lastVisited != rule.revisionID) {
			span.recordRuleMemoryVisited()
			lastVisited = rule.revisionID
			visited = true
		}
		delta.added = ruleMemory.appendInsertedFactDeltaForConditionMatchGenerated(delta.added, rule.revisionID, route, fact, span)
	}
	return m.finishTerminalTokenDelta(delta), true
}

func (m *reteBetaMemory) removeFact(id FactID) reteAgendaDelta {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
	for _, ruleName := range m.revision.ruleOrder {
		rule, ok := m.revision.rules[ruleName]
		if !ok {
			delta.supported = false
			continue
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			delta.supported = false
			continue
		}
		delta.removed = ruleMemory.appendRemovedFactDeltas(delta.removed, rule.revisionID, id)
	}
	return delta
}

func (m *reteBetaMemory) removeFactForRules(id FactID, ruleRevisionIDs []RuleRevisionID) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}, false
	}
	delta := reteAgendaDelta{supported: true}
	for _, ruleRevisionID := range ruleRevisionIDs {
		rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		if m.rules == nil || m.rules[rule.revisionID] == nil {
			return reteAgendaDelta{}, false
		}
	}
	for _, ruleRevisionID := range ruleRevisionIDs {
		rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
		if !ok {
			return reteAgendaDelta{}, false
		}
		ruleMemory := m.rules[rule.revisionID]
		if ruleMemory == nil {
			return reteAgendaDelta{}, false
		}
		delta.removed = ruleMemory.appendRemovedFactDeltas(delta.removed, rule.revisionID, id)
	}
	return delta, true
}

func (m *reteBetaMemory) updateFact(before, after FactSnapshot) reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	removed := m.removeFact(before.ID())
	added := m.insertFact(after, nil)
	return reteAgendaDelta{
		supported: removed.supported && added.supported,
		added:     added.added,
		removed:   removed.removed,
	}
}

func (m *reteBetaMemory) updateFactForRules(before, after FactSnapshot, ruleRevisionIDs []RuleRevisionID) (reteAgendaDelta, bool) {
	if m == nil {
		return reteAgendaDelta{}, false
	}
	removed, ok := m.removeFactForRules(before.ID(), ruleRevisionIDs)
	if !ok {
		return reteAgendaDelta{}, false
	}
	added, ok := m.insertFactForRules(after, ruleRevisionIDs, nil)
	if !ok {
		return reteAgendaDelta{}, false
	}
	return reteAgendaDelta{
		supported: removed.supported && added.supported,
		added:     added.added,
		removed:   removed.removed,
	}, true
}

func newReteBetaRuleMemory(rule compiledRule) *reteBetaRuleMemory {
	conditions := len(rule.conditionPlans)
	return &reteBetaRuleMemory{
		rule:             rule,
		conditionMatches: make([][]betaConditionMatchRow, conditions),
		conditionIndexes: make([]map[betaJoinKey]betaConditionMatchIndexBucket, conditions),
		prefixes:         make([][]betaPrefixRow, conditions),
		prefixIndexes:    make([]map[betaJoinKey]betaPrefixIndexBucket, conditions),
		tokenArena:       newTokenArena(),
		lookupScratch:    make([][]conditionMatch, conditions),
		prefixScratch:    make([][]conditionMatch, conditions),
	}
}

func (m *reteBetaRuleMemory) resetFacts(facts []FactSnapshot) {
	if m == nil {
		return
	}
	m.clear()
	if m.tokenArena == nil {
		m.tokenArena = newTokenArena()
	} else {
		m.tokenArena.reset()
	}
	for conditionIndex, plan := range m.rule.conditionPlans {
		for _, fact := range facts {
			match, ok, err := betaConditionMatch(plan, fact)
			if err != nil || !ok {
				continue
			}
			if !m.addConditionMatch(conditionIndex, match) {
				continue
			}
		}
	}
	for conditionIndex, matches := range m.conditionMatches {
		if len(matches) == 0 {
			return
		}
		if conditionIndex == 0 {
			plan := m.rule.conditionPlans[conditionIndex]
			for _, row := range matches {
				match := row.match
				m.addPrefix(conditionIndex, betaPrefix{
					token: m.newTokenRef(tokenRef{}, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
				})
			}
		} else {
			if !m.joinExistingPrefixes(conditionIndex) {
				return
			}
		}
	}
}

func (m *reteBetaRuleMemory) clear() {
	if m == nil {
		return
	}
	for conditionIndex := range m.conditionMatches {
		for i := range m.conditionMatches[conditionIndex] {
			m.conditionMatches[conditionIndex][i] = betaConditionMatchRow{}
		}
		m.conditionMatches[conditionIndex] = m.conditionMatches[conditionIndex][:0]
		resetConditionMatchIndexBuckets(m.conditionIndexes[conditionIndex])

		for i := range m.prefixes[conditionIndex] {
			m.prefixes[conditionIndex][i] = betaPrefixRow{}
		}
		m.prefixes[conditionIndex] = m.prefixes[conditionIndex][:0]
		resetPrefixIndexBuckets(m.prefixIndexes[conditionIndex])
		for i := range m.prefixScratch[conditionIndex] {
			m.prefixScratch[conditionIndex][i] = conditionMatch{}
		}
		m.lookupScratch[conditionIndex] = m.lookupScratch[conditionIndex][:0]
		m.prefixScratch[conditionIndex] = m.prefixScratch[conditionIndex][:0]
	}
	m.candidateScratch.reset(0, 0, 0)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, fact FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil {
		return out
	}
	for conditionIndex, plan := range m.rule.conditionPlans {
		out = m.appendInsertedFactDeltaForConditionPlan(out, ruleRevisionID, conditionIndex, plan, fact, span)
	}
	return out
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltasGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, fact *workingFact, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil {
		return out
	}
	for conditionIndex, plan := range m.rule.conditionPlans {
		out = m.appendInsertedFactDeltaForConditionPlanGenerated(out, ruleRevisionID, conditionIndex, plan, fact, span)
	}
	return out
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionMatch(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, route reteBetaConditionRoute, fact FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil || route.conditionIndex < 0 || route.conditionIndex >= len(m.rule.conditionPlans) {
		return out
	}
	match := conditionMatch{
		conditionID: route.conditionID,
		bindingSlot: route.bindingSlot,
		fact:        newConditionFactRefFromSnapshot(fact),
	}
	if !m.addConditionMatch(route.conditionIndex, match) {
		return out
	}
	if span != nil {
		span.recordConditionMatchAdded()
	}
	return m.appendRightMatchDeltas(out, ruleRevisionID, route.conditionIndex, match, span)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionMatchGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, route reteBetaConditionRoute, fact *workingFact, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil || route.conditionIndex < 0 || route.conditionIndex >= len(m.rule.conditionPlans) {
		return out
	}
	match := conditionMatch{
		conditionID: route.conditionID,
		bindingSlot: route.bindingSlot,
		fact:        newConditionFactRefFromWorkingFact(fact),
	}
	if !m.addConditionMatch(route.conditionIndex, match) {
		return out
	}
	if span != nil {
		span.recordConditionMatchAdded()
	}
	return m.appendRightMatchDeltas(out, ruleRevisionID, route.conditionIndex, match, span)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForCondition(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, fact FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.rule.conditionPlans) {
		return out
	}
	return m.appendInsertedFactDeltaForConditionPlan(out, ruleRevisionID, conditionIndex, m.rule.conditionPlans[conditionIndex], fact, span)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, fact *workingFact, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.rule.conditionPlans) {
		return out
	}
	return m.appendInsertedFactDeltaForConditionPlanGenerated(out, ruleRevisionID, conditionIndex, m.rule.conditionPlans[conditionIndex], fact, span)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionPlan(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, plan compiledConditionPlan, fact FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if span != nil {
		span.recordConditionPlanTested()
	}
	match, ok, err := betaConditionMatch(plan, fact)
	if err != nil || !ok {
		return out
	}
	if !m.addConditionMatch(conditionIndex, match) {
		return out
	}
	if span != nil {
		span.recordConditionMatchAdded()
	}
	return m.appendRightMatchDeltas(out, ruleRevisionID, conditionIndex, match, span)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionPlanGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, plan compiledConditionPlan, fact *workingFact, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if span != nil {
		span.recordConditionPlanTested()
	}
	match, ok, err := betaConditionMatchWorking(plan, fact)
	if err != nil || !ok {
		return out
	}
	if !m.addConditionMatch(conditionIndex, match) {
		return out
	}
	if span != nil {
		span.recordConditionMatchAdded()
	}
	return m.appendRightMatchDeltas(out, ruleRevisionID, conditionIndex, match, span)
}

func (m *reteBetaRuleMemory) joinExistingPrefixes(conditionIndex int) bool {
	plan := m.rule.conditionPlans[conditionIndex]
	start := len(m.prefixes[conditionIndex])
	for _, prefixRow := range m.prefixes[conditionIndex-1] {
		prefix := prefixRow.prefix
		if len(plan.joins) == 0 {
			for _, row := range m.conditionMatches[conditionIndex] {
				match := row.match
				m.addPrefix(conditionIndex, betaPrefix{
					token: m.newTokenRef(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
				})
			}
			continue
		}
		key, ok := betaJoinKeyForPrefixToken(plan, prefix.token)
		if !ok {
			continue
		}
		bucket := m.conditionIndexes[conditionIndex][key]
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			row := m.conditionMatchRow(conditionIndex, rowID)
			if row == nil {
				continue
			}
			match := row.match
			m.addPrefix(conditionIndex, betaPrefix{
				token: m.newTokenRef(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
			})
		}
	}
	return len(m.prefixes[conditionIndex]) != start
}

func (m *reteBetaRuleMemory) appendRemovedFactDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, id FactID) []reteTerminalTokenDelta {
	if m == nil {
		return out
	}
	for _, row := range m.terminalPrefixRows() {
		if row.prefix.token.isZero() || !betaPrefixContainsFact(row.prefix, id) {
			continue
		}
		out = append(out, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          row.prefix.token,
			identityKey:    candidateIdentityForTerminalToken(m.rule, row.prefix.token).key,
		})
	}
	for conditionIndex := range m.conditionMatches {
		m.removeConditionMatch(conditionIndex, id)
	}
	for conditionIndex := range m.prefixes {
		m.removePrefixesContainingFact(conditionIndex, id)
	}
	return out
}

func (m *reteBetaRuleMemory) appendRightMatchDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, match conditionMatch, span *propagationCounterSpan) []reteTerminalTokenDelta {
	plan := m.rule.conditionPlans[conditionIndex]
	if conditionIndex == 0 {
		prefix := betaPrefix{
			token: m.newTokenRef(tokenRef{}, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
		}
		return m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, conditionIndex, prefix, span)
	}

	if len(plan.joins) == 0 {
		for _, row := range m.prefixes[conditionIndex-1] {
			prefix := row.prefix
			nextPrefix := betaPrefix{
				token: m.newTokenRef(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
			}
			out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, conditionIndex, nextPrefix, span)
		}
		return out
	}

	key, ok := betaJoinKeyForFact(plan, match.fact)
	if !ok {
		return out
	}
	bucket := m.prefixIndexes[conditionIndex-1][key]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		row := m.prefixRow(conditionIndex-1, rowID)
		if row == nil {
			continue
		}
		prefix := row.prefix
		nextPrefix := betaPrefix{
			token: m.newTokenRef(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
		}
		out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, conditionIndex, nextPrefix, span)
	}
	return out
}

func (m *reteBetaRuleMemory) appendAndPropagatePrefixDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, prefix betaPrefix, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if !m.addPrefix(conditionIndex, prefix) {
		return out
	}
	if span != nil {
		span.recordPrefixAdded()
	}
	if conditionIndex == len(m.rule.conditionPlans)-1 {
		if !prefix.token.isZero() {
			if span != nil {
				span.recordTerminalDeltaEmitted()
			}
			out = append(out, reteTerminalTokenDelta{
				ruleRevisionID: ruleRevisionID,
				token:          prefix.token,
				identityKey:    candidateIdentityForTerminalToken(m.rule, prefix.token).key,
			})
		}
		return out
	}
	return m.appendPropagatedPrefixDeltas(out, ruleRevisionID, conditionIndex, prefix, span)
}

func (m *reteBetaRuleMemory) appendPropagatedPrefixDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, prefix betaPrefix, span *propagationCounterSpan) []reteTerminalTokenDelta {
	nextCondition := conditionIndex + 1
	if m == nil || nextCondition >= len(m.rule.conditionPlans) {
		return out
	}
	if span != nil {
		span.recordBetaSuccessorReached()
	}
	plan := m.rule.conditionPlans[nextCondition]
	if len(plan.joins) == 0 {
		for _, row := range m.conditionMatches[nextCondition] {
			match := row.match
			nextPrefix := betaPrefix{
				token: m.newTokenRef(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
			}
			out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, nextCondition, nextPrefix, span)
		}
		return out
	}
	key, ok := betaJoinKeyForPrefixToken(plan, prefix.token)
	if !ok {
		return out
	}
	bucket := m.conditionIndexes[nextCondition][key]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		row := m.conditionMatchRow(nextCondition, rowID)
		if row == nil {
			continue
		}
		match := row.match
		nextPrefix := betaPrefix{
			token: m.newTokenRef(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
		}
		out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, nextCondition, nextPrefix, span)
	}
	return out
}

func (m *reteBetaRuleMemory) newTokenRef(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.tokenArena == nil {
		m.tokenArena = newTokenArena()
	}
	return m.tokenArena.add(parent, entry, match, recency, generation)
}

func (m *reteBetaRuleMemory) matchesForLeftPrefix(conditionIndex int, prefix betaPrefix) ([]conditionMatch, error) {
	plan := m.rule.conditionPlans[conditionIndex]
	if len(plan.joins) == 0 {
		return m.liveConditionMatches(conditionIndex), nil
	}
	key, ok := betaJoinKeyForPrefix(plan, m.prefixMatches(conditionIndex, prefix))
	if !ok {
		return nil, nil
	}
	matches := m.lookupScratch[conditionIndex][:0]
	bucket := m.conditionIndexes[conditionIndex][key]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		if row := m.conditionMatchRow(conditionIndex, rowID); row != nil {
			matches = append(matches, row.match)
		}
	}
	m.lookupScratch[conditionIndex] = matches
	return matches, nil
}

func (m *reteBetaRuleMemory) addConditionMatch(conditionIndex int, match conditionMatch) bool {
	if rowID, ok := m.findConditionMatchRow(conditionIndex, match.fact.ID()); ok {
		row := m.conditionMatchRow(conditionIndex, rowID)
		if row != nil && conditionMatchEqual(row.match, match) {
			return false
		}
		if row != nil {
			m.removeConditionMatchRow(conditionIndex, rowID)
		}
	}

	rowID := betaConditionMatchRowID(len(m.conditionMatches[conditionIndex]))
	rows := append(m.conditionMatches[conditionIndex], betaConditionMatchRow{
		id:    rowID,
		match: match,
	})
	m.conditionMatches[conditionIndex] = rows
	m.indexConditionMatchRow(conditionIndex, rowID)
	return true
}

func (m *reteBetaRuleMemory) removeConditionMatch(conditionIndex int, id FactID) {
	rowID, ok := m.findConditionMatchRow(conditionIndex, id)
	if !ok {
		return
	}
	m.removeConditionMatchRow(conditionIndex, rowID)
}

func (m *reteBetaRuleMemory) addPrefix(conditionIndex int, prefix betaPrefix) bool {
	if _, ok := m.findPrefixRow(conditionIndex, prefix.token); ok {
		return false
	}
	rowID := betaPrefixRowID(len(m.prefixes[conditionIndex]))
	rows := append(m.prefixes[conditionIndex], betaPrefixRow{
		id:     rowID,
		prefix: prefix,
	})
	m.prefixes[conditionIndex] = rows
	m.indexPrefixRow(conditionIndex, rowID)
	return true
}

func (m *reteBetaRuleMemory) removePrefixesContainingFact(conditionIndex int, id FactID) {
	rows := m.prefixes[conditionIndex]
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if !betaPrefixContainsFact(row.prefix, id) {
			continue
		}
		m.removePrefixRow(conditionIndex, betaPrefixRowID(i))
	}
}

func (m *reteBetaRuleMemory) terminalPrefixRows() []betaPrefixRow {
	if m == nil || len(m.prefixes) == 0 {
		return nil
	}
	return m.prefixes[len(m.prefixes)-1]
}

func (m *reteBetaRuleMemory) prefixRow(conditionIndex int, rowID betaPrefixRowID) *betaPrefixRow {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.prefixes) || rowID < 0 {
		return nil
	}
	rows := m.prefixes[conditionIndex]
	if int(rowID) >= len(rows) {
		return nil
	}
	return &rows[rowID]
}

func (m *reteBetaRuleMemory) findPrefixRow(conditionIndex int, token tokenRef) (betaPrefixRowID, bool) {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.prefixes) {
		return 0, false
	}
	rows := m.prefixes[conditionIndex]
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if tokenRefEqual(row.prefix.token, token) {
			return betaPrefixRowID(i), true
		}
	}
	return 0, false
}

func (m *reteBetaRuleMemory) prefixMatches(conditionIndex int, prefix betaPrefix) []conditionMatch {
	if m == nil || prefix.token.isZero() || conditionIndex <= 0 {
		return nil
	}
	size := min(conditionIndex, prefix.token.size())
	scratch := m.prefixScratch[conditionIndex]
	if cap(scratch) < size {
		scratch = make([]conditionMatch, size)
	} else {
		scratch = scratch[:size]
	}
	fillConditionMatchesFromTokenRef(scratch, prefix.token, size)
	m.prefixScratch[conditionIndex] = scratch
	return scratch
}

func (m *reteBetaRuleMemory) conditionMatchRow(conditionIndex int, rowID betaConditionMatchRowID) *betaConditionMatchRow {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.conditionMatches) || rowID < 0 {
		return nil
	}
	rows := m.conditionMatches[conditionIndex]
	if int(rowID) >= len(rows) {
		return nil
	}
	return &rows[rowID]
}

func (m *reteBetaRuleMemory) findConditionMatchRow(conditionIndex int, id FactID) (betaConditionMatchRowID, bool) {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.conditionMatches) {
		return 0, false
	}
	rows := m.conditionMatches[conditionIndex]
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.match.fact.ID() == id {
			return betaConditionMatchRowID(i), true
		}
	}
	return 0, false
}

func (m *reteBetaRuleMemory) liveConditionMatches(conditionIndex int) []conditionMatch {
	rows := m.conditionMatches[conditionIndex]
	scratch := m.lookupScratch[conditionIndex][:0]
	for _, row := range rows {
		scratch = append(scratch, row.match)
	}
	m.lookupScratch[conditionIndex] = scratch
	return scratch
}

func collectMatchCandidatesFromPrefixRows(ctx context.Context, rule compiledRule, generation Generation, prefixes []betaPrefixRow, scratch *candidateScratch) ([]matchCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(prefixes) == 0 {
		return nil, nil
	}

	candidateCount, entryCount, pathCount := countPrefixCandidateSpace(prefixes)
	var candidates []matchCandidate
	if scratch != nil {
		scratch.reset(candidateCount, entryCount, pathCount)
		candidates = scratch.candidates[:0]
	} else {
		candidates = make([]matchCandidate, 0, candidateCount)
	}
	var seen *candidateSeenSet
	if scratch != nil {
		seen = &scratch.seen
	} else {
		localSeen := newCandidateSeenSet(candidateCount)
		seen = &localSeen
	}
	for _, prefix := range prefixes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if prefix.prefix.token.isZero() {
			continue
		}
		candidate, err := buildMatchCandidateFromTokenRefWithScratch(rule, generation, prefix.prefix.token, scratch)
		if err != nil {
			return nil, err
		}
		if seen.seen(candidates, candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if scratch != nil {
		scratch.candidates = candidates
	}
	sortMatchCandidates(nil, candidates)
	return candidates, nil
}

func countPrefixCandidateSpace(prefixes []betaPrefixRow) (candidateCount, entryCount, pathCount int) {
	for _, prefix := range prefixes {
		if prefix.prefix.token.isZero() {
			continue
		}
		candidateCount++
		entryCount += prefix.prefix.token.size()
		pathCount += prefix.prefix.token.pathLen()
	}
	return candidateCount, entryCount, pathCount
}

func (m *reteBetaRuleMemory) conditionMatchJoinKey(conditionIndex int, match conditionMatch) (betaJoinKey, bool) {
	plan := m.rule.conditionPlans[conditionIndex]
	if len(plan.joins) == 0 {
		return betaJoinKey{}, false
	}
	return betaJoinKeyForFact(plan, match.fact)
}

func (m *reteBetaRuleMemory) indexConditionMatchRow(conditionIndex int, rowID betaConditionMatchRowID) {
	row := m.conditionMatchRow(conditionIndex, rowID)
	if row == nil {
		return
	}
	key, ok := m.conditionMatchJoinKey(conditionIndex, row.match)
	if !ok {
		return
	}
	if m.conditionIndexes[conditionIndex] == nil {
		m.conditionIndexes[conditionIndex] = make(map[betaJoinKey]betaConditionMatchIndexBucket)
	}
	bucket := m.conditionIndexes[conditionIndex][key]
	bucket.append(rowID)
	m.conditionIndexes[conditionIndex][key] = bucket
}

func (m *reteBetaRuleMemory) removeConditionMatchRow(conditionIndex int, rowID betaConditionMatchRowID) {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.conditionMatches) || rowID < 0 {
		return
	}
	rows := m.conditionMatches[conditionIndex]
	index := int(rowID)
	if index >= len(rows) {
		return
	}

	removed := rows[index]
	if key, ok := m.conditionMatchJoinKey(conditionIndex, removed.match); ok {
		m.removeConditionIndexRowID(conditionIndex, key, rowID)
	}

	last := len(rows) - 1
	if index != last {
		moved := rows[last]
		moved.id = betaConditionMatchRowID(index)
		rows[index] = moved
		if key, ok := m.conditionMatchJoinKey(conditionIndex, moved.match); ok {
			m.replaceConditionIndexRowID(conditionIndex, key, betaConditionMatchRowID(last), moved.id)
		}
	}
	rows[last] = betaConditionMatchRow{}
	m.conditionMatches[conditionIndex] = rows[:last]
	clear(m.lookupScratch[conditionIndex])
	m.lookupScratch[conditionIndex] = m.lookupScratch[conditionIndex][:0]
}

func (m *reteBetaRuleMemory) prefixJoinKey(conditionIndex int, prefix betaPrefix) (betaJoinKey, bool) {
	nextCondition := conditionIndex + 1
	if nextCondition >= len(m.rule.conditionPlans) {
		return betaJoinKey{}, false
	}
	nextPlan := m.rule.conditionPlans[nextCondition]
	if len(nextPlan.joins) == 0 {
		return betaJoinKey{}, false
	}
	return betaJoinKeyForPrefix(nextPlan, m.prefixMatches(nextCondition, prefix))
}

func (m *reteBetaRuleMemory) indexPrefixRow(conditionIndex int, rowID betaPrefixRowID) {
	row := m.prefixRow(conditionIndex, rowID)
	if row == nil {
		return
	}
	key, ok := m.prefixJoinKey(conditionIndex, row.prefix)
	if !ok {
		return
	}
	if m.prefixIndexes[conditionIndex] == nil {
		m.prefixIndexes[conditionIndex] = make(map[betaJoinKey]betaPrefixIndexBucket)
	}
	bucket := m.prefixIndexes[conditionIndex][key]
	bucket.append(rowID)
	m.prefixIndexes[conditionIndex][key] = bucket
}

func (m *reteBetaRuleMemory) removePrefixRow(conditionIndex int, rowID betaPrefixRowID) {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.prefixes) || rowID < 0 {
		return
	}
	rows := m.prefixes[conditionIndex]
	index := int(rowID)
	if index >= len(rows) {
		return
	}

	removed := rows[index]
	if key, ok := m.prefixJoinKey(conditionIndex, removed.prefix); ok {
		m.removePrefixIndexRowID(conditionIndex, key, rowID)
	}

	last := len(rows) - 1
	if index != last {
		moved := rows[last]
		moved.id = betaPrefixRowID(index)
		rows[index] = moved
		if key, ok := m.prefixJoinKey(conditionIndex, moved.prefix); ok {
			m.replacePrefixIndexRowID(conditionIndex, key, betaPrefixRowID(last), moved.id)
		}
	}
	rows[last] = betaPrefixRow{}
	m.prefixes[conditionIndex] = rows[:last]
	clear(m.prefixScratch[conditionIndex])
	m.prefixScratch[conditionIndex] = m.prefixScratch[conditionIndex][:0]
	m.candidateScratch.reset(0, 0, 0)
}

func (m *reteBetaRuleMemory) removeConditionIndexRowID(conditionIndex int, key betaJoinKey, rowID betaConditionMatchRowID) {
	index := m.conditionIndexes[conditionIndex]
	bucket := index[key]
	if !bucket.remove(rowID) {
		return
	}
	if bucket.len() == 0 {
		delete(index, key)
	} else {
		index[key] = bucket
	}
}

func (m *reteBetaRuleMemory) replaceConditionIndexRowID(conditionIndex int, key betaJoinKey, oldID, newID betaConditionMatchRowID) {
	bucket := m.conditionIndexes[conditionIndex][key]
	if bucket.replace(oldID, newID) {
		m.conditionIndexes[conditionIndex][key] = bucket
	}
}

func (m *reteBetaRuleMemory) removePrefixIndexRowID(conditionIndex int, key betaJoinKey, rowID betaPrefixRowID) {
	index := m.prefixIndexes[conditionIndex]
	bucket := index[key]
	if !bucket.remove(rowID) {
		return
	}
	if bucket.len() == 0 {
		delete(index, key)
	} else {
		index[key] = bucket
	}
}

func (m *reteBetaRuleMemory) replacePrefixIndexRowID(conditionIndex int, key betaJoinKey, oldID, newID betaPrefixRowID) {
	bucket := m.prefixIndexes[conditionIndex][key]
	if bucket.replace(oldID, newID) {
		m.prefixIndexes[conditionIndex][key] = bucket
	}
}

func betaConditionMatch(plan compiledConditionPlan, fact FactSnapshot) (conditionMatch, bool, error) {
	ref := newConditionFactRefFromSnapshot(fact)
	if !plan.matchesFact(ref) {
		return conditionMatch{}, false, nil
	}
	ok, err := plan.matchesConstraints(nil, ref)
	if err != nil || !ok {
		return conditionMatch{}, false, err
	}
	return conditionMatch{
		conditionID: plan.id,
		bindingSlot: plan.bindingSlot,
		fact:        ref,
	}, true, nil
}

func betaConditionMatchWorking(plan compiledConditionPlan, fact *workingFact) (conditionMatch, bool, error) {
	if !plan.matchesFactWorking(fact) {
		return conditionMatch{}, false, nil
	}
	ok, err := plan.matchesConstraintsWorking(nil, fact)
	if err != nil || !ok {
		return conditionMatch{}, false, err
	}
	return conditionMatch{
		conditionID: plan.id,
		bindingSlot: plan.bindingSlot,
		fact:        newConditionFactRefFromWorkingFact(fact),
	}, true, nil
}

func conditionMatchLess(left, right conditionMatch) bool {
	if left.fact.ID() != right.fact.ID() {
		return factIDLess(left.fact.ID(), right.fact.ID())
	}
	return left.fact.Version() < right.fact.Version()
}

func conditionMatchEqual(left, right conditionMatch) bool {
	return left.conditionID == right.conditionID &&
		left.bindingSlot == right.bindingSlot &&
		left.fact.ID() == right.fact.ID() &&
		left.fact.Version() == right.fact.Version()
}

func betaPrefixLess(left, right betaPrefix) bool {
	if left.token.isZero() || right.token.isZero() {
		return left.token.isZero() && !right.token.isZero()
	}
	if left.token.size() != right.token.size() {
		return left.token.size() < right.token.size()
	}
	return compareTokenRef(left.token, right.token) < 0
}

func betaPrefixEqual(left, right betaPrefix) bool {
	return tokenRefEqual(left.token, right.token)
}

func tokenRefEqual(left, right tokenRef) bool {
	if left.isZero() || right.isZero() {
		return left.isZero() && right.isZero()
	}
	if left.handle == right.handle {
		return true
	}
	if left.size() != right.size() || left.generation() != right.generation() || left.identityState() != right.identityState() {
		return false
	}
	for currentLeft, currentRight := left, right; !currentLeft.isZero() || !currentRight.isZero(); currentLeft, currentRight = currentLeft.parent(), currentRight.parent() {
		leftRow, leftOK := currentLeft.resolve()
		rightRow, rightOK := currentRight.resolve()
		if !leftOK || !rightOK {
			return false
		}
		if leftRow.match.fact.ID() != rightRow.match.fact.ID() || leftRow.match.fact.Version() != rightRow.match.fact.Version() {
			return false
		}
		if leftRow.parent.isZero() || rightRow.parent.isZero() {
			return leftRow.parent.isZero() && rightRow.parent.isZero()
		}
	}
	return true
}

func compareTokenRef(left, right tokenRef) int {
	if left.isZero() || right.isZero() {
		switch {
		case left.isZero() && !right.isZero():
			return -1
		case !left.isZero() && right.isZero():
			return 1
		default:
			return 0
		}
	}
	if left.size() != right.size() {
		if left.size() < right.size() {
			return -1
		}
		return 1
	}
	leftRow, leftOK := left.resolve()
	rightRow, rightOK := right.resolve()
	if !leftOK || !rightOK {
		switch {
		case !leftOK && rightOK:
			return -1
		case leftOK && !rightOK:
			return 1
		default:
			return 0
		}
	}
	if leftRow.parent.generation != 0 || rightRow.parent.generation != 0 {
		if cmp := compareTokenRef(left.parent(), right.parent()); cmp != 0 {
			return cmp
		}
	}
	if leftRow.match.fact.ID() != rightRow.match.fact.ID() {
		if factIDLess(leftRow.match.fact.ID(), rightRow.match.fact.ID()) {
			return -1
		}
		return 1
	}
	if leftRow.match.fact.Version() != rightRow.match.fact.Version() {
		if leftRow.match.fact.Version() < rightRow.match.fact.Version() {
			return -1
		}
		return 1
	}
	return 0
}

func betaPrefixContainsFact(prefix betaPrefix, id FactID) bool {
	return prefix.token.containsFact(id)
}

func betaJoinKeyForPrefixToken(plan compiledConditionPlan, token tokenRef) (betaJoinKey, bool) {
	return betaJoinKeyForPlan(plan, func(join compiledJoinConstraint) (Value, bool) {
		match, ok := tokenRefAtSlot(token, join.refBindingSlot)
		if !ok {
			return Value{}, false
		}
		return match.fact.compiledFieldValue(join.refField, join.refFieldSlot)
	})
}

func tokenRefAtSlot(token tokenRef, slot int) (conditionMatch, bool) {
	if token.isZero() || slot < 0 {
		return conditionMatch{}, false
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return conditionMatch{}, false
		}
		if row.size == slot+1 {
			return row.match, true
		}
		if row.size <= slot {
			return conditionMatch{}, false
		}
	}
	return conditionMatch{}, false
}

func fillConditionMatchesFromTokenRef(out []conditionMatch, token tokenRef, limit int) int {
	if token.isZero() || limit <= 0 {
		return 0
	}
	row, ok := token.resolve()
	if !ok {
		return 0
	}
	written := fillConditionMatchesFromTokenRef(out, token.parent(), limit)
	if written >= limit {
		return written
	}
	out[written] = row.match
	return written + 1
}

func resetConditionMatchIndexBuckets(index map[betaJoinKey]betaConditionMatchIndexBucket) {
	for key, bucket := range index {
		index[key] = bucket.reset()
	}
}

func resetPrefixIndexBuckets(index map[betaJoinKey]betaPrefixIndexBucket) {
	for key, bucket := range index {
		index[key] = bucket.reset()
	}
}

func betaJoinKeyForFact(plan compiledConditionPlan, fact conditionFactRef) (betaJoinKey, bool) {
	return betaJoinKeyForPlan(plan, func(join compiledJoinConstraint) (Value, bool) {
		return fact.compiledFieldValue(join.field, join.fieldSlot)
	})
}

func betaJoinKeyForPrefix(plan compiledConditionPlan, matches []conditionMatch) (betaJoinKey, bool) {
	return betaJoinKeyForPlan(plan, func(join compiledJoinConstraint) (Value, bool) {
		if join.refBindingSlot < 0 || join.refBindingSlot >= len(matches) {
			return Value{}, false
		}
		return matches[join.refBindingSlot].fact.compiledFieldValue(join.refField, join.refFieldSlot)
	})
}

func betaJoinKeyForPlan(plan compiledConditionPlan, valueForJoin func(join compiledJoinConstraint) (Value, bool)) (betaJoinKey, bool) {
	if len(plan.joins) == 0 {
		return betaJoinKey{}, true
	}

	if len(plan.joins) == 1 {
		join := plan.joins[0]
		if join.indexKind != joinIndexEquality {
			return betaJoinKey{}, false
		}
		value, ok := valueForJoin(join)
		if !ok {
			return betaJoinKey{}, false
		}
		if key, ok := betaJoinKeyForValue(value); ok {
			return key, true
		}
		return betaJoinKey{
			kind:        betaJoinKeyFallback,
			stringValue: value.canonicalKey(),
		}, true
	}

	var b strings.Builder
	for _, join := range plan.joins {
		if join.indexKind != joinIndexEquality {
			return betaJoinKey{}, false
		}
		value, ok := valueForJoin(join)
		if !ok {
			return betaJoinKey{}, false
		}
		b.WriteByte('|')
		b.WriteString(value.canonicalKey())
	}
	return betaJoinKey{
		kind:        betaJoinKeyFallback,
		stringValue: b.String(),
	}, true
}

func betaJoinKeyForValue(value Value) (betaJoinKey, bool) {
	switch value.Kind() {
	case ValueNull:
		return betaJoinKey{kind: betaJoinKeyNull}, true
	case ValueBool:
		return betaJoinKey{kind: betaJoinKeyBool, boolValue: value.data.(bool)}, true
	case ValueInt:
		return betaJoinKey{kind: betaJoinKeyInt, intValue: value.data.(int64)}, true
	case ValueFloat:
		if integer, ok := betaJoinIntFromFloat(value.data.(float64)); ok {
			return betaJoinKey{kind: betaJoinKeyInt, intValue: integer}, true
		}
		return betaJoinKey{kind: betaJoinKeyFloat, floatBits: math.Float64bits(value.data.(float64))}, true
	case ValueString:
		return betaJoinKey{kind: betaJoinKeyString, stringValue: value.data.(string)}, true
	default:
		return betaJoinKey{}, false
	}
}

func betaJoinIntFromFloat(floating float64) (int64, bool) {
	if floating > float64(maxExactFloatInt) || floating < float64(-maxExactFloatInt) {
		return 0, false
	}
	if math.Trunc(floating) != floating {
		return 0, false
	}
	return int64(floating), true
}
