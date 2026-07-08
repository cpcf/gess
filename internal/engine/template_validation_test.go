package engine

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
	template, ok := revision.compiledTemplate("order")
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
	if valueString(status) != "active" {
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
	template, ok := revision.compiledTemplate("item")
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
	template, ok := revision.compiledTemplate("person")
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

func TestTemplateDeclaredTemplateRejectsUnknownFields(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	})
	session := mustSession(t, revision, "closed-session")
	template, ok := revision.compiledTemplate("person")
	if !ok {
		t.Fatal("expected compiled template person")
	}

	_, err := session.insertFact("person", template.Key(), mustFields(t, map[string]any{
		"name":  "Ada",
		"title": "Dr.",
	}))
	if err == nil {
		t.Fatal("declared template should reject unknown field")
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
	template, ok := revision.compiledTemplate("device")
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
	template, ok = revision.compiledTemplate("light")
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
	template, ok := revision.compiledTemplate("event")
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

	templateA, ok := revisionA.compiledTemplate("person")
	if !ok {
		t.Fatal("expected template from revision A")
	}
	templateB, ok := revisionB.compiledTemplate("person")
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

func TestBackchainReactiveTemplateGeneratesDemandTemplateMetadata(t *testing.T) {
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	if err := workspace.AddTemplate(TemplateSpec{
		Name:              "answer",
		Module:            "ask",
		Key:               "ask.answer:v1",
		CompatibilityKey:  "ask.answer:v1",
		BackchainReactive: true,
		Fields: []FieldSpec{
			{Name: "question", Kind: ValueString, Required: true},
			{Name: "value", Kind: ValueInt, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(answer): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	answer, ok := revision.compiledTemplate("answer")
	if !ok {
		t.Fatal("compiled revision missing answer template")
	}
	if !answer.BackchainReactive() {
		t.Fatal("answer should be backchain-reactive")
	}
	demandKey, ok := answer.BackchainDemandTemplateKey()
	if !ok || demandKey != "need-ask.answer:v1" {
		t.Fatalf("answer demand key = (%q, %t), want need-ask.answer:v1", demandKey, ok)
	}

	demand, ok := revision.compiledTemplate("need-answer")
	if !ok {
		t.Fatal("compiled revision missing need-answer template")
	}
	if demand.Module() != "ask" {
		t.Fatalf("demand module = %q, want ask", demand.Module())
	}
	if !demand.IsBackchainDemandTemplate() {
		t.Fatal("need-answer should inspect as a backchain demand template")
	}
	sourceKey, ok := demand.BackchainSourceTemplateKey()
	if !ok || sourceKey != answer.Key() {
		t.Fatalf("need-answer source key = (%q, %t), want %q", sourceKey, ok, answer.Key())
	}
	if demand.BackchainReactive() {
		t.Fatal("generated demand template should not itself be backchain-reactive")
	}
	if got := demand.Key(); got != demandKey {
		t.Fatalf("need-answer key = %q, want %q", got, demandKey)
	}

	fields := demand.Fields()
	if len(fields) != 2 {
		t.Fatalf("need-answer field count = %d, want 2", len(fields))
	}
	for _, field := range fields {
		if field.Kind != ValueAny || field.Required {
			t.Fatalf("demand field %q = kind %q required %t, want any optional", field.Name, field.Kind, field.Required)
		}
		if !field.HasDefault {
			t.Fatalf("demand field %q missing null default", field.Name)
		}
		defaultValue, ok := field.Default.(Value)
		if !ok || defaultValue.Kind() != ValueNull {
			t.Fatalf("demand field %q default = %#v, want null Value", field.Name, field.Default)
		}
	}

	byKey, ok := revision.TemplateByKey(demandKey)
	if !ok || byKey.Name() != "need-answer" {
		t.Fatalf("TemplateByKey(%q) = (%q, %t), want need-answer", demandKey, byKey.Name(), ok)
	}

	session := mustSession(t, revision, "backchain-demand-public-assert-session")
	_, err = session.Assert(context.Background(), demandKey, nil)
	if err == nil {
		t.Fatal("public Assert succeeded for engine-owned demand template")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(err, ErrValidation) = false for %v", err)
	}

	_, err = NewSession(revision, WithInitialFacts(SessionInitialFact{TemplateKey: demandKey}))
	if err == nil {
		t.Fatal("NewSession accepted engine-owned demand template as an initial fact")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(initial err, ErrValidation) = false for %v", err)
	}
}

func TestBackchainReactiveTemplateChangesRulesetID(t *testing.T) {
	base := mustCompile(t, TemplateSpec{Name: "answer"})
	explicitFalse := mustCompile(t, TemplateSpec{Name: "answer", BackchainReactive: false})
	reactive := mustCompile(t, TemplateSpec{Name: "answer", BackchainReactive: true})
	if base.ID() != explicitFalse.ID() {
		t.Fatalf("explicit false backchain metadata changed ruleset ID: %q vs %q", base.ID(), explicitFalse.ID())
	}
	if base.ID() == reactive.ID() {
		t.Fatalf("ruleset ID did not change after backchain metadata was enabled: %q", base.ID())
	}
}

func TestBackchainDemandTemplateCollisionValidation(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{Name: "answer", BackchainReactive: true}); err != nil {
		t.Fatalf("AddTemplate(answer): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{Name: "need-answer"}); err != nil {
		t.Fatalf("AddTemplate(need-answer): %v", err)
	}
	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile succeeded with colliding generated demand template")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(err, ErrValidation) = false for %v", err)
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("errors.As(err, *ValidationError) = false for %T", err)
	}
	if validation.TemplateName != "need-answer" {
		t.Fatalf("validation template = %q, want need-answer", validation.TemplateName)
	}

	workspace = NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{Name: "answer", Key: "answer:v1", BackchainReactive: true}); err != nil {
		t.Fatalf("AddTemplate(answer keyed): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{Name: "other", Key: "need-answer:v1"}); err != nil {
		t.Fatalf("AddTemplate(other): %v", err)
	}
	_, err = workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile succeeded with colliding generated demand template key")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(key collision err, ErrValidation) = false for %v", err)
	}
}

func TestBackchainReactiveRejectsSpecialConditionNames(t *testing.T) {
	for _, name := range []string{"and", "or", "not", "exists", "forall", "logical", "explicit", "accumulate", "test"} {
		t.Run(name, func(t *testing.T) {
			workspace := NewWorkspace()
			if err := workspace.AddTemplate(TemplateSpec{Name: name, BackchainReactive: true}); err != nil {
				t.Fatalf("AddTemplate(%s): %v", name, err)
			}
			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatal("Compile succeeded with backchain-reactive special condition name")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("errors.As(err, *ValidationError) = false for %T", err)
			}
			if validation.Reason != "cannot backchain on special condition" {
				t.Fatalf("validation reason = %q, want special condition rejection", validation.Reason)
			}
		})
	}
}

