package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

type activationKey struct {
	ordinal uint64
}

type activationLookupKey struct {
	ruleRevisionID RuleRevisionID
	identityKey    candidateIdentityKey
}

type activationObserveFunc func(*activation)

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
			chunk[i].token.releaseChain()
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

// recycle zeroes all rows and keeps chunk capacity for reuse. Callers must
// have released or moved any retained token chains first.
func (r *activationRows) recycle() {
	if r == nil {
		return
	}
	for chunkIndex, chunk := range r.chunks {
		clear(chunk)
		r.chunks[chunkIndex] = chunk[:0]
	}
	r.count = 0
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
	ruleRevisionID   RuleRevisionID
	identityKey      candidateIdentityKey
	token            tokenRef
	module           ModuleName
	heapIndex        int
	salience         int
	declarationOrder int
	maxRecency       Recency
	totalRecency     Recency
	supportCount     uint32
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
		RuleRevisionID:        a.ruleRevisionID,
		activationIdentityKey: a.identityKey,
		activationOrdinal:     a.key.ordinal,
	}
}

func (a activation) Generation() Generation {
	return activationGeneration(&a)
}

func (a activation) activationID() ActivationID {
	return activationIDForIdentityKey(a.identityKey, a.key.ordinal)
}

func (a *activation) incrementSupport() {
	if a == nil {
		return
	}
	if a.supportCount == 0 {
		a.supportCount = 1
		return
	}
	a.supportCount++
}

