package gess

import (
	"context"
	"strconv"
)

type reteRuntime struct {
	revision *Ruleset
	plan     reteNetworkPlan
	oracle   matcher
}

type reteNetworkPlan struct {
	rules       []reteRulePlan
	unsupported []reteUnsupportedReason
	stats       retePlanStats
}

type reteRulePlan struct {
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	salience         int
	declarationOrder int
	conditions       []reteConditionPlan
	terminal         reteTerminalPlan
	supported        bool
}

type reteConditionPlan struct {
	conditionID ConditionID
	binding     string
	bindingSlot int
	path        []int
	target      conditionTarget
	alpha       reteAlphaPlan
	beta        []reteBetaPlan
	supported   bool
}

type reteAlphaPlan struct {
	id             reteNodeID
	ruleRevisionID RuleRevisionID
	conditionID    ConditionID
	target         conditionTarget
	indexKind      conditionIndexKind
	constraints    int
}

type reteBetaPlan struct {
	id             reteNodeID
	ruleRevisionID RuleRevisionID
	conditionID    ConditionID
	path           []int
	bindingSlot    int
	refBindingSlot int
	indexKind      joinIndexKind
}

type reteTerminalPlan struct {
	id             reteNodeID
	ruleID         RuleID
	ruleRevisionID RuleRevisionID
	conditions     int
}

type reteUnsupportedReason struct {
	ruleID         RuleID
	ruleRevisionID RuleRevisionID
	conditionID    ConditionID
	binding        string
	kind           reteUnsupportedKind
	detail         string
}

type reteUnsupportedKind string

const (
	reteUnsupportedUnknownTarget reteUnsupportedKind = "unknown-target"
	reteUnsupportedNameTarget    reteUnsupportedKind = "name-target"
	reteUnsupportedOpenTemplate  reteUnsupportedKind = "open-template"
	reteUnsupportedMissingTarget reteUnsupportedKind = "missing-target"
	reteUnsupportedUnindexedJoin reteUnsupportedKind = "unindexed-join"
)

type retePlanStats struct {
	rules                 int
	conditions            int
	alphaNodes            int
	betaNodes             int
	terminalNodes         int
	unsupportedRules      int
	unsupportedConditions int
}

type reteRuntimeMetrics struct {
	plan  retePlanStats
	nodes []reteNodeMetrics
}

type reteNodeMetrics struct {
	id             reteNodeID
	kind           reteNodeKind
	ruleRevisionID RuleRevisionID
	conditionID    ConditionID
	facts          int
	tokens         int
}

type reteNodeID string

type reteNodeKind uint8

const (
	reteNodeAlpha reteNodeKind = iota + 1
	reteNodeBeta
	reteNodeTerminal
)

func newReteRuntime(revision *Ruleset) (*reteRuntime, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}
	return &reteRuntime{
		revision: revision,
		plan:     planReteNetwork(revision),
		oracle:   newNaiveMatcher(revision),
	}, nil
}

func (r *reteRuntime) match(ctx context.Context, snapshot Snapshot) ([]ruleMatchResult, error) {
	if r == nil || r.revision == nil || r.oracle == nil {
		return nil, ErrInvalidRuleset
	}
	return r.oracle.match(ctx, snapshot)
}

func (r *reteRuntime) metrics() reteRuntimeMetrics {
	if r == nil {
		return reteRuntimeMetrics{}
	}
	metrics := reteRuntimeMetrics{
		plan:  r.plan.stats,
		nodes: make([]reteNodeMetrics, 0, r.plan.stats.alphaNodes+r.plan.stats.betaNodes+r.plan.stats.terminalNodes),
	}
	for _, rule := range r.plan.rules {
		for _, condition := range rule.conditions {
			metrics.nodes = append(metrics.nodes, reteNodeMetrics{
				id:             condition.alpha.id,
				kind:           reteNodeAlpha,
				ruleRevisionID: condition.alpha.ruleRevisionID,
				conditionID:    condition.alpha.conditionID,
			})
			for _, beta := range condition.beta {
				metrics.nodes = append(metrics.nodes, reteNodeMetrics{
					id:             beta.id,
					kind:           reteNodeBeta,
					ruleRevisionID: beta.ruleRevisionID,
					conditionID:    beta.conditionID,
				})
			}
		}
		metrics.nodes = append(metrics.nodes, reteNodeMetrics{
			id:             rule.terminal.id,
			kind:           reteNodeTerminal,
			ruleRevisionID: rule.terminal.ruleRevisionID,
		})
	}
	return metrics
}

