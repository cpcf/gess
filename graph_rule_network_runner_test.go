package gess

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const (
	graphRuleNetworkHarnessModeReplay             = "graph-replay"
	graphRuleNetworkHarnessModeSeedRun            = "seed-run"
	graphRuleNetworkHarnessModeSeedRunValues      = "seed-run-values"
	graphRuleNetworkHarnessModeSeedRunValuesBatch = "seed-run-values-batch"
	graphRuleNetworkHarnessModeSeedRunPrepared    = "seed-run-prepared"
)

func TestGraphRuleNetworkRunOnlyHarness(t *testing.T) {
	if os.Getenv("GESS_GRAPH_RULE_NETWORK_RUNNER") == "" {
		t.Skip("set GESS_GRAPH_RULE_NETWORK_RUNNER=1 to run the comparable graph-rule harness")
	}

	mode := os.Getenv("GESS_GRAPH_RULE_NETWORK_MODE")
	if mode == "" {
		mode = graphRuleNetworkHarnessModeReplay
	}
	if mode != graphRuleNetworkHarnessModeReplay && mode != graphRuleNetworkHarnessModeSeedRun && mode != graphRuleNetworkHarnessModeSeedRunValues && mode != graphRuleNetworkHarnessModeSeedRunValuesBatch && mode != graphRuleNetworkHarnessModeSeedRunPrepared {
		t.Fatalf("GESS_GRAPH_RULE_NETWORK_MODE must be %q, %q, %q, %q, or %q, got %q", graphRuleNetworkHarnessModeReplay, graphRuleNetworkHarnessModeSeedRun, graphRuleNetworkHarnessModeSeedRunValues, graphRuleNetworkHarnessModeSeedRunValuesBatch, graphRuleNetworkHarnessModeSeedRunPrepared, mode)
	}

	iterations := graphRuleNetworkHarnessEnvInt(t, "GESS_GRAPH_RULE_NETWORK_ITERATIONS", 3)
	warmup := graphRuleNetworkHarnessEnvInt(t, "GESS_GRAPH_RULE_NETWORK_WARMUP", 1)
	if iterations <= 0 {
		t.Fatalf("GESS_GRAPH_RULE_NETWORK_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_GRAPH_RULE_NETWORK_WARMUP must be non-negative, got %d", warmup)
	}

	cases := graphRuleNetworkBenchmarkCases()
	orderRaw, orderSet := os.LookupEnv("GESS_GRAPH_RULE_NETWORK_ORDER")
	depthRaw, depthSet := os.LookupEnv("GESS_GRAPH_RULE_NETWORK_DEPTH")
	itemsRaw, itemsSet := os.LookupEnv("GESS_GRAPH_RULE_NETWORK_ITEMS")
	if orderSet || depthSet || itemsSet {
		if !orderSet || !depthSet || !itemsSet {
			t.Fatal("GESS_GRAPH_RULE_NETWORK_ORDER, GESS_GRAPH_RULE_NETWORK_DEPTH, and GESS_GRAPH_RULE_NETWORK_ITEMS must be provided together")
		}
		depth, err := strconv.Atoi(depthRaw)
		if err != nil {
			t.Fatalf("GESS_GRAPH_RULE_NETWORK_DEPTH: %v", err)
		}
		items, err := strconv.Atoi(itemsRaw)
		if err != nil {
			t.Fatalf("GESS_GRAPH_RULE_NETWORK_ITEMS: %v", err)
		}
		cases = []graphRuleNetworkCase{{order: graphRuleNetworkOrder(orderRaw), depth: depth, items: items}}
	}

	for _, tc := range cases {
		runGraphRuleNetworkHarnessCase(t, tc, iterations, warmup, mode)
	}
}

func runGraphRuleNetworkHarnessCase(t *testing.T, tc graphRuleNetworkCase, iterations, warmup int, mode string) {
	t.Helper()

	switch mode {
	case graphRuleNetworkHarnessModeReplay:
		runGraphRuleNetworkReplayHarnessCase(t, tc, iterations, warmup)
	case graphRuleNetworkHarnessModeSeedRun:
		runGraphRuleNetworkSeedRunHarnessCase(t, tc, iterations, warmup, mode, seedAuthoredOrderFacts)
	case graphRuleNetworkHarnessModeSeedRunValues:
		runGraphRuleNetworkSeedRunHarnessCase(t, tc, iterations, warmup, mode, seedAuthoredOrderFactsWithTemplateValues)
	case graphRuleNetworkHarnessModeSeedRunValuesBatch:
		runGraphRuleNetworkSeedRunHarnessCase(t, tc, iterations, warmup, mode, seedAuthoredOrderFactsWithTemplateValueBatch)
	case graphRuleNetworkHarnessModeSeedRunPrepared:
		runGraphRuleNetworkSeedRunHarnessCase(t, tc, iterations, warmup, mode, seedAuthoredOrderFactsWithPreparedTemplateValues)
	default:
		t.Fatalf("unsupported graph-rule-network harness mode %q", mode)
	}
}

