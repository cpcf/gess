package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSessionCheckpointWireCapturesConfigurationFactsAgendaAndRefraction(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddGlobal(t, workspace, GlobalSpec{Name: "threshold", Kind: ValueInt})
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "item",
		Key:  "item",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "label", Kind: ValueString, HasDefault: true, Default: "new"},
			{Name: "note", Kind: ValueString},
			{Name: "payload", Kind: ValueMap, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "visit-item",
		Conditions: []RuleConditionSpec{{
			Binding: "item",
			Target:  TemplateKeyFact(item.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	initial := SessionInitialFact{TemplateKey: item.Key(), Fields: mustFields(t, map[string]any{
		"id": int64(1), "payload": map[string]any{"source": "initial"},
	})}
	session, err := NewSession(
		revision,
		WithSessionID("checkpoint-capture"),
		WithInitialFacts(initial),
		WithGlobals(map[string]any{"threshold": int64(7)}),
		WithStrategy(StrategyBreadth),
		WithResetBeforeSnapshot(true),
		WithMaxDemandCascadeSteps(12),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	second, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{
		"id": int64(2), "label": "queued", "payload": map[string]any{"nested": []any{int64(3), "x"}},
	}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	modified, err := session.Modify(ctx, second.Fact.ID(), FactPatch{Set: mustFields(t, map[string]any{"label": "updated"})})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if result, err := session.Run(ctx, WithMaxFirings(1)); err != nil || result.Fired != 1 {
		t.Fatalf("Run = (%+v, %v), want one firing", result, err)
	}

	document, err := session.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("checkpointWire: %v", err)
	}
	if document.Format != checkpointWireFormat || document.Version != checkpointWireVersion || document.RulesetID != revision.ID() || document.SessionID != "checkpoint-capture" {
		t.Fatalf("envelope = %+v", document)
	}
	if document.Config.Strategy != "breadth" || !document.Config.ResetBeforeSnapshot || document.Config.DemandCascadeLimit != 12 {
		t.Fatalf("config = %+v", document.Config)
	}
	if !reflect.DeepEqual(document.Config.InitialFocusStack, []ModuleName{MainModule}) || len(document.Config.InitialFacts) != 1 {
		t.Fatalf("initial config = %+v", document.Config)
	}
	if fields := document.Config.InitialFacts[0].Fields; len(fields) != 2 || fields[0].Name != "id" || fields[1].Name != "payload" {
		t.Fatalf("raw initial fields = %+v, want authored fields without applied default", fields)
	}
	if len(document.Config.Globals) != 1 || document.Config.Globals[0].Name != "threshold" {
		t.Fatalf("globals = %+v", document.Config.Globals)
	}
	threshold, err := document.Config.Globals[0].Value.value()
	if err != nil || !threshold.Equal(newIntValue(7)) {
		t.Fatalf("threshold = (%v, %v)", threshold, err)
	}

	if len(document.State.Facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(document.State.Facts))
	}
	if document.State.Generation != session.factStore.generation || document.State.NextFactSequence != session.factStore.nextFactSequence || document.State.NextRecency != session.factStore.nextRecency {
		t.Fatalf("fact allocators = %+v, session sequence/recency = %d/%d", document.State, session.factStore.nextFactSequence, session.factStore.nextRecency)
	}
	wireModified := checkpointWireFactByID(t, document.State.Facts, modified.Fact.ID())
	if wireModified.Version != modified.Fact.Version() || wireModified.Recency != modified.Fact.Recency() || wireModified.Support != FactSupportStated {
		t.Fatalf("modified fact metadata = %+v", wireModified)
	}
	label := checkpointWireFieldByName(t, wireModified.Fields, "label")
	labelValue, err := label.Value.value()
	if err != nil || !labelValue.Equal(newStringValue("updated")) || label.Presence != FieldPresenceExplicit {
		t.Fatalf("modified label = (%+v, %v, %v)", label, labelValue, err)
	}
	initialLabel := checkpointWireFieldByName(t, document.State.Facts[0].Fields, "label")
	if initialLabel.Presence != FieldPresenceDefault {
		t.Fatalf("initial label presence = %q, want default", initialLabel.Presence)
	}
	omittedNote := checkpointWireFieldByName(t, document.State.Facts[0].Fields, "note")
	if omittedNote.Presence != FieldPresenceOmitted || omittedNote.Value != nil {
		t.Fatalf("initial note = %+v, want omitted without value", omittedNote)
	}

	statuses := map[string]int{}
	for _, activation := range document.State.Agenda.Activations {
		statuses[activation.Status]++
		if activation.Ordinal == 0 || activation.Identity == (checkpointWireCandidateIdentity{}) || len(activation.FactIDs) != 1 || len(activation.FactVersions) != 1 {
			t.Fatalf("activation = %+v", activation)
		}
	}
	if statuses["pending"] != 1 || statuses["consumed"] != 1 {
		t.Fatalf("activation statuses = %v, want one pending and one consumed", statuses)
	}
	if document.State.NextRunSequence != session.nextRunSequence || document.State.NextEventSequence != session.diagnostics.nextEventSequence {
		t.Fatalf("run/event sequences = %d/%d, want %d/%d", document.State.NextRunSequence, document.State.NextEventSequence, session.nextRunSequence, session.diagnostics.nextEventSequence)
	}

	encoded, err := encodeCheckpointWire(document)
	if err != nil {
		t.Fatalf("encodeCheckpointWire: %v", err)
	}
	decoded, err := decodeCheckpointWire(encoded)
	if err != nil {
		t.Fatalf("decodeCheckpointWire: %v", err)
	}
	if !reflect.DeepEqual(decoded, document) {
		t.Fatalf("decoded capture differs:\n got %#v\nwant %#v", decoded, document)
	}
	restored, err := restoreCheckpointWire(ctx, revision, decoded)
	if err != nil {
		t.Fatalf("restoreCheckpointWire: %v", err)
	}
	restoredDocument, err := restored.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("restored checkpointWire: %v", err)
	}
	if !reflect.DeepEqual(restoredDocument, document) {
		t.Fatalf("restored checkpoint differs:\n got %#v\nwant %#v", restoredDocument, document)
	}
	originalRun, originalErr := session.Run(ctx)
	restoredRun, restoredErr := restored.Run(ctx)
	if originalErr != nil || restoredErr != nil || originalRun.Fired != restoredRun.Fired || originalRun.Status != restoredRun.Status {
		t.Fatalf("continued runs differ: original=(%+v,%v) restored=(%+v,%v)", originalRun, originalErr, restoredRun, restoredErr)
	}
	originalNext, err := session.Assert(ctx, item.Key(), mustFields(t, map[string]any{
		"id": int64(3), "payload": map[string]any{"source": "tail"},
	}))
	if err != nil {
		t.Fatalf("original tail Assert: %v", err)
	}
	restoredNext, err := restored.Assert(ctx, item.Key(), mustFields(t, map[string]any{
		"id": int64(3), "payload": map[string]any{"source": "tail"},
	}))
	if err != nil {
		t.Fatalf("restored tail Assert: %v", err)
	}
	if originalNext.Fact.ID() != restoredNext.Fact.ID() || originalNext.Fact.Recency() != restoredNext.Fact.Recency() {
		t.Fatalf("continued allocators differ: original=%s/%d restored=%s/%d", originalNext.Fact.ID(), originalNext.Fact.Recency(), restoredNext.Fact.ID(), restoredNext.Fact.Recency())
	}
}

func TestSessionCheckpointWireCapturesLogicalSupportSourcesAndCounters(t *testing.T) {
	ctx := context.Background()
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session := mustSession(t, revision, "checkpoint-logical")
	source, err := session.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-1"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if result, err := session.Run(ctx); err != nil || result.Fired != 2 {
		t.Fatalf("Run = (%+v, %v), want two firings", result, err)
	}

	document, err := session.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("checkpointWire: %v", err)
	}
	logical := document.State.LogicalSupport
	if len(logical.Edges) != 2 || logical.Counters.CurrentLogicalFacts != 2 || logical.Counters.CurrentSupportEdges != 2 || logical.Counters.SupportEdgesAdded != 2 {
		t.Fatalf("logical support = %+v", logical)
	}
	facts := make(map[checkpointWireFactID]struct{}, len(document.State.Facts))
	for _, fact := range document.State.Facts {
		facts[fact.ID] = struct{}{}
	}
	for _, edge := range logical.Edges {
		if edge.SupportID.IsZero() || edge.ActivationID.IsZero() || edge.RuleRevisionID.IsZero() || edge.Source == (checkpointWireCandidateIdentity{}) {
			t.Fatalf("support edge identity = %+v", edge)
		}
		if _, ok := facts[edge.FactID]; !ok {
			t.Fatalf("support edge target missing from facts: %+v", edge)
		}
		for _, factID := range edge.SupportingFacts {
			if _, ok := facts[factID]; !ok {
				t.Fatalf("supporting fact missing from facts: %+v", edge)
			}
		}
	}
	if _, err := encodeCheckpointWire(document); err != nil {
		t.Fatalf("encode logical checkpoint: %v", err)
	}
	restored, err := restoreCheckpointWire(ctx, revision, document)
	if err != nil {
		t.Fatalf("restore logical checkpoint: %v", err)
	}
	restoredDocument, err := restored.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("capture restored logical checkpoint: %v", err)
	}
	if !reflect.DeepEqual(restoredDocument, document) {
		t.Fatalf("restored logical checkpoint differs:\n got %#v\nwant %#v", restoredDocument, document)
	}
	if _, err := session.Retract(ctx, source.Fact.ID()); err != nil {
		t.Fatalf("original Retract: %v", err)
	}
	if _, err := restored.Retract(ctx, source.Fact.ID()); err != nil {
		t.Fatalf("restored Retract: %v", err)
	}
	originalAfter := mustSnapshot(t, ctx, session)
	restoredAfter := mustSnapshot(t, ctx, restored)
	if !reflect.DeepEqual(originalAfter.Facts(), restoredAfter.Facts()) || !reflect.DeepEqual(originalAfter.SupportGraph(), restoredAfter.SupportGraph()) {
		t.Fatalf("logical continuation differs:\noriginal=%#v %#v\nrestored=%#v %#v", originalAfter.Facts(), originalAfter.SupportGraph(), restoredAfter.Facts(), restoredAfter.SupportGraph())
	}
}

