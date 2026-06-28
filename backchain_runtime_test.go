package gess

import (
	"context"
	"errors"
	"testing"
)

func TestBackchainDemandGenerationAssertsNeedFactOnJoinMiss(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}

	snapshot := mustSnapshot(t, ctx, session)
	demands := snapshot.FactsByTemplateKey(demandKey)
	if len(demands) != 1 {
		t.Fatalf("demands = %d, want 1", len(demands))
	}
	assertFactStringField(t, demands[0], "id", "q1")
	assertFactStringField(t, demands[0], "kind", "hardware")
	if value, ok := demands[0].Field("value"); !ok || !value.Equal(NullValue()) {
		t.Fatalf("demand value = (%v, %t), want explicit null", value, ok)
	}
	if internal := mustWorkingFactByID(t, session, demands[0].ID()); !internal.targetIndexesSkipped {
		t.Fatal("engine-owned demand fact should skip public target indexes")
	}
}

func TestBackchainDemandGenerationFeedsAnswerRuleAndOriginalGoal(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{
		Name: "provide-answer",
		Fn: func(ctx ActionContext) error {
			demand, ok := ctx.Binding("need")
			if !ok {
				t.Fatal("need binding did not resolve")
			}
			id, _ := demand.Field("id")
			kind, _ := demand.Field("kind")
			_, err := ctx.AssertTemplate(answer.Key(), Fields{
				"id":    id,
				"kind":  kind,
				"value": newStringValue("provided"),
			})
			return err
		},
	})
	consumed := 0
	mustAddAction(t, workspace, ActionSpec{
		Name: "consume-answer",
		Fn: func(ctx ActionContext) error {
			consumed++
			return nil
		},
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
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}
	demandKey := mustDemandKey(t, revision, answer.Key())
	beforeRun := mustSnapshot(t, ctx, session)
	demands := beforeRun.FactsByTemplateKey(demandKey)
	if len(demands) != 1 {
		t.Fatalf("demands before run = %d, want 1", len(demands))
	}
	demandID := demands[0].ID()
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatal("session runtime is not graph beta-backed")
	}
	terminalRows := session.rete.graphBeta.alphaFactOwnership[demandID].terminalRows
	if len(terminalRows) != 1 {
		t.Fatalf("generated demand terminal row handles = %d, want 1", len(terminalRows))
	}
	if row := session.rete.graphBeta.terminal(terminalRows[0].terminalID).rows.rowByHandle(terminalRows[0].handle); row == nil || row.token.isZero() {
		t.Fatalf("generated demand terminal row handle resolved to %#v, want live terminal row", row)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}
	if consumed != 1 {
		t.Fatalf("consume action fired %d times, want 1", consumed)
	}
	if rows := session.rete.graphBeta.alphaFactOwnership[demandID].terminalRows; len(rows) != 0 {
		t.Fatalf("generated demand terminal row handles after run = %d, want 0", len(rows))
	}
	snapshot := mustSnapshot(t, ctx, session)
	answers := snapshot.FactsByTemplateKey(answer.Key())
	if len(answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(answers))
	}
	assertFactStringField(t, answers[0], "value", "provided")
}

func TestExplicitBackchainConditionDoesNotGenerateDemand(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "explicit-answer",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Explicit{Condition: Match{
				Binding: "answer",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "id"}},
				},
				Target: TemplateKeyFact(answer.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}

	snapshot := mustSnapshot(t, ctx, session)
	if demands := snapshot.FactsByTemplateKey(demandKey); len(demands) != 0 {
		t.Fatalf("demands = %d, want 0", len(demands))
	}
}

func TestNegatedBackchainConditionDoesNotGenerateDemand(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "missing-answer",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Not{Condition: Match{
				Binding: "answer",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "id"}},
				},
				Target: TemplateKeyFact(answer.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}

	snapshot := mustSnapshot(t, ctx, session)
	if demands := snapshot.FactsByTemplateKey(demandKey); len(demands) != 0 {
		t.Fatalf("demands = %d, want 0", len(demands))
	}
}

