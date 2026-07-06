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
