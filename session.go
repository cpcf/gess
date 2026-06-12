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

	return newSnapshot(s.id, s.revision.ID(), s.generation, nil), nil
}

func (s *Session) Close() error {
	if s == nil {
		return ErrClosedSession
	}
	s.closed = true
	return nil
}
