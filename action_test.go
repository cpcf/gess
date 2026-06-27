package gess

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSessionExecuteActivationActionsUsesDetachedBindingSnapshots(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "mutate",
		Fn: func(ctx ActionContext) error {
			if _, ok := ctx.Binding(""); ok {
				return errors.New("empty binding should not resolve")
			}

			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			boundFacts := ctx.BoundFacts()
			if len(boundFacts) != 1 {
				return errors.New("expected one bound fact")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("unexpected initial binding value")
			}
			if got := boundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("unexpected initial bound fact value")
			}

			bindingFields := binding.Fields()
			bindingFields["name"] = mustValue(t, "MUT")
			if got, ok := ctx.Binding("person"); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed after mutating returned fields map")
			}

			boundFactFields := boundFacts[0].Fields()
			boundFactFields["name"] = mustValue(t, "MUT")
			if got := ctx.BoundFacts(); len(got) != 1 || !got[0].Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("bound facts changed after mutating returned fields map")
			}

			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: mustFields(t, map[string]any{"name": "Grace"}),
			})
			if err != nil {
				return err
			}

			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed after modify")
			}
			if got := boundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("bound fact changed after modify")
			}
			if got, ok := ctx.Binding("person"); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("later binding read changed after modify")
			}
			if got := ctx.BoundFacts(); len(got) != 1 || !got[0].Fields()["name"].Equal(mustValue(t, "Ada")) {
				return errors.New("later bound facts read changed after modify")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(mutate): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{
			{Name: "mutate"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("action-detached-binding-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(1), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	after := mustSnapshot(t, context.Background(), session)
	if got := after.Facts()[0].Fields()["name"]; !got.Equal(mustValue(t, "Grace")) {
		t.Fatalf("session fact name after modify = %v, want Grace", got)
	}
	if !inserted.Inserted() {
		t.Fatalf("initial assert status = %v, want inserted", inserted.Status)
	}
}

func TestActionContextLazilyMaterializesBindingSnapshots(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(noop): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-lazy-binding-session")
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	ctx, err := session.actionContextForActivation(context.Background(), selected)
	if err != nil {
		t.Fatalf("actionContextForActivation: %v", err)
	}
	if ctx.bindings == nil || ctx.bindings.len() != 1 {
		t.Fatalf("lazy binding count = %#v, want one entry", ctx.bindings)
	}
	if ctx.bindings.snapshots != nil {
		t.Fatal("action context materialized binding snapshots before they were read")
	}
	if got := ctx.ActivationID(); got != selected.id {
		t.Fatalf("activation ID = %q, want %q", got, selected.id)
	}
	if ctx.bindings.snapshots != nil {
		t.Fatal("metadata access materialized binding snapshots")
	}

	binding, ok := ctx.Binding("person")
	if !ok {
		t.Fatal("Binding(person) did not resolve")
	}
	if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("binding name = %v, want Ada", got)
	}
	if got, want := len(ctx.bindings.snapshots), 1; got != want {
		t.Fatalf("materialized snapshots = %d, want %d", got, want)
	}

	fields := binding.Fields()
	fields["name"] = mustValue(t, "MUT")
	if again, ok := ctx.Binding("person"); !ok || !again.Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("cached binding after returned field mutation = (%v, %v), want Ada", again, ok)
	}
	boundFacts := ctx.BoundFacts()
	if len(boundFacts) != 1 {
		t.Fatalf("bound facts = %d, want 1", len(boundFacts))
	}
	boundFields := boundFacts[0].Fields()
	boundFields["name"] = mustValue(t, "MUT")
	if got := ctx.BoundFacts(); len(got) != 1 || !got[0].Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("bound facts after returned field mutation = %#v, want Ada", got)
	}
}

