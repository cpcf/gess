package gess

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

type reteBetaMemory struct {
	revision            *Ruleset
	rules               map[RuleRevisionID]*reteBetaRuleMemory
	terminalTokenDeltas []reteTerminalTokenDelta
	compactionNeeded    bool
}

type reteAgendaDelta struct {
	supported bool
	added     []reteTerminalTokenDelta
	removed   []reteTerminalTokenDelta
}

type reteTerminalTokenDelta struct {
	ruleRevisionID RuleRevisionID
	token          *matchToken
}

type reteBetaRuleMemory struct {
	rule              compiledRule
	conditionMatches  [][]betaConditionMatchRow
	conditionIndexes  []map[betaJoinKey][]betaConditionMatchRowID
	prefixes          [][]betaPrefixRow
	prefixIndexes     []map[betaJoinKey][]betaPrefixRowID
	prefixViewScratch [][]betaPrefix
	tokenBacking      [][]matchToken
	lookupScratch     [][]conditionMatch
	prefixScratch     [][]conditionMatch
	compactionNeeded  bool
	candidateScratch  candidateScratch
}

type betaConditionMatchRowID int

type betaConditionMatchRow struct {
	id    betaConditionMatchRowID
	live  bool
	match conditionMatch
}

type betaPrefix struct {
	token *matchToken
}

type betaPrefixRowID int

type betaPrefixRow struct {
	id     betaPrefixRowID
	live   bool
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

		candidates, err := collectMatchCandidatesFromPrefixes(ctx, rule, generation, ruleMemory.terminalPrefixes(), &ruleMemory.candidateScratch)
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
		for _, prefix := range ruleMemory.terminalPrefixes() {
			if prefix.token == nil {
				continue
			}
			deltas = append(deltas, reteTerminalTokenDelta{
				ruleRevisionID: rule.revisionID,
				token:          prefix.token,
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
	m.compactionNeeded = false
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

func (m *reteBetaMemory) matchRuleCandidates(ctx context.Context, source factSource, rule compiledRule, alphaSource alphaFactSource) ([]matchCandidate, error) {
	if source == nil {
		return nil, ErrInvalidRuleset
	}
	ruleMemory := m.rules[rule.revisionID]
	if ruleMemory == nil {
		return rule.matchCandidatesWithAlpha(ctx, source, alphaSource)
	}
	return collectMatchCandidatesFromPrefixes(ctx, rule, source.sourceGeneration(), ruleMemory.terminalPrefixes(), &ruleMemory.candidateScratch)
}

func (m *reteBetaMemory) insertFact(fact FactSnapshot, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.revision == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
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
	return delta
}

func (m *reteBetaMemory) insertFactGenerated(fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.revision == nil || fact == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
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
		delta.added = ruleMemory.appendInsertedFactDeltasGenerated(delta.added, rule.revisionID, fact, snapshot, span)
	}
	return delta
}

func (m *reteBetaMemory) insertFactForRules(fact FactSnapshot, ruleRevisionIDs []RuleRevisionID, span *propagationCounterSpan) (reteAgendaDelta, bool) {
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
	return delta, true
}

func (m *reteBetaMemory) insertFactForRulesGenerated(fact *workingFact, snapshot FactSnapshot, ruleRevisionIDs []RuleRevisionID, span *propagationCounterSpan) (reteAgendaDelta, bool) {
	if m == nil || m.revision == nil || fact == nil {
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
		delta.added = ruleMemory.appendInsertedFactDeltasGenerated(delta.added, rule.revisionID, fact, snapshot, span)
	}
	return delta, true
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

	delta := reteAgendaDelta{supported: true}
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
	return delta, true
}

func (m *reteBetaMemory) insertFactForConditionRoutesGenerated(fact *workingFact, snapshot FactSnapshot, routes []reteBetaConditionRoute, span *propagationCounterSpan) (reteAgendaDelta, bool) {
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

	delta := reteAgendaDelta{supported: true}
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
		delta.added = ruleMemory.appendInsertedFactDeltaForConditionGenerated(delta.added, rule.revisionID, route.conditionIndex, fact, snapshot, span)
	}
	return delta, true
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
		if ruleMemory.compactionNeeded {
			m.compactionNeeded = true
		}
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
		if ruleMemory.compactionNeeded {
			m.compactionNeeded = true
		}
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
		rule:              rule,
		conditionMatches:  make([][]betaConditionMatchRow, conditions),
		conditionIndexes:  make([]map[betaJoinKey][]betaConditionMatchRowID, conditions),
		prefixes:          make([][]betaPrefixRow, conditions),
		prefixIndexes:     make([]map[betaJoinKey][]betaPrefixRowID, conditions),
		prefixViewScratch: make([][]betaPrefix, conditions),
		tokenBacking:      make([][]matchToken, 0, conditions),
		lookupScratch:     make([][]conditionMatch, conditions),
		prefixScratch:     make([][]conditionMatch, conditions),
	}
}

func (m *reteBetaRuleMemory) resetFacts(facts []FactSnapshot) {
	if m == nil {
		return
	}
	defer m.trimTokenBacking()
	m.clear()
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
				if !row.live {
					continue
				}
				match := row.match
				m.addPrefix(conditionIndex, betaPrefix{
					token: m.newMatchToken(nil, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
				})
			}
		} else {
			prefixes, err := m.joinExistingPrefixes(conditionIndex)
			if err != nil {
				return
			}
			if len(prefixes) == 0 {
				return
			}
			for _, prefix := range prefixes {
				m.addPrefix(conditionIndex, prefix)
			}
		}
	}
}