func TestBackchainReactiveRejectsDemandTemplateSourceNames(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{Name: "need-answer", BackchainReactive: true}); err != nil {
		t.Fatalf("AddTemplate(need-answer): %v", err)
	}
	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile succeeded with backchain-reactive demand template source")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("errors.As(err, *ValidationError) = false for %T", err)
	}
	if validation.Reason != "backchain-reactive template cannot be a demand template" {
		t.Fatalf("validation reason = %q, want demand source rejection", validation.Reason)
	}
}

func TestBackchainDemandTemplateRejectsPublicGeneratedFactActions(t *testing.T) {
	workspace := NewWorkspace()
	trigger := mustAddTemplate(t, workspace, TemplateSpec{Name: "trigger"})
	if err := workspace.AddTemplate(TemplateSpec{Name: "answer", BackchainReactive: true}); err != nil {
		t.Fatalf("AddTemplate(answer): %v", err)
	}
	revision := mustCompileWorkspace(t, workspace)
	answer, ok := revision.compiledTemplate("answer")
	if !ok {
		t.Fatal("compiled revision missing answer template")
	}
	demandKey, ok := answer.BackchainDemandTemplateKey()
	if !ok {
		t.Fatal("answer missing demand template key")
	}

	workspace = NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{Name: "trigger"}); err != nil {
		t.Fatalf("AddTemplate(trigger): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{Name: "answer", BackchainReactive: true}); err != nil {
		t.Fatalf("AddTemplate(answer): %v", err)
	}
	mustAddAction(t, workspace, ActionSpec{
		Name: "demand-via-context",
		Fn: func(ctx ActionContext) error {
			return ctx.AssertTemplateValues(demandKey, NullValue(), NullValue())
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "try-context-demand",
		Conditions: []RuleConditionSpec{{Binding: "trigger", Target: TemplateKeyFact(trigger.Key())}},
		Actions:    []RuleActionSpec{{Name: "demand-via-context"}},
	})
	revision = mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "backchain-demand-context-action-session")
	if _, err := session.Assert(context.Background(), trigger.Key(), nil); err != nil {
		t.Fatalf("Assert(trigger): %v", err)
	}
	if _, err := session.Run(context.Background()); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("Run context action error = %v, want ErrValidation", err)
	}

	workspace = NewWorkspace()
	trigger = mustAddTemplate(t, workspace, TemplateSpec{Name: "trigger"})
	if err := workspace.AddTemplate(TemplateSpec{Name: "answer", BackchainReactive: true}); err != nil {
		t.Fatalf("AddTemplate(answer): %v", err)
	}
	mustAddAction(t, workspace, ActionSpec{
		Name: "native-demand",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: demandKey,
			Values:      []ExpressionSpec{ConstExpr{Value: NullValue()}, ConstExpr{Value: NullValue()}},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "try-native-demand",
		Conditions: []RuleConditionSpec{{Binding: "trigger", Target: TemplateKeyFact(trigger.Key())}},
		Actions:    []RuleActionSpec{{Name: "native-demand"}},
	})
	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile succeeded with native action targeting engine-owned demand template")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(native action err, ErrValidation) = false for %v", err)
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
	template, ok := revision.compiledTemplate("payload")
	if !ok {
		t.Fatal("expected compiled payload template")
	}
	session := mustSession(t, revision, "template-immutability-session")
	result, err := session.insertFact("payload", template.Key(), mustFields(t, map[string]any{}))
	if err != nil {
		t.Fatalf("insert with canonical default: %v", err)
	}

	body, _ := result.Fact.Fields()["body"].AsMap()
	if got := valueInt64(body["count"]); got != 1 {
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
