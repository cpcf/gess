package session

import (
	"io"
	"time"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/rules"
)

type (
	Option                     = engine.SessionOption
	InitialFact                = engine.SessionInitialFact
	Session                    = engine.Session
	Snapshot                   = engine.Snapshot
	Agenda                     = engine.Agenda
	AgendaActivation           = engine.AgendaActivation
	BackchainDemandDiagnostics = engine.BackchainDemandDiagnostics
	QueryArgs                  = engine.QueryArgs
	QueryRow                   = engine.QueryRow
	QueryIterator              = engine.QueryIterator
	FactSnapshot               = engine.FactSnapshot
	FactVersion                = engine.FactVersion
	Recency                    = engine.Recency
	Generation                 = engine.Generation
	FactID                     = engine.FactID
	SessionID                  = engine.SessionID
	RunID                      = engine.RunID
	ActivationID               = engine.ActivationID
	SupportID                  = engine.SupportID
	TemplateKey                = engine.TemplateKey
	Fields                     = engine.Fields
	Value                      = engine.Value
	ValueKind                  = engine.ValueKind
	EventType                  = engine.EventType
	EventSeverity              = engine.EventSeverity
	Event                      = engine.Event
	EventListener              = engine.EventListener
	EventListenerOption        = engine.EventListenerOption
	EventFunc                  = engine.EventFunc
	TraceOption                = engine.TraceOption
	MutationKind               = engine.MutationKind
	RunStatus                  = engine.RunStatus
	RunOption                  = engine.RunOption
	RunResult                  = engine.RunResult
	ActionFailureError         = engine.ActionFailureError
	DuplicateKey               = engine.DuplicateKey
	FactSupportState           = engine.FactSupportState
	FactSupportProvenance      = engine.FactSupportProvenance
	FieldChange                = engine.FieldChange
	FactPatch                  = engine.FactPatch
	MutationDelta              = engine.MutationDelta
	AssertStatus               = engine.AssertStatus
	AssertResult               = engine.AssertResult
	ModifyStatus               = engine.ModifyStatus
	ModifyResult               = engine.ModifyResult
	RetractStatus              = engine.RetractStatus
	RetractResult              = engine.RetractResult
	ResetStatus                = engine.ResetStatus
	ResetResult                = engine.ResetResult
	ApplyRulesetStatus         = engine.ApplyRulesetStatus
	RuleRevisionSummary        = engine.RuleRevisionSummary
	RuleReplacement            = engine.RuleReplacement
	ApplyRulesetResult         = engine.ApplyRulesetResult
	LogicalSupportEdge         = engine.LogicalSupportEdge
	LogicalSupportCounters     = engine.LogicalSupportCounters
	SupportGraph               = engine.SupportGraph
)

type (
	RuntimeDiagnostics            = engine.RuntimeDiagnostics
	RuntimeMemoryOwnerDiagnostics = engine.RuntimeMemoryOwnerDiagnostics
)

