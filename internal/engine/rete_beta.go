package engine

import (
	"math"
	"slices"
	"strings"
	"sync/atomic"
)

type tokenHandle struct {
	arena *tokenArena
	row   *tokenRow
}

func (h tokenHandle) isZero() bool {
	return h.row == nil
}

type tokenRef struct {
	handle tokenHandle
}

func (r tokenRef) isZero() bool {
	return r.handle.isZero()
}

type tokenRowEntry struct {
	bindingSlot int
	factID      FactID
	factVersion FactVersion
	value       Value
	hasValue    bool
}

// tokenRow is an individually GC-owned token node: it stays alive exactly as
// long as a handle or a descendant row's parent pointer references it, so
// there is no refcounting, recycling, or staleness generation.
type tokenRow struct {
	parent      *tokenRow
	bindingSlot int
	fact        *conditionFactRef
	// value is set only for value-carrying entries (aggregate and computed
	// results); keeping it out of line keeps the common fact-carrying row
	// small, which matters because rows dominate propagation allocation.
	value        *Value
	size         int
	maxRecency   Recency
	totalRecency Recency
	// identityState accumulates the commutative identity hash of the chain's
	// public (bindingSlot >= 0) entries; publicSize counts them and slotMask
	// records which slots appeared (slots beyond 62 share the top bit). A
	// terminal whose rule needs slots 0..n-1 can use identityState directly
	// when publicSize == n and slotMask covers exactly those slots.
	identityState uint64
	slotMask      uint64
	publicSize    int32
	// holderTableID/holderRef (and the second slot) locate the bucket-table
	// rows storing this token, so exact-handle removals unlink directly
	// without identity-index probes. holderRef == tokenHolderMulti marks
	// tokens stored in more table rows than the slots track; those removals
	// use the identity-index path.
	holderTableID  uint32
	holderRef      int32
	holder2TableID uint32
	holder2Ref     int32
	// activationLink caches the agenda activation this rule-terminal token
	// supports, with the owning agenda and activation ordinal for staleness
	// verification, so removals skip the identity-bucket lookup. Tokens can
	// feed more than one agenda (candidate/direct comparisons), so only the
	// recording agenda trusts the link; stale or foreign links fall back.
	activationLink    *activation
	activationAgenda  *agenda
	activationOrdinal uint64
}

// linkTokenActivation caches the activation supporting this terminal token
// on the token's arena row.
func (a *agenda) linkTokenActivation(token tokenRef, act *activation) {
	if a == nil || act == nil || act.key.ordinal == 0 {
		return
	}
	row, ok := token.resolve()
	if !ok {
		return
	}
	row.activationLink = act
	row.activationAgenda = a
	row.activationOrdinal = act.key.ordinal
}

// takeLinkedTokenActivation resolves and clears the cached activation for a
// removal token. Agenda, ordinal, and rule-revision verification make
// foreign, reused, or recycled activation storage fall back to the identity
// lookup.
func (a *agenda) takeLinkedTokenActivation(delta reteTerminalTokenDelta) *activation {
	if a == nil {
		return nil
	}
	row, ok := delta.token.resolve()
	if !ok {
		return nil
	}
	act := row.activationLink
	if act == nil || row.activationAgenda != a || row.activationOrdinal == 0 ||
		act.key.ordinal != row.activationOrdinal ||
		act.ruleRevisionID != delta.ruleRevisionID {
		return nil
	}
	row.activationLink = nil
	row.activationAgenda = nil
	row.activationOrdinal = 0
	return act
}

const tokenHolderMulti int32 = -1

// nextTokenHolderTableID assigns bucket-table identities for holder
// backpointers; sessions may run on different goroutines, so the counter is
// atomic.
var tokenHolderTableIDSeq atomic.Uint32

func nextTokenHolderTableID() uint32 {
	return tokenHolderTableIDSeq.Add(1)
}

