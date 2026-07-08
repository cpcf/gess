package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type Workspace struct {
	modules   []ModuleSpec
	globals   []GlobalSpec
	templates []TemplateSpec
	actions   []ActionSpec
	functions []PureFunctionSpec
	exprFuncs []ExpressionFunctionSpec
	rules     []RuleSpec
	queries   []QuerySpec
}

func NewWorkspace() *Workspace {
	return &Workspace{}
}

// AddModule records a module declaration. Compile treats identical repeated
// declarations as idempotent and rejects conflicting metadata.
func (w *Workspace) AddModule(spec ModuleSpec) error {
	module, err := compileModuleSpec(spec)
	if err != nil {
		return err
	}

	w.modules = append(w.modules, moduleSpec(module))
	return nil
}

func (w *Workspace) AddTemplate(spec TemplateSpec) error {
	template, err := compileTemplateSpec(spec)
	if err != nil {
		return err
	}

	for _, existing := range w.templates {
		if strings.TrimSpace(existing.Name) == template.name {
			return &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template",
			}
		}
		existingKey := TemplateKey(strings.TrimSpace(string(existing.Key)))
		if existingKey == "" {
			existingKey = TemplateKey(strings.TrimSpace(existing.Name))
		}
		if existingKey == template.key {
			return &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template key",
			}
		}
	}

	w.templates = append(w.templates, template.spec())
	return nil
}

// AddGlobal declares a typed global constant readable from rule, query, and
// aggregate expressions and from ActionContext.Global. Values bind per session
// via WithGlobals; a declaration without a default requires every session to
// supply a value. In .gess sources the equivalent form is
// (defglobal *name* (type KIND) (default value)).
func (w *Workspace) AddGlobal(spec GlobalSpec) error {
	normalized, err := compileGlobalSpec(spec, len(w.globals))
	if err != nil {
		return err
	}
	if _, ok := w.globalIndex(normalized.name); ok {
		return &ValidationError{
			GlobalName: normalized.name,
			Reason:     "duplicate global",
		}
	}

	w.globals = append(w.globals, cloneGlobalSpec(spec))
	return nil
}

func (w *Workspace) ReplaceGlobal(spec GlobalSpec) error {
	normalized, err := compileGlobalSpec(spec, 0)
	if err != nil {
		return err
	}
	idx, ok := w.globalIndex(normalized.name)
	if !ok {
		return &ValidationError{
			Reason: "global not found",
		}
	}

	w.globals[idx] = cloneGlobalSpec(spec)
	return nil
}

func (w *Workspace) RemoveGlobal(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{
			Reason: "global name is required",
		}
	}

	idx, ok := w.globalIndex(name)
	if !ok {
		return &ValidationError{
			Reason: "global not found",
		}
	}

	w.globals = append(w.globals[:idx], w.globals[idx+1:]...)
	return nil
}

func (w *Workspace) AddAction(spec ActionSpec) error {
	normalized, err := normalizeActionSpec(spec)
	if err != nil {
		return err
	}
	if _, ok := w.actionIndex(normalized.Name); ok {
		return &ValidationError{
			Reason: "duplicate action",
		}
	}

	w.actions = append(w.actions, normalized)
	return nil
}

func (w *Workspace) ReplaceAction(spec ActionSpec) error {
	normalized, err := normalizeActionSpec(spec)
	if err != nil {
		return err
	}

	idx, ok := w.actionIndex(normalized.Name)
	if !ok {
		return &ValidationError{
			Reason: "action not found",
		}
	}

	w.actions[idx] = normalized
	return nil
}

func (w *Workspace) RemoveAction(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{
			Reason: "action name is required",
		}
	}

	idx, ok := w.actionIndex(name)
	if !ok {
		return &ValidationError{
			Reason: "action not found",
		}
	}

	w.actions = append(w.actions[:idx], w.actions[idx+1:]...)
	return nil
}

func (w *Workspace) AddFunction(spec PureFunctionSpec) error {
	normalized, err := compilePureFunctionSpec(spec, len(w.functions))
	if err != nil {
		return err
	}
	if _, ok := w.functionIndex(normalized.name); ok {
		return &ValidationError{
			FunctionName: normalized.name,
			Reason:       "duplicate function",
			Err:          ErrFunctionValidation,
		}
	}
	if _, ok := w.expressionFunctionIndex(normalized.name); ok {
		return &ValidationError{
			FunctionName: normalized.name,
			Reason:       "duplicate function",
			Err:          ErrFunctionValidation,
		}
	}

	w.functions = append(w.functions, clonePureFunctionSpec(spec))
	return nil
}

// AddExpressionFunction registers a pure function whose body is a single
// expression over its declared parameters, the programmatic equivalent of a
// .gess deffunction. Bodies may call Go-registered functions and expression
// functions defined earlier in declaration order; recursion (direct or
// mutual) is therefore impossible by construction. Bodies compile with the
// ruleset at Workspace.Compile, so body errors surface there rather than
// here.
func (w *Workspace) AddExpressionFunction(spec ExpressionFunctionSpec) error {
	normalized := cloneExpressionFunctionSpec(spec)
	if normalized.Name == "" {
		return &ValidationError{Reason: "function name is required", Err: ErrFunctionValidation}
	}
	if !validPureFunctionName(normalized.Name) {
		return &ValidationError{Reason: "invalid function name", Err: ErrFunctionValidation}
	}
	if !validPureFunctionValueKind(normalized.Return) {
		return &ValidationError{Reason: "invalid function return kind", Err: ErrFunctionValidation}
	}
	if normalized.Expression == nil {
		return &ValidationError{Reason: "function expression is required", Err: ErrFunctionValidation}
	}
	seenParams := make(map[string]struct{}, len(normalized.Params))
	for _, param := range normalized.Params {
		if param.Name == "" {
			return &ValidationError{Reason: "function parameter name is required", Err: ErrFunctionValidation}
		}
		if !validPureFunctionValueKind(param.Kind) {
			return &ValidationError{Reason: "invalid function parameter kind", Err: ErrFunctionValidation}
		}
		if _, exists := seenParams[param.Name]; exists {
			return &ValidationError{Reason: "duplicate function parameter", Err: ErrFunctionValidation}
		}
		seenParams[param.Name] = struct{}{}
	}
	if err := validateExpressionFunctionBodySpec(normalized.Expression); err != nil {
		return err
	}
	if _, ok := w.functionIndex(normalized.Name); ok {
		return &ValidationError{FunctionName: normalized.Name, Source: normalized.Source, Reason: "duplicate function", Err: ErrFunctionValidation}
	}
	if _, ok := w.expressionFunctionIndex(normalized.Name); ok {
		return &ValidationError{FunctionName: normalized.Name, Source: normalized.Source, Reason: "duplicate function", Err: ErrFunctionValidation}
	}
	w.exprFuncs = append(w.exprFuncs, normalized)
	return nil
}

