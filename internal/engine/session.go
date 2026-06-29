package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type SessionOption func(*sessionConfig)

type sessionConfig struct {
	id                  SessionID
	listeners           []EventListener
	initials            []SessionInitialFact
	eventClock          func() time.Time
	resetBeforeSnapshot bool
}

type SessionInitialFact struct {
	Name        string
	TemplateKey TemplateKey
	Fields      Fields
}

func validatePublicTemplateMutation(template Template) error {
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

func WithEventListener(listener EventListener) SessionOption {
	return func(cfg *sessionConfig) {
		if listener != nil {
			cfg.listeners = append(cfg.listeners, listener)
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

// WithResetBeforeSnapshot controls whether successful Reset calls populate
// ResetResult.Before. The default is true.
func WithResetBeforeSnapshot(enabled bool) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.resetBeforeSnapshot = enabled
	}
}

type Session struct {
	id                   SessionID
	revision             *Ruleset
	agenda               *agenda
	propagationCounters  *propagationCounterLedger
	rete                 *reteRuntime
	generation           Generation
	initialFocusStack    []ModuleName
	focusStack           []ModuleName
	initials             []SessionInitialFact
	initialCount         int
	compiledInitials     []compiledSessionInitialFact
	resetBeforeSnapshot  bool
	listeners            []EventListener
	eventClock           func() time.Time
	closed               bool
	runGuard             chan struct{}
	runActive            atomic.Bool
	runActivation        atomic.Pointer[activation]
	runHaltRequested     atomic.Bool
	runAgendaDelta       reteAgendaDelta
	runAgendaDeltas      []reteAgendaDelta
	runAgendaStates      []runAgendaDeltaState
	runAgendaBuckets     map[candidateIdentity]int
	runAgendaAdded       []reteTerminalTokenDelta
	runAgendaRemoved     []reteTerminalTokenDelta
	runAgendaUpdated     []reteTerminalTokenUpdate
	runAgendaPending     bool
	runAgendaDirect      bool
	agendaReady          bool
	agendaDirty          bool
	actionBindingScratch actionContextBindingState
	actionValueScratch   []Value
	actionMatchScratch   []conditionMatch
	mutationQueueMu      sync.Mutex
	mutationQueue        []queuedMutation
	mu                   struct {
		mutate chan struct{}
		lock   chan struct{}
	}

	nextFactSequence              uint64
	nextRecency                   Recency
	nextRunSequence               uint64
	facts                         []workingFact
	factsByID                     map[FactID]int
	factsBySequence               []int
	factsByDuplicate              duplicateIndexes
	factsByTemplate               map[TemplateKey][]FactID
	factsByName                   map[string][]FactID
	factFieldEqualIndexes         map[factFieldEqualKey][]FactSnapshot
	factTargetIndexesDirty        bool
	insertionOrder                []FactID
	slotStorage                   []factSlot
	resetWorkspace                factWorkspace
	resetFactsScratch             []FactSnapshot
	logicalSupportEdges           map[SupportID]logicalSupportEdgeRecord
	logicalSupportBySource        map[logicalSupportSourceKey]map[SupportID]struct{}
	logicalSupportByFact          map[FactID]map[SupportID]struct{}
	logicalSupportCounters        LogicalSupportCounters
	nextBackchainDemandSupportID  backchainDemandSupportID
	backchainDemandSupports       backchainDemandSupportTable
	backchainDemandSupportRecords []backchainDemandSupportRecord
	backchainDemandOwnerRecords   []backchainDemandOwnerSupportRecord
	backchainDemandInlineSupports backchainDemandInlineSupportIndex
	backchainDemandSupportOwners  backchainDemandOwnerSupportIndex
	backchainDemandByFact         backchainDemandFactSupportTable
	backchainDemandByDemand       backchainDemandFactSupportTable
	activeBackchainQueryProof     *backchainQueryProofContext
	backchainQueryProofScratch    backchainQueryProofContext
	nextEventSequence             uint64
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

func NewSession(revision *Ruleset, opts ...SessionOption) (*Session, error) {
	if revision == nil {
		return nil, ErrInvalidRuleset
	}

	cfg := sessionConfig{resetBeforeSnapshot: true}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.eventClock == nil {
		cfg.eventClock = time.Now
	}

	listeners := make([]EventListener, len(cfg.listeners))
	copy(listeners, cfg.listeners)
	initials := cloneSessionInitialFacts(cfg.initials)

	compiledInitials, err := compileSessionInitialFacts(revision, initials)
	if err != nil {
		return nil, err
	}
	state := newFactWorkspace(1, revision.estimatedRunFactCapacity(len(compiledInitials)))
	state.reserveTemplateIndexes(revision)
	state.reserveDuplicateIndexes(revision)
	state.reserveSlotStorage(revision.estimatedRunSlotCapacity(cap(state.facts)))
	if len(compiledInitials) > 0 {
		state.applyCompiledInitialFacts(compiledInitials)
	}
	rete, err := newReteRuntime(revision)
	if err != nil {
		return nil, err
	}
	agenda := newAgenda()
	agenda.reserveActivationRows(revision.estimatedRunFactCapacity(len(compiledInitials)))
	initialFacts := state.detachedFactsByInsertionOrder(revision)
	useInitialAgenda := len(listeners) == 0 && rete.supportsInitialAgendaReset()
	var initialDelta reteAgendaDelta
	if useInitialAgenda {
		initialDelta, err = rete.resetGraphBetaForGenerationWithInitialAgenda(context.Background(), initialFacts, state.generation, agenda)
	} else {
		err = rete.resetGraphBetaForGeneration(context.Background(), initialFacts, state.generation)
	}
	if err != nil {
		return nil, err
	}
	session := &Session{
		id:                  cfg.id,
		revision:            revision,
		agenda:              agenda,
		rete:                rete,
		generation:          1,
		initialFocusStack:   []ModuleName{MainModule},
		focusStack:          []ModuleName{MainModule},
		initials:            initials,
		initialCount:        len(initials),
		compiledInitials:    compiledInitials,
		resetBeforeSnapshot: cfg.resetBeforeSnapshot,
		listeners:           listeners,
		eventClock:          cfg.eventClock,
		runGuard:            make(chan struct{}, 1),
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		factsByID:              state.factsByID,
		factsBySequence:        state.factsBySequence,
		factsByDuplicate:       state.factsByDuplicate,
		factsByTemplate:        state.factsByTemplate,
		factsByName:            state.factsByName,
		factTargetIndexesDirty: state.factTargetIndexesDirty,
		nextFactSequence:       state.nextFactSequence(),
		nextRecency:            state.nextRecency(),
		nextRunSequence:        0,
		facts:                  state.facts,
		insertionOrder:         state.factsByInsertionOrder(),
		slotStorage:            state.slotStorage,
	}
	if useInitialAgenda && len(session.agenda.pending) != 0 && initialDelta.supported && len(initialDelta.removed) == 0 && len(initialDelta.updated) == 0 && len(initialDelta.demands) == 0 && len(initialDelta.resolvedDemands) == 0 && len(initialDelta.resolvedOwners) == 0 {
		session.agenda.finishInitialTerminalActivations()
		session.agendaReady = true
		session.agendaDirty = false
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
			Name:        initial.Name,
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
	return s.generation
}

func (s *Session) sourceGeneration() Generation {
	if s == nil {
		return 0
	}
	return s.generation
}

func (s *Session) attachPropagationCounters() *propagationCounterLedger {
	if s == nil {
		return nil
	}
	if s.propagationCounters == nil {
		s.propagationCounters = newPropagationCounterLedger()
	}
	s.syncPropagationCounters()
	return s.propagationCounters
}

func (s *Session) propagationCounterSnapshot() propagationCounterSnapshot {
	if s == nil || s.propagationCounters == nil {
		return propagationCounterSnapshot{}
	}
	s.syncPropagationCounters()
	if s.rete != nil && s.rete.graphBeta != nil {
		s.propagationCounters.setGraphBetaMemoryStats(s.rete.graphBeta.memoryStats())
	} else {
		s.propagationCounters.setGraphBetaMemoryStats(reteGraphBetaMemoryStats{})
	}
	return s.propagationCounters.snapshot()
}

func (s *Session) syncPropagationCounters() {
	if s == nil || s.agenda == nil {
		return
	}
	s.agenda.propagationCounters = s.propagationCounters
	if s.propagationCounters == nil {
		return
	}
	if s.rete != nil && s.rete.graphBeta != nil {
		s.propagationCounters.setTerminalRowsRetained(s.rete.graphBeta.terminalRowCount())
		s.propagationCounters.setBranchRowsRetained(s.rete.graphBeta.terminalRowsRetainedByBranch())
	} else {
		s.propagationCounters.setTerminalRowsRetained(0)
		s.propagationCounters.setBranchRowsRetained(nil)
	}
	path, unsupportedReasons := propagationRuntimeUnknown, map[string]int(nil)
	if s.rete != nil {
		path, unsupportedReasons = s.rete.propagationDiagnostics()
	}
	s.propagationCounters.setRuntimeDiagnostics(path, unsupportedReasons)
}

func (s *Session) propagationCounterPhase() propagationCounterPhase {
	if s != nil && s.agendaReady && !s.agendaDirty {
		return propagationCounterPhaseSteadyState
	}
	return propagationCounterPhaseInitial
}

func (s *Session) removeStoredFact(id FactID) {
	if s == nil || len(s.facts) == 0 {
		return
	}
	index, ok := s.factRowIndex(id)
	if !ok || index < 0 || index >= len(s.facts) || s.facts[index].id != id {
		return
	}
	last := len(s.facts) - 1
	if index != last {
		moved := s.facts[last]
		s.facts[index] = moved
		s.setFactRowIndex(moved.id, index)
	}
	s.deleteFactRowIndex(id)
	s.facts[last] = workingFact{}
	s.facts = s.facts[:last]
}

func (s *Session) reindexStoredFactRowsFrom(start int) {
	if s == nil || start < 0 {
		return
	}
	for i := start; i < len(s.facts); i++ {
		s.setFactRowIndex(s.facts[i].id, i)
	}
}

func (s *Session) workingFactByID(id FactID) (*workingFact, bool) {
	if s == nil {
		return nil, false
	}
	if s.activeBackchainQueryProof != nil {
		if fact, ok := s.activeBackchainQueryProof.workingFactByID(id); ok {
			return fact, true
		}
	}
	index, ok := s.factRowIndex(id)
	if !ok || index < 0 || index >= len(s.facts) {
		return nil, false
	}
	fact := &s.facts[index]
	if fact.id != id {
		return nil, false
	}
	return fact, true
}

func (s *Session) factRowIndex(id FactID) (int, bool) {
	if s == nil || id.IsZero() {
		return 0, false
	}
	if id.generation == s.generation && id.sequence > 0 {
		if id.sequence-1 > uint64(int(^uint(0)>>1)) {
			return 0, false
		}
		index := int(id.sequence - 1)
		if uint64(index) == id.sequence-1 && index < len(s.factsBySequence) {
			row := s.factsBySequence[index]
			if row >= 0 {
				return row, true
			}
		}
	}
	if s.factsByID == nil {
		return 0, false
	}
	index, ok := s.factsByID[id]
	return index, ok
}

func (s *Session) setFactRowIndex(id FactID, row int) {
	if s == nil || id.IsZero() || row < 0 {
		return
	}
	if id.generation == s.generation && id.sequence > 0 {
		index := int(id.sequence - 1)
		if uint64(index) == id.sequence-1 {
			for len(s.factsBySequence) <= index {
				s.factsBySequence = append(s.factsBySequence, -1)
			}
			s.factsBySequence[index] = row
			return
		}
	}
	if s.factsByID != nil {
		s.factsByID[id] = row
	}
}

func (s *Session) deleteFactRowIndex(id FactID) {
	if s == nil || id.IsZero() {
		return
	}
	if id.generation == s.generation && id.sequence > 0 {
		index := int(id.sequence - 1)
		if uint64(index) == id.sequence-1 && index < len(s.factsBySequence) {
			s.factsBySequence[index] = -1
			return
		}
	}
	if s.factsByID != nil {
		delete(s.factsByID, id)
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
		ids := s.factsByName[target.name]
		if len(ids) == 0 {
			return nil, true
		}
		out := make([]FactSnapshot, 0, len(ids))
		for _, id := range ids {
			fact, ok := s.workingFactByID(id)
			if !ok {
				continue
			}
			out = append(out, fact.detachedSnapshotForRevision(s.revision))
		}
		return out, true
	case conditionTargetTemplateKey:
		ids := s.factsByTemplate[target.templateKey]
		if len(ids) == 0 {
			return nil, true
		}
		out := make([]FactSnapshot, 0, len(ids))
		for _, id := range ids {
			fact, ok := s.workingFactByID(id)
			if !ok {
				continue
			}
			out = append(out, fact.detachedSnapshotForRevision(s.revision))
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
	facts, cached := s.factFieldEqualIndexes[key]
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
		if !ok || !workingFactMatchesFieldEqualIndex(fact, fieldSlot, value) {
			continue
		}
		facts = append(facts, fact.detachedSnapshotForRevision(s.revision))
	}
	if s.factFieldEqualIndexes == nil {
		s.factFieldEqualIndexes = make(map[factFieldEqualKey][]FactSnapshot)
	}
	s.factFieldEqualIndexes[key] = facts
	return facts, true
}

func (s *Session) recordAlphaIndexProbe(hit bool) {
	if s == nil || s.propagationCounters == nil {
		return
	}
	s.propagationCounters.recordAlphaIndexProbe(hit)
}

func (s *Session) recordAlphaIndexFallbackScan() {
	if s == nil || s.propagationCounters == nil {
		return
	}
	s.propagationCounters.recordAlphaIndexFallbackScan()
}

func (s *Session) factIDsForTarget(target conditionTarget) ([]FactID, bool) {
	switch target.kind {
	case conditionTargetName:
		return s.factsByName[target.name], true
	case conditionTargetTemplateKey:
		return s.factsByTemplate[target.templateKey], true
	default:
		return nil, false
	}
}

func (s *Session) ensureFactTargetIndexes() {
	if s == nil || !s.factTargetIndexesDirty {
		return
	}
	s.rebuildFactTargetIndexes()
}

func (s *Session) rebuildFactTargetIndexes() {
	if s == nil {
		return
	}
	if s.factsByTemplate == nil {
		s.factsByTemplate = make(map[TemplateKey][]FactID)
	} else {
		clear(s.factsByTemplate)
	}
	if s.factsByName == nil {
		s.factsByName = make(map[string][]FactID)
	} else {
		clear(s.factsByName)
	}
	for i := range s.facts {
		fact := &s.facts[i]
		if fact.id.IsZero() {
			continue
		}
		s.factsByTemplate[fact.templateKey] = append(s.factsByTemplate[fact.templateKey], fact.id)
		s.factsByName[fact.name] = append(s.factsByName[fact.name], fact.id)
	}
	s.clearFactFieldEqualIndexes()
	s.factTargetIndexesDirty = false
}

func (s *Session) clearFactFieldEqualIndexes() {
	if s == nil || len(s.factFieldEqualIndexes) == 0 {
		return
	}
	clear(s.factFieldEqualIndexes)
}

func (s *Session) removeFactTargetIndexes(templateKey TemplateKey, name string, id FactID) {
	if s == nil || id.IsZero() || s.factTargetIndexesDirty {
		return
	}
	s.factsByTemplate[templateKey] = removeFactIDFromSlice(s.factsByTemplate[templateKey], id)
	if len(s.factsByTemplate[templateKey]) == 0 {
		delete(s.factsByTemplate, templateKey)
	}
	s.factsByName[name] = removeFactIDFromSlice(s.factsByName[name], id)
	if len(s.factsByName[name]) == 0 {
		delete(s.factsByName, name)
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

func (s *Session) Close() error {
	if s == nil {
		return ErrClosedSession
	}
	s.closed = true
	return nil
}

func (s *Session) Assert(ctx context.Context, name string, fields Fields) (AssertResult, error) {
	return s.insertFactWithContextAndOrigin(ctx, name, "", fields, mutationOrigin{})
}

func (s *Session) AssertTemplate(ctx context.Context, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	return s.insertFactWithContextAndOrigin(ctx, "", templateKey, fields, mutationOrigin{})
}

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
	if _, ok := logicalSupportSourceFromOrigin(origin, s.generation); !ok {
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
		_, err := s.reconcileAgendaAfterMutation(ctx, batch.agendaDelta)
		return err
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
	template Template
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
		_, err := s.reconcileAgendaAfterMutation(ctx, batch.agendaDelta)
		return err
	}
	return nil
}

func accumulateReteAgendaDelta(current reteAgendaDelta, hasCurrent bool, next reteAgendaDelta) (reteAgendaDelta, bool) {
	if !hasCurrent {
		return next, true
	}
	return mergeReteAgendaDelta(current, next), true
}

func (b *preparedTemplateValueBatch) reserve(facts, slots int) {
	if b == nil {
		return
	}
	b.state.reserveGeneratedFactCapacity(b.session.revision, facts, slots)
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

func (p preparedTemplateValueInserter) insertPreparedSlots(b *preparedTemplateValueBatch, slots []factSlot, slotMark int) error {
	session := b.session
	fact, _, inserted, err := b.state.insertPreparedGeneratedFactSlots(session.revision, session.generation, p.template, slots, slotMark)
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
	if session.propagationCounters != nil {
		counterSpan := session.propagationCounters.beginAssert(p.template.Key(), mutationOrigin{})
		span = &counterSpan
	}
	agendaDelta, err := session.updateReteAlphaAfterAssertGenerated(b.ctx, fact, mutationOrigin{}, span)
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
	if value.kind == ValueList || value.kind == ValueMap {
		value = cloneValue(value)
	}
	slots[index].value = value
	slots[index].ok = true
	slots[index].presence = fieldPresenceExplicit
	return nil
}

func (s *Session) insertLogicalFactImmediate(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin, supportingFacts []FactID) (AssertResult, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return AssertResult{Status: AssertClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	state := s.clonedFactWorkspace()
	fact, duplicateKey, inserted, err := state.insertFact(s.revision, s.generation, name, templateKey, fields)
	if err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	supportState := s.captureLogicalSupportState()
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: s.generation,
	}
	if !inserted {
		before := fact.snapshotForRevision(s.revision)
		_, err := s.addLogicalSupportForPropagationEvent(ctx, fact, supportEvent, supportingFacts)
		if err != nil {
			s.restoreLogicalSupportState(supportState)
			return AssertResult{Status: AssertValidationFailure, Fact: before}, reteAgendaDelta{}, err
		}
		if before.Support().State == FactSupportLogical {
			s.updateFactSupportState(fact)
		}
		after := fact.snapshotForRevision(s.revision)
		s.commitFactWorkspace(state)
		var delta *MutationDelta
		if before.Support().State != after.Support().State {
			metadataDelta := MutationDelta{
				Kind:           MutationAssert,
				Generation:     s.generation,
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

	s.makeFactLogicalOnly(fact)
	if _, err := s.addLogicalSupportForPropagationEvent(ctx, fact, supportEvent, supportingFacts); err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	s.logicalSupportCounters.LogicalFactsAsserted++

	snapshot := fact.snapshotForRevision(s.revision)
	var span *propagationCounterSpan
	if s.propagationCounters != nil {
		counterSpan := s.propagationCounters.beginAssert(snapshot.TemplateKey(), origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssert(ctx, snapshot, origin, span)
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
	delta := MutationDelta{
		Kind:           MutationAssert,
		Generation:     s.generation,
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
		Status:       AssertInserted,
		Fact:         snapshot,
		DuplicateKey: duplicateKey,
		Delta:        &delta,
	}
	if len(s.listeners) > 0 {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.nextEventSequence + 1,
			Timestamp:      s.eventClock(),
			Type:           EventFactAsserted,
			Generation:     s.generation,
			Recency:        fact.recency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			FactIDs:        []FactID{fact.id},
			Delta:          &delta,
		})
		s.nextEventSequence++
	}

	return result, agendaDelta, nil
}

func (s *Session) insertFactImmediate(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin) (AssertResult, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return AssertResult{Status: AssertClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	state := s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	fact, duplicateKey, inserted, err := state.insertFact(s.revision, s.generation, name, templateKey, fields)
	if err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	if !inserted {
		before := fact.snapshotForRevision(s.revision)
		if s.addStatedSupportToFact(fact) {
			after := fact.snapshotForRevision(s.revision)
			s.commitFactWorkspace(state)
			delta := MutationDelta{
				Kind:           MutationAssert,
				Generation:     s.generation,
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

	snapshot := fact.snapshotForRevision(s.revision)
	var span *propagationCounterSpan
	if s.propagationCounters != nil {
		counterSpan := s.propagationCounters.beginAssert(snapshot.TemplateKey(), origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssert(ctx, snapshot, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure}, agendaDelta, err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure}, mergeReteAgendaDelta(agendaDelta, resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return AssertResult{Status: AssertValidationFailure}, mergeReteAgendaDelta(agendaDelta, demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(state)
	delta := MutationDelta{
		Kind:           MutationAssert,
		Generation:     s.generation,
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
		Status:       AssertInserted,
		Fact:         snapshot,
		DuplicateKey: duplicateKey,
		Delta:        &delta,
	}
	if len(s.listeners) > 0 {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.nextEventSequence + 1,
			Timestamp:      s.eventClock(),
			Type:           EventFactAsserted,
			Generation:     s.generation,
			Recency:        fact.recency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			FactIDs:        []FactID{fact.id},
			Delta:          &delta,
		})
		s.nextEventSequence++
	}

	return result, agendaDelta, nil
}

func (s *Session) insertTemplateValuesImmediate(ctx context.Context, templateKey TemplateKey, values []Value, origin mutationOrigin) (*workingFact, Template, DuplicateKey, bool, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return nil, Template{}, "", false, reteAgendaDelta{}, ErrClosedSession
	}
	template, ok := s.revision.templateByKey(templateKey)
	if !ok {
		return nil, Template{}, "", false, reteAgendaDelta{}, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}
	if err := validatePublicTemplateMutation(template); err != nil {
		return nil, Template{}, "", false, reteAgendaDelta{}, err
	}
	state := s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	fieldSlots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(template.fields))
	fieldSlots, err := template.buildValidatedFieldSlotsFromValuesInto(fieldSlots, values)
	if err != nil {
		state.rollbackGeneratedFactSlots(slotMark)
		return nil, Template{}, "", false, reteAgendaDelta{}, err
	}

	fact, duplicateKey, inserted, agendaDelta, err := s.insertPreparedTemplateSlotsImmediate(ctx, state, template, fieldSlots, mark, slotMark, origin)
	if err != nil {
		return nil, Template{}, "", false, agendaDelta, err
	}
	return fact, template, duplicateKey, inserted, agendaDelta, nil
}

func (s *Session) insertPreparedTemplateSlotsImmediate(ctx context.Context, state factWorkspace, template Template, fieldSlots []factSlot, mark factWorkspaceInsertMark, slotMark int, origin mutationOrigin) (*workingFact, DuplicateKey, bool, reteAgendaDelta, error) {
	plan, ok := s.revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	return s.insertPreparedTemplateSlotsWithPlanImmediate(ctx, state, plan, fieldSlots, mark, slotMark, origin)
}

func (s *Session) insertPreparedTemplateSlotsWithPlanImmediate(ctx context.Context, state factWorkspace, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, mark factWorkspaceInsertMark, slotMark int, origin mutationOrigin) (*workingFact, DuplicateKey, bool, reteAgendaDelta, error) {
	if !plan.valid() {
		state.rollbackGeneratedFactInsert(mark, nil)
		return nil, "", false, reteAgendaDelta{}, &ValidationError{
			Reason: "generated fact insert plan is missing",
		}
	}
	fact, duplicateKey, inserted, err := state.insertPreparedGeneratedFactSlotsWithPlan(s.revision, s.generation, plan, fieldSlots, slotMark)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil)
		return nil, "", false, reteAgendaDelta{}, err
	}
	if !inserted {
		return fact, duplicateKey, false, reteAgendaDelta{}, nil
	}

	if !plan.affectsRete {
		s.commitFactWorkspace(state)
		s.emitGeneratedAssertEvent(ctx, fact, origin)
		return fact, duplicateKey, true, reteAgendaDelta{}, nil
	}

	var span *propagationCounterSpan
	if s.propagationCounters != nil {
		counterSpan := s.propagationCounters.beginAssert(plan.templateKey, origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return nil, "", false, agendaDelta, err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return nil, "", false, mergeReteAgendaDelta(agendaDelta, resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return nil, "", false, mergeReteAgendaDelta(agendaDelta, demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(state)
	s.emitGeneratedAssertEvent(ctx, fact, origin)

	return fact, duplicateKey, true, agendaDelta, nil
}

func (s *Session) insertRuleActionGeneratedFactSlotsImmediate(ctx context.Context, state *factWorkspace, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, mark factWorkspaceInsertMark, slotMark int, origin mutationOrigin) (bool, reteAgendaDelta, error) {
	if state == nil || !plan.valid() {
		if state != nil {
			state.rollbackGeneratedFactInsert(mark, nil)
		}
		return false, reteAgendaDelta{}, &ValidationError{
			Reason: "generated fact insert plan is missing",
		}
	}
	fact, _, inserted, err := state.insertPreparedGeneratedFactSlotsWithPlanUnchecked(s.revision, s.generation, plan, fieldSlots, slotMark, factTargetIndexDirty)
	if err != nil {
		state.rollbackGeneratedFactInsert(mark, nil)
		return false, reteAgendaDelta{}, err
	}
	if !inserted {
		return false, reteAgendaDelta{}, nil
	}

	if !plan.affectsRete {
		s.commitFactWorkspace(*state)
		s.emitGeneratedAssertEvent(ctx, fact, origin)
		return true, reteAgendaDelta{}, nil
	}

	var span *propagationCounterSpan
	if s.propagationCounters != nil {
		counterSpan := s.propagationCounters.beginAssert(plan.templateKey, origin)
		span = &counterSpan
	}
	agendaDelta, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, origin, span)
	if err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return false, agendaDelta, err
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(agendaDelta, resolvedDelta), err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, resolvedDelta)
	}
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, state, agendaDelta.demands, origin); err != nil {
		if span != nil {
			span.finish()
		}
		state.rollbackGeneratedFactInsert(mark, fact)
		s.restoreReteAfterPropagationFailure()
		return false, mergeReteAgendaDelta(agendaDelta, demandDelta), err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	if span != nil {
		span.finish()
	}
	s.commitFactWorkspace(*state)
	s.emitGeneratedAssertEvent(ctx, fact, origin)

	return true, agendaDelta, nil
}

func (s *Session) flushBackchainDemandRequestsImmediate(ctx context.Context, state *factWorkspace, demands []backchainDemandID, origin mutationOrigin) (reteAgendaDelta, error) {
	if s != nil && s.activeBackchainQueryProof != nil {
		return s.activeBackchainQueryProof.flushDemands(ctx, demands, origin)
	}
	if s == nil || state == nil || len(demands) == 0 {
		s.clearBackchainDemandRequestArena()
		return reteAgendaDelta{supported: true}, nil
	}
	defer s.clearBackchainDemandRequestArena()
	combined := reteAgendaDelta{supported: true}
	queue := demands
	queueOwned := false
	for i := 0; i < len(queue); i++ {
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
		fact, _, inserted, err := state.insertPreparedEngineGeneratedFactSlots(s.revision, s.generation, template, slots, slotMark)
		if err != nil {
			return combined, err
		}
		fact.supportState = FactSupportLogical
		if !inserted {
			s.addBackchainDemandSupport(fact, demand)
			continue
		}
		var span *propagationCounterSpan
		if s.propagationCounters != nil {
			counterSpan := s.propagationCounters.beginAssert(template.Key(), origin)
			span = &counterSpan
		}
		next, err := s.updateReteAlphaAfterAssertGenerated(ctx, fact, origin, span)
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
	for _, owner := range combined.resolvedOwners {
		resolvedDelta, err := s.removeBackchainDemandSupportForOwner(ctx, owner, origin)
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, resolvedDelta)
	}
	for _, resolved := range combined.resolvedDemands {
		request, ok := s.backchainDemandRequestByID(resolved)
		if !ok {
			combined.supported = false
			continue
		}
		resolvedDelta, err := s.removeBackchainDemandSupportForRequest(ctx, request, origin)
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

func (s *Session) resolveBackchainDemandRequestsImmediate(ctx context.Context, resolved []backchainDemandID, owners []backchainDemandOwnerKey, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s != nil && s.activeBackchainQueryProof != nil {
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
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return backchainDemandRequest{}, false
	}
	return s.rete.graphBeta.backchainDemandRequestByID(id)
}

func (s *Session) clearBackchainDemandRequestArena() {
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return
	}
	s.rete.graphBeta.clearBackchainDemandRequests()
}

func (s *Session) emitGeneratedAssertEvent(ctx context.Context, fact *workingFact, origin mutationOrigin) {
	if s == nil || len(s.listeners) == 0 || fact == nil {
		return
	}
	publicSnapshot := fact.snapshotForRevision(s.revision)
	duplicateKey := fact.publicDuplicateKey(s.revision)
	delta := MutationDelta{
		Kind:           MutationAssert,
		Generation:     s.generation,
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
		Sequence:       s.nextEventSequence + 1,
		Timestamp:      s.eventClock(),
		Type:           EventFactAsserted,
		Generation:     s.generation,
		Recency:        fact.recency,
		RuleID:         origin.RuleID,
		RuleRevisionID: origin.RuleRevisionID,
		ActivationID:   origin.activationID(),
		FactIDs:        []FactID{fact.id},
		Delta:          &delta,
	})
	s.nextEventSequence++
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

	if id.Generation() != s.generation {
		if id.Generation() != 0 && id.Generation() < s.generation {
			return RetractResult{Status: RetractStale}, reteAgendaDelta{}, ErrStaleFactID
		}
		return RetractResult{Status: RetractMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	fact, ok := s.workingFactByID(id)
	if !ok {
		return RetractResult{Status: RetractMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision)
	switch fact.resolvedSupportState() {
	case FactSupportLogical:
		return RetractResult{Status: RetractLogicalOnly, Fact: before}, reteAgendaDelta{}, ErrLogicalOnlyRetract
	case FactSupportStatedAndLogical:
		s.removeStatedSupportFromFact(fact)
		after := fact.snapshotForRevision(s.revision)
		delta := MutationDelta{
			Kind:           MutationRetract,
			Generation:     s.generation,
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
			OldDuplicate:   fact.publicDuplicateKey(s.revision),
			NewDuplicate:   fact.publicDuplicateKey(s.revision),
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
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		return result, agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, resolvedDelta)
	}
	demandState := s.activeFactWorkspace()
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &demandState, agendaDelta.demands, origin); err != nil {
		return result, agendaDelta, err
	} else {
		s.commitFactWorkspace(demandState)
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: s.generation,
	}
	cascadeDelta, err := s.removeLogicalSupportsForPropagationEventDelta(ctx, supportEvent, agendaDelta)
	if err != nil {
		return result, agendaDelta, err
	}
	return result, mergeReteAgendaDelta(agendaDelta, cascadeDelta), nil
}

func (s *Session) removeFactImmediate(ctx context.Context, id FactID, origin mutationOrigin, cascade bool) (RetractResult, reteAgendaDelta, error) {
	fact, ok := s.workingFactByID(id)
	if !ok {
		return RetractResult{Status: RetractMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision)
	oldVersion := fact.version
	oldDuplicate := fact.publicDuplicateKey(s.revision)
	factID := fact.id
	factRecency := fact.recency
	factTemplateKey := fact.templateKey
	factName := fact.name

	agendaDelta, err := s.updateReteAlphaAfterRetract(ctx, before, origin)
	if err != nil {
		s.restoreReteAfterPropagationFailure()
		return RetractResult{Status: RetractValidationFailure, Fact: before}, agendaDelta, err
	}

	state := s.activeFactWorkspace()
	if !fact.dupIndex.isZero() {
		state.factsByDuplicate.deleteFact(fact.dupIndex, id)
	}
	if !fact.targetIndexesSkipped {
		state.removeFactTargetIndexes(factTemplateKey, factName, id)
	}
	state.insertionOrder = removeFactIDFromSlice(state.insertionOrder, id)
	state.removeStoredFact(id)
	s.commitFactWorkspace(state)
	if cascade {
		s.logicalSupportCounters.LogicalFactsRetracted++
		s.logicalSupportCounters.CascadeRetractions++
	}

	delta := MutationDelta{
		Kind:           MutationRetract,
		Generation:     s.generation,
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
	if len(s.listeners) > 0 {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.nextEventSequence + 1,
			Timestamp:      s.eventClock(),
			Type:           EventFactRetracted,
			Generation:     s.generation,
			Recency:        factRecency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			FactIDs:        []FactID{factID},
			Delta:          &delta,
		})
		s.nextEventSequence++
	}

	return result, agendaDelta, nil
}

func (s *Session) removeBackchainDemandFactImmediate(ctx context.Context, id FactID, origin mutationOrigin) (reteAgendaDelta, error) {
	fact, ok := s.workingFactByID(id)
	if !ok {
		return reteAgendaDelta{}, ErrFactNotFound
	}

	factTemplateKey := fact.templateKey
	factName := fact.name

	agendaDelta, err := s.updateReteAlphaAfterRetractGenerated(ctx, fact, origin)
	if err != nil {
		s.restoreReteAfterPropagationFailure()
		return agendaDelta, err
	}

	state := s.activeFactWorkspace()
	if !fact.dupIndex.isZero() {
		state.factsByDuplicate.deleteFact(fact.dupIndex, id)
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
	if s.agendaReady && !s.agendaDirty {
		return result, nil
	}
	if len(s.listeners) == 0 {
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
	next := &s.resetWorkspace
	next.reset(s.generation+1, s.revision.estimatedRunFactCapacity(len(compiledInitials)))
	next.skipFactTargetIndexes = true
	next.reserveTemplateIndexes(s.revision)
	if len(compiledInitials) > 0 {
		next.reserveDuplicateIndexes(s.revision)
	}
	facts := next.applyCompiledInitialFactsInto(compiledInitials, s.resetFactsScratch[:0], s.revision)
	s.resetFactsScratch = facts

	rete := s.rete
	if rete == nil {
		var err error
		rete, err = newReteRuntime(s.revision)
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
	}
	var oldTerminalDeltas []reteTerminalTokenDelta
	resetAgendaWithDeltas := false
	mayEmitBackchainDemandDeltas := s.rete != nil && s.rete.mayEmitBackchainDemandDeltas()
	if s.agendaReady && !s.agendaDirty && s.rete != nil && !mayEmitBackchainDemandDeltas {
		tokens, ok, err := s.rete.currentTerminalTokenDeltas(ctx)
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
		if ok {
			oldTerminalDeltas = cloneStableTerminalTokenDeltas(s.revision, tokens)
			if s.agenda != nil {
				s.agenda.materializePendingTokenFacts(s.revision)
			}
			resetAgendaWithDeltas = true
		}
	}
	resetDemandDelta, err := rete.resetGraphBetaForGenerationWithDelta(ctx, facts, next.generation)
	if err != nil {
		if s.rete != nil {
			rollbackFacts := before.facts
			if !s.resetBeforeSnapshot {
				rollbackFacts = s.detachedFactsByInsertionOrder()
			}
			_ = s.rete.resetGraphBetaForGeneration(context.Background(), rollbackFacts, s.generation)
		}
		return ResetResult{Status: ResetValidationFailure, Before: before}, err
	}
	if len(resetDemandDelta.demands) > 0 || len(resetDemandDelta.resolvedDemands) > 0 || len(resetDemandDelta.resolvedOwners) > 0 {
		resetAgendaWithDeltas = false
	}
	var newTerminalDeltas []reteTerminalTokenDelta
	if resetAgendaWithDeltas {
		tokens, ok, err := rete.currentTerminalTokenDeltas(ctx)
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
		if !ok {
			resetAgendaWithDeltas = false
		} else {
			newTerminalDeltas = tokens
		}
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordGraphRebuild(propagationCounterPhaseInitial)
	}

	oldGeneration := s.generation
	s.agendaReady = resetAgendaWithDeltas
	s.agendaDirty = false
	s.resetFocusStack()
	s.clearLogicalSupports()
	s.clearBackchainDemandSupports()
	s.swapFactWorkspace(next)
	s.generation = next.generation
	s.rete = rete
	s.syncPropagationCounters()
	if len(resetDemandDelta.resolvedDemands) > 0 || len(resetDemandDelta.resolvedOwners) > 0 {
		if _, err := s.resolveBackchainDemandRequestsImmediate(ctx, resetDemandDelta.resolvedDemands, resetDemandDelta.resolvedOwners, mutationOrigin{}); err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
	}
	if len(resetDemandDelta.demands) > 0 {
		demandState := s.activeFactWorkspace()
		demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &demandState, resetDemandDelta.demands, mutationOrigin{})
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
		s.commitFactWorkspace(demandState)
		if len(demandDelta.added) > 0 || len(demandDelta.removed) > 0 || len(demandDelta.updated) > 0 {
			resetAgendaWithDeltas = false
			s.agendaReady = false
			s.agendaDirty = false
		}
	}
	var resetDeactivations []agendaChange
	var resetActivations []agendaChange
	if resetAgendaWithDeltas {
		resetCtx := context.Background()
		var err error
		resetDeactivations, err = s.agenda.applyTerminalTokenDeltas(resetCtx, s.revision, oldTerminalDeltas, nil)
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
		if s.propagationCounters != nil {
			s.propagationCounters.recordAgendaDeltaApplication()
		}
	}
	if resetAgendaWithDeltas {
		s.agenda.reset()
		s.agendaReady = true
		s.agendaDirty = false
		var err error
		resetActivations, err = s.agenda.applyTerminalTokenDeltas(context.Background(), s.revision, nil, newTerminalDeltas)
		if err != nil {
			return ResetResult{Status: ResetValidationFailure, Before: before}, err
		}
		if s.propagationCounters != nil {
			s.propagationCounters.recordAgendaDeltaApplication()
		}
	} else {
		s.emitAgendaEvents(ctx, s.agenda.clear())
	}
	s.agenda.reserveActivationRows(s.revision.estimatedRunFactCapacity(len(compiledInitials)))

	result := ResetResult{
		Status:     ResetApplied,
		Generation: s.generation,
		Before:     before,
		Delta: MutationDelta{
			Kind:          MutationReset,
			Generation:    s.generation,
			OldGeneration: oldGeneration,
		},
	}
	if resetAgendaWithDeltas {
		s.emitAgendaEvents(ctx, resetDeactivations)
	}
	if len(s.listeners) > 0 {
		delta := MutationDelta{
			Kind:          MutationReset,
			Generation:    s.generation,
			OldGeneration: oldGeneration,
		}
		s.emitEvent(ctx, Event{
			SessionID:  s.id,
			RulesetID:  s.revision.ID(),
			Sequence:   s.nextEventSequence + 1,
			Timestamp:  s.eventClock(),
			Type:       EventReset,
			Generation: s.generation,
			FactIDs:    nil,
			Delta:      &delta,
		})
		s.nextEventSequence++
	}
	if resetAgendaWithDeltas {
		s.applyAutoFocus(resetActivations)
		s.emitAgendaEvents(ctx, resetActivations)
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

	s.rebuildFieldSlots(s.revision, next)
	snapshot = s.indexedSnapshotLocked()
	rete, err := newReteRuntime(next)
	if err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}
	phase := propagationCounterPhaseInitial
	if err := rete.resetGraphBetaForGeneration(ctx, snapshot.facts, s.generation); err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordGraphRebuild(phase)
	}

	tokens, ok, err := rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		restoreApplyRulesetState()
		return ApplyRulesetResult{}, err
	}
	var results []ruleMatchResult
	if s.propagationCounters != nil && ok {
		s.propagationCounters.recordWholeTerminalScan(phase)
	}
	if !ok {
		if s.propagationCounters != nil {
			s.propagationCounters.recordOracleStyleMatchRequest(phase)
		}
		results, err = rete.match(ctx, snapshot)
		if err != nil {
			restoreApplyRulesetState()
			return ApplyRulesetResult{}, err
		}
	}

	s.revision = next
	s.rete = rete
	if s.agenda == nil {
		s.agenda = newAgenda()
	}
	s.syncPropagationCounters()
	s.emitAgendaEvents(ctx, s.agenda.purgeRuleRevisions(plan.purgeRevisions))
	if ok {
		changes, err := s.agenda.reconcileTerminalTokens(context.Background(), next, tokens)
		if err != nil {
			return ApplyRulesetResult{}, err
		}
		s.agendaReady = true
		s.agendaDirty = false
		s.applyAutoFocus(changes)
		s.emitAgendaEvents(ctx, changes)

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
	changes, err := s.agenda.reconcile(context.Background(), next, results)
	if err != nil {
		return ApplyRulesetResult{}, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordFullAgendaReconcile(phase)
	}
	s.agendaReady = true
	s.agendaDirty = false
	s.applyAutoFocus(changes)
	s.emitAgendaEvents(ctx, changes)

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

func (s *Session) reconcileAgenda(ctx context.Context, source factSource) ([]agendaChange, error) {
	if s == nil || s.closed {
		return nil, ErrClosedSession
	}
	if s.revision == nil {
		return nil, ErrInvalidRuleset
	}
	if source == nil {
		return nil, ErrInvalidRuleset
	}
	if s.agenda == nil {
		s.agenda = newAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if s.rete == nil {
		return nil, ErrUnsupportedRuntime
	}
	phase := s.propagationCounterPhase()
	if s.propagationCounters != nil {
		s.propagationCounters.recordOracleStyleMatchRequest(phase)
	}
	results, err := s.rete.match(ctx, source)
	if err != nil {
		return nil, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordWholeTerminalScan(phase)
		s.propagationCounters.recordFullAgendaReconcile(phase)
	}
	changes, err := s.agenda.reconcile(ctx, s.revision, results)
	if err != nil {
		return nil, err
	}
	s.agendaReady = true
	s.agendaDirty = false
	s.applyAutoFocus(changes)
	s.emitAgendaEvents(ctx, changes)
	return changes, nil
}

func (s *Session) reconcileAgendaInternal(ctx context.Context) ([]agendaChange, error) {
	if changes, ok, err := s.reconcileAgendaWithoutSnapshot(ctx); ok || err != nil {
		return changes, err
	}
	return s.reconcileAgenda(ctx, s)
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
	if s.agenda == nil {
		s.agenda = newAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if s.rete == nil {
		return nil, false, nil
	}

	if s.rete.graphBeta != nil {
		changes, ok, err := s.agenda.reconcileGraphTerminalRows(ctx, s.revision, s.rete.graphBeta, collectChanges)
		if err != nil {
			return nil, true, err
		}
		if ok {
			phase := s.propagationCounterPhase()
			if s.propagationCounters != nil {
				s.propagationCounters.recordWholeTerminalScan(phase)
			}
			s.agendaReady = true
			s.agendaDirty = false
			if collectChanges {
				s.applyAutoFocus(changes)
				s.emitAgendaEvents(ctx, changes)
			}
			return changes, true, nil
		}
		return nil, true, fmt.Errorf("%w: direct graph terminal agenda build is unsupported for this graph shape", ErrUnsupportedRuntime)
	}

	tokens, ok, err := s.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		return nil, true, err
	}
	phase := s.propagationCounterPhase()
	if ok {
		if s.propagationCounters != nil {
			s.propagationCounters.recordWholeTerminalScan(phase)
		}
		var changes []agendaChange
		if collectChanges {
			var err error
			changes, err = s.agenda.reconcileTerminalTokens(ctx, s.revision, tokens)
			if err != nil {
				return nil, true, err
			}
		} else if err := s.agenda.reconcileTerminalTokensWithoutChanges(ctx, s.revision, tokens); err != nil {
			return nil, true, err
		}
		s.agendaReady = true
		s.agendaDirty = false
		if collectChanges {
			s.applyAutoFocus(changes)
			s.emitAgendaEvents(ctx, changes)
		}
		return changes, true, nil
	}

	results, ok, err := s.rete.matchWithoutSnapshot(ctx, s.generation)
	if err != nil || !ok {
		return nil, ok, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordOracleStyleMatchRequest(phase)
		s.propagationCounters.recordWholeTerminalScan(phase)
		s.propagationCounters.recordFullAgendaReconcile(phase)
	}
	changes, err := s.agenda.reconcile(ctx, s.revision, results)
	if err != nil {
		return nil, true, err
	}
	s.agendaReady = true
	s.agendaDirty = false
	s.applyAutoFocus(changes)
	s.emitAgendaEvents(ctx, changes)
	return changes, true, nil
}

func (s *Session) reconcileAgendaAfterMutation(ctx context.Context, delta reteAgendaDelta) ([]agendaChange, error) {
	if changes, ok, err := s.applyReteAgendaDeltaInternal(ctx, delta, len(s.listeners) > 0); ok || err != nil {
		return changes, err
	}
	if !delta.supported && s.agendaReady && !s.agendaDirty {
		return nil, fmt.Errorf("%w: unsupported agenda delta after steady-state mutation", ErrUnsupportedRuntime)
	}
	if len(s.listeners) == 0 && s.rete != nil && !s.rete.supportsIncrementalAgenda() {
		s.markAgendaDirty()
		return nil, nil
	}
	return s.reconcileAgendaInternal(ctx)
}

func cloneStableTerminalTokenDeltas(revision *Ruleset, deltas []reteTerminalTokenDelta) []reteTerminalTokenDelta {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]reteTerminalTokenDelta, 0, len(deltas))
	for _, delta := range deltas {
		cloned := delta
		if revision != nil {
			if rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]; ok {
				if cloned.identity.isZero() {
					cloned.identity = candidateIdentityForTerminalToken(rule, delta.token)
				}
				if factIDs, factVersions, ok := terminalTokenFactTuple(rule, delta.token); ok {
					cloned.factIDs = cloneFactIDs(factIDs)
					cloned.factVersions = cloneFactVersions(factVersions)
				}
			}
		}
		out = append(out, cloned)
	}
	return out
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
	if s.agenda == nil {
		s.agenda = newAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if !delta.supported || s.rete == nil || !s.agendaReady || s.agendaDirty {
		if s.propagationCounters != nil && !delta.supported {
			s.propagationCounters.recordUnsupportedAgendaDelta()
		}
		return nil, false, nil
	}
	if len(delta.updated) != 0 {
		if err := s.agenda.applyTerminalTokenUpdates(ctx, s.revision, delta.updated); err != nil {
			return nil, true, err
		}
	}
	var changes []agendaChange
	if collectChanges {
		var err error
		changes, err = s.agenda.applyTerminalTokenDeltas(ctx, s.revision, delta.removed, delta.added)
		if err != nil {
			return nil, true, err
		}
	} else if err := s.applyTerminalTokenDeltasWithoutChangesAndAttach(ctx, delta.removed, delta.added); err != nil {
		return nil, true, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordAgendaDeltaApplication()
	}
	s.agendaReady = true
	s.agendaDirty = false
	if collectChanges {
		s.applyAutoFocus(changes)
		s.emitAgendaEvents(ctx, changes)
	}
	return changes, true, nil
}

func (s *Session) rebuildReteRuntime(ctx context.Context, revision *Ruleset, facts []FactSnapshot) error {
	if s == nil || revision == nil {
		return nil
	}
	rete, err := newReteRuntime(revision)
	if err != nil {
		s.rete = nil
		return err
	}
	if err := rete.resetGraphBeta(ctx, facts); err != nil {
		return err
	}
	s.rete = rete
	if s.propagationCounters != nil {
		s.propagationCounters.recordGraphRebuild(s.propagationCounterPhase())
	}
	s.syncPropagationCounters()
	return nil
}

func (s *Session) restoreReteAfterPropagationFailure() {
	if s == nil || s.revision == nil {
		return
	}
	_ = s.rebuildReteRuntime(context.Background(), s.revision, s.detachedFactsByInsertionOrder())
}

func (s *Session) updateReteAlphaAfterAssert(ctx context.Context, fact FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if s == nil {
		return reteAgendaDelta{}, nil
	}
	if s.revision != nil && !s.revision.factMayAffectReteByTarget(fact.name, fact.templateKey) {
		return reteAgendaDelta{}, nil
	}
	if s.rete == nil {
		return reteAgendaDelta{}, s.rebuildReteRuntime(ctx, s.revision, s.detachedFactsByInsertionOrder())
	}
	if s.rete.usesGraphBeta() {
		return s.rete.insertBetaFactWithOrigin(ctx, fact, origin, span)
	}
	return reteAgendaDelta{}, s.rete.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterAssertGenerated(ctx context.Context, fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) (reteAgendaDelta, error) {
	if s == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	if s.revision != nil && !s.revision.factMayAffectReteByTarget(fact.name, fact.templateKey) {
		return reteAgendaDelta{}, nil
	}
	if s.rete == nil {
		return reteAgendaDelta{}, s.rebuildReteRuntime(ctx, s.revision, s.detachedFactsByInsertionOrder())
	}
	if s.rete.usesGraphBeta() {
		return s.rete.insertBetaFactGenerated(ctx, fact, origin, span)
	}
	return reteAgendaDelta{}, s.rete.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterRetract(ctx context.Context, fact FactSnapshot, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.rete == nil {
		return reteAgendaDelta{}, nil
	}
	if s.rete.usesGraphBeta() {
		return s.rete.removeBetaFact(ctx, fact, origin, s.propagationCounters)
	}
	return reteAgendaDelta{}, s.rete.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterRetractGenerated(ctx context.Context, fact *workingFact, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.rete == nil || fact == nil {
		return reteAgendaDelta{}, nil
	}
	if s.rete.usesGraphBeta() {
		return s.rete.removeBetaFactGenerated(ctx, fact, origin, s.propagationCounters)
	}
	return reteAgendaDelta{}, s.rete.unsupportedRuntimeError()
}

func (s *Session) updateReteAlphaAfterModify(ctx context.Context, before, after FactSnapshot, changes []FieldChange, duplicateChanged bool, origin mutationOrigin) (reteAgendaDelta, error) {
	if s == nil || s.rete == nil {
		return reteAgendaDelta{}, nil
	}
	if s.rete.usesGraphBeta() {
		return s.rete.updateBetaFact(ctx, before, after, changes, duplicateChanged, origin, s.propagationCounters)
	}
	return reteAgendaDelta{}, s.rete.unsupportedRuntimeError()
}

func (s *Session) rebuildFieldSlots(previous, revision *Ruleset) {
	if s == nil {
		return
	}
	for i := range s.facts {
		fact := &s.facts[i]
		template, ok := revision.templateByKey(fact.templateKey)
		if !ok {
			if fact.fields == nil {
				fact.fields = materializeFieldsFromSlots(fact.fieldSlots, fact.fieldSpecsForRevision(previous))
			}
			if fact.fieldPresence == nil {
				fact.fieldPresence = materializePresenceFromSlots(fact.fieldSlots, fact.fieldSpecsForRevision(previous))
			}
			fact.fieldSlots = nil
			continue
		}
		fields := fact.fields
		if fields == nil {
			fields = materializeFieldsFromSlots(fact.fieldSlots, fact.fieldSpecsForRevision(previous))
		}
		presence := fact.fieldPresence
		if presence == nil {
			presence = materializePresenceFromSlots(fact.fieldSlots, fact.fieldSpecsForRevision(previous))
		}
		fieldSlots := revision.buildFieldSlots(template, fields, presence)
		if len(fieldSlots) > 0 {
			fact.fields = nil
			fact.fieldSlots = fieldSlots
			fact.fieldPresence = nil
		} else {
			fact.fieldSlots = nil
			if fields != nil {
				fact.fields = fields
			}
			fact.fieldPresence = cloneFieldPresence(presence)
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
		currentTemplate, ok := current.TemplateByKey(templateKey)
		if !ok {
			return ErrIncompatibleRuleset
		}
		nextTemplate, ok := next.TemplateByKey(templateKey)
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
		nextTemplate, ok := next.TemplateByKey(initial.TemplateKey)
		if !ok {
			return ErrIncompatibleRuleset
		}
		if _, _, err := nextTemplate.applyDefaultsAndValidate(initial.Fields); err != nil {
			return ErrIncompatibleRuleset
		}
	}

	return nil
}

func templatesCompatible(left, right Template) bool {
	return reflect.DeepEqual(left.spec(), right.spec())
}

func (s *Session) emitAgendaEvents(ctx context.Context, changes []agendaChange) {
	if s == nil || len(s.listeners) == 0 || len(changes) == 0 {
		return
	}
	rulesetID := RulesetID("")
	if s.revision != nil {
		rulesetID = s.revision.ID()
	}
	for _, change := range changes {
		s.nextEventSequence++
		s.emitEvent(ctx, change.event(s.id, rulesetID, s.nextEventSequence, s.eventClock()))
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

	if id.Generation() != s.generation {
		if id.Generation() != 0 && id.Generation() < s.generation {
			return ModifyResult{Status: ModifyStale}, reteAgendaDelta{}, ErrStaleFactID
		}
		return ModifyResult{Status: ModifyMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	state := s.activeFactWorkspace()
	fact, ok := state.workingFactByID(id)
	if !ok {
		return ModifyResult{Status: ModifyMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision)
	if s.factHasLogicalSupport(id) {
		return ModifyResult{Status: ModifyLogicalSupport, Fact: before}, reteAgendaDelta{}, ErrLogicalFactModify
	}
	template, templateExists := s.revision.templateByKey(fact.templateKey)

	var beforeFields Fields
	var beforePresence map[string]FieldPresence
	var proposedFields Fields
	var proposedPresence map[string]FieldPresence
	var proposedFieldSlots []factSlot
	var fieldChanges []FieldChange
	var noChange bool
	var err error
	if templateExists && s.revision.usesFieldSlots(template) && len(fact.fieldSlots) > 0 {
		proposedFieldSlots, fieldChanges, noChange, err = template.applyPatchToFieldSlots(fact.fieldSlots, patch)
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
	newDupIndex := makeDuplicateIndexForValidatedFact(fact.name, template, proposedFields, proposedFieldSlots)
	oldDuplicate := fact.publicDuplicateKey(s.revision)

	if duplicatePolicy != DuplicateAllow {
		if newDupIndex.kind == duplicateIndexStructural {
			duplicate := false
			state.factsByDuplicate.forEachStructuralFactID(newDupIndex, func(existingID FactID) bool {
				if existingID == fact.id {
					return true
				}
				existing, ok := state.workingFactByID(existingID)
				if ok && structuralDuplicateSlotsEqual(template, proposedFieldSlots, existing.fieldSlots) {
					duplicate = true
					return false
				}
				return true
			})
			if duplicate {
				return ModifyResult{Status: ModifyDuplicate, Fact: before}, reteAgendaDelta{}, ErrDuplicateFact
			}
		} else if existingID, ok := state.factsByDuplicate.get(newDupIndex); ok && existingID != fact.id {
			return ModifyResult{Status: ModifyDuplicate, Fact: before}, reteAgendaDelta{}, ErrDuplicateFact
		}
	}

	if noChange {
		return ModifyResult{Status: ModifyNoOp, Fact: before}, reteAgendaDelta{}, nil
	}

	modifyMark := state.markFactModify(fact, duplicatePolicy != DuplicateAllow && fact.dupIndex != newDupIndex)
	state.recency++

	if duplicatePolicy != DuplicateAllow && fact.dupIndex != newDupIndex {
		if !fact.dupIndex.isZero() {
			state.factsByDuplicate.deleteFact(fact.dupIndex, fact.id)
		}
		if !newDupIndex.isZero() {
			state.factsByDuplicate.set(newDupIndex, fact.id)
		}
	}

	oldVersion := fact.version
	newDuplicate := newDupIndex.publicKeyForTemplate(fact.name, template)
	if newDupIndex.kind == duplicateIndexStructural {
		newDuplicate = makeDuplicateKeyForTemplateWithSlots(fact.name, template, proposedFields, proposedFieldSlots)
	}
	fact.version++
	fact.recency = state.recency
	if len(proposedFieldSlots) > 0 {
		fact.fields = nil
		fact.fieldSlots = proposedFieldSlots
		fact.fieldPresence = nil
	} else {
		fact.fields = proposedFields
		fact.fieldSlots = nil
		fact.fieldPresence = proposedPresence
	}
	fact.dupIndex = newDupIndex

	after := fact.snapshotForRevision(s.revision)
	duplicateChanged := oldDuplicate != newDuplicate
	agendaDelta, err := s.updateReteAlphaAfterModify(ctx, before, after, fieldChanges, duplicateChanged, origin)
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
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	if resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, agendaDelta.resolvedDemands, agendaDelta.resolvedOwners, origin); err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	} else {
		agendaDelta = mergeReteAgendaDelta(agendaDelta, resolvedDelta)
	}
	demandState := s.activeFactWorkspace()
	if demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &demandState, agendaDelta.demands, origin); err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	} else {
		s.commitFactWorkspace(demandState)
		agendaDelta = mergeReteAgendaDelta(agendaDelta, demandDelta)
	}
	supportEvent := reteGraphPropagationEvent{
		origin:           origin,
		sourceGeneration: after.Generation(),
	}
	cascadeDelta, err := s.removeLogicalSupportsForPropagationEventDelta(ctx, supportEvent, agendaDelta)
	if err != nil {
		return ModifyResult{Status: ModifyValidationFailure, Fact: before}, agendaDelta, err
	}
	agendaDelta = mergeReteAgendaDelta(agendaDelta, cascadeDelta)
	delta := MutationDelta{
		Kind:           MutationModify,
		Generation:     s.generation,
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
	if len(s.listeners) > 0 {
		s.emitEvent(ctx, Event{
			SessionID:      s.id,
			RulesetID:      s.revision.ID(),
			Sequence:       s.nextEventSequence + 1,
			Timestamp:      s.eventClock(),
			Type:           EventFactModified,
			Generation:     s.generation,
			Recency:        fact.recency,
			RuleID:         origin.RuleID,
			RuleRevisionID: origin.RuleRevisionID,
			ActivationID:   origin.activationID(),
			FactIDs:        []FactID{fact.id},
			Delta:          &delta,
		})
		s.nextEventSequence++
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

func (t Template) applyPatchToFieldSlots(current []factSlot, patch FactPatch) ([]factSlot, []FieldChange, bool, error) {
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

func (t Template) clearFieldSlot(slots []factSlot, slot int) error {
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

func (t Template) setFieldSlot(slots []factSlot, slot int, value Value) error {
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

func (t Template) fieldValidationForSlot(slot int) (fieldValidationSpec, bool) {
	if slot < 0 || len(t.fieldValidation) != len(t.fields) || slot >= len(t.fieldValidation) {
		return fieldValidationSpec{}, false
	}
	return t.fieldValidation[slot], true
}

func changedFieldSlots(template Template, beforeSlots, afterSlots []factSlot) []FieldChange {
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

func newFactModifySummary(template Template, changes []FieldChange, duplicateChanged bool) factModifySummary {
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

func (s *Session) detachedFactsByInsertionOrder() []FactSnapshot {
	if s == nil || len(s.insertionOrder) == 0 {
		return nil
	}
	facts := make([]FactSnapshot, 0, len(s.insertionOrder))
	for _, id := range s.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			continue
		}
		facts = append(facts, fact.detachedSnapshotForRevision(s.revision))
	}
	return facts
}

func (s *Session) snapshotLockedWithOptions(includeTargetIndexes bool, cloneFacts bool) Snapshot {
	facts := make([]FactSnapshot, 0, len(s.insertionOrder))
	for _, id := range s.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			continue
		}
		if cloneFacts {
			facts = append(facts, fact.snapshotForRevision(s.revision))
		} else {
			facts = append(facts, fact.detachedSnapshotForRevision(s.revision))
		}
	}

	snapshot := Snapshot{
		sessionID:  s.id,
		rulesetID:  s.revision.ID(),
		revision:   s.revision,
		generation: s.generation,
		facts:      facts,
		support:    s.currentSupportGraph(),
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
	insertionOrder            []FactID
	factsByID                 map[FactID]int
	factsBySequence           []int
	factsByDuplicate          duplicateIndexes
	duplicateReserveRulesetID RulesetID
	factsByTemplate           map[TemplateKey][]FactID
	factsByName               map[string][]FactID
	slotStorage               []factSlot
	skipFactTargetIndexes     bool
	factTargetIndexesDirty    bool
}

type factWorkspaceInsertMark struct {
	sequence               uint64
	recency                Recency
	factsLen               int
	insertionOrderLen      int
	slotStorageLen         int
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
	factsByDuplicate       duplicateIndexes
	restoreDuplicateIndex  bool
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
	var stringsCapacity, singleIntCapacity, doubleIntCapacity, intStringCapacity, stringIntCapacity, scalarCapacity, structuralCapacity int
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
	if scalarCapacity > 0 && i.scalars == nil {
		i.scalars = make(map[duplicateIndexKey]FactID, scalarCapacity)
	}
	if structuralCapacity > 0 {
		i.structural.reserve(structuralCapacity)
	}
}

func duplicateReserveKind(template Template) duplicateIndexKind {
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
	}
	return template.duplicateIndexMode
}

func duplicateTemplateSlotKind(template Template, slot int) ValueKind {
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
	case duplicateIndexStructural:
		return i.structural.get(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash})
	default:
		factID, ok := i.scalars[key]
		return factID, ok
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
	if key.kind != duplicateIndexStructural {
		if existingID, ok := i.get(key); ok && existingID == factID {
			i.delete(key)
		}
		return
	}
	i.structural.deleteFact(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash}, factID)
}

func (i duplicateIndexes) forEachStructuralFactID(key duplicateIndexKey, fn func(FactID) bool) {
	if key.kind != duplicateIndexStructural || fn == nil {
		return
	}
	i.structural.forEachFactID(duplicateStructuralIndexKey{templateKey: key.templateKey, hash: key.hash}, fn)
}

func (i duplicateIndexes) len() int {
	return len(i.strings) + len(i.singleInt) + len(i.doubleInt) + len(i.intString) + len(i.stringInt) + len(i.scalars) + i.structural.len()
}

type duplicateStructuralIndexEntry struct {
	key   duplicateStructuralIndexKey
	first FactID
	rest  []FactID
	state uint8
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
		w.facts = w.facts[:0]
	}
	if w.slotStorage == nil {
		w.slotStorage = make([]factSlot, 0, initialCapacity)
	} else {
		w.slotStorage = w.slotStorage[:0]
	}
}

func (w *factWorkspace) reserveDuplicateIndexes(revision *Ruleset) {
	if w == nil || revision == nil {
		return
	}
	rulesetID := revision.ID()
	if w.duplicateReserveRulesetID == rulesetID {
		return
	}
	w.factsByDuplicate.reserve(revision, cap(w.facts))
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
	perTemplate := max((cap(w.facts)+templateCount-1)/templateCount+runFactReservePerRule, 1)
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
	if w == nil || len(w.facts) == 0 {
		return
	}
	index, ok := w.factRowIndex(id)
	if !ok || index < 0 || index >= len(w.facts) || w.facts[index].id != id {
		return
	}
	last := len(w.facts) - 1
	if index != last {
		moved := w.facts[last]
		w.facts[index] = moved
		w.setFactRowIndex(moved.id, index)
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

func (w *factWorkspace) reserveGeneratedFactCapacity(revision *Ruleset, factCount, slotCount int) {
	if w == nil {
		return
	}
	if factCount > 0 {
		factCapacity := saturatingAddInt(len(w.facts), factCount)
		if cap(w.facts) < factCapacity {
			nextFacts := make([]workingFact, len(w.facts), factCapacity)
			copy(nextFacts, w.facts)
			w.facts = nextFacts
		}
		orderCapacity := saturatingAddInt(len(w.insertionOrder), factCount)
		if cap(w.insertionOrder) < orderCapacity {
			nextOrder := make([]FactID, len(w.insertionOrder), orderCapacity)
			copy(nextOrder, w.insertionOrder)
			w.insertionOrder = nextOrder
		}
		w.reserveFactRowSequenceRows(factCount)
	}
	if slotCount > 0 {
		w.reserveSlotStorage(saturatingAddInt(len(w.slotStorage), slotCount))
	}
	w.reserveTemplateIndexes(revision)
}

func (w *factWorkspace) reserveGeneratedFactInsert(revision *Ruleset, slotCount int) {
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
	if slotCount > 0 && cap(w.slotStorage)-len(w.slotStorage) < slotCount {
		nextCapacity := nextGeneratedSlotCapacity(len(w.slotStorage), cap(w.slotStorage), slotCount, revision)
		w.reserveSlotStorage(nextCapacity)
	}
}

func (w *factWorkspace) reserveFactRowSequenceRows(factCount int) {
	if w == nil || factCount <= 0 {
		return
	}
	target, ok := factRowSequenceReserveTarget(w.sequence, factCount)
	if !ok || target <= len(w.factsBySequence) {
		return
	}
	oldLen := len(w.factsBySequence)
	if cap(w.factsBySequence) < target {
		next := make([]int, target)
		copy(next, w.factsBySequence)
		w.factsBySequence = next
	} else {
		w.factsBySequence = w.factsBySequence[:target]
	}
	for i := oldLen; i < target; i++ {
		w.factsBySequence[i] = -1
	}
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

	w.facts = append(w.facts, fact)
	stored := &w.facts[len(w.facts)-1]
	w.setFactRowIndex(stored.id, len(w.facts)-1)
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

func (w *factWorkspace) rollbackGeneratedFactSlots(mark int) {
	if w == nil || mark < 0 || mark > len(w.slotStorage) {
		return
	}
	for i := mark; i < len(w.slotStorage); i++ {
		w.slotStorage[i] = factSlot{}
	}
	w.slotStorage = w.slotStorage[:mark]
}

func (w *factWorkspace) markGeneratedFactInsert() factWorkspaceInsertMark {
	if w == nil {
		return factWorkspaceInsertMark{}
	}
	return factWorkspaceInsertMark{
		sequence:               w.sequence,
		recency:                w.recency,
		factsLen:               len(w.facts),
		insertionOrderLen:      len(w.insertionOrder),
		slotStorageLen:         len(w.slotStorage),
		factTargetIndexesDirty: w.factTargetIndexesDirty,
	}
}

func (w *factWorkspace) rollbackGeneratedFactInsert(mark factWorkspaceInsertMark, fact *workingFact) {
	if w == nil {
		return
	}
	if fact != nil {
		if !fact.dupIndex.isZero() {
			w.factsByDuplicate.deleteFact(fact.dupIndex, fact.id)
		}
		if !fact.targetIndexesSkipped {
			w.removeFactTargetIndexes(fact.templateKey, fact.name, fact.id)
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
	w.rollbackGeneratedFactSlots(mark.slotStorageLen)
}

func (w *factWorkspace) markFactModify(fact *workingFact, restoreDuplicateIndex bool) factWorkspaceModifyMark {
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
		restoreDuplicateIndex:  restoreDuplicateIndex,
	}
	if restoreDuplicateIndex {
		mark.factsByDuplicate = cloneDuplicateIndexes(w.factsByDuplicate)
	}
	return mark
}

func (w *factWorkspace) rollbackFactModify(mark factWorkspaceModifyMark) {
	if w == nil {
		return
	}
	w.recency = mark.recency
	w.factTargetIndexesDirty = mark.factTargetIndexesDirty
	if mark.restoreDuplicateIndex {
		w.factsByDuplicate = mark.factsByDuplicate
	}
	if mark.factIndex >= 0 && mark.factIndex < len(w.facts) {
		w.facts[mark.factIndex] = mark.fact
	}
}

func (w *factWorkspace) workingFactByID(id FactID) (*workingFact, bool) {
	if w == nil {
		return nil, false
	}
	index, ok := w.factRowIndex(id)
	if !ok || index < 0 || index >= len(w.facts) {
		return nil, false
	}
	fact := &w.facts[index]
	if fact.id != id {
		return nil, false
	}
	return fact, true
}

func (w *factWorkspace) factRowIndex(id FactID) (int, bool) {
	if w == nil || id.IsZero() {
		return 0, false
	}
	if id.generation == w.generation && id.sequence > 0 {
		index := int(id.sequence - 1)
		if uint64(index) == id.sequence-1 && index < len(w.factsBySequence) {
			row := w.factsBySequence[index]
			if row >= 0 {
				return row, true
			}
		}
	}
	if w.factsByID == nil {
		return 0, false
	}
	index, ok := w.factsByID[id]
	return index, ok
}

func (w *factWorkspace) setFactRowIndex(id FactID, row int) {
	if w == nil || id.IsZero() || row < 0 {
		return
	}
	if id.generation == w.generation && id.sequence > 0 {
		index := int(id.sequence - 1)
		if uint64(index) == id.sequence-1 {
			if len(w.factsBySequence) <= index {
				oldLen := len(w.factsBySequence)
				if cap(w.factsBySequence) <= index {
					next := make([]int, index+1)
					copy(next, w.factsBySequence)
					w.factsBySequence = next
				} else {
					w.factsBySequence = w.factsBySequence[:index+1]
				}
				for i := oldLen; i <= index; i++ {
					w.factsBySequence[i] = -1
				}
			}
			w.factsBySequence[index] = row
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
	if id.generation == w.generation && id.sequence > 0 {
		index := int(id.sequence - 1)
		if uint64(index) == id.sequence-1 && index < len(w.factsBySequence) {
			w.factsBySequence[index] = -1
			return
		}
	}
	if w.factsByID != nil {
		delete(w.factsByID, id)
	}
}

func (w *factWorkspace) structuralDuplicateFact(template Template, slots []factSlot, key duplicateIndexKey) (*workingFact, bool) {
	if w == nil || key.kind != duplicateIndexStructural {
		return nil, false
	}
	var found *workingFact
	w.factsByDuplicate.forEachStructuralFactID(key, func(id FactID) bool {
		existing, ok := w.workingFactByID(id)
		if !ok {
			return true
		}
		if structuralDuplicateSlotsEqual(template, slots, existing.fieldSlots) {
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
		if equal, ok := plan.structuralScalarDuplicateSlotsEqual(slots, existing.fieldSlots); ok {
			if equal {
				found = existing
				return false
			}
			return true
		}
		if structuralDuplicateSlotsEqual(plan.template, slots, existing.fieldSlots) {
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

func (w *factWorkspace) detachedFactsByInsertionOrder(revision *Ruleset) []FactSnapshot {
	return w.detachedFactsByInsertionOrderInto(nil, revision)
}

func (w *factWorkspace) detachedFactsByInsertionOrderInto(dst []FactSnapshot, revision *Ruleset) []FactSnapshot {
	if w == nil || w.insertionOrder == nil || len(w.insertionOrder) == 0 {
		return dst[:0]
	}
	if cap(dst) < len(w.insertionOrder) {
		dst = make([]FactSnapshot, 0, len(w.insertionOrder))
	} else {
		dst = dst[:0]
	}
	for _, id := range w.insertionOrder {
		fact, ok := w.workingFactByID(id)
		if !ok {
			continue
		}
		dst = append(dst, fact.detachedSnapshotForRevision(revision))
	}
	return dst
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
				return existing, existing.publicDuplicateKey(revision), false, nil
			}
		} else {
			existingID, ok := w.factsByDuplicate.get(duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					return existing, existing.publicDuplicateKey(revision), false, nil
				}
				w.factsByDuplicate.delete(duplicateIndex)
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
		id:            id,
		name:          name,
		templateKey:   templateKey,
		version:       1,
		recency:       w.recency,
		fields:        canonical,
		fieldSlots:    fieldSlots,
		fieldPresence: presence,
		dupIndex:      duplicateIndex,
	}

	if len(fieldSlots) > 0 {
		fact.fields = nil
		fact.fieldPresence = nil
	}

	stored := w.storeFact(fact)
	if templateDuplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	w.addFactTargetIndexes(templateKey, name, id)
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, duplicateKey, true, nil
}

func (w *factWorkspace) insertFactSlots(revision *Ruleset, generation Generation, template Template, fieldSlots []factSlot, materializeDuplicateKey bool) (*workingFact, DuplicateKey, bool, error) {
	name := template.Name()
	templateKey := template.Key()
	duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
	if template.duplicatePolicy != DuplicateAllow {
		if duplicateIndex.kind == duplicateIndexStructural {
			if existing, ok := w.structuralDuplicateFact(template, fieldSlots, duplicateIndex); ok {
				if materializeDuplicateKey {
					return existing, existing.publicDuplicateKey(revision), false, nil
				}
				return existing, "", false, nil
			}
		} else {
			existingID, ok := w.factsByDuplicate.get(duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					if materializeDuplicateKey {
						return existing, existing.publicDuplicateKey(revision), false, nil
					}
					return existing, "", false, nil
				}
				w.factsByDuplicate.delete(duplicateIndex)
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
	if !materializeDuplicateKey {
		w.reserveGeneratedFactInsert(revision, len(fieldSlots))
		storedSlots = w.storeGeneratedFactSlots(fieldSlots)
	}
	fact := workingFact{
		id:          id,
		name:        name,
		templateKey: templateKey,
		version:     1,
		recency:     w.recency,
		fieldSlots:  storedSlots,
		dupIndex:    duplicateIndex,
	}

	stored := w.storeFact(fact)
	if template.duplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	w.addFactTargetIndexes(templateKey, name, id)
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, duplicateKey, true, nil
}

func (w *factWorkspace) insertPreparedGeneratedFactSlots(revision *Ruleset, generation Generation, template Template, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
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

func (w *factWorkspace) insertPreparedEngineGeneratedFactSlots(revision *Ruleset, generation Generation, template Template, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
	return w.insertPreparedGeneratedFactSlotsUnchecked(revision, generation, template, fieldSlots, slotMark, factTargetIndexSkip)
}

func (w *factWorkspace) insertPreparedGeneratedFactSlotsWithPlan(revision *Ruleset, generation Generation, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
	return w.insertPreparedGeneratedFactSlotsWithPlanUnchecked(revision, generation, plan, fieldSlots, slotMark, factTargetIndexDirty)
}

func (w *factWorkspace) insertPreparedGeneratedFactSlotsUnchecked(revision *Ruleset, generation Generation, template Template, fieldSlots []factSlot, slotMark int, indexMode factTargetIndexMode) (*workingFact, DuplicateKey, bool, error) {
	plan, ok := revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	return w.insertPreparedGeneratedFactSlotsWithPlanUnchecked(revision, generation, plan, fieldSlots, slotMark, indexMode)
}

func (w *factWorkspace) insertPreparedGeneratedFactSlotsWithPlanUnchecked(revision *Ruleset, generation Generation, plan *compiledGeneratedFactInsertPlan, fieldSlots []factSlot, slotMark int, indexMode factTargetIndexMode) (*workingFact, DuplicateKey, bool, error) {
	name := plan.name
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
			existingID, ok := w.factsByDuplicate.get(duplicateIndex)
			if ok {
				existing, ok := w.workingFactByID(existingID)
				if ok {
					w.rollbackGeneratedFactSlots(slotMark)
					return existing, "", false, nil
				}
				w.factsByDuplicate.delete(duplicateIndex)
			}
		}
	}

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	fact := workingFact{
		id:                   id,
		name:                 name,
		templateKey:          templateKey,
		version:              1,
		recency:              w.recency,
		fieldSlots:           fieldSlots,
		dupIndex:             duplicateIndex,
		targetIndexesSkipped: indexMode == factTargetIndexSkip,
	}

	stored := w.storeFact(fact)
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

func (w *factWorkspace) applyInitialFacts(revision *Ruleset, initials []SessionInitialFact) error {
	for _, initial := range initials {
		if initial.TemplateKey == "" && initial.Name == "" {
			return &ValidationError{TemplateName: "session", Reason: "initializer must set name or template key"}
		}
		if initial.TemplateKey != "" && initial.Name != "" {
			return &ValidationError{TemplateName: initial.Name, Reason: "initializer must not set both name and template key"}
		}
		_, _, _, err := w.insertFact(revision, w.generation, initial.Name, initial.TemplateKey, initial.Fields)
		if err != nil {
			return err
		}
	}
	return nil
}

type compiledSessionInitialFact struct {
	name            string
	templateKey     TemplateKey
	fields          Fields
	fieldSlots      []factSlot
	fieldSpecs      []FieldSpec
	fieldPresence   map[string]FieldPresence
	duplicatePolicy DuplicatePolicy
	duplicateIndex  duplicateIndexKey
	duplicateKey    DuplicateKey
	shareFields     bool
	shareSlots      bool
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
	if initial.TemplateKey == "" && initial.Name == "" {
		return compiledSessionInitialFact{}, &ValidationError{TemplateName: "session", Reason: "initializer must set name or template key"}
	}
	if initial.TemplateKey != "" && initial.Name != "" {
		return compiledSessionInitialFact{}, &ValidationError{TemplateName: initial.Name, Reason: "initializer must not set both name and template key"}
	}

	name := initial.Name
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

	if templateExists && revision.usesFieldSlots(template) {
		fieldSlots, err := template.buildValidatedFieldSlots(initial.Fields)
		if err != nil {
			return compiledSessionInitialFact{}, err
		}

		duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
		duplicateKey := duplicateIndex.publicKeyForTemplate(name, template)
		if duplicateIndex.kind == duplicateIndexStructural {
			duplicateKey = makeDuplicateKeyForTemplateWithSlots(name, template, nil, fieldSlots)
		}
		return compiledSessionInitialFact{
			name:            name,
			templateKey:     templateKey,
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

func (w *factWorkspace) applyCompiledInitialFactsInto(initials []compiledSessionInitialFact, dst []FactSnapshot, revision *Ruleset) []FactSnapshot {
	if cap(dst) < len(initials) {
		dst = make([]FactSnapshot, 0, len(initials))
	} else {
		dst = dst[:0]
	}
	for _, initial := range initials {
		fact := w.insertCompiledInitialFact(initial)
		if fact != nil {
			dst = append(dst, fact.detachedSnapshotForRevision(revision))
		}
	}
	return dst
}

func (w *factWorkspace) insertCompiledInitialFact(initial compiledSessionInitialFact) *workingFact {
	w.sequence++
	w.recency++
	id := newFactID(w.generation, w.sequence)
	fact := workingFact{
		id:          id,
		name:        initial.name,
		templateKey: initial.templateKey,
		version:     1,
		recency:     w.recency,
		dupIndex:    initial.duplicateIndex,
	}
	if initial.shareFields {
		fact.fields = initial.fields
	} else {
		fact.fields = cloneFields(initial.fields)
	}
	if initial.shareSlots {
		fact.fieldSlots = initial.fieldSlots
	} else {
		fact.fieldSlots = cloneFactSlots(initial.fieldSlots)
	}
	fact.fieldPresence = cloneFieldPresence(initial.fieldPresence)

	if len(fact.fieldSlots) > 0 {
		fact.fields = nil
		fact.fieldPresence = nil
	}

	stored := w.storeFact(fact)
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
	return activation != nil && activation.mutationOrigin() == origin
}

func (s *Session) shouldQueueMutationDuringRun(origin mutationOrigin) bool {
	if s == nil || !origin.isZero() || !s.runGuardHeld() {
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
		s.agendaDirty = true
		s.agendaReady = false
	}
}

func (s *Session) recordRunAgendaDelta(delta reteAgendaDelta) error {
	if s == nil {
		return nil
	}
	if !delta.supported {
		if s.propagationCounters != nil {
			s.propagationCounters.recordUnsupportedAgendaDelta()
		}
		return fmt.Errorf("%w: unsupported agenda delta during run", ErrUnsupportedRuntime)
	}
	if s.agendaDirty {
		return fmt.Errorf("%w: cannot record run agenda delta while agenda is dirty", ErrUnsupportedRuntime)
	}
	if s.canApplyRunAgendaDeltaDirect(delta) {
		return s.applyRunAgendaDeltaDirect(delta)
	}
	total := len(delta.added) + len(delta.removed) + len(delta.updated)
	if !s.runAgendaPending {
		s.runAgendaDeltas = s.runAgendaDeltas[:0]
		if s.runAgendaBuckets == nil {
			s.runAgendaBuckets = make(map[candidateIdentity]int, total)
		} else {
			clear(s.runAgendaBuckets)
		}
		for i := range s.runAgendaStates {
			s.runAgendaStates[i] = runAgendaDeltaState{}
		}
		s.runAgendaStates = slices.Grow(s.runAgendaStates[:0], total)
		s.runAgendaPending = true
	} else if s.runAgendaBuckets == nil {
		s.runAgendaBuckets = make(map[candidateIdentity]int, total)
	}
	if err := s.recordRunAgendaDeltaTokens(delta); err != nil {
		s.markAgendaDirty()
		return err
	}
	return nil
}

func (s *Session) canApplyRunAgendaDeltaDirect(delta reteAgendaDelta) bool {
	if s == nil || !delta.supported || s.agendaDirty || !s.agendaReady {
		return false
	}
	if s.revision == nil || s.revision.hasAutoFocusRules() || len(s.listeners) > 0 {
		return false
	}
	if s.runAgendaPending && !s.runAgendaDirect {
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
	s.runAgendaPending = true
	s.runAgendaDirect = true
	return nil
}

func (s *Session) applyReteAgendaDeltaDirect(ctx context.Context, delta reteAgendaDelta) (bool, error) {
	if s == nil || s.revision == nil {
		return true, ErrInvalidRuleset
	}
	if s.agenda == nil {
		s.agenda = newAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if !delta.supported || s.rete == nil || !s.agendaReady || s.agendaDirty {
		if s.propagationCounters != nil && !delta.supported {
			s.propagationCounters.recordUnsupportedAgendaDelta()
		}
		return false, nil
	}
	if len(delta.updated) != 0 {
		if err := s.agenda.applyTerminalTokenUpdates(ctx, s.revision, delta.updated); err != nil {
			return true, err
		}
	}
	if err := s.applyTerminalTokenDeltasWithoutChangesAndAttach(ctx, delta.removed, delta.added); err != nil {
		return true, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordAgendaDeltaApplication()
	}
	s.agendaReady = true
	s.agendaDirty = false
	return true, nil
}

func (s *Session) applyTerminalTokenDeltasWithoutChangesAndAttach(ctx context.Context, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta) error {
	if s == nil || s.agenda == nil || s.revision == nil {
		return ErrInvalidRuleset
	}
	if len(removed) <= 1 && len(added) <= 1 {
		handle, err := s.agenda.applySingleTerminalTokenDeltasWithoutChanges(ctx, s.revision, removed, added)
		if err != nil {
			return err
		}
		if len(added) == 1 {
			s.attachTerminalActivationHandle(added[0], handle, nil)
		}
		return nil
	}
	_, err := s.agenda.applyTerminalTokenDeltasInternal(ctx, s.revision, removed, added, false, s.attachTerminalActivationHandle)
	return err
}

func (s *Session) attachTerminalActivationHandle(token reteTerminalTokenDelta, handle activationHandle, act *activation) {
	if s == nil || handle.isZero() {
		return
	}
	if s.rete != nil && s.rete.graphBeta != nil {
		s.rete.graphBeta.setTerminalActivationHandle(token.terminalID, token.terminalRow, handle)
	}
	s.applyAutoFocusForActivation(act, handle)
}

func (s *Session) applyAutoFocusForActivation(act *activation, handle activationHandle) {
	if s == nil || s.revision == nil || !s.revision.hasAutoFocusRules() || len(s.listeners) > 0 {
		return
	}
	if act == nil && !handle.isZero() && s.agenda != nil {
		if resolved, ok := s.agenda.activationByHandlePtr(handle); ok {
			act = resolved
		}
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
	for index := s.runAgendaBuckets[identity]; index != 0; {
		state := &s.runAgendaStates[index-1]
		if terminalTokenDeltasEqual(s.revision, state.token, reteTerminalTokenDelta{
			ruleRevisionID: update.ruleRevisionID,
			token:          update.before,
			identity:       identity,
		}) {
			if state.present {
				if state.initial && !state.updated {
					state.updateBefore = state.token.token
					state.updated = true
				}
				state.token = reteTerminalTokenDelta{
					ruleRevisionID: update.ruleRevisionID,
					token:          update.after,
					identity:       identity,
				}
			}
			return nil
		}
		index = state.next
	}
	if existing, _, ok := s.agenda.activationForTerminalTokenIdentity(rule, update.before, identity); ok && existing.status == activationStatusPending {
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
			next: s.runAgendaBuckets[identity],
		}
		s.runAgendaStates = append(s.runAgendaStates, state)
		s.runAgendaBuckets[identity] = len(s.runAgendaStates)
	}
	return nil
}

func (s *Session) reconcileRunAgendaDelta(ctx context.Context) error {
	if s == nil || !s.runAgendaPending {
		return nil
	}
	if s.runAgendaDirect {
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
	if _, ok, err := s.applyReteAgendaDeltaInternal(ctx, delta, len(s.listeners) > 0); err != nil {
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

func (s *Session) abandonRunAgendaDelta() {
	if s == nil || !s.runAgendaPending {
		return
	}
	s.markAgendaDirty()
}

func (s *Session) clearRunAgendaDelta() {
	if s == nil || !s.runAgendaPending {
		return
	}
	clear(s.runAgendaDelta.added)
	clear(s.runAgendaDelta.removed)
	clear(s.runAgendaDelta.updated)
	s.runAgendaDelta.added = s.runAgendaDelta.added[:0]
	s.runAgendaDelta.removed = s.runAgendaDelta.removed[:0]
	s.runAgendaDelta.updated = s.runAgendaDelta.updated[:0]
	s.runAgendaDelta.supported = false
	for i := range s.runAgendaDeltas {
		clear(s.runAgendaDeltas[i].added)
		clear(s.runAgendaDeltas[i].removed)
		clear(s.runAgendaDeltas[i].updated)
		s.runAgendaDeltas[i].added = s.runAgendaDeltas[i].added[:0]
		s.runAgendaDeltas[i].removed = s.runAgendaDeltas[i].removed[:0]
		s.runAgendaDeltas[i].updated = s.runAgendaDeltas[i].updated[:0]
		s.runAgendaDeltas[i].supported = false
	}
	s.runAgendaDeltas = s.runAgendaDeltas[:0]
	for i := range s.runAgendaStates {
		s.runAgendaStates[i] = runAgendaDeltaState{}
	}
	s.runAgendaStates = s.runAgendaStates[:0]
	if s.runAgendaBuckets != nil {
		clear(s.runAgendaBuckets)
	}
	clear(s.runAgendaAdded)
	clear(s.runAgendaRemoved)
	clear(s.runAgendaUpdated)
	s.runAgendaAdded = s.runAgendaAdded[:0]
	s.runAgendaRemoved = s.runAgendaRemoved[:0]
	s.runAgendaUpdated = s.runAgendaUpdated[:0]
	s.runAgendaPending = false
	s.runAgendaDirect = false
}

type runAgendaDeltaState struct {
	initial      bool
	present      bool
	updated      bool
	token        reteTerminalTokenDelta
	updateBefore tokenRef
	next         int
}

func (s *Session) coalesceRunAgendaDeltas() (reteAgendaDelta, error) {
	if s == nil || !s.runAgendaPending {
		return reteAgendaDelta{}, nil
	}
	if s.revision == nil {
		return reteAgendaDelta{}, ErrInvalidRuleset
	}

	total := len(s.runAgendaStates)
	added := slices.Grow(s.runAgendaAdded[:0], total)
	removed := slices.Grow(s.runAgendaRemoved[:0], total)
	updated := slices.Grow(s.runAgendaUpdated[:0], total)
	for i := range s.runAgendaStates {
		state := &s.runAgendaStates[i]
		if state.present == state.initial {
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
		removed = append(removed, state.token)
	}
	s.runAgendaAdded = added
	s.runAgendaRemoved = removed
	s.runAgendaUpdated = updated
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
	for index := s.runAgendaBuckets[identity]; index != 0; {
		state := &s.runAgendaStates[index-1]
		if terminalTokenDeltasEqual(s.revision, state.token, token) {
			state.present = present
			state.token = token
			return nil
		}
		index = state.next
	}
	existing, _, ok := s.agenda.activationForTerminalTokenIdentity(rule, token.token, identity)
	state := runAgendaDeltaState{
		initial: ok && existing.status == activationStatusPending,
		present: ok && existing.status == activationStatusPending,
		token:   token,
		next:    s.runAgendaBuckets[identity],
	}
	state.present = present
	s.runAgendaStates = append(s.runAgendaStates, state)
	s.runAgendaBuckets[identity] = len(s.runAgendaStates)
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
		return result.Status == AssertInserted && revision.factMayAffectRuleMatches(result.Fact)
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
	if s == nil || len(s.listeners) == 0 {
		return
	}
	for _, listener := range s.listeners {
		if listener == nil {
			continue
		}
		_ = listener.HandleEvent(ctx, event.clone())
	}
}

func (s *Session) factByID(id FactID) (FactSnapshot, bool) {
	if s == nil {
		return FactSnapshot{}, false
	}
	if id.Generation() != s.generation {
		return FactSnapshot{}, false
	}
	fact, ok := s.workingFactByID(id)
	if !ok {
		return FactSnapshot{}, false
	}
	return fact.snapshotForRevision(s.revision), true
}

func (s *Session) factIDsByName(name string) []FactID {
	s.ensureFactTargetIndexes()
	ids := s.factsByName[name]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDsByTemplate(templateKey TemplateKey) []FactID {
	s.ensureFactTargetIndexes()
	ids := s.factsByTemplate[templateKey]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDForDuplicateKey(key DuplicateKey) (FactID, bool) {
	for i := range s.facts {
		fact := &s.facts[i]
		if fact.publicDuplicateKey(s.revision) == key {
			return fact.id, true
		}
	}
	return FactID{}, false
}

func (s *Session) activeFactWorkspace() factWorkspace {
	return factWorkspace{
		generation:             s.generation,
		sequence:               s.nextFactSequence,
		recency:                s.nextRecency,
		facts:                  s.facts,
		insertionOrder:         s.insertionOrder,
		factsByID:              s.factsByID,
		factsBySequence:        s.factsBySequence,
		factsByDuplicate:       s.factsByDuplicate,
		factsByTemplate:        s.factsByTemplate,
		factsByName:            s.factsByName,
		factTargetIndexesDirty: s.factTargetIndexesDirty,
		slotStorage:            s.slotStorage,
	}
}

func (s *Session) clonedFactWorkspace() factWorkspace {
	state := s.activeFactWorkspace()
	state.facts = cloneWorkingFacts(state.facts)
	state.insertionOrder = cloneFactIDs(state.insertionOrder)
	state.factsByID = cloneFactIDIndex(state.factsByID)
	state.factsBySequence = cloneFactRowSequenceIndex(state.factsBySequence)
	state.factsByDuplicate = cloneDuplicateIndexes(state.factsByDuplicate)
	state.factsByTemplate = cloneFactIDSliceMap(state.factsByTemplate)
	state.factsByName = cloneStringFactIDSliceMap(state.factsByName)
	state.slotStorage = cloneFactSlots(state.slotStorage)
	return state
}

func (s *Session) commitFactWorkspace(state factWorkspace) {
	if s == nil {
		return
	}
	s.nextFactSequence = state.sequence
	s.nextRecency = state.recency
	s.facts = state.facts
	s.factsByID = state.factsByID
	s.factsBySequence = state.factsBySequence
	s.factsByDuplicate = state.factsByDuplicate
	s.factsByTemplate = state.factsByTemplate
	s.factsByName = state.factsByName
	s.factTargetIndexesDirty = state.factTargetIndexesDirty
	s.clearFactFieldEqualIndexes()
	s.insertionOrder = state.insertionOrder
	s.slotStorage = state.slotStorage
}

func (s *Session) swapFactWorkspace(workspace *factWorkspace) {
	if s == nil || workspace == nil {
		return
	}
	s.nextFactSequence, workspace.sequence = workspace.sequence, s.nextFactSequence
	s.nextRecency, workspace.recency = workspace.recency, s.nextRecency
	s.facts, workspace.facts = workspace.facts, s.facts
	s.factsByID, workspace.factsByID = workspace.factsByID, s.factsByID
	s.factsBySequence, workspace.factsBySequence = workspace.factsBySequence, s.factsBySequence
	s.factsByDuplicate, workspace.factsByDuplicate = workspace.factsByDuplicate, s.factsByDuplicate
	s.factsByTemplate, workspace.factsByTemplate = workspace.factsByTemplate, s.factsByTemplate
	s.factsByName, workspace.factsByName = workspace.factsByName, s.factsByName
	s.factTargetIndexesDirty, workspace.factTargetIndexesDirty = workspace.factTargetIndexesDirty, s.factTargetIndexesDirty
	s.clearFactFieldEqualIndexes()
	s.insertionOrder, workspace.insertionOrder = workspace.insertionOrder, s.insertionOrder
	s.slotStorage, workspace.slotStorage = workspace.slotStorage, s.slotStorage
}

func (s *Session) reserveRunGeneratedFactStorage() {
	if s == nil || s.revision == nil || s.agenda == nil {
		return
	}
	stats := s.revision.generatedAssertReserveByRuleRevision()
	if len(stats) == 0 {
		return
	}
	var factCount, slotCount int
	s.agenda.forEachPendingActivation(func(current *activation) bool {
		if current == nil {
			return true
		}
		stat := stats[current.ruleRevisionID]
		if stat.facts == 0 {
			return true
		}
		factCount = saturatingAddInt(factCount, stat.facts)
		slotCount = saturatingAddInt(slotCount, stat.slots)
		maximum := maxIntValue()
		return factCount < maximum && slotCount < maximum
	})
	if factCount == 0 && slotCount == 0 {
		return
	}
	state := s.activeFactWorkspace()
	state.reserveGeneratedFactCapacity(s.revision, factCount, slotCount)
	s.facts = state.facts
	s.insertionOrder = state.insertionOrder
	s.factsBySequence = state.factsBySequence
	s.factsByTemplate = state.factsByTemplate
	s.factsByName = state.factsByName
	s.slotStorage = state.slotStorage
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
	s.generation++
	s.nextFactSequence = 0
	s.nextRecency = 0
	s.facts = nil
	s.factsByID = nil
	s.factsBySequence = nil
	s.factsByDuplicate = duplicateIndexes{}
	s.factsByDuplicate.reset(0)
	s.factsByTemplate = make(map[TemplateKey][]FactID)
	s.factsByName = make(map[string][]FactID)
	s.factTargetIndexesDirty = false
	s.insertionOrder = nil
	s.slotStorage = nil
}

func cloneWorkingFacts(in []workingFact) []workingFact {
	if len(in) == 0 {
		return nil
	}
	out := make([]workingFact, len(in), cap(in))
	copy(out, in)
	for i := range out {
		out[i].fields = cloneFields(out[i].fields)
		out[i].fieldSlots = cloneFactSlots(out[i].fieldSlots)
		out[i].fieldPresence = cloneFieldPresence(out[i].fieldPresence)
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

func resetFactRowSequenceIndex(index []int, capacity int) []int {
	if capacity < 0 {
		capacity = 0
	}
	if cap(index) < capacity {
		return make([]int, 0, capacity)
	}
	return index[:0]
}

func cloneFactRowSequenceIndex(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	out := make([]int, len(in), cap(in))
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
