package gess

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type reteBetaMemory struct {
	revision *Ruleset
	rules    map[RuleRevisionID]*reteBetaRuleMemory
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
}

type betaPrefix struct {
	matches []conditionMatch
	token   *matchToken
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

func (m *reteBetaMemory) match(ctx context.Context, snapshot Snapshot, source alphaFactSource) ([]ruleMatchResult, error) {
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
			return nil, fmt.Errorf("%w: missing compiled rule %q", ErrMatcher, ruleName)
		}

		candidates, err := m.matchRuleCandidates(ctx, snapshot, rule, source)
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

func (m *reteBetaMemory) resetFacts(plan reteNetworkPlan, facts []FactSnapshot) {
	if m == nil || m.revision == nil {
		return
	}
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

func (m *reteBetaMemory) matchRuleCandidates(ctx context.Context, snapshot Snapshot, rule compiledRule, source alphaFactSource) ([]matchCandidate, error) {
	ruleMemory := m.rules[rule.revisionID]
	if ruleMemory == nil {
		return rule.matchCandidatesWithAlpha(ctx, snapshot, source)
	}
	terminal := ruleMemory.terminalPrefixes()
	bindingSets := make([]bindingSet, 0, len(terminal))
	for _, prefix := range terminal {
		bindingSets = append(bindingSets, bindingSet{token: prefix.token})
	}
	return collectMatchCandidates(ctx, rule, snapshot, bindingSets)
}

func (m *reteBetaMemory) insertFact(fact FactSnapshot) reteAgendaDelta {
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
		delta.added = appendTerminalTokenDeltas(delta.added, rule.revisionID, ruleMemory.insertFact(fact))
	}
	return delta
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
		delta.removed = appendTerminalTokenDeltas(delta.removed, rule.revisionID, ruleMemory.removeFact(id))
	}
	return delta
}

func (m *reteBetaMemory) updateFact(before, after FactSnapshot) reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	removed := m.removeFact(before.ID())
	added := m.insertFact(after)
	return reteAgendaDelta{
		supported: removed.supported && added.supported,
		added:     added.added,
		removed:   removed.removed,
	}
}

func appendTerminalTokenDeltas(out []reteTerminalTokenDelta, ruleRevisionID RuleRevisionID, tokens []*matchToken) []reteTerminalTokenDelta {
	for _, token := range tokens {
		if token == nil {
			continue
		}
		out = append(out, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          token,
		})
	}
	return out
}

func newReteBetaRuleMemory(rule compiledRule) *reteBetaRuleMemory {
	conditions := len(rule.conditionPlans)
	return &reteBetaRuleMemory{
		rule:             rule,
		conditionMatches: make([][]conditionMatch, conditions),
		conditionIndexes: make([]map[betaJoinKey][]int, conditions),
		prefixes:         make([][]betaPrefix, conditions),
		prefixIndexes:    make([]map[betaJoinKey][]int, conditions),
	}
}

func (m *reteBetaRuleMemory) resetFacts(facts []FactSnapshot) {
	if m == nil {
		return
	}
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
					matches: []conditionMatch{match},
					token:   newMatchToken(nil, plan.bindingTupleEntry(match), match.fact.Recency(), match.fact.Generation()),
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
	}
}

func (m *reteBetaRuleMemory) insertFact(fact FactSnapshot) []*matchToken {
	if m == nil {
		return nil
	}
	var added []*matchToken
	for conditionIndex, plan := range m.rule.conditionPlans {
		match, ok, err := betaConditionMatch(plan, fact)
		if err != nil || !ok {
			continue
		}
		m.addConditionMatch(conditionIndex, match)
		nextPrefixes, err := m.prefixesForRightMatch(conditionIndex, match)
		if err != nil {
			continue
		}
		for _, prefix := range nextPrefixes {
			added = append(added, terminalTokensForPrefixes(m.addAndPropagatePrefix(conditionIndex, prefix))...)
		}
	}
	return added
}

func (m *reteBetaRuleMemory) joinExistingPrefixes(conditionIndex int, prefixes []betaPrefix) ([]betaPrefix, error) {
	plan := m.rule.conditionPlans[conditionIndex]
	out := prefixes[:0]
	for _, prefix := range m.prefixes[conditionIndex-1] {
		matches, err := m.matchesForLeftPrefix(conditionIndex, prefix)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			ok, err := plan.matchesJoins(context.Background(), match.fact, prefix.matches)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = append(out, betaPrefix{
				matches: append(cloneConditionMatchSlice(prefix.matches), match),
				token:   newMatchToken(prefix.token, plan.bindingTupleEntry(match), match.fact.Recency(), match.fact.Generation()),
			})
		}
	}
	return out, nil
}

func (m *reteBetaRuleMemory) removeFact(id FactID) []*matchToken {
	if m == nil {
		return nil
	}
	removed := terminalTokensForPrefixes(m.terminalPrefixesContainingFact(id))
	for conditionIndex := range m.conditionMatches {
		m.removeConditionMatch(conditionIndex, id)
	}
	for conditionIndex := range m.prefixes {
		m.removePrefixesContainingFact(conditionIndex, id)
	}
	return removed
}

