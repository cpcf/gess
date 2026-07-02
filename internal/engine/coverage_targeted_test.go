package engine

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestCoverageActionTokenFallbackHelpers(t *testing.T) {
	ctx := context.Background()
	workspace := NewWorkspace()
	source := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "source",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name: "source-rule",
		Conditions: []RuleConditionSpec{{
			Binding: "source", Target: TemplateKeyFact(source.Key()),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision := mustCompileWorkspace(t, workspace)
	session := mustSession(t, revision, "coverage-action-token-fallback")
	if _, err := session.AssertTemplate(ctx, source.Key(), mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, ok, err := session.reconcileAgendaWithoutSnapshot(ctx); err != nil {
		t.Fatalf("reconcileAgendaWithoutSnapshot: %v", err)
	} else if !ok {
		t.Fatal("reconcileAgendaWithoutSnapshot unavailable")
	}
	session.agenda.normalizePendingKeys()
	if len(session.agenda.pending) != 1 {
		t.Fatalf("pending activations = %d, want 1", len(session.agenda.pending))
	}
	activation, ok := session.agenda.activationByKeyPtr(session.agenda.pending[0])
	if !ok {
		t.Fatal("missing internal activation")
	}
	rule := revision.rulesByRevisionID[activation.ruleRevisionID]
	matches, err := session.actionMatchesForActivation(*activation, rule)
	if err != nil {
		t.Fatalf("actionMatchesForActivation: %v", err)
	}
	if got, want := len(matches), 1; got != want {
		t.Fatalf("matches = %d, want %d", got, want)
	}
	activationFactIDs := cloneActivationFactIDs(activation)
	if matches[0].fact.ID() != activationFactIDs[0] {
		t.Fatalf("match fact ID = %q, want %q", matches[0].fact.ID(), activationFactIDs[0])
	}

	expression, ok, err := compileExpressionSpec(
		BindingFieldExpr{Binding: "source", Field: "id"},
		"coverage",
		0,
		0,
		nil,
		rule.conditions,
		map[string]int{"source": 0},
		map[TemplateKey]Template{source.Key(): source},
	)
	if err != nil || !ok {
		t.Fatalf("compileExpressionSpec = (%v, %v), want ok", err, ok)
	}
	value, err := evaluateNativeActionExpressionWithToken(ctx, expression, activation.token)
	if err != nil {
		t.Fatalf("evaluateNativeActionExpressionWithToken: %v", err)
	}
	if got, ok := value.AsString(); !ok || got != "s-1" {
		t.Fatalf("token expression value = (%q, %v), want s-1", got, ok)
	}

	stale := *activation
	stale.payload = nil
	stale.setFactIDs(cloneActivationFactIDs(&stale))
	staleVersions := cloneActivationFactVersions(&stale)
	stale.token = tokenRef{}
	staleVersions[0]++
	stale.setFactVersions(staleVersions)
	if _, err := session.actionMatchesForActivation(stale, rule); !errors.Is(err, ErrMatcher) {
		t.Fatalf("stale actionMatchesForActivation error = %v, want ErrMatcher", err)
	}
}

func TestCoverageLowerQueryConditionTreeParamsAcrossShapes(t *testing.T) {
	params := map[string]ValueKind{
		"dept":    ValueString,
		"enabled": ValueBool,
		"limit":   ValueInt,
	}
	match := Match{
		Binding: "person",

		Predicates: []ExpressionSpec{
			CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "dept"}, Right: ParamExpr{Name: "dept"}},
			Call("check-limit", ParamExpr{Name: "limit"}),
		}, Target: TemplateKeyFact(TemplateKey("person")),
	}
	tree := &And{Conditions: []ConditionSpec{
		&match,
		&Or{Conditions: []ConditionSpec{
			&Test{Expression: CompareExpr{Operator: ExpressionCompareEqual, Left: ParamExpr{Name: "enabled"}, Right: ConstExpr{Value: true}}},
			&Not{Condition: Exists(&Match{Binding: "blocked", Target: TemplateKeyFact(TemplateKey("person"))})},
			Forall(
				&Match{Binding: "domain", Target: TemplateKeyFact(TemplateKey("person"))},
				&Test{Expression: CompareExpr{Operator: ExpressionCompareLessOrEqual, Left: ParamExpr{Name: "limit"}, Right: ConstExpr{Value: 10}}},
			),
		}},
		Accumulate(&Match{
			Binding: "item",

			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareGreaterThan, Left: CurrentFieldExpr{Field: "score"}, Right: ParamExpr{Name: "limit"}},
			}, Target: TemplateKeyFact(TemplateKey("item")),
		}, Sum(ParamExpr{Name: "limit"}).As("total")),
	}}

	lowered, ok := lowerQueryConditionTreeParams(tree, params).(*And)
	if !ok {
		t.Fatalf("lowered tree type = %T, want *And", lowered)
	}
	loweredMatch := lowered.Conditions[0].(*Match)
	if got, want := len(loweredMatch.JoinConstraints), 1; got != want {
		t.Fatalf("match join constraints = %d, want %d", got, want)
	}
	if loweredMatch.JoinConstraints[0].Ref.Binding != internalQueryTriggerBinding {
		t.Fatalf("join ref binding = %q, want query trigger", loweredMatch.JoinConstraints[0].Ref.Binding)
	}
	if got, want := len(loweredMatch.Predicates), 1; got != want {
		t.Fatalf("match predicates = %d, want %d", got, want)
	}
	assertExpressionUsesQueryTrigger(t, loweredMatch.Predicates[0])

	loweredOr := lowered.Conditions[1].(*Or)
	loweredTest := loweredOr.Conditions[0].(*Test)
	assertExpressionUsesQueryTrigger(t, loweredTest.Expression)
	loweredForall := loweredOr.Conditions[2].(ForallCondition)
	requirement := loweredForall.Requirement.(*Test)
	assertExpressionUsesQueryTrigger(t, requirement.Expression)

	loweredAggregate := lowered.Conditions[2].(AccumulateCondition)
	aggregateInput := loweredAggregate.Input.(*Match)
	if got, want := len(aggregateInput.JoinConstraints), 1; got != want {
		t.Fatalf("aggregate input joins = %d, want %d", got, want)
	}
	assertExpressionUsesQueryTrigger(t, loweredAggregate.Specs[0].expression)

	for _, nilSpec := range []ConditionSpec{(*And)(nil), (*Or)(nil), (*Not)(nil), (*ExistsCondition)(nil), (*ForallCondition)(nil), (*Test)(nil), (*Match)(nil), (*AccumulateCondition)(nil)} {
		if got := lowerQueryConditionTreeParams(nilSpec, params); got != nil {
			t.Fatalf("lower nil %T = %#v, want nil", nilSpec, got)
		}
	}
}

