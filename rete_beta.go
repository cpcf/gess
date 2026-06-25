package gess

import (
	"math"
	"slices"
	"strings"
)

type tokenHandle struct {
	arena      *tokenArena
	row        *tokenRow
	generation uint64
}

func (h tokenHandle) isZero() bool {
	return h.row == nil || h.generation == 0
}

type tokenRef struct {
	handle tokenHandle
}

func (r tokenRef) isZero() bool {
	return r.handle.isZero()
}

type tokenRow struct {
	slotGeneration   uint64
	parent           tokenHandle
	entry            bindingTupleEntry
	match            conditionMatch
	size             int
	factSpanStart    int
	pathLen          int
	maxRecency       Recency
	aggregateRecency Recency
	identityState    uint64
	generation       Generation
}

type tokenArena struct {
	chunks         [][]tokenRow
	factIDs        []FactID
	factVersions   []FactVersion
	count          int
	nextGeneration uint64
	keepFactSpans  bool
}

func newTokenArena() *tokenArena {
	return &tokenArena{nextGeneration: 1, keepFactSpans: true}
}

func newTokenArenaWithoutFactSpans() *tokenArena {
	return &tokenArena{nextGeneration: 1}
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
	if a.nextGeneration == 0 {
		a.nextGeneration = 1
	}
}

func (a *tokenArena) add(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
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
	chunkIndex := a.count / reteBetaMatchTokenChunkSize
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
	row.slotGeneration = a.nextGeneration
	row.match = match
	row.generation = generation
	row.factSpanStart = -1
	a.nextGeneration++

	if parentRow != nil {
		row.parent = parent.handle
		row.size = parentRow.size + 1
		row.pathLen = parentRow.pathLen + len(entry.conditionPath)
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.aggregateRecency = addRecency(parentRow.aggregateRecency, recency)
		row.identityState = parentRow.identityState
		if row.generation == 0 {
			row.generation = parentRow.generation
		}
	} else {
		row.size = 1
		row.pathLen = len(entry.conditionPath)
		row.maxRecency = recency
		row.aggregateRecency = recency
		row.identityState = candidateIdentityHashStart(generation)
		if row.generation == 0 {
			row.generation = generation
		}
	}
	row.factSpanStart = a.appendFactSpan(parentRow, match)
	identityEntry := entry
	identityEntry.value = match.value
	identityEntry.hasValue = match.hasValue
	if !match.hasValue {
		identityEntry.factID = match.fact.ID()
		identityEntry.factVersion = match.fact.Version()
	}
	row.entry = identityEntry
	row.identityState = candidateIdentityHashStep(row.identityState, identityEntry)

	a.count++
	handle := tokenHandle{arena: a, row: row, generation: row.slotGeneration}
	return tokenRef{handle: handle}
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
	chunkIndex := a.count / reteBetaMatchTokenChunkSize
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
	row.slotGeneration = a.nextGeneration
	row.factSpanStart = -1
	row.generation = generation
	row.identityState = candidateIdentityHashStart(generation)
	a.nextGeneration++
	a.count++
	return tokenRef{handle: tokenHandle{arena: a, row: row, generation: row.slotGeneration}}
}

func (a *tokenArena) resolve(handle tokenHandle) (*tokenRow, bool) {
	if a == nil || handle.isZero() {
		return nil, false
	}
	if handle.arena != nil && handle.arena != a {
		return nil, false
	}
	if handle.row.slotGeneration != handle.generation {
		return nil, false
	}
	return handle.row, true
}

func (a *tokenArena) rowCount() int {
	if a == nil {
		return 0
	}
	return a.count
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
	if r.handle.row.slotGeneration != r.handle.generation {
		return nil, false
	}
	return r.handle.row, true
}

func (r tokenRef) parent() tokenRef {
	row, ok := r.resolve()
	if !ok || row.parent.isZero() {
		return tokenRef{}
	}
	return tokenRef{handle: row.parent}
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
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.generation
}

func (r tokenRef) identityState() uint64 {
	row, ok := r.resolve()
	if !ok {
		return candidateIdentityHashStart(0)
	}
	return row.identityState
}

func (r tokenRef) matchAt(index int) (conditionMatch, bool) {
	row, ok := r.resolve()
	if !ok || index < 0 || index >= row.size {
		return conditionMatch{}, false
	}
	if index == row.size-1 {
		return row.match, true
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
		if row.match.fact.ID() == id {
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
	supported bool
	added     []reteTerminalTokenDelta
	removed   []reteTerminalTokenDelta
	updated   []reteTerminalTokenUpdate
}

type reteTerminalTokenDelta struct {
	ruleRevisionID RuleRevisionID
	token          tokenRef
	identity       candidateIdentity
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
		if leftRow.match.fact.ID() != rightRow.match.fact.ID() || leftRow.match.fact.Version() != rightRow.match.fact.Version() {
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
		if row.match.bindingSlot == slot {
			return row.match, true
		}
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
