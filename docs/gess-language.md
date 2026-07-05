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

:::caution
This is an exhaustive list, not a starting point. Readers coming from
CLIPS or Jess should not expect other top-level forms to work.
:::

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

:::caution
The pattern language intentionally stops there: per-slot predicate
constraints, connective constraints, and multifield patterns aren't part of
the language. Use `test` conditions for anything beyond equality.
:::

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
  literals, variables, projections such as `?order:id`, aggregate result
  variables, or built-in function calls such as `(+ ?a ?b)`.
- `(assert-logical (template (field value)...))`: assert a fact with logical
  support from the activation's matched facts. The fact is retracted
  automatically when its support goes away. See `advanced.md`.
- `(retract ?binding)`: retract the fact bound to `?binding` on the
  left-hand side. The target must be a fact binding, not a value or parameter.
- `(modify ?binding (set (field value)...) (unset field...))`: modify the
  bound fact in place, preserving its fact identity. `set` overwrites the named
  slots (same value grammar as `assert`); `unset` clears the named slots back
  to their template defaults. Either block may be omitted, but at least one
  slot must change. See `advanced.md` for identity and self-match behavior.
- `(bind ?name expr)`: evaluate `expr` once and bind `?name` for use by
  *later* actions in the same rule's right-hand side. Binds are ordered and
  single-assignment; they never create a fact and are not visible on the
  left-hand side. `expr` is any expression, including built-in function calls.
- `(emit value...)`: write the concatenated display forms of the values to the
  session output writer (set with `session.WithOutputWriter`; output is
  discarded when unset). This is the Gess-native output verb; it has no CLIPS
  router argument.
- `(focus MODULE)`: push a module onto the focus stack.
- `(pop-focus)`: pop the current focus.
- `(clear-focus)`: clear the focus stack back to `MAIN`.
- `(halt)`: stop the current run.
- `(call name arg...)`: invoke a host function registered through
  `dsl.Registry`. With arguments, `name` must be registered in
  `Registry.Calls`; with no arguments, in `Registry.Actions`. Arguments are
  scalar literals, variables, projections, or built-in function calls.

:::caution
Right-hand-side control flow (`if`, `while`, `foreach`, `progn`) and arbitrary
in-rule code remain host-only: compose that behavior with registered `(call
...)` actions, which receive an action context with the full mutation API.
Modifying a fact that is held up purely by logical support is rejected — a
logically-supported fact is entailed by its support, so change the support
instead. CLIPS `printout`'s router model is not carried over; use `emit`.
:::

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
- Function calls: `(name arg...)` for any other head, resolved first against
  the built-in functions below, then against pure functions registered through
  `dsl.Registry.Functions`.

### Built-in functions

A curated, deterministic set of functions is always available without host
registration. They are fixed-arity: the arithmetic and string operators that
are variadic in CLIPS/Jess are exposed as two-argument forms, so nest calls to
combine more than two operands (for example `(+ ?a (+ ?b ?c))`).

- Arithmetic: `+`, `-`, `*`, `/`, `mod`, `abs`, `min`, `max`. A float operand
  promotes the result to float; otherwise integer arithmetic stays integer.
  `/` always returns a float. Division or `mod` by zero raises a typed runtime
  error rather than panicking or producing infinity.
- Numeric conversion: `integer` (truncates toward zero), `float`.
- Numeric predicates: `numberp`, `integerp`, `floatp`.
- Strings: `str-cat`, `str-length`, `sub-string` (`(sub-string string start
  end)`, zero-based, half-open `[start, end)`), `upcase`, `lowcase`.
- Type predicates: `stringp`, `booleanp`, `nullp`.

Built-in names are reserved: a host function or `deffunction` that reuses a
built-in name fails to compile with a `shadows a built-in` error. Comparison
operators (`=`, `!=`, `<`, `<=`, `>`, `>=`) are the comparison forms above, not
functions. No built-in reads wall-clock time or randomness, so results stay
deterministic.

Built-in and registered functions may be used anywhere an expression is
allowed, including as `assert`/`modify`/`bind`/`emit`/`call` action values (for
example `(modify ?f (set (total (+ ?f:subtotal ?f:tax))))`). Action values
compile to name-resolved expressions, so they work identically whether the
ruleset is loaded at runtime or compiled to Go with `gessc`.

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
- `... target ... is not a bound fact`: `retract`/`modify` must name a fact
  binding, not a value or query parameter.
- `modify requires at least one set or unset field`: an empty `modify`.
- `bind ... is already bound` / `unknown variable`: RHS binds are
  single-assignment and cannot be forward-referenced.
- `function shadows a built-in`: a host function or `deffunction` reuses a
  reserved built-in name.
- `divide by zero`: `/` or `mod` with a zero divisor, raised when the action
  fires.
- `unregistered action`: a `call` names a function the registry doesn't
  provide.

:::caution
Unknown `declare` keys and unknown slot attributes are ignored rather than
rejected, so check spelling carefully when a declaration seems to have no
effect.
:::

## Next steps

- [Command-line tools](cli.md) to compile `.gess` files with `gessc`.
- [The tutorial](TUTORIAL.md) for a worked `.gess` example end to end.
- [Examples map](examples.md) for `.gess` examples organized by feature.
