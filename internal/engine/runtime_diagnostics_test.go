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
	query, ok := revision.query("adults-by-dept")
	if !ok {
		t.Fatal("compiled query missing")
	}
	compiledArgs, err := query.compileArgs(QueryArgs{"dept": "Engineering"})
	if err != nil {
		t.Fatalf("compileArgs: %v", err)
	}
	trigger := session.queryTriggerFact(query, &compiledArgs)
	if _, err := session.rete.graphBeta.insertFactInternal(ctx, trigger, nil, false); err != nil {
		t.Fatalf("insert query trigger: %v", err)
	}
	defer func() {
		_, _ = session.rete.graphBeta.removeFactInternal(context.Background(), trigger, nil, false)
	}()

	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	rule := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerRuleTerminal)
	if rule.Owner == "" {
		t.Fatalf("runtime diagnostics missing rule terminal owner: %#v", diagnostics.MemoryOwners)
	}
	queryOwner := runtimeDiagnosticOwner(diagnostics, runtimeMemoryOwnerQueryTerminal)
	if queryOwner.Owner == "" {
		t.Fatalf("runtime diagnostics missing query terminal owner: %#v", diagnostics.MemoryOwners)
	}
	if rule.Rows == 0 {
		t.Fatalf("rule terminal rows = 0, want retained terminal rows: %#v", rule)
	}
	if rule.Buckets == 0 {
		t.Fatalf("rule terminal buckets = 0, want retained identity buckets: %#v", rule)
	}
	if rule.Indexes == 0 {
		t.Fatalf("rule terminal indexes = 0, want retained fact reverse indexes: %#v", rule)
	}
	if queryOwner.Rows == 0 {
		t.Fatalf("query terminal rows = 0, want retained query terminal rows: %#v", queryOwner)
	}
	if queryOwner.Buckets == 0 {
		t.Fatalf("query terminal buckets = 0, want retained identity buckets: %#v", queryOwner)
	}
	if queryOwner.Indexes == 0 {
		t.Fatalf("query terminal indexes = 0, want retained fact reverse indexes: %#v", queryOwner)
	}
	for _, owner := range []RuntimeMemoryOwnerDiagnostics{rule, queryOwner} {
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

	if _, err := session.AssertTemplate(ctx, item.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("AssertTemplate(a): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, item.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("AssertTemplate(b): %v", err)
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

	first, err := session.AssertTemplate(ctx, item.Key(), mustFields(t, map[string]any{"id": "a", "state": "ready"}))
	if err != nil {
		t.Fatalf("AssertTemplate(a): %v", err)
	}
	if first.DuplicateKey == "" {
		t.Fatalf("duplicate key is empty")
	}
	if _, err := session.AssertTemplate(ctx, item.Key(), mustFields(t, map[string]any{"id": "b", "state": "ready"})); err != nil {
		t.Fatalf("AssertTemplate(b): %v", err)
	}
	if _, err := session.Assert(ctx, "dynamic", mustFields(t, map[string]any{
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
