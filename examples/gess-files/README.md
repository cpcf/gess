# Gess files

Examples that define templates, facts, rules, and queries in `.gess` source
files, then compile them to Go with `gessc`.

- `order_routing`: routes VIP orders from templates, facts, rules, and queries
  declared in `rules.gess`. The example uses generated Go (`rules_generated.go`)
  so app startup does not parse the `.gess` file.

Regenerate the Go source with:

```sh
go generate ./examples/gess-files/order_routing
```
