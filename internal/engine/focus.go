package engine

import (
	"context"
	"fmt"
)

type focusMutationResult struct{}

// CurrentFocus returns the current agenda focus, falling back to MAIN when the
// session focus stack is empty.
func (s *Session) CurrentFocus() ModuleName {
	if s == nil {
		return MainModule
	}
	return s.currentFocusInternal()
}

// FocusStack returns a copy of the session-local focus stack from bottom to top.
func (s *Session) FocusStack() []ModuleName {
	if s == nil || len(s.agendaDriver.focusStack) == 0 {
		return nil
	}
	return append([]ModuleName(nil), s.agendaDriver.focusStack...)
}

// PushFocus places module on top of the session focus stack unless it is
// already current.
func (s *Session) PushFocus(ctx context.Context, module ModuleName) error {
	return s.setFocusWithContext(ctx, module)
}

// SetFocus has the same practical behavior as PushFocus.
func (s *Session) SetFocus(ctx context.Context, module ModuleName) error {
	return s.setFocusWithContext(ctx, module)
}

// PopFocus removes and returns the current focus frame. If the stack is empty,
// it returns MAIN without mutating the stack.
func (s *Session) PopFocus(ctx context.Context) (ModuleName, error) {
	if s == nil || s.closed {
		return MainModule, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MainModule, err
	}
	if s.shouldQueueMutationDuringRun(mutationOrigin{}) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(context.Context) (any, reteAgendaDelta, error) {
				return s.popFocusInternal(), reteAgendaDelta{}, nil
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				if outcome.err != nil {
					return MainModule, outcome.err
				}
				if module, ok := outcome.value.(ModuleName); ok {
					return module, nil
				}
				return MainModule, ErrInvalidRuleset
			case <-ctx.Done():
				return MainModule, ctx.Err()
			}
		}
	}
	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return MainModule, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	return s.popFocusInternal(), nil
}

