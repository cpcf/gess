package gess

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

var benchmarkMixedCascadeRunResult RunResult

type mixedCascadeScalingCase struct {
	streams int
	limit   int
}

func BenchmarkGessMixedCascadeRuleCreatedFacts(b *testing.B) {
	cases := []mixedCascadeScalingCase{
		{streams: 4, limit: 32},
		{streams: 8, limit: 64},
		{streams: 16, limit: 128},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("streams=%d/limit=%d/rules=%d/final-facts=%d/fired=%d", tc.streams, tc.limit, mixedCascadeRuleCount(tc), mixedCascadeFinalFacts(tc), mixedCascadeFiredCount(tc))
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileMixedCascadeScalingRuleset(b, tc)

			b.ReportAllocs()
			b.ResetTimer()
			b.ReportMetric(float64(tc.limit), "limit")
			b.ReportMetric(float64(mixedCascadeTemplateCount()), "templates")
			b.ReportMetric(float64(mixedCascadeRuleCount(tc)), "rules")
			b.ReportMetric(float64(mixedCascadeInitialFacts(tc)), "initial-facts")
			b.ReportMetric(float64(mixedCascadeFinalFacts(tc)), "final-facts")
			b.ReportMetric(float64(mixedCascadeFiredCount(tc)), "fired/run")
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				session := mustSeedMixedCascadeScalingSession(b, revision, tc)
				b.StartTimer()
				result, err := session.Run(ctx)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				validateMixedCascadeHarnessSession(b, session, result, tc, "benchmark")
				benchmarkMixedCascadeRunResult = result
			}
			propagation := collectMixedCascadeScalingPropagationCounters(b, revision, tc)
			propagation.reportMetrics(func(name string, value float64) {
				b.ReportMetric(value, name)
			})
		})
	}
}

func TestMixedCascadeScalingSmokeValidatesFacts(t *testing.T) {
	if os.Getenv("GESS_MIXED_CASCADE_SMOKE") == "" {
		t.Skip("set GESS_MIXED_CASCADE_SMOKE=1 to run the mixed cascade smoke check")
	}

	ctx := context.Background()
	tc := mixedCascadeScalingCase{streams: 4, limit: 32}
	revision := mustCompileMixedCascadeScalingRuleset(t, tc)
	session := mustSeedMixedCascadeScalingSession(t, revision, tc)

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateMixedCascadeHarnessSession(t, session, result, tc, "smoke")
}

func TestMixedCascadeScalingRunOnlyHarness(t *testing.T) {
	if os.Getenv("GESS_MIXED_CASCADE_RUNNER") == "" {
		t.Skip("set GESS_MIXED_CASCADE_RUNNER=1 to run the comparable mixed cascade harness")
	}

	iterations := mixedCascadeHarnessEnvInt(t, "GESS_MIXED_CASCADE_ITERATIONS", 3)
	warmup := mixedCascadeHarnessEnvInt(t, "GESS_MIXED_CASCADE_WARMUP", 1)
	if iterations <= 0 {
		t.Fatalf("GESS_MIXED_CASCADE_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_MIXED_CASCADE_WARMUP must be non-negative, got %d", warmup)
	}

	cases := []mixedCascadeScalingCase{
		{streams: 4, limit: 32},
		{streams: 8, limit: 64},
		{streams: 16, limit: 128},
	}
	streamsRaw, streamsSet := os.LookupEnv("GESS_MIXED_CASCADE_STREAMS")
	limitRaw, limitSet := os.LookupEnv("GESS_MIXED_CASCADE_LIMIT")
	if streamsSet || limitSet {
		if !streamsSet || !limitSet {
			t.Fatal("GESS_MIXED_CASCADE_STREAMS and GESS_MIXED_CASCADE_LIMIT must be provided together")
		}
		streams, err := strconv.Atoi(streamsRaw)
		if err != nil {
			t.Fatalf("GESS_MIXED_CASCADE_STREAMS: %v", err)
		}
		limit, err := strconv.Atoi(limitRaw)
		if err != nil {
			t.Fatalf("GESS_MIXED_CASCADE_LIMIT: %v", err)
		}
		cases = []mixedCascadeScalingCase{{streams: streams, limit: limit}}
	}

	for _, tc := range cases {
		runMixedCascadeHarnessCase(t, tc, iterations, warmup)
	}
}

