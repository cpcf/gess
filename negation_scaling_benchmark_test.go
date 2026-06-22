package gess

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

var benchmarkNegationScalingRunResult RunResult

type negationScalingCase struct {
	streams            int
	customersPerStream int
	blockEvery         int
}

func BenchmarkGessNegationScalingSeedRun(b *testing.B) {
	cases := []negationScalingCase{
		{streams: 1, customersPerStream: 128, blockEvery: 2},
		{streams: 4, customersPerStream: 512, blockEvery: 2},
		{streams: 8, customersPerStream: 1024, blockEvery: 2},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("streams=%d/customers=%d/block-every=%d/rules=%d/final-facts=%d/fired=%d",
			tc.streams, tc.customersPerStream, tc.blockEvery, tc.ruleCount(), tc.finalFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision, customerKey, blockKey := mustCompileNegationScalingRuleset(b, tc)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.streams), "streams")
			b.ReportMetric(float64(tc.customersPerStream), "customers/stream")
			b.ReportMetric(float64(tc.blockEvery), "block-every")
			b.ReportMetric(float64(tc.ruleCount()), "rules")
			b.ReportMetric(float64(tc.initialFacts()), "initial-facts")
			b.ReportMetric(float64(tc.finalFacts()), "final-facts")
			b.ReportMetric(float64(tc.firedCount()), "fired/run")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				session := mustSession(b, revision, SessionID(fmt.Sprintf("negation-scaling-benchmark-%d", i)))
				result := runNegationScalingSeedRun(b, ctx, session, customerKey, blockKey, tc)
				benchmarkNegationScalingRunResult = result
			}
			b.StopTimer()

			propagation := collectNegationScalingPropagationCounters(b, revision, customerKey, blockKey, tc)
			propagation.reportMetrics(func(name string, value float64) {
				b.ReportMetric(value, name)
			})
		})
	}
}

func mustCompileNegationScalingRuleset(t testing.TB, tc negationScalingCase) (*Ruleset, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "neg-customer",
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "tier", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "neg-block",
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "customer", Kind: ValueInt, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark-negation-customer",
		Fn:   func(ActionContext) error { return nil },
	})

	for stream := range tc.streams {
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("negation-stream-%03d", stream),
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{
					Binding:     "customer",
					TemplateKey: customer.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				Not{Condition: Match{
					Binding:     "block",
					TemplateKey: block.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "active", Operator: FieldConstraintEqual, Value: true},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "customer", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
					},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "mark-negation-customer"}},
		})
	}

	return mustCompileWorkspace(t, workspace), customer.Key(), block.Key()
}

func runNegationScalingSeedRun(t testing.TB, ctx context.Context, session *Session, customerKey, blockKey TemplateKey, tc negationScalingCase) RunResult {
	t.Helper()

	for stream := range tc.streams {
		streamValue := newIntValue(int64(stream))
		for id := range tc.customersPerStream {
			_, err := session.AssertTemplate(ctx, customerKey, Fields{
				"stream": streamValue,
				"id":     newIntValue(int64(id)),
				"tier":   newStringValue(negationScalingTier(id)),
			})
			if err != nil {
				t.Fatalf("AssertTemplate(customer): %v", err)
			}
		}
	}
	for stream := range tc.streams {
		streamValue := newIntValue(int64(stream))
		for id := 0; id < tc.customersPerStream; id += tc.blockEvery {
			_, err := session.AssertTemplate(ctx, blockKey, Fields{
				"stream":   streamValue,
				"customer": newIntValue(int64(id)),
				"active":   newBoolValue(true),
			})
			if err != nil {
				t.Fatalf("AssertTemplate(block): %v", err)
			}
		}
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateNegationScalingSession(t, session, result, tc, "benchmark")
	return result
}

func validateNegationScalingSession(t testing.TB, session *Session, result RunResult, tc negationScalingCase, phase string) {
	t.Helper()

	if result.Status != RunCompleted || result.Fired != tc.firedCount() {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, tc.firedCount())
	}
	if got := len(session.factsByID); got != tc.finalFacts() {
		t.Fatalf("%s final fact count = %d, want %d", phase, got, tc.finalFacts())
	}
}

func collectNegationScalingPropagationCounters(t testing.TB, revision *Ruleset, customerKey, blockKey TemplateKey, tc negationScalingCase) propagationCounterSnapshot {
	t.Helper()

	session := mustSession(t, revision, "negation-scaling-counter-session")
	session.attachPropagationCounters()
	result := runNegationScalingSeedRun(t, context.Background(), session, customerKey, blockKey, tc)
	validateNegationScalingSession(t, session, result, tc, "counter")
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) > 0 {
		t.Fatalf("unsupported reasons = %s", snapshot.unsupportedReasonSummary())
	}
	if snapshot.Totals.TerminalRowsInserted < tc.firedCount() {
		t.Fatalf("terminal rows inserted = %d, want at least %d", snapshot.Totals.TerminalRowsInserted, tc.firedCount())
	}
	if snapshot.TerminalRowsRetained != tc.firedCount() {
		t.Fatalf("terminal rows retained = %d, want %d", snapshot.TerminalRowsRetained, tc.firedCount())
	}
	return snapshot
}

func (tc negationScalingCase) ruleCount() int {
	return tc.streams
}

func (tc negationScalingCase) initialFacts() int {
	return tc.streams*tc.customersPerStream + tc.blockedCount()
}

func (tc negationScalingCase) finalFacts() int {
	return tc.initialFacts()
}

func (tc negationScalingCase) blockedCount() int {
	if tc.blockEvery <= 0 {
		return 0
	}
	perStream := (tc.customersPerStream + tc.blockEvery - 1) / tc.blockEvery
	return tc.streams * perStream
}

func (tc negationScalingCase) firedCount() int {
	return tc.streams*tc.customersPerStream - tc.blockedCount()
}

func negationScalingTier(id int) string {
	if id%4 == 0 {
		return "priority"
	}
	return "standard"
}

func negationScalingHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	return parseNegationScalingHarnessInt(t, name, raw)
}

func parseNegationScalingHarnessInt(t testing.TB, name, raw string) int {
	t.Helper()

	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
