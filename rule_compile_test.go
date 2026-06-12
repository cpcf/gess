package gess

import (
	"context"
	"errors"
	"testing"
)

func TestWorkspaceCompilesRulesIntoImmutableRevision(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:        "classify-adult",
		Description: "labels an adult",
		Tags:        []string{"age", "adult"},
		Salience:    10,
		Conditions: []RuleConditionSpec{
			{Binding: "p", Name: "person"},
		},
		Actions: []RuleActionSpec{
			{Name: "mark"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	rule, ok := revision.Rule("classify-adult")
	if !ok {
		t.Fatal("compiled revision did not contain classify-adult rule")
	}
	if rule.ID().IsZero() {
		t.Fatal("rule ID is zero")
	}
	if rule.RevisionID().IsZero() {
		t.Fatal("rule revision ID is zero")
	}
	if rule.Name() != "classify-adult" {
		t.Fatalf("rule name = %q, want classify-adult", rule.Name())
	}
	if rule.Description() != "labels an adult" {
		t.Fatalf("rule description = %q, want labels an adult", rule.Description())
	}
	if got := rule.DeclarationOrder(); got != 0 {
		t.Fatalf("rule declaration order = %d, want 0", got)
	}
	if got := rule.Salience(); got != 10 {
		t.Fatalf("rule salience = %d, want 10", got)
	}

	conditions := rule.Conditions()
	if len(conditions) != 1 {
		t.Fatalf("compiled conditions = %#v, want 1 condition", conditions)
	}
	if conditions[0].Binding() != "p" || conditions[0].Name() != "person" {
		t.Fatalf("condition = %#v, want binding p and name person", conditions[0])
	}
	if got := conditions[0].DeclarationOrder(); got != 0 {
		t.Fatalf("condition declaration order = %d, want 0", got)
	}

	actions := rule.Actions()
	if len(actions) != 1 {
		t.Fatalf("compiled actions = %#v, want 1 action", actions)
	}
	if actions[0].Name() != "mark" {
		t.Fatalf("action name = %q, want mark", actions[0].Name())
	}
	if got := actions[0].DeclarationOrder(); got != 0 {
		t.Fatalf("action declaration order = %d, want 0", got)
	}

	listedRules := revision.Rules()
	if len(listedRules) != 1 || listedRules[0].Name() != "classify-adult" {
		t.Fatalf("rules listing = %#v, want one classify-adult rule", listedRules)
	}
	if listedRules[0].RevisionID() != rule.RevisionID() {
		t.Fatalf("listed rule revision ID = %q, want %q", listedRules[0].RevisionID(), rule.RevisionID())
	}
	if byRevision, ok := revision.RuleByRevisionID(rule.RevisionID()); !ok || byRevision.Name() != rule.Name() {
		t.Fatalf("RuleByRevisionID(%q) = (%#v, %v), want classify-adult", rule.RevisionID(), byRevision, ok)
	}

	registeredActions := revision.Actions()
	if len(registeredActions) != 1 || registeredActions[0].Name() != "mark" {
		t.Fatalf("action listing = %#v, want one mark action", registeredActions)
	}
	if byName, ok := revision.Action("mark"); !ok || byName.Name() != "mark" {
		t.Fatalf("Action(%q) = (%#v, %v), want mark", "mark", byName, ok)
	}

	conditions[0] = RuleCondition{}
	actions[0] = RuleAction{}
	if again, ok := revision.Rule("classify-adult"); !ok {
		t.Fatal("compiled rule missing on second lookup")
	} else if again.Conditions()[0].Binding() != "p" || again.Actions()[0].Name() != "mark" {
		t.Fatalf("compiled rule leaked mutable state after caller mutation: %#v", again)
	}
}

func TestRuleRevisionIdentitySurvivesUnrelatedWorkspaceEdits(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "classify-adult",
		Conditions: []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(classify-adult): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "other",
		Conditions: []RuleConditionSpec{{Binding: "q", Name: "person"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule(other): %v", err)
	}

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	rule1, ok := revision1.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 1 missing classify-adult")
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "unused",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(unused): %v", err)
	}

	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 2 missing classify-adult")
	}
	if rule1.RevisionID() != rule2.RevisionID() {
		t.Fatalf("rule revision ID changed across unrelated edit: %q vs %q", rule1.RevisionID(), rule2.RevisionID())
	}
	if revision1.ID() == revision2.ID() {
		t.Fatalf("ruleset ID should change after adding an action, but both revisions used %q", revision1.ID())
	}
}

func TestReplacingRuleBySameNamePreservesIdentity(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}

	baseRule := RuleSpec{
		Name:        "classify-adult",
		Description: "initial",
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}
	if err := workspace.AddRule(baseRule); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	rule1, ok := revision1.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 1 missing classify-adult")
	}

	if err := workspace.ReplaceRule(baseRule); err != nil {
		t.Fatalf("ReplaceRule with identical content: %v", err)
	}
	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 2 missing classify-adult")
	}
	if rule1.ID() != rule2.ID() {
		t.Fatalf("rule ID changed after identical replace: %q vs %q", rule1.ID(), rule2.ID())
	}
	if rule1.RevisionID() != rule2.RevisionID() {
		t.Fatalf("rule revision ID changed after identical replace: %q vs %q", rule1.RevisionID(), rule2.RevisionID())
	}

	if err := workspace.ReplaceRule(RuleSpec{
		Name:        "classify-adult",
		Description: "updated",
		Salience:    50,
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("ReplaceRule with changed content: %v", err)
	}
	revision3, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 3: %v", err)
	}
	rule3, ok := revision3.Rule("classify-adult")
	if !ok {
		t.Fatal("revision 3 missing classify-adult")
	}
	if rule1.ID() != rule3.ID() {
		t.Fatalf("rule ID changed after semantic edit: %q vs %q", rule1.ID(), rule3.ID())
	}
	if rule1.RevisionID() == rule3.RevisionID() {
		t.Fatalf("rule revision ID did not change after semantic edit: %q", rule1.RevisionID())
	}
}

