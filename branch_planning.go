package gess

type branchPlanningIR struct {
	id    int
	nodes []branchPlanningNode
}

type branchPlanningNode struct {
	condition normalizedRuleCondition
}

func newBranchPlanningIR(branchID int, conditions []normalizedRuleCondition) branchPlanningIR {
	nodes := make([]branchPlanningNode, len(conditions))
	for i, condition := range conditions {
		nodes[i] = branchPlanningNode{condition: cloneNormalizedRuleCondition(condition)}
	}
	return branchPlanningIR{
		id:    branchID,
		nodes: nodes,
	}
}

func newQueryGraphBranchPlanningIR(queryName string, branchID int, conditions []normalizedRuleCondition, params map[string]ValueKind) (branchPlanningIR, bool) {
	if len(conditions) == 0 {
		return branchPlanningIR{}, false
	}
	lowered := make([]normalizedRuleCondition, 0, len(conditions)+1)
	lowered = append(lowered, normalizedRuleCondition{
		spec: RuleConditionSpec{
			Binding: internalQueryTriggerBinding,
			Name:    internalQueryTriggerName(queryName),
		},
		visible: true,
	})
	for _, condition := range conditions {
		if condition.isAggregate {
			return branchPlanningIR{}, false
		}
		next := cloneNormalizedRuleCondition(condition)
		next.spec = lowerQueryConditionParams(next.spec, params)
		lowered = append(lowered, next)
	}
	return newBranchPlanningIR(branchID, lowered), true
}

func (ir branchPlanningIR) normalizedConditions() []normalizedRuleCondition {
	out := make([]normalizedRuleCondition, len(ir.nodes))
	for i, node := range ir.nodes {
		out[i] = cloneNormalizedRuleCondition(node.condition)
	}
	return out
}

func compileBranchPlanningIR(ruleName string, ruleID RuleID, ir branchPlanningIR, templatesByKey map[TemplateKey]Template, allowDuplicateBindings bool, params map[string]ValueKind) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithParams(ruleName, ruleID, ir.normalizedConditions(), templatesByKey, allowDuplicateBindings, params)
}

func compiledConditionBranchFromPlanningIR(ir branchPlanningIR, compiled compiledRuleConditionSet) compiledConditionBranch {
	return compiledConditionBranch{
		id:         ir.id,
		conditions: compiled.branchConditions,
		plans:      compiled.conditionPlans,
	}
}

func cloneNormalizedRuleCondition(condition normalizedRuleCondition) normalizedRuleCondition {
	out := condition
	out.spec = condition.spec.clone()
	out.aggregate = condition.aggregate.clone()
	out.path = cloneIntPath(condition.path)
	return out
}
