package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type SessionOption func(*sessionConfig)

type sessionConfig struct {
	id                  SessionID
	listeners           []eventListenerRegistration
	initials            []SessionInitialFact
	globals             map[string]any
	strategy            Strategy
	eventClock          func() time.Time
	resetBeforeSnapshot bool
	output              io.Writer
	explainLog          *explainLog
	demandLimit         int
}

type SessionInitialFact struct {
	// name routes a schemaless dynamic initial fact. It is unexported so the
	// public WithInitialFacts surface accepts only templated facts (a
	// TemplateKey). Engine white-box tests seed dynamic initials with
	// newDynamicInitialFact to exercise dynamic-fact assert, reset, and
	// propagation paths.
	name        string
	TemplateKey TemplateKey
	Fields      Fields
}

// newDynamicInitialFact builds a schemaless dynamic initial fact for engine
// white-box tests. Dynamic facts are internal plumbing (query triggers,
// name-routed conditions) and are not part of the public fact model.
func newDynamicInitialFact(name string, fields Fields) SessionInitialFact {
	return SessionInitialFact{name: name, Fields: fields}
}

func validatePublicTemplateMutation(template compiledTemplate) error {
	if !template.backchainDemand {
		return nil
	}
	return &ValidationError{
		TemplateName: template.name,
		Reason:       "backchain demand template is engine-owned",
	}
}

func WithSessionID(id SessionID) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.id = id
	}
}

func WithEventListener(listener EventListener, opts ...EventListenerOption) SessionOption {
	return func(cfg *sessionConfig) {
		registration := newEventListenerRegistration(listener, opts)
		if registration.listener != nil {
			cfg.listeners = append(cfg.listeners, registration)
		}
	}
}

// WithEventClock sets the clock used to timestamp emitted events.
func WithEventClock(clock func() time.Time) SessionOption {
	return func(cfg *sessionConfig) {
		if clock != nil {
			cfg.eventClock = clock
		}
	}
}

func WithInitialFacts(initials ...SessionInitialFact) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.initials = append(cfg.initials, cloneSessionInitialFacts(initials)...)
	}
}

// WithGlobals supplies immutable per-session values for declared ruleset
// globals. Reset preserves these values; changing globals requires creating a
// new session so existing matches do not need to be re-propagated.
//
// ApplyRuleset carries current values forward by name; a next revision that
// adds a global without a default cannot be applied to a live session because
// there is no way to supply the missing value at apply time — create a new
// session with WithGlobals instead.
func WithGlobals(values map[string]any) SessionOption {
	return func(cfg *sessionConfig) {
		if len(values) == 0 {
			return
		}
		if cfg.globals == nil {
			cfg.globals = make(map[string]any, len(values))
		}
		for name, value := range values {
			cfg.globals[strings.TrimSpace(name)] = cloneSpecValue(value)
		}
	}
}

// WithStrategy selects the session's conflict-resolution strategy at
// construction. The strategy is fixed for the session's lifetime and survives
// Reset and Fork; the zero value is StrategyDepth.
func WithStrategy(strategy Strategy) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.strategy = strategy
	}
}

// WithResetBeforeSnapshot controls whether successful Reset calls populate
// ResetResult.Before. The default is false: materializing a full
// working-memory snapshot per Reset is a per-call cost most sessions never
// consume.
func WithResetBeforeSnapshot(enabled bool) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.resetBeforeSnapshot = enabled
	}
}

// WithOutputWriter sets the destination for the Gess emit action. When unset,
// emitted output is discarded. The writer is shared by all activations in the
// session; it must be safe for the caller's own concurrent use, but the engine
// serializes rule firings so a single run never writes concurrently.
func WithOutputWriter(w io.Writer) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.output = w
	}
}

// WithMaxDemandCascadeSteps bounds the number of backward-chaining demand
// requests processed by one cascade. A value <= 0 leaves cascades unbounded.
func WithMaxDemandCascadeSteps(n int) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.demandLimit = max(0, n)
	}
}

type Session struct {
	id                     SessionID
	revision               *Ruleset
	agendaDriver           sessionAgendaDriver
	forkCount              uint64
	propagation            sessionPropagationCoordinator
	factStore              sessionFactStore
	initials               []SessionInitialFact
	globalValues           []Value
	initialCount           int
	compiledInitials       []compiledSessionInitialFact
	resetBeforeSnapshot    bool
	diagnostics            sessionDiagnosticsExporter
	output                 io.Writer
	closed                 bool
	runGuard               chan struct{}
	runActive              atomic.Bool
	runActivation          atomic.Pointer[activation]
	runHaltRequested       atomic.Bool
	listenerDispatchActive atomic.Bool
	actionBindingScratch   actionContextBindingState
	actionValueScratch     []Value
	actionMatchScratch     []conditionMatch
	mutationQueueMu        sync.Mutex
	mutationQueue          []queuedMutation
	mu                     struct {
		mutate chan struct{}
		lock   chan struct{}
	}

	nextRunSequence uint64
	tms             sessionTMSStore
	backchain       sessionBackchainStore
}

type queuedMutation struct {
	ctx    context.Context
	apply  func(context.Context) (any, reteAgendaDelta, error)
	result chan queuedMutationResult
}

type queuedMutationResult struct {
	value any
	err   error
}

const (
	missingFactRowIndex     int32 = -1
	maxFactRowSequenceIndex       = int(^uint32(0) >> 1)
)

func encodeCompactFactRow(row int) int {
	if row < 0 || row > maxFactRowSequenceIndex-1 {
		return int(missingFactRowIndex)
	}
	return -row - 2
}

func decodeCompactFactRow(handle int) (int, bool) {
	if handle >= -1 {
		return 0, false
	}
	return -handle - 2, true
}

func factRowSequenceIndex(id FactID, generation Generation) (int, bool) {
	if id.IsZero() || id.Generation() != generation || id.Sequence() == 0 {
		return 0, false
	}
	maxInt := int(^uint(0) >> 1)
	if id.Sequence() > uint64(maxInt) {
		return 0, false
	}
	return int(id.Sequence() - 1), true
}

func NewSession(revision *Ruleset, opts ...SessionOption) (*Session, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}

	cfg := sessionConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if !cfg.strategy.valid() {
		return nil, &ValidationError{Reason: "invalid agenda strategy"}
	}

	diagnostics := newSessionDiagnosticsExporter(cfg, 0)
	initials := cloneSessionInitialFacts(cfg.initials)
	globalValues, err := compileSessionGlobals(revision, cfg.globals)
	if err != nil {
		return nil, err
	}

	compiledInitials, err := compileSessionInitialFacts(revision, initials)
	if err != nil {
		return nil, err
	}
	initialStorage := compiledSessionInitialStorageCounts(compiledInitials)
	state := newFactWorkspace(1, initialStorage.broadFacts)
	state.reserveCompiledInitialFactStorage(initialStorage)
	state.reserveTemplateIndexes(revision)
	state.reserveDuplicateIndexes(revision)
	if len(compiledInitials) > 0 {
		state.applyCompiledInitialFacts(compiledInitials)
	}
	rete, err := newReteRuntime(revision, globalValues)
	if err != nil {
		return nil, err
	}
	agendaDriver := newSessionAgendaDriver(cfg.strategy)
	agenda := agendaDriver.agenda
	useInitialAgenda := !eventListenerRegistrationsHaveAnySubscriptions(diagnostics.listeners) && !rete.mayEmitBackchainDemandDeltas() && rete.supportsInitialAgendaReset()
	var initialDelta reteAgendaDelta
	if useInitialAgenda {
		initialDelta, err = rete.resetGraphBetaFromWorkspaceForGenerationWithInitialAgenda(context.Background(), state, state.generation, agenda)
	} else {
		initialDelta, err = rete.resetGraphBetaFromWorkspaceForGenerationWithDelta(context.Background(), state, state.generation)
	}
	if err != nil {
		return nil, err
	}
	session := &Session{
		id:                  cfg.id,
		revision:            revision,
		agendaDriver:        agendaDriver,
		propagation:         newSessionPropagationCoordinator(rete),
		factStore:           newSessionFactStore(state),
		initials:            initials,
		globalValues:        globalValues,
		initialCount:        len(initials),
		compiledInitials:    compiledInitials,
		resetBeforeSnapshot: cfg.resetBeforeSnapshot,
		diagnostics:         diagnostics,
		output:              cfg.output,
		backchain:           newSessionBackchainStore(cfg.demandLimit),
		runGuard:            make(chan struct{}, 1),
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		nextRunSequence: 0,
	}
	// The narrow listener-free path attaches activations during propagation.
	// Other shapes retain the owned lifecycle delta until the first agenda
	// boundary, preserving deferred activation-event timing without a rematch.
	if useInitialAgenda && initialDelta.supported && len(initialDelta.updated) == 0 && len(initialDelta.demands) == 0 && len(initialDelta.resolvedDemands) == 0 && len(initialDelta.resolvedOwners) == 0 {
		session.agendaDriver.installInitialAgenda(session.agendaDriver.agenda)
	} else {
		initialDelta, err = session.completeBackchainDemandDeltaImmediate(context.Background(), initialDelta, mutationOrigin{})
		if err != nil {
			return nil, err
		}
		session.propagation.setPendingLifecycleDelta(initialDelta)
	}
	session.syncPropagationCounters()
	return session, nil
}

func cloneSessionInitialFacts(initials []SessionInitialFact) []SessionInitialFact {
	if len(initials) == 0 {
		return nil
	}

	out := make([]SessionInitialFact, len(initials))
	for i, initial := range initials {
		out[i] = SessionInitialFact{
			name:        initial.name,
			TemplateKey: initial.TemplateKey,
			Fields:      cloneFields(initial.Fields),
		}
	}
	return out
}

func (s *Session) ID() SessionID {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *Session) RulesetID() RulesetID {
	if s == nil || s.revision == nil {
		return ""
	}
	return s.revision.ID()
}

func (s *Session) Generation() Generation {
	if s == nil {
		return 0
	}
	return s.factStore.generation
}

func (s *Session) sourceGeneration() Generation {
	if s == nil {
		return 0
	}
	return s.factStore.generation
}

func (s *Session) attachPropagationCounters() *propagationCounterLedger {
	if s == nil {
		return nil
	}
	s.propagation.attachCounters()
	s.syncPropagationCounters()
	return s.propagation.counters
}

func (s *Session) propagationCounterSnapshot() propagationCounterSnapshot {
	if s == nil || s.propagation.counters == nil {
		return propagationCounterSnapshot{}
	}
	s.syncPropagationCounters()
	if s.propagation.runtime != nil && s.propagation.runtime.graphBeta != nil {
		s.propagation.counters.setGraphBetaMemoryStats(s.propagation.runtime.graphBeta.memoryStats())
	} else {
		s.propagation.counters.setGraphBetaMemoryStats(reteGraphBetaMemoryStats{})
	}
	return s.propagation.counters.snapshot()
}

func (s *Session) syncPropagationCounters() {
	if s == nil || s.agendaDriver.agenda == nil {
		return
	}
	s.agendaDriver.agenda.propagationCounters = s.propagation.counters
	if s.propagation.counters == nil {
		return
	}
	s.propagation.syncCounters()
}

func (s *Session) propagationCounterPhase() propagationCounterPhase {
	if s != nil && s.agendaDriver.isReady() {
		return propagationCounterPhaseSteadyState
	}
	return propagationCounterPhaseInitial
}

func (s *Session) removeStoredFact(id FactID) {
	if s == nil {
		return
	}
	handle, ok := s.factRowIndex(id)
	if !ok {
		return
	}
	if row, generated := decodeCompactFactRow(handle); generated {
		fact, ok := s.factStore.compactFacts.fact(row)
		if !ok || fact.id != id {
			return
		}
		moved, ok := s.factStore.compactFacts.remove(row)
		s.deleteFactRowIndex(id)
		if ok {
			s.setFactRowIndex(moved, encodeCompactFactRow(row))
		}
		return
	}
	if handle < 0 || handle >= len(s.factStore.facts) || s.factStore.facts[handle].id != id {
		return
	}
	last := len(s.factStore.facts) - 1
	if handle != last {
		moved := s.factStore.facts[last]
		s.factStore.facts[handle] = moved
		s.setFactRowIndex(moved.id, handle)
	}
	s.deleteFactRowIndex(id)
	s.factStore.facts[last] = workingFact{}
	s.factStore.facts = s.factStore.facts[:last]
}

func (s *Session) reindexStoredFactRowsFrom(start int) {
	if s == nil || start < 0 {
		return
	}
	for i := start; i < len(s.factStore.facts); i++ {
		s.setFactRowIndex(s.factStore.facts[i].id, i)
	}
}

func (s *Session) workingFactByID(id FactID) (*workingFact, bool) {
	if s == nil {
		return nil, false
	}
	if s.backchain.activeQueryProof != nil {
		if fact, ok := s.backchain.activeQueryProof.workingFactByID(id); ok {
			return fact, true
		}
	}
	index, ok := s.factRowIndex(id)
	if !ok {
		return nil, false
	}
	if row, generated := decodeCompactFactRow(index); generated {
		fact, ok := s.factStore.compactFacts.fact(row)
		if !ok || fact.id != id {
			return nil, false
		}
		return fact, true
	}
	if index < 0 || index >= len(s.factStore.facts) {
		return nil, false
	}
	fact := &s.factStore.facts[index]
	if fact.id != id {
		return nil, false
	}
	return fact, true
}

func (s *Session) factScalarValueAtSlot(id FactID, version FactVersion, slot int) (Value, bool) {
	if s == nil || slot < 0 {
		return Value{}, false
	}
	if s.backchain.activeQueryProof != nil {
		if fact, ok := s.backchain.activeQueryProof.workingFactByID(id); ok {
			if fact == nil || fact.version != version {
				return Value{}, false
			}
			return fact.compiledFieldValue("", slot, s.factStore.compactSlotStore)
		}
	}
	index, ok := s.factRowIndex(id)
	if !ok {
		return Value{}, false
	}
	if row, generated := decodeCompactFactRow(index); generated {
		return s.factStore.compactFacts.scalarValueAtSlot(row, id, version, slot, s.factStore.compactSlotStore)
	}
	if index < 0 || index >= len(s.factStore.facts) {
		return Value{}, false
	}
	fact := &s.factStore.facts[index]
	if fact.id != id || fact.version != version {
		return Value{}, false
	}
	return fact.compiledFieldValue("", slot, s.factStore.compactSlotStore)
}

func (s *Session) factRowIndex(id FactID) (int, bool) {
	if s == nil || id.IsZero() {
		return 0, false
	}
	if index, ok := factRowSequenceIndex(id, s.factStore.generation); ok && index < len(s.factStore.factsBySequence) {
		row := s.factStore.factsBySequence[index]
		if row != missingFactRowIndex {
			return int(row), true
		}
	}
	if s.factStore.factsByID == nil {
		return 0, false
	}
	index, ok := s.factStore.factsByID[id]
	return index, ok
}

func (s *Session) setFactRowIndex(id FactID, row int) {
	if s == nil || id.IsZero() || row == int(missingFactRowIndex) {
		return
	}
	if row <= maxFactRowSequenceIndex {
		if index, ok := factRowSequenceIndex(id, s.factStore.generation); ok {
			if len(s.factStore.factsBySequence) <= index {
				s.factStore.factsBySequence = growFactRowSequenceIndex(s.factStore.factsBySequence, index+1)
			}
			s.factStore.factsBySequence[index] = int32(row)
			return
		}
	}
	if s.factStore.factsByID != nil {
		s.factStore.factsByID[id] = row
	}
}

func (s *Session) deleteFactRowIndex(id FactID) {
	if s == nil || id.IsZero() {
		return
	}
	if index, ok := factRowSequenceIndex(id, s.factStore.generation); ok && index < len(s.factStore.factsBySequence) {
		s.factStore.factsBySequence[index] = missingFactRowIndex
		return
	}
	if s.factStore.factsByID != nil {
		delete(s.factStore.factsByID, id)
	}
}

// factsForTarget is an internal matcher view. Callers must hold session
// ownership; returned detached snapshots may share session-owned backing.
func (s *Session) factsForTarget(target conditionTarget) ([]FactSnapshot, bool) {
	if s == nil {
		return nil, false
	}
	s.ensureFactTargetIndexes()
	switch target.kind {
	case conditionTargetName:
		ids := s.factStore.factsByName[target.name]
		if len(ids) == 0 {
			return nil, true
		}
		out := make([]FactSnapshot, 0, len(ids))
		for _, id := range ids {
			fact, ok := s.workingFactByID(id)
			if !ok {
				continue
			}
			out = append(out, fact.detachedSnapshotForRevision(s.revision, s.factStore.compactSlotStore))
		}
		return out, true
	case conditionTargetTemplateKey:
		ids := s.factStore.factsByTemplate[target.templateKey]
		if len(ids) == 0 {
			return nil, true
		}
		out := make([]FactSnapshot, 0, len(ids))
		for _, id := range ids {
			fact, ok := s.workingFactByID(id)
			if !ok {
				continue
			}
			out = append(out, fact.detachedSnapshotForRevision(s.revision, s.factStore.compactSlotStore))
		}
		return out, true
	default:
		return nil, false
	}
}

func (s *Session) factsForTargetFieldEqual(target conditionTarget, fieldSlot int, value reteGraphAlphaRouteValue) ([]FactSnapshot, bool) {
	if s == nil {
		return nil, false
	}
	s.ensureFactTargetIndexes()
	key := newFactFieldEqualKey(target, fieldSlot, value)
	facts, cached := s.factStore.factFieldEqualIndexes[key]
	if cached {
		return facts, true
	}
	targetIDs, ok := s.factIDsForTarget(target)
	if !ok {
		return nil, false
	}
	facts = make([]FactSnapshot, 0)
	for _, id := range targetIDs {
		fact, ok := s.workingFactByID(id)
		if !ok || !workingFactMatchesFieldEqualIndex(fact, fieldSlot, value, s.factStore.compactSlotStore) {
			continue
		}
		facts = append(facts, fact.detachedSnapshotForRevision(s.revision, s.factStore.compactSlotStore))
	}
	if s.factStore.factFieldEqualIndexes == nil {
		s.factStore.factFieldEqualIndexes = make(map[factFieldEqualKey][]FactSnapshot)
	}
	s.factStore.factFieldEqualIndexes[key] = facts
	return facts, true
}

func (s *Session) recordAlphaIndexProbe(hit bool) {
	if s == nil || s.propagation.counters == nil {
		return
	}
	s.propagation.counters.recordAlphaIndexProbe(hit)
}

func (s *Session) recordAlphaIndexFallbackScan() {
	if s == nil || s.propagation.counters == nil {
		return
	}
	s.propagation.counters.recordAlphaIndexFallbackScan()
}

func (s *Session) factIDsForTarget(target conditionTarget) ([]FactID, bool) {
	switch target.kind {
	case conditionTargetName:
		return s.factStore.factsByName[target.name], true
	case conditionTargetTemplateKey:
		return s.factStore.factsByTemplate[target.templateKey], true
	default:
		return nil, false
	}
}

func (s *Session) ensureFactTargetIndexes() {
	if s == nil || !s.factStore.factTargetIndexesDirty {
		return
	}
	s.rebuildFactTargetIndexes()
}

func (s *Session) rebuildFactTargetIndexes() {
	if s == nil {
		return
	}
	if s.factStore.factsByTemplate == nil {
		s.factStore.factsByTemplate = make(map[TemplateKey][]FactID)
	} else {
		clear(s.factStore.factsByTemplate)
	}
	if s.factStore.factsByName == nil {
		s.factStore.factsByName = make(map[string][]FactID)
	} else {
		clear(s.factStore.factsByName)
	}
	for _, id := range s.factStore.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok || fact.id.IsZero() {
			continue
		}
		if templateKey := fact.templateKeyForRevision(s.revision); templateKey != "" {
			s.factStore.factsByTemplate[templateKey] = append(s.factStore.factsByTemplate[templateKey], fact.id)
		}
		if name := fact.nameForRevision(s.revision); name != "" {
			s.factStore.factsByName[name] = append(s.factStore.factsByName[name], fact.id)
		}
	}
	s.clearFactFieldEqualIndexes()
	s.factStore.factTargetIndexesDirty = false
}

func (s *Session) clearFactFieldEqualIndexes() {
	if s == nil || len(s.factStore.factFieldEqualIndexes) == 0 {
		return
	}
	clear(s.factStore.factFieldEqualIndexes)
}

func (s *Session) removeFactTargetIndexes(templateKey TemplateKey, name string, id FactID) {
	if s == nil || id.IsZero() || s.factStore.factTargetIndexesDirty {
		return
	}
	s.factStore.factsByTemplate[templateKey] = removeFactIDFromSlice(s.factStore.factsByTemplate[templateKey], id)
	if len(s.factStore.factsByTemplate[templateKey]) == 0 {
		delete(s.factStore.factsByTemplate, templateKey)
	}
	s.factStore.factsByName[name] = removeFactIDFromSlice(s.factStore.factsByName[name], id)
	if len(s.factStore.factsByName[name]) == 0 {
		delete(s.factStore.factsByName, name)
	}
}

