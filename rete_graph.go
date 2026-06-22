package gess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
)

type reteGraph struct {
	alphaNodes          []reteGraphAlphaNode
	betaNodes           []reteGraphBetaNode
	aggregateNodes      []reteGraphAggregateNode
	terminalNodes       []reteGraphTerminalNode
	ruleBranchPlans     []reteGraphRuleBranchPlan
	routesByTemplateKey map[TemplateKey][]reteGraphAlphaNodeID
	routesByName        map[string][]reteGraphAlphaNodeID
	alphaRouteTables    map[TemplateKey]*reteGraphAlphaRouteTable
	successorsByStage   map[reteGraphStageRef][]reteGraphStageSuccessor
	aggregatesByStage   map[reteGraphStageRef][]reteGraphAggregateNodeID
	aggregateOuters     map[reteGraphStageRef][]reteGraphAggregateNodeID
	terminalsByStage    map[reteGraphStageRef][]reteGraphTerminalRoute
}

type reteGraphAlphaNodeID int
type reteGraphBetaNodeID int
type reteGraphAggregateNodeID int
type reteGraphTerminalNodeID int

type reteGraphStageKind uint8

const (
	reteGraphStageUnknown reteGraphStageKind = iota
	reteGraphStageAlpha
	reteGraphStageBeta
	reteGraphStageAggregate
)

type reteGraphBetaNodeKind uint8

const (
	reteGraphBetaNodeJoin reteGraphBetaNodeKind = iota + 1
	reteGraphBetaNodeNot
)

type reteGraphStageRef struct {
	kind reteGraphStageKind
	id   int
}

type reteGraphAlphaNode struct {
	id          reteGraphAlphaNodeID
	target      conditionTarget
	constraints []compiledFieldConstraint
	predicates  []compiledExpressionPredicate
	consumers   []reteBetaConditionRoute
	entry       bindingTupleEntry
	route       reteGraphAlphaRouteSelector
}

type reteGraphBetaNode struct {
	id            reteGraphBetaNodeID
	kind          reteGraphBetaNodeKind
	left          reteGraphStageRef
	right         reteGraphStageRef
	joins         []compiledJoinConstraint
	hashJoins     []compiledJoinConstraint
	residualJoins []compiledJoinConstraint
	predicates    []compiledExpressionPredicate
	entry         bindingTupleEntry
}

type reteGraphAggregateNode struct {
	id          reteGraphAggregateNodeID
	input       reteGraphStageRef
	outer       reteGraphStageRef
	inputEntry  bindingTupleEntry
	conditionID ConditionID
	bindingSlot int
	specs       []compiledAggregateSpec
	entries     []bindingTupleEntry
}

type reteGraphTerminalNode struct {
	id             reteGraphTerminalNodeID
	ruleRevisionID RuleRevisionID
	input          reteGraphStageRef
}

type reteGraphRuleBranchPlan struct {
	ruleRevisionID RuleRevisionID
	branchID       int
	conditions     []RuleConditionBranchCondition
}

type reteGraphDebugSummary struct {
	AlphaNodes          []reteGraphAlphaNode
	BetaNodes           []reteGraphBetaNode
	TerminalNodes       []reteGraphTerminalNode
	RuleBranchPlans     []reteGraphRuleBranchPlan
	RoutesByTemplateKey map[TemplateKey][]reteGraphAlphaNodeID
	RoutesByName        map[string][]reteGraphAlphaNodeID
}

type reteGraphBetaInputSide uint8

const (
	reteGraphBetaInputUnknown reteGraphBetaInputSide = iota
	reteGraphBetaInputLeft
	reteGraphBetaInputRight
)

type reteGraphStageSuccessor struct {
	betaNodeID reteGraphBetaNodeID
	side       reteGraphBetaInputSide
	entry      bindingTupleEntry
}

type reteGraphTerminalRoute struct {
	terminalID reteGraphTerminalNodeID
	entry      bindingTupleEntry
	branchID   int
}

type reteGraphAlphaKey struct {
	target      reteGraphTargetKey
	constraints string
	predicates  string
}

type reteGraphTargetKey struct {
	kind        conditionTargetKind
	name        string
	templateKey TemplateKey
}

type reteGraphBetaKey struct {
	kind       reteGraphBetaNodeKind
	left       reteGraphStageRef
	right      reteGraphStageRef
	joins      string
	predicates string
}

type reteGraphAlphaRouteSelector struct {
	fieldSlot int
	value     reteGraphAlphaRouteValue
	enabled   bool
}

type reteGraphAlphaRouteValue struct {
	kind  ValueKind
	bits  int64
	text  string
	valid bool
}

type reteGraphAlphaRouteKey struct {
	fieldSlot int
	value     reteGraphAlphaRouteValue
}

type reteGraphAlphaRouteTable struct {
	unindexed     []reteGraphAlphaNodeID
	indexed       map[reteGraphAlphaRouteKey][]reteGraphAlphaNodeID
	indexedFields []int
}