func TestBackchainDemandNonEqualityConstraintLeavesSlotUnknown(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-non-software-answer",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Match{
				Binding: "answer",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "kind", Operator: FieldConstraintNotEqual, Value: "software"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "id"}},
				},
				Target: TemplateKeyFact(answer.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}

	demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)
	if len(demands) != 1 {
		t.Fatalf("demands = %d, want 1", len(demands))
	}
	assertFactStringField(t, demands[0], "id", "q1")
	if value, ok := demands[0].Field("kind"); !ok || !value.Equal(NullValue()) {
		t.Fatalf("demand kind = (%v, %t), want explicit null", value, ok)
	}
	if value, ok := demands[0].Field("value"); !ok || !value.Equal(NullValue()) {
		t.Fatalf("demand value = (%v, %t), want explicit null", value, ok)
	}
}

func TestBackchainQueryParametersPopulateDemandSlots(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	_, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{
		Name: "provide-query-answer",
		Fn: func(ctx ActionContext) error {
			demand, ok := ctx.Binding("need")
			if !ok {
				t.Fatal("need binding did not resolve")
			}
			id, _ := demand.Field("id")
			kind, _ := demand.Field("kind")
			_, err := ctx.AssertTemplate(answer.Key(), Fields{
				"id":    id,
				"kind":  kind,
				"value": newStringValue("generated"),
			})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "answer-query-need",
		ConditionTree: Match{
			Binding: "need",
			Target:  TemplateKeyFact(TemplateKey("need-answer")),
		},
		Actions: []RuleActionSpec{{Name: "provide-query-answer"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "answers-by-id-kind",
		Parameters: []QueryParameterSpec{
			{Name: "id", Kind: ValueString},
			{Name: "kind", Kind: ValueString},
		},
		ConditionTree: Match{
			Binding: "answer",
			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "id"}, Right: ParamExpr{Name: "id"}},
				CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "kind"}, Right: ParamExpr{Name: "kind"}},
			},
			Target: TemplateKeyFact(answer.Key()),
		},
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "answer", Field: "id"}),
			ReturnValue("kind", BindingFieldExpr{Binding: "answer", Field: "kind"}),
			ReturnValue("value", BindingFieldExpr{Binding: "answer", Field: "value"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rows, err := session.QueryAll(ctx, "answers-by-id-kind", QueryArgs{"id": "q1", "kind": "hardware"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	assertQueryRowStringValue(t, rows[0], "id", "q1")
	assertQueryRowStringValue(t, rows[0], "kind", "hardware")
	assertQueryRowStringValue(t, rows[0], "value", "generated")
	demandKey := mustDemandKey(t, revision, answer.Key())
	assertBackchainAnswerDemandAbsent(t, mustSnapshot(t, ctx, session), demandKey, "q1", "hardware")
}

func TestBackchainDemandDiagnosticsTrackActiveDemands(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}

	before := mustSnapshot(t, ctx, session).BackchainDemandDiagnostics()
	if before.Active != 1 || before.Count(demandKey) != 1 {
		t.Fatalf("backchain demand diagnostics before answer = %#v, want one active %q", before, demandKey)
	}
	if _, err := session.AssertTemplate(ctx, answer.Key(), mustFields(t, map[string]any{
		"id":    "q1",
		"kind":  "hardware",
		"value": "provided",
	})); err != nil {
		t.Fatalf("AssertTemplate(answer): %v", err)
	}
	after := mustSnapshot(t, ctx, session).BackchainDemandDiagnostics()
	if after.Active != 0 || after.Count(demandKey) != 0 {
		t.Fatalf("backchain demand diagnostics after answer = %#v, want none", after)
	}
	if counters := mustSnapshot(t, ctx, session).SupportGraph().Counters; counters.CascadeRetractions != 0 || counters.LogicalFactsRetracted != 0 {
		t.Fatalf("support counters after demand cleanup = %#v, want no logical cascade counts for generated demands", counters)
	}
}

func TestBackchainRepeatedQueryGoalsDoNotGrowDemandState(t *testing.T) {
	ctx := context.Background()
	revision, edgeKey, reachableKey, _ := mustCompileBackchainReachabilityRuleset(t, true)
	demandKey := mustDemandKey(t, revision, reachableKey)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedBackchainReachabilityEdges(t, ctx, session, edgeKey)

	rows, err := session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "internet", "dst": "db"})
	if err != nil {
		t.Fatalf("first reachable QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("first reachable rows = %d, want 1", len(rows))
	}
	afterFirst := mustSnapshot(t, ctx, session)
	firstDemandDiagnostics := afterFirst.BackchainDemandDiagnostics()
	if firstDemandDiagnostics.Active != 0 || firstDemandDiagnostics.Count(demandKey) != 0 {
		t.Fatalf("demand diagnostics after first reachable query = %#v, want none", firstDemandDiagnostics)
	}
	firstLen := afterFirst.Len()

	rows, err = session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "internet", "dst": "db"})
	if err != nil {
		t.Fatalf("second reachable QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("second reachable rows = %d, want 1", len(rows))
	}
	afterSecond := mustSnapshot(t, ctx, session)
	secondDemandDiagnostics := afterSecond.BackchainDemandDiagnostics()
	if secondDemandDiagnostics.Active != 0 || secondDemandDiagnostics.Count(demandKey) != 0 {
		t.Fatalf("demand diagnostics after second reachable query = %#v, want none", secondDemandDiagnostics)
	}
	if afterSecond.Len() != firstLen {
		t.Fatalf("snapshot fact count after repeated reachable query = %d, want %d", afterSecond.Len(), firstLen)
	}

	rows, err = session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "cache", "dst": "db"})
	if err != nil {
		t.Fatalf("first unreachable QueryAll: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("first unreachable rows = %d, want 0", len(rows))
	}
	afterFailedFirst := mustSnapshot(t, ctx, session)
	failedFirstDiagnostics := afterFailedFirst.BackchainDemandDiagnostics()
	if failedFirstDiagnostics.Active != 0 || failedFirstDiagnostics.Count(demandKey) != 0 {
		t.Fatalf("demand diagnostics after first unreachable query = %#v, want none", failedFirstDiagnostics)
	}
	failedFirstLen := afterFailedFirst.Len()

	rows, err = session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "cache", "dst": "db"})
	if err != nil {
		t.Fatalf("second unreachable QueryAll: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("second unreachable rows = %d, want 0", len(rows))
	}
	afterFailedSecond := mustSnapshot(t, ctx, session)
	failedSecondDiagnostics := afterFailedSecond.BackchainDemandDiagnostics()
	if failedSecondDiagnostics.Active != 0 || failedSecondDiagnostics.Count(demandKey) != 0 {
		t.Fatalf("demand diagnostics after second unreachable query = %#v, want none", failedSecondDiagnostics)
	}
	if afterFailedSecond.Len() != failedFirstLen {
		t.Fatalf("snapshot fact count after repeated unreachable query = %d, want %d", afterFailedSecond.Len(), failedFirstLen)
	}
}

