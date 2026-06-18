package gess

import (
	"context"
	"fmt"
	"strings"
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
			propagation := collectSteadyStateScalingPropagationCounters(b, revision, tc)
			propagation.reportMetrics(func(name string, value float64) {
				b.ReportMetric(value, name)
			})
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
				return ctx.AssertTemplateValues(step.Key(), steadyStateIntValue(n+1), streamValue)
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
				return ctx.AssertTemplateValues(
					signal.Key(),
					steadyStateStringValue(steadyStateSignalKind(n)),
					steadyStateIntValue(n),
					steadyStateIntValue(50+(n%50)),
					streamValue,
				)
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
				return ctx.AssertTemplateValues(
					route.Key(),
					steadyStateStringValue(steadyStateRouteLane(n)),
					steadyStateIntValue(n),
					streamValue,
				)
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
				return ctx.AssertTemplateValues(
					decision.Key(),
					steadyStateIntValue(n),
					steadyStateStringValue(steadyStateDecisionOutcome(n)),
					streamValue,
				)
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
				return ctx.AssertTemplateValues(done.Key(), streamValue)
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
				return ctx.AssertTemplateValues(complete.Key(), streamValue)
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
	seedSteadyStateScalingSession(t, session, tc)
	return session
}

func seedSteadyStateScalingSession(t testing.TB, session *Session, tc steadyStateScalingCase) {
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

func TestSteadyStateScalingRunOnlyPreservesTerminalTokenOrdering(t *testing.T) {
	ctx := context.Background()
	tc := steadyStateScalingCase{streams: 2, limit: 16}
	revision := mustCompileSteadyStateScalingRuleset(t, tc)
	sessionA, resultA, traceA := runSteadyStateScalingSessionWithTrace(t, revision, tc, "a")
	sessionB, resultB, traceB := runSteadyStateScalingSessionWithTrace(t, revision, tc, "b")
	expectedFired := tc.streams * (4*tc.limit + 5)
	expectedFacts := tc.streams * (4*(tc.limit+1) + 2)

	if resultA.Status != RunCompleted || resultA.Fired != expectedFired {
		t.Fatalf("first run result = (%v, %d), want (%v, %d)", resultA.Status, resultA.Fired, RunCompleted, expectedFired)
	}
	if resultB.Status != RunCompleted || resultB.Fired != expectedFired {
		t.Fatalf("second run result = (%v, %d), want (%v, %d)", resultB.Status, resultB.Fired, RunCompleted, expectedFired)
	}
	if got := len(sessionA.factsByID); got != expectedFacts {
		t.Fatalf("first run final fact count = %d, want %d", got, expectedFacts)
	}
	if got := len(sessionB.factsByID); got != expectedFacts {
		t.Fatalf("second run final fact count = %d, want %d", got, expectedFacts)
	}
	if got, want := strings.Join(traceA, "|"), strings.Join(traceB, "|"); got != want {
		t.Fatalf("fired trace mismatch:\nfirst=%s\nsecond=%s", got, want)
	}
	if got := len(traceA); got != expectedFired {
		t.Fatalf("first fired trace length = %d, want %d", got, expectedFired)
	}
	if got := len(traceB); got != expectedFired {
		t.Fatalf("second fired trace length = %d, want %d", got, expectedFired)
	}
	assertSteadyStateFactMix(t, sessionA, tc)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, sessionA), newNaiveMatcher(revision), sessionA.rete)
	assertSteadyStateFactMix(t, sessionB, tc)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, sessionB), newNaiveMatcher(revision), sessionB.rete)

	second, err := sessionA.Run(ctx)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Status != RunCompleted || second.Fired != 0 {
		t.Fatalf("second run result = (%v, %d), want (%v, 0)", second.Status, second.Fired, RunCompleted)
	}
	assertSteadyStateFactMix(t, sessionA, tc)
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, sessionA), newNaiveMatcher(revision), sessionA.rete)
}

