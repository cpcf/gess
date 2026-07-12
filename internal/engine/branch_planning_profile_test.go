package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBranchPlanningProfileRanksReadyConditions(t *testing.T) {
	profile := &branchPlanningProfile{byRule: map[RuleID]map[string]float64{
		RuleID("profiled-rule"): {
			"slow":   10,
			"medium": 5,
			"fast":   1,
		},
	}}
	ir := newProfiledReorderedBranchPlanningIR("profiled-rule", 0, []normalizedRuleCondition{
		{spec: RuleConditionSpec{Binding: "slow", Target: DynamicFact("slow")}, visible: true},
		{spec: RuleConditionSpec{Binding: "medium", Target: DynamicFact("medium")}, visible: true},
		{spec: RuleConditionSpec{Binding: "fast", Target: DynamicFact("fast")}, visible: true},
	}, profile)

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"fast", "medium", "slow"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profiled condition order = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningProfilePreservesDependenciesAndSafelyCrossesNegation(t *testing.T) {
	profile := &branchPlanningProfile{byRule: map[RuleID]map[string]float64{
		RuleID("profiled-rule"): {
			"root":        1,
			"dependent":   1,
			"independent": 2,
			"blocked":     0,
		},
	}}
	ir := newProfiledReorderedBranchPlanningIR("profiled-rule", 0, []normalizedRuleCondition{
		{spec: RuleConditionSpec{Binding: "root", Target: DynamicFact("root")}, visible: true},
		{spec: RuleConditionSpec{
			Binding: "dependent",
			Target:  DynamicFact("dependent"),
			JoinConstraints: []JoinConstraintSpec{{
				Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"},
			}},
		}, visible: true},
		{spec: RuleConditionSpec{Binding: "independent", Target: DynamicFact("independent")}, visible: true},
		{spec: RuleConditionSpec{Binding: "blocked", Target: DynamicFact("blocked")}, negated: true},
	}, profile)

	if got, want := conditionBindings(ir.normalizedConditions()), []string{"root", "blocked", "dependent", "independent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profiled dependency order = %#v, want %#v", got, want)
	}
}

func TestBranchPlanningProfileKeepsRulesetIdentityAndGraphPlanEvidence(t *testing.T) {
	workspace := newPlanningProfileWorkspace(t)
	baseline, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("baseline Compile: %v", err)
	}
	profile := &branchPlanningProfile{byRule: map[RuleID]map[string]float64{
		RuleID("profiled-rule"): {"first": 10, "second": 1},
	}}
	profiled, err := workspace.compileWithBranchPlanningProfile(context.Background(), profile)
	if err != nil {
		t.Fatalf("profiled Compile: %v", err)
	}
	if baseline.ID() != profiled.ID() {
		t.Fatalf("ruleset identity changed with plan order: baseline=%q profiled=%q", baseline.ID(), profiled.ID())
	}
	baselineRule := baseline.rules["profiled-rule"]
	profiledRule := profiled.rules["profiled-rule"]
	if baselineRule.revisionID != profiledRule.revisionID {
		t.Fatalf("rule revision changed with plan order: baseline=%q profiled=%q", baselineRule.revisionID, profiledRule.revisionID)
	}
	if got, want := conditionPlanBindings(profiledRule.executionConditionBranches()[0].plans), []string{"second", "first"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profiled execution order = %#v, want %#v", got, want)
	}
	var inspection reteGraphBranchInspection
	for _, candidate := range profiled.graph.branchInspections {
		if candidate.RuleName == "profiled-rule" {
			inspection = candidate
			break
		}
	}
	if len(inspection.PlannedOrder) != 2 {
		t.Fatalf("profiled graph plan = %#v, want two planned conditions", inspection.PlannedOrder)
	}
	if got, want := inspection.PlannedOrder[0].Binding, "second"; got != want {
		t.Fatalf("graph selected plan first binding = %q, want %q", got, want)
	}
}

