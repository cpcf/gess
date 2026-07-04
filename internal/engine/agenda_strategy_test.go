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
		if got := len(session.agenda.pendingActivations()); got != 1 {
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
		if _, err := session.AssertTemplate(ctx, task.Key(), Fields{
			"kind": newStringValue(assertion.kind),
			"seq":  newIntValue(assertion.seq),
		}); err != nil {
			t.Fatalf("AssertTemplate(%s:%d): %v", assertion.kind, assertion.seq, err)
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
			if _, err := s.AssertTemplate(ctx, task.Key(), Fields{
				"kind": newStringValue("task"),
				"seq":  newIntValue(seq),
			}); err != nil {
				t.Fatalf("AssertTemplate(%d): %v", seq, err)
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
			if _, err := s.AssertTemplate(ctx, task.Key(), Fields{
				"kind": newStringValue("task"),
				"seq":  newIntValue(seq),
			}); err != nil {
				t.Fatalf("AssertTemplate(%d): %v", seq, err)
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
