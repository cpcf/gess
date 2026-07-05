# Core concepts

Gess is a forward-chaining rules engine built on a Rete network. This page
defines the vocabulary the rest of the documentation uses. For hands-on
guides, see `TUTORIAL.md`, `go-api.md`, and `session-lifecycle.md`.

## Templates

A template declares the shape of a kind of fact: a name plus typed slots
(fields), each optionally required or defaulted. Templates also carry
policy: how duplicate facts are handled and whether the template
participates in backward chaining. Declare templates in `.gess` with
`deftemplate` or in Go with `rules.TemplateSpec`.

Facts can also be asserted without a template, as dynamic facts identified
by name alone; templates add validation and stronger matching guarantees.

## Facts

A fact is one unit of working memory: a named collection of field values.
Facts are immutable snapshots from the caller's point of view; changing one
means asking the session to `Modify` it, which produces a new version.

Each fact has:

- A `FactID`, stable across modifies within a generation.
- A `FactVersion` and recency, which advance on each modify.
- A `Generation`, which advances when the session resets; IDs from earlier
  generations become stale.
- A support state: stated (asserted by host code), logical (asserted by a
  rule with logical support), or both.

## Rules

A rule pairs conditions with actions. Its left-hand side describes the
facts that must (or must not) exist; its right-hand side lists actions to
execute when the conditions match. Conditions can bind facts and field
values to names, join facts on field equality, evaluate expressions,
aggregate over groups of facts, and quantify with `not`, `exists`, and
`forall`.

Rules carry salience (a priority within a module) and can declare
auto-focus (their module takes focus when they activate).

## Activations and the agenda

When working memory changes, the Rete network incrementally computes which
rules match which combinations of facts. Each complete match becomes an
activation: a rule paired with the specific facts that satisfied it.

Activations wait on the agenda until `Run` fires them. The agenda is
partitioned by module, and within a module activations fire in a
deterministic order: higher salience first, then more recent matches, then
declaration order. Retracting or modifying a fact that a pending activation
depends on removes the activation before it can fire.

## Sessions

A session is the mutable runtime for one compiled ruleset. It owns working
memory, the agenda, the focus stack, logical support, and event delivery.
Host code asserts, modifies, and retracts facts, calls `Run` to fire rules
until quiescence, executes queries, and takes snapshots. See
`session-lifecycle.md`.

:::caution
A session has one logical owner. Overlapping calls from other goroutines
fail fast rather than blocking, so a session isn't safe to share across
goroutines the way many Go runtime objects are.
:::

## Compiled rulesets

A ruleset is an immutable compiled revision of a workspace: templates,
rules, queries, actions, and pure functions, compiled into a Rete graph
plan. Rulesets are safe to share across sessions. A running session can
swap to a newly compiled revision with `ApplyRuleset` while keeping its
facts.

:::note
`ApplyRuleset` only succeeds if the templates used by the session's live
facts are unchanged between the old and new revision; otherwise it fails
with `ErrIncompatibleRuleset`.
:::

This separation into definitions in a workspace, immutable compiled
rulesets, and mutable session state is the core structure of the API.

## Queries

A query is a named, compiled question over working memory: conditions like
a rule's left-hand side, plus declared parameters and named return values.
Queries return rows of facts and computed values. They're the intended way
for host code to read rule-engine results, rather than scanning
snapshots. Queries against backchain-reactive templates also drive
backward chaining on demand.

## Modules and focus

Rules, templates, and queries belong to modules (default `MAIN`). The
session's focus stack decides which module's activations fire during a
run; when the focused module's agenda empties, the stack pops and the run
continues below. Modules structure large rulesets into phases. See
`advanced.md`.

## Actions and host integration

Rule actions run Go code through a `rules.ActionContext`, which exposes
the matched bindings and the session mutation API. From `.gess` files,
actions are a fixed vocabulary — `assert`, `assert-logical`, `retract`,
`modify`, `bind`, `emit`, focus control, `halt`, and `call` — with `call`
dispatching to host functions registered through `dsl.Registry`. A curated
set of built-in functions (arithmetic, string, and type predicates) and
host-registered pure functions extend the expression language with
deterministic computations. Right-hand-side control flow stays host-only.

## The Rete runtime

Matching is incremental: the compiled ruleset becomes a graph of alpha
nodes (per-fact filters), join nodes (combining facts on shared values),
negation and aggregate nodes, and terminal nodes (rule and query
endpoints). Asserts, modifies, and retracts propagate as deltas through
this graph, so match cost scales with the size of the change rather than
the size of working memory. See `advanced.md` for a deeper tour.

## Next steps

- [The `.gess` language reference](gess-language.md) or the [Go API
  guide](go-api.md) to start writing templates and rules.
- [Session lifecycle](session-lifecycle.md) for the full runtime API.
- [Advanced behavior](advanced.md) for the Rete runtime, aggregates, and
  module focus in depth.
