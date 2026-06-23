package gess

import "testing"

func TestRulesetReteDependencyIndexIncludesQueries(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	alert := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "alert",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "people-by-dept",
		Parameters: []QueryParameterSpec{{
			Name: "dept",
			Kind: ValueString,
		}},
		ConditionTree: Match{
			Binding:     "person",
			TemplateKey: person.Key(),
			Predicates: []ExpressionSpec{
				CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: "dept"},
					Right:    ParamExpr{Name: "dept"},
				},
			},
		},
		Returns: []QueryReturnSpec{{
			Alias:   "person",
			Binding: "person",
		}},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)
	if revision.factMayAffectRuleMatchesByTarget(person.Name(), person.Key()) {
		t.Fatal("query-only template unexpectedly affects rule matches")
	}
	if !revision.factMayAffectReteByTarget(person.Name(), person.Key()) {
		t.Fatal("query template did not affect Rete dependency index")
	}
	if revision.factMayAffectReteByTarget(alert.Name(), alert.Key()) {
		t.Fatal("unused template unexpectedly affected Rete dependency index")
	}
}