func (s *Session) Snapshot(ctx context.Context) (Snapshot, error) {
	if s == nil || s.closed {
		return Snapshot{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if s.runGuardHeld() {
		return Snapshot{}, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return Snapshot{}, ErrConcurrencyMisuse
	}
	defer s.unlock()
	return s.snapshotLocked(), nil
}

// Fork returns an independent session with the same working state as s.
// The fork shares immutable ruleset state, but owns its agenda, fact storage,
// Rete memories, logical support, backchain support metadata, and listeners.
// Event sequence numbers continue from the parent so pre-fork event history can
// be correlated by hosts that attach a listener to the fork.
//
// Fork is idle-only. Fact storage, agenda (including refraction and pending
// value bindings), focus stack, strategy, logical support, and global values
// carry over; listeners do not — pass WithEventListener in opts to observe
// the fork, and WithStrategy to give the fork a different conflict-resolution
// strategy (pending activations are reordered under the new strategy). The
// output writer is inherited: rule emits in the fork use the same sink as the
// parent unless opts includes WithOutputWriter. Callers running parent and fork
// concurrently must provide a concurrency-safe shared writer or separate
// writers. WhatIf differs by discarding fork output unless explicitly captured.
// Rete join memories are rebuilt by re-propagating the copied facts
// rather than deep-copied, so fork cost scales with working-memory size and
// internal memory diagnostics (RuntimeDiagnostics) may differ from the parent
// until the fork processes new mutations.
func (s *Session) Fork(ctx context.Context, opts ...SessionOption) (*Session, error) {
	if s == nil || s.closed {
		return nil, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.runGuardHeld() {
		return nil, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return nil, ErrConcurrencyMisuse
	}
	defer s.unlock()

	cfg := s.forkSessionConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if !cfg.strategy.valid() {
		return nil, &ValidationError{Reason: "invalid agenda strategy"}
	}

	diagnostics := newSessionDiagnosticsExporter(cfg, s.diagnostics.nextEventSequence)
	initials := cloneSessionInitialFacts(cfg.initials)
	globalValues, err := compileSessionGlobals(s.revision, cfg.globals)
	if err != nil {
		return nil, err
	}
	compiledInitials, err := compileSessionInitialFacts(s.revision, initials)
	if err != nil {
		return nil, err
	}

	state := s.clonedFactWorkspace()
	rete, err := newReteRuntime(s.revision, globalValues)
	if err != nil {
		return nil, err
	}
	initialDelta, err := rete.resetGraphBetaFromWorkspaceForGenerationWithDelta(ctx, &state, state.generation)
	if err != nil {
		return nil, err
	}

	fork := &Session{
		id:                  cfg.id,
		revision:            s.revision,
		agendaDriver:        s.agendaDriver.cloneForFork(cfg.strategy),
		propagation:         forkSessionPropagationCoordinator(rete, &s.propagation),
		factStore:           newSessionFactStore(&state),
		initials:            initials,
		globalValues:        globalValues,
		initialCount:        len(initials),
		compiledInitials:    compiledInitials,
		resetBeforeSnapshot: cfg.resetBeforeSnapshot,
		diagnostics:         diagnostics,
		output:              cfg.output,
		backchain:           s.backchain.forkForRebuild(cfg.demandLimit),
		runGuard:            make(chan struct{}, 1),
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		nextRunSequence: s.nextRunSequence,
		tms:             s.tms.cloneForFork(),
	}
	if fork.id == "" {
		s.forkCount++
		if s.id == "" {
			fork.id = SessionID(fmt.Sprintf("fork:%d", s.forkCount))
		} else {
			fork.id = SessionID(fmt.Sprintf("%s:fork:%d", s.id, s.forkCount))
		}
	}
	fork.syncPropagationCounters()
	if len(initialDelta.resolvedDemands) > 0 || len(initialDelta.resolvedOwners) > 0 {
		if _, err := fork.resolveBackchainDemandRequestsImmediate(ctx, initialDelta.resolvedDemands, initialDelta.resolvedOwners, mutationOrigin{}); err != nil {
			return nil, err
		}
	}
	if len(initialDelta.demands) > 0 {
		forkState := fork.activeFactWorkspace()
		demandDelta, err := fork.flushBackchainDemandRequestsImmediate(ctx, &forkState, initialDelta.demands, mutationOrigin{})
		if err != nil {
			return nil, err
		}
		fork.commitFactWorkspace(forkState)
		if len(demandDelta.added) > 0 || len(demandDelta.removed) > 0 || len(demandDelta.updated) > 0 {
			fork.agendaDriver.markUnready()
		}
	}
	return fork, nil
}

func (s *Session) forkSessionConfig() sessionConfig {
	return sessionConfig{
		initials:            cloneSessionInitialFacts(s.initials),
		globals:             s.sessionGlobalValueMap(),
		strategy:            s.agendaDriver.strategy,
		resetBeforeSnapshot: s.resetBeforeSnapshot,
		output:              s.output,
		demandLimit:         s.backchain.demandLimit,
	}
}

func (s *Session) sessionGlobalValueMap() map[string]any {
	if s == nil || s.revision == nil || len(s.globalValues) == 0 || len(s.revision.globalOrder) == 0 {
		return nil
	}
	out := make(map[string]any, len(s.revision.globalOrder))
	for _, name := range s.revision.globalOrder {
		global := s.revision.globals[name]
		if global.slot < 0 || global.slot >= len(s.globalValues) {
			continue
		}
		out[name] = cloneValue(s.globalValues[global.slot])
	}
	return out
}

func (s *Session) Close() error {
	if s == nil {
		return ErrClosedSession
	}
	if s.listenerDispatchActive.Load() || s.runGuardHeld() {
		return ErrConcurrencyMisuse
	}
	if !s.lock() {
		return ErrConcurrencyMisuse
	}
	defer s.unlock()
	s.closed = true
	return nil
}

// assertByName asserts an untemplated (dynamic) fact. Dynamic facts are not a
// public concept; this is engine-internal plumbing retained for query triggers
// and white-box tests. Public callers use Assert.
func (s *Session) assertByName(ctx context.Context, name string, fields Fields) (AssertResult, error) {
	return s.insertFactWithContextAndOrigin(ctx, name, "", fields, mutationOrigin{})
}

func (s *Session) Assert(ctx context.Context, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	return s.insertFactWithContextAndOrigin(ctx, "", templateKey, fields, mutationOrigin{})
}

// AssertTemplateValues asserts a working-memory fact using values in template
// field order. It is a fact assertion API, not an output-only emission path:
// inserted facts can be matched, queried, modified, retracted, logically
// supported, returned in snapshots, and observed through fact assertion events.
func (s *Session) AssertTemplateValues(ctx context.Context, templateKey TemplateKey, values ...Value) error {
	return s.insertTemplateValuesWithContextAndOrigin(ctx, templateKey, values, mutationOrigin{})
}

func (s *Session) insertFact(name string, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	return s.insertFactWithContextAndOrigin(context.Background(), name, templateKey, fields, mutationOrigin{})
}

func (s *Session) insertFactWithContextAndOrigin(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin) (AssertResult, error) {
	if s == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AssertResult{Status: AssertValidationFailure}, err
	}
	if s.shouldQueueMutationDuringRun(origin) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(mutationCtx context.Context) (any, reteAgendaDelta, error) {
				result, agendaDelta, err := s.insertFactImmediate(mutationCtx, name, templateKey, fields, origin)
				return result, agendaDelta, err
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				if outcome.err != nil {
					if result, ok := outcome.value.(AssertResult); ok {
						return result, outcome.err
					}
					return AssertResult{}, outcome.err
				}
				if result, ok := outcome.value.(AssertResult); ok {
					return result, nil
				}
				return AssertResult{}, ErrInvalidRuleset
			case <-ctx.Done():
				return AssertResult{Status: AssertValidationFailure}, ctx.Err()
			}
		}
	}

	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return AssertResult{Status: AssertConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	result, agendaDelta, err := s.insertFactImmediate(ctx, name, templateKey, fields, origin)
	if err != nil {
		return result, err
	}
	if mutationResultNeedsReconcile(result, s.revision) {
		if origin.isZero() || !s.runGuardHeld() {
			if _, err := s.reconcileAgendaAfterMutation(ctx, agendaDelta); err != nil {
				return result, err
			}
		} else {
			if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (s *Session) insertLogicalFactWithContextAndOrigin(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin, supportingFacts []FactID) (AssertResult, error) {
	if s == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AssertResult{Status: AssertValidationFailure}, err
	}
	if _, ok := logicalSupportSourceFromOrigin(origin, s.factStore.generation); !ok {
		return AssertResult{Status: AssertValidationFailure}, ErrLogicalSupportUnavailable
	}
	if s.shouldQueueMutationDuringRun(origin) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(mutationCtx context.Context) (any, reteAgendaDelta, error) {
				result, agendaDelta, err := s.insertLogicalFactImmediate(mutationCtx, name, templateKey, fields, origin, supportingFacts)
				return result, agendaDelta, err
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				if outcome.err != nil {
					if result, ok := outcome.value.(AssertResult); ok {
						return result, outcome.err
					}
					return AssertResult{}, outcome.err
				}
				if result, ok := outcome.value.(AssertResult); ok {
					return result, nil
				}
				return AssertResult{}, ErrInvalidRuleset
			case <-ctx.Done():
				return AssertResult{Status: AssertValidationFailure}, ctx.Err()
			}
		}
	}

	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return AssertResult{Status: AssertConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	result, agendaDelta, err := s.insertLogicalFactImmediate(ctx, name, templateKey, fields, origin, supportingFacts)
	if err != nil {
		return result, err
	}
	if mutationResultNeedsReconcile(result, s.revision) {
		if origin.isZero() || !s.runGuardHeld() {
			if _, err := s.reconcileAgendaAfterMutation(ctx, agendaDelta); err != nil {
				return result, err
			}
		} else {
			if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (s *Session) insertTemplateValuesWithContextAndOrigin(ctx context.Context, templateKey TemplateKey, values []Value, origin mutationOrigin) error {
	if s == nil {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	_, template, _, inserted, agendaDelta, err := s.insertTemplateValuesImmediate(ctx, templateKey, values, origin)
	if err != nil {
		return err
	}
	if inserted && s.revision.factMayAffectRuleMatchesByTarget(template.Name(), template.Key()) {
		if origin.isZero() || !s.runGuardHeld() {
			_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
			return err
		}
		if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
			return err
		}
	}
	return nil
}

type templateValueBatch struct {
	ctx            context.Context
	session        *Session
	needsReconcile bool
	agendaDelta    reteAgendaDelta
}

func (s *Session) insertTemplateValuesBatchWithContext(ctx context.Context, fn func(*templateValueBatch) error) error {
	if s == nil {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	batch := &templateValueBatch{
		ctx:     ctx,
		session: s,
	}
	if fn != nil {
		if err := fn(batch); err != nil {
			if batch.needsReconcile {
				s.markAgendaDirty()
			}
			return err
		}
	}
	if batch.needsReconcile {
		if _, ok, err := s.applyReteAgendaDeltaInternal(ctx, batch.agendaDelta, s.shouldCollectAgendaChanges()); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: unsupported agenda delta after template value batch", ErrUnsupportedRuntime)
		}
	}
	return nil
}

func (b *templateValueBatch) insert(templateKey TemplateKey, values []Value) error {
	if b == nil || b.session == nil {
		return ErrClosedSession
	}
	if b.ctx == nil {
		b.ctx = context.Background()
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	session := b.session
	_, template, _, inserted, agendaDelta, err := session.insertTemplateValuesImmediate(b.ctx, templateKey, values, mutationOrigin{})
	if err != nil {
		return err
	}
	if inserted && session.revision.factMayAffectRuleMatchesByTarget(template.Name(), template.Key()) {
		b.agendaDelta, b.needsReconcile = accumulateReteAgendaDelta(b.agendaDelta, b.needsReconcile, agendaDelta)
	}
	return nil
}

type preparedTemplateValueInserter struct {
	template compiledTemplate
}

type preparedTemplateValueBatch struct {
	ctx            context.Context
	session        *Session
	state          factWorkspace
	needsReconcile bool
	agendaDelta    reteAgendaDelta
}

func (s *Session) prepareTemplateValueInserter(templateKey TemplateKey) (preparedTemplateValueInserter, error) {
	if s == nil || s.closed {
		return preparedTemplateValueInserter{}, ErrClosedSession
	}
	template, ok := s.revision.templateByKey(templateKey)
	if !ok {
		return preparedTemplateValueInserter{}, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}
	if !template.closed {
		return preparedTemplateValueInserter{}, &ValidationError{
			TemplateName: template.Name(),
			Reason:       "template values require a fixed template",
		}
	}
	return preparedTemplateValueInserter{template: template}, nil
}

func (s *Session) insertPreparedTemplateValuesBatchWithContext(ctx context.Context, fn func(*preparedTemplateValueBatch) error) error {
	if s == nil {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	batch := &preparedTemplateValueBatch{
		ctx:     ctx,
		session: s,
		state:   s.clonedFactWorkspace(),
	}
	batch.state.skipFactTargetIndexes = true
	if fn != nil {
		if err := fn(batch); err != nil {
			s.restoreReteAfterPropagationFailure()
			return err
		}
	}
	s.commitFactWorkspace(batch.state)
	if batch.needsReconcile {
		if _, ok, err := s.applyReteAgendaDeltaInternal(ctx, batch.agendaDelta, s.shouldCollectAgendaChanges()); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: unsupported agenda delta after prepared template value batch", ErrUnsupportedRuntime)
		}
	}
	return nil
}

func accumulateReteAgendaDelta(current reteAgendaDelta, hasCurrent bool, next reteAgendaDelta) (reteAgendaDelta, bool) {
	if !hasCurrent {
		return cloneRetainedReteAgendaDelta(next), true
	}
	return mergeReteAgendaDelta(current, next), true
}

func (b *preparedTemplateValueBatch) reserve(_ int, slots int) {
	if b == nil {
		return
	}
	b.state.reserveGeneratedFactCapacity(b.session.revision, 0, slots, 0)
}

func (p preparedTemplateValueInserter) insert2(b *preparedTemplateValueBatch, v0, v1 Value) error {
	if len(p.template.fields) != 2 {
		return &ValidationError{
			TemplateName: p.template.Name(),
			Reason:       "prepared value count does not match template field count",
		}
	}
	if b == nil || b.session == nil {
		return ErrClosedSession
	}
	if b.ctx == nil {
		b.ctx = context.Background()
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if p.supportsCompactSlots() {
		slots, slotMark := b.state.reserveGeneratedCompactFactSlots(b.session.revision, len(p.template.fields))
		if err := p.setPreparedCompactSlot(slots, 0, v0); err != nil {
			b.state.rollbackGeneratedCompactFactSlots(slotMark)
			return err
		}
		if err := p.setPreparedCompactSlot(slots, 1, v1); err != nil {
			b.state.rollbackGeneratedCompactFactSlots(slotMark)
			return err
		}
		return p.insertPreparedCompactSlots(b, slots, slotMark)
	}
	slots, slotMark := b.state.reserveGeneratedFactSlots(b.session.revision, len(p.template.fields))
	if err := p.setPreparedSlot(slots, 0, v0); err != nil {
		b.state.rollbackGeneratedFactSlots(slotMark)
		return err
	}
	if err := p.setPreparedSlot(slots, 1, v1); err != nil {
		b.state.rollbackGeneratedFactSlots(slotMark)
		return err
	}
	return p.insertPreparedSlots(b, slots, slotMark)
}

func (p preparedTemplateValueInserter) insert3(b *preparedTemplateValueBatch, v0, v1, v2 Value) error {
	if len(p.template.fields) != 3 {
		return &ValidationError{
			TemplateName: p.template.Name(),
			Reason:       "prepared value count does not match template field count",
		}
	}
	if b == nil || b.session == nil {
		return ErrClosedSession
	}
	if b.ctx == nil {
		b.ctx = context.Background()
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if p.supportsCompactSlots() {
		slots, slotMark := b.state.reserveGeneratedCompactFactSlots(b.session.revision, len(p.template.fields))
		if err := p.setPreparedCompactSlot(slots, 0, v0); err != nil {
			b.state.rollbackGeneratedCompactFactSlots(slotMark)
			return err
		}
		if err := p.setPreparedCompactSlot(slots, 1, v1); err != nil {
			b.state.rollbackGeneratedCompactFactSlots(slotMark)
			return err
		}
		if err := p.setPreparedCompactSlot(slots, 2, v2); err != nil {
			b.state.rollbackGeneratedCompactFactSlots(slotMark)
			return err
		}
		return p.insertPreparedCompactSlots(b, slots, slotMark)
	}
	slots, slotMark := b.state.reserveGeneratedFactSlots(b.session.revision, len(p.template.fields))
	if err := p.setPreparedSlot(slots, 0, v0); err != nil {
		b.state.rollbackGeneratedFactSlots(slotMark)
		return err
	}
	if err := p.setPreparedSlot(slots, 1, v1); err != nil {
		b.state.rollbackGeneratedFactSlots(slotMark)
		return err
	}
	if err := p.setPreparedSlot(slots, 2, v2); err != nil {
		b.state.rollbackGeneratedFactSlots(slotMark)
		return err
	}
	return p.insertPreparedSlots(b, slots, slotMark)
}

func (p preparedTemplateValueInserter) insertPreparedCompactSlots(b *preparedTemplateValueBatch, slots []compactFactSlot, slotMark int) error {
	session := b.session
	if err := validatePublicTemplateMutation(p.template); err != nil {
		b.state.rollbackGeneratedCompactFactSlots(slotMark)
		return err
	}
	plan, ok := session.revision.generatedFactInsertPlan(p.template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(p.template)
		plan = &compiled
	}
	fact, _, inserted, err := b.state.insertPreparedGeneratedCompactFactSlotsWithPlanUnchecked(session.revision, session.factStore.generation, plan, slots, slotMark, factTargetIndexDirty)
	if err != nil {
		b.state.rollbackGeneratedCompactFactSlots(slotMark)
		return err
	}
	if !inserted {
		return nil
	}

	if !plan.affectsRete {
		return nil
	}

	var span *propagationCounterSpan
	if session.propagation.counters != nil {
		counterSpan := session.propagation.counters.beginAssert(plan.templateKey, mutationOrigin{})
		span = &counterSpan
	}
	agendaDelta, err := session.updateReteAlphaAfterAssertGenerated(b.ctx, fact, b.state.compactSlotStore, mutationOrigin{}, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		session.restoreReteAfterPropagationFailure()
		return err
	}
	if span != nil {
		span.finish()
	}
	if plan.affectsRuleMatches {
		b.agendaDelta, b.needsReconcile = accumulateReteAgendaDelta(b.agendaDelta, b.needsReconcile, agendaDelta)
	}
	return nil
}

func (p preparedTemplateValueInserter) supportsCompactSlots() bool {
	return templateSupportsCompactGeneratedValueSlots(p.template)
}

func (p preparedTemplateValueInserter) insertPreparedSlots(b *preparedTemplateValueBatch, slots []factSlot, slotMark int) error {
	session := b.session
	fact, _, inserted, err := b.state.insertPreparedGeneratedFactSlots(session.revision, session.factStore.generation, p.template, slots, slotMark)
	if err != nil {
		b.state.rollbackGeneratedFactSlots(slotMark)
		return err
	}
	if !inserted {
		return nil
	}

	if !session.revision.factMayAffectReteByTarget(p.template.Name(), p.template.Key()) {
		return nil
	}

	var span *propagationCounterSpan
	if session.propagation.counters != nil {
		counterSpan := session.propagation.counters.beginAssert(p.template.Key(), mutationOrigin{})
		span = &counterSpan
	}
	agendaDelta, err := session.updateReteAlphaAfterAssertGenerated(b.ctx, fact, b.state.compactSlotStore, mutationOrigin{}, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		session.restoreReteAfterPropagationFailure()
		return err
	}
	if span != nil {
		span.finish()
	}
	if session.revision.factMayAffectRuleMatchesByTarget(p.template.Name(), p.template.Key()) {
		b.agendaDelta, b.needsReconcile = accumulateReteAgendaDelta(b.agendaDelta, b.needsReconcile, agendaDelta)
	}
	return nil
}

func (p preparedTemplateValueInserter) setPreparedSlot(slots []factSlot, index int, value Value) error {
	field := p.template.fields[index]
	kind := field.Kind
	var allowed []Value
	if len(p.template.fieldValidation) == len(p.template.fields) {
		validation := p.template.fieldValidation[index]
		kind = validation.kind
		allowed = validation.allowedValues
	} else {
		allowed = p.template.fieldAllowed[field.Name]
	}
	if kind != ValueAny && !isValueCompatibleWithKind(kind, value) {
		return &ValidationError{
			TemplateName: p.template.Name(),
			FieldName:    field.Name,
			Reason:       "invalid type",
		}
	}
	if len(allowed) > 0 && !valueAllowed(allowed, value) {
		return &ValidationError{
			TemplateName: p.template.Name(),
			FieldName:    field.Name,
			Reason:       "value not in allowed set",
		}
	}
	if value.Kind() == ValueList || value.Kind() == ValueMap {
		value = cloneValue(value)
	}
	slots[index].value = value
	slots[index].ok = true
	slots[index].presence = fieldPresenceExplicit
	return nil
}

func (p preparedTemplateValueInserter) setPreparedCompactSlot(slots []compactFactSlot, index int, value Value) error {
	field := p.template.fields[index]
	kind := field.Kind
	var allowed []Value
	if len(p.template.fieldValidation) == len(p.template.fields) {
		validation := p.template.fieldValidation[index]
		kind = validation.kind
		allowed = validation.allowedValues
	} else {
		allowed = p.template.fieldAllowed[field.Name]
	}
	if kind != ValueAny && !isValueCompatibleWithKind(kind, value) {
		return &ValidationError{
			TemplateName: p.template.Name(),
			FieldName:    field.Name,
			Reason:       "invalid type",
		}
	}
	if len(allowed) > 0 && !valueAllowed(allowed, value) {
		return &ValidationError{
			TemplateName: p.template.Name(),
			FieldName:    field.Name,
			Reason:       "value not in allowed set",
		}
	}
	slot, ok := compactFactSlotFromValue(value, fieldPresenceExplicit)
	if !ok {
		return &ValidationError{
			TemplateName: p.template.Name(),
			FieldName:    field.Name,
			Reason:       "compact generated value requires a scalar value",
		}
	}
	slots[index] = slot
	return nil
}

func (s *Session) insertLogicalFactImmediate(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin, supportingFacts []FactID) (AssertResult, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return AssertResult{Status: AssertClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	var replaceUndo uniqueKeyReplaceUndo
	defer s.rollbackUniqueKeyReplace(&replaceUndo)

	state := s.clonedFactWorkspace()
	fact, duplicateKey, inserted, err := state.insertFact(s.revision, s.factStore.generation, name, templateKey, fields)
	if err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	supportState := s.captureLogicalSupportState()
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: s.factStore.generation,
	}
	replaced := false
	var retractDelta reteAgendaDelta
	if !inserted {
		differs, derr := s.uniqueKeyReplaceTargetFields(templateKey, fact, fields)
		if derr != nil {
			return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, derr
		}
		if !differs {
			before := fact.snapshotForRevision(s.revision, state.compactSlotStore)
			_, err := s.addLogicalSupportForPropagationEvent(ctx, fact, supportEvent, supportingFacts)
			if err != nil {
				s.restoreLogicalSupportState(supportState)
				return AssertResult{Status: AssertValidationFailure, Fact: before}, reteAgendaDelta{}, err
			}
			if before.Support().State == FactSupportLogical {
				s.updateFactSupportState(fact)
			}
			state.replaceWorkingFact(fact)
			after := fact.snapshotForRevision(s.revision, state.compactSlotStore)
			s.commitFactWorkspace(state)
			var delta *MutationDelta
			if before.Support().State != after.Support().State {
				metadataDelta := MutationDelta{
					Kind:           MutationAssert,
					Generation:     s.factStore.generation,
					ActivationID:   origin.activationID(),
					RuleID:         origin.RuleID,
					RuleRevisionID: origin.RuleRevisionID,
					SupportBefore:  before.Support(),
					SupportAfter:   after.Support(),
					Recency:        fact.recency,
					FactID:         fact.id,
					OldVersion:     fact.version,
					NewVersion:     fact.version,
					Before:         &before,
					After:          &after,
					OldDuplicate:   duplicateKey,
					NewDuplicate:   duplicateKey,
				}
				delta = &metadataDelta
			}
			return AssertResult{
				Status:       AssertExisting,
				Fact:         after,
				DuplicateKey: duplicateKey,
				Delta:        delta,
			}, reteAgendaDelta{}, nil
		}

		// Unique-key collision with differing non-key fields: retract the old
		// fact (including any support it held) and insert a fresh logical fact
		// that receives support from the current activation. Arm the rollback
		// before the retract so a later propagation failure restores the
		// pre-existing fact.
		replaceUndo = s.armUniqueKeyReplaceUndo()
		rd, rerr := s.fullyRetractFactForReplace(ctx, fact.id, origin)
		if rerr != nil {
			return AssertResult{Status: AssertValidationFailure}, rd, rerr
		}
		retractDelta = rd
		replaced = true
		state = s.clonedFactWorkspace()
		fact, duplicateKey, inserted, err = state.insertFact(s.revision, s.factStore.generation, name, templateKey, fields)
		if err != nil {
			return AssertResult{Status: AssertValidationFailure}, retractDelta, err
		}
		if !inserted {
			return AssertResult{Status: AssertValidationFailure}, retractDelta, ErrInvalidRuleset
		}
		supportState = s.captureLogicalSupportState()
	}

	s.makeFactLogicalOnly(fact)
	if _, err := s.addLogicalSupportForPropagationEvent(ctx, fact, supportEvent, supportingFacts); err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	state.replaceWorkingFact(fact)
	s.tms.logicalSupportCounters.LogicalFactsAsserted++

	snapshot := fact.snapshotForRevision(s.revision, state.compactSlotStore)
	var span *propagationCounterSpan
	if s.propagation.counters != nil {
		counterSpan := s.propagation.counters.beginAssert(snapshot.TemplateKey(), origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssert(ctx, fact, snapshot, state.compactSlotStore, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		s.restoreLogicalSupportState(supportState)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure, Fact: snapshot}, agendaDelta, err
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(state)
	replaceUndo.disarm()
	if replaced {
		agendaDelta = coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(retractDelta, agendaDelta))
	}
	delta := MutationDelta{
		Kind:           MutationAssert,
		Generation:     s.factStore.generation,
		ActivationID:   origin.activationID(),
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		SupportAfter:   snapshot.Support(),
		Recency:        fact.recency,
		FactID:         fact.id,
		NewVersion:     fact.version,
		NewDuplicate:   duplicateKey,
		After:          &snapshot,
	}

	result := AssertResult{
		Status:       assertStatusForReplace(replaced),
		Fact:         snapshot,
		DuplicateKey: duplicateKey,
		Delta:        &delta,
	}
	s.diagnostics.nextSequence()
	if s.hasEventListenersFor(EventFactAsserted) {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.diagnostics.nextEventSequence,
			Timestamp:      s.diagnostics.now(),
			Type:           EventFactAsserted,
			Generation:     s.factStore.generation,
			Recency:        fact.recency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			ActionName:     origin.ActionName,
			ActionIndex:    origin.ActionIndex,
			FactIDs:        []FactID{fact.id},
			Delta:          &delta,
		})
	}

	return result, agendaDelta, nil
}

func (s *Session) insertFactImmediate(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin) (AssertResult, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return AssertResult{Status: AssertClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	var replaceUndo uniqueKeyReplaceUndo
	defer s.rollbackUniqueKeyReplace(&replaceUndo)

	state := s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	fact, duplicateKey, inserted, err := state.insertFact(s.revision, s.factStore.generation, name, templateKey, fields)
	if err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	replaced := false
	var retractDelta reteAgendaDelta
	if !inserted {
		differs, derr := s.uniqueKeyReplaceTargetFields(templateKey, fact, fields)
		if derr != nil {
			return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, derr
		}
		if !differs {
			before := fact.snapshotForRevision(s.revision, state.compactSlotStore)
			if s.addStatedSupportToFact(fact) {
				state.replaceWorkingFact(fact)
				after := fact.snapshotForRevision(s.revision, state.compactSlotStore)
				s.commitFactWorkspace(state)
				delta := MutationDelta{
					Kind:           MutationAssert,
					Generation:     s.factStore.generation,
					ActivationID:   origin.activationID(),
					RuleID:         origin.RuleID,
					RuleRevisionID: origin.RuleRevisionID,
					SupportBefore:  before.Support(),
					SupportAfter:   after.Support(),
					Recency:        fact.recency,
					FactID:         fact.id,
					OldVersion:     fact.version,
					NewVersion:     fact.version,
					Before:         &before,
					After:          &after,
					OldDuplicate:   duplicateKey,
					NewDuplicate:   duplicateKey,
				}
				return AssertResult{
					Status:       AssertExisting,
					Fact:         after,
					DuplicateKey: duplicateKey,
					Delta:        &delta,
				}, reteAgendaDelta{}, nil
			}
			return AssertResult{
				Status:       AssertExisting,
				Fact:         before,
				DuplicateKey: duplicateKey,
			}, reteAgendaDelta{}, nil
		}

		// Unique-key collision with differing non-key fields: retract the old
		// fact and insert a new one (with a new fact ID) in its place. Arm the
		// rollback before the retract so a later propagation failure restores
		// the pre-existing fact instead of destroying it.
		replaceUndo = s.armUniqueKeyReplaceUndo()
		rd, rerr := s.fullyRetractFactForReplace(ctx, fact.id, origin)
		if rerr != nil {
			return AssertResult{Status: AssertValidationFailure}, rd, rerr
		}
		retractDelta = rd
		replaced = true
		state = s.activeFactWorkspace()
		mark = state.markGeneratedFactInsert()
		fact, duplicateKey, inserted, err = state.insertFact(s.revision, s.factStore.generation, name, templateKey, fields)
		if err != nil {
			return AssertResult{Status: AssertValidationFailure}, retractDelta, err
		}
		if !inserted {
			return AssertResult{Status: AssertValidationFailure}, retractDelta, ErrInvalidRuleset
		}
	}

	snapshot := fact.snapshotForRevision(s.revision, state.compactSlotStore)
	var span *propagationCounterSpan
	if s.propagation.counters != nil {
		counterSpan := s.propagation.counters.beginAssert(snapshot.TemplateKey(), origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssert(ctx, fact, snapshot, state.compactSlotStore, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure}, agendaDelta, err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx, &state, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure}, mergeReteAgendaDelta(agendaDelta, resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure}, mergeReteAgendaDelta(agendaDelta, demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(state)
	replaceUndo.disarm()
	if replaced {
		agendaDelta = coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(retractDelta, agendaDelta))
	}
	delta := MutationDelta{
		Kind:           MutationAssert,
		Generation:     s.factStore.generation,
		ActivationID:   origin.activationID(),
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		SupportAfter:   snapshot.Support(),
		Recency:        fact.recency,
		FactID:         fact.id,
		NewVersion:     fact.version,
		NewDuplicate:   duplicateKey,
		After:          &snapshot,
	}

	result := AssertResult{
		Status:       assertStatusForReplace(replaced),
		Fact:         snapshot,
		DuplicateKey: duplicateKey,
		Delta:        &delta,
	}
	s.diagnostics.nextSequence()
	if s.hasEventListenersFor(EventFactAsserted) {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.diagnostics.nextEventSequence,
			Timestamp:      s.diagnostics.now(),
			Type:           EventFactAsserted,
			Generation:     s.factStore.generation,
			Recency:        fact.recency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			ActionName:     origin.ActionName,
			ActionIndex:    origin.ActionIndex,
			FactIDs:        []FactID{fact.id},
			Delta:          &delta,
		})
	}

	return result, agendaDelta, nil
}

func (s *Session) insertTemplateValuesImmediate(ctx context.Context, templateKey TemplateKey, values []Value, origin mutationOrigin) (*workingFact, compiledTemplate, DuplicateKey, bool, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return nil, compiledTemplate{}, "", false, reteAgendaDelta{}, ErrClosedSession
	}
	template, ok := s.revision.templateByKey(templateKey)
	if !ok {
		return nil, compiledTemplate{}, "", false, reteAgendaDelta{}, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}
	if err := validatePublicTemplateMutation(template); err != nil {
		return nil, compiledTemplate{}, "", false, reteAgendaDelta{}, err
	}
	state := s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	if templateSupportsCompactGeneratedValueSlots(template) {
		compactSlots, compactSlotMark := state.reserveGeneratedCompactFactSlots(s.revision, len(template.fields))
		compactSlots, err := template.buildValidatedCompactFieldSlotsFromValuesInto(compactSlots, values)
		if err != nil {
			state.rollbackGeneratedCompactFactSlots(compactSlotMark)
			return nil, compiledTemplate{}, "", false, reteAgendaDelta{}, err
		}
		fact, inserted, agendaDelta, err := s.insertPreparedTemplateCompactSlotsImmediate(ctx, state, template, compactSlots, mark, compactSlotMark, origin)
		if err != nil {
			return nil, compiledTemplate{}, "", false, agendaDelta, err
		}
		return fact, template, "", inserted, agendaDelta, nil
	}
	fieldSlots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(template.fields))
	fieldSlots, err := template.buildValidatedFieldSlotsFromValuesInto(fieldSlots, values)
	if err != nil {
		state.rollbackGeneratedFactSlots(slotMark)
		return nil, compiledTemplate{}, "", false, reteAgendaDelta{}, err
	}

	fact, duplicateKey, inserted, agendaDelta, err := s.insertPreparedTemplateSlotsImmediate(ctx, state, template, fieldSlots, mark, slotMark, origin)
	if err != nil {
		return nil, compiledTemplate{}, "", false, agendaDelta, err
	}
	return fact, template, duplicateKey, inserted, agendaDelta, nil
}

func (s *Session) insertPreparedTemplateSlotsImmediate(ctx context.Context, state factWorkspace, template compiledTemplate, fieldSlots []factSlot, mark factWorkspaceInsertMark, slotMark int, origin mutationOrigin) (*workingFact, DuplicateKey, bool, reteAgendaDelta, error) {
	plan, ok := s.revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	return s.insertPreparedTemplateSlotsWithPlanImmediate(ctx, state, plan, fieldSlots, mark, slotMark, origin)
}

func (s *Session) insertPreparedTemplateCompactSlotsImmediate(ctx context.Context, state factWorkspace, template compiledTemplate, compactSlots []compactFactSlot, mark factWorkspaceInsertMark, compactSlotMark int, origin mutationOrigin) (*workingFact, bool, reteAgendaDelta, error) {
	plan, ok := s.revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	return s.insertPreparedTemplateCompactSlotsWithPlanImmediate(ctx, state, plan, compactSlots, mark, compactSlotMark, origin)
}

func (s *Session) insertPreparedTemplateCompactSlotsWithPlanImmediate(ctx context.Context, state factWorkspace, plan *compiledGeneratedFactInsertPlan, compactSlots []compactFactSlot, mark factWorkspaceInsertMark, compactSlotMark int, origin mutationOrigin) (*workingFact, bool, reteAgendaDelta, error) {
	if !plan.valid() {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return nil, false, reteAgendaDelta{}, &ValidationError{
			Reason: "generated fact insert plan is missing",
		}
	}
	var replaceUndo uniqueKeyReplaceUndo
	defer s.rollbackUniqueKeyReplace(&replaceUndo)
	var proposed []compactFactSlot
	if plan.duplicatePolicy == DuplicateUniqueKey {
		proposed = cloneCompactFactSlots(compactSlots)
	}
	fact, _, inserted, err := state.insertPreparedGeneratedCompactFactSlotsWithPlanUnchecked(s.revision, s.factStore.generation, plan, compactSlots, compactSlotMark, factTargetIndexDirty)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return nil, false, reteAgendaDelta{}, err
	}
	replaced := false
	var retractDelta reteAgendaDelta
	if !inserted {
		if !s.uniqueKeyReplaceTargetCompactSlots(plan, fact, proposed) {
			return fact, false, reteAgendaDelta{}, nil
		}
		replaceUndo = s.armUniqueKeyReplaceUndo()
		newFact, newMark, rd, rerr := s.replaceUniqueKeyGeneratedCompactFactSlots(ctx, &state, plan, proposed, fact.id, origin)
		if rerr != nil {
			return nil, false, rd, rerr
		}
		fact, mark, retractDelta, replaced = newFact, newMark, rd, true
	}

	if !plan.affectsRete {
		s.commitFactWorkspace(state)
		replaceUndo.disarm()
		s.emitGeneratedAssertEvent(ctx, fact, origin)
		return fact, true, retractDelta, nil
	}

	var span *propagationCounterSpan
	if s.propagation.counters != nil {
		counterSpan := s.propagation.counters.beginAssert(plan.templateKey, origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, state.compactSlotStore, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return nil, false, mergeReteAgendaDelta(retractDelta, agendaDelta), err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx, &state, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return nil, false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return nil, false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(state)
	replaceUndo.disarm()
	s.emitGeneratedAssertEvent(ctx, fact, origin)
	if replaced {
		agendaDelta = coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(retractDelta, agendaDelta))
	}

	return fact, true, agendaDelta, nil
}

func (s *Session) insertPreparedTemplateSlotsWithPlanImmediate(ctx context.Context, state factWorkspace, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, mark factWorkspaceInsertMark, slotMark int, origin mutationOrigin) (*workingFact, DuplicateKey, bool, reteAgendaDelta, error) {
	if !plan.valid() {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return nil, "", false, reteAgendaDelta{}, &ValidationError{
			Reason: "generated fact insert plan is missing",
		}
	}
	var replaceUndo uniqueKeyReplaceUndo
	defer s.rollbackUniqueKeyReplace(&replaceUndo)
	var proposed []factSlot
	if plan.duplicatePolicy == DuplicateUniqueKey {
		proposed = cloneFactSlots(fieldSlots)
	}
	fact, duplicateKey, inserted, err := state.insertPreparedGeneratedFactSlotsWithPlan(s.revision, s.factStore.generation, plan, fieldSlots, slotMark)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return nil, "", false, reteAgendaDelta{}, err
	}
	replaced := false
	var retractDelta reteAgendaDelta
	if !inserted {
		if !s.uniqueKeyReplaceTargetSlots(plan, fact, proposed) {
			return fact, duplicateKey, false, reteAgendaDelta{}, nil
		}
		replaceUndo = s.armUniqueKeyReplaceUndo()
		newFact, newMark, rd, rerr := s.replaceUniqueKeyGeneratedFactSlots(ctx, &state, plan, proposed, fact.id, origin)
		if rerr != nil {
			return nil, "", false, rd, rerr
		}
		fact, mark, retractDelta, replaced = newFact, newMark, rd, true
		duplicateKey = fact.publicDuplicateKey(s.revision, state.compactSlotStore)
	}

	if !plan.affectsRete {
		s.commitFactWorkspace(state)
		replaceUndo.disarm()
		s.emitGeneratedAssertEvent(ctx, fact, origin)
		return fact, duplicateKey, true, retractDelta, nil
	}

	var span *propagationCounterSpan
	if s.propagation.counters != nil {
		counterSpan := s.propagation.counters.beginAssert(plan.templateKey, origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, state.compactSlotStore, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return nil, "", false, mergeReteAgendaDelta(retractDelta, agendaDelta), err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx, &state, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return nil, "", false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return nil, "", false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(state)
	replaceUndo.disarm()
	s.emitGeneratedAssertEvent(ctx, fact, origin)
	if replaced {
		agendaDelta = coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(retractDelta, agendaDelta))
	}

	return fact, duplicateKey, true, agendaDelta, nil
}

func (s *Session) insertRuleActionGeneratedFactSlotsImmediate(ctx context.Context, state *factWorkspace, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, mark factWorkspaceInsertMark, slotMark int, origin mutationOrigin) (bool, reteAgendaDelta, error) {
	if state == nil || !plan.valid() {
		if state != nil {
			state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		}
		return false, reteAgendaDelta{}, &ValidationError{
			Reason: "generated fact insert plan is missing",
		}
	}
	var replaceUndo uniqueKeyReplaceUndo
	defer s.rollbackUniqueKeyReplace(&replaceUndo)
	if plan.outputOnlyNoRetainEligible() {
		s.discardGeneratedOutputFactSlots(state, slotMark)
		return true, reteAgendaDelta{}, nil
	}
	// Snapshot the proposed slots before the insert: on a unique-key collision
	// the workspace insert rolls back (and clears) the reserved slot region, so
	// a replacement must reinsert from a copy taken beforehand.
	var proposed []factSlot
	if plan.duplicatePolicy == DuplicateUniqueKey {
		proposed = cloneFactSlots(fieldSlots)
	}
	fact, _, inserted, err := state.insertPreparedGeneratedFactSlotsWithPlanUnchecked(s.revision, s.factStore.generation, plan, fieldSlots, slotMark, factTargetIndexDirty)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return false, reteAgendaDelta{}, err
	}
	replaced := false
	var retractDelta reteAgendaDelta
	if !inserted {
		if !s.uniqueKeyReplaceTargetSlots(plan, fact, proposed) {
			return false, reteAgendaDelta{}, nil
		}
		replaceUndo = s.armUniqueKeyReplaceUndo()
		newFact, newMark, rd, rerr := s.replaceUniqueKeyGeneratedFactSlots(ctx, state, plan, proposed, fact.id, origin)
		if rerr != nil {
			return false, rd, rerr
		}
		fact, mark, retractDelta, replaced = newFact, newMark, rd, true
	}

	if !plan.affectsRete {
		s.commitFactWorkspace(*state)
		replaceUndo.disarm()
		s.emitGeneratedAssertEvent(ctx, fact, origin)
		return true, retractDelta, nil
	}

	var span *propagationCounterSpan
	if s.propagation.counters != nil {
		counterSpan := s.propagation.counters.beginAssert(plan.templateKey, origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, state.compactSlotStore, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(retractDelta, agendaDelta), err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx, state, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(*state)
	replaceUndo.disarm()
	s.emitGeneratedAssertEvent(ctx, fact, origin)
	if replaced {
		agendaDelta = coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(retractDelta, agendaDelta))
	}

	return true, agendaDelta, nil
}

func (s *Session) insertRuleActionGeneratedCompactFactSlotsImmediate(ctx context.Context, state *factWorkspace, plan *compiledGeneratedFactInsertPlan, compactSlots []compactFactSlot, mark factWorkspaceInsertMark, compactSlotMark int, origin mutationOrigin) (bool, reteAgendaDelta, error) {
	if state == nil || !plan.valid() {
		if state != nil {
			state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		}
		return false, reteAgendaDelta{}, &ValidationError{
			Reason: "generated fact insert plan is missing",
		}
	}
	var replaceUndo uniqueKeyReplaceUndo
	defer s.rollbackUniqueKeyReplace(&replaceUndo)
	if plan.outputOnlyNoRetainEligible() {
		s.discardGeneratedOutputCompactSlots(state, compactSlotMark)
		return true, reteAgendaDelta{}, nil
	}
	// Snapshot the proposed slots before the insert (see the broad variant).
	var proposed []compactFactSlot
	if plan.duplicatePolicy == DuplicateUniqueKey {
		proposed = cloneCompactFactSlots(compactSlots)
	}
	fact, _, inserted, err := state.insertPreparedGeneratedCompactFactSlotsWithPlanUnchecked(s.revision, s.factStore.generation, plan, compactSlots, compactSlotMark, factTargetIndexDirty)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return false, reteAgendaDelta{}, err
	}
	replaced := false
	var retractDelta reteAgendaDelta
	if !inserted {
		if !s.uniqueKeyReplaceTargetCompactSlots(plan, fact, proposed) {
			return false, reteAgendaDelta{}, nil
		}
		replaceUndo = s.armUniqueKeyReplaceUndo()
		newFact, newMark, rd, rerr := s.replaceUniqueKeyGeneratedCompactFactSlots(ctx, state, plan, proposed, fact.id, origin)
		if rerr != nil {
			return false, rd, rerr
		}
		fact, mark, retractDelta, replaced = newFact, newMark, rd, true
	}

	if !plan.affectsRete {
		s.commitFactWorkspace(*state)
		replaceUndo.disarm()
		s.emitGeneratedAssertEvent(ctx, fact, origin)
		return true, retractDelta, nil
	}

	var span *propagationCounterSpan
	if s.propagation.counters != nil {
		counterSpan := s.propagation.counters.beginAssert(plan.templateKey, origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, state.compactSlotStore, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(retractDelta, agendaDelta), err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx, state, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact, s.revision)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(mergeReteAgendaDelta(retractDelta, agendaDelta), demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(*state)
	replaceUndo.disarm()
	s.emitGeneratedAssertEvent(ctx, fact, origin)
	if replaced {
		agendaDelta = coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(retractDelta, agendaDelta))
	}

	return true, agendaDelta, nil
}

func (s *Session) discardGeneratedOutputFactSlots(state *factWorkspace, slotMark int) {
	if s == nil || state == nil {
		return
	}
	state.rollbackGeneratedFactSlots(slotMark)
	s.commitFactWorkspace(*state)
}

func (s *Session) discardGeneratedOutputCompactSlots(state *factWorkspace, compactSlotMark int) {
	if s == nil || state == nil {
		return
	}
	state.rollbackGeneratedCompactFactSlots(compactSlotMark)
	s.commitFactWorkspace(*state)
}

func (s *Session) flushBackchainDemandRequestsImmediate(ctx context.Context, state *factWorkspace, demands []backchainDemandID, origin mutationOrigin) (reteAgendaDelta, error) {
	if s != nil && s.backchain.activeQueryProof != nil && origin.queryProofID == s.backchain.activeQueryProof.id {
		return s.backchain.activeQueryProof.flushDemands(ctx, demands, origin)
	}
	if s == nil || state == nil || len(demands) == 0 {
		s.clearBackchainDemandRequestArena()
		return reteAgendaDelta{supported: true}, nil
	}
	defer s.clearBackchainDemandRequestArena()
	combined := reteAgendaDelta{supported: true}
	queue := demands
	queueOwned := false
	budget := s.backchain.activeDemandCascade
	if budget == nil {
		local := newBackchainDemandCascadeBudget(s)
		budget = &local
		s.backchain.activeDemandCascade = budget
		defer func() { s.backchain.activeDemandCascade = nil }()
	}
	for i := 0; i < len(queue); i++ {
		if err := budget.consume(); err != nil {
			return combined, err
		}
		demand, ok := s.backchainDemandRequestByID(queue[i])
		if !ok {
			combined.supported = false
			continue
		}
		template, ok := s.revision.templateByKey(demand.templateKey)
		if !ok || !template.backchainDemand {
			return combined, &ValidationError{
				TemplateName: string(demand.templateKey),
				Reason:       "unknown backchain demand template",
			}
		}
		if len(demand.slots) != len(template.fields) {
			return combined, &ValidationError{
				TemplateName: template.Name(),
				Reason:       "backchain demand slot count does not match template",
			}
		}
		slots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(demand.slots))
		copy(slots, demand.slots)
		fact, _, inserted, err := state.insertPreparedEngineGeneratedFactSlots(s.revision, s.factStore.generation, template, slots, slotMark)
		if err != nil {
			return combined, err
		}
		fact.setSupportState(FactSupportLogical)
		state.replaceWorkingFact(fact)
		if !inserted {
			s.addBackchainDemandSupport(fact, demand)
			continue
		}
		var span *propagationCounterSpan
		if s.propagation.counters != nil {
			counterSpan := s.propagation.counters.beginAssert(template.Key(), origin)
			span = &counterSpan
		}
		next, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, state.compactSlotStore, origin, span)
		if span != nil {
			span.finish()
		}
		if err != nil {
			return combined, err
		}
		next = normalizeBackchainDemandNoopDelta(next)
		s.addBackchainDemandSupport(fact, demand)
		if len(next.demands) > 0 {
			if !queueOwned {
				copied := make([]backchainDemandID, len(queue), len(queue)+len(next.demands))
				copy(copied, queue)
				queue = copied
				queueOwned = true
			}
			queue = append(queue, next.demands...)
		}
		combined = mergeReteAgendaDelta(combined, next)
	}
	if len(combined.resolvedDemands) > 0 || len(combined.resolvedOwners) > 0 {
		resolvedDelta, err := s.resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx, state, combined.resolvedDemands, combined.resolvedOwners, origin)
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, resolvedDelta)
	}
	combined.demands = nil
	combined.resolvedDemands = nil
	combined.resolvedOwners = nil
	return combined, nil
}

func (s *Session) resolveBackchainDemandRequestsInFactWorkspaceImmediate(ctx context.Context, state *factWorkspace, resolved []backchainDemandID, owners []backchainDemandOwnerKey, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || state == nil || len(resolved) == 0 && len(owners) == 0 {
		return s.resolveBackchainDemandRequestsImmediate(ctx, resolved, owners, origin)
	}
	s.swapFactWorkspace(state)
	delta, err := s.resolveBackchainDemandRequestsImmediate(ctx, resolved, owners, origin)
	s.swapFactWorkspace(state)
	return delta, err
}

func (s *Session) resolveBackchainDemandRequestsImmediate(ctx context.Context, resolved []backchainDemandID, owners []backchainDemandOwnerKey, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s != nil && s.backchain.activeQueryProof != nil {
		return combined, nil
	}
	if s == nil || len(resolved) == 0 && len(owners) == 0 {
		return combined, nil
	}
	for _, owner := range owners {
		delta, err := s.removeBackchainDemandSupportForOwner(ctx, owner, origin)
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, delta)
	}
	for _, id := range resolved {
		request, ok := s.backchainDemandRequestByID(id)
		if !ok {
			combined.supported = false
			continue
		}
		delta, err := s.removeBackchainDemandSupportForRequest(ctx, request, origin)
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, delta)
	}
	return combined, nil
}

