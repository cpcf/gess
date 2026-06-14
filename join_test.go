package gess

import (
	"context"
	"errors"
	"testing"
)

func TestJoinConstraintCompileValidation(t *testing.T) {
	template := TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueAny},
		},
	}

	t.Run("missing binding reference", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, template)
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject missing join binding references")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 1 {
			t.Fatalf("condition index = (%v, %d), want (true, 1)", validation.HasConditionIndex, validation.ConditionIndex)
		}
		if !validation.HasJoinIndex || validation.JoinIndex != 0 {
			t.Fatalf("join index = (%v, %d), want (true, 0)", validation.HasJoinIndex, validation.JoinIndex)
		}
		if validation.Reason != "join binding reference is required" {
			t.Fatalf("reason = %q, want join binding reference is required", validation.Reason)
		}
	})

	t.Run("future binding reference", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, template)
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "later", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject future join binding references")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 1 {
			t.Fatalf("condition index = (%v, %d), want (true, 1)", validation.HasConditionIndex, validation.ConditionIndex)
		}
		if !validation.HasJoinIndex || validation.JoinIndex != 0 {
			t.Fatalf("join index = (%v, %d), want (true, 0)", validation.HasJoinIndex, validation.JoinIndex)
		}
		if validation.Reason != "join binding reference must refer to an earlier condition" {
			t.Fatalf("reason = %q, want join binding reference must refer to an earlier condition", validation.Reason)
		}
	})

	t.Run("closed current field", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, template)
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "height", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject closed-template join fields")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.TemplateName != "person" {
			t.Fatalf("template name = %q, want person", validation.TemplateName)
		}
		if validation.FieldName != "height" {
			t.Fatalf("field name = %q, want height", validation.FieldName)
		}
		if !validation.HasJoinIndex || validation.JoinIndex != 0 {
			t.Fatalf("join index = (%v, %d), want (true, 0)", validation.HasJoinIndex, validation.JoinIndex)
		}
		if validation.Reason != "unknown field" {
			t.Fatalf("reason = %q, want unknown field", validation.Reason)
		}
	})

	t.Run("closed referenced field", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, template)
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "height"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject closed-template join references")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if validation.TemplateName != "person" {
			t.Fatalf("template name = %q, want person", validation.TemplateName)
		}
		if validation.FieldName != "height" {
			t.Fatalf("field name = %q, want height", validation.FieldName)
		}
		if !validation.HasJoinIndex || validation.JoinIndex != 0 {
			t.Fatalf("join index = (%v, %d), want (true, 0)", validation.HasJoinIndex, validation.JoinIndex)
		}
		if validation.Reason != "unknown field" {
			t.Fatalf("reason = %q, want unknown field", validation.Reason)
		}
	})

	t.Run("invalid operator", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, template)
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "broken",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintNotEqual, Ref: FieldRef{Binding: "left", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		_, err := workspace.Compile(context.Background())
		if err == nil {
			t.Fatal("Compile should reject unsupported join operators")
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected ValidationError, got %T: %v", err, err)
		}
		if !validation.HasConditionIndex || validation.ConditionIndex != 1 {
			t.Fatalf("condition index = (%v, %d), want (true, 1)", validation.HasConditionIndex, validation.ConditionIndex)
		}
		if !validation.HasJoinIndex || validation.JoinIndex != 0 {
			t.Fatalf("join index = (%v, %d), want (true, 0)", validation.HasJoinIndex, validation.JoinIndex)
		}
		if validation.Reason != "invalid join operator" {
			t.Fatalf("reason = %q, want invalid join operator", validation.Reason)
		}
	})
}

