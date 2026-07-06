package engine

import "encoding/json"

// ExplainSchemaVersion is the version of the machine-readable explain contract.
// It is bumped only on a breaking change (field removal or rename); additive
// changes keep the same version.
const ExplainSchemaVersion = 1

// MarshalJSON encodes a derivation as a versioned explain document. Integers
// encode as JSON numbers; values beyond 2^53 may lose precision in consumers
// that parse JSON numbers as float64. IDs encode as their string forms
// (fact:g1:12, …). These documents are export-only and are not decoded back
// into engine types.
func (d Derivation) MarshalJSON() ([]byte, error) {
	doc := derivationToJSON(d)
	doc["gessExplainSchema"] = ExplainSchemaVersion
	doc["kind"] = "derivation"
	return json.Marshal(doc)
}

func derivationToJSON(d Derivation) map[string]any {
	doc := map[string]any{
		"fact":      factToJSON(d.Fact),
		"support":   string(d.Support),
		"truncated": d.Truncated,
	}
	if d.ProducedBy != nil {
		doc["producedBy"] = firingToJSON(d.ProducedBy)
	}
	if len(d.DependsOn) > 0 {
		deps := make([]any, len(d.DependsOn))
		for i, child := range d.DependsOn {
			deps[i] = derivationToJSON(child)
		}
		doc["dependsOn"] = deps
	}
	if len(d.History) > 0 {
		history := make([]any, len(d.History))
		for i, record := range d.History {
			history[i] = mutationRecordToJSON(record)
		}
		doc["history"] = history
	}
	return doc
}

func mutationRecordToJSON(record MutationRecord) map[string]any {
	doc := map[string]any{
		"kind":     string(record.Kind),
		"sequence": int64(record.Sequence),
	}
	if record.Firing != nil {
		doc["firing"] = firingToJSON(record.Firing)
	}
	if len(record.ChangedFields) > 0 {
		doc["changedFields"] = fieldChangesToJSON(record.ChangedFields)
	}
	return doc
}

func firingToJSON(firing *Firing) map[string]any {
	if firing == nil {
		return nil
	}
	doc := map[string]any{
		"ruleId":          firing.RuleID.String(),
		"ruleName":        firing.RuleName,
		"ruleRevisionId":  firing.RuleRevisionID.String(),
		"activationId":    firing.ActivationID.String(),
		"generation":      int64(firing.Generation),
		"bindingsPartial": firing.BindingsPartial,
	}
	if firing.Action != "" {
		doc["action"] = firing.Action
	}
	if len(firing.Bindings) > 0 {
		doc["bindings"] = bindingValuesToJSON(firing.Bindings)
	}
	if len(firing.SupportingFacts) > 0 {
		doc["supportingFacts"] = factIDsToJSON(firing.SupportingFacts)
	}
	return doc
}

func bindingValuesToJSON(bindings []BindingValue) []any {
	out := make([]any, len(bindings))
	for i, binding := range bindings {
		entry := map[string]any{
			"name":  binding.Name,
			"value": valueToJSON(binding.Value),
		}
		if !binding.FromFact.IsZero() {
			entry["fromFact"] = binding.FromFact.String()
		}
		out[i] = entry
	}
	return out
}

func factToJSON(fact FactSnapshot) map[string]any {
	doc := map[string]any{
		"id":          fact.ID().String(),
		"name":        fact.Name(),
		"templateKey": string(fact.TemplateKey()),
		"version":     int64(fact.Version()),
		"support":     string(fact.Support().State),
	}
	fields := fact.Fields()
	if len(fields) > 0 {
		encoded := make(map[string]any, len(fields))
		for name, value := range fields {
			encoded[name] = valueToJSON(value)
		}
		doc["fields"] = encoded
	}
	return doc
}

func fieldChangesToJSON(changes []FieldChange) []any {
	out := make([]any, len(changes))
	for i, change := range changes {
		out[i] = map[string]any{
			"field": change.Field,
			"old":   valueToJSON(change.Old),
			"new":   valueToJSON(change.New),
		}
	}
	return out
}

func factIDsToJSON(ids []FactID) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// valueToJSON renders a Value as a native JSON value via a kind switch, since
// Value has no exported fields. int64 renders as a JSON number.
func valueToJSON(value Value) any {
	switch value.Kind() {
	case ValueNull, ValueAny:
		return nil
	case ValueBool:
		raw, _ := value.AsBool()
		return raw
	case ValueInt:
		raw, _ := value.AsInt64()
		return raw
	case ValueFloat:
		raw, _ := value.AsFloat64()
		return raw
	case ValueString:
		raw, _ := value.AsString()
		return raw
	case ValueList:
		raw, ok := value.data.([]Value)
		if !ok {
			return nil
		}
		out := make([]any, len(raw))
		for i, item := range raw {
			out[i] = valueToJSON(item)
		}
		return out
	case ValueMap:
		raw, ok := value.data.(map[string]Value)
		if !ok {
			return nil
		}
		out := make(map[string]any, len(raw))
		for key, item := range raw {
			out[key] = valueToJSON(item)
		}
		return out
	default:
		return value.String()
	}
}
