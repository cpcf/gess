package engine

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkAuthoredOrderRunResult RunResult
var benchmarkAuthoredOrderQueryRows []QueryRow

type authoredOrderBenchmarkShape string

const (
	authoredOrderSelectiveFirst         authoredOrderBenchmarkShape = "selective-first"
	authoredOrderBroadFirst             authoredOrderBenchmarkShape = "broad-first"
	authoredOrderJoinChain              authoredOrderBenchmarkShape = "join-chain"
	authoredOrderStarJoin               authoredOrderBenchmarkShape = "star-join"
	authoredOrderNegationAfterSelective authoredOrderBenchmarkShape = "negation-after-selective"
	authoredOrderNegationAfterBroad     authoredOrderBenchmarkShape = "negation-after-broad"
)

type authoredOrderBenchmarkCase struct {
	shape authoredOrderBenchmarkShape
	items int
}

type authoredOrderBenchmarkTemplates struct {
	root   TemplateKey
	event  TemplateKey
	detail TemplateKey
	tag    TemplateKey
	block  TemplateKey
}

func BenchmarkGessAuthoredOrderRuleScaling(b *testing.B) {
	for _, tc := range authoredOrderBenchmarkCases() {
		name := fmt.Sprintf("%s/items=%d/matches=%d", tc.shape, tc.items, authoredOrderExpectedMatches(tc.shape, tc.items))
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision, templates := mustCompileAuthoredOrderRuleBenchmark(b, tc.shape)
			expected := authoredOrderExpectedMatches(tc.shape, tc.items)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.items), "items")
			b.ReportMetric(float64(authoredOrderRootCount), "roots")
			b.ReportMetric(float64(authoredOrderInitialFacts(tc.items)), "initial-facts")
			b.ReportMetric(float64(expected), "matches/run")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				session := mustSession(b, revision, SessionID(fmt.Sprintf("authored-order-rule-%s-%d", tc.shape, i)))
				seedAuthoredOrderFacts(b, ctx, session, templates, tc.items)
				result, err := session.Run(ctx)
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				if result.Status != RunCompleted || result.Fired != expected {
					b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, expected)
				}
				benchmarkAuthoredOrderRunResult = result
			}
		})
	}
}

func BenchmarkGraphTerminalQueryAuthoredOrderScaling(b *testing.B) {
	for _, tc := range authoredOrderBenchmarkCases() {
		name := fmt.Sprintf("%s/items=%d/rows=%d", tc.shape, tc.items, authoredOrderExpectedMatches(tc.shape, tc.items))
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			revision, templates, queryName := mustCompileAuthoredOrderQueryBenchmark(b, tc.shape)
			session := mustSession(b, revision, SessionID(fmt.Sprintf("authored-order-query-%s", tc.shape)))
			seedAuthoredOrderFacts(b, ctx, session, templates, tc.items)
			expected := authoredOrderExpectedMatches(tc.shape, tc.items)
			rows, err := session.QueryAll(ctx, queryName, nil)
			if err != nil {
				b.Fatalf("warmup QueryAll: %v", err)
			}
			if len(rows) != expected {
				b.Fatalf("warmup rows = %d, want %d", len(rows), expected)
			}

			b.ReportAllocs()
			b.ReportMetric(float64(tc.items), "items")
			b.ReportMetric(float64(authoredOrderRootCount), "roots")
			b.ReportMetric(float64(authoredOrderInitialFacts(tc.items)), "initial-facts")
			b.ReportMetric(float64(expected), "rows/query")
			b.ResetTimer()
			for b.Loop() {
				rows, err := session.QueryAll(ctx, queryName, nil)
				if err != nil {
					b.Fatalf("QueryAll: %v", err)
				}
				if len(rows) != expected {
					b.Fatalf("rows = %d, want %d", len(rows), expected)
				}
				benchmarkAuthoredOrderQueryRows = rows
			}
		})
	}
}

