package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type reteGraph struct {
	alphaNodes          []reteGraphAlphaNode
	betaNodes           []reteGraphBetaNode
	aggregateNodes      []reteGraphAggregateNode
	terminalNodes       []reteGraphTerminalNode
	ruleBranchPlans     []reteGraphRuleBranchPlan
	branchInspections   []reteGraphBranchInspection
	queryTerminalIDs    map[string][]reteGraphTerminalNodeID
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
	reteGraphStageRoot
	reteGraphStageAlpha
	reteGraphStageBeta
	reteGraphStageAggregate
)

type reteGraphBetaNodeKind uint8

const (
	reteGraphBetaNodeJoin reteGraphBetaNodeKind = iota + 1
	reteGraphBetaNodeNot
	reteGraphBetaNodeFilter
	reteGraphBetaNodeResidualFilter
)

type reteGraphTerminalKind uint8

const (
	reteGraphTerminalRule reteGraphTerminalKind = iota + 1
	reteGraphTerminalQuery
)

type reteGraphStageRef struct {
	kind reteGraphStageKind
	id   int
}

type reteGraphAlphaNode struct {
	id             reteGraphAlphaNodeID
	target         conditionTarget
	constraints    []compiledFieldConstraint
	listPatterns   []compiledListPattern
	predicates     []compiledExpressionPredicate
	consumers      []reteBetaConditionRoute
	entry          bindingTupleEntry
	route          reteGraphAlphaRouteSelector
	generatedMatch reteGraphAlphaGeneratedMatch
	generatedOps   []reteGraphGeneratedAlphaOp
	edges          reteGraphStageEdges
}

type reteGraphAlphaGeneratedMatchKind uint8

const (
	reteGraphAlphaGeneratedMatchNone reteGraphAlphaGeneratedMatchKind = iota
	reteGraphAlphaGeneratedMatchTargetOnly
	reteGraphAlphaGeneratedMatchSlotEqual
)

type reteGraphAlphaGeneratedMatch struct {
	kind       reteGraphAlphaGeneratedMatchKind
	equalities []reteGraphAlphaGeneratedEquality
}

type reteGraphAlphaGeneratedEquality struct {
	fieldSlot int
	value     reteGraphAlphaRouteValue
}

type reteGraphGeneratedAlphaOpKind uint8

const (
	reteGraphGeneratedAlphaOpTerminal reteGraphGeneratedAlphaOpKind = iota + 1
	reteGraphGeneratedAlphaOpBetaLeft
	reteGraphGeneratedAlphaOpBetaRight
	reteGraphGeneratedAlphaOpAggregateOuter
	reteGraphGeneratedAlphaOpAggregateInput
)

type reteGraphGeneratedAlphaOp struct {
	kind        reteGraphGeneratedAlphaOpKind
	entry       bindingTupleEntry
	betaEntry   bindingTupleEntry
	terminalID  reteGraphTerminalNodeID
	branchID    int
	betaNodeID  reteGraphBetaNodeID
	aggregateID reteGraphAggregateNodeID
	side        reteGraphBetaInputSide
}

type reteGraphBetaNode struct {
	id                 reteGraphBetaNodeID
	kind               reteGraphBetaNodeKind
	left               reteGraphStageRef
	right              reteGraphStageRef
	joins              []compiledJoinConstraint
	hashJoins          []compiledJoinConstraint
	residualJoins      []compiledJoinConstraint
	predicates         []compiledExpressionPredicate
	rightPredicates    []compiledExpressionPredicate
	entry              bindingTupleEntry
	backchainDemands   []reteGraphBackchainDemandPlan
	rightHasLeftPrefix bool
	rightPrefixWidth   int
	edges              reteGraphStageEdges
}

type reteGraphBackchainDemandPlan struct {
	templateKey  TemplateKey
	side         reteGraphBetaInputSide
	slotCount    int
	defaultSlots []factSlot
	constSlots   []reteGraphBackchainDemandConstSlot
	joinSlots    []reteGraphBackchainDemandJoinSlot
	constraints  []compiledFieldConstraint
	joins        []compiledJoinConstraint
}

type reteGraphBackchainDemandConstSlot struct {
	slot  int
	value Value
}

type reteGraphBackchainDemandJoinSlot struct {
	slot        int
	bindingSlot int
	access      compiledPathAccess
	last        bool
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
	edges       reteGraphStageEdges
}

type reteGraphStageEdges struct {
	successors      []reteGraphStageSuccessor
	aggregateInputs []reteGraphAggregateNodeID
	aggregateOuters []reteGraphAggregateNodeID
	terminals       []reteGraphTerminalRoute
}

type reteGraphTerminalNode struct {
	id             reteGraphTerminalNodeID
	kind           reteGraphTerminalKind
	ruleRevisionID RuleRevisionID
	queryName      string
	input          reteGraphStageRef
	branchCount    int
	singleBranchID int
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
	Plan                reteGraphPlanInspection
}

type reteGraphPlanInspection struct {
	AlphaNodes     []reteGraphAlphaNodeInspection
	BetaNodes      []reteGraphBetaNodeInspection
	AggregateNodes []reteGraphAggregateNodeInspection
	TerminalNodes  []reteGraphTerminalNodeInspection
	Branches       []reteGraphBranchInspection
}

type reteGraphMemoryKind string

const (
	reteGraphMemoryAlphaFactSet   reteGraphMemoryKind = "alpha-fact-set"
	reteGraphMemoryBetaTokenHash  reteGraphMemoryKind = "beta-token-hash"
	reteGraphMemoryAggregate      reteGraphMemoryKind = "aggregate-bucket"
	reteGraphMemoryTerminalTokens reteGraphMemoryKind = "terminal-token-hash"
)

type reteGraphBranchOwnerKind string

const (
	reteGraphBranchOwnerRule  reteGraphBranchOwnerKind = "rule"
	reteGraphBranchOwnerQuery reteGraphBranchOwnerKind = "query"
)

type reteGraphAlphaNodeInspection struct {
	ID           reteGraphAlphaNodeID
	Target       conditionTarget
	Constraints  []compiledFieldConstraint
	ListPatterns []compiledListPattern
	Predicates   []compiledExpressionPredicate
	Route        reteGraphAlphaRouteSelector
	Consumers    []reteBetaConditionRoute
	Entry        bindingTupleEntry
	MemoryKind   reteGraphMemoryKind
}

type reteGraphBetaNodeInspection struct {
	ID              reteGraphBetaNodeID
	Kind            reteGraphBetaNodeKind
	Left            reteGraphStageRef
	Right           reteGraphStageRef
	Joins           []compiledJoinConstraint
	HashJoins       []compiledJoinConstraint
	ResidualJoins   []compiledJoinConstraint
	Predicates      []compiledExpressionPredicate
	RightPredicates []compiledExpressionPredicate
	Entry           bindingTupleEntry
	TokenWidth      int
	MemoryKind      reteGraphMemoryKind
}

type reteGraphAggregateNodeInspection struct {
	ID          reteGraphAggregateNodeID
	Input       reteGraphStageRef
	Outer       reteGraphStageRef
	ConditionID ConditionID
	BindingSlot int
	Specs       []compiledAggregateSpec
	TokenWidth  int
	MemoryKind  reteGraphMemoryKind
}

type reteGraphTerminalNodeInspection struct {
	ID             reteGraphTerminalNodeID
	Kind           reteGraphTerminalKind
	RuleRevisionID RuleRevisionID
	QueryName      string
	Input          reteGraphStageRef
	TokenWidth     int
	MemoryKind     reteGraphMemoryKind
}

type reteGraphBranchInspection struct {
	OwnerKind      reteGraphBranchOwnerKind
	RuleID         RuleID
	RuleName       string
	RuleRevisionID RuleRevisionID
	QueryName      string
	BranchID       int
	AuthoredOrder  []reteGraphConditionOrderInspection
	PlannedOrder   []reteGraphConditionOrderInspection
	Projections    []reteGraphTerminalProjectionInspection
	TerminalID     reteGraphTerminalNodeID
}

type reteGraphTerminalProjectionKind string

const (
	reteGraphTerminalProjectionActionBinding reteGraphTerminalProjectionKind = "action-binding"
	reteGraphTerminalProjectionActionField   reteGraphTerminalProjectionKind = "action-field"
	reteGraphTerminalProjectionActionValue   reteGraphTerminalProjectionKind = "action-value"
	reteGraphTerminalProjectionQueryFact     reteGraphTerminalProjectionKind = "query-fact"
	reteGraphTerminalProjectionQueryField    reteGraphTerminalProjectionKind = "query-field"
	reteGraphTerminalProjectionQueryValue    reteGraphTerminalProjectionKind = "query-value"
	reteGraphTerminalProjectionQueryConst    reteGraphTerminalProjectionKind = "query-const"
	reteGraphTerminalProjectionGeneric       reteGraphTerminalProjectionKind = "generic"
)

type reteGraphTerminalProjectionInspection struct {
	Kind        reteGraphTerminalProjectionKind
	Alias       string
	ActionName  string
	BindingSlot int
	Field       string
	Path        PathSpec
}

type reteGraphConditionOrderInspection struct {
	Order       int
	ConditionID ConditionID
	Binding     string
	BindingSlot int
	Path        []int
	Visible     bool
	Negated     bool
	Explicit    bool
	Aggregate   bool
	Test        bool
	Target      conditionTarget
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
	target       reteGraphTargetKey
	constraints  string
	listPatterns string
	predicates   string
}

type reteGraphTargetKey struct {
	kind        conditionTargetKind
	name        string
	templateKey TemplateKey
}

