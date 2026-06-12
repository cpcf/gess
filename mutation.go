package gess

type MutationKind string

const (
	MutationAssert  MutationKind = "assert"
	MutationModify  MutationKind = "modify"
	MutationRetract MutationKind = "retract"
	MutationReset   MutationKind = "reset"
)

type DuplicateKey string

type FieldChange struct {
	Field string
	Old   Value
	New   Value
}

type FactPatch struct {
	Set   Fields
	Unset []string
}

type MutationDelta struct {
	Kind          MutationKind
	Generation    Generation
	OldGeneration Generation
	Recency       Recency
	FactID        FactID
	OldVersion    FactVersion
	NewVersion    FactVersion
	Before        *FactSnapshot
	After         *FactSnapshot
	OldDuplicate  DuplicateKey
	NewDuplicate  DuplicateKey
	ChangedFields []FieldChange
}

func (d MutationDelta) FieldChanges() []FieldChange {
	out := make([]FieldChange, len(d.ChangedFields))
	copy(out, d.ChangedFields)
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
	RetractRemoved           RetractStatus = "removed"
	RetractMissing           RetractStatus = "missing"
	RetractStale             RetractStatus = "stale"
	RetractClosed            RetractStatus = "closed"
	RetractConcurrencyMisuse RetractStatus = "concurrency_misuse"
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