func TestActionContextBindingScalarValueUsesDeclaredTemplateSlotsWithoutMaterializingSnapshots(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "nickname", Kind: ValueString},
			{Name: "profile", Kind: ValueMap},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	personTemplate, err := compileTemplateSpec(workspace.templates[len(workspace.templates)-1])
	if err != nil {
		t.Fatalf("compileTemplateSpec(person): %v", err)
	}
	personNameSlot, ok := personTemplate.fieldSlot("name")
	if !ok {
		t.Fatal("person template missing name slot")
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "inspect",
		Fn: func(ctx ActionContext) error {
			if ctx.bindings == nil {
				return errors.New("missing binding state")
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("snapshots materialized before scalar read")
			}

			value, ok := ctx.bindingScalarValue("person", "name")
			if !ok {
				return errors.New("missing person name")
			}
			if value.Kind() != ValueString || value.stringValue != "Ada" {
				return fmt.Errorf("person name = %v, want Ada", value)
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("scalar read materialized snapshots")
			}
			value, ok = ctx.bindingScalarValueAtSlot(0, personNameSlot)
			if !ok {
				return errors.New("missing person name by field slot")
			}
			if value.Kind() != ValueString || value.stringValue != "Ada" {
				return fmt.Errorf("person name by field slot = %v, want Ada", value)
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("slot scalar read materialized snapshots")
			}

			if _, ok := ctx.bindingScalarValue("person", "nickname"); ok {
				return errors.New("missing slot should not resolve")
			}
			if _, ok := ctx.bindingScalarValue("person", "profile"); ok {
				return errors.New("non-scalar field should not resolve")
			}
			if _, ok := ctx.bindingScalarValue("openPerson", "name"); ok {
				return errors.New("dynamic fact should not use scalar fast path")
			}
			if _, ok := ctx.Binding("openPerson"); !ok {
				return errors.New("missing dynamic fact binding")
			}
			if _, ok := ctx.BindingScalarValue("openPerson", "name"); ok {
				return errors.New("dynamic fact should not use scalar fast path after Binding materializes snapshots")
			}
			if _, ok := ctx.bindingScalarValue("missing", "name"); ok {
				return errors.New("missing binding should not resolve")
			}
			if _, ok := ctx.bindingScalarValue("", "name"); ok {
				return errors.New("empty binding should not resolve")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(inspect): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "inspect-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
			{Binding: "openPerson", Target: DynamicFact("openPerson")},
		},
		Actions: []RuleActionSpec{{Name: "inspect"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-scalar-fast-path-session")
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{
		"name":    "Ada",
		"profile": map[string]any{"likes": "jazz"},
	})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if _, err := session.Assert(context.Background(), "openPerson", mustFields(t, map[string]any{
		"name": "Grace",
	})); err != nil {
		t.Fatalf("Assert(openPerson): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(2), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}
}

func TestActionContextUsesTokenBackedBindingsForGraphActivations(t *testing.T) {
	workspace := NewWorkspace()

	mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "inspect",
		Fn: func(ctx ActionContext) error {
			if ctx.bindings == nil {
				return errors.New("missing binding state")
			}
			if ctx.bindings.token.isZero() {
				return errors.New("binding state is not token-backed")
			}
			if len(ctx.bindings.entries) != 0 {
				return fmt.Errorf("token-backed entries = %d, want 0 before materialization", len(ctx.bindings.entries))
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("snapshots materialized before scalar read")
			}
			value, ok := ctx.BindingScalarValue("person", "name")
			if !ok || value.Kind() != ValueString || value.stringValue != "Ada" {
				return fmt.Errorf("BindingScalarValue(person.name) = %v, ok %t, want Ada", value, ok)
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("scalar read materialized snapshots")
			}
			if len(ctx.bindings.entries) != 0 {
				return fmt.Errorf("scalar read populated entries = %d, want 0", len(ctx.bindings.entries))
			}
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("Binding(person) did not resolve")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return fmt.Errorf("Binding(person).name = %v, want Ada", got)
			}
			if len(ctx.bindings.entries) != 0 {
				return fmt.Errorf("Binding populated entries = %d, want 0", len(ctx.bindings.entries))
			}
			if got, want := len(ctx.bindings.snapshots), 1; got != want {
				return fmt.Errorf("snapshots = %d, want %d", got, want)
			}
			return nil
		},
		NonEscaping: true,
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "inspect"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "action-token-backed-session")
	if session.rete == nil || session.rete.graphBeta == nil {
		t.Fatalf("session graph beta = %#v, want token-backed graph runtime", session.rete)
	}
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestActionContextBindingScalarValueSurvivesAssertWithoutMaterializingSnapshots(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	auditTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	})

	if err := workspace.AddAction(ActionSpec{
		Name: "inspect",
		Fn: func(ctx ActionContext) error {
			value, ok := ctx.bindingScalarValueAt(0, "name")
			if !ok || value.Kind() != ValueString || value.stringValue != "Ada" {
				return fmt.Errorf("initial name = %v, ok %t, want Ada", value, ok)
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("scalar read materialized snapshots")
			}

			if _, err := ctx.AssertTemplate(auditTemplate.Key(), Fields{"name": value}); err != nil {
				return err
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("assert materialized snapshots")
			}

			value, ok = ctx.bindingScalarValueAt(0, "name")
			if !ok || value.Kind() != ValueString || value.stringValue != "Ada" {
				return fmt.Errorf("name after assert = %v, ok %t, want Ada", value, ok)
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("second scalar read materialized snapshots")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(inspect): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "inspect-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "inspect"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-scalar-assert-session")
	if _, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(3), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}
}

func TestActionContextBindingScalarValueRejectsStaleLiveFact(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "noop",
		Fn: func(ActionContext) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(noop): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-scalar-stale-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	actionCtx, err := session.actionContextForActivation(context.Background(), selected)
	if err != nil {
		t.Fatalf("actionContextForActivation: %v", err)
	}
	if value, ok := actionCtx.bindingScalarValueAt(0, "name"); !ok || value.stringValue != "Ada" {
		t.Fatalf("initial scalar value = %v, ok %t, want Ada", value, ok)
	}
	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if value, ok := actionCtx.bindingScalarValueAt(0, "name"); ok {
		t.Fatalf("stale scalar value = %v, ok true; want false", value)
	}
}

func TestActionContextBindingScalarValuePreservesFrozenSnapshotAfterMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(ctx ActionContext, id FactID) error
	}{
		{
			name: "modify",
			mutate: func(ctx ActionContext, id FactID) error {
				_, err := ctx.Modify(id, FactPatch{
					Set: mustFields(t, map[string]any{"name": "Grace"}),
				})
				return err
			},
		},
		{
			name: "retract",
			mutate: func(ctx ActionContext, id FactID) error {
				_, err := ctx.Retract(id)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workspace := NewWorkspace()
			var personID FactID
			if err := workspace.AddTemplate(TemplateSpec{
				Name: "person",
				Fields: []FieldSpec{
					{Name: "name", Kind: ValueString, Required: true},
				},
			}); err != nil {
				t.Fatalf("AddTemplate(person): %v", err)
			}

			if err := workspace.AddAction(ActionSpec{
				Name: "mutate",
				Fn: func(ctx ActionContext) error {
					if ctx.bindings == nil {
						return errors.New("missing binding state")
					}
					if ctx.bindings.snapshots != nil {
						return errors.New("snapshots materialized before scalar read")
					}

					value, ok := ctx.bindingScalarValue("person", "name")
					if !ok {
						return errors.New("missing person name")
					}
					if value.Kind() != ValueString || value.stringValue != "Ada" {
						return fmt.Errorf("person name = %v, want Ada", value)
					}
					if ctx.bindings.snapshots != nil {
						return errors.New("scalar read materialized snapshots")
					}

					if err := tc.mutate(ctx, personID); err != nil {
						return err
					}
					if ctx.bindings == nil || ctx.bindings.snapshots == nil {
						return errors.New("mutation should freeze binding snapshots")
					}

					value, ok = ctx.bindingScalarValue("person", "name")
					if !ok {
						return errors.New("missing frozen person name")
					}
					if value.Kind() != ValueString || value.stringValue != "Ada" {
						return fmt.Errorf("frozen person name = %v, want Ada", value)
					}
					value, ok = ctx.bindingScalarValueAt(0, "name")
					if !ok {
						return errors.New("missing frozen person name by binding slot")
					}
					if value.Kind() != ValueString || value.stringValue != "Ada" {
						return fmt.Errorf("frozen person name by binding slot = %v, want Ada", value)
					}
					return nil
				},
			}); err != nil {
				t.Fatalf("AddAction(mutate): %v", err)
			}
			if err := workspace.AddRule(RuleSpec{
				Name: "person-rule",
				Conditions: []RuleConditionSpec{
					{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
				},
				Actions: []RuleActionSpec{{Name: "mutate"}},
			}); err != nil {
				t.Fatalf("AddRule: %v", err)
			}

			revision, err := workspace.Compile(context.Background())
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			session := mustSession(t, revision, SessionID("action-scalar-mutation-session-"+tc.name))
			inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
			if err != nil {
				t.Fatalf("AssertTemplate(person): %v", err)
			}
			personID = inserted.Fact.ID()

			snapshot := mustSnapshot(t, context.Background(), session)
			if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
				t.Fatalf("reconcileAgenda: %v", err)
			}
			selected, ok := session.agenda.next()
			if !ok {
				t.Fatal("agenda.next returned no activation")
			}

			if err := session.executeActivationActions(context.Background(), RunID(4), selected); err != nil {
				t.Fatalf("executeActivationActions: %v", err)
			}

			after := mustSnapshot(t, context.Background(), session)
			switch tc.name {
			case "modify":
				fact, ok := after.Fact(personID)
				if !ok {
					t.Fatalf("snapshot missing person fact %q", personID)
				}
				if got := fact.Fields()["name"]; !got.Equal(mustValue(t, "Grace")) {
					t.Fatalf("person name after modify = %v, want Grace", got)
				}
			case "retract":
				if _, ok := after.Fact(personID); ok {
					t.Fatalf("snapshot still contains retracted fact %q", personID)
				}
			}
		})
	}
}

func TestSessionExecuteActivationActionsFreezesLazyBindingsBeforeMutation(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var personID FactID
	if err := workspace.AddAction(ActionSpec{
		Name: "mutate-before-read",
		Fn: func(ctx ActionContext) error {
			if ctx.bindings == nil {
				return errors.New("missing lazy binding state")
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("binding snapshots materialized before action read")
			}

			if _, err := ctx.Modify(personID, FactPatch{
				Set: mustFields(t, map[string]any{"name": "Grace"}),
			}); err != nil {
				return err
			}
			if got, want := len(ctx.bindings.snapshots), 1; got != want {
				return fmt.Errorf("snapshots after modify = %d, want %d", got, want)
			}

			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding after modify")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return fmt.Errorf("binding after modify = %v, want Ada", got)
			}
			boundFacts := ctx.BoundFacts()
			if len(boundFacts) != 1 {
				return fmt.Errorf("bound facts after modify = %d, want 1", len(boundFacts))
			}
			if got := boundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return fmt.Errorf("bound fact after modify = %v, want Ada", got)
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(mutate-before-read): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "mutate-before-read"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-lazy-freeze-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}
	personID = inserted.Fact.ID()

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(5), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	after := mustSnapshot(t, context.Background(), session)
	person, ok := after.Fact(personID)
	if !ok {
		t.Fatalf("snapshot missing person fact %q", personID)
	}
	if got := person.Fields()["name"]; !got.Equal(mustValue(t, "Grace")) {
		t.Fatalf("session fact name after action = %v, want Grace", got)
	}
}

func TestSessionExecuteActivationActionsFreezesEscapedUnreadContext(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var saved ActionContext
	if err := workspace.AddAction(ActionSpec{
		Name: "save-without-read",
		Fn: func(ctx ActionContext) error {
			if ctx.bindings == nil {
				return errors.New("missing lazy binding state")
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("binding snapshots materialized before action read")
			}
			saved = ctx
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(save-without-read): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "save-without-read"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-escaped-context-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(6), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}
	if saved.bindings == nil || len(saved.bindings.snapshots) != 1 {
		t.Fatalf("saved context snapshots = %#v, want one frozen snapshot", saved.bindings)
	}

	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}

	binding, ok := saved.Binding("person")
	if !ok {
		t.Fatal("saved Binding(person) did not resolve after later modify")
	}
	if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("saved binding after later modify = %v, want Ada", got)
	}
	boundFacts := saved.BoundFacts()
	if len(boundFacts) != 1 {
		t.Fatalf("saved bound facts = %d, want 1", len(boundFacts))
	}
	if got := boundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("saved bound fact after later modify = %v, want Ada", got)
	}
}

func TestSessionExecuteActivationActionsCanSkipFreezeForNonEscapingActions(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var saved ActionContext
	if err := workspace.AddAction(ActionSpec{
		Name:        "save-without-read",
		NonEscaping: true,
		Fn: func(ctx ActionContext) error {
			if ctx.bindings == nil {
				return errors.New("missing lazy binding state")
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("binding snapshots materialized before action read")
			}
			if value, ok := ctx.BindingScalarValue("person", "name"); !ok || value.Kind() != ValueString || value.stringValue != "Ada" {
				return fmt.Errorf("BindingScalarValue(person.name) = %v, ok %t, want Ada", value, ok)
			}
			saved = ctx
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(save-without-read): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "save-without-read"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-non-escaping-context-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(7), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}
	if saved.bindings == nil {
		t.Fatal("saved context missing binding state")
	}
	if saved.bindings.snapshots != nil {
		t.Fatalf("saved context snapshots = %#v, want nil for non-escaping action", saved.bindings.snapshots)
	}

	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if _, ok := saved.Binding("person"); ok {
		t.Fatal("saved Binding(person) resolved after skipped freeze and later modify")
	}
}

func TestSessionExecuteActivationActionsFreezesEscapedUnreadContextOnCancel(t *testing.T) {
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var (
		saved        ActionContext
		secondCalled bool
	)
	runCtx, cancel := context.WithCancel(context.Background())
	if err := workspace.AddAction(ActionSpec{
		Name: "save-cancel-without-read",
		Fn: func(ctx ActionContext) error {
			if ctx.bindings == nil {
				return errors.New("missing lazy binding state")
			}
			if ctx.bindings.snapshots != nil {
				return errors.New("binding snapshots materialized before action read")
			}
			saved = ctx
			cancel()
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(save-cancel-without-read): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "unexpected",
		Fn: func(ActionContext) error {
			secondCalled = true
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(unexpected): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "save-cancel-without-read"}, {Name: "unexpected"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-escaped-context-cancel-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	err = session.executeActivationActions(runCtx, RunID(8), selected)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("executeActivationActions error = %v, want context canceled", err)
	}
	if secondCalled {
		t.Fatal("second action ran after cancellation")
	}
	if saved.bindings == nil || len(saved.bindings.snapshots) != 1 {
		t.Fatalf("saved context snapshots after cancellation = %#v, want one frozen snapshot", saved.bindings)
	}

	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}

	binding, ok := saved.Binding("person")
	if !ok {
		t.Fatal("saved Binding(person) did not resolve after cancellation and later modify")
	}
	if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("saved binding after cancellation and later modify = %v, want Ada", got)
	}
}

func TestSessionExecuteActivationActionsKeepsBindingsStableAndRunsInOrder(t *testing.T) {
	collector := &testEventCollector{}
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}

	var (
		actionsSeen       []string
		sessionID         SessionID
		rulesetID         RulesetID
		activationID      ActivationID
		ruleID            RuleID
		ruleRevisionID    RuleRevisionID
		generation        Generation
		initialBinding    FactSnapshot
		initialBoundFacts []FactSnapshot
		laterBinding      FactSnapshot
		laterBoundFacts   []FactSnapshot
		storedContext     ActionContext
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "capture",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "capture")
			sessionID = ctx.SessionID()
			rulesetID = ctx.RulesetID()
			activationID = ctx.ActivationID()
			ruleID = ctx.RuleID()
			ruleRevisionID = ctx.RuleRevisionID()
			generation = ctx.Generation()

			boundFacts := ctx.BoundFacts()
			if len(boundFacts) != 1 {
				return errors.New("expected one bound fact")
			}
			initialBoundFacts = boundFacts
			storedContext = ctx

			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			initialBinding = binding
			if _, ok := ctx.Binding("missing"); ok {
				return errors.New("unexpected missing binding")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("unexpected initial binding value")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(capture): %v", err)
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "mutate",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "mutate")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed before modify")
			}

			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: mustFields(t, map[string]any{"name": "Grace"}),
			})
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(mutate): %v", err)
	}

	if err := workspace.AddAction(ActionSpec{
		Name: "verify",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "verify")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			laterBinding = binding
			laterBoundFacts = ctx.BoundFacts()
			if got := binding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("binding changed after modify")
			}
			if got := laterBoundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
				return errors.New("bound facts changed after modify")
			}
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(verify): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{
			{Name: "capture"},
			{Name: "mutate"},
			{Name: "verify"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("action-order-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	if err := session.executeActivationActions(context.Background(), RunID(9), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	if got, want := actionsSeen, []string{"capture", "mutate", "verify"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
	if sessionID != session.ID() {
		t.Fatalf("action context session ID = %q, want %q", sessionID, session.ID())
	}
	if rulesetID != revision.ID() {
		t.Fatalf("action context ruleset ID = %q, want %q", rulesetID, revision.ID())
	}
	if activationID != selected.id {
		t.Fatalf("action context activation ID = %q, want %q", activationID, selected.id)
	}
	if ruleID != selected.ruleID {
		t.Fatalf("action context rule ID = %q, want %q", ruleID, selected.ruleID)
	}
	if ruleRevisionID != selected.ruleRevisionID {
		t.Fatalf("action context rule revision ID = %q, want %q", ruleRevisionID, selected.ruleRevisionID)
	}
	if generation != selected.generation {
		t.Fatalf("action context generation = %d, want %d", generation, selected.generation)
	}
	if len(initialBoundFacts) != 1 || len(laterBoundFacts) != 1 {
		t.Fatalf("bound facts lengths = %d and %d, want 1 and 1", len(initialBoundFacts), len(laterBoundFacts))
	}
	if initialBinding.ID() != laterBinding.ID() {
		t.Fatalf("binding fact ID changed: %q vs %q", initialBinding.ID(), laterBinding.ID())
	}
	if got := initialBoundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("initial bound fact name = %v, want Ada", got)
	}
	if got := laterBoundFacts[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("later bound fact name = %v, want Ada", got)
	}
	if got := laterBinding.Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("later binding name = %v, want Ada", got)
	}
	if got, ok := storedContext.Binding("person"); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("stored context binding = (%v, %v), want Ada", got, ok)
	}
	if got := storedContext.BoundFacts(); len(got) != 1 || !got[0].Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("stored context bound facts = %#v, want Ada", got)
	}

	after := mustSnapshot(t, context.Background(), session)
	if got := after.Facts()[0].Fields()["name"]; !got.Equal(mustValue(t, "Grace")) {
		t.Fatalf("session fact name after modify = %v, want Grace", got)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
	if events[0].Type != EventFactAsserted {
		t.Fatalf("first event type = %q, want %q", events[0].Type, EventFactAsserted)
	}
	if events[0].Delta == nil || events[0].Delta.RuleID != "" || events[0].Delta.RuleRevisionID != "" || events[0].Delta.ActivationID != "" {
		t.Fatalf("external assert event carried origin metadata: %#v", events[0].Delta)
	}
	if events[1].Type != EventRuleActivated {
		t.Fatalf("second event type = %q, want %q", events[1].Type, EventRuleActivated)
	}
	if events[2].Type != EventFactModified {
		t.Fatalf("third event type = %q, want %q", events[2].Type, EventFactModified)
	}
	if events[3].Type != EventRuleActivated {
		t.Fatalf("fourth event type = %q, want %q", events[3].Type, EventRuleActivated)
	}
	if events[2].RuleID != selected.ruleID || events[2].RuleRevisionID != selected.ruleRevisionID || events[2].ActivationID != selected.id {
		t.Fatalf("modify event origin = %#v", events[2])
	}
	if events[2].Delta == nil {
		t.Fatal("modify event missing delta")
	}
	if events[2].Delta.RuleID != selected.ruleID || events[2].Delta.RuleRevisionID != selected.ruleRevisionID || events[2].Delta.ActivationID != selected.id {
		t.Fatalf("modify delta origin = %#v", events[2].Delta)
	}
	if !inserted.Inserted() {
		t.Fatalf("initial assert status = %v, want inserted", inserted.Status)
	}
}

