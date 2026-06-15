package gess

import (
	"context"
	"errors"
	"testing"
)

func TestSessionExecuteActivationActionsUsesDetachedBindingSnapshots(t *testing.T) {
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
		Name: "mutate",
		Fn: func(ctx ActionContext) error {
			if _, ok := ctx.Binding(""); ok {
				return errors.New("empty binding should not resolve")
			}

			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			boundFacts := ctx.BoundFacts()
			if len(boundFacts) != 1 {
				return errors.New("expected one bound fact")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("unexpected initial binding value")
			}
			if got := boundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("unexpected initial bound fact value")
			}

			bindingFields := binding.Fields()
			bindingFields["name"] = mustValue(t, "MUT")
			if got, ok := ctx.Binding("person"); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed after mutating returned fields map")
			}

			boundFactFields := boundFacts[0].Fields()
			boundFactFields["name"] = mustValue(t, "MUT")
			if got := ctx.BoundFacts(); len(got) != 1 || !got[0].Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("bound facts changed after mutating returned fields map")
			}

			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: mustFields(t, map[string]any{"name": "Grace"}),
			})
			if err != nil {
				return err
			}

			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed after modify")
			}
			if got := boundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("bound fact changed after modify")
			}
			if got, ok := ctx.Binding("person"); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("later binding read changed after modify")
			}
			if got := ctx.BoundFacts(); len(got) != 1 || !got[0].Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("later bound facts read changed after modify")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(mutate): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: TemplateKey("person")},
		},
		Actions: []RuleActionSpec{
			{Name: "mutate"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("action-detached-binding-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID("run:test-action-detached-binding"), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	after := mustSnapshot(t, context.Background(), session)
	if got := after.Facts()[0].Fields()["name"]; !got.Equal(mustValue(t, "Grace")) {
		t.Fatalf("session fact name after modify = %v, want Grace", got)
	}
	if !inserted.Inserted() {
		t.Fatalf("initial assert status = %v, want inserted", inserted.Status)
	}
}

func TestSessionExecuteActivationActionsKeepsBindingsStableAndRunsInOrder(t *testing.T) {
	collector := &testEventCollector{}
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
		actionsSeen       []string
		sessionID         SessionID
		rulesetID         RulesetID
		activationID      ActivationID
		ruleID            RuleID
		ruleRevisionID    RuleRevisionID
		generation        Generation
		initialBinding    FactSnapshot
		initialBoundFacts []FactSnapshot
		laterBinding      FactSnapshot
		laterBoundFacts   []FactSnapshot
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "capture",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "capture")
			sessionID = ctx.SessionID()
			rulesetID = ctx.RulesetID()
			activationID = ctx.ActivationID()
			ruleID = ctx.RuleID()
			ruleRevisionID = ctx.RuleRevisionID()
			generation = ctx.Generation()

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
			if _, ok := ctx.Binding("missing"); ok {
				return errors.New("unexpected missing binding")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("unexpected initial binding value")
			}
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
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed before modify")
			}

			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: mustFields(t, map[string]any{"name": "Grace"}),
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
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed after modify")
			}
			if got := laterBoundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
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
			{Binding: "person", TemplateKey: TemplateKey("person")},
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
	session, err := NewSession(revision, WithSessionID("action-order-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID("run:test-action-order"), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	if got, want := actionsSeen, []string{"capture", "mutate", "verify"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
	if sessionID != session.ID() {
		t.Fatalf("action context session ID = %q, want %q", sessionID, session.ID())
	}
	if rulesetID != revision.ID() {
		t.Fatalf("action context ruleset ID = %q, want %q", rulesetID, revision.ID())
	}
	if activationID != selected.id {
		t.Fatalf("action context activation ID = %q, want %q", activationID, selected.id)
	}
	if ruleID != selected.ruleID {
		t.Fatalf("action context rule ID = %q, want %q", ruleID, selected.ruleID)
	}
	if ruleRevisionID != selected.ruleRevisionID {
		t.Fatalf("action context rule revision ID = %q, want %q", ruleRevisionID, selected.ruleRevisionID)
	}
	if generation != selected.generation {
		t.Fatalf("action context generation = %d, want %d", generation, selected.generation)
	}
	if len(initialBoundFacts) != 1 || len(laterBoundFacts) != 1 {
		t.Fatalf("bound facts lengths = %d and %d, want 1 and 1", len(initialBoundFacts), len(laterBoundFacts))
	}
	if initialBinding.ID() != laterBinding.ID() {
		t.Fatalf("binding fact ID changed: %q vs %q", initialBinding.ID(), laterBinding.ID())
	}
	if got := initialBoundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("initial bound fact name = %v, want Ada", got)
	}
	if got := laterBoundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("later bound fact name = %v, want Ada", got)
	}
	if got := laterBinding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("later binding name = %v, want Ada", got)
	}

	after := mustSnapshot(t, context.Background(), session)
	if got := after.Facts()[0].Fields()["name"]; !got.Equal(mustValue(t, "Grace")) {
		t.Fatalf("session fact name after modify = %v, want Grace", got)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
	if events[0].Type != EventFactAsserted {
		t.Fatalf("first event type = %q, want %q", events[0].Type, EventFactAsserted)
	}
	if events[0].Delta == nil || events[0].Delta.RuleID != "" || events[0].Delta.RuleRevisionID != "" || events[0].Delta.ActivationID != "" {
		t.Fatalf("external assert event carried origin metadata: %#v", events[0].Delta)
	}
	if events[1].Type != EventRuleActivated {
		t.Fatalf("second event type = %q, want %q", events[1].Type, EventRuleActivated)
	}
	if events[2].Type != EventFactModified {
		t.Fatalf("third event type = %q, want %q", events[2].Type, EventFactModified)
	}
	if events[3].Type != EventRuleActivated {
		t.Fatalf("fourth event type = %q, want %q", events[3].Type, EventRuleActivated)
	}
	if events[2].RuleID != selected.ruleID || events[2].RuleRevisionID != selected.ruleRevisionID || events[2].ActivationID != selected.id {
		t.Fatalf("modify event origin = %#v", events[2])
	}
	if events[2].Delta == nil {
		t.Fatal("modify event missing delta")
	}
	if events[2].Delta.RuleID != selected.ruleID || events[2].Delta.RuleRevisionID != selected.ruleRevisionID || events[2].Delta.ActivationID != selected.id {
		t.Fatalf("modify delta origin = %#v", events[2].Delta)
	}
	if !inserted.Inserted() {
		t.Fatalf("initial assert status = %v, want inserted", inserted.Status)
	}
}

