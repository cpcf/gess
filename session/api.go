package session

import (
	"time"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/rules"
)

type (
	// Option configures a Session at construction time. Each With*
	// function below returns an Option.
	Option = engine.SessionOption
	// InitialFact describes one fact to assert when a session is
	// constructed and to re-assert on every Reset, matching deffacts in
	// generated .gess code. Name identifies a dynamic fact; TemplateKey
	// and Fields identify a templated fact.
	InitialFact = engine.SessionInitialFact
	// Session is the mutable runtime for one compiled ruleset: working
	// memory, the agenda, the focus stack, logical support, backchain
	// demand, and event delivery. A session has one logical owner;
	// overlapping operations from other goroutines fail fast with
	// [ErrConcurrencyMisuse] rather than blocking, except that mutations
	// from other goroutines queue during an active Run.
	Session = engine.Session
	// Snapshot is an immutable, point-in-time view of working memory:
	// every fact plus the support graph, as of when Snapshot was called.
	// It does not change as the session mutates afterward.
	Snapshot = engine.Snapshot
	// BackchainDemandDiagnostics summarizes active backward-chaining
	// demand facts visible in a [Snapshot], in total and per template.
	BackchainDemandDiagnostics = engine.BackchainDemandDiagnostics
	// QueryArgs maps query parameter names, without the leading '?', to
	// the Go values passed to Query or QueryAll.
	QueryArgs = engine.QueryArgs
	// QueryRow is one result row from a query, holding fact bindings and
	// computed values under their declared aliases.
	QueryRow = engine.QueryRow
	// QueryIterator iterates the pre-materialized rows produced by
	// Session.Query.
	QueryIterator = engine.QueryIterator
	// FactSnapshot is an immutable view of one fact's identity, fields,
	// and support state as of the snapshot or event that produced it.
	FactSnapshot = engine.FactSnapshot
	// FactVersion advances each time a fact is modified; a fact's FactID
	// stays stable across modifies within a generation.
	FactVersion = engine.FactVersion
	// Recency is a session-wide counter that advances on every fact
	// mutation, used to order which pending activation fires first.
	Recency = engine.Recency
	// Generation is the working-memory reset epoch. FactIDs embed a
	// generation, so IDs from before a Reset are stale afterward.
	Generation = engine.Generation
	// FactID identifies a fact, stable across modifies within a
	// generation. IDs from an earlier generation are stale after Reset.
	FactID = engine.FactID
	// SessionID identifies a session, set with WithSessionID.
	SessionID = engine.SessionID
	// RunID is a per-session sequence number for a Run call.
	RunID = engine.RunID
	// ActivationID identifies one activation: a rule paired with the
	// specific facts that matched it.
	ActivationID = engine.ActivationID
	// SupportID identifies one logical support edge.
	SupportID = engine.SupportID
	// TemplateKey identifies a compiled template within a ruleset.
	TemplateKey = engine.TemplateKey
	// Fields is a fact's field values, keyed by field name.
	Fields = engine.Fields
	// Value is a typed field or expression value.
	Value = engine.Value
	// ValueKind is a Value's type tag.
	ValueKind = engine.ValueKind
	// EventType discriminates the kind of change an [Event] reports.
	EventType = engine.EventType
	// EventSeverity is an [Event]'s severity: info or error.
	EventSeverity = engine.EventSeverity
	// Event is one state change delivered to an [EventListener]: a fact
	// mutation, rule activation or firing, action failure, or logical
	// support change. Listeners receive a cloned copy per event.
	Event = engine.Event
	// EventListener receives Events as a session's state changes.
	// Listener errors are ignored: they never fail the mutation that
	// produced the event, and later listeners still run.
	EventListener = engine.EventListener
	// EventFunc adapts a function to [EventListener].
	EventFunc = engine.EventFunc
	// MutationKind is the kind of change recorded in a [MutationDelta]:
	// assert, modify, retract, or reset.
	MutationKind = engine.MutationKind
	// RunStatus is the outcome of a Session.Run call.
	RunStatus = engine.RunStatus
	// RunResult reports the outcome of a Run: its status and how many
	// activations fired.
	RunResult = engine.RunResult
	// ActionFailureError wraps the error an action returned during Run,
	// identifying which activation and action produced it. Unwrap
	// returns the underlying cause; Is matches [ErrActionFailed].
	ActionFailureError = engine.ActionFailureError
	// DuplicateKey is the computed duplicate-detection key for a fact
	// under its template's duplicate policy.
	DuplicateKey = engine.DuplicateKey
	// FactSupportState classifies how a fact is supported: stated,
	// logical, both, or metadata-only.
	FactSupportState = engine.FactSupportState
	// FactSupportProvenance is a fact's support classification together
	// with the engine's internal tracking of that support, returned by
	// [FactSnapshot].Support.
	FactSupportProvenance = engine.FactSupportProvenance
	// FieldChange describes one field's before and after value from a
	// Modify, as recorded in [MutationDelta].ChangedFields.
	FieldChange = engine.FieldChange
	// FactPatch is the argument to Session.Modify: fields to set or
	// overwrite, and field names to remove.
	FactPatch = engine.FactPatch
	// MutationDelta describes one mutation's effect: its kind, the fact
	// before and after, version and support changes, and which
	// activation or rule caused it, if any.
	MutationDelta = engine.MutationDelta
	// AssertStatus is the outcome of a Session.Assert or AssertTemplate
	// call.
	AssertStatus = engine.AssertStatus
	// AssertResult reports the outcome of an assert: its status, the
	// resulting fact, and the mutation delta.
	AssertResult = engine.AssertResult
	// ModifyStatus is the outcome of a Session.Modify call.
	ModifyStatus = engine.ModifyStatus
	// ModifyResult reports the outcome of a modify: its status, the
	// resulting fact, and the mutation delta.
	ModifyResult = engine.ModifyResult
	// RetractStatus is the outcome of a Session.Retract call.
	RetractStatus = engine.RetractStatus
	// RetractResult reports the outcome of a retract: its status, the
	// removed fact, and the mutation delta.
	RetractResult = engine.RetractResult
	// ResetStatus is the outcome of a Session.Reset call.
	ResetStatus = engine.ResetStatus
	// ResetResult reports the outcome of a reset: its status, the new
	// generation, and, if WithResetBeforeSnapshot was set, a snapshot of
	// working memory as it stood immediately before the reset.
	ResetResult = engine.ResetResult
	// ApplyRulesetStatus is the outcome of a Session.ApplyRuleset call.
	ApplyRulesetStatus = engine.ApplyRulesetStatus
	// RuleRevisionSummary identifies one rule revision by rule ID and
	// revision ID, as reported in an [ApplyRulesetResult].
	RuleRevisionSummary = engine.RuleRevisionSummary
	// RuleReplacement identifies a rule whose compiled revision changed
	// across an ApplyRuleset call, from OldRevisionID to NewRevisionID.
	RuleReplacement = engine.RuleReplacement
	// ApplyRulesetResult reports the outcome of swapping in a new
	// ruleset: its status and which rule revisions were added, removed,
	// replaced, or left unchanged.
	ApplyRulesetResult = engine.ApplyRulesetResult
	// LogicalSupportEdge is one edge from the facts an activation
	// matched to the logical fact it supports.
	LogicalSupportEdge = engine.LogicalSupportEdge
	// LogicalSupportCounters holds running totals for logical support:
	// current logical facts and edges, and lifetime asserts, retracts,
	// and cascades.
	LogicalSupportCounters = engine.LogicalSupportCounters
	// SupportGraph is the logical support edges and counters visible in
	// a [Snapshot], as of a given generation.
	SupportGraph = engine.SupportGraph
)

