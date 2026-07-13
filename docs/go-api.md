# Go API guide

Gess exposes four public packages. They are stable public boundaries over the
internal engine: some types are pure public values in `rules`, while runtime
handles and behavior are implemented by `internal/engine` behind the facades.

- `github.com/cpcf/gess/rules`: rule-definition values and the compiled
  `Ruleset` facade, covering templates, rules, queries, actions, expressions,
  and values.
- `github.com/cpcf/gess/session`: the runtime, covering sessions,
  workspace construction, mutations, runs, queries, snapshots, and events.
- `github.com/cpcf/gess/dsl`: `.gess` parsing, loading, code generation, and
  the registry that connects `.gess` files to host Go code.
- `github.com/cpcf/gess/scenario`: strict, deterministic scenario and
  run-report artifacts plus lossless JSON adapters for `rules.Value` data.

The preferred workflow keeps rule definitions in `.gess` files compiled with
`gessc` (see `TUTORIAL.md`). This guide covers the programmatic API,
which the generated code uses and which is available directly when rules
are built in Go.

## Building a ruleset

A `rules.Workspace` collects definitions; `Compile` produces an immutable
`*rules.Ruleset`:

```go
workspace := session.NewWorkspace()
// workspace.AddTemplate, AddRule, AddAction, AddQuery, ...
ruleset, err := workspace.Compile(ctx)
```

Workspace construction is explicit: `session.NewWorkspace` supplies the
engine-backed implementation behind the public `rules.Workspace` facade.
Importing `rules` alone is sufficient for authoring values, but compilation
requires the `session` runtime boundary.

Workspace methods: `AddModule`, `AddTemplate`, `AddAction` /
`ReplaceAction` / `RemoveAction`, `AddFunction` / `ReplaceFunction` /
`RemoveFunction`, `AddRule` / `ReplaceRule` / `RemoveRule`, and `AddQuery`
/ `ReplaceQuery` / `RemoveQuery`. Definitions are validated at `Add` time
and again at compile; errors wrap `rules.ErrValidation` and related
sentinels.

A compiled ruleset is safe to share: sessions never mutate it, and several
sessions can use the same ruleset.

## Templates

```go
err := workspace.AddTemplate(rules.TemplateSpec{
	Name: "fulfillment-route",
	Fields: []rules.FieldSpec{
		{Name: "order", Kind: rules.ValueString, Required: true},
		{Name: "lane", Kind: rules.ValueString, Required: true},
		{Name: "warehouse", Kind: rules.ValueString, Required: true},
	},
	DuplicatePolicy:   rules.DuplicateUniqueKey,
	DuplicateKeyNames: []string{"order"},
})
```

`TemplateSpec` fields: `Name`, `Module` (defaults to `MAIN`), `Key`
(defaults to the name), `Fields`, `DuplicatePolicy`, `DuplicateKeyNames`,
and `BackchainReactive`. `FieldSpec` fields: `Name`, `Kind` (defaults to
`ValueAny`), `Required`, `Default` with `HasDefault`, and `AllowedValues`.

Duplicate policies: `DuplicateStructural` (the zero value) deduplicates
facts with identical fields, `DuplicateAllow` permits duplicates, and
`DuplicateUniqueKey` keeps one current fact per key (`DuplicateKeyNames`,
which must name declared fields). Asserting a `DuplicateUniqueKey` fact
whose key matches an existing fact but whose non-key fields differ replaces
the old fact: retract old, assert new, with a new fact ID, reported as
`AssertReplaced`, while asserting an identical fact is a no-op
(`AssertExisting`).

## Rules

A `rules.RuleSpec` has a name, an optional module, salience, an optional
auto-focus flag, a condition tree, and a list of action references:

```go
err := workspace.AddRule(rules.RuleSpec{
	Name: "route-vip-order",
	ConditionTree: rules.And{Conditions: []rules.ConditionSpec{
		rules.Match{Binding: "order", Target: rules.TemplateFact("order")},
		rules.Match{
			Binding: "customer",
			Target:  rules.TemplateFact("customer"),
			FieldConstraints: []rules.FieldConstraintSpec{
				{Field: "segment", Operator: rules.FieldConstraintEqual, Value: "vip"},
			},
			JoinConstraints: []rules.JoinConstraintSpec{
				{Field: "id", Operator: rules.FieldConstraintEqual,
					Ref: rules.FieldRef{Binding: "order", Field: "customer"}},
			},
		},
	}},
	Actions: []rules.RuleActionSpec{{Name: "route-vip-order"}},
})
```

