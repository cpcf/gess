package engine

import (
	"context"
	"reflect"
	"slices"
	"sort"
	"strconv"
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

	event := changes[0].eventWithRuleID(session.ID(), revision.ID(), rule.ID(), 1, time.Unix(1, 0).UTC())
	if event.Type != EventRuleActivated {
		t.Fatalf("event type = %q, want %q", event.Type, EventRuleActivated)
	}
	if event.RuleID != rule.ID() {
		t.Fatalf("event rule ID = %q, want %q", event.RuleID, rule.ID())
	}
	if event.RuleRevisionID != rule.RevisionID() {
		t.Fatalf("event rule revision ID = %q, want %q", event.RuleRevisionID, rule.RevisionID())
	}
	if event.ActivationID != pending[0].activationID() {
		t.Fatalf("event activation ID = %q, want %q", event.ActivationID, pending[0].activationID())
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
	if got := agenda.pendingActivations()[0].activationID(); got != pending[0].activationID() {
		t.Fatalf("activation ID changed across duplicate reconcile: %q vs %q", got, pending[0].activationID())
	}
}

func TestSessionDoesNotReserveAgendaRowsFromRuleCount(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "noop",
		Fn: func(ActionContext) error {
			return nil
		},
	})
	for i := range 300 {
		mustAddRule(t, workspace, RuleSpec{
			Name: "rule-" + strconv.Itoa(i),
			Conditions: []RuleConditionSpec{{
				Binding: "source",
				Target:  TemplateKeyFact(template.Key()),
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
	}

	session := mustSession(t, mustCompileWorkspace(t, workspace), "agenda-no-broad-reserve-session")
	if got := len(session.agenda.activationRows.chunks); got != 0 {
		t.Fatalf("activation row chunks after NewSession = %d, want 0", got)
	}
	if got := session.agenda.activationRows.count; got != 0 {
		t.Fatalf("activation row count after NewSession = %d, want 0", got)
	}

	result, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if result.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", result.Status, ResetApplied)
	}
	if got := len(session.agenda.activationRows.chunks); got != 0 {
		t.Fatalf("activation row chunks after Reset = %d, want 0", got)
	}
	if got := session.agenda.activationRows.count; got != 0 {
		t.Fatalf("activation row count after Reset = %d, want 0", got)
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
	afterBindings := after.bindings()
	beforeBindings := before.bindings()
	if afterBindings[0].binding != beforeBindings[0].binding {
		t.Fatalf("activation binding tuple binding changed: got %q want %q", afterBindings[0].binding, beforeBindings[0].binding)
	}
	afterFactIDs := after.factIDs()
	beforeFactIDs := before.factIDs()
	if afterFactIDs[0] != beforeFactIDs[0] {
		t.Fatalf("activation fact ID changed: got %q want %q", afterFactIDs[0], beforeFactIDs[0])
	}
	afterFactVersions := after.factVersions()
	beforeFactVersions := before.factVersions()
	if afterFactVersions[0] != beforeFactVersions[0] {
		t.Fatalf("activation fact version changed: got %d want %d", afterFactVersions[0], beforeFactVersions[0])
	}
	afterPath := after.path()
	beforePath := before.path()
	if len(afterPath) != len(beforePath) || afterPath[0] != beforePath[0] {
		t.Fatalf("activation path changed: got %#v want %#v", afterPath, beforePath)
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

	_, err = session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
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
	if activated.activation.activationID() == initial.activationID() {
		t.Fatalf("activation ID did not change after fact version changed: %q", activated.activation.activationID())
	}
	if len(activated.activation.bindings()) != 0 || len(activated.activation.path()) != 0 || len(activated.activation.factVersions()) != 0 {
		t.Fatalf("activated change should stay compact: %#v", activated.activation)
	}
	if deactivated.activation.activationID() != initial.activationID() {
		t.Fatalf("deactivated activation ID = %q, want %q", deactivated.activation.activationID(), initial.activationID())
	}
	if deactivated.activation.status != activationStatusDeactivated {
		t.Fatalf("deactivated status = %v, want deactivated", deactivated.activation.status)
	}
	if len(deactivated.activation.bindings()) != 0 || len(deactivated.activation.path()) != 0 || len(deactivated.activation.factVersions()) != 0 {
		t.Fatalf("deactivated change should stay compact: %#v", deactivated.activation)
	}
	if got := agenda.pendingActivations(); len(got) != 1 || got[0].activationID() == initial.activationID() {
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

func TestAgendaTerminalTokenDeltasDoNotRequeueConsumedActivation(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-token-delta-refraction-session")

	_, delta, err := session.insertFactImmediate(context.Background(), "", templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}), mutationOrigin{})
	if err != nil {
		t.Fatalf("insertFactImmediate: %v", err)
	}

	agenda := newAgenda()
	changes, err := agenda.applyTerminalTokenDeltas(context.Background(), revision, nil, cloneTerminalTokenDeltas(delta.added))
	if err != nil {
		t.Fatalf("initial applyTerminalTokenDeltas: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("initial terminal delta changes = %d, want %d", got, want)
	}
	selected, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if selected.status != activationStatusConsumed {
		t.Fatalf("selected status = %v, want consumed", selected.status)
	}

	changes, err = agenda.applyTerminalTokenDeltas(context.Background(), revision, nil, cloneTerminalTokenDeltas(delta.added))
	if err != nil {
		t.Fatalf("repeat applyTerminalTokenDeltas: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("repeat terminal delta changes = %#v, want none", changes)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after repeat terminal delta = %#v, want none", got)
	}
	if got, ok := agenda.activationByKey(selected.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after repeat terminal delta = %#v, ok=%v", got, ok)
	}
}

func TestCompactAgendaEntryArenaReusesIntegerHandlesWithGeneration(t *testing.T) {
	var arena compactAgendaEntryArena
	arena.reserve(2)
	if cap(arena.rows) < 2 {
		t.Fatalf("arena row capacity = %d, want at least 2", cap(arena.rows))
	}
	first := compactAgendaEntry{
		key:              activationKey{fingerprint: 10, ordinal: 1},
		ruleRevisionID:   "rule-1@1",
		identityKey:      candidateIdentityKey{scopeHash: 5, hash: 6},
		salience:         11,
		maxRecency:       12,
		aggregateRecency: 13,
		status:           activationStatusPending,
	}
	firstHandle, stored := arena.add(first)
	if firstHandle.isZero() {
		t.Fatal("first compact handle is zero")
	}
	if stored == nil || stored.ruleRevisionID != first.ruleRevisionID || stored.identityKey != first.identityKey {
		t.Fatalf("stored first entry = %#v, want %#v", stored, first)
	}
	if got := arena.len(); got != 1 {
		t.Fatalf("arena len after first add = %d, want 1", got)
	}
	if !arena.remove(firstHandle) {
		t.Fatal("remove first compact handle returned false")
	}
	if got := arena.len(); got != 0 {
		t.Fatalf("arena len after remove = %d, want 0", got)
	}
	if entry, ok := arena.get(firstHandle); ok || entry != nil {
		t.Fatalf("stale first compact handle resolved to %#v", entry)
	}

	second := first
	second.key = activationKey{fingerprint: 20, ordinal: 2}
	second.ruleRevisionID = "rule-2@1"
	second.status = activationStatusDeactivated
	secondHandle, stored := arena.add(second)
	if secondHandle.id != firstHandle.id || secondHandle.generation == firstHandle.generation {
		t.Fatalf("second compact handle = %#v after first %#v, want reused id and new generation", secondHandle, firstHandle)
	}
	if stored == nil || stored.key != second.key || stored.status != activationStatusDeactivated {
		t.Fatalf("stored second entry = %#v, want %#v", stored, second)
	}
	if entry, ok := arena.get(secondHandle); !ok || entry.ruleRevisionID != second.ruleRevisionID {
		t.Fatalf("second compact handle resolved to %#v, ok=%v", entry, ok)
	}
	if entry, ok := arena.get(firstHandle); ok || entry != nil {
		t.Fatalf("stale reused compact handle resolved to %#v", entry)
	}
}

func TestCompactAgendaEntryArenaResetInvalidatesHandles(t *testing.T) {
	var arena compactAgendaEntryArena
	handle, _ := arena.add(compactAgendaEntry{
		key:            activationKey{fingerprint: 1, ordinal: 1},
		ruleRevisionID: "rule@1",
		status:         activationStatusPending,
	})
	if handle.isZero() {
		t.Fatal("compact handle is zero")
	}
	arena.reset()
	if got := arena.len(); got != 0 {
		t.Fatalf("arena len after reset = %d, want 0", got)
	}
	if entry, ok := arena.get(handle); ok || entry != nil {
		t.Fatalf("reset compact handle resolved to %#v", entry)
	}
	nextHandle, entry := arena.add(compactAgendaEntry{
		key:            activationKey{fingerprint: 2, ordinal: 1},
		ruleRevisionID: "rule@2",
		status:         activationStatusPending,
	})
	if nextHandle.id != handle.id || nextHandle.generation == handle.generation {
		t.Fatalf("compact handle after reset = %#v after %#v, want same id and new generation", nextHandle, handle)
	}
	if entry == nil || entry.ruleRevisionID != "rule@2" {
		t.Fatalf("compact entry after reset = %#v", entry)
	}
}

func TestAgendaTerminalTokenDeltaBatchObservesActivations(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-token-delta-batch-observe-session")

	if _, _, err := session.insertFactImmediate(ctx, "", templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}), mutationOrigin{}); err != nil {
		t.Fatalf("insertFactImmediate Ada: %v", err)
	}
	if _, _, err := session.insertFactImmediate(ctx, "", templateKey, mustFields(t, map[string]any{
		"name": "Grace",
	}), mutationOrigin{}); err != nil {
		t.Fatalf("insertFactImmediate Grace: %v", err)
	}

	tokens, ok, err := session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok || len(tokens) != 2 {
		t.Fatalf("terminal token deltas = %#v, ok=%v, want two", tokens, ok)
	}

	observed := make([]*activation, 0, len(tokens))
	agenda := newAgenda()
	if _, err := agenda.applyTerminalTokenDeltasInternal(ctx, revision, nil, cloneTerminalTokenDeltas(tokens), false, func(act *activation) {
		observed = append(observed, act)
	}); err != nil {
		t.Fatalf("applyTerminalTokenDeltasInternal: %v", err)
	}
	if got, want := len(observed), 2; got != want {
		t.Fatalf("observed activations = %d, want %d", got, want)
	}
	if got, want := len(agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations = %d, want %d", got, want)
	}
	for i, activation := range observed {
		if activation == nil || activation.status != activationStatusPending {
			t.Fatalf("observed activation %d = %#v, want pending", i, activation)
		}
		if activation.token.isZero() || activation.identityKey == (candidateIdentityKey{}) {
			t.Fatalf("observed activation %d identity/token = (%#v, %#v), want retained rule-token identity", i, activation.identityKey, activation.token)
		}
	}

	if err := agenda.applyTerminalTokenDeltasWithoutChanges(ctx, revision, cloneTerminalTokenDeltas(tokens), nil); err != nil {
		t.Fatalf("remove by terminal token identity: %v", err)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after terminal token removals = %#v, want none", got)
	}
}

func TestAgendaTerminalTokenGraphPathsDoNotUseActivationRows(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-token-no-row-arena-session")

	if _, _, err := session.insertFactImmediate(ctx, "", templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}), mutationOrigin{}); err != nil {
		t.Fatalf("insertFactImmediate: %v", err)
	}
	if got := session.agenda.activationRows.count; got != 0 {
		t.Fatalf("session terminal delta activationRows.count = %d, want 0", got)
	}

	tokens, ok, err := session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok || len(tokens) != 1 {
		t.Fatalf("terminal token deltas = %#v, ok=%v, want one", tokens, ok)
	}

	deltaAgenda := newAgenda()
	if _, err := deltaAgenda.applyTerminalTokenDeltas(ctx, revision, nil, cloneTerminalTokenDeltas(tokens)); err != nil {
		t.Fatalf("applyTerminalTokenDeltas: %v", err)
	}
	if got := deltaAgenda.activationRows.count; got != 0 {
		t.Fatalf("terminal delta activationRows.count = %d, want 0", got)
	}

	reconcileAgenda := newAgenda()
	if _, err := reconcileAgenda.reconcileTerminalTokens(ctx, revision, cloneTerminalTokenDeltas(tokens)); err != nil {
		t.Fatalf("reconcileTerminalTokens: %v", err)
	}
	if got := reconcileAgenda.activationRows.count; got != 0 {
		t.Fatalf("terminal reconcile activationRows.count = %d, want 0", got)
	}

	graphAgenda := newAgenda()
	if _, ok, err := graphAgenda.reconcileGraphTerminalRows(ctx, revision, session.rete.graphBeta, true); err != nil {
		t.Fatalf("reconcileGraphTerminalRows: %v", err)
	} else if !ok {
		t.Fatal("reconcileGraphTerminalRows unavailable")
	}
	if got := graphAgenda.activationRows.count; got != 0 {
		t.Fatalf("graph terminal row reconcile activationRows.count = %d, want 0", got)
	}
}

func TestAgendaTerminalConsumedActivationKeepsCompactDerivedIdentity(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-token-compact-tombstone-session")

	_, delta, err := session.insertFactImmediate(ctx, "", templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}), mutationOrigin{})
	if err != nil {
		t.Fatalf("insertFactImmediate: %v", err)
	}

	agenda := newAgenda()
	if _, err := agenda.applyTerminalTokenDeltas(ctx, revision, nil, cloneTerminalTokenDeltas(delta.added)); err != nil {
		t.Fatalf("applyTerminalTokenDeltas: %v", err)
	}
	pending := agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations = %d, want %d", got, want)
	}
	if pending[0].activationID().IsZero() {
		t.Fatal("public pending activation ID is zero")
	}
	if _, ok := agenda.activationByKeyPtr(pending[0].key); !ok {
		t.Fatal("stored pending activation missing")
	}

	selected, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if selected.activationID().IsZero() {
		t.Fatal("selected public activation ID is zero")
	}
	rule := revision.rulesByRevisionID[selected.ruleRevisionID]
	if rule.id.IsZero() {
		t.Fatal("selected rule ID could not be derived")
	}
	stored, ok := agenda.activationByKeyPtr(selected.key)
	if !ok {
		t.Fatal("stored consumed activation missing")
	}
	if stored.status != activationStatusConsumed {
		t.Fatalf("stored activation status = %v, want consumed", stored.status)
	}
	if stored.payload != nil {
		t.Fatalf("stored consumed activation payload = %#v, want nil", stored.payload)
	}
	if stored.token.isZero() {
		t.Fatal("stored consumed activation lost token ref")
	}
	if got := stored.mutationOrigin().activationID(); got != selected.activationID() {
		t.Fatalf("stored mutation origin activation ID = %q, want %q", got, selected.activationID())
	}
	actionCtx := newTokenActionContext(ctx, nil, selected, rule)
	origin := actionCtx.mutationOrigin()
	if !origin.ActivationID.IsZero() {
		t.Fatalf("action mutation origin cached ID = %q, want compact derived identity", origin.ActivationID)
	}
	if origin.RuleID != rule.id {
		t.Fatalf("action mutation origin rule ID = %q, want %q", origin.RuleID, rule.id)
	}
	if got := origin.activationID(); got != selected.activationID() {
		t.Fatalf("action mutation origin activation ID = %q, want %q", got, selected.activationID())
	}
}