func (m *reteBetaRuleMemory) clear() {
	if m == nil {
		return
	}
	m.compactionNeeded = false
	for conditionIndex := range m.conditionMatches {
		for i := range m.conditionMatches[conditionIndex] {
			m.conditionMatches[conditionIndex][i] = betaConditionMatchRow{}
		}
		m.conditionMatches[conditionIndex] = m.conditionMatches[conditionIndex][:0]
		resetJoinIndexBuckets(m.conditionIndexes[conditionIndex])

		for i := range m.prefixes[conditionIndex] {
			m.prefixes[conditionIndex][i] = betaPrefixRow{}
		}
		m.prefixes[conditionIndex] = m.prefixes[conditionIndex][:0]
		resetJoinIndexBuckets(m.prefixIndexes[conditionIndex])
		for i := range m.prefixViewScratch[conditionIndex] {
			m.prefixViewScratch[conditionIndex][i] = betaPrefix{}
		}
		m.prefixViewScratch[conditionIndex] = m.prefixViewScratch[conditionIndex][:0]
		for i := range m.prefixScratch[conditionIndex] {
			m.prefixScratch[conditionIndex][i] = conditionMatch{}
		}
		m.lookupScratch[conditionIndex] = m.lookupScratch[conditionIndex][:0]
		m.prefixScratch[conditionIndex] = m.prefixScratch[conditionIndex][:0]
	}
	for chunkIndex, chunk := range m.tokenBacking {
		for i := range chunk {
			chunk[i] = matchToken{}
		}
		m.tokenBacking[chunkIndex] = chunk[:0]
	}
	m.candidateScratch.reset(0, 0, 0)
}

