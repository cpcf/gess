package gess

import (
	"context"
)

type reteGraphBetaMemory struct {
	revision            *Ruleset
	graph               *reteGraph
	nodes               map[reteGraphBetaNodeID]*reteGraphBetaNodeMemory
	terminals           map[reteGraphTerminalNodeID]*reteGraphTerminalMemory
	alphaFacts          map[ConditionID]map[FactID]struct{}
	arena               *tokenArena
	terminalTokenDeltas []reteTerminalTokenDelta
}

type reteGraphBetaNodeMemory struct {
	left  tokenHashMemory
	right tokenHashMemory
}

type reteGraphTerminalMemory struct {
	rows tokenHashMemory
}

type tokenHashMemory struct {
	rows         []graphTokenRow
	indexes      map[betaJoinKey]graphTokenRowIDBucket
	identityRows map[graphTokenIdentityKey]graphTokenRowIDBucket
	factRows     map[FactID]graphTokenRowIDBucket
}

type graphTokenRowID int

type graphTokenRow struct {
	id       graphTokenRowID
	token    tokenRef
	joinKey  betaJoinKey
	identity graphTokenIdentityKey
}

type graphTokenIdentityKey struct {
	size          int
	generation    Generation
	identityState uint64
}

type graphTokenRowIDBucket struct {
	first graphTokenRowID
	rest  []graphTokenRowID
	count int
}

func (b graphTokenRowIDBucket) len() int {
	return b.count
}

func (b graphTokenRowIDBucket) at(index int) (graphTokenRowID, bool) {
	if index < 0 || index >= b.count {
		return 0, false
	}
	if index == 0 {
		return b.first, true
	}
	index--
	if index >= len(b.rest) {
		return 0, false
	}
	return b.rest[index], true
}

func (b *graphTokenRowIDBucket) append(id graphTokenRowID) {
	if b.count == 0 {
		b.first = id
		b.count = 1
		return
	}
	b.rest = append(b.rest, id)
	b.count++
}

func (b *graphTokenRowIDBucket) remove(id graphTokenRowID) bool {
	if b.count == 0 {
		return false
	}
	if b.first == id {
		last := b.count - 1
		if last == 0 {
			b.first = 0
			b.count = 0
			return true
		}
		b.first = b.rest[last-1]
		b.rest[last-1] = 0
		b.rest = b.rest[:last-1]
		b.count--
		return true
	}
	for i, current := range b.rest {
		if current != id {
			continue
		}
		last := len(b.rest) - 1
		b.rest[i] = b.rest[last]
		b.rest[last] = 0
		b.rest = b.rest[:last]
		b.count--
		return true
	}
	return false
}

func (b *graphTokenRowIDBucket) replace(oldID, newID graphTokenRowID) bool {
	if b.count == 0 {
		return false
	}
	if b.first == oldID {
		b.first = newID
		return true
	}
	for i, current := range b.rest {
		if current == oldID {
			b.rest[i] = newID
			return true
		}
	}
	return false
}

func (b graphTokenRowIDBucket) forEach(fn func(graphTokenRowID) bool) {
	if b.count == 0 {
		return
	}
	if !fn(b.first) {
		return
	}
	for i := 0; i < b.count-1 && i < len(b.rest); i++ {
		if !fn(b.rest[i]) {
			return
		}
	}
}

func (b graphTokenRowIDBucket) reset() graphTokenRowIDBucket {
	for i := range b.rest {
		b.rest[i] = 0
	}
	b.first = 0
	b.rest = b.rest[:0]
	b.count = 0
	return b
}

func newReteGraphBetaMemory(revision *Ruleset, graph *reteGraph, facts []FactSnapshot) *reteGraphBetaMemory {
	if revision == nil || graph == nil {
		return nil
	}
	memory := &reteGraphBetaMemory{
		revision:   revision,
		graph:      graph,
		nodes:      make(map[reteGraphBetaNodeID]*reteGraphBetaNodeMemory, len(graph.betaNodes)),
		terminals:  make(map[reteGraphTerminalNodeID]*reteGraphTerminalMemory, len(graph.terminalNodes)),
		alphaFacts: make(map[ConditionID]map[FactID]struct{}),
		arena:      newTokenArena(),
	}
	memory.resetFacts(facts)
	return memory
}

