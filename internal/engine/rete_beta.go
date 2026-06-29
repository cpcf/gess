package engine

import (
	"math"
	"slices"
	"strings"
)

type tokenHandle struct {
	arena      *tokenArena
	rowID      tokenArenaRowID
	generation uint64
}

func (h tokenHandle) isZero() bool {
	return h.rowID == 0 || h.generation == 0
}

type tokenRef struct {
	handle tokenHandle
}

func (r tokenRef) isZero() bool {
	return r.handle.isZero()
}

type tokenParentHandle struct {
	rowID tokenArenaRowID
}

func (h tokenParentHandle) isZero() bool {
	return h.rowID == 0
}

type tokenRowEntry struct {
	conditionID ConditionID
	binding     string
	bindingSlot int
	factID      FactID
	factVersion FactVersion
	value       Value
	hasValue    bool
}

type tokenRow struct {
	parent           tokenParentHandle
	conditionID      ConditionID
	binding          string
	bindingSlot      int
	fact             conditionFactRef
	value            Value
	hasValue         bool
	size             int
	factSpanStart    int
	pathLen          int
	maxRecency       Recency
	aggregateRecency Recency
	identityState    uint64
	orderedSlots     bool
}

type tokenArena struct {
	chunks        [][]tokenRow
	factIDs       []FactID
	factVersions  []FactVersion
	count         int
	epoch         uint64
	generation    Generation
	keepFactSpans bool
}

type tokenArenaRowID uint32

func newTokenArena() *tokenArena {
	return &tokenArena{epoch: 1, keepFactSpans: true}
}

func newTokenArenaWithoutFactSpans() *tokenArena {
	return &tokenArena{epoch: 1}
}

func (a *tokenArena) reserve(rowCapacity int) {
	if a == nil || rowCapacity <= 0 {
		return
	}
	chunkCount := (rowCapacity + reteBetaMatchTokenChunkSize - 1) / reteBetaMatchTokenChunkSize
	for len(a.chunks) < chunkCount {
		a.chunks = append(a.chunks, make([]tokenRow, 0, reteBetaMatchTokenChunkSize))
	}
	if !a.keepFactSpans {
		return
	}
	spanCapacity := rowCapacity
	if spanCapacity > cap(a.factIDs) {
		factIDs := make([]FactID, len(a.factIDs), spanCapacity)
		copy(factIDs, a.factIDs)
		a.factIDs = factIDs
	}
	if spanCapacity > cap(a.factVersions) {
		factVersions := make([]FactVersion, len(a.factVersions), spanCapacity)
		copy(factVersions, a.factVersions)
		a.factVersions = factVersions
	}
}

func (a *tokenArena) reset() {
	if a == nil {
		return
	}
	for chunkIndex, chunk := range a.chunks {
		for i := range chunk {
			chunk[i] = tokenRow{}
		}
		a.chunks[chunkIndex] = chunk[:0]
	}
	for i := range a.factIDs {
		a.factIDs[i] = FactID{}
	}
	a.factIDs = a.factIDs[:0]
	for i := range a.factVersions {
		a.factVersions[i] = 0
	}
	a.factVersions = a.factVersions[:0]
	a.count = 0
	a.generation = 0
	a.epoch++
	if a.epoch == 0 {
		a.epoch = 1
	}
}

func (a *tokenArena) add(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
	return a.addCompact(parent, tokenRowEntryForMatch(entry, match), match, recency, generation, len(entry.conditionPath))
}

func (a *tokenArena) addAlphaSource(entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	rowEntry := tokenRowEntry{
		conditionID: entry.conditionID,
		bindingSlot: entry.bindingSlot,
		value:       match.value,
		hasValue:    match.hasValue,
	}
	if rowEntry.conditionID == "" {
		rowEntry.conditionID = match.conditionID
	}
	if match.hasValue {
		rowEntry.binding = entry.binding
	} else {
		rowEntry.factID = match.fact.ID()
		rowEntry.factVersion = match.fact.Version()
	}
	match.conditionID = rowEntry.conditionID
	match.bindingSlot = rowEntry.bindingSlot
	return a.addSourceCompact(rowEntry, match, recency, generation, len(entry.conditionPath))
}

