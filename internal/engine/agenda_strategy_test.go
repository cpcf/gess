package engine

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestSessionStrategyDepthPreservesDefaultOrdering(t *testing.T) {
	want := []string{"urgent:0", "task:3", "task:2", "task:1", "decl-first:9", "decl-second:9"}

	for _, tc := range []struct {
		name string
		opts []SessionOption
	}{
		{name: "default"},
		{name: "explicit-depth", opts: []SessionOption{WithStrategy(StrategyDepth)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runStrategyOrderScenario(t, tc.name, tc.opts...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trace = %#v, want %#v", got, want)
			}
		})
	}
}

func TestSessionStrategyBreadthOrdersEqualSalienceByCreation(t *testing.T) {
	want := []string{"urgent:0", "task:1", "task:2", "task:3", "decl-first:9", "decl-second:9"}
	got := runStrategyOrderScenario(t, "breadth", WithStrategy(StrategyBreadth))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %#v, want %#v", got, want)
	}
}

func TestSessionStrategyBreadthActivationEventsCanonicalAcrossPlanOrders(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "tag", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{Name: "right"})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	const ruleName = "pair"
	mustAddRule(t, workspace, RuleSpec{
		Name: ruleName,
		Conditions: []RuleConditionSpec{
			{Binding: "left", Target: TemplateKeyFact(left.Key())},
			{Binding: "right", Target: TemplateKeyFact(right.Key())},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})

	production, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("production Compile: %v", err)
	}
	profiled, err := workspace.compileWithBranchPlanningProfile(ctx, &branchPlanningProfile{
		byRule: map[RuleID]map[string]float64{
			RuleID(ruleName): {"left": 10, "right": 1},
		},
	})
	if err != nil {
		t.Fatalf("profiled Compile: %v", err)
	}
	productionOrder := conditionPlanBindings(production.rules[ruleName].executionConditionBranches()[0].plans)
	profiledOrder := conditionPlanBindings(profiled.rules[ruleName].executionConditionBranches()[0].plans)
	if reflect.DeepEqual(productionOrder, profiledOrder) {
		t.Fatalf("planned bindings matched unexpectedly: production=%#v profiled=%#v", productionOrder, profiledOrder)
	}

	observe := func(t *testing.T, revision *Ruleset, id SessionID) ([]string, []string, []string) {
		t.Helper()
		leftTagByID := make(map[FactID]string, 2)
		activatedTags := make([]string, 0, 2)
		deactivatedTags := make([]string, 0, 2)
		listener := EventFunc(func(_ context.Context, event Event) error {
			if len(event.FactIDs) == 0 {
				return nil
			}
			switch event.Type {
			case EventRuleActivated:
				activatedTags = append(activatedTags, leftTagByID[event.FactIDs[0]])
			case EventRuleDeactivated:
				deactivatedTags = append(deactivatedTags, leftTagByID[event.FactIDs[0]])
			}
			return nil
		})
		session, err := NewSession(revision, WithSessionID(id), WithStrategy(StrategyBreadth), WithEventListener(listener, ForEventTypes(EventRuleActivated, EventRuleDeactivated)))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		for _, leftFact := range []struct {
			tag string
		}{
			{tag: "b"},
			{tag: "a"},
		} {
			result, err := session.Assert(ctx, left.Key(), Fields{
				"id":  newIntValue(1),
				"tag": newStringValue(leftFact.tag),
			})
			if err != nil {
				t.Fatalf("Assert(left %q): %v", leftFact.tag, err)
			}
			leftTagByID[result.Fact.ID()] = leftFact.tag
		}
		rightFact, err := session.Assert(ctx, right.Key(), Fields{})
		if err != nil {
			t.Fatalf("Assert(right): %v", err)
		}
		agenda, err := session.Agenda(ctx)
		if err != nil {
			t.Fatalf("Agenda: %v", err)
		}
		pending := agenda.Activations()
		pendingTags := make([]string, 0, len(pending))
		for _, act := range pending {
			factIDs := act.FactIDs()
			if len(factIDs) == 0 {
				t.Fatalf("pending activation missing fact IDs: %#v", act)
			}
			pendingTags = append(pendingTags, leftTagByID[factIDs[0]])
		}
		if _, err := session.Retract(ctx, rightFact.Fact.ID()); err != nil {
			t.Fatalf("Retract(right): %v", err)
		}
		return activatedTags, deactivatedTags, pendingTags
	}

	productionActivated, productionDeactivated, productionPending := observe(t, production, "breadth-event-production")
	profiledActivated, profiledDeactivated, profiledPending := observe(t, profiled, "breadth-event-profiled")
	if !reflect.DeepEqual(profiledActivated, productionActivated) {
		t.Fatalf("activated tag order = %#v, want %#v", profiledActivated, productionActivated)
	}
	if !reflect.DeepEqual(profiledDeactivated, productionDeactivated) {
		t.Fatalf("deactivated tag order = %#v, want %#v", profiledDeactivated, productionDeactivated)
	}
	if !reflect.DeepEqual(profiledPending, productionPending) {
		t.Fatalf("pending tag order = %#v, want %#v", profiledPending, productionPending)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(productionActivated, want) {
		t.Fatalf("production activated tag order = %#v, want %#v", productionActivated, want)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(productionDeactivated, want) {
		t.Fatalf("production deactivated tag order = %#v, want %#v", productionDeactivated, want)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(productionPending, want) {
		t.Fatalf("production pending tag order = %#v, want %#v", productionPending, want)
	}
}

func TestSessionStrategyOrderingIsDeterministic(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []SessionOption
	}{
		{name: "depth", opts: []SessionOption{WithStrategy(StrategyDepth)}},
		{name: "breadth", opts: []SessionOption{WithStrategy(StrategyBreadth)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := runStrategyOrderScenario(t, tc.name+"-first", tc.opts...)
			for i := range 3 {
				got := runStrategyOrderScenario(t, fmt.Sprintf("%s-repeat-%d", tc.name, i), tc.opts...)
				if !reflect.DeepEqual(got, first) {
					t.Fatalf("run %d trace = %#v, want %#v", i, got, first)
				}
			}
		})
	}
}

func TestSessionStrategyBreadthComposesWithFocusAndAutoFocus(t *testing.T) {
	ctx := context.Background()
	autoFocus := true
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask", AutoFocus: &autoFocus})
	mainEvent := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "main-event",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})
	askEvent := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "ask-event",
		Module: "ask",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})

	trace := make([]string, 0, 4)
	mustAddAction(t, workspace, strategyTraceAction("main", "event", "seq", &trace))
	mustAddAction(t, workspace, strategyTraceAction("ask", "event", "seq", &trace))
	mustAddRule(t, workspace, RuleSpec{
		Name:       "main-rule",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(mainEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "main"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "ask-rule",
		Module:     "ask",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(askEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "ask"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustStrategySession(t, revision, "breadth-focus-auto", WithStrategy(StrategyBreadth))
	for _, assertion := range []struct {
		key TemplateKey
		seq int64
	}{
		{key: mainEvent.Key(), seq: 1},
		{key: mainEvent.Key(), seq: 2},
		{key: askEvent.Key(), seq: 1},
		{key: askEvent.Key(), seq: 2},
	} {
		if err := session.AssertTemplateValues(ctx, assertion.key, newIntValue(assertion.seq)); err != nil {
			t.Fatalf("AssertTemplateValues(%s, %d): %v", assertion.key, assertion.seq, err)
		}
	}
	if got := session.CurrentFocus(); got != "ask" {
		t.Fatalf("current focus = %q, want ask", got)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 4 {
		t.Fatalf("run result = (%v, %d), want (%v, 4)", result.Status, result.Fired, RunCompleted)
	}
	want := []string{"ask:1", "ask:2", "main:1", "main:2"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %#v, want %#v", trace, want)
	}
}

func TestSessionStrategyBreadthAutoFocusUsesCanonicalBirthOrder(t *testing.T) {
	ctx := context.Background()
	autoFocus := true
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "low", AutoFocus: &autoFocus})
	mustAddModule(t, workspace, ModuleSpec{Name: "high", AutoFocus: &autoFocus})
	event := mustAddTemplate(t, workspace, TemplateSpec{Name: "event"})

	trace := make([]string, 0, 2)
	mustAddAction(t, workspace, ActionSpec{Name: "low", Fn: func(ActionContext) error {
		trace = append(trace, "low")
		return nil
	}})
	mustAddAction(t, workspace, ActionSpec{Name: "high", Fn: func(ActionContext) error {
		trace = append(trace, "high")
		return nil
	}})
	// Declaration order is the canonical peer-birth order: low is born first,
	// then high. Salience controls each module's agenda, not auto-focus pushes.
	mustAddRule(t, workspace, RuleSpec{
		Name:       "low-rule",
		Module:     "low",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(event.Key())}},
		Actions:    []RuleActionSpec{{Name: "low"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "high-rule",
		Module:     "high",
		Salience:   10,
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(event.Key())}},
		Actions:    []RuleActionSpec{{Name: "high"}},
	})

	session := mustStrategySession(t, mustCompileWorkspace(t, workspace), "breadth-autofocus-birth", WithStrategy(StrategyBreadth))
	if _, err := session.Assert(ctx, event.Key(), Fields{}); err != nil {
		t.Fatalf("Assert(event): %v", err)
	}
	if got, want := session.CurrentFocus(), ModuleName("high"); got != want {
		t.Fatalf("current focus = %q, want %q", got, want)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}
	if want := []string{"high", "low"}; !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %#v, want %#v", trace, want)
	}
}

