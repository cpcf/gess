# Interactive tutorial

This tutorial is a hands-on workshop for learning Gess by editing a `.gess`
file and running it against a small vulnerability response workflow. It is
written for developers who are new to Gess or new to rules engines.

The easiest way to work through it is the browser UI:

```sh
go run ./tutorial/cmd/gess-tutorial
```

Then open `http://127.0.0.1:8090`. The page has the exercise source on the
right, guided examples in the middle, and progress tracking on the left. Each
checkpoint explains one rule-engine feature, shows the `.gess` example, walks
through the important lines, and gives you a small edit to make. The first page
is an overview with no coding expected. Use **Insert example** when you want the
page to add the current example to the editor.

Each time you select **Run checks**, the browser sends your editor contents to
the local tutorial server. The server parses the `.gess` source, builds a
ruleset, creates a session with the seed facts, runs the rules, and reports
which checkpoints passed.

The exercise package is `tutorial/vulnerability_response`. It starts with an
empty `.gess` file. You add templates, seed facts, queries, and rules as you go.
The completed solution is in
`tutorial/vulnerability_response/solution/rules.gess`.

## Before you start

Run the web tutorial from the module root:

```sh
go run ./tutorial/cmd/gess-tutorial
```

The terminal prints:

```text
Gess tutorial web UI: http://127.0.0.1:8090
```

The web UI validates the editor contents in memory. Use **Save to rules.gess**
when you want to write the current editor contents back to
`tutorial/vulnerability_response/rules.gess` and regenerate the compiled Go
source.

You can also use the command-line flow:

```sh
go generate ./tutorial/vulnerability_response
go test ./tutorial/vulnerability_response
go run ./tutorial/vulnerability_response
```

The starter runs without output because it has no templates, facts, queries, or
rules yet. If you prefer a terminal prompt, run:

```sh
go run ./tutorial/cmd/gess-tutorial prompt
```

The prompt supports checkpoint-aware commands:

```text
gess-tutorial> status
gess-tutorial> hint
gess-tutorial> run
gess-tutorial> test
```

The opt-in completion test fails until you finish the checkpoints:

```sh
GESS_TUTORIAL=1 go test ./tutorial/vulnerability_response
```

After you save your progress to `rules.gess`, regenerate and run the package:

```sh
go generate ./tutorial/vulnerability_response
go run ./tutorial/vulnerability_response
```

## Scenario

The workshop models vulnerability management and response. A `vulnerability` is
a finding that may need remediation. An `asset` is the affected system. An
`accepted-risk` fact is an exception for a specific finding. Rules derive
`remediation-action` facts, where the `lane` is the response queue and the
`target` is usually a vulnerability id. The `forall` checkpoint uses an asset id
as the target because that rule derives one action for an asset. An aggregate
summarizes critical findings, and a host callback records emergency responses.

In a rules engine, you usually describe *what pattern should cause a decision*,
not the loop that scans records. Gess owns the matching loop. Your `.gess` file
declares the fact shapes, seed data, rules, and queries:

- A **template** defines the fields and types for a kind of fact.
- A **fact** is a concrete record in a session, such as one vulnerability or
  asset.
- A **rule** has conditions before `=>` and actions after `=>`.
- A **query** gives Go code a named way to read derived state.
- A **session** holds facts, runs rule activations, and answers queries.

The first checkpoints build the `.gess` file from the ground up:

- Checkpoint 1 adds `vulnerability`, `asset`, `accepted-risk`,
  `remediation-action`, and `critical-vulnerability-summary` templates.
- Checkpoint 2 adds seed facts for seven vulnerabilities, three assets, and one
  accepted-risk exception.
- Checkpoint 3 adds `actions-by-lane` and `critical-summaries` queries.

The remaining checkpoints add rules for:

- emergency routing for exploitable critical findings
- accepted-risk routing
- `and`, `or`, `exists`, `forall`, and `not`
- critical vulnerability aggregation
- calling host Go code for emergency response recording

Expected final output:

```text
emergency: VULN-100 critical-exploitable-internet
accepted-risk: VULN-200 compensating-control
and: VULN-400 critical-nonexploited
or: VULN-500 dependency-or-exposure-watch
exists: APP-100 asset-has-critical
forall: APP-300 asset-under-limit
standard: VULN-300 normal-remediation
summary: critical count=2 total=195
recorded: VULN-100/critical-exploitable-internet
```

Run the completion check:

```sh
GESS_TUTORIAL=1 go test ./tutorial/vulnerability_response
```

## If you get stuck

Compare your file with:

```sh
diff -u tutorial/vulnerability_response/rules.gess tutorial/vulnerability_response/solution/rules.gess
```

You can also restore the completed solution:

```sh
cp tutorial/vulnerability_response/solution/rules.gess tutorial/vulnerability_response/rules.gess
go generate ./tutorial/vulnerability_response
GESS_TUTORIAL=1 go test ./tutorial/vulnerability_response
```

The tutorial runner can apply the starter or solution for you:

```text
gess-tutorial> reset
gess-tutorial> solution
```
