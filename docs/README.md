# Gess documentation

Gess is a Go rules engine with a Rete-based runtime and a `.gess` file
format for defining templates, seed facts, rules, and queries outside app
code.

## Start here

New to Gess? Follow this path:

1. Write templates, facts, rules, and queries in a `.gess` file. See
   [the `.gess` language reference](gess-language.md) or jump straight into
   [the tutorial](TUTORIAL.md).
2. Generate Go with `gessc`. See [command-line tools](cli.md).
3. Build a session from the generated ruleset and run it. See the
   [Go API guide](go-api.md) and [session lifecycle](session-lifecycle.md).
4. Read results back with queries. See
   [session lifecycle](session-lifecycle.md#queries).

`TUTORIAL.md` walks through all four steps with one worked example,
`examples/gess-files/order_routing`.

## Guides

Once the basics are working, these guides go deeper:

- [Core concepts](concepts.md): templates, facts, rules, activations, the
  agenda, sessions, rulesets, and queries.
- [The `.gess` language reference](gess-language.md): every form the
  `.gess` parser accepts, with limits and errors.
- [Go API guide](go-api.md): building templates, rules, queries, actions,
  and pure functions with the `rules`, `session`, and `dsl` packages.
- [Session lifecycle](session-lifecycle.md): assert, modify, retract,
  reset, run, queries, snapshots, events, the focus stack, and
  `ApplyRuleset`.
- [Command-line tools](cli.md): `gessc` and `gessfmt`.
- [Advanced behavior](advanced.md): the Rete runtime, expression predicate
  placement, aggregates, higher-order conditions, logical support,
  backward chaining, and module focus.
- [Examples map](examples.md): what each example demonstrates, organized
  by feature, and where to start.
- [Developer guide](contributing.md): repository layout, engine
  architecture, tests, benchmarks, and the documentation workflow.
