package gess

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkQueryRows []QueryRow

type queryBenchmarkShape string

const (
	queryBenchmarkSimple   queryBenchmarkShape = "simple-filter"
	queryBenchmarkJoin     queryBenchmarkShape = "join-filter"
	queryBenchmarkNegation queryBenchmarkShape = "negation-filter"
)

type queryBenchmarkCase struct {
	shape     queryBenchmarkShape
	factCount int
}

type queryBenchmarkRevision struct {
	revision      *Ruleset
	personKey     TemplateKey
	departmentKey TemplateKey
	blockKey      TemplateKey
}

func BenchmarkGraphTerminalQueryScaling(b *testing.B) {
	cases := []queryBenchmarkCase{
		{shape: queryBenchmarkSimple, factCount: 1_000},
		{shape: queryBenchmarkSimple, factCount: 10_000},
		{shape: queryBenchmarkSimple, factCount: 50_000},
		{shape: queryBenchmarkJoin, factCount: 1_000},
		{shape: queryBenchmarkJoin, factCount: 10_000},
		{shape: queryBenchmarkJoin, factCount: 50_000},
		{shape: queryBenchmarkNegation, factCount: 1_000},
		{shape: queryBenchmarkNegation, factCount: 10_000},
		{shape: queryBenchmarkNegation, factCount: 50_000},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("%s/facts=%d/rows=%d", tc.shape, tc.factCount, benchmarkQueryExpectedRows(tc.shape, tc.factCount))
		b.Run(name+"/graph-terminal-query", func(b *testing.B) {
			benchmarkGraphTerminalQuery(b, tc)
		})
	}
}

func BenchmarkRuntimeMaterializationQueryProjection(b *testing.B) {
	ctx := context.Background()
	workspace, personKey, departmentKey, blockKey := benchmarkQueryWorkspace(b)
	returns := []QueryReturnSpec{
		ReturnValue("id_0", BindingFieldExpr{Binding: "p", Field: "id"}),
		ReturnValue("dept_0", BindingFieldExpr{Binding: "p", Field: "dept"}),
		ReturnValue("age_0", BindingFieldExpr{Binding: "p", Field: "age"}),
		ReturnValue("id_1", BindingFieldExpr{Binding: "p", Field: "id"}),
		ReturnValue("dept_1", BindingFieldExpr{Binding: "p", Field: "dept"}),
		ReturnValue("age_1", BindingFieldExpr{Binding: "p", Field: "age"}),
	}
	if err := workspace.AddQuery(QuerySpec{
		Name:       "wide",
		Parameters: []QueryParameterSpec{{Name: "dept", Kind: ValueString}},
		ConditionTree: benchmarkAdultPersonMatch(personKey, ParamExpr{
			Name: "dept",
		}),
		Returns: returns,
	}); err != nil {
		b.Fatalf("AddQuery wide: %v", err)
	}
	revision := mustCompileWorkspace(b, workspace)
	compiled := queryBenchmarkRevision{
		revision:      revision,
		personKey:     personKey,
		departmentKey: departmentKey,
		blockKey:      blockKey,
	}
	initials := benchmarkQueryFacts(b, compiled, 10_000)
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	args := QueryArgs{"dept": "dept-00"}
	rows, err := session.QueryAll(ctx, "wide", args)
	if err != nil {
		b.Fatalf("warmup QueryAll: %v", err)
	}
	expectedRows := benchmarkQueryExpectedRows(queryBenchmarkSimple, 10_000)
	if len(rows) != expectedRows {
		b.Fatalf("warmup rows = %d, want %d", len(rows), expectedRows)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(len(rows)), "rows/query")
	b.ReportMetric(float64(len(returns)), "returns/row")
	b.ResetTimer()
	for b.Loop() {
		rows, err := session.QueryAll(ctx, "wide", args)
		if err != nil {
			b.Fatalf("QueryAll: %v", err)
		}
		benchmarkQueryRows = rows
	}
}

