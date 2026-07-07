package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestSessionAssertUniqueKeyReplaceRestoresOldFactOnPropagationFailure verifies
// the replacement is atomic: when the new fact's propagation fails, the retract
// of the old fact is rolled back so the pre-existing fact survives intact.
func TestSessionAssertUniqueKeyReplaceRestoresOldFactOnPropagationFailure(t *testing.T) {
	workspace := NewWorkspace()
	metric := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "metric",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "status-ok",
		Args:   []ValueKind{ValueString},
		Return: ValueBool,
		Func: func(_ context.Context, args []Value) (Value, error) {
			status, _ := args[0].AsString()
			if status == "bad" {
				return Value{}, fmt.Errorf("status propagation failed")
			}
			return NewValue(true)
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "status-rule",
		Conditions: []RuleConditionSpec{{
			Binding:    "metric",
			Target:     TemplateKeyFact(metric.Key()),
			Predicates: []ExpressionSpec{Call("status-ok", CurrentFieldExpr{Field: "status"})},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "replace-rollback-session")

	first, err := session.Assert(context.Background(), metric.Key(), mustFields(t, map[string]any{"id": "m1", "status": "good"}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}

	// A differing replace whose new value fails predicate propagation must not
	// destroy the pre-existing fact.
	if _, err := session.Assert(context.Background(), metric.Key(), mustFields(t, map[string]any{"id": "m1", "status": "bad"})); err == nil {
		t.Fatalf("expected propagation failure for status=bad")
	}

	if _, ok := session.workingFactByID(first.Fact.ID()); !ok {
		t.Fatalf("failed replace destroyed the pre-existing fact %q", first.Fact.ID())
	}
	if id, ok := session.factIDForDuplicateKey(first.DuplicateKey); !ok || id != first.Fact.ID() {
		t.Fatalf("duplicate key resolves to %q (ok=%v) after failed replace, want %q", id, ok, first.Fact.ID())
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	count := 0
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() != metric.Key() {
			continue
		}
		count++
		if got, _ := fact.Field("status"); !got.Equal(mustValue(t, "good")) {
			t.Fatalf("surviving metric status = %v, want good", got)
		}
	}
	if count != 1 {
		t.Fatalf("metric facts after failed replace = %d, want 1", count)
	}

	// The session is not wedged: a subsequent valid replace still succeeds.
	replaced, err := session.Assert(context.Background(), metric.Key(), mustFields(t, map[string]any{"id": "m1", "status": "ok"}))
	if err != nil {
		t.Fatalf("post-failure replace: %v", err)
	}
	if replaced.Status != AssertReplaced || replaced.Fact.ID() == first.Fact.ID() {
		t.Fatalf("post-failure replace status=%v id=%v, want replaced with a new id", replaced.Status, replaced.Fact.ID())
	}
}

// TestSessionAssertTemplateValuesUniqueKeyMapStoredIdenticalIsNoOp verifies the
// slot-path comparator reads a map-stored existing fact correctly: an identical
// re-assert through the generated slot path stays a no-op instead of being
// treated as a difference.
func TestSessionAssertTemplateValuesUniqueKeyMapStoredIdenticalIsNoOp(t *testing.T) {
	workspace := NewWorkspace()
	acct := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "acct",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "bal", Kind: ValueInt, Required: true},
		},
	})
	// Referenced only by a query, so acct facts are retained but stored as a
	// field map (not slot-backed), exercising the map-stored comparison path.
	if err := workspace.AddQuery(QuerySpec{
		Name:          "accts",
		ConditionTree: Match{Binding: "acct", Target: TemplateKeyFact(acct.Key())},
		Returns:       []QueryReturnSpec{{Alias: "acct", Binding: "acct"}},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "map-stored-noop-session")

	first, err := session.Assert(context.Background(), acct.Key(), mustFields(t, map[string]any{"id": "a", "bal": 10}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	if got := len(mustWorkingFactByID(t, session, first.Fact.ID()).fieldSlotSlice()); got != 0 {
		t.Fatalf("expected map-stored fact (0 slots), got %d", got)
	}

	// Identical values through the generated slot path must not replace.
	// AssertTemplateValues takes values in compiled field order, which may
	// differ from declaration order, so build the slice from the template.
	template, _ := session.revision.Template("acct")
	byName := map[string]Value{"id": newStringValue("a"), "bal": newIntValue(10)}
	values := make([]Value, len(template.fields))
	for i, field := range template.fields {
		values[i] = byName[field.Name]
	}
	if err := session.AssertTemplateValues(context.Background(), acct.Key(), values...); err != nil {
		t.Fatalf("AssertTemplateValues: %v", err)
	}
	if id, ok := session.factIDForDuplicateKey(first.DuplicateKey); !ok || id != first.Fact.ID() {
		t.Fatalf("identical AssertTemplateValues replaced the fact: key resolves to %q, want %q", id, first.Fact.ID())
	}
	if snapshot := mustSnapshot(t, context.Background(), session); snapshot.Len() != 1 {
		t.Fatalf("facts after identical AssertTemplateValues = %d, want 1", snapshot.Len())
	}
}

// TestSessionAssertUniqueKeyReplacesOnDifferingFields verifies that a host
// Session.Assert with a matching unique key but differing non-key fields
// retracts the old fact and inserts a new one (new fact ID), while an
// identical re-assert stays a no-op.
func TestSessionAssertUniqueKeyReplacesOnDifferingFields(t *testing.T) {
	workspace := NewWorkspace()
	event := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	var fired int
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { fired++; return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "targeted-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "fact", Target: TemplateKeyFact(event.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	session := mustSession(t, mustCompileWorkspace(t, workspace), "unique-replace-session")

	first, err := session.Assert(context.Background(), event.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "open",
	}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	if !first.Inserted() {
		t.Fatalf("first assert status = %v, want inserted", first.Status)
	}
	if result, err := session.Run(context.Background()); err != nil || result.Fired != 1 {
		t.Fatalf("first run = (%v, %d, %v), want 1 firing", result.Status, result.Fired, err)
	}

	second, err := session.Assert(context.Background(), event.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "closed",
	}))
	if err != nil {
		t.Fatalf("replace assert: %v", err)
	}
	if second.Status != AssertReplaced {
		t.Fatalf("replace status = %v, want %v", second.Status, AssertReplaced)
	}
	if second.Fact.ID() == first.Fact.ID() {
		t.Fatalf("replace reused fact ID %q, want a new fact ID", first.Fact.ID())
	}
	if second.DuplicateKey == "" || second.DuplicateKey != first.DuplicateKey {
		t.Fatalf("replace duplicate key = %q, want %q", second.DuplicateKey, first.DuplicateKey)
	}
	if second.Delta == nil {
		t.Fatalf("replace should return a mutation delta")
	}
	if got, _ := second.Fact.Field("status"); !got.Equal(mustValue(t, "closed")) {
		t.Fatalf("replaced fact status = %v, want closed", got)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 1 {
		t.Fatalf("snapshot length = %d, want 1 (one fact per key)", snapshot.Len())
	}
	if id, ok := session.factIDForDuplicateKey(first.DuplicateKey); !ok || id != second.Fact.ID() {
		t.Fatalf("duplicate key resolves to %q (ok=%v), want new id %q", id, ok, second.Fact.ID())
	}
	if _, ok := session.workingFactByID(first.Fact.ID()); ok {
		t.Fatalf("old fact %q should no longer be in working memory", first.Fact.ID())
	}

	if result, err := session.Run(context.Background()); err != nil || result.Fired != 1 {
		t.Fatalf("post-replace run = (%v, %d, %v), want the replacement fact to re-fire once", result.Status, result.Fired, err)
	}

	identical, err := session.Assert(context.Background(), event.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "closed",
	}))
	if err != nil {
		t.Fatalf("identical re-assert: %v", err)
	}
	if identical.Status != AssertExisting {
		t.Fatalf("identical re-assert status = %v, want %v", identical.Status, AssertExisting)
	}
	if identical.Fact.ID() != second.Fact.ID() {
		t.Fatalf("identical re-assert id = %q, want %q", identical.Fact.ID(), second.Fact.ID())
	}
	if identical.Delta != nil {
		t.Fatalf("identical re-assert should not return a delta: %#v", identical.Delta)
	}
}

