# Machine-readable explain contract (JSON)

Gess encodes its three interrogation reports — `Derivation` (why a fact
exists), `WhyNotReport` (why a rule has no activation), and `WhatIfReport` (a
counterfactual run) — as versioned JSON documents, so UIs, audit pipelines, and
LLM agents consume proof trees and counterfactual reports as data instead of
scraping rendered text.

Each type implements `json.MarshalJSON`, so `json.Marshal(report)` produces the
document directly:

```go
raw, err := json.Marshal(derivation)  // or json.Marshal(whyNotReport), json.Marshal(whatIfReport)
```

## Envelope and versioning

Every top-level document carries the schema version and its kind:

```json
{ "gessExplainSchema": 1, "kind": "derivation", ... }
```

`kind` is one of `"derivation"`, `"whynot"`, or `"whatif"`. The version
(`ExplainSchemaVersion`) is bumped only on a **breaking** change — a field
removal or rename. Additive changes (new fields) keep the same version, so
consumers must ignore unknown fields. Nested objects (child derivations,
branches, facts) do not repeat the envelope.

## Conventions

- **IDs** encode as their string forms: `"fact:g1:12"`, `"rule:open-ticket"`,
  `"run:3"`, and so on — human-legible, greppable, and matching REPL output.
- **Integers** encode as JSON numbers. The exact digits are preserved in the
  document, but a consumer that parses JSON numbers as `float64` (JavaScript,
  most `json` libraries by default) loses precision beyond 2^53. Read
  large integer fields as strings if that matters.
- **Object key order** is deterministic (keys are sorted), so documents are
  byte-stable for equal inputs and safe to diff.
- **Nil / empty fields are omitted**; `truncated` is always present.
- These are **export-only** documents. There is no decoder back into engine
  types; the contract is a one-way projection.

## `derivation`

A fact, its support state, the firing that produced it (with the rendered
`.gess` action and — with firing-time capture — the exact bindings), the facts
it logically depends on (recursively), and its `history` of mutations. Tier-1
(snapshot) derivations omit `history` and omit `producedBy` for stated facts.

```json
{
  "gessExplainSchema": 1,
  "kind": "derivation",
  "fact": {
    "id": "fact:g1:2",
    "name": "record",
    "templateKey": "",
    "version": 0,
    "support": "stated_and_logical"
  },
  "support": "stated_and_logical",
  "truncated": false,
  "producedBy": {
    "ruleId": "rule:advance",
    "ruleName": "advance",
    "ruleRevisionId": "rule-revision:...",
    "activationId": "activation:...",
    "generation": 0,
    "action": "(modify ?r (set (status \"active\")))",
    "bindingsPartial": false,
    "bindings": [{ "name": "?r", "value": null, "fromFact": "fact:g1:2" }]
  },
  "dependsOn": [
    {
      "fact": { "id": "fact:g1:1", "name": "trigger", "templateKey": "", "version": 0, "support": "stated" },
      "support": "stated",
      "truncated": false
    }
  ]
}
```

## `whynot`

The rule identity, the `outcome` (`activated`, `already_fired`,
`never_matched`, or `blocked`), and per-branch conditions with the first
failing one classified. A `truncated:true` report is either bounded by a probe
cap or explicitly degraded because the runtime could not map a graph frontier
to a condition safely; consumers must not infer a failure from an unattributed
branch in that case. Within each branch, `satisfied:true` marks the contiguous
prefix before `firstFailing`; every condition is satisfied when
`firstFailing` is `-1` for a complete branch.

```json
{
  "gessExplainSchema": 1,
  "kind": "whynot",
  "ruleId": "rule:escalate",
  "ruleName": "escalate",
  "ruleRevisionId": "rule-revision:...",
  "outcome": "blocked",
  "truncated": false,
  "branches": [
    {
      "branchId": 0,
      "firstFailing": 1,
      "conditions": [
        { "order": 0, "plannedOrder": 0, "binding": "a", "negated": false, "test": false, "aggregate": false, "alphaMatches": 1, "satisfied": true },
        { "order": 1, "plannedOrder": 1, "binding": "alert", "negated": true, "test": false, "aggregate": false, "alphaMatches": 0, "satisfied": false, "reason": "negation_blocked", "blockers": ["fact:g1:12"], "blockerCount": 1 }
      ]
    }
  ]
}
```

## `whatif`

The bounded `run` result, the ordered `firings`, the working-memory `diff`
(the base and fork snapshots are represented by their difference, not in full),
the agenda counts before and after, and — with `WithWhatIfExplain` —
`derivations` for the added facts.

```json
{
  "gessExplainSchema": 1,
  "kind": "whatif",
  "run": { "runId": "run:1", "status": "completed", "fired": 2 },
  "agendaBeforeCount": 0,
  "agendaAfterCount": 0,
  "firings": [
    { "ruleId": "rule:derive", "ruleName": "derive", "ruleRevisionId": "rule-revision:...", "activationId": "activation:...", "factIds": ["fact:g1:1"], "sequence": 3 }
  ],
  "diff": {
    "added": [
      { "id": "fact:g1:2", "name": "derived", "templateKey": "", "version": 0, "support": "logical" }
    ]
  }
}
```
