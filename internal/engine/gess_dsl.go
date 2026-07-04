package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// DSLRegistry registers host-provided actions and pure functions used by Gess
// sources.
type DSLRegistry struct {
	Actions   map[string]ActionFunc
	Calls     map[string]DSLCallFunc
	Functions []PureFunctionSpec
}

// DSLMissingRegistrations lists host registrations referenced by a .gess source
// but absent from a Registry.
type DSLMissingRegistrations struct {
	Calls   []string
	Actions []string
}

// DSLCallFunc is a host-provided action that receives positional arguments
// evaluated from a .gess (call name arg...) form.
type DSLCallFunc func(ActionContext, []Value) error

// GessDocument is a parsed Gess source file.
type GessDocument struct {
	name     string
	forms    []gessSExpr
	initials []SessionInitialFact
}

// Name returns the source name supplied to ParseGess.
func (d *GessDocument) Name() string {
	if d == nil {
		return ""
	}
	return d.name
}

// InitialFacts returns facts declared by deffacts after LoadGess has lowered the
// document. Pass them to WithInitialFacts when creating a session.
func (d *GessDocument) InitialFacts() []SessionInitialFact {
	if d == nil {
		return nil
	}
	return cloneSessionInitialFacts(d.initials)
}

// MissingRegistrations returns host action and call registrations referenced by
// the parsed source but absent from registry. Expression function references are
// validated during load/compile because the loader needs resolved definition
// context to distinguish function calls from DSL syntax.
func (d *GessDocument) MissingRegistrations(registry DSLRegistry) DSLMissingRegistrations {
	if d == nil {
		return DSLMissingRegistrations{}
	}
	missingCalls := make(map[string]struct{})
	missingActions := make(map[string]struct{})
	for _, form := range d.forms {
		collectMissingGessCalls(form, registry, missingCalls, missingActions)
	}
	return DSLMissingRegistrations{
		Calls:   sortedStringKeys(missingCalls),
		Actions: sortedStringKeys(missingActions),
	}
}

func collectMissingGessCalls(form gessSExpr, registry DSLRegistry, missingCalls, missingActions map[string]struct{}) {
	if form.IsAtom() || len(form.List) == 0 {
		return
	}
	if form.Head() == "call" && len(form.List) >= 2 && form.List[1].IsAtom() {
		name := form.List[1].Text()
		if len(form.List) == 2 {
			if _, ok := registry.Actions[name]; !ok {
				missingActions[name] = struct{}{}
			}
		} else if _, ok := registry.Calls[name]; !ok {
			missingCalls[name] = struct{}{}
		}
	}
	for _, child := range form.List {
		collectMissingGessCalls(child, registry, missingCalls, missingActions)
	}
}

func sortedStringKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

// ParseGess parses a Gess source file into a reusable document.
func ParseGess(name string, source []byte) (*GessDocument, error) {
	forms, err := parseGessSExprs(name, source)
	if err != nil {
		return nil, err
	}
	for _, form := range forms {
		if !form.IsAtom() && len(form.List) > 0 {
			continue
		}
		return nil, &GessFileError{Span: form.Span, Reason: "top-level form must be a non-empty list"}
	}
	return &GessDocument{name: name, forms: forms}, nil
}

// LoadGess lowers a parsed Gess document into a Workspace.
func LoadGess(ctx context.Context, workspace *Workspace, doc *GessDocument, registry DSLRegistry) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if workspace == nil {
		return &GessFileError{Reason: "workspace is required"}
	}
	if doc == nil {
		return &GessFileError{Reason: "document is required"}
	}
	loader := newGessLoader(workspace, doc, registry)
	return loader.load(ctx)
}

