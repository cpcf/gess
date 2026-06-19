package gess

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"unsafe"
)

func TestReteNetworkPlanDescribesClosedTemplateRules(t *testing.T) {
	revision := mustCompileLoanUnderwritingRuleset(t, nil)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}

	if got := len(runtime.plan.unsupported); got != 0 {
		t.Fatalf("unsupported plan reasons = %d, want 0: %#v", got, runtime.plan.unsupported)
	}
	if got, want := runtime.plan.stats.rules, len(revision.ruleOrder); got != want {
		t.Fatalf("rules = %d, want %d", got, want)
	}
	if got, want := runtime.plan.stats.conditions, 10; got != want {
		t.Fatalf("conditions = %d, want %d", got, want)
	}
	if got, want := runtime.plan.stats.alphaNodes, 10; got != want {
		t.Fatalf("alpha nodes = %d, want %d", got, want)
	}
	if got, want := runtime.plan.stats.betaNodes, 5; got != want {
		t.Fatalf("beta nodes = %d, want %d", got, want)
	}
	if got, want := runtime.plan.stats.terminalNodes, 5; got != want {
		t.Fatalf("terminal nodes = %d, want %d", got, want)
	}
	if runtime.plan.stats.unsupportedRules != 0 || runtime.plan.stats.unsupportedConditions != 0 {
		t.Fatalf("unsupported stats = %#v, want no unsupported rules or conditions", runtime.plan.stats)
	}

	metrics := runtime.metrics()
	if metrics.plan != runtime.plan.stats {
		t.Fatalf("metrics plan = %#v, want %#v", metrics.plan, runtime.plan.stats)
	}
	if got, want := len(metrics.nodes), runtime.plan.stats.alphaNodes+runtime.plan.stats.betaNodes+runtime.plan.stats.terminalNodes; got != want {
		t.Fatalf("metric node count = %d, want %d", got, want)
	}
	counts := map[reteNodeKind]int{}
	for _, node := range metrics.nodes {
		if node.id == "" {
			t.Fatalf("metric node has empty id: %#v", node)
		}
		if node.facts != 0 || node.tokens != 0 {
			t.Fatalf("metric node counts = (%d facts, %d tokens), want empty scaffold counts", node.facts, node.tokens)
		}
		counts[node.kind]++
	}
	if counts[reteNodeAlpha] != runtime.plan.stats.alphaNodes {
		t.Fatalf("alpha metric count = %d, want %d", counts[reteNodeAlpha], runtime.plan.stats.alphaNodes)
	}
	if counts[reteNodeBeta] != runtime.plan.stats.betaNodes {
		t.Fatalf("beta metric count = %d, want %d", counts[reteNodeBeta], runtime.plan.stats.betaNodes)
	}
	if counts[reteNodeTerminal] != runtime.plan.stats.terminalNodes {
		t.Fatalf("terminal metric count = %d, want %d", counts[reteNodeTerminal], runtime.plan.stats.terminalNodes)
	}
}

