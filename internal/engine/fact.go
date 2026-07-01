package engine

import (
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type FactVersion uint32

type Recency uint32

// Generation is the working-memory reset epoch. Fact IDs include a generation
// component so IDs from before Reset cannot address post-reset facts.
type Generation uint64

type FactSnapshot struct {
	id            FactID
	name          string
	templateKey   TemplateKey
	version       FactVersion
	recency       Recency
	generation    Generation
	fields        Fields
	fieldSlots    []factSlot
	fieldSpecs    []FieldSpec
	fieldPresence map[string]FieldPresence
	support       FactSupportProvenance
}

type conditionFactRef struct {
	id                FactID
	name              string
	templateKey       TemplateKey
	version           FactVersion
	recency           Recency
	generation        Generation
	fields            Fields
	fieldSlots        []factSlot
	compactFieldSlots []compactFactSlot
	fieldSpecs        []FieldSpec
}

func (f FactSnapshot) ID() FactID {
	return f.id
}

func (f FactSnapshot) Name() string {
	return f.name
}

func (f FactSnapshot) TemplateKey() TemplateKey {
	return f.templateKey
}

func (f FactSnapshot) Version() FactVersion {
	return f.version
}

func (f FactSnapshot) Recency() Recency {
	return f.recency
}

func (f FactSnapshot) Generation() Generation {
	return f.generation
}

// Fields returns a defensive copy of the fact fields.
func (f FactSnapshot) Fields() Fields {
	if f.fields != nil {
		return cloneFields(f.fields)
	}
	return materializeFieldsFromSlots(f.fieldSlots, f.fieldSpecs)
}

// Field returns a defensive copy of one fact field.
func (f FactSnapshot) Field(name string) (Value, bool) {
	value, ok := f.fieldValue(name)
	if !ok {
		return Value{}, false
	}
	return cloneValue(value), true
}

func (f FactSnapshot) Path(path PathSpec) (Value, bool, error) {
	access, _, err := compilePathAccess(path, nil)
	if err != nil {
		return Value{}, false, err
	}
	value, ok := access.valueFromSnapshot(f)
	if !ok {
		return Value{}, false, nil
	}
	return cloneValue(value), true, nil
}

func (f FactSnapshot) compiledFieldValue(field string, slot int) (Value, bool) {
	if slot >= 0 && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}

	return f.fieldValue(field)
}

func (f *workingFact) compiledFieldValue(field string, slot int, compactSlotStore *factCompactSlotStore) (Value, bool) {
	if f == nil {
		return Value{}, false
	}
	fieldSlots := f.fieldSlotSlice()
	if slot >= 0 && slot < len(fieldSlots) {
		resolved := fieldSlots[slot]
		return resolved.value, resolved.ok
	}
	compactSlots := f.compactFieldSlots(compactSlotStore)
	if slot >= 0 && slot < len(compactSlots) {
		return compactSlots[slot].value()
	}
	return f.fieldValue(field, compactSlotStore)
}

func (f FactSnapshot) Support() FactSupportProvenance {
	return f.support
}

func newConditionFactRefFromSnapshot(snapshot FactSnapshot) conditionFactRef {
	return conditionFactRef{
		id:          snapshot.id,
		name:        snapshot.name,
		templateKey: snapshot.templateKey,
		version:     snapshot.version,
		recency:     snapshot.recency,
		generation:  snapshot.generation,
		fields:      snapshot.fields,
		fieldSlots:  snapshot.fieldSlots,
		fieldSpecs:  snapshot.fieldSpecs,
	}
}

func newConditionFactRefFromWorkingFact(fact *workingFact, compactSlotStore *factCompactSlotStore) conditionFactRef {
	return newConditionFactRefFromWorkingFactForTarget(fact, conditionTarget{}, compactSlotStore)
}

func newConditionFactRefFromWorkingFactForTarget(fact *workingFact, target conditionTarget, compactSlotStore *factCompactSlotStore) conditionFactRef {
	if fact == nil {
		return conditionFactRef{}
	}
	return conditionFactRef{
		id:                fact.id,
		name:              fact.storedName(),
		templateKey:       fact.templateKeyForTarget(target),
		version:           fact.version,
		recency:           fact.recency,
		generation:        fact.id.Generation(),
		fieldSlots:        fact.fieldSlotSlice(),
		compactFieldSlots: fact.compactFieldSlots(compactSlotStore),
	}
}

func (f conditionFactRef) ID() FactID {
	return f.id
}

func (f conditionFactRef) Name() string {
	return f.name
}

func (f conditionFactRef) TemplateKey() TemplateKey {
	return f.templateKey
}

func (f conditionFactRef) Version() FactVersion {
	return f.version
}

func (f conditionFactRef) Recency() Recency {
	return f.recency
}

func (f conditionFactRef) Generation() Generation {
	return f.generation
}

func (f conditionFactRef) compiledFieldValue(field string, slot int) (Value, bool) {
	if slot >= 0 && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}
	if slot >= 0 && slot < len(f.compactFieldSlots) {
		return f.compactFieldSlots[slot].value()
	}
	if f.fields != nil {
		value, ok := f.fields[field]
		if ok {
			return value, true
		}
	}
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.compactFieldSlots) {
		return f.compactFieldSlots[slot].value()
	}
	return Value{}, false
}

func (f conditionFactRef) Fields() Fields {
	if f.fields != nil {
		return cloneFields(f.fields)
	}
	if len(f.compactFieldSlots) > 0 {
		return materializeFieldsFromCompactSlots(f.compactFieldSlots, f.fieldSpecs)
	}
	return materializeFieldsFromSlots(f.fieldSlots, f.fieldSpecs)
}

func (f conditionFactRef) Field(name string) (Value, bool) {
	value, ok := f.compiledFieldValue(name, -1)
	if !ok {
		return Value{}, false
	}
	return cloneValue(value), true
}

func (f conditionFactRef) FieldPresence(field string) (FieldPresence, bool) {
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.fieldSlots) {
		return f.fieldSlots[slot].presence.fieldPresence(), true
	}
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.compactFieldSlots) {
		return f.compactFieldSlots[slot].presence.fieldPresence(), true
	}
	return FieldPresence(""), false
}

func (f conditionFactRef) FieldPresenceMap() map[string]FieldPresence {
	if len(f.compactFieldSlots) > 0 {
		return materializePresenceFromCompactSlots(f.compactFieldSlots, f.fieldSpecs)
	}
	return materializePresenceFromSlots(f.fieldSlots, f.fieldSpecs)
}

func (f conditionFactRef) snapshot() FactSnapshot {
	return FactSnapshot{
		id:          f.id,
		name:        f.name,
		templateKey: f.templateKey,
		version:     f.version,
		recency:     f.recency,
		generation:  f.generation,
		fields:      cloneFields(f.fields),
		fieldSlots:  cloneFactSlots(f.materializeFieldSlots()),
		fieldSpecs:  f.fieldSpecs,
	}
}

func (f conditionFactRef) materializeFieldSlots() []factSlot {
	if len(f.fieldSlots) > 0 {
		return f.fieldSlots
	}
	return materializeFactSlotsFromCompactSlots(f.compactFieldSlots)
}

func (f conditionFactRef) fieldSlot(name string) (int, bool) {
	for i, spec := range f.fieldSpecs {
		if spec.Name == name {
			return i, true
		}
	}
	return -1, false
}

type workingFact struct {
	id                   FactID
	templateID           templateID
	version              FactVersion
	recency              Recency
	supportState         factSupportCode
	compactSlots         factCompactSlotRef
	payload              *workingFactPayload
	targetIndexesSkipped bool
}

type workingFactPayload struct {
	name          string
	templateKey   TemplateKey
	fields        Fields
	fieldSlots    []factSlot
	fieldPresence map[string]FieldPresence
}

type generatedFactFlags uint8

const generatedFactTargetIndexesSkipped generatedFactFlags = 1 << iota

type generatedFactStore struct {
	ids           []FactID
	templateIDs   []templateID
	versions      []FactVersion
	recencies     []Recency
	supportStates []factSupportCode
	compactSlots  []factCompactSlotRef
	flags         []generatedFactFlags
	names         map[int]string
	templateKeys  map[int]TemplateKey
}

func (s *generatedFactStore) len() int {
	if s == nil {
		return 0
	}
	return len(s.ids)
}

func (s *generatedFactStore) reset() {
	if s == nil {
		return
	}
	clear(s.ids)
	clear(s.templateIDs)
	clear(s.versions)
	clear(s.recencies)
	clear(s.supportStates)
	clear(s.compactSlots)
	clear(s.flags)
	s.ids = s.ids[:0]
	s.templateIDs = s.templateIDs[:0]
	s.versions = s.versions[:0]
	s.recencies = s.recencies[:0]
	s.supportStates = s.supportStates[:0]
	s.compactSlots = s.compactSlots[:0]
	clear(s.names)
	clear(s.templateKeys)
}

func (s *generatedFactStore) reserve(capacity int) {
	if s == nil {
		return
	}
	s.ids = growGeneratedFactColumn(s.ids, capacity)
	s.templateIDs = growGeneratedFactColumn(s.templateIDs, capacity)
	s.versions = growGeneratedFactColumn(s.versions, capacity)
	s.recencies = growGeneratedFactColumn(s.recencies, capacity)
	s.supportStates = growGeneratedFactColumn(s.supportStates, capacity)
	s.compactSlots = growGeneratedFactColumn(s.compactSlots, capacity)
	s.flags = growGeneratedFactColumn(s.flags, capacity)
}

func growGeneratedFactColumn[T any](in []T, capacity int) []T {
	if capacity <= cap(in) {
		return in
	}
	return slices.Grow(in, capacity-cap(in))
}

