package gess

import "strings"

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

type NameRef struct {
	Module ModuleName
	Name   string
}

func Ref(name string) NameRef {
	return NameRef{Name: name}
}

func ModuleRef(module ModuleName, name string) NameRef {
	return NameRef{Module: module, Name: name}
}

func (r NameRef) normalized(author ModuleName) QualifiedName {
	module := normalizeModuleName(r.Module)
	if r.Module.IsZero() {
		module = normalizeModuleName(author)
	}
	return QualifiedName{
		Module: module,
		Name:   strings.TrimSpace(r.Name),
	}
}

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

func (m Module) spec() ModuleSpec {
	out := ModuleSpec{
		Name:        m.name,
		Description: m.description,
	}
	if m.autoFocus != nil {
		autoFocus := *m.autoFocus
		out.AutoFocus = &autoFocus
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

func normalizeModuleName(name ModuleName) ModuleName {
	name = ModuleName(strings.TrimSpace(string(name)))
	if name.IsZero() {
		return MainModule
	}
	return name
}

func sameModuleDeclaration(left, right Module) bool {
	if left.name != right.name || left.description != right.description {
		return false
	}
	if left.autoFocus == nil || right.autoFocus == nil {
		return left.autoFocus == nil && right.autoFocus == nil
	}
	return *left.autoFocus == *right.autoFocus
}

func compileWorkspaceModules(specs []ModuleSpec) ([]Module, map[ModuleName]Module, []ModuleName, error) {
	modules := map[ModuleName]Module{
		MainModule: implicitMainModule(),
	}
	moduleOrder := []ModuleName{MainModule}
	explicit := make(map[ModuleName]struct{}, len(specs))

	for _, spec := range specs {
		module, err := compileModuleSpec(spec)
		if err != nil {
			return nil, nil, nil, err
		}

		if _, exists := explicit[module.name]; exists {
			if sameModuleDeclaration(modules[module.name], module) {
				continue
			}
			return nil, nil, nil, &ValidationError{Reason: "duplicate module"}
		}

		if _, exists := modules[module.name]; !exists {
			moduleOrder = append(moduleOrder, module.name)
		}
		modules[module.name] = module.clone()
		explicit[module.name] = struct{}{}
	}

	compiledModules := make([]Module, 0, len(moduleOrder))
	for _, name := range moduleOrder {
		compiledModules = append(compiledModules, modules[name].clone())
	}
	return compiledModules, modules, moduleOrder, nil
}

func validateModuleReference(modules map[ModuleName]Module, name ModuleName) (ModuleName, bool) {
	normalized := normalizeModuleName(name)
	_, ok := modules[normalized]
	return normalized, ok
}
