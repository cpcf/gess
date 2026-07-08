package rules

import "strings"

// RuleActionSpec references, by Name, an action registered on the workspace.
type RuleActionSpec struct {
	Name   string
	Source SourceSpan
}

// CloneRuleActionSpec returns a defensive copy of s.
func CloneRuleActionSpec(s RuleActionSpec) RuleActionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	return out
}

// RuleAction is a compiled, inspectable action reference on a rule.
type RuleAction struct {
	NameValue  string
	Order      int
	SourceSpan SourceSpan
}

func (a RuleAction) Name() string {
	return a.NameValue
}

func (a RuleAction) DeclarationOrder() int {
	return a.Order
}

func (a RuleAction) Source() SourceSpan {
	return a.SourceSpan
}

// CloneRuleAction returns a defensive copy of a.
func CloneRuleAction(a RuleAction) RuleAction {
	return a
}

// CloneRuleActions returns a defensive copy of actions.
func CloneRuleActions(actions []RuleAction) []RuleAction {
	out := make([]RuleAction, len(actions))
	copy(out, actions)
	return out
}