func (s *generatedFactStore) append(fact workingFact) int {
	if s == nil {
		return -1
	}
	row := len(s.ids)
	s.ids = append(s.ids, fact.id)
	s.templateIDs = append(s.templateIDs, fact.templateID)
	s.versions = append(s.versions, fact.version)
	s.recencies = append(s.recencies, fact.recency)
	s.supportStates = append(s.supportStates, fact.supportState)
	s.compactSlots = append(s.compactSlots, fact.compactSlots)
	var flags generatedFactFlags
	if fact.targetIndexesSkipped {
		flags |= generatedFactTargetIndexesSkipped
	}
	s.flags = append(s.flags, flags)
	if name := fact.storedName(); name != "" {
		if s.names == nil {
			s.names = make(map[int]string, 1)
		}
		s.names[row] = name
	}
	if key := fact.storedTemplateKey(); key != "" {
		if s.templateKeys == nil {
			s.templateKeys = make(map[int]TemplateKey, 1)
		}
		s.templateKeys[row] = key
	}
	return row
}

func (s *generatedFactStore) fact(row int) (*workingFact, bool) {
	if s == nil || row < 0 || row >= len(s.ids) {
		return nil, false
	}
	fact := &workingFact{
		id:                   s.ids[row],
		templateID:           s.templateIDs[row],
		version:              s.versions[row],
		recency:              s.recencies[row],
		supportState:         s.supportStates[row],
		compactSlots:         s.compactSlots[row],
		targetIndexesSkipped: s.flags[row]&generatedFactTargetIndexesSkipped != 0,
	}
	if name, ok := s.names[row]; ok {
		fact.setName(name)
	}
	if key, ok := s.templateKeys[row]; ok {
		fact.setTemplateKey(key)
	}
	return fact, true
}

func (s *generatedFactStore) replace(row int, fact *workingFact) bool {
	if s == nil || fact == nil || row < 0 || row >= len(s.ids) || s.ids[row] != fact.id {
		return false
	}
	s.templateIDs[row] = fact.templateID
	s.versions[row] = fact.version
	s.recencies[row] = fact.recency
	s.supportStates[row] = fact.supportState
	s.compactSlots[row] = fact.compactSlots
	var flags generatedFactFlags
	if fact.targetIndexesSkipped {
		flags |= generatedFactTargetIndexesSkipped
	}
	s.flags[row] = flags
	if name := fact.storedName(); name != "" {
		if s.names == nil {
			s.names = make(map[int]string, 1)
		}
		s.names[row] = name
	} else if s.names != nil {
		delete(s.names, row)
	}
	if key := fact.storedTemplateKey(); key != "" {
		if s.templateKeys == nil {
			s.templateKeys = make(map[int]TemplateKey, 1)
		}
		s.templateKeys[row] = key
	} else if s.templateKeys != nil {
		delete(s.templateKeys, row)
	}
	return true
}

func (s *generatedFactStore) remove(row int) (FactID, bool) {
	if s == nil || row < 0 || row >= len(s.ids) {
		return FactID{}, false
	}
	last := len(s.ids) - 1
	var moved FactID
	if row != last {
		moved = s.ids[last]
		s.ids[row] = s.ids[last]
		s.templateIDs[row] = s.templateIDs[last]
		s.versions[row] = s.versions[last]
		s.recencies[row] = s.recencies[last]
		s.supportStates[row] = s.supportStates[last]
		s.compactSlots[row] = s.compactSlots[last]
		s.flags[row] = s.flags[last]
		if name, ok := s.names[last]; ok {
			if s.names == nil {
				s.names = make(map[int]string, 1)
			}
			s.names[row] = name
			delete(s.names, last)
		} else if s.names != nil {
			delete(s.names, row)
		}
		if key, ok := s.templateKeys[last]; ok {
			if s.templateKeys == nil {
				s.templateKeys = make(map[int]TemplateKey, 1)
			}
			s.templateKeys[row] = key
			delete(s.templateKeys, last)
		} else if s.templateKeys != nil {
			delete(s.templateKeys, row)
		}
	} else {
		if s.names != nil {
			delete(s.names, row)
		}
		if s.templateKeys != nil {
			delete(s.templateKeys, row)
		}
	}
	s.ids[last] = FactID{}
	s.templateIDs[last] = 0
	s.versions[last] = 0
	s.recencies[last] = 0
	s.supportStates[last] = 0
	s.compactSlots[last] = factCompactSlotRef{}
	s.flags[last] = 0
	s.ids = s.ids[:last]
	s.templateIDs = s.templateIDs[:last]
	s.versions = s.versions[:last]
	s.recencies = s.recencies[:last]
	s.supportStates = s.supportStates[:last]
	s.compactSlots = s.compactSlots[:last]
	s.flags = s.flags[:last]
	return moved, !moved.IsZero()
}

func (s *generatedFactStore) truncate(length int) {
	if s == nil || length < 0 || length >= len(s.ids) {
		return
	}
	for i := length; i < len(s.ids); i++ {
		s.ids[i] = FactID{}
		s.templateIDs[i] = 0
		s.versions[i] = 0
		s.recencies[i] = 0
		s.supportStates[i] = 0
		s.compactSlots[i] = factCompactSlotRef{}
		s.flags[i] = 0
		if s.names != nil {
			delete(s.names, i)
		}
		if s.templateKeys != nil {
			delete(s.templateKeys, i)
		}
	}
	s.ids = s.ids[:length]
	s.templateIDs = s.templateIDs[:length]
	s.versions = s.versions[:length]
	s.recencies = s.recencies[:length]
	s.supportStates = s.supportStates[:length]
	s.compactSlots = s.compactSlots[:length]
	s.flags = s.flags[:length]
}

func cloneGeneratedFactStore(in generatedFactStore) generatedFactStore {
	return generatedFactStore{
		ids:           cloneFactIDs(in.ids),
		templateIDs:   slices.Clone(in.templateIDs),
		versions:      slices.Clone(in.versions),
		recencies:     slices.Clone(in.recencies),
		supportStates: slices.Clone(in.supportStates),
		compactSlots:  slices.Clone(in.compactSlots),
		flags:         slices.Clone(in.flags),
		names:         maps.Clone(in.names),
		templateKeys:  maps.Clone(in.templateKeys),
	}
}

func (f *workingFact) ensurePayload() *workingFactPayload {
	if f == nil {
		return nil
	}
	if f.payload == nil {
		f.payload = &workingFactPayload{}
	}
	return f.payload
}

func (f *workingFact) fieldsMap() Fields {
	if f == nil || f.payload == nil {
		return nil
	}
	return f.payload.fields
}

func (f *workingFact) storedName() string {
	if f == nil || f.payload == nil {
		return ""
	}
	return f.payload.name
}

func (f *workingFact) storedTemplateKey() TemplateKey {
	if f == nil || f.payload == nil {
		return ""
	}
	return f.payload.templateKey
}

func (f *workingFact) setTemplateKey(key TemplateKey) {
	if f == nil || key == "" && f.payload == nil {
		return
	}
	payload := f.ensurePayload()
	if payload == nil {
		return
	}
	payload.templateKey = key
	f.clearPayloadIfEmpty()
}

func (f *workingFact) setTemplateIdentity(key TemplateKey, id templateID) {
	if f == nil {
		return
	}
	f.templateID = id
	if id == 0 {
		f.setTemplateKey(key)
		return
	}
	f.setTemplateKey("")
}

func (f *workingFact) setName(name string) {
	if f == nil || name == "" && f.payload == nil {
		return
	}
	payload := f.ensurePayload()
	if payload == nil {
		return
	}
	payload.name = name
	f.clearPayloadIfEmpty()
}

func (f *workingFact) fieldSlotSlice() []factSlot {
	if f == nil || f.payload == nil {
		return nil
	}
	return f.payload.fieldSlots
}

func (f *workingFact) fieldPresenceMap() map[string]FieldPresence {
	if f == nil || f.payload == nil {
		return nil
	}
	return f.payload.fieldPresence
}

func (f *workingFact) setFields(fields Fields) {
	if f == nil || fields == nil && f.payload == nil {
		return
	}
	payload := f.ensurePayload()
	if payload == nil {
		return
	}
	payload.fields = fields
	f.clearPayloadIfEmpty()
}

func (f *workingFact) setFieldSlots(slots []factSlot) {
	if f == nil || len(slots) == 0 && f.payload == nil {
		return
	}
	payload := f.ensurePayload()
	if payload == nil {
		return
	}
	payload.fieldSlots = slots
	f.clearPayloadIfEmpty()
}

func (f *workingFact) setFieldPresence(presence map[string]FieldPresence) {
	if f == nil || presence == nil && f.payload == nil {
		return
	}
	payload := f.ensurePayload()
	if payload == nil {
		return
	}
	payload.fieldPresence = presence
	f.clearPayloadIfEmpty()
}

func (f *workingFact) clearPayload() {
	if f == nil {
		return
	}
	f.payload = nil
}

func (f *workingFact) clearFields() {
	if f == nil || f.payload == nil {
		return
	}
	f.payload.fields = nil
	f.clearPayloadIfEmpty()
}

func (f *workingFact) clearFieldSlots() {
	if f == nil || f.payload == nil {
		return
	}
	f.payload.fieldSlots = nil
	f.clearPayloadIfEmpty()
}

func (f *workingFact) clearFieldPresence() {
	if f == nil || f.payload == nil {
		return
	}
	f.payload.fieldPresence = nil
	f.clearPayloadIfEmpty()
}

func (f *workingFact) clearPayloadIfEmpty() {
	if f == nil || f.payload == nil {
		return
	}
	if f.payload.name == "" && f.payload.templateKey == "" && f.payload.fields == nil && len(f.payload.fieldSlots) == 0 && f.payload.fieldPresence == nil {
		f.payload = nil
	}
}

func cloneWorkingFactPayload(in *workingFactPayload) *workingFactPayload {
	if in == nil {
		return nil
	}
	return &workingFactPayload{
		name:          in.name,
		templateKey:   in.templateKey,
		fields:        cloneFields(in.fields),
		fieldSlots:    cloneFactSlots(in.fieldSlots),
		fieldPresence: cloneFieldPresence(in.fieldPresence),
	}
}