func (m *tokenHashMemory) clear() {
	if m == nil {
		return
	}
	for i := range m.rows {
		m.rows[i] = graphTokenRow{}
	}
	m.rows = m.rows[:0]
	for key, bucket := range m.indexes {
		m.indexes[key] = bucket.reset()
	}
	for key, bucket := range m.identityRows {
		m.identityRows[key] = bucket.reset()
	}
	for key, bucket := range m.factRows {
		m.factRows[key] = bucket.reset()
	}
}

func (m *tokenHashMemory) len() int {
	if m == nil {
		return 0
	}
	return len(m.rows)
}

func (m *tokenHashMemory) bucketForKey(key betaJoinKey) graphTokenRowIDBucket {
	if m == nil || len(m.indexes) == 0 {
		return graphTokenRowIDBucket{}
	}
	return m.indexes[key]
}

func (m *tokenHashMemory) row(rowID graphTokenRowID) *graphTokenRow {
	if m == nil || rowID < 0 {
		return nil
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return nil
	}
	return &m.rows[index]
}

func (m *tokenHashMemory) insert(token tokenRef, joinKey betaJoinKey) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
	}
	if m.indexes == nil {
		m.indexes = make(map[betaJoinKey]graphTokenRowIDBucket)
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket)
	}

	identity := tokenRefKey(token)
	bucket := m.identityRows[identity]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		row := m.row(rowID)
		if row != nil && tokenRefEqual(row.token, token) {
			return false
		}
	}

	rowID := graphTokenRowID(len(m.rows))
	m.rows = append(m.rows, graphTokenRow{
		id:       rowID,
		token:    token,
		joinKey:  joinKey,
		identity: identity,
	})
	joinBucket := m.indexes[joinKey]
	joinBucket.append(rowID)
	m.indexes[joinKey] = joinBucket
	identityBucket := m.identityRows[identity]
	identityBucket.append(rowID)
	m.identityRows[identity] = identityBucket
	m.indexTokenFacts(token, rowID)
	return true
}

func (m *tokenHashMemory) insertTerminal(token tokenRef) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.identityRows == nil {
		m.identityRows = make(map[graphTokenIdentityKey]graphTokenRowIDBucket)
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket)
	}

	identity := tokenRefKey(token)
	bucket := m.identityRows[identity]
	for i := 0; i < bucket.len(); i++ {
		rowID, _ := bucket.at(i)
		row := m.row(rowID)
		if row != nil && tokenRefEqual(row.token, token) {
			return false
		}
	}

	rowID := graphTokenRowID(len(m.rows))
	m.rows = append(m.rows, graphTokenRow{
		id:       rowID,
		token:    token,
		identity: identity,
	})
	identityBucket := m.identityRows[identity]
	identityBucket.append(rowID)
	m.identityRows[identity] = identityBucket
	m.indexTokenFacts(token, rowID)
	return true
}

func (m *tokenHashMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows[id]
		if !ok || bucket.len() == 0 {
			return removed
		}
		rowID, ok := bucket.at(bucket.len() - 1)
		if !ok {
			return removed
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		m.removeRow(rowID, counters)
		removed++
	}
}

func (m *tokenHashMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(tokenRef)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows[id]
		if !ok || bucket.len() == 0 {
			return removed
		}
		rowID, ok := bucket.at(bucket.len() - 1)
		if !ok {
			return removed
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		row := m.row(rowID)
		if row != nil && !row.token.isZero() && row.token.containsFact(id) {
			fn(row.token)
		}
		m.removeRow(rowID, counters)
		removed++
	}
}