// recordTokenHolder marks the token's row as stored at (tableID, ref); a
// third holder demotes the row to the identity-index removal path.
func recordTokenHolder(token tokenRef, tableID uint32, ref int32) {
	row, ok := token.resolve()
	if !ok {
		return
	}
	switch {
	case row.holderRef == tokenHolderMulti:
	case row.holderTableID == 0 && row.holderRef == 0:
		row.holderTableID = tableID
		row.holderRef = ref
	case row.holder2TableID == 0 && row.holder2Ref == 0:
		row.holder2TableID = tableID
		row.holder2Ref = ref
	default:
		row.holderTableID = 0
		row.holderRef = tokenHolderMulti
		row.holder2TableID = 0
		row.holder2Ref = 0
	}
}

// holderRefForTable reports the row ref this token is stored at in the given
// table, or 0 when untracked there.
func (r *tokenRow) holderRefForTable(tableID uint32) int32 {
	if r.holderTableID == tableID && r.holderRef > 0 {
		return r.holderRef
	}
	if r.holder2TableID == tableID && r.holder2Ref > 0 {
		return r.holder2Ref
	}
	return 0
}

// clearTokenHolder resets the holder record when the recorded row is
// unlinked; multi-holder rows keep their marker.
func clearTokenHolder(token tokenRef, tableID uint32, ref int32) {
	row, ok := token.resolve()
	if !ok {
		return
	}
	if row.holderTableID == tableID && row.holderRef == ref {
		row.holderTableID = 0
		row.holderRef = 0
	} else if row.holder2TableID == tableID && row.holder2Ref == ref {
		row.holder2TableID = 0
		row.holder2Ref = 0
	}
}

type tokenIdentityKey = graphTokenIdentityKey

type tokenArena struct {
	factRefs     []conditionFactRef
	factRefIndex map[tokenFactRefKey]int
	spare        []tokenRow
	count        int
	generation   Generation
}

type tokenFactRefKey struct {
	id      FactID
	version FactVersion
	recency Recency
}

func newTokenArena() *tokenArena {
	return &tokenArena{}
}

func (a *tokenArena) reset() {
	if a == nil {
		return
	}
	// Pre-reset rows may still be referenced through stale handles and share
	// interned fact refs; drop the storage instead of reusing it so those rows
	// never observe post-reset data.
	a.factRefs = nil
	if a.factRefIndex != nil {
		clear(a.factRefIndex)
	}
	a.spare = nil
	a.count = 0
	a.generation = 0
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
	if source.handle.arena == a {
		// Same-arena sources share the interned fact ref directly; factRefs
		// storage outlives row recycling, so the pointer stays valid even if
		// the source row is reclaimed before the new row.
		return a.addCompactSharedFact(parent, sourceRow.fact, entry, recency, generation)
	}
	match, ok := sourceRow.conditionMatch()
	if !ok {
		return tokenRef{}
	}
	return a.addCompactInternal(parent, entry, match, recency, generation)
}

// allocRow returns a fresh GC-owned row carved from a small batch; the Go
// collector owns the storage, so rows need no recycling and pointers stay
// valid for as long as anything references them. A batch is reclaimed once
// none of its rows are referenced, so batches stay small to bound the memory
// a long-lived row can pin.
func (a *tokenArena) allocRow() *tokenRow {
	a.count++
	if len(a.spare) == 0 {
		a.spare = make([]tokenRow, tokenRowAllocBatch)
	}
	row := &a.spare[0]
	a.spare = a.spare[1:]
	return row
}

