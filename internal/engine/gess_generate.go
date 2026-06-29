package engine

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"
)

// GessSourceFile is one source file passed to the Go generator.
type GessSourceFile struct {
	Name   string
	Source []byte
}

// GessGoGeneratorOptions controls generated Go source shape.
type GessGoGeneratorOptions struct {
	PackageName  string
	FunctionName string
}

// GenerateGessGo emits Go source that builds the same Workspace as the given
// .gess files without parsing those files at application startup.
func GenerateGessGo(ctx context.Context, sources []GessSourceFile, opts GessGoGeneratorOptions) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(sources) == 0 {
		return nil, &GessFileError{Reason: "at least one source file is required"}
	}
	if opts.PackageName == "" {
		opts.PackageName = "main"
	}
	if opts.FunctionName == "" {
		opts.FunctionName = "BuildRuleset"
	}
	program, err := lowerGessSourcesForGo(ctx, sources)
	if err != nil {
		return nil, err
	}
	out, err := program.goSource(opts)
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source(out)
	if err != nil {
		return out, err
	}
	return formatted, nil
}

type generatedGessProgram struct {
	modules   []ModuleSpec
	templates []TemplateSpec
	actions   []generatedGessAction
	rules     []RuleSpec
	queries   []QuerySpec
	initials  []SessionInitialFact
}

type generatedGessActionKind string

const (
	generatedGessAssert     generatedGessActionKind = "assert"
	generatedGessCall       generatedGessActionKind = "call"
	generatedGessFocus      generatedGessActionKind = "focus"
	generatedGessPopFocus   generatedGessActionKind = "pop-focus"
	generatedGessClearFocus generatedGessActionKind = "clear-focus"
	generatedGessHalt       generatedGessActionKind = "halt"
)

type generatedGessAction struct {
	Name        string
	Kind        generatedGessActionKind
	Logical     bool
	TemplateKey TemplateKey
	FactName    string
	Fields      []string
	Values      []gessRuntimeValue
	Reads       []ActionBindingReadSpec
	CallName    string
	FocusModule ModuleName
}

func lowerGessSourcesForGo(ctx context.Context, sources []GessSourceFile) (generatedGessProgram, error) {
	workspace := NewWorkspace()
	loader := newGessLoader(workspace, &GessDocument{}, DSLRegistry{})
	var docs []*GessDocument
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return generatedGessProgram{}, err
		}
		doc, err := ParseGess(source.Name, source.Source)
		if err != nil {
			return generatedGessProgram{}, err
		}
		docs = append(docs, doc)
	}

	var program generatedGessProgram
	for _, doc := range docs {
		loader.doc = doc
		for _, form := range doc.forms {
			if err := ctx.Err(); err != nil {
				return generatedGessProgram{}, err
			}
			switch form.Head() {
			case "defmodule":
				before := len(workspace.modules)
				if err := loader.loadModule(form); err != nil {
					return generatedGessProgram{}, err
				}
				program.modules = append(program.modules, workspace.modules[before:]...)
			case "deftemplate":
				before := len(workspace.templates)
				if err := loader.loadTemplate(form); err != nil {
					return generatedGessProgram{}, err
				}
				program.templates = append(program.templates, workspace.templates[before:]...)
			}
		}
	}

	seenActions := make(map[string]struct{})
	for _, doc := range docs {
		loader.doc = doc
		for _, form := range doc.forms {
			if err := ctx.Err(); err != nil {
				return generatedGessProgram{}, err
			}
			switch form.Head() {
			case "defmodule", "deftemplate":
			case "deffacts":
				initials, err := loader.loadFacts(form)
				if err != nil {
					return generatedGessProgram{}, err
				}
				program.initials = append(program.initials, initials...)
			case "defrule":
				rule, actions, err := loadGeneratedGessRule(loader, form)
				if err != nil {
					return generatedGessProgram{}, err
				}
				for _, action := range actions {
					if _, exists := seenActions[action.Name]; exists {
						continue
					}
					seenActions[action.Name] = struct{}{}
					if err := workspace.AddAction(action.spec()); err != nil {
						return generatedGessProgram{}, loader.wrap(form.Span, "add generated action", err)
					}
				}
				if err := workspace.AddRule(rule); err != nil {
					return generatedGessProgram{}, loader.wrap(form.Span, "add rule", err)
				}
				program.actions = append(program.actions, actions...)
				program.rules = append(program.rules, rule)
			case "defquery":
				before := len(workspace.queries)
				if err := loader.loadQuery(form); err != nil {
					return generatedGessProgram{}, err
				}
				program.queries = append(program.queries, workspace.queries[before:]...)
			default:
				return generatedGessProgram{}, loader.err(form.Span, "unsupported top-level form %q", form.Head())
			}
		}
	}
	if _, err := workspace.Compile(ctx); err != nil {
		return generatedGessProgram{}, err
	}
	return program, nil
}