func (a *tokenArena) addCompact(parent tokenRef, entry tokenRowEntry, match conditionMatch, recency Recency, generation Generation, pathStepLen int) tokenRef {
	return a.addCompactInternal(parent, entry, match, recency, generation, pathStepLen)
}

func (a *tokenArena) addCompactSource(parent tokenRef, source tokenRef, entry tokenRowEntry, recency Recency, generation Generation, pathStepLen int) tokenRef {
	if source.isZero() {
		return tokenRef{}
	}
	sourceRow, ok := source.resolve()
	if !ok {
		return tokenRef{}
	}
	match, ok := sourceRow.conditionMatch()
	if !ok {
		return tokenRef{}
	}
	return a.addCompactInternal(parent, entry, match, recency, generation, pathStepLen)
}

func (a *tokenArena) addSourceCompact(entry tokenRowEntry, match conditionMatch, recency Recency, generation Generation, pathStepLen int) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	tokenGeneration := generation
	if tokenGeneration == 0 {
		tokenGeneration = a.generation
	}
	if !a.setGeneration(tokenGeneration) {
		return tokenRef{}
	}
	rowID, chunkIndex := a.nextRowID()
	for len(a.chunks) <= chunkIndex {
		a.chunks = append(a.chunks, make([]tokenRow, 0, reteBetaMatchTokenChunkSize))
	}
	chunk := a.chunks[chunkIndex]
	if len(chunk) < cap(chunk) {
		chunk = chunk[:len(chunk)+1]
	} else {
		chunk = append(chunk, tokenRow{})
	}
	a.chunks[chunkIndex] = chunk
	row := &a.chunks[chunkIndex][len(chunk)-1]
	row.fact = match.fact
	row.size = 1
	row.pathLen = pathStepLen
	row.maxRecency = recency
	row.aggregateRecency = recency
	row.factSpanStart = a.appendFactSpan(nil, match)
	row.setEntry(entry)
	row.identityState = candidateIdentityHashTokenEntryStep(candidateIdentityHashStart(tokenGeneration), entry)
	row.orderedSlots = entry.bindingSlot == 0

	a.count++
	return tokenRef{handle: tokenHandle{arena: a, rowID: rowID, generation: a.epoch}}
}

func (a *tokenArena) addCompactInternal(parent tokenRef, entry tokenRowEntry, match conditionMatch, recency Recency, generation Generation, pathStepLen int) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	var parentRow *tokenRow
	var ok bool
	if !parent.isZero() {
		if parent.handle.arena != nil && parent.handle.arena != a {
			return tokenRef{}
		}
		parentRow, ok = parent.resolve()
		if !ok {
			return tokenRef{}
		}
	}
	tokenGeneration := generation
	if tokenGeneration == 0 && parentRow != nil {
		tokenGeneration = parent.generation()
	}
	if tokenGeneration == 0 {
		tokenGeneration = a.generation
	}
	if !a.setGeneration(tokenGeneration) {
		return tokenRef{}
	}
	rowID, chunkIndex := a.nextRowID()
	for len(a.chunks) <= chunkIndex {
		a.chunks = append(a.chunks, make([]tokenRow, 0, reteBetaMatchTokenChunkSize))
	}
	chunk := a.chunks[chunkIndex]
	if len(chunk) < cap(chunk) {
		chunk = chunk[:len(chunk)+1]
	} else {
		chunk = append(chunk, tokenRow{})
	}
	a.chunks[chunkIndex] = chunk
	row := &a.chunks[chunkIndex][len(chunk)-1]
	row.fact = match.fact

	if parentRow != nil {
		row.parent = tokenParentHandle{rowID: parent.handle.rowID}
		row.size = parentRow.size + 1
		row.pathLen = parentRow.pathLen + pathStepLen
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.aggregateRecency = addRecency(parentRow.aggregateRecency, recency)
		row.identityState = parentRow.identityState
		row.orderedSlots = parentRow.orderedSlots && entry.bindingSlot == parentRow.size
	} else {
		row.size = 1
		row.pathLen = pathStepLen
		row.maxRecency = recency
		row.aggregateRecency = recency
		row.identityState = candidateIdentityHashStart(tokenGeneration)
		row.orderedSlots = entry.bindingSlot == 0
	}
	row.factSpanStart = a.appendFactSpan(parentRow, match)
	row.setEntry(entry)
	row.identityState = candidateIdentityHashTokenEntryStep(row.identityState, entry)

	a.count++
	handle := tokenHandle{arena: a, rowID: rowID, generation: a.epoch}
	return tokenRef{handle: handle}
}