func TestBackchainDemandRetractedWhenWaitingFactRetracted(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	inserted, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}
	if demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey); len(demands) != 1 {
		t.Fatalf("demands after assert = %d, want 1", len(demands))
	}

	if _, err := session.Retract(ctx, inserted.Fact.ID()); err != nil {
		t.Fatalf("Retract(request): %v", err)
	}

	if demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey); len(demands) != 0 {
		t.Fatalf("demands after retract = %d, want 0", len(demands))
	}
}

func TestBackchainDemandReplacedWhenJoinedFieldModified(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	inserted, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}
	before := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)
	if len(before) != 1 {
		t.Fatalf("demands before modify = %d, want 1", len(before))
	}
	assertFactStringField(t, before[0], "id", "q1")

	if _, err := session.Modify(ctx, inserted.Fact.ID(), FactPatch{Set: Fields{"id": newStringValue("q2")}}); err != nil {
		t.Fatalf("Modify(request): %v", err)
	}

	after := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)
	if len(after) != 1 {
		t.Fatalf("demands after modify = %d, want 1", len(after))
	}
	assertFactStringField(t, after[0], "id", "q2")
	if after[0].ID() == before[0].ID() {
		t.Fatalf("demand fact ID was reused after joined field changed: %v", after[0].ID())
	}
}

