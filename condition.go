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
	target      conditionTarget
	constraints []compiledFieldConstraint
	joins       []compiledJoinConstraint
	indexable   bool
	indexKind   conditionIndexKind
}

type conditionMatch struct {
	conditionID ConditionID
	bindingSlot int
	fact        FactSnapshot
}

type bindingSet struct {
	matches []conditionMatch
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

func conditionIDFor(ruleID RuleID, order int, binding string, name string, templateKey TemplateKey, constraints []FieldConstraint, joins []JoinConstraint) ConditionID {
	sum := sha256.New()
	sum.Write([]byte("gess/condition/v1\n"))
	sum.Write([]byte("rule:"))
	sum.Write([]byte(ruleID.String()))
	sum.Write([]byte("\norder:"))
	sum.Write(fmt.Appendf(nil, "%d", order))
	sum.Write([]byte("\nbinding:"))
	sum.Write([]byte(binding))
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
	return ConditionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}

func (p compiledConditionPlan) scan(ctx context.Context, snapshot Snapshot) ([]conditionMatch, error) {
	return p.scanWithBindings(ctx, snapshot, nil)
}

func (p compiledConditionPlan) scanWithBindings(ctx context.Context, snapshot Snapshot, bindings []conditionMatch) ([]conditionMatch, error) {
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
	err := p.forEachMatchWithBindings(ctx, snapshot, bindings, func(match conditionMatch) error {
		matches = append(matches, match)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (p compiledConditionPlan) forEachMatchWithBindings(ctx context.Context, snapshot Snapshot, bindings []conditionMatch, yield func(conditionMatch) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.target.kind == conditionTargetUnknown {
		return nil
	}

	indexes := snapshot.indexesForTarget(p.target)
	for _, idx := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if idx < 0 || idx >= len(snapshot.facts) {
			continue
		}
		fact := snapshot.facts[idx]
		ok, err := p.matchesConstraints(ctx, fact)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ok, err = p.matchesJoins(ctx, fact, bindings)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := yield(conditionMatch{
			conditionID: p.id,
			bindingSlot: p.bindingSlot,
			fact:        fact,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p compiledConditionPlan) matchesJoins(ctx context.Context, fact FactSnapshot, bindings []conditionMatch) (bool, error) {
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

func (p compiledConditionPlan) matchesFact(fact FactSnapshot) bool {
	switch p.target.kind {
	case conditionTargetName:
		return fact.Name() == p.target.name
	case conditionTargetTemplateKey:
		return fact.TemplateKey() == p.target.templateKey
	default:
		return false
	}
}

func (p compiledConditionPlan) matchesConstraints(ctx context.Context, fact FactSnapshot) (bool, error) {
	for _, constraint := range p.constraints {
		if !constraint.matches(fact) {
			return false, nil
		}
	}
	return true, nil
}

func (r compiledRule) scanCondition(ctx context.Context, snapshot Snapshot, conditionIndex int) ([]conditionMatch, error) {
	if conditionIndex < 0 || conditionIndex >= len(r.conditionPlans) {
		return nil, nil
	}
	return r.conditionPlans[conditionIndex].scan(ctx, snapshot)
}

func (r compiledRule) matchBindingSets(ctx context.Context, snapshot Snapshot) ([]bindingSet, error) {
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
	var walk func(conditionIndex int, selected []conditionMatch) error
	walk = func(conditionIndex int, selected []conditionMatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if conditionIndex == len(r.conditionPlans) {
			matches := make([]conditionMatch, len(selected))
			copy(matches, selected)
			sets = append(sets, bindingSet{matches: matches})
			return nil
		}

		matches, err := r.conditionPlans[conditionIndex].scanWithBindings(ctx, snapshot, selected)
		if err != nil {
			return err
		}
		for _, match := range matches {
			next := make([]conditionMatch, len(selected)+1)
			copy(next, selected)
			next[len(selected)] = match
			if err := walk(conditionIndex+1, next); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(0, nil); err != nil {
		return nil, err
	}
	return sets, nil
}

func (r compiledRule) matchCandidates(ctx context.Context, snapshot Snapshot) ([]matchCandidate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(r.conditionPlans) == 0 {
		return nil, nil
	}

	selected := make([]conditionMatch, len(r.conditionPlans))
	candidates := make([]matchCandidate, 0)
	seen := make(map[string]struct{})

	var walk func(conditionIndex int) error
	walk = func(conditionIndex int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if conditionIndex == len(r.conditionPlans) {
			candidate, err := buildMatchCandidateFromMatches(r, snapshot, selected[:conditionIndex])
			if err != nil {
				return err
			}
			if _, ok := seen[candidate.key]; ok {
				return nil
			}
			seen[candidate.key] = struct{}{}
			candidates = append(candidates, candidate)
			return nil
		}

		return r.conditionPlans[conditionIndex].forEachMatchWithBindings(ctx, snapshot, selected[:conditionIndex], func(match conditionMatch) error {
			selected[conditionIndex] = match
			return walk(conditionIndex + 1)
		})
	}

	if err := walk(0); err != nil {
		return nil, err
	}
	return candidates, nil
}
