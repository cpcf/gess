package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionRunCompletesWithoutMatchingActivations(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(mark): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("run-empty-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	if result.Fired != 0 {
		t.Fatalf("run fired = %d, want 0", result.Fired)
	}
	if result.RunID.IsZero() {
		t.Fatal("run ID is zero")
	}
	if len(collector.Events()) != 0 {
		t.Fatalf("unexpected events: %#v", collector.Events())
	}
}

func TestSessionRunRejectsDirtyAgendaWithoutWholeTerminalReconcile(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(mark): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(person-rule): %v", err)
	}
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("run-dirty-agenda-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("initial run status = %v, want %v", result.Status, RunCompleted)
	}
	if !session.agendaReady || session.agendaDirty {
		t.Fatalf("agenda state after initial run = ready %v dirty %v, want ready clean", session.agendaReady, session.agendaDirty)
	}
	beforeCounters := session.propagationCounterSnapshot().Totals
	if got, want := beforeCounters.WholeTerminalScans, 1; got != want {
		t.Fatalf("initial run whole terminal scans = %d, want %d", got, want)
	}
	if got, want := beforeCounters.InitialWholeTerminalScans, 1; got != want {
		t.Fatalf("initial run initial whole terminal scans = %d, want %d", got, want)
	}

	session.markAgendaDirty()
	result, err = session.Run(context.Background())
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("dirty Run error = %v, want ErrUnsupportedRuntime", err)
	}
	if result.Status != RunFailed {
		t.Fatalf("dirty run status = %v, want %v", result.Status, RunFailed)
	}
	if result.Fired != 0 {
		t.Fatalf("dirty run fired = %d, want 0", result.Fired)
	}
	afterCounters := session.propagationCounterSnapshot().Totals
	if got, want := afterCounters.FullAgendaReconciles, beforeCounters.FullAgendaReconciles; got != want {
		t.Fatalf("dirty run full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.WholeTerminalScans, beforeCounters.WholeTerminalScans; got != want {
		t.Fatalf("dirty run whole terminal scans = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.SteadyStateWholeTerminalScans, beforeCounters.SteadyStateWholeTerminalScans; got != want {
		t.Fatalf("dirty run steady-state whole terminal scans = %d, want unchanged %d", got, want)
	}
}

func TestSessionRunHaltStopsAfterCurrentActivation(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(event): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var actions []string
	if err := workspace.AddAction(ActionSpec{
		Name: "before",
		Fn: func(ctx ActionContext) error {
			actions = append(actions, "before")
			_, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"kind": "before"}))
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(before): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "halt",
		Fn: func(ctx ActionContext) error {
			actions = append(actions, "halt")
			return ctx.Halt()
		},
	}); err != nil {
		t.Fatalf("AddAction(halt): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "after",
		Fn: func(ctx ActionContext) error {
			actions = append(actions, "after")
			_, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"kind": "after"}))
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(after): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "follow-up",
		Fn: func(ActionContext) error {
			actions = append(actions, "follow-up")
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(follow-up): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name:     "halt-rule",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{Binding: "event", Target: TemplateKeyFact(TemplateKey("event"))},
		},
		Actions: []RuleActionSpec{{Name: "before"}, {Name: "halt"}, {Name: "after"}},
	}); err != nil {
		t.Fatalf("AddRule(halt-rule): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "follow-up-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "event", Target: TemplateKeyFact(TemplateKey("event"))},
		},
		Actions: []RuleActionSpec{{Name: "follow-up"}},
	}); err != nil {
		t.Fatalf("AddRule(follow-up-rule): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("run-halt-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("event"), mustFields(t, map[string]any{"id": "e-1"})); err != nil {
		t.Fatalf("AssertTemplate(event): %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run halted error = %v, want nil", err)
	}
	if result.Status != RunHalted || result.Fired != 1 {
		t.Fatalf("halted run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunHalted)
	}
	if got, want := len(actions), 3; got != want {
		t.Fatalf("actions after halted run = %#v, want 3 current-activation actions", actions)
	}
	for i, want := range []string{"before", "halt", "after"} {
		if actions[i] != want {
			t.Fatalf("actions[%d] = %q, want %q in %#v", i, actions[i], want, actions)
		}
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after halted run = %d, want %d", got, want)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	auditFacts := 0
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() == TemplateKey("audit") {
			auditFacts++
		}
	}
	if auditFacts != 2 {
		t.Fatalf("audit facts after halted run = %d, want 2", auditFacts)
	}

	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("second run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}
	if got, want := actions[len(actions)-1], "follow-up"; got != want {
		t.Fatalf("last action after second run = %q, want %q in %#v", got, want, actions)
	}
}

func TestSessionRunActionFailureRemainsDistinctFromHalt(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(event): %v", err)
	}

	terminalErr := errors.New("terminal failure")
	if err := workspace.AddAction(ActionSpec{
		Name: "halt",
		Fn: func(ctx ActionContext) error {
			return ctx.Halt()
		},
	}); err != nil {
		t.Fatalf("AddAction(halt): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "fail",
		Fn: func(ActionContext) error {
			return terminalErr
		},
	}); err != nil {
		t.Fatalf("AddAction(fail): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "halt-then-fail",
		Conditions: []RuleConditionSpec{
			{Binding: "event", Target: TemplateKeyFact(TemplateKey("event"))},
		},
		Actions: []RuleActionSpec{{Name: "halt"}, {Name: "fail"}},
	}); err != nil {
		t.Fatalf("AddRule(halt-then-fail): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("run-halt-failure-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("event"), mustFields(t, map[string]any{"id": "e-1"})); err != nil {
		t.Fatalf("AssertTemplate(event): %v", err)
	}

	result, err := session.Run(context.Background())
	if result.Status != RunActionFailed || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunActionFailed)
	}
	if !errors.Is(err, ErrActionFailed) || !errors.Is(err, terminalErr) {
		t.Fatalf("run error = %v, want action failure wrapping terminal error", err)
	}
}

