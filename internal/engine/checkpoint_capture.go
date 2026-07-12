package engine

import (
	"context"
	"fmt"
	"sort"
)

// checkpointWire captures the complete durable state of an idle session into
// the versioned wire model. It remains internal until restore can consume the
// same model and the public round-trip contract is complete.
func (s *Session) checkpointWire(ctx context.Context) (checkpointWireDocument, error) {
	if s == nil || s.closed {
		return checkpointWireDocument{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return checkpointWireDocument{}, err
	}
	if s.runGuardHeld() {
		return checkpointWireDocument{}, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return checkpointWireDocument{}, ErrConcurrencyMisuse
	}
	defer s.unlock()

	if s.agendaDriver.dirty {
		return checkpointWireDocument{}, fmt.Errorf("%w: dirty agenda cannot be checkpointed", ErrUnsupportedRuntime)
	}
	if !s.agendaDriver.ready {
		if _, err := s.reconcileAgendaInternal(ctx); err != nil {
			return checkpointWireDocument{}, err
		}
	}
	document, err := s.checkpointWireLocked()
	if err != nil {
		return checkpointWireDocument{}, err
	}
	if err := validateCheckpointWireDocument(document); err != nil {
		return checkpointWireDocument{}, err
	}
	return document, nil
}

func (s *Session) checkpointWireLocked() (checkpointWireDocument, error) {
	initials, err := checkpointWireInitialFacts(s.initials)
	if err != nil {
		return checkpointWireDocument{}, err
	}
	globals, err := s.checkpointWireGlobals()
	if err != nil {
		return checkpointWireDocument{}, err
	}
	facts, err := s.checkpointWireFacts()
	if err != nil {
		return checkpointWireDocument{}, err
	}
	agenda, err := s.checkpointWireAgenda()
	if err != nil {
		return checkpointWireDocument{}, err
	}
	support := s.checkpointWireLogicalSupport()

	return checkpointWireDocument{
		Format:    checkpointWireFormat,
		Version:   checkpointWireVersion,
		RulesetID: s.revision.ID(),
		SessionID: s.id,
		Config: checkpointWireSessionConfig{
			InitialFacts:        initials,
			Globals:             globals,
			Strategy:            checkpointWireStrategy(s.agendaDriver.strategy),
			InitialFocusStack:   cloneModuleNames(s.agendaDriver.initialFocusStack),
			ResetBeforeSnapshot: s.resetBeforeSnapshot,
			DemandCascadeLimit:  s.backchain.demandLimit,
		},
		State: checkpointWireSessionState{
			Generation:        s.factStore.generation,
			NextFactSequence:  s.factStore.nextFactSequence,
			NextRecency:       s.factStore.nextRecency,
			NextRunSequence:   s.nextRunSequence,
			NextEventSequence: s.diagnostics.nextEventSequence,
			Facts:             facts,
			LogicalSupport:    support,
			Agenda:            agenda,
			Backchain: checkpointWireBackchainState{
				Cascades:  s.backchain.demandCounters.Cascades,
				Steps:     s.backchain.demandCounters.Steps,
				LengthMax: s.backchain.demandCounters.LengthMax,
				LimitHits: s.backchain.demandCounters.LimitHits,
			},
		},
	}, nil
}

func checkpointWireStrategy(strategy Strategy) string {
	if strategy == StrategyBreadth {
		return "breadth"
	}
	return "depth"
}

func checkpointWireInitialFacts(initials []SessionInitialFact) ([]checkpointWireInitialFact, error) {
	out := make([]checkpointWireInitialFact, len(initials))
	for i, initial := range initials {
		fields, err := checkpointWireFields(initial.Fields, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: initial fact %d: %v", ErrInvalidCheckpoint, i, err)
		}
		out[i] = checkpointWireInitialFact{
			Name:        initial.name,
			TemplateKey: initial.TemplateKey,
			Fields:      fields,
		}
	}
	return out, nil
}

func (s *Session) checkpointWireGlobals() ([]checkpointWireNamedValue, error) {
	if s == nil || s.revision == nil || len(s.revision.globalOrder) == 0 {
		return []checkpointWireNamedValue{}, nil
	}
	names := append([]string(nil), s.revision.globalOrder...)
	sort.Strings(names)
	out := make([]checkpointWireNamedValue, 0, len(names))
	for _, name := range names {
		global, ok := s.revision.globals[name]
		if !ok || global.slot < 0 || global.slot >= len(s.globalValues) {
			return nil, fmt.Errorf("%w: missing global %q", ErrInvalidCheckpoint, name)
		}
		value, err := checkpointWireValueFromValue(s.globalValues[global.slot])
		if err != nil {
			return nil, fmt.Errorf("%w: global %q: %v", ErrInvalidCheckpoint, name, err)
		}
		out = append(out, checkpointWireNamedValue{Name: name, Value: value})
	}
	return out, nil
}

func (s *Session) checkpointWireFacts() ([]checkpointWireFact, error) {
	out := make([]checkpointWireFact, 0, len(s.factStore.insertionOrder))
	for _, id := range s.factStore.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			return nil, fmt.Errorf("%w: insertion order references missing fact %s", ErrInvalidCheckpoint, id)
		}
		snapshot := fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
		fields, err := checkpointWireFields(snapshot.Fields(), snapshot.FieldPresenceMap())
		if err != nil {
			return nil, fmt.Errorf("%w: fact %s: %v", ErrInvalidCheckpoint, id, err)
		}
		wire := checkpointWireFact{
			ID:      checkpointWireFactIDFromFactID(snapshot.ID()),
			Version: snapshot.Version(),
			Recency: snapshot.Recency(),
			Support: snapshot.Support().State,
			Fields:  fields,
		}
		if snapshot.TemplateKey() != "" {
			wire.TemplateKey = snapshot.TemplateKey()
		} else {
			wire.Name = snapshot.Name()
		}
		out = append(out, wire)
	}
	return out, nil
}

