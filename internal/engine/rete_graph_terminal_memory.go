package engine

type terminalTokenMemory struct {
	arena                *tokenArena
	rows                 []terminalTokenRow
	identityRows         tokenIdentityHeadTable
	factRows             factTokenBucketTable
	freeRowIDs           []graphTokenRowID
	liveRows             int
	supportCounts        []uint32
	rowReserve           int
	identityIndexReserve int
	factIndexReserve     int
	factRowsDirty        bool
	bucketRestFree       [][]graphTokenRowID
	branchRows           []*terminalBranchSupportState
}

type terminalTokenRow struct {
	identityHash  uint64
	candidateHash uint64
	token         terminalTokenRef
	handleGen     uint32
	identityNext  uint32
}

type terminalTokenRef struct {
	rowID      tokenArenaRowID
	generation uint32
}

func terminalTokenRefFromToken(token tokenRef) terminalTokenRef {
	if token.isZero() || token.handle.generation > uint64(^uint32(0)) {
		return terminalTokenRef{}
	}
	return terminalTokenRef{
		rowID:      token.handle.rowID,
		generation: uint32(token.handle.generation),
	}
}

func (r terminalTokenRef) isZero() bool {
	return r.rowID == 0 || r.generation == 0
}

func (r terminalTokenRef) toTokenRef(arena *tokenArena) tokenRef {
	if r.isZero() || arena == nil {
		return tokenRef{}
	}
	return tokenRef{handle: tokenHandle{
		arena:      arena,
		rowID:      r.rowID,
		generation: uint64(r.generation),
	}}
}

type terminalBranchSupportState struct {
	primary  terminalBranchSupport
	overflow *terminalBranchSupportOverflow
	count    int
}

func (s *terminalBranchSupportState) overflowItems() []terminalBranchSupport {
	if s == nil || s.overflow == nil {
		return nil
	}
	return s.overflow.items
}

func (s *terminalBranchSupportState) add(branchID int) {
	if s == nil || branchID < 0 {
		return
	}
	if s.count == 0 {
		s.primary = terminalBranchSupport{branchID: branchID, count: 1}
		s.count = 1
		return
	}
	if s.primary.branchID == branchID {
		s.primary.count++
		return
	}
	overflow := s.overflowItems()
	for i := 0; i < s.count-1 && i < len(overflow); i++ {
		if overflow[i].branchID == branchID {
			overflow[i].count++
			return
		}
	}
	if s.overflow == nil {
		s.overflow = &terminalBranchSupportOverflow{}
	}
	s.overflow.items = append(s.overflow.items, terminalBranchSupport{branchID: branchID, count: 1})
	s.count++
}

func (s *terminalBranchSupportState) has(branchID int) bool {
	if branchID < 0 || s == nil || s.count == 0 {
		return false
	}
	if s.primary.branchID == branchID && s.primary.count > 0 {
		return true
	}
	overflow := s.overflowItems()
	for i := 0; i < s.count-1 && i < len(overflow); i++ {
		support := overflow[i]
		if support.branchID == branchID && support.count > 0 {
			return true
		}
	}
	return false
}

func (s *terminalBranchSupportState) remove(branchID int) bool {
	if s == nil || branchID < 0 || s.count == 0 {
		return false
	}
	if s.primary.branchID == branchID {
		s.primary.count--
		if s.primary.count > 0 {
			return true
		}
		s.removeAt(0)
		return s.count > 0
	}
	overflow := s.overflowItems()
	for i := 0; i < s.count-1 && i < len(overflow); i++ {
		if overflow[i].branchID != branchID {
			continue
		}
		overflow[i].count--
		if overflow[i].count > 0 {
			return true
		}
		s.removeAt(i + 1)
		return s.count > 0
	}
	return s.count > 0
}

func (s *terminalBranchSupportState) removeAt(index int) {
	if s == nil || index < 0 || index >= s.count {
		return
	}
	overflow := s.overflowItems()
	overflowCount := min(s.count-1, len(overflow))
	if s.count == 1 || overflowCount == 0 {
		*s = terminalBranchSupportState{}
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
		s.primary = last
	} else if index-1 < len(overflow) {
		overflow[index-1] = last
	}
	s.count = overflowCount
	if len(overflow) == 0 {
		s.overflow = nil
	} else {
		s.overflow.items = overflow
	}
}

