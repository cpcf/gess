package gess

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSemanticScenario(t *testing.T) {
	t.Run("classification", func(t *testing.T) {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		classification := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "classification",
			Fields: []FieldSpec{
				{Name: "subject", Kind: ValueString, Required: true},
				{Name: "label", Kind: ValueString, Required: true},
			},
		})

		var trace []string
		mustAddAction(t, workspace, recordScenarioAction(&trace, "classify-adult", func(ctx ActionContext) error {
			personFact, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			fields := personFact.Fields()
			_, err := ctx.AssertTemplate(classification.Key(), mustFields(t, map[string]any{
				"subject": fields["name"],
				"label":   "adult",
			}))
			return err
		}))
		mustAddRule(t, workspace, RuleSpec{
			Name: "classify-adult",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "person",
					TemplateKey: person.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "age", Operator: FieldConstraintGreaterOrEqual, Value: 18},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "classify-adult"}},
		})

		revision := mustCompileWorkspace(t, workspace)
		session := mustScenarioSession(t, revision, "semantic-classification-session")
		if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"name": "Ada",
			"age":  21,
		})); err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		if got, want := trace, []string{"classify-adult"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("action trace = %#v, want %#v", got, want)
		}

		snapshot := mustSnapshot(t, context.Background(), session)
		derived := mustScenarioFact(t, snapshot, classification.Key(), map[string]any{
			"subject": "Ada",
			"label":   "adult",
		})
		if got, want := scenarioFactSummary(derived, "subject", "label"), "classification{subject="+scenarioValueString(t, "Ada")+",label="+scenarioValueString(t, "adult")+"}"; got != want {
			t.Fatalf("derived fact summary = %q, want %q", got, want)
		}
	})

	t.Run("join-driven", func(t *testing.T) {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "dept", Kind: ValueString, Required: true},
			},
		})
		department := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "department",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
			},
		})
		match := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "match",
			Fields: []FieldSpec{
				{Name: "person", Kind: ValueString, Required: true},
				{Name: "department", Kind: ValueString, Required: true},
			},
		})

		var trace []string
		mustAddAction(t, workspace, recordScenarioAction(&trace, "join-match", func(ctx ActionContext) error {
			personFact, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			departmentFact, ok := ctx.Binding("department")
			if !ok {
				return errors.New("missing department binding")
			}
			personFields := personFact.Fields()
			departmentFields := departmentFact.Fields()
			_, err := ctx.AssertTemplate(match.Key(), mustFields(t, map[string]any{
				"person":     personFields["name"],
				"department": departmentFields["name"],
			}))
			return err
		}))
		mustAddRule(t, workspace, RuleSpec{
			Name: "person-department-match",
			Conditions: []RuleConditionSpec{
				{Binding: "person", TemplateKey: person.Key()},
				{
					Binding:     "department",
					TemplateKey: department.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "name", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "person", Field: "dept"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "join-match"}},
		})

		revision := mustCompileWorkspace(t, workspace)
		session := mustScenarioSession(t, revision, "semantic-join-session")
		for _, fact := range []map[string]any{
			{"name": "Ada", "dept": "Engineering"},
			{"name": "Ben", "dept": "Sales"},
		} {
			if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, fact)); err != nil {
				t.Fatalf("AssertTemplate(person): %v", err)
			}
		}
		for _, fact := range []map[string]any{
			{"name": "Engineering"},
			{"name": "Support"},
		} {
			if _, err := session.AssertTemplate(context.Background(), department.Key(), mustFields(t, fact)); err != nil {
				t.Fatalf("AssertTemplate(department): %v", err)
			}
		}

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		if got, want := trace, []string{"join-match"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("action trace = %#v, want %#v", got, want)
		}

		snapshot := mustSnapshot(t, context.Background(), session)
		derived := mustScenarioFact(t, snapshot, match.Key(), map[string]any{
			"person":     "Ada",
			"department": "Engineering",
		})
		if got, want := scenarioFactSummary(derived, "person", "department"), "match{person="+scenarioValueString(t, "Ada")+",department="+scenarioValueString(t, "Engineering")+"}"; got != want {
			t.Fatalf("derived fact summary = %q, want %q", got, want)
		}
	})

	t.Run("same-fact-self-join", func(t *testing.T) {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "group", Kind: ValueString, Required: true},
			},
		})

		var trace []string
		mustAddAction(t, workspace, ActionSpec{
			Name: "self-join",
			Fn: func(ctx ActionContext) error {
				left, ok := ctx.Binding("left")
				if !ok {
					return errors.New("missing left binding")
				}
				right, ok := ctx.Binding("right")
				if !ok {
					return errors.New("missing right binding")
				}
				if left.ID() != right.ID() {
					trace = append(trace, "same-fact:false")
					return errors.New("expected same fact to satisfy both bindings")
				}
				trace = append(trace, "same-fact:true")
				return nil
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "same-fact-join",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: person.Key()},
				{
					Binding:     "right",
					TemplateKey: person.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "group"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "self-join"}},
		})

		revision := mustCompileWorkspace(t, workspace)
		session := mustScenarioSession(t, revision, "semantic-self-join-session")
		if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"name":  "Ada",
			"group": "ops",
		})); err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}

		result, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		if got, want := trace, []string{"same-fact:true"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("action trace = %#v, want %#v", got, want)
		}
	})

	t.Run("firing-order", func(t *testing.T) {
		trace, eventTrace := runOrderingScenarioTrace(t)
		if got, want := trace, "salience-first|recent-new|recent-old|decl-first|decl-second"; got != want {
			t.Fatalf("action trace = %q, want %q", got, want)
		}
		if got, want := eventTrace, "fired:salience-first|fired:recent-new|fired:recent-old|fired:decl-first|fired:decl-second"; got != want {
			t.Fatalf("event trace = %q, want %q", got, want)
		}
	})

	t.Run("session-isolation", func(t *testing.T) {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString, Required: true},
			},
		})
		audit := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "audit",
			Fields: []FieldSpec{
				{Name: "subject", Kind: ValueString, Required: true},
			},
		})
		subjectBySession := map[SessionID]string{
			"semantic-session-a": "Ada",
			"semantic-session-b": "Grace",
		}

		mustAddAction(t, workspace, ActionSpec{
			Name: "classify",
			Fn: func(ctx ActionContext) error {
				subject, ok := subjectBySession[ctx.SessionID()]
				if !ok {
					return fmt.Errorf("unexpected session %q", ctx.SessionID())
				}
				_, err := ctx.AssertTemplate(audit.Key(), mustFields(t, map[string]any{
					"subject": subject,
				}))
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "classify",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "person",
					TemplateKey: person.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "classify"}},
		})

		revision := mustCompileWorkspace(t, workspace)
		collectorA := &testEventCollector{}
		collectorB := &testEventCollector{}

		sessionA := mustScenarioSession(t, revision, "semantic-session-a", WithEventListener(collectorA))
		sessionB := mustScenarioSession(t, revision, "semantic-session-b", WithEventListener(collectorB))

		if _, err := sessionA.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"name":   "Ada",
			"status": "pending",
		})); err != nil {
			t.Fatalf("AssertTemplate(session A): %v", err)
		}
		if _, err := sessionB.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"name":   "Grace",
			"status": "pending",
		})); err != nil {
			t.Fatalf("AssertTemplate(session B): %v", err)
		}

		resultA1, err := sessionA.Run(context.Background())
		if err != nil {
			t.Fatalf("Run(session A first): %v", err)
		}
		if resultA1.Status != RunCompleted || resultA1.Fired != 1 {
			t.Fatalf("session A first run = (%v, %d), want (%v, 1)", resultA1.Status, resultA1.Fired, RunCompleted)
		}
		resultA2, err := sessionA.Run(context.Background())
		if err != nil {
			t.Fatalf("Run(session A second): %v", err)
		}
		if resultA2.Status != RunCompleted || resultA2.Fired != 0 {
			t.Fatalf("session A second run = (%v, %d), want (%v, 0)", resultA2.Status, resultA2.Fired, RunCompleted)
		}
		resultB, err := sessionB.Run(context.Background())
		if err != nil {
			t.Fatalf("Run(session B): %v", err)
		}
		if resultB.Status != RunCompleted || resultB.Fired != 1 {
			t.Fatalf("session B run = (%v, %d), want (%v, 1)", resultB.Status, resultB.Fired, RunCompleted)
		}
		if resultA1.RunID == resultA2.RunID {
			t.Fatalf("session A run IDs should advance between runs: %#v %#v", resultA1.RunID, resultA2.RunID)
		}

		snapshotA := mustSnapshot(t, context.Background(), sessionA)
		snapshotB := mustSnapshot(t, context.Background(), sessionB)
		if got, want := scenarioFactSummary(mustScenarioFact(t, snapshotA, audit.Key(), map[string]any{"subject": "Ada"}), "subject"), "audit{subject="+scenarioValueString(t, "Ada")+"}"; got != want {
			t.Fatalf("session A output = %q, want %q", got, want)
		}
		if got, want := scenarioFactSummary(mustScenarioFact(t, snapshotB, audit.Key(), map[string]any{"subject": "Grace"}), "subject"), "audit{subject="+scenarioValueString(t, "Grace")+"}"; got != want {
			t.Fatalf("session B output = %q, want %q", got, want)
		}

		for _, event := range collectorA.Events() {
			if event.SessionID != sessionA.ID() {
				t.Fatalf("session A event carried session ID %q, want %q", event.SessionID, sessionA.ID())
			}
		}
		for _, event := range collectorB.Events() {
			if event.SessionID != sessionB.ID() {
				t.Fatalf("session B event carried session ID %q, want %q", event.SessionID, sessionB.ID())
			}
		}
	})

	t.Run("apply-ruleset-workflow", func(t *testing.T) {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString, Required: true},
			},
		})

		var trace []string
		mustAddAction(t, workspace, recordScenarioAction(&trace, "base", nil))
		mustAddRule(t, workspace, RuleSpec{
			Name: "classify",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "person",
					TemplateKey: person.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "base"}},
		})

		revision1 := mustCompileWorkspace(t, workspace)
		session := mustScenarioSession(t, revision1, "semantic-apply-session")
		if _, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"name":   "Ada",
			"status": "pending",
		})); err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}

		result1, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run revision 1: %v", err)
		}
		if result1.Status != RunCompleted || result1.Fired != 1 {
			t.Fatalf("revision 1 run = (%v, %d), want (%v, 1)", result1.Status, result1.Fired, RunCompleted)
		}
		if got, want := trace, []string{"base"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("revision 1 trace = %#v, want %#v", got, want)
		}

		trace = nil
		mustAddAction(t, workspace, recordScenarioAction(&trace, "bonus", nil))
		mustAddRule(t, workspace, RuleSpec{
			Name:     "bonus",
			Salience: 10,
			Conditions: []RuleConditionSpec{
				{
					Binding:     "person",
					TemplateKey: person.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "bonus"}},
		})
		revision2 := mustCompileWorkspace(t, workspace)
		apply1, err := session.ApplyRuleset(context.Background(), revision2)
		if err != nil {
			t.Fatalf("ApplyRuleset revision 2: %v", err)
		}
		if apply1.Status != ApplyRulesetApplied {
			t.Fatalf("apply revision 2 status = %v, want %v", apply1.Status, ApplyRulesetApplied)
		}
		if got, want := len(apply1.AddedRuleRevisions), 1; got != want {
			t.Fatalf("revision 2 added rules = %d, want %d", got, want)
		}
		if got, want := len(apply1.UnchangedRuleRevisions), 1; got != want {
			t.Fatalf("revision 2 unchanged rules = %d, want %d", got, want)
		}

		result2, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run revision 2: %v", err)
		}
		if result2.Status != RunCompleted || result2.Fired != 1 {
			t.Fatalf("revision 2 run = (%v, %d), want (%v, 1)", result2.Status, result2.Fired, RunCompleted)
		}
		if got, want := trace, []string{"bonus"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("revision 2 trace = %#v, want %#v", got, want)
		}

		trace = nil
		if err := workspace.ReplaceRule(RuleSpec{
			Name:     "classify",
			Salience: 5,
			Conditions: []RuleConditionSpec{
				{
					Binding:     "person",
					TemplateKey: person.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "base"}},
		}); err != nil {
			t.Fatalf("ReplaceRule(classify): %v", err)
		}
		if err := workspace.RemoveRule("bonus"); err != nil {
			t.Fatalf("RemoveRule(bonus): %v", err)
		}
		if err := workspace.ReplaceAction(ActionSpec{
			Name: "base",
			Fn: func(ActionContext) error {
				trace = append(trace, "base-v2")
				return nil
			},
		}); err != nil {
			t.Fatalf("ReplaceAction(base): %v", err)
		}
		revision3 := mustCompileWorkspace(t, workspace)
		apply2, err := session.ApplyRuleset(context.Background(), revision3)
		if err != nil {
			t.Fatalf("ApplyRuleset revision 3: %v", err)
		}
		if apply2.Status != ApplyRulesetApplied {
			t.Fatalf("apply revision 3 status = %v, want %v", apply2.Status, ApplyRulesetApplied)
		}
		if got, want := len(apply2.ReplacedRuleRevisions), 1; got != want {
			t.Fatalf("revision 3 replaced rules = %d, want %d", got, want)
		}
		if got, want := len(apply2.RemovedRuleRevisions), 1; got != want {
			t.Fatalf("revision 3 removed rules = %d, want %d", got, want)
		}

		result3, err := session.Run(context.Background())
		if err != nil {
			t.Fatalf("Run revision 3: %v", err)
		}
		if result3.Status != RunCompleted || result3.Fired != 1 {
			t.Fatalf("revision 3 run = (%v, %d), want (%v, 1)", result3.Status, result3.Fired, RunCompleted)
		}
		if got, want := trace, []string{"base-v2"}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("revision 3 trace = %#v, want %#v", got, want)
		}
	})

	t.Run("action-failure", func(t *testing.T) {
		workspace := NewWorkspace()
		person := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "status", Kind: ValueString, Required: true},
			},
		})

		var trace []string
		terminalErr := errors.New("stop now")
		mustAddAction(t, workspace, ActionSpec{
			Name: "modify",
			Fn: func(ctx ActionContext) error {
				trace = append(trace, "modify")
				binding, ok := ctx.Binding("person")
				if !ok {
					return errors.New("missing person binding")
				}
				_, err := ctx.Modify(binding.ID(), FactPatch{
					Set: mustFields(t, map[string]any{"status": "done"}),
				})
				return err
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "fail",
			Fn: func(ActionContext) error {
				trace = append(trace, "fail")
				return terminalErr
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "unexpected",
			Fn: func(ActionContext) error {
				trace = append(trace, "unexpected")
				return nil
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "mutate-and-fail",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "person",
					TemplateKey: person.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "modify"}, {Name: "fail"}, {Name: "unexpected"}},
		})

		revision := mustCompileWorkspace(t, workspace)
		collector := &testEventCollector{}
		session := mustScenarioSession(t, revision, "semantic-action-failure-session", WithEventListener(collector))
		inserted, err := session.AssertTemplate(context.Background(), person.Key(), mustFields(t, map[string]any{
			"name":   "Ada",
			"status": "pending",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate(person): %v", err)
		}

		result, err := session.Run(context.Background())
		if err == nil {
			t.Fatal("expected run failure")
		}
		if result.Status != RunActionFailed {
			t.Fatalf("run status = %v, want %v", result.Status, RunActionFailed)
		}
		if result.Fired != 1 {
			t.Fatalf("run fired = %d, want 1", result.Fired)
		}
		var failure *ActionFailureError
		if !errors.As(err, &failure) {
			t.Fatalf("run error type = %T, want *ActionFailureError", err)
		}
		if !errors.Is(err, ErrActionFailed) {
			t.Fatalf("run error should satisfy ErrActionFailed: %v", err)
		}
		if !errors.Is(err, terminalErr) {
			t.Fatalf("run error should wrap terminal error: %v", err)
		}
		if failure.ActionName != "fail" || failure.ActionIndex != 1 {
			t.Fatalf("failure metadata = %#v, want fail action index 1", failure)
		}
		if got, want := trace, []string{"modify", "fail"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("action trace = %#v, want %#v", got, want)
		}
		if inserted.Fact.ID().IsZero() {
			t.Fatal("inserted fact ID is zero")
		}

		snapshot := mustSnapshot(t, context.Background(), session)
		modified := mustScenarioFact(t, snapshot, person.Key(), map[string]any{
			"name":   "Ada",
			"status": "done",
		})
		if got, want := scenarioFactSummary(modified, "name", "status"), "person{name="+scenarioValueString(t, "Ada")+",status="+scenarioValueString(t, "done")+"}"; got != want {
			t.Fatalf("modified fact summary = %q, want %q", got, want)
		}
		events := collector.Events()
		foundModified := false
		foundFailed := false
		for _, event := range events {
			switch event.Type {
			case EventFactModified:
				foundModified = true
			case EventActionFailed:
				foundFailed = true
			}
		}
		if !foundModified || !foundFailed {
			t.Fatalf("expected modified and failed events, got %#v", events)
		}
	})

	t.Run("determinism", func(t *testing.T) {
		var expected string
		for i := range 5 {
			actionTrace, eventTrace := runOrderingScenarioTrace(t)
			got := actionTrace + "||" + eventTrace
			if i == 0 {
				expected = got
				continue
			}
			if got != expected {
				t.Fatalf("scenario trace changed between runs %d and 0:\n0: %q\n%d: %q", 0, expected, i, got)
			}
		}
	})
}

