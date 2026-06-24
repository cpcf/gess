package gess

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const reteAlphaMinimumFacts = 32

type reteRuntime struct {
	revision               *Ruleset
	graph                  *reteGraph
	plan                   reteNetworkPlan
	graphAlpha             *reteGraphAlphaMemory
	graphBeta              *reteGraphBetaMemory
	alpha                  *reteAlphaMemory
	terminalRemovedScratch candidateScratch
	terminalAddedScratch   candidateScratch
}

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
	return runtime, nil
}

func (r *reteRuntime) validateExecutableGraphBetaRuntime() error {
	if r == nil || r.revision == nil {
		return ErrInvalidRuleset
	}
	if len(r.plan.rules) == 0 || r.supportsGraphBeta() {
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
	if r.graphBeta != nil {
		return r.graphBeta.match(ctx, source, r.alpha)
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
	if r.graphBeta != nil {
		return r.graphBeta.matchWithoutSnapshot(ctx, generation)
	}
	return nil, false, r.unsupportedRuntimeError()
}

func (r *reteRuntime) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	if r == nil || r.revision == nil || !r.supportsIncrementalAgenda() {
		return nil, false, nil
	}
	if r.graphBeta != nil {
		return r.graphBeta.currentTerminalTokenDeltas(ctx)
	}
	return nil, false, nil
}

func (r *reteRuntime) queryRows(ctx context.Context, query compiledQuery, args map[string]Value, trigger FactSnapshot, source Snapshot) ([]QueryRow, bool, error) {
	if r == nil || r.revision == nil || r.graphBeta == nil {
		return nil, false, nil
	}
	return r.graphBeta.queryRows(ctx, query, args, trigger, source)
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

func (r *reteRuntime) resetAlpha(ctx context.Context, facts []FactSnapshot) error {
	if r == nil {
		return nil
	}
	if r.graph != nil {
		if r.graphAlpha == nil {
			r.graphAlpha = newReteGraphAlphaMemory(r.graph)
		}
		if err := r.rebuildGraphAlpha(ctx, facts); err != nil {
			return err
		}
	}
	if r.alpha == nil {
		r.alpha = newReteAlphaMemory(r.plan)
	}
	if err := r.alpha.reset(ctx, r.plan, facts); err != nil {
		return err
	}
	if !r.plan.betaSupported {
		r.graphBeta = nil
		return nil
	}
	if r.supportsGraphBeta() {
		if r.graphBeta == nil {
			memory, err := newReteGraphBetaMemory(ctx, r.revision, r.graph, facts)
			if err != nil {
				return err
			}
			r.graphBeta = memory
		} else {
			if err := r.graphBeta.resetFacts(ctx, facts); err != nil {
				return err
			}
		}
		return nil
	}
	r.graphBeta = nil
	return nil
}

func (r *reteRuntime) clearMemories() {
	if r == nil {
		return
	}
	r.graphAlpha = nil
	r.graphBeta = nil
	r.alpha = nil
}

func (r *reteRuntime) rebuildBeta(ctx context.Context, facts []FactSnapshot) error {
	if r == nil {
		return nil
	}
	if !r.plan.betaSupported {
		r.graphBeta = nil
		return nil
	}
	if r.supportsGraphBeta() {
		memory, err := newReteGraphBetaMemory(ctx, r.revision, r.graph, facts)
		if err != nil {
			return err
		}
		r.graphBeta = memory
		return nil
	}
	r.graphBeta = nil
	return nil
}

func (r *reteRuntime) insertBetaFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (reteAgendaDelta, error) {
	return r.insertBetaFactWithOrigin(ctx, fact, mutationOrigin{}, span)
}

func (r *reteRuntime) insertBetaFactGenerated(ctx context.Context, fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if r == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.graphBeta != nil {
		delta, err := r.graphBeta.insertFactGenerated(ctx, fact, span)
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
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.graphBeta != nil {
		delta, err := r.graphBeta.insertFact(ctx, fact, span)
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
}

func (r *reteRuntime) removeBetaFact(ctx context.Context, fact FactSnapshot, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.graphBeta != nil {
		delta, err := r.graphBeta.removeFact(ctx, fact, counters)
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
}

func (r *reteRuntime) updateBetaFact(ctx context.Context, before, after FactSnapshot, counters *propagationCounterLedger) (reteAgendaDelta, error) {
	if r == nil {
		return reteAgendaDelta{}, nil
	}
	incrementalAgendaSupported := r.supportsIncrementalAgenda()
	if r.graphBeta != nil {
		delta, err := r.graphBeta.updateFact(ctx, before, after, counters)
		if err != nil {
			return delta, err
		}
		delta.supported = delta.supported && incrementalAgendaSupported
		return delta, nil
	}
	return reteAgendaDelta{}, nil
}

func (r *reteRuntime) supportsIncrementalAgenda() bool {
	return r != nil && r.plan.incrementalAgendaSupported && r.graphBeta != nil
}

func (r *reteRuntime) supportsGraphBeta() bool {
	if r == nil || r.graph == nil || !r.plan.betaSupported || len(r.plan.unsupported) != 0 {
		return false
	}
	return true
}

func (r *reteRuntime) propagationDiagnostics() (propagationRuntimePath, map[string]int) {
	if r == nil {
		return propagationRuntimeUnknown, nil
	}
	path := propagationRuntimeUnknown
	switch {
	case r.graphBeta != nil:
		path = propagationRuntimeGraphBeta
	case len(r.plan.unsupported) > 0 || r.alpha == nil:
		path = propagationRuntimeUnsupported
	case r.graphAlpha != nil || r.alpha != nil:
		path = propagationRuntimeGraphAlpha
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

func (r *reteRuntime) removeGraphAlphaFact(ctx context.Context, fact FactSnapshot) error {
	if r == nil || r.revision == nil || r.graph == nil || r.graphAlpha == nil {
		return nil
	}
	nodeIDs := r.graphAlphaRouteIDsForSnapshot(fact)
	if len(nodeIDs) == 0 {
		return nil
	}
	for _, nodeID := range nodeIDs {
		node := r.graph.alphaNode(nodeID)
		if node == nil {
			continue
		}
		matched, err := node.matchesSnapshotWithContextAndCounters(ctx, fact, nil)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		r.graphAlpha.remove(nodeID, fact.ID())
	}
	return nil
}

func (r *reteRuntime) updateGraphAlphaFact(ctx context.Context, before, after FactSnapshot) error {
	if r == nil || r.revision == nil || r.graph == nil || r.graphAlpha == nil {
		return nil
	}
	if before.TemplateKey() == after.TemplateKey() {
		nodeIDs := r.graphAlphaRouteIDsForSnapshot(after)
		for _, nodeID := range nodeIDs {
			node := r.graph.alphaNode(nodeID)
			if node == nil {
				continue
			}
			matchedBefore, err := node.matchesSnapshotWithContextAndCounters(ctx, before, nil)
			if err != nil {
				return err
			}
			matchesAfter, err := node.matchesSnapshotWithContextAndCounters(ctx, after, nil)
			if err != nil {
				return err
			}
			switch {
			case matchedBefore && matchesAfter:
				r.graphAlpha.upsert(nodeID, after)
			case matchedBefore:
				r.graphAlpha.remove(nodeID, before.ID())
			case matchesAfter:
				r.graphAlpha.upsert(nodeID, after)
			}
		}
		return nil
	}
	if err := r.removeGraphAlphaFact(ctx, before); err != nil {
		return err
	}
	nodeIDs := r.graphAlphaRouteIDsForSnapshot(after)
	if len(nodeIDs) == 0 {
		return nil
	}
	for _, nodeID := range nodeIDs {
		node := r.graph.alphaNode(nodeID)
		if node == nil {
			continue
		}
		matched, err := node.matchesSnapshotWithContextAndCounters(ctx, after, nil)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		r.graphAlpha.upsert(nodeID, after)
	}
	return nil
}

func (r *reteRuntime) projectGraphAlphaConsumerSnapshot(consumer reteBetaConditionRoute, fact FactSnapshot, span *propagationCounterSpan) {
	if r == nil || r.alpha == nil {
		return
	}
	conditionMemory := r.alpha.conditions[consumer.conditionID]
	if conditionMemory == nil {
		return
	}
	conditionMemory.upsert(fact)
}

func (r *reteRuntime) rebuildGraphAlpha(ctx context.Context, facts []FactSnapshot) error {
	if r == nil || r.revision == nil || r.graph == nil {
		return nil
	}
	if r.graphAlpha == nil {
		r.graphAlpha = newReteGraphAlphaMemory(r.graph)
	}
	r.graphAlpha.reset()
	for _, fact := range facts {
		nodeIDs := r.graphAlphaRouteIDsForSnapshot(fact)
		if len(nodeIDs) == 0 {
			continue
		}
		for _, nodeID := range nodeIDs {
			node := r.graph.alphaNode(nodeID)
			if node == nil {
				continue
			}
			matched, err := node.matchesSnapshotWithContextAndCounters(ctx, fact, nil)
			if err != nil {
				return err
			}
			if !matched {
				continue
			}
			r.graphAlpha.upsert(nodeID, fact)
		}
	}
	return nil
}

func (r *reteRuntime) graphAlphaRouteIDsForSnapshot(fact FactSnapshot) []reteGraphAlphaNodeID {
	if r == nil || r.graph == nil {
		return nil
	}
	templateIDs := r.graph.routesByTemplateKey[fact.TemplateKey()]
	nameIDs := r.graph.routesByName[fact.Name()]
	if len(templateIDs) == 0 {
		return nameIDs
	}
	if len(nameIDs) == 0 {
		return templateIDs
	}
	routes := make([]reteGraphAlphaNodeID, 0, len(templateIDs)+len(nameIDs))
	routes = append(routes, templateIDs...)
	routes = append(routes, nameIDs...)
	slices.Sort(routes)
	return routes
}

func (r *reteRuntime) graphAlphaRouteIDsForWorkingFact(fact *workingFact) []reteGraphAlphaNodeID {
	if r == nil || r.graph == nil || fact == nil {
		return nil
	}
	templateIDs := r.graph.routesByTemplateKey[fact.templateKey]
	nameIDs := r.graph.routesByName[fact.name]
	if len(templateIDs) == 0 {
		return nameIDs
	}
	if len(nameIDs) == 0 {
		return templateIDs
	}
	routes := make([]reteGraphAlphaNodeID, 0, len(templateIDs)+len(nameIDs))
	routes = append(routes, templateIDs...)
	routes = append(routes, nameIDs...)
	slices.Sort(routes)
	return routes
}

func (r *reteRuntime) insertAlphaFact(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) error {
	if r == nil || r.alpha == nil {
		return nil
	}
	if conditions, routed := r.plan.alphaRoutesForTemplateKey(fact.TemplateKey()); routed {
		ok, err := r.alpha.insertSelected(ctx, conditions, fact, span)
		if err != nil || ok {
			return err
		}
	}
	return r.alpha.insert(ctx, r.plan, fact, span)
}

func (r *reteRuntime) insertAlphaFactGenerated(ctx context.Context, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) error {
	if r == nil || r.alpha == nil || fact == nil {
		return nil
	}
	if conditions, routed := r.plan.alphaRoutesForTemplateKey(fact.templateKey); routed {
		ok, err := r.alpha.insertSelectedGenerated(ctx, conditions, fact, snapshot, span)
		if err != nil || ok {
			return err
		}
	}
	return r.alpha.insertGenerated(ctx, r.plan, fact, snapshot, span)
}

func (r *reteRuntime) removeAlphaFact(ctx context.Context, fact FactSnapshot) error {
	if r == nil {
		return nil
	}
	if err := r.removeGraphAlphaFact(ctx, fact); err != nil {
		return err
	}
	if r.alpha == nil {
		return nil
	}
	if conditions, routed := r.plan.alphaRoutesForTemplateKey(fact.TemplateKey()); routed {
		if r.alpha.removeSelected(conditions, fact.ID()) {
			return nil
		}
	}
	r.alpha.remove(fact.ID())
	return nil
}

func (r *reteRuntime) updateAlphaFact(ctx context.Context, before, after FactSnapshot) error {
	if r == nil {
		return nil
	}
	if err := r.updateGraphAlphaFact(ctx, before, after); err != nil {
		return err
	}
	if r.alpha == nil {
		return nil
	}
	if before.TemplateKey() == after.TemplateKey() {
		if conditions, routed := r.plan.alphaRoutesForTemplateKey(after.TemplateKey()); routed {
			ok, err := r.alpha.updateSelected(ctx, conditions, before, after)
			if err != nil || ok {
				return err
			}
		}
	}
	return r.alpha.update(ctx, r.plan, before, after)
}

func (r *reteRuntime) alphaFactCount(conditionID ConditionID) int {
	if r == nil {
		return 0
	}
	if r.graphBeta != nil {
		return r.graphBeta.alphaFactCount(conditionID)
	}
	if r.alpha == nil {
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
		if rule.hasAggregateConditions() && !ruleAggregatesIncrementalAgendaSupported(rule) {
			rulePlan.supported = true
			rulePlan.betaSupported = true
			plan.incrementalAgendaSupported = false
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
	plan.betaSupported = len(plan.rules) > 0 || (revision.graph != nil && len(revision.graph.betaNodes) > 0)
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
	hasAggregate := false
	for _, branch := range rule.executionConditionBranches() {
		aggregateIndex := -1
		for i, plan := range branch.plans {
			if plan.aggregate == nil {
				continue
			}
			hasAggregate = true
			if aggregateIndex >= 0 {
				return false
			}
			aggregateIndex = i
		}
		if aggregateIndex < 0 {
			continue
		}
		if !reteGraphSupportsAggregateCondition(branch.plans[aggregateIndex], aggregateIndex > 0) {
			return false
		}
	}
	return hasAggregate
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
		if join.indexKind == joinIndexEquality {
			hashJoinCount++
		}
		if !join.indexable {
			addUnsupported(reteUnsupportedUnindexedJoin, "join is not indexable by the current planner")
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

type reteGraphAlphaMemory struct {
	nodes map[reteGraphAlphaNodeID]*reteAlphaConditionMemory
}

func newReteGraphAlphaMemory(graph *reteGraph) *reteGraphAlphaMemory {
	if graph == nil {
		return nil
	}
	memory := &reteGraphAlphaMemory{
		nodes: make(map[reteGraphAlphaNodeID]*reteAlphaConditionMemory, len(graph.alphaNodes)),
	}
	for _, node := range graph.alphaNodes {
		memory.nodes[node.id] = &reteAlphaConditionMemory{
			facts:   make([]FactSnapshot, 0, reteAlphaConditionFactReserve),
			indexes: make(map[FactID]int, reteAlphaConditionIndexReserve),
		}
	}
	return memory
}

func (m *reteGraphAlphaMemory) reset() {
	if m == nil {
		return
	}
	for _, nodeMemory := range m.nodes {
		if nodeMemory != nil {
			nodeMemory.clear()
		}
	}
}

func (m *reteGraphAlphaMemory) upsert(id reteGraphAlphaNodeID, fact FactSnapshot) bool {
	if m == nil {
		return false
	}
	nodeMemory := m.nodes[id]
	if nodeMemory == nil {
		nodeMemory = &reteAlphaConditionMemory{
			facts:   make([]FactSnapshot, 0, reteAlphaConditionFactReserve),
			indexes: make(map[FactID]int, reteAlphaConditionIndexReserve),
		}
		if m.nodes == nil {
			m.nodes = make(map[reteGraphAlphaNodeID]*reteAlphaConditionMemory)
		}
		m.nodes[id] = nodeMemory
	}
	return nodeMemory.upsert(fact)
}

func (m *reteGraphAlphaMemory) remove(id reteGraphAlphaNodeID, factID FactID) {
	if m == nil {
		return
	}
	nodeMemory := m.nodes[id]
	if nodeMemory == nil {
		return
	}
	nodeMemory.remove(factID)
}

func (m *reteGraphAlphaMemory) factCount(id reteGraphAlphaNodeID) int {
	if m == nil {
		return 0
	}
	nodeMemory := m.nodes[id]
	if nodeMemory == nil {
		return 0
	}
	return len(nodeMemory.facts)
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

const reteAlphaConditionFactReserve = 128
const reteAlphaConditionIndexReserve = 16

func newReteAlphaMemory(plan reteNetworkPlan) *reteAlphaMemory {
	memory := &reteAlphaMemory{
		conditions: make(map[ConditionID]*reteAlphaConditionMemory, plan.stats.conditions),
	}
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		memory.conditions[condition.conditionID] = &reteAlphaConditionMemory{
			facts:   make([]FactSnapshot, 0, reteAlphaConditionFactReserve),
			indexes: make(map[FactID]int, reteAlphaConditionIndexReserve),
		}
	})
	return memory
}

func (m *reteAlphaMemory) reset(ctx context.Context, plan reteNetworkPlan, facts []FactSnapshot) error {
	if m == nil {
		return nil
	}
	for _, conditionMemory := range m.conditions {
		if conditionMemory != nil {
			conditionMemory.clear()
		}
	}
	for _, fact := range facts {
		if err := m.insert(ctx, plan, fact, nil); err != nil {
			return err
		}
	}
	return nil
}

func (m *reteAlphaMemory) insert(ctx context.Context, plan reteNetworkPlan, fact FactSnapshot, span *propagationCounterSpan) error {
	if m == nil {
		return nil
	}
	var err error
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		if err != nil {
			return
		}
		_, err = m.insertCondition(ctx, condition, fact, span)
	})
	return err
}

func (m *reteAlphaMemory) insertGenerated(ctx context.Context, plan reteNetworkPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) error {
	if m == nil {
		return nil
	}
	var err error
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		if err != nil {
			return
		}
		_, err = m.insertConditionGenerated(ctx, condition, fact, snapshot, span)
	})
	return err
}

func (m *reteAlphaMemory) insertSelected(ctx context.Context, conditions []reteConditionPlan, fact FactSnapshot, span *propagationCounterSpan) (bool, error) {
	if m == nil {
		return false, nil
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false, nil
		}
	}
	for _, condition := range conditions {
		if _, err := m.insertCondition(ctx, condition, fact, span); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (m *reteAlphaMemory) insertSelectedGenerated(ctx context.Context, conditions []reteConditionPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) (bool, error) {
	if m == nil {
		return false, nil
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false, nil
		}
	}
	for _, condition := range conditions {
		if _, err := m.insertConditionGenerated(ctx, condition, fact, snapshot, span); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (m *reteAlphaMemory) insertCondition(ctx context.Context, condition reteConditionPlan, fact FactSnapshot, span *propagationCounterSpan) (bool, error) {
	if m == nil {
		return false, nil
	}
	if span != nil {
		span.recordConditionsTested()
	}
	matched, err := condition.matchesAlphaWithContextAndCounters(ctx, fact, span)
	if err != nil {
		return true, err
	}
	if !matched {
		return true, nil
	}
	conditionMemory := m.conditions[condition.conditionID]
	if conditionMemory == nil {
		return false, nil
	}
	if conditionMemory.upsert(fact) && span != nil {
		span.recordAlphaMatchAdded()
	}
	return true, nil
}

func (m *reteAlphaMemory) insertConditionGenerated(ctx context.Context, condition reteConditionPlan, fact *workingFact, snapshot FactSnapshot, span *propagationCounterSpan) (bool, error) {
	if m == nil {
		return false, nil
	}
	if span != nil {
		span.recordConditionsTested()
	}
	matched, err := condition.matchesAlphaWorkingWithContextAndCounters(ctx, fact, span)
	if err != nil {
		return true, err
	}
	if !matched {
		return true, nil
	}
	conditionMemory := m.conditions[condition.conditionID]
	if conditionMemory == nil {
		return false, nil
	}
	if conditionMemory.upsert(snapshot) && span != nil {
		span.recordAlphaMatchAdded()
	}
	return true, nil
}

func (m *reteAlphaMemory) update(ctx context.Context, plan reteNetworkPlan, before, after FactSnapshot) error {
	if m == nil {
		return nil
	}
	var err error
	plan.forEachSupportedCondition(func(condition reteConditionPlan) {
		if err != nil {
			return
		}
		err = m.updateCondition(ctx, condition, before, after)
	})
	return err
}

func (m *reteAlphaMemory) updateCondition(ctx context.Context, condition reteConditionPlan, before, after FactSnapshot) error {
	if m == nil {
		return nil
	}
	conditionMemory := m.conditions[condition.conditionID]
	if conditionMemory == nil {
		return nil
	}
	matchedBefore := conditionMemory.contains(before.id)
	matchesAfter, err := condition.matchesAlphaWithContextAndCounters(ctx, after, nil)
	if err != nil {
		return err
	}
	switch {
	case matchedBefore && matchesAfter:
		conditionMemory.upsert(after)
	case matchedBefore:
		conditionMemory.remove(before.id)
	case matchesAfter:
		conditionMemory.upsert(after)
	}
	return nil
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

func (m *reteAlphaMemory) updateSelected(ctx context.Context, conditions []reteConditionPlan, before, after FactSnapshot) (bool, error) {
	if m == nil {
		return false, nil
	}
	for _, condition := range conditions {
		if m.conditions == nil || m.conditions[condition.conditionID] == nil {
			return false, nil
		}
	}
	for _, condition := range conditions {
		if err := m.updateCondition(ctx, condition, before, after); err != nil {
			return true, err
		}
	}
	return true, nil
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
		m.indexes = make(map[FactID]int, reteAlphaConditionIndexReserve)
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