type (
	// RuntimeDiagnostics is a point-in-time diagnostic of retained
	// runtime memory, returned by Session.RuntimeDiagnostics.
	RuntimeDiagnostics = engine.RuntimeDiagnostics
	// RuntimeMemoryOwnerDiagnostics summarizes one Rete runtime memory
	// owner's retained rows, indexes, and bytes.
	RuntimeMemoryOwnerDiagnostics = engine.RuntimeMemoryOwnerDiagnostics
)

const (
	// EventFactAsserted, EventFactModified, and EventFactRetracted report
	// working-memory changes; EventReset reports a session Reset.
	EventFactAsserted  = engine.EventFactAsserted
	EventFactModified  = engine.EventFactModified
	EventFactRetracted = engine.EventFactRetracted
	EventReset         = engine.EventReset
	// EventRuleActivated and EventRuleDeactivated report a match
	// entering or leaving the agenda.
	EventRuleActivated   = engine.EventRuleActivated
	EventRuleDeactivated = engine.EventRuleDeactivated
	// EventRuleFired reports one activation firing during a Run.
	EventRuleFired = engine.EventRuleFired
	// EventActionFailed reports a rule action returning an error; its
	// [Event] carries ActionName, ActionIndex, and Cause, with severity
	// [EventSeverityError].
	EventActionFailed = engine.EventActionFailed
	// EventLogicalSupportAdded and EventLogicalSupportRemoved report a
	// logical support edge being created or removed.
	EventLogicalSupportAdded   = engine.EventLogicalSupportAdded
	EventLogicalSupportRemoved = engine.EventLogicalSupportRemoved
	// EventSeverityInfo is the default event severity.
	EventSeverityInfo = engine.EventSeverityInfo
	// EventSeverityError marks [EventActionFailed] events.
	EventSeverityError = engine.EventSeverityError
	// MutationAssert, MutationModify, MutationRetract, and MutationReset
	// tag a [MutationDelta]'s Kind.
	MutationAssert  = engine.MutationAssert
	MutationModify  = engine.MutationModify
	MutationRetract = engine.MutationRetract
	MutationReset   = engine.MutationReset
	// RunCompleted reports that Run emptied the agenda.
	RunCompleted = engine.RunCompleted
	// RunHalted reports that an action called Halt; the halting
	// activation's remaining actions still ran, and a later Run
	// continues with the activations left pending.
	RunHalted = engine.RunHalted
	// RunCanceled reports that ctx was canceled during the run.
	RunCanceled = engine.RunCanceled
	// RunActionFailed reports that an action returned an error; Run
	// returns a non-nil error that is an *[ActionFailureError].
	RunActionFailed = engine.RunActionFailed
	// RunClosed reports that the session was already closed.
	RunClosed = engine.RunClosed
	// RunConcurrencyMisuse reports that this Run overlapped another Run
	// on the same session.
	RunConcurrencyMisuse = engine.RunConcurrencyMisuse
	// RunFailed reports an internal engine failure that prevented the
	// run from completing.
	RunFailed = engine.RunFailed
	// FactSupportStated marks a fact asserted by host code and not
	// logically supported.
	FactSupportStated = engine.FactSupportStated
	// FactSupportLogical marks a fact asserted only through rule-driven
	// logical support; it is retracted automatically when that support
	// is removed, and can't be modified or retracted directly.
	FactSupportLogical = engine.FactSupportLogical
	// FactSupportStatedAndLogical marks a fact that carries both stated
	// and logical support at once.
	FactSupportStatedAndLogical = engine.FactSupportStatedAndLogical
	// FactSupportMetadataOnly marks a fact support classification used
	// internally for metadata-only support transitions.
	FactSupportMetadataOnly = engine.FactSupportMetadataOnly
	// AssertInserted reports that a new fact entered working memory.
	AssertInserted = engine.AssertInserted
	// AssertExisting reports that the template's duplicate policy
	// matched an existing fact instead of inserting a new one.
	AssertExisting = engine.AssertExisting
	// AssertValidationFailure reports that the fields failed template
	// validation; the error wraps [rules.ErrValidation].
	AssertValidationFailure = engine.AssertValidationFailure
	// AssertClosed reports that the session was already closed.
	AssertClosed = engine.AssertClosed
	// AssertConcurrencyMisuse reports concurrent misuse of the session.
	AssertConcurrencyMisuse = engine.AssertConcurrencyMisuse
	// ModifyChanged reports that fields changed and propagated through
	// the Rete network.
	ModifyChanged = engine.ModifyChanged
	// ModifyNoOp reports that the patch made no difference to the
	// fact's fields.
	ModifyNoOp = engine.ModifyNoOp
	// ModifyMissing reports that no such fact exists in the current
	// generation.
	ModifyMissing = engine.ModifyMissing
	// ModifyStale reports that the fact ID's generation predates the
	// session's current generation, typically from before a Reset.
	ModifyStale = engine.ModifyStale
	// ModifyValidationFailure reports that the patched fact fails
	// template validation.
	ModifyValidationFailure = engine.ModifyValidationFailure
	// ModifyDuplicate reports that the patch collides with another
	// existing fact under the template's duplicate policy.
	ModifyDuplicate = engine.ModifyDuplicate
	// ModifyLogicalSupport reports that the fact currently has logical
	// support and can't be modified directly.
	ModifyLogicalSupport = engine.ModifyLogicalSupport
	// ModifyClosed reports that the session was already closed.
	ModifyClosed = engine.ModifyClosed
	// ModifyConcurrencyMisuse reports concurrent misuse of the session.
	ModifyConcurrencyMisuse = engine.ModifyConcurrencyMisuse
	// RetractRemoved reports that the fact left working memory; any
	// logical facts it alone supported cascade away in the same
	// operation.
	RetractRemoved = engine.RetractRemoved
	// RetractStatedSupportRemoved reports that the fact had both stated
	// and logical support, its stated support was removed, and it
	// survives as logical-only.
	RetractStatedSupportRemoved = engine.RetractStatedSupportRemoved
	// RetractLogicalOnly reports that the fact exists only through
	// logical support and can't be retracted directly; retract its
	// supporting facts instead.
	RetractLogicalOnly = engine.RetractLogicalOnly
	// RetractMissing reports that no such fact exists in the current
	// generation.
	RetractMissing = engine.RetractMissing
	// RetractStale reports that the fact ID's generation predates the
	// session's current generation, typically from before a Reset.
	RetractStale = engine.RetractStale
	// RetractClosed reports that the session was already closed.
	RetractClosed = engine.RetractClosed
	// RetractValidationFailure reports an internal propagation error
	// during retraction.
	RetractValidationFailure = engine.RetractValidationFailure
	// RetractConcurrencyMisuse reports concurrent misuse of the session.
	RetractConcurrencyMisuse = engine.RetractConcurrencyMisuse
	// ResetApplied reports that the reset succeeded: the generation
	// advanced, working memory was cleared and reseeded with initial
	// facts, logical support and backchain demand were cleared, and the
	// focus stack and agenda were rebuilt.
	ResetApplied = engine.ResetApplied
	// ResetValidationFailure reports that rebuilding initial facts or
	// the Rete graph for the new generation failed.
	ResetValidationFailure = engine.ResetValidationFailure
	// ResetClosed reports that the session was already closed.
	ResetClosed = engine.ResetClosed
	// ResetConcurrencyMisuse reports concurrent misuse of the session.
	ResetConcurrencyMisuse = engine.ResetConcurrencyMisuse
	// ApplyRulesetApplied reports that the new ruleset was swapped in
	// with working memory kept, and lists which rule revisions were
	// added, removed, replaced, or unchanged.
	ApplyRulesetApplied = engine.ApplyRulesetApplied
	// ApplyRulesetUnchanged reports that the target ruleset has the
	// same RulesetID as the current one, so no work was performed.
	ApplyRulesetUnchanged = engine.ApplyRulesetUnchanged
	// ApplyRulesetIncompatible reports that the target ruleset is nil,
	// doesn't define the templates live facts depend on with an
	// identical spec, or the configured initial facts no longer
	// validate against it.
	ApplyRulesetIncompatible = engine.ApplyRulesetIncompatible
	// ApplyRulesetClosed reports that the session was already closed.
	ApplyRulesetClosed = engine.ApplyRulesetClosed
	// ApplyRulesetConcurrencyMisuse reports concurrent misuse of the
	// session.
	ApplyRulesetConcurrencyMisuse = engine.ApplyRulesetConcurrencyMisuse
)

