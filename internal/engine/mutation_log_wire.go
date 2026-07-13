package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

const (
	mutationLogWireFormat  = "gess/session-mutation-log"
	mutationLogWireVersion = 1
)

type mutationLogWireDocument struct {
	Format               string                  `json:"format"`
	Version              int                     `json:"version"`
	RulesetID            RulesetID               `json:"rulesetId"`
	SessionID            SessionID               `json:"sessionId,omitempty"`
	BaseCheckpointDigest string                  `json:"baseCheckpointDigest"`
	Records              []mutationLogWireRecord `json:"records"`
}

type mutationLogWireRecord struct {
	Sequence       uint64                 `json:"sequence"`
	PreviousDigest string                 `json:"previousDigest"`
	Digest         string                 `json:"digest"`
	Checkpoint     checkpointWireDocument `json:"checkpoint"`
}

func encodeMutationLogWire(document mutationLogWireDocument) ([]byte, error) {
	if err := validateMutationLogWireDocument(document); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalidMutationLog, err)
	}
	return encoded, nil
}

func decodeMutationLogWire(encoded []byte) (mutationLogWireDocument, error) {
	if err := rejectDuplicateMutationLogJSONKeys(encoded); err != nil {
		return mutationLogWireDocument{}, err
	}
	var header struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := decodeMutationLogJSON(encoded, &header, false); err != nil {
		return mutationLogWireDocument{}, err
	}
	if header.Format != mutationLogWireFormat {
		return mutationLogWireDocument{}, fmt.Errorf("%w: format %q", ErrInvalidMutationLog, header.Format)
	}
	if header.Version != mutationLogWireVersion {
		return mutationLogWireDocument{}, fmt.Errorf("%w: version %d", ErrUnsupportedMutationLogVersion, header.Version)
	}
	var document mutationLogWireDocument
	if err := decodeMutationLogJSON(encoded, &document, true); err != nil {
		return mutationLogWireDocument{}, err
	}
	if err := validateMutationLogWireDocument(document); err != nil {
		return mutationLogWireDocument{}, err
	}
	return document, nil
}

func validateMutationLogWireDocument(document mutationLogWireDocument) error {
	if document.Format != mutationLogWireFormat {
		return fmt.Errorf("%w: format %q", ErrInvalidMutationLog, document.Format)
	}
	if document.Version != mutationLogWireVersion {
		return fmt.Errorf("%w: version %d", ErrUnsupportedMutationLogVersion, document.Version)
	}
	if document.RulesetID == "" {
		return fmt.Errorf("%w: missing ruleset ID", ErrInvalidMutationLog)
	}
	if !validMutationLogDigest(document.BaseCheckpointDigest) {
		return fmt.Errorf("%w: invalid base checkpoint digest", ErrInvalidMutationLog)
	}
	previous := document.BaseCheckpointDigest
	for i, record := range document.Records {
		sequence := uint64(i + 1)
		if record.Sequence != sequence {
			return fmt.Errorf("%w: record %d has non-contiguous sequence", ErrInvalidMutationLog, i)
		}
		if record.PreviousDigest != previous {
			return fmt.Errorf("%w: record %d has broken previous digest", ErrInvalidMutationLog, i)
		}
		if err := validateCheckpointWireDocument(record.Checkpoint); err != nil {
			return fmt.Errorf("%w: record %d checkpoint: %v", ErrInvalidMutationLog, i, err)
		}
		if record.Checkpoint.RulesetID != document.RulesetID {
			return fmt.Errorf("%w: record %d ruleset mismatch", ErrInvalidMutationLog, i)
		}
		if record.Checkpoint.SessionID != document.SessionID {
			return fmt.Errorf("%w: record %d session mismatch", ErrInvalidMutationLog, i)
		}
		checkpointBytes, err := encodeCheckpointWire(record.Checkpoint)
		if err != nil {
			return fmt.Errorf("%w: record %d checkpoint: %v", ErrInvalidMutationLog, i, err)
		}
		want := mutationLogRecordDigest(record.Sequence, record.PreviousDigest, checkpointBytes)
		if record.Digest != want {
			return fmt.Errorf("%w: record %d digest mismatch", ErrInvalidMutationLog, i)
		}
		previous = record.Digest
	}
	return nil
}

func checkpointWireDigest(document checkpointWireDocument) (string, error) {
	encoded, err := encodeCheckpointWire(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func mutationLogRecordDigest(sequence uint64, previous string, checkpoint []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(mutationLogWireFormat))
	_, _ = hash.Write([]byte{'\n'})
	_, _ = hash.Write([]byte(strconv.FormatUint(sequence, 10)))
	_, _ = hash.Write([]byte{'\n'})
	_, _ = hash.Write([]byte(previous))
	_, _ = hash.Write([]byte{'\n'})
	_, _ = hash.Write(checkpoint)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validMutationLogDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value[len("sha256:"):]
}

func rejectDuplicateMutationLogJSONKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := scanCheckpointJSONValue(decoder); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidMutationLog, err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON token %v", ErrInvalidMutationLog, token)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidMutationLog, err)
	}
	return nil
}

func decodeMutationLogJSON(encoded []byte, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidMutationLog, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrInvalidMutationLog)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidMutationLog, err)
	}
	return nil
}
