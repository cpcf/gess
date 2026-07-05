package engine

import (
	"bytes"
	"context"
	"testing"
)

// TestActionEffectModifyWithCallValue drives the native effect action path
// directly (no .gess loader): a modify whose set value is a function call
// (+ ?a ?b), plus a bind consumed by a later modify and an emit, all evaluated
// against frozen snapshots.
func TestActionEffectModifyWithCallValue(t *testing.T) {
	ctx := context.Background()
	ws := NewWorkspace()
	if err := ws.AddTemplate(TemplateSpec{
		Name: "order",
		Key:  "order",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "a", Kind: ValueInt, Required: true},
			{Name: "b", Kind: ValueInt, Required: true},
			{Name: "total", Kind: ValueInt, Required: true},
			{Name: "label", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}

	// bind ?sum = (+ ?order:a ?order:b)
	if err := ws.AddAction(ActionSpec{
		Name: "bind-sum",
		Effect: &ActionEffectSpec{
			Kind:   ActionEffectBind,
			Target: "sum",
			Values: []ExpressionSpec{
				Call("+", BindingFieldExpr{Binding: "order", Field: "a"}, BindingFieldExpr{Binding: "order", Field: "b"}),
			},
		},
	}); err != nil {
		t.Fatalf("AddAction bind: %v", err)
	}
	// modify ?order set total = ?sum (an RHS bind), label = (str-cat "sum-" ?order:id)
	if err := ws.AddAction(ActionSpec{
		Name: "apply-sum",
		Effect: &ActionEffectSpec{
			Kind:   ActionEffectModify,
			Target: "order",
			Fields: []string{"total", "label"},
			Values: []ExpressionSpec{
				RHSBindExpr{Name: "sum"},
				Call("str-cat", ConstExpr{Value: "sum-"}, BindingFieldExpr{Binding: "order", Field: "id"}),
			},
		},
	}); err != nil {
		t.Fatalf("AddAction modify: %v", err)
	}
	// emit "order " ?order:id " total=" ?sum  (reads id AFTER the modify)
	if err := ws.AddAction(ActionSpec{
		Name: "announce",
		Effect: &ActionEffectSpec{
			Kind: ActionEffectEmit,
			Values: []ExpressionSpec{
				ConstExpr{Value: "order "},
				BindingFieldExpr{Binding: "order", Field: "id"},
				ConstExpr{Value: " total="},
				RHSBindExpr{Name: "sum"},
			},
		},
	}); err != nil {
		t.Fatalf("AddAction emit: %v", err)
	}

	if err := ws.AddRule(RuleSpec{
		Name: "total-order",
		ConditionTree: Match(RuleConditionSpec{
			Binding: "order",
			Target:  TemplateFactIn("", "order"),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "total", Operator: FieldConstraintEqual, Value: int64(0)},
			},
		}),
		Actions: []RuleActionSpec{
			{Name: "bind-sum"},
			{Name: "apply-sum"},
			{Name: "announce"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := ws.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	var out bytes.Buffer
	fields, _ := NewFieldsFromPairs("id", "O-1", "a", int64(100), "b", int64(8), "total", int64(0), "label", "")
	session, err := NewSession(revision,
		WithInitialFacts(SessionInitialFact{TemplateKey: "order", Fields: fields}),
		WithOutputWriter(&out),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	order := snap.FactsByName("order")[0]
	total, _ := order.Field("total")
	if v, _ := total.AsInt64(); v != 108 {
		t.Fatalf("total = %v, want 108", total)
	}
	label, _ := order.Field("label")
	if v, _ := label.AsString(); v != "sum-O-1" {
		t.Fatalf("label = %v, want sum-O-1", label)
	}
	if got := out.String(); got != "order O-1 total=108" {
		t.Fatalf("emit = %q, want %q", got, "order O-1 total=108")
	}
}