// CompileGess parses and compiles a Gess source file into a Ruleset.
func CompileGess(ctx context.Context, name string, source []byte, registry DSLRegistry) (*Ruleset, error) {
	doc, err := ParseGess(name, source)
	if err != nil {
		return nil, err
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, registry); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

type gessLoader struct {
	workspace *Workspace
	doc       *GessDocument
	registry  DSLRegistry
	templates map[QualifiedName]TemplateSpec
	ruleSeq   int
}

func newGessLoader(workspace *Workspace, doc *GessDocument, registry DSLRegistry) *gessLoader {
	return &gessLoader{
		workspace: workspace,
		doc:       doc,
		registry:  registry,
		templates: make(map[QualifiedName]TemplateSpec),
	}
}

func (l *gessLoader) load(ctx context.Context) error {
	for name, fn := range l.registry.Actions {
		if err := l.workspace.AddAction(ActionSpec{Name: name, Fn: fn}); err != nil {
			return l.wrap(SourceSpan{Name: l.doc.name}, "add registered action", err)
		}
	}
	for _, fn := range l.registry.Functions {
		if err := l.workspace.AddFunction(fn); err != nil {
			return l.wrap(SourceSpan{Name: l.doc.name}, "add registered function", err)
		}
	}
	for _, form := range l.doc.forms {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch form.Head() {
		case "defmodule":
			if err := l.loadModule(form); err != nil {
				return err
			}
		case "defglobal":
			if err := l.loadGlobal(form); err != nil {
				return err
			}
		case "deftemplate":
			if err := l.loadTemplate(form); err != nil {
				return err
			}
		}
	}
	for _, form := range l.doc.forms {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch form.Head() {
		case "defmodule", "defglobal", "deftemplate":
		case "deffacts":
			initials, err := l.loadFacts(form)
			if err != nil {
				return err
			}
			l.doc.initials = append(l.doc.initials, initials...)
		case "deffunction":
			if err := l.loadExpressionFunction(form); err != nil {
				return err
			}
		case "defrule":
			if err := l.loadRule(form); err != nil {
				return err
			}
		case "defquery":
			if err := l.loadQuery(form); err != nil {
				return err
			}
		default:
			return l.err(form.Span, "unsupported top-level form %q", form.Head())
		}
	}
	return nil
}

func (l *gessLoader) loadExpressionFunction(form gessSExpr) error {
	if len(form.List) < 3 || !form.List[1].IsAtom() {
		return l.err(form.Span, "deffunction requires a function name")
	}
	spec := ExpressionFunctionSpec{Name: form.List[1].Text(), Return: valueKindUnknown}
	scope := newGessScope()
	var body *gessSExpr
	for _, item := range form.List[2:] {
		switch item.Head() {
		case "param":
			param, err := l.parseExpressionFunctionParam(item)
			if err != nil {
				return err
			}
			spec.Params = append(spec.Params, param)
			scope.params[param.Name] = struct{}{}
		case "params":
			for _, paramForm := range item.List[1:] {
				param, err := l.parseExpressionFunctionParam(paramForm)
				if err != nil {
					return err
				}
				spec.Params = append(spec.Params, param)
				scope.params[param.Name] = struct{}{}
			}
		case "return", "type":
			if len(item.List) != 2 || !item.List[1].IsAtom() {
				return l.err(item.Span, "%s requires one value kind", item.Head())
			}
			spec.Return = gessValueKind(item.List[1].Text())
		case "description":
			if len(item.List) != 2 || !item.List[1].IsAtom() {
				return l.err(item.Span, "function description requires one value")
			}
			spec.Description = item.List[1].Text()
		default:
			if body != nil {
				return l.err(item.Span, "deffunction requires exactly one expression body")
			}
			current := item
			body = &current
		}
	}
	if spec.Return == valueKindUnknown {
		return l.err(form.Span, "deffunction requires a return kind")
	}
	if body == nil {
		return l.err(form.Span, "deffunction requires an expression body")
	}
	expr, err := l.parseExpr("", *body, scope)
	if err != nil {
		return err
	}
	spec.Expression = expr
	if err := l.workspace.AddExpressionFunction(spec); err != nil {
		return l.wrap(form.Span, "add function", err)
	}
	return nil
}

func (l *gessLoader) parseExpressionFunctionParam(form gessSExpr) (ExpressionFunctionParamSpec, error) {
	if len(form.List) != 2 && len(form.List) != 3 {
		return ExpressionFunctionParamSpec{}, l.err(form.Span, "function parameter must be (param ?name KIND) or (?name KIND)")
	}
	offset := 0
	if form.Head() == "param" {
		offset = 1
	}
	if len(form.List) != offset+2 || !form.List[offset].IsAtom() || !form.List[offset+1].IsAtom() {
		return ExpressionFunctionParamSpec{}, l.err(form.Span, "function parameter requires a name and value kind")
	}
	name, ok := gessFunctionParamName(form.List[offset])
	if !ok {
		return ExpressionFunctionParamSpec{}, l.err(form.List[offset].Span, "function parameter name must be a symbol or ?name")
	}
	return ExpressionFunctionParamSpec{Name: name, Kind: gessValueKind(form.List[offset+1].Text())}, nil
}

func (l *gessLoader) loadModule(form gessSExpr) error {
	if len(form.List) < 2 || !form.List[1].IsAtom() {
		return l.err(form.Span, "defmodule requires a module name")
	}
	spec := ModuleSpec{Name: ModuleName(form.List[1].Text())}
	for _, item := range form.List[2:] {
		if item.Head() != "declare" {
			continue
		}
		for _, decl := range item.List[1:] {
			if decl.Head() == "auto-focus" {
				value, err := l.boolArg(decl, 1)
				if err != nil {
					return err
				}
				spec.AutoFocus = &value
			}
		}
	}
	if err := l.workspace.AddModule(spec); err != nil {
		return l.wrap(form.Span, "add module", err)
	}
	return nil
}

func (l *gessLoader) loadGlobal(form gessSExpr) error {
	if len(form.List) < 2 || !form.List[1].IsAtom() {
		return l.err(form.Span, "defglobal requires a global name")
	}
	name, ok := gessGlobalName(form.List[1])
	if !ok {
		return l.err(form.List[1].Span, "global name must use *name* syntax")
	}
	spec := GlobalSpec{Name: name, Kind: ValueAny}
	for _, attr := range form.List[2:] {
		switch attr.Head() {
		case "type":
			if len(attr.List) != 2 || !attr.List[1].IsAtom() {
				return l.err(attr.Span, "global type requires one value")
			}
			spec.Kind = gessValueKind(attr.List[1].Text())
		case "default":
			if len(attr.List) != 2 {
				return l.err(attr.Span, "global default requires one value")
			}
			value, err := gessAtomValue(attr.List[1])
			if err != nil {
				return err
			}
			spec.Default = value
			spec.HasDefault = true
		case "description":
			if len(attr.List) != 2 || !attr.List[1].IsAtom() {
				return l.err(attr.Span, "global description requires one value")
			}
			spec.Description = attr.List[1].Text()
		}
	}
	if err := l.workspace.AddGlobal(spec); err != nil {
		return l.wrap(form.Span, "add global", err)
	}
	return nil
}

func (l *gessLoader) loadTemplate(form gessSExpr) error {
	if len(form.List) < 2 || !form.List[1].IsAtom() {
		return l.err(form.Span, "deftemplate requires a template name")
	}
	module, name := splitGessName(form.List[1].Text())
	spec := TemplateSpec{Name: name, Module: module, DuplicatePolicy: DuplicateAllow, Source: form.Span, GessSource: gessExprSource(form)}
	for _, item := range form.List[2:] {
		switch item.Head() {
		case "declare":
			if err := l.applyTemplateDecls(&spec, item); err != nil {
				return err
			}
		case "slot":
			field, err := l.parseSlot(item)
			if err != nil {
				return err
			}
			spec.Fields = append(spec.Fields, field)
		case "":
			if !item.IsAtom() {
				return l.err(item.Span, "unsupported deftemplate item")
			}
		}
	}
	if err := l.workspace.AddTemplate(spec); err != nil {
		return l.wrap(form.Span, "add template", err)
	}
	key := QualifiedName{Module: normalizeModuleName(module), Name: name}.normalized()
	l.templates[key] = spec
	return nil
}

func (l *gessLoader) applyTemplateDecls(spec *TemplateSpec, decls gessSExpr) error {
	for _, decl := range decls.List[1:] {
		switch decl.Head() {
		case "backchain-reactive":
			value, err := l.boolArg(decl, 1)
			if err != nil {
				return err
			}
			spec.BackchainReactive = value
		case "duplicate-policy":
			if len(decl.List) != 2 || !decl.List[1].IsAtom() {
				return l.err(decl.Span, "duplicate-policy requires one value")
			}
			switch decl.List[1].Text() {
			case "structural":
				spec.DuplicatePolicy = DuplicateStructural
			case "allow":
				spec.DuplicatePolicy = DuplicateAllow
			case "unique-key":
				spec.DuplicatePolicy = DuplicateUniqueKey
			default:
				return l.err(decl.List[1].Span, "unsupported duplicate-policy %q", decl.List[1].Text())
			}
		case "duplicate-key":
			for _, field := range decl.List[1:] {
				if !field.IsAtom() {
					return l.err(field.Span, "duplicate-key fields must be symbols")
				}
				spec.DuplicateKeyNames = append(spec.DuplicateKeyNames, field.Text())
			}
		case "key":
			if len(decl.List) != 2 || !decl.List[1].IsAtom() {
				return l.err(decl.Span, "key requires one value")
			}
			spec.Key = TemplateKey(decl.List[1].Text())
		}
	}
	return nil
}

func (l *gessLoader) parseSlot(form gessSExpr) (FieldSpec, error) {
	if len(form.List) < 2 || !form.List[1].IsAtom() {
		return FieldSpec{}, l.err(form.Span, "slot requires a field name")
	}
	field := FieldSpec{Name: form.List[1].Text(), Kind: ValueAny}
	for _, attr := range form.List[2:] {
		switch attr.Head() {
		case "type":
			if len(attr.List) != 2 || !attr.List[1].IsAtom() {
				return FieldSpec{}, l.err(attr.Span, "slot type requires one value")
			}
			field.Kind = gessValueKind(attr.List[1].Text())
		case "required":
			value, err := l.boolArg(attr, 1)
			if err != nil {
				return FieldSpec{}, err
			}
			field.Required = value
		case "default":
			if len(attr.List) != 2 {
				return FieldSpec{}, l.err(attr.Span, "slot default requires one value")
			}
			value, err := gessAtomValue(attr.List[1])
			if err != nil {
				return FieldSpec{}, err
			}
			field.Default = value
			field.HasDefault = true
		}
	}
	return field, nil
}

func (l *gessLoader) loadFacts(form gessSExpr) ([]SessionInitialFact, error) {
	var out []SessionInitialFact
	for _, fact := range form.List[2:] {
		name, fields, err := l.parseFactLiteral(fact)
		if err != nil {
			return nil, err
		}
		module, factName := splitGessName(name)
		key := l.templateKey(module, factName)
		initial := SessionInitialFact{Name: name, Fields: fields}
		if key != "" {
			initial = SessionInitialFact{TemplateKey: key, Fields: fields}
		}
		out = append(out, initial)
	}
	return out, nil
}

func (l *gessLoader) loadRule(form gessSExpr) error {
	if len(form.List) < 3 || !form.List[1].IsAtom() {
		return l.err(form.Span, "defrule requires a rule name")
	}
	module, name := splitGessName(form.List[1].Text())
	rule := RuleSpec{Name: name, Module: module, Source: form.Span, GessSource: gessExprSource(form)}
	body, rhs, err := splitGessRuleBody(form.List[2:])
	if err != nil {
		return l.wrap(form.Span, "parse rule body", err)
	}
	body = applyRuleDecls(l, &rule, body)
	scope := newGessScope()
	condition, err := l.parseConditions(module, body, scope, false)
	if err != nil {
		return err
	}
	rule.ConditionTree = condition
	for i, action := range rhs {
		actionName, err := l.loadRuleAction(rule.Name, i, module, action, scope)
		if err != nil {
			return err
		}
		rule.Actions = append(rule.Actions, RuleActionSpec{Name: actionName, Source: action.Span})
	}
	if err := l.workspace.AddRule(rule); err != nil {
		return l.wrap(form.Span, "add rule", err)
	}
	return nil
}

func applyRuleDecls(l *gessLoader, rule *RuleSpec, body []gessSExpr) []gessSExpr {
	out := body[:0]
	for _, item := range body {
		if item.Head() != "declare" {
			out = append(out, item)
			continue
		}
		for _, decl := range item.List[1:] {
			switch decl.Head() {
			case "salience":
				if len(decl.List) == 2 {
					if v, ok := gessInt(decl.List[1]); ok {
						rule.Salience = int(v)
					}
				}
			case "auto-focus":
				value, err := l.boolArg(decl, 1)
				if err == nil {
					rule.AutoFocus = &value
				}
			}
		}
	}
	return out
}

func (l *gessLoader) loadQuery(form gessSExpr) error {
	if len(form.List) < 3 || !form.List[1].IsAtom() {
		return l.err(form.Span, "defquery requires a query name")
	}
	module, name := splitGessName(form.List[1].Text())
	query := QuerySpec{Name: name, Module: module, Source: form.Span, GessSource: gessExprSource(form)}
	var body []gessSExpr
	var returns []gessSExpr
	scope := newGessScope()
	for _, item := range form.List[2:] {
		switch item.Head() {
		case "declare":
			for _, decl := range item.List[1:] {
				if decl.Head() != "variables" {
					continue
				}
				for _, variable := range decl.List[1:] {
					name, ok := gessVariableName(variable)
					if !ok {
						return l.err(variable.Span, "query variable must be a ?name")
					}
					query.Parameters = append(query.Parameters, QueryParameterSpec{Name: name, Kind: ValueAny})
					scope.params[name] = struct{}{}
				}
			}
		case "return":
			returns = append(returns, item.List[1:]...)
		default:
			body = append(body, item)
		}
	}
	condition, err := l.parseConditions(module, body, scope, true)
	if err != nil {
		return err
	}
	query.ConditionTree = condition
	for _, ret := range returns {
		if len(ret.List) != 2 || !ret.List[0].IsAtom() {
			return l.err(ret.Span, "return item must be (alias value)")
		}
		expr, err := l.parseExpr(module, ret.List[1], scope)
		if err != nil {
			return err
		}
		query.Returns = append(query.Returns, QueryReturnSpec{Alias: ret.List[0].Text(), Expression: expr, Source: ret.Span})
	}
	if err := l.workspace.AddQuery(query); err != nil {
		return l.wrap(form.Span, "add query", err)
	}
	return nil
}

func (l *gessLoader) parseConditions(module ModuleName, forms []gessSExpr, scope *gessScope, query bool) (ConditionSpec, error) {
	var conditions []ConditionSpec
	for i := 0; i < len(forms); i++ {
		if name, ok := gessVariableName(forms[i]); ok && i+2 < len(forms) && forms[i+1].IsAtom() && forms[i+1].Text() == "<-" {
			cond, err := l.parsePattern(module, forms[i+2], name, scope, query)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, cond)
			i += 2
			continue
		}
		cond, err := l.parseCondition(module, forms[i], "", scope, query)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, cond)
	}
	if len(conditions) == 1 {
		return conditions[0], nil
	}
	return And{Conditions: conditions}, nil
}