func (s *terminalBranchSupportState) ids() []int {
	if s == nil || s.count == 0 {
		return nil
	}
	out := make([]int, 0, s.count)
	s.forEach(func(support terminalBranchSupport) {
		if support.count <= 0 {
			return
		}
		out = append(out, support.branchID)
	})
	return out
}

func (s *terminalBranchSupportState) forEach(fn func(terminalBranchSupport)) {
	if s == nil || s.count == 0 || fn == nil {
		return
	}
	fn(s.primary)
	overflow := s.overflowItems()
	for i := 0; i < s.count-1 && i < len(overflow); i++ {
		fn(overflow[i])
	}
}

type queryTerminalMemory struct {
	rows              []queryTerminalRow
	rowByToken        map[tokenHandle]int
	rowByTokenReserve int
	rowReserve        int
}

type queryTerminalRow struct {
	token tokenRef
}

func (r terminalTokenRow) isTerminal() bool {
	return !r.token.isZero()
}

func terminalIdentityRef(id graphTokenRowID) uint32 {
	if id < 0 || uint64(id) >= uint64(^uint32(0)) {
		return 0
	}
	return uint32(id) + 1
}

func terminalIdentityRefID(ref uint32) graphTokenRowID {
	if ref == 0 {
		return -1
	}
	return graphTokenRowID(ref - 1)
}

func terminalIdentityRefValue(ref graphTokenRowID) uint32 {
	if ref <= 0 || uint64(ref) > uint64(^uint32(0)) {
		return 0
	}
	return uint32(ref)
}

func terminalIdentityGraphRef(ref uint32) graphTokenRowID {
	return graphTokenRowID(ref)
}

func (m *terminalTokenMemory) branchState(rowID graphTokenRowID) *terminalBranchSupportState {
	if m == nil || rowID < 0 || int(rowID) >= len(m.branchRows) {
		return nil
	}
	return m.branchRows[rowID]
}

func (m *terminalTokenMemory) ensureBranchRowsCapacity(rowID graphTokenRowID) {
	if m == nil || rowID < 0 || int(rowID) < len(m.branchRows) {
		return
	}
	next := make([]*terminalBranchSupportState, len(m.rows))
	copy(next, m.branchRows)
	m.branchRows = next
}

func (m *terminalTokenMemory) addTerminalBranchSupport(rowID graphTokenRowID, branchID int) {
	if m == nil || rowID < 0 || branchID < 0 {
		return
	}
	m.ensureBranchRowsCapacity(rowID)
	if int(rowID) >= len(m.branchRows) {
		return
	}
	if m.branchRows[rowID] == nil {
		m.branchRows[rowID] = &terminalBranchSupportState{}
	}
	m.branchRows[rowID].add(branchID)
}

func (m *terminalTokenMemory) hasTerminalBranchSupport(rowID graphTokenRowID, branchID int) bool {
	return m.branchState(rowID).has(branchID)
}

func (m *terminalTokenMemory) removeTerminalBranchSupport(rowID graphTokenRowID, branchID int) {
	state := m.branchState(rowID)
	if state == nil {
		return
	}
	if !state.remove(branchID) {
		m.branchRows[rowID] = nil
	}
}

func (m *terminalTokenMemory) terminalBranchIDs(rowID graphTokenRowID) []int {
	return m.branchState(rowID).ids()
}

func (m *terminalTokenMemory) forEachTerminalBranchSupport(rowID graphTokenRowID, fn func(terminalBranchSupport)) {
	m.branchState(rowID).forEach(fn)
}

func (m *terminalTokenMemory) graphTokenRow(rowID graphTokenRowID, row terminalTokenRow) graphTokenRow {
	token := m.rowToken(row)
	out := graphTokenRow{
		handle:       m.rowHandle(rowID, row),
		token:        token,
		identity:     token.identityKey(),
		candidate:    candidateIdentity{key: candidateIdentityKey{hash: row.candidateHash}},
		supportCount: int(m.terminalSupportCount(rowID, row)),
	}
	if state := m.branchState(rowID); state != nil && state.count > 0 {
		out.branchSupport = state.primary
		out.branchOverflow = state.overflow
		out.branchCount = state.count
	}
	return out
}