func TestBranchPlanningProfileKeepsNegatedRuleIdentity(t *testing.T) {
	workspace := newNegatedPlanningProfileWorkspace(t)
	baseline, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("baseline Compile: %v", err)
	}
	profile := &branchPlanningProfile{byRule: map[RuleID]map[string]float64{
		RuleID("profiled-negated-rule"): {"first": 10, "second": 1},
	}}
	profiled, err := workspace.compileWithBranchPlanningProfile(context.Background(), profile)
	if err != nil {
		t.Fatalf("profiled Compile: %v", err)
	}
	if baseline.ID() != profiled.ID() {
		t.Fatalf("negated ruleset identity changed with plan order: baseline=%q profiled=%q", baseline.ID(), profiled.ID())
	}
	if baseline.rules["profiled-negated-rule"].revisionID != profiled.rules["profiled-negated-rule"].revisionID {
		t.Fatalf("negated rule revision changed with plan order: baseline=%q profiled=%q", baseline.rules["profiled-negated-rule"].revisionID, profiled.rules["profiled-negated-rule"].revisionID)
	}
	if got, want := conditionPlanBindings(profiled.rules["profiled-negated-rule"].executionConditionBranches()[0].plans), []string{"second", "blocked", "first"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("negated profiled execution order = %#v, want %#v", got, want)
	}
}

func BenchmarkProfileGuidedPlanningLossZones(b *testing.B) {
	b.Run("manners/default", func(b *testing.B) {
		benchmarkMannersPlanningVariant(b, nil)
	})
	b.Run("manners/profiled", func(b *testing.B) {
		benchmarkMannersPlanningVariant(b, mannersPlanningProfile())
	})
	b.Run("steady-state/default", func(b *testing.B) {
		benchmarkSteadyStatePlanningVariant(b, nil)
	})
	b.Run("steady-state/profiled", func(b *testing.B) {
		benchmarkSteadyStatePlanningVariant(b, steadyStatePlanningProfile())
	})
}

func TestMannersFindSeatingPlannerDefersCountPastNegations(t *testing.T) {
	guests := mannersGuests(64)
	initials := mannersInitialFacts(guests)
	planned := mustCompileMannersRuleset(t)
	legacy := mustCompileMannersRulesetWithProfileAndLegacyFindSeatingOrder(t, nil, true)

	if got, want := findSeatingPlannedBindings(t, planned), []string{"ctx", "seat", "g1", "g2", "blockedpath", "blockedchoice", "cnt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned find-seating order = %#v, want %#v", got, want)
	}
	if got, want := findSeatingBetaKinds(t, planned), []reteGraphBetaNodeKind{
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeResidualFilter,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeJoin,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned find-seating beta chain = %#v, want %#v", got, want)
	}
	if got, want := findSeatingPlannedBindings(t, legacy), []string{"ctx", "seat", "g1", "g2", "cnt", "blockedpath", "blockedchoice"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy find-seating order = %#v, want %#v", got, want)
	}
	if got, want := findSeatingBetaKinds(t, legacy), []reteGraphBetaNodeKind{
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeResidualFilter,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeNot,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy find-seating beta chain = %#v, want %#v", got, want)
	}
	for _, plan := range planned.rules["find-seating"].executionConditionBranches()[0].plans {
		t.Logf("plan binding=%q slot=%d negated=%v", plan.binding, plan.bindingSlot, plan.negated)
	}

	plannedResult, plannedLifecycle := collectMannersLifecycleCounters(t, planned, initials, guests)
	legacyResult, legacyLifecycle := collectMannersLifecycleCounters(t, legacy, initials, guests)
	if plannedResult.Fired != 2206 || legacyResult.Fired != 2206 {
		t.Fatalf("fired = (planned %d, legacy %d), want (2206, 2206)", plannedResult.Fired, legacyResult.Fired)
	}
	if plannedResult.Status != legacyResult.Status {
		t.Fatalf("run status = (planned %v, legacy %v), want identical", plannedResult.Status, legacyResult.Status)
	}
	t.Logf("planned lifecycle: %+v", plannedLifecycle.Totals)
	t.Logf("planned token rows by stage: %v", topPropagationStageCounts(plannedLifecycle.TokenRowsByStage, 10))
	t.Logf("legacy lifecycle: %+v", legacyLifecycle.Totals)
	t.Logf("legacy token rows by stage: %v", topPropagationStageCounts(legacyLifecycle.TokenRowsByStage, 10))
}

