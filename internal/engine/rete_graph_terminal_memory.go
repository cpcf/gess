package engine

type terminalTokenMemory struct {
	rows                 []terminalTokenRow
	rowHandles           []graphTokenRowHandleEntry
	identityRows         tokenIdentityHeadTable
	factRows             factTokenBucketTable
	rowReserve           int
	identityIndexReserve int
	factIndexReserve     int
	factRowsDirty        bool
	bucketRestFree       [][]graphTokenRowID
	freeRowHandles       []graphTokenRowHandleID
}

type terminalTokenRow struct {
	handle         graphTokenRowHandle
	token          tokenRef
	identityHash   uint64
	identityNext   graphTokenRowID
	activation     activationHandle
	supportCount   int
	branchSupport  terminalBranchSupport
	branchOverflow *terminalBranchSupportOverflow
	branchCount    int
}

type queryTerminalMemory struct {
	rows                 []queryTerminalRow
	identityRows         tokenIdentityHeadTable
	factRows             factTokenBucketTable
	rowReserve           int
	identityIndexReserve int
	factIndexReserve     int
	factRowsDirty        bool
	bucketRestFree       [][]graphTokenRowID
}

type queryTerminalRow struct {
	token        tokenRef
	identityHash uint64
	identityNext graphTokenRowID
	supportCount int
}

func (r terminalTokenRow) toGraphTokenRow() graphTokenRow {
	return graphTokenRow{
		handle:         r.handle,
		token:          r.token,
		identity:       r.token.identityKey(),
		activation:     r.activation,
		supportCount:   r.supportCount,
		branchSupport:  r.branchSupport,
		branchOverflow: r.branchOverflow,
		branchCount:    r.branchCount,
	}
}

func (r terminalTokenRow) isTerminal() bool {
	return r.supportCount > 0
}

func (r terminalTokenRow) terminalBranchOverflowItems() []terminalBranchSupport {
	if r.branchOverflow == nil {
		return nil
	}
	return r.branchOverflow.items
}

func (r *terminalTokenRow) addTerminalBranchSupport(branchID int) {
	if r == nil || branchID < 0 {
		return
	}
	if r.branchCount == 0 {
		r.branchSupport = terminalBranchSupport{branchID: branchID, count: 1}
		r.branchCount = 1
		return
	}
	if r.branchSupport.branchID == branchID {
		r.branchSupport.count++
		return
	}
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		if overflow[i].branchID == branchID {
			overflow[i].count++
			return
		}
	}
	if r.branchOverflow == nil {
		r.branchOverflow = &terminalBranchSupportOverflow{}
	}
	r.branchOverflow.items = append(r.branchOverflow.items, terminalBranchSupport{branchID: branchID, count: 1})
	r.branchCount++
}

func (r terminalTokenRow) hasTerminalBranchSupport(branchID int) bool {
	if branchID < 0 || r.branchCount == 0 {
		return false
	}
	if r.branchSupport.branchID == branchID && r.branchSupport.count > 0 {
		return true
	}
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		support := overflow[i]
		if support.branchID == branchID && support.count > 0 {
			return true
		}
	}
	return false
}

func (r *terminalTokenRow) removeTerminalBranchSupport(branchID int) {
	if r == nil || branchID < 0 || r.branchCount == 0 {
		return
	}
	if r.branchSupport.branchID == branchID {
		r.branchSupport.count--
		if r.branchSupport.count > 0 {
			return
		}
		r.removeTerminalBranchSupportAt(0)
		return
	}
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		if overflow[i].branchID != branchID {
			continue
		}
		overflow[i].count--
		if overflow[i].count > 0 {
			return
		}
		r.removeTerminalBranchSupportAt(i + 1)
		return
	}
}

func (r *terminalTokenRow) removeTerminalBranchSupportAt(index int) {
	if r == nil || index < 0 || index >= r.branchCount {
		return
	}
	overflow := r.terminalBranchOverflowItems()
	overflowCount := min(r.branchCount-1, len(overflow))
	if r.branchCount == 1 || overflowCount == 0 {
		r.branchSupport = terminalBranchSupport{}
		r.branchCount = 0
		r.branchOverflow = nil
		return
	}
	if index > overflowCount {
		return
	}
	lastOverflow := overflowCount - 1
	last := overflow[lastOverflow]
	overflow[lastOverflow] = terminalBranchSupport{}
	overflow = overflow[:lastOverflow]
	if index == 0 {
		r.branchSupport = last
	} else if index-1 < len(overflow) {
		overflow[index-1] = last
	}
	r.branchCount = overflowCount
	if len(overflow) == 0 {
		r.branchOverflow = nil
	} else {
		r.branchOverflow.items = overflow
	}
}

func (r terminalTokenRow) terminalBranchIDs() []int {
	if r.branchCount == 0 {
		return nil
	}
	out := make([]int, 0, r.branchCount)
	r.forEachTerminalBranchSupport(func(support terminalBranchSupport) {
		if support.count <= 0 {
			return
		}
		out = append(out, support.branchID)
	})
	return out
}

func (r terminalTokenRow) forEachTerminalBranchSupport(fn func(terminalBranchSupport)) {
	if r.branchCount == 0 || fn == nil {
		return
	}
	fn(r.branchSupport)
	overflow := r.terminalBranchOverflowItems()
	for i := 0; i < r.branchCount-1 && i < len(overflow); i++ {
		fn(overflow[i])
	}
}