func (a *tokenArena) nextRowID() (tokenArenaRowID, int) {
	if a == nil {
		return 0, 0
	}
	rowNumber := a.count + 1
	if rowNumber <= 0 || rowNumber > math.MaxUint32 {
		return 0, 0
	}
	return tokenArenaRowID(rowNumber), a.count / reteBetaMatchTokenChunkSize
}

func (a *tokenArena) setGeneration(generation Generation) bool {
	if a == nil {
		return false
	}
	if a.generation == 0 {
		a.generation = generation
		return true
	}
	return generation == 0 || a.generation == generation
}

func (r *tokenRow) conditionMatch() (conditionMatch, bool) {
	if r == nil {
		return conditionMatch{}, false
	}
	return conditionMatch{
		conditionID: r.conditionID,
		bindingSlot: r.bindingSlot,
		fact:        r.fact,
		value:       r.value,
		hasValue:    r.hasValue,
	}, true
}

func (r *tokenRow) setEntry(entry tokenRowEntry) {
	if r == nil {
		return
	}
	r.conditionID = entry.conditionID
	r.binding = entry.binding
	r.bindingSlot = entry.bindingSlot
	r.value = entry.value
	r.hasValue = entry.hasValue
}

func (r *tokenRow) tokenRowEntry() tokenRowEntry {
	if r == nil {
		return tokenRowEntry{}
	}
	out := tokenRowEntry{
		conditionID: r.conditionID,
		binding:     r.binding,
		bindingSlot: r.bindingSlot,
		value:       r.value,
		hasValue:    r.hasValue,
	}
	if !r.hasValue {
		out.factID = r.fact.ID()
		out.factVersion = r.fact.Version()
	}
	return out
}

func tokenRowEntryForMatch(entry bindingTupleEntry, match conditionMatch) tokenRowEntry {
	out := tokenRowEntry{
		conditionID: entry.conditionID,
		bindingSlot: entry.bindingSlot,
		value:       match.value,
		hasValue:    match.hasValue,
	}
	if out.conditionID == "" {
		out.conditionID = match.conditionID
	}
	if match.hasValue {
		out.binding = entry.binding
		return out
	}
	out.factID = match.fact.ID()
	out.factVersion = match.fact.Version()
	return out
}

func candidateIdentityHashTokenEntryStep(hash uint64, entry tokenRowEntry) uint64 {
	if entry.hasValue {
		return candidateIdentityHashValueStep(hash, entry.binding, entry.value)
	}
	return candidateIdentityHashFactStep(hash, entry.factID, entry.factVersion)
}

func tokenRowPathStepLen(token tokenRef, row *tokenRow) (int, bool) {
	if row == nil {
		return 0, false
	}
	parent := token.parent()
	if parent.isZero() {
		return row.pathLen, true
	}
	parentRow, ok := parent.resolve()
	if !ok {
		return 0, false
	}
	stepLen := row.pathLen - parentRow.pathLen
	if stepLen < 0 {
		return 0, false
	}
	return stepLen, true
}

