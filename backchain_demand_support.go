package gess

import (
	"context"
	"slices"
	"sort"
)

const backchainDemandSupportInlineLimit = 4

type backchainDemandSupportID uint64

type backchainDemandSupportRecord struct {
	id           backchainDemandSupportID
	key          backchainDemandSupportKey
	inlineKey    backchainDemandInlineSupportKey
	inline       bool
	demandFactID FactID
	supportCount int
	supportFacts [backchainDemandSupportInlineLimit]backchainDemandSupportFact
	supportExtra []backchainDemandSupportFact
	slotCount    int
	slots        [backchainDemandSupportInlineLimit]backchainDemandSlotKey
	slotExtra    []backchainDemandSlotKey
}

type backchainDemandSupportKey struct {
	templateKey  TemplateKey
	supportHash  uint64
	slotHash     uint64
	supportCount uint32
	slotCount    uint32
}

type backchainDemandSupportRequestKey struct {
	key       backchainDemandSupportKey
	slotKeys  [backchainDemandSupportInlineLimit]backchainDemandSlotKey
	slotExtra []backchainDemandSlotKey
}

type backchainDemandInlineSupportKey struct {
	templateKey TemplateKey
	support     backchainDemandSupportFact
	slotCount   uint8
	slots       [backchainDemandSupportInlineLimit]backchainDemandSlotKey
}

type backchainDemandInlineSupportEntry struct {
	id           backchainDemandSupportID
	demandFactID FactID
	support      backchainDemandSupportFact
}

type backchainDemandInlineSupportIndex struct {
	firstKey    backchainDemandInlineSupportKey
	firstEntry  backchainDemandInlineSupportEntry
	secondKey   backchainDemandInlineSupportKey
	secondEntry backchainDemandInlineSupportEntry
	overflow    map[backchainDemandInlineSupportKey]backchainDemandInlineSupportEntry
}

type backchainDemandSlotKey struct {
	ok          bool
	scalar      bool
	scalarKind  duplicateScalarKind
	bits        uint64
	stringValue string
	signature   string
}

type backchainDemandSupportIDBucket struct {
	first    backchainDemandSupportID
	second   backchainDemandSupportID
	overflow []backchainDemandSupportID
}

func (s *Session) ensureBackchainDemandSupportTables() {
	if s == nil {
		return
	}
	s.backchainDemandSupports.reserve(4)
	s.ensureBackchainDemandReverseSupportTables()
}

func (s *Session) ensureBackchainDemandReverseSupportTables() {
	if s == nil {
		return
	}
	s.backchainDemandByFact.reserve(4)
	s.backchainDemandByDemand.reserve(4)
}

func (s *Session) addBackchainDemandSupport(demandFact *workingFact, request backchainDemandRequest) {
	if s == nil || demandFact == nil || demandFact.id.IsZero() || len(request.supportFacts) == 0 {
		return
	}
	if inlineKey, ok := backchainDemandInlineSupportKeyForRequest(request); ok {
		s.addBackchainDemandInlineSupport(demandFact, request, inlineKey)
		return
	}
	requestKey, ok := backchainDemandSupportKeyForRequest(request)
	if !ok {
		return
	}
	s.ensureBackchainDemandSupportTables()
	bucket, _ := s.backchainDemandSupports.get(requestKey.key)
	if _, exists := s.findBackchainDemandSupportID(bucket, requestKey, request); exists {
		return
	}
	id := s.nextBackchainDemandSupportIDValue()
	record := newBackchainDemandSupportRecord(id, demandFact.id, requestKey, request)
	s.storeBackchainDemandSupportRecord(record)
	bucket.add(id)
	s.backchainDemandSupports.set(requestKey.key, bucket)
	for i := 0; i < record.supportCount; i++ {
		support := backchainDemandSupportRecordFact(record, i)
		factBucket, _ := s.backchainDemandByFact.get(support.id)
		factBucket.add(id)
		s.backchainDemandByFact.set(support.id, factBucket)
	}
	demandBucket, _ := s.backchainDemandByDemand.get(demandFact.id)
	demandBucket.add(id)
	s.backchainDemandByDemand.set(demandFact.id, demandBucket)
}

