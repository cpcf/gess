package engine

import (
	"context"
	"errors"
	"testing"
)

func TestActionContextAssertLogicalCreatesSupportAndCascadesOnSourceRetract(t *testing.T) {
	collector := &testEventCollector{}
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session, err := NewSession(revision, WithSessionID("logical-cascade-session"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	source, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	run, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunCompleted || run.Fired != 2 {
		t.Fatalf("run result = (%v, %d), want (%v, 2)", run.Status, run.Fired, RunCompleted)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	derived := snapshot.FactsByName("derived")
	child := snapshot.FactsByName("child")
	if len(derived) != 1 || len(child) != 1 {
		t.Fatalf("derived/child facts = (%d, %d), want (1, 1)", len(derived), len(child))
	}
	if derived[0].Support().State != FactSupportLogical || child[0].Support().State != FactSupportLogical {
		t.Fatalf("support states = (%q, %q), want logical", derived[0].Support().State, child[0].Support().State)
	}
	graph := snapshot.SupportGraph()
	if len(graph.Edges) != 2 || graph.Counters.CurrentLogicalFacts != 2 || graph.Counters.CurrentSupportEdges != 2 {
		t.Fatalf("support graph = %#v, want two logical facts and two edges", graph)
	}

	if _, err := session.Retract(context.Background(), source.Fact.ID()); err != nil {
		t.Fatalf("Retract(source): %v", err)
	}
	after := mustSnapshot(t, context.Background(), session)
	if got := after.FactsByName("derived"); len(got) != 0 {
		t.Fatalf("derived facts after source retract = %d, want 0", len(got))
	}
	if got := after.FactsByName("child"); len(got) != 0 {
		t.Fatalf("child facts after source retract = %d, want 0", len(got))
	}
	graph = after.SupportGraph()
	if len(graph.Edges) != 0 || graph.Counters.LogicalFactsRetracted != 2 || graph.Counters.CascadeRetractions != 2 {
		t.Fatalf("support graph after cascade = %#v, want empty with two cascade retractions", graph)
	}

	var added, removed int
	for _, event := range collector.Events() {
		switch event.Type {
		case EventLogicalSupportAdded:
			added++
			if event.SupportEdge == nil || event.SupportEdge.ActivationID.IsZero() {
				t.Fatalf("support add event missing edge identity: %#v", event)
			}
		case EventLogicalSupportRemoved:
			removed++
		}
	}
	if added != 2 || removed != 2 {
		t.Fatalf("support event counts = added %d removed %d, want 2/2", added, removed)
	}
}

func TestLogicalSupportRetractCascadeUsesGraphDeltas(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session := mustSession(t, revision, "logical-retract-graph-delta-session")

	source, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	session.attachPropagationCounters()
	if _, err := session.Retract(context.Background(), source.Fact.ID()); err != nil {
		t.Fatalf("Retract(source): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if got := snapshot.FactsByName("derived"); len(got) != 0 {
		t.Fatalf("derived facts after source retract = %d, want 0", len(got))
	}
	if got := snapshot.FactsByName("child"); len(got) != 0 {
		t.Fatalf("child facts after source retract = %d, want 0", len(got))
	}
	assertLogicalSupportGraphDeltaCounters(t, session)
}

func TestLogicalSupportModifyCascadeUsesGraphDeltas(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, true)
	session := mustSession(t, revision, "logical-modify-graph-delta-session")

	source, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1", "group": "shared"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	derived := mustSnapshot(t, context.Background(), session).FactsByName("derived")
	if len(derived) != 1 {
		t.Fatalf("derived facts before modify = %d, want 1", len(derived))
	}

	session.attachPropagationCounters()
	if _, err := session.Modify(context.Background(), source.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"group": "changed"}),
	}); err != nil {
		t.Fatalf("Modify(source): %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if got := snapshot.FactsByName("derived"); len(got) != 0 {
		t.Fatalf("derived facts after source modify before run = %d, want 0", len(got))
	}
	assertLogicalSupportGraphDeltaCounters(t, session)
}

func TestLogicalSupportSourceIdentitySurvivesConsumedCompactActivation(t *testing.T) {
	ctx := context.Background()
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, true)
	session := mustSession(t, revision, "logical-compact-source-identity-session")

	if _, err := session.AssertTemplate(ctx, sourceKey, mustFields(t, map[string]any{
		"id":    "s-1",
		"group": "shared",
	})); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	edges := mustSnapshot(t, ctx, session).SupportGraph().Edges
	if got, want := len(edges), 1; got != want {
		t.Fatalf("support edges = %d, want %d", got, want)
	}
	rule, ok := revision.Rule("derive")
	if !ok {
		t.Fatal("derive rule missing")
	}
	stored := consumedActivationForRuleRevision(t, session.agenda, rule.RevisionID())
	if stored.payload != nil {
		t.Fatalf("stored consumed activation kept public fields: %#v", stored)
	}
	if stored.token.isZero() {
		t.Fatal("stored consumed activation lost token ref")
	}

	source := logicalSupportSourceFromActivation(*stored)
	if source.generation != edges[0].Generation || source.ruleRevisionID != edges[0].RuleRevisionID || source.identityKey != stored.identityKey {
		t.Fatalf("source key = %#v, edge = %#v", source, edges[0])
	}
	if edges[0].ActivationID.IsZero() {
		t.Fatalf("edge activation ID is zero: %#v", edges[0])
	}
	if got := logicalSupportID(source, edges[0].FactID); got != edges[0].SupportID {
		t.Fatalf("support ID from compact source = %q, want %q", got, edges[0].SupportID)
	}

	if _, err := session.removeLogicalSupportsForSources(ctx, []logicalSupportSourceKey{source}, mutationOrigin{}); err != nil {
		t.Fatalf("removeLogicalSupportsForSources: %v", err)
	}
	after := mustSnapshot(t, ctx, session)
	if got := after.SupportGraph().Edges; len(got) != 0 {
		t.Fatalf("support edges after compact-source removal = %#v, want none", got)
	}
	if got := after.FactsByName("derived"); len(got) != 0 {
		t.Fatalf("derived facts after compact-source removal = %#v, want none", got)
	}
}

