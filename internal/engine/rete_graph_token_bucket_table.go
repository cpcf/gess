package engine

const (
	graphTokenBucketEmpty uint8 = iota
	graphTokenBucketFull
	graphTokenBucketDeleted
)

type betaJoinTokenBucketEntry struct {
	key    betaJoinKey
	bucket graphTokenRowIDBucket
	state  uint8
}

type betaJoinTokenBucketTable struct {
	entries []betaJoinTokenBucketEntry
	touched []int
	count   int
	used    int
}

func (t *betaJoinTokenBucketTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	t.rehash(graphTokenBucketSlotCapacity(capacity))
}

func (t *betaJoinTokenBucketTable) isEmpty() bool {
	return t == nil || t.count == 0
}

func (t *betaJoinTokenBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.count
}

func (t *betaJoinTokenBucketTable) get(key betaJoinKey) (graphTokenRowIDBucket, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return graphTokenRowIDBucket{}, false
	}
	index, ok := t.find(key)
	if !ok {
		return graphTokenRowIDBucket{}, false
	}
	return t.entries[index].bucket, true
}

func (t *betaJoinTokenBucketTable) set(key betaJoinKey, bucket graphTokenRowIDBucket) {
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
	t.entries[index] = betaJoinTokenBucketEntry{key: key, bucket: bucket, state: graphTokenBucketFull}
	t.count++
}

func (t *betaJoinTokenBucketTable) delete(key betaJoinKey) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	t.entries[index] = betaJoinTokenBucketEntry{state: graphTokenBucketDeleted}
	t.count--
}

func (t *betaJoinTokenBucketTable) clear(recycle func([]graphTokenRowID)) {
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
		t.entries[index] = betaJoinTokenBucketEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t *betaJoinTokenBucketTable) forEachBucket(fn func(graphTokenRowIDBucket)) {
	if t == nil || fn == nil {
		return
	}
	for i := range t.entries {
		if t.entries[i].state == graphTokenBucketFull {
			fn(t.entries[i].bucket)
		}
	}
}

func (t *betaJoinTokenBucketTable) find(key betaJoinKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashBetaJoinTokenBucketKey(key) & mask)
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

func (t *betaJoinTokenBucketTable) findInsert(key betaJoinKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashBetaJoinTokenBucketKey(key) & mask)
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

func (t *betaJoinTokenBucketTable) rehash(slotCapacity int) {
	slotCapacity = graphTokenBucketPowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]betaJoinTokenBucketEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state == graphTokenBucketFull {
			t.set(old[i].key, old[i].bucket)
		}
	}
}

type tokenIdentityBucketEntry struct {
	hash   uint64
	bucket graphTokenRowIDBucket
	state  uint8
}

type tokenIdentityBucketTable struct {
	entries []tokenIdentityBucketEntry
	touched []int
	count   int
	used    int
}

func (t *tokenIdentityBucketTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	t.rehash(tokenIdentityBucketSlotCapacity(capacity))
}

func (t *tokenIdentityBucketTable) isEmpty() bool {
	return t == nil || t.count == 0
}

func (t *tokenIdentityBucketTable) keyCount() int {
	if t == nil {
		return 0
	}
	return t.count
}

func (t *tokenIdentityBucketTable) get(key tokenIdentityKey) (graphTokenRowIDBucket, bool) {
	return t.getHash(hashTokenIdentityBucketKey(key))
}

func (t *tokenIdentityBucketTable) getHash(hash uint64) (graphTokenRowIDBucket, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return graphTokenRowIDBucket{}, false
	}
	index, ok := t.find(hash)
	if !ok {
		return graphTokenRowIDBucket{}, false
	}
	return t.entries[index].bucket, true
}

func (t *tokenIdentityBucketTable) set(key tokenIdentityKey, bucket graphTokenRowIDBucket) {
	t.setHash(hashTokenIdentityBucketKey(key), bucket)
}

func (t *tokenIdentityBucketTable) setHash(hash uint64, bucket graphTokenRowIDBucket) {
	if t == nil {
		return
	}
	if tokenIdentityBucketNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(max(8, len(t.entries)*2))
	}
	index, ok := t.findInsert(hash)
	if ok {
		t.entries[index].bucket = bucket
		return
	}
	if t.entries[index].state == graphTokenBucketEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = tokenIdentityBucketEntry{hash: hash, bucket: bucket, state: graphTokenBucketFull}
	t.count++
}

func (t *tokenIdentityBucketTable) delete(key tokenIdentityKey) {
	t.deleteHash(hashTokenIdentityBucketKey(key))
}

func (t *tokenIdentityBucketTable) deleteHash(hash uint64) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(hash)
	if !ok {
		return
	}
	t.entries[index] = tokenIdentityBucketEntry{state: graphTokenBucketDeleted}
	t.count--
}

func (t *tokenIdentityBucketTable) clear(recycle func([]graphTokenRowID)) {
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
		t.entries[index] = tokenIdentityBucketEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t *tokenIdentityBucketTable) find(hash uint64) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hash & mask)
	for {
		entry := t.entries[index]
		if entry.state == graphTokenBucketEmpty {
			return 0, false
		}
		if entry.state == graphTokenBucketFull && entry.hash == hash {
			return index, true
		}
		index = (index + 1) & int(mask)
	}
}

func (t *tokenIdentityBucketTable) findInsert(hash uint64) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hash & mask)
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
			if entry.hash == hash {
				return index, true
			}
		}
		index = (index + 1) & int(mask)
	}
}

func (t *tokenIdentityBucketTable) rehash(slotCapacity int) {
	slotCapacity = graphTokenBucketPowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]tokenIdentityBucketEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state == graphTokenBucketFull {
			index, ok := t.findInsert(old[i].hash)
			if ok {
				t.entries[index].bucket = old[i].bucket
				continue
			}
			if t.entries[index].state == graphTokenBucketEmpty {
				t.touched = append(t.touched, index)
				t.used++
			}
			t.entries[index] = old[i]
			t.count++
		}
	}
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

func tokenIdentityBucketNeedsGrow(used, slots int) bool {
	return slots == 0 || used*10 >= slots*9
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
