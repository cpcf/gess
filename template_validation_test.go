package gess

import (
	"context"
	"errors"
	"testing"
)

func TestTemplateDefaultsApplyBeforeValidation(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "order",
		Fields: []FieldSpec{
			{Name: "status", Kind: ValueString, Required: true, Default: "active", AllowedValues: []any{"active", "pending"}},
			{Name: "count", Kind: ValueInt, Default: 1},
		},
	})
	session := mustSession(t, revision, "default-session")
	template, ok := revision.Template("order")
	if !ok {
		t.Fatal("expected compiled template order")
	}

	result, err := session.insertFact("order", template.Key(), mustFields(t, map[string]any{}))
	if err != nil {
		t.Fatalf("insert with defaults: %v", err)
	}

	fields := result.Fact.Fields()
	status, ok := fields["status"]
	if !ok {
		t.Fatal("expected status field from default")
	}
	if status.Kind() != ValueString {
		t.Fatalf("status kind = %q, want %q", status.Kind(), ValueString)
	}
	if status.stringValue != "active" {
		t.Fatalf("status value = %v, want active", status)
	}

	presence, ok := result.Fact.FieldPresence("status")
	if !ok || presence != FieldPresenceDefault {
		t.Fatalf("status presence = %q, want %q", presence, FieldPresenceDefault)
	}
	if _, ok := result.Fact.FieldPresence("count"); !ok {
		t.Fatalf("count presence not available")
	}
}

func TestTemplateExplicitNullDefault(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "item",
		Fields: []FieldSpec{
			{Name: "note", Kind: ValueNull, Required: true, HasDefault: true, Default: nil},
		},
	})
	session := mustSession(t, revision, "null-default-session")
	template, ok := revision.Template("item")
	if !ok {
		t.Fatal("expected compiled template item")
	}

	result, err := session.insertFact("item", template.Key(), mustFields(t, map[string]any{}))
	if err != nil {
		t.Fatalf("insert with null default: %v", err)
	}
	fields := result.Fact.Fields()
	if got := fields["note"].Kind(); got != ValueNull {
		t.Fatalf("note default kind = %q, want %q", got, ValueNull)
	}
	if presence, ok := result.Fact.FieldPresence("note"); !ok || presence != FieldPresenceDefault {
		t.Fatalf("note presence = %q, want %q", presence, FieldPresenceDefault)
	}
}

func TestTemplateMissingRequiredFieldsFailValidation(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	session := mustSession(t, revision, "required-session")
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected compiled template person")
	}

	_, err := session.insertFact("person", template.Key(), mustFields(t, map[string]any{}))
	if err == nil {
		t.Fatal("missing required field should fail")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for missing required field, got %T: %v", err, err)
	}
	if validation.FieldName != "name" {
		t.Fatalf("missing required field name = %q, want %q", validation.FieldName, "name")
	}
	if got := len(mustSnapshot(t, context.Background(), session).Facts()); got != 0 {
		t.Fatalf("snapshot size after failed validation = %d, want 0", got)
	}
}

func TestTemplateClosedTemplateRejectsUnknownFields(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	session := mustSession(t, revision, "closed-session")
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected compiled template person")
	}

	_, err := session.insertFact("person", template.Key(), mustFields(t, map[string]any{
		"name":  "Ada",
		"title": "Dr.",
	}))
	if err == nil {
		t.Fatal("closed template should reject unknown field")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for unknown field, got %T: %v", err, err)
	}
	if validation.FieldName != "title" {
		t.Fatalf("unexpected field in validation error = %q, want %q", validation.FieldName, "title")
	}
}

func TestTemplateInvalidTypeAndAllowedValues(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "device",
		Fields: []FieldSpec{{Name: "count", Kind: ValueInt, Required: true}},
	})
	session := mustSession(t, revision, "invalid-type-session")
	template, ok := revision.Template("device")
	if !ok {
		t.Fatal("expected compiled template device")
	}

	_, err := session.insertFact("device", template.Key(), mustFields(t, map[string]any{"count": "one"}))
	if err == nil {
		t.Fatal("non-int value should fail int validation")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(%v, ErrValidation) == false", err)
	}
	if got := len(mustSnapshot(t, context.Background(), session).Facts()); got != 0 {
		t.Fatalf("snapshot changed after invalid type: %d facts", got)
	}

	revision = mustCompile(t, TemplateSpec{
		Name:   "light",
		Fields: []FieldSpec{{Name: "status", Kind: ValueString, AllowedValues: []any{"on", "off"}}},
	})
	session = mustSession(t, revision, "allowed-session")
	template, ok = revision.Template("light")
	if !ok {
		t.Fatal("expected compiled template light")
	}

	_, err = session.insertFact("light", template.Key(), mustFields(t, map[string]any{"status": "dim"}))
	if err == nil {
		t.Fatal("status outside allowed values should fail validation")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(%v, ErrValidation) == false", err)
	}
	if got := len(mustSnapshot(t, context.Background(), session).Facts()); got != 0 {
		t.Fatalf("snapshot changed after invalid allowed value: %d facts", got)
	}
}

