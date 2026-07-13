package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMutationLogWireCanonicalEmptyContract(t *testing.T) {
	base := Checkpoint{document: minimalCheckpointWireDocument()}
	log, err := NewMutationLog(base)
	if err != nil {
		t.Fatalf("NewMutationLog: %v", err)
	}
	encoded, err := encodeMutationLogWire(log.document)
	if err != nil {
		t.Fatalf("encode mutation log: %v", err)
	}
	digest, err := checkpointWireDigest(base.document)
	if err != nil {
		t.Fatalf("base digest: %v", err)
	}
	want := fmt.Sprintf(`{"format":"gess/session-mutation-log","version":1,"rulesetId":"ruleset:contract","sessionId":"session:contract","baseCheckpointDigest":%q,"records":[]}`, digest)
	if string(encoded) != want {
		t.Fatalf("canonical mutation log =\n%s\nwant\n%s", encoded, want)
	}
	decoded, err := decodeMutationLogWire(encoded)
	if err != nil {
		t.Fatalf("decode mutation log: %v", err)
	}
	if !reflect.DeepEqual(decoded, log.document) {
		t.Fatalf("decoded mutation log differs:\n got %#v\nwant %#v", decoded, log.document)
	}
}

func TestMutationLogRoundTripReplayAndContinuation(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "mutation-item",
		Key:    "mutation-item",
		Fields: []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}},
	})
	revision := mustCompileWorkspace(t, workspace)
	original := mustSession(t, revision, "mutation-log-session")
	base, err := original.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("base Checkpoint: %v", err)
	}
	log, err := NewMutationLog(base)
	if err != nil {
		t.Fatalf("NewMutationLog: %v", err)
	}
	if _, err := original.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": int64(1)})); err != nil {
		t.Fatalf("Assert 1: %v", err)
	}
	log, err = original.AppendMutationLog(ctx, log)
	if err != nil {
		t.Fatalf("AppendMutationLog 1: %v", err)
	}
	if _, err := original.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": int64(2)})); err != nil {
		t.Fatalf("Assert 2: %v", err)
	}
	log, err = original.AppendMutationLog(ctx, log)
	if err != nil {
		t.Fatalf("AppendMutationLog 2: %v", err)
	}
	if log.Len() != 2 {
		t.Fatalf("log length = %d, want 2", log.Len())
	}

	var encoded bytes.Buffer
	if err := EncodeMutationLog(&encoded, log); err != nil {
		t.Fatalf("EncodeMutationLog: %v", err)
	}
	decoded, err := DecodeMutationLog(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("DecodeMutationLog: %v", err)
	}
	replayed, err := ReplayMutationLog(ctx, revision, base, decoded)
	if err != nil {
		t.Fatalf("ReplayMutationLog: %v", err)
	}
	got, err := replayed.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("replayed checkpoint: %v", err)
	}
	want, err := original.checkpointWire(ctx)
	if err != nil {
		t.Fatalf("original checkpoint: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed checkpoint differs:\n got %#v\nwant %#v", got, want)
	}

	originalNext, err := original.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": int64(3)}))
	if err != nil {
		t.Fatalf("original continuation: %v", err)
	}
	replayedNext, err := replayed.Assert(ctx, item.Key(), mustFields(t, map[string]any{"id": int64(3)}))
	if err != nil {
		t.Fatalf("replayed continuation: %v", err)
	}
	if originalNext.Fact.ID() != replayedNext.Fact.ID() || originalNext.Fact.Recency() != replayedNext.Fact.Recency() {
		t.Fatalf("continuation identity differs: original=%s/%d replayed=%s/%d", originalNext.Fact.ID(), originalNext.Fact.Recency(), replayedNext.Fact.ID(), replayedNext.Fact.Recency())
	}
}

func TestMutationLogWireRejectsEnvelopeAndChainViolations(t *testing.T) {
	base := Checkpoint{document: minimalCheckpointWireDocument()}
	log, err := NewMutationLog(base)
	if err != nil {
		t.Fatalf("NewMutationLog: %v", err)
	}
	valid, err := encodeMutationLogWire(log.document)
	if err != nil {
		t.Fatalf("encode valid log: %v", err)
	}
	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{name: "wrong format", encoded: strings.Replace(string(valid), mutationLogWireFormat, "other", 1), want: ErrInvalidMutationLog},
		{name: "zero version", encoded: strings.Replace(string(valid), `"version":1`, `"version":0`, 1), want: ErrUnsupportedMutationLogVersion},
		{name: "unknown field", encoded: strings.Replace(string(valid), `"version":1`, `"version":1,"unknown":true`, 1), want: ErrInvalidMutationLog},
		{name: "duplicate field", encoded: strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1), want: ErrInvalidMutationLog},
		{name: "trailing value", encoded: string(valid) + `{}`, want: ErrInvalidMutationLog},
		{name: "invalid digest", encoded: strings.Replace(string(valid), log.document.BaseCheckpointDigest, "sha256:bad", 1), want: ErrInvalidMutationLog},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeMutationLogWire([]byte(tc.encoded)); !errors.Is(err, tc.want) {
				t.Fatalf("decode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMutationLogDecodeReaderEnforcesBound(t *testing.T) {
	if _, err := decodeMutationLogReader(strings.NewReader(strings.Repeat("x", 65)), 64); !errors.Is(err, ErrInvalidMutationLog) {
		t.Fatalf("oversized decode error = %v, want ErrInvalidMutationLog", err)
	}
}

func FuzzDecodeMutationLogWireDoesNotPanic(f *testing.F) {
	f.Add([]byte(`{}`))
	base := Checkpoint{document: minimalCheckpointWireDocument()}
	log, err := NewMutationLog(base)
	if err != nil {
		f.Fatalf("NewMutationLog: %v", err)
	}
	valid, err := encodeMutationLogWire(log.document)
	if err != nil {
		f.Fatalf("encode seed: %v", err)
	}
	f.Add(valid)
	f.Fuzz(func(t *testing.T, encoded []byte) {
		_, _ = decodeMutationLogWire(encoded)
	})
}
