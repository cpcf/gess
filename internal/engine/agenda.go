package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cpcf/gess/internal/fnvhash"
)

type activationKey struct {
	fingerprint activationFingerprint
	ordinal     uint64
}

type activationSortEntry struct {
	key activationKey
	act *activation
}

type activationHandle struct {
	key        activationKey
	generation uint32
}

func (h activationHandle) isZero() bool {
	return h.generation == 0
}

type activationFingerprint uint64

type activationObserveFunc func(*activation)

type activationBucket struct {
	first    *activation
	second   *activation
	overflow []*activation
}

const activationRowChunkSize = 256

type activationRows struct {
	chunks [][]activation
	count  int
}

func (r *activationRows) reset() {
	if r == nil {
		return
	}
	for chunkIndex, chunk := range r.chunks {
		for i := range chunk {
			chunk[i] = activation{}
		}
		r.chunks[chunkIndex] = chunk[:0]
	}
	r.count = 0
}

func (r *activationRows) add(act activation) *activation {
	row := r.addEmpty()
	if row != nil {
		*row = act
	}
	return row
}

func (r *activationRows) addEmpty() *activation {
	if r == nil {
		return nil
	}
	chunkIndex := r.count / activationRowChunkSize
	for len(r.chunks) <= chunkIndex {
		r.chunks = append(r.chunks, make([]activation, 0, activationRowChunkSize))
	}
	chunk := r.chunks[chunkIndex]
	if len(chunk) < cap(chunk) {
		chunk = chunk[:len(chunk)+1]
	} else {
		chunk = append(chunk, activation{})
	}
	r.chunks[chunkIndex] = chunk
	row := &r.chunks[chunkIndex][len(chunk)-1]
	r.count++
	return row
}

func (r *activationRows) truncate(count int) {
	if r == nil || count < 0 || count >= r.count {
		return
	}
	for r.count > count {
		r.count--
		chunkIndex := r.count / activationRowChunkSize
		chunk := r.chunks[chunkIndex]
		last := len(chunk) - 1
		chunk[last] = activation{}
		r.chunks[chunkIndex] = chunk[:last]
	}
}

type compactAgendaEntryID uint32

type compactAgendaEntryHandle struct {
	id         compactAgendaEntryID
	generation uint32
}

func (h compactAgendaEntryHandle) isZero() bool {
	return h.id == 0 || h.generation == 0
}

func (h compactAgendaEntryHandle) index() int {
	if h.id == 0 {
		return -1
	}
	return int(h.id - 1)
}

type compactAgendaEntry struct {
	key              activationKey
	ruleRevisionID   RuleRevisionID
	generation       Generation
	identity         candidateIdentity
	token            tokenRef
	terminalID       reteGraphTerminalNodeID
	terminalRow      graphTokenRowHandle
	salience         int
	maxRecency       Recency
	aggregateRecency Recency
	status           activationStatus
}

type compactAgendaEntryRow struct {
	entry      compactAgendaEntry
	generation uint32
	live       bool
}

type compactAgendaEntryArena struct {
	rows []compactAgendaEntryRow
	free []compactAgendaEntryID
	live int
}

func (a *compactAgendaEntryArena) reserve(capacity int) {
	if a == nil || capacity <= cap(a.rows) {
		return
	}
	rows := make([]compactAgendaEntryRow, len(a.rows), capacity)
	copy(rows, a.rows)
	a.rows = rows
	if capacity > cap(a.free) {
		free := make([]compactAgendaEntryID, len(a.free), capacity)
		copy(free, a.free)
		a.free = free
	}
}

func (a *compactAgendaEntryArena) reset() {
	if a == nil {
		return
	}
	a.free = a.free[:0]
	for index := range a.rows {
		row := &a.rows[index]
		if row.live {
			row.generation++
			if row.generation == 0 {
				row.generation = 1
			}
		}
		row.entry = compactAgendaEntry{}
		row.live = false
		a.free = append(a.free, compactAgendaEntryID(index+1))
	}
	a.live = 0
}

func (a *compactAgendaEntryArena) add(entry compactAgendaEntry) (compactAgendaEntryHandle, *compactAgendaEntry) {
	if a == nil {
		return compactAgendaEntryHandle{}, nil
	}
	rowID := a.allocateRowID()
	if rowID == 0 {
		return compactAgendaEntryHandle{}, nil
	}
	row := &a.rows[int(rowID)-1]
	if row.generation == 0 {
		row.generation = 1
	}
	row.entry = entry
	row.live = true
	a.live++
	handle := compactAgendaEntryHandle{id: rowID, generation: row.generation}
	return handle, &row.entry
}

func (a *compactAgendaEntryArena) allocateRowID() compactAgendaEntryID {
	if a == nil {
		return 0
	}
	for len(a.free) > 0 {
		last := len(a.free) - 1
		id := a.free[last]
		a.free[last] = 0
		a.free = a.free[:last]
		index := int(id) - 1
		if id == 0 || index < 0 || index >= len(a.rows) || a.rows[index].live {
			continue
		}
		return id
	}
	if uint64(len(a.rows)) >= uint64(^uint32(0)) {
		return 0
	}
	id := compactAgendaEntryID(len(a.rows) + 1)
	a.rows = append(a.rows, compactAgendaEntryRow{generation: 1})
	return id
}

func (a *compactAgendaEntryArena) get(handle compactAgendaEntryHandle) (*compactAgendaEntry, bool) {
	if a == nil || handle.isZero() {
		return nil, false
	}
	index := handle.index()
	if index < 0 || index >= len(a.rows) {
		return nil, false
	}
	row := &a.rows[index]
	if !row.live || row.generation != handle.generation {
		return nil, false
	}
	return &row.entry, true
}

func (a *compactAgendaEntryArena) remove(handle compactAgendaEntryHandle) bool {
	if a == nil || handle.isZero() {
		return false
	}
	index := handle.index()
	if index < 0 || index >= len(a.rows) {
		return false
	}
	row := &a.rows[index]
	if !row.live || row.generation != handle.generation {
		return false
	}
	row.entry = compactAgendaEntry{}
	row.live = false
	row.generation++
	if row.generation == 0 {
		row.generation = 1
	}
	a.free = append(a.free, handle.id)
	if a.live > 0 {
		a.live--
	}
	return true
}

func (a *compactAgendaEntryArena) len() int {
	if a == nil {
		return 0
	}
	return a.live
}

type activationKeyBucket struct {
	first    activationKey
	second   activationKey
	overflow []activationKey
	count    int
}

func (b activationKeyBucket) len() int {
	return b.count
}

func (b *activationKeyBucket) append(key activationKey) {
	switch b.count {
	case 0:
		b.first = key
	case 1:
		b.second = key
	default:
		if b.overflow == nil {
			b.overflow = make([]activationKey, 0, 8)
		}
		b.overflow = append(b.overflow, key)
	}
	b.count++
}

func (b activationKeyBucket) forEach(fn func(activationKey)) {
	if b.count == 0 || fn == nil {
		return
	}
	fn(b.first)
	if b.count == 1 {
		return
	}
	fn(b.second)
	for i := 0; i < b.count-2 && i < len(b.overflow); i++ {
		fn(b.overflow[i])
	}
}

func (b *activationKeyBucket) reset() activationKeyBucket {
	clear(b.overflow)
	b.first = activationKey{}
	b.second = activationKey{}
	b.overflow = b.overflow[:0]
	b.count = 0
	return *b
}

type activationStatus uint8

const (
	activationStatusPending activationStatus = iota
	activationStatusConsumed
	activationStatusDeactivated
)

func (s activationStatus) String() string {
	switch s {
	case activationStatusPending:
		return "pending"
	case activationStatusConsumed:
		return "consumed"
	case activationStatusDeactivated:
		return "deactivated"
	default:
		return "unknown"
	}
}

type activation struct {
	key              activationKey
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	generation       Generation
	identity         candidateIdentity
	token            tokenRef
	terminalID       reteGraphTerminalNodeID
	terminalRow      graphTokenRowHandle
	salience         int
	maxRecency       Recency
	aggregateRecency Recency
	status           activationStatus
	payload          *activationPayload
}

type activationPayload struct {
	bindings     []bindingTupleEntry
	path         []int
	factIDs      []FactID
	factVersions []FactVersion
}

func (a activation) mutationOrigin() mutationOrigin {
	return mutationOrigin{
		ActivationID:          a.activationID(),
		RuleID:                a.ruleID,
		RuleRevisionID:        a.ruleRevisionID,
		activationIdentityKey: a.identity.key,
		activationOrdinal:     a.key.ordinal,
	}
}

func (a activation) activationID() ActivationID {
	return activationIDForIdentityKey(a.identity.key, a.key.ordinal)
}

func (a *activation) ensureActivationID() ActivationID {
	if a == nil {
		return ""
	}
	return a.activationID()
}

func (a activation) clone() activation {
	out := a
	out.payload = nil
	out.setBindings(cloneBindingTupleEntries(a.bindings()))
	out.setFactIDs(cloneActivationFactIDs(&a))
	out.setFactVersions(cloneActivationFactVersions(&a))
	out.token = tokenRef{}
	out.setPath(cloneIntPath(a.path()))
	return out
}

func (a *activation) bindings() []bindingTupleEntry {
	if a == nil || a.payload == nil {
		return nil
	}
	return a.payload.bindings
}

func (a *activation) setBindings(bindings []bindingTupleEntry) {
	if a == nil {
		return
	}
	if len(bindings) == 0 {
		if a.payload != nil {
			a.payload.bindings = nil
			if a.payload.empty() {
				a.payload = nil
			}
		}
		return
	}
	a.ensurePayload().bindings = bindings
}

func (a *activation) path() []int {
	if a == nil || a.payload == nil {
		return nil
	}
	return a.payload.path
}

func (a *activation) setPath(path []int) {
	if a == nil {
		return
	}
	if len(path) == 0 {
		if a.payload != nil {
			a.payload.path = nil
			if a.payload.empty() {
				a.payload = nil
			}
		}
		return
	}
	a.ensurePayload().path = path
}

func (a *activation) factIDs() []FactID {
	if a == nil || a.payload == nil {
		return nil
	}
	return a.payload.factIDs
}

func (a *activation) setFactIDs(factIDs []FactID) {
	if a == nil {
		return
	}
	if len(factIDs) == 0 {
		if a.payload != nil {
			a.payload.factIDs = nil
			if a.payload.empty() {
				a.payload = nil
			}
		}
		return
	}
	a.ensurePayload().factIDs = factIDs
}

func (a *activation) factVersions() []FactVersion {
	if a == nil || a.payload == nil {
		return nil
	}
	return a.payload.factVersions
}

func (a *activation) setFactVersions(factVersions []FactVersion) {
	if a == nil {
		return
	}
	if len(factVersions) == 0 {
		if a.payload != nil {
			a.payload.factVersions = nil
			if a.payload.empty() {
				a.payload = nil
			}
		}
		return
	}
	a.ensurePayload().factVersions = factVersions
}

func (a *activation) ensurePayload() *activationPayload {
	if a.payload == nil {
		a.payload = &activationPayload{}
	}
	return a.payload
}

func (p *activationPayload) empty() bool {
	return p == nil || len(p.bindings) == 0 && len(p.path) == 0 && len(p.factIDs) == 0 && len(p.factVersions) == 0
}

type agendaChangeKind uint8

const (
	agendaChangeActivated agendaChangeKind = iota
	agendaChangeDeactivated
)

type agendaChange struct {
	kind       agendaChangeKind
	activation activation
}

func (c agendaChange) event(sessionID SessionID, rulesetID RulesetID, sequence uint64, timestamp time.Time) Event {
	eventType := EventRuleActivated
	if c.kind == agendaChangeDeactivated {
		eventType = EventRuleDeactivated
	}

	return Event{
		SessionID:      sessionID,
		RulesetID:      rulesetID,
		Sequence:       sequence,
		Timestamp:      timestamp,
		Type:           eventType,
		Generation:     c.activation.generation,
		Recency:        c.activation.maxRecency,
		RuleID:         c.activation.ruleID,
		RuleRevisionID: c.activation.ruleRevisionID,
		ActivationID:   c.activation.activationID(),
		FactIDs:        cloneActivationFactIDs(&c.activation),
	}
}

