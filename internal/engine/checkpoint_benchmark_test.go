package engine

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

var (
	benchmarkCheckpoint        Checkpoint
	benchmarkCheckpointBytes   []byte
	benchmarkCheckpointSession *Session
)

type checkpointBenchmarkFixture struct {
	name     string
	revision *Ruleset
	session  *Session
	encoded  []byte
}

func BenchmarkSessionCheckpoint(b *testing.B) {
	for _, fixture := range checkpointBenchmarkFixtures(b) {
		b.Run(fixture.name, func(b *testing.B) {
			reportCheckpointBenchmarkMetrics(b, fixture)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				checkpoint, err := fixture.session.Checkpoint(context.Background())
				if err != nil {
					b.Fatalf("Checkpoint: %v", err)
				}
				benchmarkCheckpoint = checkpoint
			}
		})
	}
}

func BenchmarkEncodeCheckpoint(b *testing.B) {
	for _, fixture := range checkpointBenchmarkFixtures(b) {
		checkpoint, err := fixture.session.Checkpoint(context.Background())
		if err != nil {
			b.Fatalf("Checkpoint: %v", err)
		}
		b.Run(fixture.name, func(b *testing.B) {
			reportCheckpointBenchmarkMetrics(b, fixture)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var encoded bytes.Buffer
				if err := EncodeCheckpoint(&encoded, checkpoint); err != nil {
					b.Fatalf("EncodeCheckpoint: %v", err)
				}
				benchmarkCheckpointBytes = encoded.Bytes()
			}
		})
	}
}

func BenchmarkDecodeCheckpoint(b *testing.B) {
	for _, fixture := range checkpointBenchmarkFixtures(b) {
		b.Run(fixture.name, func(b *testing.B) {
			reportCheckpointBenchmarkMetrics(b, fixture)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				checkpoint, err := DecodeCheckpoint(bytes.NewReader(fixture.encoded))
				if err != nil {
					b.Fatalf("DecodeCheckpoint: %v", err)
				}
				benchmarkCheckpoint = checkpoint
			}
		})
	}
}

func BenchmarkRestoreCheckpoint(b *testing.B) {
	for _, fixture := range checkpointBenchmarkFixtures(b) {
		checkpoint, err := DecodeCheckpoint(bytes.NewReader(fixture.encoded))
		if err != nil {
			b.Fatalf("DecodeCheckpoint: %v", err)
		}
		b.Run(fixture.name, func(b *testing.B) {
			reportCheckpointBenchmarkMetrics(b, fixture)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				restored, err := RestoreCheckpoint(context.Background(), fixture.revision, checkpoint)
				if err != nil {
					b.Fatalf("RestoreCheckpoint: %v", err)
				}
				if benchmarkCheckpointSession != nil {
					_ = benchmarkCheckpointSession.Close()
				}
				benchmarkCheckpointSession = restored
			}
			b.StopTimer()
			if benchmarkCheckpointSession != nil {
				_ = benchmarkCheckpointSession.Close()
				benchmarkCheckpointSession = nil
			}
		})
	}
}

func checkpointBenchmarkFixtures(tb testing.TB) []checkpointBenchmarkFixture {
	tb.Helper()
	ctx := context.Background()
	fixtures := []checkpointBenchmarkFixture{
		checkpointBenchmarkEmpty(tb),
		checkpointBenchmarkCorpus(tb, ctx),
		checkpointBenchmarkLogical(tb, ctx),
		checkpointBenchmarkAggregate(tb),
		checkpointBenchmarkBackchain(tb, ctx),
	}
	for i := range fixtures {
		checkpoint, err := fixtures[i].session.Checkpoint(ctx)
		if err != nil {
			tb.Fatalf("%s Checkpoint: %v", fixtures[i].name, err)
		}
		var encoded bytes.Buffer
		if err := EncodeCheckpoint(&encoded, checkpoint); err != nil {
			tb.Fatalf("%s EncodeCheckpoint: %v", fixtures[i].name, err)
		}
		fixtures[i].encoded = append([]byte(nil), encoded.Bytes()...)
		tb.Cleanup(func() { _ = fixtures[i].session.Close() })
	}
	return fixtures
}

func checkpointBenchmarkEmpty(tb testing.TB) checkpointBenchmarkFixture {
	workspace := NewWorkspace()
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		tb.Fatalf("empty Compile: %v", err)
	}
	return checkpointBenchmarkFixture{
		name:     "empty",
		revision: revision,
		session:  mustSession(tb, revision, "checkpoint-benchmark-empty"),
	}
}

func checkpointBenchmarkCorpus(tb testing.TB, ctx context.Context) checkpointBenchmarkFixture {
	session := benchmarkClaimsTriageCorpusSession(tb, ctx, forkWhatIfCorpusClaims)
	return checkpointBenchmarkFixture{
		name:     "corpus-64",
		revision: session.revision,
		session:  session,
	}
}

func checkpointBenchmarkLogical(tb testing.TB, ctx context.Context) checkpointBenchmarkFixture {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(tb, false)
	session := mustSession(tb, revision, "checkpoint-benchmark-logical")
	for i := range 32 {
		if _, err := session.Assert(ctx, sourceKey, mustFields(tb, map[string]any{"id": fmt.Sprintf("source-%03d", i)})); err != nil {
			tb.Fatalf("logical Assert(%d): %v", i, err)
		}
	}
	if result, err := session.Run(ctx); err != nil || result.Fired != 64 {
		tb.Fatalf("logical Run = (%+v, %v), want 64 firings", result, err)
	}
	return checkpointBenchmarkFixture{name: "logical-32", revision: revision, session: session}
}

func checkpointBenchmarkAggregate(tb testing.TB) checkpointBenchmarkFixture {
	revision, initials := buildAggregateLifecycleRuleset(tb)
	base := initials[0]
	initials = make([]SessionInitialFact, 128)
	for i := range initials {
		initials[i] = SessionInitialFact{
			TemplateKey: base.TemplateKey,
			Fields:      mustFields(tb, map[string]any{"amount": int64(i + 1)}),
		}
	}
	session, err := NewSession(revision, WithSessionID("checkpoint-benchmark-aggregate"), WithInitialFacts(initials...))
	if err != nil {
		tb.Fatalf("aggregate NewSession: %v", err)
	}
	return checkpointBenchmarkFixture{name: "aggregate-128", revision: revision, session: session}
}

func checkpointBenchmarkBackchain(tb testing.TB, ctx context.Context) checkpointBenchmarkFixture {
	revision, requestKey := mustCompileBackchainRuntimeRuleset(tb)
	session := mustSession(tb, revision, "checkpoint-benchmark-backchain")
	for i := range 32 {
		if err := session.AssertTemplateValues(ctx, requestKey, newStringValue(fmt.Sprintf("request-%03d", i))); err != nil {
			tb.Fatalf("backchain AssertTemplateValues(%d): %v", i, err)
		}
	}
	if result, err := session.Run(ctx); err != nil || result.Fired != 64 {
		tb.Fatalf("backchain Run = (%+v, %v), want 64 firings", result, err)
	}
	return checkpointBenchmarkFixture{name: "backchain-32", revision: revision, session: session}
}

func reportCheckpointBenchmarkMetrics(b *testing.B, fixture checkpointBenchmarkFixture) {
	b.Helper()
	b.ReportMetric(float64(fixture.session.factCount()), "facts")
	b.ReportMetric(float64(len(fixture.encoded)), "checkpoint-bytes")
}
