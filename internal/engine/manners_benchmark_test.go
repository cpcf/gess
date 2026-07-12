package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
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
				reportMannersLifecycleMetrics(b, lifecycle)
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
	stageTotal := 0
	for _, stage := range first.TokenRowsByStage {
		if stage.Stage.kind == reteGraphStageUnknown {
			t.Fatalf("token rows attributed to unknown stage: %d", stage.Count)
		}
		stageTotal += stage.Count
	}
	if got, want := totals.TokenRowsAllocated, 331188; got != want {
		t.Fatalf("token rows allocated = %d, want %d", got, want)
	}
	if stageTotal != totals.TokenRowsAllocated {
		t.Fatalf("token rows by stage = %d, want total allocated = %d", stageTotal, totals.TokenRowsAllocated)
	}
	wantTopStages := []propagationStageCount{
		{Stage: reteGraphStageRef{kind: reteGraphStageBeta, id: 5}, Count: 145152},
		{Stage: reteGraphStageRef{kind: reteGraphStageBeta, id: 9}, Count: 82271},
		{Stage: reteGraphStageRef{kind: reteGraphStageBeta, id: 7}, Count: 43120},
		{Stage: reteGraphStageRef{kind: reteGraphStageBeta, id: 8}, Count: 41167},
		{Stage: reteGraphStageRef{kind: reteGraphStageAlpha, id: 6}, Count: 6240},
	}
	if got := topPropagationStageCounts(first.TokenRowsByStage, len(wantTopStages)); !reflect.DeepEqual(got, wantTopStages) {
		t.Fatalf("top token allocation stages = %v, want %v", got, wantTopStages)
	}
	t.Logf("top token allocation stages: %v", topPropagationStageCounts(first.TokenRowsByStage, 8))
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
	if totals.BetaIdentityIndexInserts < totals.BetaIdentityIndexBuilds {
		t.Fatalf("beta identity-index inserts = %d, builds = %d", totals.BetaIdentityIndexInserts, totals.BetaIdentityIndexBuilds)
	}
	betaIdentityLifecycle := []struct {
		name string
		got  int
		want int
	}{
		{name: "holder hits", got: totals.BetaHolderHits, want: 114379},
		{name: "multi-holder demotions", got: totals.BetaMultiHolderDemotions, want: 0},
		{name: "index builds", got: totals.BetaIdentityIndexBuilds, want: 3},
		{name: "index inserts", got: totals.BetaIdentityIndexInserts, want: 64702},
		{name: "index probes", got: totals.BetaIdentityIndexProbes, want: 80766},
		{name: "index candidates", got: totals.BetaIdentityIndexCandidates, want: 126},
		{name: "scan fallbacks", got: totals.BetaIdentityScanFallbacks, want: 0},
		{name: "scan candidates", got: totals.BetaIdentityScanCandidates, want: 0},
	}
	for _, counts := range betaIdentityLifecycle {
		if counts.got != counts.want {
			t.Fatalf("beta identity lifecycle %s = %d, want %d", counts.name, counts.got, counts.want)
		}
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

func reportMannersLifecycleMetrics(b *testing.B, snapshot propagationCounterSnapshot) {
	b.Helper()
	totals := snapshot.Totals
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
	b.ReportMetric(float64(totals.BetaHolderHits), "beta-holder-hits/run")
	b.ReportMetric(float64(totals.BetaMultiHolderDemotions), "beta-multi-holder-demotions/run")
	b.ReportMetric(float64(totals.BetaIdentityIndexBuilds), "beta-identity-index-builds/run")
	b.ReportMetric(float64(totals.BetaIdentityIndexInserts), "beta-identity-index-inserts/run")
	b.ReportMetric(float64(totals.BetaIdentityIndexProbes), "beta-identity-index-probes/run")
	b.ReportMetric(float64(totals.BetaIdentityIndexCandidates), "beta-identity-index-candidates/run")
	b.ReportMetric(float64(totals.BetaIdentityScanFallbacks), "beta-identity-scan-fallbacks/run")
	b.ReportMetric(float64(totals.BetaIdentityScanCandidates), "beta-identity-scan-candidates/run")
	b.ReportMetric(float64(totals.TokenRowsAllocated), "token-rows-allocated/run")
	b.ReportMetric(float64(totals.BetaRowsRemoved), "beta-rows-removed/run")
	b.ReportMetric(float64(totals.NegativeBetaRowsRemoved), "negative-beta-rows-removed/run")
	b.ReportMetric(float64(totals.NegativeBlockerIncrements), "blocker-increments/run")
	b.ReportMetric(float64(totals.NegativeBlockerDecrements), "blocker-decrements/run")
	b.ReportMetric(float64(totals.NegativeBlockerZeroToOne), "blocker-zero-to-one/run")
	b.ReportMetric(float64(totals.NegativeBlockerOneToZero), "blocker-one-to-zero/run")
	for _, stage := range topPropagationStageCounts(snapshot.TokenRowsByStage, 5) {
		b.ReportMetric(float64(stage.Count), fmt.Sprintf("token-rows-%s-%d/run", propagationStageKindName(stage.Stage.kind), stage.Stage.id))
	}
}

func topPropagationStageCounts(counts []propagationStageCount, limit int) []propagationStageCount {
	top := slices.Clone(counts)
	slices.SortFunc(top, func(a, b propagationStageCount) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		if a.Stage.kind != b.Stage.kind {
			return int(a.Stage.kind) - int(b.Stage.kind)
		}
		return a.Stage.id - b.Stage.id
	})
	if limit >= 0 && len(top) > limit {
		top = top[:limit]
	}
	return top
}

func propagationStageKindName(kind reteGraphStageKind) string {
	switch kind {
	case reteGraphStageRoot:
		return "root"
	case reteGraphStageAlpha:
		return "alpha"
	case reteGraphStageBeta:
		return "beta"
	case reteGraphStageAggregate:
		return "aggregate"
	case reteGraphStageUnion:
		return "union"
	default:
		return "unknown"
	}
}