func (l *gessLoader) parseCondition(module ModuleName, form gessSExpr, binding string, scope *gessScope, query bool) (ConditionSpec, error) {
	switch form.Head() {
	case "and":
		var conditions []ConditionSpec
		for _, child := range form.List[1:] {
			cond, err := l.parseCondition(module, child, "", scope, query)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, cond)
		}
		return And{Conditions: conditions, Source: form.Span}, nil
	case "or":
		var branches []ConditionSpec
		for _, branch := range form.List[1:] {
			branchScope := scope.clone()
			cond, err := l.parseCondition(module, branch, "", branchScope, query)
			if err != nil {
				return nil, err
			}
			branches = append(branches, cond)
		}
		return Or{Conditions: branches, Source: form.Span}, nil
	case "not":
		if len(form.List) != 2 {
			return nil, l.err(form.Span, "not requires one condition")
		}
		cond, err := l.parseCondition(module, form.List[1], "", scope.clone(), query)
		if err != nil {
			return nil, err
		}
		return Not{Condition: cond, Source: form.Span}, nil
	case "exists":
		if len(form.List) != 2 {
			return nil, l.err(form.Span, "exists requires one condition")
		}
		cond, err := l.parseCondition(module, form.List[1], "", scope.clone(), query)
		if err != nil {
			return nil, err
		}
		return ExistsCondition{Condition: cloneConditionSpec(cond), Source: form.Span}, nil
	case "forall":
		if len(form.List) != 3 {
			return nil, l.err(form.Span, "forall requires domain and requirement")
		}
		local := scope.clone()
		domain, err := l.parseCondition(module, form.List[1], "", local, query)
		if err != nil {
			return nil, err
		}
		requirement, err := l.parseCondition(module, form.List[2], "", local, query)
		if err != nil {
			return nil, err
		}
		return ForallCondition{Domain: cloneConditionSpec(domain), Requirement: cloneConditionSpec(requirement), Source: form.Span}, nil
	case "test":
		if len(form.List) != 2 {
			return nil, l.err(form.Span, "test requires one expression")
		}
		if variable, ok := gessExprUsesAggregateValue(form.List[1], scope); ok {
			return nil, l.err(form.List[1].Span, "test over aggregate result ?%s is not supported by the graph runtime", variable)
		}
		expr, err := l.parseExpr(module, form.List[1], scope)
		if err != nil {
			return nil, err
		}
		return Test{Expression: expr, Source: form.Span}, nil
	case "accumulate":
		return l.parseAccumulate(module, form, scope, query)
	default:
		return l.parsePattern(module, form, binding, scope, query)
	}
}