func TestSessionRunDoesNotFireInvalidatedGraphTokenActivations(t *testing.T) {
	tests := []struct {
		name       string
		invalidate func(context.Context, *Session, FactID) error
	}{
		{
			name: "retract",
			invalidate: func(ctx context.Context, session *Session, id FactID) error {
				result, err := session.Retract(ctx, id)
				if err != nil {
					return err
				}
				if result.Status != RetractRemoved {
					return errors.New("fact was not retracted")
				}
				return nil
			},
		},
		{
			name: "modify",
			invalidate: func(ctx context.Context, session *Session, id FactID) error {
				result, err := session.Modify(ctx, id, FactPatch{
					Set: mustFields(t, map[string]any{"status": "done"}),
				})
				if err != nil {
					return err
				}
				if result.Status != ModifyChanged {
					return errors.New("fact was not modified")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			workspace := NewWorkspace()
			task := mustAddTemplate(t, workspace, TemplateSpec{
				Name: "task",
				Fields: []FieldSpec{
					{Name: "id", Kind: ValueString, Required: true},
					{Name: "status", Kind: ValueString, Required: true},
				},
			})

			actionsFired := 0
			mustAddAction(t, workspace, ActionSpec{
				Name: "record",
				Fn: func(ActionContext) error {
					actionsFired++
					return nil
				},
			})
			mustAddRule(t, workspace, RuleSpec{
				Name: "open-task",
				Conditions: []RuleConditionSpec{{
					Binding: "task",

					FieldConstraints: []FieldConstraintSpec{{
						Field:    "status",
						Operator: FieldConstraintEqual,
						Value:    mustValue(t, "open"),
					}}, Target: TemplateKeyFact(task.Key()),
				}},
				Actions: []RuleActionSpec{{Name: "record"}},
			})

			revision, err := workspace.Compile(ctx)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			session, err := NewSession(revision)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if session.rete == nil || session.rete.graphBeta == nil {
				t.Fatal("session has no graph beta runtime")
			}

			asserted, err := session.AssertTemplate(ctx, task.Key(), mustFields(t, map[string]any{
				"id":     "t-1",
				"status": "open",
			}))
			if err != nil {
				t.Fatalf("AssertTemplate: %v", err)
			}
			if got := len(session.agenda.pendingActivations()); got != 1 {
				t.Fatalf("pending activations after assert = %d, want 1", got)
			}
			rule := revision.rules["open-task"]
			terminal := session.rete.graphBeta.terminalForRule(rule.revisionID)
			if terminal == nil || terminal.rows.len() != 1 {
				t.Fatalf("terminal rows after assert = %#v, want one retained row", terminal)
			}

			if err := tt.invalidate(ctx, session, asserted.Fact.ID()); err != nil {
				t.Fatalf("invalidate: %v", err)
			}
			if got := len(session.agenda.pendingActivations()); got != 0 {
				t.Fatalf("pending activations after %s = %d, want 0", tt.name, got)
			}
			if terminal.rows.len() != 0 {
				t.Fatalf("terminal rows after %s = %d, want 0", tt.name, terminal.rows.len())
			}

			result, err := session.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Status != RunCompleted {
				t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
			}
			if result.Fired != 0 {
				t.Fatalf("run fired = %d, want 0", result.Fired)
			}
			if actionsFired != 0 {
				t.Fatalf("actions fired = %d, want 0", actionsFired)
			}
		})
	}
}

func TestSessionRunKeepsMultipleActionAssertDeltasDistinct(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "seed",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(seed): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "child",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(child): %v", err)
	}

	var recorded []string
	addChildAction := func(name, id string) {
		t.Helper()
		if err := workspace.AddAction(ActionSpec{
			Name: name,
			Fn: func(ctx ActionContext) error {
				_, err := ctx.AssertTemplate(TemplateKey("child"), mustFields(t, map[string]any{"id": id}))
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(%s): %v", name, err)
		}
	}
	addChildAction("child-a", "a")
	addChildAction("child-b", "b")
	addChildAction("child-c", "c")
	if err := workspace.AddAction(ActionSpec{
		Name: "record-child",
		Fn: func(ctx ActionContext) error {
			child, ok := ctx.Binding("child")
			if !ok {
				return ErrInvalidRuleset
			}
			value, ok := child.Field("id")
			if !ok || value.Kind() != ValueString {
				return ErrInvalidRuleset
			}
			recorded = append(recorded, value.stringValue)
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(record-child): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "seed-creates-children",
		Conditions: []RuleConditionSpec{
			{Binding: "seed", Target: TemplateKeyFact(TemplateKey("seed"))},
		},
		Actions: []RuleActionSpec{
			{Name: "child-a"},
			{Name: "child-b"},
			{Name: "child-c"},
		},
	}); err != nil {
		t.Fatalf("AddRule(seed-creates-children): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "record-created-child",
		Conditions: []RuleConditionSpec{
			{Binding: "child", Target: TemplateKeyFact(TemplateKey("child"))},
		},
		Actions: []RuleActionSpec{{Name: "record-child"}},
	}); err != nil {
		t.Fatalf("AddRule(record-created-child): %v", err)
	}
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("run-action-delta-alias"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("seed"), mustFields(t, map[string]any{"id": "seed"})); err != nil {
		t.Fatalf("AssertTemplate(seed): %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 4 {
		t.Fatalf("run result = (%v, %d), want (%v, 4)", result.Status, result.Fired, RunCompleted)
	}
	seen := map[string]int{}
	for _, id := range recorded {
		seen[id]++
	}
	if len(recorded) != 3 || len(seen) != 3 || seen["a"] != 1 || seen["b"] != 1 || seen["c"] != 1 {
		t.Fatalf("recorded children = %#v, want a, b, and c once each", recorded)
	}
}

func TestSessionRunKeepsRepeatedActionAssertFactsDistinct(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "seed",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(seed): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name:            "child",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(child): %v", err)
	}

	var recorded []FactID
	addChildAction := func(name string) {
		t.Helper()
		if err := workspace.AddAction(ActionSpec{
			Name: name,
			Fn: func(ctx ActionContext) error {
				_, err := ctx.AssertTemplate(TemplateKey("child"), mustFields(t, map[string]any{"id": "same"}))
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(%s): %v", name, err)
		}
	}
	addChildAction("child-first")
	addChildAction("child-second")
	if err := workspace.AddAction(ActionSpec{
		Name: "record-child",
		Fn: func(ctx ActionContext) error {
			child, ok := ctx.Binding("child")
			if !ok {
				return ErrInvalidRuleset
			}
			recorded = append(recorded, child.ID())
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(record-child): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "seed-creates-children",
		Conditions: []RuleConditionSpec{
			{Binding: "seed", Target: TemplateKeyFact(TemplateKey("seed"))},
		},
		Actions: []RuleActionSpec{
			{Name: "child-first"},
			{Name: "child-second"},
		},
	}); err != nil {
		t.Fatalf("AddRule(seed-creates-children): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "record-created-child",
		Conditions: []RuleConditionSpec{
			{Binding: "child", Target: TemplateKeyFact(TemplateKey("child"))},
		},
		Actions: []RuleActionSpec{{Name: "record-child"}},
	}); err != nil {
		t.Fatalf("AddRule(record-created-child): %v", err)
	}
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("run-repeated-action-assert-facts"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("seed"), mustFields(t, map[string]any{"id": "seed"})); err != nil {
		t.Fatalf("AssertTemplate(seed): %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 3 {
		t.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
	}
	if len(recorded) != 2 {
		t.Fatalf("recorded child activations = %#v, want two", recorded)
	}
	if recorded[0] == recorded[1] {
		t.Fatalf("recorded duplicate child fact ID %q twice", recorded[0])
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after run = %d, want 0", got)
	}
}

func TestSessionRunFiresActivationAndAllowsActionContextMutations(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var (
		actionsSeen       []string
		initialBinding    FactSnapshot
		initialBoundFacts []FactSnapshot
		laterBinding      FactSnapshot
		laterBoundFacts   []FactSnapshot
		personFactID      FactID
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "capture",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "capture")
			boundFacts := ctx.BoundFacts()
			if len(boundFacts) != 1 {
				return errors.New("expected one bound fact")
			}
			initialBoundFacts = boundFacts
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			initialBinding = binding
			if got := binding.Fields()["status"]; !got.Equal(mustValue(t, "pending")) {
				return errors.New("unexpected initial binding value")
			}
			personFactID = binding.ID()
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(capture): %v", err)
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "mutate",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "mutate")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			if got := binding.Fields()["status"]; !got.Equal(mustValue(t, "pending")) {
				return errors.New("binding changed before modify")
			}
			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: mustFields(t, map[string]any{"status": "done"}),
			})
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(mutate): %v", err)
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "verify",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "verify")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			laterBinding = binding
			laterBoundFacts = ctx.BoundFacts()
			if got := binding.Fields()["status"]; !got.Equal(mustValue(t, "pending")) {
				return errors.New("binding changed after modify")
			}
			if got := laterBoundFacts[0].Fields()["status"]; !got.Equal(mustValue(t, "pending")) {
				return errors.New("bound facts changed after modify")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(verify): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
				}, Target: TemplateKeyFact(TemplateKey("person")),
			},
		},
		Actions: []RuleActionSpec{
			{Name: "capture"},
			{Name: "mutate"},
			{Name: "verify"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("run-mutation-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{
		"name":   "Ada",
		"status": "pending",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	if result.Fired != 1 {
		t.Fatalf("run fired = %d, want 1", result.Fired)
	}
	if result.RunID.IsZero() {
		t.Fatal("run ID is zero")
	}
	if got, want := actionsSeen, []string{"capture", "mutate", "verify"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
	if initialBinding.ID() != laterBinding.ID() {
		t.Fatalf("binding fact ID changed: %q vs %q", initialBinding.ID(), laterBinding.ID())
	}
	if len(initialBoundFacts) != 1 || len(laterBoundFacts) != 1 {
		t.Fatalf("bound facts lengths = %d and %d, want 1 and 1", len(initialBoundFacts), len(laterBoundFacts))
	}
	if got := initialBoundFacts[0].Fields()["status"]; !got.Equal(mustValue(t, "pending")) {
		t.Fatalf("initial bound fact status = %v, want pending", got)
	}
	if got := laterBoundFacts[0].Fields()["status"]; !got.Equal(mustValue(t, "pending")) {
		t.Fatalf("later bound fact status = %v, want pending", got)
	}
	if personFactID != personFact.Fact.ID() {
		t.Fatalf("capture action person fact ID = %q, want %q", personFactID, personFact.Fact.ID())
	}

	after := mustSnapshot(t, context.Background(), session)
	stored, ok := after.Fact(personFact.Fact.ID())
	if !ok {
		t.Fatalf("snapshot missing person fact %q", personFact.Fact.ID())
	}
	if got := stored.Fields()["status"]; !got.Equal(mustValue(t, "done")) {
		t.Fatalf("session fact status after modify = %v, want done", got)
	}

	events := collector.Events()
	var fired Event
	foundFired := false
	foundModified := false
	for _, event := range events {
		if event.Type == EventRuleFired {
			fired = event
			foundFired = true
		}
		if event.Type == EventFactModified {
			foundModified = true
		}
	}
	if !foundFired {
		t.Fatal("rule fired event missing")
	}
	if !foundModified {
		t.Fatal("modify event missing")
	}
	if fired.RunID != result.RunID {
		t.Fatalf("fired event run ID = %q, want %q", fired.RunID, result.RunID)
	}
	if fired.Severity != EventSeverityInfo {
		t.Fatalf("fired event severity = %q, want %q", fired.Severity, EventSeverityInfo)
	}
	if fired.RuleID == "" || fired.RuleRevisionID == "" || fired.ActivationID == "" {
		t.Fatalf("fired event missing rule metadata: %#v", fired)
	}
	if len(fired.FactIDs) != 1 || fired.FactIDs[0] != personFact.Fact.ID() {
		t.Fatalf("fired event fact IDs = %#v, want %q", fired.FactIDs, personFact.Fact.ID())
	}
}

func TestSessionRunActionContextMutationAdvancesNextActivation(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var actionsSeen []string
	if err := workspace.AddAction(ActionSpec{
		Name: "promote",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "promote")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: mustFields(t, map[string]any{"status": "done"}),
			})
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(promote): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "record")
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(record): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "pending-rule",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
				}, Target: TemplateKeyFact(TemplateKey("person")),
			},
		},
		Actions: []RuleActionSpec{{Name: "promote"}},
	}); err != nil {
		t.Fatalf("AddRule(pending): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "done-rule",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "done")},
				}, Target: TemplateKeyFact(TemplateKey("person")),
			},
		},
		Actions: []RuleActionSpec{{Name: "record"}},
	}); err != nil {
		t.Fatalf("AddRule(done): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("run-safe-point-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: TemplateKey("person"),
			Fields: mustFields(t, map[string]any{
				"name":   "Ada",
				"status": "pending",
			}),
		}),
		WithEventListener(collector),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	if result.Fired != 2 {
		t.Fatalf("run fired = %d, want 2", result.Fired)
	}
	if got, want := actionsSeen, []string{"promote", "record"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}

	events := collector.Events()
	modifyIndex := -1
	secondFireIndex := -1
	for i, event := range events {
		if event.Type == EventFactModified && modifyIndex == -1 {
			modifyIndex = i
		}
		if event.Type == EventRuleFired && i > 0 {
			secondFireIndex = i
		}
	}
	if modifyIndex == -1 {
		t.Fatal("modify event missing")
	}
	if secondFireIndex == -1 {
		t.Fatal("second rule fired event missing")
	}
	if modifyIndex > secondFireIndex {
		t.Fatalf("modify event appeared after second fire: modify=%d fire=%d", modifyIndex, secondFireIndex)
	}
}