func TestCoverageGraphTerminalRemovalByFactIndex(t *testing.T) {
	ctx := context.Background()
	revision, templateKey := mustModifyFastPathRuleset(t)
	session, err := NewSession(revision, WithInitialFacts(SessionInitialFact{
		TemplateKey: templateKey,
		Fields: mustFields(t, map[string]any{
			"age":    32,
			"note":   "old",
			"status": "active",
		}),
	}))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.attachPropagationCounters()
	snapshot := mustSnapshot(t, ctx, session)
	facts := snapshot.FactsByTemplateKey(templateKey)
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(facts))
	}
	if revision.graph == nil || len(revision.graph.terminalNodes) != 1 {
		t.Fatalf("terminal nodes = %d, want 1", len(revision.graph.terminalNodes))
	}
	delta := reteAgendaDelta{supported: true}
	session.rete.graphBeta.removeTerminalTokensContainingFact(revision.graph.terminalNodes[0].id, facts[0].ID(), session.propagationCounters, &delta)
	if got, want := len(delta.removed), 1; got != want {
		t.Fatalf("removed terminal deltas = %d, want %d", got, want)
	}
	if delta.removed[0].identity.isZero() {
		t.Fatal("removed terminal delta missing identity")
	}
	counters := session.propagationCounterSnapshot().Totals
	if got, want := counters.TerminalRowsRemoved, 1; got != want {
		t.Fatalf("terminal rows removed = %d, want %d", got, want)
	}
	if got, want := counters.TerminalDeltasRemoved, 1; got != want {
		t.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
}

