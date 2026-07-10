package rules

import "context"

// WorkspaceHandle is the engine-owned implementation behind a public
// Workspace.
type WorkspaceHandle interface {
	AddModule(ModuleSpec) error
	AddTemplate(TemplateSpec) error
	AddGlobal(GlobalSpec) error
	ReplaceGlobal(GlobalSpec) error
	RemoveGlobal(string) error
	AddAction(ActionSpec) error
	ReplaceAction(ActionSpec) error
	RemoveAction(string) error
	AddFunction(PureFunctionSpec) error
	AddExpressionFunction(ExpressionFunctionSpec) error
	ReplaceFunction(PureFunctionSpec) error
	RemoveFunction(string) error
	AddRule(RuleSpec) error
	ReplaceRule(RuleSpec) error
	RemoveRule(string) error
	AddQuery(QuerySpec) error
	ReplaceQuery(QuerySpec) error
	RemoveQuery(string) error
	Compile(context.Context) (*Ruleset, error)
}

// Workspace is a mutable collection of module, template, action, function,
// rule, and query definitions. Definitions are validated as they're added;
// Compile produces an immutable Ruleset.
type Workspace struct {
	handle WorkspaceHandle
}

// NewWorkspaceWithHandle returns a Workspace backed by handle. Runtime
// packages use this constructor to make workspace ownership explicit while
// keeping rules independent of a concrete engine implementation.
func NewWorkspaceWithHandle(handle WorkspaceHandle) *Workspace {
	if handle == nil {
		return &Workspace{}
	}
	return &Workspace{handle: handle}
}

// WorkspaceHandleOf returns the engine-owned implementation behind w.
func WorkspaceHandleOf(w *Workspace) WorkspaceHandle {
	if w == nil {
		return nil
	}
	return w.handle
}

func (w *Workspace) requireHandle() (WorkspaceHandle, error) {
	if w == nil || w.handle == nil {
		return nil, ErrInvalidRuleset
	}
	return w.handle, nil
}

// AddModule records a module declaration. Compile treats identical repeated
// declarations as idempotent and rejects conflicting metadata.
func (w *Workspace) AddModule(spec ModuleSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddModule(spec)
}

func (w *Workspace) AddTemplate(spec TemplateSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddTemplate(spec)
}

// AddGlobal declares a typed global constant readable from rule, query, and
// aggregate expressions and from ActionContext.Global. Values bind per session
// via WithGlobals; a declaration without a default requires every session to
// supply a value. In .gess sources the equivalent form is
// (defglobal *name* (type KIND) (default value)).
func (w *Workspace) AddGlobal(spec GlobalSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddGlobal(spec)
}

func (w *Workspace) ReplaceGlobal(spec GlobalSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.ReplaceGlobal(spec)
}

func (w *Workspace) RemoveGlobal(name string) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.RemoveGlobal(name)
}

func (w *Workspace) AddAction(spec ActionSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddAction(spec)
}

func (w *Workspace) ReplaceAction(spec ActionSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.ReplaceAction(spec)
}

func (w *Workspace) RemoveAction(name string) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.RemoveAction(name)
}

func (w *Workspace) AddFunction(spec PureFunctionSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddFunction(spec)
}

// AddExpressionFunction registers a pure function whose body is a single
// expression over its declared parameters.
func (w *Workspace) AddExpressionFunction(spec ExpressionFunctionSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddExpressionFunction(spec)
}

func (w *Workspace) ReplaceFunction(spec PureFunctionSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.ReplaceFunction(spec)
}

func (w *Workspace) RemoveFunction(name string) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.RemoveFunction(name)
}

func (w *Workspace) AddRule(spec RuleSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddRule(spec)
}

func (w *Workspace) ReplaceRule(spec RuleSpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.ReplaceRule(spec)
}

func (w *Workspace) RemoveRule(name string) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.RemoveRule(name)
}

func (w *Workspace) AddQuery(spec QuerySpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.AddQuery(spec)
}

func (w *Workspace) ReplaceQuery(spec QuerySpec) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.ReplaceQuery(spec)
}

func (w *Workspace) RemoveQuery(name string) error {
	handle, err := w.requireHandle()
	if err != nil {
		return err
	}
	return handle.RemoveQuery(name)
}

// Compile compiles workspace into an immutable Ruleset.
func (w *Workspace) Compile(ctx context.Context) (*Ruleset, error) {
	handle, err := w.requireHandle()
	if err != nil {
		return nil, err
	}
	return handle.Compile(ctx)
}