func TestSessionCheckpointWireFollowsIdleOwnershipContract(t *testing.T) {
	session := mustSession(t, mustCompile(t), "checkpoint-ownership")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.checkpointWire(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled checkpoint error = %v, want context.Canceled", err)
	}
	if !session.beginRun() {
		t.Fatal("beginRun failed")
	}
	if _, err := session.checkpointWire(context.Background()); !errors.Is(err, ErrConcurrencyMisuse) {
		session.endRun()
		t.Fatalf("active-run checkpoint error = %v, want ErrConcurrencyMisuse", err)
	}
	session.endRun()

	session.agendaDriver.markDirty()
	if _, err := session.checkpointWire(context.Background()); !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("dirty-agenda checkpoint error = %v, want ErrUnsupportedRuntime", err)
	}
}

func TestSessionCheckpointRefractionStoreTracksGraphRemovalResetAndFork(t *testing.T) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(t)
	session := mustSession(t, revision, "checkpoint-refraction")
	inserted, err := session.Assert(ctx, taskKey, mustFields(t, map[string]any{"id": int64(1)}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if result, err := session.Run(ctx); err != nil || result.Fired != 1 {
		t.Fatalf("Run = (%+v, %v), want one firing", result, err)
	}
	if len(session.refractions.byIdentity) != 1 {
		t.Fatalf("refractions after fire = %d, want 1", len(session.refractions.byIdentity))
	}
	fork, err := session.Fork(ctx, WithSessionID("checkpoint-refraction-fork"))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if len(fork.refractions.byIdentity) != 1 {
		t.Fatalf("fork refractions = %d, want 1", len(fork.refractions.byIdentity))
	}
	for key := range fork.refractions.byIdentity {
		delete(fork.refractions.byIdentity, key)
	}
	if len(session.refractions.byIdentity) != 1 {
		t.Fatal("fork refraction mutation changed parent")
	}
	if _, err := session.Retract(ctx, inserted.Fact.ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if len(session.refractions.byIdentity) != 0 {
		t.Fatalf("refractions after terminal removal = %d, want 0", len(session.refractions.byIdentity))
	}
	if _, err := session.Assert(ctx, taskKey, mustFields(t, map[string]any{"id": int64(2)})); err != nil {
		t.Fatalf("second Assert: %v", err)
	}
	if result, err := session.Run(ctx); err != nil || result.Fired != 1 {
		t.Fatalf("second Run = (%+v, %v), want one firing", result, err)
	}
	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(session.refractions.byIdentity) != 0 {
		t.Fatalf("refractions after Reset = %d, want 0", len(session.refractions.byIdentity))
	}
}

func TestRestoreCheckpointWireRejectsRulesetAndGraphDisagreement(t *testing.T) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(t)
	session := mustSession(t, revision, "checkpoint-invalid-restore")
	if _, err := session.Assert(ctx, taskKey, mustFields(t, map[string]any{"id": int64(1)})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	document, err := session.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("checkpointWire: %v", err)
	}
	if _, err := restoreCheckpointWire(ctx, mustCompile(t), document); !errors.Is(err, ErrIncompatibleRuleset) {
		t.Fatalf("mismatched ruleset error = %v, want ErrIncompatibleRuleset", err)
	}
	if len(document.State.Agenda.Activations) != 1 || len(document.State.Agenda.Activations[0].FactVersions) != 1 {
		t.Fatalf("restore corruption fixture agenda = %+v", document.State.Agenda)
	}
	document.State.Agenda.Activations[0].FactVersions[0]++
	if _, err := restoreCheckpointWire(ctx, revision, document); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("graph disagreement error = %v, want ErrInvalidCheckpoint", err)
	}
}

