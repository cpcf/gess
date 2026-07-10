package engine

import "context"

type backchainQueryProofID uint64

func (id backchainQueryProofID) isZero() bool {
	return id == 0
}

type backchainQueryProofContext struct {
	session      *Session
	facts        []workingFact
	slotStorage  []factSlot
	demandQueue  []backchainDemandID
	nextSequence uint64
	nextRecency  Recency
	demandBudget backchainDemandCascadeBudget
	id           backchainQueryProofID
	nextID       backchainQueryProofID
}

func (s *Session) beginBackchainQueryProof() *backchainQueryProofContext {
	proof := &s.backchainQueryProofScratch
	proof.reset(s)
	return proof
}

func (p *backchainQueryProofContext) reset(session *Session) {
	if p == nil {
		return
	}
	for i := range p.facts {
		p.facts[i] = workingFact{}
	}
	p.facts = p.facts[:0]
	for i := range p.slotStorage {
		p.slotStorage[i] = factSlot{}
	}
	p.slotStorage = p.slotStorage[:0]
	clear(p.demandQueue)
	p.demandQueue = p.demandQueue[:0]
	p.session = session
	p.nextID++
	if p.nextID.isZero() {
		p.nextID++
	}
	p.id = p.nextID
	p.nextSequence = 0
	p.nextRecency = session.nextRecency + 1
	p.demandBudget = newBackchainDemandCascadeBudget(session)
}

func (p *backchainQueryProofContext) origin() mutationOrigin {
	if p == nil {
		return mutationOrigin{}
	}
	return mutationOrigin{queryProofID: p.id}
}

func (p *backchainQueryProofContext) flushDemands(ctx context.Context, demands []backchainDemandID, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if p == nil || p.session == nil {
		return combined, nil
	}
	defer p.session.clearBackchainDemandRequestArena()
	if len(demands) == 0 {
		return combined, nil
	}
	p.demandQueue = append(p.demandQueue, demands...)
	queue := p.demandQueue
	for i := 0; i < len(queue); i++ {
		if err := p.demandBudget.consume(); err != nil {
			return combined, err
		}
		demand, ok := p.session.backchainDemandRequestByID(queue[i])
		if !ok {
			combined.supported = false
			continue
		}
		next, err := p.insertDemand(ctx, demand, origin)
		if err != nil {
			return combined, err
		}
		next = normalizeBackchainDemandNoopDelta(next)
		if len(next.demands) > 0 {
			queue = append(queue, next.demands...)
			p.demandQueue = queue
		}
		combined = mergeReteAgendaDelta(combined, next)
	}
	combined.demands = nil
	combined.resolvedDemands = nil
	combined.resolvedOwners = nil
	return combined, nil
}

func (p *backchainQueryProofContext) insertDemand(ctx context.Context, demand backchainDemandRequest, origin mutationOrigin) (reteAgendaDelta, error) {
	if p == nil || p.session == nil {
		return reteAgendaDelta{supported: true}, nil
	}
	session := p.session
	template, ok := session.revision.templateByKey(demand.templateKey)
	if !ok || !template.backchainDemand {
		return reteAgendaDelta{supported: false}, &ValidationError{
			TemplateName: string(demand.templateKey),
			Reason:       "unknown backchain demand template",
		}
	}
	if len(demand.slots) != len(template.fields) {
		return reteAgendaDelta{supported: false}, &ValidationError{
			TemplateName: template.Name(),
			Reason:       "backchain demand slot count does not match template",
		}
	}
	plan, ok := session.revision.generatedFactInsertPlan(template.Key())
	if !ok {
		compiled := newCompiledGeneratedFactInsertPlan(template)
		plan = &compiled
	}
	slots := p.cloneDemandSlots(demand.slots)
	duplicateIndex := plan.duplicateIndex(slots)
	if existing := p.findDemandFact(plan, duplicateIndex, slots); existing != nil {
		return reteAgendaDelta{supported: true}, nil
	}
	factValue := p.newDemandFact(plan, slots)
	p.facts = append(p.facts, factValue)
	fact := &p.facts[len(p.facts)-1]
	if session.rete == nil || session.rete.graphBeta == nil {
		return reteAgendaDelta{supported: false}, ErrInvalidRuleset
	}
	return session.rete.propagateBetaEvent(ctx, newReteGraphGeneratedAssertEvent(fact, origin, nil))
}

func (p *backchainQueryProofContext) cloneDemandSlots(slots []factSlot) []factSlot {
	if len(slots) == 0 {
		return nil
	}
	start := len(p.slotStorage)
	p.slotStorage = append(p.slotStorage, slots...)
	return p.slotStorage[start:len(p.slotStorage)]
}

func (p *backchainQueryProofContext) findDemandFact(plan *compiledGeneratedFactInsertPlan, duplicateIndex duplicateIndexKey, slots []factSlot) *workingFact {
	if p == nil || plan == nil || plan.duplicatePolicy == DuplicateAllow {
		return nil
	}
	for i := range p.facts {
		fact := &p.facts[i]
		if fact.templateID != plan.templateID || plan.duplicateIndex(fact.fieldSlotSlice()) != duplicateIndex {
			continue
		}
		if duplicateIndex.kind == duplicateIndexStructural && !structuralDuplicateSlotsEqual(plan.template, slots, fact.fieldSlotSlice()) {
			continue
		}
		return fact
	}
	return nil
}

func (p *backchainQueryProofContext) newDemandFact(plan *compiledGeneratedFactInsertPlan, slots []factSlot) workingFact {
	p.nextSequence++
	sequence := ^uint64(p.nextSequence)
	recency := p.nextRecency
	p.nextRecency++
	fact := workingFact{
		id:                   newFactID(p.session.generation, sequence),
		version:              1,
		recency:              recency,
		supportState:         factSupportCodeFromState(FactSupportLogical),
		targetIndexesSkipped: true,
	}
	fact.setTemplateIdentity(plan.templateKey, plan.templateID)
	fact.setName(plan.name)
	if plan.name != "" {
		fact.setTemplateKey(plan.templateKey)
	}
	fact.setFieldSlots(slots)
	return fact
}

func (p *backchainQueryProofContext) workingFactByID(id FactID) (*workingFact, bool) {
	if p == nil || id.IsZero() {
		return nil, false
	}
	for i := range p.facts {
		if p.facts[i].id == id {
			return &p.facts[i], true
		}
	}
	return nil, false
}

func (p *backchainQueryProofContext) cleanup(ctx context.Context) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if p == nil || p.session == nil || p.session.rete == nil || p.session.rete.graphBeta == nil {
		return combined, nil
	}
	for i := len(p.facts) - 1; i >= 0; i-- {
		fact := &p.facts[i]
		delta, err := p.session.rete.propagateBetaEvent(ctx, newReteGraphGeneratedRetractEvent(fact, p.origin(), p.session.propagationCounters))
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, normalizeBackchainDemandNoopDelta(delta))
	}
	p.facts = p.facts[:0]
	p.session.clearBackchainDemandRequestArena()
	combined.demands = nil
	combined.resolvedDemands = nil
	combined.resolvedOwners = nil
	return combined, nil
}
