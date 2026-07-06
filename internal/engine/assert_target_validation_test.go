package engine

import (
	"context"
	"strings"
	"testing"
)

// An assert / assert-logical effect targeting an undeclared template must be
// rejected at compile time, not left to fail mid-firing after earlier actions
// in the same activation have committed.
func TestCompileRejectsUndeclaredAssertTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind ActionEffectKind
		want string
	}{
		{"assert", ActionEffectAssert, "assert target"},
		{"assert-logical", ActionEffectAssertLogical, "assert-logical target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			aKey := mustAddTemplate(t, workspace, TemplateSpec{
				Name:   "a",
				Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
			}).Key()
			mustAddAction(t, workspace, ActionSpec{
				Name: "bad",
				Effect: &ActionEffectSpec{
					Kind:     tc.kind,
					FactName: "undeclared-thing",
					Fields:   []string{"x"},
					Values:   []ExpressionSpec{ConstExpr{Value: int64(1)}},
				},
			})
			mustAddRule(t, workspace, RuleSpec{
				Name:       "r",
				Conditions: []RuleConditionSpec{{Binding: "a", Target: TemplateKeyFact(aKey)}},
				Actions:    []RuleActionSpec{{Name: "bad"}},
			})

			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatalf("Compile succeeded; want a compile-time error for the undeclared %s target", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "not a declared template") {
				t.Fatalf("error = %v, want it to name the undeclared %s target", err, tc.name)
			}
		})
	}
}

// A declared assert target still compiles.
func TestCompileAcceptsDeclaredAssertTarget(t *testing.T) {
	workspace := NewWorkspace()
	aKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "a",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	ticketKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "ticket",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	mustAddAction(t, workspace, ActionSpec{
		Name: "open",
		Effect: &ActionEffectSpec{
			Kind:        ActionEffectAssert,
			TemplateKey: ticketKey,
			Fields:      []string{"id"},
			Values:      []ExpressionSpec{ConstExpr{Value: "T-1"}},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "r",
		Conditions: []RuleConditionSpec{{Binding: "a", Target: TemplateKeyFact(aKey)}},
		Actions:    []RuleActionSpec{{Name: "open"}},
	})
	if _, err := workspace.Compile(context.Background()); err != nil {
		t.Fatalf("Compile with a declared assert target failed: %v", err)
	}
}