func BenchmarkMannersFindSeatingBranchOrdering(b *testing.B) {
	for _, variant := range []struct {
		name                       string
		legacyCountBeforeNegations bool
	}{
		{name: "legacy-count-before-negations", legacyCountBeforeNegations: true},
		{name: "planned-default"},
	} {
		b.Run(variant.name, func(b *testing.B) {
			benchmarkMannersFindSeatingBranchOrder(b, variant.legacyCountBeforeNegations)
		})
	}
}

func TestMannersFindSeatingLateContextSpike(t *testing.T) {
	guests := mannersGuests(64)
	initials := mannersInitialFacts(guests)
	production := mustCompileMannersRuleset(t)
	lateContext := mustCompileMannersRulesetWithLateContextFindSeatingOrder(t)

	if got, want := findSeatingPlannedBindings(t, production), []string{"ctx", "seat", "g1", "g2", "blockedpath", "blockedchoice", "cnt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("production find-seating order = %#v, want %#v", got, want)
	}
	if got, want := findSeatingBetaKinds(t, production), []reteGraphBetaNodeKind{
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeResidualFilter,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeJoin,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("production find-seating beta chain = %#v, want %#v", got, want)
	}
	if got, want := findSeatingPlannedBindings(t, lateContext), []string{"seat", "g1", "g2", "blockedpath", "blockedchoice", "ctx", "cnt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("late-context find-seating order = %#v, want %#v", got, want)
	}
	if got, want := findSeatingBetaKinds(t, lateContext), []reteGraphBetaNodeKind{
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeResidualFilter,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeNot,
		reteGraphBetaNodeJoin,
		reteGraphBetaNodeJoin,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("late-context find-seating beta chain = %#v, want %#v", got, want)
	}
	productionSlots := findSeatingBindingSlots(t, production)
	lateContextSlots := findSeatingBindingSlots(t, lateContext)
	if !reflect.DeepEqual(lateContextSlots, productionSlots) {
		t.Fatalf("late-context binding slots = %#v, want production slots %#v", lateContextSlots, productionSlots)
	}
	if want := map[string]int{"ctx": 0, "seat": 1, "g1": 2, "g2": 3, "cnt": 4, "blockedpath": -1, "blockedchoice": -1}; !reflect.DeepEqual(lateContextSlots, want) {
		t.Fatalf("late-context binding slots = %#v, want authored slots %#v", lateContextSlots, want)
	}

	productionRun := observeMannersBranchOrder(t, production, initials, guests)
	lateContextRun := observeMannersBranchOrder(t, lateContext, initials, guests)
	if productionRun.result.Fired != 2206 || lateContextRun.result.Fired != 2206 {
		t.Fatalf("fired = (production %d, late-context %d), want (2206, 2206)", productionRun.result.Fired, lateContextRun.result.Fired)
	}
	if productionRun.result.Status != lateContextRun.result.Status {
		t.Fatalf("run status = (production %v, late-context %v), want identical", productionRun.result.Status, lateContextRun.result.Status)
	}
	if productionRun.finalFacts != lateContextRun.finalFacts {
		t.Fatal("late-context final fact multiset differs from production")
	}
	productionDepthTrace := traceMannersBranchOrderWithStrategy(t, production, initials, guests, StrategyDepth)
	lateContextDepthTrace := traceMannersBranchOrderWithStrategy(t, lateContext, initials, guests, StrategyDepth)
	depthTraceSame := reflect.DeepEqual(lateContextDepthTrace.firedTrace, productionDepthTrace.firedTrace)
	if !depthTraceSame {
		index, productionEntry, lateContextEntry := firstFiredTraceDifference(productionDepthTrace.firedTrace, lateContextDepthTrace.firedTrace)
		t.Logf("depth rule-fired activation trace differs at %d: production=%q late-context=%q", index, productionEntry, lateContextEntry)
	}
	if got, want := terminalModifyLifecycle(lateContextRun.lifecycle), terminalModifyLifecycle(productionRun.lifecycle); got != want {
		t.Fatalf("late-context terminal/modify lifecycle = %+v, want production lifecycle %+v", got, want)
	}
	if lateContextRun.lifecycle.Totals.TokenRowsAllocated >= productionRun.lifecycle.Totals.TokenRowsAllocated {
		t.Fatalf("late-context token rows = %d, want fewer than production %d", lateContextRun.lifecycle.Totals.TokenRowsAllocated, productionRun.lifecycle.Totals.TokenRowsAllocated)
	}

	logMannersBranchOrderObservation(t, "production", production, productionRun)
	logMannersBranchOrderObservation(t, "late-context", lateContext, lateContextRun)

	breadthGuests := mannersGuests(8)
	breadthInitials := mannersInitialFacts(breadthGuests)
	productionBreadth := traceMannersBranchOrderWithStrategy(t, production, breadthInitials, breadthGuests, StrategyBreadth)
	lateContextBreadth := traceMannersBranchOrderWithStrategy(t, lateContext, breadthInitials, breadthGuests, StrategyBreadth)
	if productionBreadth.result != lateContextBreadth.result {
		t.Fatalf("breadth run result = (%+v, %+v), want identical", productionBreadth.result, lateContextBreadth.result)
	}
	if productionBreadth.result.Fired != 2051 {
		t.Fatalf("breadth fired = %d, want 2051", productionBreadth.result.Fired)
	}
	if productionBreadth.finalFacts != lateContextBreadth.finalFacts {
		t.Fatal("breadth final fact multiset differs between production and late-context")
	}
	if got, want := ruleOrderFromFiredTrace(lateContextBreadth.firedTrace), ruleOrderFromFiredTrace(productionBreadth.firedTrace); !reflect.DeepEqual(got, want) {
		index, productionEntry, lateContextEntry := firstFiredTraceDifference(productionBreadth.firedTrace, lateContextBreadth.firedTrace)
		t.Fatalf("breadth rule-fired order differs at %d: production=%q late-context=%q", index, productionEntry, lateContextEntry)
	}
}

