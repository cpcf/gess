package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"
)

var benchmarkLargeSteadyStateRunResult RunResult

type largeSteadyStateScalingCase struct {
	streams int
	limit   int
}

func BenchmarkGessLargeSteadyStateRuleCreatedFacts(b *testing.B) {
	cases := []largeSteadyStateScalingCase{
		{streams: 2, limit: 32},
		{streams: 8, limit: 128},
		{streams: 16, limit: 256},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("streams=%d/limit=%d/rules=%d/final-facts=%d/fired=%d", tc.streams, tc.limit, largeSteadyStateRuleCount(tc), largeSteadyStateFinalFacts(tc), largeSteadyStateFiredCount(tc))
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileLargeSteadyStateScalingRuleset(b, tc)

			b.ReportAllocs()
			b.ResetTimer()
			b.ReportMetric(float64(tc.limit), "limit")
			b.ReportMetric(float64(largeSteadyStateTemplateCount()), "templates")
			b.ReportMetric(float64(largeSteadyStateRuleCount(tc)), "rules")
			b.ReportMetric(float64(largeSteadyStateInitialFacts(tc)), "initial-facts")
			b.ReportMetric(float64(largeSteadyStateFinalFacts(tc)), "final-facts")
			b.ReportMetric(float64(largeSteadyStateFiredCount(tc)), "fired/run")
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				session := mustSeedLargeSteadyStateScalingSession(b, revision, tc)
				b.StartTimer()
				result, err := session.Run(ctx)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				validateLargeSteadyStateHarnessSession(b, session, result, tc, "benchmark")
				benchmarkLargeSteadyStateRunResult = result
			}
			propagation := collectLargeSteadyStateScalingPropagationCounters(b, revision, tc)
			propagation.reportMetrics(func(name string, value float64) {
				b.ReportMetric(value, name)
			})
		})
	}
}

func TestLargeSteadyStateScalingSmokeValidatesFacts(t *testing.T) {
	ctx := context.Background()
	tc := largeSteadyStateScalingCase{streams: 2, limit: 32}
	revision := mustCompileLargeSteadyStateScalingRuleset(t, tc)
	session := mustSeedLargeSteadyStateScalingSession(t, revision, tc)

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateLargeSteadyStateHarnessSession(t, session, result, tc, "smoke")
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)

	second, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Status != RunCompleted || second.Fired != 0 {
		t.Fatalf("second run result = (%v, %d), want (%v, 0)", second.Status, second.Fired, RunCompleted)
	}

	snapshot := collectLargeSteadyStateScalingPropagationCounters(t, revision, tc)
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) != 0 {
		t.Fatalf("unsupported reasons = %#v, want none", snapshot.UnsupportedReasons)
	}
	if snapshot.Totals.RHSAsserts != largeSteadyStateReteRHSAsserts(tc) {
		t.Fatalf("rhs asserts = %d, want %d", snapshot.Totals.RHSAsserts, largeSteadyStateReteRHSAsserts(tc))
	}
	if snapshot.Totals.TerminalRowsInserted == 0 || snapshot.Totals.ActivationsStored == 0 {
		t.Fatalf("terminal/activation counters were not populated: %#v", snapshot.Totals)
	}
}

