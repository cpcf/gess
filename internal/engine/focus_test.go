package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSessionFocusStackAPIsValidateAndReset(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "focus-stack-session")

	if got := session.CurrentFocus(); got != MainModule {
		t.Fatalf("current focus = %q, want MAIN", got)
	}
	if got, want := session.FocusStack(), []ModuleName{MainModule}; !reflect.DeepEqual(got, want) {
		t.Fatalf("focus stack = %#v, want %#v", got, want)
	}
	if err := session.PushFocus(ctx, "missing"); !errors.Is(err, ErrValidation) {
		t.Fatalf("PushFocus missing error = %v, want ErrValidation", err)
	}
	if err := session.PushFocus(ctx, "ask"); err != nil {
		t.Fatalf("PushFocus ask: %v", err)
	}
	if err := session.PushFocus(ctx, "ask"); err != nil {
		t.Fatalf("duplicate PushFocus ask: %v", err)
	}
	if got, want := session.FocusStack(), []ModuleName{MainModule, "ask"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("focus stack after push = %#v, want %#v", got, want)
	}
	popped, err := session.PopFocus(ctx)
	if err != nil {
		t.Fatalf("PopFocus ask: %v", err)
	}
	if popped != "ask" {
		t.Fatalf("popped focus = %q, want ask", popped)
	}
	if err := session.ClearFocusStack(ctx); err != nil {
		t.Fatalf("ClearFocusStack: %v", err)
	}
	if got := session.CurrentFocus(); got != MainModule {
		t.Fatalf("current focus after clear = %q, want MAIN fallback", got)
	}
	if got := session.FocusStack(); len(got) != 0 {
		t.Fatalf("focus stack after clear = %#v, want empty", got)
	}
	if err := session.PushFocus(ctx, "ask"); err != nil {
		t.Fatalf("PushFocus before reset: %v", err)
	}
	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got, want := session.FocusStack(), []ModuleName{MainModule}; !reflect.DeepEqual(got, want) {
		t.Fatalf("focus stack after reset = %#v, want %#v", got, want)
	}
}

func TestFocusStateIsSessionLocal(t *testing.T) {
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	revision := mustCompileWorkspace(t, workspace)
	first := mustSession(t, revision, "first-focus-session")
	second := mustSession(t, revision, "second-focus-session")

	if err := first.PushFocus(context.Background(), "ask"); err != nil {
		t.Fatalf("PushFocus first: %v", err)
	}
	if got := first.CurrentFocus(); got != "ask" {
		t.Fatalf("first focus = %q, want ask", got)
	}
	if got := second.CurrentFocus(); got != MainModule {
		t.Fatalf("second focus = %q, want MAIN", got)
	}
}

func TestRunDrainsFocusedModuleBeforeMain(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	mainEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "main-event"})
	askEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "ask-event", Module: "ask"})
	trace := make([]string, 0, 2)
	mustAddAction(t, workspace, ActionSpec{Name: "main-fired", Fn: func(ActionContext) error {
		trace = append(trace, "main")
		return nil
	}})
	mustAddAction(t, workspace, ActionSpec{Name: "ask-fired", Fn: func(ActionContext) error {
		trace = append(trace, "ask")
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "main-rule",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(mainEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "main-fired"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "ask-rule",
		Module:     "ask",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(askEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "ask-fired"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "focused-run-session")
	if _, err := session.AssertTemplate(ctx, mainEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate main: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, askEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate ask: %v", err)
	}
	if err := session.PushFocus(ctx, "ask"); err != nil {
		t.Fatalf("PushFocus ask: %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("run fired = %d, want 2", result.Fired)
	}
	if want := []string{"ask", "main"}; !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %#v, want %#v", trace, want)
	}
}

func TestRunLeavesUnfocusedNonMainActivationsPending(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	askEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "ask-event", Module: "ask"})
	fired := 0
	mustAddAction(t, workspace, ActionSpec{Name: "ask-fired", Fn: func(ActionContext) error {
		fired++
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "ask-rule",
		Module:     "ask",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(askEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "ask-fired"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "unfocused-run-session")
	if _, err := session.AssertTemplate(ctx, askEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate ask: %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 0 || fired != 0 {
		t.Fatalf("run fired = (%d result, %d action), want 0", result.Fired, fired)
	}
	if got := len(session.agenda.pendingActivations()); got != 1 {
		t.Fatalf("pending activations = %d, want 1", got)
	}
}

func TestAutoFocusPushesModuleWhenActivationEntersAgenda(t *testing.T) {
	ctx := context.Background()
	autoFocus := true
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask", AutoFocus: &autoFocus})
	mainEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "main-event"})
	askEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "ask-event", Module: "ask"})
	trace := make([]string, 0, 2)
	mustAddAction(t, workspace, ActionSpec{Name: "main-fired", Fn: func(ActionContext) error {
		trace = append(trace, "main")
		return nil
	}})
	mustAddAction(t, workspace, ActionSpec{Name: "ask-fired", Fn: func(ActionContext) error {
		trace = append(trace, "ask")
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "main-rule",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(mainEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "main-fired"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "ask-rule",
		Module:     "ask",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(askEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "ask-fired"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "auto-focus-run-session")
	if _, err := session.AssertTemplate(ctx, mainEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate main: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, askEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate ask: %v", err)
	}
	if got := session.CurrentFocus(); got != "ask" {
		t.Fatalf("current focus after auto-focus activation = %q, want ask", got)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("run fired = %d, want 2", result.Fired)
	}
	if want := []string{"ask", "main"}; !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %#v, want %#v", trace, want)
	}
}

func TestActionContextPushFocusAffectsNextActivation(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	mustAddModule(t, workspace, ModuleSpec{Name: "ask"})
	startEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "start"})
	askEvent := mustAddTemplate(t, workspace, TemplateSpec{Name: "ask-event", Module: "ask"})
	trace := make([]string, 0, 2)
	mustAddAction(t, workspace, ActionSpec{Name: "push-ask", Fn: func(actionCtx ActionContext) error {
		trace = append(trace, "main")
		return actionCtx.PushFocus("ask")
	}})
	mustAddAction(t, workspace, ActionSpec{Name: "ask-fired", Fn: func(ActionContext) error {
		trace = append(trace, "ask")
		return nil
	}})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "start-rule",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(startEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "push-ask"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "ask-rule",
		Module:     "ask",
		Conditions: []RuleConditionSpec{{Binding: "event", Target: TemplateKeyFact(askEvent.Key())}},
		Actions:    []RuleActionSpec{{Name: "ask-fired"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "action-focus-session")
	if _, err := session.AssertTemplate(ctx, startEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate start: %v", err)
	}
	if _, err := session.AssertTemplate(ctx, askEvent.Key(), nil); err != nil {
		t.Fatalf("AssertTemplate ask: %v", err)
	}

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 2 {
		t.Fatalf("run fired = %d, want 2", result.Fired)
	}
	if want := []string{"main", "ask"}; !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %#v, want %#v", trace, want)
	}
}