func TestTemplateDuplicateKeysUsePostDefaultValues(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "event",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active"},
		},
	})
	session := mustSession(t, revision, "duplicate-default-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected compiled template event")
	}

	first, err := session.insertFact("event", template.Key(), mustFields(t, map[string]any{"id": "evt-1"}))
	if err != nil {
		t.Fatalf("insert baseline event: %v", err)
	}

	second, err := session.insertFact("event", template.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "active",
	}))
	if err != nil {
		t.Fatalf("insert explicit active status: %v", err)
	}
	if second.Status != AssertExisting {
		t.Fatalf("expected duplicate status event to be existing")
	}
	if second.Fact.ID() != first.Fact.ID() {
		t.Fatalf("duplicate event id = %q, expected %q", second.Fact.ID(), first.Fact.ID())
	}

	_, err = session.insertFact("event", template.Key(), mustFields(t, map[string]any{
		"id":     "evt-1",
		"status": "inactive",
	}))
	if err != nil {
		t.Fatalf("insert different status event: %v", err)
	}
	if got, want := len(mustSnapshot(t, context.Background(), session).Facts()), 2; got != want {
		t.Fatalf("snapshot size after explicit different status = %d, want %d", got, want)
	}
}

func TestTemplateStableKeysAndCompatibilityMetadata(t *testing.T) {
	revisionA := mustCompile(t, TemplateSpec{
		Name:             "person",
		Key:              "person:v1",
		CompatibilityKey: TemplateKey("person:v1"),
		Fields:           []FieldSpec{{Name: "name", Kind: ValueString}},
		DuplicatePolicy:  DuplicateStructural,
	})
	revisionB := mustCompile(t, TemplateSpec{
		Name:             "person",
		Key:              "person:v1",
		CompatibilityKey: TemplateKey("person:v1"),
		Fields:           []FieldSpec{{Name: "name", Kind: ValueString}, {Name: "department", Kind: ValueString}},
		DuplicatePolicy:  DuplicateStructural,
	})

	templateA, ok := revisionA.Template("person")
	if !ok {
		t.Fatal("expected template from revision A")
	}
	templateB, ok := revisionB.Template("person")
	if !ok {
		t.Fatal("expected template from revision B")
	}

	if templateA.Key() != "person:v1" {
		t.Fatalf("revision A key = %q, want person:v1", templateA.Key())
	}
	if templateB.Key() != "person:v1" {
		t.Fatalf("revision B key = %q, want person:v1", templateB.Key())
	}
	if templateA.CompatibilityKey() != templateB.CompatibilityKey() {
		t.Fatalf("compatibility key mismatch: %q vs %q", templateA.CompatibilityKey(), templateB.CompatibilityKey())
	}

	reloaded, ok := revisionB.TemplateByKey("person:v1")
	if !ok {
		t.Fatal("expected to retrieve template from revision B by stable key")
	}
	if reloaded.Name() != "person" {
		t.Fatalf("retrieved template name = %q, want person", reloaded.Name())
	}
}

func TestTemplateCompilationNormalizesSemanticOrdering(t *testing.T) {
	revisionA := mustCompile(t, TemplateSpec{
		Name: "device",
		Fields: []FieldSpec{
			{Name: "state", Kind: ValueString, AllowedValues: []any{"on", "off"}},
			{Name: "id", Kind: ValueString},
		},
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"state", "id"},
	})
	revisionB := mustCompile(t, TemplateSpec{
		Name: "device",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString},
			{Name: "state", Kind: ValueString, AllowedValues: []any{"off", "on"}},
		},
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id", "state"},
	})
	if revisionA.ID() != revisionB.ID() {
		t.Fatalf("semantically equivalent templates produced different revision IDs: %q vs %q", revisionA.ID(), revisionB.ID())
	}
}

func TestTemplateDoesNotRetainCallerOwnedDefaultOrAllowedValues(t *testing.T) {
	defaultPayload := map[string]any{"count": 1}
	allowedPayload := map[string]any{"count": 2}
	workspace := NewWorkspace()
	err := workspace.AddTemplate(TemplateSpec{
		Name: "payload",
		Fields: []FieldSpec{
			{
				Name:          "body",
				Kind:          ValueMap,
				Default:       defaultPayload,
				AllowedValues: []any{defaultPayload, allowedPayload},
			},
		},
	})
	if err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}

	defaultPayload["count"] = 99
	allowedPayload["count"] = 100

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	template, ok := revision.Template("payload")
	if !ok {
		t.Fatal("expected compiled payload template")
	}
	session := mustSession(t, revision, "template-immutability-session")
	result, err := session.insertFact("payload", template.Key(), mustFields(t, map[string]any{}))
	if err != nil {
		t.Fatalf("insert with canonical default: %v", err)
	}

	body := result.Fact.Fields()["body"].data.(map[string]Value)
	if got := body["count"].intValue; got != 1 {
		t.Fatalf("default count = %d, want 1", got)
	}
}

func TestDynamicFactsWithoutTemplateDeclaration(t *testing.T) {
	session := mustSession(t, mustCompile(t), "dynamic-template-session")
	result, err := session.insertFact("event", "", mustFields(t, map[string]any{"name": "startup"}))
	if err != nil {
		t.Fatalf("dynamic fact insert failed: %v", err)
	}
	if !result.Inserted() {
		t.Fatalf("dynamic fact should be inserted")
	}
	if _, ok := result.Fact.Fields()["name"]; !ok {
		t.Fatal("dynamic fact should keep provided field")
	}
}
