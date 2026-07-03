package engine

const (
	graphTokenBucketEmpty uint8 = iota
	graphTokenBucketFull
	graphTokenBucketDeleted
)

// betaJoinBucketTable stores join rows in one shared arena with per-slot
// chain links, so table growth rebuilds slot heads without reallocating or
// copying row storage. Free arena rows are zeroed and linked through freeHead.
// byIdentity indexes live rows by token identity hash; it is built on the
// first token removal so insert-only memories never pay for it.
type betaJoinBucketTable struct {
	heads      []int32
	tails      []int32
	rows       []betaTokenRow
	next       []int32
	prev       []int32
	byIdentity map[uint64][]int32
	freeHead   int32
	touched    []int
	slotCount  int
	rowCount   int
}

func (t *betaJoinBucketTable) ensureIdentityIndex() {
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

func (t *betaJoinBucketTable) indexIdentity(ref int32) {
	if t.byIdentity == nil {
		return
	}
	state := t.rows[ref-1].token.identityState()
	t.byIdentity[state] = append(t.byIdentity[state], ref)
}

func (t *betaJoinBucketTable) unindexIdentity(ref int32) {
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
// using the identity index instead of scanning slot chains. The filter, when
// non-nil, must also accept the row.
func (t *betaJoinBucketTable) removeIdentityToken(token tokenRef, filter func(*betaTokenRow) bool, onTouch func()) (betaTokenRow, bool) {
	if t == nil || token.isZero() {
		return betaTokenRow{}, false
	}
	if _, ok := token.resolve(); !ok {
		return t.removeIdentityTokenScan(token, filter, onTouch)
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
		if filter != nil && !filter(row) {
			continue
		}
		removed := *row
		t.unlink(ref)
		return removed, true
	}
	return betaTokenRow{}, false
}

func (t *betaJoinBucketTable) removeIdentityTokenScan(token tokenRef, filter func(*betaTokenRow) bool, onTouch func()) (betaTokenRow, bool) {
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
		if filter != nil && !filter(row) {
			continue
		}
		removed := *row
		t.unlink(int32(i + 1))
		return removed, true
	}
	return betaTokenRow{}, false
}

func (t *betaJoinBucketTable) reserve(capacity int) bool {
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

func (t *betaJoinBucketTable) chainRow(ref int32) {
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

func (t *betaJoinBucketTable) insert(row betaTokenRow) bool {
	if t == nil || row.token.isZero() {
		return false
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
	row.token.retain()
	return true
}

func (t *betaJoinBucketTable) unlink(ref int32) bool {
	if t == nil || ref <= 0 || int(ref) > len(t.rows) || t.rows[ref-1].token.isZero() {
		return false
	}
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
	t.rows[ref-1].token.release()
	t.rows[ref-1] = betaTokenRow{}
	t.next[ref-1] = t.freeHead
	t.prev[ref-1] = 0
	t.freeHead = ref
	t.rowCount--
	return true
}

// removeMatching walks every touched slot chain once, unlinking rows the
// match function selects with O(1) per removal. onRemove sees the removed row
// before it is recycled; onTouch runs per live row visited.
func (t *betaJoinBucketTable) removeMatching(match func(*betaTokenRow) bool, onTouch func(), onRemove func(betaTokenRow)) int {
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

func (t *betaJoinBucketTable) forEachChainRow(key betaJoinKey, fn func(ref int32, row *betaTokenRow) bool) {
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

func (t *betaJoinBucketTable) clear() {
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

func (t *betaJoinBucketTable) isEmpty() bool {
	return t == nil || t.rowCount == 0
}

func (t *betaJoinBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.slotCount
}

func (t *betaJoinBucketTable) len() int {
	if t == nil {
		return 0
	}
	return t.rowCount
}

func (t *betaJoinBucketTable) slot(key betaJoinKey) int {
	return int(hashBetaJoinTokenBucketKey(key) & uint64(len(t.heads)-1))
}

type tokenIdentityHeadTable struct {
	heads   []graphTokenRowID
	touched []int
	count   int
}

func graphTokenRowIDRef(id graphTokenRowID) graphTokenRowID {
	return id + 1
}

func graphTokenRowRefID(ref graphTokenRowID) graphTokenRowID {
	return ref - 1
}

func (t *tokenIdentityHeadTable) reserve(capacity int) bool {
	if capacity <= 0 {
		return false
	}
	slotCapacity := tokenIdentityBucketSlotCapacity(capacity)
	if slotCapacity <= len(t.heads) {
		return false
	}
	t.heads = make([]graphTokenRowID, graphTokenBucketPowerOfTwo(max(8, slotCapacity)))
	t.touched = t.touched[:0]
	t.count = 0
	return true
}

func (t *tokenIdentityHeadTable) clear() {
	if t == nil || len(t.heads) == 0 {
		return
	}
	for _, index := range t.touched {
		if index >= 0 && index < len(t.heads) {
			t.heads[index] = 0
		}
	}
	t.touched = t.touched[:0]
	t.count = 0
}

func (t *tokenIdentityHeadTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.count
}

func (t *tokenIdentityHeadTable) headHash(hash uint64) graphTokenRowID {
	if t == nil || len(t.heads) == 0 {
		return 0
	}
	return t.heads[t.slot(hash)]
}

func (t *tokenIdentityHeadTable) setHeadHash(hash uint64, head graphTokenRowID) {
	if t == nil || len(t.heads) == 0 {
		return
	}
	index := t.slot(hash)
	if t.heads[index] == 0 && head != 0 {
		t.touched = append(t.touched, index)
		t.count++
	} else if t.heads[index] != 0 && head == 0 {
		t.count--
	}
	t.heads[index] = head
}

func (t *tokenIdentityHeadTable) slot(hash uint64) int {
	return int(hash & uint64(len(t.heads)-1))
}

type graphTokenIdentityBucketEntry struct {
	key    graphTokenIdentityKey
	bucket graphTokenRowIDBucket
	state  uint8
}

type graphTokenIdentityBucketTable struct {
	entries []graphTokenIdentityBucketEntry
	touched []int
	count   int
	used    int
}

func (t *graphTokenIdentityBucketTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	t.rehash(graphTokenBucketSlotCapacity(capacity))
}

func (t *graphTokenIdentityBucketTable) isEmpty() bool {
	return t == nil || t.count == 0
}

func (t *graphTokenIdentityBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.count
}

func (t *graphTokenIdentityBucketTable) get(key graphTokenIdentityKey) (graphTokenRowIDBucket, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return graphTokenRowIDBucket{}, false
	}
	index, ok := t.find(key)
	if !ok {
		return graphTokenRowIDBucket{}, false
	}
	return t.entries[index].bucket, true
}

func (t *graphTokenIdentityBucketTable) set(key graphTokenIdentityKey, bucket graphTokenRowIDBucket) {
	if t == nil {
		return
	}
	if graphTokenBucketNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(max(8, len(t.entries)*2))
	}
	index, ok := t.findInsert(key)
	if ok {
		t.entries[index].bucket = bucket
		return
	}
	if t.entries[index].state == graphTokenBucketEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = graphTokenIdentityBucketEntry{key: key, bucket: bucket, state: graphTokenBucketFull}
	t.count++
}

func (t *graphTokenIdentityBucketTable) delete(key graphTokenIdentityKey) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	t.entries[index] = graphTokenIdentityBucketEntry{state: graphTokenBucketDeleted}
	t.count--
}

func (t *graphTokenIdentityBucketTable) clear(recycle func([]graphTokenRowID)) {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for _, index := range t.touched {
		if index < 0 || index >= len(t.entries) {
			continue
		}
		if t.entries[index].state == graphTokenBucketFull && recycle != nil {
			recycle(t.entries[index].bucket.rest)
		}
		t.entries[index] = graphTokenIdentityBucketEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t *graphTokenIdentityBucketTable) find(key graphTokenIdentityKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashGraphTokenIdentityBucketKey(key) & mask)
	for {
		entry := t.entries[index]
		if entry.state == graphTokenBucketEmpty {
			return 0, false
		}
		if entry.state == graphTokenBucketFull && entry.key == key {
			return index, true
		}
		index = (index + 1) & int(mask)
	}
}

func (t *graphTokenIdentityBucketTable) findInsert(key graphTokenIdentityKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashGraphTokenIdentityBucketKey(key) & mask)
	firstDeleted := -1
	for {
		entry := t.entries[index]
		switch entry.state {
		case graphTokenBucketEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case graphTokenBucketDeleted:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		case graphTokenBucketFull:
			if entry.key == key {
				return index, true
			}
		}
		index = (index + 1) & int(mask)
	}
}

func (t *graphTokenIdentityBucketTable) rehash(slotCapacity int) {
	slotCapacity = graphTokenBucketPowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]graphTokenIdentityBucketEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state == graphTokenBucketFull {
			t.set(old[i].key, old[i].bucket)
		}
	}
}

type factTokenBucketEntry struct {
	key    FactID
	bucket graphTokenRowIDBucket
	state  uint8
}

type factTokenBucketTable struct {
	entries []factTokenBucketEntry
	touched []int
	count   int
	used    int
}

func (t *factTokenBucketTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	t.rehash(graphTokenBucketSlotCapacity(capacity))
}

func (t *factTokenBucketTable) isEmpty() bool {
	return t == nil || t.count == 0
}

func (t *factTokenBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.count
}

func (t *factTokenBucketTable) get(key FactID) (graphTokenRowIDBucket, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return graphTokenRowIDBucket{}, false
	}
	index, ok := t.find(key)
	if !ok {
		return graphTokenRowIDBucket{}, false
	}
	return t.entries[index].bucket, true
}