func (m *terminalTokenMemory) rowHandle(rowID graphTokenRowID, row terminalTokenRow) graphTokenRowHandle {
	if rowID < 0 || uint64(rowID) >= uint64(^uint32(0)) || row.handleGen == 0 {
		return graphTokenRowHandle{}
	}
	return graphTokenRowHandle{
		id:         graphTokenRowHandleID(uint32(rowID) + 1),
		generation: row.handleGen,
	}
}

func (m *terminalTokenMemory) rowToken(row terminalTokenRow) tokenRef {
	if m == nil {
		return tokenRef{}
	}
	return row.token.toTokenRef(m.arena)
}

func terminalTokenMemoryIdentityKey(token tokenRef, identity candidateIdentity) graphTokenIdentityKey {
	if !identity.isZero() {
		return graphTokenIdentityKey{
			size:          identity.count,
			generation:    identity.generation,
			identityState: identity.key.hash,
		}
	}
	return tokenRefKey(token)
}

func terminalTokenMemoryIdentityEqual(row *terminalTokenRow, rowToken tokenRef, token tokenRef, identity candidateIdentity) bool {
	if row == nil {
		return false
	}
	if !identity.isZero() {
		return row.candidateHash == identity.key.hash
	}
	return tokenRefEqual(rowToken, token)
}

func (m *terminalTokenMemory) bindTokenArena(token tokenRef) bool {
	if m == nil || token.isZero() || token.handle.arena == nil {
		return false
	}
	if m.arena == nil || m.liveRows == 0 {
		m.arena = token.handle.arena
		return true
	}
	return m.arena == token.handle.arena
}

func (m *terminalTokenMemory) terminalSupportCount(rowID graphTokenRowID, row terminalTokenRow) uint32 {
	if row.token.isZero() {
		return 0
	}
	if m != nil && rowID >= 0 && int(rowID) < len(m.supportCounts) {
		if count := m.supportCounts[rowID]; count > 1 {
			return count
		}
	}
	return 1
}

func (m *terminalTokenMemory) ensureSupportCountCapacity(rowID graphTokenRowID) {
	if m == nil || rowID < 0 || int(rowID) < len(m.supportCounts) {
		return
	}
	next := make([]uint32, len(m.rows))
	copy(next, m.supportCounts)
	m.supportCounts = next
}

func (m *terminalTokenMemory) incrementTerminalSupportCount(rowID graphTokenRowID) {
	if m == nil || rowID < 0 {
		return
	}
	row := m.row(rowID)
	if row == nil {
		return
	}
	current := m.terminalSupportCount(rowID, *row)
	if current <= 1 {
		m.ensureSupportCountCapacity(rowID)
		m.supportCounts[rowID] = 2
		return
	}
	m.supportCounts[rowID] = current + 1
}

func (m *terminalTokenMemory) decrementTerminalSupportCount(rowID graphTokenRowID) uint32 {
	if m == nil || rowID < 0 {
		return 0
	}
	row := m.row(rowID)
	if row == nil {
		return 0
	}
	current := m.terminalSupportCount(rowID, *row)
	if current <= 1 {
		return 0
	}
	next := current - 1
	if next <= 1 {
		if int(rowID) < len(m.supportCounts) {
			m.supportCounts[rowID] = 0
		}
		return 1
	}
	m.supportCounts[rowID] = next
	return next
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
	m.rowReserve = max(m.rowReserve, nextCapacity)
}

func (m *terminalTokenMemory) clear() {
	if m == nil {
		return
	}
	m.freeRowIDs = m.freeRowIDs[:0]
	for i := range m.rows {
		m.clearRowForReuse(graphTokenRowID(i))
	}
	clear(m.supportCounts)
	m.liveRows = 0
	m.identityRows.clear()
	m.factRows.clear(m.recycleBucketRest)
	m.factRowsDirty = false
}

