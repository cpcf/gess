package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type reteRuntime struct {
	revision               *Ruleset
	graph                  *reteGraph
	globalValues           []Value
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

func newReteRuntime(revision *Ruleset, globals ...[]Value) (*reteRuntime, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}
	var globalValues []Value
	if len(globals) > 0 {
		globalValues = globals[0]
	}
	runtime := &reteRuntime{
		revision:     revision,
		graph:        revision.graph,
		globalValues: cloneGlobalValues(globalValues),
		plan:         planReteNetwork(revision),
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

func (r *reteRuntime) mayEmitBackchainDemandDeltas() bool {
	if r == nil || !r.usesGraphBeta() || r.graph == nil {
		return false
	}
	return r.graph.hasBackchainDemandPlans()
}

func (r *reteRuntime) queryRows(ctx context.Context, query compiledQuery, args *compiledQueryArgs, event reteGraphPropagationEvent, source Snapshot) ([]QueryRow, bool, error) {
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
	_, err := r.resetGraphBetaForGenerationWithDelta(ctx, facts, generation)
	return err
}

func (r *reteRuntime) resetGraphBetaForGenerationWithDelta(ctx context.Context, facts []FactSnapshot, generation Generation) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	r.mode = r.determineMode()
	if !r.usesGraphBeta() {
		r.graphBeta = nil
		if len(r.plan.rules) == 0 {
			return reteAgendaDelta{supported: true}, nil
		}
		return reteAgendaDelta{}, r.unsupportedRuntimeError()
	}
	if r.graphBeta == nil {
		memory, delta, err := newReteGraphBetaMemoryForGenerationWithDelta(ctx, r.revision, r.graph, facts, generation, r.globalValues)
		if err != nil {
			return delta, err
		}
		r.graphBeta = memory
		return delta, nil
	}
	return r.graphBeta.resetFactsForGenerationWithDelta(ctx, facts, generation)
}

func (r *reteRuntime) resetGraphBetaFromWorkspaceForGenerationWithDelta(ctx context.Context, facts *factWorkspace, generation Generation) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	r.mode = r.determineMode()
	if !r.usesGraphBeta() {
		r.graphBeta = nil
		if len(r.plan.rules) == 0 {
			return reteAgendaDelta{supported: true}, nil
		}
		return reteAgendaDelta{}, r.unsupportedRuntimeError()
	}
	if r.graphBeta == nil {
		memory, delta, err := newReteGraphBetaMemoryForWorkspaceWithDelta(ctx, r.revision, r.graph, facts, generation, r.globalValues)
		if err != nil {
			return delta, err
		}
		r.graphBeta = memory
		return delta, nil
	}
	return r.graphBeta.resetFactWorkspaceForGenerationWithDelta(ctx, facts, generation)
}

func (r *reteRuntime) resetGraphBetaForGenerationWithInitialAgenda(ctx context.Context, facts []FactSnapshot, generation Generation, agenda *agenda) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	r.mode = r.determineMode()
	if !r.usesGraphBeta() {
		r.graphBeta = nil
		if len(r.plan.rules) == 0 {
			return reteAgendaDelta{supported: true}, nil
		}
		return reteAgendaDelta{}, r.unsupportedRuntimeError()
	}
	if r.graphBeta == nil {
		memory, delta, err := newReteGraphBetaMemoryForGenerationWithInitialAgenda(ctx, r.revision, r.graph, facts, generation, agenda, r.globalValues)
		if err != nil {
			return delta, err
		}
		r.graphBeta = memory
		return delta, nil
	}
	return r.graphBeta.resetFactsForGenerationWithInitialAgenda(ctx, facts, generation, agenda)
}

func (r *reteRuntime) resetGraphBetaFromWorkspaceForGenerationWithInitialAgenda(ctx context.Context, facts *factWorkspace, generation Generation, agenda *agenda) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	r.mode = r.determineMode()
	if !r.usesGraphBeta() {
		r.graphBeta = nil
		if len(r.plan.rules) == 0 {
			return reteAgendaDelta{supported: true}, nil
		}
		return reteAgendaDelta{}, r.unsupportedRuntimeError()
	}
	if r.graphBeta == nil {
		memory, delta, err := newReteGraphBetaMemoryForWorkspaceWithInitialAgenda(ctx, r.revision, r.graph, facts, generation, agenda, r.globalValues)
		if err != nil {
			return delta, err
		}
		r.graphBeta = memory
		return delta, nil
	}
	return r.graphBeta.resetFactWorkspaceForGenerationWithInitialAgenda(ctx, facts, generation, agenda)
}

func (r *reteRuntime) supportsInitialAgendaReset() bool {
	if r == nil || r.revision == nil || r.graph == nil || r.revision.hasAutoFocusRules() {
		return false
	}
	if len(r.graph.aggregateNodes) != 0 {
		return false
	}
	return true
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
	memory, err := newReteGraphBetaMemoryForGeneration(ctx, r.revision, r.graph, facts, reteGraphFactsGeneration(facts), r.globalValues)
	if err != nil {
		return err
	}
	r.graphBeta = memory
	return nil
}

