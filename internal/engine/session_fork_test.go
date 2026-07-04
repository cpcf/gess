package engine

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestSessionForkPreservesPendingAgendaRefractionAndFactIDs(t *testing.T) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(t)
	parentEvents := &testEventCollector{}
	forkEvents := &testEventCollector{}
	parent, err := NewSession(revision, WithSessionID("fork-parent"), WithEventListener(parentEvents))
	if err != nil {
		t.Fatalf("NewSession parent: %v", err)
	}
	for _, id := range []int64{1, 2} {
		if _, err := parent.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": id})); err != nil {
			t.Fatalf("AssertTemplate(%d): %v", id, err)
		}
	}
	first, err := parent.Run(ctx, WithMaxFirings(1))
	if err != nil {
		t.Fatalf("first parent Run: %v", err)
	}
	if first.Status != RunFireLimit || first.Fired != 1 {
		t.Fatalf("first parent run = (%v, %d), want (%v, 1)", first.Status, first.Fired, RunFireLimit)
	}

	fork, err := parent.Fork(ctx, WithSessionID("fork-child"), WithEventListener(forkEvents))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	parentEventCount := len(parentEvents.Events())
	parentRun, err := parent.Run(ctx)
	if err != nil {
		t.Fatalf("second parent Run: %v", err)
	}
	forkRun, err := fork.Run(ctx)
	if err != nil {
		t.Fatalf("fork Run: %v", err)
	}
	if parentRun.Status != RunCompleted || parentRun.Fired != 1 {
		t.Fatalf("second parent run = (%v, %d), want (%v, 1)", parentRun.Status, parentRun.Fired, RunCompleted)
	}
	if forkRun.Status != RunCompleted || forkRun.Fired != 1 {
		t.Fatalf("fork run = (%v, %d), want (%v, 1)", forkRun.Status, forkRun.Fired, RunCompleted)
	}
	if got := len(parentEvents.Events()); got <= parentEventCount {
		t.Fatalf("parent events after parent run = %d, want more than %d", got, parentEventCount)
	}
	if got := len(forkEvents.Events()); got == 0 {
		t.Fatal("fork listener did not receive fork events")
	}

	parentNext, err := parent.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(3)}))
	if err != nil {
		t.Fatalf("parent tail assert: %v", err)
	}
	forkNext, err := fork.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(3)}))
	if err != nil {
		t.Fatalf("fork tail assert: %v", err)
	}
	if parentNext.Fact.ID() != forkNext.Fact.ID() {
		t.Fatalf("next fact IDs diverged: parent %s fork %s", parentNext.Fact.ID(), forkNext.Fact.ID())
	}
}

func TestSessionForkDoesNotInheritListeners(t *testing.T) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(t)
	parentEvents := &testEventCollector{}
	parent, err := NewSession(revision, WithSessionID("fork-listener-parent"), WithEventListener(parentEvents))
	if err != nil {
		t.Fatalf("NewSession parent: %v", err)
	}
	if _, err := parent.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(1)})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	before := len(parentEvents.Events())
	fork, err := parent.Fork(ctx, WithSessionID("fork-listener-child"))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if _, err := fork.Run(ctx); err != nil {
		t.Fatalf("fork Run: %v", err)
	}
	if got := len(parentEvents.Events()); got != before {
		t.Fatalf("parent listener saw fork events: before %d after %d", before, got)
	}
}

func TestSessionForkAssignsFreshDefaultIDAndAllowsOverride(t *testing.T) {
	ctx := context.Background()
	parent := mustSession(t, mustCompile(t), "fork-id-parent")
	fork, err := parent.Fork(ctx)
	if err != nil {
		t.Fatalf("Fork default ID: %v", err)
	}
	if fork.ID() == "" || fork.ID() == parent.ID() {
		t.Fatalf("default fork ID = %q, parent ID = %q; want fresh non-empty ID", fork.ID(), parent.ID())
	}
	override, err := parent.Fork(ctx, WithSessionID("fork-id-override"))
	if err != nil {
		t.Fatalf("Fork override ID: %v", err)
	}
	if got, want := override.ID(), SessionID("fork-id-override"); got != want {
		t.Fatalf("override fork ID = %q, want %q", got, want)
	}
}

func TestSessionForkDivergesIndependentlyAndDiagnosticsMatch(t *testing.T) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(t)
	parent := mustSession(t, revision, "fork-diverge-parent")
	if _, err := parent.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(1)})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	parentDiagnostics, err := parent.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("parent diagnostics: %v", err)
	}
	fork, err := parent.Fork(ctx, WithSessionID("fork-diverge-child"))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	forkDiagnostics, err := fork.RuntimeDiagnostics(ctx)
	if err != nil {
		t.Fatalf("fork diagnostics: %v", err)
	}
	if !reflect.DeepEqual(stableForkDiagnostics(parentDiagnostics), stableForkDiagnostics(forkDiagnostics)) {
		t.Fatalf("fork diagnostics differ from parent at fork:\nparent=%#v\nfork=%#v", parentDiagnostics, forkDiagnostics)
	}

	if _, err := fork.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(2)})); err != nil {
		t.Fatalf("fork AssertTemplate: %v", err)
	}
	if got, want := mustSnapshot(t, ctx, parent).Len(), 1; got != want {
		t.Fatalf("parent snapshot len after fork mutation = %d, want %d", got, want)
	}
	if _, err := parent.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(3)})); err != nil {
		t.Fatalf("parent AssertTemplate: %v", err)
	}
	if got, want := mustSnapshot(t, ctx, fork).Len(), 2; got != want {
		t.Fatalf("fork snapshot len after parent mutation = %d, want %d", got, want)
	}
}

