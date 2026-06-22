package gess

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

var benchmarkAggregateScalingRunResult RunResult
var benchmarkAggregateScalingMutationResult RunResult

type aggregateScalingCase struct {
	streams        int
	itemsPerStream int
}

type aggregateScalingMutationCase struct {
	mode         string
	needsTarget  bool
	itemDelta    int
	summaryDelta int64
	run          func(testing.TB, context.Context, *Session, TemplateKey, aggregateScalingCase, FactID) RunResult
}

func aggregateScalingMutationCases() []aggregateScalingMutationCase {
	return []aggregateScalingMutationCase{
		{mode: "agenda-ready-assert", itemDelta: 1, summaryDelta: 1, run: runAggregateScalingSteadyAssert},
		{mode: "modify-input", needsTarget: true, summaryDelta: 1, run: runAggregateScalingSteadyModify},
		{mode: "retract-input", needsTarget: true, itemDelta: -1, summaryDelta: -1, run: runAggregateScalingSteadyRetract},
	}
}

func BenchmarkGessAggregateScalingSeedRun(b *testing.B) {
	cases := []aggregateScalingCase{
		{streams: 1, itemsPerStream: 128},
		{streams: 4, itemsPerStream: 512},
		{streams: 8, itemsPerStream: 1024},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("streams=%d/items=%d/rules=%d/final-facts=%d/fired=%d",
			tc.streams, tc.itemsPerStream, tc.ruleCount(), tc.finalFacts(), tc.firedCount())
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision, itemKey := mustCompileAggregateScalingRuleset(b, tc)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.streams), "streams")
			b.ReportMetric(float64(tc.itemsPerStream), "items/stream")
			b.ReportMetric(float64(tc.ruleCount()), "rules")
			b.ReportMetric(float64(tc.initialFacts()), "initial-facts")
			b.ReportMetric(float64(tc.finalFacts()), "final-facts")
			b.ReportMetric(float64(tc.firedCount()), "fired/run")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				session := mustSession(b, revision, SessionID(fmt.Sprintf("aggregate-scaling-benchmark-%d", i)))
				result := runAggregateScalingSeedRun(b, ctx, session, itemKey, tc)
				benchmarkAggregateScalingRunResult = result
			}
		})
	}
}

func BenchmarkGessAggregateScalingSteadyStateMutations(b *testing.B) {
	cases := []aggregateScalingCase{
		{streams: 1, itemsPerStream: 128},
		{streams: 4, itemsPerStream: 512},
		{streams: 8, itemsPerStream: 1024},
	}

	for _, tc := range cases {
		revision, itemKey := mustCompileAggregateScalingRuleset(b, tc)
		for _, mutation := range aggregateScalingMutationCases() {
			name := fmt.Sprintf("%s/streams=%d/items=%d/rules=%d",
				mutation.mode, tc.streams, tc.itemsPerStream, tc.ruleCount())
			b.Run(name, func(b *testing.B) {
				ctx := context.Background()
				b.ReportAllocs()
				b.ReportMetric(float64(tc.streams), "streams")
				b.ReportMetric(float64(tc.itemsPerStream), "items/stream")
				b.ReportMetric(float64(tc.ruleCount()), "rules")
				b.ReportMetric(1, "fired/run")
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					session := mustSession(b, revision, SessionID(fmt.Sprintf("aggregate-mutation-benchmark-%s-%d", mutation.mode, i)))
					seeded := runAggregateScalingSeedRun(b, ctx, session, itemKey, tc)
					if seeded.Fired != tc.firedCount() {
						b.Fatalf("seed fired = %d, want %d", seeded.Fired, tc.firedCount())
					}
					targetFact := FactID{}
					if mutation.needsTarget {
						targetFact = mustFindAggregateScalingItem(b, session, 0, 0)
					}
					b.StartTimer()
					result := mutation.run(b, ctx, session, itemKey, tc, targetFact)
					b.StopTimer()
					benchmarkAggregateScalingMutationResult = result
				}
			})
		}
	}
}

