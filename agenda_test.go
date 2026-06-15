package gess

import (
	"context"
	"sort"
	"testing"
	"time"
)

func TestAgendaReconcileSuppressesDuplicateMatchesAndBuildsEvents(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-session")

	inserted, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)

	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("changes = %d, want %d", got, want)
	}
	if changes[0].kind != agendaChangeActivated {
		t.Fatalf("change kind = %v, want activated", changes[0].kind)
	}

	pending := agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations = %d, want %d", got, want)
	}
	rule, ok := revision.Rule("match-person")
	if !ok {
		t.Fatal("compiled rule missing")
	}

	event := changes[0].event(session.ID(), revision.ID(), 1, time.Unix(1, 0).UTC())
	if event.Type != EventRuleActivated {
		t.Fatalf("event type = %q, want %q", event.Type, EventRuleActivated)
	}
	if event.RuleID != rule.ID() {
		t.Fatalf("event rule ID = %q, want %q", event.RuleID, rule.ID())
	}
	if event.RuleRevisionID != rule.RevisionID() {
		t.Fatalf("event rule revision ID = %q, want %q", event.RuleRevisionID, rule.RevisionID())
	}
	if event.ActivationID != pending[0].id {
		t.Fatalf("event activation ID = %q, want %q", event.ActivationID, pending[0].id)
	}
	if got, want := len(event.FactIDs), 1; got != want {
		t.Fatalf("event fact IDs = %d, want %d", got, want)
	}
	if event.FactIDs[0] != inserted.Fact.ID() {
		t.Fatalf("event fact ID = %q, want %q", event.FactIDs[0], inserted.Fact.ID())
	}

	changes, err = agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("duplicate reconcile: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("duplicate reconcile returned changes: %#v", changes)
	}
	if got, want := len(agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after duplicate reconcile = %d, want %d", got, want)
	}
	if got := agenda.pendingActivations()[0].id; got != pending[0].id {
		t.Fatalf("activation ID changed across duplicate reconcile: %q vs %q", got, pending[0].id)
	}
}

func TestAgendaReconcileCopiesCandidateSlices(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-candidate-ownership-session")

	if _, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if len(results) != 1 || len(results[0].candidates) != 1 {
		t.Fatalf("match results = %#v, want one candidate", results)
	}
	candidate := results[0].candidates[0]
	if len(candidate.path) == 0 {
		t.Fatal("candidate path is empty")
	}

	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	before := agenda.pendingActivations()[0]

	candidate.bindingTuple[0].binding = "mutated"
	candidate.factIDs[0] = newFactID(9, 9)
	candidate.factVersions[0] = 99
	candidate.path[0] = 42

	after := agenda.pendingActivations()[0]
	if after.bindings[0].binding != before.bindings[0].binding {
		t.Fatalf("activation binding tuple binding changed: got %q want %q", after.bindings[0].binding, before.bindings[0].binding)
	}
	if after.factIDs[0] != before.factIDs[0] {
		t.Fatalf("activation fact ID changed: got %q want %q", after.factIDs[0], before.factIDs[0])
	}
	if after.factVersions[0] != before.factVersions[0] {
		t.Fatalf("activation fact version changed: got %d want %d", after.factVersions[0], before.factVersions[0])
	}
	if len(after.path) != len(before.path) || after.path[0] != before.path[0] {
		t.Fatalf("activation path changed: got %#v want %#v", after.path, before.path)
	}
}

