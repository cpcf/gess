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
	bindingTuple     []bindingTupleEntry
	factIDs          []FactID
	factVersions     []FactVersion
	generation       Generation
	maxRecency       Recency
	aggregateRecency Recency
	path             []int
	key              string
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

		bindingSets, err := rule.matchBindingSets(ctx, snapshot)
		if err != nil {
			return nil, err
		}

		candidates, err := collectMatchCandidates(ctx, rule, snapshot, bindingSets)
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
	seen := make(map[string]struct{}, len(bindingSets))
	for _, set := range bindingSets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := buildMatchCandidate(rule, snapshot, set)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[candidate.key]; ok {
			continue
		}
		seen[candidate.key] = struct{}{}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func buildMatchCandidate(rule compiledRule, snapshot Snapshot, set bindingSet) (matchCandidate, error) {
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}
	if len(set.matches) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: empty binding set for rule %q", ErrMatcher, rule.name)
	}

	entries := make([]bindingTupleEntry, len(set.matches))
	maxRecency := Recency(0)
	aggregateRecency := Recency(0)
	for i, match := range set.matches {
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
	key := candidateKeyFor(rule.id, rule.revisionID, snapshot.Generation(), entries)

	return matchCandidate{
		ruleID:           rule.id,
		ruleRevisionID:   rule.revisionID,
		bindingTuple:     entries,
		factIDs:          factIDs,
		factVersions:     factVersions,
		generation:       snapshot.Generation(),
		maxRecency:       maxRecency,
		aggregateRecency: aggregateRecency,
		path:             path,
		key:              key,
	}, nil
}

func candidateKeyFor(ruleID RuleID, revisionID RuleRevisionID, generation Generation, bindings []bindingTupleEntry) string {
	orderedBindings := bindings
	if !bindingTupleEntriesSorted(bindings) {
		orderedBindings = append([]bindingTupleEntry(nil), bindings...)
		sort.SliceStable(orderedBindings, func(i, j int) bool {
			return bindingTupleEntryLess(orderedBindings[i], orderedBindings[j])
		})
	}

	var b strings.Builder
	b.Grow(128 + len(orderedBindings)*64)
	b.WriteString("gess/match-candidate/v2|rule=")
	b.WriteString(ruleID.String())
	b.WriteString("|revision=")
	b.WriteString(revisionID.String())
	b.WriteString("|generation=")
	b.WriteString(strconv.FormatUint(uint64(generation), 10))
	b.WriteString("|bindings=")
	for _, entry := range orderedBindings {
		b.WriteString(entry.binding)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(entry.bindingSlot))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(entry.conditionOrder))
		b.WriteByte(':')
		b.WriteString(entry.conditionID.String())
		b.WriteByte(':')
		for i, segment := range entry.conditionPath {
			if i > 0 {
				b.WriteByte('/')
			}
			b.WriteString(strconv.Itoa(segment))
		}
		b.WriteByte(':')
		b.WriteString(entry.factID.String())
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(uint64(entry.factVersion), 10))
		b.WriteByte(';')
	}

	return b.String()
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
		return left.factID.String() < right.factID.String()
	}
	return left.factVersion < right.factVersion
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