func (w *Workspace) ReplaceFunction(spec PureFunctionSpec) error {
	normalized, err := compilePureFunctionSpec(spec, 0)
	if err != nil {
		return err
	}
	if _, ok := w.expressionFunctionIndex(normalized.name); ok {
		return &ValidationError{
			FunctionName: normalized.name,
			Reason:       "duplicate function",
			Err:          ErrFunctionValidation,
		}
	}
	idx, ok := w.functionIndex(normalized.name)
	if !ok {
		return &ValidationError{
			Reason: "function not found",
			Err:    ErrFunctionValidation,
		}
	}

	w.functions[idx] = clonePureFunctionSpec(spec)
	return nil
}

func (w *Workspace) RemoveFunction(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{
			Reason: "function name is required",
			Err:    ErrFunctionValidation,
		}
	}

	idx, ok := w.functionIndex(name)
	if !ok {
		return &ValidationError{
			Reason: "function not found",
			Err:    ErrFunctionValidation,
		}
	}

	w.functions = append(w.functions[:idx], w.functions[idx+1:]...)
	return nil
}

func (w *Workspace) AddRule(spec RuleSpec) error {
	normalized, err := normalizeRuleSpec(spec)
	if err != nil {
		return err
	}

	identity := normalized.ID
	if identity.IsZero() {
		identity = RuleID(normalized.Name)
	}
	if _, ok := w.ruleIndex(normalized.Name); ok {
		return &ValidationError{
			RuleName: normalized.Name,
			Reason:   "duplicate rule",
		}
	}
	if _, ok := w.ruleIndexByID(identity); ok {
		return &ValidationError{
			RuleName: normalized.Name,
			Reason:   "duplicate rule id",
		}
	}

	normalized.ID = identity
	w.rules = append(w.rules, normalized)
	return nil
}

func (w *Workspace) ReplaceRule(spec RuleSpec) error {
	normalized, err := normalizeRuleSpec(spec)
	if err != nil {
		return err
	}

	idx, ok := w.ruleIndex(normalized.Name)
	if !ok {
		return &ValidationError{
			RuleName: normalized.Name,
			Reason:   "rule not found",
		}
	}

	existing := w.rules[idx]
	if !normalized.ID.IsZero() && normalized.ID != existing.ID {
		return &ValidationError{
			RuleName: normalized.Name,
			Reason:   "rule id is immutable once assigned",
		}
	}

	normalized.ID = existing.ID
	w.rules[idx] = normalized
	return nil
}

func (w *Workspace) RemoveRule(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{
			Reason: "rule name is required",
		}
	}

	idx, ok := w.ruleIndex(name)
	if !ok {
		return &ValidationError{
			RuleName: name,
			Reason:   "rule not found",
		}
	}

	w.rules = append(w.rules[:idx], w.rules[idx+1:]...)
	return nil
}

func (w *Workspace) AddQuery(spec QuerySpec) error {
	normalized := cloneQuerySpec(spec)
	if normalized.Name == "" {
		return &ValidationError{Reason: "query name is required", Err: ErrQueryValidation}
	}
	if _, ok := w.queryIndex(normalized.Name); ok {
		return &ValidationError{RuleName: normalized.Name, Reason: "duplicate query", Err: ErrQueryValidation}
	}
	w.queries = append(w.queries, normalized)
	return nil
}

func (w *Workspace) ReplaceQuery(spec QuerySpec) error {
	normalized := cloneQuerySpec(spec)
	if normalized.Name == "" {
		return &ValidationError{Reason: "query name is required", Err: ErrQueryValidation}
	}
	idx, ok := w.queryIndex(normalized.Name)
	if !ok {
		return &ValidationError{RuleName: normalized.Name, Reason: "query not found", Err: ErrQueryNotFound}
	}
	w.queries[idx] = normalized
	return nil
}

func (w *Workspace) RemoveQuery(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{Reason: "query name is required", Err: ErrQueryValidation}
	}
	idx, ok := w.queryIndex(name)
	if !ok {
		return &ValidationError{RuleName: name, Reason: "query not found", Err: ErrQueryNotFound}
	}
	w.queries = append(w.queries[:idx], w.queries[idx+1:]...)
	return nil
}

