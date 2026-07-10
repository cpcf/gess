package engine

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type sessionForkFieldPolicy uint8

const (
	forkSharedImmutable sessionForkFieldPolicy = iota + 1
	forkCloned
	forkCopied
	forkRebuiltDerived
	forkConfiguredFresh
	forkReinitializedTransient
)

type sessionForkFieldDecision struct {
	name      string
	policy    sessionForkFieldPolicy
	rationale string
}

// sessionForkFieldDecisions is an explicit review checkpoint for every piece of
// mutable state owned by Session. Adding, removing, or moving a Session field
// requires deciding how a fork obtains that state instead of relying on a zero
// value or a shallow struct copy by accident.
var sessionForkFieldDecisions = []sessionForkFieldDecision{
	{name: "id", policy: forkConfiguredFresh, rationale: "the caller supplies an identity or Fork derives a new child identity"},
	{name: "revision", policy: forkSharedImmutable, rationale: "compiled rulesets are immutable and safe to share"},
	{name: "agendaDriver", policy: forkCloned, rationale: "the child receives an independently owned agenda driver"},
	{name: "agendaDriver.agenda", policy: forkCloned, rationale: "pending activations and refraction are copied into an independent agenda"},
	{name: "agendaDriver.strategy", policy: forkCopied, rationale: "the inherited strategy is a value unless an option replaces it"},
	{name: "agendaDriver.initialFocusStack", policy: forkCloned, rationale: "module names are copied into child-owned slice storage"},
	{name: "agendaDriver.focusStack", policy: forkCloned, rationale: "the current focus stack is copied into child-owned slice storage"},
	{name: "agendaDriver.ready", policy: forkRebuiltDerived, rationale: "readiness is decided after graph reconstruction and demand resolution"},
	{name: "agendaDriver.dirty", policy: forkRebuiltDerived, rationale: "dirtiness is decided after graph reconstruction and demand resolution"},
	{name: "forkCount", policy: forkReinitializedTransient, rationale: "the child starts its own descendant numbering"},
	{name: "propagation", policy: forkRebuiltDerived, rationale: "the child receives a coordinator initialized around its rebuilt runtime"},
	{name: "propagation.runtime", policy: forkRebuiltDerived, rationale: "the graph runtime is rebuilt against the cloned working memory"},
	{name: "propagation.counters", policy: forkReinitializedTransient, rationale: "the child starts with a fresh ledger synchronized from its rebuilt runtime"},
	{name: "propagation.runAgendaDelta", policy: forkReinitializedTransient, rationale: "run-local delta scratch starts empty"},
	{name: "propagation.runAgendaDeltas", policy: forkReinitializedTransient, rationale: "run-local nested delta scratch starts empty"},
	{name: "propagation.runAgendaStates", policy: forkReinitializedTransient, rationale: "run-local reconciliation state starts empty"},
	{name: "propagation.runAgendaBuckets", policy: forkReinitializedTransient, rationale: "run-local candidate buckets start empty"},
	{name: "propagation.runAgendaAdded", policy: forkReinitializedTransient, rationale: "run-local added-token scratch starts empty"},
	{name: "propagation.runAgendaRemoved", policy: forkReinitializedTransient, rationale: "run-local removed-token scratch starts empty"},
	{name: "propagation.runAgendaUpdated", policy: forkReinitializedTransient, rationale: "run-local updated-token scratch starts empty"},
	{name: "propagation.runAgendaPending", policy: forkReinitializedTransient, rationale: "the child has no pending run-local reconciliation"},
	{name: "propagation.runAgendaDirect", policy: forkReinitializedTransient, rationale: "the child is not in direct agenda mode"},
	{name: "factStore", policy: forkCloned, rationale: "the child receives an independently owned fact-memory aggregate"},
	{name: "factStore.generation", policy: forkCopied, rationale: "fact identity generation continues from the snapshot"},
	{name: "initials", policy: forkCloned, rationale: "initial facts are deep-cloned and may be replaced by an option"},
	{name: "globalValues", policy: forkCloned, rationale: "globals are recompiled from cloned public values"},
	{name: "initialCount", policy: forkRebuiltDerived, rationale: "the count is derived from the child's initial facts"},
	{name: "compiledInitials", policy: forkRebuiltDerived, rationale: "compiled initial facts are rebuilt for the child configuration"},
	{name: "resetBeforeSnapshot", policy: forkCopied, rationale: "the inherited option is a value"},
	{name: "listeners", policy: forkConfiguredFresh, rationale: "listeners are not inherited implicitly and come from child options"},
	{name: "allEventListeners", policy: forkRebuiltDerived, rationale: "the aggregate is counted from the child's listeners"},
	{name: "eventListenerCounts", policy: forkRebuiltDerived, rationale: "subscription counts are rebuilt from the child's listeners"},
	{name: "explainLog", policy: forkConfiguredFresh, rationale: "explain capture is independently configured for the child"},
	{name: "eventClock", policy: forkConfiguredFresh, rationale: "the child receives an option-provided clock or a fresh default"},
	{name: "output", policy: forkCopied, rationale: "the writer is inherited by interface value unless an option replaces it"},
	{name: "closed", policy: forkReinitializedTransient, rationale: "a successfully created child starts open"},
	{name: "runGuard", policy: forkReinitializedTransient, rationale: "the child owns a new run semaphore"},
	{name: "runActive", policy: forkReinitializedTransient, rationale: "no run is active while the child is constructed"},
	{name: "runActivation", policy: forkReinitializedTransient, rationale: "no activation is executing in the child"},
	{name: "runHaltRequested", policy: forkReinitializedTransient, rationale: "halt requests do not cross the fork boundary"},
	{name: "actionBindingScratch", policy: forkReinitializedTransient, rationale: "action evaluation scratch is never shared across sessions"},
	{name: "actionValueScratch", policy: forkReinitializedTransient, rationale: "action value scratch starts empty"},
	{name: "actionMatchScratch", policy: forkReinitializedTransient, rationale: "action match scratch starts empty"},
	{name: "mutationQueueMu", policy: forkReinitializedTransient, rationale: "the child owns a distinct mutation-queue mutex"},
	{name: "mutationQueue", policy: forkReinitializedTransient, rationale: "queued external mutations do not cross the fork boundary"},
	{name: "mu", policy: forkReinitializedTransient, rationale: "the child owns new mutation and lock semaphores"},

	{name: "factStore.nextFactSequence", policy: forkRebuiltDerived, rationale: "the sequence is read from the cloned fact workspace"},
	{name: "factStore.nextRecency", policy: forkRebuiltDerived, rationale: "recency is read from the cloned fact workspace"},
	{name: "nextRunSequence", policy: forkCopied, rationale: "run sequence continuity is part of the snapshot"},
	{name: "factStore.facts", policy: forkCloned, rationale: "working facts are deep-cloned"},
	{name: "factStore.compactFacts", policy: forkCloned, rationale: "compact fact storage is cloned"},
	{name: "factStore.factsByID", policy: forkCloned, rationale: "the fact identity index is cloned"},
	{name: "factStore.factsBySequence", policy: forkCloned, rationale: "the sequence-to-row index is cloned"},
	{name: "factStore.factsByDuplicate", policy: forkCloned, rationale: "duplicate indexes are cloned"},
	{name: "factStore.factsByTemplate", policy: forkCloned, rationale: "template indexes are cloned"},
	{name: "factStore.factsByName", policy: forkCloned, rationale: "dynamic-name indexes are cloned"},
	{name: "factStore.factFieldEqualIndexes", policy: forkRebuiltDerived, rationale: "the optional equality cache is rebuilt lazily from child facts"},
	{name: "factStore.factTargetIndexesDirty", policy: forkCloned, rationale: "the cloned workspace preserves its target-index state"},
	{name: "factStore.insertionOrder", policy: forkCloned, rationale: "insertion order is copied into child-owned storage"},
	{name: "factStore.slotStorage", policy: forkCloned, rationale: "fact slots are deep-cloned"},
	{name: "factStore.compactSlotStore", policy: forkCloned, rationale: "compact slot storage is cloned"},
	{name: "factStore.resetWorkspace", policy: forkReinitializedTransient, rationale: "the reusable reset buffer starts empty"},
	{name: "tms", policy: forkCloned, rationale: "the child receives an independently owned truth-maintenance store"},
	{name: "tms.logicalSupportEdges", policy: forkCloned, rationale: "logical support records are deep-cloned"},
	{name: "tms.logicalSupportBySource", policy: forkCloned, rationale: "the source support index is deep-cloned"},
	{name: "tms.logicalSupportByFact", policy: forkCloned, rationale: "the fact support index is deep-cloned"},
	{name: "tms.logicalSupportCounters", policy: forkCopied, rationale: "logical support metrics continue from the snapshot"},
	{name: "backchain", policy: forkRebuiltDerived, rationale: "the child receives a backchain store whose persistent indexes are rebuilt and whose transients are fresh"},
	{name: "backchain.nextDemandSupportID", policy: forkRebuiltDerived, rationale: "demand support identity is reconstructed with graph demand state"},
	{name: "backchain.freeDemandSupportIDs", policy: forkRebuiltDerived, rationale: "the free list is reconstructed with graph demand state"},
	{name: "backchain.demandSupports", policy: forkRebuiltDerived, rationale: "support slots are reconstructed from graph demand state"},
	{name: "backchain.demandSupportRecords", policy: forkRebuiltDerived, rationale: "support records are reconstructed from graph demand state"},
	{name: "backchain.demandOwnerRecords", policy: forkRebuiltDerived, rationale: "owner records are reconstructed from graph demand state"},
	{name: "backchain.inlineSupports", policy: forkRebuiltDerived, rationale: "inline support indexes are reconstructed from graph demand state"},
	{name: "backchain.supportOwners", policy: forkRebuiltDerived, rationale: "support owner indexes are reconstructed from graph demand state"},
	{name: "backchain.demandByFact", policy: forkRebuiltDerived, rationale: "fact-to-demand indexes are reconstructed from graph demand state"},
	{name: "backchain.demandByDemand", policy: forkRebuiltDerived, rationale: "demand-to-fact indexes are reconstructed from graph demand state"},
	{name: "backchain.demandLimit", policy: forkCopied, rationale: "the inherited cascade limit is a value unless an option replaces it"},
	{name: "backchain.demandCounters", policy: forkCopied, rationale: "demand observability counters continue from the snapshot"},
	{name: "backchain.activeDemandCascade", policy: forkReinitializedTransient, rationale: "no demand cascade is active while the child is constructed"},
	{name: "backchain.activeQueryProof", policy: forkReinitializedTransient, rationale: "query proof ownership never crosses the fork boundary"},
	{name: "backchain.queryProofScratch", policy: forkReinitializedTransient, rationale: "session-owned proof scratch starts empty"},
	{name: "nextEventSequence", policy: forkCopied, rationale: "event sequence continuity is part of the snapshot"},
}

