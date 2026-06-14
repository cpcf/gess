package gess

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type matcher interface {
	match(context.Context, Snapshot) ([]ruleMatchResult, error)
}

type naiveMatcher struct {
	revision *Ruleset
}

type ruleMatchResult struct {
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	salience         int
	declarationOrder int
	candidates       []matchCandidate
}

type matchCandidate struct {
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	identity         candidateIdentity
	bindingTuple     []bindingTupleEntry
	factIDs          []FactID
	factVersions     []FactVersion
	generation       Generation
	maxRecency       Recency
	aggregateRecency Recency
	path             []int
}

type bindingTupleEntry struct {
	binding        string
	bindingSlot    int
	conditionOrder int
	conditionID    ConditionID
	conditionPath  []int
	factID         FactID
	factVersion    FactVersion
}

type candidateIdentity struct {
	generation Generation
	count      int
	key        candidateIdentityKey
}

type candidateIdentityKey struct {
	scopeHash uint64
	hash      uint64
}

type candidateSeenSet struct {
	first    map[candidateIdentityKey]int
	overflow map[candidateIdentityKey][]int
}

func newCandidateSeenSet(size int) candidateSeenSet {
	return candidateSeenSet{first: make(map[candidateIdentityKey]int, size)}
}

func newNaiveMatcher(revision *Ruleset) matcher {
	return &naiveMatcher{revision: revision}
}

func (m *naiveMatcher) match(ctx context.Context, snapshot Snapshot) ([]ruleMatchResult, error) {
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

		candidates, err := rule.matchCandidates(ctx, snapshot)
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

func collectMatchCandidates(ctx context.Context, rule compiledRule, snapshot Snapshot, bindingSets []bindingSet) ([]matchCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(bindingSets) == 0 {
		return nil, nil
	}

	candidates := make([]matchCandidate, 0, len(bindingSets))
	seen := newCandidateSeenSet(len(bindingSets))
	for _, set := range bindingSets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := buildMatchCandidate(rule, snapshot, set)
		if err != nil {
			return nil, err
		}
		if seen.seen(candidates, candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func buildMatchCandidate(rule compiledRule, snapshot Snapshot, set bindingSet) (matchCandidate, error) {
	return buildMatchCandidateFromMatches(rule, snapshot, set.matches)
}

func buildMatchCandidateFromMatches(rule compiledRule, snapshot Snapshot, matches []conditionMatch) (matchCandidate, error) {
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}
	if len(matches) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: empty binding set for rule %q", ErrMatcher, rule.name)
	}

	entries := make([]bindingTupleEntry, len(matches))
	maxRecency := Recency(0)
	aggregateRecency := Recency(0)
	for i, match := range matches {
		if match.bindingSlot < 0 || match.bindingSlot >= len(rule.conditions) || match.bindingSlot >= len(rule.conditionPlans) {
			return matchCandidate{}, fmt.Errorf("%w: malformed binding slot %d for rule %q", ErrMatcher, match.bindingSlot, rule.name)
		}
		condition := rule.conditions[match.bindingSlot]
		plan := rule.conditionPlans[match.bindingSlot]
		entries[i] = bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    match.bindingSlot,
			conditionOrder: condition.order,
			conditionID:    condition.id,
			conditionPath:  cloneIntPath(plan.path),
			factID:         match.fact.ID(),
			factVersion:    match.fact.Version(),
		}

		recency := match.fact.Recency()
		if recency > maxRecency {
			maxRecency = recency
		}
		aggregateRecency = addRecency(aggregateRecency, recency)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].conditionOrder != entries[j].conditionOrder {
			return entries[i].conditionOrder < entries[j].conditionOrder
		}
		if entries[i].bindingSlot != entries[j].bindingSlot {
			return entries[i].bindingSlot < entries[j].bindingSlot
		}
		if entries[i].binding != entries[j].binding {
			return entries[i].binding < entries[j].binding
		}
		if entries[i].conditionID != entries[j].conditionID {
			return entries[i].conditionID < entries[j].conditionID
		}
		if entries[i].factID != entries[j].factID {
			return entries[i].factID.String() < entries[j].factID.String()
		}
		return entries[i].factVersion < entries[j].factVersion
	})

	factIDs := make([]FactID, len(entries))
	factVersions := make([]FactVersion, len(entries))
	for i, entry := range entries {
		factIDs[i] = entry.factID
		factVersions[i] = entry.factVersion
	}

	path := candidatePathFor(entries)
	identity := candidateIdentityFor(rule.id, rule.revisionID, rule.identityScopeHash, snapshot.Generation(), entries)

	return matchCandidate{
		ruleID:           rule.id,
		ruleRevisionID:   rule.revisionID,
		identity:         identity,
		bindingTuple:     entries,
		factIDs:          factIDs,
		factVersions:     factVersions,
		generation:       snapshot.Generation(),
		maxRecency:       maxRecency,
		aggregateRecency: aggregateRecency,
		path:             path,
	}, nil
}

func candidateIdentityFor(ruleID RuleID, revisionID RuleRevisionID, scopeHash uint64, generation Generation, bindings []bindingTupleEntry) candidateIdentity {
	orderedBindings := bindings
	if !bindingTupleEntriesSorted(bindings) {
		orderedBindings = append([]bindingTupleEntry(nil), bindings...)
		sort.SliceStable(orderedBindings, func(i, j int) bool {
			return bindingTupleEntryLess(orderedBindings[i], orderedBindings[j])
		})
	}
	if scopeHash == 0 {
		scopeHash = candidateIdentityScopeHash(ruleID, revisionID)
	}

	hash := candidateIdentityHash(generation, orderedBindings)
	return candidateIdentity{
		generation: generation,
		count:      len(orderedBindings),
		key: candidateIdentityKey{
			scopeHash: scopeHash,
			hash:      hash,
		},
	}
}

