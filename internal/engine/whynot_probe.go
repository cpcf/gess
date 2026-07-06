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
	reason         WhyNotConditionReason
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
	if s.rete == nil || s.rete.graph == nil {
		return nil
	}
	var out []reteGraphBranchInspection
	for _, insp := range s.rete.graph.branchInspections {
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

	conditionIDs := make([]ConditionID, len(branch.Conditions))
	for i := range branch.Conditions {
		conditionIDs[i] = conditionIDForOrder(insp, branch.Conditions[i].Order)
	}

	topStage, ok := s.branchTopStage(insp.TerminalID, insp.BranchID)
	if !ok {
		return branch, false
	}
	betas, leafStage := s.betaChain(topStage)

	frontierIdx := -1
	for i := len(betas) - 1; i >= 0; i-- {
		if s.betaNodeLeftHasRows(betas[i]) {
			frontierIdx = i
			break
		}
	}

	// The failing condition is located by its planned position: the leaf is
	// planned position 0, and each beta (leaf-to-top) adds the next.
	failingPos := -1
	var failure whyNotFailure
	switch {
	case frontierIdx < 0:
		if s.leafMatched(insp, leafStage) {
			return branch, true
		}
		failingPos = 0
		failure = s.classifyLeaf(rule, insp)
	case frontierIdx == len(betas)-1 && s.betaNodeProducesOutput(betas[frontierIdx]):
		return branch, true
	default:
		failingPos = frontierIdx + 1
		failure = s.classifyFrontier(rule, insp, betas[frontierIdx], failingPos, cfg, report)
	}

	failingConditionID := plannedConditionID(insp, failingPos)
	for i, conditionID := range conditionIDs {
		if conditionID == failingConditionID && !conditionID.IsZero() {
			branch.Conditions[i].Reason = failure.reason
			branch.Conditions[i].RejectingSpan = failure.rejectingSpan
			branch.Conditions[i].Blockers = failure.blockers
			branch.Conditions[i].BlockerCount = failure.blockerCount
			branch.FirstFailing = i
			break
		}
	}
	branch.PartialMatches = failure.partialMatches

	plannedOrder := make(map[ConditionID]int, len(insp.PlannedOrder))
	for _, planned := range insp.PlannedOrder {
		plannedOrder[planned.ConditionID] = planned.Order
	}
	if failingPlanned, ok := plannedOrder[failingConditionID]; ok {
		for i, conditionID := range conditionIDs {
			if planned, ok := plannedOrder[conditionID]; ok {
				branch.Conditions[i].Satisfied = planned < failingPlanned
			}
		}
	}
	return branch, false
}

func plannedConditionID(insp reteGraphBranchInspection, plannedPos int) ConditionID {
	for _, planned := range insp.PlannedOrder {
		if planned.Order == plannedPos {
			return planned.ConditionID
		}
	}
	if plannedPos >= 0 && plannedPos < len(insp.PlannedOrder) {
		return insp.PlannedOrder[plannedPos].ConditionID
	}
	return ""
}

// leafMatched reports whether the leaf (first planned) condition has any
// matching fact, i.e. a single-condition branch is completely satisfied.
func (s *Session) leafMatched(insp reteGraphBranchInspection, leafStage reteGraphStageRef) bool {
	if leafStage.kind != reteGraphStageAlpha {
		return false
	}
	conditionID := plannedConditionID(insp, 0)
	return s.rete.graphBeta.alphaFactCount(conditionID) > 0
}

// betaNodeProducesOutput reports whether the node currently emits any output:
// for a positive join a left row with a non-empty join-output chain, for a
// negation a left row with no blockers.
func (s *Session) betaNodeProducesOutput(betaID reteGraphBetaNodeID) bool {
	node := s.rete.graph.betaNode(betaID)
	mem := s.rete.graphBeta.betaNodeMemoryAt(betaID)
	if node == nil || mem == nil {
		return false
	}
	produces := false
	if node.kind == reteGraphBetaNodeNot {
		mem.negative.left.forEachRow(func(row *negativeBetaLeftRow) bool {
			if row != nil && row.blockerCount == 0 {
				produces = true
				return false
			}
			return true
		})
	} else {
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
			Order:     authored.Order,
			Binding:   authored.Binding,
			Negated:   authored.Negated,
			Test:      authored.Test,
			Aggregate: authored.Aggregate,
		}
		if plan, ok := rule.conditionPlanForBindingSlot(authored.BindingSlot); ok {
			cond.Source = plan.source
		}
		for _, planned := range insp.PlannedOrder {
			if planned.ConditionID == authored.ConditionID {
				cond.PlannedOrder = planned.Order
				break
			}
		}
		if !authored.Test && !authored.Negated && !authored.Aggregate {
			cond.AlphaMatches = s.rete.graphBeta.alphaFactCount(authored.ConditionID)
		}
		conditions = append(conditions, cond)
	}
	sort.SliceStable(conditions, func(i, j int) bool { return conditions[i].Order < conditions[j].Order })
	return conditions
}

func conditionIDForOrder(insp reteGraphBranchInspection, order int) ConditionID {
	for _, authored := range insp.AuthoredOrder {
		if authored.Order == order {
			return authored.ConditionID
		}
	}
	return ""
}

func (s *Session) classifyLeaf(rule compiledRule, insp reteGraphBranchInspection) whyNotFailure {
	failure := whyNotFailure{reason: WhyNotReasonNoAlphaMatches}
	if slot, ok := bindingSlotForCondition(insp, plannedConditionID(insp, 0)); ok {
		failure.rejectingSpan = s.conditionSpan(rule, slot)
	}
	return failure
}

func (s *Session) classifyFrontier(rule compiledRule, insp reteGraphBranchInspection, betaID reteGraphBetaNodeID, plannedPos int, cfg whyNotConfig, report *WhyNotReport) whyNotFailure {
	node := s.rete.graph.betaNode(betaID)
	mem := s.rete.graphBeta.betaNodeMemoryAt(betaID)
	if node == nil || mem == nil {
		return whyNotFailure{}
	}
	conditionID := plannedConditionID(insp, plannedPos)
	slot, hasSlot := bindingSlotForCondition(insp, conditionID)
	var failure whyNotFailure

	switch node.kind {
	case reteGraphBetaNodeNot:
		failure.reason = WhyNotReasonNegationBlocked
		if hasSlot {
			failure.rejectingSpan = s.conditionSpan(rule, slot)
		}
		failure.blockers, failure.blockerCount = s.collectBlockers(node, betaID, cfg, report)
	case reteGraphBetaNodeFilter, reteGraphBetaNodeResidualFilter:
		failure.reason = WhyNotReasonPredicate
		failure.rejectingSpan = firstPredicateSpan(node)
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
