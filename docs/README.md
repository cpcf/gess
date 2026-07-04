# Gess documentation

Start with the repository `../README.md` for a quick start and
`TUTORIAL.md` for the preferred `.gess` plus `gessc` workflow. The
guides here go deeper:

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
- [Examples map](examples.md): what each example demonstrates and where to
  start.
- [Developer guide](contributing.md): repository layout, engine
  architecture, tests, benchmarks, and the documentation workflow.
