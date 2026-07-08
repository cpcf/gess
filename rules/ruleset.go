package rules

// RulesetHandle is the engine-owned implementation behind a public Ruleset.
// Implementations must return detached inspection values.
type RulesetHandle interface {
	ID() RulesetID
	Module(ModuleName) (Module, bool)
	Modules() []Module
	Template(string) (Template, bool)
	TemplateByKey(TemplateKey) (Template, bool)
	Templates() []Template
	Action(string) (Action, bool)
	Actions() []Action
	Function(string) (PureFunctionDefinition, bool)
	Functions() []PureFunctionDefinition
	Global(string) (Global, bool)
	Globals() []Global
	Rule(string) (Rule, bool)
	RuleByID(RuleID) (Rule, bool)
	RuleByRevisionID(RuleRevisionID) (Rule, bool)
	Rules() []Rule
	Query(string) (Query, bool)
	Queries() []Query
	GeneratedFactObservability(TemplateKey) (GeneratedFactObservability, bool)
	GeneratedFactObservabilityDiagnostics() []GeneratedFactObservability
}

// Ruleset is an immutable compiled ruleset facade. Its implementation is owned
// by the engine through a RulesetHandle.
type Ruleset struct {
	handle RulesetHandle
}

// NewRuleset returns a public Ruleset backed by handle.
func NewRuleset(handle RulesetHandle) *Ruleset {
	if handle == nil {
		return nil
	}
	return &Ruleset{handle: handle}
}

// RulesetHandleOf returns the engine-owned implementation behind r.
func RulesetHandleOf(r *Ruleset) RulesetHandle {
	if r == nil {
		return nil
	}
	return r.handle
}

func (r *Ruleset) ID() RulesetID {
	if r == nil || r.handle == nil {
		return ""
	}
	return r.handle.ID()
}

func (r *Ruleset) Module(name ModuleName) (Module, bool) {
	if r == nil || r.handle == nil {
		return Module{}, false
	}
	module, ok := r.handle.Module(name)
	return CloneModule(module), ok
}

func (r *Ruleset) Modules() []Module {
	if r == nil || r.handle == nil {
		return nil
	}
	modules := r.handle.Modules()
	out := make([]Module, len(modules))
	for i, module := range modules {
		out[i] = CloneModule(module)
	}
	return out
}

func (r *Ruleset) Template(name string) (Template, bool) {
	if r == nil || r.handle == nil {
		return Template{}, false
	}
	template, ok := r.handle.Template(name)
	return CloneTemplate(template), ok
}

func (r *Ruleset) TemplateByKey(key TemplateKey) (Template, bool) {
	if r == nil || r.handle == nil {
		return Template{}, false
	}
	template, ok := r.handle.TemplateByKey(key)
	return CloneTemplate(template), ok
}

func (r *Ruleset) Templates() []Template {
	if r == nil || r.handle == nil {
		return nil
	}
	return CloneTemplates(r.handle.Templates())
}

func (r *Ruleset) Action(name string) (Action, bool) {
	if r == nil || r.handle == nil {
		return Action{}, false
	}
	action, ok := r.handle.Action(name)
	return CloneAction(action), ok
}

func (r *Ruleset) Actions() []Action {
	if r == nil || r.handle == nil {
		return nil
	}
	actions := r.handle.Actions()
	out := make([]Action, len(actions))
	for i, action := range actions {
		out[i] = CloneAction(action)
	}
	return out
}

func (r *Ruleset) Function(name string) (PureFunctionDefinition, bool) {
	if r == nil || r.handle == nil {
		return PureFunctionDefinition{}, false
	}
	function, ok := r.handle.Function(name)
	return ClonePureFunctionDefinition(function), ok
}

func (r *Ruleset) Functions() []PureFunctionDefinition {
	if r == nil || r.handle == nil {
		return nil
	}
	functions := r.handle.Functions()
	out := make([]PureFunctionDefinition, len(functions))
	for i, function := range functions {
		out[i] = ClonePureFunctionDefinition(function)
	}
	return out
}

func (r *Ruleset) Global(name string) (Global, bool) {
	if r == nil || r.handle == nil {
		return Global{}, false
	}
	global, ok := r.handle.Global(name)
	return CloneGlobal(global), ok
}

func (r *Ruleset) Globals() []Global {
	if r == nil || r.handle == nil {
		return nil
	}
	globals := r.handle.Globals()
	out := make([]Global, len(globals))
	for i, global := range globals {
		out[i] = CloneGlobal(global)
	}
	return out
}

func (r *Ruleset) Rule(name string) (Rule, bool) {
	if r == nil || r.handle == nil {
		return Rule{}, false
	}
	rule, ok := r.handle.Rule(name)
	return CloneRule(rule), ok
}

func (r *Ruleset) RuleByID(id RuleID) (Rule, bool) {
	if r == nil || r.handle == nil {
		return Rule{}, false
	}
	rule, ok := r.handle.RuleByID(id)
	return CloneRule(rule), ok
}

func (r *Ruleset) RuleByRevisionID(id RuleRevisionID) (Rule, bool) {
	if r == nil || r.handle == nil {
		return Rule{}, false
	}
	rule, ok := r.handle.RuleByRevisionID(id)
	return CloneRule(rule), ok
}

func (r *Ruleset) Rules() []Rule {
	if r == nil || r.handle == nil {
		return nil
	}
	return CloneRules(r.handle.Rules())
}

func (r *Ruleset) Query(name string) (Query, bool) {
	if r == nil || r.handle == nil {
		return Query{}, false
	}
	query, ok := r.handle.Query(name)
	return CloneQuery(query), ok
}

func (r *Ruleset) Queries() []Query {
	if r == nil || r.handle == nil {
		return nil
	}
	queries := r.handle.Queries()
	out := make([]Query, len(queries))
	for i, query := range queries {
		out[i] = CloneQuery(query)
	}
	return out
}

// GeneratedFactObservability returns the compiler visibility proof for a
// generated template.
func (r *Ruleset) GeneratedFactObservability(templateKey TemplateKey) (GeneratedFactObservability, bool) {
	if r == nil || r.handle == nil {
		return GeneratedFactObservability{}, false
	}
	diagnostic, ok := r.handle.GeneratedFactObservability(templateKey)
	return CloneGeneratedFactObservability(diagnostic), ok
}

// GeneratedFactObservabilityDiagnostics returns compiler visibility proofs for
// all generated template insert plans.
func (r *Ruleset) GeneratedFactObservabilityDiagnostics() []GeneratedFactObservability {
	if r == nil || r.handle == nil {
		return nil
	}
	return CloneGeneratedFactObservabilityDiagnostics(r.handle.GeneratedFactObservabilityDiagnostics())
}