func TestLogicalSupportDuplicateAssertionsShareFactUntilLastSupportRemoved(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, true)
	session := mustSession(t, revision, "logical-duplicate-session")

	first, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1", "group": "shared"}))
	if err != nil {
		t.Fatalf("AssertTemplate(first source): %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-2", "group": "shared"}))
	if err != nil {
		t.Fatalf("AssertTemplate(second source): %v", err)
	}
	run, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Fired != 2 {
		t.Fatalf("fired = %d, want 2", run.Fired)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	derived := snapshot.FactsByName("derived")
	if len(derived) != 1 {
		t.Fatalf("derived fact count = %d, want 1", len(derived))
	}
	if edges := snapshot.SupportGraph().Edges; len(edges) != 2 {
		t.Fatalf("support edges = %d, want 2: %#v", len(edges), edges)
	}

	if _, err := session.Retract(context.Background(), first.Fact.ID()); err != nil {
		t.Fatalf("Retract(first source): %v", err)
	}
	snapshot = mustSnapshot(t, context.Background(), session)
	if got := snapshot.FactsByName("derived"); len(got) != 1 || got[0].ID() != derived[0].ID() {
		t.Fatalf("derived after first retract = %#v, want original fact retained", got)
	}
	if edges := snapshot.SupportGraph().Edges; len(edges) != 1 {
		t.Fatalf("support edges after first retract = %d, want 1", len(edges))
	}

	if _, err := session.Retract(context.Background(), second.Fact.ID()); err != nil {
		t.Fatalf("Retract(second source): %v", err)
	}
	snapshot = mustSnapshot(t, context.Background(), session)
	if got := snapshot.FactsByName("derived"); len(got) != 0 {
		t.Fatalf("derived after last retract = %d, want 0", len(got))
	}
	if edges := snapshot.SupportGraph().Edges; len(edges) != 0 {
		t.Fatalf("support edges after last retract = %d, want 0", len(edges))
	}
}