func BenchmarkMannersFindSeatingLateContextBranchOrdering(b *testing.B) {
	for _, variant := range []struct {
		name    string
		compile func(testing.TB) *Ruleset
	}{
		{name: "production-default", compile: func(t testing.TB) *Ruleset { return mustCompileMannersRuleset(t) }},
		{name: "late-context", compile: mustCompileMannersRulesetWithLateContextFindSeatingOrder},
	} {
		b.Run(variant.name, func(b *testing.B) {
			benchmarkMannersLateContextBranchOrder(b, variant.compile(b))
		})
	}
}

func benchmarkMannersLateContextBranchOrder(b *testing.B, revision *Ruleset) {
	b.Helper()
	ctx := context.Background()
	guests := mannersGuests(64)
	initials := mannersInitialFacts(guests)
	observation := observeMannersBranchOrder(b, revision, initials, guests)
	b.ReportAllocs()
	b.ResetTimer()

	var result RunResult
	for range b.N {
		b.StopTimer()
		session, err := NewSession(revision, WithInitialFacts(initials...))
		if err != nil {
			b.Fatalf("NewSession: %v", err)
		}
		b.StartTimer()
		result, err = session.Run(ctx)
		b.StopTimer()
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 2206 {
			b.Fatalf("run result = (%v, %d), want (%v, 2206)", result.Status, result.Fired, RunCompleted)
		}
		validateMannersSolution(b, ctx, session, guests)
	}
	b.ReportMetric(float64(result.Fired), "fired/run")
	reportMannersLifecycleMetrics(b, observation.lifecycle)
	reportMannersRetainedMetrics(b, observation)
}

type mannersBranchOrderObservation struct {
	result      RunResult
	lifecycle   propagationCounterSnapshot
	finalFacts  string
	diagnostics reteGraphBetaMemoryDiagnostics
	betaOwner   RuntimeMemoryOwnerDiagnostics
}

func observeMannersBranchOrder(t testing.TB, revision *Ruleset, initials []SessionInitialFact, guests []mannersGuest) mannersBranchOrderObservation {
	t.Helper()
	ctx := context.Background()
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	validateMannersSolution(t, ctx, session, guests)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	runtimeDiagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("RuntimeDiagnostics: %v", err)
	}
	var betaOwner RuntimeMemoryOwnerDiagnostics
	for _, owner := range runtimeDiagnostics.MemoryOwners {
		if owner.Owner == runtimeMemoryOwnerBeta {
			betaOwner = owner
			break
		}
	}
	if betaOwner.Owner == "" {
		t.Fatalf("runtime diagnostics missing beta owner: %#v", runtimeDiagnostics.MemoryOwners)
	}
	return mannersBranchOrderObservation{
		result:      result,
		lifecycle:   session.propagationCounterSnapshot(),
		finalFacts:  canonicalMannersFactMultiset(snapshot),
		diagnostics: session.propagation.runtime.graphBeta.diagnostics(),
		betaOwner:   betaOwner,
	}
}

