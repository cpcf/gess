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
	accountTemplate = gess.TemplateKey("account")
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

	for _, fact := range []gess.Fields{
		exampleutil.Fields("id", "A-100", "region", "emea", "balance", 120000),
		exampleutil.Fields("id", "A-200", "region", "emea", "balance", 9000),
		exampleutil.Fields("id", "A-300", "region", "amer", "balance", 250000),
	} {
		if _, err := session.AssertTemplate(ctx, accountTemplate, fact); err != nil {
			return err
		}
	}
	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "accounts-by-region", gess.QueryArgs{"region": "emea"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s: balance %d\n", stringValue(row, "id"), intValue(row, "balance"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	if err := workspace.AddTemplate(gess.TemplateSpec{
		Name: string(accountTemplate),
		Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "region", Kind: gess.ValueString, Required: true},
			{Name: "balance", Kind: gess.ValueInt, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name:       "accounts-by-region",
		Parameters: []gess.QueryParameterSpec{{Name: "region", Kind: gess.ValueString}},
		ConditionTree: gess.Match{
			Binding: "account",
			Predicates: []gess.ExpressionSpec{
				gess.CompareExpr{Operator: gess.ExpressionCompareEqual, Left: gess.CurrentFieldExpr{Field: "region"}, Right: gess.ParamExpr{Name: "region"}},
			},
			Target: gess.TemplateKeyFact(accountTemplate),
		},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("id", gess.BindingFieldExpr{Binding: "account", Field: "id"}),
			gess.ReturnValue("balance", gess.BindingFieldExpr{Binding: "account", Field: "balance"}),
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
