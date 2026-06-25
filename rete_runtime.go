package gess

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type reteRuntime struct {
	revision               *Ruleset
	graph                  *reteGraph
	plan                   reteNetworkPlan
	mode                   reteRuntimeMode
	graphBeta              *reteGraphBetaMemory
	terminalRemovedScratch candidateScratch
	terminalAddedScratch   candidateScratch
}

type reteRuntimeMode uint8

const (
	runtimeModeUnsupported reteRuntimeMode = iota
	runtimeModeGraphBeta
)

type reteNetworkPlan struct {
	rules                      []reteRulePlan
	alphaRoutes                map[TemplateKey][]reteConditionPlan
	betaRoutes                 map[TemplateKey][]RuleRevisionID
	betaConditionRoutes        map[TemplateKey][]reteBetaConditionRoute
	unsupported                []reteUnsupportedReason
	stats                      retePlanStats
	betaSupported              bool
	incrementalAgendaSupported bool
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
	conditionID     ConditionID
	binding         string
	bindingSlot     int
	path            []int
	target          conditionTarget
	constraints     []compiledFieldConstraint
	predicates      []compiledExpressionPredicate
	alphaPredicates []compiledExpressionPredicate
	alpha           reteAlphaPlan
	beta            []reteBetaPlan
	supported       bool
	betaSupported   bool
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
	conditionID    ConditionID
	bindingSlot    int
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
	reteUnsupportedMissingTarget reteUnsupportedKind = "missing-target"
	reteUnsupportedUnindexedJoin reteUnsupportedKind = "unindexed-join"
	reteUnsupportedExpression    reteUnsupportedKind = "expression-predicate"
	reteUnsupportedAggregate     reteUnsupportedKind = "aggregate"
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
	runtime := &reteRuntime{
		revision: revision,
		graph:    revision.graph,
		plan:     planReteNetwork(revision),
	}
	runtime.mode = runtime.determineMode()
	return runtime, nil
}

func (r *reteRuntime) validateExecutableGraphBetaRuntime() error {
	if r == nil || r.revision == nil {
		return ErrInvalidRuleset
	}
	if len(r.plan.rules) == 0 || r.usesGraphBeta() {
		return nil
	}
	return r.unsupportedRuntimeError()
}

func (r *reteRuntime) unsupportedRuntimeError() error {
	if r == nil {
		return ErrUnsupportedRuntime
	}
	var details []string
	if r.graph == nil {
		details = append(details, propagationUnsupportedNoGraph)
	}
	if !r.plan.betaSupported {
		details = append(details, propagationUnsupportedBetaUnsupported)
	}
	for _, reason := range r.plan.unsupported {
		if reason.ruleID != "" {
			details = append(details, fmt.Sprintf("%s rule=%q binding=%q detail=%q", reason.kind, reason.ruleID, reason.binding, reason.detail))
			continue
		}
		details = append(details, string(reason.kind))
	}
	if r.graph != nil && r.plan.betaSupported && len(r.plan.unsupported) == 0 {
		for _, node := range r.graph.betaNodes {
			if len(node.joins) > 0 && len(node.hashJoins) == 0 && len(node.residualJoins) == 0 {
				details = append(details, propagationUnsupportedNonEqualityJoin)
			}
		}
	}
	if len(details) == 0 {
		return ErrUnsupportedRuntime
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedRuntime, strings.Join(details, "; "))
}

func (r *reteRuntime) match(ctx context.Context, source factSource) ([]ruleMatchResult, error) {
	if r == nil || r.revision == nil || source == nil {
		return nil, ErrInvalidRuleset
	}
	if len(r.plan.rules) == 0 {
		return nil, nil
	}
	if err := r.validateExecutableGraphBetaRuntime(); err != nil {
		return nil, err
	}
	if r.usesGraphBeta() && r.graphBeta != nil {
		return r.graphBeta.match(ctx, source)
	}
	return nil, r.unsupportedRuntimeError()
}

