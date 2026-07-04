package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess/examples/internal/exampleutil"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

const (
	customerTemplate  = rules.TemplateKey("customer")
	inventoryTemplate = rules.TemplateKey("inventory")
	orderTemplate     = rules.TemplateKey("order")
	routeTemplate     = rules.TemplateKey("fulfillment-route")
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
	session, err := sess.New(ruleset, sess.WithEventListener(
		sess.NewTraceListener(out),
		sess.ForEventTypes(sess.EventRuleFired),
	))
	if err != nil {
		return err
	}
	defer session.Close()

	for _, fact := range []struct {
		template rules.TemplateKey
		fields   rules.Fields
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

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := rules.NewWorkspace()
	for _, spec := range []rules.TemplateSpec{
		{Name: string(customerTemplate), Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "segment", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(inventoryTemplate), Fields: []rules.FieldSpec{
			{Name: "sku", Kind: rules.ValueString, Required: true},
			{Name: "warehouse", Kind: rules.ValueString, Required: true},
			{Name: "available", Kind: rules.ValueBool, Required: true},
		}},
		{Name: string(orderTemplate), Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "customer", Kind: rules.ValueString, Required: true},
			{Name: "sku", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(routeTemplate), DuplicatePolicy: rules.DuplicateUniqueKey, DuplicateKeyNames: []string{"order"}, Fields: []rules.FieldSpec{
			{Name: "order", Kind: rules.ValueString, Required: true},
			{Name: "lane", Kind: rules.ValueString, Required: true},
			{Name: "warehouse", Kind: rules.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "route-vip-order",
		Fn: func(ctx rules.ActionContext) error {
			order, _ := ctx.BindingScalarValue("order", "id")
			warehouse, _ := ctx.BindingScalarValue("inventory", "warehouse")
			_, err := ctx.AssertTemplate(routeTemplate, rules.Fields{
				"order":     order,
				"lane":      mustValue("expedite"),
				"warehouse": warehouse,
			})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "route-standard-order",
		Fn: func(ctx rules.ActionContext) error {
			order, _ := ctx.BindingScalarValue("order", "id")
			warehouse, _ := ctx.BindingScalarValue("inventory", "warehouse")
			_, err := ctx.AssertTemplate(routeTemplate, rules.Fields{
				"order":     order,
				"lane":      mustValue("standard"),
				"warehouse": warehouse,
			})
			return err
		},
	}); err != nil {
		return nil, err
	}
	for _, rule := range []rules.RuleSpec{
		routeRule("route-vip", "vip", "route-vip-order"),
		routeRule("route-standard", "standard", "route-standard-order"),
	} {
		if err := workspace.AddRule(rule); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name:          "routes",
		ConditionTree: rules.Match{Binding: "route", Target: rules.TemplateKeyFact(routeTemplate)},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("order", rules.BindingFieldExpr{Binding: "route", Field: "order"}),
			rules.ReturnValue("lane", rules.BindingFieldExpr{Binding: "route", Field: "lane"}),
			rules.ReturnValue("warehouse", rules.BindingFieldExpr{Binding: "route", Field: "warehouse"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func routeRule(name, segment, action string) rules.RuleSpec {
	return rules.RuleSpec{
		Name: name,
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "order", Target: rules.TemplateKeyFact(orderTemplate)},
			rules.Match{
				Binding: "customer",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "segment", Operator: rules.FieldConstraintEqual, Value: segment},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "id", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "order", Field: "customer"}},
				},
				Target: rules.TemplateKeyFact(customerTemplate),
			},
			rules.Match{
				Binding: "inventory",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "available", Operator: rules.FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "sku", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "order", Field: "sku"}},
				},
				Target: rules.TemplateKeyFact(inventoryTemplate),
			},
		}},
		Actions: []rules.RuleActionSpec{{Name: action}},
	}
}

func mustValue(raw any) rules.Value {
	value, err := rules.NewValue(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func stringValue(row sess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}