type factCompactSlotRef struct {
	start uint32
	count uint32
}

type factCompactSlotStore struct {
	slots []compactFactSlot
}

func newFactCompactSlotRef(store *factCompactSlotStore, start, count int) (factCompactSlotRef, bool) {
	if count == 0 {
		return factCompactSlotRef{}, true
	}
	if store == nil || start < 0 || count < 0 || start > math.MaxUint32 || count > math.MaxUint32 {
		return factCompactSlotRef{}, false
	}
	if start > len(store.slots) || count > len(store.slots)-start {
		return factCompactSlotRef{}, false
	}
	return factCompactSlotRef{start: uint32(start), count: uint32(count)}, true
}

func (r factCompactSlotRef) slots(store *factCompactSlotStore) []compactFactSlot {
	if store == nil || r.count == 0 {
		return nil
	}
	start := int(r.start)
	count := int(r.count)
	end := start + count
	if start < 0 || count < 0 || end < start || end > len(store.slots) {
		return nil
	}
	return store.slots[start:end:end]
}

func (s *factCompactSlotStore) reset(capacity int) {
	if s == nil {
		return
	}
	if capacity < 0 {
		capacity = 0
	}
	if s.slots == nil || cap(s.slots) < capacity {
		s.slots = make([]compactFactSlot, 0, capacity)
		return
	}
	for i := range s.slots {
		s.slots[i] = compactFactSlot{}
	}
	s.slots = s.slots[:0]
}

func (s *factCompactSlotStore) len() int {
	if s == nil {
		return 0
	}
	return len(s.slots)
}

func (s *factCompactSlotStore) cap() int {
	if s == nil {
		return 0
	}
	return cap(s.slots)
}

func (s *factCompactSlotStore) reserve(capacity int) {
	if s == nil || capacity <= cap(s.slots) {
		return
	}
	next := make([]compactFactSlot, len(s.slots), capacity)
	copy(next, s.slots)
	s.slots = next
}

func (s *factCompactSlotStore) reserveSlots(slotCount int) ([]compactFactSlot, int) {
	if s == nil {
		return nil, 0
	}
	if slotCount <= 0 {
		return nil, len(s.slots)
	}
	mark := len(s.slots)
	end := mark + slotCount
	if cap(s.slots) < end {
		s.reserve(max(max(cap(s.slots)*2, end), 16))
	}
	s.slots = s.slots[:end]
	return s.slots[mark:end:end], mark
}

func (s *factCompactSlotStore) appendFromFactSlots(fieldSlots []factSlot) (factCompactSlotRef, bool) {
	if s == nil {
		return factCompactSlotRef{}, false
	}
	if len(fieldSlots) == 0 {
		return factCompactSlotRef{}, true
	}
	start := len(s.slots)
	slots, _ := s.reserveSlots(len(fieldSlots))
	for i, slot := range fieldSlots {
		compact, ok := compactFactSlotFromFactSlot(slot)
		if !ok {
			s.rollback(start)
			return factCompactSlotRef{}, false
		}
		slots[i] = compact
	}
	return newFactCompactSlotRef(s, start, len(fieldSlots))
}

func (s *factCompactSlotStore) ref(mark int, slots []compactFactSlot) (factCompactSlotRef, bool) {
	if s == nil {
		return factCompactSlotRef{}, false
	}
	if len(slots) == 0 {
		return factCompactSlotRef{}, true
	}
	return newFactCompactSlotRef(s, mark, len(slots))
}

func (s *factCompactSlotStore) rollback(mark int) {
	if s == nil || mark < 0 || mark > len(s.slots) {
		return
	}
	for i := mark; i < len(s.slots); i++ {
		s.slots[i] = compactFactSlot{}
	}
	s.slots = s.slots[:mark]
}

func cloneFactCompactSlotStore(in *factCompactSlotStore) *factCompactSlotStore {
	if in == nil {
		return nil
	}
	out := &factCompactSlotStore{}
	out.slots = cloneCompactFactSlots(in.slots)
	return out
}

func (f *workingFact) compactFieldSlots(store *factCompactSlotStore) []compactFactSlot {
	if f == nil {
		return nil
	}
	return f.compactSlots.slots(store)
}

type factSupportCode uint8

const (
	factSupportStated factSupportCode = iota
	factSupportLogical
	factSupportStatedAndLogical
)

func factSupportCodeFromState(state FactSupportState) factSupportCode {
	switch state {
	case FactSupportLogical:
		return factSupportLogical
	case FactSupportStatedAndLogical:
		return factSupportStatedAndLogical
	default:
		return factSupportStated
	}
}

func (c factSupportCode) state() FactSupportState {
	switch c {
	case factSupportLogical:
		return FactSupportLogical
	case factSupportStatedAndLogical:
		return FactSupportStatedAndLogical
	default:
		return FactSupportStated
	}
}

func (f *workingFact) setSupportState(state FactSupportState) {
	if f == nil {
		return
	}
	f.supportState = factSupportCodeFromState(state)
}

type duplicateIndexKind uint8

const (
	duplicateIndexString duplicateIndexKind = iota
	duplicateIndexSingleInt
	duplicateIndexDoubleInt
	duplicateIndexSingleScalar
	duplicateIndexDoubleScalar
	duplicateIndexIntString
	duplicateIndexStringInt
	duplicateIndexIntStringString
	duplicateIndexStringStringInt
	duplicateIndexStructural
)

type duplicateScalarKind uint8

const (
	duplicateScalarNull duplicateScalarKind = iota
	duplicateScalarBool
	duplicateScalarInt
	duplicateScalarFloat
	duplicateScalarString
)

type duplicateScalarKey struct {
	kind        duplicateScalarKind
	bits        uint64
	stringValue string
}

func duplicateScalarKeyFromValue(value Value) (duplicateScalarKey, bool) {
	switch value.Kind() {
	case ValueNull:
		return duplicateScalarKey{kind: duplicateScalarNull}, true
	case ValueBool:
		var bits uint64
		if value.boolValue {
			bits = 1
		}
		return duplicateScalarKey{kind: duplicateScalarBool, bits: bits}, true
	case ValueInt:
		return duplicateScalarKey{kind: duplicateScalarInt, bits: uint64(value.intValue)}, true
	case ValueFloat:
		floating := value.floatValue
		if math.IsNaN(floating) {
			return duplicateScalarKey{}, false
		}
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return duplicateScalarKey{kind: duplicateScalarInt, bits: uint64(int64(floating))}, true
		}
		return duplicateScalarKey{kind: duplicateScalarFloat, bits: math.Float64bits(floating)}, true
	case ValueString:
		return duplicateScalarKey{kind: duplicateScalarString, stringValue: value.stringValue}, true
	default:
		return duplicateScalarKey{}, false
	}
}

func (k duplicateScalarKey) value() Value {
	switch k.kind {
	case duplicateScalarNull:
		return NullValue()
	case duplicateScalarBool:
		return newBoolValue(k.bits != 0)
	case duplicateScalarInt:
		return newIntValue(int64(k.bits))
	case duplicateScalarFloat:
		return newFloatValue(math.Float64frombits(k.bits))
	case duplicateScalarString:
		return newStringValue(k.stringValue)
	default:
		return Value{}
	}
}

type duplicateIndexKey struct {
	kind         duplicateIndexKind
	templateKey  TemplateKey
	firstInt     int64
	secondInt    int64
	first        duplicateScalarKey
	second       duplicateScalarKey
	stringValue  string
	stringValue2 string
	stringKey    DuplicateKey
	hash         uint64
}

type compiledGeneratedFactInsertPlan struct {
	template              Template
	templateID            templateID
	name                  string
	templateKey           TemplateKey
	duplicatePolicy       DuplicatePolicy
	duplicateIndexMode    duplicateIndexKind
	duplicateKeySlots     []int
	structuralHashPrefix  uint64
	structuralScalarKinds []ValueKind
	compactSlots          bool
	storeName             bool
	affectsRuleMatches    bool
	affectsRete           bool
}

func newCompiledGeneratedFactInsertPlan(template Template) compiledGeneratedFactInsertPlan {
	plan := compiledGeneratedFactInsertPlan{
		template:           template,
		templateID:         template.id,
		name:               template.Name(),
		templateKey:        template.Key(),
		duplicatePolicy:    template.duplicatePolicy,
		duplicateIndexMode: template.duplicateIndexMode,
		compactSlots:       templateSupportsCompactGeneratedSlots(template),
		affectsRuleMatches: true,
		affectsRete:        true,
	}
	if len(template.duplicateKeySlots) > 0 {
		plan.duplicateKeySlots = slices.Clone(template.duplicateKeySlots)
	}
	if template.duplicatePolicy == DuplicateStructural && template.closed && len(template.fields) > 0 {
		plan.structuralHashPrefix = structuralDuplicateHashString(structuralDuplicateHashOffset, template.key.String())
		plan.structuralScalarKinds = compiledStructuralDuplicateScalarKinds(template)
	}
	return plan
}

func (p *compiledGeneratedFactInsertPlan) valid() bool {
	return p != nil && p.templateKey != ""
}

func (p *compiledGeneratedFactInsertPlan) duplicateIndex(slots []factSlot) duplicateIndexKey {
	switch p.duplicatePolicy {
	case DuplicateAllow:
		return duplicateIndexKey{}
	case DuplicateUniqueKey:
		if index, ok := p.typedDuplicateIndex(slots); ok {
			return index
		}
	case DuplicateStructural:
		if index, ok := p.structuralDuplicateIndex(slots); ok {
			return index
		}
	}
	return makeDuplicateIndexForValidatedFact(p.name, p.template, nil, slots)
}