type reteGraphBetaKey struct {
	kind               reteGraphBetaNodeKind
	left               reteGraphStageRef
	right              reteGraphStageRef
	joins              string
	predicates         string
	rightPredicates    string
	rightHasLeftPrefix bool
	rightPrefixWidth   int
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

func compileReteGraph(compiledRules []compiledRule, compiledQueries []compiledQuery, templatesByKey map[TemplateKey]Template) *reteGraph {
	graph := &reteGraph{
		routesByTemplateKey: make(map[TemplateKey][]reteGraphAlphaNodeID),
		routesByName:        make(map[string][]reteGraphAlphaNodeID),
		alphaRouteTables:    make(map[TemplateKey]*reteGraphAlphaRouteTable),
		successorsByStage:   make(map[reteGraphStageRef][]reteGraphStageSuccessor),
		aggregatesByStage:   make(map[reteGraphStageRef][]reteGraphAggregateNodeID),
		aggregateOuters:     make(map[reteGraphStageRef][]reteGraphAggregateNodeID),
		terminalsByStage:    make(map[reteGraphStageRef][]reteGraphTerminalRoute),
		queryTerminalIDs:    make(map[string][]reteGraphTerminalNodeID),
	}
	if len(compiledRules) == 0 && len(compiledQueries) == 0 {
		return graph
	}

	alphaIndex := make(map[reteGraphAlphaKey]reteGraphAlphaNodeID, len(compiledRules))
	betaIndex := make(map[reteGraphBetaKey]reteGraphBetaNodeID, len(compiledRules))

	for _, rule := range compiledRules {
		var terminalID reteGraphTerminalNodeID
		for _, branch := range rule.executionConditionBranches() {
			if branchContainsAggregate(branch) {
				if graph.compileHigherOrderBranch(rule, branch, alphaIndex, betaIndex, &terminalID, templatesByKey) {
					continue
				}
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
				if condition.isTest {
					if !haveStage {
						continue
					}
					betaID, _ := graph.internBetaNode(betaIndex, reteGraphBetaNodeFilter, current, reteGraphStageRef{}, nil, condition.testPredicates)
					graph.appendStageSuccessor(current, reteGraphStageSuccessor{
						betaNodeID: betaID,
						side:       reteGraphBetaInputLeft,
					})
					current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
					continue
				}
				alphaConstraints, alphaPredicates := graphAlphaConstraintsAndPredicates(condition.constraints, condition.predicates)
				alphaID, created := graph.internAlphaNode(alphaIndex, condition.target, alphaConstraints, condition.listPatterns, alphaPredicates)
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
						route = reteGraphAlphaRouteSelectorForConstraints(template, alphaConstraints)
						alphaNode.route = route
						alphaNode.configureGeneratedMatch(route)
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
				conditionEntry := graphTokenEntryForCondition(condition)
				var betaID reteGraphBetaNodeID
				outputStage := reteGraphStageRef{}
				if betaKind == reteGraphBetaNodeJoin {
					betaID, outputStage = graph.internJoinWithResidualFilter(betaIndex, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates), conditionEntry)
					graph.appendBackchainDemandPlan(betaID, condition, reteGraphBetaInputRight, condition.joins, templatesByKey)
					if current.kind == reteGraphStageAlpha && conditionIndex > 0 {
						graph.appendBackchainDemandPlan(betaID, plans[conditionIndex-1], reteGraphBetaInputLeft, condition.joins, templatesByKey)
					}
				} else {
					betaID, _ = graph.internBetaNode(betaIndex, betaKind, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates))
					outputStage = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
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
					entry:      conditionEntry,
				})
				current = outputStage
			}

			if !haveStage {
				continue
			}
			if terminalID == 0 {
				graph.terminalNodes = append(graph.terminalNodes, reteGraphTerminalNode{
					id:             reteGraphTerminalNodeID(len(graph.terminalNodes) + 1),
					kind:           reteGraphTerminalRule,
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
			graph.branchInspections = append(graph.branchInspections, inspectReteGraphRuleBranch(rule, branch, terminalID))
		}
	}

	graph.compileQueryTerminals(compiledQueries, alphaIndex, betaIndex, templatesByKey)
	graph.compileGeneratedAlphaOps()
	return graph
}

func (g *reteGraph) compileGeneratedAlphaOps() {
	if g == nil {
		return
	}
	for i := range g.alphaNodes {
		node := &g.alphaNodes[i]
		sourceEntry := node.entry
		edges := node.edges
		total := len(edges.terminals) + len(edges.successors) + len(edges.aggregateOuters) + len(edges.aggregateInputs)
		if total == 0 {
			node.generatedOps = nil
			continue
		}
		ops := make([]reteGraphGeneratedAlphaOp, 0, total)
		for _, terminal := range edges.terminals {
			entry := terminal.entry
			if entry.conditionID == "" {
				entry = sourceEntry
			}
			ops = append(ops, reteGraphGeneratedAlphaOp{
				kind:       reteGraphGeneratedAlphaOpTerminal,
				entry:      cloneBindingTupleEntry(entry),
				terminalID: terminal.terminalID,
				branchID:   terminal.branchID,
			})
		}
		for _, successor := range edges.successors {
			betaNode := g.betaNode(successor.betaNodeID)
			if betaNode == nil {
				continue
			}
			switch successor.side {
			case reteGraphBetaInputLeft:
				entry := successor.entry
				if entry.conditionID == "" {
					entry = sourceEntry
				}
				ops = append(ops, reteGraphGeneratedAlphaOp{
					kind:       reteGraphGeneratedAlphaOpBetaLeft,
					entry:      cloneBindingTupleEntry(entry),
					betaEntry:  cloneBindingTupleEntry(betaNode.entry),
					betaNodeID: successor.betaNodeID,
					side:       successor.side,
				})
			case reteGraphBetaInputRight:
				ops = append(ops, reteGraphGeneratedAlphaOp{
					kind:       reteGraphGeneratedAlphaOpBetaRight,
					entry:      cloneBindingTupleEntry(successor.entry),
					betaEntry:  cloneBindingTupleEntry(betaNode.entry),
					betaNodeID: successor.betaNodeID,
					side:       successor.side,
				})
			}
		}
		for _, aggregateID := range edges.aggregateOuters {
			ops = append(ops, reteGraphGeneratedAlphaOp{
				kind:        reteGraphGeneratedAlphaOpAggregateOuter,
				entry:       cloneBindingTupleEntry(sourceEntry),
				aggregateID: aggregateID,
			})
		}
		for _, aggregateID := range edges.aggregateInputs {
			ops = append(ops, reteGraphGeneratedAlphaOp{
				kind:        reteGraphGeneratedAlphaOpAggregateInput,
				aggregateID: aggregateID,
			})
		}
		node.generatedOps = ops
	}
}

func (g *reteGraph) compileQueryTerminals(compiledQueries []compiledQuery, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template) {
	if g == nil {
		return
	}
	for _, query := range compiledQueries {
		for _, branch := range query.graphConditionBranches {
			if branchContainsAggregate(branch) {
				g.compileQueryAggregateBranch(query, branch, alphaIndex, betaIndex, templatesByKey)
				continue
			}
			current, ok := g.compileConditionBranchStages(RuleRevisionID("query:"+query.name), branch, alphaIndex, betaIndex, templatesByKey)
			if !ok {
				continue
			}
			terminalID := reteGraphTerminalNodeID(len(g.terminalNodes) + 1)
			g.terminalNodes = append(g.terminalNodes, reteGraphTerminalNode{
				id:        terminalID,
				kind:      reteGraphTerminalQuery,
				queryName: query.name,
				input:     current,
			})
			g.queryTerminalIDs[query.name] = append(g.queryTerminalIDs[query.name], terminalID)
			g.appendTerminal(current, reteGraphTerminalRoute{
				terminalID: terminalID,
				branchID:   branch.id,
			})
			g.branchInspections = append(g.branchInspections, inspectReteGraphQueryBranch(query, branch, terminalID))
		}
	}
}

func (g *reteGraph) compileConditionBranchStages(owner RuleRevisionID, branch compiledConditionBranch, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template) (reteGraphStageRef, bool) {
	if g == nil || len(branch.plans) == 0 {
		return reteGraphStageRef{}, false
	}
	var current reteGraphStageRef
	haveStage := false
	for conditionIndex, condition := range branch.plans {
		if condition.isTest {
			if !haveStage {
				return reteGraphStageRef{}, false
			}
			betaID, _ := g.internBetaNode(betaIndex, reteGraphBetaNodeFilter, current, reteGraphStageRef{}, nil, condition.testPredicates)
			g.appendStageSuccessor(current, reteGraphStageSuccessor{
				betaNodeID: betaID,
				side:       reteGraphBetaInputLeft,
			})
			current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
			continue
		}
		alphaID := g.compileConditionAlphaForOwner(owner, condition, conditionIndex, alphaIndex, templatesByKey, true)
		alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
		if alphaID == 0 {
			return reteGraphStageRef{}, false
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
		conditionEntry := graphTokenEntryForCondition(condition)
		var betaID reteGraphBetaNodeID
		outputStage := reteGraphStageRef{}
		if betaKind == reteGraphBetaNodeJoin {
			betaID, outputStage = g.internJoinWithResidualFilter(betaIndex, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates), conditionEntry)
			g.appendBackchainDemandPlan(betaID, condition, reteGraphBetaInputRight, condition.joins, templatesByKey)
			if current.kind == reteGraphStageAlpha && conditionIndex > 0 {
				g.appendBackchainDemandPlan(betaID, branch.plans[conditionIndex-1], reteGraphBetaInputLeft, condition.joins, templatesByKey)
			}
		} else {
			betaID, _ = g.internBetaNode(betaIndex, betaKind, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates))
			outputStage = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
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
			entry:      conditionEntry,
		})
		current = outputStage
	}
	return current, haveStage
}

