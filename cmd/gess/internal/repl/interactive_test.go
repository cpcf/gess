package repl

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestHistoryNavigatorPreservesDraft(t *testing.T) {
	history := newReplHistory(10)
	history.add("load rules.gess")
	history.add("facts")
	nav := newHistoryNavigator(history)

	if got, ok := nav.previous("run"); !ok || got != "facts" {
		t.Fatalf("previous = %q, %v; want facts, true", got, ok)
	}
	if got, ok := nav.previous("ignored"); !ok || got != "load rules.gess" {
		t.Fatalf("second previous = %q, %v; want load rules.gess, true", got, ok)
	}
	if got, ok := nav.next(); !ok || got != "facts" {
		t.Fatalf("next = %q, %v; want facts, true", got, ok)
	}
	if got, ok := nav.next(); !ok || got != "run" {
		t.Fatalf("next draft = %q, %v; want run, true", got, ok)
	}
}

func TestEchoInteractiveLineKeepsCommandInScrollback(t *testing.T) {
	var out bytes.Buffer
	echoInteractiveLine(&out, "gess> ", "help")
	if got, want := out.String(), "gess> help\n"; got != want {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

func TestPromptShowsDefaultHelpLine(t *testing.T) {
	model := newPromptModel(context.Background(), nil, newReplHistory(10), "gess> ")
	model.Init()
	got := model.View().Content
	if !strings.Contains(got, interactiveHelpLine) {
		t.Fatalf("view missing help line: %q", got)
	}
	if strings.Contains(got, "Press Ctrl-C again to exit") {
		t.Fatalf("view showed ctrl-c hint before ctrl-c: %q", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("view lines = %d, want 2: %q", len(lines), got)
	}
	if want := model.input.Styles().Focused.Prompt.Render(interactiveHelpLine); lines[1] != want {
		t.Fatalf("help line = %q, want prompt-styled %q", lines[1], want)
	}
}

func TestCtrlCEmptyLineArmsThenExits(t *testing.T) {
	model := newPromptModel(context.Background(), nil, newReplHistory(10), "gess> ")

	updated, cmd := model.Update(ctrlCKey())
	model = updated.(*promptModel)
	if cmd == nil {
		t.Fatal("first ctrl-c returned nil command; want expiry timer")
	}
	if model.exit || model.interrupted || !model.ctrlCExit {
		t.Fatalf("after first ctrl-c exit=%v interrupted=%v ctrlCExit=%v, want only ctrlCExit", model.exit, model.interrupted, model.ctrlCExit)
	}
	if got := model.View().Content; !strings.Contains(got, "Press Ctrl-C again to exit") {
		t.Fatalf("view missing ctrl-c hint: %q", got)
	}
	if got := model.View().Content; strings.Contains(got, interactiveHelpLine) {
		t.Fatalf("view showed default help while ctrl-c hint armed: %q", got)
	}

	updated, _ = model.Update(ctrlCKey())
	model = updated.(*promptModel)
	if !model.exit {
		t.Fatal("second ctrl-c did not request exit")
	}
}

func TestCtrlCEmptyLineWindowExpires(t *testing.T) {
	model := newPromptModel(context.Background(), nil, newReplHistory(10), "gess> ")

	updated, _ := model.Update(ctrlCKey())
	model = updated.(*promptModel)
	updated, _ = model.Update(ctrlCExitExpiredMsg{})
	model = updated.(*promptModel)

	if model.ctrlCExit {
		t.Fatal("ctrl-c exit window stayed armed after expiry")
	}
	if got := model.View().Content; strings.Contains(got, "Press Ctrl-C again to exit") {
		t.Fatalf("view still has ctrl-c hint after expiry: %q", got)
	}
	if got := model.View().Content; !strings.Contains(got, interactiveHelpLine) {
		t.Fatalf("view missing help line after expiry: %q", got)
	}
}

func TestCtrlCNonEmptyLineStillInterrupts(t *testing.T) {
	model := newPromptModel(context.Background(), nil, newReplHistory(10), "gess> ")
	model.input.SetValue("help")

	updated, _ := model.Update(ctrlCKey())
	model = updated.(*promptModel)

	if !model.interrupted || model.exit || model.ctrlCExit {
		t.Fatalf("non-empty ctrl-c interrupted=%v exit=%v ctrlCExit=%v, want interrupted only", model.interrupted, model.exit, model.ctrlCExit)
	}
}

func TestCompleteLineCommands(t *testing.T) {
	got := completeLine(context.Background(), nil, "lo")
	assertContainsCompletion(t, got, "load ")

	got = completeLine(context.Background(), nil, "watch on rule-")
	assertContainsCompletion(t, got, "watch on rule-fired")
}

func TestCompleteLineLoadedRulesetNames(t *testing.T) {
	state := &replState{out: &bytes.Buffer{}}
	if err := state.load(context.Background(), rootPath("examples/gess-files/order_routing/rules.gess")); err != nil {
		t.Fatalf("load ruleset: %v", err)
	}
	defer state.session.Close()

	assertContainsCompletion(t, completeLine(context.Background(), state, "facts f"), "facts fulfillment-route")
	assertContainsCompletion(t, completeLine(context.Background(), state, "rule route-"), "rule route-vip-order")
	assertContainsCompletion(t, completeLine(context.Background(), state, "query routes-by-lane l"), "query routes-by-lane lane=")
	assertContainsCompletion(t, completeLine(context.Background(), state, "assert order c"), "assert order customer=")
	assertContainsCompletion(t, completeLine(context.Background(), state, "focus M"), "focus MAIN")
	assertContainsCompletion(t, completeLine(context.Background(), state, "explain fact:g1:1"), "explain fact:g1:1 ")
}

func assertContainsCompletion(t *testing.T, got []string, want string) {
	t.Helper()
	if slices.Contains(got, want) {
		return
	}
	t.Fatalf("completion missing %q in %#v", want, got)
}

func ctrlCKey() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
}