func loadGeneratedGessRule(loader *gessLoader, form gessSExpr) (RuleSpec, []generatedGessAction, error) {
	if len(form.List) < 3 || !form.List[1].IsAtom() {
		return RuleSpec{}, nil, loader.err(form.Span, "defrule requires a rule name")
	}
	module, name := splitGessName(form.List[1].Text())
	rule := RuleSpec{Name: name, Module: module}
	body, rhs, err := splitGessRuleBody(form.List[2:])
	if err != nil {
		return RuleSpec{}, nil, loader.wrap(form.Span, "parse rule body", err)
	}
	body = applyRuleDecls(loader, &rule, body)
	scope := newGessScope()
	condition, err := loader.parseConditions(module, body, scope, false)
	if err != nil {
		return RuleSpec{}, nil, err
	}
	rule.ConditionTree = condition
	actions := make([]generatedGessAction, 0, len(rhs))
	for i, actionForm := range rhs {
		action, err := buildGeneratedGessAction(loader, rule.Name, i, module, actionForm, scope)
		if err != nil {
			return RuleSpec{}, nil, err
		}
		rule.Actions = append(rule.Actions, RuleActionSpec{Name: action.Name})
		actions = append(actions, action)
	}
	return rule, actions, nil
}

func buildGeneratedGessAction(loader *gessLoader, ruleName string, index int, module ModuleName, form gessSExpr, scope *gessScope) (generatedGessAction, error) {
	switch form.Head() {
	case "assert", "assert-logical":
		return buildGeneratedGessAssertAction(loader, ruleName, index, module, form.Head() == "assert-logical", form, scope)
	case "focus":
		if len(form.List) != 2 || !form.List[1].IsAtom() {
			return generatedGessAction{}, loader.err(form.Span, "focus requires a module name")
		}
		return generatedGessAction{
			Name:        loader.generatedActionName(ruleName, index, "focus"),
			Kind:        generatedGessFocus,
			FocusModule: ModuleName(form.List[1].Text()),
		}, nil
	case "pop-focus":
		return generatedGessAction{Name: loader.generatedActionName(ruleName, index, "pop-focus"), Kind: generatedGessPopFocus}, nil
	case "clear-focus":
		return generatedGessAction{Name: loader.generatedActionName(ruleName, index, "clear-focus"), Kind: generatedGessClearFocus}, nil
	case "halt":
		return generatedGessAction{Name: loader.generatedActionName(ruleName, index, "halt"), Kind: generatedGessHalt}, nil
	case "call":
		if len(form.List) < 2 || !form.List[1].IsAtom() {
			return generatedGessAction{}, loader.err(form.Span, "call requires a registered action name")
		}
		name := form.List[1].Text()
		if len(form.List) == 2 {
			return generatedGessAction{
				Name:     name,
				Kind:     generatedGessCall,
				CallName: name,
			}, nil
		}
		action := generatedGessAction{
			Name:     loader.generatedActionName(ruleName, index, "call_"+name),
			Kind:     generatedGessCall,
			CallName: name,
		}
		for _, arg := range form.List[2:] {
			value, err := loader.runtimeValue(arg, scope)
			if err != nil {
				return generatedGessAction{}, err
			}
			action.Values = append(action.Values, value)
			if value.fieldRef {
				action.Reads = append(action.Reads, ActionBindingReadSpec{Binding: value.binding, Field: value.field})
			}
		}
		return action, nil
	default:
		return generatedGessAction{}, loader.err(form.Span, "unsupported action %q", form.Head())
	}
}

func buildGeneratedGessAssertAction(loader *gessLoader, ruleName string, index int, module ModuleName, logical bool, form gessSExpr, scope *gessScope) (generatedGessAction, error) {
	if len(form.List) != 2 {
		return generatedGessAction{}, loader.err(form.Span, "%s requires one fact literal", form.Head())
	}
	fact := form.List[1]
	if fact.IsAtom() || len(fact.List) == 0 || !fact.List[0].IsAtom() {
		return generatedGessAction{}, loader.err(fact.Span, "assert requires a fact literal")
	}
	mod, name := splitGessName(fact.Head())
	if mod.IsZero() {
		mod = module
	}
	action := generatedGessAction{
		Name:        loader.generatedActionName(ruleName, index, fact.Head()),
		Kind:        generatedGessAssert,
		Logical:     logical,
		TemplateKey: loader.templateKey(mod, name),
		FactName:    qualifiedGessName(mod, name),
	}
	for _, slot := range fact.List[1:] {
		if len(slot.List) != 2 || !slot.List[0].IsAtom() {
			return generatedGessAction{}, loader.err(slot.Span, "assert slot must be (field value)")
		}
		value, err := loader.runtimeValue(slot.List[1], scope)
		if err != nil {
			return generatedGessAction{}, err
		}
		action.Fields = append(action.Fields, slot.List[0].Text())
		action.Values = append(action.Values, value)
		if value.fieldRef {
			action.Reads = append(action.Reads, ActionBindingReadSpec{Binding: value.binding, Field: value.field})
		}
	}
	return action, nil
}

