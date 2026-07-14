package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const TopologySchemaVersion = 1

type TopologyMode string

const (
	TopologyModeFull    TopologyMode = "full"
	TopologyModeFocused TopologyMode = "focused"
	TopologyModeSummary TopologyMode = "summary"
)

type TopologySelector struct {
	NodeID         string         `json:"nodeId,omitempty"`
	RuleID         RuleID         `json:"ruleId,omitempty"`
	RuleRevisionID RuleRevisionID `json:"ruleRevisionId,omitempty"`
	FactID         FactID         `json:"factId"`
	ActivationID   ActivationID   `json:"activationId,omitempty"`
}

func (s TopologySelector) empty() bool {
	return s.NodeID == "" && s.RuleID.IsZero() && s.RuleRevisionID.IsZero() && s.FactID.IsZero() && s.ActivationID.IsZero()
}

type TopologyRequest struct {
	Selector TopologySelector `json:"selector"`
	Radius   int              `json:"radius,omitempty"`
	MaxNodes int              `json:"maxNodes,omitempty"`
	MaxEdges int              `json:"maxEdges,omitempty"`
}

type TopologyFocus struct {
	NodeID         string `json:"nodeId,omitempty"`
	RuleID         string `json:"ruleId,omitempty"`
	RuleRevisionID string `json:"ruleRevisionId,omitempty"`
	FactID         string `json:"factId,omitempty"`
	ActivationID   string `json:"activationId,omitempty"`
}

type TopologySource struct {
	Name        string `json:"name,omitempty"`
	StartLine   int    `json:"startLine,omitempty"`
	StartColumn int    `json:"startColumn,omitempty"`
	EndLine     int    `json:"endLine,omitempty"`
	EndColumn   int    `json:"endColumn,omitempty"`
}

type TopologyOwner struct {
	Kind           string         `json:"kind"`
	RuleID         RuleID         `json:"ruleId,omitempty"`
	RuleName       string         `json:"ruleName,omitempty"`
	RuleRevisionID RuleRevisionID `json:"ruleRevisionId,omitempty"`
	QueryName      string         `json:"queryName,omitempty"`
	BranchID       int            `json:"branchId"`
	ConditionID    ConditionID    `json:"conditionId,omitempty"`
	ConditionPath  []int          `json:"conditionPath,omitempty"`
	Source         TopologySource `json:"source"`
}

type TopologyNode struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Label  string          `json:"label"`
	Rank   int             `json:"rank"`
	Target string          `json:"target,omitempty"`
	Owners []TopologyOwner `json:"owners,omitempty"`
}

type TopologyEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type TopologyTotals struct {
	Nodes int            `json:"nodes"`
	Edges int            `json:"edges"`
	Kinds map[string]int `json:"kinds"`
}

type TopologyReport struct {
	Schema       int            `json:"gessTopologySchema"`
	RulesetID    RulesetID      `json:"rulesetId"`
	Mode         TopologyMode   `json:"mode"`
	Availability bool           `json:"availability"`
	Reason       string         `json:"reason"`
	Focus        TopologyFocus  `json:"focus"`
	Totals       TopologyTotals `json:"totals"`
	Returned     TopologyTotals `json:"returned"`
	Truncated    bool           `json:"truncated"`
	Nodes        []TopologyNode `json:"nodes"`
	Edges        []TopologyEdge `json:"edges"`
}

type topologyGraph struct {
	nodes []TopologyNode
	edges []TopologyEdge
}

