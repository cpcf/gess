package gess

import (
	"context"
	"fmt"
	"math"
	"strings"
)

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
	token          *matchToken
}

type reteBetaRuleMemory struct {
	rule             compiledRule
	conditionMatches [][]conditionMatch
	conditionIndexes []map[betaJoinKey][]int
	prefixes         [][]betaPrefix
	prefixIndexes    []map[betaJoinKey][]int
	tokenBacking     [][]matchToken
	lookupScratch    [][]conditionMatch
	prefixScratch    [][]conditionMatch
	candidateScratch candidateScratch
}

type betaPrefix struct {
	token *matchToken
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

func newReteBetaRuleMemory(rule compiledRule) *reteBetaRuleMemory {
	conditions := len(rule.conditionPlans)
	return &reteBetaRuleMemory{
		rule:             rule,
		conditionMatches: make([][]conditionMatch, conditions),
		conditionIndexes: make([]map[betaJoinKey][]int, conditions),
		prefixes:         make([][]betaPrefix, conditions),
		prefixIndexes:    make([]map[betaJoinKey][]int, conditions),
		tokenBacking:     make([][]matchToken, 0, conditions),
		lookupScratch:    make([][]conditionMatch, conditions),
		prefixScratch:    make([][]conditionMatch, conditions),
	}
}

func (m *reteBetaRuleMemory) resetFacts(facts []FactSnapshot) {
	if m == nil {
		return
	}
	defer m.trimTokenBacking()
	m.clear()
	for conditionIndex, plan := range m.rule.conditionPlans {
		matches := m.conditionMatches[conditionIndex][:0]
		for _, fact := range facts {
			match, ok, err := betaConditionMatch(plan, fact)
			if err != nil || !ok {
				continue
			}
			matches = append(matches, match)
		}
		m.conditionMatches[conditionIndex] = matches
		m.rebuildConditionIndex(conditionIndex)
	}
	for conditionIndex, matches := range m.conditionMatches {
		if len(matches) == 0 {
			return
		}
		prefixes := m.prefixes[conditionIndex][:0]
		if conditionIndex == 0 {
			plan := m.rule.conditionPlans[conditionIndex]
			for _, match := range matches {
				prefixes = append(prefixes, betaPrefix{
					token: m.newMatchToken(nil, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
				})
			}
		} else {
			var err error
			prefixes, err = m.joinExistingPrefixes(conditionIndex, prefixes)
			if err != nil {
				return
			}
		}
		if len(prefixes) == 0 {
			return
		}
		m.prefixes[conditionIndex] = prefixes
		m.rebuildPrefixIndex(conditionIndex)
	}
}

func (m *reteBetaRuleMemory) clear() {
	if m == nil {
		return
	}
	for conditionIndex := range m.conditionMatches {
		for i := range m.conditionMatches[conditionIndex] {
			m.conditionMatches[conditionIndex][i] = conditionMatch{}
		}
		m.conditionMatches[conditionIndex] = m.conditionMatches[conditionIndex][:0]
		resetJoinIndexBuckets(m.conditionIndexes[conditionIndex])

		for i := range m.prefixes[conditionIndex] {
			m.prefixes[conditionIndex][i] = betaPrefix{}
		}
		m.prefixes[conditionIndex] = m.prefixes[conditionIndex][:0]
		resetJoinIndexBuckets(m.prefixIndexes[conditionIndex])
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
	for _, prefixes := range m.prefixes {
		for _, prefix := range prefixes {
			liveCount += countLiveTokens(prefix.token, seen)
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
		prefixes := m.prefixes[conditionIndex]
		for i := range prefixes {
			prefixes[i].token = clone(prefixes[i].token)
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
	for _, prefixes := range m.prefixes {
		count += len(prefixes)
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
		if span != nil {
			span.recordConditionPlanTested()
		}
		match, ok, err := betaConditionMatch(plan, fact)
		if err != nil || !ok {
			continue
		}
		if m.addConditionMatch(conditionIndex, match) && span != nil {
			span.recordConditionMatchAdded()
		}
		out = m.appendRightMatchDeltas(out, ruleRevisionID, conditionIndex, match, span)
	}
	return out
}

func (m *reteBetaRuleMemory) joinExistingPrefixes(conditionIndex int, prefixes []betaPrefix) ([]betaPrefix, error) {
	plan := m.rule.conditionPlans[conditionIndex]
	out := prefixes[:0]
	for _, prefix := range m.prefixes[conditionIndex-1] {
		prefixMatches := m.prefixMatches(conditionIndex, prefix)
		matches, err := m.matchesForLeftPrefix(conditionIndex, prefix)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			ok, err := plan.matchesJoins(nil, match.fact, prefixMatches)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = append(out, betaPrefix{
				token: m.newMatchToken(prefix.token, plan.bindingTupleEntry(match), match, match.fact.Recency(), match.fact.Generation(), nil),
			})
		}
	}
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
		for _, prefix := range m.prefixes[conditionIndex-1] {
			prefixMatches := m.prefixMatches(conditionIndex, prefix)
			ok, err := plan.matchesJoins(nil, match.fact, prefixMatches)
			if err != nil || !ok {
				continue
			}
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
	prefixes := m.prefixes[conditionIndex-1]
	for _, idx := range m.prefixIndexes[conditionIndex-1][key] {
		if idx < 0 || idx >= len(prefixes) {
			continue
		}
		prefix := prefixes[idx]
		prefixMatches := m.prefixMatches(conditionIndex, prefix)
		ok, err := plan.matchesJoins(nil, match.fact, prefixMatches)
		if err != nil || !ok {
			continue
		}
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
	matches, err := m.matchesForLeftPrefix(nextCondition, prefix)
	if err != nil {
		return out
	}
	prefixMatches := m.prefixMatches(nextCondition, prefix)
	plan := m.rule.conditionPlans[nextCondition]
	for _, match := range matches {
		ok, err := plan.matchesJoins(nil, match.fact, prefixMatches)
		if err != nil || !ok {
			continue
		}
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
		return m.conditionMatches[conditionIndex], nil
	}
	key, ok := betaJoinKeyForPrefix(plan, m.prefixMatches(conditionIndex, prefix))
	if !ok {
		return nil, nil
	}
	indexes := m.conditionIndexes[conditionIndex][key]
	matches := m.lookupScratch[conditionIndex][:0]
	for _, idx := range indexes {
		if idx >= 0 && idx < len(m.conditionMatches[conditionIndex]) {
			matches = append(matches, m.conditionMatches[conditionIndex][idx])
		}
	}
	m.lookupScratch[conditionIndex] = matches
	return matches, nil
}

func (m *reteBetaRuleMemory) addConditionMatch(conditionIndex int, match conditionMatch) bool {
	matches := m.conditionMatches[conditionIndex]
	insertAt := len(matches)
	for insertAt > 0 && conditionMatchLess(match, matches[insertAt-1]) {
		insertAt--
	}
	if insertAt < len(matches) && conditionMatchEqual(matches[insertAt], match) {
		return false
	}
	if insertAt > 0 && conditionMatchEqual(matches[insertAt-1], match) {
		return false
	}
	if insertAt == len(matches) {
		m.conditionMatches[conditionIndex] = append(matches, match)
		m.indexConditionMatch(conditionIndex, insertAt, match)
		return true
	}

	matches = append(matches, conditionMatch{})
	copy(matches[insertAt+1:], matches[insertAt:])
	matches[insertAt] = match
	m.conditionMatches[conditionIndex] = matches
	m.rebuildConditionIndex(conditionIndex)
	return true
}

func (m *reteBetaRuleMemory) removeConditionMatch(conditionIndex int, id FactID) {
	matches := m.conditionMatches[conditionIndex]
	next := matches[:0]
	removed := false
	for _, match := range matches {
		if match.fact.ID() == id {
			removed = true
			continue
		}
		next = append(next, match)
	}
	if !removed {
		return
	}
	for i := len(next); i < len(matches); i++ {
		matches[i] = conditionMatch{}
	}
	m.conditionMatches[conditionIndex] = next
	m.rebuildConditionIndex(conditionIndex)
}

func (m *reteBetaRuleMemory) addPrefix(conditionIndex int, prefix betaPrefix) bool {
	prefixes := m.prefixes[conditionIndex]
	insertAt := len(prefixes)
	for insertAt > 0 && betaPrefixLess(prefix, prefixes[insertAt-1]) {
		insertAt--
	}
	if insertAt < len(prefixes) && betaPrefixEqual(prefixes[insertAt], prefix) {
		return false
	}
	if insertAt > 0 && betaPrefixEqual(prefixes[insertAt-1], prefix) {
		return false
	}
	if insertAt == len(prefixes) {
		m.prefixes[conditionIndex] = append(prefixes, prefix)
		m.indexPrefix(conditionIndex, insertAt, prefix)
		return true
	}

	prefixes = append(prefixes, betaPrefix{})
	copy(prefixes[insertAt+1:], prefixes[insertAt:])
	prefixes[insertAt] = prefix
	m.prefixes[conditionIndex] = prefixes
	m.rebuildPrefixIndex(conditionIndex)
	return true
}

func (m *reteBetaRuleMemory) removePrefixesContainingFact(conditionIndex int, id FactID) {
	prefixes := m.prefixes[conditionIndex]
	next := prefixes[:0]
	removed := false
	for _, prefix := range prefixes {
		if betaPrefixContainsFact(prefix, id) {
			removed = true
			continue
		}
		next = append(next, prefix)
	}
	if !removed {
		return
	}
	for i := len(next); i < len(prefixes); i++ {
		prefixes[i] = betaPrefix{}
	}
	m.prefixes[conditionIndex] = next
	m.rebuildPrefixIndex(conditionIndex)
}

func (m *reteBetaRuleMemory) terminalPrefixes() []betaPrefix {
	if m == nil || len(m.prefixes) == 0 {
		return nil
	}
	return m.prefixes[len(m.prefixes)-1]
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

func (m *reteBetaRuleMemory) indexConditionMatch(conditionIndex, matchIndex int, match conditionMatch) {
	plan := m.rule.conditionPlans[conditionIndex]
	if len(plan.joins) == 0 {
		return
	}
	key, ok := betaJoinKeyForFact(plan, match.fact)
	if !ok {
		return
	}
	if m.conditionIndexes[conditionIndex] == nil {
		m.conditionIndexes[conditionIndex] = make(map[betaJoinKey][]int)
	}
	m.conditionIndexes[conditionIndex][key] = append(m.conditionIndexes[conditionIndex][key], matchIndex)
}

func (m *reteBetaRuleMemory) rebuildConditionIndex(conditionIndex int) {
	if m.conditionIndexes[conditionIndex] == nil {
		m.conditionIndexes[conditionIndex] = make(map[betaJoinKey][]int)
	}
	resetJoinIndexBuckets(m.conditionIndexes[conditionIndex])
	for i, match := range m.conditionMatches[conditionIndex] {
		m.indexConditionMatch(conditionIndex, i, match)
	}
	pruneEmptyJoinIndexBuckets(m.conditionIndexes[conditionIndex])
}

func (m *reteBetaRuleMemory) indexPrefix(conditionIndex, prefixIndex int, prefix betaPrefix) {
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
		m.prefixIndexes[conditionIndex] = make(map[betaJoinKey][]int)
	}
	m.prefixIndexes[conditionIndex][key] = append(m.prefixIndexes[conditionIndex][key], prefixIndex)
}

func (m *reteBetaRuleMemory) rebuildPrefixIndex(conditionIndex int) {
	if m.prefixIndexes[conditionIndex] == nil {
		m.prefixIndexes[conditionIndex] = make(map[betaJoinKey][]int)
	}
	resetJoinIndexBuckets(m.prefixIndexes[conditionIndex])
	for i, prefix := range m.prefixes[conditionIndex] {
		m.indexPrefix(conditionIndex, i, prefix)
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
	if left.entry.factID != right.entry.factID {
		if factIDLess(left.entry.factID, right.entry.factID) {
			return -1
		}
		return 1
	}
	if left.entry.factVersion != right.entry.factVersion {
		if left.entry.factVersion < right.entry.factVersion {
			return -1
		}
		return 1
	}
	return 0
}

func betaPrefixContainsFact(prefix betaPrefix, id FactID) bool {
	for token := prefix.token; token != nil; token = token.parent {
		if token.entry.factID == id {
			return true
		}
	}
	return false
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

func resetJoinIndexBuckets(index map[betaJoinKey][]int) {
	for key, bucket := range index {
		index[key] = bucket[:0]
	}
}

func pruneEmptyJoinIndexBuckets(index map[betaJoinKey][]int) {
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