func (r *reteRuntime) insertBetaFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, error) {
	return r.insertBetaFactWithOrigin(ctx, fact, mutationOrigin{}, span)
}

func (r *reteRuntime) insertBetaWorkingFactWithOrigin(ctx context.Context, fact *workingFact, snapshot FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if r == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.usesGraphBeta() && r.graphBeta != nil {
		delta, err := r.propagateBetaEvent(ctx, newReteGraphWorkingAssertEvent(fact, snapshot, origin, span))
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return r.insertBetaFactWithOrigin(ctx, snapshot, origin, span)
}

func (r *reteRuntime) insertBetaFactGenerated(ctx context.Context, fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if r == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.usesGraphBeta() && r.graphBeta != nil {
		delta, err := r.propagateBetaEvent(ctx, newReteGraphGeneratedAssertEvent(fact, origin, span))
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
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

func (r *reteRuntime) removeBetaWorkingFact(ctx context.Context, fact *workingFact, origin mutationOrigin, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.usesGraphBeta() && r.graphBeta != nil {
		delta, err := r.propagateBetaEvent(ctx, newReteGraphWorkingRetractEvent(fact, origin, counters))
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
}

func (r *reteRuntime) removeBetaGeneratedWorkingFact(ctx context.Context, fact *workingFact, origin mutationOrigin, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.usesGraphBeta() && r.graphBeta != nil {
		delta, err := r.propagateBetaEvent(ctx, newReteGraphGeneratedRetractEvent(fact, origin, counters))
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
}

func (r *reteRuntime) updateBetaFact(ctx context.Context, before FactSnapshot, beforeFact *workingFact, afterFact *workingFact, after FactSnapshot, changes []FieldChange, duplicateChanged bool, origin mutationOrigin, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	return r.propagateBetaEvent(ctx, newReteGraphWorkingModifyEvent(r.revision, before, beforeFact, afterFact, after, changes, duplicateChanged, origin, counters))
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
	candidateCount, entryCount, pathCount := countTerminalDeltaCandidateSpace(deltas, r.revision.rulesByRevisionID)
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

func countTerminalDeltaCandidateSpace(deltas []reteTerminalTokenDelta, rules map[RuleRevisionID]compiledRule) (candidateCount, entryCount, pathCount int) {
	for _, delta := range deltas {
		if delta.token.isZero() {
			continue
		}
		candidateCount++
		entryCount += delta.token.size()
		if rule, ok := rules[delta.ruleRevisionID]; ok {
			pathCount += compiledRuleTokenPathLen(rule)
		} else {
			pathCount += delta.token.size()
		}
	}
	return candidateCount, entryCount, pathCount
}

func (r *reteRuntime) insertGraphAlphaFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, bool, error) {
	if r == nil || r.revision == nil || r.graph == nil || !r.supportsIncrementalAgenda() {
		return reteAgendaDelta{}, false, nil
	}
	delta, err := r.propagateBetaEvent(ctx, newReteGraphAssertEvent(fact, mutationOrigin{}, span))
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
	delta, err := r.propagateBetaEvent(ctx, newReteGraphGeneratedAssertEvent(fact, mutationOrigin{}, span))
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

func (p reteConditionPlan) matchesAlpha(fact FactSnapshot) (bool, error) {
	return p.matchesAlphaWithContextAndCounters(context.Background(), fact, nil)
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
		matched, err := constraint.matches(ref)
		if err != nil || !matched {
			return false, err
		}
	}
	ok, err := expressionPredicatesMatchWithContextGlobalsAndCounters(ctx, p.alphaPredicates, ref, nil, nil, span)
	if err != nil || !ok {
		return ok, err
	}
	return true, nil
}

func (p reteConditionPlan) matchesAlphaWorking(fact *workingFact, compactSlotStore *factCompactSlotStore) (bool, error) {
	return p.matchesAlphaWorkingWithContextAndCounters(context.Background(), fact, compactSlotStore, nil)
}

func (p reteConditionPlan) matchesAlphaWorkingWithContextAndCounters(ctx context.Context, fact *workingFact, compactSlotStore *factCompactSlotStore, span *propagationCounterSpan) (bool, error) {
	if !p.supported || fact == nil {
		return false, nil
	}
	switch p.target.kind {
	case conditionTargetTemplateKey:
		if !fact.matchesTemplateTarget(p.target) {
			return false, nil
		}
	case conditionTargetName:
		if fact.storedName() != p.target.name {
			return false, nil
		}
	default:
		return false, nil
	}
	for _, constraint := range p.constraints {
		matched, err := constraint.matchesWorking(fact, compactSlotStore)
		if err != nil || !matched {
			return false, err
		}
	}
	ref := newConditionFactRefFromWorkingFactForTarget(fact, p.target, compactSlotStore)
	ok, err := expressionPredicatesMatchWithContextGlobalsAndCounters(ctx, p.alphaPredicates, ref, nil, nil, span)
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