const tokenRowAllocBatch = 16

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
	row.identityState = candidateIdentityHashStart(tokenGeneration)
	row.applyEntryIdentity(entry)

	return tokenRef{handle: tokenHandle{arena: a, row: row}}
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
		row.parent = parent.handle.row
		row.size = parentRow.size + 1
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.totalRecency = addRecency(parentRow.totalRecency, recency)
		row.identityState = parentRow.identityState
		row.slotMask = parentRow.slotMask
		row.publicSize = parentRow.publicSize
	} else {
		row.size = 1
		row.maxRecency = recency
		row.totalRecency = recency
		row.identityState = candidateIdentityHashStart(tokenGeneration)
	}
	row.fact = a.internFactRef(match.fact, match.hasValue)
	row.setEntry(entry)
	row.applyEntryIdentity(entry)

	handle := tokenHandle{arena: a, row: row}
	return tokenRef{handle: handle}
}

// addCompactSharedFact is addCompactInternal for a source row in this arena:
// the already-interned fact ref is shared instead of copied and re-interned.
func (a *tokenArena) addCompactSharedFact(parent tokenRef, fact *conditionFactRef, entry tokenRowEntry, recency Recency, generation Generation) tokenRef {
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
		row.parent = parent.handle.row
		row.size = parentRow.size + 1
		row.maxRecency = max(recency, parentRow.maxRecency)
		row.totalRecency = addRecency(parentRow.totalRecency, recency)
		row.identityState = parentRow.identityState
		row.slotMask = parentRow.slotMask
		row.publicSize = parentRow.publicSize
	} else {
		row.size = 1
		row.maxRecency = recency
		row.totalRecency = recency
		row.identityState = candidateIdentityHashStart(tokenGeneration)
	}
	row.fact = fact
	row.setEntry(entry)
	row.applyEntryIdentity(entry)

	return tokenRef{handle: tokenHandle{arena: a, row: row}}
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
	match := conditionMatch{
		bindingSlot: r.bindingSlot,
		fact:        fact,
	}
	if r.value != nil {
		match.value = *r.value
		match.hasValue = true
	}
	return match, true
}

// applyEntryIdentity folds a public entry into the chain identity
// accumulators; non-public placeholder entries leave them untouched.
func (r *tokenRow) applyEntryIdentity(entry tokenRowEntry) {
	if entry.bindingSlot < 0 {
		return
	}
	r.identityState = candidateIdentityHashTokenEntryStep(r.identityState, entry)
	r.slotMask |= uint64(1) << uint(min(entry.bindingSlot, 63))
	r.publicSize++
}

func (r *tokenRow) setEntry(entry tokenRowEntry) {
	if r == nil {
		return
	}
	r.bindingSlot = entry.bindingSlot
	if entry.hasValue {
		value := entry.value
		r.value = &value
	} else {
		r.value = nil
	}
}

