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
	claimTemplate          = rules.TemplateKey("claim")
	policyTemplate         = rules.TemplateKey("policy")
	coverageTemplate       = rules.TemplateKey("coverage")
	exclusionTemplate      = rules.TemplateKey("exclusion")
	duplicateTemplate      = rules.TemplateKey("duplicate-claim")
	vendorTemplate         = rules.TemplateKey("vendor")
	estimateTemplate       = rules.TemplateKey("repair-estimate")
	approvedVendorTemplate = rules.TemplateKey("approved-vendor")
	payableClaimTemplate   = rules.TemplateKey("payable-claim")
	payableQuery           = "payable-claim"
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
		{policyTemplate, fields("id", "P-100", "status", "active")},
		{policyTemplate, fields("id", "P-200", "status", "active")},
		{coverageTemplate, fields("policy", "P-100", "cause", "wind")},
		{coverageTemplate, fields("policy", "P-200", "cause", "flood")},
		{exclusionTemplate, fields("policy", "P-200", "cause", "flood", "reason", "flood exclusion")},
		{vendorTemplate, fields("id", "national-restoration", "parent", "", "status", "approved")},
		{vendorTemplate, fields("id", "local-roofing", "parent", "national-restoration", "status", "pending")},
		{vendorTemplate, fields("id", "unknown-builder", "parent", "", "status", "pending")},
		{claimTemplate, fields("id", "C-100", "policy", "P-100", "cause", "wind")},
		{estimateTemplate, fields("claim", "C-100", "vendor", "local-roofing")},
		{claimTemplate, fields("id", "C-200", "policy", "P-200", "cause", "flood")},
		{estimateTemplate, fields("claim", "C-200", "vendor", "national-restoration")},
		{claimTemplate, fields("id", "C-300", "policy", "P-100", "cause", "wind")},
		{estimateTemplate, fields("claim", "C-300", "vendor", "unknown-builder")},
		{duplicateTemplate, fields("claim", "C-400", "original", "C-100")},
		{claimTemplate, fields("id", "C-400", "policy", "P-100", "cause", "wind")},
		{estimateTemplate, fields("claim", "C-400", "vendor", "national-restoration")},
	} {
		if _, err := session.Assert(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}

	for _, claim := range []string{"C-100", "C-200", "C-300", "C-400"} {
		rows, err := session.QueryAll(ctx, payableQuery, sess.QueryArgs{"claim": claim})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			fmt.Fprintf(out, "%s: not payable\n", claim)
			continue
		}
		result, _ := rows[0].Value("result")
		label, _ := result.AsString()
		fmt.Fprintf(out, "%s: %s\n", claim, label)
	}
	return nil
}