func TestAggregateScalingSeedRunHarness(t *testing.T) {
	if os.Getenv("GESS_AGGREGATE_SCALING_RUNNER") == "" {
		t.Skip("set GESS_AGGREGATE_SCALING_RUNNER=1 to run the comparable aggregate scaling harness")
	}

	iterations := aggregateScalingHarnessEnvInt(t, "GESS_AGGREGATE_SCALING_ITERATIONS", 3)
	warmup := aggregateScalingHarnessEnvInt(t, "GESS_AGGREGATE_SCALING_WARMUP", 1)
	if iterations <= 0 {
		t.Fatalf("GESS_AGGREGATE_SCALING_ITERATIONS must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("GESS_AGGREGATE_SCALING_WARMUP must be non-negative, got %d", warmup)
	}
	mode := strings.TrimSpace(os.Getenv("GESS_AGGREGATE_SCALING_MODE"))
	if mode == "" {
		mode = "seed-run"
	}

	cases := []aggregateScalingCase{
		{streams: 1, itemsPerStream: 128},
		{streams: 4, itemsPerStream: 512},
		{streams: 8, itemsPerStream: 1024},
	}
	streamsRaw, streamsSet := os.LookupEnv("GESS_AGGREGATE_SCALING_STREAMS")
	itemsRaw, itemsSet := os.LookupEnv("GESS_AGGREGATE_SCALING_ITEMS")
	if streamsSet || itemsSet {
		if !streamsSet || !itemsSet {
			t.Fatal("GESS_AGGREGATE_SCALING_STREAMS and GESS_AGGREGATE_SCALING_ITEMS must be provided together")
		}
		cases = []aggregateScalingCase{{
			streams:        parseAggregateScalingHarnessInt(t, "GESS_AGGREGATE_SCALING_STREAMS", streamsRaw),
			itemsPerStream: parseAggregateScalingHarnessInt(t, "GESS_AGGREGATE_SCALING_ITEMS", itemsRaw),
		}}
	}

	for _, tc := range cases {
		runAggregateScalingHarnessCase(t, tc, iterations, warmup, mode)
	}
}

func runAggregateScalingHarnessCase(t *testing.T, tc aggregateScalingCase, iterations, warmup int, mode string) {
	t.Helper()

	if mode == "all" {
		runAggregateScalingSeedRunHarnessCase(t, tc, iterations, warmup)
		for _, mutation := range aggregateScalingMutationCases() {
			runAggregateScalingMutationHarnessCase(t, tc, iterations, warmup, mutation)
		}
		return
	}
	if mode == "seed-run" {
		runAggregateScalingSeedRunHarnessCase(t, tc, iterations, warmup)
		return
	}
	for _, mutation := range aggregateScalingMutationCases() {
		if mutation.mode == mode {
			runAggregateScalingMutationHarnessCase(t, tc, iterations, warmup, mutation)
			return
		}
	}
	t.Fatalf("unsupported GESS_AGGREGATE_SCALING_MODE %q", mode)
}

func runAggregateScalingSeedRunHarnessCase(t *testing.T, tc aggregateScalingCase, iterations, warmup int) {
	t.Helper()

	ctx := context.Background()
	revision, itemKey := mustCompileAggregateScalingRuleset(t, tc)
	for range warmup {
		session := mustSession(t, revision, "aggregate-scaling-warmup-session")
		result := runAggregateScalingSeedRun(t, ctx, session, itemKey, tc)
		validateAggregateScalingSession(t, session, result, tc, "warmup")
	}

	sessions := make([]*Session, iterations)
	for i := range sessions {
		sessions[i] = mustSession(t, revision, SessionID(fmt.Sprintf("aggregate-scaling-benchmark-session-%d", i)))
	}
	results := make([]RunResult, iterations)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i, session := range sessions {
		results[i] = runAggregateScalingSeedRun(t, ctx, session, itemKey, tc)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	for i, session := range sessions {
		validateAggregateScalingSession(t, session, results[i], tc, "benchmark")
	}

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|aggregate-scaling|seed-run|streams=%d|items=%d|rules=%d|initial-facts=%d|final-facts=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f\n",
		tc.streams, tc.itemsPerStream, tc.ruleCount(), tc.initialFacts(), tc.finalFacts(), tc.firedCount(), iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
	)
}

func runAggregateScalingMutationHarnessCase(t *testing.T, tc aggregateScalingCase, iterations, warmup int, mutation aggregateScalingMutationCase) {
	t.Helper()

	ctx := context.Background()
	revision, itemKey := mustCompileAggregateScalingRuleset(t, tc)
	for i := range warmup {
		session, targetFact := prepareAggregateScalingMutationSession(t, ctx, revision, itemKey, tc, mutation, SessionID(fmt.Sprintf("aggregate-scaling-%s-warmup-session-%d", mutation.mode, i)))
		result := mutation.run(t, ctx, session, itemKey, tc, targetFact)
		validateAggregateScalingMutationSession(t, session, result, tc, mutation, "warmup")
	}

	type preparedSession struct {
		session    *Session
		targetFact FactID
		result     RunResult
	}
	prepared := make([]preparedSession, iterations)
	for i := range prepared {
		session, targetFact := prepareAggregateScalingMutationSession(t, ctx, revision, itemKey, tc, mutation, SessionID(fmt.Sprintf("aggregate-scaling-%s-benchmark-session-%d", mutation.mode, i)))
		prepared[i] = preparedSession{session: session, targetFact: targetFact}
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	for i := range prepared {
		prepared[i].result = mutation.run(t, ctx, prepared[i].session, itemKey, tc, prepared[i].targetFact)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	for i := range prepared {
		validateAggregateScalingMutationSession(t, prepared[i].session, prepared[i].result, tc, mutation, "benchmark")
	}

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	fmt.Printf(
		"GESS_RUNNER|aggregate-scaling|%s|streams=%d|items=%d|rules=%d|seeded-facts=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|alloc-bytes/op=%.0f|allocs/op=%.0f\n",
		mutation.mode, tc.streams, tc.itemsPerStream, tc.ruleCount(), tc.finalFacts(), 1, iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations),
		float64(allocBytes)/float64(iterations),
		float64(allocs)/float64(iterations),
	)
}

func prepareAggregateScalingMutationSession(t testing.TB, ctx context.Context, revision *Ruleset, itemKey TemplateKey, tc aggregateScalingCase, mutation aggregateScalingMutationCase, sessionID SessionID) (*Session, FactID) {
	t.Helper()

	session := mustSession(t, revision, sessionID)
	seeded := runAggregateScalingSeedRun(t, ctx, session, itemKey, tc)
	if seeded.Fired != tc.firedCount() {
		t.Fatalf("seed fired = %d, want %d", seeded.Fired, tc.firedCount())
	}
	targetFact := FactID{}
	if mutation.needsTarget {
		targetFact = mustFindAggregateScalingItem(t, session, 0, 0)
	}
	return session, targetFact
}

func mustCompileAggregateScalingRuleset(t testing.TB, tc aggregateScalingCase) (*Ruleset, TemplateKey) {
	t.Helper()

	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "agg-item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	summary := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "agg-summary",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "total", Kind: ValueInt, Required: true},
		},
	})

	for stream := range tc.streams {
		actionName := fmt.Sprintf("record-aggregate-stream-%03d", stream)
		mustAddAction(t, workspace, ActionSpec{
			Name: actionName,
			Fn: func(ctx ActionContext) error {
				total, ok := ctx.BindingValue("total")
				if !ok {
					return fmt.Errorf("missing total binding")
				}
				_, err := ctx.AssertTemplate(summary.Key(), Fields{
					"stream": newIntValue(int64(stream)),
					"total":  total,
				})
				return err
			},
		})
		mustAddRule(t, workspace, RuleSpec{
			Name: fmt.Sprintf("aggregate-stream-%03d", stream),
			ConditionTree: Accumulate(
				Match{
					Binding:     "item",
					TemplateKey: item.Key(),
					FieldConstraints: []FieldConstraintSpec{
						{Field: "stream", Operator: FieldConstraintEqual, Value: stream},
					},
				},
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
			Actions: []RuleActionSpec{{Name: actionName}},
		})
	}

	return mustCompileWorkspace(t, workspace), item.Key()
}

