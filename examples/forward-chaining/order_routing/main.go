package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess"
	"github.com/cpcf/gess/examples/internal/exampleutil"
)

const (
	customerTemplate  = gess.TemplateKey("customer")
	inventoryTemplate = gess.TemplateKey("inventory")
	orderTemplate     = gess.TemplateKey("order")
	routeTemplate     = gess.TemplateKey("fulfillment-route")
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	ctx := context.Background()
	ruleset, err := buildRuleset(ctx)
	if err != nil {
		return err
	}
	session, err := gess.NewSession(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	for _, fact := range []struct {
		template gess.TemplateKey
		fields   gess.Fields
	}{
		{customerTemplate, exampleutil.Fields("id", "C-100", "segment", "vip")},
		{customerTemplate, exampleutil.Fields("id", "C-200", "segment", "standard")},
		{inventoryTemplate, exampleutil.Fields("sku", "SKU-1", "warehouse", "east", "available", true)},
		{inventoryTemplate, exampleutil.Fields("sku", "SKU-2", "warehouse", "west", "available", false)},
		{orderTemplate, exampleutil.Fields("id", "O-100", "customer", "C-100", "sku", "SKU-1")},
		{orderTemplate, exampleutil.Fields("id", "O-200", "customer", "C-200", "sku", "SKU-1")},
		{orderTemplate, exampleutil.Fields("id", "O-300", "customer", "C-100", "sku", "SKU-2")},
	} {
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}
	result, err := session.Run(ctx)
	if err != nil {
		return err
	}

	rows, err := session.QueryAll(ctx, "routes", nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "fired: %d\n", result.Fired)
	for _, row := range rows {
		order := stringValue(row, "order")
		lane := stringValue(row, "lane")
		warehouse := stringValue(row, "warehouse")
		fmt.Fprintf(out, "%s -> %s via %s\n", order, lane, warehouse)
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	for _, spec := range []gess.TemplateSpec{
		{Name: string(customerTemplate), Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "segment", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(inventoryTemplate), Fields: []gess.FieldSpec{
			{Name: "sku", Kind: gess.ValueString, Required: true},
			{Name: "warehouse", Kind: gess.ValueString, Required: true},
			{Name: "available", Kind: gess.ValueBool, Required: true},
		}},
		{Name: string(orderTemplate), Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "customer", Kind: gess.ValueString, Required: true},
			{Name: "sku", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(routeTemplate), DuplicatePolicy: gess.DuplicateUniqueKey, DuplicateKeyNames: []string{"order"}, Fields: []gess.FieldSpec{
			{Name: "order", Kind: gess.ValueString, Required: true},
			{Name: "lane", Kind: gess.ValueString, Required: true},
			{Name: "warehouse", Kind: gess.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "route-vip-order",
		Fn: func(ctx gess.ActionContext) error {
			order, _ := ctx.BindingScalarValue("order", "id")
			warehouse, _ := ctx.BindingScalarValue("inventory", "warehouse")
			_, err := ctx.AssertTemplate(routeTemplate, gess.Fields{
				"order":     order,
				"lane":      mustValue("expedite"),
				"warehouse": warehouse,
			})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "route-standard-order",
		Fn: func(ctx gess.ActionContext) error {
			order, _ := ctx.BindingScalarValue("order", "id")
			warehouse, _ := ctx.BindingScalarValue("inventory", "warehouse")
			_, err := ctx.AssertTemplate(routeTemplate, gess.Fields{
				"order":     order,
				"lane":      mustValue("standard"),
				"warehouse": warehouse,
			})
			return err
		},
	}); err != nil {
		return nil, err
	}
	for _, rule := range []gess.RuleSpec{
		routeRule("route-vip", "vip", "route-vip-order"),
		routeRule("route-standard", "standard", "route-standard-order"),
	} {
		if err := workspace.AddRule(rule); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name:          "routes",
		ConditionTree: gess.Match{Binding: "route", Target: gess.TemplateKeyFact(routeTemplate)},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("order", gess.BindingFieldExpr{Binding: "route", Field: "order"}),
			gess.ReturnValue("lane", gess.BindingFieldExpr{Binding: "route", Field: "lane"}),
			gess.ReturnValue("warehouse", gess.BindingFieldExpr{Binding: "route", Field: "warehouse"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func routeRule(name, segment, action string) gess.RuleSpec {
	return gess.RuleSpec{
		Name: name,
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "order", Target: gess.TemplateKeyFact(orderTemplate)},
			gess.Match{
				Binding: "customer",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "segment", Operator: gess.FieldConstraintEqual, Value: segment},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "id", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "order", Field: "customer"}},
				},
				Target: gess.TemplateKeyFact(customerTemplate),
			},
			gess.Match{
				Binding: "inventory",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "available", Operator: gess.FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "sku", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "order", Field: "sku"}},
				},
				Target: gess.TemplateKeyFact(inventoryTemplate),
			},
		}},
		Actions: []gess.RuleActionSpec{{Name: action}},
	}
}

func mustValue(raw any) gess.Value {
	value, err := gess.NewValue(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func stringValue(row gess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}