func compileReteGraph(compiledRules []compiledRule, templatesByKey map[TemplateKey]Template) *reteGraph {
	graph := &reteGraph{
		routesByTemplateKey: make(map[TemplateKey][]reteGraphAlphaNodeID),
		routesByName:        make(map[string][]reteGraphAlphaNodeID),
		alphaRouteTables:    make(map[TemplateKey]*reteGraphAlphaRouteTable),
		successorsByStage:   make(map[reteGraphStageRef][]reteGraphStageSuccessor),
		aggregatesByStage:   make(map[reteGraphStageRef][]reteGraphAggregateNodeID),
		aggregateOuters:     make(map[reteGraphStageRef][]reteGraphAggregateNodeID),
		terminalsByStage:    make(map[reteGraphStageRef][]reteGraphTerminalRoute),
	}
	if len(compiledRules) == 0 {
		return graph
	}

	alphaIndex := make(map[reteGraphAlphaKey]reteGraphAlphaNodeID, len(compiledRules))
	betaIndex := make(map[reteGraphBetaKey]reteGraphBetaNodeID, len(compiledRules))

	for _, rule := range compiledRules {
		var terminalID reteGraphTerminalNodeID
		for _, branch := range rule.executionConditionBranches() {
			if branchContainsAggregate(branch) {
				if graph.compileAggregateBranch(rule, branch, alphaIndex, betaIndex, &terminalID, templatesByKey) {
					continue
				}
				continue
			}
			graph.ruleBranchPlans = append(graph.ruleBranchPlans, reteGraphRuleBranchPlan{
				ruleRevisionID: rule.revisionID,
				branchID:       branch.id,
				conditions:     cloneRuleConditionBranchConditions(branch.conditions),
			})
			var current reteGraphStageRef
			haveStage := false
			plans := branch.plans

			for conditionIndex, condition := range plans {
				alphaConstraints, alphaPredicates := graphAlphaConstraintsAndPredicates(condition.constraints, condition.predicates)
				alphaID, created := graph.internAlphaNode(alphaIndex, condition.target, alphaConstraints, alphaPredicates)
				alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
				if alphaNode := graph.alphaNode(alphaID); alphaNode != nil && alphaNode.entry.conditionID == "" && conditionIndex == 0 {
					alphaNode.entry = graphTokenEntryForCondition(condition)
				}
				supportedAlpha := reteGraphSupportsAlpha(condition.target, templatesByKey)
				if supportedAlpha {
					graph.appendAlphaConsumer(alphaID, reteBetaConditionRoute{
						ruleRevisionID: rule.revisionID,
						conditionIndex: conditionIndex,
						conditionID:    condition.id,
						bindingSlot:    condition.bindingSlot,
					})
				}
				if created && supportedAlpha {
					route := reteGraphAlphaRouteSelector{}
					if alphaNode := graph.alphaNode(alphaID); alphaNode != nil {
						template := templatesByKey[condition.target.templateKey]
						route = reteGraphAlphaRouteSelectorForConstraints(template, condition.constraints)
						alphaNode.route = route
					}
					switch condition.target.kind {
					case conditionTargetTemplateKey:
						graph.routesByTemplateKey[condition.target.templateKey] = append(graph.routesByTemplateKey[condition.target.templateKey], alphaID)
						graph.appendAlphaRoute(condition.target.templateKey, alphaID, route)
					case conditionTargetName:
						graph.routesByName[condition.target.name] = append(graph.routesByName[condition.target.name], alphaID)
					}
				}
				if !haveStage {
					current = alphaRef
					haveStage = true
					continue
				}

				betaKind := reteGraphBetaNodeJoin
				if condition.negated {
					betaKind = reteGraphBetaNodeNot
				}
				betaID, _ := graph.internBetaNode(betaIndex, betaKind, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates))
				if betaNode := graph.betaNode(betaID); betaNode != nil && betaNode.entry.conditionID == "" && betaKind == reteGraphBetaNodeJoin {
					betaNode.entry = graphTokenEntryForCondition(condition)
				}
				leftEntry := bindingTupleEntry{}
				if current.kind == reteGraphStageAlpha && conditionIndex > 0 {
					leftEntry = graphTokenEntryForCondition(plans[conditionIndex-1])
				}
				graph.appendStageSuccessor(current, reteGraphStageSuccessor{
					betaNodeID: betaID,
					side:       reteGraphBetaInputLeft,
					entry:      leftEntry,
				})
				graph.appendStageSuccessor(alphaRef, reteGraphStageSuccessor{
					betaNodeID: betaID,
					side:       reteGraphBetaInputRight,
					entry:      graphTokenEntryForCondition(condition),
				})
				current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
			}

			if !haveStage {
				continue
			}
			if terminalID == 0 {
				graph.terminalNodes = append(graph.terminalNodes, reteGraphTerminalNode{
					id:             reteGraphTerminalNodeID(len(graph.terminalNodes) + 1),
					ruleRevisionID: rule.revisionID,
					input:          current,
				})
				terminalID = reteGraphTerminalNodeID(len(graph.terminalNodes))
			}
			terminalEntry := bindingTupleEntry{}
			if current.kind == reteGraphStageAlpha && len(plans) > 0 {
				terminalEntry = graphTokenEntryForCondition(plans[0])
			}
			graph.appendTerminal(current, reteGraphTerminalRoute{
				terminalID: terminalID,
				entry:      terminalEntry,
				branchID:   branch.id,
			})
		}
	}

	return graph
}

