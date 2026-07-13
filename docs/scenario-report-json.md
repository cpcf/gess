# Scenario and report JSON

The `github.com/cpcf/gess/scenario` package defines two portable,
bidirectional JSON artifacts:

- `Scenario` records deterministic inputs, limits, selected queries, and
  optional expectations.
- `RunReport` records normalized results, all applied limits, and every
  truncation decision.

This package freezes the data contracts and their strict encoding and
validation functions. It does not load sources, create a session, run rules,
or evaluate expectations. A future
runner, Workbench, or MCP integration can consume these artifacts without
defining a parallel wire format, but those integrations are not implemented by
this contract.

## Go API and versions

The artifacts have independent, exact version strings:

```go
const ScenarioSchemaVersion = "gess.workbench.scenario.v1"
const RunReportSchemaVersion = "gess.workbench.report.v1"
```

Use the artifact functions rather than calling `encoding/json` directly on the
structs. The functions validate and normalize the complete document:

```go
func ValidateScenario(document Scenario) error
func MarshalScenario(document Scenario) ([]byte, error)
func UnmarshalScenario(data []byte) (Scenario, error)
func ScenarioDigest(document Scenario) (string, error)

func ValidateRunReport(document RunReport) error
func MarshalRunReport(document RunReport) ([]byte, error)
func UnmarshalRunReport(data []byte) (RunReport, error)
func RunReportDigest(document RunReport) (string, error)
```

Structural unsigned 64-bit fields use the public `DecimalUint64` wrapper:

```go
func NewDecimalUint64(value uint64) DecimalUint64
func (value DecimalUint64) Uint64() uint64
func (value DecimalUint64) String() string
```

It implements `json.Marshaler` and `json.Unmarshaler`, but artifact structs
still need the preceding contract functions for document-level validation.

Malformed or semantically invalid data wraps `ErrInvalidScenario` or
`ErrInvalidRunReport`. A decoder presented with a well-formed envelope carrying
an unknown version wraps `ErrUnsupportedScenarioVersion` or
`ErrUnsupportedRunReportVersion`, so callers can use `errors.Is` to distinguish
unsupported versions from invalid documents.

Version strings are case-sensitive. Because both artifacts are bidirectional
and used as digest inputs, a change to the accepted field set or its meaning requires a
new version. Unlike an export-only format, v1 has no silently additive fields.

The public string-enum types are `Strategy`, `TerminalStatus`, `Severity`,
`FactSupport`, `FieldPresence`, `EventType`, and `SectionAvailability`. The
wire values accepted for each are listed with their fields below.

## Scenario document

A scenario has these exact top-level members. Only `expectations` is optional.
Its nested public data types are `ScenarioSource`, `InitialFact`, `CallbackProfile`,
`RunOptions`, `ReportLimits`, `ScenarioQuery`, and `Expectations`.

| Member | Shape and meaning |
| --- | --- |
| `schemaVersion` | The string `gess.workbench.scenario.v1`. |
| `name` | A nonempty scenario identifier. |
| `sources` | Ordered `{path,digest?}` records. Paths are unique; `digest` may be omitted before a source is resolved. |
| `initialFacts` | Ordered `{template,fields}` records. Every value in the `fields` object is a typed `scenario.Value`. |
| `deffacts` | Ordered, unique `deffacts` names to select. |
| `globals` | An object from global name to typed `scenario.Value`. |
| `callbackProfile` | Required `{name,version,digest}` identity for the allowed callback registry. |
| `run` | Required `{strategy,maxFacts,maxFirings,deadlineMs}` execution limits. |
| `reportLimits` | Required bounds for every report section and output. |
| `queries` | Ordered, uniquely named `{name,args,maxRows}` query selections. Every `args` value is typed. |
| `expectations` | Optional expected terminal status and counts. |

`run.strategy` is `depth` or `breadth`. `run.maxFacts`, `run.maxFirings`, and
`run.deadlineMs` are positive integers. `reportLimits` contains ten positive
integer members:

```json
{
  "maxFacts": 1000,
  "maxFirings": 1000,
  "maxEvents": 2000,
  "maxQueryRows": 500,
  "maxDiagnostics": 100,
  "maxCounters": 100,
  "maxChecks": 100,
  "maxExplanationRefs": 100,
  "maxOutputBytes": 65536,
  "maxReportBytes": 1048576
}
```

Each selected query's `maxRows` is positive and no greater than
`reportLimits.maxQueryRows`. If `expectations` is present, it has required
`terminalStatus` and `queryRowCounts` members plus optional `factCount` and
`firingCount` members. Counts are nonnegative, and every key in
`queryRowCounts` must name a selected query.

