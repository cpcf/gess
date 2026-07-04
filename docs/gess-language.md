# The `.gess` language reference

`.gess` files define templates, seed facts, rules, and queries as
S-expressions. The compiler in `cmd/gessc` and the loader in the `dsl` package
accept the same language. This reference describes every form the parser
accepts and the limits it enforces.

For the recommended workflow around `.gess` files, see `TUTORIAL.md`. For
the Go APIs that load them, see `go-api.md`.

## Lexical rules

- A `.gess` file is a sequence of parenthesized lists made of atoms, quoted
  strings, and nested lists.
- Comments start with `;` and run to the end of the line.
- Strings use double quotes and support the escapes `\n`, `\r`, `\t`, `\"`,
  and `\\`.
- Atoms are parsed as values: `TRUE` and `FALSE` (case-insensitive) are
  booleans, `NULL` and `NIL` are null, base-10 integers are integers, and
  atoms containing `.`, `e`, or `E` that parse as numbers are floats. Every
  other bare atom is a string, so `(segment vip)` and `(segment "vip")` are
  equivalent.
- Keywords such as `defrule` and `slot`, template names, slot names, and
  module names are case-sensitive. Only boolean and null literals and slot
  type names are case-insensitive.

Format `.gess` files with `gessfmt` (see `cli.md`). The canonical layout uses
two-space indentation, a blank line between top-level forms, and expanded
closing parentheses for long forms.

## Top-level forms

A `.gess` file contains only these top-level forms. Any other form is
rejected with an `unsupported top-level form` error.

- `(defmodule NAME ...)`: declare a module.
- `(deftemplate NAME ...)`: declare a fact template.
- `(deffacts NAME fact...)`: declare seed facts.
- `(defrule NAME ... => ...)`: declare a rule.
- `(defquery NAME ...)`: declare a query.

Templates and modules are loaded before facts, rules, and queries, so
definition order within a file doesn't matter across those groups.

### Module-qualified names

Template, rule, and query names, and the template names in patterns and
`assert` forms, can be qualified with a module prefix: `BILLING::invoice`.
Unqualified names belong to the `MAIN` module.

## `defmodule`

```cl
(defmodule TRIAGE
  (declare (auto-focus TRUE))
)
```

`defmodule` declares a module for organizing templates and rules. The only
recognized declaration is `(auto-focus TRUE|FALSE)`: when true, activations of
the module's rules push the module onto the focus stack automatically. See
`advanced.md` for focus semantics.

## `deftemplate`

```cl
(deftemplate fulfillment-route
  (declare (duplicate-policy unique-key) (duplicate-key order))
  (slot order (type STRING) (required TRUE))
  (slot lane (type STRING) (required TRUE))
  (slot warehouse (type STRING) (required TRUE))
)
```

A template declares a fact shape: a name plus slots. Facts asserted against a
template are validated against its slots.

### Slots

`(slot name attr...)` accepts these attributes:

- `(type KIND)`: the value kind. Accepted kinds, case-insensitive: `STRING`,
  `INTEGER` (alias `INT`), `FLOAT` (alias `NUMBER`), `BOOLEAN` (alias
  `BOOL`), `LIST`, `MAP`, and `ANY`. Slots without a type accept any value.
- `(required TRUE|FALSE)`: whether asserts must provide the slot.
- `(default VALUE)`: a scalar default used when the slot is omitted.

There is no `multislot` form; use a `LIST`-typed slot instead. Unknown slot
attributes and unknown items inside `deftemplate` are ignored, so misspelled
attributes don't fail loading.

### Template declarations

`(declare ...)` inside a template accepts:

- `(duplicate-policy structural|allow|unique-key)`: how duplicate facts are
  handled. `allow` (the `.gess` default) permits duplicates, `structural`
  deduplicates facts with identical field values, and `unique-key`
  deduplicates by the fields named in `duplicate-key`.
