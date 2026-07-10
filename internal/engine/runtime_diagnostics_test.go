package engine

import (
	"context"
	"testing"
)

func TestSessionRuntimeDiagnosticsReportsBetaMemoryOwner(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session := mustSession(t, revision, "beta-memory-diagnostics-session")

	if _, err := session.Assert(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})); err != nil {
		t.Fatalf("Assert(employee): %v", err)
	}
	if _, err := session.Assert(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("Assert(department): %v", err)
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
		t.Fatalf("beta buckets = 0, want retained join buckets: %#v", beta)
	}
	if beta.Indexes != 0 {
		t.Fatalf("beta indexes = %d, want no retained beta fact reverse indexes: %#v", beta.Indexes, beta)
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

	if _, err := session.Assert(ctx, personKey, mustFields(t, map[string]any{
		"age":    42,
		"dept":   "Engineering",
		"id":     "ada",
		"note":   "ready",
		"status": "active",
	})); err != nil {
		t.Fatalf("Assert(person): %v", err)
	}
	if rows, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "Engineering"}); err != nil {
		t.Fatalf("QueryAll: %v", err)
	} else if got, want := len(rows), 1; got != want {
		t.Fatalf("query rows = %d, want %d", got, want)
	}
	query, ok := revision.query("adults-by-dept")
	if !ok {
		t.Fatal("compiled query missing")
	}
	compiledArgs, err := query.compileArgs(QueryArgs{"dept": "Engineering"})
	if err != nil {
		t.Fatalf("compileArgs: %v", err)
	}
	trigger := session.queryTriggerFact(query, &compiledArgs)
	if _, err := session.propagation.runtime.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerEvent(trigger)); err != nil {
		t.Fatalf("insert query trigger: %v", err)
	}
	defer func() {
		_, _ = session.propagation.runtime.graphBeta.propagateEvent(context.Background(), newReteGraphQueryTriggerRemoveEvent(trigger))
	}()

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	queryOwner := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerQueryTerminal)
	if queryOwner.Owner == "" {
		t.Fatalf("runtime diagnostics missing query terminal owner: %#v", diagnostics.MemoryOwners)
	}
	if rule := runtimeDiagnosticOwner(diagnostics, "rule-terminal"); rule.Owner != "" {
		t.Fatalf("runtime diagnostics retained rule terminal owner: %#v", rule)
	}
	agendaOwner := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerAgenda)
	if agendaOwner.Owner == "" || agendaOwner.Rows == 0 {
		t.Fatalf("runtime diagnostics missing agenda-owned activation state: %#v", diagnostics.MemoryOwners)
	}
	if queryOwner.Rows == 0 {
		t.Fatalf("query terminal rows = 0, want retained query terminal rows: %#v", queryOwner)
	}
	if queryOwner.Buckets != 0 {
		t.Fatalf("query terminal buckets = %d, want 0 after compact query terminal rows", queryOwner.Buckets)
	}
	if queryOwner.Indexes != 0 {
		t.Fatalf("query terminal indexes = %d, want 0 after removing fact reverse indexes", queryOwner.Indexes)
	}
	for _, owner := range []RuntimeMemoryOwnerDiagnostics{agendaOwner, queryOwner} {
		if owner.Bytes == 0 {
			t.Fatalf("%s bytes = 0, want retained byte estimate: %#v", owner.Owner, owner)
		}
		if owner.HighWater == 0 {
			t.Fatalf("%s high water = 0, want capacity estimate: %#v", owner.Owner, owner)
		}
	}
}

func TestSessionRuntimeDiagnosticsCountsClearedQueryTerminalCapacity(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustQueryRevision(t)
	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p1", "dept": "engineering", "age": 32})},
		SessionInitialFact{TemplateKey: personKey, Fields: mustFields(t, map[string]any{"id": "p2", "dept": "engineering", "age": 41})},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rows, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"}); err != nil {
		t.Fatalf("QueryAll: %v", err)
	} else if got, want := len(rows), 2; got != want {
		t.Fatalf("query rows = %d, want %d", got, want)
	}
	if got := queryTerminalRowsRetained(session.propagation.runtime.graphBeta, "adults-by-dept"); got != 0 {
		t.Fatalf("query terminal rows retained after QueryAll cleanup = %d, want 0", got)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	queryOwner := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerQueryTerminal)
	if queryOwner.Owner == "" {
		t.Fatalf("runtime diagnostics missing cleared query terminal owner: %#v", diagnostics.MemoryOwners)
	}
	if queryOwner.Rows != 0 {
		t.Fatalf("query terminal rows = %d, want 0 after QueryAll cleanup", queryOwner.Rows)
	}
	if queryOwner.Buckets != 0 || queryOwner.Indexes != 0 {
		t.Fatalf("query terminal buckets/indexes = %d/%d, want compact handle map only", queryOwner.Buckets, queryOwner.Indexes)
	}
	if queryOwner.Bytes == 0 || queryOwner.HighWater == 0 {
		t.Fatalf("query terminal retained bytes/highWater = %d/%d, want cleared compact capacity counted", queryOwner.Bytes, queryOwner.HighWater)
	}
}

