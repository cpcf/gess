package engine

const (
	backchainDemandSupportTableEmpty uint8 = iota
	backchainDemandSupportTableFull
	backchainDemandSupportTableDeleted
)

type backchainDemandSupportTableEntry struct {
	key    backchainDemandSupportKey
	bucket backchainDemandSupportIDBucket
	state  uint8
}

type backchainDemandSupportTable struct {
	entries []backchainDemandSupportTableEntry
	touched []int
	count   int
	used    int
}

func (t *backchainDemandSupportTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	slotCapacity := backchainDemandSupportTableSlotCapacity(capacity)
	if slotCapacity <= len(t.entries) {
		return
	}
	t.rehash(slotCapacity)
}

func (t *backchainDemandSupportTable) isEmpty() bool {
	return t == nil || t.count == 0
}

func (t *backchainDemandSupportTable) get(key backchainDemandSupportKey) (backchainDemandSupportIDBucket, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return backchainDemandSupportIDBucket{}, false
	}
	index, ok := t.find(key)
	if !ok {
		return backchainDemandSupportIDBucket{}, false
	}
	return t.entries[index].bucket, true
}

func (t *backchainDemandSupportTable) set(key backchainDemandSupportKey, bucket backchainDemandSupportIDBucket) {
	if t == nil {
		return
	}
	if backchainDemandSupportTableNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(max(8, len(t.entries)*2))
	}
	index, ok := t.findInsert(key)
	if ok {
		t.entries[index].bucket = bucket
		return
	}
	if t.entries[index].state == backchainDemandSupportTableEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = backchainDemandSupportTableEntry{key: key, bucket: bucket, state: backchainDemandSupportTableFull}
	t.count++
}

func (t *backchainDemandSupportTable) delete(key backchainDemandSupportKey) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	t.entries[index] = backchainDemandSupportTableEntry{state: backchainDemandSupportTableDeleted}
	t.count--
}

func (t *backchainDemandSupportTable) clear() {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for _, index := range t.touched {
		if index < 0 || index >= len(t.entries) {
			continue
		}
		t.entries[index] = backchainDemandSupportTableEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t *backchainDemandSupportTable) find(key backchainDemandSupportKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashBackchainDemandSupportTableKey(key) & mask)
	for {
		entry := t.entries[index]
		if entry.state == backchainDemandSupportTableEmpty {
			return 0, false
		}
		if entry.state == backchainDemandSupportTableFull && entry.key == key {
			return index, true
		}
		index = (index + 1) & int(mask)
	}
}

func (t *backchainDemandSupportTable) findInsert(key backchainDemandSupportKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashBackchainDemandSupportTableKey(key) & mask)
	firstDeleted := -1
	for {
		entry := t.entries[index]
		switch entry.state {
		case backchainDemandSupportTableEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case backchainDemandSupportTableDeleted:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		case backchainDemandSupportTableFull:
			if entry.key == key {
				return index, true
			}
		}
		index = (index + 1) & int(mask)
	}
}

func (t *backchainDemandSupportTable) rehash(slotCapacity int) {
	slotCapacity = backchainDemandSupportTablePowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]backchainDemandSupportTableEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state == backchainDemandSupportTableFull {
			t.set(old[i].key, old[i].bucket)
		}
	}
}

type backchainDemandFactSupportTableEntry struct {
	key    FactID
	bucket backchainDemandSupportIDBucket
	state  uint8
}

type backchainDemandFactSupportTable struct {
	entries []backchainDemandFactSupportTableEntry
	touched []int
	count   int
	used    int
}

func (t *backchainDemandFactSupportTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	slotCapacity := backchainDemandSupportTableSlotCapacity(capacity)
	if slotCapacity <= len(t.entries) {
		return
	}
	t.rehash(slotCapacity)
}

func (t *backchainDemandFactSupportTable) isEmpty() bool {
	return t == nil || t.count == 0
}

func (t *backchainDemandFactSupportTable) get(key FactID) (backchainDemandSupportIDBucket, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return backchainDemandSupportIDBucket{}, false
	}
	index, ok := t.find(key)
	if !ok {
		return backchainDemandSupportIDBucket{}, false
	}
	return t.entries[index].bucket, true
}

func (t *backchainDemandFactSupportTable) set(key FactID, bucket backchainDemandSupportIDBucket) {
	if t == nil {
		return
	}
	if backchainDemandSupportTableNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(max(8, len(t.entries)*2))
	}
	index, ok := t.findInsert(key)
	if ok {
		t.entries[index].bucket = bucket
		return
	}
	if t.entries[index].state == backchainDemandSupportTableEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = backchainDemandFactSupportTableEntry{key: key, bucket: bucket, state: backchainDemandSupportTableFull}
	t.count++
}

func (t *backchainDemandFactSupportTable) delete(key FactID) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	t.entries[index] = backchainDemandFactSupportTableEntry{state: backchainDemandSupportTableDeleted}
	t.count--
}

func (t *backchainDemandFactSupportTable) clear() {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for _, index := range t.touched {
		if index < 0 || index >= len(t.entries) {
			continue
		}
		t.entries[index] = backchainDemandFactSupportTableEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t *backchainDemandFactSupportTable) find(key FactID) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashBackchainDemandFactSupportTableKey(key) & mask)
	for {
		entry := t.entries[index]
		if entry.state == backchainDemandSupportTableEmpty {
			return 0, false
		}
		if entry.state == backchainDemandSupportTableFull && entry.key == key {
			return index, true
		}
		index = (index + 1) & int(mask)
	}
}

func (t *backchainDemandFactSupportTable) findInsert(key FactID) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashBackchainDemandFactSupportTableKey(key) & mask)
	firstDeleted := -1
	for {
		entry := t.entries[index]
		switch entry.state {
		case backchainDemandSupportTableEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case backchainDemandSupportTableDeleted:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		case backchainDemandSupportTableFull:
			if entry.key == key {
				return index, true
			}
		}
		index = (index + 1) & int(mask)
	}
}

func (t *backchainDemandFactSupportTable) rehash(slotCapacity int) {
	slotCapacity = backchainDemandSupportTablePowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]backchainDemandFactSupportTableEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state == backchainDemandSupportTableFull {
			t.set(old[i].key, old[i].bucket)
		}
	}
}

func backchainDemandSupportTableSlotCapacity(capacity int) int {
	if capacity <= 0 {
		return 0
	}
	return backchainDemandSupportTablePowerOfTwo(max(8, capacity*2))
}

func backchainDemandSupportTableNeedsGrow(used, slots int) bool {
	return slots == 0 || used*4 >= slots*3
}

func backchainDemandSupportTablePowerOfTwo(value int) int {
	if value <= 8 {
		return 8
	}
	out := 1
	for out < value {
		out <<= 1
	}
	return out
}

func hashBackchainDemandSupportTableKey(key backchainDemandSupportKey) uint64 {
	hash := backchainDemandHashStart()
	hash = backchainDemandHashAddString(hash, string(key.templateKey))
	hash = backchainDemandHashAddUint64(hash, key.supportHash)
	hash = backchainDemandHashAddUint64(hash, key.slotHash)
	hash = backchainDemandHashAddUint64(hash, uint64(key.supportCount))
	hash = backchainDemandHashAddUint64(hash, uint64(key.slotCount))
	return hash
}

func hashBackchainDemandFactSupportTableKey(key FactID) uint64 {
	hash := backchainDemandHashStart()
	hash = backchainDemandHashAddUint64(hash, uint64(key.Generation()))
	hash = backchainDemandHashAddUint64(hash, key.Sequence())
	return hash
}