func (m *terminalTokenMemory) reserveTerminal(rowCapacity, factCapacity int) {
	if m == nil || rowCapacity <= 0 {
		return
	}
	m.reserveRows(rowCapacity)
	m.reserveIndexes(0, rowCapacity, factCapacity)
}

func (m *terminalTokenMemory) reserveRows(rowCapacity int) {
	if m == nil || rowCapacity <= cap(m.rows) {
		return
	}
	rows := make([]terminalTokenRow, len(m.rows), rowCapacity)
	copy(rows, m.rows)
	m.rows = rows
	m.reserveRowHandles(rowCapacity)
	m.rowReserve = max(m.rowReserve, rowCapacity)
}

func (m *terminalTokenMemory) ensureRowCapacity(rowCapacity int) {
	if m == nil || rowCapacity <= cap(m.rows) {
		return
	}
	nextCapacity := max(8, cap(m.rows)*2)
	for nextCapacity < rowCapacity {
		nextCapacity *= 2
	}
	rows := make([]terminalTokenRow, len(m.rows), nextCapacity)
	copy(rows, m.rows)
	m.rows = rows
	m.reserveRowHandles(nextCapacity)
	m.rowReserve = max(m.rowReserve, nextCapacity)
}

func (m *terminalTokenMemory) reserveRowHandles(rowCapacity int) {
	if m == nil {
		return
	}
	if rowCapacity > cap(m.rowHandles) {
		handles := make([]graphTokenRowHandleEntry, len(m.rowHandles), rowCapacity)
		copy(handles, m.rowHandles)
		m.rowHandles = handles
	}
	if rowCapacity > cap(m.freeRowHandles) {
		free := make([]graphTokenRowHandleID, len(m.freeRowHandles), rowCapacity)
		copy(free, m.freeRowHandles)
		m.freeRowHandles = free
	}
}

func (m *terminalTokenMemory) reserveIndexes(_ int, identityCapacity int, factCapacity int) {
	if m == nil {
		return
	}
	if identityCapacity > 0 {
		if m.identityRows.reserve(identityCapacity) {
			m.rebuildIdentityRows()
		}
		m.identityIndexReserve = max(m.identityIndexReserve, identityCapacity)
	}
	if factCapacity > 0 {
		m.factRows.reserve(factCapacity)
		m.factIndexReserve = max(m.factIndexReserve, factCapacity)
	}
}

func (m *terminalTokenMemory) clear() {
	if m == nil {
		return
	}
	m.invalidateRowHandles()
	for i := range m.rows {
		m.rows[i] = terminalTokenRow{}
	}
	m.rows = m.rows[:0]
	m.identityRows.clear()
	m.factRows.clear(m.recycleBucketRest)
	m.factRowsDirty = false
}

func (m *terminalTokenMemory) allocateRowHandle(rowID graphTokenRowID) graphTokenRowHandle {
	if m == nil || rowID < 0 {
		return graphTokenRowHandle{}
	}
	if len(m.freeRowHandles) > 0 {
		last := len(m.freeRowHandles) - 1
		id := m.freeRowHandles[last]
		m.freeRowHandles[last] = 0
		m.freeRowHandles = m.freeRowHandles[:last]
		index := int(id - 1)
		if id != 0 && index >= 0 && index < len(m.rowHandles) {
			entry := &m.rowHandles[index]
			if entry.generation == 0 {
				entry.generation = 1
			}
			entry.rowID = rowID
			entry.live = true
			return graphTokenRowHandle{id: id, generation: entry.generation}
		}
	}
	id := graphTokenRowHandleID(len(m.rowHandles) + 1)
	entry := graphTokenRowHandleEntry{
		rowID:      rowID,
		generation: 1,
		live:       true,
	}
	m.rowHandles = append(m.rowHandles, entry)
	return graphTokenRowHandle{id: id, generation: entry.generation}
}

func (m *terminalTokenMemory) rowByHandle(handle graphTokenRowHandle) *terminalTokenRow {
	rowID, ok := m.rowIDByHandle(handle)
	if !ok {
		return nil
	}
	return m.row(rowID)
}

func (m *terminalTokenMemory) rowIDByHandle(handle graphTokenRowHandle) (graphTokenRowID, bool) {
	if m == nil || handle.isZero() {
		return 0, false
	}
	index := handle.index()
	if index < 0 || index >= len(m.rowHandles) {
		return 0, false
	}
	entry := m.rowHandles[index]
	if !entry.live || entry.generation != handle.generation || entry.rowID < 0 {
		return 0, false
	}
	return entry.rowID, true
}

func (m *terminalTokenMemory) moveRowHandle(handle graphTokenRowHandle, rowID graphTokenRowID) {
	if m == nil || handle.isZero() || rowID < 0 {
		return
	}
	index := handle.index()
	if index < 0 || index >= len(m.rowHandles) {
		return
	}
	entry := &m.rowHandles[index]
	if !entry.live || entry.generation != handle.generation {
		return
	}
	entry.rowID = rowID
}

func (m *terminalTokenMemory) releaseRowHandle(handle graphTokenRowHandle) {
	if m == nil || handle.isZero() {
		return
	}
	index := handle.index()
	if index < 0 || index >= len(m.rowHandles) {
		return
	}
	entry := &m.rowHandles[index]
	if !entry.live || entry.generation != handle.generation {
		return
	}
	entry.live = false
	entry.rowID = -1
	entry.generation++
	if entry.generation == 0 {
		entry.generation = 1
	}
	m.freeRowHandles = append(m.freeRowHandles, handle.id)
}

