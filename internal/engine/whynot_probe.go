package engine

import "sort"

func (s *Session) whyNotLocked(rule compiledRule, cfg whyNotConfig) WhyNotReport {
	report := WhyNotReport{
		RuleID:         rule.id,
		RuleName:       rule.name,
		RuleRevisionID: rule.revisionID,
	}

	if pending := s.pendingActivationsForRule(rule.revisionID); len(pending) > 0 {
		report.Outcome = WhyNotActivated
		report.Activations = pending
		return report
	}
	if s.ruleHasRefractedActivation(rule.revisionID) {
		report.Outcome = WhyNotAlreadyFired
		return report
	}

	inspections := s.branchInspectionsForRule(rule.revisionID)
	branches := make([]WhyNotBranch, 0, len(inspections))
	anyComplete := false
	for _, insp := range inspections {
		branch, complete := s.whyNotBranch(rule, insp, cfg, &report)
		anyComplete = anyComplete || complete
		branches = append(branches, branch)
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].BranchID < branches[j].BranchID })
	report.Branches = branches
	if anyComplete {
		// A branch matches completely but no activation is pending: the rule
		// fired and is refracted (firing consumes the terminal row).
		report.Outcome = WhyNotAlreadyFired
	} else {
		report.Outcome = whyNotOutcome(branches)
	}
	return report
}

// whyNotFailure is the classified failure of one branch's frontier condition.
type whyNotFailure struct {
	reason WhyNotConditionReason
	// plannedOrder locates the failing condition by its position in the planned
	// order, which uniquely identifies every condition — including `test`
	// conditions that carry no ConditionID. Attribution and the satisfied-count
	// keys off this so a test frontier anchors to its own surfaced condition
	// rather than the positive condition below it.
	plannedOrder   int
	rejectingSpan  SourceSpan
	blockers       []FactID
	blockerCount   int
	partialMatches []WhyNotPartialMatch
}

func whyNotOutcome(branches []WhyNotBranch) WhyNotOutcome {
	best := -1
	bestDepth := -1
	for i, branch := range branches {
		depth := branchSatisfiedCount(branch)
		if depth > bestDepth {
			bestDepth = depth
			best = i
		}
	}
	if best < 0 {
		return WhyNotNeverMatched
	}
	branch := branches[best]
	if branch.FirstFailing >= 0 && branch.FirstFailing < len(branch.Conditions) &&
		branch.Conditions[branch.FirstFailing].Reason == WhyNotReasonNegationBlocked {
		return WhyNotBlocked
	}
	return WhyNotNeverMatched
}

func branchSatisfiedCount(branch WhyNotBranch) int {
	count := 0
	for _, cond := range branch.Conditions {
		if cond.Satisfied {
			count++
		}
	}
	return count
}

func (s *Session) pendingActivationsForRule(rev RuleRevisionID) []AgendaActivation {
	if s.agenda == nil {
		return nil
	}
	var out []AgendaActivation
	for _, activations := range s.agenda.pendingByModule() {
		for _, act := range activations {
			if act != nil && act.ruleRevisionID == rev {
				out = append(out, s.agendaActivationView(act))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].activationID.String() < out[j].activationID.String()
	})
	return out
}

// ruleHasRefractedActivation reports whether the rule has an activation that
// already fired and remains matched (refraction). Firing consumes the terminal
// row but retains the activation with a consumed status in the agenda lookup.
func (s *Session) ruleHasRefractedActivation(rev RuleRevisionID) bool {
	if s.agenda == nil {
		return false
	}
	for _, bucket := range s.agenda.activationLookup {
		if activationRefracted(bucket.first, rev) {
			return true
		}
		for _, act := range bucket.rest {
			if activationRefracted(act, rev) {
				return true
			}
		}
	}
	return false
}

func activationRefracted(act *activation, rev RuleRevisionID) bool {
	return act != nil && act.ruleRevisionID == rev && act.status == activationStatusConsumed
}

