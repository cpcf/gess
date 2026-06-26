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

func TestDefinitionsDefaultToMainModule(t *testing.T) {
	workspace := NewWorkspace()
	person := mustAddTemplate(t, workspace, TemplateSpec{Name: "person"})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "person-rule",
		Conditions: []RuleConditionSpec{{Binding: "person", Target: TemplateKeyFact(person.Key())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "people",
		ConditionTree: Match{Binding: "person", Target: TemplateKeyFact(person.Key())},
		Returns:       []QueryReturnSpec{ReturnFact("person", "person")},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)

	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("compiled revision missing person template")
	}
	if template.Module() != MainModule {
		t.Fatalf("template module = %q, want MAIN", template.Module())
	}
	rule, ok := revision.Rule("person-rule")
	if !ok {
		t.Fatal("compiled revision missing person-rule")
	}
	if rule.Module() != MainModule {
		t.Fatalf("rule module = %q, want MAIN", rule.Module())
	}
	query, ok := revision.Query("people")
	if !ok {
		t.Fatal("compiled revision missing people query")
	}
	if query.Module() != MainModule {
		t.Fatalf("query module = %q, want MAIN", query.Module())
	}
}

func TestWorkspaceRejectsUnknownDefinitionModules(t *testing.T) {
	tests := []struct {
		name       string
		build      func(*Workspace)
		wantReason string
		wantQuery  bool
	}{
		{
			name: "template",
			build: func(workspace *Workspace) {
				if err := workspace.AddTemplate(TemplateSpec{Name: "person", Module: "missing"}); err != nil {
					t.Fatalf("AddTemplate: %v", err)
				}
			},
			wantReason: `unknown module "missing" authored in module "missing"`,
		},
		{
			name: "rule",
			build: func(workspace *Workspace) {
				mustAddRule(t, workspace, RuleSpec{Name: "person-rule", Module: "missing"})
			},
			wantReason: `unknown module "missing" authored in module "missing"`,
		},
		{
			name: "query",
			build: func(workspace *Workspace) {
				if err := workspace.AddQuery(QuerySpec{Name: "people", Module: "missing"}); err != nil {
					t.Fatalf("AddQuery: %v", err)
				}
			},
			wantReason: `unknown module "missing" authored in module "missing"`,
			wantQuery:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := NewWorkspace()
			tt.build(workspace)

			_, err := workspace.Compile(context.Background())
			if err == nil {
				t.Fatal("Compile succeeded with an unknown definition module")
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("errors.Is(err, ErrValidation) = false for %v", err)
			}
			if tt.wantQuery && !errors.Is(err, ErrQueryValidation) {
				t.Fatalf("errors.Is(err, ErrQueryValidation) = false for %v", err)
			}

			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("errors.As(err, *ValidationError) = false for %T", err)
			}
			if validation.Reason != tt.wantReason {
				t.Fatalf("validation reason = %q, want %q", validation.Reason, tt.wantReason)
			}
		})
	}
}

func TestDeclaredModuleDefinitionsKeepCurrentMatchingBehavior(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddModule(ModuleSpec{Name: "ask"}); err != nil {
		t.Fatalf("AddModule: %v", err)
	}
	person := mustAddTemplate(t, workspace, TemplateSpec{Name: "person", Module: "ask"})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "person-rule",
		Module:     "ask",
		Conditions: []RuleConditionSpec{{Binding: "person", Target: TemplateKeyFact(person.Key())}},
		Actions:    []RuleActionSpec{{Name: "mark"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "people",
		Module:        "ask",
		ConditionTree: Match{Binding: "person", Target: TemplateKeyFact(person.Key())},
		Returns:       []QueryReturnSpec{ReturnFact("person", "person")},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)
	template, ok := revision.Template("person")
	if !ok || template.Module() != "ask" {
		t.Fatalf("template module = (%q, %t), want ask", template.Module(), ok)
	}
	rule, ok := revision.Rule("person-rule")
	if !ok || rule.Module() != "ask" {
		t.Fatalf("rule module = (%q, %t), want ask", rule.Module(), ok)
	}
	query, ok := revision.Query("people")
	if !ok || query.Module() != "ask" {
		t.Fatalf("query module = (%q, %t), want ask", query.Module(), ok)
	}

	session := mustSession(t, revision, "module-matching-session")
	if _, err := session.AssertTemplate(context.Background(), person.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("run fired = (%d result, %d action), want 1", result.Fired, fired)
	}
}

func TestModuleQualifiedTemplateTargetsResolveAtCompile(t *testing.T) {
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	mustAddModule(t, workspace, ModuleSpec{Name: "interview"})
	answer := mustAddTemplate(t, workspace, TemplateSpec{Name: "answer", Module: "ask"})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:   "mark-answer",
		Module: "interview",
		Conditions: []RuleConditionSpec{
			{Binding: "answer", Target: TemplateFactIn("ask", "answer")},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	rule, ok := revision.Rule("mark-answer")
	if !ok {
		t.Fatal("compiled revision missing mark-answer rule")
	}
	conditions := rule.Conditions()
	if len(conditions) != 1 {
		t.Fatalf("compiled conditions = %d, want 1", len(conditions))
	}
	if got, want := conditions[0].TemplateKey(), answer.Key(); got != want {
		t.Fatalf("condition template key = %q, want %q", got, want)
	}

	session := mustSession(t, revision, "qualified-template-target-session")
	if _, err := session.AssertTemplate(context.Background(), answer.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("run fired = (%d result, %d action), want 1", result.Fired, fired)
	}
}

func TestModuleRelativeTemplateTargetsUseAuthorModule(t *testing.T) {
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	answer := mustAddTemplate(t, workspace, TemplateSpec{Name: "answer", Module: "ask"})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			fired++
			return nil
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:   "mark-answer",
		Module: "ask",
		Conditions: []RuleConditionSpec{
			{Binding: "answer", Target: TemplateFact("answer")},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name:          "answers",
		Module:        "ask",
		ConditionTree: Match{Binding: "answer", Target: TemplateFact("answer")},
		Returns:       []QueryReturnSpec{ReturnFact("answer", "answer")},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)
	query, ok := revision.Query("answers")
	if !ok {
		t.Fatal("compiled revision missing answers query")
	}
	if got, want := query.Conditions()[0].TemplateKey(), answer.Key(); got != want {
		t.Fatalf("query condition template key = %q, want %q", got, want)
	}

	session := mustSession(t, revision, "relative-template-target-session")
	if _, err := session.AssertTemplate(context.Background(), answer.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || fired != 1 {
		t.Fatalf("run fired = (%d result, %d action), want 1", result.Fired, fired)
	}
}

func TestModuleQualifiedTemplateTargetDiagnosticsNameReferenceAndAuthor(t *testing.T) {
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "interview"})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:   "mark-answer",
		Module: "interview",
		Conditions: []RuleConditionSpec{
			{Binding: "answer", Target: TemplateFactIn("missing", "answer")},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	_, err := workspace.Compile(context.Background())
	if err == nil {
		t.Fatal("Compile succeeded with an unknown qualified template target")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if got, want := validation.Reason, `unknown template reference "missing.answer" authored in module "interview"`; got != want {
		t.Fatalf("validation reason = %q, want %q", got, want)
	}
}

func mustAddModule(t testing.TB, workspace *Workspace, spec ModuleSpec) {
	t.Helper()
	if err := workspace.AddModule(spec); err != nil {
		t.Fatalf("AddModule(%q): %v", spec.Name, err)
	}
}