func (m *terminalTokenMemory) invalidateRowHandles() {
	if m == nil || len(m.rowHandles) == 0 {
		return
	}
	m.freeRowHandles = m.freeRowHandles[:0]
	for index := range m.rowHandles {
		entry := &m.rowHandles[index]
		if entry.live {
			entry.generation++
			if entry.generation == 0 {
				entry.generation = 1
			}
		}
		entry.live = false
		entry.rowID = -1
		m.freeRowHandles = append(m.freeRowHandles, graphTokenRowHandleID(index+1))
	}
}

func (m *terminalTokenMemory) appendBucketRow(bucket *graphTokenRowIDBucket, id graphTokenRowID) {
	if m == nil || bucket == nil {
		return
	}
	if bucket.count == 0 {
		bucket.first = id
		bucket.count = 1
		return
	}
	if bucket.count == 1 {
		bucket.second = id
		bucket.count = 2
		return
	}
	if bucket.rest == nil {
		bucket.rest = m.takeBucketRest()
	}
	bucket.rest = append(bucket.rest, id)
	bucket.count++
}

func (m *terminalTokenMemory) appendIdentityIndexRow(key graphTokenIdentityKey, id graphTokenRowID) {
	m.appendIdentityHashIndexRow(hashTokenIdentityBucketKey(key), id)
}

func (m *terminalTokenMemory) appendIdentityHashIndexRow(hash uint64, id graphTokenRowID) {
	if m == nil {
		return
	}
	row := m.row(id)
	if row == nil {
		return
	}
	head := m.identityRows.headHash(hash)
	row.identityNext = head
	m.identityRows.setHeadHash(hash, graphTokenRowIDRef(id))
}

func (m *terminalTokenMemory) appendFactIndexRow(key FactID, id graphTokenRowID) {
	if m == nil {
		return
	}
	if graphTokenBucketNeedsGrow(m.factRows.used+1, len(m.factRows.entries)) {
		m.factRows.rehash(max(8, len(m.factRows.entries)*2))
	}
	index, ok := m.factRows.findInsert(key)
	if !ok {
		if m.factRows.entries[index].state == graphTokenBucketEmpty {
			m.factRows.touched = append(m.factRows.touched, index)
			m.factRows.used++
		}
		m.factRows.entries[index] = factTokenBucketEntry{key: key, state: graphTokenBucketFull}
		m.factRows.count++
	}
	m.appendBucketRow(&m.factRows.entries[index].bucket, id)
}

func (m *terminalTokenMemory) takeBucketRest() []graphTokenRowID {
	if m == nil || len(m.bucketRestFree) == 0 {
		return make([]graphTokenRowID, 0, 8)
	}
	last := len(m.bucketRestFree) - 1
	rest := m.bucketRestFree[last]
	m.bucketRestFree[last] = nil
	m.bucketRestFree = m.bucketRestFree[:last]
	return rest[:0]
}

func (m *terminalTokenMemory) recycleBucketRest(rest []graphTokenRowID) {
	if m == nil || cap(rest) == 0 {
		return
	}
	clear(rest)
	m.bucketRestFree = append(m.bucketRestFree, rest[:0])
}

func (m *terminalTokenMemory) len() int {
	if m == nil {
		return 0
	}
	return len(m.rows)
}

func (m *terminalTokenMemory) row(rowID graphTokenRowID) *terminalTokenRow {
	if m == nil || rowID < 0 {
		return nil
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return nil
	}
	return &m.rows[index]
}

func (m *terminalTokenMemory) insertFreshTerminalRow(token tokenRef, branchID int) graphTokenRowHandle {
	if m == nil || token.isZero() {
		return graphTokenRowHandle{}
	}
	identity := tokenRefKey(token)
	rowID := graphTokenRowID(len(m.rows))
	m.ensureRowCapacity(int(rowID) + 1)
	m.ensureIdentityRowsCapacity(int(rowID) + 1)
	m.rows = m.rows[:int(rowID)+1]
	handle := m.allocateRowHandle(rowID)
	row := terminalTokenRow{
		handle:       handle,
		token:        token,
		identityHash: hashTokenIdentityBucketKey(identity),
		supportCount: 1,
	}
	row.addTerminalBranchSupport(branchID)
	m.rows[rowID] = row
	m.appendIdentityIndexRow(identity, rowID)
	m.markFactRowsDirty()
	return handle
}

func (m *terminalTokenMemory) insertTerminalRow(token tokenRef, branchID int) (graphTokenRowHandle, bool) {
	if m == nil || token.isZero() {
		return graphTokenRowHandle{}, false
	}
	identity := tokenRefKey(token)
	identityHash := hashTokenIdentityBucketKey(identity)
	for ref := m.identityRows.headHash(identityHash); ref != 0; {
		rowID := graphTokenRowRefID(ref)
		row := m.row(rowID)
		if row != nil {
			ref = row.identityNext
		} else {
			ref = 0
		}
		if row == nil || row.identityHash != identityHash || !tokenRefEqual(row.token, token) {
			continue
		}
		if row.token.handle == token.handle {
			if !row.hasTerminalBranchSupport(branchID) {
				row.addTerminalBranchSupport(branchID)
			}
			return row.handle, false
		}
		row.supportCount++
		row.addTerminalBranchSupport(branchID)
		return row.handle, false
	}

	rowID := graphTokenRowID(len(m.rows))
	m.ensureRowCapacity(int(rowID) + 1)
	m.ensureIdentityRowsCapacity(int(rowID) + 1)
	m.rows = m.rows[:int(rowID)+1]
	handle := m.allocateRowHandle(rowID)
	row := terminalTokenRow{
		handle:       handle,
		token:        token,
		identityHash: identityHash,
		supportCount: 1,
	}
	row.addTerminalBranchSupport(branchID)
	m.rows[rowID] = row
	m.appendIdentityIndexRow(identity, rowID)
	m.markFactRowsDirty()
	return handle, true
}

