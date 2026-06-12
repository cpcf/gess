package gess

import (
	"context"
	"sort"
	"time"
)

type SessionOption func(*sessionConfig)

type sessionConfig struct {
	id         SessionID
	listeners  []EventListener
	initials   []SessionInitialFact
	eventClock func() time.Time
}

type SessionInitialFact struct {
	Name        string
	TemplateKey TemplateKey
	Fields      Fields
}

func WithSessionID(id SessionID) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.id = id
	}
}

func WithEventListener(listener EventListener) SessionOption {
	return func(cfg *sessionConfig) {
		if listener != nil {
			cfg.listeners = append(cfg.listeners, listener)
		}
	}
}

// WithEventClock sets the clock used to timestamp emitted events.
func WithEventClock(clock func() time.Time) SessionOption {
	return func(cfg *sessionConfig) {
		if clock != nil {
			cfg.eventClock = clock
		}
	}
}

func WithInitialFacts(initials ...SessionInitialFact) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.initials = append(cfg.initials, cloneSessionInitialFacts(initials)...)
	}
}

type Session struct {
	id         SessionID
	revision   *Ruleset
	generation Generation
	initials   []SessionInitialFact
	listeners  []EventListener
	eventClock func() time.Time
	closed     bool
	mu         struct {
		mutate chan struct{}
		lock   chan struct{}
	}

	nextFactSequence  uint64
	nextRecency       Recency
	factsByID         map[FactID]*workingFact
	factsByDuplicate  map[DuplicateKey]FactID
	factsByTemplate   map[TemplateKey][]FactID
	factsByName       map[string][]FactID
	insertionOrder    []FactID
	nextEventSequence uint64
}

func NewSession(revision *Ruleset, opts ...SessionOption) (*Session, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}

	cfg := sessionConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.eventClock == nil {
		cfg.eventClock = time.Now
	}

	listeners := make([]EventListener, len(cfg.listeners))
	copy(listeners, cfg.listeners)
	initials := cloneSessionInitialFacts(cfg.initials)

	state := newFactWorkspace(1)
	state.factsByID = make(map[FactID]*workingFact)
	state.factsByTemplate = make(map[TemplateKey][]FactID)
	state.factsByName = make(map[string][]FactID)
	state.factsByDuplicate = make(map[DuplicateKey]FactID)

	if len(initials) > 0 {
		if err := state.applyInitialFacts(revision, initials); err != nil {
			return nil, err
		}
	}

	return &Session{
		id:         cfg.id,
		revision:   revision,
		generation: 1,
		initials:   initials,
		listeners:  listeners,
		eventClock: cfg.eventClock,
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		factsByID:        state.factsByID,
		factsByDuplicate: state.factsByDuplicate,
		factsByTemplate:  state.factsByTemplate,
		factsByName:      state.factsByName,
		nextFactSequence: state.nextFactSequence(),
		nextRecency:      state.nextRecency(),
		insertionOrder:   state.factsByInsertionOrder(),
	}, nil
}

func cloneSessionInitialFacts(initials []SessionInitialFact) []SessionInitialFact {
	if len(initials) == 0 {
		return nil
	}

	out := make([]SessionInitialFact, len(initials))
	for i, initial := range initials {
		out[i] = SessionInitialFact{
			Name:        initial.Name,
			TemplateKey: initial.TemplateKey,
			Fields:      cloneFields(initial.Fields),
		}
	}
	return out
}

func (s *Session) ID() SessionID {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *Session) RulesetID() RulesetID {
	if s == nil || s.revision == nil {
		return ""
	}
	return s.revision.ID()
}

func (s *Session) Generation() Generation {
	if s == nil {
		return 0
	}
	return s.generation
}

