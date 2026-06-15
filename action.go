package gess

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type ActionFunc func(ActionContext) error

const inlineActionContextBindingSnapshots = 2

type actionContextBindingState struct {
	mu              sync.Mutex
	entries         []bindingTupleEntry
	snapshots       []FactSnapshot
	inlineSnapshots [inlineActionContextBindingSnapshots]FactSnapshot
}

type ActionContext struct {
	ctx            context.Context
	session        *Session
	sessionID      SessionID
	rulesetID      RulesetID
	activationID   ActivationID
	ruleID         RuleID
	ruleRevisionID RuleRevisionID
	generation     Generation
	bindings       *actionContextBindingState
}

func newActionContext(ctx context.Context, session *Session, activation activation) ActionContext {
	if ctx == nil {
		ctx = context.Background()
	}

	out := ActionContext{
		ctx:            ctx,
		session:        session,
		activationID:   activation.id,
		ruleID:         activation.ruleID,
		ruleRevisionID: activation.ruleRevisionID,
		generation:     activation.generation,
	}
	if len(activation.bindings) > 0 {
		out.bindings = &actionContextBindingState{
			entries: activation.bindings,
		}
	}
	if session != nil {
		out.sessionID = session.id
		if session.revision != nil {
			out.rulesetID = session.revision.ID()
		}
	}
	return out
}

func (c ActionContext) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func (c ActionContext) SessionID() SessionID {
	return c.sessionID
}

func (c ActionContext) RulesetID() RulesetID {
	return c.rulesetID
}

func (c ActionContext) ActivationID() ActivationID {
	return c.activationID
}

func (c ActionContext) RuleID() RuleID {
	return c.ruleID
}

func (c ActionContext) RuleRevisionID() RuleRevisionID {
	return c.ruleRevisionID
}

func (c ActionContext) Generation() Generation {
	return c.generation
}

func (c ActionContext) BoundFacts() []FactSnapshot {
	if c.bindings == nil || len(c.bindings.entries) == 0 {
		return nil
	}
	if err := c.materializeAllBindings(); err != nil {
		return nil
	}
	out := make([]FactSnapshot, len(c.bindings.entries))
	copy(out, c.bindings.snapshots)
	return out
}

func (c ActionContext) Binding(name string) (FactSnapshot, bool) {
	if name == "" || c.bindings == nil {
		return FactSnapshot{}, false
	}
	for i, binding := range c.bindings.entries {
		if binding.binding != name {
			continue
		}
		return c.materializeBinding(i)
	}
	return FactSnapshot{}, false
}

func (c ActionContext) Assert(name string, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	return c.session.insertFactWithContextAndOrigin(c.Context(), name, "", fields, c.mutationOrigin())
}

func (c ActionContext) AssertTemplate(templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	return c.session.insertFactWithContextAndOrigin(c.Context(), "", templateKey, fields, c.mutationOrigin())
}

func (c ActionContext) Modify(id FactID, patch FactPatch) (ModifyResult, error) {
	if c.session == nil {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}
	if err := c.materializeAllBindings(); err != nil {
		return ModifyResult{Status: ModifyMissing}, err
	}
	return c.session.modifyWithContextAndOrigin(c.Context(), id, patch, c.mutationOrigin())
}

func (c ActionContext) Retract(id FactID) (RetractResult, error) {
	if c.session == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}
	if err := c.materializeAllBindings(); err != nil {
		return RetractResult{Status: RetractMissing}, err
	}
	return c.session.retractWithContextAndOrigin(c.Context(), id, c.mutationOrigin())
}

func (c ActionContext) mutationOrigin() mutationOrigin {
	return mutationOrigin{
		ActivationID:   c.activationID,
		RuleID:         c.ruleID,
		RuleRevisionID: c.ruleRevisionID,
	}
}

func (c ActionContext) materializeAllBindings() error {
	if c.bindings == nil || len(c.bindings.entries) == 0 {
		return nil
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	for i, entry := range c.bindings.entries {
		if _, ok := c.materializeBindingLocked(i); !ok {
			return fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, c.activationID)
		}
	}
	return nil
}