func (s *Session) addBackchainDemandInlineSupport(demandFact *workingFact, request backchainDemandRequest, inlineKey backchainDemandInlineSupportKey) {
	if s == nil || demandFact == nil || demandFact.id.IsZero() {
		return
	}
	if _, exists := s.backchainDemandInlineSupports.get(inlineKey); exists {
		return
	}
	s.ensureBackchainDemandReverseSupportTables()
	id := s.nextBackchainDemandSupportIDValue()
	record := newBackchainDemandSupportRecord(id, demandFact.id, backchainDemandSupportRequestKey{}, request)
	record.inline = true
	record.inlineKey = inlineKey
	s.storeBackchainDemandSupportRecord(record)
	s.backchainDemandInlineSupports.set(inlineKey, backchainDemandInlineSupportEntry{
		id:           id,
		demandFactID: demandFact.id,
		support:      inlineKey.support,
	})
	for i := 0; i < record.supportCount; i++ {
		support := backchainDemandSupportRecordFact(record, i)
		factBucket, _ := s.backchainDemandByFact.get(support.id)
		factBucket.add(id)
		s.backchainDemandByFact.set(support.id, factBucket)
	}
	demandBucket, _ := s.backchainDemandByDemand.get(demandFact.id)
	demandBucket.add(id)
	s.backchainDemandByDemand.set(demandFact.id, demandBucket)
}

func (s *Session) removeBackchainDemandSupportForRequest(ctx context.Context, request backchainDemandRequest, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || len(request.supportFacts) == 0 {
		return reteAgendaDelta{supported: true}, nil
	}
	if id, ok := s.singleBackchainDemandSupportIDForRequest(request); ok {
		return s.removeBackchainDemandSupportID(ctx, id, origin)
	}
	if inlineKey, keyOK := backchainDemandInlineSupportKeyForRequest(request); keyOK {
		if entry, ok := s.backchainDemandInlineSupports.get(inlineKey); ok {
			return s.removeBackchainDemandInlineSupportEntry(ctx, inlineKey, entry, origin)
		}
	}
	id, ok := s.findBackchainDemandSupportIDByRequest(request)
	if !ok {
		requestKey, keyOK := backchainDemandSupportKeyForRequest(request)
		if !keyOK || s.backchainDemandSupports.isEmpty() {
			return reteAgendaDelta{supported: true}, nil
		}
		bucket, _ := s.backchainDemandSupports.get(requestKey.key)
		id, ok = s.findBackchainDemandSupportID(bucket, requestKey, request)
		if !ok {
			return reteAgendaDelta{supported: true}, nil
		}
	}
	return s.removeBackchainDemandSupportID(ctx, id, origin)
}

func (s *Session) singleBackchainDemandSupportIDForRequest(request backchainDemandRequest) (backchainDemandSupportID, bool) {
	if s == nil || len(request.supportFacts) != 1 || s.backchainDemandByFact.isEmpty() {
		return 0, false
	}
	support := request.supportFacts[0]
	bucket, _ := s.backchainDemandByFact.get(support.id)
	id, ok := bucket.single()
	if !ok {
		return 0, false
	}
	record, ok := s.backchainDemandSupportRecordByID(id)
	if !ok || record.supportCount != 1 || record.demandFactID.IsZero() {
		return 0, false
	}
	if backchainDemandSupportRecordFact(record, 0) != support {
		return 0, false
	}
	if record.inline {
		if record.inlineKey.templateKey != request.templateKey || int(record.inlineKey.slotCount) != len(request.slots) {
			return 0, false
		}
		return id, true
	}
	if record.key.templateKey != request.templateKey || int(record.key.slotCount) != len(request.slots) {
		return 0, false
	}
	return id, true
}

func (s *Session) removeBackchainDemandInlineSupportEntry(ctx context.Context, key backchainDemandInlineSupportKey, entry backchainDemandInlineSupportEntry, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || entry.id == 0 {
		return combined, nil
	}
	s.clearBackchainDemandSupportRecord(entry.id)
	s.backchainDemandInlineSupports.delete(key)
	s.removeBackchainDemandSupportIDFromFactBucket(entry.support.id, entry.id)
	return s.removeBackchainDemandSupportDemandBucket(ctx, entry.demandFactID, entry.id, origin)
}