func runMixedCascadeHarnessCase(t *testing.T, tc mixedCascadeScalingCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision := mustCompileMixedCascadeScalingRuleset(t, tc)
	for range warmup {
		session := mustSeedMixedCascadeScalingSession(t, revision, tc)
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("warmup Run: %v", err)
		}
		validateMixedCascadeHarnessSession(t, session, result, tc, "warmup")
	}

	sessions := make([]*Session, iterations)
	for i := range sessions {
		sessions[i] = mustSeedMixedCascadeScalingSession(t, revision, tc)
	}
	results := make([]RunResult, iterations)

	profilePath := os.Getenv("GESS_MIXED_CASCADE_CPU_PROFILE")
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

	memProfilePath := os.Getenv("GESS_MIXED_CASCADE_MEM_PROFILE")
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
		validateMixedCascadeHarnessSession(t, session, results[i], tc, "benchmark")
	}
	propagationFields := ""
	if fields := collectMixedCascadeScalingPropagationCounters(t, revision, tc).runnerFields(); len(fields) > 0 {
		propagationFields = "|" + strings.Join(fields, "|")
	}

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|mixed-cascade-scaling|run-only|streams=%d|limit=%d|regions=%d|segments=%d|templates=%d|rules=%d|initial-facts=%d|final-facts=%d|fired=%d|rhs-asserts=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f%s\n",
		tc.streams, tc.limit, mixedCascadeRegionCount(), mixedCascadeSegmentCount(), mixedCascadeTemplateCount(), mixedCascadeRuleCount(tc), mixedCascadeInitialFacts(tc), mixedCascadeFinalFacts(tc), mixedCascadeFiredCount(tc), mixedCascadeRHSAsserts(tc), iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
		propagationFields,
	)
}