func (g *reteGraph) compileHigherOrderBranch(rule compiledRule, branch compiledConditionBranch, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, terminalID *reteGraphTerminalNodeID, templatesByKey map[TemplateKey]Template) bool {
	if g == nil || len(branch.plans) == 0 {
		return false
	}
	higherOrderIndex := -1
	for i, plan := range branch.plans {
		if plan.aggregate == nil {
			continue
		}
		if plan.aggregate.higherOrder == conditionHigherOrderUnknown || higherOrderIndex >= 0 {
			return false
		}
		higherOrderIndex = i
	}
	if higherOrderIndex < 0 {
		return false
	}
	condition := branch.plans[higherOrderIndex]
	if !reteGraphSupportsAggregateCondition(condition, true) {
		return false
	}

	outer := reteGraphStageRef{kind: reteGraphStageRoot}
	outerEntry := bindingTupleEntry{}
	if higherOrderIndex > 0 {
		var ok bool
		outer, outerEntry, ok = g.compilePlanSequence(rule.revisionID, branch.plans[:higherOrderIndex], reteGraphStageRef{}, bindingTupleEntry{}, false, 0, alphaIndex, betaIndex, templatesByKey, true)
		if !ok || outer.kind == reteGraphStageUnknown {
			return false
		}
	}

	var current reteGraphStageRef
	switch condition.aggregate.higherOrder {
	case conditionHigherOrderExists:
		inner, ok := g.compileExistsAbsenceStage(rule.revisionID, condition.aggregate.inputPlans, outer, outerEntry, higherOrderIndex, alphaIndex, betaIndex, templatesByKey)
		if !ok {
			contributor, _, ok := g.compilePlanSequence(rule.revisionID, condition.aggregate.inputPlans, outer, outerEntry, true, higherOrderIndex, alphaIndex, betaIndex, templatesByKey, false)
			if !ok || contributor.kind == reteGraphStageUnknown {
				return false
			}
			innerID, _ := g.internBetaNodeWithRightPrefix(betaIndex, reteGraphBetaNodeNot, outer, contributor, nil, nil)
			g.appendStageSuccessor(outer, reteGraphStageSuccessor{
				betaNodeID: innerID,
				side:       reteGraphBetaInputLeft,
				entry:      outerEntry,
			})
			g.appendStageSuccessor(contributor, reteGraphStageSuccessor{
				betaNodeID: innerID,
				side:       reteGraphBetaInputRight,
			})
			inner = reteGraphStageRef{kind: reteGraphStageBeta, id: int(innerID)}
		}
		outerID, _ := g.internBetaNodeWithRightPrefix(betaIndex, reteGraphBetaNodeNot, outer, inner, nil, nil)
		g.appendStageSuccessor(outer, reteGraphStageSuccessor{
			betaNodeID: outerID,
			side:       reteGraphBetaInputLeft,
			entry:      outerEntry,
		})
		g.appendStageSuccessor(inner, reteGraphStageSuccessor{
			betaNodeID: outerID,
			side:       reteGraphBetaInputRight,
		})
		current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(outerID)}
	case conditionHigherOrderForall:
		stage, ok := g.compileForallCounterexampleNegationStage(rule.revisionID, condition.aggregate.inputPlans, outer, outerEntry, higherOrderIndex, alphaIndex, betaIndex, templatesByKey)
		if !ok {
			counterexample, _, ok := g.compilePlanSequence(rule.revisionID, condition.aggregate.inputPlans, outer, outerEntry, true, higherOrderIndex, alphaIndex, betaIndex, templatesByKey, false)
			if !ok || counterexample.kind == reteGraphStageUnknown {
				return false
			}
			outerID, _ := g.internBetaNodeWithRightPrefix(betaIndex, reteGraphBetaNodeNot, outer, counterexample, nil, nil)
			g.appendStageSuccessor(outer, reteGraphStageSuccessor{
				betaNodeID: outerID,
				side:       reteGraphBetaInputLeft,
				entry:      outerEntry,
			})
			g.appendStageSuccessor(counterexample, reteGraphStageSuccessor{
				betaNodeID: outerID,
				side:       reteGraphBetaInputRight,
			})
			stage = reteGraphStageRef{kind: reteGraphStageBeta, id: int(outerID)}
		}
		current = stage
	default:
		return false
	}

	if higherOrderIndex+1 < len(branch.plans) {
		var ok bool
		current, _, ok = g.compilePlanSequence(rule.revisionID, branch.plans[higherOrderIndex+1:], current, bindingTupleEntry{}, true, higherOrderIndex+1, alphaIndex, betaIndex, templatesByKey, true)
		if !ok {
			return false
		}
	}

	if terminalID != nil && *terminalID == 0 {
		g.terminalNodes = append(g.terminalNodes, reteGraphTerminalNode{
			id:             reteGraphTerminalNodeID(len(g.terminalNodes) + 1),
			kind:           reteGraphTerminalRule,
			ruleRevisionID: rule.revisionID,
			input:          current,
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
	g.appendTerminal(current, reteGraphTerminalRoute{
		terminalID: *terminalID,
		branchID:   branch.id,
	})
	g.branchInspections = append(g.branchInspections, inspectReteGraphRuleBranch(rule, branch, *terminalID))
	return true
}

func (g *reteGraph) compileExistsAbsenceStage(owner RuleRevisionID, inputPlans []compiledConditionPlan, outer reteGraphStageRef, outerEntry bindingTupleEntry, conditionOffset int, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template) (reteGraphStageRef, bool) {
	if g == nil || len(inputPlans) != 1 || outer.kind == reteGraphStageUnknown {
		return reteGraphStageRef{}, false
	}
	input := inputPlans[0]
	if input.aggregate != nil || input.isTest || input.negated {
		return reteGraphStageRef{}, false
	}
	alphaID := g.compileConditionAlphaForOwner(owner, input, conditionOffset, alphaIndex, templatesByKey, false)
	if alphaID == 0 {
		return reteGraphStageRef{}, false
	}
	alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
	innerID, _ := g.internBetaNode(betaIndex, reteGraphBetaNodeNot, outer, alphaRef, input.joins, betaResidualExpressionPredicates(input.predicates))
	g.appendStageSuccessor(outer, reteGraphStageSuccessor{
		betaNodeID: innerID,
		side:       reteGraphBetaInputLeft,
		entry:      outerEntry,
	})
	g.appendStageSuccessor(alphaRef, reteGraphStageSuccessor{
		betaNodeID: innerID,
		side:       reteGraphBetaInputRight,
		entry:      graphTokenEntryForCondition(input),
	})
	return reteGraphStageRef{kind: reteGraphStageBeta, id: int(innerID)}, true
}

func (g *reteGraph) compileForallCounterexampleNegationStage(owner RuleRevisionID, inputPlans []compiledConditionPlan, outer reteGraphStageRef, outerEntry bindingTupleEntry, conditionOffset int, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template) (reteGraphStageRef, bool) {
	if g == nil || len(inputPlans) < 2 || outer.kind == reteGraphStageUnknown {
		return reteGraphStageRef{}, false
	}
	domain := inputPlans[0]
	if domain.aggregate != nil || domain.isTest || domain.negated {
		return reteGraphStageRef{}, false
	}
	rightPredicates := make([]compiledExpressionPredicate, 0)
	for _, plan := range inputPlans[1:] {
		if plan.aggregate != nil || !plan.isTest || len(plan.testPredicates) == 0 {
			return reteGraphStageRef{}, false
		}
		rightPredicates = append(rightPredicates, plan.testPredicates...)
	}
	if len(rightPredicates) == 0 {
		return reteGraphStageRef{}, false
	}
	alphaID := g.compileConditionAlphaForOwner(owner, domain, conditionOffset, alphaIndex, templatesByKey, false)
	if alphaID == 0 {
		return reteGraphStageRef{}, false
	}
	alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
	outerID, _ := g.internBetaNodeWithRightPredicates(betaIndex, reteGraphBetaNodeNot, outer, alphaRef, domain.joins, betaResidualExpressionPredicates(domain.predicates), rightPredicates)
	g.appendStageSuccessor(outer, reteGraphStageSuccessor{
		betaNodeID: outerID,
		side:       reteGraphBetaInputLeft,
		entry:      outerEntry,
	})
	g.appendStageSuccessor(alphaRef, reteGraphStageSuccessor{
		betaNodeID: outerID,
		side:       reteGraphBetaInputRight,
		entry:      graphTokenEntryForCondition(domain),
	})
	return reteGraphStageRef{kind: reteGraphStageBeta, id: int(outerID)}, true
}

func (g *reteGraph) compilePlanSequence(owner RuleRevisionID, plans []compiledConditionPlan, start reteGraphStageRef, startEntry bindingTupleEntry, haveStage bool, conditionOffset int, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template, appendConsumer bool) (reteGraphStageRef, bindingTupleEntry, bool) {
	if g == nil {
		return reteGraphStageRef{}, bindingTupleEntry{}, false
	}
	current := start
	currentEntry := startEntry
	for i, condition := range plans {
		conditionIndex := conditionOffset + i
		if condition.aggregate != nil {
			return reteGraphStageRef{}, bindingTupleEntry{}, false
		}
		if condition.isTest {
			if !haveStage {
				return reteGraphStageRef{}, bindingTupleEntry{}, false
			}
			betaID, _ := g.internBetaNode(betaIndex, reteGraphBetaNodeFilter, current, reteGraphStageRef{}, nil, condition.testPredicates)
			g.appendStageSuccessor(current, reteGraphStageSuccessor{
				betaNodeID: betaID,
				side:       reteGraphBetaInputLeft,
				entry:      currentEntry,
			})
			current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
			currentEntry = bindingTupleEntry{}
			continue
		}
		alphaID := g.compileConditionAlphaForOwner(owner, condition, conditionIndex, alphaIndex, templatesByKey, appendConsumer)
		if alphaID == 0 {
			return reteGraphStageRef{}, bindingTupleEntry{}, false
		}
		alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
		conditionEntry := graphTokenEntryForCondition(condition)
		if !haveStage {
			current = alphaRef
			currentEntry = conditionEntry
			haveStage = true
			continue
		}
		betaKind := reteGraphBetaNodeJoin
		if condition.negated {
			betaKind = reteGraphBetaNodeNot
		}
		var betaID reteGraphBetaNodeID
		outputStage := reteGraphStageRef{}
		if betaKind == reteGraphBetaNodeJoin {
			betaID, outputStage = g.internJoinWithResidualFilter(betaIndex, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates), conditionEntry)
		} else {
			betaID, _ = g.internBetaNode(betaIndex, betaKind, current, alphaRef, condition.joins, betaResidualExpressionPredicates(condition.predicates))
			outputStage = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
		}
		leftEntry := bindingTupleEntry{}
		if current.kind == reteGraphStageAlpha {
			leftEntry = currentEntry
		}
		g.appendStageSuccessor(current, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputLeft,
			entry:      leftEntry,
		})
		g.appendStageSuccessor(alphaRef, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputRight,
			entry:      conditionEntry,
		})
		current = outputStage
		currentEntry = bindingTupleEntry{}
	}
	return current, currentEntry, haveStage
}

func (g *reteGraph) compileAggregateBranchStages(owner RuleRevisionID, branch compiledConditionBranch, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template, aggregateEntries func(compiledConditionPlan) []bindingTupleEntry) (reteGraphStageRef, bool) {
	if g == nil || len(branch.plans) == 0 {
		return reteGraphStageRef{}, false
	}
	aggregateIndex := -1
	for i, plan := range branch.plans {
		if plan.aggregate == nil {
			continue
		}
		if aggregateIndex >= 0 {
			return reteGraphStageRef{}, false
		}
		aggregateIndex = i
	}
	if aggregateIndex < 0 {
		return reteGraphStageRef{}, false
	}
	condition := branch.plans[aggregateIndex]
	if condition.aggregate.higherOrder != conditionHigherOrderUnknown {
		return reteGraphStageRef{}, false
	}
	if !reteGraphSupportsAggregateCondition(condition, aggregateIndex > 0) {
		return reteGraphStageRef{}, false
	}

	var current reteGraphStageRef
	haveStage := false
	for conditionIndex := 0; conditionIndex < aggregateIndex; conditionIndex++ {
		outer := branch.plans[conditionIndex]
		if outer.aggregate != nil {
			return reteGraphStageRef{}, false
		}
		alphaID := g.compileConditionAlphaForOwner(owner, outer, conditionIndex, alphaIndex, templatesByKey, true)
		alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
		if alphaID == 0 {
			return reteGraphStageRef{}, false
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
		outerEntry := graphTokenEntryForCondition(outer)
		var betaID reteGraphBetaNodeID
		outputStage := reteGraphStageRef{}
		if betaKind == reteGraphBetaNodeJoin {
			betaID, outputStage = g.internJoinWithResidualFilter(betaIndex, current, alphaRef, outer.joins, betaResidualExpressionPredicates(outer.predicates), outerEntry)
		} else {
			betaID, _ = g.internBetaNode(betaIndex, betaKind, current, alphaRef, outer.joins, betaResidualExpressionPredicates(outer.predicates))
			outputStage = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
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
			entry:      outerEntry,
		})
		current = outputStage
	}

	outerStage := reteGraphStageRef{}
	if haveStage {
		outerStage = current
	}
	aggregateInput := reteGraphStageRef{}
	inputEntry := bindingTupleEntry{}
	for inputIndex, input := range condition.aggregate.inputPlans {
		if input.isTest {
			if !haveStage {
				return reteGraphStageRef{}, false
			}
			betaID, _ := g.internBetaNode(betaIndex, reteGraphBetaNodeFilter, current, reteGraphStageRef{}, nil, input.testPredicates)
			g.appendStageSuccessor(current, reteGraphStageSuccessor{
				betaNodeID: betaID,
				side:       reteGraphBetaInputLeft,
			})
			current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
			aggregateInput = current
			continue
		}
		inputAlphaID := g.compileConditionAlphaForOwner(owner, input, aggregateIndex+inputIndex, alphaIndex, templatesByKey, false)
		inputAlphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(inputAlphaID)}
		if inputAlphaID == 0 {
			return reteGraphStageRef{}, false
		}
		inputEntry = graphTokenEntryForCondition(input)
		if !haveStage {
			current = inputAlphaRef
			aggregateInput = inputAlphaRef
			haveStage = true
			continue
		}
		inputEntry := graphTokenEntryForCondition(input)
		betaID, outputStage := g.internJoinWithResidualFilter(betaIndex, current, inputAlphaRef, input.joins, betaResidualExpressionPredicates(input.predicates), inputEntry)
		leftEntry := bindingTupleEntry{}
		if current.kind == reteGraphStageAlpha {
			if inputIndex == 0 && aggregateIndex > 0 {
				leftEntry = graphTokenEntryForCondition(branch.plans[aggregateIndex-1])
			} else if inputIndex > 0 {
				leftEntry = graphTokenEntryForCondition(condition.aggregate.inputPlans[inputIndex-1])
			}
		}
		g.appendStageSuccessor(current, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputLeft,
			entry:      leftEntry,
		})
		g.appendStageSuccessor(inputAlphaRef, reteGraphStageSuccessor{
			betaNodeID: betaID,
			side:       reteGraphBetaInputRight,
			entry:      inputEntry,
		})
		current = outputStage
		aggregateInput = current
	}
	if aggregateInput.kind == reteGraphStageUnknown || inputEntry.conditionID == "" {
		return reteGraphStageRef{}, false
	}

	outer := reteGraphStageRef{}
	if aggregateIndex > 0 {
		outer = outerStage
	}
	entries := graphTokenEntriesForAggregateBranch(branch, condition)
	if aggregateEntries != nil {
		entries = aggregateEntries(condition)
	}
	aggregateID := g.appendAggregate(aggregateInput, outer, inputEntry, condition.id, condition.bindingSlot, condition.aggregate.specs, entries)
	aggregateRef := reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregateID)}
	current = aggregateRef
	haveStage = true

	for conditionIndex := aggregateIndex + 1; conditionIndex < len(branch.plans); conditionIndex++ {
		later := branch.plans[conditionIndex]
		if later.aggregate != nil {
			return reteGraphStageRef{}, false
		}
		alphaID := g.compileConditionAlphaForOwner(owner, later, conditionIndex, alphaIndex, templatesByKey, true)
		alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
		if alphaID == 0 {
			return reteGraphStageRef{}, false
		}
		betaKind := reteGraphBetaNodeJoin
		if later.negated {
			betaKind = reteGraphBetaNodeNot
		}
		laterEntry := graphTokenEntryForCondition(later)
		var betaID reteGraphBetaNodeID
		outputStage := reteGraphStageRef{}
		if betaKind == reteGraphBetaNodeJoin {
			betaID, outputStage = g.internJoinWithResidualFilter(betaIndex, current, alphaRef, later.joins, betaResidualExpressionPredicates(later.predicates), laterEntry)
		} else {
			betaID, _ = g.internBetaNode(betaIndex, betaKind, current, alphaRef, later.joins, betaResidualExpressionPredicates(later.predicates))
			outputStage = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
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
			entry:      laterEntry,
		})
		current = outputStage
	}

	return current, haveStage
}