func (s *Session) backchainDemandRequestByID(id backchainDemandID) (backchainDemandRequest, bool) {
	if s == nil || s.propagation.runtime == nil || s.propagation.runtime.graphBeta == nil {
		return backchainDemandRequest{}, false
	}
	return s.propagation.runtime.graphBeta.backchainDemandRequestByID(id)
}

func (s *Session) clearBackchainDemandRequestArena() {
	if s == nil || s.propagation.runtime == nil || s.propagation.runtime.graphBeta == nil {
		return
	}
	s.propagation.runtime.graphBeta.clearBackchainDemandRequests()
}

func (s *Session) emitGeneratedAssertEvent(ctx context.Context, fact *workingFact, origin mutationOrigin) {
	if s == nil || fact == nil {
		return
	}
	s.diagnostics.nextSequence()
	if !s.hasEventListenersFor(EventFactAsserted) {
		return
	}
	publicSnapshot := fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
	duplicateKey := fact.publicDuplicateKey(s.revision, s.factStore.compactSlotStore)
	delta := MutationDelta{
		Kind:           MutationAssert,
		Generation:     s.factStore.generation,
		ActivationID:   origin.activationID(),
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		SupportAfter:   publicSnapshot.Support(),
		Recency:        fact.recency,
		FactID:         fact.id,
		NewVersion:     fact.version,
		NewDuplicate:   duplicateKey,
		After:          &publicSnapshot,
	}
	s.emitEvent(ctx, Event{
		SessionID:      s.id,
		RulesetID:      s.revision.ID(),
		Sequence:       s.diagnostics.nextEventSequence,
		Timestamp:      s.diagnostics.now(),
		Type:           EventFactAsserted,
		Generation:     s.factStore.generation,
		Recency:        fact.recency,
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		ActivationID:   origin.activationID(),
		ActionName:     origin.ActionName,
		ActionIndex:    origin.ActionIndex,
		FactIDs:        []FactID{fact.id},
		Delta:          &delta,
	})
}

func (s *Session) Retract(ctx context.Context, id FactID) (RetractResult, error) {
	return s.retractWithContextAndOrigin(ctx, id, mutationOrigin{})
}