func TestAgendaTerminalTokenIdentityDeactivatesOnRetractAndModify(t *testing.T) {
	type terminalActivationState struct {
		session  *Session
		factID   FactID
		terminal *reteGraphTerminalMemory
		key      activationKey
	}
	newState := func(t *testing.T, id SessionID) terminalActivationState {
		t.Helper()
		ctx := context.Background()
		revision, templateKey := mustAgendaStatusRevision(t)
		session := mustSession(t, revision, id)
		inserted, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
			"status": "open",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}
		if got, want := len(session.agenda.pendingActivations()), 1; got != want {
			t.Fatalf("pending activations = %d, want %d", got, want)
		}
		rule, ok := revision.Rule("match-open-task")
		if !ok {
			t.Fatal("compiled rule missing")
		}
		terminal := session.rete.graphBeta.terminalForRule(rule.RevisionID())
		if terminal == nil {
			t.Fatal("terminal memory missing")
		}
		if got, want := terminal.rows.len(), 1; got != want {
			t.Fatalf("terminal rows = %d, want %d", got, want)
		}
		row := terminal.rows.rows[0]
		token := terminal.rows.rowToken(row)
		identity := terminal.terminalTokenIdentity(token)
		compiledRule, ok := revision.rulesByRevisionID[rule.RevisionID()]
		if !ok {
			t.Fatal("compiled rule revision missing")
		}
		stored, _, ok := session.agenda.activationForTerminalTokenIdentity(compiledRule, token, identity)
		if !ok || stored.status != activationStatusPending {
			t.Fatalf("activation by terminal token identity = %#v, ok=%v; want pending", stored, ok)
		}
		if stored.token.isZero() || stored.identityKey == (candidateIdentityKey{}) {
			t.Fatalf("stored activation identity/token = (%#v, %#v), want retained rule-token identity", stored.identityKey, stored.token)
		}
		return terminalActivationState{
			session:  session,
			factID:   inserted.Fact.ID(),
			terminal: terminal,
			key:      stored.key,
		}
	}
	assertDeactivated := func(t *testing.T, state terminalActivationState) {
		t.Helper()
		if got := state.session.agenda.pendingActivations(); len(got) != 0 {
			t.Fatalf("pending activations after mutation = %#v, want none", got)
		}
		if got, want := state.terminal.rows.len(), 0; got != want {
			t.Fatalf("terminal rows after mutation = %d, want %d", got, want)
		}
		stored, ok := state.session.agenda.activationByKeyPtr(state.key)
		if !ok {
			t.Fatal("stored activation missing after mutation")
		}
		if stored.status != activationStatusDeactivated {
			t.Fatalf("stored activation status = %v, want deactivated", stored.status)
		}
	}

	t.Run("retract", func(t *testing.T) {
		state := newState(t, "agenda-terminal-row-retract-session")
		if _, err := state.session.Retract(context.Background(), state.factID); err != nil {
			t.Fatalf("Retract: %v", err)
		}
		assertDeactivated(t, state)
	})

	t.Run("modify", func(t *testing.T) {
		state := newState(t, "agenda-terminal-row-modify-session")
		if _, err := state.session.Modify(context.Background(), state.factID, FactPatch{
			Set: mustFields(t, map[string]any{"status": "closed"}),
		}); err != nil {
			t.Fatalf("Modify: %v", err)
		}
		assertDeactivated(t, state)
	})
}

