package gess

import (
	"context"
	"testing"
)

func BenchmarkReteGraphSlotSpecificModifySkip(b *testing.B) {
	ctx := context.Background()
	revision, personKey := mustSlotSpecificModifyBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-skip-benchmark-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(b, map[string]any{
		"id":     1,
		"note":   "old",
		"status": "inactive",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyMatchedRefresh(b *testing.B) {
	ctx := context.Background()
	revision, personKey := mustSlotSpecificModifyBenchmarkRulesetWithDeclaredReads(b, true)
	session := mustSession(b, revision, "slot-specific-modify-matched-refresh-benchmark-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(b, map[string]any{
		"id":     1,
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyJoinedRefresh(b *testing.B) {
	ctx := context.Background()
	revision, employeeKey, departmentKey := mustSlotSpecificModifyJoinedBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-joined-refresh-benchmark-session")
	inserted, err := session.AssertTemplate(ctx, employeeKey, mustFields(b, map[string]any{
		"name": "Ada",
		"dept": "Engineering",
		"note": "old",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, departmentKey, mustFields(b, map[string]any{"id": "Engineering"})); err != nil {
		b.Fatalf("AssertTemplate department: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyFilterRefresh(b *testing.B) {
	ctx := context.Background()
	revision, eventKey := mustSlotSpecificModifyFilterBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-filter-refresh-benchmark-session")
	inserted, err := session.AssertTemplate(ctx, eventKey, mustFields(b, map[string]any{
		"id":     "event-1",
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate event: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyAlphaPredicateRefresh(b *testing.B) {
	ctx := context.Background()
	revision, personKey := mustSlotSpecificModifyAlphaPredicateBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-alpha-predicate-refresh-benchmark-session")
	inserted, err := session.AssertTemplate(ctx, personKey, mustFields(b, map[string]any{
		"id":   "person-1",
		"age":  20,
		"note": "old",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate person: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyListPatternRefresh(b *testing.B) {
	ctx := context.Background()
	revision, eventKey := mustSlotSpecificModifyListPatternBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-list-pattern-refresh-benchmark-session")
	inserted, err := session.AssertTemplate(ctx, eventKey, mustFields(b, map[string]any{
		"id":   "event-1",
		"tags": []any{"vip", "blue", "gold", "active"},
		"note": "old",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate event: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyNegationRightRefresh(b *testing.B) {
	ctx := context.Background()
	revision, customerKey, blockKey := mustSlotSpecificModifyNegationBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-negation-right-refresh-benchmark-session")
	if _, err := session.AssertTemplate(ctx, customerKey, mustFields(b, map[string]any{
		"id":   "customer-1",
		"note": "customer",
	})); err != nil {
		b.Fatalf("AssertTemplate customer: %v", err)
	}
	inserted, err := session.AssertTemplate(ctx, blockKey, mustFields(b, map[string]any{
		"customer_id": "customer-1",
		"active":      "active",
		"code":        "old",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate block: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"code": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"code": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyAggregateRefresh(b *testing.B) {
	ctx := context.Background()
	revision, itemKey := mustSlotSpecificModifyAggregateBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-aggregate-refresh-benchmark-session")
	if _, err := session.AssertTemplate(ctx, itemKey, mustFields(b, map[string]any{
		"id":     "item-1",
		"amount": 3,
		"note":   "first",
	})); err != nil {
		b.Fatalf("AssertTemplate first item: %v", err)
	}
	inserted, err := session.AssertTemplate(ctx, itemKey, mustFields(b, map[string]any{
		"id":     "item-2",
		"amount": 5,
		"note":   "old",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate second item: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyBucketedAggregateRefresh(b *testing.B) {
	ctx := context.Background()
	revision, groupKey, itemKey := mustSlotSpecificModifyBucketedAggregateBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-bucketed-aggregate-refresh-benchmark-session")
	if _, err := session.AssertTemplate(ctx, groupKey, mustFields(b, map[string]any{"id": "a", "note": "first"})); err != nil {
		b.Fatalf("AssertTemplate group a: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, groupKey, mustFields(b, map[string]any{"id": "b", "note": "second"})); err != nil {
		b.Fatalf("AssertTemplate group b: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, itemKey, mustFields(b, map[string]any{
		"id":     "item-1",
		"group":  "a",
		"amount": 3,
		"note":   "first",
	})); err != nil {
		b.Fatalf("AssertTemplate first item: %v", err)
	}
	inserted, err := session.AssertTemplate(ctx, itemKey, mustFields(b, map[string]any{
		"id":     "item-2",
		"group":  "b",
		"amount": 5,
		"note":   "old",
	}))
	if err != nil {
		b.Fatalf("AssertTemplate second item: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyBucketedAggregateOuterRefresh(b *testing.B) {
	ctx := context.Background()
	revision, groupKey, itemKey := mustSlotSpecificModifyBucketedAggregateBenchmarkRuleset(b)
	session := mustSession(b, revision, "slot-specific-modify-bucketed-aggregate-outer-refresh-benchmark-session")
	if _, err := session.AssertTemplate(ctx, groupKey, mustFields(b, map[string]any{"id": "a", "note": "first"})); err != nil {
		b.Fatalf("AssertTemplate group a: %v", err)
	}
	inserted, err := session.AssertTemplate(ctx, groupKey, mustFields(b, map[string]any{"id": "b", "note": "old"}))
	if err != nil {
		b.Fatalf("AssertTemplate group b: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, itemKey, mustFields(b, map[string]any{
		"id":     "item-1",
		"group":  "a",
		"amount": 3,
		"note":   "first",
	})); err != nil {
		b.Fatalf("AssertTemplate first item: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, itemKey, mustFields(b, map[string]any{
		"id":     "item-2",
		"group":  "b",
		"amount": 5,
		"note":   "second",
	})); err != nil {
		b.Fatalf("AssertTemplate second item: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		b.Fatalf("reconcileAgendaInternal: %v", err)
	}

	oldPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "old"})}
	newPatch := FactPatch{Set: mustFields(b, map[string]any{"note": "new"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		patch := newPatch
		if i%2 == 1 {
			patch = oldPatch
		}
		result, err := session.Modify(ctx, inserted.Fact.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphSlotSpecificModifyMixedRefresh(b *testing.B) {
	ctx := context.Background()
	session, targets := mustSlotSpecificModifyMixedBenchmarkSession(b, "slot-specific-modify-mixed-refresh-benchmark-session")
	useNew := make([]bool, len(targets))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index := i % len(targets)
		target := targets[index]
		patch := target.new
		if useNew[index] {
			patch = target.old
		}
		useNew[index] = !useNew[index]
		result, err := session.Modify(ctx, target.id, patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func TestReteGraphSlotSpecificModifyMixedRefreshUsesFastPath(t *testing.T) {
	ctx := context.Background()
	session, targets := mustSlotSpecificModifyMixedBenchmarkSession(t, "slot-specific-modify-mixed-refresh-test-session")
	session.attachPropagationCounters()

	for _, target := range targets {
		before := session.propagationCounterSnapshot().Totals
		result, err := session.Modify(ctx, target.id, target.new)
		if err != nil {
			t.Fatalf("Modify %s: %v", target.name, err)
		}
		if result.Status != ModifyChanged {
			t.Fatalf("Modify %s status = %v, want %v", target.name, result.Status, ModifyChanged)
		}
		after := session.propagationCounterSnapshot().Totals
		if got := after.ModifyFastPathFallbacks - before.ModifyFastPathFallbacks; got != 0 {
			t.Fatalf("Modify %s fast-path fallbacks = %d, want 0", target.name, got)
		}
		if got := after.ModifyFastPathSkips - before.ModifyFastPathSkips; got != 1 {
			t.Fatalf("Modify %s fast-path skips = %d, want 1", target.name, got)
		}
	}
}

func mustSlotSpecificModifyBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey) {
	return mustSlotSpecificModifyBenchmarkRulesetWithDeclaredReads(tb, false)
}

type slotSpecificModifyMixedTarget struct {
	name string
	id   FactID
	old  FactPatch
	new  FactPatch
}

func mustSlotSpecificModifyMixedBenchmarkSession(tb testing.TB, id SessionID) (*Session, []slotSpecificModifyMixedTarget) {
	tb.Helper()

	ctx := context.Background()
	revision, keys := mustSlotSpecificModifyMixedBenchmarkRuleset(tb)
	session := mustSession(tb, revision, id)

	person, err := session.AssertTemplate(ctx, keys.person, mustFields(tb, map[string]any{"id": "person-1", "note": "old", "status": "active"}))
	if err != nil {
		tb.Fatalf("AssertTemplate person: %v", err)
	}
	employee, err := session.AssertTemplate(ctx, keys.employee, mustFields(tb, map[string]any{"name": "Ada", "dept": "Engineering", "note": "old"}))
	if err != nil {
		tb.Fatalf("AssertTemplate employee: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, keys.department, mustFields(tb, map[string]any{"id": "Engineering"})); err != nil {
		tb.Fatalf("AssertTemplate department: %v", err)
	}
	event, err := session.AssertTemplate(ctx, keys.event, mustFields(tb, map[string]any{
		"id":     "event-1",
		"tags":   []any{"vip", "blue", "gold", "active"},
		"note":   "old",
		"status": "active",
	}))
	if err != nil {
		tb.Fatalf("AssertTemplate event: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, keys.customer, mustFields(tb, map[string]any{"id": "customer-1", "note": "customer"})); err != nil {
		tb.Fatalf("AssertTemplate customer: %v", err)
	}
	block, err := session.AssertTemplate(ctx, keys.block, mustFields(tb, map[string]any{"customer_id": "customer-1", "active": "active", "code": "old"}))
	if err != nil {
		tb.Fatalf("AssertTemplate block: %v", err)
	}
	item, err := session.AssertTemplate(ctx, keys.item, mustFields(tb, map[string]any{"id": "item-1", "amount": 10, "note": "old"}))
	if err != nil {
		tb.Fatalf("AssertTemplate item: %v", err)
	}
	group, err := session.AssertTemplate(ctx, keys.group, mustFields(tb, map[string]any{"id": "group-1", "note": "old"}))
	if err != nil {
		tb.Fatalf("AssertTemplate group: %v", err)
	}
	bucketItem, err := session.AssertTemplate(ctx, keys.bucketItem, mustFields(tb, map[string]any{"id": "bucket-item-1", "group": "group-1", "amount": 10, "note": "old"}))
	if err != nil {
		tb.Fatalf("AssertTemplate bucket item: %v", err)
	}
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		tb.Fatalf("reconcileAgendaInternal: %v", err)
	}

	targets := []slotSpecificModifyMixedTarget{
		{name: "person", id: person.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"note": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"note": "new"})}},
		{name: "employee", id: employee.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"note": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"note": "new"})}},
		{name: "event", id: event.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"note": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"note": "new"})}},
		{name: "block", id: block.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"code": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"code": "new"})}},
		{name: "item", id: item.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"note": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"note": "new"})}},
		{name: "bucket item", id: bucketItem.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"note": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"note": "new"})}},
		{name: "group", id: group.Fact.ID(), old: FactPatch{Set: mustFields(tb, map[string]any{"note": "old"})}, new: FactPatch{Set: mustFields(tb, map[string]any{"note": "new"})}},
	}
	return session, targets
}

type slotSpecificModifyMixedKeys struct {
	person     TemplateKey
	employee   TemplateKey
	department TemplateKey
	event      TemplateKey
	customer   TemplateKey
	block      TemplateKey
	item       TemplateKey
	group      TemplateKey
	bucketItem TemplateKey
}

func mustSlotSpecificModifyMixedBenchmarkRuleset(tb testing.TB) (*Ruleset, slotSpecificModifyMixedKeys) {
	tb.Helper()

	workspace := NewWorkspace()
	keys := slotSpecificModifyMixedKeys{}
	person := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	keys.person = person.Key()
	employee := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-employee",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	keys.employee = employee.Key()
	department := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "ssm-mixed-department",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	keys.department = department.Key()
	event := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tags", Kind: ValueList, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	keys.event = event.Key()
	customer := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-customer",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	keys.customer = customer.Key()
	block := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-block",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueString, Required: true},
			{Name: "code", Kind: ValueString, Required: true},
		},
	})
	keys.block = block.Key()
	item := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	keys.item = item.Key()
	group := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	keys.group = group.Key()
	bucketItem := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-mixed-bucket-item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	keys.bucketItem = bucketItem.Key()

	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark-list",
		Fn:   func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{Reads: []ActionBindingReadSpec{
			{Binding: "middle"},
		}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-active-person",
		Conditions: []RuleConditionSpec{{
			Binding: "person",

			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
			}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-active-event",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{Binding: "event", Target: TemplateKeyFact(event.Key())}),
			Test{Expression: CompareExpr{
				Operator: ExpressionCompareEqual,
				Left:     BindingFieldExpr{Binding: "event", Field: "status"},
				Right:    ConstExpr{Value: "active"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-list-event",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{
				ListPattern(Path("tags"),
					ListElem(ConstExpr{Value: "vip"}),
					ListSegment("middle"),
					ListElem(ConstExpr{Value: "active"}),
				),
			}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark-list"}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-customer-without-active-block",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "customer", Target: TemplateKeyFact(customer.Key())},
			Not{Condition: Match{
				Binding: "block",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: "active"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(block.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-aggregate-total",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "ssm-mixed-bucketed-aggregate-total",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group.Key())},
			Accumulate(
				Match{
					Binding: "bucketItem",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}, Target: TemplateKeyFact(bucketItem.Key()),
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "bucketItem", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	return mustCompileWorkspace(tb, workspace), keys
}

func mustSlotSpecificModifyBenchmarkRulesetWithDeclaredReads(tb testing.TB, declaredNoReads bool) (*Ruleset, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	action := ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	}
	if declaredNoReads {
		action.BindingReads = &ActionBindingReadSetSpec{}
	}
	mustAddAction(tb, workspace, action)
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-active-person",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				}, Target: TemplateKeyFact(person.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), person.Key()
}

func mustSlotSpecificModifyJoinedBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	employee := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-employee",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "ssm-department",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), employee.Key(), department.Key()
}

func mustSlotSpecificModifyFilterBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	event := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-active-event",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match(RuleConditionSpec{Binding: "event", Target: TemplateKeyFact(event.Key())}),
			Test{Expression: CompareExpr{
				Operator: ExpressionCompareEqual,
				Left:     BindingFieldExpr{Binding: "event", Field: "status"},
				Right:    ConstExpr{Value: "active"},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), event.Key()
}

func mustSlotSpecificModifyAlphaPredicateBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	person := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-predicate-person",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-alpha-predicate-person",
		Conditions: []RuleConditionSpec{{
			Binding: "person",

			Predicates: []ExpressionSpec{CompareExpr{
				Operator: ExpressionCompareGreaterOrEqual,
				Left:     CurrentFieldExpr{Field: "age"},
				Right:    ConstExpr{Value: 18},
			}}, Target: TemplateKeyFact(person.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), person.Key()
}

func mustSlotSpecificModifyListPatternBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	event := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-list-pattern-event",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tags", Kind: ValueList, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{Reads: []ActionBindingReadSpec{
			{Binding: "middle"},
		}},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-list-pattern-event",
		Conditions: []RuleConditionSpec{{
			Binding: "event",

			ListPatterns: []ListPatternSpec{
				ListPattern(Path("tags"),
					ListElem(ConstExpr{Value: "vip"}),
					ListSegment("middle"),
					ListElem(ConstExpr{Value: "active"}),
				),
			}, Target: TemplateKeyFact(event.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), event.Key()
}

func mustSlotSpecificModifyNegationBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	customer := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-customer",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	block := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-block",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "customer_id", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueString, Required: true},
			{Name: "code", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-customer-without-active-block",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "customer", Target: TemplateKeyFact(customer.Key())},
			Not{Condition: Match{
				Binding: "block",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: "active"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "customer_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "customer", Field: "id"}},
				}, Target: TemplateKeyFact(block.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), customer.Key(), block.Key()
}

func mustSlotSpecificModifyAggregateBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	item := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-aggregate-item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-aggregate-total",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), item.Key()
}

func mustSlotSpecificModifyBucketedAggregateBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	group := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-aggregate-group",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	item := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:            "ssm-bucketed-aggregate-item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "note", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name:         "mark",
		Fn:           func(ActionContext) error { return nil },
		BindingReads: &ActionBindingReadSetSpec{},
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "slot-specific-bucketed-aggregate-total",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group.Key())},
			Accumulate(
				Match{
					Binding: "item",

					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}, Target: TemplateKeyFact(item.Key()),
				},
				Count().As("count"),
				Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), group.Key(), item.Key()
}
