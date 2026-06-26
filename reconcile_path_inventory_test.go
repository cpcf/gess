package gess

import "testing"

type reconcilePathClass string

const (
	reconcilePathInitialBuild   reconcilePathClass = "initial agenda build"
	reconcilePathResetRebuild   reconcilePathClass = "reset rebuild"
	reconcilePathMigrationDebt  reconcilePathClass = "migration debt"
	reconcilePathTestOracle     reconcilePathClass = "test oracle"
	reconcilePathDiagnosticOnly reconcilePathClass = "public diagnostic"
)

type reconcilePathInventoryEntry struct {
	path          string
	class         reconcilePathClass
	owner         string
	removalPlan   string
	steadyStateOK bool
}

var productionReconcilePathInventory = []reconcilePathInventoryEntry{
	{
		path:          "Session.reconcileAgenda: snapshot-backed rete.match plus agenda.reconcile",
		class:         reconcilePathInitialBuild,
		owner:         "P4 Remove Steady-State Whole-Terminal Reconcile",
		removalPlan:   "Retain only for initial agenda construction and test parity helpers; steady-state mutation callers must apply terminal deltas or return ErrUnsupportedRuntime.",
		steadyStateOK: true,
	},
	{
		path:          "Session.reconcileAgendaWithoutSnapshot: current terminal token collection",
		class:         reconcilePathInitialBuild,
		owner:         "P4 Remove Steady-State Whole-Terminal Reconcile",
		removalPlan:   "Retain for initial agenda construction until reset/clear propagation owns the build lifecycle.",
		steadyStateOK: true,
	},
	{
		path:          "Session.reconcileAgendaWithoutSnapshot: matchWithoutSnapshot plus agenda.reconcile",
		class:         reconcilePathInitialBuild,
		owner:         "P4 Remove Steady-State Whole-Terminal Reconcile",
		removalPlan:   "Retain only as an initial-build fallback when terminal token collection is unavailable.",
		steadyStateOK: true,
	},
	{
		path:          "Session.Reset: post-reset terminal token collection and fallback reconcile",
		class:         reconcilePathResetRebuild,
		owner:         "P1 Add Clear/Reset Propagation",
		removalPlan:   "Replace rebuild-time terminal enumeration with clear/reset graph propagation once graph memories own reset lifecycle.",
		steadyStateOK: true,
	},
	{
		path:          "Session.ApplyRuleset: rebuilt-runtime terminal collection and fallback match",
		class:         reconcilePathResetRebuild,
		owner:         "P1 Add Clear/Reset Propagation",
		removalPlan:   "Keep as revision rebuild scaffolding until ruleset application is modeled as explicit graph memory lifecycle.",
		steadyStateOK: true,
	},
	{
		path:          "Session.reconcileAgendaAfterMutation: initial agenda fallback",
		class:         reconcilePathInitialBuild,
		owner:         "P1 Introduce Explicit Graph Propagation Events",
		removalPlan:   "Retain only before agenda readiness; unsupported steady-state mutation deltas return ErrUnsupportedRuntime.",
		steadyStateOK: true,
	},
	{
		path:          "Run: initial agenda readiness reconcile",
		class:         reconcilePathInitialBuild,
		owner:         "P4 Remove Steady-State Whole-Terminal Reconcile",
		removalPlan:   "Retain only for initial agenda construction until graph reset/build emits terminal state directly.",
		steadyStateOK: true,
	},
	{
		path:          "matcher_oracle_test.go: naive matcher parity helper",
		class:         reconcilePathTestOracle,
		owner:         "test-only parity harness",
		removalPlan:   "Keep in _test.go only; production package code must not instantiate the oracle matcher.",
		steadyStateOK: true,
	},
}

func TestProductionReconcilePathInventoryIsClassified(t *testing.T) {
	if len(productionReconcilePathInventory) == 0 {
		t.Fatal("production reconcile path inventory is empty")
	}
	seen := make(map[string]struct{}, len(productionReconcilePathInventory))
	for _, entry := range productionReconcilePathInventory {
		if entry.path == "" {
			t.Fatalf("inventory entry has empty path: %#v", entry)
		}
		if _, ok := seen[entry.path]; ok {
			t.Fatalf("duplicate inventory path %q", entry.path)
		}
		seen[entry.path] = struct{}{}
		if entry.class == "" {
			t.Fatalf("inventory entry %q has empty class", entry.path)
		}
		if entry.owner == "" {
			t.Fatalf("inventory entry %q has empty owner", entry.path)
		}
		if entry.removalPlan == "" {
			t.Fatalf("inventory entry %q has empty removal plan", entry.path)
		}
		if entry.class == reconcilePathMigrationDebt && entry.steadyStateOK {
			t.Fatalf("migration debt entry %q cannot be marked steady-state OK", entry.path)
		}
	}
}