var (
	// ErrClosedSession is returned by any session operation called after
	// Close, paired with the operation's ...Closed status.
	ErrClosedSession = engine.ErrClosedSession
	// ErrConcurrencyMisuse is returned when an operation can't acquire
	// the session's single-owner lock: overlapping calls from other
	// goroutines, or Snapshot/queries attempted while a Run is active.
	ErrConcurrencyMisuse = engine.ErrConcurrencyMisuse
	// ErrActionFailed is the sentinel [ActionFailureError].Is matches,
	// representing a rule action that returned an error during Run.
	ErrActionFailed = engine.ErrActionFailed
	// ErrFactNotFound is returned by Modify or Retract when the given
	// FactID doesn't exist in the current generation.
	ErrFactNotFound = engine.ErrFactNotFound
	// ErrStaleFactID is returned by Modify or Retract when the given
	// FactID's generation predates the session's current generation,
	// typically because a Reset happened since the ID was obtained.
	ErrStaleFactID = engine.ErrStaleFactID
	// ErrDuplicateFact is returned by Modify when the patched fact
	// collides with another existing fact under a deduplicating
	// duplicate policy.
	ErrDuplicateFact = engine.ErrDuplicateFact
	// ErrQueryNotFound is returned by Query or QueryAll for an unknown
	// query name.
	ErrQueryNotFound = engine.ErrQueryNotFound
	// ErrQueryArgument is returned when [QueryArgs] names an unknown
	// parameter, omits a required parameter, or supplies a value whose
	// kind doesn't match the declared parameter kind.
	ErrQueryArgument = engine.ErrQueryArgument
	// ErrQueryExecution is returned when running a compiled query fails
	// internally, independent of the caller's arguments.
	ErrQueryExecution = engine.ErrQueryExecution
	// ErrLogicalSupportUnavailable is returned when asserting a logical
	// fact without a valid activation to support it.
	ErrLogicalSupportUnavailable = engine.ErrLogicalSupportUnavailable
	// ErrLogicalOnlyRetract is returned by Retract when the fact exists
	// only through logical support; retract its supporting facts
	// instead.
	ErrLogicalOnlyRetract = engine.ErrLogicalOnlyRetract
	// ErrLogicalFactModify is returned by Modify when the fact currently
	// carries logical support, whether logical-only or stated-and-
	// logical.
	ErrLogicalFactModify = engine.ErrLogicalFactModify
)