func (a *agenda) publicActivation(act *activation) activation {
	if act == nil {
		return activation{}
	}
	out := *act
	if out.ruleID.IsZero() {
		out.ruleID = a.ruleIDForRevision(out.ruleRevisionID)
	}
	out.payload = nil
	out.setFactIDs(cloneActivationFactIDs(act))
	out.setFactVersions(cloneActivationFactVersions(act))
	out.token = tokenRef{}
	out.terminalID = 0
	out.terminalRow = graphTokenRowHandle{}
	outFactIDs := out.factIDs()
	outFactVersions := out.factVersions()
	if a == nil || a.revision == nil || len(outFactIDs) == 0 {
		return out
	}
	rule, ok := a.revision.rulesByRevisionID[out.ruleRevisionID]
	if !ok {
		return out
	}
	if !act.token.isZero() {
		if factIDs, factVersions, ok := terminalTokenFactTuple(rule, act.token); ok {
			out.setFactIDs(factIDs)
			out.setFactVersions(factVersions)
			outFactIDs = factIDs
			outFactVersions = factVersions
		}
	}
	out.setBindings(activationBindingTupleEntries(rule, outFactIDs, outFactVersions, true))
	out.setPath(activationPathForRule(rule))
	return out
}

func (a *agenda) compactChangeActivation(act *activation) activation {
	if act == nil {
		return activation{}
	}
	out := *act
	if out.ruleID.IsZero() {
		out.ruleID = a.ruleIDForRevision(out.ruleRevisionID)
	}
	out.payload = nil
	out.setFactIDs(cloneActivationFactIDs(act))
	if a != nil && a.revision != nil && !act.token.isZero() {
		if rule, ok := a.revision.rulesByRevisionID[out.ruleRevisionID]; ok {
			if factIDs, _, ok := terminalTokenFactTuple(rule, act.token); ok {
				out.setFactIDs(factIDs)
			}
		}
	}
	out.token = tokenRef{}
	out.terminalID = 0
	out.terminalRow = graphTokenRowHandle{}
	return out
}

func activationBindingTupleEntries(rule compiledRule, factIDs []FactID, factVersions []FactVersion, includePath bool) []bindingTupleEntry {
	if len(factIDs) == 0 || len(factIDs) != len(factVersions) || len(rule.conditions) == 0 || len(rule.conditionPlans) == 0 {
		return nil
	}
	n := min(len(rule.conditions), len(factIDs))
	entries := make([]bindingTupleEntry, n)
	for i := range n {
		condition := rule.conditions[i]
		plan, ok := rule.conditionPlanForBindingSlot(i)
		if !ok {
			return nil
		}
		entries[i] = bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    i,
			conditionOrder: condition.order,
			conditionID:    condition.id,
			factID:         factIDs[i],
			factVersion:    factVersions[i],
		}
		if includePath {
			entries[i].conditionPath = cloneIntPath(plan.path)
		}
	}
	return entries
}

func activationBindingTupleEntriesForActivation(rule compiledRule, act *activation, includePath bool) []bindingTupleEntry {
	if act == nil || len(rule.conditions) == 0 || len(rule.conditionPlans) == 0 {
		return nil
	}
	count := activationFactCount(act)
	if count == 0 || count != activationFactVersionCount(act) {
		return nil
	}
	n := min(len(rule.conditions), count)
	if n != count {
		return nil
	}
	entries := make([]bindingTupleEntry, n)
	if !act.token.isZero() {
		if fillActivationBindingTupleEntriesFromTokenRef(entries, rule, act.token, includePath, 0) == n {
			return entries
		}
	}
	factIDs := act.factIDs()
	factVersions := act.factVersions()
	for i := range n {
		condition := rule.conditions[i]
		plan, ok := rule.conditionPlanForBindingSlot(i)
		if !ok {
			return nil
		}
		entries[i] = bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    i,
			conditionOrder: condition.order,
			conditionID:    condition.id,
			factID:         factIDs[i],
			factVersion:    factVersions[i],
		}
		if includePath {
			entries[i].conditionPath = cloneIntPath(plan.path)
		}
	}
	return entries
}

func fillActivationBindingTupleEntriesFromTokenRef(entries []bindingTupleEntry, rule compiledRule, token tokenRef, includePath bool, index int) int {
	if token.isZero() {
		return index
	}
	row, ok := token.resolve()
	if !ok {
		return index
	}
	index = fillActivationBindingTupleEntriesFromTokenRef(entries, rule, token.parent(), includePath, index)
	if index >= len(entries) {
		return index
	}
	condition := rule.conditions[index]
	plan, ok := rule.conditionPlanForBindingSlot(index)
	if !ok {
		return index
	}
	match, ok := row.conditionMatch()
	if !ok {
		return index
	}
	entries[index] = bindingTupleEntry{
		binding:        condition.binding,
		bindingSlot:    index,
		conditionOrder: condition.order,
		conditionID:    condition.id,
		factID:         match.fact.ID(),
		factVersion:    match.fact.Version(),
		value:          cloneValue(match.value),
		hasValue:       match.hasValue,
	}
	if includePath {
		entries[index].conditionPath = cloneIntPath(plan.path)
	}
	return index + 1
}

func activationPathForRule(rule compiledRule) []int {
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return nil
	}
	pathLen := 0
	for i := range rule.conditions {
		plan, ok := rule.conditionPlanForBindingSlot(i)
		if !ok {
			return nil
		}
		pathLen += len(plan.path)
	}
	path := make([]int, 0, pathLen)
	for i := range rule.conditions {
		plan, ok := rule.conditionPlanForBindingSlot(i)
		if !ok {
			return nil
		}
		path = append(path, plan.path...)
	}
	return path
}

type agenda struct {
	activations         map[activationFingerprint]activationBucket
	activationRows      activationRows
	pending             []activationKey
	pendingActivation   []*activation
	pendingHead         int
	lazyDeactivated     int
	byFactID            map[FactID]activationKeyBucket
	byRevision          map[RuleRevisionID]activationKeyBucket
	tokenFactIndexDirty bool
	revisionIndexDirty  bool
	nextOrdinal         uint64
	handleGeneration    uint32
	revision            *Ruleset
	propagationCounters *propagationCounterLedger

	reconcileSeen        map[activationKey]struct{}
	reconcileNextPending []activationKey
	reconcileChanges     []agendaChange
	reconcileActivated   []agendaChange

	deltaRemovedKeys map[activationKey]struct{}
	deltaNextPending []activationKey
	deltaChanges     []agendaChange
	deltaActivated   []agendaChange

	purgeActivations map[activationFingerprint]activationBucket
	purgeNextPending []activationKey
	purgeChanges     []agendaChange

	sortEntries []activationSortEntry
}

func newAgenda() *agenda {
	return &agenda{
		activations:      make(map[activationFingerprint]activationBucket),
		byFactID:         make(map[FactID]activationKeyBucket),
		byRevision:       make(map[RuleRevisionID]activationKeyBucket),
		handleGeneration: 1,
	}
}

func (a *agenda) normalizePendingKeys() []activationKey {
	if a == nil {
		return nil
	}
	if a.pendingHead <= 0 {
		return a.pending
	}
	if a.pendingHead >= len(a.pending) {
		clear(a.pending)
		a.pending = a.pending[:0]
		a.invalidatePendingActivationCache()
		a.pendingHead = 0
		return a.pending
	}
	if len(a.pendingActivation) == len(a.pending) {
		copy(a.pendingActivation, a.pendingActivation[a.pendingHead:])
		clear(a.pendingActivation[len(a.pendingActivation)-a.pendingHead:])
		a.pendingActivation = a.pendingActivation[:len(a.pending)-a.pendingHead]
	} else {
		a.invalidatePendingActivationCache()
	}
	copy(a.pending, a.pending[a.pendingHead:])
	clear(a.pending[len(a.pending)-a.pendingHead:])
	a.pending = a.pending[:len(a.pending)-a.pendingHead]
	a.pendingHead = 0
	return a.pending
}

func (a *agenda) resetPendingKeys() {
	if a == nil {
		return
	}
	clear(a.pending)
	a.pending = a.pending[:0]
	a.invalidatePendingActivationCache()
	a.pendingHead = 0
}

func (a *agenda) invalidatePendingActivationCache() {
	if a == nil {
		return
	}
	clear(a.pendingActivation)
	a.pendingActivation = a.pendingActivation[:0]
}

func (a *agenda) rebuildPendingActivationCache() {
	if a == nil {
		return
	}
	if len(a.pending) == 0 {
		a.invalidatePendingActivationCache()
		return
	}
	if cap(a.pendingActivation) < len(a.pending) {
		a.pendingActivation = make([]*activation, len(a.pending))
	} else {
		a.pendingActivation = a.pendingActivation[:len(a.pending)]
	}
	for i, key := range a.pending {
		current, _ := a.activationByKeyPtr(key)
		a.pendingActivation[i] = current
	}
}

func (a *agenda) reset() {
	if a == nil {
		return
	}
	if a.activations == nil {
		a.activations = make(map[activationFingerprint]activationBucket)
	} else {
		clear(a.activations)
	}
	a.resetPendingKeys()
	a.lazyDeactivated = 0
	a.activationRows.reset()
	if a.byFactID == nil {
		a.byFactID = make(map[FactID]activationKeyBucket)
	} else {
		clear(a.byFactID)
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID]activationKeyBucket)
	} else {
		clear(a.byRevision)
	}
	a.purgeActivations = nil
	a.tokenFactIndexDirty = false
	a.revisionIndexDirty = false
	a.nextOrdinal = 0
	a.advanceHandleGeneration()
}

func (a *agenda) reconcile(ctx context.Context, revision *Ruleset, results []ruleMatchResult) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.revision = revision
	a.normalizePendingKeys()

	seen := a.reconcileSeen
	if seen == nil {
		seen = make(map[activationKey]struct{}, len(a.pending))
	} else {
		clear(seen)
	}
	nextPending := a.reconcileNextPending[:0]
	changes := a.reconcileChanges[:0]
	activated := a.reconcileActivated[:0]
	oldPending := a.pending

	for _, result := range results {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := revision.rulesByRevisionID[result.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, result.ruleRevisionID)
		}
		if result.ruleID != rule.id {
			return nil, fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, result.ruleRevisionID)
		}

		for _, candidate := range result.candidates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			existing, key, ok := a.activationForCandidate(candidate)
			if ok {
				if existing.status == activationStatusPending {
					if _, seenBefore := seen[key]; !seenBefore {
						seen[key] = struct{}{}
						nextPending = append(nextPending, key)
					}
				}
				continue
			}

			created := a.activationRows.addEmpty()
			fillActivationFromCandidate(created, rule, candidate)
			key = a.storePreparedActivation(created)

			if _, seenBefore := seen[key]; !seenBefore {
				seen[key] = struct{}{}
				nextPending = append(nextPending, key)
			}
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(created),
			})
		}
	}

	for _, key := range oldPending {
		if _, ok := seen[key]; ok {
			continue
		}
		existing, ok := a.activationByKeyPtr(key)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		existing.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(existing),
		})
	}

	changes = append(changes, activated...)

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	a.sortActivationKeys(nextPending)

	a.reconcileSeen = seen
	a.reconcileNextPending = oldPending[:0]
	a.reconcileChanges = changes[:0]
	a.reconcileActivated = activated[:0]
	a.pending = nextPending
	a.rebuildPendingActivationCache()
	return append([]agendaChange(nil), changes...), nil
}

