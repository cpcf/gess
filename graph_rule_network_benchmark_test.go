package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkGraphRuleTerminalDeltas []reteTerminalTokenDelta

type graphRuleNetworkOrder string

const (
	graphRuleNetworkSelective graphRuleNetworkOrder = "selective-first"
	graphRuleNetworkBroad     graphRuleNetworkOrder = "broad-first"
)

type graphRuleNetworkCase struct {
	order graphRuleNetworkOrder
	depth int
	items int
}

type graphRuleNetworkReplaySnapshot struct {
	Counters    propagationCounterSnapshot
	Diagnostics reteGraphBetaMemoryDiagnostics
}

func BenchmarkGraphRuleNetworkAuthoredOrderScaling(b *testing.B) {
	for _, tc := range graphRuleNetworkBenchmarkCases() {
		name := fmt.Sprintf("%s/depth=%d/items=%d/matches=%d", tc.order, tc.depth, tc.items, graphRuleNetworkExpectedMatches(tc.depth, tc.items))
		b.Run(name, func(b *testing.B) {
			revision, templates := mustCompileGraphRuleNetworkBenchmark(b, tc)
			facts := graphRuleNetworkFactSnapshots(b, revision, templates, tc.items)
			expected := graphRuleNetworkExpectedMatches(tc.depth, tc.items)
			snapshot := collectGraphRuleNetworkReplaySnapshot(b, revision, facts)

			b.ReportAllocs()
			defer func() {
				b.ReportMetric(float64(tc.depth), "join-depth")
				b.ReportMetric(float64(tc.items), "items")
				b.ReportMetric(float64(authoredOrderRootCount), "roots")
				b.ReportMetric(float64(len(facts)), "facts/replay")
				b.ReportMetric(float64(expected), "terminal-rows/replay")
				reportGraphRuleNetworkCounterMetrics(b, snapshot)
			}()

			memory := newReteGraphBetaMemory(revision, revision.graph, nil)
			if memory == nil {
				b.Fatal("newReteGraphBetaMemory returned nil")
			}

			b.ResetTimer()
			for b.Loop() {
				memory.resetFacts(nil)
				for _, fact := range facts {
					memory.insertFact(fact, nil)
				}
				deltas, ok, err := memory.currentTerminalTokenDeltas(context.Background())
				if err != nil {
					b.Fatalf("currentTerminalTokenDeltas: %v", err)
				}
				if !ok {
					b.Fatal("currentTerminalTokenDeltas unavailable")
				}
				if len(deltas) != expected {
					b.Fatalf("terminal deltas = %d, want %d", len(deltas), expected)
				}
				benchmarkGraphRuleTerminalDeltas = deltas
			}
		})
	}
}

func TestGraphRuleNetworkAuthoredOrderBenchmarkValidatesCounters(t *testing.T) {
	for _, tc := range []graphRuleNetworkCase{
		{order: graphRuleNetworkSelective, depth: 2, items: 256},
		{order: graphRuleNetworkBroad, depth: 2, items: 256},
		{order: graphRuleNetworkSelective, depth: 4, items: 256},
		{order: graphRuleNetworkBroad, depth: 4, items: 256},
	} {
		t.Run(fmt.Sprintf("%s/depth=%d", tc.order, tc.depth), func(t *testing.T) {
			revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
			facts := graphRuleNetworkFactSnapshots(t, revision, templates, tc.items)
			snapshot := collectGraphRuleNetworkReplaySnapshot(t, revision, facts)
			if got, want := snapshot.Counters.TerminalRowsRetained, graphRuleNetworkExpectedMatches(tc.depth, tc.items); got != want {
				t.Fatalf("terminal rows retained = %d, want %d", got, want)
			}
			if snapshot.Counters.Totals.BetaBucketProbes == 0 {
				t.Fatal("beta bucket probes = 0, want graph beta joins exercised")
			}
			if snapshot.Counters.Totals.BetaJoinedTokensProduced == 0 {
				t.Fatal("joined tokens produced = 0, want graph beta joins exercised")
			}
			if snapshot.Diagnostics.MaxBetaRows == 0 {
				t.Fatal("max beta rows = 0, want per-node beta memory diagnostics")
			}
			if got, want := snapshot.Diagnostics.TotalTerminalBranchRows, graphRuleNetworkExpectedMatches(tc.depth, tc.items); got != want {
				t.Fatalf("terminal branch rows = %d, want %d", got, want)
			}
		})
	}
}

