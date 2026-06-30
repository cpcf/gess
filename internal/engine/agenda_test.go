package engine

import (
	"context"
	"reflect"
	"slices"
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
	if activated.activation.activationID() == initial.id {
		t.Fatalf("activation ID did not change after fact version changed: %q", activated.activation.activationID())
	}
	if len(activated.activation.bindings()) != 0 || len(activated.activation.path()) != 0 || len(activated.activation.factVersions()) != 0 {
		t.Fatalf("activated change should stay compact: %#v", activated.activation)
	}
	if deactivated.activation.activationID() != initial.id {
		t.Fatalf("deactivated activation ID = %q, want %q", deactivated.activation.activationID(), initial.id)
	}
	if deactivated.activation.status != activationStatusDeactivated {
		t.Fatalf("deactivated status = %v, want deactivated", deactivated.activation.status)
	}
	if len(deactivated.activation.bindings()) != 0 || len(deactivated.activation.path()) != 0 || len(deactivated.activation.factVersions()) != 0 {
		t.Fatalf("deactivated change should stay compact: %#v", deactivated.activation)
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
		publicOrdinal:    3,
		ruleRevisionID:   "rule-1@1",
		generation:       4,
		identity:         candidateIdentity{key: candidateIdentityKey{scopeHash: 5, hash: 6}},
		terminalID:       7,
		terminalRow:      graphTokenRowHandle{id: 8, generation: 9},
		salience:         11,
		maxRecency:       12,
		aggregateRecency: 13,
		status:           activationStatusPending,
	}
	firstHandle, stored := arena.add(first)
	if firstHandle.isZero() {
		t.Fatal("first compact handle is zero")
	}
	if stored == nil || stored.ruleRevisionID != first.ruleRevisionID || stored.terminalRow != first.terminalRow {
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

func TestAgendaTerminalTokenDeltaBatchAttachesActivationHandles(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAgendaRevision(t, 10)
	session := mustSession(t, revision, "agenda-token-delta-batch-handle-session")

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

	type attachedActivation struct {
		delta  reteTerminalTokenDelta
		handle activationHandle
	}
	attached := make([]attachedActivation, 0, len(tokens))
	agenda := newAgenda()
	if _, err := agenda.applyTerminalTokenDeltasInternal(ctx, revision, nil, cloneTerminalTokenDeltas(tokens), false, func(delta reteTerminalTokenDelta, handle activationHandle, _ *activation) {
		attached = append(attached, attachedActivation{delta: delta, handle: handle})
	}); err != nil {
		t.Fatalf("applyTerminalTokenDeltasInternal: %v", err)
	}
	if got, want := len(attached), 2; got != want {
		t.Fatalf("attached handles = %d, want %d", got, want)
	}
	if got, want := len(agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations = %d, want %d", got, want)
	}
	for i, attachment := range attached {
		if attachment.delta.terminalID == 0 || attachment.delta.terminalRow.isZero() {
			t.Fatalf("attachment %d terminal ownership = %#v", i, attachment.delta)
		}
		if attachment.handle.isZero() {
			t.Fatalf("attachment %d handle is zero", i)
		}
		activation, ok := agenda.activationByHandlePtr(attachment.handle)
		if !ok || activation.status != activationStatusPending {
			t.Fatalf("attachment %d activation = %#v, ok=%v, want pending", i, activation, ok)
		}
		if activation.terminalID != attachment.delta.terminalID || activation.terminalRow != attachment.delta.terminalRow {
			t.Fatalf("attachment %d activation terminal ownership = (%d, %#v), want (%d, %#v)", i, activation.terminalID, activation.terminalRow, attachment.delta.terminalID, attachment.delta.terminalRow)
		}
	}

	removed := make([]reteTerminalTokenDelta, len(attached))
	for i, attachment := range attached {
		removed[i] = attachment.delta
		removed[i].activation = attachment.handle
	}
	if err := agenda.applyTerminalTokenDeltasWithoutChanges(ctx, revision, removed, nil); err != nil {
		t.Fatalf("remove by attached handles: %v", err)
	}
	if got := agenda.pendingActivations(); len(got) != 0 {
		t.Fatalf("pending activations after handle removals = %#v, want none", got)
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
	if pending[0].id.IsZero() {
		t.Fatal("public pending activation ID is zero")
	}
	if stored, ok := agenda.activationByKeyPtr(pending[0].key); !ok {
		t.Fatal("stored pending activation missing")
	} else if !stored.id.IsZero() {
		t.Fatalf("stored pending activation cached ID = %q, want derived only", stored.id)
	}

	selected, ok := agenda.next()
	if !ok {
		t.Fatal("next returned no activation")
	}
	if selected.id.IsZero() {
		t.Fatal("selected public activation ID is zero")
	}
	stored, ok := agenda.activationByKeyPtr(selected.key)
	if !ok {
		t.Fatal("stored consumed activation missing")
	}
	if stored.status != activationStatusConsumed {
		t.Fatalf("stored activation status = %v, want consumed", stored.status)
	}
	if !stored.id.IsZero() {
		t.Fatalf("stored consumed activation cached ID = %q, want derived only", stored.id)
	}
	if stored.payload != nil {
		t.Fatalf("stored consumed activation payload = %#v, want nil", stored.payload)
	}
	if stored.token.isZero() {
		t.Fatal("stored consumed activation lost token ref")
	}
	if got := stored.mutationOrigin().activationID(); got != selected.id {
		t.Fatalf("stored mutation origin activation ID = %q, want %q", got, selected.id)
	}
	actionCtx := newActionContext(ctx, nil, selected, nil)
	origin := actionCtx.mutationOrigin()
	if !origin.ActivationID.IsZero() {
		t.Fatalf("action mutation origin cached ID = %q, want compact derived identity", origin.ActivationID)
	}
	if got := origin.activationID(); got != selected.id {
		t.Fatalf("action mutation origin activation ID = %q, want %q", got, selected.id)
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

func TestTokenArenaCachesFactSpans(t *testing.T) {
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
	if got, ok := second.factIDs(); !ok || !slices.Equal(got, []FactID{firstFact.ID(), secondFact.ID()}) {
		t.Fatalf("token fact IDs = %#v, %v; want cached joined fact IDs", got, ok)
	}
	if got, ok := second.factVersions(); !ok || !slices.Equal(got, []FactVersion{firstFact.Version(), secondFact.Version()}) {
		t.Fatalf("token fact versions = %#v, %v; want cached joined fact versions", got, ok)
	}

	valueEntry := bindingTupleEntry{bindingSlot: 2, value: newIntValue(42), hasValue: true, conditionPath: []int{2}}
	valueToken := arena.add(second, valueEntry, conditionMatch{bindingSlot: 2, value: newIntValue(42), hasValue: true}, 0, secondFact.Generation())
	if got, ok := valueToken.factIDs(); !ok || !slices.Equal(got, []FactID{firstFact.ID(), secondFact.ID(), FactID{}}) {
		t.Fatalf("value token fact IDs = %#v, %v; want cached joined fact IDs with zero value row", got, ok)
	}

	otherArena := newTokenArena()
	otherFirst := otherArena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	otherSecond := otherArena.add(otherFirst, secondEntry, conditionMatch{bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	if !tokenRefEqual(second, otherSecond) {
		t.Fatal("equivalent token refs with cached fact spans should compare equal")
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
	if pending[0].id == pending[1].id {
		t.Fatalf("fingerprint collision reused public activation ID %q", pending[0].id)
	}
	if pending[0].identity.key == pending[1].identity.key {
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
	if pending[0].id == selected.id {
		t.Fatalf("consumed activation was requeued: %q", selected.id)
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
		identity: candidateIdentity{
			generation: 1,
			count:      2,
			key: candidateIdentityKey{
				scopeHash: 1,
				hash:      2,
			},
		},
		status: activationStatusPending,
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
	if changes[0].activation.activationID() != remaining[0].id {
		t.Fatalf("purge deactivated activation = %q, want %q", changes[0].activation.activationID(), remaining[0].id)
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
		identity: candidateIdentity{
			generation: 1,
			count:      1,
			key: candidateIdentityKey{
				scopeHash: 100,
				hash:      42,
			},
		},
		status: activationStatusPending,
	}
	removed.setFactIDs([]FactID{removedFactID})
	removed.setFactVersions([]FactVersion{1})
	kept := activation{
		ruleRevisionID: RuleRevisionID("kept"),
		identity: candidateIdentity{
			generation: 1,
			count:      1,
			key: candidateIdentityKey{
				scopeHash: 200,
				hash:      42,
			},
		},
		status: activationStatusPending,
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
	bucket := agenda.activations[keptKey.fingerprint]
	if bucket.first == nil || bucket.first.key != keptKey {
		t.Fatalf("bucket first after purge = %#v, want kept activation", bucket.first)
	}
	if len(bucket.overflow) != 0 {
		t.Fatalf("bucket overflow after purge = %d, want 0", len(bucket.overflow))
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
	if changes[0].activation.activationID() != initial.id {
		t.Fatalf("deactivated activation ID = %q, want %q", changes[0].activation.activationID(), initial.id)
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
	if changes[0].activation.activationID() == first.id {
		t.Fatalf("new activation ID reused consumed activation ID %q", first.id)
	}
	if got := agenda.pendingActivations(); len(got) != 1 || got[0].ruleRevisionID != rule2.RevisionID() {
		t.Fatalf("pending activations after revision replace = %#v", got)
	}
	if got, ok := agenda.activationByKey(first.key); !ok || got.status != activationStatusConsumed {
		t.Fatalf("consumed activation after revision replace = %#v, ok=%v", got, ok)
	}
}

func TestActivationLessOrdersBySalienceRecencyAndID(t *testing.T) {
	acts := []activation{
		{
			id:               ActivationID("z"),
			salience:         20,
			maxRecency:       1,
			aggregateRecency: 1,
		},
		{
			id:               ActivationID("y"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 9,
		},
		{
			id:               ActivationID("x"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
		},
		{
			id:               ActivationID("b"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
		},
		{
			id:               ActivationID("a"),
			salience:         10,
			maxRecency:       9,
			aggregateRecency: 8,
		},
	}

	sort.SliceStable(acts, func(i, j int) bool {
		return activationLess(&acts[i], &acts[j])
	})

	got := []ActivationID{acts[0].id, acts[1].id, acts[2].id, acts[3].id, acts[4].id}
	want := []ActivationID{"z", "y", "a", "b", "x"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted activation %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestActivationLessOrdersLazyActivationsLikePublicIDs(t *testing.T) {
	base := activation{
		salience:         10,
		maxRecency:       9,
		aggregateRecency: 8,
	}
	tests := []struct {
		name  string
		left  activation
		right activation
	}{
		{
			name: "scope prefix segment",
			left: func() activation {
				act := base
				act.identity.key = candidateIdentityKey{scopeHash: 10, hash: 1}
				return act
			}(),
			right: func() activation {
				act := base
				act.identity.key = candidateIdentityKey{scopeHash: 1, hash: 1}
				return act
			}(),
		},
		{
			name: "hash prefix segment",
			left: func() activation {
				act := base
				act.identity.key = candidateIdentityKey{scopeHash: 2, hash: 10}
				return act
			}(),
			right: func() activation {
				act := base
				act.identity.key = candidateIdentityKey{scopeHash: 2, hash: 1}
				return act
			}(),
		},
		{
			name: "final ordinal segment",
			left: func() activation {
				act := base
				act.identity.key = candidateIdentityKey{scopeHash: 2, hash: 3}
				act.publicOrdinal = 1
				return act
			}(),
			right: func() activation {
				act := base
				act.identity.key = candidateIdentityKey{scopeHash: 2, hash: 3}
				act.publicOrdinal = 10
				return act
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := activationLess(&tt.left, &tt.right)
			want := tt.left.activationID() < tt.right.activationID()
			if got != want {
				t.Fatalf("activationLess(%q, %q) = %v, want %v", tt.left.activationID(), tt.right.activationID(), got, want)
			}
		})
	}
}

func TestAgendaChangeEventsKeepFactEventsBare(t *testing.T) {
	activation := activation{
		id:             ActivationID("activation"),
		ruleID:         RuleID("rule"),
		ruleRevisionID: RuleRevisionID("revision"),
		generation:     1,
	}
	activation.setFactIDs([]FactID{newFactID(1, 2)})
	change := agendaChange{
		kind:       agendaChangeActivated,
		activation: activation,
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
	initialID := changes[0].activation.activationID()

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
	if pending[0].id == initialID {
		t.Fatalf("post-reset activation reused old activation ID %q", initialID)
	}
	if pending[0].generation != 2 {
		t.Fatalf("post-reset activation generation = %d, want 2", pending[0].generation)
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
	if pending[0].id == initialID {
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

func mustAgendaMatchResults(t *testing.T, revision *Ruleset, session *Session) []ruleMatchResult {
	t.Helper()
	snapshot := mustSnapshot(t, context.Background(), session)
	results, err := newNaiveMatcher(revision).match(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	return results
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
		out.id = public.id
		out.payload = nil
		out.setFactIDs(cloneFactIDs(public.factIDs()))
		out.setFactVersions(cloneFactVersions(public.factVersions()))
		out.setBindings(cloneBindingTupleEntries(public.bindings()))
		out.setPath(cloneIntPath(public.path()))
	} else {
		out.id = act.activationID()
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