func (a *agenda) applyCandidateDeltas(ctx context.Context, revision *Ruleset, removed []matchCandidate, added []matchCandidate) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.revision = revision
	a.normalizePendingKeys()

	removedKeys := a.deltaRemovedKeys
	if removedKeys == nil {
		removedKeys = make(map[activationKey]struct{}, len(removed))
	} else {
		clear(removedKeys)
	}
	for _, candidate := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		existing, key, ok := a.activationForCandidate(candidate)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		removedKeys[key] = struct{}{}
	}

	changes := a.deltaChanges[:0]
	if len(removedKeys) > 0 {
		nextPending := a.deltaNextPending[:0]
		for _, key := range a.pending {
			existing, ok := a.activationByKeyPtr(key)
			if !ok || existing.status != activationStatusPending {
				continue
			}
			if _, remove := removedKeys[key]; remove {
				existing.status = activationStatusDeactivated
				changes = append(changes, agendaChange{
					kind:       agendaChangeDeactivated,
					activation: a.compactChangeActivation(existing),
				})
				continue
			}
			nextPending = append(nextPending, key)
		}
		a.deltaNextPending = a.pending[:0]
		a.pending = nextPending
		a.invalidatePendingActivationCache()
	}

	activated := a.deltaActivated[:0]
	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	sortMatchCandidates(revision, added)
	a.reservePendingActivationKeys(len(added))
	for _, candidate := range added {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := revision.rulesByRevisionID[candidate.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, candidate.ruleRevisionID)
		}
		if candidate.ruleID != rule.id {
			return nil, fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, candidate.ruleRevisionID)
		}
		if _, _, ok := a.activationForCandidate(candidate); ok {
			continue
		}

		created := a.activationRows.addEmpty()
		fillActivationFromCandidate(created, rule, candidate)
		key := a.storePreparedActivation(created)
		a.pending = a.insertActivationKeySorted(a.pending, key, created)
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: a.compactChangeActivation(created),
		})
	}
	changes = append(changes, activated...)

	a.deltaRemovedKeys = removedKeys
	a.deltaChanges = changes[:0]
	a.deltaActivated = activated[:0]
	return append([]agendaChange(nil), changes...), nil
}

func (a *agenda) applyTerminalTokenDeltas(ctx context.Context, revision *Ruleset, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta) ([]agendaChange, error) {
	return a.applyTerminalTokenDeltasInternal(ctx, revision, removed, added, true, nil)
}

func (a *agenda) applyTerminalTokenDeltasWithoutChanges(ctx context.Context, revision *Ruleset, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta) error {
	if len(removed) <= 1 && len(added) <= 1 {
		_, err := a.applySingleTerminalTokenDeltasWithoutChanges(ctx, revision, removed, added)
		return err
	}
	_, err := a.applyTerminalTokenDeltasInternal(ctx, revision, removed, added, false, nil)
	return err
}

func (a *agenda) addInitialTerminalActivation(ctx context.Context, revision *Ruleset, delta reteTerminalTokenDelta) (*activation, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if delta.token.isZero() {
		return nil, nil
	}
	a.revision = revision
	rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
	if !ok {
		return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
	}
	identity := candidateIdentityForTerminalTokenDelta(revision, delta)
	if existing, _, ok := a.activationForTerminalTokenIdentity(rule, delta.token, identity); ok {
		existing.terminalID = delta.terminalID
		existing.terminalRow = delta.terminalRow
		if existing.status == activationStatusDeactivated {
			existing.status = activationStatusPending
		}
		return existing, nil
	}
	created, err := newActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
	if err != nil {
		return nil, err
	}
	created.terminalID = delta.terminalID
	created.terminalRow = delta.terminalRow
	key := a.storePreparedActivation(created)
	a.pending = append(a.pending, key)
	return created, nil
}

func (a *agenda) finishInitialTerminalActivations() {
	if a == nil {
		return
	}
	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	a.sortActivationKeys(a.pending)
	a.pendingHead = 0
	a.lazyDeactivated = 0
	a.rebuildPendingActivationCache()
}

func (a *agenda) applySingleTerminalTokenDeltasWithoutChanges(ctx context.Context, revision *Ruleset, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta) (*activation, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.revision = revision
	a.normalizePendingKeys()

	if len(removed) == 1 {
		delta := removed[0]
		if !delta.token.isZero() {
			rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
			if !ok {
				return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
			}
			existing, _, ok := a.activationForTerminalTokenDelta(rule, delta)
			if ok {
				switch existing.status {
				case activationStatusPending:
					existing.status = activationStatusDeactivated
					a.lazyDeactivated++
				case activationStatusConsumed:
					existing.status = activationStatusDeactivated
				}
			}
		}
	}

	if len(added) != 1 {
		return nil, nil
	}
	delta := added[0]
	if delta.token.isZero() {
		return nil, nil
	}
	rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
	if !ok {
		return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
	}
	identity := candidateIdentityForTerminalTokenDelta(revision, delta)
	if existing, key, ok := a.activationForTerminalTokenIdentity(rule, delta.token, identity); ok {
		existing.terminalID = delta.terminalID
		existing.terminalRow = delta.terminalRow
		if existing.status == activationStatusDeactivated {
			existing.status = activationStatusPending
			pending := activationKeyPending(a.pending, key)
			if pending && a.lazyDeactivated > 0 {
				a.lazyDeactivated--
			}
			if !pending {
				a.pending = a.insertActivationKeySorted(a.pending, key, existing)
			}
		}
		return existing, nil
	}
	a.reservePendingActivationKeys(1)
	created, err := newActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
	if err != nil {
		return nil, err
	}
	created.terminalID = delta.terminalID
	created.terminalRow = delta.terminalRow
	key := a.storePreparedActivation(created)
	a.pending = a.insertActivationKeySorted(a.pending, key, created)
	if stored, ok := a.activationByKeyPtr(key); ok {
		return stored, nil
	}
	return created, nil
}

func (a *agenda) applyTerminalTokenDeltasInternal(ctx context.Context, revision *Ruleset, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta, collectChanges bool, observeActivation activationObserveFunc) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.revision = revision
	a.normalizePendingKeys()

	removedKeys := a.deltaRemovedKeys
	if removedKeys == nil {
		removedKeys = make(map[activationKey]struct{}, len(removed))
	} else {
		clear(removedKeys)
	}
	for _, delta := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token.isZero() {
			continue
		}
		rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
		}
		existing, key, ok := a.activationForTerminalTokenDelta(rule, delta)
		if !ok || existing.status != activationStatusPending {
			if ok && existing.status == activationStatusConsumed {
				existing.status = activationStatusDeactivated
			}
			continue
		}
		if !collectChanges {
			existing.status = activationStatusDeactivated
			a.lazyDeactivated++
			continue
		}
		removedKeys[key] = struct{}{}
	}

	var changes []agendaChange
	if collectChanges {
		changes = a.deltaChanges[:0]
	}
	if len(removedKeys) > 0 {
		nextPending := a.deltaNextPending[:0]
		for _, key := range a.pending {
			existing, ok := a.activationByKeyPtr(key)
			if !ok || existing.status != activationStatusPending {
				continue
			}
			if _, remove := removedKeys[key]; remove {
				existing.status = activationStatusDeactivated
				if collectChanges {
					changes = append(changes, agendaChange{
						kind:       agendaChangeDeactivated,
						activation: a.compactChangeActivation(existing),
					})
				}
				continue
			}
			nextPending = append(nextPending, key)
		}
		a.deltaNextPending = a.pending[:0]
		a.pending = nextPending
		a.invalidatePendingActivationCache()
	}

	if len(added) > 1 && !terminalTokenDeltasSorted(revision, added) {
		if a.propagationCounters != nil {
			a.propagationCounters.recordAgendaSort()
		}
		sortTerminalTokenDeltas(revision, added)
	}
	a.reservePendingActivationKeys(len(added))

	var previous reteTerminalTokenDelta
	havePrevious := false
	var activated []agendaChange
	if collectChanges {
		activated = a.deltaActivated[:0]
	}
	for _, delta := range added {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token.isZero() {
			continue
		}
		rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
		}
		if havePrevious && terminalTokenDeltasEqual(revision, previous, delta) {
			continue
		}
		previous = delta
		havePrevious = true
		identity := candidateIdentityForTerminalTokenDelta(revision, delta)
		if existing, key, ok := a.activationForTerminalTokenIdentity(rule, delta.token, identity); ok {
			existing.terminalID = delta.terminalID
			existing.terminalRow = delta.terminalRow
			if existing.status == activationStatusDeactivated {
				existing.status = activationStatusPending
				pending := activationKeyPending(a.pending, key)
				if pending && a.lazyDeactivated > 0 {
					a.lazyDeactivated--
				}
				if collectChanges || !pending {
					a.pending = a.insertActivationKeySorted(a.pending, key, existing)
				}
				if collectChanges {
					activated = append(activated, agendaChange{
						kind:       agendaChangeActivated,
						activation: a.compactChangeActivation(existing),
					})
				}
			}
			if observeActivation != nil {
				observeActivation(existing)
			}
			continue
		}
		created, err := newActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
		if err != nil {
			return nil, err
		}
		created.terminalID = delta.terminalID
		created.terminalRow = delta.terminalRow
		key := a.storePreparedActivation(created)
		a.pending = a.insertActivationKeySorted(a.pending, key, created)
		if observeActivation != nil {
			observeActivation(created)
		}
		if collectChanges {
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(created),
			})
		}
	}
	if collectChanges {
		changes = append(changes, activated...)
	}

	a.deltaRemovedKeys = removedKeys
	if collectChanges {
		a.deltaChanges = changes[:0]
		a.deltaActivated = activated[:0]
		return append([]agendaChange(nil), changes...), nil
	}
	a.deltaChanges = a.deltaChanges[:0]
	a.deltaActivated = a.deltaActivated[:0]
	return nil, nil
}

func (a *agenda) applyTerminalTokenUpdates(ctx context.Context, revision *Ruleset, updates []reteTerminalTokenUpdate) error {
	if a == nil || revision == nil {
		return ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.revision = revision
	a.normalizePendingKeys()
	refreshPendingOrder := false
	for _, update := range updates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if update.before.isZero() || update.after.isZero() {
			continue
		}
		rule, ok := revision.rulesByRevisionID[update.ruleRevisionID]
		if !ok {
			return fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, update.ruleRevisionID)
		}
		identity := update.identity
		if identity.isZero() {
			identity = candidateIdentityForTerminalToken(rule, update.before)
		}
		existing, _, ok := a.activationForTerminalTokenIdentity(rule, update.before, identity)
		if !ok {
			continue
		}
		existing.token = update.after
		existing.generation = tokenRefGeneration(update.after)
		existing.maxRecency = update.after.maxRecency()
		existing.aggregateRecency = update.after.aggregateRecency()
		if existing.status == activationStatusPending {
			refreshPendingOrder = true
			continue
		}
	}
	if refreshPendingOrder {
		a.sortActivationKeys(a.pending)
		a.rebuildPendingActivationCache()
	}
	return nil
}

func (a *agenda) reconcileTerminalTokens(ctx context.Context, revision *Ruleset, deltas []reteTerminalTokenDelta) ([]agendaChange, error) {
	return a.reconcileTerminalTokensInternal(ctx, revision, deltas, true)
}

func (a *agenda) reconcileTerminalTokensWithoutChanges(ctx context.Context, revision *Ruleset, deltas []reteTerminalTokenDelta) error {
	_, err := a.reconcileTerminalTokensInternal(ctx, revision, deltas, false)
	return err
}

