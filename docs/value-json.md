# Value JSON

The `github.com/cpcf/gess/scenario` package defines the lossless JSON value
contract shared by Gess scenario and report documents, the Workbench, MCP, and
regression tooling. The contract preserves all seven `rules.Value` kinds,
including the full `int64` range and negative floating-point zero.

Every value is a kind-tagged envelope. These are the exact shapes:

| Gess kind | JSON envelope |
| --- | --- |
| `ValueNull` | `{"kind":"null"}` |
| `ValueBool` | `{"kind":"bool","bool":true}` |
| `ValueInt` | `{"kind":"int","int":"9223372036854775807"}` |
| `ValueFloat` | `{"kind":"float","float":"-0"}` |
| `ValueString` | `{"kind":"string","string":"example"}` |
| `ValueList` | `{"kind":"list","list":[...typed values...]}` |
| `ValueMap` | `{"kind":"map","map":[...sorted entries...]}` |

The `kind` names and payload field names are lowercase and case-sensitive. A
list item and a map entry's `value` must be another complete typed envelope;
ordinary JSON primitives are not valid inside a typed collection.

## Numbers

An integer payload is a base-10 string in the signed 64-bit range. Its spelling
must equal `strconv.FormatInt(value, 10)`, so `"0"`, `"-1"`, and
`"-9223372036854775808"` are canonical, while `"-0"`, `"+1"`, and `"01"`
are rejected.

A float payload is the shortest round-trippable binary64 string produced by
`strconv.FormatFloat(value, 'g', -1, 64)`. Only finite values are valid. This
means positive and negative zero are distinct canonical strings, `"0"` and
`"-0"`; negative zero retains its sign through a round trip. Spellings such as
`"1.0"` and `"1e0"` are rejected when the canonical spelling is `"1"`, and
`"NaN"` and infinity are always rejected.

Keeping both numeric payloads in strings prevents JavaScript and other JSON
consumers from rounding large integers. The `kind` tag also preserves the
difference between an integer and a float whose canonical payloads are both
`"1"`.

## Lists and maps

Lists preserve authored order:

```json
{
  "kind": "list",
  "list": [
    { "kind": "string", "string": "first" },
    { "kind": "int", "int": "2" }
  ]
}
```

Maps use an entry array rather than a JSON object so ordering is explicit:

```json
{
  "kind": "map",
  "map": [
    { "key": "", "value": { "kind": "null" } },
    { "key": "answer", "value": { "kind": "int", "int": "42" } }
  ]
}
```

Entries are sorted by key in strictly increasing lexicographic UTF-8 byte order,
the order used by Go string comparisons. Empty keys are valid and sort before
nonempty keys. The decoder rejects duplicate or out-of-order keys. Every entry
has exactly a string `key` and a typed `value`.

## Go API

`scenario.Value` is the JSON-facing wrapper for a `rules.Value`.
`scenario.NewValue` wraps a rules value, and `RulesValue` returns the wrapped
value. `Value` implements `json.Marshaler` and `json.Unmarshaler` for embedding
in larger documents. `scenario.MarshalValue` and `scenario.UnmarshalValue`
provide the same encoding and decoding directly for standalone JSON:

```go
func NewValue(value rules.Value) Value
func (v Value) RulesValue() rules.Value
func MarshalValue(value rules.Value) ([]byte, error)
func UnmarshalValue(data []byte) (rules.Value, error)
```

`NewValue`, `RulesValue`, and successful decoding use defensive copies of list
and map storage.

```go
original, err := rules.NewValue(map[string]any{
	"limit": int64(9223372036854775807),
	"items": []any{"a", math.Copysign(0, -1)},
})
if err != nil {
	return err
}

wrapped := scenario.NewValue(original)
same := wrapped.RulesValue()

data, err := scenario.MarshalValue(same)
if err != nil {
	return err
}
decoded, err := scenario.UnmarshalValue(data)
```

Every encoding or decoding failure wraps `scenario.ErrInvalidValueJSON`, so
callers can classify it with `errors.Is`. Rejected input includes:

- an unknown or missing `kind`;
- a duplicate object member or a missing, extra, ambiguous, or wrongly typed
  payload field;
- a numeric string that is not canonical, is out of range, or is not finite;
- invalid UTF-8 or an unpaired Unicode surrogate in a string or map key;
- an ordinary JSON value where a nested typed envelope is required;
- a malformed map entry or duplicate or unsorted map key; and
- non-whitespace data after the one top-level value.

Marshaling emits compact deterministic bytes. Map construction and iteration
order cannot affect the result: values with the same kinds and contents produce
byte-identical JSON.

## Tool input and output

MCP tool inputs continue to accept ordinary JSON values for convenience.
Integral JSON numbers become Gess integers when representable, fractional
numbers become floats, and the conversion applies recursively to input arrays
and objects. Clients that need exact kind selection, the full `int64` range, or
negative zero can send the typed envelope instead.

An MCP object whose `kind` is one of the seven recognized names is decoded as a
strict typed envelope. For ordinary-input compatibility, an object with no
`kind`, or with an unrecognized `kind`, remains an ordinary map. The latter
differs deliberately from direct `scenario.UnmarshalValue`, which accepts only
a typed envelope and rejects every unknown kind.

All Gess values in the custom MCP output documents use the typed envelope. The
ordinary-input compatibility rule is not an alternate output format and does
not make untagged values valid input to `scenario.UnmarshalValue`.

## Explain v1 compatibility

The existing `gessExplainSchema: 1` Explain, WhyNot, and WhatIf documents are a
one-way compatibility exception. Their integer values remain JSON numbers and
carry the precision limitation documented in [Explain JSON](explain-json.md).
They are export-only documents, not input to the shared bidirectional value
decoder.

Changing those v1 fields to typed envelopes would break their published wire
contract. Explain, WhyNot, or WhatIf can adopt the shared encoding only in a
new explain schema version with an explicit schema bump.