func (s *Session) branchInspectionsForRule(rev RuleRevisionID) []reteGraphBranchInspection {
	if s.propagation.runtime == nil || s.propagation.runtime.graph == nil {
		return nil
	}
	var out []reteGraphBranchInspection
	for _, insp := range s.propagation.runtime.graph.branchInspections {
		if insp.OwnerKind == reteGraphBranchOwnerRule && insp.RuleRevisionID == rev {
			out = append(out, insp)
		}
	}
	return out
}

// whyNotBranch diagnoses one branch. The second return reports whether the
// branch matches completely (so the rule fired and is refracted).
func (s *Session) whyNotBranch(rule compiledRule, insp reteGraphBranchInspection, cfg whyNotConfig, report *WhyNotReport) (WhyNotBranch, bool) {
	branch := WhyNotBranch{BranchID: insp.BranchID, FirstFailing: -1}
	branch.Conditions = s.whyNotConditions(rule, insp)

	topStage, ok := s.branchTopStage(insp.TerminalID, insp.BranchID)
	if !ok {
		report.Truncated = true
		return branch, false
	}
	complete, failure := s.classifyChainFrontier(rule, insp, topStage, cfg, report)
	if complete {
		for i := range branch.Conditions {
			branch.Conditions[i].Satisfied = true
		}
		return branch, true
	}

	failingPlanned := failure.plannedOrder
	for i := range branch.Conditions {
		cond := &branch.Conditions[i]
		cond.Satisfied = cond.PlannedOrder < failingPlanned
		if cond.PlannedOrder == failingPlanned {
			cond.Reason = failure.reason
			cond.RejectingSpan = failure.rejectingSpan
			cond.Blockers = failure.blockers
			cond.BlockerCount = failure.blockerCount
			branch.FirstFailing = i
		}
	}
	branch.PartialMatches = failure.partialMatches
	return branch, false
}

// classifyChainFrontier walks a branch chain from its top stage to the leaf,
// finds the frontier (the deepest beta still receiving input that stops
// producing output), and classifies the failure there. It reports whether the
// chain matches completely, and otherwise the failing condition id with its
// classified failure.
//
// The failing condition is identified by the frontier node's own condition id;
// only negative/structural nodes (no condition id) fall back to a
// planned-position count, since a single condition can span more than one beta
// stage (e.g. a residual join compiles to a join plus a filter).
func (s *Session) classifyChainFrontier(rule compiledRule, insp reteGraphBranchInspection, topStage reteGraphStageRef, cfg whyNotConfig, report *WhyNotReport) (bool, whyNotFailure) {
	betas, leafStage := s.betaChain(topStage)

	// A beta is a frontier candidate when it received input from below. For most
	// nodes the left memory holds that input, but a filter/residual-filter
	// retains only the rows that PASSED its predicate — its own left memory is an
	// output signal, not an input one. A filter that rejects every input row
	// would otherwise be invisible to this scan, hiding the condition that
	// actually stopped the match and letting the branch look complete. For a
	// filter, test the stage feeding it instead of its passed-rows memory.
	hasInput := func(i int) bool {
		if s.betaNodeIsFilter(betas[i]) {
			if i == 0 {
				return s.leafStageHasRows(insp, leafStage)
			}
			return s.betaNodeProducesOutput(betas[i-1])
		}
		return s.betaNodeLeftHasRows(betas[i])
	}

	frontierIdx := -1
	for i := len(betas) - 1; i >= 0; i-- {
		if hasInput(i) {
			frontierIdx = i
			break
		}
	}

	switch {
	case frontierIdx < 0:
		switch {
		case leafStage.kind == reteGraphStageAggregate:
			return s.classifyAggregateLeaf(rule, insp, leafStage, cfg, report)
		case s.leafMatched(insp, leafStage):
			return true, whyNotFailure{}
		default:
			return false, s.classifyLeaf(rule, insp, leafStage, report)
		}
	case frontierIdx == len(betas)-1 && s.betaNodeProducesOutput(betas[frontierIdx]):
		return true, whyNotFailure{}
	default:
		failingConditionID, plannedOrder, ok := s.frontierConditionLocator(insp, betas[frontierIdx])
		if !ok {
			report.Truncated = true
			return false, whyNotFailure{plannedOrder: -1}
		}
		failure := s.classifyFrontier(rule, insp, betas[frontierIdx], failingConditionID, cfg, report)
		failure.plannedOrder = plannedOrder
		return false, failure
	}
}