func TestSessionStrategyBreadthComposesWithHaltFireLimitAndRefraction(t *testing.T) {
	t.Run("halt", func(t *testing.T) {
		revision, taskKey, trace := mustStrategyTaskRevision(t, func(ctx ActionContext, seq int64) error {
			if seq == 1 {
				return ctx.Halt()
			}
			return nil
		})
		session := mustStrategySession(t, revision, "breadth-halt", WithStrategy(StrategyBreadth))
		assertStrategyTasks(t, session, taskKey, 1, 2)

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunHalted || result.Fired != 1 {
			t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunHalted)
		}
		if want := []string{"task:1"}; !reflect.DeepEqual(*trace, want) {
			t.Fatalf("trace = %#v, want %#v", *trace, want)
		}
		if got := len(session.agendaDriver.agenda.pendingActivations()); got != 1 {
			t.Fatalf("pending activations = %d, want 1", got)
		}
	})

	t.Run("fire-limit-refraction", func(t *testing.T) {
		revision, taskKey, trace := mustStrategyTaskRevision(t, nil)
		session := mustStrategySession(t, revision, "breadth-fire-limit", WithStrategy(StrategyBreadth))
		assertStrategyTasks(t, session, taskKey, 1, 2)

		result, err := session.Run(context.Background(), WithMaxFirings(1))
		if err != nil {
			t.Fatalf("limited Run: %v", err)
		}
		if result.Status != RunFireLimit || result.Fired != 1 {
			t.Fatalf("limited run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunFireLimit)
		}
		if want := []string{"task:1"}; !reflect.DeepEqual(*trace, want) {
			t.Fatalf("trace after limited run = %#v, want %#v", *trace, want)
		}

		result, err = session.Run(context.Background())
		if err != nil {
			t.Fatalf("resume Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 1 {
			t.Fatalf("resume run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
		}
		if want := []string{"task:1", "task:2"}; !reflect.DeepEqual(*trace, want) {
			t.Fatalf("trace after resume = %#v, want %#v", *trace, want)
		}

		result, err = session.Run(context.Background())
		if err != nil {
			t.Fatalf("refraction Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 0 {
			t.Fatalf("refraction run result = (%v, %d), want (%v, 0)", result.Status, result.Fired, RunCompleted)
		}
	})
}

func TestSessionWithStrategyRejectsUnknownStrategy(t *testing.T) {
	workspace := NewWorkspace()
	mustAddTemplate(t, workspace, TemplateSpec{Name: "event"})
	revision := mustCompileWorkspace(t, workspace)
	if _, err := NewSession(revision, WithStrategy(Strategy(99))); err == nil {
		t.Fatal("NewSession succeeded with invalid strategy")
	}
}

func runStrategyOrderScenario(t testing.TB, id string, opts ...SessionOption) []string {
	t.Helper()
	ctx := context.Background()
	workspace := NewWorkspace()
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "seq", Kind: ValueInt, Required: true},
		},
	})

	trace := make([]string, 0, 6)
	for _, name := range []string{"urgent", "task", "decl-first", "decl-second"} {
		mustAddAction(t, workspace, strategyTraceAction(name, "item", "seq", &trace))
	}
	mustAddRule(t, workspace, strategyRule("urgent", 30, task.Key(), "urgent", "urgent"))
	mustAddRule(t, workspace, strategyRule("task", 10, task.Key(), "task", "task"))
	mustAddRule(t, workspace, strategyRule("decl-first", 5, task.Key(), "tie", "decl-first"))
	mustAddRule(t, workspace, strategyRule("decl-second", 5, task.Key(), "tie", "decl-second"))

	revision := mustCompileWorkspace(t, workspace)
	session := mustStrategySession(t, revision, SessionID("strategy-"+id), opts...)
	for _, assertion := range []struct {
		kind string
		seq  int64
	}{
		{kind: "task", seq: 1},
		{kind: "task", seq: 2},
		{kind: "task", seq: 3},
		{kind: "tie", seq: 9},
		{kind: "urgent", seq: 0},
	} {
		if _, err := session.Assert(ctx, task.Key(), Fields{
			"kind": newStringValue(assertion.kind),
			"seq":  newIntValue(assertion.seq),
		}); err != nil {
			t.Fatalf("Assert(%s:%d): %v", assertion.kind, assertion.seq, err)
		}
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 6 {
		t.Fatalf("run result = (%v, %d), want (%v, 6)", result.Status, result.Fired, RunCompleted)
	}
	return trace
}