func TestSessionRunAppliesActionOriginAgendaDeltas(t *testing.T) {
	t.Run("supported single delta", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "input",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(input): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "audit",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(audit): %v", err)
		}

		var session *Session
		var actionsSeen []string
		if err := workspace.AddAction(ActionSpec{
			Name: "assert-audit",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "assert-audit")
				result, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"id": "a-1"}))
				if err != nil {
					return err
				}
				if result.Status != AssertInserted {
					return errors.New("audit was not inserted")
				}
				if session.agendaDirty {
					return errors.New("single supported assert dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("single supported assert cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("single supported assert did not record run delta")
				}
				auditRule := session.revision.rules["audit-rule"]
				terminal := session.rete.graphBeta.terminalForRule(auditRule.revisionID)
				if terminal == nil || terminal.rows.len() != 1 {
					return errors.New("audit terminal row was not retained")
				}
				row := terminal.rows.rows[0]
				identity := terminal.terminalTokenIdentity(row.token)
				activation, _, ok := session.agenda.activationForTerminalTokenIdentity(auditRule, row.token, identity)
				if !ok || activation.status != activationStatusPending {
					return errors.New("audit terminal token identity did not resolve to a pending activation")
				}
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(assert-audit): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record",
			Fn: func(ActionContext) error {
				actionsSeen = append(actionsSeen, "record")
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(record): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "input-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "input", Target: TemplateKeyFact(TemplateKey("input"))},
			},
			Actions: []RuleActionSpec{{Name: "assert-audit"}},
		}); err != nil {
			t.Fatalf("AddRule(input-rule): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "audit-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "audit", Target: TemplateKeyFact(TemplateKey("audit"))},
			},
			Actions: []RuleActionSpec{{Name: "record"}},
		}); err != nil {
			t.Fatalf("AddRule(audit-rule): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		session, err = NewSession(
			revision,
			WithSessionID("run-supported-single-delta-session"),
			WithInitialFacts(SessionInitialFact{
				TemplateKey: TemplateKey("input"),
				Fields:      mustFields(t, map[string]any{"id": "i-1"}),
			}),
		)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 2 {
			t.Fatalf("run fired = %d, want 2", result.Fired)
		}
		if got, want := actionsSeen, []string{"assert-audit", "record"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("action order = %#v, want %#v", got, want)
		}
		if session.runAgendaPending {
			t.Fatal("run delta remained pending after successful run")
		}
		if session.agendaDirty || !session.agendaReady {
			t.Fatalf("agenda state after run = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
		}
	})

	t.Run("supported single removed delta", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "trigger",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(trigger): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "task",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(task): %v", err)
		}

		var (
			session     *Session
			taskID      FactID
			actionsSeen []string
		)
		if err := workspace.AddAction(ActionSpec{
			Name: "close-task",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "close-task")
				result, err := ctx.Modify(taskID, FactPatch{
					Set: mustFields(t, map[string]any{"status": "done"}),
				})
				if err != nil {
					return err
				}
				if result.Status != ModifyChanged {
					return errors.New("task was not modified")
				}
				if session.agendaDirty {
					return errors.New("single supported modify dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("single supported modify cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("single supported modify did not record run delta")
				}
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(close-task): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "unexpected-open",
			Fn: func(ActionContext) error {
				actionsSeen = append(actionsSeen, "unexpected-open")
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(unexpected-open): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record-done",
			Fn: func(ActionContext) error {
				actionsSeen = append(actionsSeen, "record-done")
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(record-done): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name:     "close-rule",
			Salience: 20,
			Conditions: []RuleConditionSpec{
				{Binding: "trigger", Target: TemplateKeyFact(TemplateKey("trigger"))},
			},
			Actions: []RuleActionSpec{{Name: "close-task"}},
		}); err != nil {
			t.Fatalf("AddRule(close-rule): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "open-task",
			Conditions: []RuleConditionSpec{
				{
					Binding: "task",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "open")},
					}, Target: TemplateKeyFact(TemplateKey("task")),
				},
			},
			Actions: []RuleActionSpec{{Name: "unexpected-open"}},
		}); err != nil {
			t.Fatalf("AddRule(open-task): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "done-task",
			Conditions: []RuleConditionSpec{
				{
					Binding: "task",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "done")},
					}, Target: TemplateKeyFact(TemplateKey("task")),
				},
			},
			Actions: []RuleActionSpec{{Name: "record-done"}},
		}); err != nil {
			t.Fatalf("AddRule(done-task): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		collector := &testEventCollector{}
		session, err = NewSession(revision, WithSessionID("run-supported-single-removed-delta-session"), WithEventListener(collector))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		task, err := session.AssertTemplate(context.Background(), TemplateKey("task"), mustFields(t, map[string]any{
			"id":     "task-1",
			"status": "open",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate(task): %v", err)
		}
		taskID = task.Fact.ID()
		if _, err := session.AssertTemplate(context.Background(), TemplateKey("trigger"), mustFields(t, map[string]any{"id": "trigger-1"})); err != nil {
			t.Fatalf("AssertTemplate(trigger): %v", err)
		}

		runEventOffset := len(collector.Events())
		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 2 {
			t.Fatalf("run fired = %d, want 2", result.Fired)
		}
		if got, want := actionsSeen, []string{"close-task", "record-done"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("action order = %#v, want %#v", got, want)
		}
		if session.runAgendaPending {
			t.Fatal("run delta remained pending after successful run")
		}
		if session.agendaDirty || !session.agendaReady {
			t.Fatalf("agenda state after run = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
		}

		events := collector.Events()[runEventOffset:]
		modifiedIndex := -1
		deactivatedIndex := -1
		secondFiredIndex := -1
		for i, event := range events {
			switch event.Type {
			case EventFactModified:
				if modifiedIndex == -1 {
					modifiedIndex = i
				}
			case EventRuleDeactivated:
				if event.RuleID == RuleID("open-task") && deactivatedIndex == -1 {
					deactivatedIndex = i
				}
			case EventRuleFired:
				if i > 0 {
					secondFiredIndex = i
				}
			}
		}
		if modifiedIndex == -1 {
			t.Fatal("modify event missing")
		}
		if deactivatedIndex == -1 {
			t.Fatal("open-task deactivation event missing")
		}
		if secondFiredIndex == -1 {
			t.Fatal("second fired event missing")
		}
		if !(modifiedIndex < deactivatedIndex && deactivatedIndex < secondFiredIndex) {
			t.Fatalf("event indexes modify=%d deactivated=%d second-fired=%d, want modify < deactivated < second-fired", modifiedIndex, deactivatedIndex, secondFiredIndex)
		}
	})

	t.Run("supported single delta abandoned after action failure", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "input",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(input): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "audit",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(audit): %v", err)
		}

		var actionsSeen []string
		terminalErr := errors.New("stop after delta")
		if err := workspace.AddAction(ActionSpec{
			Name: "assert-audit",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "assert-audit")
				_, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"id": "a-1"}))
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(assert-audit): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "fail",
			Fn: func(ActionContext) error {
				actionsSeen = append(actionsSeen, "fail")
				return terminalErr
			},
		}); err != nil {
			t.Fatalf("AddAction(fail): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record",
			Fn: func(ActionContext) error {
				actionsSeen = append(actionsSeen, "record")
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(record): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "input-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "input", Target: TemplateKeyFact(TemplateKey("input"))},
			},
			Actions: []RuleActionSpec{{Name: "assert-audit"}, {Name: "fail"}},
		}); err != nil {
			t.Fatalf("AddRule(input-rule): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "audit-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "audit", Target: TemplateKeyFact(TemplateKey("audit"))},
			},
			Actions: []RuleActionSpec{{Name: "record"}},
		}); err != nil {
			t.Fatalf("AddRule(audit-rule): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		session, err := NewSession(
			revision,
			WithSessionID("run-supported-delta-failure-session"),
			WithInitialFacts(SessionInitialFact{
				TemplateKey: TemplateKey("input"),
				Fields:      mustFields(t, map[string]any{"id": "i-1"}),
			}),
		)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		result, err := session.Run(context.Background())
		if !errors.Is(err, ErrActionFailed) || !errors.Is(err, terminalErr) {
			t.Fatalf("Run error = %v, want ErrActionFailed wrapping terminal error", err)
		}
		if result.Status != RunActionFailed {
			t.Fatalf("run status = %v, want %v", result.Status, RunActionFailed)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		if session.runAgendaPending {
			t.Fatal("run delta remained pending after action failure")
		}
		if !session.agendaDirty || session.agendaReady {
			t.Fatalf("agenda state after action failure = dirty %v ready %v, want dirty not ready", session.agendaDirty, session.agendaReady)
		}
		session.attachPropagationCounters()
		beforeCounters := session.propagationCounterSnapshot().Totals

		result, err = session.Run(context.Background())
		if !errors.Is(err, ErrUnsupportedRuntime) {
			t.Fatalf("second Run error = %v, want ErrUnsupportedRuntime", err)
		}
		if result.Status != RunFailed {
			t.Fatalf("second run status = %v, want %v", result.Status, RunFailed)
		}
		if result.Fired != 0 {
			t.Fatalf("second run fired = %d, want 0", result.Fired)
		}
		afterCounters := session.propagationCounterSnapshot().Totals
		if got, want := afterCounters.FullAgendaReconciles, beforeCounters.FullAgendaReconciles; got != want {
			t.Fatalf("second run full agenda reconciles = %d, want unchanged %d", got, want)
		}
		if got, want := afterCounters.WholeTerminalScans, beforeCounters.WholeTerminalScans; got != want {
			t.Fatalf("second run whole terminal scans = %d, want unchanged %d", got, want)
		}
		if got, want := actionsSeen, []string{"assert-audit", "fail"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("action order = %#v, want %#v", got, want)
		}
	})

	t.Run("supported single delta canceled during delta apply", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "input",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(input): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "audit",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(audit): %v", err)
		}

		var actionsSeen []string
		if err := workspace.AddAction(ActionSpec{
			Name: "assert-audit",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "assert-audit")
				_, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"id": "a-1"}))
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(assert-audit): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record",
			Fn: func(ActionContext) error {
				actionsSeen = append(actionsSeen, "record")
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(record): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "input-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "input", Target: TemplateKeyFact(TemplateKey("input"))},
			},
			Actions: []RuleActionSpec{{Name: "assert-audit"}},
		}); err != nil {
			t.Fatalf("AddRule(input-rule): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "audit-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "audit", Target: TemplateKeyFact(TemplateKey("audit"))},
			},
			Actions: []RuleActionSpec{{Name: "record"}},
		}); err != nil {
			t.Fatalf("AddRule(audit-rule): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		runCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		session, err := NewSession(
			revision,
			WithSessionID("run-supported-delta-cancel-session"),
			WithInitialFacts(SessionInitialFact{
				TemplateKey: TemplateKey("input"),
				Fields:      mustFields(t, map[string]any{"id": "i-1"}),
			}),
			WithEventListener(EventFunc(func(_ context.Context, event Event) error {
				if event.Type == EventFactAsserted && event.RuleID == RuleID("input-rule") {
					cancel()
				}
				return nil
			})),
		)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		result, err := session.Run(runCtx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context canceled", err)
		}
		if result.Status != RunCanceled {
			t.Fatalf("run status = %v, want %v", result.Status, RunCanceled)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		if session.runAgendaPending {
			t.Fatal("run delta remained pending after cancellation")
		}
		if !session.agendaDirty || session.agendaReady {
			t.Fatalf("agenda state after cancellation = dirty %v ready %v, want dirty not ready", session.agendaDirty, session.agendaReady)
		}

		result, err = session.Run(context.Background())
		if !errors.Is(err, ErrUnsupportedRuntime) {
			t.Fatalf("second Run error = %v, want ErrUnsupportedRuntime", err)
		}
		if result.Status != RunFailed {
			t.Fatalf("second run status = %v, want %v", result.Status, RunFailed)
		}
		if result.Fired != 0 {
			t.Fatalf("second run fired = %d, want 0", result.Fired)
		}
		if got, want := actionsSeen, []string{"assert-audit"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("action order = %#v, want %#v", got, want)
		}
	})

	t.Run("supported token update delta", func(t *testing.T) {
		workspace := NewWorkspace()
		trigger := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "trigger",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		})
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name:            "person",
			DuplicatePolicy: DuplicateAllow,
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString, Required: true},
				{Name: "note", Kind: ValueString, Required: true},
			},
		})

		var (
			session     *Session
			personID    FactID
			actionsSeen []string
		)
		mustAddAction(t, workspace, ActionSpec{
			Name: "refresh-person",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "refresh-person")
				result, err := ctx.Modify(personID, FactPatch{
					Set: mustFields(t, map[string]any{"note": "new"}),
				})
				if err != nil {
					return err
				}
				if result.Status != ModifyChanged {
					return errors.New("person was not modified")
				}
				if session.agendaDirty {
					return errors.New("supported token update dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("supported token update cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("supported token update did not record run delta")
				}
				snapshot := session.propagationCounterSnapshot()
				if snapshot.Totals.ModifyFastPathSkips != 1 {
					return errors.New("person modify did not use fast-path token update")
				}
				return nil
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name:         "record-person",
			Fn:           func(ActionContext) error { actionsSeen = append(actionsSeen, "record-person"); return nil },
			BindingReads: &ActionBindingReadSetSpec{},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "refresh-person-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "trigger", Target: TemplateKeyFact(trigger.Key())},
			},
			Actions: []RuleActionSpec{{Name: "refresh-person"}},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "active-person-rule",
			Conditions: []RuleConditionSpec{{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				}, Target: TemplateKeyFact(person.Key()),
			}},
			Actions: []RuleActionSpec{{Name: "record-person"}},
		})
		revision := mustCompileWorkspace(t, workspace)
		session = mustSession(t, revision, "run-supported-token-update-delta-session")
		inserted, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"id":     "p-1",
			"status": "active",
			"note":   "old",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate person: %v", err)
		}
		personID = inserted.Fact.ID()
		if _, err := session.AssertTemplate(context.Background(), trigger.Key(), mustFields(t, map[string]any{"id": "t-1"})); err != nil {
			t.Fatalf("AssertTemplate trigger: %v", err)
		}
		session.attachPropagationCounters()

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 2 {
			t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
		}
		if got, want := actionsSeen, []string{"refresh-person", "record-person"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("action order = %#v, want %#v", got, want)
		}
		if session.runAgendaPending {
			t.Fatal("run delta remained pending after token update run")
		}
		if session.agendaDirty || !session.agendaReady {
			t.Fatalf("agenda state after token update run = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
		}
	})

	t.Run("supported multi mutation coalesces", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(person): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "audit",
			Fields: []FieldSpec{
				{Name: "kind", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(audit): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "obsolete",
			Fields: []FieldSpec{
				{Name: "kind", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(obsolete): %v", err)
		}

		var (
			session       *Session
			actionsSeen   []string
			assertResult  AssertResult
			modifyResult  ModifyResult
			retractResult RetractResult
			personID      FactID
			obsoleteID    FactID
			auditID       FactID
		)

		if err := workspace.AddAction(ActionSpec{
			Name: "assert-audit",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "assert-audit")
				result, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{
					"kind": "created",
				}))
				assertResult = result
				if err == nil {
					auditID = result.Fact.ID()
				}
				if session.agendaDirty {
					return errors.New("supported assert dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("supported assert cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("supported assert did not record run delta")
				}
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(assert-audit): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "promote-person",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "promote-person")
				binding, ok := ctx.Binding("person")
				if !ok {
					return errors.New("missing person binding")
				}
				personID = binding.ID()
				result, err := ctx.Modify(binding.ID(), FactPatch{
					Set: mustFields(t, map[string]any{"status": "done"}),
				})
				modifyResult = result
				if session.agendaDirty {
					return errors.New("supported modify dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("supported modify cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("supported modify did not record run delta")
				}
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(promote-person): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "retire-obsolete",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "retire-obsolete")
				binding, ok := ctx.Binding("obsolete")
				if !ok {
					return errors.New("missing obsolete binding")
				}
				obsoleteID = binding.ID()
				result, err := ctx.Retract(binding.ID())
				retractResult = result
				if session.agendaDirty {
					return errors.New("supported retract dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("supported retract cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("supported retract did not record run delta")
				}
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(retire-obsolete): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record-audit",
			Fn:   func(ActionContext) error { actionsSeen = append(actionsSeen, "record-audit"); return nil },
		}); err != nil {
			t.Fatalf("AddAction(record-audit): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record-done",
			Fn:   func(ActionContext) error { actionsSeen = append(actionsSeen, "record-done"); return nil },
		}); err != nil {
			t.Fatalf("AddAction(record-done): %v", err)
		}

		if err := workspace.AddRule(RuleSpec{
			Name: "pending-person",
			Conditions: []RuleConditionSpec{
				{
					Binding: "person",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
					}, Target: TemplateKeyFact(TemplateKey("person")),
				},
				{
					Binding: "obsolete", Target: TemplateKeyFact(TemplateKey("obsolete")),
				},
			},
			Actions: []RuleActionSpec{
				{Name: "assert-audit"},
				{Name: "promote-person"},
				{Name: "retire-obsolete"},
			},
		}); err != nil {
			t.Fatalf("AddRule(pending-person): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "audit-created",
			Conditions: []RuleConditionSpec{
				{
					Binding: "audit",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "kind", Operator: FieldConstraintEqual, Value: mustValue(t, "created")},
					}, Target: TemplateKeyFact(TemplateKey("audit")),
				},
			},
			Actions: []RuleActionSpec{{Name: "record-audit"}},
		}); err != nil {
			t.Fatalf("AddRule(audit-created): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "person-done",
			Conditions: []RuleConditionSpec{
				{
					Binding: "person",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "done")},
					}, Target: TemplateKeyFact(TemplateKey("person")),
				},
			},
			Actions: []RuleActionSpec{{Name: "record-done"}},
		}); err != nil {
			t.Fatalf("AddRule(person-done): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		collector := &testEventCollector{}
		session, err = NewSession(revision, WithSessionID("run-supported-delta-session"), WithEventListener(collector))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{
			"name":   "Ada",
			"status": "pending",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}
		if _, err := session.AssertTemplate(context.Background(), TemplateKey("obsolete"), mustFields(t, map[string]any{
			"kind": "stale",
		})); err != nil {
			t.Fatalf("AssertTemplate(obsolete): %v", err)
		}

		runEventOffset := len(collector.Events())
		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 3 {
			t.Fatalf("run fired = %d, want 3", result.Fired)
		}
		if got, want := actionsSeen, []string{"assert-audit", "promote-person", "retire-obsolete"}; len(got) < len(want) {
			t.Fatalf("action count = %d, want at least %d (%#v)", len(got), len(want), got)
		} else {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("leading action order = %#v, want prefix %#v", got, want)
				}
			}
			remaining := map[string]int{}
			for _, name := range got[len(want):] {
				remaining[name]++
			}
			if len(remaining) != 2 || remaining["record-audit"] != 1 || remaining["record-done"] != 1 {
				t.Fatalf("follow-on actions = %#v, want record-audit and record-done once each", got[len(want):])
			}
		}
		if assertResult.Status != AssertInserted {
			t.Fatalf("assert result status = %v, want %v", assertResult.Status, AssertInserted)
		}
		if modifyResult.Status != ModifyChanged {
			t.Fatalf("modify result status = %v, want %v", modifyResult.Status, ModifyChanged)
		}
		if retractResult.Status != RetractRemoved {
			t.Fatalf("retract result status = %v, want %v", retractResult.Status, RetractRemoved)
		}
		if assertResult.Delta == nil || modifyResult.Delta == nil || retractResult.Delta == nil {
			t.Fatalf("expected action deltas for supported run: %#v %#v %#v", assertResult, modifyResult, retractResult)
		}
		if assertResult.Delta.RuleID == "" || modifyResult.Delta.RuleID == "" || retractResult.Delta.RuleID == "" {
			t.Fatalf("supported action deltas missing rule metadata: %#v %#v %#v", assertResult.Delta, modifyResult.Delta, retractResult.Delta)
		}
		if personID != personFact.Fact.ID() {
			t.Fatalf("person binding ID = %q, want %q", personID, personFact.Fact.ID())
		}
		if obsoleteID.IsZero() {
			t.Fatal("obsolete binding ID is zero")
		}
		if auditID.IsZero() {
			t.Fatal("audit fact ID is zero")
		}

		after := mustSnapshot(t, context.Background(), session)
		if got, ok := after.Fact(personFact.Fact.ID()); !ok {
			t.Fatalf("missing person fact %q", personFact.Fact.ID())
		} else if value := got.Fields()["status"]; !value.Equal(mustValue(t, "done")) {
			t.Fatalf("person status = %v, want done", value)
		}
		if _, ok := after.Fact(obsoleteID); ok {
			t.Fatalf("obsolete fact %q remained after retract", obsoleteID)
		}
		if _, ok := after.Fact(auditID); !ok {
			t.Fatalf("audit fact %q missing after assert", auditID)
		}
		events := collector.Events()[runEventOffset:]
		var factEvents []EventType
		for _, event := range events {
			switch event.Type {
			case EventFactAsserted, EventFactModified, EventFactRetracted:
				factEvents = append(factEvents, event.Type)
			}
		}
		wantFactEvents := []EventType{EventFactAsserted, EventFactModified, EventFactRetracted}
		if len(factEvents) != len(wantFactEvents) {
			t.Fatalf("fact event count = %d, want %d (%#v)", len(factEvents), len(wantFactEvents), factEvents)
		}
		for i := range wantFactEvents {
			if factEvents[i] != wantFactEvents[i] {
				t.Fatalf("fact event order = %#v, want %#v", factEvents, wantFactEvents)
			}
		}
	})

	t.Run("supported transient add-remove pair", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "trigger",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(trigger): %v", err)
		}
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "temp",
			Fields: []FieldSpec{
				{Name: "id", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(temp): %v", err)
		}

		var (
			session      *Session
			tempID       FactID
			actionsSeen  []string
			tempFired    bool
			tempFireSeen int
		)
		if err := workspace.AddAction(ActionSpec{
			Name: "assert-temp",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "assert-temp")
				result, err := ctx.AssertTemplate(TemplateKey("temp"), mustFields(t, map[string]any{"id": "tmp-1"}))
				if err != nil {
					return err
				}
				if result.Status != AssertInserted {
					return errors.New("temp was not inserted")
				}
				tempID = result.Fact.ID()
				if session.agendaDirty {
					return errors.New("supported assert dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("supported assert cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("supported assert did not record run delta")
				}
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(assert-temp): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "retract-temp",
			Fn: func(ctx ActionContext) error {
				actionsSeen = append(actionsSeen, "retract-temp")
				if tempID.IsZero() {
					return errors.New("temp ID is zero")
				}
				result, err := ctx.Retract(tempID)
				if err != nil {
					return err
				}
				if result.Status != RetractRemoved {
					return errors.New("temp was not retracted")
				}
				if session.agendaDirty {
					return errors.New("supported retract dirtied agenda")
				}
				if !session.agendaReady {
					return errors.New("supported retract cleared agenda readiness")
				}
				if !session.runAgendaPending {
					return errors.New("supported retract did not record run delta")
				}
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(retract-temp): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record-temp",
			Fn: func(ActionContext) error {
				tempFired = true
				tempFireSeen++
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(record-temp): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "trigger-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "trigger", Target: TemplateKeyFact(TemplateKey("trigger"))},
			},
			Actions: []RuleActionSpec{{Name: "assert-temp"}, {Name: "retract-temp"}},
		}); err != nil {
			t.Fatalf("AddRule(trigger-rule): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "temp-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "temp", Target: TemplateKeyFact(TemplateKey("temp"))},
			},
			Actions: []RuleActionSpec{{Name: "record-temp"}},
		}); err != nil {
			t.Fatalf("AddRule(temp-rule): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		session, err = NewSession(
			revision,
			WithSessionID("run-supported-transient-add-remove-session"),
			WithInitialFacts(SessionInitialFact{
				TemplateKey: TemplateKey("trigger"),
				Fields:      mustFields(t, map[string]any{"id": "trigger-1"}),
			}),
		)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		if got, want := actionsSeen, []string{"assert-temp", "retract-temp"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("action order = %#v, want %#v", got, want)
		}
		if tempFired || tempFireSeen != 0 {
			t.Fatalf("temp rule fired unexpectedly: fired=%v count=%d", tempFired, tempFireSeen)
		}
		if session.runAgendaPending {
			t.Fatal("run delta remained pending after successful run")
		}
		if session.agendaDirty || !session.agendaReady {
			t.Fatalf("agenda state after run = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
		}
		after := mustSnapshot(t, context.Background(), session)
		if _, ok := after.Fact(tempID); ok {
			t.Fatalf("temp fact %q remained after retract", tempID)
		}
	})

	t.Run("name targets", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "seed-audit",
			Fn: func(ctx ActionContext) error {
				_, err := ctx.Assert("audit", mustFields(t, map[string]any{"kind": "created"}))
				return err
			},
		}); err != nil {
			t.Fatalf("AddAction(seed-audit): %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "record",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction(record): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "seed-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "seed", Target: DynamicFact("seed")},
			},
			Actions: []RuleActionSpec{{Name: "seed-audit"}},
		}); err != nil {
			t.Fatalf("AddRule(seed-rule): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "audit-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "audit", Target: DynamicFact("audit")},
			},
			Actions: []RuleActionSpec{{Name: "record"}},
		}); err != nil {
			t.Fatalf("AddRule(audit-rule): %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		collector := &testEventCollector{}
		session, err := NewSession(revision, WithSessionID("run-unsupported-delta-session"), WithEventListener(collector))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
			t.Fatalf("session Rete runtime = %#v, want supported incremental agenda", session.rete)
		}
		if _, err := session.Assert(context.Background(), "seed", mustFields(t, map[string]any{"kind": "seed"})); err != nil {
			t.Fatalf("Assert(seed): %v", err)
		}
		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Fired != 2 {
			t.Fatalf("run fired = %d, want 2", result.Fired)
		}
	})
}

