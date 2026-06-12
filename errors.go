package gess

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidRuleset    = errors.New("gess: invalid ruleset")
	ErrClosedSession     = errors.New("gess: closed session")
	ErrConcurrencyMisuse = errors.New("gess: concurrency misuse")
	ErrValidation        = errors.New("gess: validation failed")
	ErrFactNotFound      = errors.New("gess: fact not found")
	ErrStaleFactID       = errors.New("gess: stale fact id")
	ErrDuplicateFact     = errors.New("gess: duplicate fact")
)

type ValidationError struct {
	TemplateName string
	FieldName    string
	Reason       string
	Err          error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ErrValidation.Error()
	}

	msg := "gess: validation failed"
	if e.TemplateName != "" {
		msg += fmt.Sprintf(" for template %q", e.TemplateName)
	}
	if e.FieldName != "" {
		msg += fmt.Sprintf(" field %q", e.FieldName)
	}
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

func (e *ValidationError) Unwrap() error {
	if e != nil && e.Err != nil {
		return e.Err
	}
	return ErrValidation
}

func (e *ValidationError) Is(target error) bool {
	return target == ErrValidation
}
