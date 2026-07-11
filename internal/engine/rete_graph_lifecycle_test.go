package engine

import (
	"context"
	"testing"
)

func TestReteGraphLifecycleBuildEmitsOwnedFinalTerminalDelta(t *testing.T) {
	tests := []struct {
		name  string
		build func(testing.TB) (*Ruleset, []SessionInitialFact)
	}{
		{name: "alpha", build: buildAlphaLifecycleRuleset},
		{name: "join", build: buildJoinLifecycleRuleset},
		{name: "negation", build: buildNegationLifecycleRuleset},
		{name: "aggregate", build: buildAggregateLifecycleRuleset},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			revision, initials := tc.build(t)
			session, err := NewSession(revision, WithInitialFacts(initials...))
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			state := session.activeFactWorkspace()
			runtime, err := newReteRuntime(revision, session.globalValues)
			if err != nil {
				t.Fatalf("newReteRuntime: %v", err)
			}
			delta, err := runtime.resetGraphBetaFromWorkspaceForGenerationWithDelta(context.Background(), &state, session.Generation())
			if err != nil {
				t.Fatalf("reset graph: %v", err)
			}
			if !delta.supported || !delta.owned {
				t.Fatalf("lifecycle delta flags = supported %t owned %t, want true/true", delta.supported, delta.owned)
			}
			if len(delta.added) != 1 || len(delta.removed) != 0 || len(delta.updated) != 0 {
				t.Fatalf("lifecycle terminal delta = +%d -%d ~%d, want +1 -0 ~0", len(delta.added), len(delta.removed), len(delta.updated))
			}
			rule, ok := revision.Rule(tc.name)
			if !ok {
				t.Fatalf("rule %q not found", tc.name)
			}
			if delta.added[0].ruleRevisionID != rule.RevisionID() || delta.added[0].identity.isZero() {
				t.Fatalf("added terminal = %#v, want rule revision %q with identity", delta.added[0], rule.RevisionID())
			}

			retainedRuleRevision := delta.added[0].ruleRevisionID
			retainedIdentity := delta.added[0].identity
			empty := newFactWorkspace(session.Generation()+1, 0)
			if _, err := runtime.resetGraphBetaFromWorkspaceForGenerationWithDelta(context.Background(), empty, empty.generation); err != nil {
				t.Fatalf("second reset graph: %v", err)
			}
			if len(delta.added) != 1 || delta.added[0].ruleRevisionID != retainedRuleRevision || delta.added[0].identity != retainedIdentity {
				t.Fatalf("retained lifecycle delta changed after reuse: %#v", delta)
			}
		})
	}
}

func TestReteGraphNegationBlockerTransitionCounters(t *testing.T) {
	ctx := context.Background()
	revision, initials := buildNegationLifecycleRuleset(t)
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	blocker, ok := revision.Template("negation-blocker")
	if !ok {
		t.Fatal("negation-blocker template not found")
	}
	session.attachPropagationCounters()

	asserted, err := session.Assert(ctx, blocker.Key(), mustFields(t, map[string]any{"item-id": "one"}))
	if err != nil {
		t.Fatalf("Assert blocker: %v", err)
	}
	if asserted.Status != AssertInserted {
		t.Fatalf("assert blocker status = %v, want %v", asserted.Status, AssertInserted)
	}
	blocked := session.propagationCounterSnapshot().Totals
	if got, want := blocked.NegativeBlockerIncrements, 1; got != want {
		t.Fatalf("blocker increments = %d, want %d", got, want)
	}
	if got, want := blocked.NegativeBlockerZeroToOne, 1; got != want {
		t.Fatalf("blocker 0->1 transitions = %d, want %d", got, want)
	}

	retracted, err := session.Retract(ctx, asserted.Fact.ID())
	if err != nil {
		t.Fatalf("Retract blocker: %v", err)
	}
	if retracted.Status != RetractRemoved {
		t.Fatalf("retract blocker status = %v, want %v", retracted.Status, RetractRemoved)
	}
	unblocked := session.propagationCounterSnapshot().Totals
	if got, want := unblocked.NegativeBlockerDecrements, 1; got != want {
		t.Fatalf("blocker decrements = %d, want %d", got, want)
	}
	if got, want := unblocked.NegativeBlockerOneToZero, 1; got != want {
		t.Fatalf("blocker 1->0 transitions = %d, want %d", got, want)
	}
}