func TestRuleMetadataChangesDoNotChangeRuleRevisionIdentity(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:        "classify",
		Description: "initial",
		Tags:        []string{"old"},
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision1, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 1: %v", err)
	}
	rule1, ok := revision1.Rule("classify")
	if !ok {
		t.Fatal("revision 1 missing classify")
	}

	if err := workspace.ReplaceRule(RuleSpec{
		Name:        "classify",
		Description: "updated",
		Tags:        []string{"new"},
		Conditions:  []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:     []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("ReplaceRule: %v", err)
	}

	revision2, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile revision 2: %v", err)
	}
	rule2, ok := revision2.Rule("classify")
	if !ok {
		t.Fatal("revision 2 missing classify")
	}
	if rule1.RevisionID() != rule2.RevisionID() {
		t.Fatalf("metadata-only edit changed rule revision ID: %q vs %q", rule1.RevisionID(), rule2.RevisionID())
	}
	if revision1.ID() == revision2.ID() {
		t.Fatalf("ruleset ID did not change after metadata edit: %q", revision1.ID())
	}
}

func TestWorkspaceCompileRejectsDuplicateRules(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name:       "classify-adult",
		Conditions: []RuleConditionSpec{{Binding: "p", Name: "person"}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	workspace.rules = append(workspace.rules, workspace.rules[0])
	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile should reject duplicate rule names")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for duplicate rules, got %T: %v", err, err)
	}
	if validation.RuleName != "classify-adult" {
		t.Fatalf("duplicate rule validation name = %q, want classify-adult", validation.RuleName)
	}

	workspace.rules = workspace.rules[:1]
	duplicateID := workspace.rules[0]
	duplicateID.Name = "other"
	workspace.rules = append(workspace.rules, duplicateID)
	_, err = workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile should reject duplicate rule IDs")
	}
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for duplicate rule IDs, got %T: %v", err, err)
	}
	if validation.RuleName != "other" {
		t.Fatalf("duplicate rule-id validation name = %q, want other", validation.RuleName)
	}
}

func TestWorkspaceCompileRejectsInvalidRuleDefinitions(t *testing.T) {
	t.Run("missing conditions", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name:    "broken",
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject a rule without conditions")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
	})

	t.Run("duplicate binding", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}); err != nil {
			t.Fatalf("AddTemplate: %v", err)
		}
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "p", Name: "person"},
				{Binding: "p", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject duplicate bindings")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 1 {
			t.Fatalf("condition index = (%v, %d), want (true, 1)", validation.HasConditionIndex, validation.ConditionIndex)
		}
	})

	t.Run("unknown template key", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddAction(ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		}); err != nil {
			t.Fatalf("AddAction: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "p", TemplateKey: TemplateKey("person:v1")},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject unknown template keys")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 0 {
			t.Fatalf("condition index = (%v, %d), want (true, 0)", validation.HasConditionIndex, validation.ConditionIndex)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddTemplate(TemplateSpec{
			Name:   "person",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}); err != nil {
			t.Fatalf("AddTemplate: %v", err)
		}
		if err := workspace.AddRule(RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "p", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "missing"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject missing actions")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.RuleName != "broken" {
			t.Fatalf("rule name = %q, want broken", validation.RuleName)
		}
		if !validation.HasActionIndex || validation.ActionIndex != 0 {
			t.Fatalf("action index = (%v, %d), want (true, 0)", validation.HasActionIndex, validation.ActionIndex)
		}
	})
}

func TestZeroValueRuleIdentifiersAreInvalid(t *testing.T) {
	if !RuleID("").IsZero() {
		t.Fatal("empty RuleID should be zero")
	}
	if got := RuleID("").String(); got != "rule:zero" {
		t.Fatalf("zero RuleID string = %q, want rule:zero", got)
	}

	if !RuleRevisionID("").IsZero() {
		t.Fatal("empty RuleRevisionID should be zero")
	}
	if got := RuleRevisionID("").String(); got != "rule-revision:zero" {
		t.Fatalf("zero RuleRevisionID string = %q, want rule-revision:zero", got)
	}

	if !ActivationID("").IsZero() {
		t.Fatal("empty ActivationID should be zero")
	}
	if got := ActivationID("").String(); got != "activation:zero" {
		t.Fatalf("zero ActivationID string = %q, want activation:zero", got)
	}
}