func (s *Session) retractWithContextAndOrigin(ctx context.Context, id FactID, origin mutationOrigin) (RetractResult, error) {
	if s == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RetractResult{}, err
	}
	if s.shouldQueueMutationDuringRun(origin) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(mutationCtx context.Context) (any, reteAgendaDelta, error) {
				result, agendaDelta, err := s.retractImmediate(mutationCtx, id, origin)
				return result, agendaDelta, err
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				if outcome.err != nil {
					if result, ok := outcome.value.(RetractResult); ok {
						return result, outcome.err
					}
					return RetractResult{}, outcome.err
				}
				if result, ok := outcome.value.(RetractResult); ok {
					return result, nil
				}
				return RetractResult{}, ErrInvalidRuleset
			case <-ctx.Done():
				return RetractResult{}, ctx.Err()
			}
		}
	}
	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return RetractResult{Status: RetractConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	result, agendaDelta, err := s.retractImmediate(ctx, id, origin)
	if err != nil {
		return result, err
	}
	if mutationResultNeedsReconcile(result, s.revision) {
		if origin.isZero() || !s.runGuardHeld() {
			if _, err := s.reconcileAgendaAfterMutation(ctx, agendaDelta); err != nil {
				return result, err
			}
		} else {
			if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (s *Session) retractImmediate(ctx context.Context, id FactID, origin mutationOrigin) (RetractResult, reteAgendaDelta, error) {
	if s.closed {
		return RetractResult{Status: RetractClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	if id.Generation() != s.factStore.generation {
		if id.Generation() != 0 && id.Generation() < s.factStore.generation {
			return RetractResult{Status: RetractStale}, reteAgendaDelta{}, ErrStaleFactID
		}
		return RetractResult{Status: RetractMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	fact, ok := s.workingFactByID(id)
	if !ok {
		return RetractResult{Status: RetractMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
	switch fact.resolvedSupportState() {
	case FactSupportLogical:
		return RetractResult{Status: RetractLogicalOnly, Fact: before}, reteAgendaDelta{}, ErrLogicalOnlyRetract
	case FactSupportStatedAndLogical:
		s.removeStatedSupportFromFact(fact)
		state := s.activeFactWorkspace()
		state.replaceWorkingFact(fact)
		s.commitFactWorkspace(state)
		after := fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
		delta := MutationDelta{
			Kind:           MutationRetract,
			Generation:     s.factStore.generation,
			ActivationID:   origin.activationID(),
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			SupportBefore:  before.Support(),
			SupportAfter:   after.Support(),
			Recency:        fact.recency,
			FactID:         fact.id,
			OldVersion:     fact.version,
			NewVersion:     fact.version,
			Before:         &before,
			After:          &after,
			OldDuplicate:   fact.publicDuplicateKey(s.revision, s.factStore.compactSlotStore),
			NewDuplicate:   fact.publicDuplicateKey(s.revision, s.factStore.compactSlotStore),
		}
		return RetractResult{Status: RetractStatedSupportRemoved, Fact: after, Delta: &delta}, reteAgendaDelta{}, nil
	}

	result, agendaDelta, err := s.removeFactImmediate(ctx, id, origin, false)
	if err != nil {
		return result, agendaDelta, err
	}
	if demandDelta, err := s.removeBackchainDemandSupportsForFact(ctx, id, origin); err != nil {
		return result, agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		return result, agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	demandState := s.activeFactWorkspace()
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &demandState, agendaDelta.demands, origin); err != nil {
		return result, agendaDelta, err
	} else {
		s.commitFactWorkspace(demandState)
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: s.factStore.generation,
	}
	cascadeDelta, err := s.removeLogicalSupportsForPropagationEventDelta(ctx, supportEvent, agendaDelta)
	if err != nil {
		return result, agendaDelta, err
	}
	return result, coalesceReteAgendaDelta(s.revision, mergeReteAgendaDelta(agendaDelta, cascadeDelta)), nil
}

func (s *Session) removeFactImmediate(ctx context.Context, id FactID, origin mutationOrigin, cascade bool) (RetractResult, reteAgendaDelta, error) {
	fact, ok := s.workingFactByID(id)
	if !ok {
		return RetractResult{Status: RetractMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
	oldVersion := fact.version
	oldDuplicate := fact.publicDuplicateKey(s.revision, s.factStore.compactSlotStore)
	factID := fact.id
	factRecency := fact.recency
	factTemplateKey := fact.templateKeyForRevision(s.revision)
	factName := fact.nameForRevision(s.revision)

	agendaDelta, err := s.updateReteAlphaAfterRetractWorkingFact(ctx, fact, origin)
	if err != nil {
		s.restoreReteAfterPropagationFailure()
		return RetractResult{Status: RetractValidationFailure, Fact: before}, agendaDelta, err
	}

	state := s.activeFactWorkspace()
	if duplicateIndex := fact.duplicateIndexForRevision(s.revision, s.factStore.compactSlotStore); !duplicateIndex.isZero() {
		state.factsByDuplicate.deleteFact(duplicateIndex, id)
	}
	if !fact.targetIndexesSkipped {
		state.removeFactTargetIndexes(factTemplateKey, factName, id)
	}
	state.insertionOrder = removeFactIDFromSlice(state.insertionOrder, id)
	state.removeStoredFact(id)
	s.commitFactWorkspace(state)
	if cascade {
		s.tms.logicalSupportCounters.LogicalFactsRetracted++
		s.tms.logicalSupportCounters.CascadeRetractions++
	}

	delta := MutationDelta{
		Kind:           MutationRetract,
		Generation:     s.factStore.generation,
		ActivationID:   origin.activationID(),
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		Recency:        factRecency,
		FactID:         factID,
		SupportBefore:  before.Support(),
		OldVersion:     oldVersion,
		OldDuplicate:   oldDuplicate,
		Before:         &before,
	}

	result := RetractResult{
		Status: RetractRemoved,
		Fact:   before,
		Delta:  &delta,
	}
	s.diagnostics.nextSequence()
	if s.hasEventListenersFor(EventFactRetracted) {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.diagnostics.nextEventSequence,
			Timestamp:      s.diagnostics.now(),
			Type:           EventFactRetracted,
			Generation:     s.factStore.generation,
			Recency:        factRecency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			ActionName:     origin.ActionName,
			ActionIndex:    origin.ActionIndex,
			FactIDs:        []FactID{factID},
			Delta:          &delta,
		})
	}

	return result, agendaDelta, nil
}

// fullyRetractFactForReplace removes an existing fact from working memory, the
// Rete network, the duplicate index, and every supporting structure so a
// replacement fact can take its place. A unique-key replacement is defined as
// "retract the old fact, assert a new one", so the old fact must disappear
// regardless of its support state (stated, logical, or stated-and-logical) —
// unlike retractImmediate, which refuses logical-only facts and only strips the
// stated half of stated-and-logical facts. The returned delta is merged but not
// coalesced; the caller merges it with the replacement fact's assert delta and
// coalesces the combined result once, so same-identity token pairs collapse to
// a single agenda update.
func (s *Session) fullyRetractFactForReplace(ctx context.Context, id FactID, origin mutationOrigin) (reteAgendaDelta, error) {
	if _, ok := s.workingFactByID(id); !ok {
		return reteAgendaDelta{}, ErrFactNotFound
	}

	// Drop logical support the old fact received so its edges do not dangle.
	s.purgeReceivedLogicalSupport(ctx, id)

	_, agendaDelta, err := s.removeFactImmediate(ctx, id, origin, false)
	if err != nil {
		return agendaDelta, err
	}
	if demandDelta, err := s.removeBackchainDemandSupportsForFact(ctx, id, origin); err != nil {
		return agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		return agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	demandState := s.activeFactWorkspace()
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &demandState, agendaDelta.demands, origin); err != nil {
		return agendaDelta, err
	} else {
		s.commitFactWorkspace(demandState)
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: s.factStore.generation,
	}
	cascadeDelta, err := s.removeLogicalSupportsForPropagationEventDelta(ctx, supportEvent, agendaDelta)
	if err != nil {
		return agendaDelta, err
	}
	return mergeReteAgendaDelta(agendaDelta, cascadeDelta), nil
}

// assertStatusForReplace reports the assert status to surface when an insert did
// or did not replace an existing unique-key fact.
func assertStatusForReplace(replaced bool) AssertStatus {
	if replaced {
		return AssertReplaced
	}
	return AssertInserted
}

// uniqueKeyReplaceUndo snapshots the mutable session state before a unique-key
// replacement. A replacement retracts (and commits) the old fact before the new
// fact's propagation, so if that propagation later fails the ordinary
// per-insert rollback would leave the pre-existing fact permanently destroyed.
// Arming captures the pre-replacement workspace and logical support so the whole
// replacement can be rolled back atomically; it is disarmed once the
// replacement has committed successfully.
type uniqueKeyReplaceUndo struct {
	workspace factWorkspace
	support   logicalSupportState
	armed     bool
}

// armUniqueKeyReplaceUndo captures the pre-replacement state. It must be called
// before the old fact is retracted. It deep-clones the workspace, so callers
// only arm it once a replacement is certain (a differing unique-key collision).
func (s *Session) armUniqueKeyReplaceUndo() uniqueKeyReplaceUndo {
	return uniqueKeyReplaceUndo{
		workspace: s.clonedFactWorkspace(),
		support:   s.captureLogicalSupportState(),
		armed:     true,
	}
}

func (u *uniqueKeyReplaceUndo) disarm() {
	if u != nil {
		u.armed = false
	}
}

// rollbackUniqueKeyReplace restores the session to its pre-replacement state. It
// is a no-op unless the undo is armed, so it is safe to defer unconditionally.
func (s *Session) rollbackUniqueKeyReplace(u *uniqueKeyReplaceUndo) {
	if u == nil || !u.armed {
		return
	}
	u.armed = false
	s.commitFactWorkspace(u.workspace)
	s.restoreLogicalSupportState(u.support)
	s.restoreReteAfterPropagationFailure()
}

// uniqueKeyReplaceTargetFields reports whether asserting fields against the given
// template would replace existing: it returns true only when the template uses
// the unique-key duplicate policy and the proposed fields differ (after template
// defaults are applied) from the existing fact's current values on any field.
// Because the duplicate index keys on the key fields alone, the two facts share
// the same key, so any difference is necessarily in a non-key field.
func (s *Session) uniqueKeyReplaceTargetFields(templateKey TemplateKey, existing *workingFact, fields Fields) (bool, error) {
	if templateKey == "" || existing == nil {
		return false, nil
	}
	template, ok := s.revision.templateByKey(templateKey)
	if !ok || template.duplicatePolicy != DuplicateUniqueKey {
		return false, nil
	}
	canonical, _, err := template.applyDefaultsAndValidate(normalizeFields(fields))
	if err != nil {
		return false, err
	}
	current := existing.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
	keyed := make(map[string]struct{}, len(template.duplicateKeyNames))
	for _, name := range template.duplicateKeyNames {
		keyed[name] = struct{}{}
	}
	for _, spec := range template.fields {
		// The collision already establishes equality on the key fields, so
		// only the non-key fields decide whether this is a replacement.
		if _, ok := keyed[spec.Name]; ok {
			continue
		}
		proposed, proposedOK := canonical[spec.Name]
		existingValue, existingOK := current.Field(spec.Name)
		if proposedOK != existingOK {
			return true, nil
		}
		if proposedOK && !proposed.Equal(existingValue) {
			return true, nil
		}
	}
	return false, nil
}

// uniqueKeyReplaceTargetSlots reports whether the proposed validated slots differ
// from the existing fact on any field. It is the slot-backed counterpart of
// uniqueKeyReplaceTargetFields for the generated/native assert paths, where the
// proposed fact is already materialized as field slots. Comparison is by value
// only, so a field that matches after defaults is not treated as a difference.
func (s *Session) uniqueKeyReplaceTargetSlots(plan *compiledGeneratedFactInsertPlan, existing *workingFact, proposed []factSlot) bool {
	if plan == nil || existing == nil || plan.duplicatePolicy != DuplicateUniqueKey {
		return false
	}
	// Read the existing fact through its snapshot so map-stored, slot-stored,
	// and compact-stored facts all compare correctly; comparing only the
	// existing fact's slot slice would treat a map-stored fact as empty and
	// spuriously report a difference.
	current := existing.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
	for i, spec := range plan.template.fields {
		// Key fields are equal by construction of the collision; only the
		// non-key fields decide whether this is a replacement.
		if slices.Contains(plan.duplicateKeySlots, i) {
			continue
		}
		proposedValue, proposedOK := Value{}, false
		if i < len(proposed) && proposed[i].ok {
			proposedValue, proposedOK = proposed[i].value, true
		}
		existingValue, existingOK := current.Field(spec.Name)
		if proposedOK != existingOK {
			return true
		}
		if proposedOK && !proposedValue.Equal(existingValue) {
			return true
		}
	}
	return false
}

// uniqueKeyReplaceTargetCompactSlots is the compact-slot counterpart of
// uniqueKeyReplaceTargetSlots.
func (s *Session) uniqueKeyReplaceTargetCompactSlots(plan *compiledGeneratedFactInsertPlan, existing *workingFact, proposed []compactFactSlot) bool {
	return s.uniqueKeyReplaceTargetSlots(plan, existing, materializeFactSlotsFromCompactSlots(proposed))
}

// replaceUniqueKeyGeneratedFactSlots retracts the existing unique-key fact oldID
// and inserts a new generated fact built from proposed on state. It refreshes
// state to the active workspace and returns the new fact, the insert mark for
// caller-side rollback, and the (uncoalesced) retract agenda delta to merge with
// the assert delta.
func (s *Session) replaceUniqueKeyGeneratedFactSlots(ctx context.Context, state *factWorkspace, plan *compiledGeneratedFactInsertPlan, proposed []factSlot, oldID FactID, origin mutationOrigin) (*workingFact, factWorkspaceInsertMark, reteAgendaDelta, error) {
	snapshot := cloneFactSlots(proposed)
	retractDelta, err := s.fullyRetractFactForReplace(ctx, oldID, origin)
	if err != nil {
		return nil, factWorkspaceInsertMark{}, retractDelta, err
	}
	*state = s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	slots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(snapshot))
	copy(slots, snapshot)
	fact, _, inserted, err := state.insertPreparedGeneratedFactSlotsWithPlanUnchecked(s.revision, s.factStore.generation, plan, slots, slotMark, factTargetIndexDirty)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return nil, mark, retractDelta, err
	}
	if !inserted {
		return nil, mark, retractDelta, ErrInvalidRuleset
	}
	return fact, mark, retractDelta, nil
}

// replaceUniqueKeyGeneratedCompactFactSlots is the compact-slot counterpart of
// replaceUniqueKeyGeneratedFactSlots.
func (s *Session) replaceUniqueKeyGeneratedCompactFactSlots(ctx context.Context, state *factWorkspace, plan *compiledGeneratedFactInsertPlan, proposed []compactFactSlot, oldID FactID, origin mutationOrigin) (*workingFact, factWorkspaceInsertMark, reteAgendaDelta, error) {
	snapshot := cloneCompactFactSlots(proposed)
	retractDelta, err := s.fullyRetractFactForReplace(ctx, oldID, origin)
	if err != nil {
		return nil, factWorkspaceInsertMark{}, retractDelta, err
	}
	*state = s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	slots, compactSlotMark := state.reserveGeneratedCompactFactSlots(s.revision, len(snapshot))
	copy(slots, snapshot)
	fact, _, inserted, err := state.insertPreparedGeneratedCompactFactSlotsWithPlanUnchecked(s.revision, s.factStore.generation, plan, slots, compactSlotMark, factTargetIndexDirty)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil, s.revision)
		return nil, mark, retractDelta, err
	}
	if !inserted {
		return nil, mark, retractDelta, ErrInvalidRuleset
	}
	return fact, mark, retractDelta, nil
}

func (s *Session) removeBackchainDemandFactImmediate(ctx context.Context, id FactID, origin mutationOrigin) (reteAgendaDelta, error) {
	fact, ok := s.workingFactByID(id)
	if !ok {
		return reteAgendaDelta{}, ErrFactNotFound
	}

	factTemplateKey := fact.templateKeyForRevision(s.revision)
	factName := fact.nameForRevision(s.revision)

	agendaDelta, err := s.updateReteAlphaAfterRetractGeneratedWorkingFact(ctx, fact, origin)
	if err != nil {
		s.restoreReteAfterPropagationFailure()
		return agendaDelta, err
	}

	state := s.activeFactWorkspace()
	if duplicateIndex := fact.duplicateIndexForRevision(s.revision, s.factStore.compactSlotStore); !duplicateIndex.isZero() {
		state.factsByDuplicate.deleteFact(duplicateIndex, id)
	}
	if !fact.targetIndexesSkipped {
		state.removeFactTargetIndexes(factTemplateKey, factName, id)
	}
	state.insertionOrder = removeFactIDFromSlice(state.insertionOrder, id)
	state.removeStoredFact(id)
	s.commitFactWorkspace(state)

	return agendaDelta, nil
}

func (s *Session) Reset(ctx context.Context) (ResetResult, error) {
	if s == nil {
		return ResetResult{Status: ResetClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ResetResult{}, err
	}
	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return ResetResult{Status: ResetConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	result, err := s.resetImmediate(ctx)
	if err != nil {
		return result, err
	}
	if s.agendaDriver.isReady() {
		return result, nil
	}
	if !s.shouldCollectAgendaChanges() {
		if ok, err := s.reconcileAgendaWithoutSnapshotAndChanges(ctx); ok || err != nil {
			return result, err
		}
	}
	if _, err := s.reconcileAgendaInternal(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Session) ApplyRuleset(ctx context.Context, next *Ruleset) (ApplyRulesetResult, error) {
	if s == nil {
		return ApplyRulesetResult{Status: ApplyRulesetClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ApplyRulesetResult{}, err
	}
	if s.shouldQueueMutationDuringRun(mutationOrigin{}) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(mutationCtx context.Context) (any, reteAgendaDelta, error) {
				result, err := s.applyRulesetImmediate(mutationCtx, next)
				return result, reteAgendaDelta{}, err
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				if outcome.err != nil {
					if result, ok := outcome.value.(ApplyRulesetResult); ok {
						return result, outcome.err
					}
					return ApplyRulesetResult{}, outcome.err
				}
				if result, ok := outcome.value.(ApplyRulesetResult); ok {
					return result, nil
				}
				return ApplyRulesetResult{}, ErrInvalidRuleset
			case <-ctx.Done():
				return ApplyRulesetResult{}, ctx.Err()
			}
		}
	}
	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return ApplyRulesetResult{Status: ApplyRulesetConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	return s.applyRulesetImmediate(ctx, next)
}

func (s *Session) resetImmediate(ctx context.Context) (ResetResult, error) {
	if s.closed {
		return ResetResult{Status: ResetClosed}, ErrClosedSession
	}

	compiledInitials, err := s.compiledResetInitials()
	if err != nil {
		return ResetResult{Status: ResetValidationFailure, Before: s.snapshotLocked()}, err
	}

	var before Snapshot
	if s.resetBeforeSnapshot {
		before = s.detachedSnapshotLocked()
	}
	initialStorage := compiledSessionInitialStorageCounts(compiledInitials)
	next := &s.factStore.resetWorkspace
	next.reset(s.factStore.generation+1, initialStorage.broadFacts)
	next.skipFactTargetIndexes = true
	next.reserveCompiledInitialFactStorage(initialStorage)
	next.reserveTemplateIndexes(s.revision)
	if len(compiledInitials) > 0 {
		next.reserveDuplicateIndexes(s.revision)
	}
	next.applyCompiledInitialFacts(compiledInitials)

	rete := s.propagation.runtime
	if rete == nil {
		var err error
		rete, err = newReteRuntime(s.revision, s.globalValues)
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
	}
	resetDemandDelta, err := rete.resetGraphBetaFromWorkspaceForGenerationWithDelta(ctx, next, next.generation)
	if err != nil {
		if s.propagation.runtime != nil {
			rollbackState := s.activeFactWorkspace()
			_, _ = s.propagation.runtime.resetGraphBetaFromWorkspaceForGenerationWithDelta(context.Background(), &rollbackState, s.factStore.generation)
		}
		return ResetResult{Status: ResetValidationFailure, Before: before}, err
	}
	if s.propagation.counters != nil {
		s.propagation.counters.recordGraphRebuild(propagationCounterPhaseInitial)
	}

	oldGeneration := s.factStore.generation
	s.agendaDriver.markUnready()
	s.resetFocusStack()
	s.clearLogicalSupports()
	s.clearBackchainDemandSupports()
	s.swapFactWorkspace(next)
	s.factStore.generation = next.generation
	s.propagation.installRuntime(rete)
	s.syncPropagationCounters()
	resetDemandDelta, err = s.completeBackchainDemandDeltaImmediate(ctx, resetDemandDelta, mutationOrigin{})
	if err != nil {
		return ResetResult{Status: ResetValidationFailure, Before: before}, err
	}
	s.propagation.clearPendingLifecycleDelta()
	s.emitAgendaEvents(ctx, s.agendaDriver.agenda.clear())
	s.propagation.setPendingLifecycleDelta(resetDemandDelta)

	result := ResetResult{
		Status:     ResetApplied,
		Generation: s.factStore.generation,
		Before:     before,
		Delta: MutationDelta{
			Kind:          MutationReset,
			Generation:    s.factStore.generation,
			OldGeneration: oldGeneration,
		},
	}
	s.diagnostics.nextSequence()
	if s.hasEventListenersFor(EventReset) {
		delta := MutationDelta{
			Kind:          MutationReset,
			Generation:    s.factStore.generation,
			OldGeneration: oldGeneration,
		}
		s.emitEvent(ctx, Event{
			SessionID:  s.id,
			RulesetID:  s.revision.ID(),
			Sequence:   s.diagnostics.nextEventSequence,
			Timestamp:  s.diagnostics.now(),
			Type:       EventReset,
			Generation: s.factStore.generation,
			FactIDs:    nil,
			Delta:      &delta,
		})
	}
	if _, err := s.applyPendingLifecycleAgendaDeltaWithEventContext(context.Background(), ctx, s.shouldCollectAgendaChanges()); err != nil {
		return ResetResult{Status: ResetValidationFailure, Before: before}, err
	}

	return result, nil
}

func (s *Session) applyRulesetImmediate(ctx context.Context, next *Ruleset) (ApplyRulesetResult, error) {
	if s.closed {
		return ApplyRulesetResult{Status: ApplyRulesetClosed}, ErrClosedSession
	}

	previousID := RulesetID("")
	if s.revision != nil {
		previousID = s.revision.ID()
	}
	if next == nil {
		return ApplyRulesetResult{
			Status:            ApplyRulesetIncompatible,
			PreviousRulesetID: previousID,
		}, ErrIncompatibleRuleset
	}

	nextID := next.ID()
	if nextID == previousID {
		if err := s.rebindUnchangedRuleset(next); err != nil {
			return ApplyRulesetResult{
				Status:            ApplyRulesetIncompatible,
				PreviousRulesetID: previousID,
				CurrentRulesetID:  nextID,
			}, err
		}
		return ApplyRulesetResult{
			Status:            ApplyRulesetUnchanged,
			PreviousRulesetID: previousID,
			CurrentRulesetID:  nextID,
		}, nil
	}
	if s.revision == nil {
		return ApplyRulesetResult{
			Status:            ApplyRulesetIncompatible,
			PreviousRulesetID: previousID,
			CurrentRulesetID:  nextID,
		}, ErrInvalidRuleset
	}

	snapshot := s.indexedSnapshotLocked()
	if err := rulesetCompatibleWithSession(s.revision, next, snapshot, s.initials); err != nil {
		return ApplyRulesetResult{
			Status:            ApplyRulesetIncompatible,
			PreviousRulesetID: previousID,
			CurrentRulesetID:  nextID,
		}, err
	}

	plan, err := classifyRulesetChanges(s.revision, next)
	if err != nil {
		return ApplyRulesetResult{
			Status:            ApplyRulesetIncompatible,
			PreviousRulesetID: previousID,
			CurrentRulesetID:  nextID,
		}, err
	}
	if !s.agendaDriver.isReady() {
		if _, ok, err := s.reconcileAgendaWithoutSnapshot(ctx); err != nil {
			return ApplyRulesetResult{}, err
		} else if !ok {
			return ApplyRulesetResult{}, fmt.Errorf("%w: ruleset apply requires a graph lifecycle agenda", ErrUnsupportedRuntime)
		}
	}

	rollbackFacts := s.clonedFactWorkspace()
	rollbackSupport := s.captureLogicalSupportState()
	restoreApplyRulesetState := func() {
		s.commitFactWorkspace(rollbackFacts)
		s.restoreLogicalSupportState(rollbackSupport)
		s.restoreReteAfterPropagationFailure()
	}
	if _, err := s.removeLogicalSupportsForRuleRevisions(ctx, plan.purgeRevisions, mutationOrigin{}); err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}

	globalValues, err := rebindSessionGlobals(s.revision, next, s.globalValues)
	if err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}
	s.rebuildFieldSlots(s.revision, next)
	rete, err := newReteRuntime(next, globalValues)
	if err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}
	phase := propagationCounterPhaseInitial
	state := s.activeFactWorkspace()
	lifecycleDelta, err := rete.resetGraphBetaFromWorkspaceForGenerationWithDelta(ctx, &state, s.factStore.generation)
	if err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}
	if s.propagation.counters != nil {
		s.propagation.counters.recordGraphRebuild(phase)
	}

	s.revision = next
	s.globalValues = globalValues
	s.propagation.installRuntime(rete)
	s.agendaDriver.ensureAgenda()
	s.syncPropagationCounters()
	lifecycleDelta, err = s.completeBackchainDemandDeltaImmediate(ctx, lifecycleDelta, mutationOrigin{})
	if err != nil {
		return ApplyRulesetResult{}, err
	}
	s.emitAgendaEvents(ctx, s.agendaDriver.agenda.purgeRuleRevisions(plan.purgeRevisions))
	lifecycleDelta.added = s.agendaDriver.agenda.filterTerminalTokenDeltasForRulesetApply(next, lifecycleDelta.added, plan.activationRevisions())
	s.propagation.clearPendingLifecycleDelta()
	s.propagation.setPendingLifecycleDelta(lifecycleDelta)
	if _, err := s.applyPendingLifecycleAgendaDelta(ctx, s.shouldCollectAgendaChanges()); err != nil {
		return ApplyRulesetResult{}, err
	}

	return ApplyRulesetResult{
		Status:                 ApplyRulesetApplied,
		PreviousRulesetID:      previousID,
		CurrentRulesetID:       nextID,
		AddedRuleRevisions:     plan.Added,
		RemovedRuleRevisions:   plan.Removed,
		ReplacedRuleRevisions:  plan.Replaced,
		UnchangedRuleRevisions: plan.Unchanged,
	}, nil
}

func (s *Session) rebindUnchangedRuleset(next *Ruleset) error {
	if s == nil || s.revision == nil || next == nil || s.propagation.runtime == nil {
		return ErrInvalidRuleset
	}
	prepared, err := s.propagation.runtime.prepareRevisionRebind(next)
	if err != nil {
		return err
	}

	// Preparation above is the only fallible phase. Retarget every owner that
	// retains compiled revision data together so no session operation can see
	// a mixture of old and new host closures.
	s.propagation.runtime.applyRevisionRebind(prepared)
	s.agendaDriver.ensureAgenda()
	s.agendaDriver.agenda.rebindRevision(next)
	s.revision = next
	return nil
}

func (s *Session) reconcileAgendaInternal(ctx context.Context) ([]agendaChange, error) {
	if changes, ok, err := s.reconcileAgendaWithoutSnapshot(ctx); ok || err != nil {
		return changes, err
	}
	return nil, fmt.Errorf("%w: agenda has no pending graph lifecycle delta", ErrUnsupportedRuntime)
}

func (s *Session) reconcileAgendaWithoutSnapshot(ctx context.Context) ([]agendaChange, bool, error) {
	return s.reconcileAgendaWithoutSnapshotInternal(ctx, true)
}

func (s *Session) reconcileAgendaWithoutSnapshotAndChanges(ctx context.Context) (bool, error) {
	_, ok, err := s.reconcileAgendaWithoutSnapshotInternal(ctx, s.shouldCollectAgendaChanges())
	return ok, err
}

func (s *Session) reconcileAgendaWithoutSnapshotInternal(ctx context.Context, collectChanges bool) ([]agendaChange, bool, error) {
	if s == nil || s.closed {
		return nil, true, ErrClosedSession
	}
	if s.revision == nil {
		return nil, true, ErrInvalidRuleset
	}
	if s.agendaDriver.agenda == nil {
		s.agendaDriver.ensureAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if s.agendaDriver.isReady() {
		return nil, true, nil
	}
	if s.propagation.runtime == nil {
		return nil, false, nil
	}
	if s.propagation.hasPendingLifecycleDelta() {
		changes, err := s.applyPendingLifecycleAgendaDelta(ctx, collectChanges)
		return changes, true, err
	}

	return nil, false, nil
}

func (s *Session) reconcileAgendaAfterMutation(ctx context.Context, delta reteAgendaDelta) ([]agendaChange, error) {
	if s.propagation.hasPendingLifecycleDelta() {
		combined := mergeReteAgendaDelta(s.propagation.pendingLifecycleDelta, delta)
		s.propagation.setPendingLifecycleDelta(coalesceReteAgendaDelta(s.revision, combined))
		return s.applyPendingLifecycleAgendaDelta(ctx, s.shouldCollectAgendaChanges())
	}
	if changes, ok, err := s.applyReteAgendaDeltaInternal(ctx, delta, s.shouldCollectAgendaChanges()); ok || err != nil {
		return changes, err
	}
	if !delta.supported && s.agendaDriver.isReady() {
		return nil, fmt.Errorf("%w: unsupported agenda delta after steady-state mutation", ErrUnsupportedRuntime)
	}
	if !s.shouldCollectAgendaChanges() && s.propagation.runtime != nil && !s.propagation.runtime.supportsIncrementalAgenda() {
		s.markAgendaDirty()
		return nil, nil
	}
	return s.reconcileAgendaInternal(ctx)
}

func (s *Session) applyPendingLifecycleAgendaDelta(ctx context.Context, collectChanges bool) ([]agendaChange, error) {
	return s.applyPendingLifecycleAgendaDeltaWithEventContext(ctx, ctx, collectChanges)
}

func (s *Session) applyPendingLifecycleAgendaDeltaWithEventContext(ctx, eventCtx context.Context, collectChanges bool) ([]agendaChange, error) {
	if s == nil || s.closed {
		return nil, ErrClosedSession
	}
	if s.revision == nil || s.propagation.runtime == nil {
		return nil, ErrInvalidRuleset
	}
	if !s.propagation.hasPendingLifecycleDelta() {
		return nil, nil
	}
	if s.agendaDriver.dirty {
		return nil, fmt.Errorf("%w: dirty agenda cannot apply graph lifecycle delta", ErrUnsupportedRuntime)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	delta := s.propagation.pendingLifecycleDelta
	if !delta.supported {
		return nil, fmt.Errorf("%w: unsupported graph lifecycle agenda delta", ErrUnsupportedRuntime)
	}
	s.agendaDriver.ensureAgenda()
	s.syncPropagationCounters()
	if len(delta.updated) != 0 {
		if err := s.agendaDriver.agenda.applyTerminalTokenUpdates(ctx, s.revision, delta.updated); err != nil {
			return nil, err
		}
	}
	var changes []agendaChange
	if collectChanges {
		var err error
		changes, err = s.agendaDriver.agenda.applyTerminalTokenDeltas(ctx, s.revision, delta.removed, delta.added)
		if err != nil {
			return nil, err
		}
	} else if err := s.applyTerminalTokenDeltasWithoutChangesAndAttach(ctx, delta.removed, delta.added); err != nil {
		return nil, err
	}
	if s.propagation.counters != nil {
		s.propagation.counters.recordAgendaDeltaApplication()
	}
	s.agendaDriver.markReady()
	s.propagation.clearPendingLifecycleDelta()
	if collectChanges {
		s.applyAutoFocus(changes)
		s.emitAgendaEvents(eventCtx, changes)
	}
	return changes, nil
}

func (s *Session) applyReteAgendaDelta(ctx context.Context, delta reteAgendaDelta) ([]agendaChange, bool, error) {
	return s.applyReteAgendaDeltaInternal(ctx, delta, true)
}

func (s *Session) applyReteAgendaDeltaInternal(ctx context.Context, delta reteAgendaDelta, collectChanges bool) ([]agendaChange, bool, error) {
	if s == nil || s.closed {
		return nil, true, ErrClosedSession
	}
	if s.revision == nil {
		return nil, true, ErrInvalidRuleset
	}
	if s.agendaDriver.agenda == nil {
		s.agendaDriver.ensureAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if !delta.supported || s.propagation.runtime == nil || !s.agendaDriver.ready || s.agendaDriver.dirty {
		if s.propagation.counters != nil && !delta.supported {
			s.propagation.counters.recordUnsupportedAgendaDelta()
		}
		return nil, false, nil
	}
	if len(delta.updated) != 0 {
		if err := s.agendaDriver.agenda.applyTerminalTokenUpdates(ctx, s.revision, delta.updated); err != nil {
			return nil, true, err
		}
	}
	var changes []agendaChange
	if collectChanges {
		var err error
		changes, err = s.agendaDriver.agenda.applyTerminalTokenDeltas(ctx, s.revision, delta.removed, delta.added)
		if err != nil {
			return nil, true, err
		}
	} else if err := s.applyTerminalTokenDeltasWithoutChangesAndAttach(ctx, delta.removed, delta.added); err != nil {
		return nil, true, err
	}
	if s.propagation.counters != nil {
		s.propagation.counters.recordAgendaDeltaApplication()
	}
	s.agendaDriver.markReady()
	if collectChanges {
		s.applyAutoFocus(changes)
		s.emitAgendaEvents(ctx, changes)
	}
	return changes, true, nil
}

func (s *Session) rebuildReteRuntimeFromWorkspace(ctx context.Context, revision *Ruleset, facts *factWorkspace, generation Generation) error {
	if s == nil || revision == nil {
		return nil
	}
	rete, err := newReteRuntime(revision, s.globalValues)
	if err != nil {
		s.propagation.installRuntime(nil)
		return err
	}
	if _, err := rete.resetGraphBetaFromWorkspaceForGenerationWithDelta(ctx, facts, generation); err != nil {
		return err
	}
	s.propagation.installRuntime(rete)
	if s.propagation.counters != nil {
		s.propagation.counters.recordGraphRebuild(s.propagationCounterPhase())
	}
	s.syncPropagationCounters()
	return nil
}

func (s *Session) restoreReteAfterPropagationFailure() {
	if s == nil || s.revision == nil {
		return
	}
	state := s.activeFactWorkspace()
	_ = s.rebuildReteRuntimeFromWorkspace(context.Background(), s.revision, &state, s.factStore.generation)
}

func (s *Session) updateReteAlphaAfterAssert(ctx context.Context, fact *workingFact, snapshot FactSnapshot, compactSlotStore *factCompactSlotStore, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if s == nil {
		return reteAgendaDelta{}, nil
	}
	if s.revision != nil && !s.revision.factMayAffectReteByTarget(snapshot.name, snapshot.templateKey) {
		return reteAgendaDelta{}, nil
	}
	if s.propagation.runtime == nil {
		state := s.activeFactWorkspace()
		return reteAgendaDelta{}, s.rebuildReteRuntimeFromWorkspace(ctx, s.revision, &state, s.factStore.generation)
	}
	if s.propagation.runtime.usesGraphBeta() {
		if s.propagation.runtime.graphBeta != nil {
			s.propagation.runtime.graphBeta.compactSlotStore = compactSlotStore
		}
		return s.propagation.runtime.insertBetaWorkingFactWithOrigin(ctx, fact, snapshot, origin, span)
	}
	return reteAgendaDelta{}, s.propagation.runtime.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterAssertGenerated(ctx context.Context, fact *workingFact, compactSlotStore *factCompactSlotStore, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if s == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	if s.revision != nil && !s.revision.factMayAffectReteByTarget(fact.nameForRevision(s.revision), fact.templateKeyForRevision(s.revision)) {
		return reteAgendaDelta{}, nil
	}
	if s.propagation.runtime == nil {
		state := s.activeFactWorkspace()
		return reteAgendaDelta{}, s.rebuildReteRuntimeFromWorkspace(ctx, s.revision, &state, s.factStore.generation)
	}
	if s.propagation.runtime.usesGraphBeta() {
		if s.propagation.runtime.graphBeta != nil {
			s.propagation.runtime.graphBeta.compactSlotStore = compactSlotStore
		}
		return s.propagation.runtime.insertBetaFactGenerated(ctx, fact, origin, span)
	}
	return reteAgendaDelta{}, s.propagation.runtime.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterRetract(ctx context.Context, fact FactSnapshot, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.propagation.runtime == nil {
		return reteAgendaDelta{}, nil
	}
	if s.propagation.runtime.usesGraphBeta() {
		return s.propagation.runtime.removeBetaFact(ctx, fact, origin, s.propagation.counters)
	}
	return reteAgendaDelta{}, s.propagation.runtime.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterRetractWorkingFact(ctx context.Context, fact *workingFact, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.propagation.runtime == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	if s.propagation.runtime.usesGraphBeta() {
		if s.propagation.runtime.graphBeta != nil {
			s.propagation.runtime.graphBeta.compactSlotStore = s.factStore.compactSlotStore
		}
		return s.propagation.runtime.removeBetaWorkingFact(ctx, fact, origin, s.propagation.counters)
	}
	return reteAgendaDelta{}, s.propagation.runtime.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterRetractGeneratedWorkingFact(ctx context.Context, fact *workingFact, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.propagation.runtime == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	if s.propagation.runtime.usesGraphBeta() {
		if s.propagation.runtime.graphBeta != nil {
			s.propagation.runtime.graphBeta.compactSlotStore = s.factStore.compactSlotStore
		}
		return s.propagation.runtime.removeBetaGeneratedWorkingFact(ctx, fact, origin, s.propagation.counters)
	}
	return reteAgendaDelta{}, s.propagation.runtime.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterModify(ctx context.Context, before FactSnapshot, beforeFact *workingFact, afterFact *workingFact, after FactSnapshot, changes []FieldChange, duplicateChanged bool, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.propagation.runtime == nil {
		return reteAgendaDelta{}, nil
	}
	if s.propagation.runtime.usesGraphBeta() {
		if s.propagation.runtime.graphBeta != nil {
			s.propagation.runtime.graphBeta.compactSlotStore = s.factStore.compactSlotStore
		}
		return s.propagation.runtime.updateBetaFact(ctx, before, beforeFact, afterFact, after, changes, duplicateChanged, origin, s.propagation.counters)
	}
	return reteAgendaDelta{}, s.propagation.runtime.unsupportedRuntimeError()
}

func (s *Session) rebuildFieldSlots(previous, revision *Ruleset) {
	if s == nil {
		return
	}
	for i := range s.factStore.facts {
		fact := &s.factStore.facts[i]
		templateKey := fact.templateKeyForRevision(previous)
		template, ok := revision.templateByKey(templateKey)
		if !ok {
			if fact.fieldsMap() == nil {
				fact.setFields(materializeFieldsFromSlots(fact.materializeFieldSlots(s.factStore.compactSlotStore), fact.fieldSpecsForRevision(previous, s.factStore.compactSlotStore)))
			}
			if fact.fieldPresenceMap() == nil {
				fact.setFieldPresence(materializePresenceFromSlots(fact.materializeFieldSlots(s.factStore.compactSlotStore), fact.fieldSpecsForRevision(previous, s.factStore.compactSlotStore)))
			}
			fact.clearFieldSlots()
			fact.compactSlots = factCompactSlotRef{}
			continue
		}
		fields := fact.fieldsMap()
		if fields == nil {
			fields = materializeFieldsFromSlots(fact.materializeFieldSlots(s.factStore.compactSlotStore), fact.fieldSpecsForRevision(previous, s.factStore.compactSlotStore))
		}
		presence := fact.fieldPresenceMap()
		if presence == nil {
			presence = materializePresenceFromSlots(fact.materializeFieldSlots(s.factStore.compactSlotStore), fact.fieldSpecsForRevision(previous, s.factStore.compactSlotStore))
		}
		fieldSlots := revision.buildFieldSlots(template, fields, presence)
		if len(fieldSlots) > 0 {
			fact.clearFields()
			fact.setFieldSlots(fieldSlots)
			fact.compactSlots = factCompactSlotRef{}
			fact.clearFieldPresence()
		} else {
			fact.clearFieldSlots()
			fact.compactSlots = factCompactSlotRef{}
			if fields != nil {
				fact.setFields(fields)
			}
			fact.setFieldPresence(cloneFieldPresence(presence))
		}
	}
}

type rulesetChangePlan struct {
	Added     []RuleRevisionSummary
	Removed   []RuleRevisionSummary
	Replaced  []RuleReplacement
	Unchanged []RuleRevisionSummary

	purgeRevisions map[RuleRevisionID]struct{}
}

func (p rulesetChangePlan) activationRevisions() map[RuleRevisionID]struct{} {
	count := len(p.Added) + len(p.Replaced)
	if count == 0 {
		return nil
	}
	out := make(map[RuleRevisionID]struct{}, count)
	for _, rule := range p.Added {
		out[rule.RevisionID] = struct{}{}
	}
	for _, replacement := range p.Replaced {
		out[replacement.NewRevisionID] = struct{}{}
	}
	return out
}

func (a *agenda) filterTerminalTokenDeltasForRulesetApply(revision *Ruleset, deltas []reteTerminalTokenDelta, activateRevisions map[RuleRevisionID]struct{}) []reteTerminalTokenDelta {
	if len(deltas) == 0 {
		return nil
	}
	out := deltas[:0]
	for _, delta := range deltas {
		if _, ok := activateRevisions[delta.ruleRevisionID]; ok {
			out = append(out, delta)
			continue
		}
		if a != nil && revision != nil {
			rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
			if ok {
				identity := candidateIdentityForTerminalTokenDelta(revision, delta)
				if _, _, exists := a.activationForTerminalTokenIdentity(rule, delta.token, identity); exists {
					out = append(out, delta)
				}
			}
		}
	}
	clear(deltas[len(out):])
	return out
}

func (a *agenda) filterRuleMatchResultsForRulesetApply(results []ruleMatchResult, activateRevisions map[RuleRevisionID]struct{}) []ruleMatchResult {
	if len(results) == 0 {
		return nil
	}
	out := results[:0]
	for _, result := range results {
		if _, ok := activateRevisions[result.ruleRevisionID]; ok {
			out = append(out, result)
			continue
		}
		if a == nil {
			continue
		}
		keep := false
		for _, candidate := range result.candidates {
			if _, _, ok := a.activationForCandidate(candidate); ok {
				keep = true
				break
			}
		}
		if keep {
			out = append(out, result)
		}
	}
	clear(results[len(out):])
	return out
}

func classifyRulesetChanges(current, next *Ruleset) (rulesetChangePlan, error) {
	plan := rulesetChangePlan{
		purgeRevisions: make(map[RuleRevisionID]struct{}),
	}
	if current == nil || next == nil {
		return plan, ErrInvalidRuleset
	}

	currentRules := current.Rules()
	nextRules := next.Rules()

	currentByID := make(map[RuleID]Rule, len(currentRules))
	for _, rule := range currentRules {
		currentByID[rule.ID()] = rule
	}
	nextByID := make(map[RuleID]Rule, len(nextRules))
	for _, rule := range nextRules {
		nextByID[rule.ID()] = rule
	}

	for _, rule := range nextRules {
		currentRule, ok := currentByID[rule.ID()]
		if !ok {
			plan.Added = append(plan.Added, ruleRevisionSummaryForRule(rule))
			continue
		}
		if currentRule.RevisionID() == rule.RevisionID() {
			plan.Unchanged = append(plan.Unchanged, ruleRevisionSummaryForRule(rule))
			continue
		}
		plan.Replaced = append(plan.Replaced, RuleReplacement{
			RuleID:        rule.ID(),
			OldRevisionID: currentRule.RevisionID(),
			NewRevisionID: rule.RevisionID(),
		})
		plan.purgeRevisions[currentRule.RevisionID()] = struct{}{}
	}

	for _, rule := range currentRules {
		if _, ok := nextByID[rule.ID()]; ok {
			continue
		}
		plan.Removed = append(plan.Removed, ruleRevisionSummaryForRule(rule))
		plan.purgeRevisions[rule.RevisionID()] = struct{}{}
	}

	return plan, nil
}

func ruleRevisionSummaryForRule(rule Rule) RuleRevisionSummary {
	return RuleRevisionSummary{
		RuleID:     rule.ID(),
		RevisionID: rule.RevisionID(),
	}
}

func rulesetCompatibleWithSession(current, next *Ruleset, snapshot Snapshot, initials []SessionInitialFact) error {
	if current == nil || next == nil {
		return ErrIncompatibleRuleset
	}

	for _, fact := range snapshot.Facts() {
		templateKey := fact.TemplateKey()
		if templateKey == "" {
			continue
		}
		currentTemplate, ok := current.templateByKey(templateKey)
		if !ok {
			return ErrIncompatibleRuleset
		}
		nextTemplate, ok := next.templateByKey(templateKey)
		if !ok {
			return ErrIncompatibleRuleset
		}
		if !templatesCompatible(currentTemplate, nextTemplate) {
			return ErrIncompatibleRuleset
		}
	}

	for _, initial := range initials {
		if initial.TemplateKey == "" {
			continue
		}
		nextTemplate, ok := next.templateByKey(initial.TemplateKey)
		if !ok {
			return ErrIncompatibleRuleset
		}
		if _, _, err := nextTemplate.applyDefaultsAndValidate(initial.Fields); err != nil {
			return ErrIncompatibleRuleset
		}
	}

	return nil
}

func templatesCompatible(left, right compiledTemplate) bool {
	leftSpec := left.spec()
	rightSpec := right.spec()
	leftSpec.Source = SourceSpan{}
	rightSpec.Source = SourceSpan{}
	return reflect.DeepEqual(leftSpec, rightSpec)
}

func (s *Session) emitAgendaEvents(ctx context.Context, changes []agendaChange) {
	if s == nil || len(changes) == 0 {
		return
	}
	rulesetID := RulesetID("")
	if s.revision != nil {
		rulesetID = s.revision.ID()
	}
	for _, change := range changes {
		eventType := EventRuleActivated
		if change.kind == agendaChangeDeactivated {
			eventType = EventRuleDeactivated
		}
		s.diagnostics.nextSequence()
		if !s.hasEventListenersFor(eventType) {
			continue
		}
		ruleID := RuleID("")
		source := SourceSpan{}
		if s.revision != nil {
			if rule, ok := s.revision.rulesByRevisionID[change.activation.ruleRevisionID]; ok {
				ruleID = rule.id
				source = rule.source
			}
		}
		s.emitEvent(ctx, change.eventWithRuleID(s.id, rulesetID, ruleID, source, s.diagnostics.nextEventSequence, s.diagnostics.now()))
	}
}

func (s *Session) Modify(ctx context.Context, id FactID, patch FactPatch) (ModifyResult, error) {
	return s.modifyWithContextAndOrigin(ctx, id, patch, mutationOrigin{})
}

func (s *Session) modifyWithContextAndOrigin(ctx context.Context, id FactID, patch FactPatch, origin mutationOrigin) (ModifyResult, error) {
	if s == nil {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ModifyResult{}, err
	}
	if s.shouldQueueMutationDuringRun(origin) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(mutationCtx context.Context) (any, reteAgendaDelta, error) {
				result, agendaDelta, err := s.modifyImmediate(mutationCtx, id, patch, origin)
				return result, agendaDelta, err
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				if outcome.err != nil {
					if result, ok := outcome.value.(ModifyResult); ok {
						return result, outcome.err
					}
					return ModifyResult{}, outcome.err
				}
				if result, ok := outcome.value.(ModifyResult); ok {
					return result, nil
				}
				return ModifyResult{}, ErrInvalidRuleset
			case <-ctx.Done():
				return ModifyResult{}, ctx.Err()
			}
		}
	}
	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return ModifyResult{Status: ModifyConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	result, agendaDelta, err := s.modifyImmediate(ctx, id, patch, origin)
	if err != nil {
		return result, err
	}
	if mutationResultNeedsReconcile(result, s.revision) {
		if origin.isZero() || !s.runGuardHeld() {
			if _, err := s.reconcileAgendaAfterMutation(ctx, agendaDelta); err != nil {
				return result, err
			}
		} else {
			if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (s *Session) modifyImmediate(ctx context.Context, id FactID, patch FactPatch, origin mutationOrigin) (ModifyResult, reteAgendaDelta, error) {
	if s.closed {
		return ModifyResult{Status: ModifyClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	if id.Generation() != s.factStore.generation {
		if id.Generation() != 0 && id.Generation() < s.factStore.generation {
			return ModifyResult{Status: ModifyStale}, reteAgendaDelta{}, ErrStaleFactID
		}
		return ModifyResult{Status: ModifyMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	state := s.activeFactWorkspace()
	fact, ok := state.workingFactByID(id)
	if !ok {
		return ModifyResult{Status: ModifyMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision, state.compactSlotStore)
	if s.factHasLogicalSupport(id) {
		return ModifyResult{Status: ModifyLogicalSupport, Fact: before}, reteAgendaDelta{}, ErrLogicalFactModify
	}
	template, templateExists := fact.templateForRevision(s.revision)

	var beforeFields Fields
	var beforePresence map[string]FieldPresence
	var proposedFields Fields
	var proposedPresence map[string]FieldPresence
	var proposedFieldSlots []factSlot
	var fieldChanges []FieldChange
	var noChange bool
	var err error
	if templateExists && s.revision.usesFieldSlots(template) && fact.fieldSlotCount(state.compactSlotStore) > 0 {
		proposedFieldSlots, fieldChanges, noChange, err = template.applyPatchToFieldSlots(fact.materializeFieldSlots(state.compactSlotStore), patch)
		if err != nil {
			return ModifyResult{Status: ModifyValidationFailure, Fact: before}, reteAgendaDelta{}, err
		}
	} else {
		beforeFields = before.Fields()
		beforePresence = before.FieldPresenceMap()
		proposedFields = cloneFields(beforeFields)
		proposedPresence = cloneFieldPresence(beforePresence)
		for _, field := range patch.Unset {
			delete(proposedFields, field)
			delete(proposedPresence, field)
		}
		for field, value := range patch.Set {
			proposedFields = setField(proposedFields, field, value)
			if proposedPresence == nil {
				proposedPresence = make(map[string]FieldPresence)
			}
			proposedPresence[field] = FieldPresenceExplicit
		}

		if templateExists {
			proposedFields, proposedPresence, err = template.applyDefaultsAndValidate(proposedFields)
			if err != nil {
				return ModifyResult{Status: ModifyValidationFailure, Fact: before}, reteAgendaDelta{}, err
			}
		}
		proposedFieldSlots = s.revision.buildFieldSlots(template, proposedFields, proposedPresence)
		fieldChanges = changedFields(beforeFields, beforePresence, proposedFields, proposedPresence)
		noChange = len(fieldChanges) == 0
	}

	duplicatePolicy := template.duplicatePolicy
	factName := fact.nameForRevision(s.revision)
	newDupIndex := makeDuplicateIndexForValidatedFact(factName, template, proposedFields, proposedFieldSlots)
	oldDuplicate := fact.publicDuplicateKey(s.revision, state.compactSlotStore)

	if duplicatePolicy != DuplicateAllow {
		if newDupIndex.kind == duplicateIndexStructural {
			duplicate := false
			state.factsByDuplicate.forEachStructuralFactID(newDupIndex, func(existingID FactID) bool {
				if existingID == fact.id {
					return true
				}
				existing, ok := state.workingFactByID(existingID)
				if ok && workingFactStructuralDuplicateSlotsEqual(template, proposedFieldSlots, existing, state.compactSlotStore) {
					duplicate = true
					return false
				}
				return true
			})
			if duplicate {
				return ModifyResult{Status: ModifyDuplicate, Fact: before}, reteAgendaDelta{}, ErrDuplicateFact
			}
		} else if existingID, ok := state.duplicateFactID(s.revision, newDupIndex); ok && existingID != fact.id {
			return ModifyResult{Status: ModifyDuplicate, Fact: before}, reteAgendaDelta{}, ErrDuplicateFact
		}
	}

	if noChange {
		return ModifyResult{Status: ModifyNoOp, Fact: before}, reteAgendaDelta{}, nil
	}

	currentDupIndex := fact.duplicateIndexForRevision(s.revision, state.compactSlotStore)
	modifyMark := state.markFactModify(fact, currentDupIndex, newDupIndex, duplicatePolicy != DuplicateAllow && currentDupIndex != newDupIndex)
	state.recency++

	if duplicatePolicy != DuplicateAllow && currentDupIndex != newDupIndex {
		if !currentDupIndex.isZero() {
			state.factsByDuplicate.deleteFact(currentDupIndex, fact.id)
		}
		if !newDupIndex.isZero() {
			state.factsByDuplicate.set(newDupIndex, fact.id)
		}
	}

	oldVersion := fact.version
	newDuplicate := newDupIndex.publicKeyForTemplate(factName, template)
	if newDupIndex.kind == duplicateIndexStructural {
		newDuplicate = makeDuplicateKeyForTemplateWithSlots(factName, template, proposedFields, proposedFieldSlots)
	}
	fact.version++
	fact.recency = state.recency
	if len(proposedFieldSlots) > 0 {
		if _, generated := decodeCompactFactRow(modifyMark.factIndex); generated && templateSupportsCompactGeneratedSlots(template) {
			compactRef, ok := state.storeCompactFactSlots(proposedFieldSlots)
			if !ok {
				state.rollbackFactModify(modifyMark)
				return ModifyResult{Status: ModifyValidationFailure, Fact: before}, reteAgendaDelta{}, &ValidationError{TemplateName: template.Name(), Reason: "generated compact fact slot conversion failed"}
			}
			fact.clearFields()
			fact.clearFieldSlots()
			fact.compactSlots = compactRef
			fact.clearFieldPresence()
		} else {
			fact.clearFields()
			fact.setFieldSlots(proposedFieldSlots)
			fact.compactSlots = factCompactSlotRef{}
			fact.clearFieldPresence()
		}
	} else {
		fact.setFields(proposedFields)
		fact.clearFieldSlots()
		fact.compactSlots = factCompactSlotRef{}
		fact.setFieldPresence(proposedPresence)
	}
	state.replaceWorkingFact(fact)
	after := fact.snapshotForRevision(s.revision, state.compactSlotStore)
	duplicateChanged := oldDuplicate != newDuplicate
	agendaDelta, err := s.updateReteAlphaAfterModify(ctx, before, &modifyMark.fact, fact, after, fieldChanges, duplicateChanged, origin)
	if err != nil {
		state.rollbackFactModify(modifyMark)
		s.commitFactWorkspace(state)
		s.restoreReteAfterPropagationFailure()
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	}
	s.commitFactWorkspace(state)
	if demandDelta, err := s.removeBackchainDemandSupportsForFactVersion(ctx, before.ID(), before.Version(), origin); err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, resolvedDelta)
	}
	demandState := s.activeFactWorkspace()
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &demandState, agendaDelta.demands, origin); err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	} else {
		s.commitFactWorkspace(demandState)
		agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, demandDelta)
	}
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: after.Generation(),
	}
	cascadeDelta, err := s.removeLogicalSupportsForPropagationEventDelta(ctx, supportEvent, agendaDelta)
	if err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	}
	agendaDelta = mergeReteAgendaDeltaIfNeeded(agendaDelta, cascadeDelta)
	delta := MutationDelta{
		Kind:           MutationModify,
		Generation:     s.factStore.generation,
		ActivationID:   origin.activationID(),
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		Recency:        fact.recency,
		FactID:         fact.id,
		SupportBefore:  before.Support(),
		SupportAfter:   after.Support(),
		OldVersion:     oldVersion,
		NewVersion:     fact.version,
		Before:         &before,
		After:          &after,
		OldDuplicate:   oldDuplicate,
		NewDuplicate:   newDuplicate,
		ChangedFields:  fieldChanges,
	}
	result := ModifyResult{
		Status: ModifyChanged,
		Fact:   after,
		Delta:  &delta,
	}
	s.diagnostics.nextSequence()
	if s.hasEventListenersFor(EventFactModified) {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.diagnostics.nextEventSequence,
			Timestamp:      s.diagnostics.now(),
			Type:           EventFactModified,
			Generation:     s.factStore.generation,
			Recency:        fact.recency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			ActionName:     origin.ActionName,
			ActionIndex:    origin.ActionIndex,
			FactIDs:        []FactID{fact.id},
			Delta:          &delta,
		})
	}

	return result, agendaDelta, nil
}

func setField(fields Fields, field string, value Value) Fields {
	if fields == nil {
		fields = make(Fields)
	}
	fields[field] = cloneValue(value)
	return fields
}

func fieldsAndPresenceEqual(leftFields Fields, leftPresence map[string]FieldPresence, rightFields Fields, rightPresence map[string]FieldPresence) bool {
	if len(leftFields) != len(rightFields) {
		return false
	}
	for key, left := range leftFields {
		right, ok := rightFields[key]
		if !ok || !left.Equal(right) {
			return false
		}
	}
	for key, right := range rightFields {
		left, ok := leftFields[key]
		if !ok || !left.Equal(right) {
			return false
		}
	}

	if len(leftPresence) != len(rightPresence) {
		return false
	}
	for key, left := range leftPresence {
		right, ok := rightPresence[key]
		if !ok || left != right {
			return false
		}
	}
	for key, right := range rightPresence {
		left, ok := leftPresence[key]
		if !ok || left != right {
			return false
		}
	}
	return true
}

func (t compiledTemplate) applyPatchToFieldSlots(current []factSlot, patch FactPatch) ([]factSlot, []FieldChange, bool, error) {
	if !t.closed || len(t.fields) == 0 || len(current) == 0 {
		return nil, nil, false, ErrInvalidRuleset
	}
	proposed := copyFactSlots(current)
	if len(proposed) < len(t.fields) {
		next := make([]factSlot, len(t.fields))
		copy(next, proposed)
		proposed = next
	}

	for _, fieldName := range patch.Unset {
		if _, set := patch.Set[fieldName]; set {
			continue
		}
		slot, ok := t.fieldSlot(fieldName)
		if !ok || slot < 0 || slot >= len(t.fields) {
			continue
		}
		if err := t.clearFieldSlot(proposed, slot); err != nil {
			return nil, nil, false, err
		}
	}
	for fieldName, value := range patch.Set {
		slot, ok := t.fieldSlot(fieldName)
		if !ok || slot < 0 || slot >= len(t.fields) {
			return nil, nil, false, &ValidationError{TemplateName: t.name, FieldName: fieldName, Reason: "unknown field"}
		}
		if err := t.setFieldSlot(proposed, slot, value); err != nil {
			return nil, nil, false, err
		}
	}

	changes := changedFieldSlots(t, current, proposed)
	return proposed, changes, len(changes) == 0, nil
}

func (t compiledTemplate) clearFieldSlot(slots []factSlot, slot int) error {
	validation, hasValidation := t.fieldValidationForSlot(slot)
	field := t.fields[slot]
	if hasValidation && validation.hasDefault {
		slots[slot] = factSlot{
			value:    cloneValue(validation.defaultValue),
			presence: fieldPresenceDefault,
			ok:       true,
		}
		return nil
	}
	if !hasValidation {
		if defaultValue, hasDefault := t.fieldDefaults[field.Name]; hasDefault {
			slots[slot] = factSlot{
				value:    cloneValue(defaultValue),
				presence: fieldPresenceDefault,
				ok:       true,
			}
			return nil
		}
	}
	if hasValidation && validation.required || !hasValidation && field.Required {
		return &ValidationError{TemplateName: t.name, FieldName: field.Name, Reason: "required field is missing"}
	}
	slots[slot] = factSlot{presence: fieldPresenceOmitted}
	return nil
}

func (t compiledTemplate) setFieldSlot(slots []factSlot, slot int, value Value) error {
	validation, hasValidation := t.fieldValidationForSlot(slot)
	field := t.fields[slot]
	kind := field.Kind
	var allowed []Value
	if hasValidation {
		kind = validation.kind
		allowed = validation.allowedValues
	} else {
		allowed = t.fieldAllowed[field.Name]
	}
	if kind != ValueAny && !isValueCompatibleWithKind(kind, value) {
		return &ValidationError{TemplateName: t.name, FieldName: field.Name, Reason: "invalid type"}
	}
	if len(allowed) > 0 && !valueAllowed(allowed, value) {
		return &ValidationError{TemplateName: t.name, FieldName: field.Name, Reason: "value not in allowed set"}
	}
	if slots[slot].ok && slots[slot].value.Equal(value) {
		return nil
	}
	slots[slot] = factSlot{
		value:    cloneValue(value),
		presence: fieldPresenceExplicit,
		ok:       true,
	}
	return nil
}

func (t compiledTemplate) fieldValidationForSlot(slot int) (fieldValidationSpec, bool) {
	if slot < 0 || len(t.fieldValidation) != len(t.fields) || slot >= len(t.fieldValidation) {
		return fieldValidationSpec{}, false
	}
	return t.fieldValidation[slot], true
}

func changedFieldSlots(template compiledTemplate, beforeSlots, afterSlots []factSlot) []FieldChange {
	if len(template.fields) == 0 {
		return nil
	}
	changes := make([]FieldChange, 0, 1)
	for slot, field := range template.fields {
		before := factSlot{presence: fieldPresenceOmitted}
		if slot < len(beforeSlots) {
			before = beforeSlots[slot]
		}
		after := factSlot{presence: fieldPresenceOmitted}
		if slot < len(afterSlots) {
			after = afterSlots[slot]
		}
		if before.presence == after.presence && before.ok == after.ok && (!before.ok || before.value.Equal(after.value)) {
			continue
		}
		var beforeValue, afterValue Value
		if before.ok {
			beforeValue = before.value
		}
		if after.ok {
			afterValue = after.value
		}
		changes = append(changes, FieldChange{
			Field: field.Name,
			Old:   cloneValue(beforeValue),
			New:   cloneValue(afterValue),
		})
	}
	if len(changes) > 1 {
		sort.Slice(changes, func(i, j int) bool {
			return changes[i].Field < changes[j].Field
		})
	}
	return changes
}

func copyFactSlots(in []factSlot) []factSlot {
	if len(in) == 0 {
		return nil
	}
	out := make([]factSlot, len(in))
	copy(out, in)
	return out
}

func changedFields(beforeFields Fields, beforePresence map[string]FieldPresence, afterFields Fields, afterPresence map[string]FieldPresence) []FieldChange {
	keyCount := len(beforeFields) + len(afterFields)
	if keyCount == 0 {
		keyCount = len(beforePresence) + len(afterPresence)
	}
	if keyCount == 0 {
		return nil
	}
	keys := make([]string, 0, keyCount)
	for key := range beforeFields {
		keys = append(keys, key)
	}
	for key := range afterFields {
		if _, ok := beforeFields[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	for key := range beforePresence {
		if _, ok := beforeFields[key]; ok {
			continue
		}
		if _, ok := afterFields[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	for key := range afterPresence {
		if _, ok := beforeFields[key]; ok {
			continue
		}
		if _, ok := afterFields[key]; ok {
			continue
		}
		if _, ok := beforePresence[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	changes := make([]FieldChange, 0, len(keys))
	for index := 0; index < len(keys); index++ {
		key := keys[index]
		for index+1 < len(keys) && keys[index+1] == key {
			index++
		}
		beforePresenceType, beforeHasPresence := beforePresence[key]
		beforeValue, beforeHasValue := beforeFields[key]
		afterPresenceType, afterHasPresence := afterPresence[key]
		afterValue, afterHasValue := afterFields[key]

		beforeEquivalent := beforeHasPresence == afterHasPresence && beforePresenceType == afterPresenceType
		beforeEquivalent = beforeEquivalent && beforeHasValue == afterHasValue
		if beforeEquivalent && beforeHasValue {
			beforeEquivalent = beforeValue.Equal(afterValue)
		}
		if beforeEquivalent {
			continue
		}

		if !beforeHasValue {
			beforeValue = Value{}
		}
		if !afterHasValue {
			afterValue = Value{}
		}

		changes = append(changes, FieldChange{
			Field: key,
			Old:   cloneValue(beforeValue),
			New:   cloneValue(afterValue),
		})
	}

	return changes
}

type factModifySummary struct {
	unknown          bool
	changes          []FieldChange
	changedSlots     [4]int
	changedSlotCount int
	duplicateChanged bool
}

func newFactModifySummary(template compiledTemplate, changes []FieldChange, duplicateChanged bool) factModifySummary {
	if len(changes) == 0 {
		return factModifySummary{}
	}
	summary := factModifySummary{
		changes:          changes,
		duplicateChanged: duplicateChanged,
	}
	for _, change := range changes {
		slot, ok := template.fieldSlot(change.Field)
		if !ok || slot < 0 {
			summary.unknown = true
			continue
		}
		if summary.hasChangedSlot(slot) {
			continue
		}
		if summary.changedSlotCount >= len(summary.changedSlots) {
			summary.unknown = true
			continue
		}
		summary.changedSlots[summary.changedSlotCount] = slot
		summary.changedSlotCount++
	}
	sort.Ints(summary.changedSlots[:summary.changedSlotCount])
	return summary
}

func newFactModifySummaryFromPropagationEvent(event reteGraphPropagationEvent) factModifySummary {
	if len(event.changes) == 0 {
		return factModifySummary{}
	}
	summary := factModifySummary{
		changes:          event.changes,
		duplicateChanged: event.duplicateChanged,
	}
	if len(event.changedSlots) == 0 {
		summary.unknown = true
		return summary
	}
	for _, slot := range event.changedSlots {
		if slot < 0 {
			summary.unknown = true
			continue
		}
		if summary.hasChangedSlot(slot) {
			continue
		}
		if summary.changedSlotCount >= len(summary.changedSlots) {
			summary.unknown = true
			continue
		}
		summary.changedSlots[summary.changedSlotCount] = slot
		summary.changedSlotCount++
	}
	sort.Ints(summary.changedSlots[:summary.changedSlotCount])
	return summary
}

func (s factModifySummary) knownSlotChange() bool {
	return !s.unknown && s.changedSlotCount > 0
}

func (s factModifySummary) hasChangedSlot(slot int) bool {
	for i := 0; i < s.changedSlotCount; i++ {
		if s.changedSlots[i] == slot {
			return true
		}
	}
	return false
}

func removeFactIDFromSlice(ids []FactID, target FactID) []FactID {
	for i, id := range ids {
		if id != target {
			continue
		}
		copy(ids[i:], ids[i+1:])
		ids = ids[:len(ids)-1]
		break
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func (s *Session) snapshotLocked() Snapshot {
	return s.snapshotLockedWithOptions(false, true)
}

func (s *Session) indexedSnapshotLocked() Snapshot {
	return s.snapshotLockedWithOptions(true, false)
}

func (s *Session) detachedSnapshotLocked() Snapshot {
	return s.snapshotLockedWithOptions(false, false)
}

func (s *Session) snapshotLockedWithOptions(includeTargetIndexes bool, cloneFacts bool) Snapshot {
	facts := make([]FactSnapshot, 0, len(s.factStore.insertionOrder))
	for _, id := range s.factStore.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			continue
		}
		if cloneFacts {
			facts = append(facts, fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore))
		} else {
			facts = append(facts, fact.detachedSnapshotForRevision(s.revision, s.factStore.compactSlotStore))
		}
	}

	snapshot := Snapshot{
		sessionID:      s.id,
		rulesetID:      s.revision.ID(),
		revision:       s.revision,
		generation:     s.factStore.generation,
		globalValues:   cloneGlobalValues(s.globalValues),
		facts:          facts,
		support:        s.currentSupportGraph(),
		demandCounters: s.backchain.demandCounters,
	}
	if includeTargetIndexes {
		snapshot.byID, snapshot.byName, snapshot.byTemplate = snapshotIndexes(facts)
	} else {
		snapshot.byID = snapshotIDIndex(facts)
	}
	return snapshot
}

type factWorkspace struct {
	generation                Generation
	sequence                  uint64
	recency                   Recency
	facts                     []workingFact
	compactFacts              compactFactStore
	insertionOrder            []FactID
	factsByID                 map[FactID]int
	factsBySequence           []int32
	factsByDuplicate          duplicateIndexes
	duplicateReserveRulesetID RulesetID
	factsByTemplate           map[TemplateKey][]FactID
	factsByName               map[string][]FactID
	slotStorage               []factSlot
	compactSlotStore          *factCompactSlotStore
	skipFactTargetIndexes     bool
	factTargetIndexesDirty    bool
}

type factWorkspaceInsertMark struct {
	sequence               uint64
	recency                Recency
	factsLen               int
	compactFactsLen        int
	insertionOrderLen      int
	slotStorageLen         int
	compactSlotStoreLen    int
	factTargetIndexesDirty bool
}

type factTargetIndexMode uint8

const (
	factTargetIndexEager factTargetIndexMode = iota
	factTargetIndexDirty
	factTargetIndexSkip
)

type factWorkspaceModifyMark struct {
	recency                Recency
	factTargetIndexesDirty bool
	factIndex              int
	fact                   workingFact
	compactSlotStoreLen    int
	duplicateIndexUndo     duplicateIndexModifyUndo
}

// duplicateIndexModifyUndo records the point mutation a fact modify applies to
// the duplicate indexes (remove under oldKey, add under newKey) so rollback can
// reverse it without cloning the whole index.
type duplicateIndexModifyUndo struct {
	tracked    bool
	factID     FactID
	oldKey     duplicateIndexKey
	oldHeld    bool
	newKey     duplicateIndexKey
	newHeld    bool
	newPrevHad bool
	newPrevID  FactID
}

type duplicateSingleIntIndexKey struct {
	templateKey TemplateKey
	value       int64
}

type duplicateDoubleIntIndexKey struct {
	templateKey TemplateKey
	first       int64
	second      int64
}

type duplicateIntStringIndexKey struct {
	templateKey TemplateKey
	intValue    int64
	stringValue string
}

type duplicateStringIntIndexKey struct {
	templateKey TemplateKey
	stringValue string
	intValue    int64
}

type duplicateIntStringStringIndexKey struct {
	templateKey  TemplateKey
	intValue     int64
	stringValue  string
	stringValue2 string
}

type duplicateStructuralIndexKey struct {
	templateKey TemplateKey
	hash        uint64
}

type duplicateIndexes struct {
	strings    map[DuplicateKey]FactID
	singleInt  map[duplicateSingleIntIndexKey]FactID
	doubleInt  map[duplicateDoubleIntIndexKey]FactID
	intString  map[duplicateIntStringIndexKey]FactID
	stringInt  map[duplicateStringIntIndexKey]FactID
	intString2 map[duplicateIntStringStringIndexKey]FactID
	string2Int duplicateStringStringIntIndexTable
	scalars    map[duplicateIndexKey]FactID
	structural duplicateStructuralIndexTable
}

func (i *duplicateIndexes) reset(initialCapacity int) {
	if i.strings != nil {
		clear(i.strings)
	}
	if i.singleInt != nil {
		clear(i.singleInt)
	}
	if i.doubleInt != nil {
		clear(i.doubleInt)
	}
	if i.intString != nil {
		clear(i.intString)
	}
	if i.stringInt != nil {
		clear(i.stringInt)
	}
	if i.intString2 != nil {
		clear(i.intString2)
	}
	i.string2Int.clear()
	if i.scalars != nil {
		clear(i.scalars)
	}
	i.structural.clear()
}

func (i *duplicateIndexes) reserve(revision *Ruleset, factCapacity int) {
	if i == nil || revision == nil || factCapacity <= 0 {
		return
	}
	templateCount := 0
	for _, name := range revision.templateOrder {
		template := revision.templates[name]
		if template.duplicatePolicy != DuplicateAllow {
			templateCount++
		}
	}
	if templateCount == 0 {
		return
	}
	perTemplate := max(1, (factCapacity+templateCount-1)/templateCount)
	var stringsCapacity, singleIntCapacity, doubleIntCapacity, intStringCapacity, stringIntCapacity, intString2Capacity, string2IntCapacity, scalarCapacity, structuralCapacity int
	for _, name := range revision.templateOrder {
		template := revision.templates[name]
		if template.duplicatePolicy == DuplicateAllow {
			continue
		}
		switch duplicateReserveKind(template) {
		case duplicateIndexSingleInt:
			singleIntCapacity += perTemplate
		case duplicateIndexDoubleInt:
			doubleIntCapacity += perTemplate
		case duplicateIndexIntString:
			intStringCapacity += perTemplate
		case duplicateIndexStringInt:
			stringIntCapacity += perTemplate
		case duplicateIndexIntStringString:
			intString2Capacity += perTemplate
		case duplicateIndexStringStringInt:
			string2IntCapacity += perTemplate
		case duplicateIndexSingleScalar, duplicateIndexDoubleScalar:
			scalarCapacity += perTemplate
		case duplicateIndexStructural:
			structuralCapacity += perTemplate
		default:
			stringsCapacity += perTemplate
		}
	}
	if stringsCapacity > 0 && i.strings == nil {
		i.strings = make(map[DuplicateKey]FactID, stringsCapacity)
	}
	if singleIntCapacity > 0 && i.singleInt == nil {
		i.singleInt = make(map[duplicateSingleIntIndexKey]FactID, singleIntCapacity)
	}
	if doubleIntCapacity > 0 && i.doubleInt == nil {
		i.doubleInt = make(map[duplicateDoubleIntIndexKey]FactID, doubleIntCapacity)
	}
	if intStringCapacity > 0 && i.intString == nil {
		i.intString = make(map[duplicateIntStringIndexKey]FactID, intStringCapacity)
	}
	if stringIntCapacity > 0 && i.stringInt == nil {
		i.stringInt = make(map[duplicateStringIntIndexKey]FactID, stringIntCapacity)
	}
	if intString2Capacity > 0 && i.intString2 == nil {
		i.intString2 = make(map[duplicateIntStringStringIndexKey]FactID, intString2Capacity)
	}
	if string2IntCapacity > 0 {
		i.string2Int.reserve(string2IntCapacity)
	}
	if scalarCapacity > 0 && i.scalars == nil {
		i.scalars = make(map[duplicateIndexKey]FactID, scalarCapacity)
	}
	if structuralCapacity > 0 {
		i.structural.reserve(structuralCapacity)
	}
}

func duplicateReserveKind(template compiledTemplate) duplicateIndexKind {
	if template.duplicatePolicy == DuplicateStructural {
		return duplicateIndexStructural
	}
	if template.duplicatePolicy != DuplicateUniqueKey {
		return duplicateIndexString
	}
	switch template.duplicateIndexMode {
	case duplicateIndexSingleScalar:
		if len(template.duplicateKeySlots) == 1 && duplicateTemplateSlotKind(template, template.duplicateKeySlots[0]) == ValueInt {
			return duplicateIndexSingleInt
		}
	case duplicateIndexDoubleScalar:
		if len(template.duplicateKeySlots) == 2 &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[0]) == ValueInt &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[1]) == ValueInt {
			return duplicateIndexDoubleInt
		}
		if len(template.duplicateKeySlots) == 2 &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[0]) == ValueInt &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[1]) == ValueString {
			return duplicateIndexIntString
		}
		if len(template.duplicateKeySlots) == 2 &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[0]) == ValueString &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[1]) == ValueInt {
			return duplicateIndexStringInt
		}
		if len(template.duplicateKeySlots) == 3 &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[0]) == ValueInt &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[1]) == ValueString &&
			duplicateTemplateSlotKind(template, template.duplicateKeySlots[2]) == ValueString {
			return duplicateIndexIntStringString
		}
	case duplicateIndexIntStringString:
		return duplicateIndexIntStringString
	case duplicateIndexStringStringInt:
		return duplicateIndexStringStringInt
	}
	return template.duplicateIndexMode
}

func duplicateTemplateSlotKind(template compiledTemplate, slot int) ValueKind {
	if slot < 0 || slot >= len(template.fields) {
		return ValueAny
	}
	return template.fields[slot].Kind
}

func (i duplicateIndexes) get(key duplicateIndexKey) (FactID, bool) {
	switch key.kind {
	case duplicateIndexString:
		factID, ok := i.strings[key.stringKey]
		return factID, ok
	case duplicateIndexSingleInt:
		factID, ok := i.singleInt[duplicateSingleIntIndexKey{templateKey: key.templateKey, value: key.firstInt}]
		return factID, ok
	case duplicateIndexDoubleInt:
		factID, ok := i.doubleInt[duplicateDoubleIntIndexKey{templateKey: key.templateKey, first: key.firstInt, second: key.secondInt}]
		return factID, ok
	case duplicateIndexIntString:
		factID, ok := i.intString[duplicateIntStringIndexKey{templateKey: key.templateKey, intValue: key.firstInt, stringValue: key.stringValue}]
		return factID, ok
	case duplicateIndexStringInt:
		factID, ok := i.stringInt[duplicateStringIntIndexKey{templateKey: key.templateKey, stringValue: key.stringValue, intValue: key.firstInt}]
		return factID, ok
	case duplicateIndexIntStringString:
		factID, ok := i.intString2[duplicateIntStringStringIndexKey{templateKey: key.templateKey, intValue: key.firstInt, stringValue: key.stringValue, stringValue2: key.stringValue2}]
		return factID, ok
	case duplicateIndexStringStringInt:
		return FactID{}, false
	case duplicateIndexStructural:
		return i.structural.get(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash})
	default:
		factID, ok := i.scalars[key]
		return factID, ok
	}
}

func (i duplicateIndexes) holdsFact(key duplicateIndexKey, factID FactID) bool {
	if key.isZero() || factID.IsZero() {
		return false
	}
	switch key.kind {
	case duplicateIndexStringStringInt:
		held := false
		i.string2Int.forEachFactID(key, func(id FactID) bool {
			if id == factID {
				held = true
				return false
			}
			return true
		})
		return held
	case duplicateIndexStructural:
		held := false
		i.structural.forEachFactID(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash}, func(id FactID) bool {
			if id == factID {
				held = true
				return false
			}
			return true
		})
		return held
	default:
		existing, ok := i.get(key)
		return ok && existing == factID
	}
}

func (i *duplicateIndexes) applyModifyUndo(undo duplicateIndexModifyUndo) {
	if i == nil || !undo.tracked {
		return
	}
	if !undo.newKey.isZero() {
		switch undo.newKey.kind {
		case duplicateIndexStringStringInt, duplicateIndexStructural:
			if !undo.newHeld {
				i.deleteFact(undo.newKey, undo.factID)
			}
		default:
			if undo.newPrevHad {
				i.set(undo.newKey, undo.newPrevID)
			} else {
				i.deleteFact(undo.newKey, undo.factID)
			}
		}
	}
	if undo.oldHeld {
		i.set(undo.oldKey, undo.factID)
	}
}

func (i *duplicateIndexes) set(key duplicateIndexKey, factID FactID) {
	if key.isZero() {
		return
	}
	switch key.kind {
	case duplicateIndexString:
		if i.strings == nil {
			i.strings = make(map[DuplicateKey]FactID)
		}
		i.strings[key.stringKey] = factID
	case duplicateIndexSingleInt:
		if i.singleInt == nil {
			i.singleInt = make(map[duplicateSingleIntIndexKey]FactID)
		}
		i.singleInt[duplicateSingleIntIndexKey{templateKey: key.templateKey, value: key.firstInt}] = factID
	case duplicateIndexDoubleInt:
		if i.doubleInt == nil {
			i.doubleInt = make(map[duplicateDoubleIntIndexKey]FactID)
		}
		i.doubleInt[duplicateDoubleIntIndexKey{templateKey: key.templateKey, first: key.firstInt, second: key.secondInt}] = factID
	case duplicateIndexIntString:
		if i.intString == nil {
			i.intString = make(map[duplicateIntStringIndexKey]FactID)
		}
		i.intString[duplicateIntStringIndexKey{templateKey: key.templateKey, intValue: key.firstInt, stringValue: key.stringValue}] = factID
	case duplicateIndexStringInt:
		if i.stringInt == nil {
			i.stringInt = make(map[duplicateStringIntIndexKey]FactID)
		}
		i.stringInt[duplicateStringIntIndexKey{templateKey: key.templateKey, stringValue: key.stringValue, intValue: key.firstInt}] = factID
	case duplicateIndexIntStringString:
		if i.intString2 == nil {
			i.intString2 = make(map[duplicateIntStringStringIndexKey]FactID)
		}
		i.intString2[duplicateIntStringStringIndexKey{templateKey: key.templateKey, intValue: key.firstInt, stringValue: key.stringValue, stringValue2: key.stringValue2}] = factID
	case duplicateIndexStringStringInt:
		i.string2Int.set(key, factID)
	case duplicateIndexStructural:
		i.structural.set(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash}, factID)
	default:
		if i.scalars == nil {
			i.scalars = make(map[duplicateIndexKey]FactID)
		}
		i.scalars[key] = factID
	}
}

func (i *duplicateIndexes) delete(key duplicateIndexKey) {
	if key.isZero() {
		return
	}
	switch key.kind {
	case duplicateIndexString:
		delete(i.strings, key.stringKey)
	case duplicateIndexSingleInt:
		delete(i.singleInt, duplicateSingleIntIndexKey{templateKey: key.templateKey, value: key.firstInt})
	case duplicateIndexDoubleInt:
		delete(i.doubleInt, duplicateDoubleIntIndexKey{templateKey: key.templateKey, first: key.firstInt, second: key.secondInt})
	case duplicateIndexIntString:
		delete(i.intString, duplicateIntStringIndexKey{templateKey: key.templateKey, intValue: key.firstInt, stringValue: key.stringValue})
	case duplicateIndexStringInt:
		delete(i.stringInt, duplicateStringIntIndexKey{templateKey: key.templateKey, stringValue: key.stringValue, intValue: key.firstInt})
	case duplicateIndexIntStringString:
		delete(i.intString2, duplicateIntStringStringIndexKey{templateKey: key.templateKey, intValue: key.firstInt, stringValue: key.stringValue, stringValue2: key.stringValue2})
	case duplicateIndexStringStringInt:
		i.string2Int.delete(key)
	case duplicateIndexStructural:
		i.structural.delete(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash})
	default:
		delete(i.scalars, key)
	}
}

func (i *duplicateIndexes) deleteFact(key duplicateIndexKey, factID FactID) {
	if key.isZero() {
		return
	}
	if key.kind == duplicateIndexStringStringInt {
		i.string2Int.deleteFact(key, factID)
		return
	}
	if key.kind != duplicateIndexStructural {
		if existingID, ok := i.get(key); ok && existingID == factID {
			i.delete(key)
		}
		return
	}
	i.structural.deleteFact(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash}, factID)
}

func (i duplicateIndexes) forEachStringStringIntFactID(key duplicateIndexKey, fn func(FactID) bool) {
	if key.kind != duplicateIndexStringStringInt || fn == nil {
		return
	}
	i.string2Int.forEachFactID(key, fn)
}

func (i duplicateIndexes) forEachStructuralFactID(key duplicateIndexKey, fn func(FactID) bool) {
	if key.kind != duplicateIndexStructural || fn == nil {
		return
	}
	i.structural.forEachFactID(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash}, fn)
}

func (i duplicateIndexes) len() int {
	return len(i.strings) + len(i.singleInt) + len(i.doubleInt) + len(i.intString) + len(i.stringInt) + len(i.intString2) + i.string2Int.len() + len(i.scalars) + i.structural.len()
}

type duplicateStructuralIndexEntry struct {
	key   duplicateStructuralIndexKey
	first FactID
	rest  []FactID
	state uint8
}

type duplicateStringStringIntIndexEntry struct {
	hash     uint64
	first    FactID
	overflow int32
	state    uint8
}

type duplicateStringStringIntOverflowEntry struct {
	factID FactID
	next   int32
}

type duplicateStringStringIntIndexTable struct {
	entries  []duplicateStringStringIntIndexEntry
	overflow []duplicateStringStringIntOverflowEntry
	touched  []int
	count    int
	used     int
}

func (t *duplicateStringStringIntIndexTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	t.rehash(duplicateStringStringIntSlotCapacity(capacity))
}

func (t *duplicateStringStringIntIndexTable) clear() {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for _, index := range t.touched {
		if index < 0 || index >= len(t.entries) {
			continue
		}
		t.entries[index] = duplicateStringStringIntIndexEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t duplicateStringStringIntIndexTable) len() int {
	return t.count
}

func (t *duplicateStringStringIntIndexTable) set(key duplicateIndexKey, factID FactID) {
	if t == nil || factID.IsZero() {
		return
	}
	hash := hashDuplicateStringStringIntIndexKey(key)
	if duplicateStringStringIntNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(duplicateStringStringIntGrowCapacity(len(t.entries), t.used+1))
	}
	index, ok := t.findInsert(hash)
	if ok {
		t.addToEntry(index, factID)
		return
	}
	if t.entries[index].state == graphTokenBucketEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = duplicateStringStringIntIndexEntry{hash: hash, first: factID, overflow: -1, state: graphTokenBucketFull}
	t.count++
}

func (t *duplicateStringStringIntIndexTable) delete(key duplicateIndexKey) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(hashDuplicateStringStringIntIndexKey(key))
	if !ok {
		return
	}
	if t.entries[index].overflow >= 0 {
		return
	}
	t.entries[index] = duplicateStringStringIntIndexEntry{overflow: -1, state: graphTokenBucketDeleted}
	t.count--
}