func TestJoinConstraintSlotResolutionAndFallback(t *testing.T) {
	t.Run("closed template uses slots", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Closed: true,
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueAny},
				{Name: "label", Kind: ValueString},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "join-eq",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		planJoin := revision.rules["join-eq"].conditionPlans[1].joins[0]
		if planJoin.fieldSlot < 0 {
			t.Fatalf("field slot = %d, want non-negative", planJoin.fieldSlot)
		}
		if planJoin.refFieldSlot < 0 {
			t.Fatalf("ref field slot = %d, want non-negative", planJoin.refFieldSlot)
		}

		session, err := NewSession(revision, WithSessionID("join-slot-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		inserted, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
			"age":   20,
			"label": "alpha",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}

		snapshot := session.indexedSnapshotLocked()
		sets, err := revision.rules["join-eq"].matchBindingSets(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("matchBindingSets: %v", err)
		}
		if got, want := len(sets), 1; got != want {
			t.Fatalf("binding set count = %d, want %d", got, want)
		}
		if got := sets[0].matches[0].fact.ID(); got != inserted.Fact.ID() {
			t.Fatalf("left match fact = %q, want %q", got, inserted.Fact.ID())
		}
		if got := sets[0].matches[1].fact.ID(); got != inserted.Fact.ID() {
			t.Fatalf("right match fact = %q, want %q", got, inserted.Fact.ID())
		}
	})

	t.Run("closed current falls back to dynamic ref", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Closed: true,
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueAny},
				{Name: "label", Kind: ValueString},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "join-dynamic-ref",
			Conditions: []RuleConditionSpec{
				{Binding: "baseline", Name: "baseline"},
				{
					Binding:     "candidate",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "baseline", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		planJoin := revision.rules["join-dynamic-ref"].conditionPlans[1].joins[0]
		if planJoin.fieldSlot < 0 {
			t.Fatalf("field slot = %d, want non-negative", planJoin.fieldSlot)
		}
		if planJoin.refFieldSlot != -1 {
			t.Fatalf("ref field slot = %d, want -1 for dynamic reference", planJoin.refFieldSlot)
		}

		session, err := NewSession(revision, WithSessionID("join-dynamic-ref-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if _, err := session.Assert(context.Background(), "baseline", mustFields(t, map[string]any{"age": 20})); err != nil {
			t.Fatalf("Assert baseline: %v", err)
		}
		inserted, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
			"age":   20,
			"label": "beta",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}

		snapshot := session.indexedSnapshotLocked()
		sets, err := revision.rules["join-dynamic-ref"].matchBindingSets(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("matchBindingSets: %v", err)
		}
		if got, want := len(sets), 1; got != want {
			t.Fatalf("binding set count = %d, want %d", got, want)
		}
		if got := sets[0].matches[0].fact.Name(); got != "baseline" {
			t.Fatalf("left match name = %q, want baseline", got)
		}
		if got := sets[0].matches[1].fact.ID(); got != inserted.Fact.ID() {
			t.Fatalf("right match fact = %q, want %q", got, inserted.Fact.ID())
		}
	})

	t.Run("dynamic current falls back to closed ref", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Closed: true,
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueAny},
				{Name: "label", Kind: ValueString},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "join-dynamic-current",
			Conditions: []RuleConditionSpec{
				{Binding: "baseline", TemplateKey: personTemplate.Key()},
				{
					Binding: "candidate",
					Name:    "candidate",
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "baseline", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		planJoin := revision.rules["join-dynamic-current"].conditionPlans[1].joins[0]
		if planJoin.fieldSlot != -1 {
			t.Fatalf("field slot = %d, want -1 for dynamic current conditions", planJoin.fieldSlot)
		}
		if planJoin.refFieldSlot < 0 {
			t.Fatalf("ref field slot = %d, want non-negative", planJoin.refFieldSlot)
		}

		session, err := NewSession(revision, WithSessionID("join-dynamic-current-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		insertedRef, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
			"age":   20,
			"label": "gamma",
		}))
		if err != nil {
			t.Fatalf("AssertTemplate baseline: %v", err)
		}
		insertedCurrent, err := session.Assert(context.Background(), "candidate", mustFields(t, map[string]any{"age": 20}))
		if err != nil {
			t.Fatalf("Assert candidate: %v", err)
		}

		snapshot := session.indexedSnapshotLocked()
		sets, err := revision.rules["join-dynamic-current"].matchBindingSets(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("matchBindingSets: %v", err)
		}
		if got, want := len(sets), 1; got != want {
			t.Fatalf("binding set count = %d, want %d", got, want)
		}
		if got := sets[0].matches[0].fact.ID(); got != insertedRef.Fact.ID() {
			t.Fatalf("left match fact = %q, want %q", got, insertedRef.Fact.ID())
		}
		if got := sets[0].matches[1].fact.ID(); got != insertedCurrent.Fact.ID() {
			t.Fatalf("right match fact = %q, want %q", got, insertedCurrent.Fact.ID())
		}
	})
}

