package engine

// sessionPropagationCoordinator owns the mutable compiled-graph runtime,
// propagation observability, and run-local terminal-delta lifecycle for one
// session. Forks always receive a freshly initialized coordinator so none of
// the runtime or reconciliation scratch can alias its parent.
type sessionPropagationCoordinator struct {
	runtime  *reteRuntime
	counters *propagationCounterLedger

	runAgendaDelta   reteAgendaDelta
	runAgendaDeltas  []reteAgendaDelta
	runAgendaStates  []runAgendaDeltaState
	runAgendaBuckets map[candidateIdentity]int
	runAgendaAdded   []reteTerminalTokenDelta
	runAgendaRemoved []reteTerminalTokenDelta
	runAgendaUpdated []reteTerminalTokenUpdate
	runAgendaPending bool
	runAgendaDirect  bool
}

func newSessionPropagationCoordinator(runtime *reteRuntime) sessionPropagationCoordinator {
	return sessionPropagationCoordinator{runtime: runtime}
}

func forkSessionPropagationCoordinator(runtime *reteRuntime, parent *sessionPropagationCoordinator) sessionPropagationCoordinator {
	child := newSessionPropagationCoordinator(runtime)
	if parent != nil && parent.counters != nil {
		child.counters = newPropagationCounterLedger()
	}
	return child
}

func (p *sessionPropagationCoordinator) installRuntime(runtime *reteRuntime) {
	if p == nil {
		return
	}
	p.runtime = runtime
}

func (p *sessionPropagationCoordinator) attachCounters() *propagationCounterLedger {
	if p == nil {
		return nil
	}
	if p.counters == nil {
		p.counters = newPropagationCounterLedger()
	}
	return p.counters
}

func (p *sessionPropagationCoordinator) syncCounters() {
	if p == nil || p.counters == nil {
		return
	}
	if p.runtime != nil && p.runtime.graphBeta != nil {
		p.counters.setTerminalRowsRetained(p.runtime.graphBeta.terminalRowCount())
	} else {
		p.counters.setTerminalRowsRetained(0)
	}
	path, unsupportedReasons := propagationRuntimeUnknown, map[string]int(nil)
	if p.runtime != nil {
		path, unsupportedReasons = p.runtime.propagationDiagnostics()
	}
	p.counters.setRuntimeDiagnostics(path, unsupportedReasons)
}

func (p *sessionPropagationCoordinator) releaseTransientAgendaDeltas() {
	if p == nil || p.runtime == nil || p.runtime.graphBeta == nil {
		return
	}
	p.runtime.graphBeta.releaseTransientTerminalDeltas()
}

func (p *sessionPropagationCoordinator) clearRunAgendaDelta() {
	if p == nil || !p.runAgendaPending {
		return
	}
	clear(p.runAgendaDelta.added)
	clear(p.runAgendaDelta.removed)
	clear(p.runAgendaDelta.updated)
	p.runAgendaDelta.added = p.runAgendaDelta.added[:0]
	p.runAgendaDelta.removed = p.runAgendaDelta.removed[:0]
	p.runAgendaDelta.updated = p.runAgendaDelta.updated[:0]
	p.runAgendaDelta.supported = false
	for i := range p.runAgendaDeltas {
		clear(p.runAgendaDeltas[i].added)
		clear(p.runAgendaDeltas[i].removed)
		clear(p.runAgendaDeltas[i].updated)
		p.runAgendaDeltas[i].added = p.runAgendaDeltas[i].added[:0]
		p.runAgendaDeltas[i].removed = p.runAgendaDeltas[i].removed[:0]
		p.runAgendaDeltas[i].updated = p.runAgendaDeltas[i].updated[:0]
		p.runAgendaDeltas[i].supported = false
	}
	p.runAgendaDeltas = p.runAgendaDeltas[:0]
	for i := range p.runAgendaStates {
		p.runAgendaStates[i] = runAgendaDeltaState{}
	}
	p.runAgendaStates = p.runAgendaStates[:0]
	if p.runAgendaBuckets != nil {
		clear(p.runAgendaBuckets)
	}
	clear(p.runAgendaAdded)
	clear(p.runAgendaRemoved)
	clear(p.runAgendaUpdated)
	p.runAgendaAdded = p.runAgendaAdded[:0]
	p.runAgendaRemoved = p.runAgendaRemoved[:0]
	p.runAgendaUpdated = p.runAgendaUpdated[:0]
	p.runAgendaPending = false
	p.runAgendaDirect = false
}
