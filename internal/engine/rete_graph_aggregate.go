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
	if !aggregateSpecsNeedInputValues(m.node.specs) {
		bucket := m.memory.bucketForParent(m.owner.aggregateParentToken(m.node, token))
		if bucket == nil {
			delta.supported = false
			return
		}
		inserted, ok := bucket.addCountOnlyMember(token, m.node.inputEntry.bindingSlot)
		if !ok {
			delta.supported = false
			return
		}
		if inserted {
			m.owner.refreshAggregateOutputDeferred(m.id, bucket, span, nil, delta)
		}
		return
	}
	match, ok := tokenFactMatchForBindingSlot(token, m.node.inputEntry.bindingSlot)
	if !ok {
		delta.supported = false
		return
	}
	bucket := m.memory.bucketForParent(m.owner.aggregateParentToken(m.node, token))
	if bucket == nil {
		delta.supported = false
		return
	}
	memberKey := tokenRefKey(token)
	if existing, ok := bucket.scalarMembers[memberKey]; ok {
		delete(bucket.scalarMembers, memberKey)
		if !bucket.removeScalarMember(m.node, existing, &m.memory.numeric) {
			delta.supported = false
			return
		}
	}
	member, ok := aggregateScalarMemberFromToken(m.owner.context(), m.node, match, token)
	if !ok {
		delta.supported = false
		return
	}
	if err := bucket.addScalarMember(m.node, member, &m.memory.numeric); err != nil {
		delta.supported = false
		return
	}
	if bucket.scalarMembers == nil {
		bucket.scalarMembers = make(map[graphTokenIdentityKey]reteGraphAggregateScalarMember)
	}
	bucket.scalarMembers[memberKey] = member
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
	if !aggregateSpecsNeedInputValues(m.node.specs) {
		if bucket.removeCountOnlyMember(token) {
			m.owner.refreshAggregateOutputDeferred(m.id, bucket, nil, counters, delta)
		}
		return
	}
	memberKey := tokenRefKey(token)
	member, ok := bucket.scalarMembers[memberKey]
	if !ok {
		return
	}
	delete(bucket.scalarMembers, memberKey)
	if !bucket.removeScalarMember(m.node, member, &m.memory.numeric) {
		delta.supported = false
		return
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
		changed := false
		if !aggregateSpecsNeedInputValues(m.node.specs) {
			for key, memberFactID := range bucket.countOnlyMembers {
				if memberFactID != factID {
					continue
				}
				delete(bucket.countOnlyMembers, key)
				if bucket.count > 0 {
					bucket.count--
				}
				changed = true
			}
		} else if len(bucket.scalarMembers) > 0 {
			for key, member := range bucket.scalarMembers {
				if member.factID != factID {
					continue
				}
				delete(bucket.scalarMembers, key)
				if !bucket.removeScalarMember(m.node, member, &m.memory.numeric) {
					delta.supported = false
					return
				}
				changed = true
			}
		}
		if changed {
			m.owner.refreshAggregateOutputDeferred(m.id, bucket, nil, counters, delta)
		}
	})
}