func (a generatedGessAction) spec() ActionSpec {
	spec := ActionSpec{Name: a.Name}
	switch a.Kind {
	case generatedGessAssert:
		if len(a.Reads) > 0 {
			spec.BindingReads = &ActionBindingReadSetSpec{Reads: a.Reads}
		}
		fields := append([]string(nil), a.Fields...)
		values := append([]gessRuntimeValue(nil), a.Values...)
		templateKey := a.TemplateKey
		factName := a.FactName
		logical := a.Logical
		spec.Fn = func(ctx ActionContext) error {
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
				_, err = ctx.AssertLogical(factName, f)
				return err
			}
			if templateKey != "" {
				_, err = ctx.AssertTemplate(templateKey, f)
				return err
			}
			_, err = ctx.Assert(factName, f)
			return err
		}
	case generatedGessCall:
		if len(a.Reads) > 0 {
			spec.BindingReads = &ActionBindingReadSetSpec{Reads: a.Reads}
		}
		spec.Fn = func(ActionContext) error { return nil }
	case generatedGessFocus:
		module := a.FocusModule
		spec.Fn = func(ctx ActionContext) error { return ctx.PushFocus(module) }
	case generatedGessPopFocus:
		spec.Fn = func(ctx ActionContext) error {
			_, err := ctx.PopFocus()
			return err
		}
	case generatedGessClearFocus:
		spec.Fn = func(ctx ActionContext) error { return ctx.ClearFocusStack() }
	case generatedGessHalt:
		spec.Fn = func(ctx ActionContext) error { return ctx.Halt() }
	}
	return spec
}

