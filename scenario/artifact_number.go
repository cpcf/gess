package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// DecimalUint64 carries an unsigned 64-bit structural counter as a canonical
// decimal JSON string. String encoding keeps report identities and ordering
// exact in JavaScript clients.
type DecimalUint64 uint64

var (
	_ json.Marshaler   = DecimalUint64(0)
	_ json.Unmarshaler = (*DecimalUint64)(nil)
)

// NewDecimalUint64 constructs a decimal counter from value.
func NewDecimalUint64(value uint64) DecimalUint64 {
	return DecimalUint64(value)
}

// Uint64 returns the underlying counter value.
func (value DecimalUint64) Uint64() uint64 {
	return uint64(value)
}

// String returns the canonical unsigned decimal representation.
func (value DecimalUint64) String() string {
	return strconv.FormatUint(uint64(value), 10)
}

// MarshalJSON implements json.Marshaler.
func (value DecimalUint64) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(value.String())), nil
}

// UnmarshalJSON implements json.Unmarshaler. The receiver is unchanged when
// decoding fails.
func (value *DecimalUint64) UnmarshalJSON(data []byte) error {
	if value == nil {
		return fmt.Errorf("cannot unmarshal into a nil *DecimalUint64")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var text string
	if err := decoder.Decode(&text); err != nil {
		return fmt.Errorf("decimal uint64 must be a JSON string: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decimal uint64 has trailing JSON")
		}
		return fmt.Errorf("decimal uint64 has trailing JSON: %w", err)
	}
	if text == "" {
		return fmt.Errorf("decimal uint64 must not be empty")
	}
	if len(text) > 1 && text[0] == '0' {
		return fmt.Errorf("decimal uint64 must not contain leading zeroes")
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("decimal uint64 must contain only ASCII digits")
		}
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("decimal uint64 is out of range: %w", err)
	}
	*value = DecimalUint64(parsed)
	return nil
}