func (a *activation) decrementSupport() bool {
	if a == nil {
		return false
	}
	if a.supportCount <= 1 {
		a.supportCount = 0
		return true
	}
	a.supportCount--
	return false
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

type agendaModuleQueue struct {
	module ModuleName
	heap   []*activation
}

func newAgendaModuleQueue(module ModuleName) *agendaModuleQueue {
	return &agendaModuleQueue{
		module: module,
		heap:   []*activation{nil},
	}
}

func (q *agendaModuleQueue) len() int {
	if q == nil || len(q.heap) == 0 {
		return 0
	}
	return len(q.heap) - 1
}

func (q *agendaModuleQueue) empty() bool {
	return q.len() == 0
}

func (q *agendaModuleQueue) reset() {
	if q == nil {
		return
	}
	for i := 1; i < len(q.heap); i++ {
		if act := q.heap[i]; act != nil {
			act.heapIndex = 0
		}
		q.heap[i] = nil
	}
	q.heap = q.heap[:1]
}

func (q *agendaModuleQueue) push(a *agenda, act *activation) {
	if q == nil || act == nil || act.status != activationStatusPending || act.heapIndex > 0 {
		return
	}
	if len(q.heap) == 0 {
		q.heap = append(q.heap, nil)
	}
	q.heap = append(q.heap, act)
	act.heapIndex = len(q.heap) - 1
	q.siftUp(a, act.heapIndex)
}

func (q *agendaModuleQueue) pop(a *agenda) (*activation, bool) {
	if q == nil || q.empty() {
		return nil, false
	}
	act := q.heap[1]
	q.removeAt(a, 1)
	return act, act != nil
}

func (q *agendaModuleQueue) remove(a *agenda, act *activation) bool {
	if q == nil || act == nil || act.heapIndex <= 0 || act.heapIndex >= len(q.heap) || q.heap[act.heapIndex] != act {
		return false
	}
	q.removeAt(a, act.heapIndex)
	return true
}

func (q *agendaModuleQueue) fix(a *agenda, act *activation) {
	if q == nil || act == nil || act.heapIndex <= 0 || act.heapIndex >= len(q.heap) || q.heap[act.heapIndex] != act {
		return
	}
	index := act.heapIndex
	if index > 1 && a.activationLess(q.heap[index], q.heap[index/2]) {
		q.siftUp(a, index)
		return
	}
	q.siftDown(a, index)
}

func (q *agendaModuleQueue) removeAt(a *agenda, index int) {
	if q == nil || index <= 0 || index >= len(q.heap) {
		return
	}
	last := len(q.heap) - 1
	removed := q.heap[index]
	if index != last {
		q.swap(index, last)
	}
	q.heap[last] = nil
	q.heap = q.heap[:last]
	if removed != nil {
		removed.heapIndex = 0
	}
	if index < len(q.heap) {
		if index > 1 && a.activationLess(q.heap[index], q.heap[index/2]) {
			q.siftUp(a, index)
		} else {
			q.siftDown(a, index)
		}
	}
}

func (q *agendaModuleQueue) siftUp(a *agenda, index int) {
	for index > 1 {
		parent := index / 2
		if !a.activationLess(q.heap[index], q.heap[parent]) {
			return
		}
		q.swap(index, parent)
		index = parent
	}
}

func (q *agendaModuleQueue) siftDown(a *agenda, index int) {
	for {
		left := index * 2
		if left >= len(q.heap) {
			return
		}
		child := left
		right := left + 1
		if right < len(q.heap) && a.activationLess(q.heap[right], q.heap[left]) {
			child = right
		}
		if !a.activationLess(q.heap[child], q.heap[index]) {
			return
		}
		q.swap(index, child)
		index = child
	}
}

func (q *agendaModuleQueue) swap(left, right int) {
	q.heap[left], q.heap[right] = q.heap[right], q.heap[left]
	if q.heap[left] != nil {
		q.heap[left].heapIndex = left
	}
	if q.heap[right] != nil {
		q.heap[right].heapIndex = right
	}
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
	return c.eventWithRuleID(sessionID, rulesetID, "", sequence, timestamp)
}

func (c agendaChange) eventWithRuleID(sessionID SessionID, rulesetID RulesetID, ruleID RuleID, sequence uint64, timestamp time.Time) Event {
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
		Generation:     c.activation.Generation(),
		Recency:        c.activation.maxRecency,
		RuleID:         ruleID,
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
	out.payload = nil
	if out.supportCount == 0 {
		out.supportCount = 1
	}
	out.setFactIDs(cloneActivationFactIDs(act))
	out.setFactVersions(cloneActivationFactVersions(act))
	out.token = tokenRef{}
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
	out.payload = nil
	if out.supportCount == 0 {
		out.supportCount = 1
	}
	out.setFactIDs(cloneActivationFactIDs(act))
	if a != nil && a.revision != nil && !act.token.isZero() {
		if rule, ok := a.revision.rulesByRevisionID[out.ruleRevisionID]; ok {
			if factIDs, _, ok := terminalTokenFactTuple(rule, act.token); ok {
				out.setFactIDs(factIDs)
			}
		}
	}
	out.token = tokenRef{}
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

type activationLookupBucket struct {
	first *activation
	rest  []*activation
}

type agenda struct {
	activationLookup    map[activationLookupKey]activationLookupBucket
	activations         []*activation
	activationRows      activationRows
	terminalActivations activationRows
	moduleQueues        map[ModuleName]*agendaModuleQueue
	nextOrdinal         uint64
	handleGeneration    uint32
	revision            *Ruleset
	propagationCounters *propagationCounterLedger

	reconcileSeen      map[activationKey]struct{}
	reconcileChanges   []agendaChange
	reconcileActivated []agendaChange

	deltaChanges   []agendaChange
	deltaActivated []agendaChange

	purgeChanges []agendaChange

	pendingScratch []*activation

	activationRowsSpare activationRows
	activationsSpare    []*activation
	payloadPool         []*activationPayload
}

const activationPayloadPoolCap = 4096

func (a *agenda) takeActivationPayload() *activationPayload {
	if a == nil || len(a.payloadPool) == 0 {
		return &activationPayload{}
	}
	payload := a.payloadPool[len(a.payloadPool)-1]
	a.payloadPool = a.payloadPool[:len(a.payloadPool)-1]
	return payload
}

// recycleActivationPayload returns a payload owned by a dropped activation to
// the pool, keeping only the fact tuple array capacity. Callers must ensure no
// copies share the payload pointer; agenda-external copies always clone.
func (a *agenda) recycleActivationPayload(payload *activationPayload) {
	if a == nil || payload == nil || len(a.payloadPool) >= activationPayloadPoolCap {
		return
	}
	*payload = activationPayload{
		factIDs:      payload.factIDs[:0],
		factVersions: payload.factVersions[:0],
	}
	a.payloadPool = append(a.payloadPool, payload)
}

func newAgenda() *agenda {
	return &agenda{
		activationLookup: make(map[activationLookupKey]activationLookupBucket),
		moduleQueues:     make(map[ModuleName]*agendaModuleQueue),
		handleGeneration: 1,
	}
}

func (a *agenda) queueForModule(module ModuleName) *agendaModuleQueue {
	if a == nil {
		return nil
	}
	module = normalizeModuleName(module)
	if module.IsZero() {
		module = MainModule
	}
	if a.moduleQueues == nil {
		a.moduleQueues = make(map[ModuleName]*agendaModuleQueue)
	}
	queue := a.moduleQueues[module]
	if queue == nil {
		queue = newAgendaModuleQueue(module)
		a.moduleQueues[module] = queue
	}
	return queue
}

func (a *agenda) resetModuleQueues() {
	if a == nil {
		return
	}
	for module, queue := range a.moduleQueues {
		if queue != nil {
			queue.reset()
		}
		if queue == nil || queue.empty() {
			delete(a.moduleQueues, module)
		}
	}
}

func (a *agenda) enqueueActivation(act *activation) {
	if a == nil || act == nil || act.status != activationStatusPending {
		return
	}
	queue := a.queueForModule(act.module)
	if queue != nil {
		queue.push(a, act)
	}
}

func (a *agenda) dequeueActivation(act *activation) bool {
	if a == nil || act == nil || act.heapIndex <= 0 {
		return false
	}
	queue := a.moduleQueues[normalizeModuleName(act.module)]
	if queue == nil {
		return false
	}
	removed := queue.remove(a, act)
	if queue.empty() {
		delete(a.moduleQueues, queue.module)
	}
	return removed
}

func (a *agenda) fixActivationOrder(act *activation) {
	if a == nil || act == nil || act.heapIndex <= 0 {
		return
	}
	if queue := a.moduleQueues[normalizeModuleName(act.module)]; queue != nil {
		queue.fix(a, act)
	}
}

func (a *agenda) pendingActivationCount() int {
	if a == nil {
		return 0
	}
	count := 0
	for _, queue := range a.moduleQueues {
		count += queue.len()
	}
	return count
}

func (a *agenda) reset() {
	if a == nil {
		return
	}
	if a.activationLookup == nil {
		a.activationLookup = make(map[activationLookupKey]activationLookupBucket)
	} else {
		a.clearActivationLookup()
	}
	clear(a.activations)
	a.activations = a.activations[:0]
	a.resetModuleQueues()
	a.activationRows.reset()
	a.terminalActivations.reset()
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

	seen := a.reconcileSeen
	if seen == nil {
		seen = make(map[activationKey]struct{}, a.pendingActivationCount())
	} else {
		clear(seen)
	}
	changes := a.reconcileChanges[:0]
	activated := a.reconcileActivated[:0]

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
					seen[key] = struct{}{}
				} else if existing.status == activationStatusDeactivated {
					existing.status = activationStatusPending
					a.enqueueActivation(existing)
					seen[key] = struct{}{}
					activated = append(activated, agendaChange{
						kind:       agendaChangeActivated,
						activation: a.compactChangeActivation(existing),
					})
				}
				continue
			}

			created := a.activationRows.addEmpty()
			fillActivationFromCandidate(created, rule, candidate)
			key = a.storePreparedActivation(created)
			a.enqueueActivation(created)

			seen[key] = struct{}{}
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(created),
			})
		}
	}

	a.forEachPendingActivation(func(existing *activation) bool {
		key := existing.key
		if _, ok := seen[key]; ok {
			return true
		}
		a.dequeueActivation(existing)
		existing.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(existing),
		})
		return true
	})

	changes = append(changes, activated...)

	a.reconcileSeen = seen
	a.reconcileChanges = changes[:0]
	a.reconcileActivated = activated[:0]
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

	changes := a.deltaChanges[:0]
	for _, candidate := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		existing, _, ok := a.activationForCandidate(candidate)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		a.dequeueActivation(existing)
		existing.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(existing),
		})
	}

	activated := a.deltaActivated[:0]
	sortMatchCandidates(revision, added)
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
		a.storePreparedActivation(created)
		a.enqueueActivation(created)
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: a.compactChangeActivation(created),
		})
	}
	changes = append(changes, activated...)

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
		existing.incrementSupport()
		if existing.status == activationStatusDeactivated {
			rearmActivationToken(existing, delta.token)
			existing.status = activationStatusPending
			a.enqueueActivation(existing)
		}
		return existing, nil
	}
	created, err := a.newTerminalActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
	if err != nil {
		return nil, err
	}
	created.supportCount = 1
	a.storePreparedActivation(created)
	a.enqueueActivation(created)
	return created, nil
}

