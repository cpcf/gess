package gess

import (
	"context"
	"fmt"
	"strings"
)

type ActionFunc func(ActionContext) error

type actionContextBinding struct {
	name string
	fact FactSnapshot
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
	bindings       []actionContextBinding
}

func newActionContext(ctx context.Context, session *Session, activation activation, bindings []actionContextBinding) ActionContext {
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
		bindings:       bindings,
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
	if len(c.bindings) == 0 {
		return nil
	}
	out := make([]FactSnapshot, len(c.bindings))
	for i, binding := range c.bindings {
		out[i] = binding.fact
	}
	return out
}

func (c ActionContext) Binding(name string) (FactSnapshot, bool) {
	if name == "" {
		return FactSnapshot{}, false
	}
	for _, binding := range c.bindings {
		if binding.name != name {
			continue
		}
		return binding.fact, true
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
	return c.session.modifyWithContextAndOrigin(c.Context(), id, patch, c.mutationOrigin())
}

func (c ActionContext) Retract(id FactID) (RetractResult, error) {
	if c.session == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
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

func (s *Session) executeActivationActions(ctx context.Context, runID RunID, activation activation) error {
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

	bindings := make([]actionContextBinding, 0, len(activation.bindings))
	for _, entry := range activation.bindings {
		fact, ok := s.factsByID[entry.factID]
		if !ok {
			return ActionContext{}, fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, entry.factID, activation.id)
		}
		if fact.generation != activation.generation || fact.version != entry.factVersion {
			return ActionContext{}, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, activation.id)
		}
		snapshot := fact.detachedSnapshot()
		bindings = append(bindings, actionContextBinding{
			name: entry.binding,
			fact: snapshot,
		})
	}

	return newActionContext(ctx, s, activation, bindings), nil
}
