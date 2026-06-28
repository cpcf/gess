package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSteadyStateScalingRunOnlyHarness(t *testing.T) {
	if os.Getenv("GESS_STEADY_STATE_RUNNER") == "" {
		t.Skip("set GESS_STEADY_STATE_RUNNER=1 to run the comparable steady-state harness")
	}

	iterations := steadyStateHarnessEnvInt(t, "GESS_STEADY_STATE_ITERATIONS", 3)
	warmup := steadyStateHarnessEnvInt(t, "GESS_STEADY_STATE_WARMUP", 1)
	if iterations <= 0 {
		t.Fatalf("GESS_STEADY_STATE_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_STEADY_STATE_WARMUP must be non-negative, got %d", warmup)
	}

	cases := []steadyStateScalingCase{
		{streams: 1, limit: 8},
		{streams: 2, limit: 16},
		{streams: 4, limit: 32},
		{streams: 8, limit: 64},
	}
	streamsRaw, streamsSet := os.LookupEnv("GESS_STEADY_STATE_STREAMS")
	limitRaw, limitSet := os.LookupEnv("GESS_STEADY_STATE_LIMIT")
	if streamsSet || limitSet {
		if !streamsSet || !limitSet {
			t.Fatal("GESS_STEADY_STATE_STREAMS and GESS_STEADY_STATE_LIMIT must be provided together")
		}
		streams, err := strconv.Atoi(streamsRaw)
		if err != nil {
			t.Fatalf("GESS_STEADY_STATE_STREAMS: %v", err)
		}
		limit, err := strconv.Atoi(limitRaw)
		if err != nil {
			t.Fatalf("GESS_STEADY_STATE_LIMIT: %v", err)
		}
		cases = []steadyStateScalingCase{{streams: streams, limit: limit}}
	}

	for _, tc := range cases {
		runSteadyStateHarnessCase(t, tc, iterations, warmup)
	}
}

func runSteadyStateHarnessCase(t *testing.T, tc steadyStateScalingCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision := mustCompileSteadyStateScalingRuleset(t, tc)
	for range warmup {
		session := mustSeedSteadyStateScalingSession(t, revision, tc)
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("warmup Run: %v", err)
		}
		validateSteadyStateHarnessSession(t, session, result, tc, "warmup")
	}

	sessions := make([]*Session, iterations)
	for i := range sessions {
		sessions[i] = mustSeedSteadyStateScalingSession(t, revision, tc)
	}
	results := make([]RunResult, iterations)

	profilePath := os.Getenv("GESS_STEADY_STATE_CPU_PROFILE")
	var profileFile *os.File
	if profilePath != "" {
		var err error
		profileFile, err = os.Create(profilePath)
		if err != nil {
			t.Fatalf("create CPU profile: %v", err)
		}
		defer profileFile.Close()
		if err := pprof.StartCPUProfile(profileFile); err != nil {
			t.Fatalf("start CPU profile: %v", err)
		}
	}

	memProfilePath := os.Getenv("GESS_STEADY_STATE_MEM_PROFILE")
	memProfileRate := runtime.MemProfileRate
	if memProfilePath != "" {
		runtime.MemProfileRate = 1
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i, session := range sessions {
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("benchmark Run: %v", err)
		}
		results[i] = result
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if memProfilePath != "" {
		runtime.MemProfileRate = memProfileRate
	}
	if profilePath != "" {
		pprof.StopCPUProfile()
	}
	if memProfilePath != "" {
		memProfileFile, err := os.Create(memProfilePath)
		if err != nil {
			t.Fatalf("create allocation profile: %v", err)
		}
		profile := pprof.Lookup("allocs")
		if profile == nil {
			if err := memProfileFile.Close(); err != nil {
				t.Fatalf("close allocation profile: %v", err)
			}
			t.Fatal("allocation profile unavailable")
		}
		if err := profile.WriteTo(memProfileFile, 0); err != nil {
			_ = memProfileFile.Close()
			t.Fatalf("write allocation profile: %v", err)
		}
		if err := memProfileFile.Close(); err != nil {
			t.Fatalf("close allocation profile: %v", err)
		}
	}

	for i, session := range sessions {
		validateSteadyStateHarnessSession(t, session, results[i], tc, "benchmark")
	}
	propagationFields := ""
	if fields := collectSteadyStateScalingPropagationCounters(t, revision, tc).runnerFields(); len(fields) > 0 {
		propagationFields = "|" + strings.Join(fields, "|")
	}

	ruleCount := tc.streams * 6
	finalFacts := tc.streams * (4*(tc.limit+1) + 2)
	firedCount := tc.streams * (4*tc.limit + 5)
	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|steady-state-scaling|run-only|streams=%d|limit=%d|rules=%d|final-facts=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f%s\n",
		tc.streams, tc.limit, ruleCount, finalFacts, firedCount, iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
		propagationFields,
	)
}

func validateSteadyStateHarnessSession(t testing.TB, session *Session, result RunResult, tc steadyStateScalingCase, phase string) {
	t.Helper()

	firedCount := tc.streams * (4*tc.limit + 5)
	finalFacts := tc.streams * (4*(tc.limit+1) + 2)
	if result.Status != RunCompleted || result.Fired != firedCount {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, firedCount)
	}
	if got := len(session.facts); got != finalFacts {
		t.Fatalf("%s final fact count = %d, want %d", phase, got, finalFacts)
	}
	assertSteadyStateFactMix(t, session, tc)
}

func steadyStateHarnessEnvInt(t *testing.T, name string, defaultValue int) int {
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