func benchmarkGraphTerminalQuery(b *testing.B, tc queryBenchmarkCase) {
	b.Helper()
	ctx := context.Background()
	compiled := benchmarkQueryRevision(b, tc.shape)
	initials := benchmarkQueryFacts(b, compiled, tc.factCount)
	session, err := NewSession(compiled.revision, WithInitialFacts(initials...))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	queryName, args := benchmarkQueryInvocation(tc.shape)
	rows, err := session.QueryAll(ctx, queryName, args)
	if err != nil {
		b.Fatalf("warmup QueryAll: %v", err)
	}
	expectedRows := benchmarkQueryExpectedRows(tc.shape, tc.factCount)
	if len(rows) != expectedRows {
		b.Fatalf("warmup rows = %d, want %d", len(rows), expectedRows)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(tc.factCount), "facts")
	b.ReportMetric(float64(len(rows)), "rows/query")
	b.ResetTimer()
	for b.Loop() {
		rows, err := session.QueryAll(ctx, queryName, args)
		if err != nil {
			b.Fatalf("QueryAll: %v", err)
		}
		benchmarkQueryRows = rows
	}
}

func benchmarkQueryRevision(tb testing.TB, shape queryBenchmarkShape) queryBenchmarkRevision {
	tb.Helper()
	workspace, personKey, departmentKey, blockKey := benchmarkQueryWorkspace(tb)
	switch shape {
	case queryBenchmarkSimple:
		mustAddBenchmarkSimpleQuery(tb, workspace, personKey)
	case queryBenchmarkJoin:
		mustAddBenchmarkJoinQuery(tb, workspace, personKey, departmentKey)
	case queryBenchmarkNegation:
		mustAddBenchmarkNegationQuery(tb, workspace, personKey, blockKey)
	default:
		tb.Fatalf("unsupported query benchmark shape %q", shape)
	}
	return queryBenchmarkRevision{
		revision:      mustCompileWorkspace(tb, workspace),
		personKey:     personKey,
		departmentKey: departmentKey,
		blockKey:      blockKey,
	}
}

func benchmarkQueryWorkspace(tb testing.TB) (*Workspace, TemplateKey, TemplateKey, TemplateKey) {
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
	department := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "department",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	block := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "block",
		Fields: []FieldSpec{
			{Name: "person_id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	return workspace, person.Key(), department.Key(), block.Key()
}

func mustAddBenchmarkSimpleQuery(tb testing.TB, workspace *Workspace, personKey TemplateKey) {
	tb.Helper()
	if err := workspace.AddQuery(QuerySpec{
		Name: "simple",
		Parameters: []QueryParameterSpec{
			{Name: "dept", Kind: ValueString},
		},
		ConditionTree: benchmarkAdultPersonMatch(personKey, ParamExpr{Name: "dept"}),
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
		},
	}); err != nil {
		tb.Fatalf("AddQuery simple: %v", err)
	}
}

func mustAddBenchmarkJoinQuery(tb testing.TB, workspace *Workspace, personKey, departmentKey TemplateKey) {
	tb.Helper()
	if err := workspace.AddQuery(QuerySpec{
		Name: "join",
		Parameters: []QueryParameterSpec{
			{Name: "region", Kind: ValueString},
		},
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding:     "d",
				TemplateKey: departmentKey,
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "region"}, Right: ParamExpr{Name: "region"}},
				},
			},
			Match{
				Binding:     "p",
				TemplateKey: personKey,
				JoinConstraints: []JoinConstraintSpec{
					{Field: "dept", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "d", Field: "id"}},
				},
				Predicates: []ExpressionSpec{
					CompareExpr{Operator: ExpressionCompareGreaterOrEqual, Left: CurrentFieldExpr{Field: "age"}, Right: ConstExpr{Value: 18}},
				},
			},
		}},
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
			ReturnValue("dept", BindingFieldExpr{Binding: "d", Field: "id"}),
		},
	}); err != nil {
		tb.Fatalf("AddQuery join: %v", err)
	}
}