func (r *reteRuntime) matchWithoutSnapshot(ctx context.Context, generation Generation) ([]ruleMatchResult, bool, error) {
	if r == nil || r.revision == nil {
		return nil, false, nil
	}
	if len(r.plan.rules) == 0 {
		return nil, true, nil
	}
	if err := r.validateExecutableGraphBetaRuntime(); err != nil {
		return nil, true, err
	}
	if r.supportsIncrementalAgenda() {
		return r.graphBeta.matchWithoutSnapshot(ctx, generation)
	}
	return nil, false, nil
}

func (r *reteRuntime) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	if r == nil || r.revision == nil || !r.supportsIncrementalAgenda() {
		return nil, false, nil
	}
	if r.usesGraphBeta() && r.graphBeta != nil {
		return r.graphBeta.currentTerminalTokenDeltas(ctx)
	}
	return nil, false, nil
}

func (r *reteRuntime) queryRows(ctx context.Context, query compiledQuery, args map[string]Value, event reteGraphPropagationEvent, source Snapshot) ([]QueryRow, bool, error) {
	if r == nil || r.revision == nil || !r.usesGraphBeta() || r.graphBeta == nil {
		return nil, false, nil
	}
	return r.graphBeta.queryRows(ctx, query, args, event, source)
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

func (r *reteRuntime) resetGraphBeta(ctx context.Context, facts []FactSnapshot) error {
	return r.resetGraphBetaForGeneration(ctx, facts, reteGraphFactsGeneration(facts))
}

func (r *reteRuntime) resetGraphBetaForGeneration(ctx context.Context, facts []FactSnapshot, generation Generation) error {
	if r == nil {
		return nil
	}
	r.mode = r.determineMode()
	if !r.usesGraphBeta() {
		r.graphBeta = nil
		if len(r.plan.rules) == 0 {
			return nil
		}
		return r.unsupportedRuntimeError()
	}
	if r.graphBeta == nil {
		memory, err := newReteGraphBetaMemoryForGeneration(ctx, r.revision, r.graph, facts, generation)
		if err != nil {
			return err
		}
		r.graphBeta = memory
		return nil
	}
	return r.graphBeta.resetFactsForGeneration(ctx, facts, generation)
}

func (r *reteRuntime) clearMemories() {
	if r == nil {
		return
	}
	r.graphBeta = nil
}

func (r *reteRuntime) rebuildBeta(ctx context.Context, facts []FactSnapshot) error {
	if r == nil {
		return nil
	}
	r.mode = r.determineMode()
	if !r.usesGraphBeta() {
		r.graphBeta = nil
		return nil
	}
	memory, err := newReteGraphBetaMemory(ctx, r.revision, r.graph, facts)
	if err != nil {
		return err
	}
	r.graphBeta = memory
	return nil
}

func (r *reteRuntime) insertBetaFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, error) {
	return r.insertBetaFactWithOrigin(ctx, fact, mutationOrigin{}, span)
}

func (r *reteRuntime) insertBetaFactGenerated(ctx context.Context, fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if r == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	return r.propagateBetaEvent(ctx, newReteGraphGeneratedAssertEvent(fact, r.revision, origin, span))
}

func (r *reteRuntime) insertBetaFactWithOrigin(ctx context.Context, fact FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	return r.propagateBetaEvent(ctx, newReteGraphAssertEvent(fact, origin, span))
}

func (r *reteRuntime) removeBetaFact(ctx context.Context, fact FactSnapshot, origin mutationOrigin, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	return r.propagateBetaEvent(ctx, newReteGraphRetractEvent(fact, origin, counters))
}

func (r *reteRuntime) updateBetaFact(ctx context.Context, before, after FactSnapshot, changes []FieldChange, duplicateChanged bool, origin mutationOrigin, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	return r.propagateBetaEvent(ctx, newReteGraphModifyEvent(r.revision, before, after, changes, duplicateChanged, origin, counters))
}

