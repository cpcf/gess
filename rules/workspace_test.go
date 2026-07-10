package rules_test

import (
	"errors"
	"testing"

	"github.com/cpcf/gess/rules"
)

func TestWorkspaceWithoutHandleFailsSafely(t *testing.T) {
	workspaces := []*rules.Workspace{
		{},
		rules.NewWorkspaceWithHandle(nil),
	}
	for _, workspace := range workspaces {
		if err := workspace.AddTemplate(rules.TemplateSpec{Name: "item"}); !errors.Is(err, rules.ErrInvalidRuleset) {
			t.Fatalf("AddTemplate error = %v, want ErrInvalidRuleset", err)
		}
	}

	var workspace *rules.Workspace
	if err := workspace.AddTemplate(rules.TemplateSpec{Name: "item"}); !errors.Is(err, rules.ErrInvalidRuleset) {
		t.Fatalf("nil Workspace AddTemplate error = %v, want ErrInvalidRuleset", err)
	}
}
