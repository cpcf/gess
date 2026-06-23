package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkQueryRows []QueryRow
var benchmarkJessLikeQueryRunResult RunResult

func BenchmarkSnapshotQueryVsJessLikeTriggerRule(b *testing.B) {
	const factCount = 1000
	ctx := context.Background()
	nativeRevision, nativePersonKey := benchmarkNativeQueryRevision(b)
	jessLikeRevision, jessPersonKey := benchmarkJessLikeQueryRevision(b)
	nativeFacts := benchmarkQueryFacts(b, nativePersonKey, factCount)
	jessFacts := benchmarkQueryFacts(b, jessPersonKey, factCount)
	nativeSession := mustSession(b, nativeRevision, "native-query-benchmark-session")
	for _, fact := range nativeFacts {
		if _, err := nativeSession.AssertTemplate(ctx, fact.TemplateKey, fact.Fields); err != nil {
			b.Fatalf("AssertTemplate: %v", err)
		}
	}
	nativeSnapshot, err := nativeSession.Snapshot(ctx)
	if err != nil {
		b.Fatalf("Snapshot: %v", err)
	}

	b.Run("native-snapshot-query", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := nativeSnapshot.QueryAll(ctx, "adults-by-dept", QueryArgs{"dept": "engineering"})
			if err != nil {
				b.Fatalf("QueryAll: %v", err)
			}
			benchmarkQueryRows = rows
		}
	})

	b.Run("jess-like-trigger-rule", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			session, err := NewSession(jessLikeRevision, WithInitialFacts(jessFacts...))
			if err != nil {
				b.Fatalf("NewSession: %v", err)
			}
			if _, err := session.AssertTemplate(ctx, "query_trigger", mustFields(b, map[string]any{"dept": "engineering"})); err != nil {
				b.Fatalf("AssertTemplate trigger: %v", err)
			}
			result, err := session.Run(ctx)
			if err != nil {
				b.Fatalf("Run: %v", err)
			}
			benchmarkJessLikeQueryRunResult = result
		}
	})
}

func benchmarkNativeQueryRevision(tb testing.TB) (*Ruleset, TemplateKey) {
	tb.Helper()
	workspace := NewWorkspace()
	person := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	mustAddAdultQuery(tb, workspace, person.Key())
	return mustCompileWorkspace(tb, workspace), person.Key()
}

func benchmarkJessLikeQueryRevision(tb testing.TB) (*Ruleset, TemplateKey) {
	tb.Helper()
	workspace := NewWorkspace()
	person := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
		},
	})
	trigger := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "query_trigger",
		Fields: []FieldSpec{
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{Name: "capture", Fn: func(ActionContext) error { return nil }})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "jess-like-adults-by-dept",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "q",
				TemplateKey: trigger.Key(),
			},
			{
				Binding:     "p",
				TemplateKey: person.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "dept", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "q", Field: "dept"}},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareGreaterOrEqual,
						Left:     CurrentFieldExpr{Field: "age"},
						Right:    ConstExpr{Value: 18},
					},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "capture"}},
	})
	return mustCompileWorkspace(tb, workspace), person.Key()
}

func benchmarkQueryFacts(tb testing.TB, personKey TemplateKey, count int) []SessionInitialFact {
	tb.Helper()
	facts := make([]SessionInitialFact, 0, count)
	for i := range count {
		dept := "engineering"
		if i%4 == 0 {
			dept = "sales"
		}
		age := 20 + i%40
		if i%7 == 0 {
			age = 16
		}
		facts = append(facts, SessionInitialFact{
			TemplateKey: personKey,
			Fields: mustFields(tb, map[string]any{
				"id":   fmt.Sprintf("p-%04d", i),
				"dept": dept,
				"age":  age,
			}),
		})
	}
	return facts
}
