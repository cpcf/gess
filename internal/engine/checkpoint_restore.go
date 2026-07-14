package engine

import (
	"context"
	"fmt"
	"reflect"
)

// restoreCheckpointWire rebuilds a new session from a validated checkpoint
// document. It stays internal until the public codec and resource bounds land.
func restoreCheckpointWire(ctx context.Context, revision *Ruleset, document checkpointWireDocument, opts ...SessionOption) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, ErrInvalidRuleset
	}
	if err := validateCheckpointWireDocument(document); err != nil {
		return nil, err
	}
	if revision.ID() != document.RulesetID {
		return nil, fmt.Errorf("%w: checkpoint ruleset %s, supplied ruleset %s", ErrIncompatibleRuleset, document.RulesetID, revision.ID())
	}
	local, err := checkpointRestoreLocalConfig(opts)
	if err != nil {
		return nil, err
	}

	initials, err := checkpointInitialFactsFromWire(document.Config.InitialFacts)
	if err != nil {
		return nil, err
	}
	globals, err := checkpointGlobalsFromWire(document.Config.Globals)
	if err != nil {
		return nil, err
	}
	globalValues, err := compileSessionGlobals(revision, globals)
	if err != nil {
		return nil, fmt.Errorf("%w: globals: %v", ErrInvalidCheckpoint, err)
	}
	compiledInitials, err := compileSessionInitialFacts(revision, initials)
	if err != nil {
		return nil, fmt.Errorf("%w: initial facts: %v", ErrInvalidCheckpoint, err)
	}
	state, err := checkpointFactWorkspaceFromWire(revision, document.State)
	if err != nil {
		return nil, err
	}
	state.maxFacts = local.maxFacts
	if err := validateFactLimit(local.maxFacts, state.factCount()); err != nil {
		return nil, err
	}
	rete, err := newReteRuntime(revision, globalValues)
	if err != nil {
		return nil, err
	}
	graphState := checkpointGraphLifecycleWorkspace(state, revision)
	lifecycleDelta, err := rete.resetGraphBetaFromWorkspaceForGenerationWithDelta(ctx, &graphState, state.generation)
	if err != nil {
		return nil, fmt.Errorf("%w: graph lifecycle build: %v", ErrInvalidCheckpoint, err)
	}

	strategy := StrategyDepth
	if document.Config.Strategy == "breadth" {
		strategy = StrategyBreadth
	}
	sessionID := document.SessionID
	if local.id != "" {
		sessionID = local.id
	}
	config := sessionConfig{
		id:                  sessionID,
		initials:            initials,
		globals:             globals,
		strategy:            strategy,
		resetBeforeSnapshot: document.Config.ResetBeforeSnapshot,
		demandLimit:         document.Config.DemandCascadeLimit,
		listeners:           local.listeners,
		eventClock:          local.eventClock,
		output:              local.output,
		explainLog:          local.explainLog,
		maxFacts:            local.maxFacts,
	}
	diagnostics := newSessionDiagnosticsExporter(sessionConfig{eventClock: local.eventClock}, document.State.NextEventSequence)
	session := &Session{
		id:                  sessionID,
		revision:            revision,
		agendaDriver:        newSessionAgendaDriver(strategy),
		propagation:         newSessionPropagationCoordinator(rete),
		factStore:           newSessionFactStore(state),
		initials:            initials,
		globalValues:        globalValues,
		initialCount:        len(initials),
		compiledInitials:    compiledInitials,
		resetBeforeSnapshot: document.Config.ResetBeforeSnapshot,
		diagnostics:         diagnostics,
		output:              local.output,
		backchain:           newSessionBackchainStore(document.Config.DemandCascadeLimit),
		runGuard:            make(chan struct{}, 1),
		mu: struct {
			mutate chan struct{}
			lock   chan struct{}
		}{make(chan struct{}, 1), make(chan struct{}, 1)},
		nextRunSequence: document.State.NextRunSequence,
	}
	session.agendaDriver.initialFocusStack = cloneModuleNames(document.Config.InitialFocusStack)
	if len(session.agendaDriver.initialFocusStack) == 0 {
		session.agendaDriver.initialFocusStack = []ModuleName{MainModule}
	}

	lifecycleDelta, err = session.completeBackchainDemandDeltaImmediate(ctx, lifecycleDelta, mutationOrigin{})
	if err != nil {
		return nil, fmt.Errorf("%w: settle backchain lifecycle: %v", ErrInvalidCheckpoint, err)
	}
	if err := session.restoreCheckpointFactAllocators(document.State); err != nil {
		return nil, err
	}
	session.propagation.setPendingLifecycleDelta(lifecycleDelta)
	if _, err := session.reconcileAgendaInternal(ctx); err != nil {
		return nil, fmt.Errorf("%w: reconcile graph lifecycle agenda: %v", ErrInvalidCheckpoint, err)
	}
	if err := session.restoreCheckpointAgenda(document.State.Agenda); err != nil {
		return nil, err
	}
	if err := session.restoreCheckpointLogicalSupport(document.State.LogicalSupport); err != nil {
		return nil, err
	}
	session.backchain.demandCounters = backchainDemandCascadeCounters{
		Cascades:  document.State.Backchain.Cascades,
		Steps:     document.State.Backchain.Steps,
		LengthMax: document.State.Backchain.LengthMax,
		LimitHits: document.State.Backchain.LimitHits,
	}
	session.diagnostics = newSessionDiagnosticsExporter(config, document.State.NextEventSequence)
	session.nextRunSequence = document.State.NextRunSequence
	session.syncPropagationCounters()
	return session, nil
}

