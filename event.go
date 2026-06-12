package gess

import (
	"context"
	"time"
)

type EventType string

const (
	EventFactAsserted  EventType = "fact_asserted"
	EventFactModified  EventType = "fact_modified"
	EventFactRetracted EventType = "fact_retracted"
	EventReset         EventType = "reset"
)

type Event struct {
	SessionID  SessionID
	RulesetID  RulesetID
	Sequence   uint64
	Timestamp  time.Time
	Type       EventType
	Generation Generation
	Recency    Recency
	FactIDs    []FactID
	Delta      *MutationDelta
}

func (e Event) RelatedFactIDs() []FactID {
	out := make([]FactID, len(e.FactIDs))
	copy(out, e.FactIDs)
	return out
}

type EventListener interface {
	HandleEvent(context.Context, Event) error
}

type EventFunc func(context.Context, Event) error

func (f EventFunc) HandleEvent(ctx context.Context, event Event) error {
	return f(ctx, event)
}
