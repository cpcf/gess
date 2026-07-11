package engine

import (
	"context"
	"testing"
)

const forkWhatIfCorpusClaims = 64

var benchmarkWhatIfReport WhatIfReport

func BenchmarkSessionForkCorpusScale(b *testing.B) {
	ctx := context.Background()
	parent := benchmarkClaimsTriageCorpusSession(b, ctx, forkWhatIfCorpusClaims)
	wantFacts := parent.factCount()

	b.ReportAllocs()
	b.ReportMetric(float64(wantFacts), "facts")
	b.ResetTimer()
	for b.Loop() {
		fork, err := parent.Fork(ctx, WithSessionID("fork-corpus-benchmark-child"))
		if err != nil {
			b.Fatalf("Fork: %v", err)
		}
		if got := fork.factCount(); got != wantFacts {
			_ = fork.Close()
			b.Fatalf("fork facts = %d, want %d", got, wantFacts)
		}
		if err := fork.Close(); err != nil {
			b.Fatalf("Close fork: %v", err)
		}
	}
}

func BenchmarkSessionWhatIfCorpusScale(b *testing.B) {
	ctx := context.Background()
	parent := benchmarkClaimsTriageCorpusSession(b, ctx, forkWhatIfCorpusClaims)
	baseFacts := parent.factCount()
	allFacts := claimsTriageInitialFacts(b, forkWhatIfCorpusClaims+8)
	scenarioFacts := allFacts[forkWhatIfCorpusClaims*4:]
	expectedFired := claimsTriageFiredCount(forkWhatIfCorpusClaims+8) - claimsTriageFiredCount(forkWhatIfCorpusClaims)
	expectedAdded := len(scenarioFacts) + expectedFired

	b.ReportAllocs()
	b.ReportMetric(float64(baseFacts), "base-facts")
	b.ReportMetric(float64(len(scenarioFacts)), "scenario-facts")
	b.ResetTimer()
	for b.Loop() {
		report, err := parent.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
			for _, fact := range scenarioFacts {
				if _, err := fork.Assert(ctx, fact.TemplateKey, fact.Fields); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatalf("WhatIf: %v", err)
		}
		if report.Run.Status != RunCompleted || report.Run.Fired != expectedFired {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", report.Run.Status, report.Run.Fired, RunCompleted, expectedFired)
		}
		if got := report.Base.Len(); got != baseFacts {
			b.Fatalf("base facts = %d, want %d", got, baseFacts)
		}
		if len(report.Diff.Added) != expectedAdded || len(report.Diff.Retracted) != 0 || len(report.Diff.Modified) != 0 {
			b.Fatalf("diff counts = added %d, retracted %d, modified %d; want %d, 0, 0", len(report.Diff.Added), len(report.Diff.Retracted), len(report.Diff.Modified), expectedAdded)
		}
		if report.ForkSession != nil {
			_ = report.ForkSession.Close()
			b.Fatal("WhatIf retained a fork without WithWhatIfRetainFork")
		}
		benchmarkWhatIfReport = report
	}
}

func benchmarkClaimsTriageCorpusSession(tb testing.TB, ctx context.Context, claims int) *Session {
	tb.Helper()
	revision := mustCompileClaimsTriageRuleset(tb, nil)
	session, err := NewSession(revision,
		WithSessionID("fork-whatif-corpus-parent"),
		WithInitialFacts(claimsTriageInitialFacts(tb, claims)...),
	)
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	tb.Cleanup(func() { _ = session.Close() })
	result, err := session.Run(ctx)
	if err != nil {
		tb.Fatalf("Run parent: %v", err)
	}
	if want := claimsTriageFiredCount(claims); result.Status != RunCompleted || result.Fired != want {
		tb.Fatalf("parent run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, want)
	}
	return session
}
