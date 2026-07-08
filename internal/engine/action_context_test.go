package engine

import (
	"testing"

	gessrules "github.com/cpcf/gess/rules"
)

func actionContextForTest(t testing.TB, ctx ActionContext) *actionContext {
	t.Helper()
	internalCtx, ok := unwrapActionContext(ctx)
	if !ok {
		t.Fatalf("ActionContext handle = %T, want engine actionContextHandle", gessrules.ActionContextHandleOf(ctx))
	}
	return internalCtx
}
