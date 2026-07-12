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

// Strategy selects the conflict-resolution ordering a session uses among
// pending activations of equal salience within the focused module. Salience,
// module focus, refraction, and activation identity are strategy-independent.
type Strategy uint8

const (
	// StrategyDepth is the default: equal-salience activations fire most
	// recent first (LIFO), with deterministic declaration-order tie-breaks.
	StrategyDepth Strategy = iota
	// StrategyBreadth fires equal-salience activations in birth order (FIFO).
	// Activations born in one propagation epoch use their canonical authored
	// binding tuple as the deterministic peer order.
	StrategyBreadth
)

func (s Strategy) valid() bool {
	switch s {
	case StrategyDepth, StrategyBreadth:
		return true
	default:
		return false
	}
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
	birthEpoch       uint64
	birthRank        uint64
	module           ModuleName
	heapIndex        int
	salience         int
	declarationOrder int
	maxRecency       Recency
	totalRecency     Recency
	supportCount     uint32
	status           activationStatus
	payload          *activationPayload
	queryProofID     backchainQueryProofID
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
		queryProofID:          a.queryProofID,
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

func (q *agendaModuleQueue) peekPending(a *agenda) (*activation, bool) {
	for q != nil && !q.empty() {
		act := q.heap[1]
		if act != nil && act.status == activationStatusPending {
			return act, true
		}
		q.pop(a)
	}
	return nil, false
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
	return c.eventWithRuleID(sessionID, rulesetID, "", SourceSpan{}, sequence, timestamp)
}

func (c agendaChange) eventWithRuleID(sessionID SessionID, rulesetID RulesetID, ruleID RuleID, source SourceSpan, sequence uint64, timestamp time.Time) Event {
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
		Source:         source,
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
			binding:        condition.BindingName,
			bindingSlot:    i,
			conditionOrder: condition.Order,
			conditionID:    condition.IDValue,
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
			binding:        condition.BindingName,
			bindingSlot:    i,
			conditionOrder: condition.Order,
			conditionID:    condition.IDValue,
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
		binding:        condition.BindingName,
		bindingSlot:    index,
		conditionOrder: condition.Order,
		conditionID:    condition.IDValue,
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
	lessActivation      func(*activation, *activation) bool
	strategy            Strategy
	nextOrdinal         uint64
	nextBirthEpoch      uint64
	initialBirthEpoch   uint64
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

	birthActivationScratch []*activation
	birthRecordScratch     []activationBirthRankRecord
	birthEntryScratch      []activationBirthSortEntry
	birthSeenScratch       []uint64

	activationRowsSpare activationRows
	activationsSpare    []*activation
	payloadPool         []*activationPayload
}

func (a *agenda) rebindRevision(revision *Ruleset) {
	if a != nil {
		a.revision = revision
	}
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
	return newAgendaWithStrategy(StrategyDepth)
}

func newAgendaWithStrategy(strategy Strategy) *agenda {
	agenda := &agenda{
		activationLookup: make(map[activationLookupKey]activationLookupBucket),
		moduleQueues:     make(map[ModuleName]*agendaModuleQueue),
		strategy:         strategy,
		handleGeneration: 1,
	}
	if strategy == StrategyBreadth {
		agenda.lessActivation = agenda.activationBreadthLess
	} else {
		agenda.lessActivation = agenda.activationDepthLess
	}
	return agenda
}

func (a *agenda) cloneForFork(strategy Strategy) *agenda {
	if a == nil {
		return newAgendaWithStrategy(strategy)
	}
	out := newAgendaWithStrategy(strategy)
	out.nextOrdinal = a.nextOrdinal
	out.nextBirthEpoch = a.nextBirthEpoch
	out.initialBirthEpoch = a.initialBirthEpoch
	out.handleGeneration = a.handleGeneration
	out.revision = a.revision
	if out.handleGeneration == 0 {
		out.handleGeneration = 1
	}
	for _, current := range a.activations {
		if current == nil {
			continue
		}
		cloned := a.cloneActivationForFork(current)
		cloned.heapIndex = 0
		stored := out.activationRows.add(cloned)
		out.storeActivationRef(stored)
		if stored.status == activationStatusPending {
			out.enqueueActivation(stored)
		}
	}
	return out
}

// cloneActivationForFork materializes token-backed bindings before the clone
// drops its token, so value bindings (aggregate results) survive the fork.
func (a *agenda) cloneActivationForFork(act *activation) activation {
	out := act.clone()
	if act.token.isZero() || len(out.bindings()) > 0 || a.revision == nil {
		return out
	}
	rule, ok := a.revision.rulesByRevisionID[act.ruleRevisionID]
	if !ok {
		return out
	}
	if entries := activationBindingTupleEntriesForActivation(rule, act, false); len(entries) > 0 {
		out.setBindings(entries)
	}
	return out
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
	a.nextBirthEpoch = 0
	a.initialBirthEpoch = 0
	a.clearBirthRankScratch()
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
	breadth := a.strategy == StrategyBreadth
	birthEpoch := uint64(0)
	var birthActivations []*activation
	if breadth {
		birthActivations = a.birthActivationScratch[:0]
		defer func() {
			clear(birthActivations)
			a.birthActivationScratch = birthActivations[:0]
		}()
	}

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
					if breadth {
						a.setActivationBirth(existing, a.defaultBirthEpoch(&birthEpoch), 0)
						birthActivations = append(birthActivations, existing)
					}
					a.enqueueActivation(existing)
					seen[key] = struct{}{}
					if !breadth {
						activated = append(activated, agendaChange{
							kind:       agendaChangeActivated,
							activation: a.compactChangeActivation(existing),
						})
					}
				}
				continue
			}

			created := a.activationRows.addEmpty()
			fillActivationFromCandidate(created, rule, candidate)
			key = a.storePreparedActivation(created)
			if breadth {
				a.setActivationBirth(created, a.defaultBirthEpoch(&birthEpoch), 0)
				birthActivations = append(birthActivations, created)
			}
			a.enqueueActivation(created)

			seen[key] = struct{}{}
			if !breadth {
				activated = append(activated, agendaChange{
					kind:       agendaChangeActivated,
					activation: a.compactChangeActivation(created),
				})
			}
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

	if breadth {
		a.assignCanonicalBirthRanks(birthEpoch, birthActivations)
		slices.SortStableFunc(birthActivations, activationBirthCompare)
		slices.SortStableFunc(changes, agendaChangeBirthCompare)
		for _, current := range birthActivations {
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(current),
			})
		}
	}
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
	breadth := a.strategy == StrategyBreadth
	birthEpoch := uint64(0)
	var birthActivations []*activation
	if breadth {
		birthActivations = a.birthActivationScratch[:0]
		defer func() {
			clear(birthActivations)
			a.birthActivationScratch = birthActivations[:0]
		}()
	}
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
		if breadth {
			a.setActivationBirth(created, a.defaultBirthEpoch(&birthEpoch), 0)
			birthActivations = append(birthActivations, created)
		}
		a.enqueueActivation(created)
		if !breadth {
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(created),
			})
		}
	}
	if breadth {
		a.assignCanonicalBirthRanks(birthEpoch, birthActivations)
		slices.SortStableFunc(birthActivations, activationBirthCompare)
		slices.SortStableFunc(changes, agendaChangeBirthCompare)
		for _, current := range birthActivations {
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(current),
			})
		}
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
		a.linkTokenActivation(delta.token, existing)
		if existing.status == activationStatusDeactivated {
			rearmActivationToken(existing, delta.token)
			existing.status = activationStatusPending
			if a.strategy == StrategyBreadth {
				a.setActivationBirth(existing, a.ensureInitialBirthEpoch(), 0)
			}
			a.enqueueActivation(existing)
		}
		return existing, nil
	}
	created, err := a.newTerminalActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
	if err != nil {
		return nil, err
	}
	created.supportCount = 1
	created.queryProofID = delta.queryProofID
	a.storePreparedActivation(created)
	if a.strategy == StrategyBreadth {
		a.setActivationBirth(created, a.ensureInitialBirthEpoch(), 0)
	}
	a.enqueueActivation(created)
	a.linkTokenActivation(delta.token, created)
	return created, nil
}

