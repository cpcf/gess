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
	requestTemplate  = rules.TemplateKey("deployment-request")
	checkTemplate    = rules.TemplateKey("release-check")
	hasCheckTemplate = rules.TemplateKey("deployment-has-check")
	gateTemplate     = rules.TemplateKey("rollout-gate")
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
		{requestTemplate, exampleutil.Fields("service", "checkout", "version", "2026.06.1")},
		{checkTemplate, exampleutil.Fields("service", "checkout", "name", "tests", "status", "pass")},
		{checkTemplate, exampleutil.Fields("service", "checkout", "name", "security", "status", "pass")},
		{requestTemplate, exampleutil.Fields("service", "billing", "version", "2026.06.1")},
		{checkTemplate, exampleutil.Fields("service", "billing", "name", "tests", "status", "pass")},
		{checkTemplate, exampleutil.Fields("service", "billing", "name", "security", "status", "fail")},
	} {
		if _, err := session.Assert(ctx, fact.template, fact.fields); err != nil {
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

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := rules.NewWorkspace()
	for _, spec := range []rules.TemplateSpec{
		{Name: string(requestTemplate), Fields: []rules.FieldSpec{
			{Name: "service", Kind: rules.ValueString, Required: true},
			{Name: "version", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(checkTemplate), DuplicatePolicy: rules.DuplicateAllow, Fields: []rules.FieldSpec{
			{Name: "service", Kind: rules.ValueString, Required: true},
			{Name: "name", Kind: rules.ValueString, Required: true},
			{Name: "status", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(hasCheckTemplate), DuplicatePolicy: rules.DuplicateUniqueKey, DuplicateKeyNames: []string{"service", "version"}, Fields: []rules.FieldSpec{
			{Name: "service", Kind: rules.ValueString, Required: true},
			{Name: "version", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(gateTemplate), DuplicatePolicy: rules.DuplicateUniqueKey, DuplicateKeyNames: []string{"service", "version"}, Fields: []rules.FieldSpec{
			{Name: "service", Kind: rules.ValueString, Required: true},
			{Name: "version", Kind: rules.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "mark-has-check",
		Fn: func(ctx rules.ActionContext) error {
			service, _ := ctx.BindingScalarValue("request", "service")
			version, _ := ctx.BindingScalarValue("request", "version")
			_, err := ctx.Assert(hasCheckTemplate, rules.Fields{"service": service, "version": version})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "open-gate",
		Fn: func(ctx rules.ActionContext) error {
			service, _ := ctx.BindingScalarValue("request", "service")
			version, _ := ctx.BindingScalarValue("request", "version")
			_, err := ctx.Assert(gateTemplate, rules.Fields{"service": service, "version": version})
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "deployment-request-has-checks",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "request", Target: rules.TemplateKeyFact(requestTemplate)},
			rules.Exists(rules.Match{
				Binding: "check",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "service", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "request", Field: "service"}},
				},
				Target: rules.TemplateKeyFact(checkTemplate),
			}),
		}},
		Actions: []rules.RuleActionSpec{{Name: "mark-has-check"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "deployment-request-with-all-checks-passing",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "request", Target: rules.TemplateKeyFact(requestTemplate)},
			rules.Match{
				Binding: "hasCheck",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "service", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "request", Field: "service"}},
					{Field: "version", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "request", Field: "version"}},
				},
				Target: rules.TemplateKeyFact(hasCheckTemplate),
			},
			rules.Forall(
				rules.Match{
					Binding: "candidate",
					JoinConstraints: []rules.JoinConstraintSpec{
						{Field: "service", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "request", Field: "service"}},
					},
					Target: rules.TemplateKeyFact(checkTemplate),
				},
				rules.Test{Expression: rules.CompareExpr{
					Operator: rules.ExpressionCompareEqual,
					Left:     rules.BindingPath("candidate", rules.Path("status")),
					Right:    rules.ConstExpr{Value: "pass"},
				}},
			),
		}},
		Actions: []rules.RuleActionSpec{{Name: "open-gate"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name:          "open-gates",
		ConditionTree: rules.Match{Binding: "gate", Target: rules.TemplateKeyFact(gateTemplate)},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("service", rules.BindingFieldExpr{Binding: "gate", Field: "service"}),
			rules.ReturnValue("version", rules.BindingFieldExpr{Binding: "gate", Field: "version"}),
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
