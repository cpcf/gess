package engine

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidRuleset              = errors.New("gess: invalid ruleset")
	ErrIncompatibleRuleset         = errors.New("gess: incompatible ruleset")
	ErrClosedSession               = errors.New("gess: closed session")
	ErrConcurrencyMisuse           = errors.New("gess: concurrency misuse")
	ErrActionFailed                = errors.New("gess: action failed")
	ErrValidation                  = errors.New("gess: validation failed")
	ErrFactNotFound                = errors.New("gess: fact not found")
	ErrStaleFactID                 = errors.New("gess: stale fact id")
	ErrDuplicateFact               = errors.New("gess: duplicate fact")
	ErrMatcher                     = errors.New("gess: matcher error")
	ErrUnsupportedRuntime          = errors.New("gess: unsupported runtime")
	ErrInvalidPath                 = errors.New("gess: invalid path")
	ErrInvalidListPattern          = errors.New("gess: invalid list pattern")
	ErrInvalidHigherOrderCondition = errors.New("gess: invalid higher-order condition")
	ErrAggregateValidation         = errors.New("gess: aggregate validation failed")
	ErrAggregateEvaluation         = errors.New("gess: aggregate evaluation failed")
	ErrFunctionValidation          = errors.New("gess: function validation failed")
	ErrFunctionEvaluation          = errors.New("gess: function evaluation failed")
	ErrQueryNotFound               = errors.New("gess: query not found")
	ErrQueryArgument               = errors.New("gess: query argument failed")
	ErrQueryValidation             = errors.New("gess: query validation failed")
	ErrQueryExecution              = errors.New("gess: query execution failed")
	ErrLogicalSupportUnavailable   = errors.New("gess: logical support unavailable")
	ErrLogicalOnlyRetract          = errors.New("gess: cannot retract logical-only fact")
	ErrLogicalFactModify           = errors.New("gess: cannot modify fact with logical support")
	ErrDivideByZero                = errors.New("gess: divide by zero")
	ErrBuiltinArgument             = errors.New("gess: built-in function argument error")
	ErrExplainLogUnavailable       = errors.New("gess: explain log unavailable")
	ErrRuleNotFound                = errors.New("gess: rule not found")
)

type ValidationError struct {
	TemplateName       string
	RuleName           string
	FunctionName       string
	GlobalName         string
	Source             SourceSpan
	FieldName          string
	ConditionIndex     int
	HasConditionIndex  bool
	ConstraintIndex    int
	HasConstraintIndex bool
	PredicateIndex     int
	HasPredicateIndex  bool
	JoinIndex          int
	HasJoinIndex       bool
	ActionIndex        int
	HasActionIndex     bool
	Reason             string
	Err                error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ErrValidation.Error()
	}

	msg := "gess: validation failed"
	if loc := sourceSpanLocation(e.Source); loc != "" {
		msg = loc + ": " + msg
	}
	if e.TemplateName != "" {
		msg += fmt.Sprintf(" for template %q", e.TemplateName)
	}
	if e.RuleName != "" {
		msg += fmt.Sprintf(" for rule %q", e.RuleName)
	}
	if e.FunctionName != "" {
		msg += fmt.Sprintf(" for function %q", e.FunctionName)
	}
	if e.GlobalName != "" {
		msg += fmt.Sprintf(" for global %q", e.GlobalName)
	}
	if e.FieldName != "" {
		msg += fmt.Sprintf(" field %q", e.FieldName)
	}
	if e.HasConditionIndex {
		msg += fmt.Sprintf(" condition %d", e.ConditionIndex)
	}
	if e.HasConstraintIndex {
		msg += fmt.Sprintf(" constraint %d", e.ConstraintIndex)
	}
	if e.HasPredicateIndex {
		msg += fmt.Sprintf(" predicate %d", e.PredicateIndex)
	}
	if e.HasJoinIndex {
		msg += fmt.Sprintf(" join %d", e.JoinIndex)
	}
	if e.HasActionIndex {
		msg += fmt.Sprintf(" action %d", e.ActionIndex)
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