func TestLargeSteadyStateScalingRunOnlyHarness(t *testing.T) {
	if os.Getenv("GESS_LARGE_STEADY_STATE_RUNNER") == "" {
		t.Skip("set GESS_LARGE_STEADY_STATE_RUNNER=1 to run the comparable large steady-state harness")
	}

	iterations := largeSteadyStateHarnessEnvInt(t, "GESS_LARGE_STEADY_STATE_ITERATIONS", 3)
	warmup := largeSteadyStateHarnessEnvInt(t, "GESS_LARGE_STEADY_STATE_WARMUP", 1)
	if iterations <= 0 {
		t.Fatalf("GESS_LARGE_STEADY_STATE_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_LARGE_STEADY_STATE_WARMUP must be non-negative, got %d", warmup)
	}

	cases := []largeSteadyStateScalingCase{
		{streams: 2, limit: 32},
		{streams: 8, limit: 128},
		{streams: 16, limit: 256},
	}
	streamsRaw, streamsSet := os.LookupEnv("GESS_LARGE_STEADY_STATE_STREAMS")
	limitRaw, limitSet := os.LookupEnv("GESS_LARGE_STEADY_STATE_LIMIT")
	if streamsSet || limitSet {
		if !streamsSet || !limitSet {
			t.Fatal("GESS_LARGE_STEADY_STATE_STREAMS and GESS_LARGE_STEADY_STATE_LIMIT must be provided together")
		}
		streams, err := strconv.Atoi(streamsRaw)
		if err != nil {
			t.Fatalf("GESS_LARGE_STEADY_STATE_STREAMS: %v", err)
		}
		limit, err := strconv.Atoi(limitRaw)
		if err != nil {
			t.Fatalf("GESS_LARGE_STEADY_STATE_LIMIT: %v", err)
		}
		cases = []largeSteadyStateScalingCase{{streams: streams, limit: limit}}
	}

	for _, tc := range cases {
		runLargeSteadyStateHarnessCase(t, tc, iterations, warmup)
	}
}

func runLargeSteadyStateHarnessCase(t *testing.T, tc largeSteadyStateScalingCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision := mustCompileLargeSteadyStateScalingRuleset(t, tc)
	for range warmup {
		session := mustSeedLargeSteadyStateScalingSession(t, revision, tc)
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("warmup Run: %v", err)
		}
		validateLargeSteadyStateHarnessSession(t, session, result, tc, "warmup")
	}

	sessions := make([]*Session, iterations)
	for i := range sessions {
		sessions[i] = mustSeedLargeSteadyStateScalingSession(t, revision, tc)
	}
	results := make([]RunResult, iterations)

	profilePath := os.Getenv("GESS_LARGE_STEADY_STATE_CPU_PROFILE")
	var profileFile *os.File
	if profilePath != "" {
		var err error
		profileFile, err = os.Create(profilePath)
		if err != nil {
			t.Fatalf("create CPU profile: %v", err)
		}
		defer profileFile.Close()
		if err := pprof.StartCPUProfile(profileFile); err != nil {
			t.Fatalf("start CPU profile: %v", err)
		}
	}

	memProfilePath := os.Getenv("GESS_LARGE_STEADY_STATE_MEM_PROFILE")
	memProfileRate := runtime.MemProfileRate
	if memProfilePath != "" {
		runtime.MemProfileRate = 1
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i, session := range sessions {
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("benchmark Run: %v", err)
		}
		results[i] = result
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if memProfilePath != "" {
		runtime.MemProfileRate = memProfileRate
	}
	if profilePath != "" {
		pprof.StopCPUProfile()
	}
	if memProfilePath != "" {
		memProfileFile, err := os.Create(memProfilePath)
		if err != nil {
			t.Fatalf("create allocation profile: %v", err)
		}
		profile := pprof.Lookup("allocs")
		if profile == nil {
			if err := memProfileFile.Close(); err != nil {
				t.Fatalf("close allocation profile: %v", err)
			}
			t.Fatal("allocation profile unavailable")
		}
		if err := profile.WriteTo(memProfileFile, 0); err != nil {
			_ = memProfileFile.Close()
			t.Fatalf("write allocation profile: %v", err)
		}
		if err := memProfileFile.Close(); err != nil {
			t.Fatalf("close allocation profile: %v", err)
		}
	}

	for i, session := range sessions {
		validateLargeSteadyStateHarnessSession(t, session, results[i], tc, "benchmark")
	}
	propagationFields := ""
	if fields := collectLargeSteadyStateScalingPropagationCounters(t, revision, tc).runnerFields(); len(fields) > 0 {
		propagationFields = "|" + strings.Join(fields, "|")
	}

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|large-steady-state-scaling|run-only|streams=%d|limit=%d|templates=%d|rules=%d|initial-facts=%d|final-facts=%d|fired=%d|rhs-asserts=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f%s\n",
		tc.streams, tc.limit, largeSteadyStateTemplateCount(), largeSteadyStateRuleCount(tc), largeSteadyStateInitialFacts(tc), largeSteadyStateFinalFacts(tc), largeSteadyStateFiredCount(tc), largeSteadyStateRHSAsserts(tc), iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
		propagationFields,
	)
}