func (w *Workspace) Compile(ctx context.Context) (*Ruleset, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	compiledModules, modules, moduleOrder, err := compileWorkspaceModules(w.modules)
	if err != nil {
		return nil, err
	}
	if err := w.validateDefinitionModules(modules); err != nil {
		return nil, err
	}

	compiledTemplates := make([]compiledTemplate, 0, len(w.templates))
	for _, spec := range w.templates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		template, err := compileTemplateSpec(spec)
		if err != nil {
			return nil, err
		}
		compiledTemplates = append(compiledTemplates, template)
	}
	demandTemplates, err := compileBackchainDemandTemplates(compiledTemplates)
	if err != nil {
		return nil, err
	}
	compiledTemplates = append(compiledTemplates, demandTemplates...)

	sort.Slice(compiledTemplates, func(i, j int) bool {
		return compiledTemplates[i].name < compiledTemplates[j].name
	})

	templates := make(map[string]compiledTemplate, len(compiledTemplates))
	templatesByKey := make(map[TemplateKey]compiledTemplate, len(compiledTemplates))
	templatesByQualifiedName := make(map[QualifiedName]compiledTemplate, len(compiledTemplates))
	templatesByID := make([]compiledTemplate, 0, len(compiledTemplates))
	templateIDsByName := make(map[string]templateID, len(compiledTemplates))
	templateIDsByKey := make(map[TemplateKey]templateID, len(compiledTemplates))
	templateOrder := make([]string, 0, len(compiledTemplates))
	for i, template := range compiledTemplates {
		if _, exists := templates[template.name]; exists {
			return nil, &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template",
			}
		}
		if _, exists := templatesByKey[template.key]; exists {
			return nil, &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template key",
			}
		}
		template.id = templateID(i + 1)
		// Compiled templates are immutable, so one isolated clone backs
		// every index of the revision.
		cloned := template.clone()
		templates[template.name] = cloned
		templatesByKey[template.key] = cloned
		templatesByQualifiedName[template.QualifiedName()] = cloned
		templatesByID = append(templatesByID, cloned)
		templateIDsByName[template.name] = template.id
		templateIDsByKey[template.key] = template.id
		templateOrder = append(templateOrder, template.name)
	}
	templateResolver := newTemplateResolver(templatesByKey, templatesByQualifiedName)

	compiledActions := make([]compiledAction, 0, len(w.actions))
	actionsByName := make(map[string]compiledAction, len(w.actions))
	actionOrder := make([]string, 0, len(w.actions))
	for i, spec := range w.actions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		action, err := compileActionSpec(spec, i)
		if err != nil {
			return nil, err
		}
		if _, exists := actionsByName[action.name]; exists {
			return nil, &ValidationError{
				Reason: "duplicate action",
			}
		}
		actionsByName[action.name] = action
		compiledActions = append(compiledActions, action)
		actionOrder = append(actionOrder, action.name)
	}

	builtins := builtinFunctionSpecs()
	reservedBuiltins := make(map[string]struct{}, len(builtins))
	userFunctions := make(map[string]struct{}, len(w.functions)+len(w.exprFuncs))
	compiledFunctions := make([]compiledPureFunction, 0, len(w.functions))
	functionsByName := make(map[string]compiledPureFunction, len(builtins)+len(w.functions)+len(w.exprFuncs))
	functionOrder := make([]string, 0, len(w.functions)+len(w.exprFuncs))
	// Built-ins are always available and cannot be shadowed. They are kept out
	// of compiledFunctions/functionOrder so they stay invisible to ruleset
	// inspection and do not perturb the ruleset identity hash.
	for i, spec := range builtins {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		function, err := compilePureFunctionSpec(spec, -1-i)
		if err != nil {
			return nil, err
		}
		functionsByName[function.name] = function
		reservedBuiltins[function.name] = struct{}{}
	}
	for i, spec := range w.functions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		function, err := compilePureFunctionSpec(spec, i)
		if err != nil {
			return nil, err
		}
		if _, reserved := reservedBuiltins[function.name]; reserved {
			return nil, &ValidationError{
				FunctionName: function.name,
				Reason:       "function shadows a built-in",
				Err:          ErrFunctionValidation,
			}
		}
		if _, exists := userFunctions[function.name]; exists {
			return nil, &ValidationError{
				FunctionName: function.name,
				Reason:       "duplicate function",
				Err:          ErrFunctionValidation,
			}
		}
		userFunctions[function.name] = struct{}{}
		functionsByName[function.name] = function
		compiledFunctions = append(compiledFunctions, function)
		functionOrder = append(functionOrder, function.name)
	}
	for _, spec := range w.exprFuncs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		function, err := compileExpressionFunctionSpec(spec, len(compiledFunctions), functionsByName)
		if err != nil {
			return nil, err
		}
		if _, reserved := reservedBuiltins[function.name]; reserved {
			return nil, &ValidationError{
				FunctionName: function.name,
				Reason:       "function shadows a built-in",
				Err:          ErrFunctionValidation,
			}
		}
		if _, exists := functionsByName[function.name]; exists {
			return nil, &ValidationError{
				FunctionName: function.name,
				Reason:       "duplicate function",
				Err:          ErrFunctionValidation,
			}
		}
		functionsByName[function.name] = function
		compiledFunctions = append(compiledFunctions, function)
		functionOrder = append(functionOrder, function.name)
	}

	compiledGlobals := make([]compiledGlobal, 0, len(w.globals))
	globalsByName := make(map[string]compiledGlobal, len(w.globals))
	globalOrder := make([]string, 0, len(w.globals))
	for i, spec := range w.globals {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		global, err := compileGlobalSpec(spec, i)
		if err != nil {
			return nil, err
		}
		if _, exists := globalsByName[global.name]; exists {
			return nil, &ValidationError{
				Reason: "duplicate global",
			}
		}
		globalsByName[global.name] = global
		compiledGlobals = append(compiledGlobals, global)
		globalOrder = append(globalOrder, global.name)
	}

	compiledRules := make([]compiledRule, 0, len(w.rules))
	rulesByName := make(map[string]compiledRule, len(w.rules))
	rulesByID := make(map[RuleID]compiledRule, len(w.rules))
	rulesByRevisionID := make(map[RuleRevisionID]compiledRule, len(w.rules))
	ruleOrder := make([]string, 0, len(w.rules))
	hasEffectiveAutoFocus := false
	allRulesInMainModule := true
	conditionTemplateKeys := make(map[TemplateKey]struct{})
	conditionNames := make(map[string]struct{})
	queryConditionTemplateKeys := make(map[TemplateKey]struct{})
	queryConditionNames := make(map[string]struct{})
	for i, spec := range w.rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ruleID := spec.ID
		if ruleID.IsZero() {
			ruleID = RuleID(strings.TrimSpace(spec.Name))
		}
		rule, err := compileRuleSpec(spec, ruleID, i, modules, templateResolver, actionsByName, functionsByName, globalsByName)
		if err != nil {
			return nil, err
		}
		if _, exists := rulesByName[rule.name]; exists {
			return nil, &ValidationError{
				RuleName: rule.name,
				Reason:   "duplicate rule",
			}
		}
		if _, exists := rulesByID[rule.id]; exists {
			return nil, &ValidationError{
				RuleName: rule.name,
				Reason:   "duplicate rule id",
			}
		}
		if _, exists := rulesByRevisionID[rule.revisionID]; exists {
			return nil, &ValidationError{
				RuleName: rule.name,
				Reason:   "duplicate rule revision",
			}
		}
		if rule.effectiveAutoFocus {
			hasEffectiveAutoFocus = true
		}
		if rule.module != MainModule {
			allRulesInMainModule = false
		}
		rulesByName[rule.name] = rule
		rulesByID[rule.id] = rule
		rulesByRevisionID[rule.revisionID] = rule
		compiledRules = append(compiledRules, rule)
		ruleOrder = append(ruleOrder, rule.name)
		indexRuleConditionDependencies(rule, conditionTemplateKeys, conditionNames)
	}

	compiledQueries := make([]compiledQuery, 0, len(w.queries))
	queriesByName := make(map[string]compiledQuery, len(w.queries))
	queryOrder := make([]string, 0, len(w.queries))
	for _, spec := range w.queries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		query, err := compileQuerySpec(spec, templateResolver, functionsByName, globalsByName)
		if err != nil {
			return nil, err
		}
		if _, exists := queriesByName[query.name]; exists {
			return nil, &ValidationError{RuleName: query.name, Reason: "duplicate query", Err: ErrQueryValidation}
		}
		queriesByName[query.name] = query
		compiledQueries = append(compiledQueries, query)
		queryOrder = append(queryOrder, query.name)
		indexQueryConditionDependencies(query, queryConditionTemplateKeys, queryConditionNames)
	}

	generatedFactInsertPlans := compileGeneratedFactInsertPlans(templatesByKey, templateIDsByKey, conditionTemplateKeys, conditionNames, queryConditionTemplateKeys, queryConditionNames)
	compiledRules = annotateGeneratedFactInsertPlansOnRules(compiledRules, rulesByName, rulesByID, rulesByRevisionID, generatedFactInsertPlans)

	return &Ruleset{
		id:                         rulesetID(compiledModules, compiledTemplates, compiledActions, compiledFunctions, compiledGlobals, compiledRules, compiledQueries),
		modules:                    modules,
		moduleOrder:                moduleOrder,
		templates:                  templates,
		templatesByKey:             templatesByKey,
		templatesByID:              templatesByID,
		templateIDsByName:          templateIDsByName,
		templateIDsByKey:           templateIDsByKey,
		templateOrder:              templateOrder,
		actions:                    actionsByName,
		actionOrder:                actionOrder,
		functions:                  functionsByName,
		functionOrder:              functionOrder,
		globals:                    globalsByName,
		globalOrder:                globalOrder,
		rules:                      rulesByName,
		rulesByID:                  rulesByID,
		rulesByRevisionID:          rulesByRevisionID,
		ruleOrder:                  ruleOrder,
		queries:                    queriesByName,
		queryOrder:                 queryOrder,
		conditionTemplateKeys:      conditionTemplateKeys,
		conditionNames:             conditionNames,
		queryConditionTemplateKeys: queryConditionTemplateKeys,
		queryConditionNames:        queryConditionNames,
		assertTemplateActionCount:  countAssertTemplateActions(compiledRules),
		generatedAssertReserve:     generatedAssertReserveByRuleRevision(compiledRules),
		hasEffectiveAutoFocus:      hasEffectiveAutoFocus,
		allRulesInMainModule:       allRulesInMainModule,
		generatedFactInsertPlans:   generatedFactInsertPlans,
		graph:                      compileReteGraph(compiledRules, compiledQueries, templatesByKey),
	}, nil
}

