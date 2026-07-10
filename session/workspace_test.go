package session_test

import (
	"context"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestNewWorkspaceBuildsEngineBackedWorkspace(t *testing.T) {
	workspace := session.NewWorkspace()
	if workspace == nil {
		t.Fatal("NewWorkspace returned nil")
	}
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: "item",
		Key:  rules.TemplateKey("item"),
		Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	ruleset, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, ok := ruleset.TemplateByKey("item"); !ok {
		t.Fatal("compiled ruleset does not contain item template")
	}
}
