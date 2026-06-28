package engine

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type backchainReactiveCase struct {
	templates int
	reactive  int
}

var benchmarkBackchainReactiveRulesetID RulesetID

func BenchmarkGessBackchainReactiveCompile(b *testing.B) {
	cases := []backchainReactiveCase{
		{templates: 16, reactive: 4},
		{templates: 64, reactive: 16},
		{templates: 256, reactive: 64},
	}

	for _, tc := range cases {
		b.Run(fmt.Sprintf("templates=%d/reactive=%d", tc.templates, tc.reactive), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(tc.templates), "templates")
			b.ReportMetric(float64(tc.reactive), "reactive")
			b.ReportMetric(float64(tc.reactive), "generated-demand-templates")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				revision := mustCompileBackchainReactiveRuleset(b, tc)
				benchmarkBackchainReactiveRulesetID = revision.ID()
			}
		})
	}
}

func BenchmarkGessBackchainRuntimeDemand(b *testing.B) {
	ctx := context.Background()
	revision, requestKey := mustCompileBackchainRuntimeRuleset(b)
	session, err := NewSession(revision, WithResetBeforeSnapshot(false))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	b.ReportAllocs()
	b.ReportMetric(1, "requests")
	b.ReportMetric(1, "generated-demand-facts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetBackchainRuntimeDemand(b, ctx, session)
		runBackchainRuntimeDemand(b, ctx, session, requestKey)
	}
}

func BenchmarkGessBackchainRecursiveReachability(b *testing.B) {
	ctx := context.Background()
	revision, edgeKey, _, requestKey := mustCompileBackchainReachabilityRuleset(b, false)
	session, err := NewSession(revision, WithResetBeforeSnapshot(false))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	edges := [][2]string{
		{"internet", "web"},
		{"web", "api"},
		{"api", "db"},
		{"api", "cache"},
	}
	b.ReportAllocs()
	b.ReportMetric(float64(len(edges)), "edges")
	b.ReportMetric(3, "derived-reachable-facts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := session.Reset(ctx); err != nil {
			b.Fatalf("Reset: %v", err)
		}
		for _, edge := range edges {
			if err := session.AssertTemplateValues(ctx, edgeKey, newStringValue(edge[1]), newStringValue(edge[0])); err != nil {
				b.Fatalf("Assert edge %v: %v", edge, err)
			}
		}
		if err := session.AssertTemplateValues(ctx, requestKey, newStringValue("db"), newStringValue("internet")); err != nil {
			b.Fatalf("Assert request: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 4 {
			b.Fatalf("run result = (%v, %d), want (%v, 4)", result.Status, result.Fired, RunCompleted)
		}
	}
}

func BenchmarkGessBackchainQueryRecursiveReachability(b *testing.B) {
	ctx := context.Background()
	revision, edgeKey, _, _ := mustCompileBackchainReachabilityRuleset(b, true)
	session, err := NewSession(revision, WithResetBeforeSnapshot(false))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	edges := [][2]string{
		{"internet", "web"},
		{"web", "api"},
		{"api", "db"},
		{"api", "cache"},
	}
	b.ReportAllocs()
	b.ReportMetric(float64(len(edges)), "edges")
	b.ReportMetric(3, "derived-reachable-facts")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := session.Reset(ctx); err != nil {
			b.Fatalf("Reset: %v", err)
		}
		for _, edge := range edges {
			if err := session.AssertTemplateValues(ctx, edgeKey, newStringValue(edge[1]), newStringValue(edge[0])); err != nil {
				b.Fatalf("Assert edge %v: %v", edge, err)
			}
		}
		rows, err := session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "internet", "dst": "db"})
		if err != nil {
			b.Fatalf("QueryAll: %v", err)
		}
		if len(rows) != 1 {
			b.Fatalf("rows = %d, want 1", len(rows))
		}
	}
}

func TestBackchainReactiveCompileHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_REACTIVE_RUNNER")) == "" {
		t.Skip("set GESS_BACKCHAIN_REACTIVE_RUNNER=1 to run benchmark harness")
	}

	iterations := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_ITERATIONS", 3)
	warmup := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_WARMUP", 1)
	tc := backchainReactiveCase{
		templates: backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_TEMPLATES", 64),
		reactive:  backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_REACTIVE", 16),
	}
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("warmup must be non-negative, got %d", warmup)
	}

	for range warmup {
		validateBackchainReactiveRuleset(t, mustCompileBackchainReactiveRuleset(t, tc), tc)
	}

	var elapsed time.Duration
	for range iterations {
		start := time.Now()
		revision := mustCompileBackchainReactiveRuleset(t, tc)
		elapsed += time.Since(start)
		validateBackchainReactiveRuleset(t, revision, tc)
	}
	nsPerOp := float64(elapsed.Nanoseconds()) / float64(iterations)

	fmt.Printf(
		"GESS_RUNNER|backchain-reactive|compile|templates=%d|reactive=%d|generated-demand-templates=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f\n",
		tc.templates, tc.reactive, tc.reactive, iterations, warmup, elapsed.Nanoseconds(), nsPerOp)
}

func TestBackchainRuntimeDemandHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_RUNTIME_RUNNER")) == "" {
		t.Skip("set GESS_BACKCHAIN_RUNTIME_RUNNER=1 to run benchmark harness")
	}

	iterations := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_RUNTIME_ITERATIONS", 1000)
	warmup := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_RUNTIME_WARMUP", 20)
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("warmup must be non-negative, got %d", warmup)
	}
	measurePhases := strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_RUNTIME_PHASES")) != ""
	collectCounters := strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_RUNTIME_COUNTERS")) != ""

	ctx := context.Background()
	revision, requestKey := mustCompileBackchainRuntimeRuleset(t)
	session, err := NewSession(revision, WithResetBeforeSnapshot(false))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for range warmup {
		resetBackchainRuntimeDemand(t, ctx, session)
		runBackchainRuntimeDemand(t, ctx, session, requestKey)
	}
	if collectCounters {
		session, err = NewSession(revision, WithResetBeforeSnapshot(false))
		if err != nil {
			t.Fatalf("NewSession(counter): %v", err)
		}
		session.attachPropagationCounters()
	}

	var elapsed, resetElapsed, assertElapsed, runElapsed time.Duration
	for range iterations {
		start := time.Now()
		resetStart := time.Now()
		resetBackchainRuntimeDemand(t, ctx, session)
		resetElapsed += time.Since(resetStart)
		assertStart := time.Now()
		if err := session.AssertTemplateValues(ctx, requestKey, newStringValue("q1")); err != nil {
			t.Fatalf("AssertTemplateValues(request): %v", err)
		}
		assertElapsed += time.Since(assertStart)
		runStart := time.Now()
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		runElapsed += time.Since(runStart)
		if result.Status != RunCompleted || result.Fired != 2 {
			t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
		}
		elapsed += time.Since(start)
	}
	nsPerOp := float64(elapsed.Nanoseconds()) / float64(iterations)
	fields := []string{
		fmt.Sprintf("GESS_RUNNER|backchain-reactive|runtime-demand|requests=%d|generated-demand-facts=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f",
			1, 1, iterations, warmup, elapsed.Nanoseconds(), nsPerOp),
	}
	if measurePhases {
		fields = append(fields,
			fmt.Sprintf("reset-ns/op=%.0f", float64(resetElapsed.Nanoseconds())/float64(iterations)),
			fmt.Sprintf("assert-ns/op=%.0f", float64(assertElapsed.Nanoseconds())/float64(iterations)),
			fmt.Sprintf("run-ns/op=%.0f", float64(runElapsed.Nanoseconds())/float64(iterations)),
		)
	}
	if collectCounters {
		fields = append(fields, session.propagationCounterSnapshot().runnerFields()...)
	}
	fmt.Println(strings.Join(fields, "|"))
}

func TestBackchainReachabilityHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_REACHABILITY_RUNNER")) == "" {
		t.Skip("set GESS_BACKCHAIN_REACHABILITY_RUNNER=1 to run benchmark harness")
	}

	iterations := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACHABILITY_ITERATIONS", 1000)
	warmup := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACHABILITY_WARMUP", 20)
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("warmup must be non-negative, got %d", warmup)
	}
	mode := backchainReachabilityHarnessMode(t)
	measurePhases := strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_REACHABILITY_PHASES")) != ""

	ctx := context.Background()
	revision, edgeKey, _, requestKey := mustCompileBackchainReachabilityRuleset(t, mode == "query")
	session, err := NewSession(revision, WithResetBeforeSnapshot(false))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for range warmup {
		runBackchainReachabilityHarnessCase(t, ctx, session, edgeKey, requestKey, mode)
	}

	var elapsed, resetElapsed, seedElapsed, proveElapsed time.Duration
	for range iterations {
		start := time.Now()
		resetStart := time.Now()
		if _, err := session.Reset(ctx); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		resetElapsed += time.Since(resetStart)
		seedStart := time.Now()
		seedBackchainReachabilityEdges(t, ctx, session, edgeKey)
		seedElapsed += time.Since(seedStart)
		proveStart := time.Now()
		proveBackchainReachabilityHarnessCase(t, ctx, session, requestKey, mode)
		proveElapsed += time.Since(proveStart)
		elapsed += time.Since(start)
	}
	nsPerOp := float64(elapsed.Nanoseconds()) / float64(iterations)
	fields := []string{
		fmt.Sprintf("GESS_RUNNER|backchain-reactive|recursive-reachability|mode=%s|edges=%d|derived-reachable-facts=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f",
			mode, 4, 3, iterations, warmup, elapsed.Nanoseconds(), nsPerOp),
	}
	if measurePhases {
		fields = append(fields,
			fmt.Sprintf("reset-ns/op=%.0f", float64(resetElapsed.Nanoseconds())/float64(iterations)),
			fmt.Sprintf("seed-ns/op=%.0f", float64(seedElapsed.Nanoseconds())/float64(iterations)),
			fmt.Sprintf("prove-ns/op=%.0f", float64(proveElapsed.Nanoseconds())/float64(iterations)),
		)
	}
	fmt.Println(strings.Join(fields, "|"))
}

