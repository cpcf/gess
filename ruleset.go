package gess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type Workspace struct {
	templates []TemplateSpec
	actions   []ActionSpec
	rules     []RuleSpec
}

func NewWorkspace() *Workspace {
	return &Workspace{}
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

func (w *Workspace) Compile(ctx context.Context) (*Ruleset, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	compiledTemplates := make([]Template, 0, len(w.templates))
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

	sort.Slice(compiledTemplates, func(i, j int) bool {
		return compiledTemplates[i].name < compiledTemplates[j].name
	})

	templates := make(map[string]Template, len(compiledTemplates))
	templatesByKey := make(map[TemplateKey]Template, len(compiledTemplates))
	templateOrder := make([]string, 0, len(compiledTemplates))
	for _, template := range compiledTemplates {
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
		templates[template.name] = template.clone()
		templatesByKey[template.key] = template.clone()
		templateOrder = append(templateOrder, template.name)
	}

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

	compiledRules := make([]compiledRule, 0, len(w.rules))
	rulesByName := make(map[string]compiledRule, len(w.rules))
	rulesByID := make(map[RuleID]compiledRule, len(w.rules))
	rulesByRevisionID := make(map[RuleRevisionID]compiledRule, len(w.rules))
	ruleOrder := make([]string, 0, len(w.rules))
	conditionTemplateKeys := make(map[TemplateKey]struct{})
	conditionNames := make(map[string]struct{})
	for i, spec := range w.rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ruleID := spec.ID
		if ruleID.IsZero() {
			ruleID = RuleID(strings.TrimSpace(spec.Name))
		}
		rule, err := compileRuleSpec(spec, ruleID, i, templatesByKey, actionsByName)
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
		rulesByName[rule.name] = rule
		rulesByID[rule.id] = rule
		rulesByRevisionID[rule.revisionID] = rule
		compiledRules = append(compiledRules, rule)
		ruleOrder = append(ruleOrder, rule.name)
		indexRuleConditionDependencies(rule, conditionTemplateKeys, conditionNames)
	}

	return &Ruleset{
		id:                    rulesetID(compiledTemplates, compiledActions, compiledRules),
		templates:             templates,
		templatesByKey:        templatesByKey,
		templateOrder:         templateOrder,
		actions:               actionsByName,
		actionOrder:           actionOrder,
		rules:                 rulesByName,
		rulesByID:             rulesByID,
		rulesByRevisionID:     rulesByRevisionID,
		ruleOrder:             ruleOrder,
		conditionTemplateKeys: conditionTemplateKeys,
		conditionNames:        conditionNames,
		graph:                 compileReteGraph(compiledRules, templatesByKey),
	}, nil
}

type Ruleset struct {
	id                    RulesetID
	templates             map[string]Template
	templatesByKey        map[TemplateKey]Template
	templateOrder         []string
	actions               map[string]compiledAction
	actionOrder           []string
	rules                 map[string]compiledRule
	rulesByID             map[RuleID]compiledRule
	rulesByRevisionID     map[RuleRevisionID]compiledRule
	ruleOrder             []string
	conditionTemplateKeys map[TemplateKey]struct{}
	conditionNames        map[string]struct{}
	graph                 *reteGraph
}

const runFactReservePerRule = 64

func (r *Ruleset) estimatedRunFactCapacity(initialFacts int) int {
	if initialFacts < 0 {
		initialFacts = 0
	}
	if r == nil || len(r.ruleOrder) == 0 {
		return initialFacts
	}
	reserve := max(len(r.ruleOrder)*runFactReservePerRule, initialFacts)
	return initialFacts + reserve
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
	next := max(capacity*2, length+minGrowth)
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

func (r *Ruleset) Template(name string) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	template, ok := r.templates[name]
	if !ok {
		return Template{}, false
	}
	return template.clone(), true
}

func (r *Ruleset) TemplateByKey(key TemplateKey) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	template, ok := r.templatesByKey[key]
	if !ok {
		return Template{}, false
	}
	return template.clone(), true
}