func (l *gessLoader) parsePattern(module ModuleName, form gessSExpr, binding string, scope *gessScope, query bool) (ConditionSpec, error) {
	if form.IsAtom() || len(form.List) == 0 || !form.List[0].IsAtom() {
		return nil, l.err(form.Span, "expected fact pattern")
	}
	if binding == "" {
		scope.generated++
		binding = fmt.Sprintf("__gess%d", scope.generated)
	}
	mod, name := splitGessName(form.Head())
	if mod.IsZero() {
		mod = module
	}
	spec := RuleConditionSpec{Binding: binding, Target: TemplateFactIn(mod, name), Source: form.Span}
	if binding != "" {
		scope.bindings[binding] = struct{}{}
	}
	for _, slot := range form.List[1:] {
		if len(slot.List) != 2 || !slot.List[0].IsAtom() {
			return nil, l.err(slot.Span, "slot constraint must be (field value)")
		}
		field := slot.List[0].Text()
		value := slot.List[1]
		if variable, ok := gessVariableName(value); ok {
			if _, isParam := scope.params[variable]; isParam && query {
				spec.Predicates = append(spec.Predicates, CompareExpr{
					Operator: ExpressionCompareEqual,
					Left:     CurrentFieldExpr{Field: field},
					Right:    ParamExpr{Name: variable},
				})
				continue
			}
			if ref, ok := scope.vars[variable]; ok {
				spec.JoinConstraints = append(spec.JoinConstraints, JoinConstraintSpec{
					Field: field, Operator: FieldConstraintEqual, Ref: ref,
				})
				continue
			}
			if binding == "" {
				return nil, l.err(value.Span, "variable %q needs a fact binding", value.Text())
			}
			scope.vars[variable] = FieldRef{Binding: binding, Field: field}
			continue
		}
		if global, ok := gessGlobalName(value); ok {
			spec.Predicates = append(spec.Predicates, CompareExpr{
				Operator: ExpressionCompareEqual,
				Left:     CurrentFieldExpr{Field: field},
				Right:    GlobalExpr{Name: global},
			})
			continue
		}
		scalar, err := gessAtomValue(value)
		if err != nil {
			return nil, err
		}
		spec.FieldConstraints = append(spec.FieldConstraints, FieldConstraintSpec{
			Field: field, Operator: FieldConstraintEqual, Value: scalar,
		})
	}
	return Match(spec), nil
}