func mustAddBenchmarkNegationQuery(tb testing.TB, workspace *Workspace, personKey, blockKey TemplateKey) {
	tb.Helper()
	if err := workspace.AddQuery(QuerySpec{
		Name: "negation",
		Parameters: []QueryParameterSpec{
			{Name: "dept", Kind: ValueString},
		},
		ConditionTree: And{Conditions: []ConditionSpec{
			benchmarkAdultPersonMatch(personKey, ParamExpr{Name: "dept"}),
			Not{Condition: Match{
				Binding:     "b",
				TemplateKey: blockKey,
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: true},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "person_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "p", Field: "id"}},
				},
			}},
		}},
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "p", Field: "id"}),
		},
	}); err != nil {
		tb.Fatalf("AddQuery negation: %v", err)
	}
}

func benchmarkAdultPersonMatch(personKey TemplateKey, dept ExpressionSpec) Match {
	return Match{
		Binding:     "p",
		TemplateKey: personKey,
		Predicates: []ExpressionSpec{
			CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "dept"}, Right: dept},
			CompareExpr{Operator: ExpressionCompareGreaterOrEqual, Left: CurrentFieldExpr{Field: "age"}, Right: ConstExpr{Value: 18}},
		},
	}
}

func benchmarkQueryInvocation(shape queryBenchmarkShape) (string, QueryArgs) {
	switch shape {
	case queryBenchmarkSimple:
		return "simple", QueryArgs{"dept": "dept-00"}
	case queryBenchmarkJoin:
		return "join", QueryArgs{"region": "region-0"}
	case queryBenchmarkNegation:
		return "negation", QueryArgs{"dept": "dept-00"}
	default:
		return "", nil
	}
}

func benchmarkQueryFacts(tb testing.TB, compiled queryBenchmarkRevision, count int) []SessionInitialFact {
	tb.Helper()
	facts := make([]SessionInitialFact, 0, count+benchmarkQueryDepartmentCount+count/10+1)
	for i := range benchmarkQueryDepartmentCount {
		facts = append(facts, SessionInitialFact{
			TemplateKey: compiled.departmentKey,
			Fields: mustFields(tb, map[string]any{
				"id":     benchmarkQueryDepartmentID(i),
				"region": fmt.Sprintf("region-%d", i%4),
				"active": true,
			}),
		})
	}
	for i := range count {
		id := fmt.Sprintf("p-%05d", i)
		dept := benchmarkQueryDepartmentID(i % benchmarkQueryDepartmentCount)
		age := 20 + i%40
		if i%7 == 0 {
			age = 16
		}
		facts = append(facts, SessionInitialFact{
			TemplateKey: compiled.personKey,
			Fields: mustFields(tb, map[string]any{
				"id":   id,
				"dept": dept,
				"age":  age,
			}),
		})
		if i%10 == 0 {
			facts = append(facts, SessionInitialFact{
				TemplateKey: compiled.blockKey,
				Fields: mustFields(tb, map[string]any{
					"person_id": id,
					"active":    true,
				}),
			})
		}
	}
	return facts
}

const benchmarkQueryDepartmentCount = 16

func benchmarkQueryDepartmentID(index int) string {
	return fmt.Sprintf("dept-%02d", index)
}

func benchmarkQueryExpectedRows(shape queryBenchmarkShape, count int) int {
	rows := 0
	for i := range count {
		deptIndex := i % benchmarkQueryDepartmentCount
		adult := i%7 != 0
		blocked := i%10 == 0
		switch shape {
		case queryBenchmarkSimple:
			if deptIndex == 0 && adult {
				rows++
			}
		case queryBenchmarkJoin:
			if deptIndex%4 == 0 && adult {
				rows++
			}
		case queryBenchmarkNegation:
			if deptIndex == 0 && adult && !blocked {
				rows++
			}
		}
	}
	return rows
}