func (t *duplicateStringStringIntIndexTable) deleteFact(key duplicateIndexKey, factID FactID) {
	if t == nil || t.count == 0 || factID.IsZero() {
		return
	}
	index, ok := t.find(hashDuplicateStringStringIntIndexKey(key))
	if !ok {
		return
	}
	entry := &t.entries[index]
	if entry.first == factID {
		if entry.overflow >= 0 {
			overflowIndex := entry.overflow
			overflow := t.overflow[overflowIndex]
			entry.first = overflow.factID
			entry.overflow = overflow.next
			t.overflow[overflowIndex] = duplicateStringStringIntOverflowEntry{}
		} else {
			t.entries[index] = duplicateStringStringIntIndexEntry{overflow: -1, state: graphTokenBucketDeleted}
		}
		t.count--
		return
	}
	previous := int32(-1)
	for overflowIndex := entry.overflow; overflowIndex >= 0; {
		overflow := t.overflow[overflowIndex]
		if overflow.factID == factID {
			if previous >= 0 {
				t.overflow[previous].next = overflow.next
			} else {
				entry.overflow = overflow.next
			}
			t.overflow[overflowIndex] = duplicateStringStringIntOverflowEntry{}
			t.count--
			return
		}
		previous = overflowIndex
		overflowIndex = overflow.next
	}
}

