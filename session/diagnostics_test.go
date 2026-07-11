package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestDiagnosticsPublicFacade(t *testing.T) {
	ctx := context.Background()
	workspace := session.NewWorkspace()
	err := workspace.AddTemplate(rules.TemplateSpec{
		Name:   "item",
		Fields: []rules.FieldSpec{{Name: "id", Kind: rules.ValueString, Required: true}},
	})
	if err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	revision, err := rules.Compile(ctx, workspace)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	sess, err := session.New(revision, session.WithSessionID("public-diagnostics"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close()
	fields, err := rules.NewFields(map[string]any{"id": "one"})
	if err != nil {
		t.Fatalf("NewFields: %v", err)
	}
	if _, err := sess.Assert(ctx, rules.TemplateKey("item"), fields); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	report, err := sess.Diagnostics(ctx, session.WithDiagnosticsFacts())
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if report.Schema != session.DiagnosticsSchemaVersion || len(report.Facts) != 1 {
		t.Fatalf("public diagnostics = %#v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("json.Marshal returned empty document")
	}
}
