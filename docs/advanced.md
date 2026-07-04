# Advanced behavior

This guide covers the runtime machinery behind the concepts in
`concepts.md`: the Rete graph, expression predicate placement, aggregates,
higher-order conditions, logical support, backward chaining, and module
focus.

## The Rete runtime

The compiled Rete graph is the only production matching runtime. Compiling
a ruleset builds a graph of:

- Alpha nodes: per-fact filters routed by template, then by literal field
  constraints. Equality constraints become indexed routes, so a fact only
  reaches the alpha memories it can match.
- Beta nodes: joins between the partial matches on the left and facts on
  the right. Equality join constraints compile to indexed hash joins;
  other comparisons and residual predicates evaluate per candidate pair.
- Negative nodes: `not` conditions maintain a blocker count per left row;
  a row produces output only while its count is zero.
- Aggregate nodes: maintain per-group aggregate values incrementally.
- Terminal nodes: rule endpoints that feed the agenda and query endpoints
  that produce rows.

Asserts, modifies, and retracts propagate through the graph as deltas.
Tokens (partial matches) carry a commutative identity hash so removals
resolve without scanning memories again. The agenda updates from terminal
deltas: new tokens add activations, removed tokens cancel pending ones.

Within a module, activations fire in a deterministic order: higher
salience first, then higher recency (more recently touched facts), then
earlier declaration order, with stable tie-breaking after that.

`Session.RuntimeDiagnostics(ctx)` reports per-owner memory statistics
(fact, alpha, beta, query-terminal, agenda, and aggregate owners) with row,
bucket, index, tombstone, and byte counts for capacity monitoring.

Rule shapes the graph can't represent fail compilation or session
construction with an error wrapping `rules.ErrUnsupportedRuntime` rather
than falling back to a slower matcher.

## Expression predicate placement

The compiler classifies every expression predicate on a condition:

- Alpha placement: the expression reads only the condition's own fact. It
  runs once per fact before any joins, and simple comparisons are lowered
  further into field constraints that can use indexes.
- Beta-residual placement: the expression reads bindings from earlier
  conditions, so it must run after the join, once per joined token.

Placement is automatic and can be inspected through
`rules.ExpressionPredicate.Placement()`. The performance implication:
filters that read one fact are cheap and often indexed, while
cross-binding comparisons pay a per-join-result cost. Where possible,
phrase constraints against a single fact, and prefer equality joins, which
use hash indexes.

Conjunctions split: in an `and` expression, single-fact conjuncts hoist to
alpha placement even when a sibling conjunct stays residual.

## Aggregates

`accumulate` conditions group by the partial match from the preceding
conditions: each combination of earlier bindings gets its own aggregate
bucket over the facts matching the input condition. Empty groups are
valid — `count` reports zero and `collect` reports an empty list.

Maintenance is incremental. Asserting a matching fact folds into the
bucket; retracting subtracts where that's sound (counts and integer sums)
and rebuilds the bucket where it isn't (minimum and maximum when the
departing value ties the current extreme, float sums, and `collect`). A bucket
only re-emits downstream when its value actually changes.

`collect` results are deterministically ordered, but not in insertion
order. Aggregates aren't allowed under `not`, and a standalone `test` over
an aggregate result isn't supported; compare aggregate results in actions
or downstream facts instead.

## Higher-order conditions

- `not` matches while no fact satisfies its condition, maintained by
  blocker counts. Bindings inside a `not` are local.
- `exists` matches while at least one fact satisfies its condition and
  produces exactly one activation regardless of how many facts match.
  Contributor churn that doesn't change the truth value produces no agenda
  churn.
- `forall(domain, requirement)` matches while every fact matching the
  domain also has a matching requirement fact. It's vacuously true when
  the domain is empty. Internally it compiles to counterexample negation:
  no domain match without its requirement.

Limits, enforced at compile time with errors wrapping
`rules.ErrInvalidHigherOrderCondition`: `exists` and `forall` can't appear
under `not` or as an `or` branch; the domain must be a positive
conjunction of matches; a `forall` requirement is at most one positive
match plus tests. Bindings inside `exists` and `forall` don't escape.