// ClearFocusStack empties the session focus stack. CurrentFocus then reports
// MAIN as the fallback.
func (s *Session) ClearFocusStack(ctx context.Context) error {
	if s == nil || s.closed {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.shouldQueueMutationDuringRun(mutationOrigin{}) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(context.Context) (any, reteAgendaDelta, error) {
				s.clearFocusStackInternal()
				return focusMutationResult{}, reteAgendaDelta{}, nil
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				return outcome.err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	s.clearFocusStackInternal()
	return nil
}

func (c actionContext) PushFocus(module ModuleName) error {
	if c.session == nil {
		return ErrClosedSession
	}
	return c.session.pushFocusWithOrigin(c.Context(), module, c.mutationOrigin())
}

func (c actionContext) SetFocus(module ModuleName) error {
	return c.PushFocus(module)
}

func (c actionContext) PopFocus() (ModuleName, error) {
	if c.session == nil {
		return MainModule, ErrClosedSession
	}
	return c.session.popFocusWithOrigin(c.Context(), c.mutationOrigin())
}

func (c actionContext) ClearFocusStack() error {
	if c.session == nil {
		return ErrClosedSession
	}
	return c.session.clearFocusStackWithOrigin(c.Context(), c.mutationOrigin())
}

func (s *Session) setFocusWithContext(ctx context.Context, module ModuleName) error {
	if s == nil || s.closed {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := s.validateFocusModule(module)
	if err != nil {
		return err
	}
	if s.shouldQueueMutationDuringRun(mutationOrigin{}) {
		resultCh := make(chan queuedMutationResult, 1)
		if s.enqueueMutationDuringRun(queuedMutation{
			ctx: ctx,
			apply: func(context.Context) (any, reteAgendaDelta, error) {
				s.pushFocusInternal(normalized)
				return focusMutationResult{}, reteAgendaDelta{}, nil
			},
			result: resultCh,
		}) {
			select {
			case outcome := <-resultCh:
				return outcome.err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	locked, ok := s.beginMutationForOrigin(mutationOrigin{})
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	s.pushFocusInternal(normalized)
	return nil
}

func (s *Session) pushFocusWithOrigin(ctx context.Context, module ModuleName, origin mutationOrigin) error {
	if s == nil || s.closed {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := s.validateFocusModule(module)
	if err != nil {
		return err
	}
	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	s.pushFocusInternal(normalized)
	return nil
}

func (s *Session) popFocusWithOrigin(ctx context.Context, origin mutationOrigin) (ModuleName, error) {
	if s == nil || s.closed {
		return MainModule, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MainModule, err
	}
	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return MainModule, ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	return s.popFocusInternal(), nil
}

func (s *Session) clearFocusStackWithOrigin(ctx context.Context, origin mutationOrigin) error {
	if s == nil || s.closed {
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
	s.clearFocusStackInternal()
	return nil
}

func (s *Session) validateFocusModule(module ModuleName) (ModuleName, error) {
	if s == nil || s.revision == nil {
		return MainModule, ErrInvalidRuleset
	}
	normalized := normalizeModuleName(module)
	if _, ok := s.revision.modules[normalized]; !ok {
		return normalized, &ValidationError{Reason: fmt.Sprintf("unknown module %q", normalized)}
	}
	return normalized, nil
}

func (s *Session) currentFocusInternal() ModuleName {
	if s == nil {
		return MainModule
	}
	return s.agendaDriver.currentFocus()
}

func (s *Session) pushFocusInternal(module ModuleName) {
	if s == nil {
		return
	}
	s.agendaDriver.pushFocus(module)
}

func (s *Session) popFocusInternal() ModuleName {
	if s == nil {
		return MainModule
	}
	return s.agendaDriver.popFocus()
}

func (s *Session) clearFocusStackInternal() {
	if s == nil {
		return
	}
	s.agendaDriver.clearFocusStack()
}

func (s *Session) resetFocusStack() {
	if s == nil {
		return
	}
	s.agendaDriver.resetFocusStack()
}

func (s *Session) nextFocusedActivation() (*activation, activation, bool) {
	if s == nil || s.agendaDriver.agenda == nil {
		return nil, activation{}, false
	}
	for {
		module := s.currentFocusInternal()
		if module == MainModule && s.revision != nil && s.revision.allRulesInMainModule {
			return s.agendaDriver.agenda.nextInternalPtr()
		}
		current, selected, ok := s.agendaDriver.agenda.nextInternalPtrForModule(module)
		if ok {
			return current, selected, true
		}
		if len(s.agendaDriver.focusStack) == 0 {
			return nil, activation{}, false
		}
		s.popFocusInternal()
	}
}

func (s *Session) hasFocusedActivation() bool {
	if s == nil || s.agendaDriver.agenda == nil {
		return false
	}
	depth := len(s.agendaDriver.focusStack)
	for {
		module := MainModule
		if depth > 0 {
			module = s.agendaDriver.focusStack[depth-1]
			if module.IsZero() {
				module = MainModule
			}
		}
		if module == MainModule && s.revision != nil && s.revision.allRulesInMainModule {
			return s.agendaDriver.agenda.hasPendingActivation()
		}
		if s.agendaDriver.agenda.hasPendingActivationForModule(module) {
			return true
		}
		if depth == 0 {
			return false
		}
		depth--
	}
}

func (s *Session) applyAutoFocus(changes []agendaChange) {
	if s == nil || s.revision == nil || len(changes) == 0 {
		return
	}
	for _, change := range changes {
		if change.kind != agendaChangeActivated {
			continue
		}
		rule, ok := s.revision.rulesByRevisionID[change.activation.ruleRevisionID]
		if !ok || !rule.effectiveAutoFocus {
			continue
		}
		s.pushFocusInternal(rule.module)
	}
}

func (s *Session) shouldCollectAgendaChanges() bool {
	return s != nil && (s.hasAgendaEventListeners() || s.revision.hasAutoFocusRules())
}
