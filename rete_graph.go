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
	terminalNodes       []reteGraphTerminalNode
	routesByTemplateKey map[TemplateKey][]reteGraphAlphaNodeID
	routesByName        map[string][]reteGraphAlphaNodeID
	alphaRouteTables    map[TemplateKey]*reteGraphAlphaRouteTable
	successorsByStage   map[reteGraphStageRef][]reteGraphStageSuccessor
	terminalsByStage    map[reteGraphStageRef][]reteGraphTerminalRoute
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
	entry       bindingTupleEntry
	route       reteGraphAlphaRouteSelector
}

type reteGraphBetaNode struct {
	id            reteGraphBetaNodeID
	left          reteGraphStageRef
	right         reteGraphStageRef
	joins         []compiledJoinConstraint
	hashJoins     []compiledJoinConstraint
	residualJoins []compiledJoinConstraint
	entry         bindingTupleEntry
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
		terminalsByStage:    make(map[reteGraphStageRef][]reteGraphTerminalRoute),
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

			betaID, _ := graph.internBetaNode(betaIndex, current, alphaRef, condition.joins)
			if betaNode := graph.betaNode(betaID); betaNode != nil && betaNode.entry.conditionID == "" {
				betaNode.entry = graphTokenEntryForCondition(condition)
			}
			leftEntry := bindingTupleEntry{}
			if current.kind == reteGraphStageAlpha && conditionIndex > 0 {
				leftEntry = graphTokenEntryForCondition(rule.conditionPlans[conditionIndex-1])
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
		graph.terminalNodes = append(graph.terminalNodes, reteGraphTerminalNode{
			id:             reteGraphTerminalNodeID(len(graph.terminalNodes) + 1),
			ruleRevisionID: rule.revisionID,
			input:          current,
		})
		terminalEntry := bindingTupleEntry{}
		if current.kind == reteGraphStageAlpha && len(rule.conditionPlans) > 0 {
			terminalEntry = graphTokenEntryForCondition(rule.conditionPlans[0])
		}
		graph.appendTerminal(current, reteGraphTerminalRoute{
			terminalID: reteGraphTerminalNodeID(len(graph.terminalNodes)),
			entry:      terminalEntry,
		})
	}

	return graph
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
		return leftWidth + 1
	default:
		return 0
	}
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
	hashJoins, residualJoins := splitCompiledJoinConstraints(joins)
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
		id:            id,
		left:          left,
		right:         right,
		joins:         cloneCompiledJoinConstraints(joins),
		hashJoins:     cloneCompiledJoinConstraints(hashJoins),
		residualJoins: cloneCompiledJoinConstraints(residualJoins),
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

func (g *reteGraph) debugSummary() reteGraphDebugSummary {
	if g == nil {
		return reteGraphDebugSummary{}
	}
	return reteGraphDebugSummary{
		AlphaNodes:          cloneReteGraphAlphaNodes(g.alphaNodes),
		BetaNodes:           cloneReteGraphBetaNodes(g.betaNodes),
		TerminalNodes:       cloneReteGraphTerminalNodes(g.terminalNodes),
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