func (a *agenda) reconcileGraphTerminalRows(ctx context.Context, revision *Ruleset, memory *reteGraphBetaMemory, collectChanges bool) ([]agendaChange, bool, error) {
	if a == nil || revision == nil {
		return nil, true, ErrInvalidRuleset
	}
	if memory == nil || memory.graph == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	a.revision = revision
	a.normalizePendingKeys()

	seen := a.reconcileSeen
	if seen == nil {
		seen = make(map[activationKey]struct{}, len(a.pending))
	} else {
		clear(seen)
	}
	nextPending := a.reconcileNextPending[:0]
	var changes []agendaChange
	var activated []agendaChange
	if collectChanges {
		changes = a.reconcileChanges[:0]
		activated = a.reconcileActivated[:0]
	}
	for _, terminalNode := range memory.graph.terminalNodes {
		if terminalNode.kind != reteGraphTerminalRule {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, true, err
		}
		rule, ok := revision.rulesByRevisionID[terminalNode.ruleRevisionID]
		if !ok {
			return nil, true, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, terminalNode.ruleRevisionID)
		}
		terminal := memory.terminalAt(terminalNode.id)
		if terminal == nil {
			continue
		}
		for rowIndex, row := range terminal.rows.rows {
			if row.token.isZero() {
				continue
			}
			handle := terminal.rows.rowHandle(graphTokenRowID(rowIndex), row)
			token := terminal.rows.rowToken(row)
			identity := terminal.terminalTokenIdentity(token)
			if existing, key, ok := a.activationForTerminalTokenIdentity(rule, token, identity); ok {
				existing.terminalID = terminalNode.id
				existing.terminalRow = handle
				if existing.status == activationStatusPending {
					if _, seenBefore := seen[key]; !seenBefore {
						seen[key] = struct{}{}
						nextPending = append(nextPending, key)
					}
				} else if existing.status == activationStatusDeactivated {
					existing.status = activationStatusPending
					if _, seenBefore := seen[key]; !seenBefore {
						seen[key] = struct{}{}
						nextPending = append(nextPending, key)
					}
					if collectChanges {
						activated = append(activated, agendaChange{
							kind:       agendaChangeActivated,
							activation: a.compactChangeActivation(existing),
						})
					}
				}
				continue
			}
			created, err := newActivationFromTerminalTokenWithIdentity(rule, token, identity)
			if err != nil {
				return nil, true, err
			}
			created.terminalID = terminalNode.id
			created.terminalRow = handle
			key := a.storePreparedActivation(created)
			if _, seenBefore := seen[key]; !seenBefore {
				seen[key] = struct{}{}
				nextPending = append(nextPending, key)
			}
			if collectChanges {
				activated = append(activated, agendaChange{
					kind:       agendaChangeActivated,
					activation: a.compactChangeActivation(created),
				})
			}
		}
	}

	for _, key := range a.pending {
		if _, ok := seen[key]; ok {
			continue
		}
		existing, ok := a.activationByKeyPtr(key)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		existing.status = activationStatusDeactivated
		if collectChanges {
			changes = append(changes, agendaChange{
				kind:       agendaChangeDeactivated,
				activation: a.compactChangeActivation(existing),
			})
		}
	}

	if collectChanges {
		changes = append(changes, activated...)
	}

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	a.sortActivationKeys(nextPending)
	a.reconcileSeen = seen
	a.reconcileNextPending = a.pending[:0]
	a.pending = nextPending
	a.rebuildPendingActivationCache()
	a.reconcileChanges = a.reconcileChanges[:0]
	a.reconcileActivated = a.reconcileActivated[:0]
	if collectChanges {
		return append([]agendaChange(nil), changes...), true, nil
	}
	return nil, true, nil
}

func (a *agenda) reconcileTerminalTokensInternal(ctx context.Context, revision *Ruleset, deltas []reteTerminalTokenDelta, collectChanges bool) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.revision = revision
	a.normalizePendingKeys()

	seen := a.reconcileSeen
	if seen == nil {
		seen = make(map[activationKey]struct{}, max(len(a.pending), len(deltas)))
	} else {
		clear(seen)
	}
	nextPending := a.reconcileNextPending[:0]
	var changes []agendaChange
	var activated []agendaChange
	if collectChanges {
		changes = a.reconcileChanges[:0]
		activated = a.reconcileActivated[:0]
	}

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	sortTerminalTokenDeltas(revision, deltas)

	var previous reteTerminalTokenDelta
	havePrevious := false
	for _, delta := range deltas {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token.isZero() {
			continue
		}
		rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
		}
		if havePrevious && terminalTokenDeltasEqual(revision, previous, delta) {
			continue
		}
		previous = delta
		havePrevious = true

		identity := candidateIdentityForTerminalTokenDelta(revision, delta)
		existing, key, ok := a.activationForTerminalTokenIdentity(rule, delta.token, identity)
		if ok {
			existing.terminalID = delta.terminalID
			existing.terminalRow = delta.terminalRow
			if existing.status == activationStatusPending {
				if _, seenBefore := seen[key]; !seenBefore {
					seen[key] = struct{}{}
					nextPending = append(nextPending, key)
				}
			} else if existing.status == activationStatusDeactivated {
				existing.status = activationStatusPending
				if _, seenBefore := seen[key]; !seenBefore {
					seen[key] = struct{}{}
					nextPending = append(nextPending, key)
				}
				if collectChanges {
					activated = append(activated, agendaChange{
						kind:       agendaChangeActivated,
						activation: a.compactChangeActivation(existing),
					})
				}
			}
			continue
		}

		created, err := newActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
		if err != nil {
			return nil, err
		}
		created.terminalID = delta.terminalID
		created.terminalRow = delta.terminalRow
		key = a.storePreparedActivation(created)
		if _, seenBefore := seen[key]; !seenBefore {
			seen[key] = struct{}{}
			nextPending = append(nextPending, key)
		}
		if collectChanges {
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(created),
			})
		}
	}

	for _, key := range a.pending {
		existing, ok := a.activationByKeyPtr(key)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		existing.status = activationStatusDeactivated
		if collectChanges {
			changes = append(changes, agendaChange{
				kind:       agendaChangeDeactivated,
				activation: a.compactChangeActivation(existing),
			})
		}
	}

	if collectChanges {
		changes = append(changes, activated...)
	}

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	a.sortActivationKeys(nextPending)

	a.reconcileSeen = seen
	a.reconcileNextPending = a.pending[:0]
	a.pending = nextPending
	a.rebuildPendingActivationCache()
	if !collectChanges {
		a.reconcileChanges = a.reconcileChanges[:0]
		a.reconcileActivated = a.reconcileActivated[:0]
		return nil, nil
	}
	a.reconcileChanges = changes[:0]
	a.reconcileActivated = activated[:0]
	return append([]agendaChange(nil), changes...), nil
}

func agendaDeltaCandidateLess(revision *Ruleset, left, right matchCandidate) bool {
	if revision != nil {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.declarationOrder != rightRule.declarationOrder {
			return leftRule.declarationOrder < rightRule.declarationOrder
		}
	}
	for i := 0; i < len(left.factIDs) && i < len(right.factIDs); i++ {
		if left.factIDs[i] != right.factIDs[i] {
			return factIDLess(left.factIDs[i], right.factIDs[i])
		}
		if left.factVersions[i] != right.factVersions[i] {
			return left.factVersions[i] < right.factVersions[i]
		}
	}
	if len(left.factIDs) != len(right.factIDs) {
		return len(left.factIDs) < len(right.factIDs)
	}
	if left.ruleID != right.ruleID {
		return left.ruleID < right.ruleID
	}
	return left.ruleRevisionID < right.ruleRevisionID
}

func sortMatchCandidates(revision *Ruleset, candidates []matchCandidate) {
	slices.SortStableFunc(candidates, func(left, right matchCandidate) int {
		return compareLess(
			agendaDeltaCandidateLess(revision, left, right),
			agendaDeltaCandidateLess(revision, right, left),
		)
	})
}

func agendaDeltaTerminalTokenLess(revision *Ruleset, left, right reteTerminalTokenDelta) bool {
	if revision != nil {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.declarationOrder != rightRule.declarationOrder {
			return leftRule.declarationOrder < rightRule.declarationOrder
		}
	}
	compare := compareTerminalTokenDeltaFacts(left, right)
	if compare != 0 {
		return compare < 0
	}
	if revision != nil && left.ruleRevisionID != right.ruleRevisionID {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.id != rightRule.id {
			return leftRule.id < rightRule.id
		}
	}
	return left.ruleRevisionID < right.ruleRevisionID
}

func compareTerminalTokenDeltaFacts(left, right reteTerminalTokenDelta) int {
	if len(left.factIDs) > 0 || len(right.factIDs) > 0 || len(left.factVersions) > 0 || len(right.factVersions) > 0 {
		return compareFactVersionSlices(left.factIDs, left.factVersions, right.factIDs, right.factVersions)
	}
	return compareTerminalTokenFacts(left.token, right.token)
}

func sortTerminalTokenDeltas(revision *Ruleset, deltas []reteTerminalTokenDelta) {
	slices.SortStableFunc(deltas, func(left, right reteTerminalTokenDelta) int {
		return compareTerminalTokenDeltaOrder(revision, left, right)
	})
}

func terminalTokenDeltasSorted(revision *Ruleset, deltas []reteTerminalTokenDelta) bool {
	return slices.IsSortedFunc(deltas, func(left, right reteTerminalTokenDelta) int {
		return compareTerminalTokenDeltaOrder(revision, left, right)
	})
}

func compareTerminalTokenDeltaOrder(revision *Ruleset, left, right reteTerminalTokenDelta) int {
	return compareLess(
		agendaDeltaTerminalTokenLess(revision, left, right),
		agendaDeltaTerminalTokenLess(revision, right, left),
	)
}

func terminalTokenDeltasEqual(revision *Ruleset, left, right reteTerminalTokenDelta) bool {
	if left.ruleRevisionID != right.ruleRevisionID {
		return false
	}
	if revision != nil {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.id != rightRule.id {
			return false
		}
	}
	leftIdentity := candidateIdentityForTerminalTokenDelta(revision, left)
	rightIdentity := candidateIdentityForTerminalTokenDelta(revision, right)
	if leftIdentity.key != rightIdentity.key {
		return false
	}
	if leftIdentity.generation != 0 && rightIdentity.generation != 0 && leftIdentity.generation != rightIdentity.generation {
		return false
	}
	if leftIdentity.count != 0 && rightIdentity.count != 0 && leftIdentity.count != rightIdentity.count {
		return false
	}
	if len(left.factIDs) > 0 || len(right.factIDs) > 0 || len(left.factVersions) > 0 || len(right.factVersions) > 0 {
		return factVersionSlicesEqual(left.factIDs, left.factVersions, right.factIDs, right.factVersions)
	}
	return terminalTokenFactVersionsEqual(left.token, right.token)
}

func compareLess(leftLess, rightLess bool) int {
	switch {
	case leftLess:
		return -1
	case rightLess:
		return 1
	default:
		return 0
	}
}

func (a *agenda) purgeRuleRevisions(revisionIDs map[RuleRevisionID]struct{}) []agendaChange {
	if a == nil || len(revisionIDs) == 0 {
		return nil
	}
	a.normalizePendingKeys()

	changes := a.purgeChanges[:0]
	for _, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current == nil {
			continue
		}
		if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
			continue
		}
		if current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(current),
		})
	}

	nextActivations := a.purgeActivations
	if nextActivations == nil {
		nextActivations = make(map[activationFingerprint]activationBucket, len(a.activations))
	} else {
		clear(nextActivations)
	}
	a.resetIndexesForRebuild()
	oldPending := a.pending
	oldActivationCount := len(a.activations)

	for identityKey, bucket := range a.activations {
		nextBucket := activationBucket{}
		overflow := bucket.overflow[:0]
		if current := bucket.first; current != nil {
			if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
				nextBucket.first = current
				a.indexActivation(current)
			}
		}
		if current := bucket.second; current != nil {
			if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
				if nextBucket.first == nil {
					nextBucket.first = current
				} else {
					nextBucket.second = current
				}
				a.indexActivation(current)
			}
		}
		for _, current := range bucket.overflow {
			if current == nil {
				continue
			}
			if _, ok := revisionIDs[current.ruleRevisionID]; ok {
				continue
			}
			if nextBucket.first == nil {
				nextBucket.first = current
			} else if nextBucket.second == nil {
				nextBucket.second = current
			} else {
				overflow = append(overflow, current)
			}
			a.indexActivation(current)
		}
		if len(bucket.overflow) > 0 {
			clear(bucket.overflow[len(overflow):])
		}
		if len(overflow) > 0 {
			nextBucket.overflow = overflow
		}
		if nextBucket.first != nil {
			nextActivations[identityKey] = nextBucket
		}
	}

	oldActivations := a.activations
	a.activations = nextActivations
	if len(nextActivations) == 0 || len(nextActivations)*4 < oldActivationCount {
		a.purgeActivations = nil
	} else {
		clear(oldActivations)
		a.purgeActivations = oldActivations
	}

	nextPending := a.purgeNextPending[:0]
	for _, key := range oldPending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current.status != activationStatusPending {
			if ok && current.status == activationStatusDeactivated && a.lazyDeactivated > 0 {
				a.lazyDeactivated--
			}
			continue
		}
		nextPending = append(nextPending, key)
	}

	a.pending = nextPending
	a.rebuildPendingActivationCache()
	a.purgeNextPending = oldPending[:0]
	a.pruneEmptyIndexes()
	out := append([]agendaChange(nil), changes...)
	clear(changes)
	a.purgeChanges = changes[:0]

	return out
}