func TestCompactGraphActivationsPreserveOrderingAndFocusSelection(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "bucket", Kind: ValueString, Required: true},
		},
	})
	for _, actionName := range []string{"salience-first", "recent-new", "recent-old", "decl-first", "decl-second", "ask"} {
		mustAddAction(t, workspace, ActionSpec{Name: actionName, Fn: func(ActionContext) error { return nil }})
	}
	mustAddRule(t, workspace, RuleSpec{
		Name:     "salience-first",
		Salience: 30,
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(task.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "bucket",
				Operator: FieldConstraintEqual,
				Value:    "top",
			}},
		}},
		Actions: []RuleActionSpec{{Name: "salience-first"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "recent-new",
		Salience: 20,
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(task.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "bucket",
				Operator: FieldConstraintEqual,
				Value:    "recent-new",
			}},
		}},
		Actions: []RuleActionSpec{{Name: "recent-new"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "recent-old",
		Salience: 20,
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(task.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "bucket",
				Operator: FieldConstraintEqual,
				Value:    "recent-old",
			}},
		}},
		Actions: []RuleActionSpec{{Name: "recent-old"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "decl-first",
		Salience: 10,
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(task.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "bucket",
				Operator: FieldConstraintEqual,
				Value:    "tie",
			}},
		}},
		Actions: []RuleActionSpec{{Name: "decl-first"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "decl-second",
		Salience: 10,
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(task.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "bucket",
				Operator: FieldConstraintEqual,
				Value:    "tie",
			}},
		}},
		Actions: []RuleActionSpec{{Name: "decl-second"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "ask",
		Module:   "ask",
		Salience: 15,
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(task.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "bucket",
				Operator: FieldConstraintEqual,
				Value:    "ask",
			}},
		}},
		Actions: []RuleActionSpec{{Name: "ask"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "compact-graph-order-focus-session")
	for _, bucket := range []string{"tie", "recent-old", "ask", "recent-new", "top"} {
		if _, err := session.AssertTemplate(ctx, task.Key(), mustFields(t, map[string]any{"bucket": bucket})); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", bucket, err)
		}
	}
	tokens, ok, err := session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok {
		t.Fatal("currentTerminalTokenDeltas unavailable")
	}

	agenda := newAgenda()
	if _, err := agenda.reconcileTerminalTokens(ctx, revision, cloneTerminalTokenDeltas(tokens)); err != nil {
		t.Fatalf("reconcileTerminalTokens: %v", err)
	}
	for _, pending := range agenda.pending {
		stored, ok := agenda.activationByKeyPtr(pending)
		if !ok {
			t.Fatalf("stored activation for key %#v missing", pending)
		}
		if stored.payload != nil {
			t.Fatalf("stored graph activation kept public fields: %#v", stored)
		}
	}

	if _, selected, ok := agenda.nextInternalPtrForModule("ask"); !ok {
		t.Fatal("nextInternalPtrForModule(ask) returned no activation")
	} else if got := compactGraphActivationRuleName(t, revision, selected); got != "ask" {
		t.Fatalf("focused activation = %q, want ask", got)
	} else if selected.salience != 15 {
		t.Fatalf("focused activation salience = %d, want 15", selected.salience)
	}

	var got []string
	for {
		_, selected, ok := agenda.nextInternalPtr()
		if !ok {
			break
		}
		got = append(got, compactGraphActivationRuleName(t, revision, selected))
	}
	want := []string{"salience-first", "recent-new", "recent-old", "decl-first", "decl-second"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining activation order = %#v, want %#v", got, want)
	}
}

func TestAgendaTerminalTokenFactIndexMaterializesLazily(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-token-fact-index-session")

	inserted, delta, err := session.insertFactImmediate(context.Background(), "", templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}), mutationOrigin{})
	if err != nil {
		t.Fatalf("insertFactImmediate: %v", err)
	}

	agenda := newAgenda()
	if _, err := agenda.applyTerminalTokenDeltas(context.Background(), revision, nil, cloneTerminalTokenDeltas(delta.added)); err != nil {
		t.Fatalf("applyTerminalTokenDeltas: %v", err)
	}
	if !agenda.tokenFactIndexDirty {
		t.Fatal("token fact index should be dirty after storing a token-backed activation")
	}
	if got := agenda.byFactID[inserted.Fact.ID()].len(); got != 0 {
		t.Fatalf("eager token fact index entries = %d, want 0", got)
	}

	activations := agenda.activationsByFactID(inserted.Fact.ID())
	if got, want := len(activations), 1; got != want {
		t.Fatalf("activationsByFactID = %d, want %d", got, want)
	}
	activationFactIDs := activations[0].factIDs()
	if activationFactIDs[0] != inserted.Fact.ID() {
		t.Fatalf("activation fact ID = %q, want %q", activationFactIDs[0], inserted.Fact.ID())
	}
	if agenda.tokenFactIndexDirty {
		t.Fatal("token fact index should be clean after fact lookup")
	}
	if got := agenda.byFactID[inserted.Fact.ID()].len(); got != 1 {
		t.Fatalf("lazy token fact index entries = %d, want 1", got)
	}
}

