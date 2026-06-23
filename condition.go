package gess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type conditionTargetKind uint8

const (
	conditionTargetUnknown conditionTargetKind = iota
	conditionTargetName
	conditionTargetTemplateKey
)

type conditionIndexKind uint8

const (
	conditionIndexUnknown conditionIndexKind = iota
	conditionIndexName
	conditionIndexTemplateKey
)

type conditionTarget struct {
	kind        conditionTargetKind
	name        string
	templateKey TemplateKey
}

type compiledConditionPlan struct {
	id          ConditionID
	binding     string
	bindingSlot int
	path        []int
	negated     bool
	aggregate   *compiledAggregatePlan
	target      conditionTarget
	constraints []compiledFieldConstraint
	joins       []compiledJoinConstraint
	predicates  []compiledExpressionPredicate
	indexable   bool
	indexKind   conditionIndexKind
}

type conditionMatch struct {
	conditionID ConditionID
	bindingSlot int
	fact        conditionFactRef
	value       Value
	hasValue    bool
}

type bindingSet struct {
	matches []conditionMatch
	token   *matchToken
}

func isValidBindingName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case i == 0 && (r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z')):
		case i > 0 && (r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9')):
		default:
			return false
		}
	}
	return true
}

func conditionIDFor(ruleID RuleID, order int, binding string, name string, templateKey TemplateKey, constraints []FieldConstraint, joins []JoinConstraint, predicates []compiledExpressionPredicate, negated bool) ConditionID {
	sum := sha256.New()
	sum.Write([]byte("gess/condition/v1\n"))
	sum.Write([]byte("rule:"))
	sum.Write([]byte(ruleID.String()))
	sum.Write([]byte("\norder:"))
	sum.Write(fmt.Appendf(nil, "%d", order))
	sum.Write([]byte("\nbinding:"))
	sum.Write([]byte(binding))
	if negated {
		sum.Write([]byte("\nnegated:true"))
	}
	sum.Write([]byte("\nname:"))
	sum.Write([]byte(name))
	sum.Write([]byte("\ntemplate-key:"))
	sum.Write([]byte(templateKey.String()))
	sum.Write([]byte("\nconstraints:"))
	for _, constraint := range constraints {
		sum.Write([]byte(constraint.Field))
		sum.Write([]byte(":"))
		sum.Write([]byte(string(constraint.Operator)))
		sum.Write([]byte(":"))
		sum.Write([]byte(constraint.Value.String()))
		sum.Write([]byte(";"))
	}
	sum.Write([]byte("\njoins:"))
	for _, join := range joins {
		sum.Write([]byte(join.Field))
		sum.Write([]byte(":"))
		sum.Write([]byte(string(join.Operator)))
		sum.Write([]byte(":"))
		sum.Write([]byte(join.Ref.Binding))
		sum.Write([]byte("."))
		sum.Write([]byte(join.Ref.Field))
		sum.Write([]byte(";"))
	}
	if len(predicates) > 0 {
		sum.Write([]byte("\npredicates:"))
		sum.Write([]byte(serializeCompiledExpressionPredicates(predicates)))
	}
	return ConditionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}

func aggregateConditionIDFor(ruleID RuleID, order int, specs []compiledAggregateSpec, input []compiledConditionPlan) ConditionID {
	sum := sha256.New()
	sum.Write([]byte("gess/aggregate-condition/v1\n"))
	sum.Write([]byte("rule:"))
	sum.Write([]byte(ruleID.String()))
	sum.Write([]byte("\norder:"))
	sum.Write(fmt.Appendf(nil, "%d", order))
	for _, spec := range specs {
		sum.Write([]byte("\nspec:"))
		sum.Write([]byte(spec.binding))
		sum.Write([]byte(":"))
		sum.Write([]byte(spec.kind))
		if spec.hasExpr {
			sum.Write([]byte(":"))
			sum.Write([]byte(serializeCompiledExpression(spec.expression)))
		}
	}
	for _, plan := range input {
		sum.Write([]byte("\ninput:"))
		sum.Write([]byte(plan.id.String()))
	}
	return ConditionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
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

	facts, ok := source.factsForTarget(p.target)
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
		ok, err := predicate.matchesWithParams(fact, bindings, params)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
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
		return fact != nil && fact.name == p.target.name
	case conditionTargetTemplateKey:
		return fact != nil && fact.templateKey == p.target.templateKey
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
		if !constraint.matchesWorking(fact) {
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