func (p generatedGessProgram) goSource(opts GessGoGeneratorOptions) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by gessc; DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", opts.PackageName)
	fmt.Fprintf(&b, "import (\n")
	fmt.Fprintf(&b, "\t\"context\"\n")
	if len(p.actions) > 0 {
		fmt.Fprintf(&b, "\t\"fmt\"\n")
	}
	fmt.Fprintf(&b, "\n\tgessdsl \"github.com/cpcf/gess/dsl\"\n")
	fmt.Fprintf(&b, "\tgessrules \"github.com/cpcf/gess/rules\"\n")
	fmt.Fprintf(&b, "\tgesssession \"github.com/cpcf/gess/session\"\n")
	fmt.Fprintf(&b, ")\n\n")
	fmt.Fprintf(&b, "func %s(ctx context.Context, registry gessdsl.Registry) (*gessrules.Ruleset, []gesssession.InitialFact, error) {\n", opts.FunctionName)
	fmt.Fprintf(&b, "\tworkspace := gessrules.NewWorkspace()\n")
	for _, module := range p.modules {
		fmt.Fprintf(&b, "\tif err := workspace.AddModule(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderModuleSpec(module))
	}
	for _, template := range p.templates {
		fmt.Fprintf(&b, "\tif err := workspace.AddTemplate(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderTemplateSpec(template))
	}
	for _, action := range p.actions {
		if check := renderGeneratedActionRegistryCheck(action); check != "" {
			b.WriteString(check)
		}
		fmt.Fprintf(&b, "\tif err := workspace.AddAction(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderGeneratedAction(action))
	}
	for _, rule := range p.rules {
		fmt.Fprintf(&b, "\tif err := workspace.AddRule(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderRuleSpec(rule))
	}
	for _, query := range p.queries {
		fmt.Fprintf(&b, "\tif err := workspace.AddQuery(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderQuerySpec(query))
	}
	fmt.Fprintf(&b, "\truleset, err := workspace.Compile(ctx)\n")
	fmt.Fprintf(&b, "\tif err != nil {\n\t\treturn nil, nil, err\n\t}\n")
	fmt.Fprintf(&b, "\treturn ruleset, %s, nil\n", renderInitialFacts(p.initials))
	fmt.Fprintf(&b, "}\n")
	if len(p.actions) > 0 {
		writeGeneratedActionHelpers(&b)
	}
	return b.Bytes(), nil
}

func renderModuleSpec(spec ModuleSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.ModuleSpec{Name: %s", renderModuleName(spec.Name))
	if spec.Description != "" {
		fmt.Fprintf(&b, ", Description: %s", strconv.Quote(spec.Description))
	}
	if spec.AutoFocus != nil {
		fmt.Fprintf(&b, ", AutoFocus: func() *bool { v := %t; return &v }()", *spec.AutoFocus)
	}
	b.WriteString("}")
	return b.String()
}

func renderTemplateSpec(spec TemplateSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.TemplateSpec{Name: %s", strconv.Quote(spec.Name))
	if !spec.Module.IsZero() {
		fmt.Fprintf(&b, ", Module: %s", renderModuleName(spec.Module))
	}
	if spec.Key != "" {
		fmt.Fprintf(&b, ", Key: %s", renderTemplateKey(spec.Key))
	}
	if spec.CompatibilityKey != "" {
		fmt.Fprintf(&b, ", CompatibilityKey: %s", renderTemplateKey(spec.CompatibilityKey))
	}
	if len(spec.Fields) > 0 {
		fmt.Fprintf(&b, ", Fields: []gessrules.FieldSpec{%s}", renderFieldSpecs(spec.Fields))
	}
	if spec.DuplicatePolicy != DuplicateStructural {
		fmt.Fprintf(&b, ", DuplicatePolicy: %s", renderDuplicatePolicy(spec.DuplicatePolicy))
	}
	if len(spec.DuplicateKeyNames) > 0 {
		fmt.Fprintf(&b, ", DuplicateKeyNames: %s", renderStringSlice(spec.DuplicateKeyNames))
	}
	if spec.BackchainReactive {
		b.WriteString(", BackchainReactive: true")
	}
	b.WriteString("}")
	return b.String()
}

func renderFieldSpecs(fields []FieldSpec) string {
	var b strings.Builder
	for i, field := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Name: %s", strconv.Quote(field.Name))
		if field.Kind != valueKindUnknown {
			fmt.Fprintf(&b, ", Kind: %s", renderValueKind(field.Kind))
		}
		if field.Required {
			b.WriteString(", Required: true")
		}
		if field.HasDefault {
			fmt.Fprintf(&b, ", Default: %s, HasDefault: true", renderAnyValue(field.Default))
		}
		if len(field.AllowedValues) > 0 {
			fmt.Fprintf(&b, ", AllowedValues: []any{%s}", renderAnyValues(field.AllowedValues))
		}
		b.WriteString("}")
	}
	return b.String()
}

func renderGeneratedAction(action generatedGessAction) string {
	switch action.Kind {
	case generatedGessAssert:
		return fmt.Sprintf("gessGeneratedAssertAction(%s, %t, %s, %s, %s, %s, %s)",
			strconv.Quote(action.Name),
			action.Logical,
			renderTemplateKey(action.TemplateKey),
			strconv.Quote(action.FactName),
			renderStringSlice(action.Fields),
			renderGeneratedValues(action.Values),
			renderBindingReadSpecs(action.Reads),
		)
	case generatedGessCall:
		if len(action.Values) == 0 {
			return fmt.Sprintf("gessRegisteredAction(%s, registry)", strconv.Quote(action.CallName))
		}
		return fmt.Sprintf("gessGeneratedCallAction(%s, %s, %s, %s, registry)",
			strconv.Quote(action.Name),
			strconv.Quote(action.CallName),
			renderGeneratedValues(action.Values),
			renderBindingReadSpecs(action.Reads),
		)
	case generatedGessFocus:
		return fmt.Sprintf("gessrules.ActionSpec{Name: %s, Fn: func(ctx gessrules.ActionContext) error { return ctx.PushFocus(%s) }}", strconv.Quote(action.Name), renderModuleName(action.FocusModule))
	case generatedGessPopFocus:
		return fmt.Sprintf("gessrules.ActionSpec{Name: %s, Fn: func(ctx gessrules.ActionContext) error { _, err := ctx.PopFocus(); return err }}", strconv.Quote(action.Name))
	case generatedGessClearFocus:
		return fmt.Sprintf("gessrules.ActionSpec{Name: %s, Fn: func(ctx gessrules.ActionContext) error { return ctx.ClearFocusStack() }}", strconv.Quote(action.Name))
	case generatedGessHalt:
		return fmt.Sprintf("gessrules.ActionSpec{Name: %s, Fn: func(ctx gessrules.ActionContext) error { return ctx.Halt() }}", strconv.Quote(action.Name))
	default:
		return "gessrules.ActionSpec{}"
	}
}

func renderGeneratedActionRegistryCheck(action generatedGessAction) string {
	if action.Kind != generatedGessCall {
		return ""
	}
	if len(action.Values) == 0 {
		return fmt.Sprintf("\tif _, ok := registry.Actions[%s]; !ok {\n\t\treturn nil, nil, fmt.Errorf(\"gess: registered action %%q is required\", %s)\n\t}\n", strconv.Quote(action.CallName), strconv.Quote(action.CallName))
	}
	return fmt.Sprintf("\tif _, ok := registry.Calls[%s]; !ok {\n\t\treturn nil, nil, fmt.Errorf(\"gess: registered call %%q is required\", %s)\n\t}\n", strconv.Quote(action.CallName), strconv.Quote(action.CallName))
}

func renderRuleSpec(spec RuleSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.RuleSpec{Name: %s", strconv.Quote(spec.Name))
	if !spec.Module.IsZero() {
		fmt.Fprintf(&b, ", Module: %s", renderModuleName(spec.Module))
	}
	if spec.ID != "" {
		fmt.Fprintf(&b, ", ID: gessrules.RuleID(%s)", strconv.Quote(string(spec.ID)))
	}
	if spec.Description != "" {
		fmt.Fprintf(&b, ", Description: %s", strconv.Quote(spec.Description))
	}
	if spec.Salience != 0 {
		fmt.Fprintf(&b, ", Salience: %d", spec.Salience)
	}
	if spec.AutoFocus != nil {
		fmt.Fprintf(&b, ", AutoFocus: func() *bool { v := %t; return &v }()", *spec.AutoFocus)
	}
	if spec.ConditionTree != nil {
		fmt.Fprintf(&b, ", ConditionTree: %s", renderCondition(spec.ConditionTree))
	}
	if len(spec.Actions) > 0 {
		fmt.Fprintf(&b, ", Actions: []gessrules.RuleActionSpec{%s}", renderRuleActions(spec.Actions))
	}
	b.WriteString("}")
	return b.String()
}

func renderQuerySpec(spec QuerySpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.QuerySpec{Name: %s", strconv.Quote(spec.Name))
	if !spec.Module.IsZero() {
		fmt.Fprintf(&b, ", Module: %s", renderModuleName(spec.Module))
	}
	if len(spec.Parameters) > 0 {
		fmt.Fprintf(&b, ", Parameters: []gessrules.QueryParameterSpec{%s}", renderQueryParameters(spec.Parameters))
	}
	if spec.ConditionTree != nil {
		fmt.Fprintf(&b, ", ConditionTree: %s", renderCondition(spec.ConditionTree))
	}
	if len(spec.Returns) > 0 {
		fmt.Fprintf(&b, ", Returns: []gessrules.QueryReturnSpec{%s}", renderQueryReturns(spec.Returns))
	}
	b.WriteString("}")
	return b.String()
}

func renderCondition(spec ConditionSpec) string {
	switch condition := spec.(type) {
	case And:
		return "gessrules.And{Conditions: []gessrules.ConditionSpec{" + renderConditions(condition.Conditions) + "}}"
	case Or:
		return "gessrules.Or{Conditions: []gessrules.ConditionSpec{" + renderConditions(condition.Conditions) + "}}"
	case Not:
		return "gessrules.Not{Condition: " + renderCondition(condition.Condition) + "}"
	case ExistsCondition:
		return "gessrules.Exists(" + renderCondition(condition.Condition) + ")"
	case ForallCondition:
		return "gessrules.Forall(" + renderCondition(condition.Domain) + ", " + renderCondition(condition.Requirement) + ")"
	case Test:
		return "gessrules.Test{Expression: " + renderExpression(condition.Expression) + "}"
	case Match:
		return renderMatch(RuleConditionSpec(condition))
	case AccumulateCondition:
		return "gessrules.Accumulate(" + renderCondition(condition.Input) + renderAggregateSpecs(condition.Specs) + ")"
	default:
		return "nil"
	}
}

func renderConditions(conditions []ConditionSpec) string {
	var b strings.Builder
	for i, condition := range conditions {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(renderCondition(condition))
	}
	return b.String()
}

func renderMatch(spec RuleConditionSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.Match{Binding: %s", strconv.Quote(spec.Binding))
	fmt.Fprintf(&b, ", Target: %s", renderFactTarget(spec.Target))
	if len(spec.FieldConstraints) > 0 {
		fmt.Fprintf(&b, ", FieldConstraints: []gessrules.FieldConstraintSpec{%s}", renderFieldConstraints(spec.FieldConstraints))
	}
	if len(spec.JoinConstraints) > 0 {
		fmt.Fprintf(&b, ", JoinConstraints: []gessrules.JoinConstraintSpec{%s}", renderJoinConstraints(spec.JoinConstraints))
	}
	if len(spec.Predicates) > 0 {
		fmt.Fprintf(&b, ", Predicates: []gessrules.ExpressionSpec{%s}", renderExpressions(spec.Predicates))
	}
	b.WriteString("}")
	return b.String()
}

func renderFieldConstraints(specs []FieldConstraintSpec) string {
	var b strings.Builder
	for i, spec := range specs {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Field: %s, Operator: %s, Value: %s}", strconv.Quote(spec.Field), renderFieldConstraintOperator(spec.Operator), renderAnyValue(spec.Value))
	}
	return b.String()
}

func renderJoinConstraints(specs []JoinConstraintSpec) string {
	var b strings.Builder
	for i, spec := range specs {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Field: %s, Operator: %s, Ref: gessrules.FieldRef{Binding: %s, Field: %s}}", strconv.Quote(spec.Field), renderFieldConstraintOperator(spec.Operator), strconv.Quote(spec.Ref.Binding), strconv.Quote(spec.Ref.Field))
	}
	return b.String()
}

func renderExpression(spec ExpressionSpec) string {
	switch expr := spec.(type) {
	case ConstExpr:
		return "gessrules.ConstExpr{Value: " + renderAnyValue(expr.Value) + "}"
	case CurrentFieldExpr:
		return "gessrules.CurrentFieldExpr{Field: " + strconv.Quote(expr.Field) + "}"
	case BindingFieldExpr:
		return "gessrules.BindingFieldExpr{Binding: " + strconv.Quote(expr.Binding) + ", Field: " + strconv.Quote(expr.Field) + "}"
	case BindingValueExpr:
		return "gessrules.BindingValueExpr{Binding: " + strconv.Quote(expr.Binding) + "}"
	case ParamExpr:
		return "gessrules.ParamExpr{Name: " + strconv.Quote(expr.Name) + "}"
	case CallExpr:
		return "gessrules.Call(" + strconv.Quote(expr.Name) + renderCallArgs(expr.Args) + ")"
	case CompareExpr:
		return "gessrules.CompareExpr{Operator: " + renderExpressionCompareOperator(expr.Operator) + ", Left: " + renderExpression(expr.Left) + ", Right: " + renderExpression(expr.Right) + "}"
	case BooleanExpr:
		return "gessrules.BooleanExpr{Operator: " + renderExpressionBooleanOperator(expr.Operator) + ", Operands: []gessrules.ExpressionSpec{" + renderExpressions(expr.Operands) + "}}"
	default:
		return "nil"
	}
}

func renderExpressions(specs []ExpressionSpec) string {
	var b strings.Builder
	for i, spec := range specs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(renderExpression(spec))
	}
	return b.String()
}

func renderCallArgs(args []ExpressionSpec) string {
	if len(args) == 0 {
		return ""
	}
	return ", " + renderExpressions(args)
}

func renderAggregateSpecs(specs []AggregateSpec) string {
	var b strings.Builder
	for _, spec := range specs {
		b.WriteString(", ")
		switch spec.Kind() {
		case AggregateCount:
			b.WriteString("gessrules.Count()")
		case AggregateSum:
			b.WriteString("gessrules.Sum(" + renderExpression(spec.Expression()) + ")")
		case AggregateMin:
			b.WriteString("gessrules.Min(" + renderExpression(spec.Expression()) + ")")
		case AggregateMax:
			b.WriteString("gessrules.Max(" + renderExpression(spec.Expression()) + ")")
		case AggregateCollect:
			b.WriteString("gessrules.Collect(" + renderExpression(spec.Expression()) + ")")
		}
		if spec.Binding() != "" {
			b.WriteString(".As(" + strconv.Quote(spec.Binding()) + ")")
		}
	}
	return b.String()
}

func renderRuleActions(actions []RuleActionSpec) string {
	var b strings.Builder
	for i, action := range actions {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Name: %s}", strconv.Quote(action.Name))
	}
	return b.String()
}

func renderQueryParameters(params []QueryParameterSpec) string {
	var b strings.Builder
	for i, param := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Name: %s, Kind: %s}", strconv.Quote(param.Name), renderValueKind(param.Kind))
	}
	return b.String()
}

func renderQueryReturns(returns []QueryReturnSpec) string {
	var b strings.Builder
	for i, ret := range returns {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "gessrules.ReturnValue(%s, %s)", strconv.Quote(ret.Alias), renderExpression(ret.Expression))
	}
	return b.String()
}

func renderInitialFacts(initials []SessionInitialFact) string {
	if len(initials) == 0 {
		return "nil"
	}
	var b strings.Builder
	b.WriteString("[]gesssession.InitialFact{")
	for i, initial := range initials {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{")
		if initial.Name != "" {
			fmt.Fprintf(&b, "Name: %s, ", strconv.Quote(initial.Name))
		}
		if initial.TemplateKey != "" {
			fmt.Fprintf(&b, "TemplateKey: %s, ", renderTemplateKey(initial.TemplateKey))
		}
		fmt.Fprintf(&b, "Fields: %s", renderFields(initial.Fields))
		b.WriteString("}")
	}
	b.WriteString("}")
	return b.String()
}

func renderFields(fields Fields) string {
	if len(fields) == 0 {
		return "nil"
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("gessrules.MustFields(")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s, %s", strconv.Quote(key), renderValue(fields[key]))
	}
	b.WriteString(")")
	return b.String()
}

func renderGeneratedValues(values []gessRuntimeValue) string {
	if len(values) == 0 {
		return "nil"
	}
	var b strings.Builder
	b.WriteString("[]gessGeneratedActionValue{")
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{")
		switch {
		case value.hasConst:
			fmt.Fprintf(&b, "Constant: %s, HasConstant: true", renderAnyValue(value.constant))
		case value.fieldRef:
			fmt.Fprintf(&b, "Binding: %s, Field: %s, FieldRef: true", strconv.Quote(value.binding), strconv.Quote(value.field))
		case value.bindingValue:
			fmt.Fprintf(&b, "Binding: %s, BindingValue: true", strconv.Quote(value.binding))
		}
		b.WriteString("}")
	}
	b.WriteString("}")
	return b.String()
}

func renderBindingReadSpecs(reads []ActionBindingReadSpec) string {
	if len(reads) == 0 {
		return "nil"
	}
	var b strings.Builder
	b.WriteString("[]gessrules.ActionBindingReadSpec{")
	for i, read := range reads {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Binding: %s, Field: %s}", strconv.Quote(read.Binding), strconv.Quote(read.Field))
	}
	b.WriteString("}")
	return b.String()
}

func writeGeneratedActionHelpers(b *bytes.Buffer) {
	b.WriteString(`

type gessGeneratedActionValue struct {
	Constant     any
	HasConstant  bool
	Binding      string
	Field        string
	FieldRef     bool
	BindingValue bool
}

func (v gessGeneratedActionValue) value(ctx gessrules.ActionContext) (any, error) {
	switch {
	case v.HasConstant:
		return v.Constant, nil
	case v.FieldRef:
		value, ok := ctx.BindingScalarValue(v.Binding, v.Field)
		if !ok {
			return nil, fmt.Errorf("gess: generated action missing binding field %s.%s", v.Binding, v.Field)
		}
		return value, nil
	case v.BindingValue:
		value, ok := ctx.BindingValue(v.Binding)
		if !ok {
			return nil, fmt.Errorf("gess: generated action missing binding value %s", v.Binding)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("gess: generated action value is not configured")
	}
}

func gessGeneratedAssertAction(name string, logical bool, templateKey gessrules.TemplateKey, factName string, fields []string, values []gessGeneratedActionValue, reads []gessrules.ActionBindingReadSpec) gessrules.ActionSpec {
	spec := gessrules.ActionSpec{Name: name}
	if len(reads) > 0 {
		spec.BindingReads = &gessrules.ActionBindingReadSetSpec{Reads: reads}
	}
	spec.Fn = func(ctx gessrules.ActionContext) error {
		pairs := make([]any, 0, len(fields)*2)
		for i, field := range fields {
			value, err := values[i].value(ctx)
			if err != nil {
				return err
			}
			pairs = append(pairs, field, value)
		}
		f, err := gessrules.NewFieldsFromPairs(pairs...)
		if err != nil {
			return err
		}
		if logical {
			_, err = ctx.AssertLogical(factName, f)
			return err
		}
		if templateKey != "" {
			_, err = ctx.AssertTemplate(templateKey, f)
			return err
		}
		_, err = ctx.Assert(factName, f)
		return err
	}
	return spec
}

func gessRegisteredAction(name string, registry gessdsl.Registry) gessrules.ActionSpec {
	fn, ok := registry.Actions[name]
	if !ok {
		return gessrules.ActionSpec{Name: name, Fn: func(gessrules.ActionContext) error {
			return fmt.Errorf("gess: registered action %q is required", name)
		}}
	}
	return gessrules.ActionSpec{Name: name, Fn: fn}
}

func gessGeneratedCallAction(name string, callName string, values []gessGeneratedActionValue, reads []gessrules.ActionBindingReadSpec, registry gessdsl.Registry) gessrules.ActionSpec {
	call, ok := registry.Calls[callName]
	spec := gessrules.ActionSpec{Name: name}
	if len(reads) > 0 {
		spec.BindingReads = &gessrules.ActionBindingReadSetSpec{Reads: reads}
	}
	spec.Fn = func(ctx gessrules.ActionContext) error {
		if !ok {
			return fmt.Errorf("gess: registered call %q is required", callName)
		}
		args := make([]gessrules.Value, 0, len(values))
		for _, value := range values {
			raw, err := value.value(ctx)
			if err != nil {
				return err
			}
			normalized, err := gessrules.NewValue(raw)
			if err != nil {
				return err
			}
			args = append(args, normalized)
		}
		return call(ctx, args)
	}
	return spec
}
`)
}

func renderFactTarget(target FactTarget) string {
	switch target.Kind() {
	case FactTargetTemplate:
		ref := target.Ref()
		if !ref.Module.IsZero() {
			return fmt.Sprintf("gessrules.TemplateFactIn(%s, %s)", renderModuleName(ref.Module), strconv.Quote(ref.Name))
		}
		return fmt.Sprintf("gessrules.TemplateFact(%s)", strconv.Quote(ref.Name))
	case FactTargetTemplateKey:
		return fmt.Sprintf("gessrules.TemplateKeyFact(%s)", renderTemplateKey(target.TemplateKey()))
	case FactTargetDynamic:
		ref := target.Ref()
		if !ref.Module.IsZero() {
			return fmt.Sprintf("gessrules.DynamicFactIn(%s, %s)", renderModuleName(ref.Module), strconv.Quote(ref.Name))
		}
		return fmt.Sprintf("gessrules.DynamicFact(%s)", strconv.Quote(ref.Name))
	default:
		return "gessrules.FactTarget{}"
	}
}

func renderAnyValues(values []any) string {
	var b strings.Builder
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(renderAnyValue(value))
	}
	return b.String()
}

func renderAnyValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "nil"
	case string:
		return strconv.Quote(typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("int64(%d)", typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case Value:
		return renderValue(typed)
	default:
		return fmt.Sprintf("%#v", typed)
	}
}

func renderValue(value Value) string {
	switch value.Kind() {
	case ValueNull:
		return "gessrules.NullValue()"
	case ValueBool:
		v, _ := value.AsBool()
		return fmt.Sprintf("%t", v)
	case ValueInt:
		v, _ := value.AsInt64()
		return fmt.Sprintf("int64(%d)", v)
	case ValueFloat:
		v, _ := value.AsFloat64()
		return strconv.FormatFloat(v, 'g', -1, 64)
	case ValueString:
		v, _ := value.AsString()
		return strconv.Quote(v)
	default:
		return "nil"
	}
}

func renderStringSlice(values []string) string {
	if len(values) == 0 {
		return "nil"
	}
	var b strings.Builder
	b.WriteString("[]string{")
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(value))
	}
	b.WriteString("}")
	return b.String()
}

func renderModuleName(name ModuleName) string {
	return "gessrules.ModuleName(" + strconv.Quote(string(name)) + ")"
}

func renderTemplateKey(key TemplateKey) string {
	return "gessrules.TemplateKey(" + strconv.Quote(string(key)) + ")"
}