The scenario package records expectations; it does not evaluate them. A producer
can publish evaluation results in a report's `checks` section.

## Run-report document

A run report has these exact top-level members, all required:

| Member | Shape and meaning |
| --- | --- |
| `schemaVersion` | The string `gess.workbench.report.v1`. |
| `producer`, `engine` | `{name,version}` build identities. |
| `sources` | Ordered resolved `{path,digest}` records; both members are required and paths are unique. |
| `scenarioDigest` | Digest of the canonical input scenario. |
| `rulesetId` | Stable compiled-ruleset identifier. |
| `callbackProfile` | The applied `{name,version,digest}` callback profile. |
| `limits` | `{input,run,report}` containing every applied admission, execution, and report limit. |
| `terminal` | `{status,runId,fired,error?}` terminal result. |
| `output` | Bounded UTF-8 output text and byte-level truncation metadata. |
| `facts`, `firings`, `events` | Bounded collections of normalized runtime records. |
| `queries` | Ordered query results; each result contains its own bounded rows. |
| `diagnostics`, `counters`, `checks` | Bounded diagnostic, counter, and expectation-check results. |
| `explanationRefs` | Bounded references to separately stored explanation artifacts. |

The report object graph uses `BuildInfo`, `ResolvedSource`, `AppliedLimits`,
`InputLimits`, `TerminalResult`, `ErrorPayload`, `SourceSpan`, `Output`,
`Fact`, `Firing`, `Event`, `QueryResult`, `QueryRow`, `QueryCell`, `Diagnostic`,
`Counter`, `CheckResult`, and `ArtifactReference`. Each bounded item type has a
matching exported collection type, and `SectionStatus` carries its
availability and reason.

### Terminal result and stable run IDs

`terminal.status` is one of:

- `quiescent`: no activation remains ready to fire;
- `max_facts`: the applied working-memory fact limit stopped the run;
- `max_firings`: the applied firing limit stopped the run;
- `deadline`: the applied deadline stopped the run;
- `error`: execution failed;
- `canceled`: the caller canceled execution; or
- `halted`: a rule action explicitly halted the session.

`terminal.error` is required only for `error` and forbidden for every other
status. Its shape is `{code,message,span?}`. A source `span`, wherever one
appears, is `{path,startLine,startColumn,endLine,endColumn}` with positive,
ordered coordinates and a portable path.

`terminal.runId` has canonical `run:<unsigned-decimal>` form, identifies a
nonzero run, and is stable within the artifact. Its decimal spelling has no
leading zeroes and fits `uint64`. The `runId` on every firing must equal it.
Events have their own required `runId`: they use either the terminal run ID or
the stable sentinel `run:zero` for lifecycle work outside that run. An artifact
producer must never put a random session ID in either field.

`terminal.fired` cannot exceed `limits.run.maxFirings`. When the `firings`
section is available, it also equals `firings.total`. When the `facts` section
is available, `facts.total` cannot exceed `limits.run.maxFacts`.

### Limits and truncation

`limits.input` records four positive admission bounds:
`maxRequestBytes`, `maxSourceFiles`, `maxSourceFileBytes`, and
`maxInitialFacts`. `limits.run` repeats the applied `strategy`, `maxFacts`,
`maxFirings`, and `deadlineMs`. `limits.report` repeats every applied report
limit, including `maxReportBytes`. A report cannot list more sources than
`limits.input.maxSourceFiles`, and its compact canonical encoding cannot exceed
`limits.report.maxReportBytes`.

These fields make the report self-describing even when a section was omitted,
was unsupported, or was not truncated.

Every bounded collection has the same seven required members:

```json
{
  "status": { "availability": "available", "reason": "" },
  "limit": 100,
  "total": 123,
  "totalKnown": true,
  "returned": 100,
  "truncated": true,
  "items": []
}
```

`status.availability` is `available`, `omitted`, or `unsupported`. An available
section has an empty `status.reason`, sets `totalKnown:true`, and follows exact
counting rules: `limit` equals the corresponding applied limit, `returned`
equals `len(items)` and is no greater than `min(total,limit)`, and `truncated`
equals `returned < total`. A producer may return fewer than the item limit when
the overall `maxReportBytes` ceiling requires additional deterministic
truncation.

An omitted or unsupported section supplies a nonempty reason and sets
`total:0`, `totalKnown:false`, `returned:0`, and `truncated:false`, with an empty
payload. Its `limit` still equals the applied limit, so the report records the
decision without pretending the empty payload is a measured zero. Empty
`items` is `[]`, not `null`, and every false truncation flag remains present.

