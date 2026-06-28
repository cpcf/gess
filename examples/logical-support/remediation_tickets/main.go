package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess"
	"github.com/cpcf/gess/examples/internal/exampleutil"
)

const findingTemplate = gess.TemplateKey("finding")

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

	finding, err := session.AssertTemplate(ctx, findingTemplate, exampleutil.Fields("id", "F-100", "system", "api-01", "severity", "critical"))
	if err != nil {
		return err
	}
	if _, err := session.Run(ctx); err != nil {
		return err
	}
	before, err := session.Snapshot(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "tickets before retract: %d\n", len(before.FactsByName("remediation-ticket")))
	fmt.Fprintf(out, "support edges before retract: %d\n", len(before.SupportGraph().Edges))

	if _, err := session.Retract(ctx, finding.Fact.ID()); err != nil {
		return err
	}
	after, err := session.Snapshot(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "tickets after retract: %d\n", len(after.FactsByName("remediation-ticket")))
	fmt.Fprintf(out, "cascade retractions: %d\n", after.SupportGraph().Counters.CascadeRetractions)
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	if err := workspace.AddTemplate(gess.TemplateSpec{
		Name: string(findingTemplate),
		Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "system", Kind: gess.ValueString, Required: true},
			{Name: "severity", Kind: gess.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "open-ticket",
		Fn: func(ctx gess.ActionContext) error {
			id, _ := ctx.BindingScalarValue("finding", "id")
			system, _ := ctx.BindingScalarValue("finding", "system")
			_, err := ctx.AssertLogical("remediation-ticket", gess.Fields{"finding": id, "system": system})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "critical-finding-opens-ticket",
		ConditionTree: gess.Match{
			Binding: "finding",
			FieldConstraints: []gess.FieldConstraintSpec{
				{Field: "severity", Operator: gess.FieldConstraintEqual, Value: "critical"},
			},
			Target: gess.TemplateKeyFact(findingTemplate),
		},
		Actions: []gess.RuleActionSpec{{Name: "open-ticket"}},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}