func runAggregateScalingSeedRun(t testing.TB, ctx context.Context, session *Session, itemKey TemplateKey, tc aggregateScalingCase) RunResult {
	t.Helper()

	for stream := range tc.streams {
		streamValue := newIntValue(int64(stream))
		for id := range tc.itemsPerStream {
			_, err := session.AssertTemplate(ctx, itemKey, Fields{
				"stream": streamValue,
				"id":     newIntValue(int64(id)),
				"amount": newIntValue(int64(aggregateScalingAmount(id))),
			})
			if err != nil {
				t.Fatalf("AssertTemplate(item): %v", err)
			}
		}
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	validateAggregateScalingSession(t, session, result, tc, "benchmark")
	return result
}

func runAggregateScalingSteadyAssert(t testing.TB, ctx context.Context, session *Session, itemKey TemplateKey, tc aggregateScalingCase, targetFact FactID) RunResult {
	t.Helper()

	_, err := session.AssertTemplate(ctx, itemKey, Fields{
		"stream": newIntValue(0),
		"id":     newIntValue(int64(tc.itemsPerStream)),
		"amount": newIntValue(1),
	})
	if err != nil {
		t.Fatalf("steady assert: %v", err)
	}
	return runAggregateScalingSteadyMutation(t, ctx, session, "agenda-ready-assert")
}

func runAggregateScalingSteadyModify(t testing.TB, ctx context.Context, session *Session, itemKey TemplateKey, tc aggregateScalingCase, targetFact FactID) RunResult {
	t.Helper()

	_, err := session.Modify(ctx, targetFact, FactPatch{Set: Fields{"amount": newIntValue(int64(aggregateScalingAmount(0) + 1))}})
	if err != nil {
		t.Fatalf("steady modify: %v", err)
	}
	return runAggregateScalingSteadyMutation(t, ctx, session, "modify-input")
}

func runAggregateScalingSteadyRetract(t testing.TB, ctx context.Context, session *Session, itemKey TemplateKey, tc aggregateScalingCase, targetFact FactID) RunResult {
	t.Helper()

	if _, err := session.Retract(ctx, targetFact); err != nil {
		t.Fatalf("steady retract: %v", err)
	}
	return runAggregateScalingSteadyMutation(t, ctx, session, "retract-input")
}

func runAggregateScalingSteadyMutation(t testing.TB, ctx context.Context, session *Session, phase string) RunResult {
	t.Helper()

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("%s Run: %v", phase, err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("%s run result = (%v, %d), want (%v, 1)", phase, result.Status, result.Fired, RunCompleted)
	}
	return result
}

func mustFindAggregateScalingItem(t testing.TB, session *Session, stream, id int) FactID {
	t.Helper()

	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, fact := range snapshot.FactsByTemplateKey(TemplateKey("agg-item")) {
		streamValue, ok := fact.Field("stream")
		if !ok {
			continue
		}
		idValue, ok := fact.Field("id")
		if !ok {
			continue
		}
		streamInt, streamOK := streamValue.AsInt64()
		idInt, idOK := idValue.AsInt64()
		if streamOK && idOK && int(streamInt) == stream && int(idInt) == id {
			return fact.ID()
		}
	}
	t.Fatalf("missing aggregate item stream=%d id=%d", stream, id)
	return FactID{}
}

func validateAggregateScalingMutationSession(t testing.TB, session *Session, result RunResult, tc aggregateScalingCase, mutation aggregateScalingMutationCase, phase string) {
	t.Helper()

	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("%s %s run result = (%v, %d), want (%v, 1)", phase, mutation.mode, result.Status, result.Fired, RunCompleted)
	}
	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("%s %s snapshot: %v", phase, mutation.mode, err)
	}

	wantItems := tc.initialFacts() + mutation.itemDelta
	wantSummaries := tc.streams + 1
	itemCount := len(snapshot.FactsByTemplateKey(TemplateKey("agg-item")))
	summaryCount := 0
	summaryTotal := int64(0)
	for _, fact := range snapshot.FactsByTemplateKey(TemplateKey("agg-summary")) {
		totalValue, ok := fact.Field("total")
		if !ok {
			t.Fatalf("%s %s summary missing total", phase, mutation.mode)
		}
		total, ok := totalValue.AsInt64()
		if !ok {
			t.Fatalf("%s %s summary total = %v, want int", phase, mutation.mode, totalValue)
		}
		summaryCount++
		summaryTotal += total
	}
	if itemCount != wantItems || summaryCount != wantSummaries {
		t.Fatalf("%s %s fact mix = item:%d summary:%d, want item:%d summary:%d",
			phase, mutation.mode, itemCount, summaryCount, wantItems, wantSummaries)
	}
	if got, want := snapshot.Len(), wantItems+wantSummaries; got != want {
		t.Fatalf("%s %s final fact count = %d, want %d", phase, mutation.mode, got, want)
	}
	wantSummaryTotal := int64((tc.streams+1)*tc.expectedTotalPerStream()) + mutation.summaryDelta
	if summaryTotal != wantSummaryTotal {
		t.Fatalf("%s %s summary total = %d, want %d", phase, mutation.mode, summaryTotal, wantSummaryTotal)
	}
}

