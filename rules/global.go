package rules

import "strings"

// GlobalSpec declares a named, typed global with an optional default value.
type GlobalSpec struct {
	Name        string
	Kind        ValueKind
	Default     any
	HasDefault  bool
	Description string
}

// CloneGlobalSpec returns a defensive copy of s.
func CloneGlobalSpec(s GlobalSpec) GlobalSpec {
	s.Name = strings.TrimSpace(s.Name)
	if s.Kind == valueKindUnknown {
		s.Kind = ValueAny
	}
	s.Default = cloneSpecValue(s.Default)
	return s
}

// Global is the compiled, inspectable form of a GlobalSpec.
type Global struct {
	NameValue       string
	KindValue       ValueKind
	DefaultValue    Value
	HasDefaultValue bool
	DescriptionText string
	Order           int
}

func (g Global) Name() string {
	return g.NameValue
}

func (g Global) Kind() ValueKind {
	return g.KindValue
}

func (g Global) Default() (Value, bool) {
	if !g.HasDefaultValue {
		return Value{}, false
	}
	return CloneValue(g.DefaultValue), true
}

func (g Global) Description() string {
	return g.DescriptionText
}

func (g Global) DeclarationOrder() int {
	return g.Order
}

// CloneGlobal returns a defensive copy of g.
func CloneGlobal(g Global) Global {
	out := g
	out.DefaultValue = CloneValue(g.DefaultValue)
	return out
}