func (a *agenda) finishInitialTerminalActivations() {
	if a == nil {
		return
	}
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

	if len(removed) == 1 {
		delta := removed[0]
		if !delta.token.isZero() {
			rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
			if !ok {
				return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
			}
			existing, _, ok := a.activationForTerminalTokenDelta(rule, delta)
			if ok {
				deactivate := existing.decrementSupport()
				switch {
				case !deactivate:
				case existing.status == activationStatusPending:
					a.dequeueActivation(existing)
					existing.status = activationStatusDeactivated
					a.compactDeactivatedTokenActivation(existing)
				case existing.status == activationStatusConsumed:
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
	if existing, _, ok := a.activationForTerminalTokenIdentity(rule, delta.token, identity); ok {
		existing.incrementSupport()
		if existing.status == activationStatusDeactivated {
			rearmActivationToken(existing, delta.token)
			existing.status = activationStatusPending
			a.enqueueActivation(existing)
		}
		return existing, nil
	}
	created, err := a.newTerminalActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
	if err != nil {
		return nil, err
	}
	created.supportCount = 1
	a.storePreparedActivation(created)
	a.enqueueActivation(created)
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

	var changes []agendaChange
	if collectChanges {
		changes = a.deltaChanges[:0]
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
		existing, _, ok := a.activationForTerminalTokenDelta(rule, delta)
		if !ok {
			continue
		}
		deactivate := existing.decrementSupport()
		if !deactivate || existing.status != activationStatusPending {
			if deactivate && existing.status == activationStatusConsumed {
				existing.status = activationStatusDeactivated
			}
			continue
		}
		if !collectChanges {
			a.dequeueActivation(existing)
			existing.status = activationStatusDeactivated
			a.compactDeactivatedTokenActivation(existing)
			continue
		}
		a.dequeueActivation(existing)
		existing.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(existing),
		})
		a.compactDeactivatedTokenActivation(existing)
	}

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
		identity := candidateIdentityForTerminalTokenDelta(revision, delta)
		if existing, _, ok := a.activationForTerminalTokenIdentity(rule, delta.token, identity); ok {
			existing.incrementSupport()
			if existing.status == activationStatusDeactivated {
				rearmActivationToken(existing, delta.token)
				existing.status = activationStatusPending
				a.enqueueActivation(existing)
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

		created, err := a.newTerminalActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
		if err != nil {
			return nil, err
		}
		created.supportCount = 1
		a.storePreparedActivation(created)
		a.enqueueActivation(created)
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
		update.after.retainChain()
		existing.token.releaseChain()
		existing.token = update.after
		existing.maxRecency = update.after.maxRecency()
		existing.totalRecency = update.after.totalRecency()
		if existing.status == activationStatusPending {
			a.fixActivationOrder(existing)
			continue
		}
	}
	return nil
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

	changes := a.purgeChanges[:0]
	a.forEachPendingActivation(func(current *activation) bool {
		if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
			return true
		}
		a.dequeueActivation(current)
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(current),
		})
		return true
	})

	a.clearActivationLookup()

	oldActivations := a.activations
	a.activationLookup = make(map[activationLookupKey]activationLookupBucket, len(oldActivations))
	a.activations = make([]*activation, 0, len(oldActivations))
	for _, current := range oldActivations {
		if current == nil {
			continue
		}
		if _, ok := revisionIDs[current.ruleRevisionID]; ok {
			continue
		}
		a.storeActivationRef(current)
	}
	clear(oldActivations)

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
	for {
		queue, ok := a.nextQueue()
		if !ok {
			return nil, activation{}, false
		}
		current, ok := queue.pop(a)
		if queue.empty() {
			delete(a.moduleQueues, queue.module)
		}
		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusConsumed
		selected := a.activationRunSnapshot(current)
		a.compactConsumedTokenActivation(current)
		return current, selected, true
	}
}