func (s *Session) removeBackchainDemandSupportsForFact(ctx context.Context, id FactID, origin mutationOrigin) (reteAgendaDelta, error) {
	return s.removeBackchainDemandSupportsForFactVersionMatch(ctx, id, 0, false, origin)
}

func (s *Session) removeBackchainDemandSupportsForFactVersion(ctx context.Context, id FactID, version FactVersion, origin mutationOrigin) (reteAgendaDelta, error) {
	return s.removeBackchainDemandSupportsForFactVersionMatch(ctx, id, version, true, origin)
}

func (s *Session) removeBackchainDemandSupportsForFactVersionMatch(ctx context.Context, id FactID, version FactVersion, matchVersion bool, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || id.IsZero() || s.backchainDemandByFact.isEmpty() {
		return combined, nil
	}
	bucket, _ := s.backchainDemandByFact.get(id)
	if bucket.empty() {
		return combined, nil
	}
	var inline [backchainDemandSupportInlineLimit]backchainDemandSupportID
	supportIDs := inline[:0]
	bucket.forEach(func(supportID backchainDemandSupportID) {
		record, ok := s.backchainDemandSupportRecordByID(supportID)
		if !ok {
			return
		}
		if matchVersion && !backchainDemandSupportRecordContainsFactVersion(record, id, version) {
			return
		}
		supportIDs = append(supportIDs, supportID)
	})
	sort.Slice(supportIDs, func(i, j int) bool {
		left, leftOK := s.backchainDemandSupportRecordByID(supportIDs[i])
		right, rightOK := s.backchainDemandSupportRecordByID(supportIDs[j])
		if !leftOK || !rightOK {
			return supportIDs[i] < supportIDs[j]
		}
		if cmp := compareBackchainDemandSupportRecords(left, right); cmp != 0 {
			return cmp < 0
		}
		return supportIDs[i] < supportIDs[j]
	})
	for _, supportID := range supportIDs {
		delta, err := s.removeBackchainDemandSupportID(ctx, supportID, origin)
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, delta)
	}
	return combined, nil
}

func (s *Session) removeBackchainDemandSupportID(ctx context.Context, id backchainDemandSupportID, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || id == 0 {
		return combined, nil
	}
	record, ok := s.backchainDemandSupportRecordByID(id)
	if !ok {
		return combined, nil
	}
	s.clearBackchainDemandSupportRecord(id)
	if record.inline {
		s.backchainDemandInlineSupports.delete(record.inlineKey)
	} else {
		s.removeBackchainDemandSupportIDFromSupportBucket(record.key, id)
	}
	for i := 0; i < record.supportCount; i++ {
		support := backchainDemandSupportRecordFact(record, i)
		s.removeBackchainDemandSupportIDFromFactBucket(support.id, id)
	}
	return s.removeBackchainDemandSupportDemandBucket(ctx, record.demandFactID, id, origin)
}

func (s *Session) removeBackchainDemandSupportDemandBucket(ctx context.Context, demandFactID FactID, id backchainDemandSupportID, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || demandFactID.IsZero() || id == 0 {
		return combined, nil
	}
	demandBucket, _ := s.backchainDemandByDemand.get(demandFactID)
	demandBucket.remove(id)
	if !demandBucket.empty() {
		s.backchainDemandByDemand.set(demandFactID, demandBucket)
		return combined, nil
	}
	s.backchainDemandByDemand.delete(demandFactID)
	if _, ok := s.workingFactByID(demandFactID); !ok {
		return combined, nil
	}
	delta, err := s.removeBackchainDemandFactImmediate(ctx, demandFactID, origin)
	if err != nil {
		return combined, err
	}
	delta = normalizeBackchainDemandNoopDelta(delta)
	return mergeReteAgendaDelta(combined, delta), nil
}

