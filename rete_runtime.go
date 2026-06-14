package gess

import (
	"context"
	"strconv"
)

const reteAlphaMinimumFacts = 32

type reteRuntime struct {
	revision *Ruleset
	plan     reteNetworkPlan
	oracle   matcher
	alpha    *reteAlphaMemory
	beta     *reteBetaMemory
}

type reteNetworkPlan struct {
	rules         []reteRulePlan
	unsupported   []reteUnsupportedReason
	stats         retePlanStats
	betaSupported bool
}

type reteRulePlan struct {
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	salience         int
	declarationOrder int
	conditions       []reteConditionPlan
	terminal         reteTerminalPlan
	supported        bool
	betaSupported    bool
}

type reteConditionPlan struct {
	conditionID   ConditionID
	binding       string
	bindingSlot   int
	path          []int
	target        conditionTarget
	constraints   []compiledFieldConstraint
	alpha         reteAlphaPlan
	beta          []reteBetaPlan
	supported     bool
	betaSupported bool
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
	if r.plan.betaSupported && r.beta != nil {
		return r.beta.match(ctx, snapshot, r.alpha)
	}
	if len(r.plan.unsupported) > 0 || r.alpha == nil {
		return r.oracle.match(ctx, snapshot)
	}
	return (&alphaMatcher{revision: r.revision, source: r.alpha}).match(ctx, snapshot)
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
				facts:          r.alphaFactCount(condition.conditionID),
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

func (r *reteRuntime) resetAlpha(facts []FactSnapshot) {
	if r == nil {
		return
	}
	if len(facts) < reteAlphaMinimumFacts {
		r.clearMemories()
	} else {
		alpha := newReteAlphaMemory(r.plan)
		alpha.reset(r.plan, facts)
		r.alpha = alpha
	}
	r.rebuildBeta(facts)
}

func (r *reteRuntime) clearMemories() {
	if r == nil {
		return
	}
	r.alpha = nil
	r.beta = nil
}

func (r *reteRuntime) rebuildBeta(facts []FactSnapshot) {
	if r == nil {
		return
	}
	if !r.plan.betaSupported || len(facts) < reteAlphaMinimumFacts {
		r.beta = nil
		return
	}
	r.beta = newReteBetaMemory(r.revision, r.plan, facts)
}

func (r *reteRuntime) insertBetaFact(fact FactSnapshot) {
	if r == nil || r.beta == nil {
		return
	}
	r.beta.insertFact(fact)
}

func (r *reteRuntime) removeBetaFact(id FactID) {
	if r == nil || r.beta == nil {
		return
	}
	r.beta.removeFact(id)
}

func (r *reteRuntime) updateBetaFact(before, after FactSnapshot) {
	if r == nil || r.beta == nil {
		return
	}
	r.beta.updateFact(before, after)
}

func (r *reteRuntime) insertAlphaFact(fact FactSnapshot) {
	if r == nil || r.alpha == nil {
		return
	}
	r.alpha.insert(r.plan, fact)
}

func (r *reteRuntime) removeAlphaFact(id FactID) {
	if r == nil || r.alpha == nil {
		return
	}
	r.alpha.remove(id)
}

func (r *reteRuntime) updateAlphaFact(before, after FactSnapshot) {
	if r == nil || r.alpha == nil {
		return
	}
	r.alpha.update(r.plan, before, after)
}

func (r *reteRuntime) alphaFactCount(conditionID ConditionID) int {
	if r == nil || r.alpha == nil {
		return 0
	}
	return r.alpha.factCount(conditionID)
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
			supported:     true,
			betaSupported: true,
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
			if !condition.betaSupported {
				rulePlan.betaSupported = false
			}
		}
		plan.stats.terminalNodes++
		if !rulePlan.supported {
			plan.stats.unsupportedRules++
		}
		if rulePlan.betaSupported {
			plan.betaSupported = true
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
		constraints: condition.constraints,
		alpha: reteAlphaPlan{
			id:             reteAlphaNodeID(rule.revisionID, condition.id),
			ruleRevisionID: rule.revisionID,
			conditionID:    condition.id,
			target:         condition.target,
			indexKind:      condition.indexKind,
			constraints:    len(condition.constraints),
		},
		beta:          make([]reteBetaPlan, 0, len(condition.joins)),
		supported:     true,
		betaSupported: true,
	}

	var unsupported []reteUnsupportedReason
	addUnsupported := func(kind reteUnsupportedKind, detail string) {
		conditionPlan.supported = false
		conditionPlan.betaSupported = false
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
		if join.indexKind != joinIndexEquality {
			conditionPlan.betaSupported = false
		}
		if !join.indexable {
			addUnsupported(reteUnsupportedUnindexedJoin, "join is not indexable by the current planner")
		}
	}

	return conditionPlan, unsupported
}

type alphaMatcher struct {
	revision *Ruleset
	source   alphaFactSource
}

type alphaFactSource interface {
	factsForCondition(ConditionID) ([]FactSnapshot, bool)
}

