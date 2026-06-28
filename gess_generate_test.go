package gess

import (
	"context"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestGenerateGessGoEmitsBuildableSource(t *testing.T) {
	source := []byte(`
(deftemplate item
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (item (id "I-1")))

(defrule record-item
  ?item <- (item (id ?id))
  =>
  (call record ?item:id "generated"))

(defquery items
  ?item <- (item (id ?id))
  (return (id ?id)))
`)
	generated, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "items.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "rules",
		FunctionName: "BuildItems",
	})
	if err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "rules_generated.go", generated, parser.AllErrors); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, generated)
	}
	text := string(generated)
	for _, want := range []string{
		"package rules",
		"func BuildItems(ctx context.Context, registry gess.DSLRegistry)",
		"workspace.AddTemplate",
		"workspace.AddAction",
		"workspace.AddQuery",
		"gessGeneratedCallAction",
		"registry.Calls",
		"gess.MustFields",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source missing %q:\n%s", want, text)
		}
	}
}