func (p *compiledGeneratedFactInsertPlan) typedDuplicateIndex(slots []factSlot) (duplicateIndexKey, bool) {
	if p.duplicatePolicy != DuplicateUniqueKey {
		return duplicateIndexKey{}, false
	}
	return p.typedDuplicateIndexFromScalar(func(slot int) (duplicateScalarKey, bool) {
		return duplicateScalarKeyFromFactSlot(slots, slot)
	})
}

func (p *compiledGeneratedFactInsertPlan) typedDuplicateIndexFromCompact(slots []compactFactSlot) (duplicateIndexKey, bool) {
	if p.duplicatePolicy != DuplicateUniqueKey {
		return duplicateIndexKey{}, false
	}
	return p.typedDuplicateIndexFromScalar(func(slot int) (duplicateScalarKey, bool) {
		return duplicateScalarKeyFromCompactFactSlot(slots, slot)
	})
}

func (p *compiledGeneratedFactInsertPlan) typedDuplicateIndexFromScalar(slotValue func(int) (duplicateScalarKey, bool)) (duplicateIndexKey, bool) {
	switch p.duplicateIndexMode {
	case duplicateIndexSingleScalar:
		if len(p.duplicateKeySlots) != 1 {
			return duplicateIndexKey{}, false
		}
		first, ok := slotValue(p.duplicateKeySlots[0])
		if !ok {
			return duplicateIndexKey{}, false
		}
		if first.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexSingleInt,
				templateKey: p.templateKey,
				firstInt:    int64(first.bits),
			}, true
		}
		return duplicateIndexKey{
			kind:        duplicateIndexSingleScalar,
			templateKey: p.templateKey,
			first:       first,
		}, true
	case duplicateIndexDoubleScalar:
		if len(p.duplicateKeySlots) != 2 {
			return duplicateIndexKey{}, false
		}
		first, ok := slotValue(p.duplicateKeySlots[0])
		if !ok {
			return duplicateIndexKey{}, false
		}
		second, ok := slotValue(p.duplicateKeySlots[1])
		if !ok {
			return duplicateIndexKey{}, false
		}
		if first.kind == duplicateScalarInt && second.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexDoubleInt,
				templateKey: p.templateKey,
				firstInt:    int64(first.bits),
				secondInt:   int64(second.bits),
			}, true
		}
		if first.kind == duplicateScalarInt && second.kind == duplicateScalarString {
			return duplicateIndexKey{
				kind:        duplicateIndexIntString,
				templateKey: p.templateKey,
				firstInt:    int64(first.bits),
				stringValue: second.stringValue,
			}, true
		}
		if first.kind == duplicateScalarString && second.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexStringInt,
				templateKey: p.templateKey,
				firstInt:    int64(second.bits),
				stringValue: first.stringValue,
			}, true
		}
		return duplicateIndexKey{
			kind:        duplicateIndexDoubleScalar,
			templateKey: p.templateKey,
			first:       first,
			second:      second,
		}, true
	case duplicateIndexIntStringString:
		if len(p.duplicateKeySlots) != 3 {
			return duplicateIndexKey{}, false
		}
		first, ok := slotValue(p.duplicateKeySlots[0])
		if !ok || first.kind != duplicateScalarInt {
			return duplicateIndexKey{}, false
		}
		second, ok := slotValue(p.duplicateKeySlots[1])
		if !ok || second.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		third, ok := slotValue(p.duplicateKeySlots[2])
		if !ok || third.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		return duplicateIndexKey{
			kind:         duplicateIndexIntStringString,
			templateKey:  p.templateKey,
			firstInt:     int64(first.bits),
			stringValue:  second.stringValue,
			stringValue2: third.stringValue,
		}, true
	case duplicateIndexStringStringInt:
		if len(p.duplicateKeySlots) != 3 {
			return duplicateIndexKey{}, false
		}
		first, ok := slotValue(p.duplicateKeySlots[0])
		if !ok || first.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		second, ok := slotValue(p.duplicateKeySlots[1])
		if !ok || second.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		third, ok := slotValue(p.duplicateKeySlots[2])
		if !ok || third.kind != duplicateScalarInt {
			return duplicateIndexKey{}, false
		}
		return duplicateIndexKey{
			kind:         duplicateIndexStringStringInt,
			templateKey:  p.templateKey,
			firstInt:     int64(third.bits),
			stringValue:  first.stringValue,
			stringValue2: second.stringValue,
		}, true
	default:
		return duplicateIndexKey{}, false
	}
}

func (p *compiledGeneratedFactInsertPlan) duplicateIndexFromCompact(slots []compactFactSlot) duplicateIndexKey {
	switch p.duplicatePolicy {
	case DuplicateAllow:
		return duplicateIndexKey{}
	case DuplicateUniqueKey:
		if index, ok := p.typedDuplicateIndexFromCompact(slots); ok {
			return index
		}
	case DuplicateStructural:
		materialized := materializeFactSlotsFromCompactSlots(slots)
		if index, ok := p.structuralDuplicateIndex(materialized); ok {
			return index
		}
		return makeDuplicateIndexForValidatedFact(p.name, p.template, nil, materialized)
	}
	return makeDuplicateIndexForValidatedFact(p.name, p.template, nil, materializeFactSlotsFromCompactSlots(slots))
}

func duplicateScalarKeyFromFactSlot(slots []factSlot, slot int) (duplicateScalarKey, bool) {
	if slot < 0 || slot >= len(slots) {
		return duplicateScalarKey{}, false
	}
	current := slots[slot]
	if !current.ok {
		return duplicateScalarKey{}, false
	}
	return duplicateScalarKeyFromValue(current.value)
}

func duplicateScalarKeyFromCompactFactSlot(slots []compactFactSlot, slot int) (duplicateScalarKey, bool) {
	if slot < 0 || slot >= len(slots) {
		return duplicateScalarKey{}, false
	}
	current := slots[slot]
	if !current.ok {
		return duplicateScalarKey{}, false
	}
	return duplicateScalarKey{
		kind:        current.kind,
		bits:        current.bits,
		stringValue: current.stringValue,
	}, true
}

func (p *compiledGeneratedFactInsertPlan) structuralDuplicateIndex(slots []factSlot) (duplicateIndexKey, bool) {
	if p.duplicatePolicy != DuplicateStructural || p.structuralHashPrefix == 0 || len(slots) == 0 || len(p.template.fields) == 0 {
		return duplicateIndexKey{}, false
	}
	hash, ok := p.structuralScalarDuplicateHash(slots)
	if !ok {
		hash, ok = structuralDuplicateSlotsHashWithPrefix(p.template, slots, p.structuralHashPrefix)
	}
	if !ok {
		return duplicateIndexKey{}, false
	}
	return duplicateIndexKey{
		kind:        duplicateIndexStructural,
		templateKey: p.templateKey,
		hash:        hash,
	}, true
}

func compiledStructuralDuplicateScalarKinds(template Template) []ValueKind {
	if template.duplicatePolicy != DuplicateStructural || !template.closed || len(template.fields) == 0 {
		return nil
	}
	kinds := make([]ValueKind, len(template.fields))
	for i, field := range template.fields {
		switch field.Kind {
		case ValueNull, ValueBool, ValueInt, ValueFloat, ValueString:
			kinds[i] = field.Kind
		default:
			return nil
		}
	}
	return kinds
}

func (p *compiledGeneratedFactInsertPlan) structuralScalarDuplicateHash(slots []factSlot) (uint64, bool) {
	if len(p.structuralScalarKinds) == 0 {
		return 0, false
	}
	hash := p.structuralHashPrefix
	for i := range p.structuralScalarKinds {
		if i >= len(slots) {
			break
		}
		slot := slots[i]
		if !slot.ok {
			hash = structuralDuplicateHashByte(hash, 0)
			continue
		}
		var ok bool
		hash, ok = structuralDuplicateHashKnownScalarValue(hash, p.structuralScalarKinds[i], slot.value)
		if !ok {
			return 0, false
		}
	}
	return hash, true
}

func (p *compiledGeneratedFactInsertPlan) structuralScalarDuplicateSlotsEqual(left, right []factSlot) (bool, bool) {
	if len(p.structuralScalarKinds) == 0 {
		return false, false
	}
	for i, kind := range p.structuralScalarKinds {
		var leftSlot, rightSlot factSlot
		if i < len(left) {
			leftSlot = left[i]
		}
		if i < len(right) {
			rightSlot = right[i]
		}
		if leftSlot.ok != rightSlot.ok {
			return false, true
		}
		if !leftSlot.ok {
			continue
		}
		equal, ok := structuralDuplicateScalarValuesEqual(kind, leftSlot.value, rightSlot.value)
		if !ok {
			return false, false
		}
		if !equal {
			return false, true
		}
	}
	return true, true
}

func (p *compiledGeneratedFactInsertPlan) structuralScalarDuplicateWorkingFactEqual(left []factSlot, right *workingFact, compactSlotStore *factCompactSlotStore) (bool, bool) {
	if right == nil {
		return false, true
	}
	compactSlots := right.compactFieldSlots(compactSlotStore)
	if len(compactSlots) == 0 {
		return p.structuralScalarDuplicateSlotsEqual(left, right.fieldSlotSlice())
	}
	if len(p.structuralScalarKinds) == 0 {
		return false, false
	}
	for i, kind := range p.structuralScalarKinds {
		var leftSlot factSlot
		if i < len(left) {
			leftSlot = left[i]
		}
		var rightSlot compactFactSlot
		if i < len(compactSlots) {
			rightSlot = compactSlots[i]
		}
		if leftSlot.ok != rightSlot.ok {
			return false, true
		}
		if !leftSlot.ok {
			continue
		}
		rightValue, ok := rightSlot.value()
		if !ok {
			return false, false
		}
		equal, ok := structuralDuplicateScalarValuesEqual(kind, leftSlot.value, rightValue)
		if !ok {
			return false, false
		}
		if !equal {
			return false, true
		}
	}
	return true, true
}

func (k duplicateIndexKey) isZero() bool {
	return k.kind == duplicateIndexString && k.templateKey == "" && k.stringKey == ""
}

