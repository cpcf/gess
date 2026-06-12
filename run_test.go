package gess

import (
	"context"
	"errors"
	"testing"
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
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
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

func TestSessionRunBlocksExternalMutationsDuringRun(t *testing.T) {
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
		session  *Session
		personID FactID
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "check-external",
		Fn: func(ActionContext) error {
			result, err := session.Assert(context.Background(), "note", mustFields(t, map[string]any{"kind": "note"}))
			if !errors.Is(err, ErrConcurrencyMisuse) || result.Status != AssertConcurrencyMisuse {
				return errors.New("assert should return concurrency misuse")
			}
			result2, err := session.AssertTemplate(context.Background(), TemplateKey("audit"), mustFields(t, map[string]any{"kind": "audit"}))
			if !errors.Is(err, ErrConcurrencyMisuse) || result2.Status != AssertConcurrencyMisuse {
				return errors.New("assert template should return concurrency misuse")
			}
			modifyResult, err := session.Modify(context.Background(), personID, FactPatch{
				Set: mustFields(t, map[string]any{"name": "Bob"}),
			})
			if !errors.Is(err, ErrConcurrencyMisuse) || modifyResult.Status != ModifyConcurrencyMisuse {
				return errors.New("modify should return concurrency misuse")
			}
			retractResult, err := session.Retract(context.Background(), personID)
			if !errors.Is(err, ErrConcurrencyMisuse) || retractResult.Status != RetractConcurrencyMisuse {
				return errors.New("retract should return concurrency misuse")
			}
			resetResult, err := session.Reset(context.Background())
			if !errors.Is(err, ErrConcurrencyMisuse) || resetResult.Status != ResetConcurrencyMisuse {
				return errors.New("reset should return concurrency misuse")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(check-external): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: TemplateKey("person"),
			},
		},
		Actions: []RuleActionSpec{{Name: "check-external"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err = NewSession(revision, WithSessionID("run-external-mutation-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	asserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	personID = asserted.Fact.ID()

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
}
