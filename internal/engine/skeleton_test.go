package engine

import (
	"context"
	"errors"
	"slices"
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
		Name: "person",
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

	template, ok := revision.compiledTemplate("person")
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

func TestWorkspaceCompilesDeterministicTemplateIDs(t *testing.T) {
	workspace := NewWorkspace()
	mustAddTemplate(t, workspace, TemplateSpec{Name: "zeta", Fields: []FieldSpec{{Name: "id", Kind: ValueString}}})
	mustAddTemplate(t, workspace, TemplateSpec{Name: "alpha", Key: "alpha-key", Fields: []FieldSpec{{Name: "id", Kind: ValueString}}})

	revision := mustCompileWorkspace(t, workspace)
	alphaID, ok := revision.templateIDByKey("alpha-key")
	if !ok {
		t.Fatal("missing alpha template id by key")
	}
	zetaID, ok := revision.templateIDByName("zeta")
	if !ok {
		t.Fatal("missing zeta template id by name")
	}
	if alphaID == 0 || zetaID == 0 || alphaID == zetaID {
		t.Fatalf("template ids = alpha:%d zeta:%d, want distinct non-zero ids", alphaID, zetaID)
	}
	if alphaID >= zetaID {
		t.Fatalf("template ids = alpha:%d zeta:%d, want sorted name order", alphaID, zetaID)
	}
	template, ok := revision.templateByID(alphaID)
	if !ok || template.Key() != "alpha-key" || template.Name() != "alpha" {
		t.Fatalf("templateByID(alpha) = (%#v, %t), want alpha template", template, ok)
	}
	if _, ok := revision.templateByID(templateID(len(revision.templatesByID) + 1)); ok {
		t.Fatal("templateByID returned a template for an out-of-range id")
	}
}

func TestGeneratedFactInsertPlansCarryTemplateIDs(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "generated",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "record",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values:      []ExpressionSpec{ConstExpr{Value: "g1"}},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:          "generate",
		ConditionTree: Match{Binding: "source", Target: TemplateKeyFact(source.Key())},
		Actions:       []RuleActionSpec{{Name: "record"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	plan, ok := revision.generatedFactInsertPlan(generated.Key())
	if !ok {
		t.Fatal("missing generated fact insert plan")
	}
	templateID, ok := revision.templateIDByKey(generated.Key())
	if !ok {
		t.Fatal("missing generated template id")
	}
	if plan.templateID != templateID {
		t.Fatalf("plan template id = %d, want %d", plan.templateID, templateID)
	}
	rule := revision.rules["generate"]
	actionPlan := rule.actionExecutions[0].assertTemplateValues.insertPlan
	if actionPlan.templateID != templateID {
		t.Fatalf("rule action plan template id = %d, want %d", actionPlan.templateID, templateID)
	}
}

func TestGeneratedFactObservabilityDiagnosticsExplainCompilerProof(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	outputOnly := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "output",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	queryVisible := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "query-output",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	reactive := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "reactive-output",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	for _, spec := range []struct {
		name string
		key  TemplateKey
	}{
		{name: "emit-output", key: outputOnly.Key()},
		{name: "emit-query-output", key: queryVisible.Key()},
		{name: "emit-reactive-output", key: reactive.Key()},
	} {
		mustAddInternalAction(t, workspace, ActionSpec{
			Name: spec.name,
			AssertTemplateValues: &AssertTemplateValuesActionSpec{
				TemplateKey: spec.key,
				Values:      []ExpressionSpec{ConstExpr{Value: "g1"}},
			},
		})
	}
	mustAddRule(t, workspace, RuleSpec{
		Name:          "generate",
		ConditionTree: Match{Binding: "source", Target: TemplateKeyFact(source.Key())},
		Actions: []RuleActionSpec{
			{Name: "emit-output"},
			{Name: "emit-query-output"},
			{Name: "emit-reactive-output"},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:          "consume-reactive",
		ConditionTree: Match{Binding: "generated", Target: TemplateKeyFact(reactive.Key())},
		Actions:       []RuleActionSpec{{Name: "noop"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "query-generated",
		ConditionTree: Match{
			Binding: "generated", Target: TemplateKeyFact(queryVisible.Key()),
		},
		Returns: []QueryReturnSpec{
			ReturnValue("id", BindingFieldExpr{Binding: "generated", Field: "id"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery: %v", err)
	}

	revision := mustCompileWorkspace(t, workspace)
	outputDiag := mustGeneratedFactObservability(t, revision, outputOnly.Key())
	if outputDiag.Kind != GeneratedFactOutputOnly || outputDiag.RuleMatchVisible || outputDiag.QueryVisible {
		t.Fatalf("output diag = %#v, want output-only without rule/query visibility", outputDiag)
	}
	if !generatedFactDiagnosticHasReason(outputDiag, "no rule or query condition targets the generated template name or key") {
		t.Fatalf("output diag reasons = %#v, want output-only reason", outputDiag.DiagnosticReasons)
	}
	queryDiag := mustGeneratedFactObservability(t, revision, queryVisible.Key())
	if queryDiag.Kind != GeneratedFactQueryVisible || queryDiag.RuleMatchVisible || !queryDiag.QueryVisible {
		t.Fatalf("query diag = %#v, want query-visible only", queryDiag)
	}
	reactiveDiag := mustGeneratedFactObservability(t, revision, reactive.Key())
	if reactiveDiag.Kind != GeneratedFactReactiveWorkingMemory || !reactiveDiag.RuleMatchVisible {
		t.Fatalf("reactive diag = %#v, want reactive working-memory", reactiveDiag)
	}

	diagnostics := revision.GeneratedFactObservabilityDiagnostics()
	if len(diagnostics) == 0 {
		t.Fatal("GeneratedFactObservabilityDiagnostics returned no rows")
	}
	diagnostics[0].DiagnosticReasons[0] = "mutated"
	again := mustGeneratedFactObservability(t, revision, diagnostics[0].TemplateKey)
	if generatedFactDiagnosticHasReason(again, "mutated") {
		t.Fatalf("diagnostic reasons alias compiler state: %#v", again.DiagnosticReasons)
	}
}

func mustGeneratedFactObservability(t testing.TB, revision *Ruleset, key TemplateKey) GeneratedFactObservability {
	t.Helper()
	diagnostic, ok := revision.GeneratedFactObservability(key)
	if !ok {
		t.Fatalf("missing generated fact observability for %q", key)
	}
	return diagnostic
}

func generatedFactDiagnosticHasReason(diagnostic GeneratedFactObservability, reason string) bool {
	return slices.Contains(diagnostic.DiagnosticReasons, reason)
}

func TestWorkspaceCompilesImplicitMainModule(t *testing.T) {
	revision := mustCompile(t)

	module, ok := revision.Module(MainModule)
	if !ok {
		t.Fatal("compiled revision did not contain implicit MAIN module")
	}
	if module.Name() != MainModule {
		t.Fatalf("module name = %q, want %q", module.Name(), MainModule)
	}
	if module.Description() != "" {
		t.Fatalf("MAIN description = %q, want empty", module.Description())
	}
	if value, ok := module.AutoFocusDefault(); ok || value {
		t.Fatalf("MAIN auto-focus default = (%t, %t), want no default", value, ok)
	}

	modules := revision.Modules()
	if len(modules) != 1 || modules[0].Name() != MainModule {
		t.Fatalf("compiled modules = %#v, want only MAIN", modules)
	}

	modules[0].NameValue = "mutated-by-caller"
	modules = revision.Modules()
	if len(modules) != 1 || modules[0].Name() != MainModule {
		t.Fatalf("Ruleset.Modules leaked mutable module state: %#v", modules)
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

func mustSession(t testing.TB, revision *Ruleset, id SessionID) *Session {
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
