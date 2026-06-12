package gess

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testEventCollector struct {
	mu      sync.Mutex
	events  []Event
	waitCh  chan struct{}
	block   chan struct{}
	onEvent func(context.Context, Event) error
}

func (c *testEventCollector) HandleEvent(ctx context.Context, event Event) error {
	c.mu.Lock()
	c.events = append(c.events, event.clone())
	if c.waitCh != nil {
		close(c.waitCh)
		c.waitCh = nil
	}
	c.mu.Unlock()
	if c.block != nil {
		<-c.block
	}
	if c.onEvent != nil {
		return c.onEvent(ctx, event)
	}
	return nil
}

func (c *testEventCollector) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	for i, event := range c.events {
		out[i] = event.clone()
	}
	return out
}

func TestSessionEventClockCanBeInjectedForDeterministicTimestamps(t *testing.T) {
	revision := mustCompile(t)
	clockValues := []time.Time{
		time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 12, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 12, 10, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 12, 10, 0, 3, 0, time.UTC),
	}
	i := 0
	sessionID := SessionID("event-clock-session")
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID(sessionID),
		WithEventClock(func() time.Time {
			value := clockValues[i]
			if i < len(clockValues)-1 {
				i++
			}
			return value
		}),
		WithEventListener(collector),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	asserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	modified, err := session.Modify(context.Background(), asserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Ada II"}),
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if _, err := session.Retract(context.Background(), asserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	events := collector.Events()
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	if events[0].Type != EventFactAsserted || events[1].Type != EventFactModified || events[2].Type != EventFactRetracted || events[3].Type != EventReset {
		t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	}

	for i, event := range events {
		if got, want := event.Timestamp, clockValues[i]; got != want {
			t.Fatalf("event %d timestamp = %v, want %v", i, got, want)
		}
	}
	if got, want := events[0].Sequence, uint64(1); got != want {
		t.Fatalf("assert sequence = %d, want %d", got, want)
	}
	if got, want := events[1].Sequence, uint64(2); got != want {
		t.Fatalf("modify sequence = %d, want %d", got, want)
	}
	if got, want := events[2].Sequence, uint64(3); got != want {
		t.Fatalf("retract sequence = %d, want %d", got, want)
	}
	if got, want := events[3].Sequence, uint64(4); got != want {
		t.Fatalf("reset sequence = %d, want %d", got, want)
	}

	for i := range events {
		if events[i].SessionID != sessionID {
			t.Fatalf("event %d session id = %q", i, events[i].SessionID)
		}
		if events[i].RulesetID != revision.ID() {
			t.Fatalf("event %d ruleset id = %q, want %q", i, events[i].RulesetID, revision.ID())
		}
	}

	if events[1].Recency != modified.Fact.Recency() {
		t.Fatalf("modify event recency = %d, want %d", events[1].Recency, modified.Fact.Recency())
	}
	if events[1].Generation != 1 || events[3].Generation != 2 {
		t.Fatalf("event generations = %d, %d, want 1 and 2", events[1].Generation, events[3].Generation)
	}
	if events[3].FactIDs != nil {
		t.Fatalf("reset event fact IDs should be nil, got %#v", events[3].FactIDs)
	}
	if events[3].Delta == nil || events[3].Delta.Generation != 2 || events[3].Delta.OldGeneration != 1 {
		t.Fatalf("reset event delta generation mismatch: %#v", events[3].Delta)
	}
}

func TestSessionListenerFailureDoesNotFailMutationAndStillDispatchesToLaterListeners(t *testing.T) {
	listenerErr := errors.New("listener failure")
	first := &testEventCollector{
		onEvent: func(_ context.Context, _ Event) error {
			return listenerErr
		},
	}
	second := &testEventCollector{}

	session, err := NewSession(mustCompile(t), WithEventListener(first), WithEventListener(second))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if !inserted.Inserted() {
		t.Fatalf("insert status = %v, want %v", inserted.Status, AssertInserted)
	}

	firstEvents := first.Events()
	secondEvents := second.Events()
	if len(firstEvents) != 1 {
		t.Fatalf("first listener events = %d, want 1", len(firstEvents))
	}
	if len(secondEvents) != 1 {
		t.Fatalf("second listener events = %d, want 1", len(secondEvents))
	}
	if firstEvents[0].Sequence != 1 || secondEvents[0].Sequence != 1 {
		t.Fatalf("listener event sequences = (%d, %d), want (1, 1)", firstEvents[0].Sequence, secondEvents[0].Sequence)
	}
}
