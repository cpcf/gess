package engine

import (
	"context"
	"testing"
)

// A trailing test that rejects every input row must not be reported as a
// completed (fired) match: the filter node retains only passed rows, so the
// frontier scan has to look at the filter's input, not its output.
func TestWhyNotTrailingTestRejectsAllNeverFired(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-trailing-test", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "v", Kind: ValueInt, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Test{Expression: CompareExpr{
					Operator: ExpressionCompareGreaterThan,
					Left:     BindingFieldExpr{Binding: "a", Field: "v"},
					Right:    BindingFieldExpr{Binding: "a", Field: "v"},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a}
	})

	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"v": 5})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	if branch.FirstFailing < 0 {
		t.Fatalf("FirstFailing = %d, want a failing condition; conditions=%+v", branch.FirstFailing, branch.Conditions)
	}
	failing := branch.Conditions[branch.FirstFailing]
	if !failing.Test {
		t.Errorf("failing condition Test = false, want the trailing test; got %+v", failing)
	}
	if failing.Reason != WhyNotReasonPredicate {
		t.Errorf("failing reason = %q, want %q", failing.Reason, WhyNotReasonPredicate)
	}
}

// With two stacked standalone tests, the failing one must be blamed — not an
// earlier passing test or a matched condition. The reported conditions are in
// evaluation (planned) order.
func TestWhyNotStackedTestsBlamesFailingTest(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-stacked-tests", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "v", Kind: ValueInt, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Test{Expression: CompareExpr{Operator: ExpressionCompareGreaterThan, Left: BindingFieldExpr{Binding: "a", Field: "v"}, Right: ConstExpr{Value: int64(0)}}},   // passes
				Test{Expression: CompareExpr{Operator: ExpressionCompareGreaterThan, Left: BindingFieldExpr{Binding: "a", Field: "v"}, Right: ConstExpr{Value: int64(100)}}}, // fails
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a}
	})
	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"v": 5})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	// Conditions are in evaluation order: a (planned 0), test>0 (1), test>100 (2).
	for i, c := range branch.Conditions {
		if c.PlannedOrder != i {
			t.Fatalf("condition[%d] PlannedOrder = %d, want conditions in evaluation order", i, c.PlannedOrder)
		}
	}
	failing := branch.Conditions[branch.FirstFailing]
	if !failing.Test || failing.PlannedOrder != 2 {
		t.Fatalf("FirstFailing points at planned=%d test=%v, want the failing second test (planned 2)", failing.PlannedOrder, failing.Test)
	}
	if failing.Reason != WhyNotReasonPredicate {
		t.Errorf("failing reason = %q, want %q", failing.Reason, WhyNotReasonPredicate)
	}
	// The first (passing) test must be satisfied and not blamed.
	first := branch.Conditions[1]
	if !first.Test || !first.Satisfied || first.Reason != WhyNotReasonNone {
		t.Errorf("first test = %+v, want satisfied and unblamed", first)
	}
}

// A trailing test after a satisfied negation must be blamed for the failure,
// not the negation (which passed). The negation carries no condition id and
// occupies its own planned slot, so the frontier's planned position must count
// it.
func TestWhyNotTestAfterNegationBlamesTest(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-test-after-negation", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "v", Kind: ValueInt, Required: true}, {Name: "k", Kind: ValueString, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "k", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Not{Condition: Match{Binding: "b", Target: TemplateKeyFact(b), JoinConstraints: []JoinConstraintSpec{
					{Field: "k", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "k"}},
				}}},
				Test{Expression: CompareExpr{Operator: ExpressionCompareGreaterThan, Left: BindingFieldExpr{Binding: "a", Field: "v"}, Right: ConstExpr{Value: int64(100)}}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "b": b}
	})
	// a present, no b: the negation is satisfied; the trailing test 5>100 fails.
	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"v": 5, "k": "j"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if !failing.Test {
		t.Fatalf("FirstFailing = %+v, want the trailing test blamed, not the negation", failing)
	}
	if failing.Reason != WhyNotReasonPredicate {
		t.Errorf("failing reason = %q, want %q", failing.Reason, WhyNotReasonPredicate)
	}
	// The satisfied negation must not be blamed.
	for _, cond := range branch.Conditions {
		if cond.Negated && cond.Reason != WhyNotReasonNone {
			t.Errorf("negation wrongly blamed with reason %q: %+v", cond.Reason, cond)
		}
	}
}

// A trailing test after a matching join must be blamed for the failure — the
// join itself matched — and the test condition must be surfaced.
func TestWhyNotTrailingTestAfterJoinBlamesTest(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-trailing-test-join", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "k", Kind: ValueString, Required: true}, {Name: "v", Kind: ValueInt, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "k", Kind: ValueString, Required: true}, {Name: "v", Kind: ValueInt, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b), JoinConstraints: []JoinConstraintSpec{
					{Field: "k", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "k"}},
				}},
				Test{Expression: CompareExpr{
					Operator: ExpressionCompareGreaterThan,
					Left:     BindingFieldExpr{Binding: "b", Field: "v"},
					Right:    BindingFieldExpr{Binding: "a", Field: "v"},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "b": b}
	})
	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"k": "j", "v": 5})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	if _, err := session.Assert(context.Background(), keys["b"], mustFields(t, map[string]any{"k": "j", "v": 5})); err != nil {
		t.Fatalf("Assert(b): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	if branch.FirstFailing < 0 {
		t.Fatalf("FirstFailing = %d, want the trailing test; conditions=%+v", branch.FirstFailing, branch.Conditions)
	}
	failing := branch.Conditions[branch.FirstFailing]
	if !failing.Test {
		t.Errorf("failing condition Test = false, want the trailing test blamed, not the join; got %+v", failing)
	}
	if failing.Reason != WhyNotReasonPredicate {
		t.Errorf("failing reason = %q, want %q", failing.Reason, WhyNotReasonPredicate)
	}
	// The join conditions a and b did match, so they must be satisfied. The
	// WhyNotCondition.Binding is the un-prefixed authored binding name.
	checked := 0
	for _, cond := range branch.Conditions {
		if cond.Binding == "a" || cond.Binding == "b" {
			checked++
			if !cond.Satisfied {
				t.Errorf("condition %q Satisfied = false, want true (join matched); got %+v", cond.Binding, cond)
			}
		}
	}
	if checked != 2 {
		t.Fatalf("expected to check bindings a and b, but matched %d conditions: %+v", checked, branch.Conditions)
	}
}
