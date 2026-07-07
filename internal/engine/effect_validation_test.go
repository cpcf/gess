package engine

import (
	"context"
	"strings"
	"testing"
)

// An assert/modify effect that names a field the template does not declare, or
// supplies a statically-typed value whose kind does not match the field, and a
// modify/retract effect whose target is not a bound fact, and a bind without
// exactly one value, must all be rejected at compile time — never left to abort
// a firing after earlier effects have already mutated working memory.
func TestCompileRejectsInvalidEffectFields(t *testing.T) {
	template := TemplateSpec{
		Name: "ticket",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "count", Kind: ValueInt},
			{Name: "extra", Kind: ValueAny},
		},
	}

	for _, tc := range []struct {
		name   string
		effect func(key TemplateKey) *ActionEffectSpec
		want   string
	}{
		{
			name: "assert unknown field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "ticket", Fields: []string{"nope"}, Values: []ExpressionSpec{ConstExpr{Value: "x"}}}
			},
			want: "unknown field",
		},
		{
			name: "assert value type mismatch",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "ticket", Fields: []string{"id"}, Values: []ExpressionSpec{ConstExpr{Value: int64(1)}}}
			},
			want: "value type does not match template field",
		},
		{
			name: "assert-logical unknown field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssertLogical, TemplateKey: key, FactName: "ticket", Fields: []string{"nope"}, Values: []ExpressionSpec{ConstExpr{Value: "x"}}}
			},
			want: "unknown field",
		},
		{
			name: "modify unknown set field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "t", Fields: []string{"nope"}, Values: []ExpressionSpec{ConstExpr{Value: "x"}}}
			},
			want: "unknown field",
		},
		{
			name: "modify unknown unset field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "t", Unset: []string{"nope"}}
			},
			want: "unknown field",
		},
		{
			name: "modify value type mismatch",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "t", Fields: []string{"id"}, Values: []ExpressionSpec{ConstExpr{Value: int64(1)}}}
			},
			want: "value type does not match template field",
		},
		{
			name: "modify target not a bound fact",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "ghost", Fields: []string{"count"}, Values: []ExpressionSpec{ConstExpr{Value: int64(1)}}}
			},
			want: `modify target "ghost" is not a bound fact`,
		},
		{
			name: "retract target not a bound fact",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectRetract, Target: "ghost"}
			},
			want: `retract target "ghost" is not a bound fact`,
		},
		{
			name: "bind with two values",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectBind, Target: "x", Values: []ExpressionSpec{ConstExpr{Value: int64(1)}, ConstExpr{Value: int64(2)}}}
			},
			want: "bind requires exactly one value",
		},
		{
			name: "modify target with stray marker prefix",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "?t", Fields: []string{"count"}, Values: []ExpressionSpec{ConstExpr{Value: int64(1)}}}
			},
			want: `modify target "?t" is not a bound fact`,
		},
		{
			name: "assert field and value count mismatch",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "ticket", Fields: []string{"id", "extra"}, Values: []ExpressionSpec{ConstExpr{Value: "T-1"}}}
			},
			want: "effect has 2 fields but 1 values",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			key := mustAddTemplate(t, workspace, template).Key()
			mustAddAction(t, workspace, ActionSpec{Name: "act", Effect: tc.effect(key)})
			mustAddRule(t, workspace, RuleSpec{
				Name:       "r",
				Conditions: []RuleConditionSpec{{Binding: "t", Target: TemplateKeyFact(key)}},
				Actions:    []RuleActionSpec{{Name: "act"}},
			})
			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatalf("Compile succeeded; want a compile error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// Effects whose fields and targets are valid must still compile — including a
// value routed into a slot declared with kind ValueAny, which the type check
// must skip rather than reject.
func TestCompileAcceptsValidEffects(t *testing.T) {
	template := TemplateSpec{
		Name: "ticket",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "count", Kind: ValueInt},
			{Name: "extra", Kind: ValueAny},
		},
	}

	for _, tc := range []struct {
		name   string
		effect func(key TemplateKey) *ActionEffectSpec
	}{
		{
			name: "assert matching string field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "ticket", Fields: []string{"id"}, Values: []ExpressionSpec{ConstExpr{Value: "T-1"}}}
			},
		},
		{
			name: "assert matching int field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "ticket", Fields: []string{"id", "count"}, Values: []ExpressionSpec{ConstExpr{Value: "T-1"}, ConstExpr{Value: int64(3)}}}
			},
		},
		{
			name: "assert string into any field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "ticket", Fields: []string{"id", "extra"}, Values: []ExpressionSpec{ConstExpr{Value: "T-1"}, ConstExpr{Value: "anything"}}}
			},
		},
		{
			name: "modify matching field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "t", Fields: []string{"count"}, Values: []ExpressionSpec{ConstExpr{Value: int64(9)}}}
			},
		},
		{
			name: "modify unset declared field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "t", Unset: []string{"count"}}
			},
		},
		{
			name: "retract bound fact",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectRetract, Target: "t"}
			},
		},
		{
			name: "bind one value",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectBind, Target: "x", Values: []ExpressionSpec{ConstExpr{Value: int64(1)}}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			key := mustAddTemplate(t, workspace, template).Key()
			mustAddAction(t, workspace, ActionSpec{Name: "act", Effect: tc.effect(key)})
			mustAddRule(t, workspace, RuleSpec{
				Name:       "r",
				Conditions: []RuleConditionSpec{{Binding: "t", Target: TemplateKeyFact(key)}},
				Actions:    []RuleActionSpec{{Name: "act"}},
			})
			if _, err := workspace.Compile(context.Background()); err != nil {
				t.Fatalf("Compile with a valid effect failed: %v", err)
			}
		})
	}
}