func (s *Session) Topology(ctx context.Context, request TopologyRequest) (TopologyReport, error) {
	if s == nil || s.closed {
		return TopologyReport{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return TopologyReport{}, err
	}
	if s.runGuardHeld() || !s.lock() {
		return TopologyReport{}, ErrConcurrencyMisuse
	}
	defer s.unlock()
	return s.topologyLocked(request), nil
}

func (s *Session) topologyLocked(request TopologyRequest) TopologyReport {
	if request.MaxNodes <= 0 {
		request.MaxNodes = 800
	}
	if request.MaxEdges <= 0 {
		request.MaxEdges = 1800
	}
	if request.Radius <= 0 {
		request.Radius = 2
	}
	report := TopologyReport{Schema: TopologySchemaVersion, RulesetID: s.revision.ID(), Availability: true, Focus: topologyFocus(request.Selector)}
	if s.revision == nil || s.revision.graph == nil {
		report.Mode = TopologyModeSummary
		report.Availability = false
		report.Reason = "compiled topology is unavailable"
		return report
	}
	graph := buildTopologyGraph(s.revision)
	report.Totals = topologyTotals(graph.nodes, graph.edges)
	if request.Selector.empty() {
		if len(graph.nodes) > request.MaxNodes || len(graph.edges) > request.MaxEdges {
			report.Mode = TopologyModeSummary
			report.Truncated = true
			report.Nodes = []TopologyNode{}
			report.Edges = []TopologyEdge{}
			report.Returned = topologyTotals(report.Nodes, report.Edges)
			return report
		}
		report.Mode = TopologyModeFull
		report.Nodes, report.Edges = graph.nodes, graph.edges
		report.Returned = report.Totals
		return report
	}
	seeds, reason := s.topologySeeds(graph, request.Selector)
	if len(seeds) == 0 {
		report.Mode = TopologyModeFocused
		report.Availability = false
		report.Reason = reason
		report.Nodes = []TopologyNode{}
		report.Edges = []TopologyEdge{}
		report.Returned = topologyTotals(report.Nodes, report.Edges)
		return report
	}
	report.Mode = TopologyModeFocused
	report.Nodes, report.Edges, report.Truncated = topologyNeighborhood(graph, seeds, request.Radius, request.MaxNodes, request.MaxEdges)
	report.Returned = topologyTotals(report.Nodes, report.Edges)
	return report
}

func topologyFocus(selector TopologySelector) TopologyFocus {
	focus := TopologyFocus{
		NodeID:         selector.NodeID,
		RuleID:         string(selector.RuleID),
		RuleRevisionID: string(selector.RuleRevisionID),
		ActivationID:   string(selector.ActivationID),
	}
	if !selector.FactID.IsZero() {
		focus.FactID = selector.FactID.String()
	}
	return focus
}

func buildTopologyGraph(revision *Ruleset) topologyGraph {
	g := revision.graph
	nodes := make([]TopologyNode, 0, 1+len(g.alphaNodes)+len(g.betaNodes)+len(g.aggregateNodes)+len(g.unionNodes)+len(g.terminalNodes))
	nodes = append(nodes, TopologyNode{ID: "rete:root", Kind: "root", Label: "Facts", Rank: 0})
	for _, node := range g.alphaNodes {
		nodes = append(nodes, TopologyNode{ID: topologyStageID(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(node.id)}), Kind: "alpha", Label: "Alpha", Target: topologyTarget(node.target), Owners: topologyOwners(revision, node.conditionStamps)})
	}
	for _, node := range g.betaNodes {
		kind := map[reteGraphBetaNodeKind]string{reteGraphBetaNodeJoin: "join", reteGraphBetaNodeNot: "not", reteGraphBetaNodeFilter: "filter", reteGraphBetaNodeResidualFilter: "residual-filter"}[node.kind]
		nodes = append(nodes, TopologyNode{ID: topologyStageID(reteGraphStageRef{kind: reteGraphStageBeta, id: int(node.id)}), Kind: kind, Label: topologyLabel(kind), Owners: topologyOwners(revision, node.conditionStamps)})
	}
	for _, node := range g.aggregateNodes {
		nodes = append(nodes, TopologyNode{ID: topologyStageID(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(node.id)}), Kind: "aggregate", Label: "Aggregate", Owners: topologyOwners(revision, node.conditionStamps)})
	}
	for _, node := range g.unionNodes {
		nodes = append(nodes, TopologyNode{ID: topologyStageID(reteGraphStageRef{kind: reteGraphStageUnion, id: int(node.id)}), Kind: "union", Label: "Union", Owners: topologyOwners(revision, node.conditionStamps)})
	}
	for _, node := range g.terminalNodes {
		kind, label := "rule-terminal", "Rule terminal"
		if node.kind == reteGraphTerminalQuery {
			kind, label = "query-terminal", "Query "+node.queryName
		} else if rule, ok := revision.rulesByRevisionID[node.ruleRevisionID]; ok {
			label = rule.name
		}
		nodes = append(nodes, TopologyNode{ID: topologyTerminalID(node.id), Kind: kind, Label: label, Owners: topologyTerminalOwners(revision, node)})
	}
	edges := make([]TopologyEdge, 0)
	for _, alpha := range g.alphaNodes {
		edges = append(edges, topologyEdge("rete:root", topologyStageID(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alpha.id)}), "route"))
	}
	for _, beta := range g.betaNodes {
		to := topologyStageID(reteGraphStageRef{kind: reteGraphStageBeta, id: int(beta.id)})
		edges = append(edges, topologyEdge(topologyStageID(beta.left), to, "left"), topologyEdge(topologyStageID(beta.right), to, "right"))
	}
	for _, aggregate := range g.aggregateNodes {
		to := topologyStageID(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregate.id)})
		edges = append(edges, topologyEdge(topologyStageID(aggregate.input), to, "input"), topologyEdge(topologyStageID(aggregate.outer), to, "outer"))
	}
	for _, alpha := range g.alphaNodes {
		from := topologyStageID(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(alpha.id)})
		for _, union := range alpha.edges.unions {
			edges = append(edges, topologyEdge(from, topologyStageID(reteGraphStageRef{kind: reteGraphStageUnion, id: int(union)}), "union"))
		}
	}
	for _, beta := range g.betaNodes {
		from := topologyStageID(reteGraphStageRef{kind: reteGraphStageBeta, id: int(beta.id)})
		for _, union := range beta.edges.unions {
			edges = append(edges, topologyEdge(from, topologyStageID(reteGraphStageRef{kind: reteGraphStageUnion, id: int(union)}), "union"))
		}
	}
	for _, aggregate := range g.aggregateNodes {
		from := topologyStageID(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(aggregate.id)})
		for _, union := range aggregate.edges.unions {
			edges = append(edges, topologyEdge(from, topologyStageID(reteGraphStageRef{kind: reteGraphStageUnion, id: int(union)}), "union"))
		}
	}
	for _, terminal := range g.terminalNodes {
		edges = append(edges, topologyEdge(topologyStageID(terminal.input), topologyTerminalID(terminal.id), "terminal"))
	}
	edges = topologyUniqueEdges(edges)
	topologyRanks(nodes, edges)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Rank != nodes[j].Rank {
			return nodes[i].Rank < nodes[j].Rank
		}
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		return nodes[i].ID < nodes[j].ID
	})
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return topologyGraph{nodes: nodes, edges: edges}
}

