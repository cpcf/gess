package main

import (
	"context"
	"fmt"
	"io"
	"os"

	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

const (
	serviceTemplate             = rules.TemplateKey("service")
	dependencyTemplate          = rules.TemplateKey("dependency")
	advisoryTemplate            = rules.TemplateKey("security-advisory")
	waiverTemplate              = rules.TemplateKey("risk-waiver")
	reachableDependencyTemplate = rules.TemplateKey("reachable-dependency")
	serviceImpactTemplate       = rules.TemplateKey("service-impact")
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
	session, err := sess.New(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	facts := []struct {
		template rules.TemplateKey
		fields   rules.Fields
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
		if _, err := session.Assert(ctx, fact.template, fact.fields); err != nil {
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
		rows, err := session.QueryAll(ctx, impactQuery, sess.QueryArgs{"service": q.service, "cve": q.cve})
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

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := rules.NewWorkspace()
	for _, spec := range []rules.TemplateSpec{
		{
			Name: string(serviceTemplate),
			Fields: []rules.FieldSpec{
				{Name: "name", Kind: rules.ValueString, Required: true},
				{Name: "root", Kind: rules.ValueString, Required: true},
			},
		},
		{
			Name: string(dependencyTemplate),
			Fields: []rules.FieldSpec{
				{Name: "src", Kind: rules.ValueString, Required: true},
				{Name: "dst", Kind: rules.ValueString, Required: true},
				{Name: "scope", Kind: rules.ValueString, Required: true},
			},
		},
		{
			Name: string(advisoryTemplate),
			Fields: []rules.FieldSpec{
				{Name: "package", Kind: rules.ValueString, Required: true},
				{Name: "cve", Kind: rules.ValueString, Required: true},
				{Name: "severity", Kind: rules.ValueString, Required: true},
			},
		},
		{
			Name: string(waiverTemplate),
			Fields: []rules.FieldSpec{
				{Name: "service", Kind: rules.ValueString, Required: true},
				{Name: "cve", Kind: rules.ValueString, Required: true},
				{Name: "reason", Kind: rules.ValueString, Required: true},
			},
		},
		{
			Name:              string(reachableDependencyTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   rules.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"src", "dst"},
			Fields: []rules.FieldSpec{
				{Name: "src", Kind: rules.ValueString, Required: true},
				{Name: "dst", Kind: rules.ValueString, Required: true},
			},
		},
		{
			Name:              string(serviceImpactTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   rules.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"service", "cve"},
			Fields: []rules.FieldSpec{
				{Name: "service", Kind: rules.ValueString, Required: true},
				{Name: "cve", Kind: rules.ValueString, Required: true},
			},
		},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	for _, spec := range []rules.ActionSpec{
		{
			Name: "assert-direct-reachable-dependency",
			AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
				TemplateKey: reachableDependencyTemplate,
				Values: []rules.ExpressionSpec{
					rules.BindingFieldExpr{Binding: "need", Field: "dst"},
					rules.BindingFieldExpr{Binding: "need", Field: "src"},
				},
			},
		},
		{
			Name: "assert-transitive-reachable-dependency",
			AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
				TemplateKey: reachableDependencyTemplate,
				Values: []rules.ExpressionSpec{
					rules.BindingFieldExpr{Binding: "need", Field: "dst"},
					rules.BindingFieldExpr{Binding: "need", Field: "src"},
				},
			},
		},
		{
			Name: "assert-service-impact",
			AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
				TemplateKey: serviceImpactTemplate,
				Values: []rules.ExpressionSpec{
					rules.BindingFieldExpr{Binding: "need", Field: "cve"},
					rules.BindingFieldExpr{Binding: "need", Field: "service"},
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
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "prove-service-impact",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-service-impact"))},
			rules.Match{
				Binding: "service",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "name", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "service"}},
				},
				Target: rules.TemplateKeyFact(serviceTemplate),
			},
			rules.Match{
				Binding: "advisory",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "severity", Operator: rules.FieldConstraintEqual, Value: "critical"},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "cve", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "cve"}},
				},
				Target: rules.TemplateKeyFact(advisoryTemplate),
			},
			rules.Match{
				Binding: "path",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "service", Field: "root"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "advisory", Field: "package"}},
				},
				Target: rules.TemplateKeyFact(reachableDependencyTemplate),
			},
			rules.Not{Condition: rules.Match{
				Binding: "waiver",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "service", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "service"}},
					{Field: "cve", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "cve"}},
				},
				Target: rules.TemplateKeyFact(waiverTemplate),
			}},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-service-impact"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name: impactQuery,
		Parameters: []rules.QueryParameterSpec{
			{Name: "service", Kind: rules.ValueString},
			{Name: "cve", Kind: rules.ValueString},
		},
		ConditionTree: rules.Match{
			Binding: "impact",
			Predicates: []rules.ExpressionSpec{
				rules.CompareExpr{Operator: rules.ExpressionCompareEqual, Left: rules.CurrentFieldExpr{Field: "service"}, Right: rules.ParamExpr{Name: "service"}},
				rules.CompareExpr{Operator: rules.ExpressionCompareEqual, Left: rules.CurrentFieldExpr{Field: "cve"}, Right: rules.ParamExpr{Name: "cve"}},
			},
			Target: rules.TemplateKeyFact(serviceImpactTemplate),
		},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("service", rules.BindingFieldExpr{Binding: "impact", Field: "service"}),
			rules.ReturnValue("cve", rules.BindingFieldExpr{Binding: "impact", Field: "cve"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func addReachableDependencyRules(workspace *rules.Workspace) error {
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "prove-direct-runtime-dependency",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-reachable-dependency"))},
			rules.Match{
				Binding: "dependency",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "scope", Operator: rules.FieldConstraintEqual, Value: "runtime"},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: rules.TemplateKeyFact(dependencyTemplate),
			},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-direct-reachable-dependency"}},
	}); err != nil {
		return err
	}
	return workspace.AddRule(rules.RuleSpec{
		Name: "prove-transitive-runtime-dependency",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-reachable-dependency"))},
			rules.Match{
				Binding: "dependency",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "scope", Operator: rules.FieldConstraintEqual, Value: "runtime"},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "src"}},
				},
				Target: rules.TemplateKeyFact(dependencyTemplate),
			},
			rules.Match{
				Binding: "tail",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "dependency", Field: "dst"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: rules.TemplateKeyFact(reachableDependencyTemplate),
			},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-transitive-reachable-dependency"}},
	})
}

func fields(pairs ...any) rules.Fields {
	return rules.MustFields(pairs...)
}
