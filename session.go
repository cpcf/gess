package gess

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type SessionOption func(*sessionConfig)

type sessionConfig struct {
	id         SessionID
	listeners  []EventListener
	initials   []SessionInitialFact
	eventClock func() time.Time
}

type SessionInitialFact struct {
	Name        string
	TemplateKey TemplateKey
	Fields      Fields
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

type Session struct {
	id                   SessionID
	revision             *Ruleset
	agenda               *agenda
	propagationCounters  *propagationCounterLedger
	rete                 *reteRuntime
	generation           Generation
	initials             []SessionInitialFact
	initialCount         int
	compiledInitials     []compiledSessionInitialFact
	listeners            []EventListener
	eventClock           func() time.Time
	closed               bool
	runGuard             chan struct{}
	runActive            atomic.Bool
	runActivation        atomic.Pointer[activation]
	runAgendaDelta       reteAgendaDelta
	runAgendaDeltas      []reteAgendaDelta
	runAgendaStates      []runAgendaDeltaState
	runAgendaBuckets     map[candidateIdentity]int
	runAgendaAdded       []reteTerminalTokenDelta
	runAgendaRemoved     []reteTerminalTokenDelta
	runAgendaPending     bool
	agendaReady          bool
	agendaDirty          bool
	actionBindingScratch actionContextBindingState
	mutationQueueMu      sync.Mutex
	mutationQueue        []queuedMutation
	mu                   struct {
		mutate chan struct{}
		lock   chan struct{}
	}

	nextFactSequence  uint64
	nextRecency       Recency
	nextRunSequence   uint64
	facts             []workingFact
	factsByID         map[FactID]int
	factsByDuplicate  duplicateIndexes
	factsByTemplate   map[TemplateKey][]FactID
	factsByName       map[string][]FactID
	insertionOrder    []FactID
	slotStorage       []factSlot
	resetWorkspace    factWorkspace
	resetFactsScratch []FactSnapshot
	nextEventSequence uint64
}

type queuedMutation struct {
	ctx    context.Context
	apply  func(context.Context) (any, error)
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

	cfg := sessionConfig{}
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
	state.reserveSlotStorage(revision.estimatedRunSlotCapacity(cap(state.facts)))
	if len(compiledInitials) > 0 {
		state.applyCompiledInitialFacts(compiledInitials)
	}
	rete, err := newReteRuntime(revision)
	if err != nil {
		return nil, err
	}
	rete.resetAlpha(state.detachedFactsByInsertionOrder(revision))

	session := &Session{
		id:               cfg.id,
		revision:         revision,
		agenda:           newAgenda(),
		rete:             rete,
		generation:       1,
		initials:         initials,
		initialCount:     len(initials),
		compiledInitials: compiledInitials,
		listeners:        listeners,
		eventClock:       cfg.eventClock,
		runGuard:         make(chan struct{}, 1),
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		factsByID:        state.factsByID,
		factsByDuplicate: state.factsByDuplicate,
		factsByTemplate:  state.factsByTemplate,
		factsByName:      state.factsByName,
		nextFactSequence: state.nextFactSequence(),
		nextRecency:      state.nextRecency(),
		nextRunSequence:  0,
		facts:            state.facts,
		insertionOrder:   state.factsByInsertionOrder(),
		slotStorage:      state.slotStorage,
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
	} else {
		s.propagationCounters.setTerminalRowsRetained(0)
	}
	path, unsupportedReasons := propagationRuntimeUnknown, map[string]int(nil)
	if s.rete != nil {
		path, unsupportedReasons = s.rete.propagationDiagnostics()
	}
	s.propagationCounters.setRuntimeDiagnostics(path, unsupportedReasons)
}

func (s *Session) removeStoredFact(id FactID) {
	if s == nil || len(s.facts) == 0 {
		return
	}
	for i := range s.facts {
		if s.facts[i].id != id {
			continue
		}
		copy(s.facts[i:], s.facts[i+1:])
		last := len(s.facts) - 1
		s.facts[last] = workingFact{}
		s.facts = s.facts[:last]
		s.reindexStoredFactRowsFrom(i)
		return
	}
}

func (s *Session) reindexStoredFactRowsFrom(start int) {
	if s == nil || s.factsByID == nil || start < 0 {
		return
	}
	for i := start; i < len(s.facts); i++ {
		id := s.facts[i].id
		if _, ok := s.factsByID[id]; ok {
			s.factsByID[id] = i
		}
	}
}

func (s *Session) workingFactByID(id FactID) (*workingFact, bool) {
	if s == nil || s.factsByID == nil {
		return nil, false
	}
	index, ok := s.factsByID[id]
	if !ok || index < 0 || index >= len(s.facts) {
		return nil, false
	}
	fact := &s.facts[index]
	if fact.id != id {
		return nil, false
	}
	return fact, true
}

// factsForTarget is an internal matcher view. Callers must hold session
// ownership; returned detached snapshots may share session-owned backing.
func (s *Session) factsForTarget(target conditionTarget) ([]FactSnapshot, bool) {
	if s == nil {
		return nil, false
	}
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
			apply: func(mutationCtx context.Context) (any, error) {
				result, _, err := s.insertFactImmediate(mutationCtx, name, templateKey, fields, origin)
				return result, err
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
			s.recordRunAgendaDelta(agendaDelta)
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

	_, template, inserted, agendaDelta, err := s.insertTemplateValuesImmediate(ctx, templateKey, values, origin)
	if err != nil {
		return err
	}
	if inserted && s.revision.factMayAffectRuleMatchesByTarget(template.Name(), template.Key()) {
		if origin.isZero() || !s.runGuardHeld() {
			_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
			return err
		}
		s.recordRunAgendaDelta(agendaDelta)
	}
	return nil
}

func (s *Session) insertFactImmediate(ctx context.Context, name string, templateKey TemplateKey, fields Fields, origin mutationOrigin) (AssertResult, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return AssertResult{Status: AssertClosed}, reteAgendaDelta{}, ErrClosedSession
	}

	state := s.activeFactWorkspace()
	fact, duplicateKey, inserted, err := state.insertFact(s.revision, s.generation, name, templateKey, fields)
	if err != nil {
		return AssertResult{Status: AssertValidationFailure}, reteAgendaDelta{}, err
	}
	s.commitFactWorkspace(state)
	if !inserted {
		return AssertResult{
			Status:       AssertExisting,
			Fact:         fact.snapshotForRevision(s.revision),
			DuplicateKey: duplicateKey,
		}, reteAgendaDelta{}, nil
	}

	snapshot := fact.snapshotForRevision(s.revision)
	var span *propagationCounterSpan
	if s.propagationCounters != nil {
		counterSpan := s.propagationCounters.beginAssert(snapshot.TemplateKey(), origin)
		span = &counterSpan
	}
	agendaDelta := s.updateReteAlphaAfterAssert(snapshot, origin, span)
	if span != nil {
		span.finish()
	}
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

func (s *Session) insertTemplateValuesImmediate(ctx context.Context, templateKey TemplateKey, values []Value, origin mutationOrigin) (*workingFact, Template, bool, reteAgendaDelta, error) {
	if s == nil || s.closed {
		return nil, Template{}, false, reteAgendaDelta{}, ErrClosedSession
	}
	template, ok := s.revision.templateByKey(templateKey)
	if !ok {
		return nil, Template{}, false, reteAgendaDelta{}, &ValidationError{
			TemplateName: string(templateKey),
			Reason:       "unknown template key",
		}
	}
	state := s.activeFactWorkspace()
	fieldSlots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(template.fields))
	fieldSlots, err := template.buildValidatedFieldSlotsFromValuesInto(fieldSlots, values)
	if err != nil {
		state.rollbackGeneratedFactSlots(slotMark)
		return nil, Template{}, false, reteAgendaDelta{}, err
	}

	fact, _, inserted, err := state.insertPreparedGeneratedFactSlots(s.revision, s.generation, template, fieldSlots, slotMark)
	if err != nil {
		state.rollbackGeneratedFactSlots(slotMark)
		return nil, Template{}, false, reteAgendaDelta{}, err
	}
	s.commitFactWorkspace(state)
	if !inserted {
		return fact, template, false, reteAgendaDelta{}, nil
	}

	var span *propagationCounterSpan
	if s.propagationCounters != nil {
		counterSpan := s.propagationCounters.beginAssert(template.Key(), origin)
		span = &counterSpan
	}
	agendaDelta := s.updateReteAlphaAfterAssertGenerated(fact, origin, span)
	if span != nil {
		span.finish()
	}

	if len(s.listeners) > 0 {
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

	return fact, template, true, agendaDelta, nil
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
			apply: func(mutationCtx context.Context) (any, error) {
				result, _, err := s.retractImmediate(mutationCtx, id, origin)
				return result, err
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
			s.recordRunAgendaDelta(agendaDelta)
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
	oldVersion := fact.version
	oldDuplicate := fact.publicDuplicateKey(s.revision)
	factID := fact.id
	factRecency := fact.recency
	factTemplateKey := fact.templateKey
	factName := fact.name

	delete(s.factsByID, id)
	if !fact.dupIndex.isZero() {
		if existingID, ok := s.factsByDuplicate.get(fact.dupIndex); ok && existingID == id {
			s.factsByDuplicate.delete(fact.dupIndex)
		}
	}
	s.factsByTemplate[factTemplateKey] = removeFactIDFromSlice(s.factsByTemplate[factTemplateKey], id)
	if len(s.factsByTemplate[factTemplateKey]) == 0 {
		delete(s.factsByTemplate, factTemplateKey)
	}
	s.factsByName[factName] = removeFactIDFromSlice(s.factsByName[factName], id)
	if len(s.factsByName[factName]) == 0 {
		delete(s.factsByName, factName)
	}
	s.insertionOrder = removeFactIDFromSlice(s.insertionOrder, id)
	s.removeStoredFact(id)
	agendaDelta := s.updateReteAlphaAfterRetract(before)

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
			apply: func(mutationCtx context.Context) (any, error) {
				return s.applyRulesetImmediate(mutationCtx, next)
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

	before := s.detachedSnapshotLocked()
	next := &s.resetWorkspace
	next.reset(s.generation+1, s.revision.estimatedRunFactCapacity(len(compiledInitials)))
	next.reserveTemplateIndexes(s.revision)
	next.reserveSlotStorage(s.revision.estimatedRunSlotCapacity(cap(next.facts)))
	facts := next.applyCompiledInitialFactsInto(compiledInitials, s.resetFactsScratch[:0], s.revision)
	s.resetFactsScratch = facts

	oldGeneration := s.generation
	s.agendaReady = false
	s.agendaDirty = false
	s.swapFactWorkspace(next)
	s.generation = next.generation
	if s.rete == nil {
		s.rebuildReteRuntime(s.revision, facts)
	} else {
		s.rete.resetAlpha(facts)
		s.syncPropagationCounters()
	}
	s.emitAgendaEvents(ctx, s.agenda.clear())

	delta := MutationDelta{
		Kind:          MutationReset,
		Generation:    s.generation,
		OldGeneration: oldGeneration,
	}
	result := ResetResult{
		Status:     ResetApplied,
		Generation: s.generation,
		Before:     before,
		Delta:      delta,
	}
	if len(s.listeners) > 0 {
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

	s.rebuildFieldSlots(s.revision, next)
	snapshot = s.indexedSnapshotLocked()
	rete, err := newReteRuntime(next)
	if err != nil {
		return ApplyRulesetResult{}, err
	}
	rete.resetAlpha(snapshot.facts)

	tokens, ok, err := rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		return ApplyRulesetResult{}, err
	}
	var results []ruleMatchResult
	if !ok {
		results, err = rete.match(ctx, snapshot)
		if err != nil {
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
	s.agendaReady = true
	s.agendaDirty = false
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
	results, err := s.rete.match(ctx, source)
	if err != nil {
		return nil, err
	}
	changes, err := s.agenda.reconcile(ctx, s.revision, results)
	if err != nil {
		return nil, err
	}
	s.agendaReady = true
	s.agendaDirty = false
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

	tokens, ok, err := s.rete.currentTerminalTokenDeltas(ctx)
	if err != nil {
		return nil, true, err
	}
	if ok {
		changes, err := s.agenda.reconcileTerminalTokens(ctx, s.revision, tokens)
		if err != nil {
			return nil, true, err
		}
		s.agendaReady = true
		s.agendaDirty = false
		s.emitAgendaEvents(ctx, changes)
		return changes, true, nil
	}

	results, ok, err := s.rete.matchWithoutSnapshot(ctx, s.generation)
	if err != nil || !ok {
		return nil, ok, err
	}
	changes, err := s.agenda.reconcile(ctx, s.revision, results)
	if err != nil {
		return nil, true, err
	}
	s.agendaReady = true
	s.agendaDirty = false
	s.emitAgendaEvents(ctx, changes)
	return changes, true, nil
}

func (s *Session) reconcileAgendaAfterMutation(ctx context.Context, delta reteAgendaDelta) ([]agendaChange, error) {
	if changes, ok, err := s.applyReteAgendaDelta(ctx, delta); ok || err != nil {
		return changes, err
	}
	return s.reconcileAgendaInternal(ctx)
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
		return nil, false, nil
	}
	var changes []agendaChange
	if collectChanges {
		var err error
		changes, err = s.agenda.applyTerminalTokenDeltas(ctx, s.revision, delta.removed, delta.added)
		if err != nil {
			return nil, true, err
		}
	} else if err := s.agenda.applyTerminalTokenDeltasWithoutChanges(ctx, s.revision, delta.removed, delta.added); err != nil {
		return nil, true, err
	}
	if s.propagationCounters != nil {
		s.propagationCounters.recordAgendaDeltaApplication()
	}
	s.agendaReady = true
	s.agendaDirty = false
	if collectChanges {
		s.emitAgendaEvents(ctx, changes)
	}
	return changes, true, nil
}

func (s *Session) rebuildReteRuntime(revision *Ruleset, facts []FactSnapshot) {
	if s == nil || revision == nil {
		return
	}
	rete, err := newReteRuntime(revision)
	if err != nil {
		s.rete = nil
		return
	}
	rete.resetAlpha(facts)
	s.rete = rete
	s.syncPropagationCounters()
}

func (s *Session) updateReteAlphaAfterAssert(fact FactSnapshot, origin mutationOrigin, span *propagationCounterSpan) reteAgendaDelta {
	if s == nil {
		return reteAgendaDelta{}
	}
	if s.rete == nil {
		s.rebuildReteRuntime(s.revision, s.detachedFactsByInsertionOrder())
		return reteAgendaDelta{}
	}
	if s.rete.alpha == nil {
		s.rete.resetAlpha(s.detachedFactsByInsertionOrder())
		return reteAgendaDelta{}
	}
	if delta, ok := s.rete.insertGraphAlphaFact(fact, span); ok {
		return delta
	}
	s.rete.insertAlphaFact(fact, span)
	return s.rete.insertBetaFactWithOrigin(fact, origin, span)
}

func (s *Session) updateReteAlphaAfterAssertGenerated(fact *workingFact, origin mutationOrigin, span *propagationCounterSpan) reteAgendaDelta {
	if s == nil || fact == nil {
		return reteAgendaDelta{}
	}
	if s.rete == nil {
		s.rebuildReteRuntime(s.revision, s.detachedFactsByInsertionOrder())
		return reteAgendaDelta{}
	}
	if s.rete.alpha == nil {
		s.rete.resetAlpha(s.detachedFactsByInsertionOrder())
		return reteAgendaDelta{}
	}
	if delta, ok := s.rete.insertGraphAlphaFactGenerated(fact, span); ok {
		return delta
	}
	snapshot := fact.detachedSnapshotForRevision(s.revision)
	s.rete.insertAlphaFactGenerated(fact, snapshot, span)
	return s.rete.insertBetaFactGenerated(fact, origin, span)
}

func (s *Session) updateReteAlphaAfterRetract(fact FactSnapshot) reteAgendaDelta {
	if s == nil || s.rete == nil {
		return reteAgendaDelta{}
	}
	s.rete.removeAlphaFact(fact)
	return s.rete.removeBetaFact(fact, s.propagationCounters)
}

func (s *Session) updateReteAlphaAfterModify(before, after FactSnapshot) reteAgendaDelta {
	if s == nil || s.rete == nil {
		return reteAgendaDelta{}
	}
	delta := s.rete.updateBetaFact(before, after, s.propagationCounters)
	s.rete.updateAlphaFact(before, after)
	return delta
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
			apply: func(mutationCtx context.Context) (any, error) {
				result, _, err := s.modifyImmediate(mutationCtx, id, patch, origin)
				return result, err
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
			s.recordRunAgendaDelta(agendaDelta)
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

	fact, ok := s.workingFactByID(id)
	if !ok {
		return ModifyResult{Status: ModifyMissing}, reteAgendaDelta{}, ErrFactNotFound
	}

	before := fact.snapshotForRevision(s.revision)
	template, templateExists := s.revision.templateByKey(fact.templateKey)

	beforeFields := before.Fields()
	beforePresence := before.FieldPresenceMap()
	proposedFields := cloneFields(beforeFields)
	proposedPresence := cloneFieldPresence(beforePresence)
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

	var err error
	if templateExists {
		proposedFields, proposedPresence, err = template.applyDefaultsAndValidate(proposedFields)
		if err != nil {
			return ModifyResult{Status: ModifyValidationFailure, Fact: before}, reteAgendaDelta{}, err
		}
	}

	duplicatePolicy := template.duplicatePolicy
	proposedFieldSlots := s.revision.buildFieldSlots(template, proposedFields, proposedPresence)
	newDupIndex := makeDuplicateIndexForValidatedFact(fact.name, template, proposedFields, proposedFieldSlots)
	oldDuplicate := fact.publicDuplicateKey(s.revision)

	if duplicatePolicy != DuplicateAllow {
		if existingID, ok := s.factsByDuplicate.get(newDupIndex); ok && existingID != fact.id {
			return ModifyResult{Status: ModifyDuplicate, Fact: before}, reteAgendaDelta{}, ErrDuplicateFact
		}
	}

	if fieldsAndPresenceEqual(beforeFields, beforePresence, proposedFields, proposedPresence) {
		return ModifyResult{Status: ModifyNoOp, Fact: before}, reteAgendaDelta{}, nil
	}

	s.nextRecency++

	if duplicatePolicy != DuplicateAllow && fact.dupIndex != newDupIndex {
		if !fact.dupIndex.isZero() {
			s.factsByDuplicate.delete(fact.dupIndex)
		}
		if !newDupIndex.isZero() {
			s.factsByDuplicate.set(newDupIndex, fact.id)
		}
	}

	oldVersion := fact.version
	newDuplicate := newDupIndex.publicKeyForTemplate(fact.name, template)
	fact.version++
	fact.recency = s.nextRecency
	if len(proposedFieldSlots) > 0 {
		fact.fields = nil
		fact.fieldSlots = cloneFactSlots(proposedFieldSlots)
		fact.fieldPresence = nil
	} else {
		fact.fields = proposedFields
		fact.fieldSlots = nil
		fact.fieldPresence = proposedPresence
	}
	fact.dupIndex = newDupIndex

	after := fact.snapshotForRevision(s.revision)
	agendaDelta := s.updateReteAlphaAfterModify(before, after)
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
		ChangedFields:  changedFields(beforeFields, beforePresence, proposedFields, proposedPresence),
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

func changedFields(beforeFields Fields, beforePresence map[string]FieldPresence, afterFields Fields, afterPresence map[string]FieldPresence) []FieldChange {
	keys := make(map[string]struct{}, len(beforeFields)+len(afterFields)+len(beforePresence)+len(afterPresence))
	for key := range beforeFields {
		keys[key] = struct{}{}
	}
	for key := range afterFields {
		keys[key] = struct{}{}
	}
	for key := range beforePresence {
		keys[key] = struct{}{}
	}
	for key := range afterPresence {
		keys[key] = struct{}{}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)

	changes := make([]FieldChange, 0, len(orderedKeys))
	for _, key := range orderedKeys {
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
		generation: s.generation,
		facts:      facts,
	}
	if includeTargetIndexes {
		snapshot.byID, snapshot.byName, snapshot.byTemplate = snapshotIndexes(facts)
	} else {
		snapshot.byID = snapshotIDIndex(facts)
	}
	return snapshot
}

type factWorkspace struct {
	generation       Generation
	sequence         uint64
	recency          Recency
	facts            []workingFact
	insertionOrder   []FactID
	factsByID        map[FactID]int
	factsByDuplicate duplicateIndexes
	factsByTemplate  map[TemplateKey][]FactID
	factsByName      map[string][]FactID
	slotStorage      []factSlot
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

type duplicateIndexes struct {
	strings   map[DuplicateKey]FactID
	singleInt map[duplicateSingleIntIndexKey]FactID
	doubleInt map[duplicateDoubleIntIndexKey]FactID
	scalars   map[duplicateIndexKey]FactID
}

func (i *duplicateIndexes) reset(initialCapacity int) {
	if initialCapacity < 0 {
		initialCapacity = 0
	}
	if i.strings == nil {
		i.strings = make(map[DuplicateKey]FactID, initialCapacity)
	} else {
		clear(i.strings)
	}
	if i.singleInt == nil {
		i.singleInt = make(map[duplicateSingleIntIndexKey]FactID, initialCapacity)
	} else {
		clear(i.singleInt)
	}
	if i.doubleInt == nil {
		i.doubleInt = make(map[duplicateDoubleIntIndexKey]FactID, initialCapacity)
	} else {
		clear(i.doubleInt)
	}
	if i.scalars == nil {
		i.scalars = make(map[duplicateIndexKey]FactID, initialCapacity)
	} else {
		clear(i.scalars)
	}
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
	default:
		delete(i.scalars, key)
	}
}

func (i duplicateIndexes) len() int {
	return len(i.strings) + len(i.singleInt) + len(i.doubleInt) + len(i.scalars)
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
	if w.factsByID == nil {
		w.factsByID = make(map[FactID]int, initialCapacity)
	} else {
		clear(w.factsByID)
	}
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

func (w *factWorkspace) reserveTemplateIndexes(revision *Ruleset) {
	if w == nil || revision == nil {
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

func (w *factWorkspace) reserveSlotStorage(capacity int) {
	if w == nil || capacity <= cap(w.slotStorage) {
		return
	}
	next := make([]factSlot, len(w.slotStorage), capacity)
	copy(next, w.slotStorage)
	w.slotStorage = next
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
	if slotCount > 0 && cap(w.slotStorage)-len(w.slotStorage) < slotCount {
		nextCapacity := nextGeneratedSlotCapacity(len(w.slotStorage), cap(w.slotStorage), slotCount, revision)
		w.reserveSlotStorage(nextCapacity)
	}
}

func (w *factWorkspace) storeFact(fact workingFact) *workingFact {
	if w == nil {
		return nil
	}

	w.facts = append(w.facts, fact)
	stored := &w.facts[len(w.facts)-1]
	if w.factsByID != nil {
		w.factsByID[stored.id] = len(w.facts) - 1
	}
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

func (w *factWorkspace) workingFactByID(id FactID) (*workingFact, bool) {
	if w == nil || w.factsByID == nil {
		return nil, false
	}
	index, ok := w.factsByID[id]
	if !ok || index < 0 || index >= len(w.facts) {
		return nil, false
	}
	fact := &w.facts[index]
	if fact.id != id {
		return nil, false
	}
	return fact, true
}

func (w *factWorkspace) reindexFactRowsFrom(start int) {
	if w == nil || w.factsByID == nil || start < 0 {
		return
	}
	for i := start; i < len(w.facts); i++ {
		id := w.facts[i].id
		if _, ok := w.factsByID[id]; ok {
			w.factsByID[id] = i
		}
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
		existingID, ok := w.factsByDuplicate.get(duplicateIndex)
		if ok {
			existing, ok := w.workingFactByID(existingID)
			if ok {
				return existing, existing.publicDuplicateKey(revision), false, nil
			}
			w.factsByDuplicate.delete(duplicateIndex)
		}
	}

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	duplicateKey := duplicateIndex.publicKeyForTemplate(name, template)

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
	w.factsByTemplate[templateKey] = append(w.factsByTemplate[templateKey], id)
	w.factsByName[name] = append(w.factsByName[name], id)
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, duplicateKey, true, nil
}

func (w *factWorkspace) insertFactSlots(revision *Ruleset, generation Generation, template Template, fieldSlots []factSlot, materializeDuplicateKey bool) (*workingFact, DuplicateKey, bool, error) {
	name := template.Name()
	templateKey := template.Key()
	duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
	if template.duplicatePolicy != DuplicateAllow {
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

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	var duplicateKey DuplicateKey
	if materializeDuplicateKey {
		duplicateKey = duplicateIndex.publicKeyForTemplate(name, template)
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
	w.factsByTemplate[templateKey] = append(w.factsByTemplate[templateKey], id)
	w.factsByName[name] = append(w.factsByName[name], id)
	w.insertionOrder = append(w.insertionOrder, id)

	return stored, duplicateKey, true, nil
}

func (w *factWorkspace) insertPreparedGeneratedFactSlots(revision *Ruleset, generation Generation, template Template, fieldSlots []factSlot, slotMark int) (*workingFact, DuplicateKey, bool, error) {
	name := template.Name()
	templateKey := template.Key()
	duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
	if template.duplicatePolicy != DuplicateAllow {
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

	w.sequence++
	w.recency++
	id := newFactID(generation, w.sequence)
	fact := workingFact{
		id:          id,
		name:        name,
		templateKey: templateKey,
		version:     1,
		recency:     w.recency,
		fieldSlots:  fieldSlots,
		dupIndex:    duplicateIndex,
	}

	stored := w.storeFact(fact)
	if template.duplicatePolicy != DuplicateAllow {
		w.factsByDuplicate.set(duplicateIndex, id)
	}
	w.factsByTemplate[templateKey] = append(w.factsByTemplate[templateKey], id)
	w.factsByName[name] = append(w.factsByName[name], id)
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
		name = template.Name()
	}

	if templateExists && revision.usesFieldSlots(template) {
		fieldSlots, err := template.buildValidatedFieldSlots(initial.Fields)
		if err != nil {
			return compiledSessionInitialFact{}, err
		}

		duplicateIndex := makeDuplicateIndexForValidatedFact(name, template, nil, fieldSlots)
		return compiledSessionInitialFact{
			name:            name,
			templateKey:     templateKey,
			fieldSlots:      fieldSlots,
			fieldSpecs:      template.fields,
			duplicatePolicy: template.duplicatePolicy,
			duplicateIndex:  duplicateIndex,
			duplicateKey:    duplicateIndex.publicKeyForTemplate(name, template),
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
		duplicateKey:    duplicateIndex.publicKeyForTemplate(name, template),
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
	w.factsByTemplate[initial.templateKey] = append(w.factsByTemplate[initial.templateKey], id)
	w.factsByName[initial.name] = append(w.factsByName[initial.name], id)
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

func (s *Session) consumeAgendaDirty() bool {
	if s == nil || !s.agendaDirty {
		return false
	}
	s.agendaDirty = false
	return true
}

func (s *Session) recordRunAgendaDelta(delta reteAgendaDelta) {
	if s == nil {
		return
	}
	if !delta.supported || s.agendaDirty {
		s.markAgendaDirty()
		return
	}
	total := len(delta.added) + len(delta.removed)
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
	}
}

func (s *Session) recordRunAgendaDeltaTokens(delta reteAgendaDelta) error {
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

func (s *Session) reconcileRunAgendaDelta(ctx context.Context) error {
	if s == nil || !s.runAgendaPending {
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
		s.markAgendaDirty()
		return nil
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
	s.runAgendaDelta.added = s.runAgendaDelta.added[:0]
	s.runAgendaDelta.removed = s.runAgendaDelta.removed[:0]
	s.runAgendaDelta.supported = false
	for i := range s.runAgendaDeltas {
		clear(s.runAgendaDeltas[i].added)
		clear(s.runAgendaDeltas[i].removed)
		s.runAgendaDeltas[i].added = s.runAgendaDeltas[i].added[:0]
		s.runAgendaDeltas[i].removed = s.runAgendaDeltas[i].removed[:0]
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
	s.runAgendaAdded = s.runAgendaAdded[:0]
	s.runAgendaRemoved = s.runAgendaRemoved[:0]
	s.runAgendaPending = false
}

type runAgendaDeltaState struct {
	initial bool
	present bool
	token   reteTerminalTokenDelta
	next    int
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
	for i := range s.runAgendaStates {
		state := &s.runAgendaStates[i]
		if state.present == state.initial {
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
	return reteAgendaDelta{
		supported: true,
		added:     added,
		removed:   removed,
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
			value, err := req.apply(mutationCtx)
			s.endMutation()
			if err == nil && mutationResultNeedsReconcile(value, s.revision) {
				if _, reconcileErr := s.reconcileAgendaInternal(ctx); reconcileErr != nil {
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
	default:
		return true
	}
}

func (s *Session) beginMutationForOrigin(origin mutationOrigin) (bool, bool) {
	if s == nil {
		return false, false
	}
	if s.runGuardHeld() {
		if !s.canMutateDuringRun(origin) {
			return false, false
		}
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
	ids := s.factsByName[name]
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func (s *Session) factIDsByTemplate(templateKey TemplateKey) []FactID {
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
		generation:       s.generation,
		sequence:         s.nextFactSequence,
		recency:          s.nextRecency,
		facts:            s.facts,
		insertionOrder:   s.insertionOrder,
		factsByID:        s.factsByID,
		factsByDuplicate: s.factsByDuplicate,
		factsByTemplate:  s.factsByTemplate,
		factsByName:      s.factsByName,
		slotStorage:      s.slotStorage,
	}
}

func (s *Session) commitFactWorkspace(state factWorkspace) {
	if s == nil {
		return
	}
	s.nextFactSequence = state.sequence
	s.nextRecency = state.recency
	s.facts = state.facts
	s.factsByID = state.factsByID
	s.factsByDuplicate = state.factsByDuplicate
	s.factsByTemplate = state.factsByTemplate
	s.factsByName = state.factsByName
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
	s.factsByDuplicate, workspace.factsByDuplicate = workspace.factsByDuplicate, s.factsByDuplicate
	s.factsByTemplate, workspace.factsByTemplate = workspace.factsByTemplate, s.factsByTemplate
	s.factsByName, workspace.factsByName = workspace.factsByName, s.factsByName
	s.insertionOrder, workspace.insertionOrder = workspace.insertionOrder, s.insertionOrder
	s.slotStorage, workspace.slotStorage = workspace.slotStorage, s.slotStorage
}

func (s *Session) resetWorkingMemory() {
	s.generation++
	s.nextFactSequence = 0
	s.nextRecency = 0
	s.facts = nil
	s.factsByID = make(map[FactID]int)
	s.factsByDuplicate = duplicateIndexes{}
	s.factsByDuplicate.reset(0)
	s.factsByTemplate = make(map[TemplateKey][]FactID)
	s.factsByName = make(map[string][]FactID)
	s.insertionOrder = nil
	s.slotStorage = nil
}