func TestTokenArenaMaterializesFactSpansAtBoundary(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 3, recency: 10, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 5, recency: 11, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version(), conditionPath: []int{0}}
	secondEntry := bindingTupleEntry{bindingSlot: 1, factID: secondFact.ID(), factVersion: secondFact.Version(), conditionPath: []int{1}}

	first := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	second := arena.add(first, secondEntry, conditionMatch{bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	for i := range 32 {
		fact := FactSnapshot{id: newFactID(1, uint64(i+10)), version: FactVersion(i + 1), recency: Recency(i + 20), generation: 1}
		entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
		arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	}

	if got, want := cloneActivationFactIDs(&activation{token: second}), []FactID{firstFact.ID(), secondFact.ID()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cached token fact IDs = %#v, want %#v", got, want)
	}
	if got, want := cloneActivationFactVersions(&activation{token: second}), []FactVersion{firstFact.Version(), secondFact.Version()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cached token fact versions = %#v, want %#v", got, want)
	}
	if got, ok := second.factIDs(); ok || got != nil {
		t.Fatalf("token cached fact IDs = %#v, %v; want unavailable", got, ok)
	}
	if got, ok := second.factVersions(); ok || got != nil {
		t.Fatalf("token cached fact versions = %#v, %v; want unavailable", got, ok)
	}

	valueEntry := bindingTupleEntry{bindingSlot: 2, value: newIntValue(42), hasValue: true, conditionPath: []int{2}}
	valueToken := arena.add(second, valueEntry, conditionMatch{bindingSlot: 2, value: newIntValue(42), hasValue: true}, 0, secondFact.Generation())
	if got, want := cloneActivationFactIDs(&activation{token: valueToken}), []FactID{firstFact.ID(), secondFact.ID(), FactID{}}; !slices.Equal(got, want) {
		t.Fatalf("value token materialized fact IDs = %#v; want %#v", got, want)
	}

	otherArena := newTokenArena()
	otherFirst := otherArena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	otherSecond := otherArena.add(otherFirst, secondEntry, conditionMatch{bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	if !tokenRefEqual(second, otherSecond) {
		t.Fatal("equivalent token refs should compare equal")
	}
	if !terminalTokenFactVersionsEqual(second, otherSecond) {
		t.Fatal("equivalent terminal token fact spans should compare equal")
	}
}

func TestTerminalTokenIdentityUsesCachedOrderedSlots(t *testing.T) {
	rule := compiledRule{
		id:                "rule",
		revisionID:        "rule-revision",
		identityScopeHash: candidateIdentityScopeHash("rule", "rule-revision"),
		conditions: []RuleCondition{
			{id: "first", binding: "first", order: 0},
			{id: "second", binding: "second", order: 1},
		},
		conditionPlans: []compiledConditionPlan{
			{id: "first", binding: "first", bindingSlot: 0, path: []int{0}},
			{id: "second", binding: "second", bindingSlot: 1, path: []int{1}},
		},
	}
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 3, recency: 10, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 5, recency: 11, generation: 1}
	firstMatch := conditionMatch{conditionID: "first", bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}
	secondMatch := conditionMatch{conditionID: "second", bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}
	firstEntry := rule.conditionPlans[0].bindingTupleEntry(firstMatch)
	secondEntry := rule.conditionPlans[1].bindingTupleEntry(secondMatch)

	arena := newTokenArena()
	firstToken := arena.add(tokenRef{}, firstEntry, firstMatch, firstFact.Recency(), firstFact.Generation())
	token := arena.add(firstToken, secondEntry, secondMatch, secondFact.Recency(), secondFact.Generation())
	if !token.orderedSlots() {
		t.Fatal("ordered token did not record ordered binding slots")
	}

	cached, ok := candidateIdentityForTerminalTokenCached(rule, token)
	if !ok {
		t.Fatal("candidateIdentityForTerminalTokenCached did not accept ordered token")
	}
	fast, ok := candidateIdentityForTerminalTokenFast(rule, token)
	if !ok {
		t.Fatal("candidateIdentityForTerminalTokenFast did not accept ordered token")
	}
	if cached != fast {
		t.Fatalf("cached identity = %#v, want existing fast identity %#v", cached, fast)
	}
	if got := candidateIdentityForTerminalToken(rule, token); got != cached {
		t.Fatalf("terminal token identity = %#v, want cached %#v", got, cached)
	}
}

func TestTerminalTokenIdentityRejectsCachedOutOfOrderSlots(t *testing.T) {
	rule := compiledRule{
		id:                "rule",
		revisionID:        "rule-revision",
		identityScopeHash: candidateIdentityScopeHash("rule", "rule-revision"),
		conditions: []RuleCondition{
			{id: "first", binding: "first", order: 0},
			{id: "second", binding: "second", order: 1},
		},
		conditionPlans: []compiledConditionPlan{
			{id: "first", binding: "first", bindingSlot: 0, path: []int{0}},
			{id: "second", binding: "second", bindingSlot: 1, path: []int{1}},
		},
	}
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 3, recency: 10, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 5, recency: 11, generation: 1}
	firstMatch := conditionMatch{conditionID: "first", bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}
	secondMatch := conditionMatch{conditionID: "second", bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}
	firstEntry := rule.conditionPlans[0].bindingTupleEntry(firstMatch)
	secondEntry := rule.conditionPlans[1].bindingTupleEntry(secondMatch)

	arena := newTokenArena()
	secondToken := arena.add(tokenRef{}, secondEntry, secondMatch, secondFact.Recency(), secondFact.Generation())
	token := arena.add(secondToken, firstEntry, firstMatch, firstFact.Recency(), firstFact.Generation())
	if token.orderedSlots() {
		t.Fatal("out-of-order token recorded ordered binding slots")
	}
	if _, ok := candidateIdentityForTerminalTokenCached(rule, token); ok {
		t.Fatal("cached identity accepted out-of-order token")
	}
	fast, ok := candidateIdentityForTerminalTokenFast(rule, token)
	if !ok {
		t.Fatal("candidateIdentityForTerminalTokenFast did not accept out-of-order token")
	}
	if got := candidateIdentityForTerminalToken(rule, token); got != fast {
		t.Fatalf("terminal token identity = %#v, want fast fallback %#v", got, fast)
	}
}

func TestTokenArenaResetInvalidatesReusedRows(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 3, recency: 10, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version(), conditionPath: []int{0}}

	stale := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	if _, ok := stale.resolve(); !ok {
		t.Fatal("token should resolve before reset")
	}

	arena.reset()
	if _, ok := stale.resolve(); ok {
		t.Fatal("token should not resolve after reset")
	}

	nextFact := FactSnapshot{id: newFactID(2, 1), version: 1, recency: 1, generation: 2}
	nextEntry := bindingTupleEntry{bindingSlot: 0, factID: nextFact.ID(), factVersion: nextFact.Version()}
	next := arena.add(tokenRef{}, nextEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(nextFact)}, nextFact.Recency(), nextFact.Generation())
	if _, ok := stale.resolve(); ok {
		t.Fatal("stale token should not resolve after row reuse")
	}
	if _, ok := next.resolve(); !ok {
		t.Fatal("new token should resolve after reset")
	}
}

func TestAgendaTerminalTokenReconcileMatchesCandidateReconcile(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-terminal-token-reconcile-session")

	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	})); err != nil {
		t.Fatalf("AssertTemplate(Ada): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"name": "Bob",
	})); err != nil {
		t.Fatalf("AssertTemplate(Bob): %v", err)
	}

	snapshot := mustSnapshot(t, ctx, session)
	results, err := session.rete.match(ctx, snapshot)
	if err != nil {
		t.Fatalf("match: %v", err)
	}

	candidateAgenda := newAgenda()
	candidateChanges, err := candidateAgenda.reconcile(ctx, revision, results)
	if err != nil {
		t.Fatalf("candidate reconcile: %v", err)
	}

	tokens, ok, err := session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok {
		t.Fatal("currentTerminalTokenDeltas unexpectedly unavailable for beta-backed session")
	}

	terminalAgenda := newAgenda()
	terminalChanges, err := terminalAgenda.reconcileTerminalTokens(ctx, revision, cloneTerminalTokenDeltas(tokens))
	if err != nil {
		t.Fatalf("reconcileTerminalTokens: %v", err)
	}

	if !agendaChangesPublicEqual(terminalAgenda, terminalChanges, candidateAgenda, candidateChanges) {
		t.Fatalf("terminal changes differ from candidate changes:\nterminal=%#v\ncandidate=%#v", terminalChanges, candidateChanges)
	}
	if !reflect.DeepEqual(terminalAgenda.pendingActivations(), candidateAgenda.pendingActivations()) {
		t.Fatalf("terminal pending activations differ from candidate reconcile:\nterminal=%#v\ncandidate=%#v", terminalAgenda.pendingActivations(), candidateAgenda.pendingActivations())
	}
}

