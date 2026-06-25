package gess

import (
	"context"
	"fmt"
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