func TestSessionRunActionFailureStopsLaterActionsAndEmitsFailureEvent(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var (
		actionsSeen    []string
		assertResult   AssertResult
		templateResult AssertResult
		retractResult  RetractResult
		terminalCalled bool
		personBinding  FactSnapshot
		terminalErr    = errors.New("stop actions")
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "assert-dynamic",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "assert-dynamic")
			result, err := ctx.Assert("note", mustFields(t, map[string]any{"kind": "dynamic"}))
			assertResult = result
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(assert-dynamic): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "assert-template",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "assert-template")
			result, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"kind": "template"}))
			templateResult = result
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(assert-template): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "retract",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "retract")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			personBinding = binding
			result, err := ctx.Retract(binding.ID())
			retractResult = result
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(retract): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "fail",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "fail")
			return terminalErr
		},
	}); err != nil {
		t.Fatalf("AddAction(fail): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "unexpected",
		Fn: func(ActionContext) error {
			terminalCalled = true
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(unexpected): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "action-failure-rule",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person", Target: TemplateKeyFact(TemplateKey("person")),
			},
		},
		Actions: []RuleActionSpec{
			{Name: "assert-dynamic"},
			{Name: "assert-template"},
			{Name: "retract"},
			{Name: "fail"},
			{Name: "unexpected"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("run-failure-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	result, err := session.Run(context.Background())
	if err == nil {
		t.Fatal("expected action failure")
	}
	if result.Status != RunActionFailed {
		t.Fatalf("run status = %v, want %v", result.Status, RunActionFailed)
	}
	if result.Fired != 1 {
		t.Fatalf("run fired = %d, want 1", result.Fired)
	}
	var failure *ActionFailureError
	if !errors.As(err, &failure) {
		t.Fatalf("expected ActionFailureError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrActionFailed) {
		t.Fatalf("run error = %v, want ErrActionFailed", err)
	}
	if failure.RunID != result.RunID || failure.ActionName != "fail" || failure.ActionIndex != 3 {
		t.Fatalf("action failure metadata = %#v", failure)
	}
	if failure.RuleID == "" || failure.RuleRevisionID == "" || failure.ActivationID == "" {
		t.Fatalf("action failure missing rule metadata: %#v", failure)
	}
	if !errors.Is(err, terminalErr) {
		t.Fatalf("action failure cause = %v, want %v", err, terminalErr)
	}
	if terminalCalled {
		t.Fatal("later action was called after error")
	}
	if got, want := actionsSeen, []string{"assert-dynamic", "assert-template", "retract", "fail"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
	if assertResult.Status != AssertInserted || assertResult.Delta == nil {
		t.Fatalf("assert result = %#v", assertResult)
	}
	if templateResult.Status != AssertInserted || templateResult.Delta == nil {
		t.Fatalf("template assert result = %#v", templateResult)
	}
	if retractResult.Status != RetractRemoved || retractResult.Delta == nil {
		t.Fatalf("retract result = %#v", retractResult)
	}
	if assertResult.Delta.RuleID != failure.RuleID || assertResult.Delta.RuleRevisionID != failure.RuleRevisionID || assertResult.Delta.ActivationID != failure.ActivationID {
		t.Fatalf("dynamic assert origin = %#v", assertResult.Delta)
	}
	if templateResult.Delta.RuleID != failure.RuleID || templateResult.Delta.RuleRevisionID != failure.RuleRevisionID || templateResult.Delta.ActivationID != failure.ActivationID {
		t.Fatalf("template assert origin = %#v", templateResult.Delta)
	}
	if retractResult.Delta.RuleID != failure.RuleID || retractResult.Delta.RuleRevisionID != failure.RuleRevisionID || retractResult.Delta.ActivationID != failure.ActivationID {
		t.Fatalf("retract origin = %#v", retractResult.Delta)
	}
	if personBinding.ID() != personFact.Fact.ID() {
		t.Fatalf("binding fact ID = %q, want %q", personBinding.ID(), personFact.Fact.ID())
	}

	events := collector.Events()
	var failed Event
	var fired Event
	foundFailed := false
	foundFired := false
	for _, event := range events {
		if event.Type == EventActionFailed {
			failed = event
			foundFailed = true
		}
		if event.Type == EventRuleFired {
			fired = event
			foundFired = true
		}
	}
	if !foundFailed {
		t.Fatal("action failed event missing")
	}
	if !foundFired {
		t.Fatal("rule fired event missing")
	}
	if failed.RunID != result.RunID {
		t.Fatalf("failed event run ID = %q, want %q", failed.RunID, result.RunID)
	}
	if failed.RuleID != failure.RuleID || failed.RuleRevisionID != failure.RuleRevisionID || failed.ActivationID != failure.ActivationID {
		t.Fatalf("failed event origin = %#v", failed)
	}
	if failed.ActionName != "fail" || failed.ActionIndex != 3 {
		t.Fatalf("failed event action metadata = %#v", failed)
	}
	if failed.Severity != EventSeverityError {
		t.Fatalf("failed event severity = %q, want %q", failed.Severity, EventSeverityError)
	}
	if !errors.Is(failed.Cause, terminalErr) {
		t.Fatalf("failed event cause = %v, want %v", failed.Cause, terminalErr)
	}
	if len(failed.FactIDs) != 1 || failed.FactIDs[0] != personFact.Fact.ID() {
		t.Fatalf("failed event fact IDs = %#v, want %q", failed.FactIDs, personFact.Fact.ID())
	}
	if fired.RunID != result.RunID {
		t.Fatalf("fired event run ID = %q, want %q", fired.RunID, result.RunID)
	}
}

func TestSessionRunCancellationBeforeSelectionDoesNotConsumeActivation(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(mark): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("run-cancel-selection-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: TemplateKey("person"),
			Fields:      mustFields(t, map[string]any{"name": "Ada"}),
		}),
		WithEventListener(collector),
		WithEventListener(EventFunc(func(_ context.Context, event Event) error {
			if event.Type == EventRuleActivated {
				cancel()
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	result, err := session.Run(cancelCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", err)
	}
	if result.Status != RunCanceled {
		t.Fatalf("run status = %v, want %v", result.Status, RunCanceled)
	}
	if result.Fired != 0 {
		t.Fatalf("run fired = %d, want 0", result.Fired)
	}
	if result.RunID.IsZero() {
		t.Fatal("run ID is zero")
	}
	pending := session.agenda.pendingActivations()
	if len(pending) != 1 {
		t.Fatalf("pending activations = %d, want 1", len(pending))
	}
	for _, event := range collector.Events() {
		if event.Type == EventRuleFired {
			t.Fatalf("unexpected rule fired event: %#v", event)
		}
	}
}

func TestSessionRunCancellationBeforeLaterActionReturnsCanceled(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var (
		actionsSeen []string
		cancel      context.CancelFunc
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "first",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "first")
			cancel()
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(first): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "second",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "second")
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(second): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "first"}, {Name: "second"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	cancel = runCancel
	session := mustSession(t, revision, "run-cancel-later-session")
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	result, err := session.Run(runCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context canceled", err)
	}
	if result.Status != RunCanceled {
		t.Fatalf("run status = %v, want %v", result.Status, RunCanceled)
	}
	if result.Fired != 1 {
		t.Fatalf("run fired = %d, want 1", result.Fired)
	}
	if got, want := actionsSeen, []string{"first"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
}

func TestSessionRunRejectsRecursiveAndOverlappingRuns(t *testing.T) {
	t.Run("recursive", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(person): %v", err)
		}

		var innerResult RunResult
		var innerErr error
		var session *Session
		if err := workspace.AddAction(ActionSpec{
			Name: "recurse",
			Fn: func(ActionContext) error {
				innerResult, innerErr = session.Run(context.Background())
				if !errors.Is(innerErr, ErrConcurrencyMisuse) {
					return innerErr
				}
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(recurse): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "person-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
			},
			Actions: []RuleActionSpec{{Name: "recurse"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		session = mustSession(t, revision, "run-recursive-session")
		if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("outer Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("outer run status = %v, want %v", result.Status, RunCompleted)
		}
		if innerErr == nil || !errors.Is(innerErr, ErrConcurrencyMisuse) {
			t.Fatalf("inner run error = %v, want ErrConcurrencyMisuse", innerErr)
		}
		if innerResult.Status != RunConcurrencyMisuse {
			t.Fatalf("inner run status = %v, want %v", innerResult.Status, RunConcurrencyMisuse)
		}
	})

	t.Run("overlapping", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(person): %v", err)
		}

		started := make(chan struct{})
		release := make(chan struct{})
		if err := workspace.AddAction(ActionSpec{
			Name: "block",
			Fn: func(ActionContext) error {
				close(started)
				<-release
				return nil
			},
		}); err != nil {
			t.Fatalf("AddAction(block): %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "person-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
			},
			Actions: []RuleActionSpec{{Name: "block"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		session := mustSession(t, revision, "run-overlap-session")
		if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}

		firstDone := make(chan struct{})
		go func() {
			defer close(firstDone)
			if _, err := session.Run(context.Background()); err != nil {
				t.Errorf("outer Run: %v", err)
			}
		}()

		<-started
		secondResult, secondErr := session.Run(context.Background())
		if !errors.Is(secondErr, ErrConcurrencyMisuse) {
			t.Fatalf("overlapping run error = %v, want ErrConcurrencyMisuse", secondErr)
		}
		if secondResult.Status != RunConcurrencyMisuse {
			t.Fatalf("overlapping run status = %v, want %v", secondResult.Status, RunConcurrencyMisuse)
		}
		close(release)
		<-firstDone
	})
}

func TestSessionRunQueuesExternalMutationsBetweenActivations(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var (
		session     *Session
		personID    FactID
		actionsSeen []string
	)

	started := make(chan struct{})
	release := make(chan struct{})
	if err := workspace.AddAction(ActionSpec{
		Name: "pause",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "pause")
			close(started)
			<-release
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(pause): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "record-audit",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "audit")
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(record-audit): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "record-done",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "done")
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(record-done): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "pending-person",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
				}, Target: TemplateKeyFact(TemplateKey("person")),
			},
		},
		Actions: []RuleActionSpec{{Name: "pause"}},
	}); err != nil {
		t.Fatalf("AddRule(pending-person): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "audit-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", Target: TemplateKeyFact(TemplateKey("audit"))},
		},
		Actions: []RuleActionSpec{{Name: "record-audit"}},
	}); err != nil {
		t.Fatalf("AddRule(audit-rule): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "done-person",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "done")},
				}, Target: TemplateKeyFact(TemplateKey("person")),
			},
		},
		Actions: []RuleActionSpec{{Name: "record-done"}},
	}); err != nil {
		t.Fatalf("AddRule(done-person): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	collector := &testEventCollector{}
	session, err = NewSession(revision, WithSessionID("run-external-mutation-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	asserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{
		"name":   "Ada",
		"status": "pending",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	personID = asserted.Fact.ID()
	session.attachPropagationCounters()

	type assertOutcome struct {
		result AssertResult
		err    error
	}
	type modifyOutcome struct {
		result ModifyResult
		err    error
	}

	runDone := make(chan struct{})
	var runResult RunResult
	var runErr error
	go func() {
		defer close(runDone)
		runResult, runErr = session.Run(context.Background())
	}()

	<-started

	assertDone := make(chan assertOutcome, 1)
	go func() {
		result, err := session.AssertTemplate(context.Background(), TemplateKey("audit"), mustFields(t, map[string]any{"kind": "queued"}))
		assertDone <- assertOutcome{result: result, err: err}
	}()
	waitForQueuedMutationCount(t, session, 1)

	modifyDone := make(chan modifyOutcome, 1)
	go func() {
		result, err := session.Modify(context.Background(), personID, FactPatch{
			Set: mustFields(t, map[string]any{"status": "done"}),
		})
		modifyDone <- modifyOutcome{result: result, err: err}
	}()
	waitForQueuedMutationCount(t, session, 2)

	resetResult, resetErr := session.Reset(context.Background())
	if !errors.Is(resetErr, ErrConcurrencyMisuse) || resetResult.Status != ResetConcurrencyMisuse {
		t.Fatalf("reset during run = (%v, %v), want concurrency misuse", resetResult.Status, resetErr)
	}

	select {
	case outcome := <-assertDone:
		t.Fatalf("queued assert completed before safe point: %#v", outcome)
	default:
	}
	select {
	case outcome := <-modifyDone:
		t.Fatalf("queued modify completed before safe point: %#v", outcome)
	default:
	}

	close(release)

	var assertedAudit assertOutcome
	select {
	case assertedAudit = <-assertDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued assert")
	}
	if assertedAudit.err != nil {
		t.Fatalf("queued assert: %v", assertedAudit.err)
	}
	if assertedAudit.result.Status != AssertInserted {
		t.Fatalf("queued assert status = %v, want %v", assertedAudit.result.Status, AssertInserted)
	}
	var modifiedPerson modifyOutcome
	select {
	case modifiedPerson = <-modifyDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued modify")
	}
	if modifiedPerson.err != nil {
		t.Fatalf("queued modify: %v", modifiedPerson.err)
	}
	if modifiedPerson.result.Status != ModifyChanged {
		t.Fatalf("queued modify status = %v, want %v", modifiedPerson.result.Status, ModifyChanged)
	}

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not complete")
	}
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if runResult.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", runResult.Status, RunCompleted)
	}
	if runResult.Fired != 3 {
		t.Fatalf("run fired = %d, want 3", runResult.Fired)
	}
	counters := session.propagationCounterSnapshot().Totals
	if got := counters.SteadyStateWholeTerminalScans; got != 0 {
		t.Fatalf("steady-state whole-terminal scans after queued mutations = %d, want 0", got)
	}
	if got := counters.FullAgendaReconciles; got != 0 {
		t.Fatalf("full agenda reconciles after queued mutations = %d, want 0", got)
	}
	if got := counters.AgendaDeltaApplications; got == 0 {
		t.Fatal("agenda delta applications after queued mutations = 0, want queued mutations to apply incrementally")
	}
	if session.rete == nil {
		t.Fatal("session Rete runtime is nil")
	}
	assertMatcherParity(t, session.revision, mustSnapshot(t, context.Background(), session), newNaiveMatcher(session.revision), session.rete)
	if got, want := actionsSeen, []string{"pause", "done", "audit"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}

	events := make([]EventType, 0)
	for _, event := range collector.Events() {
		if event.Type == EventFactAsserted || event.Type == EventFactModified {
			events = append(events, event.Type)
		}
	}
	if got, want := events, []EventType{EventFactAsserted, EventFactAsserted, EventFactModified}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("fact mutation event order = %#v, want %#v", got, want)
	}
}

func waitForQueuedMutationCount(t *testing.T, session *Session, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		session.mutationQueueMu.Lock()
		got := len(session.mutationQueue)
		session.mutationQueueMu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("queued mutations = %d, want %d", got, want)
		case <-ticker.C:
		}
	}
}