// The type check mirrors the runtime's numeric tolerance: a value whose static
// kind is a function's declared numeric return must still compile into a field
// of the other numeric kind, because the function may actually return the
// matching kind at fire time. Rejecting it would be a false compile error on a
// ruleset that runs correctly.
func TestCompileAcceptsNumericFunctionReturnIntoNumericField(t *testing.T) {
	workspace := NewWorkspace()
	key := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "counter",
		Fields: []FieldSpec{
			{Name: "n", Kind: ValueInt, Required: true},
		},
	}).Key()
	// Declared FLOAT, but the implementation returns an INT.
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "as-count",
		Args:   []ValueKind{ValueInt},
		Return: ValueFloat,
		Func1: func(_ context.Context, v Value) (Value, error) {
			return newIntValue(v.intValue), nil
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "bump",
		Effect: &ActionEffectSpec{
			Kind:   ActionEffectModify,
			Target: "c",
			Fields: []string{"n"},
			Values: []ExpressionSpec{Call("as-count", BindingFieldExpr{Binding: "c", Field: "n"})},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "r",
		Conditions: []RuleConditionSpec{{Binding: "c", Target: TemplateKeyFact(key)}},
		Actions:    []RuleActionSpec{{Name: "bump"}},
	})
	if _, err := workspace.Compile(context.Background()); err != nil {
		t.Fatalf("Compile rejected a numeric-return function value into a numeric field: %v", err)
	}
}