func TestSteadyStateScalingPropagationCountersScale(t *testing.T) {
	small := steadyStateScalingCase{streams: 4, limit: 32}
	large := steadyStateScalingCase{streams: 8, limit: 64}

	smallSnapshot := runSteadyStateScalingWithPropagationCounters(t, small)
	largeSnapshot := runSteadyStateScalingWithPropagationCounters(t, large)

	if largeSnapshot.Totals.Asserts <= smallSnapshot.Totals.Asserts {
		t.Fatalf("assert totals did not scale: small=%d large=%d", smallSnapshot.Totals.Asserts, largeSnapshot.Totals.Asserts)
	}
	if largeSnapshot.Totals.RHSAsserts <= smallSnapshot.Totals.RHSAsserts {
		t.Fatalf("rhs assert totals did not scale: small=%d large=%d", smallSnapshot.Totals.RHSAsserts, largeSnapshot.Totals.RHSAsserts)
	}
	if largeSnapshot.Totals.RuleMemoriesVisited <= smallSnapshot.Totals.RuleMemoriesVisited {
		t.Fatalf("rule memory visits did not scale: small=%d large=%d", smallSnapshot.Totals.RuleMemoriesVisited, largeSnapshot.Totals.RuleMemoriesVisited)
	}
	if largeSnapshot.Totals.TerminalDeltasEmitted <= smallSnapshot.Totals.TerminalDeltasEmitted {
		t.Fatalf("terminal deltas did not scale: small=%d large=%d", smallSnapshot.Totals.TerminalDeltasEmitted, largeSnapshot.Totals.TerminalDeltasEmitted)
	}
	if len(largeSnapshot.ByTemplate) == 0 || len(largeSnapshot.ByOrigin) == 0 {
		t.Fatalf("counter distributions are empty: %#v", largeSnapshot)
	}
	if got := largeSnapshot.ByOrigin[propagationOriginExternal].Asserts; got == 0 {
		t.Fatalf("external origin distribution is empty: %#v", largeSnapshot.ByOrigin)
	}
	if got := largeSnapshot.ByOrigin[propagationOriginRHS].RHSAsserts; got == 0 {
		t.Fatalf("rhs origin distribution is empty: %#v", largeSnapshot.ByOrigin)
	}
}

func TestSteadyStateScalingResetRunReusesTerminalTokenLifetimeAcrossCycles(t *testing.T) {
	ctx := context.Background()
	tc := steadyStateScalingCase{streams: 2, limit: 8}
	revision := mustCompileSteadyStateScalingRuleset(t, tc)
	initials := make([]SessionInitialFact, 0, tc.streams)
	for stream := 0; stream < tc.streams; stream++ {
		initials = append(initials, SessionInitialFact{
			TemplateKey: TemplateKey("step"),
			Fields: Fields{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(0),
			},
		})
	}
	session, err := NewSession(revision, WithSessionID("steady-state-reset-cycle"), WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	expectedFired := tc.streams * (4*tc.limit + 5)
	expectedFacts := tc.streams * (4*(tc.limit+1) + 2)

	for cycle := range 3 {
		if cycle > 0 {
			if _, err := session.Reset(ctx); err != nil {
				t.Fatalf("Reset cycle %d: %v", cycle, err)
			}
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("Run cycle %d: %v", cycle, err)
		}
		if result.Status != RunCompleted || result.Fired != expectedFired {
			t.Fatalf("run cycle %d result = (%v, %d), want (%v, %d)", cycle, result.Status, result.Fired, RunCompleted, expectedFired)
		}
		if got := len(session.factsByID); got != expectedFacts {
			t.Fatalf("cycle %d final fact count = %d, want %d", cycle, got, expectedFacts)
		}
		assertSteadyStateFactMix(t, session, tc)
		assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.rete)
	}
}

func runSteadyStateScalingWithPropagationCounters(t testing.TB, tc steadyStateScalingCase) propagationCounterSnapshot {
	t.Helper()

	ctx := context.Background()
	revision := mustCompileSteadyStateScalingRuleset(t, tc)
	session := mustSession(t, revision, SessionID(fmt.Sprintf("steady-state-scaling-counters-%d-%d", tc.streams, tc.limit)))
	if session.rete == nil {
		t.Fatal("session Rete runtime is nil")
	}
	session.attachPropagationCounters()
	seedSteadyStateScalingSession(t, session, tc)

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	expectedFired := tc.streams * (4*tc.limit + 5)
	if result.Status != RunCompleted || result.Fired != expectedFired {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, expectedFired)
	}
	return session.propagationCounterSnapshot()
}