// classifyAggregateLeaf diagnoses a branch whose chain terminates on (or is fed
// from) an aggregate stage that produced no output row — a branch reaches here
// only when the aggregate emitted nothing, since any output would have filled
// the beta above it or the terminal itself. It reports a complete match when
// the aggregate does hold an output row (the rule fired and is refracted);
// otherwise the failing condition: the outer chain when no outer token opened a
// bucket, else the aggregate condition itself.
func (s *Session) classifyAggregateLeaf(rule compiledRule, insp reteGraphBranchInspection, leafStage reteGraphStageRef, cfg whyNotConfig, report *WhyNotReport) (bool, whyNotFailure) {
	aggID := reteGraphAggregateNodeID(leafStage.id)
	node := s.propagation.runtime.graph.aggregateNode(aggID)
	if node == nil {
		return false, s.classifyLeaf(rule, insp, leafStage, report)
	}
	mem := s.propagation.runtime.graphBeta.aggregateMemory(aggID)
	if aggregateHasOutput(mem) {
		return true, whyNotFailure{}
	}
	// With a grouping outer chain, no bucket means no outer token reached the
	// aggregate: the failure is upstream, in the conditions before it.
	if node.outer.kind != reteGraphStageUnknown && mem.bucketCount() == 0 {
		if complete, outerFailure := s.classifyChainFrontier(rule, insp, node.outer, cfg, report); !complete {
			return false, outerFailure
		}
	}
	// The outer token exists (or the aggregate has no grouping) but the
	// aggregate produced nothing (e.g. min/max over an empty bucket): the
	// aggregate condition is the frontier.
	stamp, ok := s.conditionStampForStage(insp, leafStage)
	if !ok {
		report.Truncated = true
		return false, whyNotFailure{plannedOrder: -1}
	}
	failure := whyNotFailure{reason: WhyNotReasonNoAlphaMatches, plannedOrder: stamp.plannedOrder}
	if slot, ok := bindingSlotForCondition(insp, node.conditionID); ok {
		failure.rejectingSpan = s.conditionSpan(rule, slot)
	}
	failure.partialMatches = s.decodeAggregatePartialMatches(rule, mem, failure.rejectingSpan, cfg, report)
	return false, failure
}

func aggregateHasOutput(mem *reteGraphAggregateNodeMemory) bool {
	has := false
	mem.forEachBucket(func(bucket *reteGraphAggregateBucket) {
		if bucket != nil && bucket.hasValue {
			has = true
		}
	})
	return has
}

// decodeAggregatePartialMatches reports the outer tokens that opened aggregate
// buckets as near-miss partial matches: the grouping facts that were present
// but whose bucket produced no aggregate row.
func (s *Session) decodeAggregatePartialMatches(rule compiledRule, mem *reteGraphAggregateNodeMemory, span SourceSpan, cfg whyNotConfig, report *WhyNotReport) []WhyNotPartialMatch {
	var matches []WhyNotPartialMatch
	scanned := 0
	mem.forEachBucket(func(bucket *reteGraphAggregateBucket) {
		if bucket == nil || bucket.parent.isZero() {
			return
		}
		scanned++
		if scanned > cfg.maxProbedRows {
			report.Truncated = true
			return
		}
		if pm := s.partialMatchForToken(rule, bucket.parent, span); pm.Satisfied > 0 {
			matches = append(matches, pm)
		}
	})
	sort.SliceStable(matches, func(i, j int) bool { return partialMatchLess(matches[i], matches[j]) })
	if cfg.maxPartialMatches > 0 && len(matches) > cfg.maxPartialMatches {
		matches = matches[:cfg.maxPartialMatches]
		report.Truncated = true
	}
	return matches
}