func (c ActionContext) materializeBinding(index int) (FactSnapshot, bool) {
	if c.bindings == nil || index < 0 || index >= len(c.bindings.entries) {
		return FactSnapshot{}, false
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	return c.materializeBindingLocked(index)
}

func (c ActionContext) materializeBindingLocked(index int) (FactSnapshot, bool) {
	if len(c.bindings.snapshots) == 0 {
		if len(c.bindings.entries) <= len(c.bindings.inlineSnapshots) {
			c.bindings.snapshots = c.bindings.inlineSnapshots[:len(c.bindings.entries)]
		} else {
			c.bindings.snapshots = make([]FactSnapshot, len(c.bindings.entries))
		}
	}
	if snapshot := c.bindings.snapshots[index]; !snapshot.id.IsZero() {
		return snapshot, true
	}
	if c.session == nil {
		return FactSnapshot{}, false
	}
	entry := c.bindings.entries[index]
	fact, ok := c.session.factsByID[entry.factID]
	if !ok {
		return FactSnapshot{}, false
	}
	if fact.generation != c.generation || fact.version != entry.factVersion {
		return FactSnapshot{}, false
	}
	snapshot := fact.detachedSnapshot()
	c.bindings.snapshots[index] = snapshot
	return snapshot, true
}

type ActionSpec struct {
	Name string
	Fn   ActionFunc
}

func (s ActionSpec) clone() ActionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	return out
}

func normalizeActionSpec(spec ActionSpec) (ActionSpec, error) {
	normalized := spec.clone()
	if normalized.Name == "" {
		return ActionSpec{}, &ValidationError{
			Reason: "action name is required",
		}
	}
	if normalized.Fn == nil {
		return ActionSpec{}, &ValidationError{
			Reason: "action function is required",
		}
	}
	return normalized, nil
}

type Action struct {
	name  string
	order int
}

func (a Action) Name() string {
	return a.name
}

func (a Action) DeclarationOrder() int {
	return a.order
}

func (a Action) clone() Action {
	return a
}

type compiledAction struct {
	name  string
	fn    ActionFunc
	order int
}

func compileActionSpec(spec ActionSpec, order int) (compiledAction, error) {
	normalized, err := normalizeActionSpec(spec)
	if err != nil {
		return compiledAction{}, err
	}

	return compiledAction{
		name:  normalized.Name,
		fn:    normalized.Fn,
		order: order,
	}, nil
}

func (a compiledAction) inspect() Action {
	return Action{
		name:  a.name,
		order: a.order,
	}
}

func (a compiledAction) clone() compiledAction {
	return a
}

func (s *Session) executeActivationActions(ctx context.Context, runID RunID, activation activation) (err error) {
	if s == nil {
		return ErrClosedSession
	}
	if s.closed {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.revision == nil {
		return ErrInvalidRuleset
	}

	rule, ok := s.revision.rulesByRevisionID[activation.ruleRevisionID]
	if !ok {
		return fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, activation.ruleRevisionID)
	}
	if rule.id != activation.ruleID {
		return fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, activation.ruleRevisionID)
	}

	actionCtx, err := s.actionContextForActivation(ctx, activation)
	if err != nil {
		return err
	}
	defer func() {
		if freezeErr := actionCtx.materializeAllBindings(); err == nil && freezeErr != nil {
			err = freezeErr
		}
	}()

	for _, actionSpec := range rule.actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		action, ok := s.revision.actions[actionSpec.name]
		if !ok {
			return fmt.Errorf("%w: missing action %q", ErrInvalidRuleset, actionSpec.name)
		}
		if err := action.fn(actionCtx); err != nil {
			return &ActionFailureError{
				RunID:          runID,
				RuleID:         activation.ruleID,
				RuleRevisionID: activation.ruleRevisionID,
				ActivationID:   activation.id,
				ActionName:     actionSpec.name,
				ActionIndex:    actionSpec.order,
				Err:            err,
			}
		}
	}

	return nil
}

func (s *Session) actionContextForActivation(ctx context.Context, activation activation) (ActionContext, error) {
	if s == nil {
		return ActionContext{}, ErrClosedSession
	}
	if s.closed {
		return ActionContext{}, ErrClosedSession
	}
	if s.revision == nil {
		return ActionContext{}, ErrInvalidRuleset
	}

	for _, entry := range activation.bindings {
		fact, ok := s.factsByID[entry.factID]
		if !ok {
			return ActionContext{}, fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, entry.factID, activation.id)
		}
		if fact.generation != activation.generation || fact.version != entry.factVersion {
			return ActionContext{}, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, activation.id)
		}
	}

	return newActionContext(ctx, s, activation), nil
}
