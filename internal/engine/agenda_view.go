package engine

import (
	"context"
	"fmt"
	"slices"
)

// Agenda is an immutable point-in-time view of a session's pending agenda.
type Agenda struct {
	focusStack  []ModuleName
	activations []AgendaActivation
	byModule    map[ModuleName][]AgendaActivation
}

// AgendaActivation describes one pending rule activation without exposing
// internal token or join structures.
type AgendaActivation struct {
	activationID   ActivationID
	ruleID         RuleID
	ruleRevisionID RuleRevisionID
	ruleName       string
	module         ModuleName
	salience       int
	factIDs        []FactID
}

// Agenda returns an idle-only, point-in-time view of the pending agenda. The
// returned value is immutable and is invalidated by any subsequent session
// mutation or run.
//
// If the agenda needs reconciliation (for example after ApplyRuleset), Agenda
// performs the same reconciliation Run would, which can push auto-focus
// frames and emit activation events.
func (s *Session) Agenda(ctx context.Context) (Agenda, error) {
	if s == nil || s.closed {
		return Agenda{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Agenda{}, err
	}
	if s.runGuardHeld() {
		return Agenda{}, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return Agenda{}, ErrConcurrencyMisuse
	}
	defer s.unlock()

	if s.agendaDriver.dirty {
		return Agenda{}, fmt.Errorf("%w: dirty agenda cannot be reconciled during run", ErrUnsupportedRuntime)
	}
	if !s.agendaDriver.ready {
		if _, err := s.reconcileAgendaInternal(ctx); err != nil {
			return Agenda{}, err
		}
	}
	return s.agendaLocked(), nil
}

// FocusStack returns a copy of the session focus stack from bottom to top at
// the time the agenda view was captured.
func (a Agenda) FocusStack() []ModuleName {
	return cloneModuleNames(a.focusStack)
}

// Activations returns pending activations in the order Run would fire them
// from the captured focus stack if no further mutations occurred. Pending
// activations in modules not reachable from the captured focus stack (or
// MAIN) never fire in that state, so they are excluded here; use
// ActivationsForModule to inspect them.
func (a Agenda) Activations() []AgendaActivation {
	return cloneAgendaActivations(a.activations)
}

// ActivationsForModule returns pending activations for module in module-local
// firing order. The result is independent of the captured focus stack.
func (a Agenda) ActivationsForModule(module ModuleName) []AgendaActivation {
	if len(a.byModule) == 0 {
		return []AgendaActivation{}
	}
	normalized := normalizeModuleName(module)
	if normalized.IsZero() {
		normalized = MainModule
	}
	return cloneAgendaActivations(a.byModule[normalized])
}

// Len reports the number of activations in fire order, matching
// Activations; it excludes pending activations in unfocused modules.
func (a Agenda) Len() int {
	return len(a.activations)
}

func (a AgendaActivation) ActivationID() ActivationID {
	return a.activationID
}

func (a AgendaActivation) RuleID() RuleID {
	return a.ruleID
}

func (a AgendaActivation) RuleRevisionID() RuleRevisionID {
	return a.ruleRevisionID
}

func (a AgendaActivation) RuleName() string {
	return a.ruleName
}

func (a AgendaActivation) Module() ModuleName {
	return a.module
}

func (a AgendaActivation) Salience() int {
	return a.salience
}

func (a AgendaActivation) FactIDs() []FactID {
	return cloneFactIDs(a.factIDs)
}

func (s *Session) agendaLocked() Agenda {
	out := Agenda{
		focusStack: cloneModuleNames(s.agendaDriver.focusStack),
		byModule:   make(map[ModuleName][]AgendaActivation),
	}
	if s == nil || s.agendaDriver.agenda == nil {
		out.activations = []AgendaActivation{}
		return out
	}
	pending := s.agendaDriver.agenda.pendingByModule()
	if len(pending) == 0 {
		out.activations = []AgendaActivation{}
		return out
	}
	for module, activations := range pending {
		view := make([]AgendaActivation, 0, len(activations))
		for _, act := range activations {
			view = append(view, s.agendaActivationView(act))
		}
		out.byModule[module] = view
	}
	ordered := s.agendaDriver.agenda.activationsInFocusOrder(pending, s.agendaDriver.focusStack)
	out.activations = make([]AgendaActivation, len(ordered))
	for i, act := range ordered {
		out.activations[i] = s.agendaActivationView(act)
	}
	return out
}

func (s *Session) agendaActivationView(act *activation) AgendaActivation {
	if act == nil {
		return AgendaActivation{}
	}
	public := s.agendaDriver.agenda.publicActivation(act)
	view := AgendaActivation{
		activationID:   public.activationID(),
		ruleRevisionID: public.ruleRevisionID,
		module:         public.module,
		salience:       public.salience,
		factIDs:        cloneActivationFactIDs(&public),
	}
	if s != nil && s.revision != nil {
		if rule, ok := s.revision.rulesByRevisionID[public.ruleRevisionID]; ok {
			view.ruleID = rule.id
			view.ruleName = rule.name
			view.module = rule.module
			view.salience = rule.salience
		}
	}
	return view
}

func (a *agenda) pendingByModule() map[ModuleName][]*activation {
	if a == nil || len(a.moduleQueues) == 0 {
		return nil
	}
	out := make(map[ModuleName][]*activation, len(a.moduleQueues))
	for module, queue := range a.moduleQueues {
		if queue == nil {
			continue
		}
		for i := 1; i < len(queue.heap); i++ {
			act := queue.heap[i]
			if act == nil || act.status != activationStatusPending {
				continue
			}
			out[module] = append(out[module], act)
		}
		if len(out[module]) == 0 {
			delete(out, module)
			continue
		}
		slices.SortStableFunc(out[module], func(left, right *activation) int {
			return a.activationCompare(left, right)
		})
	}
	return out
}

func (a *agenda) activationsInFocusOrder(pending map[ModuleName][]*activation, focusStack []ModuleName) []*activation {
	if a == nil || len(pending) == 0 {
		return nil
	}
	indexes := make(map[ModuleName]int, len(pending))
	stack := cloneModuleNames(focusStack)
	out := make([]*activation, 0, pendingActivationPointerCount(pending))
	for {
		module := currentFocusFromStack(stack)
		if module == MainModule && a.revision != nil && a.revision.allRulesInMainModule {
			nextModule, act, ok := a.nextPendingAcrossModules(pending, indexes)
			if !ok {
				return out
			}
			indexes[nextModule]++
			out = append(out, act)
			continue
		}
		module = normalizeModuleName(module)
		if module.IsZero() {
			module = MainModule
		}
		if idx := indexes[module]; idx < len(pending[module]) {
			out = append(out, pending[module][idx])
			indexes[module] = idx + 1
			continue
		}
		if len(stack) == 0 {
			return out
		}
		stack[len(stack)-1] = ""
		stack = stack[:len(stack)-1]
	}
}

func (a *agenda) nextPendingAcrossModules(pending map[ModuleName][]*activation, indexes map[ModuleName]int) (ModuleName, *activation, bool) {
	var bestModule ModuleName
	var best *activation
	for module, activations := range pending {
		idx := indexes[module]
		if idx >= len(activations) {
			continue
		}
		act := activations[idx]
		if best == nil || a.activationLess(act, best) {
			bestModule = module
			best = act
		}
	}
	return bestModule, best, best != nil
}

func currentFocusFromStack(stack []ModuleName) ModuleName {
	if len(stack) == 0 {
		return MainModule
	}
	module := stack[len(stack)-1]
	if module.IsZero() {
		return MainModule
	}
	return module
}

func pendingActivationPointerCount(pending map[ModuleName][]*activation) int {
	count := 0
	for _, activations := range pending {
		count += len(activations)
	}
	return count
}

func cloneModuleNames(modules []ModuleName) []ModuleName {
	if len(modules) == 0 {
		return nil
	}
	return append([]ModuleName(nil), modules...)
}

func cloneAgendaActivations(activations []AgendaActivation) []AgendaActivation {
	if len(activations) == 0 {
		return []AgendaActivation{}
	}
	out := make([]AgendaActivation, len(activations))
	for i, act := range activations {
		out[i] = act
		out[i].factIDs = cloneFactIDs(act.factIDs)
	}
	return out
}