func (k duplicateIndexKey) publicKeyForTemplate(name string, template Template) DuplicateKey {
	switch k.kind {
	case duplicateIndexSingleInt:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[0], k.firstInt)
		return DuplicateKey(b.String())
	case duplicateIndexDoubleInt:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[0], k.firstInt)
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[1], k.secondInt)
		return DuplicateKey(b.String())
	case duplicateIndexIntString:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[0], k.firstInt)
		writeDuplicateStringKeyEntry(&b, template.duplicateKeyNames[1], k.stringValue)
		return DuplicateKey(b.String())
	case duplicateIndexStringInt:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateStringKeyEntry(&b, template.duplicateKeyNames[0], k.stringValue)
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[1], k.firstInt)
		return DuplicateKey(b.String())
	case duplicateIndexIntStringString:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[0], k.firstInt)
		writeDuplicateStringKeyEntry(&b, template.duplicateKeyNames[1], k.stringValue)
		writeDuplicateStringKeyEntry(&b, template.duplicateKeyNames[2], k.stringValue2)
		return DuplicateKey(b.String())
	case duplicateIndexStringStringInt:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateStringKeyEntry(&b, template.duplicateKeyNames[0], k.stringValue)
		writeDuplicateStringKeyEntry(&b, template.duplicateKeyNames[1], k.stringValue2)
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[2], k.firstInt)
		return DuplicateKey(b.String())
	case duplicateIndexSingleScalar:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateScalarKeyEntry(&b, template.duplicateKeyNames[0], k.first)
		return DuplicateKey(b.String())
	case duplicateIndexDoubleScalar:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateScalarKeyEntry(&b, template.duplicateKeyNames[0], k.first)
		writeDuplicateScalarKeyEntry(&b, template.duplicateKeyNames[1], k.second)
		return DuplicateKey(b.String())
	default:
		return k.stringKey
	}
}

func (k duplicateIndexKey) publicKeyCapacity(name string, template Template) int {
	size := len("name:") + len(name) + len("|template:") + len(k.templateKey) + len("|fields:")
	switch k.kind {
	case duplicateIndexSingleInt:
		size += len(template.duplicateKeyNames[0]) + 2 + len("number:") + int64Len(k.firstInt)
	case duplicateIndexDoubleInt:
		size += len(template.duplicateKeyNames[0]) + 2 + len("number:") + int64Len(k.firstInt)
		size += len(template.duplicateKeyNames[1]) + 2 + len("number:") + int64Len(k.secondInt)
	case duplicateIndexIntString:
		size += len(template.duplicateKeyNames[0]) + 2 + len("number:") + int64Len(k.firstInt)
		size += len(template.duplicateKeyNames[1]) + 2 + duplicateStringKeyValueCapacity(k.stringValue)
	case duplicateIndexStringInt:
		size += len(template.duplicateKeyNames[0]) + 2 + duplicateStringKeyValueCapacity(k.stringValue)
		size += len(template.duplicateKeyNames[1]) + 2 + len("number:") + int64Len(k.firstInt)
	case duplicateIndexIntStringString:
		size += len(template.duplicateKeyNames[0]) + 2 + len("number:") + int64Len(k.firstInt)
		size += len(template.duplicateKeyNames[1]) + 2 + duplicateStringKeyValueCapacity(k.stringValue)
		size += len(template.duplicateKeyNames[2]) + 2 + duplicateStringKeyValueCapacity(k.stringValue2)
	case duplicateIndexStringStringInt:
		size += len(template.duplicateKeyNames[0]) + 2 + duplicateStringKeyValueCapacity(k.stringValue)
		size += len(template.duplicateKeyNames[1]) + 2 + duplicateStringKeyValueCapacity(k.stringValue2)
		size += len(template.duplicateKeyNames[2]) + 2 + len("number:") + int64Len(k.firstInt)
	case duplicateIndexSingleScalar:
		size += len(template.duplicateKeyNames[0]) + 2 + duplicateScalarKeyValueCapacity(k.first)
	case duplicateIndexDoubleScalar:
		size += len(template.duplicateKeyNames[0]) + 2 + duplicateScalarKeyValueCapacity(k.first)
		size += len(template.duplicateKeyNames[1]) + 2 + duplicateScalarKeyValueCapacity(k.second)
	}
	return size
}

func writeDuplicateIntKeyEntry(b *strings.Builder, fieldName string, value int64) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	b.WriteString("number:")
	var buf [20]byte
	b.Write(strconv.AppendInt(buf[:0], value, 10))
	b.WriteByte(';')
}

func writeDuplicateStringKeyEntry(b *strings.Builder, fieldName string, value string) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	b.WriteString("string:")
	b.WriteString(strconv.Quote(value))
	b.WriteByte(';')
}

func writeDuplicateScalarKeyEntry(b *strings.Builder, fieldName string, value duplicateScalarKey) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	encodeDuplicateScalarKey(b, value)
	b.WriteByte(';')
}

func encodeDuplicateScalarKey(b *strings.Builder, value duplicateScalarKey) {
	switch value.kind {
	case duplicateScalarNull:
		b.WriteString("null")
	case duplicateScalarBool:
		b.WriteString("bool:")
		if value.bits != 0 {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case duplicateScalarInt:
		b.WriteString("number:")
		var buf [20]byte
		b.Write(strconv.AppendInt(buf[:0], int64(value.bits), 10))
	case duplicateScalarFloat:
		b.WriteString("number:")
		floating := math.Float64frombits(value.bits)
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			var buf [20]byte
			b.Write(strconv.AppendInt(buf[:0], int64(floating), 10))
			return
		}
		var buf [32]byte
		b.Write(strconv.AppendFloat(buf[:0], floating, 'g', -1, 64))
	case duplicateScalarString:
		b.WriteString("string:")
		b.WriteString(strconv.Quote(value.stringValue))
	}
}

func duplicateScalarKeyValueCapacity(value duplicateScalarKey) int {
	switch value.kind {
	case duplicateScalarNull:
		return len("null")
	case duplicateScalarBool:
		if value.bits != 0 {
			return len("bool:true")
		}
		return len("bool:false")
	case duplicateScalarInt:
		return len("number:") + int64Len(int64(value.bits))
	case duplicateScalarFloat:
		floating := math.Float64frombits(value.bits)
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return len("number:") + int64Len(int64(floating))
		}
		var buf [32]byte
		return len("number:") + len(strconv.AppendFloat(buf[:0], floating, 'g', -1, 64))
	case duplicateScalarString:
		return len("string:") + len(value.stringValue) + len(`""`) + len(`\u0000`)
	default:
		return len("any")
	}
}

func duplicateStringKeyValueCapacity(value string) int {
	return len("string:") + len(value) + len(`""`) + len(`\u0000`)
}

type factSlot struct {
	value    Value
	presence fieldPresenceCode
	ok       bool
}

type compactFactSlot struct {
	stringValue string
	bits        uint64
	kind        duplicateScalarKind
	presence    fieldPresenceCode
	ok          bool
}

func compactFactSlotFromFactSlot(slot factSlot) (compactFactSlot, bool) {
	out := compactFactSlot{presence: slot.presence, ok: slot.ok}
	if !slot.ok {
		return out, true
	}
	return compactFactSlotFromValue(slot.value, slot.presence)
}

func compactFactSlotFromValue(value Value, presence fieldPresenceCode) (compactFactSlot, bool) {
	key, ok := duplicateScalarKeyFromValue(value)
	if !ok {
		return compactFactSlot{}, false
	}
	return compactFactSlot{
		stringValue: key.stringValue,
		bits:        key.bits,
		kind:        key.kind,
		presence:    presence,
		ok:          true,
	}, true
}

func (s compactFactSlot) value() (Value, bool) {
	if !s.ok {
		return Value{}, false
	}
	switch s.kind {
	case duplicateScalarNull:
		return NullValue(), true
	case duplicateScalarBool:
		return newBoolValue(s.bits != 0), true
	case duplicateScalarInt:
		return newIntValue(int64(s.bits)), true
	case duplicateScalarFloat:
		return newFloatValue(math.Float64frombits(s.bits)), true
	case duplicateScalarString:
		return newStringValue(s.stringValue), true
	default:
		return Value{}, false
	}
}

type fieldPresenceCode uint8

const (
	fieldPresenceOmitted fieldPresenceCode = iota
	fieldPresenceDefault
	fieldPresenceExplicit
)

func encodeFieldPresence(presence FieldPresence) fieldPresenceCode {
	switch presence {
	case FieldPresenceDefault:
		return fieldPresenceDefault
	case FieldPresenceExplicit:
		return fieldPresenceExplicit
	default:
		return fieldPresenceOmitted
	}
}

func (p fieldPresenceCode) fieldPresence() FieldPresence {
	switch p {
	case fieldPresenceDefault:
		return FieldPresenceDefault
	case fieldPresenceExplicit:
		return FieldPresenceExplicit
	default:
		return FieldPresenceOmitted
	}
}

func (f *workingFact) snapshot() FactSnapshot {
	return f.snapshotForRevision(nil, nil)
}

func (f *workingFact) snapshotForRevision(revision *Ruleset, compactSlotStore *factCompactSlotStore) FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.nameForRevision(revision),
		templateKey:   f.templateKeyForRevision(revision),
		version:       f.version,
		recency:       f.recency,
		generation:    f.id.Generation(),
		fields:        cloneFields(f.fieldsMap()),
		fieldSlots:    f.materializeFieldSlots(compactSlotStore),
		fieldSpecs:    f.fieldSpecsForRevision(revision, compactSlotStore),
		fieldPresence: cloneFieldPresence(f.fieldPresenceMap()),
		support:       FactSupportProvenance{State: f.resolvedSupportState()},
	}
}

func (f *workingFact) detachedSnapshot() FactSnapshot {
	return f.detachedSnapshotForRevision(nil, nil)
}