func (m *terminalTokenMemory) allocateRowHandle(rowID graphTokenRowID) graphTokenRowHandle {
	if m == nil || rowID < 0 || int(rowID) >= len(m.rows) {
		return graphTokenRowHandle{}
	}
	if uint64(rowID) >= uint64(^uint32(0)) {
		return graphTokenRowHandle{}
	}
	row := &m.rows[int(rowID)]
	if row.handleGen == 0 {
		row.handleGen = 1
	}
	m.liveRows++
	return m.rowHandle(rowID, *row)
}

func (m *terminalTokenMemory) allocateRowID() graphTokenRowID {
	if m == nil {
		return -1
	}
	for len(m.freeRowIDs) > 0 {
		last := len(m.freeRowIDs) - 1
		rowID := m.freeRowIDs[last]
		m.freeRowIDs[last] = 0
		m.freeRowIDs = m.freeRowIDs[:last]
		if rowID < 0 || int(rowID) >= len(m.rows) {
			continue
		}
		if !m.rows[int(rowID)].token.isZero() {
			continue
		}
		return rowID
	}
	rowID := graphTokenRowID(len(m.rows))
	m.ensureRowCapacity(int(rowID) + 1)
	m.rows = m.rows[:int(rowID)+1]
	return rowID
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
	if index < 0 || index >= len(m.rows) {
		return 0, false
	}
	row := &m.rows[index]
	if row.token.isZero() || m.rowHandle(graphTokenRowID(index), *row) != handle {
		return 0, false
	}
	return graphTokenRowID(index), true
}

func (m *terminalTokenMemory) clearRowForReuse(rowID graphTokenRowID) {
	if m == nil || rowID < 0 || int(rowID) >= len(m.rows) {
		return
	}
	row := &m.rows[int(rowID)]
	generation := row.handleGen + 1
	if generation == 0 {
		generation = 1
	}
	*row = terminalTokenRow{handleGen: generation}
	if int(rowID) < len(m.supportCounts) {
		m.supportCounts[rowID] = 0
	}
	if int(rowID) < len(m.branchRows) {
		m.branchRows[rowID] = nil
	}
	m.freeRowIDs = append(m.freeRowIDs, rowID)
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
	row.identityNext = terminalIdentityRefValue(head)
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
	return m.liveRows
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

func (m *terminalTokenMemory) insertFreshTerminalRow(token tokenRef, branchID int, identity candidateIdentity) graphTokenRowHandle {
	if m == nil || token.isZero() || !m.bindTokenArena(token) {
		return graphTokenRowHandle{}
	}
	key := terminalTokenMemoryIdentityKey(token, identity)
	rowID := m.allocateRowID()
	if rowID < 0 {
		return graphTokenRowHandle{}
	}
	m.ensureIdentityRowsCapacity(int(rowID) + 1)
	handle := m.allocateRowHandle(rowID)
	row := terminalTokenRow{
		handleGen:     handle.generation,
		token:         terminalTokenRefFromToken(token),
		identityHash:  hashTokenIdentityBucketKey(key),
		candidateHash: identity.key.hash,
	}
	m.rows[rowID] = row
	m.addTerminalBranchSupport(rowID, branchID)
	m.appendIdentityIndexRow(key, rowID)
	m.markFactRowsDirty()
	return handle
}

func (m *terminalTokenMemory) insertTerminalRow(token tokenRef, branchID int, identity candidateIdentity) (graphTokenRowHandle, bool) {
	if m == nil || token.isZero() || !m.bindTokenArena(token) {
		return graphTokenRowHandle{}, false
	}
	key := terminalTokenMemoryIdentityKey(token, identity)
	identityHash := hashTokenIdentityBucketKey(key)
	for ref := m.identityRows.headHash(identityHash); ref != 0; {
		rowID := terminalIdentityRefID(uint32(ref))
		row := m.row(rowID)
		if row != nil {
			ref = terminalIdentityGraphRef(row.identityNext)
		} else {
			ref = 0
		}
		if row == nil {
			continue
		}
		rowToken := m.rowToken(*row)
		if row.identityHash != identityHash {
			continue
		}
		if !terminalTokenMemoryIdentityEqual(row, rowToken, token, identity) {
			continue
		}
		if rowToken.handle == token.handle {
			row.candidateHash = identity.key.hash
			if !m.hasTerminalBranchSupport(rowID, branchID) {
				m.addTerminalBranchSupport(rowID, branchID)
			}
			return m.rowHandle(rowID, *row), false
		}
		row.candidateHash = identity.key.hash
		m.incrementTerminalSupportCount(rowID)
		m.addTerminalBranchSupport(rowID, branchID)
		return m.rowHandle(rowID, *row), false
	}

	rowID := m.allocateRowID()
	if rowID < 0 {
		return graphTokenRowHandle{}, false
	}
	m.ensureIdentityRowsCapacity(int(rowID) + 1)
	handle := m.allocateRowHandle(rowID)
	row := terminalTokenRow{
		handleGen:     handle.generation,
		token:         terminalTokenRefFromToken(token),
		identityHash:  identityHash,
		candidateHash: identity.key.hash,
	}
	m.rows[rowID] = row
	m.addTerminalBranchSupport(rowID, branchID)
	m.appendIdentityIndexRow(key, rowID)
	m.markFactRowsDirty()
	return handle, true
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
		rowToken := tokenRef{}
		if row != nil {
			rowToken = m.rowToken(*row)
		}
		if row != nil && !rowToken.isZero() && rowToken.containsFact(id) && fn != nil {
			fn(m.graphTokenRow(rowID, *row))
		}
		m.removeRow(rowID, counters)
		removed++
	}
}