func mustStrategyTaskRevision(t testing.TB, after func(ActionContext, int64) error) (*Ruleset, TemplateKey, *[]string) {
	t.Helper()
	workspace := NewWorkspace()
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "task",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})
	trace := make([]string, 0, 2)
	mustAddAction(t, workspace, ActionSpec{
		Name: "task",
		Fn: func(ctx ActionContext) error {
			seq := strategyBindingInt(ctx, "item", "seq")
			trace = append(trace, fmt.Sprintf("task:%d", seq))
			if after != nil {
				return after(ctx, seq)
			}
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "task",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(task.Key())}},
		Actions:    []RuleActionSpec{{Name: "task"}},
	})
	return mustCompileWorkspace(t, workspace), task.Key(), &trace
}

func assertStrategyTasks(t testing.TB, session *Session, task TemplateKey, seqs ...int64) {
	t.Helper()
	for _, seq := range seqs {
		if err := session.AssertTemplateValues(context.Background(), task, newIntValue(seq)); err != nil {
			t.Fatalf("AssertTemplateValues(%d): %v", seq, err)
		}
	}
}

func mustStrategySession(t testing.TB, revision *Ruleset, id SessionID, opts ...SessionOption) *Session {
	t.Helper()
	options := append([]SessionOption{WithSessionID(id)}, opts...)
	session, err := NewSession(revision, options...)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func strategyRule(name string, salience int, task TemplateKey, kind, action string) RuleSpec {
	return RuleSpec{
		Name:     name,
		Salience: salience,
		Conditions: []RuleConditionSpec{{
			Binding: "item",
			Target:  TemplateKeyFact(task),
			FieldConstraints: []FieldConstraintSpec{{
				Field:    "kind",
				Operator: FieldConstraintEqual,
				Value:    kind,
			}},
		}},
		Actions: []RuleActionSpec{{Name: action}},
	}
}

func strategyTraceAction(name, binding, field string, trace *[]string) ActionSpec {
	return ActionSpec{
		Name: name,
		Fn: func(ctx ActionContext) error {
			seq := strategyBindingInt(ctx, binding, field)
			*trace = append(*trace, fmt.Sprintf("%s:%d", name, seq))
			return nil
		},
	}
}

func strategyBindingInt(ctx ActionContext, binding, field string) int64 {
	value, ok := ctx.BindingScalarValue(binding, field)
	if !ok {
		panic(fmt.Sprintf("missing %s.%s binding", binding, field))
	}
	seq, ok := value.AsInt64()
	if !ok {
		panic(fmt.Sprintf("%s.%s is not an int", binding, field))
	}
	return seq
}

func TestSessionStrategyBreadthSurvivesResetAndFork(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "seq", Kind: ValueInt, Required: true},
		},
	})
	trace := make([]string, 0, 3)
	mustAddAction(t, workspace, strategyTraceAction("task", "item", "seq", &trace))
	mustAddRule(t, workspace, strategyRule("task", 10, task.Key(), "task", "task"))
	revision := mustCompileWorkspace(t, workspace)
	session := mustStrategySession(t, revision, SessionID("strategy-reset"), WithStrategy(StrategyBreadth))

	assertTasks := func(s *Session) {
		t.Helper()
		for seq := int64(1); seq <= 3; seq++ {
			if _, err := s.Assert(ctx, task.Key(), Fields{
				"kind": newStringValue("task"),
				"seq":  newIntValue(seq),
			}); err != nil {
				t.Fatalf("Assert(%d): %v", seq, err)
			}
		}
	}
	runAndTrace := func(s *Session) []string {
		t.Helper()
		trace = trace[:0]
		result, err := s.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 3 {
			t.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
		}
		return append([]string(nil), trace...)
	}

	want := []string{"task:1", "task:2", "task:3"}
	assertTasks(session)
	if got := runAndTrace(session); !reflect.DeepEqual(got, want) {
		t.Fatalf("initial trace = %#v, want %#v", got, want)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	assertTasks(session)
	if got := runAndTrace(session); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-reset trace = %#v, want %#v", got, want)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("second Reset: %v", err)
	}
	assertTasks(session)
	fork, err := session.Fork(ctx, WithSessionID(SessionID("strategy-reset-fork")))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer fork.Close()
	if got := runAndTrace(fork); !reflect.DeepEqual(got, want) {
		t.Fatalf("fork trace = %#v, want %#v", got, want)
	}

	if _, err := fork.Reset(ctx); err != nil {
		t.Fatalf("fork Reset: %v", err)
	}
	assertTasks(fork)
	if got := runAndTrace(fork); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-reset fork trace = %#v, want %#v", got, want)
	}
}

