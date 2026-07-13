package engine

import (
	"context"
	"fmt"
	"io"
)

const (
	CheckpointFormat          = checkpointWireFormat
	CheckpointVersion         = checkpointWireVersion
	DefaultCheckpointMaxBytes = 64 << 20
)

// Checkpoint is an opaque, detached durable-session value.
type Checkpoint struct {
	document checkpointWireDocument
}

func (c Checkpoint) FormatVersion() int {
	return c.document.Version
}

func (c Checkpoint) RulesetID() RulesetID {
	return c.document.RulesetID
}

func (c Checkpoint) SessionID() SessionID {
	return c.document.SessionID
}

// Checkpoint captures the complete durable state of an idle session.
func (s *Session) Checkpoint(ctx context.Context) (Checkpoint, error) {
	document, err := s.checkpointWire(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{document: document}, nil
}

// EncodeCheckpoint writes the canonical versioned checkpoint document.
func EncodeCheckpoint(w io.Writer, checkpoint Checkpoint) error {
	if w == nil {
		return fmt.Errorf("%w: nil checkpoint writer", ErrInvalidCheckpoint)
	}
	encoded, err := encodeCheckpointWire(checkpoint.document)
	if err != nil {
		return err
	}
	written, err := w.Write(encoded)
	if err != nil {
		return err
	}
	if written != len(encoded) {
		return io.ErrShortWrite
	}
	return nil
}

// DecodeCheckpoint reads one canonical checkpoint document with a default
// allocation bound. Unknown fields and trailing data are rejected.
func DecodeCheckpoint(r io.Reader) (Checkpoint, error) {
	return decodeCheckpointReader(r, DefaultCheckpointMaxBytes)
}

func decodeCheckpointReader(r io.Reader, maxBytes int64) (Checkpoint, error) {
	if r == nil {
		return Checkpoint{}, fmt.Errorf("%w: nil checkpoint reader", ErrInvalidCheckpoint)
	}
	if maxBytes <= 0 {
		return Checkpoint{}, fmt.Errorf("%w: invalid checkpoint size limit", ErrInvalidCheckpoint)
	}
	encoded, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return Checkpoint{}, err
	}
	if int64(len(encoded)) > maxBytes {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint exceeds %d bytes", ErrInvalidCheckpoint, maxBytes)
	}
	document, err := decodeCheckpointWire(encoded)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{document: document}, nil
}

// RestoreCheckpoint builds an independent session from checkpoint against the
// matching compiled ruleset.
func RestoreCheckpoint(ctx context.Context, revision *Ruleset, checkpoint Checkpoint, opts ...SessionOption) (*Session, error) {
	return restoreCheckpointWire(ctx, revision, checkpoint.document, opts...)
}