func (a *agenda) next() (activation, bool) {
	return a.nextActivation()
}

func (a *agenda) nextInternal() (activation, bool) {
	return a.nextActivation()
}

func (a *agenda) nextActivation() (activation, bool) {
	_, out, ok := a.nextActivationPtr()
	return out, ok
}

func (a *agenda) nextInternalPtr() (*activation, activation, bool) {
	return a.nextActivationPtr()
}

func (a *agenda) nextInternalPtrForModule(module ModuleName) (*activation, activation, bool) {
	return a.nextActivationPtrForModule(module)
}

func (a *agenda) nextActivationPtr() (*activation, activation, bool) {
	if a == nil {
		return nil, activation{}, false
	}
	usePendingActivation := len(a.pendingActivation) == len(a.pending)
	for a.pendingHead < len(a.pending) {
		key := a.pending[a.pendingHead]
		var current *activation
		ok := false
		if usePendingActivation {
			current = a.pendingActivation[a.pendingHead]
			ok = current != nil && current.key == key
		}
		if !ok {
			current, ok = a.activationByKeyPtr(key)
		}
		a.pendingHead++

		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusConsumed
		selected := a.activationRunSnapshot(current)
		compactConsumedTokenActivation(current)
		return current, selected, true
	}
	a.pending = a.pending[:0]
	a.invalidatePendingActivationCache()
	a.pendingHead = 0
	a.lazyDeactivated = 0
	return nil, activation{}, false
}

func (a *agenda) nextActivationPtrForModule(module ModuleName) (*activation, activation, bool) {
	if a == nil {
		return nil, activation{}, false
	}
	usePendingActivation := len(a.pendingActivation) == len(a.pending)
	for a.pendingHead < len(a.pending) {
		key := a.pending[a.pendingHead]
		var current *activation
		ok := false
		if usePendingActivation {
			current = a.pendingActivation[a.pendingHead]
			ok = current != nil && current.key == key
		}
		if !ok {
			current, ok = a.activationByKeyPtr(key)
		}
		if !ok || current.status != activationStatusPending {
			a.pendingHead++
			continue
		}
		if a.activationModule(current) != module {
			break
		}
		a.pendingHead++
		current.status = activationStatusConsumed
		selected := a.activationRunSnapshot(current)
		compactConsumedTokenActivation(current)
		return current, selected, true
	}
	if a.pendingHead >= len(a.pending) {
		a.pending = a.pending[:0]
		a.invalidatePendingActivationCache()
		a.pendingHead = 0
		a.lazyDeactivated = 0
		return nil, activation{}, false
	}
	a.normalizePendingKeys()
	for i, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current.status != activationStatusPending || a.activationModule(current) != module {
			continue
		}
		copy(a.pending[i:], a.pending[i+1:])
		last := len(a.pending) - 1
		a.pending[last] = activationKey{}
		a.pending = a.pending[:last]
		a.invalidatePendingActivationCache()
		current.status = activationStatusConsumed
		selected := a.activationRunSnapshot(current)
		compactConsumedTokenActivation(current)
		return current, selected, true
	}
	return nil, activation{}, false
}

func compactConsumedTokenActivation(current *activation) {
	if current == nil || current.status != activationStatusConsumed || current.token.isZero() {
		return
	}
	current.payload = nil
	current.terminalID = 0
	current.terminalRow = graphTokenRowHandle{}
}

func (a *agenda) activationRunSnapshot(current *activation) activation {
	out := activationRunSnapshot(current)
	if out.ruleID.IsZero() {
		out.ruleID = a.ruleIDForRevision(out.ruleRevisionID)
	}
	return out
}

func (a *agenda) activationModule(act *activation) ModuleName {
	if a == nil || a.revision == nil || act == nil {
		return ""
	}
	rule, ok := a.revision.rulesByRevisionID[act.ruleRevisionID]
	if !ok {
		return ""
	}
	return rule.module
}

func (a *agenda) ruleIDForRevision(id RuleRevisionID) RuleID {
	if a == nil || a.revision == nil || id.IsZero() {
		return ""
	}
	rule, ok := a.revision.rulesByRevisionID[id]
	if !ok {
		return ""
	}
	return rule.id
}

func activationRunSnapshot(current *activation) activation {
	if current == nil {
		return activation{}
	}
	if current.token.isZero() {
		out := *current
		out.payload = nil
		out.setFactIDs(cloneFactIDs(current.factIDs()))
		out.setFactVersions(cloneFactVersions(current.factVersions()))
		return out
	}
	return activation{
		key:              current.key,
		ruleID:           current.ruleID,
		ruleRevisionID:   current.ruleRevisionID,
		generation:       current.generation,
		identity:         current.identity,
		token:            current.token,
		terminalID:       current.terminalID,
		terminalRow:      current.terminalRow,
		salience:         current.salience,
		maxRecency:       current.maxRecency,
		aggregateRecency: current.aggregateRecency,
		status:           current.status,
	}
}

func (a *agenda) clear() []agendaChange {
	if a == nil {
		return nil
	}
	a.normalizePendingKeys()
	changes := make([]agendaChange, 0, len(a.pending))
	for _, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(current),
		})
	}
	a.reset()
	return changes
}

func (a *agenda) materializePendingTokenFacts(revision *Ruleset) {
	if a == nil || revision == nil {
		return
	}
	a.normalizePendingKeys()
	for _, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current.status != activationStatusPending || current.token.isZero() {
			continue
		}
		if len(current.factIDs()) > 0 && len(current.factVersions()) > 0 {
			continue
		}
		rule, ok := revision.rulesByRevisionID[current.ruleRevisionID]
		if !ok {
			continue
		}
		factIDs, factVersions, ok := terminalTokenFactTuple(rule, current.token)
		if !ok {
			continue
		}
		current.setFactIDs(cloneFactIDs(factIDs))
		current.setFactVersions(cloneFactVersions(factVersions))
	}
}

func (a *agenda) activationByKey(key activationKey) (activation, bool) {
	if a == nil {
		return activation{}, false
	}
	current, ok := a.activationByKeyPtr(key)
	if !ok {
		return activation{}, false
	}
	return a.publicActivation(current), true
}

func (a *agenda) pendingActivations() []activation {
	if a == nil || len(a.pending) == 0 {
		return nil
	}
	a.normalizePendingKeys()
	if len(a.pending) == 0 {
		return nil
	}
	a.compactLazyPending(false)
	out := make([]activation, 0, len(a.pending))
	for _, key := range a.pending {
		if current, ok := a.activationByKeyPtr(key); ok && current.status == activationStatusPending {
			out = append(out, a.publicActivation(current))
		}
	}
	return out
}

func (a *agenda) forEachPendingActivation(fn func(*activation) bool) {
	if a == nil || fn == nil || len(a.pending) == 0 {
		return
	}
	usePendingActivation := len(a.pendingActivation) == len(a.pending)
	for i := a.pendingHead; i < len(a.pending); i++ {
		key := a.pending[i]
		var current *activation
		ok := false
		if usePendingActivation {
			current = a.pendingActivation[i]
			ok = current != nil && current.key == key
		}
		if !ok {
			current, ok = a.activationByKeyPtr(key)
		}
		if !ok || current.status != activationStatusPending {
			continue
		}
		if !fn(current) {
			return
		}
	}
}

func (a *agenda) activationsByFactID(id FactID) []activation {
	if a == nil {
		return nil
	}
	a.ensureFactIndex()
	bucket := a.byFactID[id]
	if bucket.len() == 0 {
		return nil
	}
	out := make([]activation, 0, bucket.len())
	bucket.forEach(func(key activationKey) {
		if current, ok := a.activationByKeyPtr(key); ok {
			out = append(out, a.publicActivation(current))
		}
	})
	sortActivations(out)
	return out
}

func (a *agenda) activationsByRuleRevisionID(id RuleRevisionID) []activation {
	if a == nil {
		return nil
	}
	a.ensureRevisionIndex()
	bucket := a.byRevision[id]
	if bucket.len() == 0 {
		return nil
	}
	out := make([]activation, 0, bucket.len())
	bucket.forEach(func(key activationKey) {
		if current, ok := a.activationByKeyPtr(key); ok {
			out = append(out, a.publicActivation(current))
		}
	})
	sortActivations(out)
	return out
}

func (a *agenda) rebuildIndexes() {
	if a == nil {
		return
	}
	a.resetIndexesForRebuild()
	for _, bucket := range a.activations {
		if current := bucket.first; current != nil {
			a.indexActivationFacts(current, false)
			a.indexActivationRevision(current)
		}
		if current := bucket.second; current != nil {
			a.indexActivationFacts(current, false)
			a.indexActivationRevision(current)
		}
		for _, current := range bucket.overflow {
			if current != nil {
				a.indexActivationFacts(current, false)
				a.indexActivationRevision(current)
			}
		}
	}
	a.pruneEmptyIndexes()
}

func (a *agenda) ensureFactIndex() {
	if a == nil || !a.tokenFactIndexDirty {
		return
	}
	if a.byFactID == nil {
		a.byFactID = make(map[FactID]activationKeyBucket)
	} else {
		resetActivationIndex(a.byFactID)
	}
	for _, bucket := range a.activations {
		if current := bucket.first; current != nil {
			a.indexActivationFacts(current, true)
		}
		if current := bucket.second; current != nil {
			a.indexActivationFacts(current, true)
		}
		for _, current := range bucket.overflow {
			if current != nil {
				a.indexActivationFacts(current, true)
			}
		}
	}
	pruneEmptyActivationIndex(a.byFactID)
	a.tokenFactIndexDirty = false
}

func (a *agenda) ensureRevisionIndex() {
	if a == nil || !a.revisionIndexDirty {
		return
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID]activationKeyBucket)
	} else {
		resetActivationIndex(a.byRevision)
	}
	for _, bucket := range a.activations {
		if current := bucket.first; current != nil {
			a.indexActivationRevision(current)
		}
		if current := bucket.second; current != nil {
			a.indexActivationRevision(current)
		}
		for _, current := range bucket.overflow {
			if current != nil {
				a.indexActivationRevision(current)
			}
		}
	}
	pruneEmptyActivationIndex(a.byRevision)
	a.revisionIndexDirty = false
}

func (a *agenda) resetIndexesForRebuild() {
	a.tokenFactIndexDirty = false
	a.revisionIndexDirty = false
	if a.byFactID == nil {
		a.byFactID = make(map[FactID]activationKeyBucket)
	} else {
		resetActivationIndex(a.byFactID)
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID]activationKeyBucket)
	} else {
		resetActivationIndex(a.byRevision)
	}
}

func (a *agenda) pruneEmptyIndexes() {
	if a == nil {
		return
	}
	pruneEmptyActivationIndex(a.byFactID)
	pruneEmptyActivationIndex(a.byRevision)
}