func (m *reteBetaRuleMemory) trimTokenBacking() {
	if m == nil || len(m.tokenBacking) == 0 {
		return
	}
	first := -1
	last := -1
	for i, chunk := range m.tokenBacking {
		if len(chunk) == 0 {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		keep := min(reteBetaMatchTokenChunkReserve, len(m.tokenBacking))
		for i := keep; i < len(m.tokenBacking); i++ {
			m.tokenBacking[i] = nil
		}
		m.tokenBacking = m.tokenBacking[:keep]
		return
	}
	liveLen := last - first + 1
	reserveLen := min(reteBetaMatchTokenChunkReserve, first)
	totalKeep := liveLen + reserveLen
	copy(m.tokenBacking[:liveLen], m.tokenBacking[first:last+1])
	if reserveLen > 0 {
		copy(m.tokenBacking[liveLen:totalKeep], m.tokenBacking[first-reserveLen:first])
	}
	for i := totalKeep; i < len(m.tokenBacking); i++ {
		m.tokenBacking[i] = nil
	}
	m.tokenBacking = m.tokenBacking[:totalKeep]
}

func (m *reteBetaRuleMemory) compactTokenBacking() {
	if m == nil || len(m.tokenBacking) <= 1 {
		return
	}
	capacity := m.tokenBackingCapacity()
	if capacity <= reteBetaMatchTokenChunkSize {
		return
	}
	liveUpperBound := m.liveTokenUpperBound()
	if liveUpperBound == 0 {
		for i := range m.tokenBacking {
			m.tokenBacking[i] = nil
		}
		m.tokenBacking = m.tokenBacking[:0]
		return
	}
	if liveUpperBound*4 >= capacity {
		return
	}

	seen := make(map[*matchToken]struct{}, liveUpperBound)
	liveCount := 0
	for _, rows := range m.prefixes {
		for _, row := range rows {
			if !row.live {
				continue
			}
			liveCount += countLiveTokens(row.prefix.token, seen)
		}
	}
	if liveCount == 0 {
		for i := range m.tokenBacking {
			m.tokenBacking[i] = nil
		}
		m.tokenBacking = m.tokenBacking[:0]
		return
	}
	if liveCount*4 >= capacity {
		return
	}

	chunkCount := (liveCount + reteBetaMatchTokenChunkSize - 1) / reteBetaMatchTokenChunkSize
	cloned := make(map[*matchToken]*matchToken, liveCount)
	newBacking := make([][]matchToken, 0, chunkCount)
	var clone func(*matchToken) *matchToken
	clone = func(token *matchToken) *matchToken {
		if token == nil {
			return nil
		}
		if next, ok := cloned[token]; ok {
			return next
		}
		parent := clone(token.parent)
		if len(newBacking) == 0 || len(newBacking[len(newBacking)-1]) == cap(newBacking[len(newBacking)-1]) {
			newBacking = append(newBacking, make([]matchToken, 0, reteBetaMatchTokenChunkSize))
		}
		copied := *token
		copied.parent = parent
		chunkIndex := len(newBacking) - 1
		chunk := append(newBacking[chunkIndex], copied)
		newBacking[chunkIndex] = chunk
		next := &newBacking[chunkIndex][len(chunk)-1]
		cloned[token] = next
		return next
	}

	for conditionIndex := range m.prefixes {
		rows := m.prefixes[conditionIndex]
		for i := range rows {
			rows[i].prefix.token = clone(rows[i].prefix.token)
		}
	}
	for i := range m.tokenBacking {
		m.tokenBacking[i] = nil
	}
	m.tokenBacking = newBacking
}

func (m *reteBetaRuleMemory) tokenBackingCapacity() int {
	if m == nil {
		return 0
	}
	capacity := 0
	for _, chunk := range m.tokenBacking {
		capacity += cap(chunk)
	}
	return capacity
}

func (m *reteBetaRuleMemory) liveTokenUpperBound() int {
	if m == nil {
		return 0
	}
	count := 0
	for _, rows := range m.prefixes {
		for _, row := range rows {
			if row.live {
				count++
			}
		}
	}
	return count
}

func countLiveTokens(token *matchToken, seen map[*matchToken]struct{}) int {
	if token == nil {
		return 0
	}
	if _, ok := seen[token]; ok {
		return 0
	}
	seen[token] = struct{}{}
	return 1 + countLiveTokens(token.parent, seen)
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

func (m *reteBetaRuleMemory) appendInsertedFactDeltasGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil {
		return out
	}
	for conditionIndex, plan := range m.rule.conditionPlans {
		out = m.appendInsertedFactDeltaForConditionPlanGenerated(out, ruleRevisionID, conditionIndex, plan, fact, snapshot, span)
	}
	return out
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForCondition(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, fact FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.rule.conditionPlans) {
		return out
	}
	return m.appendInsertedFactDeltaForConditionPlan(out, ruleRevisionID, conditionIndex, m.rule.conditionPlans[conditionIndex], fact, span)
}

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.rule.conditionPlans) {
		return out
	}
	return m.appendInsertedFactDeltaForConditionPlanGenerated(out, ruleRevisionID, conditionIndex, m.rule.conditionPlans[conditionIndex], fact, snapshot, span)
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

