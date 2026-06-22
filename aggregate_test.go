package gess

import (
	"context"
	"errors"
	"testing"
)

func TestAccumulateBuiltInsBindValuesAndUpdateAcrossMutations(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			row := Fields{}
			for _, name := range []string{"count", "sum", "min", "max", "collected"} {
				value, ok := ctx.BindingValue(name)
				if !ok {
					return errors.New("missing aggregate binding " + name)
				}
				row[name] = value
			}
			observed = append(observed, row)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "totals",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("sum"),
			Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
			Max(BindingFieldExpr{Binding: "item", Field: "amount"}).As("max"),
			Collect(BindingFieldExpr{Binding: "item", Field: "amount"}).As("collected"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-session")

	first, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertAggregateRow(t, observed[len(observed)-1], 2, 8, 3, 5, []Value{mustValue(t, 3), mustValue(t, 5)})

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 1})}); err != nil {
		t.Fatalf("modify second: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("second Run fired = %d, want 1", result.Fired)
	}
	assertAggregateRow(t, observed[len(observed)-1], 2, 4, 1, 3, []Value{mustValue(t, 3), mustValue(t, 1)})

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertAggregateRow(t, observed[len(observed)-1], 1, 1, 1, 1, []Value{mustValue(t, 1)})
}

func TestAccumulateEmptyCountSumCollectAndMinMaxNoContinuation(t *testing.T) {
	var fired int
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "amount", Kind: ValueInt},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			fired++
			count, ok := ctx.BindingValue("count")
			if !ok || !count.Equal(mustValue(t, 0)) {
				return errors.New("count did not bind zero")
			}
			sum, ok := ctx.BindingValue("sum")
			if !ok || !sum.Equal(mustValue(t, 0)) {
				return errors.New("sum did not bind zero")
			}
			collected, ok := ctx.BindingValue("collected")
			if !ok || collected.Kind() != ValueList || len(collected.data.([]Value)) != 0 {
				return errors.New("collect did not bind empty list")
			}
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "empty-count-sum-collect",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("sum"),
			Collect(BindingFieldExpr{Binding: "item", Field: "amount"}).As("collected"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "empty-min-suppresses",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-empty-session")
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("fired = result %d action %d, want 1", result.Fired, fired)
	}
}

func TestAccumulateCountAndSumUseIncrementalAgendaDeltas(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, Fields{"count": count, "total": total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "total",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-incremental-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental aggregate agenda support", session.rete)
	}

	first, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 2)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 8)) {
		t.Fatalf("first Run fired/row = %d/%v, want 1/count=2 total=8", result.Fired, observed[len(observed)-1])
	}

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 1})}); err != nil {
		t.Fatalf("modify second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 2)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 4)) {
		t.Fatalf("second Run fired/row = %d/%v, want 1/count=2 total=4", result.Fired, observed[len(observed)-1])
	}

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 || !observed[len(observed)-1]["count"].Equal(mustValue(t, 1)) || !observed[len(observed)-1]["total"].Equal(mustValue(t, 1)) {
		t.Fatalf("third Run fired/row = %d/%v, want 1/count=1 total=1", result.Fired, observed[len(observed)-1])
	}
}

