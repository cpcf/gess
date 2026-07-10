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
	accountTemplate     = rules.TemplateKey("account")
	transactionTemplate = rules.TemplateKey("transaction")
	alertTemplate       = rules.TemplateKey("velocity-alert")
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
		{accountTemplate, exampleutil.Fields("id", "A-100")},
		{accountTemplate, exampleutil.Fields("id", "A-200")},
		{transactionTemplate, exampleutil.Fields("id", "T-1", "account", "A-100", "amount", 450, "window", "5m")},
		{transactionTemplate, exampleutil.Fields("id", "T-2", "account", "A-100", "amount", 400, "window", "5m")},
		{transactionTemplate, exampleutil.Fields("id", "T-3", "account", "A-100", "amount", 350, "window", "5m")},
		{transactionTemplate, exampleutil.Fields("id", "T-4", "account", "A-200", "amount", 950, "window", "5m")},
	} {
		if _, err := session.Assert(ctx, fact.template, fact.fields); err != nil {
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

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := sess.NewWorkspace()
	for _, spec := range []rules.TemplateSpec{
		{Name: string(accountTemplate), Fields: []rules.FieldSpec{{Name: "id", Kind: rules.ValueString, Required: true}}},
		{Name: string(transactionTemplate), DuplicatePolicy: rules.DuplicateAllow, Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "account", Kind: rules.ValueString, Required: true},
			{Name: "amount", Kind: rules.ValueInt, Required: true},
			{Name: "window", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(alertTemplate), DuplicatePolicy: rules.DuplicateUniqueKey, DuplicateKeyNames: []string{"account"}, Fields: []rules.FieldSpec{
			{Name: "account", Kind: rules.ValueString, Required: true},
			{Name: "count", Kind: rules.ValueInt, Required: true},
			{Name: "total", Kind: rules.ValueInt, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "assert-alert",
		Fn: func(ctx rules.ActionContext) error {
			account, _ := ctx.BindingScalarValue("account", "id")
			count, _ := ctx.BindingValue("count")
			total, _ := ctx.BindingValue("total")
			countValue, _ := count.AsInt64()
			totalValue, _ := total.AsInt64()
			if countValue < 3 || totalValue < 1000 {
				return nil
			}
			_, err := ctx.Assert(alertTemplate, rules.Fields{"account": account, "count": count, "total": total})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "three-transactions-over-thousand",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "account", Target: rules.TemplateKeyFact(accountTemplate)},
			rules.Accumulate(
				rules.Match{
					Binding: "transaction",
					FieldConstraints: []rules.FieldConstraintSpec{
						{Field: "window", Operator: rules.FieldConstraintEqual, Value: "5m"},
					},
					JoinConstraints: []rules.JoinConstraintSpec{
						{Field: "account", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "account", Field: "id"}},
					},
					Target: rules.TemplateKeyFact(transactionTemplate),
				},
				rules.Count().As("count"),
				rules.Sum(rules.BindingFieldExpr{Binding: "transaction", Field: "amount"}).As("total"),
			),
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-alert"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name:          "velocity-alerts",
		ConditionTree: rules.Match{Binding: "alert", Target: rules.TemplateKeyFact(alertTemplate)},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("account", rules.BindingFieldExpr{Binding: "alert", Field: "account"}),
			rules.ReturnValue("count", rules.BindingFieldExpr{Binding: "alert", Field: "count"}),
			rules.ReturnValue("total", rules.BindingFieldExpr{Binding: "alert", Field: "total"}),
		},
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

func intValue(row sess.QueryRow, alias string) int64 {
	value, _ := row.Value(alias)
	out, _ := value.AsInt64()
	return out
}
