package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestStrategyBreadthOptimizerTransparency(t *testing.T) {
	ctx := context.Background()

	var recorder *chooseRecorder
	workspace, ruleName := newBreadthOptimizerTransparencyWorkspace(t, &recorder)

	baseline, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("baseline Compile: %v", err)
	}
	profile := &branchPlanningProfile{byRule: map[RuleID]map[string]float64{
		RuleID(ruleName): {"left": 10, "right": 10, "gate": 1},
	}}
	profiled, err := workspace.compileWithBranchPlanningProfile(ctx, profile)
	if err != nil {
		t.Fatalf("profiled Compile: %v", err)
	}

	if baseline.ID() != profiled.ID() {
		t.Fatalf("ruleset identity changed with profiled plan: baseline=%q profiled=%q", baseline.ID(), profiled.ID())
	}
	if baseline.rules[ruleName].revisionID != profiled.rules[ruleName].revisionID {
		t.Fatalf(
			"rule revision changed with profiled plan: baseline=%q profiled=%q",
			baseline.rules[ruleName].revisionID,
			profiled.rules[ruleName].revisionID,
		)
	}

	baselineInspection := mustBranchInspectionForRule(t, baseline, ruleName)
	profiledInspection := mustBranchInspectionForRule(t, profiled, ruleName)
	if got, want := bindingSlotsByName(baselineInspection.PlannedOrder), map[string]int{"left": 0, "right": 1, "gate": 2, "choice": -1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("baseline binding slots = %#v, want %#v", got, want)
	}
	if got, want := bindingSlotsByName(profiledInspection.PlannedOrder), map[string]int{"left": 0, "right": 1, "gate": 2, "choice": -1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profiled binding slots = %#v, want %#v", got, want)
	}
	if got, want := plannedBindings(baselineInspection.PlannedOrder), plannedBindings(profiledInspection.PlannedOrder); reflect.DeepEqual(got, want) {
		t.Fatalf("planned orders matched unexpectedly: baseline=%#v profiled=%#v", got, want)
	}

	witnessFacts := defaultBreadthTransparencySeedFacts()
	witness := breadthTransparencyWitness{
		facts:    witnessFacts,
		baseline: mustBreadthTransparencyPendingObservation(t, baseline, witnessFacts),
		profiled: mustBreadthTransparencyPendingObservation(t, profiled, witnessFacts),
	}
	baselinePending := witness.baseline
	profiledPending := witness.profiled
	if got, want := baselinePending.semanticIdentitySet, profiledPending.semanticIdentitySet; !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic pending identity set = %#v, want %#v", got, want)
	}
	if got, want := len(baselinePending.semanticIdentitySet), 4; got != want {
		t.Fatalf("baseline semantic pending activation count = %d, want %d", got, want)
	}
	if got, want := baselinePending.rawOrdinalTupleOrder, profiledPending.rawOrdinalTupleOrder; reflect.DeepEqual(got, want) {
		t.Fatalf("raw ordinal tuple orders matched unexpectedly: baseline=%#v profiled=%#v", got, want)
	}

	for _, strategy := range []struct {
		name  string
		value Strategy
	}{
		{name: "depth", value: StrategyDepth},
		{name: "breadth", value: StrategyBreadth},
	} {
		t.Run(strategy.name, func(t *testing.T) {
			baselineRun := mustRunBreadthTransparencyPlan(t, baseline, strategy.value, witness.facts, &recorder)
			profiledRun := mustRunBreadthTransparencyPlan(t, profiled, strategy.value, witness.facts, &recorder)

			if baselineRun.result.Status != RunCompleted || profiledRun.result.Status != RunCompleted {
				t.Fatalf("run status = (%v, %v), want (%v, %v)", baselineRun.result.Status, profiledRun.result.Status, RunCompleted, RunCompleted)
			}
			if baselineRun.result.Fired != 1 || profiledRun.result.Fired != 1 {
				t.Fatalf("fired = (%d, %d), want (1, 1)", baselineRun.result.Fired, profiledRun.result.Fired)
			}
			if got, want := baselineRun.chosen, profiledRun.chosen; !reflect.DeepEqual(got, want) {
				t.Fatalf("chosen authored-slot tuples = %#v, want %#v", got, want)
			}
			if strategy.value == StrategyBreadth {
				if got, want := baselineRun.chosen, []string{"l1|r1|g2"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("breadth chosen authored-slot tuples = %#v, want %#v", got, want)
				}
			}
			if got, want := baselineRun.firedRules, profiledRun.firedRules; !reflect.DeepEqual(got, want) {
				t.Fatalf("fired rule order = %#v, want %#v", got, want)
			}
			if baselineRun.finalFacts != profiledRun.finalFacts {
				t.Fatalf("final fact multiset differed:\nbaseline:\n%s\n\nprofiled:\n%s", baselineRun.finalFacts, profiledRun.finalFacts)
			}
		})
	}
}

type breadthTransparencyPendingObservation struct {
	rawOrdinalTupleOrder []string
	semanticIdentitySet  []string
}