func (m *reteBetaRuleMemory) appendInsertedFactDeltaForConditionPlanGenerated(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, conditionIndex int, plan compiledConditionPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) []reteTerminalTokenDelta {
	if span != nil {
		span.recordConditionPlanTested()
	}
	match, ok, err := betaConditionMatchWorking(plan, fact, snapshot)
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

func (m *reteBetaRuleMemory) joinExistingPrefixes(conditionIndex int) ([]betaPrefix, error) {
	plan := m.rule.conditionPlans[conditionIndex]
	out := m.prefixViewScratch[conditionIndex][:0]
	for _, prefix := range m.livePrefixes(conditionIndex - 1) {
		if len(plan.joins) == 0 {
			for _, match := range m.liveConditionMatches(conditionIndex) {
				out = append(out, betaPrefix{
					token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
				})
			}
			continue
		}
		key, ok := betaJoinKeyForPrefixToken(plan, prefix.token)
		if !ok {
			continue
		}
		for _, rowID := range m.conditionIndexes[conditionIndex][key] {
			row := m.conditionMatchRow(conditionIndex, rowID)
			if row == nil || !row.live {
				continue
			}
			match := row.match
			out = append(out, betaPrefix{
				token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
			})
		}
	}
	m.prefixViewScratch[conditionIndex] = out
	return out, nil
}

func (m *reteBetaRuleMemory) appendRemovedFactDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, id FactID) []reteTerminalTokenDelta {
	if m == nil {
		return out
	}
	for _, prefix := range m.terminalPrefixes() {
		if prefix.token == nil || !betaPrefixContainsFact(prefix, id) {
			continue
		}
		out = append(out, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          prefix.token,
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
			token: m.newMatchToken(nil, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
		}
		return m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, conditionIndex, prefix, span)
	}

	if len(plan.joins) == 0 {
		for _, prefix := range m.livePrefixes(conditionIndex - 1) {
			nextPrefix := betaPrefix{
				token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
			}
			out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, conditionIndex, nextPrefix, span)
		}
		return out
	}

	key, ok := betaJoinKeyForFact(plan, match.fact)
	if !ok {
		return out
	}
	for _, rowID := range m.prefixIndexes[conditionIndex-1][key] {
		row := m.prefixRow(conditionIndex-1, rowID)
		if row == nil || !row.live {
			continue
		}
		prefix := row.prefix
		nextPrefix := betaPrefix{
			token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
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
		if prefix.token != nil {
			if span != nil {
				span.recordTerminalDeltaEmitted()
			}
			out = append(out, reteTerminalTokenDelta{
				ruleRevisionID: ruleRevisionID,
				token:          prefix.token,
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
		for _, match := range m.liveConditionMatches(nextCondition) {
			nextPrefix := betaPrefix{
				token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
			}
			out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, nextCondition, nextPrefix, span)
		}
		return out
	}
	key, ok := betaJoinKeyForPrefixToken(plan, prefix.token)
	if !ok {
		return out
	}
	for _, rowID := range m.conditionIndexes[nextCondition][key] {
		row := m.conditionMatchRow(nextCondition, rowID)
		if row == nil || !row.live {
			continue
		}
		match := row.match
		nextPrefix := betaPrefix{
			token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), span),
		}
		out = m.appendAndPropagatePrefixDeltas(out, ruleRevisionID, nextCondition, nextPrefix, span)
	}
	return out
}

