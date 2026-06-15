package gess

import (
	"context"
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
	if session.rete == nil || session.rete.alpha == nil || session.rete.beta == nil {
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
	if session.rete.beta == nil {
		t.Fatal("expected beta memory to be initialized")
	}
	betaMemory := session.rete.beta

	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)

	if _, err := session.AssertTemplate(ctx, employeeKey, mustFields(t, map[string]any{"name": "Ben", "dept": "Sales"})); err != nil {
		t.Fatalf("AssertTemplate(Ben): %v", err)
	}
	if session.rete.beta != betaMemory {
		t.Fatal("assert rebuilt beta memory, want incremental update")
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)

	employee := mustSessionFactByTemplateAndField(t, session, employeeKey, "name", "Ada")
	if _, err := session.Modify(ctx, employee.ID(), FactPatch{Set: mustFields(t, map[string]any{"dept": "Sales"})}); err != nil {
		t.Fatalf("Modify(Ada): %v", err)
	}
	if session.rete.beta != betaMemory {
		t.Fatal("modify rebuilt beta memory, want incremental update")
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)

	salesDepartment := mustSessionFactByTemplateAndField(t, session, departmentKey, "id", "Sales")
	if _, err := session.Retract(ctx, salesDepartment.ID()); err != nil {
		t.Fatalf("Retract(Sales department): %v", err)
	}
	if session.rete.beta != betaMemory {
		t.Fatal("retract rebuilt beta memory, want incremental update")
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)

	resetResult, err := session.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if resetResult.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", resetResult.Status, ResetApplied)
	}
	assertMatcherParity(t, revision1, mustSnapshot(t, ctx, session), newNaiveMatcher(revision1), session.rete)

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
	if session.rete == nil || session.rete.alpha == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after initial reset = %#v, want populated memories", session.rete)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	betaRuleMemory := session.rete.beta.rules[rule.revisionID]
	key := betaJoinKey{kind: betaJoinKeyString, stringValue: "Engineering"}
	before := betaRuleMemory.prefixIndexes[0][key]
	if got, want := len(before), 2; got != want {
		t.Fatalf("prefix bucket len before reset = %d, want %d", got, want)
	}
	if got := len(betaRuleMemory.prefixes[1]); got == 0 {
		t.Fatal("expected joined beta prefixes before reset")
	}
	assertBetaPrefixMatchesUseBacking(t, betaRuleMemory, 1)
	beforePtr := reflect.ValueOf(before).Pointer()
	beforeCap := cap(before)

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.rete == nil || session.rete.beta == nil {
		t.Fatalf("beta memory after reset = %#v, want populated memories", session.rete)
	}
	after := session.rete.beta.rules[rule.revisionID].prefixIndexes[0][key]
	if got, want := len(after), 2; got != want {
		t.Fatalf("prefix bucket len after reset = %d, want %d", got, want)
	}
	if got := reflect.ValueOf(after).Pointer(); got != beforePtr {
		t.Fatalf("prefix bucket backing array changed across reset: got %#x want %#x", got, beforePtr)
	}
	if got := cap(after); got != beforeCap {
		t.Fatalf("prefix bucket capacity changed across reset: got %d want %d", got, beforeCap)
	}
	assertBetaPrefixMatchesUseBacking(t, session.rete.beta.rules[rule.revisionID], 1)
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

	matches1, err := betaRuleMemory.matchesForLeftPrefix(1, betaRuleMemory.prefixes[0][0])
	if err != nil {
		t.Fatalf("matchesForLeftPrefix first call: %v", err)
	}
	matches2, err := betaRuleMemory.matchesForLeftPrefix(1, betaRuleMemory.prefixes[0][0])
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

func assertBetaPrefixMatchesUseBacking(t testing.TB, memory *reteBetaRuleMemory, conditionIndex int) {
	t.Helper()
	if memory == nil {
		t.Fatal("missing beta rule memory")
	}
	if conditionIndex < 0 || conditionIndex >= len(memory.prefixes) || conditionIndex >= len(memory.prefixBacking) {
		t.Fatalf("condition index %d out of range", conditionIndex)
	}
	chunks := memory.prefixBacking[conditionIndex]
	if len(chunks) == 0 {
		t.Fatalf("prefix backing for condition %d is empty", conditionIndex)
	}
	for i, prefix := range memory.prefixes[conditionIndex] {
		if len(prefix.matches) == 0 {
			t.Fatalf("prefix %d has empty matches", i)
		}
		if cap(prefix.matches) != len(prefix.matches) {
			t.Fatalf("prefix %d matches capacity = %d, want %d", i, cap(prefix.matches), len(prefix.matches))
		}
		if !conditionMatchesInAnyChunk(prefix.matches, chunks) {
			t.Fatalf("prefix %d matches are outside backing storage", i)
		}
	}
}

func conditionMatchesInAnyChunk(matches []conditionMatch, chunks [][]conditionMatch) bool {
	matchStart := sliceDataPtr(matches)
	matchEnd := matchStart + uintptr(len(matches))*unsafe.Sizeof(matches[0])
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		chunkStart := sliceDataPtr(chunk)
		chunkEnd := chunkStart + uintptr(len(chunk))*unsafe.Sizeof(chunk[0])
		if matchStart >= chunkStart && matchEnd <= chunkEnd {
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
