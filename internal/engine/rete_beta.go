package engine

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

type tokenParentHandle struct {
	row *tokenRow
	gen uint64
}

func (h tokenParentHandle) isZero() bool {
	return h.row == nil
}

type tokenRowEntry struct {
	bindingSlot int
	factID      FactID
	factVersion FactVersion
	value       Value
	hasValue    bool
}

type tokenRow struct {
	parent        tokenParentHandle
	bindingSlot   int
	fact          *conditionFactRef
	value         Value
	hasValue      bool
	size          int
	maxRecency    Recency
	totalRecency  Recency
	identityState uint64
	// rowGen is the allocation generation of this row. Handles capture it at
	// allocation time; a mismatch means the row was recycled and the handle is
	// stale. Zero marks a free row.
	rowGen uint64
	// refs counts durable holders (beta/negative/terminal/query/aggregate
	// memories). Rows reaching zero are queued for recycling and reclaimed at
	// the next safe boundary.
	refs        int32
	pendingFree bool
}

type tokenIdentityKey = graphTokenIdentityKey

type tokenArena struct {
	chunks       [][]tokenRow
	factRefs     []conditionFactRef
	factRefIndex map[tokenFactRefKey]int
	count        int
	nextRowGen   uint64
	freeRows     []*tokenRow
	pendingFree  []*tokenRow
	recentRows   []*tokenRow
	generation   Generation

	statGrown   int
	statReused  int
	statFreed   int
	statSwept   int
	statFlushes int
}

type tokenFactRefKey struct {
	id      FactID
	version FactVersion
	recency Recency
}

func newTokenArena() *tokenArena {
	return &tokenArena{}
}