func TestLogicalSupportStatedMergeAndMutationGuards(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, true)
	session := mustSession(t, revision, "logical-stated-merge-session")

	source, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1", "group": "shared"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	derived := mustSnapshot(t, context.Background(), session).FactsByName("derived")[0]

	if _, err := session.Modify(context.Background(), derived.ID(), FactPatch{Set: mustFields(t, map[string]any{"id": "changed"})}); !errors.Is(err, ErrLogicalFactModify) {
		t.Fatalf("Modify(logical) error = %v, want ErrLogicalFactModify", err)
	}
	if _, err := session.Retract(context.Background(), derived.ID()); !errors.Is(err, ErrLogicalOnlyRetract) {
		t.Fatalf("Retract(logical) error = %v, want ErrLogicalOnlyRetract", err)
	}

	stated, err := session.Assert(context.Background(), "derived", mustFields(t, map[string]any{"id": "shared"}))
	if err != nil {
		t.Fatalf("AssertTemplate(stated duplicate): %v", err)
	}
	if stated.Status != AssertExisting || stated.Fact.Support().State != FactSupportStatedAndLogical {
		t.Fatalf("stated merge result = %#v, want existing stated_and_logical", stated)
	}
	retracted, err := session.Retract(context.Background(), derived.ID())
	if err != nil {
		t.Fatalf("Retract(stated support): %v", err)
	}
	if retracted.Status != RetractStatedSupportRemoved || retracted.Fact.Support().State != FactSupportLogical {
		t.Fatalf("stated support retract = %#v, want logical fact retained", retracted)
	}

	if _, err := session.Assert(context.Background(), "derived", mustFields(t, map[string]any{"id": "shared"})); err != nil {
		t.Fatalf("Assert(stated duplicate again): %v", err)
	}
	if _, err := session.Retract(context.Background(), source.Fact.ID()); err != nil {
		t.Fatalf("Retract(source): %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	derivedFacts := snapshot.FactsByName("derived")
	if len(derivedFacts) != 1 || derivedFacts[0].Support().State != FactSupportStated {
		t.Fatalf("derived after logical support removed = %#v, want stated fact retained", derivedFacts)
	}
	if edges := snapshot.SupportGraph().Edges; len(edges) != 0 {
		t.Fatalf("support edges = %d, want 0", len(edges))
	}
}

func TestLogicalSupportClearsOnReset(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, true)
	session := mustSession(t, revision, "logical-reset-session")
	if _, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1", "group": "shared"})); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if edges := mustSnapshot(t, context.Background(), session).SupportGraph().Edges; len(edges) != 1 {
		t.Fatalf("support edges before reset = %d, want 1", len(edges))
	}
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	if snapshot.Len() != 0 {
		t.Fatalf("snapshot length after reset = %d, want 0", snapshot.Len())
	}
	graph := snapshot.SupportGraph()
	if len(graph.Edges) != 0 || graph.Generation != snapshot.Generation() {
		t.Fatalf("support graph after reset = %#v, want empty current-generation graph", graph)
	}
}