func topologyStageID(ref reteGraphStageRef) string {
	switch ref.kind {
	case reteGraphStageRoot:
		return "rete:root"
	case reteGraphStageAlpha:
		return fmt.Sprintf("rete:alpha:%d", ref.id)
	case reteGraphStageBeta:
		return fmt.Sprintf("rete:beta:%d", ref.id)
	case reteGraphStageAggregate:
		return fmt.Sprintf("rete:aggregate:%d", ref.id)
	case reteGraphStageUnion:
		return fmt.Sprintf("rete:union:%d", ref.id)
	}
	return "rete:unsupported"
}
func topologyTerminalID(id reteGraphTerminalNodeID) string {
	return fmt.Sprintf("rete:terminal:%d", id)
}
func topologyEdge(from, to, kind string) TopologyEdge {
	return TopologyEdge{ID: from + "->" + to + ":" + kind, Source: from, Target: to, Kind: kind}
}
func topologyLabel(kind string) string {
	if kind == "" {
		return "Unsupported"
	}
	return kind
}
func topologyTarget(target conditionTarget) string {
	if target.kind == conditionTargetTemplateKey {
		return target.templateKey.String()
	}
	if target.kind == conditionTargetName {
		return target.name
	}
	return "unsupported"
}

func topologyOwners(revision *Ruleset, stamps []reteGraphConditionStamp) []TopologyOwner {
	out := make([]TopologyOwner, 0, len(stamps))
	for _, stamp := range stamps {
		owner := TopologyOwner{Kind: "rule", RuleRevisionID: stamp.owner, BranchID: stamp.branchID, ConditionID: stamp.conditionID}
		if rule, ok := revision.rulesByRevisionID[stamp.owner]; ok {
			owner.RuleID, owner.RuleName = rule.id, rule.name
			owner.ConditionPath = topologyConditionPath(revision.graph, stamp)
			_, owner.Source = topologyConditionMetadata(rule.conditions, stamp.conditionID)
		} else {
			owner.Kind, owner.QueryName = "query", strings.TrimPrefix(string(stamp.owner), "query:")
			if query, ok := revision.queries[owner.QueryName]; ok {
				owner.ConditionPath = topologyConditionPath(revision.graph, stamp)
				_, owner.Source = topologyConditionMetadata(query.conditions, stamp.conditionID)
			}
		}
		out = append(out, owner)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RuleRevisionID != out[j].RuleRevisionID {
			return out[i].RuleRevisionID.String() < out[j].RuleRevisionID.String()
		}
		if out[i].BranchID != out[j].BranchID {
			return out[i].BranchID < out[j].BranchID
		}
		return out[i].ConditionID.String() < out[j].ConditionID.String()
	})
	return out
}

