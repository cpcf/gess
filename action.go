package gess

import (
	"context"
	"strings"
)

type ActionFunc func(ActionContext) error

type ActionContext struct {
	ctx context.Context
}

func newActionContext(ctx context.Context) ActionContext {
	return ActionContext{ctx: ctx}
}

func (c ActionContext) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
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