func (r *tokenRow) tokenRowEntry() tokenRowEntry {
	if r == nil {
		return tokenRowEntry{}
	}
	out := tokenRowEntry{
		bindingSlot: r.bindingSlot,
	}
	if r.value != nil {
		out.value = *r.value
		out.hasValue = true
	} else if r.fact != nil {
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
	return candidateIdentityHashFactStep(hash, entry.bindingSlot, entry.factID, entry.factVersion)
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
	return tokenRef{handle: tokenHandle{arena: a, row: row}}
}

func (a *tokenArena) resolve(handle tokenHandle) (*tokenRow, bool) {
	if a == nil || handle.isZero() {
		return nil, false
	}
	if handle.arena != nil && handle.arena != a {
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

func (r tokenRef) resolve() (*tokenRow, bool) {
	if r.handle.row == nil {
		return nil, false
	}
	return r.handle.row, true
}

func (r tokenRef) parent() tokenRef {
	row, ok := r.resolve()
	if !ok || row.parent == nil {
		return tokenRef{}
	}
	return tokenRef{handle: tokenHandle{arena: r.handle.arena, row: row.parent}}
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

// parentRow steps to row's parent within the arena. nil means the chain
// ended; the bool is retained for call-site compatibility and is always true.
func (a *tokenArena) parentRow(row *tokenRow) (*tokenRow, bool) {
	return row.parent, true
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
		if leftRow.parent == nil || rightRow.parent == nil {
			return leftRow.parent == nil && rightRow.parent == nil
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

// tokenFactPtrAtSlot resolves the fact bound at slot without copying a
// conditionMatch. direct=false means the caller must use the tokenRefAtSlot
// path (value-bound row or positional matchAt fallback); direct=true with
// found=false matches tokenRefAtSlot reporting no match.
func tokenFactPtrAtSlot(token tokenRef, slot int) (fact *conditionFactRef, found bool, direct bool) {
	if token.isZero() || slot < 0 {
		return nil, false, false
	}
	row, ok := token.resolve()
	if !ok {
		return nil, false, true
	}
	arena := token.handle.arena
	for row != nil {
		if row.bindingSlot == slot {
			if row.value != nil || row.fact == nil {
				return nil, false, false
			}
			return row.fact, true, true
		}
		if row, ok = arena.parentRow(row); !ok {
			return nil, false, true
		}
	}
	return nil, false, false
}

// tokenFactsAtSlots resolves the facts for count binding slots in one chain
// walk, resolving each row exactly once. The nearest row to the tip wins for
// each slot, matching tokenRefAtSlot. It reports false when any slot is not
// found on the chain (callers needing the positional matchAt fallback must
// use tokenRefAtSlot).
func tokenFactsAtSlots(token tokenRef, slots [3]int, count int, out *[3]*conditionFactRef) bool {
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
			out[i] = row.fact
		}
		if remaining == 0 {
			return true
		}
		if row.parent == nil {
			return false
		}
		current = tokenRef{handle: tokenHandle{arena: current.handle.arena, row: row.parent}}
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

// betaJoinKeySlot is one extracted hash-join key component, produced without
// materializing an intermediate Value on supported paths.
type betaJoinKeySlot struct {
	kind        betaJoinKeyKind
	boolValue   bool
	intValue    int64
	floatBits   uint64
	stringValue string
}

func betaJoinKeySlotFromValue(value Value) (betaJoinKeySlot, bool) {
	switch value.Kind() {
	case ValueNull:
		return betaJoinKeySlot{kind: betaJoinKeyNull}, true
	case ValueBool:
		return betaJoinKeySlot{kind: betaJoinKeyBool, boolValue: value.boolValue}, true
	case ValueInt:
		return betaJoinKeySlot{kind: betaJoinKeyInt, intValue: value.intValue}, true
	case ValueFloat:
		if integer, ok := betaJoinIntFromFloat(value.floatValue); ok {
			return betaJoinKeySlot{kind: betaJoinKeyInt, intValue: integer}, true
		}
		return betaJoinKeySlot{kind: betaJoinKeyFloat, floatBits: math.Float64bits(value.floatValue)}, true
	case ValueString:
		return betaJoinKeySlot{kind: betaJoinKeyString, stringValue: value.stringValue}, true
	default:
		return betaJoinKeySlot{}, false
	}
}

func betaJoinKeySlotFromCompact(slot compactFactSlot) (betaJoinKeySlot, bool) {
	if !slot.ok {
		return betaJoinKeySlot{}, false
	}
	switch slot.kind {
	case duplicateScalarNull:
		return betaJoinKeySlot{kind: betaJoinKeyNull}, true
	case duplicateScalarBool:
		return betaJoinKeySlot{kind: betaJoinKeyBool, boolValue: slot.bits != 0}, true
	case duplicateScalarInt:
		return betaJoinKeySlot{kind: betaJoinKeyInt, intValue: int64(slot.bits)}, true
	case duplicateScalarFloat:
		floating := math.Float64frombits(slot.bits)
		if integer, ok := betaJoinIntFromFloat(floating); ok {
			return betaJoinKeySlot{kind: betaJoinKeyInt, intValue: integer}, true
		}
		return betaJoinKeySlot{kind: betaJoinKeyFloat, floatBits: slot.bits}, true
	case duplicateScalarString:
		return betaJoinKeySlot{kind: betaJoinKeyString, stringValue: slot.stringValue}, true
	default:
		return betaJoinKeySlot{}, false
	}
}

// betaJoinKeySlotsComparableForEquality mirrors valuesComparableForEquality
// on folded slots: numerics are always mutually comparable, other kinds only
// to themselves.
func betaJoinKeySlotsComparableForEquality(left, right betaJoinKeySlot) bool {
	if betaJoinKeySlotNumeric(left) && betaJoinKeySlotNumeric(right) {
		return true
	}
	return left.kind == right.kind
}

func betaJoinKeySlotNumeric(slot betaJoinKeySlot) bool {
	return slot.kind == betaJoinKeyInt || slot.kind == betaJoinKeyFloat
}

// betaJoinKeySlotsEqual mirrors Value.Equal on folded slots. Integral floats
// fold to int, so a remaining float component is never integral and cross
// int/float equality is always false; float equality goes through numeric
// comparison so NaN stays unequal to itself.
func betaJoinKeySlotsEqual(left, right betaJoinKeySlot) bool {
	if left.kind != right.kind {
		return false
	}
	switch left.kind {
	case betaJoinKeyNull:
		return true
	case betaJoinKeyBool:
		return left.boolValue == right.boolValue
	case betaJoinKeyInt:
		return left.intValue == right.intValue
	case betaJoinKeyFloat:
		return math.Float64frombits(left.floatBits) == math.Float64frombits(right.floatBits)
	case betaJoinKeyString:
		return left.stringValue == right.stringValue
	default:
		return false
	}
}

// compareBetaJoinKeySlots mirrors compareNumericValues on folded slots;
// ok=false when either side is non-numeric.
func compareBetaJoinKeySlots(left, right betaJoinKeySlot) (int, bool) {
	if !betaJoinKeySlotNumeric(left) || !betaJoinKeySlotNumeric(right) {
		return 0, false
	}
	switch {
	case left.kind == betaJoinKeyInt && right.kind == betaJoinKeyInt:
		switch {
		case left.intValue < right.intValue:
			return -1, true
		case left.intValue > right.intValue:
			return 1, true
		default:
			return 0, true
		}
	case left.kind == betaJoinKeyInt:
		return compareIntAndFloatValues(left.intValue, math.Float64frombits(right.floatBits)), true
	case right.kind == betaJoinKeyInt:
		return -compareIntAndFloatValues(right.intValue, math.Float64frombits(left.floatBits)), true
	default:
		leftFloat := math.Float64frombits(left.floatBits)
		rightFloat := math.Float64frombits(right.floatBits)
		switch {
		case leftFloat < rightFloat:
			return -1, true
		case leftFloat > rightFloat:
			return 1, true
		default:
			return 0, true
		}
	}
}

// betaJoinKeyFromSlots assembles the composite key from already-extracted
// slots, writing each component once.
func betaJoinKeyFromSlots(slots *[3]betaJoinKeySlot, count int) betaJoinKey {
	key := betaJoinKey{
		kind:        slots[0].kind,
		boolValue:   slots[0].boolValue,
		intValue:    slots[0].intValue,
		floatBits:   slots[0].floatBits,
		stringValue: slots[0].stringValue,
	}
	if count >= 2 {
		key.secondKind = slots[1].kind
		key.secondBoolValue = slots[1].boolValue
		key.secondIntValue = slots[1].intValue
		key.secondFloatBits = slots[1].floatBits
		key.secondStringValue = slots[1].stringValue
	}
	if count >= 3 {
		key.thirdKind = slots[2].kind
		key.thirdBoolValue = slots[2].boolValue
		key.thirdIntValue = slots[2].intValue
		key.thirdFloatBits = slots[2].floatBits
		key.thirdStringValue = slots[2].stringValue
	}
	return key
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