func (a *tokenArena) reserve(rowCapacity int) {
	if a == nil || rowCapacity <= 0 {
		return
	}
	chunkCount := (rowCapacity + reteBetaMatchTokenChunkSize - 1) / reteBetaMatchTokenChunkSize
	for len(a.chunks) < chunkCount {
		a.chunks = append(a.chunks, make([]tokenRow, 0, reteBetaMatchTokenChunkSize))
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
	for i := range a.factRefs {
		a.factRefs[i] = conditionFactRef{}
	}
	a.factRefs = a.factRefs[:0]
	if a.factRefIndex != nil {
		clear(a.factRefIndex)
	}
	a.count = 0
	a.generation = 0
	a.freeRows = a.freeRows[:0]
	a.pendingFree = a.pendingFree[:0]
	a.recentRows = a.recentRows[:0]
}

func (a *tokenArena) add(parent tokenRef, entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
	return a.addCompact(parent, tokenRowEntryForMatch(entry, match), match, recency, generation)
}

func (a *tokenArena) addAlphaSource(entry bindingTupleEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	rowEntry := tokenRowEntry{
		bindingSlot: entry.bindingSlot,
		value:       match.value,
		hasValue:    match.hasValue,
	}
	if !match.hasValue {
		rowEntry.factID = match.fact.ID()
		rowEntry.factVersion = match.fact.Version()
	}
	match.bindingSlot = rowEntry.bindingSlot
	return a.addSourceCompact(rowEntry, match, recency, generation)
}

func (a *tokenArena) addCompact(parent tokenRef, entry tokenRowEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
	return a.addCompactInternal(parent, entry, match, recency, generation)
}

func (a *tokenArena) addCompactSource(parent tokenRef, source tokenRef, entry tokenRowEntry, recency Recency, generation Generation) tokenRef {
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
	return a.addCompactInternal(parent, entry, match, recency, generation)
}

// allocRow returns a fresh zeroed row with its allocation generation assigned,
// reusing a recycled row when one is available. Rows live in chunks whose
// backing arrays never move, so *tokenRow stays valid for the arena lifetime.
func (a *tokenArena) allocRow() *tokenRow {
	for n := len(a.freeRows); n > 0; n = len(a.freeRows) {
		row := a.freeRows[n-1]
		a.freeRows = a.freeRows[:n-1]
		if row == nil || row.rowGen != 0 {
			continue
		}
		a.nextRowGen++
		row.rowGen = a.nextRowGen
		a.recentRows = append(a.recentRows, row)
		a.statReused++
		return row
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
	a.count++
	a.nextRowGen++
	row.rowGen = a.nextRowGen
	a.recentRows = append(a.recentRows, row)
	a.statGrown++
	return row
}

func (a *tokenArena) addSourceCompact(entry tokenRowEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
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
	row := a.allocRow()
	if row == nil {
		return tokenRef{}
	}
	row.fact = a.internFactRef(match.fact, match.hasValue)
	row.size = 1
	row.maxRecency = recency
	row.totalRecency = recency
	row.setEntry(entry)
	row.identityState = candidateIdentityHashTokenEntryStep(candidateIdentityHashStart(tokenGeneration), entry)

	return tokenRef{handle: tokenHandle{arena: a, row: row, generation: row.rowGen}}
}

func (a *tokenArena) addCompactInternal(parent tokenRef, entry tokenRowEntry, match conditionMatch, recency Recency, generation Generation) tokenRef {
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
	row := a.allocRow()
	if row == nil {
		return tokenRef{}
	}
	if parentRow != nil {
		// Re-resolve after allocation: allocRow may have appended to the
		// parent's chunk slice header, but never reallocates existing chunk
		// storage, so parentRow stays valid; keep the resolve for safety.
		row.parent = tokenParentHandle{row: parent.handle.row, gen: parent.handle.generation}
		row.size = parentRow.size + 1
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.totalRecency = addRecency(parentRow.totalRecency, recency)
		row.identityState = parentRow.identityState
	} else {
		row.size = 1
		row.maxRecency = recency
		row.totalRecency = recency
		row.identityState = candidateIdentityHashStart(tokenGeneration)
	}
	row.fact = a.internFactRef(match.fact, match.hasValue)
	row.setEntry(entry)
	row.identityState = candidateIdentityHashTokenEntryStep(row.identityState, entry)

	handle := tokenHandle{arena: a, row: row, generation: row.rowGen}
	return tokenRef{handle: handle}
}

func (a *tokenArena) internFactRef(fact conditionFactRef, hasValue bool) *conditionFactRef {
	if a == nil || hasValue || fact.ID().IsZero() {
		return nil
	}
	if fact.ID().sequence > transientFactSequenceThreshold {
		return a.appendFactRef(fact)
	}
	key := tokenFactRefKey{id: fact.ID(), version: fact.Version(), recency: fact.Recency()}
	if a.factRefIndex != nil {
		if index, ok := a.factRefIndex[key]; ok && index >= 0 && index < len(a.factRefs) {
			return &a.factRefs[index]
		}
	} else {
		a.factRefIndex = make(map[tokenFactRefKey]int)
	}
	ref := a.appendFactRef(fact)
	index := len(a.factRefs) - 1
	a.factRefIndex[key] = index
	return ref
}

func (a *tokenArena) appendFactRef(fact conditionFactRef) *conditionFactRef {
	if a == nil {
		return nil
	}
	a.factRefs = append(a.factRefs, fact)
	return &a.factRefs[len(a.factRefs)-1]
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
	var fact conditionFactRef
	if r.fact != nil {
		fact = *r.fact
	}
	return conditionMatch{
		bindingSlot: r.bindingSlot,
		fact:        fact,
		value:       r.value,
		hasValue:    r.hasValue,
	}, true
}

func (r *tokenRow) setEntry(entry tokenRowEntry) {
	if r == nil {
		return
	}
	r.bindingSlot = entry.bindingSlot
	r.value = entry.value
	r.hasValue = entry.hasValue
}

func (r *tokenRow) tokenRowEntry() tokenRowEntry {
	if r == nil {
		return tokenRowEntry{}
	}
	out := tokenRowEntry{
		bindingSlot: r.bindingSlot,
		value:       r.value,
		hasValue:    r.hasValue,
	}
	if !r.hasValue && r.fact != nil {
		out.factID = r.fact.ID()
		out.factVersion = r.fact.Version()
	}
	return out
}

func tokenRowEntryForMatch(entry bindingTupleEntry, match conditionMatch) tokenRowEntry {
	out := tokenRowEntry{
		bindingSlot: entry.bindingSlot,
		value:       match.value,
		hasValue:    match.hasValue,
	}
	if entry.hasValue {
		out.value = cloneValue(entry.value)
		out.hasValue = true
		return out
	}
	if !match.hasValue {
		out.factID = match.fact.ID()
		out.factVersion = match.fact.Version()
	}
	return out
}

func candidateIdentityHashTokenEntryStep(hash uint64, entry tokenRowEntry) uint64 {
	if entry.hasValue {
		return candidateIdentityHashValueStep(hash, entry.bindingSlot, entry.value)
	}
	return candidateIdentityHashFactStep(hash, entry.factID, entry.factVersion)
}

func (a *tokenArena) addSeed(generation Generation) tokenRef {
	if a == nil {
		return tokenRef{}
	}
	if !a.setGeneration(generation) {
		return tokenRef{}
	}
	row := a.allocRow()
	if row == nil {
		return tokenRef{}
	}
	row.identityState = candidateIdentityHashStart(generation)
	// Seed rows anchor propagation roots and must never be recycled.
	row.refs = 1
	return tokenRef{handle: tokenHandle{arena: a, row: row, generation: row.rowGen}}
}

func (a *tokenArena) resolve(handle tokenHandle) (*tokenRow, bool) {
	if a == nil || handle.isZero() {
		return nil, false
	}
	if handle.arena != nil && handle.arena != a {
		return nil, false
	}
	if handle.row.rowGen != handle.generation {
		return nil, false
	}
	return handle.row, true
}

// retainToken records a durable holder of the token's arena row.
func (a *tokenArena) retainToken(token tokenRef) {
	if a == nil {
		return
	}
	if row, ok := a.resolve(token.handle); ok {
		row.refs++
	}
}

// releaseToken drops a durable holder. Rows reaching zero holders are queued
// and recycled by flushPendingFree at the next safe boundary.
func (a *tokenArena) releaseToken(token tokenRef) {
	if a == nil {
		return
	}
	row, ok := a.resolve(token.handle)
	if !ok {
		return
	}
	if row.refs > 0 {
		row.refs--
	}
	if row.refs == 0 && !row.pendingFree {
		row.pendingFree = true
		a.pendingFree = append(a.pendingFree, token.handle.row)
	}
}

// flushPendingFree recycles rows with no remaining holders. Callers must only
// invoke it at boundaries where no transient tokenRef from earlier propagation
// is still in use, such as the end of a fire iteration.
func (a *tokenArena) flushPendingFree() {
	if a == nil {
		return
	}
	a.statFlushes++
	for _, row := range a.pendingFree {
		if row == nil || !row.pendingFree {
			continue
		}
		row.pendingFree = false
		if row.refs != 0 {
			continue
		}
		*row = tokenRow{}
		a.freeRows = append(a.freeRows, row)
		a.statFreed++
	}
	a.pendingFree = a.pendingFree[:0]
	// Rows allocated since the last flush that never gained a durable holder
	// are transient propagation products (dropped duplicates, rejected joins)
	// and are dead once the boundary is reached.
	for _, row := range a.recentRows {
		if row == nil || row.rowGen == 0 || row.refs != 0 || row.pendingFree {
			continue
		}
		*row = tokenRow{}
		a.freeRows = append(a.freeRows, row)
		a.statSwept++
	}
	a.recentRows = a.recentRows[:0]
}

func (r tokenRef) retain() {
	r.handle.arena.retainToken(r)
}

func (r tokenRef) release() {
	r.handle.arena.releaseToken(r)
}

// retainChain retains every row on the token's parent chain. Terminal rows can
// outlive their token's derivation through identity support counting, so they
// must keep the full chain resolvable rather than relying on upstream
// memories.
func (r tokenRef) retainChain() {
	for current := r; !current.isZero(); current = current.parent() {
		current.retain()
	}
}

func (r tokenRef) releaseChain() {
	for current := r; !current.isZero(); current = current.parent() {
		current.release()
	}
}

func (a *tokenArena) rowCount() int {
	if a == nil {
		return 0
	}
	return a.count
}

func (r tokenRef) resolve() (*tokenRow, bool) {
	if r.handle.isZero() {
		return nil, false
	}
	if r.handle.row.rowGen != r.handle.generation {
		return nil, false
	}
	return r.handle.row, true
}

func (r tokenRef) parent() tokenRef {
	row, ok := r.resolve()
	if !ok || row.parent.isZero() {
		return tokenRef{}
	}
	return tokenRef{handle: tokenHandle{arena: r.handle.arena, row: row.parent.row, generation: row.parent.gen}}
}

func (r tokenRef) size() int {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.size
}

func (r tokenRef) maxRecency() Recency {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.maxRecency
}

func (r tokenRef) totalRecency() Recency {
	row, ok := r.resolve()
	if !ok {
		return 0
	}
	return row.totalRecency
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

func (r tokenRef) identityKey() tokenIdentityKey {
	if row, ok := r.resolve(); ok {
		return tokenIdentityKey{
			size:          row.size,
			generation:    r.handle.arena.generation,
			identityState: row.identityState,
		}
	}
	return tokenIdentityKey{
		identityState: candidateIdentityHashStart(0),
	}
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
	if r.isZero() {
		return false
	}
	row, ok := r.resolve()
	if !ok {
		return false
	}
	arena := r.handle.arena
	for row != nil {
		if row.fact != nil && row.fact.ID() == id {
			return true
		}
		if row, ok = arena.parentRow(row); !ok {
			return false
		}
	}
	return false
}

func (r tokenRef) factIDs() ([]FactID, bool) {
	return nil, false
}

func (r tokenRef) factVersions() ([]FactVersion, bool) {
	return nil, false
}

type reteAgendaDelta struct {
	supported bool
	// owned reports that the payload slices no longer alias reusable
	// graph-beta scratch or arena storage, so the delta may be retained
	// across mutations without another defensive copy.
	owned           bool
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
	thirdKind         betaJoinKeyKind
	thirdBoolValue    bool
	thirdIntValue     int64
	thirdFloatBits    uint64
	thirdStringValue  string
}

const reteBetaMatchTokenChunkSize = 64
const reteBetaMatchTokenChunkReserve = 2
const transientFactSequenceThreshold = ^uint64(0) >> 1

// parentRow steps to row's parent within the arena, resolving once. nil with
// ok=true means the chain ended; ok=false means the parent handle is stale.
func (a *tokenArena) parentRow(row *tokenRow) (*tokenRow, bool) {
	if row.parent.isZero() {
		return nil, true
	}
	parent := row.parent.row
	if parent.rowGen != row.parent.gen {
		return nil, false
	}
	return parent, true
}

// factIdentity reads the row's fact identity without copying a conditionMatch.
func (r *tokenRow) factIdentity() (FactID, FactVersion) {
	if r.fact == nil {
		return FactID{}, 0
	}
	return r.fact.ID(), r.fact.Version()
}

func tokenRefEqual(left, right tokenRef) bool {
	if left.isZero() || right.isZero() {
		return left.isZero() && right.isZero()
	}
	if left.handle == right.handle {
		return true
	}
	leftRow, leftOK := left.resolve()
	rightRow, rightOK := right.resolve()
	if !leftOK || !rightOK {
		return false
	}
	if leftRow.size != rightRow.size || leftRow.identityState != rightRow.identityState {
		return false
	}
	leftArena, rightArena := left.handle.arena, right.handle.arena
	if leftArena.generation != rightArena.generation {
		return false
	}
	for {
		leftID, leftVersion := leftRow.factIdentity()
		rightID, rightVersion := rightRow.factIdentity()
		if leftID != rightID || leftVersion != rightVersion {
			return false
		}
		if leftRow.parent.isZero() || rightRow.parent.isZero() {
			return leftRow.parent.isZero() && rightRow.parent.isZero()
		}
		if leftRow, leftOK = leftArena.parentRow(leftRow); !leftOK {
			return false
		}
		if rightRow, rightOK = rightArena.parentRow(rightRow); !rightOK {
			return false
		}
	}
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
	row, ok := token.resolve()
	if !ok {
		return conditionMatch{}, false
	}
	arena := token.handle.arena
	for row != nil {
		if row.bindingSlot == slot {
			return row.conditionMatch()
		}
		if row, ok = arena.parentRow(row); !ok {
			return conditionMatch{}, false
		}
	}
	return token.matchAt(slot)
}

// tokenFactsAtSlots resolves the facts for count binding slots in one chain
// walk, resolving each row exactly once. The nearest row to the tip wins for
// each slot, matching tokenRefAtSlot. It reports false when any slot is not
// found on the chain (callers needing the positional matchAt fallback must
// use tokenRefAtSlot).
func tokenFactsAtSlots(token tokenRef, slots [3]int, count int, out *[3]conditionFactRef) bool {
	if count <= 0 || count > 3 || out == nil {
		return false
	}
	var found [3]bool
	remaining := count
	for current := token; !current.isZero(); {
		row, ok := current.resolve()
		if !ok {
			return false
		}
		slot := row.bindingSlot
		for i := range count {
			if found[i] || slots[i] != slot {
				continue
			}
			found[i] = true
			remaining--
			if row.fact != nil {
				out[i] = *row.fact
			}
		}
		if remaining == 0 {
			return true
		}
		if row.parent.isZero() {
			return false
		}
		current = tokenRef{handle: tokenHandle{arena: current.handle.arena, row: row.parent.row, generation: row.parent.gen}}
	}
	return false
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

	if len(plan.joins) == 2 || len(plan.joins) == 3 {
		for _, join := range plan.joins {
			if join.indexKind != joinIndexEquality {
				return betaJoinKey{}, false, nil
			}
		}
		firstValue, ok, err := valueForJoin(plan.joins[0])
		if err != nil || !ok {
			return betaJoinKey{}, false, err
		}
		secondValue, ok, err := valueForJoin(plan.joins[1])
		if err != nil || !ok {
			return betaJoinKey{}, false, err
		}
		if len(plan.joins) == 2 {
			if key, ok := betaJoinKeyForTwoValues(firstValue, secondValue); ok {
				return key, true, nil
			}
		} else {
			thirdValue, ok, err := valueForJoin(plan.joins[2])
			if err != nil || !ok {
				return betaJoinKey{}, false, err
			}
			if key, ok := betaJoinKeyForThreeValues(firstValue, secondValue, thirdValue); ok {
				return key, true, nil
			}
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

func betaJoinKeyForThreeValues(first, second, third Value) (betaJoinKey, bool) {
	key, ok := betaJoinKeyForTwoValues(first, second)
	if !ok {
		return betaJoinKey{}, false
	}
	thirdKey, ok := betaJoinKeyForValue(third)
	if !ok || thirdKey.kind == betaJoinKeyCanonical {
		return betaJoinKey{}, false
	}
	key.thirdKind = thirdKey.kind
	key.thirdBoolValue = thirdKey.boolValue
	key.thirdIntValue = thirdKey.intValue
	key.thirdFloatBits = thirdKey.floatBits
	key.thirdStringValue = thirdKey.stringValue
	return key, true
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
	identity := token.identityKey()
	return betaJoinKey{
		kind:            betaJoinKeyTokenIdentity,
		intValue:        int64(identity.size),
		floatBits:       uint64(identity.generation),
		secondFloatBits: identity.identityState,
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
