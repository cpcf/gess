package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestStructuralOrRejectsUnsupportedLeafKinds(t *testing.T) {
	match := Match{Binding: "x", Target: TemplateFact("item")}
	if err := validateStructuralConditionProgram("aggregate", And{Conditions: []ConditionSpec{match, Accumulate(match, Count().As("count"))}}); !errors.Is(err, ErrAggregateValidation) {
		t.Fatalf("aggregate validation error = %v, want ErrAggregateValidation", err)
	}
	if err := validateStructuralConditionProgram("higher-order", And{Conditions: []ConditionSpec{match, Exists(match)}}); !errors.Is(err, ErrInvalidHigherOrderCondition) {
		t.Fatalf("higher-order validation error = %v, want ErrInvalidHigherOrderCondition", err)
	}
}

func TestOversizedStructuralOrRejectsUnsupportedLeavesAtCompileTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail func(TemplateKey) ConditionSpec
		want error
	}{
		{name: "aggregate", tail: func(key TemplateKey) ConditionSpec {
			return Accumulate(Match{Binding: "member", Target: TemplateKeyFact(key)}, Count().As("count"))
		}, want: ErrAggregateValidation},
		{name: "higher-order", tail: func(key TemplateKey) ConditionSpec {
			return Exists(Match{Binding: "member", Target: TemplateKeyFact(key)})
		}, want: ErrInvalidHigherOrderCondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			item := mustAddTemplate(t, workspace, TemplateSpec{Name: "unsupported-structural-item", Fields: []FieldSpec{{Name: "side", Kind: ValueString, Required: true}}})
			mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
			conditions := make([]ConditionSpec, 0, 12)
			for i := range 11 {
				binding := fmt.Sprintf("item%d", i)
				conditions = append(conditions, Or{Conditions: []ConditionSpec{
					Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: []FieldConstraintSpec{{Field: "side", Operator: FieldConstraintEqual, Value: "left"}}},
					Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: []FieldConstraintSpec{{Field: "side", Operator: FieldConstraintEqual, Value: "right"}}},
				}})
			}
			conditions = append(conditions, tc.tail(item.Key()))
			mustAddRule(t, workspace, RuleSpec{Name: "unsupported-structural", ConditionTree: And{Conditions: conditions}, Actions: []RuleActionSpec{{Name: "noop"}}})
			_, err := workspace.Compile(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Compile error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestOversizedStructuralOrQueryRejectsUnsupportedLeavesAtCompileTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail func(TemplateKey) ConditionSpec
		want error
	}{
		{name: "aggregate", tail: func(key TemplateKey) ConditionSpec {
			return Accumulate(Match{Binding: "member", Target: TemplateKeyFact(key)}, Count().As("count"))
		}, want: ErrAggregateValidation},
		{name: "higher-order", tail: func(key TemplateKey) ConditionSpec {
			return Exists(Match{Binding: "member", Target: TemplateKeyFact(key)})
		}, want: ErrInvalidHigherOrderCondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			item := mustAddTemplate(t, workspace, TemplateSpec{Name: "unsupported-query-item", Fields: []FieldSpec{{Name: "side", Kind: ValueString, Required: true}}})
			conditions := make([]ConditionSpec, 0, 12)
			for i := range 11 {
				binding := fmt.Sprintf("item%d", i)
				conditions = append(conditions, Or{Conditions: []ConditionSpec{
					Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: []FieldConstraintSpec{{Field: "side", Operator: FieldConstraintEqual, Value: "left"}}},
					Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: []FieldConstraintSpec{{Field: "side", Operator: FieldConstraintEqual, Value: "right"}}},
				}})
			}
			conditions = append(conditions, tc.tail(item.Key()))
			if err := workspace.AddQuery(QuerySpec{Name: "unsupported-query", ConditionTree: And{Conditions: conditions}, Returns: []QueryReturnSpec{ReturnValue("side", BindingFieldExpr{Binding: "item10", Field: "side"})}}); err != nil {
				t.Fatal(err)
			}
			_, err := workspace.Compile(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Compile error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestStructuralOrCompilesAndMaintainsUnionSupport(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "structural-or-item",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "side", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	groups := make([]ConditionSpec, 16)
	for i := range groups {
		binding := fmt.Sprintf("item%d", i)
		group := fmt.Sprintf("g%d", i)
		base := []FieldConstraintSpec{{Field: "group", Operator: FieldConstraintEqual, Value: group}}
		left := append([]FieldConstraintSpec(nil), base...)
		right := append([]FieldConstraintSpec(nil), base...)
		if i == 0 {
			left = append(left, FieldConstraintSpec{Field: "active", Operator: FieldConstraintEqual, Value: true})
			right = append(right, FieldConstraintSpec{Field: "dept", Operator: FieldConstraintEqual, Value: "eng"})
		} else {
			left = append(left, FieldConstraintSpec{Field: "side", Operator: FieldConstraintEqual, Value: "left"})
			right = append(right, FieldConstraintSpec{Field: "side", Operator: FieldConstraintEqual, Value: "right"})
		}
		groups[i] = Or{Conditions: []ConditionSpec{
			Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: left},
			Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: right},
		}}
	}
	mustAddRule(t, workspace, RuleSpec{
		Name: "structural-or", ConditionTree: And{Conditions: groups}, Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule := revision.rules["structural-or"]
	if !rule.structuralConditionProgram {
		t.Fatal("rule did not select structural condition execution")
	}
	if got := len(rule.conditionBranchPlans); got != maxInspectedConditionBranches {
		t.Fatalf("bounded public branches = %d, want %d", got, maxInspectedConditionBranches)
	}
	inspected, _ := revision.Rule("structural-or")
	if !inspected.ConditionBranchesTruncated() {
		t.Fatal("public rule branch inspection did not report truncation")
	}
	if got := len(revision.graph.alphaNodes) + len(revision.graph.betaNodes) + len(revision.graph.unionNodes); got > 200 {
		t.Fatalf("structural graph nodes = %d, want linear-sized graph", got)
	}

	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !session.propagation.runtime.supportsGraphBeta() {
		t.Fatalf("graph beta unsupported: %+v", session.propagation.runtime.plan.unsupported)
	}
	if !session.propagation.runtime.supportsIncrementalAgenda() {
		t.Fatalf("incremental agenda unsupported: %+v", session.propagation.runtime.plan)
	}
	var first FactID
	for i := range groups {
		result, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{
			"group": fmt.Sprintf("g%d", i), "side": "left", "active": true, "dept": "eng",
		}))
		if err != nil {
			t.Fatalf("Assert group %d: %v", i, err)
		}
		if i == 0 {
			first = result.Fact.ID()
		}
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.propagation.runtime)
	if got := len(session.agendaDriver.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations with overlapping arm support = %d, want 1", got)
	}
	if _, err := session.Modify(ctx, first, FactPatch{Set: mustFields(t, map[string]any{"active": false})}); err != nil {
		t.Fatalf("Modify retaining second support: %v", err)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.propagation.runtime)
	if got := len(session.agendaDriver.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations after first support removed = %d, want 1", got)
	}
	if _, err := session.Modify(ctx, first, FactPatch{Set: mustFields(t, map[string]any{"dept": "sales"})}); err != nil {
		t.Fatalf("Modify removing last support: %v", err)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.propagation.runtime)
	if got := len(session.agendaDriver.agenda.pendingActivations()); got != 0 {
		t.Fatalf("pending activations after last support removed = %d, want 0", got)
	}
}