func TestAuthoredOrderScalingBenchmarksValidateExpectedCounts(t *testing.T) {
	ctx := context.Background()
	for _, shape := range authoredOrderBenchmarkShapes() {
		t.Run("rule/"+string(shape), func(t *testing.T) {
			revision, templates := mustCompileAuthoredOrderRuleBenchmark(t, shape)
			session := mustSession(t, revision, SessionID("authored-order-rule-validation-"+string(shape)))
			seedAuthoredOrderFacts(t, ctx, session, templates, 256)
			result, err := session.Run(ctx)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got, want := result.Fired, authoredOrderExpectedMatches(shape, 256); got != want {
				t.Fatalf("fired = %d, want %d", got, want)
			}
		})
		t.Run("query/"+string(shape), func(t *testing.T) {
			revision, templates, queryName := mustCompileAuthoredOrderQueryBenchmark(t, shape)
			session := mustSession(t, revision, SessionID("authored-order-query-validation-"+string(shape)))
			seedAuthoredOrderFacts(t, ctx, session, templates, 256)
			rows, err := session.QueryAll(ctx, queryName, nil)
			if err != nil {
				t.Fatalf("QueryAll: %v", err)
			}
			if got, want := len(rows), authoredOrderExpectedMatches(shape, 256); got != want {
				t.Fatalf("rows = %d, want %d", got, want)
			}
		})
	}
}

func authoredOrderBenchmarkCases() []authoredOrderBenchmarkCase {
	shapes := authoredOrderBenchmarkShapes()
	cases := make([]authoredOrderBenchmarkCase, 0, len(shapes)*2)
	for _, shape := range shapes {
		cases = append(cases,
			authoredOrderBenchmarkCase{shape: shape, items: 1_000},
			authoredOrderBenchmarkCase{shape: shape, items: 10_000},
		)
	}
	return cases
}

func authoredOrderBenchmarkShapes() []authoredOrderBenchmarkShape {
	return []authoredOrderBenchmarkShape{
		authoredOrderSelectiveFirst,
		authoredOrderBroadFirst,
		authoredOrderJoinChain,
		authoredOrderStarJoin,
		authoredOrderNegationAfterSelective,
		authoredOrderNegationAfterBroad,
	}
}

func mustCompileAuthoredOrderRuleBenchmark(t testing.TB, shape authoredOrderBenchmarkShape) (*Ruleset, authoredOrderBenchmarkTemplates) {
	t.Helper()
	workspace, templates := authoredOrderWorkspace(t)
	mustAddAction(t, workspace, ActionSpec{
		Name: "record-authored-order-match",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:          "authored-order-" + string(shape),
		ConditionTree: authoredOrderConditionTree(shape, templates),
		Actions:       []RuleActionSpec{{Name: "record-authored-order-match"}},
	})
	return mustCompileWorkspace(t, workspace), templates
}

