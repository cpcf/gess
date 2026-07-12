package server

import (
	"fmt"

	"github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

func projectSnapshot(snapshot sess.Snapshot) map[string]any {
	facts := snapshot.Facts()
	projected := make([]any, len(facts))
	for i, fact := range facts {
		projected[i] = projectFact(fact)
	}
	return map[string]any{
		"gessMcpSchema": mcpToolSchemaVersion,
		"kind":          "snapshot",
		"sessionId":     snapshot.SessionID().String(),
		"rulesetId":     snapshot.RulesetID().String(),
		"generation":    uint64(snapshot.Generation()),
		"facts":         projected,
	}
}

func projectFact(fact sess.FactSnapshot) map[string]any {
	fields := fact.Fields()
	projectedFields := make(map[string]any, len(fields))
	for name, value := range fields {
		projectedFields[name] = projectValue(value)
	}
	projectedPresence := make(map[string]any)
	for name, presence := range fact.FieldPresenceMap() {
		projectedPresence[name] = string(presence)
	}
	out := map[string]any{
		"id":          fact.ID().String(),
		"name":        fact.Name(),
		"templateKey": fact.TemplateKey().String(),
		"version":     uint32(fact.Version()),
		"recency":     uint32(fact.Recency()),
		"generation":  uint64(fact.Generation()),
		"support":     string(fact.Support().State),
		"fields":      projectedFields,
	}
	if len(projectedPresence) > 0 {
		out["fieldPresence"] = projectedPresence
	}
	return out
}

func projectAgenda(agenda sess.Agenda) map[string]any {
	activations := agenda.Activations()
	projected := make([]any, len(activations))
	for i, activation := range activations {
		factIDs := activation.FactIDs()
		projectedFactIDs := make([]string, len(factIDs))
		for j, id := range factIDs {
			projectedFactIDs[j] = id.String()
		}
		projected[i] = map[string]any{
			"activationId":   activation.ActivationID().String(),
			"ruleId":         activation.RuleID().String(),
			"ruleRevisionId": activation.RuleRevisionID().String(),
			"ruleName":       activation.RuleName(),
			"module":         activation.Module().String(),
			"salience":       activation.Salience(),
			"factIds":        projectedFactIDs,
		}
	}
	focus := agenda.FocusStack()
	projectedFocus := make([]string, len(focus))
	for i, module := range focus {
		projectedFocus[i] = module.String()
	}
	return map[string]any{
		"gessMcpSchema": mcpToolSchemaVersion,
		"kind":          "agenda",
		"focusStack":    projectedFocus,
		"activations":   projected,
	}
}

func projectAssertResult(result sess.AssertResult) map[string]any {
	out := mutationResult("assert", string(result.Status), result.Fact)
	if result.DuplicateKey != "" {
		out["duplicateKey"] = string(result.DuplicateKey)
	}
	return out
}

func projectModifyResult(result sess.ModifyResult) map[string]any {
	return mutationResult("modify", string(result.Status), result.Fact)
}

func projectRetractResult(result sess.RetractResult) map[string]any {
	return mutationResult("retract", string(result.Status), result.Fact)
}

func mutationResult(kind, status string, fact sess.FactSnapshot) map[string]any {
	out := map[string]any{
		"gessMcpSchema": mcpToolSchemaVersion,
		"kind":          kind,
		"status":        status,
	}
	if !fact.ID().IsZero() {
		out["fact"] = projectFact(fact)
	}
	return out
}

func projectQueryRows(name string, rows []sess.QueryRow, limit int) map[string]any {
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	projected := make([]any, len(rows))
	for i, row := range rows {
		projected[i] = projectQueryRow(row)
	}
	return map[string]any{
		"gessMcpSchema": mcpToolSchemaVersion,
		"kind":          "query",
		"name":          name,
		"rows":          projected,
		"rowCount":      len(projected),
		"totalRows":     total,
		"truncated":     total > len(projected),
		"maxRows":       limit,
	}
}

func projectQueryRow(row sess.QueryRow) map[string]any {
	aliases := row.Aliases()
	projectedAliases := append([]string(nil), aliases...)
	facts := make(map[string]any)
	values := make(map[string]any)
	for _, alias := range aliases {
		if fact, ok := row.Fact(alias); ok {
			facts[alias] = projectFact(fact)
			continue
		}
		if value, ok := row.Value(alias); ok {
			values[alias] = projectValue(value)
		}
	}
	out := map[string]any{"aliases": projectedAliases}
	if len(facts) > 0 {
		out["facts"] = facts
	}
	if len(values) > 0 {
		out["values"] = values
	}
	return out
}

func projectValue(value rules.Value) any {
	switch value.Kind() {
	case rules.ValueNull:
		return nil
	case rules.ValueBool:
		out, _ := value.AsBool()
		return out
	case rules.ValueInt:
		out, _ := value.AsInt64()
		return out
	case rules.ValueFloat:
		out, _ := value.AsFloat64()
		return out
	case rules.ValueString:
		out, _ := value.AsString()
		return out
	case rules.ValueList:
		values, _ := value.AsList()
		out := make([]any, len(values))
		for i, item := range values {
			out[i] = projectValue(item)
		}
		return out
	case rules.ValueMap:
		values, _ := value.AsMap()
		out := make(map[string]any, len(values))
		for key, item := range values {
			out[key] = projectValue(item)
		}
		return out
	default:
		return fmt.Sprint(value)
	}
}
