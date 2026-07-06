package engine

import "encoding/json"

// MarshalJSON encodes a why-not report as a versioned explain document. See
// Derivation.MarshalJSON for the integer-precision and export-only caveats.
func (r WhyNotReport) MarshalJSON() ([]byte, error) {
	doc := whyNotReportToJSON(r)
	doc["gessExplainSchema"] = ExplainSchemaVersion
	doc["kind"] = "whynot"
	return json.Marshal(doc)
}

func whyNotReportToJSON(r WhyNotReport) map[string]any {
	doc := map[string]any{
		"ruleId":         r.RuleID.String(),
		"ruleName":       r.RuleName,
		"ruleRevisionId": r.RuleRevisionID.String(),
		"outcome":        string(r.Outcome),
		"truncated":      r.Truncated,
	}
	if len(r.Activations) > 0 {
		activations := make([]any, len(r.Activations))
		for i, activation := range r.Activations {
			activations[i] = agendaActivationToJSON(activation)
		}
		doc["activations"] = activations
	}
	if len(r.Branches) > 0 {
		branches := make([]any, len(r.Branches))
		for i, branch := range r.Branches {
			branches[i] = whyNotBranchToJSON(branch)
		}
		doc["branches"] = branches
	}
	return doc
}

func agendaActivationToJSON(activation AgendaActivation) map[string]any {
	return map[string]any{
		"activationId": activation.ActivationID().String(),
		"ruleId":       activation.RuleID().String(),
		"ruleName":     activation.RuleName(),
		"module":       string(activation.Module()),
		"salience":     int64(activation.Salience()),
		"factIds":      factIDsToJSON(activation.FactIDs()),
	}
}

func whyNotBranchToJSON(branch WhyNotBranch) map[string]any {
	conditions := make([]any, len(branch.Conditions))
	for i, condition := range branch.Conditions {
		conditions[i] = whyNotConditionToJSON(condition)
	}
	doc := map[string]any{
		"branchId":     int64(branch.BranchID),
		"firstFailing": int64(branch.FirstFailing),
		"conditions":   conditions,
	}
	if len(branch.PartialMatches) > 0 {
		matches := make([]any, len(branch.PartialMatches))
		for i, match := range branch.PartialMatches {
			matches[i] = whyNotPartialMatchToJSON(match)
		}
		doc["partialMatches"] = matches
	}
	return doc
}

func whyNotConditionToJSON(condition WhyNotCondition) map[string]any {
	doc := map[string]any{
		"order":        int64(condition.Order),
		"plannedOrder": int64(condition.PlannedOrder),
		"binding":      condition.Binding,
		"negated":      condition.Negated,
		"test":         condition.Test,
		"aggregate":    condition.Aggregate,
		"alphaMatches": int64(condition.AlphaMatches),
		"satisfied":    condition.Satisfied,
	}
	if loc := sourceSpanLocation(condition.Source); loc != "" {
		doc["source"] = loc
	}
	if condition.Reason != WhyNotReasonNone {
		doc["reason"] = string(condition.Reason)
		if loc := sourceSpanLocation(condition.RejectingSpan); loc != "" {
			doc["rejectingSpan"] = loc
		}
		if len(condition.Blockers) > 0 {
			doc["blockers"] = factIDsToJSON(condition.Blockers)
			doc["blockerCount"] = int64(condition.BlockerCount)
		}
	}
	return doc
}

func whyNotPartialMatchToJSON(match WhyNotPartialMatch) map[string]any {
	doc := map[string]any{
		"facts":     factIDsToJSON(match.Facts),
		"satisfied": int64(match.Satisfied),
	}
	if len(match.Bindings) > 0 {
		doc["bindings"] = bindingValuesToJSON(match.Bindings)
	}
	if loc := sourceSpanLocation(match.RejectedBySpan); loc != "" {
		doc["rejectedBy"] = loc
	}
	return doc
}