type mannersBranchOrderTrace struct {
	result     RunResult
	finalFacts string
	firedTrace []string
}

func traceMannersBranchOrderWithStrategy(t testing.TB, revision *Ruleset, initials []SessionInitialFact, guests []mannersGuest, strategy Strategy) mannersBranchOrderTrace {
	t.Helper()
	ctx := context.Background()
	firedTrace := make([]string, 0, 2206)
	listener := EventFunc(func(_ context.Context, event Event) error {
		if event.Type == EventRuleFired {
			firedTrace = append(firedTrace, fmt.Sprintf("%s|%s|%v", event.RuleID, event.ActivationID, event.FactIDs))
		}
		return nil
	})
	session, err := NewSession(
		revision,
		WithInitialFacts(initials...),
		WithStrategy(strategy),
		WithEventListener(listener, ForEventTypes(EventRuleFired)),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	validateMannersSolution(t, ctx, session, guests)
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return mannersBranchOrderTrace{
		result:     result,
		finalFacts: canonicalMannersFactMultiset(snapshot),
		firedTrace: firedTrace,
	}
}

type mannersTerminalModifyLifecycle struct {
	Asserts                      int
	RHSAsserts                   int
	AgendaDeltaApplications      int
	ActivationsStored            int
	TerminalDeltasEmitted        int
	TerminalDeltasRemoved        int
	NegativeTerminalRowsRemoved  int
	TerminalRowsInserted         int
	TerminalRowsDeduped          int
	TerminalRowsRemoved          int
	ModifyCascades               int
	ModifyRawTerminalAdds        int
	ModifyRawTerminalRemoves     int
	ModifyKeptTerminalAdds       int
	ModifyKeptTerminalRemoves    int
	ModifyCoalescedPairs         int
	ModifyDistinctTokenUpdates   int
	ModifySameTokenCancellations int
	TerminalRowsRetained         int
}

func terminalModifyLifecycle(snapshot propagationCounterSnapshot) mannersTerminalModifyLifecycle {
	totals := snapshot.Totals
	return mannersTerminalModifyLifecycle{
		Asserts:                      totals.Asserts,
		RHSAsserts:                   totals.RHSAsserts,
		AgendaDeltaApplications:      totals.AgendaDeltaApplications,
		ActivationsStored:            totals.ActivationsStored,
		TerminalDeltasEmitted:        totals.TerminalDeltasEmitted,
		TerminalDeltasRemoved:        totals.TerminalDeltasRemoved,
		NegativeTerminalRowsRemoved:  totals.NegativeTerminalRowsRemoved,
		TerminalRowsInserted:         totals.TerminalRowsInserted,
		TerminalRowsDeduped:          totals.TerminalRowsDeduped,
		TerminalRowsRemoved:          totals.TerminalRowsRemoved,
		ModifyCascades:               totals.ModifyCascades,
		ModifyRawTerminalAdds:        totals.ModifyRawTerminalAdds,
		ModifyRawTerminalRemoves:     totals.ModifyRawTerminalRemoves,
		ModifyKeptTerminalAdds:       totals.ModifyKeptTerminalAdds,
		ModifyKeptTerminalRemoves:    totals.ModifyKeptTerminalRemoves,
		ModifyCoalescedPairs:         totals.ModifyCoalescedPairs,
		ModifyDistinctTokenUpdates:   totals.ModifyDistinctTokenUpdates,
		ModifySameTokenCancellations: totals.ModifySameTokenCancellations,
		TerminalRowsRetained:         snapshot.TerminalRowsRetained,
	}
}

func canonicalMannersFactMultiset(snapshot Snapshot) string {
	facts := snapshot.Facts()
	encoded := make([]string, 0, len(facts))
	for _, fact := range facts {
		fields := fact.Fields()
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		slices.Sort(names)
		var out strings.Builder
		fmt.Fprintf(&out, "%s|%s", fact.TemplateKey(), fact.Name())
		for _, name := range names {
			fmt.Fprintf(&out, "|%s=%s", name, fields[name].String())
		}
		encoded = append(encoded, out.String())
	}
	slices.Sort(encoded)
	return strings.Join(encoded, "\n")
}

func ruleOrderFromFiredTrace(trace []string) []string {
	out := make([]string, len(trace))
	for i, entry := range trace {
		out[i], _, _ = strings.Cut(entry, "|")
	}
	return out
}

func firstFiredTraceDifference(left, right []string) (int, string, string) {
	limit := min(len(left), len(right))
	for i := range limit {
		if left[i] != right[i] {
			return i, left[i], right[i]
		}
	}
	if len(left) == len(right) {
		return -1, "", ""
	}
	var leftEntry, rightEntry string
	if limit < len(left) {
		leftEntry = left[limit]
	}
	if limit < len(right) {
		rightEntry = right[limit]
	}
	return limit, leftEntry, rightEntry
}

func reportMannersRetainedMetrics(b *testing.B, observation mannersBranchOrderObservation) {
	b.Helper()
	memory := observation.lifecycle.GraphBetaMemory
	b.ReportMetric(float64(memory.TokenRows), "retained-beta-token-rows")
	b.ReportMetric(float64(memory.TokenRowCapacity), "retained-beta-token-capacity")
	b.ReportMetric(float64(memory.TokenRowReserve), "retained-beta-token-reserve")
	b.ReportMetric(float64(memory.JoinIndexKeys), "retained-beta-join-index-keys")
	b.ReportMetric(float64(memory.JoinIndexReserve), "retained-beta-join-index-reserve")
	b.ReportMetric(float64(memory.IdentityIndexKeys), "retained-beta-identity-index-keys")
	b.ReportMetric(float64(memory.IdentityIndexReserve), "retained-beta-identity-index-reserve")
	b.ReportMetric(float64(memory.FactIndexKeys), "retained-beta-fact-index-keys")
	b.ReportMetric(float64(memory.FactIndexReserve), "retained-beta-fact-index-reserve")
	b.ReportMetric(float64(observation.betaOwner.Rows), "estimated-structural-beta-rows")
	b.ReportMetric(float64(observation.betaOwner.Buckets), "estimated-structural-beta-buckets")
	b.ReportMetric(float64(observation.betaOwner.Indexes), "estimated-structural-beta-indexes")
	b.ReportMetric(float64(observation.betaOwner.Bytes), "estimated-structural-beta-bytes")
	b.ReportMetric(float64(observation.betaOwner.HighWater), "estimated-structural-beta-high-water")
	b.ReportMetric(float64(observation.diagnostics.MaxBetaRows), "max-beta-node-rows")
	b.ReportMetric(float64(observation.diagnostics.MaxBetaJoinIndexKeys), "max-beta-node-join-index-keys")
	b.ReportMetric(float64(observation.diagnostics.MaxBetaJoinBucketDepth), "max-beta-node-join-bucket-depth")
}

func logMannersBranchOrderObservation(t testing.TB, label string, revision *Ruleset, observation mannersBranchOrderObservation) {
	t.Helper()
	t.Logf("%s terminal/modify lifecycle: %+v", label, terminalModifyLifecycle(observation.lifecycle))
	t.Logf("%s lifecycle totals: %+v", label, observation.lifecycle.Totals)
	t.Logf("%s token rows by stage: %v", label, topPropagationStageCounts(observation.lifecycle.TokenRowsByStage, -1))
	t.Logf("%s token rows by source: %v", label, topPropagationTokenRowSourceCounts(observation.lifecycle.TokenRowsBySource, -1))
	t.Logf("%s retained beta stats: %+v", label, observation.lifecycle.GraphBetaMemory)
	t.Logf("%s estimated structural beta owner (excludes token arena batches): %+v", label, observation.betaOwner)
	diagnosticsByID := make(map[reteGraphBetaNodeID]reteGraphBetaNodeMemoryDiagnostics, len(observation.diagnostics.BetaNodes))
	for _, diagnostic := range observation.diagnostics.BetaNodes {
		diagnosticsByID[diagnostic.ID] = diagnostic
	}
	retained := make([]reteGraphBetaNodeMemoryDiagnostics, 0, 7)
	for _, id := range findSeatingBetaNodeIDs(t, revision) {
		retained = append(retained, diagnosticsByID[id])
	}
	t.Logf("%s find-seating retained beta nodes: %+v", label, retained)
}

func benchmarkMannersFindSeatingBranchOrder(b *testing.B, legacyCountBeforeNegations bool) {
	b.Helper()
	ctx := context.Background()
	guests := mannersGuests(64)
	initials := mannersInitialFacts(guests)
	revision := mustCompileMannersRulesetWithProfileAndLegacyFindSeatingOrder(b, nil, legacyCountBeforeNegations)
	result, lifecycle := collectMannersLifecycleCounters(b, revision, initials, guests)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		session, err := NewSession(revision, WithInitialFacts(initials...))
		if err != nil {
			b.Fatalf("NewSession: %v", err)
		}
		b.StartTimer()
		result, err = session.Run(ctx)
		b.StopTimer()
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 2206 {
			b.Fatalf("run result = (%v, %d), want (%v, 2206)", result.Status, result.Fired, RunCompleted)
		}
		validateMannersSolution(b, ctx, session, guests)
	}
	b.ReportMetric(float64(result.Fired), "fired/run")
	reportMannersLifecycleMetrics(b, lifecycle)
}