func checkpointGraphLifecycleWorkspace(state *factWorkspace, revision *Ruleset) factWorkspace {
	if state == nil {
		return factWorkspace{}
	}
	graphState := *state
	if revision == nil || len(state.insertionOrder) < 2 {
		return graphState
	}
	ordered := make([]FactID, 0, len(state.insertionOrder))
	appendClass := func(reactive bool) {
		for _, id := range state.insertionOrder {
			fact, ok := state.workingFactByID(id)
			if !ok {
				continue
			}
			template, templated := fact.templateRefForRevision(revision)
			if templated && template.backchainReactive == reactive {
				ordered = append(ordered, id)
			} else if !templated && !reactive {
				ordered = append(ordered, id)
			}
		}
	}
	appendClass(true)
	appendClass(false)
	graphState.insertionOrder = ordered
	return graphState
}

func (s *Session) restoreCheckpointFactAllocators(state checkpointWireSessionState) error {
	if s == nil || len(s.factStore.insertionOrder) != len(state.Facts) {
		return fmt.Errorf("%w: settled graph changed checkpoint fact count", ErrInvalidCheckpoint)
	}
	for i, wire := range state.Facts {
		if s.factStore.insertionOrder[i] != checkpointFactIDFromWire(wire.ID) {
			return fmt.Errorf("%w: settled graph changed checkpoint fact order", ErrInvalidCheckpoint)
		}
	}
	s.factStore.nextFactSequence = state.NextFactSequence
	s.factStore.nextRecency = state.NextRecency
	return nil
}

func checkpointRestoreLocalConfig(opts []SessionOption) (sessionConfig, error) {
	var local sessionConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&local)
		}
	}
	if len(local.initials) != 0 || len(local.globals) != 0 || local.strategy != StrategyDepth || local.resetBeforeSnapshot || local.demandLimit != 0 {
		return sessionConfig{}, fmt.Errorf("%w: restore options cannot override persisted semantic configuration", ErrInvalidCheckpoint)
	}
	return local, nil
}

func checkpointInitialFactsFromWire(wire []checkpointWireInitialFact) ([]SessionInitialFact, error) {
	out := make([]SessionInitialFact, len(wire))
	for i, fact := range wire {
		fields, _, err := checkpointFieldsFromWire(fact.Fields)
		if err != nil {
			return nil, fmt.Errorf("%w: initial fact %d: %v", ErrInvalidCheckpoint, i, err)
		}
		out[i] = SessionInitialFact{name: fact.Name, TemplateKey: fact.TemplateKey, Fields: fields}
	}
	return out, nil
}

