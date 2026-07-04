package engine

import (
	"context"
	"errors"
	"fmt"
)

type RunOption interface {
	applyRunOption(*runConfig) error
}

type runOptionFunc func(*runConfig) error

func (f runOptionFunc) applyRunOption(config *runConfig) error {
	if f == nil {
		return &ValidationError{Reason: "run option is nil"}
	}
	return f(config)
}

type runConfig struct {
	maxFirings    int
	hasMaxFirings bool
}

func WithMaxFirings(n int) RunOption {
	return runOptionFunc(func(config *runConfig) error {
		if n <= 0 {
			return &ValidationError{Reason: "max firings must be greater than zero"}
		}
		config.maxFirings = n
		config.hasMaxFirings = true
		return nil
	})
}

func newRunConfig(opts []RunOption) (runConfig, error) {
	var config runConfig
	for _, opt := range opts {
		if opt == nil {
			return runConfig{}, &ValidationError{Reason: "run option is nil"}
		}
		if err := opt.applyRunOption(&config); err != nil {
			return runConfig{}, err
		}
	}
	return config, nil
}

func (s *Session) Run(ctx context.Context, opts ...RunOption) (RunResult, error) {
	if s == nil || s.closed {
		return RunResult{Status: RunClosed}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := newRunConfig(opts)
	if err != nil {
		return RunResult{Status: RunFailed}, err
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

	result, mutationHeld, err := s.runAgendaWithMutationReleased(ctx, config)
	if mutationHeld {
		s.endMutation()
	}
	return result, err
}

func (s *Session) runAgendaWithMutationReleased(ctx context.Context, config runConfig) (RunResult, bool, error) {
	if !s.beginRun() {
		return RunResult{Status: RunConcurrencyMisuse}, true, ErrConcurrencyMisuse
	}

	s.nextRunSequence++
	runID := RunID(s.nextRunSequence)
	s.runHaltRequested.Store(false)
	s.runActive.Store(true)
	s.endMutation()

	result, err := s.runAgendaLoop(ctx, runID, config)
	mutationHeld := s.beginMutation()
	s.runActivation.Store(nil)
	s.runActive.Store(false)
	s.endRun()
	if !mutationHeld {
		return result, false, ErrConcurrencyMisuse
	}
	return result, true, err
}

func (s *Session) runAgendaLoop(ctx context.Context, runID RunID, config runConfig) (RunResult, error) {
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
	if s.agendaDirty {
		return abort(RunFailed, 0, fmt.Errorf("%w: dirty agenda cannot be reconciled during run", ErrUnsupportedRuntime))
	}
	if !s.agendaReady {
		if ok, err := s.reconcileAgendaWithoutSnapshotAndChanges(ctx); ok || err != nil {
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return abort(RunCanceled, 0, err)
				}
				return abort(RunFailed, 0, err)
			}
		} else {
			if _, err := s.reconcileAgenda(ctx, s); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return abort(RunCanceled, 0, err)
				}
				return abort(RunFailed, 0, err)
			}
		}
	}

	s.reserveRunGeneratedFactStorage()

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

		if err := ctx.Err(); err != nil {
			return abort(RunCanceled, fired, err)
		}

		s.mutationQueueMu.Lock()
		if len(s.mutationQueue) > 0 {
			s.mutationQueueMu.Unlock()
			continue
		}
		if s.runHaltRequested.Load() {
			s.mutationQueueMu.Unlock()
			return RunResult{RunID: runID, Status: RunHalted, Fired: fired}, nil
		}
		if config.hasMaxFirings && fired >= config.maxFirings {
			hasMore := s.hasFocusedActivation()
			s.mutationQueueMu.Unlock()
			if !hasMore {
				if s.agenda != nil {
					s.agenda.compactConsumedActivationRows()
				}
				return RunResult{RunID: runID, Status: RunCompleted, Fired: fired}, nil
			}
			return RunResult{RunID: runID, Status: RunFireLimit, Fired: fired}, nil
		}
		currentActivation, activation, ok := s.nextFocusedActivation()
		if !ok {
			s.mutationQueueMu.Unlock()
			if s.agenda != nil {
				s.agenda.compactConsumedActivationRows()
			}
			return RunResult{RunID: runID, Status: RunCompleted, Fired: fired}, nil
		}
		s.mutationQueueMu.Unlock()
		fired++

		s.emitRuleFiredEvent(ctx, runID, activation)

		s.runActivation.Store(currentActivation)
		err := s.executeTrustedActivationActions(ctx, runID, activation)
		s.runActivation.Store(nil)
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
		if s.agendaDirty {
			return abort(RunFailed, fired, fmt.Errorf("%w: dirty agenda cannot be reconciled during run", ErrUnsupportedRuntime))
		}
	}
}

func (s *Session) requestRunHalt() {
	if s == nil {
		return
	}
	s.runHaltRequested.Store(true)
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
	ruleID := RuleID("")
	if s.revision != nil {
		if rule, ok := s.revision.rulesByRevisionID[activation.ruleRevisionID]; ok {
			ruleID = rule.id
		}
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
		Generation:     activation.Generation(),
		Recency:        activation.maxRecency,
		RuleID:         ruleID,
		RuleRevisionID: activation.ruleRevisionID,
		ActivationID:   activation.activationID(),
		FactIDs:        cloneActivationFactIDs(&activation),
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
		Generation:     activation.Generation(),
		Recency:        activation.maxRecency,
		RuleID:         failure.RuleID,
		RuleRevisionID: failure.RuleRevisionID,
		ActivationID:   failure.ActivationID,
		ActionName:     failure.ActionName,
		ActionIndex:    failure.ActionIndex,
		Cause:          failure.Err,
		FactIDs:        cloneActivationFactIDs(&activation),
	})
}
