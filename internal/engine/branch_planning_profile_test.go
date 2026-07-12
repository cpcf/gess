package engine

import (
	"context"
	"fmt"
	"reflect"
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

func findSeatingBetaKinds(t testing.TB, revision *Ruleset) []reteGraphBetaNodeKind {
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
	reversed := make([]reteGraphBetaNodeKind, 0, 7)
	for stage.kind == reteGraphStageBeta {
		node := revision.graph.betaNode(reteGraphBetaNodeID(stage.id))
		if node == nil {
			t.Fatalf("find-seating beta node %d not found", stage.id)
		}
		reversed = append(reversed, node.kind)
		stage = node.left
	}
	kinds := make([]reteGraphBetaNodeKind, len(reversed))
	for i := range reversed {
		kinds[len(reversed)-1-i] = reversed[i]
	}
	return kinds
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