func (a *tokenArena) appendFactSpan(parent *tokenRow, match conditionMatch) int {
	if a == nil || !a.keepFactSpans {
		return -1
	}
	start := len(a.factIDs)
	if parent != nil {
		parentEnd := parent.factSpanStart + parent.size
		if parent.factSpanStart < 0 || parentEnd > len(a.factIDs) || parentEnd > len(a.factVersions) {
			return -1
		}
		a.factIDs = append(a.factIDs, a.factIDs[parent.factSpanStart:parentEnd]...)
		a.factVersions = append(a.factVersions, a.factVersions[parent.factSpanStart:parentEnd]...)
	}
	var id FactID
	var version FactVersion
	if !match.hasValue {
		id = match.fact.ID()
		version = match.fact.Version()
	}
	a.factIDs = append(a.factIDs, id)
	a.factVersions = append(a.factVersions, version)
	return start
}

func (a *tokenArena) addSeed(generation Generation) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	if !a.setGeneration(generation) {
		return tokenRef{}
	}
	rowID, chunkIndex := a.nextRowID()
	for len(a.chunks) <= chunkIndex {
		a.chunks = append(a.chunks, make([]tokenRow, 0, reteBetaMatchTokenChunkSize))
	}
	chunk := a.chunks[chunkIndex]
	if len(chunk) < cap(chunk) {
		chunk = chunk[:len(chunk)+1]
	} else {
		chunk = append(chunk, tokenRow{})
	}
	a.chunks[chunkIndex] = chunk
	row := &a.chunks[chunkIndex][len(chunk)-1]
	row.factSpanStart = -1
	row.identityState = candidateIdentityHashStart(generation)
	row.orderedSlots = true
	a.count++
	return tokenRef{handle: tokenHandle{arena: a, rowID: rowID, generation: a.epoch}}
}

func (a *tokenArena) resolve(handle tokenHandle) (*tokenRow, bool) {
	if a == nil || handle.isZero() {
		return nil, false
	}
	if handle.arena != nil && handle.arena != a {
		return nil, false
	}
	if handle.generation != a.epoch {
		return nil, false
	}
	return a.rowByID(handle.rowID)
}

func (a *tokenArena) rowCount() int {
	if a == nil {
		return 0
	}
	return a.count
}

func (a *tokenArena) rowByID(id tokenArenaRowID) (*tokenRow, bool) {
	if a == nil || id == 0 {
		return nil, false
	}
	index := int(id - 1)
	if index < 0 || index >= a.count {
		return nil, false
	}
	chunkIndex := index / reteBetaMatchTokenChunkSize
	rowIndex := index % reteBetaMatchTokenChunkSize
	if chunkIndex < 0 || chunkIndex >= len(a.chunks) || rowIndex >= len(a.chunks[chunkIndex]) {
		return nil, false
	}
	return &a.chunks[chunkIndex][rowIndex], true
}

func (r *tokenRow) factIDs(arena *tokenArena) []FactID {
	if r == nil || arena == nil || r.size <= 0 {
		return nil
	}
	end := r.factSpanStart + r.size
	if r.factSpanStart < 0 || end > len(arena.factIDs) {
		return nil
	}
	return arena.factIDs[r.factSpanStart:end]
}

func (r *tokenRow) factVersions(arena *tokenArena) []FactVersion {
	if r == nil || arena == nil || r.size <= 0 {
		return nil
	}
	end := r.factSpanStart + r.size
	if r.factSpanStart < 0 || end > len(arena.factVersions) {
		return nil
	}
	return arena.factVersions[r.factSpanStart:end]
}

func (r tokenRef) resolve() (*tokenRow, bool) {
	if r.handle.isZero() {
		return nil, false
	}
	if r.handle.arena == nil || r.handle.generation != r.handle.arena.epoch {
		return nil, false
	}
	return r.handle.arena.rowByID(r.handle.rowID)
}

func (r tokenRef) parent() tokenRef {
	row, ok := r.resolve()
	if !ok || row.parent.isZero() {
		return tokenRef{}
	}
	return tokenRef{handle: tokenHandle{arena: r.handle.arena, rowID: row.parent.rowID, generation: r.handle.generation}}
}