func (r *Ruleset) templateByKey(key TemplateKey) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	template, ok := r.templatesByKey[key]
	return template, ok
}

func (r *Ruleset) usesFieldSlots(template Template) bool {
	if r == nil || !template.closed || template.key == "" {
		return false
	}
	_, ok := r.conditionTemplateKeys[template.key]
	return ok
}

func (r *Ruleset) buildFieldSlots(template Template, fields Fields, presence map[string]FieldPresence) []factSlot {
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
		out = append(out, r.templates[name].clone())
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
	return action.inspect().clone(), true
}

func (r *Ruleset) Actions() []Action {
	if r == nil {
		return nil
	}
	out := make([]Action, 0, len(r.actionOrder))
	for _, name := range r.actionOrder {
		out = append(out, r.actions[name].inspect().clone())
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
	return rule.inspect().clone(), true
}

func (r *Ruleset) RuleByID(id RuleID) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	rule, ok := r.rulesByID[id]
	if !ok {
		return Rule{}, false
	}
	return rule.inspect().clone(), true
}

func (r *Ruleset) RuleByRevisionID(id RuleRevisionID) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	rule, ok := r.rulesByRevisionID[id]
	if !ok {
		return Rule{}, false
	}
	return rule.inspect().clone(), true
}

func (r *Ruleset) Rules() []Rule {
	if r == nil {
		return nil
	}
	out := make([]Rule, 0, len(r.ruleOrder))
	for _, name := range r.ruleOrder {
		out = append(out, r.rules[name].inspect().clone())
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

func (w *Workspace) actionIndex(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, action := range w.actions {
		if action.Name == name {
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

func rulesetID(templates []Template, actions []compiledAction, rules []compiledRule) RulesetID {
	sum := sha256.New()
	sum.Write([]byte("gess/ruleset/v2\n"))
	for _, template := range templates {
		sum.Write(fmt.Appendf(nil, "template:%s:%s:%s:%d:%t\n", template.name, template.key, template.compatibilityKey, template.duplicatePolicy, template.closed))
		sum.Write(fmt.Appendf(nil, "dup:%d:", template.duplicatePolicy))
		sum.Write(fmt.Appendf(nil, "%d\n", len(template.duplicateKeyNames)))
		for _, fieldName := range template.duplicateKeyNames {
			sum.Write(fmt.Appendf(nil, "dupkey:%s\n", fieldName))
		}
		for _, field := range template.fields {
			sum.Write(fmt.Appendf(nil, "field:%s:%s:%t", field.Name, field.Kind, field.Required))
			if fieldDefault, hasDefault := template.fieldDefaults[field.Name]; hasDefault {
				sum.Write(fmt.Appendf(nil, ":default:%s", fieldDefault.canonicalKey()))
			}
			sum.Write([]byte("\n"))
			if allowed, hasAllowed := template.fieldAllowed[field.Name]; hasAllowed {
				sum.Write(fmt.Appendf(nil, "allowed:%s:", field.Name))
				for _, allowedValue := range allowed {
					sum.Write([]byte(allowedValue.canonicalKey()))
					sum.Write([]byte(","))
				}
				sum.Write([]byte("\n"))
			}
		}
	}

	sum.Write([]byte("actions:\n"))
	for _, action := range actions {
		sum.Write(fmt.Appendf(nil, "action:%s:%d:%t\n", action.name, action.order, action.skipBindingFreeze))
	}

	sum.Write([]byte("rules:\n"))
	for _, rule := range rules {
		sum.Write(fmt.Appendf(nil, "rule:%s:%s:%s:%d:%d\n", rule.id, rule.revisionID, rule.name, rule.salience, rule.declarationOrder))
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
			sum.Write(fmt.Appendf(nil, "condition:%d:%s:%s:%s\n", condition.order, condition.binding, condition.name, condition.templateKey))
		}
		sum.Write(fmt.Appendf(nil, "action-count:%d\n", len(rule.actions)))
		for _, action := range rule.actions {
			sum.Write(fmt.Appendf(nil, "action:%d:%s\n", action.order, action.name))
		}
	}

	return RulesetID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}