type Ruleset struct {
	id                         RulesetID
	modules                    map[ModuleName]Module
	moduleOrder                []ModuleName
	templates                  map[string]compiledTemplate
	templatesByKey             map[TemplateKey]compiledTemplate
	templatesByID              []compiledTemplate
	templateIDsByName          map[string]templateID
	templateIDsByKey           map[TemplateKey]templateID
	templateOrder              []string
	actions                    map[string]compiledAction
	actionOrder                []string
	functions                  map[string]compiledPureFunction
	functionOrder              []string
	globals                    map[string]compiledGlobal
	globalOrder                []string
	rules                      map[string]compiledRule
	rulesByID                  map[RuleID]compiledRule
	rulesByRevisionID          map[RuleRevisionID]compiledRule
	ruleOrder                  []string
	queries                    map[string]compiledQuery
	queryOrder                 []string
	conditionTemplateKeys      map[TemplateKey]struct{}
	conditionNames             map[string]struct{}
	queryConditionTemplateKeys map[TemplateKey]struct{}
	queryConditionNames        map[string]struct{}
	assertTemplateActionCount  int
	generatedAssertReserve     map[RuleRevisionID][]generatedAssertReserve
	hasEffectiveAutoFocus      bool
	allRulesInMainModule       bool
	generatedFactInsertPlans   map[TemplateKey]*compiledGeneratedFactInsertPlan
	graph                      *reteGraph
}

type generatedAssertReserve struct {
	templateKey  TemplateKey
	facts        int
	slots        int
	compactSlots int
}

type GeneratedFactObservabilityKind = gessrules.GeneratedFactObservabilityKind

const (
	GeneratedFactReactiveWorkingMemory = gessrules.GeneratedFactReactiveWorkingMemory
	GeneratedFactQueryVisible          = gessrules.GeneratedFactQueryVisible
	GeneratedFactOutputOnly            = gessrules.GeneratedFactOutputOnly
)

type GeneratedFactObservability = gessrules.GeneratedFactObservability

func (r *Ruleset) hasAutoFocusRules() bool {
	return r != nil && r.hasEffectiveAutoFocus
}

const (
	runFactReservePerRule              = 128
	runFactReservePerAssertTemplateRHS = 256
)

func (r *Ruleset) estimatedRunFactCapacity(initialFacts int) int {
	if initialFacts < 0 {
		initialFacts = 0
	}
	if r == nil || len(r.ruleOrder) == 0 {
		return initialFacts
	}
	reserve := max(len(r.ruleOrder)*runFactReservePerRule, r.assertTemplateActionCount*runFactReservePerAssertTemplateRHS, initialFacts)
	return initialFacts + reserve
}

func countAssertTemplateActions(rules []compiledRule) int {
	count := 0
	for _, rule := range rules {
		for _, action := range rule.actionExecutions {
			if action.kind == compiledRuleActionAssertTemplateValues {
				count++
			}
		}
	}
	return count
}

