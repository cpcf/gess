# Gess

Gess is a Go rules engine. It provides a Rete-based runtime, a Go API for
building rulesets, and a `.gess` file format for defining templates, seed facts,
rules, and queries outside app code.

The preferred workflow is to keep rule definitions in `.gess` files, compile
them to Go with `gessc`, and use the generated ruleset from normal Go code.

## Status

This repository is under active development. The public API is organized around
the `rules`, `session`, and `dsl` packages.

## Requirements

- Go 1.26.2 or newer, matching the module `go.mod`.

## Quick start

Use the module path `github.com/cpcf/gess` from Go code. The main public
packages are `github.com/cpcf/gess/rules`, `github.com/cpcf/gess/session`, and
`github.com/cpcf/gess/dsl`.

Run the examples from the module root:

```sh
go test ./examples/...
```

Regenerate and test the compiled `.gess` example:

```sh
go generate ./examples/gess-files/order_routing
go test ./examples/gess-files/order_routing
```

Run `gessc` directly:

```sh
go run ./cmd/gessc \
  -package main \
  -func buildGeneratedRuleset \
  -o examples/gess-files/order_routing/rules_generated.go \
  examples/gess-files/order_routing/rules.gess
```

Format `.gess` files with `gessfmt`:

```sh
go run ./cmd/gessfmt -w examples/gess-files/order_routing/rules.gess
```

## `.gess` workflow

A typical project keeps rules and templates in a source file:

```cl
(deftemplate order
  (slot id (type STRING) (required TRUE))
)

(deftemplate routed-order
  (slot order (type STRING) (required TRUE))
)

(defrule route-order
  (order (id ?id))
  =>
  (assert (routed-order
    (order ?id)
  )
  )
)
```

Compile that file during generation:

```go
//go:generate go run ../../../cmd/gessc -package main -func buildGeneratedRuleset -o rules_generated.go rules.gess
```

That relative path is for the in-repository examples. In another module, point
`go:generate` at the `gessc` command however you provide it; the flags are the
same.

Use the generated build function from app code:

```go
ctx := context.Background()
ruleset, initials, err := buildGeneratedRuleset(ctx, dsl.Registry{})
if err != nil {
	return err
}

session, err := sess.New(ruleset, sess.WithInitialFacts(initials...))
if err != nil {
	return err
}
defer session.Close()

_, err = session.Run(ctx)
```

See `TUTORIAL.md` for a fuller walkthrough based on
`examples/gess-files/order_routing`.

For an interactive edit-and-run workshop, use `tutorial/README.md` or run
`go run ./tutorial/cmd/gess-tutorial`.

## Packages

- `rules`: public types for templates, conditions, actions, queries, values, and
  compiled rulesets.
- `session`: runtime API for asserting, modifying, retracting, running rules,
  querying, snapshots, events, and logical support.
- `dsl`: parser, loader, generated-code support, and registry hooks for `.gess`
  files.
- `cmd/gessc`: command-line compiler from `.gess` files to generated Go.

Most implementation code lives under `internal/engine`.

## Examples

Examples are in `examples/`:

- `gess-files`: `.gess` files compiled with `gessc`.
- `forward-chaining`: deriving facts from asserted facts.
- `queries`: named query APIs over asserted and derived facts.
- `negation`: `not` conditions.
- `aggregates`: `accumulate`, `count`, and `sum`.
- `logical-support`: logical assertions and support cascades.
- `backward-chaining`: query-driven proof examples.
- `modules-focus`: module declarations and agenda focus control.
- `higher-order`: `exists` and `forall` conditions.
- `vulnerability_management`: larger end-to-end example.

## Development

Format touched Go files, update source, and run tests after implementation
changes:

```sh
gofmt -w <touched-go-files>
go fix ./...
go test ./...
```

For docs-only changes, run the relevant example tests when commands or snippets
refer to examples.

## References

Gess uses rule-engine concepts associated with Rete-family systems, including
Jess and CLIPS. It is a Go-native implementation and is not intended to be a
Jess compatibility layer.