func (r *reteRuntime) propagateBetaEvent(ctx context.Context, event reteGraphPropagationEvent) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.usesGraphBeta() && r.graphBeta != nil {
		delta, err := r.graphBeta.propagateEvent(ctx, event)
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
}

func (r *reteRuntime) supportsIncrementalAgenda() bool {
	return r != nil && r.usesGraphBeta() && r.plan.incrementalAgendaSupported && r.graphBeta != nil
}

func (r *reteRuntime) supportsGraphBeta() bool {
	if r == nil || r.graph == nil || !r.plan.betaSupported || len(r.plan.unsupported) != 0 {
		return false
	}
	return true
}

func (r *reteRuntime) determineMode() reteRuntimeMode {
	if r == nil || r.revision == nil {
		return runtimeModeUnsupported
	}
	if r.supportsGraphBeta() {
		return runtimeModeGraphBeta
	}
	return runtimeModeUnsupported
}

func (r *reteRuntime) usesGraphBeta() bool {
	return r != nil && r.mode == runtimeModeGraphBeta && r.supportsGraphBeta()
}

func (r *reteRuntime) propagationDiagnostics() (propagationRuntimePath, map[string]int) {
	if r == nil {
		return propagationRuntimeUnknown, nil
	}
	path := propagationRuntimeUnknown
	switch {
	case r.usesGraphBeta() && r.graphBeta != nil:
		path = propagationRuntimeGraphBeta
	case r.mode == runtimeModeUnsupported:
		path = propagationRuntimeUnsupported
	}

	reasons := make(map[string]int)
	if r.graph == nil {
		reasons[propagationUnsupportedNoGraph]++
	}
	if !r.plan.betaSupported {
		reasons[propagationUnsupportedBetaUnsupported]++
	}
	for _, reason := range r.plan.unsupported {
		reasons[string(reason.kind)]++
	}
	if r.graph != nil && r.plan.betaSupported && len(r.plan.unsupported) == 0 {
		for _, node := range r.graph.betaNodes {
			if len(node.joins) > 0 && len(node.hashJoins) == 0 && len(node.residualJoins) == 0 {
				reasons[propagationUnsupportedNonEqualityJoin]++
			}
		}
	}
	if path == propagationRuntimeGraphBeta && len(reasons) == 0 {
		return path, nil
	}
	return path, reasons
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
		if delta.token.isZero() {
			continue
		}
		rule, ok := r.revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, ErrMatcher
		}
		candidate, err := buildMatchCandidateFromTokenRefWithScratch(rule, tokenRefGeneration(delta.token), delta.token, scratch)
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
		if delta.token.isZero() {
			continue
		}
		candidateCount++
		entryCount += delta.token.size()
		pathCount += delta.token.pathLen()
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

func (r *reteRuntime) insertGraphAlphaFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, bool, error) {
	if r == nil || r.revision == nil || r.graph == nil || !r.supportsIncrementalAgenda() {
		return reteAgendaDelta{}, false, nil
	}
	delta, err := r.graphBeta.insertFact(ctx, fact, span)
	if err != nil {
		return delta, true, err
	}
	delta.supported = r.supportsIncrementalAgenda()
	return delta, true, nil
}

func (r *reteRuntime) insertGraphAlphaFactGenerated(ctx context.Context, fact *workingFact, span *propagationCounterSpan) (reteAgendaDelta, bool, error) {
	if r == nil || r.revision == nil || r.graph == nil || !r.supportsIncrementalAgenda() || fact == nil {
		return reteAgendaDelta{}, false, nil
	}
	delta, err := r.graphBeta.insertFactGenerated(ctx, fact, span)
	if err != nil {
		return delta, true, err
	}
	delta.supported = r.supportsIncrementalAgenda()
	return delta, true, nil
}