func (g *reteGraph) compileAggregateBranch(rule compiledRule, branch compiledConditionBranch, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, terminalID *reteGraphTerminalNodeID, templatesByKey map[TemplateKey]Template) bool {
	if g == nil || len(branch.plans) == 0 {
		return false
	}
	aggregateIndex := -1
	for i, plan := range branch.plans {
		if plan.aggregate == nil {
			continue
		}
		if aggregateIndex >= 0 {
			return false
		}
		aggregateIndex = i
	}
	if aggregateIndex < 0 || aggregateIndex != len(branch.plans)-1 {
		return false
	}
	condition := branch.plans[aggregateIndex]
	if !reteGraphSupportsAggregateCondition(condition, aggregateIndex > 0) {
		return false
	}

	var current reteGraphStageRef
	haveStage := false
	for conditionIndex := 0; conditionIndex < aggregateIndex; conditionIndex++ {
		outer := branch.plans[conditionIndex]
		if outer.aggregate != nil {
			return false
		}
		alphaID := g.compileConditionAlpha(rule, outer, conditionIndex, alphaIndex, templatesByKey, true)
		alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
		if alphaID == 0 {
			return false
		}
		if !haveStage {
			current = alphaRef
			haveStage = true
			continue
		}
		betaKind := reteGraphBetaNodeJoin
		if outer.negated {
			betaKind = reteGraphBetaNodeNot
		}
		betaID, _ := g.internBetaNode(betaIndex, betaKind, current, alphaRef, outer.joins, betaResidualExpressionPredicates(outer.predicates))
		if betaNode := g.betaNode(betaID); betaNode != nil && betaNode.entry.conditionID == "" && betaKind == reteGraphBetaNodeJoin {
			betaNode.entry = graphTokenEntryForCondition(outer)
		}
		leftEntry := bindingTupleEntry{}
		if current.kind == reteGraphStageAlpha && conditionIndex > 0 {
			leftEntry = graphTokenEntryForCondition(branch.plans[conditionIndex-1])
		}
		g.appendStageSuccessor(current, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputLeft,
			entry:      leftEntry,
		})
		g.appendStageSuccessor(alphaRef, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputRight,
			entry:      graphTokenEntryForCondition(outer),
		})
		current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
	}

	input := condition.aggregate.inputPlans[0]
	inputAlphaID := g.compileConditionAlpha(rule, input, aggregateIndex, alphaIndex, templatesByKey, false)
	inputAlphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(inputAlphaID)}
	if inputAlphaID == 0 {
		return false
	}
	aggregateInput := inputAlphaRef
	if haveStage {
		betaID, _ := g.internBetaNode(betaIndex, reteGraphBetaNodeJoin, current, inputAlphaRef, input.joins, betaResidualExpressionPredicates(input.predicates))
		if betaNode := g.betaNode(betaID); betaNode != nil && betaNode.entry.conditionID == "" {
			betaNode.entry = graphTokenEntryForCondition(input)
		}
		leftEntry := bindingTupleEntry{}
		if current.kind == reteGraphStageAlpha && aggregateIndex > 0 {
			leftEntry = graphTokenEntryForCondition(branch.plans[aggregateIndex-1])
		}
		g.appendStageSuccessor(current, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputLeft,
			entry:      leftEntry,
		})
		g.appendStageSuccessor(inputAlphaRef, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputRight,
			entry:      graphTokenEntryForCondition(input),
		})
		aggregateInput = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
	}

	outer := reteGraphStageRef{}
	if haveStage {
		outer = current
	}
	aggregateID := g.appendAggregate(aggregateInput, outer, graphTokenEntryForCondition(input), condition.id, condition.bindingSlot, condition.aggregate.specs, graphTokenEntriesForAggregateBindings(rule, condition))
	aggregateRef := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregateID)}
	if terminalID != nil && *terminalID == 0 {
		g.terminalNodes = append(g.terminalNodes, reteGraphTerminalNode{
			id:             reteGraphTerminalNodeID(len(g.terminalNodes) + 1),
			ruleRevisionID: rule.revisionID,
			input:          aggregateRef,
		})
		*terminalID = reteGraphTerminalNodeID(len(g.terminalNodes))
	}
	if terminalID == nil || *terminalID == 0 {
		return false
	}
	g.ruleBranchPlans = append(g.ruleBranchPlans, reteGraphRuleBranchPlan{
		ruleRevisionID: rule.revisionID,
		branchID:       branch.id,
		conditions:     cloneRuleConditionBranchConditions(branch.conditions),
	})
	g.appendTerminal(aggregateRef, reteGraphTerminalRoute{
		terminalID: *terminalID,
		branchID:   branch.id,
	})
	return true
}

func (g *reteGraph) compileConditionAlpha(rule compiledRule, condition compiledConditionPlan, conditionIndex int, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, templatesByKey map[TemplateKey]Template, appendConsumer bool) reteGraphAlphaNodeID {
	alphaConstraints, alphaPredicates := graphAlphaConstraintsAndPredicates(condition.constraints, condition.predicates)
	alphaID, created := g.internAlphaNode(alphaIndex, condition.target, alphaConstraints, alphaPredicates)
	if alphaNode := g.alphaNode(alphaID); alphaNode != nil && alphaNode.entry.conditionID == "" && conditionIndex == 0 {
		alphaNode.entry = graphTokenEntryForCondition(condition)
	}
	supportedAlpha := reteGraphSupportsAlpha(condition.target, templatesByKey)
	if appendConsumer && supportedAlpha {
		g.appendAlphaConsumer(alphaID, reteBetaConditionRoute{
			ruleRevisionID: rule.revisionID,
			conditionIndex: conditionIndex,
			conditionID:    condition.id,
			bindingSlot:    condition.bindingSlot,
		})
	}
	if created && supportedAlpha {
		route := reteGraphAlphaRouteSelector{}
		if alphaNode := g.alphaNode(alphaID); alphaNode != nil {
			template := templatesByKey[condition.target.templateKey]
			route = reteGraphAlphaRouteSelectorForConstraints(template, condition.constraints)
			alphaNode.route = route
		}
		switch condition.target.kind {
		case conditionTargetTemplateKey:
			g.routesByTemplateKey[condition.target.templateKey] = append(g.routesByTemplateKey[condition.target.templateKey], alphaID)
			g.appendAlphaRoute(condition.target.templateKey, alphaID, route)
		case conditionTargetName:
			g.routesByName[condition.target.name] = append(g.routesByName[condition.target.name], alphaID)
		}
	}
	return alphaID
}

