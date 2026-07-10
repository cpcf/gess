package engine

import (
	"context"

	gessrules "github.com/cpcf/gess/rules"
)

type workspaceHandle struct {
	*Workspace
}

// PublicWorkspace returns a public facade for a new engine-owned workspace.
func PublicWorkspace() *gessrules.Workspace {
	return gessrules.NewWorkspaceWithHandle(workspaceHandle{Workspace: NewWorkspace()})
}

func (h workspaceHandle) Compile(ctx context.Context) (*gessrules.Ruleset, error) {
	if h.Workspace == nil {
		return nil, ErrInvalidRuleset
	}
	ruleset, err := h.Workspace.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return PublicRuleset(ruleset), nil
}

type rulesetHandle struct {
	*Ruleset
}

// PublicRuleset returns the public facade for an engine-owned compiled ruleset.
func PublicRuleset(ruleset *Ruleset) *gessrules.Ruleset {
	if ruleset == nil {
		return nil
	}
	return gessrules.NewRuleset(rulesetHandle{Ruleset: ruleset})
}

// RulesetFromPublic returns the engine-owned compiled ruleset behind revision.
func RulesetFromPublic(revision *gessrules.Ruleset) (*Ruleset, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}
	switch handle := gessrules.RulesetHandleOf(revision).(type) {
	case rulesetHandle:
		if handle.Ruleset == nil {
			return nil, ErrInvalidRuleset
		}
		return handle.Ruleset, nil
	case *rulesetHandle:
		if handle == nil || handle.Ruleset == nil {
			return nil, ErrInvalidRuleset
		}
		return handle.Ruleset, nil
	default:
		return nil, ErrInvalidRuleset
	}
}

// WorkspaceFromPublic returns the engine-owned workspace behind workspace.
func WorkspaceFromPublic(workspace *gessrules.Workspace) (*Workspace, error) {
	if workspace == nil {
		return nil, ErrInvalidRuleset
	}
	switch handle := gessrules.WorkspaceHandleOf(workspace).(type) {
	case workspaceHandle:
		if handle.Workspace == nil {
			return nil, ErrInvalidRuleset
		}
		return handle.Workspace, nil
	case *workspaceHandle:
		if handle == nil || handle.Workspace == nil {
			return nil, ErrInvalidRuleset
		}
		return handle.Workspace, nil
	default:
		return nil, ErrInvalidRuleset
	}
}