func mustCompileMixedCascadeScalingRuleset(t testing.TB, tc mixedCascadeScalingCase) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	account := mustAddTemplate(t, workspace, mixedCascadeTemplate("account", []FieldSpec{
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "segment", Kind: ValueString, Required: true},
		{Name: "tier", Kind: ValueString, Required: true},
	}, []string{"customer"}))

	policy := mustAddTemplate(t, workspace, mixedCascadeTemplate("policy", []FieldSpec{
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "segment", Kind: ValueString, Required: true},
		{Name: "limit", Kind: ValueInt, Required: true},
		{Name: "priority", Kind: ValueString, Required: true},
	}, []string{"region", "segment"}))
	policyLimitSlot := mixedCascadeSlot(t, policy, "limit")
	policyPrioritySlot := mixedCascadeSlot(t, policy, "priority")

	tick := mustAddTemplate(t, workspace, mixedCascadeTemplate("tick", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	tickNSlot := mixedCascadeSlot(t, tick, "n")

	signal := mustAddTemplate(t, workspace, mixedCascadeTemplate("signal", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "kind", Kind: ValueString, Required: true},
		{Name: "severity", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	signalNSlot := mixedCascadeSlot(t, signal, "n")
	signalSeveritySlot := mixedCascadeSlot(t, signal, "severity")

	exposure := mustAddTemplate(t, workspace, mixedCascadeTemplate("exposure", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "bucket", Kind: ValueString, Required: true},
		{Name: "amount", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	exposureAmountSlot := mixedCascadeSlot(t, exposure, "amount")

	correlated := mustAddTemplate(t, workspace, mixedCascadeTemplate("correlated", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "peer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "severity", Kind: ValueInt, Required: true},
	}, []string{"stream", "n"}))
	correlatedSeveritySlot := mixedCascadeSlot(t, correlated, "severity")

	caseFact := mustAddTemplate(t, workspace, mixedCascadeTemplate("cascade-case", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "priority", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))

	escalation := mustAddTemplate(t, workspace, mixedCascadeTemplate("escalation", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "reason", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))

	audit := mustAddTemplate(t, workspace, mixedCascadeTemplate("cascade-audit", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "n", Kind: ValueInt, Required: true},
		{Name: "customer", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
		{Name: "code", Kind: ValueString, Required: true},
	}, []string{"stream", "n"}))

	complete := mustAddTemplate(t, workspace, mixedCascadeTemplate("cascade-complete", []FieldSpec{
		{Name: "stream", Kind: ValueInt, Required: true},
		{Name: "region", Kind: ValueInt, Required: true},
	}, []string{"stream"}))

	for stream := 0; stream < tc.streams; stream++ {
		stream := stream
		region := mixedCascadeRegion(stream)
		segment := mixedCascadeSegment(stream)
		customer := mixedCascadeCustomer(stream)
		peer := mixedCascadePeer(stream, tc.streams)

		advanceAction := fmt.Sprintf("mixed-advance-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: advanceAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, tickNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(tick.Key(),
					steadyStateIntValue(customer),
					steadyStateIntValue(n+1),
					steadyStateIntValue(region),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-advance-stream-%03d", stream),
			Conditions: []RuleConditionSpec{{
				Binding:     "tick",
				TemplateKey: tick.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					{Field: "n", Operator: FieldConstraintLessThan, Value: tc.limit},
				},
			}},
			Actions: []RuleActionSpec{{Name: advanceAction}},
		})

		signalAction := fmt.Sprintf("mixed-signal-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: signalAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, tickNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(signal.Key(),
					steadyStateIntValue(customer),
					steadyStateStringValue(mixedCascadeSignalKind(n, stream)),
					steadyStateIntValue(n),
					steadyStateIntValue(region),
					steadyStateIntValue(mixedCascadeSeverity(n, stream)),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-signal-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "tick",
					TemplateKey: tick.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "customer", Operator: FieldConstraintEqual, Value: customer},
					},
				},
				{
					Binding:     "account",
					TemplateKey: account.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "customer", Operator: FieldConstraintEqual, Value: customer},
						{Field: "tier", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "tick", Field: "region"}},
					},
				},
				{
					Binding:     "policy",
					TemplateKey: policy.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Value: region},
						{Field: "segment", Operator: FieldConstraintEqual, Value: segment},
						{Field: "limit", Operator: FieldConstraintGreaterOrEqual, Value: 100},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "tick", Field: "region"}},
						{Field: "segment", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "account", Field: "segment"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: signalAction}},
		})

		exposureAction := fmt.Sprintf("mixed-exposure-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: exposureAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, signalNSlot)
				if err != nil {
					return err
				}
				severity, err := steadyStateBindingIntAtSlot(ctx, 0, signalSeveritySlot)
				if err != nil {
					return err
				}
				limitValue, err := steadyStateBindingIntAtSlot(ctx, 1, policyLimitSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(exposure.Key(),
					steadyStateIntValue(mixedCascadeAmount(severity, limitValue)),
					steadyStateStringValue(mixedCascadeBucket(severity)),
					steadyStateIntValue(customer),
					steadyStateIntValue(n),
					steadyStateIntValue(region),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-exposure-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
				},
				{
					Binding:     "policy",
					TemplateKey: policy.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Value: region},
						{Field: "segment", Operator: FieldConstraintEqual, Value: segment},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "region"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: exposureAction}},
		})

		correlatedAction := fmt.Sprintf("mixed-correlated-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: correlatedAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, signalNSlot)
				if err != nil {
					return err
				}
				severity, err := steadyStateBindingIntAtSlot(ctx, 0, signalSeveritySlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(correlated.Key(),
					steadyStateIntValue(n),
					steadyStateIntValue(peer),
					steadyStateIntValue(region),
					steadyStateIntValue(severity+mixedCascadePeerSeverityBias(peer)),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-correlated-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				{
					Binding:     "peer",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: peer},
						{Field: "region", Operator: FieldConstraintEqual, Value: region},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "region"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: correlatedAction}},
		})

		caseAction := fmt.Sprintf("mixed-case-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: caseAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, signalNSlot)
				if err != nil {
					return err
				}
				amount, err := steadyStateBindingIntAtSlot(ctx, 1, exposureAmountSlot)
				if err != nil {
					return err
				}
				correlatedSeverity, err := steadyStateBindingIntAtSlot(ctx, 2, correlatedSeveritySlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(caseFact.Key(),
					steadyStateIntValue(customer),
					steadyStateIntValue(n),
					steadyStateStringValue(mixedCascadePriority(amount, correlatedSeverity)),
					steadyStateIntValue(region),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-case-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "severity", Operator: FieldConstraintGreaterOrEqual, Value: 10},
					},
				},
				{
					Binding:     "exposure",
					TemplateKey: exposure.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "amount", Operator: FieldConstraintGreaterOrEqual, Value: 100},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
						{Field: "customer", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "customer"}},
					},
				},
				{
					Binding:     "correlated",
					TemplateKey: correlated.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "region"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: caseAction}},
		})

		escalationAction := fmt.Sprintf("mixed-escalation-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: escalationAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, signalNSlot)
				if err != nil {
					return err
				}
				priorityValue, ok := ctx.bindingScalarValueAtSlot(2, policyPrioritySlot)
				if !ok || priorityValue.Kind() != ValueString {
					return fmt.Errorf("missing policy priority")
				}
				return ctx.AssertTemplateValues(escalation.Key(),
					steadyStateIntValue(customer),
					steadyStateIntValue(n),
					steadyStateStringValue(mixedCascadeReason(priorityValue.stringValue, n)),
					steadyStateIntValue(region),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-escalation-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				{
					Binding:     "case",
					TemplateKey: caseFact.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "priority", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
						{Field: "customer", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "customer"}},
					},
				},
				{
					Binding:     "policy",
					TemplateKey: policy.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Value: region},
						{Field: "segment", Operator: FieldConstraintEqual, Value: segment},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "region"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: escalationAction}},
		})

		auditAction := fmt.Sprintf("mixed-audit-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: auditAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, signalNSlot)
				if err != nil {
					return err
				}
				return ctx.AssertTemplateValues(audit.Key(),
					steadyStateStringValue(mixedCascadeAuditCode(stream, n)),
					steadyStateIntValue(customer),
					steadyStateIntValue(n),
					steadyStateIntValue(region),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-audit-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				{
					Binding:     "case",
					TemplateKey: caseFact.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
					},
				},
				{
					Binding:     "escalation",
					TemplateKey: escalation.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "signal", Field: "n"}},
						{Field: "customer", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "case", Field: "customer"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: auditAction}},
		})

		completeAction := fmt.Sprintf("mixed-complete-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: completeAction,
			Fn: func(ctx ActionContext) error {
				return ctx.AssertTemplateValues(complete.Key(),
					steadyStateIntValue(region),
					steadyStateIntValue(stream),
				)
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("mixed-complete-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "tick",
					TemplateKey: tick.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
					},
				},
				{
					Binding:     "audit",
					TemplateKey: audit.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "customer", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "tick", Field: "customer"}},
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "tick", Field: "region"}},
					},
				},
				{
					Binding:     "policy",
					TemplateKey: policy.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Value: region},
						{Field: "segment", Operator: FieldConstraintEqual, Value: segment},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "tick", Field: "region"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: completeAction}},
		})
	}

	return mustCompileWorkspace(t, workspace)
}