func TestCheckpointRestoreRebuildsGraphLifecycleShapes(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		build func(testing.TB) (*Ruleset, []SessionInitialFact)
	}{
		{name: "alpha", build: buildAlphaLifecycleRuleset},
		{name: "join", build: buildJoinLifecycleRuleset},
		{name: "negation", build: buildNegationLifecycleRuleset},
		{name: "aggregate", build: buildAggregateLifecycleRuleset},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			revision, initials := tc.build(t)
			session, err := NewSession(revision, WithInitialFacts(initials...))
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			document, err := session.checkpointWire(ctx)
			if err != nil {
				t.Fatalf("checkpointWire: %v", err)
			}
			restored, err := restoreCheckpointWire(ctx, revision, document)
			if err != nil {
				t.Fatalf("restoreCheckpointWire: %v", err)
			}
			restoredDocument, err := restored.checkpointWire(ctx)
			if err != nil {
				t.Fatalf("restored checkpointWire: %v", err)
			}
			if !reflect.DeepEqual(restoredDocument, document) {
				t.Fatalf("restored %s lifecycle differs:\n got %#v\nwant %#v", tc.name, restoredDocument, document)
			}
		})
	}
}

func TestCheckpointRestoreRebuildsActiveBackchainDemandState(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Match{
				Binding: "answer",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "kind", Operator: FieldConstraintEqual, Value: "hardware"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "id"}},
				},
				Target: TemplateKeyFact(answer.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: request.Key(),
		Fields:      mustFields(t, map[string]any{"id": "q1"}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := len(mustSnapshot(t, ctx, session).FactsByTemplateKey(demandKey)); got != 1 {
		t.Fatalf("active demands = %d, want 1", got)
	}
	beforeDiagnostics, err := session.Diagnostics(ctx)
	if err != nil {
		t.Fatalf("Diagnostics before: %v", err)
	}
	document, err := session.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("checkpointWire: %v", err)
	}
	restored, err := restoreCheckpointWire(ctx, revision, document)
	if err != nil {
		t.Fatalf("restoreCheckpointWire: %v", err)
	}
	restoredDocument, err := restored.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("restored checkpointWire: %v", err)
	}
	if !reflect.DeepEqual(restoredDocument, document) {
		t.Fatalf("restored backchain checkpoint differs:\n got %#v\nwant %#v", restoredDocument, document)
	}
	afterDiagnostics, err := restored.Diagnostics(ctx)
	if err != nil {
		t.Fatalf("Diagnostics after: %v", err)
	}
	if !reflect.DeepEqual(beforeDiagnostics.Backchain, afterDiagnostics.Backchain) {
		t.Fatalf("backchain diagnostics differ: before=%+v after=%+v", beforeDiagnostics.Backchain, afterDiagnostics.Backchain)
	}
}