func (m *reteBetaRuleMemory) newMatchToken(parent *matchToken, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation, span *propagationCounterSpan) *matchToken {
	if m == nil {
		return nil
	}
	if span != nil {
		span.recordTokenCreated()
	}
	token := makeMatchToken(parent, entry, match, recency, generation)
	chunks := m.tokenBacking
	last := len(chunks) - 1
	if last < 0 || len(chunks[last]) == cap(chunks[last]) {
		chunks = append(chunks, make([]matchToken, 0, reteBetaMatchTokenChunkSize))
		last = len(chunks) - 1
	}
	chunk := append(chunks[last], token)
	chunks[last] = chunk
	m.tokenBacking = chunks
	return &chunks[last][len(chunk)-1]
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
	indexes := m.conditionIndexes[conditionIndex][key]
	matches := m.lookupScratch[conditionIndex][:0]
	for _, rowID := range indexes {
		if row := m.conditionMatchRow(conditionIndex, rowID); row != nil && row.live {
			matches = append(matches, row.match)
		}
	}
	m.lookupScratch[conditionIndex] = matches
	return matches, nil
}

func (m *reteBetaRuleMemory) addConditionMatch(conditionIndex int, match conditionMatch) bool {
	if rowID, ok := m.findLiveConditionMatchRow(conditionIndex, match.fact.ID()); ok {
		row := m.conditionMatchRow(conditionIndex, rowID)
		if row != nil && conditionMatchEqual(row.match, match) {
			return false
		}
		if row != nil {
			row.live = false
			row.match = conditionMatch{}
		}
	}

	rowID := betaConditionMatchRowID(len(m.conditionMatches[conditionIndex]))
	rows := append(m.conditionMatches[conditionIndex], betaConditionMatchRow{
		id:    rowID,
		live:  true,
		match: match,
	})
	m.conditionMatches[conditionIndex] = rows
	m.indexConditionMatch(conditionIndex, rowID, match)
	return true
}

func (m *reteBetaRuleMemory) removeConditionMatch(conditionIndex int, id FactID) {
	rowID, ok := m.findLiveConditionMatchRow(conditionIndex, id)
	if !ok {
		return
	}
	row := m.conditionMatchRow(conditionIndex, rowID)
	if row == nil || !row.live {
		return
	}
	row.live = false
	row.match = conditionMatch{}
	m.compactionNeeded = true
}

func (m *reteBetaRuleMemory) addPrefix(conditionIndex int, prefix betaPrefix) bool {
	if _, ok := m.findLivePrefixRow(conditionIndex, prefix.token); ok {
		return false
	}
	rowID := betaPrefixRowID(len(m.prefixes[conditionIndex]))
	rows := append(m.prefixes[conditionIndex], betaPrefixRow{
		id:     rowID,
		live:   true,
		prefix: prefix,
	})
	m.prefixes[conditionIndex] = rows
	m.indexPrefix(conditionIndex, rowID, prefix)
	return true
}

func (m *reteBetaRuleMemory) removePrefixesContainingFact(conditionIndex int, id FactID) {
	rows := m.prefixes[conditionIndex]
	removed := false
	for i := range rows {
		row := &rows[i]
		if !row.live || !betaPrefixContainsFact(row.prefix, id) {
			continue
		}
		removed = true
		row.live = false
		row.prefix = betaPrefix{}
	}
	if removed {
		m.compactionNeeded = true
		m.prefixes[conditionIndex] = rows
	}
}