func (s *Session) Snapshot(ctx context.Context) (Snapshot, error) {
	if s == nil || s.closed {
		return Snapshot{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if !s.lock() {
		return Snapshot{}, ErrConcurrencyMisuse
	}
	defer s.unlock()
	return s.snapshotLocked(), nil
}

func (s *Session) Close() error {
	if s == nil {
		return ErrClosedSession
	}
	s.closed = true
	return nil
}

func (s *Session) Assert(ctx context.Context, name string, fields Fields) (AssertResult, error) {
	return s.insertFactWithContext(ctx, name, "", fields)
}

func (s *Session) AssertTemplate(ctx context.Context, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	return s.insertFactWithContext(ctx, "", templateKey, fields)
}

func (s *Session) insertFact(name string, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	return s.insertFactWithContext(context.Background(), name, templateKey, fields)
}

func (s *Session) insertFactWithContext(ctx context.Context, name string, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if s == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AssertResult{Status: AssertValidationFailure}, err
	}
	if !s.beginMutation() {
		return AssertResult{Status: AssertConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	defer s.endMutation()

	if s == nil || s.closed {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}

	state := factWorkspace{
		sequence:         &s.nextFactSequence,
		recency:          &s.nextRecency,
		insertionOrder:   &s.insertionOrder,
		factsByID:        s.factsByID,
		factsByDuplicate: s.factsByDuplicate,
		factsByTemplate:  s.factsByTemplate,
		factsByName:      s.factsByName,
	}
	fact, duplicateKey, inserted, err := state.insertFact(s.revision, s.generation, name, templateKey, fields)
	if err != nil {
		return AssertResult{Status: AssertValidationFailure}, err
	}
	if !inserted {
		return AssertResult{
			Status:       AssertExisting,
			Fact:         fact.snapshot(),
			DuplicateKey: duplicateKey,
		}, nil
	}

	snapshot := fact.snapshot()
	delta := MutationDelta{
		Kind:         MutationAssert,
		Generation:   s.generation,
		Recency:      fact.recency,
		FactID:       fact.id,
		NewVersion:   fact.version,
		NewDuplicate: duplicateKey,
		After:        &snapshot,
	}

	result := AssertResult{
		Status:       AssertInserted,
		Fact:         snapshot,
		DuplicateKey: duplicateKey,
		Delta:        &delta,
	}
	s.emitEvent(ctx, Event{
		SessionID:  s.id,
		RulesetID:  s.revision.ID(),
		Sequence:   s.nextEventSequence + 1,
		Timestamp:  s.eventClock(),
		Type:       EventFactAsserted,
		Generation: s.generation,
		Recency:    fact.recency,
		FactIDs:    []FactID{fact.id},
		Delta:      &delta,
	})
	s.nextEventSequence++

	return result, nil
}

func (s *Session) Retract(ctx context.Context, id FactID) (RetractResult, error) {
	if s == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RetractResult{}, err
	}
	if !s.beginMutation() {
		return RetractResult{Status: RetractConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	defer s.endMutation()

	if s.closed {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}

	if id.Generation() != s.generation {
		if id.Generation() != 0 && id.Generation() < s.generation {
			return RetractResult{Status: RetractStale}, ErrStaleFactID
		}
		return RetractResult{Status: RetractMissing}, ErrFactNotFound
	}

	fact, ok := s.factsByID[id]
	if !ok {
		return RetractResult{Status: RetractMissing}, ErrFactNotFound
	}

	before := fact.snapshot()
	oldVersion := fact.version
	oldDuplicate := fact.dupKey

	delete(s.factsByID, id)
	if oldDuplicate != "" {
		if existingID, ok := s.factsByDuplicate[oldDuplicate]; ok && existingID == id {
			delete(s.factsByDuplicate, oldDuplicate)
		}
	}
	s.factsByTemplate[fact.templateKey] = removeFactIDFromSlice(s.factsByTemplate[fact.templateKey], id)
	if len(s.factsByTemplate[fact.templateKey]) == 0 {
		delete(s.factsByTemplate, fact.templateKey)
	}
	s.factsByName[fact.name] = removeFactIDFromSlice(s.factsByName[fact.name], id)
	if len(s.factsByName[fact.name]) == 0 {
		delete(s.factsByName, fact.name)
	}
	s.insertionOrder = removeFactIDFromSlice(s.insertionOrder, id)

	delta := MutationDelta{
		Kind:         MutationRetract,
		Generation:   s.generation,
		Recency:      fact.recency,
		FactID:       fact.id,
		OldVersion:   oldVersion,
		OldDuplicate: oldDuplicate,
		Before:       &before,
	}

	result := RetractResult{
		Status: RetractRemoved,
		Fact:   before,
		Delta:  &delta,
	}
	s.emitEvent(ctx, Event{
		SessionID:  s.id,
		RulesetID:  s.revision.ID(),
		Sequence:   s.nextEventSequence + 1,
		Timestamp:  s.eventClock(),
		Type:       EventFactRetracted,
		Generation: s.generation,
		Recency:    fact.recency,
		FactIDs:    []FactID{fact.id},
		Delta:      &delta,
	})
	s.nextEventSequence++

	return result, nil
}

func (s *Session) Reset(ctx context.Context) (ResetResult, error) {
	if s == nil {
		return ResetResult{Status: ResetClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ResetResult{}, err
	}
	if !s.beginMutation() {
		return ResetResult{Status: ResetConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	defer s.endMutation()

	if s.closed {
		return ResetResult{Status: ResetClosed}, ErrClosedSession
	}

	before := s.snapshotLocked()
	next := newFactWorkspace(s.generation + 1)
	if err := next.applyInitialFacts(s.revision, s.initials); err != nil {
		return ResetResult{Status: ResetValidationFailure, Before: before}, err
	}

	oldGeneration := s.generation
	s.generation = s.generation + 1
	s.nextFactSequence = next.nextFactSequence()
	s.nextRecency = next.nextRecency()
	s.factsByID = next.factsByID
	s.factsByDuplicate = next.factsByDuplicate
	s.factsByTemplate = next.factsByTemplate
	s.factsByName = next.factsByName
	s.insertionOrder = next.factsByInsertionOrder()

	delta := MutationDelta{
		Kind:          MutationReset,
		Generation:    s.generation,
		OldGeneration: oldGeneration,
	}
	result := ResetResult{
		Status:     ResetApplied,
		Generation: s.generation,
		Before:     before,
		Delta:      delta,
	}
	s.emitEvent(ctx, Event{
		SessionID:  s.id,
		RulesetID:  s.revision.ID(),
		Sequence:   s.nextEventSequence + 1,
		Timestamp:  s.eventClock(),
		Type:       EventReset,
		Generation: s.generation,
		FactIDs:    nil,
		Delta:      &delta,
	})
	s.nextEventSequence++

	return result, nil
}

func (s *Session) Modify(ctx context.Context, id FactID, patch FactPatch) (ModifyResult, error) {
	if s == nil {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ModifyResult{}, err
	}
	if !s.beginMutation() {
		return ModifyResult{Status: ModifyConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	defer s.endMutation()

	if s.closed {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}

	if id.Generation() != s.generation {
		if id.Generation() != 0 && id.Generation() < s.generation {
			return ModifyResult{Status: ModifyStale}, ErrStaleFactID
		}
		return ModifyResult{Status: ModifyMissing}, ErrFactNotFound
	}

	fact, ok := s.factsByID[id]
	if !ok {
		return ModifyResult{Status: ModifyMissing}, ErrFactNotFound
	}

	before := fact.snapshot()
	template, templateExists := s.revision.TemplateByKey(fact.templateKey)

	proposedFields, proposedPresence := applyPatchToFact(fact, patch)
	var err error
	if templateExists {
		proposedFields, proposedPresence, err = template.applyDefaultsAndValidate(proposedFields)
		if err != nil {
			return ModifyResult{Status: ModifyValidationFailure, Fact: before}, err
		}
	}

	newDuplicate := makeDuplicateKeyForTemplate(fact.name, template, proposedFields)
	duplicatePolicy := template.duplicatePolicy
	if templateExists && duplicatePolicy == DuplicateAllow {
		newDuplicate = ""
	}
	oldDuplicate := fact.dupKey

	if duplicatePolicy != DuplicateAllow {
		if existingID, ok := s.factsByDuplicate[newDuplicate]; ok && existingID != fact.id {
			return ModifyResult{Status: ModifyDuplicate, Fact: before}, ErrDuplicateFact
		}
	}

	if fieldsAndPresenceEqual(before.fields, before.fieldPresence, proposedFields, proposedPresence) {
		return ModifyResult{Status: ModifyNoOp, Fact: before}, nil
	}

	s.nextRecency++

	if duplicatePolicy != DuplicateAllow && oldDuplicate != newDuplicate {
		delete(s.factsByDuplicate, oldDuplicate)
		s.factsByDuplicate[newDuplicate] = fact.id
	}

	oldVersion := fact.version
	fact.version++
	fact.recency = s.nextRecency
	fact.fields = proposedFields
	fact.fieldPresence = proposedPresence
	fact.dupKey = newDuplicate

	after := fact.snapshot()
	delta := MutationDelta{
		Kind:          MutationModify,
		Generation:    s.generation,
		Recency:       fact.recency,
		FactID:        fact.id,
		OldVersion:    oldVersion,
		NewVersion:    fact.version,
		Before:        &before,
		After:         &after,
		OldDuplicate:  oldDuplicate,
		NewDuplicate:  newDuplicate,
		ChangedFields: changedFields(before.fields, before.fieldPresence, proposedFields, proposedPresence),
	}
	result := ModifyResult{
		Status: ModifyChanged,
		Fact:   after,
		Delta:  &delta,
	}
	s.emitEvent(ctx, Event{
		SessionID:  s.id,
		RulesetID:  s.revision.ID(),
		Sequence:   s.nextEventSequence + 1,
		Timestamp:  s.eventClock(),
		Type:       EventFactModified,
		Generation: s.generation,
		Recency:    fact.recency,
		FactIDs:    []FactID{fact.id},
		Delta:      &delta,
	})
	s.nextEventSequence++

	return result, nil
}

func applyPatchToFact(fact *workingFact, patch FactPatch) (Fields, map[string]FieldPresence) {
	nextFields := cloneFields(fact.fields)
	nextPresence := cloneFieldPresence(fact.fieldPresence)

	for _, field := range patch.Unset {
		delete(nextFields, field)
		delete(nextPresence, field)
	}

	for field, value := range patch.Set {
		nextFields = setField(nextFields, field, value)
		if nextPresence == nil {
			nextPresence = make(map[string]FieldPresence)
		}
		nextPresence[field] = FieldPresenceExplicit
	}

	return nextFields, nextPresence
}

func setField(fields Fields, field string, value Value) Fields {
	if fields == nil {
		fields = make(Fields)
	}
	fields[field] = cloneValue(value)
	return fields
}

func fieldsAndPresenceEqual(leftFields Fields, leftPresence map[string]FieldPresence, rightFields Fields, rightPresence map[string]FieldPresence) bool {
	if len(leftFields) != len(rightFields) {
		return false
	}
	for key, left := range leftFields {
		right, ok := rightFields[key]
		if !ok || !left.Equal(right) {
			return false
		}
	}
	for key, right := range rightFields {
		left, ok := leftFields[key]
		if !ok || !left.Equal(right) {
			return false
		}
	}

	if len(leftPresence) != len(rightPresence) {
		return false
	}
	for key, left := range leftPresence {
		right, ok := rightPresence[key]
		if !ok || left != right {
			return false
		}
	}
	for key, right := range rightPresence {
		left, ok := leftPresence[key]
		if !ok || left != right {
			return false
		}
	}
	return true
}

func changedFields(beforeFields Fields, beforePresence map[string]FieldPresence, afterFields Fields, afterPresence map[string]FieldPresence) []FieldChange {
	keys := make(map[string]struct{}, len(beforeFields)+len(afterFields)+len(beforePresence)+len(afterPresence))
	for key := range beforeFields {
		keys[key] = struct{}{}
	}
	for key := range afterFields {
		keys[key] = struct{}{}
	}
	for key := range beforePresence {
		keys[key] = struct{}{}
	}
	for key := range afterPresence {
		keys[key] = struct{}{}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	changes := make([]FieldChange, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		beforePresenceType, beforeHasPresence := beforePresence[key]
		beforeValue, beforeHasValue := beforeFields[key]
		afterPresenceType, afterHasPresence := afterPresence[key]
		afterValue, afterHasValue := afterFields[key]

		beforeEquivalent := beforeHasPresence == afterHasPresence && beforePresenceType == afterPresenceType
		beforeEquivalent = beforeEquivalent && beforeHasValue == afterHasValue
		if beforeEquivalent && beforeHasValue {
			beforeEquivalent = beforeValue.Equal(afterValue)
		}
		if beforeEquivalent {
			continue
		}

		if !beforeHasValue {
			beforeValue = Value{}
		}
		if !afterHasValue {
			afterValue = Value{}
		}

		changes = append(changes, FieldChange{
			Field: key,
			Old:   cloneValue(beforeValue),
			New:   cloneValue(afterValue),
		})
	}

	return changes
}

func removeFactIDFromSlice(ids []FactID, target FactID) []FactID {
	for i, id := range ids {
		if id != target {
			continue
		}
		copy(ids[i:], ids[i+1:])
		ids = ids[:len(ids)-1]
		break
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func (s *Session) snapshotLocked() Snapshot {
	facts := make([]FactSnapshot, 0, len(s.insertionOrder))
	for _, id := range s.insertionOrder {
		fact, ok := s.factsByID[id]
		if !ok {
			continue
		}
		facts = append(facts, fact.snapshot())
	}

	return newSnapshot(s.id, s.revision.ID(), s.generation, facts)
}

type factWorkspace struct {
	generation       Generation
	sequence         *uint64
	recency          *Recency
	insertionOrder   *[]FactID
	factsByID        map[FactID]*workingFact
	factsByDuplicate map[DuplicateKey]FactID
	factsByTemplate  map[TemplateKey][]FactID
	factsByName      map[string][]FactID
}

func newFactWorkspace(generation Generation) *factWorkspace {
	var sequence uint64
	var recency Recency
	insertionOrder := make([]FactID, 0)
	return &factWorkspace{
		generation:       generation,
		sequence:         &sequence,
		recency:          &recency,
		insertionOrder:   &insertionOrder,
		factsByID:        make(map[FactID]*workingFact),
		factsByDuplicate: make(map[DuplicateKey]FactID),
		factsByTemplate:  make(map[TemplateKey][]FactID),
		factsByName:      make(map[string][]FactID),
	}
}

func (w *factWorkspace) nextFactSequence() uint64 {
	if w == nil || w.sequence == nil {
		return 0
	}
	return *w.sequence
}

func (w *factWorkspace) nextRecency() Recency {
	if w == nil || w.recency == nil {
		return 0
	}
	return *w.recency
}

func (w *factWorkspace) factsByInsertionOrder() []FactID {
	if w == nil || w.insertionOrder == nil {
		return nil
	}
	return *w.insertionOrder
}

func (w *factWorkspace) insertFact(revision *Ruleset, generation Generation, name string, templateKey TemplateKey, fields Fields) (*workingFact, DuplicateKey, bool, error) {
	canonical := normalizeFields(fields)
	template, templateExists := revision.TemplateByKey(templateKey)
	if templateKey != "" && !templateExists {
		return nil, "", false, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}

	if templateExists {
		name = template.Name()
	}

	var presence map[string]FieldPresence
	var err error
	if templateExists {
		canonical, presence, err = template.applyDefaultsAndValidate(canonical)
		if err != nil {
			return nil, "", false, err
		}
	} else {
		presence = make(map[string]FieldPresence, len(canonical))
		for field := range canonical {
			presence[field] = FieldPresenceExplicit
		}
	}

	templateDuplicatePolicy := template.duplicatePolicy
	duplicateKey := makeDuplicateKeyForTemplate(name, template, canonical)
	if templateExists && templateDuplicatePolicy == DuplicateAllow {
		duplicateKey = ""
	}

	if templateDuplicatePolicy != DuplicateAllow {
		existingID, ok := w.factsByDuplicate[duplicateKey]
		if ok {
			existing, ok := w.factsByID[existingID]
			if ok {
				return existing, duplicateKey, false, nil
			}
			delete(w.factsByDuplicate, duplicateKey)
		}
	}

	*w.sequence++
	*w.recency++
	id := newFactID(generation, *w.sequence)
	fact := &workingFact{
		id:            id,
		name:          name,
		templateKey:   templateKey,
		version:       1,
		recency:       *w.recency,
		generation:    generation,
		fields:        canonical,
		fieldPresence: presence,
		dupKey:        duplicateKey,
	}

	w.factsByID[id] = fact
	if templateDuplicatePolicy != DuplicateAllow {
		w.factsByDuplicate[duplicateKey] = id
	}
	w.factsByTemplate[templateKey] = append(w.factsByTemplate[templateKey], id)
	w.factsByName[name] = append(w.factsByName[name], id)
	*w.insertionOrder = append(*w.insertionOrder, id)

	return fact, duplicateKey, true, nil
}

func (w *factWorkspace) applyInitialFacts(revision *Ruleset, initials []SessionInitialFact) error {
	for _, initial := range initials {
		if initial.TemplateKey == "" && initial.Name == "" {
			return &ValidationError{TemplateName: "session", Reason: "initializer must set name or template key"}
		}
		if initial.TemplateKey != "" && initial.Name != "" {
			return &ValidationError{TemplateName: initial.Name, Reason: "initializer must not set both name and template key"}
		}
		_, _, _, err := w.insertFact(revision, w.generation, initial.Name, initial.TemplateKey, initial.Fields)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) beginMutation() bool {
	if s == nil {
		return false
	}
	select {
	case s.mu.mutate <- struct{}{}:
		select {
		case s.mu.lock <- struct{}{}:
			return true
		default:
			<-s.mu.mutate
			return false
		}
	default:
		return false
	}
}

func (s *Session) endMutation() {
	if s == nil {
		return
	}
	select {
	case <-s.mu.lock:
	default:
	}
	select {
	case <-s.mu.mutate:
	default:
	}
}

func (s *Session) lock() bool {
	select {
	case s.mu.lock <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Session) unlock() {
	if s == nil {
		return
	}
	select {
	case <-s.mu.lock:
	default:
	}
}

func (s *Session) emitEvent(ctx context.Context, event Event) {
	if s == nil || len(s.listeners) == 0 {
		return
	}
	for _, listener := range s.listeners {
		if listener == nil {
			continue
		}
		_ = listener.HandleEvent(ctx, event.clone())
	}
}

func (s *Session) factByID(id FactID) (FactSnapshot, bool) {
	if s == nil {
		return FactSnapshot{}, false
	}
	if id.Generation() != s.generation {
		return FactSnapshot{}, false
	}
	fact, ok := s.factsByID[id]
	if !ok {
		return FactSnapshot{}, false
	}
	return fact.snapshot(), true
}

func (s *Session) factIDsByName(name string) []FactID {
	ids := s.factsByName[name]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDsByTemplate(templateKey TemplateKey) []FactID {
	ids := s.factsByTemplate[templateKey]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDForDuplicateKey(key DuplicateKey) (FactID, bool) {
	factID, ok := s.factsByDuplicate[key]
	return factID, ok
}

func (s *Session) resetWorkingMemory() {
	s.generation++
	s.nextFactSequence = 0
	s.nextRecency = 0
	s.factsByID = make(map[FactID]*workingFact)
	s.factsByDuplicate = make(map[DuplicateKey]FactID)
	s.factsByTemplate = make(map[TemplateKey][]FactID)
	s.factsByName = make(map[string][]FactID)
	s.insertionOrder = nil
}
