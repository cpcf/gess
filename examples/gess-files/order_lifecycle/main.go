// Command order_lifecycle shows the .gess mutation verbs — modify, retract,
// bind, and emit — compiled to Go with gessc. Shipped orders are modified in
// place, cancelled orders are retracted, and each rule emits a line to the
// session output writer.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	dsl "github.com/cpcf/gess/dsl"
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
	ruleset, initials, err := buildGeneratedRuleset(ctx, dsl.Registry{})
	if err != nil {
		return err
	}
	session, err := sess.New(ruleset,
		sess.WithInitialFacts(initials...),
		sess.WithOutputWriter(out),
	)
	if err != nil {
		return err
	}
	defer session.Close()

	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "shipped-orders", sess.QueryArgs{})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "shipped: %s total %d\n", stringValue(row, "id"), intValue(row, "total"))
	}
	return nil
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