func topologyConditionPath(graph *reteGraph, stamp reteGraphConditionStamp) []int {
	for _, branch := range graph.branchInspections {
		ownerMatches := branch.RuleRevisionID == stamp.owner || branch.OwnerKind == reteGraphBranchOwnerQuery && RuleRevisionID("query:"+branch.QueryName) == stamp.owner
		if !ownerMatches || branch.BranchID != stamp.branchID {
			continue
		}
		for _, condition := range branch.AuthoredOrder {
			if condition.ConditionID == stamp.conditionID {
				return append([]int(nil), condition.Path...)
			}
		}
	}
	return nil
}

func topologyTerminalOwners(revision *Ruleset, node reteGraphTerminalNode) []TopologyOwner {
	if node.kind == reteGraphTerminalQuery {
		return []TopologyOwner{{Kind: "query", QueryName: node.queryName}}
	}
	owner := TopologyOwner{Kind: "rule", RuleRevisionID: node.ruleRevisionID}
	if rule, ok := revision.rulesByRevisionID[node.ruleRevisionID]; ok {
		owner.RuleID, owner.RuleName = rule.id, rule.name
		owner.Source = topologySource(rule.source)
	}
	return []TopologyOwner{owner}
}

func topologyConditionMetadata(conditions []RuleCondition, id ConditionID) ([]int, TopologySource) {
	for i, condition := range conditions {
		if condition.ID() == id {
			return []int{i}, topologySource(condition.Source())
		}
	}
	return nil, TopologySource{}
}
func topologySource(source SourceSpan) TopologySource {
	return TopologySource{Name: source.Name, StartLine: source.StartLine, StartColumn: source.StartColumn, EndLine: source.EndLine, EndColumn: source.EndColumn}
}

func topologyUniqueEdges(edges []TopologyEdge) []TopologyEdge {
	seen := map[string]struct{}{}
	out := edges[:0]
	for _, edge := range edges {
		if edge.Source == "rete:unsupported" || edge.Target == "rete:unsupported" {
			continue
		}
		if _, ok := seen[edge.ID]; ok {
			continue
		}
		seen[edge.ID] = struct{}{}
		out = append(out, edge)
	}
	return out
}