type breadthTransparencyWitness struct {
	facts    []breadthTransparencySeedFact
	baseline breadthTransparencyPendingObservation
	profiled breadthTransparencyPendingObservation
}

type breadthTransparencySeedFact struct {
	template TemplateKey
	id       int64
	tag      string
}

type chooseRecorder struct {
	chosen []string
}

type breadthTransparencyRun struct {
	result     RunResult
	chosen     []string
	firedRules []string
	finalFacts string
}

func newBreadthOptimizerTransparencyWorkspace(t testing.TB, recorderSlot **chooseRecorder) (*Workspace, string) {
	t.Helper()

	workspace := NewWorkspace()
	left := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "left",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "tag", Kind: ValueString, Required: true},
		},
	})
	right := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "right",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "tag", Kind: ValueString, Required: true},
		},
	})
	gate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "gate",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "tag", Kind: ValueString, Required: true},
		},
	})
	choice := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "choice",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "left_tag", Kind: ValueString, Required: true},
			{Name: "right_tag", Kind: ValueString, Required: true},
			{Name: "gate_tag", Kind: ValueString, Required: true},
		},
	})

	const ruleName = "choose-first"
	mustAddAction(t, workspace, ActionSpec{
		Name: "choose",
		Fn: func(ctx ActionContext) error {
			internalCtx := actionContextForTest(t, ctx)
			leftValue, ok := internalCtx.bindingScalarValueAt(0, "id")
			if !ok {
				return fmt.Errorf("missing left.id at authored slot 0")
			}
			rightValue, ok := internalCtx.bindingScalarValueAt(1, "id")
			if !ok {
				return fmt.Errorf("missing right.id at authored slot 1")
			}
			leftTag, ok := internalCtx.bindingScalarValueAt(0, "tag")
			if !ok {
				return fmt.Errorf("missing left.tag at authored slot 0")
			}
			rightTag, ok := internalCtx.bindingScalarValueAt(1, "tag")
			if !ok {
				return fmt.Errorf("missing right.tag at authored slot 1")
			}
			gateTag, ok := internalCtx.bindingScalarValueAt(2, "tag")
			if !ok {
				return fmt.Errorf("missing gate.tag at authored slot 2")
			}
			leftID := valueInt64(leftValue)
			if got := valueInt64(rightValue); got != leftID {
				return fmt.Errorf("joined ids = (%d, %d), want equality", leftID, got)
			}
			if recorderSlot == nil || *recorderSlot == nil {
				return fmt.Errorf("recorder not installed")
			}
			chosenTuple := fmt.Sprintf("%s|%s|%s", valueString(leftTag), valueString(rightTag), valueString(gateTag))
			(*recorderSlot).chosen = append((*recorderSlot).chosen, chosenTuple)
			_, err := ctx.AssertLogical(choice.Key(), Fields{
				"id":        newIntValue(leftID),
				"left_tag":  leftTag,
				"right_tag": rightTag,
				"gate_tag":  gateTag,
			})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: ruleName,
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding: "left",
				Target:  TemplateKeyFact(left.Key()),
			},
			Match{
				Binding: "right",
				Target:  TemplateKeyFact(right.Key()),
				JoinConstraints: []JoinConstraintSpec{{
					Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "id"},
				}},
			},
			Match{
				Binding: "gate",
				Target:  TemplateKeyFact(gate.Key()),
				JoinConstraints: []JoinConstraintSpec{{
					Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "id"},
				}},
			},
			Not{Condition: Match{
				Binding: "choice",
				Target:  TemplateKeyFact(choice.Key()),
				JoinConstraints: []JoinConstraintSpec{{
					Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "left", Field: "id"},
				}},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "choose"}},
	})
	return workspace, ruleName
}

func mustBranchInspectionForRule(t testing.TB, revision *Ruleset, ruleName string) reteGraphBranchInspection {
	t.Helper()
	for _, inspection := range revision.graph.branchInspections {
		if inspection.RuleName == ruleName {
			return inspection
		}
	}
	t.Fatalf("branch inspection for %q not found", ruleName)
	return reteGraphBranchInspection{}
}

func bindingSlotsByName(conditions []reteGraphConditionOrderInspection) map[string]int {
	out := make(map[string]int, len(conditions))
	for _, condition := range conditions {
		if condition.Test {
			continue
		}
		out[conditionInspectionName(condition)] = condition.BindingSlot
	}
	return out
}

func plannedBindings(conditions []reteGraphConditionOrderInspection) []string {
	out := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if condition.Test {
			continue
		}
		out = append(out, conditionInspectionName(condition))
	}
	return out
}

func conditionInspectionName(condition reteGraphConditionOrderInspection) string {
	if condition.Binding != "" {
		return condition.Binding
	}
	return condition.Target.name
}