func (s *Session) removeBackchainDemandSupportIDFromSupportBucket(key backchainDemandSupportKey, id backchainDemandSupportID) {
	bucket, _ := s.backchainDemandSupports.get(key)
	bucket.remove(id)
	if bucket.empty() {
		s.backchainDemandSupports.delete(key)
		return
	}
	s.backchainDemandSupports.set(key, bucket)
}

func (s *Session) removeBackchainDemandSupportIDFromFactBucket(factID FactID, id backchainDemandSupportID) {
	bucket, _ := s.backchainDemandByFact.get(factID)
	bucket.remove(id)
	if bucket.empty() {
		s.backchainDemandByFact.delete(factID)
		return
	}
	s.backchainDemandByFact.set(factID, bucket)
}

func normalizeBackchainDemandNoopDelta(delta reteAgendaDelta) reteAgendaDelta {
	if delta.supported {
		return delta
	}
	if len(delta.added) != 0 || len(delta.removed) != 0 || len(delta.updated) != 0 || len(delta.demands) != 0 || len(delta.resolvedDemands) != 0 {
		return delta
	}
	delta.supported = true
	return delta
}

func (s *Session) clearBackchainDemandSupports() {
	if s == nil {
		return
	}
	s.backchainDemandSupports.clear()
	s.backchainDemandInlineSupports.clear()
	for i := range s.backchainDemandSupportRecords {
		s.backchainDemandSupportRecords[i] = backchainDemandSupportRecord{}
	}
	s.backchainDemandSupportRecords = s.backchainDemandSupportRecords[:0]
	s.backchainDemandByFact.clear()
	s.backchainDemandByDemand.clear()
	s.nextBackchainDemandSupportID = 0
}

func (s *Session) nextBackchainDemandSupportIDValue() backchainDemandSupportID {
	s.nextBackchainDemandSupportID++
	if s.nextBackchainDemandSupportID == 0 {
		s.nextBackchainDemandSupportID++
	}
	return s.nextBackchainDemandSupportID
}

func (s *Session) findBackchainDemandSupportID(bucket backchainDemandSupportIDBucket, requestKey backchainDemandSupportRequestKey, request backchainDemandRequest) (backchainDemandSupportID, bool) {
	var found backchainDemandSupportID
	bucket.forEach(func(id backchainDemandSupportID) {
		if found != 0 {
			return
		}
		record, ok := s.backchainDemandSupportRecordPtrByID(id)
		if !ok || !backchainDemandSupportRecordMatchesRequest(record, requestKey, request) {
			return
		}
		found = id
	})
	return found, found != 0
}

func (s *Session) findBackchainDemandSupportIDByRequest(request backchainDemandRequest) (backchainDemandSupportID, bool) {
	if s == nil || s.backchainDemandByFact.isEmpty() || len(request.supportFacts) == 0 {
		return 0, false
	}
	bucket, _ := s.backchainDemandByFact.get(request.supportFacts[0].id)
	var found backchainDemandSupportID
	bucket.forEach(func(id backchainDemandSupportID) {
		if found != 0 {
			return
		}
		record, ok := s.backchainDemandSupportRecordPtrByID(id)
		if !ok || !backchainDemandSupportRecordMatchesRawRequest(record, request) {
			return
		}
		found = id
	})
	return found, found != 0
}

func (s *Session) storeBackchainDemandSupportRecord(record backchainDemandSupportRecord) {
	if s == nil || record.id == 0 {
		return
	}
	index, ok := backchainDemandSupportRecordIndex(record.id)
	if !ok {
		return
	}
	for len(s.backchainDemandSupportRecords) <= index {
		s.backchainDemandSupportRecords = append(s.backchainDemandSupportRecords, backchainDemandSupportRecord{})
	}
	s.backchainDemandSupportRecords[index] = record
}

func (s *Session) backchainDemandSupportRecordByID(id backchainDemandSupportID) (backchainDemandSupportRecord, bool) {
	record, ok := s.backchainDemandSupportRecordPtrByID(id)
	if !ok {
		return backchainDemandSupportRecord{}, false
	}
	return *record, true
}

