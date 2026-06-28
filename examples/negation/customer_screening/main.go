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
	customerTemplate = gess.TemplateKey("customer")
	holdTemplate     = gess.TemplateKey("compliance-hold")
	eligibleTemplate = gess.TemplateKey("eligible-customer")
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
		{customerTemplate, exampleutil.Fields("id", "C-100", "status", "active")},
		{customerTemplate, exampleutil.Fields("id", "C-200", "status", "active")},
		{customerTemplate, exampleutil.Fields("id", "C-300", "status", "inactive")},
		{holdTemplate, exampleutil.Fields("customer", "C-200", "reason", "sanctions-review")},
	} {
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}
	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "eligible-customers", nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s: eligible\n", stringValue(row, "customer"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	for _, spec := range []gess.TemplateSpec{
		{Name: string(customerTemplate), Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "status", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(holdTemplate), Fields: []gess.FieldSpec{
			{Name: "customer", Kind: gess.ValueString, Required: true},
			{Name: "reason", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(eligibleTemplate), DuplicatePolicy: gess.DuplicateUniqueKey, DuplicateKeyNames: []string{"customer"}, Fields: []gess.FieldSpec{
			{Name: "customer", Kind: gess.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "assert-eligible",
		Fn: func(ctx gess.ActionContext) error {
			customer, _ := ctx.BindingScalarValue("customer", "id")
			_, err := ctx.AssertTemplate(eligibleTemplate, gess.Fields{"customer": customer})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "active-customer-without-hold",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{
				Binding: "customer",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "status", Operator: gess.FieldConstraintEqual, Value: "active"},
				},
				Target: gess.TemplateKeyFact(customerTemplate),
			},
			gess.Not{Condition: gess.Match{
				Binding: "hold",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "customer", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "customer", Field: "id"}},
				},
				Target: gess.TemplateKeyFact(holdTemplate),
			}},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-eligible"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name:          "eligible-customers",
		ConditionTree: gess.Match{Binding: "eligible", Target: gess.TemplateKeyFact(eligibleTemplate)},
		Returns:       []gess.QueryReturnSpec{gess.ReturnValue("customer", gess.BindingFieldExpr{Binding: "eligible", Field: "customer"})},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func stringValue(row gess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}
