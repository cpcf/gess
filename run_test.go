package gess

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
			{Binding: "person", TemplateKey: TemplateKey("person")},
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
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
				},
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
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
				},
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
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "done")},
				},
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
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
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
			{Binding: "person", TemplateKey: TemplateKey("person")},
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
			{Binding: "person", TemplateKey: TemplateKey("person")},
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
				{Binding: "person", TemplateKey: TemplateKey("person")},
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
				{Binding: "person", TemplateKey: TemplateKey("person")},
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
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "pending")},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "pause"}},
	}); err != nil {
		t.Fatalf("AddRule(pending-person): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "audit-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", TemplateKey: TemplateKey("audit")},
		},
		Actions: []RuleActionSpec{{Name: "record-audit"}},
	}); err != nil {
		t.Fatalf("AddRule(audit-rule): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "done-person",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "done")},
				},
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