| Collection | Applied limit |
| --- | --- |
| `facts` | `limits.report.maxFacts` |
| `firings` | `limits.report.maxFirings` |
| `events` | `limits.report.maxEvents` |
| each `queries[].rows` | that query result's `maxRows`, which cannot exceed `limits.report.maxQueryRows` |
| `diagnostics` | `limits.report.maxDiagnostics` |
| `counters` | `limits.report.maxCounters` |
| `checks` | `limits.report.maxChecks` |
| `explanationRefs` | `limits.report.maxExplanationRefs` |

`output` uses the same `status` and `totalKnown` model with byte-specific
members: `limitBytes`, `totalBytes`, `returnedBytes`, `truncated`, and `text`.
When available, `limitBytes` equals `limits.report.maxOutputBytes`,
`returnedBytes` is the UTF-8 byte length of `text`, and `truncated` is exactly
`returnedBytes < totalBytes`. It is no greater than either `totalBytes` or
`limitBytes`; the overall report byte ceiling may require a shorter result even
when the output-specific limit is larger. Omitted or unsupported output has an
empty string and the same unknown-zero metadata required of unavailable
collections.

### Item shapes

The bounded sections use these exact item members:

- A fact has `id`, `name`, `template`, `version`, `recency`, `generation`,
  `sequence`, `support`, `fields`, and `fieldPresence`. Exactly one of `name`
  and `template` is nonempty. `support` is `stated`, `logical`,
  `stated_and_logical`, or `metadata_only`. Every `fields` value is a typed
  `scenario.Value`. A template fact's `fieldPresence` maps field names to
  `omitted`, `default`, or `explicit`: omitted fields have no value, while
  default and explicit fields require one. Dynamic facts have an empty
  `fieldPresence`. `id` is exactly `fact:g<generation>:<sequence>`.
- A firing has `sequence`, `runId`, `activationId`, `ruleId`,
  `ruleRevisionId`, `ruleName`, `module`, `salience`, optional `source`, and
  `factIds`.
- An event has `sequence`, `runId`, `type`, `severity`, `generation`,
  `recency`, `ruleId`, `ruleRevisionId`, `activationId`, optional `source`,
  `actionName`, optional `actionIndex`, `factIds`, and optional `error`.
  Severity is `info`, `warning`, or `error`. Event type is one of
  `fact_asserted`, `fact_modified`, `fact_retracted`, `reset`,
  `rule_activated`, `rule_deactivated`, `rule_fired`, `action_failed`,
  `logical_support_added`, or `logical_support_removed`.
  Rule activation, deactivation, firing, action-failure, and logical-support
  events require `ruleId`, `ruleRevisionId`, and `activationId`. Those three
  fields are either all present or all empty on the remaining event types.
  An `action_failed` event belongs to the terminal run, requires an `error`
  terminal result, uses `error` severity, and requires `actionName`,
  `actionIndex`, and `error`; those action-specific members are empty or absent
  on every other event type.
- A query result has `name`, typed `args`, `maxRows`, and a bounded `rows`
  collection. Each row is `{cells:[...]}`. A cell has `alias` and exactly one
  of `factId` or typed `value`; aliases are unique within a row.
- A diagnostic has `id`, `phase`, `severity`, `code`, `message`, `target`,
  optional `span`, and `retryable`.
- A counter has `name`, canonical decimal-string `value`, and `unit`.
- A check has `path`, `passed`, `expected`, `actual`, and `message`.
- An explanation reference has `kind`, `id`, `schemaVersion`, and `digest`.

Events, diagnostics, and counters do not contain typed Gess values. Typed
`scenario.Value` envelopes occur only in initial and final fact fields,
`globals`, query arguments, and query value cells.

## Numeric precision

The artifact contract separates three numeric categories:

- Gess values use the kind-tagged [Value JSON](value-json.md) envelopes.
- Structural unsigned 64-bit fields use `DecimalUint64` and encode as JSON
  strings. These are fact `version`, `recency`, `generation`, and `sequence`;
  firing and event `sequence`; event `generation` and `recency`; and counter
  `value`.
- Bounds, counts, source coordinates, salience, and action indexes remain JSON
  numbers but must fit JavaScript's exact integer range.

A `DecimalUint64` string is `"0"` or an unsigned base-10 value without leading
zeroes, signs, whitespace, or non-ASCII digits, within the full `uint64` range.
All four structural fields on a fact and every firing or event `sequence` must
be positive. Event `generation` and `recency`, and counter `value`, may be zero.

Ordinary nonnegative integer fields are at most `9007199254740991` (2^53 - 1).
Positive limits and source coordinates start at 1; firing salience may range
from `-9007199254740991` through `9007199254740991`. This keeps every ordinary
JSON number exact in JavaScript without sacrificing full-width structural
identities and counters.

