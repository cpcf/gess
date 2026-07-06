package engine

import "sort"

func (s *Session) branchTopStage(terminalID reteGraphTerminalNodeID, branchID int) (reteGraphStageRef, bool) {
	if s.rete == nil || s.rete.graph == nil {
		return reteGraphStageRef{}, false
	}
	for stage, routes := range s.rete.graph.terminalsByStage {
		for _, route := range routes {
			if route.terminalID == terminalID && route.branchID == branchID {
				return stage, true
			}
		}
	}
	return reteGraphStageRef{}, false
}

// betaChain returns the branch's beta nodes in leaf-to-top order (each adds one
// condition) plus the leaf stage feeding the bottom node.
func (s *Session) betaChain(topStage reteGraphStageRef) ([]reteGraphBetaNodeID, reteGraphStageRef) {
	var betas []reteGraphBetaNodeID
	stage := topStage
	for i := 0; stage.kind == reteGraphStageBeta && i < 4096; i++ {
		node := s.rete.graph.betaNode(reteGraphBetaNodeID(stage.id))
		if node == nil {
			break
		}
		betas = append(betas, node.id)
		stage = node.left
	}
	for i, j := 0, len(betas)-1; i < j; i, j = i+1, j-1 {
		betas[i], betas[j] = betas[j], betas[i]
	}
	return betas, stage
}

func (s *Session) betaNodeLeftHasRows(betaID reteGraphBetaNodeID) bool {
	node := s.rete.graph.betaNode(betaID)
	mem := s.rete.graphBeta.betaNodeMemoryAt(betaID)
	if node == nil || mem == nil {
		return false
	}
	has := false
	if node.kind == reteGraphBetaNodeNot {
		mem.negative.left.forEachRow(func(*negativeBetaLeftRow) bool { has = true; return false })
	} else {
		mem.left.forEachRow(func(*betaTokenRow) bool { has = true; return false })
	}
	return has
}

func bindingSlotForCondition(insp reteGraphBranchInspection, conditionID ConditionID) (int, bool) {
	for _, authored := range insp.AuthoredOrder {
		if authored.ConditionID == conditionID {
			return authored.BindingSlot, true
		}
	}
	return 0, false
}

func (s *Session) conditionSpan(rule compiledRule, bindingSlot int) SourceSpan {
	if plan, ok := rule.conditionPlanForBindingSlot(bindingSlot); ok {
		return plan.source
	}
	return SourceSpan{}
}

func (s *Session) frontierBucketsAllEmpty(node *reteGraphBetaNode, mem *reteGraphBetaNodeMemory, cfg whyNotConfig, report *WhyNotReport) bool {
	allEmpty := true
	scanned := 0
	mem.left.forEachRow(func(row *betaTokenRow) bool {
		scanned++
		if scanned > cfg.maxProbedRows {
			report.Truncated = true
			return false
		}
		key, ok := graphBetaJoinKeyForLeftToken(node, row.token)
		if !ok {
			return true
		}
		bucketNonEmpty := false
		mem.right.forEachJoinRow(key, func(graphTokenRowID, *betaTokenRow) bool {
			bucketNonEmpty = true
			return false
		})
		if bucketNonEmpty {
			allEmpty = false
			return false
		}
		return true
	})
	return allEmpty
}

func (s *Session) collectBlockers(node *reteGraphBetaNode, betaID reteGraphBetaNodeID, cfg whyNotConfig, report *WhyNotReport) ([]FactID, int) {
	negMem := s.rete.graphBeta.negativeBetaMemory(betaID, node)
	if negMem.memory == nil {
		return nil, 0
	}
	seen := make(map[FactID]struct{})
	var blockers []FactID
	total := 0
	scanned := 0
	negMem.memory.left.forEachRow(func(left *negativeBetaLeftRow) bool {
		if left == nil || left.blockerCount == 0 {
			return true
		}
		scanned++
		if scanned > cfg.maxProbedRows {
			report.Truncated = true
			return false
		}
		negMem.memory.right.forEachJoinRow(left.joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
			rightMatch, ok := negMem.rightLastMatch(rightRow.token)
			if !ok {
				return true
			}
			matched, err := negMem.leftRightMatchToken(left.token, rightRow.token, rightMatch, nil)
			if err != nil || !matched {
				return true
			}
			total++
			if fm, ok := tokenLastMatch(rightRow.token); ok && !fm.fact.id.IsZero() {
				if _, dup := seen[fm.fact.id]; !dup {
					seen[fm.fact.id] = struct{}{}
					blockers = append(blockers, fm.fact.id)
				}
			}
			return true
		})
		return true
	})
	sort.Slice(blockers, func(i, j int) bool { return factIDLess(blockers[i], blockers[j]) })
	if cfg.maxBlockers > 0 && len(blockers) > cfg.maxBlockers {
		blockers = blockers[:cfg.maxBlockers]
		report.Truncated = true
	}
	return blockers, total
}