func generatedAssertReserveByRuleRevision(rules []compiledRule) map[RuleRevisionID][]generatedAssertReserve {
	if len(rules) == 0 {
		return nil
	}
	out := make(map[RuleRevisionID][]generatedAssertReserve, len(rules))
	for _, rule := range rules {
		reserveByTemplate := make(map[TemplateKey]int)
		var reserves []generatedAssertReserve
		for _, action := range rule.actionExecutions {
			if action.kind != compiledRuleActionAssertTemplateValues {
				continue
			}
			plan := action.assertTemplateValues.insertPlan
			if plan.outputOnlyNoRetainEligible() {
				continue
			}
			fields := len(plan.template.fields)
			index, ok := reserveByTemplate[plan.templateKey]
			if !ok {
				index = len(reserves)
				reserveByTemplate[plan.templateKey] = index
				reserves = append(reserves, generatedAssertReserve{templateKey: plan.templateKey})
			}
			reserves[index].facts++
			if plan.compactSlots {
				reserves[index].compactSlots += fields
			} else {
				reserves[index].slots += fields
			}
		}
		if len(reserves) != 0 {
			out[rule.revisionID] = reserves
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func templateSupportsCompactGeneratedSlots(template compiledTemplate) bool {
	if !template.closed || len(template.fields) == 0 {
		return false
	}
	for _, field := range template.fields {
		switch field.Kind {
		case ValueNull, ValueBool, ValueInt, ValueFloat, ValueString:
		default:
			return false
		}
	}
	return true
}

func templateSupportsCompactGeneratedValueSlots(template compiledTemplate) bool {
	if !templateSupportsCompactGeneratedSlots(template) {
		return false
	}
	if len(template.fieldValidation) != len(template.fields) {
		return true
	}
	for _, validation := range template.fieldValidation {
		switch validation.kind {
		case ValueNull, ValueBool, ValueInt, ValueFloat, ValueString:
		default:
			return false
		}
	}
	return true
}

func (r *Ruleset) generatedAssertReserveByRuleRevision() map[RuleRevisionID][]generatedAssertReserve {
	if r == nil {
		return nil
	}
	return r.generatedAssertReserve
}

func compileGeneratedFactInsertPlans(templatesByKey map[TemplateKey]compiledTemplate, templateIDsByKey map[TemplateKey]templateID, conditionTemplateKeys map[TemplateKey]struct{}, conditionNames map[string]struct{}, queryConditionTemplateKeys map[TemplateKey]struct{}, queryConditionNames map[string]struct{}) map[TemplateKey]*compiledGeneratedFactInsertPlan {
	if len(templatesByKey) == 0 {
		return nil
	}
	out := make(map[TemplateKey]*compiledGeneratedFactInsertPlan, len(templatesByKey))
	for key, template := range templatesByKey {
		plan := newCompiledGeneratedFactInsertPlan(template)
		if !plan.valid() {
			continue
		}
		if templateID, ok := templateIDsByKey[key]; ok {
			plan.templateID = templateID
		}
		_, ruleNameTargeted := conditionNames[plan.name]
		_, queryNameTargeted := queryConditionNames[plan.name]
		plan.storeName = ruleNameTargeted || queryNameTargeted
		plan.affectsRuleMatches = targetMayAffectConditions(plan.name, plan.templateKey, conditionTemplateKeys, conditionNames)
		plan.queryVisible = targetMayAffectConditions(plan.name, plan.templateKey, queryConditionTemplateKeys, queryConditionNames)
		plan.affectsRete = plan.affectsRuleMatches || plan.queryVisible
		out[key] = &plan
	}
	return out
}

func annotateGeneratedFactInsertPlansOnRules(rules []compiledRule, byName map[string]compiledRule, byID map[RuleID]compiledRule, byRevisionID map[RuleRevisionID]compiledRule, plans map[TemplateKey]*compiledGeneratedFactInsertPlan) []compiledRule {
	if len(rules) == 0 || len(plans) == 0 {
		return rules
	}
	for i := range rules {
		rule := rules[i]
		changed := false
		for actionIndex := range rule.actionExecutions {
			action := &rule.actionExecutions[actionIndex]
			if action.kind != compiledRuleActionAssertTemplateValues {
				continue
			}
			plan, ok := plans[action.assertTemplateValues.template.Key()]
			if !ok {
				continue
			}
			action.assertTemplateValues.insertPlan.affectsRuleMatches = plan.affectsRuleMatches
			action.assertTemplateValues.insertPlan.queryVisible = plan.queryVisible
			action.assertTemplateValues.insertPlan.affectsRete = plan.affectsRete
			action.assertTemplateValues.insertPlan.storeName = plan.storeName
			changed = true
		}
		if !changed {
			continue
		}
		rules[i] = rule
		byName[rule.name] = rule
		byID[rule.id] = rule
		byRevisionID[rule.revisionID] = rule
	}
	return rules
}

func targetMayAffectConditions(name string, templateKey TemplateKey, conditionTemplateKeys map[TemplateKey]struct{}, conditionNames map[string]struct{}) bool {
	if templateKey != "" {
		if _, ok := conditionTemplateKeys[templateKey]; ok {
			return true
		}
	}
	if name != "" {
		if _, ok := conditionNames[name]; ok {
			return true
		}
	}
	return false
}

func (p *compiledGeneratedFactInsertPlan) generatedFactObservability() GeneratedFactObservability {
	if p == nil {
		return GeneratedFactObservability{}
	}
	out := GeneratedFactObservability{
		TemplateKey:      p.templateKey,
		TemplateName:     p.name,
		RuleMatchVisible: p.affectsRuleMatches,
		QueryVisible:     p.queryVisible,
		StoresName:       p.storeName,
	}
	switch {
	case p.affectsRuleMatches:
		out.Kind = GeneratedFactReactiveWorkingMemory
		out.DiagnosticReasons = append(out.DiagnosticReasons, "generated template is targeted by a rule condition")
	case p.queryVisible:
		out.Kind = GeneratedFactQueryVisible
		out.DiagnosticReasons = append(out.DiagnosticReasons, "generated template is targeted by a query condition")
	default:
		out.Kind = GeneratedFactOutputOnly
		out.DiagnosticReasons = append(out.DiagnosticReasons, "no rule or query condition targets the generated template name or key")
	}
	if p.storeName {
		out.DiagnosticReasons = append(out.DiagnosticReasons, "a name-targeted condition requires retaining the generated fact name")
	}
	return out
}

func (r *Ruleset) generatedFactInsertPlan(templateKey TemplateKey) (*compiledGeneratedFactInsertPlan, bool) {
	if r == nil || templateKey == "" || r.generatedFactInsertPlans == nil {
		return nil, false
	}
	plan, ok := r.generatedFactInsertPlans[templateKey]
	return plan, ok && plan != nil
}

func (r *Ruleset) templateIDByKey(key TemplateKey) (templateID, bool) {
	if r == nil || key == "" || r.templateIDsByKey == nil {
		return 0, false
	}
	id, ok := r.templateIDsByKey[key]
	return id, ok && id != 0
}

func (r *Ruleset) templateIDByName(name string) (templateID, bool) {
	if r == nil || name == "" || r.templateIDsByName == nil {
		return 0, false
	}
	id, ok := r.templateIDsByName[name]
	return id, ok && id != 0
}

func (r *Ruleset) templateByID(id templateID) (compiledTemplate, bool) {
	if ref, ok := r.templateRefByID(id); ok {
		return *ref, true
	}
	return compiledTemplate{}, false
}

// templateRefByID avoids copying the compiled template; the returned
// pointer aliases immutable post-compile state and must not be mutated.
func (r *Ruleset) templateRefByID(id templateID) (*compiledTemplate, bool) {
	if r == nil || id == 0 {
		return nil, false
	}
	index := int(id) - 1
	if index < 0 || index >= len(r.templatesByID) {
		return nil, false
	}
	return &r.templatesByID[index], true
}

func (r *Ruleset) estimatedRunSlotCapacity(factCapacity int) int {
	if r == nil || factCapacity <= 0 {
		return 0
	}
	maxSlots := 0
	for _, name := range r.templateOrder {
		template := r.templates[name]
		if template.closed && len(template.fields) > maxSlots {
			maxSlots = len(template.fields)
		}
	}
	if maxSlots == 0 {
		return factCapacity
	}
	return factCapacity * maxSlots
}

func nextGeneratedFactCapacity(length, capacity int, revision *Ruleset) int {
	if length < 0 {
		length = 0
	}
	if capacity < length {
		capacity = length
	}
	minGrowth := runFactReservePerRule
	if revision != nil && len(revision.ruleOrder) > 0 {
		minGrowth = max(minGrowth, len(revision.ruleOrder)*4)
	}
	growth := max(capacity/8, minGrowth)
	next := max(capacity+growth, length+minGrowth)
	if next == 0 {
		next = minGrowth
	}
	return next
}

func nextGeneratedSlotCapacity(length, capacity, needed int, revision *Ruleset) int {
	if length < 0 {
		length = 0
	}
	if capacity < length {
		capacity = length
	}
	if needed < 1 {
		needed = 1
	}
	minGrowth := needed * runFactReservePerRule
	if revision != nil {
		if slots := revision.estimatedRunSlotCapacity(runFactReservePerRule); slots > minGrowth {
			minGrowth = slots
		}
	}
	next := max(max(capacity*2, length+minGrowth), length+needed)
	return next
}

func (r *Ruleset) ID() RulesetID {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *Ruleset) Module(name ModuleName) (Module, bool) {
	if r == nil {
		return Module{}, false
	}
	module, ok := r.modules[ModuleName(strings.TrimSpace(string(name)))]
	if !ok {
		return Module{}, false
	}
	return cloneModule(module), true
}

func (r *Ruleset) Modules() []Module {
	if r == nil {
		return nil
	}
	out := make([]Module, 0, len(r.moduleOrder))
	for _, name := range r.moduleOrder {
		out = append(out, cloneModule(r.modules[name]))
	}
	return out
}

func (r *Ruleset) compiledTemplate(name string) (compiledTemplate, bool) {
	if r == nil {
		return compiledTemplate{}, false
	}
	template, ok := r.templates[name]
	if !ok {
		return compiledTemplate{}, false
	}
	return template.clone(), true
}

func (r *Ruleset) Template(name string) (Template, bool) {
	template, ok := r.compiledTemplate(name)
	if !ok {
		return Template{}, false
	}
	return template.inspect(), true
}

func (r *Ruleset) TemplateByKey(key TemplateKey) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	template, ok := r.templatesByKey[key]
	if !ok {
		return Template{}, false
	}
	return template.inspect(), true
}

func (r *Ruleset) templateByKey(key TemplateKey) (compiledTemplate, bool) {
	if r == nil {
		return compiledTemplate{}, false
	}
	template, ok := r.templatesByKey[key]
	return template, ok
}

func (r *Ruleset) usesFieldSlots(template compiledTemplate) bool {
	if r == nil || !template.closed || template.key == "" {
		return false
	}
	_, ok := r.conditionTemplateKeys[template.key]
	return ok
}

func (r *Ruleset) buildFieldSlots(template compiledTemplate, fields Fields, presence map[string]FieldPresence) []factSlot {
	if r == nil || template.key == "" {
		return nil
	}
	if !r.usesFieldSlots(template) {
		return nil
	}
	return template.buildFieldSlots(fields, presence)
}

func (r *Ruleset) Templates() []Template {
	if r == nil {
		return nil
	}
	out := make([]Template, 0, len(r.templateOrder))
	for _, name := range r.templateOrder {
		out = append(out, r.templates[name].inspect())
	}
	return out
}

func (r *Ruleset) Action(name string) (Action, bool) {
	if r == nil {
		return Action{}, false
	}
	action, ok := r.actions[name]
	if !ok {
		return Action{}, false
	}
	return cloneAction(action.inspect()), true
}

func (r *Ruleset) Actions() []Action {
	if r == nil {
		return nil
	}
	out := make([]Action, 0, len(r.actionOrder))
	for _, name := range r.actionOrder {
		out = append(out, cloneAction(r.actions[name].inspect()))
	}
	return out
}

func (r *Ruleset) Function(name string) (PureFunctionDefinition, bool) {
	if r == nil {
		return PureFunctionDefinition{}, false
	}
	function, ok := r.functions[strings.TrimSpace(name)]
	if !ok || function.order < 0 {
		// Built-ins carry a negative order and are implicit rather than
		// declared, so they are not exposed through inspection, matching
		// Functions().
		return PureFunctionDefinition{}, false
	}
	return function.inspect(), true
}

func (r *Ruleset) Functions() []PureFunctionDefinition {
	if r == nil {
		return nil
	}
	out := make([]PureFunctionDefinition, 0, len(r.functionOrder))
	for _, name := range r.functionOrder {
		out = append(out, r.functions[name].inspect())
	}
	return out
}

func (r *Ruleset) Global(name string) (Global, bool) {
	if r == nil {
		return Global{}, false
	}
	global, ok := r.globals[strings.TrimSpace(name)]
	if !ok {
		return Global{}, false
	}
	return global.public(), true
}

func (r *Ruleset) Globals() []Global {
	if r == nil {
		return nil
	}
	out := make([]Global, 0, len(r.globalOrder))
	for _, name := range r.globalOrder {
		out = append(out, r.globals[name].public())
	}
	return out
}

func (r *Ruleset) Rule(name string) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	rule, ok := r.rules[name]
	if !ok {
		return Rule{}, false
	}
	return cloneRule(rule.inspect()), true
}

func (r *Ruleset) RuleByID(id RuleID) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	rule, ok := r.rulesByID[id]
	if !ok {
		return Rule{}, false
	}
	return cloneRule(rule.inspect()), true
}