func checkpointWireFields(fields Fields, presence map[string]FieldPresence) ([]checkpointWireField, error) {
	names := make([]string, 0, len(fields)+len(presence))
	seen := make(map[string]struct{}, len(fields)+len(presence))
	for name := range fields {
		names = append(names, name)
		seen[name] = struct{}{}
	}
	for name := range presence {
		if _, exists := seen[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]checkpointWireField, len(names))
	for i, name := range names {
		out[i] = checkpointWireField{Name: name, Presence: presence[name]}
		if raw, ok := fields[name]; ok {
			value, err := checkpointWireValueFromValue(raw)
			if err != nil {
				return nil, err
			}
			out[i].Value = &value
		}
	}
	return out, nil
}

func checkpointWireFactIDFromFactID(id FactID) checkpointWireFactID {
	return checkpointWireFactID{Generation: id.Generation(), Sequence: id.Sequence()}
}

func (s *Session) checkpointWireLogicalSupport() checkpointWireLogicalSupportState {
	ids := make([]SupportID, 0, len(s.tms.logicalSupportEdges))
	for id := range s.tms.logicalSupportEdges {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	edges := make([]checkpointWireLogicalSupportEdge, 0, len(ids))
	for _, id := range ids {
		record := s.tms.logicalSupportEdges[id]
		edge := record.edge
		supporting := make([]checkpointWireFactID, len(edge.SupportingFacts))
		for i, factID := range edge.SupportingFacts {
			supporting[i] = checkpointWireFactIDFromFactID(factID)
		}
		edges = append(edges, checkpointWireLogicalSupportEdge{
			SupportID:       edge.SupportID,
			FactID:          checkpointWireFactIDFromFactID(edge.FactID),
			RuleID:          edge.RuleID,
			RuleRevisionID:  edge.RuleRevisionID,
			ActivationID:    edge.ActivationID,
			Generation:      edge.Generation,
			Source:          checkpointWireCandidateIdentity{ScopeHash: record.source.identityKey.scopeHash, Hash: record.source.identityKey.hash},
			SupportingFacts: supporting,
		})
	}
	counters := s.currentSupportGraph().Counters
	return checkpointWireLogicalSupportState{
		Edges: edges,
		Counters: checkpointWireLogicalSupportCounters{
			CurrentLogicalFacts:          counters.CurrentLogicalFacts,
			CurrentStatedAndLogicalFacts: counters.CurrentStatedAndLogicalFacts,
			CurrentSupportEdges:          counters.CurrentSupportEdges,
			LogicalFactsAsserted:         counters.LogicalFactsAsserted,
			LogicalFactsRetracted:        counters.LogicalFactsRetracted,
			SupportEdgesAdded:            counters.SupportEdgesAdded,
			SupportEdgesRemoved:          counters.SupportEdgesRemoved,
			MetadataOnlyTransitions:      counters.MetadataOnlyTransitions,
			CascadeRetractions:           counters.CascadeRetractions,
			CascadeBreadthMax:            counters.CascadeBreadthMax,
			CascadeDepthMax:              counters.CascadeDepthMax,
		},
	}
}

func (s *Session) checkpointWireAgenda() (checkpointWireAgendaState, error) {
	state := checkpointWireAgendaState{
		Ready:       s.agendaDriver.ready,
		Dirty:       s.agendaDriver.dirty,
		FocusStack:  cloneModuleNames(s.agendaDriver.focusStack),
		Activations: []checkpointWireActivation{},
	}
	agenda := s.agendaDriver.agenda
	if agenda == nil {
		return state, nil
	}
	state.NextOrdinal = agenda.nextOrdinal
	state.NextBirthEpoch = agenda.nextBirthEpoch
	state.InitialBirthEpoch = agenda.initialBirthEpoch
	state.HandleGeneration = agenda.handleGeneration
	for row, activation := range agenda.activations {
		if activation == nil || activation.status == activationStatusDeactivated {
			continue
		}
		materialized := agenda.publicActivation(activation)
		materialized.supportCount = activation.supportCount
		wire, err := checkpointWireActivationFromActivation(materialized)
		if err != nil {
			return checkpointWireAgendaState{}, fmt.Errorf("%w: agenda row %d: %v", ErrInvalidCheckpoint, row, err)
		}
		state.Activations = append(state.Activations, wire)
	}
	terminalActivations, err := s.checkpointWireImplicitRefractions(state.Activations)
	if err != nil {
		return checkpointWireAgendaState{}, err
	}
	state.Activations = append(state.Activations, terminalActivations...)
	return state, nil
}

// A drained agenda releases consumed activation rows. The session refraction
// store retains graph-independent records for those fired matches. Pending or
// still-retained consumed agenda rows captured above win.
func (s *Session) checkpointWireImplicitRefractions(captured []checkpointWireActivation) ([]checkpointWireActivation, error) {
	if s == nil || len(s.refractions.byIdentity) == 0 {
		return nil, nil
	}
	seen := make(map[activationLookupKey]struct{}, len(captured))
	for _, activation := range captured {
		seen[activationLookupKey{
			ruleRevisionID: activation.RuleRevisionID,
			identityKey: candidateIdentityKey{
				scopeHash: activation.Identity.ScopeHash,
				hash:      activation.Identity.Hash,
			},
		}] = struct{}{}
	}
	keys := make([]activationLookupKey, 0, len(s.refractions.byIdentity))
	for key := range s.refractions.byIdentity {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ruleRevisionID != keys[j].ruleRevisionID {
			return keys[i].ruleRevisionID.String() < keys[j].ruleRevisionID.String()
		}
		if keys[i].identityKey.scopeHash != keys[j].identityKey.scopeHash {
			return keys[i].identityKey.scopeHash < keys[j].identityKey.scopeHash
		}
		return keys[i].identityKey.hash < keys[j].identityKey.hash
	})
	out := make([]checkpointWireActivation, 0, len(keys))
	for _, key := range keys {
		wire, err := checkpointWireActivationFromActivation(s.refractions.byIdentity[key])
		if err != nil {
			return nil, err
		}
		out = append(out, wire)
	}
	return out, nil
}

