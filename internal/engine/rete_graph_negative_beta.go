package engine

type reteGraphNegativeBetaMemory struct {
	owner  *reteGraphBetaMemory
	id     reteGraphBetaNodeID
	node   *reteGraphBetaNode
	memory *reteGraphBetaNodeMemory
}

func (m *reteGraphBetaMemory) negativeBetaMemory(nodeID reteGraphBetaNodeID, node *reteGraphBetaNode) reteGraphNegativeBetaMemory {
	if m == nil || node == nil {
		return reteGraphNegativeBetaMemory{}
	}
	return reteGraphNegativeBetaMemory{
		owner:  m,
		id:     nodeID,
		node:   node,
		memory: m.nodeMemory(nodeID),
	}
}

func (m reteGraphNegativeBetaMemory) insertLeft(joinKey betaJoinKey, token tokenRef, deferOutputs bool, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || token.isZero() {
		return false, nil
	}
	count, ok := m.blockerCountForLeft(joinKey, token, span)
	if !ok {
		return false, nil
	}
	inserted := m.memory.left.insertWithNegativeBlockerCount(token, joinKey, count)
	if !inserted {
		return true, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputLeft)
	}
	if count != 0 || deferOutputs {
		return true, nil
	}
	if span != nil {
		span.recordBetaJoinedTokenProduced()
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	if err := m.owner.propagateFromStage(source, token, span, delta); err != nil {
		return false, err
	}
	return true, nil
}

func (m reteGraphNegativeBetaMemory) insertRight(joinKey betaJoinKey, token tokenRef, deferOutputs bool, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || token.isZero() {
		return false, nil
	}
	inserted := m.memory.right.insert(token, joinKey)
	if !inserted {
		return true, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputRight)
	}
	var currentMatch conditionMatch
	var ok bool
	if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 || len(m.node.rightPredicates) != 0 {
		currentMatch, ok = tokenLastMatch(token)
		if !ok {
			return false, nil
		}
	}
	depth := m.memory.left.joinRowCount(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(depth)
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	var joinErr error
	m.memory.left.forEachJoinRow(joinKey, func(_ graphTokenRowID, leftRow *betaTokenRow) bool {
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		if leftRow == nil || leftRow.token.isZero() {
			return true
		}
		if m.node.rightHasLeftPrefix && !tokenRefHasPrefix(token, leftRow.token) {
			return true
		}
		if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 {
			if ok, err := m.owner.residualJoinsMatch(m.node, currentMatch.fact, leftRow.token, span); err != nil {
				joinErr = err
				return false
			} else if !ok {
				return true
			}
		}
		if ok, err := m.owner.rightPredicatesMatch(m.node, currentMatch, leftRow.token, span); err != nil {
			joinErr = err
			return false
		} else if !ok {
			return true
		}
		if leftRow.incrementNegativeBlockerCount() == 1 && !deferOutputs {
			m.owner.propagateRemoveFromStage(source, leftRow.token, nil, delta)
		}
		return true
	})
	if joinErr != nil {
		return false, joinErr
	}
	return true, nil
}