### Condition forms

`ConditionSpec` is an interface with these implementations:

- `rules.Match`: a positive fact pattern with a `Binding`, a `Target`,
  `FieldConstraints`, `JoinConstraints`, `Predicates` (expressions), and
  `ListPatterns`. Set `Volatile: true` when the matched fact is expected to be
  modified frequently; the static planner may place that independent gate
  later to reduce propagation churn. The hint does not change match semantics
  or revision identity.
- `rules.And`, `rules.Or`: grouping. Bindings inside an `Or` branch stay
  local to that branch.
- `rules.Not`: absence; bindings inside are local.
- `rules.Exists(condition)`: at least one match, one activation.
- `rules.Forall(domain, requirement)`: every domain match has a matching
  requirement.
- `rules.Test{Expression: ...}`: a boolean expression over earlier
  bindings.
- `rules.Accumulate(input, specs...)`: aggregation; see the aggregates
  section.
- `rules.Explicit{Condition: ...}`: opt a condition out of backward-chaining
  demand generation.

Large disjunctive trees execute as structural graph unions instead of an
eager Cartesian branch expansion. `Rule.ConditionBranches` and
`Query.ConditionBranches` remain bounded inspection views;
`ConditionBranchesTruncated()` reports when the returned branch list is only
a prefix.

`RuleSpec` also accepts a flat `Conditions []RuleConditionSpec` slice as a
shorthand for a top-level `And` of matches.

:::caution
Set either `Conditions` or `ConditionTree` on a `RuleSpec`, not both.
Nothing on the struct itself prevents setting both at once.
:::

`Volatile` is an explicit Go authoring hint. The zero value preserves ordinary
static planning, and Gess never infers it from runtime counters or names.

### Targets, constraints, and joins

A `Match` targets facts through a `FactTarget`: `TemplateFact(name)`,
`TemplateFactIn(module, name)`, `TemplateKeyFact(key)` for template-backed
facts, or `DynamicFact(name)` / `DynamicFactIn(module, name)` for facts
asserted without a template.

`FieldConstraintSpec` compares one field (or nested `Path`) of the matched
fact against a constant `Value` with an operator: `FieldConstraintEqual`,
`FieldConstraintNotEqual`, `FieldConstraintLessThan`,
`FieldConstraintLessOrEqual`, `FieldConstraintGreaterThan`,
`FieldConstraintGreaterOrEqual`, or `FieldConstraintExists` (no value).

`JoinConstraintSpec` compares a field of the matched fact against a field
of an earlier binding through `Ref: rules.FieldRef{Binding, Field}`.
Equality joins compile to indexed hash joins in the Rete graph.

### Aggregates

```go
rules.Accumulate(
	rules.Match{Binding: "transaction", Target: rules.TemplateFact("transaction")},
	rules.Count().As("count"),
	rules.Sum(rules.BindingFieldExpr{Binding: "transaction", Field: "amount"}).As("total"),
)
```

Aggregate constructors: `rules.Count()`, `rules.Sum(expr)`,
`rules.Min(expr)`, `rules.Max(expr)`, and `rules.Collect(expr)`. Bind each
result with `.As(name)`; actions read results through
`ActionContext.BindingValue(name)`.

## Actions

Rules reference actions by name; the workspace owns the implementations:

```go
err := workspace.AddAction(rules.ActionSpec{
	Name: "route-vip-order",
	Fn: func(ctx rules.ActionContext) error {
		order, _ := ctx.BindingScalarValue("order", "id")
		warehouse, _ := ctx.BindingScalarValue("inventory", "warehouse")
		_, err := ctx.Assert(rules.TemplateKey("fulfillment-route"), rules.Fields{
			"order":     order,
			"lane":      mustValue("expedite"),
			"warehouse": warehouse,
		})
		return err
	},
})
```

`ActionSpec` takes exactly one of `Fn` (an `ActionFunc`) or
`AssertTemplateValues` (a declarative assert of expression values in
template field order, which the `.gess` compiler uses for `assert`
actions). The optional `BindingReads` declares which bindings the action
reads so the runtime can avoid materializing the rest.

