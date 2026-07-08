package rules

import "strings"

// FactTarget names the facts a condition matches.
type FactTarget struct {
	kind        FactTargetKind
	ref         NameRef
	templateKey TemplateKey
}

// DynamicFact builds a target matching dynamic facts with name.
func DynamicFact(name string) FactTarget {
	return FactTarget{kind: FactTargetDynamic, ref: Ref(name)}.Normalized()
}

// DynamicFactIn builds a target matching dynamic facts in module.
func DynamicFactIn(module ModuleName, name string) FactTarget {
	return FactTarget{kind: FactTargetDynamic, ref: ModuleRef(module, name)}.Normalized()
}

// TemplateFact builds a target matching facts of the named template.
func TemplateFact(name string) FactTarget {
	return FactTarget{kind: FactTargetTemplate, ref: Ref(name)}.Normalized()
}

// TemplateFactIn builds a target matching facts of the named template in module.
func TemplateFactIn(module ModuleName, name string) FactTarget {
	return FactTarget{kind: FactTargetTemplate, ref: ModuleRef(module, name)}.Normalized()
}

// TemplateKeyFact builds a target matching facts of the template key.
func TemplateKeyFact(key TemplateKey) FactTarget {
	return FactTarget{kind: FactTargetTemplateKey, templateKey: key}.Normalized()
}

func (t FactTarget) Kind() FactTargetKind {
	return t.kind
}

func (t FactTarget) Ref() NameRef {
	return t.ref
}

func (t FactTarget) TemplateKey() TemplateKey {
	return t.templateKey
}

// Normalized returns t with trimmed names and inferred kind where possible.
func (t FactTarget) Normalized() FactTarget {
	out := t
	out.ref.Name = strings.TrimSpace(out.ref.Name)
	out.ref.Module = ModuleName(strings.TrimSpace(string(out.ref.Module)))
	out.templateKey = TemplateKey(strings.TrimSpace(string(out.templateKey)))
	switch {
	case out.kind == FactTargetUnknown && out.templateKey != "":
		out.kind = FactTargetTemplateKey
	case out.kind == FactTargetUnknown && out.ref.Name != "":
		out.kind = FactTargetDynamic
	}
	return out
}