func plannedConditionAt(insp reteGraphBranchInspection, plannedPos int) (reteGraphConditionOrderInspection, bool) {
	var found reteGraphConditionOrderInspection
	ok := false
	for _, planned := range insp.PlannedOrder {
		if planned.Order != plannedPos {
			continue
		}
		if ok {
			return reteGraphConditionOrderInspection{}, false
		}
		found = planned
		ok = true
	}
	return found, ok
}

// frontierConditionLocator resolves a beta frontier through the condition
// identity stamped onto that node while its branch was compiled.
func (s *Session) frontierConditionLocator(insp reteGraphBranchInspection, betaID reteGraphBetaNodeID) (ConditionID, int, bool) {
	stamp, ok := s.conditionStampForStage(insp, reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)})
	if !ok {
		return "", -1, false
	}
	return stamp.conditionID, stamp.plannedOrder, true
}

func (s *Session) conditionStampForStage(insp reteGraphBranchInspection, stage reteGraphStageRef) (reteGraphConditionStamp, bool) {
	stamp, ok := s.propagation.runtime.graph.conditionStampForStage(stage, insp.RuleRevisionID, insp.BranchID)
	if !ok {
		return reteGraphConditionStamp{}, false
	}
	planned, ok := plannedConditionAt(insp, stamp.plannedOrder)
	if !ok || planned.ConditionID != stamp.conditionID {
		return reteGraphConditionStamp{}, false
	}
	switch stage.kind {
	case reteGraphStageBeta:
		node := s.propagation.runtime.graph.betaNode(reteGraphBetaNodeID(stage.id))
		if node == nil {
			return reteGraphConditionStamp{}, false
		}
		if node.kind == reteGraphBetaNodeFilter && !planned.Test && !planned.Aggregate {
			return reteGraphConditionStamp{}, false
		}
		if node.kind == reteGraphBetaNodeNot && !planned.Negated && !planned.Aggregate {
			return reteGraphConditionStamp{}, false
		}
	case reteGraphStageAggregate:
		if !planned.Aggregate {
			return reteGraphConditionStamp{}, false
		}
	}
	return stamp, true
}

// betaNodeIsFilter reports whether the node only forwards rows that pass a
// predicate (a `test` filter or a residual filter), so its left memory holds
// output, not input.
func (s *Session) betaNodeIsFilter(betaID reteGraphBetaNodeID) bool {
	node := s.propagation.runtime.graph.betaNode(betaID)
	return node != nil && (node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter)
}

// leafStageHasRows reports whether the stage feeding the bottom beta currently
// holds rows: matching facts for an alpha leaf, an output row for an aggregate.
func (s *Session) leafStageHasRows(insp reteGraphBranchInspection, leafStage reteGraphStageRef) bool {
	switch leafStage.kind {
	case reteGraphStageAlpha:
		_, ok := s.conditionStampForStage(insp, leafStage)
		return ok && s.alphaStageFactCount(leafStage) > 0
	case reteGraphStageAggregate:
		return aggregateHasOutput(s.propagation.runtime.graphBeta.aggregateMemory(reteGraphAggregateNodeID(leafStage.id)))
	default:
		return false
	}
}

// leafMatched reports whether the leaf (first planned) condition has any
// matching fact, i.e. a single-condition branch is completely satisfied.
func (s *Session) leafMatched(insp reteGraphBranchInspection, leafStage reteGraphStageRef) bool {
	if leafStage.kind != reteGraphStageAlpha {
		return false
	}
	_, ok := s.conditionStampForStage(insp, leafStage)
	return ok && s.alphaStageFactCount(leafStage) > 0
}

