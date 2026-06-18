package gess

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestNaiveMatcherProducesCanonicalPerRuleCandidates(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueAny, Required: true},
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Name: "person"},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "age-pairs",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: personTemplate.Key()},
			{
				Binding:     "candidate",
				TemplateKey: personTemplate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("matcher-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	first, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"age":  30,
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate first: %v", err)
	}
	second, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"age":  20,
		"name": "Grace",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate second: %v", err)
	}
	third, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"age":  40,
		"name": "Barbara",
	}))
	if err != nil {
		t.Fatalf("AssertTemplate third: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	before := snapshot.String()

	results, err := newNaiveMatcher(revision).match(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if snapshot.String() != before {
		t.Fatal("matcher mutated the snapshot")
	}
	if got, want := len(results), 2; got != want {
		t.Fatalf("rule result count = %d, want %d", got, want)
	}

	matchPerson := revision.rules["match-person"]
	if got := results[0]; got.ruleID != matchPerson.id || got.ruleRevisionID != matchPerson.revisionID {
		t.Fatalf("first rule result = %#v, want match-person metadata", got)
	}
	if got := results[0].declarationOrder; got != 0 {
		t.Fatalf("first rule declaration order = %d, want 0", got)
	}
	if got, want := len(results[0].candidates), 3; got != want {
		t.Fatalf("match-person candidate count = %d, want %d", got, want)
	}
	for i, candidate := range results[0].candidates {
		if got := candidate.bindingTuple; len(got) != 1 || got[0].binding != "person" || got[0].bindingSlot != 0 || got[0].conditionOrder != 0 {
			t.Fatalf("match-person candidate %d tuple = %#v, want single person binding", i, got)
		}
		if got, want := candidate.bindingTuple[0].conditionPath, []int{0}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("match-person candidate %d condition path = %#v, want %#v", i, got, want)
		}
		if candidate.factIDs[0] != []FactID{first.Fact.ID(), second.Fact.ID(), third.Fact.ID()}[i] {
			t.Fatalf("match-person candidate %d fact id = %q, want insertion order", i, candidate.factIDs[0])
		}
		if candidate.factVersions[0] != []FactVersion{first.Fact.Version(), second.Fact.Version(), third.Fact.Version()}[i] {
			t.Fatalf("match-person candidate %d fact version = %d, want %d", i, candidate.factVersions[0], []FactVersion{first.Fact.Version(), second.Fact.Version(), third.Fact.Version()}[i])
		}
		if candidate.generation != snapshot.Generation() {
			t.Fatalf("match-person candidate %d generation = %d, want %d", i, candidate.generation, snapshot.Generation())
		}
		if candidate.maxRecency != []Recency{first.Fact.Recency(), second.Fact.Recency(), third.Fact.Recency()}[i] {
			t.Fatalf("match-person candidate %d max recency = %d", i, candidate.maxRecency)
		}
		if candidate.aggregateRecency != candidate.maxRecency {
			t.Fatalf("match-person candidate %d aggregate recency = %d, want %d", i, candidate.aggregateRecency, candidate.maxRecency)
		}
		if got, want := candidate.path, []int{0}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("match-person candidate %d path = %#v, want %#v", i, got, want)
		}
		if candidate.identity.isZero() {
			t.Fatalf("match-person candidate %d identity is empty", i)
		}
	}

	agePairs := revision.rules["age-pairs"]
	if got := results[1]; got.ruleID != agePairs.id || got.ruleRevisionID != agePairs.revisionID {
		t.Fatalf("second rule result = %#v, want age-pairs metadata", got)
	}
	if got := results[1].declarationOrder; got != 1 {
		t.Fatalf("second rule declaration order = %d, want 1", got)
	}
	if got, want := len(results[1].candidates), 3; got != want {
		t.Fatalf("age-pairs candidate count = %d, want %d", got, want)
	}

	wantFactIDs := [][]FactID{
		{first.Fact.ID(), third.Fact.ID()},
		{second.Fact.ID(), first.Fact.ID()},
		{second.Fact.ID(), third.Fact.ID()},
	}
	wantVersions := [][]FactVersion{
		{first.Fact.Version(), third.Fact.Version()},
		{second.Fact.Version(), first.Fact.Version()},
		{second.Fact.Version(), third.Fact.Version()},
	}
	wantMaxRecency := []Recency{third.Fact.Recency(), second.Fact.Recency(), third.Fact.Recency()}
	wantAggregateRecency := []Recency{Recency(uint64(first.Fact.Recency()) + uint64(third.Fact.Recency())), Recency(uint64(second.Fact.Recency()) + uint64(first.Fact.Recency())), Recency(uint64(second.Fact.Recency()) + uint64(third.Fact.Recency()))}

	for i, candidate := range results[1].candidates {
		if got, want := len(candidate.bindingTuple), 2; got != want {
			t.Fatalf("age-pairs candidate %d tuple length = %d, want %d", i, got, want)
		}
		if candidate.bindingTuple[0].binding != "threshold" || candidate.bindingTuple[1].binding != "candidate" {
			t.Fatalf("age-pairs candidate %d tuple order = %#v, want threshold then candidate", i, candidate.bindingTuple)
		}
		if candidate.bindingTuple[0].bindingSlot != 0 || candidate.bindingTuple[1].bindingSlot != 1 {
			t.Fatalf("age-pairs candidate %d binding slots = %#v, want threshold=0 candidate=1", i, candidate.bindingTuple)
		}
		if candidate.bindingTuple[0].conditionOrder != 0 || candidate.bindingTuple[1].conditionOrder != 1 {
			t.Fatalf("age-pairs candidate %d condition order = %#v, want threshold=0 candidate=1", i, candidate.bindingTuple)
		}
		if got, want := candidate.bindingTuple[0].conditionPath, []int{0}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("age-pairs candidate %d threshold condition path = %#v, want %#v", i, got, want)
		}
		if got, want := candidate.bindingTuple[1].conditionPath, []int{1}; len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("age-pairs candidate %d candidate condition path = %#v, want %#v", i, got, want)
		}
		if candidate.factIDs[0] != wantFactIDs[i][0] || candidate.factIDs[1] != wantFactIDs[i][1] {
			t.Fatalf("age-pairs candidate %d fact ids = %#v, want %#v", i, candidate.factIDs, wantFactIDs[i])
		}
		if candidate.factVersions[0] != wantVersions[i][0] || candidate.factVersions[1] != wantVersions[i][1] {
			t.Fatalf("age-pairs candidate %d fact versions = %#v, want %#v", i, candidate.factVersions, wantVersions[i])
		}
		if candidate.generation != snapshot.Generation() {
			t.Fatalf("age-pairs candidate %d generation = %d, want %d", i, candidate.generation, snapshot.Generation())
		}
		if candidate.maxRecency != wantMaxRecency[i] {
			t.Fatalf("age-pairs candidate %d max recency = %d, want %d", i, candidate.maxRecency, wantMaxRecency[i])
		}
		if candidate.aggregateRecency != wantAggregateRecency[i] {
			t.Fatalf("age-pairs candidate %d aggregate recency = %d, want %d", i, candidate.aggregateRecency, wantAggregateRecency[i])
		}
		if got, want := candidate.path, []int{0, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("age-pairs candidate %d path = %#v, want %#v", i, got, want)
		}
		if candidate.identity.isZero() {
			t.Fatalf("age-pairs candidate %d identity is empty", i)
		}
	}
}