func (m *alphaMatcher) match(ctx context.Context, snapshot Snapshot) ([]ruleMatchResult, error) {
	if m == nil || m.revision == nil || m.source == nil {
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
			return nil, ErrMatcher
		}

		candidates, err := rule.matchCandidatesWithAlpha(ctx, snapshot, m.source)
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

type reteAlphaMemory struct {
	conditions map[ConditionID]*reteAlphaConditionMemory
}

type reteAlphaConditionMemory struct {
	facts   []FactSnapshot
	indexes map[FactID]int
}

func newReteAlphaMemory(plan reteNetworkPlan) *reteAlphaMemory {
	memory := &reteAlphaMemory{
		conditions: make(map[ConditionID]*reteAlphaConditionMemory, plan.stats.conditions),
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		memory.conditions[condition.conditionID] = &reteAlphaConditionMemory{
			indexes: make(map[FactID]int),
		}
	})
	return memory
}

func (m *reteAlphaMemory) reset(plan reteNetworkPlan, facts []FactSnapshot) {
	if m == nil {
		return
	}
	for _, fact := range facts {
		m.insert(plan, fact)
	}
}

func (m *reteAlphaMemory) insert(plan reteNetworkPlan, fact FactSnapshot) {
	if m == nil {
		return
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		if !condition.matchesAlpha(fact) {
			return
		}
		conditionMemory := m.conditions[condition.conditionID]
		if conditionMemory == nil {
			return
		}
		conditionMemory.upsert(fact)
	})
}

func (m *reteAlphaMemory) update(plan reteNetworkPlan, before, after FactSnapshot) {
	if m == nil {
		return
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		conditionMemory := m.conditions[condition.conditionID]
		if conditionMemory == nil {
			return
		}
		matchedBefore := conditionMemory.contains(before.id)
		matchesAfter := condition.matchesAlpha(after)
		switch {
		case matchedBefore && matchesAfter:
			conditionMemory.upsert(after)
		case matchedBefore:
			conditionMemory.remove(before.id)
		case matchesAfter:
			conditionMemory.upsert(after)
		}
	})
}

func (m *reteAlphaMemory) remove(id FactID) {
	if m == nil {
		return
	}
	for _, conditionMemory := range m.conditions {
		if conditionMemory != nil {
			conditionMemory.remove(id)
		}
	}
}

func (m *reteAlphaMemory) factsForCondition(conditionID ConditionID) ([]FactSnapshot, bool) {
	if m == nil {
		return nil, false
	}
	conditionMemory, ok := m.conditions[conditionID]
	if !ok {
		return nil, false
	}
	return conditionMemory.facts, true
}

func (m *reteAlphaMemory) factCount(conditionID ConditionID) int {
	if m == nil {
		return 0
	}
	conditionMemory := m.conditions[conditionID]
	if conditionMemory == nil {
		return 0
	}
	return len(conditionMemory.facts)
}

func (m *reteAlphaConditionMemory) contains(id FactID) bool {
	if m == nil || m.indexes == nil {
		return false
	}
	_, ok := m.indexes[id]
	return ok
}

func (m *reteAlphaConditionMemory) upsert(fact FactSnapshot) bool {
	if m == nil {
		return false
	}
	if m.indexes == nil {
		m.indexes = make(map[FactID]int)
	}
	if idx, ok := m.indexes[fact.id]; ok {
		m.facts[idx] = fact
		return false
	}
	idx := len(m.facts)
	for idx > 0 && factIDLess(fact.id, m.facts[idx-1].id) {
		idx--
	}
	m.facts = append(m.facts, FactSnapshot{})
	copy(m.facts[idx+1:], m.facts[idx:])
	m.facts[idx] = fact
	m.reindexFrom(idx)
	return true
}

func (m *reteAlphaConditionMemory) remove(id FactID) {
	if m == nil || m.indexes == nil {
		return
	}
	idx, ok := m.indexes[id]
	if !ok {
		return
	}
	copy(m.facts[idx:], m.facts[idx+1:])
	m.facts[len(m.facts)-1] = FactSnapshot{}
	m.facts = m.facts[:len(m.facts)-1]
	delete(m.indexes, id)
	m.reindexFrom(idx)
}

func (m *reteAlphaConditionMemory) reindexFrom(start int) {
	if m == nil {
		return
	}
	for i := start; i < len(m.facts); i++ {
		m.indexes[m.facts[i].id] = i
	}
}

func (p reteNetworkPlan) forEachSupportedCondition(yield func(reteConditionPlan)) {
	for _, rule := range p.rules {
		for _, condition := range rule.conditions {
			if condition.supported {
				yield(condition)
			}
		}
	}
}

func (p reteConditionPlan) matchesAlpha(fact FactSnapshot) bool {
	if !p.supported {
		return false
	}
	switch p.target.kind {
	case conditionTargetTemplateKey:
		if fact.TemplateKey() != p.target.templateKey {
			return false
		}
	case conditionTargetName:
		if fact.Name() != p.target.name {
			return false
		}
	default:
		return false
	}
	for _, constraint := range p.constraints {
		if !constraint.matches(fact) {
			return false
		}
	}
	return true
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
