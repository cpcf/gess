package engine

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"slices"
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
	modules    []ModuleSpec
	globals    []GlobalSpec
	templates  []TemplateSpec
	functions  []ExpressionFunctionSpec
	registered []string
	actions    []ActionSpec
	rules      []RuleSpec
	queries    []QuerySpec
	initials   []SessionInitialFact
}

func lowerGessSourcesForGo(ctx context.Context, sources []GessSourceFile) (generatedGessProgram, error) {
	workspace := NewWorkspace()
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

	// The registry is not available at generation time. Stub every host call
	// and action a source references so the loader can build real action specs
	// (with those stub closures) and Compile can validate them. The stubs are
	// never emitted: bare (call name) renders gessRegisteredAction and
	// (call name arg...) renders gessGeneratedCallAction, both resolving the
	// real host function from the generated program's registry at build time.
	registry := DSLRegistry{Actions: map[string]ActionFunc{}, Calls: map[string]DSLCallFunc{}}
	registeredActions := make(map[string]struct{})
	for _, doc := range docs {
		missing := doc.MissingRegistrations(DSLRegistry{})
		for _, name := range missing.Actions {
			registry.Actions[name] = func(ActionContext) error { return nil }
			registeredActions[name] = struct{}{}
		}
		for _, name := range missing.Calls {
			registry.Calls[name] = func(ActionContext, []Value) error { return nil }
		}
	}
	loader := newGessLoader(workspace, &GessDocument{}, registry)
	// Register the stub host actions on the workspace so a bare (call name)
	// resolves during Compile; they render as gessRegisteredAction.
	registeredActionNames := make([]string, 0, len(registeredActions))
	for name := range registeredActions {
		registeredActionNames = append(registeredActionNames, name)
	}
	slices.Sort(registeredActionNames)
	for _, name := range registeredActionNames {
		if err := workspace.AddAction(ActionSpec{Name: name, Fn: registry.Actions[name]}); err != nil {
			return generatedGessProgram{}, err
		}
	}

	var program generatedGessProgram
	program.registered = registeredActionNames
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
			case "defglobal":
				before := len(workspace.globals)
				if err := loader.loadGlobal(form); err != nil {
					return generatedGessProgram{}, err
				}
				program.globals = append(program.globals, workspace.globals[before:]...)
			case "deftemplate":
				before := len(workspace.templates)
				if err := loader.loadTemplate(form); err != nil {
					return generatedGessProgram{}, err
				}
				program.templates = append(program.templates, workspace.templates[before:]...)
			case "deffunction":
				before := len(workspace.exprFuncs)
				if err := loader.loadExpressionFunction(form); err != nil {
					return generatedGessProgram{}, err
				}
				program.functions = append(program.functions, workspace.exprFuncs[before:]...)
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
			case "defmodule", "defglobal", "deftemplate", "deffunction":
			case "deffacts":
				initials, err := loader.loadFacts(form)
				if err != nil {
					return generatedGessProgram{}, err
				}
				program.initials = append(program.initials, initials...)
			case "defrule":
				before := len(workspace.actions)
				if err := loader.loadRule(form); err != nil {
					return generatedGessProgram{}, err
				}
				for _, action := range workspace.actions[before:] {
					if _, exists := seenActions[action.Name]; exists {
						continue
					}
					seenActions[action.Name] = struct{}{}
					program.actions = append(program.actions, action)
				}
				program.rules = append(program.rules, workspace.rules[len(workspace.rules)-1])
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

func (p generatedGessProgram) goSource(opts GessGoGeneratorOptions) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by gessc; DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", opts.PackageName)
	fmt.Fprintf(&b, "import (\n")
	fmt.Fprintf(&b, "\t\"context\"\n")
	if p.usesFmt() {
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
	for _, global := range p.globals {
		fmt.Fprintf(&b, "\tif err := workspace.AddGlobal(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderGlobalSpec(global))
	}
	for _, template := range p.templates {
		fmt.Fprintf(&b, "\tif err := workspace.AddTemplate(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderTemplateSpec(template))
	}
	for _, function := range p.functions {
		fmt.Fprintf(&b, "\tif err := workspace.AddExpressionFunction(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderExpressionFunctionSpec(function))
	}
	for _, name := range p.registered {
		fmt.Fprintf(&b, "\tif _, ok := registry.Actions[%s]; !ok {\n\t\treturn nil, nil, fmt.Errorf(\"gess: registered action %%q is required\", %s)\n\t}\n", strconv.Quote(name), strconv.Quote(name))
		fmt.Fprintf(&b, "\tif err := workspace.AddAction(gessRegisteredAction(%s, registry)); err != nil {\n\t\treturn nil, nil, err\n\t}\n", strconv.Quote(name))
	}
	for _, action := range p.actions {
		if action.Call != nil {
			fmt.Fprintf(&b, "\tif _, ok := registry.Calls[%s]; !ok {\n\t\treturn nil, nil, fmt.Errorf(\"gess: registered call %%q is required\", %s)\n\t}\n", strconv.Quote(action.Call.Name), strconv.Quote(action.Call.Name))
		}
		fmt.Fprintf(&b, "\tif err := workspace.AddAction(%s); err != nil {\n\t\treturn nil, nil, err\n\t}\n", renderActionSpec(action))
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
	if len(p.registered) > 0 {
		writeGeneratedActionHelpers(&b)
	}
	return b.Bytes(), nil
}

// usesFmt reports whether the generated file references fmt (registry presence
// checks for registered actions and host calls, and the gessRegisteredAction
// helper).
func (p generatedGessProgram) usesFmt() bool {
	if len(p.registered) > 0 {
		return true
	}
	for _, action := range p.actions {
		if action.Call != nil {
			return true
		}
	}
	return false
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

func renderGlobalSpec(spec GlobalSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.GlobalSpec{Name: %s, Kind: %s", strconv.Quote(spec.Name), renderValueKind(spec.Kind))
	if spec.HasDefault {
		fmt.Fprintf(&b, ", Default: %s, HasDefault: true", renderAnyValue(spec.Default))
	}
	if spec.Description != "" {
		fmt.Fprintf(&b, ", Description: %s", strconv.Quote(spec.Description))
	}
	b.WriteString("}")
	return b.String()
}

func renderTemplateSpec(spec TemplateSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.TemplateSpec{Name: %s", strconv.Quote(spec.Name))
	if !sourceSpanIsZero(spec.Source) {
		fmt.Fprintf(&b, ", Source: %s", renderSourceSpan(spec.Source))
	}
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

func renderExpressionFunctionSpec(spec ExpressionFunctionSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gessrules.ExpressionFunctionSpec{Name: %s, Return: %s", strconv.Quote(spec.Name), renderValueKind(spec.Return))
	if len(spec.Params) > 0 {
		fmt.Fprintf(&b, ", Params: []gessrules.ExpressionFunctionParamSpec{%s}", renderExpressionFunctionParams(spec.Params))
	}
	if spec.Expression != nil {
		fmt.Fprintf(&b, ", Expression: %s", renderExpression(spec.Expression))
	}
	if spec.Description != "" {
		fmt.Fprintf(&b, ", Description: %s", strconv.Quote(spec.Description))
	}
	b.WriteString("}")
	return b.String()
}

func renderExpressionFunctionParams(params []ExpressionFunctionParamSpec) string {
	var b strings.Builder
	for i, param := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Name: %s, Kind: %s}", strconv.Quote(param.Name), renderValueKind(param.Kind))
	}
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

func renderActionSpec(action ActionSpec) string {
	var b strings.Builder
	b.WriteString("gessrules.ActionSpec{Name: ")
	b.WriteString(strconv.Quote(action.Name))
	if action.GessSource != "" {
		b.WriteString(", GessSource: ")
		b.WriteString(strconv.Quote(action.GessSource))
	}
	switch {
	case action.Effect != nil:
		b.WriteString(", Effect: ")
		b.WriteString(renderActionEffectSpec(action.Effect))
	case action.Call != nil:
		b.WriteString(", Call: ")
		b.WriteString(renderActionCallSpec(action.Call))
	case action.AssertTemplateValues != nil:
		b.WriteString(", AssertTemplateValues: ")
		b.WriteString(renderAssertTemplateValuesSpec(action.AssertTemplateValues))
	}
	b.WriteString("}")
	return b.String()
}

func renderActionEffectSpec(spec *ActionEffectSpec) string {
	var b strings.Builder
	b.WriteString("&gessrules.ActionEffectSpec{Kind: ")
	b.WriteString(renderActionEffectKind(spec.Kind))
	if spec.Target != "" {
		fmt.Fprintf(&b, ", Target: %s", strconv.Quote(spec.Target))
	}
	if spec.TemplateKey != "" {
		fmt.Fprintf(&b, ", TemplateKey: %s", renderTemplateKey(spec.TemplateKey))
	}
	if spec.FactName != "" {
		fmt.Fprintf(&b, ", FactName: %s", strconv.Quote(spec.FactName))
	}
	if len(spec.Fields) > 0 {
		fmt.Fprintf(&b, ", Fields: %s", renderStringSlice(spec.Fields))
	}
	if len(spec.Unset) > 0 {
		fmt.Fprintf(&b, ", Unset: %s", renderStringSlice(spec.Unset))
	}
	if len(spec.Values) > 0 {
		fmt.Fprintf(&b, ", Values: []gessrules.ExpressionSpec{%s}", renderExpressions(spec.Values))
	}
	b.WriteString("}")
	return b.String()
}

func renderActionCallSpec(spec *ActionCallSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "&gessrules.ActionCallSpec{Name: %s, Fn: registry.Calls[%s]", strconv.Quote(spec.Name), strconv.Quote(spec.Name))
	if len(spec.Args) > 0 {
		fmt.Fprintf(&b, ", Args: []gessrules.ExpressionSpec{%s}", renderExpressions(spec.Args))
	}
	b.WriteString("}")
	return b.String()
}

func renderAssertTemplateValuesSpec(spec *AssertTemplateValuesActionSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "&gessrules.AssertTemplateValuesActionSpec{TemplateKey: %s", renderTemplateKey(spec.TemplateKey))
	if len(spec.Values) > 0 {
		fmt.Fprintf(&b, ", Values: []gessrules.ExpressionSpec{%s}", renderExpressions(spec.Values))
	}
	b.WriteString("}")
	return b.String()
}

func renderActionEffectKind(kind ActionEffectKind) string {
	switch kind {
	case ActionEffectAssert:
		return "gessrules.ActionEffectAssert"
	case ActionEffectAssertLogical:
		return "gessrules.ActionEffectAssertLogical"
	case ActionEffectModify:
		return "gessrules.ActionEffectModify"
	case ActionEffectRetract:
		return "gessrules.ActionEffectRetract"
	case ActionEffectEmit:
		return "gessrules.ActionEffectEmit"
	case ActionEffectBind:
		return "gessrules.ActionEffectBind"
	case ActionEffectPushFocus:
		return "gessrules.ActionEffectPushFocus"
	case ActionEffectPopFocus:
		return "gessrules.ActionEffectPopFocus"
	case ActionEffectClearFocus:
		return "gessrules.ActionEffectClearFocus"
	case ActionEffectHalt:
		return "gessrules.ActionEffectHalt"
	default:
		return "gessrules.ActionEffectAssert"
	}
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
	if !sourceSpanIsZero(spec.Source) {
		fmt.Fprintf(&b, ", Source: %s", renderSourceSpan(spec.Source))
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
	if !sourceSpanIsZero(spec.Source) {
		fmt.Fprintf(&b, ", Source: %s", renderSourceSpan(spec.Source))
	}
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
		return "gessrules.And{Conditions: []gessrules.ConditionSpec{" + renderConditions(condition.Conditions) + "}" + renderConditionSource(condition.Source) + "}"
	case Or:
		return "gessrules.Or{Conditions: []gessrules.ConditionSpec{" + renderConditions(condition.Conditions) + "}" + renderConditionSource(condition.Source) + "}"
	case Not:
		return "gessrules.Not{Condition: " + renderCondition(condition.Condition) + renderConditionSource(condition.Source) + "}"
	case ExistsCondition:
		return "gessrules.ExistsCondition{Condition: " + renderCondition(condition.Condition) + renderConditionSource(condition.Source) + "}"
	case ForallCondition:
		return "gessrules.ForallCondition{Domain: " + renderCondition(condition.Domain) + ", Requirement: " + renderCondition(condition.Requirement) + renderConditionSource(condition.Source) + "}"
	case Test:
		return "gessrules.Test{Expression: " + renderExpression(condition.Expression) + renderConditionSource(condition.Source) + "}"
	case Match:
		return renderMatch(RuleConditionSpec(condition))
	case AccumulateCondition:
		return "gessrules.AccumulateCondition{Input: " + renderCondition(condition.Input) + ", Specs: []gessrules.AggregateSpec{" + renderAggregateSpecList(condition.Specs) + "}" + renderConditionSource(condition.Source) + "}"
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
	if !sourceSpanIsZero(spec.Source) {
		fmt.Fprintf(&b, ", Source: %s", renderSourceSpan(spec.Source))
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
	case GlobalExpr:
		return "gessrules.GlobalExpr{Name: " + strconv.Quote(expr.Name) + "}"
	case RHSBindExpr:
		return "gessrules.RHSBindExpr{Name: " + strconv.Quote(expr.Name) + "}"
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
		b.WriteString(renderAggregateSpec(spec))
	}
	return b.String()
}

func renderAggregateSpecList(specs []AggregateSpec) string {
	var b strings.Builder
	for i, spec := range specs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(renderAggregateSpec(spec))
	}
	return b.String()
}

func renderAggregateSpec(spec AggregateSpec) string {
	var b strings.Builder
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
	return b.String()
}

func renderRuleActions(actions []RuleActionSpec) string {
	var b strings.Builder
	for i, action := range actions {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{Name: %s", strconv.Quote(action.Name))
		if !sourceSpanIsZero(action.Source) {
			fmt.Fprintf(&b, ", Source: %s", renderSourceSpan(action.Source))
		}
		b.WriteString("}")
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
		fmt.Fprintf(&b, "gessrules.QueryReturnSpec{Alias: %s, Expression: %s", strconv.Quote(ret.Alias), renderExpression(ret.Expression))
		if !sourceSpanIsZero(ret.Source) {
			fmt.Fprintf(&b, ", Source: %s", renderSourceSpan(ret.Source))
		}
		b.WriteString("}")
	}
	return b.String()
}

func renderConditionSource(source SourceSpan) string {
	if sourceSpanIsZero(source) {
		return ""
	}
	return ", Source: " + renderSourceSpan(source)
}

func renderSourceSpan(source SourceSpan) string {
	return fmt.Sprintf("gessrules.SourceSpan{Name: %s, StartLine: %d, StartColumn: %d, EndLine: %d, EndColumn: %d}",
		strconv.Quote(source.Name),
		source.StartLine,
		source.StartColumn,
		source.EndLine,
		source.EndColumn,
	)
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

func writeGeneratedActionHelpers(b *bytes.Buffer) {
	b.WriteString(`

func gessRegisteredAction(name string, registry gessdsl.Registry) gessrules.ActionSpec {
	fn, ok := registry.Actions[name]
	if !ok {
		return gessrules.ActionSpec{Name: name, Fn: func(gessrules.ActionContext) error {
			return fmt.Errorf("gess: registered action %q is required", name)
		}}
	}
	return gessrules.ActionSpec{Name: name, Fn: fn}
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
		// Dynamic (untemplated) facts are engine-internal only and cannot be
		// authored in .gess, so they never reach code generation.
		panic("gess: code generation does not support dynamic fact targets")
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