func TestAgendaTerminalTokenReconcilePreservesConsumedAndDeactivatesMissingPending(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-terminal-token-deactivate-session")

	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	})); err != nil {
		t.Fatalf("AssertTemplate(Ada): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"name": "Bob",
	})); err != nil {
		t.Fatalf("AssertTemplate(Bob): %v", err)
	}

	tokens, ok, err := session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok {
		t.Fatal("currentTerminalTokenDeltas unexpectedly unavailable for beta-backed session")
	}

	agenda := newAgenda()
	changes, err := agenda.reconcileTerminalTokens(ctx, revision, cloneTerminalTokenDeltas(tokens))
	if err != nil {
		t.Fatalf("initial reconcileTerminalTokens: %v", err)
	}
	if got, want := len(changes), 2; got != want {
		t.Fatalf("initial terminal changes = %d, want %d", got, want)
	}

	consumed, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if consumed.status != activationStatusConsumed {
		t.Fatalf("consumed status = %v, want consumed", consumed.status)
	}
	remaining := agenda.pendingActivations()
	if got, want := len(remaining), 1; got != want {
		t.Fatalf("remaining pending activations = %d, want %d", got, want)
	}

	remainingFactIDs := remaining[0].factIDs()
	if _, err := session.Retract(ctx, remainingFactIDs[0]); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	tokens, ok, err = session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas after retract: %v", err)
	}
	if !ok {
		t.Fatal("currentTerminalTokenDeltas unexpectedly unavailable after retract")
	}

	changes, err = agenda.reconcileTerminalTokens(ctx, revision, cloneTerminalTokenDeltas(tokens))
	if err != nil {
		t.Fatalf("reconcileTerminalTokens after retract: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("terminal changes after retract = %d, want %d", got, want)
	}
	if changes[0].kind != agendaChangeDeactivated {
		t.Fatalf("change kind = %v, want deactivated", changes[0].kind)
	}
	changeActivation := agenda.publicActivation(&changes[0].activation)
	if got, want := changeActivation.factIDs()[0], remainingFactIDs[0]; got != want {
		t.Fatalf("deactivated fact ID = %q, want %q", got, want)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after missing token reconcile = %#v, want none", got)
	}
	if got, ok := agenda.activationByKey(consumed.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after missing token reconcile = %#v, ok=%v", got, ok)
	}
}

func TestAgendaCandidateDeltasReturnStableChangesWhenScratchIsReused(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-delta-scratch-session")

	first, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(Ada): %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Bob",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(Bob): %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	if len(results) != 1 || len(results[0].candidates) != 2 {
		t.Fatalf("match results = %#v, want two candidates", results)
	}

	firstCandidate := mustCandidateForFactID(t, results[0].candidates, first.Fact.ID())
	secondCandidate := mustCandidateForFactID(t, results[0].candidates, second.Fact.ID())
	firstChanges, err := agenda.applyCandidateDeltas(context.Background(), revision, []matchCandidate{firstCandidate}, nil)
	if err != nil {
		t.Fatalf("apply first delta: %v", err)
	}
	if got, want := len(firstChanges), 1; got != want {
		t.Fatalf("first delta changes = %d, want %d", got, want)
	}
	if got := cloneActivationFactIDs(&firstChanges[0].activation)[0]; got != first.Fact.ID() {
		t.Fatalf("first change fact ID = %q, want %q", got, first.Fact.ID())
	}
	if agenda.deltaRemovedKeys == nil || cap(agenda.deltaChanges) == 0 || cap(agenda.deltaNextPending) == 0 {
		t.Fatalf("agenda delta scratch not retained: removed=%#v changesCap=%d pendingCap=%d", agenda.deltaRemovedKeys, cap(agenda.deltaChanges), cap(agenda.deltaNextPending))
	}

	secondChanges, err := agenda.applyCandidateDeltas(context.Background(), revision, []matchCandidate{secondCandidate}, nil)
	if err != nil {
		t.Fatalf("apply second delta: %v", err)
	}
	if got, want := len(secondChanges), 1; got != want {
		t.Fatalf("second delta changes = %d, want %d", got, want)
	}
	if got := cloneActivationFactIDs(&secondChanges[0].activation)[0]; got != second.Fact.ID() {
		t.Fatalf("second change fact ID = %q, want %q", got, second.Fact.ID())
	}
	if got := cloneActivationFactIDs(&firstChanges[0].activation)[0]; got != first.Fact.ID() {
		t.Fatalf("first returned change was mutated after scratch reuse: got %q want %q", got, first.Fact.ID())
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
	if pending[0].activationID() == pending[1].activationID() {
		t.Fatalf("colliding activations reused public ID %q", pending[0].activationID())
	}

	changes, err = agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("duplicate collision reconcile: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("duplicate collision reconcile changes = %#v, want none", changes)
	}
}

func TestAgendaActivationFingerprintIncludesScopeHash(t *testing.T) {
	revision, _ := mustAgendaRevision(t, 10)
	rule := revision.rules["match-person"]
	firstIdentity := candidateIdentity{
		generation: 1,
		count:      1,
		key: candidateIdentityKey{
			scopeHash: 100,
			hash:      42,
		},
	}
	secondIdentity := candidateIdentity{
		generation: 1,
		count:      1,
		key: candidateIdentityKey{
			scopeHash: 200,
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
				mustCollisionCandidate(rule.id, rule.revisionID, firstIdentity, firstID, 1, 1),
				mustCollisionCandidate(rule.id, rule.revisionID, secondIdentity, secondID, 1, 2),
			},
		},
	}

	agenda := newAgenda()
	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, want := len(changes), 2; got != want {
		t.Fatalf("scoped fingerprint changes = %d, want %d", got, want)
	}
	if got, want := len(agenda.activations), 2; got != want {
		t.Fatalf("activation fingerprint buckets = %d, want %d", got, want)
	}
	pending := agenda.pendingActivations()
	if got, want := len(pending), 2; got != want {
		t.Fatalf("pending fingerprint collision activations = %d, want %d", got, want)
	}
	if pending[0].activationID() == pending[1].activationID() {
		t.Fatalf("fingerprint collision reused public activation ID %q", pending[0].activationID())
	}
	if pending[0].identityKey == pending[1].identityKey {
		t.Fatalf("test did not create distinct full identities: %#v", pending)
	}

	changes, err = agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("duplicate reconcile: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("duplicate scoped fingerprint changes = %#v, want none", changes)
	}
}

func TestAgendaScopedActivationFingerprintDoesNotRequeueConsumedActivation(t *testing.T) {
	revision, _ := mustAgendaRevision(t, 10)
	rule := revision.rules["match-person"]
	firstIdentity := candidateIdentity{
		generation: 1,
		count:      1,
		key: candidateIdentityKey{
			scopeHash: 100,
			hash:      42,
		},
	}
	secondIdentity := candidateIdentity{
		generation: 1,
		count:      1,
		key: candidateIdentityKey{
			scopeHash: 200,
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
				mustCollisionCandidate(rule.id, rule.revisionID, firstIdentity, firstID, 1, 1),
				mustCollisionCandidate(rule.id, rule.revisionID, secondIdentity, secondID, 1, 2),
			},
		},
	}

	agenda := newAgenda()
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

	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("reconcile after consume: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("reconcile after consume changes = %#v, want none", changes)
	}
	pending := agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending after consumed scoped activation = %d, want %d", got, want)
	}
	if pending[0].activationID() == selected.activationID() {
		t.Fatalf("consumed activation was requeued: %q", selected.activationID())
	}
	if got, ok := agenda.activationByKey(selected.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after scoped reconcile = %#v, ok=%v", got, ok)
	}
}