func TestAccumulateMinAndMaxUseIncrementalAgendaDeltas(t *testing.T) {
	var observed []Fields
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			row := Fields{}
			for _, name := range []string{"min", "max"} {
				value, ok := ctx.BindingValue(name)
				if !ok {
					return errors.New("missing aggregate binding " + name)
				}
				row[name] = value
			}
			observed = append(observed, row)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "extrema",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
			Max(BindingFieldExpr{Binding: "item", Field: "amount"}).As("max"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-extrema-incremental-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental min/max aggregate agenda support", session.rete)
	}

	first, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	third, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "c", "amount": 1}))
	if err != nil {
		t.Fatalf("assert third: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 1, 5)

	if _, err := session.Modify(context.Background(), third.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 7})}); err != nil {
		t.Fatalf("modify third: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("second Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 3, 7)

	if _, err := session.Retract(context.Background(), third.Fact.ID()); err != nil {
		t.Fatalf("retract third: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 3, 5)

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("fourth Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fourth Run fired = %d, want 1", result.Fired)
	}
	assertExtremaRow(t, observed[len(observed)-1], 5, 5)

	if _, err := session.Retract(context.Background(), second.Fact.ID()); err != nil {
		t.Fatalf("retract second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
}

func TestAccumulateCollectUsesIncrementalAgendaDeltas(t *testing.T) {
	var observed []Value
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			collected, ok := ctx.BindingValue("collected")
			if !ok {
				return errors.New("missing collected binding")
			}
			observed = append(observed, collected)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "collected",
		ConditionTree: Accumulate(
			Match{Binding: "item", TemplateKey: item.Key()},
			Collect(BindingFieldExpr{Binding: "item", Field: "amount"}).As("collected"),
		),
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-collect-incremental-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want incremental collect aggregate agenda support", session.rete)
	}

	first, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("first Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{mustValue(t, 3), mustValue(t, 5)})

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"amount": 1})}); err != nil {
		t.Fatalf("modify second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("second Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{mustValue(t, 3), mustValue(t, 1)})

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract first: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("third Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{mustValue(t, 1)})

	if _, err := session.Retract(context.Background(), second.Fact.ID()); err != nil {
		t.Fatalf("retract second: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("fourth Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("fourth Run fired = %d, want 1", result.Fired)
	}
	assertCollectedValue(t, observed[len(observed)-1], []Value{})
}

func TestAccumulateAfterOuterBindingUsesBucketedIncrementalAgenda(t *testing.T) {
	var observed []bucketedAggregateRow
	workspace := NewWorkspace()
	group := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			groupID, ok := ctx.BindingScalarValue("group", "id")
			if !ok {
				return errors.New("missing group id")
			}
			groupName, ok := groupID.AsString()
			if !ok {
				return errors.New("group id is not a string")
			}
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			total, ok := ctx.BindingValue("total")
			if !ok {
				return errors.New("missing total binding")
			}
			observed = append(observed, bucketedAggregateRow{group: groupName, count: count, total: total})
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "group-totals",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", TemplateKey: group.Key()},
			Accumulate(
				Match{
					Binding:     "item",
					TemplateKey: item.Key(),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					},
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "bucketed-aggregate-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want bucketed aggregate incremental agenda support", session.rete)
	}

	if _, err := session.AssertTemplate(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("assert group a: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.AssertTemplate(context.Background(), group.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert group b: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	first, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i1", "group": "a", "amount": 3}))
	if err != nil {
		t.Fatalf("assert item i1: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	second, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "i2", "group": "b", "amount": 5}))
	if err != nil {
		t.Fatalf("assert item i2: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	start := len(observed)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("first Run fired = %d, want 2", result.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"a": {1, 3}, "b": {1, 5}})

	if _, err := session.Modify(context.Background(), second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"group": "a", "amount": 2})}); err != nil {
		t.Fatalf("modify item i2: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("retract item i1: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	start = len(observed)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("second Run fired = %d, want 2", result.Fired)
	}
	assertBucketedAggregateRows(t, observed[start:], map[string][2]int64{"a": {1, 2}, "b": {0, 0}})
}

func TestAccumulateResultFeedsDownstreamJoinIncrementally(t *testing.T) {
	var observed []int64
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	gate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "gate",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "expected", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "record",
		Fn: func(ctx ActionContext) error {
			count, ok := ctx.BindingValue("count")
			if !ok {
				return errors.New("missing count binding")
			}
			observed = append(observed, count.intValue)
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "count-gate",
		ConditionTree: And{Conditions: []ConditionSpec{
			Accumulate(
				Match{Binding: "item", TemplateKey: item.Key()},
				Count().As("count"),
			),
			Match{
				Binding:     "gate",
				TemplateKey: gate.Key(),
				Predicates: []ExpressionSpec{
					CompareExpr{
						Operator: ExpressionCompareEqual,
						Left:     CurrentFieldExpr{Field: "expected"},
						Right:    BindingValueExpr{Binding: "count"},
					},
				},
			},
		}},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "aggregate-downstream-join-session")
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		t.Fatalf("rete runtime = %#v, want downstream aggregate incremental agenda support", session.rete)
	}

	if _, err := session.AssertTemplate(context.Background(), gate.Key(), mustFields(t, map[string]any{"expected": 2})); err != nil {
		t.Fatalf("assert gate 2: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "a"})); err != nil {
		t.Fatalf("assert item a: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	if _, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "b"})); err != nil {
		t.Fatalf("assert item b: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if result.Fired != 1 || observed[len(observed)-1] != 2 {
		t.Fatalf("first Run fired/observed = %d/%v, want 1/2", result.Fired, observed)
	}

	if _, err := session.AssertTemplate(context.Background(), gate.Key(), mustFields(t, map[string]any{"expected": 3})); err != nil {
		t.Fatalf("assert gate 3: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"id": "c"})); err != nil {
		t.Fatalf("assert item c: %v", err)
	}
	assertSessionAgendaMatchesFullReteReconcile(t, session)
	result, err = session.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.Fired != 1 || observed[len(observed)-1] != 3 {
		t.Fatalf("second Run fired/observed = %d/%v, want 1/3", result.Fired, observed)
	}
}

func TestAccumulateValidationRejectsUnsupportedShapesAndCollisions(t *testing.T) {
	item := TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "amount", Kind: ValueInt}}}
	cases := []struct {
		name string
		tree ConditionSpec
	}{
		{
			name: "or input",
			tree: Accumulate(Or{Conditions: []ConditionSpec{
				Match{Binding: "a", TemplateKey: TemplateKey("item")},
				Match{Binding: "b", TemplateKey: TemplateKey("item")},
			}}, Count().As("count")),
		},
		{
			name: "result collision",
			tree: And{Conditions: []ConditionSpec{
				Match{Binding: "count", TemplateKey: TemplateKey("item")},
				Accumulate(Match{Binding: "item", TemplateKey: TemplateKey("item")}, Count().As("count")),
			}},
		},
		{
			name: "accumulate inside or",
			tree: Or{Conditions: []ConditionSpec{
				Accumulate(Match{Binding: "item", TemplateKey: TemplateKey("item")}, Count().As("count")),
				Match{Binding: "other", TemplateKey: TemplateKey("item")},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			mustAddTemplate(t, workspace, item)
			mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ActionContext) error { return nil }})
			mustAddRule(t, workspace, RuleSpec{
				Name:          "bad",
				ConditionTree: tc.tree,
				Actions:       []RuleActionSpec{{Name: "record"}},
			})
			_, err := workspace.Compile(context.Background())
			if !errors.Is(err, ErrAggregateValidation) {
				t.Fatalf("Compile error = %v, want ErrAggregateValidation", err)
			}
		})
	}
}

