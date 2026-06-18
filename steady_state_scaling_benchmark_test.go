package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkSteadyStateRunResult RunResult

type steadyStateScalingCase struct {
	streams int
	limit   int
}

func BenchmarkGessSteadyStateRuleCreatedFacts(b *testing.B) {
	cases := []steadyStateScalingCase{
		{streams: 1, limit: 8},
		{streams: 2, limit: 16},
		{streams: 4, limit: 32},
		{streams: 8, limit: 64},
	}

	for _, tc := range cases {
		ruleCount := tc.streams * 6
		finalFacts := tc.streams * (4*(tc.limit+1) + 2)
		firedCount := tc.streams * (4*tc.limit + 5)
		name := fmt.Sprintf("streams=%d/limit=%d/rules=%d/final-facts=%d/fired=%d", tc.streams, tc.limit, ruleCount, finalFacts, firedCount)
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileSteadyStateScalingRuleset(b, tc)

			b.ReportAllocs()
			b.ResetTimer()
			b.ReportMetric(float64(tc.limit), "limit")
			b.ReportMetric(float64(ruleCount), "rules")
			b.ReportMetric(float64(finalFacts), "final-facts")
			b.ReportMetric(float64(firedCount), "fired/run")
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				session := mustSeedSteadyStateScalingSession(b, revision, tc)
				b.StartTimer()
				result, err := session.Run(ctx)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				if result.Status != RunCompleted || result.Fired != firedCount {
					b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, firedCount)
				}
				if got := len(session.factsByID); got != finalFacts {
					b.Fatalf("final fact count = %d, want %d", got, finalFacts)
				}
				assertSteadyStateFactMix(b, session, tc)
				benchmarkSteadyStateRunResult = result
			}
		})
	}
}

func mustCompileSteadyStateScalingRuleset(t testing.TB, tc steadyStateScalingCase) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	step := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "step",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
		},
	})
	stepNSlot, ok := step.fieldSlot("n")
	if !ok {
		t.Fatal("step template missing n slot")
	}
	signal := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "signal",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	route := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "route",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
			{Name: "lane", Kind: ValueString, Required: true},
		},
	})
	decision := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "decision",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
			{Name: "outcome", Kind: ValueString, Required: true},
		},
	})
	done := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "done",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
		},
	})
	complete := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "complete",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
		},
	})

	for stream := 0; stream < tc.streams; stream++ {
		stream := stream
		streamValue := steadyStateIntValue(stream)
		advanceAction := fmt.Sprintf("advance-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: advanceAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				_, err = ctx.AssertTemplate(step.Key(), Fields{
					"stream": streamValue,
					"n":      steadyStateIntValue(n + 1),
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("advance-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "step",
					TemplateKey: step.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintLessThan, Value: tc.limit},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: advanceAction}},
		})

		signalAction := fmt.Sprintf("signal-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: signalAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				_, err = ctx.AssertTemplate(signal.Key(), Fields{
					"stream": streamValue,
					"n":      steadyStateIntValue(n),
					"kind":   steadyStateStringValue(steadyStateSignalKind(n)),
					"score":  steadyStateIntValue(50 + (n % 50)),
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("signal-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "step",
					TemplateKey: step.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: signalAction}},
		})

		routeAction := fmt.Sprintf("route-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: routeAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				_, err = ctx.AssertTemplate(route.Key(), Fields{
					"stream": streamValue,
					"n":      steadyStateIntValue(n),
					"lane":   steadyStateStringValue(steadyStateRouteLane(n)),
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("route-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "step",
					TemplateKey: step.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
						{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "step", Field: "n"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: routeAction}},
		})

		decisionAction := fmt.Sprintf("decision-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: decisionAction,
			Fn: func(ctx ActionContext) error {
				n, err := steadyStateBindingIntAtSlot(ctx, 0, stepNSlot)
				if err != nil {
					return err
				}
				_, err = ctx.AssertTemplate(decision.Key(), Fields{
					"stream":  streamValue,
					"n":       steadyStateIntValue(n),
					"outcome": steadyStateStringValue(steadyStateDecisionOutcome(n)),
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("decision-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "step",
					TemplateKey: step.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
						{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "step", Field: "n"}},
					},
				},
				{
					Binding:     "route",
					TemplateKey: route.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "lane", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "n", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "step", Field: "n"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: decisionAction}},
		})

		doneAction := fmt.Sprintf("finish-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: doneAction,
			Fn: func(ctx ActionContext) error {
				_, err := ctx.AssertTemplate(done.Key(), Fields{
					"stream": streamValue,
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("finish-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "decision",
					TemplateKey: decision.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: doneAction}},
		})

		completeAction := fmt.Sprintf("complete-%03d", stream)
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: completeAction,
			Fn: func(ctx ActionContext) error {
				_, err := ctx.AssertTemplate(complete.Key(), Fields{
					"stream": streamValue,
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("complete-stream-%03d", stream),
			Conditions: []RuleConditionSpec{
				{
					Binding:     "done",
					TemplateKey: done.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				{
					Binding:     "signal",
					TemplateKey: signal.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "kind", Operator: FieldConstraintNotEqual, Value: "blocked"},
						{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 50},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					},
				},
				{
					Binding:     "route",
					TemplateKey: route.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "lane", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					},
				},
				{
					Binding:     "decision",
					TemplateKey: decision.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
						{Field: "n", Operator: FieldConstraintEqual, Value: tc.limit},
						{Field: "outcome", Operator: FieldConstraintNotEqual, Value: "blocked"},
					},
					JoinConstraints: []JoinConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "done", Field: "stream"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: completeAction}},
		})
	}

	return mustCompileWorkspace(t, workspace)
}