func TestAgendaActivationIdentityChangesWhenFactVersionChanges(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-version-session")

	inserted, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	initial := agenda.pendingActivations()[0]

	modified, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}

	results = mustAgendaMatchResults(t, revision, session)
	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("version reconcile: %v", err)
	}
	if got, want := len(changes), 2; got != want {
		t.Fatalf("version reconcile changes = %d, want %d", got, want)
	}

	var activated, deactivated *agendaChange
	for i := range changes {
		switch changes[i].kind {
		case agendaChangeActivated:
			activated = &changes[i]
		case agendaChangeDeactivated:
			deactivated = &changes[i]
		}
	}
	if activated == nil || deactivated == nil {
		t.Fatalf("missing activation transition kinds: %#v", changes)
	}
	if activated.activation.id == initial.id {
		t.Fatalf("activation ID did not change after fact version changed: %q", activated.activation.id)
	}
	if activated.activation.factVersions[0] != modified.Fact.Version() {
		t.Fatalf("new activation fact version = %d, want %d", activated.activation.factVersions[0], modified.Fact.Version())
	}
	if deactivated.activation.id != initial.id {
		t.Fatalf("deactivated activation ID = %q, want %q", deactivated.activation.id, initial.id)
	}
	if deactivated.activation.status != activationStatusDeactivated {
		t.Fatalf("deactivated status = %v, want deactivated", deactivated.activation.status)
	}
	if got := agenda.pendingActivations(); len(got) != 1 || got[0].id == initial.id {
		t.Fatalf("pending activations after version change = %#v, want one new activation", got)
	}
}

