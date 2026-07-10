package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSessionApplyRulesetAddsRuleCreatesActivationAndKeepsInitialFactsValidForReset(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})

	revision1 := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(
		revision1,
		WithSessionID("apply-add-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: template.Key(),
			Fields:      mustFields(t, map[string]any{"name": "Ada"}),
		}),
		WithEventListener(collector),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	workspace2 := NewWorkspace()
	mustAddTemplate(t, workspace2, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace2, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace2, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision2 := mustCompileWorkspace(t, workspace2)
	rule2, ok := revision2.Rule("match-person")
	if !ok {
		t.Fatal("expected replacement rule in compiled revision")
	}

	result, err := session.ApplyRuleset(context.Background(), revision2)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	if result.PreviousRulesetID != revision1.ID() {
		t.Fatalf("previous ruleset id = %q, want %q", result.PreviousRulesetID, revision1.ID())
	}
	if result.CurrentRulesetID != revision2.ID() {
		t.Fatalf("current ruleset id = %q, want %q", result.CurrentRulesetID, revision2.ID())
	}
	if len(result.AddedRuleRevisions) != 1 || result.AddedRuleRevisions[0].RuleID != rule2.ID() || result.AddedRuleRevisions[0].RevisionID != rule2.RevisionID() {
		t.Fatalf("added revisions = %#v, want %q/%q", result.AddedRuleRevisions, rule2.ID(), rule2.RevisionID())
	}
	if len(result.RemovedRuleRevisions) != 0 || len(result.ReplacedRuleRevisions) != 0 || len(result.UnchangedRuleRevisions) != 0 {
		t.Fatalf("unexpected apply metadata: removed=%#v replaced=%#v unchanged=%#v", result.RemovedRuleRevisions, result.ReplacedRuleRevisions, result.UnchangedRuleRevisions)
	}
	if session.RulesetID() != revision2.ID() {
		t.Fatalf("session ruleset id = %q, want %q", session.RulesetID(), revision2.ID())
	}
	if snapshot := mustSnapshot(t, context.Background(), session); snapshot.RulesetID() != revision2.ID() {
		t.Fatalf("snapshot ruleset id = %q, want %q", snapshot.RulesetID(), revision2.ID())
	}
	pending := session.agendaDriver.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations = %d, want %d", got, want)
	}
	if pending[0].ruleRevisionID != rule2.RevisionID() {
		t.Fatalf("pending activation revision = %q, want %q", pending[0].ruleRevisionID, rule2.RevisionID())
	}

	resetResult, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if resetResult.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", resetResult.Status, ResetApplied)
	}
	if session.RulesetID() != revision2.ID() {
		t.Fatalf("ruleset id after reset = %q, want %q", session.RulesetID(), revision2.ID())
	}
	resetSnapshot := mustSnapshot(t, context.Background(), session)
	if resetSnapshot.RulesetID() != revision2.ID() {
		t.Fatalf("reset snapshot ruleset id = %q, want %q", resetSnapshot.RulesetID(), revision2.ID())
	}
	if got, want := resetSnapshot.Len(), 1; got != want {
		t.Fatalf("reset snapshot length = %d, want %d", got, want)
	}
	if got, want := resetSnapshot.Facts()[0].Fields()["name"], mustValue(t, "Ada"); !got.Equal(want) {
		t.Fatalf("reset snapshot name = %v, want %v", got, want)
	}
}

