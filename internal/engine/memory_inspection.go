package engine

import (
	"context"
	"encoding/json"
	"sort"
)

const MemoryInspectionSchemaVersion = 1

type MemoryInspectionRequest struct {
	NodeID   string `json:"nodeId,omitempty"`
	Cursor   int    `json:"cursor,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	MaxBytes int    `json:"maxBytes,omitempty"`
}

type MemoryOwnerSummary struct {
	Owner      string `json:"owner"`
	Rows       uint64 `json:"rows"`
	Buckets    uint64 `json:"buckets"`
	Indexes    uint64 `json:"indexes"`
	Tombstones uint64 `json:"tombstones"`
	Bytes      uint64 `json:"bytes"`
	HighWater  uint64 `json:"highWater"`
}

type MemoryNodeSummary struct {
	NodeID          string `json:"nodeId"`
	Kind            string `json:"kind"`
	Rows            int    `json:"rows"`
	Capacity        int    `json:"capacity"`
	Buckets         int    `json:"buckets"`
	Indexes         int    `json:"indexes"`
	TokenWidth      int    `json:"tokenWidth,omitempty"`
	DetailSupported bool   `json:"detailSupported"`
	DetailReason    string `json:"detailReason"`
}

type MemoryRow struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	FactIDs        []string `json:"factIds"`
	RuleID         string   `json:"ruleId,omitempty"`
	RuleRevisionID string   `json:"ruleRevisionId,omitempty"`
	RuleName       string   `json:"ruleName,omitempty"`
	Module         string   `json:"module,omitempty"`
	Salience       int      `json:"salience,omitempty"`
}

type MemoryRowCollection struct {
	Availability bool        `json:"availability"`
	Reason       string      `json:"reason"`
	Items        []MemoryRow `json:"items"`
	Limit        int         `json:"limit"`
	MaxBytes     int         `json:"maxBytes"`
	Total        int         `json:"total"`
	Returned     int         `json:"returned"`
	NextCursor   *int        `json:"nextCursor"`
	Truncated    bool        `json:"truncated"`
}

type MemoryInspectionReport struct {
	Schema       int                  `json:"gessMemorySchema"`
	RulesetID    RulesetID            `json:"rulesetId"`
	Generation   Generation           `json:"generation"`
	Availability bool                 `json:"availability"`
	Reason       string               `json:"reason"`
	Owners       []MemoryOwnerSummary `json:"owners"`
	Nodes        []MemoryNodeSummary  `json:"nodes"`
	Detail       MemoryRowCollection  `json:"detail"`
}

func (s *Session) MemoryInspection(ctx context.Context, request MemoryInspectionRequest) (MemoryInspectionReport, error) {
	if s == nil || s.closed {
		return MemoryInspectionReport{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MemoryInspectionReport{}, err
	}
	if s.runGuardHeld() || !s.lock() {
		return MemoryInspectionReport{}, ErrConcurrencyMisuse
	}
	defer s.unlock()
	return s.memoryInspectionLocked(request), nil
}

func (s *Session) memoryInspectionLocked(request MemoryInspectionRequest) MemoryInspectionReport {
	if request.Cursor < 0 {
		request.Cursor = 0
	}
	if request.Limit <= 0 || request.Limit > 100 {
		request.Limit = 100
	}
	if request.MaxBytes < 256 || request.MaxBytes > 64<<10 {
		request.MaxBytes = 64 << 10
	}
	report := MemoryInspectionReport{
		Schema: MemoryInspectionSchemaVersion, RulesetID: s.revision.ID(), Generation: s.factStore.generation,
		Availability: true,
		Detail:       MemoryRowCollection{Availability: false, Reason: "select a memory with supported row detail", Items: []MemoryRow{}, Limit: request.Limit, MaxBytes: request.MaxBytes},
	}
	if s.propagation.runtime == nil || s.propagation.runtime.graphBeta == nil {
		report.Availability, report.Reason = false, "Rete runtime memory is unavailable"
		return report
	}
	for _, owner := range s.runtimeDiagnosticsLocked().MemoryOwners {
		report.Owners = append(report.Owners, MemoryOwnerSummary(owner))
	}
	report.Nodes = s.memoryNodeSummariesLocked()
	if request.NodeID != "" {
		report.Detail = s.memoryRowsLocked(request)
	}
	return report
}

func (s *Session) memoryNodeSummariesLocked() []MemoryNodeSummary {
	memory := s.propagation.runtime.graphBeta
	graph := s.revision.graph
	out := make([]MemoryNodeSummary, 0, len(graph.alphaNodes)+len(graph.betaNodes)+len(graph.aggregateNodes)+len(graph.terminalNodes)+1)
	for _, node := range graph.alphaNodes {
		index := int(node.id)
		rows, capacity := 0, 0
		if index >= 0 && index < len(memory.alpha.facts) {
			rows = memory.alpha.facts[index].count()
			capacity = len(memory.alpha.facts[index].inline) + cap(memory.alpha.facts[index].overflow)
		}
		out = append(out, MemoryNodeSummary{NodeID: topologyStageID(reteGraphStageRef{kind: reteGraphStageAlpha, id: index}), Kind: "alpha", Rows: rows, Capacity: capacity, DetailSupported: true})
	}
	for _, diagnostic := range memory.diagnostics().BetaNodes {
		kind := map[reteGraphBetaNodeKind]string{reteGraphBetaNodeJoin: "beta", reteGraphBetaNodeNot: "negative", reteGraphBetaNodeFilter: "filter", reteGraphBetaNodeResidualFilter: "residual-filter"}[diagnostic.Kind]
		capacity := 0
		if node := memory.betaNodeMemoryAt(diagnostic.ID); node != nil {
			if diagnostic.Kind == reteGraphBetaNodeNot {
				capacity = node.negative.left.rowCapacity() + node.negative.right.rowCapacity()
			} else {
				capacity = node.left.rowCapacity() + node.right.rowCapacity()
			}
		}
		out = append(out, MemoryNodeSummary{NodeID: topologyStageID(reteGraphStageRef{kind: reteGraphStageBeta, id: int(diagnostic.ID)}), Kind: kind, Rows: diagnostic.TotalRows, Capacity: capacity, Buckets: diagnostic.TotalJoinBucketDepth, Indexes: diagnostic.TotalJoinIndexKeys + diagnostic.IdentityIndexKeys + diagnostic.FactIndexKeys, TokenWidth: diagnostic.TokenWidth, DetailReason: "token rows are intentionally opaque"})
	}
	for _, node := range graph.aggregateNodes {
		rows, capacity, buckets := 0, 0, 0
		if current := memory.aggregateMemory(node.id); current != nil {
			buckets = current.bucketCount()
			capacity = cap(current.buckets.rows)
			current.forEachBucket(func(bucket *reteGraphAggregateBucket) {
				rows += 1 + len(bucket.inputTokens)
				if !bucket.token.isZero() {
					rows++
				}
			})
		}
		out = append(out, MemoryNodeSummary{NodeID: topologyStageID(reteGraphStageRef{kind: reteGraphStageAggregate, id: int(node.id)}), Kind: "aggregate", Rows: rows, Capacity: capacity, Buckets: buckets, DetailReason: "aggregate accumulator rows are intentionally opaque"})
	}
	for _, diagnostic := range memory.diagnostics().Terminals {
		kind := "terminal"
		if diagnostic.Kind == reteGraphTerminalQuery {
			kind = "query-terminal"
		}
		out = append(out, MemoryNodeSummary{NodeID: topologyTerminalID(diagnostic.ID), Kind: kind, Rows: diagnostic.Rows, TokenWidth: diagnostic.TokenWidth, DetailReason: "terminal token rows are intentionally opaque"})
	}
	agendaRows := 0
	if s.agendaDriver.agenda != nil {
		agendaRows = s.agendaDriver.agenda.pendingActivationCount()
	}
	out = append(out, MemoryNodeSummary{NodeID: "rete:agenda", Kind: "agenda", Rows: agendaRows, Capacity: agendaRows, DetailSupported: true})
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func (s *Session) memoryRowsLocked(request MemoryInspectionRequest) MemoryRowCollection {
	collection := MemoryRowCollection{Availability: true, Items: []MemoryRow{}, Limit: request.Limit, MaxBytes: request.MaxBytes}
	rows := make([]MemoryRow, 0)
	if request.NodeID == "rete:agenda" {
		if !s.agendaDriver.ready || s.agendaDriver.dirty {
			collection.Availability, collection.Reason = false, "agenda detail is unavailable until the agenda is ready"
			return collection
		}
		for _, activation := range s.agendaLocked().Activations() {
			ids := activation.FactIDs()
			factIDs := make([]string, len(ids))
			for i, id := range ids {
				factIDs[i] = id.String()
			}
			rows = append(rows, MemoryRow{ID: activation.ActivationID().String(), Kind: "activation", FactIDs: factIDs, RuleID: activation.RuleID().String(), RuleRevisionID: activation.RuleRevisionID().String(), RuleName: activation.RuleName(), Module: activation.Module().String(), Salience: activation.Salience()})
		}
	} else {
		var alpha *reteGraphAlphaFactSet
		for _, node := range s.revision.graph.alphaNodes {
			if request.NodeID == topologyStageID(reteGraphStageRef{kind: reteGraphStageAlpha, id: int(node.id)}) && int(node.id) < len(s.propagation.runtime.graphBeta.alpha.facts) {
				alpha = &s.propagation.runtime.graphBeta.alpha.facts[int(node.id)]
				break
			}
		}
		if alpha == nil {
			collection.Availability, collection.Reason = false, "row detail is unsupported for this memory"
			return collection
		}
		alpha.forEach(func(id FactID) bool {
			rows = append(rows, MemoryRow{ID: id.String(), Kind: "fact", FactIDs: []string{id.String()}})
			return true
		})
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	}
	collection.Total = len(rows)
	if request.Cursor >= len(rows) {
		return collection
	}
	bytesUsed := 2
	for index := request.Cursor; index < len(rows) && len(collection.Items) < request.Limit; index++ {
		encoded, _ := json.Marshal(rows[index])
		if bytesUsed+len(encoded)+1 > request.MaxBytes {
			break
		}
		bytesUsed += len(encoded) + 1
		collection.Items = append(collection.Items, rows[index])
	}
	collection.Returned = len(collection.Items)
	next := request.Cursor + collection.Returned
	if next < len(rows) {
		collection.NextCursor = &next
		collection.Truncated = true
	}
	return collection
}
