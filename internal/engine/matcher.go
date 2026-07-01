package engine

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/cpcf/gess/internal/fnvhash"
)

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
	value          Value
	hasValue       bool
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

type candidateScratch struct {
	candidates          []matchCandidate
	seen                candidateSeenSet
	bindingTupleEntries []bindingTupleEntry
	factIDs             []FactID
	factVersions        []FactVersion
	path                []int
}

func newCandidateSeenSet(size int) candidateSeenSet {
	return candidateSeenSet{first: make(map[candidateIdentityKey]int, size)}
}

func (s *candidateScratch) reset(candidateCount, entryCount, pathCount int) {
	if s == nil {
		return
	}
	s.candidates = resetCandidateBuffer(s.candidates, candidateCount)
	s.bindingTupleEntries = resetCandidateBuffer(s.bindingTupleEntries, entryCount)
	s.factIDs = resetCandidateBuffer(s.factIDs, entryCount)
	s.factVersions = resetCandidateBuffer(s.factVersions, entryCount)
	s.path = resetCandidateBuffer(s.path, pathCount)
	s.seen.reset(candidateCount)
}

func (s *candidateScratch) tokenBuffers(size, pathLen int) ([]bindingTupleEntry, []FactID, []FactVersion, []int) {
	if s == nil {
		return make([]bindingTupleEntry, size), make([]FactID, size), make([]FactVersion, size), make([]int, pathLen)
	}

	entryStart := len(s.bindingTupleEntries)
	s.bindingTupleEntries = s.bindingTupleEntries[:entryStart+size]
	factIDStart := len(s.factIDs)
	s.factIDs = s.factIDs[:factIDStart+size]
	factVersionStart := len(s.factVersions)
	s.factVersions = s.factVersions[:factVersionStart+size]
	pathStart := len(s.path)
	s.path = s.path[:pathStart+pathLen]

	return s.bindingTupleEntries[entryStart : entryStart+size],
		s.factIDs[factIDStart : factIDStart+size],
		s.factVersions[factVersionStart : factVersionStart+size],
		s.path[pathStart : pathStart+pathLen]
}

func (s *candidateSeenSet) reset(candidateCount int) {
	if s == nil {
		return
	}
	if s.first == nil {
		s.first = make(map[candidateIdentityKey]int, candidateCount)
	} else {
		clear(s.first)
	}
	if s.overflow == nil {
		if candidateCount > 1 {
			s.overflow = make(map[candidateIdentityKey][]int, candidateCount)
		}
		return
	}
	clear(s.overflow)
}

func buildMatchCandidateFromTokenRef(rule compiledRule, token tokenRef) (matchCandidate, error) {
	return buildMatchCandidateFromTokenRefWithScratch(rule, token.generation(), token, nil)
}

func buildMatchCandidateFromTokenRefWithScratch(rule compiledRule, generation Generation, token tokenRef, scratch *candidateScratch) (matchCandidate, error) {
	if token.isZero() {
		return matchCandidate{}, fmt.Errorf("%w: empty token for rule %q", ErrMatcher, rule.name)
	}
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}

	var entries []bindingTupleEntry
	var factIDs []FactID
	var factVersions []FactVersion
	var path []int
	size := token.size()
	pathLen := token.pathLen()
	haveEntries := false
	if publicEntries, publicFactIDs, publicFactVersions, publicPath, ok := terminalTokenBindingTuple(rule, token); ok {
		entries = publicEntries
		factIDs = publicFactIDs
		factVersions = publicFactVersions
		path = publicPath
		haveEntries = true
	} else if scratch != nil {
		entries, factIDs, factVersions, path = scratch.tokenBuffers(size, pathLen)
	} else {
		entries = make([]bindingTupleEntry, size)
		factIDs = make([]FactID, size)
		factVersions = make([]FactVersion, size)
		path = make([]int, pathLen)
	}
	if !haveEntries {
		if _, _, err := fillTokenRef(rule, entries, factIDs, factVersions, path, 0, 0, token); err != nil {
			return matchCandidate{}, err
		}
	}

	identity := candidateIdentityFor(rule.id, rule.revisionID, rule.identityScopeHash, generation, entries)

	return matchCandidate{
		ruleID:           rule.id,
		ruleRevisionID:   rule.revisionID,
		identity:         identity,
		bindingTuple:     entries,
		factIDs:          factIDs,
		factVersions:     factVersions,
		generation:       generation,
		maxRecency:       token.maxRecency(),
		aggregateRecency: token.aggregateRecency(),
		path:             path,
	}, nil
}