func TestJoinConstraintMatching(t *testing.T) {
	t.Run("equality order and self join", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Closed: true,
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueAny},
				{Name: "label", Kind: ValueString},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "join-eq",
			Conditions: []RuleConditionSpec{
				{Binding: "left", TemplateKey: personTemplate.Key()},
				{
					Binding:     "right",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}

		rule := revision.rules["join-eq"]
		condition := rule.conditions[1]
		joins := condition.JoinConstraints()
		if got, want := len(joins), 1; got != want {
			t.Fatalf("join count = %d, want %d", got, want)
		}
		if joins[0].Field != "age" || joins[0].Operator != FieldConstraintEqual || joins[0].Ref.Binding != "left" || joins[0].Ref.Field != "age" {
			t.Fatalf("join inspection = %#v, want age eq left.age", joins[0])
		}

		planJoin := rule.conditionPlans[1].joins[0]
		if got, want := planJoin.bindingSlot, 1; got != want {
			t.Fatalf("join binding slot = %d, want %d", got, want)
		}
		if got, want := planJoin.refBindingSlot, 0; got != want {
			t.Fatalf("join ref binding slot = %d, want %d", got, want)
		}
		if got, want := len(planJoin.path), 2; got != want || planJoin.path[0] != 1 || planJoin.path[1] != 0 {
			t.Fatalf("join path = %#v, want [1 0]", planJoin.path)
		}
		if !planJoin.indexable || planJoin.indexKind != joinIndexEquality {
			t.Fatalf("join index metadata = (%v, %v), want (true, joinIndexEquality)", planJoin.indexable, planJoin.indexKind)
		}

		session, err := NewSession(revision, WithSessionID("join-eq-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		values := []struct {
			age   any
			label string
		}{
			{age: 20, label: "a"},
			{age: 25, label: "b"},
			{age: 20, label: "c"},
		}
		var factIDs []FactID
		for _, tc := range values {
			result, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": tc.age, "label": tc.label}))
			if err != nil {
				t.Fatalf("AssertTemplate(%v): %v", tc, err)
			}
			factIDs = append(factIDs, result.Fact.ID())
		}

		snapshot := mustSnapshot(t, context.Background(), session)
		sets, err := rule.matchBindingSets(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("matchBindingSets: %v", err)
		}
		if got, want := len(sets), 5; got != want {
			t.Fatalf("binding set count = %d, want %d", got, want)
		}

		want := [][]FactID{
			{factIDs[0], factIDs[0]},
			{factIDs[0], factIDs[2]},
			{factIDs[1], factIDs[1]},
			{factIDs[2], factIDs[0]},
			{factIDs[2], factIDs[2]},
		}
		for i, set := range sets {
			if got := len(set.matches); got != 2 {
				t.Fatalf("binding set %d length = %d, want 2", i, got)
			}
			for slot, expectedID := range want[i] {
				if got := set.matches[slot].fact.ID(); got != expectedID {
					t.Fatalf("binding set %d slot %d fact = %q, want %q", i, slot, got, expectedID)
				}
			}
		}
	})

	t.Run("numeric comparison", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Closed: true,
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueAny},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "join-gt",
			Conditions: []RuleConditionSpec{
				{Binding: "threshold", TemplateKey: personTemplate.Key()},
				{
					Binding:     "candidate",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}

		session, err := NewSession(revision, WithSessionID("join-gt-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		values := []any{20.5, 20, 21}
		var factIDs []FactID
		for _, age := range values {
			result, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": age}))
			if err != nil {
				t.Fatalf("AssertTemplate(%v): %v", age, err)
			}
			factIDs = append(factIDs, result.Fact.ID())
		}

		snapshot := mustSnapshot(t, context.Background(), session)
		sets, err := revision.rules["join-gt"].matchBindingSets(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("matchBindingSets: %v", err)
		}
		if got, want := len(sets), 3; got != want {
			t.Fatalf("binding set count = %d, want %d", got, want)
		}

		want := [][]FactID{
			{factIDs[0], factIDs[2]},
			{factIDs[1], factIDs[0]},
			{factIDs[1], factIDs[2]},
		}
		for i, set := range sets {
			for slot, expectedID := range want[i] {
				if got := set.matches[slot].fact.ID(); got != expectedID {
					t.Fatalf("binding set %d slot %d fact = %q, want %q", i, slot, got, expectedID)
				}
			}
		}

		thresholdValue := sets[0].matches[0].fact.Fields()["age"]
		candidateValue := sets[0].matches[1].fact.Fields()["age"]
		if thresholdValue.Kind() != ValueFloat || candidateValue.Kind() != ValueInt {
			t.Fatalf("numeric join values = (%v, %v), want float/int", thresholdValue.Kind(), candidateValue.Kind())
		}
	})

	t.Run("no match no error", func(t *testing.T) {
		workspace := NewWorkspace()
		personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
			Name:   "person",
			Closed: true,
			Fields: []FieldSpec{
				{Name: "age", Kind: ValueAny},
			},
		})
		mustAddAction(t, workspace, ActionSpec{
			Name: "mark",
			Fn:   func(ActionContext) error { return nil },
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: "join-gt",
			Conditions: []RuleConditionSpec{
				{Binding: "threshold", TemplateKey: personTemplate.Key()},
				{
					Binding:     "candidate",
					TemplateKey: personTemplate.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
					},
				},
			},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})

		revision, err := workspace.Compile(context.Background())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		session, err := NewSession(revision, WithSessionID("join-empty-session"))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": 20})); err != nil {
			t.Fatalf("AssertTemplate: %v", err)
		}
		snapshot := mustSnapshot(t, context.Background(), session)

		sets, err := revision.rules["join-gt"].matchBindingSets(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("matchBindingSets: %v", err)
		}
		if len(sets) != 0 {
			t.Fatalf("binding sets = %#v, want none", sets)
		}
	})
}

func TestJoinConstraintCancellation(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueAny},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "join-cancel",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: personTemplate.Key()},
			{
				Binding:     "candidate",
				TemplateKey: personTemplate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("join-cancel-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, age := range []any{20, 21} {
		if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": age})); err != nil {
			t.Fatalf("AssertTemplate(%v): %v", age, err)
		}
	}
	snapshot := mustSnapshot(t, context.Background(), session)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sets, err := revision.rules["join-cancel"].matchBindingSets(ctx, snapshot)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("matchBindingSets error = %v, want context.Canceled", err)
	}
	if sets != nil {
		t.Fatalf("binding sets = %#v, want nil after cancellation", sets)
	}
}