func (r *Ruleset) RuleByRevisionID(id RuleRevisionID) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	rule, ok := r.rulesByRevisionID[id]
	if !ok {
		return Rule{}, false
	}
	return cloneRule(rule.inspect()), true
}

func (r *Ruleset) Rules() []Rule {
	if r == nil {
		return nil
	}
	out := make([]Rule, 0, len(r.ruleOrder))
	for _, name := range r.ruleOrder {
		out = append(out, cloneRule(r.rules[name].inspect()))
	}
	return out
}

func (r *Ruleset) Query(name string) (Query, bool) {
	query, ok := r.query(name)
	if !ok {
		return Query{}, false
	}
	return query.inspect(), true
}

func (r *Ruleset) query(name string) (compiledQuery, bool) {
	if r == nil {
		return compiledQuery{}, false
	}
	query, ok := r.queries[strings.TrimSpace(name)]
	return query, ok
}

func (r *Ruleset) Queries() []Query {
	if r == nil {
		return nil
	}
	out := make([]Query, 0, len(r.queryOrder))
	for _, name := range r.queryOrder {
		out = append(out, r.queries[name].inspect())
	}
	return out
}

// GeneratedFactObservability returns the compiler visibility proof for a
// generated template.
func (r *Ruleset) GeneratedFactObservability(templateKey TemplateKey) (GeneratedFactObservability, bool) {
	plan, ok := r.generatedFactInsertPlan(templateKey)
	if !ok {
		return GeneratedFactObservability{}, false
	}
	return gessrules.CloneGeneratedFactObservability(plan.generatedFactObservability()), true
}