func findSeatingPlannedBindings(t testing.TB, revision *Ruleset) []string {
	t.Helper()
	for _, inspection := range revision.graph.branchInspections {
		if inspection.RuleName == "find-seating" {
			bindings := make([]string, len(inspection.PlannedOrder))
			for i, condition := range inspection.PlannedOrder {
				bindings[i] = condition.Binding
			}
			return bindings
		}
	}
	t.Fatal("find-seating branch inspection not found")
	return nil
}

func findSeatingBindingSlots(t testing.TB, revision *Ruleset) map[string]int {
	t.Helper()
	for _, inspection := range revision.graph.branchInspections {
		if inspection.RuleName != "find-seating" {
			continue
		}
		slots := make(map[string]int, len(inspection.PlannedOrder))
		for _, condition := range inspection.PlannedOrder {
			slots[condition.Binding] = condition.BindingSlot
		}
		return slots
	}
	t.Fatal("find-seating branch inspection not found")
	return nil
}

func findSeatingBetaKinds(t testing.TB, revision *Ruleset) []reteGraphBetaNodeKind {
	t.Helper()
	ids := findSeatingBetaNodeIDs(t, revision)
	kinds := make([]reteGraphBetaNodeKind, len(ids))
	for i, id := range ids {
		node := revision.graph.betaNode(id)
		if node == nil {
			t.Fatalf("find-seating beta node %d not found", id)
		}
		kinds[i] = node.kind
	}
	return kinds
}

