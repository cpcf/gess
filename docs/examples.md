# Examples map

Every leaf example under `examples/` is a `package main` with a
`run(io.Writer)` function and a test, so each works two ways:

```sh
go run ./examples/<path>
go test ./examples/<path>
```

Run everything with `go test ./examples/...`. Most examples build rules
with the pure Go API; `examples/gess-files` and the tutorial workshop use
`.gess` files compiled with `gessc`.

## Examples by feature

| Goal | Start here |
| --- | --- |
| Learn the preferred `.gess` workflow | [`gess-files/order_routing`](https://github.com/cpcf/gess/tree/main/examples/gess-files/order_routing) |
| Derive facts from other facts in Go | [`forward-chaining/order_routing`](https://github.com/cpcf/gess/tree/main/examples/forward-chaining/order_routing) |
| Read results with parameterized queries | [`queries/account_lookup`](https://github.com/cpcf/gess/tree/main/examples/queries/account_lookup) |
| Require that a blocking fact is absent | [`negation/customer_screening`](https://github.com/cpcf/gess/tree/main/examples/negation/customer_screening) |
| Count, sum, or collect over fact groups | [`aggregates/fraud_velocity`](https://github.com/cpcf/gess/tree/main/examples/aggregates/fraud_velocity) |
| Derived facts that retract with their support | [`logical-support/remediation_tickets`](https://github.com/cpcf/gess/tree/main/examples/logical-support/remediation_tickets) |
| Prove facts on demand | [`backward-chaining/incident_response`](https://github.com/cpcf/gess/tree/main/examples/backward-chaining/incident_response) |
| Phase rules with modules and focus | [`modules-focus/intake_pipeline`](https://github.com/cpcf/gess/tree/main/examples/modules-focus/intake_pipeline) |
| Gate on `exists` and `forall` | [`higher-order/readiness_gate`](https://github.com/cpcf/gess/tree/main/examples/higher-order/readiness_gate) |
| Study a larger integrated system | [`vulnerability_management`](https://github.com/cpcf/gess/tree/main/examples/vulnerability_management) |

## The examples in detail

### [`gess-files/order_routing`](https://github.com/cpcf/gess/tree/main/examples/gess-files/order_routing)

The canonical `.gess` workflow: templates, `deffacts` seed facts, a rule,
and a parameterized `defquery` in `rules.gess`, compiled by `gessc` into a
generated build function. This is the worked example behind
`TUTORIAL.md`. After editing the `.gess` file, regenerate:

```sh
go generate ./examples/gess-files/order_routing
go test ./examples/gess-files/order_routing
```

### [`forward-chaining/order_routing`](https://github.com/cpcf/gess/tree/main/examples/forward-chaining/order_routing)

The same order-routing behavior built entirely with the Go API:
`rules.TemplateSpec`, joins across order, customer, and inventory facts, an
action that asserts a derived route, a unique-key duplicate policy, and a
query. Start here to learn `rules.Match`, join constraints, and
`ActionContext`.

### [`queries/account_lookup`](https://github.com/cpcf/gess/tree/main/examples/queries/account_lookup)

The smallest complete program: one template, no rules, one parameterized
query using expression predicates and `rules.ParamExpr`, executed with
`session.QueryAll` and typed row extraction. Start here when the goal is
reading engine state from Go.

### [`negation/customer_screening`](https://github.com/cpcf/gess/tree/main/examples/negation/customer_screening)

`rules.Not` with a join: a customer is eligible only when no compliance
hold exists for them. Shows how absence is proven and how asserting the
blocker later removes the derived result.

### [`aggregates/fraud_velocity`](https://github.com/cpcf/gess/tree/main/examples/aggregates/fraud_velocity)

`rules.Accumulate` with `Count` and `Sum` over transaction facts, with the
threshold decision made in the action from the bound aggregate results.

### [`logical-support/remediation_tickets`](https://github.com/cpcf/gess/tree/main/examples/logical-support/remediation_tickets)

An action calls `AssertLogical` to open a ticket justified by a finding;
retracting the finding cascades the ticket away. Inspects
`Snapshot.SupportGraph()` edges and cascade counters. Start here for truth
maintenance.

### [`backward-chaining`](https://github.com/cpcf/gess/tree/main/examples/backward-chaining)

Three examples of query-driven proof with backchain-reactive templates and
`need-<template>` demand facts:

- [`incident_response`](https://github.com/cpcf/gess/tree/main/examples/backward-chaining/incident_response):
  direct and transitive reachability proofs, and the clearest introduction
  to the mechanism.
- [`insurance_claims`](https://github.com/cpcf/gess/tree/main/examples/backward-chaining/insurance_claims):
  proving claims payable through delegated approval chains, coverage, and
  exclusions.
- [`supply_chain_impact`](https://github.com/cpcf/gess/tree/main/examples/backward-chaining/supply_chain_impact):
  transitive vulnerable-dependency impact with waivers.

### [`modules-focus/intake_pipeline`](https://github.com/cpcf/gess/tree/main/examples/modules-focus/intake_pipeline)

Rules and templates partitioned into intake and response modules,
`SetFocus` and `FocusStack` from the host, and `PushFocus`/`PopFocus` from
actions. Start here to structure a ruleset into phases.

### [`higher-order/readiness_gate`](https://github.com/cpcf/gess/tree/main/examples/higher-order/readiness_gate)

A rollout gate combining `rules.Exists` (at least one readiness check
reported) with `rules.Forall` plus `rules.Test` (every check passed).

### [`vulnerability_management`](https://github.com/cpcf/gess/tree/main/examples/vulnerability_management)

The large end-to-end example: three modules with auto-focus, joins, nested
path constraints, expression predicates with placement inspection,
negation, `or` branches, incremental aggregates, logical-support tickets,
backchain-reactive reachability, and a batched feed driving module focus.
Run `go run ./examples/vulnerability_management` for a terminal trace, or
add `--serve :8080` for the browser interface. Its
[README](https://github.com/cpcf/gess/blob/main/examples/vulnerability_management/README.md)
maps the source files.

### [`stress`](https://github.com/cpcf/gess/tree/main/examples/stress)

Synthetic scaling harnesses, not tutorials:
[`complex_scale`](https://github.com/cpcf/gess/tree/main/examples/stress/complex_scale)
generates rulesets of fixed shapes and reports timings and memory
statistics;
[`extreme_scale`](https://github.com/cpcf/gess/tree/main/examples/stress/extreme_scale)
pushes million-scale rule and fact counts and can compare against Jess
given a `jess.jar`. Use these for local scaling experiments only.

## The tutorial workshop

[`tutorial/vulnerability_response`](https://github.com/cpcf/gess/tree/main/tutorial/vulnerability_response)
is an exercise: `rules.gess` starts empty, and the reference answer lives
in `solution/rules.gess`. Check progress with:

```sh
go generate ./tutorial/vulnerability_response
GESS_TUTORIAL=1 go test ./tutorial/vulnerability_response
```

`go run ./tutorial/cmd/gess-tutorial` serves a browser workshop with an
editor, checkpoints, and checks; adding the `prompt` argument runs the
same workshop as a terminal session. See
[`tutorial/README.md`](https://github.com/cpcf/gess/blob/main/tutorial/README.md).

## Next steps

- [Core concepts](concepts.md) or [Go API guide](go-api.md) for the
  vocabulary and APIs the examples use.
- [Advanced behavior](advanced.md) for the mechanisms behind aggregates,
  logical support, and backward chaining.
- [Developer guide](contributing.md) for how examples are tested and
  added.