func (m reteGraphNegativeBetaMemory) removeLeft(joinKey betaJoinKey, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.memory == nil || delta == nil || token.isZero() {
		return false
	}
	removedRow, removedOK := m.memory.left.removeTokenWithJoinKey(token, joinKey, counters)
	if !removedOK {
		return true
	}
	if counters != nil {
		counters.recordNegativeRowRemoved()
	}
	if removedRow.negativeBlockerCount() != 0 {
		return true
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	m.owner.propagateRemoveFromStage(source, removedRow.token, counters, delta)
	return true
}

func (m reteGraphNegativeBetaMemory) removeRight(joinKey betaJoinKey, token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || token.isZero() {
		return false
	}
	removedRow, removedOK := m.memory.right.removeTokenWithJoinKey(token, joinKey, counters)
	if !removedOK {
		return true
	}
	if counters != nil {
		counters.recordNegativeRowRemoved()
	}
	var currentMatch conditionMatch
	var ok bool
	if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 || len(m.node.rightPredicates) != 0 {
		currentMatch, ok = tokenLastMatch(removedRow.token)
		if !ok {
			return false
		}
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	m.memory.left.forEachJoinRow(joinKey, func(_ graphTokenRowID, leftRow *betaTokenRow) bool {
		if leftRow == nil || leftRow.token.isZero() {
			return true
		}
		if m.node.rightHasLeftPrefix && !tokenRefHasPrefix(removedRow.token, leftRow.token) {
			return true
		}
		if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 {
			if ok, err := m.owner.residualJoinsMatch(m.node, currentMatch.fact, leftRow.token, nil); err != nil {
				delta.supported = false
			} else if !ok {
				return true
			}
		}
		if ok, err := m.owner.rightPredicatesMatch(m.node, currentMatch, leftRow.token, nil); err != nil {
			delta.supported = false
		} else if !ok {
			return true
		}
		if leftRow.negativeBlockerCount() <= 0 {
			delta.supported = false
			return true
		}
		if leftRow.decrementNegativeBlockerCount() == 0 {
			if err := m.owner.propagateFromStage(source, leftRow.token, nil, delta); err != nil {
				delta.supported = false
			}
		}
		return true
	})
	return true
}

func (m reteGraphNegativeBetaMemory) removeContainingFact(side reteGraphBetaInputSide, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || id.IsZero() {
		return false
	}
	switch side {
	case reteGraphBetaInputLeft:
		return m.removeLeftContainingFact(id, counters, delta)
	case reteGraphBetaInputRight:
		return m.removeRightContainingFact(id, counters, delta)
	default:
		return false
	}
}

func (m reteGraphNegativeBetaMemory) removeLeftContainingFact(id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.memory == nil || delta == nil || id.IsZero() {
		return false
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	m.memory.left.removeTokensContainingFact(id, counters, func(row graphTokenRow) {
		if row.negativeBlockerCount() == 0 {
			m.owner.propagateRemoveFromStage(source, row.token, counters, delta)
		}
	})
	return true
}

func (m reteGraphNegativeBetaMemory) removeRightContainingFact(id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || id.IsZero() {
		return false
	}
	var tokens []tokenRef
	m.memory.right.forEachTokenContainingFact(id, counters, func(row graphTokenRow) {
		if !row.token.isZero() {
			tokens = append(tokens, row.token)
		}
	})
	for _, token := range tokens {
		joinKey, ok, err := graphBetaJoinKeyForRightTokenWithContext(m.owner.context(), m.node, token, nil)
		if err != nil || !ok {
			return false
		}
		if !m.removeRight(joinKey, token, counters, delta) {
			return false
		}
	}
	return true
}

func (m reteGraphNegativeBetaMemory) blockerCountForLeft(joinKey betaJoinKey, left tokenRef, span *propagationCounterSpan) (int, bool) {
	if m.owner == nil || m.node == nil || m.memory == nil || left.isZero() {
		return 0, false
	}
	depth := m.memory.right.joinRowCount(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(depth)
	}
	count := 0
	supported := true
	m.memory.right.forEachJoinRow(joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		if rightRow == nil || rightRow.token.isZero() {
			return true
		}
		if m.node.rightHasLeftPrefix && !tokenRefHasPrefix(rightRow.token, left) {
			return true
		}
		if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 || len(m.node.rightPredicates) != 0 {
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				supported = false
				return false
			}
			if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 {
				ok, err := m.owner.residualJoinsMatch(m.node, rightMatch.fact, left, span)
				if err != nil {
					supported = false
					return false
				}
				if !ok {
					return true
				}
			}
			ok, err := m.owner.rightPredicatesMatch(m.node, rightMatch, left, span)
			if err != nil {
				supported = false
				return false
			}
			if !ok {
				return true
			}
		}
		count++
		return true
	})
	return count, supported
}