func topologyRanks(nodes []TopologyNode, edges []TopologyEdge) {
	ranks := map[string]int{"rete:root": 0}
	for range nodes {
		changed := false
		for _, edge := range edges {
			next := ranks[edge.Source] + 1
			if next > ranks[edge.Target] {
				ranks[edge.Target] = next
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for i := range nodes {
		nodes[i].Rank = ranks[nodes[i].ID]
	}
}

func topologyTotals(nodes []TopologyNode, edges []TopologyEdge) TopologyTotals {
	kinds := map[string]int{}
	for _, node := range nodes {
		kinds[node.Kind]++
	}
	return TopologyTotals{Nodes: len(nodes), Edges: len(edges), Kinds: kinds}
}

func (s *Session) topologySeeds(graph topologyGraph, selector TopologySelector) (map[string]struct{}, string) {
	seeds := map[string]struct{}{}
	if selector.NodeID != "" {
		for _, node := range graph.nodes {
			if node.ID == selector.NodeID {
				seeds[node.ID] = struct{}{}
				return seeds, ""
			}
		}
		return nil, "node not found"
	}
	revisionID := selector.RuleRevisionID
	if !selector.RuleID.IsZero() {
		rule, ok := s.revision.rulesByID[selector.RuleID]
		if !ok {
			return nil, "rule not found"
		}
		revisionID = rule.revisionID
	}
	if !selector.ActivationID.IsZero() {
		found := false
		if s.agendaDriver.agenda != nil {
			s.agendaDriver.agenda.forEachActivation(func(act *activation) bool {
				if act.activationID() == selector.ActivationID {
					revisionID = act.ruleRevisionID
					found = true
					return false
				}
				return true
			})
		}
		if !found {
			return nil, "activation not found"
		}
	}
	if !selector.FactID.IsZero() {
		fact, ok := s.factByID(selector.FactID)
		if !ok {
			return nil, "fact not found"
		}
		for _, node := range graph.nodes {
			if node.Kind == "alpha" && (node.Target == fact.TemplateKey().String() || node.Target == fact.Name()) {
				seeds[node.ID] = struct{}{}
			}
		}
		if len(seeds) == 0 {
			return nil, "fact target has no graph route"
		}
		return seeds, ""
	}
	if !revisionID.IsZero() {
		for _, node := range graph.nodes {
			for _, owner := range node.Owners {
				if owner.RuleRevisionID == revisionID {
					seeds[node.ID] = struct{}{}
				}
			}
		}
		if len(seeds) == 0 {
			return nil, "rule revision not found"
		}
		return seeds, ""
	}
	return nil, "selector is unsupported"
}

func topologyNeighborhood(graph topologyGraph, seeds map[string]struct{}, radius, maxNodes, maxEdges int) ([]TopologyNode, []TopologyEdge, bool) {
	adj := map[string][]string{}
	for _, edge := range graph.edges {
		adj[edge.Source] = append(adj[edge.Source], edge.Target)
		adj[edge.Target] = append(adj[edge.Target], edge.Source)
	}
	selected := map[string]struct{}{}
	seedIDs := make([]string, 0, len(seeds))
	for id := range seeds {
		seedIDs = append(seedIDs, id)
	}
	sort.Strings(seedIDs)
	truncated := len(seedIDs) > maxNodes
	if len(seedIDs) > maxNodes {
		seedIDs = seedIDs[:maxNodes]
	}
	frontier := make([]string, 0, len(seedIDs))
	for _, id := range seedIDs {
		selected[id] = struct{}{}
		frontier = append(frontier, id)
	}
	for depth := 0; depth < radius && len(frontier) > 0; depth++ {
		next := []string{}
		for _, id := range frontier {
			neighbors := append([]string(nil), adj[id]...)
			sort.Strings(neighbors)
			for _, neighbor := range neighbors {
				if _, ok := selected[neighbor]; ok {
					continue
				}
				if len(selected) >= maxNodes {
					truncated = true
					continue
				}
				selected[neighbor] = struct{}{}
				next = append(next, neighbor)
			}
		}
		frontier = next
	}
	nodes := make([]TopologyNode, 0, len(selected))
	for _, node := range graph.nodes {
		if _, ok := selected[node.ID]; ok {
			nodes = append(nodes, node)
		}
	}
	edges := make([]TopologyEdge, 0)
	for _, edge := range graph.edges {
		_, from := selected[edge.Source]
		_, to := selected[edge.Target]
		if from && to {
			if len(edges) >= maxEdges {
				truncated = true
				continue
			}
			edges = append(edges, edge)
		}
	}
	return nodes, edges, truncated
}