func TestSessionForkWithStrategyOverridesOrdering(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "seq", Kind: ValueInt, Required: true},
		},
	})
	trace := make([]string, 0, 3)
	mustAddAction(t, workspace, strategyTraceAction("task", "item", "seq", &trace))
	mustAddRule(t, workspace, strategyRule("task", 10, task.Key(), "task", "task"))
	revision := mustCompileWorkspace(t, workspace)
	session := mustStrategySession(t, revision, SessionID("strategy-fork-override"))

	assertTasks := func(s *Session) {
		t.Helper()
		for seq := int64(1); seq <= 3; seq++ {
			if _, err := s.Assert(ctx, task.Key(), Fields{
				"kind": newStringValue("task"),
				"seq":  newIntValue(seq),
			}); err != nil {
				t.Fatalf("Assert(%d): %v", seq, err)
			}
		}
	}
	runAndTrace := func(s *Session) []string {
		t.Helper()
		trace = trace[:0]
		result, err := s.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 3 {
			t.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
		}
		return append([]string(nil), trace...)
	}

	assertTasks(session)
	fork, err := session.Fork(ctx,
		WithSessionID(SessionID("strategy-fork-override-fork")),
		WithStrategy(StrategyBreadth))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer fork.Close()

	breadthWant := []string{"task:1", "task:2", "task:3"}
	if got := runAndTrace(fork); !reflect.DeepEqual(got, breadthWant) {
		t.Fatalf("fork trace = %#v, want %#v", got, breadthWant)
	}
	if _, err := fork.Reset(ctx); err != nil {
		t.Fatalf("fork Reset: %v", err)
	}
	assertTasks(fork)
	if got := runAndTrace(fork); !reflect.DeepEqual(got, breadthWant) {
		t.Fatalf("post-reset fork trace = %#v, want %#v", got, breadthWant)
	}

	depthWant := []string{"task:3", "task:2", "task:1"}
	if got := runAndTrace(session); !reflect.DeepEqual(got, depthWant) {
		t.Fatalf("parent trace = %#v, want %#v", got, depthWant)
	}
}