func (t duplicateStringStringIntIndexTable) forEachFactID(key duplicateIndexKey, fn func(FactID) bool) {
	if t.count == 0 || len(t.entries) == 0 || fn == nil {
		return
	}
	index, ok := t.find(hashDuplicateStringStringIntIndexKey(key))
	if !ok {
		return
	}
	entry := t.entries[index]
	if !entry.first.IsZero() && !fn(entry.first) {
		return
	}
	for overflowIndex := entry.overflow; overflowIndex >= 0; {
		overflow := t.overflow[overflowIndex]
		if !overflow.factID.IsZero() && !fn(overflow.factID) {
			return
		}
		overflowIndex = overflow.next
	}
}

func (t *duplicateStringStringIntIndexTable) addToEntry(index int, factID FactID) {
	entry := &t.entries[index]
	if entry.first.IsZero() {
		entry.first = factID
		t.count++
		return
	}
	if entry.first == factID {
		return
	}
	for overflowIndex := entry.overflow; overflowIndex >= 0; {
		overflow := t.overflow[overflowIndex]
		if overflow.factID == factID {
			return
		}
		overflowIndex = overflow.next
	}
	if len(t.overflow) == int(^uint32(0)>>1) {
		return
	}
	t.overflow = append(t.overflow, duplicateStringStringIntOverflowEntry{factID: factID, next: entry.overflow})
	entry.overflow = int32(len(t.overflow) - 1)
	t.count++
}

func (t *duplicateStringStringIntIndexTable) find(hash uint64) (int, bool) {
	index := int(hash % uint64(len(t.entries)))
	for {
		entry := t.entries[index]
		if entry.state == graphTokenBucketEmpty {
			return 0, false
		}
		if entry.state == graphTokenBucketFull && entry.hash == hash {
			return index, true
		}
		index++
		if index == len(t.entries) {
			index = 0
		}
	}
}

func (t *duplicateStringStringIntIndexTable) findInsert(hash uint64) (int, bool) {
	index := int(hash % uint64(len(t.entries)))
	firstDeleted := -1
	for {
		entry := t.entries[index]
		switch entry.state {
		case graphTokenBucketEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case graphTokenBucketDeleted:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		case graphTokenBucketFull:
			if entry.hash == hash {
				return index, true
			}
		}
		index++
		if index == len(t.entries) {
			index = 0
		}
	}
}

func (t *duplicateStringStringIntIndexTable) rehash(slotCapacity int) {
	slotCapacity = max(8, slotCapacity)
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	oldOverflow := t.overflow
	t.entries = make([]duplicateStringStringIntIndexEntry, slotCapacity)
	t.overflow = t.overflow[:0]
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state != graphTokenBucketFull {
			continue
		}
		t.setHash(old[i].hash, old[i].first)
		for overflowIndex := old[i].overflow; overflowIndex >= 0; {
			overflow := oldOverflow[overflowIndex]
			t.setHash(old[i].hash, overflow.factID)
			overflowIndex = overflow.next
		}
	}
}

func (t *duplicateStringStringIntIndexTable) setHash(hash uint64, factID FactID) {
	if t == nil || factID.IsZero() {
		return
	}
	if duplicateStringStringIntNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(duplicateStringStringIntGrowCapacity(len(t.entries), t.used+1))
	}
	index, ok := t.findInsert(hash)
	if ok {
		t.addToEntry(index, factID)
		return
	}
	if t.entries[index].state == graphTokenBucketEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = duplicateStringStringIntIndexEntry{hash: hash, first: factID, overflow: -1, state: graphTokenBucketFull}
	t.count++
}

func hashDuplicateStringStringIntIndexKey(key duplicateIndexKey) uint64 {
	hash := graphTokenBucketMixString(0, key.templateKey.String())
	hash = graphTokenBucketMixString(hash, key.stringValue)
	hash = graphTokenBucketMixString(hash, key.stringValue2)
	return graphTokenBucketMixAdd(hash, uint64(key.firstInt))
}

func duplicateStringStringIntSlotCapacity(capacity int) int {
	if capacity <= 0 {
		return 0
	}
	return max(8, capacity+(capacity+2)/3)
}

func duplicateStringStringIntNeedsGrow(used, slots int) bool {
	return slots == 0 || used*4 >= slots*3
}

func duplicateStringStringIntGrowCapacity(current, needed int) int {
	next := max(8, current*2)
	minimum := duplicateStringStringIntSlotCapacity(needed)
	if next < minimum {
		next = minimum
	}
	return next
}

type duplicateStructuralIndexTable struct {
	entries []duplicateStructuralIndexEntry
	touched []int
	count   int
	used    int
}

func (t *duplicateStructuralIndexTable) reserve(capacity int) {
	if capacity <= 0 {
		return
	}
	t.rehash(graphTokenBucketSlotCapacity(capacity))
}

func (t *duplicateStructuralIndexTable) clear() {
	if t == nil || len(t.entries) == 0 {
		return
	}
	for _, index := range t.touched {
		if index < 0 || index >= len(t.entries) {
			continue
		}
		entry := &t.entries[index]
		for i := range entry.rest {
			entry.rest[i] = FactID{}
		}
		*entry = duplicateStructuralIndexEntry{}
	}
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
}

func (t duplicateStructuralIndexTable) len() int {
	return t.count
}

func (t *duplicateStructuralIndexTable) get(key duplicateStructuralIndexKey) (FactID, bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 {
		return FactID{}, false
	}
	index, ok := t.find(key)
	if !ok {
		return FactID{}, false
	}
	id := t.entries[index].first
	if id.IsZero() {
		return FactID{}, false
	}
	return id, true
}

func (t *duplicateStructuralIndexTable) set(key duplicateStructuralIndexKey, factID FactID) {
	if t == nil || factID.IsZero() {
		return
	}
	if graphTokenBucketNeedsGrow(t.used+1, len(t.entries)) {
		t.rehash(max(8, len(t.entries)*2))
	}
	index, ok := t.findInsert(key)
	if ok {
		entry := &t.entries[index]
		if entry.first.IsZero() {
			entry.first = factID
			return
		}
		entry.rest = append(entry.rest, factID)
		return
	}
	if t.entries[index].state == graphTokenBucketEmpty {
		t.touched = append(t.touched, index)
		t.used++
	}
	t.entries[index] = duplicateStructuralIndexEntry{key: key, first: factID, state: graphTokenBucketFull}
	t.count++
}

func (t *duplicateStructuralIndexTable) delete(key duplicateStructuralIndexKey) {
	if t == nil || t.count == 0 {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	entry := &t.entries[index]
	for i := range entry.rest {
		entry.rest[i] = FactID{}
	}
	*entry = duplicateStructuralIndexEntry{state: graphTokenBucketDeleted}
	t.count--
}

func (t *duplicateStructuralIndexTable) deleteFact(key duplicateStructuralIndexKey, factID FactID) {
	if t == nil || t.count == 0 || factID.IsZero() {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	entry := &t.entries[index]
	if entry.first == factID {
		if len(entry.rest) == 0 {
			*entry = duplicateStructuralIndexEntry{state: graphTokenBucketDeleted}
			t.count--
			return
		}
		last := len(entry.rest) - 1
		entry.first = entry.rest[last]
		entry.rest[last] = FactID{}
		entry.rest = entry.rest[:last]
		return
	}
	for restIndex, id := range entry.rest {
		if id != factID {
			continue
		}
		last := len(entry.rest) - 1
		entry.rest[restIndex] = entry.rest[last]
		entry.rest[last] = FactID{}
		entry.rest = entry.rest[:last]
		return
	}
}

func (t *duplicateStructuralIndexTable) forEachFactID(key duplicateStructuralIndexKey, fn func(FactID) bool) {
	if t == nil || t.count == 0 || len(t.entries) == 0 || fn == nil {
		return
	}
	index, ok := t.find(key)
	if !ok {
		return
	}
	entry := t.entries[index]
	if !entry.first.IsZero() && !fn(entry.first) {
		return
	}
	for _, id := range entry.rest {
		if id.IsZero() || !fn(id) {
			return
		}
	}
}

func (t *duplicateStructuralIndexTable) find(key duplicateStructuralIndexKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashDuplicateStructuralIndexKey(key) & mask)
	for {
		entry := t.entries[index]
		if entry.state == graphTokenBucketEmpty {
			return 0, false
		}
		if entry.state == graphTokenBucketFull && entry.key == key {
			return index, true
		}
		index = (index + 1) & int(mask)
	}
}

func (t *duplicateStructuralIndexTable) findInsert(key duplicateStructuralIndexKey) (int, bool) {
	mask := uint64(len(t.entries) - 1)
	index := int(hashDuplicateStructuralIndexKey(key) & mask)
	firstDeleted := -1
	for {
		entry := t.entries[index]
		switch entry.state {
		case graphTokenBucketEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case graphTokenBucketDeleted:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		case graphTokenBucketFull:
			if entry.key == key {
				return index, true
			}
		}
		index = (index + 1) & int(mask)
	}
}

func (t *duplicateStructuralIndexTable) rehash(slotCapacity int) {
	slotCapacity = graphTokenBucketPowerOfTwo(max(8, slotCapacity))
	if slotCapacity <= len(t.entries) && t.used == t.count {
		return
	}
	old := t.entries
	t.entries = make([]duplicateStructuralIndexEntry, slotCapacity)
	t.touched = t.touched[:0]
	t.count = 0
	t.used = 0
	for i := range old {
		if old[i].state != graphTokenBucketFull {
			continue
		}
		t.set(old[i].key, old[i].first)
		for _, id := range old[i].rest {
			t.set(old[i].key, id)
		}
	}
}

func hashDuplicateStructuralIndexKey(key duplicateStructuralIndexKey) uint64 {
	return key.hash
}

func newFactWorkspace(generation Generation, initialCapacity int) *factWorkspace {
	w := &factWorkspace{}
	w.reset(generation, initialCapacity)
	return w
}

func (w *factWorkspace) reset(generation Generation, initialCapacity int) {
	if w == nil {
		return
	}
	if initialCapacity < 0 {
		initialCapacity = 0
	}
	w.generation = generation
	w.sequence = 0
	w.recency = 0
	w.skipFactTargetIndexes = false
	w.factTargetIndexesDirty = false
	w.factsByID = nil
	w.factsBySequence = resetFactRowSequenceIndex(w.factsBySequence, initialCapacity)
	w.factsByDuplicate.reset(initialCapacity)
	if w.factsByTemplate == nil {
		w.factsByTemplate = make(map[TemplateKey][]FactID, initialCapacity)
	} else {
		clear(w.factsByTemplate)
	}
	if w.factsByName == nil {
		w.factsByName = make(map[string][]FactID, initialCapacity)
	} else {
		clear(w.factsByName)
	}
	if w.insertionOrder == nil {
		w.insertionOrder = make([]FactID, 0, initialCapacity)
	} else if cap(w.insertionOrder) < initialCapacity {
		w.insertionOrder = make([]FactID, 0, initialCapacity)
	} else {
		w.insertionOrder = w.insertionOrder[:0]
	}
	if w.facts == nil {
		w.facts = make([]workingFact, 0, initialCapacity)
	} else if cap(w.facts) < initialCapacity {
		w.facts = make([]workingFact, 0, initialCapacity)
	} else {
		for i := range w.facts {
			w.facts[i] = workingFact{}
		}
		w.facts = w.facts[:0]
	}
	w.compactFacts.reset()
	w.slotStorage = nil
	if w.compactSlotStore == nil {
		w.compactSlotStore = &factCompactSlotStore{}
	}
	w.compactSlotStore.reset(initialCapacity)
}

func (w *factWorkspace) reserveDuplicateIndexes(revision *Ruleset) {
	if w == nil || revision == nil {
		return
	}
	rulesetID := revision.ID()
	if w.duplicateReserveRulesetID == rulesetID {
		return
	}
	w.factsByDuplicate.reserve(revision, cap(w.facts)+cap(w.compactFacts.ids))
	w.duplicateReserveRulesetID = rulesetID
}

func (w *factWorkspace) reserveTemplateIndexes(revision *Ruleset) {
	if w == nil || revision == nil || w.skipFactTargetIndexes {
		return
	}
	templateCount := len(revision.templateOrder)
	if templateCount == 0 {
		return
	}
	rowCount := len(w.facts) + w.compactFacts.len()
	perTemplate := max((rowCount+templateCount-1)/templateCount+runFactReservePerRule, 1)
	for _, name := range revision.templateOrder {
		template := revision.templates[name]
		if template.key != "" {
			if ids := w.factsByTemplate[template.key]; cap(ids) < perTemplate {
				next := make([]FactID, len(ids), perTemplate)
				copy(next, ids)
				w.factsByTemplate[template.key] = next
			}
		}
		if template.name != "" {
			if ids := w.factsByName[template.name]; cap(ids) < perTemplate {
				next := make([]FactID, len(ids), perTemplate)
				copy(next, ids)
				w.factsByName[template.name] = next
			}
		}
	}
}

func (w *factWorkspace) addFactTargetIndexes(templateKey TemplateKey, name string, id FactID) {
	if w == nil || id.IsZero() {
		return
	}
	if w.skipFactTargetIndexes || w.factTargetIndexesDirty {
		w.factTargetIndexesDirty = true
		return
	}
	w.factsByTemplate[templateKey] = append(w.factsByTemplate[templateKey], id)
	w.factsByName[name] = append(w.factsByName[name], id)
}

func (w *factWorkspace) markFactTargetIndexesDirty() {
	if w == nil {
		return
	}
	w.factTargetIndexesDirty = true
}

func (w *factWorkspace) removeFactTargetIndexes(templateKey TemplateKey, name string, id FactID) {
	if w == nil || id.IsZero() || w.factTargetIndexesDirty {
		return
	}
	w.factsByTemplate[templateKey] = removeFactIDFromSlice(w.factsByTemplate[templateKey], id)
	if len(w.factsByTemplate[templateKey]) == 0 {
		delete(w.factsByTemplate, templateKey)
	}
	w.factsByName[name] = removeFactIDFromSlice(w.factsByName[name], id)
	if len(w.factsByName[name]) == 0 {
		delete(w.factsByName, name)
	}
}

func (w *factWorkspace) removeStoredFact(id FactID) {
	if w == nil {
		return
	}
	handle, ok := w.factRowIndex(id)
	if !ok {
		return
	}
	if row, generated := decodeCompactFactRow(handle); generated {
		fact, ok := w.compactFacts.fact(row)
		if !ok || fact.id != id {
			return
		}
		moved, ok := w.compactFacts.remove(row)
		w.deleteFactRowIndex(id)
		if ok {
			w.setFactRowIndex(moved, encodeCompactFactRow(row))
		}
		return
	}
	if handle < 0 || handle >= len(w.facts) || w.facts[handle].id != id {
		return
	}
	last := len(w.facts) - 1
	if handle != last {
		moved := w.facts[last]
		w.facts[handle] = moved
		w.setFactRowIndex(moved.id, handle)
	}
	w.deleteFactRowIndex(id)
	w.facts[last] = workingFact{}
	w.facts = w.facts[:last]
}

func (w *factWorkspace) reserveSlotStorage(capacity int) {
	if w == nil || capacity <= cap(w.slotStorage) {
		return
	}
	next := make([]factSlot, len(w.slotStorage), capacity)
	copy(next, w.slotStorage)
	w.slotStorage = next
}

func (w *factWorkspace) reserveCompactSlotStorage(capacity int) {
	if w == nil {
		return
	}
	if w.compactSlotStore == nil {
		w.compactSlotStore = &factCompactSlotStore{}
	}
	w.compactSlotStore.reserve(capacity)
}

func (w *factWorkspace) reserveGeneratedFactCapacity(revision *Ruleset, factCount, slotCount, compactSlotCount int) {
	if w == nil {
		return
	}
	if factCount > 0 {
		w.compactFacts.reserve(saturatingAddInt(w.compactFacts.len(), factCount))
		w.reserveFactRowSequenceRows(factCount)
		orderCapacity := saturatingAddInt(len(w.insertionOrder), factCount)
		if cap(w.insertionOrder) < orderCapacity {
			nextOrder := make([]FactID, len(w.insertionOrder), orderCapacity)
			copy(nextOrder, w.insertionOrder)
			w.insertionOrder = nextOrder
		}
	}
	if slotCount > 0 {
		w.reserveSlotStorage(saturatingAddInt(len(w.slotStorage), slotCount))
	}
	if compactSlotCount > 0 {
		w.reserveCompactSlotStorage(saturatingAddInt(w.compactSlotStore.len(), compactSlotCount))
	}
	w.reserveTemplateIndexes(revision)
}

func (w *factWorkspace) reserveGeneratedFactRowInsert(revision *Ruleset) {
	if w == nil {
		return
	}
	if len(w.facts) == cap(w.facts) {
		nextCapacity := nextGeneratedFactCapacity(len(w.facts), cap(w.facts), revision)
		if nextCapacity > cap(w.facts) {
			nextFacts := make([]workingFact, len(w.facts), nextCapacity)
			copy(nextFacts, w.facts)
			w.facts = nextFacts
		}
	}
	if len(w.insertionOrder) == cap(w.insertionOrder) {
		nextCapacity := nextGeneratedFactCapacity(len(w.insertionOrder), cap(w.insertionOrder), revision)
		if nextCapacity > cap(w.insertionOrder) {
			nextOrder := make([]FactID, len(w.insertionOrder), nextCapacity)
			copy(nextOrder, w.insertionOrder)
			w.insertionOrder = nextOrder
		}
	}
	w.reserveFactRowSequenceRows(1)
}

func (w *factWorkspace) reserveGeneratedFactInsert(revision *Ruleset, slotCount int) {
	if w == nil {
		return
	}
	w.reserveGeneratedFactRowInsert(revision)
	if slotCount > 0 && cap(w.slotStorage)-len(w.slotStorage) < slotCount {
		nextCapacity := nextGeneratedSlotCapacity(len(w.slotStorage), cap(w.slotStorage), slotCount, revision)
		w.reserveSlotStorage(nextCapacity)
	}
}

func (w *factWorkspace) reserveGeneratedCompactFactInsert(revision *Ruleset, slotCount int) {
	if w == nil {
		return
	}
	w.reserveGeneratedFactMetadataInsert(revision)
	if w.compactSlotStore == nil {
		w.compactSlotStore = &factCompactSlotStore{}
	}
	if slotCount > 0 && w.compactSlotStore.cap()-w.compactSlotStore.len() < slotCount {
		nextCapacity := max(max(w.compactSlotStore.cap()*2, w.compactSlotStore.len()+slotCount), 16)
		w.reserveCompactSlotStorage(nextCapacity)
	}
}

func (w *factWorkspace) reserveGeneratedFactMetadataInsert(revision *Ruleset) {
	if w == nil {
		return
	}
	if len(w.insertionOrder) == cap(w.insertionOrder) {
		nextCapacity := nextGeneratedFactCapacity(len(w.insertionOrder), cap(w.insertionOrder), revision)
		if nextCapacity > cap(w.insertionOrder) {
			nextOrder := make([]FactID, len(w.insertionOrder), nextCapacity)
			copy(nextOrder, w.insertionOrder)
			w.insertionOrder = nextOrder
		}
	}
	w.reserveFactRowSequenceRows(1)
}

func (w *factWorkspace) reserveFactRowSequenceRows(factCount int) {
	if w == nil || factCount <= 0 {
		return
	}
	target, ok := factRowSequenceReserveTarget(w.sequence, factCount)
	if !ok || target <= len(w.factsBySequence) {
		return
	}
	w.factsBySequence = growFactRowSequenceIndex(w.factsBySequence, target)
}

func growFactRowSequenceIndex(index []int32, length int) []int32 {
	if length <= len(index) || length < 0 {
		return index
	}
	oldLen := len(index)
	if cap(index) < length {
		maxInt := int(^uint(0) >> 1)
		nextCapacity := cap(index)
		if nextCapacity == 0 {
			nextCapacity = 8
		} else if nextCapacity <= maxInt/2 {
			nextCapacity *= 2
		} else {
			nextCapacity = maxInt
		}
		if nextCapacity < length {
			nextCapacity = length
		}
		next := make([]int32, length, nextCapacity)
		copy(next, index)
		index = next
	} else {
		index = index[:length]
	}
	for i := oldLen; i < length; i++ {
		index[i] = missingFactRowIndex
	}
	return index
}

func factRowSequenceReserveTarget(sequence uint64, factCount int) (int, bool) {
	if factCount <= 0 {
		return 0, false
	}
	target := sequence + uint64(factCount)
	if target < sequence || target > uint64(int(^uint(0)>>1)) {
		return 0, false
	}
	return int(target), true
}

func (w *factWorkspace) storeFact(fact workingFact) *workingFact {
	if w == nil {
		return nil
	}
	if fact.payload != nil {
		return w.storeCompactFact(fact)
	}

	w.facts = append(w.facts, fact)
	stored := &w.facts[len(w.facts)-1]
	w.setFactRowIndex(stored.id, len(w.facts)-1)
	return stored
}

func (w *factWorkspace) storeCompactFact(fact workingFact) *workingFact {
	if w == nil {
		return nil
	}
	row := w.compactFacts.append(fact)
	if row < 0 {
		return nil
	}
	w.setFactRowIndex(fact.id, encodeCompactFactRow(row))
	stored, _ := w.compactFacts.fact(row)
	return stored
}

func (w *factWorkspace) reserveGeneratedFactSlots(revision *Ruleset, slotCount int) ([]factSlot, int) {
	if w == nil {
		return nil, 0
	}
	if slotCount <= 0 {
		return nil, len(w.slotStorage)
	}
	mark := len(w.slotStorage)
	w.reserveGeneratedFactInsert(revision, slotCount)
	end := mark + slotCount
	if cap(w.slotStorage) < end {
		w.reserveSlotStorage(end)
	}
	w.slotStorage = w.slotStorage[:end]
	slots := w.slotStorage[mark:end:end]
	return slots, mark
}

func (w *factWorkspace) reserveGeneratedCompactFactSlots(revision *Ruleset, slotCount int) ([]compactFactSlot, int) {
	if w == nil {
		return nil, 0
	}
	if w.compactSlotStore == nil {
		w.compactSlotStore = &factCompactSlotStore{}
	}
	if slotCount <= 0 {
		return nil, w.compactSlotStore.len()
	}
	w.reserveGeneratedCompactFactInsert(revision, slotCount)
	return w.compactSlotStore.reserveSlots(slotCount)
}

func (w *factWorkspace) rollbackGeneratedFactSlots(mark int) {
	if w == nil || mark < 0 || mark > len(w.slotStorage) {
		return
	}
	for i := mark; i < len(w.slotStorage); i++ {
		w.slotStorage[i] = factSlot{}
	}
	w.slotStorage = w.slotStorage[:mark]
}

func (w *factWorkspace) storeCompactFactSlots(fieldSlots []factSlot) (factCompactSlotRef, bool) {
	if w == nil {
		return factCompactSlotRef{}, false
	}
	if w.compactSlotStore == nil {
		w.compactSlotStore = &factCompactSlotStore{}
	}
	return w.compactSlotStore.appendFromFactSlots(fieldSlots)
}

func (w *factWorkspace) rollbackGeneratedCompactFactSlots(mark int) {
	if w == nil || w.compactSlotStore == nil {
		return
	}
	w.compactSlotStore.rollback(mark)
}

func (w *factWorkspace) markGeneratedFactInsert() factWorkspaceInsertMark {
	if w == nil {
		return factWorkspaceInsertMark{}
	}
	return factWorkspaceInsertMark{
		sequence:               w.sequence,
		recency:                w.recency,
		factsLen:               len(w.facts),
		compactFactsLen:        w.compactFacts.len(),
		insertionOrderLen:      len(w.insertionOrder),
		slotStorageLen:         len(w.slotStorage),
		compactSlotStoreLen:    w.compactSlotStore.len(),
		factTargetIndexesDirty: w.factTargetIndexesDirty,
	}
}

func (w *factWorkspace) rollbackGeneratedFactInsert(mark factWorkspaceInsertMark, fact *workingFact, revision *Ruleset) {
	if w == nil {
		return
	}
	if fact != nil {
		if duplicateIndex := fact.duplicateIndexForRevision(revision, w.compactSlotStore); !duplicateIndex.isZero() {
			w.factsByDuplicate.deleteFact(duplicateIndex, fact.id)
		}
		if !fact.targetIndexesSkipped {
			w.removeFactTargetIndexes(fact.templateKeyForRevision(nil), fact.storedName(), fact.id)
		}
		w.deleteFactRowIndex(fact.id)
	}
	w.sequence = mark.sequence
	w.recency = mark.recency
	w.factTargetIndexesDirty = mark.factTargetIndexesDirty
	if mark.insertionOrderLen >= 0 && mark.insertionOrderLen <= len(w.insertionOrder) {
		for i := mark.insertionOrderLen; i < len(w.insertionOrder); i++ {
			w.insertionOrder[i] = FactID{}
		}
		w.insertionOrder = w.insertionOrder[:mark.insertionOrderLen]
	}
	if mark.factsLen >= 0 && mark.factsLen <= len(w.facts) {
		for i := mark.factsLen; i < len(w.facts); i++ {
			w.facts[i] = workingFact{}
		}
		w.facts = w.facts[:mark.factsLen]
	}
	w.compactFacts.truncate(mark.compactFactsLen)
	w.rollbackGeneratedFactSlots(mark.slotStorageLen)
	w.rollbackGeneratedCompactFactSlots(mark.compactSlotStoreLen)
}

func (w *factWorkspace) markFactModify(fact *workingFact, currentDupIndex, newDupIndex duplicateIndexKey, trackDuplicateIndex bool) factWorkspaceModifyMark {
	if w == nil || fact == nil {
		return factWorkspaceModifyMark{factIndex: -1}
	}
	index := -1
	if found, ok := w.factRowIndex(fact.id); ok {
		index = found
	}
	mark := factWorkspaceModifyMark{
		recency:                w.recency,
		factTargetIndexesDirty: w.factTargetIndexesDirty,
		factIndex:              index,
		fact:                   *fact,
		compactSlotStoreLen:    w.compactSlotStore.len(),
	}
	mark.fact.payload = cloneWorkingFactPayload(fact.payload)
	if trackDuplicateIndex {
		undo := duplicateIndexModifyUndo{tracked: true, factID: fact.id, oldKey: currentDupIndex, newKey: newDupIndex}
		if !currentDupIndex.isZero() {
			undo.oldHeld = w.factsByDuplicate.holdsFact(currentDupIndex, fact.id)
		}
		if !newDupIndex.isZero() {
			switch newDupIndex.kind {
			case duplicateIndexStringStringInt, duplicateIndexStructural:
				undo.newHeld = w.factsByDuplicate.holdsFact(newDupIndex, fact.id)
			default:
				undo.newPrevID, undo.newPrevHad = w.factsByDuplicate.get(newDupIndex)
			}
		}
		mark.duplicateIndexUndo = undo
	}
	return mark
}

func (w *factWorkspace) rollbackFactModify(mark factWorkspaceModifyMark) {
	if w == nil {
		return
	}
	w.recency = mark.recency
	w.factTargetIndexesDirty = mark.factTargetIndexesDirty
	w.factsByDuplicate.applyModifyUndo(mark.duplicateIndexUndo)
	if mark.factIndex >= 0 && mark.factIndex < len(w.facts) {
		w.facts[mark.factIndex] = mark.fact
	} else if row, generated := decodeCompactFactRow(mark.factIndex); generated {
		w.compactFacts.replace(row, &mark.fact)
	}
	w.rollbackGeneratedCompactFactSlots(mark.compactSlotStoreLen)
}

func (w *factWorkspace) workingFactByID(id FactID) (*workingFact, bool) {
	if w == nil {
		return nil, false
	}
	index, ok := w.factRowIndex(id)
	if !ok {
		return nil, false
	}
	if row, generated := decodeCompactFactRow(index); generated {
		fact, ok := w.compactFacts.fact(row)
		if !ok || fact.id != id {
			return nil, false
		}
		return fact, true
	}
	if index < 0 || index >= len(w.facts) {
		return nil, false
	}
	fact := &w.facts[index]
	if fact.id != id {
		return nil, false
	}
	return fact, true
}

func (w *factWorkspace) replaceWorkingFact(fact *workingFact) bool {
	if w == nil || fact == nil {
		return false
	}
	index, ok := w.factRowIndex(fact.id)
	if !ok {
		return false
	}
	if row, generated := decodeCompactFactRow(index); generated {
		return w.compactFacts.replace(row, fact)
	}
	if index < 0 || index >= len(w.facts) || w.facts[index].id != fact.id {
		return false
	}
	w.facts[index] = *fact
	w.facts[index].payload = cloneWorkingFactPayload(fact.payload)
	return true
}

func (w *factWorkspace) factRowIndex(id FactID) (int, bool) {
	if w == nil || id.IsZero() {
		return 0, false
	}
	if index, ok := factRowSequenceIndex(id, w.generation); ok && index < len(w.factsBySequence) {
		row := w.factsBySequence[index]
		if row != missingFactRowIndex {
			return int(row), true
		}
	}
	if w.factsByID == nil {
		return 0, false
	}
	index, ok := w.factsByID[id]
	return index, ok
}

func (w *factWorkspace) setFactRowIndex(id FactID, row int) {
	if w == nil || id.IsZero() || row == int(missingFactRowIndex) {
		return
	}
	if row <= maxFactRowSequenceIndex {
		if index, ok := factRowSequenceIndex(id, w.generation); ok {
			if len(w.factsBySequence) <= index {
				w.factsBySequence = growFactRowSequenceIndex(w.factsBySequence, index+1)
			}
			w.factsBySequence[index] = int32(row)
			return
		}
	}
	if w.factsByID != nil {
		w.factsByID[id] = row
	}
}

func (w *factWorkspace) deleteFactRowIndex(id FactID) {
	if w == nil || id.IsZero() {
		return
	}
	if index, ok := factRowSequenceIndex(id, w.generation); ok && index < len(w.factsBySequence) {
		w.factsBySequence[index] = missingFactRowIndex
		return
	}
	if w.factsByID != nil {
		delete(w.factsByID, id)
	}
}

func (w *factWorkspace) structuralDuplicateFact(template compiledTemplate, slots []factSlot, key duplicateIndexKey) (*workingFact, bool) {
	if w == nil || key.kind != duplicateIndexStructural {
		return nil, false
	}
	var found *workingFact
	w.factsByDuplicate.forEachStructuralFactID(key, func(id FactID) bool {
		existing, ok := w.workingFactByID(id)
		if !ok {
			return true
		}
		if workingFactStructuralDuplicateSlotsEqual(template, slots, existing, w.compactSlotStore) {
			found = existing
			return false
		}
		return true
	})
	if found != nil {
		return found, true
	}
	return nil, false
}

func (w *factWorkspace) duplicateFactID(revision *Ruleset, key duplicateIndexKey) (FactID, bool) {
	if w == nil || key.isZero() {
		return FactID{}, false
	}
	if key.kind == duplicateIndexStringStringInt {
		var stale []FactID
		var found FactID
		w.factsByDuplicate.forEachStringStringIntFactID(key, func(id FactID) bool {
			existing, ok := w.workingFactByID(id)
			if !ok {
				stale = append(stale, id)
				return true
			}
			if existing.duplicateIndexForRevision(revision, w.compactSlotStore) == key {
				found = id
				return false
			}
			return true
		})
		for _, id := range stale {
			w.factsByDuplicate.deleteFact(key, id)
		}
		if !found.IsZero() {
			return found, true
		}
		return FactID{}, false
	}
	id, ok := w.factsByDuplicate.get(key)
	if !ok {
		return FactID{}, false
	}
	if _, exists := w.workingFactByID(id); exists {
		return id, true
	}
	w.factsByDuplicate.delete(key)
	return FactID{}, false
}

func (w *factWorkspace) structuralDuplicateFactWithPlan(plan *compiledGeneratedFactInsertPlan, slots []factSlot, key duplicateIndexKey) (*workingFact, bool) {
	if w == nil || plan == nil || key.kind != duplicateIndexStructural {
		return nil, false
	}
	var found *workingFact
	w.factsByDuplicate.forEachStructuralFactID(key, func(id FactID) bool {
		existing, ok := w.workingFactByID(id)
		if !ok {
			return true
		}
		if equal, ok := plan.structuralScalarDuplicateWorkingFactEqual(slots, existing, w.compactSlotStore); ok {
			if equal {
				found = existing
				return false
			}
			return true
		}
		if workingFactStructuralDuplicateSlotsEqual(plan.template, slots, existing, w.compactSlotStore) {
			found = existing
			return false
		}
		return true
	})
	if found != nil {
		return found, true
	}
	return nil, false
}

func (w *factWorkspace) reindexFactRowsFrom(start int) {
	if w == nil || start < 0 {
		return
	}
	for i := start; i < len(w.facts); i++ {
		w.setFactRowIndex(w.facts[i].id, i)
	}
}

func (w *factWorkspace) nextFactSequence() uint64 {
	if w == nil {
		return 0
	}
	return w.sequence
}

func (w *factWorkspace) factCount() int {
	if w == nil {
		return 0
	}
	return len(w.facts) + w.compactFacts.len()
}

func (w *factWorkspace) nextRecency() Recency {
	if w == nil {
		return 0
	}
	return w.recency
}

func (w *factWorkspace) factsByInsertionOrder() []FactID {
	if w == nil || w.insertionOrder == nil {
		return nil
	}
	return w.insertionOrder
}

func (w *factWorkspace) insertFact(revision *Ruleset, generation Generation, name string, templateKey TemplateKey, fields Fields) (*workingFact, DuplicateKey, bool, error) {
	template, templateExists := revision.templateByKey(templateKey)
	if templateKey != "" && !templateExists {
		return nil, "", false, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}

	if templateExists {
		if err := validatePublicTemplateMutation(template); err != nil {
			return nil, "", false, err
		}
		name = template.Name()
	}

	if templateExists && revision.usesFieldSlots(template) {
		fieldSlots, err := template.buildValidatedFieldSlots(fields)
		if err != nil {
			return nil, "", false, err
		}
		return w.insertFactSlots(revision, generation, template, fieldSlots, true)
	}

	canonical := normalizeFields(fields)
	var presence map[string]FieldPresence
	var err error
	if templateExists {
		canonical, presence, err = template.applyDefaultsAndValidate(canonical)
		if err != nil {
			return nil, "", false, err
		}
	} else {
		presence = make(map[string]FieldPresence, len(canonical))
		for field := range canonical {
			presence[field] = FieldPresenceExplicit
		}
	}

	templateDuplicatePolicy := template.duplicatePolicy
	fieldSlots := revision.buildFieldSlots(template, canonical, presence)
	duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, canonical, fieldSlots)

	if templateDuplicatePolicy != DuplicateAllow {
		if duplicateIndex.kind == duplicateIndexStructural {
			if existing, ok := w.structuralDuplicateFact(template, fieldSlots, duplicateIndex); ok {
				return existing, existing.publicDuplicateKey(revision, w.compactSlotStore), false, nil
			}
		} else {
			existingID, ok := w.duplicateFactID(revision, duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					return existing, existing.publicDuplicateKey(revision, w.compactSlotStore), false, nil
				}
				w.factsByDuplicate.deleteFact(duplicateIndex, existingID)
			}
		}
	}

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	duplicateKey := duplicateIndex.publicKeyForTemplate(name, template)
	if duplicateIndex.kind == duplicateIndexStructural {
		duplicateKey = makeDuplicateKeyForTemplateWithSlots(name, template, canonical, fieldSlots)
	}

	fact := workingFact{
		id:      id,
		version: 1,
		recency: w.recency,
	}
	if templateExists {
		fact.setTemplateIdentity(templateKey, template.id)
	} else {
		fact.setTemplateIdentity(templateKey, 0)
	}
	fact.setName(name)
	if name != "" {
		fact.setTemplateKey(templateKey)
	}
	fact.setFields(canonical)
	fact.setFieldSlots(fieldSlots)
	fact.setFieldPresence(presence)

	if len(fieldSlots) > 0 {
		fact.clearFields()
		fact.clearFieldPresence()
	}

	stored := w.storeFact(fact)
	if templateDuplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	w.addFactTargetIndexes(templateKey, name, id)
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, duplicateKey, true, nil
}