The `rules.ActionContext` passed to `Fn` provides activation identity,
binding access, the mutation API including `AssertLogical`, `Halt`, and
focus control. See `session-lifecycle.md` for the full method list.

## Queries

```go
err := workspace.AddQuery(rules.QuerySpec{
	Name:       "accounts-by-region",
	Parameters: []rules.QueryParameterSpec{{Name: "region", Kind: rules.ValueString}},
	ConditionTree: rules.Match{
		Binding: "account",
		Target:  rules.TemplateFact("account"),
		Predicates: []rules.ExpressionSpec{
			rules.CompareExpr{
				Operator: rules.ExpressionCompareEqual,
				Left:     rules.CurrentFieldExpr{Field: "region"},
				Right:    rules.ParamExpr{Name: "region"},
			},
		},
	},
	Returns: []rules.QueryReturnSpec{
		rules.ReturnValue("id", rules.BindingFieldExpr{Binding: "account", Field: "id"}),
		rules.ReturnValue("balance", rules.BindingFieldExpr{Binding: "account", Field: "balance"}),
	},
})
```

A query has parameters (referenced with `rules.ParamExpr`), the same
condition forms as a rule, and returns built with `rules.ReturnFact` for
whole facts or `rules.ReturnValue` for scalar expressions.
Execute queries with `session.Query` or `session.QueryAll`.
Rows are deterministic for a fixed session history, but their order is
otherwise unspecified and may change after mutations or runtime-memory
refactors. Sort explicitly when presentation or downstream processing needs a
defined order.

## Expressions

`ExpressionSpec` nodes compose predicates, tests, aggregate inputs, and
query returns:

- `rules.ConstExpr{Value: ...}`: a constant.
- `rules.CurrentFieldExpr{Field: ...}` or `rules.CurrentPath(path)`: a
  field of the fact matched by the current condition.
- `rules.BindingFieldExpr{Binding, Field}` or
  `rules.BindingPath(binding, path)`: a field of an earlier binding.
- `rules.BindingValueExpr{Binding: ...}`: a value binding, such as an
  aggregate result.
- `rules.ParamExpr{Name: ...}`: a query parameter.
- `rules.HasPath(path)`: whether a nested path exists.
- `rules.CompareExpr{Operator, Left, Right}` with the
  `ExpressionCompare...` operators.
- `rules.BooleanExpr{Operator, Operands}` with `ExpressionBoolAnd`,
  `ExpressionBoolOr`, and `ExpressionBoolNot`.
- `rules.Call(name, args...)`: invoke a registered pure function.

Nested values in `LIST` and `MAP` fields are addressed with
`rules.Path(root, segments...)`, using `rules.MapKey(key)` and
`rules.ListIndex(index)` segments.

## Pure functions

Pure functions are deterministic helpers callable from expressions:

```go
err := workspace.AddFunction(rules.PureFunctionSpec{
	Name:   "risk-band",
	Args:   []rules.ValueKind{rules.ValueInt},
	Return: rules.ValueString,
	Func: func(_ context.Context, args []rules.Value) (rules.Value, error) {
		score, _ := args[0].AsInt64()
		if score >= 90 {
			return rules.NewValue("high")
		}
		return rules.NewValue("low")
	},
})
```

Provide exactly one of `Func` (variadic) or the fixed-arity `Func0` through
`Func3`, matching `len(Args)`. `rules.Call("risk-band", ...)` resolves the
name at compile time; unknown names fail compilation with an error wrapping
`rules.ErrFunctionValidation`.

## Values and fields

Facts carry `rules.Value` data with kinds `ValueNull`, `ValueBool`,
`ValueInt`, `ValueFloat`, `ValueString`, `ValueList`, and `ValueMap`.
Construct values from Go data:

- `rules.NewValue(raw)`: converts booleans, strings, integer types, floats,
  `[]any` and typed slices, and `map[string]any`. Unsupported inputs, such
  as structs, functions, NaN, or unsigned values past the signed range,
  return an error wrapping `rules.ErrUnsupportedValue`.
- `rules.NewFields(map[string]any)`, `rules.NewFieldsFromPairs(pairs...)`,
  and `rules.MustFields(pairs...)` build field maps; `MustFields` panics on
  conversion errors and suits tests and fixed data.