func buildRuleset(ctx context.Context) (*rules.Ruleset, error) {
	workspace := sess.NewWorkspace()
	for _, spec := range []rules.TemplateSpec{
		{Name: string(claimTemplate), Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "policy", Kind: rules.ValueString, Required: true},
			{Name: "cause", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(policyTemplate), Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "status", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(coverageTemplate), Fields: []rules.FieldSpec{
			{Name: "policy", Kind: rules.ValueString, Required: true},
			{Name: "cause", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(exclusionTemplate), Fields: []rules.FieldSpec{
			{Name: "policy", Kind: rules.ValueString, Required: true},
			{Name: "cause", Kind: rules.ValueString, Required: true},
			{Name: "reason", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(duplicateTemplate), Fields: []rules.FieldSpec{
			{Name: "claim", Kind: rules.ValueString, Required: true},
			{Name: "original", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(vendorTemplate), Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "parent", Kind: rules.ValueString, Required: true},
			{Name: "status", Kind: rules.ValueString, Required: true},
		}},
		{Name: string(estimateTemplate), Fields: []rules.FieldSpec{
			{Name: "claim", Kind: rules.ValueString, Required: true},
			{Name: "vendor", Kind: rules.ValueString, Required: true},
		}},
		{
			Name:              string(approvedVendorTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   rules.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"vendor"},
			Fields:            []rules.FieldSpec{{Name: "vendor", Kind: rules.ValueString, Required: true}},
		},
		{
			Name:              string(payableClaimTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   rules.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"claim"},
			Fields: []rules.FieldSpec{
				{Name: "claim", Kind: rules.ValueString, Required: true},
				{Name: "result", Kind: rules.ValueString, Required: true},
			},
		},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	for _, spec := range []rules.ActionSpec{
		{
			Name: "assert-direct-approved-vendor",
			AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
				TemplateKey: approvedVendorTemplate,
				Values:      []rules.ExpressionSpec{rules.BindingFieldExpr{Binding: "need", Field: "vendor"}},
			},
		},
		{
			Name: "assert-inherited-approved-vendor",
			AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
				TemplateKey: approvedVendorTemplate,
				Values:      []rules.ExpressionSpec{rules.BindingFieldExpr{Binding: "need", Field: "vendor"}},
			},
		},
		{
			Name: "assert-payable-claim",
			AssertTemplateValues: &rules.AssertTemplateValuesActionSpec{
				TemplateKey: payableClaimTemplate,
				Values: []rules.ExpressionSpec{
					rules.BindingFieldExpr{Binding: "need", Field: "claim"},
					rules.ConstExpr{Value: "payable"},
				},
			},
		},
	} {
		if err := workspace.AddAction(spec); err != nil {
			return nil, err
		}
	}
	if err := addVendorRules(workspace); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "prove-payable-property-claim",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-payable-claim"))},
			rules.Match{
				Binding: "claim",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "id", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "claim"}},
				},
				Target: rules.TemplateKeyFact(claimTemplate),
			},
			rules.Match{
				Binding:          "policy",
				FieldConstraints: []rules.FieldConstraintSpec{{Field: "status", Operator: rules.FieldConstraintEqual, Value: "active"}},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "id", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "policy"}},
				},
				Target: rules.TemplateKeyFact(policyTemplate),
			},
			rules.Match{
				Binding: "coverage",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "policy", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "policy"}},
					{Field: "cause", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "cause"}},
				},
				Target: rules.TemplateKeyFact(coverageTemplate),
			},
			rules.Match{
				Binding: "estimate",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "claim", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "id"}},
				},
				Target: rules.TemplateKeyFact(estimateTemplate),
			},
			rules.Match{
				Binding: "approved",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "vendor", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "estimate", Field: "vendor"}},
				},
				Target: rules.TemplateKeyFact(approvedVendorTemplate),
			},
			rules.Not{Condition: rules.Match{
				Binding: "exclusion",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "policy", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "policy"}},
					{Field: "cause", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "cause"}},
				},
				Target: rules.TemplateKeyFact(exclusionTemplate),
			}},
			rules.Not{Condition: rules.Match{
				Binding: "duplicate",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "claim", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "claim", Field: "id"}},
				},
				Target: rules.TemplateKeyFact(duplicateTemplate),
			}},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-payable-claim"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(rules.QuerySpec{
		Name:       payableQuery,
		Parameters: []rules.QueryParameterSpec{{Name: "claim", Kind: rules.ValueString}},
		ConditionTree: rules.Match{
			Binding: "payable",
			Predicates: []rules.ExpressionSpec{
				rules.CompareExpr{Operator: rules.ExpressionCompareEqual, Left: rules.CurrentFieldExpr{Field: "claim"}, Right: rules.ParamExpr{Name: "claim"}},
			},
			Target: rules.TemplateKeyFact(payableClaimTemplate),
		},
		Returns: []rules.QueryReturnSpec{
			rules.ReturnValue("claim", rules.BindingFieldExpr{Binding: "payable", Field: "claim"}),
			rules.ReturnValue("result", rules.BindingFieldExpr{Binding: "payable", Field: "result"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func addVendorRules(workspace *rules.Workspace) error {
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "prove-direct-approved-vendor",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-approved-vendor"))},
			rules.Match{
				Binding:          "vendor",
				FieldConstraints: []rules.FieldConstraintSpec{{Field: "status", Operator: rules.FieldConstraintEqual, Value: "approved"}},
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "id", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "vendor"}},
				},
				Target: rules.TemplateKeyFact(vendorTemplate),
			},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-direct-approved-vendor"}},
	}); err != nil {
		return err
	}
	return workspace.AddRule(rules.RuleSpec{
		Name: "prove-inherited-approved-vendor",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "need", Target: rules.TemplateKeyFact(rules.TemplateKey("need-approved-vendor"))},
			rules.Match{
				Binding: "vendor",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "id", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "need", Field: "vendor"}},
				},
				Target: rules.TemplateKeyFact(vendorTemplate),
			},
			rules.Match{
				Binding: "parent",
				JoinConstraints: []rules.JoinConstraintSpec{
					{Field: "vendor", Operator: rules.FieldConstraintEqual, Ref: rules.FieldRef{Binding: "vendor", Field: "parent"}},
				},
				Target: rules.TemplateKeyFact(approvedVendorTemplate),
			},
		}},
		Actions: []rules.RuleActionSpec{{Name: "assert-inherited-approved-vendor"}},
	})
}

func fields(pairs ...any) rules.Fields {
	return rules.MustFields(pairs...)
}
