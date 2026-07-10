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
	systemTemplate       = rules.TemplateKey("system")
	connectionTemplate   = rules.TemplateKey("connection")
	controlTemplate      = rules.TemplateKey("segmentation-control")
	reachableTemplate    = rules.TemplateKey("reachable")
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
	session, err := sess.New(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	for _, fact := range []struct {
		template rules.TemplateKey
		fields   rules.Fields
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
		if _, err := session.Assert(ctx, fact.template, fact.fields); err != nil {
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
		rows, err := session.QueryAll(ctx, canReachQuery, sess.QueryArgs{"src": q.src, "dst": q.dst})
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

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := sess.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: string(systemTemplate),
		Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "tier", Kind: rules.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: string(connectionTemplate),
		Fields: []rules.FieldSpec{
			{Name: "src", Kind: rules.ValueString, Required: true},
			{Name: "dst", Kind: rules.ValueString, Required: true},
			{Name: "protocol", Kind: rules.ValueString, Required: true},
			{Name: "open", Kind: rules.ValueBool, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: string(controlTemplate),
		Fields: []rules.FieldSpec{
			{Name: "src", Kind: rules.ValueString, Required: true},
			{Name: "dst", Kind: rules.ValueString, Required: true},
			{Name: "reason", Kind: rules.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name:              string(reachableTemplate),
		BackchainReactive: true,
		DuplicatePolicy:   rules.DuplicateUniqueKey,
		DuplicateKeyNames: []string{"src", "dst"},
		Fields: []rules.FieldSpec{
			{Name: "src", Kind: rules.ValueString, Required: true},
			{Name: "dst", Kind: rules.ValueString, Required: true},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: assertDirectPath,
		AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
			TemplateKey: reachableTemplate,
			Values: []rules.ExpressionSpec{
				rules.BindingFieldExpr{Binding: "need", Field: "dst"},
				rules.BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: assertTransitivePath,
		AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
			TemplateKey: reachableTemplate,
			Values: []rules.ExpressionSpec{
				rules.BindingFieldExpr{Binding: "need", Field: "dst"},
				rules.BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "prove-direct-reachability",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-reachable"))},
			rules.Match{
				Binding: "connection",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "open", Operator: rules.FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: rules.TemplateKeyFact(connectionTemplate),
			},
			rules.Not{Condition: rules.Match{
				Binding: "control",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: rules.TemplateKeyFact(controlTemplate),
			}},
		}},
		Actions: []rules.RuleActionSpec{{Name: assertDirectPath}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "prove-transitive-reachability",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-reachable"))},
			rules.Match{
				Binding: "connection",
				FieldConstraints: []rules.FieldConstraintSpec{
					{Field: "open", Operator: rules.FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "src"}},
				},
				Target: rules.TemplateKeyFact(connectionTemplate),
			},
			rules.Match{
				Binding: "tail",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "connection", Field: "dst"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: rules.TemplateKeyFact(reachableTemplate),
			},
			rules.Not{Condition: rules.Match{
				Binding: "control",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "src", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "connection", Field: "dst"}},
				},
				Target: rules.TemplateKeyFact(controlTemplate),
			}},
		}},
		Actions: []rules.RuleActionSpec{{Name: assertTransitivePath}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name: canReachQuery,
		Parameters: []rules.QueryParameterSpec{
			{Name: "src", Kind: rules.ValueString},
			{Name: "dst", Kind: rules.ValueString},
		},
		ConditionTree: rules.Match{
			Binding: "path",
			Predicates: []rules.ExpressionSpec{
				rules.CompareExpr{Operator: rules.ExpressionCompareEqual, Left: rules.CurrentFieldExpr{Field: "src"}, Right: rules.ParamExpr{Name: "src"}},
				rules.CompareExpr{Operator: rules.ExpressionCompareEqual, Left: rules.CurrentFieldExpr{Field: "dst"}, Right: rules.ParamExpr{Name: "dst"}},
			},
			Target: rules.TemplateKeyFact(reachableTemplate),
		},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("src", rules.BindingFieldExpr{Binding: "path", Field: "src"}),
			rules.ReturnValue("dst", rules.BindingFieldExpr{Binding: "path", Field: "dst"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func fields(pairs ...any) rules.Fields {
	return rules.MustFields(pairs...)
}