func mustBreadthTransparencyPendingObservation(t testing.TB, revision *Ruleset, facts []breadthTransparencySeedFact) breadthTransparencyPendingObservation {
	t.Helper()

	session, err := NewSession(revision, WithStrategy(StrategyBreadth))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	seedBreadthTransparencyFacts(t, ctx, session, facts)

	agenda, err := session.Agenda(ctx)
	if err != nil {
		t.Fatalf("Agenda: %v", err)
	}
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	pending := make([]activation, 0, agenda.Len())
	session.agendaDriver.agenda.forEachPendingActivation(func(current *activation) bool {
		pending = append(pending, session.agendaDriver.agenda.publicActivation(current))
		return true
	})
	slices.SortFunc(pending, func(left, right activation) int {
		switch {
		case left.key.ordinal < right.key.ordinal:
			return -1
		case left.key.ordinal > right.key.ordinal:
			return 1
		default:
			return 0
		}
	})

	rawOrdinalTupleOrder := make([]string, 0, len(pending))
	semanticIdentitySet := make([]string, 0, len(pending))
	for _, activation := range pending {
		tuple := authoredFactTupleForPendingActivation(t, snapshot, activation)
		rawOrdinalTupleOrder = append(rawOrdinalTupleOrder, fmt.Sprintf("%d|%s", activation.key.ordinal, tuple))
		semanticIdentitySet = append(semanticIdentitySet, fmt.Sprintf("%d:%d|%s", activation.identityKey.scopeHash, activation.identityKey.hash, tuple))
	}
	slices.Sort(semanticIdentitySet)
	return breadthTransparencyPendingObservation{
		rawOrdinalTupleOrder: rawOrdinalTupleOrder,
		semanticIdentitySet:  semanticIdentitySet,
	}
}

func mustRunBreadthTransparencyPlan(t testing.TB, revision *Ruleset, strategy Strategy, facts []breadthTransparencySeedFact, slot **chooseRecorder) breadthTransparencyRun {
	t.Helper()

	recorder := &chooseRecorder{}
	*slot = recorder

	firedRules := make([]string, 0, 1)
	listener := EventFunc(func(_ context.Context, event Event) error {
		if event.Type == EventRuleFired {
			firedRules = append(firedRules, event.RuleID.String())
		}
		return nil
	})
	session, err := NewSession(
		revision,
		WithStrategy(strategy),
		WithEventListener(listener, ForEventTypes(EventRuleFired)),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	seedBreadthTransparencyFacts(t, ctx, session, facts)

	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return breadthTransparencyRun{
		result:     result,
		chosen:     append([]string(nil), recorder.chosen...),
		firedRules: append([]string(nil), firedRules...),
		finalFacts: canonicalBreadthTransparencyFactMultiset(snapshot),
	}
}

func defaultBreadthTransparencySeedFacts() []breadthTransparencySeedFact {
	return []breadthTransparencySeedFact{
		{template: "right", id: 1, tag: "r1"},
		{template: "right", id: 1, tag: "r2"},
		{template: "gate", id: 1, tag: "g2"},
		{template: "gate", id: 1, tag: "g1"},
		{template: "left", id: 1, tag: "l1"},
	}
}

func seedBreadthTransparencyFacts(t testing.TB, ctx context.Context, session *Session, facts []breadthTransparencySeedFact) {
	t.Helper()
	for _, fact := range facts {
		if _, err := session.Assert(ctx, fact.template, Fields{"id": newIntValue(fact.id), "tag": newStringValue(fact.tag)}); err != nil {
			t.Fatalf("Assert(%s:%d): %v", fact.template, fact.id, err)
		}
	}
}

func authoredFactTupleForPendingActivation(t testing.TB, snapshot Snapshot, activation activation) string {
	t.Helper()
	entries := append([]bindingTupleEntry(nil), activation.bindings()...)
	slices.SortFunc(entries, func(left, right bindingTupleEntry) int {
		switch {
		case left.bindingSlot < right.bindingSlot:
			return -1
		case left.bindingSlot > right.bindingSlot:
			return 1
		default:
			return strings.Compare(left.binding, right.binding)
		}
	})
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.bindingSlot < 0 || entry.hasValue || entry.factID.IsZero() {
			continue
		}
		fact, ok := snapshot.Fact(entry.factID)
		if !ok {
			t.Fatalf("activation fact %s missing from snapshot", entry.factID)
		}
		id, ok := fact.Field("id")
		if !ok {
			t.Fatalf("fact %s missing id field", entry.factID)
		}
		tag := ""
		if value, ok := fact.Field("tag"); ok {
			tag = valueString(value)
		}
		parts = append(parts, fmt.Sprintf("%d:%s:%d:%s", entry.bindingSlot, fact.Name(), valueInt64(id), tag))
	}
	return strings.Join(parts, "|")
}

func canonicalBreadthTransparencyFactMultiset(snapshot Snapshot) string {
	facts := snapshot.Facts()
	encoded := make([]string, 0, len(facts))
	for _, fact := range facts {
		fields := fact.Fields()
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		slices.Sort(names)

		var out strings.Builder
		fmt.Fprintf(&out, "%s|%s", fact.TemplateKey(), fact.Name())
		for _, name := range names {
			fmt.Fprintf(&out, "|%s=%s", name, fields[name].String())
		}
		encoded = append(encoded, out.String())
	}
	slices.Sort(encoded)
	return strings.Join(encoded, "\n")
}
