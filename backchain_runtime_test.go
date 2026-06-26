package gess

import (
	"context"
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
