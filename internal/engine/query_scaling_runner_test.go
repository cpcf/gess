package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestQueryScalingRunOnlyHarness(t *testing.T) {
	if os.Getenv("GESS_QUERY_SCALING_RUNNER") == "" {
		t.Skip("set GESS_QUERY_SCALING_RUNNER=1 to run the comparable query scaling harness")
	}

	iterations := queryScalingHarnessEnvInt(t, "GESS_QUERY_SCALING_ITERATIONS", 10)
	warmup := queryScalingHarnessEnvInt(t, "GESS_QUERY_SCALING_WARMUP", 2)
	if iterations <= 0 {
		t.Fatalf("GESS_QUERY_SCALING_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_QUERY_SCALING_WARMUP must be non-negative, got %d", warmup)
	}

	cases := []queryBenchmarkCase{
		{shape: queryBenchmarkSimple, factCount: 1_000},
		{shape: queryBenchmarkSimple, factCount: 10_000},
		{shape: queryBenchmarkSimple, factCount: 50_000},
		{shape: queryBenchmarkJoin, factCount: 1_000},
		{shape: queryBenchmarkJoin, factCount: 10_000},
		{shape: queryBenchmarkJoin, factCount: 50_000},
		{shape: queryBenchmarkNegation, factCount: 1_000},
		{shape: queryBenchmarkNegation, factCount: 10_000},
		{shape: queryBenchmarkNegation, factCount: 50_000},
	}
	shapeRaw, shapeSet := os.LookupEnv("GESS_QUERY_SCALING_SHAPE")
	factsRaw, factsSet := os.LookupEnv("GESS_QUERY_SCALING_FACTS")
	if shapeSet || factsSet {
		if !shapeSet || !factsSet {
			t.Fatal("GESS_QUERY_SCALING_SHAPE and GESS_QUERY_SCALING_FACTS must be provided together")
		}
		cases = []queryBenchmarkCase{{
			shape:     parseQueryScalingShape(t, shapeRaw),
			factCount: parseQueryScalingHarnessInt(t, "GESS_QUERY_SCALING_FACTS", factsRaw),
		}}
	}

	for _, tc := range cases {
		runQueryScalingHarnessCase(t, tc, iterations, warmup)
	}
}

func runQueryScalingHarnessCase(t *testing.T, tc queryBenchmarkCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	compiled := benchmarkQueryRevision(t, tc.shape)
	initials := benchmarkQueryFacts(t, compiled, tc.factCount)
	session, err := NewSession(compiled.revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	queryName, args := benchmarkQueryInvocation(tc.shape)
	expectedRows := benchmarkQueryExpectedRows(tc.shape, tc.factCount)

	for range warmup {
		rows, err := session.QueryAll(ctx, queryName, args)
		if err != nil {
			t.Fatalf("warmup QueryAll: %v", err)
		}
		validateQueryScalingRows(t, tc, "warmup", rows, expectedRows)
	}

	var lastRows []QueryRow
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for range iterations {
		rows, err := session.QueryAll(ctx, queryName, args)
		if err != nil {
			t.Fatalf("benchmark QueryAll: %v", err)
		}
		validateQueryScalingRows(t, tc, "benchmark", rows, expectedRows)
		lastRows = rows
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	benchmarkQueryRows = lastRows

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|query-scaling|query-only|shape=%s|facts=%d|initial-facts=%d|rows=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f\n",
		tc.shape,
		tc.factCount,
		len(initials),
		expectedRows,
		iterations,
		warmup,
		elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
	)
}

func validateQueryScalingRows(t testing.TB, tc queryBenchmarkCase, phase string, rows []QueryRow, expectedRows int) {
	t.Helper()
	if len(rows) != expectedRows {
		t.Fatalf("%s %s facts=%d rows = %d, want %d", phase, tc.shape, tc.factCount, len(rows), expectedRows)
	}
}

func parseQueryScalingShape(t testing.TB, raw string) queryBenchmarkShape {
	t.Helper()
	shape := queryBenchmarkShape(strings.TrimSpace(raw))
	switch shape {
	case queryBenchmarkSimple, queryBenchmarkJoin, queryBenchmarkNegation:
		return shape
	default:
		t.Fatalf("unsupported GESS_QUERY_SCALING_SHAPE %q", raw)
		return ""
	}
}

func queryScalingHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue
	}
	return parseQueryScalingHarnessInt(t, name, raw)
}

func parseQueryScalingHarnessInt(t testing.TB, name, raw string) int {
	t.Helper()
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
