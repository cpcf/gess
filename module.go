package gess

import "strings"

type ModuleSpec struct {
	Name        ModuleName
	Description string
	AutoFocus   *bool
}

func (s ModuleSpec) clone() ModuleSpec {
	out := s
	if s.AutoFocus != nil {
		autoFocus := *s.AutoFocus
		out.AutoFocus = &autoFocus
	}
	return out
}

type Module struct {
	name        ModuleName
	description string
	autoFocus   *bool
}

func (m Module) Name() ModuleName {
	return m.name
}

func (m Module) Description() string {
	return m.description
}

func (m Module) AutoFocusDefault() (bool, bool) {
	if m.autoFocus == nil {
		return false, false
	}
	return *m.autoFocus, true
}

func (m Module) clone() Module {
	out := m
	if m.autoFocus != nil {
		autoFocus := *m.autoFocus
		out.autoFocus = &autoFocus
	}
	return out
}

func compileModuleSpec(spec ModuleSpec) (Module, error) {
	normalized := spec.clone()
	normalized.Name = ModuleName(strings.TrimSpace(string(normalized.Name)))
	if normalized.Name.IsZero() {
		return Module{}, &ValidationError{Reason: "module name is required"}
	}
	return Module{
		name:        normalized.Name,
		description: normalized.Description,
		autoFocus:   normalized.AutoFocus,
	}, nil
}

func implicitMainModule() Module {
	return Module{name: MainModule}
}