func (l *gessLoader) parseAccumulate(module ModuleName, form gessSExpr, scope *gessScope, query bool) (ConditionSpec, error) {
	if len(form.List) < 3 {
		return nil, l.err(form.Span, "accumulate requires input and aggregate bindings")
	}
	local := scope.clone()
	input, err := l.parseCondition(module, form.List[1], "", local, query)
	if err != nil {
		return nil, err
	}
	var specs []AggregateSpec
	for _, bind := range form.List[2:] {
		if bind.Head() != "bind" || len(bind.List) != 3 {
			return nil, l.err(bind.Span, "aggregate binding must be (bind ?name (aggregate ...))")
		}
		name, ok := gessVariableName(bind.List[1])
		if !ok {
			return nil, l.err(bind.List[1].Span, "aggregate result must be a ?name")
		}
		call := bind.List[2]
		if len(call.List) == 0 || !call.List[0].IsAtom() {
			return nil, l.err(call.Span, "aggregate call is required")
		}
		var spec AggregateSpec
		switch call.Head() {
		case "count":
			spec = Count()
		case "sum", "min", "max", "collect":
			if len(call.List) != 2 {
				return nil, l.err(call.Span, "%s requires one expression", call.Head())
			}
			expr, err := l.parseExpr(module, call.List[1], local)
			if err != nil {
				return nil, err
			}
			switch call.Head() {
			case "sum":
				spec = Sum(expr)
			case "min":
				spec = Min(expr)
			case "max":
				spec = Max(expr)
			case "collect":
				spec = Collect(expr)
			}
		default:
			return nil, l.err(call.Span, "unsupported aggregate %q", call.Head())
		}
		specs = append(specs, spec.As(name))
		scope.values[name] = struct{}{}
	}
	return AccumulateCondition{Input: cloneConditionSpec(input), Specs: specs, Source: form.Span}, nil
}

func (l *gessLoader) parseExpr(module ModuleName, form gessSExpr, scope *gessScope) (ExpressionSpec, error) {
	if form.IsAtom() {
		if global, ok := gessGlobalName(form); ok {
			return GlobalExpr{Name: global}, nil
		}
		if binding, field, ok := splitGessProjection(form.Text()); ok {
			return BindingFieldExpr{Binding: binding, Field: field}, nil
		}
		if variable, ok := gessVariableName(form); ok {
			if ref, ok := scope.vars[variable]; ok {
				return BindingFieldExpr{Binding: ref.Binding, Field: ref.Field, Path: ref.Path}, nil
			}
			if _, ok := scope.values[variable]; ok {
				return BindingValueExpr{Binding: variable}, nil
			}
			if _, ok := scope.params[variable]; ok {
				return ParamExpr{Name: variable}, nil
			}
			return nil, l.err(form.Span, "unknown variable %q", form.Text())
		}
		value, err := gessAtomValue(form)
		if err != nil {
			return nil, err
		}
		return ConstExpr{Value: value}, nil
	}
	if len(form.List) == 0 || !form.List[0].IsAtom() {
		return nil, l.err(form.Span, "expression requires an operator")
	}
	op := form.Head()
	switch op {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		if len(form.List) != 3 {
			return nil, l.err(form.Span, "comparison requires two operands")
		}
		left, err := l.parseExpr(module, form.List[1], scope)
		if err != nil {
			return nil, err
		}
		right, err := l.parseExpr(module, form.List[2], scope)
		if err != nil {
			return nil, err
		}
		return CompareExpr{Operator: gessCompareOp(op), Left: left, Right: right}, nil
	case "and", "or":
		operands := make([]ExpressionSpec, 0, len(form.List)-1)
		for _, item := range form.List[1:] {
			expr, err := l.parseExpr(module, item, scope)
			if err != nil {
				return nil, err
			}
			operands = append(operands, expr)
		}
		operator := ExpressionBoolAnd
		if op == "or" {
			operator = ExpressionBoolOr
		}
		return BooleanExpr{Operator: operator, Operands: operands}, nil
	case "not":
		if len(form.List) != 2 {
			return nil, l.err(form.Span, "not expression requires one operand")
		}
		expr, err := l.parseExpr(module, form.List[1], scope)
		if err != nil {
			return nil, err
		}
		return BooleanExpr{Operator: ExpressionBoolNot, Operands: []ExpressionSpec{expr}}, nil
	default:
		args := make([]ExpressionSpec, 0, len(form.List)-1)
		for _, item := range form.List[1:] {
			expr, err := l.parseExpr(module, item, scope)
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
		}
		return Call(op, args...), nil
	}
}

