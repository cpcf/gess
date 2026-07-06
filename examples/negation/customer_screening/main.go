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
	customerTemplate = rules.TemplateKey("customer")
	holdTemplate     = rules.TemplateKey("compliance-hold")
	eligibleTemplate = rules.TemplateKey("eligible-customer")
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
	session, err := sess.New(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	for _, fact := range []struct {
		template rules.TemplateKey
		fields   rules.Fields
	}{
		{customerTemplate, exampleutil.Fields("id", "C-100", "status", "active")},
		{customerTemplate, exampleutil.Fields("id", "C-200", "status", "active")},
		{customerTemplate, exampleutil.Fields("id", "C-300", "status", "inactive")},
		{holdTemplate, exampleutil.Fields("customer", "C-200", "reason", "sanctions-review")},
	} {
		if _, err := session.Assert(ctx, fact.template, fact.fields); err != nil {
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

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := rules.NewWorkspace()
	for _, spec := range []rules.TemplateSpec{
		{Name: string(customerTemplate), Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "status", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(holdTemplate), Fields: []rules.FieldSpec{
			{Name: "customer", Kind: rules.ValueString, Required: true},
			{Name: "reason", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(eligibleTemplate), DuplicatePolicy: rules.DuplicateUniqueKey, DuplicateKeyNames: []string{"customer"}, Fields: []rules.FieldSpec{
			{Name: "customer", Kind: rules.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "assert-eligible",
		Fn: func(ctx rules.ActionContext) error {
			customer, _ := ctx.BindingScalarValue("customer", "id")
			_, err := ctx.Assert(eligibleTemplate, rules.Fields{"customer": customer})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "active-customer-without-hold",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{
				Binding: "customer",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "status", Operator: rules.FieldConstraintEqual, Value: "active"},
				},
				Target: rules.TemplateKeyFact(customerTemplate),
			},
			rules.Not{Condition: rules.Match{
				Binding: "hold",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "customer", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "customer", Field: "id"}},
				},
				Target: rules.TemplateKeyFact(holdTemplate),
			}},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-eligible"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name:          "eligible-customers",
		ConditionTree: rules.Match{Binding: "eligible", Target: rules.TemplateKeyFact(eligibleTemplate)},
		Returns:       []rules.QueryReturnSpec{rules.ReturnValue("customer", rules.BindingFieldExpr{Binding: "eligible", Field: "customer"})},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func stringValue(row sess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}