// TestSessionAssertUniqueKeyIdenticalAfterDefaultsIsNoOp verifies that a
// unique-key assert whose fields are identical to the existing fact only after
// template defaults are applied is treated as a no-op, not a replacement.
func TestSessionAssertUniqueKeyIdenticalAfterDefaultsIsNoOp(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "audit",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active", HasDefault: true},
		},
	})
	session := mustSession(t, revision, "unique-default-noop-session")
	template, ok := revision.Template("audit")
	if !ok {
		t.Fatal("expected template audit")
	}

	first, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "a"}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	// Re-assert supplying status explicitly equal to the default: identical after defaults.
	second, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{
		"id":     "a",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("identical-after-default assert: %v", err)
	}
	if second.Status != AssertExisting {
		t.Fatalf("identical-after-default status = %v, want %v", second.Status, AssertExisting)
	}
	if second.Fact.ID() != first.Fact.ID() {
		t.Fatalf("identical-after-default reused id = %q, want %q", second.Fact.ID(), first.Fact.ID())
	}
}

// TestSessionAssertUniqueKeyReplaceEmitsRetractThenAssertEvents verifies the
// replacement surfaces as a fact-retracted event for the old fact followed by a
// fact-asserted event for the new fact, both carrying the shared duplicate key.
func TestSessionAssertUniqueKeyReplaceEmitsRetractThenAssertEvents(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	})
	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("unique-replace-events-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected template event")
	}

	first, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "open",
	}))
	if err != nil {
		t.Fatalf("first assert: %v", err)
	}
	before := len(collector.Events())

	second, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "closed",
	}))
	if err != nil {
		t.Fatalf("replace assert: %v", err)
	}

	events := collector.Events()
	if got := len(events) - before; got != 2 {
		t.Fatalf("replace emitted %d events, want 2 (retract, assert): %#v", got, events[before:])
	}
	retract := events[before]
	assert := events[before+1]
	if retract.Type != EventFactRetracted {
		t.Fatalf("first replace event = %q, want %q", retract.Type, EventFactRetracted)
	}
	if len(retract.FactIDs) != 1 || retract.FactIDs[0] != first.Fact.ID() {
		t.Fatalf("retract event fact ids = %v, want [%q]", retract.FactIDs, first.Fact.ID())
	}
	if retract.Delta == nil || retract.Delta.OldDuplicate != first.DuplicateKey {
		t.Fatalf("retract event delta = %#v, want OldDuplicate %q", retract.Delta, first.DuplicateKey)
	}
	if assert.Type != EventFactAsserted {
		t.Fatalf("second replace event = %q, want %q", assert.Type, EventFactAsserted)
	}
	if len(assert.FactIDs) != 1 || assert.FactIDs[0] != second.Fact.ID() {
		t.Fatalf("assert event fact ids = %v, want [%q]", assert.FactIDs, second.Fact.ID())
	}
	if assert.Delta == nil || assert.Delta.NewDuplicate != first.DuplicateKey {
		t.Fatalf("assert event delta = %#v, want NewDuplicate %q", assert.Delta, first.DuplicateKey)
	}
}