func (m *terminalTokenMemory) refreshTerminalTokensContainingFact(id FactID, updates []reteTerminalTokenUpdate, collectUpdates bool, identityForToken func(tokenRef) candidateIdentity, refresh func(graphTokenRow) (tokenRef, bool)) []reteTerminalTokenUpdate {
	if m == nil || id.IsZero() || refresh == nil {
		return updates
	}
	m.ensureFactRows()
	bucket, ok := m.factRows.get(id)
	if !ok || bucket.len() == 0 {
		return updates
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
		row := m.row(rowID)
		if row == nil || row.token.isZero() || !row.isTerminal() || !row.token.containsFact(id) {
			continue
		}
		beforeIdentityHash := row.identityHash
		next, ok := refresh(row.toGraphTokenRow())
		if !ok || next.isZero() {
			continue
		}
		before := row.token
		identity := candidateIdentity{}
		if collectUpdates && identityForToken != nil {
			identity = identityForToken(before)
		}
		m.replaceRowTokenWithPreviousIdentityHash(rowID, beforeIdentityHash, next)
		if collectUpdates {
			updates = append(updates, reteTerminalTokenUpdate{
				before:   before,
				after:    next,
				identity: identity,
			})
		}
	}
	return updates
}

func (m *terminalTokenMemory) replaceRowToken(rowID graphTokenRowID, token tokenRef) {
	m.replaceRowTokenWithPreviousIdentityHash(rowID, 0, token)
}

func (m *terminalTokenMemory) replaceRowTokenWithPreviousIdentityHash(rowID graphTokenRowID, previousIdentityHash uint64, token tokenRef) {
	if m == nil || rowID < 0 || token.isZero() {
		return
	}
	row := m.row(rowID)
	if row == nil || row.token.isZero() {
		return
	}
	nextIdentityHash := hashTokenIdentityBucketKey(token.identityKey())
	if previousIdentityHash == 0 {
		previousIdentityHash = row.identityHash
	}
	if previousIdentityHash != nextIdentityHash {
		m.ensureIdentityRowsCapacity(len(m.rows))
		m.removeIdentityHashIndexRow(previousIdentityHash, rowID)
		m.appendIdentityHashIndexRow(nextIdentityHash, rowID)
		row.identityHash = nextIdentityHash
	}
	row.token = token
	m.markFactRowsDirty()
}

func (m *terminalTokenMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	m.ensureFactRows()
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows.get(id)
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

func (m *terminalTokenMemory) removeTokensContainingFact(id FactID, counters *propagationCounterLedger, fn func(graphTokenRow)) int {
	if m == nil || id.IsZero() {
		return 0
	}
	m.ensureFactRows()
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows.get(id)
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
		if row != nil && !row.token.isZero() && row.token.containsFact(id) && fn != nil {
			fn(row.toGraphTokenRow())
		}
		m.removeRow(rowID, counters)
		removed++
	}
}