func TestGraphRuleNetworkDiagnosticsBoundDepthGrowth(t *testing.T) {
	depth2 := collectGraphRuleNetworkReplaySnapshotForCase(t, graphRuleNetworkCase{order: graphRuleNetworkSelective, depth: 2, items: 256})
	depth4 := collectGraphRuleNetworkReplaySnapshotForCase(t, graphRuleNetworkCase{order: graphRuleNetworkSelective, depth: 4, items: 256})
	if depth4.Diagnostics.WidestRetainedBetaTokenWidth <= depth2.Diagnostics.WidestRetainedBetaTokenWidth {
		t.Fatalf("depth-4 retained token width = %d, want greater than depth-2 width %d", depth4.Diagnostics.WidestRetainedBetaTokenWidth, depth2.Diagnostics.WidestRetainedBetaTokenWidth)
	}
	if depth4.Diagnostics.MaxBetaRows > depth2.Diagnostics.MaxBetaRows*2 {
		t.Fatalf("depth-4 max beta rows = %d, want bounded by twice depth-2 max beta rows %d", depth4.Diagnostics.MaxBetaRows, depth2.Diagnostics.MaxBetaRows)
	}
}

func TestGraphRuleNetworkReplayBuildsFactSourceIndexesLazily(t *testing.T) {
	tc := graphRuleNetworkCase{order: graphRuleNetworkSelective, depth: 4, items: 256}
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	facts := graphRuleNetworkFactSnapshots(t, revision, templates, tc.items)
	memory := newReteGraphBetaMemory(revision, revision.graph, nil)
	if memory == nil {
		t.Fatal("newReteGraphBetaMemory returned nil")
	}
	memory.resetFacts(nil)
	for _, fact := range facts {
		memory.insertFact(fact, nil)
	}
	if !memory.factTargetIndexesDirty {
		t.Fatal("fact target indexes should stay dirty until generic fact source access")
	}
	eventFacts, ok := memory.factsForTarget(conditionTarget{kind: conditionTargetTemplateKey, templateKey: templates.event})
	if !ok {
		t.Fatal("factsForTarget returned !ok")
	}
	if got, want := len(eventFacts), tc.items; got != want {
		t.Fatalf("event facts = %d, want %d", got, want)
	}
	if memory.factTargetIndexesDirty {
		t.Fatal("fact target indexes remained dirty after factsForTarget")
	}
}

func graphRuleNetworkBenchmarkCases() []graphRuleNetworkCase {
	orders := []graphRuleNetworkOrder{graphRuleNetworkSelective, graphRuleNetworkBroad}
	depthItems := []struct {
		depth int
		items []int
	}{
		{depth: 2, items: []int{1_000, 10_000}},
		{depth: 3, items: []int{1_000, 10_000}},
		{depth: 4, items: []int{250, 1_000}},
	}
	cases := make([]graphRuleNetworkCase, 0, len(orders)*6)
	for _, order := range orders {
		for _, depthItem := range depthItems {
			for _, count := range depthItem.items {
				cases = append(cases, graphRuleNetworkCase{order: order, depth: depthItem.depth, items: count})
			}
		}
	}
	return cases
}

func mustCompileGraphRuleNetworkBenchmark(t testing.TB, tc graphRuleNetworkCase) (*Ruleset, authoredOrderBenchmarkTemplates) {
	t.Helper()
	workspace, templates := authoredOrderWorkspace(t)
	mustAddAction(t, workspace, ActionSpec{
		Name: "record-graph-rule-network-match",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:          fmt.Sprintf("graph-rule-network-%s-depth-%d", tc.order, tc.depth),
		ConditionTree: graphRuleNetworkConditionTree(tc.order, tc.depth, templates),
		Actions:       []RuleActionSpec{{Name: "record-graph-rule-network-match"}},
	})
	return mustCompileWorkspace(t, workspace), templates
}

