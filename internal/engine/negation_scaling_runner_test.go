package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNegationScalingSeedRunHarness(t *testing.T) {
	if os.Getenv("GESS_NEGATION_SCALING_RUNNER") == "" {
		t.Skip("set GESS_NEGATION_SCALING_RUNNER=1 to run the comparable negation scaling harness")
	}

	iterations := negationScalingHarnessEnvInt(t, "GESS_NEGATION_SCALING_ITERATIONS", 3)
	warmup := negationScalingHarnessEnvInt(t, "GESS_NEGATION_SCALING_WARMUP", 1)
	if iterations <= 0 {
		t.Fatalf("GESS_NEGATION_SCALING_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_NEGATION_SCALING_WARMUP must be non-negative, got %d", warmup)
	}

	cases := []negationScalingCase{
		{streams: 1, customersPerStream: 128, blockEvery: 2},
		{streams: 4, customersPerStream: 512, blockEvery: 2},
		{streams: 8, customersPerStream: 1024, blockEvery: 2},
	}
	streamsRaw, streamsSet := os.LookupEnv("GESS_NEGATION_SCALING_STREAMS")
	customersRaw, customersSet := os.LookupEnv("GESS_NEGATION_SCALING_CUSTOMERS")
	blockEveryRaw, blockEverySet := os.LookupEnv("GESS_NEGATION_SCALING_BLOCK_EVERY")
	if streamsSet || customersSet || blockEverySet {
		if !streamsSet || !customersSet || !blockEverySet {
			t.Fatal("GESS_NEGATION_SCALING_STREAMS, GESS_NEGATION_SCALING_CUSTOMERS, and GESS_NEGATION_SCALING_BLOCK_EVERY must be provided together")
		}
		cases = []negationScalingCase{{
			streams:            parseNegationScalingHarnessInt(t, "GESS_NEGATION_SCALING_STREAMS", streamsRaw),
			customersPerStream: parseNegationScalingHarnessInt(t, "GESS_NEGATION_SCALING_CUSTOMERS", customersRaw),
			blockEvery:         parseNegationScalingHarnessInt(t, "GESS_NEGATION_SCALING_BLOCK_EVERY", blockEveryRaw),
		}}
	}

	for _, tc := range cases {
		runNegationScalingHarnessCase(t, tc, iterations, warmup)
	}
}

func runNegationScalingHarnessCase(t *testing.T, tc negationScalingCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision, customerKey, blockKey := mustCompileNegationScalingRuleset(t, tc)
	for range warmup {
		session := mustSession(t, revision, "negation-scaling-warmup-session")
		result := runNegationScalingSeedRun(t, ctx, session, customerKey, blockKey, tc)
		validateNegationScalingSession(t, session, result, tc, "warmup")
	}

	sessions := make([]*Session, iterations)
	for i := range sessions {
		sessions[i] = mustSession(t, revision, SessionID(fmt.Sprintf("negation-scaling-benchmark-session-%d", i)))
	}
	results := make([]RunResult, iterations)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i, session := range sessions {
		results[i] = runNegationScalingSeedRun(t, ctx, session, customerKey, blockKey, tc)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	for i, session := range sessions {
		validateNegationScalingSession(t, session, results[i], tc, "benchmark")
	}
	propagationFields := ""
	if fields := collectNegationScalingPropagationCounters(t, revision, customerKey, blockKey, tc).runnerFields(); len(fields) > 0 {
		propagationFields = "|" + strings.Join(fields, "|")
	}

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|negation-scaling|seed-run|streams=%d|customers=%d|block-every=%d|rules=%d|initial-facts=%d|final-facts=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f%s\n",
		tc.streams, tc.customersPerStream, tc.blockEvery, tc.ruleCount(), tc.initialFacts(), tc.finalFacts(), tc.firedCount(), iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
		propagationFields,
	)
}