// TestRuleActionAssertUniqueKeyReplacesSummary drives the tutorial-shaped
// scenario: an accumulate rule asserts a unique-key summary fact from its RHS.
// Adding another member replaces the summary in place with the new count/total,
// exercising the replace path while a run is in flight (recorded agenda delta).
func TestRuleActionAssertUniqueKeyReplacesSummary(t *testing.T) {
	workspace := NewWorkspace()
	vuln := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "vulnerability",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	summary := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "critical-summary",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"severity"},
		Fields: []FieldSpec{
			{Name: "severity", Kind: ValueString, Required: true},
			{Name: "count", Kind: ValueInt, Required: true},
			{Name: "total", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "summarize",
		Fn: func(ctx ActionContext) error {
			count, _ := ctx.BindingValue("count")
			total, _ := ctx.BindingValue("total")
			_, err := ctx.Assert(summary.Key(), Fields{
				"severity": mustValue(t, "critical"),
				"count":    count,
				"total":    total,
			})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "summarize-critical",
		ConditionTree: Accumulate(
			Match{Binding: "vulnerability", Target: TemplateKeyFact(vuln.Key())},
			Count().As("count"),
			Sum(BindingFieldExpr{Binding: "vulnerability", Field: "score"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "summarize"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "critical-summaries",
		ConditionTree: Match{Binding: "summary", Target: TemplateKeyFact(summary.Key())},
		Returns:       []QueryReturnSpec{{Alias: "summary", Binding: "summary"}},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "summary-replace-session")

	if _, err := session.Assert(context.Background(), vuln.Key(), mustFields(t, map[string]any{"id": "VULN-100", "score": 98})); err != nil {
		t.Fatalf("assert VULN-100: %v", err)
	}
	if _, err := session.Assert(context.Background(), vuln.Key(), mustFields(t, map[string]any{"id": "VULN-400", "score": 97})); err != nil {
		t.Fatalf("assert VULN-400: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}

	firstID := mustSingleSummary(t, session, summary.Key(), 2, 195)

	if _, err := session.Assert(context.Background(), vuln.Key(), mustFields(t, map[string]any{"id": "VULN-900", "score": 100})); err != nil {
		t.Fatalf("assert VULN-900: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}

	secondID := mustSingleSummary(t, session, summary.Key(), 3, 295)
	if secondID == firstID {
		t.Fatalf("replacement summary reused fact ID %q, want a new fact ID", firstID)
	}
}

// TestRuleActionNativeAssertUniqueKeyReplaces exercises the native rule-RHS
// assert path (generated compact/broad slots) replacing a unique-key fact when
// its non-key fields differ.
func TestRuleActionNativeAssertUniqueKeyReplaces(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "source",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "seq", Kind: ValueInt, Required: true},
			{Name: "label", Kind: ValueString, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "current-label",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"key"},
		Fields: []FieldSpec{
			{Name: "key", Kind: ValueString, Required: true},
			{Name: "label", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "record-label",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				ConstExpr{Value: "latest"},
				BindingFieldExpr{Binding: "source", Field: "label"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "record-latest-label",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "record-label"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "current-labels",
		ConditionTree: Match{Binding: "label", Target: TemplateKeyFact(generated.Key())},
		Returns:       []QueryReturnSpec{{Alias: "label", Binding: "label"}},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "native-replace-session")

	if _, err := session.Assert(context.Background(), source.Key(), mustFields(t, map[string]any{"seq": 1, "label": "alpha"})); err != nil {
		t.Fatalf("assert source 1: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	firstID := mustSingleLabel(t, session, generated.Key(), "alpha")

	if _, err := session.Assert(context.Background(), source.Key(), mustFields(t, map[string]any{"seq": 2, "label": "bravo"})); err != nil {
		t.Fatalf("assert source 2: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	secondID := mustSingleLabel(t, session, generated.Key(), "bravo")
	if secondID == firstID {
		t.Fatalf("native replacement reused fact ID %q, want a new fact ID", firstID)
	}
}

// TestRuleActionAssertLogicalUniqueKeyReplaces verifies that replacing a
// logically-supported unique-key fact via a rule-RHS assert-logical fully
// retracts the old fact (dropping the support it received) and inserts a fresh
// logical fact that carries new support from the current activation.
func TestRuleActionAssertLogicalUniqueKeyReplaces(t *testing.T) {
	workspace := NewWorkspace()
	trigger := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "trigger",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "phase", Kind: ValueInt, Required: true},
		},
	})
	status := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "status",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"name"},
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "phase", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "derive-status",
		Fn: func(ctx ActionContext) error {
			phase, ok := ctx.BindingScalarValue("trigger", "phase")
			if !ok {
				return errors.New("missing trigger phase binding")
			}
			_, err := ctx.AssertLogical(status.Key(), Fields{
				"name":  mustValue(t, "current"),
				"phase": phase,
			})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "derive",
		Conditions: []RuleConditionSpec{
			{Binding: "trigger", Target: TemplateKeyFact(trigger.Key())},
		},
		Actions: []RuleActionSpec{{Name: "derive-status"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "statuses",
		ConditionTree: Match{Binding: "status", Target: TemplateKeyFact(status.Key())},
		Returns:       []QueryReturnSpec{{Alias: "status", Binding: "status"}},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}
	session := mustSession(t, mustCompileWorkspace(t, workspace), "logical-replace-session")

	if _, err := session.Assert(context.Background(), trigger.Key(), mustFields(t, map[string]any{"id": "t1", "phase": 1})); err != nil {
		t.Fatalf("assert t1: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	firstID, firstState := mustSingleStatus(t, session, status.Key(), 1)
	if firstState != FactSupportLogical {
		t.Fatalf("first status support = %v, want %v", firstState, FactSupportLogical)
	}
	if got := session.currentSupportGraph().Counters.CurrentSupportEdges; got != 1 {
		t.Fatalf("support edges after first derive = %d, want 1", got)
	}

	if _, err := session.Assert(context.Background(), trigger.Key(), mustFields(t, map[string]any{"id": "t2", "phase": 2})); err != nil {
		t.Fatalf("assert t2: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	secondID, secondState := mustSingleStatus(t, session, status.Key(), 2)
	if secondID == firstID {
		t.Fatalf("logical replacement reused fact ID %q, want a new fact ID", firstID)
	}
	if secondState != FactSupportLogical {
		t.Fatalf("replacement status support = %v, want %v", secondState, FactSupportLogical)
	}
	// The old fact's received support edge is gone; only the current fact's
	// edge remains.
	if got := session.currentSupportGraph().Counters.CurrentSupportEdges; got != 1 {
		t.Fatalf("support edges after replace = %d, want 1", got)
	}
}

func mustSingleStatus(t *testing.T, session *Session, key TemplateKey, wantPhase int64) (FactID, FactSupportState) {
	t.Helper()
	snapshot := mustSnapshot(t, context.Background(), session)
	var found FactID
	var state FactSupportState
	seen := 0
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() != key {
			continue
		}
		seen++
		found = fact.ID()
		state = fact.Support().State
		phase, _ := fact.Field("phase")
		if !phase.Equal(mustValue(t, wantPhase)) {
			t.Fatalf("status phase = %v, want %d", phase, wantPhase)
		}
	}
	if seen != 1 {
		t.Fatalf("status facts = %d, want exactly 1", seen)
	}
	return found, state
}

func mustSingleLabel(t *testing.T, session *Session, key TemplateKey, wantLabel string) FactID {
	t.Helper()
	snapshot := mustSnapshot(t, context.Background(), session)
	var found FactID
	seen := 0
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() != key {
			continue
		}
		seen++
		found = fact.ID()
		label, _ := fact.Field("label")
		if !label.Equal(mustValue(t, wantLabel)) {
			t.Fatalf("label = %v, want %q", label, wantLabel)
		}
	}
	if seen != 1 {
		t.Fatalf("%q facts = %d, want exactly 1", key, seen)
	}
	return found
}

// mustSingleSummary asserts there is exactly one working-memory fact for the
// given unique-key template with the expected count/total, returning its ID.
func mustSingleSummary(t *testing.T, session *Session, key TemplateKey, wantCount, wantTotal int64) FactID {
	t.Helper()
	snapshot := mustSnapshot(t, context.Background(), session)
	var found FactID
	seen := 0
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() != key {
			continue
		}
		seen++
		found = fact.ID()
		count, _ := fact.Field("count")
		total, _ := fact.Field("total")
		if !count.Equal(mustValue(t, wantCount)) || !total.Equal(mustValue(t, wantTotal)) {
			t.Fatalf("summary = count %v total %v, want count %d total %d", count, total, wantCount, wantTotal)
		}
	}
	if seen != 1 {
		t.Fatalf("summary facts = %d, want exactly 1", seen)
	}
	return found
}
