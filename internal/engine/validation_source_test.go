package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGessSemanticValidationErrorsCarryAuthoredSource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		source     string
		wantLine   int
		wantColumn int
		wantReason string
	}{
		{
			name: "expression",
			source: `(deftemplate item
  (slot score (type INT)))
(defrule bad-expression
  ?item <- (item)
  (test (> ?item:missing 0))
  =>
  (halt))`,
			wantLine:   5,
			wantColumn: 3,
			wantReason: "unknown field",
		},
		{
			name: "field constraint",
			source: `(deftemplate item
  (slot score (type INT)))
(defrule bad-constraint
  ?item <- (item (score "high"))
  =>
  (halt))`,
			wantLine:   4,
			wantColumn: 12,
			wantReason: "constraint value kind string can never equal field \"score\" of kind int",
		},
		{
			name: "rule condition",
			source: `(deftemplate item
  (slot score (type INT)))
(defrule duplicate-binding
  ?item <- (item (score 1))
  ?item <- (item (score 2))
  =>
  (halt))`,
			wantLine:   5,
			wantColumn: 12,
			wantReason: "duplicate binding",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileGess(context.Background(), "semantic-errors.gess", []byte(tc.source), DSLRegistry{})
			if err == nil {
				t.Fatal("CompileGess succeeded, want semantic validation error")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("CompileGess error = %T, want *ValidationError: %v", err, err)
			}
			if got := validation.Source; got.Name != "semantic-errors.gess" || got.StartLine != tc.wantLine || got.StartColumn != tc.wantColumn {
				t.Fatalf("validation source = %+v, want semantic-errors.gess:%d:%d", got, tc.wantLine, tc.wantColumn)
			}
			if validation.Reason != tc.wantReason {
				t.Fatalf("validation reason = %q, want %q", validation.Reason, tc.wantReason)
			}
			if location := fmt.Sprintf("semantic-errors.gess:%d:%d", tc.wantLine, tc.wantColumn); !strings.Contains(err.Error(), location) {
				t.Fatalf("validation error = %q, want source location containing %q", err.Error(), location)
			}
		})
	}
}

func TestDefinitionValidationErrorsUseAvailableSourceFallback(t *testing.T) {
	span := SourceSpan{Name: "definitions.gess", StartLine: 17, StartColumn: 5, EndLine: 17, EndColumn: 20}
	for _, tc := range []struct {
		name    string
		compile func() error
	}{
		{
			name: "template",
			compile: func() error {
				_, err := compileTemplateSpec(TemplateSpec{Name: "bad", Source: span, DuplicatePolicy: DuplicatePolicy(99)})
				return err
			},
		},
		{
			name: "query",
			compile: func() error {
				_, err := compileQuerySpec(QuerySpec{Name: "bad", Source: span}, templateResolver{}, nil, nil)
				return err
			},
		},
		{
			name: "query return",
			compile: func() error {
				_, err := compileQueryReturns("bad", []QueryReturnSpec{{Source: span}}, nil, nil, nil, nil, nil, nil)
				return err
			},
		},
		{
			name: "expression function",
			compile: func() error {
				_, err := compileExpressionFunctionSpec(ExpressionFunctionSpec{Source: span}, 0, nil)
				return err
			},
		},
		{
			name: "join constraint",
			compile: func() error {
				_, _, err := compileJoinConstraintSpecWithSource(JoinConstraintSpec{
					Field: "value", Operator: FieldConstraintOpUnknown,
					Ref: FieldRef{Binding: "left", Field: "value"},
				}, span, "bad", 1, 0, nil, nil, nil, nil)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertValidationErrorSource(t, tc.compile(), span)
		})
	}
}

func TestRuleNestedValidationErrorsUseConditionAndActionSources(t *testing.T) {
	conditionSpan := SourceSpan{Name: "nested.gess", StartLine: 9, StartColumn: 3}
	actionSpan := SourceSpan{Name: "nested.gess", StartLine: 12, StartColumn: 3}

	t.Run("list pattern", func(t *testing.T) {
		workspace := NewWorkspace()
		item := mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "id", Kind: ValueString}}})
		mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
		mustAddRule(t, workspace, RuleSpec{
			Name: "bad-list",
			Conditions: []RuleConditionSpec{{
				Binding: "item", Target: TemplateKeyFact(item.Key()), Source: conditionSpan,
				ListPatterns: []ListPatternSpec{{}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		_, err := workspace.Compile(context.Background())
		assertValidationErrorSource(t, err, conditionSpan)
	})

	t.Run("aggregate", func(t *testing.T) {
		workspace := NewWorkspace()
		item := mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "id", Kind: ValueString}}})
		mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
		mustAddRule(t, workspace, RuleSpec{
			Name: "bad-aggregate",
			ConditionTree: AccumulateCondition{
				Input:  Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
				Source: conditionSpan,
			},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		_, err := workspace.Compile(context.Background())
		assertValidationErrorSource(t, err, conditionSpan)
	})

	t.Run("action", func(t *testing.T) {
		workspace := NewWorkspace()
		item := mustAddTemplate(t, workspace, TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "id", Kind: ValueString}}})
		mustAddAction(t, workspace, ActionSpec{Name: "bad-modify", Effect: &ActionEffectSpec{Kind: ActionEffectModify, Target: "missing"}})
		mustAddRule(t, workspace, RuleSpec{
			Name:       "bad-action",
			Conditions: []RuleConditionSpec{{Binding: "item", Target: TemplateKeyFact(item.Key())}},
			Actions:    []RuleActionSpec{{Name: "bad-modify", Source: actionSpan}},
		})
		_, err := workspace.Compile(context.Background())
		assertValidationErrorSource(t, err, actionSpan)
	})
}

func assertValidationErrorSource(t *testing.T, err error, want SourceSpan) {
	t.Helper()
	if err == nil {
		t.Fatal("compile succeeded, want validation error")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T, want *ValidationError: %v", err, err)
	}
	if validation.Source != want {
		t.Fatalf("validation source = %+v, want %+v", validation.Source, want)
	}
}