func TestCheckpointRestoreRebuildsSettledBackchainState(t *testing.T) {
	ctx := context.Background()
	revision, requestKey := mustCompileBackchainRuntimeRuleset(t)
	session := mustSession(t, revision, "checkpoint-settled-backchain")
	runBackchainRuntimeDemand(t, ctx, session, requestKey)

	document, err := session.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("checkpointWire: %v", err)
	}
	restored, err := restoreCheckpointWire(ctx, revision, document)
	if err != nil {
		t.Fatalf("restoreCheckpointWire: %v", err)
	}
	restoredDocument, err := restored.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("restored checkpointWire: %v", err)
	}
	if !reflect.DeepEqual(restoredDocument, document) {
		t.Fatalf("restored settled backchain checkpoint differs:\n got %#v\nwant %#v", restoredDocument, document)
	}
}

func checkpointWireFactByID(t *testing.T, facts []checkpointWireFact, id FactID) checkpointWireFact {
	t.Helper()
	want := checkpointWireFactIDFromFactID(id)
	for _, fact := range facts {
		if fact.ID == want {
			return fact
		}
	}
	t.Fatalf("missing checkpoint fact %s", id)
	return checkpointWireFact{}
}

func checkpointWireFieldByName(t *testing.T, fields []checkpointWireField, name string) checkpointWireField {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("missing checkpoint field %q", name)
	return checkpointWireField{}
}