func TestSessionForkSupportsConcurrentUse(t *testing.T) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(t)
	parent := mustSession(t, revision, "fork-concurrent-parent")
	for i := range 32 {
		if _, err := parent.AssertTemplate(ctx, taskKey, mustFields(t, map[string]any{"id": int64(i)})); err != nil {
			t.Fatalf("AssertTemplate(%d): %v", i, err)
		}
	}
	fork, err := parent.Fork(ctx, WithSessionID("fork-concurrent-child"))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, session := range []*Session{parent, fork} {
		wg.Add(1)
		go func(session *Session) {
			defer wg.Done()
			result, err := session.Run(ctx)
			if err != nil {
				errs <- err
				return
			}
			if result.Status != RunCompleted || result.Fired != 32 {
				errs <- &ValidationError{Reason: "unexpected fork concurrent run result"}
			}
		}(session)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent run: %v", err)
		}
	}
}

func TestSessionForkRebuildsBackchainDemandOwners(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	request, answer := mustBackchainDemandTemplates(t, workspace)
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "request-needs-answer",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "request", Target: TemplateKeyFact(request.Key())},
			Match{
				Binding: "answer",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "kind", Operator: FieldConstraintEqual, Value: "hardware"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "request", Field: "id"}},
				},
				Target: TemplateKeyFact(answer.Key()),
			},
		}},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	demandKey := mustDemandKey(t, revision, answer.Key())
	parent := mustSession(t, revision, "fork-backchain-parent")
	if _, err := parent.AssertTemplate(ctx, request.Key(), mustFields(t, map[string]any{"id": "q1"})); err != nil {
		t.Fatalf("AssertTemplate(request): %v", err)
	}
	if before := mustSnapshot(t, ctx, parent).BackchainDemandDiagnostics(); before.Active != 1 || before.Count(demandKey) != 1 {
		t.Fatalf("parent demand diagnostics before fork = %#v, want one active %q", before, demandKey)
	}
	fork, err := parent.Fork(ctx, WithSessionID("fork-backchain-child"))
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if before := mustSnapshot(t, ctx, fork).BackchainDemandDiagnostics(); before.Active != 1 || before.Count(demandKey) != 1 {
		t.Fatalf("fork demand diagnostics after fork = %#v, want one active %q", before, demandKey)
	}

	for label, session := range map[string]*Session{"parent": parent, "fork": fork} {
		if _, err := session.AssertTemplate(ctx, answer.Key(), mustFields(t, map[string]any{
			"id":    "q1",
			"kind":  "hardware",
			"value": "provided",
		})); err != nil {
			t.Fatalf("%s AssertTemplate(answer): %v", label, err)
		}
		after := mustSnapshot(t, ctx, session).BackchainDemandDiagnostics()
		if after.Active != 0 || after.Count(demandKey) != 0 {
			t.Fatalf("%s demand diagnostics after answer = %#v, want none", label, after)
		}
	}
}

func BenchmarkSessionForkVsRebuild(b *testing.B) {
	ctx := context.Background()
	revision, taskKey := mustForkTaskRevision(b)
	initials := make([]SessionInitialFact, 0, 512)
	for i := range 512 {
		initials = append(initials, SessionInitialFact{
			TemplateKey: taskKey,
			Fields:      mustFields(b, map[string]any{"id": int64(i)}),
		})
	}
	parent, err := NewSession(revision, WithSessionID("fork-benchmark-parent"), WithInitialFacts(initials...))
	if err != nil {
		b.Fatalf("NewSession parent: %v", err)
	}
	if _, err := parent.Run(ctx, WithMaxFirings(128)); err != nil {
		b.Fatalf("seed parent Run: %v", err)
	}

	b.Run("Fork", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			fork, err := parent.Fork(ctx, WithSessionID(SessionID("fork-benchmark-child")))
			if err != nil {
				b.Fatalf("Fork: %v", err)
			}
			if fork == nil {
				b.Fatal("nil fork")
			}
		}
	})
	b.Run("NewReassertRun", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			session, err := NewSession(revision, WithSessionID(SessionID("fork-benchmark-rebuild")))
			if err != nil {
				b.Fatalf("NewSession: %v", err)
			}
			for _, initial := range initials {
				if _, err := session.AssertTemplate(ctx, initial.TemplateKey, initial.Fields); err != nil {
					b.Fatalf("AssertTemplate: %v", err)
				}
			}
			if _, err := session.Run(ctx, WithMaxFirings(128)); err != nil {
				b.Fatalf("Run: %v", err)
			}
		}
	})
}

func mustForkTaskRevision(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	task := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            "task",
		Fields:          []FieldSpec{{Name: "id", Kind: ValueInt, Required: true}},
		DuplicatePolicy: DuplicateAllow,
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "task-rule",
		Conditions: []RuleConditionSpec{{Binding: "task", Target: TemplateKeyFact(task.Key())}},
		Actions:    []RuleActionSpec{{Name: "noop"}},
	})
	return mustCompileWorkspace(t, workspace), task.Key()
}

func stableForkDiagnostics(in RuntimeDiagnostics) RuntimeDiagnostics {
	out := RuntimeDiagnostics{MemoryOwners: make([]RuntimeMemoryOwnerDiagnostics, len(in.MemoryOwners))}
	for i, owner := range in.MemoryOwners {
		owner.Bytes = 0
		owner.HighWater = 0
		out.MemoryOwners[i] = owner
	}
	return out
}
