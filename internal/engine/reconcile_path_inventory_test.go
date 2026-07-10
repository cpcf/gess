package engine

import "testing"

type reconcilePathClass string

const (
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

var productionReconcilePathInventory = []reconcilePathInventoryEntry{}

var retainedReconcilePathInventory = []reconcilePathInventoryEntry{
	{
		path:          "reconcile_test_helper_test.go: Session.reconcileAgenda whole-terminal parity helper",
		class:         reconcilePathTestOracle,
		owner:         "test-only parity harness",
		removalPlan:   "Keep in _test.go only; production lifecycle code must consume graph-emitted terminal deltas.",
		steadyStateOK: false,
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
	if len(productionReconcilePathInventory) != 0 {
		t.Fatalf("production whole-terminal reconcile paths remain: %#v", productionReconcilePathInventory)
	}
	seen := make(map[string]struct{}, len(retainedReconcilePathInventory))
	for _, entry := range retainedReconcilePathInventory {
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
		if entry.class != reconcilePathTestOracle && entry.class != reconcilePathDiagnosticOnly {
			t.Fatalf("retained reconcile entry %q has production class %q", entry.path, entry.class)
		}
	}
}
