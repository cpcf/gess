package engine

import (
	"context"
	"testing"
)

func TestTemplateValueBatchAppliesAgendaDeltasWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	var actions []string
	session, templateKey := mustTemplateValueBatchSession(t, &actions)

	err := session.insertTemplateValuesBatchWithContext(ctx, func(batch *templateValueBatch) error {
		return batch.insert(templateKey, []Value{mustValue(t, "Ada"), mustValue(t, "active")})
	})
	if err != nil {
		t.Fatalf("insertTemplateValuesBatchWithContext: %v", err)
	}
	assertTemplateValueBatchUsedAgendaDelta(t, session)
	assertTemplateValueBatchUsedCompactSlots(t, session, templateKey, 2)

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("run fired = %d, want 1", result.Fired)
	}
	if len(actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(actions))
	}
}

func TestPreparedTemplateValueBatchAppliesAgendaDeltasWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	var actions []string
	session, templateKey := mustTemplateValueBatchSession(t, &actions)
	inserter, err := session.prepareTemplateValueInserter(templateKey)
	if err != nil {
		t.Fatalf("prepareTemplateValueInserter: %v", err)
	}

	err = session.insertPreparedTemplateValuesBatchWithContext(ctx, func(batch *preparedTemplateValueBatch) error {
		return inserter.insert2(batch, mustValue(t, "Ada"), mustValue(t, "active"))
	})
	if err != nil {
		t.Fatalf("insertPreparedTemplateValuesBatchWithContext: %v", err)
	}
	assertTemplateValueBatchUsedAgendaDelta(t, session)
	assertTemplateValueBatchUsedCompactSlots(t, session, templateKey, 2)

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 {
		t.Fatalf("run fired = %d, want 1", result.Fired)
	}
	if len(actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(actions))
	}
}

func assertTemplateValueBatchUsedCompactSlots(t testing.TB, session *Session, templateKey TemplateKey, wantSlots int) {
	t.Helper()
	ids := session.factIDsByTemplate(templateKey)
	if got := len(ids); got != 1 {
		t.Fatalf("template facts = %d, want 1", got)
	}
	generatedFact := mustWorkingFactByID(t, session, ids[0])
	if got := len(generatedFact.fieldSlotSlice()); got != 0 {
		t.Fatalf("generated fact retained wide slots = %d, want 0", got)
	}
	if got := len(session.slotStorage); got != 0 {
		t.Fatalf("generated wide slot storage = %d, want 0", got)
	}
	if got, want := len(generatedFact.compactFieldSlots(session.compactSlotStore)), wantSlots; got != want {
		t.Fatalf("generated compact slots = %d, want %d", got, want)
	}
}

func mustTemplateValueBatchSession(t testing.TB, actions *[]string) (*Session, TemplateKey) {
	t.Helper()
	ctx := context.Background()
	workspace := NewWorkspace()
	templateKey := TemplateKey("item")
	if err := workspace.AddTemplate(TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(item): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "record-active",
		Fn: func(ActionContext) error {
			*actions = append(*actions, "active")
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(record-active): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "active-item",
		Conditions: []RuleConditionSpec{
			{
				Binding: "item",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: mustValue(t, "active")},
				}, Target: TemplateKeyFact(templateKey),
			},
		},
		Actions: []RuleActionSpec{{Name: "record-active"}},
	}); err != nil {
		t.Fatalf("AddRule(active-item): %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "template-value-batch-session")
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	if result.Fired != 0 {
		t.Fatalf("initial run fired = %d, want 0", result.Fired)
	}
	session.attachPropagationCounters()
	return session, templateKey
}

func assertTemplateValueBatchUsedAgendaDelta(t testing.TB, session *Session) {
	t.Helper()
	counters := session.propagationCounterSnapshot().Totals
	if got := counters.SteadyStateWholeTerminalScans; got != 0 {
		t.Fatalf("steady-state whole-terminal scans after template batch = %d, want 0", got)
	}
	if got := counters.FullAgendaReconciles; got != 0 {
		t.Fatalf("full agenda reconciles after template batch = %d, want 0", got)
	}
	if got := counters.AgendaDeltaApplications; got != 1 {
		t.Fatalf("agenda delta applications after template batch = %d, want 1", got)
	}
}
