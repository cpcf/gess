package engine

import (
	"context"
	"reflect"
	"unsafe"
)

const runtimeMemoryOwnerAlpha = "alpha"
const runtimeMemoryOwnerBeta = "beta"
const runtimeMemoryOwnerRuleTerminal = "rule-terminal"
const runtimeMemoryOwnerQueryTerminal = "query-terminal"
const runtimeMemoryOwnerAgenda = "agenda"

// RuntimeDiagnostics reports retained runtime memory owners for diagnostics.
type RuntimeDiagnostics struct {
	MemoryOwners []RuntimeMemoryOwnerDiagnostics
}

// RuntimeMemoryOwnerDiagnostics summarizes one Rete/runtime memory owner.
type RuntimeMemoryOwnerDiagnostics struct {
	Owner      string
	Rows       uint64
	Buckets    uint64
	Indexes    uint64
	Tombstones uint64
	Bytes      uint64
	HighWater  uint64
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
	for _, owner := range s.rete.graphBeta.terminalMemoryOwnerDiagnostics() {
		if owner.Owner != "" {
			owners = append(owners, owner)
		}
	}
	if owner := s.agenda.agendaMemoryOwnerDiagnostics(); owner.Owner != "" {
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

func (m *reteGraphBetaMemory) terminalMemoryOwnerDiagnostics() []RuntimeMemoryOwnerDiagnostics {
	if m == nil || m.graph == nil {
		return nil
	}
	owners := map[string]*RuntimeMemoryOwnerDiagnostics{
		runtimeMemoryOwnerRuleTerminal:  {Owner: runtimeMemoryOwnerRuleTerminal},
		runtimeMemoryOwnerQueryTerminal: {Owner: runtimeMemoryOwnerQueryTerminal},
	}
	for _, node := range m.graph.terminalNodes {
		terminal := m.terminalAt(node.id)
		if terminal == nil {
			continue
		}
		ownerName := runtimeMemoryOwnerRuleTerminal
		if node.kind == reteGraphTerminalQuery {
			ownerName = runtimeMemoryOwnerQueryTerminal
		}
		addTerminalMemoryOwnerDiagnostics(owners[ownerName], terminal.rows)
	}
	out := make([]RuntimeMemoryOwnerDiagnostics, 0, len(owners))
	for _, ownerName := range []string{runtimeMemoryOwnerRuleTerminal, runtimeMemoryOwnerQueryTerminal} {
		owner := owners[ownerName]
		if owner == nil || (owner.Rows == 0 && owner.Buckets == 0 && owner.Indexes == 0 && owner.Bytes == 0 && owner.HighWater == 0) {
			continue
		}
		out = append(out, *owner)
	}
	return out
}

func addTerminalMemoryOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, memory any) {
	if out == nil {
		return
	}
	type tokenMemoryDiagnostics interface {
		diagnostics() reteGraphTokenMemoryDiagnostics
	}
	diagnostics, ok := memory.(tokenMemoryDiagnostics)
	if !ok {
		return
	}
	stats := diagnostics.diagnostics()
	out.Rows += uint64(stats.Rows)
	out.Buckets += uint64(stats.IdentityIndexKeys)
	out.Indexes += uint64(stats.FactIndexKeys)
	out.HighWater += uint64(reflectTokenMemoryHighWater(memory))
	out.Bytes += reflectTokenMemoryRetainedBytes(memory)
}

func (a *agenda) agendaMemoryOwnerDiagnostics() RuntimeMemoryOwnerDiagnostics {
	if a == nil {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	out := RuntimeMemoryOwnerDiagnostics{Owner: runtimeMemoryOwnerAgenda}
	out.Rows = uint64(a.activationRows.count)
	out.Buckets = uint64(len(a.activations))
	out.Indexes = uint64(len(a.byFactID) + len(a.byRevision))
	out.Tombstones = uint64(a.consumedActivationRows())
	out.HighWater = uint64(agendaMemoryHighWater(a))
	out.Bytes = agendaMemoryRetainedBytes(a)
	if out.Rows == 0 && out.Buckets == 0 && out.Indexes == 0 && out.Tombstones == 0 && out.Bytes == 0 && out.HighWater == 0 {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	return out
}

func (a *agenda) consumedActivationRows() int {
	if a == nil {
		return 0
	}
	consumed := 0
	for _, chunk := range a.activationRows.chunks {
		for i := range chunk {
			if chunk[i].status == activationStatusConsumed {
				consumed++
			}
		}
	}
	return consumed
}

func agendaMemoryHighWater(a *agenda) int {
	if a == nil {
		return 0
	}
	highWater := cap(a.activationRows.chunks)
	for _, chunk := range a.activationRows.chunks {
		highWater += cap(chunk)
	}
	highWater += cap(a.pending)
	highWater += cap(a.pendingActivation)
	highWater += cap(a.reconcileNextPending)
	highWater += cap(a.reconcileChanges)
	highWater += cap(a.reconcileActivated)
	highWater += cap(a.deltaNextPending)
	highWater += cap(a.deltaChanges)
	highWater += cap(a.deltaActivated)
	highWater += cap(a.purgeNextPending)
	highWater += cap(a.purgeChanges)
	highWater += cap(a.sortEntries)
	for _, bucket := range a.activations {
		highWater += cap(bucket.overflow)
	}
	for _, bucket := range a.byFactID {
		highWater += cap(bucket.overflow)
	}
	for _, bucket := range a.byRevision {
		highWater += cap(bucket.overflow)
	}
	for _, bucket := range a.purgeActivations {
		highWater += cap(bucket.overflow)
	}
	return highWater
}

func agendaMemoryRetainedBytes(a *agenda) uint64 {
	if a == nil {
		return 0
	}
	var bytes uint64
	bytes += sliceBytes[[]activation](cap(a.activationRows.chunks))
	for _, chunk := range a.activationRows.chunks {
		bytes += sliceBytes[activation](cap(chunk))
		for i := range chunk {
			bytes += activationPayloadBytes(chunk[i].payload)
		}
	}
	bytes += mapEntryBytes[activationFingerprint, activationBucket](len(a.activations))
	for _, bucket := range a.activations {
		bytes += sliceBytes[*activation](cap(bucket.overflow))
	}
	bytes += sliceBytes[activationKey](cap(a.pending))
	bytes += sliceBytes[*activation](cap(a.pendingActivation))
	bytes += mapEntryBytes[FactID, activationKeyBucket](len(a.byFactID))
	for _, bucket := range a.byFactID {
		bytes += sliceBytes[activationKey](cap(bucket.overflow))
	}
	bytes += mapEntryBytes[RuleRevisionID, activationKeyBucket](len(a.byRevision))
	for _, bucket := range a.byRevision {
		bytes += sliceBytes[activationKey](cap(bucket.overflow))
	}
	bytes += mapEntryBytes[activationKey, struct{}](len(a.reconcileSeen))
	bytes += sliceBytes[activationKey](cap(a.reconcileNextPending))
	bytes += sliceBytes[agendaChange](cap(a.reconcileChanges))
	bytes += sliceBytes[agendaChange](cap(a.reconcileActivated))
	bytes += mapEntryBytes[activationKey, struct{}](len(a.deltaRemovedKeys))
	bytes += sliceBytes[activationKey](cap(a.deltaNextPending))
	bytes += sliceBytes[agendaChange](cap(a.deltaChanges))
	bytes += sliceBytes[agendaChange](cap(a.deltaActivated))
	bytes += mapEntryBytes[activationFingerprint, activationBucket](len(a.purgeActivations))
	for _, bucket := range a.purgeActivations {
		bytes += sliceBytes[*activation](cap(bucket.overflow))
	}
	bytes += sliceBytes[activationKey](cap(a.purgeNextPending))
	bytes += sliceBytes[agendaChange](cap(a.purgeChanges))
	bytes += sliceBytes[activationSortEntry](cap(a.sortEntries))
	return bytes
}

func activationPayloadBytes(payload *activationPayload) uint64 {
	if payload == nil {
		return 0
	}
	var bytes uint64
	bytes += uint64(unsafe.Sizeof(*payload))
	bytes += sliceBytes[bindingTupleEntry](cap(payload.bindings))
	bytes += sliceBytes[int](cap(payload.path))
	bytes += sliceBytes[FactID](cap(payload.factIDs))
	bytes += sliceBytes[FactVersion](cap(payload.factVersions))
	return bytes
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

// Terminal memory is mid-rewrite in active worktrees, so byte estimates inspect
// the shared field names instead of depending on one concrete row type.
func reflectTokenMemoryHighWater(memory any) int {
	value := reflect.ValueOf(memory)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0
	}
	highWater := 0
	for _, name := range []string{"rows", "rowHandles", "freeRowHandles", "bucketRestFree"} {
		highWater += reflectSliceCap(value.FieldByName(name))
	}
	for _, name := range []string{"indexes", "identityRows", "factRows"} {
		table := value.FieldByName(name)
		if !table.IsValid() {
			continue
		}
		highWater += reflectSliceCap(table.FieldByName("entries"))
		highWater += reflectSliceCap(table.FieldByName("touched"))
	}
	rests := value.FieldByName("bucketRestFree")
	if rests.IsValid() && rests.Kind() == reflect.Slice {
		for i := 0; i < rests.Len(); i++ {
			highWater += reflectSliceCap(rests.Index(i))
		}
	}
	return highWater
}

func reflectTokenMemoryRetainedBytes(memory any) uint64 {
	value := reflect.ValueOf(memory)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0
	}
	var bytes uint64
	for _, name := range []string{"rows", "rowHandles", "freeRowHandles", "bucketRestFree"} {
		bytes += reflectSliceBytes(value.FieldByName(name))
	}
	for _, name := range []string{"indexes", "identityRows", "factRows"} {
		bytes += reflectBucketTableBytes(value.FieldByName(name))
	}
	rests := value.FieldByName("bucketRestFree")
	if rests.IsValid() && rests.Kind() == reflect.Slice {
		for i := 0; i < rests.Len(); i++ {
			bytes += reflectSliceBytes(rests.Index(i))
		}
	}
	return bytes
}

func reflectBucketTableBytes(table reflect.Value) uint64 {
	if !table.IsValid() {
		return 0
	}
	if table.Kind() == reflect.Pointer {
		if table.IsNil() {
			return 0
		}
		table = table.Elem()
	}
	if table.Kind() != reflect.Struct {
		return 0
	}
	var bytes uint64
	entries := table.FieldByName("entries")
	bytes += reflectSliceBytes(entries)
	bytes += reflectSliceBytes(table.FieldByName("touched"))
	if !entries.IsValid() || entries.Kind() != reflect.Slice {
		return bytes
	}
	for i := 0; i < entries.Len(); i++ {
		entry := entries.Index(i)
		if !reflectBucketEntryFull(entry) {
			continue
		}
		bucket := entry.FieldByName("bucket")
		if !bucket.IsValid() || bucket.Kind() != reflect.Struct {
			continue
		}
		bytes += reflectSliceBytes(bucket.FieldByName("rest"))
	}
	return bytes
}

func reflectBucketEntryFull(entry reflect.Value) bool {
	if !entry.IsValid() || entry.Kind() != reflect.Struct {
		return false
	}
	state := entry.FieldByName("state")
	if !state.IsValid() {
		return false
	}
	switch state.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return state.Uint() == uint64(graphTokenBucketFull)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return state.Int() == int64(graphTokenBucketFull)
	default:
		return false
	}
}

func reflectSliceCap(value reflect.Value) int {
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return 0
	}
	return value.Cap()
}

func reflectSliceBytes(value reflect.Value) uint64 {
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return 0
	}
	return uint64(value.Cap()) * uint64(value.Type().Elem().Size())
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
