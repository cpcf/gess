package gess

import "maps"

type ValueKind string

const (
	ValueAny    ValueKind = "any"
	ValueNull   ValueKind = "null"
	ValueBool   ValueKind = "bool"
	ValueInt    ValueKind = "int"
	ValueFloat  ValueKind = "float"
	ValueString ValueKind = "string"
	ValueList   ValueKind = "list"
	ValueMap    ValueKind = "map"
)

type Value struct {
	kind ValueKind
	data any
}

func NullValue() Value {
	return Value{kind: ValueNull}
}

func (v Value) Kind() ValueKind {
	if v.kind == "" {
		return ValueAny
	}
	return v.kind
}

type Fields map[string]Value

func cloneFields(in Fields) Fields {
	if len(in) == 0 {
		return nil
	}
	out := make(Fields, len(in))
	maps.Copy(out, in)
	return out
}
