package engine

import (
	"context"
	"fmt"
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
			b.ReportMetric(float64(guestCount), "guests")
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
		})
	}
}