func (a *agenda) nextActivationPtrForModule(module ModuleName) (*activation, activation, bool) {
	if a == nil {
		return nil, activation{}, false
	}
	module = normalizeModuleName(module)
	if module.IsZero() {
		module = MainModule
	}
	queue := a.moduleQueues[module]
	for queue != nil && !queue.empty() {
		current, ok := queue.pop(a)
		if queue.empty() {
			delete(a.moduleQueues, module)
		}
		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusConsumed
		selected := a.activationRunSnapshot(current)
		a.compactConsumedTokenActivation(current)
		return current, selected, true
	}
	return nil, activation{}, false
}

func (a *agenda) nextQueue() (*agendaModuleQueue, bool) {
	if a == nil {
		return nil, false
	}
	var best *agendaModuleQueue
	var bestAct *activation
	for module, queue := range a.moduleQueues {
		for queue != nil && !queue.empty() {
			act := queue.heap[1]
			if act != nil && act.status == activationStatusPending {
				break
			}
			queue.pop(a)
		}
		if queue == nil || queue.empty() {
			delete(a.moduleQueues, module)
			continue
		}
		act := queue.heap[1]
		if bestAct == nil || a.activationLess(act, bestAct) {
			best = queue
			bestAct = act
		}
	}
	return best, best != nil
}

// compactConsumedTokenActivation swaps a fired activation from token-backed to
// slice-backed identity so its token chain can be recycled. Identity lookups
// fall back to the materialized fact slices; a reactivation re-arms the token
// from the incoming delta.
func (a *agenda) compactConsumedTokenActivation(current *activation) {
	if current == nil || current.status != activationStatusConsumed || current.token.isZero() {
		return
	}
	payload := a.takeActivationPayload()
	factIDs, factVersions, ok := materializePublicTokenFactsInto(current.token, payload.factIDs, payload.factVersions)
	payload.factIDs = factIDs
	payload.factVersions = factVersions
	if !ok {
		a.recycleActivationPayload(payload)
		current.payload = nil
		return
	}
	current.payload = payload
	current.token.releaseChain()
	current.token = tokenRef{}
}