func (f *workingFact) detachedSnapshotForRevision(revision *Ruleset, compactSlotStore *factCompactSlotStore) FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.nameForRevision(revision),
		templateKey:   f.templateKeyForRevision(revision),
		version:       f.version,
		recency:       f.recency,
		generation:    f.id.Generation(),
		fields:        f.fieldsMap(),
		fieldSlots:    f.materializeFieldSlots(compactSlotStore),
		fieldSpecs:    f.fieldSpecsForRevision(revision, compactSlotStore),
		fieldPresence: f.fieldPresenceMap(),
		support:       FactSupportProvenance{State: f.resolvedSupportState()},
	}
}

func (f *workingFact) resolvedSupportState() FactSupportState {
	if f == nil {
		return FactSupportStated
	}
	return f.supportState.state()
}

func (f *workingFact) nameForRevision(revision *Ruleset) string {
	if f == nil {
		return ""
	}
	if name := f.storedName(); name != "" {
		return name
	}
	if revision != nil {
		if template, ok := f.templateForRevision(revision); ok {
			return template.Name()
		}
	}
	return ""
}

func (f *workingFact) templateKeyForTarget(target conditionTarget) TemplateKey {
	if f == nil {
		return ""
	}
	if key := f.storedTemplateKey(); key != "" {
		return key
	}
	if target.templateKey != "" && target.templateID != 0 && f.templateID == target.templateID {
		return target.templateKey
	}
	if target.kind == conditionTargetTemplateKey && target.templateID != 0 && f.templateID == target.templateID {
		return target.templateKey
	}
	return ""
}

func (f *workingFact) matchesTemplateTarget(target conditionTarget) bool {
	if f == nil || target.kind != conditionTargetTemplateKey {
		return false
	}
	if target.templateID != 0 && f.templateID == target.templateID {
		return true
	}
	return f.storedTemplateKey() == target.templateKey
}

func (f *workingFact) templateKeyForRevision(revision *Ruleset) TemplateKey {
	if f == nil {
		return ""
	}
	if key := f.storedTemplateKey(); key != "" {
		return key
	}
	if template, ok := f.templateForRevision(revision); ok {
		return template.Key()
	}
	return ""
}

func (f *workingFact) templateForRevision(revision *Ruleset) (Template, bool) {
	if f == nil || revision == nil {
		return Template{}, false
	}
	if f.templateID != 0 {
		return revision.templateByID(f.templateID)
	}
	if key := f.storedTemplateKey(); key != "" {
		return revision.templateByKey(key)
	}
	return Template{}, false
}

func (f *workingFact) fieldSpecsForRevision(revision *Ruleset, compactSlotStore *factCompactSlotStore) []FieldSpec {
	if f == nil || f.fieldSlotCount(compactSlotStore) == 0 || revision == nil {
		return nil
	}
	template, ok := f.templateForRevision(revision)
	if !ok {
		return nil
	}
	return template.fields
}

func (f *workingFact) duplicateIndexForRevision(revision *Ruleset, compactSlotStore *factCompactSlotStore) duplicateIndexKey {
	if f == nil {
		return duplicateIndexKey{}
	}
	template := Template{key: f.templateKeyForRevision(revision)}
	if revision != nil {
		if resolved, ok := f.templateForRevision(revision); ok {
			template = resolved
		}
	}
	if template.duplicatePolicy == DuplicateAllow {
		return duplicateIndexKey{}
	}
	return makeDuplicateIndexForValidatedFact(f.nameForRevision(revision), template, f.fieldsMap(), f.materializeFieldSlots(compactSlotStore))
}

func (f *workingFact) publicDuplicateKey(revision *Ruleset, compactSlotStore *factCompactSlotStore) DuplicateKey {
	duplicateIndex := f.duplicateIndexForRevision(revision, compactSlotStore)
	if f == nil || duplicateIndex.isZero() {
		return ""
	}
	template := Template{key: f.templateKeyForRevision(revision)}
	name := f.nameForRevision(revision)
	if revision != nil {
		if resolved, ok := f.templateForRevision(revision); ok {
			template = resolved
		}
	}
	if duplicateIndex.kind == duplicateIndexStructural {
		return makeDuplicateKeyForTemplateWithSlots(name, template, nil, f.materializeFieldSlots(compactSlotStore))
	}
	return duplicateIndex.publicKeyForTemplate(name, template)
}

func (f *workingFact) fieldSlotCount(compactSlotStore *factCompactSlotStore) int {
	if f == nil {
		return 0
	}
	fieldSlots := f.fieldSlotSlice()
	if len(fieldSlots) > 0 {
		return len(fieldSlots)
	}
	return len(f.compactFieldSlots(compactSlotStore))
}

func (f *workingFact) materializeFieldSlots(compactSlotStore *factCompactSlotStore) []factSlot {
	if f == nil {
		return nil
	}
	fieldSlots := f.fieldSlotSlice()
	if len(fieldSlots) > 0 {
		return fieldSlots
	}
	return materializeFactSlotsFromCompactSlots(f.compactFieldSlots(compactSlotStore))
}

func (f FactSnapshot) clone() FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.name,
		templateKey:   f.templateKey,
		version:       f.version,
		recency:       f.recency,
		generation:    f.generation,
		fields:        cloneFields(f.fields),
		fieldSlots:    cloneFactSlots(f.fieldSlots),
		fieldSpecs:    f.fieldSpecs,
		fieldPresence: cloneFieldPresence(f.fieldPresence),
		support:       f.support,
	}
}

func (f FactSnapshot) String() string {
	fields := f.Fields()
	presence := f.FieldPresenceMap()

	var b strings.Builder
	b.WriteString("Fact{")
	b.WriteString("id:")
	b.WriteString(f.id.String())
	b.WriteString(", name:")
	b.WriteString(f.name)
	b.WriteString(", template:")
	b.WriteString(f.templateKey.String())
	b.WriteString(", version:")
	b.WriteString(strconv.FormatUint(uint64(f.version), 10))
	b.WriteString(", recency:")
	b.WriteString(strconv.FormatUint(uint64(f.recency), 10))
	b.WriteString(", generation:")
	b.WriteString(strconv.FormatUint(uint64(f.generation), 10))
	b.WriteString(", fields:{")
	emitOrderedFieldsString(&b, fields)
	b.WriteString("}, presence:{")
	emitOrderedPresenceString(&b, presence)
	b.WriteString("}}")
	return b.String()
}

func emitOrderedFieldsString(b *strings.Builder, fields Fields) {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(fields[key].String())
	}
}

func emitOrderedPresenceString(b *strings.Builder, presence map[string]FieldPresence) {
	keys := make([]string, 0, len(presence))
	for key := range presence {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(string(presence[key]))
	}
}

func makeDuplicateKey(name string, templateKey TemplateKey, fields Fields) DuplicateKey {
	return makeDuplicateKeyForTemplate(name, Template{key: templateKey}, fields)
}

func makeDuplicateKeyForTemplate(name string, template Template, fields Fields) DuplicateKey {
	var b strings.Builder
	b.WriteString("name:")
	b.WriteString(name)
	b.WriteString("|template:")
	b.WriteString(template.key.String())
	b.WriteString("|fields:")
	emitDuplicateKeyFields(&b, template, fields, nil)
	return DuplicateKey(b.String())
}

func makeDuplicateKeyForValidatedFact(name string, template Template, fields Fields, slots []factSlot) DuplicateKey {
	_, duplicateKey := makeDuplicateIdentityForValidatedFact(name, template, fields, slots)
	return duplicateKey
}

func makeDuplicateIdentityForValidatedFact(name string, template Template, fields Fields, slots []factSlot) (duplicateIndexKey, DuplicateKey) {
	index := makeDuplicateIndexForValidatedFact(name, template, fields, slots)
	return index, index.publicKeyForTemplate(name, template)
}

func makeDuplicateIndexForValidatedFact(name string, template Template, fields Fields, slots []factSlot) duplicateIndexKey {
	if template.duplicatePolicy == DuplicateAllow {
		return duplicateIndexKey{}
	}

	if index, ok := makeTypedDuplicateIndexForValidatedFact(name, template, fields, slots); ok {
		return index
	}

	if index, ok := makeStructuralDuplicateIndexForValidatedFact(template, slots); ok {
		return index
	}

	if len(slots) > 0 {
		duplicateKey := makeDuplicateKeyForTemplateWithSlots(name, template, fields, slots)
		return duplicateIndexKey{
			kind:        duplicateIndexString,
			templateKey: template.key,
			stringKey:   duplicateKey,
		}
	}

	duplicateKey := makeDuplicateKeyForTemplate(name, template, fields)
	return duplicateIndexKey{
		kind:        duplicateIndexString,
		templateKey: template.key,
		stringKey:   duplicateKey,
	}
}

func makeStructuralDuplicateIndexForValidatedFact(template Template, slots []factSlot) (duplicateIndexKey, bool) {
	if template.duplicatePolicy != DuplicateStructural || !template.closed || len(slots) == 0 || len(template.fields) == 0 {
		return duplicateIndexKey{}, false
	}
	hash, ok := structuralDuplicateSlotsHash(template, slots)
	if !ok {
		return duplicateIndexKey{}, false
	}
	return duplicateIndexKey{
		kind:        duplicateIndexStructural,
		templateKey: template.key,
		hash:        hash,
	}, true
}

func makeDuplicateKeyForTemplateWithSlots(name string, template Template, fields Fields, slots []factSlot) DuplicateKey {
	var b strings.Builder
	if len(slots) > 0 {
		b.Grow(duplicateKeyCapacity(name, template, fields, slots))
	}
	b.WriteString("name:")
	b.WriteString(name)
	b.WriteString("|template:")
	b.WriteString(template.key.String())
	b.WriteString("|fields:")
	emitDuplicateKeyFields(&b, template, fields, slots)
	return DuplicateKey(b.String())
}