func (w *factWorkspace) insertFactSlots(revision *Ruleset, generation Generation, template compiledTemplate, fieldSlots []factSlot, materializeDuplicateKey bool) (*workingFact, DuplicateKey, bool, error) {
	name := template.Name()
	templateKey := template.Key()
	duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
	if template.duplicatePolicy != DuplicateAllow {
		if duplicateIndex.kind == duplicateIndexStructural {
			if existing, ok := w.structuralDuplicateFact(template, fieldSlots, duplicateIndex); ok {
				if materializeDuplicateKey {
					return existing, existing.publicDuplicateKey(revision, w.compactSlotStore), false, nil
				}
				return existing, "", false, nil
			}
		} else {
			existingID, ok := w.duplicateFactID(revision, duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					if materializeDuplicateKey {
						return existing, existing.publicDuplicateKey(revision, w.compactSlotStore), false, nil
					}
					return existing, "", false, nil
				}
				w.factsByDuplicate.deleteFact(duplicateIndex, existingID)
			}
		}
	}

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	var duplicateKey DuplicateKey
	if materializeDuplicateKey {
		duplicateKey = duplicateIndex.publicKeyForTemplate(name, template)
		if duplicateIndex.kind == duplicateIndexStructural {
			duplicateKey = makeDuplicateKeyForTemplateWithSlots(name, template, nil, fieldSlots)
		}
	}
	storedSlots := fieldSlots
	var compactSlots factCompactSlotRef
	if !materializeDuplicateKey && templateSupportsCompactGeneratedSlots(template) {
		if compact, ok := w.storeCompactFactSlots(fieldSlots); ok {
			storedSlots = nil
			compactSlots = compact
		} else {
			w.reserveGeneratedFactInsert(revision, len(fieldSlots))
			storedSlots = w.storeGeneratedFactSlots(fieldSlots)
		}
	}
	fact := workingFact{
		id:           id,
		version:      1,
		recency:      w.recency,
		compactSlots: compactSlots,
	}
	fact.setTemplateIdentity(templateKey, template.id)
	fact.setName(name)
	if name != "" {
		fact.setTemplateKey(templateKey)
	}
	fact.setFieldSlots(storedSlots)

	stored := w.storeFact(fact)
	if template.duplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	w.addFactTargetIndexes(templateKey, name, id)
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, duplicateKey, true, nil
}

func (w *factWorkspace) insertPreparedGeneratedFactSlots(revision *Ruleset, generation Generation, template compiledTemplate, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
	if err := validatePublicTemplateMutation(template); err != nil {
		w.rollbackGeneratedFactSlots(slotMark)
		return nil, "", false, err
	}
	plan, ok := revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	return w.insertPreparedGeneratedFactSlotsWithPlan(revision, generation, plan, fieldSlots, slotMark)
}

func (w *factWorkspace) insertPreparedEngineGeneratedFactSlots(revision *Ruleset, generation Generation, template compiledTemplate, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
	return w.insertPreparedGeneratedFactSlotsUnchecked(revision, generation, template, fieldSlots, slotMark, factTargetIndexSkip)
}

func (w *factWorkspace) insertPreparedGeneratedFactSlotsWithPlan(revision *Ruleset, generation Generation, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
	return w.insertPreparedGeneratedFactSlotsWithPlanUnchecked(revision, generation, plan, fieldSlots, slotMark, factTargetIndexDirty)
}

func (w *factWorkspace) insertPreparedGeneratedFactSlotsUnchecked(revision *Ruleset, generation Generation, template compiledTemplate, fieldSlots []factSlot, slotMark int, indexMode factTargetIndexMode) (*workingFact, DuplicateKey, bool, error) {
	plan, ok := revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	return w.insertPreparedGeneratedFactSlotsWithPlanUnchecked(revision, generation, plan, fieldSlots, slotMark, indexMode)
}

func (w *factWorkspace) insertPreparedGeneratedFactSlotsWithPlanUnchecked(revision *Ruleset, generation Generation, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, slotMark int, indexMode factTargetIndexMode) (*workingFact, DuplicateKey, bool, error) {
	name := plan.name
	storedName := name
	if !plan.storeName {
		storedName = ""
	}
	templateKey := plan.templateKey
	var duplicateIndex duplicateIndexKey
	if plan.duplicatePolicy != DuplicateAllow {
		duplicateIndex = plan.duplicateIndex(fieldSlots)
		if duplicateIndex.kind == duplicateIndexStructural {
			if existing, ok := w.structuralDuplicateFactWithPlan(plan, fieldSlots, duplicateIndex); ok {
				w.rollbackGeneratedFactSlots(slotMark)
				return existing, "", false, nil
			}
		} else {
			existingID, ok := w.duplicateFactID(revision, duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					w.rollbackGeneratedFactSlots(slotMark)
					return existing, "", false, nil
				}
				w.factsByDuplicate.deleteFact(duplicateIndex, existingID)
			}
		}
	}

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	var compactSlots factCompactSlotRef
	if plan.compactSlots {
		if compact, ok := w.storeCompactFactSlots(fieldSlots); ok {
			compactSlots = compact
			if len(compactSlots.slots(w.compactSlotStore)) > 0 {
				w.rollbackGeneratedFactSlots(slotMark)
				fieldSlots = nil
			}
		}
	}
	fact := workingFact{
		id:                   id,
		version:              1,
		recency:              w.recency,
		compactSlots:         compactSlots,
		targetIndexesSkipped: indexMode == factTargetIndexSkip,
	}
	fact.setTemplateIdentity(templateKey, plan.templateID)
	fact.setName(storedName)
	if storedName != "" {
		fact.setTemplateKey(templateKey)
	}
	fact.setFieldSlots(fieldSlots)

	var stored *workingFact
	if len(compactSlots.slots(w.compactSlotStore)) > 0 && len(fieldSlots) == 0 {
		stored = w.storeCompactFact(fact)
	} else {
		stored = w.storeFact(fact)
	}
	if plan.duplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	switch indexMode {
	case factTargetIndexEager:
		w.addFactTargetIndexes(templateKey, name, id)
	case factTargetIndexDirty:
		w.markFactTargetIndexesDirty()
	}
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, "", true, nil
}

func (w *factWorkspace) insertPreparedGeneratedCompactFactSlotsWithPlanUnchecked(revision *Ruleset, generation Generation, plan *compiledGeneratedFactInsertPlan, compactSlots []compactFactSlot, compactSlotMark int, indexMode factTargetIndexMode) (*workingFact, DuplicateKey, bool, error) {
	name := plan.name
	storedName := name
	if !plan.storeName {
		storedName = ""
	}
	templateKey := plan.templateKey
	var duplicateIndex duplicateIndexKey
	if plan.duplicatePolicy != DuplicateAllow {
		duplicateIndex = plan.duplicateIndexFromCompact(compactSlots)
		if duplicateIndex.kind == duplicateIndexStructural {
			materialized := materializeFactSlotsFromCompactSlots(compactSlots)
			if existing, ok := w.structuralDuplicateFactWithPlan(plan, materialized, duplicateIndex); ok {
				w.rollbackGeneratedCompactFactSlots(compactSlotMark)
				return existing, "", false, nil
			}
		} else {
			existingID, ok := w.duplicateFactID(revision, duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					w.rollbackGeneratedCompactFactSlots(compactSlotMark)
					return existing, "", false, nil
				}
				w.factsByDuplicate.deleteFact(duplicateIndex, existingID)
			}
		}
	}

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	if w.compactSlotStore == nil {
		w.compactSlotStore = &factCompactSlotStore{}
	}
	compactRef, ok := w.compactSlotStore.ref(compactSlotMark, compactSlots)
	if !ok {
		w.rollbackGeneratedCompactFactSlots(compactSlotMark)
		return nil, "", false, &ValidationError{TemplateName: plan.name, Reason: "generated compact fact slot range is invalid"}
	}
	fact := workingFact{
		id:                   id,
		version:              1,
		recency:              w.recency,
		compactSlots:         compactRef,
		targetIndexesSkipped: indexMode == factTargetIndexSkip,
	}
	fact.setTemplateIdentity(templateKey, plan.templateID)
	if storedName != "" {
		fact.setName(storedName)
		fact.setTemplateKey(templateKey)
	}

	stored := w.storeCompactFact(fact)
	if plan.duplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	switch indexMode {
	case factTargetIndexEager:
		w.addFactTargetIndexes(templateKey, name, id)
	case factTargetIndexDirty:
		w.markFactTargetIndexesDirty()
	}
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, "", true, nil
}

func (w *factWorkspace) storeGeneratedFactSlots(fieldSlots []factSlot) []factSlot {
	if len(fieldSlots) == 0 {
		return nil
	}
	if cap(w.slotStorage)-len(w.slotStorage) < len(fieldSlots) {
		nextCapacity := max(max(cap(w.slotStorage)*2, len(fieldSlots)), 16)
		w.slotStorage = make([]factSlot, 0, nextCapacity)
	}
	start := len(w.slotStorage)
	w.slotStorage = append(w.slotStorage, fieldSlots...)
	return w.slotStorage[start:len(w.slotStorage):len(w.slotStorage)]
}

type compiledSessionInitialFact struct {
	name            string
	templateKey     TemplateKey
	templateID      templateID
	fields          Fields
	fieldSlots      []factSlot
	compactSlots    []compactFactSlot
	fieldSpecs      []FieldSpec
	fieldPresence   map[string]FieldPresence
	duplicatePolicy DuplicatePolicy
	duplicateIndex  duplicateIndexKey
	duplicateKey    DuplicateKey
	shareFields     bool
	shareSlots      bool
}

type compiledSessionInitialStorage struct {
	broadFacts   int
	compactFacts int
	compactSlots int
}

func compiledSessionInitialStorageCounts(initials []compiledSessionInitialFact) compiledSessionInitialStorage {
	var out compiledSessionInitialStorage
	for _, initial := range initials {
		if len(initial.compactSlots) > 0 {
			out.compactFacts++
			out.compactSlots += len(initial.compactSlots)
			continue
		}
		out.broadFacts++
	}
	return out
}

func compileSessionInitialFacts(revision *Ruleset, initials []SessionInitialFact) ([]compiledSessionInitialFact, error) {
	if len(initials) == 0 {
		return nil, nil
	}

	compiled := make([]compiledSessionInitialFact, 0, len(initials))
	seen := make(map[duplicateIndexKey]struct{}, len(initials))
	for _, initial := range initials {
		next, err := compileSessionInitialFact(revision, initial)
		if err != nil {
			return nil, err
		}
		if next.duplicatePolicy != DuplicateAllow {
			if _, ok := seen[next.duplicateIndex]; ok {
				continue
			}
			seen[next.duplicateIndex] = struct{}{}
		}
		compiled = append(compiled, next)
	}
	return compiled, nil
}

func (s *Session) compiledResetInitials() ([]compiledSessionInitialFact, error) {
	if s == nil {
		return nil, ErrClosedSession
	}
	if len(s.initials) == s.initialCount {
		return s.compiledInitials, nil
	}
	compiled, err := compileSessionInitialFacts(s.revision, s.initials)
	if err != nil {
		return nil, err
	}
	s.initialCount = len(s.initials)
	s.compiledInitials = compiled
	return compiled, nil
}

func compileSessionInitialFact(revision *Ruleset, initial SessionInitialFact) (compiledSessionInitialFact, error) {
	if initial.TemplateKey == "" && initial.name == "" {
		return compiledSessionInitialFact{}, &ValidationError{TemplateName: "session", Reason: "initializer must set a template key"}
	}
	if initial.TemplateKey != "" && initial.name != "" {
		return compiledSessionInitialFact{}, &ValidationError{TemplateName: initial.name, Reason: "initializer must not set both name and template key"}
	}

	name := initial.name
	templateKey := initial.TemplateKey
	template, templateExists := revision.templateByKey(templateKey)
	if templateKey != "" && !templateExists {
		return compiledSessionInitialFact{}, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}
	if templateExists {
		if err := validatePublicTemplateMutation(template); err != nil {
			return compiledSessionInitialFact{}, err
		}
		name = template.Name()
	}

	if templateExists && (revision.usesFieldSlots(template) || templateSupportsCompactGeneratedSlots(template)) {
		fieldSlots, err := template.buildValidatedFieldSlots(initial.Fields)
		if err != nil {
			return compiledSessionInitialFact{}, err
		}

		duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
		duplicateKey := duplicateIndex.publicKeyForTemplate(name, template)
		if duplicateIndex.kind == duplicateIndexStructural {
			duplicateKey = makeDuplicateKeyForTemplateWithSlots(name, template, nil, fieldSlots)
		}
		if compactSlots, ok := compactFactSlotsFromFactSlots(fieldSlots); ok && len(compactSlots) > 0 {
			plan := newCompiledGeneratedFactInsertPlan(template)
			duplicateIndex = plan.duplicateIndexFromCompact(compactSlots)
			duplicateKey = duplicateIndex.publicKeyForTemplate(name, template)
			if duplicateIndex.kind == duplicateIndexStructural {
				duplicateKey = makeDuplicateKeyForTemplateWithSlots(name, template, nil, materializeFactSlotsFromCompactSlots(compactSlots))
			}
			return compiledSessionInitialFact{
				name:            name,
				templateKey:     templateKey,
				templateID:      template.id,
				compactSlots:    compactSlots,
				fieldSpecs:      template.fields,
				duplicatePolicy: template.duplicatePolicy,
				duplicateIndex:  duplicateIndex,
				duplicateKey:    duplicateKey,
			}, nil
		}
		return compiledSessionInitialFact{
			name:            name,
			templateKey:     templateKey,
			templateID:      template.id,
			fieldSlots:      fieldSlots,
			fieldSpecs:      template.fields,
			duplicatePolicy: template.duplicatePolicy,
			duplicateIndex:  duplicateIndex,
			duplicateKey:    duplicateKey,
			shareSlots:      factSlotsShareable(fieldSlots),
		}, nil
	}

	fields := normalizeFields(initial.Fields)
	var presence map[string]FieldPresence
	var err error
	if templateExists {
		fields, presence, err = template.applyDefaultsAndValidate(fields)
		if err != nil {
			return compiledSessionInitialFact{}, err
		}
	} else {
		presence = make(map[string]FieldPresence, len(fields))
		for field := range fields {
			presence[field] = FieldPresenceExplicit
		}
	}

	duplicatePolicy := template.duplicatePolicy
	fieldSlots := revision.buildFieldSlots(template, fields, presence)
	duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, fields, fieldSlots)
	duplicateKey := duplicateIndex.publicKeyForTemplate(name, template)
	if duplicateIndex.kind == duplicateIndexStructural {
		duplicateKey = makeDuplicateKeyForTemplateWithSlots(name, template, fields, fieldSlots)
	}
	var fieldSpecs []FieldSpec
	if len(fieldSlots) > 0 {
		fields = nil
		presence = nil
		fieldSpecs = template.fields
	}

	return compiledSessionInitialFact{
		name:            name,
		templateKey:     templateKey,
		templateID:      template.id,
		fields:          fields,
		fieldSlots:      fieldSlots,
		fieldSpecs:      fieldSpecs,
		fieldPresence:   presence,
		duplicatePolicy: duplicatePolicy,
		duplicateIndex:  duplicateIndex,
		duplicateKey:    duplicateKey,
		shareFields:     fieldsShareable(fields),
		shareSlots:      factSlotsShareable(fieldSlots),
	}, nil
}

func (w *factWorkspace) applyCompiledInitialFacts(initials []compiledSessionInitialFact) {
	for _, initial := range initials {
		w.insertCompiledInitialFact(initial)
	}
}

func (w *factWorkspace) reserveCompiledInitialFactStorage(storage compiledSessionInitialStorage) {
	if w == nil {
		return
	}
	totalFacts := saturatingAddInt(storage.broadFacts, storage.compactFacts)
	if totalFacts > 0 {
		w.reserveFactRowSequenceRows(totalFacts)
		if cap(w.insertionOrder) < totalFacts {
			nextOrder := make([]FactID, len(w.insertionOrder), totalFacts)
			copy(nextOrder, w.insertionOrder)
			w.insertionOrder = nextOrder
		}
	}
	if storage.compactFacts > 0 {
		w.compactFacts.reserve(saturatingAddInt(w.compactFacts.len(), storage.compactFacts))
	}
	if storage.compactSlots > 0 {
		w.reserveCompactSlotStorage(saturatingAddInt(w.compactSlotStore.len(), storage.compactSlots))
	}
}