func (m *reteBetaRuleMemory) compactRows() bool {
	if m == nil || !m.compactionNeeded {
		return false
	}

	compacted := false
	for conditionIndex, rows := range m.conditionMatches {
		if len(rows) == 0 {
			continue
		}
		liveRows := rows[:0]
		for i := range rows {
			row := rows[i]
			if !row.live {
				continue
			}
			row.id = betaConditionMatchRowID(len(liveRows))
			liveRows = append(liveRows, row)
		}
		if len(liveRows) == len(rows) {
			continue
		}
		compacted = true
		for i := len(liveRows); i < len(rows); i++ {
			rows[i] = betaConditionMatchRow{}
		}
		m.conditionMatches[conditionIndex] = liveRows
		m.rebuildConditionIndex(conditionIndex)
	}

	for conditionIndex, rows := range m.prefixes {
		if len(rows) == 0 {
			continue
		}
		liveRows := rows[:0]
		for i := range rows {
			row := rows[i]
			if !row.live {
				continue
			}
			row.id = betaPrefixRowID(len(liveRows))
			liveRows = append(liveRows, row)
		}
		if len(liveRows) == len(rows) {
			continue
		}
		compacted = true
		for i := len(liveRows); i < len(rows); i++ {
			rows[i] = betaPrefixRow{}
		}
		m.prefixes[conditionIndex] = liveRows
		m.rebuildPrefixIndex(conditionIndex)
	}

	if compacted {
		for i := range m.prefixViewScratch {
			clear(m.prefixViewScratch[i])
			m.prefixViewScratch[i] = m.prefixViewScratch[i][:0]
		}
		for i := range m.lookupScratch {
			clear(m.lookupScratch[i])
			m.lookupScratch[i] = m.lookupScratch[i][:0]
		}
		for i := range m.prefixScratch {
			clear(m.prefixScratch[i])
			m.prefixScratch[i] = m.prefixScratch[i][:0]
		}
		m.candidateScratch.reset(0, 0, 0)
	}
	m.compactionNeeded = false
	return compacted
}

func (m *reteBetaRuleMemory) terminalPrefixes() []betaPrefix {
	if m == nil || len(m.prefixes) == 0 {
		return nil
	}
	return m.livePrefixes(len(m.prefixes) - 1)
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

func (m *reteBetaRuleMemory) findLivePrefixRow(conditionIndex int, token *matchToken) (betaPrefixRowID, bool) {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.prefixes) {
		return 0, false
	}
	rows := m.prefixes[conditionIndex]
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.live && matchTokenEqual(row.prefix.token, token) {
			return betaPrefixRowID(i), true
		}
	}
	return 0, false
}

func (m *reteBetaRuleMemory) livePrefixes(conditionIndex int) []betaPrefix {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.prefixes) {
		return nil
	}
	rows := m.prefixes[conditionIndex]
	scratch := m.prefixViewScratch[conditionIndex][:0]
	for _, row := range rows {
		if row.live {
			scratch = append(scratch, row.prefix)
		}
	}
	sort.SliceStable(scratch, func(i, j int) bool {
		return betaPrefixLess(scratch[i], scratch[j])
	})
	m.prefixViewScratch[conditionIndex] = scratch
	return scratch
}

func (m *reteBetaRuleMemory) prefixMatches(conditionIndex int, prefix betaPrefix) []conditionMatch {
	if m == nil || prefix.token == nil || conditionIndex <= 0 {
		return nil
	}
	size := min(conditionIndex, prefix.token.size)
	scratch := m.prefixScratch[conditionIndex]
	if cap(scratch) < size {
		scratch = make([]conditionMatch, size)
	} else {
		scratch = scratch[:size]
	}
	fillConditionMatchesFromToken(scratch, prefix.token, size)
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

func (m *reteBetaRuleMemory) findLiveConditionMatchRow(conditionIndex int, id FactID) (betaConditionMatchRowID, bool) {
	if m == nil || conditionIndex < 0 || conditionIndex >= len(m.conditionMatches) {
		return 0, false
	}
	rows := m.conditionMatches[conditionIndex]
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.live && row.match.fact.ID() == id {
			return betaConditionMatchRowID(i), true
		}
	}
	return 0, false
}

