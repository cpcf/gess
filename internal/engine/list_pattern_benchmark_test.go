package engine

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkListPatternFired int

type listPatternBenchmarkShape string

const (
	listPatternBenchmarkSegmentMiddle listPatternBenchmarkShape = "segment-middle"
	listPatternBenchmarkRestTail      listPatternBenchmarkShape = "rest-tail"
	listPatternBenchmarkFixedExact    listPatternBenchmarkShape = "fixed-exact"
)

type listPatternBenchmarkCase struct {
	shape     listPatternBenchmarkShape
	factCount int
}

type listPatternBenchmarkRevision struct {
	revision *Ruleset
	eventKey TemplateKey
}

func BenchmarkGessListPatternScaling(b *testing.B) {
	cases := []listPatternBenchmarkCase{
		{shape: listPatternBenchmarkSegmentMiddle, factCount: 1_000},
		{shape: listPatternBenchmarkSegmentMiddle, factCount: 10_000},
		{shape: listPatternBenchmarkSegmentMiddle, factCount: 50_000},
		{shape: listPatternBenchmarkRestTail, factCount: 1_000},
		{shape: listPatternBenchmarkRestTail, factCount: 10_000},
		{shape: listPatternBenchmarkRestTail, factCount: 50_000},
		{shape: listPatternBenchmarkFixedExact, factCount: 1_000},
		{shape: listPatternBenchmarkFixedExact, factCount: 10_000},
		{shape: listPatternBenchmarkFixedExact, factCount: 50_000},
	}

	for _, tc := range cases {
		hits := benchmarkListPatternExpectedHits(tc.shape, tc.factCount)
		name := fmt.Sprintf("%s/facts=%d/hits=%d/seed-run", tc.shape, tc.factCount, hits)
		b.Run(name, func(b *testing.B) {
			benchmarkListPatternSeedRun(b, tc)
		})
	}
}

func benchmarkListPatternSeedRun(b *testing.B, tc listPatternBenchmarkCase) {
	b.Helper()
	ctx := context.Background()
	compiled := benchmarkListPatternRevision(b, tc.shape)
	initials := benchmarkListPatternFacts(b, compiled.eventKey, tc.shape, tc.factCount)
	expectedHits := benchmarkListPatternExpectedHits(tc.shape, tc.factCount)

	session, err := NewSession(compiled.revision, WithInitialFacts(initials...))
	if err != nil {
		b.Fatalf("warmup NewSession: %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		b.Fatalf("warmup Run: %v", err)
	}
	if result.Fired != expectedHits {
		b.Fatalf("warmup fired = %d, want %d", result.Fired, expectedHits)
	}

	b.ReportAllocs()
	b.ReportMetric(float64(tc.factCount), "facts")
	b.ReportMetric(float64(expectedHits), "hits/run")
	b.ResetTimer()
	for b.Loop() {
		session, err := NewSession(compiled.revision, WithInitialFacts(initials...))
		if err != nil {
			b.Fatalf("NewSession: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Fired != expectedHits {
			b.Fatalf("fired = %d, want %d", result.Fired, expectedHits)
		}
		benchmarkListPatternFired = result.Fired
	}
}

func benchmarkListPatternRevision(tb testing.TB, shape listPatternBenchmarkShape) listPatternBenchmarkRevision {
	tb.Helper()
	workspace := NewWorkspace()
	event := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "lp-event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tags", Kind: ValueList, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{Name: "touch", Fn: func(ActionContext) error { return nil }})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "list-pattern",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{benchmarkListPatternSpec(tb, shape)}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "touch"}},
	})
	return listPatternBenchmarkRevision{
		revision: mustCompileWorkspace(tb, workspace),
		eventKey: event.Key(),
	}
}

func benchmarkListPatternSpec(tb testing.TB, shape listPatternBenchmarkShape) ListPatternSpec {
	tb.Helper()
	switch shape {
	case listPatternBenchmarkSegmentMiddle:
		return ListPattern(Path("tags"),
			ListElem(ConstExpr{Value: "vip"}),
			ListSegment("middle"),
			ListElem(ConstExpr{Value: "active"}),
		)
	case listPatternBenchmarkRestTail:
		return ListPattern(Path("tags"),
			ListElem(ConstExpr{Value: "vip"}),
			ListWildcard(),
			ListRestWildcard(),
		)
	case listPatternBenchmarkFixedExact:
		return ListPattern(Path("tags"),
			ListElem(ConstExpr{Value: "vip"}),
			ListElem(ConstExpr{Value: "active"}),
		)
	default:
		tb.Fatalf("unsupported list pattern benchmark shape %q", shape)
		return ListPattern(Path("tags"))
	}
}

func benchmarkListPatternFacts(tb testing.TB, eventKey TemplateKey, shape listPatternBenchmarkShape, count int) []SessionInitialFact {
	tb.Helper()
	facts := make([]SessionInitialFact, 0, count)
	for i := range count {
		facts = append(facts, SessionInitialFact{
			TemplateKey: eventKey,
			Fields: mustFields(tb, map[string]any{
				"id":   fmt.Sprintf("e-%05d", i),
				"tags": benchmarkListPatternTags(shape, i),
			}),
		})
	}
	return facts
}

func benchmarkListPatternTags(shape listPatternBenchmarkShape, index int) []any {
	switch shape {
	case listPatternBenchmarkSegmentMiddle:
		if index%2 != 0 {
			return []any{"standard", benchmarkListPatternTag(index), "active"}
		}
		tags := []any{"vip"}
		for j := range index % 5 {
			tags = append(tags, benchmarkListPatternTag(index+j))
		}
		return append(tags, "active")
	case listPatternBenchmarkRestTail:
		if index%3 == 0 {
			return []any{"vip"}
		}
		tags := []any{"vip", benchmarkListPatternTag(index)}
		for j := range index % 4 {
			tags = append(tags, benchmarkListPatternTag(index+j+1))
		}
		return tags
	case listPatternBenchmarkFixedExact:
		if index%5 == 0 {
			return []any{"vip", "active"}
		}
		if index%5 == 1 {
			return []any{"vip", benchmarkListPatternTag(index), "active"}
		}
		return []any{"standard", "active"}
	default:
		return nil
	}
}

func benchmarkListPatternExpectedHits(shape listPatternBenchmarkShape, count int) int {
	hits := 0
	for i := range count {
		switch shape {
		case listPatternBenchmarkSegmentMiddle:
			if i%2 == 0 {
				hits++
			}
		case listPatternBenchmarkRestTail:
			if i%3 != 0 {
				hits++
			}
		case listPatternBenchmarkFixedExact:
			if i%5 == 0 {
				hits++
			}
		}
	}
	return hits
}

func benchmarkListPatternTag(index int) string {
	return fmt.Sprintf("tag-%02d", index%17)
}
