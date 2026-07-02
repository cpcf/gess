package engine

import (
	"context"
	"reflect"
	"testing"
)

func TestNarrowedTokenRowsPreserveBoundaryMaterialization(t *testing.T) {
	rule := compiledRule{
		id:                "narrowed-token-rule",
		revisionID:        "narrowed-token-rule-revision",
		identityScopeHash: candidateIdentityScopeHash("narrowed-token-rule", "narrowed-token-rule-revision"),
		conditions: []RuleCondition{
			{id: "first", binding: "first", order: 0},
			{id: "second", binding: "second", order: 1},
		},
		conditionPlans: []compiledConditionPlan{
			{id: "first", binding: "first", bindingSlot: 0, path: []int{0}},
			{id: "second", binding: "second", bindingSlot: 1, path: []int{1}},
		},
	}
	firstFact := FactSnapshot{
		id:         newFactID(1, 1),
		version:    3,
		recency:    4,
		generation: 1,
		fields: Fields{
			"id":    newStringValue("first"),
			"score": newIntValue(3),
		},
	}
	secondFact := FactSnapshot{
		id:         newFactID(1, 2),
		version:    5,
		recency:    9,
		generation: 1,
		fields: Fields{
			"id":    newStringValue("second"),
			"score": newIntValue(5),
		},
	}
	token := narrowedTokenRowTestToken(rule, firstFact, secondFact)

	candidate, err := buildMatchCandidateFromTokenRef(rule, token)
	if err != nil {
		t.Fatalf("buildMatchCandidateFromTokenRef: %v", err)
	}
	if got, want := candidate.path, []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate path = %#v, want %#v", got, want)
	}
	if got := candidate.bindingTuple; len(got) != 2 || got[0].binding != "first" || got[1].binding != "second" {
		t.Fatalf("candidate bindings = %#v, want first/second", got)
	}
	if got, want := candidate.maxRecency, secondFact.Recency(); got != want {
		t.Fatalf("max recency = %d, want %d", got, want)
	}
	if got, want := candidate.totalRecency, Recency(uint64(firstFact.Recency())+uint64(secondFact.Recency())); got != want {
		t.Fatalf("total recency = %d, want %d", got, want)
	}

	actionCtx := newTokenActionContext(context.Background(), nil, activation{token: token, ruleRevisionID: rule.revisionID}, rule)
	firstIndex, ok := actionCtx.bindings.bindingIndex("first")
	if !ok || firstIndex != 0 {
		t.Fatalf("token-backed action binding index = %d, ok %t, want first at 0", firstIndex, ok)
	}
	if got := actionCtx.bindings.entryAt(firstIndex); got.binding != "first" || got.factID != firstFact.ID() {
		t.Fatalf("token-backed action binding entry = %#v, want first fact %q", got, firstFact.ID())
	}
	if _, ok := actionCtx.bindings.bindingIndex("missing"); ok {
		t.Fatal("missing token-backed action binding resolved")
	}

	query := compiledQuery{
		name:             "narrowed-token-query",
		returns:          narrowedTokenRowQueryReturns(),
		returnAliases:    []string{"first_id", "second_score"},
		returnAliasIndex: map[string]int{"first_id": 0, "second_score": 1},
	}
	row, err := query.materializeTokenValueRowInto(context.Background(), token, &compiledQueryArgs{}, 0, make([]Value, len(query.returns)))
	if err != nil {
		t.Fatalf("materializeTokenValueRowInto: %v", err)
	}
	if got, ok := row.Value("first_id"); !ok || !got.Equal(newStringValue("first")) {
		t.Fatalf("query first_id = %v, ok %t, want first", got, ok)
	}
	if got, ok := row.Value("second_score"); !ok || !got.Equal(newIntValue(5)) {
		t.Fatalf("query second_score = %v, ok %t, want 5", got, ok)
	}

	duplicate := narrowedTokenRowTestToken(rule, firstFact, secondFact)
	duplicateCandidate, err := buildMatchCandidateFromTokenRef(rule, duplicate)
	if err != nil {
		t.Fatalf("duplicate buildMatchCandidateFromTokenRef: %v", err)
	}
	if candidate.identity != duplicateCandidate.identity {
		t.Fatalf("duplicate token identity = %#v, want %#v", duplicateCandidate.identity, candidate.identity)
	}
	if got, want := candidateIdentityForTerminalToken(rule, token), candidate.identity; got != want {
		t.Fatalf("terminal token identity = %#v, want candidate identity %#v", got, want)
	}
}

func narrowedTokenRowTestToken(rule compiledRule, firstFact, secondFact FactSnapshot) tokenRef {
	arena := newTokenArena()
	firstMatch := conditionMatch{conditionID: "first", bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}
	secondMatch := conditionMatch{conditionID: "second", bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}
	first := arena.add(tokenRef{}, rule.conditionPlans[0].bindingTupleEntry(firstMatch), firstMatch, firstFact.Recency(), firstFact.Generation())
	return arena.add(first, rule.conditionPlans[1].bindingTupleEntry(secondMatch), secondMatch, secondFact.Recency(), secondFact.Generation())
}

func narrowedTokenRowQueryReturns() []compiledQueryReturn {
	return []compiledQueryReturn{
		{
			alias:       "first_id",
			binding:     "first",
			bindingSlot: 0,
			projection: compiledQueryReturnProjection{
				kind:        compiledQueryReturnProjectionBindingField,
				bindingSlot: 0,
				access:      testCompiledPathAccess("id"),
			},
		},
		{
			alias:       "second_score",
			binding:     "second",
			bindingSlot: 1,
			projection: compiledQueryReturnProjection{
				kind:        compiledQueryReturnProjectionBindingField,
				bindingSlot: 1,
				access:      testCompiledPathAccess("score"),
			},
			order: 1,
		},
	}
}