func TestCandidateIdentityCanonicalizesBindingTupleOrder(t *testing.T) {
	base := []bindingTupleEntry{
		{
			binding:        "beta",
			bindingSlot:    1,
			conditionOrder: 1,
			conditionID:    ConditionID("condition-beta"),
			conditionPath:  []int{1},
			factID:         newFactID(1, 2),
			factVersion:    2,
		},
		{
			binding:        "alpha",
			bindingSlot:    0,
			conditionOrder: 0,
			conditionID:    ConditionID("condition-alpha"),
			conditionPath:  []int{0},
			factID:         newFactID(1, 1),
			factVersion:    1,
		},
	}
	reversed := []bindingTupleEntry{base[1], base[0]}

	keyA := candidateIdentityFor(RuleID("rule"), RuleRevisionID("revision"), 0, 7, base)
	keyB := candidateIdentityFor(RuleID("rule"), RuleRevisionID("revision"), 0, 7, reversed)

	if keyA != keyB {
		t.Fatalf("canonical identities differ for reordered bindings: %#v vs %#v", keyA, keyB)
	}
}

func TestCollectMatchCandidatesSuppressesDuplicateBindingTuples(t *testing.T) {
	rule := compiledRule{
		id:         RuleID("rule"),
		revisionID: RuleRevisionID("revision"),
		name:       "rule",
		conditions: []RuleCondition{
			{
				id:      ConditionID("condition"),
				binding: "fact",
				order:   0,
			},
		},
		conditionPlans: []compiledConditionPlan{
			{
				id:          ConditionID("condition"),
				binding:     "fact",
				bindingSlot: 0,
				path:        []int{0},
			},
		},
	}
	fact := FactSnapshot{
		id:         newFactID(1, 1),
		name:       "fact",
		version:    1,
		recency:    1,
		generation: 1,
	}
	bindingSets := []bindingSet{
		{matches: []conditionMatch{{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}}},
		{matches: []conditionMatch{{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}}},
	}

	candidates, err := collectMatchCandidates(context.Background(), rule, newSnapshot("session", "ruleset", 1, nil), bindingSets)
	if err != nil {
		t.Fatalf("collectMatchCandidates: %v", err)
	}
	if got, want := len(candidates), 1; got != want {
		t.Fatalf("candidate count = %d, want %d", got, want)
	}
}