func (m *reteBetaRuleMemory) liveConditionMatches(conditionIndex int) []conditionMatch {
	rows := m.conditionMatches[conditionIndex]
	scratch := m.lookupScratch[conditionIndex][:0]
	for _, row := range rows {
		if row.live {
			scratch = append(scratch, row.match)
		}
	}
	m.lookupScratch[conditionIndex] = scratch
	return scratch
}

func collectMatchCandidatesFromPrefixes(ctx context.Context, rule compiledRule, generation Generation, prefixes []betaPrefix, scratch *candidateScratch) ([]matchCandidate, error) {
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
		if prefix.token == nil {
			continue
		}
		candidate, err := buildMatchCandidateFromTokenGenerationWithScratch(rule, generation, prefix.token, scratch)
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
	return candidates, nil
}

func countPrefixCandidateSpace(prefixes []betaPrefix) (candidateCount, entryCount, pathCount int) {
	for _, prefix := range prefixes {
		if prefix.token == nil {
			continue
		}
		candidateCount++
		entryCount += prefix.token.size
		pathCount += prefix.token.pathLen
	}
	return candidateCount, entryCount, pathCount
}

func (m *reteBetaRuleMemory) indexConditionMatch(conditionIndex int, rowID betaConditionMatchRowID, match conditionMatch) {
	plan := m.rule.conditionPlans[conditionIndex]
	if len(plan.joins) == 0 {
		return
	}
	key, ok := betaJoinKeyForFact(plan, match.fact)
	if !ok {
		return
	}
	if m.conditionIndexes[conditionIndex] == nil {
		m.conditionIndexes[conditionIndex] = make(map[betaJoinKey][]betaConditionMatchRowID)
	}
	m.conditionIndexes[conditionIndex][key] = append(m.conditionIndexes[conditionIndex][key], rowID)
}

func (m *reteBetaRuleMemory) rebuildConditionIndex(conditionIndex int) {
	if m.conditionIndexes[conditionIndex] == nil {
		m.conditionIndexes[conditionIndex] = make(map[betaJoinKey][]betaConditionMatchRowID)
	}
	resetJoinIndexBuckets(m.conditionIndexes[conditionIndex])
	for i, row := range m.conditionMatches[conditionIndex] {
		if !row.live {
			continue
		}
		m.indexConditionMatch(conditionIndex, betaConditionMatchRowID(i), row.match)
	}
	pruneEmptyJoinIndexBuckets(m.conditionIndexes[conditionIndex])
}

func (m *reteBetaRuleMemory) indexPrefix(conditionIndex int, prefixIndex betaPrefixRowID, prefix betaPrefix) {
	nextCondition := conditionIndex + 1
	if nextCondition >= len(m.rule.conditionPlans) {
		return
	}
	nextPlan := m.rule.conditionPlans[nextCondition]
	if len(nextPlan.joins) == 0 {
		return
	}
	key, ok := betaJoinKeyForPrefix(nextPlan, m.prefixMatches(nextCondition, prefix))
	if !ok {
		return
	}
	if m.prefixIndexes[conditionIndex] == nil {
		m.prefixIndexes[conditionIndex] = make(map[betaJoinKey][]betaPrefixRowID)
	}
	m.prefixIndexes[conditionIndex][key] = append(m.prefixIndexes[conditionIndex][key], prefixIndex)
}

func (m *reteBetaRuleMemory) rebuildPrefixIndex(conditionIndex int) {
	if m.prefixIndexes[conditionIndex] == nil {
		m.prefixIndexes[conditionIndex] = make(map[betaJoinKey][]betaPrefixRowID)
	}
	resetJoinIndexBuckets(m.prefixIndexes[conditionIndex])
	for i, row := range m.prefixes[conditionIndex] {
		if !row.live {
			continue
		}
		m.indexPrefix(conditionIndex, betaPrefixRowID(i), row.prefix)
	}
	pruneEmptyJoinIndexBuckets(m.prefixIndexes[conditionIndex])
}