func makeTypedDuplicateIndexForValidatedFact(name string, template Template, fields Fields, slots []factSlot) (duplicateIndexKey, bool) {
	switch template.duplicateIndexMode {
	case duplicateIndexSingleScalar:
		fieldName := template.duplicateKeyNames[0]
		value, ok := duplicateFieldValue(fieldName, template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		scalar, ok := duplicateScalarKeyFromValue(value)
		if !ok {
			return duplicateIndexKey{}, false
		}
		if scalar.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexSingleInt,
				templateKey: template.key,
				firstInt:    int64(scalar.bits),
			}, true
		}
		return duplicateIndexKey{
			kind:        duplicateIndexSingleScalar,
			templateKey: template.key,
			first:       scalar,
		}, true
	case duplicateIndexDoubleScalar:
		firstField := template.duplicateKeyNames[0]
		secondField := template.duplicateKeyNames[1]
		firstValue, ok := duplicateFieldValue(firstField, template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		secondValue, ok := duplicateFieldValue(secondField, template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		firstScalar, ok := duplicateScalarKeyFromValue(firstValue)
		if !ok {
			return duplicateIndexKey{}, false
		}
		secondScalar, ok := duplicateScalarKeyFromValue(secondValue)
		if !ok {
			return duplicateIndexKey{}, false
		}
		if firstScalar.kind == duplicateScalarInt && secondScalar.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexDoubleInt,
				templateKey: template.key,
				firstInt:    int64(firstScalar.bits),
				secondInt:   int64(secondScalar.bits),
			}, true
		}
		if firstScalar.kind == duplicateScalarInt && secondScalar.kind == duplicateScalarString {
			return duplicateIndexKey{
				kind:        duplicateIndexIntString,
				templateKey: template.key,
				firstInt:    int64(firstScalar.bits),
				stringValue: secondScalar.stringValue,
			}, true
		}
		if firstScalar.kind == duplicateScalarString && secondScalar.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexStringInt,
				templateKey: template.key,
				firstInt:    int64(secondScalar.bits),
				stringValue: firstScalar.stringValue,
			}, true
		}
		return duplicateIndexKey{
			kind:        duplicateIndexDoubleScalar,
			templateKey: template.key,
			first:       firstScalar,
			second:      secondScalar,
		}, true
	case duplicateIndexIntStringString:
		firstValue, ok := duplicateFieldValue(template.duplicateKeyNames[0], template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		secondValue, ok := duplicateFieldValue(template.duplicateKeyNames[1], template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		thirdValue, ok := duplicateFieldValue(template.duplicateKeyNames[2], template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		firstScalar, ok := duplicateScalarKeyFromValue(firstValue)
		if !ok || firstScalar.kind != duplicateScalarInt {
			return duplicateIndexKey{}, false
		}
		secondScalar, ok := duplicateScalarKeyFromValue(secondValue)
		if !ok || secondScalar.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		thirdScalar, ok := duplicateScalarKeyFromValue(thirdValue)
		if !ok || thirdScalar.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		return duplicateIndexKey{
			kind:         duplicateIndexIntStringString,
			templateKey:  template.key,
			firstInt:     int64(firstScalar.bits),
			stringValue:  secondScalar.stringValue,
			stringValue2: thirdScalar.stringValue,
		}, true
	case duplicateIndexStringStringInt:
		firstValue, ok := duplicateFieldValue(template.duplicateKeyNames[0], template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		secondValue, ok := duplicateFieldValue(template.duplicateKeyNames[1], template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		thirdValue, ok := duplicateFieldValue(template.duplicateKeyNames[2], template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		firstScalar, ok := duplicateScalarKeyFromValue(firstValue)
		if !ok || firstScalar.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		secondScalar, ok := duplicateScalarKeyFromValue(secondValue)
		if !ok || secondScalar.kind != duplicateScalarString {
			return duplicateIndexKey{}, false
		}
		thirdScalar, ok := duplicateScalarKeyFromValue(thirdValue)
		if !ok || thirdScalar.kind != duplicateScalarInt {
			return duplicateIndexKey{}, false
		}
		return duplicateIndexKey{
			kind:         duplicateIndexStringStringInt,
			templateKey:  template.key,
			firstInt:     int64(thirdScalar.bits),
			stringValue:  firstScalar.stringValue,
			stringValue2: secondScalar.stringValue,
		}, true
	default:
		return duplicateIndexKey{}, false
	}
}

const (
	structuralDuplicateHashOffset = uint64(14695981039346656037)
	structuralDuplicateHashPrime  = uint64(1099511628211)
)

func structuralDuplicateSlotsHash(template Template, slots []factSlot) (uint64, bool) {
	hash := structuralDuplicateHashOffset
	hash = structuralDuplicateHashString(hash, template.key.String())
	return structuralDuplicateSlotsHashWithPrefix(template, slots, hash)
}

func structuralDuplicateSlotsHashWithPrefix(template Template, slots []factSlot, hash uint64) (uint64, bool) {
	for i := range template.fields {
		if i >= len(slots) {
			break
		}
		slot := slots[i]
		if !slot.ok {
			hash = structuralDuplicateHashByte(hash, 0)
			continue
		}
		var ok bool
		hash, ok = structuralDuplicateHashValue(hash, slot.value)
		if !ok {
			return 0, false
		}
	}
	return hash, true
}

func structuralDuplicateSlotsEqual(template Template, left, right []factSlot) bool {
	for i := range template.fields {
		var leftSlot, rightSlot factSlot
		if i < len(left) {
			leftSlot = left[i]
		}
		if i < len(right) {
			rightSlot = right[i]
		}
		if leftSlot.ok != rightSlot.ok {
			return false
		}
		if leftSlot.ok && !leftSlot.value.Equal(rightSlot.value) {
			return false
		}
	}
	return true
}

func structuralDuplicateHashValue(hash uint64, value Value) (uint64, bool) {
	return structuralDuplicateHashScalarValue(hash, value)
}

func structuralDuplicateHashScalarValue(hash uint64, value Value) (uint64, bool) {
	switch value.Kind() {
	case ValueNull:
		return structuralDuplicateHashScalar(hash, duplicateScalarNull, 0, ""), true
	case ValueBool:
		if value.boolValue {
			return structuralDuplicateHashScalar(hash, duplicateScalarBool, 1, ""), true
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarBool, 0, ""), true
	case ValueInt:
		return structuralDuplicateHashScalar(hash, duplicateScalarInt, uint64(value.intValue), ""), true
	case ValueFloat:
		floating := value.floatValue
		if math.IsNaN(floating) {
			return 0, false
		}
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return structuralDuplicateHashScalar(hash, duplicateScalarInt, uint64(int64(floating)), ""), true
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarFloat, math.Float64bits(floating), ""), true
	case ValueString:
		return structuralDuplicateHashScalar(hash, duplicateScalarString, 0, value.stringValue), true
	case ValueList:
		hash = structuralDuplicateHashByte(hash, 6)
		values, ok := value.data.([]Value)
		if !ok {
			return 0, false
		}
		hash = structuralDuplicateHashUint64(hash, uint64(len(values)))
		for _, item := range values {
			var itemOK bool
			hash, itemOK = structuralDuplicateHashValue(hash, item)
			if !itemOK {
				return 0, false
			}
		}
		return hash, true
	default:
		return 0, false
	}
}

func structuralDuplicateHashKnownScalarValue(hash uint64, kind ValueKind, value Value) (uint64, bool) {
	switch kind {
	case ValueNull:
		if value.kind != valueKindUnknown && value.kind != ValueNull {
			return 0, false
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarNull, 0, ""), true
	case ValueBool:
		if value.kind != ValueBool {
			return 0, false
		}
		if value.boolValue {
			return structuralDuplicateHashScalar(hash, duplicateScalarBool, 1, ""), true
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarBool, 0, ""), true
	case ValueInt:
		if value.kind != ValueInt {
			return 0, false
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarInt, uint64(value.intValue), ""), true
	case ValueFloat:
		if value.kind != ValueFloat {
			return 0, false
		}
		floating := value.floatValue
		if math.IsNaN(floating) {
			return 0, false
		}
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return structuralDuplicateHashScalar(hash, duplicateScalarInt, uint64(int64(floating)), ""), true
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarFloat, math.Float64bits(floating), ""), true
	case ValueString:
		if value.kind != ValueString {
			return 0, false
		}
		return structuralDuplicateHashScalar(hash, duplicateScalarString, 0, value.stringValue), true
	default:
		return 0, false
	}
}

func structuralDuplicateScalarValuesEqual(kind ValueKind, left, right Value) (bool, bool) {
	switch kind {
	case ValueNull:
		leftNull := left.kind == valueKindUnknown || left.kind == ValueNull
		rightNull := right.kind == valueKindUnknown || right.kind == ValueNull
		return leftNull && rightNull, true
	case ValueBool:
		if left.kind != ValueBool || right.kind != ValueBool {
			return false, false
		}
		return left.boolValue == right.boolValue, true
	case ValueInt:
		if left.kind != ValueInt || right.kind != ValueInt {
			return false, false
		}
		return left.intValue == right.intValue, true
	case ValueFloat:
		if left.kind != ValueFloat || right.kind != ValueFloat {
			return false, false
		}
		return left.floatValue == right.floatValue, true
	case ValueString:
		if left.kind != ValueString || right.kind != ValueString {
			return false, false
		}
		return left.stringValue == right.stringValue, true
	default:
		return false, false
	}
}

func structuralDuplicateHashScalar(hash uint64, kind duplicateScalarKind, bits uint64, stringValue string) uint64 {
	hash = structuralDuplicateHashByte(hash, 1)
	hash = structuralDuplicateHashByte(hash, byte(kind))
	hash = structuralDuplicateHashUint64(hash, bits)
	return structuralDuplicateHashString(hash, stringValue)
}

func structuralDuplicateHashString(hash uint64, value string) uint64 {
	hash = structuralDuplicateHashUint64(hash, uint64(len(value)))
	for i := range value {
		hash = structuralDuplicateHashByte(hash, value[i])
	}
	return hash
}

func structuralDuplicateHashUint64(hash, value uint64) uint64 {
	return structuralDuplicateHashAvalanche(hash ^ structuralDuplicateHashAvalanche(value+0x9e3779b97f4a7c15))
}

func structuralDuplicateHashAvalanche(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}

func structuralDuplicateHashByte(hash uint64, value byte) uint64 {
	hash ^= uint64(value)
	hash *= structuralDuplicateHashPrime
	return hash
}

func duplicateFields(values Fields, template Template) Fields {
	if template.duplicatePolicy == DuplicateAllow {
		return nil
	}
	if template.duplicatePolicy != DuplicateUniqueKey {
		return values
	}

	out := make(Fields, len(template.duplicateKeyNames))
	for _, fieldName := range template.duplicateKeyNames {
		if value, ok := values[fieldName]; ok {
			out[fieldName] = value
		}
	}
	return out
}

func workingFactStructuralDuplicateSlotsEqual(template Template, slots []factSlot, fact *workingFact, compactSlotStore *factCompactSlotStore) bool {
	if fact == nil {
		return false
	}
	compactSlots := fact.compactFieldSlots(compactSlotStore)
	if len(compactSlots) == 0 {
		return structuralDuplicateSlotsEqual(template, slots, fact.fieldSlotSlice())
	}
	if len(slots) != len(compactSlots) {
		return false
	}
	for i := range slots {
		left := slots[i]
		right := compactSlots[i]
		if left.ok != right.ok {
			return false
		}
		if left.presence != right.presence {
			return false
		}
		if !left.ok {
			continue
		}
		rightValue, ok := right.value()
		if !ok || !left.value.Equal(rightValue) {
			return false
		}
	}
	return true
}

func (f FactSnapshot) FieldPresence(field string) (FieldPresence, bool) {
	if f.fieldPresence != nil {
		presence, ok := f.fieldPresence[field]
		if ok {
			return presence, true
		}
	}
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.fieldSlots) {
		return f.fieldSlots[slot].presence.fieldPresence(), true
	}
	return FieldPresence(""), false
}

func (f FactSnapshot) FieldPresenceMap() map[string]FieldPresence {
	if f.fieldPresence != nil {
		return cloneFieldPresence(f.fieldPresence)
	}
	return materializePresenceFromSlots(f.fieldSlots, f.fieldSpecs)
}

func cloneFieldPresence(in map[string]FieldPresence) map[string]FieldPresence {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]FieldPresence, len(in))
	maps.Copy(out, in)
	return out
}

