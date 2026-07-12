package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// FuzzExecutableSemantics is the standing graph-vs-oracle differential
// harness. Inputs describe a bounded ruleset, fact corpus, and lifecycle. The
// generated programs deliberately stay within graph-native production shapes;
// unsupported shapes belong in focused compiler tests until the graph supports
// them.
func FuzzExecutableSemantics(f *testing.F) {
	for _, seed := range [][]byte{
		{0, 1, 2, 3, 4, 5},
		{1, 8, 7, 6, 5, 4, 3, 2},
		{2, 3, 1, 4, 1, 5, 9, 2, 6},
		{3, 0xff, 0, 0x80, 7, 3, 11},
		{4, 2, 7, 1, 8, 2, 8, 1},
		{5, 6, 5, 3, 5, 8, 9, 7, 9},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		corpus := newSemanticsCorpus(data)
		revision, templateKey := corpus.compile(t)
		left := corpus.run(t, revision, templateKey, "semantics-replay")
		right := corpus.run(t, revision, templateKey, "semantics-replay")

		leftJSON := semanticsExplainJSON(t, left)
		rightJSON := semanticsExplainJSON(t, right)
		if !bytes.Equal(leftJSON, rightJSON) {
			t.Fatalf("equal histories produced different explain JSON:\nleft=%s\nright=%s", leftJSON, rightJSON)
		}
	})
}

type semanticsCorpus struct {
	bytes []byte
	pos   int
	shape byte
	limit int64
	facts []semanticsFact
}

type semanticsFact struct {
	group  int64
	score  int64
	active bool
}

func newSemanticsCorpus(data []byte) semanticsCorpus {
	if len(data) == 0 {
		data = []byte{0}
	}
	c := semanticsCorpus{bytes: append([]byte(nil), data...)}
	c.shape = c.next() % 6
	c.limit = int64(c.next() % 5)
	factCount := 1 + int(c.next()%8)
	c.facts = make([]semanticsFact, factCount)
	for i := range c.facts {
		c.facts[i] = semanticsFact{
			group:  int64(c.next() % 4),
			score:  int64(c.next() % 8),
			active: c.next()%2 == 0,
		}
	}
	return c
}

func (c *semanticsCorpus) next() byte {
	value := c.bytes[c.pos%len(c.bytes)]
	c.pos++
	return value
}

func (c semanticsCorpus) compile(t testing.TB) (*Ruleset, TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name:            fmt.Sprintf("entity_%d", c.shape),
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueInt, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
			{Name: "active", Kind: ValueBool, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "observe", Fn: func(ActionContext) error { return nil }})
	mustAddRule(t, workspace, RuleSpec{
		Name:          "property",
		Salience:      int(c.limit) - 2,
		ConditionTree: c.condition(template.Key()),
		Actions:       []RuleActionSpec{{Name: "observe"}},
	})
	return mustCompileWorkspace(t, workspace), template.Key()
}

func (c semanticsCorpus) condition(templateKey TemplateKey) ConditionSpec {
	match := func(binding string) Match {
		return Match{Binding: binding, Target: TemplateKeyFact(templateKey)}
	}
	constrained := func(binding string, constraints ...FieldConstraintSpec) Match {
		return Match{Binding: binding, Target: TemplateKeyFact(templateKey), FieldConstraints: constraints}
	}
	joinGroup := func(binding, earlier string) Match {
		return Match{
			Binding: binding,
			Target:  TemplateKeyFact(templateKey),
			JoinConstraints: []JoinConstraintSpec{{
				Field: "group", Operator: FieldConstraintEqual,
				Ref: FieldRef{Binding: earlier, Field: "group"},
			}},
		}
	}

	switch c.shape {
	case 0:
		return constrained("item",
			FieldConstraintSpec{Field: "active", Operator: FieldConstraintEqual, Value: true},
			FieldConstraintSpec{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: c.limit},
		)
	case 1:
		return And{Conditions: []ConditionSpec{match("left"), joinGroup("right", "left")}}
	case 2:
		blocker := constrained("blocker", FieldConstraintSpec{Field: "active", Operator: FieldConstraintEqual, Value: true})
		return And{Conditions: []ConditionSpec{match("item"), Not{Condition: blocker}}}
	case 3:
		return Or{Conditions: []ConditionSpec{
			constrained("item", FieldConstraintSpec{Field: "active", Operator: FieldConstraintEqual, Value: true}),
			constrained("item", FieldConstraintSpec{Field: "score", Operator: FieldConstraintGreaterThan, Value: c.limit}),
		}}
	case 4:
		peer := joinGroup("peer", "item")
		peer.FieldConstraints = []FieldConstraintSpec{{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: c.limit}}
		return And{Conditions: []ConditionSpec{match("item"), Exists(peer)}}
	default:
		return And{Conditions: []ConditionSpec{
			match("item"),
			Accumulate(joinGroup("member", "item"), Count().As("member_count")),
		}}
	}
}

func (c semanticsCorpus) run(t *testing.T, revision *Ruleset, templateKey TemplateKey, id SessionID) *Session {
	t.Helper()
	ctx := context.Background()
	session, err := NewSession(revision, WithSessionID(id), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ids := make([]FactID, 0, len(c.facts))
	for _, fact := range c.facts {
		result, err := session.Assert(ctx, templateKey, mustFields(t, map[string]any{
			"group": fact.group, "score": fact.score, "active": fact.active,
		}))
		if err != nil {
			t.Fatalf("Assert: %v", err)
		}
		ids = append(ids, result.Fact.ID())
		assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.propagation.runtime)
	}

	modifyIndex := int(c.bytes[0]) % len(ids)
	if _, err := session.Modify(ctx, ids[modifyIndex], FactPatch{Set: mustFields(t, map[string]any{
		"score": int64(c.bytes[len(c.bytes)-1] % 8),
	})}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.propagation.runtime)

	if len(ids) > 1 {
		retractIndex := (modifyIndex + 1) % len(ids)
		if _, err := session.Retract(ctx, ids[retractIndex]); err != nil {
			t.Fatalf("Retract: %v", err)
		}
		assertMatcherParity(t, revision, mustSnapshot(t, ctx, session), newNaiveMatcher(revision), session.propagation.runtime)
	}
	return session
}

func semanticsExplainJSON(t *testing.T, session *Session) []byte {
	t.Helper()
	ctx := context.Background()
	snapshot := mustSnapshot(t, ctx, session)
	facts := snapshot.Facts()
	if len(facts) == 0 {
		t.Fatal("semantics corpus unexpectedly has no live facts")
	}
	derivation, err := session.Explain(ctx, facts[0].ID())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	whyNot, err := session.WhyNot(ctx, "property")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	emptyRevision, err := NewWorkspace().Compile(ctx)
	if err != nil {
		t.Fatalf("Compile empty WhatIf ruleset: %v", err)
	}
	whatIfBase, err := NewSession(emptyRevision, WithSessionID("semantics-whatif"))
	if err != nil {
		t.Fatalf("NewSession for WhatIf: %v", err)
	}
	defer whatIfBase.Close()
	whatIf, err := whatIfBase.WhatIf(ctx, func(context.Context, *Session) error { return nil })
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	document, err := json.Marshal([]any{derivation, whyNot, whatIf})
	if err != nil {
		t.Fatalf("Marshal explain documents: %v", err)
	}
	return document
}