// GeneratedFactObservabilityDiagnostics returns compiler visibility proofs for
// all generated template insert plans.
func (r *Ruleset) GeneratedFactObservabilityDiagnostics() []GeneratedFactObservability {
	if r == nil || len(r.generatedFactInsertPlans) == 0 {
		return nil
	}
	out := make([]GeneratedFactObservability, 0, len(r.generatedFactInsertPlans))
	seen := make(map[TemplateKey]struct{}, len(r.generatedFactInsertPlans))
	for _, name := range r.templateOrder {
		template := r.templates[name]
		plan, ok := r.generatedFactInsertPlan(template.Key())
		if !ok {
			continue
		}
		out = append(out, gessrules.CloneGeneratedFactObservability(plan.generatedFactObservability()))
		seen[template.Key()] = struct{}{}
	}
	if len(seen) == len(r.generatedFactInsertPlans) {
		return out
	}
	keys := make([]string, 0, len(r.generatedFactInsertPlans)-len(seen))
	byKey := make(map[string]TemplateKey, len(r.generatedFactInsertPlans)-len(seen))
	for key := range r.generatedFactInsertPlans {
		if _, ok := seen[key]; ok {
			continue
		}
		stringKey := key.String()
		keys = append(keys, stringKey)
		byKey[stringKey] = key
	}
	sort.Strings(keys)
	for _, stringKey := range keys {
		plan, ok := r.generatedFactInsertPlan(byKey[stringKey])
		if !ok {
			continue
		}
		out = append(out, gessrules.CloneGeneratedFactObservability(plan.generatedFactObservability()))
	}
	return out
}

func indexRuleConditionDependencies(rule compiledRule, templateKeys map[TemplateKey]struct{}, names map[string]struct{}) {
	for _, branch := range rule.executionConditionBranches() {
		for _, plan := range branch.plans {
			indexConditionTargetDependency(plan.target, templateKeys, names)
			if plan.aggregate != nil {
				for _, input := range plan.aggregate.inputPlans {
					indexConditionTargetDependency(input.target, templateKeys, names)
				}
			}
		}
	}
}

func indexQueryConditionDependencies(query compiledQuery, templateKeys map[TemplateKey]struct{}, names map[string]struct{}) {
	for _, branch := range query.conditionBranches {
		for _, plan := range branch.plans {
			indexConditionTargetDependency(plan.target, templateKeys, names)
			if plan.aggregate != nil {
				for _, input := range plan.aggregate.inputPlans {
					indexConditionTargetDependency(input.target, templateKeys, names)
				}
			}
		}
	}
}

func indexConditionTargetDependency(target conditionTarget, templateKeys map[TemplateKey]struct{}, names map[string]struct{}) {
	switch target.kind {
	case conditionTargetTemplateKey:
		if target.templateKey != "" {
			templateKeys[target.templateKey] = struct{}{}
		}
	case conditionTargetName:
		if target.name != "" {
			names[target.name] = struct{}{}
		}
	}
}

func (r *Ruleset) factMayAffectRuleMatches(fact FactSnapshot) bool {
	return r.factMayAffectRuleMatchesByTarget(fact.name, fact.templateKey)
}

func (r *Ruleset) factMayAffectRuleMatchesByTarget(name string, templateKey TemplateKey) bool {
	if r == nil {
		return true
	}
	if templateKey != "" {
		if _, ok := r.conditionTemplateKeys[templateKey]; ok {
			return true
		}
	}
	if name != "" {
		if _, ok := r.conditionNames[name]; ok {
			return true
		}
	}
	return false
}

func (r *Ruleset) factMayAffectReteByTarget(name string, templateKey TemplateKey) bool {
	if r == nil {
		return true
	}
	if r.factMayAffectRuleMatchesByTarget(name, templateKey) {
		return true
	}
	if templateKey != "" {
		if _, ok := r.queryConditionTemplateKeys[templateKey]; ok {
			return true
		}
	}
	if name != "" {
		if _, ok := r.queryConditionNames[name]; ok {
			return true
		}
	}
	return false
}