func (s *Session) alphaStageFactCount(stage reteGraphStageRef) int {
	if s == nil || s.propagation.runtime == nil || s.propagation.runtime.graphBeta == nil || stage.kind != reteGraphStageAlpha {
		return 0
	}
	index := stage.id
	if index <= 0 || index >= len(s.propagation.runtime.graphBeta.alpha.facts) {
		return 0
	}
	return s.propagation.runtime.graphBeta.alpha.facts[index].count()
}

func (s *Session) alphaMatchesForCondition(insp reteGraphBranchInspection, plannedOrder int) int {
	if s == nil || s.propagation.runtime == nil || s.propagation.runtime.graph == nil || s.propagation.runtime.graphBeta == nil {
		return 0
	}
	facts := make(map[FactID]struct{})
	for i := range s.propagation.runtime.graph.alphaNodes {
		stage := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(s.propagation.runtime.graph.alphaNodes[i].id)}
		stamp, ok := s.conditionStampForStage(insp, stage)
		if !ok || stamp.plannedOrder != plannedOrder {
			continue
		}
		index := stage.id
		if index <= 0 || index >= len(s.propagation.runtime.graphBeta.alpha.facts) {
			continue
		}
		s.propagation.runtime.graphBeta.alpha.facts[index].forEach(func(id FactID) bool {
			facts[id] = struct{}{}
			return true
		})
	}
	return len(facts)
}

// betaNodeProducesOutput reports whether the node currently emits any output:
// for a positive join a left row with a non-empty join-output chain, for a
// negation a left row with no blockers.
func (s *Session) betaNodeProducesOutput(betaID reteGraphBetaNodeID) bool {
	node := s.propagation.runtime.graph.betaNode(betaID)
	mem := s.propagation.runtime.graphBeta.betaNodeMemoryAt(betaID)
	if node == nil || mem == nil {
		return false
	}
	produces := false
	switch node.kind {
	case reteGraphBetaNodeNot:
		mem.negative.left.forEachRow(func(row *negativeBetaLeftRow) bool {
			if row != nil && row.blockerCount == 0 {
				produces = true
				return false
			}
			return true
		})
	case reteGraphBetaNodeFilter, reteGraphBetaNodeResidualFilter:
		// Filter memories store only rows that passed the predicate (and never
		// set outputHead), so a non-empty left memory is the output signal.
		mem.left.forEachRow(func(row *betaTokenRow) bool {
			if row != nil {
				produces = true
				return false
			}
			return true
		})
	default:
		mem.left.forEachRow(func(row *betaTokenRow) bool {
			if row != nil && row.outputHead != 0 {
				produces = true
				return false
			}
			return true
		})
	}
	return produces
}

func (s *Session) whyNotConditions(rule compiledRule, insp reteGraphBranchInspection) []WhyNotCondition {
	conditions := make([]WhyNotCondition, 0, len(insp.AuthoredOrder))
	for _, authored := range insp.AuthoredOrder {
		cond := WhyNotCondition{
			Order:   authored.Order,
			Binding: authored.Binding,
			Negated: authored.Negated,
		}
		if plan, ok := rule.conditionPlanForBindingSlot(authored.BindingSlot); ok {
			cond.Source = plan.source
		}
		// The authored order carries no aggregate/test marker; the planned order
		// (built from the compiled plans) does, so resolve them by condition id.
		for _, planned := range insp.PlannedOrder {
			if planned.ConditionID == authored.ConditionID {
				cond.PlannedOrder = planned.Order
				cond.Aggregate = planned.Aggregate
				cond.Test = planned.Test
				break
			}
		}
		if plan, ok := rule.conditionPlanAtOrder(insp.BranchID, cond.PlannedOrder); ok && plan.syntheticBinding {
			cond.Binding = ""
		}
		if !cond.Test && !cond.Negated && !cond.Aggregate {
			cond.AlphaMatches = s.alphaMatchesForCondition(insp, cond.PlannedOrder)
		}
		conditions = append(conditions, cond)
	}
	// Standalone `test` conditions are not authored bindings, so they never
	// appear in AuthoredOrder; surface them from the planned order (they carry a
	// zero condition id) so a test-only failure has a condition to anchor to.
	for _, planned := range insp.PlannedOrder {
		if !planned.Test || !planned.ConditionID.IsZero() {
			continue
		}
		conditions = append(conditions, WhyNotCondition{
			Order:        planned.Order,
			PlannedOrder: planned.Order,
			Test:         true,
			Source:       s.testConditionSource(rule, insp, planned),
		})
	}
	// Order the reported conditions by evaluation (planned) order so a surfaced
	// `test` renders between the conditions it runs between, not appended last.
	sort.SliceStable(conditions, func(i, j int) bool { return conditions[i].PlannedOrder < conditions[j].PlannedOrder })
	return conditions
}

