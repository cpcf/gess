package rules

// FactPatch describes a fact modification: Set overwrites the named fields and
// Unset clears the named fields, restoring their template defaults.
type FactPatch struct {
	Set   Fields
	Unset []string
}

// MutationKind is the kind of change recorded in a MutationDelta.
type MutationKind string

const (
	MutationAssert  MutationKind = "assert"
	MutationModify  MutationKind = "modify"
	MutationRetract MutationKind = "retract"
	MutationReset   MutationKind = "reset"
)

// DuplicateKey is the computed duplicate-detection key for a fact.
type DuplicateKey string

// FactSupportState classifies how a fact is supported.
type FactSupportState string

const (
	FactSupportStated           FactSupportState = "stated"
	FactSupportLogical          FactSupportState = "logical"
	FactSupportStatedAndLogical FactSupportState = "stated_and_logical"
	FactSupportMetadataOnly     FactSupportState = "metadata_only"
)

// FactSupportProvenance is a fact's support classification.
type FactSupportProvenance struct {
	State FactSupportState
}

// FieldChange describes one field's before and after value.
type FieldChange struct {
	Field string
	Old   Value
	New   Value
}

// CloneFieldChange returns a defensive copy of change.
func CloneFieldChange(change FieldChange) FieldChange {
	return FieldChange{
		Field: change.Field,
		Old:   CloneValue(change.Old),
		New:   CloneValue(change.New),
	}
}

// CloneFieldChanges returns a defensive copy of changes.
func CloneFieldChanges(changes []FieldChange) []FieldChange {
	out := make([]FieldChange, len(changes))
	for i, change := range changes {
		out[i] = CloneFieldChange(change)
	}
	return out
}

// MutationDelta describes one mutation's effect.
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

// FieldChanges returns a cloned copy of changed fields.
func (d MutationDelta) FieldChanges() []FieldChange {
	return CloneFieldChanges(d.ChangedFields)
}

// CloneMutationDelta returns a defensive copy of delta.
func CloneMutationDelta(delta MutationDelta) MutationDelta {
	out := delta
	out.Before = CloneFactSnapshotPtr(delta.Before)
	out.After = CloneFactSnapshotPtr(delta.After)
	out.ChangedFields = CloneFieldChanges(delta.ChangedFields)
	return out
}

// CloneMutationDeltaPtr returns a defensive copy of delta when non-nil.
func CloneMutationDeltaPtr(delta *MutationDelta) *MutationDelta {
	if delta == nil {
		return nil
	}
	out := CloneMutationDelta(*delta)
	return &out
}

// AssertStatus is the outcome of an assert.
type AssertStatus string

const (
	AssertInserted          AssertStatus = "inserted"
	AssertExisting          AssertStatus = "existing"
	AssertReplaced          AssertStatus = "replaced"
	AssertValidationFailure AssertStatus = "validation_failure"
	AssertClosed            AssertStatus = "closed"
	AssertConcurrencyMisuse AssertStatus = "concurrency_misuse"
)

// AssertResult reports the outcome of an assert.
type AssertResult struct {
	Status       AssertStatus
	Fact         FactSnapshot
	DuplicateKey DuplicateKey
	Delta        *MutationDelta
}

// Inserted reports whether the assert inserted a new fact.
func (r AssertResult) Inserted() bool {
	return r.Status == AssertInserted
}

// CloneAssertResult returns a defensive copy of result.
func CloneAssertResult(result AssertResult) AssertResult {
	out := result
	out.Fact = CloneFactSnapshot(result.Fact)
	out.Delta = CloneMutationDeltaPtr(result.Delta)
	return out
}

// ModifyStatus is the outcome of a modify call.
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

// ModifyResult reports the outcome of a modify.
type ModifyResult struct {
	Status ModifyStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

// Changed reports whether the modify changed the fact.
func (r ModifyResult) Changed() bool {
	return r.Status == ModifyChanged
}

// CloneModifyResult returns a defensive copy of result.
func CloneModifyResult(result ModifyResult) ModifyResult {
	out := result
	out.Fact = CloneFactSnapshot(result.Fact)
	out.Delta = CloneMutationDeltaPtr(result.Delta)
	return out
}

// RetractStatus is the outcome of a retract call.
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

// RetractResult reports the outcome of a retract.
type RetractResult struct {
	Status RetractStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

// Removed reports whether the retract removed the fact.
func (r RetractResult) Removed() bool {
	return r.Status == RetractRemoved
}

// CloneRetractResult returns a defensive copy of result.
func CloneRetractResult(result RetractResult) RetractResult {
	out := result
	out.Fact = CloneFactSnapshot(result.Fact)
	out.Delta = CloneMutationDeltaPtr(result.Delta)
	return out
}

// RunStatus is the outcome of a Run.
type RunStatus string

const (
	RunCompleted         RunStatus = "completed"
	RunHalted            RunStatus = "halted"
	RunFireLimit         RunStatus = "fire_limit"
	RunCanceled          RunStatus = "canceled"
	RunActionFailed      RunStatus = "action_failed"
	RunClosed            RunStatus = "closed"
	RunConcurrencyMisuse RunStatus = "concurrency_misuse"
	RunFailed            RunStatus = "failed"
)

// ResetStatus is the outcome of a reset.
type ResetStatus string

const (
	ResetApplied           ResetStatus = "applied"
	ResetValidationFailure ResetStatus = "validation_failure"
	ResetClosed            ResetStatus = "closed"
	ResetConcurrencyMisuse ResetStatus = "concurrency_misuse"
)

// ApplyRulesetStatus is the outcome of applying a ruleset to a session.
type ApplyRulesetStatus string

const (
	ApplyRulesetApplied           ApplyRulesetStatus = "applied"
	ApplyRulesetUnchanged         ApplyRulesetStatus = "unchanged"
	ApplyRulesetIncompatible      ApplyRulesetStatus = "incompatible"
	ApplyRulesetClosed            ApplyRulesetStatus = "closed"
	ApplyRulesetConcurrencyMisuse ApplyRulesetStatus = "concurrency_misuse"
)