func TestBackchainDemandRegeneratedWhenAnswerRetracted(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}
	demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)
	if len(demands) != 1 {
		t.Fatalf("demands after request = %d, want 1", len(demands))
	}
	answerFact, err := session.AssertTemplate(ctx, answer.Key(), mustFields(t, map[string]any{
		"id":    "q1",
		"kind":  "hardware",
		"value": "provided",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate(answer): %v", err)
	}
	if demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey); len(demands) != 0 {
		t.Fatalf("demands after answer = %d, want 0", len(demands))
	}

	if _, err := session.Retract(ctx, answerFact.Fact.ID()); err != nil {
		t.Fatalf("Retract(answer): %v", err)
	}

	demands = mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)
	if len(demands) != 1 {
		t.Fatalf("demands after answer retract = %d, want 1", len(demands))
	}
	assertFactStringField(t, demands[0], "id", "q1")
}

func TestBackchainDemandResetClearsRuntimeDemands(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}
	if demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey); len(demands) != 1 {
		t.Fatalf("demands before reset = %d, want 1", len(demands))
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey); len(demands) != 0 {
		t.Fatalf("demands after reset = %d, want 0", len(demands))
	}
}

func TestBackchainDemandResetGeneratesDemandForInitialFact(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
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
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: request.Key(),
		Fields:      mustFields(t, map[string]any{"id": "q1"}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	demands := mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)
	if len(demands) != 1 {
		t.Fatalf("demands after reset = %d, want 1", len(demands))
	}
	assertFactStringField(t, demands[0], "id", "q1")
	assertFactStringField(t, demands[0], "kind", "hardware")
}

func TestBackchainRecursiveReachabilityDerivesTransitiveGoal(t *testing.T) {
	ctx := context.Background()
	revision, edgeKey, reachableKey, requestKey := mustCompileBackchainReachabilityRuleset(t, false)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, edge := range [][2]string{
		{"internet", "web"},
		{"web", "api"},
		{"api", "db"},
		{"api", "cache"},
	} {
		if err := session.AssertTemplateValues(ctx, edgeKey, newStringValue(edge[1]), newStringValue(edge[0])); err != nil {
			t.Fatalf("Assert edge %v: %v", edge, err)
		}
	}
	if err := session.AssertTemplateValues(ctx, requestKey, newStringValue("db"), newStringValue("internet")); err != nil {
		t.Fatalf("Assert request: %v", err)
	}

	before := mustSnapshot(t, ctx, session)
	if got := len(before.FactsByTemplateKey(reachableKey)); got != 0 {
		t.Fatalf("reachable facts before run = %d, want 0", got)
	}
	demandKey := mustDemandKey(t, revision, reachableKey)
	if got := len(before.FactsByTemplateKey(demandKey)); got == 0 {
		t.Fatal("expected generated need-reachable facts before run")
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 4 {
		t.Fatalf("run result = (%v, %d), want (%v, 4)", result.Status, result.Fired, RunCompleted)
	}

	snapshot := mustSnapshot(t, ctx, session)
	assertBackchainReachableFact(t, snapshot, reachableKey, "api", "db")
	assertBackchainReachableFact(t, snapshot, reachableKey, "web", "db")
	assertBackchainReachableFact(t, snapshot, reachableKey, "internet", "db")
	assertBackchainDemandAbsent(t, snapshot, demandKey, "api", "db")
	assertBackchainDemandAbsent(t, snapshot, demandKey, "web", "db")
	assertBackchainDemandAbsent(t, snapshot, demandKey, "internet", "db")
}

func TestBackchainQueryTimeDemandDerivesTransitiveGoal(t *testing.T) {
	ctx := context.Background()
	revision, edgeKey, reachableKey, _ := mustCompileBackchainReachabilityRuleset(t, true)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedBackchainReachabilityEdges(t, ctx, session, edgeKey)

	rows, err := session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "internet", "dst": "db"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	assertQueryRowStringValue(t, rows[0], "src", "internet")
	assertQueryRowStringValue(t, rows[0], "dst", "db")

	snapshot := mustSnapshot(t, ctx, session)
	assertBackchainReachableFact(t, snapshot, reachableKey, "api", "db")
	assertBackchainReachableFact(t, snapshot, reachableKey, "web", "db")
	assertBackchainReachableFact(t, snapshot, reachableKey, "internet", "db")
	demandKey := mustDemandKey(t, revision, reachableKey)
	assertBackchainDemandAbsent(t, snapshot, demandKey, "api", "db")
	assertBackchainDemandAbsent(t, snapshot, demandKey, "web", "db")
	assertBackchainDemandAbsent(t, snapshot, demandKey, "internet", "db")
	if got := queryTerminalRowsRetained(session.rete.graphBeta, "reachable-paths"); got != 0 {
		t.Fatalf("query terminal rows retained after QueryAll cleanup = %d, want 0", got)
	}
}

func TestBackchainQueryTimeDemandReturnsZeroRowsForUnreachableGoal(t *testing.T) {
	ctx := context.Background()
	revision, edgeKey, reachableKey, _ := mustCompileBackchainReachabilityRuleset(t, true)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedBackchainReachabilityEdges(t, ctx, session, edgeKey)

	rows, err := session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "cache", "dst": "db"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0: %#v", len(rows), rows)
	}
	snapshot := mustSnapshot(t, ctx, session)
	demandKey := mustDemandKey(t, revision, reachableKey)
	assertBackchainDemandAbsent(t, snapshot, demandKey, "cache", "db")
	if got := queryTerminalRowsRetained(session.rete.graphBeta, "reachable-paths"); got != 0 {
		t.Fatalf("query terminal rows retained after QueryAll cleanup = %d, want 0", got)
	}
}

