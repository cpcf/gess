package rules

import "context"

// GlobalValue builds an expression reading the named session global.
func GlobalValue(name string) GlobalExpr {
	return GlobalExpr{Name: name}
}

// Compile compiles workspace into an immutable Ruleset, equivalent to calling
// workspace.Compile directly.
func Compile(ctx context.Context, workspace *Workspace) (*Ruleset, error) {
	return workspace.Compile(ctx)
}