func betaConditionMatch(plan compiledConditionPlan, fact FactSnapshot) (conditionMatch, bool, error) {
	if !plan.matchesFact(fact) {
		return conditionMatch{}, false, nil
	}
	ok, err := plan.matchesConstraints(nil, fact)
	if err != nil || !ok {
		return conditionMatch{}, false, err
	}
	return conditionMatch{
		conditionID: plan.id,
		bindingSlot: plan.bindingSlot,
		fact:        fact,
	}, true, nil
}

func betaConditionMatchWorking(plan compiledConditionPlan, fact *workingFact, snapshot FactSnapshot) (conditionMatch, bool, error) {
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
		fact:        snapshot,
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
	if left.token == nil || right.token == nil {
		return left.token == nil && right.token != nil
	}
	if left.token.size != right.token.size {
		return left.token.size < right.token.size
	}
	return compareMatchToken(left.token, right.token) < 0
}

func betaPrefixEqual(left, right betaPrefix) bool {
	return matchTokenEqual(left.token, right.token)
}

func compareMatchToken(left, right *matchToken) int {
	if left == nil || right == nil {
		switch {
		case left == nil && right != nil:
			return -1
		case left != nil && right == nil:
			return 1
		default:
			return 0
		}
	}
	if left.parent != nil || right.parent != nil {
		if cmp := compareMatchToken(left.parent, right.parent); cmp != 0 {
			return cmp
		}
	}
	if left.match.fact.ID() != right.match.fact.ID() {
		if factIDLess(left.match.fact.ID(), right.match.fact.ID()) {
			return -1
		}
		return 1
	}
	if left.match.fact.Version() != right.match.fact.Version() {
		if left.match.fact.Version() < right.match.fact.Version() {
			return -1
		}
		return 1
	}
	return 0
}

func betaPrefixContainsFact(prefix betaPrefix, id FactID) bool {
	for token := prefix.token; token != nil; token = token.parent {
		if token.match.fact.ID() == id {
			return true
		}
	}
	return false
}

func betaJoinKeyForPrefixToken(plan compiledConditionPlan, token *matchToken) (betaJoinKey, bool) {
	return betaJoinKeyForPlan(plan, func(join compiledJoinConstraint) (Value, bool) {
		match, ok := matchTokenAtSlot(token, join.refBindingSlot)
		if !ok {
			return Value{}, false
		}
		return match.fact.compiledFieldValue(join.refField, join.refFieldSlot)
	})
}

func matchTokenAtSlot(token *matchToken, slot int) (conditionMatch, bool) {
	if token == nil || slot < 0 {
		return conditionMatch{}, false
	}
	for current := token; current != nil; current = current.parent {
		if current.size == slot+1 {
			return current.match, true
		}
		if current.size <= slot {
			return conditionMatch{}, false
		}
	}
	return conditionMatch{}, false
}

func fillConditionMatchesFromToken(out []conditionMatch, token *matchToken, limit int) int {
	if token == nil || limit <= 0 {
		return 0
	}
	written := fillConditionMatchesFromToken(out, token.parent, limit)
	if written >= limit {
		return written
	}
	out[written] = token.match
	return written + 1
}

func resetJoinIndexBuckets[T any](index map[betaJoinKey][]T) {
	for key, bucket := range index {
		index[key] = bucket[:0]
	}
}

func pruneEmptyJoinIndexBuckets[T any](index map[betaJoinKey][]T) {
	for key, bucket := range index {
		if len(bucket) == 0 {
			delete(index, key)
		}
	}
}

func betaJoinKeyForFact(plan compiledConditionPlan, fact FactSnapshot) (betaJoinKey, bool) {
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
