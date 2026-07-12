# Developer guide

This guide covers the repository layout, the engine architecture, and the
conventions for tests, benchmarks, and documentation.

## Repository layout

- `rules/`, `session/`, `dsl/`: the public packages. `rules` owns the
  public rule-definition values, workspace facade, and compiled ruleset
  facade; `session` explicitly constructs engine-backed workspaces and exposes
  the runtime facade, while `dsl` exposes the loader facade.
  Keep public import paths stable while the engine evolves, and avoid
  exposing new engine internals directly.
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
- Session runtime: `session.go` (lifecycle, mutations, queuing) coordinates
  named session owners: `session_fact_store.go` (working memory),
  `session_propagation.go` (Rete runtime and terminal-delta lifecycle),
  `session_agenda_driver.go` (agenda, strategy, and focus),
  `session_tms_store.go` and `session_backchain_store.go` (support state),
  and `session_diagnostics_exporter.go` (events and explain history).
  `run.go` drives firing; `agenda.go` implements module queues and conflict
  resolution; `fact.go`, `mutation.go`, `snapshot.go`, `event.go`, and
  `action.go` provide the runtime values and boundaries. Truth maintenance
  and backward chaining remain implemented in `logical_support.go`,
  `backchain_demand_support.go`, and `backchain_query_proof.go`.
  `runtime_diagnostics.go` and `propagation_counter.go` provide
  instrumentation. Public values, errors, and identifiers are declared in
  `rules`; the corresponding engine files retain compiler and runtime helpers.
- The `.gess` language: `gess_dsl.go` and `gess_dsl_parse.go` (loader and
  parser), `gess_generate.go` (Go code generation for `gessc`).

### Session ownership and graph invariants

Each mutable session subsystem has one named owner. `Fork` shares the
immutable compiled ruleset, deep-clones fact, agenda, focus, and TMS state,
rebuilds mutable Rete and backchain ownership, and starts run/proof scratch
fresh. Event listeners and explain history are configured fresh; event
sequence continuity and the documented output-writer reference carry over.
`session_fork_ownership_test.go` recursively inventories `Session` and every
named `session*` owner. Adding or moving a field requires an explicit fork
ownership policy and rationale.

Several internal contracts are load-bearing:

- Every `or` branch exposes identical binding names and templates.
- Large Cartesian `or` products compile to support-counted union stages; the
  bounded public branch list is inspection data, never an execution path.
- `conditionPlans` remain in planned execution order. Public condition order
  is resolved through `bindingSlot`, never by positional indexing with
  `condition.Order`.
- Compiled condition-tree paths remain authored paths. Physical planning must
  not rewrite them; rule and ruleset identities are plan-independent.
- Activation identity is the binding tuple, including its fact identities and
  versions; display paths are not identity.
- Terminal and agenda deltas have one owner. A delta that does not report
  owned storage may alias transient Rete arenas and must be applied or cloned
  before that arena is released.

Keep these contracts explicit when adding graph nodes, changing planning, or
extending session lifecycle transitions.

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
- `executable_semantics_fuzz_test.go` generates bounded template, rule, fact,
  and lifecycle corpora, checks graph-vs-oracle parity after every mutation,
  and verifies byte-identical explain JSON for equal histories.
- Fixture runners (Miss Manners in `manners_runner_test.go`, claims
  triage, loan underwriting, and the scaling runners) double as
  correctness tests and as opt-in performance harnesses gated by
  `GESS_*_RUNNER` environment variables, with knobs for iterations, fact
  counts, and profiles.
- `reconcile_path_inventory_test.go` is a governance test: production has no
  whole-terminal reconcile path; retained parity helpers must stay in test
  files and remain explicitly classified.
- Initial construction, `Reset`, and `ApplyRuleset` build graph memory through
  propagation events and update the agenda from owned terminal lifecycle
  deltas. A missing lifecycle delta is `ErrUnsupportedRuntime`, never a signal
  to rematch all terminals.

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