func (r tokenRef) size() int {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.size
}

func (r tokenRef) pathLen() int {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.pathLen
}

func (r tokenRef) maxRecency() Recency {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.maxRecency
}

func (r tokenRef) aggregateRecency() Recency {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.aggregateRecency
}

func (r tokenRef) generation() Generation {
	if _, ok := r.resolve(); !ok || r.handle.arena == nil {
		return 0
	}
	return r.handle.arena.generation
}

func (r tokenRef) identityState() uint64 {
	row, ok := r.resolve()
	if !ok {
		return candidateIdentityHashStart(0)
	}
	return row.identityState
}

func (r tokenRef) orderedSlots() bool {
	row, ok := r.resolve()
	return ok && row.orderedSlots
}

func (r tokenRef) matchAt(index int) (conditionMatch, bool) {
	row, ok := r.resolve()
	if !ok || index < 0 || index >= row.size {
		return conditionMatch{}, false
	}
	if index == row.size-1 {
		return row.conditionMatch()
	}
	return r.parent().matchAt(index)
}

func (r tokenRef) containsFact(id FactID) bool {
	ids, ok := r.factIDs()
	if ok {
		return slices.Contains(ids, id)
	}
	for current := r; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return false
		}
		match, ok := row.conditionMatch()
		if !ok {
			return false
		}
		if match.fact.ID() == id {
			return true
		}
	}
	return false
}

func (r tokenRef) factIDs() ([]FactID, bool) {
	row, ok := r.resolve()
	if !ok {
		return nil, false
	}
	factIDs := row.factIDs(r.handle.arena)
	return factIDs, len(factIDs) == row.size
}

func (r tokenRef) factVersions() ([]FactVersion, bool) {
	row, ok := r.resolve()
	if !ok {
		return nil, false
	}
	factVersions := row.factVersions(r.handle.arena)
	return factVersions, len(factVersions) == row.size
}

type reteAgendaDelta struct {
	supported       bool
	added           []reteTerminalTokenDelta
	removed         []reteTerminalTokenDelta
	updated         []reteTerminalTokenUpdate
	demands         []backchainDemandID
	resolvedDemands []backchainDemandID
	resolvedOwners  []backchainDemandOwnerKey
}

type backchainDemandID uint64

type backchainDemandRequest struct {
	templateKey  TemplateKey
	slots        []factSlot
	supportFacts []backchainDemandSupportFact
	owner        backchainDemandOwnerKey
}

type backchainDemandRecord struct {
	id           backchainDemandID
	templateKey  TemplateKey
	slotStart    int
	slotCount    int
	supportStart int
	supportCount int
	owner        backchainDemandOwnerKey
}

type backchainDemandSupportFact struct {
	id      FactID
	version FactVersion
}

type reteTerminalTokenDelta struct {
	ruleRevisionID RuleRevisionID
	token          tokenRef
	identity       candidateIdentity
	terminalID     reteGraphTerminalNodeID
	terminalRow    graphTokenRowHandle
	activation     activationHandle
	factIDs        []FactID
	factVersions   []FactVersion
}

type reteTerminalTokenUpdate struct {
	ruleRevisionID RuleRevisionID
	before         tokenRef
	after          tokenRef
	identity       candidateIdentity
}

type betaJoinKeyKind uint8

const (
	betaJoinKeyUnknown betaJoinKeyKind = iota
	betaJoinKeyNull
	betaJoinKeyBool
	betaJoinKeyInt
	betaJoinKeyFloat
	betaJoinKeyString
	betaJoinKeyCanonical
	betaJoinKeyTokenIdentity
)

type betaJoinKey struct {
	kind              betaJoinKeyKind
	boolValue         bool
	intValue          int64
	floatBits         uint64
	stringValue       string
	secondKind        betaJoinKeyKind
	secondBoolValue   bool
	secondIntValue    int64
	secondFloatBits   uint64
	secondStringValue string
}