func TestReteRuntimeRoutesSharedClosedTemplateAlphaOnce(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-a",
		Conditions: []RuleConditionSpec{{
			Binding:     "person",
			TemplateKey: person.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-b",
		Conditions: []RuleConditionSpec{{
			Binding:     "p",
			TemplateKey: person.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("shared-alpha-counter-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	if _, err := session.AssertTemplate(ctx, person.Key(), mustFields(t, map[string]any{"age": 20})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.RHSAsserts, 0; got != want {
		t.Fatalf("rhs asserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionsTested, 1; got != want {
		t.Fatalf("conditions tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionPlansTested, 0; got != want {
		t.Fatalf("condition plans tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RuleMemoriesVisited, 0; got != want {
		t.Fatalf("rule memories visited = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionMatchesAdded, 0; got != want {
		t.Fatalf("condition matches added = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalDeltasEmitted, 2; got != want {
		t.Fatalf("terminal deltas emitted = %d, want %d", got, want)
	}

	ruleA := revision.rules["adult-a"]
	ruleB := revision.rules["adult-b"]
	if got, want := session.rete.alphaFactCount(ruleA.conditionPlans[0].id), 1; got != want {
		t.Fatalf("alpha fact count for adult-a = %d, want %d", got, want)
	}
	if got, want := session.rete.alphaFactCount(ruleB.conditionPlans[0].id), 1; got != want {
		t.Fatalf("alpha fact count for adult-b = %d, want %d", got, want)
	}
}

func TestReteRuntimeRoutesSharedClosedTemplateAlphaOnceForGeneratedFacts(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-a",
		Conditions: []RuleConditionSpec{{
			Binding:     "person",
			TemplateKey: person.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-b",
		Conditions: []RuleConditionSpec{{
			Binding:     "p",
			TemplateKey: person.Key(),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("shared-alpha-generated-counter-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	originRule := revision.rules["adult-a"]
	origin := mutationOrigin{
		ActivationID:   ActivationID("activation:shared-alpha-generated"),
		RuleID:         originRule.id,
		RuleRevisionID: originRule.revisionID,
	}
	if _, _, _, _, err := session.insertTemplateValuesImmediate(ctx, person.Key(), []Value{mustValue(t, 20)}, origin); err != nil {
		t.Fatalf("insertTemplateValuesImmediate: %v", err)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.RHSAsserts, 1; got != want {
		t.Fatalf("rhs asserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionsTested, 1; got != want {
		t.Fatalf("conditions tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionPlansTested, 0; got != want {
		t.Fatalf("condition plans tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RuleMemoriesVisited, 0; got != want {
		t.Fatalf("rule memories visited = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionMatchesAdded, 0; got != want {
		t.Fatalf("condition matches added = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalDeltasEmitted, 2; got != want {
		t.Fatalf("terminal deltas emitted = %d, want %d", got, want)
	}
}

func TestReteRuntimeReportsFallbackBoundaries(t *testing.T) {
	workspace := NewWorkspace()
	openTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Closed: false,
		Fields: []FieldSpec{{Name: "kind", Kind: ValueString}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "name-target",
		Conditions: []RuleConditionSpec{{Binding: "event", Name: "event"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "open-template",
		Conditions: []RuleConditionSpec{{Binding: "event", TemplateKey: openTemplate.Key()}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if got, want := len(runtime.plan.unsupported), 2; got != want {
		t.Fatalf("unsupported reasons = %d, want %d: %#v", got, want, runtime.plan.unsupported)
	}
	if got, want := runtime.plan.stats.unsupportedRules, 2; got != want {
		t.Fatalf("unsupported rules = %d, want %d", got, want)
	}
	if got, want := runtime.plan.stats.unsupportedConditions, 2; got != want {
		t.Fatalf("unsupported conditions = %d, want %d", got, want)
	}

	kinds := map[reteUnsupportedKind]bool{}
	for _, reason := range runtime.plan.unsupported {
		if reason.ruleID == "" || reason.ruleRevisionID == "" || reason.conditionID == "" || reason.binding == "" || reason.detail == "" {
			t.Fatalf("unsupported reason is missing identity fields: %#v", reason)
		}
		kinds[reason.kind] = true
	}
	if !kinds[reteUnsupportedNameTarget] {
		t.Fatalf("unsupported kinds = %#v, want %q", kinds, reteUnsupportedNameTarget)
	}
	if !kinds[reteUnsupportedOpenTemplate] {
		t.Fatalf("unsupported kinds = %#v, want %q", kinds, reteUnsupportedOpenTemplate)
	}
}

func TestReteRuntimeRoutesClosedTemplateSubscribersByTemplateKey(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "left",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "right",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	extra := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "extra",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "left-a",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "left",
				TemplateKey: left.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 1},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "left-b",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "left",
				TemplateKey: left.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 2},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "right-a",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "right",
				TemplateKey: right.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 1},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "extra-a",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "extra",
				TemplateKey: extra.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 1},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if got, want := len(runtime.plan.alphaRoutes[left.Key()]), 2; got != want {
		t.Fatalf("alpha subscribers for %s = %d, want %d", left.Key(), got, want)
	}
	if got, want := len(runtime.plan.betaRoutes[left.Key()]), 2; got != want {
		t.Fatalf("beta subscribers for %s = %d, want %d", left.Key(), got, want)
	}
	if got, want := len(runtime.plan.betaConditionRoutes[left.Key()]), 2; got != want {
		t.Fatalf("beta condition subscribers for %s = %d, want %d", left.Key(), got, want)
	}
	if got, want := runtime.plan.betaRoutes[left.Key()][0], revision.rules["left-a"].revisionID; got != want {
		t.Fatalf("first beta subscriber for %s = %s, want %s", left.Key(), got, want)
	}
	if got, want := runtime.plan.betaRoutes[left.Key()][1], revision.rules["left-b"].revisionID; got != want {
		t.Fatalf("second beta subscriber for %s = %s, want %s", left.Key(), got, want)
	}

	session, err := NewSession(revision, WithSessionID("route-template-key"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	origin := mutationOrigin{
		ActivationID:   ActivationID("activation:route-template-key"),
		RuleID:         revision.rules["left-a"].id,
		RuleRevisionID: revision.rules["left-a"].revisionID,
	}
	if _, _, err := session.insertFactImmediate(ctx, "", left.Key(), mustFields(t, map[string]any{"id": 1}), origin); err != nil {
		t.Fatalf("insertFactImmediate: %v", err)
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.RHSAsserts, 1; got != want {
		t.Fatalf("rhs asserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RuleMemoriesVisited, 0; got != want {
		t.Fatalf("rule memories visited = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionsTested, 2; got != want {
		t.Fatalf("conditions tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionPlansTested, 0; got != want {
		t.Fatalf("condition plans tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionMatchesAdded, 0; got != want {
		t.Fatalf("condition matches added = %d, want %d", got, want)
	}

	publicSession, err := NewSession(revision, WithSessionID("route-public-template-key"))
	if err != nil {
		t.Fatalf("NewSession public: %v", err)
	}
	publicSession.attachPropagationCounters()
	if _, err := publicSession.AssertTemplate(ctx, left.Key(), mustFields(t, map[string]any{"id": 2})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	snapshot = publicSession.propagationCounterSnapshot()
	if got, want := snapshot.Totals.Asserts, 1; got != want {
		t.Fatalf("public asserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RHSAsserts, 0; got != want {
		t.Fatalf("public rhs asserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RuleMemoriesVisited, 0; got != want {
		t.Fatalf("public rule memories visited = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionsTested, 2; got != want {
		t.Fatalf("public conditions tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionPlansTested, 0; got != want {
		t.Fatalf("public condition plans tested = %d, want %d", got, want)
	}
}

func TestReteRuntimeRoutesBetaInsertToMatchingConditionNode(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "left",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "right",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "paired",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "left",
				TemplateKey: left.Key(),
			},
			{
				Binding:     "right",
				TemplateKey: right.Key(),
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if got, want := len(runtime.plan.betaConditionRoutes[right.Key()]), 1; got != want {
		t.Fatalf("beta condition subscribers for %s = %d, want %d", right.Key(), got, want)
	}
	if got, want := runtime.plan.betaConditionRoutes[right.Key()][0].conditionIndex, 1; got != want {
		t.Fatalf("right beta condition index = %d, want %d", got, want)
	}

	session, err := NewSession(revision, WithSessionID("route-beta-condition"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	if _, err := session.AssertTemplate(ctx, right.Key(), mustFields(t, map[string]any{"id": 1})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.RuleMemoriesVisited, 0; got != want {
		t.Fatalf("rule memories visited = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionsTested, 1; got != want {
		t.Fatalf("conditions tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionPlansTested, 0; got != want {
		t.Fatalf("condition plans tested = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ConditionMatchesAdded, 0; got != want {
		t.Fatalf("condition matches added = %d, want %d", got, want)
	}
}

func TestSessionReconcileAgendaInternalUsesSessionSourceForUnsupportedPlans(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	openTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Closed: false,
		Fields: []FieldSpec{{Name: "kind", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "by-name",
		Conditions: []RuleConditionSpec{{Binding: "event", Name: "event"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "by-template",
		Conditions: []RuleConditionSpec{{Binding: "event", TemplateKey: openTemplate.Key()}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	initials := []SessionInitialFact{
		{TemplateKey: openTemplate.Key(), Fields: mustFields(t, map[string]any{"kind": "alpha"})},
		{TemplateKey: openTemplate.Key(), Fields: mustFields(t, map[string]any{"kind": "beta"})},
	}
	sessionInternal, err := NewSession(revision, WithSessionID("fallback-source-internal"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(internal): %v", err)
	}
	sessionSnapshot, err := NewSession(revision, WithSessionID("fallback-source-snapshot"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(snapshot): %v", err)
	}

	snapshot := mustSnapshot(t, ctx, sessionSnapshot)
	snapshotChanges, err := sessionSnapshot.reconcileAgenda(ctx, snapshot)
	if err != nil {
		t.Fatalf("snapshot reconcileAgenda: %v", err)
	}
	internalChanges, err := sessionInternal.reconcileAgendaInternal(ctx)
	if err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}

	if !reflect.DeepEqual(internalChanges, snapshotChanges) {
		t.Fatalf("internal reconcile changes differ from snapshot reconcile:\ninternal=%#v\nsnapshot=%#v", internalChanges, snapshotChanges)
	}
	if !reflect.DeepEqual(sessionInternal.agenda.pendingActivations(), sessionSnapshot.agenda.pendingActivations()) {
		t.Fatalf("internal pending activations differ from snapshot reconcile:\ninternal=%#v\nsnapshot=%#v", sessionInternal.agenda.pendingActivations(), sessionSnapshot.agenda.pendingActivations())
	}
}

func TestSessionReconcileAgendaWithoutSnapshotUsesTerminalTokensForBetaPlans(t *testing.T) {
	ctx := context.Background()
	revision := mustCompileLoanUnderwritingRuleset(t, nil)
	initials := loanUnderwritingTemplateInitialFacts(t)

	terminalSession, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(terminal): %v", err)
	}
	snapshotSession, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(snapshot): %v", err)
	}

	snapshot := mustSnapshot(t, ctx, snapshotSession)
	snapshotChanges, err := snapshotSession.reconcileAgenda(ctx, snapshot)
	if err != nil {
		t.Fatalf("snapshot reconcileAgenda: %v", err)
	}

	terminalChanges, ok, err := terminalSession.reconcileAgendaWithoutSnapshot(ctx)
	if err != nil {
		t.Fatalf("reconcileAgendaWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("reconcileAgendaWithoutSnapshot unexpectedly unavailable for beta-backed session")
	}

	if !agendaChangesPublicEqual(terminalSession.agenda, terminalChanges, snapshotSession.agenda, snapshotChanges) {
		t.Fatalf("terminal-token reconcile changes differ from snapshot reconcile:\nterminal=%#v\nsnapshot=%#v", terminalChanges, snapshotChanges)
	}
	if !reflect.DeepEqual(terminalSession.agenda.pendingActivations(), snapshotSession.agenda.pendingActivations()) {
		t.Fatalf("terminal-token pending activations differ from snapshot reconcile:\nterminal=%#v\nsnapshot=%#v", terminalSession.agenda.pendingActivations(), snapshotSession.agenda.pendingActivations())
	}
}

func TestReteRuntimeParityHarnessMatchesLoanUnderwritingOracle(t *testing.T) {
	ctx := context.Background()
	revision := mustCompileLoanUnderwritingRuleset(t, nil)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if got := len(runtime.plan.unsupported); got != 0 {
		t.Fatalf("unsupported plan reasons = %#v, want none", runtime.plan.unsupported)
	}

	session := mustSession(t, revision, "rete-parity-session")
	for _, fact := range loanUnderwritingInitialFacts(t) {
		if _, err := session.AssertTemplate(ctx, fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	if session.rete == nil || session.rete.alpha == nil || (session.rete.beta == nil && session.rete.graphBeta == nil) {
		t.Fatalf("session Rete runtime = %#v, want populated alpha and beta memories", session.rete)
	}
	snapshot := mustSnapshot(t, ctx, session)

	assertMatcherParity(t, revision, snapshot, newNaiveMatcher(revision), runtime)
	assertMatcherParity(t, revision, snapshot, newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeAlphaMemoryMaintainsAssertModifyRetractParity(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
		{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
	})
	session := mustSession(t, revision, "alpha-lifecycle-session")

	young, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 17, "status": "active"}))
	if err != nil {
		t.Fatalf("AssertTemplate young: %v", err)
	}
	inactive, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "inactive"}))
	if err != nil {
		t.Fatalf("AssertTemplate inactive: %v", err)
	}
	active, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 22, "status": "active"}))
	if err != nil {
		t.Fatalf("AssertTemplate active: %v", err)
	}
	assertAlphaMemoryFillerFacts(t, session, templateKey, reteAlphaMinimumFacts-3)

	assertAlphaMemoryCount(t, session, "adult-active", 1)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	if _, err := session.Modify(ctx, inactive.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"status": "active"})}); err != nil {
		t.Fatalf("Modify inactive: %v", err)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 2)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	if _, err := session.Modify(ctx, active.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"age": 16})}); err != nil {
		t.Fatalf("Modify active: %v", err)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 1)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	if _, err := session.Retract(ctx, inactive.Fact.ID()); err != nil {
		t.Fatalf("Retract inactive: %v", err)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 0)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	if _, err := session.Retract(ctx, young.Fact.ID()); err != nil {
		t.Fatalf("Retract young: %v", err)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 0)
}

func TestReteRuntimeAlphaMemoryResetRebuildsForInitialFacts(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
		{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
	})
	conditionID := revision.rules["adult-active"].conditionPlans[0].id
	initials := []SessionInitialFact{
		{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 18, "status": "active"})},
		{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 22, "status": "active"})},
		{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 16, "status": "active"})},
	}
	for i := len(initials); i < reteAlphaMinimumFacts; i++ {
		initials = append(initials, SessionInitialFact{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 15, "status": "inactive"})})
	}
	session, err := NewSession(revision, WithSessionID("alpha-reset-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 2)

	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 40, "status": "active"})); err != nil {
		t.Fatalf("AssertTemplate extra: %v", err)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 3)
	alphaMemory := session.rete.alpha
	alphaConditionMemory := alphaMemory.conditions[conditionID]
	alphaFactsPtr := reflect.ValueOf(alphaConditionMemory.facts).Pointer()
	alphaIndexesPtr := reflect.ValueOf(alphaConditionMemory.indexes).Pointer()

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.rete.alpha != alphaMemory {
		t.Fatalf("alpha memory pointer changed across reset: got %p want %p", session.rete.alpha, alphaMemory)
	}
	if session.rete.alpha.conditions[conditionID] != alphaConditionMemory {
		t.Fatalf("alpha condition memory pointer changed across reset: got %p want %p", session.rete.alpha.conditions[conditionID], alphaConditionMemory)
	}
	if got := reflect.ValueOf(session.rete.alpha.conditions[conditionID].facts).Pointer(); got != alphaFactsPtr {
		t.Fatalf("alpha facts backing array pointer changed across reset: got %#x want %#x", got, alphaFactsPtr)
	}
	if got := reflect.ValueOf(session.rete.alpha.conditions[conditionID].indexes).Pointer(); got != alphaIndexesPtr {
		t.Fatalf("alpha index map pointer changed across reset: got %#x want %#x", got, alphaIndexesPtr)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 2)
	assertAlphaMemoryGeneration(t, session, "adult-active", session.Generation())
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeAlphaMemoryApplyRulesetRebuildsForNewRevision(t *testing.T) {
	ctx := context.Background()
	revision1, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
		{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
	})
	revision2, _ := mustAlphaMemoryRuleset(t, "young-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintLessThan, Value: 18},
		{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
	})
	session := mustSession(t, revision1, "alpha-apply-session")
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 17, "status": "active"})); err != nil {
		t.Fatalf("AssertTemplate young: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"})); err != nil {
		t.Fatalf("AssertTemplate adult: %v", err)
	}
	assertAlphaMemoryFillerFacts(t, session, templateKey, reteAlphaMinimumFacts-2)
	assertAlphaMemoryCount(t, session, "adult-active", 1)

	if _, err := session.ApplyRuleset(ctx, revision2); err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	assertAlphaMemoryCount(t, session, "young-active", 1)
	assertMatcherParity(t, revision2, mustSnapshot(t, ctx, session), newNaiveMatcher(revision2), session.rete)
}

func TestReteRuntimeUnsupportedPlanFallsBackToOracle(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "dynamic-event",
		Conditions: []RuleConditionSpec{{Binding: "event", Name: "event"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "alpha-fallback-session")
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"kind": "created"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if len(runtime.plan.unsupported) == 0 {
		t.Fatalf("unsupported plan reasons = %#v, want fallback reasons", runtime.plan.unsupported)
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), runtime)
}

func TestReteRuntimeBetaMemoryMaintainsParityAcrossLifecycle(t *testing.T) {
	ctx := context.Background()
	revision1, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	initials := mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)
	session, err := NewSession(revision1, WithSessionID("beta-lifecycle-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil {
		t.Fatal("expected beta session to initialize Rete runtime")
	}
	if !session.rete.plan.betaSupported {
		t.Fatalf("beta plan = %#v, want supported", session.rete.plan)
	}
	if session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision1, session)
		return
	}
	if session.rete.beta == nil {
		t.Fatal("expected beta memory to be initialized")
	}
	betaMemory := session.rete.beta

	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)
	assertReteRuntimeMatchWithoutSnapshotParity(t, session)
	assertBetaTokenRefsUseArena(t, betaMemory.rules[revision1.rules["employee-department"].revisionID])

	betaRuleMemory := betaMemory.rules[revision1.rules["employee-department"].revisionID]
	trackedPrefix := betaRuleMemory.terminalPrefixRows()[0].prefix
	trackedToken := trackedPrefix.token
	if trackedToken.isZero() {
		t.Fatal("tracked terminal token is zero")
	}

	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ben", "dept": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate(Ben): %v", err)
	}
	if session.rete.beta != betaMemory {
		t.Fatal("assert rebuilt beta memory, want incremental update")
	}
	assertBetaTokenRefsUseArena(t, betaRuleMemory)
	updatedPrefix := findBetaPrefixRowByToken(t, betaRuleMemory.terminalPrefixRows(), trackedToken).prefix
	if !tokenRefEqual(updatedPrefix.token, trackedToken) {
		t.Fatalf("terminal beta token changed after append: got %#v want %#v", updatedPrefix.token, trackedToken)
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)
	assertReteRuntimeMatchWithoutSnapshotParity(t, session)

	employee := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "Ada")
	if _, err := session.Modify(ctx, employee.ID(), FactPatch{Set: mustFields(t, map[string]any{"dept": "Sales"})}); err != nil {
		t.Fatalf("Modify(Ada): %v", err)
	}
	if session.rete.beta != betaMemory {
		t.Fatal("modify rebuilt beta memory, want incremental update")
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)
	assertReteRuntimeMatchWithoutSnapshotParity(t, session)

	salesDepartment := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Sales")
	if _, err := session.Retract(ctx, salesDepartment.ID()); err != nil {
		t.Fatalf("Retract(Sales department): %v", err)
	}
	if session.rete.beta != betaMemory {
		t.Fatal("retract rebuilt beta memory, want incremental update")
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)
	assertReteRuntimeMatchWithoutSnapshotParity(t, session)

	resetResult, err := session.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if resetResult.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", resetResult.Status, ResetApplied)
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)
	assertReteRuntimeMatchWithoutSnapshotParity(t, session)

	workspace2 := NewWorkspace()
	noise2 := mustAddTemplate(t, workspace2, TemplateSpec{
		Name:   "noise",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	employee2 := mustAddTemplate(t, workspace2, TemplateSpec{
		Name:   "employee",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department2 := mustAddTemplate(t, workspace2, TemplateSpec{
		Name:   "department",
		Closed: true,
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace2, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace2, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", TemplateKey: employee2.Key()},
			{
				Binding:     "department",
				TemplateKey: department2.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace2, RuleSpec{
		Name: "engineering-employee",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "employee",
				TemplateKey: employee2.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "dept", Operator: FieldConstraintEqual, Value: "Engineering"},
				},
			},
			{
				Binding:     "department",
				TemplateKey: department2.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: "Engineering"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision2 := mustCompileWorkspace(t, workspace2)

	result, err := session.ApplyRuleset(ctx, revision2)
	if err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	if result.Status != ApplyRulesetApplied {
		t.Fatalf("apply status = %v, want %v", result.Status, ApplyRulesetApplied)
	}
	if session.rete == nil || session.rete.beta == nil || !session.rete.plan.betaSupported {
		t.Fatalf("beta runtime after apply = %#v", session.rete)
	}
	assertMatcherParity(t, revision2, mustSnapshot(t, ctx, session), newNaiveMatcher(revision2), session.rete)
	_ = noise2
}

func TestReteRuntimeBetaConditionRowsCompactAfterRetractAndReadd(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "employee",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "label", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", TemplateKey: employee.Key()},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	initials := make([]SessionInitialFact, 0, reteAlphaMinimumFacts+4)
	for i := range reteAlphaMinimumFacts {
		initials = append(initials, SessionInitialFact{
			TemplateKey: noise.Key(),
			Fields:      mustFields(t, map[string]any{"bucket": i}),
		})
	}
	initials = append(initials,
		SessionInitialFact{
			TemplateKey: employee.Key(),
			Fields:      mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"}),
		},
		SessionInitialFact{
			TemplateKey: department.Key(),
			Fields:      mustFields(t, map[string]any{"id": "Engineering", "label": "east"}),
		},
		SessionInitialFact{
			TemplateKey: department.Key(),
			Fields:      mustFields(t, map[string]any{"id": "Engineering", "label": "west"}),
		},
		SessionInitialFact{
			TemplateKey: department.Key(),
			Fields:      mustFields(t, map[string]any{"id": "Sales", "label": "south"}),
		},
	)
	session, err := NewSession(revision, WithSessionID("beta-condition-row-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	rule := revision.rules["employee-department"]
	memory := session.rete.beta.rules[rule.revisionID]
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	engineeringKey := betaJoinKey{kind: betaJoinKeyString, stringValue: "Engineering"}
	beforeBucket := conditionIndexBucketIDs(memory.conditionIndexes[1][engineeringKey])
	if got, want := len(beforeBucket), 2; got != want {
		t.Fatalf("engineering bucket size before update = %d, want %d", got, want)
	}
	if got, want := len(memory.conditionMatches[1]), 3; got != want {
		t.Fatalf("condition row count before update = %d, want %d", got, want)
	}
	if added := memory.addConditionMatch(1, memory.conditionMatches[1][0].match); added {
		t.Fatal("duplicate condition row was added")
	}
	if got := len(memory.conditionMatches[1]); got != 3 {
		t.Fatalf("condition row count after duplicate add = %d, want 3", got)
	}
	if got := memory.conditionIndexes[1][engineeringKey].len(); got != 2 {
		t.Fatalf("engineering bucket size after duplicate add = %d, want 2", got)
	}

	retractedID := memory.conditionMatches[1][0].match.fact.ID()
	if _, err := session.Retract(ctx, retractedID); err != nil {
		t.Fatalf("Retract(%s): %v", retractedID, err)
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after retract = %#v, want populated memories", session.rete)
	}
	if got, want := len(memory.conditionMatches[1]), 2; got != want {
		t.Fatalf("condition row count after retract = %d, want %d", got, want)
	}
	afterRetractBucket := conditionIndexBucketIDs(memory.conditionIndexes[1][engineeringKey])
	if got, want := len(afterRetractBucket), 1; got != want {
		t.Fatalf("engineering bucket size after retract = %d, want %d", got, want)
	}
	if got, _ := memory.conditionMatches[1][afterRetractBucket[0]].match.fact.compiledFieldValue("id", 0); got.Kind() != ValueString || got.data.(string) != "Engineering" {
		t.Fatalf("engineering bucket row after retract has id %s, want Engineering", got.String())
	}

	readded, err := session.AssertTemplate(ctx, department.Key(), mustFields(t, map[string]any{"id": "Engineering", "label": "north"}))
	if err != nil {
		t.Fatalf("AssertTemplate(Engineering): %v", err)
	}
	if readded.Status != AssertInserted {
		t.Fatalf("readded status = %v, want %v", readded.Status, AssertInserted)
	}
	if got, want := len(memory.conditionMatches[1]), 3; got != want {
		t.Fatalf("condition row count after re-add = %d, want %d", got, want)
	}
	if memory.conditionMatches[1][2].match.fact.ID() != readded.Fact.ID() {
		t.Fatalf("re-added condition row fact ID = %s, want %s", memory.conditionMatches[1][2].match.fact.ID(), readded.Fact.ID())
	}
	afterReaddBucket := conditionIndexBucketIDs(memory.conditionIndexes[1][engineeringKey])
	if got, want := len(afterReaddBucket), 2; got != want {
		t.Fatalf("engineering bucket size after re-add = %d, want %d", got, want)
	}
	if !conditionBucketContainsFact(memory, 1, engineeringKey, readded.Fact.ID()) {
		t.Fatalf("engineering bucket after re-add = %#v, want fact %s", afterReaddBucket, readded.Fact.ID())
	}

	matches, err := memory.matchesForLeftPrefix(1, memory.prefixes[0][0].prefix)
	if err != nil {
		t.Fatalf("matchesForLeftPrefix: %v", err)
	}
	if got, want := len(matches), 2; got != want {
		t.Fatalf("matches for employee prefix after re-add = %d, want %d", got, want)
	}
	if !conditionMatchesContainFact(matches, readded.Fact.ID()) {
		t.Fatalf("matches after re-add = %#v, want fact %s", matches, readded.Fact.ID())
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeBetaPrefixRowsCompactAfterRetractAndReadd(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	initials := mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)
	session, err := NewSession(revision, WithSessionID("beta-prefix-row-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	rule := revision.rules["employee-department"]
	memory := session.rete.beta.rules[rule.revisionID]
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}

	engineeringKey := betaJoinKey{kind: betaJoinKeyString, stringValue: "Engineering"}
	beforeBucket := prefixIndexBucketIDs(memory.prefixIndexes[0][engineeringKey])
	if got, want := len(beforeBucket), 1; got != want {
		t.Fatalf("engineering prefix bucket size before update = %d, want %d", got, want)
	}
	if got, want := len(memory.prefixes[0]), 1; got != want {
		t.Fatalf("condition 0 prefix row count before update = %d, want %d", got, want)
	}
	retractedMatch, ok := memory.prefixes[0][0].prefix.token.matchAt(0)
	if !ok {
		t.Fatal("condition 0 prefix token did not resolve")
	}
	retractedID := retractedMatch.fact.ID()
	if _, err := session.Retract(ctx, retractedID); err != nil {
		t.Fatalf("Retract(%s): %v", retractedID, err)
	}
	afterRetractBucket := prefixIndexBucketIDs(memory.prefixIndexes[0][engineeringKey])
	if got, want := len(memory.prefixes[0]), 0; got != want {
		t.Fatalf("condition 0 prefix row count after retract = %d, want %d", got, want)
	}
	if got, want := len(afterRetractBucket), 0; got != want {
		t.Fatalf("engineering prefix bucket size after retract = %d, want %d", got, want)
	}

	readded, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Grace", "dept": "Engineering"}))
	if err != nil {
		t.Fatalf("AssertTemplate(Grace): %v", err)
	}
	if readded.Status != AssertInserted {
		t.Fatalf("readded status = %v, want %v", readded.Status, AssertInserted)
	}
	if got, want := len(memory.prefixes[0]), 1; got != want {
		t.Fatalf("condition 0 prefix row count after re-add = %d, want %d", got, want)
	}
	if memory.prefixes[0][0].id != 0 {
		t.Fatalf("re-added prefix row = %#v, want rowID 0", memory.prefixes[0][0])
	}
	afterReaddBucket := prefixIndexBucketIDs(memory.prefixIndexes[0][engineeringKey])
	if got, want := len(afterReaddBucket), 1; got != want {
		t.Fatalf("engineering prefix bucket size after re-add = %d, want %d", got, want)
	}
	if afterReaddBucket[0] != 0 {
		t.Fatalf("engineering prefix bucket row IDs after re-add = %#v, want [0]", afterReaddBucket)
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeBetaRowsCompactAfterModifyWithoutTerminalRemovals(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	ticket := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "ticket",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "active-approved",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "ticket",
				TemplateKey: ticket.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				},
			},
			{
				Binding:     "review",
				TemplateKey: ticket.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "approved"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "beta-row-compaction-no-terminal-removals-session")

	inserted, err := session.AssertTemplate(ctx, ticket.Key(), mustFields(t, map[string]any{"status": "active"}))
	if err != nil {
		t.Fatalf("AssertTemplate(active): %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after insert = %#v, want populated memories", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	rule := revision.rules["active-approved"]
	memory := session.rete.beta.rules[rule.revisionID]
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	if got, want := len(memory.conditionMatches[0]), 1; got != want {
		t.Fatalf("condition 0 row count before modify = %d, want %d", got, want)
	}
	if got, want := len(memory.prefixes[0]), 1; got != want {
		t.Fatalf("condition 0 prefix row count before modify = %d, want %d", got, want)
	}
	if memory.conditionMatches[0][0].id != 0 {
		t.Fatalf("initial condition row = %#v, want rowID 0", memory.conditionMatches[0][0])
	}
	if memory.prefixes[0][0].id != 0 {
		t.Fatalf("initial prefix row = %#v, want rowID 0", memory.prefixes[0][0])
	}

	_, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"status": "inactive"})}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate(inactive): %v", err)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals after inactive modify = %d, want 0", got)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta(inactive): %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta(inactive) unexpectedly skipped")
	}
	if got, want := len(memory.conditionMatches[0]), 0; got != want {
		t.Fatalf("condition 0 row count after removal = %d, want %d", got, want)
	}
	if got, want := len(memory.prefixes[0]), 0; got != want {
		t.Fatalf("condition 0 prefix row count after removal = %d, want %d", got, want)
	}
	if got, want := len(memory.conditionIndexes[0]), 0; got != want {
		t.Fatalf("condition 0 index count after compaction = %d, want %d", got, want)
	}
	if got, want := len(memory.prefixIndexes[0]), 0; got != want {
		t.Fatalf("condition 0 prefix index count after compaction = %d, want %d", got, want)
	}

	readded, err := session.Modify(ctx, inserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"status": "active"})})
	if err != nil {
		t.Fatalf("Modify(active): %v", err)
	}
	if readded.Status != ModifyChanged {
		t.Fatalf("readded modify status = %v, want %v", readded.Status, ModifyChanged)
	}
	if got, want := len(memory.conditionMatches[0]), 1; got != want {
		t.Fatalf("condition 0 row count after re-add = %d, want %d", got, want)
	}
	if memory.conditionMatches[0][0].id != 0 {
		t.Fatalf("re-added condition row = %#v, want rowID 0", memory.conditionMatches[0][0])
	}
	if got, want := len(memory.prefixes[0]), 1; got != want {
		t.Fatalf("condition 0 prefix row count after re-add = %d, want %d", got, want)
	}
	if memory.prefixes[0][0].id != 0 {
		t.Fatalf("re-added prefix row = %#v, want rowID 0", memory.prefixes[0][0])
	}
	if got, want := len(memory.conditionIndexes[0]), 0; got != want {
		t.Fatalf("condition 0 index count after re-add = %d, want %d", got, want)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeBetaIndexedJoinsSkipTombstonedRowsAcrossReadd(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("beta-indexed-join-tombstone-session"),
		WithInitialFacts(mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	rule := revision.rules["employee-department"]
	memory := session.rete.beta.rules[rule.revisionID]
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}

	engineeringKey := betaJoinKey{kind: betaJoinKeyString, stringValue: "Engineering"}
	if got, want := memory.prefixIndexes[0][engineeringKey].len(), 1; got != want {
		t.Fatalf("engineering prefix bucket size before readd = %d, want %d", got, want)
	}
	if got, want := memory.conditionIndexes[1][engineeringKey].len(), 1; got != want {
		t.Fatalf("engineering condition bucket size before readd = %d, want %d", got, want)
	}

	employee := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "Ada")
	engineering := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	if _, err := session.Retract(ctx, employee.ID()); err != nil {
		t.Fatalf("Retract(%s): %v", employee.ID(), err)
	}
	if _, err := session.Retract(ctx, engineering.ID()); err != nil {
		t.Fatalf("Retract(%s): %v", engineering.ID(), err)
	}
	if got, want := len(memory.prefixes[0]), 0; got != want {
		t.Fatalf("condition 0 prefix row count after retracts = %d, want %d", got, want)
	}
	if got, want := len(memory.conditionMatches[1]), 1; got != want {
		t.Fatalf("condition row count after retracts = %d, want %d", got, want)
	}
	if got, want := memory.prefixIndexes[0][engineeringKey].len(), 0; got != want {
		t.Fatalf("engineering prefix bucket size after retracts = %d, want %d", got, want)
	}
	if got, want := memory.conditionIndexes[1][engineeringKey].len(), 0; got != want {
		t.Fatalf("engineering condition bucket size after retracts = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after retracts = %d, want 0", got)
	}

	readdedDepartment, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"}))
	if err != nil {
		t.Fatalf("AssertTemplate(Engineering): %v", err)
	}
	if readdedDepartment.Status != AssertInserted {
		t.Fatalf("readded department status = %v, want %v", readdedDepartment.Status, AssertInserted)
	}
	if got, want := len(memory.conditionMatches[1]), 2; got != want {
		t.Fatalf("condition row count after department re-add = %d, want %d", got, want)
	}
	if memory.conditionMatches[1][1].id != 1 {
		t.Fatalf("re-added condition row = %#v, want rowID 1", memory.conditionMatches[1][1])
	}
	if memory.conditionMatches[1][1].match.fact.ID() != readdedDepartment.Fact.ID() {
		t.Fatalf("re-added condition row fact ID = %s, want %s", memory.conditionMatches[1][1].match.fact.ID(), readdedDepartment.Fact.ID())
	}
	afterDepartmentBucket := conditionIndexBucketIDs(memory.conditionIndexes[1][engineeringKey])
	if got, want := len(afterDepartmentBucket), 1; got != want {
		t.Fatalf("engineering condition bucket size after department re-add = %d, want %d", got, want)
	}
	if afterDepartmentBucket[0] != 1 {
		t.Fatalf("engineering condition bucket row IDs after department re-add = %#v, want [1]", afterDepartmentBucket)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after department re-add = %d, want 0", got)
	}

	readdedEmployee, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"}))
	if err != nil {
		t.Fatalf("AssertTemplate(Ada): %v", err)
	}
	if readdedEmployee.Status != AssertInserted {
		t.Fatalf("readded employee status = %v, want %v", readdedEmployee.Status, AssertInserted)
	}
	if got, want := len(memory.prefixes[0]), 1; got != want {
		t.Fatalf("prefix row count after employee re-add = %d, want %d", got, want)
	}
	if memory.prefixes[0][0].id != 0 {
		t.Fatalf("re-added prefix row = %#v, want rowID 0", memory.prefixes[0][0])
	}
	readdedMatch, ok := memory.prefixes[0][0].prefix.token.matchAt(0)
	if !ok {
		t.Fatal("re-added prefix token did not resolve")
	}
	if readdedMatch.fact.ID() != readdedEmployee.Fact.ID() {
		t.Fatalf("re-added prefix row fact ID = %s, want %s", readdedMatch.fact.ID(), readdedEmployee.Fact.ID())
	}
	afterEmployeeBucket := prefixIndexBucketIDs(memory.prefixIndexes[0][engineeringKey])
	if got, want := len(afterEmployeeBucket), 1; got != want {
		t.Fatalf("engineering prefix bucket size after employee re-add = %d, want %d", got, want)
	}
	if afterEmployeeBucket[0] != 0 {
		t.Fatalf("engineering prefix bucket row IDs after employee re-add = %#v, want [0]", afterEmployeeBucket)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after employee re-add = %d, want 1", got)
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeBetaNoJoinSuccessorUsesLiveConditionRows(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Closed: true,
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Closed: true,
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "left-right-no-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", TemplateKey: left.Key()},
			{Binding: "right", TemplateKey: right.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initials := make([]SessionInitialFact, 0, reteAlphaMinimumFacts+1)
	for i := range reteAlphaMinimumFacts {
		initials = append(initials, SessionInitialFact{
			TemplateKey: noise.Key(),
			Fields:      mustFields(t, map[string]any{"bucket": i}),
		})
	}
	initials = append(initials, SessionInitialFact{
		TemplateKey: right.Key(),
		Fields:      mustFields(t, map[string]any{"id": "r1"}),
	})
	session, err := NewSession(revision, WithSessionID("beta-no-join-successor-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations before left assert = %d, want 0", got)
	}

	inserted, err := session.AssertTemplate(ctx, left.Key(), mustFields(t, map[string]any{"id": "l1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(left): %v", err)
	}
	if inserted.Status != AssertInserted {
		t.Fatalf("left assert status = %v, want %v", inserted.Status, AssertInserted)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after left assert = %d, want 1", got)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeAgendaDeltasMaintainParityAcrossLifecycle(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("beta-agenda-delta-session"),
		WithInitialFacts(mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if _, err := session.reconcileAgenda(ctx, mustSnapshot(t, ctx, session)); err != nil {
		t.Fatalf("initial reconcileAgenda: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ben", "dept": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate(Ben): %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	employee := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "Ada")
	if _, err := session.Modify(ctx, employee.ID(), FactPatch{Set: mustFields(t, map[string]any{"dept": "Sales"})}); err != nil {
		t.Fatalf("Modify(Ada): %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	salesDepartment := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Sales")
	if _, err := session.Retract(ctx, salesDepartment.ID()); err != nil {
		t.Fatalf("Retract(Sales department): %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeAgendaDeltasMaintainParityForSmallSupportedSession(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("beta-small-agenda-delta-session"),
		WithInitialFacts(
			SessionInitialFact{TemplateKey: employeeKey, Fields: mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})},
			SessionInitialFact{TemplateKey: departmentKey, Fields: mustFields(t, map[string]any{"id": "Engineering"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if _, err := session.reconcileAgenda(ctx, mustSnapshot(t, ctx, session)); err != nil {
		t.Fatalf("initial reconcileAgenda: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ben", "dept": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate(Ben): %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	ben := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "Ben")
	if _, err := session.Modify(ctx, ben.ID(), FactPatch{Set: mustFields(t, map[string]any{"dept": "Sales"})}); err != nil {
		t.Fatalf("Modify(Ben): %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate(Sales department): %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeTerminalTokenDeltasMatchCandidateDeltas(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("beta-terminal-delta-parity-session"),
		WithInitialFacts(mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}

	results, err := session.rete.match(ctx, session.indexedSnapshotLocked())
	if err != nil {
		t.Fatalf("initial Rete match: %v", err)
	}
	candidateAgenda := newAgenda()
	if _, err := candidateAgenda.reconcile(ctx, revision, results); err != nil {
		t.Fatalf("candidate agenda initial reconcile: %v", err)
	}
	directAgenda := newAgenda()
	if _, err := directAgenda.reconcile(ctx, revision, results); err != nil {
		t.Fatalf("direct agenda initial reconcile: %v", err)
	}

	_, assertDelta, err := session.insertFactImmediate(ctx, "", employeeKey, mustFields(t, map[string]any{
		"name": "Ben",
		"dept": "Sales",
	}), mutationOrigin{})
	if err != nil {
		t.Fatalf("insertFactImmediate(Ben): %v", err)
	}
	firstChanges := assertTerminalTokenDeltaMatchesCandidateDelta(t, revision, session, candidateAgenda, directAgenda, assertDelta)
	if got, want := len(firstChanges), 1; got != want {
		t.Fatalf("assert direct changes = %d, want %d", got, want)
	}
	firstFactID := cloneActivationFactIDs(&firstChanges[0].activation)[0]

	employee := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "Ada")
	_, modifyDelta, err := session.modifyImmediate(ctx, employee.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"dept": "Sales"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate(Ada): %v", err)
	}
	assertTerminalTokenDeltaMatchesCandidateDelta(t, revision, session, candidateAgenda, directAgenda, modifyDelta)

	salesDepartment := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Sales")
	_, retractDelta, err := session.retractImmediate(ctx, salesDepartment.ID(), mutationOrigin{})
	if err != nil {
		t.Fatalf("retractImmediate(Sales): %v", err)
	}
	assertTerminalTokenDeltaMatchesCandidateDelta(t, revision, session, candidateAgenda, directAgenda, retractDelta)

	if got := cloneActivationFactIDs(&firstChanges[0].activation)[0]; got != firstFactID {
		t.Fatalf("returned direct change was mutated after later deltas: got %q want %q", got, firstFactID)
	}
}

func TestReteRuntimeRejectsNilRuleset(t *testing.T) {
	runtime, err := newReteRuntime(nil)
	if err != ErrInvalidRuleset {
		t.Fatalf("newReteRuntime(nil) error = %v, want %v", err, ErrInvalidRuleset)
	}
	if runtime != nil {
		t.Fatalf("newReteRuntime(nil) runtime = %#v, want nil", runtime)
	}
}

func TestReteRuntimeFallsBackForNumericJoinPlans(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "threshold",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "candidate",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "older-than-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: threshold.Key()},
			{
				Binding:     "candidate",
				TemplateKey: candidate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initials := make([]SessionInitialFact, 0, reteAlphaMinimumFacts+3)
	for i := range reteAlphaMinimumFacts {
		initials = append(initials, SessionInitialFact{
			TemplateKey: noise.Key(),
			Fields:      mustFields(t, map[string]any{"bucket": i}),
		})
	}
	initials = append(initials,
		SessionInitialFact{TemplateKey: threshold.Key(), Fields: mustFields(t, map[string]any{"age": 20})},
		SessionInitialFact{TemplateKey: candidate.Key(), Fields: mustFields(t, map[string]any{"age": 10})},
		SessionInitialFact{TemplateKey: candidate.Key(), Fields: mustFields(t, map[string]any{"age": 30})},
	)
	session, err := NewSession(revision, WithSessionID("numeric-join-fallback-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil {
		t.Fatal("expected Rete runtime")
	}
	if session.rete.plan.betaSupported {
		t.Fatalf("beta plan = %#v, want unsupported for numeric joins", session.rete.plan)
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeUsesBetaForSupportedRulesWithMixedFallback(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "employee",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Closed: true,
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "threshold",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "candidate",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", TemplateKey: employee.Key()},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "older-than-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: threshold.Key()},
			{
				Binding:     "candidate",
				TemplateKey: candidate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initials := make([]SessionInitialFact, 0, reteAlphaMinimumFacts+5)
	for i := range reteAlphaMinimumFacts {
		initials = append(initials, SessionInitialFact{
			TemplateKey: noise.Key(),
			Fields:      mustFields(t, map[string]any{"bucket": i}),
		})
	}
	initials = append(initials,
		SessionInitialFact{TemplateKey: employee.Key(), Fields: mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})},
		SessionInitialFact{TemplateKey: department.Key(), Fields: mustFields(t, map[string]any{"id": "Engineering"})},
		SessionInitialFact{TemplateKey: threshold.Key(), Fields: mustFields(t, map[string]any{"age": 20})},
		SessionInitialFact{TemplateKey: candidate.Key(), Fields: mustFields(t, map[string]any{"age": 10})},
		SessionInitialFact{TemplateKey: candidate.Key(), Fields: mustFields(t, map[string]any{"age": 30})},
	)
	session, err := NewSession(revision, WithSessionID("mixed-beta-fallback-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.beta == nil || !session.rete.plan.betaSupported {
		t.Fatalf("beta runtime = %#v, want mixed beta support", session.rete)
	}
	equalityRule := revision.rules["employee-department"]
	numericRule := revision.rules["older-than-threshold"]
	if session.rete.beta.rules[equalityRule.revisionID] == nil {
		t.Fatal("equality join rule did not get beta memory")
	}
	if session.rete.beta.rules[numericRule.revisionID] != nil {
		t.Fatal("numeric join rule unexpectedly got beta memory")
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
	if results, ok, err := session.rete.matchWithoutSnapshot(ctx, session.Generation()); err != nil {
		t.Fatalf("matchWithoutSnapshot: %v", err)
	} else if ok {
		t.Fatalf("matchWithoutSnapshot unexpectedly supported mixed fallback plan: %#v", results)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.rete.beta.rules[equalityRule.revisionID] == nil {
		t.Fatal("equality join rule lost beta memory after reset")
	}
	if session.rete.beta.rules[numericRule.revisionID] != nil {
		t.Fatal("numeric join rule unexpectedly got beta memory after reset")
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeMatchWithoutSnapshotMatchesSnapshotForFullBetaMemory(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("beta-no-snapshot-session"),
		WithInitialFacts(mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("full beta session runtime = %#v, want populated beta memory", session.rete)
	}

	snapshot := mustSnapshot(t, ctx, session)
	snapshotResults, err := session.rete.match(ctx, snapshot)
	if err != nil {
		t.Fatalf("snapshot match: %v", err)
	}
	snapshotResults = cloneRuleMatchResults(snapshotResults)
	noSnapshotResults, ok, err := session.rete.matchWithoutSnapshot(ctx, session.Generation())
	if err != nil {
		t.Fatalf("matchWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("matchWithoutSnapshot unexpectedly unavailable for full beta-backed session")
	}
	if !ruleMatchResultsEqual(noSnapshotResults, snapshotResults) {
		t.Fatalf("matchWithoutSnapshot results differ from snapshot match:\nno-snapshot=%#v\nsnapshot=%#v", noSnapshotResults, snapshotResults)
	}
}

func TestReteRuntimeDefaultSessionFallsBackForUnsupportedSmallPlan(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(mark): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "dynamic-event",
		Conditions: []RuleConditionSpec{{Binding: "event", Name: "event"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(dynamic-event): %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "small-unsupported-fallback-session")
	if session.rete == nil {
		t.Fatal("expected default Rete runtime")
	}
	if len(session.rete.plan.unsupported) == 0 {
		t.Fatalf("unsupported plan reasons = %#v, want fallback reason", session.rete.plan.unsupported)
	}
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"kind": "queued"})); err != nil {
		t.Fatalf("Assert(event): %v", err)
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeResetKeepsSmallSupportedMemories(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	rule := revision.rules["employee-department"]
	session, err := NewSession(
		revision,
		WithSessionID("beta-reset-small-session"),
		WithInitialFacts(
			SessionInitialFact{TemplateKey: employeeKey, Fields: mustFields(t, map[string]any{"name": "Initial", "dept": "Engineering"})},
		),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		for i := range reteAlphaMinimumFacts {
			if _, err := session.AssertTemplate(ctx, noiseKey, mustFields(t, map[string]any{"bucket": i})); err != nil {
				t.Fatalf("AssertTemplate noise %d: %v", i, err)
			}
		}
		if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
			t.Fatalf("AssertTemplate department: %v", err)
		}
		if _, err := session.Reset(ctx); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.alpha == nil || session.rete.beta == nil {
		t.Fatalf("small initial Rete runtime = %#v, want populated memories", session.rete)
	}
	betaMemory := session.rete.beta
	betaRuleMemory := betaMemory.rules[rule.revisionID]
	betaConditionMatchesPtr := reflect.ValueOf(betaRuleMemory.conditionMatches[0]).Pointer()
	betaConditionIndexesPtr := reflect.ValueOf(betaRuleMemory.conditionIndexes[0]).Pointer()
	betaPrefixesPtr := reflect.ValueOf(betaRuleMemory.prefixes[0]).Pointer()
	betaPrefixIndexesPtr := reflect.ValueOf(betaRuleMemory.prefixIndexes[0]).Pointer()

	for i := range reteAlphaMinimumFacts {
		if _, err := session.AssertTemplate(ctx, noiseKey, mustFields(t, map[string]any{"bucket": i})); err != nil {
			t.Fatalf("AssertTemplate noise %d: %v", i, err)
		}
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate department: %v", err)
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatal("expected beta memory after crossing threshold")
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.rete == nil || session.rete.alpha == nil || session.rete.beta == nil {
		t.Fatalf("Rete memories after small reset = %#v, want populated memories", session.rete)
	}
	if session.rete.beta != betaMemory {
		t.Fatalf("beta memory pointer changed across reset: got %p want %p", session.rete.beta, betaMemory)
	}
	if session.rete.beta.rules[rule.revisionID] != betaRuleMemory {
		t.Fatalf("beta rule memory pointer changed across reset: got %p want %p", session.rete.beta.rules[rule.revisionID], betaRuleMemory)
	}
	if got := reflect.ValueOf(session.rete.beta.rules[rule.revisionID].conditionMatches[0]).Pointer(); got != betaConditionMatchesPtr {
		t.Fatalf("beta condition matches backing array changed across reset: got %#x want %#x", got, betaConditionMatchesPtr)
	}
	if got := reflect.ValueOf(session.rete.beta.rules[rule.revisionID].conditionIndexes[0]).Pointer(); got != betaConditionIndexesPtr {
		t.Fatalf("beta condition index map changed across reset: got %#x want %#x", got, betaConditionIndexesPtr)
	}
	if got := reflect.ValueOf(session.rete.beta.rules[rule.revisionID].prefixes[0]).Pointer(); got != betaPrefixesPtr {
		t.Fatalf("beta prefix backing array changed across reset: got %#x want %#x", got, betaPrefixesPtr)
	}
	if got := reflect.ValueOf(session.rete.beta.rules[rule.revisionID].prefixIndexes[0]).Pointer(); got != betaPrefixIndexesPtr {
		t.Fatalf("beta prefix index map changed across reset: got %#x want %#x", got, betaPrefixIndexesPtr)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeBetaJoinIndexesReuseBucketBackingAcrossReset(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	rule := revision.rules["employee-department"]
	initials := mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)
	initials = append(initials, SessionInitialFact{
		TemplateKey: employeeKey,
		Fields:      mustFields(t, map[string]any{"name": "Grace", "dept": "Engineering"}),
	})
	session, err := NewSession(
		revision,
		WithSessionID("beta-reset-bucket-session"),
		WithInitialFacts(initials...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.alpha == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	betaRuleMemory := session.rete.beta.rules[rule.revisionID]
	key := betaJoinKey{kind: betaJoinKeyString, stringValue: "Engineering"}
	before := betaRuleMemory.prefixIndexes[0][key]
	if got, want := before.len(), 2; got != want {
		t.Fatalf("prefix bucket len before reset = %d, want %d", got, want)
	}
	if got := len(betaRuleMemory.prefixes[1]); got == 0 {
		t.Fatal("expected joined beta prefixes before reset")
	}
	assertBetaPrefixesUseLinkedTokenMatches(t, betaRuleMemory, 1)
	assertBetaTokenRefsUseArena(t, betaRuleMemory)
	beforeArena := betaRuleMemory.tokenArena
	beforeToken := betaRuleMemory.prefixes[1][0].prefix.token
	beforeRows := betaRuleMemory.tokenArena.rowCount()
	if beforeToken.isZero() {
		t.Fatal("expected non-zero beta token before reset")
	}
	beforeRestPtr := reflect.ValueOf(before.rest).Pointer()
	beforeRestCap := cap(before.rest)

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after reset = %#v, want populated memories", session.rete)
	}
	after := session.rete.beta.rules[rule.revisionID].prefixIndexes[0][key]
	if got, want := after.len(), 2; got != want {
		t.Fatalf("prefix bucket len after reset = %d, want %d", got, want)
	}
	if got := reflect.ValueOf(after.rest).Pointer(); got != beforeRestPtr {
		t.Fatalf("prefix bucket overflow backing array changed across reset: got %#x want %#x", got, beforeRestPtr)
	}
	if got := cap(after.rest); got != beforeRestCap {
		t.Fatalf("prefix bucket overflow capacity changed across reset: got %d want %d", got, beforeRestCap)
	}
	assertBetaPrefixesUseLinkedTokenMatches(t, session.rete.beta.rules[rule.revisionID], 1)
	assertBetaTokenRefsUseArena(t, session.rete.beta.rules[rule.revisionID])
	if got := session.rete.beta.rules[rule.revisionID].tokenArena; got != beforeArena {
		t.Fatalf("token arena changed across reset: got %p want %p", got, beforeArena)
	}
	if _, ok := beforeToken.resolve(); ok {
		t.Fatal("pre-reset token handle resolved after reset")
	}
	if got := session.rete.beta.rules[rule.revisionID].tokenArena.rowCount(); got != beforeRows {
		t.Fatalf("token arena row count after reset = %d, want %d", got, beforeRows)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeTokenArenaTrimsDeadRowsAfterReset(t *testing.T) {
	ctx := context.Background()
	revision, itemKey := mustTokenBackingRuleset(t)
	initials := mustTokenBackingInitialFacts(t, itemKey, 300)
	session, err := NewSession(
		revision,
		WithSessionID("token-backing-reset-session"),
		WithInitialFacts(initials...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	rule := revision.rules["match-item"]
	memory := session.rete.beta.rules[rule.revisionID]
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	beforeRows := memory.tokenArena.rowCount()
	if beforeRows < 256 {
		t.Fatalf("token arena rows before reset = %d, want at least 256", beforeRows)
	}
	beforeToken := memory.prefixes[0][0].prefix.token

	session.initials = session.initials[:1]
	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	memory = session.rete.beta.rules[rule.revisionID]
	afterRows := memory.tokenArena.rowCount()
	if afterRows >= beforeRows {
		t.Fatalf("token arena rows after reset = %d, want fewer than %d", afterRows, beforeRows)
	}
	if afterRows != 1 {
		t.Fatalf("token arena rows after reset = %d, want 1", afterRows)
	}
	if _, ok := beforeToken.resolve(); ok {
		t.Fatal("pre-reset token handle resolved after reset")
	}
	assertBetaTokenRefsUseArena(t, memory)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeTokenArenaRefsSurviveRepeatedRetract(t *testing.T) {
	ctx := context.Background()
	revision, itemKey := mustTokenBackingRuleset(t)
	initials := mustTokenBackingInitialFacts(t, itemKey, 128)
	session, err := NewSession(
		revision,
		WithSessionID("token-backing-retract-session"),
		WithInitialFacts(initials...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	if result, err := session.Run(ctx); err != nil {
		t.Fatalf("initial Run: %v", err)
	} else if result.Status != RunCompleted {
		t.Fatalf("initial run status = %v, want %v", result.Status, RunCompleted)
	}

	rule := revision.rules["match-item"]
	memory := session.rete.beta.rules[rule.revisionID]
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	beforeRows := memory.tokenArena.rowCount()
	if beforeRows < 128 {
		t.Fatalf("token arena rows before retract = %d, want at least 128", beforeRows)
	}
	if _, ok, err := session.rete.beta.currentTerminalTokenDeltas(ctx); err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	} else if !ok {
		t.Fatal("currentTerminalTokenDeltas unexpectedly unavailable")
	}
	if len(session.rete.beta.terminalTokenDeltas) == 0 {
		t.Fatal("terminal token scratch unexpectedly empty before retract")
	}

	ids := make([]FactID, 0, len(initials))
	for _, fact := range mustSnapshot(t, ctx, session).Facts() {
		ids = append(ids, fact.ID())
	}
	for i := range 120 {
		if _, err := session.Retract(ctx, ids[i]); err != nil {
			t.Fatalf("Retract %d: %v", i, err)
		}
	}

	memory = session.rete.beta.rules[rule.revisionID]
	afterRows := memory.tokenArena.rowCount()
	if afterRows != beforeRows {
		t.Fatalf("token arena rows after retract = %d, want stable %d", afterRows, beforeRows)
	}
	if got := len(session.rete.beta.terminalTokenDeltas); got != 0 {
		t.Fatalf("terminal token scratch len after compaction = %d, want 0", got)
	}
	assertBetaTokenRefsUseArena(t, memory)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeBetaJoinLookupReusesScratchAcrossCalls(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	initials := mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)
	session, err := NewSession(
		revision,
		WithSessionID("beta-lookup-scratch-session"),
		WithInitialFacts(initials...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	rule := revision.rules["employee-department"]
	betaRuleMemory := session.rete.beta.rules[rule.revisionID]
	if betaRuleMemory == nil {
		t.Fatal("expected beta rule memory")
	}
	if len(betaRuleMemory.prefixes[0]) == 0 {
		t.Fatal("expected left-side prefixes")
	}

	matches1, err := betaRuleMemory.matchesForLeftPrefix(1, betaRuleMemory.prefixes[0][0].prefix)
	if err != nil {
		t.Fatalf("matchesForLeftPrefix first call: %v", err)
	}
	matches2, err := betaRuleMemory.matchesForLeftPrefix(1, betaRuleMemory.prefixes[0][0].prefix)
	if err != nil {
		t.Fatalf("matchesForLeftPrefix second call: %v", err)
	}
	if got, want := reflect.ValueOf(matches1).Pointer(), reflect.ValueOf(matches2).Pointer(); got != want {
		t.Fatalf("lookup scratch backing changed between calls: got %#x want %#x", got, want)
	}
	if len(matches1) != len(matches2) {
		t.Fatalf("lookup results changed between calls: %d vs %d", len(matches1), len(matches2))
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeTerminalDeltaCandidatesUseIndependentScratchLanes(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
		{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
	})
	session := mustSession(t, revision, "terminal-delta-scratch-session")

	inserted, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{
		"age":    20,
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	before, ok := session.factByID(inserted.Fact.ID())
	if !ok {
		t.Fatalf("fact %q not found before modify", inserted.Fact.ID())
	}

	_, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"age": 21}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if session.rete == nil {
		t.Fatal("expected Rete runtime")
	}

	removed, err := session.rete.candidatesForTerminalDeltas(delta.removed, &session.rete.terminalRemovedScratch)
	if err != nil {
		t.Fatalf("removed candidates: %v", err)
	}
	if got, want := len(removed), 1; got != want {
		t.Fatalf("removed candidate count = %d, want %d", got, want)
	}
	if removed[0].factIDs[0] != before.ID() {
		t.Fatalf("removed candidate fact ID = %q, want %q", removed[0].factIDs[0], before.ID())
	}
	removedPath := append([]int(nil), removed[0].path...)
	removedVersion := removed[0].factVersions[0]

	added, err := session.rete.candidatesForTerminalDeltas(delta.added, &session.rete.terminalAddedScratch)
	if err != nil {
		t.Fatalf("added candidates: %v", err)
	}
	if got, want := len(added), 1; got != want {
		t.Fatalf("added candidate count = %d, want %d", got, want)
	}
	added[0].factIDs[0] = newFactID(9, 9)
	added[0].factVersions[0]++
	if len(added[0].path) > 0 {
		added[0].path[0] = 99
	}

	if removed[0].factIDs[0] != before.ID() {
		t.Fatalf("removed candidate fact ID changed after added conversion: got %q want %q", removed[0].factIDs[0], before.ID())
	}
	if removed[0].factVersions[0] != removedVersion {
		t.Fatalf("removed candidate version changed after added conversion: got %d want %d", removed[0].factVersions[0], removedVersion)
	}
	if !reflect.DeepEqual(removed[0].path, removedPath) {
		t.Fatalf("removed candidate path changed after added conversion: got %#v want %#v", removed[0].path, removedPath)
	}
}

func TestReteRuntimeAgendaActivationsDoNotAliasCandidateScratch(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session, err := NewSession(
		revision,
		WithSessionID("candidate-scratch-activation-session"),
		WithInitialFacts(mustBetaMemoryInitialFacts(t, noiseKey, employeeKey, departmentKey)...),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}

	snapshot := mustSnapshot(t, ctx, session)
	if _, err := session.reconcileAgenda(ctx, snapshot); err != nil {
		t.Fatalf("initial reconcileAgenda: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if len(pending) == 0 {
		t.Fatal("expected pending activation after initial reconcile")
	}
	before := pending[0]
	beforeBindings := cloneBindingTupleEntries(before.bindings)
	beforeFactIDs := cloneFactIDs(before.factIDs)
	beforeVersions := cloneFactVersions(before.factVersions)
	beforePath := cloneIntPath(before.path)

	if _, err := session.AssertTemplate(ctx, noiseKey, mustFields(t, map[string]any{"bucket": 99})); err != nil {
		t.Fatalf("AssertTemplate noise: %v", err)
	}
	if _, err := session.reconcileAgenda(ctx, mustSnapshot(t, ctx, session)); err != nil {
		t.Fatalf("second reconcileAgenda: %v", err)
	}
	after, ok := session.agenda.activationByKey(before.key)
	if !ok {
		t.Fatalf("activation %v disappeared after second reconcile", before.key)
	}
	if !reflect.DeepEqual(after.bindings, beforeBindings) {
		t.Fatalf("activation bindings changed after candidate scratch reuse: got %#v want %#v", after.bindings, beforeBindings)
	}
	if !reflect.DeepEqual(after.factIDs, beforeFactIDs) {
		t.Fatalf("activation fact IDs changed after candidate scratch reuse: got %#v want %#v", after.factIDs, beforeFactIDs)
	}
	if !reflect.DeepEqual(after.factVersions, beforeVersions) {
		t.Fatalf("activation fact versions changed after candidate scratch reuse: got %#v want %#v", after.factVersions, beforeVersions)
	}
	if !reflect.DeepEqual(after.path, beforePath) {
		t.Fatalf("activation path changed after candidate scratch reuse: got %#v want %#v", after.path, beforePath)
	}
}

func TestReteRuntimeBetaJoinTreatsExactIntegralFloatsAsInts(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueFloat, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "int-float-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", TemplateKey: left.Key()},
			{
				Binding:     "right",
				TemplateKey: right.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "bucket"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "beta-int-float-session")

	if _, err := session.AssertTemplate(ctx, left.Key(), mustFields(t, map[string]any{"bucket": 7})); err != nil {
		t.Fatalf("AssertTemplate left: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, right.Key(), mustFields(t, map[string]any{"bucket": 7.0})); err != nil {
		t.Fatalf("AssertTemplate right: %v", err)
	}

	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func TestReteRuntimeRetractKeepsAgendaDeltaPathForSmallSupportedSession(t *testing.T) {
	ctx := context.Background()
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	initials := make([]SessionInitialFact, 0, reteAlphaMinimumFacts)
	for i := range reteAlphaMinimumFacts - 3 {
		initials = append(initials, SessionInitialFact{
			TemplateKey: noiseKey,
			Fields:      mustFields(t, map[string]any{"bucket": i}),
		})
	}
	initials = append(initials,
		SessionInitialFact{TemplateKey: employeeKey, Fields: mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})},
		SessionInitialFact{TemplateKey: departmentKey, Fields: mustFields(t, map[string]any{"id": "Engineering"})},
		SessionInitialFact{TemplateKey: departmentKey, Fields: mustFields(t, map[string]any{"id": "Sales"})},
	)
	session, err := NewSession(revision, WithSessionID("beta-retract-small-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete != nil && session.rete.graphBeta != nil {
		assertGraphBetaRuntimeParity(t, revision, session)
		return
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatal("expected beta memory at threshold")
	}
	if _, err := session.reconcileAgenda(ctx, mustSnapshot(t, ctx, session)); err != nil {
		t.Fatalf("initial reconcileAgenda: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations before retract = %d, want %d", got, want)
	}

	department := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	if _, err := session.Retract(ctx, department.ID()); err != nil {
		t.Fatalf("Retract(Engineering department): %v", err)
	}
	if session.rete == nil || session.rete.alpha == nil || session.rete.beta == nil {
		t.Fatalf("Rete memories after retract = %#v, want populated memories", session.rete)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after retract = %d, want 0", got)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
}

func mustBetaMemoryRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Closed: true,
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "employee",
		Closed: true,
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}, {Name: "dept", Kind: ValueString, Required: true}},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Closed: true,
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", TemplateKey: employee.Key()},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), noise.Key(), employee.Key(), department.Key()
}

func mustBetaMemoryInitialFacts(t testing.TB, noiseKey, employeeKey, departmentKey TemplateKey) []SessionInitialFact {
	t.Helper()

	initials := make([]SessionInitialFact, 0, reteAlphaMinimumFacts+3)
	for i := range reteAlphaMinimumFacts {
		initials = append(initials, SessionInitialFact{
			TemplateKey: noiseKey,
			Fields:      mustFields(t, map[string]any{"bucket": i}),
		})
	}
	initials = append(initials,
		SessionInitialFact{
			TemplateKey: employeeKey,
			Fields:      mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"}),
		},
		SessionInitialFact{
			TemplateKey: departmentKey,
			Fields:      mustFields(t, map[string]any{"id": "Engineering"}),
		},
		SessionInitialFact{
			TemplateKey: departmentKey,
			Fields:      mustFields(t, map[string]any{"id": "Sales"}),
		},
	)
	return initials
}

func mustTokenBackingRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		Closed:          true,
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
		Name: "match-item",
		Conditions: []RuleConditionSpec{
			{Binding: "item", TemplateKey: item.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), item.Key()
}

func mustTokenBackingInitialFacts(t testing.TB, itemKey TemplateKey, count int) []SessionInitialFact {
	t.Helper()

	initials := make([]SessionInitialFact, 0, count)
	for i := range count {
		initials = append(initials, SessionInitialFact{
			TemplateKey: itemKey,
			Fields: mustFields(t, map[string]any{
				"id": fmt.Sprintf("item-%03d", i),
			}),
		})
	}
	return initials
}

func mustSessionFactByTemplateAndField(t *testing.T, session *Session, templateKey TemplateKey, field string, want any) FactSnapshot {
	t.Helper()

	snapshot := mustSnapshot(t, context.Background(), session)
	expected := mustValue(t, want)
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() != templateKey {
			continue
		}
		got, ok := fact.Field(field)
		if !ok {
			continue
		}
		if got.Equal(expected) {
			return fact
		}
	}
	t.Fatalf("fact not found for template %q field %q = %v", templateKey, field, want)
	return FactSnapshot{}
}

func mustAlphaMemoryRuleset(t testing.TB, ruleName string, constraints []FieldConstraintSpec) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
		Closed:          true,
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: ruleName,
		Conditions: []RuleConditionSpec{
			{
				Binding:          "person",
				TemplateKey:      person.Key(),
				FieldConstraints: constraints,
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), person.Key()
}

func assertAlphaMemoryCount(t testing.TB, session *Session, ruleName string, want int) {
	t.Helper()
	if session == nil || session.rete == nil {
		t.Fatal("session has no Rete runtime")
	}
	rule, ok := session.revision.rules[ruleName]
	if !ok {
		t.Fatalf("rule %q not found", ruleName)
	}
	if len(rule.conditionPlans) == 0 {
		t.Fatalf("rule %q has no conditions", ruleName)
	}
	conditionID := rule.conditionPlans[0].id
	if got := session.rete.alphaFactCount(conditionID); got != want {
		t.Fatalf("alpha fact count for %s = %d, want %d", ruleName, got, want)
	}
}

func assertAlphaMemoryFillerFacts(t testing.TB, session *Session, templateKey TemplateKey, count int) {
	t.Helper()
	for i := range count {
		if _, err := session.AssertTemplate(context.Background(), templateKey, mustFields(t, map[string]any{"age": 15, "status": "inactive"})); err != nil {
			t.Fatalf("AssertTemplate filler %d: %v", i, err)
		}
	}
}

func assertAlphaMemoryGeneration(t testing.TB, session *Session, ruleName string, want Generation) {
	t.Helper()
	if session == nil || session.rete == nil || session.rete.alpha == nil {
		t.Fatal("session has no alpha memory")
	}
	rule, ok := session.revision.rules[ruleName]
	if !ok {
		t.Fatalf("rule %q not found", ruleName)
	}
	conditionID := rule.conditionPlans[0].id
	facts, ok := session.rete.alpha.factsForCondition(conditionID)
	if !ok {
		t.Fatalf("alpha facts for %s unavailable", ruleName)
	}
	for i, fact := range facts {
		if fact.Generation() != want {
			t.Fatalf("alpha fact %d generation = %d, want %d", i, fact.Generation(), want)
		}
	}
}

func sliceDataPtr[T any](slice []T) uintptr {
	if len(slice) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&slice[0]))
}

func assertBetaPrefixesUseLinkedTokenMatches(t testing.TB, memory *reteBetaRuleMemory, conditionIndex int) {
	t.Helper()
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	if conditionIndex < 0 || conditionIndex >= len(memory.prefixes) {
		t.Fatalf("condition index %d out of range", conditionIndex)
	}
	for i, row := range memory.prefixes[conditionIndex] {
		prefix := row.prefix
		if prefix.token.isZero() {
			t.Fatalf("prefix %d has nil token", i)
		}
		if got, want := prefix.token.size(), conditionIndex+1; got != want {
			t.Fatalf("prefix %d token size = %d, want %d", i, got, want)
		}
		for token := prefix.token; !token.isZero(); token = token.parent() {
			row, ok := token.resolve()
			if !ok {
				t.Fatalf("prefix %d token could not resolve", i)
			}
			if row.match.bindingSlot < 0 || row.match.bindingSlot >= len(memory.rule.conditionPlans) {
				t.Fatalf("prefix %d token binding slot = %d, want valid condition slot", i, row.match.bindingSlot)
			}
			if row.match.conditionID != memory.rule.conditionPlans[row.match.bindingSlot].id {
				t.Fatalf("prefix %d token condition = %q, want %q", i, row.match.conditionID, memory.rule.conditionPlans[row.match.bindingSlot].id)
			}
		}
	}
}

func assertBetaTokenRefsUseArena(t testing.TB, memory *reteBetaRuleMemory) {
	t.Helper()
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	if memory.tokenArena == nil {
		t.Fatal("beta token arena is nil")
	}
	for conditionIndex, rows := range memory.prefixes {
		for i, row := range rows {
			prefix := row.prefix
			if prefix.token.isZero() {
				t.Fatalf("prefix %d for condition %d has nil token", i, conditionIndex)
			}
			resolved, ok := memory.tokenArena.resolve(prefix.token.handle)
			if !ok {
				t.Fatalf("prefix %d for condition %d token failed arena resolution", i, conditionIndex)
			}
			match, ok := prefix.token.matchAt(prefix.token.size() - 1)
			if !ok {
				t.Fatalf("prefix %d for condition %d token slot failed arena resolution", i, conditionIndex)
			}
			if resolved.match.fact.ID() != match.fact.ID() {
				t.Fatalf("prefix %d for condition %d token row mismatch", i, conditionIndex)
			}
		}
	}
}

func findBetaPrefixRowByToken(t testing.TB, prefixes []betaPrefixRow, token tokenRef) betaPrefixRow {
	t.Helper()
	for _, prefix := range prefixes {
		if tokenRefEqual(prefix.prefix.token, token) {
			return prefix
		}
	}
	t.Fatalf("did not find beta prefix for token %#v", token)
	return betaPrefixRow{}
}

func conditionBucketContainsFact(memory *reteBetaRuleMemory, conditionIndex int, key betaJoinKey, id FactID) bool {
	if memory == nil || conditionIndex < 0 || conditionIndex >= len(memory.conditionMatches) {
		return false
	}
	found := false
	memory.conditionIndexes[conditionIndex][key].forEach(func(rowID betaConditionMatchRowID) bool {
		row := memory.conditionMatchRow(conditionIndex, rowID)
		if row != nil && row.match.fact.ID() == id {
			found = true
			return false
		}
		return true
	})
	return found
}

func conditionIndexBucketIDs(bucket betaConditionMatchIndexBucket) []betaConditionMatchRowID {
	out := make([]betaConditionMatchRowID, 0, bucket.len())
	bucket.forEach(func(rowID betaConditionMatchRowID) bool {
		out = append(out, rowID)
		return true
	})
	return out
}

func prefixIndexBucketIDs(bucket betaPrefixIndexBucket) []betaPrefixRowID {
	out := make([]betaPrefixRowID, 0, bucket.len())
	bucket.forEach(func(rowID betaPrefixRowID) bool {
		out = append(out, rowID)
		return true
	})
	return out
}

func conditionMatchesContainFact(matches []conditionMatch, id FactID) bool {
	for _, match := range matches {
		if match.fact.ID() == id {
			return true
		}
	}
	return false
}

func assertMatcherParity(t testing.TB, revision *Ruleset, snapshot Snapshot, oracle matcher, candidate matcher) {
	t.Helper()

	oracleResults, err := oracle.match(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("oracle match: %v", err)
	}
	candidateResults, err := candidate.match(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("candidate match: %v", err)
	}

	assertRuleMatchResultsEqual(t, "candidate", candidateResults, "oracle", oracleResults)

	oracleOrder := agendaOrderForResults(t, revision, oracleResults)
	candidateOrder := agendaOrderForResults(t, revision, candidateResults)
	if !reflect.DeepEqual(candidateOrder, oracleOrder) {
		t.Fatalf("candidate agenda order differs from oracle:\ncandidate=%#v\noracle=%#v", candidateOrder, oracleOrder)
	}
}

func assertRuleMatchResultsEqual(t testing.TB, leftName string, left []ruleMatchResult, rightName string, right []ruleMatchResult) {
	t.Helper()

	if len(left) != len(right) {
		t.Fatalf("%s result count = %d, %s result count = %d", leftName, len(left), rightName, len(right))
	}
	for i := range left {
		leftResult := left[i]
		rightResult := right[i]
		if leftResult.ruleID != rightResult.ruleID ||
			leftResult.ruleRevisionID != rightResult.ruleRevisionID ||
			leftResult.salience != rightResult.salience ||
			leftResult.declarationOrder != rightResult.declarationOrder {
			t.Fatalf("result %d metadata differs:\n%s=%#v\n%s=%#v", i, leftName, leftResult, rightName, rightResult)
		}
		if len(leftResult.candidates) != len(rightResult.candidates) {
			t.Fatalf("result %d candidate count differs: %s=%d %s=%d", i, leftName, len(leftResult.candidates), rightName, len(rightResult.candidates))
		}
		for j := range leftResult.candidates {
			assertMatchCandidateEqual(t, i, j, leftName, leftResult.candidates[j], rightName, rightResult.candidates[j])
		}
	}
}

func assertMatchCandidateEqual(t testing.TB, resultIndex, candidateIndex int, leftName string, left matchCandidate, rightName string, right matchCandidate) {
	t.Helper()

	if left.ruleID != right.ruleID ||
		left.ruleRevisionID != right.ruleRevisionID ||
		left.identity != right.identity ||
		left.generation != right.generation ||
		left.maxRecency != right.maxRecency ||
		left.aggregateRecency != right.aggregateRecency {
		t.Fatalf("candidate %d/%d metadata differs:\n%s=%#v\n%s=%#v", resultIndex, candidateIndex, leftName, left, rightName, right)
	}
	if !reflect.DeepEqual(left.bindingTuple, right.bindingTuple) {
		t.Fatalf("candidate %d/%d binding tuple differs:\n%s=%#v\n%s=%#v", resultIndex, candidateIndex, leftName, left.bindingTuple, rightName, right.bindingTuple)
	}
	if !reflect.DeepEqual(left.factIDs, right.factIDs) {
		t.Fatalf("candidate %d/%d fact IDs differ:\n%s=%#v\n%s=%#v", resultIndex, candidateIndex, leftName, left.factIDs, rightName, right.factIDs)
	}
	if !reflect.DeepEqual(left.factVersions, right.factVersions) {
		t.Fatalf("candidate %d/%d fact versions differ:\n%s=%#v\n%s=%#v", resultIndex, candidateIndex, leftName, left.factVersions, rightName, right.factVersions)
	}
	if !reflect.DeepEqual(left.path, right.path) {
		t.Fatalf("candidate %d/%d path differs:\n%s=%#v\n%s=%#v", resultIndex, candidateIndex, leftName, left.path, rightName, right.path)
	}
}

func agendaOrderForResults(t testing.TB, revision *Ruleset, results []ruleMatchResult) []activationParityRecord {
	t.Helper()

	agenda := newAgenda()
	if _, err := agenda.reconcile(context.Background(), revision, results); err != nil {
		t.Fatalf("agenda reconcile: %v", err)
	}
	activations := agenda.pendingActivations()
	records := make([]activationParityRecord, len(activations))
	for i, activation := range activations {
		records[i] = activationParityRecord{
			id:               activation.id,
			ruleID:           activation.ruleID,
			ruleRevisionID:   activation.ruleRevisionID,
			generation:       activation.generation,
			identity:         activation.identity,
			bindings:         activation.bindings,
			factIDs:          activation.factIDs,
			factVersions:     activation.factVersions,
			path:             activation.path,
			maxRecency:       activation.maxRecency,
			aggregateRecency: activation.aggregateRecency,
			declarationOrder: activation.declarationOrder,
			salience:         activation.salience,
		}
	}
	return records
}

func assertSessionAgendaMatchesFullReteReconcile(t *testing.T, session *Session) {
	t.Helper()
	if session == nil || session.rete == nil {
		t.Fatal("session has no Rete runtime")
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	results, err := session.rete.match(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("Rete match: %v", err)
	}
	oracle := newAgenda()
	if _, err := oracle.reconcile(context.Background(), session.revision, results); err != nil {
		t.Fatalf("oracle reconcile: %v", err)
	}
	got := activationParityRecordsFromActivations(session.agenda.pendingActivations())
	want := activationParityRecordsFromActivations(oracle.pendingActivations())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental agenda differs from full reconcile:\nincremental=%#v\nfull=%#v", got, want)
	}
}

func assertTerminalTokenDeltaMatchesCandidateDelta(t *testing.T, revision *Ruleset, session *Session, candidateAgenda, directAgenda *agenda, delta reteAgendaDelta) []agendaChange {
	t.Helper()
	removed, err := session.rete.candidatesForTerminalDeltas(delta.removed, &session.rete.terminalRemovedScratch)
	if err != nil {
		t.Fatalf("removed candidates: %v", err)
	}
	added, err := session.rete.candidatesForTerminalDeltas(delta.added, &session.rete.terminalAddedScratch)
	if err != nil {
		t.Fatalf("added candidates: %v", err)
	}
	candidateChanges, err := candidateAgenda.applyCandidateDeltas(context.Background(), revision, removed, added)
	if err != nil {
		t.Fatalf("applyCandidateDeltas: %v", err)
	}
	directChanges, err := directAgenda.applyTerminalTokenDeltas(context.Background(), revision, cloneTerminalTokenDeltas(delta.removed), cloneTerminalTokenDeltas(delta.added))
	if err != nil {
		t.Fatalf("applyTerminalTokenDeltas: %v", err)
	}
	if !agendaChangesPublicEqual(directAgenda, directChanges, candidateAgenda, candidateChanges) {
		t.Fatalf("direct terminal changes differ from candidate changes:\ndirect=%#v\ncandidate=%#v", directChanges, candidateChanges)
	}
	got := activationParityRecordsFromActivations(directAgenda.pendingActivations())
	want := activationParityRecordsFromActivations(candidateAgenda.pendingActivations())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("direct terminal pending differs from candidate pending:\ndirect=%#v\ncandidate=%#v", got, want)
	}
	return directChanges
}

func cloneTerminalTokenDeltas(deltas []reteTerminalTokenDelta) []reteTerminalTokenDelta {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]reteTerminalTokenDelta, len(deltas))
	copy(out, deltas)
	return out
}

func assertReteRuntimeMatchWithoutSnapshotParity(t *testing.T, session *Session) {
	t.Helper()
	if session == nil || session.rete == nil {
		t.Fatal("session has no Rete runtime")
	}

	ctx := context.Background()
	snapshot := mustSnapshot(t, ctx, session)
	snapshotResults, err := session.rete.match(ctx, snapshot)
	if err != nil {
		t.Fatalf("snapshot match: %v", err)
	}
	snapshotResults = cloneRuleMatchResults(snapshotResults)
	noSnapshotResults, ok, err := session.rete.matchWithoutSnapshot(ctx, session.Generation())
	if err != nil {
		t.Fatalf("matchWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("matchWithoutSnapshot unexpectedly unavailable for full beta-backed session")
	}
	if !ruleMatchResultsEqual(noSnapshotResults, snapshotResults) {
		t.Fatalf("matchWithoutSnapshot results differ from snapshot match:\nno-snapshot=%#v\nsnapshot=%#v", noSnapshotResults, snapshotResults)
	}
}

func assertGraphBetaRuntimeParity(t *testing.T, revision *Ruleset, session *Session) {
	t.Helper()
	if session == nil || session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta memory", session.rete)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, context.Background(), session), newNaiveMatcher(revision), session.rete)
	assertReteRuntimeMatchWithoutSnapshotParity(t, session)
}

func ruleMatchResultsEqual(left, right []ruleMatchResult) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ruleID != right[i].ruleID ||
			left[i].ruleRevisionID != right[i].ruleRevisionID ||
			left[i].salience != right[i].salience ||
			left[i].declarationOrder != right[i].declarationOrder {
			return false
		}
		if len(left[i].candidates) != len(right[i].candidates) {
			return false
		}
		for j := range left[i].candidates {
			if !reflect.DeepEqual(left[i].candidates[j], right[i].candidates[j]) {
				return false
			}
		}
	}
	return true
}

func cloneRuleMatchResults(results []ruleMatchResult) []ruleMatchResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]ruleMatchResult, len(results))
	for i, result := range results {
		out[i] = result
		if len(result.candidates) == 0 {
			continue
		}
		out[i].candidates = make([]matchCandidate, len(result.candidates))
		for j, candidate := range result.candidates {
			out[i].candidates[j] = candidate
			out[i].candidates[j].bindingTuple = cloneBindingTupleEntries(candidate.bindingTuple)
			out[i].candidates[j].factIDs = cloneFactIDs(candidate.factIDs)
			out[i].candidates[j].factVersions = cloneFactVersions(candidate.factVersions)
			out[i].candidates[j].path = cloneIntPath(candidate.path)
		}
	}
	return out
}

func activationParityRecordsFromActivations(activations []activation) []activationParityRecord {
	records := make([]activationParityRecord, len(activations))
	for i, activation := range activations {
		records[i] = activationParityRecord{
			id:               activation.id,
			ruleID:           activation.ruleID,
			ruleRevisionID:   activation.ruleRevisionID,
			generation:       activation.generation,
			identity:         activation.identity,
			bindings:         activation.bindings,
			factIDs:          activation.factIDs,
			factVersions:     activation.factVersions,
			path:             activation.path,
			maxRecency:       activation.maxRecency,
			aggregateRecency: activation.aggregateRecency,
			declarationOrder: activation.declarationOrder,
			salience:         activation.salience,
		}
	}
	return records
}

type activationParityRecord struct {
	id               ActivationID
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	generation       Generation
	identity         candidateIdentity
	bindings         []bindingTupleEntry
	factIDs          []FactID
	factVersions     []FactVersion
	path             []int
	maxRecency       Recency
	aggregateRecency Recency
	declarationOrder int
	salience         int
}
