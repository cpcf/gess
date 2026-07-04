package engine

import (
	"context"
	"time"
)

type EventType string

const (
	EventFactAsserted          EventType = "fact_asserted"
	EventFactModified          EventType = "fact_modified"
	EventFactRetracted         EventType = "fact_retracted"
	EventReset                 EventType = "reset"
	EventRuleActivated         EventType = "rule_activated"
	EventRuleDeactivated       EventType = "rule_deactivated"
	EventRuleFired             EventType = "rule_fired"
	EventActionFailed          EventType = "action_failed"
	EventLogicalSupportAdded   EventType = "logical_support_added"
	EventLogicalSupportRemoved EventType = "logical_support_removed"
)

type EventSeverity string

const (
	EventSeverityInfo  EventSeverity = "info"
	EventSeverityError EventSeverity = "error"
)

type Event struct {
	SessionID SessionID
	RulesetID RulesetID
	RunID     RunID
	// Sequence is a session-lifetime counter over generated events. It
	// advances for every generated event whether or not any listener is
	// subscribed to its type, so listeners registered with ForEventTypes
	// observe gaps but never renumbering. Rule activation/deactivation
	// events are only generated when a listener subscribes to them, so
	// numbering is comparable between sessions only when their listener
	// configurations match.
	Sequence       uint64
	Timestamp      time.Time
	Type           EventType
	Severity       EventSeverity
	Generation     Generation
	Recency        Recency
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	ActivationID   ActivationID
	Source         SourceSpan
	ActionName     string
	ActionIndex    int
	Cause          error
	FactIDs        []FactID
	Delta          *MutationDelta
	SupportEdge    *LogicalSupportEdge
}

func cloneMutationDelta(delta *MutationDelta) *MutationDelta {
	if delta == nil {
		return nil
	}

	clone := *delta
	if delta.Before != nil {
		before := delta.Before.clone()
		clone.Before = &before
	}
	if delta.After != nil {
		after := delta.After.clone()
		clone.After = &after
	}
	clone.ChangedFields = make([]FieldChange, len(delta.ChangedFields))
	for i, field := range delta.ChangedFields {
		clone.ChangedFields[i] = FieldChange{
			Field: field.Field,
			Old:   cloneValue(field.Old),
			New:   cloneValue(field.New),
		}
	}
	return &clone
}

func (e Event) clone() Event {
	out := e
	out.FactIDs = e.RelatedFactIDs()
	out.Delta = cloneMutationDelta(e.Delta)
	if e.SupportEdge != nil {
		edge := e.SupportEdge.clone()
		out.SupportEdge = &edge
	}
	return out
}

func (e Event) RelatedFactIDs() []FactID {
	if e.FactIDs == nil {
		return nil
	}
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

// EventListenerOption configures a listener registered with WithEventListener.
type EventListenerOption func(*eventListenerConfig)

type eventListenerConfig struct {
	types map[EventType]struct{}
}

type eventListenerRegistration struct {
	listener EventListener
	types    map[EventType]struct{}
}

// ForEventTypes limits a listener to the supplied event types. With no filter,
// listeners receive every event type.
// ForEventTypes restricts a listener registration to the given event types.
// Events of other types are neither constructed nor delivered for that
// listener; repeated ForEventTypes options replace earlier ones (last wins),
// and an empty type list subscribes the listener to nothing. Sequence numbers
// are global to the session, so a filtered listener observes gaps.
func ForEventTypes(types ...EventType) EventListenerOption {
	return func(cfg *eventListenerConfig) {
		cfg.types = make(map[EventType]struct{}, len(types))
		for _, eventType := range types {
			cfg.types[eventType] = struct{}{}
		}
	}
}

func newEventListenerRegistration(listener EventListener, opts []EventListenerOption) eventListenerRegistration {
	if listener == nil {
		return eventListenerRegistration{}
	}
	var cfg eventListenerConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return eventListenerRegistration{
		listener: listener,
		types:    cloneEventTypeSet(cfg.types),
	}
}

func cloneEventListenerRegistrations(in []eventListenerRegistration) []eventListenerRegistration {
	if len(in) == 0 {
		return nil
	}
	out := make([]eventListenerRegistration, len(in))
	for i, registration := range in {
		out[i] = eventListenerRegistration{
			listener: registration.listener,
			types:    cloneEventTypeSet(registration.types),
		}
	}
	return out
}

func cloneEventTypeSet(in map[EventType]struct{}) map[EventType]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[EventType]struct{}, len(in))
	for eventType := range in {
		out[eventType] = struct{}{}
	}
	return out
}

func (r eventListenerRegistration) subscribesTo(eventType EventType) bool {
	if r.listener == nil {
		return false
	}
	if r.types == nil {
		return true
	}
	_, ok := r.types[eventType]
	return ok
}

func eventListenerRegistrationsHaveAnySubscriptions(registrations []eventListenerRegistration) bool {
	for _, registration := range registrations {
		if registration.listener == nil {
			continue
		}
		if registration.types == nil || len(registration.types) > 0 {
			return true
		}
	}
	return false
}

func countEventListenerSubscriptions(registrations []eventListenerRegistration) (int, map[EventType]int) {
	all := 0
	counts := make(map[EventType]int)
	for _, registration := range registrations {
		if registration.listener == nil {
			continue
		}
		if registration.types == nil {
			all++
			continue
		}
		for eventType := range registration.types {
			counts[eventType]++
		}
	}
	if len(counts) == 0 {
		counts = nil
	}
	return all, counts
}