func candidateIdentityScopeHash(ruleID RuleID, revisionID RuleRevisionID) uint64 {
	hash := uint64(1469598103934665603)
	hash = fnvMixUint64(hash, 4)
	hash = fnvMixString(hash, ruleID.String())
	hash = fnvMixString(hash, revisionID.String())
	return hash
}

func candidateIdentityHash(generation Generation, orderedBindings []bindingTupleEntry) uint64 {
	hash := uint64(1469598103934665603)
	hash = fnvMixUint64(hash, 4)
	hash = fnvMixUint64(hash, uint64(generation))
	hash = fnvMixUint64(hash, uint64(len(orderedBindings)))
	for _, entry := range orderedBindings {
		hash = fnvMixUint64(hash, uint64(entry.factID.generation))
		hash = fnvMixUint64(hash, entry.factID.sequence)
		hash = fnvMixUint64(hash, uint64(entry.factVersion))
	}

	return hash
}

func fnvMixString(hash uint64, value string) uint64 {
	hash = fnvMixUint64(hash, uint64(len(value)))
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= 1099511628211
	}
	return hash
}

func fnvMixUint64(hash uint64, value uint64) uint64 {
	for range 8 {
		hash ^= uint64(byte(value))
		hash *= 1099511628211
		value >>= 8
	}
	return hash
}

func (s *candidateSeenSet) seen(candidates []matchCandidate, candidate matchCandidate) bool {
	if s == nil {
		return false
	}
	key := candidate.identity.key
	idx, ok := s.first[key]
	if !ok {
		s.first[key] = len(candidates)
		return false
	}
	if candidateAtIndexEqual(candidates, idx, candidate) {
		return true
	}
	for _, idx := range s.overflow[key] {
		if candidateAtIndexEqual(candidates, idx, candidate) {
			return true
		}
	}
	if s.overflow == nil {
		s.overflow = make(map[candidateIdentityKey][]int)
	}
	s.overflow[key] = append(s.overflow[key], len(candidates))
	return false
}

func candidateAtIndexEqual(candidates []matchCandidate, idx int, candidate matchCandidate) bool {
	if idx < 0 || idx >= len(candidates) {
		return false
	}
	return candidateIdentityEqual(candidates[idx].identity, candidates[idx].factIDs, candidates[idx].factVersions, candidate.identity, candidate.factIDs, candidate.factVersions)
}

func candidateSeen(candidates []matchCandidate, indexes []int, candidate matchCandidate) bool {
	for _, idx := range indexes {
		if idx < 0 || idx >= len(candidates) {
			continue
		}
		if candidateIdentityEqual(candidates[idx].identity, candidates[idx].factIDs, candidates[idx].factVersions, candidate.identity, candidate.factIDs, candidate.factVersions) {
			return true
		}
	}
	return false
}

func candidateIdentityEqual(left candidateIdentity, leftFactIDs []FactID, leftFactVersions []FactVersion, right candidateIdentity, rightFactIDs []FactID, rightFactVersions []FactVersion) bool {
	if left.key != right.key {
		return false
	}
	if left.generation != right.generation || left.count != right.count {
		return false
	}
	if len(leftFactIDs) != len(rightFactIDs) || len(leftFactVersions) != len(rightFactVersions) || len(leftFactIDs) != len(leftFactVersions) {
		return false
	}
	for i := range leftFactIDs {
		if leftFactIDs[i] != rightFactIDs[i] || leftFactVersions[i] != rightFactVersions[i] {
			return false
		}
	}
	return true
}

func (i candidateIdentity) isZero() bool {
	return i.key == candidateIdentityKey{}
}

func bindingTupleEntriesSorted(entries []bindingTupleEntry) bool {
	for i := 1; i < len(entries); i++ {
		if bindingTupleEntryLess(entries[i], entries[i-1]) {
			return false
		}
	}
	return true
}

func bindingTupleEntryLess(left, right bindingTupleEntry) bool {
	if left.conditionOrder != right.conditionOrder {
		return left.conditionOrder < right.conditionOrder
	}
	if left.bindingSlot != right.bindingSlot {
		return left.bindingSlot < right.bindingSlot
	}
	if left.binding != right.binding {
		return left.binding < right.binding
	}
	if left.conditionID != right.conditionID {
		return left.conditionID < right.conditionID
	}
	if left.factID != right.factID {
		return factIDLess(left.factID, right.factID)
	}
	return left.factVersion < right.factVersion
}

func factIDLess(left, right FactID) bool {
	if left.generation != right.generation {
		return left.generation < right.generation
	}
	return left.sequence < right.sequence
}

func writeUintToBuilder(b *strings.Builder, value uint64) {
	var buf [20]byte
	b.Write(strconv.AppendUint(buf[:0], value, 10))
}

func candidatePathFor(entries []bindingTupleEntry) []int {
	var out []int
	for _, entry := range entries {
		out = append(out, entry.conditionPath...)
	}
	return out
}

func cloneIntPath(path []int) []int {
	if len(path) == 0 {
		return nil
	}
	out := make([]int, len(path))
	copy(out, path)
	return out
}

func addRecency(total Recency, next Recency) Recency {
	sum := uint64(total) + uint64(next)
	if sum < uint64(total) {
		return Recency(^uint64(0))
	}
	return Recency(sum)
}