// compactDeactivatedTokenActivation mirrors compactConsumedTokenActivation for
// activations invalidated before firing, so their token chains stop pinning
// arena rows for the rest of the run.
func (a *agenda) compactDeactivatedTokenActivation(current *activation) {
	if current == nil || current.status != activationStatusDeactivated || current.token.isZero() {
		return
	}
	// Reusing populated tuple arrays in place would corrupt them if the
	// materialization failed partway; only empty arrays are reused.
	var scratchIDs []FactID
	var scratchVersions []FactVersion
	pooled := (*activationPayload)(nil)
	if existing := current.payload; existing != nil {
		if len(existing.factIDs) == 0 && len(existing.factVersions) == 0 {
			scratchIDs = existing.factIDs
			scratchVersions = existing.factVersions
		}
	} else {
		pooled = a.takeActivationPayload()
		scratchIDs = pooled.factIDs
		scratchVersions = pooled.factVersions
	}
	factIDs, factVersions, ok := materializePublicTokenFactsInto(current.token, scratchIDs, scratchVersions)
	if !ok {
		if pooled != nil {
			pooled.factIDs = factIDs
			pooled.factVersions = factVersions
			a.recycleActivationPayload(pooled)
		}
		return
	}
	payload := current.payload
	if payload == nil {
		payload = pooled
		current.payload = payload
	}
	payload.factIDs = factIDs
	payload.factVersions = factVersions
	current.token.releaseChain()
	current.token = tokenRef{}
}

// rearmActivationToken restores token backing on a reactivated activation that
// released its token when it was consumed. The incoming delta token carries the
// same public fact tuple, so bindings are unchanged.
func rearmActivationToken(existing *activation, token tokenRef) {
	if existing == nil || !existing.token.isZero() || token.isZero() {
		return
	}
	existing.token = token
	existing.token.retainChain()
}

// materializePublicTokenFactsInto reuses the capacity of ids and versions
// when possible. On failure the returned slices carry whatever was written so
// callers can recycle them; their previous contents are not preserved.
func materializePublicTokenFactsInto(token tokenRef, ids []FactID, versions []FactVersion) ([]FactID, []FactVersion, bool) {
	if token.isZero() {
		return nil, nil, true
	}
	size := token.size()
	if cap(ids) < size {
		ids = make([]FactID, 0, size)
	} else {
		ids = ids[:0]
	}
	if cap(versions) < size {
		versions = make([]FactVersion, 0, size)
	} else {
		versions = versions[:0]
	}
	current := token
	for {
		id, version, hasFact, ok := nextPublicTokenFact(&current)
		if !ok {
			return ids, versions, false
		}
		if !hasFact {
			break
		}
		ids = append(ids, id)
		versions = append(versions, version)
	}
	slices.Reverse(ids)
	slices.Reverse(versions)
	return ids, versions, true
}

func (a *agenda) activationRunSnapshot(current *activation) activation {
	return activationRunSnapshot(current)
}

func (a *agenda) activationModule(act *activation) ModuleName {
	if act == nil {
		return ""
	}
	return act.module
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
		out.setBindings(cloneBindingTupleEntries(current.bindings()))
		out.setFactIDs(cloneFactIDs(current.factIDs()))
		out.setFactVersions(cloneFactVersions(current.factVersions()))
		out.setPath(cloneIntPath(current.path()))
		return out
	}
	return activation{
		key:              current.key,
		ruleRevisionID:   current.ruleRevisionID,
		identityKey:      current.identityKey,
		token:            current.token,
		module:           current.module,
		salience:         current.salience,
		declarationOrder: current.declarationOrder,
		maxRecency:       current.maxRecency,
		totalRecency:     current.totalRecency,
		status:           current.status,
	}
}

func (a *agenda) clear() []agendaChange {
	if a == nil {
		return nil
	}
	changes := make([]agendaChange, 0, a.pendingActivationCount())
	a.forEachPendingActivation(func(current *activation) bool {
		a.dequeueActivation(current)
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(current),
		})
		return true
	})
	a.reset()
	return changes
}

func (a *agenda) materializePendingTokenFacts(revision *Ruleset) {
	if a == nil || revision == nil {
		return
	}
	a.forEachPendingActivation(func(current *activation) bool {
		if current.token.isZero() {
			return true
		}
		if len(current.factIDs()) > 0 && len(current.factVersions()) > 0 {
			return true
		}
		rule, ok := revision.rulesByRevisionID[current.ruleRevisionID]
		if !ok {
			return true
		}
		factIDs, factVersions, ok := terminalTokenFactTuple(rule, current.token)
		if !ok {
			return true
		}
		current.setFactIDs(cloneFactIDs(factIDs))
		current.setFactVersions(cloneFactVersions(factVersions))
		return true
	})
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
	if a == nil {
		return nil
	}
	count := a.pendingActivationCount()
	if count == 0 {
		return nil
	}
	out := make([]activation, 0, count)
	a.forEachPendingActivation(func(current *activation) bool {
		out = append(out, a.publicActivation(current))
		return true
	})
	sortActivations(out)
	return out
}