func TestCoverageAggregateStateResultsAcrossKinds(t *testing.T) {
	ctx := context.Background()
	amountExpression := compiledExpression{
		kind:       expressionNodeCurrentField,
		resultKind: ValueInt,
		access:     testCompiledPathAccess("amount"),
	}
	facts := []conditionFactRef{
		newConditionFactRefFromSnapshot(FactSnapshot{id: newFactID(1, 1), name: "item", fields: Fields{"amount": newIntValue(3)}}),
		newConditionFactRefFromSnapshot(FactSnapshot{id: newFactID(1, 2), name: "item", fields: Fields{"amount": newIntValue(5)}}),
	}

	cases := []struct {
		name string
		kind AggregateKind
		want Value
	}{
		{name: "count", kind: AggregateCount, want: newIntValue(2)},
		{name: "sum", kind: AggregateSum, want: newIntValue(8)},
		{name: "min", kind: AggregateMin, want: newIntValue(3)},
		{name: "max", kind: AggregateMax, want: newIntValue(5)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := aggregateState{spec: compiledAggregateSpec{kind: tc.kind, binding: tc.name, expression: amountExpression, hasExpr: tc.kind != AggregateCount}}
			for _, fact := range facts {
				if err := state.add(ctx, fact, nil); err != nil {
					t.Fatalf("aggregate add: %v", err)
				}
			}
			got, ok, err := state.result()
			if err != nil || !ok || !got.Equal(tc.want) {
				t.Fatalf("aggregate result = (%v, %v, %v), want %v", got, ok, err, tc.want)
			}
		})
	}

	collect := aggregateState{spec: compiledAggregateSpec{kind: AggregateCollect, binding: "items", expression: amountExpression, hasExpr: true}}
	for _, fact := range facts {
		if err := collect.add(ctx, fact, nil); err != nil {
			t.Fatalf("collect add: %v", err)
		}
	}
	collected, ok, err := collect.result()
	if err != nil || !ok || collected.Kind() != ValueList {
		t.Fatalf("collect result = (%v, %v, %v), want list", collected, ok, err)
	}
	values := collected.data.([]Value)
	if len(values) != 2 || !values[0].Equal(newIntValue(3)) || !values[1].Equal(newIntValue(5)) {
		t.Fatalf("collect values = %#v, want [3 5]", values)
	}

	for _, tc := range []struct {
		name  string
		state aggregateState
		ok    bool
		value Value
	}{
		{name: "exists empty", state: aggregateState{spec: compiledAggregateSpec{kind: aggregateExists}}, ok: false},
		{name: "forall empty", state: aggregateState{spec: compiledAggregateSpec{kind: aggregateForall}}, ok: true, value: newBoolValue(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := tc.state.result()
			if err != nil || ok != tc.ok || (ok && !got.Equal(tc.value)) {
				t.Fatalf("higher-order aggregate result = (%v, %v, %v), want (%v, %v)", got, ok, err, tc.value, tc.ok)
			}
		})
	}
}

func TestCoverageCompiledAggregatePlanEvaluatesInputMatches(t *testing.T) {
	ctx := context.Background()
	amountExpression := compiledExpression{
		kind:       expressionNodeCurrentField,
		resultKind: ValueInt,
		access:     testCompiledPathAccess("amount"),
	}
	inputPlan := compiledConditionPlan{
		id:          "item",
		binding:     "item",
		bindingSlot: 0,
		path:        []int{0},
		target:      conditionTarget{kind: conditionTargetName, name: "item"},
	}
	aggregatePlan := compiledConditionPlan{
		id:          "totals",
		bindingSlot: 1,
		aggregate: &compiledAggregatePlan{
			inputPlans: []compiledConditionPlan{inputPlan},
			specs: []compiledAggregateSpec{
				{kind: AggregateCount, binding: "count"},
				{kind: AggregateSum, binding: "sum", expression: amountExpression, hasExpr: true},
			},
		},
	}
	source := newSnapshot("coverage-aggregate-plan", "coverage-ruleset", 1, []FactSnapshot{
		{id: newFactID(1, 1), name: "item", version: 1, recency: 1, generation: 1, fields: Fields{"amount": newIntValue(3)}},
		{id: newFactID(1, 2), name: "item", version: 1, recency: 2, generation: 1, fields: Fields{"amount": newIntValue(5)}},
	})

	var matches []conditionMatch
	if err := aggregatePlan.forEachAggregateMatch(ctx, source, nil, func(match conditionMatch) error {
		matches = append(matches, match)
		return nil
	}); err != nil {
		t.Fatalf("forEachAggregateMatch: %v", err)
	}
	if got, want := len(matches), 2; got != want {
		t.Fatalf("aggregate matches = %d, want %d", got, want)
	}
	if !matches[0].hasValue || matches[0].bindingSlot != 1 || !matches[0].value.Equal(newIntValue(2)) {
		t.Fatalf("count match = %#v, want binding slot 1 value 2", matches[0])
	}
	if !matches[1].hasValue || matches[1].bindingSlot != 2 || !matches[1].value.Equal(newIntValue(8)) {
		t.Fatalf("sum match = %#v, want binding slot 2 value 8", matches[1])
	}

	missingAggregate := compiledConditionPlan{id: "missing"}
	if err := missingAggregate.forEachAggregateMatch(ctx, source, nil, func(conditionMatch) error { return nil }); !errors.Is(err, ErrAggregateEvaluation) {
		t.Fatalf("missing aggregate error = %v, want ErrAggregateEvaluation", err)
	}
}

