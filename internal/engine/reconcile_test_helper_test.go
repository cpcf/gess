package engine

import "context"

// reconcileAgenda is the test-only whole-terminal parity oracle. Production
// lifecycle paths consume graph-emitted terminal deltas instead.
func (s *Session) reconcileAgenda(ctx context.Context, source factSource) ([]agendaChange, error) {
	if s == nil || s.closed {
		return nil, ErrClosedSession
	}
	if s.revision == nil || source == nil {
		return nil, ErrInvalidRuleset
	}
	if s.agendaDriver.agenda == nil {
		s.agendaDriver.ensureAgenda()
		s.syncPropagationCounters()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.agendaDriver.isReady() {
		return nil, nil
	}
	if s.propagation.runtime == nil {
		return nil, ErrUnsupportedRuntime
	}
	phase := s.propagationCounterPhase()
	if s.propagation.counters != nil {
		s.propagation.counters.recordOracleStyleMatchRequest(phase)
	}
	results, err := s.propagation.runtime.match(ctx, source)
	if err != nil {
		return nil, err
	}
	if s.propagation.counters != nil {
		s.propagation.counters.recordWholeTerminalScan(phase)
		s.propagation.counters.recordFullAgendaReconcile(phase)
	}
	changes, err := s.agendaDriver.agenda.reconcile(ctx, s.revision, results)
	if err != nil {
		return nil, err
	}
	s.agendaDriver.markReady()
	s.applyAutoFocus(changes)
	s.emitAgendaEvents(ctx, changes)
	return changes, nil
}
