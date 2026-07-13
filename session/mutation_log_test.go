package session_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestMutationLogPublicRoundTripReplay(t *testing.T) {
	ctx := context.Background()
	workspace := session.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{
		Name:   "item",
		Key:    "item",
		Fields: []rules.FieldSpec{{Name: "id", Kind: rules.ValueInt, Required: true}},
	}); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	original, err := session.New(revision, session.WithSessionID("public-mutation-log"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	base, err := original.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	log, err := session.NewMutationLog(base)
	if err != nil {
		t.Fatalf("NewMutationLog: %v", err)
	}
	if _, err := original.Assert(ctx, "item", rules.MustFields("id", int64(1))); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	log, err = original.AppendMutationLog(ctx, log)
	if err != nil {
		t.Fatalf("AppendMutationLog: %v", err)
	}
	if log.FormatVersion() != session.MutationLogVersion || log.Len() != 1 {
		t.Fatalf("log metadata = version %d length %d", log.FormatVersion(), log.Len())
	}
	var encoded bytes.Buffer
	if err := session.EncodeMutationLog(&encoded, log); err != nil {
		t.Fatalf("EncodeMutationLog: %v", err)
	}
	decoded, err := session.DecodeMutationLog(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("DecodeMutationLog: %v", err)
	}
	replayed, err := session.ReplayMutationLog(ctx, revision, base, decoded)
	if err != nil {
		t.Fatalf("ReplayMutationLog: %v", err)
	}
	originalSnapshot, err := original.Snapshot(ctx)
	if err != nil {
		t.Fatalf("original Snapshot: %v", err)
	}
	replayedSnapshot, err := replayed.Snapshot(ctx)
	if err != nil {
		t.Fatalf("replayed Snapshot: %v", err)
	}
	if len(originalSnapshot.Facts()) != len(replayedSnapshot.Facts()) || originalSnapshot.Facts()[0].String() != replayedSnapshot.Facts()[0].String() {
		t.Fatalf("replayed snapshot differs: original=%v replayed=%v", originalSnapshot.Facts(), replayedSnapshot.Facts())
	}
}

func TestMutationLogPublicFailures(t *testing.T) {
	if err := session.EncodeMutationLog(nil, session.MutationLog{}); !errors.Is(err, rules.ErrInvalidMutationLog) {
		t.Fatalf("nil writer error = %v, want ErrInvalidMutationLog", err)
	}
	if _, err := session.DecodeMutationLog(bytes.NewBufferString(`{}`)); !errors.Is(err, rules.ErrInvalidMutationLog) {
		t.Fatalf("invalid decode error = %v, want ErrInvalidMutationLog", err)
	}
}