func (m *tokenHashMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(tokenRef)) {
	if m == nil || id.IsZero() {
		return
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket, ok := m.factRows[id]
	if !ok || bucket.len() == 0 {
		return
	}
	var previous graphTokenRowID
	havePrevious := false
	for i := 0; i < bucket.len(); i++ {
		rowID, ok := bucket.at(i)
		if !ok || (havePrevious && rowID == previous) {
			continue
		}
		havePrevious = true
		previous = rowID
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		row := m.row(rowID)
		if row == nil || row.token.isZero() || !row.token.containsFact(id) {
			continue
		}
		fn(row.token)
	}
}

func (m *tokenHashMemory) removeRow(rowID graphTokenRowID, counters *propagationCounterLedger) {
	if m == nil || rowID < 0 {
		return
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return
	}
	removed := m.rows[index]
	if bucket, ok := m.indexes[removed.joinKey]; ok {
		if bucket.remove(rowID) {
			if bucket.len() == 0 {
				delete(m.indexes, removed.joinKey)
			} else {
				m.indexes[removed.joinKey] = bucket
			}
		}
	}
	if bucket, ok := m.identityRows[removed.identity]; ok {
		if bucket.remove(rowID) {
			if bucket.len() == 0 {
				delete(m.identityRows, removed.identity)
			} else {
				m.identityRows[removed.identity] = bucket
			}
		}
	}
	m.removeTokenFacts(removed.token, rowID)
	last := len(m.rows) - 1
	if index != last {
		moved := m.rows[last]
		moved.id = rowID
		m.rows[index] = moved
		if bucket, ok := m.indexes[moved.joinKey]; ok {
			if bucket.replace(graphTokenRowID(last), rowID) {
				m.indexes[moved.joinKey] = bucket
			}
		}
		if bucket, ok := m.identityRows[moved.identity]; ok {
			if bucket.replace(graphTokenRowID(last), rowID) {
				m.identityRows[moved.identity] = bucket
			}
		}
		m.replaceTokenFactRows(moved.token, graphTokenRowID(last), rowID)
	}
	m.rows[last] = graphTokenRow{}
	m.rows = m.rows[:last]
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m *tokenHashMemory) indexTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || token.isZero() {
		return
	}
	if m.factRows == nil {
		m.factRows = make(map[FactID]graphTokenRowIDBucket)
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		id := row.match.fact.ID()
		if id.IsZero() {
			continue
		}
		bucket := m.factRows[id]
		bucket.append(rowID)
		m.factRows[id] = bucket
	}
}

func (m *tokenHashMemory) removeTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || len(m.factRows) == 0 || token.isZero() {
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		id := row.match.fact.ID()
		if id.IsZero() {
			continue
		}
		bucket, ok := m.factRows[id]
		if !ok || !bucket.remove(rowID) {
			continue
		}
		if bucket.len() == 0 {
			delete(m.factRows, id)
		} else {
			m.factRows[id] = bucket
		}
	}
}

func (m *tokenHashMemory) replaceTokenFactRows(token tokenRef, oldID, newID graphTokenRowID) {
	if m == nil || len(m.factRows) == 0 || token.isZero() {
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		id := row.match.fact.ID()
		if id.IsZero() {
			continue
		}
		bucket, ok := m.factRows[id]
		if ok && bucket.replace(oldID, newID) {
			m.factRows[id] = bucket
		}
	}
}

func (m *reteGraphBetaMemory) resetFacts(facts []FactSnapshot) {
	if m == nil || m.graph == nil {
		return
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	} else {
		m.arena.reset()
	}
	m.clearMemories()
	for _, fact := range facts {
		m.insertFact(fact, nil)
	}
}

func (m *reteGraphBetaMemory) clearMemories() {
	if m == nil {
		return
	}
	for _, node := range m.nodes {
		if node != nil {
			node.left.clear()
			node.right.clear()
		}
	}
	for _, terminal := range m.terminals {
		if terminal != nil {
			terminal.rows.clear()
		}
	}
	for conditionID, facts := range m.alphaFacts {
		clear(facts)
		m.alphaFacts[conditionID] = facts
	}
	clear(m.terminalTokenDeltas)
	m.terminalTokenDeltas = m.terminalTokenDeltas[:0]
}

