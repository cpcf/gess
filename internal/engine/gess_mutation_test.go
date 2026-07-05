package engine

import (
	"bytes"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func factStringField(t *testing.T, fact FactSnapshot, field string) string {
	t.Helper()
	value, ok := fact.Field(field)
	if !ok {
		t.Fatalf("fact %v missing field %q", fact.ID(), field)
	}
	s, ok := value.AsString()
	if !ok {
		t.Fatalf("field %q is not a string: %v", field, value)
	}
	return s
}

func newGessSession(t *testing.T, source string, opts ...SessionOption) *Session {
	t.Helper()
	ctx := context.Background()
	doc, err := ParseGess("mutation.gess", []byte(source))
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	opts = append([]SessionOption{WithInitialFacts(doc.InitialFacts()...)}, opts...)
	session, err := NewSession(revision, opts...)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func TestGessDSLMutationVerbsLoaderErrors(t *testing.T) {
	ctx := context.Background()
	prelude := `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE))
  (slot note (type STRING)))
`
	cases := []struct {
		name    string
		rule    string
		wantErr string
	}{
		{
			name:    "retract unbound variable",
			rule:    "(defrule r ?order <- (order (id ?id)) => (retract ?missing))",
			wantErr: "not a bound fact",
		},
		{
			name:    "retract non-list arg",
			rule:    "(defrule r ?order <- (order (id ?id)) => (retract order))",
			wantErr: "retract target must be a ?binding",
		},
		{
			name:    "modify empty",
			rule:    "(defrule r ?order <- (order (id ?id)) => (modify ?order))",
			wantErr: "at least one set or unset block",
		},
		{
			name:    "modify bad block",
			rule:    "(defrule r ?order <- (order (id ?id)) => (modify ?order (replace (status \"x\"))))",
			wantErr: "modify block must be",
		},
		{
			name:    "modify bad set slot",
			rule:    "(defrule r ?order <- (order (id ?id)) => (modify ?order (set status)))",
			wantErr: "modify set slot must be (field value)",
		},
		{
			name:    "modify target is value not binding",
			rule:    "(defrule r ?order <- (order (id ?id) (status ?status)) => (modify ?status (set (note \"x\"))))",
			wantErr: "is a value, not a fact binding",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseGess("mutation.gess", []byte(prelude+tc.rule))
			if err != nil {
				t.Fatalf("ParseGess: %v", err)
			}
			err = LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{})
			if err == nil {
				t.Fatalf("LoadGess succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestGessDSLRetractRemovesFact(t *testing.T) {
	ctx := context.Background()
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot expired (type BOOL) (required TRUE)))

(deffacts seed
  (order (id "O-1") (expired TRUE))
  (order (id "O-2") (expired FALSE)))

(defrule drop-expired
  ?order <- (order (expired TRUE))
  =>
  (retract ?order))
`)
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Fired != 1 {
		t.Fatalf("fired = %d, want 1", run.Fired)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	orders := snap.FactsByName("order")
	if len(orders) != 1 {
		t.Fatalf("orders = %d, want 1", len(orders))
	}
	if got := factStringField(t, orders[0], "id"); got != "O-2" {
		t.Fatalf("remaining order id = %q, want O-2", got)
	}
}

func TestGessDSLModifyChangesSlotAndRepropagates(t *testing.T) {
	ctx := context.Background()
	// flag is matched by observe-flag, so an asserted flag is a real
	// working-memory fact rather than an output-only value. Its presence
	// proves the in-place modify re-propagated into flag-shipped.
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE)))

(deftemplate flag
  (slot id (type STRING) (required TRUE)))

(deftemplate observed
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (status "new")))

(defrule ship-new
  ?order <- (order (status "new"))
  =>
  (modify ?order (set (status "shipped"))))

(defrule flag-shipped
  (order (id ?id) (status "shipped"))
  =>
  (assert (flag (id ?id))))

(defrule observe-flag
  (flag (id ?id))
  =>
  (assert (observed (id ?id))))
`)
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	orders := snap.FactsByName("order")
	if len(orders) != 1 {
		t.Fatalf("orders = %d, want 1", len(orders))
	}
	if got := factStringField(t, orders[0], "status"); got != "shipped" {
		t.Fatalf("status = %q, want shipped", got)
	}
	if flags := snap.FactsByName("flag"); len(flags) != 1 {
		t.Fatalf("flags = %d, want 1 (modify should re-propagate to flag-shipped)", len(flags))
	}
}

func TestGessDSLModifyPreservesFactIdentity(t *testing.T) {
	ctx := context.Background()
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (status "new")))

(defrule ship-new
  ?order <- (order (status "new"))
  =>
  (modify ?order (set (status "shipped"))))
`)
	defer session.Close()
	before, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot before: %v", err)
	}
	beforeID := before.FactsByName("order")[0].ID()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	afterID := after.FactsByName("order")[0].ID()
	if beforeID != afterID {
		t.Fatalf("fact ID changed across modify: before=%v after=%v", beforeID, afterID)
	}
}

func TestGessDSLModifyRejectsLogicallySupportedFact(t *testing.T) {
	ctx := context.Background()
	// A purely logically-supported fact is entailed by its support and the
	// engine forbids modifying it in place. The .gess modify verb surfaces
	// that invariant as an action failure wrapping ErrLogicalFactModify.
	session := newGessSession(t, `
(deftemplate finding
  (slot id (type STRING) (required TRUE)))

(deftemplate ticket
  (slot id (type STRING) (required TRUE))
  (slot priority (type STRING) (required TRUE)))

(deffacts seed
  (finding (id "F-1")))

(defrule open-ticket
  (finding (id ?id))
  =>
  (assert-logical (ticket (id ?id) (priority "low"))))

(defrule escalate
  ?ticket <- (ticket (priority "low"))
  =>
  (modify ?ticket (set (priority "high"))))
`)
	defer session.Close()
	_, err := session.Run(ctx)
	if !errors.Is(err, ErrLogicalFactModify) {
		t.Fatalf("Run error = %v, want wrapping ErrLogicalFactModify", err)
	}
}

func TestGessDSLModifyDoesNotLivelock(t *testing.T) {
	ctx := context.Background()
	session := newGessSession(t, `
(deftemplate counter
  (slot id (type STRING) (required TRUE))
  (slot n (type INT) (required TRUE)))

(deffacts seed
  (counter (id "C-1") (n 0)))

(defrule bump
  ?counter <- (counter (n 0))
  =>
  (modify ?counter (set (n 1))))
`)
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v (self-modify should not livelock)", run.Status, RunCompleted)
	}
	if run.Fired != 1 {
		t.Fatalf("fired = %d, want 1", run.Fired)
	}
}

func TestGessDSLEmitWritesToOutputWriter(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot warehouse (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (warehouse "west")))

(defrule announce
  ?order <- (order (id ?id) (warehouse ?warehouse))
  =>
  (emit "order " ?id " routed to " ?warehouse))
`, WithOutputWriter(&out))
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := out.String(), "order O-1 routed to west"; got != want {
		t.Fatalf("emit output = %q, want %q", got, want)
	}
}