func (s *Session) decodePartialMatches(rule compiledRule, node *reteGraphBetaNode, betaID reteGraphBetaNodeID, mem *reteGraphBetaNodeMemory, cfg whyNotConfig, span SourceSpan, report *WhyNotReport) []WhyNotPartialMatch {
	var matches []WhyNotPartialMatch
	scanned := 0
	collect := func(token tokenRef) bool {
		scanned++
		if scanned > cfg.maxProbedRows {
			report.Truncated = true
			return false
		}
		if pm := s.partialMatchForToken(rule, token, span); pm.Satisfied > 0 {
			matches = append(matches, pm)
		}
		return true
	}
	if node.kind == reteGraphBetaNodeNot {
		negMem := s.rete.graphBeta.negativeBetaMemory(betaID, node)
		if negMem.memory != nil {
			negMem.memory.left.forEachRow(func(left *negativeBetaLeftRow) bool {
				if left == nil {
					return true
				}
				return collect(left.token)
			})
		}
	} else {
		mem.left.forEachRow(func(row *betaTokenRow) bool {
			if row == nil {
				return true
			}
			return collect(row.token)
		})
	}
	sort.SliceStable(matches, func(i, j int) bool { return partialMatchLess(matches[i], matches[j]) })
	if cfg.maxPartialMatches > 0 && len(matches) > cfg.maxPartialMatches {
		matches = matches[:cfg.maxPartialMatches]
		report.Truncated = true
	}
	return matches
}

func (s *Session) partialMatchForToken(rule compiledRule, token tokenRef, span SourceSpan) WhyNotPartialMatch {
	matches := partialTokenMatches(token)
	pm := WhyNotPartialMatch{Satisfied: len(matches), RejectedBySpan: span}
	for _, match := range matches {
		pm.Facts = append(pm.Facts, match.fact.id)
		binding := BindingValue{Name: bindingName(rule, match.bindingSlot), FromFact: match.fact.id}
		if match.hasValue {
			binding.Value = cloneValue(match.value)
		}
		pm.Bindings = append(pm.Bindings, binding)
	}
	return pm
}

func partialTokenMatches(token tokenRef) []conditionMatch {
	var matches []conditionMatch
	seen := make(map[int]struct{})
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			break
		}
		match, ok := row.conditionMatch()
		if !ok || match.bindingSlot < 0 {
			continue
		}
		if _, dup := seen[match.bindingSlot]; dup {
			continue
		}
		seen[match.bindingSlot] = struct{}{}
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].bindingSlot < matches[j].bindingSlot })
	return matches
}

func bindingName(rule compiledRule, bindingSlot int) string {
	if plan, ok := rule.conditionPlanForBindingSlot(bindingSlot); ok && plan.binding != "" {
		return "?" + plan.binding
	}
	return ""
}

func firstJoinSpan(node *reteGraphBetaNode) SourceSpan {
	for _, join := range node.hashJoins {
		if !sourceSpanIsZero(join.source) {
			return join.source
		}
	}
	for _, join := range node.joins {
		if !sourceSpanIsZero(join.source) {
			return join.source
		}
	}
	return SourceSpan{}
}

func firstResidualSpan(node *reteGraphBetaNode) SourceSpan {
	for _, join := range node.residualJoins {
		if !sourceSpanIsZero(join.source) {
			return join.source
		}
	}
	for _, predicate := range node.predicates {
		if !sourceSpanIsZero(predicate.source) {
			return predicate.source
		}
	}
	for _, predicate := range node.rightPredicates {
		if !sourceSpanIsZero(predicate.source) {
			return predicate.source
		}
	}
	return SourceSpan{}
}

func firstPredicateSpan(node *reteGraphBetaNode) SourceSpan {
	for _, predicate := range node.predicates {
		if !sourceSpanIsZero(predicate.source) {
			return predicate.source
		}
	}
	for _, predicate := range node.rightPredicates {
		if !sourceSpanIsZero(predicate.source) {
			return predicate.source
		}
	}
	return firstResidualSpan(node)
}

func partialMatchLess(a, b WhyNotPartialMatch) bool {
	n := min(len(a.Facts), len(b.Facts))
	for i := range n {
		if a.Facts[i] != b.Facts[i] {
			return factIDLess(a.Facts[i], b.Facts[i])
		}
	}
	return len(a.Facts) < len(b.Facts)
}
