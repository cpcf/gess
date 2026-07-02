package engine

type reteGraphNegativeBetaMemory struct {
	owner  *reteGraphBetaMemory
	id     reteGraphBetaNodeID
	node   *reteGraphBetaNode
	memory *reteGraphNegativeBetaNodeMemory
}

type reteGraphNegativeBetaNodeMemory struct {
	left  negativeBetaLeftMemory
	right negativeBetaRightMemory
}

type negativeBetaLeftMemory struct {
	indexes          negativeBetaLeftBucketTable
	rowReserve       int
	joinIndexReserve int
}

type negativeBetaRightMemory struct {
	indexes          negativeBetaRightBucketTable
	rowReserve       int
	joinIndexReserve int
}

type negativeBetaLeftRow struct {
	token        tokenRef
	joinKey      betaJoinKey
	blockerCount int
	output       tokenRef
}

type negativeBetaRightRow struct {
	token   tokenRef
	joinKey betaJoinKey
}

type negativeBetaLeftBucketTable struct {
	buckets   []negativeBetaLeftBucket
	touched   []int
	slotCount int
	rowCount  int
}

type negativeBetaRightBucketTable struct {
	buckets   []negativeBetaRightBucket
	touched   []int
	slotCount int
	rowCount  int
}

type negativeBetaLeftBucket struct {
	rows []negativeBetaLeftRow
}