func (t *factTokenBucketTable) set(key FactID, bucket graphTokenRowIDBucket) {
	if t == nil {
		return
	}
	if graphTokenBucketNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(max(8, len(t.entries)*2))
	}
	index, ok := t.findInsert(key)
	if ok {
		t.entries[index].bucket = bucket
		return
	}
	if t.entries[index].state == graphTokenBucketEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = factTokenBucketEntry{key: key, bucket: bucket, state: graphTokenBucketFull}
	t.count++
}

func (t *factTokenBucketTable) delete(key FactID) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	t.entries[index] = factTokenBucketEntry{state: graphTokenBucketDeleted}
	t.count--
}

func (t *factTokenBucketTable) clear(recycle func([]graphTokenRowID)) {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for _, index := range t.touched {
		if index < 0 || index >= len(t.entries) {
			continue
		}
		if t.entries[index].state == graphTokenBucketFull && recycle != nil {
			recycle(t.entries[index].bucket.rest)
		}
		t.entries[index] = factTokenBucketEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t *factTokenBucketTable) find(key FactID) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashFactTokenBucketKey(key) & mask)
	for {
		entry := t.entries[index]
		if entry.state == graphTokenBucketEmpty {
			return 0, false
		}
		if entry.state == graphTokenBucketFull && entry.key == key {
			return index, true
		}
		index = (index + 1) & int(mask)
	}
}