func TestSessionExecuteActivationActionsAssertTemplateUsesSlotBackedInsertion(t *testing.T) {
	collector := &testEventCollector{}
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name:            "gate",
		Fields:          []FieldSpec{{Name: "id", Kind: ValueString}},
		DuplicatePolicy: DuplicateAllow,
	}); err != nil {
		t.Fatalf("AddTemplate(gate): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name:              "audit",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active", AllowedValues: []any{"active", "pending"}},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var (
		firstResult  AssertResult
		secondResult AssertResult
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "assert-audit",
		Fn: func(ctx ActionContext) error {
			result, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"id": "audit-1"}))
			firstResult = result
			if err != nil {
				return err
			}
			duplicate, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{
				"id":     "audit-1",
				"status": "active",
			}))
			secondResult = duplicate
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(assert-audit): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "noop",
		Fn:   func(ActionContext) error { return nil },
	}); err != nil {
		t.Fatalf("AddAction(noop): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "assert-audit"}, {Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule(person-rule): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "audit-gate-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", Target: TemplateKeyFact(TemplateKey("audit"))},
			{Binding: "gate", Target: TemplateKeyFact(TemplateKey("gate"))},
		},
		Actions: []RuleActionSpec{{Name: "noop"}},
	}); err != nil {
		t.Fatalf("AddRule(audit-gate-rule): %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("action-slot-assert-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}

	eventsBefore := len(collector.Events())
	if err := session.executeActivationActions(context.Background(), RunID(10), selected); err != nil {
		t.Fatalf("executeActivationActions: %v", err)
	}

	if got, want := len(collector.Events()), eventsBefore+1; got != want {
		t.Fatalf("events after action assert = %d, want %d", got, want)
	}
	createdEvent := collector.Events()[eventsBefore]
	if createdEvent.Type != EventFactAsserted {
		t.Fatalf("created event type = %v, want %v", createdEvent.Type, EventFactAsserted)
	}
	if createdEvent.Delta == nil || createdEvent.Delta.ActivationID != selected.id || createdEvent.Delta.RuleID != selected.ruleID || createdEvent.Delta.RuleRevisionID != selected.ruleRevisionID {
		t.Fatalf("created event delta origin = %#v", createdEvent.Delta)
	}

	if !firstResult.Inserted() {
		t.Fatalf("first assert result = %v, want inserted", firstResult.Status)
	}
	if firstResult.Delta == nil || firstResult.Delta.ActivationID != selected.id || firstResult.Delta.RuleID != selected.ruleID || firstResult.Delta.RuleRevisionID != selected.ruleRevisionID {
		t.Fatalf("first assert delta origin = %#v", firstResult.Delta)
	}
	if secondResult.Status != AssertExisting {
		t.Fatalf("duplicate assert status = %v, want %v", secondResult.Status, AssertExisting)
	}
	if secondResult.DuplicateKey != firstResult.DuplicateKey {
		t.Fatalf("duplicate key mismatch: %q != %q", secondResult.DuplicateKey, firstResult.DuplicateKey)
	}

	internal := mustWorkingFactByID(t, session, firstResult.Fact.ID())
	if internal.fields != nil {
		t.Fatal("slot-backed action fact should not retain canonical fields")
	}
	if got := len(internal.fieldSlots); got == 0 {
		t.Fatal("slot-backed action fact should have slot storage")
	}

	fields := firstResult.Fact.Fields()
	fields["status"] = mustValue(t, "mutated")
	if got, ok := firstResult.Fact.Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("action fact fields map was not defensive: (%v, %v)", got, ok)
	}
	if got, ok := firstResult.Fact.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("action fact status presence = (%v, %v), want default", got, ok)
	}
	if personFact.Fact.ID().IsZero() {
		t.Fatal("person fact should have been inserted")
	}
}