func graphRuleNetworkConditionTree(order graphRuleNetworkOrder, depth int, templates authoredOrderBenchmarkTemplates) ConditionSpec {
	conditions := make([]ConditionSpec, 0, depth)
	switch order {
	case graphRuleNetworkBroad:
		conditions = append(conditions, authoredOrderEventMatch(templates), authoredOrderRootJoinedToEventMatch(templates))
	default:
		conditions = append(conditions, authoredOrderRootMatch(templates), authoredOrderEventJoinedToRootMatch(templates))
	}
	if depth >= 3 {
		conditions = append(conditions, authoredOrderDetailJoinedToEventMatch(templates))
	}
	if depth >= 4 {
		conditions = append(conditions, authoredOrderTagJoinedToEventMatch(templates))
	}
	return And{Conditions: conditions}
}

func graphRuleNetworkFactSnapshots(t testing.TB, revision *Ruleset, templates authoredOrderBenchmarkTemplates, items int) []FactSnapshot {
	t.Helper()
	session := mustSession(t, revision, SessionID(fmt.Sprintf("graph-rule-network-facts-%d", items)))
	seedAuthoredOrderFacts(t, context.Background(), session, templates, items)
	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snapshot.Facts()
}

func collectGraphRuleNetworkReplaySnapshotForCase(t testing.TB, tc graphRuleNetworkCase) graphRuleNetworkReplaySnapshot {
	t.Helper()
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	facts := graphRuleNetworkFactSnapshots(t, revision, templates, tc.items)
	return collectGraphRuleNetworkReplaySnapshot(t, revision, facts)
}

func collectGraphRuleNetworkReplaySnapshot(t testing.TB, revision *Ruleset, facts []FactSnapshot) graphRuleNetworkReplaySnapshot {
	t.Helper()
	memory := newReteGraphBetaMemory(revision, revision.graph, nil)
	if memory == nil {
		t.Fatal("newReteGraphBetaMemory returned nil")
	}
	ledger := newPropagationCounterLedger()
	memory.resetFacts(nil)
	for _, fact := range facts {
		span := ledger.beginAssert(fact.TemplateKey(), mutationOrigin{})
		memory.insertFact(fact, &span)
		span.finish()
	}
	ledger.setTerminalRowsRetained(memory.terminalRowCount())
	ledger.setBranchRowsRetained(memory.terminalRowsRetainedByBranch())
	ledger.setGraphBetaMemoryStats(memory.memoryStats())
	return graphRuleNetworkReplaySnapshot{
		Counters:    ledger.snapshot(),
		Diagnostics: memory.diagnostics(),
	}
}

func reportGraphRuleNetworkCounterMetrics(b *testing.B, snapshot graphRuleNetworkReplaySnapshot) {
	b.Helper()
	counters := snapshot.Counters
	b.ReportMetric(float64(counters.Totals.BetaBucketProbes), "beta-bucket-probes/replay")
	b.ReportMetric(float64(counters.Totals.BetaBucketDepthTotal), "beta-bucket-depth-total/replay")
	b.ReportMetric(float64(counters.Totals.BetaBucketDepthMax), "beta-bucket-depth-max")
	b.ReportMetric(float64(counters.Totals.BetaBucketDepthTotal)/float64(max(1, counters.Totals.BetaBucketProbes)), "beta-bucket-depth-mean")
	b.ReportMetric(float64(counters.Totals.BetaCandidateRowsScanned), "beta-candidate-rows-scanned/replay")
	b.ReportMetric(float64(counters.Totals.BetaResidualTests), "beta-residual-tests/replay")
	b.ReportMetric(float64(counters.Totals.BetaResidualFailures), "beta-residual-failures/replay")
	b.ReportMetric(float64(counters.Totals.BetaJoinedTokensProduced), "beta-joined-tokens/replay")
	b.ReportMetric(float64(counters.Totals.TokensCreated), "tokens-created/replay")
	b.ReportMetric(float64(counters.Totals.TerminalDeltasEmitted), "terminal-deltas/replay")
	b.ReportMetric(float64(counters.Totals.TerminalRowsInserted), "terminal-rows-inserted/replay")
	b.ReportMetric(float64(counters.Totals.TerminalRowsDeduped), "terminal-rows-deduped/replay")
	b.ReportMetric(float64(counters.TerminalRowsRetained), "terminal-rows-retained")
	b.ReportMetric(float64(counters.GraphBetaMemory.TokenRows), "graph-token-rows-retained")
	b.ReportMetric(float64(counters.GraphBetaMemory.JoinIndexKeys), "graph-join-index-keys")
	reportGraphRuleNetworkDiagnosticMetrics(b, snapshot.Diagnostics)
}

