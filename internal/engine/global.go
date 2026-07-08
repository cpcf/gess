package engine

import (
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type GlobalSpec = gessrules.GlobalSpec

func cloneGlobalSpec(s GlobalSpec) GlobalSpec {
	return gessrules.CloneGlobalSpec(s)
}

type Global = gessrules.Global

type compiledGlobal struct {
	name         string
	kind         ValueKind
	defaultValue Value
	hasDefault   bool
	description  string
	slot         int
}

func (g compiledGlobal) public() Global {
	return Global{
		NameValue:       g.name,
		KindValue:       g.kind,
		DefaultValue:    cloneValue(g.defaultValue),
		HasDefaultValue: g.hasDefault,
		DescriptionText: g.description,
		Order:           g.slot,
	}
}

func compileGlobalSpec(spec GlobalSpec, slot int) (compiledGlobal, error) {
	normalized := cloneGlobalSpec(spec)
	if normalized.Name == "" {
		return compiledGlobal{}, &ValidationError{Reason: "global name is required"}
	}
	if !isSupportedKind(normalized.Kind) {
		return compiledGlobal{}, &ValidationError{Reason: "unsupported global kind"}
	}
	out := compiledGlobal{
		name:        normalized.Name,
		kind:        normalized.Kind,
		description: strings.TrimSpace(normalized.Description),
		slot:        slot,
	}
	if normalized.HasDefault {
		value, err := canonicalValue(normalized.Default)
		if err != nil {
			return compiledGlobal{}, &ValidationError{Reason: "invalid global default", Err: err}
		}
		if !isValueCompatibleWithKind(normalized.Kind, value) {
			return compiledGlobal{}, &ValidationError{Reason: "global default has incompatible type"}
		}
		out.defaultValue = value
		out.hasDefault = true
	}
	return out, nil
}

func cloneGlobalValues(values []Value) []Value {
	if len(values) == 0 {
		return nil
	}
	out := make([]Value, len(values))
	for i, value := range values {
		out[i] = cloneValue(value)
	}
	return out
}

func compileSessionGlobals(revision *Ruleset, supplied map[string]any) ([]Value, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}
	normalizedSupplied := make(map[string]any, len(supplied))
	for name := range supplied {
		normalized := strings.TrimSpace(name)
		if _, ok := revision.globals[normalized]; !ok {
			return nil, &ValidationError{GlobalName: normalized, Reason: "unknown global"}
		}
		normalizedSupplied[normalized] = supplied[name]
	}
	if len(revision.globalOrder) == 0 {
		return nil, nil
	}
	values := make([]Value, len(revision.globalOrder))
	for _, name := range revision.globalOrder {
		global := revision.globals[name]
		value, ok := Value{}, false
		if raw, supplied := normalizedSupplied[name]; supplied {
			canonical, err := canonicalValue(raw)
			if err != nil {
				return nil, &ValidationError{GlobalName: name, Reason: "invalid global value", Err: err}
			}
			value, ok = canonical, true
		} else if global.hasDefault {
			value, ok = cloneValue(global.defaultValue), true
		}
		if !ok {
			return nil, &ValidationError{GlobalName: name, Reason: "missing required global"}
		}
		if !isValueCompatibleWithKind(global.kind, value) {
			return nil, &ValidationError{GlobalName: name, Reason: "global value has incompatible type"}
		}
		if global.slot < 0 || global.slot >= len(values) {
			return nil, ErrInvalidRuleset
		}
		values[global.slot] = cloneValue(value)
	}
	return values, nil
}

func rebindSessionGlobals(previous, next *Ruleset, current []Value) ([]Value, error) {
	if next == nil {
		return nil, ErrInvalidRuleset
	}
	supplied := make(map[string]any)
	if previous != nil {
		for _, name := range previous.globalOrder {
			if _, ok := next.globals[name]; !ok {
				continue
			}
			global := previous.globals[name]
			if global.slot < 0 || global.slot >= len(current) {
				continue
			}
			supplied[name] = cloneValue(current[global.slot])
		}
	}
	return compileSessionGlobals(next, supplied)
}