func runGraphRuleNetworkReplayHarnessCase(t *testing.T, tc graphRuleNetworkCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	facts := graphRuleNetworkFactSnapshots(t, revision, templates, tc.items)
	expected := graphRuleNetworkExpectedMatches(tc.depth, tc.items)

	memory := newReteGraphBetaMemory(revision, revision.graph, nil)
	if memory == nil {
		t.Fatal("newReteGraphBetaMemory returned nil")
	}
	for range warmup {
		graphRuleNetworkReplay(t, ctx, memory, facts, expected, "warmup")
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for range iterations {
		graphRuleNetworkReplay(t, ctx, memory, facts, expected, "benchmark")
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	snapshot := collectGraphRuleNetworkReplaySnapshot(t, revision, facts)
	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|graph-rule-network|graph-replay|order=%s|depth=%d|items=%d|rules=1|initial-facts=%d|terminal-rows=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f|graph-token-rows-retained=%d|max-beta-node-token-rows=%d|max-beta-left-token-rows=%d|max-beta-right-token-rows=%d|beta-bucket-probes=%d|beta-candidate-rows-scanned=%d|beta-joined-tokens=%d|tokens-created=%d\n",
		tc.order, tc.depth, tc.items, authoredOrderInitialFacts(tc.items), expected, iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
		snapshot.Counters.GraphBetaMemory.TokenRows,
		snapshot.Diagnostics.MaxBetaRows,
		snapshot.Diagnostics.MaxBetaLeftRows,
		snapshot.Diagnostics.MaxBetaRightRows,
		snapshot.Counters.Totals.BetaBucketProbes,
		snapshot.Counters.Totals.BetaCandidateRowsScanned,
		snapshot.Counters.Totals.BetaJoinedTokensProduced,
		snapshot.Counters.Totals.TokensCreated,
	)
}

func runGraphRuleNetworkSeedRunHarnessCase(t *testing.T, tc graphRuleNetworkCase, iterations, warmup int, mode string, seed func(testing.TB, context.Context, *Session, authoredOrderBenchmarkTemplates, int)) {
	t.Helper()

	ctx := context.Background()
	revision, templates := mustCompileGraphRuleNetworkBenchmark(t, tc)
	expected := graphRuleNetworkExpectedMatches(tc.depth, tc.items)
	initialFacts := authoredOrderInitialFacts(tc.items)

	for i := range warmup {
		session := mustSession(t, revision, SessionID(fmt.Sprintf("graph-rule-network-seed-warmup-%s-%d-%d-%d", tc.order, tc.depth, tc.items, i)))
		session.attachPropagationCounters()
		seed(t, ctx, session, templates, tc.items)
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("warmup Run: %v", err)
		}
		validateGraphRuleNetworkSeedRunHarnessSession(t, session, result, expected, initialFacts, "warmup")
	}

	sessions := make([]*Session, iterations)
	for i := range sessions {
		sessions[i] = mustSession(t, revision, SessionID(fmt.Sprintf("graph-rule-network-seed-benchmark-%s-%d-%d-%d", tc.order, tc.depth, tc.items, i)))
		sessions[i].attachPropagationCounters()
	}

	results := make([]RunResult, iterations)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i, session := range sessions {
		seed(t, ctx, session, templates, tc.items)
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("benchmark Run: %v", err)
		}
		results[i] = result
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	for i, session := range sessions {
		validateGraphRuleNetworkSeedRunHarnessSession(t, session, results[i], expected, initialFacts, "benchmark")
	}

	snapshot := sessions[len(sessions)-1].propagationCounterSnapshot()
	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|graph-rule-network|%s|order=%s|depth=%d|items=%d|rules=1|initial-facts=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f|graph-token-rows-retained=%d|terminal-rows-retained=%d|beta-bucket-probes=%d|beta-candidate-rows-scanned=%d|beta-joined-tokens=%d|tokens-created=%d\n",
		mode, tc.order, tc.depth, tc.items, initialFacts, expected, iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
		snapshot.GraphBetaMemory.TokenRows,
		snapshot.TerminalRowsRetained,
		snapshot.Totals.BetaBucketProbes,
		snapshot.Totals.BetaCandidateRowsScanned,
		snapshot.Totals.BetaJoinedTokensProduced,
		snapshot.Totals.TokensCreated,
	)
}

