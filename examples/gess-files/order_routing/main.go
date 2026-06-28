package main

import (
	"context"
	"fmt"
	"io"
	"os"

	dsl "github.com/cpcf/gess/dsl"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
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
	session, err := sess.New(ruleset, sess.WithInitialFacts(initials...))
	if err != nil {
		return err
	}
	defer session.Close()

	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "routes-by-lane", sess.QueryArgs{"lane": "expedite"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s -> %s\n", stringValue(row, "order"), stringValue(row, "warehouse"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*rules.Ruleset, []sess.InitialFact, error) {
	return buildGeneratedRuleset(ctx, dsl.Registry{})
}

func stringValue(row sess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}