func (t *factTokenBucketTable) findInsert(key FactID) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashFactTokenBucketKey(key) & mask)
	firstDeleted := -1
	for {
		entry := t.entries[index]
		switch entry.state {
		case graphTokenBucketEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case graphTokenBucketDeleted:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		case graphTokenBucketFull:
			if entry.key == key {
				return index, true
			}
		}
		index = (index + 1) & int(mask)
	}
}

func (t *factTokenBucketTable) rehash(slotCapacity int) {
	slotCapacity = graphTokenBucketPowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]factTokenBucketEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state == graphTokenBucketFull {
			t.set(old[i].key, old[i].bucket)
		}
	}
}

func graphTokenBucketSlotCapacity(capacity int) int {
	if capacity <= 0 {
		return 0
	}
	return graphTokenBucketPowerOfTwo(max(8, capacity*2))
}

func tokenIdentityBucketSlotCapacity(capacity int) int {
	if capacity <= 0 {
		return 0
	}
	return graphTokenBucketPowerOfTwo(max(8, (capacity*10+8)/9))
}

func graphTokenBucketNeedsGrow(used, slots int) bool {
	return slots == 0 || used*4 >= slots*3
}

func graphTokenBucketPowerOfTwo(value int) int {
	if value <= 8 {
		return 8
	}
	out := 1
	for out < value {
		out <<= 1
	}
	return out
}

