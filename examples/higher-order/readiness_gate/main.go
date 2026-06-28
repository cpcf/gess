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
	requestTemplate  = gess.TemplateKey("deployment-request")
	checkTemplate    = gess.TemplateKey("release-check")
	hasCheckTemplate = gess.TemplateKey("deployment-has-check")
	gateTemplate     = gess.TemplateKey("rollout-gate")
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
		{requestTemplate, exampleutil.Fields("service", "checkout", "version", "2026.06.1")},
		{checkTemplate, exampleutil.Fields("service", "checkout", "name", "tests", "status", "pass")},
		{checkTemplate, exampleutil.Fields("service", "checkout", "name", "security", "status", "pass")},
		{requestTemplate, exampleutil.Fields("service", "billing", "version", "2026.06.1")},
		{checkTemplate, exampleutil.Fields("service", "billing", "name", "tests", "status", "pass")},
		{checkTemplate, exampleutil.Fields("service", "billing", "name", "security", "status", "fail")},
	} {
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}
	if _, err := session.Run(ctx); err != nil {
		return err
	}
	rows, err := session.QueryAll(ctx, "open-gates", nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintf(out, "%s %s: gate open\n", stringValue(row, "service"), stringValue(row, "version"))
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	for _, spec := range []gess.TemplateSpec{
		{Name: string(requestTemplate), Fields: []gess.FieldSpec{
			{Name: "service", Kind: gess.ValueString, Required: true},
			{Name: "version", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(checkTemplate), DuplicatePolicy: gess.DuplicateAllow, Fields: []gess.FieldSpec{
			{Name: "service", Kind: gess.ValueString, Required: true},
			{Name: "name", Kind: gess.ValueString, Required: true},
			{Name: "status", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(hasCheckTemplate), DuplicatePolicy: gess.DuplicateUniqueKey, DuplicateKeyNames: []string{"service", "version"}, Fields: []gess.FieldSpec{
			{Name: "service", Kind: gess.ValueString, Required: true},
			{Name: "version", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(gateTemplate), DuplicatePolicy: gess.DuplicateUniqueKey, DuplicateKeyNames: []string{"service", "version"}, Fields: []gess.FieldSpec{
			{Name: "service", Kind: gess.ValueString, Required: true},
			{Name: "version", Kind: gess.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "mark-has-check",
		Fn: func(ctx gess.ActionContext) error {
			service, _ := ctx.BindingScalarValue("request", "service")
			version, _ := ctx.BindingScalarValue("request", "version")
			_, err := ctx.AssertTemplate(hasCheckTemplate, gess.Fields{"service": service, "version": version})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "open-gate",
		Fn: func(ctx gess.ActionContext) error {
			service, _ := ctx.BindingScalarValue("request", "service")
			version, _ := ctx.BindingScalarValue("request", "version")
			_, err := ctx.AssertTemplate(gateTemplate, gess.Fields{"service": service, "version": version})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "deployment-request-has-checks",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "request", Target: gess.TemplateKeyFact(requestTemplate)},
			gess.Exists(gess.Match{
				Binding: "check",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "service", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "request", Field: "service"}},
				},
				Target: gess.TemplateKeyFact(checkTemplate),
			}),
		}},
		Actions: []gess.RuleActionSpec{{Name: "mark-has-check"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "deployment-request-with-all-checks-passing",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "request", Target: gess.TemplateKeyFact(requestTemplate)},
			gess.Match{
				Binding: "hasCheck",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "service", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "request", Field: "service"}},
					{Field: "version", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "request", Field: "version"}},
				},
				Target: gess.TemplateKeyFact(hasCheckTemplate),
			},
			gess.Forall(
				gess.Match{
					Binding: "candidate",
					JoinConstraints: []gess.JoinConstraintSpec{
						{Field: "service", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "request", Field: "service"}},
					},
					Target: gess.TemplateKeyFact(checkTemplate),
				},
				gess.Test{Expression: gess.CompareExpr{
					Operator: gess.ExpressionCompareEqual,
					Left:     gess.BindingPath("candidate", gess.Path("status")),
					Right:    gess.ConstExpr{Value: "pass"},
				}},
			),
		}},
		Actions: []gess.RuleActionSpec{{Name: "open-gate"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name:          "open-gates",
		ConditionTree: gess.Match{Binding: "gate", Target: gess.TemplateKeyFact(gateTemplate)},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("service", gess.BindingFieldExpr{Binding: "gate", Field: "service"}),
			gess.ReturnValue("version", gess.BindingFieldExpr{Binding: "gate", Field: "version"}),
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
