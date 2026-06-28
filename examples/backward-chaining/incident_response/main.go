package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess"
)

const (
	systemTemplate       = gess.TemplateKey("system")
	connectionTemplate   = gess.TemplateKey("connection")
	controlTemplate      = gess.TemplateKey("segmentation-control")
	reachableTemplate    = gess.TemplateKey("reachable")
	canReachQuery        = "can-reach"
	assertDirectPath     = "assert-direct-path"
	assertTransitivePath = "assert-transitive-path"
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
		{systemTemplate, fields("id", "internet", "tier", "external")},
		{systemTemplate, fields("id", "web", "tier", "edge")},
		{systemTemplate, fields("id", "app", "tier", "service")},
		{systemTemplate, fields("id", "db", "tier", "data")},
		{systemTemplate, fields("id", "admin", "tier", "privileged")},
		{connectionTemplate, fields("src", "internet", "dst", "web", "protocol", "https", "open", true)},
		{connectionTemplate, fields("src", "web", "dst", "app", "protocol", "https", "open", true)},
		{connectionTemplate, fields("src", "app", "dst", "db", "protocol", "postgres", "open", true)},
		{connectionTemplate, fields("src", "internet", "dst", "admin", "protocol", "ssh", "open", true)},
		{controlTemplate, fields("src", "internet", "dst", "admin", "reason", "emergency-access-disabled")},
	} {
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}

	for _, q := range []struct {
		src string
		dst string
	}{
		{"internet", "db"},
		{"internet", "admin"},
		{"db", "internet"},
	} {
		rows, err := session.QueryAll(ctx, canReachQuery, gess.QueryArgs{"src": q.src, "dst": q.dst})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Fprintf(out, "%s -> %s: blocked\n", q.src, q.dst)
			continue
		}
		fmt.Fprintf(out, "%s -> %s: reachable\n", q.src, q.dst)
	}
	return nil
}

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	if err := workspace.AddTemplate(gess.TemplateSpec{
		Name: string(systemTemplate),
		Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "tier", Kind: gess.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddTemplate(gess.TemplateSpec{
		Name: string(connectionTemplate),
		Fields: []gess.FieldSpec{
			{Name: "src", Kind: gess.ValueString, Required: true},
			{Name: "dst", Kind: gess.ValueString, Required: true},
			{Name: "protocol", Kind: gess.ValueString, Required: true},
			{Name: "open", Kind: gess.ValueBool, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddTemplate(gess.TemplateSpec{
		Name: string(controlTemplate),
		Fields: []gess.FieldSpec{
			{Name: "src", Kind: gess.ValueString, Required: true},
			{Name: "dst", Kind: gess.ValueString, Required: true},
			{Name: "reason", Kind: gess.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddTemplate(gess.TemplateSpec{
		Name:              string(reachableTemplate),
		BackchainReactive: true,
		DuplicatePolicy:   gess.DuplicateUniqueKey,
		DuplicateKeyNames: []string{"src", "dst"},
		Fields: []gess.FieldSpec{
			{Name: "src", Kind: gess.ValueString, Required: true},
			{Name: "dst", Kind: gess.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: assertDirectPath,
		AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
			TemplateKey: reachableTemplate,
			Values: []gess.ExpressionSpec{
				gess.BindingFieldExpr{Binding: "need", Field: "dst"},
				gess.BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: assertTransitivePath,
		AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
			TemplateKey: reachableTemplate,
			Values: []gess.ExpressionSpec{
				gess.BindingFieldExpr{Binding: "need", Field: "dst"},
				gess.BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "prove-direct-reachability",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-reachable"))},
			gess.Match{
				Binding: "connection",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "open", Operator: gess.FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: gess.TemplateKeyFact(connectionTemplate),
			},
			gess.Not{Condition: gess.Match{
				Binding: "control",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: gess.TemplateKeyFact(controlTemplate),
			}},
		}},
		Actions: []gess.RuleActionSpec{{Name: assertDirectPath}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "prove-transitive-reachability",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-reachable"))},
			gess.Match{
				Binding: "connection",
				FieldConstraints: []gess.FieldConstraintSpec{
					{Field: "open", Operator: gess.FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "src"}},
				},
				Target: gess.TemplateKeyFact(connectionTemplate),
			},
			gess.Match{
				Binding: "tail",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "connection", Field: "dst"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: gess.TemplateKeyFact(reachableTemplate),
			},
			gess.Not{Condition: gess.Match{
				Binding: "control",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "src", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "connection", Field: "dst"}},
				},
				Target: gess.TemplateKeyFact(controlTemplate),
			}},
		}},
		Actions: []gess.RuleActionSpec{{Name: assertTransitivePath}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name: canReachQuery,
		Parameters: []gess.QueryParameterSpec{
			{Name: "src", Kind: gess.ValueString},
			{Name: "dst", Kind: gess.ValueString},
		},
		ConditionTree: gess.Match{
			Binding: "path",
			Predicates: []gess.ExpressionSpec{
				gess.CompareExpr{Operator: gess.ExpressionCompareEqual, Left: gess.CurrentFieldExpr{Field: "src"}, Right: gess.ParamExpr{Name: "src"}},
				gess.CompareExpr{Operator: gess.ExpressionCompareEqual, Left: gess.CurrentFieldExpr{Field: "dst"}, Right: gess.ParamExpr{Name: "dst"}},
			},
			Target: gess.TemplateKeyFact(reachableTemplate),
		},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("src", gess.BindingFieldExpr{Binding: "path", Field: "src"}),
			gess.ReturnValue("dst", gess.BindingFieldExpr{Binding: "path", Field: "dst"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func fields(pairs ...any) gess.Fields {
	return gess.MustFields(pairs...)
}