func TestCoverageExpressionWrappersForFactsAndTokens(t *testing.T) {
	ctx := context.Background()
	fact := newConditionFactRefFromSnapshot(FactSnapshot{
		id:         newFactID(1, 1),
		name:       "event",
		version:    1,
		recency:    3,
		generation: 1,
		fields: Fields{
			"score": newIntValue(12),
		},
	})
	bindingFact := newConditionFactRefFromSnapshot(FactSnapshot{
		id:         newFactID(1, 2),
		name:       "event",
		version:    1,
		recency:    4,
		generation: 1,
		fields: Fields{
			"score": newIntValue(7),
		},
	})
	currentScore := compiledExpression{kind: expressionNodeCurrentField, resultKind: ValueInt, access: testCompiledPathAccess("score")}
	bindingScore := compiledExpression{kind: expressionNodeBindingField, resultKind: ValueInt, bindingSlot: 0, access: testCompiledPathAccess("score")}
	bindings := []conditionMatch{{bindingSlot: 0, fact: bindingFact}}

	if got, ok := currentScore.currentValueFromFact(fact); !ok || !got.Equal(newIntValue(12)) {
		t.Fatalf("currentValueFromFact = (%v, %v), want 12", got, ok)
	}
	if got, ok := bindingScore.bindingValueFromFact(bindingFact); !ok || !got.Equal(newIntValue(7)) {
		t.Fatalf("bindingValueFromFact = (%v, %v), want 7", got, ok)
	}

	predicate := compiledExpressionPredicate{expression: compiledExpression{
		kind:       expressionNodeCompare,
		resultKind: ValueBool,
		compareOp:  ExpressionCompareGreaterOrEqual,
		operands: []compiledExpression{
			currentScore,
			{kind: expressionNodeConst, resultKind: ValueInt, value: newIntValue(10)},
		},
	}}
	if ok, err := predicate.matches(fact, bindings); err != nil || !ok {
		t.Fatalf("predicate.matches = (%v, %v), want true", ok, err)
	}
	if ok, err := expressionPredicatesMatch([]compiledExpressionPredicate{predicate}, fact, bindings); err != nil || !ok {
		t.Fatalf("expressionPredicatesMatch = (%v, %v), want true", ok, err)
	}
	value, ok, err := predicate.expression.evaluateWithContextParams(ctx, fact, bindings, nil)
	if err != nil || !ok || !value.Equal(newBoolValue(true)) {
		t.Fatalf("evaluateWithContextParams = (%v, %v, %v), want true", value, ok, err)
	}

	arena := newTokenArena()
	plan := compiledConditionPlan{id: "event", binding: "event", bindingSlot: 0, path: []int{0}}
	match := conditionMatch{conditionID: plan.id, bindingSlot: 0, fact: bindingFact}
	token := arena.add(tokenRef{}, plan.bindingTupleEntry(match), match, bindingFact.Recency(), bindingFact.Generation())
	tokenPredicate := compiledExpressionPredicate{expression: compiledExpression{
		kind:       expressionNodeCompare,
		resultKind: ValueBool,
		compareOp:  ExpressionCompareEqual,
		operands: []compiledExpression{
			bindingScore,
			{kind: expressionNodeConst, resultKind: ValueInt, value: newIntValue(7)},
		},
	}}
	if ok, err := tokenPredicate.matchesToken(fact, token); err != nil || !ok {
		t.Fatalf("matchesToken = (%v, %v), want true", ok, err)
	}
	if ok, err := expressionPredicatesMatchToken([]compiledExpressionPredicate{tokenPredicate}, fact, token, nil); err != nil || !ok {
		t.Fatalf("expressionPredicatesMatchToken = (%v, %v), want true", ok, err)
	}

	paramExpression := compiledExpression{kind: expressionNodeParam, resultKind: ValueInt, paramName: "limit"}
	value, ok, err = paramExpression.evaluateTokenWithParams(fact, token, map[string]Value{"limit": newIntValue(9)})
	if err != nil || !ok || !value.Equal(newIntValue(9)) {
		t.Fatalf("evaluateTokenWithParams = (%v, %v, %v), want 9", value, ok, err)
	}
	value, ok, err = paramExpression.evaluateTokenWithContextParamsOffset(ctx, fact, token, map[string]Value{"limit": newIntValue(11)}, 0)
	if err != nil || !ok || !value.Equal(newIntValue(11)) {
		t.Fatalf("evaluateTokenWithContextParamsOffset = (%v, %v, %v), want 11", value, ok, err)
	}
}