func TestSessionStrategyBreadthPreservesSequentialMutationEpochsAcrossDirectAndCoalescedRunPaths(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	start := mustAddTemplate(t, workspace, TemplateSpec{Name: "start"})
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "task",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})

	trace := make([]string, 0, 2)
	mustAddAction(t, workspace, ActionSpec{
		Name: "seed",
		Fn: func(ctx ActionContext) error {
			if err := ctx.AssertTemplateValues(task.Key(), newIntValue(2)); err != nil {
				return err
			}
			return ctx.AssertTemplateValues(task.Key(), newIntValue(1))
		},
	})
	mustAddAction(t, workspace, strategyTraceAction("task", "item", "seq", &trace))
	mustAddRule(t, workspace, RuleSpec{
		Name:       "seed",
		Conditions: []RuleConditionSpec{{Binding: "start", Target: TemplateKeyFact(start.Key())}},
		Actions:    []RuleActionSpec{{Name: "seed"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "task",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(task.Key())}},
		Actions:    []RuleActionSpec{{Name: "task"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	runTrace := func(opts ...SessionOption) []string {
		t.Helper()
		trace = trace[:0]
		session, err := NewSession(
			revision,
			append([]SessionOption{
				WithStrategy(StrategyBreadth),
				WithInitialFacts(SessionInitialFact{TemplateKey: start.Key(), Fields: mustFields(t, map[string]any{})}),
			}, opts...)...,
		)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 3 {
			t.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
		}
		return append([]string(nil), trace...)
	}

	want := []string{"task:2", "task:1"}
	if got := runTrace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("direct trace = %#v, want %#v", got, want)
	}
	if got := runTrace(WithEventListener(EventFunc(func(context.Context, Event) error { return nil }), ForEventTypes(EventRuleFired))); !reflect.DeepEqual(got, want) {
		t.Fatalf("coalesced trace = %#v, want %#v", got, want)
	}
}

func TestSessionStrategyBreadthAssignsNewBirthOnReactivation(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "item",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})
	carry := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "carry",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})
	blocker := mustAddTemplate(t, workspace, TemplateSpec{Name: "blocker"})

	trace := make([]string, 0, 3)
	mustAddAction(t, workspace, strategyTraceAction("carry", "item", "seq", &trace))
	mustAddAction(t, workspace, strategyTraceAction("task", "item", "seq", &trace))
	mustAddRule(t, workspace, RuleSpec{
		Name:       "carry",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(carry.Key())}},
		Actions:    []RuleActionSpec{{Name: "carry"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "task",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Not{Condition: Match{Binding: "blocked", Target: TemplateKeyFact(blocker.Key())}},
		}},
		Actions: []RuleActionSpec{{Name: "task"}},
	})

	session := mustStrategySession(t, mustCompileWorkspace(t, workspace), "breadth-reactivation", WithStrategy(StrategyBreadth))
	assertStrategyTasks(t, session, item.Key(), 1, 2)
	blocked, err := session.Assert(ctx, blocker.Key(), mustFields(t, map[string]any{}))
	if err != nil {
		t.Fatalf("Assert(blocker): %v", err)
	}
	if _, err := session.Assert(ctx, carry.Key(), mustFields(t, map[string]any{"seq": 0})); err != nil {
		t.Fatalf("Assert(carry): %v", err)
	}
	if _, err := session.Retract(ctx, blocked.Fact.ID()); err != nil {
		t.Fatalf("Retract(blocker): %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 3 {
		t.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
	}
	if got, want := append([]string(nil), trace...), []string{"carry:0", "task:1", "task:2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %#v, want %#v", got, want)
	}
}

