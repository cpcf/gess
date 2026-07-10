package engine

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkGraphRuleResults []ruleMatchResult

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
				b.ReportMetric(float64(expected), "matches/replay")
				reportGraphRuleNetworkCounterMetrics(b, snapshot)
			}()

			memory, err := newReteGraphBetaMemory(context.Background(), revision, revision.graph, nil)
			if err != nil {
				b.Fatalf("newReteGraphBetaMemory: %v", err)
			}
			if memory == nil {
				b.Fatal("newReteGraphBetaMemory returned nil")
			}

			b.ResetTimer()
			for b.Loop() {
				if err := memory.resetFacts(context.Background(), nil); err != nil {
					b.Fatalf("resetFacts: %v", err)
				}
				for _, fact := range facts {
					if _, err := memory.insertFact(context.Background(), fact, nil); err != nil {
						b.Fatalf("insertFact: %v", err)
					}
				}
				results, err := memory.match(context.Background(), graphRuleNetworkReplayFactSource(revision, facts))
				if err != nil {
					b.Fatalf("match: %v", err)
				}
				if got := graphRuleNetworkResultCandidateCount(results); got != expected {
					b.Fatalf("match candidates = %d, want %d", got, expected)
				}
				benchmarkGraphRuleResults = results
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
			if snapshot.Counters.Totals.BetaBucketProbes == 0 {
				t.Fatal("beta bucket probes = 0, want graph beta joins exercised")
			}
			if snapshot.Counters.Totals.BetaJoinedTokensProduced == 0 {
				t.Fatal("joined tokens produced = 0, want graph beta joins exercised")
			}
			if snapshot.Diagnostics.MaxBetaRows == 0 {
				t.Fatal("max beta rows = 0, want per-node beta memory diagnostics")
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

func TestGraphRuleNetworkSessionOwnsFactSourceIndexes(t *testing.T) {
	ctx := context.Background()
	tc := graphRuleNetworkCase{order: graphRuleNetworkSelective, depth: 4, items: 256}
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	session := mustSession(t, revision, "graph-rule-network-fact-source-index-session")
	for _, fact := range graphRuleNetworkFactSnapshots(t, revision, templates, tc.items) {
		if _, err := session.Assert(ctx, fact.TemplateKey(), fact.Fields()); err != nil {
			t.Fatalf("Assert(%s): %v", fact.TemplateKey(), err)
		}
	}
	eventFacts, ok := session.factsForTarget(conditionTarget{kind: conditionTargetTemplateKey, templateKey: templates.event})
	if !ok {
		t.Fatal("session factsForTarget returned !ok")
	}
	if got, want := len(eventFacts), tc.items; got != want {
		t.Fatalf("event facts = %d, want %d", got, want)
	}
}

func TestGraphRuleNetworkTemplateValueBatchSeedRuns(t *testing.T) {
	ctx := context.Background()
	tc := graphRuleNetworkCase{order: graphRuleNetworkSelective, depth: 4, items: 256}
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	session := mustSession(t, revision, SessionID("graph-rule-network-template-value-batch"))
	session.attachPropagationCounters()

	seedAuthoredOrderFactsWithTemplateValueBatch(t, ctx, session, templates, tc.items)
	if got, want := session.factCount(), authoredOrderInitialFacts(tc.items); got != want {
		t.Fatalf("facts retained = %d, want %d", got, want)
	}
	if session.agendaDriver.dirty || !session.agendaDriver.ready {
		t.Fatalf("agenda state after batch seed = dirty %v ready %v, want clean ready", session.agendaDriver.dirty, session.agendaDriver.ready)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != graphRuleNetworkExpectedMatches(tc.depth, tc.items) {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, graphRuleNetworkExpectedMatches(tc.depth, tc.items))
	}
	snapshot := session.propagationCounterSnapshot()
	_ = snapshot
}

func TestGraphRuleNetworkPreparedTemplateValueSeedRuns(t *testing.T) {
	ctx := context.Background()
	tc := graphRuleNetworkCase{order: graphRuleNetworkSelective, depth: 4, items: 256}
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	session := mustSession(t, revision, SessionID("graph-rule-network-prepared-template-value"))
	session.attachPropagationCounters()

	seedAuthoredOrderFactsWithPreparedTemplateValues(t, ctx, session, templates, tc.items)
	if got, want := session.factCount(), authoredOrderInitialFacts(tc.items); got != want {
		t.Fatalf("facts retained = %d, want %d", got, want)
	}
	if !session.factStore.factTargetIndexesDirty {
		t.Fatal("fact target indexes should stay dirty after prepared seed")
	}
	if got, want := len(session.factIDsByTemplate(templates.event)), tc.items; got != want {
		t.Fatalf("event fact ids = %d, want %d", got, want)
	}
	if session.factStore.factTargetIndexesDirty {
		t.Fatal("fact target indexes remained dirty after fact id lookup")
	}
	if session.agendaDriver.dirty || !session.agendaDriver.ready {
		t.Fatalf("agenda state after prepared seed = dirty %v ready %v, want clean ready", session.agendaDriver.dirty, session.agendaDriver.ready)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != graphRuleNetworkExpectedMatches(tc.depth, tc.items) {
		t.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, graphRuleNetworkExpectedMatches(tc.depth, tc.items))
	}
	snapshot := session.propagationCounterSnapshot()
	_ = snapshot
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
	memory, err := newReteGraphBetaMemory(context.Background(), revision, revision.graph, nil)
	if err != nil {
		t.Fatalf("newReteGraphBetaMemory: %v", err)
	}
	if memory == nil {
		t.Fatal("newReteGraphBetaMemory returned nil")
	}
	ledger := newPropagationCounterLedger()
	if err := memory.resetFacts(context.Background(), nil); err != nil {
		t.Fatalf("resetFacts: %v", err)
	}
	for _, fact := range facts {
		span := ledger.beginAssert(fact.TemplateKey(), mutationOrigin{})
		if _, err := memory.insertFact(context.Background(), fact, &span); err != nil {
			t.Fatalf("insertFact: %v", err)
		}
		span.finish()
	}
	ledger.setTerminalRowsRetained(memory.terminalRowCount())
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