func TestActionContextAssertTemplateValuesUsesEffectPathAndLazyDuplicateKey(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "generated",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Default: "effect", HasDefault: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		Fn: func(ctx ActionContext) error {
			return ctx.AssertTemplateValues(generated.Key(), mustValue(t, 7))
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "effect-assert-values-session")
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 1)}); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}

	fact := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", 7)
	if got, want := fact.fieldSlots[1].value, mustValue(t, "effect"); !got.Equal(want) {
		t.Fatalf("generated default kind = %v, want %v", got, want)
	}
	internal := mustWorkingFactByID(t, session, fact.ID())
	if internal == nil {
		t.Fatalf("missing internal generated fact %q", fact.ID())
	}

	duplicate, err := session.AssertTemplate(ctx, generated.Key(), Fields{"id": mustValue(t, 7)})
	if err != nil {
		t.Fatalf("AssertTemplate(generated duplicate): %v", err)
	}
	if duplicate.Status != AssertExisting {
		t.Fatalf("duplicate status = %v, want %v", duplicate.Status, AssertExisting)
	}
	if duplicate.DuplicateKey == "" {
		t.Fatal("duplicate key was not materialized for public duplicate result")
	}
	if got := internal.publicDuplicateKey(session.revision); got != duplicate.DuplicateKey {
		t.Fatalf("public duplicate key = %q, want %q", got, duplicate.DuplicateKey)
	}
}