func TestMatchCandidatesMatchesMaterializedBindingSets(t *testing.T) {
	revision, snapshot := mustMatcherJoinFixture(t)
	rule := revision.rules["age-pairs"]

	streamed, err := rule.matchCandidates(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("matchCandidates: %v", err)
	}
	sets, err := rule.matchBindingSets(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("matchBindingSets: %v", err)
	}
	materializedSets := make([]bindingSet, len(sets))
	for i, set := range sets {
		materializedSets[i] = bindingSet{matches: set.matches}
	}
	materialized, err := collectMatchCandidates(context.Background(), rule, snapshot, materializedSets)
	if err != nil {
		t.Fatalf("collectMatchCandidates: %v", err)
	}

	if !reflect.DeepEqual(streamed, materialized) {
		t.Fatalf("streamed candidates differ from materialized candidates:\nstreamed=%#v\nmaterialized=%#v", streamed, materialized)
	}
}

func TestMatchBindingSetsIncludeLinkedTokens(t *testing.T) {
	revision, snapshot := mustMatcherJoinFixture(t)
	rule := revision.rules["age-pairs"]

	sets, err := rule.matchBindingSets(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("matchBindingSets: %v", err)
	}
	if got, want := len(sets), 6; got != want {
		t.Fatalf("binding set count = %d, want %d", got, want)
	}
	if sets[0].token == nil {
		t.Fatal("binding set token is nil")
	}
	if sets[0].token.parent == nil {
		t.Fatal("binding set token parent is nil")
	}
	if got, want := sets[0].token.size, 2; got != want {
		t.Fatalf("binding set token size = %d, want %d", got, want)
	}
	if got, want := sets[0].token.pathLen, 2; got != want {
		t.Fatalf("binding set token path length = %d, want %d", got, want)
	}

	candidate, err := buildMatchCandidate(rule, snapshot.Generation(), sets[0])
	if err != nil {
		t.Fatalf("buildMatchCandidate: %v", err)
	}
	if got, want := candidate.path, []int{0, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("linked token candidate path = %#v, want %#v", got, want)
	}
}

