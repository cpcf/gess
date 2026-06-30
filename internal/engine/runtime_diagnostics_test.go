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

func TestSessionRuntimeDiagnosticsSplitsRuleAndQueryTerminalOwners(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustRuntimeGuardRuleset(t)
	session := mustSession(t, revision, "terminal-memory-diagnostics-session")

	if _, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{
		"age":    42,
		"dept":   "Engineering",
		"id":     "ada",
		"note":   "ready",
		"status": "active",
	})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if rows, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "Engineering"}); err != nil {
		t.Fatalf("QueryAll: %v", err)
	} else if got, want := len(rows), 1; got != want {
		t.Fatalf("query rows = %d, want %d", got, want)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	rule := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerRuleTerminal)
	if rule.Owner == "" {
		t.Fatalf("runtime diagnostics missing rule terminal owner: %#v", diagnostics.MemoryOwners)
	}
	query := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerQueryTerminal)
	if query.Owner == "" {
		t.Fatalf("runtime diagnostics missing query terminal owner: %#v", diagnostics.MemoryOwners)
	}
	if rule.Rows == 0 {
		t.Fatalf("rule terminal rows = 0, want retained terminal rows: %#v", rule)
	}
	if rule.Buckets == 0 {
		t.Fatalf("rule terminal buckets = 0, want retained identity buckets: %#v", rule)
	}
	for _, owner := range []RuntimeMemoryOwnerDiagnostics{rule, query} {
		if owner.Bytes == 0 {
			t.Fatalf("%s bytes = 0, want retained byte estimate: %#v", owner.Owner, owner)
		}
		if owner.HighWater == 0 {
			t.Fatalf("%s high water = 0, want capacity estimate: %#v", owner.Owner, owner)
		}
	}
}

func TestSessionRuntimeDiagnosticsReportsAgendaEntriesAndTombstones(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustRuntimeGuardRuleset(t)
	session := mustSession(t, revision, "agenda-memory-diagnostics-session")

	if _, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{
		"age":    42,
		"dept":   "Engineering",
		"id":     "ada",
		"note":   "ready",
		"status": "active",
	})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := result.Fired, 1; got != want {
		t.Fatalf("fired = %d, want %d", got, want)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	agenda := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerAgenda)
	if agenda.Owner == "" {
		t.Fatalf("runtime diagnostics missing agenda owner: %#v", diagnostics.MemoryOwners)
	}
	if agenda.Rows == 0 {
		t.Fatalf("agenda rows = 0, want retained activation entries: %#v", agenda)
	}
	if agenda.Tombstones == 0 {
		t.Fatalf("agenda tombstones = 0, want consumed activation tombstones: %#v", agenda)
	}
	if agenda.Bytes == 0 {
		t.Fatalf("agenda bytes = 0, want retained byte estimate: %#v", agenda)
	}
	if agenda.HighWater == 0 {
		t.Fatalf("agenda high water = 0, want capacity estimate: %#v", agenda)
	}
}

func runtimeDiagnosticOwner(diagnostics RuntimeDiagnostics, ownerName string) RuntimeMemoryOwnerDiagnostics {
	for _, owner := range diagnostics.MemoryOwners {
		if owner.Owner == ownerName {
			return owner
		}
	}
	return RuntimeMemoryOwnerDiagnostics{}
}