func TestAgendaIndexesSuppressRepeatedFactIDs(t *testing.T) {
	agenda := newAgenda()
	factID := newFactID(1, 1)
	activation := activation{
		ruleRevisionID: RuleRevisionID("revision"),
		identityKey:    candidateIdentityKey{scopeHash: 1, hash: 2},
		status:         activationStatusPending,
	}
	activation.setFactIDs([]FactID{factID, factID})
	activation.setFactVersions([]FactVersion{1, 1})

	agenda.storeActivation(&activation)

	if got, want := agenda.byFactID[factID].len(), 1; got != want {
		t.Fatalf("fact index keys = %d, want %d", got, want)
	}
	if got, want := len(agenda.activationsByFactID(factID)), 1; got != want {
		t.Fatalf("fact index activations = %d, want %d", got, want)
	}
	if got, want := agenda.byRevision[activation.ruleRevisionID].len(), 1; got != want {
		t.Fatalf("revision index keys = %d, want %d", got, want)
	}
	if got, want := len(agenda.activationsByRuleRevisionID(activation.ruleRevisionID)), 1; got != want {
		t.Fatalf("revision index activations = %d, want %d", got, want)
	}
}

func TestActivationKeyBucketSupportsInlineSecondAndReset(t *testing.T) {
	var bucket activationKeyBucket
	first := activationKey{fingerprint: 1, ordinal: 1}
	second := activationKey{fingerprint: 2, ordinal: 2}
	third := activationKey{fingerprint: 3, ordinal: 3}

	if got := bucket.len(); got != 0 {
		t.Fatalf("empty bucket len = %d, want 0", got)
	}

	bucket.append(first)
	if got := bucket.len(); got != 1 {
		t.Fatalf("single-item bucket len = %d, want 1", got)
	}
	if bucket.first != first {
		t.Fatalf("bucket first = %#v, want %#v", bucket.first, first)
	}
	if bucket.second != (activationKey{}) {
		t.Fatalf("bucket second after first append = %#v, want zero", bucket.second)
	}
	if bucket.overflow != nil {
		t.Fatalf("bucket overflow after first append = %#v, want nil", bucket.overflow)
	}

	bucket.append(second)
	if got := bucket.len(); got != 2 {
		t.Fatalf("two-item bucket len = %d, want 2", got)
	}
	if bucket.first != first {
		t.Fatalf("bucket first after second append = %#v, want %#v", bucket.first, first)
	}
	if bucket.second != second {
		t.Fatalf("bucket second after second append = %#v, want %#v", bucket.second, second)
	}
	if bucket.overflow != nil {
		t.Fatalf("bucket overflow after second append = %#v, want nil", bucket.overflow)
	}

	got := make([]activationKey, 0, 3)
	bucket.forEach(func(key activationKey) {
		got = append(got, key)
	})
	if !reflect.DeepEqual(got, []activationKey{first, second}) {
		t.Fatalf("bucket iteration after two appends = %#v, want %#v", got, []activationKey{first, second})
	}

	bucket.append(third)
	if got := bucket.len(); got != 3 {
		t.Fatalf("overflow bucket len = %d, want 3", got)
	}
	if len(bucket.overflow) != 1 || bucket.overflow[0] != third {
		t.Fatalf("bucket overflow after third append = %#v, want [%#v]", bucket.overflow, third)
	}

	got = got[:0]
	bucket.forEach(func(key activationKey) {
		got = append(got, key)
	})
	if !reflect.DeepEqual(got, []activationKey{first, second, third}) {
		t.Fatalf("bucket iteration after overflow append = %#v, want %#v", got, []activationKey{first, second, third})
	}

	reset := bucket.reset()
	if got := reset.len(); got != 0 {
		t.Fatalf("reset bucket len = %d, want 0", got)
	}
	if reset.first != (activationKey{}) {
		t.Fatalf("reset bucket first = %#v, want zero", reset.first)
	}
	if reset.second != (activationKey{}) {
		t.Fatalf("reset bucket second = %#v, want zero", reset.second)
	}
	if got := len(reset.overflow); got != 0 {
		t.Fatalf("reset bucket overflow len = %d, want 0", got)
	}

	got = got[:0]
	reset.forEach(func(key activationKey) {
		got = append(got, key)
	})
	if len(got) != 0 {
		t.Fatalf("reset bucket iteration = %#v, want none", got)
	}
}

func TestAgendaPurgeRuleRevisionsRemovesPurgedActivationsFromAllIndexes(t *testing.T) {
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-purge-session")

	first, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(Ada): %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{
		"name": "Bob",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(Bob): %v", err)
	}

	agenda := newAgenda()
	results := mustAgendaMatchResults(t, revision, session)
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	consumed, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if consumed.status != activationStatusConsumed {
		t.Fatalf("consumed status = %v, want consumed", consumed.status)
	}
	remaining := agenda.pendingActivations()
	if got, want := len(remaining), 1; got != want {
		t.Fatalf("remaining pending activations = %d, want %d", got, want)
	}

	changes := agenda.purgeRuleRevisions(map[RuleRevisionID]struct{}{
		consumed.ruleRevisionID: {},
	})
	if got, want := len(changes), 1; got != want {
		t.Fatalf("purge changes = %d, want %d", got, want)
	}
	if changes[0].kind != agendaChangeDeactivated {
		t.Fatalf("purge change kind = %v, want deactivated", changes[0].kind)
	}
	if changes[0].activation.activationID() != remaining[0].activationID() {
		t.Fatalf("purge deactivated activation = %q, want %q", changes[0].activation.activationID(), remaining[0].activationID())
	}
	if changes[0].activation.status != activationStatusDeactivated {
		t.Fatalf("purge deactivated status = %v, want deactivated", changes[0].activation.status)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after purge = %#v, want none", got)
	}
	if got := len(agenda.activations); got != 0 {
		t.Fatalf("activation map size after purge = %d, want 0", got)
	}
	if got := agenda.activationsByRuleRevisionID(consumed.ruleRevisionID); len(got) != 0 {
		t.Fatalf("rule revision activations after purge = %#v, want none", got)
	}
	if got := len(agenda.byFactID); got != 0 {
		t.Fatalf("fact index map size after purge = %d, want 0", got)
	}
	if got := len(agenda.byRevision); got != 0 {
		t.Fatalf("revision index map size after purge = %d, want 0", got)
	}
	if got, ok := agenda.activationByKey(consumed.key); ok {
		t.Fatalf("consumed activation still reachable after purge: %#v", got)
	}
	if got, ok := agenda.activationByKey(remaining[0].key); ok {
		t.Fatalf("pending activation still reachable after purge: %#v", got)
	}
	if got := agenda.byFactID[first.Fact.ID()].len(); got != 0 {
		t.Fatalf("fact index for %q after purge = %d, want 0", first.Fact.ID(), got)
	}
	if got := agenda.byFactID[second.Fact.ID()].len(); got != 0 {
		t.Fatalf("fact index for %q after purge = %d, want 0", second.Fact.ID(), got)
	}
	if got := agenda.byRevision[consumed.ruleRevisionID].len(); got != 0 {
		t.Fatalf("revision index after purge = %d, want 0", got)
	}
}