func TestSessionForkFieldOwnershipComplete(t *testing.T) {
	validPolicies := map[sessionForkFieldPolicy]bool{
		forkSharedImmutable:        true,
		forkCloned:                 true,
		forkCopied:                 true,
		forkRebuiltDerived:         true,
		forkConfiguredFresh:        true,
		forkReinitializedTransient: true,
	}

	decisions := make(map[string]sessionForkFieldDecision, len(sessionForkFieldDecisions))
	var duplicates []string
	for _, decision := range sessionForkFieldDecisions {
		if _, exists := decisions[decision.name]; exists {
			duplicates = append(duplicates, decision.name)
		}
		decisions[decision.name] = decision
		if !validPolicies[decision.policy] {
			t.Errorf("Session.%s has invalid fork policy %d", decision.name, decision.policy)
		}
		if decision.rationale == "" {
			t.Errorf("Session.%s fork policy has no rationale", decision.name)
		}
	}
	slices.Sort(duplicates)
	if len(duplicates) > 0 {
		t.Errorf("duplicate Session fork policy entries: %v", duplicates)
	}

	fields := make(map[string]struct{}, len(decisions))
	var missing []string
	for _, name := range sessionOwnedFieldPaths(reflect.TypeFor[Session](), "") {
		fields[name] = struct{}{}
		if _, exists := decisions[name]; !exists {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("Session fields missing an explicit fork policy: %v", missing)
	}

	var stale []string
	for name := range decisions {
		if _, exists := fields[name]; !exists {
			stale = append(stale, name)
		}
	}
	slices.Sort(stale)
	if len(stale) > 0 {
		t.Errorf("fork policy entries for fields no longer on Session: %v", stale)
	}
}

func sessionOwnedFieldPaths(owner reflect.Type, prefix string) []string {
	var paths []string
	for field := range owner.Fields() {
		path := field.Name
		if prefix != "" {
			path = prefix + "." + path
		}
		paths = append(paths, path)
		if field.Type.PkgPath() == owner.PkgPath() && strings.HasPrefix(field.Type.Name(), "session") {
			paths = append(paths, sessionOwnedFieldPaths(field.Type, path)...)
		}
	}
	return paths
}

func TestSessionForkStartsWithFreshFactStoreCachesAndScratch(t *testing.T) {
	parent := mustSession(t, mustCompile(t), "fact-store-owner-parent")
	parent.factStore.factFieldEqualIndexes = map[factFieldEqualKey][]FactSnapshot{
		{}: nil,
	}
	parent.factStore.resetWorkspace.facts = make([]workingFact, 1)

	child, err := parent.Fork(context.Background())
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if child.factStore.factFieldEqualIndexes != nil {
		t.Fatalf("child equality cache = %#v, want fresh nil cache", child.factStore.factFieldEqualIndexes)
	}
	if child.factStore.resetWorkspace.facts != nil {
		t.Fatalf("child reset scratch facts = %#v, want fresh nil scratch", child.factStore.resetWorkspace.facts)
	}
	if parent.factStore.factFieldEqualIndexes == nil {
		t.Fatal("Fork cleared the parent equality cache")
	}
	if len(parent.factStore.resetWorkspace.facts) != 1 {
		t.Fatalf("parent reset scratch facts = %d, want 1", len(parent.factStore.resetWorkspace.facts))
	}
}

func TestSessionForkStartsWithFreshPropagationOwnership(t *testing.T) {
	parent := mustSession(t, mustCompile(t), "propagation-owner-parent")
	parentCounters := parent.attachPropagationCounters()
	parentCounters.recordAgendaDeltaApplication()
	parent.propagation.runAgendaAdded = make([]reteTerminalTokenDelta, 1)

	child, err := parent.Fork(context.Background())
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if child.propagation.runtime == parent.propagation.runtime {
		t.Fatal("fork shares the parent Rete runtime")
	}
	childCounters := child.propagation.counters
	if childCounters == nil {
		t.Fatal("fork did not preserve attached counter observability with a fresh ledger")
	}
	if childCounters == parentCounters {
		t.Fatal("fork shares the parent propagation counter ledger")
	}
	if got := child.propagationCounterSnapshot().Totals.AgendaDeltaApplications; got != 0 {
		t.Fatalf("fork agenda delta applications = %d, want 0", got)
	}
	if len(child.propagation.runAgendaAdded) != 0 || child.propagation.runAgendaPending || child.propagation.runAgendaDirect {
		t.Fatalf("fork run delta scratch = %#v, want empty and inactive", child.propagation)
	}
	if got := parent.propagationCounterSnapshot().Totals.AgendaDeltaApplications; got != 1 {
		t.Fatalf("parent agenda delta applications = %d, want 1", got)
	}
	if len(parent.propagation.runAgendaAdded) != 1 {
		t.Fatalf("parent added-token scratch = %d, want 1", len(parent.propagation.runAgendaAdded))
	}
}

func TestSessionForkOwnsTMSAndRebuildsBackchainStore(t *testing.T) {
	parent := mustSession(t, mustCompile(t), "store-owner-parent")
	supportID := SupportID("support-1")
	source := logicalSupportSourceKey{}
	factID := newFactID(1, 1)
	parent.tms.logicalSupportEdges = map[SupportID]logicalSupportEdgeRecord{
		supportID: {},
	}
	parent.tms.logicalSupportBySource = map[logicalSupportSourceKey]map[SupportID]struct{}{
		source: {supportID: {}},
	}
	parent.tms.logicalSupportByFact = map[FactID]map[SupportID]struct{}{
		factID: {supportID: {}},
	}
	parent.tms.logicalSupportCounters.SupportEdgesAdded = 7

	parent.backchain.nextDemandSupportID = 9
	parent.backchain.freeDemandSupportIDs = []backchainDemandSupportID{3}
	parent.backchain.demandSupportRecords = []backchainDemandSupportRecord{{id: 1}}
	parent.backchain.demandLimit = 11
	parent.backchain.demandCounters = backchainDemandCascadeCounters{Cascades: 2, Steps: 5}
	parent.backchain.activeDemandCascade = &backchainDemandCascadeBudget{session: parent, started: true}
	parent.backchain.queryProofScratch.session = parent
	parent.backchain.queryProofScratch.facts = make([]workingFact, 1)
	parent.backchain.activeQueryProof = &parent.backchain.queryProofScratch

	child, err := parent.Fork(context.Background())
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if child.tms.logicalSupportCounters != parent.tms.logicalSupportCounters {
		t.Fatalf("child TMS counters = %#v, want %#v", child.tms.logicalSupportCounters, parent.tms.logicalSupportCounters)
	}
	delete(child.tms.logicalSupportEdges, supportID)
	delete(child.tms.logicalSupportBySource[source], supportID)
	delete(child.tms.logicalSupportByFact[factID], supportID)
	if len(parent.tms.logicalSupportEdges) != 1 || len(parent.tms.logicalSupportBySource[source]) != 1 || len(parent.tms.logicalSupportByFact[factID]) != 1 {
		t.Fatal("child TMS mutation aliased parent maps")
	}

	if child.backchain.nextDemandSupportID != 0 || len(child.backchain.freeDemandSupportIDs) != 0 || len(child.backchain.demandSupportRecords) != 0 {
		t.Fatalf("child persistent backchain state = %#v, want graph-rebuilt empty state", child.backchain)
	}
	if child.backchain.demandLimit != 11 || child.backchain.demandCounters != parent.backchain.demandCounters {
		t.Fatalf("child backchain config/counters = limit %d, counters %#v", child.backchain.demandLimit, child.backchain.demandCounters)
	}
	if child.backchain.activeDemandCascade != nil || child.backchain.activeQueryProof != nil {
		t.Fatal("child inherited active backchain transient state")
	}
	if child.backchain.queryProofScratch.session != nil || len(child.backchain.queryProofScratch.facts) != 0 {
		t.Fatalf("child proof scratch = %#v, want fresh zero state", child.backchain.queryProofScratch)
	}
	if parent.backchain.activeDemandCascade == nil || parent.backchain.activeQueryProof == nil || len(parent.backchain.queryProofScratch.facts) != 1 {
		t.Fatal("Fork mutated parent backchain transient state")
	}
}
