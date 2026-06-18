package gess

import (
	"context"
	"strconv"
)

const reteAlphaMinimumFacts = 32

type reteRuntime struct {
	revision               *Ruleset
	plan                   reteNetworkPlan
	oracle                 matcher
	alpha                  *reteAlphaMemory
	beta                   *reteBetaMemory
	terminalRemovedScratch candidateScratch
	terminalAddedScratch   candidateScratch
}

type reteNetworkPlan struct {
	rules               []reteRulePlan
	alphaRoutes         map[TemplateKey][]reteConditionPlan
	betaRoutes          map[TemplateKey][]RuleRevisionID
	betaConditionRoutes map[TemplateKey][]reteBetaConditionRoute
	unsupported         []reteUnsupportedReason
	stats               retePlanStats
	betaSupported       bool
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

type reteBetaConditionRoute struct {
	ruleRevisionID RuleRevisionID
	conditionIndex int
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

func (r *reteRuntime) match(ctx context.Context, source factSource) ([]ruleMatchResult, error) {
	if r == nil || r.revision == nil || r.oracle == nil || source == nil {
		return nil, ErrInvalidRuleset
	}
	if r.plan.betaSupported && r.beta != nil {
		return r.beta.match(ctx, source, r.alpha)
	}
	if len(r.plan.unsupported) > 0 || r.alpha == nil {
		return r.oracle.match(ctx, source)
	}
	return (&alphaMatcher{revision: r.revision, source: r.alpha}).match(ctx, source)
}

func (r *reteRuntime) matchWithoutSnapshot(ctx context.Context, generation Generation) ([]ruleMatchResult, bool, error) {
	if r == nil || r.revision == nil || r.beta == nil {
		return nil, false, nil
	}
	return r.beta.matchWithoutSnapshot(ctx, generation)
}

func (r *reteRuntime) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	if r == nil || r.revision == nil || r.beta == nil || !r.supportsIncrementalAgenda() {
		return nil, false, nil
	}
	return r.beta.currentTerminalTokenDeltas(ctx)
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
	if r.alpha == nil {
		r.alpha = newReteAlphaMemory(r.plan)
	}
	r.alpha.reset(r.plan, facts)
	if !r.plan.betaSupported {
		r.beta = nil
		return
	}
	if r.beta == nil {
		r.beta = newReteBetaMemory(r.revision, r.plan, facts)
		return
	}
	r.beta.resetFacts(r.plan, facts)
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
	if !r.plan.betaSupported {
		r.beta = nil
		return
	}
	r.beta = newReteBetaMemory(r.revision, r.plan, facts)
}

func (r *reteRuntime) insertBetaFact(fact FactSnapshot, span *propagationCounterSpan) reteAgendaDelta {
	return r.insertBetaFactWithOrigin(fact, mutationOrigin{}, span)
}

func (r *reteRuntime) insertBetaFactGenerated(fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) reteAgendaDelta {
	if r == nil || r.beta == nil || fact == nil {
		return reteAgendaDelta{}
	}
	if r.supportsIncrementalAgenda() {
		if routes, routed := r.plan.betaConditionRoutesForTemplateKey(fact.templateKey); routed {
			if delta, ok := r.beta.insertFactForConditionRoutesGenerated(fact, routes, span); ok {
				delta.supported = delta.supported && r.supportsIncrementalAgenda()
				return delta
			}
		}
		if ruleRevisionIDs, routed := r.plan.betaRoutesForTemplateKey(fact.templateKey); routed {
			if delta, ok := r.beta.insertFactForRulesGenerated(fact, ruleRevisionIDs, span); ok {
				delta.supported = delta.supported && r.supportsIncrementalAgenda()
				return delta
			}
		}
	}
	delta := r.beta.insertFactGenerated(fact, span)
	delta.supported = delta.supported && r.supportsIncrementalAgenda()
	return delta
}

func (r *reteRuntime) insertBetaFactWithOrigin(fact FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) reteAgendaDelta {
	if r == nil || r.beta == nil {
		return reteAgendaDelta{}
	}
	if r.supportsIncrementalAgenda() {
		if routes, routed := r.plan.betaConditionRoutesForTemplateKey(fact.TemplateKey()); routed {
			if delta, ok := r.beta.insertFactForConditionRoutes(fact, routes, span); ok {
				delta.supported = delta.supported && r.supportsIncrementalAgenda()
				return delta
			}
		}
		if ruleRevisionIDs, routed := r.plan.betaRoutesForTemplateKey(fact.TemplateKey()); routed {
			if delta, ok := r.beta.insertFactForRules(fact, ruleRevisionIDs, span); ok {
				delta.supported = delta.supported && r.supportsIncrementalAgenda()
				return delta
			}
		}
	}
	delta := r.beta.insertFact(fact, span)
	delta.supported = delta.supported && r.supportsIncrementalAgenda()
	return delta
}

func (r *reteRuntime) removeBetaFact(fact FactSnapshot) reteAgendaDelta {
	if r == nil || r.beta == nil {
		return reteAgendaDelta{}
	}
	if r.supportsIncrementalAgenda() {
		if ruleRevisionIDs, routed := r.plan.betaRoutesForTemplateKey(fact.TemplateKey()); routed {
			if delta, ok := r.beta.removeFactForRules(fact.ID(), ruleRevisionIDs); ok {
				delta.supported = delta.supported && r.supportsIncrementalAgenda()
				return delta
			}
		}
	}
	delta := r.beta.removeFact(fact.ID())
	delta.supported = delta.supported && r.supportsIncrementalAgenda()
	return delta
}

func (r *reteRuntime) updateBetaFact(before, after FactSnapshot) reteAgendaDelta {
	if r == nil || r.beta == nil {
		return reteAgendaDelta{}
	}
	if r.supportsIncrementalAgenda() {
		if ruleRevisionIDs, routed := r.plan.betaRoutesForTemplateKeys(before.TemplateKey(), after.TemplateKey()); routed {
			if delta, ok := r.beta.updateFactForRules(before, after, ruleRevisionIDs); ok {
				delta.supported = delta.supported && r.supportsIncrementalAgenda()
				return delta
			}
		}
	}
	delta := r.beta.updateFact(before, after)
	delta.supported = delta.supported && r.supportsIncrementalAgenda()
	return delta
}

func (r *reteRuntime) supportsIncrementalAgenda() bool {
	if r == nil || r.beta == nil || len(r.plan.rules) == 0 {
		return false
	}
	for _, rule := range r.plan.rules {
		if !rule.supported || !rule.betaSupported {
			return false
		}
	}
	return true
}

func (r *reteRuntime) candidatesForTerminalDeltas(deltas []reteTerminalTokenDelta, scratch *candidateScratch) ([]matchCandidate, error) {
	if r == nil || r.revision == nil {
		return nil, ErrInvalidRuleset
	}
	if len(deltas) == 0 {
		return nil, nil
	}
	candidateCount, entryCount, pathCount := countTerminalDeltaCandidateSpace(deltas)
	var candidates []matchCandidate
	if scratch != nil {
		scratch.reset(candidateCount, entryCount, pathCount)
		candidates = scratch.candidates[:0]
	} else {
		candidates = make([]matchCandidate, 0, candidateCount)
	}
	var seen *candidateSeenSet
	if scratch != nil {
		seen = &scratch.seen
	} else {
		localSeen := newCandidateSeenSet(candidateCount)
		seen = &localSeen
	}
	for _, delta := range deltas {
		if delta.token == nil {
			continue
		}
		rule, ok := r.revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, ErrMatcher
		}
		candidate, err := buildMatchCandidateFromTokenGenerationWithScratch(rule, matchTokenGeneration(delta.token), delta.token, scratch)
		if err != nil {
			return nil, err
		}
		if seen.seen(candidates, candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if scratch != nil {
		scratch.candidates = candidates
	}
	return candidates, nil
}

func countTerminalDeltaCandidateSpace(deltas []reteTerminalTokenDelta) (candidateCount, entryCount, pathCount int) {
	for _, delta := range deltas {
		if delta.token == nil {
			continue
		}
		candidateCount++
		entryCount += delta.token.size
		pathCount += delta.token.pathLen
	}
	return candidateCount, entryCount, pathCount
}

func matchTokenGeneration(token *matchToken) Generation {
	for token != nil {
		if id := token.match.fact.ID(); !id.IsZero() {
			return id.Generation()
		}
		token = token.parent
	}
	return 0
}

func (r *reteRuntime) insertAlphaFact(fact FactSnapshot, span *propagationCounterSpan) {
	if r == nil || r.alpha == nil {
		return
	}
	if conditions, routed := r.plan.alphaRoutesForTemplateKey(fact.TemplateKey()); routed {
		if r.alpha.insertSelected(conditions, fact, span) {
			return
		}
	}
	r.alpha.insert(r.plan, fact, span)
}

func (r *reteRuntime) insertAlphaFactGenerated(fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) {
	if r == nil || r.alpha == nil || fact == nil {
		return
	}
	if conditions, routed := r.plan.alphaRoutesForTemplateKey(fact.templateKey); routed {
		if r.alpha.insertSelectedGenerated(conditions, fact, snapshot, span) {
			return
		}
	}
	r.alpha.insertGenerated(r.plan, fact, snapshot, span)
}

func (r *reteRuntime) removeAlphaFact(fact FactSnapshot) {
	if r == nil || r.alpha == nil {
		return
	}
	if conditions, routed := r.plan.alphaRoutesForTemplateKey(fact.TemplateKey()); routed {
		if r.alpha.removeSelected(conditions, fact.ID()) {
			return
		}
	}
	r.alpha.remove(fact.ID())
}

func (r *reteRuntime) updateAlphaFact(before, after FactSnapshot) {
	if r == nil || r.alpha == nil {
		return
	}
	if before.TemplateKey() == after.TemplateKey() {
		if conditions, routed := r.plan.alphaRoutesForTemplateKey(after.TemplateKey()); routed {
			if r.alpha.updateSelected(conditions, before, after) {
				return
			}
		}
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
		rules:               make([]reteRulePlan, 0, len(revision.ruleOrder)),
		alphaRoutes:         make(map[TemplateKey][]reteConditionPlan),
		betaRoutes:          make(map[TemplateKey][]RuleRevisionID),
		betaConditionRoutes: make(map[TemplateKey][]reteBetaConditionRoute),
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
		ruleRouteKeys := make(map[TemplateKey]struct{})

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
		for conditionIndex, condition := range rulePlan.conditions {
			plan.stats.betaNodes += len(condition.beta)
			if !condition.supported {
				plan.stats.unsupportedConditions++
			}
			if !condition.betaSupported {
				rulePlan.betaSupported = false
			}
			if condition.supported && condition.target.kind == conditionTargetTemplateKey {
				templateKey := condition.target.templateKey
				plan.alphaRoutes[templateKey] = append(plan.alphaRoutes[templateKey], condition)
				plan.betaConditionRoutes[templateKey] = append(plan.betaConditionRoutes[templateKey], reteBetaConditionRoute{
					ruleRevisionID: rule.revisionID,
					conditionIndex: conditionIndex,
				})
				if _, ok := ruleRouteKeys[templateKey]; !ok {
					plan.betaRoutes[templateKey] = append(plan.betaRoutes[templateKey], rule.revisionID)
					ruleRouteKeys[templateKey] = struct{}{}
				}
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

func (p reteNetworkPlan) alphaRoutesForTemplateKey(templateKey TemplateKey) ([]reteConditionPlan, bool) {
	if p.alphaRoutes == nil {
		return nil, false
	}
	return p.alphaRoutes[templateKey], true
}

func (p reteNetworkPlan) betaRoutesForTemplateKey(templateKey TemplateKey) ([]RuleRevisionID, bool) {
	if p.betaRoutes == nil {
		return nil, false
	}
	return p.betaRoutes[templateKey], true
}

func (p reteNetworkPlan) betaConditionRoutesForTemplateKey(templateKey TemplateKey) ([]reteBetaConditionRoute, bool) {
	if p.betaConditionRoutes == nil {
		return nil, false
	}
	return p.betaConditionRoutes[templateKey], true
}

func (p reteNetworkPlan) betaRoutesForTemplateKeys(templateKeys ...TemplateKey) ([]RuleRevisionID, bool) {
	if p.betaRoutes == nil {
		return nil, false
	}
	if len(templateKeys) == 0 {
		return nil, true
	}
	if len(templateKeys) == 1 || sameTemplateKeys(templateKeys) {
		return p.betaRoutes[templateKeys[0]], true
	}
	selected := make(map[RuleRevisionID]struct{})
	for _, templateKey := range templateKeys {
		ruleRevisionIDs, routed := p.betaRoutesForTemplateKey(templateKey)
		if !routed {
			return nil, false
		}
		for _, ruleRevisionID := range ruleRevisionIDs {
			selected[ruleRevisionID] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return nil, true
	}
	ruleRevisionIDs := make([]RuleRevisionID, 0, len(selected))
	for _, rule := range p.rules {
		if _, ok := selected[rule.ruleRevisionID]; ok {
			ruleRevisionIDs = append(ruleRevisionIDs, rule.ruleRevisionID)
		}
	}
	return ruleRevisionIDs, true
}

func sameTemplateKeys(templateKeys []TemplateKey) bool {
	first := templateKeys[0]
	for _, templateKey := range templateKeys[1:] {
		if templateKey != first {
			return false
		}
	}
	return true
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

type factSource interface {
	sourceGeneration() Generation
	factsForTarget(conditionTarget) ([]FactSnapshot, bool)
}

type alphaFactSource interface {
	factsForCondition(ConditionID) ([]FactSnapshot, bool)
}

func (m *alphaMatcher) match(ctx context.Context, source factSource) ([]ruleMatchResult, error) {
	if m == nil || m.revision == nil || m.source == nil || source == nil {
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

		candidates, err := rule.matchCandidatesWithAlpha(ctx, source, m.source)
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
	for _, conditionMemory := range m.conditions {
		if conditionMemory != nil {
			conditionMemory.clear()
		}
	}
	for _, fact := range facts {
		m.insert(plan, fact, nil)
	}
}

func (m *reteAlphaMemory) insert(plan reteNetworkPlan, fact FactSnapshot, span *propagationCounterSpan) {
	if m == nil {
		return
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		m.insertCondition(condition, fact, span)
	})
}

func (m *reteAlphaMemory) insertGenerated(plan reteNetworkPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) {
	if m == nil {
		return
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		m.insertConditionGenerated(condition, fact, snapshot, span)
	})
}

func (m *reteAlphaMemory) insertSelected(conditions []reteConditionPlan, fact FactSnapshot, span *propagationCounterSpan) bool {
	if m == nil {
		return false
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false
		}
	}
	for _, condition := range conditions {
		m.insertCondition(condition, fact, span)
	}
	return true
}

func (m *reteAlphaMemory) insertSelectedGenerated(conditions []reteConditionPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) bool {
	if m == nil {
		return false
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false
		}
	}
	for _, condition := range conditions {
		m.insertConditionGenerated(condition, fact, snapshot, span)
	}
	return true
}

func (m *reteAlphaMemory) insertCondition(condition reteConditionPlan, fact FactSnapshot, span *propagationCounterSpan) bool {
	if m == nil {
		return false
	}
	if span != nil {
		span.recordConditionsTested()
	}
	if !condition.matchesAlpha(fact) {
		return true
	}
	conditionMemory := m.conditions[condition.conditionID]
	if conditionMemory == nil {
		return false
	}
	if conditionMemory.upsert(fact) && span != nil {
		span.recordAlphaMatchAdded()
	}
	return true
}

func (m *reteAlphaMemory) insertConditionGenerated(condition reteConditionPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) bool {
	if m == nil {
		return false
	}
	if span != nil {
		span.recordConditionsTested()
	}
	if !condition.matchesAlphaWorking(fact) {
		return true
	}
	conditionMemory := m.conditions[condition.conditionID]
	if conditionMemory == nil {
		return false
	}
	if conditionMemory.upsert(snapshot) && span != nil {
		span.recordAlphaMatchAdded()
	}
	return true
}

func (m *reteAlphaMemory) update(plan reteNetworkPlan, before, after FactSnapshot) {
	if m == nil {
		return
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		m.updateCondition(condition, before, after)
	})
}

func (m *reteAlphaMemory) updateCondition(condition reteConditionPlan, before, after FactSnapshot) {
	if m == nil {
		return
	}
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

func (m *reteAlphaMemory) removeSelected(conditions []reteConditionPlan, id FactID) bool {
	if m == nil {
		return false
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false
		}
	}
	for _, condition := range conditions {
		m.conditions[condition.conditionID].remove(id)
	}
	return true
}

func (m *reteAlphaMemory) updateSelected(conditions []reteConditionPlan, before, after FactSnapshot) bool {
	if m == nil {
		return false
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false
		}
	}
	for _, condition := range conditions {
		m.updateCondition(condition, before, after)
	}
	return true
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

func (m *reteAlphaConditionMemory) clear() {
	if m == nil {
		return
	}
	for i := range m.facts {
		m.facts[i] = FactSnapshot{}
	}
	clear(m.indexes)
	m.facts = m.facts[:0]
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
	ref := newConditionFactRefFromSnapshot(fact)
	for _, constraint := range p.constraints {
		if !constraint.matches(ref) {
			return false
		}
	}
	return true
}

func (p reteConditionPlan) matchesAlphaWorking(fact *workingFact) bool {
	if !p.supported || fact == nil {
		return false
	}
	switch p.target.kind {
	case conditionTargetTemplateKey:
		if fact.templateKey != p.target.templateKey {
			return false
		}
	case conditionTargetName:
		if fact.name != p.target.name {
			return false
		}
	default:
		return false
	}
	for _, constraint := range p.constraints {
		if !constraint.matchesWorking(fact) {
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