func validateAggregateScalingSession(t testing.TB, session *Session, result RunResult, tc aggregateScalingCase, phase string) {
	t.Helper()

	if result.Status != RunCompleted || result.Fired != tc.firedCount() {
		t.Fatalf("%s run result = (%v, %d), want (%v, %d)", phase, result.Status, result.Fired, RunCompleted, tc.firedCount())
	}
	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("%s snapshot: %v", phase, err)
	}
	if got := snapshot.Len(); got != tc.finalFacts() {
		t.Fatalf("%s final fact count = %d, want %d", phase, got, tc.finalFacts())
	}

	summaryTotals := make(map[int]int64, tc.streams)
	for _, fact := range snapshot.FactsByTemplateKey(TemplateKey("agg-summary")) {
		streamValue, ok := fact.Field("stream")
		if !ok {
			t.Fatalf("%s summary missing stream", phase)
		}
		totalValue, ok := fact.Field("total")
		if !ok {
			t.Fatalf("%s summary missing total", phase)
		}
		stream, ok := streamValue.AsInt64()
		if !ok {
			t.Fatalf("%s summary stream = %v, want int", phase, streamValue)
		}
		total, ok := totalValue.AsInt64()
		if !ok {
			t.Fatalf("%s summary total = %v, want int", phase, totalValue)
		}
		summaryTotals[int(stream)] = total
	}
	if len(summaryTotals) != tc.streams {
		t.Fatalf("%s summaries = %d, want %d", phase, len(summaryTotals), tc.streams)
	}
	wantTotal := int64(tc.expectedTotalPerStream())
	for stream := range tc.streams {
		if got := summaryTotals[stream]; got != wantTotal {
			t.Fatalf("%s summary stream %d total = %d, want %d", phase, stream, got, wantTotal)
		}
	}
}

func (tc aggregateScalingCase) ruleCount() int {
	return tc.streams
}

func (tc aggregateScalingCase) initialFacts() int {
	return tc.streams * tc.itemsPerStream
}

func (tc aggregateScalingCase) finalFacts() int {
	return tc.initialFacts() + tc.streams
}

func (tc aggregateScalingCase) firedCount() int {
	return tc.streams
}

func (tc aggregateScalingCase) expectedTotalPerStream() int {
	total := 0
	for id := range tc.itemsPerStream {
		total += aggregateScalingAmount(id)
	}
	return total
}

func aggregateScalingAmount(id int) int {
	return id%10 + 1
}

func aggregateScalingHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	return parseAggregateScalingHarnessInt(t, name, raw)
}

func parseAggregateScalingHarnessInt(t testing.TB, name, raw string) int {
	t.Helper()

	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
