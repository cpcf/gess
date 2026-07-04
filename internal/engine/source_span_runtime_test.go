package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGessRuntimeActionFailureCarriesSource(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	source := []byte(`(deftemplate item
  (slot id (type STRING) (required TRUE)))

(defrule fail-action
  ?item <- (item (id ?id))
  =>
  (call fail))
`)
	revision, err := CompileGess(ctx, "runtime-errors.gess", source, DSLRegistry{
		Actions: map[string]ActionFunc{
			"fail": func(ActionContext) error { return boom },
		},
	})
	if err != nil {
		t.Fatalf("CompileGess: %v", err)
	}
	rule, ok := revision.Rule("fail-action")
	if !ok {
		t.Fatal("compiled rule not found")
	}
	if got := rule.Source(); got.Name != "runtime-errors.gess" || got.StartLine != 4 || got.StartColumn != 1 {
		t.Fatalf("rule source = %+v, want runtime-errors.gess:4:1", got)
	}

	collector := &testEventCollector{}
	session, err := NewSession(revision, WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if _, err := session.AssertTemplate(ctx, "item", MustFields("id", "I-1")); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	_, err = session.Run(ctx)
	if err == nil {
		t.Fatal("Run succeeded, want action failure")
	}
	var failure *ActionFailureError
	if !errors.As(err, &failure) {
		t.Fatalf("Run error = %T, want *ActionFailureError", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Run error does not wrap boom: %v", err)
	}
	if got := failure.RuleSource; got.Name != "runtime-errors.gess" || got.StartLine != 4 || got.StartColumn != 1 {
		t.Fatalf("rule source = %+v, want runtime-errors.gess:4:1", got)
	}
	if got := failure.ActionSource; got.Name != "runtime-errors.gess" || got.StartLine != 7 || got.StartColumn != 3 {
		t.Fatalf("action source = %+v, want runtime-errors.gess:7:3", got)
	}
	if got := failure.Source; got != failure.ActionSource {
		t.Fatalf("failure source = %+v, want action source %+v", got, failure.ActionSource)
	}
	if !strings.Contains(failure.Error(), "runtime-errors.gess:7:3") {
		t.Fatalf("failure error = %q, want source location", failure.Error())
	}

	var activated, fired, failed bool
	for _, event := range collector.Events() {
		switch event.Type {
		case EventRuleActivated:
			activated = true
			if got := event.Source; got.Name != "runtime-errors.gess" || got.StartLine != 4 {
				t.Fatalf("activated source = %+v, want rule source", got)
			}
		case EventRuleFired:
			fired = true
			if got := event.Source; got.Name != "runtime-errors.gess" || got.StartLine != 4 {
				t.Fatalf("fired source = %+v, want rule source", got)
			}
		case EventActionFailed:
			failed = true
			if got := event.Source; got.Name != "runtime-errors.gess" || got.StartLine != 7 {
				t.Fatalf("failed source = %+v, want action source", got)
			}
		}
	}
	if !activated || !fired || !failed {
		t.Fatalf("events activated=%t fired=%t failed=%t, want all true", activated, fired, failed)
	}
}

func TestGessRuntimeFunctionEvaluationCarriesConditionSource(t *testing.T) {
	ctx := context.Background()
	source := []byte(`(deftemplate item
  (slot id (type STRING) (required TRUE)))

(defrule bad-predicate
  ?item <- (item (id ?id))
  (test (explode ?id))
  =>
  (halt))
`)
	revision, err := CompileGess(ctx, "predicate-errors.gess", source, DSLRegistry{
		Functions: []PureFunctionSpec{{
			Name:   "explode",
			Args:   []ValueKind{ValueString},
			Return: ValueBool,
			Func1: func(context.Context, Value) (Value, error) {
				return Value{}, errors.New("predicate exploded")
			},
		}},
	})
	if err != nil {
		t.Fatalf("CompileGess: %v", err)
	}
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	_, err = session.AssertTemplate(ctx, "item", MustFields("id", "I-1"))
	if err == nil {
		t.Fatal("AssertTemplate succeeded, want function evaluation error")
	}
	var eval *FunctionEvaluationError
	if !errors.As(err, &eval) {
		t.Fatalf("AssertTemplate error = %T, want *FunctionEvaluationError", err)
	}
	if got := eval.Source; got.Name != "predicate-errors.gess" || got.StartLine != 6 || got.StartColumn != 3 {
		t.Fatalf("evaluation source = %+v, want predicate-errors.gess:6:3", got)
	}
	if !strings.Contains(eval.Error(), "predicate-errors.gess:6:3") {
		t.Fatalf("evaluation error = %q, want source location", eval.Error())
	}
}

func TestGessGoGeneratorEmitsSourceSpans(t *testing.T) {
	source := []byte(`(deftemplate item
  (slot id (type STRING)))

(defrule generated-source
  ?item <- (item (id ?id))
  =>
  (halt))
`)
	generated, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "generated-source.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "rules",
		FunctionName: "BuildGeneratedSource",
	})
	if err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}
	text := string(generated)
	for _, want := range []string{
		`gessrules.SourceSpan{Name: "generated-source.gess", StartLine: 1, StartColumn: 1`,
		`gessrules.SourceSpan{Name: "generated-source.gess", StartLine: 4, StartColumn: 1`,
		`gessrules.SourceSpan{Name: "generated-source.gess", StartLine: 7, StartColumn: 3`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source missing %q:\n%s", want, text)
		}
	}
}

func TestSourceSpansDoNotAffectRevisionIdentity(t *testing.T) {
	ctx := context.Background()
	first := []byte(`(deftemplate item
  (slot id (type STRING)))

(defrule stable
  ?item <- (item (id ?id))
  =>
  (halt))
`)
	second := []byte(`

(deftemplate item
  (slot id (type STRING)))


(defrule stable
  ?item <- (item (id ?id))
  =>
  (halt))
`)
	firstRevision, err := CompileGess(ctx, "stable.gess", first, DSLRegistry{})
	if err != nil {
		t.Fatalf("CompileGess first: %v", err)
	}
	secondRevision, err := CompileGess(ctx, "stable.gess", second, DSLRegistry{})
	if err != nil {
		t.Fatalf("CompileGess second: %v", err)
	}
	firstRule, _ := firstRevision.Rule("stable")
	secondRule, _ := secondRevision.Rule("stable")
	if firstRule.Source().StartLine == secondRule.Source().StartLine {
		t.Fatalf("rule sources did not differ: first=%+v second=%+v", firstRule.Source(), secondRule.Source())
	}
	if firstRule.RevisionID() != secondRule.RevisionID() {
		t.Fatalf("revision IDs differ for source-only change: %s != %s", firstRule.RevisionID(), secondRule.RevisionID())
	}
	if firstRevision.ID() != secondRevision.ID() {
		t.Fatalf("ruleset IDs differ for source-only change: %s != %s", firstRevision.ID(), secondRevision.ID())
	}
}