func (m *terminalTokenMemory) removeToken(token tokenRef, counters *propagationCounterLedger, branchIDs ...int) (graphTokenRow, bool) {
	if m == nil || token.isZero() {
		return graphTokenRow{}, false
	}
	branchID := -1
	if len(branchIDs) > 0 {
		branchID = branchIDs[0]
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	identity := tokenRefKey(token)
	identityHash := hashTokenIdentityBucketKey(identity)
	if m.identityRows.headHash(identityHash) == 0 {
		return graphTokenRow{}, false
	}
	for ref := m.identityRows.headHash(identityHash); ref != 0; {
		rowID := graphTokenRowRefID(ref)
		row := m.row(rowID)
		if row != nil {
			ref = row.identityNext
		} else {
			ref = 0
		}
		if row == nil || row.identityHash != identityHash {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		if row.isTerminal() && row.supportCount > 1 {
			row.supportCount--
			row.removeTerminalBranchSupport(branchID)
			return graphTokenRow{}, false
		}
		removed := row.toGraphTokenRow()
		m.removeRow(rowID, counters)
		return removed, true
	}
	return graphTokenRow{}, false
}

func (m *terminalTokenMemory) removeTokenByHandle(handle graphTokenRowHandle, counters *propagationCounterLedger, branchID int) (graphTokenRow, bool, bool) {
	if m == nil || handle.isZero() {
		return graphTokenRow{}, false, false
	}
	rowID, ok := m.rowIDByHandle(handle)
	if !ok {
		return graphTokenRow{}, false, false
	}
	if counters != nil {
		counters.recordRemovalRowTouched()
	}
	row := m.row(rowID)
	if row == nil || row.token.isZero() {
		return graphTokenRow{}, false, false
	}
	if row.isTerminal() && row.supportCount > 1 {
		row.supportCount--
		row.removeTerminalBranchSupport(branchID)
		return graphTokenRow{}, false, true
	}
	removed := row.toGraphTokenRow()
	m.removeRow(rowID, counters)
	return removed, true, true
}

func (m *terminalTokenMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(graphTokenRow)) {
	if m == nil || id.IsZero() {
		return
	}
	m.ensureFactRows()
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket, ok := m.factRows.get(id)
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
		if fn != nil {
			fn(row.toGraphTokenRow())
		}
	}
}

func (m *terminalTokenMemory) removeRow(rowID graphTokenRowID, counters *propagationCounterLedger) {
	if m == nil || rowID < 0 {
		return
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return
	}
	removed := m.rows[index]
	m.removeIdentityHashIndexRow(removed.identityHash, rowID)
	if !m.factRowsDirty {
		m.removeTokenFacts(removed.token, rowID)
	}
	last := len(m.rows) - 1
	if index != last {
		moved := m.rows[last]
		m.rows[index] = moved
		m.moveRowHandle(moved.handle, rowID)
		if counters != nil {
			counters.recordRemovalRowMoved()
		}
		m.replaceIdentityHashIndexRow(moved.identityHash, graphTokenRowID(last), rowID)
		if !m.factRowsDirty {
			m.replaceTokenFactRows(moved.token, graphTokenRowID(last), rowID)
		}
	}
	m.releaseRowHandle(removed.handle)
	m.rows[last] = terminalTokenRow{}
	m.rows = m.rows[:last]
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m *terminalTokenMemory) ensureIdentityRowsCapacity(rowCapacity int) {
	if m == nil {
		return
	}
	if m.identityRows.reserve(max(8, rowCapacity)) {
		m.rebuildIdentityRows()
	}
}

func (m *terminalTokenMemory) rebuildIdentityRows() {
	if m == nil {
		return
	}
	m.identityRows.clear()
	for index := range m.rows {
		m.rows[index].identityNext = 0
	}
	for index := range m.rows {
		row := &m.rows[index]
		if row.token.isZero() {
			continue
		}
		hash := row.identityHash
		if hash == 0 {
			hash = hashTokenIdentityBucketKey(row.token.identityKey())
			row.identityHash = hash
		}
		m.appendIdentityHashIndexRow(hash, graphTokenRowID(index))
	}
}

func (m *terminalTokenMemory) removeIdentityHashIndexRow(hash uint64, rowID graphTokenRowID) bool {
	if m == nil || len(m.identityRows.heads) == 0 {
		return false
	}
	var previous *terminalTokenRow
	for ref := m.identityRows.headHash(hash); ref != 0; {
		currentID := graphTokenRowRefID(ref)
		current := m.row(currentID)
		if current == nil {
			break
		}
		next := current.identityNext
		if currentID == rowID {
			if previous == nil {
				m.identityRows.setHeadHash(hash, next)
			} else {
				previous.identityNext = next
			}
			current.identityNext = 0
			return true
		}
		previous = current
		ref = next
	}
	return false
}

func (m *terminalTokenMemory) replaceIdentityHashIndexRow(hash uint64, oldID, newID graphTokenRowID) bool {
	if m == nil || len(m.identityRows.heads) == 0 || oldID == newID {
		return false
	}
	oldRef := graphTokenRowIDRef(oldID)
	newRef := graphTokenRowIDRef(newID)
	for ref := m.identityRows.headHash(hash); ref != 0; {
		currentID := graphTokenRowRefID(ref)
		current := m.row(currentID)
		if current == nil {
			break
		}
		if ref == oldRef {
			m.identityRows.setHeadHash(hash, newRef)
			return true
		}
		if current.identityNext == oldRef {
			current.identityNext = newRef
			return true
		}
		ref = current.identityNext
	}
	return false
}

func (m *terminalTokenMemory) markFactRowsDirty() {
	if m == nil {
		return
	}
	m.factRowsDirty = true
}

func (m *terminalTokenMemory) ensureFactRows() {
	if m == nil || !m.factRowsDirty {
		return
	}
	m.rebuildFactRows()
}

func (m *terminalTokenMemory) rebuildFactRows() {
	if m == nil {
		return
	}
	m.factRows.clear(m.recycleBucketRest)
	m.factRows.reserve(max(m.factIndexReserve, len(m.rows)))
	for index, row := range m.rows {
		if row.token.isZero() {
			continue
		}
		m.indexTokenFacts(row.token, graphTokenRowID(index))
	}
	m.factRowsDirty = false
}

func (m *terminalTokenMemory) indexTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			m.appendFactIndexRow(id, rowID)
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		match, ok := row.conditionMatch()
		if !ok {
			return
		}
		id := match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		m.appendFactIndexRow(id, rowID)
	}
}

func (m *terminalTokenMemory) removeTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || m.factRows.isEmpty() || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket, ok := m.factRows.get(id)
			if !ok || !bucket.remove(rowID) {
				continue
			}
			if bucket.len() == 0 {
				m.recycleBucketRest(bucket.rest)
				m.factRows.delete(id)
			} else {
				m.factRows.set(id, bucket)
			}
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		match, ok := row.conditionMatch()
		if !ok {
			return
		}
		id := match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket, ok := m.factRows.get(id)
		if !ok || !bucket.remove(rowID) {
			continue
		}
		if bucket.len() == 0 {
			m.recycleBucketRest(bucket.rest)
			m.factRows.delete(id)
		} else {
			m.factRows.set(id, bucket)
		}
	}
}

func (m *terminalTokenMemory) replaceTokenFactRows(token tokenRef, oldID, newID graphTokenRowID) {
	if m == nil || m.factRows.isEmpty() || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket, ok := m.factRows.get(id)
			if ok && bucket.replace(oldID, newID) {
				m.factRows.set(id, bucket)
			}
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		match, ok := row.conditionMatch()
		if !ok {
			return
		}
		id := match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket, ok := m.factRows.get(id)
		if ok && bucket.replace(oldID, newID) {
			m.factRows.set(id, bucket)
		}
	}
}