func mixedCascadeTemplate(name string, fields []FieldSpec, duplicateKeys []string) TemplateSpec {
	return TemplateSpec{
		Name:              name,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: duplicateKeys,
		Fields:            fields,
	}
}

func mixedCascadeSlot(t testing.TB, template Template, field string) int {
	t.Helper()
	slot, ok := template.fieldSlot(field)
	if !ok {
		t.Fatalf("%s template missing %s slot", template.Key(), field)
	}
	return slot
}

func mustSeedMixedCascadeScalingSession(t testing.TB, revision *Ruleset, tc mixedCascadeScalingCase) *Session {
	t.Helper()

	session := mustSession(t, revision, SessionID(fmt.Sprintf("mixed-cascade-scaling-%d-%d", tc.streams, tc.limit)))
	if session.rete == nil {
		t.Fatal("session Rete runtime is nil")
	}
	seedMixedCascadeScalingSession(t, session, tc)
	return session
}

func seedMixedCascadeScalingSession(t testing.TB, session *Session, tc mixedCascadeScalingCase) {
	t.Helper()

	ctx := context.Background()
	for region := 0; region < mixedCascadeRegionCount(); region++ {
		for segment := 0; segment < mixedCascadeSegmentCount(); segment++ {
			if _, err := session.AssertTemplate(ctx, TemplateKey("policy"), Fields{
				"region":   steadyStateIntValue(region),
				"segment":  steadyStateStringValue(mixedCascadeSegmentName(segment)),
				"limit":    steadyStateIntValue(mixedCascadePolicyLimit(region, segment)),
				"priority": steadyStateStringValue(mixedCascadePolicyPriority(region, segment)),
			}); err != nil {
				t.Fatalf("AssertTemplate(policy region=%d segment=%d): %v", region, segment, err)
			}
		}
	}
	for stream := 0; stream < tc.streams; stream++ {
		region := mixedCascadeRegion(stream)
		customer := mixedCascadeCustomer(stream)
		if _, err := session.AssertTemplate(ctx, TemplateKey("account"), Fields{
			"customer": steadyStateIntValue(customer),
			"region":   steadyStateIntValue(region),
			"segment":  steadyStateStringValue(mixedCascadeSegment(stream)),
			"tier":     steadyStateStringValue(mixedCascadeTier(stream)),
		}); err != nil {
			t.Fatalf("AssertTemplate(account stream=%d): %v", stream, err)
		}
		if _, err := session.AssertTemplate(ctx, TemplateKey("tick"), Fields{
			"stream":   steadyStateIntValue(stream),
			"n":        steadyStateIntValue(0),
			"customer": steadyStateIntValue(customer),
			"region":   steadyStateIntValue(region),
		}); err != nil {
			t.Fatalf("AssertTemplate(tick stream=%d): %v", stream, err)
		}
	}
}