func (a *agenda) forEachPendingActivation(fn func(*activation) bool) {
	if a == nil || fn == nil {
		return
	}
	scratch := a.pendingScratch[:0]
	for _, queue := range a.moduleQueues {
		if queue == nil {
			continue
		}
		for i := 1; i < len(queue.heap); i++ {
			current := queue.heap[i]
			if current == nil || current.status != activationStatusPending {
				continue
			}
			scratch = append(scratch, current)
		}
	}
	a.pendingScratch = scratch
	for _, current := range scratch {
		if current == nil || current.status != activationStatusPending {
			continue
		}
		if !fn(current) {
			break
		}
	}
	clear(scratch)
	a.pendingScratch = scratch[:0]
}

func (a *agenda) activationsByFactID(id FactID) []activation {
	if a == nil || id.IsZero() {
		return nil
	}
	var out []activation
	a.forEachActivation(func(current *activation) bool {
		if activationContainsFactID(current, id) {
			out = append(out, a.publicActivation(current))
		}
		return true
	})
	sortActivations(out)
	return out
}

func (a *agenda) activationsByRuleRevisionID(id RuleRevisionID) []activation {
	if a == nil {
		return nil
	}
	var out []activation
	a.forEachActivation(func(current *activation) bool {
		if current != nil && current.ruleRevisionID == id {
			out = append(out, a.publicActivation(current))
		}
		return true
	})
	sortActivations(out)
	return out
}

func activationContainsFactID(act *activation, id FactID) bool {
	if act == nil || id.IsZero() {
		return false
	}
	if !act.token.isZero() {
		return act.token.containsFact(id)
	}
	return slices.Contains(act.factIDs(), id)
}