func mustCompileBackchainReactiveRuleset(t testing.TB, tc backchainReactiveCase) *Ruleset {
	t.Helper()
	if tc.templates < 0 || tc.reactive < 0 || tc.reactive > tc.templates {
		t.Fatalf("invalid backchain reactive case: %#v", tc)
	}

	workspace := NewWorkspace()
	for i := 0; i < tc.templates; i++ {
		if err := workspace.AddTemplate(TemplateSpec{
			Name:              fmt.Sprintf("bc-answer-%03d", i),
			BackchainReactive: i < tc.reactive,
			Fields: []FieldSpec{
				{Name: "question", Kind: ValueString, Required: true},
				{Name: "value", Kind: ValueInt, Required: true},
				{Name: "source", Kind: ValueString},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(%d): %v", i, err)
		}
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}

func mustCompileBackchainRuntimeRuleset(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	request := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "request",
		Key:  "request",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	answer := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "answer",
		Key:               "answer",
		BackchainReactive: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "value", Kind: ValueString},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "provide-answer",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: answer.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "need", Field: "id"},
				BindingFieldExpr{Binding: "need", Field: "kind"},
				ConstExpr{Value: "provided"},
			},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "consume-answer",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "answer-need",
		ConditionTree: Match{
			Binding: "need",
			FieldConstraints: []FieldConstraintSpec{
				{Field: "kind", Operator: FieldConstraintEqual, Value: "hardware"},
			},
			Target: TemplateKeyFact(TemplateKey("need-answer")),
		},
		Actions: []RuleActionSpec{{Name: "provide-answer"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "consume-request-answer",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Match{
				Binding: "answer",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "kind", Operator: FieldConstraintEqual, Value: "hardware"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "id"}},
				},
				Target: TemplateKeyFact(answer.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "consume-answer"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, request.Key()
}

func runBackchainRuntimeDemand(t testing.TB, ctx context.Context, session *Session, requestKey TemplateKey) {
	t.Helper()
	if err := session.AssertTemplateValues(ctx, requestKey, newStringValue("q1")); err != nil {
		t.Fatalf("AssertTemplateValues(request): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}
}

func resetBackchainRuntimeDemand(t testing.TB, ctx context.Context, session *Session) {
	t.Helper()
	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
}

func backchainReachabilityHarnessMode(t testing.TB) string {
	t.Helper()
	mode := strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_REACHABILITY_MODE"))
	if mode == "" {
		return "query"
	}
	switch mode {
	case "query", "run-request":
		return mode
	default:
		t.Fatalf("unsupported GESS_BACKCHAIN_REACHABILITY_MODE %q", mode)
		return ""
	}
}

func runBackchainReachabilityHarnessCase(t testing.TB, ctx context.Context, session *Session, edgeKey, requestKey TemplateKey, mode string) {
	t.Helper()
	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	seedBackchainReachabilityEdges(t, ctx, session, edgeKey)
	proveBackchainReachabilityHarnessCase(t, ctx, session, requestKey, mode)
}

func proveBackchainReachabilityHarnessCase(t testing.TB, ctx context.Context, session *Session, requestKey TemplateKey, mode string) {
	t.Helper()
	switch mode {
	case "query":
		rows, err := session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "internet", "dst": "db"})
		if err != nil {
			t.Fatalf("QueryAll: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
	case "run-request":
		if err := session.AssertTemplateValues(ctx, requestKey, newStringValue("db"), newStringValue("internet")); err != nil {
			t.Fatalf("AssertTemplateValues(request): %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 4 {
			t.Fatalf("run result = (%v, %d), want (%v, 4)", result.Status, result.Fired, RunCompleted)
		}
	default:
		t.Fatalf("unsupported backchain reachability mode %q", mode)
	}
}

func validateBackchainReactiveRuleset(t testing.TB, revision *Ruleset, tc backchainReactiveCase) {
	t.Helper()

	templates := revision.Templates()
	if got, want := len(templates), tc.templates+tc.reactive; got != want {
		t.Fatalf("template count = %d, want %d", got, want)
	}

	reactive := 0
	demand := 0
	for _, template := range templates {
		if template.BackchainReactive() {
			reactive++
			if _, ok := template.BackchainDemandTemplateKey(); !ok {
				t.Fatalf("reactive template %q missing demand key", template.Name())
			}
		}
		if template.IsBackchainDemandTemplate() {
			demand++
			if _, ok := template.BackchainSourceTemplateKey(); !ok {
				t.Fatalf("demand template %q missing source key", template.Name())
			}
		}
	}
	if reactive != tc.reactive {
		t.Fatalf("reactive templates = %d, want %d", reactive, tc.reactive)
	}
	if demand != tc.reactive {
		t.Fatalf("demand templates = %d, want %d", demand, tc.reactive)
	}
}

func backchainReactiveHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