func mustSeedSteadyStateScalingSession(t testing.TB, revision *Ruleset, tc steadyStateScalingCase) *Session {
	t.Helper()

	session := mustSession(t, revision, SessionID(fmt.Sprintf("steady-state-scaling-%d-%d", tc.streams, tc.limit)))
	if session.rete == nil {
		t.Fatal("session Rete runtime is nil")
	}
	for stream := 0; stream < tc.streams; stream++ {
		if _, err := session.AssertTemplate(context.Background(), TemplateKey("step"), Fields{
			"stream": steadyStateIntValue(stream),
			"n":      steadyStateIntValue(0),
		}); err != nil {
			t.Fatalf("AssertTemplate(step stream=%d): %v", stream, err)
		}
	}
	return session
}

func assertSteadyStateFactMix(t testing.TB, session *Session, tc steadyStateScalingCase) {
	t.Helper()

	perTemplate := tc.streams * (tc.limit + 1)
	for _, templateKey := range []TemplateKey{"step", "signal", "route", "decision"} {
		if got := len(session.factsByTemplate[templateKey]); got != perTemplate {
			t.Fatalf("%s fact count = %d, want %d", templateKey, got, perTemplate)
		}
	}
	assertSteadyStateTerminalFacts(t, session, tc, TemplateKey("done"))
	assertSteadyStateTerminalFacts(t, session, tc, TemplateKey("complete"))
}

func assertSteadyStateTerminalFacts(t testing.TB, session *Session, tc steadyStateScalingCase, templateKey TemplateKey) {
	t.Helper()

	terminalIDs := session.factsByTemplate[templateKey]
	if got := len(terminalIDs); got != tc.streams {
		t.Fatalf("%s fact count = %d, want %d", templateKey, got, tc.streams)
	}
	seen := make([]bool, tc.streams)
	for _, id := range terminalIDs {
		fact := session.factsByID[id]
		if fact == nil {
			t.Fatalf("%s fact %s missing from factsByID", templateKey, id)
		}
		stream, err := steadyStateIntField(fact.snapshot(), "stream")
		if err != nil {
			t.Fatalf("%s fact %s stream: %v", templateKey, id, err)
		}
		if stream < 0 || stream >= tc.streams {
			t.Fatalf("%s stream = %d, want range [0,%d)", templateKey, stream, tc.streams)
		}
		if seen[stream] {
			t.Fatalf("duplicate %s fact for stream %d", templateKey, stream)
		}
		seen[stream] = true
	}
	for stream, ok := range seen {
		if !ok {
			t.Fatalf("missing %s fact for stream %d", templateKey, stream)
		}
	}
}

func steadyStateIntField(fact FactSnapshot, field string) (int, error) {
	value, ok := fact.Field(field)
	if !ok || value.Kind() != ValueInt {
		return 0, fmt.Errorf("missing int field %q", field)
	}
	return int(value.data.(int64)), nil
}

func steadyStateBindingInt(ctx ActionContext, binding, field string) (int, error) {
	value, ok := ctx.bindingScalarValue(binding, field)
	if !ok || value.Kind() != ValueInt {
		return 0, fmt.Errorf("missing int field %q on binding %q", field, binding)
	}
	return int(value.data.(int64)), nil
}

func steadyStateBindingIntAt(ctx ActionContext, bindingSlot int, field string) (int, error) {
	value, ok := ctx.bindingScalarValueAt(bindingSlot, field)
	if !ok || value.Kind() != ValueInt {
		return 0, fmt.Errorf("missing int field %q on binding slot %d", field, bindingSlot)
	}
	return int(value.data.(int64)), nil
}

func steadyStateBindingIntAtSlot(ctx ActionContext, bindingSlot, fieldSlot int) (int, error) {
	value, ok := ctx.bindingScalarValueAtSlot(bindingSlot, fieldSlot)
	if !ok || value.Kind() != ValueInt {
		return 0, fmt.Errorf("missing int field slot %d on binding slot %d", fieldSlot, bindingSlot)
	}
	return int(value.data.(int64)), nil
}

func steadyStateIntValue(n int) Value {
	return Value{kind: ValueInt, data: int64(n)}
}

func steadyStateStringValue(s string) Value {
	return Value{kind: ValueString, data: s}
}

func steadyStateSignalKind(n int) string {
	if n%4 == 0 {
		return "risk"
	}
	return "routine"
}

func steadyStateRouteLane(n int) string {
	if n%8 == 0 {
		return "escalated"
	}
	return "standard"
}

func steadyStateDecisionOutcome(n int) string {
	if n%8 == 0 {
		return "review"
	}
	return "approve"
}
