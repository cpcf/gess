package engine

import (
	"reflect"
	"slices"
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
	{name: "agenda", policy: forkCloned, rationale: "pending activations are copied into an independent agenda"},
	{name: "strategy", policy: forkCopied, rationale: "the inherited strategy is a value unless an option replaces it"},
	{name: "forkCount", policy: forkReinitializedTransient, rationale: "the child starts its own descendant numbering"},
	{name: "propagationCounters", policy: forkRebuiltDerived, rationale: "a child ledger is synchronized from the rebuilt runtime"},
	{name: "rete", policy: forkRebuiltDerived, rationale: "the graph runtime is rebuilt against the cloned working memory"},
	{name: "generation", policy: forkCopied, rationale: "fact identity generation continues from the snapshot"},
	{name: "initialFocusStack", policy: forkCloned, rationale: "module names are copied into child-owned slice storage"},
	{name: "focusStack", policy: forkCloned, rationale: "the current focus stack is copied into child-owned slice storage"},
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
	{name: "runAgendaDelta", policy: forkReinitializedTransient, rationale: "run-local delta scratch starts empty"},
	{name: "runAgendaDeltas", policy: forkReinitializedTransient, rationale: "run-local nested delta scratch starts empty"},
	{name: "runAgendaStates", policy: forkReinitializedTransient, rationale: "run-local reconciliation state starts empty"},
	{name: "runAgendaBuckets", policy: forkReinitializedTransient, rationale: "run-local candidate buckets start empty"},
	{name: "runAgendaAdded", policy: forkReinitializedTransient, rationale: "run-local added-token scratch starts empty"},
	{name: "runAgendaRemoved", policy: forkReinitializedTransient, rationale: "run-local removed-token scratch starts empty"},
	{name: "runAgendaUpdated", policy: forkReinitializedTransient, rationale: "run-local updated-token scratch starts empty"},
	{name: "runAgendaPending", policy: forkReinitializedTransient, rationale: "the child has no pending run-local reconciliation"},
	{name: "runAgendaDirect", policy: forkReinitializedTransient, rationale: "the child is not in direct agenda mode"},
	{name: "agendaReady", policy: forkRebuiltDerived, rationale: "readiness is decided after graph reconstruction and demand resolution"},
	{name: "agendaDirty", policy: forkRebuiltDerived, rationale: "dirtiness is decided after graph reconstruction and demand resolution"},
	{name: "actionBindingScratch", policy: forkReinitializedTransient, rationale: "action evaluation scratch is never shared across sessions"},
	{name: "actionValueScratch", policy: forkReinitializedTransient, rationale: "action value scratch starts empty"},
	{name: "actionMatchScratch", policy: forkReinitializedTransient, rationale: "action match scratch starts empty"},
	{name: "mutationQueueMu", policy: forkReinitializedTransient, rationale: "the child owns a distinct mutation-queue mutex"},
	{name: "mutationQueue", policy: forkReinitializedTransient, rationale: "queued external mutations do not cross the fork boundary"},
	{name: "mu", policy: forkReinitializedTransient, rationale: "the child owns new mutation and lock semaphores"},

	{name: "nextFactSequence", policy: forkRebuiltDerived, rationale: "the sequence is read from the cloned fact workspace"},
	{name: "nextRecency", policy: forkRebuiltDerived, rationale: "recency is read from the cloned fact workspace"},
	{name: "nextRunSequence", policy: forkCopied, rationale: "run sequence continuity is part of the snapshot"},
	{name: "facts", policy: forkCloned, rationale: "working facts are deep-cloned"},
	{name: "compactFacts", policy: forkCloned, rationale: "compact fact storage is cloned"},
	{name: "factsByID", policy: forkCloned, rationale: "the fact identity index is cloned"},
	{name: "factsBySequence", policy: forkCloned, rationale: "the sequence-to-row index is cloned"},
	{name: "factsByDuplicate", policy: forkCloned, rationale: "duplicate indexes are cloned"},
	{name: "factsByTemplate", policy: forkCloned, rationale: "template indexes are cloned"},
	{name: "factsByName", policy: forkCloned, rationale: "dynamic-name indexes are cloned"},
	{name: "factFieldEqualIndexes", policy: forkRebuiltDerived, rationale: "the optional equality cache is rebuilt lazily from child facts"},
	{name: "factTargetIndexesDirty", policy: forkCloned, rationale: "the cloned workspace preserves its target-index state"},
	{name: "insertionOrder", policy: forkCloned, rationale: "insertion order is copied into child-owned storage"},
	{name: "slotStorage", policy: forkCloned, rationale: "fact slots are deep-cloned"},
	{name: "compactSlotStore", policy: forkCloned, rationale: "compact slot storage is cloned"},
	{name: "resetWorkspace", policy: forkReinitializedTransient, rationale: "the reusable reset buffer starts empty"},
	{name: "logicalSupportEdges", policy: forkCloned, rationale: "logical support records are deep-cloned"},
	{name: "logicalSupportBySource", policy: forkCloned, rationale: "the source support index is deep-cloned"},
	{name: "logicalSupportByFact", policy: forkCloned, rationale: "the fact support index is deep-cloned"},
	{name: "logicalSupportCounters", policy: forkCopied, rationale: "logical support metrics continue from the snapshot"},
	{name: "nextBackchainDemandSupportID", policy: forkRebuiltDerived, rationale: "demand support identity is reconstructed with graph demand state"},
	{name: "freeBackchainDemandSupportIDs", policy: forkRebuiltDerived, rationale: "the free list is reconstructed with graph demand state"},
	{name: "backchainDemandSupports", policy: forkRebuiltDerived, rationale: "support slots are reconstructed from graph demand state"},
	{name: "backchainDemandSupportRecords", policy: forkRebuiltDerived, rationale: "support records are reconstructed from graph demand state"},
	{name: "backchainDemandOwnerRecords", policy: forkRebuiltDerived, rationale: "owner records are reconstructed from graph demand state"},
	{name: "backchainDemandInlineSupports", policy: forkRebuiltDerived, rationale: "inline support indexes are reconstructed from graph demand state"},
	{name: "backchainDemandSupportOwners", policy: forkRebuiltDerived, rationale: "support owner indexes are reconstructed from graph demand state"},
	{name: "backchainDemandByFact", policy: forkRebuiltDerived, rationale: "fact-to-demand indexes are reconstructed from graph demand state"},
	{name: "backchainDemandByDemand", policy: forkRebuiltDerived, rationale: "demand-to-fact indexes are reconstructed from graph demand state"},
	{name: "demandLimit", policy: forkCopied, rationale: "the inherited cascade limit is a value unless an option replaces it"},
	{name: "demandCounters", policy: forkCopied, rationale: "demand observability counters continue from the snapshot"},
	{name: "activeDemandCascade", policy: forkReinitializedTransient, rationale: "no demand cascade is active while the child is constructed"},
	{name: "activeBackchainQueryProof", policy: forkReinitializedTransient, rationale: "query proof ownership never crosses the fork boundary"},
	{name: "backchainQueryProofScratch", policy: forkReinitializedTransient, rationale: "session-owned proof scratch starts empty"},
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

	sessionType := reflect.TypeFor[Session]()
	fields := make(map[string]struct{}, sessionType.NumField())
	var missing []string
	for field := range sessionType.Fields() {
		name := field.Name
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