func reportGraphRuleNetworkDiagnosticMetrics(b *testing.B, diagnostics reteGraphBetaMemoryDiagnostics) {
	b.Helper()
	b.ReportMetric(float64(len(diagnostics.BetaNodes)), "beta-node-count")
	b.ReportMetric(float64(diagnostics.MaxBetaRows), "max-beta-node-token-rows")
	b.ReportMetric(float64(diagnostics.MaxBetaLeftRows), "max-beta-left-token-rows")
	b.ReportMetric(float64(diagnostics.MaxBetaRightRows), "max-beta-right-token-rows")
	b.ReportMetric(float64(diagnostics.MaxBetaJoinIndexKeys), "max-beta-node-join-index-keys")
	b.ReportMetric(float64(diagnostics.MaxBetaJoinBucketDepth), "max-beta-node-join-bucket-depth")
	b.ReportMetric(float64(diagnostics.WidestRetainedBetaTokenWidth), "widest-retained-beta-token-width")
	b.ReportMetric(float64(len(diagnostics.Terminals)), "terminal-node-count")
	b.ReportMetric(float64(diagnostics.MaxTerminalRows), "max-terminal-node-token-rows")
	b.ReportMetric(float64(diagnostics.TotalTerminalBranchRows), "terminal-branch-rows-retained")
	b.ReportMetric(float64(diagnostics.MaxTerminalBranchRows), "max-terminal-branch-rows")
	for _, node := range diagnostics.BetaNodes {
		prefix := fmt.Sprintf("beta-node-%d", node.ID)
		b.ReportMetric(float64(node.TotalRows), prefix+"-token-rows")
		b.ReportMetric(float64(node.LeftRows), prefix+"-left-token-rows")
		b.ReportMetric(float64(node.RightRows), prefix+"-right-token-rows")
		b.ReportMetric(float64(node.TokenWidth), prefix+"-token-width")
		b.ReportMetric(float64(node.TotalJoinIndexKeys), prefix+"-join-index-keys")
		b.ReportMetric(float64(node.MaxJoinBucketDepth), prefix+"-join-bucket-depth-max")
	}
	for _, terminal := range diagnostics.Terminals {
		prefix := fmt.Sprintf("terminal-node-%d", terminal.ID)
		b.ReportMetric(float64(terminal.Rows), prefix+"-token-rows")
		b.ReportMetric(float64(terminal.TokenWidth), prefix+"-token-width")
		for branchID, rows := range terminal.BranchRows {
			b.ReportMetric(float64(rows), fmt.Sprintf("%s-branch-%d-rows", prefix, branchID))
		}
	}
}

func graphRuleNetworkExpectedMatches(depth, items int) int {
	matches := 0
	for id := range items {
		if !authoredOrderBaseMatch(id) {
			continue
		}
		if depth >= 3 && !authoredOrderDetailSelected(id) {
			continue
		}
		if depth >= 4 && !authoredOrderTagPriority(id) {
			continue
		}
		matches++
	}
	return matches
}
