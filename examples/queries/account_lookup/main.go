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
	accountTemplate = rules.TemplateKey("account")
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

	for _, fact := range []rules.Fields{
		exampleutil.Fields("id", "A-100", "region", "emea", "balance", 120000),
		exampleutil.Fields("id", "A-200", "region", "emea", "balance", 9000),
		exampleutil.Fields("id", "A-300", "region", "amer", "balance", 250000),
	} {
		if _, err := session.Assert(ctx, accountTemplate, fact); err != nil {
			return err
		}
	}
	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "accounts-by-region", sess.QueryArgs{"region": "emea"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s: balance %d\n", stringValue(row, "id"), intValue(row, "balance"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := sess.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: string(accountTemplate),
		Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "region", Kind: rules.ValueString, Required: true},
			{Name: "balance", Kind: rules.ValueInt, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name:       "accounts-by-region",
		Parameters: []rules.QueryParameterSpec{{Name: "region", Kind: rules.ValueString}},
		ConditionTree: rules.Match{
			Binding: "account",
			Predicates: []rules.ExpressionSpec{
				rules.CompareExpr{Operator: rules.ExpressionCompareEqual, Left: rules.CurrentFieldExpr{Field: "region"}, Right: rules.ParamExpr{Name: "region"}},
			},
			Target: rules.TemplateKeyFact(accountTemplate),
		},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("id", rules.BindingFieldExpr{Binding: "account", Field: "id"}),
			rules.ReturnValue("balance", rules.BindingFieldExpr{Binding: "account", Field: "balance"}),
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