func (s *Session) backchainDemandSupportRecordPtrByID(id backchainDemandSupportID) (*backchainDemandSupportRecord, bool) {
	if s == nil || id == 0 {
		return nil, false
	}
	index, ok := backchainDemandSupportRecordIndex(id)
	if !ok || index >= len(s.backchainDemandSupportRecords) {
		return nil, false
	}
	record := &s.backchainDemandSupportRecords[index]
	if record.id != id {
		return nil, false
	}
	return record, true
}

func (s *Session) clearBackchainDemandSupportRecord(id backchainDemandSupportID) {
	if s == nil || id == 0 {
		return
	}
	index, ok := backchainDemandSupportRecordIndex(id)
	if !ok || index >= len(s.backchainDemandSupportRecords) || s.backchainDemandSupportRecords[index].id != id {
		return
	}
	s.backchainDemandSupportRecords[index] = backchainDemandSupportRecord{}
}

func backchainDemandSupportRecordIndex(id backchainDemandSupportID) (int, bool) {
	if id == 0 || uint64(id-1) > uint64(int(^uint(0)>>1)) {
		return 0, false
	}
	return int(id - 1), true
}

func newBackchainDemandSupportRecord(id backchainDemandSupportID, demandFactID FactID, requestKey backchainDemandSupportRequestKey, request backchainDemandRequest) backchainDemandSupportRecord {
	record := backchainDemandSupportRecord{
		id:           id,
		key:          requestKey.key,
		demandFactID: demandFactID,
		supportCount: len(request.supportFacts),
		slotCount:    len(request.slots),
	}
	for i := 0; i < min(record.supportCount, backchainDemandSupportInlineLimit); i++ {
		record.supportFacts[i] = request.supportFacts[i]
	}
	if record.supportCount > backchainDemandSupportInlineLimit {
		record.supportExtra = make([]backchainDemandSupportFact, record.supportCount-backchainDemandSupportInlineLimit)
		copy(record.supportExtra, request.supportFacts[backchainDemandSupportInlineLimit:])
	}
	for i := 0; i < min(record.slotCount, backchainDemandSupportInlineLimit); i++ {
		record.slots[i] = requestKey.slotKeys[i]
	}
	if record.slotCount > backchainDemandSupportInlineLimit {
		record.slotExtra = make([]backchainDemandSlotKey, record.slotCount-backchainDemandSupportInlineLimit)
		copy(record.slotExtra, requestKey.slotExtra)
	}
	return record
}

func backchainDemandSupportRecordMatchesRequest(record *backchainDemandSupportRecord, requestKey backchainDemandSupportRequestKey, request backchainDemandRequest) bool {
	if record == nil || record.key != requestKey.key || record.supportCount != len(request.supportFacts) || record.slotCount != len(request.slots) {
		return false
	}
	for i := 0; i < record.supportCount; i++ {
		if backchainDemandSupportRecordFact(*record, i) != request.supportFacts[i] {
			return false
		}
	}
	for i := 0; i < record.slotCount; i++ {
		if backchainDemandSupportRecordSlot(*record, i) != backchainDemandSupportRequestSlot(requestKey, i) {
			return false
		}
	}
	return true
}

func backchainDemandSupportRecordMatchesRawRequest(record *backchainDemandSupportRecord, request backchainDemandRequest) bool {
	if record == nil || record.key.templateKey != request.templateKey || record.supportCount != len(request.supportFacts) || record.slotCount != len(request.slots) {
		return false
	}
	for i := 0; i < record.supportCount; i++ {
		if backchainDemandSupportRecordFact(*record, i) != request.supportFacts[i] {
			return false
		}
	}
	for i := 0; i < record.slotCount; i++ {
		if backchainDemandSupportRecordSlot(*record, i) != backchainDemandSlotKeyForFactSlot(request.slots[i]) {
			return false
		}
	}
	return true
}

func backchainDemandSupportRecordContainsFactVersion(record backchainDemandSupportRecord, id FactID, version FactVersion) bool {
	for i := 0; i < record.supportCount; i++ {
		support := backchainDemandSupportRecordFact(record, i)
		if support.id == id && support.version == version {
			return true
		}
	}
	return false
}

