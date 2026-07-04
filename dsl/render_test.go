package dsl_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	dsl "github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/internal/gesssexp"
	rules "github.com/cpcf/gess/rules"
)

func TestRenderRulesetRoundTripsReferenceCorpora(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		registry dsl.Registry
	}{
		{
			name: "order routing",
			path: "../examples/gess-files/order_routing/rules.gess",
		},
		{
			name: "tutorial solution",
			path: "../tutorial/vulnerability_response/solution/rules.gess",
			registry: dsl.Registry{Calls: map[string]dsl.CallFunc{
				"record-emergency": func(rules.ActionContext, []rules.Value) error { return nil },
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			original, err := dsl.Compile(context.Background(), tt.path, source, tt.registry)
			if err != nil {
				t.Fatalf("compile original: %v", err)
			}
			rendered, err := dsl.RenderRuleset(original)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			formatted, err := gesssexp.Format("<rendered>", rendered)
			if err != nil {
				t.Fatalf("format rendered: %v\n%s", err, rendered)
			}
			if !bytes.Equal(rendered, formatted) {
				t.Fatalf("render is not gessfmt-stable:\n%s", rendered)
			}
			roundTrip, err := dsl.Compile(context.Background(), "<rendered>", rendered, tt.registry)
			if err != nil {
				t.Fatalf("compile rendered: %v\n%s", err, rendered)
			}
			assertSameConstructNames(t, roundTrip, original)
			renderedAgain, err := dsl.RenderRuleset(roundTrip)
			if err != nil {
				t.Fatalf("render round trip: %v", err)
			}
			if !bytes.Equal(rendered, renderedAgain) {
				t.Fatalf("render is not deterministic after round trip\nfirst:\n%s\nsecond:\n%s", rendered, renderedAgain)
			}
		})
	}
}

func assertSameConstructNames(t *testing.T, got, want *rules.Ruleset) {
	t.Helper()
	if strings.Join(templateNames(got), ",") != strings.Join(templateNames(want), ",") {
		t.Fatalf("template names = %v, want %v", templateNames(got), templateNames(want))
	}
	if strings.Join(ruleNames(got), ",") != strings.Join(ruleNames(want), ",") {
		t.Fatalf("rule names = %v, want %v", ruleNames(got), ruleNames(want))
	}
	if strings.Join(queryNames(got), ",") != strings.Join(queryNames(want), ",") {
		t.Fatalf("query names = %v, want %v", queryNames(got), queryNames(want))
	}
}

func templateNames(r *rules.Ruleset) []string {
	values := r.Templates()
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.Name()
	}
	sort.Strings(out)
	return out
}

func ruleNames(r *rules.Ruleset) []string {
	values := r.Rules()
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.Name()
	}
	sort.Strings(out)
	return out
}

func queryNames(r *rules.Ruleset) []string {
	values := r.Queries()
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.Name()
	}
	sort.Strings(out)
	return out
}

func TestRenderIndividualConstructs(t *testing.T) {
	source := []byte(`(defmodule OPS (declare (auto-focus TRUE)))

(defglobal *threshold* (type INTEGER) (default 10) (description "limit"))

(deffunction high-enough
  (param ?score INTEGER)
  (return BOOLEAN)
  (> ?score 10)
)

(deftemplate OPS::item
  (declare (duplicate-policy unique-key) (duplicate-key id) (backchain-reactive TRUE))
  (slot id (type STRING) (required TRUE))
  (slot score (type INTEGER) (required TRUE))
)

(defrule OPS::route
  (declare (salience 5) (auto-focus TRUE))
  ?item <- (OPS::item (id "A") (score ?score))
  (test (high-enough ?score))
  =>
  (assert (OPS::item (id "B") (score ?score)))
)

(defquery OPS::items
  (declare (variables ?id))
  ?item <- (OPS::item (id ?id) (score ?score))
  (return (score ?score))
)
`)
	compiled, err := dsl.Compile(context.Background(), "constructs.gess", source, dsl.Registry{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	checks := []struct {
		name string
		fn   func() ([]byte, error)
		want string
	}{
		{"module", func() ([]byte, error) { return dsl.RenderModule(compiled, rules.ModuleName("OPS")) }, "(defmodule OPS"},
		{"template", func() ([]byte, error) { return dsl.RenderTemplate(compiled, "item") }, "(deftemplate OPS::item"},
		{"rule", func() ([]byte, error) { return dsl.RenderRule(compiled, "route") }, "(defrule OPS::route"},
		{"query", func() ([]byte, error) { return dsl.RenderQuery(compiled, "items") }, "(defquery OPS::items"},
		{"function", func() ([]byte, error) { return dsl.RenderFunction(compiled, "high-enough") }, "(deffunction high-enough"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			rendered, err := check.fn()
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(string(rendered), check.want) {
				t.Fatalf("rendered construct missing %q:\n%s", check.want, rendered)
			}
			formatted, err := gesssexp.Format("<fragment>", rendered)
			if err != nil {
				t.Fatalf("format: %v\n%s", err, rendered)
			}
			if !bytes.Equal(rendered, formatted) {
				t.Fatalf("fragment not stable:\n%s", rendered)
			}
		})
	}
}

func TestRenderGoAuthoredRulesetUsesRegistrations(t *testing.T) {
	workspace := rules.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: "item",
		Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "score", Kind: rules.ValueInt, Required: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddFunction(rules.PureFunctionSpec{
		Name:   "host-ok",
		Return: rules.ValueBool,
		Args:   []rules.ValueKind{rules.ValueInt},
		Func1: func(context.Context, rules.Value) (rules.Value, error) {
			return rules.NewValue(true)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddAction(rules.ActionSpec{Name: "notify", Fn: func(rules.ActionContext) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name: "go-authored",
		ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
			rules.Match{Binding: "item", Target: rules.TemplateFact("item")},
			rules.Test{Expression: rules.Call("host-ok", rules.BindingFieldExpr{Binding: "item", Field: "score"})},
		}},
		Actions: []rules.RuleActionSpec{{Name: "notify"}},
	}); err != nil {
		t.Fatal(err)
	}
	compiled, err := rules.Compile(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := dsl.RenderRuleset(compiled)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(rendered), "(call notify)") {
		t.Fatalf("go action was not rendered as call:\n%s", rendered)
	}
	registry := dsl.Registry{
		Actions: map[string]rules.ActionFunc{"notify": func(rules.ActionContext) error { return nil }},
		Functions: []rules.PureFunctionSpec{{
			Name:   "host-ok",
			Return: rules.ValueBool,
			Args:   []rules.ValueKind{rules.ValueInt},
			Func1: func(context.Context, rules.Value) (rules.Value, error) {
				return rules.NewValue(true)
			},
		}},
	}
	if _, err := dsl.Compile(context.Background(), "<rendered>", rendered, registry); err != nil {
		t.Fatalf("compile rendered: %v\n%s", err, rendered)
	}
	comment, err := dsl.RenderFunction(compiled, "host-ok")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(comment); got != "; go-function: host-ok\n" {
		t.Fatalf("go function render = %q", got)
	}
}

func TestRenderRulesetIsDeterministic(t *testing.T) {
	path := filepath.Join("..", "examples", "gess-files", "order_routing", "rules.gess")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := dsl.Compile(context.Background(), path, source, dsl.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := dsl.RenderRuleset(compiled)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dsl.RenderRuleset(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("render not deterministic\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