func TestSessionExecuteActivationActionsAssertTemplateUsesSlotBackedInsertion(t *testing.T) {
	collector := &testEventCollector{}
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name:            "gate",
		Fields:          []FieldSpec{{Name: "id", Kind: ValueString}},
		DuplicatePolicy: DuplicateAllow,
	}); err != nil {
		t.Fatalf("AddTemplate(gate): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name:              "audit",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active", AllowedValues: []any{"active", "pending"}},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var (
		firstResult  AssertResult
		secondResult AssertResult
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "assert-audit",
		Fn: func(ctx ActionContext) error {
			result, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"id": "audit-1"}))
			firstResult = result
			if err != nil {
				return err
			}
			duplicate, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{
				"id":     "audit-1",
				"status": "active",
			}))
			secondResult = duplicate
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(assert-audit): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(noop): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: TemplateKey("person")},
		},
		Actions: []RuleActionSpec{{Name: "assert-audit"}, {Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule(person-rule): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "audit-gate-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", TemplateKey: TemplateKey("audit")},
			{Binding: "gate", TemplateKey: TemplateKey("gate")},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule(audit-gate-rule): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("action-slot-assert-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	eventsBefore := len(collector.Events())
	if err := session.executeActivationActions(context.Background(), RunID("run:test-slot-assert"), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	if got, want := len(collector.Events()), eventsBefore+1; got != want {
		t.Fatalf("events after action assert = %d, want %d", got, want)
	}
	createdEvent := collector.Events()[eventsBefore]
	if createdEvent.Type != EventFactAsserted {
		t.Fatalf("created event type = %v, want %v", createdEvent.Type, EventFactAsserted)
	}
	if createdEvent.Delta == nil || createdEvent.Delta.ActivationID != selected.id || createdEvent.Delta.RuleID != selected.ruleID || createdEvent.Delta.RuleRevisionID != selected.ruleRevisionID {
		t.Fatalf("created event delta origin = %#v", createdEvent.Delta)
	}

	if !firstResult.Inserted() {
		t.Fatalf("first assert result = %v, want inserted", firstResult.Status)
	}
	if firstResult.Delta == nil || firstResult.Delta.ActivationID != selected.id || firstResult.Delta.RuleID != selected.ruleID || firstResult.Delta.RuleRevisionID != selected.ruleRevisionID {
		t.Fatalf("first assert delta origin = %#v", firstResult.Delta)
	}
	if secondResult.Status != AssertExisting {
		t.Fatalf("duplicate assert status = %v, want %v", secondResult.Status, AssertExisting)
	}
	if secondResult.DuplicateKey != firstResult.DuplicateKey {
		t.Fatalf("duplicate key mismatch: %q != %q", secondResult.DuplicateKey, firstResult.DuplicateKey)
	}

	internal := session.factsByID[firstResult.Fact.ID()]
	if internal.fields != nil {
		t.Fatal("slot-backed action fact should not retain canonical fields")
	}
	if got := len(internal.fieldSlots); got == 0 {
		t.Fatal("slot-backed action fact should have slot storage")
	}

	fields := firstResult.Fact.Fields()
	fields["status"] = mustValue(t, "mutated")
	if got, ok := firstResult.Fact.Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("action fact fields map was not defensive: (%v, %v)", got, ok)
	}
	if got, ok := firstResult.Fact.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("action fact status presence = (%v, %v), want default", got, ok)
	}
	if personFact.Fact.ID().IsZero() {
		t.Fatal("person fact should have been inserted")
	}
}