func checkpointGlobalsFromWire(wire []checkpointWireNamedValue) (map[string]any, error) {
	out := make(map[string]any, len(wire))
	for _, global := range wire {
		value, err := global.Value.value()
		if err != nil {
			return nil, fmt.Errorf("%w: global %q: %v", ErrInvalidCheckpoint, global.Name, err)
		}
		out[global.Name] = value
	}
	return out, nil
}

func checkpointFactWorkspaceFromWire(revision *Ruleset, state checkpointWireSessionState) (*factWorkspace, error) {
	workspace := newFactWorkspace(state.Generation, len(state.Facts))
	workspace.reserveTemplateIndexes(revision)
	workspace.reserveDuplicateIndexes(revision)
	var previousSequence uint64
	for i, wire := range state.Facts {
		if wire.ID.Sequence <= previousSequence {
			return nil, fmt.Errorf("%w: facts are not in insertion order at row %d", ErrInvalidCheckpoint, i)
		}
		previousSequence = wire.ID.Sequence
		fields, presence, err := checkpointFieldsFromWire(wire.Fields)
		if err != nil {
			return nil, fmt.Errorf("%w: fact %d: %v", ErrInvalidCheckpoint, i, err)
		}
		workspace.sequence = wire.ID.Sequence - 1
		workspace.recency = wire.Recency - 1
		var stored *workingFact
		var inserted bool
		if wire.TemplateKey != "" {
			template, ok := revision.templateByKey(wire.TemplateKey)
			if !ok {
				return nil, fmt.Errorf("%w: fact %d references unknown template %q", ErrInvalidCheckpoint, i, wire.TemplateKey)
			}
			canonical, canonicalPresence, err := checkpointTemplateFields(template, fields, presence)
			if err != nil {
				return nil, fmt.Errorf("%w: fact %d: %v", ErrInvalidCheckpoint, i, err)
			}
			slots := template.buildFieldSlots(canonical, canonicalPresence)
			stored, _, inserted, err = workspace.insertFactSlots(revision, state.Generation, template, slots, true)
			if err != nil {
				return nil, fmt.Errorf("%w: fact %d: %v", ErrInvalidCheckpoint, i, err)
			}
			if !inserted {
				return nil, fmt.Errorf("%w: fact %d violates duplicate policy", ErrInvalidCheckpoint, i)
			}
		} else {
			stored, _, inserted, err = workspace.insertFact(revision, state.Generation, wire.Name, "", fields)
			if err != nil {
				return nil, fmt.Errorf("%w: fact %d: %v", ErrInvalidCheckpoint, i, err)
			}
			if !inserted {
				return nil, fmt.Errorf("%w: fact %d violates duplicate policy", ErrInvalidCheckpoint, i)
			}
		}
		if stored == nil || stored.id != newFactID(state.Generation, wire.ID.Sequence) || stored.recency != wire.Recency {
			return nil, fmt.Errorf("%w: fact %d identity allocation mismatch", ErrInvalidCheckpoint, i)
		}
		stored.version = wire.Version
		stored.setSupportState(wire.Support)
		workspace.replaceWorkingFact(stored)
	}
	workspace.sequence = state.NextFactSequence
	workspace.recency = state.NextRecency
	return workspace, nil
}

func checkpointFieldsFromWire(wire []checkpointWireField) (Fields, map[string]FieldPresence, error) {
	fields := make(Fields, len(wire))
	presence := make(map[string]FieldPresence, len(wire))
	for _, field := range wire {
		if field.Value != nil {
			value, err := field.Value.value()
			if err != nil {
				return nil, nil, err
			}
			fields[field.Name] = value
		}
		presence[field.Name] = field.Presence
	}
	return fields, presence, nil
}