func (m terminalTokenMemory) factIndexKeyCount() int {
	if !m.factRowsDirty {
		return m.factRows.keyCount()
	}
	if len(m.rows) == 0 {
		return 0
	}
	seen := make(map[FactID]struct{}, len(m.rows))
	for _, row := range m.rows {
		if row.token.isZero() {
			continue
		}
		if factIDs, ok := row.token.factIDs(); ok {
			for i, id := range factIDs {
				if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
					continue
				}
				seen[id] = struct{}{}
			}
			continue
		}
		for current := row.token; !current.isZero(); current = current.parent() {
			tokenRow, ok := current.resolve()
			if !ok {
				break
			}
			match, ok := tokenRow.conditionMatch()
			if !ok {
				break
			}
			id := match.fact.ID()
			if id.IsZero() || current.parent().containsFact(id) {
				continue
			}
			seen[id] = struct{}{}
		}
	}
	return len(seen)
}

func (m *queryTerminalMemory) reserveQuery(rowCapacity, factCapacity int) {
	if m == nil || rowCapacity <= 0 {
		return
	}
	m.reserveRows(rowCapacity)
	m.reserveIndexes(rowCapacity, factCapacity)
}

func (m *queryTerminalMemory) reserveRows(rowCapacity int) {
	if m == nil || rowCapacity <= cap(m.rows) {
		return
	}
	rows := make([]queryTerminalRow, len(m.rows), rowCapacity)
	copy(rows, m.rows)
	m.rows = rows
	m.rowReserve = max(m.rowReserve, rowCapacity)
}

func (m *queryTerminalMemory) ensureRowCapacity(rowCapacity int) {
	if m == nil || rowCapacity <= cap(m.rows) {
		return
	}
	nextCapacity := max(8, cap(m.rows)*2)
	for nextCapacity < rowCapacity {
		nextCapacity *= 2
	}
	rows := make([]queryTerminalRow, len(m.rows), nextCapacity)
	copy(rows, m.rows)
	m.rows = rows
	m.rowReserve = max(m.rowReserve, nextCapacity)
}

func (m *queryTerminalMemory) reserveIndexes(identityCapacity int, factCapacity int) {
	if m == nil {
		return
	}
	if identityCapacity > 0 {
		if m.identityRows.reserve(identityCapacity) {
			m.rebuildIdentityRows()
		}
		m.identityIndexReserve = max(m.identityIndexReserve, identityCapacity)
	}
	if factCapacity > 0 {
		m.factRows.reserve(factCapacity)
		m.factIndexReserve = max(m.factIndexReserve, factCapacity)
	}
}

func (m *queryTerminalMemory) clear() {
	if m == nil {
		return
	}
	for i := range m.rows {
		m.rows[i] = queryTerminalRow{}
	}
	m.rows = m.rows[:0]
	m.identityRows.clear()
	m.factRows.clear(m.recycleBucketRest)
	m.factRowsDirty = false
}

func (m *queryTerminalMemory) len() int {
	if m == nil {
		return 0
	}
	return len(m.rows)
}

func (m *queryTerminalMemory) row(rowID graphTokenRowID) *queryTerminalRow {
	if m == nil || rowID < 0 {
		return nil
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return nil
	}
	return &m.rows[index]
}

func (m *queryTerminalMemory) insertRow(token tokenRef) bool {
	if m == nil || token.isZero() {
		return false
	}
	identityHash := hashTokenIdentityBucketKey(token.identityKey())
	for ref := m.identityRows.headHash(identityHash); ref != 0; {
		rowID := graphTokenRowRefID(ref)
		row := m.row(rowID)
		if row != nil {
			ref = row.identityNext
		} else {
			ref = 0
		}
		if row == nil || row.identityHash != identityHash || !tokenRefEqual(row.token, token) {
			continue
		}
		row.supportCount++
		return false
	}

	rowID := graphTokenRowID(len(m.rows))
	m.ensureRowCapacity(int(rowID) + 1)
	m.ensureIdentityRowsCapacity(int(rowID) + 1)
	m.rows = m.rows[:int(rowID)+1]
	m.rows[rowID] = queryTerminalRow{
		token:        token,
		identityHash: identityHash,
		supportCount: 1,
	}
	m.appendIdentityHashIndexRow(identityHash, rowID)
	m.markFactRowsDirty()
	return true
}

func (m *queryTerminalMemory) refreshTokensContainingFact(id FactID, refresh func(tokenRef) (tokenRef, bool)) {
	if m == nil || id.IsZero() || refresh == nil {
		return
	}
	m.ensureFactRows()
	bucket, ok := m.factRows.get(id)
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
		row := m.row(rowID)
		if row == nil || row.token.isZero() || row.supportCount <= 0 || !row.token.containsFact(id) {
			continue
		}
		beforeIdentityHash := row.identityHash
		next, ok := refresh(row.token)
		if !ok || next.isZero() {
			continue
		}
		m.replaceRowTokenWithPreviousIdentityHash(rowID, beforeIdentityHash, next)
	}
}

