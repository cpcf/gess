package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess"
)

const (
	serviceTemplate             = gess.TemplateKey("service")
	dependencyTemplate          = gess.TemplateKey("dependency")
	advisoryTemplate            = gess.TemplateKey("security-advisory")
	waiverTemplate              = gess.TemplateKey("risk-waiver")
	reachableDependencyTemplate = gess.TemplateKey("reachable-dependency")
	serviceImpactTemplate       = gess.TemplateKey("service-impact")
	impactQuery                 = "service-affected-by-cve"
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

	facts := []struct {
		template gess.TemplateKey
		fields   gess.Fields
	}{
		{serviceTemplate, fields("name", "checkout", "root", "checkout-api")},
		{serviceTemplate, fields("name", "billing", "root", "billing-worker")},
		{dependencyTemplate, fields("src", "checkout-api", "dst", "auth-lib", "scope", "runtime")},
		{dependencyTemplate, fields("src", "auth-lib", "dst", "json-parser", "scope", "runtime")},
		{dependencyTemplate, fields("src", "billing-worker", "dst", "reporting-lib", "scope", "runtime")},
		{dependencyTemplate, fields("src", "reporting-lib", "dst", "json-parser", "scope", "test")},
		{advisoryTemplate, fields("package", "json-parser", "cve", "CVE-2026-4242", "severity", "critical")},
		{advisoryTemplate, fields("package", "reporting-lib", "cve", "CVE-2026-9000", "severity", "high")},
		{waiverTemplate, fields("service", "billing", "cve", "CVE-2026-9000", "reason", "isolated batch job")},
	}
	for _, fact := range facts {
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}

	for _, q := range []struct {
		service string
		cve     string
	}{
		{"checkout", "CVE-2026-4242"},
		{"billing", "CVE-2026-4242"},
		{"billing", "CVE-2026-9000"},
	} {
		rows, err := session.QueryAll(ctx, impactQuery, gess.QueryArgs{"service": q.service, "cve": q.cve})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Fprintf(out, "%s affected by %s: no\n", q.service, q.cve)
			continue
		}
		fmt.Fprintf(out, "%s affected by %s: yes\n", q.service, q.cve)
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	for _, spec := range []gess.TemplateSpec{
		{
			Name: string(serviceTemplate),
			Fields: []gess.FieldSpec{
				{Name: "name", Kind: gess.ValueString, Required: true},
				{Name: "root", Kind: gess.ValueString, Required: true},
			},
		},
		{
			Name: string(dependencyTemplate),
			Fields: []gess.FieldSpec{
				{Name: "src", Kind: gess.ValueString, Required: true},
				{Name: "dst", Kind: gess.ValueString, Required: true},
				{Name: "scope", Kind: gess.ValueString, Required: true},
			},
		},
		{
			Name: string(advisoryTemplate),
			Fields: []gess.FieldSpec{
				{Name: "package", Kind: gess.ValueString, Required: true},
				{Name: "cve", Kind: gess.ValueString, Required: true},
				{Name: "severity", Kind: gess.ValueString, Required: true},
			},
		},
		{
			Name: string(waiverTemplate),
			Fields: []gess.FieldSpec{
				{Name: "service", Kind: gess.ValueString, Required: true},
				{Name: "cve", Kind: gess.ValueString, Required: true},
				{Name: "reason", Kind: gess.ValueString, Required: true},
			},
		},
		{
			Name:              string(reachableDependencyTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   gess.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"src", "dst"},
			Fields: []gess.FieldSpec{
				{Name: "src", Kind: gess.ValueString, Required: true},
				{Name: "dst", Kind: gess.ValueString, Required: true},
			},
		},
		{
			Name:              string(serviceImpactTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   gess.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"service", "cve"},
			Fields: []gess.FieldSpec{
				{Name: "service", Kind: gess.ValueString, Required: true},
				{Name: "cve", Kind: gess.ValueString, Required: true},
			},
		},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	for _, spec := range []gess.ActionSpec{
		{
			Name: "assert-direct-reachable-dependency",
			AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
				TemplateKey: reachableDependencyTemplate,
				Values: []gess.ExpressionSpec{
					gess.BindingFieldExpr{Binding: "need", Field: "dst"},
					gess.BindingFieldExpr{Binding: "need", Field: "src"},
				},
			},
		},
		{
			Name: "assert-transitive-reachable-dependency",
			AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
				TemplateKey: reachableDependencyTemplate,
				Values: []gess.ExpressionSpec{
					gess.BindingFieldExpr{Binding: "need", Field: "dst"},
					gess.BindingFieldExpr{Binding: "need", Field: "src"},
				},
			},
		},
		{
			Name: "assert-service-impact",
			AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
				TemplateKey: serviceImpactTemplate,
				Values: []gess.ExpressionSpec{
					gess.BindingFieldExpr{Binding: "need", Field: "cve"},
					gess.BindingFieldExpr{Binding: "need", Field: "service"},
				},
			},
		},
	} {
		if err := workspace.AddAction(spec); err != nil {
			return nil, err
		}
	}
	if err := addReachableDependencyRules(workspace); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "prove-service-impact",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-service-impact"))},
			gess.Match{
				Binding: "service",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "name", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "service"}},
				},
				Target: gess.TemplateKeyFact(serviceTemplate),
			},
			gess.Match{
				Binding: "advisory",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "severity", Operator: gess.FieldConstraintEqual, Value: "critical"},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "cve", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "cve"}},
				},
				Target: gess.TemplateKeyFact(advisoryTemplate),
			},
			gess.Match{
				Binding: "path",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "service", Field: "root"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "advisory", Field: "package"}},
				},
				Target: gess.TemplateKeyFact(reachableDependencyTemplate),
			},
			gess.Not{Condition: gess.Match{
				Binding: "waiver",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "service", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "service"}},
					{Field: "cve", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "cve"}},
				},
				Target: gess.TemplateKeyFact(waiverTemplate),
			}},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-service-impact"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name: impactQuery,
		Parameters: []gess.QueryParameterSpec{
			{Name: "service", Kind: gess.ValueString},
			{Name: "cve", Kind: gess.ValueString},
		},
		ConditionTree: gess.Match{
			Binding: "impact",
			Predicates: []gess.ExpressionSpec{
				gess.CompareExpr{Operator: gess.ExpressionCompareEqual, Left: gess.CurrentFieldExpr{Field: "service"}, Right: gess.ParamExpr{Name: "service"}},
				gess.CompareExpr{Operator: gess.ExpressionCompareEqual, Left: gess.CurrentFieldExpr{Field: "cve"}, Right: gess.ParamExpr{Name: "cve"}},
			},
			Target: gess.TemplateKeyFact(serviceImpactTemplate),
		},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("service", gess.BindingFieldExpr{Binding: "impact", Field: "service"}),
			gess.ReturnValue("cve", gess.BindingFieldExpr{Binding: "impact", Field: "cve"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func addReachableDependencyRules(workspace *gess.Workspace) error {
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "prove-direct-runtime-dependency",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-reachable-dependency"))},
			gess.Match{
				Binding: "dependency",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "scope", Operator: gess.FieldConstraintEqual, Value: "runtime"},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: gess.TemplateKeyFact(dependencyTemplate),
			},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-direct-reachable-dependency"}},
	}); err != nil {
		return err
	}
	return workspace.AddRule(gess.RuleSpec{
		Name: "prove-transitive-runtime-dependency",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-reachable-dependency"))},
			gess.Match{
				Binding: "dependency",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "scope", Operator: gess.FieldConstraintEqual, Value: "runtime"},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "src"}},
				},
				Target: gess.TemplateKeyFact(dependencyTemplate),
			},
			gess.Match{
				Binding: "tail",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "dependency", Field: "dst"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: gess.TemplateKeyFact(reachableDependencyTemplate),
			},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-transitive-reachable-dependency"}},
	})
}

func fields(pairs ...any) gess.Fields {
	return gess.MustFields(pairs...)
}
