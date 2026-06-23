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

func TestGraphRuleNetworkRunOnlyHarness(t *testing.T) {
	if os.Getenv("GESS_GRAPH_RULE_NETWORK_RUNNER") == "" {
		t.Skip("set GESS_GRAPH_RULE_NETWORK_RUNNER=1 to run the comparable graph-rule harness")
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
		runGraphRuleNetworkHarnessCase(t, tc, iterations, warmup)
	}
}

func runGraphRuleNetworkHarnessCase(t *testing.T, tc graphRuleNetworkCase, iterations, warmup int) {
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