func TestGessDSLEmitWithoutWriterIsNoOp(t *testing.T) {
	ctx := context.Background()
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1")))

(defrule announce
  ?order <- (order (id ?id))
  =>
  (emit "order " ?id))
`)
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run with no output writer: %v", err)
	}
	if run.Fired != 1 {
		t.Fatalf("fired = %d, want 1", run.Fired)
	}
}

// TestGessDSLActionVerbParity exercises every RHS action verb through both the
// loader and the Go-codegen path so the two switches cannot drift: a verb added
// to one but not the other fails here.
func TestGessDSLActionVerbParity(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(defmodule OTHER)

(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE))
  (slot note (type STRING)))

(deftemplate copy
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (status "new")))

(defrule every-verb
  ?order <- (order (id ?id) (status "new"))
  =>
  (bind ?label ?id)
  (assert (copy (id ?id)))
  (assert-logical (copy (id ?id)))
  (emit "order " ?id " " ?label)
  (focus OTHER)
  (pop-focus)
  (clear-focus)
  (modify ?order (set (status "shipped")) (unset note))
  (retract ?order)
  (halt))
`)
	doc, err := ParseGess("parity.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	if err := LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{}); err != nil {
		t.Fatalf("loader rejected a verb: %v", err)
	}
	if _, err := GenerateGessGo(ctx, []GessSourceFile{{Name: "parity.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "rules",
		FunctionName: "BuildParity",
	}); err != nil {
		t.Fatalf("codegen rejected a verb: %v", err)
	}
}

func TestGenerateGessGoEmitsMutationVerbs(t *testing.T) {
	source := []byte(`
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE))
  (slot note (type STRING)))

(deffacts seed
  (order (id "O-1") (status "new")))

(defrule ship
  ?order <- (order (id ?id) (status "new"))
  =>
  (modify ?order (set (status "shipped")) (unset note))
  (emit "shipped " ?id)
  (retract ?order))
`)
	generated, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "orders.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "rules",
		FunctionName: "BuildOrders",
	})
	if err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "rules_generated.go", generated, parser.AllErrors); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, generated)
	}
	text := string(generated)
	for _, want := range []string{
		"gessrules.ActionEffectModify",
		"gessrules.ActionEffectRetract",
		"gessrules.ActionEffectEmit",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source missing %q:\n%s", want, text)
		}
	}
}