func TestBackchainSnapshotQueryTimeDemandRemainsUnsupported(t *testing.T) {
	ctx := context.Background()
	revision, edgeKey, _, _ := mustCompileBackchainReachabilityRuleset(t, true)
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedBackchainReachabilityEdges(t, ctx, session, edgeKey)
	snapshot := mustSnapshot(t, ctx, session)

	_, err = snapshot.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "internet", "dst": "db"})
	if err == nil {
		t.Fatal("Snapshot QueryAll unexpectedly succeeded")
	}
	if !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("Snapshot QueryAll error = %v, want ErrUnsupportedRuntime", err)
	}
	if !errors.Is(err, ErrQueryExecution) {
		t.Fatalf("Snapshot QueryAll error = %v, want ErrQueryExecution wrapper", err)
	}
}

func mustBackchainDemandTemplates(t testing.TB, workspace *Workspace) (Template, Template) {
	t.Helper()
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
	return request, answer
}

func mustCompileBackchainReachabilityRuleset(t testing.TB, includeQuery bool) (*Ruleset, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	edge := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "edge",
		DuplicatePolicy: DuplicateUniqueKey,
		DuplicateKeyNames: []string{
			"src",
			"dst",
		},
		Fields: []FieldSpec{
			{Name: "src", Kind: ValueString, Required: true},
			{Name: "dst", Kind: ValueString, Required: true},
		},
	})
	reachable := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "reachable",
		BackchainReactive: true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{
			"src",
			"dst",
		},
		Fields: []FieldSpec{
			{Name: "src", Kind: ValueString, Required: true},
			{Name: "dst", Kind: ValueString, Required: true},
		},
	})
	request := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "reachability-request",
		DuplicatePolicy: DuplicateUniqueKey,
		DuplicateKeyNames: []string{
			"src",
			"dst",
		},
		Fields: []FieldSpec{
			{Name: "src", Kind: ValueString, Required: true},
			{Name: "dst", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-direct-reachable",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: reachable.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "need", Field: "dst"},
				BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-transitive-reachable",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: reachable.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "need", Field: "dst"},
				BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:         "consume-reachable",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "direct-reachability",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "need", Target: TemplateKeyFact(TemplateKey("need-reachable"))},
			Match{
				Binding: "edge",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: TemplateKeyFact(edge.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "assert-direct-reachable"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "transitive-reachability",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "need", Target: TemplateKeyFact(TemplateKey("need-reachable"))},
			Match{
				Binding: "edge",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "src"}},
				},
				Target: TemplateKeyFact(edge.Key()),
			},
			Match{
				Binding: "tail",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "edge", Field: "dst"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: TemplateKeyFact(reachable.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "assert-transitive-reachable"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "consume-request-reachability",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Match{
				Binding: "reachable",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "src"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "dst"}},
				},
				Target: TemplateKeyFact(reachable.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "consume-reachable"}},
	})
	if includeQuery {
		if err := workspace.AddQuery(QuerySpec{
			Name: "reachable-paths",
			Parameters: []QueryParameterSpec{
				{Name: "src", Kind: ValueString},
				{Name: "dst", Kind: ValueString},
			},
			ConditionTree: Match{
				Binding: "reachable",
				Predicates: []ExpressionSpec{
					CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "src"}, Right: ParamExpr{Name: "src"}},
					CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "dst"}, Right: ParamExpr{Name: "dst"}},
				},
				Target: TemplateKeyFact(reachable.Key()),
			},
			Returns: []QueryReturnSpec{
				ReturnValue("src", BindingFieldExpr{Binding: "reachable", Field: "src"}),
				ReturnValue("dst", BindingFieldExpr{Binding: "reachable", Field: "dst"}),
			},
		}); err != nil {
			t.Fatalf("AddQuery(reachable-paths): %v", err)
		}
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, edge.Key(), reachable.Key(), request.Key()
}