func TestIndexedTerminalDeltaCoalescerCounters(t *testing.T) {
	arena := newTokenArena()
	removedToken := arena.addSeed(1)
	addedToken := arena.addSeed(1)
	const deltaCount = 9
	added := make([]reteTerminalTokenDelta, deltaCount)
	removed := make([]reteTerminalTokenDelta, deltaCount)
	for i := range deltaCount {
		identity := candidateIdentity{
			generation: 1,
			count:      1,
			key: candidateIdentityKey{
				scopeHash: 1,
				hash:      uint64(i + 1),
			},
		}
		added[i] = reteTerminalTokenDelta{ruleRevisionID: "rule", token: addedToken, identity: identity, terminalID: 1}
		removed[i] = reteTerminalTokenDelta{ruleRevisionID: "rule", token: removedToken, identity: identity, terminalID: 1}
	}
	counters := newPropagationCounterLedger()
	keptAdded, keptRemoved, updates := coalesceTerminalTokenDeltasWithCounters(nil, added, removed, counters)
	if len(keptAdded) != 0 || len(keptRemoved) != 0 || len(updates) != deltaCount {
		t.Fatalf("coalesced deltas = +%d -%d ~%d, want +0 -0 ~%d", len(keptAdded), len(keptRemoved), len(updates), deltaCount)
	}
	totals := counters.snapshot().Totals
	if got, want := totals.ModifyCoalescedPairs, deltaCount; got != want {
		t.Fatalf("coalesced pairs = %d, want %d", got, want)
	}
	if got, want := totals.ModifyDistinctTokenUpdates, deltaCount; got != want {
		t.Fatalf("distinct token updates = %d, want %d", got, want)
	}
	if got, want := totals.CoalescerIdentityIndexProbes, deltaCount; got != want {
		t.Fatalf("identity index probes = %d, want %d", got, want)
	}
	if got, want := totals.CoalescerIdentityIndexCandidates, deltaCount; got != want {
		t.Fatalf("identity index candidates = %d, want %d", got, want)
	}
}

func buildAlphaLifecycleRuleset(t testing.TB) (*Ruleset, []SessionInitialFact) {
	t.Helper()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{Name: "alpha-item", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}})
	mustAddAction(t, workspace, noopAction())
	mustAddRule(t, workspace, RuleSpec{
		Name:       "alpha",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(item.Key())}},
		Actions:    []RuleActionSpec{{Name: "noop"}},
	})
	return mustCompileWorkspace(t, workspace), []SessionInitialFact{{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "one"})}}
}

func buildJoinLifecycleRuleset(t testing.TB) (*Ruleset, []SessionInitialFact) {
	t.Helper()
	workspace := NewWorkspace()
	employee := mustAddTemplate(t, workspace, TemplateSpec{Name: "join-employee", Fields: []FieldSpec{{Name: "dept", Kind: ValueString, Required: true}}})
	department := mustAddTemplate(t, workspace, TemplateSpec{Name: "join-department", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}})
	mustAddAction(t, workspace, noopAction())
	mustAddRule(t, workspace, RuleSpec{
		Name: "join",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
			{Binding: "department", Target: TemplateKeyFact(department.Key()), JoinConstraints: []JoinConstraintSpec{{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}}}},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	return mustCompileWorkspace(t, workspace), []SessionInitialFact{
		{TemplateKey: employee.Key(), Fields: mustFields(t, map[string]any{"dept": "engineering"})},
		{TemplateKey: department.Key(), Fields: mustFields(t, map[string]any{"id": "engineering"})},
	}
}

func buildNegationLifecycleRuleset(t testing.TB) (*Ruleset, []SessionInitialFact) {
	t.Helper()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{Name: "negation-item", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}})
	blocker := mustAddTemplate(t, workspace, TemplateSpec{Name: "negation-blocker", Fields: []FieldSpec{{Name: "item-id", Kind: ValueString, Required: true}}})
	mustAddAction(t, workspace, noopAction())
	mustAddRule(t, workspace, RuleSpec{
		Name: "negation",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Not{Condition: Match{Binding: "blocker", Target: TemplateKeyFact(blocker.Key()), JoinConstraints: []JoinConstraintSpec{{Field: "item-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "item", Field: "id"}}}}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	return mustCompileWorkspace(t, workspace), []SessionInitialFact{{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"id": "one"})}}
}

func buildAggregateLifecycleRuleset(t testing.TB) (*Ruleset, []SessionInitialFact) {
	t.Helper()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "aggregate-item",
		DuplicatePolicy: DuplicateAllow,
		Fields:          []FieldSpec{{Name: "amount", Kind: ValueInt, Required: true}},
	})
	mustAddAction(t, workspace, noopAction())
	mustAddRule(t, workspace, RuleSpec{
		Name:          "aggregate",
		ConditionTree: Accumulate(Match{Binding: "item", Target: TemplateKeyFact(item.Key())}, Count().As("count")),
		Actions:       []RuleActionSpec{{Name: "noop"}},
	})
	return mustCompileWorkspace(t, workspace), []SessionInitialFact{
		{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"amount": 3})},
		{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"amount": 5})},
	}
}
