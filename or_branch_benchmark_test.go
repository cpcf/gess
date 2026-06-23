package gess

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkReteRuntimeOrBranchExpansionVsSeparateRules(b *testing.B) {
	const factCount = 1000
	ctx := context.Background()
	facts := make([]SessionInitialFact, 0, factCount)
	for i := range factCount {
		facts = append(facts, SessionInitialFact{
			TemplateKey: "person",
			Fields: mustFields(b, map[string]any{
				"id":     fmt.Sprintf("p-%04d", i),
				"active": true,
				"dept":   "engineering",
			}),
		})
	}

	for _, tc := range []struct {
		name     string
		revision *Ruleset
	}{
		{name: "deduped-or", revision: benchmarkOrBranchRevision(b, false)},
		{name: "separate-rules", revision: benchmarkOrBranchRevision(b, true)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				session, err := NewSession(tc.revision, WithInitialFacts(facts...))
				if err != nil {
					b.Fatalf("NewSession: %v", err)
				}
				if _, err := session.Run(ctx); err != nil {
					b.Fatalf("Run: %v", err)
				}
			}
		})
	}
}

func benchmarkOrBranchRevision(tb testing.TB, separateRules bool) *Ruleset {
	tb.Helper()
	workspace := NewWorkspace()
	person := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	activeBranch := Match{
		Binding:     "person",
		TemplateKey: person.Key(),
		FieldConstraints: []FieldConstraintSpec{
			{Field: "active", Operator: FieldConstraintEqual, Value: true},
		},
	}
	deptBranch := Match{
		Binding:     "person",
		TemplateKey: person.Key(),
		FieldConstraints: []FieldConstraintSpec{
			{Field: "dept", Operator: FieldConstraintEqual, Value: "engineering"},
		},
	}
	if separateRules {
		mustAddRule(tb, workspace, RuleSpec{
			Name:          "active-person",
			ConditionTree: activeBranch,
			Actions:       []RuleActionSpec{{Name: "mark"}},
		})
		mustAddRule(tb, workspace, RuleSpec{
			Name:          "engineering-person",
			ConditionTree: deptBranch,
			Actions:       []RuleActionSpec{{Name: "mark"}},
		})
	} else {
		mustAddRule(tb, workspace, RuleSpec{
			Name: "or-person",
			ConditionTree: Or{Conditions: []ConditionSpec{
				activeBranch,
				deptBranch,
			}},
			Actions: []RuleActionSpec{{Name: "mark"}},
		})
	}
	return mustCompileWorkspace(tb, workspace)
}
