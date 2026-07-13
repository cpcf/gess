package engine

import gessrules "github.com/cpcf/gess/rules"

var (
	ErrInvalidRuleset                = gessrules.ErrInvalidRuleset
	ErrIncompatibleRuleset           = gessrules.ErrIncompatibleRuleset
	ErrClosedSession                 = gessrules.ErrClosedSession
	ErrConcurrencyMisuse             = gessrules.ErrConcurrencyMisuse
	ErrActionFailed                  = gessrules.ErrActionFailed
	ErrValidation                    = gessrules.ErrValidation
	ErrFactNotFound                  = gessrules.ErrFactNotFound
	ErrStaleFactID                   = gessrules.ErrStaleFactID
	ErrDuplicateFact                 = gessrules.ErrDuplicateFact
	ErrMatcher                       = gessrules.ErrMatcher
	ErrUnsupportedRuntime            = gessrules.ErrUnsupportedRuntime
	ErrInvalidPath                   = gessrules.ErrInvalidPath
	ErrInvalidListPattern            = gessrules.ErrInvalidListPattern
	ErrInvalidHigherOrderCondition   = gessrules.ErrInvalidHigherOrderCondition
	ErrAggregateValidation           = gessrules.ErrAggregateValidation
	ErrAggregateEvaluation           = gessrules.ErrAggregateEvaluation
	ErrFunctionValidation            = gessrules.ErrFunctionValidation
	ErrFunctionEvaluation            = gessrules.ErrFunctionEvaluation
	ErrQueryNotFound                 = gessrules.ErrQueryNotFound
	ErrQueryArgument                 = gessrules.ErrQueryArgument
	ErrQueryValidation               = gessrules.ErrQueryValidation
	ErrQueryExecution                = gessrules.ErrQueryExecution
	ErrLogicalSupportUnavailable     = gessrules.ErrLogicalSupportUnavailable
	ErrLogicalOnlyRetract            = gessrules.ErrLogicalOnlyRetract
	ErrLogicalFactModify             = gessrules.ErrLogicalFactModify
	ErrDivideByZero                  = gessrules.ErrDivideByZero
	ErrBuiltinArgument               = gessrules.ErrBuiltinArgument
	ErrExplainLogUnavailable         = gessrules.ErrExplainLogUnavailable
	ErrRuleNotFound                  = gessrules.ErrRuleNotFound
	ErrDemandCascadeLimit            = gessrules.ErrDemandCascadeLimit
	ErrInvalidCheckpoint             = gessrules.ErrInvalidCheckpoint
	ErrUnsupportedCheckpointVersion  = gessrules.ErrUnsupportedCheckpointVersion
	ErrInvalidMutationLog            = gessrules.ErrInvalidMutationLog
	ErrUnsupportedMutationLogVersion = gessrules.ErrUnsupportedMutationLogVersion
)

type ValidationError = gessrules.ValidationError
type DemandCascadeLimitError = gessrules.DemandCascadeLimitError