func branchContainsAggregate(branch compiledConditionBranch) bool {
	for _, plan := range branch.plans {
		if plan.aggregate != nil {
			return true
		}
	}
	return false
}

func reteGraphSupportsAggregateCondition(condition compiledConditionPlan, allowInputJoins bool) bool {
	if condition.aggregate == nil || len(condition.aggregate.inputPlans) != 1 || len(condition.aggregate.specs) == 0 {
		return false
	}
	input := condition.aggregate.inputPlans[0]
	if input.aggregate != nil || input.negated || len(betaResidualExpressionPredicates(input.predicates)) != 0 {
		return false
	}
	if !allowInputJoins && len(input.joins) != 0 {
		return false
	}
	for _, spec := range condition.aggregate.specs {
		switch spec.kind {
		case AggregateCount, AggregateSum, AggregateMin, AggregateMax:
		default:
			return false
		}
	}
	return true
}

func reteGraphSupportsAlpha(target conditionTarget, templatesByKey map[TemplateKey]Template) bool {
	switch target.kind {
	case conditionTargetTemplateKey:
		if target.templateKey == "" {
			return false
		}
		template, ok := templatesByKey[target.templateKey]
		return ok && template.closed
	case conditionTargetName:
		return target.name != ""
	default:
		return false
	}
}

func (g *reteGraph) internAlphaNode(index map[reteGraphAlphaKey]reteGraphAlphaNodeID, target conditionTarget, constraints []compiledFieldConstraint, predicates []compiledExpressionPredicate) (reteGraphAlphaNodeID, bool) {
	if g == nil {
		return 0, false
	}
	key := reteGraphAlphaKey{
		target: reteGraphTargetKey{
			kind:        target.kind,
			name:        target.name,
			templateKey: target.templateKey,
		},
		constraints: serializeCompiledFieldConstraints(constraints),
		predicates:  serializeCompiledExpressionPredicates(predicates),
	}
	if id, ok := index[key]; ok {
		return id, false
	}

	id := reteGraphAlphaNodeID(len(g.alphaNodes) + 1)
	g.alphaNodes = append(g.alphaNodes, reteGraphAlphaNode{
		id:          id,
		target:      target,
		constraints: cloneCompiledFieldConstraints(constraints),
		predicates:  cloneCompiledExpressionPredicates(predicates),
	})
	index[key] = id
	return id, true
}

func (g *reteGraph) appendAlphaConsumer(id reteGraphAlphaNodeID, route reteBetaConditionRoute) {
	if g == nil || id <= 0 {
		return
	}
	index := int(id) - 1
	if index < 0 || index >= len(g.alphaNodes) {
		return
	}
	g.alphaNodes[index].consumers = append(g.alphaNodes[index].consumers, route)
}

func (g *reteGraph) alphaNodeEntry(ref reteGraphStageRef) bindingTupleEntry {
	if g == nil || ref.kind != reteGraphStageAlpha || ref.id <= 0 {
		return bindingTupleEntry{}
	}
	node := g.alphaNode(reteGraphAlphaNodeID(ref.id))
	if node == nil {
		return bindingTupleEntry{}
	}
	return node.entry
}

func (g *reteGraph) betaNode(id reteGraphBetaNodeID) *reteGraphBetaNode {
	if g == nil || id <= 0 {
		return nil
	}
	index := int(id) - 1
	if index < 0 || index >= len(g.betaNodes) {
		return nil
	}
	return &g.betaNodes[index]
}

func (g *reteGraph) aggregateNode(id reteGraphAggregateNodeID) *reteGraphAggregateNode {
	if g == nil || id <= 0 {
		return nil
	}
	index := int(id) - 1
	if index < 0 || index >= len(g.aggregateNodes) {
		return nil
	}
	return &g.aggregateNodes[index]
}

func (g *reteGraph) stageTokenWidth(stage reteGraphStageRef) int {
	if g == nil {
		return 0
	}
	switch stage.kind {
	case reteGraphStageAlpha:
		return 1
	case reteGraphStageBeta:
		node := g.betaNode(reteGraphBetaNodeID(stage.id))
		if node == nil {
			return 0
		}
		leftWidth := g.stageTokenWidth(node.left)
		if leftWidth <= 0 {
			return 0
		}
		if node.kind == reteGraphBetaNodeNot {
			return leftWidth
		}
		return leftWidth + 1
	case reteGraphStageAggregate:
		node := g.aggregateNode(reteGraphAggregateNodeID(stage.id))
		if node == nil {
			return 0
		}
		if node.outer.kind != reteGraphStageUnknown {
			return g.stageTokenWidth(node.outer) + len(node.specs)
		}
		return len(node.specs)
	default:
		return 0
	}
}

func (g *reteGraph) appendAggregate(input, outer reteGraphStageRef, inputEntry bindingTupleEntry, conditionID ConditionID, bindingSlot int, specs []compiledAggregateSpec, entries []bindingTupleEntry) reteGraphAggregateNodeID {
	if g == nil || input.kind == reteGraphStageUnknown {
		return 0
	}
	id := reteGraphAggregateNodeID(len(g.aggregateNodes) + 1)
	g.aggregateNodes = append(g.aggregateNodes, reteGraphAggregateNode{
		id:          id,
		input:       input,
		outer:       outer,
		inputEntry:  cloneBindingTupleEntry(inputEntry),
		conditionID: conditionID,
		bindingSlot: bindingSlot,
		specs:       cloneCompiledAggregateSpecs(specs),
		entries:     cloneBindingTupleEntries(entries),
	})
	g.aggregatesByStage[input] = append(g.aggregatesByStage[input], id)
	if outer.kind != reteGraphStageUnknown {
		g.aggregateOuters[outer] = append(g.aggregateOuters[outer], id)
	}
	return id
}

