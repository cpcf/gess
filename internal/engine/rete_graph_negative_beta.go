package engine

type reteGraphNegativeBetaMemory struct {
	owner  *reteGraphBetaMemory
	id     reteGraphBetaNodeID
	node   *reteGraphBetaNode
	memory *reteGraphNegativeBetaNodeMemory
}

type reteGraphNegativeBetaNodeMemory struct {
	left  negativeBetaLeftMemory
	right betaSideMemory
}

type negativeBetaLeftMemory struct {
	indexes          negativeBetaLeftBucketTable
	rowReserve       int
	joinIndexReserve int
}

type negativeBetaLeftRow struct {
	token        tokenRef
	joinKey      betaJoinKey
	blockerCount int
	output       tokenRef
}

// negativeBetaLeftBucketTable mirrors betaJoinBucketTable: rows live in one
// shared arena with per-slot chain links, free rows are recycled through
// freeHead, and byIdentity indexes live rows by token identity hash on the
// first token removal. Unlinked rows release both the input token and the
// negative output token.
type negativeBetaLeftBucketTable struct {
	heads      []int32
	tails      []int32
	rows       []negativeBetaLeftRow
	next       []int32
	prev       []int32
	byIdentity map[uint64][]int32
	freeHead   int32
	touched    []int
	slotCount  int
	rowCount   int
	id         uint32
}

func (t *negativeBetaLeftBucketTable) holderID() uint32 {
	if t.id == 0 {
		t.id = nextTokenHolderTableID()
	}
	return t.id
}

func (m *reteGraphBetaMemory) negativeBetaMemory(nodeID reteGraphBetaNodeID, node *reteGraphBetaNode) reteGraphNegativeBetaMemory {
	if m == nil || node == nil {
		return reteGraphNegativeBetaMemory{}
	}
	nodeMemory := m.nodeMemory(nodeID)
	if nodeMemory == nil {
		return reteGraphNegativeBetaMemory{}
	}
	return reteGraphNegativeBetaMemory{
		owner:  m,
		id:     nodeID,
		node:   node,
		memory: &nodeMemory.negative,
	}
}

func (m *reteGraphNegativeBetaNodeMemory) clear() {
	if m == nil {
		return
	}
	m.left.clear()
	m.right.clear()
}

func (m reteGraphNegativeBetaMemory) insertLeft(joinKey betaJoinKey, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || token.isZero() {
		return false, nil
	}
	count, ok, err := m.blockerCountForLeft(joinKey, token, span)
	if err != nil || !ok {
		return false, err
	}
	row, inserted := m.memory.left.insert(token, joinKey, count)
	if !inserted {
		return true, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputLeft)
	}
	if m.owner.propagationCounters != nil {
		m.owner.propagationCounters.recordNegativeBlockersInitialized(count)
	}
	if count != 0 {
		return true, nil
	}
	return m.emitLeftAdd(row, span, delta)
}

func (m reteGraphNegativeBetaMemory) insertRight(joinKey betaJoinKey, token tokenRef, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || token.isZero() {
		return false, nil
	}
	_, inserted := m.memory.right.insert(token, joinKey)
	if !inserted {
		return true, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputRight)
	}
	currentMatch, ok := m.rightLastMatch(token)
	if !ok {
		return false, nil
	}
	depth := m.memory.left.joinRowCount(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(depth)
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	var joinErr error
	m.memory.left.forEachJoinRow(joinKey, func(leftRow *negativeBetaLeftRow) bool {
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		matched, err := m.leftRightMatch(leftRow, token, currentMatch, span)
		if err != nil {
			joinErr = err
			return false
		}
		if !matched {
			return true
		}
		wasZero := leftRow.blockerCount == 0
		leftRow.blockerCount++
		if m.owner.propagationCounters != nil {
			m.owner.propagationCounters.recordNegativeBlockerIncrement(wasZero)
		}
		if leftRow.blockerCount == 1 && !leftRow.output.isZero() {
			m.owner.propagateRemoveFromStage(source, leftRow.output, nil, delta)
			leftRow.output = tokenRef{}
		}
		return true
	})
	if joinErr != nil {
		return false, joinErr
	}
	return true, nil
}