func (m *queryTerminalMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	m.ensureFactRows()
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	removed := 0
	for {
		bucket, ok := m.factRows.get(id)
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

func (m *queryTerminalMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(tokenRef)) {
	if m == nil || id.IsZero() {
		return
	}
	m.ensureFactRows()
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	bucket, ok := m.factRows.get(id)
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
		if fn != nil {
			fn(row.token)
		}
	}
}

func (m *queryTerminalMemory) removeToken(token tokenRef, counters *propagationCounterLedger) bool {
	if m == nil || token.isZero() {
		return false
	}
	if counters != nil {
		counters.recordRemovalIndexLookup()
	}
	identityHash := hashTokenIdentityBucketKey(token.identityKey())
	if m.identityRows.headHash(identityHash) == 0 {
		return false
	}
	for ref := m.identityRows.headHash(identityHash); ref != 0; {
		rowID := graphTokenRowRefID(ref)
		row := m.row(rowID)
		if row != nil {
			ref = row.identityNext
		} else {
			ref = 0
		}
		if row == nil || row.identityHash != identityHash {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		if row.supportCount > 1 {
			row.supportCount--
			return false
		}
		m.removeRow(rowID, counters)
		return true
	}
	return false
}

func (m *queryTerminalMemory) replaceRowTokenWithPreviousIdentityHash(rowID graphTokenRowID, previousIdentityHash uint64, token tokenRef) {
	if m == nil || rowID < 0 || token.isZero() {
		return
	}
	row := m.row(rowID)
	if row == nil || row.token.isZero() {
		return
	}
	nextIdentityHash := hashTokenIdentityBucketKey(token.identityKey())
	if previousIdentityHash == 0 {
		previousIdentityHash = row.identityHash
	}
	if previousIdentityHash != nextIdentityHash {
		m.ensureIdentityRowsCapacity(len(m.rows))
		m.removeIdentityHashIndexRow(previousIdentityHash, rowID)
		m.appendIdentityHashIndexRow(nextIdentityHash, rowID)
		row.identityHash = nextIdentityHash
	}
	row.token = token
	m.markFactRowsDirty()
}

func (m *queryTerminalMemory) removeRow(rowID graphTokenRowID, counters *propagationCounterLedger) {
	if m == nil || rowID < 0 {
		return
	}
	index := int(rowID)
	if index < 0 || index >= len(m.rows) {
		return
	}
	removed := m.rows[index]
	m.removeIdentityHashIndexRow(removed.identityHash, rowID)
	if !m.factRowsDirty {
		m.removeTokenFacts(removed.token, rowID)
	}
	last := len(m.rows) - 1
	if index != last {
		moved := m.rows[last]
		m.rows[index] = moved
		if counters != nil {
			counters.recordRemovalRowMoved()
		}
		m.replaceIdentityHashIndexRow(moved.identityHash, graphTokenRowID(last), rowID)
		if !m.factRowsDirty {
			m.replaceTokenFactRows(moved.token, graphTokenRowID(last), rowID)
		}
	}
	m.rows[last] = queryTerminalRow{}
	m.rows = m.rows[:last]
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m *queryTerminalMemory) ensureIdentityRowsCapacity(rowCapacity int) {
	if m == nil {
		return
	}
	if m.identityRows.reserve(max(8, rowCapacity)) {
		m.rebuildIdentityRows()
	}
}

func (m *queryTerminalMemory) rebuildIdentityRows() {
	if m == nil {
		return
	}
	m.identityRows.clear()
	for index := range m.rows {
		m.rows[index].identityNext = 0
	}
	for index := range m.rows {
		row := &m.rows[index]
		if row.token.isZero() {
			continue
		}
		hash := row.identityHash
		if hash == 0 {
			hash = hashTokenIdentityBucketKey(row.token.identityKey())
			row.identityHash = hash
		}
		m.appendIdentityHashIndexRow(hash, graphTokenRowID(index))
	}
}

func (m *queryTerminalMemory) appendIdentityHashIndexRow(hash uint64, id graphTokenRowID) {
	if m == nil {
		return
	}
	row := m.row(id)
	if row == nil {
		return
	}
	head := m.identityRows.headHash(hash)
	row.identityNext = head
	m.identityRows.setHeadHash(hash, graphTokenRowIDRef(id))
}

func (m *queryTerminalMemory) removeIdentityHashIndexRow(hash uint64, rowID graphTokenRowID) bool {
	if m == nil || len(m.identityRows.heads) == 0 {
		return false
	}
	var previous *queryTerminalRow
	for ref := m.identityRows.headHash(hash); ref != 0; {
		currentID := graphTokenRowRefID(ref)
		current := m.row(currentID)
		if current == nil {
			break
		}
		next := current.identityNext
		if currentID == rowID {
			if previous == nil {
				m.identityRows.setHeadHash(hash, next)
			} else {
				previous.identityNext = next
			}
			current.identityNext = 0
			return true
		}
		previous = current
		ref = next
	}
	return false
}

func (m *queryTerminalMemory) replaceIdentityHashIndexRow(hash uint64, oldID, newID graphTokenRowID) bool {
	if m == nil || len(m.identityRows.heads) == 0 || oldID == newID {
		return false
	}
	oldRef := graphTokenRowIDRef(oldID)
	newRef := graphTokenRowIDRef(newID)
	for ref := m.identityRows.headHash(hash); ref != 0; {
		currentID := graphTokenRowRefID(ref)
		current := m.row(currentID)
		if current == nil {
			break
		}
		if ref == oldRef {
			m.identityRows.setHeadHash(hash, newRef)
			return true
		}
		if current.identityNext == oldRef {
			current.identityNext = newRef
			return true
		}
		ref = current.identityNext
	}
	return false
}

func (m *queryTerminalMemory) appendBucketRow(bucket *graphTokenRowIDBucket, id graphTokenRowID) {
	if m == nil || bucket == nil {
		return
	}
	if bucket.count == 0 {
		bucket.first = id
		bucket.count = 1
		return
	}
	if bucket.count == 1 {
		bucket.second = id
		bucket.count = 2
		return
	}
	if bucket.rest == nil {
		bucket.rest = m.takeBucketRest()
	}
	bucket.rest = append(bucket.rest, id)
	bucket.count++
}

func (m *queryTerminalMemory) appendFactIndexRow(key FactID, id graphTokenRowID) {
	if m == nil {
		return
	}
	if graphTokenBucketNeedsGrow(m.factRows.used+1, len(m.factRows.entries)) {
		m.factRows.rehash(max(8, len(m.factRows.entries)*2))
	}
	index, ok := m.factRows.findInsert(key)
	if !ok {
		if m.factRows.entries[index].state == graphTokenBucketEmpty {
			m.factRows.touched = append(m.factRows.touched, index)
			m.factRows.used++
		}
		m.factRows.entries[index] = factTokenBucketEntry{key: key, state: graphTokenBucketFull}
		m.factRows.count++
	}
	m.appendBucketRow(&m.factRows.entries[index].bucket, id)
}

func (m *queryTerminalMemory) takeBucketRest() []graphTokenRowID {
	if m == nil || len(m.bucketRestFree) == 0 {
		return make([]graphTokenRowID, 0, 8)
	}
	last := len(m.bucketRestFree) - 1
	rest := m.bucketRestFree[last]
	m.bucketRestFree[last] = nil
	m.bucketRestFree = m.bucketRestFree[:last]
	return rest[:0]
}

func (m *queryTerminalMemory) recycleBucketRest(rest []graphTokenRowID) {
	if m == nil || cap(rest) == 0 {
		return
	}
	clear(rest)
	m.bucketRestFree = append(m.bucketRestFree, rest[:0])
}

func (m *queryTerminalMemory) markFactRowsDirty() {
	if m == nil {
		return
	}
	m.factRowsDirty = true
}

func (m *queryTerminalMemory) ensureFactRows() {
	if m == nil || !m.factRowsDirty {
		return
	}
	m.rebuildFactRows()
}

func (m *queryTerminalMemory) rebuildFactRows() {
	if m == nil {
		return
	}
	m.factRows.clear(m.recycleBucketRest)
	m.factRows.reserve(max(m.factIndexReserve, len(m.rows)))
	for index, row := range m.rows {
		if row.token.isZero() {
			continue
		}
		m.indexTokenFacts(row.token, graphTokenRowID(index))
	}
	m.factRowsDirty = false
}

func (m *queryTerminalMemory) indexTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			m.appendFactIndexRow(id, rowID)
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		match, ok := row.conditionMatch()
		if !ok {
			return
		}
		id := match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		m.appendFactIndexRow(id, rowID)
	}
}

