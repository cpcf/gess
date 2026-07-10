package engine

import (
	"context"
	"testing"
)

func TestWhyNotDegradesWhenFrontierMappingIsInconsistent(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-mapping-mismatch", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "v", Kind: ValueInt, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Test{Expression: CompareExpr{
					Operator: ExpressionCompareGreaterThan,
					Left:     BindingFieldExpr{Binding: "a", Field: "v"},
					Right:    ConstExpr{Value: int64(100)},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a}
	})
	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"v": 5})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}

	found := false
	for i := range session.rete.graph.branchInspections {
		inspection := &session.rete.graph.branchInspections[i]
		if inspection.RuleName != "r" {
			continue
		}
		if len(inspection.PlannedOrder) != 2 || !inspection.PlannedOrder[1].Test {
			t.Fatalf("unexpected planned conditions: %+v", inspection.PlannedOrder)
		}
		inspection.PlannedOrder[1].Test = false
		found = true
		break
	}
	if !found {
		t.Fatal("rule branch inspection not found")
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if !report.Truncated {
		t.Fatalf("Truncated = false, want explicit degradation for inconsistent frontier mapping: %+v", report)
	}
	if len(report.Branches) != 1 {
		t.Fatalf("branches = %d, want 1", len(report.Branches))
	}
	branch := report.Branches[0]
	if branch.FirstFailing != -1 {
		t.Fatalf("FirstFailing = %d, want no condition attribution for degraded mapping", branch.FirstFailing)
	}
	for _, condition := range branch.Conditions {
		if condition.Satisfied || condition.Reason != WhyNotReasonNone {
			t.Fatalf("condition was attributed despite degraded mapping: %+v", condition)
		}
	}
}
