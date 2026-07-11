package engine

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkBackchainDemandSupportChurn(b *testing.B) {
	for _, active := range []int{1, 128, 1_024} {
		b.Run(fmt.Sprintf("active=%d", active), func(b *testing.B) {
			session := &Session{}
			requests := make([]backchainDemandRequest, active)
			demandFacts := make([]workingFact, active)
			for i := range active {
				sequence := uint64(i + 1)
				requests[i] = backchainDemandRequest{
					templateKey:  TemplateKey("answer"),
					supportFacts: []backchainDemandSupportFact{{id: newFactID(1, sequence), version: 1}},
					slots:        []factSlot{{value: newIntValue(int64(i)), ok: true}},
				}
				demandFacts[i].id = newFactID(2, sequence)
			}

			churnBackchainDemandSupports(b, session, demandFacts, requests)
			assertBackchainDemandChurnOwner(b, session, active)

			b.ReportAllocs()
			b.ReportMetric(float64(active), "supports/op")
			b.ResetTimer()
			for b.Loop() {
				churnBackchainDemandSupports(b, session, demandFacts, requests)
			}
			b.StopTimer()
			owner := assertBackchainDemandChurnOwner(b, session, active)
			b.ReportMetric(float64(owner.HighWater), "retained-slots")
			b.ReportMetric(float64(owner.Indexes), "reusable-slots")
			b.ReportMetric(float64(owner.Bytes), "retained-B")
		})
	}
}

func churnBackchainDemandSupports(tb testing.TB, session *Session, demandFacts []workingFact, requests []backchainDemandRequest) {
	tb.Helper()
	for i := range requests {
		if id := session.addBackchainDemandSupport(&demandFacts[i], requests[i]); id == 0 {
			tb.Fatalf("add support %d returned zero ID", i)
		}
	}
	for i := range requests {
		if _, err := session.removeBackchainDemandSupportForRequest(context.Background(), requests[i], mutationOrigin{}); err != nil {
			tb.Fatalf("remove support %d: %v", i, err)
		}
	}
}

func assertBackchainDemandChurnOwner(tb testing.TB, session *Session, active int) RuntimeMemoryOwnerDiagnostics {
	tb.Helper()
	owner := session.backchainDemandSupportMemoryOwnerDiagnostics()
	want := uint64(active)
	if owner.Rows != 0 || owner.HighWater != want || owner.Tombstones != want || owner.Indexes != want {
		tb.Fatalf("demand support owner = %#v, want rows/high-water/tombstones/indexes 0/%d/%d/%d", owner, active, active, active)
	}
	if owner.Bytes == 0 {
		tb.Fatal("demand support retained bytes = 0")
	}
	return owner
}