func mustScenarioSession(t *testing.T, revision *Ruleset, id SessionID, opts ...SessionOption) *Session {
	t.Helper()
	options := make([]SessionOption, 0, len(opts)+1)
	options = append(options, WithSessionID(id))
	options = append(options, opts...)
	session, err := NewSession(revision, options...)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func recordScenarioAction(trace *[]string, label string, body func(ActionContext) error) ActionSpec {
	return ActionSpec{
		Name: label,
		Fn: func(ctx ActionContext) error {
			if trace != nil {
				*trace = append(*trace, label)
			}
			if body != nil {
				return body(ctx)
			}
			return nil
		},
	}
}

func mustScenarioFact(t *testing.T, snapshot Snapshot, templateKey TemplateKey, want map[string]any) FactSnapshot {
	t.Helper()
	expected := mustFields(t, want)
	for _, fact := range snapshot.FactsByTemplateKey(templateKey) {
		if fact.Fields().Equal(expected) {
			return fact
		}
	}
	t.Fatalf("snapshot missing fact for template %q with fields %#v", templateKey, want)
	return FactSnapshot{}
}

func scenarioFactSummary(fact FactSnapshot, fieldNames ...string) string {
	fields := fact.Fields()
	parts := make([]string, 0, len(fieldNames))
	for _, fieldName := range fieldNames {
		parts = append(parts, fmt.Sprintf("%s=%s", fieldName, fields[fieldName].String()))
	}
	return fmt.Sprintf("%s{%s}", fact.TemplateKey(), strings.Join(parts, ","))
}

func scenarioFiredEventSummary(revision *Ruleset, events []Event) string {
	tokens := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type != EventRuleFired {
			continue
		}
		tokens = append(tokens, "fired:"+scenarioRuleName(revision, event.RuleRevisionID))
	}
	return strings.Join(tokens, "|")
}

