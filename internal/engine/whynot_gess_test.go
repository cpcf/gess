package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWhyNotSuppressesGeneratedGessBindings(t *testing.T) {
	ctx := context.Background()
	doc, err := ParseGess("implicit-bindings.gess", []byte(`
(deftemplate a (slot id (type STRING) (required TRUE)))
(deftemplate b (slot id (type STRING) (required TRUE)))
(deffacts seed (a (id "a-1")))
(defrule r
  (a (id ?id))
  (b (id ?id))
  =>
  (emit "noop"))
`))
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	report, err := session.WhyNot(ctx, "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	branch := singleBranch(t, report)
	for _, condition := range branch.Conditions {
		if condition.Binding != "" {
			t.Fatalf("implicit condition binding = %q, want suppressed", condition.Binding)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if strings.Contains(string(encoded), `"binding":"__gess`) {
		t.Fatalf("WhyNot JSON leaked generated binding: %s", encoded)
	}
}

func TestWhyNotPreservesAuthoredGeneratedLookingBinding(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-authored-generated-looking", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "__gess1", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b)},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a}
	})
	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"id": "a-1"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	branch := singleBranch(t, report)
	if branch.Conditions[0].Binding != "__gess1" {
		t.Fatalf("authored binding = %q, want preserved __gess1", branch.Conditions[0].Binding)
	}
}