func findSeatingBetaNodeIDs(t testing.TB, revision *Ruleset) []reteGraphBetaNodeID {
	t.Helper()
	var terminalID reteGraphTerminalNodeID
	for _, inspection := range revision.graph.branchInspections {
		if inspection.RuleName == "find-seating" {
			terminalID = inspection.TerminalID
			break
		}
	}
	if terminalID == 0 || int(terminalID) > len(revision.graph.terminalNodes) {
		t.Fatalf("find-seating terminal = %d, want a valid terminal", terminalID)
	}
	stage := revision.graph.terminalNodes[int(terminalID)-1].input
	reversed := make([]reteGraphBetaNodeID, 0, 7)
	for stage.kind == reteGraphStageBeta {
		id := reteGraphBetaNodeID(stage.id)
		node := revision.graph.betaNode(id)
		if node == nil {
			t.Fatalf("find-seating beta node %d not found", stage.id)
		}
		reversed = append(reversed, id)
		stage = node.left
	}
	ids := make([]reteGraphBetaNodeID, len(reversed))
	for i := range reversed {
		ids[len(reversed)-1-i] = reversed[i]
	}
	return ids
}

func benchmarkMannersPlanningVariant(b *testing.B, profile *branchPlanningProfile) {
	b.Helper()
	ctx := context.Background()
	guests := mannersGuests(64)
	revision := mustCompileMannersRulesetWithProfile(b, profile)
	initials := mannersInitialFacts(guests)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		session, err := NewSession(revision, WithInitialFacts(initials...))
		if err != nil {
			b.Fatalf("NewSession: %v", err)
		}
		b.StartTimer()
		result, err := session.Run(ctx)
		b.StopTimer()
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			b.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		validateMannersSolution(b, ctx, session, guests)
	}
}