func checkpointWireActivationFromActivation(activation activation) (checkpointWireActivation, error) {
	factIDs := activation.factIDs()
	wireFactIDs := make([]checkpointWireFactID, len(factIDs))
	for i, id := range factIDs {
		wireFactIDs[i] = checkpointWireFactIDFromFactID(id)
	}
	bindings := activation.bindings()
	wireBindings := make([]checkpointWireBinding, len(bindings))
	for i, binding := range bindings {
		wire := checkpointWireBinding{
			Name:           binding.binding,
			Slot:           binding.bindingSlot,
			ConditionOrder: binding.conditionOrder,
			ConditionID:    binding.conditionID,
			ConditionPath:  append([]int(nil), binding.conditionPath...),
			FactID:         checkpointWireFactIDFromFactID(binding.factID),
			FactVersion:    binding.factVersion,
		}
		if binding.hasValue {
			value, err := checkpointWireValueFromValue(binding.value)
			if err != nil {
				return checkpointWireActivation{}, fmt.Errorf("binding %d: %v", i, err)
			}
			wire.Value = &value
		}
		wireBindings[i] = wire
	}
	return checkpointWireActivation{
		Ordinal:          activation.key.ordinal,
		RuleRevisionID:   activation.ruleRevisionID,
		Identity:         checkpointWireCandidateIdentity{ScopeHash: activation.identityKey.scopeHash, Hash: activation.identityKey.hash},
		BirthEpoch:       activation.birthEpoch,
		BirthRank:        activation.birthRank,
		Module:           activation.module,
		Salience:         activation.salience,
		DeclarationOrder: activation.declarationOrder,
		MaxRecency:       activation.maxRecency,
		TotalRecency:     activation.totalRecency,
		SupportCount:     activation.supportCount,
		Status:           activation.status.String(),
		Path:             append([]int(nil), activation.path()...),
		FactIDs:          wireFactIDs,
		FactVersions:     append([]FactVersion(nil), activation.factVersions()...),
		Bindings:         wireBindings,
	}, nil
}