func (a *agenda) finishInitialTerminalActivations() {
	if a == nil {
		return
	}
	if a.strategy != StrategyBreadth || a.initialBirthEpoch == 0 {
		return
	}
	epoch := a.initialBirthEpoch
	initial := a.birthActivationScratch[:0]
	a.forEachPendingActivation(func(current *activation) bool {
		if current != nil && current.birthEpoch == epoch {
			initial = append(initial, current)
		}
		return true
	})
	a.assignCanonicalBirthRanks(epoch, initial)
	a.initialBirthEpoch = 0
	clear(initial)
	a.birthActivationScratch = initial[:0]
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
	breadth := a.strategy == StrategyBreadth
	defaultBirthEpoch := uint64(0)

	if len(removed) == 1 {
		delta := removed[0]
		if !delta.token.isZero() {
			existing := a.takeLinkedTokenActivation(delta)
			if existing == nil {
				rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
				if !ok {
					return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
				}
				if found, _, ok := a.activationForTerminalTokenDelta(rule, delta); ok {
					existing = found
				}
			}
			if existing != nil {
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
		a.linkTokenActivation(delta.token, existing)
		if existing.status == activationStatusDeactivated {
			rearmActivationToken(existing, delta.token)
			existing.status = activationStatusPending
			a.enqueueActivation(existing)
			if breadth {
				epoch := delta.birthEpoch
				if epoch == 0 {
					epoch = a.defaultBirthEpoch(&defaultBirthEpoch)
				}
				a.setActivationBirth(existing, epoch, 0)
				a.assignCanonicalBirthRanks(epoch, []*activation{existing})
			}
		}
		return existing, nil
	}
	created, err := a.newTerminalActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
	if err != nil {
		return nil, err
	}
	created.supportCount = 1
	created.queryProofID = delta.queryProofID
	a.storePreparedActivation(created)
	a.enqueueActivation(created)
	if breadth {
		epoch := delta.birthEpoch
		if epoch == 0 {
			epoch = a.defaultBirthEpoch(&defaultBirthEpoch)
		}
		a.setActivationBirth(created, epoch, 0)
		a.assignCanonicalBirthRanks(epoch, []*activation{created})
	}
	a.linkTokenActivation(delta.token, created)
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
	breadth := a.strategy == StrategyBreadth
	defaultBirthEpoch := uint64(0)
	var breadthActivated []*activation
	if breadth {
		breadthActivated = a.birthActivationScratch[:0]
		defer func() {
			clear(breadthActivated)
			a.birthActivationScratch = breadthActivated[:0]
		}()
	}
	for _, delta := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token.isZero() {
			continue
		}
		existing := a.takeLinkedTokenActivation(delta)
		if existing == nil {
			rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
			if !ok {
				return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
			}
			found, _, ok := a.activationForTerminalTokenDelta(rule, delta)
			if !ok {
				continue
			}
			existing = found
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
	if breadth && collectChanges {
		slices.SortStableFunc(changes, agendaChangeBirthCompare)
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
			a.linkTokenActivation(delta.token, existing)
			if existing.status == activationStatusDeactivated {
				rearmActivationToken(existing, delta.token)
				existing.status = activationStatusPending
				a.enqueueActivation(existing)
				if breadth {
					epoch := delta.birthEpoch
					if epoch == 0 {
						epoch = a.defaultBirthEpoch(&defaultBirthEpoch)
					}
					a.setActivationBirth(existing, epoch, 0)
					breadthActivated = append(breadthActivated, existing)
				} else {
					if collectChanges {
						activated = append(activated, agendaChange{
							kind:       agendaChangeActivated,
							activation: a.compactChangeActivation(existing),
						})
					}
				}
			}
			if !breadth && observeActivation != nil {
				observeActivation(existing)
			}
			continue
		}

		created, err := a.newTerminalActivationFromTerminalTokenWithIdentity(rule, delta.token, identity)
		if err != nil {
			return nil, err
		}
		created.supportCount = 1
		created.queryProofID = delta.queryProofID
		a.storePreparedActivation(created)
		a.enqueueActivation(created)
		if breadth {
			epoch := delta.birthEpoch
			if epoch == 0 {
				epoch = a.defaultBirthEpoch(&defaultBirthEpoch)
			}
			a.setActivationBirth(created, epoch, 0)
			breadthActivated = append(breadthActivated, created)
		} else {
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
		a.linkTokenActivation(delta.token, created)
	}
	if breadth {
		a.assignCanonicalBirthRanksByEpoch(breadthActivated)
		slices.SortStableFunc(breadthActivated, activationBirthCompare)
		for _, current := range breadthActivated {
			if current == nil {
				continue
			}
			if observeActivation != nil {
				observeActivation(current)
			}
			if collectChanges {
				activated = append(activated, agendaChange{
					kind:       agendaChangeActivated,
					activation: a.compactChangeActivation(current),
				})
			}
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
		existing.token = update.after
		existing.maxRecency = update.after.maxRecency()
		existing.totalRecency = update.after.totalRecency()
		if !update.queryProofID.isZero() {
			existing.queryProofID = update.queryProofID
		}
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

func (a *agenda) nextInternalPtrForQueryProof(proofID backchainQueryProofID) (*activation, activation, bool) {
	return a.nextActivationPtrForQueryProof(proofID)
}

func (a *agenda) hasPendingActivation() bool {
	_, ok := a.peekActivationPtr()
	return ok
}

func (a *agenda) hasPendingActivationForModule(module ModuleName) bool {
	_, ok := a.peekActivationPtrForModule(module)
	return ok
}

func (a *agenda) hasPendingActivationForQueryProof(proofID backchainQueryProofID) bool {
	if a == nil || proofID.isZero() {
		return false
	}
	for _, current := range a.activations {
		if current != nil && current.status == activationStatusPending && current.queryProofID == proofID {
			return true
		}
	}
	return false
}

func (a *agenda) peekActivationPtr() (*activation, bool) {
	queue, ok := a.nextQueue()
	if !ok {
		return nil, false
	}
	return queue.peekPending(a)
}

func (a *agenda) peekActivationPtrForModule(module ModuleName) (*activation, bool) {
	if a == nil {
		return nil, false
	}
	module = normalizeModuleName(module)
	if module.IsZero() {
		module = MainModule
	}
	queue := a.moduleQueues[module]
	if queue == nil {
		return nil, false
	}
	act, ok := queue.peekPending(a)
	if queue.empty() {
		delete(a.moduleQueues, module)
	}
	return act, ok
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

func (a *agenda) nextActivationPtrForQueryProof(proofID backchainQueryProofID) (*activation, activation, bool) {
	if a == nil || proofID.isZero() {
		return nil, activation{}, false
	}
	var best *activation
	for _, current := range a.activations {
		if current == nil || current.status != activationStatusPending || current.queryProofID != proofID {
			continue
		}
		if best == nil || a.activationLess(current, best) {
			best = current
		}
	}
	if best == nil || !a.dequeueActivation(best) {
		return nil, activation{}, false
	}
	best.status = activationStatusConsumed
	selected := a.activationRunSnapshot(best)
	a.compactConsumedTokenActivation(best)
	return best, selected, true
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
		queryProofID:     current.queryProofID,
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
	return activationDepthLess(nil, left, right)
}

func activationDepthLess(revision *Ruleset, left, right *activation) bool {
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
	if revision != nil && left.ruleRevisionID != right.ruleRevisionID && left.declarationOrder != right.declarationOrder {
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

func (a *agenda) activationLess(left, right *activation) bool {
	return a.lessActivation(left, right)
}

func (a *agenda) activationDepthLess(left, right *activation) bool {
	if a == nil {
		return activationDepthLess(nil, left, right)
	}
	return activationDepthLess(a.revision, left, right)
}

func (a *agenda) activationBreadthLess(left, right *activation) bool {
	if left == nil || right == nil {
		return right != nil
	}
	if left.salience != right.salience {
		return left.salience > right.salience
	}
	if left.birthEpoch != right.birthEpoch {
		return left.birthEpoch < right.birthEpoch
	}
	if left.birthRank != right.birthRank {
		return left.birthRank < right.birthRank
	}
	if left.key.ordinal < right.key.ordinal {
		return true
	}
	if left.key.ordinal != right.key.ordinal {
		return false
	}
	return false
}

func activationBirthLess(left, right *activation) bool {
	if left == nil || right == nil {
		return right != nil
	}
	if left.birthEpoch != right.birthEpoch {
		return left.birthEpoch < right.birthEpoch
	}
	if left.birthRank != right.birthRank {
		return left.birthRank < right.birthRank
	}
	return left.key.ordinal < right.key.ordinal
}

func activationBirthCompare(left, right *activation) int {
	return compareLess(activationBirthLess(left, right), activationBirthLess(right, left))
}

func agendaChangeBirthCompare(left, right agendaChange) int {
	return activationBirthCompare(&left.activation, &right.activation)
}

func (a *agenda) reserveBirthEpoch() uint64 {
	if a == nil {
		return 0
	}
	a.nextBirthEpoch++
	if a.nextBirthEpoch == 0 {
		a.nextBirthEpoch = 1
	}
	return a.nextBirthEpoch
}

func (a *agenda) ensureInitialBirthEpoch() uint64 {
	if a == nil {
		return 0
	}
	if a.initialBirthEpoch == 0 {
		a.initialBirthEpoch = a.reserveBirthEpoch()
	}
	return a.initialBirthEpoch
}

func (a *agenda) defaultBirthEpoch(epoch *uint64) uint64 {
	if epoch == nil {
		return 0
	}
	if *epoch == 0 {
		*epoch = a.reserveBirthEpoch()
	}
	return *epoch
}

func (a *agenda) setActivationBirth(act *activation, epoch, rank uint64) {
	if act == nil {
		return
	}
	act.birthEpoch = epoch
	act.birthRank = rank
}

type activationBirthRankRecord struct {
	act       *activation
	entryBase int
	entryLen  int
}

type activationBirthSortEntry struct {
	entry             bindingTupleEntry
	canonicalValueKey string
}

func activationBirthEntryLess(left, right activationBirthSortEntry) bool {
	if left.entry.conditionOrder != right.entry.conditionOrder {
		return left.entry.conditionOrder < right.entry.conditionOrder
	}
	if left.entry.bindingSlot != right.entry.bindingSlot {
		return left.entry.bindingSlot < right.entry.bindingSlot
	}
	if left.entry.binding != right.entry.binding {
		return left.entry.binding < right.entry.binding
	}
	if left.entry.conditionID != right.entry.conditionID {
		return left.entry.conditionID < right.entry.conditionID
	}
	if left.entry.factID != right.entry.factID {
		return factIDLess(left.entry.factID, right.entry.factID)
	}
	if left.entry.factVersion != right.entry.factVersion {
		return left.entry.factVersion < right.entry.factVersion
	}
	if left.entry.hasValue != right.entry.hasValue {
		return !left.entry.hasValue
	}
	return left.canonicalValueKey < right.canonicalValueKey
}

func (a *agenda) prepareActivationBirthRankRecords(activations []*activation) []activationBirthRankRecord {
	if a == nil || a.revision == nil || len(activations) == 0 {
		return nil
	}
	totalEntries := 0
	for _, act := range activations {
		if act == nil {
			continue
		}
		if rule, ok := a.revision.rulesByRevisionID[act.ruleRevisionID]; ok {
			totalEntries += len(rule.conditions)
		}
	}
	records := slices.Grow(a.birthRecordScratch[:0], len(activations))
	entries := a.birthEntryScratch
	if cap(entries) < totalEntries {
		entries = make([]activationBirthSortEntry, totalEntries)
	} else {
		entries = entries[:totalEntries]
		clear(entries)
	}
	nextEntry := 0
	for _, act := range activations {
		if act == nil {
			continue
		}
		rule, ok := a.revision.rulesByRevisionID[act.ruleRevisionID]
		if !ok {
			records = append(records, activationBirthRankRecord{act: act})
			continue
		}
		entryLen := len(rule.conditions)
		record := activationBirthRankRecord{act: act, entryBase: nextEntry, entryLen: entryLen}
		if !a.fillActivationBirthEntries(rule, act, entries[nextEntry:nextEntry+entryLen]) {
			record.entryLen = 0
		}
		nextEntry += entryLen
		records = append(records, record)
	}
	a.birthRecordScratch = records
	a.birthEntryScratch = entries
	return records
}

func (a *agenda) fillActivationBirthEntries(rule compiledRule, act *activation, dst []activationBirthSortEntry) bool {
	if a == nil || act == nil || len(dst) != len(rule.conditions) {
		return false
	}
	words := (len(dst) + 63) / 64
	if cap(a.birthSeenScratch) < words {
		a.birthSeenScratch = make([]uint64, words)
	} else {
		a.birthSeenScratch = a.birthSeenScratch[:words]
		clear(a.birthSeenScratch)
	}
	seenCount := 0
	put := func(match conditionMatch) bool {
		slot := match.bindingSlot
		if slot < 0 {
			return true
		}
		if slot >= len(dst) {
			return false
		}
		word, mask := slot/64, uint64(1)<<uint(slot%64)
		if a.birthSeenScratch[word]&mask != 0 {
			return false
		}
		condition := rule.conditions[slot]
		entry := bindingTupleEntry{
			binding:        condition.BindingName,
			bindingSlot:    slot,
			conditionOrder: condition.Order,
			conditionID:    condition.IDValue,
			value:          match.value,
			hasValue:       match.hasValue,
		}
		valueKey := ""
		if match.hasValue {
			valueKey = match.value.CanonicalKey()
		} else {
			entry.factID = match.fact.ID()
			entry.factVersion = match.fact.Version()
		}
		dst[slot] = activationBirthSortEntry{entry: entry, canonicalValueKey: valueKey}
		a.birthSeenScratch[word] |= mask
		seenCount++
		return true
	}
	if !act.token.isZero() {
		for current := act.token; !current.isZero(); current = current.parent() {
			row, ok := current.resolve()
			if !ok {
				return false
			}
			match, ok := row.conditionMatch()
			if !ok || !put(match) {
				return false
			}
		}
		return seenCount == len(dst)
	}
	bindings := act.bindings()
	if len(bindings) > 0 {
		for _, entry := range bindings {
			slot := entry.bindingSlot
			if slot < 0 || slot >= len(dst) {
				return false
			}
			word, mask := slot/64, uint64(1)<<uint(slot%64)
			if a.birthSeenScratch[word]&mask != 0 {
				return false
			}
			valueKey := ""
			if entry.hasValue {
				valueKey = entry.value.CanonicalKey()
			}
			dst[slot] = activationBirthSortEntry{entry: entry, canonicalValueKey: valueKey}
			a.birthSeenScratch[word] |= mask
			seenCount++
		}
		return seenCount == len(dst)
	}
	factIDs, factVersions := act.factIDs(), act.factVersions()
	if len(factIDs) != len(dst) || len(factVersions) != len(dst) {
		return false
	}
	for slot := range dst {
		condition := rule.conditions[slot]
		dst[slot].entry = bindingTupleEntry{
			binding:        condition.BindingName,
			bindingSlot:    slot,
			conditionOrder: condition.Order,
			conditionID:    condition.IDValue,
			factID:         factIDs[slot],
			factVersion:    factVersions[slot],
		}
	}
	return true
}

// Same-epoch breadth births are logically simultaneous. Canonical birth ranks
// provide a deterministic authored-tuple order for those peers rather than
// preserving physical insertion order.
func (a *agenda) activationBirthRecordLess(left, right activationBirthRankRecord) bool {
	if left.act == nil || right.act == nil {
		return right.act != nil
	}
	if left.act.declarationOrder != right.act.declarationOrder {
		return left.act.declarationOrder < right.act.declarationOrder
	}
	leftEntries := a.birthEntryScratch[left.entryBase : left.entryBase+left.entryLen]
	rightEntries := a.birthEntryScratch[right.entryBase : right.entryBase+right.entryLen]
	for i := 0; i < len(leftEntries) && i < len(rightEntries); i++ {
		if activationBirthEntryLess(leftEntries[i], rightEntries[i]) {
			return true
		}
		if activationBirthEntryLess(rightEntries[i], leftEntries[i]) {
			return false
		}
	}
	if len(leftEntries) != len(rightEntries) {
		return len(leftEntries) < len(rightEntries)
	}
	if left.act.identityKey.scopeHash != right.act.identityKey.scopeHash {
		return left.act.identityKey.scopeHash < right.act.identityKey.scopeHash
	}
	if left.act.identityKey.hash != right.act.identityKey.hash {
		return left.act.identityKey.hash < right.act.identityKey.hash
	}
	if left.act.ruleRevisionID != right.act.ruleRevisionID {
		return left.act.ruleRevisionID < right.act.ruleRevisionID
	}
	return left.act.key.ordinal < right.act.key.ordinal
}

func (a *agenda) clearBirthRankRecords() {
	if a == nil {
		return
	}
	clear(a.birthRecordScratch)
	a.birthRecordScratch = a.birthRecordScratch[:0]
	clear(a.birthEntryScratch)
	a.birthEntryScratch = a.birthEntryScratch[:0]
	clear(a.birthSeenScratch)
	a.birthSeenScratch = a.birthSeenScratch[:0]
}

func (a *agenda) clearBirthRankScratch() {
	if a == nil {
		return
	}
	clear(a.birthActivationScratch)
	a.birthActivationScratch = a.birthActivationScratch[:0]
	a.clearBirthRankRecords()
}

func (a *agenda) assignCanonicalBirthRanks(epoch uint64, activations []*activation) {
	if a == nil || epoch == 0 || len(activations) == 0 {
		return
	}
	if len(activations) == 1 {
		act := activations[0]
		a.setActivationBirth(act, epoch, 1)
		if act != nil && act.status == activationStatusPending {
			a.fixActivationOrder(act)
		}
		return
	}
	records := a.prepareActivationBirthRankRecords(activations)
	defer a.clearBirthRankRecords()
	slices.SortStableFunc(records, func(left, right activationBirthRankRecord) int {
		return compareLess(
			a.activationBirthRecordLess(left, right),
			a.activationBirthRecordLess(right, left),
		)
	})
	for i, record := range records {
		act := record.act
		activations[i] = act
		a.setActivationBirth(act, epoch, uint64(i+1))
		if act.status == activationStatusPending {
			a.fixActivationOrder(act)
		}
	}
}

func (a *agenda) assignCanonicalBirthRanksByEpoch(activations []*activation) {
	if a == nil || len(activations) == 0 {
		return
	}
	if len(activations) == 1 {
		act := activations[0]
		if act != nil && act.birthEpoch != 0 {
			a.setActivationBirth(act, act.birthEpoch, 1)
			if act.status == activationStatusPending {
				a.fixActivationOrder(act)
			}
		}
		return
	}
	slices.SortStableFunc(activations, func(left, right *activation) int {
		if left == nil || right == nil {
			return compareLess(left == nil, right == nil)
		}
		return compareLess(left.birthEpoch < right.birthEpoch, right.birthEpoch < left.birthEpoch)
	})
	start := 0
	for start < len(activations) {
		current := activations[start]
		if current == nil || current.birthEpoch == 0 {
			start++
			continue
		}
		epoch := current.birthEpoch
		end := start + 1
		for end < len(activations) {
			next := activations[end]
			if next == nil || next.birthEpoch != epoch {
				break
			}
			end++
		}
		a.assignCanonicalBirthRanks(epoch, activations[start:end])
		start = end
	}
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
			a.recycleActivationPayload(current.payload)
			current.payload = nil
			continue
		}
		stored := nextRows.add(*current)
		stored.heapIndex = 0
		a.storeActivationRef(stored)
		a.enqueueActivation(stored)
		a.linkTokenActivation(stored.token, stored)
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
	a.birthActivationScratch = nil
	a.birthRecordScratch = nil
	a.birthEntryScratch = nil
	a.birthSeenScratch = nil
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
	if token.isZero() || len(rule.conditions) == 0 {
		return candidateIdentity{}, false
	}
	// Chains covering exactly slots 0..n-1 already carry the canonical
	// commutative identity state built up during construction, so the
	// terminal identity is a finish step away.
	if n := len(rule.conditions); n < 63 {
		if row, ok := token.resolve(); ok && int(row.publicSize) == n && row.slotMask == uint64(1)<<uint(n)-1 {
			scopeHash := rule.identityScopeHash
			if scopeHash == 0 {
				scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
			}
			return candidateIdentity{
				generation: tokenRefGeneration(token),
				count:      n,
				key: candidateIdentityKey{
					scopeHash: scopeHash,
					hash:      candidateIdentityHashFinish(row.identityState, n),
				},
			}, true
		}
	}
	if len(rule.conditions) > 8 {
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
		if row.value != nil {
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
			state = candidateIdentityHashFactStep(state, i, factIDs[i], factVersions[i])
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
		*token = tokenRef{handle: tokenHandle{arena: token.handle.arena, row: row.parent}}
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
