package engine

import (
	"context"
	"reflect"
	"unsafe"
)

const runtimeMemoryOwnerAlpha = "alpha"
const runtimeMemoryOwnerBeta = "beta"
const runtimeMemoryOwnerFact = "fact"
const runtimeMemoryOwnerRuleTerminal = "rule-terminal"
const runtimeMemoryOwnerQueryTerminal = "query-terminal"
const runtimeMemoryOwnerAgenda = "agenda"
const runtimeMemoryOwnerAggregate = "aggregate"

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
	owners := make([]RuntimeMemoryOwnerDiagnostics, 0, 7)
	if owner := factWorkspaceMemoryOwnerDiagnostics(s.activeFactWorkspace()); owner.Owner != "" {
		owners = append(owners, owner)
	}
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
	if owner := s.rete.graphBeta.aggregateMemoryOwnerDiagnostics(); owner.Owner != "" {
		owners = append(owners, owner)
	}
	return RuntimeDiagnostics{MemoryOwners: owners}
}

func factWorkspaceMemoryOwnerDiagnostics(workspace any) RuntimeMemoryOwnerDiagnostics {
	value := reflectDerefValue(reflect.ValueOf(workspace))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	out := RuntimeMemoryOwnerDiagnostics{Owner: runtimeMemoryOwnerFact}

	facts := value.FieldByName("facts")
	if facts.IsValid() && facts.Kind() == reflect.Slice {
		out.Rows += uint64(facts.Len())
		out.HighWater += uint64(facts.Cap())
		out.Bytes += reflectSliceBytes(facts)
		for i := 0; i < facts.Len(); i++ {
			addWorkingFactDynamicOwnerDiagnostics(&out, facts.Index(i))
		}
	}

	for _, name := range []string{"insertionOrder", "factsBySequence", "slotStorage"} {
		slice := value.FieldByName(name)
		out.HighWater += uint64(reflectSliceCap(slice))
		out.Bytes += reflectSliceBytes(slice)
	}
	compactStore := reflectDerefValue(value.FieldByName("compactSlotStore"))
	if compactStore.IsValid() && compactStore.Kind() == reflect.Struct {
		slice := compactStore.FieldByName("slots")
		out.HighWater += uint64(reflectSliceCap(slice))
		out.Bytes += reflectSliceBytes(slice)
	}
	out.Indexes += uint64(reflectSliceLen(value.FieldByName("factsBySequence")))

	for _, name := range []string{"factsByID", "factsByTemplate", "factsByName"} {
		addFactMapOwnerDiagnostics(&out, value.FieldByName(name))
	}

	duplicates := value.FieldByName("factsByDuplicate")
	out.Indexes += uint64(reflectContainerEntryCount(duplicates, 0))
	out.HighWater += uint64(reflectContainerHighWater(duplicates, 0))
	out.Bytes += reflectContainerRetainedBytes(duplicates, 0)

	if out.Rows == 0 && out.Buckets == 0 && out.Indexes == 0 && out.Bytes == 0 && out.HighWater == 0 {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	return out
}

func addFactMapOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, value reflect.Value) {
	if out == nil || !value.IsValid() || value.Kind() != reflect.Map {
		return
	}
	out.Buckets += uint64(value.Len())
	out.Indexes += uint64(reflectMapIndexEntries(value))
	out.HighWater += uint64(reflectMapHighWater(value))
	out.Bytes += reflectMapRetainedBytes(value)
}

func addWorkingFactDynamicOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, fact reflect.Value) {
	if out == nil {
		return
	}
	fact = reflectDerefValue(fact)
	if !fact.IsValid() || fact.Kind() != reflect.Struct {
		return
	}
	for _, name := range []string{"fields", "fieldPresence"} {
		field := fact.FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.Map {
			continue
		}
		out.HighWater += uint64(reflectMapHighWater(field))
		out.Bytes += reflectMapRetainedBytes(field)
	}
	if duplicate := fact.FieldByName("dupIndex"); duplicate.IsValid() && duplicate.Kind() == reflect.Pointer && !duplicate.IsNil() {
		out.HighWater++
		out.Bytes += uint64(duplicate.Type().Elem().Size())
	}
}