func TestLogicalSupportFromFailedFiringIsCleanedUp(t *testing.T) {
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "source",
		Fields:            []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
	})
	failErr := errors.New("stop")
	mustAddAction(t, workspace, ActionSpec{
		Name: "derive",
		Fn: func(ctx ActionContext) error {
			id, ok := ctx.BindingScalarValue("source", "id")
			if !ok {
				return ErrFactNotFound
			}
			_, err := ctx.AssertLogical("derived", Fields{"id": id})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "fail",
		Fn:   func(ActionContext) error { return failErr },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "derive-then-fail",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "derive"}, {Name: "fail"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "logical-failed-firing-session")
	if _, err := session.AssertTemplate(context.Background(), source.Key(), mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	run, err := session.Run(context.Background())
	if !errors.Is(err, ErrActionFailed) || run.Status != RunActionFailed {
		t.Fatalf("Run = (%#v, %v), want action failure", run, err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	if got := snapshot.FactsByName("derived"); len(got) != 0 {
		t.Fatalf("derived facts after failed firing = %d, want 0", len(got))
	}
	if edges := snapshot.SupportGraph().Edges; len(edges) != 0 {
		t.Fatalf("support edges after failed firing = %d, want 0", len(edges))
	}
}

func mustLogicalSupportRuleset(t testing.TB, duplicateOnly bool) (*Ruleset, TemplateKey, TemplateKey, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	sourceFields := []FieldSpec{
		{Name: "id", Kind: ValueString, Required: true},
	}
	if duplicateOnly {
		sourceFields = append(sourceFields, FieldSpec{Name: "group", Kind: ValueString, Required: true})
	}
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "source",
		Fields:            sourceFields,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
	})
	derived := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "derived",
		Fields:            []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
	})
	child := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "child",
		Fields:            []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
	})

	mustAddAction(t, workspace, ActionSpec{
		Name: "derive",
		Fn: func(ctx ActionContext) error {
			field := "id"
			if duplicateOnly {
				field = "group"
			}
			id, ok := ctx.BindingScalarValue("source", field)
			if !ok {
				return ErrFactNotFound
			}
			_, err := ctx.AssertLogical("derived", Fields{"id": id})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "derive-child",
		Fn: func(ctx ActionContext) error {
			derivedFact, ok := ctx.Binding("derived")
			if !ok {
				return ErrFactNotFound
			}
			id, ok := derivedFact.Field("id")
			if !ok {
				return ErrFactNotFound
			}
			_, err := ctx.AssertLogical("child", Fields{"id": id})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "derive",
		Conditions: []RuleConditionSpec{
			{Binding: "source", Target: TemplateKeyFact(source.Key())},
		},
		Actions: []RuleActionSpec{{Name: "derive"}},
	})
	if !duplicateOnly {
		mustAddRule(t, workspace, RuleSpec{
			Name:       "derive-child",
			Conditions: []RuleConditionSpec{{Binding: "derived", Target: DynamicFact("derived")}},
			Actions:    []RuleActionSpec{{Name: "derive-child"}},
		})
	}
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, source.Key(), derived.Key(), child.Key()
}

func consumedActivationForRuleRevision(t testing.TB, agenda *agenda, revisionID RuleRevisionID) *activation {
	t.Helper()
	if agenda == nil {
		t.Fatal("agenda is nil")
	}
	var found *activation
	agenda.forEachActivation(func(current *activation) bool {
		if current != nil && current.ruleRevisionID == revisionID && current.status == activationStatusConsumed {
			found = current
			return false
		}
		return true
	})
	if found != nil {
		return found
	}
	t.Fatalf("consumed activation for rule revision %q not found", revisionID)
	return nil
}

func assertLogicalSupportGraphDeltaCounters(t testing.TB, session *Session) {
	t.Helper()
	counters := session.propagationCounterSnapshot().Totals
	if got := counters.FullAgendaReconciles; got != 0 {
		t.Fatalf("full agenda reconciles = %d, want 0", got)
	}
	if got := counters.SteadyStateAgendaReconciles; got != 0 {
		t.Fatalf("steady-state agenda reconciles = %d, want 0", got)
	}
	if got := counters.UnsupportedAgendaDeltas; got != 0 {
		t.Fatalf("unsupported agenda deltas = %d, want 0", got)
	}
}
