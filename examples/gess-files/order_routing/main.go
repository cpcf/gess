package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess"
)

//go:generate go run ../../../cmd/gessc -package main -func buildGeneratedRuleset -o rules_generated.go rules.gess

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	ctx := context.Background()
	ruleset, initials, err := buildRuleset(ctx)
	if err != nil {
		return err
	}
	session, err := gess.NewSession(ruleset, gess.WithInitialFacts(initials...))
	if err != nil {
		return err
	}
	defer session.Close()

	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "routes-by-lane", gess.QueryArgs{"lane": "expedite"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s -> %s\n", stringValue(row, "order"), stringValue(row, "warehouse"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, []gess.SessionInitialFact, error) {
	return buildGeneratedRuleset(ctx, gess.DSLRegistry{})
}

func stringValue(row gess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}