func checkpointTemplateFields(template compiledTemplate, wireFields Fields, wirePresence map[string]FieldPresence) (Fields, map[string]FieldPresence, error) {
	authored := make(Fields)
	for name, value := range wireFields {
		switch wirePresence[name] {
		case FieldPresenceExplicit:
			authored[name] = value
		case FieldPresenceDefault:
		case FieldPresenceOmitted:
			return nil, nil, fmt.Errorf("field %q has a value with omitted presence", name)
		default:
			return nil, nil, fmt.Errorf("field %q has missing presence", name)
		}
	}
	canonical, presence, err := template.applyDefaultsAndValidate(authored)
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(canonical, wireFields) || !reflect.DeepEqual(presence, wirePresence) {
		return nil, nil, fmt.Errorf("template fields or presence do not match declared defaults")
	}
	return canonical, presence, nil
}

func (s *Session) restoreCheckpointAgenda(wire checkpointWireAgendaState) error {
	if s == nil || s.agendaDriver.agenda == nil {
		return fmt.Errorf("%w: missing rebuilt agenda", ErrInvalidCheckpoint)
	}
	agenda := s.agendaDriver.agenda
	byIdentity := make(map[activationLookupKey]checkpointWireActivation, len(wire.Activations))
	for _, activation := range wire.Activations {
		key := activationLookupKey{
			ruleRevisionID: activation.RuleRevisionID,
			identityKey: candidateIdentityKey{
				scopeHash: activation.Identity.ScopeHash,
				hash:      activation.Identity.Hash,
			},
		}
		if _, exists := byIdentity[key]; exists {
			return fmt.Errorf("%w: duplicate serialized activation identity", ErrInvalidCheckpoint)
		}
		byIdentity[key] = activation
	}
	matched := make(map[activationLookupKey]struct{}, len(byIdentity))
	for _, current := range agenda.activations {
		if current == nil || current.status == activationStatusDeactivated {
			continue
		}
		key := activationLookupKey{ruleRevisionID: current.ruleRevisionID, identityKey: current.identityKey}
		persisted, ok := byIdentity[key]
		if !ok {
			return fmt.Errorf("%w: rebuilt graph activation %s/%d/%d for facts %v missing from checkpoint", ErrInvalidCheckpoint, current.ruleRevisionID, current.identityKey.scopeHash, current.identityKey.hash, current.factIDs())
		}
		materialized := agenda.publicActivation(current)
		if err := validateCheckpointActivationCandidate(materialized, persisted); err != nil {
			return err
		}
		matched[key] = struct{}{}
		current.key.ordinal = persisted.Ordinal
		current.birthEpoch = persisted.BirthEpoch
		current.birthRank = persisted.BirthRank
		current.module = persisted.Module
		current.salience = persisted.Salience
		current.declarationOrder = persisted.DeclarationOrder
		current.maxRecency = persisted.MaxRecency
		current.totalRecency = persisted.TotalRecency
		current.supportCount = persisted.SupportCount
		if persisted.Status == "consumed" {
			current.status = activationStatusConsumed
			agenda.compactConsumedTokenActivation(current)
			s.refractions.record(agenda, *current)
		}
	}
	if len(matched) != len(byIdentity) {
		for key, persisted := range byIdentity {
			if _, ok := matched[key]; !ok {
				return fmt.Errorf("%w: checkpoint activation %s/%d/%d for facts %v missing from rebuilt graph: matched %d of %d", ErrInvalidCheckpoint, key.ruleRevisionID, key.identityKey.scopeHash, key.identityKey.hash, persisted.FactIDs, len(matched), len(byIdentity))
			}
		}
	}
	agenda.nextOrdinal = wire.NextOrdinal
	agenda.nextBirthEpoch = wire.NextBirthEpoch
	agenda.initialBirthEpoch = wire.InitialBirthEpoch
	agenda.handleGeneration = wire.HandleGeneration
	agenda.resetModuleQueues()
	for _, current := range agenda.activations {
		if current != nil && current.status == activationStatusPending {
			agenda.enqueueActivation(current)
		}
	}
	s.agendaDriver.ready = wire.Ready
	s.agendaDriver.dirty = wire.Dirty
	s.agendaDriver.focusStack = cloneModuleNames(wire.FocusStack)
	return nil
}