func TestSessionApplyRulesetConsumesPendingInitialLifecycleBeforeTransition(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "pending-apply-person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "existing-person",
		Conditions: []RuleConditionSpec{{Binding: "person", Target: TemplateKeyFact(person.Key())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	first := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(
		first,
		WithEventListener(collector),
		WithInitialFacts(SessionInitialFact{TemplateKey: person.Key(), Fields: mustFields(t, map[string]any{"name": "Ada"})}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.agendaDriver.isReady() {
		t.Fatal("listener-backed initial agenda unexpectedly ready before apply")
	}
	session.attachPropagationCounters()

	mustAddRule(t, workspace, RuleSpec{
		Name:       "added-person",
		Conditions: []RuleConditionSpec{{Binding: "person", Target: TemplateKeyFact(person.Key())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	second := mustCompileWorkspace(t, workspace)
	result, err := session.ApplyRuleset(context.Background(), second)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	if got, want := len(session.agendaDriver.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations after apply = %d, want %d", got, want)
	}
	if got, want := len(collector.Events()), 2; got != want {
		t.Fatalf("activation events after apply = %d, want %d", got, want)
	}
	counters := session.propagationCounterSnapshot().Totals
	if counters.OracleStyleMatchRequests != 0 || counters.WholeTerminalScans != 0 || counters.FullAgendaReconciles != 0 {
		t.Fatalf("ruleset apply used whole-terminal reconcile: %+v", counters)
	}
}

func TestSessionApplyRulesetRemovesPendingActivationsWithoutTouchingFacts(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision1 := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(revision1, WithSessionID("apply-remove-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), mustSnapshot(t, context.Background(), session)); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	if got := session.agendaDriver.agenda.pendingActivations(); len(got) != 1 {
		t.Fatalf("pending activations before apply = %d, want 1", len(got))
	}

	workspace2 := NewWorkspace()
	mustAddTemplate(t, workspace2, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	revision2 := mustCompileWorkspace(t, workspace2)

	result, err := session.ApplyRuleset(context.Background(), revision2)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	rule1, ok := revision1.Rule("match-person")
	if !ok {
		t.Fatal("expected original rule in compiled revision")
	}
	if len(result.RemovedRuleRevisions) != 1 || result.RemovedRuleRevisions[0].RuleID != rule1.ID() || result.RemovedRuleRevisions[0].RevisionID != rule1.RevisionID() {
		t.Fatalf("removed revisions = %#v, want %q/%q", result.RemovedRuleRevisions, rule1.ID(), rule1.RevisionID())
	}
	if len(result.AddedRuleRevisions) != 0 || len(result.ReplacedRuleRevisions) != 0 || len(result.UnchangedRuleRevisions) != 0 {
		t.Fatalf("unexpected apply metadata: added=%#v replaced=%#v unchanged=%#v", result.AddedRuleRevisions, result.ReplacedRuleRevisions, result.UnchangedRuleRevisions)
	}
	if got := session.agendaDriver.agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after remove = %#v, want none", got)
	}
	if got := session.agendaDriver.agenda.activationsByRuleRevisionID(rule1.RevisionID()); len(got) != 0 {
		t.Fatalf("old revision activations still indexed after remove: %#v", got)
	}
	if snapshot := mustSnapshot(t, context.Background(), session); snapshot.Len() != 1 {
		t.Fatalf("snapshot length after remove = %d, want 1", snapshot.Len())
	}
	if got := collector.Events(); len(got) != 3 {
		t.Fatalf("events after remove = %d, want 3", len(got))
	}
	if got := collector.Events()[2].Type; got != EventRuleDeactivated {
		t.Fatalf("final event type = %v, want %v", got, EventRuleDeactivated)
	}
	if got := collector.Events()[2].FactIDs[0]; got != inserted.Fact.ID() {
		t.Fatalf("deactivation event fact id = %q, want %q", got, inserted.Fact.ID())
	}
}

func TestSessionApplyRulesetReplacesRulePurgesOldActivationStateAndCreatesReplacement(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "match-person",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision1 := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision1, WithSessionID("apply-replace-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), mustSnapshot(t, context.Background(), session)); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	oldActivation, ok := session.agendaDriver.agenda.next()
	if !ok {
		t.Fatal("expected pending activation")
	}
	oldRuleID := revision1.rulesByRevisionID[oldActivation.ruleRevisionID].id

	if err := workspace.ReplaceRule(RuleSpec{
		Name:     "match-person",
		Salience: 20,
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("ReplaceRule: %v", err)
	}
	revision2 := mustCompileWorkspace(t, workspace)
	rule2, ok := revision2.Rule("match-person")
	if !ok {
		t.Fatal("expected replacement rule in compiled revision")
	}

	result, err := session.ApplyRuleset(context.Background(), revision2)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	if len(result.ReplacedRuleRevisions) != 1 {
		t.Fatalf("replaced revisions = %#v, want one", result.ReplacedRuleRevisions)
	}
	if replacement := result.ReplacedRuleRevisions[0]; replacement.RuleID != oldRuleID || replacement.OldRevisionID != oldActivation.ruleRevisionID || replacement.NewRevisionID != rule2.RevisionID() {
		t.Fatalf("replacement metadata = %#v, want rule %q %q -> %q", replacement, oldRuleID, oldActivation.ruleRevisionID, rule2.RevisionID())
	}
	if len(result.AddedRuleRevisions) != 0 || len(result.RemovedRuleRevisions) != 0 || len(result.UnchangedRuleRevisions) != 0 {
		t.Fatalf("unexpected apply metadata: added=%#v removed=%#v unchanged=%#v", result.AddedRuleRevisions, result.RemovedRuleRevisions, result.UnchangedRuleRevisions)
	}
	if got := session.agendaDriver.agenda.activationsByRuleRevisionID(oldActivation.ruleRevisionID); len(got) != 0 {
		t.Fatalf("old revision activations still indexed after replace: %#v", got)
	}
	if _, ok := session.agendaDriver.agenda.activationByKey(oldActivation.key); ok {
		t.Fatalf("old activation key %#v still present after replace", oldActivation.key)
	}
	pending := session.agendaDriver.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after replace = %d, want %d", got, want)
	}
	if pending[0].ruleRevisionID != rule2.RevisionID() {
		t.Fatalf("replacement activation revision = %q, want %q", pending[0].ruleRevisionID, rule2.RevisionID())
	}
	if pending[0].activationID() == oldActivation.activationID() {
		t.Fatalf("replacement activation reused old activation id %q", oldActivation.activationID())
	}
}

func TestSessionApplyRulesetUnchangedPreservesAgendaStateAndEvents(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
		DuplicatePolicy: DuplicateAllow,
		Fields:          []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("apply-unchanged-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("Assert(Ada): %v", err)
	}
	if _, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Grace"})); err != nil {
		t.Fatalf("Assert(Grace): %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), mustSnapshot(t, context.Background(), session)); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agendaDriver.agenda.next()
	if !ok {
		t.Fatal("expected a pending activation to consume")
	}
	beforeEvents := len(collector.Events())
	beforePending := session.agendaDriver.agenda.pendingActivations()

	unchanged, err := session.ApplyRuleset(context.Background(), revision)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if unchanged.Status != ApplyRulesetUnchanged {
		t.Fatalf("apply status = %v, want %v", unchanged.Status, ApplyRulesetUnchanged)
	}
	if len(unchanged.AddedRuleRevisions) != 0 || len(unchanged.RemovedRuleRevisions) != 0 || len(unchanged.ReplacedRuleRevisions) != 0 || len(unchanged.UnchangedRuleRevisions) != 0 {
		t.Fatalf("unchanged apply should not report revision metadata: %#v", unchanged)
	}
	if got := len(collector.Events()); got != beforeEvents {
		t.Fatalf("events after unchanged apply = %d, want %d", got, beforeEvents)
	}
	afterPending := session.agendaDriver.agenda.pendingActivations()
	if len(afterPending) != len(beforePending) {
		t.Fatalf("pending activations changed after unchanged apply: before=%#v after=%#v", beforePending, afterPending)
	}
	if got, ok := session.agendaDriver.agenda.activationByKey(selected.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after unchanged apply = %#v, ok=%v", got, ok)
	}
	rule, ok := revision.Rule("match-person")
	if !ok {
		t.Fatal("expected rule in compiled revision")
	}
	if got := session.agendaDriver.agenda.activationsByRuleRevisionID(rule.RevisionID()); len(got) != 2 {
		t.Fatalf("revision activations after unchanged apply = %d, want 2", len(got))
	}
}

func TestSessionApplyRulesetUnchangedRebindsPendingActionClosure(t *testing.T) {
	build := func(record func(string)) (*Ruleset, TemplateKey) {
		workspace := NewWorkspace()
		template := mustAddTemplate(t, workspace, TemplateSpec{
			Name:            "action-rebind-item",
			DuplicatePolicy: DuplicateAllow,
			Fields:          []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "record",
			Fn: func(ActionContext) error {
				record("fired")
				return nil
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name:       "record-item",
			Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(template.Key())}},
			Actions:    []RuleActionSpec{{Name: "record"}},
		})
		return mustCompileWorkspace(t, workspace), template.Key()
	}

	var calls []string
	oldRevision, templateKey := build(func(string) { calls = append(calls, "old") })
	newRevision, _ := build(func(string) { calls = append(calls, "new") })
	if oldRevision.ID() != newRevision.ID() {
		t.Fatalf("closure-only revision IDs differ: old=%q new=%q", oldRevision.ID(), newRevision.ID())
	}
	collector := &testEventCollector{}
	session, err := NewSession(oldRevision, WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	for _, id := range []string{"one", "two"} {
		if _, err := session.Assert(context.Background(), templateKey, mustFields(t, map[string]any{"id": id})); err != nil {
			t.Fatalf("Assert(%s): %v", id, err)
		}
	}
	if result, err := session.Run(context.Background(), WithMaxFirings(1)); err != nil || result.Fired != 1 {
		t.Fatalf("first Run = (%#v, %v), want one firing", result, err)
	}
	if !reflect.DeepEqual(calls, []string{"old"}) {
		t.Fatalf("calls before apply = %v, want [old]", calls)
	}
	pendingBefore := session.agendaDriver.agenda.pendingActivations()
	if len(pendingBefore) != 1 {
		t.Fatalf("pending before apply = %d, want 1", len(pendingBefore))
	}
	graphMemory := session.propagation.runtime.graphBeta
	agenda := session.agendaDriver.agenda
	focusBefore := append([]ModuleName(nil), session.agendaDriver.focusStack...)
	countersBefore := session.propagationCounterSnapshot()
	eventsBefore := len(collector.Events())

	result, err := session.ApplyRuleset(context.Background(), newRevision)
	if err != nil || result.Status != ApplyRulesetUnchanged {
		t.Fatalf("ApplyRuleset = (%#v, %v), want unchanged", result, err)
	}
	if session.propagation.runtime.graphBeta != graphMemory || session.agendaDriver.agenda != agenda {
		t.Fatal("unchanged rebind replaced graph memory or agenda ownership")
	}
	if !reflect.DeepEqual(session.propagationCounterSnapshot(), countersBefore) {
		t.Fatal("unchanged rebind changed propagation counters")
	}
	if !reflect.DeepEqual(session.agendaDriver.focusStack, focusBefore) {
		t.Fatalf("focus after apply = %v, want %v", session.agendaDriver.focusStack, focusBefore)
	}
	if got := len(collector.Events()); got != eventsBefore {
		t.Fatalf("events after apply = %d, want %d", got, eventsBefore)
	}
	pendingAfter := session.agendaDriver.agenda.pendingActivations()
	if len(pendingAfter) != 1 || pendingAfter[0].activationID() != pendingBefore[0].activationID() {
		t.Fatalf("pending activation after apply = %#v, want identity %q", pendingAfter, pendingBefore[0].activationID())
	}

	if result, err := session.Run(context.Background()); err != nil || result.Fired != 1 {
		t.Fatalf("second Run = (%#v, %v), want one firing", result, err)
	}
	if !reflect.DeepEqual(calls, []string{"old", "new"}) {
		t.Fatalf("calls after apply = %v, want [old new]", calls)
	}
}

func TestSessionApplyRulesetUnchangedRebindsActionCallClosure(t *testing.T) {
	build := func(call DSLCallFunc) (*Ruleset, TemplateKey) {
		workspace := NewWorkspace()
		template := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "call-rebind-item",
			Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "notify",
			Call: &ActionCallSpec{Name: "notify", Fn: call},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name:       "notify-item",
			Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(template.Key())}},
			Actions:    []RuleActionSpec{{Name: "notify"}},
		})
		return mustCompileWorkspace(t, workspace), template.Key()
	}

	var calls []string
	oldRevision, templateKey := build(func(ActionContext, []Value) error { calls = append(calls, "old"); return nil })
	newRevision, _ := build(func(ActionContext, []Value) error { calls = append(calls, "new"); return nil })
	session := mustSession(t, oldRevision, "action-call-rebind")
	if _, err := session.Assert(context.Background(), templateKey, mustFields(t, map[string]any{"id": "one"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if result, err := session.ApplyRuleset(context.Background(), newRevision); err != nil || result.Status != ApplyRulesetUnchanged {
		t.Fatalf("ApplyRuleset = (%#v, %v), want unchanged", result, err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"new"}) {
		t.Fatalf("call closures invoked = %v, want [new]", calls)
	}
}

func TestSessionApplyRulesetUnchangedRebindsPureFunctionPerSession(t *testing.T) {
	build := func(value bool) (*Ruleset, TemplateKey) {
		workspace := NewWorkspace()
		template := mustAddTemplate(t, workspace, TemplateSpec{
			Name:            "function-rebind-item",
			DuplicatePolicy: DuplicateAllow,
			Fields:          []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
		})
		mustAddPureFunction(t, workspace, PureFunctionSpec{
			Name:   "enabled",
			Return: ValueBool,
			Func0: func(context.Context) (Value, error) {
				return newBoolValue(value), nil
			},
		})
		mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
		mustAddRule(t, workspace, RuleSpec{
			Name: "enabled-item",
			Conditions: []RuleConditionSpec{{
				Binding:    "item",
				Target:     TemplateKeyFact(template.Key()),
				Predicates: []ExpressionSpec{Call("enabled")},
			}},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		return mustCompileWorkspace(t, workspace), template.Key()
	}

	oldRevision, templateKey := build(false)
	newRevision, _ := build(true)
	if oldRevision.ID() != newRevision.ID() {
		t.Fatalf("closure-only revision IDs differ: old=%q new=%q", oldRevision.ID(), newRevision.ID())
	}
	rebound := mustSession(t, oldRevision, "pure-function-rebound")
	untouched := mustSession(t, oldRevision, "pure-function-untouched")
	graphMemory := rebound.propagation.runtime.graphBeta
	if result, err := rebound.ApplyRuleset(context.Background(), newRevision); err != nil || result.Status != ApplyRulesetUnchanged {
		t.Fatalf("ApplyRuleset = (%#v, %v), want unchanged", result, err)
	}
	if rebound.propagation.runtime.graphBeta != graphMemory {
		t.Fatal("pure-function rebind replaced graph memory")
	}
	for _, session := range []*Session{rebound, untouched} {
		if _, err := session.Assert(context.Background(), templateKey, mustFields(t, map[string]any{"id": session.ID().String()})); err != nil {
			t.Fatalf("Assert(%s): %v", session.ID(), err)
		}
	}
	if got := len(rebound.agendaDriver.agenda.pendingActivations()); got != 1 {
		t.Fatalf("rebound pending activations = %d, want 1", got)
	}
	if got := len(untouched.agendaDriver.agenda.pendingActivations()); got != 0 {
		t.Fatalf("untouched pending activations = %d, want 0", got)
	}
}

func TestSessionApplyRulesetUnchangedRejectsIncompatibleGraphLayout(t *testing.T) {
	build := func(equalityComparator bool) *Ruleset {
		workspace := NewWorkspace()
		left := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "layout-left",
			Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
		})
		right := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "layout-right",
			Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}},
		})
		mustAddPureFunction(t, workspace, PureFunctionSpec{
			Name:               "same-group",
			Args:               []ValueKind{ValueString, ValueString},
			Return:             ValueBool,
			EqualityComparator: equalityComparator,
			Func: func(_ context.Context, args []Value) (Value, error) {
				leftValue, _ := args[0].AsString()
				rightValue, _ := args[1].AsString()
				return newBoolValue(leftValue == rightValue), nil
			},
		})
		mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
		mustAddRule(t, workspace, RuleSpec{
			Name: "layout-rule",
			Conditions: []RuleConditionSpec{
				{Binding: "left", Target: TemplateKeyFact(left.Key())},
				{
					Binding: "right",
					Target:  TemplateKeyFact(right.Key()),
					Predicates: []ExpressionSpec{Call("same-group",
						CurrentFieldExpr{Field: "group"},
						BindingFieldExpr{Binding: "left", Field: "group"},
					)},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		return mustCompileWorkspace(t, workspace)
	}

	oldRevision := build(false)
	unsafeRevision := build(true)
	if oldRevision.ID() != unsafeRevision.ID() {
		t.Fatalf("optimizer-only revision IDs differ: old=%q new=%q", oldRevision.ID(), unsafeRevision.ID())
	}
	session := mustSession(t, oldRevision, "layout-rebind")
	runtime := session.propagation.runtime
	graphMemory := runtime.graphBeta
	agenda := session.agendaDriver.agenda

	result, err := session.ApplyRuleset(context.Background(), unsafeRevision)
	if !errors.Is(err, ErrIncompatibleRuleset) || result.Status != ApplyRulesetIncompatible {
		t.Fatalf("ApplyRuleset = (%#v, %v), want incompatible graph layout", result, err)
	}
	if session.revision != oldRevision || session.propagation.runtime != runtime || runtime.graphBeta != graphMemory || session.agendaDriver.agenda != agenda {
		t.Fatal("failed graph-layout preflight partially mutated session owners")
	}
}

func TestSessionApplyRulesetKeepsUnchangedRefractionStateAcrossUnrelatedRuleChange(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
		DuplicatePolicy: DuplicateAllow,
		Fields:          []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "keep",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "change",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "keep-person",
		Salience: 20,
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "keep"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "change-person",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "change"}},
	})
	revision1 := mustCompileWorkspace(t, workspace)
	keepRule, ok := revision1.Rule("keep-person")
	if !ok {
		t.Fatal("expected unchanged rule in first revision")
	}
	session, err := NewSession(revision1, WithSessionID("apply-unrelated-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), mustSnapshot(t, context.Background(), session)); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	kept, ok := session.agendaDriver.agenda.next()
	if !ok {
		t.Fatal("expected a pending activation to consume")
	}

	if err := workspace.ReplaceRule(RuleSpec{
		Name:     "change-person",
		Salience: 30,
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "change"}},
	}); err != nil {
		t.Fatalf("ReplaceRule: %v", err)
	}
	revision2 := mustCompileWorkspace(t, workspace)
	change2, ok := revision2.Rule("change-person")
	if !ok {
		t.Fatal("expected replacement rule in compiled revision")
	}

	result, err := session.ApplyRuleset(context.Background(), revision2)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	if len(result.UnchangedRuleRevisions) != 1 || result.UnchangedRuleRevisions[0].RuleID != keepRule.ID() || result.UnchangedRuleRevisions[0].RevisionID != keepRule.RevisionID() {
		t.Fatalf("unchanged revisions = %#v, want %q/%q", result.UnchangedRuleRevisions, keepRule.ID(), keepRule.RevisionID())
	}
	if len(result.ReplacedRuleRevisions) != 1 || result.ReplacedRuleRevisions[0].RuleID != change2.ID() || result.ReplacedRuleRevisions[0].NewRevisionID != change2.RevisionID() {
		t.Fatalf("replaced revisions = %#v, want replacement for %q/%q", result.ReplacedRuleRevisions, change2.ID(), change2.RevisionID())
	}
	keptAfterApply, ok := session.agendaDriver.agenda.activationByKey(kept.key)
	if !ok || keptAfterApply.status != activationStatusConsumed {
		t.Fatalf("unchanged consumed activation after unrelated change = %#v, ok=%v", keptAfterApply, ok)
	}
	if got, want := keptAfterApply.activationID(), kept.activationID(); got != want {
		t.Fatalf("unchanged activation ID after unrelated change = %q, want %q", got, want)
	}
	if got, want := keptAfterApply.factIDs(), []FactID{inserted.Fact.ID()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged activation fact IDs after unrelated change = %#v, want %#v", got, want)
	}
	if got, want := keptAfterApply.path(), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged activation path after unrelated change = %#v, want %#v", got, want)
	}
	if got := keptAfterApply.bindings(); len(got) != 1 || got[0].binding != "person" || got[0].factID != inserted.Fact.ID() || got[0].conditionPath[0] != 0 {
		t.Fatalf("unchanged activation binding after unrelated change = %#v", got)
	}
	if got := session.agendaDriver.agenda.activationsByRuleRevisionID(kept.ruleRevisionID); len(got) != 1 {
		t.Fatalf("unchanged revision activations after unrelated change = %d, want 1", len(got))
	}
	if got := session.agendaDriver.agenda.activationsByRuleRevisionID(change2.RevisionID()); len(got) != 1 {
		t.Fatalf("replacement revision activations after unrelated change = %d, want 1", len(got))
	}
}

func TestSessionApplyRulesetGraphBetaRemovalStaysEmptyAcrossReplacement(t *testing.T) {
	ctx := context.Background()
	workspace, employeeKey, departmentKey, regionKey, officeKey := mustGraphTopologyRemovalWorkspace(t)
	revision1 := mustCompileWorkspace(t, workspace)
	session, err := NewSession(
		revision1,
		WithSessionID("graph-beta-shared-topology-apply-session"),
		WithInitialFacts(mustGraphTopologyRemovalInitialFacts(t, employeeKey, departmentKey, regionKey, officeKey)...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.propagation.runtime == nil || session.propagation.runtime.graphBeta == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta", session.propagation.runtime)
	}
	assertGraphTopologyRemovalShape(t, revision1)
	if !session.propagation.runtime.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.propagation.runtime)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agendaDriver.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before retract = %d, want %d", got, want)
	}

	department := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	if _, err := session.Retract(ctx, department.ID()); err != nil {
		t.Fatalf("Retract(Engineering department): %v", err)
	}
	if got := len(session.agendaDriver.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after retract = %d, want 0", got)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	session.attachPropagationCounters()
	beforeApplyCounters := session.propagationCounterSnapshot().Totals

	replacement := RuleSpec{
		Name:     "employee-department-region-a",
		Salience: 5,
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employeeKey)},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				}, Target: TemplateKeyFact(departmentKey),
			},
			{
				Binding: "region",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "department", Field: "region"}},
				}, Target: TemplateKeyFact(regionKey),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}
	if err := workspace.ReplaceRule(replacement); err != nil {
		t.Fatalf("ReplaceRule(%s): %v", replacement.Name, err)
	}
	revision2 := mustCompileWorkspace(t, workspace)
	result, err := session.ApplyRuleset(ctx, revision2)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	if result.PreviousRulesetID != revision1.ID() {
		t.Fatalf("previous ruleset id = %q, want %q", result.PreviousRulesetID, revision1.ID())
	}
	if result.CurrentRulesetID != revision2.ID() {
		t.Fatalf("current ruleset id = %q, want %q", result.CurrentRulesetID, revision2.ID())
	}
	if len(result.ReplacedRuleRevisions) != 1 {
		t.Fatalf("replaced revisions = %#v, want one", result.ReplacedRuleRevisions)
	}
	if session.RulesetID() != revision2.ID() {
		t.Fatalf("session ruleset id = %q, want %q", session.RulesetID(), revision2.ID())
	}
	if session.propagation.runtime == nil || session.propagation.runtime.graphBeta == nil {
		t.Fatalf("Rete runtime after apply = %#v, want graph beta", session.propagation.runtime)
	}
	if got := session.agendaDriver.agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after apply = %#v, want none", got)
	}
	afterApplyCounters := session.propagationCounterSnapshot().Totals
	if got, want := afterApplyCounters.GraphRebuilds-beforeApplyCounters.GraphRebuilds, 1; got != want {
		t.Fatalf("apply graph rebuilds = +%d, want +%d", got, want)
	}
	if got, want := afterApplyCounters.InitialGraphRebuilds-beforeApplyCounters.InitialGraphRebuilds, 1; got != want {
		t.Fatalf("apply initial graph rebuilds = +%d, want +%d", got, want)
	}
	if got := afterApplyCounters.SteadyStateGraphRebuilds - beforeApplyCounters.SteadyStateGraphRebuilds; got != 0 {
		t.Fatalf("apply steady-state graph rebuilds = +%d, want 0", got)
	}
	if got := afterApplyCounters.SteadyStateWholeTerminalScans - beforeApplyCounters.SteadyStateWholeTerminalScans; got != 0 {
		t.Fatalf("apply steady-state whole terminal scans = +%d, want 0", got)
	}
	if got, want := afterApplyCounters.OracleStyleMatchRequests, beforeApplyCounters.OracleStyleMatchRequests; got != want {
		t.Fatalf("apply oracle-style match requests = %d, want unchanged %d", got, want)
	}
	if got, want := afterApplyCounters.FullAgendaReconciles, beforeApplyCounters.FullAgendaReconciles; got != want {
		t.Fatalf("apply full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := afterApplyCounters.WholeTerminalScans, beforeApplyCounters.WholeTerminalScans; got != want {
		t.Fatalf("apply whole terminal scans = %d, want unchanged %d", got, want)
	}
	if got, want := afterApplyCounters.AgendaDeltaApplications, beforeApplyCounters.AgendaDeltaApplications+1; got != want {
		t.Fatalf("apply agenda delta applications = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	assertGraphBetaRuntimeParity(t, revision2, session)
}

func TestSessionApplyRulesetRejectsIncompatibleTemplateChangesWithoutMutatingSession(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision1 := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(revision1, WithSessionID("apply-incompatible-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), mustSnapshot(t, context.Background(), session)); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	beforeSnapshot := mustSnapshot(t, context.Background(), session)
	beforeEvents := len(collector.Events())
	beforePending := session.agendaDriver.agenda.pendingActivations()

	workspace2 := NewWorkspace()
	mustAddTemplate(t, workspace2, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace2, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace2, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision2 := mustCompileWorkspace(t, workspace2)

	result, err := session.ApplyRuleset(context.Background(), revision2)
	if !errors.Is(err, ErrIncompatibleRuleset) {
		t.Fatalf("apply incompatible error = %v, want ErrIncompatibleRuleset", err)
	}
	if result.Status != ApplyRulesetIncompatible {
		t.Fatalf("apply incompatible status = %v, want %v", result.Status, ApplyRulesetIncompatible)
	}
	if session.RulesetID() != revision1.ID() {
		t.Fatalf("ruleset id after incompatible apply = %q, want %q", session.RulesetID(), revision1.ID())
	}
	afterSnapshot := mustSnapshot(t, context.Background(), session)
	if afterSnapshot.RulesetID() != beforeSnapshot.RulesetID() {
		t.Fatalf("snapshot ruleset id changed after incompatible apply: %q -> %q", beforeSnapshot.RulesetID(), afterSnapshot.RulesetID())
	}
	if afterSnapshot.String() != beforeSnapshot.String() {
		t.Fatalf("snapshot changed after incompatible apply: before=%q after=%q", beforeSnapshot, afterSnapshot)
	}
	if got := len(collector.Events()); got != beforeEvents {
		t.Fatalf("events after incompatible apply = %d, want %d", got, beforeEvents)
	}
	if got := session.agendaDriver.agenda.pendingActivations(); len(got) != len(beforePending) {
		t.Fatalf("pending activations after incompatible apply = %#v, want %#v", got, beforePending)
	}
	rule1, ok := revision1.Rule("match-person")
	if !ok {
		t.Fatal("expected rule in compiled revision")
	}
	if got := session.agendaDriver.agenda.activationsByRuleRevisionID(rule1.RevisionID()); len(got) != 1 {
		t.Fatalf("existing activation index after incompatible apply = %d, want 1", len(got))
	}
	if got := inserted.Fact.ID(); got.IsZero() {
		t.Fatal("expected inserted fact to have an ID")
	}
}

func TestSessionApplyRulesetQueuesDuringRunBeforeNextActivation(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	auditTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "audit",
		Fields: []FieldSpec{{Name: "kind", Kind: ValueString, Required: true}},
	})

	var (
		actionsSeen       []string
		auditRulesetID    RulesetID
		auditRuleRevision RuleRevisionID
		started           = make(chan struct{})
		release           = make(chan struct{})
	)

	mustAddAction(t, workspace, ActionSpec{
		Name: "pause",
		Fn: func(ActionContext) error {
			close(started)
			<-release
			actionsSeen = append(actionsSeen, "pause")
			return nil
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "audit",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "audit")
			auditRulesetID = ctx.RulesetID()
			auditRuleRevision = ctx.RuleRevisionID()
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "pause-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "pause"}},
	})
	revision1 := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision1, WithSessionID("apply-queued-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("Assert(person): %v", err)
	}
	if _, err := session.Assert(context.Background(), auditTemplate.Key(), mustFields(t, map[string]any{"kind": "queued"})); err != nil {
		t.Fatalf("Assert(audit): %v", err)
	}

	runDone := make(chan struct{})
	var runResult RunResult
	var runErr error
	go func() {
		defer close(runDone)
		runResult, runErr = session.Run(context.Background())
	}()

	<-started

	workspace2 := workspace
	mustAddRule(t, workspace2, RuleSpec{
		Name: "audit-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", Target: TemplateKeyFact(auditTemplate.Key())},
		},
		Actions: []RuleActionSpec{{Name: "audit"}},
	})
	revision2 := mustCompileWorkspace(t, workspace2)

	applyDone := make(chan struct {
		result ApplyRulesetResult
		err    error
	}, 1)
	go func() {
		result, err := session.ApplyRuleset(context.Background(), revision2)
		applyDone <- struct {
			result ApplyRulesetResult
			err    error
		}{result: result, err: err}
	}()
	waitForQueuedMutationCount(t, session, 1)

	select {
	case outcome := <-applyDone:
		t.Fatalf("apply completed before run reached safe point: %#v", outcome)
	default:
	}

	close(release)

	var applyOutcome struct {
		result ApplyRulesetResult
		err    error
	}
	select {
	case applyOutcome = <-applyDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued apply")
	}
	if applyOutcome.err != nil {
		t.Fatalf("ApplyRuleset: %v", applyOutcome.err)
	}
	if applyOutcome.result.Status != ApplyRulesetApplied {
		t.Fatalf("queued apply status = %v, want %v", applyOutcome.result.Status, ApplyRulesetApplied)
	}
	if applyOutcome.result.CurrentRulesetID != revision2.ID() {
		t.Fatalf("queued apply ruleset id = %q, want %q", applyOutcome.result.CurrentRulesetID, revision2.ID())
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
	if runResult.Fired != 2 {
		t.Fatalf("run fired = %d, want 2", runResult.Fired)
	}
	if got, want := actionsSeen, []string{"pause", "audit"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
	if auditRulesetID != revision2.ID() {
		t.Fatalf("audit action ruleset id = %q, want %q", auditRulesetID, revision2.ID())
	}
	auditRule, ok := revision2.Rule("audit-rule")
	if !ok {
		t.Fatal("expected audit rule in replacement revision")
	}
	if auditRuleRevision != auditRule.RevisionID() {
		t.Fatalf("audit action rule revision id = %q, want %q", auditRuleRevision, auditRule.RevisionID())
	}
}

func mustCompileWorkspace(t testing.TB, workspace *Workspace) *Ruleset {
	t.Helper()
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}
