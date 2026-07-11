package engine

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

var benchmarkMannersRunResult RunResult

func BenchmarkGessMannersSessionRun(b *testing.B) {
	ctx := context.Background()
	revision := mustCompileMannersRuleset(b)

	for _, guestCount := range []int{16, 32, 64, 128} {
		guests := mannersGuests(guestCount)
		initials := mannersInitialFacts(guests)
		b.Run(fmt.Sprintf("guests=%d", guestCount), func(b *testing.B) {
			b.ReportAllocs()
			var lifecycle propagationCounterSnapshot
			if guestCount == 64 {
				_, lifecycle = collectMannersLifecycleCounters(b, revision, initials, guests)
			}
			b.ResetTimer()
			b.StopTimer()

			var result RunResult
			for i := 0; i < b.N; i++ {
				session, err := NewSession(revision, WithInitialFacts(initials...))
				if err != nil {
					b.Fatalf("NewSession: %v", err)
				}

				b.StartTimer()
				result, err = session.Run(ctx)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				if result.Status != RunCompleted {
					b.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
				}
				validateMannersSolution(b, ctx, session, guests)
				benchmarkMannersRunResult = result
			}

			b.ReportMetric(float64(result.Fired), "fired/run")
			b.ReportMetric(float64(guestCount), "guests")
			if guestCount == 64 {
				reportMannersLifecycleMetrics(b, lifecycle.Totals)
			}
		})
	}
}

func TestManners64LifecycleCountersDeterministic(t *testing.T) {
	guests := mannersGuests(64)
	revision := mustCompileMannersRuleset(t)
	initials := mannersInitialFacts(guests)

	firstResult, first := collectMannersLifecycleCounters(t, revision, initials, guests)
	secondResult, second := collectMannersLifecycleCounters(t, revision, initials, guests)
	if firstResult.Fired != 2206 || secondResult.Fired != 2206 {
		t.Fatalf("fired = (%d, %d), want (2206, 2206)", firstResult.Fired, secondResult.Fired)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("lifecycle snapshots differ:\nfirst:  %+v\nsecond: %+v", first.Totals, second.Totals)
	}
	totals := first.Totals
	if got, want := totals.ModifyRawTerminalAdds, totals.ModifyKeptTerminalAdds+totals.ModifyCoalescedPairs; got != want {
		t.Fatalf("raw terminal adds = %d, want kept adds + coalesced pairs = %d", got, want)
	}
	if got, want := totals.ModifyRawTerminalRemoves, totals.ModifyKeptTerminalRemoves+totals.ModifyCoalescedPairs; got != want {
		t.Fatalf("raw terminal removes = %d, want kept removes + coalesced pairs = %d", got, want)
	}
	if got, want := totals.ModifyCoalescedPairs, totals.ModifyDistinctTokenUpdates+totals.ModifySameTokenCancellations; got != want {
		t.Fatalf("coalesced pairs = %d, want updates + cancellations = %d", got, want)
	}
	if totals.NegativeBlockerZeroToOne > totals.NegativeBlockerIncrements {
		t.Fatalf("negative blocker 0->1 transitions = %d, increments = %d", totals.NegativeBlockerZeroToOne, totals.NegativeBlockerIncrements)
	}
	if totals.NegativeBlockerOneToZero > totals.NegativeBlockerDecrements {
		t.Fatalf("negative blocker 1->0 transitions = %d, decrements = %d", totals.NegativeBlockerOneToZero, totals.NegativeBlockerDecrements)
	}
	if totals.FullAgendaReconciles != 0 || totals.WholeTerminalScans != 0 || totals.OracleStyleMatchRequests != 0 || totals.UnsupportedAgendaDeltas != 0 {
		t.Fatalf("non-graph lifecycle counters = %+v", totals)
	}
}

func collectMannersLifecycleCounters(t testing.TB, revision *Ruleset, initials []SessionInitialFact, guests []mannersGuest) (RunResult, propagationCounterSnapshot) {
	t.Helper()
	ctx := context.Background()
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	snapshot := session.propagationCounterSnapshot()
	validateMannersSolution(t, ctx, session, guests)
	return result, snapshot
}

func reportMannersLifecycleMetrics(b *testing.B, totals propagationCounterTotals) {
	b.Helper()
	b.ReportMetric(float64(totals.ModifyCascades), "modify-cascades/run")
	b.ReportMetric(float64(totals.ModifyRawTerminalAdds), "raw-terminal-adds/run")
	b.ReportMetric(float64(totals.ModifyRawTerminalRemoves), "raw-terminal-removes/run")
	b.ReportMetric(float64(totals.ModifyKeptTerminalAdds), "kept-terminal-adds/run")
	b.ReportMetric(float64(totals.ModifyKeptTerminalRemoves), "kept-terminal-removes/run")
	b.ReportMetric(float64(totals.ModifyCoalescedPairs), "coalesced-pairs/run")
	b.ReportMetric(float64(totals.ModifyDistinctTokenUpdates), "distinct-token-updates/run")
	b.ReportMetric(float64(totals.ModifySameTokenCancellations), "same-token-cancellations/run")
	b.ReportMetric(float64(totals.CoalescerIdentityIndexProbes), "identity-index-probes/run")
	b.ReportMetric(float64(totals.CoalescerIdentityIndexCandidates), "identity-index-candidates/run")
	b.ReportMetric(float64(totals.TokenRowsAllocated), "token-rows-allocated/run")
	b.ReportMetric(float64(totals.BetaRowsRemoved), "beta-rows-removed/run")
	b.ReportMetric(float64(totals.NegativeBetaRowsRemoved), "negative-beta-rows-removed/run")
	b.ReportMetric(float64(totals.NegativeBlockerIncrements), "blocker-increments/run")
	b.ReportMetric(float64(totals.NegativeBlockerDecrements), "blocker-decrements/run")
	b.ReportMetric(float64(totals.NegativeBlockerZeroToOne), "blocker-zero-to-one/run")
	b.ReportMetric(float64(totals.NegativeBlockerOneToZero), "blocker-one-to-zero/run")
}
