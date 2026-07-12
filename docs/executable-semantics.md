# Executable semantics

This document defines the matching and lifecycle behavior that Gess treats as
its semantic contract. The contract is executable: the engine's fuzz harness
generates bounded rule and fact corpora, executes their lifecycle through the
production Rete graph, and compares terminal matches and agenda order with the
independent matcher oracle. It also checks that identical histories produce
byte-identical explain JSON.

## Values and expression evaluation

Values have one of the declared Gess kinds. Integer and floating-point values
compare numerically; other ordered comparisons require compatible kinds.
Equality is kind-aware except for numeric equivalence. Lists compare by their
elements. `null` is a value; a missing field or path is not.

| Evaluation | Result |
| --- | --- |
| Boolean expression evaluates to `true` | condition passes |
| Boolean expression evaluates to `false` | condition does not pass |
| Referenced field or path is missing | evaluation error |
| Operator receives an incompatible kind | evaluation error |
| Pure function returns an error | evaluation error |
| Evaluation error inside `not`, `exists`, or `forall` | error propagates; it is not converted to a match or non-match |

`has-path` is the explicit way to test presence without producing a
missing-path error. Rule predicates and standalone `test` conditions must
evaluate to booleans. Evaluation is deterministic and pure functions must be
side-effect free.

## Condition forms

Each condition consumes zero or more partial tuples and emits zero or more new
tuples. Bindings created inside `not`, `exists`, and `forall` are local to that
condition and do not escape it.

| Form | Match semantics |
| --- | --- |
| `match` | Emits one tuple for every fact of the target name or template that satisfies field constraints, joins, list patterns, and predicates. A fact may participate in more than one binding unless constraints exclude it. |
| `and` | Evaluates children left to right for meaning; a tuple survives only when every child succeeds. The compiler may reorder independent positive matches without changing results. |
| `or` | Emits the union of its branches in authored branch order. Identical terminal tuples are deduplicated. Every branch must export a compatible binding contract. |
| `not C` | Emits its input tuple exactly once when `C` has no match for that tuple; otherwise emits none. |
| `exists C` | Emits its input tuple exactly once when `C` has at least one match; the number of matches does not multiply the outer tuple. |
| `forall D R` | Emits its input tuple when every tuple in domain `D` has at least one matching requirement `R`. An empty domain succeeds. |
| `test E` | Emits its input tuple when boolean expression `E` is true. |
| `accumulate C A...` | Evaluates `C` for each outer tuple and emits one value binding per aggregate. `count` is zero on an empty group; `sum`, `min`, `max`, and `collect` follow their documented empty-group and kind validation rules. Group identity includes the outer tuple. |

A complete terminal tuple becomes one activation per rule revision and fact or
value binding tuple. Assert, retract, and modify update those tuples through
graph propagation; production matching never falls back to the oracle.

## Ordering guarantees

The focus stack chooses the active module. Within that module, activations are
ordered by higher salience, then match recency, then deterministic authored
tuple and declaration order. A fixed ruleset, session ID, mutation history,
and host behavior therefore produce the same firing and event order.

Query rows and aggregate collections are deterministic for a fixed session
history, but callers that need a domain-specific order must sort explicitly.
Machine-readable derivation, why-not, and what-if reports are byte-identical
for equal inputs at the same explain schema version.

## Lifecycle semantics

| Operation | Semantic effect |
| --- | --- |
| Assert | Validates and applies the template's duplicate policy, assigns identity and recency, propagates an add through alpha and beta memories, and adds newly complete terminal tuples to the agenda. |
| Modify | Keeps `FactID`, advances version and recency when fields change, and propagates the precise removal/change/add effects. A no-op patch changes nothing. Pending activations that no longer match are removed. |
| Retract | Removes stated support or the fact itself according to its support state, propagates removal, removes dependent activations, and cascades unsupported logical facts. |
| Run | Fires eligible activations in agenda order until quiescence, halt, limit, cancellation, or error. Mutations from an action propagate before the next activation is selected. |
| Reset | Advances the generation, clears working memory, agenda, focus, demand, and logical support, reasserts initial facts, and rebuilds graph-owned memories by propagation. Old fact IDs become stale. |
| ApplyRuleset | Keeps live facts only when template compatibility holds. An unchanged declaration rebinds host closures without rebuilding runtime state; a changed compatible revision purges invalid logical support and rebuilds graph memories and agenda through lifecycle deltas. |
| Query | Reads graph terminal memory. Backward-chaining queries may create scoped demand and run only proof-origin activations; ordinary unrelated agenda entries remain pending. |
| WhatIf | Runs on an isolated fork and reports its diff. Unless explicitly retained, the fork is closed and the base session is unchanged. |

Sessions have one logical owner. Unsupported graph shapes fail with
`ErrUnsupportedRuntime`; they are never made to appear supported by a
rule-local matcher or full reconciliation path.

## Verification harness

`FuzzExecutableSemantics` in `internal/engine` derives template definitions,
rule shapes, facts, and mutations from fuzz bytes. Its seed corpus covers
positive constraints, joins, negation, disjunction, `exists`, and aggregates.
After every assert, modify, and retract it compares the graph's complete match
candidates and agenda ordering against the brute-force oracle. It then replays
the same history in a second session and compares the combined derivation,
why-not, and what-if JSON byte for byte.

Run the seed corpus with normal tests, or fuzz it directly:

```sh
go test ./internal/engine -run=FuzzExecutableSemantics
go test ./internal/engine -run=^$ -fuzz=FuzzExecutableSemantics -fuzztime=30s
```