func TestLinkedMatchTokenEqualityUsesFactIdentity(t *testing.T) {
	revision, snapshot := mustMatcherJoinFixture(t)
	rule := revision.rules["age-pairs"]

	sets, err := rule.matchBindingSets(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("matchBindingSets: %v", err)
	}
	if len(sets) < 2 {
		t.Fatalf("binding set count = %d, want at least 2", len(sets))
	}

	var rebuilt *matchToken
	for i, match := range sets[0].matches {
		entry := rule.conditionPlans[i].bindingTupleEntry(match)
		rebuilt = newMatchToken(rebuilt, entry, match, match.fact.Recency(), snapshot.Generation())
	}
	if !matchTokenEqual(sets[0].token, rebuilt) {
		t.Fatalf("rebuilt token is not equal:\nleft=%#v\nright=%#v", sets[0].token, rebuilt)
	}
	if matchTokenEqual(sets[0].token, sets[1].token) {
		t.Fatalf("different token chains compare equal:\nleft=%#v\nright=%#v", sets[0].token, sets[1].token)
	}
}

func TestCollectMatchCandidatesObservesCancellation(t *testing.T) {
	rule := compiledRule{
		id:         RuleID("rule"),
		revisionID: RuleRevisionID("revision"),
		name:       "rule",
		conditions: []RuleCondition{
			{
				id:      ConditionID("condition"),
				binding: "fact",
				order:   0,
			},
		},
		conditionPlans: []compiledConditionPlan{
			{
				id:          ConditionID("condition"),
				binding:     "fact",
				bindingSlot: 0,
				path:        []int{0},
			},
		},
	}
	fact := FactSnapshot{
		id:         newFactID(1, 1),
		name:       "fact",
		version:    1,
		recency:    1,
		generation: 1,
	}
	bindingSets := []bindingSet{
		{matches: []conditionMatch{{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	candidates, err := collectMatchCandidates(ctx, rule, newSnapshot("session", "ruleset", 1, nil), bindingSets)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("collectMatchCandidates error = %v, want context.Canceled", err)
	}
	if candidates != nil {
		t.Fatalf("candidates = %#v, want nil after cancellation", candidates)
	}
}

func BenchmarkNaiveMatcherJoinCandidates(b *testing.B) {
	revision, snapshot := mustMatcherJoinFixture(b)
	rule := revision.rules["age-pairs"]

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates, err := rule.matchCandidates(context.Background(), snapshot)
		if err != nil {
			b.Fatalf("matchCandidates: %v", err)
		}
		if len(candidates) != 6 {
			b.Fatalf("candidate count = %d, want 6", len(candidates))
		}
	}
}

func mustMatcherJoinFixture(tb testing.TB) (*Ruleset, Snapshot) {
	tb.Helper()

	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "name", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "age-pairs",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: personTemplate.Key()},
			{
				Binding:     "candidate",
				TemplateKey: personTemplate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		tb.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("matcher-benchmark-session"))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	for i, age := range []any{20, 21, 22, 23} {
		if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(tb, map[string]any{
			"age":  age,
			"name": string(rune('a' + i)),
		})); err != nil {
			tb.Fatalf("AssertTemplate(%v): %v", age, err)
		}
	}

	return revision, session.indexedSnapshotLocked()
}

func TestNaiveMatcherCancellationReturnsContextError(t *testing.T) {
	workspace := NewWorkspace()
	personTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueAny}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "join-cancel",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: personTemplate.Key()},
			{
				Binding:     "candidate",
				TemplateKey: personTemplate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("matcher-cancel-session"))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, age := range []any{20, 21, 22, 23} {
		if _, err := session.AssertTemplate(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{"age": age})); err != nil {
			t.Fatalf("AssertTemplate(%v): %v", age, err)
		}
	}
	snapshot := mustSnapshot(t, context.Background(), session)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := newNaiveMatcher(revision).match(ctx, snapshot)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("match error = %v, want context.Canceled", err)
	}
	if results != nil {
		t.Fatalf("match results = %#v, want nil after cancellation", results)
	}
}
