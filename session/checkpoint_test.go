package session_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestCheckpointPublicRoundTripAndContinuation(t *testing.T) {
	ctx := context.Background()
	workspace := session.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name: "task",
		Key:  "task",
		Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueInt, Required: true},
			{Name: "label", Kind: rules.ValueString, HasDefault: true, Default: "new"},
		},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if err := workspace.AddAction(rules.ActionSpec{Name: "noop", Fn: func(rules.ActionContext) error { return nil }}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name:       "visit-task",
		Conditions: []rules.RuleConditionSpec{{Binding: "task", Target: rules.TemplateKeyFact("task")}},
		Actions:    []rules.RuleActionSpec{{Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	ruleset, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	original, err := session.New(ruleset, session.WithSessionID("checkpoint-public"), session.WithStrategy(session.StrategyBreadth))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := original.Assert(ctx, "task", rules.MustFields("id", int64(1))); err != nil {
		t.Fatalf("Assert 1: %v", err)
	}
	if _, err := original.Assert(ctx, "task", rules.MustFields("id", int64(2), "label", "queued")); err != nil {
		t.Fatalf("Assert 2: %v", err)
	}
	if result, err := original.Run(ctx, session.WithMaxFirings(1)); err != nil || result.Fired != 1 {
		t.Fatalf("Run = (%+v, %v), want one firing", result, err)
	}

	checkpoint, err := original.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if checkpoint.FormatVersion() != session.CheckpointVersion || checkpoint.RulesetID() != ruleset.ID() || checkpoint.SessionID() != "checkpoint-public" {
		t.Fatalf("checkpoint metadata = version %d ruleset %s session %s", checkpoint.FormatVersion(), checkpoint.RulesetID(), checkpoint.SessionID())
	}
	var encoded bytes.Buffer
	if err := session.EncodeCheckpoint(&encoded, checkpoint); err != nil {
		t.Fatalf("EncodeCheckpoint: %v", err)
	}
	decoded, err := session.DecodeCheckpoint(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("DecodeCheckpoint: %v", err)
	}

	events := 0
	restored, err := session.Restore(ctx, ruleset, decoded,
		session.WithSessionID("checkpoint-restored"),
		session.WithEventListener(session.EventFunc(func(context.Context, session.Event) error {
			events++
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.ID() != "checkpoint-restored" || events != 0 {
		t.Fatalf("restored ID/events = %s/%d, want override and no historical events", restored.ID(), events)
	}
	originalSnapshot, err := original.Snapshot(ctx)
	if err != nil {
		t.Fatalf("original Snapshot: %v", err)
	}
	restoredSnapshot, err := restored.Snapshot(ctx)
	if err != nil {
		t.Fatalf("restored Snapshot: %v", err)
	}
	if !reflect.DeepEqual(checkpointFactStrings(originalSnapshot.Facts()), checkpointFactStrings(restoredSnapshot.Facts())) {
		t.Fatalf("restored facts differ: original=%v restored=%v", checkpointFactStrings(originalSnapshot.Facts()), checkpointFactStrings(restoredSnapshot.Facts()))
	}
	originalAgenda, err := original.Agenda(ctx)
	if err != nil {
		t.Fatalf("original Agenda: %v", err)
	}
	restoredAgenda, err := restored.Agenda(ctx)
	if err != nil {
		t.Fatalf("restored Agenda: %v", err)
	}
	if len(originalAgenda.Activations()) != len(restoredAgenda.Activations()) {
		t.Fatalf("pending agenda lengths = %d/%d", len(originalAgenda.Activations()), len(restoredAgenda.Activations()))
	}
	originalRun, originalErr := original.Run(ctx)
	restoredRun, restoredErr := restored.Run(ctx)
	if originalErr != nil || restoredErr != nil || originalRun.Fired != restoredRun.Fired || originalRun.Status != restoredRun.Status {
		t.Fatalf("continued runs differ: original=(%+v,%v) restored=(%+v,%v)", originalRun, originalErr, restoredRun, restoredErr)
	}
}

func TestCheckpointPublicCodecAndRestoreFailures(t *testing.T) {
	if _, err := session.DecodeCheckpoint(bytes.NewBufferString(`{}`)); !errors.Is(err, rules.ErrInvalidCheckpoint) {
		t.Fatalf("invalid decode error = %v, want ErrInvalidCheckpoint", err)
	}
	if err := session.EncodeCheckpoint(nil, session.Checkpoint{}); !errors.Is(err, rules.ErrInvalidCheckpoint) {
		t.Fatalf("nil writer error = %v, want ErrInvalidCheckpoint", err)
	}

	ctx := context.Background()
	workspace := session.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{Name: "item", Key: "item"}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	ruleset, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	sess, err := session.New(ruleset)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	checkpoint, err := sess.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	other, err := session.NewWorkspace().Compile(ctx)
	if err != nil {
		t.Fatalf("Compile other: %v", err)
	}
	if _, err := session.Restore(ctx, other, checkpoint); !errors.Is(err, rules.ErrIncompatibleRuleset) {
		t.Fatalf("mismatched restore error = %v, want ErrIncompatibleRuleset", err)
	}
	if _, err := session.Restore(ctx, ruleset, checkpoint, session.WithInitialFacts(session.InitialFact{TemplateKey: "item"})); !errors.Is(err, rules.ErrInvalidCheckpoint) {
		t.Fatalf("semantic override error = %v, want ErrInvalidCheckpoint", err)
	}
}

func checkpointFactStrings(facts []session.FactSnapshot) []string {
	out := make([]string, len(facts))
	for i, fact := range facts {
		out[i] = fact.String()
	}
	return out
}