func (l *gessLoader) loadRuleAction(ruleName string, index int, module ModuleName, form gessSExpr, scope *gessScope) (string, error) {
	switch form.Head() {
	case "assert", "assert-logical":
		if len(form.List) != 2 {
			return "", l.err(form.Span, "%s requires one fact literal", form.Head())
		}
		action, err := l.buildAssertAction(ruleName, index, module, form.Head() == "assert-logical", form.List[1], scope)
		if err != nil {
			return "", err
		}
		action.GessSource = gessExprSource(form)
		if err := l.workspace.AddAction(action); err != nil {
			return "", l.wrap(form.Span, "add generated assert action", err)
		}
		return action.Name, nil
	case "focus":
		if len(form.List) != 2 || !form.List[1].IsAtom() {
			return "", l.err(form.Span, "focus requires a module name")
		}
		name := l.generatedActionName(ruleName, index, "focus")
		module := ModuleName(form.List[1].Text())
		return name, l.workspace.AddAction(ActionSpec{Name: name, GessSource: gessExprSource(form), Fn: func(ctx ActionContext) error {
			return ctx.PushFocus(module)
		}})
	case "pop-focus":
		name := l.generatedActionName(ruleName, index, "pop-focus")
		return name, l.workspace.AddAction(ActionSpec{Name: name, GessSource: gessExprSource(form), Fn: func(ctx ActionContext) error {
			_, err := ctx.PopFocus()
			return err
		}})
	case "clear-focus":
		name := l.generatedActionName(ruleName, index, "clear-focus")
		return name, l.workspace.AddAction(ActionSpec{Name: name, GessSource: gessExprSource(form), Fn: func(ctx ActionContext) error {
			return ctx.ClearFocusStack()
		}})
	case "halt":
		name := l.generatedActionName(ruleName, index, "halt")
		return name, l.workspace.AddAction(ActionSpec{Name: name, GessSource: gessExprSource(form), Fn: func(ctx ActionContext) error {
			return ctx.Halt()
		}})
	case "call":
		if len(form.List) < 2 || !form.List[1].IsAtom() {
			return "", l.err(form.Span, "call requires a registered action name")
		}
		name := form.List[1].Text()
		if len(form.List) == 2 {
			if _, ok := l.registry.Actions[name]; !ok {
				return "", l.err(form.List[1].Span, "unregistered action %q", name)
			}
			return name, nil
		}
		call, ok := l.registry.Calls[name]
		if !ok {
			return "", l.err(form.List[1].Span, "unregistered action %q", name)
		}
		action, err := l.buildCallAction(ruleName, index, name, call, form.List[2:], scope)
		if err != nil {
			return "", err
		}
		if err := l.workspace.AddAction(action); err != nil {
			return "", l.wrap(form.Span, "add generated call action", err)
		}
		return action.Name, nil
	default:
		return "", l.err(form.Span, "unsupported action %q", form.Head())
	}
}

func (l *gessLoader) buildCallAction(ruleName string, index int, callName string, call DSLCallFunc, args []gessSExpr, scope *gessScope) (ActionSpec, error) {
	values := make([]gessRuntimeValue, 0, len(args))
	for _, arg := range args {
		value, err := l.runtimeValue(arg, scope)
		if err != nil {
			return ActionSpec{}, err
		}
		values = append(values, value)
	}
	action := ActionSpec{Name: l.generatedActionName(ruleName, index, "call_"+callName)}
	action.GessSource = gessCallSource(callName, values)
	var reads []ActionBindingReadSpec
	for _, value := range values {
		if value.fieldRef {
			reads = append(reads, ActionBindingReadSpec{Binding: value.binding, Field: value.field})
		}
	}
	if len(reads) > 0 {
		action.BindingReads = &ActionBindingReadSetSpec{Reads: reads}
	}
	action.Fn = func(ctx ActionContext) error {
		args := make([]Value, 0, len(values))
		for _, value := range values {
			raw, err := value.value(ctx)
			if err != nil {
				return err
			}
			normalized, err := NewValue(raw)
			if err != nil {
				return err
			}
			args = append(args, normalized)
		}
		return call(ctx, args)
	}
	return action, nil
}

func gessCallSource(callName string, values []gessRuntimeValue) string {
	var b strings.Builder
	b.WriteString("(call ")
	b.WriteString(callName)
	for _, value := range values {
		b.WriteByte(' ')
		b.WriteString(gessRuntimeValueSource(value))
	}
	b.WriteByte(')')
	return b.String()
}

func gessRuntimeValueSource(value gessRuntimeValue) string {
	switch {
	case value.hasConst:
		normalized, err := NewValue(value.constant)
		if err != nil {
			return strconv.Quote(fmt.Sprint(value.constant))
		}
		return renderGessValue(normalized)
	case value.fieldRef:
		return "?" + value.binding + ":" + value.field
	case value.bindingValue:
		return "?" + value.binding
	default:
		return "NULL"
	}
}

func (l *gessLoader) buildAssertAction(ruleName string, index int, module ModuleName, logical bool, fact gessSExpr, scope *gessScope) (ActionSpec, error) {
	if fact.IsAtom() || len(fact.List) == 0 || !fact.List[0].IsAtom() {
		return ActionSpec{}, l.err(fact.Span, "assert requires a fact literal")
	}
	mod, name := splitGessName(fact.Head())
	if mod.IsZero() {
		mod = module
	}
	templateKey := l.templateKey(mod, name)
	values := make([]gessRuntimeValue, 0, len(fact.List)-1)
	fields := make([]string, 0, len(fact.List)-1)
	for _, slot := range fact.List[1:] {
		if len(slot.List) != 2 || !slot.List[0].IsAtom() {
			return ActionSpec{}, l.err(slot.Span, "assert slot must be (field value)")
		}
		value, err := l.runtimeValue(slot.List[1], scope)
		if err != nil {
			return ActionSpec{}, err
		}
		fields = append(fields, slot.List[0].Text())
		values = append(values, value)
	}
	action := ActionSpec{Name: l.generatedActionName(ruleName, index, fact.Head())}
	var reads []ActionBindingReadSpec
	for _, value := range values {
		if value.fieldRef {
			reads = append(reads, ActionBindingReadSpec{Binding: value.binding, Field: value.field})
		}
	}
	if len(reads) > 0 {
		action.BindingReads = &ActionBindingReadSetSpec{Reads: reads}
	}
	if !logical && templateKey != "" {
		templateSpec, ok := l.templates[QualifiedName{Module: normalizeModuleName(mod), Name: name}.normalized()]
		if ok {
			if native, ok := gessNativeAssertTemplateValues(templateKey, templateSpec, fields, values); ok {
				action.BindingReads = nil
				action.AssertTemplateValues = native
				return action, nil
			}
		}
	}
	action.Fn = func(ctx ActionContext) error {
		pairs := make([]any, 0, len(fields)*2)
		for i, field := range fields {
			value, err := values[i].value(ctx)
			if err != nil {
				return err
			}
			pairs = append(pairs, field, value)
		}
		f, err := NewFieldsFromPairs(pairs...)
		if err != nil {
			return err
		}
		if logical {
			if templateKey != "" {
				if ctx.session == nil {
					return ErrClosedSession
				}
				if ctx.RuleRevisionID().IsZero() || ctx.ActivationID().IsZero() {
					return ErrLogicalSupportUnavailable
				}
				if err := ctx.materializeAllBindings(); err != nil {
					return err
				}
				_, err = ctx.session.insertLogicalFactWithContextAndOrigin(ctx.Context(), "", templateKey, f, ctx.mutationOrigin(), ctx.supportingFactIDs())
				return err
			}
			_, err = ctx.AssertLogical(qualifiedGessName(mod, name), f)
			return err
		}
		if templateKey != "" {
			_, err = ctx.AssertTemplate(templateKey, f)
			return err
		}
		_, err = ctx.Assert(qualifiedGessName(mod, name), f)
		return err
	}
	return action, nil
}

