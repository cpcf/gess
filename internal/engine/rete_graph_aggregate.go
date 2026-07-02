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
		if bucket.addCountOnlyMember(token) {
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
	if existing, ok := bucket.members[memberKey]; ok {
		if !m.memory.removeMember(m.node, bucket, existing) {
			delta.supported = false
			return
		}
	}
	member, ok := m.owner.aggregateMember(m.node, token, match)
	if !ok {
		delta.supported = false
		return
	}
	if err := m.memory.addMember(m.node, bucket, member); err != nil {
		delta.supported = false
		return
	}
	if bucket.members == nil {
		bucket.members = make(map[graphTokenIdentityKey]reteGraphAggregateMember)
	}
	bucket.members[memberKey] = member
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
	member, ok := bucket.members[memberKey]
	if !ok {
		return
	}
	delete(bucket.members, memberKey)
	if !m.memory.removeMember(m.node, bucket, member) {
		delta.supported = false
		return
	}
	m.owner.refreshAggregateOutputDeferred(m.id, bucket, nil, counters, delta)
}

func (m reteGraphAggregateMemory) openBucket(parent tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m.owner == nil || delta == nil || parent.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.memory == nil {
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
	if m.owner == nil || delta == nil || parent.isZero() {
		if delta != nil {
			delta.supported = false
		}
		return
	}
	if m.memory == nil {
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
			kept := 0
			count := bucket.countOnlyMemberCount()
			for i := range count {
				token := bucket.countOnlyMemberAt(i)
				if !token.containsFact(factID) {
					bucket.setCountOnlyMemberAt(kept, token)
					kept++
					continue
				}
				changed = true
			}
			bucket.truncateCountOnlyMembers(kept)
		} else if len(bucket.members) > 0 {
			for key, member := range bucket.members {
				if !member.token.containsFact(factID) {
					continue
				}
				delete(bucket.members, key)
				if !m.memory.removeMember(m.node, bucket, member) {
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