func mustCompileAuthoredOrderQueryBenchmark(t testing.TB, shape authoredOrderBenchmarkShape) (*Ruleset, authoredOrderBenchmarkTemplates, string) {
	t.Helper()
	workspace, templates := authoredOrderWorkspace(t)
	queryName := "authored-order-" + string(shape)
	if err := workspace.AddQuery(QuerySpec{
		Name:          queryName,
		ConditionTree: authoredOrderConditionTree(shape, templates),
		Returns: []QueryReturnSpec{
			ReturnValue("event", BindingFieldExpr{Binding: "event", Field: "id"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	return mustCompileWorkspace(t, workspace), templates, queryName
}

func authoredOrderWorkspace(t testing.TB) (*Workspace, authoredOrderBenchmarkTemplates) {
	t.Helper()
	workspace := NewWorkspace()
	root := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order-root",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order-event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "root", Kind: ValueInt, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	detail := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order-detail",
		Fields: []FieldSpec{
			{Name: "event", Kind: ValueInt, Required: true},
			{Name: "code", Kind: ValueString, Required: true},
		},
	})
	tag := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order-tag",
		Fields: []FieldSpec{
			{Name: "event", Kind: ValueInt, Required: true},
			{Name: "label", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "order-block",
		Fields: []FieldSpec{
			{Name: "event", Kind: ValueInt, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	return workspace, authoredOrderBenchmarkTemplates{
		root:   root.Key(),
		event:  event.Key(),
		detail: detail.Key(),
		tag:    tag.Key(),
		block:  block.Key(),
	}
}

func authoredOrderConditionTree(shape authoredOrderBenchmarkShape, templates authoredOrderBenchmarkTemplates) ConditionSpec {
	switch shape {
	case authoredOrderSelectiveFirst:
		return And{Conditions: []ConditionSpec{
			authoredOrderRootMatch(templates),
			authoredOrderEventJoinedToRootMatch(templates),
		}}
	case authoredOrderBroadFirst:
		return And{Conditions: []ConditionSpec{
			authoredOrderEventMatch(templates),
			authoredOrderRootJoinedToEventMatch(templates),
		}}
	case authoredOrderJoinChain:
		return And{Conditions: []ConditionSpec{
			authoredOrderRootMatch(templates),
			authoredOrderEventJoinedToRootMatch(templates),
			authoredOrderDetailJoinedToEventMatch(templates),
		}}
	case authoredOrderStarJoin:
		return And{Conditions: []ConditionSpec{
			authoredOrderEventMatch(templates),
			authoredOrderRootJoinedToEventMatch(templates),
			authoredOrderDetailJoinedToEventMatch(templates),
			authoredOrderTagJoinedToEventMatch(templates),
		}}
	case authoredOrderNegationAfterSelective:
		return And{Conditions: []ConditionSpec{
			authoredOrderRootMatch(templates),
			authoredOrderEventJoinedToRootMatch(templates),
			authoredOrderBlockNotJoinedToEvent(templates),
		}}
	case authoredOrderNegationAfterBroad:
		return And{Conditions: []ConditionSpec{
			authoredOrderEventMatch(templates),
			authoredOrderRootJoinedToEventMatch(templates),
			authoredOrderBlockNotJoinedToEvent(templates),
		}}
	default:
		panic(fmt.Sprintf("unsupported authored-order benchmark shape %q", shape))
	}
}

func authoredOrderRootMatch(templates authoredOrderBenchmarkTemplates) Match {
	return Match{
		Binding: "root",

		FieldConstraints: []FieldConstraintSpec{
			{Field: "group", Operator: FieldConstraintEqual, Value: "target"},
			{Field: "active", Operator: FieldConstraintEqual, Value: true},
		}, Target: TemplateKeyFact(templates.root),
	}
}

func authoredOrderRootJoinedToEventMatch(templates authoredOrderBenchmarkTemplates) Match {
	match := authoredOrderRootMatch(templates)
	match.JoinConstraints = []JoinConstraintSpec{
		{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "root"}},
	}
	return match
}

func authoredOrderEventMatch(templates authoredOrderBenchmarkTemplates) Match {
	return Match{
		Binding: "event",

		FieldConstraints: []FieldConstraintSpec{
			{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: authoredOrderScoreThreshold},
		}, Target: TemplateKeyFact(templates.event),
	}
}

func authoredOrderEventJoinedToRootMatch(templates authoredOrderBenchmarkTemplates) Match {
	match := authoredOrderEventMatch(templates)
	match.JoinConstraints = []JoinConstraintSpec{
		{Field: "root", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "id"}},
	}
	return match
}

func authoredOrderDetailJoinedToEventMatch(templates authoredOrderBenchmarkTemplates) Match {
	return Match{
		Binding: "detail",

		FieldConstraints: []FieldConstraintSpec{
			{Field: "code", Operator: FieldConstraintEqual, Value: "selected"},
		},
		JoinConstraints: []JoinConstraintSpec{
			{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
		}, Target: TemplateKeyFact(templates.detail),
	}
}

func authoredOrderTagJoinedToEventMatch(templates authoredOrderBenchmarkTemplates) Match {
	return Match{
		Binding: "tag",

		FieldConstraints: []FieldConstraintSpec{
			{Field: "label", Operator: FieldConstraintEqual, Value: "priority"},
		},
		JoinConstraints: []JoinConstraintSpec{
			{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
		}, Target: TemplateKeyFact(templates.tag),
	}
}

func authoredOrderBlockNotJoinedToEvent(templates authoredOrderBenchmarkTemplates) Not {
	return Not{Condition: Match{
		Binding: "block",

		FieldConstraints: []FieldConstraintSpec{
			{Field: "active", Operator: FieldConstraintEqual, Value: true},
		},
		JoinConstraints: []JoinConstraintSpec{
			{Field: "event", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "event", Field: "id"}},
		}, Target: TemplateKeyFact(templates.block),
	}}
}

func seedAuthoredOrderFacts(t testing.TB, ctx context.Context, session *Session, templates authoredOrderBenchmarkTemplates, items int) {
	t.Helper()
	for id := range authoredOrderRootCount {
		group := "other"
		if authoredOrderRootSelected(id) {
			group = "target"
		}
		if _, err := session.AssertTemplate(ctx, templates.root, Fields{
			"id":     newIntValue(int64(id)),
			"group":  newStringValue(group),
			"active": newBoolValue(true),
		}); err != nil {
			t.Fatalf("AssertTemplate(root): %v", err)
		}
	}
	for id := range items {
		if _, err := session.AssertTemplate(ctx, templates.event, Fields{
			"id":    newIntValue(int64(id)),
			"root":  newIntValue(int64(id % authoredOrderRootCount)),
			"score": newIntValue(int64(authoredOrderScore(id))),
		}); err != nil {
			t.Fatalf("AssertTemplate(event): %v", err)
		}
		if _, err := session.AssertTemplate(ctx, templates.detail, Fields{
			"event": newIntValue(int64(id)),
			"code":  newStringValue(authoredOrderDetailCode(id)),
		}); err != nil {
			t.Fatalf("AssertTemplate(detail): %v", err)
		}
		if _, err := session.AssertTemplate(ctx, templates.tag, Fields{
			"event": newIntValue(int64(id)),
			"label": newStringValue(authoredOrderTagLabel(id)),
		}); err != nil {
			t.Fatalf("AssertTemplate(tag): %v", err)
		}
		if authoredOrderBlocked(id) {
			if _, err := session.AssertTemplate(ctx, templates.block, Fields{
				"event":  newIntValue(int64(id)),
				"active": newBoolValue(true),
			}); err != nil {
				t.Fatalf("AssertTemplate(block): %v", err)
			}
		}
	}
}

const (
	authoredOrderRootCount      = 64
	authoredOrderScoreThreshold = 50
)

func authoredOrderInitialFacts(items int) int {
	blocked := 0
	for id := range items {
		if authoredOrderBlocked(id) {
			blocked++
		}
	}
	return authoredOrderRootCount + 3*items + blocked
}

func authoredOrderExpectedMatches(shape authoredOrderBenchmarkShape, items int) int {
	matches := 0
	for id := range items {
		if !authoredOrderBaseMatch(id) {
			continue
		}
		switch shape {
		case authoredOrderSelectiveFirst, authoredOrderBroadFirst:
			matches++
		case authoredOrderJoinChain:
			if authoredOrderDetailSelected(id) {
				matches++
			}
		case authoredOrderStarJoin:
			if authoredOrderDetailSelected(id) && authoredOrderTagPriority(id) {
				matches++
			}
		case authoredOrderNegationAfterSelective, authoredOrderNegationAfterBroad:
			if !authoredOrderBlocked(id) {
				matches++
			}
		}
	}
	return matches
}

func authoredOrderBaseMatch(id int) bool {
	return authoredOrderRootSelected(id%authoredOrderRootCount) && authoredOrderScore(id) >= authoredOrderScoreThreshold
}

func authoredOrderRootSelected(rootID int) bool {
	return rootID%8 == 0
}

func authoredOrderScore(id int) int {
	return id % 100
}

func authoredOrderDetailCode(id int) string {
	if authoredOrderDetailSelected(id) {
		return "selected"
	}
	return "other"
}

func authoredOrderDetailSelected(id int) bool {
	return id%3 == 0
}

func authoredOrderTagLabel(id int) string {
	if authoredOrderTagPriority(id) {
		return "priority"
	}
	return "normal"
}

func authoredOrderTagPriority(id int) bool {
	return id%4 == 0
}

func authoredOrderBlocked(id int) bool {
	return id%7 == 0
}