func (f FactSnapshot) fieldValue(name string) (Value, bool) {
	if f.fields != nil {
		value, ok := f.fields[name]
		if ok {
			return value, true
		}
	}
	if slot, ok := f.fieldSlot(name); ok && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		if resolved.ok {
			return resolved.value, true
		}
	}
	return Value{}, false
}

func (f *workingFact) fieldValue(name string, compactSlotStore *factCompactSlotStore) (Value, bool) {
	if f == nil {
		return Value{}, false
	}
	if fields := f.fieldsMap(); fields != nil {
		value, ok := fields[name]
		if ok {
			return value, true
		}
	}
	fieldSlots := f.fieldSlotSlice()
	if slot, ok := f.fieldSlot(name); ok && slot < len(fieldSlots) {
		resolved := fieldSlots[slot]
		if resolved.ok {
			return resolved.value, true
		}
	}
	compactSlots := f.compactFieldSlots(compactSlotStore)
	if slot, ok := f.fieldSlot(name); ok && slot < len(compactSlots) {
		return compactSlots[slot].value()
	}
	return Value{}, false
}

func (f FactSnapshot) fieldSlot(name string) (int, bool) {
	for i, spec := range f.fieldSpecs {
		if spec.Name == name {
			return i, true
		}
	}
	return -1, false
}

func (f *workingFact) fieldSlot(name string) (int, bool) {
	return -1, false
}

func materializeFieldsFromSlots(slots []factSlot, specs []FieldSpec) Fields {
	if len(slots) == 0 || len(specs) == 0 {
		return nil
	}

	out := make(Fields, len(specs))
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		slot := slots[i]
		if !slot.ok {
			continue
		}
		out[spec.Name] = cloneValue(slot.value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func materializeFieldsFromCompactSlots(slots []compactFactSlot, specs []FieldSpec) Fields {
	if len(slots) == 0 || len(specs) == 0 {
		return nil
	}

	out := make(Fields, len(specs))
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		value, ok := slots[i].value()
		if !ok {
			continue
		}
		out[spec.Name] = cloneValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func materializePresenceFromSlots(slots []factSlot, specs []FieldSpec) map[string]FieldPresence {
	if len(slots) == 0 || len(specs) == 0 {
		return nil
	}

	out := make(map[string]FieldPresence, len(specs))
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		out[spec.Name] = slots[i].presence.fieldPresence()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func materializePresenceFromCompactSlots(slots []compactFactSlot, specs []FieldSpec) map[string]FieldPresence {
	if len(slots) == 0 || len(specs) == 0 {
		return nil
	}

	out := make(map[string]FieldPresence, len(specs))
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		out[spec.Name] = slots[i].presence.fieldPresence()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func materializeFactSlotsFromCompactSlots(slots []compactFactSlot) []factSlot {
	if len(slots) == 0 {
		return nil
	}
	out := make([]factSlot, len(slots))
	for i, slot := range slots {
		value, ok := slot.value()
		out[i] = factSlot{
			value:    value,
			presence: slot.presence,
			ok:       ok,
		}
	}
	return out
}

func cloneFactSlots(in []factSlot) []factSlot {
	if len(in) == 0 {
		return nil
	}
	out := make([]factSlot, len(in))
	for i, slot := range in {
		out[i] = factSlot{
			value:    cloneValue(slot.value),
			presence: slot.presence,
			ok:       slot.ok,
		}
	}
	return out
}

func cloneCompactFactSlots(in []compactFactSlot) []compactFactSlot {
	if len(in) == 0 {
		return nil
	}
	out := make([]compactFactSlot, len(in))
	copy(out, in)
	return out
}

func emitDuplicateKeyFields(b *strings.Builder, template Template, fields Fields, slots []factSlot) {
	if template.duplicatePolicy == DuplicateAllow {
		return
	}

	if template.duplicatePolicy == DuplicateUniqueKey {
		emitDuplicateKeyFieldsByNames(b, template, fields, slots)
		return
	}

	if len(slots) > 0 && len(template.fields) > 0 {
		emitDuplicateKeyFieldsByTemplateOrder(b, template.fields, slots)
		return
	}

	if fields == nil {
		return
	}
	b.WriteString(fields.duplicateKey())
}

func emitDuplicateKeyFieldsByNames(b *strings.Builder, template Template, fields Fields, slots []factSlot) {
	if len(template.duplicateKeyNames) == 0 {
		return
	}

	if len(slots) > 0 && len(template.duplicateKeySlots) == len(template.duplicateKeyNames) {
		for i, slot := range template.duplicateKeySlots {
			if slot < 0 || slot >= len(slots) {
				continue
			}
			resolved := slots[slot]
			if resolved.ok {
				writeDuplicateKeyEntry(b, template.duplicateKeyNames[i], resolved.value)
			}
		}
		return
	}

	for _, fieldName := range template.duplicateKeyNames {
		if value, ok := duplicateFieldValue(fieldName, template, fields, slots); ok {
			writeDuplicateKeyEntry(b, fieldName, value)
		}
	}
}

func emitDuplicateKeyFieldsByTemplateOrder(b *strings.Builder, specs []FieldSpec, slots []factSlot) {
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		slot := slots[i]
		if !slot.ok {
			continue
		}
		writeDuplicateKeyEntry(b, spec.Name, slot.value)
	}
}

func duplicateFieldValue(fieldName string, template Template, fields Fields, slots []factSlot) (Value, bool) {
	if len(slots) > 0 {
		if slot, ok := template.fieldSlot(fieldName); ok && slot >= 0 && slot < len(slots) {
			resolved := slots[slot]
			if resolved.ok {
				return resolved.value, true
			}
			return Value{}, false
		}
	}
	value, ok := fields[fieldName]
	return value, ok
}

func writeDuplicateKeyEntry(b *strings.Builder, fieldName string, value Value) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	encodeValueForDuplicateKey(b, value)
	b.WriteByte(';')
}

func duplicateKeyCapacity(name string, template Template, fields Fields, slots []factSlot) int {
	size := len("name:") + len(name) + len("|template:") + len(template.key) + len("|fields:")
	if template.duplicatePolicy == DuplicateAllow {
		return size
	}
	if len(slots) > 0 {
		if template.duplicatePolicy == DuplicateUniqueKey {
			if len(template.duplicateKeySlots) == len(template.duplicateKeyNames) {
				for i, slot := range template.duplicateKeySlots {
					if slot < 0 || slot >= len(slots) {
						continue
					}
					resolved := slots[slot]
					if resolved.ok {
						size += len(template.duplicateKeyNames[i]) + 2 + duplicateKeyValueCapacity(resolved.value)
					}
				}
				return size
			}
		} else if len(template.fields) > 0 {
			for i, spec := range template.fields {
				if i >= len(slots) {
					break
				}
				if !slots[i].ok {
					continue
				}
				size += len(spec.Name) + 2 + duplicateKeyValueCapacity(slots[i].value)
			}
			return size
		}
	}
	if template.duplicatePolicy == DuplicateUniqueKey {
		for _, fieldName := range template.duplicateKeyNames {
			if value, ok := fields[fieldName]; ok {
				size += len(fieldName) + 2 + duplicateKeyValueCapacity(value)
			}
		}
		return size
	}
	if fields == nil {
		return size
	}
	for key, value := range fields {
		size += len(key) + 2 + duplicateKeyValueCapacity(value)
	}
	return size
}
