package engine

import (
	"context"
	"testing"
)

func TestDiffSnapshots(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session := mustSession(t, revision, "diff-session")
	ctx := context.Background()

	// Baseline: assert one source, run so the logical chain forms.
	if _, err := session.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := mustSnapshot(t, ctx, session)

	// Identical snapshot: empty diff.
	if diff := DiffSnapshots(before, before); !diff.Empty() {
		t.Fatalf("self-diff = %+v, want empty", diff)
	}

	// Add a second source.
	if _, err := session.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-2"})); err != nil {
		t.Fatalf("Assert(s-2): %v", err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := mustSnapshot(t, ctx, session)

	diff := DiffSnapshots(before, after)
	if len(diff.Retracted) != 0 {
		t.Fatalf("retracted = %d, want 0", len(diff.Retracted))
	}
	// The new source plus its derived and child logical facts are added.
	if len(diff.Added) < 3 {
		t.Fatalf("added = %d, want at least 3 (source + derived + child)", len(diff.Added))
	}
	// Deterministic: identical repeated diff.
	if diff2 := DiffSnapshots(before, after); !sameAdded(diff, diff2) {
		t.Fatalf("diff not deterministic")
	}

	// Retract a source; the diff shows the cascade as retractions.
	source1 := singleFactByField(t, after, "source", "id", "s-1")
	if _, err := session.Retract(ctx, source1); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	post := mustSnapshot(t, ctx, session)
	retractDiff := DiffSnapshots(after, post)
	if len(retractDiff.Retracted) < 3 {
		t.Fatalf("retracted = %d, want at least 3 (source + cascade)", len(retractDiff.Retracted))
	}
}

func TestDiffSnapshotsFieldAndSupportChange(t *testing.T) {
	workspace := NewWorkspace()
	taskKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}).Key()
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "diff-fields")
	ctx := context.Background()

	task, err := session.Assert(ctx, taskKey, mustFields(t, map[string]any{"id": "t-1", "status": "open"}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	before := mustSnapshot(t, ctx, session)

	if _, err := session.Modify(ctx, task.Fact.ID(), FactPatch{Set: Fields{"status": newStringValue("closed")}}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	after := mustSnapshot(t, ctx, session)

	diff := DiffSnapshots(before, after)
	if len(diff.Modified) != 1 {
		t.Fatalf("modified = %d, want 1", len(diff.Modified))
	}
	mod := diff.Modified[0]
	if len(mod.ChangedFields) != 1 || mod.ChangedFields[0].Field != "status" {
		t.Fatalf("changed fields = %+v, want status", mod.ChangedFields)
	}
	old, _ := mod.ChangedFields[0].Old.AsString()
	updated, _ := mod.ChangedFields[0].New.AsString()
	if old != "open" || updated != "closed" {
		t.Fatalf("status change = %q -> %q, want open -> closed", old, updated)
	}
}

// A fact whose fields are unchanged but whose support state transitions must
// appear in Modified with empty ChangedFields and the support transition
// recorded in SupportBefore/SupportAfter.
func TestDiffSnapshotsSupportOnlyChange(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, true)
	session := mustSession(t, revision, "diff-support")
	ctx := context.Background()

	// Assert a source and run so derived{id:shared} exists as a logical-only
	// fact (the derive action keys the derived fact off the source's group).
	if _, err := session.Assert(ctx, sourceKey, mustFields(t, map[string]any{"id": "s-1", "group": "shared"})); err != nil {
		t.Fatalf("Assert(source): %v", err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := mustSnapshot(t, ctx, session)
	derivedID := singleFactByField(t, before, "derived", "id", "shared")
	if got := factSupportStateByID(t, before, derivedID); got != FactSupportLogical {
		t.Fatalf("derived support before = %v, want %v", got, FactSupportLogical)
	}

	// A stated assert of the same derived key merges into the logical fact:
	// identical fields, support state transitions to stated_and_logical.
	res, err := session.assertByName(ctx, "derived", mustFields(t, map[string]any{"id": "shared"}))
	if err != nil {
		t.Fatalf("assert(derived stated): %v", err)
	}
	if res.Status != AssertExisting {
		t.Fatalf("stated assert status = %v, want AssertExisting (merge)", res.Status)
	}
	after := mustSnapshot(t, ctx, session)

	diff := DiffSnapshots(before, after)
	var mod *FactModification
	for i := range diff.Modified {
		if diff.Modified[i].After.ID() == derivedID {
			mod = &diff.Modified[i]
			break
		}
	}
	if mod == nil {
		t.Fatalf("derived fact not in Modified; diff = %+v", diff)
	}
	if len(mod.ChangedFields) != 0 {
		t.Fatalf("ChangedFields = %+v, want empty (support-only change)", mod.ChangedFields)
	}
	if mod.SupportBefore != FactSupportLogical || mod.SupportAfter != FactSupportStatedAndLogical {
		t.Fatalf("support transition = %v -> %v, want %v -> %v", mod.SupportBefore, mod.SupportAfter, FactSupportLogical, FactSupportStatedAndLogical)
	}
}

func factSupportStateByID(t *testing.T, snapshot Snapshot, id FactID) FactSupportState {
	t.Helper()
	fact, ok := snapshot.Fact(id)
	if !ok {
		t.Fatalf("fact %s not in snapshot", id)
	}
	return fact.Support().State
}

func sameAdded(a, b SnapshotDiff) bool {
	if len(a.Added) != len(b.Added) {
		return false
	}
	for i := range a.Added {
		if a.Added[i].ID() != b.Added[i].ID() {
			return false
		}
	}
	return true
}

func singleFactByField(t *testing.T, snapshot Snapshot, name, field, value string) FactID {
	t.Helper()
	for _, fact := range snapshot.FactsByName(name) {
		if v, ok := fact.Field(field); ok {
			if s, _ := v.AsString(); s == value {
				return fact.ID()
			}
		}
	}
	t.Fatalf("no %s fact with %s=%s", name, field, value)
	return FactID{}
}
