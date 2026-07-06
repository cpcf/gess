package engine

import (
	"context"
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

	if _, err := session.AssertTemplate(context.Background(), keys["a"], mustFields(t, map[string]any{"id": "a-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
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

	if _, err := session.AssertTemplate(context.Background(), keys["a"], mustFields(t, map[string]any{"id": "a-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
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

	if _, err := session.AssertTemplate(context.Background(), keys["a"], mustFields(t, map[string]any{"x": "1"})); err != nil {
		t.Fatalf("AssertTemplate(a): %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), keys["b"], mustFields(t, map[string]any{"x": "2"})); err != nil {
		t.Fatalf("AssertTemplate(b): %v", err)
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

	if _, err := session.AssertTemplate(context.Background(), keys["a"], mustFields(t, map[string]any{"k": "j", "v": 5})); err != nil {
		t.Fatalf("AssertTemplate(a): %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), keys["b"], mustFields(t, map[string]any{"k": "j", "v": 5})); err != nil {
		t.Fatalf("AssertTemplate(b): %v", err)
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

	if _, err := session.AssertTemplate(context.Background(), keys["a"], mustFields(t, map[string]any{"host": "h1"})); err != nil {
		t.Fatalf("AssertTemplate(a): %v", err)
	}
	blocker, err := session.AssertTemplate(context.Background(), keys["alert"], mustFields(t, map[string]any{"host": "h1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(alert): %v", err)
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
		if _, err := session.AssertTemplate(ctx, key, mustFields(t, fields)); err != nil {
			t.Fatalf("AssertTemplate: %v", err)
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
	if _, err := session.AssertTemplate(ctx, keys["a"], mustFields(t, map[string]any{"id": "a-1", "host": "h1"})); err != nil {
		t.Fatalf("AssertTemplate(a-1): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, keys["a"], mustFields(t, map[string]any{"id": "a-2", "host": "h1"})); err != nil {
		t.Fatalf("AssertTemplate(a-2): %v", err)
	}
	if _, err := session.AssertTemplate(ctx, keys["alert"], mustFields(t, map[string]any{"host": "h1"})); err != nil {
		t.Fatalf("AssertTemplate(alert): %v", err)
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
		if _, err := session.AssertTemplate(ctx, aKey, mustFields(b, map[string]any{"x": "a" + itoa(i)})); err != nil {
			b.Fatalf("AssertTemplate(a): %v", err)
		}
		if _, err := session.AssertTemplate(ctx, bKey, mustFields(b, map[string]any{"x": "b" + itoa(i)})); err != nil {
			b.Fatalf("AssertTemplate(b): %v", err)
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