func scenarioRuleName(revision *Ruleset, id RuleRevisionID) string {
	if revision != nil {
		if rule, ok := revision.RuleByRevisionID(id); ok {
			return rule.Name()
		}
	}
	return id.String()
}

func scenarioValueString(t *testing.T, raw any) string {
	t.Helper()
	return mustValue(t, raw).String()
}

func runOrderingScenarioTrace(t *testing.T) (string, string) {
	t.Helper()
	workspace := NewWorkspace()
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "bucket", Kind: ValueString, Required: true},
		},
	})

	var trace []string
	mustAddAction(t, workspace, recordScenarioAction(&trace, "salience-first", nil))
	mustAddAction(t, workspace, recordScenarioAction(&trace, "recent-new", nil))
	mustAddAction(t, workspace, recordScenarioAction(&trace, "recent-old", nil))
	mustAddAction(t, workspace, recordScenarioAction(&trace, "decl-first", nil))
	mustAddAction(t, workspace, recordScenarioAction(&trace, "decl-second", nil))

	mustAddRule(t, workspace, RuleSpec{
		Name:     "salience-first",
		Salience: 30,
		Conditions: []RuleConditionSpec{
			{
				Binding:     "task",
				TemplateKey: task.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Value: "top"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "salience-first"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "recent-new",
		Salience: 20,
		Conditions: []RuleConditionSpec{
			{
				Binding:     "task",
				TemplateKey: task.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Value: "recent-new"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "recent-new"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "recent-old",
		Salience: 20,
		Conditions: []RuleConditionSpec{
			{
				Binding:     "task",
				TemplateKey: task.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Value: "recent-old"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "recent-old"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "decl-first",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{
				Binding:     "task",
				TemplateKey: task.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Value: "tie"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "decl-first"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "decl-second",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{
				Binding:     "task",
				TemplateKey: task.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "bucket", Operator: FieldConstraintEqual, Value: "tie"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "decl-second"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	collector := &testEventCollector{}
	session := mustScenarioSession(t, revision, "semantic-order-helper-session", WithEventListener(collector))
	for _, bucket := range []string{"tie", "recent-old", "recent-new", "top"} {
		if _, err := session.AssertTemplate(context.Background(), task.Key(), mustFields(t, map[string]any{"bucket": bucket})); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", bucket, err)
		}
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	if result.Fired != 5 {
		t.Fatalf("run fired = %d, want 5", result.Fired)
	}
	if got, want := trace, []string{"salience-first", "recent-new", "recent-old", "decl-first", "decl-second"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] || got[4] != want[4] {
		t.Fatalf("action trace = %#v, want %#v", got, want)
	}

	return strings.Join(trace, "|"), scenarioFiredEventSummary(revision, collector.Events())
}
