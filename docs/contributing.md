# Developer guide

This guide covers the repository layout, the engine architecture, and the
conventions for tests, benchmarks, and documentation.

## Repository layout

- `rules/`, `session/`, `dsl/`: the public packages. Each is a thin
  re-export of `internal/engine` types and constructors, so the public
  import paths stay stable while the engine evolves. New public surface is
  added by aliasing it in these files.
- `internal/engine/`: nearly all implementation code, as one flat package
  with tests and benchmarks beside the implementation files.
- `internal/gesssexp/`: the S-expression lexer, parser, and canonical
  formatter behind the `.gess` language and `gessfmt`.
- `internal/fnvhash/`: hash primitives used for order-independent token
  and identity hashing.
- `cmd/gessc/`, `cmd/gessfmt/`: the command-line tools, thin wrappers over
  `dsl.GenerateGo` and `gesssexp.Format`.
- `examples/`, `tutorial/`: runnable examples and the workshop; see
  `examples.md`.
- `docs/`: this documentation set.

## Engine architecture

The compiled Rete graph is the only production matching runtime. Feature
gaps are closed with explicit graph node types and session-owned memories;
rule shapes the graph can't represent fail with an error wrapping
`ErrUnsupportedRuntime` rather than falling back to a slower path.

A map of `internal/engine` by subsystem:

- Definitions and compilation: `template.go` (templates, duplicate
  policies, backchain demand template synthesis), `rule.go` (rule specs
  and condition forms, including higher-order conditions), `ruleset.go`
  (workspace and compiled ruleset revisions), `condition.go` (condition
  plans), `branch_planning.go` (condition ordering and `or` branch
  planning), `query.go` (queries and query terminals).
- Expressions and predicates: `expression.go` (expression trees and
  alpha/beta-residual placement), `predicate.go` (field constraints),
  `path.go` (nested paths), `list_pattern.go` (list destructuring),
  `pure_function.go` (host pure functions).
- Rete graph: `rete_graph.go` (graph structure, alpha routing, node
  interning), `rete_graph_beta.go` (runtime beta memory and propagation),
  `rete_beta.go` (token rows and identity hashing),
  `rete_graph_join_outputs.go` and `rete_graph_token_bucket_table.go`
  (join output and bucket storage), `rete_graph_negative_beta.go`
  (blocker-count negation), `rete_graph_aggregate.go` and `aggregate.go`
  (aggregates), `rete_graph_terminal_memory.go` (terminal token memory),
  `rete_runtime.go` (revision-to-graph bridge), `fact_field_index.go`
  (alpha field indexes).
- Session runtime: `session.go` (lifecycle, mutations, queuing),
  `run.go` (the run driver), `agenda.go` (module queues and conflict
  resolution), `focus.go` (focus stack and auto-focus), `module.go`,
  `fact.go` (working-memory facts), `mutation.go` (result types),
  `snapshot.go`, `event.go`, `action.go` (action contexts),
  `logical_support.go` (truth maintenance), `backchain_demand_support.go`
  and `backchain_query_proof.go` (backward chaining),
  `runtime_diagnostics.go` and `propagation_counter.go`
  (instrumentation), `value.go`, `errors.go`, `id.go`.
- The `.gess` language: `gess_dsl.go` and `gess_dsl_parse.go` (loader and
  parser), `gess_generate.go` (Go code generation for `gessc`).

## Development workflow

Run Go commands from the module root. After each implementation step:

```sh
gofmt -w <touched-go-files>
go fix ./...
go test ./...
```

Style expectations:

- Idiomatic Go; small interfaces defined near consumers; explicit errors
  with wrapping and sentinels where callers need `errors.Is` or
  `errors.As`; `context.Context` on operations that run, block, or call
  user-provided behavior.
- Commits use Conventional Commit messages, such as
  `feat(memory): add fact identity model`.

## Tests

`go test ./...` is the normal verification command. Conventions in
`internal/engine`:

- Contract and table-driven tests cover rule-engine semantics;
  `semantic_scenario_test.go` holds end-to-end scenarios asserting fired
  action traces.
- `matcher_oracle_test.go` defines a brute-force oracle matcher, and
  parity helpers assert that the Rete graph agrees with it. New matching
  behavior should extend oracle parity coverage.
- Fixture runners (Miss Manners in `manners_runner_test.go`, claims
  triage, loan underwriting, and the scaling runners) double as
  correctness tests and as opt-in performance harnesses gated by
  `GESS_*_RUNNER` environment variables, with knobs for iterations, fact
  counts, and profiles.
- `reconcile_path_inventory_test.go` is a governance test: every
  whole-terminal reconcile path must be enumerated, which guards against
  reintroducing steady-state reconciliation.

Behavior changes come with new or updated tests, preferring contract-style
and table-driven forms.

## Benchmarks

Benchmark files are named `*_benchmark_test.go`. Run them with:

```sh
go test -bench=. -benchmem ./internal/engine/
```

Add benchmarks only where performance claims or regressions matter:
propagation fanout, bucket probes, row scans, memory growth, modify cost,
retract cost, and agenda deltas all have precedents to follow.

## Documentation workflow

Markdown in this repository is checked with Vale, configured by
`.vale.ini`: the Google style at warning level, with project terms
accepted through the vocabulary in
`.vale/styles/config/vocabularies/Gess/accept.txt`.

After editing any Markdown file:

```sh
vale <changed .md files>
```

Fix findings rather than weakening `.vale.ini`. Add a term to the
vocabulary only when it's a genuine project or domain term, such as a
product name or a rule-engine term of art. Keep code identifiers, flags,
and `.gess` forms in backticks, which Vale ignores as code.

When documentation changes commands, snippets, or generated-code
instructions, run the affected example tests, or `go test ./...` for
broad changes.
