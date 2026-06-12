package gess

import (
	"context"
	"time"
)

type SessionOption func(*sessionConfig)

type sessionConfig struct {
	id        SessionID
	listeners []EventListener
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

type Session struct {
	id         SessionID
	revision   *Ruleset
	generation Generation
	listeners  []EventListener
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

	listeners := make([]EventListener, len(cfg.listeners))
	copy(listeners, cfg.listeners)

	return &Session{
		id:         cfg.id,
		revision:   revision,
		generation: 1,
		listeners:  listeners,
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		factsByID:        make(map[FactID]*workingFact),
		factsByDuplicate: make(map[DuplicateKey]FactID),
		factsByTemplate:  make(map[TemplateKey][]FactID),
		factsByName:      make(map[string][]FactID),
	}, nil
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

	facts := make([]FactSnapshot, 0, len(s.insertionOrder))
	for _, id := range s.insertionOrder {
		fact, ok := s.factsByID[id]
		if !ok {
			continue
		}
		facts = append(facts, fact.snapshot())
	}

	return newSnapshot(s.id, s.revision.ID(), s.generation, facts), nil
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

	canonical := normalizeFields(fields)
	template, templateExists := s.revision.TemplateByKey(templateKey)
	if templateKey != "" && !templateExists {
		result := AssertResult{Status: AssertValidationFailure}
		if name != "" {
			result.Fact = FactSnapshot{name: name}
		}
		return result, &ValidationError{
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
			return AssertResult{Status: AssertValidationFailure}, err
		}
	} else {
		presence = make(map[string]FieldPresence, len(canonical))
		for field := range canonical {
			presence[field] = FieldPresenceExplicit
		}
	}

	duplicateKey := makeDuplicateKeyForTemplate(name, template, canonical)
	duplicatePolicy := template.duplicatePolicy
	if templateExists && duplicatePolicy == DuplicateAllow {
		duplicateKey = ""
	}

	if duplicatePolicy != DuplicateAllow {
		if existingID, ok := s.factsByDuplicate[duplicateKey]; ok {
			fact, ok := s.factsByID[existingID]
			if ok {
				factSnapshot := fact.snapshot()
				return AssertResult{
					Status:       AssertExisting,
					Fact:         factSnapshot,
					DuplicateKey: duplicateKey,
				}, nil
			}
		}
	}

	s.nextFactSequence++
	s.nextRecency++
	id := newFactID(s.generation, s.nextFactSequence)
	fact := &workingFact{
		id:            id,
		name:          name,
		templateKey:   templateKey,
		version:       1,
		recency:       s.nextRecency,
		generation:    s.generation,
		fields:        canonical,
		fieldPresence: presence,
		dupKey:        duplicateKey,
	}

	s.factsByID[id] = fact
	if duplicatePolicy != DuplicateAllow {
		s.factsByDuplicate[duplicateKey] = id
	}
	s.factsByTemplate[templateKey] = append(s.factsByTemplate[templateKey], id)
	s.factsByName[name] = append(s.factsByName[name], id)
	s.insertionOrder = append(s.insertionOrder, id)

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
		Timestamp:  time.Now(),
		Type:       EventFactAsserted,
		Generation: s.generation,
		Recency:    fact.recency,
		FactIDs:    []FactID{fact.id},
		Delta:      &delta,
	})
	s.nextEventSequence++

	return result, nil
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
		_ = listener.HandleEvent(ctx, event)
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
	s.nextEventSequence = 0
	s.factsByID = make(map[FactID]*workingFact)
	s.factsByDuplicate = make(map[DuplicateKey]FactID)
	s.factsByTemplate = make(map[TemplateKey][]FactID)
	s.factsByName = make(map[string][]FactID)
	s.insertionOrder = nil
}
