package rules

// FieldSpec declares one template field: its name, kind, whether it's
// required, an optional default, and an optional closed set of allowed values.
type FieldSpec struct {
	Name          string
	Kind          ValueKind
	Required      bool
	Default       any
	HasDefault    bool
	AllowedValues []any
}

// CloneFieldSpec returns a defensive copy of field.
func CloneFieldSpec(field FieldSpec) FieldSpec {
	out := field
	out.Default = cloneSpecValue(field.Default)
	out.AllowedValues = make([]any, len(field.AllowedValues))
	for i, value := range field.AllowedValues {
		out.AllowedValues[i] = cloneSpecValue(value)
	}
	return out
}

// CloneFieldSpecs returns a defensive copy of fields.
func CloneFieldSpecs(fields []FieldSpec) []FieldSpec {
	out := make([]FieldSpec, len(fields))
	for i, field := range fields {
		out[i] = CloneFieldSpec(field)
	}
	return out
}

// TemplateSpec declares a fact template: its name, module, key, fields,
// duplicate policy, and whether it's backward-chaining reactive.
type TemplateSpec struct {
	Name              string
	Module            ModuleName
	Key               TemplateKey
	CompatibilityKey  TemplateKey
	Source            SourceSpan
	GessSource        string
	Fields            []FieldSpec
	DuplicatePolicy   DuplicatePolicy
	DuplicateKeyNames []string
	BackchainReactive bool
}

// Template is the compiled, inspectable form of a TemplateSpec.
type Template struct {
	NameValue              string
	ModuleValue            ModuleName
	KeyValue               TemplateKey
	CompatibilityKeyValue  TemplateKey
	FieldValues            []FieldSpec
	DuplicatePolicyValue   DuplicatePolicy
	DuplicateKeyValues     []string
	BackchainReactiveValue bool
	BackchainDemandKey     TemplateKey
	BackchainDemandValue   bool
	BackchainSourceKey     TemplateKey
	SourceSpan             SourceSpan
	GessSourceText         string
}

func (t Template) Name() string {
	return t.NameValue
}

func (t Template) Module() ModuleName {
	return t.ModuleValue
}

func (t Template) QualifiedName() QualifiedName {
	return QualifiedName{Module: t.ModuleValue, Name: t.NameValue}.normalized()
}

func (t Template) Key() TemplateKey {
	return t.KeyValue
}

func (t Template) CompatibilityKey() TemplateKey {
	return t.CompatibilityKeyValue
}

func (t Template) DuplicatePolicy() DuplicatePolicy {
	return t.DuplicatePolicyValue
}

func (t Template) DuplicateKeys() []string {
	return append([]string(nil), t.DuplicateKeyValues...)
}

func (t Template) BackchainReactive() bool {
	return t.BackchainReactiveValue
}

func (t Template) Source() SourceSpan {
	return t.SourceSpan
}

func (t Template) GessSource() string {
	return t.GessSourceText
}

func (t Template) BackchainDemandTemplateKey() (TemplateKey, bool) {
	if !t.BackchainReactiveValue || t.BackchainDemandKey == "" {
		return "", false
	}
	return t.BackchainDemandKey, true
}

func (t Template) IsBackchainDemandTemplate() bool {
	return t.BackchainDemandValue
}

func (t Template) BackchainSourceTemplateKey() (TemplateKey, bool) {
	if !t.BackchainDemandValue || t.BackchainSourceKey == "" {
		return "", false
	}
	return t.BackchainSourceKey, true
}

func (t Template) Fields() []FieldSpec {
	return CloneFieldSpecs(t.FieldValues)
}

// CloneTemplate returns a defensive copy of t.
func CloneTemplate(t Template) Template {
	out := t
	out.FieldValues = CloneFieldSpecs(t.FieldValues)
	out.DuplicateKeyValues = append([]string(nil), t.DuplicateKeyValues...)
	return out
}

// CloneTemplates returns a defensive copy of templates.
func CloneTemplates(templates []Template) []Template {
	out := make([]Template, len(templates))
	for i, template := range templates {
		out[i] = CloneTemplate(template)
	}
	return out
}
