package gess

import (
	"context"
	"errors"
	"strconv"
)

func (s *Session) Run(ctx context.Context) (RunResult, error) {
	if s == nil || s.closed {
		return RunResult{Status: RunClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RunResult{Status: RunCanceled}, err
	}
	if !s.beginMutation() {
		return RunResult{Status: RunConcurrencyMisuse}, ErrConcurrencyMisuse
	}
	if s.closed {
		s.endMutation()
		return RunResult{Status: RunClosed}, ErrClosedSession
	}
	if err := ctx.Err(); err != nil {
		s.endMutation()
		return RunResult{Status: RunCanceled}, err
	}
	if s.revision == nil {
		s.endMutation()
		return RunResult{Status: RunFailed}, ErrInvalidRuleset
	}
	if !s.beginRun() {
		s.endMutation()
		return RunResult{Status: RunConcurrencyMisuse}, ErrConcurrencyMisuse
	}

	s.nextRunSequence++
	runID := RunID("run:" + strconv.FormatUint(s.nextRunSequence, 10))
	s.setRunState(runGuardState{
		runID:  runID,
		active: true,
	})
	s.endMutation()
	defer s.endRun()
	defer s.setRunState(runGuardState{})
	var runErr error
	abort := func(status RunStatus, fired int, err error) (RunResult, error) {
		runErr = err
		return RunResult{RunID: runID, Status: status, Fired: fired}, err
	}
	defer func() {
		if runErr != nil {
			s.failQueuedMutations(runErr)
		}
	}()

	if err := s.drainQueuedMutations(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return abort(RunCanceled, 0, err)
		}
		return abort(RunFailed, 0, err)
	}
	if !s.agendaReady || s.agendaDirty {
		if _, err := s.reconcileAgendaInternal(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return abort(RunCanceled, 0, err)
			}
			return abort(RunFailed, 0, err)
		}
	}

	fired := 0
	for {
		if err := ctx.Err(); err != nil {
			return abort(RunCanceled, fired, err)
		}

		if err := s.drainQueuedMutations(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return abort(RunCanceled, fired, err)
			}
			return abort(RunFailed, fired, err)
		}

		s.mutationQueueMu.Lock()
		if len(s.mutationQueue) > 0 {
			s.mutationQueueMu.Unlock()
			continue
		}
		activation, ok := s.agenda.next()
		if !ok {
			s.endRun()
			s.setRunState(runGuardState{})
			s.mutationQueueMu.Unlock()
			return RunResult{RunID: runID, Status: RunCompleted, Fired: fired}, nil
		}
		s.mutationQueueMu.Unlock()
		fired++

		s.emitRuleFiredEvent(ctx, runID, activation)

		s.setRunState(runGuardState{
			runID:               runID,
			active:              true,
			allowMutationOrigin: activation.mutationOrigin(),
		})
		err := s.executeActivationActions(ctx, runID, activation)
		s.setRunState(runGuardState{
			runID:  runID,
			active: true,
		})
		if err != nil {
			s.abandonRunAgendaDelta()
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return abort(RunCanceled, fired, err)
			}
			var actionFailure *ActionFailureError
			if errors.As(err, &actionFailure) {
				s.emitActionFailedEvent(ctx, runID, activation, *actionFailure)
				return abort(RunActionFailed, fired, actionFailure)
			}
			return abort(RunFailed, fired, err)
		}

		if err := s.reconcileRunAgendaDelta(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return abort(RunCanceled, fired, err)
			}
			return abort(RunFailed, fired, err)
		}
		if s.consumeAgendaDirty() {
			if _, err := s.reconcileAgendaInternal(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return abort(RunCanceled, fired, err)
				}
				return abort(RunFailed, fired, err)
			}
		}
	}
}

func (s *Session) beginRun() bool {
	if s == nil || s.runGuard == nil {
		return false
	}
	select {
	case s.runGuard <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Session) endRun() {
	if s == nil || s.runGuard == nil {
		return
	}
	select {
	case <-s.runGuard:
	default:
	}
}

func (s *Session) emitRuleFiredEvent(ctx context.Context, runID RunID, activation activation) {
	if s == nil || len(s.listeners) == 0 {
		return
	}
	rulesetID := RulesetID("")
	if s.revision != nil {
		rulesetID = s.revision.ID()
	}
	s.nextEventSequence++
	s.emitEvent(ctx, Event{
		SessionID:      s.id,
		RulesetID:      rulesetID,
		RunID:          runID,
		Sequence:       s.nextEventSequence,
		Timestamp:      s.eventClock(),
		Type:           EventRuleFired,
		Severity:       EventSeverityInfo,
		Generation:     activation.generation,
		Recency:        activation.maxRecency,
		RuleID:         activation.ruleID,
		RuleRevisionID: activation.ruleRevisionID,
		ActivationID:   activation.activationID(),
		FactIDs:        cloneFactIDs(activation.factIDs),
	})
}

func (s *Session) emitActionFailedEvent(ctx context.Context, runID RunID, activation activation, failure ActionFailureError) {
	if s == nil || len(s.listeners) == 0 {
		return
	}
	rulesetID := RulesetID("")
	if s.revision != nil {
		rulesetID = s.revision.ID()
	}
	s.nextEventSequence++
	s.emitEvent(ctx, Event{
		SessionID:      s.id,
		RulesetID:      rulesetID,
		RunID:          runID,
		Sequence:       s.nextEventSequence,
		Timestamp:      s.eventClock(),
		Type:           EventActionFailed,
		Severity:       EventSeverityError,
		Generation:     activation.generation,
		Recency:        activation.maxRecency,
		RuleID:         failure.RuleID,
		RuleRevisionID: failure.RuleRevisionID,
		ActivationID:   failure.ActivationID,
		ActionName:     failure.ActionName,
		ActionIndex:    failure.ActionIndex,
		Cause:          failure.Err,
		FactIDs:        cloneFactIDs(activation.factIDs),
	})
}
