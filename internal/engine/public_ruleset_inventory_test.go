package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestPublicWorkspaceConstructionHasNoRegistrationSideEffect(t *testing.T) {
	engineFile, err := parser.ParseFile(token.NewFileSet(), "public_ruleset.go", nil, 0)
	if err != nil {
		t.Fatalf("parse public_ruleset.go: %v", err)
	}
	for _, declaration := range engineFile.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "init" {
			t.Fatal("public workspace construction must not use init registration")
		}
	}

	workspaceSource, err := os.ReadFile("../../rules/workspace.go")
	if err != nil {
		t.Fatalf("read rules/workspace.go: %v", err)
	}
	if strings.Contains(string(workspaceSource), "RegisterWorkspaceFactory") {
		t.Fatal("rules workspace retains a global factory registration seam")
	}
}