func validateCheckpointActivationCandidate(rebuilt activation, persisted checkpointWireActivation) error {
	rebuiltIDs := rebuilt.factIDs()
	if len(rebuiltIDs) != len(persisted.FactIDs) || len(rebuilt.factVersions()) != len(persisted.FactVersions) {
		return fmt.Errorf("%w: activation fact tuple length mismatch", ErrInvalidCheckpoint)
	}
	for i, id := range rebuiltIDs {
		if checkpointWireFactIDFromFactID(id) != persisted.FactIDs[i] || rebuilt.factVersions()[i] != persisted.FactVersions[i] {
			return fmt.Errorf("%w: activation fact tuple mismatch", ErrInvalidCheckpoint)
		}
	}
	return nil
}

func (s *Session) restoreCheckpointLogicalSupport(wire checkpointWireLogicalSupportState) error {
	state := logicalSupportState{
		edges:    make(map[SupportID]logicalSupportEdgeRecord, len(wire.Edges)),
		bySource: make(map[logicalSupportSourceKey]map[SupportID]struct{}),
		byFact:   make(map[FactID]map[SupportID]struct{}),
		counters: LogicalSupportCounters{
			LogicalFactsAsserted:    wire.Counters.LogicalFactsAsserted,
			LogicalFactsRetracted:   wire.Counters.LogicalFactsRetracted,
			SupportEdgesAdded:       wire.Counters.SupportEdgesAdded,
			SupportEdgesRemoved:     wire.Counters.SupportEdgesRemoved,
			MetadataOnlyTransitions: wire.Counters.MetadataOnlyTransitions,
			CascadeRetractions:      wire.Counters.CascadeRetractions,
			CascadeBreadthMax:       wire.Counters.CascadeBreadthMax,
			CascadeDepthMax:         wire.Counters.CascadeDepthMax,
		},
	}
	for i, persisted := range wire.Edges {
		source := logicalSupportSourceKey{
			generation:     persisted.Generation,
			ruleRevisionID: persisted.RuleRevisionID,
			identityKey: candidateIdentityKey{
				scopeHash: persisted.Source.ScopeHash,
				hash:      persisted.Source.Hash,
			},
		}
		factID := checkpointFactIDFromWire(persisted.FactID)
		supporting := make([]FactID, len(persisted.SupportingFacts))
		for j, id := range persisted.SupportingFacts {
			supporting[j] = checkpointFactIDFromWire(id)
		}
		edge := LogicalSupportEdge{
			SupportID:       persisted.SupportID,
			FactID:          factID,
			RuleID:          persisted.RuleID,
			RuleRevisionID:  persisted.RuleRevisionID,
			ActivationID:    persisted.ActivationID,
			Generation:      persisted.Generation,
			SupportingFacts: supporting,
		}
		if logicalSupportID(source, factID) != edge.SupportID {
			return fmt.Errorf("%w: logical support edge %d has inconsistent support ID", ErrInvalidCheckpoint, i)
		}
		if _, exists := state.edges[edge.SupportID]; exists {
			return fmt.Errorf("%w: duplicate logical support ID", ErrInvalidCheckpoint)
		}
		state.edges[edge.SupportID] = logicalSupportEdgeRecord{edge: edge, source: source}
		if state.bySource[source] == nil {
			state.bySource[source] = make(map[SupportID]struct{})
		}
		state.bySource[source][edge.SupportID] = struct{}{}
		if state.byFact[factID] == nil {
			state.byFact[factID] = make(map[SupportID]struct{})
		}
		state.byFact[factID][edge.SupportID] = struct{}{}
	}
	s.restoreLogicalSupportState(state)
	actual := s.currentSupportGraph().Counters
	if actual.CurrentLogicalFacts != wire.Counters.CurrentLogicalFacts ||
		actual.CurrentStatedAndLogicalFacts != wire.Counters.CurrentStatedAndLogicalFacts ||
		actual.CurrentSupportEdges != wire.Counters.CurrentSupportEdges {
		return fmt.Errorf("%w: logical support current counters disagree with restored facts and edges", ErrInvalidCheckpoint)
	}
	return nil
}

func checkpointFactIDFromWire(id checkpointWireFactID) FactID {
	return newFactID(id.Generation, id.Sequence)
}
