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
	return true
}

func (m *tokenHashMemory) removeContainingFact(id FactID) {
	if m == nil {
		return
	}
	for i := len(m.rows) - 1; i >= 0; i-- {
		row := m.rows[i]
		if row.token.isZero() || !row.token.containsFact(id) {
			continue
		}
		m.removeRow(graphTokenRowID(i))
	}
}

func (m *tokenHashMemory) removeRow(rowID graphTokenRowID) {
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
	}
	m.rows[last] = graphTokenRow{}
	m.rows = m.rows[:last]
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
	switch side {
	case reteGraphBetaInputLeft:
		bucket := nodeMemory.right.bucketForKey(joinKey)
		for i := 0; i < bucket.len(); i++ {
			rowID, _ := bucket.at(i)
			rightRow := nodeMemory.right.row(rowID)
			if rightRow == nil || rightRow.token.isZero() {
				continue
			}
			rightMatch, ok := tokenLastMatch(rightRow.token)
			if !ok {
				continue
			}
			output := m.newTokenRef(token, entry, rightMatch, rightMatch.fact.Recency(), rightMatch.fact.Generation(), span)
			if output.isZero() {
				continue
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
			leftRow := nodeMemory.left.row(rowID)
			if leftRow == nil || leftRow.token.isZero() {
				continue
			}
			output := m.newTokenRef(leftRow.token, entry, currentMatch, currentMatch.fact.Recency(), currentMatch.fact.Generation(), span)
			if output.isZero() {
				continue
			}
			m.propagateFromStage(reteGraphStageRef{kind: reteGraphStageBeta, id: int(nodeID)}, output, span, delta)
		}
	}
	return true
}

func (m *reteGraphBetaMemory) removeFact(id FactID) reteAgendaDelta {
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
		for _, row := range terminal.rows.rows {
			if row.token.isZero() || !row.token.containsFact(id) {
				continue
			}
			delta.removed = append(delta.removed, reteTerminalTokenDelta{
				ruleRevisionID: terminalNode.ruleRevisionID,
				token:          row.token,
			})
		}
		terminal.rows.removeContainingFact(id)
	}
	for _, node := range m.nodes {
		if node == nil {
			continue
		}
		node.left.removeContainingFact(id)
		node.right.removeContainingFact(id)
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

func (m *reteGraphBetaMemory) updateFact(before, after FactSnapshot) reteAgendaDelta {
	if m == nil {
		return reteAgendaDelta{}
	}
	removed := m.removeFact(before.ID())
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
	if !terminal.rows.insert(token, betaJoinKey{}) {
		return
	}
	if span != nil {
		span.recordTerminalDeltaEmitted()
	}
	delta.added = append(delta.added, reteTerminalTokenDelta{
		ruleRevisionID: m.terminalRuleRevision(terminalID),
		token:          token,
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
			})
		}
	}
	m.terminalTokenDeltas = deltas
	return deltas, true, nil
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
	return betaJoinKeyForPlan(compiledConditionPlan{joins: node.joins}, func(join compiledJoinConstraint) (Value, bool) {
		match, ok := tokenRefAtSlot(token, join.refBindingSlot)
		if !ok {
			return Value{}, false
		}
		return match.fact.compiledFieldValue(join.refField, join.refFieldSlot)
	})
}

func graphBetaJoinKeyForRightToken(node *reteGraphBetaNode, token tokenRef) (betaJoinKey, bool) {
	match, ok := tokenLastMatch(token)
	if !ok {
		return betaJoinKey{}, false
	}
	return betaJoinKeyForPlan(compiledConditionPlan{joins: node.joins}, func(join compiledJoinConstraint) (Value, bool) {
		return match.fact.compiledFieldValue(join.field, join.fieldSlot)
	})
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