func (m *reteGraphBetaMemory) insertFact(fact FactSnapshot, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	templateKey := fact.TemplateKey()
	nodeIDs, routed := m.graph.routesByTemplateKey[templateKey]
	if !routed || len(nodeIDs) == 0 {
		return reteAgendaDelta{}
	}

	delta := reteAgendaDelta{supported: true}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if span != nil {
			span.recordConditionsTested()
		}
		if !node.matchesSnapshot(fact) {
			continue
		}
		if span != nil {
			span.recordAlphaMatchAdded()
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromSnapshot(fact),
		}
		if !m.insertAlphaMatch(nodeID, match, span, &delta) {
			delta.supported = false
		}
	}
	return delta
}

func (m *reteGraphBetaMemory) insertFactGenerated(fact *workingFact, span *propagationCounterSpan) reteAgendaDelta {
	if m == nil || m.graph == nil || fact == nil {
		return reteAgendaDelta{}
	}
	templateKey := fact.templateKey
	nodeIDs, routed := m.graph.routesByTemplateKey[templateKey]
	if !routed || len(nodeIDs) == 0 {
		return reteAgendaDelta{}
	}

	delta := reteAgendaDelta{supported: true}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if span != nil {
			span.recordConditionsTested()
		}
		if !node.matchesWorking(fact) {
			continue
		}
		if span != nil {
			span.recordAlphaMatchAdded()
		}
		match := conditionMatch{
			conditionID: node.entry.conditionID,
			bindingSlot: node.entry.bindingSlot,
			fact:        newConditionFactRefFromWorkingFact(fact),
		}
		if !m.insertAlphaMatchGenerated(nodeID, match, span, &delta) {
			delta.supported = false
		}
	}
	return delta
}

func (m *reteGraphBetaMemory) insertAlphaMatch(nodeID reteGraphAlphaNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil {
		return false
	}
	node := m.graph.alphaNode(nodeID)
	if node == nil {
		return false
	}
	m.propagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, span, delta)
	return true
}

func (m *reteGraphBetaMemory) insertAlphaMatchGenerated(nodeID reteGraphAlphaNodeID, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil {
		return false
	}
	node := m.graph.alphaNode(nodeID)
	if node == nil {
		return false
	}
	m.propagateAlphaStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, node.entry, match, span, delta)
	return true
}

