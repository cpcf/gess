package gess

import (
	"context"
	"errors"
	"testing"
)

func TestWorkspaceRejectsConflictingModuleRedeclarationAtCompile(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddModule(ModuleSpec{Name: "ask", Description: "questions"}); err != nil {
		t.Fatalf("AddModule(ask questions): %v", err)
	}
	if err := workspace.AddModule(ModuleSpec{Name: "ask", Description: "answers"}); err != nil {
		t.Fatalf("AddModule(ask answers): %v", err)
	}

	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile succeeded with conflicting module redeclaration")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(err, ErrValidation) = false for %v", err)
	}

	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("errors.As(err, *ValidationError) = false for %T", err)
	}
	if validation.Reason != "duplicate module" {
		t.Fatalf("validation reason = %q, want duplicate module", validation.Reason)
	}
}

func TestWorkspaceAcceptsIdenticalModuleRedeclaration(t *testing.T) {
	autoFocus := true
	workspace := NewWorkspace()
	spec := ModuleSpec{
		Name:        "ask",
		Description: "questions",
		AutoFocus:   &autoFocus,
	}
	if err := workspace.AddModule(spec); err != nil {
		t.Fatalf("AddModule first: %v", err)
	}
	autoFocus = false
	redeclaredAutoFocus := true
	spec.AutoFocus = &redeclaredAutoFocus
	if err := workspace.AddModule(spec); err != nil {
		t.Fatalf("AddModule second: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	modules := revision.Modules()
	if len(modules) != 2 || modules[0].Name() != MainModule || modules[1].Name() != "ask" {
		t.Fatalf("modules = %#v, want MAIN then ask", modules)
	}
	module, ok := revision.Module("ask")
	if !ok {
		t.Fatal("compiled revision did not contain ask module")
	}
	value, hasDefault := module.AutoFocusDefault()
	if !hasDefault || !value {
		t.Fatalf("ask auto-focus default = (%t, %t), want true default", value, hasDefault)
	}
}