func (m *reteBetaRuleMemory) addAndPropagatePrefix(conditionIndex int, prefix betaPrefix) []betaPrefix {
	if !m.addPrefix(conditionIndex, prefix) {
		return nil
	}
	if conditionIndex == len(m.rule.conditionPlans)-1 {
		return []betaPrefix{prefix}
	}
	return m.propagatePrefix(conditionIndex, prefix)
}

func (m *reteBetaRuleMemory) propagatePrefix(conditionIndex int, prefix betaPrefix) []betaPrefix {
	nextCondition := conditionIndex + 1
	if m == nil || nextCondition >= len(m.rule.conditionPlans) {
		return nil
	}
	matches, err := m.matchesForLeftPrefix(nextCondition, prefix)
	if err != nil {
		return nil
	}
	var added []betaPrefix
	for _, match := range matches {
		ok, err := m.rule.conditionPlans[nextCondition].matchesJoins(context.Background(), match.fact, prefix.matches)
		if err != nil || !ok {
			continue
		}
		nextPrefix := betaPrefix{
			matches: append(cloneConditionMatchSlice(prefix.matches), match),
			token:   newMatchToken(prefix.token, m.rule.conditionPlans[nextCondition].bindingTupleEntry(match), match.fact.Recency(), match.fact.Generation()),
		}
		added = append(added, m.addAndPropagatePrefix(nextCondition, nextPrefix)...)
	}
	return added
}

func (m *reteBetaRuleMemory) prefixesForRightMatch(conditionIndex int, match conditionMatch) ([]betaPrefix, error) {
	plan := m.rule.conditionPlans[conditionIndex]
	if conditionIndex == 0 {
		return []betaPrefix{{
			matches: []conditionMatch{match},
			token:   newMatchToken(nil, plan.bindingTupleEntry(match), match.fact.Recency(), match.fact.Generation()),
		}}, nil
	}

	var prefixes []betaPrefix
	if len(plan.joins) == 0 {
		prefixes = m.prefixes[conditionIndex-1]
	} else {
		key, ok := betaJoinKeyForFact(plan, match.fact)
		if !ok {
			return nil, nil
		}
		for _, idx := range m.prefixIndexes[conditionIndex-1][key] {
			if idx >= 0 && idx < len(m.prefixes[conditionIndex-1]) {
				prefixes = append(prefixes, m.prefixes[conditionIndex-1][idx])
			}
		}
	}

	out := make([]betaPrefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		ok, err := plan.matchesJoins(context.Background(), match.fact, prefix.matches)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, betaPrefix{
			matches: append(cloneConditionMatchSlice(prefix.matches), match),
			token:   newMatchToken(prefix.token, plan.bindingTupleEntry(match), match.fact.Recency(), match.fact.Generation()),
		})
	}
	return out, nil
}

func (m *reteBetaRuleMemory) matchesForLeftPrefix(conditionIndex int, prefix betaPrefix) ([]conditionMatch, error) {
	plan := m.rule.conditionPlans[conditionIndex]
	if len(plan.joins) == 0 {
		return m.conditionMatches[conditionIndex], nil
	}
	key, ok := betaJoinKeyForPrefix(plan, prefix.matches)
	if !ok {
		return nil, nil
	}
	indexes := m.conditionIndexes[conditionIndex][key]
	matches := make([]conditionMatch, 0, len(indexes))
	for _, idx := range indexes {
		if idx >= 0 && idx < len(m.conditionMatches[conditionIndex]) {
			matches = append(matches, m.conditionMatches[conditionIndex][idx])
		}
	}
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

func (m *reteBetaRuleMemory) terminalPrefixesContainingFact(id FactID) []betaPrefix {
	terminal := m.terminalPrefixes()
	if len(terminal) == 0 {
		return nil
	}
	out := make([]betaPrefix, 0)
	for _, prefix := range terminal {
		if betaPrefixContainsFact(prefix, id) {
			out = append(out, prefix)
		}
	}
	return out
}

func terminalTokensForPrefixes(prefixes []betaPrefix) []*matchToken {
	if len(prefixes) == 0 {
		return nil
	}
	tokens := make([]*matchToken, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.token != nil {
			tokens = append(tokens, prefix.token)
		}
	}
	return tokens
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
	key, ok := betaJoinKeyForPrefix(nextPlan, prefix.matches)
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
	ok, err := plan.matchesConstraints(context.Background(), fact)
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
	for i := 0; i < len(left.matches) && i < len(right.matches); i++ {
		if left.matches[i].fact.ID() != right.matches[i].fact.ID() {
			return factIDLess(left.matches[i].fact.ID(), right.matches[i].fact.ID())
		}
		if left.matches[i].fact.Version() != right.matches[i].fact.Version() {
			return left.matches[i].fact.Version() < right.matches[i].fact.Version()
		}
	}
	return len(left.matches) < len(right.matches)
}

func betaPrefixEqual(left, right betaPrefix) bool {
	if len(left.matches) != len(right.matches) {
		return false
	}
	for i := range left.matches {
		if !conditionMatchEqual(left.matches[i], right.matches[i]) {
			return false
		}
	}
	return matchTokenEqual(left.token, right.token)
}

func betaPrefixContainsFact(prefix betaPrefix, id FactID) bool {
	for token := prefix.token; token != nil; token = token.parent {
		if token.entry.factID == id {
			return true
		}
	}
	return false
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

func cloneConditionMatchSlice(in []conditionMatch) []conditionMatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]conditionMatch, len(in))
	copy(out, in)
	return out
}