## Logical support and truth maintenance

`AssertLogical` (from an action context, or `assert-logical` in `.gess`)
asserts a fact whose justification is the asserting activation's matched
facts. The session records a support edge per asserting activation.

- A fact can be stated, logical, or both. Asserting an existing logical
  fact adds stated support; retracting a stated-and-logical fact removes
  only the stated support.
- When a supporting match goes away, because a supporting fact was
  retracted or modified out of the match, the support edge is removed. A fact whose
  last logical support disappears and that has no stated support is
  retracted automatically, and that retraction cascades through any facts
  it supported in turn.
- Stated support is sticky: losing logical support never retracts a stated
  fact.
- Directly retracting a logical-only fact fails with
  `ErrLogicalOnlyRetract`; modifying any fact with logical support fails
  with `ErrLogicalFactModify`. Change the supporting facts instead.

Inspect the state with `Snapshot.SupportGraph()`, which returns the edges
(with rule, activation, and supporting fact identities) and counters,
including cascade retraction totals and cascade depth. The
`EventLogicalSupportAdded` and `EventLogicalSupportRemoved` events track
edge lifecycle. `Reset` clears all logical support.

## Backward chaining

Backward chaining makes rules prove facts on demand instead of eagerly.

1. Declare a template backchain-reactive with
   `(declare (backchain-reactive TRUE))` in `.gess` or
   `BackchainReactive: true` in a `TemplateSpec`.
   Compilation synthesizes a demand template named `need-<template>` with
   the same slots.
2. Write proof rules that match the demand fact and assert the real fact:

   ```cl
   (defrule direct-reachability
     ?need <- (need-reachable (src ?src) (dst ?dst))
     (edge (src ?src) (dst ?dst))
     =>
     (assert (reachable (src ?src) (dst ?dst))))
   ```

3. When a join needs a `reachable` fact that doesn't exist, the runtime
   generates a `need-reachable` demand with the slot values it can
   determine from the join; unknown slots stay unconstrained. Proof rules
   can themselves demand further facts, so transitive and recursive proofs
   work.

Session queries drive demand too: `Query` and `QueryAll` against
backchain-reactive templates inject the query's constraints as demand,
run the proof rules, answer from the results, and clean up the proof
state. Snapshot queries don't generate demand.

Conditions wrapped in `rules.Explicit{...}` and negated conditions never
generate demand. `Snapshot.BackchainDemandDiagnostics()` reports active
demand counts per template. The `examples/backward-chaining` examples show
the pattern end to end.

## Modules and the focus stack

Every rule belongs to a module; the agenda is partitioned per module. The
session's focus stack (initially `[MAIN]`) selects which partition fires:

- `Run` draws activations only from the module on top of the stack. When
  that module's agenda empties, the frame pops automatically and the run
  continues with the module below; an empty stack falls back to `MAIN`.
- Activations in a non-`MAIN` module that never gains focus stay pending
  across runs.
- Auto-focus, declared per rule or as a module default, pushes the
  module onto the stack the moment one of the rule's activations enters
  the agenda.
- Applications control focus with `PushFocus`, `SetFocus`, `PopFocus`, and
  `ClearFocusStack` on the session; actions use the same methods on the
  action context or the `.gess` `focus`, `pop-focus`, and `clear-focus`
  actions. Focus changes from actions affect the very next activation
  selection.
- `Reset` restores the initial focus stack.

Use modules to phase work, for example an intake module that normalizes
facts followed by a response module that acts on them, with rules or the
host pushing focus between phases. The `examples/modules-focus` example shows
the pattern.

## Next steps

- [Examples map](examples.md) for runnable examples of each mechanism
  covered here.
- [Session lifecycle](session-lifecycle.md) for the host-facing API these
  mechanisms build on.
- [Developer guide](contributing.md) for the engine's internal
  architecture.