func collectMixedCascadeScalingPropagationCounters(t testing.TB, revision *Ruleset, tc mixedCascadeScalingCase) propagationCounterSnapshot {
	t.Helper()

	ctx := context.Background()
	session := mustSeedMixedCascadeScalingSession(t, revision, tc)
	session.attachPropagationCounters()

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateMixedCascadeHarnessSession(t, session, result, tc, "counter")
	return session.propagationCounterSnapshot()
}

func validateMixedCascadeHarnessSession(t testing.TB, session *Session, result RunResult, tc mixedCascadeScalingCase, phase string) {
	t.Helper()

	if result.Status != RunCompleted || result.Fired != mixedCascadeFiredCount(tc) {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, mixedCascadeFiredCount(tc))
	}
	if got := len(session.factsByID); got != mixedCascadeFinalFacts(tc) {
		t.Fatalf("%s final fact count = %d, want %d", phase, got, mixedCascadeFinalFacts(tc))
	}

	perStep := tc.streams * (tc.limit + 1)
	assertMixedCascadeTemplateCount(t, session, phase, "account", tc.streams)
	assertMixedCascadeTemplateCount(t, session, phase, "policy", mixedCascadePolicyFacts())
	assertMixedCascadeTemplateCount(t, session, phase, "tick", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "signal", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "exposure", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "correlated", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "cascade-case", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "escalation", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "cascade-audit", perStep)
	assertMixedCascadeTemplateCount(t, session, phase, "cascade-complete", tc.streams)
	assertMixedCascadeSelectedFacts(t, session, tc)
}

func assertMixedCascadeTemplateCount(t testing.TB, session *Session, phase string, template string, want int) {
	t.Helper()
	session.ensureFactTargetIndexes()
	if got := len(session.factsByTemplate[TemplateKey(template)]); got != want {
		t.Fatalf("%s %s fact count = %d, want %d", phase, template, got, want)
	}
}

func assertMixedCascadeSelectedFacts(t testing.TB, session *Session, tc mixedCascadeScalingCase) {
	t.Helper()

	for stream := 0; stream < tc.streams; stream++ {
		region := mixedCascadeRegion(stream)
		customer := mixedCascadeCustomer(stream)
		for _, n := range largeSteadyStateSelectedNs(tc.limit) {
			severity := mixedCascadeSeverity(n, stream)
			limitValue := mixedCascadePolicyLimit(region, stream%mixedCascadeSegmentCount())
			amount := mixedCascadeAmount(severity, limitValue)
			peer := mixedCascadePeer(stream, tc.streams)
			correlatedSeverity := severity + mixedCascadePeerSeverityBias(peer)
			assertLargeSteadyStateFact(t, session, "tick", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"customer": steadyStateIntValue(customer),
				"region":   steadyStateIntValue(region),
			})
			assertLargeSteadyStateFact(t, session, "signal", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"customer": steadyStateIntValue(customer),
				"region":   steadyStateIntValue(region),
				"kind":     steadyStateStringValue(mixedCascadeSignalKind(n, stream)),
				"severity": steadyStateIntValue(severity),
			})
			assertLargeSteadyStateFact(t, session, "exposure", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"customer": steadyStateIntValue(customer),
				"region":   steadyStateIntValue(region),
				"bucket":   steadyStateStringValue(mixedCascadeBucket(severity)),
				"amount":   steadyStateIntValue(amount),
			})
			assertLargeSteadyStateFact(t, session, "correlated", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"peer":     steadyStateIntValue(peer),
				"region":   steadyStateIntValue(region),
				"severity": steadyStateIntValue(correlatedSeverity),
			})
			assertLargeSteadyStateFact(t, session, "cascade-case", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"customer": steadyStateIntValue(customer),
				"region":   steadyStateIntValue(region),
				"priority": steadyStateStringValue(mixedCascadePriority(amount, correlatedSeverity)),
			})
			assertLargeSteadyStateFact(t, session, "escalation", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"customer": steadyStateIntValue(customer),
				"region":   steadyStateIntValue(region),
				"reason":   steadyStateStringValue(mixedCascadeReason(mixedCascadePolicyPriority(region, stream%mixedCascadeSegmentCount()), n)),
			})
			assertLargeSteadyStateFact(t, session, "cascade-audit", map[string]Value{
				"stream":   steadyStateIntValue(stream),
				"n":        steadyStateIntValue(n),
				"customer": steadyStateIntValue(customer),
				"region":   steadyStateIntValue(region),
				"code":     steadyStateStringValue(mixedCascadeAuditCode(stream, n)),
			})
		}
		assertLargeSteadyStateFact(t, session, "cascade-complete", map[string]Value{
			"stream": steadyStateIntValue(stream),
			"region": steadyStateIntValue(region),
		})
	}
}

func mixedCascadeHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
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

func mixedCascadeTemplateCount() int { return 10 }

func mixedCascadeRuleCount(tc mixedCascadeScalingCase) int { return tc.streams * 8 }

func mixedCascadeInitialFacts(tc mixedCascadeScalingCase) int {
	return tc.streams*2 + mixedCascadePolicyFacts()
}

func mixedCascadeFinalFacts(tc mixedCascadeScalingCase) int {
	return tc.streams*(7*(tc.limit+1)+2) + mixedCascadePolicyFacts()
}

func mixedCascadeFiredCount(tc mixedCascadeScalingCase) int {
	return tc.streams * (7*tc.limit + 7)
}

func mixedCascadeRHSAsserts(tc mixedCascadeScalingCase) int {
	return mixedCascadeFinalFacts(tc) - mixedCascadeInitialFacts(tc)
}

func mixedCascadeRegionCount() int { return 2 }

func mixedCascadeSegmentCount() int { return 2 }

func mixedCascadePolicyFacts() int { return mixedCascadeRegionCount() * mixedCascadeSegmentCount() }

func mixedCascadeRegion(stream int) int { return stream % mixedCascadeRegionCount() }

func mixedCascadeSegment(stream int) string {
	return mixedCascadeSegmentName(stream % mixedCascadeSegmentCount())
}

func mixedCascadeSegmentName(segment int) string {
	if segment%2 == 0 {
		return "retail"
	}
	return "commercial"
}

func mixedCascadeCustomer(stream int) int { return 100000 + stream }

func mixedCascadePeer(stream, streams int) int {
	if streams <= 0 {
		return stream
	}
	return (stream + mixedCascadeRegionCount()) % streams
}

func mixedCascadeTier(stream int) string {
	if stream%3 == 0 {
		return "gold"
	}
	return "standard"
}

func mixedCascadePolicyLimit(region, segment int) int {
	return 100 + region*25 + segment*15
}

func mixedCascadePolicyPriority(region, segment int) string {
	if (region+segment)%2 == 0 {
		return "elevated"
	}
	return "normal"
}

func mixedCascadeSignalKind(n, stream int) string {
	if (n+stream)%5 == 0 {
		return "shared"
	}
	return "local"
}

func mixedCascadeSeverity(n, stream int) int {
	return 20 + ((n*7 + stream*3) % 80)
}

func mixedCascadePeerSeverityBias(peer int) int {
	return peer % 11
}

func mixedCascadeBucket(severity int) string {
	if severity >= 75 {
		return "critical"
	}
	if severity >= 50 {
		return "watch"
	}
	return "normal"
}

func mixedCascadeAmount(severity, limitValue int) int {
	return limitValue + severity*3
}

func mixedCascadePriority(amount, severity int) string {
	if amount+severity >= 390 {
		return "urgent"
	}
	if amount+severity >= 260 {
		return "elevated"
	}
	return "normal"
}

func mixedCascadeReason(priority string, n int) string {
	if n%16 == 0 {
		return "periodic-" + priority
	}
	return "cascade-" + priority
}

func mixedCascadeAuditCode(stream, n int) string {
	return fmt.Sprintf("M%d-%d", stream%mixedCascadeRegionCount(), n%13)
}