func TestSessionStrategyBreadthReactivationParityAcrossDirectAndCoalescedRunPaths(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	start := mustAddTemplate(t, workspace, TemplateSpec{Name: "start"})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "item",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})
	carry := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "carry",
		Fields: []FieldSpec{{Name: "seq", Kind: ValueInt, Required: true}},
	})
	blocker := mustAddTemplate(t, workspace, TemplateSpec{Name: "blocker"})

	trace := make([]string, 0, 3)
	mustAddAction(t, workspace, ActionSpec{
		Name: "seed",
		Fn: func(ctx ActionContext) error {
			blocked, err := ctx.Assert(blocker.Key(), Fields{})
			if err != nil {
				return err
			}
			if err := ctx.AssertTemplateValues(carry.Key(), newIntValue(0)); err != nil {
				return err
			}
			_, err = ctx.Retract(blocked.Fact.ID())
			return err
		},
	})
	mustAddAction(t, workspace, strategyTraceAction("carry", "item", "seq", &trace))
	mustAddAction(t, workspace, strategyTraceAction("task", "item", "seq", &trace))
	mustAddRule(t, workspace, RuleSpec{
		Name:       "seed",
		Salience:   10,
		Conditions: []RuleConditionSpec{{Binding: "start", Target: TemplateKeyFact(start.Key())}},
		Actions:    []RuleActionSpec{{Name: "seed"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "carry",
		Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(carry.Key())}},
		Actions:    []RuleActionSpec{{Name: "carry"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "task",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Not{Condition: Match{Binding: "blocked", Target: TemplateKeyFact(blocker.Key())}},
		}},
		Actions: []RuleActionSpec{{Name: "task"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	runTrace := func(opts ...SessionOption) []string {
		t.Helper()
		trace = trace[:0]
		session, err := NewSession(
			revision,
			append([]SessionOption{
				WithStrategy(StrategyBreadth),
				WithInitialFacts(
					SessionInitialFact{TemplateKey: start.Key(), Fields: mustFields(t, map[string]any{})},
					SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"seq": 2})},
					SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{"seq": 1})},
				),
			}, opts...)...,
		)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 4 {
			t.Fatalf("run result = (%v, %d), want (%v, 4)", result.Status, result.Fired, RunCompleted)
		}
		return append([]string(nil), trace...)
	}

	want := []string{"carry:0", "task:2", "task:1"}
	if got := runTrace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("direct trace = %#v, want %#v", got, want)
	}
	if got := runTrace(WithEventListener(EventFunc(func(context.Context, Event) error { return nil }), ForEventTypes(EventRuleFired))); !reflect.DeepEqual(got, want) {
		t.Fatalf("coalesced trace = %#v, want %#v", got, want)
	}
}

func strategyName(strategy Strategy) string {
	switch strategy {
	case StrategyDepth:
		return "depth"
	case StrategyBreadth:
		return "breadth"
	default:
		return fmt.Sprintf("strategy-%d", strategy)
	}
}