const (
	EventFactAsserted             = engine.EventFactAsserted
	EventFactModified             = engine.EventFactModified
	EventFactRetracted            = engine.EventFactRetracted
	EventReset                    = engine.EventReset
	EventRuleActivated            = engine.EventRuleActivated
	EventRuleDeactivated          = engine.EventRuleDeactivated
	EventRuleFired                = engine.EventRuleFired
	EventActionFailed             = engine.EventActionFailed
	EventLogicalSupportAdded      = engine.EventLogicalSupportAdded
	EventLogicalSupportRemoved    = engine.EventLogicalSupportRemoved
	EventSeverityInfo             = engine.EventSeverityInfo
	EventSeverityError            = engine.EventSeverityError
	MutationAssert                = engine.MutationAssert
	MutationModify                = engine.MutationModify
	MutationRetract               = engine.MutationRetract
	MutationReset                 = engine.MutationReset
	RunCompleted                  = engine.RunCompleted
	RunHalted                     = engine.RunHalted
	RunFireLimit                  = engine.RunFireLimit
	RunCanceled                   = engine.RunCanceled
	RunActionFailed               = engine.RunActionFailed
	RunClosed                     = engine.RunClosed
	RunConcurrencyMisuse          = engine.RunConcurrencyMisuse
	RunFailed                     = engine.RunFailed
	FactSupportStated             = engine.FactSupportStated
	FactSupportLogical            = engine.FactSupportLogical
	FactSupportStatedAndLogical   = engine.FactSupportStatedAndLogical
	FactSupportMetadataOnly       = engine.FactSupportMetadataOnly
	AssertInserted                = engine.AssertInserted
	AssertExisting                = engine.AssertExisting
	AssertValidationFailure       = engine.AssertValidationFailure
	AssertClosed                  = engine.AssertClosed
	AssertConcurrencyMisuse       = engine.AssertConcurrencyMisuse
	ModifyChanged                 = engine.ModifyChanged
	ModifyNoOp                    = engine.ModifyNoOp
	ModifyMissing                 = engine.ModifyMissing
	ModifyStale                   = engine.ModifyStale
	ModifyValidationFailure       = engine.ModifyValidationFailure
	ModifyDuplicate               = engine.ModifyDuplicate
	ModifyLogicalSupport          = engine.ModifyLogicalSupport
	ModifyClosed                  = engine.ModifyClosed
	ModifyConcurrencyMisuse       = engine.ModifyConcurrencyMisuse
	RetractRemoved                = engine.RetractRemoved
	RetractStatedSupportRemoved   = engine.RetractStatedSupportRemoved
	RetractLogicalOnly            = engine.RetractLogicalOnly
	RetractMissing                = engine.RetractMissing
	RetractStale                  = engine.RetractStale
	RetractClosed                 = engine.RetractClosed
	RetractValidationFailure      = engine.RetractValidationFailure
	RetractConcurrencyMisuse      = engine.RetractConcurrencyMisuse
	ResetApplied                  = engine.ResetApplied
	ResetValidationFailure        = engine.ResetValidationFailure
	ResetClosed                   = engine.ResetClosed
	ResetConcurrencyMisuse        = engine.ResetConcurrencyMisuse
	ApplyRulesetApplied           = engine.ApplyRulesetApplied
	ApplyRulesetUnchanged         = engine.ApplyRulesetUnchanged
	ApplyRulesetIncompatible      = engine.ApplyRulesetIncompatible
	ApplyRulesetClosed            = engine.ApplyRulesetClosed
	ApplyRulesetConcurrencyMisuse = engine.ApplyRulesetConcurrencyMisuse
)

var (
	ErrClosedSession             = engine.ErrClosedSession
	ErrConcurrencyMisuse         = engine.ErrConcurrencyMisuse
	ErrActionFailed              = engine.ErrActionFailed
	ErrFactNotFound              = engine.ErrFactNotFound
	ErrStaleFactID               = engine.ErrStaleFactID
	ErrDuplicateFact             = engine.ErrDuplicateFact
	ErrQueryNotFound             = engine.ErrQueryNotFound
	ErrQueryArgument             = engine.ErrQueryArgument
	ErrQueryExecution            = engine.ErrQueryExecution
	ErrLogicalSupportUnavailable = engine.ErrLogicalSupportUnavailable
	ErrLogicalOnlyRetract        = engine.ErrLogicalOnlyRetract
	ErrLogicalFactModify         = engine.ErrLogicalFactModify
)

func WithSessionID(id SessionID) Option {
	return engine.WithSessionID(id)
}

func WithEventListener(listener EventListener, opts ...EventListenerOption) Option {
	return engine.WithEventListener(listener, opts...)
}

func ForEventTypes(types ...EventType) EventListenerOption {
	return engine.ForEventTypes(types...)
}

func NewTraceListener(w io.Writer, opts ...TraceOption) EventListener {
	return engine.NewTraceListener(w, opts...)
}

func TraceWithTimestamps() TraceOption {
	return engine.TraceWithTimestamps()
}

func WithEventClock(clock func() time.Time) Option {
	return engine.WithEventClock(clock)
}

func WithInitialFacts(initials ...InitialFact) Option {
	return engine.WithInitialFacts(initials...)
}

func WithResetBeforeSnapshot(enabled bool) Option {
	return engine.WithResetBeforeSnapshot(enabled)
}

func WithMaxFirings(n int) RunOption {
	return engine.WithMaxFirings(n)
}

func New(revision *rules.Ruleset, opts ...Option) (*Session, error) {
	return engine.NewSession(revision, opts...)
}