func (m reteGraphNegativeBetaMemory) removeLeftToken(token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.memory == nil || delta == nil || token.isZero() {
		return false
	}
	removedRow, removedOK := m.memory.left.removeToken(token, counters)
	if !removedOK {
		return true
	}
	if counters != nil {
		counters.recordNegativeRowRemoved()
		counters.recordNegativeBetaRowRemoved()
	}
	if removedRow.output.isZero() {
		return true
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	m.owner.propagateRemoveFromStage(source, removedRow.output, counters, delta)
	return true
}

func (m reteGraphNegativeBetaMemory) removeRightToken(token tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || token.isZero() {
		return false
	}
	removedRow, removedOK := m.memory.right.removeToken(token, counters)
	if !removedOK {
		return true
	}
	if counters != nil {
		counters.recordNegativeRowRemoved()
		counters.recordNegativeBetaRowRemoved()
	}
	return m.unblockLeftRows(removedRow.joinKey, removedRow.token, counters, delta)
}

func (m reteGraphNegativeBetaMemory) unblockLeftRows(joinKey betaJoinKey, right tokenRef, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	currentMatch, ok := m.rightLastMatch(right)
	if !ok {
		return false
	}
	m.memory.left.forEachJoinRow(joinKey, func(leftRow *negativeBetaLeftRow) bool {
		matched, err := m.leftRightMatch(leftRow, right, currentMatch, nil)
		if err != nil {
			delta.supported = false
			return false
		}
		if !matched {
			return true
		}
		if leftRow.blockerCount <= 0 {
			delta.supported = false
			return true
		}
		wasOne := leftRow.blockerCount == 1
		leftRow.blockerCount--
		if counters != nil {
			counters.recordNegativeBlockerDecrement(wasOne)
		}
		if leftRow.blockerCount == 0 {
			if ok, err := m.emitLeftAdd(leftRow, nil, delta); err != nil {
				delta.supported = false
				return false
			} else if !ok {
				delta.supported = false
			}
		} else if leftRow.blockerCount < 0 {
			delta.supported = false
			return false
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
	m.memory.left.removeTokensContainingFact(id, counters, func(row negativeBetaLeftRow) {
		if counters != nil {
			counters.recordNegativeBetaRowRemoved()
		}
		if !row.output.isZero() {
			m.owner.propagateRemoveFromStage(source, row.output, counters, delta)
		}
	})
	return true
}

func (m reteGraphNegativeBetaMemory) removeRightContainingFact(id FactID, counters *propagationCounterLedger, delta *reteAgendaDelta) bool {
	if m.owner == nil || m.node == nil || m.memory == nil || delta == nil || id.IsZero() {
		return false
	}
	var removedRows []betaTokenRow
	m.memory.right.removeTokensContainingFact(id, counters, func(row betaTokenRow) {
		removedRows = append(removedRows, row)
	})
	for _, row := range removedRows {
		if counters != nil {
			counters.recordNegativeRowRemoved()
			counters.recordNegativeBetaRowRemoved()
		}
		if !m.unblockLeftRows(row.joinKey, row.token, counters, delta) {
			return false
		}
	}
	return true
}

func (m reteGraphNegativeBetaMemory) blockerCountForLeft(joinKey betaJoinKey, left tokenRef, span *propagationCounterSpan) (int, bool, error) {
	if m.owner == nil || m.node == nil || m.memory == nil || left.isZero() {
		return 0, false, nil
	}
	depth := m.memory.right.joinRowCount(joinKey)
	if span != nil {
		span.recordBetaBucketProbe(depth)
	}
	count := 0
	supported := true
	var matchErr error
	m.memory.right.forEachJoinRow(joinKey, func(_ graphTokenRowID, rightRow *betaTokenRow) bool {
		if span != nil {
			span.recordBetaCandidateRowScanned()
		}
		rightMatch, ok := m.rightLastMatch(rightRow.token)
		if !ok {
			supported = false
			return false
		}
		matched, err := m.leftRightMatchToken(left, rightRow.token, rightMatch, span)
		if err != nil {
			matchErr = err
			return false
		}
		if matched {
			count++
		}
		return true
	})
	return count, supported, matchErr
}

func (m reteGraphNegativeBetaMemory) emitLeftAdd(row *negativeBetaLeftRow, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m.owner == nil || row == nil || delta == nil || row.token.isZero() {
		return false, nil
	}
	if row.output.isZero() {
		row.output = m.owner.newNegativeOutputTokenRef(reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}, row.token, span)
	}
	if row.output.isZero() {
		return false, nil
	}
	if span != nil {
		span.recordBetaJoinedTokenProduced()
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	if err := m.owner.propagateFromStage(source, row.output, span, delta); err != nil {
		return false, err
	}
	return true, nil
}

func (m reteGraphNegativeBetaMemory) leftRightMatch(leftRow *negativeBetaLeftRow, right tokenRef, rightMatch conditionMatch, span *propagationCounterSpan) (bool, error) {
	if leftRow == nil || leftRow.token.isZero() {
		return false, nil
	}
	return m.leftRightMatchToken(leftRow.token, right, rightMatch, span)
}

func (m reteGraphNegativeBetaMemory) leftRightMatchToken(left tokenRef, right tokenRef, rightMatch conditionMatch, span *propagationCounterSpan) (bool, error) {
	if m.node == nil || left.isZero() || right.isZero() {
		return false, nil
	}
	if m.node.rightHasLeftPrefix && !tokenRefHasPrefix(right, left) {
		return false, nil
	}
	if len(m.node.residualJoins) != 0 || len(m.node.predicates) != 0 {
		ok, err := m.owner.residualJoinsMatch(m.node, &rightMatch.fact, left, span)
		if err != nil || !ok {
			return ok, err
		}
	}
	return m.owner.rightPredicatesMatch(m.node, rightMatch, left, span)
}

func (m reteGraphNegativeBetaMemory) rightLastMatch(token tokenRef) (conditionMatch, bool) {
	if m.node == nil || token.isZero() {
		return conditionMatch{}, false
	}
	if len(m.node.residualJoins) == 0 && len(m.node.predicates) == 0 && len(m.node.rightPredicates) == 0 {
		return conditionMatch{}, true
	}
	return tokenLastMatch(token)
}

func (m *negativeBetaLeftMemory) clear() {
	if m == nil {
		return
	}
	m.indexes.clear()
}

func (m *negativeBetaLeftMemory) len() int {
	if m == nil {
		return 0
	}
	return m.indexes.len()
}

func (m *negativeBetaLeftMemory) rowCapacity() int {
	if m == nil {
		return 0
	}
	return cap(m.indexes.rows)
}

func (m *negativeBetaLeftMemory) joinRowCount(key betaJoinKey) int {
	count := 0
	m.forEachJoinRow(key, func(*negativeBetaLeftRow) bool {
		count++
		return true
	})
	return count
}

func (m *negativeBetaLeftMemory) forEachJoinRow(key betaJoinKey, fn func(*negativeBetaLeftRow) bool) {
	if m == nil || fn == nil || m.indexes.isEmpty() {
		return
	}
	m.indexes.forEachChainRow(key, func(_ int32, row *negativeBetaLeftRow) bool {
		return fn(row)
	})
}

func (m *negativeBetaLeftMemory) forEachRow(fn func(*negativeBetaLeftRow) bool) {
	if m == nil || fn == nil {
		return
	}
	for rowIndex := range m.indexes.rows {
		row := &m.indexes.rows[rowIndex]
		if row.token.isZero() {
			continue
		}
		if !fn(row) {
			return
		}
	}
}

func (m *negativeBetaLeftMemory) insert(token tokenRef, joinKey betaJoinKey, blockerCount int) (*negativeBetaLeftRow, bool) {
	if m == nil || token.isZero() {
		return nil, false
	}
	ref, inserted := m.indexes.insert(negativeBetaLeftRow{
		token:        token,
		joinKey:      joinKey,
		blockerCount: blockerCount,
	})
	if !inserted {
		return nil, false
	}
	m.joinIndexReserve = max(m.joinIndexReserve, len(m.indexes.heads))
	m.rowReserve = max(m.rowReserve, m.indexes.len())
	return &m.indexes.rows[ref-1], true
}

func (m *negativeBetaLeftMemory) removeToken(token tokenRef, counters *propagationCounterLedger) (negativeBetaLeftRow, bool) {
	if m == nil || token.isZero() {
		return negativeBetaLeftRow{}, false
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	var onTouch func()
	if counters != nil {
		onTouch = counters.recordRemovalRowTouched
	}
	removed, ok := m.indexes.removeIdentityToken(token, onTouch)
	if !ok {
		return negativeBetaLeftRow{}, false
	}
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
	return removed, true
}

func (m *negativeBetaLeftMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(negativeBetaLeftRow)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	var onTouch func()
	if counters != nil {
		onTouch = counters.recordRemovalRowTouched
	}
	return m.indexes.removeMatching(func(row *negativeBetaLeftRow) bool {
		return row.token.containsFact(id)
	}, onTouch, func(row negativeBetaLeftRow) {
		if counters != nil {
			counters.recordRemovalRowRemoved()
		}
		if fn != nil {
			fn(row)
		}
	})
}

func (m negativeBetaLeftMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	diag := reteGraphTokenMemoryDiagnostics{
		Rows:          m.len(),
		JoinIndexKeys: m.indexes.keyCount(),
	}
	seen := make(map[betaJoinKey]struct{}, m.indexes.keyCount())
	m.forEachRow(func(row *negativeBetaLeftRow) bool {
		if row.token.isZero() {
			return true
		}
		if _, ok := seen[row.joinKey]; ok {
			return true
		}
		seen[row.joinKey] = struct{}{}
		depth := m.joinRowCount(row.joinKey)
		diag.JoinBucketDepthTotal += depth
		diag.JoinBucketDepthMax = max(diag.JoinBucketDepthMax, depth)
		return true
	})
	return diag
}

func (t *negativeBetaLeftBucketTable) ensureIdentityIndex() {
	if t.byIdentity != nil {
		return
	}
	t.byIdentity = make(map[uint64][]int32, t.rowCount)
	for i := range t.rows {
		if t.rows[i].token.isZero() {
			continue
		}
		state := t.rows[i].token.identityState()
		t.byIdentity[state] = append(t.byIdentity[state], int32(i+1))
	}
}

func (t *negativeBetaLeftBucketTable) indexIdentity(ref int32) {
	if t.byIdentity == nil {
		return
	}
	state := t.rows[ref-1].token.identityState()
	t.byIdentity[state] = append(t.byIdentity[state], ref)
}

func (t *negativeBetaLeftBucketTable) unindexIdentity(ref int32) {
	if t.byIdentity == nil {
		return
	}
	state := t.rows[ref-1].token.identityState()
	refs := t.byIdentity[state]
	for i, existing := range refs {
		if existing != ref {
			continue
		}
		refs[i] = refs[len(refs)-1]
		refs = refs[:len(refs)-1]
		if len(refs) == 0 {
			delete(t.byIdentity, state)
		} else {
			t.byIdentity[state] = refs
		}
		return
	}
}

// removeIdentityToken unlinks the first row structurally equal to token,
// using the identity index instead of scanning slot chains.
func (t *negativeBetaLeftBucketTable) removeIdentityToken(token tokenRef, onTouch func()) (negativeBetaLeftRow, bool) {
	if t == nil || token.isZero() {
		return negativeBetaLeftRow{}, false
	}
	tokenRow, ok := token.resolve()
	if !ok {
		return t.removeIdentityTokenScan(token, onTouch)
	}
	if ref := tokenRow.holderRefForTable(t.id); t.id != 0 && ref > 0 && int(ref) <= len(t.rows) {
		held := &t.rows[ref-1]
		if held.token.handle.row == tokenRow {
			if onTouch != nil {
				onTouch()
			}
			removed := *held
			t.unlink(ref)
			return removed, true
		}
	}
	t.ensureIdentityIndex()
	for _, ref := range t.byIdentity[token.identityState()] {
		row := &t.rows[ref-1]
		if row.token.isZero() {
			continue
		}
		if onTouch != nil {
			onTouch()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		removed := *row
		t.unlink(ref)
		return removed, true
	}
	return negativeBetaLeftRow{}, false
}

func (t *negativeBetaLeftBucketTable) removeIdentityTokenScan(token tokenRef, onTouch func()) (negativeBetaLeftRow, bool) {
	for i := range t.rows {
		row := &t.rows[i]
		if row.token.isZero() {
			continue
		}
		if onTouch != nil {
			onTouch()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		removed := *row
		t.unlink(int32(i + 1))
		return removed, true
	}
	return negativeBetaLeftRow{}, false
}

func (t *negativeBetaLeftBucketTable) reserve(capacity int) bool {
	if capacity <= 0 {
		return false
	}
	slotCapacity := graphTokenBucketSlotCapacity(capacity)
	if slotCapacity <= len(t.heads) {
		return false
	}
	t.heads = make([]int32, graphTokenBucketPowerOfTwo(max(8, slotCapacity)))
	t.tails = make([]int32, len(t.heads))
	t.touched = t.touched[:0]
	t.slotCount = 0
	t.freeHead = 0
	live := 0
	for i := range t.rows {
		if t.rows[i].token.isZero() {
			t.next[i] = t.freeHead
			t.freeHead = int32(i + 1)
			continue
		}
		t.chainRow(int32(i + 1))
		live++
	}
	t.rowCount = live
	return true
}

func (t *negativeBetaLeftBucketTable) chainRow(ref int32) {
	slot := t.slot(t.rows[ref-1].joinKey)
	t.next[ref-1] = 0
	if t.heads[slot] == 0 {
		t.touched = append(t.touched, slot)
		t.slotCount++
		t.heads[slot] = ref
		t.tails[slot] = ref
		t.prev[ref-1] = 0
		return
	}
	tail := t.tails[slot]
	t.next[tail-1] = ref
	t.prev[ref-1] = tail
	t.tails[slot] = ref
}

func (t *negativeBetaLeftBucketTable) insert(row negativeBetaLeftRow) (int32, bool) {
	if t == nil || row.token.isZero() {
		return 0, false
	}
	t.reserve(max(8, t.rowCount+1))
	var ref int32
	if t.freeHead != 0 {
		ref = t.freeHead
		t.freeHead = t.next[ref-1]
		t.rows[ref-1] = row
	} else {
		t.rows = append(t.rows, row)
		t.next = append(t.next, 0)
		t.prev = append(t.prev, 0)
		ref = int32(len(t.rows))
	}
	t.chainRow(ref)
	t.indexIdentity(ref)
	t.rowCount++
	recordTokenHolder(row.token, t.holderID(), ref)
	return ref, true
}

func (t *negativeBetaLeftBucketTable) unlink(ref int32) bool {
	if t == nil || ref <= 0 || int(ref) > len(t.rows) || t.rows[ref-1].token.isZero() {
		return false
	}
	clearTokenHolder(t.rows[ref-1].token, t.id, ref)
	t.unindexIdentity(ref)
	slot := t.slot(t.rows[ref-1].joinKey)
	next := t.next[ref-1]
	prev := t.prev[ref-1]
	if prev == 0 {
		t.heads[slot] = next
	} else {
		t.next[prev-1] = next
	}
	if next != 0 {
		t.prev[next-1] = prev
	}
	if t.tails[slot] == ref {
		t.tails[slot] = prev
	}
	if t.heads[slot] == 0 {
		t.slotCount--
	}
	t.rows[ref-1] = negativeBetaLeftRow{}
	t.next[ref-1] = t.freeHead
	t.prev[ref-1] = 0
	t.freeHead = ref
	t.rowCount--
	return true
}

// removeMatching walks every live row once, unlinking rows the match function
// selects with O(1) per removal. onRemove sees the removed row before it is
// recycled; onTouch runs per live row visited.
func (t *negativeBetaLeftBucketTable) removeMatching(match func(*negativeBetaLeftRow) bool, onTouch func(), onRemove func(negativeBetaLeftRow)) int {
	if t == nil || match == nil || t.rowCount == 0 {
		return 0
	}
	removed := 0
	for i := range t.rows {
		row := &t.rows[i]
		if row.token.isZero() {
			continue
		}
		if onTouch != nil {
			onTouch()
		}
		if !match(row) {
			continue
		}
		if onRemove != nil {
			onRemove(*row)
		}
		t.unlink(int32(i + 1))
		removed++
	}
	return removed
}

func (t *negativeBetaLeftBucketTable) forEachChainRow(key betaJoinKey, fn func(ref int32, row *negativeBetaLeftRow) bool) {
	if t == nil || fn == nil || t.rowCount == 0 || len(t.heads) == 0 {
		return
	}
	for ref := t.heads[t.slot(key)]; ref != 0; {
		nextRef := t.next[ref-1]
		row := &t.rows[ref-1]
		if row.joinKey == key && !fn(ref, row) {
			return
		}
		ref = nextRef
	}
}

func (t *negativeBetaLeftBucketTable) clear() {
	if t == nil {
		return
	}
	for _, index := range t.touched {
		if index >= 0 && index < len(t.heads) {
			t.heads[index] = 0
			t.tails[index] = 0
		}
	}
	t.touched = t.touched[:0]
	clear(t.rows)
	t.rows = t.rows[:0]
	t.next = t.next[:0]
	t.prev = t.prev[:0]
	t.byIdentity = nil
	t.freeHead = 0
	t.slotCount = 0
	t.rowCount = 0
}

func (t *negativeBetaLeftBucketTable) isEmpty() bool {
	return t == nil || t.rowCount == 0
}

func (t *negativeBetaLeftBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.slotCount
}

func (t *negativeBetaLeftBucketTable) len() int {
	if t == nil {
		return 0
	}
	return t.rowCount
}

func (t *negativeBetaLeftBucketTable) slot(key betaJoinKey) int {
	return int(hashBetaJoinTokenBucketKey(key) & uint64(len(t.heads)-1))
}