func TestNativeAssertTemplateValuesAction(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "generated",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"kind", "id"},
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "source", Field: "id"},
				ConstExpr{Value: "native"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate-native",
		Conditions: []RuleConditionSpec{{
			Binding: "source", Target: TemplateKeyFact(source.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	session := mustSession(t, mustCompileWorkspace(t, workspace), "native-assert-action-session")
	if _, err := session.AssertTemplate(context.Background(), source.Key(), mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	generatedFacts := snapshot.FactsByTemplateKey(generated.Key())
	if len(generatedFacts) != 1 {
		t.Fatalf("generated facts = %d, want 1", len(generatedFacts))
	}
	if got, ok := generatedFacts[0].Field("kind"); !ok || !got.Equal(mustValue(t, "native")) {
		t.Fatalf("generated kind = (%v, %t), want native", got, ok)
	}
	if got, ok := generatedFacts[0].Field("id"); !ok || !got.Equal(mustValue(t, "s-1")) {
		t.Fatalf("generated id = (%v, %t), want s-1", got, ok)
	}
}

func TestNativeAssertTemplateValuesActionEmitsListenerEventWithOrigin(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "generated",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "source", Field: "id"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate-native",
		Conditions: []RuleConditionSpec{{
			Binding: "source", Target: TemplateKeyFact(source.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	collector := &testEventCollector{}
	revision := mustCompileWorkspace(t, workspace)
	session, err := NewSession(revision, WithSessionID("native-assert-action-listener-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), source.Key(), mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}

	events := collector.Events()
	if got, want := len(events), 4; got != want {
		t.Fatalf("events = %d, want %d: %#v", got, want, events)
	}
	if events[0].Type != EventFactAsserted || events[1].Type != EventRuleActivated || events[2].Type != EventRuleFired || events[3].Type != EventFactAsserted {
		t.Fatalf("event types = %q, %q, %q, %q; want assert, activate, fired, assert", events[0].Type, events[1].Type, events[2].Type, events[3].Type)
	}
	generatedEvent := events[3]
	selected := events[1]
	if generatedEvent.RuleID != selected.RuleID || generatedEvent.RuleRevisionID != selected.RuleRevisionID || generatedEvent.ActivationID != selected.ActivationID {
		t.Fatalf("generated event origin = (%q, %q, %q), want (%q, %q, %q)", generatedEvent.RuleID, generatedEvent.RuleRevisionID, generatedEvent.ActivationID, selected.RuleID, selected.RuleRevisionID, selected.ActivationID)
	}
	if generatedEvent.Delta == nil {
		t.Fatal("generated assert event delta is nil")
	}
	if generatedEvent.Delta.RuleID != selected.RuleID || generatedEvent.Delta.RuleRevisionID != selected.RuleRevisionID || generatedEvent.Delta.ActivationID != selected.ActivationID {
		t.Fatalf("generated event delta origin = %#v, want selected activation", generatedEvent.Delta)
	}
	if generatedEvent.Delta.NewDuplicate == "" {
		t.Fatal("generated assert event duplicate key is empty")
	}
	if generatedEvent.Delta.After == nil {
		t.Fatal("generated assert event after snapshot is nil")
	}
	if got, ok := generatedEvent.Delta.After.Field("id"); !ok || !got.Equal(mustValue(t, "s-1")) {
		t.Fatalf("generated assert event after id = (%v, %t), want s-1", got, ok)
	}
}

func TestNativeAssertTemplateValuesActionUsesUntargetedTokenFastPath(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "generated",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddPureFunction(t, workspace, PureFunctionSpec{
		Name:   "prefix-fast",
		Args:   []ValueKind{ValueString, ValueString},
		Return: ValueString,
		Func2: func(_ context.Context, prefix Value, id Value) (Value, error) {
			return newStringValue(prefix.stringValue + id.stringValue), nil
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				Call("prefix-fast", ConstExpr{Value: "hit-"}, BindingFieldExpr{Binding: "source", Field: "id"}),
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate-native",
		Conditions: []RuleConditionSpec{{
			Binding: "source", Target: TemplateKeyFact(source.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	compiled := revision.rules["generate-native"].actionExecutions[0].assertTemplateValues
	if got, want := compiled.tokenValues[0].kind, compiledTokenActionValueStringCall2ConstBindingField; got != want {
		t.Fatalf("token action value kind = %v, want %v", got, want)
	}
	if compiled.insertPlan.affectsRuleMatches || compiled.insertPlan.affectsRete {
		t.Fatalf("untargeted generated insert plan affects rule=%v rete=%v, want false/false", compiled.insertPlan.affectsRuleMatches, compiled.insertPlan.affectsRete)
	}

	session := mustSession(t, revision, "native-assert-action-token-fast-path-session")
	if _, err := session.AssertTemplate(context.Background(), source.Key(), mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	session.attachPropagationCounters()
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}
	fact := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", "hit-s-1")
	if fact.ID().IsZero() {
		t.Fatal("generated fact was not inserted")
	}
	counters := session.propagationCounterSnapshot()
	if got := counters.Totals.RHSAsserts; got != 0 {
		t.Fatalf("RHS assert counter = %d, want 0 for untargeted generated fact", got)
	}
	if got := counters.ByTemplate[generated.Key()].Asserts; got != 0 {
		t.Fatalf("generated template assert counter = %d, want 0", got)
	}
}

func TestNativeAssertTemplateValuesActionUsesAggregateValueTokenFastPath(t *testing.T) {
	workspace := NewWorkspace()
	item := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	})
	summary := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "summary",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "total", Kind: ValueInt, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "summarize",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: summary.Key(),
			Values: []ExpressionSpec{
				BindingValueExpr{Binding: "total"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "summarize-items",
		ConditionTree: Accumulate(
			Match{Binding: "item", Target: TemplateKeyFact(item.Key())},
			Sum(BindingFieldExpr{Binding: "item", Field: "amount"}).As("total"),
		),
		Actions: []RuleActionSpec{{Name: "summarize"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	compiled := revision.rules["summarize-items"].actionExecutions[0].assertTemplateValues
	if got, want := compiled.tokenValues[0].kind, compiledTokenActionValueBindingValue; got != want {
		t.Fatalf("token action value kind = %v, want %v", got, want)
	}
	summaryPlan := revision.reteGraphDebugSummary()
	branch := findPlanInspectionBranch(t, summaryPlan.Plan.Branches, reteGraphBranchOwnerRule, "summarize-items", "")
	if got, want := len(branch.Projections), 1; got != want {
		t.Fatalf("rule projections = %d, want %d", got, want)
	}
	if got, want := branch.Projections[0].Kind, reteGraphTerminalProjectionActionValue; got != want {
		t.Fatalf("rule projection kind = %q, want %q", got, want)
	}

	session := mustSession(t, revision, "native-assert-action-aggregate-token-fast-path-session")
	if _, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"amount": 3})); err != nil {
		t.Fatalf("AssertTemplate(item 3): %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), item.Key(), mustFields(t, map[string]any{"amount": 4})); err != nil {
		t.Fatalf("AssertTemplate(item 4): %v", err)
	}
	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	summaries := snapshot.FactsByTemplateKey(summary.Key())
	if len(summaries) != 1 {
		t.Fatalf("summary facts = %d, want 1", len(summaries))
	}
	if got, ok := summaries[0].Field("total"); !ok || !got.Equal(mustValue(t, 7)) {
		t.Fatalf("summary total = (%v, %t), want 7", got, ok)
	}
}

func TestNativeAssertTemplateValuesActionRetainsStoredSlotBackingsAcrossFullFields(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "generated",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "source", Field: "id"},
				ConstExpr{Value: "native"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	session := mustSession(t, mustCompileWorkspace(t, workspace), "native-assert-fast-slot-session")
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 7)}); err != nil {
		t.Fatalf("AssertTemplate(source 7): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 8)}); err != nil {
		t.Fatalf("AssertTemplate(source 8): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}

	first := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", 7)
	second := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", 8)
	if got, want := first.fieldSlots[0].value, mustValue(t, 7); !got.Equal(want) {
		t.Fatalf("first generated id = %v, want %v", got, want)
	}
	if got, want := second.fieldSlots[0].value, mustValue(t, 8); !got.Equal(want) {
		t.Fatalf("second generated id = %v, want %v", got, want)
	}
	if got, want := first.fieldSlots[1].value, mustValue(t, "native"); !got.Equal(want) {
		t.Fatalf("first generated kind = %v, want %v", got, want)
	}
	if got, want := second.fieldSlots[1].value, mustValue(t, "native"); !got.Equal(want) {
		t.Fatalf("second generated kind = %v, want %v", got, want)
	}
}

func TestNativeAssertTemplateValuesActionPartialUsesDefaults(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "generated",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Default: "effect", HasDefault: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "source", Field: "id"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	session := mustSession(t, mustCompileWorkspace(t, workspace), "native-assert-partial-default-session")
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 7)}); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}

	generatedFact := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", 7)
	if got, want := generatedFact.fieldSlots[1].value, mustValue(t, "effect"); !got.Equal(want) {
		t.Fatalf("generated default kind = %v, want %v", got, want)
	}
	if got, want := generatedFact.fieldSlots[1].presence.fieldPresence(), FieldPresenceDefault; got != want {
		t.Fatalf("generated kind presence = %v, want %v", got, want)
	}
}

func TestNativeAssertTemplateValuesActionDuplicateRollsBackPreparedSlots(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "generated",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id", "kind"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Required: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: generated.Key(),
			Values: []ExpressionSpec{
				ConstExpr{Value: 7},
				ConstExpr{Value: "native"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	session := mustSession(t, mustCompileWorkspace(t, workspace), "native-assert-duplicate-slot-rollback-session")
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 1)}); err != nil {
		t.Fatalf("AssertTemplate(source 1): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 2)}); err != nil {
		t.Fatalf("AssertTemplate(source 2): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}

	session.ensureFactTargetIndexes()
	generatedIDs := session.factsByTemplate[generated.Key()]
	if got := len(generatedIDs); got != 1 {
		t.Fatalf("generated fact count = %d, want 1", got)
	}
	if got, want := len(session.slotStorage), len(generated.fields); got != want {
		t.Fatalf("generated slot storage length = %d, want %d", got, want)
	}
	if got, want := session.nextFactSequence, uint64(3); got != want {
		t.Fatalf("next fact sequence = %d, want %d", got, want)
	}
	if got, want := session.nextRecency, Recency(3); got != want {
		t.Fatalf("next recency = %d, want %d", got, want)
	}
}

func TestActionContextAssertTemplateValuesRetainsStoredSlotBackingsAcrossScratchReuse(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "generated",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Default: "effect", HasDefault: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		Fn: func(ctx ActionContext) error {
			if err := ctx.AssertTemplateValues(generated.Key(), mustValue(t, 7)); err != nil {
				return err
			}
			return ctx.AssertTemplateValues(generated.Key(), mustValue(t, 8))
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "effect-assert-scratch-session")
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 1)}); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 1 {
		t.Fatalf("run result = (%v, %d), want (%v, 1)", result.Status, result.Fired, RunCompleted)
	}

	first := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", 7)
	second := mustSessionFactByTemplateAndField(t, session, generated.Key(), "id", 8)
	if got, want := first.fieldSlots[0].value, mustValue(t, 7); !got.Equal(want) {
		t.Fatalf("first generated id = %v, want %v", got, want)
	}
	if got, want := second.fieldSlots[0].value, mustValue(t, 8); !got.Equal(want) {
		t.Fatalf("second generated id = %v, want %v", got, want)
	}
	if got, want := first.fieldSlots[1].value, mustValue(t, "effect"); !got.Equal(want) {
		t.Fatalf("first generated default kind = %v, want %v", got, want)
	}
	if got, want := second.fieldSlots[1].value, mustValue(t, "effect"); !got.Equal(want) {
		t.Fatalf("second generated default kind = %v, want %v", got, want)
	}
}

func TestActionContextAssertTemplateValuesDuplicateRollsBackPreparedSlots(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
		},
	})
	generated := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "generated",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "kind", Kind: ValueString, Default: "effect", HasDefault: true},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "generate",
		Fn: func(ctx ActionContext) error {
			return ctx.AssertTemplateValues(generated.Key(), mustValue(t, 7))
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "generate",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "generate"}},
	})

	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "effect-assert-duplicate-slot-rollback-session")
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 1)}); err != nil {
		t.Fatalf("AssertTemplate(source 1): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, source.Key(), Fields{"id": mustValue(t, 2)}); err != nil {
		t.Fatalf("AssertTemplate(source 2): %v", err)
	}
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted || result.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", result.Status, result.Fired, RunCompleted)
	}

	session.ensureFactTargetIndexes()
	generatedIDs := session.factsByTemplate[generated.Key()]
	if got := len(generatedIDs); got != 1 {
		t.Fatalf("generated fact count = %d, want 1", got)
	}
	if got, want := len(session.slotStorage), len(generated.fields); got != want {
		t.Fatalf("generated slot storage length = %d, want %d", got, want)
	}
	if got, want := session.nextFactSequence, uint64(3); got != want {
		t.Fatalf("next fact sequence = %d, want %d", got, want)
	}
	if got, want := session.nextRecency, Recency(3); got != want {
		t.Fatalf("next recency = %d, want %d", got, want)
	}
}

