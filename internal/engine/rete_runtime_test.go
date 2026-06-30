package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const reteMemoryMinimumFacts = 32

func TestReteNetworkPlanDescribesDeclaredTemplateRules(t *testing.T) {
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
	if !runtime.plan.incrementalAgendaSupported {
		t.Fatal("incremental agenda plan flag = false, want true for supported declared-template rules")
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

func TestReteRuntimeUnsupportedPlanFailsExplicitly(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
	})
	session := mustSession(t, revision, "unsupported-runtime-match-session")
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	snapshot := mustSnapshot(t, ctx, session)

	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}
	injectUnsupportedRuntimePlan(t, runtime, "adult-active")

	_, err = runtime.match(ctx, snapshot)
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("match error = %v, want ErrUnsupportedRuntime", err)
	}
	for _, want := range []string{"unsupported runtime", "missing-target", `rule="adult-active"`, `binding="person"`, `detail="test unsupported graph plan"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("match error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestSessionRunFailsWhenGraphRuntimeUnsupported(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
	})
	session := mustSession(t, revision, "unsupported-runtime-run-session")
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	injectUnsupportedRuntimePlan(t, session.rete, "adult-active")
	session.agendaReady = false
	session.agendaDirty = true

	result, err := session.Run(ctx)
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("Run error = %v, want ErrUnsupportedRuntime", err)
	}
	if result.Status != RunFailed {
		t.Fatalf("Run status = %s, want %s", result.Status, RunFailed)
	}
}

func TestProductionGraphFlowsDoNotRequestOracleStyleMatching(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustRuntimeGuardRuleset(t)
	session, err := NewSession(revision, WithSessionID("production-purity-guards"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("initial reconcileAgendaInternal: %v", err)
	}
	before := session.propagationCounterSnapshot().Totals

	asserted, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{
		"age":    32,
		"dept":   "engineering",
		"id":     "p1",
		"note":   "seed",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if asserted.Fact.ID().IsZero() {
		t.Fatal("AssertTemplate returned zero fact ID")
	}
	if _, err := session.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"}); err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if _, err := session.Modify(ctx, asserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"note": "updated"})}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := session.Retract(ctx, asserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	after := session.propagationCounterSnapshot().Totals
	assertNoOracleStyleMatchingSince(t, before, after)
}

func TestProductionLogicalSupportFlowDoesNotRequestOracleStyleMatching(t *testing.T) {
	ctx := context.Background()
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session, err := NewSession(revision, WithSessionID("production-logical-purity-guards"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("initial reconcileAgendaInternal: %v", err)
	}
	before := session.propagationCounterSnapshot().Totals

	asserted, err := session.AssertTemplate(ctx, sourceKey, mustFields(t, map[string]any{"id": "s1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := session.Retract(ctx, asserted.Fact.ID()); err != nil {
		t.Fatalf("Retract(source): %v", err)
	}

	after := session.propagationCounterSnapshot().Totals
	assertNoOracleStyleMatchingSince(t, before, after)
}

func TestProductionMutationsFailWhenGraphRuntimeUnsupported(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
	})

	t.Run("assert", func(t *testing.T) {
		session := mustSession(t, revision, "unsupported-runtime-assert-session")
		injectUnsupportedRuntimePlan(t, session.rete, "adult-active")
		_, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"}))
		assertUnsupportedRuntimeDetail(t, err)
	})

	t.Run("retract", func(t *testing.T) {
		session := mustSession(t, revision, "unsupported-runtime-retract-session")
		asserted, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"}))
		if err != nil {
			t.Fatalf("AssertTemplate setup: %v", err)
		}
		injectUnsupportedRuntimePlan(t, session.rete, "adult-active")
		_, err = session.Retract(ctx, asserted.Fact.ID())
		assertUnsupportedRuntimeDetail(t, err)
	})

	t.Run("modify", func(t *testing.T) {
		session := mustSession(t, revision, "unsupported-runtime-modify-session")
		asserted, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"}))
		if err != nil {
			t.Fatalf("AssertTemplate setup: %v", err)
		}
		injectUnsupportedRuntimePlan(t, session.rete, "adult-active")
		_, err = session.Modify(ctx, asserted.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"status": "inactive"})})
		assertUnsupportedRuntimeDetail(t, err)
	})
}

func TestSessionReconcileAgendaRequiresReteRuntime(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustAlphaMemoryRuleset(t, "adult-active", []FieldConstraintSpec{
		{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
	})
	session := mustSession(t, revision, "missing-runtime-session")
	if _, err := session.AssertTemplate(ctx, templateKey, mustFields(t, map[string]any{"age": 20, "status": "active"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	session.rete = nil

	if _, err := session.reconcileAgenda(ctx, session); !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("reconcileAgenda error = %v, want ErrUnsupportedRuntime", err)
	}
}

func TestReteRuntimeRoutesSharedDeclaredTemplateAlphaOnce(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
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
			Binding: "person",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-b",
		Conditions: []RuleConditionSpec{{
			Binding: "p",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			}, Target: TemplateKeyFact(person.Key()),
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

func TestReteRuntimeRoutesSharedDeclaredTemplateAlphaOnceForGeneratedFacts(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
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
			Binding: "person",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-b",
		Conditions: []RuleConditionSpec{{
			Binding: "p",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
			}, Target: TemplateKeyFact(person.Key()),
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
	if _, _, _, _, _, err := session.insertTemplateValuesImmediate(ctx, person.Key(), []Value{mustValue(t, 20)}, origin); err != nil {
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

func TestReteRuntimePlansNameTargetsAsGraphRoutes(t *testing.T) {
	workspace := NewWorkspace()
	eventTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "kind", Kind: ValueString}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "name-target",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact("event")}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "template-target",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(eventTemplate.Key())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if got := len(runtime.plan.unsupported); got != 0 {
		t.Fatalf("unsupported reasons = %#v, want none", runtime.plan.unsupported)
	}
	if got := runtime.plan.stats.unsupportedRules; got != 0 {
		t.Fatalf("unsupported rules = %d, want 0", got)
	}
	if got := runtime.plan.stats.unsupportedConditions; got != 0 {
		t.Fatalf("unsupported conditions = %d, want 0", got)
	}
	if !runtime.plan.incrementalAgendaSupported {
		t.Fatal("incremental agenda plan flag = false, want true")
	}
	if len(revision.graph.routesByName["event"]) != 1 {
		t.Fatalf("name routes = %#v, want one event route", revision.graph.routesByName)
	}
	if len(revision.graph.routesByTemplateKey[eventTemplate.Key()]) != 1 {
		t.Fatalf("template routes = %#v, want one event route", revision.graph.routesByTemplateKey)
	}
}

func BenchmarkReteRuntimeSupportsIncrementalAgendaPlanFlag(b *testing.B) {
	revision := mustCompileLargeSteadyStateScalingRuleset(b, largeSteadyStateScalingCase{streams: 16, limit: 256})
	session := mustSeedLargeSteadyStateScalingSession(b, revision, largeSteadyStateScalingCase{streams: 16, limit: 256})
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		b.Fatal("large steady-state runtime does not support incremental agenda")
	}

	b.ReportMetric(float64(len(session.rete.plan.rules)), "rules")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !session.rete.supportsIncrementalAgenda() {
			b.Fatal("incremental agenda support changed")
		}
	}
}

func TestReteRuntimeRoutesDeclaredTemplateSubscribersByTemplateKey(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "left",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "right",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	extra := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "extra",
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
				Binding: "left",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 1},
				}, Target: TemplateKeyFact(left.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "left-b",
		Conditions: []RuleConditionSpec{
			{
				Binding: "left",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 2},
				}, Target: TemplateKeyFact(left.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "right-a",
		Conditions: []RuleConditionSpec{
			{
				Binding: "right",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 1},
				}, Target: TemplateKeyFact(right.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "extra-a",
		Conditions: []RuleConditionSpec{
			{
				Binding: "extra",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Value: 1},
				}, Target: TemplateKeyFact(extra.Key()),
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
	if got, want := snapshot.Totals.AlphaMatchesAdded, 1; got != want {
		t.Fatalf("alpha matches added = %d, want %d", got, want)
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
	if got, want := snapshot.Totals.AlphaMatchesAdded, 1; got != want {
		t.Fatalf("public alpha matches added = %d, want %d", got, want)
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
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "right",
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
				Binding: "left", Target: TemplateKeyFact(left.Key()),
			},
			{
				Binding: "right", Target: TemplateKeyFact(right.Key()),
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

func TestSessionReconcileAgendaInternalSupportsNameAndTemplateTargets(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	eventTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "kind", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "by-name",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact("event")}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "by-template",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(eventTemplate.Key())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	initials := []SessionInitialFact{
		{TemplateKey: eventTemplate.Key(), Fields: mustFields(t, map[string]any{"kind": "alpha"})},
		{TemplateKey: eventTemplate.Key(), Fields: mustFields(t, map[string]any{"kind": "beta"})},
	}
	sessionInternal, err := NewSession(revision, WithSessionID("name-template-source-internal"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(internal): %v", err)
	}
	sessionSnapshot, err := NewSession(revision, WithSessionID("name-template-source-snapshot"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(snapshot): %v", err)
	}

	if _, err := sessionSnapshot.reconcileAgenda(ctx, mustSnapshot(t, ctx, sessionSnapshot)); err != nil {
		t.Fatalf("snapshot reconcileAgenda: %v", err)
	}
	if _, err := sessionInternal.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
}

func TestSessionReconcileAgendaWithoutSnapshotUsesTerminalTokensForBetaPlans(t *testing.T) {
	ctx := context.Background()
	revision := mustCompileLoanUnderwritingRuleset(t, nil)
	initials := loanUnderwritingTemplateInitialFacts(t)

	terminalSession, err := NewSession(revision, WithEventListener(&testEventCollector{}), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession(terminal): %v", err)
	}
	terminalSession.attachPropagationCounters()
	snapshotSession, err := NewSession(revision, WithEventListener(&testEventCollector{}), WithInitialFacts(initials...))
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

	counters := terminalSession.propagationCounterSnapshot().Totals
	if got, want := counters.WholeTerminalScans, 1; got != want {
		t.Fatalf("whole terminal scans = %d, want %d", got, want)
	}
	if got, want := counters.InitialWholeTerminalScans, 1; got != want {
		t.Fatalf("initial whole terminal scans = %d, want %d", got, want)
	}
	if got := counters.SteadyStateWholeTerminalScans; got != 0 {
		t.Fatalf("steady-state whole terminal scans = %d, want 0", got)
	}
	if got := counters.FullAgendaReconciles; got != 0 {
		t.Fatalf("full agenda reconciles = %d, want 0 for terminal token reconcile", got)
	}
}

func TestSessionReconcileAgendaWithoutSnapshotUsesGraphTerminalRowsWhenTerminalDeltasUnavailable(t *testing.T) {
	ctx := context.Background()
	revision := mustCompileLoanUnderwritingRuleset(t, nil)
	session, err := NewSession(revision, WithEventListener(&testEventCollector{}), WithInitialFacts(loanUnderwritingTemplateInitialFacts(t)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatal("session runtime is not graph beta-backed")
	}
	session.attachPropagationCounters()
	session.rete.plan.incrementalAgendaSupported = false

	changes, ok, err := session.reconcileAgendaWithoutSnapshot(ctx)
	if err != nil {
		t.Fatalf("reconcileAgendaWithoutSnapshot: %v", err)
	}
	if !ok {
		t.Fatalf("reconcileAgendaWithoutSnapshot unavailable, want graph terminal row reconcile")
	}
	if len(changes) == 0 {
		t.Fatal("graph terminal row reconcile produced no agenda changes")
	}
	if !session.agendaReady || session.agendaDirty {
		t.Fatalf("agenda state = ready %t dirty %t, want ready and clean", session.agendaReady, session.agendaDirty)
	}

	counters := session.propagationCounterSnapshot().Totals
	if got, want := counters.FullAgendaReconciles, 0; got != want {
		t.Fatalf("full agenda reconciles = %d, want %d", got, want)
	}
	if got, want := counters.InitialAgendaReconciles, 0; got != want {
		t.Fatalf("initial agenda reconciles = %d, want %d", got, want)
	}
	if got := counters.SteadyStateAgendaReconciles; got != 0 {
		t.Fatalf("steady-state agenda reconciles = %d, want 0", got)
	}
	if got, want := counters.OracleStyleMatchRequests, 0; got != want {
		t.Fatalf("oracle-style match requests = %d, want %d", got, want)
	}
	if got, want := counters.InitialOracleStyleMatchRequests, 0; got != want {
		t.Fatalf("initial oracle-style match requests = %d, want %d", got, want)
	}
	if got, want := counters.WholeTerminalScans, 1; got != want {
		t.Fatalf("whole terminal scans = %d, want %d", got, want)
	}
}

func TestSessionSteadyStateUnsupportedAgendaDeltaFailsWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: templateKey,
		Fields: mustFields(t, map[string]any{
			"age":    32,
			"note":   "old",
			"status": "active",
		}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	if _, ok, err := session.reconcileAgendaWithoutSnapshot(ctx); err != nil {
		t.Fatalf("initial reconcileAgendaWithoutSnapshot: %v", err)
	} else if !ok {
		t.Fatal("initial graph terminal reconcile unavailable")
	}
	before := session.propagationCounterSnapshot().Totals
	if !session.agendaReady || session.agendaDirty {
		t.Fatalf("agenda state before unsupported delta = ready %v dirty %v, want clean ready", session.agendaReady, session.agendaDirty)
	}

	if _, err := session.reconcileAgendaAfterMutation(ctx, reteAgendaDelta{}); !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("reconcileAgendaAfterMutation error = %v, want ErrUnsupportedRuntime", err)
	}
	after := session.propagationCounterSnapshot().Totals
	if got, want := after.UnsupportedAgendaDeltas-before.UnsupportedAgendaDeltas, 1; got != want {
		t.Fatalf("unsupported agenda deltas = +%d, want +%d", got, want)
	}
	if got, want := after.FullAgendaReconciles, before.FullAgendaReconciles; got != want {
		t.Fatalf("full agenda reconciles = %d, want unchanged %d", got, want)
	}
	if got, want := after.WholeTerminalScans, before.WholeTerminalScans; got != want {
		t.Fatalf("whole terminal scans = %d, want unchanged %d", got, want)
	}
	if !session.agendaReady || session.agendaDirty {
		t.Fatalf("agenda state after unsupported delta = ready %v dirty %v, want clean ready", session.agendaReady, session.agendaDirty)
	}
}

func TestSessionRunUnsupportedAgendaDeltaFailsWithoutDirtyAgenda(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: templateKey,
		Fields: mustFields(t, map[string]any{
			"age":    32,
			"note":   "old",
			"status": "active",
		}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	if _, ok, err := session.reconcileAgendaWithoutSnapshot(ctx); err != nil {
		t.Fatalf("initial reconcileAgendaWithoutSnapshot: %v", err)
	} else if !ok {
		t.Fatal("initial graph terminal reconcile unavailable")
	}
	before := session.propagationCounterSnapshot().Totals

	if err := session.recordRunAgendaDelta(reteAgendaDelta{}); !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("recordRunAgendaDelta error = %v, want ErrUnsupportedRuntime", err)
	}
	after := session.propagationCounterSnapshot().Totals
	if got, want := after.UnsupportedAgendaDeltas-before.UnsupportedAgendaDeltas, 1; got != want {
		t.Fatalf("unsupported agenda deltas = +%d, want +%d", got, want)
	}
	if session.runAgendaPending {
		t.Fatal("unsupported run delta left pending run agenda state")
	}
	if !session.agendaReady || session.agendaDirty {
		t.Fatalf("agenda state after unsupported run delta = ready %v dirty %v, want clean ready", session.agendaReady, session.agendaDirty)
	}
}

func TestPropagationPurityDiagnosticsRecordUnsupportedDeltaAndResetRebuild(t *testing.T) {
	ctx := context.Background()
	revision := mustCompileLoanUnderwritingRuleset(t, nil)
	session, err := NewSession(revision, WithInitialFacts(loanUnderwritingTemplateInitialFacts(t)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()

	if _, ok, err := session.applyReteAgendaDelta(ctx, reteAgendaDelta{}); err != nil {
		t.Fatalf("applyReteAgendaDelta unsupported: %v", err)
	} else if ok {
		t.Fatal("applyReteAgendaDelta unsupported returned ok, want unavailable")
	}
	counters := session.propagationCounterSnapshot().Totals
	if got, want := counters.UnsupportedAgendaDeltas, 1; got != want {
		t.Fatalf("unsupported agenda deltas = %d, want %d", got, want)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	counters = session.propagationCounterSnapshot().Totals
	if got, want := counters.GraphRebuilds, 1; got != want {
		t.Fatalf("graph rebuilds = %d, want %d", got, want)
	}
	if got, want := counters.InitialGraphRebuilds, 1; got != want {
		t.Fatalf("initial graph rebuilds = %d, want %d", got, want)
	}
	if got := counters.SteadyStateGraphRebuilds; got != 0 {
		t.Fatalf("steady-state graph rebuilds = %d, want 0", got)
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
	if session.rete == nil || !session.rete.usesGraphBeta() || session.rete.graphBeta == nil {
		t.Fatalf("session Rete runtime = %#v, want graph beta mode", session.rete)
	}
	snapshot := mustSnapshot(t, ctx, session)
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}

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
	assertAlphaMemoryFillerFacts(t, session, templateKey, reteMemoryMinimumFacts-3)

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
	initials := []SessionInitialFact{
		{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 18, "status": "active"})},
		{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 22, "status": "active"})},
		{TemplateKey: templateKey, Fields: mustFields(t, map[string]any{"age": 16, "status": "active"})},
	}
	for i := len(initials); i < reteMemoryMinimumFacts; i++ {
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
	graphBetaMemory := session.rete.graphBeta
	if graphBetaMemory == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta memory", session.rete)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.rete.graphBeta != graphBetaMemory {
		t.Fatalf("graph beta memory pointer changed across reset: got %p want %p", session.rete.graphBeta, graphBetaMemory)
	}
	assertAlphaMemoryCount(t, session, "adult-active", 2)
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
	assertAlphaMemoryFillerFacts(t, session, templateKey, reteMemoryMinimumFacts-2)
	assertAlphaMemoryCount(t, session, "adult-active", 1)

	if _, err := session.ApplyRuleset(ctx, revision2); err != nil {
		t.Fatalf("ApplyRuleset: %v", err)
	}
	assertAlphaMemoryCount(t, session, "young-active", 1)
	assertMatcherParity(t, revision2, mustSnapshot(t, ctx, session), newNaiveMatcher(revision2), session.rete)
}

func TestReteRuntimeNameTargetPlanExecutesOnGraph(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "dynamic-event",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact("event")}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "name-target-graph-session")
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"kind": "created"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if err := runtime.resetGraphBeta(ctx, mustSnapshot(t, ctx, session).Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}
	if len(runtime.plan.unsupported) != 0 {
		t.Fatalf("unsupported plan reasons = %#v, want none", runtime.plan.unsupported)
	}
	results, err := runtime.match(ctx, mustSnapshot(t, ctx, session))
	if err != nil {
		t.Fatalf("runtime match: %v", err)
	}
	if got, want := len(results), 1; got != want {
		t.Fatalf("match results = %d, want %d", got, want)
	}
}

func TestReteRuntimeBetaNoJoinSuccessorUsesLiveConditionRows(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "left-right-no-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{Binding: "right", Target: TemplateKeyFact(right.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initials := make([]SessionInitialFact, 0, reteMemoryMinimumFacts+1)
	for i := range reteMemoryMinimumFacts {
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

func TestReteRuntimeUsesGraphBetaForResidualOnlyNumericJoinPlans(t *testing.T) {
	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "threshold",
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "candidate",
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "older-than-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", Target: TemplateKeyFact(threshold.Key())},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				}, Target: TemplateKeyFact(candidate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initials := make([]SessionInitialFact, 0, reteMemoryMinimumFacts+3)
	for i := range reteMemoryMinimumFacts {
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
	session, err := NewSession(revision, WithSessionID("numeric-join-residual-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil {
		t.Fatal("expected Rete runtime")
	}
	if !session.rete.plan.betaSupported {
		t.Fatalf("beta plan = %#v, want supported for residual numeric joins", session.rete.plan)
	}
	session.attachPropagationCounters()
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) != 0 {
		t.Fatalf("unsupported reasons = %#v, want none", snapshot.UnsupportedReasons)
	}

	assertGraphBetaRuntimeParity(t, revision, session)
}

func TestReteRuntimeUsesGraphBetaForMixedEqualityAndResidualJoins(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "candidate",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "candidate-above-threshold",
		Conditions: []RuleConditionSpec{
			{
				Binding: "threshold", Target: TemplateKeyFact(threshold.Key()),
			},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "score"}},
				}, Target: TemplateKeyFact(candidate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("graph-beta-mixed-residual-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil || !session.rete.plan.betaSupported {
		t.Fatalf("graph beta runtime = %#v, want mixed graph beta support", session.rete)
	}
	session.attachPropagationCounters()
	initialCounters := session.propagationCounterSnapshot()
	if initialCounters.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", initialCounters.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(initialCounters.UnsupportedReasons) != 0 {
		t.Fatalf("unsupported reasons = %#v, want none for mixed hash/residual graph beta", initialCounters.UnsupportedReasons)
	}

	assertGraphBetaRuntimeParity(t, revision, session)

	thresholdFact, err := session.AssertTemplate(ctx, threshold.Key(), mustFields(t, map[string]any{"group": "A", "score": 10}))
	if err != nil {
		t.Fatalf("AssertTemplate threshold: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	failingCandidate, err := session.AssertTemplate(ctx, candidate.Key(), mustFields(t, map[string]any{"group": "A", "score": 8}))
	if err != nil {
		t.Fatalf("AssertTemplate failing candidate: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	_, err = session.AssertTemplate(ctx, candidate.Key(), mustFields(t, map[string]any{"group": "A", "score": 12}))
	if err != nil {
		t.Fatalf("AssertTemplate passing candidate: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := snapshot.Totals.BetaLeftInputInserts, 2; got != want {
		t.Fatalf("beta left input inserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaRightInputInserts, 2; got != want {
		t.Fatalf("beta right input inserts = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaBucketProbes, 3; got != want {
		t.Fatalf("beta bucket probes = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaJoinIndexHits, 2; got != want {
		t.Fatalf("beta join index hits = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaJoinIndexMisses, 1; got != want {
		t.Fatalf("beta join index misses = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaBucketDepthTotal, 2; got != want {
		t.Fatalf("beta bucket depth total = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaBucketDepthMax, 1; got != want {
		t.Fatalf("beta bucket depth max = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaCandidateRowsScanned, 2; got != want {
		t.Fatalf("beta candidate rows scanned = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaResidualTests, 2; got != want {
		t.Fatalf("beta residual tests = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaResidualFailures, 1; got != want {
		t.Fatalf("beta residual failures = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.BetaJoinedTokensProduced, 3; got != want {
		t.Fatalf("beta joined tokens produced = %d, want %d", got, want)
	}
	if got, want := len(snapshot.ByBranch), 1; got != want {
		t.Fatalf("branch counter count = %d, want %d: %#v", got, want, snapshot.ByBranch)
	}
	var branchKey propagationBranchKey
	var branchTotals propagationCounterTotals
	for key, totals := range snapshot.ByBranch {
		branchKey = key
		branchTotals = totals
	}
	if branchKey.ownerKind != reteGraphBranchOwnerRule {
		t.Fatalf("branch owner kind = %q, want %q", branchKey.ownerKind, reteGraphBranchOwnerRule)
	}
	if branchKey.terminalID == 0 {
		t.Fatalf("branch terminal ID = %d, want non-zero", branchKey.terminalID)
	}
	if got, want := branchKey.branchID, 0; got != want {
		t.Fatalf("branch ID = %d, want %d", got, want)
	}
	if got, want := branchTotals.TerminalRowsInserted, snapshot.Totals.TerminalRowsInserted; got != want {
		t.Fatalf("branch terminal rows inserted = %d, want total %d", got, want)
	}
	if got, want := branchTotals.TerminalDeltasEmitted, snapshot.Totals.TerminalDeltasEmitted; got != want {
		t.Fatalf("branch terminal deltas emitted = %d, want total %d", got, want)
	}
	if got, want := snapshot.BranchRowsRetained[branchKey], snapshot.TerminalRowsRetained; got != want {
		t.Fatalf("branch terminal rows retained = %d, want total %d", got, want)
	}
	fields := strings.Join(snapshot.runnerFields(), "\n")
	if !strings.Contains(fields, "propagation-by-branch=") {
		t.Fatalf("runner fields missing branch summary: %s", fields)
	}
	if !strings.Contains(fields, "terminal-rows-retained="+strconv.Itoa(snapshot.TerminalRowsRetained)) {
		t.Fatalf("runner fields missing branch retained rows: %s", fields)
	}

	if _, err := session.Modify(ctx, failingCandidate.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"group": "A", "score": 15})}); err != nil {
		t.Fatalf("Modify failing candidate: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Retract(ctx, thresholdFact.Fact.ID()); err != nil {
		t.Fatalf("Retract threshold: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	reverseSession, err := NewSession(revision, WithSessionID("graph-beta-mixed-residual-reverse-session"), WithInitialFacts(
		SessionInitialFact{TemplateKey: candidate.Key(), Fields: mustFields(t, map[string]any{"group": "B", "score": 8})},
		SessionInitialFact{TemplateKey: candidate.Key(), Fields: mustFields(t, map[string]any{"group": "B", "score": 14})},
		SessionInitialFact{TemplateKey: threshold.Key(), Fields: mustFields(t, map[string]any{"group": "B", "score": 10})},
	))
	if err != nil {
		t.Fatalf("NewSession reverse: %v", err)
	}
	if reverseSession.rete == nil || reverseSession.rete.graphBeta == nil {
		t.Fatalf("reverse graph beta runtime = %#v, want graph beta support", reverseSession.rete)
	}
	assertGraphBetaRuntimeParity(t, revision, reverseSession)
}

func TestReteRuntimeExecutesAlphaExpressionPredicates(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-person",
		Conditions: []RuleConditionSpec{{
			Binding: "person",

			Predicates: []ExpressionSpec{BooleanExpr{
				Operator: ExpressionBoolAnd,
				Operands: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentFieldExpr{Field: "age"},
						Right:    ConstExpr{Value: 18},
					},
					BooleanExpr{
						Operator: ExpressionBoolNot,
						Operands: []ExpressionSpec{CompareExpr{
							Operator: ExpressionCompareGreaterOrEqual,
							Left:     CurrentFieldExpr{Field: "age"},
							Right:    ConstExpr{Value: 65},
						}},
					},
					BooleanExpr{
						Operator: ExpressionBoolOr,
						Operands: []ExpressionSpec{
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "age"},
								Right:    ConstExpr{Value: 19},
							},
							CompareExpr{
								Operator: ExpressionCompareEqual,
								Left:     CurrentFieldExpr{Field: "age"},
								Right:    ConstExpr{Value: 20},
							},
						},
					},
				},
			}}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("alpha-expression-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil || !session.rete.supportsGraphBeta() {
		t.Fatalf("graph beta runtime = %#v, want expression predicate support", session.rete)
	}
	session.attachPropagationCounters()

	young, err := session.AssertTemplate(ctx, person.Key(), mustFields(t, map[string]any{"age": 17}))
	if err != nil {
		t.Fatalf("AssertTemplate young: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after young assert = %d, want 0", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Modify(ctx, young.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"age": 19})}); err != nil {
		t.Fatalf("Modify young to adult: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after adult modify = %d, want 1", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Modify(ctx, young.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"age": 16})}); err != nil {
		t.Fatalf("Modify adult to young: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after failing modify = %d, want 0", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Retract(ctx, young.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got := snapshot.Totals.ExpressionPredicateErrors; got != 0 {
		t.Fatalf("expression predicate errors = %d, want 0", got)
	}
}

func TestReteRuntimeExecutesBetaResidualExpressionPredicates(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	system := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "system",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	finding := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "finding",
		Fields: []FieldSpec{
			{Name: "system-id", Kind: ValueString, Required: true},
			{Name: "risk", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "system-finding-risk",
		Conditions: []RuleConditionSpec{
			{Binding: "system", Target: TemplateKeyFact(system.Key())},
			{
				Binding: "finding",

				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "system-id"},
						Right:    BindingFieldExpr{Binding: "system", Field: "id"},
					},
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentFieldExpr{Field: "risk"},
						Right:    ConstExpr{Value: 90},
					},
				}, Target: TemplateKeyFact(finding.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("beta-expression-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil || !session.rete.supportsGraphBeta() {
		t.Fatalf("graph beta runtime = %#v, want expression predicate support", session.rete)
	}
	session.attachPropagationCounters()

	systemFact, err := session.AssertTemplate(ctx, system.Key(), mustFields(t, map[string]any{"id": "sys-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate system: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, finding.Key(), mustFields(t, map[string]any{"system-id": "sys-2", "risk": 99})); err != nil {
		t.Fatalf("AssertTemplate wrong finding: %v", err)
	}
	lowRisk, err := session.AssertTemplate(ctx, finding.Key(), mustFields(t, map[string]any{"system-id": "sys-1", "risk": 70}))
	if err != nil {
		t.Fatalf("AssertTemplate low finding: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations before passing finding = %d, want 0", got)
	}

	passing, err := session.AssertTemplate(ctx, finding.Key(), mustFields(t, map[string]any{"system-id": "sys-1", "risk": 95}))
	if err != nil {
		t.Fatalf("AssertTemplate passing finding: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after passing finding = %d, want 1", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Modify(ctx, lowRisk.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"risk": 91})}); err != nil {
		t.Fatalf("Modify low finding: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 2 {
		t.Fatalf("pending activations after risk modify = %d, want 2", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Modify(ctx, passing.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"system-id": "sys-2"})}); err != nil {
		t.Fatalf("Modify passing finding away: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after system modify = %d, want 1", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Retract(ctx, systemFact.Fact.ID()); err != nil {
		t.Fatalf("Retract system: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after system retract = %d, want 0", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ExpressionPredicateTests, 1; got < want {
		t.Fatalf("expression predicate tests = %d, want at least %d", got, want)
	}
	if got := snapshot.Totals.ExpressionPredicateFailures; got != 0 {
		t.Fatalf("expression predicate failures = %d, want 0", got)
	}
	if got := snapshot.Totals.ExpressionPredicateErrors; got != 0 {
		t.Fatalf("expression predicate errors = %d, want 0", got)
	}
	if got := snapshot.Totals.BetaResidualTests; got != 0 {
		t.Fatalf("beta residual tests = %d, want 0 for expression-only beta filters", got)
	}
}

func TestReteRuntimeResidualFilterEvaluatesExpressionPredicateOnce(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "minimum", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "candidate",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	predicateCalls := 0
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "above-threshold",
		Args:   []ValueKind{ValueInt, ValueInt},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			predicateCalls++
			score, _ := args[0].AsInt64()
			minimum, _ := args[1].AsInt64()
			return NewValue(score > minimum)
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "candidate-above-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", Target: TemplateKeyFact(threshold.Key())},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{{
					Field:    "group",
					Operator: FieldConstraintEqual,
					Ref:      FieldRef{Binding: "threshold", Field: "group"},
				}},
				Predicates: []ExpressionSpec{
					Call("above-threshold", CurrentFieldExpr{Field: "score"}, BindingFieldExpr{Binding: "threshold", Field: "minimum"}),
				}, Target: TemplateKeyFact(candidate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	joinNode, residualNode := graphSplitJoinAndResidualFilterNodes(t, revision)
	if got, want := len(joinNode.hashJoins), 1; got != want {
		t.Fatalf("hash joins = %d, want %d", got, want)
	}
	if got, want := len(residualNode.predicates), 1; got != want {
		t.Fatalf("residual predicates = %d, want %d", got, want)
	}
	session := mustSession(t, revision, "residual-filter-expression-once")
	session.attachPropagationCounters()

	if _, err := session.AssertTemplate(ctx, threshold.Key(), mustFields(t, map[string]any{"group": "A", "minimum": 10})); err != nil {
		t.Fatalf("AssertTemplate threshold: %v", err)
	}
	if got := predicateCalls; got != 0 {
		t.Fatalf("predicate calls after left input = %d, want 0", got)
	}
	if _, err := session.AssertTemplate(ctx, candidate.Key(), mustFields(t, map[string]any{"group": "A", "score": 12})); err != nil {
		t.Fatalf("AssertTemplate candidate: %v", err)
	}
	if got, want := predicateCalls, 1; got != want {
		t.Fatalf("predicate calls = %d, want %d", got, want)
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ExpressionPredicateTests, 1; got != want {
		t.Fatalf("expression predicate tests = %d, want %d", got, want)
	}
}

func TestReteRuntimeOrBranchesDeduplicateEquivalentActivations(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error {
		fired++
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name: "or-dedupe",
		ConditionTree: Or{Conditions: []ConditionSpec{
			Match{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				}, Target: TemplateKeyFact(person.Key()),
			},
			Match{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "dept", Operator: FieldConstraintEqual, Value: "engineering"},
				}, Target: TemplateKeyFact(person.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("or-dedupe-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil || !session.rete.supportsGraphBeta() {
		t.Fatalf("graph beta runtime = %#v, want or branch support", session.rete)
	}
	if _, err := session.AssertTemplate(ctx, person.Key(), mustFields(t, map[string]any{"id": "p-1", "active": true, "dept": "engineering"})); err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations = %d, want 1", got)
	}
	assertGraphBetaRuntimeParity(t, revision, session)
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired = %d, want 1", fired)
	}
}

func TestReteRuntimeOrBranchSupportKeepsActivationWhileEquivalentBranchRemains(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "block",
		Fields: []FieldSpec{
			{Name: "person_id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "or-support",
		ConditionTree: Or{Conditions: []ConditionSpec{
			Match{Binding: "person", Target: TemplateKeyFact(person.Key())},
			And{Conditions: []ConditionSpec{
				Match{Binding: "person", Target: TemplateKeyFact(person.Key())},
				Not{Condition: Match{
					Binding: "block",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "person_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "id"}},
					}, Target: TemplateKeyFact(block.Key()),
				}},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("or-support-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, person.Key(), mustFields(t, map[string]any{"id": "p-1"})); err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got := len(pending); got != 1 {
		t.Fatalf("pending activations after person = %d, want 1", got)
	}
	activationID := pending[0].activationID()
	if got, want := terminalContributorBranchIDs(t, session, "or-support"), []int{0, 1}; !slices.Equal(got, want) {
		t.Fatalf("terminal contributor branch IDs after person = %#v, want %#v", got, want)
	}
	if _, err := session.AssertTemplate(ctx, block.Key(), mustFields(t, map[string]any{"person_id": "p-1"})); err != nil {
		t.Fatalf("AssertTemplate block: %v", err)
	}
	pending = session.agenda.pendingActivations()
	if got := len(pending); got != 1 {
		t.Fatalf("pending activations after block = %d, want 1", got)
	}
	if pending[0].activationID() != activationID {
		t.Fatalf("activation changed after one branch support disappeared: got %q want %q", pending[0].activationID(), activationID)
	}
	if got, want := terminalContributorBranchIDs(t, session, "or-support"), []int{0}; !slices.Equal(got, want) {
		t.Fatalf("terminal contributor branch IDs after block = %#v, want %#v", got, want)
	}
	for _, event := range collector.Events() {
		if event.Type == EventRuleDeactivated {
			t.Fatalf("unexpected deactivation event while equivalent branch remained: %#v", event)
		}
	}
}

func terminalContributorBranchIDs(t testing.TB, session *Session, ruleName string) []int {
	t.Helper()
	if session == nil || session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("session has no graph beta runtime")
	}
	rule, ok := session.revision.rules[ruleName]
	if !ok {
		t.Fatalf("rule %q not found", ruleName)
	}
	terminal := session.rete.graphBeta.terminalForRule(rule.revisionID)
	if terminal == nil {
		t.Fatalf("terminal for rule %q not found", ruleName)
	}
	if got, want := terminal.rows.len(), 1; got != want {
		t.Fatalf("terminal row count for rule %q = %d, want %d", ruleName, got, want)
	}
	return terminal.rows.rows[0].terminalBranchIDs()
}

func TestReteRuntimeRejectsMalformedExpressionPredicateShapes(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-person",
		Conditions: []RuleConditionSpec{{
			Binding: "person",

			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     CurrentFieldExpr{Field: "age"},
				Right:    ConstExpr{Value: 18},
			}}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)

	tests := []struct {
		name   string
		mutate func(*compiledExpressionPredicate)
	}{
		{
			name: "unsupported node kind",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.expression.kind = expressionNodeKind("unsupported-test-shape")
			},
		},
		{
			name: "invalid comparison operator",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.expression.compareOp = ExpressionCompareUnknown
			},
		},
		{
			name: "invalid comparison arity",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.expression.operands = predicate.expression.operands[:1]
			},
		},
		{
			name: "incompatible comparison operand kinds",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.expression.operands[0].resultKind = ValueString
				predicate.expression.operands[1].resultKind = ValueInt
			},
		},
		{
			name: "alpha predicate references binding",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.placement = ExpressionPredicatePlacementAlpha
				predicate.expression.operands[1] = compiledExpression{
					kind:        expressionNodeBindingField,
					resultKind:  ValueInt,
					access:      testCompiledPathAccess("age"),
					binding:     "person",
					bindingSlot: 0,
				}
			},
		},
		{
			name: "beta predicate has no binding reference",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.placement = ExpressionPredicatePlacementBetaResidual
			},
		},
		{
			name: "boolean operand is not bool",
			mutate: func(predicate *compiledExpressionPredicate) {
				predicate.expression = compiledExpression{
					kind:       expressionNodeBoolean,
					resultKind: ValueBool,
					boolOp:     ExpressionBoolAnd,
					operands: []compiledExpression{{
						kind:       expressionNodeConst,
						resultKind: ValueInt,
						value:      newIntValue(1),
					}},
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := *revision
			mutated.rules = make(map[string]compiledRule, len(revision.rules))
			maps.Copy(mutated.rules, revision.rules)
			rule := mutated.rules["adult-person"]
			rule.conditionPlans = append([]compiledConditionPlan(nil), rule.conditionPlans...)
			rule.conditionPlans[0].predicates = cloneCompiledExpressionPredicates(rule.conditionPlans[0].predicates)
			tc.mutate(&rule.conditionPlans[0].predicates[0])
			mutated.rules["adult-person"] = rule

			runtime, err := newReteRuntime(&mutated)
			if err != nil {
				t.Fatalf("newReteRuntime: %v", err)
			}
			if runtime.supportsGraphBeta() {
				t.Fatal("runtime supports graph beta for malformed expression predicate, want unsupported")
			}
			if got := runtime.plan.stats.unsupportedConditions; got != 1 {
				t.Fatalf("unsupported conditions = %d, want 1", got)
			}
			err = runtime.validateExecutableGraphBetaRuntime()
			if !errors.Is(err, ErrUnsupportedRuntime) {
				t.Fatalf("validateExecutableGraphBetaRuntime error = %v, want ErrUnsupportedRuntime", err)
			}
			for _, want := range []string{"expression-predicate", `rule="adult-person"`, `binding="person"`, "expression predicate 0"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("unsupported runtime error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestReteGraphAlphaExpressionPredicateErrorsAreCounted(t *testing.T) {
	ledger := newPropagationCounterLedger()
	span := ledger.beginAssert("", mutationOrigin{})
	node := reteGraphAlphaNode{
		target: conditionTarget{kind: conditionTargetName, name: "event"},
		predicates: []compiledExpressionPredicate{{
			placement: ExpressionPredicatePlacementAlpha,
			expression: compiledExpression{
				kind:       expressionNodeCompare,
				resultKind: ValueBool,
				compareOp:  ExpressionCompareUnknown,
				operands: []compiledExpression{
					{kind: expressionNodeCurrentField, resultKind: ValueInt, access: testCompiledPathAccess("score")},
					{kind: expressionNodeConst, resultKind: ValueInt, value: newIntValue(10)},
				},
			},
		}},
	}
	fact := FactSnapshot{
		id:     FactID{generation: 1, sequence: 1},
		name:   "event",
		fields: Fields{"score": newIntValue(20)},
	}
	if node.matchesSnapshotWithCounters(fact, &span) {
		t.Fatal("matchesSnapshotWithCounters = true, want false for malformed expression")
	}
	span.finish()

	snapshot := ledger.snapshot()
	if got, want := snapshot.Totals.ExpressionPredicateTests, 1; got != want {
		t.Fatalf("expression predicate tests = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.ExpressionPredicateErrors, 1; got != want {
		t.Fatalf("expression predicate errors = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ExpressionPredicateFailures; got != 0 {
		t.Fatalf("expression predicate failures = %d, want 0", got)
	}
}

func TestReteRuntimeDefaultSessionUsesGraphForSmallNameTargetPlan(t *testing.T) {
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
		Conditions: []RuleConditionSpec{{Binding: "event", Target: DynamicFact("event")}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(dynamic-event): %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "small-name-target-graph-session")
	if session.rete == nil {
		t.Fatal("expected default Rete runtime")
	}
	if len(session.rete.plan.unsupported) != 0 {
		t.Fatalf("unsupported plan reasons = %#v, want none", session.rete.plan.unsupported)
	}
	if session.propagationCounters != nil {
		t.Fatalf("normal session unexpectedly has propagation counters: %#v", session.propagationCounters)
	}
	session.attachPropagationCounters()
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) != 0 {
		t.Fatalf("unsupported reasons = %#v, want none", snapshot.UnsupportedReasons)
	}
	if _, err := session.Assert(ctx, "event", mustFields(t, map[string]any{"kind": "queued"})); err != nil {
		t.Fatalf("Assert(event): %v", err)
	}
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
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("graph beta memory after initial reset = %#v, want populated memories", session.rete)
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
	beforeBindings := cloneBindingTupleEntries(before.bindings())
	beforeFactIDs := cloneFactIDs(before.factIDs())
	beforeVersions := cloneFactVersions(before.factVersions())
	beforePath := cloneIntPath(before.path())

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
	if !reflect.DeepEqual(after.bindings(), beforeBindings) {
		t.Fatalf("activation bindings changed after candidate scratch reuse: got %#v want %#v", after.bindings(), beforeBindings)
	}
	if !reflect.DeepEqual(after.factIDs(), beforeFactIDs) {
		t.Fatalf("activation fact IDs changed after candidate scratch reuse: got %#v want %#v", after.factIDs(), beforeFactIDs)
	}
	if !reflect.DeepEqual(after.factVersions(), beforeVersions) {
		t.Fatalf("activation fact versions changed after candidate scratch reuse: got %#v want %#v", after.factVersions(), beforeVersions)
	}
	if !reflect.DeepEqual(after.path(), beforePath) {
		t.Fatalf("activation path changed after candidate scratch reuse: got %#v want %#v", after.path(), beforePath)
	}
}

func TestReteRuntimeBetaJoinTreatsExactIntegralFloatsAsInts(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "left",
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "right",
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueFloat, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "int-float-join",
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{
				Binding: "right",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "bucket"}},
				}, Target: TemplateKeyFact(right.Key()),
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

func TestReteRuntimeGraphBetaRemovalRetractSharedTopology(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey, regionKey, officeKey := mustGraphTopologyRemovalRuleset(t)
	initials := mustGraphTopologyRemovalInitialFacts(t, employeeKey, departmentKey, regionKey, officeKey)
	session, err := NewSession(revision, WithSessionID("graph-beta-shared-topology-retract-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	assertGraphTopologyRemovalShape(t, revision)
	if !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before retract = %d, want %d", got, want)
	}
	results, err := session.rete.match(ctx, mustSnapshot(t, ctx, session))
	if err != nil {
		t.Fatalf("initial Rete match: %v", err)
	}
	candidateAgenda := newAgenda()
	if _, err := candidateAgenda.reconcile(ctx, revision, results); err != nil {
		t.Fatalf("candidate reconcile: %v", err)
	}
	directAgenda := newAgenda()
	if _, err := directAgenda.reconcile(ctx, revision, results); err != nil {
		t.Fatalf("direct reconcile: %v", err)
	}

	session.attachPropagationCounters()
	initialSnapshot := session.propagationCounterSnapshot()
	if got, want := initialSnapshot.TerminalRowsRetained, 2; got != want {
		t.Fatalf("terminal rows retained after attach = %d, want %d", got, want)
	}
	department := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	_, delta, err := session.retractImmediate(ctx, department.ID(), mutationOrigin{})
	if err != nil {
		t.Fatalf("retractImmediate(%s): %v", department.ID(), err)
	}
	if got, want := len(delta.removed), 2; got != want {
		t.Fatalf("terminal token removals = %d, want %d", got, want)
	}
	directChanges := assertTerminalTokenDeltaMatchesCandidateDelta(t, revision, session, candidateAgenda, directAgenda, delta)
	if got, want := len(directChanges), 2; got != want {
		t.Fatalf("direct agenda changes = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after retract = %d, want 0", got)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := snapshot.Totals.TerminalDeltasRemoved, 2; got != want {
		t.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsRemoved, 2; got != want {
		t.Fatalf("terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativePropagationEvents, 4; got != want {
		t.Fatalf("negative propagation events = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeRowsRemoved, 3; got != want {
		t.Fatalf("negative rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeTerminalRowsRemoved, 2; got != want {
		t.Fatalf("negative terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.TerminalRowsRetained, 0; got != want {
		t.Fatalf("terminal rows retained after retract = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 6; got != want {
		t.Fatalf("removal index lookups = %d, want topology-limited %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsTouched, 5; got != want {
		t.Fatalf("removal rows touched = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsRemoved, 5; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaRemovalModifySharedTopology(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey, regionKey, officeKey := mustGraphTopologyRemovalRuleset(t)
	initials := mustGraphTopologyRemovalInitialFacts(t, employeeKey, departmentKey, regionKey, officeKey)
	session, err := NewSession(revision, WithSessionID("graph-beta-shared-topology-modify-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	assertGraphTopologyRemovalShape(t, revision)
	if !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	results, err := session.rete.match(ctx, mustSnapshot(t, ctx, session))
	if err != nil {
		t.Fatalf("initial Rete match: %v", err)
	}
	candidateAgenda := newAgenda()
	if _, err := candidateAgenda.reconcile(ctx, revision, results); err != nil {
		t.Fatalf("candidate reconcile: %v", err)
	}
	directAgenda := newAgenda()
	if _, err := directAgenda.reconcile(ctx, revision, results); err != nil {
		t.Fatalf("direct reconcile: %v", err)
	}

	session.attachPropagationCounters()
	region := mustSessionFactByTemplateAndField(t, session, regionKey, "id", "East")
	result, delta, err := session.modifyImmediate(ctx, region.ID(), FactPatch{Set: mustFields(t, map[string]any{"id": "West"})}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate(%s): %v", region.ID(), err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal token removals = %d, want %d", got, want)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	directChanges := assertTerminalTokenDeltaMatchesCandidateDelta(t, revision, session, candidateAgenda, directAgenda, delta)
	if got, want := len(directChanges), 1; got != want {
		t.Fatalf("direct agenda changes = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after modify = %d, want 1", got)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
	if got, want := snapshot.Totals.TerminalDeltasRemoved, 1; got != want {
		t.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 2; got != want {
		t.Fatalf("removal index lookups = %d, want topology-limited %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsTouched, 2; got != want {
		t.Fatalf("removal rows touched = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsRemoved, 2; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaModifyStopsMatchingAlphaCondition(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustAlphaMemoryRuleset(t, "active-person", []FieldConstraintSpec{
		{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
	})
	session := mustSession(t, revision, "graph-beta-modify-stops-alpha-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{"age": 20, "status": "active"}))
	if err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"status": "inactive"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal token removals = %d, want %d", got, want)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after modify = %d, want 0", got)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	assertGraphBetaAlphaFactCount(t, session, "active-person", 0, 0)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := snapshot.Totals.TerminalDeltasRemoved, 1; got != want {
		t.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativePropagationEvents, 1; got != want {
		t.Fatalf("negative propagation events = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeTerminalRowsRemoved, 1; got != want {
		t.Fatalf("negative terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 1; got != want {
		t.Fatalf("removal index lookups = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsTouched, 1; got != want {
		t.Fatalf("removal rows touched = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsRemoved, 1; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaModifyUnmatchedIrrelevantSlotSkipsPropagation(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-unmatched-irrelevant-slot-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{
		"age":    20,
		"note":   "old",
		"status": "inactive",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations before modify = %d, want 0", got)
	}
	assertGraphBetaAlphaFactCount(t, session, "active-person", 0, 0)

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got, ok := result.Fact.Field("note"); !ok || !got.Equal(mustValue(t, "new")) {
		t.Fatalf("modified note = %v, %t; want new, true", got, ok)
	}
	assertGraphBetaAlphaFactCount(t, session, "active-person", 0, 0)
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
	if got := snapshot.Totals.RemovalIndexLookups; got != 0 {
		t.Fatalf("removal index lookups = %d, want 0", got)
	}
	if got := snapshot.Totals.TerminalDeltasRemoved; got != 0 {
		t.Fatalf("terminal deltas removed = %d, want 0", got)
	}
}

func TestReteRuntimeGraphBetaModifyMatchedIrrelevantSlotUsesRouteScopedEvents(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustModifyFastPathRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-matched-irrelevant-slot-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{
		"age":    20,
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal token removals = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal token additions = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 1; got != want {
		t.Fatalf("removal index lookups = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaModifyMatchedDeclaredUnobservedSlotRefreshesActivation(t *testing.T) {
	ctx := context.Background()
	revision, personKey := mustModifyFastPathDeclaredNoReadRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-matched-declared-unobserved-slot-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(t, map[string]any{
		"age":    20,
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	beforeActivationID := pending[0].activationID()

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal token updates = %d, want %d", got, want)
	}
	if delta.updated[0].before.isZero() {
		t.Fatal("terminal update before token is zero")
	}
	if delta.updated[0].after.isZero() {
		t.Fatal("terminal update after token was not refreshed")
	}
	match, ok := tokenFactMatchForBindingSlot(delta.updated[0].after, 0)
	if !ok {
		t.Fatal("terminal update after token missing refreshed fact match")
	}
	if got, want := match.fact.Version(), result.Fact.Version(); got != want {
		t.Fatalf("terminal update after token version = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	pending = session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after modify = %d, want %d", got, want)
	}
	if got := pending[0].activationID(); got != beforeActivationID {
		t.Fatalf("activation ID after unobserved modify = %q, want %q", got, beforeActivationID)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run after token refresh: %v", err)
	}

	result, delta, err = session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"status": "inactive"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate status: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("status modify = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after relevant modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply relevant delta: %v", err)
	} else if !ok {
		t.Fatal("apply relevant delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeGraphBetaModifyAlphaPredicateUnobservedSlotRefreshesActivation(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "predicate-person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "adult-person",
		Conditions: []RuleConditionSpec{{
			Binding: "person",

			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     CurrentFieldExpr{Field: "age"},
				Right:    ConstExpr{Value: 18},
			}}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "graph-beta-modify-alpha-predicate-unobserved-slot-session")
	inserted, err := session.AssertTemplate(ctx, person.Key(), mustFields(t, map[string]any{
		"age":  20,
		"note": "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	beforeActivationID := pending[0].activationID()

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("note modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal removals after note modify = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal additions after note modify = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal updates after note modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply note delta: %v", err)
	} else if !ok {
		t.Fatal("apply note delta unexpectedly skipped")
	}
	pending = session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after note modify = %d, want %d", got, want)
	}
	if got := pending[0].activationID(); got != beforeActivationID {
		t.Fatalf("activation ID after note modify = %q, want %q", got, beforeActivationID)
	}
	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips after note modify = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after note modify = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(ctx, inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"age": 17}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate age: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("age modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after age modify = %d, want %d", got, want)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal additions after age modify = %d, want 0", got)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply age delta: %v", err)
	} else if !ok {
		t.Fatal("apply age delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeGraphBetaModifyJoinedDeclaredUnobservedSlotRefreshesActivation(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey := mustBetaModifyFastPathDeclaredNoReadRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-joined-declared-unobserved-slot-session")
	employee, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{
		"name": "Ada",
		"dept": "Engineering",
		"note": "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate department: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	beforeActivationID := pending[0].activationID()

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, employee.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal token updates = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	pending = session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after modify = %d, want %d", got, want)
	}
	if got := pending[0].activationID(); got != beforeActivationID {
		t.Fatalf("activation ID after joined unobserved modify = %q, want %q", got, beforeActivationID)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run after joined token refresh: %v", err)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(ctx, employee.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"dept": "Research"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate dept: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("dept modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after join-key modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply join-key delta: %v", err)
	} else if !ok {
		t.Fatal("apply join-key delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeGraphBetaModifyFilterDeclaredUnobservedSlotRefreshesActivation(t *testing.T) {
	ctx := context.Background()
	revision, eventKey := mustFilterModifyFastPathDeclaredNoReadRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-filter-declared-unobserved-slot-session")
	event, err := session.AssertTemplate(ctx, eventKey, mustFields(t, map[string]any{
		"id":     "event-1",
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate event: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	beforeActivationID := pending[0].activationID()

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, event.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal token updates = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	pending = session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after modify = %d, want %d", got, want)
	}
	if got := pending[0].activationID(); got != beforeActivationID {
		t.Fatalf("activation ID after filter unobserved modify = %q, want %q", got, beforeActivationID)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run after filter token refresh: %v", err)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(ctx, event.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"status": "inactive"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate status: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("status modify = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal removals after filter predicate modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply predicate delta: %v", err)
	} else if !ok {
		t.Fatal("apply predicate delta unexpectedly skipped")
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeGraphBetaModifyNegationLeftDeclaredUnobservedSlotRefreshesActivation(t *testing.T) {
	ctx := context.Background()
	revision, customerKey, _ := mustNegationModifyFastPathDeclaredNoReadRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-negation-left-declared-unobserved-slot-session")
	customer, err := session.AssertTemplate(ctx, customerKey, mustFields(t, map[string]any{
		"id":   "c-1",
		"note": "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate customer: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}
	beforeActivationID := pending[0].activationID()

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, customer.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"note": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate note: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got, want := len(delta.updated), 1; got != want {
		t.Fatalf("terminal token updates = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	pending = session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after modify = %d, want %d", got, want)
	}
	if got := pending[0].activationID(); got != beforeActivationID {
		t.Fatalf("activation ID after negative left unobserved modify = %q, want %q", got, beforeActivationID)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
}

func TestReteRuntimeGraphBetaModifyNegationRightDeclaredUnobservedSlotRefreshesBlocker(t *testing.T) {
	ctx := context.Background()
	revision, customerKey, blockKey := mustNegationModifyFastPathDeclaredNoReadRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-negation-right-declared-unobserved-slot-session")
	if _, err := session.AssertTemplate(ctx, customerKey, mustFields(t, map[string]any{
		"id":   "c-1",
		"note": "customer",
	})); err != nil {
		t.Fatalf("AssertTemplate customer: %v", err)
	}
	block, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{
		"customer_id": "c-1",
		"active":      true,
		"code":        "old",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate block: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations before modify = %d, want 0", got)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, block.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"code": "new"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate code: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got := len(delta.removed); got != 0 {
		t.Fatalf("terminal token removals = %d, want 0", got)
	}
	if got := len(delta.added); got != 0 {
		t.Fatalf("terminal token additions = %d, want 0", got)
	}
	if got := len(delta.updated); got != 0 {
		t.Fatalf("terminal token updates = %d, want 0", got)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after blocker metadata modify = %d, want 0", got)
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.ModifyFastPathSkips, 1; got != want {
		t.Fatalf("modify fast-path skips after code modify = %d, want %d", got, want)
	}
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after code modify = %d, want 0", got)
	}

	result, delta, err = session.modifyImmediate(ctx, block.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"active": false}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate active: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("active modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal additions after blocker predicate modify = %d, want %d", got, want)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("apply predicate delta: %v", err)
	} else if !ok {
		t.Fatal("apply predicate delta unexpectedly skipped")
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after blocker predicate modify = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot = session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks after active modify = %d, want 0", got)
	}
}

func TestReteRuntimeGraphBetaModifyReplacesJoinedTokenVersion(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-joined-version-session")
	employee, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"}))
	if err != nil {
		t.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate department: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, employee.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal token removals = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal token additions = %d, want %d", got, want)
	}
	removed := terminalDeltaCandidateForFact(t, session, delta.removed, employee.Fact.ID(), &session.rete.terminalRemovedScratch)
	added := terminalDeltaCandidateForFact(t, session, delta.added, employee.Fact.ID(), &session.rete.terminalAddedScratch)
	removedIndex := candidateFactIndex(t, removed, employee.Fact.ID())
	addedIndex := candidateFactIndex(t, added, employee.Fact.ID())
	if removed.factVersions[removedIndex] != employee.Fact.Version() {
		t.Fatalf("removed employee version = %d, want %d", removed.factVersions[removedIndex], employee.Fact.Version())
	}
	if added.factVersions[addedIndex] != result.Fact.Version() {
		t.Fatalf("added employee version = %d, want %d", added.factVersions[addedIndex], result.Fact.Version())
	}
	if removed.identity == added.identity {
		t.Fatalf("modify reused terminal identity %#v", added.identity)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after modify = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
}

func TestReteRuntimeGraphBetaModifyMovesBetweenJoinBuckets(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-join-bucket-session")
	employee, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"}))
	if err != nil {
		t.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate Engineering: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate Sales: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations before modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	result, delta, err := session.modifyImmediate(ctx, employee.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"dept": "Sales"}),
	}, mutationOrigin{})
	if err != nil {
		t.Fatalf("modifyImmediate: %v", err)
	}
	if result.Status != ModifyChanged {
		t.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
	}
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("terminal token removals = %d, want %d", got, want)
	}
	if got, want := len(delta.added), 1; got != want {
		t.Fatalf("terminal token additions = %d, want %d", got, want)
	}
	removed := terminalDeltaCandidateForFact(t, session, delta.removed, employee.Fact.ID(), &session.rete.terminalRemovedScratch)
	added := terminalDeltaCandidateForFact(t, session, delta.added, employee.Fact.ID(), &session.rete.terminalAddedScratch)
	if removed.factIDs[1] == added.factIDs[1] {
		t.Fatalf("modify did not move joined department: removed %#v added %#v", removed.factIDs, added.factIDs)
	}
	if _, ok, err := session.applyReteAgendaDelta(ctx, delta); err != nil {
		t.Fatalf("applyReteAgendaDelta: %v", err)
	} else if !ok {
		t.Fatal("applyReteAgendaDelta unexpectedly skipped")
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after modify = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
	if got, want := snapshot.Totals.TerminalDeltasRemoved, 1; got != want {
		t.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeRowsRemoved, 1; got != want {
		t.Fatalf("negative rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeTerminalRowsRemoved, 1; got != want {
		t.Fatalf("negative terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.TerminalRowsRetained, 1; got != want {
		t.Fatalf("terminal rows retained after modify = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaNegationAssertRetract(t *testing.T) {
	ctx := context.Background()
	var fired int
	revision, customerKey, blockKey := mustNegationRuleset(t, func(ctx ActionContext) error {
		if _, ok := ctx.Binding("block"); ok {
			t.Fatal("negated binding should not be visible to actions")
		}
		if _, ok := ctx.Binding("customer"); !ok {
			t.Fatal("customer binding missing")
		}
		fired++
		return nil
	})
	session := mustSession(t, revision, "graph-beta-negation-assert-retract-session")
	if !session.rete.supportsGraphBeta() {
		t.Fatalf("runtime does not support graph beta: %#v", session.rete.plan.unsupported)
	}

	if _, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-2", "active": true})); err != nil {
		t.Fatalf("AssertTemplate(unrelated block): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations with only right facts = %d, want 0", got)
	}
	customer, err := session.AssertTemplate(ctx, customerKey, mustFields(t, map[string]any{"id": "c-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(customer): %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after customer = %d, want %d", got, want)
	}
	block, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": true}))
	if err != nil {
		t.Fatalf("AssertTemplate(block): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after blocking fact = %d, want 0", got)
	}
	if _, err := session.Retract(ctx, block.Fact.ID()); err != nil {
		t.Fatalf("Retract(block): %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after block retract = %d, want %d", got, want)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired actions = %d, want 1", fired)
	}
	blockAgain, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": true, "code": "after-run"}))
	if err != nil {
		t.Fatalf("AssertTemplate(block after run): %v", err)
	}
	if _, err := session.Retract(ctx, blockAgain.Fact.ID()); err != nil {
		t.Fatalf("Retract(block after run): %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after consumed negative activation unblocked = %d, want %d", got, want)
	}
	if _, err := session.Retract(ctx, customer.Fact.ID()); err != nil {
		t.Fatalf("Retract(customer): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after customer retract = %d, want 0", got)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeGraphBetaNegationModifyAndMultipleBlockers(t *testing.T) {
	ctx := context.Background()
	revision, customerKey, blockKey := mustNegationRuleset(t, func(ActionContext) error { return nil })
	session := mustSession(t, revision, "graph-beta-negation-modify-session")
	if _, err := session.AssertTemplate(ctx, customerKey, mustFields(t, map[string]any{"id": "c-1"})); err != nil {
		t.Fatalf("AssertTemplate(customer): %v", err)
	}
	inactive, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": false}))
	if err != nil {
		t.Fatalf("AssertTemplate(inactive block): %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations with inactive block = %d, want %d", got, want)
	}
	if _, err := session.Modify(ctx, inactive.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"active": true})}); err != nil {
		t.Fatalf("Modify block active=true: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations with active block = %d, want 0", got)
	}
	if _, err := session.Modify(ctx, inactive.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"code": "still-blocking"})}); err != nil {
		t.Fatalf("Modify active block code: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after still-blocking modify = %d, want 0", got)
	}
	if _, err := session.Modify(ctx, inactive.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"active": false})}); err != nil {
		t.Fatalf("Modify block active=false: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after inactive modify = %d, want %d", got, want)
	}

	session.attachPropagationCounters()
	first, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": true, "code": "first"}))
	if err != nil {
		t.Fatalf("AssertTemplate(first active block): %v", err)
	}
	second, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": true, "code": "second"}))
	if err != nil {
		t.Fatalf("AssertTemplate(second active block): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations with two active blocks = %d, want 0", got)
	}
	if _, err := session.Retract(ctx, first.Fact.ID()); err != nil {
		t.Fatalf("Retract(first block): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations with one remaining active block = %d, want 0", got)
	}
	if _, err := session.Retract(ctx, second.Fact.ID()); err != nil {
		t.Fatalf("Retract(second block): %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 1; got != want {
		t.Fatalf("pending activations after last block retract = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) != 0 {
		t.Fatalf("unsupported reasons = %#v, want none", snapshot.UnsupportedReasons)
	}
}

func TestReteRuntimeGraphBetaNegationRetractBlockedLeftWithMultipleBlockers(t *testing.T) {
	ctx := context.Background()
	revision, customerKey, blockKey := mustNegationRuleset(t, func(ActionContext) error { return nil })
	session := mustSession(t, revision, "graph-beta-negation-retract-blocked-left-session")
	customer, err := session.AssertTemplate(ctx, customerKey, mustFields(t, map[string]any{"id": "c-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(customer): %v", err)
	}
	first, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": true, "code": "first"}))
	if err != nil {
		t.Fatalf("AssertTemplate(first active block): %v", err)
	}
	second, err := session.AssertTemplate(ctx, blockKey, mustFields(t, map[string]any{"customer_id": "c-1", "active": true, "code": "second"}))
	if err != nil {
		t.Fatalf("AssertTemplate(second active block): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations with two active blocks = %d, want 0", got)
	}

	session.attachPropagationCounters()
	if _, err := session.Retract(ctx, customer.Fact.ID()); err != nil {
		t.Fatalf("Retract(customer): %v", err)
	}
	if _, err := session.Retract(ctx, first.Fact.ID()); err != nil {
		t.Fatalf("Retract(first block): %v", err)
	}
	if _, err := session.Retract(ctx, second.Fact.ID()); err != nil {
		t.Fatalf("Retract(second block): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after blocked customer and blockers retract = %d, want 0", got)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) != 0 {
		t.Fatalf("unsupported reasons = %#v, want none", snapshot.UnsupportedReasons)
	}
}

func TestReteRuntimeGraphBetaModifyDoesNotRequeueConsumedActivation(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session := mustSession(t, revision, "graph-beta-modify-consumed-activation-session")
	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate Ada: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ben", "dept": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate Ben: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate Engineering: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate Sales: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before consume = %d, want %d", got, want)
	}

	consumed, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda next returned no activation")
	}
	storedConsumed, ok := session.agenda.activationByKey(consumed.key)
	if !ok {
		t.Fatalf("consumed activation %v missing after next", consumed.key)
	}
	consumedFactIDs := cloneFactIDs(storedConsumed.factIDs())
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations after consume = %d, want %d", got, want)
	}
	targetEmployeeID := activationFactIDForTemplate(t, session, pending[0], employeeKey)

	session.attachPropagationCounters()
	if _, err := session.Modify(ctx, targetEmployeeID, FactPatch{
		Set: mustFields(t, map[string]any{"dept": "Missing"}),
	}); err != nil {
		t.Fatalf("Modify pending employee: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after modify = %d, want 0", got)
	}
	stored, ok := session.agenda.activationByKey(consumed.key)
	if !ok {
		t.Fatalf("consumed activation %v disappeared", consumed.key)
	}
	if stored.status != activationStatusConsumed {
		t.Fatalf("consumed activation status = %v, want %v", stored.status, activationStatusConsumed)
	}
	if !reflect.DeepEqual(stored.factIDs(), consumedFactIDs) {
		t.Fatalf("consumed activation facts = %#v, want %#v", stored.factIDs(), consumedFactIDs)
	}
	snapshot := session.propagationCounterSnapshot()
	if got := snapshot.Totals.ModifyFastPathFallbacks; got != 0 {
		t.Fatalf("modify fast-path fallbacks = %d, want 0", got)
	}
}

func TestReteRuntimeGraphBetaRemovalResetSharedTopology(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey, regionKey, officeKey := mustGraphTopologyRemovalRuleset(t)
	initials := mustGraphTopologyRemovalInitialFacts(t, employeeKey, departmentKey, regionKey, officeKey)
	session, err := NewSession(revision, WithSessionID("graph-beta-shared-topology-reset-session"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	assertGraphTopologyRemovalShape(t, revision)
	if !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations before retract = %d, want %d", got, want)
	}
	assertGraphBetaAlphaFactCount(t, session, "employee-department-region-a", 1, 1)
	assertGraphBetaAlphaFactCount(t, session, "employee-department-office", 1, 1)

	department := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	if _, err := session.Retract(ctx, department.ID()); err != nil {
		t.Fatalf("Retract(Engineering department): %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after retract = %d, want 0", got)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
	assertGraphBetaAlphaFactCount(t, session, "employee-department-region-a", 1, 0)
	assertGraphBetaAlphaFactCount(t, session, "employee-department-office", 1, 0)

	resetResult, err := session.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if resetResult.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", resetResult.Status, ResetApplied)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("Rete runtime after reset = %#v, want graph beta", session.rete)
	}
	if got, want := len(session.agenda.pendingActivations()), 2; got != want {
		t.Fatalf("pending activations after reset = %d, want %d", got, want)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	assertGraphBetaRuntimeParity(t, revision, session)
	assertGraphBetaAlphaFactCount(t, session, "employee-department-region-a", 1, 1)
	assertGraphBetaAlphaFactCount(t, session, "employee-department-office", 1, 1)
}

func TestReteRuntimeGraphBetaTerminalMemoryDiagnostics(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "candidate",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "candidate-above-threshold",
		Conditions: []RuleConditionSpec{
			{
				Binding: "threshold", Target: TemplateKeyFact(threshold.Key()),
			},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "score"}},
				}, Target: TemplateKeyFact(candidate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("graph-beta-terminal-memory-diagnostics"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("graph beta runtime = %#v, want graph beta support", session.rete)
	}
	session.attachPropagationCounters()

	initialCounters := session.propagationCounterSnapshot()
	if initialCounters.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", initialCounters.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := initialCounters.TerminalRowsRetained, 0; got != want {
		t.Fatalf("terminal rows retained = %d, want %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.TokenMemories, 1; got != want {
		t.Fatalf("graph token memories = %d, want only initialized terminal memory %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.BetaTokenMemories, 0; got != want {
		t.Fatalf("graph beta token memories = %d, want %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.TerminalTokenMemories, 1; got != want {
		t.Fatalf("graph terminal token memories = %d, want %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.TokenRows, 0; got != want {
		t.Fatalf("graph token rows = %d, want %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.TokenRowReserve, 0; got != want {
		t.Fatalf("graph token row reserve = %d, want lazy initial reserve %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.JoinIndexReserve, 0; got != want {
		t.Fatalf("graph join index reserve = %d, want lazy initial reserve %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.IdentityIndexReserve, 0; got != want {
		t.Fatalf("graph identity index reserve = %d, want lazy initial reserve %d", got, want)
	}
	if got, want := initialCounters.GraphBetaMemory.FactIndexReserve, 0; got != want {
		t.Fatalf("graph fact index reserve = %d, want lazy initial reserve %d", got, want)
	}

	thresholdFact, err := session.AssertTemplate(ctx, threshold.Key(), mustFields(t, map[string]any{"group": "A", "score": 10}))
	if err != nil {
		t.Fatalf("AssertTemplate threshold: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, candidate.Key(), mustFields(t, map[string]any{"group": "A", "score": 12})); err != nil {
		t.Fatalf("AssertTemplate candidate: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	rule := revision.rules["candidate-above-threshold"]
	terminal := session.rete.graphBeta.terminalForRule(rule.revisionID)
	if terminal == nil || terminal.rows.len() != 1 {
		t.Fatalf("terminal memory = %#v, want one retained row", terminal)
	}
	var terminalID reteGraphTerminalNodeID
	for _, node := range revision.graph.terminalNodes {
		if node.ruleRevisionID == rule.revisionID {
			terminalID = node.id
			break
		}
	}
	if terminalID == 0 {
		t.Fatalf("terminal node for rule revision %q not found", rule.revisionID)
	}
	terminalToken := terminal.rows.rows[0].token
	if terminalToken.isZero() {
		t.Fatal("retained terminal token is zero")
	}

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.TerminalRowsInserted, 1; got != want {
		t.Fatalf("terminal rows inserted = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsDeduped, 0; got != want {
		t.Fatalf("terminal rows deduped = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsRemoved, 0; got != want {
		t.Fatalf("terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.TerminalRowsRetained, 1; got != want {
		t.Fatalf("terminal rows retained = %d, want %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.TokenRows, 4; got != want {
		t.Fatalf("graph token rows = %d, want beta inputs plus terminal row %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.JoinIndexKeys, 3; got != want {
		t.Fatalf("graph join index keys = %d, want join and residual filter beta keys %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.IdentityIndexKeys, 4; got != want {
		t.Fatalf("graph identity index keys = %d, want each retained token identity %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.FactIndexKeys, 6; got != want {
		t.Fatalf("graph fact index keys = %d, want beta fact rows plus terminal token facts %d", got, want)
	}

	duplicateDelta := reteAgendaDelta{supported: true}
	duplicateSpan := propagationCounterSpan{ledger: session.propagationCounters}
	_, _ = session.rete.graphBeta.insertTerminalToken(terminalID, 0, terminalToken, &duplicateDelta, &duplicateSpan)
	duplicateSpan.finish()
	if len(duplicateDelta.added) != 0 {
		t.Fatalf("duplicate terminal delta additions = %d, want 0", len(duplicateDelta.added))
	}
	snapshot = session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.TerminalRowsInserted, 1; got != want {
		t.Fatalf("terminal rows inserted after duplicate = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsDeduped, 1; got != want {
		t.Fatalf("terminal rows deduped after duplicate = %d, want %d", got, want)
	}
	if got, want := snapshot.TerminalRowsRetained, 1; got != want {
		t.Fatalf("terminal rows retained after duplicate = %d, want %d", got, want)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	if _, err := session.Retract(ctx, thresholdFact.Fact.ID()); err != nil {
		t.Fatalf("Retract threshold: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	snapshot = session.propagationCounterSnapshot()
	if got, want := snapshot.Totals.TerminalRowsRemoved, 1; got != want {
		t.Fatalf("terminal rows removed after retract = %d, want %d", got, want)
	}
	if got, want := snapshot.TerminalRowsRetained, 0; got != want {
		t.Fatalf("terminal rows retained after retract = %d, want %d", got, want)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	snapshot = session.propagationCounterSnapshot()
	if got, want := snapshot.TerminalRowsRetained, 0; got != want {
		t.Fatalf("terminal rows retained after reset = %d, want %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.FactIndexReserve, 0; got != want {
		t.Fatalf("graph fact index reserve after reset = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsInserted, 1; got != want {
		t.Fatalf("terminal rows inserted after reset = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsDeduped, 1; got != want {
		t.Fatalf("terminal rows deduped after reset = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsRemoved, 1; got != want {
		t.Fatalf("terminal rows removed after reset = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaTerminalRowsAndAgendaShareTokenIdentity(t *testing.T) {
	ctx := context.Background()
	revision, _, employeeKey, departmentKey := mustBetaMemoryRuleset(t)
	session := mustSession(t, revision, "graph-beta-terminal-agenda-identity-session")
	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(t, map[string]any{"id": "Engineering"})); err != nil {
		t.Fatalf("AssertTemplate department: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}

	rule := revision.rules["employee-department"]
	terminal := session.rete.graphBeta.terminalForRule(rule.revisionID)
	if terminal == nil || terminal.rows.len() != 1 {
		t.Fatalf("terminal memory = %#v, want one row", terminal)
	}
	row := terminal.rows.rows[0]
	if row.terminalIdentity.isZero() {
		t.Fatal("terminal row identity is zero")
	}
	if row.terminalIdentity != terminal.terminalTokenIdentity(row.token) {
		t.Fatalf("terminal row identity = %#v, want token identity", row.terminalIdentity)
	}

	tokens, ok, err := session.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok || len(tokens) != 1 {
		t.Fatalf("terminal tokens = %#v, ok=%v, want one", tokens, ok)
	}
	if tokens[0].identity != row.terminalIdentity {
		t.Fatalf("terminal delta identity = %#v, want %#v", tokens[0].identity, row.terminalIdentity)
	}

	if got, want := len(session.agenda.pending), 1; got != want {
		t.Fatalf("pending keys = %d, want %d", got, want)
	}
	stored, ok := session.agenda.activationByKeyPtr(session.agenda.pending[0])
	if !ok {
		t.Fatal("pending activation not found")
	}
	if stored.identity != row.terminalIdentity {
		t.Fatalf("activation identity = %#v, want terminal row identity %#v", stored.identity, row.terminalIdentity)
	}
	if !stored.id.IsZero() {
		t.Fatalf("stored activation ID = %q, want lazy zero ID", stored.id)
	}

	duplicateTokens := append(cloneTerminalTokenDeltas(tokens), tokens[0])
	agenda := newAgenda()
	changes, err := agenda.reconcileTerminalTokens(ctx, revision, duplicateTokens)
	if err != nil {
		t.Fatalf("reconcile duplicate terminal tokens: %v", err)
	}
	if got, want := len(changes), 1; got != want {
		t.Fatalf("duplicate terminal token changes = %d, want %d", got, want)
	}
	if got, want := len(agenda.pending), 1; got != want {
		t.Fatalf("duplicate terminal token pending keys = %d, want %d", got, want)
	}
}

func TestReteRuntimeGraphBetaRetractedReassertedFactGetsNewTokenIdentity(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "item-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "item", Target: TemplateKeyFact(item.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "graph-beta-reasserted-token-identity-session")

	first, err := session.AssertTemplate(ctx, item.Key(), mustFields(t, map[string]any{"id": "same"}))
	if err != nil {
		t.Fatalf("AssertTemplate first: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcile first: %v", err)
	}
	firstActivation := singlePendingActivation(t, session)
	firstIdentity := firstActivation.identity
	firstFactIDs := cloneFactIDs(firstActivation.factIDs())
	if firstIdentity.isZero() {
		t.Fatal("first identity is zero")
	}

	if _, err := session.Retract(ctx, first.Fact.ID()); err != nil {
		t.Fatalf("Retract first: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending after retract = %d, want 0", got)
	}

	second, err := session.AssertTemplate(ctx, item.Key(), mustFields(t, map[string]any{"id": "same"}))
	if err != nil {
		t.Fatalf("AssertTemplate second: %v", err)
	}
	if first.Fact.ID() == second.Fact.ID() {
		t.Fatalf("reasserted fact reused ID %q", second.Fact.ID())
	}
	secondActivation := singlePendingActivation(t, session)
	if secondActivation.identity == firstIdentity {
		t.Fatalf("reasserted activation reused identity %#v", secondActivation.identity)
	}
	if reflect.DeepEqual(secondActivation.factIDs(), firstFactIDs) {
		t.Fatalf("reasserted activation facts = %#v, want new fact IDs", secondActivation.factIDs())
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestReteRuntimeGraphBetaTokenIdentityIndexesUseFactIdentity(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	threshold := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "candidate",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "candidate-above-threshold",
		Conditions: []RuleConditionSpec{
			{
				Binding: "threshold", Target: TemplateKeyFact(threshold.Key()),
			},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "score"}},
				}, Target: TemplateKeyFact(candidate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("graph-beta-token-identity-indexes"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("graph beta runtime = %#v, want graph beta support", session.rete)
	}
	session.attachPropagationCounters()

	if _, err := session.AssertTemplate(ctx, threshold.Key(), mustFields(t, map[string]any{"group": "A", "score": 10})); err != nil {
		t.Fatalf("AssertTemplate threshold: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, candidate.Key(), mustFields(t, map[string]any{"group": "A", "score": 12})); err != nil {
		t.Fatalf("AssertTemplate candidate 12: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, candidate.Key(), mustFields(t, map[string]any{"group": "A", "score": 13})); err != nil {
		t.Fatalf("AssertTemplate candidate 13: %v", err)
	}
	assertGraphBetaRuntimeParity(t, revision, session)

	snapshot := session.propagationCounterSnapshot()
	if got, want := snapshot.GraphBetaMemory.TokenRows, 7; got != want {
		t.Fatalf("graph token rows = %d, want join inputs, residual rows, and terminal rows %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.IdentityIndexKeys, 7; got != want {
		t.Fatalf("graph identity index keys = %d, want one key per retained token %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.IdentityIndexKeysMax, 2; got != want {
		t.Fatalf("graph identity index keys max = %d, want two keys in right/terminal memories %d", got, want)
	}
	if got, want := snapshot.GraphBetaMemory.FactIndexKeys, 9; got != want {
		t.Fatalf("graph fact index keys = %d, want beta fact rows plus terminal token facts %d", got, want)
	}
}

func TestReteRuntimeGraphBetaRemovalRetractSparseTopology(t *testing.T) {
	ctx := context.Background()
	revision, employeeKey, departmentKey := mustGraphRemovalBenchmarkRuleset(t)
	const retainedPairs = 128

	session := mustGraphRemovalSparseBenchmarkSession(t, revision, employeeKey, departmentKey, retainedPairs)
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	if !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		t.Fatalf("reconcileAgendaInternal: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), retainedPairs+2; got != want {
		t.Fatalf("pending activations before retract = %d, want %d", got, want)
	}

	salesEmployee := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "sales-employee")
	salesDepartment := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Sales")
	targetDepartment := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	keptFacts := []FactID{salesEmployee.ID(), salesDepartment.ID()}
	var keptActivationKey activationKey
	var keptActivationFound bool
	for _, activation := range session.agenda.pendingActivations() {
		if reflect.DeepEqual(cloneActivationFactIDs(&activation), keptFacts) {
			keptActivationKey = activation.key
			keptActivationFound = true
			break
		}
	}
	if !keptActivationFound {
		t.Fatalf("did not find kept activation for fact IDs %#v", keptFacts)
	}

	session.attachPropagationCounters()
	if _, err := session.Retract(ctx, targetDepartment.ID()); err != nil {
		t.Fatalf("Retract(Engineering department): %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), retainedPairs+1; got != want {
		t.Fatalf("pending activations after retract = %d, want %d", got, want)
	}
	keptActivation, ok := session.agenda.activationByKey(keptActivationKey)
	if !ok {
		t.Fatalf("kept activation %v disappeared after sparse retract", keptActivationKey)
	}
	if keptActivation.status != activationStatusPending {
		t.Fatalf("kept activation status = %v, want pending", keptActivation.status)
	}
	if !reflect.DeepEqual(keptActivation.factIDs(), keptFacts) {
		t.Fatalf("kept activation fact IDs = %#v, want %#v", keptActivation.factIDs(), keptFacts)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got, want := snapshot.Totals.TerminalDeltasRemoved, 1; got != want {
		t.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativePropagationEvents, 2; got != want {
		t.Fatalf("negative propagation events = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeRowsRemoved, 1; got != want {
		t.Fatalf("negative rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeTerminalRowsRemoved, 1; got != want {
		t.Fatalf("negative terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 2; got != want {
		t.Fatalf("removal index lookups = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsTouched, 2; got != want {
		t.Fatalf("removal rows touched = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsRemoved, 2; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
}

func mustBetaMemoryRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	noise := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "noise",
		Fields: []FieldSpec{{Name: "bucket", Kind: ValueInt, Required: true}},
	})
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "employee",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}, {Name: "dept", Kind: ValueString, Required: true}},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), noise.Key(), employee.Key(), department.Key()
}

func mustBetaModifyFastPathDeclaredNoReadRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "employee",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), employee.Key(), department.Key()
}

func mustFilterModifyFastPathDeclaredNoReadRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "active-event",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{Binding: "event", Target: TemplateKeyFact(event.Key())}),
			Test{Expression: CompareExpr{
				Operator: ExpressionCompareEqual,
				Left:     BindingFieldExpr{Binding: "event", Field: "status"},
				Right:    ConstExpr{Value: "active"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), event.Key()
}

func mustNegationRuleset(t testing.TB, action func(ActionContext) error) (*Ruleset, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "block",
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
			{Name: "code", Kind: ValueString},
		},
	})
	if action == nil {
		action = func(ActionContext) error { return nil }
	}
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   action,
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "customer-without-active-block",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "customer", Target: TemplateKeyFact(customer.Key())},
			Not{Condition: Match{
				Binding: "block",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(block.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), customer.Key(), block.Key()
}

func mustNegationModifyFastPathDeclaredNoReadRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "customer",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "block",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
			{Name: "code", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "customer-without-active-block",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "customer", Target: TemplateKeyFact(customer.Key())},
			Not{Condition: Match{
				Binding: "block",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(block.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), customer.Key(), block.Key()
}

func mustGraphTopologyRemovalWorkspace(t testing.TB) (*Workspace, TemplateKey, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	employee := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "employee",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}, {Name: "dept", Kind: ValueString, Required: true}},
	})
	department := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "department",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}, {Name: "region", Kind: ValueString, Required: true}, {Name: "office", Kind: ValueString, Required: true}},
	})
	region := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "region",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	office := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "office",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	conditionsA := []RuleConditionSpec{
		{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
		{
			Binding: "department",

			JoinConstraints: []JoinConstraintSpec{
				{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
			}, Target: TemplateKeyFact(department.Key()),
		},
		{
			Binding: "region",

			JoinConstraints: []JoinConstraintSpec{
				{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "department", Field: "region"}},
			}, Target: TemplateKeyFact(region.Key()),
		},
	}
	conditionsB := []RuleConditionSpec{
		{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
		{
			Binding: "department",

			JoinConstraints: []JoinConstraintSpec{
				{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
			}, Target: TemplateKeyFact(department.Key()),
		},
		{
			Binding: "office",

			JoinConstraints: []JoinConstraintSpec{
				{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "department", Field: "office"}},
			}, Target: TemplateKeyFact(office.Key()),
		},
	}
	mustAddRule(t, workspace, RuleSpec{
		Name:       "employee-department-region-a",
		Conditions: conditionsA,
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "employee-department-office",
		Conditions: conditionsB,
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	return workspace, employee.Key(), department.Key(), region.Key(), office.Key()
}

func mustGraphTopologyRemovalRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()

	workspace, employeeKey, departmentKey, regionKey, officeKey := mustGraphTopologyRemovalWorkspace(t)
	return mustCompileWorkspace(t, workspace), employeeKey, departmentKey, regionKey, officeKey
}

func assertGraphTopologyRemovalShape(t *testing.T, revision *Ruleset) {
	t.Helper()

	summary := revision.reteGraphDebugSummary()
	if got, want := len(summary.TerminalNodes), 2; got != want {
		t.Fatalf("terminal nodes = %d, want %d", got, want)
	}
	if got, want := len(summary.BetaNodes), 3; got != want {
		t.Fatalf("beta nodes = %d, want shared first beta plus two branch betas (%d)", got, want)
	}
	shared := reteGraphStageRef{kind: reteGraphStageBeta, id: int(summary.BetaNodes[0].id)}
	if got := summary.BetaNodes[1].left; got != shared {
		t.Fatalf("region branch left input = %#v, want shared first beta %#v", got, shared)
	}
	if got := summary.BetaNodes[2].left; got != shared {
		t.Fatalf("office branch left input = %#v, want shared first beta %#v", got, shared)
	}
}

func mustGraphTopologyRemovalInitialFacts(t testing.TB, employeeKey, departmentKey, regionKey, officeKey TemplateKey) []SessionInitialFact {
	t.Helper()

	return []SessionInitialFact{
		{
			TemplateKey: employeeKey,
			Fields:      mustFields(t, map[string]any{"name": "Ada", "dept": "Engineering"}),
		},
		{
			TemplateKey: departmentKey,
			Fields:      mustFields(t, map[string]any{"id": "Engineering", "region": "East", "office": "HQ"}),
		},
		{
			TemplateKey: regionKey,
			Fields:      mustFields(t, map[string]any{"id": "East"}),
		},
		{
			TemplateKey: officeKey,
			Fields:      mustFields(t, map[string]any{"id": "HQ"}),
		},
	}
}

func mustBetaMemoryInitialFacts(t testing.TB, noiseKey, employeeKey, departmentKey TemplateKey) []SessionInitialFact {
	t.Helper()

	initials := make([]SessionInitialFact, 0, reteMemoryMinimumFacts+3)
	for i := range reteMemoryMinimumFacts {
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
			{Binding: "item", Target: TemplateKeyFact(item.Key())},
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

func terminalDeltaCandidateForFact(t *testing.T, session *Session, deltas []reteTerminalTokenDelta, factID FactID, scratch *candidateScratch) matchCandidate {
	t.Helper()
	if session == nil || session.rete == nil {
		t.Fatal("session has no Rete runtime")
	}
	candidates, err := session.rete.candidatesForTerminalDeltas(deltas, scratch)
	if err != nil {
		t.Fatalf("terminal delta candidates: %v", err)
	}
	for _, candidate := range candidates {
		if slices.Contains(candidate.factIDs, factID) {
			return candidate
		}
	}
	t.Fatalf("terminal delta candidate containing fact %q not found in %#v", factID, candidates)
	return matchCandidate{}
}

func candidateFactIndex(t *testing.T, candidate matchCandidate, factID FactID) int {
	t.Helper()
	for i, id := range candidate.factIDs {
		if id == factID {
			return i
		}
	}
	t.Fatalf("candidate fact %q not found in %#v", factID, candidate.factIDs)
	return -1
}

func activationFactIDForTemplate(t *testing.T, session *Session, activation activation, templateKey TemplateKey) FactID {
	t.Helper()
	if session == nil {
		t.Fatal("missing session")
	}
	for _, id := range activation.factIDs() {
		fact, ok := session.factByID(id)
		if ok && fact.TemplateKey() == templateKey {
			return id
		}
	}
	t.Fatalf("activation %q has no fact for template %q", activation.id, templateKey)
	return FactID{}
}

func mustAlphaMemoryRuleset(t testing.TB, ruleName string, constraints []FieldConstraintSpec) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
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
				Binding: "person",

				FieldConstraints: constraints, Target: TemplateKeyFact(person.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), person.Key()
}

func mustRuntimeGuardRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "active-adult",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				}, Target: TemplateKeyFact(person.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddAdultQuery(t, workspace, person.Key())
	return mustCompileWorkspace(t, workspace), person.Key()
}

func assertNoOracleStyleMatchingSince(t testing.TB, before, after propagationCounterTotals) {
	t.Helper()
	if got := after.OracleStyleMatchRequests - before.OracleStyleMatchRequests; got != 0 {
		t.Fatalf("oracle-style match requests delta = %d, want 0", got)
	}
	if got := after.SteadyStateOracleStyleMatchRequests - before.SteadyStateOracleStyleMatchRequests; got != 0 {
		t.Fatalf("steady-state oracle-style match requests delta = %d, want 0", got)
	}
	if got := after.SteadyStateAgendaReconciles - before.SteadyStateAgendaReconciles; got != 0 {
		t.Fatalf("steady-state agenda reconciles delta = %d, want 0", got)
	}
}

func assertUnsupportedRuntimeDetail(t testing.TB, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("error = %v, want ErrUnsupportedRuntime", err)
	}
	for _, want := range []string{"unsupported runtime", "test unsupported graph plan"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unsupported runtime error %q does not contain %q", err.Error(), want)
		}
	}
}

func mustModifyFastPathRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "active-person",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				}, Target: TemplateKeyFact(person.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), person.Key()
}

func mustModifyFastPathDeclaredNoReadRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "active-person",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				}, Target: TemplateKeyFact(person.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(t, workspace), person.Key()
}

func injectUnsupportedRuntimePlan(t testing.TB, runtime *reteRuntime, ruleName string) {
	t.Helper()
	if runtime == nil {
		t.Fatal("runtime is nil")
	}
	rule, ok := runtime.revision.rules[ruleName]
	if !ok {
		t.Fatalf("rule %q not found", ruleName)
	}
	if len(rule.conditionPlans) == 0 {
		t.Fatalf("rule %q has no conditions", ruleName)
	}
	runtime.plan.betaSupported = false
	runtime.plan.unsupported = []reteUnsupportedReason{{
		ruleID:         rule.id,
		ruleRevisionID: rule.revisionID,
		conditionID:    rule.conditionPlans[0].id,
		binding:        rule.conditionPlans[0].binding,
		kind:           reteUnsupportedMissingTarget,
		detail:         "test unsupported graph plan",
	}}
	runtime.graphBeta = nil
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

func assertGraphBetaAlphaFactCount(t testing.TB, session *Session, ruleName string, conditionIndex int, want int) {
	t.Helper()
	if session == nil || session.rete == nil || session.rete.graphBeta == nil {
		t.Fatal("session has no graph beta memory")
	}
	rule, ok := session.revision.rules[ruleName]
	if !ok {
		t.Fatalf("rule %q not found", ruleName)
	}
	if conditionIndex < 0 || conditionIndex >= len(rule.conditionPlans) {
		t.Fatalf("rule %q condition index %d out of range", ruleName, conditionIndex)
	}
	conditionID := rule.conditionPlans[conditionIndex].id
	if got := session.rete.alphaFactCount(conditionID); got != want {
		t.Fatalf("graph beta alpha fact count for %s condition %d = %d, want %d", ruleName, conditionIndex, got, want)
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
			bindings:         activation.bindings(),
			factIDs:          activation.factIDs(),
			factVersions:     activation.factVersions(),
			path:             activation.path(),
			maxRecency:       activation.maxRecency,
			aggregateRecency: activation.aggregateRecency,
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

func singlePendingActivation(t *testing.T, session *Session) activation {
	t.Helper()
	pending := session.agenda.pendingActivations()
	if got, want := len(pending), 1; got != want {
		t.Fatalf("pending activations = %d, want %d", got, want)
	}
	return pending[0]
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
			bindings:         activation.bindings(),
			factIDs:          activation.factIDs(),
			factVersions:     activation.factVersions(),
			path:             activation.path(),
			maxRecency:       activation.maxRecency,
			aggregateRecency: activation.aggregateRecency,
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