const reteBetaMatchTokenChunkSize = 64
const reteBetaMatchTokenChunkReserve = 2

func tokenRefEqual(left, right tokenRef) bool {
	if left.isZero() || right.isZero() {
		return left.isZero() && right.isZero()
	}
	if left.handle == right.handle {
		return true
	}
	if left.size() != right.size() || left.generation() != right.generation() || left.identityState() != right.identityState() {
		return false
	}
	leftFactIDs, leftIDsOK := left.factIDs()
	rightFactIDs, rightIDsOK := right.factIDs()
	leftFactVersions, leftVersionsOK := left.factVersions()
	rightFactVersions, rightVersionsOK := right.factVersions()
	if leftIDsOK && rightIDsOK && leftVersionsOK && rightVersionsOK {
		if len(leftFactIDs) != len(rightFactIDs) || len(leftFactVersions) != len(rightFactVersions) || len(leftFactIDs) != len(leftFactVersions) {
			return false
		}
		for i := range leftFactIDs {
			if leftFactIDs[i] != rightFactIDs[i] || leftFactVersions[i] != rightFactVersions[i] {
				return false
			}
		}
		return true
	}
	for currentLeft, currentRight := left, right; !currentLeft.isZero() || !currentRight.isZero(); currentLeft, currentRight = currentLeft.parent(), currentRight.parent() {
		leftRow, leftOK := currentLeft.resolve()
		rightRow, rightOK := currentRight.resolve()
		if !leftOK || !rightOK {
			return false
		}
		leftMatch, leftOK := leftRow.conditionMatch()
		rightMatch, rightOK := rightRow.conditionMatch()
		if !leftOK || !rightOK {
			return false
		}
		if leftMatch.fact.ID() != rightMatch.fact.ID() || leftMatch.fact.Version() != rightMatch.fact.Version() {
			return false
		}
		if leftRow.parent.isZero() || rightRow.parent.isZero() {
			return leftRow.parent.isZero() && rightRow.parent.isZero()
		}
	}
	return true
}

func tokenRefHasPrefix(token, prefix tokenRef) bool {
	if token.isZero() || prefix.isZero() {
		return false
	}
	tokenPrefix := tokenRefPrefix(token, prefix.size())
	return !tokenPrefix.isZero() && tokenRefEqual(tokenPrefix, prefix)
}

func tokenRefPrefix(token tokenRef, size int) tokenRef {
	if token.isZero() {
		return tokenRef{}
	}
	if size < 0 || size > token.size() {
		return tokenRef{}
	}
	for current := token; !current.isZero(); current = current.parent() {
		if current.size() == size {
			return current
		}
	}
	return tokenRef{}
}

func tokenRefAtSlot(token tokenRef, slot int) (conditionMatch, bool) {
	if token.isZero() || slot < 0 {
		return conditionMatch{}, false
	}
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return conditionMatch{}, false
		}
		if row.bindingSlot != slot {
			continue
		}
		match, ok := row.conditionMatch()
		if !ok {
			return conditionMatch{}, false
		}
		return match, true
	}
	return token.matchAt(slot)
}

func betaJoinKeyForPlan(plan compiledConditionPlan, valueForJoin func(join compiledJoinConstraint) (Value, bool)) (betaJoinKey, bool) {
	key, ok, _ := betaJoinKeyForPlanWithError(plan, func(join compiledJoinConstraint) (Value, bool, error) {
		value, ok := valueForJoin(join)
		return value, ok, nil
	})
	return key, ok
}