type negativeBetaRightBucket struct {
	rows []negativeBetaRightRow
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
	count, ok := m.blockerCountForLeft(joinKey, token, span)
	if !ok {
		return false, nil
	}
	row, inserted := m.memory.left.insert(token, joinKey, count)
	if !inserted {
		return true, nil
	}
	if span != nil {
		span.recordBetaInputInsert(reteGraphBetaInputLeft)
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
	inserted := m.memory.right.insert(token, joinKey)
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
		leftRow.blockerCount++
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
	if removedRow.output.isZero() {
		return true
	}
	source := reteGraphStageRef{kind: reteGraphStageBeta, id: int(m.id)}
	m.owner.propagateRemoveFromStage(source, removedRow.output, counters, delta)
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
	currentMatch, ok := m.rightLastMatch(removedRow.token)
	if !ok {
		return false
	}
	m.memory.left.forEachJoinRow(joinKey, func(leftRow *negativeBetaLeftRow) bool {
		matched, err := m.leftRightMatch(leftRow, removedRow.token, currentMatch, nil)
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
		leftRow.blockerCount--
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
	var tokens []tokenRef
	m.memory.right.forEachTokenContainingFact(id, counters, func(row negativeBetaRightRow) {
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
	m.memory.right.forEachJoinRow(joinKey, func(rightRow *negativeBetaRightRow) bool {
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
			supported = false
			return false
		}
		if matched {
			count++
		}
		return true
	})
	return count, supported
}

func (m reteGraphNegativeBetaMemory) emitLeftAdd(row *negativeBetaLeftRow, span *propagationCounterSpan, delta *reteAgendaDelta) (bool, error) {
	if m.owner == nil || row == nil || delta == nil || row.token.isZero() {
		return false, nil
	}
	if row.output.isZero() {
		row.output = m.owner.newNegativeOutputTokenRef(row.token, span)
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
		ok, err := m.owner.residualJoinsMatch(m.node, rightMatch.fact, left, span)
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
	capacity := 0
	for bucketIndex := range m.indexes.buckets {
		capacity += cap(m.indexes.buckets[bucketIndex].rows)
	}
	return capacity
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
	bucket := m.indexes.bucket(key)
	if bucket == nil {
		return
	}
	for rowIndex := range bucket.rows {
		row := &bucket.rows[rowIndex]
		if row.joinKey != key {
			continue
		}
		if !fn(row) {
			return
		}
	}
}

func (m *negativeBetaLeftMemory) forEachRow(fn func(*negativeBetaLeftRow) bool) {
	if m == nil || fn == nil {
		return
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			if !fn(&bucket.rows[rowIndex]) {
				return
			}
		}
	}
}

func (m *negativeBetaLeftMemory) insert(token tokenRef, joinKey betaJoinKey, blockerCount int) (*negativeBetaLeftRow, bool) {
	if m == nil || token.isZero() {
		return nil, false
	}
	if m.indexes.reserve(max(8, m.indexes.len()+1)) {
		m.joinIndexReserve = max(m.joinIndexReserve, len(m.indexes.buckets))
	}
	bucket := m.indexes.bucket(joinKey)
	if bucket == nil {
		return nil, false
	}
	slot := m.indexes.slot(joinKey)
	if len(bucket.rows) == 0 {
		m.indexes.touched = append(m.indexes.touched, slot)
		m.indexes.slotCount++
	}
	bucket.rows = append(bucket.rows, negativeBetaLeftRow{
		token:        token,
		joinKey:      joinKey,
		blockerCount: blockerCount,
	})
	m.indexes.rowCount++
	m.rowReserve = max(m.rowReserve, m.indexes.len())
	return &bucket.rows[len(bucket.rows)-1], true
}

func (m *negativeBetaLeftMemory) removeTokenWithJoinKey(token tokenRef, joinKey betaJoinKey, counters *propagationCounterLedger) (negativeBetaLeftRow, bool) {
	if m == nil || token.isZero() {
		return negativeBetaLeftRow{}, false
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket := m.indexes.bucket(joinKey)
	if bucket == nil {
		return negativeBetaLeftRow{}, false
	}
	for rowIndex := range bucket.rows {
		row := &bucket.rows[rowIndex]
		if row.joinKey != joinKey {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		removed := *row
		m.removeBucketRow(bucket, rowIndex, counters)
		return removed, true
	}
	return negativeBetaLeftRow{}, false
}

func (m *negativeBetaLeftMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(negativeBetaLeftRow)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := 0; rowIndex < len(bucket.rows); {
			row := &bucket.rows[rowIndex]
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			if row.token.isZero() || !row.token.containsFact(id) {
				rowIndex++
				continue
			}
			if fn != nil {
				fn(*row)
			}
			m.removeBucketRow(bucket, rowIndex, counters)
			removed++
		}
	}
	return removed
}

func (m *negativeBetaLeftMemory) removeBucketRow(bucket *negativeBetaLeftBucket, rowIndex int, counters *propagationCounterLedger) {
	if m == nil || bucket == nil || rowIndex < 0 || rowIndex >= len(bucket.rows) {
		return
	}
	last := len(bucket.rows) - 1
	if rowIndex != last {
		bucket.rows[rowIndex] = bucket.rows[last]
		if counters != nil {
			counters.recordRemovalRowMoved()
		}
	}
	bucket.rows[last] = negativeBetaLeftRow{}
	bucket.rows = bucket.rows[:last]
	m.indexes.rowCount--
	if len(bucket.rows) == 0 {
		m.indexes.slotCount--
	}
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

func (m *negativeBetaRightMemory) clear() {
	if m == nil {
		return
	}
	m.indexes.clear()
}

func (m *negativeBetaRightMemory) len() int {
	if m == nil {
		return 0
	}
	return m.indexes.len()
}

func (m *negativeBetaRightMemory) rowCapacity() int {
	if m == nil {
		return 0
	}
	capacity := 0
	for bucketIndex := range m.indexes.buckets {
		capacity += cap(m.indexes.buckets[bucketIndex].rows)
	}
	return capacity
}

func (m *negativeBetaRightMemory) joinRowCount(key betaJoinKey) int {
	count := 0
	m.forEachJoinRow(key, func(*negativeBetaRightRow) bool {
		count++
		return true
	})
	return count
}

func (m *negativeBetaRightMemory) forEachJoinRow(key betaJoinKey, fn func(*negativeBetaRightRow) bool) {
	if m == nil || fn == nil || m.indexes.isEmpty() {
		return
	}
	bucket := m.indexes.bucket(key)
	if bucket == nil {
		return
	}
	for rowIndex := range bucket.rows {
		row := &bucket.rows[rowIndex]
		if row.joinKey != key {
			continue
		}
		if !fn(row) {
			return
		}
	}
}

func (m *negativeBetaRightMemory) forEachRow(fn func(*negativeBetaRightRow) bool) {
	if m == nil || fn == nil {
		return
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			if !fn(&bucket.rows[rowIndex]) {
				return
			}
		}
	}
}

func (m *negativeBetaRightMemory) insert(token tokenRef, joinKey betaJoinKey) bool {
	if m == nil || token.isZero() {
		return false
	}
	if m.indexes.reserve(max(8, m.indexes.len()+1)) {
		m.joinIndexReserve = max(m.joinIndexReserve, len(m.indexes.buckets))
	}
	bucket := m.indexes.bucket(joinKey)
	if bucket == nil {
		return false
	}
	slot := m.indexes.slot(joinKey)
	if len(bucket.rows) == 0 {
		m.indexes.touched = append(m.indexes.touched, slot)
		m.indexes.slotCount++
	}
	bucket.rows = append(bucket.rows, negativeBetaRightRow{
		token:   token,
		joinKey: joinKey,
	})
	m.indexes.rowCount++
	m.rowReserve = max(m.rowReserve, m.indexes.len())
	return true
}

func (m *negativeBetaRightMemory) removeTokenWithJoinKey(token tokenRef, joinKey betaJoinKey, counters *propagationCounterLedger) (negativeBetaRightRow, bool) {
	if m == nil || token.isZero() {
		return negativeBetaRightRow{}, false
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket := m.indexes.bucket(joinKey)
	if bucket == nil {
		return negativeBetaRightRow{}, false
	}
	for rowIndex := range bucket.rows {
		row := &bucket.rows[rowIndex]
		if row.joinKey != joinKey {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		removed := *row
		m.removeBucketRow(bucket, rowIndex, counters)
		return removed, true
	}
	return negativeBetaRightRow{}, false
}

func (m *negativeBetaRightMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(negativeBetaRightRow)) {
	if m == nil || id.IsZero() {
		return
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := range bucket.rows {
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			row := &bucket.rows[rowIndex]
			if row.token.isZero() || !row.token.containsFact(id) {
				continue
			}
			if fn != nil {
				fn(*row)
			}
		}
	}
}

func (m *negativeBetaRightMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(negativeBetaRightRow)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for bucketIndex := range m.indexes.buckets {
		bucket := &m.indexes.buckets[bucketIndex]
		for rowIndex := 0; rowIndex < len(bucket.rows); {
			row := &bucket.rows[rowIndex]
			if counters != nil {
				counters.recordRemovalRowTouched()
			}
			if row.token.isZero() || !row.token.containsFact(id) {
				rowIndex++
				continue
			}
			if fn != nil {
				fn(*row)
			}
			m.removeBucketRow(bucket, rowIndex, counters)
			removed++
		}
	}
	return removed
}

func (m *negativeBetaRightMemory) removeBucketRow(bucket *negativeBetaRightBucket, rowIndex int, counters *propagationCounterLedger) {
	if m == nil || bucket == nil || rowIndex < 0 || rowIndex >= len(bucket.rows) {
		return
	}
	last := len(bucket.rows) - 1
	if rowIndex != last {
		bucket.rows[rowIndex] = bucket.rows[last]
		if counters != nil {
			counters.recordRemovalRowMoved()
		}
	}
	bucket.rows[last] = negativeBetaRightRow{}
	bucket.rows = bucket.rows[:last]
	m.indexes.rowCount--
	if len(bucket.rows) == 0 {
		m.indexes.slotCount--
	}
}

func (m negativeBetaRightMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	diag := reteGraphTokenMemoryDiagnostics{
		Rows:          m.len(),
		JoinIndexKeys: m.indexes.keyCount(),
	}
	seen := make(map[betaJoinKey]struct{}, m.indexes.keyCount())
	m.forEachRow(func(row *negativeBetaRightRow) bool {
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

func (t *negativeBetaLeftBucketTable) reserve(capacity int) bool {
	if capacity <= 0 {
		return false
	}
	slotCapacity := graphTokenBucketSlotCapacity(capacity)
	if slotCapacity <= len(t.buckets) {
		return false
	}
	old := t.buckets
	t.buckets = make([]negativeBetaLeftBucket, graphTokenBucketPowerOfTwo(max(8, slotCapacity)))
	t.touched = t.touched[:0]
	t.slotCount = 0
	t.rowCount = 0
	for i := range old {
		for rowIndex := range old[i].rows {
			t.appendRehashed(old[i].rows[rowIndex])
		}
		old[i].clear()
	}
	return true
}

func (t *negativeBetaLeftBucketTable) clear() {
	if t == nil || len(t.buckets) == 0 {
		return
	}
	for _, index := range t.touched {
		if index >= 0 && index < len(t.buckets) {
			t.buckets[index].clear()
		}
	}
	t.touched = t.touched[:0]
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

func (t *negativeBetaLeftBucketTable) bucket(key betaJoinKey) *negativeBetaLeftBucket {
	if t == nil || len(t.buckets) == 0 {
		return nil
	}
	return &t.buckets[t.slot(key)]
}

func (t *negativeBetaLeftBucketTable) slot(key betaJoinKey) int {
	return int(hashBetaJoinTokenBucketKey(key) & uint64(len(t.buckets)-1))
}

func (t *negativeBetaLeftBucketTable) appendRehashed(row negativeBetaLeftRow) {
	if t == nil || len(t.buckets) == 0 || row.token.isZero() {
		return
	}
	index := t.slot(row.joinKey)
	bucket := &t.buckets[index]
	if len(bucket.rows) == 0 {
		t.touched = append(t.touched, index)
		t.slotCount++
	}
	bucket.rows = append(bucket.rows, row)
	t.rowCount++
}

func (b *negativeBetaLeftBucket) clear() {
	if b == nil || len(b.rows) == 0 {
		return
	}
	clear(b.rows)
	b.rows = b.rows[:0]
}

func (t *negativeBetaRightBucketTable) reserve(capacity int) bool {
	if capacity <= 0 {
		return false
	}
	slotCapacity := graphTokenBucketSlotCapacity(capacity)
	if slotCapacity <= len(t.buckets) {
		return false
	}
	old := t.buckets
	t.buckets = make([]negativeBetaRightBucket, graphTokenBucketPowerOfTwo(max(8, slotCapacity)))
	t.touched = t.touched[:0]
	t.slotCount = 0
	t.rowCount = 0
	for i := range old {
		for rowIndex := range old[i].rows {
			t.appendRehashed(old[i].rows[rowIndex])
		}
		old[i].clear()
	}
	return true
}

func (t *negativeBetaRightBucketTable) clear() {
	if t == nil || len(t.buckets) == 0 {
		return
	}
	for _, index := range t.touched {
		if index >= 0 && index < len(t.buckets) {
			t.buckets[index].clear()
		}
	}
	t.touched = t.touched[:0]
	t.slotCount = 0
	t.rowCount = 0
}

func (t *negativeBetaRightBucketTable) isEmpty() bool {
	return t == nil || t.rowCount == 0
}

func (t *negativeBetaRightBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.slotCount
}

func (t *negativeBetaRightBucketTable) len() int {
	if t == nil {
		return 0
	}
	return t.rowCount
}

func (t *negativeBetaRightBucketTable) bucket(key betaJoinKey) *negativeBetaRightBucket {
	if t == nil || len(t.buckets) == 0 {
		return nil
	}
	return &t.buckets[t.slot(key)]
}

func (t *negativeBetaRightBucketTable) slot(key betaJoinKey) int {
	return int(hashBetaJoinTokenBucketKey(key) & uint64(len(t.buckets)-1))
}

func (t *negativeBetaRightBucketTable) appendRehashed(row negativeBetaRightRow) {
	if t == nil || len(t.buckets) == 0 || row.token.isZero() {
		return
	}
	index := t.slot(row.joinKey)
	bucket := &t.buckets[index]
	if len(bucket.rows) == 0 {
		t.touched = append(t.touched, index)
		t.slotCount++
	}
	bucket.rows = append(bucket.rows, row)
	t.rowCount++
}

func (b *negativeBetaRightBucket) clear() {
	if b == nil || len(b.rows) == 0 {
		return
	}
	clear(b.rows)
	b.rows = b.rows[:0]
}