func collectSteadyStateScalingPropagationCounters(t testing.TB, revision *Ruleset, tc steadyStateScalingCase) propagationCounterSnapshot {
	t.Helper()

	ctx := context.Background()
	session := mustSeedSteadyStateScalingSession(t, revision, tc)
	session.attachPropagationCounters()

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateSteadyStateHarnessSession(t, session, result, tc, "counter")
	return session.propagationCounterSnapshot()
}

func assertSteadyStateFactMix(t testing.TB, session *Session, tc steadyStateScalingCase) {
	t.Helper()

	assertSteadyStateFacts(t, session, TemplateKey("step"), []string{"stream", "n"}, steadyStateExpectedStepFacts(tc))
	assertSteadyStateFacts(t, session, TemplateKey("signal"), []string{"stream", "n"}, steadyStateExpectedSignalFacts(tc))
	assertSteadyStateFacts(t, session, TemplateKey("route"), []string{"stream", "n"}, steadyStateExpectedRouteFacts(tc))
	assertSteadyStateFacts(t, session, TemplateKey("decision"), []string{"stream", "n"}, steadyStateExpectedDecisionFacts(tc))
	assertSteadyStateFacts(t, session, TemplateKey("done"), []string{"stream"}, steadyStateExpectedTerminalFacts(tc))
	assertSteadyStateFacts(t, session, TemplateKey("complete"), []string{"stream"}, steadyStateExpectedTerminalFacts(tc))
}

type steadyStateFactExpectation struct {
	key    string
	fields map[string]Value
}

func assertSteadyStateFacts(t testing.TB, session *Session, templateKey TemplateKey, keyFields []string, expected []steadyStateFactExpectation) {
	t.Helper()

	actualIDs := session.factsByTemplate[templateKey]
	if got, want := len(actualIDs), len(expected); got != want {
		t.Fatalf("%s fact count = %d, want %d", templateKey, got, want)
	}

	expectedByKey := make(map[string]steadyStateFactExpectation, len(expected))
	for _, row := range expected {
		if _, ok := expectedByKey[row.key]; ok {
			t.Fatalf("duplicate expected %s key %q", templateKey, row.key)
		}
		expectedByKey[row.key] = row
	}

	for _, id := range actualIDs {
		fact := session.factsByID[id]
		if fact == nil {
			t.Fatalf("%s fact %s missing from factsByID", templateKey, id)
		}
		snapshot := fact.snapshot()
		key, err := steadyStateFactKey(snapshot, keyFields)
		if err != nil {
			t.Fatalf("%s fact %s key: %v", templateKey, id, err)
		}
		row, ok := expectedByKey[key]
		if !ok {
			t.Fatalf("unexpected %s fact %s: %s", templateKey, id, snapshot.String())
		}
		assertSteadyStateFactFields(t, templateKey, id, snapshot, row.fields)
		delete(expectedByKey, key)
	}

	if len(expectedByKey) > 0 {
		for key := range expectedByKey {
			t.Fatalf("missing %s fact for key %s", templateKey, key)
		}
	}
}

func assertSteadyStateFactFields(t testing.TB, templateKey TemplateKey, id FactID, fact FactSnapshot, expected map[string]Value) {
	t.Helper()

	actual := fact.Fields()
	if got, want := len(actual), len(expected); got != want {
		t.Fatalf("%s fact %s field count = %d, want %d", templateKey, id, got, want)
	}
	for fieldName, want := range expected {
		got, ok := actual[fieldName]
		if !ok {
			t.Fatalf("%s fact %s missing field %q: %s", templateKey, id, fieldName, fact.String())
		}
		if !got.Equal(want) {
			t.Fatalf("%s fact %s field %q = %s, want %s", templateKey, id, fieldName, got.String(), want.String())
		}
	}
}

func steadyStateExpectedStepFacts(tc steadyStateScalingCase) []steadyStateFactExpectation {
	rows := make([]steadyStateFactExpectation, 0, tc.streams*(tc.limit+1))
	for stream := 0; stream < tc.streams; stream++ {
		for n := 0; n <= tc.limit; n++ {
			fields := map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
			}
			rows = append(rows, steadyStateFactExpectation{
				key:    steadyStateFactKeyFromValues(fields, "stream", "n"),
				fields: fields,
			})
		}
	}
	return rows
}

