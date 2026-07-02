package engine

import (
	"context"
	"fmt"
	"sort"
)

type matcher interface {
	match(context.Context, factSource) ([]ruleMatchResult, error)
}

type naiveMatcher struct {
	revision *Ruleset
}

func newNaiveMatcher(revision *Ruleset) matcher {
	return &naiveMatcher{revision: revision}
}

func (m *naiveMatcher) match(ctx context.Context, source factSource) ([]ruleMatchResult, error) {
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

		candidates, err := rule.matchCandidates(ctx, source)
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

type bindingSet struct {
	matches []conditionMatch
	token   *matchToken
}

type matchToken struct {
	parent        *matchToken
	match         conditionMatch
	size          int
	pathLen       int
	maxRecency    Recency
	totalRecency  Recency
	identityState uint64
}

type aggregateValueBinding struct {
	name  string
	value Value
}

func collectMatchCandidates(ctx context.Context, rule compiledRule, source factSource, bindingSets []bindingSet) ([]matchCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return nil, ErrInvalidRuleset
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
		candidate, err := buildMatchCandidate(rule, source.sourceGeneration(), set)
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

func buildMatchCandidate(rule compiledRule, generation Generation, set bindingSet) (matchCandidate, error) {
	if set.token != nil {
		return buildMatchCandidateFromTokenGeneration(rule, generation, set.token)
	}
	return buildMatchCandidateFromMatches(rule, generation, set.matches)
}

func buildMatchCandidateFromMatches(rule compiledRule, generation Generation, matches []conditionMatch) (matchCandidate, error) {
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}
	if len(matches) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: empty binding set for rule %q", ErrMatcher, rule.name)
	}

	entries := make([]bindingTupleEntry, 0, len(matches))
	maxRecency := Recency(0)
	totalRecency := Recency(0)
	for _, match := range matches {
		if match.bindingSlot < 0 || match.bindingSlot >= len(rule.conditions) {
			continue
		}
		condition := rule.conditions[match.bindingSlot]
		plan, ok := rule.conditionPlanForBindingSlot(match.bindingSlot)
		if !ok {
			return matchCandidate{}, fmt.Errorf("%w: missing condition plan for binding slot %d in rule %q", ErrMatcher, match.bindingSlot, rule.name)
		}
		entries = append(entries, bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    match.bindingSlot,
			conditionOrder: condition.order,
			conditionID:    condition.id,
			conditionPath:  plan.path,
			factID:         match.fact.ID(),
			factVersion:    match.fact.Version(),
			value:          cloneValue(match.value),
			hasValue:       match.hasValue,
		})

		if !match.hasValue {
			recency := match.fact.Recency()
			if recency > maxRecency {
				maxRecency = recency
			}
			totalRecency = addRecency(totalRecency, recency)
		}
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
	identity := candidateIdentityFor(rule.id, rule.revisionID, rule.identityScopeHash, generation, entries)

	return matchCandidate{
		ruleID:         rule.id,
		ruleRevisionID: rule.revisionID,
		identity:       identity,
		bindingTuple:   entries,
		factIDs:        factIDs,
		factVersions:   factVersions,
		generation:     generation,
		maxRecency:     maxRecency,
		totalRecency:   totalRecency,
		path:           path,
	}, nil
}

func buildMatchCandidateFromToken(rule compiledRule, generation Generation, token *matchToken) (matchCandidate, error) {
	return buildMatchCandidateFromTokenGeneration(rule, generation, token)
}