func TestSessionExecuteActivationActionsSupportsActionMutationsAndStopsOnError(t *testing.T) {
	collector := &testEventCollector{}
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
		actionsSeen     []string
		assertResult    AssertResult
		templateResult  AssertResult
		retractResult   RetractResult
		terminalCalled  bool
		personBinding   FactSnapshot
		terminalErr     = errors.New("stop actions")
		selectionID     ActivationID
		selectionRuleID RuleID
		selectionRevID  RuleRevisionID
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
		Name: "action-mutations",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: TemplateKey("person")},
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
	session, err := NewSession(revision, WithSessionID("action-mutation-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}
	selectionID = selected.id
	selectionRuleID = selected.ruleID
	selectionRevID = selected.ruleRevisionID

	err = session.executeActivationActions(context.Background(), RunID("run:test-action-failure"), selected)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("executeActivationActions error = %v, want %v", err, terminalErr)
	}
	var failure *ActionFailureError
	if !errors.As(err, &failure) {
		t.Fatalf("expected ActionFailureError, got %T: %v", err, err)
	}
	if failure.RunID != RunID("run:test-action-failure") || failure.RuleID != selectionRuleID || failure.RuleRevisionID != selectionRevID || failure.ActivationID != selectionID || failure.ActionName != "fail" || failure.ActionIndex != 3 {
		t.Fatalf("action failure metadata = %#v", failure)
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
	if assertResult.Delta.RuleID != selectionRuleID || assertResult.Delta.RuleRevisionID != selectionRevID || assertResult.Delta.ActivationID != selectionID {
		t.Fatalf("dynamic assert origin = %#v", assertResult.Delta)
	}
	if templateResult.Delta.RuleID != selectionRuleID || templateResult.Delta.RuleRevisionID != selectionRevID || templateResult.Delta.ActivationID != selectionID {
		t.Fatalf("template assert origin = %#v", templateResult.Delta)
	}
	if assertResult.Fact.Support().State != FactSupportStated || templateResult.Fact.Support().State != FactSupportStated {
		t.Fatalf("action assertion support states = (%q, %q), want stated", assertResult.Fact.Support().State, templateResult.Fact.Support().State)
	}
	if retractResult.Delta.RuleID != selectionRuleID || retractResult.Delta.RuleRevisionID != selectionRevID || retractResult.Delta.ActivationID != selectionID {
		t.Fatalf("retract origin = %#v", retractResult.Delta)
	}
	if personBinding.ID() != personFact.Fact.ID() {
		t.Fatalf("binding fact ID = %q, want %q", personBinding.ID(), personFact.Fact.ID())
	}

	events := collector.Events()
	if got, want := len(events), 5; got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
	if events[0].Type != EventFactAsserted {
		t.Fatalf("first event type = %q, want %q", events[0].Type, EventFactAsserted)
	}
	if events[0].Delta == nil || events[0].Delta.RuleID != "" || events[0].Delta.RuleRevisionID != "" || events[0].Delta.ActivationID != "" {
		t.Fatalf("external assert event carried origin metadata: %#v", events[0].Delta)
	}
	if events[1].Type != EventRuleActivated || events[2].Type != EventFactAsserted || events[3].Type != EventFactAsserted || events[4].Type != EventFactRetracted {
		t.Fatalf("event types = %#v", []EventType{events[1].Type, events[2].Type, events[3].Type, events[4].Type})
	}
	for i := 2; i < len(events); i++ {
		if events[i].RuleID != selectionRuleID || events[i].RuleRevisionID != selectionRevID || events[i].ActivationID != selectionID {
			t.Fatalf("event %d origin = %#v", i, events[i])
		}
		if events[i].Delta == nil {
			t.Fatalf("event %d missing delta", i)
		}
		if events[i].Delta.RuleID != selectionRuleID || events[i].Delta.RuleRevisionID != selectionRevID || events[i].Delta.ActivationID != selectionID {
			t.Fatalf("event %d delta origin = %#v", i, events[i].Delta)
		}
	}

	after := mustSnapshot(t, context.Background(), session)
	if after.Len() != 2 {
		t.Fatalf("snapshot length after action mutations = %d, want 2", after.Len())
	}
	if _, ok := after.Fact(personFact.Fact.ID()); ok {
		t.Fatalf("snapshot still contained retracted person fact %q", personFact.Fact.ID())
	}
	if got := after.FactsByName("note"); len(got) != 1 {
		t.Fatalf("snapshot note facts = %d, want 1", len(got))
	}
	if got := after.FactsByTemplateKey(TemplateKey("audit")); len(got) != 1 {
		t.Fatalf("snapshot audit facts = %d, want 1", len(got))
	}
}

func TestSessionExecuteActivationActionsRejectsStaleBindings(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	called := false
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			called = true
			return nil
		},
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
	session := mustSession(t, revision, "action-stale-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}
	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}

	err = session.executeActivationActions(context.Background(), RunID("run:test-stale"), selected)
	if !errors.Is(err, ErrMatcher) {
		t.Fatalf("executeActivationActions error = %v, want ErrMatcher", err)
	}
	if called {
		t.Fatal("action ran for stale activation")
	}
}
