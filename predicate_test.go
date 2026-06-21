package gess

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestFieldConstraintCompileValidation(t *testing.T) {
	t.Run("missing field name", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Operator: FieldConstraintEqual, Value: 1},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject missing field names")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.Reason != "field name is required" {
			t.Fatalf("reason = %q, want field name is required", validation.Reason)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 0 {
			t.Fatalf("condition index = (%v, %d), want (true, 0)", validation.HasConditionIndex, validation.ConditionIndex)
		}
		if !validation.HasConstraintIndex || validation.ConstraintIndex != 0 {
			t.Fatalf("constraint index = (%v, %d), want (true, 0)", validation.HasConstraintIndex, validation.ConstraintIndex)
		}
	})

	t.Run("invalid operator", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "age", Operator: FieldConstraintOperator("bogus"), Value: 1},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject invalid operators")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.Reason != "invalid field constraint operator" {
			t.Fatalf("reason = %q, want invalid field constraint operator", validation.Reason)
		}
		if !validation.HasConstraintIndex || validation.ConstraintIndex != 0 {
			t.Fatalf("constraint index = (%v, %d), want (true, 0)", validation.HasConstraintIndex, validation.ConstraintIndex)
		}
	})

	t.Run("invalid constant", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Value: math.NaN()},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject invalid constants")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.Reason != "invalid constraint value" {
			t.Fatalf("reason = %q, want invalid constraint value", validation.Reason)
		}
		if !errors.Is(err, ErrUnsupportedValue) {
			t.Fatalf("compile error should unwrap to ErrUnsupportedValue, got %v", err)
		}
	})

	t.Run("exists with value", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "age", Operator: FieldConstraintExists, Value: 1},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject exists constraints with values")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.Reason != "exists constraint must not set a value" {
			t.Fatalf("reason = %q, want exists constraint must not set a value", validation.Reason)
		}
	})

	t.Run("closed template unknown field", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "name", Operator: FieldConstraintEqual, Value: "Ada"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject closed-template field references")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.TemplateName != "person" {
			t.Fatalf("template name = %q, want person", validation.TemplateName)
		}
		if validation.FieldName != "name" {
			t.Fatalf("field name = %q, want name", validation.FieldName)
		}
		if validation.Reason != "unknown field" {
			t.Fatalf("reason = %q, want unknown field", validation.Reason)
		}
		if !validation.HasConstraintIndex || validation.ConstraintIndex != 0 {
			t.Fatalf("constraint index = (%v, %d), want (true, 0)", validation.HasConstraintIndex, validation.ConstraintIndex)
		}
	})
}

func TestFieldConstraintSlotResolutionAndFallback(t *testing.T) {
	t.Run("closed template uses slot", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
				{Name: "name", Kind: ValueString, Required: true},
				{Name: "tag", Kind: ValueString},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "closed-age",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Value: 18},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		planConstraint := revision.rules["closed-age"].conditionPlans[0].constraints[0]
		if planConstraint.fieldSlot < 0 {
			t.Fatalf("field slot = %d, want non-negative", planConstraint.fieldSlot)
		}

		session, err := NewSession(revision, WithSessionID("field-slot-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		inserted, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
			"age":  18,
			"name": "Ada",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}

		snapshot := session.indexedSnapshotLocked()
		matches, err := revision.rules["closed-age"].scanCondition(context.Background(), snapshot, 0)
		if err != nil {
			t.Fatalf("scanCondition: %v", err)
		}
		if got, want := len(matches), 1; got != want {
			t.Fatalf("match count = %d, want %d", got, want)
		}
		if matches[0].fact.ID() != inserted.Fact.ID() {
			t.Fatalf("matched fact = %q, want %q", matches[0].fact.ID(), inserted.Fact.ID())
		}
	})

	t.Run("name target reads dynamic fields by name", func(t *testing.T) {
		workspace := NewWorkspace()
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "named-age",
			Conditions: []RuleConditionSpec{
				{
					Binding: "p",
					Name:    "person",
					FieldConstraints: []FieldConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Value: 18},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		planConstraint := revision.rules["named-age"].conditionPlans[0].constraints[0]
		if planConstraint.fieldSlot != -1 {
			t.Fatalf("field slot = %d, want -1 for name-target conditions", planConstraint.fieldSlot)
		}

		session, err := NewSession(revision, WithSessionID("field-fallback-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		inserted, err := session.Assert(context.Background(), "person", mustFields(t, map[string]any{"age": 18}))
		if err != nil {
			t.Fatalf("Assert: %v", err)
		}

		snapshot := session.indexedSnapshotLocked()
		matches, err := revision.rules["named-age"].scanCondition(context.Background(), snapshot, 0)
		if err != nil {
			t.Fatalf("scanCondition: %v", err)
		}
		if got, want := len(matches), 1; got != want {
			t.Fatalf("match count = %d, want %d", got, want)
		}
		if matches[0].fact.ID() != inserted.Fact.ID() {
			t.Fatalf("matched fact = %q, want %q", matches[0].fact.ID(), inserted.Fact.ID())
		}
	})

	t.Run("missing optional field does not match", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name: "person",
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueInt, Required: true},
				{Name: "tag", Kind: ValueString},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "tag-exists",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "tag", Operator: FieldConstraintExists},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "tag-eq",
			Conditions: []RuleConditionSpec{
				{
					Binding:     "p",
					TemplateKey: personTemplate.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "tag", Operator: FieldConstraintEqual, Value: "blue"},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		for _, ruleName := range []string{"tag-exists", "tag-eq"} {
			planConstraint := revision.rules[ruleName].conditionPlans[0].constraints[0]
			if planConstraint.fieldSlot < 0 {
				t.Fatalf("%s field slot = %d, want non-negative", ruleName, planConstraint.fieldSlot)
			}
		}

		session, err := NewSession(revision, WithSessionID("field-optional-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": 18})); err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}

		snapshot := session.indexedSnapshotLocked()
		for _, ruleName := range []string{"tag-exists", "tag-eq"} {
			matches, err := revision.rules[ruleName].scanCondition(context.Background(), snapshot, 0)
			if err != nil {
				t.Fatalf("scanCondition(%s): %v", ruleName, err)
			}
			if len(matches) != 0 {
				t.Fatalf("%s matched missing optional field: %#v", ruleName, matches)
			}
		}
	})
}