func buildMatchCandidateFromTokenGeneration(rule compiledRule, generation Generation, token *matchToken) (matchCandidate, error) {
	if token == nil {
		return matchCandidate{}, fmt.Errorf("%w: empty token for rule %q", ErrMatcher, rule.name)
	}
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return matchCandidate{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}

	entries := make([]bindingTupleEntry, token.size)
	factIDs := make([]FactID, token.size)
	factVersions := make([]FactVersion, token.size)
	path := make([]int, token.pathLen)
	entryLen, pathLen, err := fillMatchToken(rule, entries, factIDs, factVersions, path, 0, 0, token)
	if err != nil {
		return matchCandidate{}, err
	}
	entries = entries[:entryLen]
	factIDs = factIDs[:entryLen]
	factVersions = factVersions[:entryLen]
	path = path[:pathLen]

	identity := candidateIdentityFor(rule.id, rule.revisionID, rule.identityScopeHash, generation, entries)

	return matchCandidate{
		ruleID:         rule.id,
		ruleRevisionID: rule.revisionID,
		identity:       identity,
		bindingTuple:   entries,
		factIDs:        factIDs,
		factVersions:   factVersions,
		generation:     generation,
		maxRecency:     token.maxRecency,
		totalRecency:   token.totalRecency,
		path:           path,
	}, nil
}

func fillMatchToken(rule compiledRule, entries []bindingTupleEntry, factIDs []FactID, factVersions []FactVersion, path []int, entryIndex, pathIndex int, token *matchToken) (int, int, error) {
	if token == nil {
		return entryIndex, pathIndex, nil
	}
	entryIndex, pathIndex, err := fillMatchToken(rule, entries, factIDs, factVersions, path, entryIndex, pathIndex, token.parent)
	if err != nil {
		return entryIndex, pathIndex, err
	}
	if token.match.bindingSlot < 0 || token.match.bindingSlot >= len(rule.conditions) {
		return entryIndex, pathIndex, nil
	}
	entry, err := bindingTupleEntryForMatch(rule, token.match)
	if err != nil {
		return entryIndex, pathIndex, err
	}
	entries[entryIndex] = entry
	factIDs[entryIndex] = entry.factID
	factVersions[entryIndex] = entry.factVersion
	copy(path[pathIndex:], entry.conditionPath)
	return entryIndex + 1, pathIndex + len(entry.conditionPath), nil
}

func newMatchToken(parent *matchToken, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) *matchToken {
	token := makeMatchToken(parent, entry, match, recency, generation)
	return &token
}

func makeMatchToken(parent *matchToken, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) matchToken {
	token := matchToken{
		parent: parent,
		match:  match,
	}
	if parent == nil {
		token.size = 1
		token.pathLen = len(entry.conditionPath)
		token.maxRecency = recency
		token.totalRecency = recency
		token.identityState = candidateIdentityHashStart(generation)
	} else {
		token.size = parent.size + 1
		token.pathLen = parent.pathLen + len(entry.conditionPath)
		token.maxRecency = max(recency, parent.maxRecency)
		token.totalRecency = addRecency(parent.totalRecency, recency)
		token.identityState = parent.identityState
	}
	token.identityState = candidateIdentityHashStep(token.identityState, entry)
	return token
}

func matchTokenEqual(left, right *matchToken) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	if left.size != right.size || left.identityState != right.identityState {
		return false
	}
	for left != nil && right != nil {
		if left.match.hasValue != right.match.hasValue {
			return false
		}
		if left.match.hasValue {
			if !left.match.value.Equal(right.match.value) {
				return false
			}
		} else if left.match.fact.ID() != right.match.fact.ID() || left.match.fact.Version() != right.match.fact.Version() {
			return false
		}
		left = left.parent
		right = right.parent
	}
	return left == nil && right == nil
}

func (p compiledConditionPlan) bindingTupleEntry(match conditionMatch) bindingTupleEntry {
	return bindingTupleEntry{
		binding:        p.binding,
		bindingSlot:    match.bindingSlot,
		conditionOrder: p.bindingSlot,
		conditionID:    p.id,
		conditionPath:  p.path,
		factID:         match.fact.ID(),
		factVersion:    match.fact.Version(),
		value:          cloneValue(match.value),
		hasValue:       match.hasValue,
	}
}

