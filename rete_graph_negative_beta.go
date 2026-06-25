package gess

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
	bucket := m.memory.left.bucketForKey(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(bucket.len())
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		leftRow := m.memory.left.row(rowID)
		if leftRow == nil || leftRow.token.isZero() {
			continue
		}
		if m.node.rightHasLeftPrefix && !tokenRefHasPrefix(token, leftRow.token) {
			continue
		}
		if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 {
			if ok, err := m.owner.residualJoinsMatch(m.node, currentMatch.fact, leftRow.token, span); err != nil {
				return false, err
			} else if !ok {
				continue
			}
		}
		if ok, err := m.owner.rightPredicatesMatch(m.node, currentMatch, leftRow.token, span); err != nil {
			return false, err
		} else if !ok {
			continue
		}
		if leftRow.incrementNegativeBlockerCount() == 1 && !deferOutputs {
			m.owner.propagateRemoveFromStage(source, leftRow.token, nil, delta)
		}
	}
	return true, nil
}

func (m reteGraphNegativeBetaMemory) blockerCountForLeft(joinKey betaJoinKey, left tokenRef, span *propagationCounterSpan) (int, bool) {
	if m.owner == nil || m.node == nil || m.memory == nil || left.isZero() {
		return 0, false
	}
	bucket := m.memory.right.bucketForKey(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(bucket.len())
	}
	count := 0
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		rightRow := m.memory.right.row(rowID)
		if rightRow == nil || rightRow.token.isZero() {
			continue
		}
		if m.node.rightHasLeftPrefix && !tokenRefHasPrefix(rightRow.token, left) {
			continue
		}
		if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 || len(m.node.rightPredicates) != 0 {
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				return count, false
			}
			if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 {
				ok, err := m.owner.residualJoinsMatch(m.node, rightMatch.fact, left, span)
				if err != nil {
					return count, false
				}
				if !ok {
					continue
				}
			}
			ok, err := m.owner.rightPredicatesMatch(m.node, rightMatch, left, span)
			if err != nil {
				return count, false
			}
			if !ok {
				continue
			}
		}
		count++
	}
	return count, true
}