func seedAuthoredOrderFactsWithTemplateValues(t testing.TB, ctx context.Context, session *Session, templates authoredOrderBenchmarkTemplates, items int) {
	t.Helper()
	// Template value assertions use compiled slot order; template compilation sorts fields by name.
	for id := range authoredOrderRootCount {
		group := "other"
		if authoredOrderRootSelected(id) {
			group = "target"
		}
		if err := session.insertTemplateValuesWithContextAndOrigin(ctx, templates.root, []Value{
			newBoolValue(true),
			newStringValue(group),
			newIntValue(int64(id)),
		}, mutationOrigin{}); err != nil {
			t.Fatalf("insertTemplateValues(root): %v", err)
		}
	}
	for id := range items {
		if err := session.insertTemplateValuesWithContextAndOrigin(ctx, templates.event, []Value{
			newIntValue(int64(id)),
			newIntValue(int64(id % authoredOrderRootCount)),
			newIntValue(int64(authoredOrderScore(id))),
		}, mutationOrigin{}); err != nil {
			t.Fatalf("insertTemplateValues(event): %v", err)
		}
		if err := session.insertTemplateValuesWithContextAndOrigin(ctx, templates.detail, []Value{
			newStringValue(authoredOrderDetailCode(id)),
			newIntValue(int64(id)),
		}, mutationOrigin{}); err != nil {
			t.Fatalf("insertTemplateValues(detail): %v", err)
		}
		if err := session.insertTemplateValuesWithContextAndOrigin(ctx, templates.tag, []Value{
			newIntValue(int64(id)),
			newStringValue(authoredOrderTagLabel(id)),
		}, mutationOrigin{}); err != nil {
			t.Fatalf("insertTemplateValues(tag): %v", err)
		}
		if authoredOrderBlocked(id) {
			if err := session.insertTemplateValuesWithContextAndOrigin(ctx, templates.block, []Value{
				newBoolValue(true),
				newIntValue(int64(id)),
			}, mutationOrigin{}); err != nil {
				t.Fatalf("insertTemplateValues(block): %v", err)
			}
		}
	}
}

func seedAuthoredOrderFactsWithTemplateValueBatch(t testing.TB, ctx context.Context, session *Session, templates authoredOrderBenchmarkTemplates, items int) {
	t.Helper()
	err := session.insertTemplateValuesBatchWithContext(ctx, func(batch *templateValueBatch) error {
		seedAuthoredOrderFactsIntoTemplateValueBatch(t, batch, templates, items)
		return nil
	})
	if err != nil {
		t.Fatalf("insertTemplateValuesBatch: %v", err)
	}
}

func seedAuthoredOrderFactsIntoTemplateValueBatch(t testing.TB, batch *templateValueBatch, templates authoredOrderBenchmarkTemplates, items int) {
	t.Helper()
	for id := range authoredOrderRootCount {
		group := "other"
		if authoredOrderRootSelected(id) {
			group = "target"
		}
		if err := batch.insert(templates.root, []Value{
			newBoolValue(true),
			newStringValue(group),
			newIntValue(int64(id)),
		}); err != nil {
			t.Fatalf("insertTemplateValuesBatch(root): %v", err)
		}
	}
	for id := range items {
		if err := batch.insert(templates.event, []Value{
			newIntValue(int64(id)),
			newIntValue(int64(id % authoredOrderRootCount)),
			newIntValue(int64(authoredOrderScore(id))),
		}); err != nil {
			t.Fatalf("insertTemplateValuesBatch(event): %v", err)
		}
		if err := batch.insert(templates.detail, []Value{
			newStringValue(authoredOrderDetailCode(id)),
			newIntValue(int64(id)),
		}); err != nil {
			t.Fatalf("insertTemplateValuesBatch(detail): %v", err)
		}
		if err := batch.insert(templates.tag, []Value{
			newIntValue(int64(id)),
			newStringValue(authoredOrderTagLabel(id)),
		}); err != nil {
			t.Fatalf("insertTemplateValuesBatch(tag): %v", err)
		}
		if authoredOrderBlocked(id) {
			if err := batch.insert(templates.block, []Value{
				newBoolValue(true),
				newIntValue(int64(id)),
			}); err != nil {
				t.Fatalf("insertTemplateValuesBatch(block): %v", err)
			}
		}
	}
}

