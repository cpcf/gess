package engine

import (
	"context"
	"errors"
	"testing"
)

// lineageRuleset builds a session that asserts a "record" from a "trigger" via
// rule create/action open, then modifies it via rule advance/action advance.
// Both actions carry rendered .gess source so Firing.Action is populated.
func lineageRuleset(t testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	triggerKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "trigger",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	recordKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "record",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}).Key()
	mustAddAction(t, workspace, ActionSpec{
		Name:       "open",
		GessSource: `(assert (record (id ?t.id) (status "open")))`,
		Fn: func(ctx ActionContext) error {
			id, _ := ctx.BindingScalarValue("t", "id")
			_, err := ctx.AssertTemplate(recordKey, Fields{"id": id, "status": newStringValue("open")})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name:       "advance",
		GessSource: `(modify ?r (set (status "active")))`,
		Fn: func(ctx ActionContext) error {
			record, _ := ctx.Binding("r")
			_, err := ctx.Modify(record.ID(), FactPatch{Set: Fields{"status": newStringValue("active")}})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "create",
		Conditions: []RuleConditionSpec{{Binding: "t", Target: TemplateKeyFact(triggerKey)}},
		Actions:    []RuleActionSpec{{Name: "open"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "advance",
		Conditions: []RuleConditionSpec{{
			Binding: "r",
			Target:  TemplateKeyFact(recordKey),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
			},
		}},
		Actions: []RuleActionSpec{{Name: "advance"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, triggerKey, recordKey
}

func TestSessionExplainWithoutLogUnavailable(t *testing.T) {
	revision, triggerKey, _ := lineageRuleset(t)
	session := mustSession(t, revision, "explain-no-log")
	if _, err := session.AssertTemplate(context.Background(), triggerKey, mustFields(t, map[string]any{"id": "t-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	record := singleFact(t, snapshot, "record")
	if _, err := session.Explain(context.Background(), record.ID()); !errors.Is(err, ErrExplainLogUnavailable) {
		t.Fatalf("Session.Explain without log err = %v, want ErrExplainLogUnavailable", err)
	}
	// Tier 1 still works from the snapshot.
	if _, ok := snapshot.Explain(record.ID()); !ok {
		t.Fatalf("Snapshot.Explain fallback failed")
	}
}

func TestSessionExplainLineageAndActionSource(t *testing.T) {
	revision, triggerKey, _ := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("explain-lineage"), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), triggerKey, mustFields(t, map[string]any{"id": "t-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	record := singleFact(t, snapshot, "record")

	derivation, err := session.Explain(context.Background(), record.ID())
	if err != nil {
		t.Fatalf("Session.Explain: %v", err)
	}
	if derivation.ProducedBy == nil {
		t.Fatalf("ProducedBy nil, want the advance firing")
	}
	if derivation.ProducedBy.RuleName != "advance" {
		t.Fatalf("ProducedBy.RuleName = %q, want advance", derivation.ProducedBy.RuleName)
	}
	if derivation.ProducedBy.Action != `(modify ?r (set (status "active")))` {
		t.Fatalf("ProducedBy.Action = %q, want rendered advance source", derivation.ProducedBy.Action)
	}
	if !derivation.ProducedBy.BindingsPartial {
		t.Fatalf("ProducedBy.BindingsPartial = false, want true for reconstruction")
	}

	if len(derivation.History) != 2 {
		t.Fatalf("History len = %d, want 2 (assert, modify)", len(derivation.History))
	}
	assert, modify := derivation.History[0], derivation.History[1]
	if assert.Kind != MutationAssert || assert.Firing == nil || assert.Firing.RuleName != "create" {
		t.Fatalf("assert record = %+v, want rule create", assert)
	}
	if modify.Kind != MutationModify || modify.Firing == nil || modify.Firing.RuleName != "advance" {
		t.Fatalf("modify record = %+v, want rule advance", modify)
	}
	if modify.Sequence <= assert.Sequence {
		t.Fatalf("history out of order: assert seq %d, modify seq %d", assert.Sequence, modify.Sequence)
	}
	if len(modify.ChangedFields) != 1 || modify.ChangedFields[0].Field != "status" {
		t.Fatalf("modify ChangedFields = %+v, want status change", modify.ChangedFields)
	}
	old, _ := modify.ChangedFields[0].Old.AsString()
	updated, _ := modify.ChangedFields[0].New.AsString()
	if old != "open" || updated != "active" {
		t.Fatalf("status change = %q -> %q, want open -> active", old, updated)
	}
}

func TestSessionExplainMultiActionAttribution(t *testing.T) {
	workspace := NewWorkspace()
	srcKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "src",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	aKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "a",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	bKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "b",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	mustAddAction(t, workspace, ActionSpec{Name: "make-a", Fn: func(ctx ActionContext) error {
		id, _ := ctx.BindingScalarValue("s", "id")
		_, err := ctx.AssertTemplate(aKey, Fields{"id": id})
		return err
	}})
	mustAddAction(t, workspace, ActionSpec{Name: "make-b", Fn: func(ctx ActionContext) error {
		id, _ := ctx.BindingScalarValue("s", "id")
		_, err := ctx.AssertTemplate(bKey, Fields{"id": id})
		return err
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "fan-out",
		Conditions: []RuleConditionSpec{{Binding: "s", Target: TemplateKeyFact(srcKey)}},
		Actions:    []RuleActionSpec{{Name: "make-a"}, {Name: "make-b"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("explain-multi"), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), srcKey, mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)

	for _, tc := range []struct {
		name   string
		action string
	}{{"a", "make-a"}, {"b", "make-b"}} {
		fact := singleFact(t, snapshot, tc.name)
		derivation, err := session.Explain(context.Background(), fact.ID())
		if err != nil {
			t.Fatalf("Explain(%s): %v", tc.name, err)
		}
		if len(derivation.History) != 1 || derivation.History[0].Firing == nil {
			t.Fatalf("fact %s history = %+v, want one attributed assert", tc.name, derivation.History)
		}
	}
}

func TestExplainLogRecordsRetractLineage(t *testing.T) {
	revision, triggerKey, _ := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("explain-retract"), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	log := session.explainLog

	if _, err := session.AssertTemplate(context.Background(), triggerKey, mustFields(t, map[string]any{"id": "t-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	recordID := singleFact(t, snapshot, "record").ID()
	if _, err := session.Retract(context.Background(), recordID); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	entries, truncated := log.historyForFact(recordID)
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	if len(entries) != 3 {
		t.Fatalf("record log entries = %d, want 3 (assert, modify, retract)", len(entries))
	}
	wantKinds := []MutationKind{MutationAssert, MutationModify, MutationRetract}
	for i, want := range wantKinds {
		if entries[i].kind != want {
			t.Fatalf("entry %d kind = %q, want %q", i, entries[i].kind, want)
		}
	}
	if entries[0].actionName != "open" || entries[1].actionName != "advance" {
		t.Fatalf("action attribution = %q, %q; want open, advance", entries[0].actionName, entries[1].actionName)
	}
}

func TestSessionExplainEvictionTruncatesHistory(t *testing.T) {
	revision, _, recordKey := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("explain-evict"), WithExplainLog(WithExplainLogMaxEntries(2)))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record, err := session.AssertTemplate(context.Background(), recordKey, mustFields(t, map[string]any{"id": "r-1", "status": "open"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	recordID := record.Fact.ID()
	for _, status := range []string{"active", "closed"} {
		if _, err := session.Modify(context.Background(), recordID, FactPatch{Set: Fields{"status": newStringValue(status)}}); err != nil {
			t.Fatalf("Modify(%s): %v", status, err)
		}
	}

	derivation, err := session.Explain(context.Background(), recordID)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !derivation.Truncated {
		t.Fatalf("Truncated = false, want true after eviction")
	}
	if len(derivation.History) != 2 {
		t.Fatalf("History len = %d, want 2 after eviction of the assert", len(derivation.History))
	}
}

func TestSessionExplainResetEmptiesLog(t *testing.T) {
	revision, _, recordKey := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("explain-reset"), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record, err := session.AssertTemplate(context.Background(), recordKey, mustFields(t, map[string]any{"id": "r-1", "status": "open"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if entries, _ := session.explainLog.historyForFact(record.Fact.ID()); len(entries) == 0 {
		t.Fatalf("log did not record the assert")
	}
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if session.explainLog.total != 0 {
		t.Fatalf("log total after reset = %d, want 0", session.explainLog.total)
	}
}

func TestSessionExplainForkRequiresReoptIn(t *testing.T) {
	revision, triggerKey, _ := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("explain-fork-base"), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), triggerKey, mustFields(t, map[string]any{"id": "t-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var parentMaxSeq uint64
	for _, entries := range session.explainLog.byFact {
		for _, entry := range entries {
			if entry.sequence > parentMaxSeq {
				parentMaxSeq = entry.sequence
			}
		}
	}

	plainFork, err := session.Fork(context.Background(), WithSessionID("explain-fork-plain"))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer plainFork.Close()
	forkSnap := mustSnapshot(t, context.Background(), plainFork)
	recordID := singleFact(t, forkSnap, "record").ID()
	if _, err := plainFork.Explain(context.Background(), recordID); !errors.Is(err, ErrExplainLogUnavailable) {
		t.Fatalf("fork without re-opt-in Explain err = %v, want ErrExplainLogUnavailable", err)
	}

	loggedFork, err := session.Fork(context.Background(), WithSessionID("explain-fork-logged"), WithExplainLog())
	if err != nil {
		t.Fatalf("Fork(WithExplainLog): %v", err)
	}
	defer loggedFork.Close()
	if loggedFork.explainLog.total != 0 {
		t.Fatalf("re-opted fork log total = %d, want 0 (fresh)", loggedFork.explainLog.total)
	}
	if _, err := loggedFork.AssertTemplate(context.Background(), triggerKey, mustFields(t, map[string]any{"id": "t-2"})); err != nil {
		t.Fatalf("fork AssertTemplate: %v", err)
	}
	var forkMinSeq uint64
	for _, entries := range loggedFork.explainLog.byFact {
		for _, entry := range entries {
			if forkMinSeq == 0 || entry.sequence < forkMinSeq {
				forkMinSeq = entry.sequence
			}
		}
	}
	if forkMinSeq <= parentMaxSeq {
		t.Fatalf("fork sequence %d not monotonic past parent max %d", forkMinSeq, parentMaxSeq)
	}
}

func BenchmarkSessionExplainLogRecording(b *testing.B) {
	revision, _, recordKey := lineageRuleset(b)
	session, err := NewSession(revision, WithSessionID("explain-log-bench"), WithExplainLog())
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	fields := mustFields(b, map[string]any{"id": "r-1", "status": "open"})
	b.ReportAllocs()
	for b.Loop() {
		record, err := session.AssertTemplate(ctx, recordKey, fields)
		if err != nil {
			b.Fatalf("AssertTemplate: %v", err)
		}
		if _, err := session.Modify(ctx, record.Fact.ID(), FactPatch{Set: Fields{"status": newStringValue("active")}}); err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if _, err := session.Reset(ctx); err != nil {
			b.Fatalf("Reset: %v", err)
		}
	}
}

func BenchmarkSessionAssertNoExplainLog(b *testing.B) {
	revision, _, recordKey := lineageRuleset(b)
	session, err := NewSession(revision, WithSessionID("explain-log-off-bench"))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	fields := mustFields(b, map[string]any{"id": "r-1", "status": "open"})
	b.ReportAllocs()
	for b.Loop() {
		record, err := session.AssertTemplate(ctx, recordKey, fields)
		if err != nil {
			b.Fatalf("AssertTemplate: %v", err)
		}
		if _, err := session.Modify(ctx, record.Fact.ID(), FactPatch{Set: Fields{"status": newStringValue("active")}}); err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if _, err := session.Reset(ctx); err != nil {
			b.Fatalf("Reset: %v", err)
		}
	}
}