func (m *reteGraphBetaMemory) propagateAlphaStage(source reteGraphStageRef, sourceEntry bindingTupleEntry, match conditionMatch, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		entry := terminal.entry
		if entry.conditionID == "" {
			entry = sourceEntry
		}
		if entry.conditionID == "" {
			delta.supported = false
			continue
		}
		m.recordAlphaFact(entry, match.fact)
		token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), span)
		if token.isZero() {
			delta.supported = false
			continue
		}
		m.insertTerminalToken(terminal.terminalID, token, delta, span)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		switch successor.side {
		case reteGraphBetaInputLeft:
			entry := successor.entry
			if entry.conditionID == "" {
				entry = sourceEntry
			}
			if entry.conditionID == "" {
				delta.supported = false
				continue
			}
			m.recordAlphaFact(entry, match.fact)
			token := m.newTokenRef(tokenRef{}, entry, match, match.fact.Recency(), match.fact.Generation(), span)
			if token.isZero() || !m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta) {
				delta.supported = false
			}
		case reteGraphBetaInputRight:
			m.recordAlphaFact(successor.entry, match.fact)
			edgeMatch := conditionMatch{
				conditionID: successor.entry.conditionID,
				bindingSlot: successor.entry.bindingSlot,
				fact:        match.fact,
			}
			token := m.newTokenRef(tokenRef{}, successor.entry, edgeMatch, match.fact.Recency(), match.fact.Generation(), span)
			if token.isZero() || !m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta) {
				delta.supported = false
			}
		default:
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) propagateFromStage(source reteGraphStageRef, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		m.insertTerminalToken(terminal.terminalID, token, delta, span)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		node := m.graph.betaNode(successor.betaNodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if !m.insertBetaInput(successor.betaNodeID, successor.side, token, node.entry, span, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) propagateRemoveFactFromStage(source reteGraphStageRef, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil {
		return
	}
	for _, terminal := range m.graph.terminalsByStage[source] {
		m.removeTerminalTokensContainingFact(terminal.terminalID, id, counters, delta)
	}
	for _, successor := range m.graph.successorsByStage[source] {
		if !m.removeBetaInputContainingFact(successor.betaNodeID, successor.side, id, counters, delta) {
			delta.supported = false
		}
	}
}

func (m *reteGraphBetaMemory) insertBetaInput(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, token tokenRef, entry bindingTupleEntry, span *propagationCounterSpan, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || token.isZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	var inserted bool
	var joinKey betaJoinKey
	var ok bool
	switch side {
	case reteGraphBetaInputLeft:
		joinKey, ok = graphBetaJoinKeyForLeftToken(node, token)
		if !ok {
			return false
		}
		inserted = nodeMemory.left.insert(token, joinKey)
	case reteGraphBetaInputRight:
		joinKey, ok = graphBetaJoinKeyForRightToken(node, token)
		if !ok {
			return false
		}
		inserted = nodeMemory.right.insert(token, joinKey)
	default:
		return false
	}
	if !inserted {
		return true
	}
	if span != nil {
		span.recordBetaInputInsert(side)
		span.recordBetaBucketProbe()
	}
	switch side {
	case reteGraphBetaInputLeft:
		bucket := nodeMemory.right.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			rightRow := nodeMemory.right.row(rowID)
			if rightRow == nil || rightRow.token.isZero() {
				continue
			}
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, rightMatch.fact, token, span); err != nil {
				return false
			} else if !ok {
				continue
			}
			output := m.newTokenRef(token, entry, rightMatch, rightMatch.fact.Recency(), rightMatch.fact.Generation(), span)
			if output.isZero() {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta)
		}
	case reteGraphBetaInputRight:
		currentMatch, ok := tokenLastMatch(token)
		if !ok {
			return false
		}
		bucket := nodeMemory.left.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			if span != nil {
				span.recordBetaCandidateRowScanned()
			}
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			if ok, err := m.residualJoinsMatch(node, currentMatch.fact, leftRow.token, span); err != nil {
				return false
			} else if !ok {
				continue
			}
			output := m.newTokenRef(leftRow.token, entry, currentMatch, currentMatch.fact.Recency(), currentMatch.fact.Generation(), span)
			if output.isZero() {
				continue
			}
			if span != nil {
				span.recordBetaJoinedTokenProduced()
			}
			m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta)
		}
	}
	return true
}

func (m *reteGraphBetaMemory) removeBetaInputContainingFact(nodeID reteGraphBetaNodeID, side reteGraphBetaInputSide, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m == nil || delta == nil || id.IsZero() {
		return false
	}
	node := m.graph.betaNode(nodeID)
	if node == nil {
		return false
	}
	nodeMemory := m.nodeMemory(nodeID)
	var removed int
	switch side {
	case reteGraphBetaInputLeft:
		removed = nodeMemory.left.removeContainingFact(id, counters)
	case reteGraphBetaInputRight:
		removed = nodeMemory.right.removeContainingFact(id, counters)
	default:
		return false
	}
	if removed > 0 {
		m.propagateRemoveFactFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, id, counters, delta)
	}
	return true
}

