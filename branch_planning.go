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
	id      int
	nodes   []branchPlanningNode
	joins   []branchPlanningJoin
	reorder bool
}

type branchPlanningNode struct {
	condition normalizedRuleCondition
	order     int
	defines   []string
	dependsOn []string
	hardDeps  []string
	movable   bool
	barrier   branchPlanningBarrierKind
}

type branchPlanningJoin struct {
	leftBinding  string
	leftField    string
	operator     FieldConstraintOperator
	rightBinding string
	rightField   string
}

func newBranchPlanningIR(branchID int, conditions []normalizedRuleCondition) branchPlanningIR {
	nodes := make([]branchPlanningNode, len(conditions))
	for i, condition := range conditions {
		nodes[i] = newBranchPlanningNode(condition, i)
	}
	return branchPlanningIR{
		id:    branchID,
		nodes: nodes,
	}
}

func newReorderedBranchPlanningIR(branchID int, conditions []normalizedRuleCondition) branchPlanningIR {
	ir := newBranchPlanningIR(branchID, conditions)
	ir.reorder = true
	for i := range ir.nodes {
		ir.joins = append(ir.joins, extractBranchPlanningJoins(&ir.nodes[i])...)
	}
	return ir
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
	return newReorderedBranchPlanningIR(branchID, lowered), true
}