func factIDSeenBefore(ids []FactID, id FactID) bool {
	return slices.Contains(ids, id)
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
	if left.totalRecency != right.totalRecency {
		return left.totalRecency > right.totalRecency
	}
	if left.identityKey.scopeHash < right.identityKey.scopeHash {
		return true
	}
	if left.identityKey.scopeHash != right.identityKey.scopeHash {
		return false
	}
	if left.identityKey.hash < right.identityKey.hash {
		return true
	}
	if left.identityKey.hash != right.identityKey.hash {
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
	if left.totalRecency != right.totalRecency {
		return left.totalRecency > right.totalRecency
	}
	if left.ruleRevisionID != right.ruleRevisionID && left.declarationOrder != right.declarationOrder {
		return left.declarationOrder < right.declarationOrder
	}
	if left.identityKey.scopeHash < right.identityKey.scopeHash {
		return true
	}
	if left.identityKey.scopeHash != right.identityKey.scopeHash {
		return false
	}
	if left.identityKey.hash < right.identityKey.hash {
		return true
	}
	if left.identityKey.hash != right.identityKey.hash {
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
	var found *activation
	a.forEachActivationLookup(activationLookupKey{
		ruleRevisionID: candidate.ruleRevisionID,
		identityKey:    candidate.identity.key,
	}, func(current *activation) bool {
		if activationMatchesCandidate(current, candidate) {
			found = current
			return false
		}
		return true
	})
	if found != nil {
		return found, found.key, true
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
	var found *activation
	a.forEachActivationLookup(activationLookupKey{
		ruleRevisionID: rule.revisionID,
		identityKey:    identity.key,
	}, func(current *activation) bool {
		if activationMatchesTerminalToken(current, rule, identity, token) {
			found = current
			return false
		}
		return true
	})
	if found != nil {
		return found, found.key, true
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
	var found *activation
	a.forEachActivationLookup(activationLookupKey{
		ruleRevisionID: rule.revisionID,
		identityKey:    identity.key,
	}, func(current *activation) bool {
		if activationMatchesTerminalTokenDelta(current, rule, identity, delta) {
			found = current
			return false
		}
		return true
	})
	if found != nil {
		return found, found.key, true
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
	if current.identityKey != candidate.identity.key {
		return false
	}
	if !current.token.isZero() {
		return matchTokenFactsEqualSlices(current.token, candidate.factIDs, candidate.factVersions)
	}
	return factVersionSlicesEqual(current.factIDs(), current.factVersions(), candidate.factIDs, candidate.factVersions)
}

func activationMatchesTerminalToken(current *activation, rule compiledRule, identity candidateIdentity, token tokenRef) bool {
	if current == nil {
		return false
	}
	if current.ruleRevisionID != rule.revisionID {
		return false
	}
	if current.identityKey != identity.key {
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
	if current.identityKey != identity.key {
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
	return key
}

func (a *agenda) storePreparedActivation(act *activation) activationKey {
	if act == nil {
		return activationKey{}
	}
	a.ensureHandleGeneration()
	key := activationKey{
		ordinal: a.nextOrdinal + 1,
	}
	a.nextOrdinal++
	act.key = key
	act.token.retainChain()
	a.storeActivationRef(act)
	if a.propagationCounters != nil {
		a.propagationCounters.recordActivationStored()
	}
	return key
}

func (a *agenda) storeActivationRef(act *activation) {
	if a == nil || act == nil {
		return
	}
	a.activations = append(a.activations, act)
	a.storeActivationLookupRef(act)
}

func (a *agenda) storeActivationLookupRef(act *activation) {
	if a == nil || act == nil || act.key == (activationKey{}) {
		return
	}
	if a.activationLookup == nil {
		a.activationLookup = make(map[activationLookupKey]activationLookupBucket)
	}
	key := activationLookupKey{
		ruleRevisionID: act.ruleRevisionID,
		identityKey:    act.identityKey,
	}
	bucket := a.activationLookup[key]
	if bucket.first == nil && len(bucket.rest) == 0 {
		bucket.first = act
	} else {
		bucket.rest = append(bucket.rest, act)
	}
	a.activationLookup[key] = bucket
}

func (a *agenda) clearActivationLookup() {
	if a == nil {
		return
	}
	for key, bucket := range a.activationLookup {
		clear(bucket.rest)
		delete(a.activationLookup, key)
	}
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

func (a *agenda) activationByKeyPtr(key activationKey) (*activation, bool) {
	if a == nil || key == (activationKey{}) {
		return nil, false
	}
	for _, current := range a.activations {
		if current == nil || current.key != key {
			continue
		}
		return current, true
	}
	return nil, false
}

func (a *agenda) compactConsumedActivationRows() {
	if a == nil {
		return
	}
	a.compactActivationStorage()
	if a.pendingActivationCount() == 0 {
		a.releaseCompletedRunStorage()
	}
}

const compactRetiredActivationFloor = 64

// maybeCompactActivationStorage compacts retired activation rows mid-run once
// they outnumber pending work. Safe only at the fire boundary, when no
// activation pointers are held outside the agenda's own storage.
func (a *agenda) maybeCompactActivationStorage() {
	if a == nil {
		return
	}
	pending := a.pendingActivationCount()
	retired := a.activationRows.count + a.terminalActivations.count - pending
	if retired <= max(compactRetiredActivationFloor, 4*pending) {
		return
	}
	a.compactActivationStorage()
}

// compactActivationStorage drops consumed and deactivated activation rows,
// releasing their token chains, and rebuilds the lookup, ref slice, and module
// queues over the surviving pending activations. Storage from the previous
// compaction is recycled so repeated mid-run compaction does not churn.
func (a *agenda) compactActivationStorage() {
	if a == nil || len(a.activations) == 0 {
		return
	}
	oldActivations := a.activations
	pendingCount := a.pendingActivationCount()

	a.clearActivationLookup()
	if a.activationLookup == nil {
		a.activationLookup = make(map[activationLookupKey]activationLookupBucket, pendingCount)
	}
	a.activations = a.activationsSpare[:0]
	a.resetModuleQueues()

	nextRows := a.activationRowsSpare
	a.activationRowsSpare = activationRows{}
	for _, current := range oldActivations {
		if current == nil {
			continue
		}
		if current.status != activationStatusPending {
			current.token.releaseChain()
			a.recycleActivationPayload(current.payload)
			current.payload = nil
			continue
		}
		stored := nextRows.add(*current)
		stored.heapIndex = 0
		a.storeActivationRef(stored)
		a.enqueueActivation(stored)
	}

	oldRows := a.activationRows
	a.activationRows = nextRows
	oldRows.recycle()
	a.activationRowsSpare = oldRows
	a.terminalActivations.recycle()
	clear(oldActivations)
	a.activationsSpare = oldActivations[:0]
}

func (a *agenda) releaseCompletedRunStorage() {
	if a == nil {
		return
	}
	a.activationLookup = nil
	a.activations = nil
	a.activationRows = activationRows{}
	a.terminalActivations = activationRows{}
	a.activationRowsSpare = activationRows{}
	a.activationsSpare = nil
	a.payloadPool = nil
	a.moduleQueues = nil
	a.reconcileSeen = nil
	a.reconcileChanges = nil
	a.reconcileActivated = nil
	a.deltaChanges = nil
	a.deltaActivated = nil
	a.purgeChanges = nil
	a.pendingScratch = nil
}

func (a *agenda) forEachActivationLookup(key activationLookupKey, fn func(*activation) bool) {
	if a == nil || fn == nil {
		return
	}
	bucket := a.activationLookup[key]
	if bucket.first != nil && !fn(bucket.first) {
		return
	}
	for _, current := range bucket.rest {
		if current == nil {
			continue
		}
		if !fn(current) {
			return
		}
	}
}

func (a *agenda) forEachActivation(fn func(*activation) bool) {
	if a == nil || fn == nil {
		return
	}
	for _, current := range a.activations {
		if current == nil {
			continue
		}
		if !fn(current) {
			return
		}
	}
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

func activationGeneration(act *activation) Generation {
	if act == nil {
		return 0
	}
	if !act.token.isZero() {
		return tokenRefGeneration(act.token)
	}
	factIDs := act.factIDs()
	if len(factIDs) == 0 {
		return 0
	}
	return factIDs[0].Generation()
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
	if index >= len(factIDs) {
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
	if index >= len(factVersions) {
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
	dst.ruleRevisionID = candidate.ruleRevisionID
	dst.identityKey = candidate.identity.key
	dst.module = rule.module
	dst.heapIndex = 0
	dst.payload = nil
	dst.setBindings(cloneBindingTupleEntries(candidate.bindingTuple))
	dst.setFactIDs(cloneFactIDs(candidate.factIDs))
	dst.setFactVersions(cloneFactVersions(candidate.factVersions))
	dst.salience = rule.salience
	dst.declarationOrder = rule.declarationOrder
	dst.maxRecency = candidate.maxRecency
	dst.totalRecency = candidate.totalRecency
	dst.supportCount = 1
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

func (a *agenda) newTerminalActivationFromTerminalTokenWithIdentity(rule compiledRule, token tokenRef, identity candidateIdentity) (*activation, error) {
	if a == nil {
		return nil, ErrInvalidRuleset
	}
	rowCount := a.terminalActivations.count
	out := a.terminalActivations.addEmpty()
	if err := fillActivationFromTerminalTokenWithIdentity(out, rule, token, identity); err != nil {
		a.terminalActivations.truncate(rowCount)
		return nil, err
	}
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
	dst.ruleRevisionID = rule.revisionID
	dst.identityKey = identity.key
	dst.token = token
	dst.module = rule.module
	dst.heapIndex = 0
	dst.salience = rule.salience
	dst.declarationOrder = rule.declarationOrder
	dst.maxRecency = row.maxRecency
	dst.totalRecency = row.totalRecency
	dst.status = activationStatusPending
	return nil
}

func candidateIdentityForTerminalToken(rule compiledRule, token tokenRef) candidateIdentity {
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
	if match.bindingSlot < 0 {
		return state, count, true
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
	if len(factIDs) != len(factVersions) {
		return false
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
	if match.bindingSlot < 0 {
		return next, true
	}
	if next >= len(factIDs) || next >= len(factVersions) {
		return next, false
	}
	if factIDs[next] != match.fact.ID() || factVersions[next] != match.fact.Version() {
		return next, false
	}
	return next + 1, true
}

func terminalTokenFactVersionsEqual(left, right tokenRef) bool {
	l, r := left, right
	for {
		leftID, leftVersion, leftHas, leftOK := nextPublicTokenFact(&l)
		rightID, rightVersion, rightHas, rightOK := nextPublicTokenFact(&r)
		if !leftOK || !rightOK || leftHas != rightHas {
			return false
		}
		if !leftHas {
			return true
		}
		if leftID != rightID || leftVersion != rightVersion {
			return false
		}
	}
}

// nextPublicTokenFact advances token toward the root and returns the next
// public (bindingSlot >= 0) row's fact identity. hasFact is false once the
// chain is exhausted; ok is false when a row fails to resolve.
func nextPublicTokenFact(token *tokenRef) (id FactID, version FactVersion, hasFact bool, ok bool) {
	for !token.isZero() {
		row, resolved := token.resolve()
		if !resolved {
			return FactID{}, 0, false, false
		}
		slot := row.bindingSlot
		id, version = row.factIdentity()
		*token = tokenRef{handle: tokenHandle{arena: token.handle.arena, rowID: row.parent.rowID, generation: row.parent.gen}}
		if slot < 0 {
			continue
		}
		return id, version, true, true
	}
	return FactID{}, 0, false, true
}

func cloneFactVersions(versions []FactVersion) []FactVersion {
	if len(versions) == 0 {
		return nil
	}
	out := make([]FactVersion, len(versions))
	copy(out, versions)
	return out
}
