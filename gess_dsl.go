package gess

import (
	"context"
	"fmt"
	"maps"
	"strings"
)

// DSLRegistry registers host-provided actions and pure functions used by Gess
// sources.
type DSLRegistry struct {
	Actions   map[string]ActionFunc
	Calls     map[string]DSLCallFunc
	Functions []PureFunctionSpec
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

// ParseGess parses a Gess source file into a reusable document.
func ParseGess(name string, source []byte) (*GessDocument, error) {
	forms, err := parseGessSExprs(name, source)
	if err != nil {
		return nil, err
	}
	for _, form := range forms {
		if !form.isAtom() && len(form.list) > 0 {
			continue
		}
		return nil, &GessFileError{Span: form.span, Reason: "top-level form must be a non-empty list"}
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
		switch form.head() {
		case "defmodule":
			if err := l.loadModule(form); err != nil {
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
		switch form.head() {
		case "defmodule", "deftemplate":
		case "deffacts":
			initials, err := l.loadFacts(form)
			if err != nil {
				return err
			}
			l.doc.initials = append(l.doc.initials, initials...)
		case "defrule":
			if err := l.loadRule(form); err != nil {
				return err
			}
		case "defquery":
			if err := l.loadQuery(form); err != nil {
				return err
			}
		default:
			return l.err(form.span, "unsupported top-level form %q", form.head())
		}
	}
	return nil
}

func (l *gessLoader) loadModule(form gessSExpr) error {
	if len(form.list) < 2 || !form.list[1].isAtom() {
		return l.err(form.span, "defmodule requires a module name")
	}
	spec := ModuleSpec{Name: ModuleName(form.list[1].text())}
	for _, item := range form.list[2:] {
		if item.head() != "declare" {
			continue
		}
		for _, decl := range item.list[1:] {
			if decl.head() == "auto-focus" {
				value, err := l.boolArg(decl, 1)
				if err != nil {
					return err
				}
				spec.AutoFocus = &value
			}
		}
	}
	if err := l.workspace.AddModule(spec); err != nil {
		return l.wrap(form.span, "add module", err)
	}
	return nil
}

func (l *gessLoader) loadTemplate(form gessSExpr) error {
	if len(form.list) < 2 || !form.list[1].isAtom() {
		return l.err(form.span, "deftemplate requires a template name")
	}
	module, name := splitGessName(form.list[1].text())
	spec := TemplateSpec{Name: name, Module: module}
	for _, item := range form.list[2:] {
		switch item.head() {
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
			if !item.isAtom() {
				return l.err(item.span, "unsupported deftemplate item")
			}
		}
	}
	if err := l.workspace.AddTemplate(spec); err != nil {
		return l.wrap(form.span, "add template", err)
	}
	key := QualifiedName{Module: normalizeModuleName(module), Name: name}.normalized()
	l.templates[key] = spec
	return nil
}

func (l *gessLoader) applyTemplateDecls(spec *TemplateSpec, decls gessSExpr) error {
	for _, decl := range decls.list[1:] {
		switch decl.head() {
		case "backchain-reactive":
			value, err := l.boolArg(decl, 1)
			if err != nil {
				return err
			}
			spec.BackchainReactive = value
		case "duplicate-policy":
			if len(decl.list) != 2 || !decl.list[1].isAtom() {
				return l.err(decl.span, "duplicate-policy requires one value")
			}
			switch decl.list[1].text() {
			case "structural":
				spec.DuplicatePolicy = DuplicateStructural
			case "allow":
				spec.DuplicatePolicy = DuplicateAllow
			case "unique-key":
				spec.DuplicatePolicy = DuplicateUniqueKey
			default:
				return l.err(decl.list[1].span, "unsupported duplicate-policy %q", decl.list[1].text())
			}
		case "duplicate-key":
			for _, field := range decl.list[1:] {
				if !field.isAtom() {
					return l.err(field.span, "duplicate-key fields must be symbols")
				}
				spec.DuplicateKeyNames = append(spec.DuplicateKeyNames, field.text())
			}
		case "key":
			if len(decl.list) != 2 || !decl.list[1].isAtom() {
				return l.err(decl.span, "key requires one value")
			}
			spec.Key = TemplateKey(decl.list[1].text())
		}
	}
	return nil
}

func (l *gessLoader) parseSlot(form gessSExpr) (FieldSpec, error) {
	if len(form.list) < 2 || !form.list[1].isAtom() {
		return FieldSpec{}, l.err(form.span, "slot requires a field name")
	}
	field := FieldSpec{Name: form.list[1].text(), Kind: ValueAny}
	for _, attr := range form.list[2:] {
		switch attr.head() {
		case "type":
			if len(attr.list) != 2 || !attr.list[1].isAtom() {
				return FieldSpec{}, l.err(attr.span, "slot type requires one value")
			}
			field.Kind = gessValueKind(attr.list[1].text())
		case "required":
			value, err := l.boolArg(attr, 1)
			if err != nil {
				return FieldSpec{}, err
			}
			field.Required = value
		case "default":
			if len(attr.list) != 2 {
				return FieldSpec{}, l.err(attr.span, "slot default requires one value")
			}
			value, err := gessAtomValue(attr.list[1])
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
	for _, fact := range form.list[2:] {
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
	if len(form.list) < 3 || !form.list[1].isAtom() {
		return l.err(form.span, "defrule requires a rule name")
	}
	module, name := splitGessName(form.list[1].text())
	rule := RuleSpec{Name: name, Module: module}
	body, rhs, err := splitGessRuleBody(form.list[2:])
	if err != nil {
		return l.wrap(form.span, "parse rule body", err)
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
		rule.Actions = append(rule.Actions, RuleActionSpec{Name: actionName})
	}
	if err := l.workspace.AddRule(rule); err != nil {
		return l.wrap(form.span, "add rule", err)
	}
	return nil
}

func applyRuleDecls(l *gessLoader, rule *RuleSpec, body []gessSExpr) []gessSExpr {
	out := body[:0]
	for _, item := range body {
		if item.head() != "declare" {
			out = append(out, item)
			continue
		}
		for _, decl := range item.list[1:] {
			switch decl.head() {
			case "salience":
				if len(decl.list) == 2 {
					if v, ok := gessInt(decl.list[1]); ok {
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
	if len(form.list) < 3 || !form.list[1].isAtom() {
		return l.err(form.span, "defquery requires a query name")
	}
	module, name := splitGessName(form.list[1].text())
	query := QuerySpec{Name: name, Module: module}
	var body []gessSExpr
	var returns []gessSExpr
	scope := newGessScope()
	for _, item := range form.list[2:] {
		switch item.head() {
		case "declare":
			for _, decl := range item.list[1:] {
				if decl.head() != "variables" {
					continue
				}
				for _, variable := range decl.list[1:] {
					name, ok := gessVariableName(variable)
					if !ok {
						return l.err(variable.span, "query variable must be a ?name")
					}
					query.Parameters = append(query.Parameters, QueryParameterSpec{Name: name, Kind: ValueAny})
					scope.params[name] = struct{}{}
				}
			}
		case "return":
			returns = append(returns, item.list[1:]...)
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
		if len(ret.list) != 2 || !ret.list[0].isAtom() {
			return l.err(ret.span, "return item must be (alias value)")
		}
		expr, err := l.parseExpr(module, ret.list[1], scope)
		if err != nil {
			return err
		}
		query.Returns = append(query.Returns, ReturnValue(ret.list[0].text(), expr))
	}
	if err := l.workspace.AddQuery(query); err != nil {
		return l.wrap(form.span, "add query", err)
	}
	return nil
}

func (l *gessLoader) parseConditions(module ModuleName, forms []gessSExpr, scope *gessScope, query bool) (ConditionSpec, error) {
	var conditions []ConditionSpec
	for i := 0; i < len(forms); i++ {
		if name, ok := gessVariableName(forms[i]); ok && i+2 < len(forms) && forms[i+1].isAtom() && forms[i+1].text() == "<-" {
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
	switch form.head() {
	case "and":
		return l.parseConditions(module, form.list[1:], scope, query)
	case "or":
		var branches []ConditionSpec
		for _, branch := range form.list[1:] {
			branchScope := scope.clone()
			cond, err := l.parseCondition(module, branch, "", branchScope, query)
			if err != nil {
				return nil, err
			}
			branches = append(branches, cond)
		}
		return Or{Conditions: branches}, nil
	case "not":
		if len(form.list) != 2 {
			return nil, l.err(form.span, "not requires one condition")
		}
		cond, err := l.parseCondition(module, form.list[1], "", scope.clone(), query)
		if err != nil {
			return nil, err
		}
		return Not{Condition: cond}, nil
	case "exists":
		if len(form.list) != 2 {
			return nil, l.err(form.span, "exists requires one condition")
		}
		cond, err := l.parseCondition(module, form.list[1], "", scope.clone(), query)
		if err != nil {
			return nil, err
		}
		return Exists(cond), nil
	case "forall":
		if len(form.list) != 3 {
			return nil, l.err(form.span, "forall requires domain and requirement")
		}
		local := scope.clone()
		domain, err := l.parseCondition(module, form.list[1], "", local, query)
		if err != nil {
			return nil, err
		}
		requirement, err := l.parseCondition(module, form.list[2], "", local, query)
		if err != nil {
			return nil, err
		}
		return Forall(domain, requirement), nil
	case "test":
		if len(form.list) != 2 {
			return nil, l.err(form.span, "test requires one expression")
		}
		if variable, ok := gessExprUsesAggregateValue(form.list[1], scope); ok {
			return nil, l.err(form.list[1].span, "test over aggregate result ?%s is not supported by the graph runtime", variable)
		}
		expr, err := l.parseExpr(module, form.list[1], scope)
		if err != nil {
			return nil, err
		}
		return Test{Expression: expr}, nil
	case "accumulate":
		return l.parseAccumulate(module, form, scope, query)
	default:
		return l.parsePattern(module, form, binding, scope, query)
	}
}

func (l *gessLoader) parsePattern(module ModuleName, form gessSExpr, binding string, scope *gessScope, query bool) (ConditionSpec, error) {
	if form.isAtom() || len(form.list) == 0 || !form.list[0].isAtom() {
		return nil, l.err(form.span, "expected fact pattern")
	}
	if binding == "" {
		scope.generated++
		binding = fmt.Sprintf("__gess%d", scope.generated)
	}
	mod, name := splitGessName(form.head())
	if mod.IsZero() {
		mod = module
	}
	spec := RuleConditionSpec{Binding: binding, Target: TemplateFactIn(mod, name)}
	if binding != "" {
		scope.bindings[binding] = struct{}{}
	}
	for _, slot := range form.list[1:] {
		if len(slot.list) != 2 || !slot.list[0].isAtom() {
			return nil, l.err(slot.span, "slot constraint must be (field value)")
		}
		field := slot.list[0].text()
		value := slot.list[1]
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
				return nil, l.err(value.span, "variable %q needs a fact binding", value.text())
			}
			scope.vars[variable] = FieldRef{Binding: binding, Field: field}
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
	if len(form.list) < 3 {
		return nil, l.err(form.span, "accumulate requires input and aggregate bindings")
	}
	local := scope.clone()
	input, err := l.parseCondition(module, form.list[1], "", local, query)
	if err != nil {
		return nil, err
	}
	var specs []AggregateSpec
	for _, bind := range form.list[2:] {
		if bind.head() != "bind" || len(bind.list) != 3 {
			return nil, l.err(bind.span, "aggregate binding must be (bind ?name (aggregate ...))")
		}
		name, ok := gessVariableName(bind.list[1])
		if !ok {
			return nil, l.err(bind.list[1].span, "aggregate result must be a ?name")
		}
		call := bind.list[2]
		if len(call.list) == 0 || !call.list[0].isAtom() {
			return nil, l.err(call.span, "aggregate call is required")
		}
		var spec AggregateSpec
		switch call.head() {
		case "count":
			spec = Count()
		case "sum", "min", "max", "collect":
			if len(call.list) != 2 {
				return nil, l.err(call.span, "%s requires one expression", call.head())
			}
			expr, err := l.parseExpr(module, call.list[1], local)
			if err != nil {
				return nil, err
			}
			switch call.head() {
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
			return nil, l.err(call.span, "unsupported aggregate %q", call.head())
		}
		specs = append(specs, spec.As(name))
		scope.values[name] = struct{}{}
	}
	return Accumulate(input, specs...), nil
}

func (l *gessLoader) parseExpr(module ModuleName, form gessSExpr, scope *gessScope) (ExpressionSpec, error) {
	if form.isAtom() {
		if binding, field, ok := splitGessProjection(form.text()); ok {
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
			return nil, l.err(form.span, "unknown variable %q", form.text())
		}
		value, err := gessAtomValue(form)
		if err != nil {
			return nil, err
		}
		return ConstExpr{Value: value}, nil
	}
	if len(form.list) == 0 || !form.list[0].isAtom() {
		return nil, l.err(form.span, "expression requires an operator")
	}
	op := form.head()
	switch op {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		if len(form.list) != 3 {
			return nil, l.err(form.span, "comparison requires two operands")
		}
		left, err := l.parseExpr(module, form.list[1], scope)
		if err != nil {
			return nil, err
		}
		right, err := l.parseExpr(module, form.list[2], scope)
		if err != nil {
			return nil, err
		}
		return CompareExpr{Operator: gessCompareOp(op), Left: left, Right: right}, nil
	case "and", "or":
		operands := make([]ExpressionSpec, 0, len(form.list)-1)
		for _, item := range form.list[1:] {
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
		if len(form.list) != 2 {
			return nil, l.err(form.span, "not expression requires one operand")
		}
		expr, err := l.parseExpr(module, form.list[1], scope)
		if err != nil {
			return nil, err
		}
		return BooleanExpr{Operator: ExpressionBoolNot, Operands: []ExpressionSpec{expr}}, nil
	default:
		args := make([]ExpressionSpec, 0, len(form.list)-1)
		for _, item := range form.list[1:] {
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
	switch form.head() {
	case "assert", "assert-logical":
		if len(form.list) != 2 {
			return "", l.err(form.span, "%s requires one fact literal", form.head())
		}
		action, err := l.buildAssertAction(ruleName, index, module, form.head() == "assert-logical", form.list[1], scope)
		if err != nil {
			return "", err
		}
		if err := l.workspace.AddAction(action); err != nil {
			return "", l.wrap(form.span, "add generated assert action", err)
		}
		return action.Name, nil
	case "focus":
		if len(form.list) != 2 || !form.list[1].isAtom() {
			return "", l.err(form.span, "focus requires a module name")
		}
		name := l.generatedActionName(ruleName, index, "focus")
		module := ModuleName(form.list[1].text())
		return name, l.workspace.AddAction(ActionSpec{Name: name, Fn: func(ctx ActionContext) error {
			return ctx.PushFocus(module)
		}})
	case "pop-focus":
		name := l.generatedActionName(ruleName, index, "pop-focus")
		return name, l.workspace.AddAction(ActionSpec{Name: name, Fn: func(ctx ActionContext) error {
			_, err := ctx.PopFocus()
			return err
		}})
	case "clear-focus":
		name := l.generatedActionName(ruleName, index, "clear-focus")
		return name, l.workspace.AddAction(ActionSpec{Name: name, Fn: func(ctx ActionContext) error {
			return ctx.ClearFocusStack()
		}})
	case "halt":
		name := l.generatedActionName(ruleName, index, "halt")
		return name, l.workspace.AddAction(ActionSpec{Name: name, Fn: func(ctx ActionContext) error {
			return ctx.Halt()
		}})
	case "call":
		if len(form.list) < 2 || !form.list[1].isAtom() {
			return "", l.err(form.span, "call requires a registered action name")
		}
		name := form.list[1].text()
		if len(form.list) == 2 {
			if _, ok := l.registry.Actions[name]; !ok {
				return "", l.err(form.list[1].span, "unregistered action %q", name)
			}
			return name, nil
		}
		call, ok := l.registry.Calls[name]
		if !ok {
			return "", l.err(form.list[1].span, "unregistered action %q", name)
		}
		action, err := l.buildCallAction(ruleName, index, name, call, form.list[2:], scope)
		if err != nil {
			return "", err
		}
		if err := l.workspace.AddAction(action); err != nil {
			return "", l.wrap(form.span, "add generated call action", err)
		}
		return action.Name, nil
	default:
		return "", l.err(form.span, "unsupported action %q", form.head())
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

func (l *gessLoader) buildAssertAction(ruleName string, index int, module ModuleName, logical bool, fact gessSExpr, scope *gessScope) (ActionSpec, error) {
	if fact.isAtom() || len(fact.list) == 0 || !fact.list[0].isAtom() {
		return ActionSpec{}, l.err(fact.span, "assert requires a fact literal")
	}
	mod, name := splitGessName(fact.head())
	if mod.IsZero() {
		mod = module
	}
	templateKey := l.templateKey(mod, name)
	values := make([]gessRuntimeValue, 0, len(fact.list)-1)
	fields := make([]string, 0, len(fact.list)-1)
	for _, slot := range fact.list[1:] {
		if len(slot.list) != 2 || !slot.list[0].isAtom() {
			return ActionSpec{}, l.err(slot.span, "assert slot must be (field value)")
		}
		value, err := l.runtimeValue(slot.list[1], scope)
		if err != nil {
			return ActionSpec{}, err
		}
		fields = append(fields, slot.list[0].text())
		values = append(values, value)
	}
	action := ActionSpec{Name: l.generatedActionName(ruleName, index, fact.head())}
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

func (l *gessLoader) runtimeValue(form gessSExpr, scope *gessScope) (gessRuntimeValue, error) {
	if form.isAtom() {
		if binding, field, ok := splitGessProjection(form.text()); ok {
			return gessRuntimeValue{binding: binding, field: field, fieldRef: true}, nil
		}
		if variable, ok := gessVariableName(form); ok {
			if ref, ok := scope.vars[variable]; ok {
				return gessRuntimeValue{binding: ref.Binding, field: ref.Field, fieldRef: true}, nil
			}
			if _, ok := scope.values[variable]; ok {
				return gessRuntimeValue{binding: variable, bindingValue: true}, nil
			}
			return gessRuntimeValue{}, l.err(form.span, "unknown variable %q", form.text())
		}
		value, err := gessAtomValue(form)
		if err != nil {
			return gessRuntimeValue{}, err
		}
		return gessRuntimeValue{constant: value, hasConst: true}, nil
	}
	return gessRuntimeValue{}, l.err(form.span, "assert values must be scalar expressions")
}

func (l *gessLoader) parseFactLiteral(fact gessSExpr) (string, Fields, error) {
	if fact.isAtom() || len(fact.list) == 0 || !fact.list[0].isAtom() {
		return "", nil, l.err(fact.span, "fact literal must be a list")
	}
	pairs := make([]any, 0, (len(fact.list)-1)*2)
	for _, slot := range fact.list[1:] {
		if len(slot.list) != 2 || !slot.list[0].isAtom() {
			return "", nil, l.err(slot.span, "fact slot must be (field value)")
		}
		value, err := gessAtomValue(slot.list[1])
		if err != nil {
			return "", nil, err
		}
		pairs = append(pairs, slot.list[0].text(), value)
	}
	fields, err := NewFieldsFromPairs(pairs...)
	if err != nil {
		return "", nil, l.wrap(fact.span, "build fact fields", err)
	}
	return fact.head(), fields, nil
}

func (l *gessLoader) generatedActionName(ruleName string, index int, suffix string) string {
	l.ruleSeq++
	return fmt.Sprintf("__gess_%s_%d_%d_%s", ruleName, index, l.ruleSeq, sanitizeGessActionName(suffix))
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
	if len(form.list) <= index || !form.list[index].isAtom() {
		return false, l.err(form.span, "%s requires a boolean value", form.head())
	}
	switch strings.ToUpper(form.list[index].text()) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	default:
		return false, l.err(form.list[index].span, "expected TRUE or FALSE")
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
		if item.isAtom() && item.text() == "=>" {
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
	if !expr.isAtom() || !strings.HasPrefix(expr.text(), "?") {
		return "", false
	}
	text := strings.TrimPrefix(expr.text(), "?")
	if text == "" || strings.Contains(text, ":") {
		return "", false
	}
	return strings.ReplaceAll(text, "-", "_"), true
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
		return ValueKind(strings.ToLower(kind))
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
	if !expr.isAtom() {
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
	if expr.isAtom() {
		variable, ok := gessVariableName(expr)
		if !ok {
			return "", false
		}
		_, ok = scope.values[variable]
		return variable, ok
	}
	for _, child := range expr.list {
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