func TestAgendaCandidateDeltasDoNotRequeueConsumedActivation(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-delta-refraction-session")

	if _, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	selected, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if selected.status != activationStatusConsumed {
		t.Fatalf("selected status = %v, want consumed", selected.status)
	}

	changes, err := agenda.applyCandidateDeltas(context.Background(), revision, nil, results[0].candidates)
	if err != nil {
		t.Fatalf("applyCandidateDeltas: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("repeat delta changes = %#v, want none", changes)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after repeat delta = %#v, want none", got)
	}
	if got, ok := agenda.activationByKey(selected.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after repeat delta = %#v, ok=%v", got, ok)
	}
}

func TestAgendaActivationIdentityHandlesHashCollisions(t *testing.T) {
	revision, _ := mustAgendaRevision(t, 10)
	rule := revision.rules["match-person"]
	identity := candidateIdentity{
		generation: 1,
		count:      1,
		key: candidateIdentityKey{
			scopeHash: candidateIdentityScopeHash(rule.id, rule.revisionID),
			hash:      42,
		},
	}
	firstID := newFactID(1, 1)
	secondID := newFactID(1, 2)
	results := []ruleMatchResult{
		{
			ruleID:           rule.id,
			ruleRevisionID:   rule.revisionID,
			salience:         rule.salience,
			declarationOrder: rule.declarationOrder,
			candidates: []matchCandidate{
				mustCollisionCandidate(rule.id, rule.revisionID, identity, firstID, 1, 1),
				mustCollisionCandidate(rule.id, rule.revisionID, identity, secondID, 1, 2),
				mustCollisionCandidate(rule.id, rule.revisionID, identity, firstID, 1, 1),
			},
		},
	}

	agenda := newAgenda()
	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, want := len(changes), 2; got != want {
		t.Fatalf("collision activation changes = %d, want %d", got, want)
	}
	pending := agenda.pendingActivations()
	if got, want := len(pending), 2; got != want {
		t.Fatalf("pending collision activations = %d, want %d", got, want)
	}
	if pending[0].id == pending[1].id {
		t.Fatalf("colliding activations reused public ID %q", pending[0].id)
	}

	changes, err = agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("duplicate collision reconcile: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("duplicate collision reconcile changes = %#v, want none", changes)
	}
}

func TestAgendaIndexesSuppressRepeatedFactIDs(t *testing.T) {
	agenda := newAgenda()
	factID := newFactID(1, 1)
	activation := activation{
		ruleRevisionID: RuleRevisionID("revision"),
		identity: candidateIdentity{
			generation: 1,
			count:      2,
			key: candidateIdentityKey{
				scopeHash: 1,
				hash:      2,
			},
		},
		factIDs:      []FactID{factID, factID},
		factVersions: []FactVersion{1, 1},
		status:       activationStatusPending,
	}

	agenda.storeActivation(&activation)
	indexActivation(agenda.byFactID, agenda.byRevision, activation)

	if got, want := len(agenda.byFactID[factID]), 1; got != want {
		t.Fatalf("fact index keys = %d, want %d", got, want)
	}
	if got, want := len(agenda.activationsByFactID(factID)), 1; got != want {
		t.Fatalf("fact index activations = %d, want %d", got, want)
	}
	if got, want := len(agenda.byRevision[activation.ruleRevisionID]), 1; got != want {
		t.Fatalf("revision index keys = %d, want %d", got, want)
	}
	if got, want := len(agenda.activationsByRuleRevisionID(activation.ruleRevisionID)), 1; got != want {
		t.Fatalf("revision index activations = %d, want %d", got, want)
	}
}

func TestAgendaReconcileDeactivatesMissingPendingActivation(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-missing-session")

	inserted, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	initial := agenda.pendingActivations()[0]

	if _, err := session.Retract(context.Background(), inserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	results = mustAgendaMatchResults(t, revision, session)
	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("retract reconcile: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("retract reconcile changes = %d, want %d", got, want)
	}
	if changes[0].kind != agendaChangeDeactivated {
		t.Fatalf("change kind = %v, want deactivated", changes[0].kind)
	}
	if changes[0].activation.id != initial.id {
		t.Fatalf("deactivated activation ID = %q, want %q", changes[0].activation.id, initial.id)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after retract = %#v, want none", got)
	}
	if got, ok := agenda.activationByKey(initial.key); !ok || got.status != activationStatusDeactivated {
		t.Fatalf("stored activation after retract = %#v, ok=%v", got, ok)
	}
}

func TestAgendaNextConsumesBeforeFutureReconcile(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-next-session")

	if _, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	selected, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if selected.status != activationStatusConsumed {
		t.Fatalf("selected status = %v, want consumed", selected.status)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after next = %#v, want none", got)
	}

	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("reconcile after consume: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("reconcile after consume returned changes: %#v", changes)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after consume and reconcile = %#v, want none", got)
	}
	if got, ok := agenda.activationByKey(selected.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation state = %#v, ok=%v", got, ok)
	}
}

func TestAgendaResetClearsStateAndAllowsNewGenerationMatches(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session, err := NewSession(
		revision,
		WithSessionID("agenda-reset-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: templateKey,
			Fields: mustFields(t, map[string]any{
				"name": "Ada",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	results := mustAgendaMatchResults(t, revision, session)
	if _, err := session.agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	if pending[0].id == selected.id {
		t.Fatalf("reset reused consumed activation ID %q", selected.id)
	}
	if pending[0].generation != 2 {
		t.Fatalf("reset activation generation = %d, want 2", pending[0].generation)
	}
	byRevision := session.agenda.activationsByRuleRevisionID(selected.ruleRevisionID)
	if got, want := len(byRevision), 1; got != want {
		t.Fatalf("activations by revision after reset = %d, want %d", got, want)
	}
	if byRevision[0].id != pending[0].id {
		t.Fatalf("revision index activation = %q, want %q", byRevision[0].id, pending[0].id)
	}

	results = mustAgendaMatchResults(t, revision, session)
	changes, err := session.agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("post-reset reconcile: %v", err)
	}
	if got := len(changes); got != 0 {
		t.Fatalf("post-reset reconcile changes = %d, want none", got)
	}
	if got := session.agenda.pendingActivations(); len(got) != 1 || got[0].id == selected.id {
		t.Fatalf("post-reset pending activations = %#v, want new activation ID", got)
	}
}

func TestAgendaReplacementUsesNewRevisionIdentityAndDoesNotShareRefractionState(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
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
			{Binding: "person", TemplateKey: template.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	session := mustSession(t, revision1, "agenda-replace-session")
	if _, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision1, session)
	if _, err := agenda.reconcile(context.Background(), revision1, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	first := agenda.pendingActivations()[0]
	if _, ok := agenda.next(); !ok {
		t.Fatal("next returned no activation")
	}

	if err := workspace.ReplaceRule(RuleSpec{
		Name:     "match-person",
		Salience: 20,
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: template.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("ReplaceRule: %v", err)
	}

	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("match-person")
	if !ok {
		t.Fatal("revision 2 rule missing")
	}
	results = mustAgendaMatchResults(t, revision2, session)
	changes, err := agenda.reconcile(context.Background(), revision2, results)
	if err != nil {
		t.Fatalf("reconcile revision 2: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("revision 2 changes = %d, want %d", got, want)
	}
	if changes[0].activation.ruleRevisionID != rule2.RevisionID() {
		t.Fatalf("new activation revision = %q, want %q", changes[0].activation.ruleRevisionID, rule2.RevisionID())
	}
	if changes[0].activation.id == first.id {
		t.Fatalf("new activation ID reused consumed activation ID %q", first.id)
	}
	if got := agenda.pendingActivations(); len(got) != 1 || got[0].ruleRevisionID != rule2.RevisionID() {
		t.Fatalf("pending activations after revision replace = %#v", got)
	}
	if got, ok := agenda.activationByKey(first.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after revision replace = %#v, ok=%v", got, ok)
	}
}

func TestActivationLessOrdersBySalienceRecencyDeclarationAndID(t *testing.T) {
	acts := []activation{
		{
			id:               ActivationID("z"),
			salience:         20,
			maxRecency:       1,
			aggregateRecency: 1,
			declarationOrder: 0,
		},
		{
			id:               ActivationID("y"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 9,
			declarationOrder: 0,
		},
		{
			id:               ActivationID("x"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
			declarationOrder: 0,
		},
		{
			id:               ActivationID("b"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
			declarationOrder: 1,
		},
		{
			id:               ActivationID("a"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
			declarationOrder: 1,
		},
	}

	sort.SliceStable(acts, func(i, j int) bool {
		return activationLess(&acts[i], &acts[j])
	})

	got := []ActivationID{acts[0].id, acts[1].id, acts[2].id, acts[3].id, acts[4].id}
	want := []ActivationID{"z", "y", "x", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted activation %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAgendaChangeEventsKeepFactEventsBare(t *testing.T) {
	change := agendaChange{
		kind: agendaChangeActivated,
		activation: activation{
			id:             ActivationID("activation"),
			ruleID:         RuleID("rule"),
			ruleRevisionID: RuleRevisionID("revision"),
			generation:     1,
			factIDs:        []FactID{newFactID(1, 2)},
		},
	}

	event := change.event(SessionID("session"), RulesetID("ruleset"), 3, time.Unix(2, 0).UTC())
	if event.Type != EventRuleActivated {
		t.Fatalf("event type = %q, want %q", event.Type, EventRuleActivated)
	}
	if event.RuleID != "rule" || event.RuleRevisionID != "revision" || event.ActivationID != "activation" {
		t.Fatalf("activation event metadata = %#v", event)
	}
	if got, want := len(event.FactIDs), 1; got != want {
		t.Fatalf("activation event fact IDs = %d, want %d", got, want)
	}

	factEvent := Event{Type: EventFactAsserted, FactIDs: []FactID{newFactID(1, 3)}}
	if factEvent.RuleID != "" || factEvent.RuleRevisionID != "" || factEvent.ActivationID != "" {
		t.Fatalf("fact event picked up rule metadata: %#v", factEvent)
	}
}

func TestSessionReconcileAgendaEmitsActivationEvents(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("agenda-events-session"),
		WithEventListener(collector),
		WithEventClock(countingClock()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after assert = %d, want %d", got, want)
	}
	activationID := pending[0].id
	snapshot := mustSnapshot(t, context.Background(), session)
	changes, err := session.reconcileAgenda(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	if got := len(changes); got != 0 {
		t.Fatalf("duplicate activation changes = %d, want none", got)
	}

	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("duplicate reconcileAgenda: %v", err)
	}

	if _, err := session.Retract(context.Background(), inserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	snapshot = mustSnapshot(t, context.Background(), session)
	changes, err = session.reconcileAgenda(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("retract reconcileAgenda: %v", err)
	}
	if got := len(changes); got != 0 {
		t.Fatalf("duplicate deactivation changes = %d, want none", got)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d: %#v", got, want, events)
	}
	if events[0].Type != EventFactAsserted || events[1].Type != EventRuleActivated || events[2].Type != EventFactRetracted || events[3].Type != EventRuleDeactivated {
		t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	}
	for i, event := range events {
		if got, want := event.Sequence, uint64(i+1); got != want {
			t.Fatalf("event %d sequence = %d, want %d", i, got, want)
		}
	}

	rule := revision.rules["match-person"]
	for _, event := range []Event{events[1], events[3]} {
		if event.RuleID != rule.id {
			t.Fatalf("rule event ID = %q, want %q", event.RuleID, rule.id)
		}
		if event.RuleRevisionID != rule.revisionID {
			t.Fatalf("rule event revision ID = %q, want %q", event.RuleRevisionID, rule.revisionID)
		}
		if event.ActivationID != activationID {
			t.Fatalf("rule event activation ID = %q, want %q", event.ActivationID, activationID)
		}
		if got, want := len(event.FactIDs), 1; got != want {
			t.Fatalf("rule event fact IDs = %d, want %d", got, want)
		}
		if event.FactIDs[0] != inserted.Fact.ID() {
			t.Fatalf("rule event fact ID = %q, want %q", event.FactIDs[0], inserted.Fact.ID())
		}
	}
}

func TestSessionResetEmitsPendingActivationDeactivationAndClearsRefraction(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("agenda-reset-events-session"),
		WithEventListener(collector),
		WithEventClock(countingClock()),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: templateKey,
			Fields:      mustFields(t, map[string]any{"name": "Ada"}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	changes, err := session.reconcileAgenda(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("initial reconcileAgenda: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("initial changes = %d, want %d", got, want)
	}
	initialID := changes[0].activation.id

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	if pending[0].id == initialID {
		t.Fatalf("post-reset activation reused old activation ID %q", initialID)
	}
	if pending[0].generation != 2 {
		t.Fatalf("post-reset activation generation = %d, want 2", pending[0].generation)
	}

	snapshot = mustSnapshot(t, context.Background(), session)
	changes, err = session.reconcileAgenda(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("post-reset reconcileAgenda: %v", err)
	}
	if got := len(changes); got != 0 {
		t.Fatalf("duplicate post-reset changes = %d, want none", got)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d: %#v", got, want, events)
	}
	if events[0].Type != EventRuleActivated || events[1].Type != EventRuleDeactivated || events[2].Type != EventReset || events[3].Type != EventRuleActivated {
		t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	}
	if events[1].ActivationID != initialID {
		t.Fatalf("reset deactivation ID = %q, want %q", events[1].ActivationID, initialID)
	}
	if events[2].Generation != 2 {
		t.Fatalf("reset event generation = %d, want 2", events[2].Generation)
	}
}

func countingClock() func() time.Time {
	var tick int64
	return func() time.Time {
		tick++
		return time.Unix(tick, 0).UTC()
	}
}

func mustAgendaRevision(t *testing.T, salience int) (*Ruleset, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "match-person",
		Salience: salience,
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: template.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, template.Key()
}

func mustAgendaMatchResults(t *testing.T, revision *Ruleset, session *Session) []ruleMatchResult {
	t.Helper()
	snapshot := mustSnapshot(t, context.Background(), session)
	results, err := newNaiveMatcher(revision).match(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	return results
}

func mustCollisionCandidate(ruleID RuleID, revisionID RuleRevisionID, identity candidateIdentity, factID FactID, version FactVersion, recency Recency) matchCandidate {
	return matchCandidate{
		ruleID:           ruleID,
		ruleRevisionID:   revisionID,
		identity:         identity,
		factIDs:          []FactID{factID},
		factVersions:     []FactVersion{version},
		generation:       identity.generation,
		maxRecency:       recency,
		aggregateRecency: recency,
		path:             []int{0},
		bindingTuple: []bindingTupleEntry{
			{
				binding:        "person",
				bindingSlot:    0,
				conditionOrder: 0,
				factID:         factID,
				factVersion:    version,
			},
		},
	}
}
