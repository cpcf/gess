package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess"
)

const (
	claimTemplate          = gess.TemplateKey("claim")
	policyTemplate         = gess.TemplateKey("policy")
	coverageTemplate       = gess.TemplateKey("coverage")
	exclusionTemplate      = gess.TemplateKey("exclusion")
	duplicateTemplate      = gess.TemplateKey("duplicate-claim")
	vendorTemplate         = gess.TemplateKey("vendor")
	estimateTemplate       = gess.TemplateKey("repair-estimate")
	approvedVendorTemplate = gess.TemplateKey("approved-vendor")
	payableClaimTemplate   = gess.TemplateKey("payable-claim")
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
	session, err := gess.NewSession(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	for _, fact := range []struct {
		template gess.TemplateKey
		fields   gess.Fields
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
		if _, err := session.AssertTemplate(ctx, fact.template, fact.fields); err != nil {
			return err
		}
	}

	for _, claim := range []string{"C-100", "C-200", "C-300", "C-400"} {
		rows, err := session.QueryAll(ctx, payableQuery, gess.QueryArgs{"claim": claim})
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

func buildRuleset(ctx context.Context) (*gess.Ruleset, error) {
	workspace := gess.NewWorkspace()
	for _, spec := range []gess.TemplateSpec{
		{Name: string(claimTemplate), Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "policy", Kind: gess.ValueString, Required: true},
			{Name: "cause", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(policyTemplate), Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "status", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(coverageTemplate), Fields: []gess.FieldSpec{
			{Name: "policy", Kind: gess.ValueString, Required: true},
			{Name: "cause", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(exclusionTemplate), Fields: []gess.FieldSpec{
			{Name: "policy", Kind: gess.ValueString, Required: true},
			{Name: "cause", Kind: gess.ValueString, Required: true},
			{Name: "reason", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(duplicateTemplate), Fields: []gess.FieldSpec{
			{Name: "claim", Kind: gess.ValueString, Required: true},
			{Name: "original", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(vendorTemplate), Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "parent", Kind: gess.ValueString, Required: true},
			{Name: "status", Kind: gess.ValueString, Required: true},
		}},
		{Name: string(estimateTemplate), Fields: []gess.FieldSpec{
			{Name: "claim", Kind: gess.ValueString, Required: true},
			{Name: "vendor", Kind: gess.ValueString, Required: true},
		}},
		{
			Name:              string(approvedVendorTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   gess.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"vendor"},
			Fields:            []gess.FieldSpec{{Name: "vendor", Kind: gess.ValueString, Required: true}},
		},
		{
			Name:              string(payableClaimTemplate),
			BackchainReactive: true,
			DuplicatePolicy:   gess.DuplicateUniqueKey,
			DuplicateKeyNames: []string{"claim"},
			Fields: []gess.FieldSpec{
				{Name: "claim", Kind: gess.ValueString, Required: true},
				{Name: "result", Kind: gess.ValueString, Required: true},
			},
		},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	for _, spec := range []gess.ActionSpec{
		{
			Name: "assert-direct-approved-vendor",
			AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
				TemplateKey: approvedVendorTemplate,
				Values:      []gess.ExpressionSpec{gess.BindingFieldExpr{Binding: "need", Field: "vendor"}},
			},
		},
		{
			Name: "assert-inherited-approved-vendor",
			AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
				TemplateKey: approvedVendorTemplate,
				Values:      []gess.ExpressionSpec{gess.BindingFieldExpr{Binding: "need", Field: "vendor"}},
			},
		},
		{
			Name: "assert-payable-claim",
			AssertTemplateValues: &gess.AssertTemplateValuesActionSpec{
				TemplateKey: payableClaimTemplate,
				Values: []gess.ExpressionSpec{
					gess.BindingFieldExpr{Binding: "need", Field: "claim"},
					gess.ConstExpr{Value: "payable"},
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
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "prove-payable-property-claim",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-payable-claim"))},
			gess.Match{
				Binding: "claim",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "id", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "claim"}},
				},
				Target: gess.TemplateKeyFact(claimTemplate),
			},
			gess.Match{
				Binding:          "policy",
				FieldConstraints: []gess.FieldConstraintSpec{{Field: "status", Operator: gess.FieldConstraintEqual, Value: "active"}},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "id", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "policy"}},
				},
				Target: gess.TemplateKeyFact(policyTemplate),
			},
			gess.Match{
				Binding: "coverage",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "policy", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "policy"}},
					{Field: "cause", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "cause"}},
				},
				Target: gess.TemplateKeyFact(coverageTemplate),
			},
			gess.Match{
				Binding: "estimate",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "claim", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "id"}},
				},
				Target: gess.TemplateKeyFact(estimateTemplate),
			},
			gess.Match{
				Binding: "approved",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "vendor", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "estimate", Field: "vendor"}},
				},
				Target: gess.TemplateKeyFact(approvedVendorTemplate),
			},
			gess.Not{Condition: gess.Match{
				Binding: "exclusion",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "policy", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "policy"}},
					{Field: "cause", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "cause"}},
				},
				Target: gess.TemplateKeyFact(exclusionTemplate),
			}},
			gess.Not{Condition: gess.Match{
				Binding: "duplicate",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "claim", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "claim", Field: "id"}},
				},
				Target: gess.TemplateKeyFact(duplicateTemplate),
			}},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-payable-claim"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddQuery(gess.QuerySpec{
		Name:       payableQuery,
		Parameters: []gess.QueryParameterSpec{{Name: "claim", Kind: gess.ValueString}},
		ConditionTree: gess.Match{
			Binding: "payable",
			Predicates: []gess.ExpressionSpec{
				gess.CompareExpr{Operator: gess.ExpressionCompareEqual, Left: gess.CurrentFieldExpr{Field: "claim"}, Right: gess.ParamExpr{Name: "claim"}},
			},
			Target: gess.TemplateKeyFact(payableClaimTemplate),
		},
		Returns: []gess.QueryReturnSpec{
			gess.ReturnValue("claim", gess.BindingFieldExpr{Binding: "payable", Field: "claim"}),
			gess.ReturnValue("result", gess.BindingFieldExpr{Binding: "payable", Field: "result"}),
		},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func addVendorRules(workspace *gess.Workspace) error {
	if err := workspace.AddRule(gess.RuleSpec{
		Name: "prove-direct-approved-vendor",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-approved-vendor"))},
			gess.Match{
				Binding:          "vendor",
				FieldConstraints: []gess.FieldConstraintSpec{{Field: "status", Operator: gess.FieldConstraintEqual, Value: "approved"}},
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "id", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "vendor"}},
				},
				Target: gess.TemplateKeyFact(vendorTemplate),
			},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-direct-approved-vendor"}},
	}); err != nil {
		return err
	}
	return workspace.AddRule(gess.RuleSpec{
		Name: "prove-inherited-approved-vendor",
		ConditionTree: gess.And{Conditions: []gess.ConditionSpec{
			gess.Match{Binding: "need", Target: gess.TemplateKeyFact(gess.TemplateKey("need-approved-vendor"))},
			gess.Match{
				Binding: "vendor",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "id", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "need", Field: "vendor"}},
				},
				Target: gess.TemplateKeyFact(vendorTemplate),
			},
			gess.Match{
				Binding: "parent",
				JoinConstraints: []gess.JoinConstraintSpec{
					{Field: "vendor", Operator: gess.FieldConstraintEqual, Ref: gess.FieldRef{Binding: "vendor", Field: "parent"}},
				},
				Target: gess.TemplateKeyFact(approvedVendorTemplate),
			},
		}},
		Actions: []gess.RuleActionSpec{{Name: "assert-inherited-approved-vendor"}},
	})
}

func fields(pairs ...any) gess.Fields {
	return gess.MustFields(pairs...)
}