Read values with `Kind()`, `AsBool()`, `AsInt64()`, `AsFloat64()`,
`AsString()`, and `Equal()`.

### Portable value JSON

The `scenario` package converts all seven value kinds to a strict typed JSON
envelope without losing `int64` precision, float kind, or negative zero.
`scenario.NewValue(value)` creates a JSON-facing `scenario.Value`, whose
`RulesValue()` method returns the wrapped `rules.Value`. For standalone bytes,
use `scenario.MarshalValue(value)` and `scenario.UnmarshalValue(data)`.
Invalid input returns an error wrapping `scenario.ErrInvalidValueJSON`.

Nested lists use nested envelopes, and maps use entry arrays sorted by key, so
equal typed contents marshal to deterministic bytes. See
[Value JSON](value-json.md) for the exact seven shapes, canonical number rules,
strict decoder behavior, and the Explain v1 compatibility exception.

### Portable scenario and run-report artifacts

The `scenario` package also owns the `Scenario` input and `RunReport` output
contracts. Their independent version constants are
`ScenarioSchemaVersion` (`gess.workbench.scenario.v1`) and
`RunReportSchemaVersion` (`gess.workbench.report.v1`). Use the contract
functions instead of marshaling the structs directly:

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

Invalid documents wrap `ErrInvalidScenario` or `ErrInvalidRunReport`. Strict
decoding classifies unknown versions with `ErrUnsupportedScenarioVersion` or
`ErrUnsupportedRunReportVersion`. The contract functions normalize ordering,
enforce every applied limit and truncation invariant, and emit byte-identical
JSON for equal artifacts. They define data contracts only: they do not load
source files, run a session, or evaluate expectations.

Full-width structural counters use `scenario.DecimalUint64`, constructed with
`scenario.NewDecimalUint64(value)` and read with `Uint64()`. It encodes as a
canonical decimal string, while bounded counts and limits remain JSON numbers
within JavaScript's safe-integer range. Every bounded report section also
publishes its `status`, `totalKnown`, applied limit, returned count, and
truncation decision; `limits` echoes input, run, and report bounds.

See [Scenario and report JSON](scenario-report-json.md) for every wire member,
enumerated value, canonical order, digest and portable-path rule, and the
locations that use typed `scenario.Value` envelopes.

## The `dsl` package

The `dsl` package connects `.gess` sources to the preceding API:

- `dsl.Parse(name, source)` parses a `.gess` file into a `*dsl.Document`.
- `dsl.Load(ctx, workspace, doc, registry)` loads a parsed document into a
  workspace.
- `dsl.Compile(ctx, name, source, registry)` parses, loads, and compiles in
  one call.
- `dsl.GenerateGo(ctx, sources, opts)` generates Go source with a build
  function; `gessc` is a thin wrapper around it.
- `dsl.InitialFacts(doc)` extracts `deffacts` facts as
  `session.InitialFact` values.

`dsl.Registry` supplies the host implementations a `.gess` file references:

```go
registry := dsl.Registry{
	Calls: map[string]dsl.CallFunc{
		"record": func(ctx rules.ActionContext, args []rules.Value) error {
			return nil
		},
	},
}
```

`Registry.Calls` backs `(call name arg...)` actions, `Registry.Actions`
backs zero-argument `(call name)` actions, and `Registry.Functions`
registers pure functions for expressions. Loading fails when a `.gess` file
references a name the registry doesn't provide.

## Running the result

```go
session, err := sess.New(ruleset, sess.WithInitialFacts(initials...))
if err != nil {
	return err
}
defer session.Close()

if _, err := session.Assert(ctx, rules.TemplateKey("order"), fields); err != nil {
	return err
}
result, err := session.Run(ctx)
if err != nil {
	return err
}
rows, err := session.QueryAll(ctx, "routes", nil)
```

See `session-lifecycle.md` for the full session API: mutation results,
run statuses, queries, snapshots, events, the focus stack, and
`ApplyRuleset`.

## Next steps

- [Session lifecycle](session-lifecycle.md) for the full runtime API.
- [Advanced behavior](advanced.md) for aggregates, higher-order
  conditions, logical support, and backward chaining.
- [Examples map](examples.md) for Go-API examples organized by feature.