- `(duplicate-key field...)`: the key fields for the `unique-key` policy.
- `(backchain-reactive TRUE|FALSE)`: enable backward chaining for the
  template. Compilation also creates a demand template named
  `need-<template>` that rules can match to prove goals on demand. See
  `advanced.md`.
- `(key VALUE)`: set an explicit template key used by
  `session.AssertTemplate` and generated code.

## `deffacts`

```cl
(deffacts seed-orders
  (order (id "O-100") (customer "C-100") (sku "SKU-1"))
  (customer (id "C-100") (segment "vip"))
)
```

`deffacts` declares seed facts as fact literals: a template name followed by
`(field value)` pairs with scalar values. Seed facts aren't asserted
automatically. They become the initial facts returned by the generated build
function (or `dsl.InitialFacts`), and the host passes them to the session with
`session.WithInitialFacts`.

## `defrule`

```cl
(defrule route-vip-order
  ?order <- (order (customer ?customer) (sku ?sku))
  (customer (id ?customer) (segment "vip"))
  (inventory (sku ?sku) (available TRUE) (warehouse ?warehouse))
  =>
  (assert (fulfillment-route
    (order ?order:id)
    (lane "expedite")
    (warehouse ?warehouse))
  )
)
```

A rule has a left-hand side of conditions, a mandatory `=>` separator, and a
right-hand side of actions.

### Rule declarations

`(declare ...)` items anywhere on the left-hand side accept:

- `(salience N)`: an integer priority. Higher salience activations fire
  first within a module.
- `(auto-focus TRUE|FALSE)`: when true, an activation of this rule pushes its
  module onto the focus stack.

### Patterns

A pattern is `([MODULE::]template (slot value)...)`. Every slot constraint is
a two-element `(field value)` list; positional constraints aren't supported.
Slot values can be:

- A scalar literal (string, integer, float, boolean, or null), which
  constrains the field to equal that value.
- A variable `?name`. The first occurrence binds the variable to the field;
  later occurrences of the same variable, in the same or other patterns,
  become equality join constraints. Hyphens in variable names normalize to
  underscores, so `?account-id` and `?account_id` are the same variable.

Bind the whole fact with `?binding <- (pattern)`. A bound fact's fields are
readable elsewhere as a projection `?binding:field`, usable in expressions,
`assert` values, `call` arguments, and query returns.

The pattern language intentionally stops there: per-slot predicate
constraints, connective constraints, and multifield patterns aren't part of
the language. Use `test` conditions for anything beyond equality.

### Condition forms

- `(and cond...)`: groups conditions; nested `and` flattens.
- `(or cond...)`: matches when any branch matches. Variables bound inside a
  branch stay local to that branch.
- `(not cond)`: matches when no fact matches the condition.
- `(exists cond)`: matches when at least one fact matches, producing a single
  activation regardless of how many facts match.
- `(forall domain requirement)`: matches when every fact matching the domain
  condition also has a matching requirement fact.
- `(test EXPR)`: evaluates an expression over bound variables. A `test` over
  an aggregate result variable is rejected; constrain aggregate results in Go
  after querying, or assert them into a fact and match that.
- `(accumulate input (bind ?name (agg ...))...)`: aggregates over the facts
  matching the input condition. See the next section.

### Aggregates

```cl
(defrule summarize-critical-vulnerabilities
  (accumulate
    (vulnerability (severity "critical") (score ?score))
    (bind ?count (count))
    (bind ?total (sum ?score))
  )
  =>
  (assert (critical-vulnerability-summary
    (severity "critical")
    (count ?count)
    (total ?total)
  )
  )
)
```

`accumulate` takes one input condition and one or more `(bind ?name (agg))`
bindings. The aggregate forms are `(count)`, `(sum EXPR)`, `(min EXPR)`,
`(max EXPR)`, `(collect EXPR)`. Result variables are usable in the rule's
actions and in later expressions.

### Actions