func gessNativeAssertTemplateValues(templateKey TemplateKey, template TemplateSpec, fields []string, values []gessRuntimeValue) (*AssertTemplateValuesActionSpec, bool) {
	if len(fields) != len(template.Fields) || len(values) != len(template.Fields) {
		return nil, false
	}
	byField := make(map[string]gessRuntimeValue, len(fields))
	for i, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, false
		}
		if _, exists := byField[field]; exists {
			return nil, false
		}
		byField[field] = values[i]
	}
	compiledFields := make([]FieldSpec, len(template.Fields))
	copy(compiledFields, template.Fields)
	slices.SortFunc(compiledFields, func(a, b FieldSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	out := &AssertTemplateValuesActionSpec{
		TemplateKey: templateKey,
		Values:      make([]ExpressionSpec, len(values)),
	}
	for i, field := range compiledFields {
		value, ok := byField[field.Name]
		if !ok {
			return nil, false
		}
		expression, ok := gessRuntimeValueExpression(value)
		if !ok {
			return nil, false
		}
		out.Values[i] = expression
	}
	return out, true
}

func gessRuntimeValueExpression(value gessRuntimeValue) (ExpressionSpec, bool) {
	switch {
	case value.hasConst:
		return ConstExpr{Value: value.constant}, true
	case value.fieldRef:
		return BindingFieldExpr{Binding: value.binding, Field: value.field}, true
	case value.bindingValue:
		return BindingValueExpr{Binding: value.binding}, true
	default:
		return nil, false
	}
}

func (l *gessLoader) runtimeValue(form gessSExpr, scope *gessScope) (gessRuntimeValue, error) {
	if form.IsAtom() {
		if binding, field, ok := splitGessProjection(form.Text()); ok {
			return gessRuntimeValue{binding: binding, field: field, fieldRef: true}, nil
		}
		if variable, ok := gessVariableName(form); ok {
			if ref, ok := scope.vars[variable]; ok {
				return gessRuntimeValue{binding: ref.Binding, field: ref.Field, fieldRef: true}, nil
			}
			if _, ok := scope.values[variable]; ok {
				return gessRuntimeValue{binding: variable, bindingValue: true}, nil
			}
			return gessRuntimeValue{}, l.err(form.Span, "unknown variable %q", form.Text())
		}
		value, err := gessAtomValue(form)
		if err != nil {
			return gessRuntimeValue{}, err
		}
		return gessRuntimeValue{constant: value, hasConst: true}, nil
	}
	return gessRuntimeValue{}, l.err(form.Span, "assert values must be scalar expressions")
}

func (l *gessLoader) parseFactLiteral(fact gessSExpr) (string, Fields, error) {
	if fact.IsAtom() || len(fact.List) == 0 || !fact.List[0].IsAtom() {
		return "", nil, l.err(fact.Span, "fact literal must be a list")
	}
	pairs := make([]any, 0, (len(fact.List)-1)*2)
	for _, slot := range fact.List[1:] {
		if len(slot.List) != 2 || !slot.List[0].IsAtom() {
			return "", nil, l.err(slot.Span, "fact slot must be (field value)")
		}
		value, err := gessAtomValue(slot.List[1])
		if err != nil {
			return "", nil, err
		}
		pairs = append(pairs, slot.List[0].Text(), value)
	}
	fields, err := NewFieldsFromPairs(pairs...)
	if err != nil {
		return "", nil, l.wrap(fact.Span, "build fact fields", err)
	}
	return fact.Head(), fields, nil
}

func (l *gessLoader) generatedActionName(ruleName string, index int, suffix string) string {
	l.ruleSeq++
	return fmt.Sprintf("__gess_%s_%d_%d_%s", ruleName, index, l.ruleSeq, sanitizeGessActionName(suffix))
}

func gessExprSource(expr gessSExpr) string {
	if expr.IsAtom() {
		if expr.String {
			return strconv.Quote(expr.Text())
		}
		return expr.Text()
	}
	var b strings.Builder
	b.WriteByte('(')
	for i, child := range expr.List {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(gessExprSource(child))
	}
	b.WriteByte(')')
	return b.String()
}

func (l *gessLoader) templateKey(module ModuleName, name string) TemplateKey {
	if l == nil {
		return ""
	}
	spec, ok := l.templates[QualifiedName{Module: normalizeModuleName(module), Name: name}.normalized()]
	if !ok {
		return ""
	}
	key := TemplateKey(strings.TrimSpace(string(spec.Key)))
	if key == "" {
		key = TemplateKey(strings.TrimSpace(spec.Name))
	}
	return key
}

func (l *gessLoader) boolArg(form gessSExpr, index int) (bool, error) {
	if len(form.List) <= index || !form.List[index].IsAtom() {
		return false, l.err(form.Span, "%s requires a boolean value", form.Head())
	}
	switch strings.ToUpper(form.List[index].Text()) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	default:
		return false, l.err(form.List[index].Span, "expected TRUE or FALSE")
	}
}

func (l *gessLoader) err(span SourceSpan, format string, args ...any) error {
	return &GessFileError{Span: span, Reason: fmt.Sprintf(format, args...)}
}

