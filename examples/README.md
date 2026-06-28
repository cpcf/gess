# Gess examples

- `backward-chaining`: query-driven proof examples.
- `forward-chaining`: rules that derive new facts from asserted facts.
- `negation`: `not` conditions and blocker facts.
- `aggregates`: `accumulate` with count and sum.
- `logical-support`: derived facts that cascade away with their support.
- `modules-focus`: module declarations and agenda focus control.
- `queries`: query APIs over asserted and derived facts.
- `higher-order`: `exists` and `forall` conditions.
- `gess-files`: templates, facts, rules, and queries loaded from `.gess` files.
- `vulnerability_management`: larger end-to-end example.

Start with `../TUTORIAL.md` for the preferred `.gess` plus `gessc` workflow.

Run the examples with:

```sh
go test ./examples/...
```