func mustCompileLargeSteadyStateScalingRuleset(t testing.TB, tc largeSteadyStateScalingCase) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	step := mustAddTemplate(t, workspace, largeSteadyStateTemplate("step", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	stepNSlot := mustLargeSteadyStateSlot(t, step, "n")

	signal := mustAddTemplate(t, workspace, largeSteadyStateTemplate("signal", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "kind", Kind: ValueString, Required: true},
		{Name: "score", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	signalNSlot := mustLargeSteadyStateSlot(t, signal, "n")

	route := mustAddTemplate(t, workspace, largeSteadyStateTemplate("route", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "lane", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))
	routeNSlot := mustLargeSteadyStateSlot(t, route, "n")

	score := mustAddTemplate(t, workspace, largeSteadyStateTemplate("score", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "band", Kind: ValueString, Required: true},
		{Name: "value", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	scoreNSlot := mustLargeSteadyStateSlot(t, score, "n")
	scoreBandSlot := mustLargeSteadyStateSlot(t, score, "band")

	review := mustAddTemplate(t, workspace, largeSteadyStateTemplate("review", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "reason", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))

	decision := mustAddTemplate(t, workspace, largeSteadyStateTemplate("decision", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "outcome", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))
	decisionNSlot := mustLargeSteadyStateSlot(t, decision, "n")

	audit := mustAddTemplate(t, workspace, largeSteadyStateTemplate("audit", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "code", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))

	done := mustAddTemplate(t, workspace, largeSteadyStateTemplate("done", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
	}, []string{"stream"}))
	complete := mustAddTemplate(t, workspace, largeSteadyStateTemplate("complete", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
	}, []string{"stream"}))

	for stream := 0; stream < tc.streams; stream++ {
		stream := stream
		streamValue := steadyStateIntValue(stream)

		advanceAction := fmt.Sprintf("large-advance-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: advanceAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(step.Key(), steadyStateIntValue(n+1), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-advance-stream-%03d", stream),
			Conditions: []RuleConditionSpec{{
				Binding: "step",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					{Field: "n", Operator: FieldConstraintLessThan, Value: tc.limit},
				}, Target: TemplateKeyFact(step.Key()),
			}},
			Actions: []RuleActionSpec{{Name: advanceAction}},
		})

		signalAction := fmt.Sprintf("large-signal-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: signalAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(signal.Key(), steadyStateStringValue(largeSteadyStateSignalKind(n)), steadyStateIntValue(n), steadyStateIntValue(largeSteadyStateScoreValue(n)), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-signal-stream-%03d", stream),
			Conditions: []RuleConditionSpec{{
				Binding: "step",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
				}, Target: TemplateKeyFact(step.Key()),
			}},
			Actions: []RuleActionSpec{{Name: signalAction}},
		})

		routeAction := fmt.Sprintf("large-route-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: routeAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(route.Key(), steadyStateStringValue(largeSteadyStateRouteLane(n)), steadyStateIntValue(n), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-route-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding: "step",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					}, Target: TemplateKeyFact(step.Key()),
				},
				{
					Binding: "signal",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
						{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "step", Field: "n"}},
					}, Target: TemplateKeyFact(signal.Key()),
				},
			},
			Actions: []RuleActionSpec{{Name: routeAction}},
		})

		scoreAction := fmt.Sprintf("large-score-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: scoreAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, signalNSlot)
				if err != nil {
					return err
				}
				value := largeSteadyStateScoreValue(n)
				return ctx.AssertTemplateValues(score.Key(), steadyStateStringValue(largeSteadyStateScoreBand(value)), steadyStateIntValue(n), streamValue, steadyStateIntValue(value))
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-score-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding: "signal",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
					}, Target: TemplateKeyFact(signal.Key()),
				},
				{
					Binding: "route",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "lane", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
					}, Target: TemplateKeyFact(route.Key()),
				},
			},
			Actions: []RuleActionSpec{{Name: scoreAction}},
		})

		reviewAction := fmt.Sprintf("large-review-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: reviewAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, scoreNSlot)
				if err != nil {
					return err
				}
				bandValue, ok := ctx.bindingScalarValueAtSlot(0, scoreBandSlot)
				if !ok || bandValue.Kind() != ValueString {
					return fmt.Errorf("missing score band on binding slot 0")
				}
				return ctx.AssertTemplateValues(review.Key(), steadyStateIntValue(n), steadyStateStringValue(largeSteadyStateReviewReason(n, bandValue.stringValue)), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-review-stream-%03d", stream),
			Conditions: []RuleConditionSpec{{
				Binding: "score",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					{Field: "band", Operator: FieldConstraintNotEqual, Value: "blocked"},
				}, Target: TemplateKeyFact(score.Key()),
			}},
			Actions: []RuleActionSpec{{Name: reviewAction}},
		})

		decisionAction := fmt.Sprintf("large-decision-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: decisionAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, routeNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(decision.Key(), steadyStateIntValue(n), steadyStateStringValue(largeSteadyStateDecisionOutcome(n)), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-decision-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding: "route",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "lane", Operator: FieldConstraintNotEqual, Value: "blocked"},
					}, Target: TemplateKeyFact(route.Key()),
				},
				{
					Binding: "score",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "value", Operator: FieldConstraintGreaterOrEqual, Value: 50},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "route", Field: "n"}},
					}, Target: TemplateKeyFact(score.Key()),
				},
			},
			Actions: []RuleActionSpec{{Name: decisionAction}},
		})

		auditAction := fmt.Sprintf("large-audit-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: auditAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, decisionNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(audit.Key(), steadyStateStringValue(largeSteadyStateAuditCode(n)), steadyStateIntValue(n), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-audit-stream-%03d", stream),
			Conditions: []RuleConditionSpec{{
				Binding: "decision",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					{Field: "outcome", Operator: FieldConstraintNotEqual, Value: "blocked"},
				}, Target: TemplateKeyFact(decision.Key()),
			}},
			Actions: []RuleActionSpec{{Name: auditAction}},
		})

		doneAction := fmt.Sprintf("large-done-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: doneAction,
			Fn: func(ctx ActionContext) error {
				return ctx.AssertTemplateValues(done.Key(), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-done-stream-%03d", stream),
			Conditions: []RuleConditionSpec{{
				Binding: "audit",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
				}, Target: TemplateKeyFact(audit.Key()),
			}},
			Actions: []RuleActionSpec{{Name: doneAction}},
		})

		completeAction := fmt.Sprintf("large-complete-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: completeAction,
			Fn: func(ctx ActionContext) error {
				return ctx.AssertTemplateValues(complete.Key(), streamValue)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("large-complete-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding: "done",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					}, Target: TemplateKeyFact(done.Key()),
				},
				{
					Binding: "signal",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					}, Target: TemplateKeyFact(signal.Key()),
				},
				{
					Binding: "route",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "lane", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					}, Target: TemplateKeyFact(route.Key()),
				},
				{
					Binding: "score",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "value", Operator: FieldConstraintGreaterOrEqual, Value: 50},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					}, Target: TemplateKeyFact(score.Key()),
				},
				{
					Binding: "decision",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "outcome", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					}, Target: TemplateKeyFact(decision.Key()),
				},
				{
					Binding: "audit",

					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "code", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					}, Target: TemplateKeyFact(audit.Key()),
				},
			},
			Actions: []RuleActionSpec{{Name: completeAction}},
		})
	}

	return mustCompileWorkspace(t, workspace)
}

