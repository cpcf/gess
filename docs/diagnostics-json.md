# Machine-readable runtime diagnostics

`Session.Diagnostics` reports the current Rete runtime state as a typed Go
value and a versioned JSON document. It is intended for operational tools,
benchmarks, workbenches, and agent integrations that need runtime state without
depending on engine internals.

```go
report, err := session.Diagnostics(ctx)
if err != nil {
    return err
}
raw, err := json.Marshal(report)
```

The default report contains counts and retained-memory estimates. It does not
clone every fact payload. Fact identity, fields, presence, and support can be
requested explicitly when they are needed:

```go
report, err := session.Diagnostics(ctx, sess.WithDiagnosticsFacts())
```

As with other session inspection calls, diagnostics cannot run concurrently
with an active `Run` and can return `ErrConcurrencyMisuse`.

## Envelope and versioning

Every document carries `gessDiagnosticsSchema`. Its current value is available
as `session.DiagnosticsSchemaVersion`:

```json
{
  "gessDiagnosticsSchema": 1,
  "session": {
    "sessionId": "orders",
    "rulesetId": "sha256:...",
    "generation": 1,
    "factCount": 24
  },
  "graph": {
    "runtime": "rete",
    "alphaNodes": 8,
    "betaNodes": 4,
    "unionNodes": 2,
    "aggregateNodes": 1,
    "ruleTerminals": 3,
    "queryTerminals": 1,
    "ruleBranches": 3,
    "queryBranches": 1
  }
}
```

The version is bumped for a breaking change, such as removing or renaming a
field or changing its meaning. Additive fields keep the same version. Consumers
must ignore unknown fields. Reports are export-only; there is no decoder back
into a session.

## Sections

- `session` identifies the session, compiled ruleset, working-memory
  generation, and fact count.
- `graph` summarizes the immutable compiled Rete graph.
- `memory` reports retained state by owner: fact, alpha, beta, query terminal,
  agenda, aggregate, and backchain demand support. Owners with no retained
  storage may be absent.
- `agenda` reports pending activations, readiness, conflict strategy, and focus.
- `terminals` separates rule terminal nodes from query terminal nodes and rows.
- `aggregates` reports compiled aggregate nodes and retained aggregate memory.
- `queries` reports compiled definitions, transient proof state, and retained
  terminal rows.
- `truthMaintenance` reports current logical support plus lifetime edge and
  cascade counters.
- `backchain` reports active demand facts, support rows, cascade configuration,
  and lifetime cascade counters.
- `facts` is omitted unless `WithDiagnosticsFacts` is supplied. When present,
  it follows fact insertion order and contains detached field values.

## Field stability

Stable fields describe public runtime concepts and retain their meaning for a
schema version. This includes the envelope, session identity and counts, graph
node and branch counts, agenda state, terminal counts, query state,
truth-maintenance counts, backchain counts, memory owner names, and opt-in fact
details.

The following numeric measurements are **debug-only**: every memory owner's
`rows`, `buckets`, `indexes`, `tombstones`, `bytes`, and `highWater`, and the
corresponding aggregate memory measurements. They are useful for comparison
within one Gess build, but their exact values may change after a non-breaking
storage refactor. `bytes` is an estimate of retained storage, not a heap
measurement.

`agenda.ready`, `agenda.dirty`, and `queries.activeProof` are
**experimental** lifecycle observations. Their fields remain present for schema
version 1, but consumers should not treat a particular intermediate state as a
cross-version protocol guarantee.

IDs encode as their string forms. JSON object key order is deterministic under
Go's standard encoder, while arrays use deterministic engine order. Empty
sections remain present so consumers can distinguish a supported section with
zero state from an absent future capability.
