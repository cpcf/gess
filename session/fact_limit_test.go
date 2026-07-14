package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestWithMaxFactsBoundsWorkingMemory(t *testing.T) {
	ctx := context.Background()
	ruleset, initials := compileFactLimitRules(t, `
(deftemplate item
	(declare (duplicate-policy unique-key) (duplicate-key id))
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (item (id "one")))
`)
	sess, err := session.New(ruleset, session.WithInitialFacts(initials...), session.WithMaxFacts(1))
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer sess.Close()

	result, err := sess.Assert(ctx, "item", rules.Fields{"id": rules.StringValue("two")})
	if !errors.Is(err, session.ErrFactLimit) {
		t.Fatalf("Assert error = %v, want ErrFactLimit", err)
	}
	var limitErr *session.FactLimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != 1 || limitErr.Facts != 2 {
		t.Fatalf("Assert error = %#v, want limit 1 and facts 2", err)
	}
	if result.Status != session.AssertValidationFailure {
		t.Fatalf("Assert status = %v, want validation failure", result.Status)
	}

	snapshot, err := sess.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := len(snapshot.Facts()); got != 1 {
		t.Fatalf("fact count after rejected assert = %d, want 1", got)
	}

	existing, err := sess.Assert(ctx, "item", rules.Fields{"id": rules.StringValue("one")})
	if err != nil {
		t.Fatalf("duplicate Assert: %v", err)
	}
	if existing.Status != session.AssertExisting {
		t.Fatalf("duplicate Assert status = %v, want existing", existing.Status)
	}

	if _, err := sess.Retract(ctx, snapshot.Facts()[0].ID()); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if _, err := sess.Assert(ctx, "item", rules.Fields{"id": rules.StringValue("two")}); err != nil {
		t.Fatalf("Assert after retract: %v", err)
	}
	if _, err := sess.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := sess.Assert(ctx, "item", rules.Fields{"id": rules.StringValue("two")}); !errors.Is(err, session.ErrFactLimit) {
		t.Fatalf("Assert after Reset error = %v, want ErrFactLimit", err)
	}
}

func TestWithMaxFactsRejectsInitialAndRuleGeneratedOverflow(t *testing.T) {
	ctx := context.Background()
	ruleset, initials := compileFactLimitRules(t, `
(deftemplate seed
  (slot id (type STRING) (required TRUE)))

(deftemplate child
  (slot id (type STRING) (required TRUE)))

(deffacts seeds
  (seed (id "one"))
  (seed (id "two")))

(defrule create-child
  (seed (id "one"))
  =>
  (assert (child (id "generated"))))

(defquery children
  (child (id ?id))
  (return (id ?id)))
`)
	if _, err := session.New(ruleset, session.WithInitialFacts(initials...), session.WithMaxFacts(1)); !errors.Is(err, session.ErrFactLimit) {
		t.Fatalf("session.New error = %v, want ErrFactLimit", err)
	}

	sess, err := session.New(ruleset, session.WithInitialFacts(initials[:1]...), session.WithMaxFacts(1))
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer sess.Close()
	run, err := sess.Run(ctx)
	if !errors.Is(err, session.ErrFactLimit) {
		t.Fatalf("Run error = %v, want ErrFactLimit", err)
	}
	if run.Status != session.RunActionFailed {
		t.Fatalf("Run status = %v, want action failed", run.Status)
	}
	snapshot, snapshotErr := sess.Snapshot(ctx)
	if snapshotErr != nil {
		t.Fatalf("Snapshot: %v", snapshotErr)
	}
	if got := len(snapshot.Facts()); got != 1 {
		t.Fatalf("fact count after rejected action = %d, want 1", got)
	}
}

func TestWithMaxFactsCarriesAcrossFork(t *testing.T) {
	ctx := context.Background()
	ruleset, initials := compileFactLimitRules(t, `
(deftemplate item
	(declare (duplicate-policy unique-key) (duplicate-key id))
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (item (id "one")))
`)
	parent, err := session.New(ruleset, session.WithInitialFacts(initials...), session.WithMaxFacts(1))
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer parent.Close()
	child, err := parent.Fork(ctx)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer child.Close()
	if _, err := child.Assert(ctx, "item", rules.Fields{"id": rules.StringValue("two")}); !errors.Is(err, session.ErrFactLimit) {
		t.Fatalf("fork Assert error = %v, want ErrFactLimit", err)
	}
}

func compileFactLimitRules(t *testing.T, source string) (*rules.Ruleset, []session.InitialFact) {
	t.Helper()
	ctx := context.Background()
	doc, err := dsl.Parse("fact-limit.gess", []byte(source))
	if err != nil {
		t.Fatalf("dsl.Parse: %v", err)
	}
	workspace := session.NewWorkspace()
	if err := dsl.Load(ctx, workspace, doc, dsl.Registry{}); err != nil {
		t.Fatalf("dsl.Load: %v", err)
	}
	ruleset, err := rules.Compile(ctx, workspace)
	if err != nil {
		t.Fatalf("rules.Compile: %v", err)
	}
	return ruleset, dsl.InitialFacts(doc)
}