func TestAgendaPurgeRuleRevisionsPromotesSurvivingOverflowActivation(t *testing.T) {
	agenda := newAgenda()
	removedFactID := newFactID(1, 1)
	keptFactID := newFactID(1, 2)
	removed := activation{
		ruleRevisionID: RuleRevisionID("removed"),
		identityKey:    candidateIdentityKey{scopeHash: 100, hash: 42},
		status:         activationStatusPending,
	}
	removed.setFactIDs([]FactID{removedFactID})
	removed.setFactVersions([]FactVersion{1})
	kept := activation{
		ruleRevisionID: RuleRevisionID("kept"),
		identityKey:    candidateIdentityKey{scopeHash: 200, hash: 42},
		status:         activationStatusPending,
	}
	kept.setFactIDs([]FactID{keptFactID})
	kept.setFactVersions([]FactVersion{1})

	removedKey := agenda.storeActivation(&removed)
	keptKey := agenda.storeActivation(&kept)
	agenda.pending = append(agenda.pending, removedKey, keptKey)

	changes := agenda.purgeRuleRevisions(map[RuleRevisionID]struct{}{
		removed.ruleRevisionID: {},
	})
	if got, want := len(changes), 1; got != want {
		t.Fatalf("purge changes = %d, want %d", got, want)
	}
	if changes[0].activation.key != removedKey {
		t.Fatalf("purged activation key = %#v, want %#v", changes[0].activation.key, removedKey)
	}
	if _, ok := agenda.activationByKey(removedKey); ok {
		t.Fatalf("removed activation key %#v is still reachable", removedKey)
	}
	gotKept, ok := agenda.activationByKey(keptKey)
	if !ok {
		t.Fatalf("kept activation key %#v is not reachable", keptKey)
	}
	if gotKept.status != activationStatusPending {
		t.Fatalf("kept activation status = %v, want pending", gotKept.status)
	}
	pending := agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after purge = %d, want %d", got, want)
	}
	if pending[0].key != keptKey {
		t.Fatalf("pending activation key = %#v, want %#v", pending[0].key, keptKey)
	}
	if got := agenda.activationsByFactID(removedFactID); len(got) != 0 {
		t.Fatalf("removed fact activations after purge = %#v, want none", got)
	}
	if got := agenda.activationsByFactID(keptFactID); len(got) != 1 || got[0].key != keptKey {
		t.Fatalf("kept fact activations after purge = %#v, want kept activation", got)
	}
	if got := agenda.activationsByRuleRevisionID(removed.ruleRevisionID); len(got) != 0 {
		t.Fatalf("removed revision activations after purge = %#v, want none", got)
	}
	if got := agenda.activationsByRuleRevisionID(kept.ruleRevisionID); len(got) != 1 || got[0].key != keptKey {
		t.Fatalf("kept revision activations after purge = %#v, want kept activation", got)
	}
	ref := agenda.activations[keptKey.fingerprint]
	if ref.isZero() {
		t.Fatal("activation ref after purge is zero")
	}
	if stored, ok := agenda.activationByOrdinalRef(ref); !ok || stored.key != keptKey {
		t.Fatalf("activation ref after purge = (%#v, %v), want kept key %#v", stored, ok, keptKey)
	}
	if bucket := agenda.activationCollisions[keptKey.fingerprint]; bucket.len() != 0 {
		t.Fatalf("collision bucket after purge = %d, want 0", bucket.len())
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
	if changes[0].activation.activationID() != initial.activationID() {
		t.Fatalf("deactivated activation ID = %q, want %q", changes[0].activation.activationID(), initial.activationID())
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
	if pending[0].activationID() == selected.activationID() {
		t.Fatalf("reset reused consumed activation ID %q", selected.activationID())
	}
	if pending[0].Generation() != 2 {
		t.Fatalf("reset activation generation = %d, want 2", pending[0].Generation())
	}
	byRevision := session.agenda.activationsByRuleRevisionID(selected.ruleRevisionID)
	if got, want := len(byRevision), 1; got != want {
		t.Fatalf("activations by revision after reset = %d, want %d", got, want)
	}
	if byRevision[0].activationID() != pending[0].activationID() {
		t.Fatalf("revision index activation = %q, want %q", byRevision[0].activationID(), pending[0].activationID())
	}

	results = mustAgendaMatchResults(t, revision, session)
	changes, err := session.agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		t.Fatalf("post-reset reconcile: %v", err)
	}
	if got := len(changes); got != 0 {
		t.Fatalf("post-reset reconcile changes = %d, want none", got)
	}
	if got := session.agenda.pendingActivations(); len(got) != 1 || got[0].activationID() == selected.activationID() {
		t.Fatalf("post-reset pending activations = %#v, want new activation ID", got)
	}
}

func TestAgendaReplacementUsesNewRevisionIdentityAndDoesNotShareRefractionState(t *testing.T) {
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
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
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
	if changes[0].activation.activationID() == first.activationID() {
		t.Fatalf("new activation ID reused consumed activation ID %q", first.activationID())
	}
	if got := agenda.pendingActivations(); len(got) != 1 || got[0].ruleRevisionID != rule2.RevisionID() {
		t.Fatalf("pending activations after revision replace = %#v", got)
	}
	if got, ok := agenda.activationByKey(first.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after revision replace = %#v, ok=%v", got, ok)
	}
}

