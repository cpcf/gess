package engine

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"testing"
	"time"
)

// BenchmarkGessSteadyStateWarmReset measures the reset+run reuse pattern:
// arenas, indexes, and agenda storage keep their capacity across cycles, so
// this isolates per-fire pipeline cost from cold-session growth.
func BenchmarkGessSteadyStateWarmReset(b *testing.B) {
	cases := []steadyStateScalingCase{
		{streams: 4, limit: 32},
		{streams: 8, limit: 64},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("streams=%d/limit=%d", tc.streams, tc.limit)
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision := mustCompileSteadyStateScalingRuleset(b, tc)
			firedCount := tc.streams * (4*tc.limit + 5)
			session := mustSeedSteadyStateScalingSession(b, revision, tc)
			if _, err := session.Run(ctx); err != nil {
				b.Fatalf("warm Run: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				seedSteadyStateScalingSession(b, session, tc)
				b.StartTimer()
				result, err := session.Run(ctx)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				if result.Status != RunCompleted || result.Fired != firedCount {
					b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, firedCount)
				}
			}
		})
	}
}

// TestSteadyStateWarmCycleProfile captures a CPU profile that covers only
// warm reset+seed+run cycles, keeping compile and cold-session setup out of
// the samples. Enable with GESS_WARM_CYCLE_PROFILE=<profile path>.
func TestSteadyStateWarmCycleProfile(t *testing.T) {
	if os.Getenv("GESS_WARM_CYCLE_PROFILE") == "" {
		t.Skip("set GESS_WARM_CYCLE_PROFILE to run")
	}
	ctx := context.Background()
	tc := steadyStateScalingCase{streams: 8, limit: 64}
	revision := mustCompileSteadyStateScalingRuleset(t, tc)
	session := mustSeedSteadyStateScalingSession(t, revision, tc)
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("warm Run: %v", err)
	}
	file, err := os.Create(os.Getenv("GESS_WARM_CYCLE_PROFILE"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := pprof.StartCPUProfile(file); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	const cycles = 400
	for range cycles {
		if _, err := session.Reset(ctx); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		seedSteadyStateScalingSession(t, session, tc)
		if result, err := session.Run(ctx); err != nil || result.Fired != tc.streams*(4*tc.limit+5) {
			t.Fatalf("Run: %v %v", result, err)
		}
	}
	elapsed := time.Since(start)
	pprof.StopCPUProfile()
	t.Logf("warm cycle: %d cycles, %s/cycle", cycles, elapsed/cycles)
}