// An assert that omits a required no-default field, a constant whose kind
// crosses a strictly-typed numeric field, and a constant outside a field's
// allowed set each abort at fire time on the runtime storage path, so they must
// be rejected at compile time rather than left to break a non-atomic firing.
func TestCompileRejectsEffectRuntimeStorageFailures(t *testing.T) {
	template := TemplateSpec{
		Name: "record",
		Fields: []FieldSpec{
			{Name: "key", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueFloat},
			{Name: "state", Kind: ValueString, AllowedValues: []any{"open", "closed"}},
		},
	}
	for _, tc := range []struct {
		name   string
		effect func(key TemplateKey) *ActionEffectSpec
		want   string
	}{
		{
			name: "assert omits required no-default field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "record", Fields: []string{"score"}, Values: []ExpressionSpec{ConstExpr{Value: float64(1)}}}
			},
			want: "required field is missing",
		},
		{
			name: "assert-logical omits required no-default field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssertLogical, TemplateKey: key, FactName: "record", Fields: []string{"score"}, Values: []ExpressionSpec{ConstExpr{Value: float64(1)}}}
			},
			want: "required field is missing",
		},
		{
			name: "assert int constant into float field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "record", Fields: []string{"key", "score"}, Values: []ExpressionSpec{ConstExpr{Value: "k"}, ConstExpr{Value: int64(5)}}}
			},
			want: "value type does not match template field",
		},
		{
			name: "modify int constant into float field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "r", Fields: []string{"score"}, Values: []ExpressionSpec{ConstExpr{Value: int64(5)}}}
			},
			want: "value type does not match template field",
		},
		{
			name: "assert constant outside allowed set",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "record", Fields: []string{"key", "state"}, Values: []ExpressionSpec{ConstExpr{Value: "k"}, ConstExpr{Value: "banana"}}}
			},
			want: "value not in allowed set",
		},
		{
			name: "modify constant outside allowed set",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "r", Fields: []string{"state"}, Values: []ExpressionSpec{ConstExpr{Value: "banana"}}}
			},
			want: "value not in allowed set",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			key := mustAddTemplate(t, workspace, template).Key()
			mustAddAction(t, workspace, ActionSpec{Name: "act", Effect: tc.effect(key)})
			mustAddRule(t, workspace, RuleSpec{
				Name:       "r",
				Conditions: []RuleConditionSpec{{Binding: "r", Target: TemplateKeyFact(key)}},
				Actions:    []RuleActionSpec{{Name: "act"}},
			})
			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatalf("Compile succeeded; want a compile error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// A modify may omit a required field (it patches a subset of slots), and an
// assert or modify constant that is a member of the field's allowed set, or a
// matching-kind constant into a strictly-typed field, must still compile.
func TestCompileAcceptsRequiredAndAllowedEffects(t *testing.T) {
	template := TemplateSpec{
		Name: "record",
		Fields: []FieldSpec{
			{Name: "key", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueFloat},
			{Name: "state", Kind: ValueString, AllowedValues: []any{"open", "closed"}},
		},
	}
	for _, tc := range []struct {
		name   string
		effect func(key TemplateKey) *ActionEffectSpec
	}{
		{
			name: "modify omits required field",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectModify, Target: "r", Fields: []string{"score"}, Values: []ExpressionSpec{ConstExpr{Value: float64(2)}}}
			},
		},
		{
			name: "assert supplies required field and matching float",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "record", Fields: []string{"key", "score"}, Values: []ExpressionSpec{ConstExpr{Value: "k"}, ConstExpr{Value: float64(5)}}}
			},
		},
		{
			name: "assert allowed-set member",
			effect: func(key TemplateKey) *ActionEffectSpec {
				return &ActionEffectSpec{Kind: ActionEffectAssert, TemplateKey: key, FactName: "record", Fields: []string{"key", "state"}, Values: []ExpressionSpec{ConstExpr{Value: "k"}, ConstExpr{Value: "open"}}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			key := mustAddTemplate(t, workspace, template).Key()
			mustAddAction(t, workspace, ActionSpec{Name: "act", Effect: tc.effect(key)})
			mustAddRule(t, workspace, RuleSpec{
				Name:       "r",
				Conditions: []RuleConditionSpec{{Binding: "r", Target: TemplateKeyFact(key)}},
				Actions:    []RuleActionSpec{{Name: "act"}},
			})
			if _, err := workspace.Compile(context.Background()); err != nil {
				t.Fatalf("Compile with a valid effect failed: %v", err)
			}
		})
	}
}
