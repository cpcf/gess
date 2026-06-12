package gess

import (
	"context"
	"errors"
	"testing"
)

func TestEmptyRulesetCreatesIsolatedSessions(t *testing.T) {
	ctx := context.Background()
	revision := mustCompile(t)

	sessionA := mustSession(t, revision, "session-a")
	sessionB := mustSession(t, revision, "session-b")

	snapshotA := mustSnapshot(t, ctx, sessionA)
	snapshotB := mustSnapshot(t, ctx, sessionB)

	if snapshotA.RulesetID() != revision.ID() {
		t.Fatalf("snapshot A ruleset ID = %q, want %q", snapshotA.RulesetID(), revision.ID())
	}
	if snapshotB.RulesetID() != revision.ID() {
		t.Fatalf("snapshot B ruleset ID = %q, want %q", snapshotB.RulesetID(), revision.ID())
	}
	if snapshotA.SessionID() == snapshotB.SessionID() {
		t.Fatalf("sessions should be distinguishable: both snapshots used %q", snapshotA.SessionID())
	}
	if snapshotA.Generation() != 1 || snapshotB.Generation() != 1 {
		t.Fatalf("new sessions should start at generation 1, got %d and %d", snapshotA.Generation(), snapshotB.Generation())
	}
	if snapshotA.Len() != 0 || snapshotB.Len() != 0 {
		t.Fatalf("new sessions should have empty working memory, got %d and %d facts", snapshotA.Len(), snapshotB.Len())
	}
}

func TestWorkspaceCompilesTemplatesIntoImmutableRevision(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	spec := TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}

	if err := workspace.AddTemplate(spec); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	spec.Fields[0].Name = "mutated-by-caller"

	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("compiled revision did not contain person template")
	}
	if template.Name() != "person" || template.Key() != "person" {
		t.Fatalf("template identity = (%q, %q), want (person, person)", template.Name(), template.Key())
	}

	fields := template.Fields()
	if len(fields) != 1 || fields[0].Name != "name" {
		t.Fatalf("compiled fields = %#v, want original name field", fields)
	}

	fields[0].Name = "mutated-through-accessor"
	fields = template.Fields()
	if fields[0].Name != "name" {
		t.Fatalf("Template.Fields leaked mutable state: %#v", fields)
	}
}

func TestValidationErrorsAreStructured(t *testing.T) {
	workspace := NewWorkspace()
	err := workspace.AddTemplate(TemplateSpec{})
	if err == nil {
		t.Fatal("AddTemplate should reject an unnamed template")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(err, ErrValidation) = false for %v", err)
	}

	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("errors.As(err, *ValidationError) = false for %T", err)
	}
}

func TestEmptySnapshotIsImmutable(t *testing.T) {
	ctx := context.Background()
	session := mustSession(t, mustCompile(t), "session")
	snapshot := mustSnapshot(t, ctx, session)

	facts := snapshot.Facts()
	facts = append(facts, FactSnapshot{name: "caller-added"})

	if snapshot.Len() != 0 {
		t.Fatalf("mutating returned facts slice changed snapshot length to %d", snapshot.Len())
	}
	if len(snapshot.Facts()) != 0 {
		t.Fatalf("mutating returned facts slice changed snapshot facts to %#v", snapshot.Facts())
	}
}

func mustCompile(t *testing.T, specs ...TemplateSpec) *Ruleset {
	t.Helper()
	workspace := NewWorkspace()
	for _, spec := range specs {
		if err := workspace.AddTemplate(spec); err != nil {
			t.Fatalf("AddTemplate(%q): %v", spec.Name, err)
		}
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}

func mustSession(t *testing.T, revision *Ruleset, id SessionID) *Session {
	t.Helper()
	session, err := NewSession(revision, WithSessionID(id))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func mustSnapshot(t *testing.T, ctx context.Context, session *Session) Snapshot {
	t.Helper()
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snapshot
}
