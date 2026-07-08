package rules

// Rule is the compiled, inspectable form of a RuleSpec.
type Rule struct {
	IDValue                RuleID
	RevisionIDValue        RuleRevisionID
	NameValue              string
	ModuleValue            ModuleName
	DescriptionText        string
	TagValues              []string
	SalienceValue          int
	AutoFocusValue         bool
	HasAutoFocus           bool
	EffectiveAutoFocusFlag bool
	Order                  int
	SourceSpan             SourceSpan
	GessSourceText         string
	ConditionValues        []RuleCondition
	ConditionTreeValue     RuleConditionTree
	ConditionBranchValues  []RuleConditionBranch
	ActionValues           []RuleAction
}

func (r Rule) ID() RuleID {
	return r.IDValue
}

func (r Rule) RevisionID() RuleRevisionID {
	return r.RevisionIDValue
}

func (r Rule) Name() string {
	return r.NameValue
}

func (r Rule) Module() ModuleName {
	return r.ModuleValue
}

func (r Rule) Description() string {
	return r.DescriptionText
}

func (r Rule) Tags() []string {
	out := make([]string, len(r.TagValues))
	copy(out, r.TagValues)
	return out
}

func (r Rule) Salience() int {
	return r.SalienceValue
}

func (r Rule) AutoFocus() (bool, bool) {
	return r.AutoFocusValue, r.HasAutoFocus
}

func (r Rule) EffectiveAutoFocus() bool {
	return r.EffectiveAutoFocusFlag
}

func (r Rule) DeclarationOrder() int {
	return r.Order
}

func (r Rule) Source() SourceSpan {
	return r.SourceSpan
}

func (r Rule) GessSource() string {
	return r.GessSourceText
}

func (r Rule) Conditions() []RuleCondition {
	return CloneRuleConditions(r.ConditionValues)
}

func (r Rule) ConditionTree() RuleConditionTree {
	return CloneRuleConditionTree(r.ConditionTreeValue)
}

func (r Rule) ConditionBranches() []RuleConditionBranch {
	return CloneRuleConditionBranches(r.ConditionBranchValues)
}

func (r Rule) Actions() []RuleAction {
	return CloneRuleActions(r.ActionValues)
}

// CloneRule returns a defensive copy of r.
func CloneRule(r Rule) Rule {
	out := r
	out.TagValues = append([]string(nil), r.TagValues...)
	out.ConditionValues = CloneRuleConditions(r.ConditionValues)
	out.ConditionTreeValue = CloneRuleConditionTree(r.ConditionTreeValue)
	out.ConditionBranchValues = CloneRuleConditionBranches(r.ConditionBranchValues)
	out.ActionValues = CloneRuleActions(r.ActionValues)
	return out
}

// CloneRules returns a defensive copy of rules.
func CloneRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	for i, rule := range rules {
		out[i] = CloneRule(rule)
	}
	return out
}