func (r *reteRuntime) alphaFactCount(conditionID ConditionID) int {
	if r == nil {
		return 0
	}
	if r.graphBeta != nil {
		return r.graphBeta.alphaFactCount(conditionID)
	}
	return 0
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
		aggregateUnsupported := ruleAggregateUnsupportedReasons(rule)
		if len(aggregateUnsupported) != 0 {
			rulePlan.supported = false
			rulePlan.betaSupported = false
			plan.incrementalAgendaSupported = false
			plan.unsupported = append(plan.unsupported, aggregateUnsupported...)
		}
		ruleRouteKeys := make(map[TemplateKey]struct{})

		aggregateIncrementalSupported := ruleAggregatesIncrementalAgendaSupported(rule)
		for _, condition := range rule.conditionPlans {
			if condition.aggregate != nil && aggregateIncrementalSupported {
				continue
			}
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
					conditionID:    condition.conditionID,
					bindingSlot:    condition.bindingSlot,
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
		plan.rules = append(plan.rules, rulePlan)
	}
	plan.betaSupported = revision.graph != nil
	plan.incrementalAgendaSupported = len(plan.rules) > 0
	for _, rulePlan := range plan.rules {
		rule, ok := revision.rulesByRevisionID[rulePlan.ruleRevisionID]
		if ok && rule.hasAggregateConditions() && !ruleAggregatesIncrementalAgendaSupported(rule) {
			plan.incrementalAgendaSupported = false
		}
		if !rulePlan.betaSupported {
			plan.betaSupported = false
		}
		if !rulePlan.supported || !rulePlan.betaSupported {
			plan.incrementalAgendaSupported = false
		}
	}

	return plan
}

func ruleAggregatesIncrementalAgendaSupported(rule compiledRule) bool {
	if len(ruleAggregateUnsupportedReasons(rule)) != 0 {
		return false
	}
	return rule.hasAggregateConditions()
}

func ruleAggregateUnsupportedReasons(rule compiledRule) []reteUnsupportedReason {
	hasAggregate := false
	var unsupported []reteUnsupportedReason
	for _, branch := range rule.executionConditionBranches() {
		aggregateIndex := -1
		for i, plan := range branch.plans {
			if plan.aggregate == nil {
				continue
			}
			hasAggregate = true
			if aggregateIndex >= 0 {
				unsupported = append(unsupported, reteUnsupportedReason{
					ruleID:         rule.id,
					ruleRevisionID: rule.revisionID,
					conditionID:    plan.id,
					binding:        plan.binding,
					kind:           reteUnsupportedAggregate,
					detail:         "multiple aggregate conditions in one branch are not graph-supported",
				})
				continue
			}
			aggregateIndex = i
		}
		if aggregateIndex < 0 {
			continue
		}
		if !reteGraphSupportsAggregateCondition(branch.plans[aggregateIndex], aggregateIndex > 0) {
			plan := branch.plans[aggregateIndex]
			unsupported = append(unsupported, reteUnsupportedReason{
				ruleID:         rule.id,
				ruleRevisionID: rule.revisionID,
				conditionID:    plan.id,
				binding:        plan.binding,
				kind:           reteUnsupportedAggregate,
				detail:         "aggregate condition shape is not graph-supported",
			})
		}
	}
	if !hasAggregate {
		return nil
	}
	return unsupported
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
	predicates := cloneCompiledExpressionPredicates(condition.predicates)
	conditionPlan := reteConditionPlan{
		conditionID:     condition.id,
		binding:         condition.binding,
		bindingSlot:     condition.bindingSlot,
		path:            cloneIntPath(condition.path),
		target:          condition.target,
		constraints:     condition.constraints,
		predicates:      predicates,
		alphaPredicates: alphaExpressionPredicates(predicates),
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
		betaSupported: false,
	}
	if condition.aggregate != nil {
		conditionPlan.supported = true
		conditionPlan.betaSupported = true
		return conditionPlan, nil
	}
	if condition.isTest {
		conditionPlan.supported = true
		conditionPlan.betaSupported = true
		return conditionPlan, nil
	}

	var unsupported []reteUnsupportedReason
	hashJoinCount := 0
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
		if _, ok := revision.templateByKey(condition.target.templateKey); !ok {
			addUnsupported(reteUnsupportedMissingTarget, "template target is not present in the compiled revision")
		}
	case conditionTargetName:
		if condition.target.name == "" {
			addUnsupported(reteUnsupportedNameTarget, "name target is empty")
		}
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
		if join.isHashJoin() {
			hashJoinCount++
		}
	}
	for _, predicate := range condition.predicates {
		if !predicate.graphExecutable() {
			addUnsupported(reteUnsupportedExpression, fmt.Sprintf("expression predicate %d is not executable by the current graph runtime", predicate.order))
		}
	}
	if conditionPlan.supported && (len(condition.joins) == 0 || hashJoinCount > 0 || len(condition.joins) == len(conditionPlan.beta)) {
		conditionPlan.betaSupported = true
	}

	return conditionPlan, unsupported
}