func newBranchPlanningNode(condition normalizedRuleCondition, order int) branchPlanningNode {
	out := branchPlanningNode{
		condition: cloneNormalizedRuleCondition(condition),
		order:     order,
		defines:   branchPlanningDefinedBindings(condition),
		dependsOn: branchPlanningDependencyBindings(condition),
		hardDeps:  branchPlanningHardDependencyBindings(condition),
		movable:   branchPlanningConditionMovable(condition),
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
	nodes := ir.plannedNodes()
	out := make([]normalizedRuleCondition, len(nodes))
	bindingIndexes := make(map[string]int, len(nodes))
	for i, node := range nodes {
		out[i] = cloneNormalizedRuleCondition(node.condition)
		if binding := branchPlanningConditionBinding(node.condition); binding != "" {
			bindingIndexes[binding] = i
		}
		for _, binding := range node.defines {
			bindingIndexes[binding] = i
		}
	}
	for _, join := range ir.joins {
		leftIndex, haveLeft := bindingIndexes[join.leftBinding]
		rightIndex, haveRight := bindingIndexes[join.rightBinding]
		if !haveLeft || !haveRight || leftIndex == rightIndex {
			continue
		}
		if leftIndex > rightIndex {
			out[leftIndex].spec.JoinConstraints = append(out[leftIndex].spec.JoinConstraints, JoinConstraintSpec{
				Field:    join.leftField,
				Operator: join.operator,
				Ref: FieldRef{
					Binding: join.rightBinding,
					Field:   join.rightField,
				},
			})
			continue
		}
		inverted, ok := invertFieldConstraintOperator(join.operator)
		if !ok {
			continue
		}
		out[rightIndex].spec.JoinConstraints = append(out[rightIndex].spec.JoinConstraints, JoinConstraintSpec{
			Field:    join.rightField,
			Operator: inverted,
			Ref: FieldRef{
				Binding: join.leftBinding,
				Field:   join.leftField,
			},
		})
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

func extractBranchPlanningJoins(node *branchPlanningNode) []branchPlanningJoin {
	if node == nil || node.condition.isAggregate {
		return nil
	}
	condition := &node.condition.spec
	if len(condition.JoinConstraints) == 0 {
		return nil
	}
	joins := make([]branchPlanningJoin, 0, len(condition.JoinConstraints))
	for _, join := range condition.JoinConstraints {
		leftBinding := strings.TrimSpace(condition.Binding)
		rightBinding := strings.TrimSpace(join.Ref.Binding)
		if leftBinding == "" || rightBinding == "" {
			continue
		}
		joins = append(joins, branchPlanningJoin{
			leftBinding:  leftBinding,
			leftField:    strings.TrimSpace(join.Field),
			operator:     join.Operator,
			rightBinding: rightBinding,
			rightField:   strings.TrimSpace(join.Ref.Field),
		})
	}
	condition.JoinConstraints = nil
	return joins
}

func (ir branchPlanningIR) plannedNodes() []branchPlanningNode {
	if !ir.reorder || len(ir.nodes) < 2 {
		return cloneBranchPlanningNodes(ir.nodes)
	}
	out := make([]branchPlanningNode, 0, len(ir.nodes))
	segment := make([]branchPlanningNode, 0, len(ir.nodes))
	for _, node := range ir.nodes {
		if node.movable && node.barrier == branchPlanningBarrierNone {
			segment = append(segment, node)
			continue
		}
		out = append(out, planBranchPlanningSegment(segment, out)...)
		segment = segment[:0]
		out = append(out, cloneBranchPlanningNode(node))
	}
	out = append(out, planBranchPlanningSegment(segment, out)...)
	return out
}

func planBranchPlanningSegment(segment []branchPlanningNode, prior []branchPlanningNode) []branchPlanningNode {
	if len(segment) < 2 {
		return cloneBranchPlanningNodes(segment)
	}
	defined := make(map[string]struct{})
	for _, node := range prior {
		for _, binding := range node.defines {
			defined[binding] = struct{}{}
		}
	}
	remaining := cloneBranchPlanningNodes(segment)
	out := make([]branchPlanningNode, 0, len(remaining))
	for len(remaining) > 0 {
		nextIndex := selectNextBranchPlanningNode(remaining, defined)
		next := remaining[nextIndex]
		out = append(out, next)
		for _, binding := range next.defines {
			defined[binding] = struct{}{}
		}
		copy(remaining[nextIndex:], remaining[nextIndex+1:])
		remaining[len(remaining)-1] = branchPlanningNode{}
		remaining = remaining[:len(remaining)-1]
	}
	return out
}

func selectNextBranchPlanningNode(nodes []branchPlanningNode, defined map[string]struct{}) int {
	best := -1
	for i, node := range nodes {
		if !branchPlanningNodeReady(node, nodes, defined) {
			continue
		}
		if best < 0 || branchPlanningNodeLess(node, nodes[best]) {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	return 0
}

func branchPlanningNodeReady(node branchPlanningNode, remaining []branchPlanningNode, defined map[string]struct{}) bool {
	futureDefinitions := make(map[string]struct{})
	for _, other := range remaining {
		for _, binding := range other.defines {
			futureDefinitions[binding] = struct{}{}
		}
	}
	for _, dep := range node.hardDeps {
		if _, ok := defined[dep]; ok {
			continue
		}
		if _, ok := futureDefinitions[dep]; ok {
			return false
		}
	}
	return true
}

func branchPlanningNodeLess(left, right branchPlanningNode) bool {
	leftScore := branchPlanningSelectivityScore(left)
	rightScore := branchPlanningSelectivityScore(right)
	if leftScore != rightScore {
		return leftScore < rightScore
	}
	return left.order < right.order
}

func branchPlanningSelectivityScore(node branchPlanningNode) int {
	score := 1000
	condition := node.condition.spec
	if strings.TrimSpace(condition.TemplateKey.String()) != "" || strings.TrimSpace(condition.Name) != "" {
		score -= 10
	}
	for _, constraint := range condition.FieldConstraints {
		if constraint.Operator == FieldConstraintEqual {
			score -= 100
			continue
		}
		score -= 25
	}
	score += len(condition.Predicates) * 10
	return score
}

func branchPlanningConditionMovable(condition normalizedRuleCondition) bool {
	if condition.negated || condition.isAggregate {
		return false
	}
	if strings.TrimSpace(condition.spec.Binding) == internalQueryTriggerBinding {
		return false
	}
	return true
}

func branchPlanningConditionBinding(condition normalizedRuleCondition) string {
	if condition.isAggregate {
		return ""
	}
	return strings.TrimSpace(condition.spec.Binding)
}

func cloneBranchPlanningNodes(nodes []branchPlanningNode) []branchPlanningNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]branchPlanningNode, len(nodes))
	for i, node := range nodes {
		out[i] = cloneBranchPlanningNode(node)
	}
	return out
}

func cloneBranchPlanningNode(node branchPlanningNode) branchPlanningNode {
	out := node
	out.condition = cloneNormalizedRuleCondition(node.condition)
	out.defines = append([]string(nil), node.defines...)
	out.dependsOn = append([]string(nil), node.dependsOn...)
	out.hardDeps = append([]string(nil), node.hardDeps...)
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

func branchPlanningHardDependencyBindings(condition normalizedRuleCondition) []string {
	bindings := make(map[string]struct{})
	if condition.isAggregate {
		addConditionSpecBranchPlanningDependencies(bindings, condition.aggregate.Input)
		for _, spec := range condition.aggregate.Specs {
			addExpressionSpecBranchPlanningDependencies(bindings, spec.Expression())
		}
		return sortedBranchPlanningBindings(bindings)
	}
	for _, predicate := range condition.spec.Predicates {
		addExpressionSpecBranchPlanningDependencies(bindings, predicate)
	}
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