func benchmarkSteadyStatePlanningVariant(b *testing.B, profile *branchPlanningProfile) {
	b.Helper()
	ctx := context.Background()
	tc := steadyStateScalingCase{streams: 8, limit: 64}
	revision := mustCompileSteadyStateScalingRulesetWithProfile(b, tc, profile)
	expectedFired := tc.streams * (4*tc.limit + 5)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		session := mustSeedSteadyStateScalingSession(b, revision, tc)
		b.StartTimer()
		result, err := session.Run(ctx)
		b.StopTimer()
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != expectedFired {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, expectedFired)
		}
		if got := session.factCount(); got != 2096 {
			b.Fatalf("final fact count = %d, want 2096", got)
		}
		assertSteadyStateFactMix(b, session, tc)
	}
}

func mannersPlanningProfile() *branchPlanningProfile {
	return &branchPlanningProfile{byRule: map[RuleID]map[string]float64{
		RuleID("assign-first-seat"): {
			"ctx": 1,
			"g":   192,
			"cnt": 1,
		},
	}}
}

func steadyStatePlanningProfile() *branchPlanningProfile {
	byRule := make(map[RuleID]map[string]float64)
	for stream := range 8 {
		byRule[RuleID(fmt.Sprintf("advance-stream-%03d", stream))] = map[string]float64{"step": 1}
		byRule[RuleID(fmt.Sprintf("signal-stream-%03d", stream))] = map[string]float64{"step": 1}
		byRule[RuleID(fmt.Sprintf("finish-stream-%03d", stream))] = map[string]float64{"decision": 1}
	}
	return &branchPlanningProfile{byRule: byRule}
}

func newPlanningProfileWorkspace(t testing.TB) *Workspace {
	t.Helper()
	workspace := NewWorkspace()
	first := mustAddTemplate(t, workspace, TemplateSpec{Name: "first", Fields: []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}}})
	second := mustAddTemplate(t, workspace, TemplateSpec{Name: "second", Fields: []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}}})
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "profiled-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "first", Target: TemplateKeyFact(first.Key())},
			{Binding: "second", Target: TemplateKeyFact(second.Key())},
		},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	return workspace
}

func newNegatedPlanningProfileWorkspace(t testing.TB) *Workspace {
	t.Helper()
	workspace := NewWorkspace()
	first := mustAddTemplate(t, workspace, TemplateSpec{Name: "negated-first", Fields: []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}}})
	second := mustAddTemplate(t, workspace, TemplateSpec{Name: "negated-second", Fields: []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}}})
	blocked := mustAddTemplate(t, workspace, TemplateSpec{Name: "negated-blocked", Fields: []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}}})
	mustAddAction(t, workspace, ActionSpec{Name: "record-negated", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "profiled-negated-rule",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "first", Target: TemplateKeyFact(first.Key())},
			Match{Binding: "second", Target: TemplateKeyFact(second.Key())},
			Not{Condition: Match{Binding: "blocked", Target: TemplateKeyFact(blocked.Key())}},
		}},
		Actions: []RuleActionSpec{{Name: "record-negated"}},
	})
	return workspace
}

func mustCompileWorkspaceWithBranchPlanningProfile(t testing.TB, workspace *Workspace, profile *branchPlanningProfile) *Ruleset {
	t.Helper()
	if profile == nil {
		return mustCompileWorkspace(t, workspace)
	}
	revision, err := workspace.compileWithBranchPlanningProfile(context.Background(), profile)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}

func conditionPlanBindings(plans []compiledConditionPlan) []string {
	out := make([]string, len(plans))
	for i, plan := range plans {
		out[i] = plan.binding
	}
	return out
}