func backchainDemandSupportRecordFact(record backchainDemandSupportRecord, index int) backchainDemandSupportFact {
	if index < backchainDemandSupportInlineLimit {
		return record.supportFacts[index]
	}
	return record.supportExtra[index-backchainDemandSupportInlineLimit]
}

func backchainDemandSupportRecordSlot(record backchainDemandSupportRecord, index int) backchainDemandSlotKey {
	if index < backchainDemandSupportInlineLimit {
		return record.slots[index]
	}
	return record.slotExtra[index-backchainDemandSupportInlineLimit]
}

func backchainDemandSupportRequestSlot(requestKey backchainDemandSupportRequestKey, index int) backchainDemandSlotKey {
	if index < backchainDemandSupportInlineLimit {
		return requestKey.slotKeys[index]
	}
	return requestKey.slotExtra[index-backchainDemandSupportInlineLimit]
}

func backchainDemandInlineSupportKeyForRequest(request backchainDemandRequest) (backchainDemandInlineSupportKey, bool) {
	if request.templateKey == "" || len(request.supportFacts) != 1 || len(request.slots) > backchainDemandSupportInlineLimit {
		return backchainDemandInlineSupportKey{}, false
	}
	out := backchainDemandInlineSupportKey{
		templateKey: request.templateKey,
		support:     request.supportFacts[0],
		slotCount:   uint8(len(request.slots)),
	}
	for i, slot := range request.slots {
		out.slots[i] = backchainDemandSlotKeyForFactSlot(slot)
	}
	return out, true
}

func (i *backchainDemandInlineSupportIndex) get(key backchainDemandInlineSupportKey) (backchainDemandInlineSupportEntry, bool) {
	if i == nil {
		return backchainDemandInlineSupportEntry{}, false
	}
	if i.firstEntry.id != 0 && i.firstKey == key {
		return i.firstEntry, true
	}
	if i.secondEntry.id != 0 && i.secondKey == key {
		return i.secondEntry, true
	}
	if i.overflow == nil {
		return backchainDemandInlineSupportEntry{}, false
	}
	entry, ok := i.overflow[key]
	return entry, ok
}

func (i *backchainDemandInlineSupportIndex) set(key backchainDemandInlineSupportKey, entry backchainDemandInlineSupportEntry) {
	if i == nil || entry.id == 0 {
		return
	}
	if i.firstEntry.id == 0 {
		i.firstKey = key
		i.firstEntry = entry
		return
	}
	if i.firstKey == key {
		i.firstEntry = entry
		return
	}
	if i.secondEntry.id == 0 {
		i.secondKey = key
		i.secondEntry = entry
		return
	}
	if i.secondKey == key {
		i.secondEntry = entry
		return
	}
	if i.overflow == nil {
		i.overflow = make(map[backchainDemandInlineSupportKey]backchainDemandInlineSupportEntry, 4)
	}
	i.overflow[key] = entry
}

func (i *backchainDemandInlineSupportIndex) delete(key backchainDemandInlineSupportKey) {
	if i == nil {
		return
	}
	if i.firstEntry.id != 0 && i.firstKey == key {
		i.firstKey = i.secondKey
		i.firstEntry = i.secondEntry
		i.secondKey = backchainDemandInlineSupportKey{}
		i.secondEntry = backchainDemandInlineSupportEntry{}
		i.promoteOverflow()
		return
	}
	if i.secondEntry.id != 0 && i.secondKey == key {
		i.secondKey = backchainDemandInlineSupportKey{}
		i.secondEntry = backchainDemandInlineSupportEntry{}
		i.promoteOverflow()
		return
	}
	if i.overflow != nil {
		delete(i.overflow, key)
	}
}

func (i *backchainDemandInlineSupportIndex) promoteOverflow() {
	if i == nil || i.secondEntry.id != 0 || len(i.overflow) == 0 {
		return
	}
	for key, entry := range i.overflow {
		i.secondKey = key
		i.secondEntry = entry
		delete(i.overflow, key)
		return
	}
}

