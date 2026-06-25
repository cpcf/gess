package gess

type reteGraphNegativeBetaMemory struct {
	owner  *reteGraphBetaMemory
	node   *reteGraphBetaNode
	memory *reteGraphBetaNodeMemory
}

func (m *reteGraphBetaMemory) negativeBetaMemory(nodeID reteGraphBetaNodeID, node *reteGraphBetaNode) reteGraphNegativeBetaMemory {
	if m == nil || node == nil {
		return reteGraphNegativeBetaMemory{}
	}
	return reteGraphNegativeBetaMemory{
		owner:  m,
		node:   node,
		memory: m.nodeMemory(nodeID),
	}
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