func (g *reteGraph) appendStageSuccessor(source reteGraphStageRef, successor reteGraphStageSuccessor) {
	if g == nil || source.kind == reteGraphStageUnknown || successor.betaNodeID <= 0 {
		return
	}
	g.successorsByStage[source] = append(g.successorsByStage[source], successor)
}

func (g *reteGraph) appendTerminal(source reteGraphStageRef, terminal reteGraphTerminalRoute) {
	if g == nil || source.kind == reteGraphStageUnknown || terminal.terminalID <= 0 {
		return
	}
	g.terminalsByStage[source] = append(g.terminalsByStage[source], terminal)
}

func (g *reteGraph) alphaNode(id reteGraphAlphaNodeID) *reteGraphAlphaNode {
	if g == nil || id <= 0 {
		return nil
	}
	index := int(id) - 1
	if index < 0 || index >= len(g.alphaNodes) {
		return nil
	}
	return &g.alphaNodes[index]
}

func (g *reteGraph) appendAlphaRoute(templateKey TemplateKey, id reteGraphAlphaNodeID, route reteGraphAlphaRouteSelector) {
	if g == nil || templateKey == "" || id <= 0 {
		return
	}
	table := g.alphaRouteTables[templateKey]
	if table == nil {
		table = &reteGraphAlphaRouteTable{}
		g.alphaRouteTables[templateKey] = table
	}
	if !route.enabled {
		table.unindexed = append(table.unindexed, id)
		return
	}
	key := route.key()
	if table.indexed == nil {
		table.indexed = make(map[reteGraphAlphaRouteKey][]reteGraphAlphaNodeID)
	}
	if _, ok := table.indexed[key]; !ok {
		if !table.hasIndexedField(route.fieldSlot) {
			table.indexedFields = append(table.indexedFields, route.fieldSlot)
		}
	}
	table.indexed[key] = append(table.indexed[key], id)
}

func (t *reteGraphAlphaRouteTable) hasIndexedField(fieldSlot int) bool {
	if t == nil {
		return false
	}
	return slices.Contains(t.indexedFields, fieldSlot)
}

func (t *reteGraphAlphaRouteTable) singleIndexedField() (int, bool) {
	if t == nil || len(t.unindexed) != 0 || len(t.indexedFields) != 1 {
		return 0, false
	}
	return t.indexedFields[0], true
}

func reteGraphAlphaRouteSelectorForConstraints(template Template, constraints []compiledFieldConstraint) reteGraphAlphaRouteSelector {
	for _, constraint := range constraints {
		if constraint.operator != FieldConstraintOpEqual || constraint.fieldSlot < 0 {
			continue
		}
		value, ok := reteGraphAlphaRouteValueFromValue(constraint.value)
		if !ok {
			continue
		}
		if !reteGraphAlphaRouteFieldKindMatches(template, constraint.fieldSlot, value.kind) {
			continue
		}
		return reteGraphAlphaRouteSelector{
			fieldSlot: constraint.fieldSlot,
			value:     value,
			enabled:   true,
		}
	}
	return reteGraphAlphaRouteSelector{}
}

func reteGraphAlphaRouteFieldKindMatches(template Template, fieldSlot int, kind ValueKind) bool {
	if fieldSlot < 0 || fieldSlot >= len(template.fields) {
		return false
	}
	return template.fields[fieldSlot].Kind == kind
}

func (s reteGraphAlphaRouteSelector) key() reteGraphAlphaRouteKey {
	return reteGraphAlphaRouteKey{
		fieldSlot: s.fieldSlot,
		value:     s.value,
	}
}

func reteGraphAlphaRouteValueFromValue(value Value) (reteGraphAlphaRouteValue, bool) {
	switch value.Kind() {
	case ValueBool:
		if value.boolValue {
			return reteGraphAlphaRouteValue{kind: ValueBool, bits: 1, valid: true}, true
		}
		return reteGraphAlphaRouteValue{kind: ValueBool, valid: true}, true
	case ValueInt:
		return reteGraphAlphaRouteValue{kind: ValueInt, bits: value.intValue, valid: true}, true
	case ValueString:
		return reteGraphAlphaRouteValue{kind: ValueString, text: value.stringValue, valid: true}, true
	default:
		return reteGraphAlphaRouteValue{}, false
	}
}

func (n reteGraphAlphaNode) matchesSnapshot(fact FactSnapshot) bool {
	return n.matchesSnapshotWithCounters(fact, nil)
}

func (n reteGraphAlphaNode) matchesSnapshotWithCounters(fact FactSnapshot, span *propagationCounterSpan) bool {
	switch n.target.kind {
	case conditionTargetTemplateKey:
		if fact.TemplateKey() != n.target.templateKey {
			return false
		}
	case conditionTargetName:
		if fact.Name() != n.target.name {
			return false
		}
	default:
		return false
	}
	ref := newConditionFactRefFromSnapshot(fact)
	for _, constraint := range n.constraints {
		if !constraint.matches(ref) {
			return false
		}
	}
	if !n.expressionPredicatesMatch(ref, span) {
		return false
	}
	return true
}

func (n reteGraphAlphaNode) matchesWorking(fact *workingFact) bool {
	return n.matchesWorkingWithCounters(fact, nil)
}

func (n reteGraphAlphaNode) matchesWorkingWithCounters(fact *workingFact, span *propagationCounterSpan) bool {
	if fact == nil {
		return false
	}
	switch n.target.kind {
	case conditionTargetTemplateKey:
		if fact.templateKey != n.target.templateKey {
			return false
		}
	case conditionTargetName:
		if fact.name != n.target.name {
			return false
		}
	default:
		return false
	}
	for _, constraint := range n.constraints {
		if !constraint.matchesWorking(fact) {
			return false
		}
	}
	ref := newConditionFactRefFromWorkingFact(fact)
	if !n.expressionPredicatesMatch(ref, span) {
		return false
	}
	return true
}