The right-hand side accepts exactly these actions; anything else is rejected
with an `unsupported action` error:

- `(assert (template (field value)...))`: assert a fact. Values are scalar
  literals, variables, projections such as `?order:id`, or aggregate result
  variables. Nested expressions aren't allowed as values.
- `(assert-logical (template (field value)...))`: assert a fact with logical
  support from the activation's matched facts. The fact is retracted
  automatically when its support goes away. See `advanced.md`.
- `(focus MODULE)`: push a module onto the focus stack.
- `(pop-focus)`: pop the current focus.
- `(clear-focus)`: clear the focus stack back to `MAIN`.
- `(halt)`: stop the current run.
- `(call name arg...)`: invoke a host function registered through
  `dsl.Registry`. With arguments, `name` must be registered in
  `Registry.Calls`; with no arguments, in `Registry.Actions`. Arguments are
  scalar literals, variables, or projections.

`retract`, `modify`, `bind`, and `printout` aren't `.gess` actions. Rules
that need side effects or fact mutation beyond `assert` call registered host
functions, which receive an action context with the full mutation API.

## `defquery`

```cl
(defquery routes-by-lane
  (declare (variables ?lane))
  ?route <- (fulfillment-route
    (lane ?lane)
    (order ?order)
    (warehouse ?warehouse)
  )
  (return
    (order ?order)
    (warehouse ?warehouse)
  )
)
```

A query has optional parameters, a body of conditions, and optional returns:

- `(declare (variables ?a ?b ...))` declares query parameters. Callers
  pass them by name through `session.QueryArgs`, keyed without the `?`
  prefix.
- The body accepts the same condition forms as a rule's left-hand side,
  including fact bindings, `not`, `exists`, `forall`, `or`, `test`, and
  `accumulate`. A pattern variable with the same name as a parameter becomes
  an equality constraint against the argument.
- `(return (alias value)...)` names the columns of each result row. Values
  are full expressions: variables, projections, literals, comparisons, and
  function calls. Multiple `return` forms concatenate.

## Expressions

Expressions appear in `test` conditions, aggregate arguments, and query
returns:

- Atoms: scalar literals, bound variables `?name`, projections
  `?binding:field`, and query parameters.
- Comparisons, each with exactly two operands: `=`, `!=` (alias `<>`), `<`,
  `<=`, `>`, `>=`.
- Boolean forms: `(and e...)`, `(or e...)`, `(not e)`.
- Function calls: `(name arg...)` for any other head, resolved against pure
  functions registered through `dsl.Registry.Functions`.

There are no built-in arithmetic operators. Register a pure function and call
it by name when a rule needs computation.

## Host integration through the registry

`.gess` files reference host Go code by name; the `dsl.Registry` supplies the
implementations when the ruleset is built:

- `Calls`: named functions for `(call name arg...)` actions, with signature
  `func(rules.ActionContext, []rules.Value) error`.
- `Actions`: named zero-argument actions for `(call name)`.
- `Functions`: pure functions callable from expressions.

Compilation fails when a `.gess` file references a name the registry doesn't
provide, so missing host integrations surface at build time rather than when
a rule fires.

## Errors and limits

The loader reports precise `file:line:column` positions for syntax errors.
Notable rejections, verbatim from the loader:

- `unsupported top-level form`: only the five forms listed earlier are
  accepted.
- `slot constraint must be (field value)`: no positional patterns.
- `unsupported action`: the right-hand side accepts only the actions listed
  earlier.
- `unsupported aggregate`: only `count`, `sum`, `min`, `max`, and `collect`.
- `test over aggregate result ... is not supported by the graph runtime`.
- `assert values must be scalar expressions`: no nested expressions in
  `assert` or `call` values.
- `unregistered action`: a `call` names a function the registry doesn't
  provide.

Unknown `declare` keys and unknown slot attributes are ignored rather than
rejected, so check spelling carefully when a declaration seems to have no
effect.
