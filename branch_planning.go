package gess

import (
	"sort"
	"strings"
)

type branchPlanningBarrierKind string

const (
	branchPlanningBarrierNone      branchPlanningBarrierKind = ""
	branchPlanningBarrierNegation  branchPlanningBarrierKind = "negation"
	branchPlanningBarrierAggregate branchPlanningBarrierKind = "aggregate"
)

type branchPlanningIR struct {
	id    int
	nodes []branchPlanningNode
}

type branchPlanningNode struct {
	condition normalizedRuleCondition
	defines   []string
	dependsOn []string
	movable   bool
	barrier   branchPlanningBarrierKind
}

func newBranchPlanningIR(branchID int, conditions []normalizedRuleCondition) branchPlanningIR {
	nodes := make([]branchPlanningNode, len(conditions))
	for i, condition := range conditions {
		nodes[i] = newBranchPlanningNode(condition)
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

func newBranchPlanningNode(condition normalizedRuleCondition) branchPlanningNode {
	out := branchPlanningNode{
		condition: cloneNormalizedRuleCondition(condition),
		defines:   branchPlanningDefinedBindings(condition),
		dependsOn: branchPlanningDependencyBindings(condition),
		movable:   !condition.negated && !condition.isAggregate,
	}
	switch {
	case condition.isAggregate:
		out.barrier = branchPlanningBarrierAggregate
	case condition.negated:
		out.barrier = branchPlanningBarrierNegation
	default:
		out.barrier = branchPlanningBarrierNone
	}
	return out
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

func branchPlanningDefinedBindings(condition normalizedRuleCondition) []string {
	bindings := make(map[string]struct{})
	if condition.isAggregate {
		for _, spec := range condition.aggregate.Specs {
			addBranchPlanningBinding(bindings, spec.Binding())
		}
		return sortedBranchPlanningBindings(bindings)
	}
	if condition.visible {
		addBranchPlanningBinding(bindings, condition.spec.Binding)
	}
	return sortedBranchPlanningBindings(bindings)
}

func branchPlanningDependencyBindings(condition normalizedRuleCondition) []string {
	bindings := make(map[string]struct{})
	if condition.isAggregate {
		addConditionSpecBranchPlanningDependencies(bindings, condition.aggregate.Input)
		for _, spec := range condition.aggregate.Specs {
			addExpressionSpecBranchPlanningDependencies(bindings, spec.Expression())
		}
		return sortedBranchPlanningBindings(bindings)
	}
	addRuleConditionSpecBranchPlanningDependencies(bindings, condition.spec)
	return sortedBranchPlanningBindings(bindings)
}

func addConditionSpecBranchPlanningDependencies(bindings map[string]struct{}, spec ConditionSpec) {
	switch condition := spec.(type) {
	case nil:
	case Match:
		addRuleConditionSpecBranchPlanningDependencies(bindings, RuleConditionSpec(condition))
	case *Match:
		if condition != nil {
			addRuleConditionSpecBranchPlanningDependencies(bindings, RuleConditionSpec(*condition))
		}
	case And:
		for _, child := range condition.Conditions {
			addConditionSpecBranchPlanningDependencies(bindings, child)
		}
	case *And:
		if condition != nil {
			addConditionSpecBranchPlanningDependencies(bindings, And(*condition))
		}
	case Or:
		for _, child := range condition.Conditions {
			addConditionSpecBranchPlanningDependencies(bindings, child)
		}
	case *Or:
		if condition != nil {
			addConditionSpecBranchPlanningDependencies(bindings, Or(*condition))
		}
	case Not:
		addConditionSpecBranchPlanningDependencies(bindings, condition.Condition)
	case *Not:
		if condition != nil {
			addConditionSpecBranchPlanningDependencies(bindings, condition.Condition)
		}
	case AccumulateCondition:
		addConditionSpecBranchPlanningDependencies(bindings, condition.Input)
		for _, spec := range condition.Specs {
			addExpressionSpecBranchPlanningDependencies(bindings, spec.Expression())
		}
	case *AccumulateCondition:
		if condition != nil {
			addConditionSpecBranchPlanningDependencies(bindings, condition.Input)
			for _, spec := range condition.Specs {
				addExpressionSpecBranchPlanningDependencies(bindings, spec.Expression())
			}
		}
	}
}

func addRuleConditionSpecBranchPlanningDependencies(bindings map[string]struct{}, condition RuleConditionSpec) {
	for _, join := range condition.JoinConstraints {
		addBranchPlanningBinding(bindings, join.Ref.Binding)
	}
	for _, predicate := range condition.Predicates {
		addExpressionSpecBranchPlanningDependencies(bindings, predicate)
	}
}

func addExpressionSpecBranchPlanningDependencies(bindings map[string]struct{}, spec ExpressionSpec) {
	switch expression := spec.(type) {
	case nil:
	case BindingFieldExpr:
		addBranchPlanningBinding(bindings, expression.Binding)
	case *BindingFieldExpr:
		if expression != nil {
			addBranchPlanningBinding(bindings, expression.Binding)
		}
	case BindingValueExpr:
		addBranchPlanningBinding(bindings, expression.Binding)
	case *BindingValueExpr:
		if expression != nil {
			addBranchPlanningBinding(bindings, expression.Binding)
		}
	case CompareExpr:
		addExpressionSpecBranchPlanningDependencies(bindings, expression.Left)
		addExpressionSpecBranchPlanningDependencies(bindings, expression.Right)
	case *CompareExpr:
		if expression != nil {
			addExpressionSpecBranchPlanningDependencies(bindings, expression.Left)
			addExpressionSpecBranchPlanningDependencies(bindings, expression.Right)
		}
	case BooleanExpr:
		for _, operand := range expression.Operands {
			addExpressionSpecBranchPlanningDependencies(bindings, operand)
		}
	case *BooleanExpr:
		if expression != nil {
			for _, operand := range expression.Operands {
				addExpressionSpecBranchPlanningDependencies(bindings, operand)
			}
		}
	}
}

func addBranchPlanningBinding(bindings map[string]struct{}, binding string) {
	binding = strings.TrimSpace(binding)
	if binding != "" {
		bindings[binding] = struct{}{}
	}
}

func sortedBranchPlanningBindings(bindings map[string]struct{}) []string {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]string, 0, len(bindings))
	for binding := range bindings {
		out = append(out, binding)
	}
	sort.Strings(out)
	return out
}