func (n reteGraphAlphaNode) expressionPredicatesMatch(fact conditionFactRef, span *propagationCounterSpan) bool {
	for _, predicate := range n.predicates {
		if span != nil {
			span.recordExpressionPredicateTest()
		}
		ok, err := predicate.matches(fact, nil)
		if err != nil {
			if span != nil {
				span.recordExpressionPredicateError()
			}
			return false
		}
		if !ok {
			if span != nil {
				span.recordExpressionPredicateFailure()
			}
			return false
		}
	}
	return true
}

func (g *reteGraph) internBetaNode(index map[reteGraphBetaKey]reteGraphBetaNodeID, kind reteGraphBetaNodeKind, left, right reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate) (reteGraphBetaNodeID, bool) {
	if g == nil {
		return 0, false
	}
	if kind == 0 {
		kind = reteGraphBetaNodeJoin
	}
	hashJoins, residualJoins := splitCompiledJoinConstraints(joins)
	hashJoins = append(hashJoins, expressionPredicateHashJoins(predicates)...)
	key := reteGraphBetaKey{
		kind:       kind,
		left:       left,
		right:      right,
		joins:      serializeCompiledJoinConstraints(joins),
		predicates: serializeCompiledExpressionPredicates(predicates),
	}
	if id, ok := index[key]; ok {
		return id, false
	}

	id := reteGraphBetaNodeID(len(g.betaNodes) + 1)
	g.betaNodes = append(g.betaNodes, reteGraphBetaNode{
		id:            id,
		kind:          kind,
		left:          left,
		right:         right,
		joins:         cloneCompiledJoinConstraints(joins),
		hashJoins:     cloneCompiledJoinConstraints(hashJoins),
		residualJoins: cloneCompiledJoinConstraints(residualJoins),
		predicates:    cloneCompiledExpressionPredicates(predicates),
	})
	index[key] = id
	return id, true
}

func graphTokenEntryForCondition(condition compiledConditionPlan) bindingTupleEntry {
	return bindingTupleEntry{
		binding:        condition.binding,
		bindingSlot:    condition.bindingSlot,
		conditionOrder: condition.bindingSlot,
		conditionID:    condition.id,
		conditionPath:  cloneIntPath(condition.path),
	}
}

func graphTokenEntriesForAggregateBindings(rule compiledRule, condition compiledConditionPlan) []bindingTupleEntry {
	if condition.aggregate == nil {
		return nil
	}
	entries := make([]bindingTupleEntry, len(condition.aggregate.specs))
	for i := range entries {
		bindingSlot := condition.bindingSlot + i
		entry := bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    bindingSlot,
			conditionOrder: bindingSlot,
			conditionID:    condition.id,
			conditionPath:  cloneIntPath(condition.path),
		}
		if bindingSlot >= 0 && bindingSlot < len(rule.conditions) {
			public := rule.conditions[bindingSlot]
			entry.binding = public.binding
			entry.conditionOrder = public.order
			entry.conditionID = public.id
		}
		entries[i] = entry
	}
	return entries
}

func cloneCompiledAggregateSpecs(in []compiledAggregateSpec) []compiledAggregateSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]compiledAggregateSpec, len(in))
	copy(out, in)
	return out
}

func (g *reteGraph) debugSummary() reteGraphDebugSummary {
	if g == nil {
		return reteGraphDebugSummary{}
	}
	return reteGraphDebugSummary{
		AlphaNodes:          cloneReteGraphAlphaNodes(g.alphaNodes),
		BetaNodes:           cloneReteGraphBetaNodes(g.betaNodes),
		TerminalNodes:       cloneReteGraphTerminalNodes(g.terminalNodes),
		RuleBranchPlans:     cloneReteGraphRuleBranchPlans(g.ruleBranchPlans),
		RoutesByTemplateKey: cloneReteGraphAlphaRoutes(g.routesByTemplateKey),
		RoutesByName:        cloneReteGraphNameRoutes(g.routesByName),
	}
}

func (r *Ruleset) reteGraphDebugSummary() reteGraphDebugSummary {
	if r == nil || r.graph == nil {
		return reteGraphDebugSummary{}
	}
	return r.graph.debugSummary()
}

func cloneReteGraphAlphaNodes(in []reteGraphAlphaNode) []reteGraphAlphaNode {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphAlphaNode, len(in))
	for i, node := range in {
		out[i] = node
		out[i].constraints = cloneCompiledFieldConstraints(node.constraints)
		out[i].predicates = cloneCompiledExpressionPredicates(node.predicates)
		out[i].consumers = cloneReteGraphAlphaConsumers(node.consumers)
		out[i].entry = cloneBindingTupleEntry(node.entry)
	}
	return out
}

func cloneReteGraphBetaNodes(in []reteGraphBetaNode) []reteGraphBetaNode {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphBetaNode, len(in))
	for i, node := range in {
		out[i] = node
		out[i].joins = cloneCompiledJoinConstraints(node.joins)
		out[i].hashJoins = cloneCompiledJoinConstraints(node.hashJoins)
		out[i].residualJoins = cloneCompiledJoinConstraints(node.residualJoins)
		out[i].predicates = cloneCompiledExpressionPredicates(node.predicates)
		out[i].entry = cloneBindingTupleEntry(node.entry)
	}
	return out
}