func (m *terminalTokenMemory) removeToken(token tokenRef, counters *propagationCounterLedger, branchIDs ...int) (graphTokenRow, bool) {
	return m.removeTokenWithIdentity(token, candidateIdentity{}, counters, branchIDs...)
}

func (m *terminalTokenMemory) removeTokenWithIdentity(token tokenRef, identity candidateIdentity, counters *propagationCounterLedger, branchIDs ...int) (graphTokenRow, bool) {
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
	key := terminalTokenMemoryIdentityKey(token, identity)
	identityHash := hashTokenIdentityBucketKey(key)
	if m.identityRows.headHash(identityHash) == 0 {
		return graphTokenRow{}, false
	}
	for ref := m.identityRows.headHash(identityHash); ref != 0; {
		rowID := terminalIdentityRefID(uint32(ref))
		row := m.row(rowID)
		if row != nil {
			ref = terminalIdentityGraphRef(row.identityNext)
		} else {
			ref = 0
		}
		if row == nil || row.identityHash != identityHash {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		rowToken := m.rowToken(*row)
		if !terminalTokenMemoryIdentityEqual(row, rowToken, token, identity) {
			continue
		}
		if row.isTerminal() && m.terminalSupportCount(rowID, *row) > 1 {
			m.decrementTerminalSupportCount(rowID)
			m.removeTerminalBranchSupport(rowID, branchID)
			return graphTokenRow{}, false
		}
		removed := m.graphTokenRow(rowID, *row)
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
	if row.isTerminal() && m.terminalSupportCount(rowID, *row) > 1 {
		m.decrementTerminalSupportCount(rowID)
		m.removeTerminalBranchSupport(rowID, branchID)
		return graphTokenRow{}, false, true
	}
	removed := m.graphTokenRow(rowID, *row)
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
		rowToken := tokenRef{}
		if row != nil {
			rowToken = m.rowToken(*row)
		}
		if row == nil || rowToken.isZero() || !rowToken.containsFact(id) {
			continue
		}
		if fn != nil {
			fn(m.graphTokenRow(rowID, *row))
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
		m.removeTokenFacts(m.rowToken(removed), rowID)
	}
	m.clearRowForReuse(rowID)
	if m.liveRows > 0 {
		m.liveRows--
	}
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
			hash = hashTokenIdentityBucketKey(m.rowToken(*row).identityKey())
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
		currentID := terminalIdentityRefID(uint32(ref))
		current := m.row(currentID)
		if current == nil {
			break
		}
		next := current.identityNext
		if currentID == rowID {
			if previous == nil {
				m.identityRows.setHeadHash(hash, terminalIdentityGraphRef(next))
			} else {
				previous.identityNext = next
			}
			current.identityNext = 0
			return true
		}
		previous = current
		ref = terminalIdentityGraphRef(next)
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
		currentID := terminalIdentityRefID(uint32(ref))
		current := m.row(currentID)
		if current == nil {
			break
		}
		if ref == oldRef {
			m.identityRows.setHeadHash(hash, newRef)
			return true
		}
		if terminalIdentityGraphRef(current.identityNext) == oldRef {
			current.identityNext = terminalIdentityRefValue(newRef)
			return true
		}
		ref = terminalIdentityGraphRef(current.identityNext)
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
		m.indexTokenFacts(m.rowToken(row), graphTokenRowID(index))
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
		token := m.rowToken(row)
		if factIDs, ok := token.factIDs(); ok {
			for i, id := range factIDs {
				if id.IsZero() || factIDSeenBefore(factIDs[:i], id) {
					continue
				}
				seen[id] = struct{}{}
			}
			continue
		}
		for current := token; !current.isZero(); current = current.parent() {
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

func (m *queryTerminalMemory) clear() {
	if m == nil {
		return
	}
	for i := range m.rows {
		m.rows[i] = queryTerminalRow{}
	}
	m.rows = m.rows[:0]
	for token := range m.rowByToken {
		delete(m.rowByToken, token)
	}
}

func (m *queryTerminalMemory) len() int {
	if m == nil {
		return 0
	}
	return len(m.rows)
}

func (m *queryTerminalMemory) insertRow(token tokenRef) bool {
	if m == nil || token.isZero() {
		return false
	}
	if _, ok := m.rowByToken[token.handle]; ok {
		return false
	}
	rowID := graphTokenRowID(len(m.rows))
	m.ensureRowCapacity(int(rowID) + 1)
	if m.rowByToken == nil {
		m.rowByToken = make(map[tokenHandle]int, max(8, cap(m.rows)))
	}
	m.rows = m.rows[:int(rowID)+1]
	m.rows[rowID] = queryTerminalRow{token: token}
	m.rowByToken[token.handle] = int(rowID)
	m.rowByTokenReserve = max(m.rowByTokenReserve, len(m.rowByToken))
	return true
}

func (m *queryTerminalMemory) removeContainingFact(id FactID, counters *propagationCounterLedger) int {
	if m == nil || id.IsZero() {
		return 0
	}
	removed := 0
	for i := len(m.rows) - 1; i >= 0; i-- {
		row := &m.rows[i]
		if row.token.isZero() {
			continue
		}
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !row.token.containsFact(id) {
			continue
		}
		m.removeRow(graphTokenRowID(i), counters)
		removed++
	}
	return removed
}

func (m *queryTerminalMemory) forEachTokenContainingFact(id FactID, counters *propagationCounterLedger, fn func(tokenRef)) {
	if m == nil || id.IsZero() {
		return
	}
	for i := range m.rows {
		row := &m.rows[i]
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if row.token.isZero() || !row.token.containsFact(id) {
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
	if rowID, ok := m.rowByToken[token.handle]; ok {
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		m.removeRow(graphTokenRowID(rowID), counters)
		return true
	}
	for i := range m.rows {
		row := &m.rows[i]
		if counters != nil {
			counters.recordRemovalRowTouched()
		}
		if !tokenRefEqual(row.token, token) {
			continue
		}
		m.removeRow(graphTokenRowID(i), counters)
		return true
	}
	return false
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
	if m.rowByToken != nil && !removed.token.isZero() {
		delete(m.rowByToken, removed.token.handle)
	}
	last := len(m.rows) - 1
	if index != last {
		m.rows[index] = m.rows[last]
		if m.rowByToken != nil && !m.rows[index].token.isZero() {
			m.rowByToken[m.rows[index].token.handle] = index
		}
		if counters != nil {
			counters.recordRemovalRowMoved()
		}
	}
	m.rows[last] = queryTerminalRow{}
	m.rows = m.rows[:last]
	if counters != nil {
		counters.recordRemovalRowRemoved()
	}
}

func (m queryTerminalMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	return reteGraphTokenMemoryDiagnostics{
		Rows: len(m.rows),
	}
}

func (m terminalTokenMemory) diagnostics() reteGraphTokenMemoryDiagnostics {
	return reteGraphTokenMemoryDiagnostics{
		Rows:              len(m.rows),
		IdentityIndexKeys: m.identityRows.keyCount(),
		FactIndexKeys:     m.factIndexKeyCount(),
	}
}
