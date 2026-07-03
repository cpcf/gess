package engine

type reteGraphAggregateMemory struct {
	owner  *reteGraphBetaMemory
	id     reteGraphAggregateNodeID
	node   *reteGraphAggregateNode
	memory *reteGraphAggregateNodeMemory
}

func (m *reteGraphBetaMemory) graphAggregateMemory(id reteGraphAggregateNodeID) reteGraphAggregateMemory {
	if m == nil || m.graph == nil || id <= 0 {
		return reteGraphAggregateMemory{}
	}
	return reteGraphAggregateMemory{
		owner:  m,
		id:     id,
		node:   m.graph.aggregateNode(id),
		memory: m.aggregateMemory(id),
	}
}

func (m reteGraphAggregateMemory) insertInput(match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || m.node == nil || match.fact.ID().IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	token := m.owner.newTokenRef(tokenRef{}, m.node.inputEntry, match, match.fact.Recency(), match.fact.Generation(), span)
	m.insertToken(token, span, delta)
}

func (m reteGraphAggregateMemory) removeInput(match conditionMatch, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || m.node == nil || match.fact.ID().IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	token := m.owner.newTokenRef(tokenRef{}, m.node.inputEntry, match, match.fact.Recency(), match.fact.Generation(), nil)
	m.removeToken(token, counters, delta)
}

func (m reteGraphAggregateMemory) insertToken(token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || token.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.node == nil || m.memory == nil {
		delta.supported = false
		return
	}
	bucket := m.memory.bucketForParent(m.owner.aggregateParentToken(m.node, token))
	if bucket == nil {
		delta.supported = false
		return
	}
	if !bucket.addInputToken(token) {
		return
	}
	if !bucket.addAccumulatorToken(m.owner.context(), m.node, token) {
		bucket.removeInputToken(token)
		delta.supported = false
		return
	}
	m.owner.refreshAggregateOutputDeferred(m.id, bucket, span, nil, delta)
}

func (m reteGraphAggregateMemory) removeToken(token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || token.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.node == nil || m.memory == nil {
		delta.supported = false
		return
	}
	bucket, ok := m.memory.bucketForParentIfExists(m.owner.aggregateParentToken(m.node, token))
	if !ok {
		return
	}
	if !bucket.removeInputToken(token) {
		return
	}
	if !bucket.removeAccumulatorToken(m.owner.context(), m.node, token) {
		if !bucket.rebuildAccumulator(m.owner.context(), m.node) {
			delta.supported = false
			return
		}
	}
	m.owner.refreshAggregateOutputDeferred(m.id, bucket, nil, counters, delta)
}

func (m reteGraphAggregateMemory) openBucket(parent tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.node == nil || m.memory == nil {
		delta.supported = false
		return
	}
	if parent.isZero() && m.node.outer.kind != reteGraphStageRoot && m.node.outer.kind != reteGraphStageUnknown {
		delta.supported = false
		return
	}
	bucket := m.memory.bucketForParent(parent)
	if bucket == nil {
		delta.supported = false
		return
	}
	if bucket.hasValue {
		return
	}
	m.owner.refreshAggregateOutputDeferred(m.id, bucket, span, nil, delta)
}

func (m reteGraphAggregateMemory) removeBucket(parent tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.node == nil || m.memory == nil {
		delta.supported = false
		return
	}
	if parent.isZero() && m.node.outer.kind != reteGraphStageRoot && m.node.outer.kind != reteGraphStageUnknown {
		delta.supported = false
		return
	}
	bucket, ok := m.memory.bucketForParentIfExists(parent)
	if !ok {
		return
	}
	if !bucket.token.isZero() {
		stage := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(m.id)}
		m.owner.propagateRemoveFromStage(stage, bucket.token, counters, delta)
	}
	removed, ok := m.memory.removeBucketForParent(parent)
	if ok {
		m.memory.recycleBucket(removed)
	}
}

func (m reteGraphAggregateMemory) removeBucketsContainingFact(factID FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || factID.IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.memory == nil || m.memory.bucketCount() == 0 {
		return
	}
	var removed []graphTokenIdentityKey
	m.memory.forEachBucketWithKey(func(key graphTokenIdentityKey, bucket *reteGraphAggregateBucket) {
		if bucket == nil || !bucket.parent.containsFact(factID) {
			return
		}
		removed = append(removed, key)
	})
	for _, key := range removed {
		bucket, ok := m.memory.bucketByKey(key)
		if !ok {
			continue
		}
		if !bucket.token.isZero() {
			stage := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(m.id)}
			m.owner.propagateRemoveFromStage(stage, bucket.token, counters, delta)
		}
		if removed, ok := m.memory.removeBucketByKey(key); ok {
			m.memory.recycleBucket(removed)
		}
	}
}

func (m reteGraphAggregateMemory) removeMembersContainingFact(factID FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || factID.IsZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.node == nil || m.memory == nil || m.memory.bucketCount() == 0 {
		return
	}
	m.memory.forEachBucket(func(bucket *reteGraphAggregateBucket) {
		if bucket == nil {
			return
		}
		changed, subtracted := bucket.removeInputTokensContainingFactSubtractive(m.owner.context(), m.node, factID)
		if changed {
			if !subtracted && !bucket.rebuildAccumulator(m.owner.context(), m.node) {
				delta.supported = false
				return
			}
			m.owner.refreshAggregateOutputDeferred(m.id, bucket, nil, counters, delta)
		}
	})
}
