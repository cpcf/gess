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
	if conditions[0].ID().IsZero() {
		t.Fatal("condition ID is zero")
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
	if got1, got2 := rule1.Conditions()[0].ID(), rule2.Conditions()[0].ID(); got1 != got2 {
		t.Fatalf("condition ID changed across unrelated edit: %q vs %q", got1, got2)
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
	if got1, got2 := rule1.Conditions()[0].ID(), rule2.Conditions()[0].ID(); got1 != got2 {
		t.Fatalf("condition ID changed after identical replace: %q vs %q", got1, got2)
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
	if got1, got3 := rule1.Conditions()[0].ID(), rule3.Conditions()[0].ID(); got1 != got3 {
		t.Fatalf("condition ID changed when condition content did not: %q vs %q", got1, got3)
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

	t.Run("missing binding", func(t *testing.T) {
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
				{Binding: " ", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject missing bindings")
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
		if validation.Reason != "condition binding is required" {
			t.Fatalf("reason = %q, want condition binding is required", validation.Reason)
		}
	})

	t.Run("invalid binding name", func(t *testing.T) {
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
				{Binding: "1person", Name: "person"},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		}); err != nil {
			t.Fatalf("AddRule: %v", err)
		}

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject invalid binding names")
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
		if validation.Reason != "invalid binding name" {
			t.Fatalf("reason = %q, want invalid binding name", validation.Reason)
		}
	})

	t.Run("invalid condition target", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			condition RuleConditionSpec
		}{
			{
				name:      "missing",
				condition: RuleConditionSpec{Binding: "p"},
			},
			{
				name:      "conflicting",
				condition: RuleConditionSpec{Binding: "p", Name: "person", TemplateKey: TemplateKey("person")},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
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
					Name:       "broken",
					Conditions: []RuleConditionSpec{tc.condition},
					Actions:    []RuleActionSpec{{Name: "mark"}},
				}); err != nil {
					t.Fatalf("AddRule: %v", err)
				}

				_, err := workspace.Compile(context.Background())
				if err == nil {
					t.Fatal("Compile should reject invalid condition targets")
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
				if validation.Reason != "condition target must be either a name or a template key" {
					t.Fatalf("reason = %q, want target validation failure", validation.Reason)
				}
			})
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

	if !ConditionID("").IsZero() {
		t.Fatal("empty ConditionID should be zero")
	}
	if got := ConditionID("").String(); got != "condition:zero" {
		t.Fatalf("zero ConditionID string = %q, want condition:zero", got)
	}
}

func TestCompiledConditionScanMatchesFactsDeterministically(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person-by-name",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Name: "person"},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person-by-template",
		Conditions: []RuleConditionSpec{
			{Binding: "person", TemplateKey: personTemplate.Key()},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	nameRule := revision.rules["match-person-by-name"]
	namePlan := nameRule.conditionPlans[0]
	if got, want := namePlan.binding, "person"; got != want {
		t.Fatalf("name plan binding = %q, want %q", got, want)
	}
	if got, want := namePlan.bindingSlot, 0; got != want {
		t.Fatalf("name plan binding slot = %d, want %d", got, want)
	}
	if got, want := namePlan.path, []int{0}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("name plan path = %#v, want %#v", got, want)
	}
	if !namePlan.indexable || namePlan.indexKind != conditionIndexName {
		t.Fatalf("name plan index metadata = (%v, %v), want (true, conditionIndexName)", namePlan.indexable, namePlan.indexKind)
	}
	if namePlan.target.kind != conditionTargetName || namePlan.target.name != "person" {
		t.Fatalf("name plan target = %#v, want name target person", namePlan.target)
	}

	templateRule := revision.rules["match-person-by-template"]
	templatePlan := templateRule.conditionPlans[0]
	if templatePlan.target.kind != conditionTargetTemplateKey || templatePlan.target.templateKey != personTemplate.Key() {
		t.Fatalf("template plan target = %#v, want template key %q", templatePlan.target, personTemplate.Key())
	}
	if !templatePlan.indexable || templatePlan.indexKind != conditionIndexTemplateKey {
		t.Fatalf("template plan index metadata = (%v, %v), want (true, conditionIndexTemplateKey)", templatePlan.indexable, templatePlan.indexKind)
	}

	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"kind": "dynamic"})); err != nil {
		t.Fatalf("Assert dynamic person: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.Assert(context.Background(), "other", mustFields(t, map[string]any{"kind": "noise"})); err != nil {
		t.Fatalf("Assert other: %v", err)
	}

	before := mustSnapshot(t, context.Background(), session)

	nameMatches := nameRule.scanCondition(before, 0)
	if got, want := len(nameMatches), 2; got != want {
		t.Fatalf("name matches = %d, want %d", got, want)
	}
	if nameMatches[0].bindingSlot != 0 || nameMatches[1].bindingSlot != 0 {
		t.Fatalf("name match binding slots = %#v, want all zero", nameMatches)
	}
	if nameMatches[0].conditionID != namePlan.id || nameMatches[1].conditionID != namePlan.id {
		t.Fatalf("name match condition IDs = %#v, want %q", nameMatches, namePlan.id)
	}
	if got, want := nameMatches[0].fact.Name(), "person"; got != want {
		t.Fatalf("first name match fact name = %q, want %q", got, want)
	}
	if got, want := nameMatches[1].fact.TemplateKey(), personTemplate.Key(); got != want {
		t.Fatalf("second name match template key = %q, want %q", got, want)
	}

	templateMatches := templateRule.scanCondition(before, 0)
	if got, want := len(templateMatches), 1; got != want {
		t.Fatalf("template matches = %d, want %d", got, want)
	}
	if templateMatches[0].bindingSlot != 0 {
		t.Fatalf("template match binding slot = %d, want 0", templateMatches[0].bindingSlot)
	}
	if templateMatches[0].conditionID != templatePlan.id {
		t.Fatalf("template match condition ID = %q, want %q", templateMatches[0].conditionID, templatePlan.id)
	}
	if got, want := templateMatches[0].fact.TemplateKey(), personTemplate.Key(); got != want {
		t.Fatalf("template match template key = %q, want %q", got, want)
	}

	after := mustSnapshot(t, context.Background(), session)
	if before.String() != after.String() {
		t.Fatalf("snapshot changed after scan: before %q after %q", before, after)
	}
}

func mustAddTemplate(t *testing.T, workspace *Workspace, spec TemplateSpec) Template {
	t.Helper()
	if err := workspace.AddTemplate(spec); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	compiled, err := compileTemplateSpec(workspace.templates[len(workspace.templates)-1])
	if err != nil {
		t.Fatalf("compileTemplateSpec: %v", err)
	}
	return compiled
}

func mustAddAction(t *testing.T, workspace *Workspace, spec ActionSpec) {
	t.Helper()
	if err := workspace.AddAction(spec); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
}

func mustAddRule(t *testing.T, workspace *Workspace, spec RuleSpec) {
	t.Helper()
	if err := workspace.AddRule(spec); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
}