func largeSteadyStateTemplate(name string, fields []FieldSpec, duplicateKeys []string) TemplateSpec {
	return TemplateSpec{
		Name:              name,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: duplicateKeys,
		Fields:            fields,
	}
}

func mustLargeSteadyStateSlot(t testing.TB, template Template, field string) int {
	t.Helper()
	slot, ok := template.fieldSlot(field)
	if !ok {
		t.Fatalf("%s template missing %s slot", template.Key(), field)
	}
	return slot
}

func mustSeedLargeSteadyStateScalingSession(t testing.TB, revision *Ruleset, tc largeSteadyStateScalingCase) *Session {
	t.Helper()

	session := mustSession(t, revision, SessionID(fmt.Sprintf("large-steady-state-scaling-%d-%d", tc.streams, tc.limit)))
	if session.rete == nil {
		t.Fatal("session Rete runtime is nil")
	}
	seedLargeSteadyStateScalingSession(t, session, tc)
	return session
}

func seedLargeSteadyStateScalingSession(t testing.TB, session *Session, tc largeSteadyStateScalingCase) {
	t.Helper()
	if session == nil {
		t.Fatal("session is nil")
	}
	for stream := 0; stream < tc.streams; stream++ {
		if _, err := session.AssertTemplate(context.Background(), TemplateKey("step"), Fields{
			"stream": steadyStateIntValue(stream),
			"n":      steadyStateIntValue(0),
		}); err != nil {
			t.Fatalf("AssertTemplate(step stream=%d): %v", stream, err)
		}
	}
}