func hashTokenIdentityBucketKey(key tokenIdentityKey) uint64 {
	hash := uint64(0x9e3779b97f4a7c15)
	hash = graphTokenBucketMixAdd(hash, uint64(key.size))
	hash = graphTokenBucketMixAdd(hash, uint64(key.generation))
	hash = graphTokenBucketMixAdd(hash, key.identityState)
	return hash
}

func hashGraphTokenIdentityBucketKey(key graphTokenIdentityKey) uint64 {
	return hashTokenIdentityBucketKey(key)
}

func hashFactTokenBucketKey(key FactID) uint64 {
	hash := uint64(0x9e3779b97f4a7c15)
	hash = graphTokenBucketMixAdd(hash, uint64(key.generation))
	hash = graphTokenBucketMixAdd(hash, key.sequence)
	return hash
}

func hashBetaJoinTokenBucketKey(key betaJoinKey) uint64 {
	hash := uint64(0x9e3779b97f4a7c15)
	hash = graphTokenBucketMixAdd(hash, uint64(key.kind))
	hash = hashBetaJoinTokenBucketValue(hash, key.kind, key.boolValue, key.intValue, key.floatBits, key.stringValue)
	hash = graphTokenBucketMixAdd(hash, uint64(key.secondKind))
	hash = hashBetaJoinTokenBucketValue(hash, key.secondKind, key.secondBoolValue, key.secondIntValue, key.secondFloatBits, key.secondStringValue)
	if key.thirdKind != betaJoinKeyUnknown {
		hash = graphTokenBucketMixAdd(hash, uint64(key.thirdKind))
		hash = hashBetaJoinTokenBucketValue(hash, key.thirdKind, key.thirdBoolValue, key.thirdIntValue, key.thirdFloatBits, key.thirdStringValue)
	}
	return hash
}

func hashBetaJoinTokenBucketValue(hash uint64, kind betaJoinKeyKind, boolValue bool, intValue int64, floatBits uint64, stringValue string) uint64 {
	switch kind {
	case betaJoinKeyBool:
		if boolValue {
			return graphTokenBucketMixAdd(hash, 1)
		}
		return graphTokenBucketMixAdd(hash, 0)
	case betaJoinKeyInt:
		return graphTokenBucketMixAdd(hash, uint64(intValue))
	case betaJoinKeyFloat, betaJoinKeyTokenIdentity:
		return graphTokenBucketMixAdd(hash, floatBits)
	case betaJoinKeyString, betaJoinKeyCanonical:
		return graphTokenBucketMixString(hash, stringValue)
	default:
		return hash
	}
}

func graphTokenBucketMixString(hash uint64, value string) uint64 {
	hash = graphTokenBucketMixAdd(hash, uint64(len(value)))
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= 1099511628211
	}
	return graphTokenBucketAvalanche(hash)
}

func graphTokenBucketMixAdd(hash, value uint64) uint64 {
	return graphTokenBucketAvalanche(hash ^ graphTokenBucketAvalanche(value+0x9e3779b97f4a7c15))
}

func graphTokenBucketAvalanche(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}