func (w *factWorkspace) applyCompiledInitialFactsInto(initials []compiledSessionInitialFact, dst []FactSnapshot, revision *Ruleset) []FactSnapshot {
	if cap(dst) < len(initials) {
		dst = make([]FactSnapshot, 0, len(initials))
	} else {
		dst = dst[:0]
	}
	for _, initial := range initials {
		fact := w.insertCompiledInitialFact(initial)
		if fact != nil {
			dst = append(dst, fact.detachedSnapshotForRevision(revision, w.compactSlotStore))
		}
	}
	return dst
}

func (w *factWorkspace) insertCompiledInitialFact(initial compiledSessionInitialFact) *workingFact {
	w.sequence++
	w.recency++
	id := newFactID(w.generation, w.sequence)
	fact := workingFact{
		id:      id,
		version: 1,
		recency: w.recency,
	}
	fact.setTemplateIdentity(initial.templateKey, initial.templateID)
	if initial.templateID == 0 {
		fact.setName(initial.name)
		fact.setTemplateKey(initial.templateKey)
	}
	if initial.shareFields {
		fact.setFields(initial.fields)
	} else {
		fact.setFields(cloneFields(initial.fields))
	}
	if initial.shareSlots {
		fact.setFieldSlots(initial.fieldSlots)
	} else {
		fact.setFieldSlots(cloneFactSlots(initial.fieldSlots))
	}
	fact.setFieldPresence(cloneFieldPresence(initial.fieldPresence))

	if len(fact.fieldSlotSlice()) > 0 {
		fact.clearFields()
		fact.clearFieldPresence()
	}

	var stored *workingFact
	if len(initial.compactSlots) > 0 {
		if w.compactSlotStore == nil {
			w.compactSlotStore = &factCompactSlotStore{}
		}
		compactRef, ok := w.compactSlotStore.appendCompactSlots(initial.compactSlots)
		if ok {
			fact.clearPayload()
			fact.compactSlots = compactRef
			stored = w.storeCompactFact(fact)
		}
	}
	if stored == nil {
		stored = w.storeFact(fact)
	}
	if initial.duplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(initial.duplicateIndex, id)
	}
	w.addFactTargetIndexes(initial.templateKey, initial.name, id)
	w.insertionOrder = append(w.insertionOrder, id)
	return stored
}

func (s *Session) runGuardHeld() bool {
	if s == nil || s.runGuard == nil {
		return false
	}
	select {
	case s.runGuard <- struct{}{}:
		<-s.runGuard
		return false
	default:
		return true
	}
}

func (s *Session) canMutateDuringRun(origin mutationOrigin) bool {
	if s == nil || origin.isZero() {
		return false
	}
	if !s.runActive.Load() {
		return false
	}
	activation := s.runActivation.Load()
	return origin.matchesActivation(activation)
}

func (s *Session) shouldQueueMutationDuringRun(origin mutationOrigin) bool {
	if s == nil || s.listenerDispatchActive.Load() || !origin.isZero() || !s.runGuardHeld() {
		return false
	}
	return true
}

func (s *Session) enqueueMutationDuringRun(req queuedMutation) bool {
	if s == nil {
		return false
	}
	s.mutationQueueMu.Lock()
	defer s.mutationQueueMu.Unlock()
	if !s.runGuardHeld() {
		return false
	}
	s.mutationQueue = append(s.mutationQueue, req)
	return true
}

func (s *Session) popQueuedMutations() []queuedMutation {
	if s == nil {
		return nil
	}
	s.mutationQueueMu.Lock()
	if len(s.mutationQueue) == 0 {
		s.mutationQueueMu.Unlock()
		return nil
	}
	out := make([]queuedMutation, len(s.mutationQueue))
	copy(out, s.mutationQueue)
	s.mutationQueue = nil
	s.mutationQueueMu.Unlock()
	return out
}

func (s *Session) failQueuedMutations(err error) {
	if s == nil {
		return
	}
	for _, req := range s.popQueuedMutations() {
		if req.result == nil {
			continue
		}
		req.result <- queuedMutationResult{err: err}
	}
}

func (s *Session) markAgendaDirty() {
	if s != nil {
		s.clearRunAgendaDelta()
		s.agendaDriver.markDirty()
	}
}

func (s *Session) recordRunAgendaDelta(delta reteAgendaDelta) error {
	if s == nil {
		return nil
	}
	delta = s.assignRunAgendaBirthEpochs(delta)
	if !delta.supported {
		if s.propagation.counters != nil {
			s.propagation.counters.recordUnsupportedAgendaDelta()
		}
		return fmt.Errorf("%w: unsupported agenda delta during run", ErrUnsupportedRuntime)
	}
	if s.agendaDriver.dirty {
		return fmt.Errorf("%w: cannot record run agenda delta while agenda is dirty", ErrUnsupportedRuntime)
	}
	if s.canApplyRunAgendaDeltaDirect(delta) {
		return s.applyRunAgendaDeltaDirect(delta)
	}
	total := len(delta.added) + len(delta.removed) + len(delta.updated)
	if !s.propagation.runAgendaPending {
		s.propagation.runAgendaDeltas = s.propagation.runAgendaDeltas[:0]
		if s.propagation.runAgendaBuckets == nil {
			s.propagation.runAgendaBuckets = make(map[candidateIdentity]int, total)
		} else {
			clear(s.propagation.runAgendaBuckets)
		}
		for i := range s.propagation.runAgendaStates {
			s.propagation.runAgendaStates[i] = runAgendaDeltaState{}
		}
		s.propagation.runAgendaStates = slices.Grow(s.propagation.runAgendaStates[:0], total)
		s.propagation.runAgendaPending = true
	} else if s.propagation.runAgendaBuckets == nil {
		s.propagation.runAgendaBuckets = make(map[candidateIdentity]int, total)
	}
	if err := s.recordRunAgendaDeltaTokens(delta); err != nil {
		s.markAgendaDirty()
		return err
	}
	return nil
}

func (s *Session) assignRunAgendaBirthEpochs(delta reteAgendaDelta) reteAgendaDelta {
	if s == nil || s.agendaDriver.strategy != StrategyBreadth || len(delta.added) == 0 {
		return delta
	}
	needsEpoch := false
	for i := range delta.added {
		if delta.added[i].birthEpoch == 0 {
			needsEpoch = true
			break
		}
	}
	if !needsEpoch {
		return delta
	}
	agenda := s.agendaDriver.ensureAgenda()
	epoch := agenda.reserveBirthEpoch()
	for i := range delta.added {
		if delta.added[i].birthEpoch == 0 {
			delta.added[i].birthEpoch = epoch
		}
	}
	return delta
}

func (s *Session) canApplyRunAgendaDeltaDirect(delta reteAgendaDelta) bool {
	if s == nil || !delta.supported || s.agendaDriver.dirty || !s.agendaDriver.ready {
		return false
	}
	if s.revision == nil || s.revision.hasAutoFocusRules() || s.hasAgendaEventListeners() {
		return false
	}
	if s.propagation.runAgendaPending && !s.propagation.runAgendaDirect {
		return false
	}
	return true
}

func (s *Session) applyRunAgendaDeltaDirect(delta reteAgendaDelta) error {
	if s == nil {
		return nil
	}
	if ok, err := s.applyReteAgendaDeltaDirect(context.Background(), delta); err != nil {
		s.markAgendaDirty()
		return err
	} else if !ok {
		return fmt.Errorf("%w: unsupported direct agenda delta during run", ErrUnsupportedRuntime)
	}
	s.propagation.runAgendaPending = true
	s.propagation.runAgendaDirect = true
	return nil
}

func (s *Session) applyReteAgendaDeltaDirect(ctx context.Context, delta reteAgendaDelta) (bool, error) {
	if s == nil || s.revision == nil {
		return true, ErrInvalidRuleset
	}
	if s.agendaDriver.agenda == nil {
		s.agendaDriver.ensureAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if !delta.supported || s.propagation.runtime == nil || !s.agendaDriver.ready || s.agendaDriver.dirty {
		if s.propagation.counters != nil && !delta.supported {
			s.propagation.counters.recordUnsupportedAgendaDelta()
		}
		return false, nil
	}
	if len(delta.updated) != 0 {
		if err := s.agendaDriver.agenda.applyTerminalTokenUpdates(ctx, s.revision, delta.updated); err != nil {
			return true, err
		}
	}
	if err := s.applyTerminalTokenDeltasWithoutChangesAndAttach(ctx, delta.removed, delta.added); err != nil {
		return true, err
	}
	if s.propagation.counters != nil {
		s.propagation.counters.recordAgendaDeltaApplication()
	}
	s.agendaDriver.markReady()
	return true, nil
}

func (s *Session) applyTerminalTokenDeltasWithoutChangesAndAttach(ctx context.Context, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta) error {
	if s == nil || s.agendaDriver.agenda == nil || s.revision == nil {
		return ErrInvalidRuleset
	}
	if len(removed) <= 1 && len(added) <= 1 {
		act, err := s.agendaDriver.agenda.applySingleTerminalTokenDeltasWithoutChanges(ctx, s.revision, removed, added)
		if err != nil {
			return err
		}
		if len(added) == 1 {
			s.applyAutoFocusForActivation(act)
		}
		return nil
	}
	_, err := s.agendaDriver.agenda.applyTerminalTokenDeltasInternal(ctx, s.revision, removed, added, false, s.applyAutoFocusForActivation)
	return err
}

func (s *Session) applyAutoFocusForActivation(act *activation) {
	if s == nil || s.revision == nil || !s.revision.hasAutoFocusRules() || s.hasAgendaEventListeners() {
		return
	}
	if act == nil || act.status != activationStatusPending {
		return
	}
	rule, ok := s.revision.rulesByRevisionID[act.ruleRevisionID]
	if !ok || !rule.effectiveAutoFocus {
		return
	}
	s.pushFocusInternal(rule.module)
}

func (s *Session) recordRunAgendaDeltaTokens(delta reteAgendaDelta) error {
	for _, update := range delta.updated {
		if err := s.recordCoalescedRunAgendaTokenUpdate(update); err != nil {
			return err
		}
	}
	for _, token := range delta.removed {
		if err := s.recordCoalescedRunAgendaToken(token, false); err != nil {
			return err
		}
	}
	for _, token := range delta.added {
		if err := s.recordCoalescedRunAgendaToken(token, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) recordCoalescedRunAgendaTokenUpdate(update reteTerminalTokenUpdate) error {
	if s == nil || update.before.isZero() || update.after.isZero() {
		return nil
	}
	rule, ok := s.revision.rulesByRevisionID[update.ruleRevisionID]
	if !ok {
		return fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, update.ruleRevisionID)
	}
	identity := update.identity
	if identity.isZero() {
		identity = candidateIdentityForTerminalToken(rule, update.before)
	}
	for index := s.propagation.runAgendaBuckets[identity]; index != 0; {
		state := &s.propagation.runAgendaStates[index-1]
		if terminalTokenDeltasEqual(s.revision, state.token, reteTerminalTokenDelta{
			ruleRevisionID: update.ruleRevisionID,
			token:          update.before,
			identity:       identity,
		}) {
			if state.present {
				birthEpoch := state.token.birthEpoch
				if state.initial && !state.updated {
					state.updateBefore = state.token.token
					state.updated = true
				}
				state.token = reteTerminalTokenDelta{
					ruleRevisionID: update.ruleRevisionID,
					token:          update.after,
					identity:       identity,
					birthEpoch:     birthEpoch,
				}
			}
			return nil
		}
		index = state.next
	}
	if existing, _, ok := s.agendaDriver.agenda.activationForTerminalTokenIdentity(rule, update.before, identity); ok && existing.status == activationStatusPending {
		state := runAgendaDeltaState{
			initial:      true,
			present:      true,
			updated:      true,
			updateBefore: update.before,
			token: reteTerminalTokenDelta{
				ruleRevisionID: update.ruleRevisionID,
				token:          update.after,
				identity:       identity,
			},
			next: s.propagation.runAgendaBuckets[identity],
		}
		s.propagation.runAgendaStates = append(s.propagation.runAgendaStates, state)
		s.propagation.runAgendaBuckets[identity] = len(s.propagation.runAgendaStates)
	}
	return nil
}

func (s *Session) reconcileRunAgendaDelta(ctx context.Context) error {
	if s == nil || !s.propagation.runAgendaPending {
		return nil
	}
	if s.propagation.runAgendaDirect {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			s.markAgendaDirty()
			return err
		}
		s.clearRunAgendaDelta()
		return nil
	}
	delta, err := s.coalesceRunAgendaDeltas()
	if err != nil {
		s.clearRunAgendaDelta()
		s.markAgendaDirty()
		return err
	}
	if _, ok, err := s.applyReteAgendaDeltaInternal(ctx, delta, s.shouldCollectAgendaChanges()); err != nil {
		s.clearRunAgendaDelta()
		s.markAgendaDirty()
		return err
	} else if !ok {
		s.clearRunAgendaDelta()
		return fmt.Errorf("%w: unsupported coalesced agenda delta during run", ErrUnsupportedRuntime)
	}
	s.clearRunAgendaDelta()
	return nil
}

// releaseTransientAgendaDeltas recycles graph-beta delta arena storage between
// fire iterations, after the pending run delta has been fully applied.
func (s *Session) releaseTransientAgendaDeltas() {
	if s == nil {
		return
	}
	s.propagation.releaseTransientAgendaDeltas()
}

func (s *Session) abandonRunAgendaDelta() {
	if s == nil || !s.propagation.runAgendaPending {
		return
	}
	s.markAgendaDirty()
}

func (s *Session) clearRunAgendaDelta() {
	if s == nil {
		return
	}
	s.propagation.clearRunAgendaDelta()
}

type runAgendaDeltaState struct {
	initial      bool
	present      bool
	updated      bool
	reactivated  bool
	token        reteTerminalTokenDelta
	removedToken reteTerminalTokenDelta
	updateBefore tokenRef
	next         int
}

func (s *Session) coalesceRunAgendaDeltas() (reteAgendaDelta, error) {
	if s == nil || !s.propagation.runAgendaPending {
		return reteAgendaDelta{}, nil
	}
	if s.revision == nil {
		return reteAgendaDelta{}, ErrInvalidRuleset
	}

	total := len(s.propagation.runAgendaStates)
	added := slices.Grow(s.propagation.runAgendaAdded[:0], total)
	removed := slices.Grow(s.propagation.runAgendaRemoved[:0], total)
	updated := slices.Grow(s.propagation.runAgendaUpdated[:0], total)
	for i := range s.propagation.runAgendaStates {
		state := &s.propagation.runAgendaStates[i]
		if state.present == state.initial {
			if s.agendaDriver.strategy == StrategyBreadth && state.present && state.reactivated {
				if !state.removedToken.token.isZero() {
					removed = append(removed, state.removedToken)
				}
				if !state.token.token.isZero() {
					added = append(added, state.token)
				}
				continue
			}
			if state.present && state.updated && !state.updateBefore.isZero() && !state.token.token.isZero() && state.updateBefore != state.token.token {
				updated = append(updated, reteTerminalTokenUpdate{
					ruleRevisionID: state.token.ruleRevisionID,
					before:         state.updateBefore,
					after:          state.token.token,
					identity:       state.token.identity,
				})
			}
			continue
		}
		if state.present {
			added = append(added, state.token)
			continue
		}
		if !state.removedToken.token.isZero() {
			removed = append(removed, state.removedToken)
			continue
		}
		removed = append(removed, state.token)
	}
	s.propagation.runAgendaAdded = added
	s.propagation.runAgendaRemoved = removed
	s.propagation.runAgendaUpdated = updated
	return reteAgendaDelta{
		supported: true,
		added:     added,
		removed:   removed,
		updated:   updated,
	}, nil
}

func (s *Session) recordCoalescedRunAgendaToken(token reteTerminalTokenDelta, present bool) error {
	if s == nil || token.token.isZero() {
		return nil
	}
	rule, ok := s.revision.rulesByRevisionID[token.ruleRevisionID]
	if !ok {
		return fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, token.ruleRevisionID)
	}
	identity := candidateIdentityForTerminalTokenDelta(s.revision, token)
	for index := s.propagation.runAgendaBuckets[identity]; index != 0; {
		state := &s.propagation.runAgendaStates[index-1]
		if terminalTokenDeltasEqual(s.revision, state.token, token) {
			wasPresent := state.present
			if s.agendaDriver.strategy == StrategyBreadth && !present && wasPresent {
				state.removedToken = state.token
			}
			if s.agendaDriver.strategy == StrategyBreadth && present && !wasPresent && (state.initial || !state.removedToken.token.isZero()) {
				state.reactivated = true
			}
			state.present = present
			state.token = token
			if s.agendaDriver.strategy == StrategyBreadth && !present && state.removedToken.token.isZero() {
				state.removedToken = token
			}
			return nil
		}
		index = state.next
	}
	existing, _, ok := s.agendaDriver.agenda.activationForTerminalTokenIdentity(rule, token.token, identity)
	state := runAgendaDeltaState{
		initial: ok && existing.status == activationStatusPending,
		present: ok && existing.status == activationStatusPending,
		token:   token,
		next:    s.propagation.runAgendaBuckets[identity],
	}
	state.present = present
	if s.agendaDriver.strategy == StrategyBreadth && !present {
		state.removedToken = token
	}
	s.propagation.runAgendaStates = append(s.propagation.runAgendaStates, state)
	s.propagation.runAgendaBuckets[identity] = len(s.propagation.runAgendaStates)
	return nil
}

func (s *Session) drainQueuedMutations(ctx context.Context) error {
	for {
		requests := s.popQueuedMutations()
		if len(requests) == 0 {
			return nil
		}
		for i, req := range requests {
			if req.result == nil {
				continue
			}
			if req.ctx != nil {
				if err := req.ctx.Err(); err != nil {
					req.result <- queuedMutationResult{err: err}
					continue
				}
			}
			if err := ctx.Err(); err != nil {
				req.result <- queuedMutationResult{err: err}
				for j := i + 1; j < len(requests); j++ {
					remaining := requests[j]
					if remaining.result == nil {
						continue
					}
					if remaining.ctx != nil {
						if remainingErr := remaining.ctx.Err(); remainingErr != nil {
							remaining.result <- queuedMutationResult{err: remainingErr}
							continue
						}
					}
					remaining.result <- queuedMutationResult{err: err}
				}
				return err
			}
			if !s.beginMutation() {
				err := ErrConcurrencyMisuse
				req.result <- queuedMutationResult{err: err}
				for j := i + 1; j < len(requests); j++ {
					remaining := requests[j]
					if remaining.result == nil {
						continue
					}
					if remaining.ctx != nil {
						if remainingErr := remaining.ctx.Err(); remainingErr != nil {
							remaining.result <- queuedMutationResult{err: remainingErr}
							continue
						}
					}
					remaining.result <- queuedMutationResult{err: err}
				}
				return err
			}
			mutationCtx := ctx
			if req.ctx != nil {
				mutationCtx = req.ctx
			}
			value, agendaDelta, err := req.apply(mutationCtx)
			s.endMutation()
			if err == nil && mutationResultNeedsReconcile(value, s.revision) {
				if _, reconcileErr := s.reconcileAgendaAfterMutation(ctx, agendaDelta); reconcileErr != nil {
					err = reconcileErr
				}
			}
			req.result <- queuedMutationResult{value: value, err: err}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
		}
	}
}

func mutationResultNeedsReconcile(value any, revision *Ruleset) bool {
	switch result := value.(type) {
	case AssertResult:
		return (result.Status == AssertInserted || result.Status == AssertReplaced) && revision.factMayAffectRuleMatches(result.Fact)
	case ModifyResult:
		return result.Status == ModifyChanged && revision.factMayAffectRuleMatches(result.Fact)
	case RetractResult:
		return result.Status == RetractRemoved && revision.factMayAffectRuleMatches(result.Fact)
	case ResetResult:
		return result.Status == ResetApplied
	case ApplyRulesetResult:
		return false
	case focusMutationResult, ModuleName:
		return false
	default:
		return true
	}
}

func (s *Session) beginMutationForOrigin(origin mutationOrigin) (bool, bool) {
	if s == nil {
		return false, false
	}
	if s.canMutateDuringRun(origin) {
		return false, true
	}
	if s.runGuardHeld() {
		return false, false
	}
	if !s.beginMutation() {
		return false, false
	}
	return true, true
}

func (s *Session) beginMutation() bool {
	if s == nil {
		return false
	}
	select {
	case s.mu.mutate <- struct{}{}:
		select {
		case s.mu.lock <- struct{}{}:
			return true
		default:
			<-s.mu.mutate
			return false
		}
	default:
		return false
	}
}

func (s *Session) endMutation() {
	if s == nil {
		return
	}
	select {
	case <-s.mu.lock:
	default:
	}
	select {
	case <-s.mu.mutate:
	default:
	}
}

func (s *Session) lock() bool {
	select {
	case s.mu.lock <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Session) unlock() {
	if s == nil {
		return
	}
	select {
	case <-s.mu.lock:
	default:
	}
}

func (s *Session) emitEvent(ctx context.Context, event Event) {
	if s == nil {
		return
	}
	wasActive := s.listenerDispatchActive.Swap(true)
	defer s.listenerDispatchActive.Store(wasActive)
	s.diagnostics.emit(ctx, event)
}

func (s *Session) hasEventListenersFor(eventType EventType) bool {
	if s == nil {
		return false
	}
	return s.diagnostics.hasListenersFor(eventType)
}

func (s *Session) hasAgendaEventListeners() bool {
	return s.hasEventListenersFor(EventRuleActivated) || s.hasEventListenersFor(EventRuleDeactivated)
}

func (s *Session) factByID(id FactID) (FactSnapshot, bool) {
	if s == nil {
		return FactSnapshot{}, false
	}
	if id.Generation() != s.factStore.generation {
		return FactSnapshot{}, false
	}
	fact, ok := s.workingFactByID(id)
	if !ok {
		return FactSnapshot{}, false
	}
	return fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore), true
}

func (s *Session) factIDsByName(name string) []FactID {
	s.ensureFactTargetIndexes()
	ids := s.factStore.factsByName[name]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDsByTemplate(templateKey TemplateKey) []FactID {
	s.ensureFactTargetIndexes()
	ids := s.factStore.factsByTemplate[templateKey]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDForDuplicateKey(key DuplicateKey) (FactID, bool) {
	for _, id := range s.factStore.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			continue
		}
		if fact.publicDuplicateKey(s.revision, s.factStore.compactSlotStore) == key {
			return fact.id, true
		}
	}
	return FactID{}, false
}

func (s *Session) factCount() int {
	if s == nil {
		return 0
	}
	return len(s.factStore.facts) + s.factStore.compactFacts.len()
}

func (s *Session) activeFactWorkspace() factWorkspace {
	return s.factStore.workspace()
}

func (s *Session) clonedFactWorkspace() factWorkspace {
	return s.factStore.clonedWorkspace()
}

func (s *Session) commitFactWorkspace(state factWorkspace) {
	if s == nil {
		return
	}
	s.factStore.commit(state)
}

func (s *Session) swapFactWorkspace(workspace *factWorkspace) {
	if s == nil || workspace == nil {
		return
	}
	s.factStore.swap(workspace)
}

func (s *Session) reserveRunGeneratedFactStorage() {
	if s == nil || s.revision == nil || s.agendaDriver.agenda == nil {
		return
	}
	stats := s.revision.generatedAssertReserveByRuleRevision()
	if len(stats) == 0 {
		return
	}
	var factCount, slotCount, compactSlotCount int
	s.agendaDriver.agenda.forEachPendingActivation(func(current *activation) bool {
		if current == nil {
			return true
		}
		reserves := stats[current.ruleRevisionID]
		if len(reserves) == 0 {
			return true
		}
		for _, reserve := range reserves {
			factCount = saturatingAddInt(factCount, reserve.facts)
			slotCount = saturatingAddInt(slotCount, reserve.slots)
			compactSlotCount = saturatingAddInt(compactSlotCount, reserve.compactSlots)
			maximum := maxIntValue()
			if factCount >= maximum || slotCount >= maximum || compactSlotCount >= maximum {
				return false
			}
		}
		return true
	})
	if factCount == 0 && slotCount == 0 && compactSlotCount == 0 {
		return
	}
	state := s.activeFactWorkspace()
	state.reserveGeneratedFactCapacity(s.revision, factCount, slotCount, compactSlotCount)
	s.factStore.facts = state.facts
	s.factStore.compactFacts = state.compactFacts
	s.factStore.insertionOrder = state.insertionOrder
	s.factStore.factsBySequence = state.factsBySequence
	s.factStore.factsByTemplate = state.factsByTemplate
	s.factStore.factsByName = state.factsByName
	s.factStore.slotStorage = state.slotStorage
	s.factStore.compactSlotStore = state.compactSlotStore
}

func saturatingAddInt(left, right int) int {
	if right <= 0 {
		return left
	}
	maximum := maxIntValue()
	if left > maximum-right {
		return maximum
	}
	return left + right
}

func maxIntValue() int {
	return int(^uint(0) >> 1)
}

func (s *Session) resetWorkingMemory() {
	s.factStore.generation++
	s.factStore.nextFactSequence = 0
	s.factStore.nextRecency = 0
	s.factStore.facts = nil
	s.factStore.compactFacts = compactFactStore{}
	s.factStore.factsByID = nil
	s.factStore.factsBySequence = nil
	s.factStore.factsByDuplicate = duplicateIndexes{}
	s.factStore.factsByDuplicate.reset(0)
	s.factStore.factsByTemplate = make(map[TemplateKey][]FactID)
	s.factStore.factsByName = make(map[string][]FactID)
	s.factStore.factTargetIndexesDirty = false
	s.factStore.insertionOrder = nil
	s.factStore.slotStorage = nil
	s.factStore.compactSlotStore = nil
}

func cloneWorkingFacts(in []workingFact) []workingFact {
	if len(in) == 0 {
		return nil
	}
	out := make([]workingFact, len(in), cap(in))
	copy(out, in)
	for i := range out {
		out[i].payload = cloneWorkingFactPayload(out[i].payload)
	}
	return out
}

func cloneFactIDIndex(in map[FactID]int) map[FactID]int {
	if in == nil {
		return nil
	}
	out := make(map[FactID]int, len(in))
	maps.Copy(out, in)
	return out
}

func resetFactRowSequenceIndex(index []int32, capacity int) []int32 {
	if capacity < 0 {
		capacity = 0
	}
	if cap(index) < capacity {
		return make([]int32, 0, capacity)
	}
	return index[:0]
}

func cloneFactRowSequenceIndex(in []int32) []int32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int32, len(in), cap(in))
	copy(out, in)
	return out
}

func cloneFactIDSliceMap(in map[TemplateKey][]FactID) map[TemplateKey][]FactID {
	if in == nil {
		return nil
	}
	out := make(map[TemplateKey][]FactID, len(in))
	for key, ids := range in {
		out[key] = cloneFactIDs(ids)
	}
	return out
}

func cloneStringFactIDSliceMap(in map[string][]FactID) map[string][]FactID {
	if in == nil {
		return nil
	}
	out := make(map[string][]FactID, len(in))
	for key, ids := range in {
		out[key] = cloneFactIDs(ids)
	}
	return out
}

func cloneDuplicateIndexes(in duplicateIndexes) duplicateIndexes {
	return duplicateIndexes{
		strings:    cloneDuplicateKeyFactIDMap(in.strings),
		singleInt:  cloneSingleIntFactIDMap(in.singleInt),
		doubleInt:  cloneDoubleIntFactIDMap(in.doubleInt),
		intString:  cloneIntStringFactIDMap(in.intString),
		stringInt:  cloneStringIntFactIDMap(in.stringInt),
		intString2: cloneIntStringStringFactIDMap(in.intString2),
		string2Int: cloneStringStringIntIndexTable(in.string2Int),
		scalars:    cloneDuplicateIndexFactIDMap(in.scalars),
		structural: cloneDuplicateStructuralIndexTable(in.structural),
	}
}

func cloneDuplicateKeyFactIDMap(in map[DuplicateKey]FactID) map[DuplicateKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[DuplicateKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneSingleIntFactIDMap(in map[duplicateSingleIntIndexKey]FactID) map[duplicateSingleIntIndexKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[duplicateSingleIntIndexKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneDoubleIntFactIDMap(in map[duplicateDoubleIntIndexKey]FactID) map[duplicateDoubleIntIndexKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[duplicateDoubleIntIndexKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneIntStringFactIDMap(in map[duplicateIntStringIndexKey]FactID) map[duplicateIntStringIndexKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[duplicateIntStringIndexKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneStringIntFactIDMap(in map[duplicateStringIntIndexKey]FactID) map[duplicateStringIntIndexKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[duplicateStringIntIndexKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneIntStringStringFactIDMap(in map[duplicateIntStringStringIndexKey]FactID) map[duplicateIntStringStringIndexKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[duplicateIntStringStringIndexKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneDuplicateIndexFactIDMap(in map[duplicateIndexKey]FactID) map[duplicateIndexKey]FactID {
	if in == nil {
		return nil
	}
	out := make(map[duplicateIndexKey]FactID, len(in))
	maps.Copy(out, in)
	return out
}

func cloneDuplicateStructuralIndexTable(in duplicateStructuralIndexTable) duplicateStructuralIndexTable {
	if in.count == 0 || len(in.entries) == 0 {
		return duplicateStructuralIndexTable{}
	}
	out := duplicateStructuralIndexTable{}
	out.reserve(in.count)
	for i := range in.entries {
		entry := in.entries[i]
		if entry.state != graphTokenBucketFull {
			continue
		}
		out.set(entry.key, entry.first)
		for _, id := range entry.rest {
			out.set(entry.key, id)
		}
	}
	return out
}

func cloneStringStringIntIndexTable(in duplicateStringStringIntIndexTable) duplicateStringStringIntIndexTable {
	if in.count == 0 || len(in.entries) == 0 {
		return duplicateStringStringIntIndexTable{}
	}
	out := duplicateStringStringIntIndexTable{}
	out.reserve(in.count)
	for i := range in.entries {
		entry := in.entries[i]
		if entry.state != graphTokenBucketFull {
			continue
		}
		out.setHash(entry.hash, entry.first)
		for overflowIndex := entry.overflow; overflowIndex >= 0; {
			overflow := in.overflow[overflowIndex]
			out.setHash(entry.hash, overflow.factID)
			overflowIndex = overflow.next
		}
	}
	return out
}
