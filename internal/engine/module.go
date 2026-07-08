package engine

import (
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type QualifiedName = gessrules.QualifiedName
type NameRef = gessrules.NameRef

func Ref(name string) NameRef {
	return gessrules.Ref(name)
}

func ModuleRef(module ModuleName, name string) NameRef {
	return gessrules.ModuleRef(module, name)
}

func normalizeQualifiedName(n QualifiedName) QualifiedName {
	return QualifiedName{
		Module: normalizeModuleName(n.Module),
		Name:   strings.TrimSpace(n.Name),
	}
}

func normalizeNameRef(r NameRef, author ModuleName) QualifiedName {
	module := normalizeModuleName(r.Module)
	if r.Module.IsZero() {
		module = normalizeModuleName(author)
	}
	return QualifiedName{
		Module: module,
		Name:   strings.TrimSpace(r.Name),
	}
}

type ModuleSpec = gessrules.ModuleSpec

func cloneModuleSpec(s ModuleSpec) ModuleSpec {
	return gessrules.CloneModuleSpec(s)
}

type Module = gessrules.Module

func cloneModule(m Module) Module {
	return gessrules.CloneModule(m)
}

func moduleSpec(m Module) ModuleSpec {
	return gessrules.ModuleSpecFromModule(m)
}

func compileModuleSpec(spec ModuleSpec) (Module, error) {
	normalized := cloneModuleSpec(spec)
	normalized.Name = ModuleName(strings.TrimSpace(string(normalized.Name)))
	if normalized.Name.IsZero() {
		return Module{}, &ValidationError{Reason: "module name is required"}
	}
	return Module{
		NameValue:       normalized.Name,
		DescriptionText: normalized.Description,
		AutoFocusValue:  normalized.AutoFocus,
	}, nil
}

func implicitMainModule() Module {
	return Module{NameValue: MainModule}
}

func normalizeModuleName(name ModuleName) ModuleName {
	name = ModuleName(strings.TrimSpace(string(name)))
	if name.IsZero() {
		return MainModule
	}
	return name
}

func sameModuleDeclaration(left, right Module) bool {
	if left.NameValue != right.NameValue || left.DescriptionText != right.DescriptionText {
		return false
	}
	if left.AutoFocusValue == nil || right.AutoFocusValue == nil {
		return left.AutoFocusValue == nil && right.AutoFocusValue == nil
	}
	return *left.AutoFocusValue == *right.AutoFocusValue
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

		if _, exists := explicit[module.NameValue]; exists {
			if sameModuleDeclaration(modules[module.NameValue], module) {
				continue
			}
			return nil, nil, nil, &ValidationError{Reason: "duplicate module"}
		}

		if _, exists := modules[module.NameValue]; !exists {
			moduleOrder = append(moduleOrder, module.NameValue)
		}
		modules[module.NameValue] = cloneModule(module)
		explicit[module.NameValue] = struct{}{}
	}

	compiledModules := make([]Module, 0, len(moduleOrder))
	for _, name := range moduleOrder {
		compiledModules = append(compiledModules, cloneModule(modules[name]))
	}
	return compiledModules, modules, moduleOrder, nil
}

func validateModuleReference(modules map[ModuleName]Module, name ModuleName) (ModuleName, bool) {
	normalized := normalizeModuleName(name)
	_, ok := modules[normalized]
	return normalized, ok
}