func assertAggregateRow(t *testing.T, row Fields, count, sum, min, max int64, collected []Value) {
	t.Helper()
	wants := map[string]Value{
		"count": mustValue(t, count),
		"sum":   mustValue(t, sum),
		"min":   mustValue(t, min),
		"max":   mustValue(t, max),
	}
	for key, want := range wants {
		if got := row[key]; !got.Equal(want) {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
	gotCollected := row["collected"]
	wantCollected := mustValue(t, collected)
	if !gotCollected.Equal(wantCollected) {
		t.Fatalf("collected = %v, want %v", gotCollected, wantCollected)
	}
}

func assertExtremaRow(t *testing.T, row Fields, min, max int64) {
	t.Helper()
	if got := row["min"]; !got.Equal(mustValue(t, min)) {
		t.Fatalf("min = %v, want %d", got, min)
	}
	if got := row["max"]; !got.Equal(mustValue(t, max)) {
		t.Fatalf("max = %v, want %d", got, max)
	}
}

func assertCollectedValue(t *testing.T, got Value, want []Value) {
	t.Helper()
	expected := mustValue(t, want)
	if !got.Equal(expected) {
		t.Fatalf("collected = %v, want %v", got, expected)
	}
}

type bucketedAggregateRow struct {
	group string
	count Value
	total Value
}

func assertBucketedAggregateRows(t *testing.T, rows []bucketedAggregateRow, want map[string][2]int64) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %d rows", rows, len(want))
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		values, ok := want[row.group]
		if !ok {
			t.Fatalf("unexpected aggregate row for group %q: %v", row.group, row)
		}
		if seen[row.group] {
			t.Fatalf("duplicate aggregate row for group %q", row.group)
		}
		seen[row.group] = true
		if !row.count.Equal(mustValue(t, values[0])) || !row.total.Equal(mustValue(t, values[1])) {
			t.Fatalf("group %q row = count %v total %v, want count %d total %d", row.group, row.count, row.total, values[0], values[1])
		}
	}
	for group := range want {
		if !seen[group] {
			t.Fatalf("missing aggregate row for group %q", group)
		}
	}
}