func (l *gessLoader) wrap(span SourceSpan, reason string, err error) error {
	if err == nil {
		return nil
	}
	return &GessFileError{Span: span, Reason: reason, Err: err}
}

type gessScope struct {
	vars      map[string]FieldRef
	values    map[string]struct{}
	params    map[string]struct{}
	bindings  map[string]struct{}
	generated int
}

func newGessScope() *gessScope {
	return &gessScope{
		vars:     make(map[string]FieldRef),
		values:   make(map[string]struct{}),
		params:   make(map[string]struct{}),
		bindings: make(map[string]struct{}),
	}
}

func (s *gessScope) clone() *gessScope {
	out := newGessScope()
	out.generated = s.generated
	maps.Copy(out.vars, s.vars)
	for k := range s.values {
		out.values[k] = struct{}{}
	}
	for k := range s.params {
		out.params[k] = struct{}{}
	}
	for k := range s.bindings {
		out.bindings[k] = struct{}{}
	}
	return out
}

type gessRuntimeValue struct {
	constant     any
	hasConst     bool
	binding      string
	field        string
	fieldRef     bool
	bindingValue bool
}

func (v gessRuntimeValue) value(ctx ActionContext) (any, error) {
	switch {
	case v.hasConst:
		return v.constant, nil
	case v.fieldRef:
		value, ok := ctx.BindingScalarValue(v.binding, v.field)
		if !ok {
			return nil, fmt.Errorf("gess: Gess action missing binding field %s.%s", v.binding, v.field)
		}
		return value, nil
	case v.bindingValue:
		value, ok := ctx.BindingValue(v.binding)
		if !ok {
			return nil, fmt.Errorf("gess: Gess action missing binding value %s", v.binding)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("gess: Gess action value is not configured")
	}
}

func splitGessRuleBody(items []gessSExpr) ([]gessSExpr, []gessSExpr, error) {
	for i, item := range items {
		if item.IsAtom() && item.Text() == "=>" {
			return items[:i], items[i+1:], nil
		}
	}
	return nil, nil, fmt.Errorf("missing =>")
}

func splitGessName(name string) (ModuleName, string) {
	parts := strings.SplitN(strings.TrimSpace(name), "::", 2)
	if len(parts) == 2 {
		return ModuleName(parts[0]), parts[1]
	}
	return "", name
}

func qualifiedGessName(module ModuleName, name string) string {
	module = normalizeModuleName(module)
	if module == MainModule {
		return name
	}
	return module.String() + "::" + name
}

func gessVariableName(expr gessSExpr) (string, bool) {
	if !expr.IsAtom() || !strings.HasPrefix(expr.Text(), "?") {
		return "", false
	}
	text := strings.TrimPrefix(expr.Text(), "?")
	if text == "" || strings.Contains(text, ":") {
		return "", false
	}
	return strings.ReplaceAll(text, "-", "_"), true
}

func gessFunctionParamName(expr gessSExpr) (string, bool) {
	if !expr.IsAtom() {
		return "", false
	}
	if name, ok := gessVariableName(expr); ok {
		return name, true
	}
	text := strings.TrimSpace(expr.Text())
	if text == "" || strings.Contains(text, ":") || strings.Contains(text, "*") || strings.HasPrefix(text, "?") {
		return "", false
	}
	return strings.ReplaceAll(text, "-", "_"), true
}

func gessGlobalName(expr gessSExpr) (string, bool) {
	if !expr.IsAtom() {
		return "", false
	}
	text := strings.TrimSpace(expr.Text())
	if len(text) < 3 || !strings.HasPrefix(text, "*") || !strings.HasSuffix(text, "*") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "*"), "*"))
	if name == "" || strings.Contains(name, ":") || strings.Contains(name, "*") {
		return "", false
	}
	return strings.ReplaceAll(name, "-", "_"), true
}

func splitGessProjection(text string) (string, string, bool) {
	if !strings.HasPrefix(text, "?") {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "?"), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return strings.ReplaceAll(parts[0], "-", "_"), parts[1], true
}

func gessValueKind(kind string) ValueKind {
	switch strings.ToUpper(kind) {
	case "STRING":
		return ValueString
	case "INTEGER", "INT":
		return ValueInt
	case "FLOAT", "NUMBER":
		return ValueFloat
	case "BOOLEAN", "BOOL":
		return ValueBool
	case "LIST":
		return ValueList
	case "MAP":
		return ValueMap
	case "ANY":
		return ValueAny
	default:
		return parseValueKind(kind)
	}
}

func gessCompareOp(op string) ExpressionComparisonOperator {
	switch op {
	case "=":
		return ExpressionCompareEqual
	case "!=", "<>":
		return ExpressionCompareNotEqual
	case "<":
		return ExpressionCompareLessThan
	case "<=":
		return ExpressionCompareLessOrEqual
	case ">":
		return ExpressionCompareGreaterThan
	case ">=":
		return ExpressionCompareGreaterOrEqual
	default:
		return ExpressionCompareUnknown
	}
}

func gessInt(expr gessSExpr) (int64, bool) {
	if !expr.IsAtom() {
		return 0, false
	}
	value, err := gessAtomValue(expr)
	if err != nil {
		return 0, false
	}
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}

func gessExprUsesAggregateValue(expr gessSExpr, scope *gessScope) (string, bool) {
	if scope == nil {
		return "", false
	}
	if expr.IsAtom() {
		variable, ok := gessVariableName(expr)
		if !ok {
			return "", false
		}
		_, ok = scope.values[variable]
		return variable, ok
	}
	for _, child := range expr.List {
		if variable, ok := gessExprUsesAggregateValue(child, scope); ok {
			return variable, true
		}
	}
	return "", false
}

func sanitizeGessActionName(value string) string {
	value = strings.ReplaceAll(value, "::", "_")
	value = strings.ReplaceAll(value, "-", "_")
	if value == "" {
		return "action"
	}
	return value
}