func renderValueKind(kind ValueKind) string {
	switch kind {
	case ValueAny:
		return "gessrules.ValueAny"
	case ValueNull:
		return "gessrules.ValueNull"
	case ValueBool:
		return "gessrules.ValueBool"
	case ValueInt:
		return "gessrules.ValueInt"
	case ValueFloat:
		return "gessrules.ValueFloat"
	case ValueString:
		return "gessrules.ValueString"
	case ValueList:
		return "gessrules.ValueList"
	case ValueMap:
		return "gessrules.ValueMap"
	default:
		return fmt.Sprintf("gessrules.ValueKind(%d)", kind)
	}
}

func renderDuplicatePolicy(policy DuplicatePolicy) string {
	switch policy {
	case DuplicateAllow:
		return "gessrules.DuplicateAllow"
	case DuplicateUniqueKey:
		return "gessrules.DuplicateUniqueKey"
	default:
		return "gessrules.DuplicateStructural"
	}
}

func renderFieldConstraintOperator(op FieldConstraintOperator) string {
	switch op {
	case FieldConstraintExists:
		return "gessrules.FieldConstraintExists"
	case FieldConstraintEqual:
		return "gessrules.FieldConstraintEqual"
	case FieldConstraintNotEqual:
		return "gessrules.FieldConstraintNotEqual"
	case FieldConstraintLessThan:
		return "gessrules.FieldConstraintLessThan"
	case FieldConstraintLessOrEqual:
		return "gessrules.FieldConstraintLessOrEqual"
	case FieldConstraintGreaterThan:
		return "gessrules.FieldConstraintGreaterThan"
	case FieldConstraintGreaterOrEqual:
		return "gessrules.FieldConstraintGreaterOrEqual"
	default:
		return "gessrules.FieldConstraintOperator(" + strconv.Quote(string(op)) + ")"
	}
}

func renderExpressionCompareOperator(op ExpressionComparisonOperator) string {
	switch op {
	case ExpressionCompareEqual:
		return "gessrules.ExpressionCompareEqual"
	case ExpressionCompareNotEqual:
		return "gessrules.ExpressionCompareNotEqual"
	case ExpressionCompareLessThan:
		return "gessrules.ExpressionCompareLessThan"
	case ExpressionCompareLessOrEqual:
		return "gessrules.ExpressionCompareLessOrEqual"
	case ExpressionCompareGreaterThan:
		return "gessrules.ExpressionCompareGreaterThan"
	case ExpressionCompareGreaterOrEqual:
		return "gessrules.ExpressionCompareGreaterOrEqual"
	default:
		return "gessrules.ExpressionCompareUnknown"
	}
}

func renderExpressionBooleanOperator(op ExpressionBooleanOperator) string {
	switch op {
	case ExpressionBoolAnd:
		return "gessrules.ExpressionBoolAnd"
	case ExpressionBoolOr:
		return "gessrules.ExpressionBoolOr"
	case ExpressionBoolNot:
		return "gessrules.ExpressionBoolNot"
	default:
		return "gessrules.ExpressionBoolUnknown"
	}
}