func (a *agenda) indexActivation(act *activation) {
	if a == nil || act == nil {
		return
	}
	if a.byFactID == nil {
		a.byFactID = make(map[FactID]activationKeyBucket)
	}

	a.indexActivationFacts(act, false)
	a.revisionIndexDirty = true
}

func (a *agenda) indexActivationRevision(act *activation) {
	if a == nil || act == nil {
		return
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID]activationKeyBucket)
	}
	revisionBucket := a.byRevision[act.ruleRevisionID]
	revisionBucket.append(act.key)
	a.byRevision[act.ruleRevisionID] = revisionBucket
}

func (a *agenda) indexActivationFacts(act *activation, includeTokenFacts bool) {
	if a == nil || act == nil {
		return
	}
	if a.byFactID == nil {
		a.byFactID = make(map[FactID]activationKeyBucket)
	}
	if !act.token.isZero() {
		if includeTokenFacts {
			a.indexActivationTokenFacts(act.key, act.token)
			return
		}
		a.tokenFactIndexDirty = true
		return
	}
	factIDs := act.factIDs()
	for i, factID := range factIDs {
		if factIDSeenBefore(factIDs[:i], factID) {
			continue
		}
		factBucket := a.byFactID[factID]
		factBucket.append(act.key)
		a.byFactID[factID] = factBucket
	}
}

func (a *agenda) indexActivationTokenFacts(key activationKey, token tokenRef) {
	if a == nil || token.isZero() {
		return
	}
	if factIDs, ok := token.factIDs(); ok {
		for i, factID := range factIDs {
			if factIDSeenBefore(factIDs[:i], factID) {
				continue
			}
			factBucket := a.byFactID[factID]
			factBucket.append(key)
			a.byFactID[factID] = factBucket
		}
		return
	}
	row, ok := token.resolve()
	if !ok {
		return
	}
	a.indexActivationTokenFacts(key, token.parent())
	match, ok := row.conditionMatch()
	if !ok {
		return
	}
	factID := match.fact.ID()
	if tokenRefContainsFactID(token.parent(), factID) {
		return
	}
	factBucket := a.byFactID[factID]
	factBucket.append(key)
	a.byFactID[factID] = factBucket
}

func tokenRefContainsFactID(token tokenRef, id FactID) bool {
	return token.containsFact(id)
}

func resetActivationIndex[K comparable](index map[K]activationKeyBucket) {
	for key, bucket := range index {
		index[key] = bucket.reset()
	}
}

func pruneEmptyActivationIndex[K comparable](index map[K]activationKeyBucket) {
	for key, bucket := range index {
		if bucket.len() == 0 {
			delete(index, key)
		}
	}
}

func factIDSeenBefore(ids []FactID, id FactID) bool {
	return slices.Contains(ids, id)
}

func (a *agenda) sortActivationKeys(keys []activationKey) {
	if a == nil || len(keys) < 2 {
		return
	}
	entries := a.sortEntries
	if cap(entries) < len(keys) {
		entries = make([]activationSortEntry, len(keys))
	} else {
		entries = entries[:len(keys)]
	}
	for i, key := range keys {
		act, _ := a.activationByKeyPtr(key)
		entries[i] = activationSortEntry{key: key, act: act}
	}
	slices.SortStableFunc(entries, func(left, right activationSortEntry) int {
		return a.activationCompare(left.act, right.act)
	})
	for i, entry := range entries {
		keys[i] = entry.key
		entries[i] = activationSortEntry{}
	}
	a.sortEntries = entries[:0]
	a.invalidatePendingActivationCache()
}

func (a *agenda) insertActivationKeySorted(keys []activationKey, key activationKey, act *activation) []activationKey {
	a.invalidatePendingActivationCache()
	if a == nil || act == nil || len(keys) == 0 {
		return append(keys, key)
	}
	if last, ok := a.activationByKeyPtr(keys[len(keys)-1]); ok && !a.activationLess(act, last) {
		return append(keys, key)
	}
	index := sort.Search(len(keys), func(i int) bool {
		existing, _ := a.activationByKeyPtr(keys[i])
		return a.activationLess(act, existing)
	})
	keys = append(keys, activationKey{})
	copy(keys[index+1:], keys[index:])
	keys[index] = key
	return keys
}

func (a *agenda) reservePendingActivationKeys(additional int) {
	if a == nil || additional <= 0 {
		return
	}
	a.normalizePendingKeys()
	a.pending = slices.Grow(a.pending, additional)
	a.pendingActivation = slices.Grow(a.pendingActivation, additional)
}

func sortActivations(activations []activation) {
	slices.SortStableFunc(activations, func(left, right activation) int {
		return activationCompare(&left, &right)
	})
}

func activationCompare(left, right *activation) int {
	return compareLess(activationLess(left, right), activationLess(right, left))
}

func (a *agenda) activationCompare(left, right *activation) int {
	return compareLess(a.activationLess(left, right), a.activationLess(right, left))
}

func activationLess(left, right *activation) bool {
	if left == nil || right == nil {
		return right != nil
	}
	if left.salience != right.salience {
		return left.salience > right.salience
	}
	if left.maxRecency != right.maxRecency {
		return left.maxRecency > right.maxRecency
	}
	if left.aggregateRecency != right.aggregateRecency {
		return left.aggregateRecency > right.aggregateRecency
	}
	if left.identity.key.scopeHash < right.identity.key.scopeHash {
		return true
	}
	if left.identity.key.scopeHash != right.identity.key.scopeHash {
		return false
	}
	if left.identity.key.hash < right.identity.key.hash {
		return true
	}
	if left.identity.key.hash != right.identity.key.hash {
		return false
	}
	if left.key.ordinal < right.key.ordinal {
		return true
	}
	if left.key.ordinal != right.key.ordinal {
		return false
	}
	return false
}

func (a *agenda) activationLess(left, right *activation) bool {
	if left == nil || right == nil {
		return right != nil
	}
	if left.salience != right.salience {
		return left.salience > right.salience
	}
	if left.maxRecency != right.maxRecency {
		return left.maxRecency > right.maxRecency
	}
	if left.aggregateRecency != right.aggregateRecency {
		return left.aggregateRecency > right.aggregateRecency
	}
	if a != nil && a.revision != nil && left.ruleRevisionID != right.ruleRevisionID {
		leftRule, leftOK := a.revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := a.revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.declarationOrder != rightRule.declarationOrder {
			return leftRule.declarationOrder < rightRule.declarationOrder
		}
	}
	if left.identity.key.scopeHash < right.identity.key.scopeHash {
		return true
	}
	if left.identity.key.scopeHash != right.identity.key.scopeHash {
		return false
	}
	if left.identity.key.hash < right.identity.key.hash {
		return true
	}
	if left.identity.key.hash != right.identity.key.hash {
		return false
	}
	if left.key.ordinal < right.key.ordinal {
		return true
	}
	if left.key.ordinal != right.key.ordinal {
		return false
	}
	return false
}

func (a *agenda) activationForCandidate(candidate matchCandidate) (*activation, activationKey, bool) {
	if a == nil {
		return nil, activationKey{}, false
	}
	fingerprint := activationFingerprintForIdentityKey(candidate.identity.key)
	bucket := a.activations[fingerprint]
	if current := bucket.first; current != nil && activationMatchesCandidate(current, candidate) {
		return current, current.key, true
	}
	if current := bucket.second; current != nil && activationMatchesCandidate(current, candidate) {
		return current, current.key, true
	}
	for _, current := range bucket.overflow {
		if current != nil && activationMatchesCandidate(current, candidate) {
			return current, current.key, true
		}
	}
	return nil, activationKey{}, false
}

func (a *agenda) activationForTerminalToken(rule compiledRule, token tokenRef) (*activation, activationKey, bool) {
	return a.activationForTerminalTokenIdentity(rule, token, candidateIdentityForTerminalToken(rule, token))
}

func (a *agenda) activationForTerminalTokenIdentity(rule compiledRule, token tokenRef, identity candidateIdentity) (*activation, activationKey, bool) {
	if a == nil {
		return nil, activationKey{}, false
	}
	if identity.isZero() {
		identity = candidateIdentityForTerminalToken(rule, token)
	}
	fingerprint := activationFingerprintForIdentityKey(identity.key)
	bucket := a.activations[fingerprint]
	if current := bucket.first; current != nil && activationMatchesTerminalToken(current, rule, identity, token) {
		return current, current.key, true
	}
	if current := bucket.second; current != nil && activationMatchesTerminalToken(current, rule, identity, token) {
		return current, current.key, true
	}
	for _, current := range bucket.overflow {
		if current != nil && activationMatchesTerminalToken(current, rule, identity, token) {
			return current, current.key, true
		}
	}
	return nil, activationKey{}, false
}

func (a *agenda) activationForTerminalTokenDelta(rule compiledRule, delta reteTerminalTokenDelta) (*activation, activationKey, bool) {
	if a == nil {
		return nil, activationKey{}, false
	}
	identity := candidateIdentityForTerminalTokenDelta(a.revision, delta)
	if identity.isZero() {
		identity = candidateIdentityForTerminalToken(rule, delta.token)
	}
	fingerprint := activationFingerprintForIdentityKey(identity.key)
	bucket := a.activations[fingerprint]
	if current := bucket.first; current != nil && activationMatchesTerminalTokenDelta(current, rule, identity, delta) {
		return current, current.key, true
	}
	if current := bucket.second; current != nil && activationMatchesTerminalTokenDelta(current, rule, identity, delta) {
		return current, current.key, true
	}
	for _, current := range bucket.overflow {
		if current != nil && activationMatchesTerminalTokenDelta(current, rule, identity, delta) {
			return current, current.key, true
		}
	}
	return nil, activationKey{}, false
}

func activationMatchesCandidate(current *activation, candidate matchCandidate) bool {
	if current == nil {
		return false
	}
	if current.ruleRevisionID != candidate.ruleRevisionID {
		return false
	}
	if !current.ruleID.IsZero() && current.ruleID != candidate.ruleID {
		return false
	}
	if current.identity.key != candidate.identity.key || current.identity.generation != candidate.identity.generation || current.identity.count != candidate.identity.count {
		return false
	}
	if !current.token.isZero() {
		return matchTokenFactsEqualSlices(current.token, candidate.factIDs, candidate.factVersions)
	}
	return candidateIdentityEqual(current.identity, current.factIDs(), current.factVersions(), candidate.identity, candidate.factIDs, candidate.factVersions)
}

func activationMatchesTerminalToken(current *activation, rule compiledRule, identity candidateIdentity, token tokenRef) bool {
	if current == nil {
		return false
	}
	if current.ruleRevisionID != rule.revisionID {
		return false
	}
	if current.identity.key != identity.key {
		return false
	}
	if identity.generation != 0 && current.identity.generation != identity.generation {
		return false
	}
	if identity.count != 0 && current.identity.count != identity.count {
		return false
	}
	if !current.token.isZero() {
		return terminalTokenFactVersionsEqual(current.token, token)
	}
	return matchTokenFactsEqualSlices(token, current.factIDs(), current.factVersions())
}

func activationMatchesTerminalTokenDelta(current *activation, rule compiledRule, identity candidateIdentity, delta reteTerminalTokenDelta) bool {
	if current == nil {
		return false
	}
	if current.ruleRevisionID != rule.revisionID {
		return false
	}
	if current.identity.key != identity.key {
		return false
	}
	if identity.generation != 0 && current.identity.generation != identity.generation {
		return false
	}
	if identity.count != 0 && current.identity.count != identity.count {
		return false
	}
	if !current.token.isZero() && (len(delta.factIDs) > 0 || len(delta.factVersions) > 0) {
		if matchTokenFactsEqualSlices(current.token, delta.factIDs, delta.factVersions) {
			return true
		}
		if len(current.factIDs()) > 0 || len(current.factVersions()) > 0 {
			return factVersionSlicesEqual(current.factIDs(), current.factVersions(), delta.factIDs, delta.factVersions)
		}
		return false
	}
	if len(delta.factIDs) > 0 || len(delta.factVersions) > 0 {
		return factVersionSlicesEqual(current.factIDs(), current.factVersions(), delta.factIDs, delta.factVersions)
	}
	if !current.token.isZero() {
		return terminalTokenFactVersionsEqual(current.token, delta.token)
	}
	return matchTokenFactsEqualSlices(delta.token, current.factIDs(), current.factVersions())
}

