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
	SessionID      SessionID
	RulesetID      RulesetID
	RunID          RunID
	Sequence       uint64
	Timestamp      time.Time
	Type           EventType
	Severity       EventSeverity
	Generation     Generation
	Recency        Recency
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	ActivationID   ActivationID
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
