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

const claimsTriageMutationTargetClaimID = "CL-001"

func TestClaimsTriageComparableHarness(t *testing.T) {
	if os.Getenv("GESS_CLAIMS_TRIAGE_RUNNER") == "" {
		t.Skip("set GESS_CLAIMS_TRIAGE_RUNNER=1 to run the comparable claims triage harness")
	}

	iterations := claimsTriageHarnessEnvInt(t, "GESS_CLAIMS_TRIAGE_ITERATIONS", 10_000)
	warmup := claimsTriageHarnessEnvInt(t, "GESS_CLAIMS_TRIAGE_WARMUP", 1_000)
	factCount := claimsTriageHarnessEnvInt(t, "GESS_CLAIMS_TRIAGE_FACT_COUNT", claimsTriageBenchmarkFactCount)
	mode := strings.TrimSpace(os.Getenv("GESS_CLAIMS_TRIAGE_MODE"))
	if mode == "" {
		mode = "reset-run"
	}
	if iterations <= 0 {
		t.Fatalf("GESS_CLAIMS_TRIAGE_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_CLAIMS_TRIAGE_WARMUP must be non-negative, got %d", warmup)
	}

	switch mode {
	case "reset-run":
		runClaimsTriageResetRunHarness(t, factCount, iterations, warmup)
	case "mutation-cycle":
		runClaimsTriageMutationCycleHarness(t, factCount, iterations, warmup)
	default:
		t.Fatalf("unsupported GESS_CLAIMS_TRIAGE_MODE %q", mode)
	}
}

func runClaimsTriageResetRunHarness(t *testing.T, factCount, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision := mustCompileClaimsTriageRuleset(t, nil)
	initials := claimsTriageInitialFacts(t, factCount)
	expectedFired := claimsTriageFiredCount(factCount)
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	for range warmup {
		if _, err := session.Reset(ctx); err != nil {
			t.Fatalf("warmup Reset: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("warmup Run: %v", err)
		}
		validateClaimsTriageResetRun(t, session, result, factCount, expectedFired, "warmup")
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var elapsed int64
	for range iterations {
		start := time.Now()
		if _, err := session.Reset(ctx); err != nil {
			t.Fatalf("benchmark Reset: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("benchmark Run: %v", err)
		}
		elapsed += time.Since(start).Nanoseconds()
		validateClaimsTriageResetRun(t, session, result, factCount, expectedFired, "benchmark")
	}
	runtime.ReadMemStats(&after)

	printClaimsTriageRunnerRow("reset-run", factCount, factCount*4, expectedFired, iterations, warmup, time.Duration(elapsed), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
}

func runClaimsTriageMutationCycleHarness(t *testing.T, factCount, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision := mustCompileClaimsTriageRuleset(t, nil)
	expectedInitialFired := claimsTriageFiredCount(factCount)
	expectedFinalFacts := factCount*4 + expectedInitialFired + 1

	for range warmup {
		session, targetSignal := prepareClaimsTriageMutationSession(t, ctx, revision, factCount, expectedInitialFired)
		result := runClaimsTriageSignalFraudMutation(t, ctx, session, targetSignal)
		validateClaimsTriageMutation(t, session, result, expectedFinalFacts, "warmup")
	}

	var allocBytes uint64
	var allocs uint64
	var elapsed int64
	for range iterations {
		session, targetSignal := prepareClaimsTriageMutationSession(t, ctx, revision, factCount, expectedInitialFired)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		result := runClaimsTriageSignalFraudMutation(t, ctx, session, targetSignal)
		elapsed += time.Since(start).Nanoseconds()
		runtime.ReadMemStats(&after)
		allocBytes += after.TotalAlloc - before.TotalAlloc
		allocs += after.Mallocs - before.Mallocs
		validateClaimsTriageMutation(t, session, result, expectedFinalFacts, "benchmark")
	}

	printClaimsTriageRunnerRow("mutation-cycle", factCount, factCount*4, 1, iterations, warmup, time.Duration(elapsed), allocBytes, allocs)
}

func prepareClaimsTriageMutationSession(t testing.TB, ctx context.Context, revision *Ruleset, factCount, expectedFired int) (*Session, FactID) {
	t.Helper()

	session := mustSession(t, revision, "claims-triage-mutation-cycle")
	var targetSignal FactID
	for _, fact := range claimsTriageInitialFacts(t, factCount) {
		inserted, err := session.Assert(ctx, fact.TemplateKey, fact.Fields)
		if err != nil {
			t.Fatalf("Assert(%s): %v", fact.TemplateKey, err)
		}
		if fact.TemplateKey == "signal" && claimsTriageInitialFactStringField(fact, "claim-id") == claimsTriageMutationTargetClaimID {
			targetSignal = inserted.Fact.ID()
		}
	}
	if targetSignal.IsZero() {
		t.Fatalf("target signal for claim %s not found", claimsTriageMutationTargetClaimID)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	validateClaimsTriageResetRun(t, session, result, factCount, expectedFired, "initial")
	return session, targetSignal
}

func runClaimsTriageSignalFraudMutation(t testing.TB, ctx context.Context, session *Session, targetSignal FactID) RunResult {
	t.Helper()

	if _, err := session.Modify(ctx, targetSignal, FactPatch{Set: mustFields(t, map[string]any{
		"kind":  "fraud",
		"score": 90,
	})}); err != nil {
		t.Fatalf("Modify(signal): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run after signal mutation: %v", err)
	}
	return result
}

func validateClaimsTriageResetRun(t testing.TB, session *Session, result RunResult, factCount, expectedFired int, phase string) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != expectedFired {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, expectedFired)
	}
	expectedFacts := factCount*4 + expectedFired
	if got := session.factCount(); got != expectedFacts {
		t.Fatalf("%s final facts = %d, want %d", phase, got, expectedFacts)
	}
}

func validateClaimsTriageMutation(t testing.TB, session *Session, result RunResult, expectedFinalFacts int, phase string) {
	t.Helper()
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("%s mutation run result = (%v, %d), want (%v, 1)", phase, result.Status, result.Fired, RunCompleted)
	}
	if got := session.factCount(); got != expectedFinalFacts {
		t.Fatalf("%s mutation final facts = %d, want %d", phase, got, expectedFinalFacts)
	}
}

func claimsTriageInitialFactStringField(fact SessionInitialFact, field string) string {
	value, ok := fact.Fields[field]
	if !ok {
		return ""
	}
	text, ok := value.AsString()
	if !ok {
		return ""
	}
	return text
}

func printClaimsTriageRunnerRow(mode string, claimCount, initialFacts, fired, iterations, warmup int, elapsed time.Duration, allocBytes, allocs uint64) {
	fmt.Printf(
		"GESS_RUNNER|claims-triage|%s|claims=%d|initial-facts=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f\n",
		mode,
		claimCount,
		initialFacts,
		fired,
		iterations,
		warmup,
		elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
	)
}

func claimsTriageHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