func factVersionSlicesEqual(leftIDs []FactID, leftVersions []FactVersion, rightIDs []FactID, rightVersions []FactVersion) bool {
	if len(leftIDs) != len(rightIDs) || len(leftVersions) != len(rightVersions) || len(leftIDs) != len(leftVersions) {
		return false
	}
	for i := range leftIDs {
		if leftIDs[i] != rightIDs[i] || leftVersions[i] != rightVersions[i] {
			return false
		}
	}
	return true
}

func compareFactVersionSlices(leftIDs []FactID, leftVersions []FactVersion, rightIDs []FactID, rightVersions []FactVersion) int {
	n := min(len(leftIDs), len(rightIDs))
	for i := range n {
		if leftIDs[i] != rightIDs[i] {
			if factIDLess(leftIDs[i], rightIDs[i]) {
				return -1
			}
			return 1
		}
		leftVersion := FactVersion(0)
		if i < len(leftVersions) {
			leftVersion = leftVersions[i]
		}
		rightVersion := FactVersion(0)
		if i < len(rightVersions) {
			rightVersion = rightVersions[i]
		}
		if leftVersion != rightVersion {
			if leftVersion < rightVersion {
				return -1
			}
			return 1
		}
	}
	if len(leftIDs) != len(rightIDs) {
		if len(leftIDs) < len(rightIDs) {
			return -1
		}
		return 1
	}
	if len(leftVersions) != len(rightVersions) {
		if len(leftVersions) < len(rightVersions) {
			return -1
		}
		return 1
	}
	return 0
}

func (a *agenda) storeActivation(act *activation) activationKey {
	if act == nil {
		return activationKey{}
	}
	stored := a.activationRows.addEmpty()
	if stored == nil {
		stored = act
	} else {
		*stored = *act
	}
	key := a.storePreparedActivation(stored)
	a.ensureRevisionIndex()
	return key
}

func (a *agenda) storePreparedActivation(act *activation) activationKey {
	if act == nil {
		return activationKey{}
	}
	a.ensureHandleGeneration()
	fingerprint := activationFingerprintForIdentityKey(act.identity.key)
	bucket := a.activations[fingerprint]
	key := activationKey{
		fingerprint: fingerprint,
		ordinal:     a.nextOrdinal,
	}
	a.nextOrdinal++
	act.key = key
	if bucket.first == nil {
		bucket.first = act
	} else if bucket.second == nil {
		bucket.second = act
	} else {
		if bucket.overflow == nil {
			bucket.overflow = make([]*activation, 0, 8)
		}
		bucket.overflow = append(bucket.overflow, act)
	}
	a.activations[fingerprint] = bucket
	a.indexActivation(act)
	if a.propagationCounters != nil {
		a.propagationCounters.recordActivationStored()
	}
	return key
}

func (a *agenda) ensureHandleGeneration() uint32 {
	if a == nil {
		return 0
	}
	if a.handleGeneration == 0 {
		a.handleGeneration = 1
	}
	return a.handleGeneration
}

func (a *agenda) advanceHandleGeneration() {
	if a == nil {
		return
	}
	a.handleGeneration++
	if a.handleGeneration == 0 {
		a.handleGeneration = 1
	}
}

func (a *agenda) handleForActivationKey(key activationKey) activationHandle {
	if a == nil {
		return activationHandle{}
	}
	return activationHandle{
		key:        key,
		generation: a.ensureHandleGeneration(),
	}
}

func (a *agenda) activationByHandlePtr(handle activationHandle) (*activation, bool) {
	if a == nil || handle.isZero() || handle.generation != a.ensureHandleGeneration() {
		return nil, false
	}
	return a.activationByKeyPtr(handle.key)
}

func (a *agenda) activationByKeyPtr(key activationKey) (*activation, bool) {
	if a == nil {
		return nil, false
	}
	return activationFromBuckets(a.activations, key)
}

func activationFromBuckets(buckets map[activationFingerprint]activationBucket, key activationKey) (*activation, bool) {
	bucket := buckets[key.fingerprint]
	if bucket.first != nil && bucket.first.key == key {
		return bucket.first, true
	}
	if bucket.second != nil && bucket.second.key == key {
		return bucket.second, true
	}
	for _, current := range bucket.overflow {
		if current != nil && current.key == key {
			return current, true
		}
	}
	return nil, false
}

func activationFingerprintForIdentityKey(key candidateIdentityKey) activationFingerprint {
	hash := fnvhash.Offset64
	hash = fnvhash.MixUint64(hash, key.scopeHash)
	hash = fnvhash.MixUint64(hash, key.hash)
	return activationFingerprint(hash)
}

func activationIDForIdentityKey(identity candidateIdentityKey, index uint64) ActivationID {
	if identity == (candidateIdentityKey{}) {
		return ""
	}
	var b strings.Builder
	b.Grow(64)
	b.WriteString("activation:v1:")
	writeUintToBuilder(&b, identity.scopeHash)
	b.WriteByte(':')
	writeUintToBuilder(&b, identity.hash)
	b.WriteByte(':')
	writeUintToBuilder(&b, index)
	return ActivationID(b.String())
}

func activationKeyPending(keys []activationKey, want activationKey) bool {
	return slices.Contains(keys, want)
}

func (a *agenda) compactLazyPending(force bool) {
	if a == nil || a.lazyDeactivated == 0 {
		return
	}
	a.normalizePendingKeys()
	if !force && a.lazyDeactivated < 1024 && a.lazyDeactivated*2 < len(a.pending) {
		return
	}
	next := a.deltaNextPending[:0]
	for _, key := range a.pending {
		if current, ok := a.activationByKeyPtr(key); ok && current.status == activationStatusPending {
			next = append(next, key)
		}
	}
	a.deltaNextPending = a.pending[:0]
	a.pending = next
	a.rebuildPendingActivationCache()
	a.lazyDeactivated = 0
}

func cloneBindingTupleEntries(entries []bindingTupleEntry) []bindingTupleEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]bindingTupleEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
		out[i].conditionPath = cloneIntPath(entry.conditionPath)
		out[i].value = cloneValue(entry.value)
	}
	return out
}

func cloneFactIDs(ids []FactID) []FactID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func cloneActivationFactIDs(act *activation) []FactID {
	if act == nil {
		return nil
	}
	if factIDs := act.factIDs(); len(factIDs) > 0 {
		return cloneFactIDs(factIDs)
	}
	if act.token.isZero() {
		return cloneFactIDs(act.factIDs())
	}
	factIDs, ok := act.token.factIDs()
	if !ok {
		out := make([]FactID, act.token.size())
		fillTokenRefFactIDs(out, 0, act.token)
		return out
	}
	out := make([]FactID, len(factIDs))
	copy(out, factIDs)
	return out
}

func cloneActivationFactVersions(act *activation) []FactVersion {
	if act == nil {
		return nil
	}
	if factVersions := act.factVersions(); len(factVersions) > 0 {
		return cloneFactVersions(factVersions)
	}
	if act.token.isZero() {
		return cloneFactVersions(act.factVersions())
	}
	factVersions, ok := act.token.factVersions()
	if !ok {
		out := make([]FactVersion, act.token.size())
		fillTokenRefFactVersions(out, 0, act.token)
		return out
	}
	out := make([]FactVersion, len(factVersions))
	copy(out, factVersions)
	return out
}

func activationFactCount(act *activation) int {
	if act == nil {
		return 0
	}
	if !act.token.isZero() {
		return tokenRefSize(act.token)
	}
	return len(act.factIDs())
}

func activationFactVersionCount(act *activation) int {
	if act == nil {
		return 0
	}
	if !act.token.isZero() {
		return tokenRefSize(act.token)
	}
	return len(act.factVersions())
}

func fillTokenRefFactIDs(factIDs []FactID, index int, token tokenRef) int {
	if token.isZero() {
		return index
	}
	row, ok := token.resolve()
	if !ok {
		return index
	}
	index = fillTokenRefFactIDs(factIDs, index, token.parent())
	match, ok := row.conditionMatch()
	if !ok {
		return index
	}
	factIDs[index] = match.fact.ID()
	return index + 1
}

func fillTokenRefFactVersions(factVersions []FactVersion, index int, token tokenRef) int {
	if token.isZero() {
		return index
	}
	row, ok := token.resolve()
	if !ok {
		return index
	}
	index = fillTokenRefFactVersions(factVersions, index, token.parent())
	match, ok := row.conditionMatch()
	if !ok {
		return index
	}
	factVersions[index] = match.fact.Version()
	return index + 1
}

func activationFromCandidate(rule compiledRule, candidate matchCandidate) activation {
	var out activation
	fillActivationFromCandidate(&out, rule, candidate)
	return out
}

func fillActivationFromCandidate(dst *activation, rule compiledRule, candidate matchCandidate) {
	if dst == nil {
		return
	}
	dst.ruleID = candidate.ruleID
	dst.ruleRevisionID = candidate.ruleRevisionID
	dst.generation = candidate.generation
	dst.identity = candidate.identity
	dst.payload = nil
	dst.setBindings(cloneBindingTupleEntries(candidate.bindingTuple))
	dst.setFactIDs(cloneFactIDs(candidate.factIDs))
	dst.setFactVersions(cloneFactVersions(candidate.factVersions))
	dst.salience = rule.salience
	dst.maxRecency = candidate.maxRecency
	dst.aggregateRecency = candidate.aggregateRecency
	dst.status = activationStatusPending
}

func activationFromTerminalToken(rule compiledRule, token tokenRef) (activation, error) {
	return activationFromTerminalTokenWithIdentity(rule, token, candidateIdentityForTerminalToken(rule, token))
}

func activationFromTerminalTokenWithIdentity(rule compiledRule, token tokenRef, identity candidateIdentity) (activation, error) {
	var out activation
	if err := fillActivationFromTerminalTokenWithIdentity(&out, rule, token, identity); err != nil {
		return activation{}, err
	}
	return out, nil
}

func newActivationFromTerminalTokenWithIdentity(rule compiledRule, token tokenRef, identity candidateIdentity) (*activation, error) {
	out := new(activation)
	if err := fillActivationFromTerminalTokenWithIdentity(out, rule, token, identity); err != nil {
		return nil, err
	}
	out.ruleID = ""
	return out, nil
}

func fillActivationFromTerminalTokenWithIdentity(dst *activation, rule compiledRule, token tokenRef, identity candidateIdentity) error {
	if dst == nil {
		return fmt.Errorf("%w: empty activation storage for rule %q", ErrMatcher, rule.name)
	}
	if token.isZero() {
		return fmt.Errorf("%w: empty token for rule %q", ErrMatcher, rule.name)
	}
	if len(rule.conditionPlans) == 0 {
		return fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}
	row, ok := token.resolve()
	if !ok {
		return fmt.Errorf("%w: stale token for rule %q", ErrMatcher, rule.name)
	}
	if identity.isZero() {
		identity = candidateIdentityForTerminalToken(rule, token)
	}
	dst.ruleID = rule.id
	dst.ruleRevisionID = rule.revisionID
	dst.generation = token.generation()
	dst.identity = identity
	dst.token = token
	dst.salience = rule.salience
	dst.maxRecency = row.maxRecency
	dst.aggregateRecency = row.aggregateRecency
	dst.status = activationStatusPending
	return nil
}