func TestSessionRuntimeDiagnosticsReportsReleasedAgendaAfterRun(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustRuntimeGuardRuleset(t)
	session := mustSession(t, revision, "agenda-memory-diagnostics-session")

	if _, err := session.Assert(ctx, personKey, mustFields(t, map[string]any{
		"age":    42,
		"dept":   "Engineering",
		"id":     "ada",
		"note":   "ready",
		"status": "active",
	})); err != nil {
		t.Fatalf("Assert(person): %v", err)
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
		return
	}
	if agenda.Rows != 0 {
		t.Fatalf("agenda rows = %d, want no retained activation entries after run completion: %#v", agenda.Rows, agenda)
	}
	if agenda.Tombstones != 0 {
		t.Fatalf("agenda tombstones = %d, want no retained consumed activation tombstones after run completion: %#v", agenda.Tombstones, agenda)
	}
	if agenda.Bytes == 0 {
		t.Fatalf("agenda bytes = 0, want retained byte estimate: %#v", agenda)
	}
	if agenda.HighWater == 0 {
		t.Fatalf("agenda high water = 0, want capacity estimate: %#v", agenda)
	}
}

func TestSessionRuntimeDiagnosticsReportsAggregateMemoryOwner(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "item-count",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
		),
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-memory-diagnostics-session")

	if _, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	if _, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("Assert(b): %v", err)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	aggregate := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerAggregate)
	if aggregate.Owner == "" {
		t.Fatalf("runtime diagnostics missing aggregate owner: %#v", diagnostics.MemoryOwners)
	}
	if aggregate.Rows == 0 {
		t.Fatalf("aggregate rows = 0, want buckets, members, or result tokens: %#v", aggregate)
	}
	if aggregate.Buckets == 0 {
		t.Fatalf("aggregate buckets = 0, want retained aggregate buckets: %#v", aggregate)
	}
	if aggregate.Bytes == 0 {
		t.Fatalf("aggregate bytes = 0, want retained byte estimate: %#v", aggregate)
	}
	if aggregate.HighWater == 0 {
		t.Fatalf("aggregate high water = 0, want capacity estimate: %#v", aggregate)
	}
}

func TestSessionRuntimeDiagnosticsReportsFactMemoryOwner(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "item",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "state", Kind: ValueString, Required: true},
		},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "fact-memory-diagnostics-session")

	first, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": "a", "state": "ready"}))
	if err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	if first.DuplicateKey == "" {
		t.Fatalf("duplicate key is empty")
	}
	if _, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": "b", "state": "ready"})); err != nil {
		t.Fatalf("Assert(b): %v", err)
	}
	if _, err := session.assertByName(ctx, "dynamic", mustFields(t, map[string]any{
		"id":      "dynamic-1",
		"payload": map[string]any{"risk": 95},
	})); err != nil {
		t.Fatalf("Assert(dynamic): %v", err)
	}

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	fact := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerFact)
	if fact.Owner == "" {
		t.Fatalf("runtime diagnostics missing fact owner: %#v", diagnostics.MemoryOwners)
	}
	if got, want := fact.Rows, uint64(3); got != want {
		t.Fatalf("fact rows = %d, want %d: %#v", got, want, fact)
	}
	if fact.Buckets == 0 {
		t.Fatalf("fact buckets = 0, want retained fact-base map buckets: %#v", fact)
	}
	if fact.Indexes == 0 {
		t.Fatalf("fact indexes = 0, want fact-base and duplicate index entries: %#v", fact)
	}
	if fact.Bytes == 0 {
		t.Fatalf("fact bytes = 0, want retained byte estimate: %#v", fact)
	}
	if fact.HighWater == 0 {
		t.Fatalf("fact high water = 0, want capacity estimate: %#v", fact)
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