func planReteNetwork(revision *Ruleset) reteNetworkPlan {
	if revision == nil {
		return reteNetworkPlan{}
	}

	plan := reteNetworkPlan{
		rules: make([]reteRulePlan, 0, len(revision.ruleOrder)),
	}
	for _, ruleName := range revision.ruleOrder {
		rule, ok := revision.rules[ruleName]
		if !ok {
			continue
		}

		rulePlan := reteRulePlan{
			ruleID:           rule.id,
			ruleRevisionID:   rule.revisionID,
			salience:         rule.salience,
			declarationOrder: rule.declarationOrder,
			conditions:       make([]reteConditionPlan, 0, len(rule.conditionPlans)),
			terminal: reteTerminalPlan{
				id:             reteTerminalNodeID(rule.revisionID),
				ruleID:         rule.id,
				ruleRevisionID: rule.revisionID,
				conditions:     len(rule.conditionPlans),
			},
			supported: true,
		}

		for _, condition := range rule.conditionPlans {
			conditionPlan, unsupported := planReteCondition(revision, rule, condition)
			if len(unsupported) > 0 {
				rulePlan.supported = false
				plan.unsupported = append(plan.unsupported, unsupported...)
			}
			rulePlan.conditions = append(rulePlan.conditions, conditionPlan)
		}

		plan.stats.rules++
		plan.stats.conditions += len(rulePlan.conditions)
		plan.stats.alphaNodes += len(rulePlan.conditions)
		for _, condition := range rulePlan.conditions {
			plan.stats.betaNodes += len(condition.beta)
			if !condition.supported {
				plan.stats.unsupportedConditions++
			}
		}
		plan.stats.terminalNodes++
		if !rulePlan.supported {
			plan.stats.unsupportedRules++
		}
		plan.rules = append(plan.rules, rulePlan)
	}

	return plan
}

func planReteCondition(revision *Ruleset, rule compiledRule, condition compiledConditionPlan) (reteConditionPlan, []reteUnsupportedReason) {
	conditionPlan := reteConditionPlan{
		conditionID: condition.id,
		binding:     condition.binding,
		bindingSlot: condition.bindingSlot,
		path:        cloneIntPath(condition.path),
		target:      condition.target,
		alpha: reteAlphaPlan{
			id:             reteAlphaNodeID(rule.revisionID, condition.id),
			ruleRevisionID: rule.revisionID,
			conditionID:    condition.id,
			target:         condition.target,
			indexKind:      condition.indexKind,
			constraints:    len(condition.constraints),
		},
		beta:      make([]reteBetaPlan, 0, len(condition.joins)),
		supported: true,
	}

	var unsupported []reteUnsupportedReason
	addUnsupported := func(kind reteUnsupportedKind, detail string) {
		conditionPlan.supported = false
		unsupported = append(unsupported, reteUnsupportedReason{
			ruleID:         rule.id,
			ruleRevisionID: rule.revisionID,
			conditionID:    condition.id,
			binding:        condition.binding,
			kind:           kind,
			detail:         detail,
		})
	}

	switch condition.target.kind {
	case conditionTargetTemplateKey:
		template, ok := revision.templateByKey(condition.target.templateKey)
		if !ok {
			addUnsupported(reteUnsupportedMissingTarget, "template target is not present in the compiled revision")
		} else if !template.closed {
			addUnsupported(reteUnsupportedOpenTemplate, "open-template fields are map-shaped and not planned as fixed slots yet")
		}
	case conditionTargetName:
		addUnsupported(reteUnsupportedNameTarget, "name targets may match dynamic facts, so shape is left on the semantic fallback")
	default:
		addUnsupported(reteUnsupportedUnknownTarget, "condition target cannot be planned")
	}

	for i, join := range condition.joins {
		conditionPlan.beta = append(conditionPlan.beta, reteBetaPlan{
			id:             reteBetaNodeID(rule.revisionID, condition.id, i),
			ruleRevisionID: rule.revisionID,
			conditionID:    condition.id,
			path:           cloneIntPath(join.path),
			bindingSlot:    join.bindingSlot,
			refBindingSlot: join.refBindingSlot,
			indexKind:      join.indexKind,
		})
		if !join.indexable {
			addUnsupported(reteUnsupportedUnindexedJoin, "join is not indexable by the current planner")
		}
	}

	return conditionPlan, unsupported
}

func reteAlphaNodeID(ruleRevisionID RuleRevisionID, conditionID ConditionID) reteNodeID {
	return reteNodeID("alpha:" + ruleRevisionID.String() + ":" + conditionID.String())
}

func reteBetaNodeID(ruleRevisionID RuleRevisionID, conditionID ConditionID, joinIndex int) reteNodeID {
	return reteNodeID("beta:" + ruleRevisionID.String() + ":" + conditionID.String() + ":" + strconv.Itoa(joinIndex))
}

func reteTerminalNodeID(ruleRevisionID RuleRevisionID) reteNodeID {
	return reteNodeID("terminal:" + ruleRevisionID.String())
}
