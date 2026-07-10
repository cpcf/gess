package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func whyNotSession(t *testing.T, id SessionID, build func(*Workspace) map[string]TemplateKey) (*Session, map[string]TemplateKey) {
	t.Helper()
	workspace := NewWorkspace()
	keys := build(workspace)
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return mustSession(t, revision, id), keys
}

func noopAction() ActionSpec {
	return ActionSpec{Name: "noop", Fn: func(ActionContext) error { return nil }}
}

func TestWhyNotActivatedAndAlreadyFired(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-activated", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name:          "r",
			ConditionTree: Match{Binding: "a", Target: TemplateKeyFact(a)},
			Actions:       []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a}
	})

	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"id": "a-1"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotActivated {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotActivated)
	}
	if len(report.Activations) != 1 || report.Activations[0].RuleName() != "r" {
		t.Fatalf("Activations = %+v, want one for rule r", report.Activations)
	}

	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fired, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot after run: %v", err)
	}
	if fired.Outcome != WhyNotAlreadyFired {
		t.Fatalf("Outcome after run = %q, want %q", fired.Outcome, WhyNotAlreadyFired)
	}
	if len(fired.Branches) != 1 {
		t.Fatalf("branches after run = %d, want 1", len(fired.Branches))
	}
	branch := fired.Branches[0]
	if branch.FirstFailing != -1 || len(branch.Conditions) != 1 || !branch.Conditions[0].Satisfied {
		t.Fatalf("complete branch after run = %+v, want every condition satisfied with no failure", branch)
	}
	encoded, err := json.Marshal(fired)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var document struct {
		Branches []struct {
			FirstFailing int `json:"firstFailing"`
			Conditions   []struct {
				Satisfied bool `json:"satisfied"`
			} `json:"conditions"`
		} `json:"branches"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if len(document.Branches) != 1 || document.Branches[0].FirstFailing != -1 ||
		len(document.Branches[0].Conditions) != 1 || !document.Branches[0].Conditions[0].Satisfied {
		t.Fatalf("already_fired JSON branch = %s, want satisfied condition with no failure", encoded)
	}
}

func TestWhyNotNeverMatched(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-never", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b)},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "b": b}
	})

	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"id": "a-1"})); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Binding != "b" {
		t.Fatalf("first failing binding = %q, want b", failing.Binding)
	}
	if failing.Reason != WhyNotReasonNoAlphaMatches || failing.AlphaMatches != 0 {
		t.Fatalf("failing condition = %+v, want no_alpha_matches with 0 alpha matches", failing)
	}
	if len(branch.PartialMatches) != 1 || len(branch.PartialMatches[0].Facts) != 1 {
		t.Fatalf("partial matches = %+v, want one showing condition a's fact", branch.PartialMatches)
	}
}

// whyNotGroupAggregateRule builds a rule of the shape `group + Accumulate(Min)`:
// an outer grouping condition followed by an aggregate whose Min yields no row
// over an empty bucket.
func whyNotGroupAggregateRule(t *testing.T, w *Workspace) map[string]TemplateKey {
	t.Helper()
	group := mustAddTemplate(t, w, TemplateSpec{
		Name:            "group",
		DuplicatePolicy: DuplicateAllow,
		Fields:          []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	item := mustAddTemplate(t, w, TemplateSpec{
		Name:            "item",
		DuplicatePolicy: DuplicateAllow,
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
		},
	}).Key()
	mustAddAction(t, w, noopAction())
	mustAddRule(t, w, RuleSpec{
		Name: "r",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "group", Target: TemplateKeyFact(group)},
			Accumulate(
				Match{
					Binding: "item",
					Target:  TemplateKeyFact(item),
					JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					},
				},
				Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
			),
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	return map[string]TemplateKey{"group": group, "item": item}
}

func TestWhyNotAggregateNoOutputWithOuterToken(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-aggregate", func(w *Workspace) map[string]TemplateKey {
		return whyNotGroupAggregateRule(t, w)
	})

	// The group fact exists (the aggregate's outer token) but no item matches,
	// so Min over the empty bucket produces no output and the branch cannot
	// fire. The aggregate condition — not the matched outer condition — is the
	// frontier.
	if _, err := session.Assert(context.Background(), keys["group"], mustFields(t, map[string]any{"id": "g-1"})); err != nil {
		t.Fatalf("Assert(group): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	if branch.FirstFailing < 0 {
		t.Fatalf("FirstFailing = %d, want the aggregate condition", branch.FirstFailing)
	}
	// The frontier is the aggregate condition (order 1), not the matched outer
	// group condition (order 0) — the bug reported it as order 0.
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Order != 1 {
		t.Fatalf("first failing order = %d, want 1 (the aggregate condition, not the outer group)", failing.Order)
	}
	if !failing.Aggregate {
		t.Fatalf("failing condition = %+v, want the Aggregate flag set", failing)
	}
	if failing.Reason != WhyNotReasonNoAlphaMatches {
		t.Fatalf("failing reason = %q, want %q", failing.Reason, WhyNotReasonNoAlphaMatches)
	}
	group := branch.Conditions[0]
	if group.Order != 0 || group.Binding != "group" || group.Aggregate {
		t.Fatalf("condition 0 = %+v, want the non-aggregate outer group", group)
	}
	if !group.Satisfied {
		t.Fatalf("outer group condition should be satisfied: %+v", group)
	}
	if group.Reason != WhyNotReasonNone {
		t.Fatalf("outer group condition should carry no failure reason: %+v", group)
	}
	// The near-miss shows the grouping fact whose bucket produced no row.
	if len(branch.PartialMatches) != 1 || len(branch.PartialMatches[0].Facts) != 1 {
		t.Fatalf("partial matches = %+v, want one showing the group fact", branch.PartialMatches)
	}
}

// A condition after an aggregate must be blamed for the failure, not the
// aggregate. The aggregate feeds a beta as a non-beta stage, so the conditions
// below it are off the frontier's left spine and its planned position must
// offset the frontier count.
func TestWhyNotConditionAfterAggregate(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-after-aggregate", func(w *Workspace) map[string]TemplateKey {
		group := mustAddTemplate(t, w, TemplateSpec{Name: "group", DuplicatePolicy: DuplicateAllow, Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		item := mustAddTemplate(t, w, TemplateSpec{Name: "item", DuplicatePolicy: DuplicateAllow, Fields: []FieldSpec{{Name: "group", Kind: ValueString, Required: true}, {Name: "amount", Kind: ValueInt, Required: true}}}).Key()
		after := mustAddTemplate(t, w, TemplateSpec{Name: "after", DuplicatePolicy: DuplicateAllow, Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "group", Target: TemplateKeyFact(group)},
				Accumulate(
					Match{Binding: "item", Target: TemplateKeyFact(item), JoinConstraints: []JoinConstraintSpec{
						{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "group", Field: "id"}},
					}},
					Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
				),
				Match{Binding: "after", Target: TemplateKeyFact(after)},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"group": group, "item": item, "after": after}
	})
	ctx := context.Background()
	// group and item present so the aggregate produces its min row; no `after`
	// fact, so the trailing condition is the frontier.
	if _, err := session.Assert(ctx, keys["group"], mustFields(t, map[string]any{"id": "g-1"})); err != nil {
		t.Fatalf("Assert(group): %v", err)
	}
	if _, err := session.Assert(ctx, keys["item"], mustFields(t, map[string]any{"group": "g-1", "amount": int64(3)})); err != nil {
		t.Fatalf("Assert(item): %v", err)
	}

	report, err := session.WhyNot(ctx, "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Binding != "after" {
		t.Fatalf("FirstFailing blames %+v, want the `after` condition (planned 2), not the aggregate", failing)
	}
	if failing.Reason != WhyNotReasonNoAlphaMatches {
		t.Errorf("failing reason = %q, want %q", failing.Reason, WhyNotReasonNoAlphaMatches)
	}
	// The aggregate matched (produced its row) and must not be blamed.
	for _, cond := range branch.Conditions {
		if cond.Aggregate && (!cond.Satisfied || cond.Reason != WhyNotReasonNone) {
			t.Errorf("aggregate wrongly blamed: %+v", cond)
		}
	}
}

func TestWhyNotAggregateNoOuterToken(t *testing.T) {
	session, _ := whyNotSession(t, "whynot-aggregate-noouter", func(w *Workspace) map[string]TemplateKey {
		return whyNotGroupAggregateRule(t, w)
	})

	// No group fact at all: no outer token opens a bucket, so the failure is
	// upstream in the outer group condition, not the aggregate.
	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	if branch.FirstFailing != 0 {
		t.Fatalf("FirstFailing = %d, want 0 (the outer group condition)", branch.FirstFailing)
	}
	failing := branch.Conditions[0]
	if failing.Binding != "group" || failing.Reason != WhyNotReasonNoAlphaMatches {
		t.Fatalf("failing condition = %+v, want the group condition with no_alpha_matches", failing)
	}
}

func TestWhyNotAggregateNoOutputWithoutOuter(t *testing.T) {
	session, _ := whyNotSession(t, "whynot-aggregate-solo", func(w *Workspace) map[string]TemplateKey {
		item := mustAddTemplate(t, w, TemplateSpec{
			Name:            "item",
			DuplicatePolicy: DuplicateAllow,
			Fields:          []FieldSpec{{Name: "amount", Kind: ValueInt, Required: true}},
		}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: Accumulate(
				Match{Binding: "item", Target: TemplateKeyFact(item)},
				Min(BindingFieldExpr{Binding: "item", Field: "amount"}).As("min"),
			),
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"item": item}
	})

	// No items: Min produces no output, and with no outer grouping the aggregate
	// is itself the sole failing condition rather than a nonexistent condition 0.
	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	if branch.FirstFailing != 0 {
		t.Fatalf("FirstFailing = %d, want 0 (the sole aggregate condition)", branch.FirstFailing)
	}
	failing := branch.Conditions[branch.FirstFailing]
	if !failing.Aggregate {
		t.Fatalf("failing condition = %+v, want the Aggregate flag set", failing)
	}
	if failing.Reason != WhyNotReasonNoAlphaMatches {
		t.Fatalf("failing reason = %q, want %q", failing.Reason, WhyNotReasonNoAlphaMatches)
	}
}

func TestWhyNotJoinMismatch(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-join", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "x", Kind: ValueString, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "x", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b), JoinConstraints: []JoinConstraintSpec{
					{Field: "x", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "x"}},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "b": b}
	})

	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"x": "1"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	if _, err := session.Assert(context.Background(), keys["b"], mustFields(t, map[string]any{"x": "2"})); err != nil {
		t.Fatalf("Assert(b): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Binding != "b" || failing.Reason != WhyNotReasonJoinMismatch {
		t.Fatalf("failing condition = %+v, want b with join_mismatch", failing)
	}
	// Spec-built rules carry no source spans; span fidelity is exercised via
	// the .gess REPL drive in the rendering ticket.
	if len(branch.PartialMatches) == 0 {
		t.Fatalf("expected a nearest-miss partial match for the join")
	}
}

func TestWhyNotResidualPredicate(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-predicate", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "k", Kind: ValueString, Required: true}, {Name: "v", Kind: ValueInt, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "k", Kind: ValueString, Required: true}, {Name: "v", Kind: ValueInt, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b), JoinConstraints: []JoinConstraintSpec{
					{Field: "k", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "k"}},
				}},
				Test{Expression: CompareExpr{
					Operator: ExpressionCompareGreaterThan,
					Left:     BindingFieldExpr{Binding: "b", Field: "v"},
					Right:    BindingFieldExpr{Binding: "a", Field: "v"},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "b": b}
	})

	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"k": "j", "v": 5})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	if _, err := session.Assert(context.Background(), keys["b"], mustFields(t, map[string]any{"k": "j", "v": 5})); err != nil {
		t.Fatalf("Assert(b): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Reason != WhyNotReasonPredicate {
		t.Fatalf("failing reason = %q, want predicate_rejected (%+v)", failing.Reason, failing)
	}
}

func TestWhyNotNegationBlockedThenActivated(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-negation", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "host", Kind: ValueString, Required: true}}}).Key()
		alert := mustAddTemplate(t, w, TemplateSpec{Name: "alert", Fields: []FieldSpec{{Name: "host", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Not{Condition: Match{Binding: "alert", Target: TemplateKeyFact(alert), JoinConstraints: []JoinConstraintSpec{
					{Field: "host", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "host"}},
				}}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "alert": alert}
	})

	if _, err := session.Assert(context.Background(), keys["a"], mustFields(t, map[string]any{"host": "h1"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	blocker, err := session.Assert(context.Background(), keys["alert"], mustFields(t, map[string]any{"host": "h1"}))
	if err != nil {
		t.Fatalf("Assert(alert): %v", err)
	}

	report, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotBlocked {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotBlocked)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Reason != WhyNotReasonNegationBlocked {
		t.Fatalf("failing reason = %q, want negation_blocked", failing.Reason)
	}
	if len(failing.Blockers) != 1 || failing.Blockers[0] != blocker.Fact.ID() {
		t.Fatalf("blockers = %v, want [%v]", failing.Blockers, blocker.Fact.ID())
	}

	if _, err := session.Retract(context.Background(), blocker.Fact.ID()); err != nil {
		t.Fatalf("Retract(blocker): %v", err)
	}
	unblocked, err := session.WhyNot(context.Background(), "r")
	if err != nil {
		t.Fatalf("WhyNot after retract: %v", err)
	}
	if unblocked.Outcome != WhyNotActivated {
		t.Fatalf("Outcome after unblock = %q, want %q", unblocked.Outcome, WhyNotActivated)
	}
}

// TestWhyNotResidualJoinChain covers a rule where a residual (inequality) join
// compiles to two beta stages, so the beta chain is longer than the condition
// count. A following equality-joined condition that fails must still be
// identified (the old positional mapping produced FirstFailing = -1).
func TestWhyNotResidualJoinChain(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-residual", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "v", Kind: ValueInt, Required: true}}}).Key()
		b := mustAddTemplate(t, w, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "v", Kind: ValueInt, Required: true}, {Name: "w", Kind: ValueString, Required: true}}}).Key()
		c := mustAddTemplate(t, w, TemplateSpec{Name: "c", Fields: []FieldSpec{{Name: "w", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Match{Binding: "b", Target: TemplateKeyFact(b), JoinConstraints: []JoinConstraintSpec{
					{Field: "v", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "a", Field: "v"}},
				}},
				Match{Binding: "c", Target: TemplateKeyFact(c), JoinConstraints: []JoinConstraintSpec{
					{Field: "w", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "b", Field: "w"}},
				}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "b": b, "c": c}
	})
	ctx := context.Background()
	mustAssert := func(key TemplateKey, fields map[string]any) {
		if _, err := session.Assert(ctx, key, mustFields(t, fields)); err != nil {
			t.Fatalf("Assert: %v", err)
		}
	}
	mustAssert(keys["a"], map[string]any{"v": 5})
	mustAssert(keys["b"], map[string]any{"v": 10, "w": "x"}) // 10 > 5, b matches
	mustAssert(keys["c"], map[string]any{"w": "zzz"})        // zzz != x, c fails

	report, err := session.WhyNot(ctx, "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("outcome = %q, want never_matched", report.Outcome)
	}
	branch := singleBranch(t, report) // asserts FirstFailing is in range (not -1)
	if branch.Conditions[branch.FirstFailing].Binding != "c" {
		t.Fatalf("first failing = %q, want c (the failing equality join)", branch.Conditions[branch.FirstFailing].Binding)
	}
}

// TestWhyNotBlockerCountDistinct covers a single blocking fact that blocks two
// distinct partial matches: BlockerCount must count distinct facts, not pairs.
func TestWhyNotBlockerCountDistinct(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-blockers", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}, {Name: "host", Kind: ValueString, Required: true}}}).Key()
		alert := mustAddTemplate(t, w, TemplateSpec{Name: "alert", Fields: []FieldSpec{{Name: "host", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Not{Condition: Match{Binding: "alert", Target: TemplateKeyFact(alert), JoinConstraints: []JoinConstraintSpec{
					{Field: "host", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "host"}},
				}}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "alert": alert}
	})
	ctx := context.Background()
	// Two distinct a facts sharing host h1, each a separate partial match.
	if _, err := session.Assert(ctx, keys["a"], mustFields(t, map[string]any{"id": "a-1", "host": "h1"})); err != nil {
		t.Fatalf("Assert(a-1): %v", err)
	}
	if _, err := session.Assert(ctx, keys["a"], mustFields(t, map[string]any{"id": "a-2", "host": "h1"})); err != nil {
		t.Fatalf("Assert(a-2): %v", err)
	}
	if _, err := session.Assert(ctx, keys["alert"], mustFields(t, map[string]any{"host": "h1"})); err != nil {
		t.Fatalf("Assert(alert): %v", err)
	}

	report, err := session.WhyNot(ctx, "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if len(failing.Blockers) != 1 {
		t.Fatalf("distinct blockers = %d, want 1", len(failing.Blockers))
	}
	if failing.BlockerCount != 1 {
		t.Fatalf("BlockerCount = %d, want 1 (distinct facts, not left-row pairs)", failing.BlockerCount)
	}
}

// When more facts block a negation than WithWhyNotMaxBlockers allows, the
// listed Blockers are capped but BlockerCount reports the true total and the
// report is marked Truncated.
func TestWhyNotBlockerCapTruncates(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-blocker-cap", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "host", Kind: ValueString, Required: true}}}).Key()
		alert := mustAddTemplate(t, w, TemplateSpec{Name: "alert", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}, {Name: "host", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{
			Name: "r",
			ConditionTree: And{Conditions: []ConditionSpec{
				Match{Binding: "a", Target: TemplateKeyFact(a)},
				Not{Condition: Match{Binding: "alert", Target: TemplateKeyFact(alert), JoinConstraints: []JoinConstraintSpec{
					{Field: "host", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "host"}},
				}}},
			}},
			Actions: []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"a": a, "alert": alert}
	})
	ctx := context.Background()
	if _, err := session.Assert(ctx, keys["a"], mustFields(t, map[string]any{"host": "h1"})); err != nil {
		t.Fatalf("Assert(a): %v", err)
	}
	// Five distinct alert facts all block the single negation on host h1.
	for _, id := range []string{"al-1", "al-2", "al-3", "al-4", "al-5"} {
		if _, err := session.Assert(ctx, keys["alert"], mustFields(t, map[string]any{"id": id, "host": "h1"})); err != nil {
			t.Fatalf("Assert(%s): %v", id, err)
		}
	}

	report, err := session.WhyNot(ctx, "r", WithWhyNotMaxBlockers(2))
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if !report.Truncated {
		t.Fatalf("report.Truncated = false, want true when the blocker cap is hit")
	}
	branch := singleBranch(t, report)
	failing := branch.Conditions[branch.FirstFailing]
	if failing.Reason != WhyNotReasonNegationBlocked {
		t.Fatalf("failing reason = %q, want %q", failing.Reason, WhyNotReasonNegationBlocked)
	}
	if len(failing.Blockers) != 2 {
		t.Fatalf("listed Blockers = %d, want 2 (capped)", len(failing.Blockers))
	}
	if failing.BlockerCount != 5 {
		t.Fatalf("BlockerCount = %d, want 5 (true total despite the cap)", failing.BlockerCount)
	}
}

// An "or" rule compiles to more than one branch; WhyNot must report every
// branch in BranchID order and diagnose each independently. Only the branch
// closest to matching has a satisfied condition.
func TestWhyNotOrRuleReportsEveryBranch(t *testing.T) {
	session, keys := whyNotSession(t, "whynot-or", func(w *Workspace) map[string]TemplateKey {
		order := mustAddTemplate(t, w, TemplateSpec{Name: "order", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}, {Name: "status", Kind: ValueString, Required: true}}}).Key()
		item := mustAddTemplate(t, w, TemplateSpec{Name: "item", Fields: []FieldSpec{{Name: "order_id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		// Both or arms expose the same bindings (x, y) over the same templates,
		// differing only in the status constraint, so exactly one arm can match
		// a given order.
		arm := func(status string) ConditionSpec {
			return And{Conditions: []ConditionSpec{
				Match{Binding: "x", Target: TemplateKeyFact(order), FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: status},
				}},
				Match{Binding: "y", Target: TemplateKeyFact(item), JoinConstraints: []JoinConstraintSpec{
					{Field: "order_id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "x", Field: "id"}},
				}},
			}}
		}
		mustAddRule(t, w, RuleSpec{
			Name:          "r",
			ConditionTree: Or{Conditions: []ConditionSpec{arm("new"), arm("urgent")}},
			Actions:       []RuleActionSpec{{Name: "noop"}},
		})
		return map[string]TemplateKey{"order": order, "item": item}
	})
	ctx := context.Background()
	// A "new" order with no item: the "new" arm matches x but is missing y; the
	// "urgent" arm matches nothing.
	if _, err := session.Assert(ctx, keys["order"], mustFields(t, map[string]any{"id": "o-1", "status": "new"})); err != nil {
		t.Fatalf("Assert(order): %v", err)
	}

	report, err := session.WhyNot(ctx, "r")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.Outcome != WhyNotNeverMatched {
		t.Fatalf("Outcome = %q, want %q", report.Outcome, WhyNotNeverMatched)
	}
	if len(report.Branches) != 2 {
		t.Fatalf("Branches = %d, want 2 (an or rule has one branch per arm)", len(report.Branches))
	}
	if report.Branches[0].BranchID > report.Branches[1].BranchID {
		t.Fatalf("branches not sorted by BranchID: %d then %d", report.Branches[0].BranchID, report.Branches[1].BranchID)
	}
	// Exactly one branch (the "new" arm) has a satisfied condition; that branch
	// has a failing condition (the missing item). The other arm matched nothing.
	satisfied := 0
	for i := range report.Branches {
		branch := report.Branches[i]
		if branchHasSatisfied(branch) {
			satisfied++
			if branch.FirstFailing < 0 {
				t.Fatalf("closest branch has no failing condition: %+v", branch.Conditions)
			}
		}
	}
	if satisfied != 1 {
		t.Fatalf("branches with a satisfied condition = %d, want 1 (only the closest arm)", satisfied)
	}
}

func branchHasSatisfied(branch WhyNotBranch) bool {
	for _, cond := range branch.Conditions {
		if cond.Satisfied {
			return true
		}
	}
	return false
}

func TestWhyNotErrors(t *testing.T) {
	session, _ := whyNotSession(t, "whynot-errors", func(w *Workspace) map[string]TemplateKey {
		a := mustAddTemplate(t, w, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}}}).Key()
		mustAddAction(t, w, noopAction())
		mustAddRule(t, w, RuleSpec{Name: "r", ConditionTree: Match{Binding: "a", Target: TemplateKeyFact(a)}, Actions: []RuleActionSpec{{Name: "noop"}}})
		return map[string]TemplateKey{"a": a}
	})

	if _, err := session.WhyNot(context.Background(), "missing"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("WhyNot(missing) err = %v, want ErrRuleNotFound", err)
	}
	session.Close()
	if _, err := session.WhyNot(context.Background(), "r"); !errors.Is(err, ErrClosedSession) {
		t.Fatalf("WhyNot(closed) err = %v, want ErrClosedSession", err)
	}
}

// BenchmarkSessionWhyNot measures the read-only probe cost on a session with a
// join rule and many facts whose keys do not align (a wide near-miss frontier).
// WhyNot adds no hot-path state, so assert/modify/run throughput is unchanged
// when it is never called; this bounds the on-demand probe cost.
func BenchmarkSessionWhyNot(b *testing.B) {
	workspace := NewWorkspace()
	aKey := mustAddTemplate(b, workspace, TemplateSpec{Name: "a", Fields: []FieldSpec{{Name: "x", Kind: ValueString, Required: true}}}).Key()
	bKey := mustAddTemplate(b, workspace, TemplateSpec{Name: "b", Fields: []FieldSpec{{Name: "x", Kind: ValueString, Required: true}}}).Key()
	mustAddAction(b, workspace, noopAction())
	mustAddRule(b, workspace, RuleSpec{
		Name: "r",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "a", Target: TemplateKeyFact(aKey)},
			Match{Binding: "b", Target: TemplateKeyFact(bKey), JoinConstraints: []JoinConstraintSpec{
				{Field: "x", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "a", Field: "x"}},
			}},
		}},
		Actions: []RuleActionSpec{{Name: "noop"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("whynot-bench"))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	for i := range 64 {
		if _, err := session.Assert(ctx, aKey, mustFields(b, map[string]any{"x": "a" + itoa(i)})); err != nil {
			b.Fatalf("Assert(a): %v", err)
		}
		if _, err := session.Assert(ctx, bKey, mustFields(b, map[string]any{"x": "b" + itoa(i)})); err != nil {
			b.Fatalf("Assert(b): %v", err)
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.WhyNot(ctx, "r"); err != nil {
			b.Fatalf("WhyNot: %v", err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func singleBranch(t *testing.T, report WhyNotReport) WhyNotBranch {
	t.Helper()
	if len(report.Branches) != 1 {
		t.Fatalf("branches = %d, want 1", len(report.Branches))
	}
	branch := report.Branches[0]
	if branch.FirstFailing < 0 || branch.FirstFailing >= len(branch.Conditions) {
		t.Fatalf("FirstFailing = %d out of range (%d conditions)", branch.FirstFailing, len(branch.Conditions))
	}
	return branch
}
