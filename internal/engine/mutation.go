package engine

import (
	"fmt"

	gessrules "github.com/cpcf/gess/rules"
)

type MutationKind = gessrules.MutationKind

const (
	MutationAssert  = gessrules.MutationAssert
	MutationModify  = gessrules.MutationModify
	MutationRetract = gessrules.MutationRetract
	MutationReset   = gessrules.MutationReset
)

type RunStatus = gessrules.RunStatus

const (
	RunCompleted         = gessrules.RunCompleted
	RunHalted            = gessrules.RunHalted
	RunFireLimit         = gessrules.RunFireLimit
	RunCanceled          = gessrules.RunCanceled
	RunActionFailed      = gessrules.RunActionFailed
	RunClosed            = gessrules.RunClosed
	RunConcurrencyMisuse = gessrules.RunConcurrencyMisuse
	RunFailed            = gessrules.RunFailed
)

type RunResult struct {
	RunID  RunID
	Status RunStatus
	Fired  int
}

type ActionFailureError struct {
	RunID          RunID
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	ActivationID   ActivationID
	ActionName     string
	ActionIndex    int
	Source         SourceSpan
	RuleSource     SourceSpan
	ActionSource   SourceSpan
	Err            error
}

func (e *ActionFailureError) Error() string {
	if e == nil {
		return ErrActionFailed.Error()
	}

	msg := "gess: action failed"
	if location := sourceSpanLocation(e.Source); location != "" {
		msg += " at " + location
	}
	if !e.RunID.IsZero() {
		msg += " run " + e.RunID.String()
	}
	if !e.RuleID.IsZero() {
		msg += " rule " + e.RuleID.String()
	}
	if !e.RuleRevisionID.IsZero() {
		msg += " revision " + e.RuleRevisionID.String()
	}
	if !e.ActivationID.IsZero() {
		msg += " activation " + e.ActivationID.String()
	}
	if e.ActionIndex >= 0 {
		msg += fmt.Sprintf(" action %d", e.ActionIndex)
	}
	if e.ActionName != "" {
		msg += " " + fmt.Sprintf("%q", e.ActionName)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ActionFailureError) Unwrap() error {
	if e != nil && e.Err != nil {
		return e.Err
	}
	return ErrActionFailed
}

func (e *ActionFailureError) Is(target error) bool {
	return target == ErrActionFailed
}

type DuplicateKey = gessrules.DuplicateKey

type FactSupportState = gessrules.FactSupportState

const (
	FactSupportStated           = gessrules.FactSupportStated
	FactSupportLogical          = gessrules.FactSupportLogical
	FactSupportStatedAndLogical = gessrules.FactSupportStatedAndLogical
	FactSupportMetadataOnly     = gessrules.FactSupportMetadataOnly
)

type FactSupportProvenance = gessrules.FactSupportProvenance

type FieldChange = gessrules.FieldChange

type FactPatch = gessrules.FactPatch

type mutationOrigin struct {
	ActivationID   ActivationID
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	ActionName     string
	ActionIndex    int

	activationIdentityKey candidateIdentityKey
	activationOrdinal     uint64
}

func (o mutationOrigin) isZero() bool {
	return o == (mutationOrigin{})
}

func (o mutationOrigin) activationID() ActivationID {
	if !o.ActivationID.IsZero() {
		return o.ActivationID
	}
	return activationIDForIdentityKey(o.activationIdentityKey, o.activationOrdinal)
}

func (o mutationOrigin) matchesActivation(act *activation) bool {
	if o.isZero() || act == nil {
		return false
	}
	if o.RuleRevisionID != act.ruleRevisionID {
		return false
	}
	if !o.ActivationID.IsZero() ||
		o.activationIdentityKey == (candidateIdentityKey{}) ||
		act.identityKey == (candidateIdentityKey{}) {
		return o.activationID() == act.activationID()
	}
	return o.activationIdentityKey == act.identityKey && o.activationOrdinal == act.key.ordinal
}

func mutationOriginForRuleActivation(rule compiledRule, act activation) mutationOrigin {
	origin := act.mutationOrigin()
	origin.RuleID = rule.id
	return origin
}

type MutationDelta struct {
	Kind           MutationKind
	Generation     Generation
	OldGeneration  Generation
	ActivationID   ActivationID
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	SupportBefore  FactSupportProvenance
	SupportAfter   FactSupportProvenance
	Recency        Recency
	FactID         FactID
	OldVersion     FactVersion
	NewVersion     FactVersion
	Before         *FactSnapshot
	After          *FactSnapshot
	OldDuplicate   DuplicateKey
	NewDuplicate   DuplicateKey
	ChangedFields  []FieldChange
}

func (d MutationDelta) FieldChanges() []FieldChange {
	out := make([]FieldChange, len(d.ChangedFields))
	for i, field := range d.ChangedFields {
		out[i] = FieldChange{
			Field: field.Field,
			Old:   cloneValue(field.Old),
			New:   cloneValue(field.New),
		}
	}
	return out
}

type AssertStatus = gessrules.AssertStatus

const (
	AssertInserted          = gessrules.AssertInserted
	AssertExisting          = gessrules.AssertExisting
	AssertReplaced          = gessrules.AssertReplaced
	AssertValidationFailure = gessrules.AssertValidationFailure
	AssertClosed            = gessrules.AssertClosed
	AssertConcurrencyMisuse = gessrules.AssertConcurrencyMisuse
)

type AssertResult struct {
	Status       AssertStatus
	Fact         FactSnapshot
	DuplicateKey DuplicateKey
	Delta        *MutationDelta
}

func (r AssertResult) Inserted() bool {
	return r.Status == AssertInserted
}

type ModifyStatus = gessrules.ModifyStatus

const (
	ModifyChanged           = gessrules.ModifyChanged
	ModifyNoOp              = gessrules.ModifyNoOp
	ModifyMissing           = gessrules.ModifyMissing
	ModifyStale             = gessrules.ModifyStale
	ModifyValidationFailure = gessrules.ModifyValidationFailure
	ModifyDuplicate         = gessrules.ModifyDuplicate
	ModifyLogicalSupport    = gessrules.ModifyLogicalSupport
	ModifyClosed            = gessrules.ModifyClosed
	ModifyConcurrencyMisuse = gessrules.ModifyConcurrencyMisuse
)

type ModifyResult struct {
	Status ModifyStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

func (r ModifyResult) Changed() bool {
	return r.Status == ModifyChanged
}

type RetractStatus = gessrules.RetractStatus

const (
	RetractRemoved              = gessrules.RetractRemoved
	RetractStatedSupportRemoved = gessrules.RetractStatedSupportRemoved
	RetractLogicalOnly          = gessrules.RetractLogicalOnly
	RetractMissing              = gessrules.RetractMissing
	RetractStale                = gessrules.RetractStale
	RetractClosed               = gessrules.RetractClosed
	RetractValidationFailure    = gessrules.RetractValidationFailure
	RetractConcurrencyMisuse    = gessrules.RetractConcurrencyMisuse
)

type RetractResult struct {
	Status RetractStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

func (r RetractResult) Removed() bool {
	return r.Status == RetractRemoved
}

type ResetStatus = gessrules.ResetStatus

const (
	ResetApplied           = gessrules.ResetApplied
	ResetValidationFailure = gessrules.ResetValidationFailure
	ResetClosed            = gessrules.ResetClosed
	ResetConcurrencyMisuse = gessrules.ResetConcurrencyMisuse
)

type ResetResult struct {
	Status     ResetStatus
	Generation Generation
	Before     Snapshot
	Delta      MutationDelta
}

type ApplyRulesetStatus = gessrules.ApplyRulesetStatus

const (
	ApplyRulesetApplied           = gessrules.ApplyRulesetApplied
	ApplyRulesetUnchanged         = gessrules.ApplyRulesetUnchanged
	ApplyRulesetIncompatible      = gessrules.ApplyRulesetIncompatible
	ApplyRulesetClosed            = gessrules.ApplyRulesetClosed
	ApplyRulesetConcurrencyMisuse = gessrules.ApplyRulesetConcurrencyMisuse
)

type RuleRevisionSummary struct {
	RuleID     RuleID
	RevisionID RuleRevisionID
}

type RuleReplacement struct {
	RuleID        RuleID
	OldRevisionID RuleRevisionID
	NewRevisionID RuleRevisionID
}

type ApplyRulesetResult struct {
	Status                 ApplyRulesetStatus
	PreviousRulesetID      RulesetID
	CurrentRulesetID       RulesetID
	AddedRuleRevisions     []RuleRevisionSummary
	RemovedRuleRevisions   []RuleRevisionSummary
	ReplacedRuleRevisions  []RuleReplacement
	UnchangedRuleRevisions []RuleRevisionSummary
}

func (r ApplyRulesetResult) Applied() bool {
	return r.Status == ApplyRulesetApplied
}
