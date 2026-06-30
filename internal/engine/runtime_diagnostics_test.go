package engine

import (
	"context"
	"testing"
)

func TestSessionRuntimeDiagnosticsReportsBetaMemoryOwner(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session := mustSession(t, revision, "beta-memory-diagnostics-session")

	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate(employee): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate(department): %v", err)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	var beta RuntimeMemoryOwnerDiagnostics
	for _, owner := range diagnostics.MemoryOwners {
		if owner.Owner == runtimeMemoryOwnerBeta {
			beta = owner
			break
		}
	}
	if beta.Owner == "" {
		t.Fatalf("runtime diagnostics missing beta owner: %#v", diagnostics.MemoryOwners)
	}
	if beta.Rows == 0 {
		t.Fatalf("beta rows = 0, want retained beta token rows: %#v", beta)
	}
	if beta.Buckets == 0 {
		t.Fatalf("beta buckets = 0, want retained join or identity buckets: %#v", beta)
	}
	if beta.Indexes == 0 {
		t.Fatalf("beta indexes = 0, want retained fact reverse indexes: %#v", beta)
	}
	if beta.Bytes == 0 {
		t.Fatalf("beta bytes = 0, want retained byte estimate: %#v", beta)
	}
	if beta.HighWater == 0 {
		t.Fatalf("beta high water = 0, want capacity estimate: %#v", beta)
	}
}