func (m *reteGraphBetaMemory) removeFact(fact FactSnapshot, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
	id := fact.ID()
	templateKey := fact.TemplateKey()
	nodeIDs, routed := m.graph.routesByTemplateKey[templateKey]
	if !routed || len(nodeIDs) == 0 {
		m.removeAlphaFact(id)
		return delta
	}
	for _, nodeID := range nodeIDs {
		node := m.graph.alphaNode(nodeID)
		if node == nil {
			delta.supported = false
			continue
		}
		if !node.matchesSnapshot(fact) {
			continue
		}
		m.propagateRemoveFactFromStage(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(nodeID)}, id, counters, &delta)
	}
	m.removeAlphaFact(id)
	return delta
}

func (m *reteGraphBetaMemory) removeFactByIndexes(id FactID, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil || m.graph == nil {
		return reteAgendaDelta{}
	}
	delta := reteAgendaDelta{supported: true}
	m.removeAlphaFact(id)
	for _, terminalNode := range m.graph.terminalNodes {
		terminal := m.terminals[terminalNode.id]
		if terminal == nil {
			continue
		}
		terminal.rows.forEachTokenContainingFact(id, counters, func(token tokenRef) {
			delta.removed = append(delta.removed, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          token,
				identityKey:    m.terminalTokenIdentityKey(terminalNode.ruleRevisionID, token),
			})
			if counters != nil {
				counters.recordTerminalDeltaRemoved()
			}
		})
		terminal.rows.removeContainingFact(id, counters)
	}
	for _, node := range m.nodes {
		if node == nil {
			continue
		}
		node.left.removeContainingFact(id, counters)
		node.right.removeContainingFact(id, counters)
	}
	return delta
}

func (m *reteGraphBetaMemory) recordAlphaFact(entry bindingTupleEntry, fact conditionFactRef) {
	if m == nil || entry.conditionID == "" || fact.ID().IsZero() {
		return
	}
	if m.alphaFacts == nil {
		m.alphaFacts = make(map[ConditionID]map[FactID]struct{})
	}
	facts := m.alphaFacts[entry.conditionID]
	if facts == nil {
		facts = make(map[FactID]struct{})
		m.alphaFacts[entry.conditionID] = facts
	}
	if _, ok := facts[fact.ID()]; ok {
		return
	}
	facts[fact.ID()] = struct{}{}
}

func (m *reteGraphBetaMemory) removeAlphaFact(id FactID) {
	if m == nil || id.IsZero() {
		return
	}
	for conditionID, facts := range m.alphaFacts {
		delete(facts, id)
		if len(facts) == 0 {
			delete(m.alphaFacts, conditionID)
		}
	}
}

func (m *reteGraphBetaMemory) alphaFactCount(conditionID ConditionID) int {
	if m == nil || conditionID == "" {
		return 0
	}
	return len(m.alphaFacts[conditionID])
}

func (m *reteGraphBetaMemory) updateFact(before, after FactSnapshot, counters *propagationCounterLedger) reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	removed := m.removeFact(before, counters)
	added := m.insertFact(after, nil)
	return reteAgendaDelta{
		supported: removed.supported && added.supported,
		added:     added.added,
		removed:   removed.removed,
	}
}

func (m *reteGraphBetaMemory) insertTerminalToken(terminalID reteGraphTerminalNodeID, token tokenRef, delta *reteAgendaDelta, span *propagationCounterSpan) {
	if m == nil || delta == nil || token.isZero() {
		return
	}
	terminal := m.terminal(terminalID)
	if terminal == nil {
		delta.supported = false
		return
	}
	if !terminal.rows.insertTerminal(token) {
		return
	}
	if span != nil {
		span.recordTerminalDeltaEmitted()
	}
	ruleRevisionID := m.terminalRuleRevision(terminalID)
	delta.added = append(delta.added, reteTerminalTokenDelta{
		ruleRevisionID: ruleRevisionID,
		token:          token,
		identityKey:    m.terminalTokenIdentityKey(ruleRevisionID, token),
	})
}

