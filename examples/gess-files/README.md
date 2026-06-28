# Gess files

Examples that define templates, facts, rules, and queries in `.gess` source
files, then load them through `ParseGess` and `LoadGess`.

- `order_routing`: routes VIP orders from templates, facts, rules, and queries
  declared in `rules.gess`. The example uses generated Go (`rules_generated.go`)
  so application startup does not parse the `.gess` file.

Regenerate the Go source with:

```sh
go generate ./examples/gess-files/order_routing
```