func seedAuthoredOrderFactsWithPreparedTemplateValues(t testing.TB, ctx context.Context, session *Session, templates authoredOrderBenchmarkTemplates, items int) {
	t.Helper()
	root := mustPrepareTemplateValueInserter(t, session, templates.root)
	event := mustPrepareTemplateValueInserter(t, session, templates.event)
	detail := mustPrepareTemplateValueInserter(t, session, templates.detail)
	tag := mustPrepareTemplateValueInserter(t, session, templates.tag)
	block := mustPrepareTemplateValueInserter(t, session, templates.block)

	err := session.insertPreparedTemplateValuesBatchWithContext(ctx, func(batch *preparedTemplateValueBatch) error {
		batch.reserve(authoredOrderInitialFacts(items), authoredOrderPreparedSeedSlotCount(items))
		seedAuthoredOrderFactsIntoPreparedTemplateValueBatch(t, batch, root, event, detail, tag, block, items)
		return nil
	})
	if err != nil {
		t.Fatalf("insertPreparedTemplateValuesBatch: %v", err)
	}
}

func mustPrepareTemplateValueInserter(t testing.TB, session *Session, templateKey TemplateKey) preparedTemplateValueInserter {
	t.Helper()
	inserter, err := session.prepareTemplateValueInserter(templateKey)
	if err != nil {
		t.Fatalf("prepareTemplateValueInserter(%s): %v", templateKey, err)
	}
	return inserter
}

func seedAuthoredOrderFactsIntoPreparedTemplateValueBatch(t testing.TB, batch *preparedTemplateValueBatch, root, event, detail, tag, block preparedTemplateValueInserter, items int) {
	t.Helper()
	for id := range authoredOrderRootCount {
		group := "other"
		if authoredOrderRootSelected(id) {
			group = "target"
		}
		if err := root.insert3(batch,
			newBoolValue(true),
			newStringValue(group),
			newIntValue(int64(id)),
		); err != nil {
			t.Fatalf("insertPreparedTemplateValues(root): %v", err)
		}
	}
	for id := range items {
		if err := event.insert3(batch,
			newIntValue(int64(id)),
			newIntValue(int64(id%authoredOrderRootCount)),
			newIntValue(int64(authoredOrderScore(id))),
		); err != nil {
			t.Fatalf("insertPreparedTemplateValues(event): %v", err)
		}
		if err := detail.insert2(batch,
			newStringValue(authoredOrderDetailCode(id)),
			newIntValue(int64(id)),
		); err != nil {
			t.Fatalf("insertPreparedTemplateValues(detail): %v", err)
		}
		if err := tag.insert2(batch,
			newIntValue(int64(id)),
			newStringValue(authoredOrderTagLabel(id)),
		); err != nil {
			t.Fatalf("insertPreparedTemplateValues(tag): %v", err)
		}
		if authoredOrderBlocked(id) {
			if err := block.insert2(batch,
				newBoolValue(true),
				newIntValue(int64(id)),
			); err != nil {
				t.Fatalf("insertPreparedTemplateValues(block): %v", err)
			}
		}
	}
}

func authoredOrderPreparedSeedSlotCount(items int) int {
	blocked := 0
	for id := range items {
		if authoredOrderBlocked(id) {
			blocked++
		}
	}
	return authoredOrderRootCount*3 + items*7 + blocked*2
}

func graphRuleNetworkReplay(t testing.TB, ctx context.Context, memory *reteGraphBetaMemory, facts []FactSnapshot, expected int, phase string) {
	t.Helper()
	memory.resetFacts(nil)
	for _, fact := range facts {
		memory.insertFact(fact, nil)
	}
	deltas, ok, err := memory.currentTerminalTokenDeltas(ctx)
	if err != nil {
		t.Fatalf("%s currentTerminalTokenDeltas: %v", phase, err)
	}
	if !ok {
		t.Fatalf("%s currentTerminalTokenDeltas unavailable", phase)
	}
	if len(deltas) != expected {
		t.Fatalf("%s terminal deltas = %d, want %d", phase, len(deltas), expected)
	}
	benchmarkGraphRuleTerminalDeltas = deltas
}

func validateGraphRuleNetworkSeedRunHarnessSession(t testing.TB, session *Session, result RunResult, expected, initialFacts int, phase string) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != expected {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, expected)
	}
	if got := len(session.factsByID); got != initialFacts {
		t.Fatalf("%s facts retained = %d, want %d", phase, got, initialFacts)
	}
}

func graphRuleNetworkHarnessEnvInt(t *testing.T, name string, defaultValue int) int {
	t.Helper()

	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
