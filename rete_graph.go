package gess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

type reteGraph struct {
	alphaNodes          []reteGraphAlphaNode
	betaNodes           []reteGraphBetaNode
	terminalNodes       []reteGraphTerminalNode
	routesByTemplateKey map[TemplateKey][]reteGraphAlphaNodeID
}

type reteGraphAlphaNodeID int
type reteGraphBetaNodeID int
type reteGraphTerminalNodeID int

type reteGraphStageKind uint8

const (
	reteGraphStageUnknown reteGraphStageKind = iota
	reteGraphStageAlpha
	reteGraphStageBeta
)

type reteGraphStageRef struct {
	kind reteGraphStageKind
	id   int
}

type reteGraphAlphaNode struct {
	id          reteGraphAlphaNodeID
	target      conditionTarget
	constraints []compiledFieldConstraint
	consumers   []reteBetaConditionRoute
}

type reteGraphBetaNode struct {
	id    reteGraphBetaNodeID
	left  reteGraphStageRef
	right reteGraphStageRef
	joins []compiledJoinConstraint
}

type reteGraphTerminalNode struct {
	id             reteGraphTerminalNodeID
	ruleRevisionID RuleRevisionID
	input          reteGraphStageRef
}

type reteGraphDebugSummary struct {
	AlphaNodes          []reteGraphAlphaNode
	BetaNodes           []reteGraphBetaNode
	TerminalNodes       []reteGraphTerminalNode
	RoutesByTemplateKey map[TemplateKey][]reteGraphAlphaNodeID
}

type reteGraphAlphaKey struct {
	target      reteGraphTargetKey
	constraints string
}

type reteGraphTargetKey struct {
	kind        conditionTargetKind
	name        string
	templateKey TemplateKey
}

type reteGraphBetaKey struct {
	left  reteGraphStageRef
	right reteGraphStageRef
	joins string
}

func compileReteGraph(compiledRules []compiledRule, templatesByKey map[TemplateKey]Template) *reteGraph {
	graph := &reteGraph{
		routesByTemplateKey: make(map[TemplateKey][]reteGraphAlphaNodeID),
	}
	if len(compiledRules) == 0 {
		return graph
	}

	alphaIndex := make(map[reteGraphAlphaKey]reteGraphAlphaNodeID, len(compiledRules))
	betaIndex := make(map[reteGraphBetaKey]reteGraphBetaNodeID, len(compiledRules))

	for _, rule := range compiledRules {
		var current reteGraphStageRef
		haveStage := false

		for conditionIndex, condition := range rule.conditionPlans {
			alphaID, created := graph.internAlphaNode(alphaIndex, condition.target, condition.constraints)
			alphaRef := reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alphaID)}
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
				graph.routesByTemplateKey[condition.target.templateKey] = append(graph.routesByTemplateKey[condition.target.templateKey], alphaID)
			}
			if !haveStage {
				current = alphaRef
				haveStage = true
				continue
			}

			betaID, _ := graph.internBetaNode(betaIndex, current, alphaRef, condition.joins)
			current = reteGraphStageRef{kind: reteGraphStageBeta, id: int(betaID)}
		}

		if !haveStage {
			continue
		}
		graph.terminalNodes = append(graph.terminalNodes, reteGraphTerminalNode{
			id:             reteGraphTerminalNodeID(len(graph.terminalNodes) + 1),
			ruleRevisionID: rule.revisionID,
			input:          current,
		})
	}

	return graph
}

func reteGraphSupportsAlpha(target conditionTarget, templatesByKey map[TemplateKey]Template) bool {
	if target.kind != conditionTargetTemplateKey || target.templateKey == "" {
		return false
	}
	template, ok := templatesByKey[target.templateKey]
	return ok && template.closed
}

func (g *reteGraph) internAlphaNode(index map[reteGraphAlphaKey]reteGraphAlphaNodeID, target conditionTarget, constraints []compiledFieldConstraint) (reteGraphAlphaNodeID, bool) {
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
	}
	if id, ok := index[key]; ok {
		return id, false
	}

	id := reteGraphAlphaNodeID(len(g.alphaNodes) + 1)
	g.alphaNodes = append(g.alphaNodes, reteGraphAlphaNode{
		id:          id,
		target:      target,
		constraints: cloneCompiledFieldConstraints(constraints),
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

func (n reteGraphAlphaNode) matchesSnapshot(fact FactSnapshot) bool {
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
	return true
}

func (n reteGraphAlphaNode) matchesWorking(fact *workingFact) bool {
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
	return true
}

func (g *reteGraph) internBetaNode(index map[reteGraphBetaKey]reteGraphBetaNodeID, left, right reteGraphStageRef, joins []compiledJoinConstraint) (reteGraphBetaNodeID, bool) {
	if g == nil {
		return 0, false
	}
	key := reteGraphBetaKey{
		left:  left,
		right: right,
		joins: serializeCompiledJoinConstraints(joins),
	}
	if id, ok := index[key]; ok {
		return id, false
	}

	id := reteGraphBetaNodeID(len(g.betaNodes) + 1)
	g.betaNodes = append(g.betaNodes, reteGraphBetaNode{
		id:    id,
		left:  left,
		right: right,
		joins: cloneCompiledJoinConstraints(joins),
	})
	index[key] = id
	return id, true
}

func (g *reteGraph) debugSummary() reteGraphDebugSummary {
	if g == nil {
		return reteGraphDebugSummary{}
	}
	return reteGraphDebugSummary{
		AlphaNodes:          cloneReteGraphAlphaNodes(g.alphaNodes),
		BetaNodes:           cloneReteGraphBetaNodes(g.betaNodes),
		TerminalNodes:       cloneReteGraphTerminalNodes(g.terminalNodes),
		RoutesByTemplateKey: cloneReteGraphAlphaRoutes(g.routesByTemplateKey),
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
		out[i].consumers = cloneReteGraphAlphaConsumers(node.consumers)
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

func cloneReteGraphAlphaConsumers(in []reteBetaConditionRoute) []reteBetaConditionRoute {
	if len(in) == 0 {
		return nil
	}
	out := make([]reteBetaConditionRoute, len(in))
	copy(out, in)
	return out
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