func TestStructuralOrQueryUsesGraphUnion(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{Name: "structural-query-item", Fields: []FieldSpec{
		{Name: "group", Kind: ValueString, Required: true},
		{Name: "side", Kind: ValueString, Required: true},
	}})
	groups := make([]ConditionSpec, 11)
	for i := range groups {
		binding, group := fmt.Sprintf("item%d", i), fmt.Sprintf("g%d", i)
		arm := func(side string) ConditionSpec {
			return Match{Binding: binding, Target: TemplateKeyFact(item.Key()), FieldConstraints: []FieldConstraintSpec{
				{Field: "group", Operator: FieldConstraintEqual, Value: group},
				{Field: "side", Operator: FieldConstraintEqual, Value: side},
			}}
		}
		groups[i] = Or{Conditions: []ConditionSpec{arm("left"), arm("right")}}
	}
	if err := workspace.AddQuery(QuerySpec{
		Name: "structural-query", ConditionTree: And{Conditions: groups},
		Returns: []QueryReturnSpec{ReturnValue("group", BindingFieldExpr{Binding: "item10", Field: "group"})},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	if !revision.queries["structural-query"].structuralProgram {
		t.Fatal("query did not select structural condition execution")
	}
	inspected, _ := revision.Query("structural-query")
	if !inspected.ConditionBranchesTruncated() {
		t.Fatal("public query branch inspection did not report truncation")
	}
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for i := range groups {
		if _, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{"group": fmt.Sprintf("g%d", i), "side": "left"})); err != nil {
			t.Fatalf("Assert group %d: %v", i, err)
		}
	}
	rows, err := session.QueryAll(ctx, "structural-query", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("query rows = %d, want 1", len(rows))
	}
}

func TestReteGraphUnionKeepsCollidingTokenIdentitiesSeparate(t *testing.T) {
	arena := newTokenArena()
	makeToken := func(sequence uint64) tokenRef {
		fact := FactSnapshot{id: newFactID(1, sequence), version: 1, recency: Recency(sequence), generation: 1}
		return arena.add(tokenRef{}, bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	}
	first, second := makeToken(1), makeToken(2)
	firstRow, _ := first.resolve()
	secondRow, _ := second.resolve()
	secondRow.identityState = firstRow.identityState
	if first.identityKey() != second.identityKey() || tokenRefEqual(first, second) {
		t.Fatal("test setup did not create a distinct-token identity-key collision")
	}
	memory := &reteGraphBetaMemory{
		graph:  &reteGraph{unionNodes: []reteGraphUnionNode{{id: 1}}},
		arena:  arena,
		unions: make([]reteGraphUnionMemory, 2),
	}
	delta := reteAgendaDelta{supported: true}
	if err := memory.insertUnionSupport(1, first, nil, &delta); err != nil {
		t.Fatal(err)
	}
	if err := memory.insertUnionSupport(1, second, nil, &delta); err != nil {
		t.Fatal(err)
	}
	bucket := memory.unions[1].rows[first.identityKey()]
	if got := len(bucket); got != 2 {
		t.Fatalf("collision bucket rows = %d, want 2", got)
	}
	memory.removeUnionSupport(1, second, nil, &delta)
	bucket = memory.unions[1].rows[first.identityKey()]
	if len(bucket) != 1 || !tokenRefEqual(bucket[0].token, first) {
		t.Fatal("removing colliding second token removed or replaced the first row")
	}
}
