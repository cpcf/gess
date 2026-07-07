package engine

import (
	"context"
	"errors"
	"testing"
)

func whatIfBaseSession(t testing.TB) (*Session, TemplateKey) {
	t.Helper()
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session := mustSession(t, revision, "whatif-base")
	if _, err := session.Assert(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("Assert(s-1): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return session, sourceKey
}

func TestWhatIfAssertScenario(t *testing.T) {
	session, sourceKey := whatIfBaseSession(t)
	ctx := context.Background()
	baseBefore := mustSnapshot(t, ctx, session)

	report, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, err := fork.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-2"}))
		return err
	}, WithWhatIfExplain())
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}

	if len(report.Firings) != 2 {
		t.Fatalf("firings = %d, want 2 (derive + derive-child for s-2)", len(report.Firings))
	}
	// s-2 plus its derived and child logical facts are added.
	if len(report.Diff.Added) != 3 {
		t.Fatalf("added = %d, want 3 (source + derived + child)", len(report.Diff.Added))
	}
	if len(report.Diff.Retracted) != 0 {
		t.Fatalf("retracted = %d, want 0", len(report.Diff.Retracted))
	}
	if len(report.Derivations) != 3 {
		t.Fatalf("derivations = %d, want one per added fact", len(report.Derivations))
	}

	// Base is untouched.
	baseAfter := mustSnapshot(t, ctx, session)
	if baseAfter.Len() != baseBefore.Len() {
		t.Fatalf("base changed: len %d -> %d", baseBefore.Len(), baseAfter.Len())
	}
	if got := mustSnapshot(t, ctx, session).FactsByName("source"); len(got) != 1 {
		t.Fatalf("base has %d sources, want 1 (fork must not leak)", len(got))
	}
}

func TestWhatIfRetractScenario(t *testing.T) {
	session, _ := whatIfBaseSession(t)
	ctx := context.Background()
	base := mustSnapshot(t, ctx, session)
	sourceID := singleFactByField(t, base, "source", "id", "s-1")

	report, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, err := fork.Retract(ctx, sourceID)
		return err
	})
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if len(report.Diff.Retracted) < 3 {
		t.Fatalf("retracted = %d, want at least 3 (source + logical cascade)", len(report.Diff.Retracted))
	}
	// Base still has the whole chain.
	if got := mustSnapshot(t, ctx, session).FactsByName("child"); len(got) != 1 {
		t.Fatalf("base child count = %d, want 1 (base untouched)", len(got))
	}
}

// With WithWhatIfExplain the report promises a derivation for every added
// fact, so a fact that cannot be explained from the reused snapshot must abort
// rather than yield a report with derivations silently missing.
func TestWhatIfDerivationsAbortsOnMissingFact(t *testing.T) {
	session, sourceKey := whatIfBaseSession(t)
	fork, err := session.Fork(context.Background(), WithExplainLog())
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer func() { _ = fork.Close() }()
	// Snapshot before asserting, so the fact is absent from this snapshot.
	snapshot, err := fork.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	res, err := fork.Assert(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-err"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	if _, err := whatIfDerivations(fork, snapshot, []FactSnapshot{res.Fact}); err == nil {
		t.Fatalf("whatIfDerivations for a fact absent from the snapshot = nil error, want an abort")
	} else if !errors.Is(err, ErrFactNotFound) {
		t.Fatalf("error = %v, want ErrFactNotFound wrapped", err)
	}
}

func TestWhatIfFireLimit(t *testing.T) {
	session, sourceKey := whatIfBaseSession(t)
	ctx := context.Background()
	report, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, err := fork.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-2"}))
		return err
	}, WithWhatIfMaxFirings(1))
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if report.Run.Status != RunFireLimit {
		t.Fatalf("run status = %q, want %q", report.Run.Status, RunFireLimit)
	}
	if report.Run.Fired != 1 {
		t.Fatalf("fired = %d, want 1", report.Run.Fired)
	}
}

func TestWhatIfScenarioErrorLeavesBaseIntact(t *testing.T) {
	session, sourceKey := whatIfBaseSession(t)
	ctx := context.Background()
	baseBefore := mustSnapshot(t, ctx, session)

	sentinel := errors.New("scenario boom")
	_, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, _ = fork.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-9"}))
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WhatIf err = %v, want wrapped sentinel", err)
	}
	baseAfter := mustSnapshot(t, ctx, session)
	if baseAfter.Len() != baseBefore.Len() {
		t.Fatalf("base changed after scenario error: %d -> %d", baseBefore.Len(), baseAfter.Len())
	}
}

// BenchmarkSessionWhatIf measures an end-to-end what-if (fork + scenario +
// bounded run + report) alongside the fork-vs-rebuild cost benchmarks.
func BenchmarkSessionWhatIf(b *testing.B) {
	session, sourceKey := whatIfBaseSession(b)
	ctx := context.Background()
	fields := mustFields(b, map[string]any{"id": "s-x"})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
			_, err := fork.Assert(ctx, sourceKey, fields)
			return err
		}); err != nil {
			b.Fatalf("WhatIf: %v", err)
		}
	}
}

func TestWhatIfRetainFork(t *testing.T) {
	session, sourceKey := whatIfBaseSession(t)
	ctx := context.Background()
	report, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, err := fork.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-2"}))
		return err
	}, WithWhatIfRetainFork())
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if report.ForkSession == nil {
		t.Fatalf("ForkSession nil, want retained fork")
	}
	defer report.ForkSession.Close()
	// The retained fork is usable for follow-up.
	if _, err := report.ForkSession.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-3"})); err != nil {
		t.Fatalf("follow-up assert on retained fork: %v", err)
	}
	if _, err := report.ForkSession.Run(ctx); err != nil {
		t.Fatalf("follow-up run on retained fork: %v", err)
	}
}
