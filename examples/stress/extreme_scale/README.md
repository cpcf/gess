# Extreme scale stress example

This example generates synthetic Gess `.gess` source or Jess `.clp` source to
push parser, compiler, session, run, and query behavior. It is intentionally
artificial and temporary: use it to find scaling limits, memory pressure, and
failure modes.

The generator does not check in a huge rules file. It creates one at runtime
from these dimensions:

- `-engine`: `gess` or `jess`
- `-rules`: generated `defrule` count
- `-facts`: generated input fact count
- `-queries`: generated `defquery` count
- `-buckets`: shared bucket values used by facts, rules, and query parameters

## Smoke run

The default run is large enough to exercise the full path without making normal
developer commands expensive:

```sh
go run ./examples/stress/extreme_scale
```

It prints source generation, parse, load, compile, session, run, query, and
memory measurements.

Gess memory lines use Go `runtime.MemStats`. Jess memory lines report Java heap
`used`, `committed`, and `max` after an explicit `Runtime.gc()` at the measured
phase. Use Java GC logs or JFR when you need peak-before-GC numbers.

## Generate huge rules files

Use `-write-only` for million-scale source generation. This streams directly to
disk and skips parse, compile, run, and query work:

```sh
go run ./examples/stress/extreme_scale \
  -rules 1000000 \
  -facts 1000000 \
  -queries 100000 \
  -buckets 1000 \
  -write /tmp/gess-extreme-scale.gess \
  -write-only
```

Generate the equivalent Jess `.clp` file with the same dimensions:

```sh
go run ./examples/stress/extreme_scale \
  -engine jess \
  -rules 1000000 \
  -facts 1000000 \
  -queries 100000 \
  -buckets 1000 \
  -write /tmp/jess-extreme-scale.clp \
  -write-only
```

That produces a very large file. Check disk space first.

## Compile or run a chosen size

For compiler stress, leave `-run=false` so the example stops after compiling:

```sh
go run ./examples/stress/extreme_scale \
  -rules 100000 \
  -facts 250000 \
  -queries 10000 \
  -buckets 1000 \
  -run=false
```

For runtime stress, choose counts that produce a manageable number of
activations. Rules match facts in the same bucket, so increasing both rules and
facts can multiply quickly:

```sh
go run ./examples/stress/extreme_scale \
  -rules 10000 \
  -facts 100000 \
  -queries 1000 \
  -buckets 1000 \
  -query-samples 5
```

Run the same generated shape through Jess with `-engine jess`. The runner uses
the same jar setup as the existing Jess benchmark scripts: it writes a temporary
`jess/RU.class` shim, compiles a small Java `Rete` runner with `javac`, batches
the generated source, calls `reset`, calls `run`, and samples queries with
`runQueryStar`:

```sh
go run ./examples/stress/extreme_scale \
  -engine jess \
  -rules 10000 \
  -facts 100000 \
  -queries 1000 \
  -buckets 1000 \
  -query-samples 5 \
  -jess-jar ../gess-design/jess.jar
```

## Try `gessc`

The generated file can also be passed to `gessc`, although very large generated
Go files may stress the Go compiler more than Gess itself:

```sh
go run ./cmd/gessc \
  -package main \
  -func buildExtremeRuleset \
  -o /tmp/gess-extreme-scale_generated.go \
  /tmp/gess-extreme-scale.gess
```