func cloneReteGraphTerminalNodes(in []reteGraphTerminalNode) []reteGraphTerminalNode {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphTerminalNode, len(in))
	copy(out, in)
	return out
}

func cloneReteGraphRuleBranchPlans(in []reteGraphRuleBranchPlan) []reteGraphRuleBranchPlan {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphRuleBranchPlan, len(in))
	for i, plan := range in {
		out[i] = plan
		out[i].conditions = cloneRuleConditionBranchConditions(plan.conditions)
	}
	return out
}

func cloneReteGraphAlphaRoutes(in map[TemplateKey][]reteGraphAlphaNodeID) map[TemplateKey][]reteGraphAlphaNodeID {
	if len(in) == 0 {
		return nil
	}
	out := make(map[TemplateKey][]reteGraphAlphaNodeID, len(in))
	for key, ids := range in {
		out[key] = append([]reteGraphAlphaNodeID(nil), ids...)
	}
	return out
}

func cloneReteGraphNameRoutes(in map[string][]reteGraphAlphaNodeID) map[string][]reteGraphAlphaNodeID {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]reteGraphAlphaNodeID, len(in))
	for key, ids := range in {
		out[key] = append([]reteGraphAlphaNodeID(nil), ids...)
	}
	return out
}

func cloneReteGraphAlphaConsumers(in []reteBetaConditionRoute) []reteBetaConditionRoute {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteBetaConditionRoute, len(in))
	copy(out, in)
	return out
}

func cloneBindingTupleEntry(entry bindingTupleEntry) bindingTupleEntry {
	if len(entry.conditionPath) == 0 {
		return entry
	}
	entry.conditionPath = append([]int(nil), entry.conditionPath...)
	return entry
}

func cloneCompiledFieldConstraints(in []compiledFieldConstraint) []compiledFieldConstraint {
	if len(in) == 0 {
		return nil
	}
	out := make([]compiledFieldConstraint, len(in))
	copy(out, in)
	return out
}

func cloneCompiledJoinConstraints(in []compiledJoinConstraint) []compiledJoinConstraint {
	if len(in) == 0 {
		return nil
	}
	out := make([]compiledJoinConstraint, len(in))
	for i, constraint := range in {
		out[i] = constraint
		out[i].path = append([]int(nil), constraint.path...)
	}
	return out
}

func splitCompiledJoinConstraints(in []compiledJoinConstraint) ([]compiledJoinConstraint, []compiledJoinConstraint) {
	if len(in) == 0 {
		return nil, nil
	}
	hashJoins := make([]compiledJoinConstraint, 0, len(in))
	residualJoins := make([]compiledJoinConstraint, 0, len(in))
	for _, join := range in {
		if join.isHashJoin() {
			hashJoins = append(hashJoins, join)
			continue
		}
		residualJoins = append(residualJoins, join)
	}
	return hashJoins, residualJoins
}

func expressionPredicateHashJoins(predicates []compiledExpressionPredicate) []compiledJoinConstraint {
	if len(predicates) == 0 {
		return nil
	}
	out := make([]compiledJoinConstraint, 0, len(predicates))
	for _, predicate := range predicates {
		join, ok := expressionPredicateHashJoin(predicate)
		if ok {
			out = append(out, join)
		}
	}
	return out
}

func expressionPredicateHashJoin(predicate compiledExpressionPredicate) (compiledJoinConstraint, bool) {
	if predicate.placement != ExpressionPredicatePlacementBetaResidual {
		return compiledJoinConstraint{}, false
	}
	expression := predicate.expression
	if expression.kind != expressionNodeCompare || expression.compareOp != ExpressionCompareEqual || len(expression.operands) != 2 {
		return compiledJoinConstraint{}, false
	}

	current, binding, ok := expressionPredicateHashJoinOperands(expression.operands[0], expression.operands[1])
	if !ok {
		return compiledJoinConstraint{}, false
	}
	if current.field == "" || binding.field == "" || binding.binding == "" || binding.bindingSlot < 0 {
		return compiledJoinConstraint{}, false
	}
	conditionIndex := -1
	if len(predicate.path) > 0 {
		conditionIndex = predicate.path[0]
	}
	return compiledJoinConstraint{
		path:           cloneIntPath(predicate.path),
		bindingSlot:    conditionIndex,
		field:          current.field,
		fieldSlot:      current.fieldSlot,
		operator:       FieldConstraintOpEqual,
		refBinding:     binding.binding,
		refBindingSlot: binding.bindingSlot,
		refField:       binding.field,
		refFieldSlot:   binding.fieldSlot,
		indexable:      true,
		indexKind:      joinIndexEquality,
	}, true
}

func expressionPredicateHashJoinOperands(left, right compiledExpression) (compiledExpression, compiledExpression, bool) {
	if left.kind == expressionNodeCurrentField && right.kind == expressionNodeBindingField {
		return left, right, true
	}
	if right.kind == expressionNodeCurrentField && left.kind == expressionNodeBindingField {
		return right, left, true
	}
	return compiledExpression{}, compiledExpression{}, false
}

func graphAlphaConstraintsAndPredicates(constraints []compiledFieldConstraint, predicates []compiledExpressionPredicate) ([]compiledFieldConstraint, []compiledExpressionPredicate) {
	alphaPredicates := alphaExpressionPredicates(predicates)
	if len(alphaPredicates) == 0 {
		return constraints, nil
	}
	outConstraints := cloneCompiledFieldConstraints(constraints)
	outPredicates := make([]compiledExpressionPredicate, 0, len(alphaPredicates))
	for _, predicate := range alphaPredicates {
		constraint, ok := expressionPredicateAlphaConstraint(predicate)
		if ok {
			outConstraints = append(outConstraints, constraint)
			continue
		}
		outPredicates = append(outPredicates, predicate)
	}
	return outConstraints, outPredicates
}

