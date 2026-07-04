package engine

import (
	"bytes"
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
	for i, event := range events {
		if event.RuleID != "" || event.RuleRevisionID != "" || event.ActivationID != "" {
			t.Fatalf("fact event %d carried rule metadata: %#v", i, event)
		}
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

func TestSessionEventListenerMasksFilterDeliveryAndPreserveGlobalSequence(t *testing.T) {
	revision, personKey, _, _ := mustTraceRuleset(t)
	ruleFired := &testEventCollector{}
	all := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithEventListener(ruleFired, ForEventTypes(EventRuleFired)),
		WithEventListener(all),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.AssertTemplate(context.Background(), personKey, mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	filtered := ruleFired.Events()
	if len(filtered) != 1 {
		t.Fatalf("filtered events = %d, want 1", len(filtered))
	}
	if filtered[0].Type != EventRuleFired {
		t.Fatalf("filtered event type = %v, want %v", filtered[0].Type, EventRuleFired)
	}

	unfiltered := all.Events()
	if len(unfiltered) < 3 {
		t.Fatalf("unfiltered events = %d, want at least 3", len(unfiltered))
	}
	var fired Event
	for _, event := range unfiltered {
		if event.Type == EventRuleFired {
			fired = event
			break
		}
	}
	if fired.Type != EventRuleFired {
		t.Fatalf("unfiltered listener did not receive rule fired event: %#v", unfiltered)
	}
	if filtered[0].Sequence != fired.Sequence {
		t.Fatalf("filtered sequence = %d, unfiltered fired sequence = %d", filtered[0].Sequence, fired.Sequence)
	}
	if filtered[0].Sequence == 1 {
		t.Fatalf("filtered listener should observe a global sequence gap, got sequence 1")
	}
	for i, event := range unfiltered {
		if got, want := event.Sequence, uint64(i+1); got != want {
			t.Fatalf("unfiltered event %d sequence = %d, want %d", i, got, want)
		}
	}
}

func TestSessionEventMaskSkipsFactEventConstruction(t *testing.T) {
	var clockCalls int
	session, err := NewSession(
		mustCompile(t),
		WithEventClock(func() time.Time {
			clockCalls++
			return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
		}),
		WithEventListener(&testEventCollector{}, ForEventTypes(EventRuleFired)),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if _, err := session.Retract(context.Background(), inserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	if clockCalls != 0 {
		t.Fatalf("event clock calls = %d, want 0 masked fact event envelopes", clockCalls)
	}
	if session.nextEventSequence != 0 {
		t.Fatalf("next event sequence = %d, want 0 for fully masked fact churn", session.nextEventSequence)
	}
}

func TestTraceListenerProducesDeterministicOutput(t *testing.T) {
	revision, personKey, sourceKey, failKey := mustTraceRuleset(t)
	var out bytes.Buffer
	clockValues := make([]time.Time, 0, 64)
	for i := range 64 {
		clockValues = append(clockValues, time.Date(2026, 7, 4, 12, 0, i, 0, time.UTC))
	}
	clockIndex := 0
	session, err := NewSession(
		revision,
		WithSessionID("trace-session"),
		WithEventListener(NewTraceListener(&out)),
		WithEventClock(func() time.Time {
			value := clockValues[clockIndex]
			if clockIndex < len(clockValues)-1 {
				clockIndex++
			}
			return value
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	person, err := session.AssertTemplate(context.Background(), personKey, mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run(person): %v", err)
	}
	if _, err := session.Modify(context.Background(), person.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify(person): %v", err)
	}
	if _, err := session.Retract(context.Background(), person.Fact.ID()); err != nil {
		t.Fatalf("Retract(person): %v", err)
	}

	source, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run(source): %v", err)
	}
	if _, err := session.Retract(context.Background(), source.Fact.ID()); err != nil {
		t.Fatalf("Retract(source): %v", err)
	}

	if _, err := session.AssertTemplate(context.Background(), failKey, mustFields(t, map[string]any{"id": "f-1"})); err != nil {
		t.Fatalf("AssertTemplate(fail): %v", err)
	}
	if _, err := session.Run(context.Background()); !errors.Is(err, ErrActionFailed) {
		t.Fatalf("Run(fail) error = %v, want ErrActionFailed", err)
	}
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if out.Len() == 0 {
		t.Fatal("trace output was empty")
	}
	if got, want := out.String(), traceGoldenOutput; got != want {
		t.Fatalf("trace output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func mustTraceRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "source",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "derived",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "child",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	fail := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "fail",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})

	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddAction(t, workspace, ActionSpec{
		Name: "derive",
		Fn: func(ctx ActionContext) error {
			id, ok := ctx.BindingScalarValue("source", "id")
			if !ok {
				return ErrFactNotFound
			}
			_, err := ctx.AssertLogical("derived", Fields{"id": id})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "derive-child",
		Fn: func(ctx ActionContext) error {
			derived, ok := ctx.Binding("derived")
			if !ok {
				return ErrFactNotFound
			}
			id, ok := derived.Field("id")
			if !ok {
				return ErrFactNotFound
			}
			_, err := ctx.AssertLogical("child", Fields{"id": id})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "fail-action",
		Fn: func(ActionContext) error {
			return errors.New("trace action failed")
		},
	})

	mustAddRule(t, workspace, RuleSpec{
		Name:       "observe-person",
		Conditions: []RuleConditionSpec{{Binding: "person", Target: TemplateKeyFact(person.Key())}},
		Actions:    []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "derive",
		Conditions: []RuleConditionSpec{{Binding: "source", Target: TemplateKeyFact(source.Key())}},
		Actions:    []RuleActionSpec{{Name: "derive"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "derive-child",
		Conditions: []RuleConditionSpec{{Binding: "derived", Target: DynamicFact("derived")}},
		Actions:    []RuleActionSpec{{Name: "derive-child"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "fail-rule",
		Conditions: []RuleConditionSpec{{Binding: "fail", Target: TemplateKeyFact(fail.Key())}},
		Actions:    []RuleActionSpec{{Name: "fail-action"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, person.Key(), source.Key(), fail.Key()
}

const traceGoldenOutput = `seq=1 type=fact_asserted fact=fact:g1:1 template=person fields={name="Ada"}
seq=2 type=rule_activated rule=observe-person revision=sha256:de45b210d3481980a250dbcb542c9eff40b93d7827300d18e0d4fb216102fa9b activation=activation:v1:2600649297375840360:14200915881358373148:1 facts=[fact:g1:1]
seq=3 type=rule_fired severity=info run=run:1 rule=observe-person revision=sha256:de45b210d3481980a250dbcb542c9eff40b93d7827300d18e0d4fb216102fa9b activation=activation:v1:2600649297375840360:14200915881358373148:1 facts=[fact:g1:1]
seq=4 type=fact_modified fact=fact:g1:1 template=person fields={name="Grace"} changes={name="Ada"->"Grace"}
seq=5 type=rule_activated rule=observe-person revision=sha256:de45b210d3481980a250dbcb542c9eff40b93d7827300d18e0d4fb216102fa9b activation=activation:v1:2600649297375840360:18070296845151748607:2 facts=[fact:g1:1]
seq=6 type=fact_retracted fact=fact:g1:1 template=person fields={name="Grace"}
seq=7 type=rule_deactivated rule=observe-person revision=sha256:de45b210d3481980a250dbcb542c9eff40b93d7827300d18e0d4fb216102fa9b activation=activation:v1:2600649297375840360:18070296845151748607:2 facts=[fact:g1:1]
seq=8 type=fact_asserted fact=fact:g1:2 template=source fields={id="s-1"}
seq=9 type=rule_activated rule=derive revision=sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7 activation=activation:v1:12796299967188325699:7937034350915783232:3 facts=[fact:g1:2]
seq=10 type=rule_fired severity=info run=run:2 rule=derive revision=sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7 activation=activation:v1:12796299967188325699:7937034350915783232:3 facts=[fact:g1:2]
seq=11 type=logical_support_added severity=info support=support:v2:sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7:12796299967188325699:7937034350915783232:fact:g1:3 fact=fact:g1:3 rule=derive revision=sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7 activation=activation:v1:12796299967188325699:7937034350915783232:3 supporting=[fact:g1:2]
seq=12 type=fact_asserted fact=fact:g1:3 template=derived fields={id="s-1"} rule=derive revision=sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7 activation=activation:v1:12796299967188325699:7937034350915783232:3
seq=13 type=rule_activated rule=derive-child revision=sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0 activation=activation:v1:17956796239649721052:14595700963830923953:4 facts=[fact:g1:3]
seq=14 type=rule_fired severity=info run=run:2 rule=derive-child revision=sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0 activation=activation:v1:17956796239649721052:14595700963830923953:4 facts=[fact:g1:3]
seq=15 type=logical_support_added severity=info support=support:v2:sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0:17956796239649721052:14595700963830923953:fact:g1:4 fact=fact:g1:4 rule=derive-child revision=sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0 activation=activation:v1:17956796239649721052:14595700963830923953:4 supporting=[fact:g1:3]
seq=16 type=fact_asserted fact=fact:g1:4 template=child fields={id="s-1"} rule=derive-child revision=sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0 activation=activation:v1:17956796239649721052:14595700963830923953:4
seq=17 type=fact_retracted fact=fact:g1:2 template=source fields={id="s-1"}
seq=18 type=logical_support_removed severity=info support=support:v2:sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7:12796299967188325699:7937034350915783232:fact:g1:3 fact=fact:g1:3 rule=derive revision=sha256:e9f90a1865a109cbb5cec534e0fe99ceb07ed22566f0cf441cad89309d9030d7 activation=activation:v1:12796299967188325699:7937034350915783232:3 supporting=[fact:g1:2]
seq=19 type=fact_retracted fact=fact:g1:3 template=derived fields={id="s-1"}
seq=20 type=logical_support_removed severity=info support=support:v2:sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0:17956796239649721052:14595700963830923953:fact:g1:4 fact=fact:g1:4 rule=derive-child revision=sha256:990501bf7a935c22bf0c3c60102a2976a5cd4e2e77bc10c83fa8b7995de5d8b0 activation=activation:v1:17956796239649721052:14595700963830923953:4 supporting=[fact:g1:3]
seq=21 type=fact_retracted fact=fact:g1:4 template=child fields={id="s-1"}
seq=22 type=fact_asserted fact=fact:g1:5 template=fail fields={id="f-1"}
seq=23 type=rule_activated rule=fail-rule revision=sha256:4f6b959ac0c955b96beaecd38d4f96c0c262caea3421e4589dc79b257594aef0 activation=activation:v1:15524002455324254194:8149420427212459193:5 facts=[fact:g1:5]
seq=24 type=rule_fired severity=info run=run:3 rule=fail-rule revision=sha256:4f6b959ac0c955b96beaecd38d4f96c0c262caea3421e4589dc79b257594aef0 activation=activation:v1:15524002455324254194:8149420427212459193:5 facts=[fact:g1:5]
seq=25 type=action_failed severity=error run=run:3 rule=fail-rule revision=sha256:4f6b959ac0c955b96beaecd38d4f96c0c262caea3421e4589dc79b257594aef0 activation=activation:v1:15524002455324254194:8149420427212459193:5 facts=[fact:g1:5] action="fail-action" action_index=0 error="trace action failed"
seq=26 type=reset generation=2 old_generation=1
`
