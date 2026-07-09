# Command-line tools

Gess ships three commands. The `gess` command runs the interactive REPL,
`gessc` compiles `.gess` files to Go, and `gessfmt` formats `.gess` files.
All three live under `cmd/` and run
with `go run` from the module root. When a standalone binary is more
convenient, install them:

```sh
go install github.com/cpcf/gess/cmd/gess@latest
go install github.com/cpcf/gess/cmd/gessc@latest
go install github.com/cpcf/gess/cmd/gessfmt@latest
```

## `gess repl`

```sh
gess repl [--stub-calls] [--no-prompt]
```

The REPL is a shell over the public session API:

```sh
gess> load examples/gess-files/order_routing/rules.gess
gess> facts
gess> run 1
gess> agenda
gess> query routes-by-lane lane=expedite
```

Interactive terminals support shell-style editing: up/down arrow history,
`ctrl-r` reverse history search, `tab` completion, `ctrl-l` clear-screen, and
`ctrl-d` exit on an empty line. Completion uses the current ruleset when one is
loaded, including template names, field names, rule names, query names, fact
IDs, module names, and watch event types. Command history is persisted in the
user state directory.

Piped mode is deterministic and exits non-zero if any command reports an error:

```sh
gess repl < script.txt
```

Use `--stub-calls` when loading `.gess` files with unregistered `(call ...)`
actions that should print stub invocations instead of failing. Use
`--no-prompt` to force line-oriented behavior even when stdin is a terminal.

## `gessc`

```sh
gessc [-o file] [-package name] [-func name] rules.gess [...]
```

`gessc` parses one or more `.gess` files and emits a single Go source file
containing a build function:

```go
func BuildRuleset(ctx context.Context, registry dsl.Registry) (*rules.Ruleset, []session.InitialFact, error)
```

The function compiles the ruleset and returns the `deffacts` seed facts as
initial facts for `session.WithInitialFacts`. Generated code validates at
build time that every `(call ...)` name in the `.gess` sources is present
in the supplied registry, so missing host integrations fail at startup.

Flags:

- `-o file`: output path. With no `-o`, the generated source goes to
  standard output.
- `-package name`: package name for the generated file. Defaults to
  `main`.
- `-func name`: name of the generated build function. Defaults to
  `BuildRuleset`.

Passing several `.gess` files merges them into one generated ruleset.

### Use with go generate

Keep the compile step next to the code that uses it:

```go
//go:generate go run ../../../cmd/gessc -package main -func buildGeneratedRuleset -o rules_generated.go rules.gess
```

Then regenerate with:

```sh
go generate ./examples/gess-files/order_routing
```

The relative `../../../cmd/gessc` path suits the in-repository examples; in
another module, point the directive at an installed `gessc` binary or a
tool dependency. The flags are the same.

Errors are reported with `file:line:column` positions and stop generation,
exiting nonzero.

## `gessfmt`

```sh
gessfmt [-w] [-l] [-check] [file ...]
```

The `gessfmt` command rewrites `.gess` files into the canonical layout:
two-space indentation, one blank line between top-level forms, short forms
kept on one line, and long forms expanded with closing parentheses on their
own lines.

:::caution
`gessfmt` currently discards `;` comments: the parser drops them, so
formatted output contains no comments and `-w` deletes them from the file
irreversibly. Until comment preservation lands, do not run `gessfmt -w` on
files whose comments you want to keep.
:::

Flags:

- No flags with file arguments: print each formatted file to standard
  output.
- `-w`: write the result back to each file.
- `-l`: list the files whose formatting differs, without rewriting them.
- `-check`: like `-l`, and exit nonzero when any file needs formatting.
  Suits continuous-integration checks.
- No arguments: read from standard input and write the formatted result to
  standard output. The `-w`, `-l`, and `-check` flags require file
  arguments.

Typical usage:

```sh
go run ./cmd/gessfmt -w examples/gess-files/order_routing/rules.gess
```

Files that fail to parse are reported with `file:line:column` positions and
the command exits nonzero.

## Next steps

- [The tutorial](TUTORIAL.md) to see `gessc` used end to end.
- [Go API guide](go-api.md) to build the generated ruleset into a session.
- [The `.gess` language reference](gess-language.md) for what `gessc`
  accepts.
