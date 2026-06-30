package engine

import (
	"context"
	"unsafe"
)

const runtimeMemoryOwnerAlpha = "alpha"
const runtimeMemoryOwnerBeta = "beta"

// RuntimeDiagnostics reports retained runtime memory owners for diagnostics.
type RuntimeDiagnostics struct {
	MemoryOwners []RuntimeMemoryOwnerDiagnostics
}

// RuntimeMemoryOwnerDiagnostics summarizes one Rete/runtime memory owner.
type RuntimeMemoryOwnerDiagnostics struct {
	Owner     string
	Rows      uint64
	Buckets   uint64
	Indexes   uint64
	Bytes     uint64
	HighWater uint64
}

// RuntimeDiagnostics returns a point-in-time diagnostic snapshot of retained runtime memory owners.
func (s *Session) RuntimeDiagnostics(ctx context.Context) (RuntimeDiagnostics, error) {
	if s == nil || s.closed {
		return RuntimeDiagnostics{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RuntimeDiagnostics{}, err
	}
	if s.runGuardHeld() {
		return RuntimeDiagnostics{}, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return RuntimeDiagnostics{}, ErrConcurrencyMisuse
	}
	defer s.unlock()

	return s.runtimeDiagnosticsLocked(), nil
}

func (s *Session) runtimeDiagnosticsLocked() RuntimeDiagnostics {
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return RuntimeDiagnostics{}
	}
	owners := make([]RuntimeMemoryOwnerDiagnostics, 0, 1)
	if owner := s.rete.graphBeta.alphaMemoryOwnerDiagnostics(); owner.Owner != "" {
		owners = append(owners, owner)
	}
	if owner := s.rete.graphBeta.betaMemoryOwnerDiagnostics(); owner.Owner != "" {
		owners = append(owners, owner)
	}
	return RuntimeDiagnostics{MemoryOwners: owners}
}

