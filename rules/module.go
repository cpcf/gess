package rules

import "strings"

// QualifiedName is a module-qualified name, rendered as "MODULE.Name".
type QualifiedName struct {
	Module ModuleName
	Name   string
}

func (n QualifiedName) normalized() QualifiedName {
	return QualifiedName{
		Module: normalizeModuleName(n.Module),
		Name:   strings.TrimSpace(n.Name),
	}
}

func (n QualifiedName) String() string {
	normalized := n.normalized()
	if normalized.Name == "" {
		return normalized.Module.String()
	}
	return normalized.Module.String() + "." + normalized.Name
}

// NameRef is an unresolved reference to a named definition.
type NameRef struct {
	Module ModuleName
	Name   string
}

// Ref builds an unqualified NameRef.
func Ref(name string) NameRef {
	return NameRef{Name: name}
}

// ModuleRef builds a NameRef explicitly qualified to module.
func ModuleRef(module ModuleName, name string) NameRef {
	return NameRef{Module: module, Name: name}
}

func normalizeModuleName(name ModuleName) ModuleName {
	name = ModuleName(strings.TrimSpace(string(name)))
	if name == "" {
		return MainModule
	}
	return name
}

// ModuleSpec declares a module: its Name and optional default focus behavior.
type ModuleSpec struct {
	Name        ModuleName
	Description string
	AutoFocus   *bool
}

// CloneModuleSpec returns a defensive copy of s.
func CloneModuleSpec(s ModuleSpec) ModuleSpec {
	out := s
	if s.AutoFocus != nil {
		autoFocus := *s.AutoFocus
		out.AutoFocus = &autoFocus
	}
	return out
}

// Module is the compiled, inspectable form of a ModuleSpec.
type Module struct {
	NameValue       ModuleName
	DescriptionText string
	AutoFocusValue  *bool
}

func (m Module) Name() ModuleName {
	return m.NameValue
}

func (m Module) Description() string {
	return m.DescriptionText
}

func (m Module) AutoFocusDefault() (bool, bool) {
	if m.AutoFocusValue == nil {
		return false, false
	}
	return *m.AutoFocusValue, true
}

// CloneModule returns a defensive copy of m.
func CloneModule(m Module) Module {
	out := m
	if m.AutoFocusValue != nil {
		autoFocus := *m.AutoFocusValue
		out.AutoFocusValue = &autoFocus
	}
	return out
}

// ModuleSpecFromModule returns a ModuleSpec equivalent to m.
func ModuleSpecFromModule(m Module) ModuleSpec {
	out := ModuleSpec{
		Name:        m.NameValue,
		Description: m.DescriptionText,
	}
	if m.AutoFocusValue != nil {
		autoFocus := *m.AutoFocusValue
		out.AutoFocus = &autoFocus
	}
	return out
}