func steadyStateExpectedSignalFacts(tc steadyStateScalingCase) []steadyStateFactExpectation {
	rows := make([]steadyStateFactExpectation, 0, tc.streams*(tc.limit+1))
	for stream := 0; stream < tc.streams; stream++ {
		for n := 0; n <= tc.limit; n++ {
			fields := map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"kind":   steadyStateStringValue(steadyStateSignalKind(n)),
				"score":  steadyStateIntValue(50 + (n % 50)),
			}
			rows = append(rows, steadyStateFactExpectation{
				key:    steadyStateFactKeyFromValues(fields, "stream", "n"),
				fields: fields,
			})
		}
	}
	return rows
}

func steadyStateExpectedRouteFacts(tc steadyStateScalingCase) []steadyStateFactExpectation {
	rows := make([]steadyStateFactExpectation, 0, tc.streams*(tc.limit+1))
	for stream := 0; stream < tc.streams; stream++ {
		for n := 0; n <= tc.limit; n++ {
			fields := map[string]Value{
				"stream": steadyStateIntValue(stream),
				"n":      steadyStateIntValue(n),
				"lane":   steadyStateStringValue(steadyStateRouteLane(n)),
			}
			rows = append(rows, steadyStateFactExpectation{
				key:    steadyStateFactKeyFromValues(fields, "stream", "n"),
				fields: fields,
			})
		}
	}
	return rows
}

func steadyStateExpectedDecisionFacts(tc steadyStateScalingCase) []steadyStateFactExpectation {
	rows := make([]steadyStateFactExpectation, 0, tc.streams*(tc.limit+1))
	for stream := 0; stream < tc.streams; stream++ {
		for n := 0; n <= tc.limit; n++ {
			fields := map[string]Value{
				"stream":  steadyStateIntValue(stream),
				"n":       steadyStateIntValue(n),
				"outcome": steadyStateStringValue(steadyStateDecisionOutcome(n)),
			}
			rows = append(rows, steadyStateFactExpectation{
				key:    steadyStateFactKeyFromValues(fields, "stream", "n"),
				fields: fields,
			})
		}
	}
	return rows
}

func steadyStateExpectedTerminalFacts(tc steadyStateScalingCase) []steadyStateFactExpectation {
	rows := make([]steadyStateFactExpectation, 0, tc.streams)
	for stream := 0; stream < tc.streams; stream++ {
		fields := map[string]Value{
			"stream": steadyStateIntValue(stream),
		}
		rows = append(rows, steadyStateFactExpectation{
			key:    steadyStateFactKeyFromValues(fields, "stream"),
			fields: fields,
		})
	}
	return rows
}

func steadyStateFactKeyFromValues(fields map[string]Value, keyFields ...string) string {
	parts := make([]string, 0, len(keyFields))
	for _, fieldName := range keyFields {
		parts = append(parts, fieldName+"="+fields[fieldName].String())
	}
	return strings.Join(parts, "|")
}

func steadyStateFactKey(fact FactSnapshot, keyFields []string) (string, error) {
	parts := make([]string, 0, len(keyFields))
	for _, fieldName := range keyFields {
		value, ok := fact.Field(fieldName)
		if !ok {
			return "", fmt.Errorf("missing field %q", fieldName)
		}
		parts = append(parts, fieldName+"="+value.String())
	}
	return strings.Join(parts, "|"), nil
}

func runSteadyStateScalingSessionWithTrace(t testing.TB, revision *Ruleset, tc steadyStateScalingCase, label string) (*Session, RunResult, []string) {
	t.Helper()

	collector := &testEventCollector{}
	session, err := NewSession(
		revision,
		WithSessionID(SessionID(fmt.Sprintf("steady-state-scaling-%d-%d-%s", tc.streams, tc.limit, label))),
		WithEventListener(collector),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil {
		t.Fatal("session Rete runtime is nil")
	}
	seedSteadyStateScalingSession(t, session, tc)

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return session, result, steadyStateFiredRuleTrace(revision, collector.Events())
}

func steadyStateFiredRuleTrace(revision *Ruleset, events []Event) []string {
	trace := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type != EventRuleFired {
			continue
		}
		trace = append(trace, steadyStateRuleName(revision, event.RuleRevisionID))
	}
	return trace
}

func steadyStateRuleName(revision *Ruleset, id RuleRevisionID) string {
	if revision != nil {
		if rule, ok := revision.RuleByRevisionID(id); ok {
			return rule.Name()
		}
	}
	return id.String()
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