// WithSessionID sets the [SessionID] returned later by [Session].ID.
func WithSessionID(id SessionID) Option {
	return engine.WithSessionID(id)
}

// WithEventListener registers a listener to receive the session's Events.
// Repeat the option to register more than one listener; a nil listener is
// ignored.
func WithEventListener(listener EventListener) Option {
	return engine.WithEventListener(listener)
}

// WithEventClock overrides the timestamp source used to stamp
// [Event].Timestamp. A nil clock is ignored, leaving the default time.Now.
func WithEventClock(clock func() time.Time) Option {
	return engine.WithEventClock(clock)
}

// WithInitialFacts appends facts to assert at construction and re-assert
// on every Reset, matching deffacts in generated .gess code. The option
// can be given more than once; facts accumulate across calls.
func WithInitialFacts(initials ...InitialFact) Option {
	return engine.WithInitialFacts(initials...)
}

// WithResetBeforeSnapshot controls whether a successful Reset populates
// [ResetResult].Before with a snapshot of working memory immediately before
// the reset. It defaults to false, since materializing that snapshot has
// a cost most sessions don't need to pay.
func WithResetBeforeSnapshot(enabled bool) Option {
	return engine.WithResetBeforeSnapshot(enabled)
}

// New builds a session from revision: it compiles the configured initial
// facts against revision, builds the Rete runtime, seeds working memory,
// and computes the initial agenda. It returns an error if revision is nil
// or the initial facts fail to validate against it.
func New(revision *rules.Ruleset, opts ...Option) (*Session, error) {
	return engine.NewSession(revision, opts...)
}