func (m *reteGraphBetaMemory) removeTerminalTokensContainingFact(terminalID reteGraphTerminalNodeID, id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) {
	if m == nil || delta == nil || id.IsZero() {
		return
	}
	terminal := m.terminals[terminalID]
	if terminal == nil {
		return
	}
	ruleRevisionID := m.terminalRuleRevision(terminalID)
	terminal.rows.removeTokensContainingFact(id, counters, func(token tokenRef) {
		delta.removed = append(delta.removed, reteTerminalTokenDelta{
			ruleRevisionID: ruleRevisionID,
			token:          token,
			identityKey:    m.terminalTokenIdentityKey(ruleRevisionID, token),
		})
		if counters != nil {
			counters.recordTerminalDeltaRemoved()
		}
	})
}

func (m *reteGraphBetaMemory) currentTerminalTokenDeltas(ctx context.Context) ([]reteTerminalTokenDelta, bool, error) {
	if m == nil || m.graph == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	deltas := m.terminalTokenDeltas[:0]
	for _, terminalNode := range m.graph.terminalNodes {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		terminal := m.terminals[terminalNode.id]
		if terminal == nil {
			continue
		}
		for _, row := range terminal.rows.rows {
			if row.token.isZero() {
				continue
			}
			deltas = append(deltas, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          row.token,
				identityKey:    m.terminalTokenIdentityKey(terminalNode.ruleRevisionID, row.token),
			})
		}
	}
	m.terminalTokenDeltas = deltas
	return deltas, true, nil
}

func (m *reteGraphBetaMemory) terminalTokenIdentityKey(ruleRevisionID RuleRevisionID, token tokenRef) candidateIdentityKey {
	if m == nil || m.revision == nil || token.isZero() {
		return candidateIdentityKey{}
	}
	rule, ok := m.revision.rulesByRevisionID[ruleRevisionID]
	if !ok {
		return candidateIdentityKey{}
	}
	return candidateIdentityForTerminalToken(rule, token).key
}

func (m *reteGraphBetaMemory) newTokenRef(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation, span *propagationCounterSpan) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	if span != nil {
		span.recordTokenCreated()
	}
	if m.arena == nil {
		m.arena = newTokenArena()
	}
	return m.arena.add(parent, entry, match, recency, generation)
}

func (m *reteGraphBetaMemory) nodeMemory(id reteGraphBetaNodeID) *reteGraphBetaNodeMemory {
	if m == nil {
		return nil
	}
	node := m.nodes[id]
	if node != nil {
		return node
	}
	node = &reteGraphBetaNodeMemory{}
	if m.nodes == nil {
		m.nodes = make(map[reteGraphBetaNodeID]*reteGraphBetaNodeMemory)
	}
	m.nodes[id] = node
	return node
}

func (m *reteGraphBetaMemory) terminal(id reteGraphTerminalNodeID) *reteGraphTerminalMemory {
	if m == nil {
		return nil
	}
	terminal := m.terminals[id]
	if terminal != nil {
		return terminal
	}
	terminal = &reteGraphTerminalMemory{}
	if m.terminals == nil {
		m.terminals = make(map[reteGraphTerminalNodeID]*reteGraphTerminalMemory)
	}
	m.terminals[id] = terminal
	return terminal
}

func (m *reteGraphBetaMemory) terminalRuleRevision(id reteGraphTerminalNodeID) RuleRevisionID {
	if m == nil || m.graph == nil || id <= 0 {
		return ""
	}
	index := int(id) - 1
	if index < 0 || index >= len(m.graph.terminalNodes) {
		return ""
	}
	return m.graph.terminalNodes[index].ruleRevisionID
}

func tokenRefKey(token tokenRef) graphTokenIdentityKey {
	return graphTokenIdentityKey{
		size:          token.size(),
		generation:    token.generation(),
		identityState: token.identityState(),
	}
}

func tokenLastMatch(token tokenRef) (conditionMatch, bool) {
	row, ok := token.resolve()
	if !ok {
		return conditionMatch{}, false
	}
	return row.match, true
}

func graphBetaJoinKeyForLeftToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	if node == nil || len(node.hashJoins) == 0 && len(node.joins) > 0 {
		return betaJoinKey{}, false
	}
	return betaJoinKeyForPlan(compiledConditionPlan{joins: node.hashJoins}, func(join compiledJoinConstraint) (Value, bool) {
		match, ok := tokenRefAtSlot(token, join.refBindingSlot)
		if !ok {
			return Value{}, false
		}
		return match.fact.compiledFieldValue(join.refField, join.refFieldSlot)
	})
}

func graphBetaJoinKeyForRightToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	if node == nil || len(node.hashJoins) == 0 && len(node.joins) > 0 {
		return betaJoinKey{}, false
	}
	match, ok := tokenLastMatch(token)
	if !ok {
		return betaJoinKey{}, false
	}
	return betaJoinKeyForPlan(compiledConditionPlan{joins: node.hashJoins}, func(join compiledJoinConstraint) (Value, bool) {
		return match.fact.compiledFieldValue(join.field, join.fieldSlot)
	})
}

func (m *reteGraphBetaMemory) residualJoinsMatch(node *reteGraphBetaNode, fact conditionFactRef, bindings tokenRef, span *propagationCounterSpan) (bool, error) {
	if m == nil || node == nil || len(node.residualJoins) == 0 {
		return true, nil
	}
	for _, join := range node.residualJoins {
		if span != nil {
			span.recordBetaResidualTest()
		}
		ok, err := join.matchesToken(fact, bindings)
		if err != nil {
			return false, err
		}
		if !ok {
			if span != nil {
				span.recordBetaResidualFailure()
			}
			return false, nil
		}
	}
	return true, nil
}

func (m *reteGraphBetaMemory) rowCount() int {
	if m == nil {
		return 0
	}
	total := 0
	for _, node := range m.nodes {
		if node != nil {
			total += len(node.left.rows)
			total += len(node.right.rows)
		}
	}
	for _, terminal := range m.terminals {
		if terminal != nil {
			total += len(terminal.rows.rows)
		}
	}
	return total
}

func (m *reteGraphBetaMemory) match(ctx context.Context, source factSource, alphaSource alphaFactSource) ([]ruleMatchResult, error) {
	if m == nil || m.revision == nil {
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
		terminal := m.terminalForRule(rule.revisionID)
		var candidates []matchCandidate
		if terminal != nil {
			var err error
			candidates, err = m.collectTerminalCandidates(ctx, rule, terminal)
			if err != nil {
				return nil, err
			}
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

func (m *reteGraphBetaMemory) matchWithoutSnapshot(ctx context.Context, generation Generation) ([]ruleMatchResult, bool, error) {
	results, err := m.match(ctx, nil, nil)
	if err != nil {
		return nil, false, err
	}
	return results, true, nil
}

func (m *reteGraphBetaMemory) collectTerminalCandidates(ctx context.Context, rule compiledRule, terminal *reteGraphTerminalMemory) ([]matchCandidate, error) {
	if terminal == nil || terminal.rows.len() == 0 {
		return nil, nil
	}
	candidates := make([]matchCandidate, 0, terminal.rows.len())
	seen := newCandidateSeenSet(terminal.rows.len())
	for _, row := range terminal.rows.rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.token.isZero() {
			continue
		}
		candidate, err := buildMatchCandidateFromTokenRef(rule, row.token)
		if err != nil {
			return nil, err
		}
		if seen.seen(candidates, candidate) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sortMatchCandidates(nil, candidates)
	return candidates, nil
}

func (m *reteGraphBetaMemory) terminalForRule(ruleRevisionID RuleRevisionID) *reteGraphTerminalMemory {
	if m == nil || m.graph == nil {
		return nil
	}
	for _, terminalNode := range m.graph.terminalNodes {
		if terminalNode.ruleRevisionID != ruleRevisionID {
			continue
		}
		return m.terminals[terminalNode.id]
	}
	return nil
}
