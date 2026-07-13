package engine

import (
	"context"
	"fmt"
	"io"
)

const (
	MutationLogFormat          = mutationLogWireFormat
	MutationLogVersion         = mutationLogWireVersion
	DefaultMutationLogMaxBytes = 64 << 20
)

// MutationLog is an opaque, immutable sequence of durable semantic commits.
type MutationLog struct {
	document mutationLogWireDocument
}

func (l MutationLog) FormatVersion() int {
	return l.document.Version
}

func (l MutationLog) RulesetID() RulesetID {
	return l.document.RulesetID
}

func (l MutationLog) SessionID() SessionID {
	return l.document.SessionID
}

func (l MutationLog) Len() int {
	return len(l.document.Records)
}

// NewMutationLog anchors an empty mutation log to base.
func NewMutationLog(base Checkpoint) (MutationLog, error) {
	if err := validateCheckpointWireDocument(base.document); err != nil {
		return MutationLog{}, fmt.Errorf("%w: base checkpoint: %v", ErrInvalidMutationLog, err)
	}
	digest, err := checkpointWireDigest(base.document)
	if err != nil {
		return MutationLog{}, fmt.Errorf("%w: base checkpoint: %v", ErrInvalidMutationLog, err)
	}
	document := mutationLogWireDocument{
		Format:               mutationLogWireFormat,
		Version:              mutationLogWireVersion,
		RulesetID:            base.document.RulesetID,
		SessionID:            base.document.SessionID,
		BaseCheckpointDigest: digest,
		Records:              []mutationLogWireRecord{},
	}
	return MutationLog{document: document}, nil
}

// AppendMutationLog returns a new log with checkpoint as its next semantic
// commit. Existing log values remain unchanged.
func AppendMutationLog(log MutationLog, checkpoint Checkpoint) (MutationLog, error) {
	if err := validateMutationLogWireDocument(log.document); err != nil {
		return MutationLog{}, err
	}
	if err := validateCheckpointWireDocument(checkpoint.document); err != nil {
		return MutationLog{}, fmt.Errorf("%w: checkpoint: %v", ErrInvalidMutationLog, err)
	}
	if checkpoint.document.RulesetID != log.document.RulesetID {
		return MutationLog{}, fmt.Errorf("%w: checkpoint ruleset mismatch", ErrInvalidMutationLog)
	}
	if checkpoint.document.SessionID != log.document.SessionID {
		return MutationLog{}, fmt.Errorf("%w: checkpoint session mismatch", ErrInvalidMutationLog)
	}
	previous := log.document.BaseCheckpointDigest
	if len(log.document.Records) > 0 {
		previous = log.document.Records[len(log.document.Records)-1].Digest
	}
	encoded, err := encodeCheckpointWire(checkpoint.document)
	if err != nil {
		return MutationLog{}, fmt.Errorf("%w: checkpoint: %v", ErrInvalidMutationLog, err)
	}
	sequence := uint64(len(log.document.Records) + 1)
	record := mutationLogWireRecord{
		Sequence:       sequence,
		PreviousDigest: previous,
		Digest:         mutationLogRecordDigest(sequence, previous, encoded),
		Checkpoint:     checkpoint.document,
	}
	document := log.document
	document.Records = append(append([]mutationLogWireRecord(nil), log.document.Records...), record)
	if err := validateMutationLogWireDocument(document); err != nil {
		return MutationLog{}, err
	}
	return MutationLog{document: document}, nil
}

// AppendMutationLog checkpoints the idle session and appends it to log.
func (s *Session) AppendMutationLog(ctx context.Context, log MutationLog) (MutationLog, error) {
	checkpoint, err := s.Checkpoint(ctx)
	if err != nil {
		return MutationLog{}, err
	}
	return AppendMutationLog(log, checkpoint)
}

func EncodeMutationLog(w io.Writer, log MutationLog) error {
	if w == nil {
		return fmt.Errorf("%w: nil mutation log writer", ErrInvalidMutationLog)
	}
	encoded, err := encodeMutationLogWire(log.document)
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

func DecodeMutationLog(r io.Reader) (MutationLog, error) {
	return decodeMutationLogReader(r, DefaultMutationLogMaxBytes)
}

func decodeMutationLogReader(r io.Reader, maxBytes int64) (MutationLog, error) {
	if r == nil {
		return MutationLog{}, fmt.Errorf("%w: nil mutation log reader", ErrInvalidMutationLog)
	}
	if maxBytes <= 0 {
		return MutationLog{}, fmt.Errorf("%w: invalid mutation log size limit", ErrInvalidMutationLog)
	}
	encoded, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return MutationLog{}, err
	}
	if int64(len(encoded)) > maxBytes {
		return MutationLog{}, fmt.Errorf("%w: mutation log exceeds %d bytes", ErrInvalidMutationLog, maxBytes)
	}
	document, err := decodeMutationLogWire(encoded)
	if err != nil {
		return MutationLog{}, err
	}
	return MutationLog{document: document}, nil
}

// ReplayMutationLog validates each committed checkpoint against revision and
// restores the final commit. An empty log restores base.
func ReplayMutationLog(ctx context.Context, revision *Ruleset, base Checkpoint, log MutationLog, opts ...SessionOption) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, ErrInvalidRuleset
	}
	if err := validateMutationLogWireDocument(log.document); err != nil {
		return nil, err
	}
	if err := validateCheckpointWireDocument(base.document); err != nil {
		return nil, fmt.Errorf("%w: base checkpoint: %v", ErrInvalidMutationLog, err)
	}
	baseDigest, err := checkpointWireDigest(base.document)
	if err != nil {
		return nil, fmt.Errorf("%w: base checkpoint: %v", ErrInvalidMutationLog, err)
	}
	if base.document.RulesetID != log.document.RulesetID || base.document.SessionID != log.document.SessionID || baseDigest != log.document.BaseCheckpointDigest {
		return nil, fmt.Errorf("%w: base checkpoint does not match log anchor", ErrInvalidMutationLog)
	}
	if revision.ID() != log.document.RulesetID {
		return nil, fmt.Errorf("%w: mutation log ruleset %s, supplied ruleset %s", ErrIncompatibleRuleset, log.document.RulesetID, revision.ID())
	}
	if len(log.document.Records) == 0 {
		return restoreCheckpointWire(ctx, revision, base.document, opts...)
	}
	for i, record := range log.document.Records {
		last := i == len(log.document.Records)-1
		var restored *Session
		if last {
			restored, err = restoreCheckpointWire(ctx, revision, record.Checkpoint, opts...)
		} else {
			restored, err = restoreCheckpointWire(ctx, revision, record.Checkpoint)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: record %d cannot restore: %v", ErrInvalidMutationLog, i, err)
		}
		if last {
			return restored, nil
		}
		if err := restored.Close(); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: missing final record", ErrInvalidMutationLog)
}