func TestSessionExecuteActivationActionsSupportsActionMutationsAndStopsOnError(t *testing.T) {
	collector := &testEventCollector{}
	workspace := NewWorkspace()

	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(audit): %v", err)
	}

	var (
		actionsSeen     []string
		assertResult    AssertResult
		templateResult  AssertResult
		retractResult   RetractResult
		terminalCalled  bool
		personBinding   FactSnapshot
		terminalErr     = errors.New("stop actions")
		selectionID     ActivationID
		selectionRuleID RuleID
		selectionRevID  RuleRevisionID
	)

	if err := workspace.AddAction(ActionSpec{
		Name: "assert-dynamic",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "assert-dynamic")
			result, err := ctx.Assert("note", mustFields(t, map[string]any{"kind": "dynamic"}))
			assertResult = result
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(assert-dynamic): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "assert-template",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "assert-template")
			result, err := ctx.AssertTemplate(TemplateKey("audit"), mustFields(t, map[string]any{"kind": "template"}))
			templateResult = result
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(assert-template): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "retract",
		Fn: func(ctx ActionContext) error {
			actionsSeen = append(actionsSeen, "retract")
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			personBinding = binding
			result, err := ctx.Retract(binding.ID())
			retractResult = result
			return err
		},
	}); err != nil {
		t.Fatalf("AddAction(retract): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "fail",
		Fn: func(ActionContext) error {
			actionsSeen = append(actionsSeen, "fail")
			return terminalErr
		},
	}); err != nil {
		t.Fatalf("AddAction(fail): %v", err)
	}
	if err := workspace.AddAction(ActionSpec{
		Name: "unexpected",
		Fn: func(ActionContext) error {
			terminalCalled = true
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(unexpected): %v", err)
	}

	if err := workspace.AddRule(RuleSpec{
		Name: "action-mutations",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{
			{Name: "assert-dynamic"},
			{Name: "assert-template"},
			{Name: "retract"},
			{Name: "fail"},
			{Name: "unexpected"},
		},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("action-mutation-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	personFact, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}
	selectionID = selected.id
	selectionRuleID = selected.ruleID
	selectionRevID = selected.ruleRevisionID

	err = session.executeActivationActions(context.Background(), RunID(11), selected)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("executeActivationActions error = %v, want %v", err, terminalErr)
	}
	var failure *ActionFailureError
	if !errors.As(err, &failure) {
		t.Fatalf("expected ActionFailureError, got %T: %v", err, err)
	}
	if failure.RunID != RunID(11) || failure.RuleID != selectionRuleID || failure.RuleRevisionID != selectionRevID || failure.ActivationID != selectionID || failure.ActionName != "fail" || failure.ActionIndex != 3 {
		t.Fatalf("action failure metadata = %#v", failure)
	}
	if terminalCalled {
		t.Fatal("later action was called after error")
	}

	if got, want := actionsSeen, []string{"assert-dynamic", "assert-template", "retract", "fail"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("action order = %#v, want %#v", got, want)
	}
	if assertResult.Status != AssertInserted || assertResult.Delta == nil {
		t.Fatalf("assert result = %#v", assertResult)
	}
	if templateResult.Status != AssertInserted || templateResult.Delta == nil {
		t.Fatalf("template assert result = %#v", templateResult)
	}
	if retractResult.Status != RetractRemoved || retractResult.Delta == nil {
		t.Fatalf("retract result = %#v", retractResult)
	}
	if assertResult.Delta.RuleID != selectionRuleID || assertResult.Delta.RuleRevisionID != selectionRevID || assertResult.Delta.ActivationID != selectionID {
		t.Fatalf("dynamic assert origin = %#v", assertResult.Delta)
	}
	if templateResult.Delta.RuleID != selectionRuleID || templateResult.Delta.RuleRevisionID != selectionRevID || templateResult.Delta.ActivationID != selectionID {
		t.Fatalf("template assert origin = %#v", templateResult.Delta)
	}
	if assertResult.Fact.Support().State != FactSupportStated || templateResult.Fact.Support().State != FactSupportStated {
		t.Fatalf("action assertion support states = (%q, %q), want stated", assertResult.Fact.Support().State, templateResult.Fact.Support().State)
	}
	if retractResult.Delta.RuleID != selectionRuleID || retractResult.Delta.RuleRevisionID != selectionRevID || retractResult.Delta.ActivationID != selectionID {
		t.Fatalf("retract origin = %#v", retractResult.Delta)
	}
	if personBinding.ID() != personFact.Fact.ID() {
		t.Fatalf("binding fact ID = %q, want %q", personBinding.ID(), personFact.Fact.ID())
	}

	events := collector.Events()
	if got, want := len(events), 5; got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
	if events[0].Type != EventFactAsserted {
		t.Fatalf("first event type = %q, want %q", events[0].Type, EventFactAsserted)
	}
	if events[0].Delta == nil || events[0].Delta.RuleID != "" || events[0].Delta.RuleRevisionID != "" || events[0].Delta.ActivationID != "" {
		t.Fatalf("external assert event carried origin metadata: %#v", events[0].Delta)
	}
	if events[1].Type != EventRuleActivated || events[2].Type != EventFactAsserted || events[3].Type != EventFactAsserted || events[4].Type != EventFactRetracted {
		t.Fatalf("event types = %#v", []EventType{events[1].Type, events[2].Type, events[3].Type, events[4].Type})
	}
	for i := 2; i < len(events); i++ {
		if events[i].RuleID != selectionRuleID || events[i].RuleRevisionID != selectionRevID || events[i].ActivationID != selectionID {
			t.Fatalf("event %d origin = %#v", i, events[i])
		}
		if events[i].Delta == nil {
			t.Fatalf("event %d missing delta", i)
		}
		if events[i].Delta.RuleID != selectionRuleID || events[i].Delta.RuleRevisionID != selectionRevID || events[i].Delta.ActivationID != selectionID {
			t.Fatalf("event %d delta origin = %#v", i, events[i].Delta)
		}
	}

	after := mustSnapshot(t, context.Background(), session)
	if after.Len() != 2 {
		t.Fatalf("snapshot length after action mutations = %d, want 2", after.Len())
	}
	if _, ok := after.Fact(personFact.Fact.ID()); ok {
		t.Fatalf("snapshot still contained retracted person fact %q", personFact.Fact.ID())
	}
	if got := after.FactsByName("note"); len(got) != 1 {
		t.Fatalf("snapshot note facts = %d, want 1", len(got))
	}
	if got := after.FactsByTemplateKey(TemplateKey("audit")); len(got) != 1 {
		t.Fatalf("snapshot audit facts = %d, want 1", len(got))
	}
}

func TestSessionExecuteActivationActionsRejectsStaleBindings(t *testing.T) {
	workspace := NewWorkspace()
	if err := workspace.AddTemplate(TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
		},
	}); err != nil {
		t.Fatalf("AddTemplate(person): %v", err)
	}
	called := false
	if err := workspace.AddAction(ActionSpec{
		Name: "mark",
		Fn: func(ActionContext) error {
			called = true
			return nil
		},
	}); err != nil {
		t.Fatalf("AddAction(mark): %v", err)
	}
	if err := workspace.AddRule(RuleSpec{
		Name: "person-rule",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(TemplateKey("person"))},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "action-stale-session")
	inserted, err := session.AssertTemplate(context.Background(), TemplateKey("person"), mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("AssertTemplate(person): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		t.Fatalf("reconcileAgenda: %v", err)
	}
	selected, ok := session.agenda.next()
	if !ok {
		t.Fatal("agenda.next returned no activation")
	}
	if _, err := session.Modify(context.Background(), inserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"name": "Grace"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}

	err = session.executeActivationActions(context.Background(), RunID(12), selected)
	if !errors.Is(err, ErrMatcher) {
		t.Fatalf("executeActivationActions error = %v, want ErrMatcher", err)
	}
	if called {
		t.Fatal("action ran for stale activation")
	}
}
