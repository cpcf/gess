package engine

import "fmt"

type MutationKind string

const (
	MutationAssert  MutationKind = "assert"
	MutationModify  MutationKind = "modify"
	MutationRetract MutationKind = "retract"
	MutationReset   MutationKind = "reset"
)

type RunStatus string

const (
	RunCompleted         RunStatus = "completed"
	RunHalted            RunStatus = "halted"
	RunCanceled          RunStatus = "canceled"
	RunActionFailed      RunStatus = "action_failed"
	RunClosed            RunStatus = "closed"
	RunConcurrencyMisuse RunStatus = "concurrency_misuse"
	RunFailed            RunStatus = "failed"
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
	Err            error
}

func (e *ActionFailureError) Error() string {
	if e == nil {
		return ErrActionFailed.Error()
	}

	msg := "gess: action failed"
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

type DuplicateKey string

type FactSupportState string

const (
	FactSupportStated           FactSupportState = "stated"
	FactSupportLogical          FactSupportState = "logical"
	FactSupportStatedAndLogical FactSupportState = "stated_and_logical"
	FactSupportMetadataOnly     FactSupportState = "metadata_only"
)

type FactSupportProvenance struct {
	State      FactSupportState
	provenance supportProvenance
}

type supportProvenance struct {
	source uint64
	trace  uint64
}

type FieldChange struct {
	Field string
	Old   Value
	New   Value
}

type FactPatch struct {
	Set   Fields
	Unset []string
}

type mutationOrigin struct {
	ActivationID   ActivationID
	RuleID         RuleID
	RuleRevisionID RuleRevisionID

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
	if !o.RuleID.IsZero() && !act.ruleID.IsZero() && o.RuleID != act.ruleID {
		return false
	}
	return o.activationID() == act.activationID()
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

type AssertStatus string

const (
	AssertInserted          AssertStatus = "inserted"
	AssertExisting          AssertStatus = "existing"
	AssertValidationFailure AssertStatus = "validation_failure"
	AssertClosed            AssertStatus = "closed"
	AssertConcurrencyMisuse AssertStatus = "concurrency_misuse"
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

type ModifyStatus string

const (
	ModifyChanged           ModifyStatus = "changed"
	ModifyNoOp              ModifyStatus = "no_op"
	ModifyMissing           ModifyStatus = "missing"
	ModifyStale             ModifyStatus = "stale"
	ModifyValidationFailure ModifyStatus = "validation_failure"
	ModifyDuplicate         ModifyStatus = "duplicate"
	ModifyLogicalSupport    ModifyStatus = "logical_support"
	ModifyClosed            ModifyStatus = "closed"
	ModifyConcurrencyMisuse ModifyStatus = "concurrency_misuse"
)

type ModifyResult struct {
	Status ModifyStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

func (r ModifyResult) Changed() bool {
	return r.Status == ModifyChanged
}

type RetractStatus string

const (
	RetractRemoved              RetractStatus = "removed"
	RetractStatedSupportRemoved RetractStatus = "stated_support_removed"
	RetractLogicalOnly          RetractStatus = "logical_only"
	RetractMissing              RetractStatus = "missing"
	RetractStale                RetractStatus = "stale"
	RetractClosed               RetractStatus = "closed"
	RetractValidationFailure    RetractStatus = "validation_failure"
	RetractConcurrencyMisuse    RetractStatus = "concurrency_misuse"
)

type RetractResult struct {
	Status RetractStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

func (r RetractResult) Removed() bool {
	return r.Status == RetractRemoved
}

type ResetStatus string

const (
	ResetApplied           ResetStatus = "applied"
	ResetValidationFailure ResetStatus = "validation_failure"
	ResetClosed            ResetStatus = "closed"
	ResetConcurrencyMisuse ResetStatus = "concurrency_misuse"
)

type ResetResult struct {
	Status     ResetStatus
	Generation Generation
	Before     Snapshot
	Delta      MutationDelta
}

type ApplyRulesetStatus string

const (
	ApplyRulesetApplied           ApplyRulesetStatus = "applied"
	ApplyRulesetUnchanged         ApplyRulesetStatus = "unchanged"
	ApplyRulesetIncompatible      ApplyRulesetStatus = "incompatible"
	ApplyRulesetClosed            ApplyRulesetStatus = "closed"
	ApplyRulesetConcurrencyMisuse ApplyRulesetStatus = "concurrency_misuse"
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