func TestCoverageAgendaActivationFromCandidateAndTerminalToken(t *testing.T) {
	rule := compiledRule{
		id:                "rule",
		revisionID:        "rule-revision",
		name:              "rule",
		salience:          7,
		declarationOrder:  3,
		identityScopeHash: candidateIdentityScopeHash("rule", "rule-revision"),
		conditions: []RuleCondition{{
			id:      "event",
			binding: "event",
			order:   0,
		}},
		conditionPlans: []compiledConditionPlan{{
			id:          "event",
			binding:     "event",
			bindingSlot: 0,
			path:        []int{0},
		}},
	}
	fact := newConditionFactRefFromSnapshot(FactSnapshot{
		id:         newFactID(1, 1),
		name:       "event",
		version:    2,
		recency:    5,
		generation: 1,
		fields:     Fields{"kind": newStringValue("ready")},
	})
	match := conditionMatch{conditionID: "event", bindingSlot: 0, fact: fact}
	entry := rule.conditionPlans[0].bindingTupleEntry(match)
	arena := newTokenArena()
	token := arena.add(tokenRef{}, entry, match, fact.Recency(), fact.Generation())

	activation, err := activationFromTerminalToken(rule, token)
	if err != nil {
		t.Fatalf("activationFromTerminalToken: %v", err)
	}
	activationFactIDs := cloneActivationFactIDs(&activation)
	if activation.ruleRevisionID != rule.revisionID || activation.salience != rule.salience || activationFactIDs[0] != fact.ID() {
		t.Fatalf("terminal token activation = %#v, want rule/fact metadata", activation)
	}
	if _, count, ok := terminalTokenIdentityStateForRule(rule, token, candidateIdentityHashStart(fact.Generation()), 0); !ok || count != 1 {
		t.Fatalf("terminalTokenIdentityStateForRule = (_, %d, %v), want count 1 ok", count, ok)
	}

	candidate := matchCandidate{
		ruleID:           rule.id,
		ruleRevisionID:   rule.revisionID,
		generation:       fact.Generation(),
		identity:         candidateIdentityForTerminalToken(rule, token),
		bindingTuple:     []bindingTupleEntry{entry},
		factIDs:          []FactID{fact.ID()},
		factVersions:     []FactVersion{fact.Version()},
		maxRecency:       fact.Recency(),
		aggregateRecency: fact.Recency(),
	}
	candidateActivation := activationFromCandidate(rule, candidate)
	if candidateActivation.identityKey != activation.identityKey || candidateActivation.factIDs()[0] != fact.ID() {
		t.Fatalf("candidate activation = %#v, want identity/fact from terminal activation %#v", candidateActivation, activation)
	}

	agenda := newAgenda()
	key := agenda.storeActivation(&activation)
	if key == (activationKey{}) {
		t.Fatal("storeActivation returned zero key")
	}
	if found, foundKey, ok := agenda.activationForTerminalToken(rule, token); !ok || found == nil || foundKey != key {
		t.Fatalf("activationForTerminalToken = (%#v, %#v, %v), want stored activation", found, foundKey, ok)
	}
	if found, foundKey, ok := agenda.activationForCandidate(candidate); !ok || found == nil || foundKey != key {
		t.Fatalf("activationForCandidate = (%#v, %#v, %v), want stored activation", found, foundKey, ok)
	}
}

func assertExpressionUsesQueryTrigger(t testing.TB, spec ExpressionSpec) {
	t.Helper()
	if !expressionSpecUsesQueryTrigger(spec) {
		t.Fatalf("expression %#v does not reference query trigger binding", spec)
	}
}

func expressionSpecUsesQueryTrigger(spec ExpressionSpec) bool {
	switch expression := spec.(type) {
	case BindingFieldExpr:
		return expression.Binding == internalQueryTriggerBinding
	case *BindingFieldExpr:
		return expression != nil && expression.Binding == internalQueryTriggerBinding
	case CompareExpr:
		return expressionSpecUsesQueryTrigger(expression.Left) || expressionSpecUsesQueryTrigger(expression.Right)
	case *CompareExpr:
		return expression != nil && expressionSpecUsesQueryTrigger(*expression)
	case BooleanExpr:
		if slices.ContainsFunc(expression.Operands, expressionSpecUsesQueryTrigger) {
			return true
		}
	case *BooleanExpr:
		return expression != nil && expressionSpecUsesQueryTrigger(*expression)
	case CallExpr:
		if slices.ContainsFunc(expression.Args, expressionSpecUsesQueryTrigger) {
			return true
		}
	case *CallExpr:
		return expression != nil && expressionSpecUsesQueryTrigger(*expression)
	}
	return false
}