func TestActivationLessOrdersBySalienceRecencyAndCompactIdentity(t *testing.T) {
	acts := []activation{
		{
			salience:         20,
			maxRecency:       1,
			aggregateRecency: 1,
			identityKey:      candidateIdentityKey{hash: 5},
		},
		{
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 9,
			identityKey:      candidateIdentityKey{hash: 4},
		},
		{
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
			identityKey:      candidateIdentityKey{hash: 3},
		},
		{
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
			identityKey:      candidateIdentityKey{hash: 2},
		},
		{
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
			identityKey:      candidateIdentityKey{hash: 1},
		},
	}

	sort.SliceStable(acts, func(i, j int) bool {
		return activationLess(&acts[i], &acts[j])
	})

	got := []uint64{acts[0].identityKey.hash, acts[1].identityKey.hash, acts[2].identityKey.hash, acts[3].identityKey.hash, acts[4].identityKey.hash}
	want := []uint64{5, 4, 1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted activation %d hash = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestActivationLessOrdersLazyActivationsByCompactIdentity(t *testing.T) {
	base := activation{
		salience:         10,
		maxRecency:       9,
		aggregateRecency: 8,
	}
	tests := []struct {
		name  string
		left  activation
		right activation
		want  bool
	}{
		{
			name: "scope prefix segment",
			left: func() activation {
				act := base
				act.identityKey = candidateIdentityKey{scopeHash: 10, hash: 1}
				return act
			}(),
			right: func() activation {
				act := base
				act.identityKey = candidateIdentityKey{scopeHash: 1, hash: 1}
				return act
			}(),
			want: false,
		},
		{
			name: "hash prefix segment",
			left: func() activation {
				act := base
				act.identityKey = candidateIdentityKey{scopeHash: 2, hash: 10}
				return act
			}(),
			right: func() activation {
				act := base
				act.identityKey = candidateIdentityKey{scopeHash: 2, hash: 1}
				return act
			}(),
			want: false,
		},
		{
			name: "internal activation key ordinal",
			left: func() activation {
				act := base
				act.identityKey = candidateIdentityKey{scopeHash: 2, hash: 3}
				act.key.ordinal = 1
				return act
			}(),
			right: func() activation {
				act := base
				act.identityKey = candidateIdentityKey{scopeHash: 2, hash: 3}
				act.key.ordinal = 10
				return act
			}(),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := activationLess(&tt.left, &tt.right)
			if got != tt.want {
				t.Fatalf("activationLess(%#v, %#v) = %v, want %v", tt.left.identityKey, tt.right.identityKey, got, tt.want)
			}
		})
	}
}

func TestAgendaChangeEventsKeepFactEventsBare(t *testing.T) {
	activation := activation{
		ruleRevisionID: RuleRevisionID("revision"),
		identityKey:    candidateIdentityKey{scopeHash: 1, hash: 2},
		key:            activationKey{ordinal: 3},
	}
	activation.setFactIDs([]FactID{newFactID(1, 2)})
	change := agendaChange{
		kind:       agendaChangeActivated,
		activation: activation,
	}

	event := change.eventWithRuleID(SessionID("session"), RulesetID("ruleset"), RuleID("rule"), 3, time.Unix(2, 0).UTC())
	if event.Type != EventRuleActivated {
		t.Fatalf("event type = %q, want %q", event.Type, EventRuleActivated)
	}
	if event.RuleID != "rule" || event.RuleRevisionID != "revision" || event.ActivationID != activation.activationID() {
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
	activationID := pending[0].activationID()
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
	initialID := changes[0].activation.activationID()

	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	if pending[0].activationID() == initialID {
		t.Fatalf("post-reset activation reused old activation ID %q", initialID)
	}
	if pending[0].Generation() != 2 {
		t.Fatalf("post-reset activation generation = %d, want 2", pending[0].Generation())
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

func TestSessionGraphResetAppliesAgendaDeltasWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("graph-reset-agenda-delta-session"),
		WithEventListener(collector),
		WithEventClock(countingClock()),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: templateKey,
			Fields: mustFields(t, map[string]any{
				"age":    32,
				"note":   "old",
				"status": "active",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	changes, ok, err := session.reconcileAgendaWithoutSnapshot(ctx)
	if err != nil {
		t.Fatalf("initial reconcileAgendaWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("initial graph terminal reconcile unavailable")
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("initial changes = %d, want %d", got, want)
	}
	initialID := changes[0].activation.activationID()
	beforeCounters := session.propagationCounterSnapshot().Totals

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	if pending[0].activationID() == initialID {
		t.Fatalf("post-reset activation reused old activation ID %q", initialID)
	}
	if pending[0].Generation() != 2 {
		t.Fatalf("post-reset activation generation = %d, want 2", pending[0].Generation())
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

	afterCounters := session.propagationCounterSnapshot().Totals
	if got, want := afterCounters.FullAgendaReconciles, beforeCounters.FullAgendaReconciles; got != want {
		t.Fatalf("reset full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.WholeTerminalScans, beforeCounters.WholeTerminalScans; got != want {
		t.Fatalf("reset whole terminal scans = %d, want unchanged %d", got, want)
	}
}

func TestSessionGraphResetWithoutListenersKeepsAgendaReadyWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("graph-reset-no-listener-agenda-delta-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: templateKey,
			Fields: mustFields(t, map[string]any{
				"age":    32,
				"note":   "old",
				"status": "active",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	if session.agendaDirty || !session.agendaReady {
		t.Fatalf("initial agenda state = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("initial pending activations = %d, want %d", got, want)
	}
	changes, ok, err := session.reconcileAgendaWithoutSnapshot(ctx)
	if err != nil {
		t.Fatalf("initial reconcileAgendaWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("initial graph terminal reconcile unavailable")
	}
	if got, want := len(changes), 0; got != want {
		t.Fatalf("initial changes = %d, want %d", got, want)
	}
	beforeCounters := session.propagationCounterSnapshot().Totals

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.agendaDirty || !session.agendaReady {
		t.Fatalf("agenda state after reset = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}

	afterCounters := session.propagationCounterSnapshot().Totals
	if got, want := afterCounters.FullAgendaReconciles, beforeCounters.FullAgendaReconciles; got != want {
		t.Fatalf("reset full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.WholeTerminalScans, beforeCounters.WholeTerminalScans; got != want {
		t.Fatalf("reset whole terminal scans = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.AgendaDeltaApplications, beforeCounters.AgendaDeltaApplications; got != want {
		t.Fatalf("reset agenda delta applications = %d, want unchanged %d", got, want)
	}
}

func TestSessionGraphResetAppliesJoinedTerminalRemovalsWithStableFacts(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID("graph-reset-join-agenda-delta-session"),
		WithEventListener(collector),
		WithEventClock(countingClock()),
		WithInitialFacts(
			SessionInitialFact{TemplateKey: employeeKey, Fields: mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})},
			SessionInitialFact{TemplateKey: departmentKey, Fields: mustFields(t, map[string]any{"id": "Engineering"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	changes, ok, err := session.reconcileAgendaWithoutSnapshot(ctx)
	if err != nil {
		t.Fatalf("initial reconcileAgendaWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("initial graph terminal reconcile unavailable")
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("initial changes = %d, want %d", got, want)
	}
	initialID := changes[0].activation.activationID()
	if got, want := len(changes[0].activation.factIDs()), 2; got != want {
		t.Fatalf("initial activation fact IDs = %d, want joined token width %d", got, want)
	}
	beforeCounters := session.propagationCounterSnapshot().Totals

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	if pending[0].activationID() == initialID {
		t.Fatalf("post-reset activation reused old activation ID %q", initialID)
	}
	if got, want := len(pending[0].factIDs()), 2; got != want {
		t.Fatalf("post-reset activation fact IDs = %d, want joined token width %d", got, want)
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
	if got, want := len(events[1].FactIDs), 2; got != want {
		t.Fatalf("reset deactivation fact IDs = %d, want joined token width %d", got, want)
	}

	afterCounters := session.propagationCounterSnapshot().Totals
	if got, want := afterCounters.FullAgendaReconciles, beforeCounters.FullAgendaReconciles; got != want {
		t.Fatalf("reset full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.WholeTerminalScans, beforeCounters.WholeTerminalScans; got != want {
		t.Fatalf("reset whole terminal scans = %d, want unchanged %d", got, want)
	}
}

func TestSessionGraphResetAgendaSurvivesResetListenerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	revision, templateKey := mustModifyFastPathRuleset(t)
	collector := &testEventCollector{}
	collector.onEvent = func(_ context.Context, event Event) error {
		if event.Type == EventReset {
			cancel()
			return context.Canceled
		}
		return nil
	}
	session, err := NewSession(
		revision,
		WithSessionID("graph-reset-cancel-listener-session"),
		WithEventListener(collector),
		WithEventClock(countingClock()),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: templateKey,
			Fields: mustFields(t, map[string]any{
				"age":    32,
				"note":   "old",
				"status": "active",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	if _, ok, err := session.reconcileAgendaWithoutSnapshot(ctx); err != nil {
		t.Fatalf("initial reconcileAgendaWithoutSnapshot: %v", err)
	} else if !ok {
		t.Fatal("initial graph terminal reconcile unavailable")
	}
	beforeCounters := session.propagationCounterSnapshot().Totals

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("reset listener did not cancel context")
	}
	if session.agendaDirty || !session.agendaReady {
		t.Fatalf("agenda state after reset = dirty %v ready %v, want clean ready", session.agendaDirty, session.agendaReady)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d: %#v", got, want, events)
	}
	if events[0].Type != EventRuleActivated || events[1].Type != EventRuleDeactivated || events[2].Type != EventReset || events[3].Type != EventRuleActivated {
		t.Fatalf("event order = %#v", []EventType{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	}

	afterCounters := session.propagationCounterSnapshot().Totals
	if got, want := afterCounters.FullAgendaReconciles, beforeCounters.FullAgendaReconciles; got != want {
		t.Fatalf("reset full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := afterCounters.WholeTerminalScans, beforeCounters.WholeTerminalScans; got != want {
		t.Fatalf("reset whole terminal scans = %d, want unchanged %d", got, want)
	}
}

func countingClock() func() time.Time {
	var tick int64
	return func() time.Time {
		tick++
		return time.Unix(tick, 0).UTC()
	}
}

func mustAgendaRevision(t testing.TB, salience int) (*Ruleset, TemplateKey) {
	t.Helper()
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
		Salience: salience,
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, template.Key()
}

func mustAgendaStatusRevision(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "task",
		Fields: []FieldSpec{{Name: "status", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-open-task",
		Conditions: []RuleConditionSpec{{
			Binding: "task",
			Target:  TemplateKeyFact(template.Key()),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "status",
				Operator: FieldConstraintEqual,
				Value:    "open",
			}},
		}},
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

func compactGraphActivationRuleName(t testing.TB, revision *Ruleset, activation activation) string {
	t.Helper()
	rule, ok := revision.rulesByRevisionID[activation.ruleRevisionID]
	if !ok {
		t.Fatalf("rule revision %q missing", activation.ruleRevisionID)
	}
	return rule.name
}

func mustCandidateForFactID(t testing.TB, candidates []matchCandidate, id FactID) matchCandidate {
	t.Helper()
	for _, candidate := range candidates {
		if slices.Contains(candidate.factIDs, id) {
			return candidate
		}
	}
	t.Fatalf("did not find candidate for fact %q", id)
	return matchCandidate{}
}

func agendaChangesPublicEqual(leftAgenda *agenda, left []agendaChange, rightAgenda *agenda, right []agendaChange) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].kind != right[i].kind {
			return false
		}
		leftActivation := compactAgendaChangeActivationForCompare(leftAgenda, &left[i].activation)
		rightActivation := compactAgendaChangeActivationForCompare(rightAgenda, &right[i].activation)
		if !reflect.DeepEqual(leftActivation, rightActivation) {
			return false
		}
	}
	return true
}

func compactAgendaChangeActivationForCompare(owner *agenda, act *activation) activation {
	if act == nil {
		return activation{}
	}
	out := *act
	if owner != nil {
		public := owner.publicActivation(act)
		out.payload = nil
		out.setFactIDs(cloneFactIDs(public.factIDs()))
		out.setFactVersions(cloneFactVersions(public.factVersions()))
		out.setBindings(cloneBindingTupleEntries(public.bindings()))
		out.setPath(cloneIntPath(public.path()))
	} else {
		out.setFactIDs(cloneActivationFactIDs(act))
	}
	out.payload = nil
	out.token = tokenRef{}
	return out
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