type factSource interface {
	sourceGeneration() Generation
	factsForTarget(conditionTarget) ([]FactSnapshot, bool)
}

type indexedFactSource interface {
	factsForTargetFieldEqual(conditionTarget, int, reteGraphAlphaRouteValue) ([]FactSnapshot, bool)
}

type alphaIndexCounterRecorder interface {
	recordAlphaIndexProbe(bool)
	recordAlphaIndexFallbackScan()
}

type alphaFactSource interface {
	factsForCondition(ConditionID) ([]FactSnapshot, bool)
}

func (p reteConditionPlan) matchesAlpha(fact FactSnapshot) bool {
	ok, _ := p.matchesAlphaWithContextAndCounters(context.Background(), fact, nil)
	return ok
}

func (p reteConditionPlan) matchesAlphaWithContextAndCounters(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (bool, error) {
	if !p.supported {
		return false, nil
	}
	switch p.target.kind {
	case conditionTargetTemplateKey:
		if fact.TemplateKey() != p.target.templateKey {
			return false, nil
		}
	case conditionTargetName:
		if fact.Name() != p.target.name {
			return false, nil
		}
	default:
		return false, nil
	}
	ref := newConditionFactRefFromSnapshot(fact)
	for _, constraint := range p.constraints {
		if !constraint.matches(ref) {
			return false, nil
		}
	}
	ok, err := expressionPredicatesMatchWithContextAndCounters(ctx, p.alphaPredicates, ref, nil, span)
	if err != nil || !ok {
		return ok, err
	}
	return true, nil
}

func (p reteConditionPlan) matchesAlphaWorking(fact *workingFact) bool {
	ok, _ := p.matchesAlphaWorkingWithContextAndCounters(context.Background(), fact, nil)
	return ok
}

func (p reteConditionPlan) matchesAlphaWorkingWithContextAndCounters(ctx context.Context, fact *workingFact, span *propagationCounterSpan) (bool, error) {
	if !p.supported || fact == nil {
		return false, nil
	}
	switch p.target.kind {
	case conditionTargetTemplateKey:
		if fact.templateKey != p.target.templateKey {
			return false, nil
		}
	case conditionTargetName:
		if fact.name != p.target.name {
			return false, nil
		}
	default:
		return false, nil
	}
	for _, constraint := range p.constraints {
		if !constraint.matchesWorking(fact) {
			return false, nil
		}
	}
	ref := newConditionFactRefFromWorkingFact(fact)
	ok, err := expressionPredicatesMatchWithContextAndCounters(ctx, p.alphaPredicates, ref, nil, span)
	if err != nil || !ok {
		return ok, err
	}
	return true, nil
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