func (g *reteGraph) compileAggregateBranch(rule compiledRule, branch compiledConditionBranch, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, terminalID *reteGraphTerminalNodeID, templatesByKey map[TemplateKey]Template) bool {
	current, ok := g.compileAggregateBranchStages(rule.revisionID, branch, alphaIndex, betaIndex, templatesByKey, func(condition compiledConditionPlan) []bindingTupleEntry {
		return graphTokenEntriesForAggregateBindings(rule, condition)
	})
	if !ok {
		return false
	}
	if terminalID != nil && *terminalID == 0 {
		g.terminalNodes = append(g.terminalNodes, reteGraphTerminalNode{
			id:             reteGraphTerminalNodeID(len(g.terminalNodes) + 1),
			kind:           reteGraphTerminalRule,
			ruleRevisionID: rule.revisionID,
			input:          current,
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
	g.appendTerminal(current, reteGraphTerminalRoute{
		terminalID: *terminalID,
		branchID:   branch.id,
	})
	g.branchInspections = append(g.branchInspections, inspectReteGraphRuleBranch(rule, branch, *terminalID))
	return true
}

func (g *reteGraph) compileQueryAggregateBranch(query compiledQuery, branch compiledConditionBranch, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, betaIndex map[reteGraphBetaKey]reteGraphBetaNodeID, templatesByKey map[TemplateKey]Template) bool {
	current, ok := g.compileAggregateBranchStages(RuleRevisionID("query:"+query.name), branch, alphaIndex, betaIndex, templatesByKey, func(condition compiledConditionPlan) []bindingTupleEntry {
		return graphTokenEntriesForAggregateBranch(branch, condition)
	})
	if !ok {
		return false
	}
	terminalID := reteGraphTerminalNodeID(len(g.terminalNodes) + 1)
	g.terminalNodes = append(g.terminalNodes, reteGraphTerminalNode{
		id:        terminalID,
		kind:      reteGraphTerminalQuery,
		queryName: query.name,
		input:     current,
	})
	g.queryTerminalIDs[query.name] = append(g.queryTerminalIDs[query.name], terminalID)
	g.appendTerminal(current, reteGraphTerminalRoute{
		terminalID: terminalID,
		branchID:   branch.id,
	})
	g.branchInspections = append(g.branchInspections, inspectReteGraphQueryBranch(query, branch, terminalID))
	return true
}

func (g *reteGraph) compileConditionAlpha(rule compiledRule, condition compiledConditionPlan, conditionIndex int, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, templatesByKey map[TemplateKey]Template, appendConsumer bool) reteGraphAlphaNodeID {
	return g.compileConditionAlphaForOwner(rule.revisionID, condition, conditionIndex, alphaIndex, templatesByKey, appendConsumer)
}

func (g *reteGraph) compileConditionAlphaForOwner(owner RuleRevisionID, condition compiledConditionPlan, conditionIndex int, alphaIndex map[reteGraphAlphaKey]reteGraphAlphaNodeID, templatesByKey map[TemplateKey]Template, appendConsumer bool) reteGraphAlphaNodeID {
	alphaConstraints, alphaPredicates := graphAlphaConstraintsAndPredicates(condition.constraints, condition.predicates)
	alphaID, created := g.internAlphaNode(alphaIndex, condition.target, alphaConstraints, condition.listPatterns, alphaPredicates)
	if alphaNode := g.alphaNode(alphaID); alphaNode != nil && alphaNode.entry.conditionID == "" && conditionIndex == 0 {
		alphaNode.entry = graphTokenEntryForCondition(condition)
	}
	supportedAlpha := reteGraphSupportsAlpha(condition.target, templatesByKey)
	if appendConsumer && supportedAlpha {
		g.appendAlphaConsumer(alphaID, reteBetaConditionRoute{
			ruleRevisionID: owner,
			conditionIndex: conditionIndex,
			conditionID:    condition.id,
			bindingSlot:    condition.bindingSlot,
		})
	}
	if created && supportedAlpha {
		route := reteGraphAlphaRouteSelector{}
		if alphaNode := g.alphaNode(alphaID); alphaNode != nil {
			template := templatesByKey[condition.target.templateKey]
			route = reteGraphAlphaRouteSelectorForConstraints(template, alphaConstraints)
			alphaNode.route = route
			alphaNode.configureGeneratedMatch(route)
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
	if condition.aggregate == nil || len(condition.aggregate.inputPlans) == 0 || len(condition.aggregate.specs) == 0 {
		return false
	}
	for inputIndex, input := range condition.aggregate.inputPlans {
		if input.aggregate != nil {
			return false
		}
		if input.negated && condition.aggregate.higherOrder == conditionHigherOrderUnknown {
			return false
		}
		if input.isTest {
			if inputIndex == 0 || len(input.testPredicates) == 0 {
				return false
			}
			continue
		}
		if len(betaResidualExpressionPredicates(input.predicates)) != 0 {
			return false
		}
		if !allowInputJoins && inputIndex == 0 && len(input.joins) != 0 {
			return false
		}
	}
	for _, spec := range condition.aggregate.specs {
		switch spec.kind {
		case AggregateCount, AggregateSum, AggregateMin, AggregateMax, AggregateCollect:
		case aggregateExists, aggregateForall:
			if condition.aggregate.higherOrder == conditionHigherOrderUnknown {
				return false
			}
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

func (g *reteGraph) internAlphaNode(index map[reteGraphAlphaKey]reteGraphAlphaNodeID, target conditionTarget, constraints []compiledFieldConstraint, listPatterns []compiledListPattern, predicates []compiledExpressionPredicate) (reteGraphAlphaNodeID, bool) {
	if g == nil {
		return 0, false
	}
	key := reteGraphAlphaKey{
		target: reteGraphTargetKey{
			kind:        target.kind,
			name:        target.name,
			templateKey: target.templateKey,
		},
		constraints:  serializeCompiledFieldConstraints(constraints),
		listPatterns: serializeCompiledListPatterns(listPatterns),
		predicates:   serializeCompiledExpressionPredicates(predicates),
	}
	if id, ok := index[key]; ok {
		return id, false
	}

	id := reteGraphAlphaNodeID(len(g.alphaNodes) + 1)
	g.alphaNodes = append(g.alphaNodes, reteGraphAlphaNode{
		id:           id,
		target:       target,
		constraints:  cloneCompiledFieldConstraints(constraints),
		listPatterns: cloneCompiledListPatterns(listPatterns),
		predicates:   cloneCompiledExpressionPredicates(predicates),
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
	case reteGraphStageRoot:
		return 0
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
		if node.kind == reteGraphBetaNodeNot || node.kind == reteGraphBetaNodeFilter || node.kind == reteGraphBetaNodeResidualFilter {
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
	g.appendStageAggregateInput(input, id)
	if outer.kind != reteGraphStageUnknown {
		g.aggregateOuters[outer] = append(g.aggregateOuters[outer], id)
		g.appendStageAggregateOuter(outer, id)
	}
	return id
}

func (g *reteGraph) appendStageSuccessor(source reteGraphStageRef, successor reteGraphStageSuccessor) {
	if g == nil || source.kind == reteGraphStageUnknown || successor.betaNodeID <= 0 {
		return
	}
	g.successorsByStage[source] = append(g.successorsByStage[source], successor)
	edges := g.stageEdges(source)
	if edges != nil {
		edges.successors = append(edges.successors, successor)
	}
}

func (g *reteGraph) appendTerminal(source reteGraphStageRef, terminal reteGraphTerminalRoute) {
	if g == nil || source.kind == reteGraphStageUnknown || terminal.terminalID <= 0 {
		return
	}
	if index := int(terminal.terminalID) - 1; index >= 0 && index < len(g.terminalNodes) {
		node := &g.terminalNodes[index]
		if node.branchCount == 0 {
			node.branchCount = 1
			node.singleBranchID = terminal.branchID
		} else if node.singleBranchID != terminal.branchID {
			node.branchCount++
		}
	}
	g.terminalsByStage[source] = append(g.terminalsByStage[source], terminal)
	edges := g.stageEdges(source)
	if edges != nil {
		edges.terminals = append(edges.terminals, terminal)
	}
}

func (g *reteGraph) appendStageAggregateInput(source reteGraphStageRef, id reteGraphAggregateNodeID) {
	if g == nil || source.kind == reteGraphStageUnknown || id <= 0 {
		return
	}
	edges := g.stageEdges(source)
	if edges != nil {
		edges.aggregateInputs = append(edges.aggregateInputs, id)
	}
}

func (g *reteGraph) appendStageAggregateOuter(source reteGraphStageRef, id reteGraphAggregateNodeID) {
	if g == nil || source.kind == reteGraphStageUnknown || id <= 0 {
		return
	}
	edges := g.stageEdges(source)
	if edges != nil {
		edges.aggregateOuters = append(edges.aggregateOuters, id)
	}
}

func (g *reteGraph) stageEdges(source reteGraphStageRef) *reteGraphStageEdges {
	if g == nil {
		return nil
	}
	switch source.kind {
	case reteGraphStageAlpha:
		node := g.alphaNode(reteGraphAlphaNodeID(source.id))
		if node == nil {
			return nil
		}
		return &node.edges
	case reteGraphStageBeta:
		node := g.betaNode(reteGraphBetaNodeID(source.id))
		if node == nil {
			return nil
		}
		return &node.edges
	case reteGraphStageAggregate:
		node := g.aggregateNode(reteGraphAggregateNodeID(source.id))
		if node == nil {
			return nil
		}
		return &node.edges
	default:
		return nil
	}
}

func (g *reteGraph) stageSuccessors(source reteGraphStageRef) []reteGraphStageSuccessor {
	if edges := g.stageEdges(source); edges != nil {
		return edges.successors
	}
	return g.successorsByStage[source]
}

func (g *reteGraph) stageAggregateInputs(source reteGraphStageRef) []reteGraphAggregateNodeID {
	if edges := g.stageEdges(source); edges != nil {
		return edges.aggregateInputs
	}
	return g.aggregatesByStage[source]
}

func (g *reteGraph) stageAggregateOuters(source reteGraphStageRef) []reteGraphAggregateNodeID {
	if edges := g.stageEdges(source); edges != nil {
		return edges.aggregateOuters
	}
	return g.aggregateOuters[source]
}

func (g *reteGraph) stageTerminals(source reteGraphStageRef) []reteGraphTerminalRoute {
	if edges := g.stageEdges(source); edges != nil {
		return edges.terminals
	}
	return g.terminalsByStage[source]
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

func (g *reteGraph) alphaRoutesMayObserveModify(before, after FactSnapshot, summary factModifySummary) bool {
	if g == nil || summary.unknown || len(summary.changes) == 0 {
		return true
	}
	if before.TemplateKey() != after.TemplateKey() || before.Name() != after.Name() {
		return true
	}
	for _, id := range g.routesByTemplateKey[after.TemplateKey()] {
		node := g.alphaNode(id)
		if node == nil || node.mayObserveModify(summary) {
			return true
		}
	}
	for _, id := range g.routesByName[after.Name()] {
		node := g.alphaNode(id)
		if node == nil || node.mayObserveModify(summary) {
			return true
		}
	}
	return false
}

func (n reteGraphAlphaNode) mayObserveModify(summary factModifySummary) bool {
	if summary.unknown || len(summary.changes) == 0 {
		return true
	}
	if listPatternsMayObserveModify(n.listPatterns, summary) {
		return true
	}
	for _, constraint := range n.constraints {
		if summary.observesAccess(constraint.access) {
			return true
		}
	}
	for _, predicate := range n.predicates {
		if expressionMayObserveCurrentFactModify(predicate.expression, summary) {
			return true
		}
	}
	return false
}

func listPatternsMayObserveModify(patterns []compiledListPattern, summary factModifySummary) bool {
	for _, pattern := range patterns {
		if summary.observesAccess(pattern.path) {
			return true
		}
	}
	return false
}

func (s factModifySummary) observesAccess(access compiledPathAccess) bool {
	if s.unknown {
		return true
	}
	if access.rootSlot >= 0 {
		return s.hasChangedSlot(access.rootSlot)
	}
	if access.root == "" {
		return true
	}
	for _, change := range s.changes {
		if change.Field == access.root {
			return true
		}
	}
	return false
}

func reteGraphAlphaRouteSelectorForConstraints(template Template, constraints []compiledFieldConstraint) reteGraphAlphaRouteSelector {
	for _, constraint := range constraints {
		if constraint.operator != FieldConstraintOpEqual || constraint.access.rootSlot < 0 || !constraint.access.topLevel() {
			continue
		}
		value, ok := reteGraphAlphaRouteValueFromValue(constraint.value)
		if !ok {
			continue
		}
		if !reteGraphAlphaRouteFieldKindMatches(template, constraint.access.rootSlot, value.kind) {
			continue
		}
		return reteGraphAlphaRouteSelector{
			fieldSlot: constraint.access.rootSlot,
			value:     value,
			enabled:   true,
		}
	}
	return reteGraphAlphaRouteSelector{}
}

func (n *reteGraphAlphaNode) configureGeneratedMatch(route reteGraphAlphaRouteSelector) {
	if n == nil || len(n.listPatterns) != 0 || len(n.predicates) != 0 {
		return
	}
	if len(n.constraints) == 0 {
		n.generatedMatch = reteGraphAlphaGeneratedMatch{kind: reteGraphAlphaGeneratedMatchTargetOnly}
		return
	}
	equalities := make([]reteGraphAlphaGeneratedEquality, 0, len(n.constraints))
	for _, constraint := range n.constraints {
		if constraint.operator != FieldConstraintOpEqual || constraint.access.rootSlot < 0 || !constraint.access.topLevel() {
			return
		}
		value, ok := reteGraphAlphaRouteValueFromValue(constraint.value)
		if !ok {
			return
		}
		equalities = append(equalities, reteGraphAlphaGeneratedEquality{
			fieldSlot: constraint.access.rootSlot,
			value:     value,
		})
	}
	if len(equalities) == 0 {
		return
	}
	if route.enabled && len(equalities) == 1 && (equalities[0].fieldSlot != route.fieldSlot || equalities[0].value != route.value) {
		return
	}
	n.generatedMatch = reteGraphAlphaGeneratedMatch{
		kind:       reteGraphAlphaGeneratedMatchSlotEqual,
		equalities: equalities,
	}
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
	ok, _ := n.matchesSnapshotWithContextAndCounters(context.Background(), fact, nil)
	return ok
}

func (n reteGraphAlphaNode) matchesSnapshotWithCounters(fact FactSnapshot, span *propagationCounterSpan) bool {
	ok, _ := n.matchesSnapshotWithContextAndCounters(context.Background(), fact, span)
	return ok
}

func (n reteGraphAlphaNode) matchesSnapshotWithContextAndCounters(ctx context.Context, fact FactSnapshot, span *propagationCounterSpan) (bool, error) {
	switch n.target.kind {
	case conditionTargetTemplateKey:
		if fact.TemplateKey() != n.target.templateKey {
			return false, nil
		}
	case conditionTargetName:
		if fact.Name() != n.target.name {
			return false, nil
		}
	default:
		return false, nil
	}
	ref := newConditionFactRefFromSnapshot(fact)
	for _, constraint := range n.constraints {
		if !constraint.matchesWithCounters(ref, span) {
			return false, nil
		}
	}
	if !n.listPatternsMatch(ref, tokenRef{}) {
		return false, nil
	}
	ok, err := n.expressionPredicatesMatch(ctx, ref, span)
	if err != nil || !ok {
		return ok, err
	}
	return true, nil
}

func (n reteGraphAlphaNode) matchesWorking(fact *workingFact) bool {
	ok, _ := n.matchesWorkingWithContextAndCounters(context.Background(), fact, nil)
	return ok
}

func (n reteGraphAlphaNode) matchesWorkingWithCounters(fact *workingFact, span *propagationCounterSpan) bool {
	ok, _ := n.matchesWorkingWithContextAndCounters(context.Background(), fact, span)
	return ok
}

func (n reteGraphAlphaNode) matchesWorkingWithContextAndCounters(ctx context.Context, fact *workingFact, span *propagationCounterSpan) (bool, error) {
	if fact == nil {
		return false, nil
	}
	switch n.target.kind {
	case conditionTargetTemplateKey:
		if fact.templateKey != n.target.templateKey {
			return false, nil
		}
	case conditionTargetName:
		if fact.storedName() != n.target.name {
			return false, nil
		}
	default:
		return false, nil
	}
	for _, constraint := range n.constraints {
		if !constraint.matchesWorkingWithCounters(fact, span) {
			return false, nil
		}
	}
	ref := newConditionFactRefFromWorkingFact(fact)
	if !n.listPatternsMatch(ref, tokenRef{}) {
		return false, nil
	}
	ok, err := n.expressionPredicatesMatch(ctx, ref, span)
	if err != nil || !ok {
		return ok, err
	}
	return true, nil
}

func (n reteGraphAlphaNode) matchesGeneratedWorkingWithContextAndCounters(ctx context.Context, fact *workingFact, span *propagationCounterSpan) (bool, error) {
	if n.generatedMatch.kind != reteGraphAlphaGeneratedMatchNone {
		return n.generatedMatch.matchesWorking(n.target, fact), nil
	}
	return n.matchesWorkingWithContextAndCounters(ctx, fact, span)
}

func (m reteGraphAlphaGeneratedMatch) matchesWorking(target conditionTarget, fact *workingFact) bool {
	if fact == nil || !target.matchesWorkingFact(fact) {
		return false
	}
	switch m.kind {
	case reteGraphAlphaGeneratedMatchTargetOnly:
		return true
	case reteGraphAlphaGeneratedMatchSlotEqual:
		for _, equality := range m.equalities {
			value, ok := fact.compiledFieldValue("", equality.fieldSlot)
			if !ok || !equality.value.matchesValue(value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (target conditionTarget) matchesWorkingFact(fact *workingFact) bool {
	if fact == nil {
		return false
	}
	switch target.kind {
	case conditionTargetTemplateKey:
		return fact.templateKey == target.templateKey
	case conditionTargetName:
		return fact.storedName() == target.name
	default:
		return false
	}
}

func (v reteGraphAlphaRouteValue) matchesValue(value Value) bool {
	if !v.valid || value.Kind() != v.kind {
		return false
	}
	switch v.kind {
	case ValueBool:
		if value.boolValue {
			return v.bits == 1
		}
		return v.bits == 0
	case ValueInt:
		return value.intValue == v.bits
	case ValueString:
		return value.stringValue == v.text
	default:
		return false
	}
}

func (n reteGraphAlphaNode) listPatternsMatch(fact conditionFactRef, bindings tokenRef) bool {
	for _, pattern := range n.listPatterns {
		if ok, err := pattern.matchesFactOnly(fact, bindings); err != nil || !ok {
			return false
		}
	}
	return true
}

func (n reteGraphAlphaNode) listPatternCaptures(fact conditionFactRef, bindings tokenRef) ([]listPatternCapture, bool) {
	if len(n.listPatterns) == 0 {
		return nil, true
	}
	var out []listPatternCapture
	for _, pattern := range n.listPatterns {
		captures, ok, err := pattern.matchesFact(fact, bindings)
		if err != nil || !ok {
			return nil, false
		}
		out = append(out, captures...)
	}
	return out, true
}

func (n reteGraphAlphaNode) expressionPredicatesMatch(ctx context.Context, fact conditionFactRef, span *propagationCounterSpan) (bool, error) {
	for _, predicate := range n.predicates {
		if span != nil {
			span.recordExpressionPredicateTest()
		}
		ok, err := predicate.matchesWithContextParamsAndCounters(ctx, fact, nil, nil, span)
		if err != nil {
			if span != nil {
				span.recordExpressionPredicateError()
			}
			return false, err
		}
		if !ok {
			if span != nil {
				span.recordExpressionPredicateFailure()
			}
			return false, nil
		}
	}
	return true, nil
}

func (g *reteGraph) internBetaNode(index map[reteGraphBetaKey]reteGraphBetaNodeID, kind reteGraphBetaNodeKind, left, right reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate) (reteGraphBetaNodeID, bool) {
	return g.internBetaNodeInternal(index, kind, left, right, joins, predicates, nil, false, 0)
}

func (g *reteGraph) internBetaNodeWithRightPrefix(index map[reteGraphBetaKey]reteGraphBetaNodeID, kind reteGraphBetaNodeKind, left, right reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate) (reteGraphBetaNodeID, bool) {
	return g.internBetaNodeInternal(index, kind, left, right, joins, predicates, nil, true, g.stageTokenWidth(left))
}

func (g *reteGraph) internBetaNodeWithRightPredicates(index map[reteGraphBetaKey]reteGraphBetaNodeID, kind reteGraphBetaNodeKind, left, right reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate, rightPredicates []compiledExpressionPredicate) (reteGraphBetaNodeID, bool) {
	return g.internBetaNodeInternal(index, kind, left, right, joins, predicates, rightPredicates, false, 0)
}

func (g *reteGraph) internJoinWithResidualFilter(index map[reteGraphBetaKey]reteGraphBetaNodeID, left, right reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate, entry bindingTupleEntry) (reteGraphBetaNodeID, reteGraphStageRef) {
	hashJoins, residualJoins := planCompiledJoinConstraints(joins, predicates)
	if len(residualJoins) == 0 && len(predicates) == 0 {
		betaID, _ := g.internBetaNode(index, reteGraphBetaNodeJoin, left, right, joins, predicates)
		if betaNode := g.betaNode(betaID); betaNode != nil && betaNode.entry.conditionID == "" {
			betaNode.entry = entry
		}
		return betaID, reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
	}
	betaID, _ := g.internBetaNode(index, reteGraphBetaNodeJoin, left, right, hashJoins, nil)
	if betaNode := g.betaNode(betaID); betaNode != nil && betaNode.entry.conditionID == "" {
		betaNode.entry = entry
	}
	filterSource := reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
	filterID, created := g.internResidualFilterNode(index, filterSource, residualJoins, predicates)
	if created {
		g.appendStageSuccessor(filterSource, reteGraphStageSuccessor{
			betaNodeID: filterID,
			side:       reteGraphBetaInputLeft,
		})
	}
	return betaID, reteGraphStageRef{kind: reteGraphStageBeta, id: int(filterID)}
}

func (g *reteGraph) appendBackchainDemandPlan(betaID reteGraphBetaNodeID, condition compiledConditionPlan, side reteGraphBetaInputSide, joins []compiledJoinConstraint, templatesByKey map[TemplateKey]Template) {
	if g == nil || betaID == 0 || condition.explicit || condition.negated || condition.target.kind != conditionTargetTemplateKey {
		return
	}
	if side != reteGraphBetaInputLeft && side != reteGraphBetaInputRight {
		return
	}
	source, ok := templatesByKey[condition.target.templateKey]
	if !ok || !source.backchainReactive {
		return
	}
	demandKey, ok := source.BackchainDemandTemplateKey()
	if !ok {
		return
	}
	demandTemplate, ok := templatesByKey[demandKey]
	if !ok || !demandTemplate.backchainDemand || !demandTemplate.closed {
		return
	}
	node := g.betaNode(betaID)
	if node == nil || node.kind != reteGraphBetaNodeJoin {
		return
	}
	plan := reteGraphBackchainDemandPlan{
		templateKey:  demandKey,
		side:         side,
		slotCount:    len(demandTemplate.fields),
		defaultSlots: compileReteGraphBackchainDemandDefaultSlots(demandTemplate, condition.constraints),
		constraints:  cloneCompiledFieldConstraints(condition.constraints),
		joins:        cloneCompiledJoinConstraints(joins),
	}
	plan.constSlots = compileReteGraphBackchainDemandConstSlots(demandTemplate, condition.constraints)
	plan.joinSlots = compileReteGraphBackchainDemandJoinSlots(demandTemplate, side, joins)
	for _, existing := range node.backchainDemands {
		if existing.templateKey == plan.templateKey &&
			existing.side == plan.side &&
			serializeCompiledFieldConstraints(existing.constraints) == serializeCompiledFieldConstraints(plan.constraints) &&
			serializeCompiledJoinConstraints(existing.joins) == serializeCompiledJoinConstraints(plan.joins) {
			return
		}
	}
	node.backchainDemands = append(node.backchainDemands, plan)
}

func compileReteGraphBackchainDemandDefaultSlots(template Template, constraints []compiledFieldConstraint) []factSlot {
	if len(template.fields) == 0 {
		return nil
	}
	out := make([]factSlot, len(template.fields))
	for i := range out {
		out[i] = factSlot{
			value:    NullValue(),
			ok:       true,
			presence: fieldPresenceExplicit,
		}
	}
	for _, slot := range compileReteGraphBackchainDemandConstSlots(template, constraints) {
		out[slot.slot].value = slot.value
	}
	return out
}

func compileReteGraphBackchainDemandConstSlots(template Template, constraints []compiledFieldConstraint) []reteGraphBackchainDemandConstSlot {
	if len(constraints) == 0 || len(template.fields) == 0 {
		return nil
	}
	out := make([]reteGraphBackchainDemandConstSlot, 0, len(constraints))
	for _, constraint := range constraints {
		if constraint.operator != FieldConstraintOpEqual || !constraint.access.topLevel() {
			continue
		}
		slot := constraint.access.rootSlot
		if slot < 0 {
			var ok bool
			slot, ok = template.fieldSlot(constraint.access.root)
			if !ok {
				continue
			}
		}
		if slot < 0 || slot >= len(template.fields) {
			continue
		}
		out = append(out, reteGraphBackchainDemandConstSlot{
			slot:  slot,
			value: cloneValue(constraint.value),
		})
	}
	return out
}

func compileReteGraphBackchainDemandJoinSlots(template Template, side reteGraphBetaInputSide, joins []compiledJoinConstraint) []reteGraphBackchainDemandJoinSlot {
	if len(joins) == 0 || len(template.fields) == 0 {
		return nil
	}
	out := make([]reteGraphBackchainDemandJoinSlot, 0, len(joins))
	for _, join := range joins {
		if join.operator != FieldConstraintOpEqual {
			continue
		}
		switch side {
		case reteGraphBetaInputRight:
			if !join.access.topLevel() {
				continue
			}
			slot := join.access.rootSlot
			if slot < 0 {
				var ok bool
				slot, ok = template.fieldSlot(join.access.root)
				if !ok {
					continue
				}
			}
			if slot < 0 || slot >= len(template.fields) {
				continue
			}
			out = append(out, reteGraphBackchainDemandJoinSlot{
				slot:        slot,
				bindingSlot: join.refBindingSlot,
				access:      join.refAccess,
			})
		case reteGraphBetaInputLeft:
			if !join.refAccess.topLevel() {
				continue
			}
			slot := join.refAccess.rootSlot
			if slot < 0 {
				var ok bool
				slot, ok = template.fieldSlot(join.refAccess.root)
				if !ok {
					continue
				}
			}
			if slot < 0 || slot >= len(template.fields) {
				continue
			}
			out = append(out, reteGraphBackchainDemandJoinSlot{
				slot:   slot,
				access: join.access,
				last:   true,
			})
		}
	}
	return out
}

func (g *reteGraph) hasBackchainDemandPlans() bool {
	if g == nil {
		return false
	}
	for i := range g.betaNodes {
		if len(g.betaNodes[i].backchainDemands) > 0 {
			return true
		}
	}
	return false
}

func (g *reteGraph) internResidualFilterNode(index map[reteGraphBetaKey]reteGraphBetaNodeID, input reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate) (reteGraphBetaNodeID, bool) {
	if g == nil {
		return 0, false
	}
	key := reteGraphBetaKey{
		kind:       reteGraphBetaNodeResidualFilter,
		left:       input,
		joins:      serializeCompiledJoinConstraints(joins),
		predicates: serializeCompiledExpressionPredicates(predicates),
	}
	if id, ok := index[key]; ok {
		return id, false
	}
	id := reteGraphBetaNodeID(len(g.betaNodes) + 1)
	g.betaNodes = append(g.betaNodes, reteGraphBetaNode{
		id:            id,
		kind:          reteGraphBetaNodeResidualFilter,
		left:          input,
		joins:         cloneCompiledJoinConstraints(joins),
		residualJoins: cloneCompiledJoinConstraints(joins),
		predicates:    cloneCompiledExpressionPredicates(predicates),
	})
	index[key] = id
	return id, true
}

func (g *reteGraph) internBetaNodeInternal(index map[reteGraphBetaKey]reteGraphBetaNodeID, kind reteGraphBetaNodeKind, left, right reteGraphStageRef, joins []compiledJoinConstraint, predicates []compiledExpressionPredicate, rightPredicates []compiledExpressionPredicate, rightHasLeftPrefix bool, rightPrefixWidth int) (reteGraphBetaNodeID, bool) {
	if g == nil {
		return 0, false
	}
	if kind == 0 {
		kind = reteGraphBetaNodeJoin
	}
	hashJoins, residualJoins := planCompiledJoinConstraints(joins, predicates)
	key := reteGraphBetaKey{
		kind:               kind,
		left:               left,
		right:              right,
		joins:              serializeCompiledJoinConstraints(joins),
		predicates:         serializeCompiledExpressionPredicates(predicates),
		rightPredicates:    serializeCompiledExpressionPredicates(rightPredicates),
		rightHasLeftPrefix: rightHasLeftPrefix,
		rightPrefixWidth:   rightPrefixWidth,
	}
	if id, ok := index[key]; ok {
		return id, false
	}

	id := reteGraphBetaNodeID(len(g.betaNodes) + 1)
	g.betaNodes = append(g.betaNodes, reteGraphBetaNode{
		id:                 id,
		kind:               kind,
		left:               left,
		right:              right,
		joins:              cloneCompiledJoinConstraints(joins),
		hashJoins:          cloneCompiledJoinConstraints(hashJoins),
		residualJoins:      cloneCompiledJoinConstraints(residualJoins),
		predicates:         cloneCompiledExpressionPredicates(predicates),
		rightPredicates:    cloneCompiledExpressionPredicates(rightPredicates),
		rightHasLeftPrefix: rightHasLeftPrefix,
		rightPrefixWidth:   rightPrefixWidth,
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

func graphTokenEntriesForAggregateBranch(branch compiledConditionBranch, condition compiledConditionPlan) []bindingTupleEntry {
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
		for _, branchCondition := range branch.conditions {
			public := branchCondition.condition
			if public.order != bindingSlot {
				continue
			}
			entry.binding = public.binding
			entry.conditionOrder = public.order
			entry.conditionID = public.id
			break
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
		Plan:                g.inspectPlan(),
	}
}

func (g *reteGraph) inspectPlan() reteGraphPlanInspection {
	if g == nil {
		return reteGraphPlanInspection{}
	}
	return reteGraphPlanInspection{
		AlphaNodes:     g.inspectAlphaNodes(),
		BetaNodes:      g.inspectBetaNodes(),
		AggregateNodes: g.inspectAggregateNodes(),
		TerminalNodes:  g.inspectTerminalNodes(),
		Branches:       cloneReteGraphBranchInspections(g.branchInspections),
	}
}

func (g *reteGraph) inspectAlphaNodes() []reteGraphAlphaNodeInspection {
	if g == nil || len(g.alphaNodes) == 0 {
		return nil
	}
	out := make([]reteGraphAlphaNodeInspection, len(g.alphaNodes))
	for i, node := range g.alphaNodes {
		out[i] = reteGraphAlphaNodeInspection{
			ID:           node.id,
			Target:       node.target,
			Constraints:  cloneCompiledFieldConstraints(node.constraints),
			ListPatterns: cloneCompiledListPatterns(node.listPatterns),
			Predicates:   cloneCompiledExpressionPredicates(node.predicates),
			Route:        node.route,
			Consumers:    cloneReteGraphAlphaConsumers(node.consumers),
			Entry:        cloneBindingTupleEntry(node.entry),
			MemoryKind:   reteGraphMemoryAlphaFactSet,
		}
	}
	return out
}

func (g *reteGraph) inspectBetaNodes() []reteGraphBetaNodeInspection {
	if g == nil || len(g.betaNodes) == 0 {
		return nil
	}
	out := make([]reteGraphBetaNodeInspection, len(g.betaNodes))
	for i, node := range g.betaNodes {
		out[i] = reteGraphBetaNodeInspection{
			ID:              node.id,
			Kind:            node.kind,
			Left:            node.left,
			Right:           node.right,
			Joins:           cloneCompiledJoinConstraints(node.joins),
			HashJoins:       cloneCompiledJoinConstraints(node.hashJoins),
			ResidualJoins:   cloneCompiledJoinConstraints(node.residualJoins),
			Predicates:      cloneCompiledExpressionPredicates(node.predicates),
			RightPredicates: cloneCompiledExpressionPredicates(node.rightPredicates),
			Entry:           cloneBindingTupleEntry(node.entry),
			TokenWidth:      g.stageTokenWidth(reteGraphStageRef{kind: reteGraphStageBeta, id: int(node.id)}),
			MemoryKind:      reteGraphMemoryBetaTokenHash,
		}
	}
	return out
}

func (g *reteGraph) inspectAggregateNodes() []reteGraphAggregateNodeInspection {
	if g == nil || len(g.aggregateNodes) == 0 {
		return nil
	}
	out := make([]reteGraphAggregateNodeInspection, len(g.aggregateNodes))
	for i, node := range g.aggregateNodes {
		out[i] = reteGraphAggregateNodeInspection{
			ID:          node.id,
			Input:       node.input,
			Outer:       node.outer,
			ConditionID: node.conditionID,
			BindingSlot: node.bindingSlot,
			Specs:       cloneCompiledAggregateSpecs(node.specs),
			TokenWidth:  g.stageTokenWidth(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(node.id)}),
			MemoryKind:  reteGraphMemoryAggregate,
		}
	}
	return out
}

func (g *reteGraph) inspectTerminalNodes() []reteGraphTerminalNodeInspection {
	if g == nil || len(g.terminalNodes) == 0 {
		return nil
	}
	out := make([]reteGraphTerminalNodeInspection, len(g.terminalNodes))
	for i, node := range g.terminalNodes {
		out[i] = reteGraphTerminalNodeInspection{
			ID:             node.id,
			Kind:           node.kind,
			RuleRevisionID: node.ruleRevisionID,
			QueryName:      node.queryName,
			Input:          node.input,
			TokenWidth:     g.stageTokenWidth(node.input),
			MemoryKind:     reteGraphMemoryTerminalTokens,
		}
	}
	return out
}

func (r *Ruleset) reteGraphDebugSummary() reteGraphDebugSummary {
	if r == nil || r.graph == nil {
		return reteGraphDebugSummary{}
	}
	return r.graph.debugSummary()
}

func inspectReteGraphRuleBranch(rule compiledRule, branch compiledConditionBranch, terminalID reteGraphTerminalNodeID) reteGraphBranchInspection {
	return reteGraphBranchInspection{
		OwnerKind:      reteGraphBranchOwnerRule,
		RuleID:         rule.id,
		RuleName:       rule.name,
		RuleRevisionID: rule.revisionID,
		BranchID:       branch.id,
		AuthoredOrder:  inspectReteGraphAuthoredOrder(branch.conditions),
		PlannedOrder:   inspectReteGraphPlannedOrder(branch.plans),
		Projections:    inspectReteGraphRuleProjections(rule),
		TerminalID:     terminalID,
	}
}

func inspectReteGraphQueryBranch(query compiledQuery, branch compiledConditionBranch, terminalID reteGraphTerminalNodeID) reteGraphBranchInspection {
	return reteGraphBranchInspection{
		OwnerKind:     reteGraphBranchOwnerQuery,
		QueryName:     query.name,
		BranchID:      branch.id,
		AuthoredOrder: inspectReteGraphQueryAuthoredOrder(query, branch.id),
		PlannedOrder:  inspectReteGraphPlannedOrder(branch.plans),
		Projections:   inspectReteGraphQueryProjections(query),
		TerminalID:    terminalID,
	}
}

func inspectReteGraphRuleProjections(rule compiledRule) []reteGraphTerminalProjectionInspection {
	if len(rule.actionExecutions) == 0 {
		return nil
	}
	out := make([]reteGraphTerminalProjectionInspection, 0)
	for _, action := range rule.actionExecutions {
		if !action.bindingReads.known {
			out = append(out, reteGraphTerminalProjectionInspection{
				Kind:       reteGraphTerminalProjectionGeneric,
				ActionName: action.name,
			})
			continue
		}
		for _, read := range action.bindingReads.reads {
			projection := reteGraphTerminalProjectionInspection{
				ActionName:  action.name,
				BindingSlot: read.bindingSlot,
			}
			if read.value {
				projection.Kind = reteGraphTerminalProjectionActionValue
			} else if read.whole {
				projection.Kind = reteGraphTerminalProjectionActionBinding
			} else {
				projection.Kind = reteGraphTerminalProjectionActionField
				projection.Field = read.access.root
				projection.Path = read.access.path.clone()
			}
			out = append(out, projection)
		}
	}
	return out
}

func inspectReteGraphQueryProjections(query compiledQuery) []reteGraphTerminalProjectionInspection {
	if len(query.returns) == 0 {
		return nil
	}
	out := make([]reteGraphTerminalProjectionInspection, len(query.returns))
	for i, ret := range query.returns {
		out[i] = reteGraphTerminalProjectionInspection{
			Alias:       ret.alias,
			BindingSlot: ret.bindingSlot,
		}
		if ret.fact {
			out[i].Kind = reteGraphTerminalProjectionQueryFact
			continue
		}
		out[i].BindingSlot = ret.projection.bindingSlot
		switch ret.projection.kind {
		case compiledQueryReturnProjectionBindingField:
			out[i].Kind = reteGraphTerminalProjectionQueryField
			out[i].Field = ret.projection.access.root
			out[i].Path = ret.projection.access.path.clone()
		case compiledQueryReturnProjectionBindingValue:
			out[i].Kind = reteGraphTerminalProjectionQueryValue
		case compiledQueryReturnProjectionConst:
			out[i].Kind = reteGraphTerminalProjectionQueryConst
		default:
			out[i].Kind = reteGraphTerminalProjectionGeneric
		}
	}
	return out
}

func inspectReteGraphQueryAuthoredOrder(query compiledQuery, branchID int) []reteGraphConditionOrderInspection {
	for _, branch := range query.conditionBranchPlans {
		if branch.id == branchID {
			return inspectReteGraphAuthoredOrder(branch.conditions)
		}
	}
	return nil
}

func inspectReteGraphAuthoredOrder(conditions []RuleConditionBranchCondition) []reteGraphConditionOrderInspection {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]reteGraphConditionOrderInspection, len(conditions))
	for i, condition := range conditions {
		public := condition.condition
		out[i] = reteGraphConditionOrderInspection{
			Order:       i,
			ConditionID: public.id,
			Binding:     public.binding,
			BindingSlot: public.order,
			Path:        condition.Path(),
			Visible:     condition.visible,
			Negated:     condition.negated,
			Explicit:    condition.explicit,
			Target: conditionTarget{
				kind:        conditionTargetKindForRuleCondition(public),
				name:        public.name,
				templateKey: public.templateKey,
			},
		}
	}
	return out
}

func inspectReteGraphPlannedOrder(plans []compiledConditionPlan) []reteGraphConditionOrderInspection {
	if len(plans) == 0 {
		return nil
	}
	out := make([]reteGraphConditionOrderInspection, len(plans))
	for i, plan := range plans {
		out[i] = reteGraphConditionOrderInspection{
			Order:       i,
			ConditionID: plan.id,
			Binding:     plan.binding,
			BindingSlot: plan.bindingSlot,
			Path:        cloneIntPath(plan.path),
			Visible:     !plan.negated,
			Negated:     plan.negated,
			Explicit:    plan.explicit,
			Aggregate:   plan.aggregate != nil,
			Test:        plan.isTest,
			Target:      plan.target,
		}
	}
	return out
}

func conditionTargetKindForRuleCondition(condition RuleCondition) conditionTargetKind {
	if condition.templateKey != "" {
		return conditionTargetTemplateKey
	}
	if condition.name != "" {
		return conditionTargetName
	}
	return conditionTargetUnknown
}

func cloneReteGraphAlphaNodes(in []reteGraphAlphaNode) []reteGraphAlphaNode {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphAlphaNode, len(in))
	for i, node := range in {
		out[i] = node
		out[i].constraints = cloneCompiledFieldConstraints(node.constraints)
		out[i].listPatterns = cloneCompiledListPatterns(node.listPatterns)
		out[i].predicates = cloneCompiledExpressionPredicates(node.predicates)
		out[i].consumers = cloneReteGraphAlphaConsumers(node.consumers)
		out[i].entry = cloneBindingTupleEntry(node.entry)
		out[i].generatedMatch.equalities = cloneReteGraphAlphaGeneratedEqualities(node.generatedMatch.equalities)
		out[i].generatedOps = cloneReteGraphGeneratedAlphaOps(node.generatedOps)
	}
	return out
}

func cloneReteGraphAlphaGeneratedEqualities(in []reteGraphAlphaGeneratedEquality) []reteGraphAlphaGeneratedEquality {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphAlphaGeneratedEquality, len(in))
	copy(out, in)
	return out
}

func cloneReteGraphGeneratedAlphaOps(in []reteGraphGeneratedAlphaOp) []reteGraphGeneratedAlphaOp {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphGeneratedAlphaOp, len(in))
	for i, op := range in {
		out[i] = op
		out[i].entry = cloneBindingTupleEntry(op.entry)
		out[i].betaEntry = cloneBindingTupleEntry(op.betaEntry)
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
		out[i].rightPredicates = cloneCompiledExpressionPredicates(node.rightPredicates)
		out[i].entry = cloneBindingTupleEntry(node.entry)
		out[i].backchainDemands = cloneReteGraphBackchainDemandPlans(node.backchainDemands)
	}
	return out
}

func cloneReteGraphBackchainDemandPlans(in []reteGraphBackchainDemandPlan) []reteGraphBackchainDemandPlan {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphBackchainDemandPlan, len(in))
	for i, plan := range in {
		out[i] = plan
		out[i].defaultSlots = cloneFactSlots(plan.defaultSlots)
		out[i].constSlots = cloneReteGraphBackchainDemandConstSlots(plan.constSlots)
		out[i].joinSlots = cloneReteGraphBackchainDemandJoinSlots(plan.joinSlots)
		out[i].constraints = cloneCompiledFieldConstraints(plan.constraints)
		out[i].joins = cloneCompiledJoinConstraints(plan.joins)
	}
	return out
}

func cloneReteGraphBackchainDemandConstSlots(in []reteGraphBackchainDemandConstSlot) []reteGraphBackchainDemandConstSlot {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphBackchainDemandConstSlot, len(in))
	for i, slot := range in {
		out[i] = slot
		out[i].value = cloneValue(slot.value)
	}
	return out
}

func cloneReteGraphBackchainDemandJoinSlots(in []reteGraphBackchainDemandJoinSlot) []reteGraphBackchainDemandJoinSlot {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphBackchainDemandJoinSlot, len(in))
	copy(out, in)
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

func cloneReteGraphBranchInspections(in []reteGraphBranchInspection) []reteGraphBranchInspection {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphBranchInspection, len(in))
	for i, branch := range in {
		out[i] = branch
		out[i].AuthoredOrder = cloneReteGraphConditionOrderInspections(branch.AuthoredOrder)
		out[i].PlannedOrder = cloneReteGraphConditionOrderInspections(branch.PlannedOrder)
		out[i].Projections = cloneReteGraphTerminalProjectionInspections(branch.Projections)
	}
	return out
}

func cloneReteGraphTerminalProjectionInspections(in []reteGraphTerminalProjectionInspection) []reteGraphTerminalProjectionInspection {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphTerminalProjectionInspection, len(in))
	for i, projection := range in {
		out[i] = projection
		out[i].Path = projection.Path.clone()
	}
	return out
}

func cloneReteGraphConditionOrderInspections(in []reteGraphConditionOrderInspection) []reteGraphConditionOrderInspection {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteGraphConditionOrderInspection, len(in))
	for i, condition := range in {
		out[i] = condition
		out[i].Path = cloneIntPath(condition.Path)
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
	for i, constraint := range in {
		out[i] = constraint
		out[i].value = cloneValue(constraint.value)
		out[i].values = cloneValueSlice(constraint.values)
		out[i].access = constraint.access.clone()
	}
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
		out[i].access = constraint.access.clone()
		out[i].refAccess = constraint.refAccess.clone()
		if constraint.hasLeftKeyExpression {
			out[i].leftKeyExpression = constraint.leftKeyExpression.clone()
		}
		if constraint.hasRightKeyExpression {
			out[i].rightKeyExpression = constraint.rightKeyExpression.clone()
		}
	}
	return out
}

func planCompiledJoinConstraints(joins []compiledJoinConstraint, predicates []compiledExpressionPredicate) ([]compiledJoinConstraint, []compiledJoinConstraint) {
	hashJoins, residualJoins := splitCompiledJoinConstraints(joins)
	hashJoins = append(hashJoins, expressionPredicateHashJoins(predicates)...)
	sort.SliceStable(hashJoins, func(i, j int) bool {
		return compiledJoinHashKeySortKey(hashJoins[i]) < compiledJoinHashKeySortKey(hashJoins[j])
	})
	hashJoins = dedupeCompiledHashJoins(hashJoins)
	return hashJoins, residualJoins
}

func dedupeCompiledHashJoins(hashJoins []compiledJoinConstraint) []compiledJoinConstraint {
	if len(hashJoins) < 2 {
		return hashJoins
	}
	out := hashJoins[:0]
	var previous string
	for _, join := range hashJoins {
		key := compiledJoinHashKeySortKey(join)
		if len(out) > 0 && key == previous {
			continue
		}
		out = append(out, join)
		previous = key
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

func compiledJoinHashKeySortKey(join compiledJoinConstraint) string {
	var b strings.Builder
	b.WriteString(join.access.display())
	b.WriteByte('|')
	b.WriteString(join.refBinding)
	b.WriteByte('|')
	b.WriteString(join.refAccess.display())
	b.WriteByte('|')
	b.WriteString(string(join.operator))
	if join.hasLeftKeyExpression {
		b.WriteString("|left-expr:")
		b.WriteString(serializeCompiledExpression(join.leftKeyExpression))
	}
	if join.hasRightKeyExpression {
		b.WriteString("|right-expr:")
		b.WriteString(serializeCompiledExpression(join.rightKeyExpression))
	}
	return b.String()
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
	if join, ok := expressionPredicateKeyExtractorHashJoin(predicate); ok {
		return join, true
	}
	if join, ok := expressionPredicateFunctionHashJoin(predicate); ok {
		return join, true
	}
	if predicate.expression.containsFunctionCall() {
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
	if current.access.root == "" || binding.access.root == "" || binding.binding == "" || binding.bindingSlot < 0 || !current.access.topLevel() || !binding.access.topLevel() {
		return compiledJoinConstraint{}, false
	}
	return newExpressionPredicateHashJoin(predicate, current, binding), true
}

func expressionPredicateKeyExtractorHashJoin(predicate compiledExpressionPredicate) (compiledJoinConstraint, bool) {
	expression := predicate.expression
	if expression.kind != expressionNodeCompare || expression.compareOp != ExpressionCompareEqual || len(expression.operands) != 2 {
		return compiledJoinConstraint{}, false
	}
	current, binding, ok := expressionPredicateHashKeyExpressions(expression.operands[0], expression.operands[1])
	if !ok {
		return compiledJoinConstraint{}, false
	}
	if !expressionHashKeyOperandsTopLevel(current) || !expressionHashKeyOperandsTopLevel(binding) {
		return compiledJoinConstraint{}, false
	}
	if current.kind != expressionNodeCall && binding.kind != expressionNodeCall {
		return compiledJoinConstraint{}, false
	}
	return newExpressionPredicateKeyExtractorHashJoin(predicate, current, binding), true
}

func expressionPredicateFunctionHashJoin(predicate compiledExpressionPredicate) (compiledJoinConstraint, bool) {
	call, ok := expressionPredicateEqualityFunctionCall(predicate.expression)
	if !ok {
		return compiledJoinConstraint{}, false
	}
	current, binding, ok := expressionPredicateHashJoinOperands(call.operands[0], call.operands[1])
	if !ok {
		return compiledJoinConstraint{}, false
	}
	if current.access.root == "" || binding.access.root == "" || binding.binding == "" || binding.bindingSlot < 0 || !current.access.topLevel() || !binding.access.topLevel() {
		return compiledJoinConstraint{}, false
	}
	return newExpressionPredicateHashJoin(predicate, current, binding), true
}

func newExpressionPredicateKeyExtractorHashJoin(predicate compiledExpressionPredicate, current, binding compiledExpression) compiledJoinConstraint {
	join := newExpressionPredicateHashJoin(predicate, expressionHashKeyAccessOperand(current), expressionHashKeyAccessOperand(binding))
	join.leftKeyExpression = current.clone()
	join.hasLeftKeyExpression = true
	join.rightKeyExpression = binding.clone()
	join.hasRightKeyExpression = true
	return join
}

func expressionPredicateEqualityFunctionCall(expression compiledExpression) (compiledExpression, bool) {
	if expression.kind == expressionNodeCall {
		return expressionEqualityFunctionCall(expression)
	}
	if expression.kind != expressionNodeCompare || expression.compareOp != ExpressionCompareEqual || len(expression.operands) != 2 {
		return compiledExpression{}, false
	}
	if call, ok := expressionEqualityFunctionCompareOperand(expression.operands[0], expression.operands[1]); ok {
		return call, true
	}
	return expressionEqualityFunctionCompareOperand(expression.operands[1], expression.operands[0])
}

func expressionEqualityFunctionCompareOperand(call, constant compiledExpression) (compiledExpression, bool) {
	if constant.kind != expressionNodeConst || constant.resultKind != ValueBool {
		return compiledExpression{}, false
	}
	boolValue, ok := constant.value.AsBool()
	if !ok || !boolValue {
		return compiledExpression{}, false
	}
	return expressionEqualityFunctionCall(call)
}

func expressionEqualityFunctionCall(expression compiledExpression) (compiledExpression, bool) {
	if expression.kind != expressionNodeCall || !expression.function.equalityComparator || expression.resultKind != ValueBool || len(expression.operands) != 2 {
		return compiledExpression{}, false
	}
	return expression, true
}

func newExpressionPredicateHashJoin(predicate compiledExpressionPredicate, current, binding compiledExpression) compiledJoinConstraint {
	conditionIndex := -1
	if len(predicate.path) > 0 {
		conditionIndex = predicate.path[0]
	}
	return compiledJoinConstraint{
		path:           cloneIntPath(predicate.path),
		bindingSlot:    conditionIndex,
		access:         current.access.clone(),
		operator:       FieldConstraintOpEqual,
		refBinding:     binding.binding,
		refBindingSlot: binding.bindingSlot,
		refAccess:      binding.access.clone(),
		indexable:      true,
		indexKind:      joinIndexEquality,
	}
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

func expressionPredicateHashKeyExpressions(left, right compiledExpression) (compiledExpression, compiledExpression, bool) {
	if expressionHashKeyCurrent(left) && expressionHashKeyBinding(right) {
		return left, right, true
	}
	if expressionHashKeyCurrent(right) && expressionHashKeyBinding(left) {
		return right, left, true
	}
	return compiledExpression{}, compiledExpression{}, false
}

func expressionHashKeyCurrent(expression compiledExpression) bool {
	access := expressionHashKeyAccessOperand(expression)
	return access.kind == expressionNodeCurrentField && expressionCertifiedHashKeyExpression(expression)
}

func expressionHashKeyBinding(expression compiledExpression) bool {
	access := expressionHashKeyAccessOperand(expression)
	return access.kind == expressionNodeBindingField && access.binding != "" && access.bindingSlot >= 0 && expressionCertifiedHashKeyExpression(expression)
}

func expressionCertifiedHashKeyExpression(expression compiledExpression) bool {
	switch expression.kind {
	case expressionNodeCurrentField, expressionNodeBindingField:
		return true
	case expressionNodeCall:
		return expression.function.indexKeyExtractor && len(expression.operands) == 1 && validPureFunctionIndexKeyKind(expression.resultKind)
	default:
		return false
	}
}

func expressionHashKeyAccessOperand(expression compiledExpression) compiledExpression {
	if expression.kind == expressionNodeCall && expression.function.indexKeyExtractor && len(expression.operands) == 1 {
		return expression.operands[0]
	}
	return expression
}

func expressionHashKeyOperandsTopLevel(expression compiledExpression) bool {
	access := expressionHashKeyAccessOperand(expression)
	return access.access.root != "" && access.access.topLevel()
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
	if predicate.expression.containsFunctionCall() {
		return compiledFieldConstraint{}, false
	}
	expression := predicate.expression
	if constraint, ok := expressionPredicateAlphaMembershipConstraint(expression); ok {
		return constraint, true
	}
	if expression.kind != expressionNodeCompare || len(expression.operands) != 2 {
		return compiledFieldConstraint{}, false
	}
	current, constant, operator, ok := expressionPredicateAlphaConstraintOperands(expression.operands[0], expression.operands[1], expression.compareOp)
	if !ok {
		return compiledFieldConstraint{}, false
	}
	if current.access.root == "" || !current.access.topLevel() {
		return compiledFieldConstraint{}, false
	}
	return compiledFieldConstraint{
		operator: operator,
		value:    cloneValue(constant.value),
		access:   current.access.clone(),
	}, true
}

func expressionPredicateAlphaMembershipConstraint(expression compiledExpression) (compiledFieldConstraint, bool) {
	if expression.kind != expressionNodeBoolean || expression.boolOp != ExpressionBoolOr || len(expression.operands) == 0 {
		return compiledFieldConstraint{}, false
	}

	var access compiledPathAccess
	values := make([]Value, 0, len(expression.operands))
	seen := make(map[string]struct{}, len(expression.operands))
	for i, operand := range expression.operands {
		current, constant, operator, ok := expressionPredicateAlphaConstraintOperandsForMembership(operand)
		if !ok || operator != FieldConstraintOpEqual {
			return compiledFieldConstraint{}, false
		}
		if current.access.root == "" || !current.access.topLevel() {
			return compiledFieldConstraint{}, false
		}
		if i == 0 {
			access = current.access.clone()
		} else if current.access.display() != access.display() {
			return compiledFieldConstraint{}, false
		}
		key := constant.value.canonicalKey()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, cloneValue(constant.value))
	}
	if len(values) == 0 {
		return compiledFieldConstraint{}, false
	}
	return compiledFieldConstraint{
		operator: fieldConstraintOpIn,
		values:   values,
		access:   access,
	}, true
}

func expressionPredicateAlphaConstraintOperandsForMembership(expression compiledExpression) (compiledExpression, compiledExpression, FieldConstraintOperator, bool) {
	if expression.kind != expressionNodeCompare || len(expression.operands) != 2 {
		return compiledExpression{}, compiledExpression{}, "", false
	}
	return expressionPredicateAlphaConstraintOperands(expression.operands[0], expression.operands[1], expression.compareOp)
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
	valueKeys := make([]string, 0, len(constraint.values))
	for _, value := range constraint.values {
		valueKeys = append(valueKeys, value.canonicalKey())
	}
	sort.Strings(valueKeys)
	valuesKey := strings.Join(valueKeys, ",")
	return fmt.Sprintf(
		"field:%d:%s\npath:%d:%s\noperator:%d:%s\nvalue:%d:%s\nvalues:%d:%s\nfield-slot:%d\n",
		len(constraint.access.root), constraint.access.root,
		len(constraint.access.display()), constraint.access.display(),
		len(constraint.operator), constraint.operator,
		len(valueKey), valueKey,
		len(valuesKey), valuesKey,
		constraint.access.rootSlot,
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
		"field:%d:%s\npath:%d:%s\nleft-key:%t:%d:%s\noperator:%d:%s\nref-field:%d:%s\nref-path:%d:%s\nright-key:%t:%d:%s\nbinding-slot:%d\nref-binding-slot:%d\nfield-slot:%d\nref-field-slot:%d\n",
		len(join.access.root), join.access.root,
		len(join.access.display()), join.access.display(),
		join.hasLeftKeyExpression,
		len(serializeOptionalJoinKeyExpression(join.hasLeftKeyExpression, join.leftKeyExpression)),
		serializeOptionalJoinKeyExpression(join.hasLeftKeyExpression, join.leftKeyExpression),
		len(join.operator), join.operator,
		len(join.refAccess.root), join.refAccess.root,
		len(join.refAccess.display()), join.refAccess.display(),
		join.hasRightKeyExpression,
		len(serializeOptionalJoinKeyExpression(join.hasRightKeyExpression, join.rightKeyExpression)),
		serializeOptionalJoinKeyExpression(join.hasRightKeyExpression, join.rightKeyExpression),
		join.bindingSlot,
		join.refBindingSlot,
		join.access.rootSlot,
		join.refAccess.rootSlot,
	)
}

func serializeOptionalJoinKeyExpression(ok bool, expression compiledExpression) string {
	if !ok {
		return ""
	}
	return serializeCompiledExpression(expression)
}