func collectLargeSteadyStateScalingPropagationCounters(t testing.TB, revision *Ruleset, tc largeSteadyStateScalingCase) propagationCounterSnapshot {
	t.Helper()

	ctx := context.Background()
	session := mustSeedLargeSteadyStateScalingSession(t, revision, tc)
	session.attachPropagationCounters()

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateLargeSteadyStateHarnessSession(t, session, result, tc, "counter")
	return session.propagationCounterSnapshot()
}

func validateLargeSteadyStateHarnessSession(t testing.TB, session *Session, result RunResult, tc largeSteadyStateScalingCase, phase string) {
	t.Helper()

	if result.Status != RunCompleted || result.Fired != largeSteadyStateFiredCount(tc) {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, largeSteadyStateFiredCount(tc))
	}
	if got := len(session.facts); got != largeSteadyStateFinalFacts(tc) {
		t.Fatalf("%s final fact count = %d, want %d", phase, got, largeSteadyStateFinalFacts(tc))
	}

	perStep := tc.streams * (tc.limit + 1)
	assertLargeSteadyStateTemplateCount(t, session, phase, "step", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "signal", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "route", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "score", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "review", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "decision", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "audit", perStep)
	assertLargeSteadyStateTemplateCount(t, session, phase, "done", tc.streams)
	assertLargeSteadyStateTemplateCount(t, session, phase, "complete", tc.streams)
	assertLargeSteadyStateSelectedFacts(t, session, tc)
}

func assertLargeSteadyStateTemplateCount(t testing.TB, session *Session, phase string, template string, want int) {
	t.Helper()
	session.ensureFactTargetIndexes()
	if got := len(session.factsByTemplate[TemplateKey(template)]); got != want {
		t.Fatalf("%s %s fact count = %d, want %d", phase, template, got, want)
	}
}

func assertLargeSteadyStateSelectedFacts(t testing.TB, session *Session, tc largeSteadyStateScalingCase) {
	t.Helper()

	for stream := 0; stream < tc.streams; stream++ {
		for _, n := range largeSteadyStateSelectedNs(tc.limit) {
			scoreValue := largeSteadyStateScoreValue(n)
			band := largeSteadyStateScoreBand(scoreValue)
			assertLargeSteadyStateFact(t, session, "step", map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
			})
			assertLargeSteadyStateFact(t, session, "signal", map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"kind":   steadyStateStringValue(largeSteadyStateSignalKind(n)),
				"score":  steadyStateIntValue(scoreValue),
			})
			assertLargeSteadyStateFact(t, session, "route", map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"lane":   steadyStateStringValue(largeSteadyStateRouteLane(n)),
			})
			assertLargeSteadyStateFact(t, session, "score", map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"band":   steadyStateStringValue(band),
				"value":  steadyStateIntValue(scoreValue),
			})
			assertLargeSteadyStateFact(t, session, "review", map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"reason": steadyStateStringValue(largeSteadyStateReviewReason(n, band)),
			})
			assertLargeSteadyStateFact(t, session, "decision", map[string]Value{
				"stream":  steadyStateIntValue(stream),
				"n":       steadyStateIntValue(n),
				"outcome": steadyStateStringValue(largeSteadyStateDecisionOutcome(n)),
			})
			assertLargeSteadyStateFact(t, session, "audit", map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"code":   steadyStateStringValue(largeSteadyStateAuditCode(n)),
			})
		}
		assertLargeSteadyStateFact(t, session, "done", map[string]Value{
			"stream": steadyStateIntValue(stream),
		})
		assertLargeSteadyStateFact(t, session, "complete", map[string]Value{
			"stream": steadyStateIntValue(stream),
		})
	}
}