func (m *reteGraphBetaMemory) alphaMemoryOwnerDiagnostics() RuntimeMemoryOwnerDiagnostics {
	if m == nil {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	var out RuntimeMemoryOwnerDiagnostics
	out.Owner = runtimeMemoryOwnerAlpha

	out.Rows += uint64(alphaFactSetRows(m.alphaFacts))
	out.Rows += uint64(len(m.alphaFactOwnership))
	out.Rows += uint64(len(m.alphaFactRouteStorage))
	out.Rows += uint64(len(m.alphaFactTerminalStorage))
	out.Rows += uint64(len(m.alphaFactBetaStorage))

	alphaOverflowBuckets := alphaFactSetOverflowBuckets(m.alphaFacts)
	out.Buckets += uint64(alphaOverflowBuckets)
	out.Buckets += uint64(len(m.alphaFactCounts))
	out.Buckets += uint64(len(m.factsByName))
	out.Buckets += uint64(len(m.factsByTemplate))
	out.Buckets += uint64(len(m.factFieldEqualIndexes))

	out.Indexes += uint64(alphaConditionIndexCount(m.alphaConditions))
	out.Indexes += uint64(len(m.alphaFactCounts))
	out.Indexes += uint64(len(m.factsByName))
	out.Indexes += uint64(len(m.factsByTemplate))
	out.Indexes += uint64(len(m.factFieldEqualIndexes))

	out.HighWater = uint64(alphaMemoryHighWater(m))
	out.Bytes = alphaMemoryRetainedBytes(m)
	return out
}

func (m *reteGraphBetaMemory) betaMemoryOwnerDiagnostics() RuntimeMemoryOwnerDiagnostics {
	if m == nil {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	out := RuntimeMemoryOwnerDiagnostics{Owner: runtimeMemoryOwnerBeta}
	for _, node := range m.nodes {
		if node == nil {
			continue
		}
		addBetaTokenMemoryOwnerDiagnostics(&out, node.left)
		addBetaTokenMemoryOwnerDiagnostics(&out, node.right)
	}
	if out.Rows == 0 && out.Buckets == 0 && out.Indexes == 0 && out.Bytes == 0 && out.HighWater == 0 {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	return out
}

func addBetaTokenMemoryOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, memory tokenHashMemory) {
	if out == nil {
		return
	}
	joinBuckets := memory.indexes.keyCount()
	identityBuckets := memory.identityRows.keyCount()
	factIndexes := memory.factIndexKeyCount()

	out.Rows += uint64(len(memory.rows))
	out.Buckets += uint64(joinBuckets + identityBuckets)
	out.Indexes += uint64(factIndexes)
	out.HighWater += uint64(betaTokenMemoryHighWater(memory))
	out.Bytes += betaTokenMemoryRetainedBytes(memory)
}

func betaTokenMemoryHighWater(memory tokenHashMemory) int {
	highWater := cap(memory.rows)
	highWater += cap(memory.rowHandles)
	highWater += cap(memory.freeRowHandles)
	highWater += cap(memory.indexes.entries)
	highWater += cap(memory.indexes.touched)
	highWater += cap(memory.identityRows.entries)
	highWater += cap(memory.identityRows.touched)
	highWater += cap(memory.factRows.entries)
	highWater += cap(memory.factRows.touched)
	highWater += cap(memory.bucketRestFree)
	for _, rest := range memory.bucketRestFree {
		highWater += cap(rest)
	}
	return highWater
}

func betaTokenMemoryRetainedBytes(memory tokenHashMemory) uint64 {
	var bytes uint64
	bytes += sliceBytes[graphTokenRow](cap(memory.rows))
	bytes += sliceBytes[graphTokenRowHandleEntry](cap(memory.rowHandles))
	bytes += sliceBytes[graphTokenRowHandleID](cap(memory.freeRowHandles))
	bytes += betaJoinBucketTableBytes(memory.indexes)
	bytes += graphTokenIdentityBucketTableBytes(memory.identityRows)
	bytes += factTokenBucketTableBytes(memory.factRows)
	bytes += bucketRestFreeBytes(memory.bucketRestFree)
	return bytes
}

func betaJoinBucketTableBytes(table betaJoinTokenBucketTable) uint64 {
	var bytes uint64
	bytes += sliceBytes[betaJoinTokenBucketEntry](cap(table.entries))
	bytes += sliceBytes[int](cap(table.touched))
	for i := range table.entries {
		if table.entries[i].state == graphTokenBucketFull {
			bytes += sliceBytes[graphTokenRowID](cap(table.entries[i].bucket.rest))
		}
	}
	return bytes
}

func graphTokenIdentityBucketTableBytes(table graphTokenIdentityBucketTable) uint64 {
	var bytes uint64
	bytes += sliceBytes[graphTokenIdentityBucketEntry](cap(table.entries))
	bytes += sliceBytes[int](cap(table.touched))
	for i := range table.entries {
		if table.entries[i].state == graphTokenBucketFull {
			bytes += sliceBytes[graphTokenRowID](cap(table.entries[i].bucket.rest))
		}
	}
	return bytes
}

func factTokenBucketTableBytes(table factTokenBucketTable) uint64 {
	var bytes uint64
	bytes += sliceBytes[factTokenBucketEntry](cap(table.entries))
	bytes += sliceBytes[int](cap(table.touched))
	for i := range table.entries {
		if table.entries[i].state == graphTokenBucketFull {
			bytes += sliceBytes[graphTokenRowID](cap(table.entries[i].bucket.rest))
		}
	}
	return bytes
}

func bucketRestFreeBytes(rests [][]graphTokenRowID) uint64 {
	bytes := sliceBytes[[]graphTokenRowID](cap(rests))
	for _, rest := range rests {
		bytes += sliceBytes[graphTokenRowID](cap(rest))
	}
	return bytes
}

func alphaFactSetRows(sets []reteGraphAlphaFactSet) int {
	rows := 0
	for i := range sets {
		for _, id := range sets[i].inline {
			if !id.IsZero() {
				rows++
			}
		}
		rows += len(sets[i].overflow)
	}
	return rows
}

func alphaFactSetOverflowBuckets(sets []reteGraphAlphaFactSet) int {
	buckets := 0
	for i := range sets {
		if len(sets[i].overflow) == 0 {
			continue
		}
		buckets++
	}
	return buckets
}

func alphaConditionIndexCount(conditions [][]ConditionID) int {
	indexes := 0
	for _, conditionIDs := range conditions {
		if len(conditionIDs) > 0 {
			indexes++
		}
	}
	return indexes
}

func alphaMemoryHighWater(m *reteGraphBetaMemory) int {
	if m == nil {
		return 0
	}
	highWater := cap(m.alphaFacts)
	highWater += cap(m.alphaConditions)
	highWater += cap(m.alphaFactOwnershipIDs)
	highWater += cap(m.alphaFactRouteStorage)
	highWater += cap(m.alphaFactTerminalStorage)
	highWater += cap(m.alphaFactBetaStorage)
	for _, facts := range m.factsByName {
		highWater += cap(facts)
	}
	for _, facts := range m.factsByTemplate {
		highWater += cap(facts)
	}
	for _, facts := range m.factFieldEqualIndexes {
		highWater += cap(facts)
	}
	return highWater
}

func alphaMemoryRetainedBytes(m *reteGraphBetaMemory) uint64 {
	if m == nil {
		return 0
	}
	var bytes uint64
	bytes += sliceBytes[reteGraphAlphaFactSet](cap(m.alphaFacts))
	bytes += sliceBytes[[]ConditionID](cap(m.alphaConditions))
	for _, conditionIDs := range m.alphaConditions {
		bytes += sliceBytes[ConditionID](cap(conditionIDs))
	}
	bytes += mapEntryBytes[FactID, alphaFactOwnershipRow](len(m.alphaFactOwnership))
	bytes += sliceBytes[FactID](cap(m.alphaFactOwnershipIDs))
	bytes += sliceBytes[reteGraphAlphaNodeID](cap(m.alphaFactRouteStorage))
	bytes += sliceBytes[generatedTerminalRowHandle](cap(m.alphaFactTerminalStorage))
	bytes += sliceBytes[generatedBetaRowHandle](cap(m.alphaFactBetaStorage))
	bytes += mapEntryBytes[ConditionID, int](len(m.alphaFactCounts))

	for i := range m.alphaFacts {
		bytes += mapEntryBytes[FactID, struct{}](len(m.alphaFacts[i].overflow))
	}
	bytes += snapshotIndexMapBytes[string](m.factsByName)
	bytes += snapshotIndexMapBytes[TemplateKey](m.factsByTemplate)
	bytes += snapshotIndexMapBytes[factFieldEqualKey](m.factFieldEqualIndexes)
	bytes += mapEntryBytes[FactID, int](len(m.factNameIndexes))
	bytes += mapEntryBytes[FactID, int](len(m.factTemplateIndexes))
	return bytes
}

func snapshotIndexMapBytes[K comparable](values map[K][]FactSnapshot) uint64 {
	bytes := mapEntryBytes[K, []FactSnapshot](len(values))
	for _, facts := range values {
		bytes += sliceBytes[FactSnapshot](cap(facts))
	}
	return bytes
}

func sliceBytes[T any](capacity int) uint64 {
	if capacity <= 0 {
		return 0
	}
	var zero T
	return uint64(capacity) * uint64(unsafe.Sizeof(zero))
}

func mapEntryBytes[K comparable, V any](entries int) uint64 {
	if entries <= 0 {
		return 0
	}
	var key K
	var value V
	return uint64(entries) * uint64(unsafe.Sizeof(key)+unsafe.Sizeof(value))
}
