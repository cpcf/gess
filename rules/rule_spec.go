package rules

import "strings"

// RuleSpec defines a rule.
type RuleSpec struct {
	Name        string
	Module      ModuleName
	ID          RuleID
	Description string
	Source      SourceSpan
	GessSource  string
	Tags        []string
	Salience    int
	AutoFocus   *bool
	// Conditions is the flat positive conjunction form. When ConditionTree is
	// nil, compile normalizes Conditions to And(Match...) without changing
	// condition ordering, condition identity, graph topology, or agenda behavior.
	Conditions []RuleConditionSpec
	// ConditionTree is the structured left-hand side form. It is mutually
	// exclusive with Conditions in one RuleSpec.
	ConditionTree ConditionSpec
	Actions       []RuleActionSpec
}

// CloneRuleSpec returns a defensive copy of s.
func CloneRuleSpec(s RuleSpec) RuleSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	out.Module = normalizeModuleName(out.Module)
	out.ID = RuleID(strings.TrimSpace(string(out.ID)))
	out.Tags = append([]string(nil), s.Tags...)
	if s.AutoFocus != nil {
		autoFocus := *s.AutoFocus
		out.AutoFocus = &autoFocus
	}
	out.Conditions = make([]RuleConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = CloneRuleConditionSpec(condition)
	}
	out.ConditionTree = CloneConditionSpec(s.ConditionTree)
	out.Actions = make([]RuleActionSpec, len(s.Actions))
	for i, action := range s.Actions {
		out.Actions[i] = CloneRuleActionSpec(action)
	}
	return out
}
