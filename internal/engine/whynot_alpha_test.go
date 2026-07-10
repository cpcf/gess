package engine

import (
	"context"
	"testing"
)

func TestWhyNotUsesAlphaMemoryWhenCountIndexIsUnavailable(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-alpha-memory", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b)},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		mustAddRule(t, w, RuleSpec{
			Name:          "single",
			ConditionTree: Match{Binding: "a", Target: TemplateKeyFact(a)},
			Actions:       []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a}
	})
	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"id": "a-1"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}

	inspection := session.branchInspectionsForRule(session.revision.rules["r"].revisionID)[0]
	conditionID := inspection.AuthoredOrder[0].ConditionID
	if got := session.propagation.runtime.graphBeta.alphaFactCount(conditionID); got != 1 {
		t.Fatalf("constraint-free alpha count characterization = %d, want 1", got)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	delete(session.propagation.runtime.graphBeta.alpha.factCounts, conditionID)
	delete(session.propagation.runtime.graphBeta.alpha.factCounts, session.branchInspectionsForRule(session.revision.rules["single"].revisionID)[0].AuthoredOrder[0].ConditionID)

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Binding != "b" {
		t.Fatalf("first failing binding = %q, want b after reading a from alpha memory", failing.Binding)
	}
	for _, condition := range branch.Conditions {
		if condition.Binding == "a" && (!condition.Satisfied || condition.AlphaMatches != 1) {
			t.Fatalf("condition a = %+v, want satisfied with one alpha-memory match", condition)
		}
	}
	fired, err := session.WhyNot(context.Background(), "single")
	if err != nil {
		t.Fatalf("WhyNot(single): %v", err)
	}
	if fired.Outcome != WhyNotAlreadyFired || len(fired.Branches) != 1 ||
		len(fired.Branches[0].Conditions) != 1 || fired.Branches[0].Conditions[0].AlphaMatches != 1 {
		t.Fatalf("single-condition report = %+v, want already_fired from alpha memory with one match", fired)
	}
}

func TestWhyNotAlphaMemoryCharacterization(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-alpha-characterization", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", DuplicatePolicy: DuplicateAllow, Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		add := func(name string, first Match) {
			mustAddRule(t, w, RuleSpec{
				Name:          name,
				ConditionTree: And{Conditions: []ConditionSpec{first, Match{Binding: "b", Target: TemplateKeyFact(b)}}},
				Actions:       []RuleActionSpec{{Name: "noop"}},
			})
		}
		add("bare", Match{Binding: "a", Target: TemplateKeyFact(a)})
		add("shared", Match{Binding: "a", Target: TemplateKeyFact(a)})
		add("constrained", Match{Binding: "a", Target: TemplateKeyFact(a), FieldConstraints: []FieldConstraintSpec{
			{Field: "status", Operator: FieldConstraintEqual, Value: "new"},
		}})
		return map[string]TemplateKey{"a": a}
	})
	for _, fields := range []map[string]any{
		{"id": "a-1", "status": "new"},
		{"id": "a-2", "status": "old"},
	} {
		if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, fields)); err != nil {
			t.Fatalf("Assert(a): %v", err)
		}
	}

	for ruleName, want := range map[string]int{"bare": 2, "shared": 2, "constrained": 1} {
		rule := session.revision.rules[ruleName]
		inspection := session.branchInspectionsForRule(rule.revisionID)[0]
		conditionID := inspection.AuthoredOrder[0].ConditionID
		if got := session.propagation.runtime.graphBeta.alphaFactCount(conditionID); got != want {
			t.Errorf("%s auxiliary alpha count = %d, want %d", ruleName, got, want)
		}
		report, err := session.WhyNot(context.Background(), ruleName)
		if err != nil {
			t.Fatalf("WhyNot(%s): %v", ruleName, err)
		}
		branch := singleBranch(t, report)
		var got int
		for _, condition := range branch.Conditions {
			if condition.Binding == "a" {
				got = condition.AlphaMatches
			}
		}
		if got != want {
			t.Errorf("%s public alpha matches = %d, want %d", ruleName, got, want)
		}
	}
}