func (m *reteGraphBetaMemory) alphaMemoryOwnerDiagnostics() RuntimeMemoryOwnerDiagnostics {
	if m == nil {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	var out RuntimeMemoryOwnerDiagnostics
	out.Owner = runtimeMemoryOwnerAlpha

	out.Rows += uint64(alphaFactSetRows(m.alpha.facts))
	out.Rows += uint64(len(m.alpha.factOwnership))
	out.Rows += uint64(len(m.alpha.factRouteStorage))
	out.Rows += uint64(len(m.alpha.factTerminalStorage))
	out.Rows += uint64(len(m.alpha.factBetaStorage))

	alphaOverflowBuckets := alphaFactSetOverflowBuckets(m.alpha.facts)
	out.Buckets += uint64(alphaOverflowBuckets)
	out.Buckets += uint64(len(m.alpha.factCounts))
	out.Buckets += uint64(len(m.factRefsByName))
	out.Buckets += uint64(len(m.factRefsByTemplate))
	out.Buckets += uint64(len(m.factFieldEqualRefs))

	out.Indexes += uint64(alphaConditionIndexCount(m.alpha.conditions))
	out.Indexes += uint64(len(m.alpha.factCounts))
	out.Indexes += uint64(len(m.factRefsByName))
	out.Indexes += uint64(len(m.factRefsByTemplate))
	out.Indexes += uint64(len(m.factFieldEqualRefs))

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
		addBetaSideMemoryOwnerDiagnostics(&out, node.left)
		addBetaSideMemoryOwnerDiagnostics(&out, node.right)
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
			addTerminalMemoryOwnerDiagnostics(owners[ownerName], terminal.queryRows)
			continue
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
	out.Rows = uint64(a.agendaActivationEntryCount())
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

func (m *reteGraphBetaMemory) aggregateMemoryOwnerDiagnostics() RuntimeMemoryOwnerDiagnostics {
	if m == nil {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	out := RuntimeMemoryOwnerDiagnostics{Owner: runtimeMemoryOwnerAggregate}
	for _, memory := range m.aggregates {
		if memory == nil {
			continue
		}
		addAggregateNodeMemoryOwnerDiagnostics(&out, memory)
	}
	if out.Rows == 0 && out.Buckets == 0 && out.Indexes == 0 && out.Tombstones == 0 && out.Bytes == 0 && out.HighWater == 0 {
		return RuntimeMemoryOwnerDiagnostics{}
	}
	return out
}

func addAggregateNodeMemoryOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, memory *reteGraphAggregateNodeMemory) {
	if out == nil || memory == nil {
		return
	}
	out.Buckets += uint64(memory.bucketCount())
	out.HighWater += uint64(len(memory.buckets.rows) + cap(memory.buckets.free))
	out.Bytes += mapEntryBytes[graphTokenIdentityKey, reteGraphAggregateBucketID](len(memory.buckets.ids))
	out.Bytes += sliceBytes[reteGraphAggregateBucket](cap(memory.buckets.rows))
	out.Bytes += sliceBytes[reteGraphAggregateBucketID](cap(memory.buckets.free))
	out.HighWater += uint64(cap(memory.numeric.intSums))
	out.HighWater += uint64(cap(memory.numeric.floatSums))
	out.HighWater += uint64(cap(memory.numeric.floaty))
	out.Bytes += sliceBytes[int64](cap(memory.numeric.intSums))
	out.Bytes += sliceBytes[float64](cap(memory.numeric.floatSums))
	out.Bytes += sliceBytes[bool](cap(memory.numeric.floaty))
	memory.forEachBucket(func(bucket *reteGraphAggregateBucket) {
		addAggregateBucketOwnerDiagnostics(out, bucket)
	})
	for _, id := range memory.buckets.free {
		bucket := memory.buckets.bucketByID(id)
		if bucket == nil {
			continue
		}
		addAggregateBucketOwnerDiagnostics(out, bucket)
	}
}

func addAggregateBucketOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, bucket *reteGraphAggregateBucket) {
	if out == nil || bucket == nil {
		return
	}
	members := len(bucket.members) + bucket.countOnlyMemberCount()
	resultTokens := 0
	if !bucket.token.isZero() {
		resultTokens = 1
	}
	out.Rows += uint64(1 + members + resultTokens)
	out.Indexes += uint64(len(bucket.members))
	out.HighWater += uint64(1 + members + resultTokens)
	out.HighWater += uint64(cap(bucket.countOnlyRest))
	out.HighWater += uint64(cap(bucket.extrema))
	out.HighWater += uint64(cap(bucket.collects))
	out.HighWater += uint64(cap(bucket.values))
	out.Bytes += mapEntryBytes[graphTokenIdentityKey, reteGraphAggregateMember](len(bucket.members))
	out.Bytes += sliceBytes[tokenRef](cap(bucket.countOnlyRest))
	out.Bytes += sliceBytes[reteGraphAggregateExtremum](cap(bucket.extrema))
	for _, extremum := range bucket.extrema {
		out.Bytes += mapEntryBytes[string, reteGraphAggregateExtremumValue](len(extremum.values))
	}
	out.Bytes += sliceBytes[[]reteGraphAggregateCollectEntry](cap(bucket.collects))
	for _, collect := range bucket.collects {
		out.HighWater += uint64(cap(collect))
		out.Bytes += sliceBytes[reteGraphAggregateCollectEntry](cap(collect))
	}
	out.Bytes += sliceBytes[Value](cap(bucket.values))
}

func (a *agenda) consumedActivationRows() int {
	if a == nil {
		return 0
	}
	consumed := 0
	forEachAgendaActivation(a.activations, func(current *activation) {
		if current.status == activationStatusConsumed {
			consumed++
		}
	})
	return consumed
}

func (a *agenda) agendaActivationEntryCount() int {
	if a == nil {
		return 0
	}
	count := 0
	forEachAgendaActivation(a.activations, func(*activation) {
		count++
	})
	return count
}

func forEachAgendaActivation(buckets map[activationFingerprint]activationBucket, fn func(*activation)) {
	if fn == nil {
		return
	}
	for _, bucket := range buckets {
		if bucket.first != nil {
			fn(bucket.first)
		}
		if bucket.second != nil {
			fn(bucket.second)
		}
		for _, current := range bucket.overflow {
			if current != nil {
				fn(current)
			}
		}
	}
}

func (a *agenda) activationStoredInRows(act *activation) bool {
	if a == nil || act == nil {
		return false
	}
	for _, chunk := range a.activationRows.chunks {
		for i := range chunk {
			if &chunk[i] == act {
				return true
			}
		}
	}
	return false
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
	forEachAgendaActivation(a.activations, func(current *activation) {
		if current == nil || a.activationStoredInRows(current) {
			return
		}
		bytes += uint64(unsafe.Sizeof(*current))
		bytes += activationPayloadBytes(current.payload)
	})
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

func addBetaSideMemoryOwnerDiagnostics(out *RuntimeMemoryOwnerDiagnostics, memory betaSideMemory) {
	if out == nil {
		return
	}
	joinBuckets := memory.indexes.keyCount()
	identityBuckets := memory.identityRows.keyCount()
	factIndexes := memory.factIndexKeyCount()

	out.Rows += uint64(len(memory.rows))
	out.Buckets += uint64(joinBuckets + identityBuckets)
	out.Indexes += uint64(factIndexes)
	out.HighWater += uint64(betaSideMemoryHighWater(memory))
	out.Bytes += betaSideMemoryRetainedBytes(memory)
}

func betaSideMemoryHighWater(memory betaSideMemory) int {
	highWater := cap(memory.rows)
	highWater += cap(memory.rowHandles)
	highWater += cap(memory.freeRowHandles)
	highWater += cap(memory.indexes.heads)
	highWater += cap(memory.indexes.tails)
	highWater += cap(memory.indexes.touched)
	highWater += cap(memory.identityRows.heads)
	highWater += cap(memory.identityRows.touched)
	highWater += cap(memory.factRows.entries)
	highWater += cap(memory.factRows.touched)
	highWater += cap(memory.factLinks)
	highWater += cap(memory.freeFactLinks)
	return highWater
}

func betaSideMemoryRetainedBytes(memory betaSideMemory) uint64 {
	var bytes uint64
	bytes += sliceBytes[betaTokenRow](cap(memory.rows))
	bytes += sliceBytes[betaTokenRowHandleEntry](cap(memory.rowHandles))
	bytes += sliceBytes[graphTokenRowHandleID](cap(memory.freeRowHandles))
	bytes += betaJoinHeadTableBytes(memory.indexes)
	bytes += tokenIdentityHeadTableBytes(memory.identityRows)
	bytes += betaFactHeadTableBytes(memory.factRows)
	bytes += sliceBytes[betaFactLinkRow](cap(memory.factLinks))
	bytes += sliceBytes[betaFactLinkID](cap(memory.freeFactLinks))
	return bytes
}

func betaJoinHeadTableBytes(table betaJoinHeadTable) uint64 {
	var bytes uint64
	bytes += sliceBytes[graphTokenRowID](cap(table.heads))
	bytes += sliceBytes[graphTokenRowID](cap(table.tails))
	bytes += sliceBytes[int](cap(table.touched))
	return bytes
}

func tokenIdentityHeadTableBytes(table tokenIdentityHeadTable) uint64 {
	var bytes uint64
	bytes += sliceBytes[graphTokenRowID](cap(table.heads))
	bytes += sliceBytes[int](cap(table.touched))
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

func betaFactHeadTableBytes(table betaFactHeadTable) uint64 {
	var bytes uint64
	bytes += sliceBytes[betaFactHeadEntry](cap(table.entries))
	bytes += sliceBytes[int](cap(table.touched))
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
	for _, name := range []string{"rows", "rowHandles", "freeRowHandles", "freeRowIDs", "bucketRestFree", "factLinks", "freeFactLinks"} {
		highWater += reflectSliceCap(value.FieldByName(name))
	}
	for _, name := range []string{"indexes", "identityRows", "factRows"} {
		table := value.FieldByName(name)
		if !table.IsValid() {
			continue
		}
		highWater += reflectSliceCap(table.FieldByName("entries"))
		highWater += reflectSliceCap(table.FieldByName("heads"))
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
	for _, name := range []string{"rows", "rowHandles", "freeRowHandles", "freeRowIDs", "bucketRestFree", "factLinks", "freeFactLinks"} {
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
	bytes += reflectSliceBytes(table.FieldByName("heads"))
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

func reflectSliceLen(value reflect.Value) int {
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return 0
	}
	return value.Len()
}

func reflectSliceBytes(value reflect.Value) uint64 {
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return 0
	}
	return uint64(value.Cap()) * uint64(value.Type().Elem().Size())
}

func reflectDerefValue(value reflect.Value) reflect.Value {
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func reflectMapIndexEntries(value reflect.Value) int {
	if !value.IsValid() || value.Kind() != reflect.Map {
		return 0
	}
	entries := value.Len()
	iter := value.MapRange()
	for iter.Next() {
		mapValue := iter.Value()
		if mapValue.Kind() == reflect.Slice {
			entries += mapValue.Len()
		}
	}
	return entries
}

func reflectMapHighWater(value reflect.Value) int {
	if !value.IsValid() || value.Kind() != reflect.Map {
		return 0
	}
	highWater := value.Len()
	iter := value.MapRange()
	for iter.Next() {
		mapValue := iter.Value()
		if mapValue.Kind() == reflect.Slice {
			highWater += mapValue.Cap()
		}
	}
	return highWater
}

func reflectMapRetainedBytes(value reflect.Value) uint64 {
	if !value.IsValid() || value.Kind() != reflect.Map {
		return 0
	}
	bytes := uint64(value.Len()) * uint64(value.Type().Key().Size()+value.Type().Elem().Size())
	iter := value.MapRange()
	for iter.Next() {
		mapValue := iter.Value()
		if mapValue.Kind() == reflect.Slice {
			bytes += reflectSliceBytes(mapValue)
		}
	}
	return bytes
}

func reflectContainerEntryCount(value reflect.Value, depth int) int {
	if depth > 4 {
		return 0
	}
	value = reflectDerefValue(value)
	if !value.IsValid() {
		return 0
	}
	switch value.Kind() {
	case reflect.Map:
		entries := value.Len()
		iter := value.MapRange()
		for iter.Next() {
			entries += reflectContainerEntryCount(iter.Value(), depth+1)
		}
		return entries
	case reflect.Slice, reflect.Array:
		entries := value.Len()
		for i := 0; i < value.Len(); i++ {
			entries += reflectContainerEntryCount(value.Index(i), depth+1)
		}
		return entries
	case reflect.Struct:
		entries := 0
		for _, field := range value.Fields() {
			entries += reflectContainerEntryCount(field, depth+1)
		}
		return entries
	default:
		return 0
	}
}

func reflectContainerHighWater(value reflect.Value, depth int) int {
	if depth > 4 {
		return 0
	}
	value = reflectDerefValue(value)
	if !value.IsValid() {
		return 0
	}
	switch value.Kind() {
	case reflect.Map:
		highWater := value.Len()
		iter := value.MapRange()
		for iter.Next() {
			highWater += reflectContainerHighWater(iter.Value(), depth+1)
		}
		return highWater
	case reflect.Slice:
		highWater := value.Cap()
		for i := 0; i < value.Len(); i++ {
			highWater += reflectContainerHighWater(value.Index(i), depth+1)
		}
		return highWater
	case reflect.Array:
		highWater := value.Len()
		for i := 0; i < value.Len(); i++ {
			highWater += reflectContainerHighWater(value.Index(i), depth+1)
		}
		return highWater
	case reflect.Struct:
		highWater := 0
		for _, field := range value.Fields() {
			highWater += reflectContainerHighWater(field, depth+1)
		}
		return highWater
	default:
		return 0
	}
}

func reflectContainerRetainedBytes(value reflect.Value, depth int) uint64 {
	if depth > 4 {
		return 0
	}
	value = reflectDerefValue(value)
	if !value.IsValid() {
		return 0
	}
	switch value.Kind() {
	case reflect.Map:
		bytes := reflectMapRetainedBytes(value)
		iter := value.MapRange()
		for iter.Next() {
			bytes += reflectContainerRetainedBytes(iter.Value(), depth+1)
		}
		return bytes
	case reflect.Slice:
		bytes := reflectSliceBytes(value)
		for i := 0; i < value.Len(); i++ {
			bytes += reflectContainerRetainedBytes(value.Index(i), depth+1)
		}
		return bytes
	case reflect.Array:
		var bytes uint64
		for i := 0; i < value.Len(); i++ {
			bytes += reflectContainerRetainedBytes(value.Index(i), depth+1)
		}
		return bytes
	case reflect.Struct:
		var bytes uint64
		for _, field := range value.Fields() {
			bytes += reflectContainerRetainedBytes(field, depth+1)
		}
		return bytes
	default:
		return 0
	}
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
	highWater := cap(m.alpha.facts)
	highWater += cap(m.alpha.conditions)
	highWater += cap(m.alpha.factOwnershipIDs)
	highWater += cap(m.alpha.factRouteStorage)
	highWater += cap(m.alpha.factTerminalStorage)
	highWater += cap(m.alpha.factBetaStorage)
	for _, refs := range m.factRefsByName {
		highWater += cap(refs)
	}
	for _, refs := range m.factRefsByTemplate {
		highWater += cap(refs)
	}
	for _, refs := range m.factFieldEqualRefs {
		highWater += cap(refs)
	}
	return highWater
}

func alphaMemoryRetainedBytes(m *reteGraphBetaMemory) uint64 {
	if m == nil {
		return 0
	}
	var bytes uint64
	bytes += sliceBytes[reteGraphAlphaFactSet](cap(m.alpha.facts))
	bytes += sliceBytes[[]ConditionID](cap(m.alpha.conditions))
	for _, conditionIDs := range m.alpha.conditions {
		bytes += sliceBytes[ConditionID](cap(conditionIDs))
	}
	bytes += mapEntryBytes[FactID, alphaFactOwnershipRow](len(m.alpha.factOwnership))
	bytes += sliceBytes[FactID](cap(m.alpha.factOwnershipIDs))
	bytes += sliceBytes[reteGraphAlphaNodeID](cap(m.alpha.factRouteStorage))
	bytes += sliceBytes[generatedTerminalRowHandle](cap(m.alpha.factTerminalStorage))
	bytes += sliceBytes[generatedBetaRowHandle](cap(m.alpha.factBetaStorage))
	bytes += mapEntryBytes[ConditionID, int](len(m.alpha.factCounts))

	for i := range m.alpha.facts {
		bytes += sliceBytes[FactID](cap(m.alpha.facts[i].overflow))
	}
	bytes += factRefIndexMapBytes[string](m.factRefsByName)
	bytes += factRefIndexMapBytes[TemplateKey](m.factRefsByTemplate)
	bytes += factRefIndexMapBytes[factFieldEqualKey](m.factFieldEqualRefs)
	bytes += mapEntryBytes[FactID, int](len(m.factNameIndexes))
	bytes += mapEntryBytes[FactID, int](len(m.factTemplateIndexes))
	return bytes
}

func factRefIndexMapBytes[K comparable](values map[K][]factIndexRef) uint64 {
	bytes := mapEntryBytes[K, []factIndexRef](len(values))
	for _, refs := range values {
		bytes += sliceBytes[factIndexRef](cap(refs))
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
