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
	accountTemplate     = gess.TemplateKey("account")
	transactionTemplate = gess.TemplateKey("transaction")
	alertTemplate       = gess.TemplateKey("velocity-alert")
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
		{accountTemplate, exampleutil.Fields("id", "A-100")},
		{accountTemplate, exampleutil.Fields("id", "A-200")},
		{transactionTemplate, exampleutil.Fields("id", "T-1", "account", "A-100", "amount", 450, "window", "5m")},
		{transactionTemplate, exampleutil.Fields("id", "T-2", "account", "A-100", "amount", 400, "window", "5m")},
		{transactionTemplate, exampleutil.Fields("id", "T-3", "account", "A-100", "amount", 350, "window", "5m")},
		{transactionTemplate, exampleutil.Fields("id", "T-4", "account", "A-200", "amount", 950, "window", "5m")},
	} {
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}
	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "velocity-alerts", nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s: %d transactions totaling %d\n", stringValue(row, "account"), intValue(row, "count"), intValue(row, "total"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	for _, spec := range []gess.TemplateSpec{
		{Name: string(accountTemplate), Fields: []gess.FieldSpec{{Name: "id", Kind: gess.ValueString, Required: true}}},
		{Name: string(transactionTemplate), DuplicatePolicy: gess.DuplicateAllow, Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "account", Kind: gess.ValueString, Required: true},
			{Name: "amount", Kind: gess.ValueInt, Required: true},
			{Name: "window", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(alertTemplate), DuplicatePolicy: gess.DuplicateUniqueKey, DuplicateKeyNames: []string{"account"}, Fields: []gess.FieldSpec{
			{Name: "account", Kind: gess.ValueString, Required: true},
			{Name: "count", Kind: gess.ValueInt, Required: true},
			{Name: "total", Kind: gess.ValueInt, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "assert-alert",
		Fn: func(ctx gess.ActionContext) error {
			account, _ := ctx.BindingScalarValue("account", "id")
			count, _ := ctx.BindingValue("count")
			total, _ := ctx.BindingValue("total")
			countValue, _ := count.AsInt64()
			totalValue, _ := total.AsInt64()
			if countValue < 3 || totalValue < 1000 {
				return nil
			}
			_, err := ctx.AssertTemplate(alertTemplate, gess.Fields{"account": account, "count": count, "total": total})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "three-transactions-over-thousand",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "account", Target: gess.TemplateKeyFact(accountTemplate)},
			gess.Accumulate(
				gess.Match{
					Binding: "transaction",
					FieldConstraints: []gess.FieldConstraintSpec{
						{Field: "window", Operator: gess.FieldConstraintEqual, Value: "5m"},
					},
					JoinConstraints: []gess.JoinConstraintSpec{
						{Field: "account", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "account", Field: "id"}},
					},
					Target: gess.TemplateKeyFact(transactionTemplate),
				},
				gess.Count().As("count"),
				gess.Sum(gess.BindingFieldExpr{Binding: "transaction", Field: "amount"}).As("total"),
			),
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-alert"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name:          "velocity-alerts",
		ConditionTree: gess.Match{Binding: "alert", Target: gess.TemplateKeyFact(alertTemplate)},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("account", gess.BindingFieldExpr{Binding: "alert", Field: "account"}),
			gess.ReturnValue("count", gess.BindingFieldExpr{Binding: "alert", Field: "count"}),
			gess.ReturnValue("total", gess.BindingFieldExpr{Binding: "alert", Field: "total"}),
		},
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

func intValue(row gess.QueryRow, alias string) int64 {
	value, _ := row.Value(alias)
	out, _ := value.AsInt64()
	return out
}