## Canonical ordering and bytes

`MarshalScenario` and `MarshalRunReport` emit compact JSON with a fixed member
order and no trailing newline. They normalize empty maps to `{}` and empty
slices to `[]`. JSON object keys are sorted, including field maps, `globals`,
query arguments, expectation counts, and field-presence maps.

Scenario arrays retain authored order: sources, initial facts, `deffacts`, and
selected queries are not reordered. A report retains the supplied order of
both sources and query results. Lists retain their runtime ordering, including
firing fact IDs, event fact IDs, and query cells.

Other report collections use these canonical orders:

- facts by `generation`, then `sequence`, then `id`;
- firings and events by `sequence`;
- query rows lexicographically by their ordered cells, using each `alias` and
  either `factId` or canonical typed-value bytes;
- diagnostics by `id`, `phase`, `severity`, `code`, `target`, `message`, then
  source span;
- counters by `name`, `unit`, then `value`;
- checks by `path`, `expected`, `actual`, then `message`; and
- explanation references by `kind`, `id`, `schemaVersion`, then `digest`.

The validation functions also reject duplicate identities. This includes source
paths, `deffacts` names, query names, fact IDs, firing and event sequences,
activation IDs, diagnostic IDs, counter names, check paths, explanation
`kind`/`id` pairs, and row aliases. Firing and event fact-ID lists preserve
order and may repeat an ID when multiple bindings refer to the same fact.
Every firing, event, and query-cell fact reference uses the same canonical
`fact:g<generation>:<sequence>` spelling as a reported fact, even when the
referenced fact is absent because the facts section was truncated.

An accepted artifact therefore has stable bytes after any decode-and-encode
cycle. Construction order still matters for the explicitly order-preserving
arrays.

## Digests and portable paths

Every digest uses lowercase `sha256:` followed by exactly 64 lowercase
hexadecimal digits. `ScenarioDigest` and `RunReportDigest` hash the respective
compact canonical bytes, without a fixture newline, and return that spelling.
`scenarioDigest` is expected to be the result of `ScenarioDigest` for the input
scenario; a standalone report decoder validates its spelling but cannot verify
it without the scenario.

A scenario source may omit `digest`; every resolved report source requires it.
The artifact package does not open files or verify source content. The producer
is responsible for hashing the exact raw source bytes, without newline or text
normalization, and for keeping the scenario and report source lists consistent.
Callback profiles and explanation references also carry required digests.

`maxReportBytes` is a hard bound on the entire compact report, including its
required envelope and identities. A producer must determine that the fixed
metadata can fit before execution; when it cannot, the request fails admission
outside the report contract instead of emitting an oversized or structurally
incomplete report.

Source and source-span paths are normalized, relative slash paths. The decoder
rejects absolute paths, volume-qualified paths such as `C:...`, backslashes,
empty segments, `.` and `..` segments, traversal, and any spelling that changes
under path cleaning. This keeps artifacts portable across hosts while
preserving the source order chosen by the producer.

## Strict decoding

Both decoders accept exactly one valid UTF-8 JSON document. They reject:

- a missing, non-string, or unsupported `schemaVersion`;
- duplicate, unknown, missing, or wrongly typed object members at any level;
- invalid UTF-8, unpaired Unicode surrogates, or trailing non-space data;
- a fractional, unsafe, or out-of-range JSON number where an integer is
  required, or a non-canonical `DecimalUint64` string;
- an invalid enumerated value, identifier, path, digest, typed value, source
  span, or conditional member; and
- inconsistent totals, applied limits, truncation flags, run IDs, or related
  cross-field counts.

All required collections remain present even when empty. The optional members
are limited to those marked in the preceding sections: scenario source digests
and expectations; expectation fact and firing counts; error and source-span
objects; event action indexes; and the mutually exclusive query-cell `factId`
and `value`.

## Excluded state and Explain v1

The artifacts exclude timestamps, wall-clock durations, absolute filesystem
paths, random session IDs, host environment details, and UI state such as
selected tabs, panel sizes, and expanded rows. Consumers needing that state
must store it in a separate workspace document.

Explain schema v1 is a documented compatibility exception. Explain, WhyNot,
and WhatIf documents are one-way exports: their integer fields remain ordinary
JSON numbers, their decoders are intentionally absent, and consumers ignore
unknown additive fields. Scenario and run-report v1 must not copy that
exception. Their decoders are strict, and their Gess value locations always
use the lossless typed envelope. See [Explain JSON](explain-json.md) for the
existing contract.