func expressionPredicateAlphaConstraint(predicate compiledExpressionPredicate) (compiledFieldConstraint, bool) {
	if predicate.placement != ExpressionPredicatePlacementAlpha {
		return compiledFieldConstraint{}, false
	}
	expression := predicate.expression
	if expression.kind != expressionNodeCompare || len(expression.operands) != 2 {
		return compiledFieldConstraint{}, false
	}
	current, constant, operator, ok := expressionPredicateAlphaConstraintOperands(expression.operands[0], expression.operands[1], expression.compareOp)
	if !ok {
		return compiledFieldConstraint{}, false
	}
	if current.field == "" {
		return compiledFieldConstraint{}, false
	}
	return compiledFieldConstraint{
		field:     current.field,
		operator:  operator,
		value:     cloneValue(constant.value),
		fieldSlot: current.fieldSlot,
	}, true
}

func expressionPredicateAlphaConstraintOperands(left, right compiledExpression, operator ExpressionComparisonOperator) (compiledExpression, compiledExpression, FieldConstraintOperator, bool) {
	if left.kind == expressionNodeCurrentField && right.kind == expressionNodeConst {
		fieldOperator, ok := expressionComparisonFieldConstraintOperator(operator)
		return left, right, fieldOperator, ok
	}
	if left.kind == expressionNodeConst && right.kind == expressionNodeCurrentField {
		fieldOperator, ok := expressionComparisonFieldConstraintOperator(flipExpressionComparisonOperator(operator))
		return right, left, fieldOperator, ok
	}
	return compiledExpression{}, compiledExpression{}, "", false
}

func expressionComparisonFieldConstraintOperator(operator ExpressionComparisonOperator) (FieldConstraintOperator, bool) {
	switch operator {
	case ExpressionCompareEqual:
		return FieldConstraintOpEqual, true
	case ExpressionCompareNotEqual:
		return FieldConstraintOpNotEqual, true
	case ExpressionCompareLessThan:
		return FieldConstraintOpLessThan, true
	case ExpressionCompareLessOrEqual:
		return FieldConstraintOpLessOrEqual, true
	case ExpressionCompareGreaterThan:
		return FieldConstraintOpGreaterThan, true
	case ExpressionCompareGreaterOrEqual:
		return FieldConstraintOpGreaterOrEqual, true
	default:
		return "", false
	}
}

func flipExpressionComparisonOperator(operator ExpressionComparisonOperator) ExpressionComparisonOperator {
	switch operator {
	case ExpressionCompareLessThan:
		return ExpressionCompareGreaterThan
	case ExpressionCompareLessOrEqual:
		return ExpressionCompareGreaterOrEqual
	case ExpressionCompareGreaterThan:
		return ExpressionCompareLessThan
	case ExpressionCompareGreaterOrEqual:
		return ExpressionCompareLessOrEqual
	default:
		return operator
	}
}

func alphaExpressionPredicates(predicates []compiledExpressionPredicate) []compiledExpressionPredicate {
	if len(predicates) == 0 {
		return nil
	}
	out := make([]compiledExpressionPredicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate.placement == ExpressionPredicatePlacementAlpha {
			out = append(out, predicate)
		}
	}
	return out
}

func betaResidualExpressionPredicates(predicates []compiledExpressionPredicate) []compiledExpressionPredicate {
	if len(predicates) == 0 {
		return nil
	}
	out := make([]compiledExpressionPredicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate.placement == ExpressionPredicatePlacementBetaResidual {
			out = append(out, predicate)
		}
	}
	return out
}

func serializeCompiledFieldConstraints(constraints []compiledFieldConstraint) string {
	if len(constraints) == 0 {
		return ""
	}
	parts := make([]string, len(constraints))
	for i, constraint := range constraints {
		parts[i] = serializeCompiledFieldConstraint(constraint)
	}
	sort.Strings(parts)
	sum := sha256.New()
	sum.Write([]byte("gess/rete-graph/constraint/v1\n"))
	for _, part := range parts {
		sum.Write(fmt.Appendf(nil, "constraint:%d:%s\n", len(part), part))
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

func serializeCompiledFieldConstraint(constraint compiledFieldConstraint) string {
	valueKey := constraint.value.canonicalKey()
	return fmt.Sprintf(
		"field:%d:%s\noperator:%d:%s\nvalue:%d:%s\nfield-slot:%d\n",
		len(constraint.field), constraint.field,
		len(constraint.operator), constraint.operator,
		len(valueKey), valueKey,
		constraint.fieldSlot,
	)
}

func serializeCompiledJoinConstraints(joins []compiledJoinConstraint) string {
	if len(joins) == 0 {
		return ""
	}
	parts := make([]string, len(joins))
	for i, join := range joins {
		parts[i] = serializeCompiledJoinConstraint(join)
	}
	sort.Strings(parts)
	sum := sha256.New()
	sum.Write([]byte("gess/rete-graph/join/v1\n"))
	for _, part := range parts {
		sum.Write(fmt.Appendf(nil, "join:%d:%s\n", len(part), part))
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

func serializeCompiledJoinConstraint(join compiledJoinConstraint) string {
	return fmt.Sprintf(
		"field:%d:%s\noperator:%d:%s\nref-field:%d:%s\nbinding-slot:%d\nref-binding-slot:%d\nfield-slot:%d\nref-field-slot:%d\n",
		len(join.field), join.field,
		len(join.operator), join.operator,
		len(join.refField), join.refField,
		join.bindingSlot,
		join.refBindingSlot,
		join.fieldSlot,
		join.refFieldSlot,
	)
}