func mustDemandKey(t testing.TB, revision *Ruleset, source TemplateKey) TemplateKey {
	t.Helper()
	template, ok := revision.TemplateByKey(source)
	if !ok {
		t.Fatalf("TemplateByKey(%q) missing", source)
	}
	demandKey, ok := template.BackchainDemandTemplateKey()
	if !ok {
		t.Fatalf("template %q missing demand key", template.Name())
	}
	return demandKey
}

func seedBackchainReachabilityEdges(t testing.TB, ctx context.Context, session *Session, edgeKey TemplateKey) {
	t.Helper()
	for _, edge := range [][2]string{
		{"internet", "web"},
		{"web", "api"},
		{"api", "db"},
		{"api", "cache"},
	} {
		if err := session.AssertTemplateValues(ctx, edgeKey, newStringValue(edge[1]), newStringValue(edge[0])); err != nil {
			t.Fatalf("Assert edge %v: %v", edge, err)
		}
	}
}

func assertBackchainReachableFact(t testing.TB, snapshot Snapshot, reachableKey TemplateKey, src, dst string) {
	t.Helper()
	for _, fact := range snapshot.FactsByTemplateKey(reachableKey) {
		srcValue, srcOK := fact.Field("src")
		dstValue, dstOK := fact.Field("dst")
		if srcOK && dstOK && srcValue.Equal(newStringValue(src)) && dstValue.Equal(newStringValue(dst)) {
			return
		}
	}
	t.Fatalf("reachable(%q, %q) not found in %#v", src, dst, snapshot.FactsByTemplateKey(reachableKey))
}

func assertBackchainDemandAbsent(t testing.TB, snapshot Snapshot, demandKey TemplateKey, src, dst string) {
	t.Helper()
	for _, fact := range snapshot.FactsByTemplateKey(demandKey) {
		srcValue, srcOK := fact.Field("src")
		dstValue, dstOK := fact.Field("dst")
		if srcOK && dstOK && srcValue.Equal(newStringValue(src)) && dstValue.Equal(newStringValue(dst)) {
			t.Fatalf("need-reachable(%q, %q) still present", src, dst)
		}
	}
}

func assertBackchainAnswerDemandAbsent(t testing.TB, snapshot Snapshot, demandKey TemplateKey, id, kind string) {
	t.Helper()
	for _, fact := range snapshot.FactsByTemplateKey(demandKey) {
		idValue, idOK := fact.Field("id")
		kindValue, kindOK := fact.Field("kind")
		if idOK && kindOK && idValue.Equal(newStringValue(id)) && kindValue.Equal(newStringValue(kind)) {
			t.Fatalf("need-answer(%q, %q) still present", id, kind)
		}
	}
}

func assertFactStringField(t testing.TB, fact FactSnapshot, field string, want string) {
	t.Helper()
	value, ok := fact.Field(field)
	if !ok {
		t.Fatalf("fact %q missing field %q", fact.ID(), field)
	}
	got, ok := value.AsString()
	if !ok || got != want {
		t.Fatalf("field %q = (%v, %t), want %q", field, value, ok, want)
	}
}