func candidateIdentityForTerminalToken(rule compiledRule, token tokenRef) candidateIdentity {
	if identity, ok := candidateIdentityForTerminalTokenCached(rule, token); ok {
		return identity
	}
	if identity, ok := candidateIdentityForTerminalTokenFast(rule, token); ok {
		return identity
	}
	entries, _, _, _, ok := terminalTokenBindingTuple(rule, token)
	if ok {
		return candidateIdentityFor(rule.id, rule.revisionID, rule.identityScopeHash, tokenRefGeneration(token), entries)
	}
	identity := candidateIdentity{
		generation: tokenRefGeneration(token),
		count:      tokenRefSize(token),
		key: candidateIdentityKey{
			scopeHash: rule.identityScopeHash,
			hash:      candidateIdentityHashFinish(tokenRefIdentityState(token), tokenRefSize(token)),
		},
	}
	if identity.key.scopeHash == 0 {
		identity.key.scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
	}
	return identity
}

func candidateIdentityForTerminalTokenCached(rule compiledRule, token tokenRef) (candidateIdentity, bool) {
	if token.isZero() || len(rule.conditions) == 0 {
		return candidateIdentity{}, false
	}
	row, ok := token.resolve()
	if !ok || row.size != len(rule.conditions) || !row.orderedSlots {
		return candidateIdentity{}, false
	}
	generation := token.handle.arena.generation
	scopeHash := rule.identityScopeHash
	if scopeHash == 0 {
		scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
	}
	return candidateIdentity{
		generation: generation,
		count:      row.size,
		key: candidateIdentityKey{
			scopeHash: scopeHash,
			hash:      candidateIdentityHashFinish(row.identityState, row.size),
		},
	}, true
}

func candidateIdentityForTerminalTokenFast(rule compiledRule, token tokenRef) (candidateIdentity, bool) {
	if token.isZero() || len(rule.conditions) == 0 || len(rule.conditions) > 8 {
		return candidateIdentity{}, false
	}
	var factIDs [8]FactID
	var factVersions [8]FactVersion
	var valueEntries [8]tokenRowEntry
	var seen uint8
	var values uint8
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return candidateIdentity{}, false
		}
		slot := row.bindingSlot
		if slot < 0 {
			continue
		}
		if slot >= len(rule.conditions) {
			return candidateIdentity{}, false
		}
		mask := uint8(1 << uint(slot))
		if seen&mask != 0 {
			return candidateIdentity{}, false
		}
		if row.hasValue {
			valueEntries[slot] = row.tokenRowEntry()
			values |= mask
		} else {
			if row.fact == nil {
				return candidateIdentity{}, false
			}
			factIDs[slot] = row.fact.ID()
			factVersions[slot] = row.fact.Version()
		}
		seen |= mask
	}
	if seen != uint8(1<<uint(len(rule.conditions)))-1 {
		return candidateIdentity{}, false
	}
	generation := tokenRefGeneration(token)
	state := candidateIdentityHashStart(generation)
	count := 0
	for i := 0; i < len(rule.conditions); i++ {
		mask := uint8(1 << uint(i))
		if values&mask != 0 {
			state = candidateIdentityHashTokenEntryStep(state, valueEntries[i])
		} else {
			state = candidateIdentityHashFactStep(state, factIDs[i], factVersions[i])
		}
		count++
	}
	scopeHash := rule.identityScopeHash
	if scopeHash == 0 {
		scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
	}
	return candidateIdentity{
		generation: generation,
		count:      count,
		key: candidateIdentityKey{
			scopeHash: scopeHash,
			hash:      candidateIdentityHashFinish(state, count),
		},
	}, true
}

func terminalTokenBindingTuple(rule compiledRule, token tokenRef) ([]bindingTupleEntry, []FactID, []FactVersion, []int, bool) {
	if token.isZero() || len(rule.conditions) == 0 {
		return nil, nil, nil, nil, false
	}
	matches, ok := tokenPublicMatchesForRule(rule, token)
	if !ok {
		return nil, nil, nil, nil, false
	}
	entries := make([]bindingTupleEntry, len(matches))
	factIDs := make([]FactID, len(matches))
	factVersions := make([]FactVersion, len(matches))
	pathLen := 0
	for i, match := range matches {
		entry, err := bindingTupleEntryForMatch(rule, match)
		if err != nil {
			return nil, nil, nil, nil, false
		}
		entries[i] = entry
		factIDs[i] = entry.factID
		factVersions[i] = entry.factVersion
		pathLen += len(entry.conditionPath)
	}
	path := make([]int, 0, pathLen)
	for _, entry := range entries {
		path = append(path, entry.conditionPath...)
	}
	return entries, factIDs, factVersions, path, true
}

func terminalTokenFactTuple(rule compiledRule, token tokenRef) ([]FactID, []FactVersion, bool) {
	if token.isZero() || len(rule.conditions) == 0 {
		return nil, nil, false
	}
	n := len(rule.conditions)
	factIDs := make([]FactID, n)
	factVersions := make([]FactVersion, n)
	if n <= 64 {
		var seen uint64
		for current := token; !current.isZero(); current = current.parent() {
			row, ok := current.resolve()
			if !ok {
				return nil, nil, false
			}
			match, ok := row.conditionMatch()
			if !ok {
				return nil, nil, false
			}
			slot := match.bindingSlot
			if slot < 0 {
				continue
			}
			if slot >= n {
				return nil, nil, false
			}
			mask := uint64(1) << uint(slot)
			if seen&mask != 0 {
				return nil, nil, false
			}
			seen |= mask
			factIDs[slot] = match.fact.ID()
			factVersions[slot] = match.fact.Version()
		}
		wantSeen := ^uint64(0)
		if n < 64 {
			wantSeen = uint64(1)<<uint(n) - 1
		}
		if seen != wantSeen {
			return nil, nil, false
		}
		return factIDs, factVersions, true
	}

	seen := make([]bool, n)
	seenCount := 0
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return nil, nil, false
		}
		match, ok := row.conditionMatch()
		if !ok {
			return nil, nil, false
		}
		slot := match.bindingSlot
		if slot < 0 {
			continue
		}
		if slot >= n || seen[slot] {
			return nil, nil, false
		}
		seen[slot] = true
		seenCount++
		factIDs[slot] = match.fact.ID()
		factVersions[slot] = match.fact.Version()
	}
	if seenCount != n {
		return nil, nil, false
	}
	return factIDs, factVersions, true
}

func tokenPublicMatchesForRule(rule compiledRule, token tokenRef) ([]conditionMatch, bool) {
	if token.isZero() || len(rule.conditions) == 0 {
		return nil, false
	}
	matches := make([]conditionMatch, len(rule.conditions))
	seen := make([]bool, len(rule.conditions))
	for current := token; !current.isZero(); current = current.parent() {
		row, ok := current.resolve()
		if !ok {
			return nil, false
		}
		match, ok := row.conditionMatch()
		if !ok {
			return nil, false
		}
		slot := match.bindingSlot
		if slot < 0 {
			continue
		}
		if slot >= len(matches) || seen[slot] {
			return nil, false
		}
		matches[slot] = match
		seen[slot] = true
	}
	for _, ok := range seen {
		if !ok {
			return nil, false
		}
	}
	return matches, true
}

func terminalTokenIdentityStateForRule(rule compiledRule, token tokenRef, state uint64, count int) (uint64, int, bool) {
	if token.isZero() {
		return state, count, true
	}
	row, ok := token.resolve()
	if !ok {
		return state, count, false
	}
	state, count, ok = terminalTokenIdentityStateForRule(rule, token.parent(), state, count)
	if !ok {
		return state, count, false
	}
	match, ok := row.conditionMatch()
	if !ok {
		return state, count, false
	}
	entry, err := bindingTupleEntryForMatch(rule, match)
	if err != nil {
		return state, count, false
	}
	return candidateIdentityHashStep(state, entry), count + 1, true
}

func candidateIdentityForTerminalTokenDelta(revision *Ruleset, delta reteTerminalTokenDelta) candidateIdentity {
	if !delta.identity.isZero() {
		return delta.identity
	}
	if revision == nil {
		return candidateIdentity{}
	}
	rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
	if !ok {
		return candidateIdentity{}
	}
	return candidateIdentityForTerminalToken(rule, delta.token)
}

func tokenRefSize(token tokenRef) int {
	if token.isZero() {
		return 0
	}
	return token.size()
}

func tokenRefIdentityState(token tokenRef) uint64 {
	if token.isZero() {
		return candidateIdentityHashStart(0)
	}
	return token.identityState()
}

func tokenRefGeneration(token tokenRef) Generation {
	if token.isZero() {
		return 0
	}
	return token.generation()
}

func matchTokenFactsEqualSlices(token tokenRef, factIDs []FactID, factVersions []FactVersion) bool {
	if tokenRefSize(token) != len(factIDs) || len(factIDs) != len(factVersions) {
		return false
	}
	tokenFactIDs, idsOK := token.factIDs()
	tokenFactVersions, versionsOK := token.factVersions()
	if idsOK && versionsOK {
		if len(tokenFactIDs) != len(factIDs) || len(tokenFactVersions) != len(factVersions) {
			return false
		}
		for i := range tokenFactIDs {
			if tokenFactIDs[i] != factIDs[i] || tokenFactVersions[i] != factVersions[i] {
				return false
			}
		}
		return true
	}
	index, ok := matchTokenFactsEqualSlicesAt(token, factIDs, factVersions, 0)
	return ok && index == len(factIDs)
}

func matchTokenFactsEqualSlicesAt(token tokenRef, factIDs []FactID, factVersions []FactVersion, index int) (int, bool) {
	if token.isZero() {
		return index, true
	}
	row, ok := token.resolve()
	if !ok {
		return index, false
	}
	next, ok := matchTokenFactsEqualSlicesAt(token.parent(), factIDs, factVersions, index)
	if !ok || next >= len(factIDs) || next >= len(factVersions) {
		return next, false
	}
	match, ok := row.conditionMatch()
	if !ok {
		return next, false
	}
	if factIDs[next] != match.fact.ID() || factVersions[next] != match.fact.Version() {
		return next, false
	}
	return next + 1, true
}

func terminalTokenFactVersionsEqual(left, right tokenRef) bool {
	if tokenRefSize(left) != tokenRefSize(right) {
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
	return terminalTokenFactVersionsEqualAt(left, right)
}

func terminalTokenFactVersionsEqualAt(left, right tokenRef) bool {
	if left.isZero() || right.isZero() {
		return left.isZero() && right.isZero()
	}
	leftRow, leftOK := left.resolve()
	rightRow, rightOK := right.resolve()
	if !leftOK || !rightOK {
		return false
	}
	if !terminalTokenFactVersionsEqualAt(left.parent(), right.parent()) {
		return false
	}
	leftMatch, leftOK := leftRow.conditionMatch()
	rightMatch, rightOK := rightRow.conditionMatch()
	return leftOK && rightOK && leftMatch.fact.ID() == rightMatch.fact.ID() && leftMatch.fact.Version() == rightMatch.fact.Version()
}

func compareTerminalTokenFacts(left, right tokenRef) int {
	if left.isZero() || right.isZero() {
		switch {
		case left.isZero() && right.isZero():
			return 0
		case left.isZero():
			return -1
		default:
			return 1
		}
	}
	leftRow, leftOK := left.resolve()
	rightRow, rightOK := right.resolve()
	if !leftOK || !rightOK {
		return 0
	}
	if compare := compareTerminalTokenFacts(left.parent(), right.parent()); compare != 0 {
		return compare
	}
	leftMatch, leftOK := leftRow.conditionMatch()
	rightMatch, rightOK := rightRow.conditionMatch()
	if !leftOK || !rightOK {
		return 0
	}
	if leftMatch.fact.ID() != rightMatch.fact.ID() {
		if factIDLess(leftMatch.fact.ID(), rightMatch.fact.ID()) {
			return -1
		}
		return 1
	}
	if leftMatch.fact.Version() != rightMatch.fact.Version() {
		if leftMatch.fact.Version() < rightMatch.fact.Version() {
			return -1
		}
		return 1
	}
	return 0
}

func cloneFactVersions(versions []FactVersion) []FactVersion {
	if len(versions) == 0 {
		return nil
	}
	out := make([]FactVersion, len(versions))
	copy(out, versions)
	return out
}
