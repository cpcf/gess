package gess

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
	if s == nil || len(s.focusStack) == 0 {
		return nil
	}
	return append([]ModuleName(nil), s.focusStack...)
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

func (c ActionContext) PushFocus(module ModuleName) error {
	if c.session == nil {
		return ErrClosedSession
	}
	return c.session.pushFocusWithOrigin(c.Context(), module, c.mutationOrigin())
}

func (c ActionContext) SetFocus(module ModuleName) error {
	return c.PushFocus(module)
}

func (c ActionContext) PopFocus() (ModuleName, error) {
	if c.session == nil {
		return MainModule, ErrClosedSession
	}
	return c.session.popFocusWithOrigin(c.Context(), c.mutationOrigin())
}

func (c ActionContext) ClearFocusStack() error {
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
	if s == nil || len(s.focusStack) == 0 {
		return MainModule
	}
	return normalizeModuleName(s.focusStack[len(s.focusStack)-1])
}

func (s *Session) pushFocusInternal(module ModuleName) {
	if s == nil {
		return
	}
	module = normalizeModuleName(module)
	if s.currentFocusInternal() == module {
		return
	}
	s.focusStack = append(s.focusStack, module)
}

func (s *Session) popFocusInternal() ModuleName {
	if s == nil || len(s.focusStack) == 0 {
		return MainModule
	}
	top := normalizeModuleName(s.focusStack[len(s.focusStack)-1])
	s.focusStack[len(s.focusStack)-1] = ""
	s.focusStack = s.focusStack[:len(s.focusStack)-1]
	return top
}

func (s *Session) clearFocusStackInternal() {
	if s == nil {
		return
	}
	for i := range s.focusStack {
		s.focusStack[i] = ""
	}
	s.focusStack = s.focusStack[:0]
}

func (s *Session) resetFocusStack() {
	if s == nil {
		return
	}
	s.focusStack = append(s.focusStack[:0], s.initialFocusStack...)
	if len(s.focusStack) == 0 {
		s.focusStack = append(s.focusStack, MainModule)
	}
}

func (s *Session) nextFocusedActivation() (*activation, activation, bool) {
	if s == nil || s.agenda == nil {
		return nil, activation{}, false
	}
	for {
		module := s.currentFocusInternal()
		current, selected, ok := s.agenda.nextInternalPtrForModule(module)
		if ok {
			return current, selected, true
		}
		if len(s.focusStack) == 0 {
			return nil, activation{}, false
		}
		s.popFocusInternal()
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
	return s != nil && (len(s.listeners) > 0 || s.revision.hasAutoFocusRules())
}