func fillTokenRef(rule compiledRule, entries []bindingTupleEntry, factIDs []FactID, factVersions []FactVersion, path []int, entryIndex, pathIndex int, token tokenRef) (int, int, error) {
	if token.isZero() {
		return entryIndex, pathIndex, nil
	}
	row, ok := token.resolve()
	if !ok {
		return entryIndex, pathIndex, fmt.Errorf("%w: stale token for rule %q", ErrMatcher, rule.name)
	}
	entryIndex, pathIndex, err := fillTokenRef(rule, entries, factIDs, factVersions, path, entryIndex, pathIndex, token.parent())
	if err != nil {
		return entryIndex, pathIndex, err
	}
	match, ok := row.conditionMatch()
	if !ok {
		return entryIndex, pathIndex, fmt.Errorf("%w: stale token for rule %q", ErrMatcher, rule.name)
	}
	entry, err := bindingTupleEntryForMatch(rule, match)
	if err != nil {
		return entryIndex, pathIndex, err
	}
	entries[entryIndex] = entry
	factIDs[entryIndex] = entry.factID
	factVersions[entryIndex] = entry.factVersion
	copy(path[pathIndex:], entry.conditionPath)
	return entryIndex + 1, pathIndex + len(entry.conditionPath), nil
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
	hash := fnvhash.Offset64
	hash = fnvhash.MixUint64(hash, 4)
	hash = fnvhash.MixString(hash, ruleID.String())
	hash = fnvhash.MixString(hash, revisionID.String())
	return hash
}

func candidateIdentityHash(generation Generation, orderedBindings []bindingTupleEntry) uint64 {
	hash := candidateIdentityHashStart(generation)
	for _, entry := range orderedBindings {
		hash = candidateIdentityHashStep(hash, entry)
	}
	return candidateIdentityHashFinish(hash, len(orderedBindings))
}

func candidateIdentityHashStart(generation Generation) uint64 {
	hash := fnvhash.Offset64
	hash = fnvhash.MixUint64(hash, 4)
	hash = fnvhash.MixUint64(hash, uint64(generation))
	return hash
}

func candidateIdentityHashStep(hash uint64, entry bindingTupleEntry) uint64 {
	if entry.hasValue {
		return candidateIdentityHashValueStep(hash, entry.binding, entry.value)
	}
	return candidateIdentityHashFactStep(hash, entry.factID, entry.factVersion)
}

func candidateIdentityHashFactStep(hash uint64, id FactID, version FactVersion) uint64 {
	hash = fnvhash.MixUint64(hash, 0)
	hash = fnvhash.MixUint64(hash, uint64(id.generation))
	hash = fnvhash.MixUint64(hash, id.sequence)
	hash = fnvhash.MixUint64(hash, uint64(version))
	return hash
}

func candidateIdentityHashValueStep(hash uint64, binding string, value Value) uint64 {
	hash = fnvhash.MixUint64(hash, 1)
	hash = fnvhash.MixString(hash, binding)
	hash = fnvhash.MixString(hash, value.canonicalKey())
	return hash
}

func candidateIdentityHashFinish(hash uint64, count int) uint64 {
	return fnvhash.MixUint64(hash, uint64(count))
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
	if left.factVersion != right.factVersion {
		return left.factVersion < right.factVersion
	}
	if left.hasValue != right.hasValue {
		return !left.hasValue
	}
	return left.value.canonicalKey() < right.value.canonicalKey()
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

func resetCandidateBuffer[T any](buf []T, size int) []T {
	if cap(buf) < size {
		return make([]T, 0, size)
	}
	return buf[:0]
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
	if sum > math.MaxUint32 {
		return Recency(math.MaxUint32)
	}
	return Recency(sum)
}

func bindingTupleEntryForMatch(rule compiledRule, match conditionMatch) (bindingTupleEntry, error) {
	if match.bindingSlot < 0 || match.bindingSlot >= len(rule.conditions) {
		return bindingTupleEntry{}, fmt.Errorf("%w: malformed binding slot %d for rule %q", ErrMatcher, match.bindingSlot, rule.name)
	}
	condition := rule.conditions[match.bindingSlot]
	plan, ok := rule.conditionPlanForBindingSlot(match.bindingSlot)
	if !ok {
		return bindingTupleEntry{}, fmt.Errorf("%w: missing condition plan for binding slot %d in rule %q", ErrMatcher, match.bindingSlot, rule.name)
	}
	return bindingTupleEntry{
		binding:        condition.binding,
		bindingSlot:    match.bindingSlot,
		conditionOrder: condition.order,
		conditionID:    condition.id,
		conditionPath:  plan.path,
		factID:         match.fact.ID(),
		factVersion:    match.fact.Version(),
		value:          cloneValue(match.value),
		hasValue:       match.hasValue,
	}, nil
}
