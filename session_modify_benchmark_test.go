package gess

import (
	"context"
	"testing"
)

func BenchmarkSessionModifyUnroutedDynamicFact(b *testing.B) {
	ctx := context.Background()
	session := mustSession(b, mustCompileUnroutedModifyBenchmarkRuleset(b), "modify-unrouted-dynamic-benchmark-session")
	inserted, err := session.Assert(ctx, "person", mustFields(b, map[string]any{
		"name":  "Ada",
		"count": 1,
	}))
	if err != nil {
		b.Fatalf("Assert: %v", err)
	}
	patches := []FactPatch{
		{Set: mustFields(b, map[string]any{"count": 2})},
		{Set: mustFields(b, map[string]any{"count": 1})},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := session.Modify(ctx, inserted.Fact.ID(), patches[i%len(patches)])
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = ModifyResult{Status: result.Status, Fact: result.Fact}
	}
}

func mustCompileUnroutedModifyBenchmarkRuleset(tb testing.TB) *Ruleset {
	tb.Helper()
	workspace := NewWorkspace()
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		tb.Fatalf("Compile: %v", err)
	}
	return revision
}