func (w *Workspace) actionIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, action := range w.actions {
		if action.Name == name {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) functionIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, function := range w.functions {
		if strings.TrimSpace(function.Name) == name {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) expressionFunctionIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, function := range w.exprFuncs {
		if strings.TrimSpace(function.Name) == name {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) globalIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, global := range w.globals {
		if strings.TrimSpace(global.Name) == name {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) ruleIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, rule := range w.rules {
		if rule.Name == name {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) ruleIndexByID(id RuleID) (int, bool) {
	for i, rule := range w.rules {
		if rule.ID == id {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) queryIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, query := range w.queries {
		if strings.TrimSpace(query.Name) == name {
			return i, true
		}
	}
	return -1, false
}

func (w *Workspace) validateDefinitionModules(modules map[ModuleName]Module) error {
	for _, spec := range w.templates {
		module, ok := validateModuleReference(modules, spec.Module)
		if !ok {
			return &ValidationError{
				TemplateName: strings.TrimSpace(spec.Name),
				Reason:       fmt.Sprintf("unknown module %q authored in module %q", module, module),
			}
		}
	}
	for _, spec := range w.rules {
		module, ok := validateModuleReference(modules, spec.Module)
		if !ok {
			return &ValidationError{
				RuleName: strings.TrimSpace(spec.Name),
				Reason:   fmt.Sprintf("unknown module %q authored in module %q", module, module),
			}
		}
	}
	for _, spec := range w.queries {
		module, ok := validateModuleReference(modules, spec.Module)
		if !ok {
			return &ValidationError{
				RuleName: strings.TrimSpace(spec.Name),
				Reason:   fmt.Sprintf("unknown module %q authored in module %q", module, module),
				Err:      ErrQueryValidation,
			}
		}
	}
	return nil
}

func rulesetID(modules []Module, templates []compiledTemplate, actions []compiledAction, functions []compiledPureFunction, globals []compiledGlobal, rules []compiledRule, queries []compiledQuery) RulesetID {
	sum := sha256.New()
	sum.Write([]byte("gess/ruleset/v2\n"))
	sum.Write([]byte("modules:\n"))
	for _, module := range modules {
		sum.Write(fmt.Appendf(nil, "module:%s:", module.NameValue))
		sum.Write([]byte(module.DescriptionText))
		if autoFocus, ok := module.AutoFocusDefault(); ok {
			sum.Write(fmt.Appendf(nil, ":auto-focus:%t", autoFocus))
		}
		sum.Write([]byte("\n"))
	}

	for _, template := range templates {
		sum.Write(fmt.Appendf(nil, "template:%s:%s:%s:%s:%d:%t\n", template.module, template.name, template.key, template.compatibilityKey, template.duplicatePolicy, template.closed))
		if template.backchainReactive || template.backchainDemandKey != "" || template.backchainDemand || template.backchainSourceKey != "" {
			sum.Write(fmt.Appendf(nil, "backchain:%t:%s:%t:%s\n", template.backchainReactive, template.backchainDemandKey, template.backchainDemand, template.backchainSourceKey))
		}
		sum.Write(fmt.Appendf(nil, "dup:%d:", template.duplicatePolicy))
		sum.Write(fmt.Appendf(nil, "%d\n", len(template.duplicateKeyNames)))
		for _, fieldName := range template.duplicateKeyNames {
			sum.Write(fmt.Appendf(nil, "dupkey:%s\n", fieldName))
		}
		for _, field := range template.fields {
			sum.Write(fmt.Appendf(nil, "field:%s:%s:%t", field.Name, field.Kind, field.Required))
			if fieldDefault, hasDefault := template.fieldDefaults[field.Name]; hasDefault {
				sum.Write(fmt.Appendf(nil, ":default:%s", fieldDefault.CanonicalKey()))
			}
			sum.Write([]byte("\n"))
			if allowed, hasAllowed := template.fieldAllowed[field.Name]; hasAllowed {
				sum.Write(fmt.Appendf(nil, "allowed:%s:", field.Name))
				for _, allowedValue := range allowed {
					sum.Write([]byte(allowedValue.CanonicalKey()))
					sum.Write([]byte(","))
				}
				sum.Write([]byte("\n"))
			}
		}
	}

	sum.Write([]byte("actions:\n"))
	for _, action := range actions {
		sum.Write([]byte("action:"))
		sum.Write([]byte(serializeCompiledActionDeclaration(action)))
		sum.Write([]byte("\n"))
	}

	sum.Write([]byte("functions:\n"))
	for _, function := range functions {
		sum.Write(fmt.Appendf(nil, "function:%s:%d:%s:", function.name, function.order, function.ret))
		for _, arg := range function.args {
			sum.Write([]byte(arg.String()))
			sum.Write([]byte(","))
		}
		if function.expressionBacked {
			sum.Write([]byte(":expression:"))
			sum.Write([]byte(function.description))
			sum.Write([]byte(":params:"))
			for _, param := range function.paramNames {
				sum.Write([]byte(param))
				sum.Write([]byte(","))
			}
			sum.Write([]byte(":body:"))
			sum.Write([]byte(renderExpression(function.expressionSpec)))
		}
		sum.Write([]byte("\n"))
	}

	sum.Write([]byte("globals:\n"))
	for _, global := range globals {
		sum.Write(fmt.Appendf(nil, "global:%s:%d:%s:%t:", global.name, global.slot, global.kind, global.hasDefault))
		if global.hasDefault {
			sum.Write([]byte(global.defaultValue.CanonicalKey()))
		}
		sum.Write([]byte("\n"))
	}

	sum.Write([]byte("rules:\n"))
	for _, rule := range rules {
		sum.Write(fmt.Appendf(nil, "rule:%s:%s:%s:%s:%d:%d\n", rule.module, rule.id, rule.revisionID, rule.name, rule.salience, rule.declarationOrder))
		sum.Write(fmt.Appendf(nil, "auto-focus:%t:%t:%t\n", rule.hasAutoFocus, rule.autoFocus, rule.effectiveAutoFocus))
		sum.Write([]byte("description:"))
		sum.Write([]byte(rule.description))
		sum.Write([]byte("\n"))
		sum.Write(fmt.Appendf(nil, "tags:%d:", len(rule.tags)))
		for _, tag := range rule.tags {
			sum.Write([]byte(tag))
			sum.Write([]byte(","))
		}
		sum.Write([]byte("\n"))
		sum.Write(fmt.Appendf(nil, "condition-count:%d\n", len(rule.conditions)))
		for _, condition := range rule.conditions {
			sum.Write(fmt.Appendf(nil, "condition:%d:%s:%s:%s\n", condition.Order, condition.BindingName, condition.NameValue, condition.TemplateKeyValue))
		}
		sum.Write(fmt.Appendf(nil, "action-count:%d\n", len(rule.actions)))
		for _, action := range rule.actions {
			sum.Write(fmt.Appendf(nil, "action:%d:%s\n", action.Order, action.NameValue))
		}
		for _, action := range rule.actionExecutions {
			sum.Write([]byte("action-exec:"))
			sum.Write([]byte(serializeCompiledRuleAction(action)))
			sum.Write([]byte("\n"))
		}
	}

	sum.Write([]byte("queries:\n"))
	for _, query := range queries {
		sum.Write(fmt.Appendf(nil, "query:%s:%s:%d\n", query.module, query.name, len(query.parameters)))
		for _, param := range query.parameters {
			sum.Write(fmt.Appendf(nil, "param:%d:%s:%s\n", param.Order, param.NameValue, param.KindValue))
		}
		sum.Write(fmt.Appendf(nil, "condition-count:%d\n", len(query.conditions)))
		for _, condition := range query.conditions {
			sum.Write(fmt.Appendf(nil, "condition:%d:%s:%s:%s:%s\n", condition.Order, condition.IDValue, condition.BindingName, condition.NameValue, condition.TemplateKeyValue))
		}
		for branchIndex, branch := range query.conditionBranches {
			sum.Write(fmt.Appendf(nil, "branch:%d:\n", branchIndex))
			for _, plan := range branch.plans {
				sum.Write(fmt.Appendf(nil, "plan:%s:%s:%d:%t:%s:%s:", plan.id, plan.binding, plan.bindingSlot, plan.negated, plan.target.name, plan.target.templateKey))
				sum.Write([]byte(serializeCompiledFieldConstraints(plan.constraints)))
				sum.Write([]byte("|"))
				sum.Write([]byte(serializeCompiledListPatterns(plan.listPatterns)))
				sum.Write([]byte("|"))
				sum.Write([]byte(serializeCompiledJoinConstraints(plan.joins)))
				sum.Write([]byte("|"))
				sum.Write([]byte(serializeCompiledExpressionPredicates(plan.predicates)))
				sum.Write([]byte("\n"))
			}
		}
		for _, ret := range query.returns {
			sum.Write(fmt.Appendf(nil, "return:%d:%s:%t:%s:%s\n", ret.order, ret.alias, ret.fact, ret.binding, serializeCompiledExpression(ret.expression)))
		}
	}

	return RulesetID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}