func (i *backchainDemandInlineSupportIndex) clear() {
	if i == nil {
		return
	}
	i.firstKey = backchainDemandInlineSupportKey{}
	i.firstEntry = backchainDemandInlineSupportEntry{}
	i.secondKey = backchainDemandInlineSupportKey{}
	i.secondEntry = backchainDemandInlineSupportEntry{}
	clear(i.overflow)
}

func backchainDemandSupportKeyForRequest(request backchainDemandRequest) (backchainDemandSupportRequestKey, bool) {
	if request.templateKey == "" || len(request.supportFacts) == 0 {
		return backchainDemandSupportRequestKey{}, false
	}
	out := backchainDemandSupportRequestKey{
		key: backchainDemandSupportKey{
			templateKey:  request.templateKey,
			supportHash:  hashBackchainDemandSupportFacts(request.supportFacts),
			supportCount: uint32(len(request.supportFacts)),
			slotCount:    uint32(len(request.slots)),
		},
	}
	slotHash := backchainDemandHashStart()
	slotHash = backchainDemandHashAddUint64(slotHash, uint64(len(request.slots)))
	for i, slot := range request.slots {
		slotKey := backchainDemandSlotKeyForFactSlot(slot)
		if i < backchainDemandSupportInlineLimit {
			out.slotKeys[i] = slotKey
		} else {
			out.slotExtra = append(out.slotExtra, slotKey)
		}
		slotHash = hashBackchainDemandSlotKey(slotHash, slotKey)
	}
	out.key.slotHash = slotHash
	return out, true
}

func backchainDemandSlotKeyForFactSlot(slot factSlot) backchainDemandSlotKey {
	out := backchainDemandSlotKey{ok: slot.ok}
	if !slot.ok {
		return out
	}
	if scalar, ok := duplicateScalarKeyFromValue(slot.value); ok {
		out.scalar = true
		out.scalarKind = scalar.kind
		out.bits = scalar.bits
		out.stringValue = scalar.stringValue
		return out
	}
	out.signature = slot.value.canonicalKey()
	return out
}

