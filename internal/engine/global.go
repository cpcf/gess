package engine

import "strings"

type GlobalSpec struct {
	Name        string
	Kind        ValueKind
	Default     any
	HasDefault  bool
	Description string
}

func (s GlobalSpec) clone() GlobalSpec {
	s.Name = strings.TrimSpace(s.Name)
	if s.Kind == valueKindUnknown {
		s.Kind = ValueAny
	}
	s.Default = cloneSpecValue(s.Default)
	return s
}

type Global struct {
	name         string
	kind         ValueKind
	defaultValue Value
	hasDefault   bool
	description  string
	slot         int
}

func (g Global) Name() string {
	return g.name
}

func (g Global) Kind() ValueKind {
	return g.kind
}

func (g Global) Default() (Value, bool) {
	if !g.hasDefault {
		return Value{}, false
	}
	return cloneValue(g.defaultValue), true
}

func (g Global) Description() string {
	return g.description
}

func (g Global) DeclarationOrder() int {
	return g.slot
}

func (g Global) clone() Global {
	g.defaultValue = cloneValue(g.defaultValue)
	return g
}

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
		name:         g.name,
		kind:         g.kind,
		defaultValue: cloneValue(g.defaultValue),
		hasDefault:   g.hasDefault,
		description:  g.description,
		slot:         g.slot,
	}
}

func (g compiledGlobal) clone() compiledGlobal {
	g.defaultValue = cloneValue(g.defaultValue)
	return g
}

func compileGlobalSpec(spec GlobalSpec, slot int) (compiledGlobal, error) {
	normalized := spec.clone()
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

func cloneGlobalSpecs(in []GlobalSpec) []GlobalSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]GlobalSpec, len(in))
	for i, spec := range in {
		out[i] = spec.clone()
	}
	return out
}

func cloneCompiledGlobals(in []compiledGlobal) []compiledGlobal {
	if len(in) == 0 {
		return nil
	}
	out := make([]compiledGlobal, len(in))
	for i, global := range in {
		out[i] = global.clone()
	}
	return out
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
			return nil, &ValidationError{Reason: "unknown global"}
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
				return nil, &ValidationError{Reason: "invalid global value", Err: err}
			}
			value, ok = canonical, true
		} else if global.hasDefault {
			value, ok = cloneValue(global.defaultValue), true
		}
		if !ok {
			return nil, &ValidationError{Reason: "missing required global"}
		}
		if !isValueCompatibleWithKind(global.kind, value) {
			return nil, &ValidationError{Reason: "global value has incompatible type"}
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