func assertLargeSteadyStateFact(t testing.TB, session *Session, template string, expected map[string]Value) {
	t.Helper()

	session.ensureFactTargetIndexes()
	templateKey := TemplateKey(template)
	for _, id := range session.factsByTemplate[templateKey] {
		fact := mustWorkingFactByID(t, session, id)
		if fact == nil {
			t.Fatalf("%s fact %s missing from working facts", templateKey, id)
		}
		snapshot := fact.snapshotForRevision(session.revision)
		if largeSteadyStateFactMatches(snapshot, expected) {
			assertSteadyStateFactFields(t, templateKey, id, snapshot, expected)
			return
		}
	}
	t.Fatalf("missing %s fact with fields %s", templateKey, largeSteadyStateExpectedFieldsString(expected))
}

func largeSteadyStateFactMatches(fact FactSnapshot, expected map[string]Value) bool {
	for field, want := range expected {
		got, ok := fact.Field(field)
		if !ok || !got.Equal(want) {
			return false
		}
	}
	return true
}

func largeSteadyStateExpectedFieldsString(fields map[string]Value) string {
	parts := make([]string, 0, len(fields))
	for _, name := range []string{"stream", "n", "kind", "score", "lane", "band", "value", "reason", "outcome", "code"} {
		if value, ok := fields[name]; ok {
			parts = append(parts, name+"="+value.String())
		}
	}
	return strings.Join(parts, ",")
}

func largeSteadyStateSelectedNs(limit int) []int {
	candidates := []int{0, limit / 2, limit}
	out := make([]int, 0, len(candidates))
	seen := make(map[int]struct{}, len(candidates))
	for _, n := range candidates {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func largeSteadyStateTemplateCount() int {
	return 9
}

func largeSteadyStateRuleCount(tc largeSteadyStateScalingCase) int {
	return tc.streams * 9
}

func largeSteadyStateInitialFacts(tc largeSteadyStateScalingCase) int {
	return tc.streams
}

func largeSteadyStateFinalFacts(tc largeSteadyStateScalingCase) int {
	return tc.streams * (7*(tc.limit+1) + 2)
}

func largeSteadyStateFiredCount(tc largeSteadyStateScalingCase) int {
	return tc.streams * (7*tc.limit + 8)
}

func largeSteadyStateRHSAsserts(tc largeSteadyStateScalingCase) int {
	return largeSteadyStateFinalFacts(tc) - largeSteadyStateInitialFacts(tc)
}

func largeSteadyStateReteRHSAsserts(tc largeSteadyStateScalingCase) int {
	return largeSteadyStateRHSAsserts(tc) - tc.streams*(tc.limit+2)
}

func largeSteadyStateScoreValue(n int) int {
	return 50 + (n % 50)
}

func largeSteadyStateSignalKind(n int) string {
	if n%4 == 0 {
		return "risk"
	}
	return "routine"
}

func largeSteadyStateRouteLane(n int) string {
	return fmt.Sprintf("lane%d", n%8)
}

func largeSteadyStateScoreBand(score int) string {
	if score >= 80 {
		return "high"
	}
	return "normal"
}

func largeSteadyStateReviewReason(n int, band string) string {
	if n%16 == 0 {
		return "periodic"
	}
	return band
}

func largeSteadyStateDecisionOutcome(n int) string {
	if n%8 == 0 {
		return "review"
	}
	return "approve"
}

func largeSteadyStateAuditCode(n int) string {
	return fmt.Sprintf("A%d", n%10)
}

func largeSteadyStateHarnessEnvInt(t *testing.T, name string, defaultValue int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