func compareBackchainDemandSupportRecords(left, right backchainDemandSupportRecord) int {
	if left.key.templateKey != right.key.templateKey {
		if left.key.templateKey < right.key.templateKey {
			return -1
		}
		return 1
	}
	if left.supportCount != right.supportCount {
		return cmpUint64(uint64(left.supportCount), uint64(right.supportCount))
	}
	for i := 0; i < left.supportCount; i++ {
		if cmp := compareBackchainDemandSupportFacts(backchainDemandSupportRecordFact(left, i), backchainDemandSupportRecordFact(right, i)); cmp != 0 {
			return cmp
		}
	}
	if left.slotCount != right.slotCount {
		return cmpUint64(uint64(left.slotCount), uint64(right.slotCount))
	}
	for i := 0; i < left.slotCount; i++ {
		if cmp := compareBackchainDemandSlotKey(backchainDemandSupportRecordSlot(left, i), backchainDemandSupportRecordSlot(right, i)); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func compareBackchainDemandSlotKey(left, right backchainDemandSlotKey) int {
	if left.ok != right.ok {
		if !left.ok {
			return -1
		}
		return 1
	}
	if left.scalar != right.scalar {
		if !left.scalar {
			return -1
		}
		return 1
	}
	if left.scalarKind != right.scalarKind {
		return cmpUint64(uint64(left.scalarKind), uint64(right.scalarKind))
	}
	if left.bits != right.bits {
		return cmpUint64(left.bits, right.bits)
	}
	if left.stringValue != right.stringValue {
		if left.stringValue < right.stringValue {
			return -1
		}
		return 1
	}
	if left.signature != right.signature {
		if left.signature < right.signature {
			return -1
		}
		return 1
	}
	return 0
}

func (bucket backchainDemandSupportIDBucket) empty() bool {
	return bucket.first == 0 && bucket.second == 0 && len(bucket.overflow) == 0
}

func (bucket backchainDemandSupportIDBucket) contains(id backchainDemandSupportID) bool {
	if id == 0 {
		return false
	}
	if bucket.first == id || bucket.second == id {
		return true
	}
	return slices.Contains(bucket.overflow, id)
}

func (bucket backchainDemandSupportIDBucket) single() (backchainDemandSupportID, bool) {
	if bucket.first == 0 || bucket.second != 0 || len(bucket.overflow) != 0 {
		return 0, false
	}
	return bucket.first, true
}

func (bucket *backchainDemandSupportIDBucket) add(id backchainDemandSupportID) bool {
	if id == 0 || bucket.contains(id) {
		return false
	}
	switch {
	case bucket.first == 0:
		bucket.first = id
	case bucket.second == 0:
		bucket.second = id
	default:
		bucket.overflow = append(bucket.overflow, id)
	}
	return true
}

func (bucket *backchainDemandSupportIDBucket) remove(id backchainDemandSupportID) bool {
	switch id {
	case 0:
		return false
	case bucket.first:
		bucket.first = bucket.second
		bucket.second = 0
		bucket.promoteOverflow()
		return true
	case bucket.second:
		bucket.second = 0
		bucket.promoteOverflow()
		return true
	}
	for i, existing := range bucket.overflow {
		if existing != id {
			continue
		}
		copy(bucket.overflow[i:], bucket.overflow[i+1:])
		bucket.overflow[len(bucket.overflow)-1] = 0
		bucket.overflow = bucket.overflow[:len(bucket.overflow)-1]
		return true
	}
	return false
}

func (bucket *backchainDemandSupportIDBucket) promoteOverflow() {
	if bucket.second != 0 || len(bucket.overflow) == 0 {
		return
	}
	bucket.second = bucket.overflow[0]
	copy(bucket.overflow, bucket.overflow[1:])
	bucket.overflow[len(bucket.overflow)-1] = 0
	bucket.overflow = bucket.overflow[:len(bucket.overflow)-1]
}

func (bucket backchainDemandSupportIDBucket) forEach(fn func(backchainDemandSupportID)) {
	if bucket.first != 0 {
		fn(bucket.first)
	}
	if bucket.second != 0 {
		fn(bucket.second)
	}
	for _, id := range bucket.overflow {
		if id != 0 {
			fn(id)
		}
	}
}

func hashBackchainDemandSupportFacts(supportFacts []backchainDemandSupportFact) uint64 {
	hash := backchainDemandHashStart()
	hash = backchainDemandHashAddUint64(hash, uint64(len(supportFacts)))
	for _, support := range supportFacts {
		hash = backchainDemandHashAddUint64(hash, uint64(support.id.generation))
		hash = backchainDemandHashAddUint64(hash, support.id.sequence)
		hash = backchainDemandHashAddUint64(hash, uint64(support.version))
	}
	return hash
}

func hashBackchainDemandSlotKey(hash uint64, slot backchainDemandSlotKey) uint64 {
	if slot.ok {
		hash = backchainDemandHashAddUint64(hash, 1)
	} else {
		hash = backchainDemandHashAddUint64(hash, 0)
	}
	if slot.scalar {
		hash = backchainDemandHashAddUint64(hash, 1)
	} else {
		hash = backchainDemandHashAddUint64(hash, 0)
	}
	hash = backchainDemandHashAddUint64(hash, uint64(slot.scalarKind))
	hash = backchainDemandHashAddUint64(hash, slot.bits)
	hash = backchainDemandHashAddString(hash, slot.stringValue)
	hash = backchainDemandHashAddString(hash, slot.signature)
	return hash
}

func backchainDemandHashStart() uint64 {
	return 1469598103934665603
}

func backchainDemandHashAddUint64(hash uint64, value uint64) uint64 {
	return backchainDemandHashAvalanche(hash ^ backchainDemandHashAvalanche(value+0x9e3779b97f4a7c15))
}

func backchainDemandHashAvalanche(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}

func backchainDemandHashAddString(hash uint64, value string) uint64 {
	const prime uint64 = 1099511628211
	hash = backchainDemandHashAddUint64(hash, uint64(len(value)))
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= prime
	}
	return hash
}