func bindingTupleEntryForMatchUnchecked(rule compiledRule, plan compiledConditionPlan, match conditionMatch) bindingTupleEntry {
	condition := RuleCondition{}
	if match.bindingSlot >= 0 && match.bindingSlot < len(rule.conditions) {
		condition = rule.conditions[match.bindingSlot]
	}
	return bindingTupleEntry{
		binding:        condition.binding,
		bindingSlot:    match.bindingSlot,
		conditionOrder: condition.order,
		conditionID:    plan.id,
		conditionPath:  plan.path,
		factID:         match.fact.ID(),
		factVersion:    match.fact.Version(),
		value:          cloneValue(match.value),
		hasValue:       match.hasValue,
	}
}

func (p compiledConditionPlan) scan(ctx context.Context, source factSource) ([]conditionMatch, error) {
	return p.scanWithBindings(ctx, source, nil)
}

func (p compiledConditionPlan) scanWithBindings(ctx context.Context, source factSource, bindings []conditionMatch) ([]conditionMatch, error) {
	return p.scanWithBindingsAndParams(ctx, source, bindings, nil)
}

func (p compiledConditionPlan) scanWithBindingsAndParams(ctx context.Context, source factSource, bindings []conditionMatch, params map[string]Value) ([]conditionMatch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.target.kind == conditionTargetUnknown {
		return nil, nil
	}

	matches := make([]conditionMatch, 0)
	err := p.forEachMatchWithBindingsAndParams(ctx, source, bindings, params, func(match conditionMatch) error {
		matches = append(matches, match)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (p compiledConditionPlan) forEachMatchWithBindings(ctx context.Context, source factSource, bindings []conditionMatch, yield func(conditionMatch) error) error {
	return p.forEachMatchWithBindingsAndParams(ctx, source, bindings, nil, yield)
}

func (p compiledConditionPlan) forEachMatchWithBindingsAndParams(ctx context.Context, source factSource, bindings []conditionMatch, params map[string]Value, yield func(conditionMatch) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.aggregate != nil {
		return p.forEachAggregateMatch(ctx, source, bindings, yield)
	}
	if p.target.kind == conditionTargetUnknown {
		return nil
	}

	facts, ok := p.factsForTarget(source)
	if !ok {
		return nil
	}
	for _, fact := range facts {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := newConditionFactRefFromSnapshot(fact)
		ok, err := p.matchesConstraints(ctx, ref)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ok, err = p.matchesJoins(ctx, ref, bindings)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ok, err = p.matchesListPatterns(ctx, ref)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ok, err = p.matchesPredicatesWithParams(ctx, ref, bindings, params)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := yield(conditionMatch{
			conditionID: p.id,
			bindingSlot: p.bindingSlot,
			fact:        ref,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p compiledConditionPlan) factsForTarget(source factSource) ([]FactSnapshot, bool) {
	recorder, _ := source.(alphaIndexCounterRecorder)
	if indexed, ok := source.(indexedFactSource); ok {
		fieldSlot, value, ok := p.literalEqualityFieldIndex()
		if ok {
			facts, ok := indexed.factsForTargetFieldEqual(p.target, fieldSlot, value)
			if ok {
				if recorder != nil {
					recorder.recordAlphaIndexProbe(len(facts) > 0)
				}
				return facts, true
			}
		}
	}
	if recorder != nil {
		recorder.recordAlphaIndexFallbackScan()
	}
	return source.factsForTarget(p.target)
}

func (p compiledConditionPlan) literalEqualityFieldIndex() (int, reteGraphAlphaRouteValue, bool) {
	for _, constraint := range p.constraints {
		if constraint.operator != FieldConstraintOpEqual || constraint.access.rootSlot < 0 || !constraint.access.topLevel() {
			continue
		}
		value, ok := reteGraphAlphaRouteValueFromValue(constraint.value)
		if !ok {
			continue
		}
		return constraint.access.rootSlot, value, true
	}
	return 0, reteGraphAlphaRouteValue{}, false
}

func (p compiledConditionPlan) forEachAlphaMatchWithBindings(ctx context.Context, facts []FactSnapshot, bindings []conditionMatch, yield func(conditionMatch) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.target.kind == conditionTargetUnknown {
		return nil
	}

	for _, fact := range facts {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := newConditionFactRefFromSnapshot(fact)
		ok, err := p.matchesJoins(ctx, ref, bindings)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ok, err = p.matchesListPatterns(ctx, ref)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ok, err = p.matchesPredicates(ctx, ref, bindings)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := yield(conditionMatch{
			conditionID: p.id,
			bindingSlot: p.bindingSlot,
			fact:        ref,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p compiledConditionPlan) matchesJoins(ctx context.Context, fact conditionFactRef, bindings []conditionMatch) (bool, error) {
	for _, join := range p.joins {
		ok, err := join.matches(fact, bindings)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (p compiledConditionPlan) matchesPredicates(ctx context.Context, fact conditionFactRef, bindings []conditionMatch) (bool, error) {
	return p.matchesPredicatesWithParams(ctx, fact, bindings, nil)
}

func (p compiledConditionPlan) matchesPredicatesWithParams(ctx context.Context, fact conditionFactRef, bindings []conditionMatch, params map[string]Value) (bool, error) {
	for _, predicate := range p.predicates {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		ok, err := predicate.matchesWithContextParams(ctx, fact, bindings, params)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (p compiledConditionPlan) matchesTestBindings(ctx context.Context, bindings []conditionMatch) (bool, error) {
	for _, predicate := range p.testPredicates {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		ok, err := predicate.matchesWithContextParams(ctx, conditionFactRef{}, bindings, nil)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func (p compiledConditionPlan) matchesListPatterns(ctx context.Context, fact conditionFactRef) (bool, error) {
	if len(p.listPatterns) == 0 {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for _, pattern := range p.listPatterns {
		_, ok, err := pattern.matchesFact(fact, tokenRef{})
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func (p compiledConditionPlan) listPatternCaptureMatches(fact conditionFactRef) ([]conditionMatch, bool, error) {
	if len(p.listPatterns) == 0 {
		return nil, true, nil
	}
	var out []conditionMatch
	for _, pattern := range p.listPatterns {
		captures, ok, err := pattern.matchesFact(fact, tokenRef{})
		if err != nil || !ok {
			return nil, ok, err
		}
		for _, capture := range captures {
			out = append(out, conditionMatch{
				conditionID: p.id,
				bindingSlot: capture.bindingSlot,
				value:       cloneValue(capture.value),
				hasValue:    true,
			})
		}
	}
	return out, true, nil
}

func (p compiledConditionPlan) matchesFact(fact conditionFactRef) bool {
	switch p.target.kind {
	case conditionTargetName:
		return fact.name == p.target.name
	case conditionTargetTemplateKey:
		return fact.templateKey == p.target.templateKey
	default:
		return false
	}
}

func (p compiledConditionPlan) matchesFactWorking(fact *workingFact) bool {
	switch p.target.kind {
	case conditionTargetName:
		return fact != nil && fact.storedName() == p.target.name
	case conditionTargetTemplateKey:
		return fact != nil && fact.matchesTemplateTarget(p.target)
	default:
		return false
	}
}

func (p compiledConditionPlan) matchesConstraints(ctx context.Context, fact conditionFactRef) (bool, error) {
	for _, constraint := range p.constraints {
		if !constraint.matches(fact) {
			return false, nil
		}
	}
	return true, nil
}

func (p compiledConditionPlan) matchesConstraintsWorking(ctx context.Context, fact *workingFact) (bool, error) {
	for _, constraint := range p.constraints {
		if !constraint.matchesWorking(fact, nil) {
			return false, nil
		}
	}
	return true, nil
}

func (r compiledRule) scanCondition(ctx context.Context, source factSource, conditionIndex int) ([]conditionMatch, error) {
	if conditionIndex < 0 || conditionIndex >= len(r.conditionPlans) {
		return nil, nil
	}
	return r.conditionPlans[conditionIndex].scan(ctx, source)
}

func (r compiledRule) matchBindingSets(ctx context.Context, source factSource) ([]bindingSet, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(r.conditionPlans) == 0 {
		return nil, nil
	}

	var sets []bindingSet
	var walk func(conditionIndex int, selected []conditionMatch, token *matchToken) error
	walk = func(conditionIndex int, selected []conditionMatch, token *matchToken) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if conditionIndex == len(r.conditionPlans) {
			matches := compactSelectedConditionMatches(selected)
			sets = append(sets, bindingSet{matches: matches, token: token})
			return nil
		}

		plan := r.conditionPlans[conditionIndex]
		if plan.isTest {
			ok, err := plan.matchesTestBindings(ctx, selected)
			if err != nil || !ok {
				return err
			}
			return walk(conditionIndex+1, selected, token)
		}
		if plan.aggregate != nil {
			bindings, ok, err := plan.aggregate.evaluate(ctx, source, selected)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			next := make([]conditionMatch, len(selected)+len(bindings))
			copy(next, selected)
			nextToken := token
			for i, binding := range bindings {
				match := conditionMatch{
					conditionID: plan.id,
					bindingSlot: plan.bindingSlot + i,
					value:       binding.value,
					hasValue:    true,
				}
				next = selectedConditionMatchesWithMatch(next, match)
				entry := bindingTupleEntryForMatchUnchecked(r, plan, match)
				nextToken = newMatchToken(nextToken, entry, match, 0, source.sourceGeneration())
			}
			return walk(conditionIndex+1, next, nextToken)
		}

		matches, err := plan.scanWithBindings(ctx, source, selected)
		if err != nil {
			return err
		}
		for _, match := range matches {
			next := selectedConditionMatchesWithMatch(selected, match)
			entry := plan.bindingTupleEntry(match)
			nextToken := newMatchToken(token, entry, match, match.fact.Recency(), source.sourceGeneration())
			captures, ok, err := plan.listPatternCaptureMatches(match.fact)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			for _, capture := range captures {
				next = selectedConditionMatchesWithMatch(next, capture)
				captureEntry := bindingTupleEntryForMatchUnchecked(r, plan, capture)
				nextToken = newMatchToken(nextToken, captureEntry, capture, 0, source.sourceGeneration())
			}
			if err := walk(conditionIndex+1, next, nextToken); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(0, nil, nil); err != nil {
		return nil, err
	}
	return sets, nil
}

func (r compiledRule) matchCandidates(ctx context.Context, source factSource) ([]matchCandidate, error) {
	return r.matchCandidatesWithAlpha(ctx, source, nil)
}

func (r compiledRule) matchCandidatesWithAlpha(ctx context.Context, source factSource, alphaSource alphaFactSource) ([]matchCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil {
		return nil, ErrInvalidRuleset
	}
	if len(r.conditionPlans) == 0 {
		return nil, nil
	}

	candidates := make([]matchCandidate, 0)
	seen := newCandidateSeenSet(0)

	for _, branch := range r.executionConditionBranches() {
		plans := branch.plans
		if len(plans) == 0 {
			continue
		}
		var walk func(conditionIndex int, selected []conditionMatch) error
		walk = func(conditionIndex int, selected []conditionMatch) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if conditionIndex == len(plans) {
				candidate, err := buildMatchCandidateFromMatches(r, source.sourceGeneration(), compactSelectedConditionMatches(selected))
				if err != nil {
					return err
				}
				if seen.seen(candidates, candidate) {
					return nil
				}
				candidates = append(candidates, candidate)
				return nil
			}

			plan := plans[conditionIndex]
			if plan.isTest {
				ok, err := plan.matchesTestBindings(ctx, selected)
				if err != nil || !ok {
					return err
				}
				return walk(conditionIndex+1, selected)
			}
			if plan.aggregate != nil {
				bindings, ok, err := plan.aggregate.evaluate(ctx, source, selected)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				next := make([]conditionMatch, len(selected)+len(bindings))
				copy(next, selected)
				for i, binding := range bindings {
					next = selectedConditionMatchesWithMatch(next, conditionMatch{
						conditionID: plan.id,
						bindingSlot: plan.bindingSlot + i,
						value:       binding.value,
						hasValue:    true,
					})
				}
				return walk(conditionIndex+1, next)
			}
			yield := func(match conditionMatch) error {
				next := selectedConditionMatchesWithMatch(selected, match)
				captures, ok, err := plan.listPatternCaptureMatches(match.fact)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				for _, capture := range captures {
					next = selectedConditionMatchesWithMatch(next, capture)
				}
				return walk(conditionIndex+1, next)
			}
			if alphaSource != nil {
				if facts, ok := alphaSource.factsForCondition(plan.id); ok {
					return plan.forEachAlphaMatchWithBindings(ctx, facts, selected, yield)
				}
			}
			return plan.forEachMatchWithBindings(ctx, source, selected, yield)
		}

		if err := walk(0, nil); err != nil {
			return nil, err
		}
	}
	sortMatchCandidates(nil, candidates)
	return candidates, nil
}

func selectedConditionMatchesWithMatch(selected []conditionMatch, match conditionMatch) []conditionMatch {
	if match.bindingSlot < 0 {
		next := make([]conditionMatch, len(selected)+1)
		copy(next, selected)
		next[len(selected)] = match
		return next
	}
	if match.bindingSlot < len(selected) {
		next := make([]conditionMatch, len(selected))
		copy(next, selected)
		next[match.bindingSlot] = match
		return next
	}
	next := make([]conditionMatch, match.bindingSlot+1)
	copy(next, selected)
	next[match.bindingSlot] = match
	return next
}

func compactSelectedConditionMatches(selected []conditionMatch) []conditionMatch {
	if len(selected) == 0 {
		return nil
	}
	out := make([]conditionMatch, 0, len(selected))
	for _, match := range selected {
		if match.conditionID == "" && !match.hasValue && match.fact.ID().IsZero() {
			continue
		}
		out = append(out, match)
	}
	return out
}

func (p compiledConditionPlan) forEachAggregateMatch(ctx context.Context, source factSource, outer []conditionMatch, yield func(conditionMatch) error) error {
	if p.aggregate == nil {
		return fmt.Errorf("%w: missing aggregate plan", ErrAggregateEvaluation)
	}
	bindings, ok, err := p.aggregate.evaluate(ctx, source, outer)
	if err != nil || !ok {
		return err
	}
	for i, binding := range bindings {
		if err := yield(conditionMatch{
			conditionID: p.id,
			bindingSlot: p.bindingSlot + i,
			value:       binding.value,
			hasValue:    true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p compiledAggregatePlan) evaluate(ctx context.Context, source factSource, outer []conditionMatch) ([]aggregateValueBinding, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	states := make([]aggregateState, len(p.specs))
	for i, spec := range p.specs {
		states[i] = aggregateState{spec: spec}
	}
	var walk func(int, []conditionMatch) error
	walk = func(index int, selected []conditionMatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index == len(p.inputPlans) {
			var current conditionFactRef
			if len(selected) > len(outer) {
				current = selected[len(selected)-1].fact
			}
			for i := range states {
				if err := states[i].add(ctx, current, selected); err != nil {
					return err
				}
			}
			return nil
		}
		plan := p.inputPlans[index]
		if plan.negated {
			matched := false
			positive := plan
			positive.negated = false
			if err := positive.forEachMatchWithBindings(ctx, source, selected, func(conditionMatch) error {
				matched = true
				return nil
			}); err != nil {
				return err
			}
			if matched {
				return nil
			}
			return walk(index+1, append(selected, conditionMatch{
				conditionID: plan.id,
				bindingSlot: plan.bindingSlot,
			}))
		}
		return plan.forEachMatchWithBindings(ctx, source, selected, func(match conditionMatch) error {
			return walk(index+1, append(selected, match))
		})
	}
	selected := make([]conditionMatch, len(outer), len(outer)+len(p.inputPlans))
	copy(selected, outer)
	if err := walk(0, selected); err != nil {
		return nil, false, err
	}

	out := make([]aggregateValueBinding, 0, len(states))
	for i := range states {
		value, ok, err := states[i].result()
		if err != nil || !ok {
			return nil, false, err
		}
		out = append(out, aggregateValueBinding{name: states[i].spec.binding, value: value})
	}
	return out, true, nil
}

type aggregateState struct {
	spec       compiledAggregateSpec
	count      int64
	intSum     int64
	floatSum   float64
	floaty     bool
	minMax     Value
	haveMinMax bool
	values     []Value
}

func (s *aggregateState) add(ctx context.Context, current conditionFactRef, bindings []conditionMatch) error {
	s.count++
	if s.spec.kind == AggregateCount || s.spec.kind == aggregateExists || s.spec.kind == aggregateForall {
		return nil
	}
	value, ok, err := s.spec.expression.evaluateWithContextParamsAndCounters(ctx, current, bindings, nil, &FunctionEvaluationError{
		ConditionIndex: -1,
		PredicateIndex: -1,
	}, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAggregateEvaluation, err)
	}
	if !ok {
		return fmt.Errorf("%w: missing aggregate input value", ErrAggregateEvaluation)
	}
	switch s.spec.kind {
	case AggregateSum:
		return s.addSum(value)
	case AggregateMin:
		return s.addMinMax(value, true)
	case AggregateMax:
		return s.addMinMax(value, false)
	case AggregateCollect:
		s.values = append(s.values, cloneValue(value))
		return nil
	default:
		return fmt.Errorf("%w: unsupported aggregate kind %q", ErrAggregateEvaluation, s.spec.kind)
	}
}

func (s *aggregateState) addSum(value Value) error {
	switch value.Kind() {
	case ValueInt:
		if s.floaty {
			s.floatSum += float64(value.intValue)
			return nil
		}
		next, overflow := safeAddInt64(s.intSum, value.intValue)
		if overflow {
			return fmt.Errorf("%w: integer sum overflow", ErrAggregateEvaluation)
		}
		s.intSum = next
	case ValueFloat:
		s.floaty = true
		s.floatSum += float64(s.intSum) + value.floatValue
		s.intSum = 0
	default:
		return fmt.Errorf("%w: sum input must be numeric", ErrAggregateEvaluation)
	}
	return nil
}

func (s *aggregateState) addMinMax(value Value, min bool) error {
	if !s.haveMinMax {
		s.minMax = cloneValue(value)
		s.haveMinMax = true
		return nil
	}
	comparison, ok := compareValues(value, s.minMax)
	if !ok {
		return fmt.Errorf("%w: min/max input is not comparable", ErrAggregateEvaluation)
	}
	if (min && comparison < 0) || (!min && comparison > 0) {
		s.minMax = cloneValue(value)
	}
	return nil
}

func (s aggregateState) result() (Value, bool, error) {
	switch s.spec.kind {
	case AggregateCount:
		return newIntValue(s.count), true, nil
	case aggregateExists:
		if s.count == 0 {
			return Value{}, false, nil
		}
		return newBoolValue(true), true, nil
	case aggregateForall:
		if s.count != 0 {
			return Value{}, false, nil
		}
		return newBoolValue(true), true, nil
	case AggregateSum:
		if s.floaty {
			value, err := canonicalFloat(s.floatSum)
			return value, err == nil, err
		}
		return newIntValue(s.intSum), true, nil
	case AggregateMin, AggregateMax:
		if !s.haveMinMax {
			return Value{}, false, nil
		}
		return cloneValue(s.minMax), true, nil
	case AggregateCollect:
		value, err := canonicalValue(s.values)
		return value, err == nil, err
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported aggregate kind %q", ErrAggregateEvaluation, s.spec.kind)
	}
}
