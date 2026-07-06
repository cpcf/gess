package engine

import "encoding/json"

// MarshalJSON encodes a what-if report as a versioned explain document. The
// base and fork snapshots are represented by their working-memory diff rather
// than in full; see Derivation.MarshalJSON for the precision and export-only
// caveats.
func (r WhatIfReport) MarshalJSON() ([]byte, error) {
	doc := map[string]any{
		"gessExplainSchema": ExplainSchemaVersion,
		"kind":              "whatif",
		"run": map[string]any{
			"runId":  r.Run.RunID.String(),
			"status": string(r.Run.Status),
			"fired":  int64(r.Run.Fired),
		},
		"diff":              snapshotDiffToJSON(r.Diff),
		"agendaBeforeCount": int64(r.AgendaBefore.Len()),
		"agendaAfterCount":  int64(r.AgendaAfter.Len()),
	}
	if len(r.Firings) > 0 {
		firings := make([]any, len(r.Firings))
		for i, firing := range r.Firings {
			firings[i] = whatIfFiringToJSON(firing)
		}
		doc["firings"] = firings
	}
	if len(r.Derivations) > 0 {
		derivations := make([]any, len(r.Derivations))
		for i, derivation := range r.Derivations {
			derivations[i] = derivationToJSON(derivation)
		}
		doc["derivations"] = derivations
	}
	return json.Marshal(doc)
}

func whatIfFiringToJSON(firing WhatIfFiring) map[string]any {
	return map[string]any{
		"ruleId":         firing.RuleID.String(),
		"ruleName":       firing.RuleName,
		"ruleRevisionId": firing.RuleRevisionID.String(),
		"activationId":   firing.ActivationID.String(),
		"factIds":        factIDsToJSON(firing.FactIDs),
		"sequence":       int64(firing.Sequence),
	}
}

func snapshotDiffToJSON(diff SnapshotDiff) map[string]any {
	doc := map[string]any{}
	if len(diff.Added) > 0 {
		added := make([]any, len(diff.Added))
		for i, fact := range diff.Added {
			added[i] = factToJSON(fact)
		}
		doc["added"] = added
	}
	if len(diff.Retracted) > 0 {
		retracted := make([]any, len(diff.Retracted))
		for i, fact := range diff.Retracted {
			retracted[i] = factToJSON(fact)
		}
		doc["retracted"] = retracted
	}
	if len(diff.Modified) > 0 {
		modified := make([]any, len(diff.Modified))
		for i, mod := range diff.Modified {
			entry := map[string]any{
				"id":            mod.After.ID().String(),
				"supportBefore": string(mod.SupportBefore),
				"supportAfter":  string(mod.SupportAfter),
			}
			if len(mod.ChangedFields) > 0 {
				entry["changedFields"] = fieldChangesToJSON(mod.ChangedFields)
			}
			modified[i] = entry
		}
		doc["modified"] = modified
	}
	return doc
}
