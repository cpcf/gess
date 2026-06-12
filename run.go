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

	snapshot := s.snapshotLocked()
	if _, err := s.reconcileAgenda(ctx, snapshot); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return RunResult{RunID: runID, Status: RunCanceled}, err
		}
		return RunResult{RunID: runID, Status: RunFailed}, err
	}

	fired := 0
	for {
		if err := ctx.Err(); err != nil {
			return RunResult{RunID: runID, Status: RunCanceled, Fired: fired}, err
		}

		activation, ok := s.agenda.next()
		if !ok {
			return RunResult{RunID: runID, Status: RunCompleted, Fired: fired}, nil
		}
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
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return RunResult{RunID: runID, Status: RunCanceled, Fired: fired}, err
			}
			var actionFailure *ActionFailureError
			if errors.As(err, &actionFailure) {
				s.emitActionFailedEvent(ctx, runID, activation, *actionFailure)
				return RunResult{RunID: runID, Status: RunActionFailed, Fired: fired}, actionFailure
			}
			return RunResult{RunID: runID, Status: RunFailed, Fired: fired}, err
		}

		snapshot = s.snapshotLocked()
		if _, err := s.reconcileAgenda(ctx, snapshot); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return RunResult{RunID: runID, Status: RunCanceled, Fired: fired}, err
			}
			return RunResult{RunID: runID, Status: RunFailed, Fired: fired}, err
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
	if s == nil {
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
		ActivationID:   activation.id,
		FactIDs:        cloneFactIDs(activation.factIDs),
	})
}

func (s *Session) emitActionFailedEvent(ctx context.Context, runID RunID, activation activation, failure ActionFailureError) {
	if s == nil {
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
