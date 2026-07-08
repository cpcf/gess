package rules

import "context"

// ActionFunc is the Go implementation of a rule or query action.
type ActionFunc func(ActionContext) error

// DSLCallFunc is a host-provided action call implementation.
type DSLCallFunc func(ActionContext, []Value) error

// ActionContextHandle is the runtime implementation behind ActionContext.
type ActionContextHandle interface {
	SetRHSBind(name string, value Value)
	RHSBind(name string) (Value, bool)
	Context() context.Context
	SessionID() SessionID
	RulesetID() RulesetID
	ActivationID() ActivationID
	RuleID() RuleID
	RuleRevisionID() RuleRevisionID
	Generation() Generation
	BoundFacts() []FactSnapshot
	Binding(name string) (FactSnapshot, bool)
	BindingID(name string) (FactID, bool)
	BindingValue(name string) (Value, bool)
	BindingScalarValue(name, field string) (Value, bool)
	Global(name string) (Value, bool)
	Assert(templateKey TemplateKey, fields Fields) (AssertResult, error)
	AssertLogical(templateKey TemplateKey, fields Fields) (AssertResult, error)
	AssertTemplateValues(templateKey TemplateKey, values ...Value) error
	Modify(id FactID, patch FactPatch) (ModifyResult, error)
	Retract(id FactID) (RetractResult, error)
	Halt() error
	Emit(values ...Value) error
	PushFocus(module ModuleName) error
	SetFocus(module ModuleName) error
	PopFocus() (ModuleName, error)
	ClearFocusStack() error
}

// ActionContext is passed to an ActionFunc. It exposes the activation's
// identity and generation, read access to bound facts and values, and the
// mutation API.
type ActionContext struct {
	handle ActionContextHandle
}

// NewActionContext returns an ActionContext backed by handle.
func NewActionContext(handle ActionContextHandle) ActionContext {
	return ActionContext{handle: handle}
}

// ActionContextHandleOf returns the engine-owned implementation behind c.
func ActionContextHandleOf(c ActionContext) ActionContextHandle {
	return c.handle
}

func (c ActionContext) SetRHSBind(name string, value Value) {
	if c.handle == nil {
		return
	}
	c.handle.SetRHSBind(name, value)
}

func (c ActionContext) RHSBind(name string) (Value, bool) {
	if c.handle == nil {
		return Value{}, false
	}
	return c.handle.RHSBind(name)
}

func (c ActionContext) Context() context.Context {
	if c.handle == nil {
		return context.Background()
	}
	return c.handle.Context()
}

func (c ActionContext) SessionID() SessionID {
	if c.handle == nil {
		return ""
	}
	return c.handle.SessionID()
}

func (c ActionContext) RulesetID() RulesetID {
	if c.handle == nil {
		return ""
	}
	return c.handle.RulesetID()
}

func (c ActionContext) ActivationID() ActivationID {
	if c.handle == nil {
		return ""
	}
	return c.handle.ActivationID()
}

func (c ActionContext) RuleID() RuleID {
	if c.handle == nil {
		return ""
	}
	return c.handle.RuleID()
}

func (c ActionContext) RuleRevisionID() RuleRevisionID {
	if c.handle == nil {
		return ""
	}
	return c.handle.RuleRevisionID()
}

func (c ActionContext) Generation() Generation {
	if c.handle == nil {
		return 0
	}
	return c.handle.Generation()
}

func (c ActionContext) BoundFacts() []FactSnapshot {
	if c.handle == nil {
		return nil
	}
	return CloneFactSnapshots(c.handle.BoundFacts())
}

func (c ActionContext) Binding(name string) (FactSnapshot, bool) {
	if c.handle == nil {
		return FactSnapshot{}, false
	}
	fact, ok := c.handle.Binding(name)
	return CloneFactSnapshot(fact), ok
}

func (c ActionContext) BindingID(name string) (FactID, bool) {
	if c.handle == nil {
		return FactID{}, false
	}
	return c.handle.BindingID(name)
}

func (c ActionContext) BindingValue(name string) (Value, bool) {
	if c.handle == nil {
		return Value{}, false
	}
	value, ok := c.handle.BindingValue(name)
	if !ok {
		return Value{}, false
	}
	return CloneValue(value), true
}

func (c ActionContext) BindingScalarValue(name, field string) (Value, bool) {
	if c.handle == nil {
		return Value{}, false
	}
	value, ok := c.handle.BindingScalarValue(name, field)
	if !ok {
		return Value{}, false
	}
	return CloneValue(value), true
}

func (c ActionContext) Global(name string) (Value, bool) {
	if c.handle == nil {
		return Value{}, false
	}
	value, ok := c.handle.Global(name)
	if !ok {
		return Value{}, false
	}
	return CloneValue(value), true
}

func (c ActionContext) Assert(templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if c.handle == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	result, err := c.handle.Assert(templateKey, fields)
	return CloneAssertResult(result), err
}

func (c ActionContext) AssertLogical(templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if c.handle == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	result, err := c.handle.AssertLogical(templateKey, fields)
	return CloneAssertResult(result), err
}

func (c ActionContext) AssertTemplateValues(templateKey TemplateKey, values ...Value) error {
	if c.handle == nil {
		return ErrClosedSession
	}
	return c.handle.AssertTemplateValues(templateKey, values...)
}

func (c ActionContext) Modify(id FactID, patch FactPatch) (ModifyResult, error) {
	if c.handle == nil {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}
	result, err := c.handle.Modify(id, patch)
	return CloneModifyResult(result), err
}

func (c ActionContext) Retract(id FactID) (RetractResult, error) {
	if c.handle == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}
	result, err := c.handle.Retract(id)
	return CloneRetractResult(result), err
}

func (c ActionContext) Halt() error {
	if c.handle == nil {
		return ErrClosedSession
	}
	return c.handle.Halt()
}

func (c ActionContext) Emit(values ...Value) error {
	if c.handle == nil {
		return ErrClosedSession
	}
	return c.handle.Emit(values...)
}

func (c ActionContext) PushFocus(module ModuleName) error {
	if c.handle == nil {
		return ErrClosedSession
	}
	return c.handle.PushFocus(module)
}

func (c ActionContext) SetFocus(module ModuleName) error {
	if c.handle == nil {
		return ErrClosedSession
	}
	return c.handle.SetFocus(module)
}

func (c ActionContext) PopFocus() (ModuleName, error) {
	if c.handle == nil {
		return MainModule, ErrClosedSession
	}
	return c.handle.PopFocus()
}

func (c ActionContext) ClearFocusStack() error {
	if c.handle == nil {
		return ErrClosedSession
	}
	return c.handle.ClearFocusStack()
}