func TestCompiledFieldValueUsesSlotBeforeMapFallback(t *testing.T) {
	fact := FactSnapshot{
		fields: Fields{
			"tag": mustValue(t, "blue"),
		},
		fieldSlots: []factSlot{
			{},
		},
	}

	if value, ok := fact.compiledFieldValue("tag", 0); ok {
		t.Fatalf("slot value = %v, true; want missing indexed slot", value)
	}

	value, ok := fact.compiledFieldValue("tag", -1)
	if !ok || !value.Equal(mustValue(t, "blue")) {
		t.Fatalf("fallback value = (%v, %v), want blue true", value, ok)
	}
}

func TestFieldConstraintEvaluation(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "tag", Kind: ValueString},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-eq",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintEqual, Value: 18},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-neq",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintNotEqual, Value: 21},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-range",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Value: 17},
					{Field: "age", Operator: FieldConstraintLessThan, Value: 19},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "name-eq",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "name", Operator: FieldConstraintEqual, Value: "Ada"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "name-order",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "name", Operator: FieldConstraintLessThan, Value: "Bob"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "tag-exists",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "tag", Operator: FieldConstraintExists},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "incompatible",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Value: "18"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "incompatible-neq",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintNotEqual, Value: "18"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	rangeRule := revision.rules["age-range"]
	condition := rangeRule.conditions[0]
	constraints := condition.FieldConstraints()
	if got, want := len(constraints), 2; got != want {
		t.Fatalf("constraint count = %d, want %d", got, want)
	}
	if constraints[0].Operator != FieldConstraintGreaterThan || !constraints[0].Value.Equal(mustValue(t, 17)) {
		t.Fatalf("first constraint = %#v, want age > 17", constraints[0])
	}
	if constraints[1].Operator != FieldConstraintLessThan || !constraints[1].Value.Equal(mustValue(t, 19)) {
		t.Fatalf("second constraint = %#v, want age < 19", constraints[1])
	}
	planConstraints := rangeRule.conditionPlans[0].constraints
	if got, want := len(planConstraints), 2; got != want {
		t.Fatalf("plan constraint count = %d, want %d", got, want)
	}
	if planConstraints[0].operator != FieldConstraintGreaterThan || planConstraints[1].operator != FieldConstraintLessThan {
		t.Fatalf("plan constraint order = (%q, %q), want (> , <)", planConstraints[0].operator, planConstraints[1].operator)
	}

	session, err := NewSession(revision, WithSessionID("predicate-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"age":  18,
		"name": "Ada",
		"tag":  "blue",
	})); err != nil {
		t.Fatalf("AssertTemplate first: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"age":  21,
		"name": "Zoe",
	})); err != nil {
		t.Fatalf("AssertTemplate second: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)

	tests := []struct {
		name     string
		ruleName string
		want     int
	}{
		{name: "age-eq", ruleName: "age-eq", want: 1},
		{name: "age-neq", ruleName: "age-neq", want: 1},
		{name: "age-range", ruleName: "age-range", want: 1},
		{name: "name-eq", ruleName: "name-eq", want: 1},
		{name: "name-order", ruleName: "name-order", want: 1},
		{name: "tag-exists", ruleName: "tag-exists", want: 1},
		{name: "incompatible", ruleName: "incompatible", want: 0},
		{name: "incompatible-neq", ruleName: "incompatible-neq", want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := revision.rules[tc.ruleName]
			matches, err := rule.scanCondition(context.Background(), snapshot, 0)
			if err != nil {
				t.Fatalf("scanCondition: %v", err)
			}
			if got := len(matches); got != tc.want {
				t.Fatalf("match count = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFieldConstraintScanCancellation(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-eq",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "p",
				TemplateKey: personTemplate.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintEqual, Value: 18},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": 18})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rule := revision.rules["age-eq"]
	matches, err := rule.scanCondition(ctx, snapshot, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scanCondition error = %v, want context.Canceled", err)
	}
	if matches != nil {
		t.Fatalf("matches = %#v, want nil after cancellation", matches)
	}
}