func (m *queryTerminalMemory) removeTokenFacts(token tokenRef, rowID graphTokenRowID) {
	if m == nil || m.factRows.isEmpty() || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket, ok := m.factRows.get(id)
			if !ok || !bucket.remove(rowID) {
				continue
			}
			if bucket.len() == 0 {
				m.recycleBucketRest(bucket.rest)
				m.factRows.delete(id)
			} else {
				m.factRows.set(id, bucket)
			}
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		match, ok := row.conditionMatch()
		if !ok {
			return
		}
		id := match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket, ok := m.factRows.get(id)
		if !ok || !bucket.remove(rowID) {
			continue
		}
		if bucket.len() == 0 {
			m.recycleBucketRest(bucket.rest)
			m.factRows.delete(id)
		} else {
			m.factRows.set(id, bucket)
		}
	}
}

func (m *queryTerminalMemory) replaceTokenFactRows(token tokenRef, oldID, newID graphTokenRowID) {
	if m == nil || m.factRows.isEmpty() || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, id := range factIDs {
			if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
				continue
			}
			bucket, ok := m.factRows.get(id)
			if ok && bucket.replace(oldID, newID) {
				m.factRows.set(id, bucket)
			}
		}
		return
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return
		}
		match, ok := row.conditionMatch()
		if !ok {
			return
		}
		id := match.fact.ID()
		if id.IsZero() || current.parent().containsFact(id) {
			continue
		}
		bucket, ok := m.factRows.get(id)
		if ok && bucket.replace(oldID, newID) {
			m.factRows.set(id, bucket)
		}
	}
}

func (m queryTerminalMemory) factIndexKeyCount() int {
	if !m.factRowsDirty {
		return m.factRows.keyCount()
	}
	if len(m.rows) == 0 {
		return 0
	}
	seen := make(map[FactID]struct{}, len(m.rows))
	for _, row := range m.rows {
		if row.token.isZero() {
			continue
		}
		if factIDs, ok := row.token.factIDs(); ok {
			for i, id := range factIDs {
				if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
					continue
				}
				seen[id] = struct{}{}
			}
			continue
		}
		for current := row.token; !current.isZero(); current = current.parent() {
			tokenRow, ok := current.resolve()
			if !ok {
				break
			}
			match, ok := tokenRow.conditionMatch()
			if !ok {
				break
			}
			id := match.fact.ID()
			if id.IsZero() || current.parent().containsFact(id) {
				continue
			}
			seen[id] = struct{}{}
		}
	}
	return len(seen)
}

func (m queryTerminalMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	return reteGraphTokenMemoryDiagnostics{
		Rows:              len(m.rows),
		IdentityIndexKeys: m.identityRows.keyCount(),
		FactIndexKeys:     m.factIndexKeyCount(),
	}
}

func (m terminalTokenMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	return reteGraphTokenMemoryDiagnostics{
		Rows:              len(m.rows),
		IdentityIndexKeys: m.identityRows.keyCount(),
		FactIndexKeys:     m.factIndexKeyCount(),
	}
}