// testConditionSource resolves the source span of a standalone `test` condition
// from the compiled branch plans, matched by planned position.
func (s *Session) testConditionSource(rule compiledRule, insp reteGraphBranchInspection, planned reteGraphConditionOrderInspection) SourceSpan {
	if plan, ok := rule.conditionPlanAtOrder(insp.BranchID, planned.Order); ok {
		return plan.source
	}
	return SourceSpan{}
}

func (s *Session) classifyLeaf(rule compiledRule, insp reteGraphBranchInspection, leafStage reteGraphStageRef, report *WhyNotReport) whyNotFailure {
	stamp, ok := s.conditionStampForStage(insp, leafStage)
	if !ok {
		report.Truncated = true
		return whyNotFailure{plannedOrder: -1}
	}
	failure := whyNotFailure{reason: WhyNotReasonNoAlphaMatches, plannedOrder: stamp.plannedOrder}
	if slot, ok := bindingSlotForCondition(insp, stamp.conditionID); ok {
		failure.rejectingSpan = s.conditionSpan(rule, slot)
	}
	return failure
}

func (s *Session) classifyFrontier(rule compiledRule, insp reteGraphBranchInspection, betaID reteGraphBetaNodeID, conditionID ConditionID, cfg whyNotConfig, report *WhyNotReport) whyNotFailure {
	node := s.propagation.runtime.graph.betaNode(betaID)
	if node == nil {
		return whyNotFailure{}
	}
	// A filter that rejected every input row never allocated a node memory (it
	// stores only passed rows), so it is classified from the graph node alone.
	if node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter {
		return whyNotFailure{reason: WhyNotReasonPredicate, rejectingSpan: firstPredicateSpan(node)}
	}
	mem := s.propagation.runtime.graphBeta.betaNodeMemoryAt(betaID)
	if mem == nil {
		return whyNotFailure{}
	}
	slot, hasSlot := bindingSlotForCondition(insp, conditionID)
	var failure whyNotFailure

	switch node.kind {
	case reteGraphBetaNodeNot:
		failure.reason = WhyNotReasonNegationBlocked
		if hasSlot {
			failure.rejectingSpan = s.conditionSpan(rule, slot)
		}
		failure.blockers, failure.blockerCount = s.collectBlockers(node, betaID, cfg, report)
	default:
		// The node's right memory holds the added condition's facts; an empty
		// right side means the condition matched nothing (reliable even for
		// constraint-free conditions, unlike the alpha fact count).
		if frontierRightEmpty(mem) {
			failure.reason = WhyNotReasonNoAlphaMatches
			if hasSlot {
				failure.rejectingSpan = s.conditionSpan(rule, slot)
			}
		} else if s.frontierBucketsAllEmpty(node, mem, cfg, report) {
			failure.reason = WhyNotReasonJoinMismatch
			failure.rejectingSpan = firstJoinSpan(node)
		} else {
			failure.reason = WhyNotReasonPredicate
			failure.rejectingSpan = firstResidualSpan(node)
		}
	}

	failure.partialMatches = s.decodePartialMatches(rule, node, betaID, mem, cfg, failure.rejectingSpan, report)
	return failure
}
