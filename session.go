package gess

import "context"

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

	nextFactSequence uint64
	nextRecency      Recency
	factsByID        map[FactID]*workingFact
	factsByDuplicate map[DuplicateKey]FactID
	factsByTemplate  map[TemplateKey][]FactID
	factsByName      map[string][]FactID
	insertionOrder   []FactID
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
		id:               cfg.id,
		revision:         revision,
		generation:       1,
		listeners:        listeners,
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

func (s *Session) insertFact(name string, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if s == nil || s.closed {
		if s == nil {
			return AssertResult{}, ErrClosedSession
		}
		return AssertResult{}, ErrClosedSession
	}

	canonical := normalizeFields(fields)
	template, templateExists := s.revision.TemplateByKey(templateKey)
	if templateKey != "" && !templateExists {
		return AssertResult{}, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}

	var presence map[string]FieldPresence
	var err error
	if templateExists {
		canonical, presence, err = template.applyDefaultsAndValidate(canonical)
		if err != nil {
			return AssertResult{}, err
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
				return AssertResult{Status: AssertExisting, Fact: fact.snapshot()}, nil
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
		Kind:       MutationAssert,
		Generation: s.generation,
		Recency:    fact.recency,
		FactID:     fact.id,
		NewVersion: fact.version,
		After:      &snapshot,
	}

	return AssertResult{Status: AssertInserted, Fact: snapshot, Delta: &delta}, nil
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