func betaJoinKeyForPlanWithError(plan compiledConditionPlan, valueForJoin func(join compiledJoinConstraint) (Value, bool, error)) (betaJoinKey, bool, error) {
	if len(plan.joins) == 0 {
		return betaJoinKey{}, true, nil
	}

	if len(plan.joins) == 1 {
		join := plan.joins[0]
		if join.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		value, ok, err := valueForJoin(join)
		if err != nil || !ok {
			return betaJoinKey{}, false, err
		}
		if key, ok := betaJoinKeyForValue(value); ok {
			return key, true, nil
		}
		return betaJoinKey{
			kind:        betaJoinKeyCanonical,
			stringValue: value.canonicalKey(),
		}, true, nil
	}

	if len(plan.joins) == 2 {
		firstJoin := plan.joins[0]
		secondJoin := plan.joins[1]
		if firstJoin.indexKind != joinIndexEquality || secondJoin.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		firstValue, ok, err := valueForJoin(firstJoin)
		if err != nil || !ok {
			return betaJoinKey{}, false, err
		}
		secondValue, ok, err := valueForJoin(secondJoin)
		if err != nil || !ok {
			return betaJoinKey{}, false, err
		}
		if key, ok := betaJoinKeyForTwoValues(firstValue, secondValue); ok {
			return key, true, nil
		}
	}

	var b strings.Builder
	for _, join := range plan.joins {
		if join.indexKind != joinIndexEquality {
			return betaJoinKey{}, false, nil
		}
		value, ok, err := valueForJoin(join)
		if err != nil || !ok {
			return betaJoinKey{}, false, err
		}
		b.WriteByte('|')
		b.WriteString(value.canonicalKey())
	}
	return betaJoinKey{
		kind:        betaJoinKeyCanonical,
		stringValue: b.String(),
	}, true, nil
}

func betaJoinKeyForTwoValues(first, second Value) (betaJoinKey, bool) {
	firstKey, ok := betaJoinKeyForValue(first)
	if !ok || firstKey.kind == betaJoinKeyCanonical {
		return betaJoinKey{}, false
	}
	secondKey, ok := betaJoinKeyForValue(second)
	if !ok || secondKey.kind == betaJoinKeyCanonical {
		return betaJoinKey{}, false
	}
	firstKey.secondKind = secondKey.kind
	firstKey.secondBoolValue = secondKey.boolValue
	firstKey.secondIntValue = secondKey.intValue
	firstKey.secondFloatBits = secondKey.floatBits
	firstKey.secondStringValue = secondKey.stringValue
	return firstKey, true
}

func betaJoinKeyForSingleValue(value Value) (betaJoinKey, bool) {
	if key, ok := betaJoinKeyForValue(value); ok {
		return key, true
	}
	return betaJoinKey{
		kind:        betaJoinKeyCanonical,
		stringValue: value.canonicalKey(),
	}, true
}

func betaJoinKeyForTokenIdentity(token tokenRef) (betaJoinKey, bool) {
	if token.isZero() {
		return betaJoinKey{}, false
	}
	return betaJoinKey{
		kind:            betaJoinKeyTokenIdentity,
		intValue:        int64(token.size()),
		floatBits:       uint64(token.generation()),
		secondFloatBits: token.identityState(),
	}, true
}

func betaJoinKeyForValue(value Value) (betaJoinKey, bool) {
	switch value.Kind() {
	case ValueNull:
		return betaJoinKey{kind: betaJoinKeyNull}, true
	case ValueBool:
		return betaJoinKey{kind: betaJoinKeyBool, boolValue: value.boolValue}, true
	case ValueInt:
		return betaJoinKey{kind: betaJoinKeyInt, intValue: value.intValue}, true
	case ValueFloat:
		if integer, ok := betaJoinIntFromFloat(value.floatValue); ok {
			return betaJoinKey{kind: betaJoinKeyInt, intValue: integer}, true
		}
		return betaJoinKey{kind: betaJoinKeyFloat, floatBits: math.Float64bits(value.floatValue)}, true
	case ValueString:
		return betaJoinKey{kind: betaJoinKeyString, stringValue: value.stringValue}, true
	default:
		return betaJoinKey{}, false
	}
}

func betaJoinIntFromFloat(floating float64) (int64, bool) {
	if floating > float64(maxExactFloatInt) || floating < float64(-maxExactFloatInt) {
		return 0, false
	}
	if math.Trunc(floating) != floating {
		return 0, false
	}
	return int64(floating), true
}
