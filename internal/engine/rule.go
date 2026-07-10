package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type RuleConditionSpec = gessrules.RuleConditionSpec

func cloneRuleConditionSpec(s RuleConditionSpec) RuleConditionSpec {
	return gessrules.CloneRuleConditionSpec(s)
}

type FactTargetKind = gessrules.FactTargetKind
type FactTarget = gessrules.FactTarget

const (
	FactTargetUnknown     = gessrules.FactTargetUnknown
	FactTargetDynamic     = gessrules.FactTargetDynamic
	FactTargetTemplate    = gessrules.FactTargetTemplate
	FactTargetTemplateKey = gessrules.FactTargetTemplateKey
)

func DynamicFact(name string) FactTarget {
	return gessrules.DynamicFact(name)
}

func DynamicFactIn(module ModuleName, name string) FactTarget {
	return gessrules.DynamicFactIn(module, name)
}

func TemplateFact(name string) FactTarget {
	return gessrules.TemplateFact(name)
}

func TemplateFactIn(module ModuleName, name string) FactTarget {
	return gessrules.TemplateFactIn(module, name)
}

func TemplateKeyFact(key TemplateKey) FactTarget {
	return gessrules.TemplateKeyFact(key)
}

type templateResolver struct {
	byKey  map[TemplateKey]compiledTemplate
	byName map[QualifiedName]compiledTemplate
}

func newTemplateResolver(byKey map[TemplateKey]compiledTemplate, byName map[QualifiedName]compiledTemplate) templateResolver {
	return templateResolver{byKey: byKey, byName: byName}
}

func (r templateResolver) templateByKey(key TemplateKey) (compiledTemplate, bool) {
	template, ok := r.byKey[key]
	return template, ok
}

func (r templateResolver) resolveFactTarget(ruleName string, conditionIndex int, author ModuleName, target FactTarget) (conditionTarget, conditionIndexKind, *compiledTemplate, string, TemplateKey, error) {
	target = target.Normalized()
	switch target.Kind() {
	case FactTargetDynamic:
		targetRef := target.Ref()
		ref := normalizeNameRef(targetRef, author)
		if ref.Name == "" {
			return conditionTarget{}, conditionIndexUnknown, nil, "", "", &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				Reason:            "dynamic fact target name is required",
			}
		}
		name := ref.Name
		if targetRef.Module != "" {
			name = ref.Module.String() + "::" + ref.Name
		}
		return conditionTarget{kind: conditionTargetName, name: name}, conditionIndexName, nil, name, "", nil
	case FactTargetTemplate:
		ref := normalizeNameRef(target.Ref(), author)
		if ref.Name == "" {
			return conditionTarget{}, conditionIndexUnknown, nil, "", "", &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				Reason:            "template target name is required",
			}
		}
		template, ok := r.byName[ref]
		if !ok {
			return conditionTarget{}, conditionIndexUnknown, nil, "", "", &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				Reason:            fmt.Sprintf("unknown template reference %q authored in module %q", ref.String(), normalizeModuleName(author).String()),
			}
		}
		return conditionTarget{kind: conditionTargetTemplateKey, templateKey: template.key, templateID: template.id}, conditionIndexTemplateKey, &template, "", template.key, nil
	case FactTargetTemplateKey:
		templateKey := target.TemplateKey()
		if templateKey == "" {
			return conditionTarget{}, conditionIndexUnknown, nil, "", "", &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				Reason:            "template key target is required",
			}
		}
		template, ok := r.byKey[templateKey]
		if !ok {
			return conditionTarget{}, conditionIndexUnknown, nil, "", "", &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				Reason:            "unknown template key",
			}
		}
		return conditionTarget{kind: conditionTargetTemplateKey, templateKey: templateKey, templateID: template.id}, conditionIndexTemplateKey, &template, "", templateKey, nil
	default:
		return conditionTarget{}, conditionIndexUnknown, nil, "", "", &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			Reason:            "condition target is required",
		}
	}
}

type ConditionSpec = gessrules.ConditionSpec
type And = gessrules.And
type Or = gessrules.Or
type Not = gessrules.Not
type Explicit = gessrules.Explicit
type ExistsCondition = gessrules.ExistsCondition
type ForallCondition = gessrules.ForallCondition
type Test = gessrules.Test
type Match = gessrules.Match

func Exists(condition ConditionSpec) ExistsCondition {
	return gessrules.Exists(condition)
}

func Forall(domain ConditionSpec, requirement ConditionSpec) ForallCondition {
	return gessrules.Forall(domain, requirement)
}

func cloneConditionSpec(spec ConditionSpec) ConditionSpec {
	return gessrules.CloneConditionSpec(spec)
}

type RuleActionSpec = gessrules.RuleActionSpec

func cloneRuleActionSpec(s RuleActionSpec) RuleActionSpec {
	return gessrules.CloneRuleActionSpec(s)
}

type RuleSpec = gessrules.RuleSpec

func cloneRuleSpec(s RuleSpec) RuleSpec {
	return gessrules.CloneRuleSpec(s)
}

type RuleCondition = gessrules.RuleCondition

type RuleConditionBranch = gessrules.RuleConditionBranch

type RuleConditionBranchCondition = gessrules.RuleConditionBranchCondition

type ConditionTreeKind = gessrules.ConditionTreeKind

const (
	ConditionTreeKindUnknown    = gessrules.ConditionTreeKindUnknown
	ConditionTreeKindAnd        = gessrules.ConditionTreeKindAnd
	ConditionTreeKindMatch      = gessrules.ConditionTreeKindMatch
	ConditionTreeKindTest       = gessrules.ConditionTreeKindTest
	ConditionTreeKindNot        = gessrules.ConditionTreeKindNot
	ConditionTreeKindOr         = gessrules.ConditionTreeKindOr
	ConditionTreeKindExists     = gessrules.ConditionTreeKindExists
	ConditionTreeKindForall     = gessrules.ConditionTreeKindForall
	ConditionTreeKindAccumulate = gessrules.ConditionTreeKindAccumulate
)

type RuleConditionTree = gessrules.RuleConditionTree

type RuleAction = gessrules.RuleAction

func cloneRuleAction(a RuleAction) RuleAction {
	return gessrules.CloneRuleAction(a)
}

func cloneRuleActions(actions []RuleAction) []RuleAction {
	return gessrules.CloneRuleActions(actions)
}

type Rule = gessrules.Rule

func cloneRule(rule Rule) Rule {
	return gessrules.CloneRule(rule)
}

type compiledRule struct {
	id                          RuleID
	revisionID                  RuleRevisionID
	name                        string
	module                      ModuleName
	description                 string
	tags                        []string
	salience                    int
	autoFocus                   bool
	hasAutoFocus                bool
	effectiveAutoFocus          bool
	declarationOrder            int
	source                      SourceSpan
	gessSource                  string
	identityScopeHash           uint64
	conditions                  []RuleCondition
	treeConditions              []RuleCondition
	conditionTree               RuleConditionTree
	conditionTreeShape          compiledConditionTreeShape
	conditionPlans              []compiledConditionPlan
	conditionBranches           []compiledConditionBranch
	conditionBranchPlans        []RuleConditionBranch
	actions                     []RuleAction
	actionExecutions            []compiledRuleAction
	allActionsSkipBindingFreeze bool
}

func (r compiledRule) inspect() Rule {
	return Rule{
		IDValue:                r.id,
		RevisionIDValue:        r.revisionID,
		NameValue:              r.name,
		ModuleValue:            r.module,
		DescriptionText:        r.description,
		TagValues:              append([]string(nil), r.tags...),
		SalienceValue:          r.salience,
		AutoFocusValue:         r.autoFocus,
		HasAutoFocus:           r.hasAutoFocus,
		EffectiveAutoFocusFlag: r.effectiveAutoFocus,
		Order:                  r.declarationOrder,
		SourceSpan:             r.source,
		GessSourceText:         r.gessSource,
		ConditionValues:        cloneRuleConditions(r.conditions),
		ConditionTreeValue:     cloneRuleConditionTree(r.conditionTree),
		ConditionBranchValues:  cloneRuleConditionBranches(r.conditionBranchPlans),
		ActionValues:           append([]RuleAction(nil), r.actions...),
	}
}

func (r compiledRule) conditionPlanForBindingSlot(bindingSlot int) (compiledConditionPlan, bool) {
	if bindingSlot < 0 {
		return compiledConditionPlan{}, false
	}
	for _, plan := range r.conditionPlans {
		if plan.bindingSlot == bindingSlot {
			return plan, true
		}
		for _, pattern := range plan.listPatterns {
			for _, element := range pattern.elements {
				if element.kind == ListPatternElementSegment && element.bindingSlot == bindingSlot {
					return plan, true
				}
			}
		}
		if plan.aggregate != nil && bindingSlot >= plan.bindingSlot && bindingSlot < plan.bindingSlot+len(plan.aggregate.specs) {
			return plan, true
		}
	}
	return compiledConditionPlan{}, false
}

// conditionPlanAtOrder returns the compiled plan at the given planned position
// within a branch, matching the planned order the graph inspection reports.
func (r compiledRule) conditionPlanAtOrder(branchID, order int) (compiledConditionPlan, bool) {
	for _, branch := range r.executionConditionBranches() {
		if branch.id != branchID {
			continue
		}
		if order < 0 || order >= len(branch.plans) {
			return compiledConditionPlan{}, false
		}
		return branch.plans[order], true
	}
	return compiledConditionPlan{}, false
}

func (r compiledRule) executionConditionBranches() []compiledConditionBranch {
	if len(r.conditionBranches) > 0 {
		return r.conditionBranches
	}
	if len(r.conditionPlans) == 0 {
		return nil
	}
	return []compiledConditionBranch{{plans: r.conditionPlans}}
}

func (r compiledRule) hasAggregateConditions() bool {
	for _, branch := range r.executionConditionBranches() {
		for _, plan := range branch.plans {
			if plan.aggregate != nil {
				return true
			}
		}
	}
	return false
}

func cloneRuleConditions(conditions []RuleCondition) []RuleCondition {
	return gessrules.CloneRuleConditions(conditions)
}

func cloneRuleCondition(condition RuleCondition) RuleCondition {
	return gessrules.CloneRuleCondition(condition)
}

func cloneRuleConditionBranches(branches []RuleConditionBranch) []RuleConditionBranch {
	return gessrules.CloneRuleConditionBranches(branches)
}

func cloneRuleConditionBranchConditions(conditions []RuleConditionBranchCondition) []RuleConditionBranchCondition {
	return gessrules.CloneRuleConditionBranchConditions(conditions)
}

func cloneRuleConditionBranchCondition(condition RuleConditionBranchCondition) RuleConditionBranchCondition {
	return gessrules.CloneRuleConditionBranchCondition(condition)
}

func cloneRuleConditionTree(tree RuleConditionTree) RuleConditionTree {
	return gessrules.CloneRuleConditionTree(tree)
}

type compiledConditionTreeShape struct {
	kind           ConditionTreeKind
	children       []compiledConditionTreeShape
	conditionIndex int
	test           ExpressionSpec
	aggregate      AccumulateCondition
	source         SourceSpan
}

func (s compiledConditionTreeShape) clone() compiledConditionTreeShape {
	out := s
	out.children = make([]compiledConditionTreeShape, len(s.children))
	for i, child := range s.children {
		out.children[i] = child.clone()
	}
	out.test = cloneExpressionSpec(s.test)
	out.aggregate = cloneAccumulateCondition(s.aggregate)
	return out
}

type normalizedRuleCondition struct {
	spec        RuleConditionSpec
	aggregate   AccumulateCondition
	higherOrder compiledHigherOrderConditionSpec
	test        ExpressionSpec
	isAggregate bool
	isTest      bool
	path        []int
	visible     bool
	negated     bool
	explicit    bool
	source      SourceSpan
}

type conditionHigherOrderKind string

const (
	conditionHigherOrderUnknown conditionHigherOrderKind = ""
	conditionHigherOrderExists  conditionHigherOrderKind = "exists"
	conditionHigherOrderForall  conditionHigherOrderKind = "forall"
)

type compiledHigherOrderConditionSpec struct {
	kind        conditionHigherOrderKind
	input       ConditionSpec
	requirement ConditionSpec
}

func (s compiledHigherOrderConditionSpec) clone() compiledHigherOrderConditionSpec {
	return compiledHigherOrderConditionSpec{
		kind:        s.kind,
		input:       cloneConditionSpec(s.input),
		requirement: cloneConditionSpec(s.requirement),
	}
}

type normalizedRuleConditionBranch struct {
	conditions []normalizedRuleCondition
}

type normalizedRuleConditionExecutionBranch struct {
	source int
	branch normalizedRuleConditionBranch
}

type compiledConditionBranch struct {
	id         int
	conditions []RuleConditionBranchCondition
	plans      []compiledConditionPlan
}

func (b compiledConditionBranch) clone() compiledConditionBranch {
	out := b
	out.conditions = make([]RuleConditionBranchCondition, len(b.conditions))
	for i, condition := range b.conditions {
		out.conditions[i] = cloneRuleConditionBranchCondition(condition)
	}
	out.plans = make([]compiledConditionPlan, len(b.plans))
	for i, plan := range b.plans {
		out.plans[i] = cloneCompiledConditionPlan(plan)
	}
	return out
}

func normalizeRuleSpec(spec RuleSpec) (RuleSpec, error) {
	normalized := cloneRuleSpec(spec)
	if normalized.Name == "" {
		return RuleSpec{}, &ValidationError{
			Reason: "rule name is required",
		}
	}
	return normalized, nil
}

func normalizeRuleConditions(spec RuleSpec) ([]normalizedRuleCondition, compiledConditionTreeShape, error) {
	if spec.ConditionTree != nil && len(spec.Conditions) > 0 {
		return nil, compiledConditionTreeShape{}, &ValidationError{
			RuleName: spec.Name,
			Reason:   "rule cannot define both flat conditions and a condition tree",
		}
	}
	if spec.ConditionTree != nil {
		return flattenConditionTreeSpec(spec.Name, spec.ConditionTree)
	}
	if len(spec.Conditions) == 0 {
		return nil, compiledConditionTreeShape{}, nil
	}

	conditions := make([]normalizedRuleCondition, len(spec.Conditions))
	children := make([]compiledConditionTreeShape, len(spec.Conditions))
	for i, condition := range spec.Conditions {
		conditions[i] = normalizedRuleCondition{
			spec:    cloneRuleConditionSpec(condition),
			path:    []int{i},
			visible: true,
			source:  condition.Source,
		}
		children[i] = compiledConditionTreeShape{
			kind:           ConditionTreeKindMatch,
			conditionIndex: i,
		}
	}
	return conditions, compiledConditionTreeShape{
		kind:     ConditionTreeKindAnd,
		children: children,
	}, nil
}

func normalizeRuleConditionBranches(spec RuleSpec) ([]normalizedRuleConditionBranch, error) {
	if spec.ConditionTree != nil && len(spec.Conditions) > 0 {
		return nil, &ValidationError{
			RuleName: spec.Name,
			Reason:   "rule cannot define both flat conditions and a condition tree",
		}
	}
	if spec.ConditionTree != nil {
		return expandConditionTreeBranches(spec.Name, spec.ConditionTree)
	}
	if len(spec.Conditions) == 0 {
		return nil, nil
	}
	branch := normalizedRuleConditionBranch{
		conditions: make([]normalizedRuleCondition, len(spec.Conditions)),
	}
	for i, condition := range spec.Conditions {
		branch.conditions[i] = normalizedRuleCondition{
			spec:    cloneRuleConditionSpec(condition),
			path:    []int{i},
			visible: true,
			source:  condition.Source,
		}
	}
	return []normalizedRuleConditionBranch{branch}, nil
}

func lowerReturnValueFieldConstraints(constraints []FieldConstraintSpec) ([]FieldConstraintSpec, []ExpressionSpec) {
	if len(constraints) == 0 {
		return nil, nil
	}
	fieldConstraints := make([]FieldConstraintSpec, 0, len(constraints))
	predicates := make([]ExpressionSpec, 0)
	for _, constraint := range constraints {
		expression, ok := fieldConstraintReturnValueExpression(constraint.Value)
		if !ok {
			fieldConstraints = append(fieldConstraints, constraint)
			continue
		}
		if constant, ok := constExpressionValue(expression); ok {
			out := cloneFieldConstraintSpec(constraint)
			out.Value = cloneSpecValue(constant)
			fieldConstraints = append(fieldConstraints, out)
			continue
		}
		operator, ok := expressionComparisonOperatorFromFieldConstraint(constraint.Operator)
		if !ok {
			fieldConstraints = append(fieldConstraints, constraint)
			continue
		}
		predicates = append(predicates, CompareExpr{
			Operator: operator,
			Left:     CurrentPath(pathOrField(constraint.Path, constraint.Field)),
			Right:    cloneExpressionSpec(expression),
		})
	}
	return fieldConstraints, predicates
}

func fieldConstraintReturnValueExpression(value any) (ExpressionSpec, bool) {
	switch expression := value.(type) {
	case ExpressionSpec:
		if expression == nil {
			return nil, false
		}
		return cloneExpressionSpec(expression), true
	default:
		return nil, false
	}
}

func constExpressionValue(spec ExpressionSpec) (any, bool) {
	switch expression := spec.(type) {
	case ConstExpr:
		return cloneSpecValue(expression.Value), true
	case *ConstExpr:
		if expression == nil {
			return nil, false
		}
		return cloneSpecValue(expression.Value), true
	default:
		return nil, false
	}
}

func expressionComparisonOperatorFromFieldConstraint(operator FieldConstraintOperator) (ExpressionComparisonOperator, bool) {
	switch operator {
	case FieldConstraintOpEqual:
		return ExpressionCompareEqual, true
	case FieldConstraintOpNotEqual:
		return ExpressionCompareNotEqual, true
	case FieldConstraintOpLessThan:
		return ExpressionCompareLessThan, true
	case FieldConstraintOpLessOrEqual:
		return ExpressionCompareLessOrEqual, true
	case FieldConstraintOpGreaterThan:
		return ExpressionCompareGreaterThan, true
	case FieldConstraintOpGreaterOrEqual:
		return ExpressionCompareGreaterOrEqual, true
	default:
		return ExpressionCompareUnknown, false
	}
}

func expressionSpecReferencesCurrentFact(spec ExpressionSpec) bool {
	switch expression := spec.(type) {
	case nil:
		return false
	case CurrentFieldExpr, *CurrentFieldExpr, HasPathExpr, *HasPathExpr:
		return true
	case CallExpr:
		if slices.ContainsFunc(expression.Args, expressionSpecReferencesCurrentFact) {
			return true
		}
	case *CallExpr:
		if expression != nil {
			return expressionSpecReferencesCurrentFact(CallExpr(*expression))
		}
	case CompareExpr:
		return expressionSpecReferencesCurrentFact(expression.Left) || expressionSpecReferencesCurrentFact(expression.Right)
	case *CompareExpr:
		if expression != nil {
			return expressionSpecReferencesCurrentFact(CompareExpr(*expression))
		}
	case BooleanExpr:
		if slices.ContainsFunc(expression.Operands, expressionSpecReferencesCurrentFact) {
			return true
		}
	case *BooleanExpr:
		if expression != nil {
			return expressionSpecReferencesCurrentFact(BooleanExpr(*expression))
		}
	}
	return false
}

const maxExpandedExecutionBranchesPerBranch = 16

func expandRuleConditionBranchesForExecution(branches []normalizedRuleConditionBranch) []normalizedRuleConditionExecutionBranch {
	if len(branches) == 0 {
		return nil
	}
	out := make([]normalizedRuleConditionExecutionBranch, 0, len(branches))
	for source, branch := range branches {
		expanded := expandRuleConditionBranchForExecution(branch)
		for _, execution := range expanded {
			out = append(out, normalizedRuleConditionExecutionBranch{
				source: source,
				branch: execution,
			})
		}
	}
	return out
}

func expandRuleConditionBranchForExecution(branch normalizedRuleConditionBranch) []normalizedRuleConditionBranch {
	expanded := []normalizedRuleConditionBranch{cloneNormalizedRuleConditionBranch(branch)}
	for conditionIndex, condition := range branch.conditions {
		if condition.negated || condition.isAggregate {
			continue
		}
		for predicateIndex, predicate := range condition.spec.Predicates {
			alternatives, ok := disjunctiveJoinPredicateAlternatives(predicate)
			if !ok {
				continue
			}
			if len(expanded)*len(alternatives) > maxExpandedExecutionBranchesPerBranch {
				return []normalizedRuleConditionBranch{cloneNormalizedRuleConditionBranch(branch)}
			}
			next := make([]normalizedRuleConditionBranch, 0, len(expanded)*len(alternatives))
			for _, existing := range expanded {
				for _, alternative := range alternatives {
					branchCopy := cloneNormalizedRuleConditionBranch(existing)
					branchCopy.conditions[conditionIndex].spec.Predicates[predicateIndex] = cloneExpressionSpec(alternative)
					next = append(next, branchCopy)
				}
			}
			expanded = next
		}
	}
	return expanded
}

func cloneNormalizedRuleConditionBranch(branch normalizedRuleConditionBranch) normalizedRuleConditionBranch {
	out := normalizedRuleConditionBranch{
		conditions: make([]normalizedRuleCondition, len(branch.conditions)),
	}
	for i, condition := range branch.conditions {
		out.conditions[i] = cloneNormalizedRuleCondition(condition)
	}
	return out
}

func disjunctiveJoinPredicateAlternatives(spec ExpressionSpec) ([]ExpressionSpec, bool) {
	var expression BooleanExpr
	switch typed := spec.(type) {
	case BooleanExpr:
		expression = typed
	case *BooleanExpr:
		if typed == nil {
			return nil, false
		}
		expression = *typed
	default:
		return nil, false
	}
	if expression.Operator != ExpressionBoolOr || len(expression.Operands) == 0 {
		return nil, false
	}
	alternatives := make([]ExpressionSpec, 0, len(expression.Operands))
	for _, operand := range expression.Operands {
		if !isDisjunctiveJoinPredicateAlternative(operand) {
			return nil, false
		}
		alternatives = append(alternatives, cloneExpressionSpec(operand))
	}
	return alternatives, true
}

func isDisjunctiveJoinPredicateAlternative(spec ExpressionSpec) bool {
	var expression CompareExpr
	switch typed := spec.(type) {
	case CompareExpr:
		expression = typed
	case *CompareExpr:
		if typed == nil {
			return false
		}
		expression = *typed
	default:
		return false
	}
	if expression.Operator != ExpressionCompareEqual {
		return false
	}
	return currentFieldAndBindingFieldExpression(expression.Left, expression.Right) ||
		currentFieldAndBindingFieldExpression(expression.Right, expression.Left)
}

func currentFieldAndBindingFieldExpression(currentSpec, bindingSpec ExpressionSpec) bool {
	currentPath, ok := currentFieldExpressionPath(currentSpec)
	if !ok || !pathTopLevel(currentPath) {
		return false
	}
	bindingPath, ok := bindingFieldExpressionPath(bindingSpec)
	return ok && pathTopLevel(bindingPath)
}

func currentFieldExpressionPath(spec ExpressionSpec) (PathSpec, bool) {
	switch expression := spec.(type) {
	case CurrentFieldExpr:
		path := pathOrField(expression.Path, expression.Field)
		return path, pathRoot(path) != ""
	case *CurrentFieldExpr:
		if expression == nil {
			return PathSpec{}, false
		}
		path := pathOrField(expression.Path, expression.Field)
		return path, pathRoot(path) != ""
	default:
		return PathSpec{}, false
	}
}

func bindingFieldExpressionPath(spec ExpressionSpec) (PathSpec, bool) {
	switch expression := spec.(type) {
	case BindingFieldExpr:
		if strings.TrimSpace(expression.Binding) == "" {
			return PathSpec{}, false
		}
		path := pathOrField(expression.Path, expression.Field)
		return path, pathRoot(path) != ""
	case *BindingFieldExpr:
		if expression == nil || strings.TrimSpace(expression.Binding) == "" {
			return PathSpec{}, false
		}
		path := pathOrField(expression.Path, expression.Field)
		return path, pathRoot(path) != ""
	default:
		return PathSpec{}, false
	}
}

func expandConditionTreeBranches(ruleName string, spec ConditionSpec) ([]normalizedRuleConditionBranch, error) {
	return expandConditionTreeNodeBranches(ruleName, spec, nil, true, false)
}

func expandConditionTreeNodeBranches(ruleName string, spec ConditionSpec, path []int, visible bool, negated bool) ([]normalizedRuleConditionBranch, error) {
	switch condition := spec.(type) {
	case nil:
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "condition tree node is required",
		}
	case And:
		return expandAndConditionTreeBranches(ruleName, condition.Conditions, path, visible, negated)
	case *And:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandAndConditionTreeBranches(ruleName, condition.Conditions, path, visible, negated)
	case Or:
		return expandOrConditionTreeBranches(ruleName, condition.Conditions, path, visible, negated)
	case *Or:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandOrConditionTreeBranches(ruleName, condition.Conditions, path, visible, negated)
	case Not:
		return expandNotConditionTreeBranches(ruleName, condition.Condition, path)
	case *Not:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandNotConditionTreeBranches(ruleName, condition.Condition, path)
	case Explicit:
		match, err := explicitMatchCondition(ruleName, condition.Condition, negated)
		if err != nil {
			return nil, err
		}
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				spec:     match,
				path:     cloneIntPath(path),
				visible:  visible,
				negated:  negated,
				explicit: true,
				source:   firstSourceSpan(match.Source, condition.Source),
			}},
		}}, nil
	case *Explicit:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandConditionTreeNodeBranches(ruleName, *condition, path, visible, negated)
	case ExistsCondition:
		if negated {
			return nil, higherOrderValidationError(ruleName, -1, "exists condition is not supported under not")
		}
		if err := validateExistsConditionShape(ruleName, -1, condition.Condition); err != nil {
			return nil, err
		}
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				higherOrder: compiledHigherOrderConditionSpec{
					kind:  conditionHigherOrderExists,
					input: cloneConditionSpec(condition.Condition),
				},
				path:    cloneIntPath(path),
				visible: false,
				source:  condition.Source,
			}},
		}}, nil
	case *ExistsCondition:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandConditionTreeNodeBranches(ruleName, *condition, path, visible, negated)
	case ForallCondition:
		if negated {
			return nil, higherOrderValidationError(ruleName, -1, "forall condition is not supported under not")
		}
		if err := validateForallConditionShape(ruleName, -1, condition.Domain, condition.Requirement); err != nil {
			return nil, err
		}
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				higherOrder: compiledHigherOrderConditionSpec{
					kind:        conditionHigherOrderForall,
					input:       cloneConditionSpec(condition.Domain),
					requirement: cloneConditionSpec(condition.Requirement),
				},
				path:    cloneIntPath(path),
				visible: false,
				source:  condition.Source,
			}},
		}}, nil
	case *ForallCondition:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandConditionTreeNodeBranches(ruleName, *condition, path, visible, negated)
	case Test:
		if negated {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "test condition is not supported under not",
			}
		}
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				test:    cloneExpressionSpec(condition.Expression),
				isTest:  true,
				path:    cloneIntPath(path),
				visible: false,
				source:  condition.Source,
			}},
		}}, nil
	case *Test:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandConditionTreeNodeBranches(ruleName, *condition, path, visible, negated)
	case Match:
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				spec:    cloneRuleConditionSpec(RuleConditionSpec(condition)),
				path:    cloneIntPath(path),
				visible: visible,
				negated: negated,
				source:  RuleConditionSpec(condition).Source,
			}},
		}}, nil
	case *Match:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				spec:    cloneRuleConditionSpec(RuleConditionSpec(*condition)),
				path:    cloneIntPath(path),
				visible: visible,
				negated: negated,
				source:  RuleConditionSpec(*condition).Source,
			}},
		}}, nil
	case AccumulateCondition:
		if negated {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "accumulate condition is not supported under not",
			}
		}
		return []normalizedRuleConditionBranch{{
			conditions: []normalizedRuleCondition{{
				aggregate:   cloneAccumulateCondition(condition),
				isAggregate: true,
				path:        cloneIntPath(path),
				visible:     true,
				source:      condition.Source,
			}},
		}}, nil
	case *AccumulateCondition:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandConditionTreeNodeBranches(ruleName, *condition, path, visible, negated)
	default:
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "unsupported condition tree node",
		}
	}
}

// maxRuleConditionBranches bounds or-branch expansion: nested or groups
// multiply branch counts, so an unbounded cross-product makes compile time
// exponential in the number of or groups. Scaffolding S-05: removable once
// or-node prefix sharing makes branch counts sub-exponential in the graph.
const maxRuleConditionBranches = 1024

func expandAndConditionTreeBranches(ruleName string, specs []ConditionSpec, path []int, visible bool, negated bool) ([]normalizedRuleConditionBranch, error) {
	if len(specs) == 0 {
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "and condition requires at least one child",
		}
	}
	branches := []normalizedRuleConditionBranch{{}}
	for i, spec := range specs {
		childBranches, err := expandConditionTreeNodeBranches(ruleName, spec, appendConditionTreePath(path, i), visible, negated)
		if err != nil {
			return nil, err
		}
		if len(branches)*len(childBranches) > maxRuleConditionBranches {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason: fmt.Sprintf("or conditions expand to more than %d combined branches; split the rule into smaller rules",
					maxRuleConditionBranches),
			}
		}
		next := make([]normalizedRuleConditionBranch, 0, len(branches)*len(childBranches))
		for _, existing := range branches {
			for _, child := range childBranches {
				combined := normalizedRuleConditionBranch{
					conditions: make([]normalizedRuleCondition, 0, len(existing.conditions)+len(child.conditions)),
				}
				combined.conditions = append(combined.conditions, existing.conditions...)
				combined.conditions = append(combined.conditions, child.conditions...)
				next = append(next, combined)
			}
		}
		branches = next
	}
	return branches, nil
}

func expandOrConditionTreeBranches(ruleName string, specs []ConditionSpec, path []int, visible bool, negated bool) ([]normalizedRuleConditionBranch, error) {
	if len(specs) == 0 {
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "or condition requires at least one branch",
		}
	}
	if negated {
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "or condition is not supported under not",
		}
	}
	branches := make([]normalizedRuleConditionBranch, 0, len(specs))
	for i, spec := range specs {
		if conditionTreeContainsHigherOrder(spec) {
			return nil, higherOrderValidationError(ruleName, -1, "higher-order condition is not supported inside or")
		}
		if conditionTreeContainsAggregate(spec) {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "accumulate condition is not supported inside or",
				Err:      ErrAggregateValidation,
			}
		}
		childBranches, err := expandConditionTreeNodeBranches(ruleName, spec, appendConditionTreePath(path, i), visible, negated)
		if err != nil {
			return nil, err
		}
		branches = append(branches, childBranches...)
	}
	return branches, nil
}

func expandNotConditionTreeBranches(ruleName string, spec ConditionSpec, path []int) ([]normalizedRuleConditionBranch, error) {
	switch condition := spec.(type) {
	case Match:
		return expandConditionTreeNodeBranches(ruleName, condition, appendConditionTreePath(path, 0), false, true)
	case *Match:
		if condition == nil {
			return nil, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return expandConditionTreeNodeBranches(ruleName, *condition, appendConditionTreePath(path, 0), false, true)
	case Or, *Or:
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "or condition is not supported under not",
		}
	case ExistsCondition, *ExistsCondition:
		return nil, higherOrderValidationError(ruleName, -1, "exists condition is not supported under not")
	case ForallCondition, *ForallCondition:
		return nil, higherOrderValidationError(ruleName, -1, "forall condition is not supported under not")
	case Test, *Test:
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "test condition is not supported under not",
		}
	default:
		return nil, &ValidationError{
			RuleName: ruleName,
			Reason:   "not condition currently supports a single match child",
		}
	}
}

func appendConditionTreePath(path []int, index int) []int {
	out := make([]int, len(path), len(path)+1)
	copy(out, path)
	out = append(out, index)
	return out
}

func flattenConditionTreeSpec(ruleName string, spec ConditionSpec) ([]normalizedRuleCondition, compiledConditionTreeShape, error) {
	conditions := make([]normalizedRuleCondition, 0)
	shape, err := flattenConditionTreeNode(ruleName, spec, &conditions, nil, true, false)
	if err != nil {
		return nil, compiledConditionTreeShape{}, err
	}
	return conditions, shape, nil
}

func flattenConditionTreeNode(ruleName string, spec ConditionSpec, conditions *[]normalizedRuleCondition, path []int, visible bool, negated bool) (compiledConditionTreeShape, error) {
	switch condition := spec.(type) {
	case nil:
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "condition tree node is required",
		}
	case And:
		return flattenAndConditionTreeNode(ruleName, condition.Conditions, conditions, path, visible, negated)
	case *And:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenAndConditionTreeNode(ruleName, condition.Conditions, conditions, path, visible, negated)
	case Or:
		return flattenOrConditionTreeNode(ruleName, condition.Conditions, conditions, path, visible, negated)
	case *Or:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenOrConditionTreeNode(ruleName, condition.Conditions, conditions, path, visible, negated)
	case Not:
		return flattenNotConditionTreeNode(ruleName, condition.Condition, conditions, path)
	case *Not:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenNotConditionTreeNode(ruleName, condition.Condition, conditions, path)
	case Explicit:
		match, err := explicitMatchCondition(ruleName, condition.Condition, negated)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		index := len(*conditions)
		*conditions = append(*conditions, normalizedRuleCondition{
			spec:     match,
			path:     cloneIntPath(path),
			visible:  visible,
			negated:  negated,
			explicit: true,
			source:   firstSourceSpan(match.Source, condition.Source),
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindMatch,
			conditionIndex: index,
		}, nil
	case *Explicit:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenConditionTreeNode(ruleName, *condition, conditions, path, visible, negated)
	case ExistsCondition:
		if negated {
			return compiledConditionTreeShape{}, higherOrderValidationError(ruleName, -1, "exists condition is not supported under not")
		}
		if err := validateExistsConditionShape(ruleName, -1, condition.Condition); err != nil {
			return compiledConditionTreeShape{}, err
		}
		child, err := flattenConditionTreeNode(ruleName, condition.Condition, conditions, appendConditionTreePath(path, 0), true, false)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		return compiledConditionTreeShape{
			kind:     ConditionTreeKindExists,
			children: []compiledConditionTreeShape{child},
		}, nil
	case *ExistsCondition:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenConditionTreeNode(ruleName, *condition, conditions, path, visible, negated)
	case ForallCondition:
		if negated {
			return compiledConditionTreeShape{}, higherOrderValidationError(ruleName, -1, "forall condition is not supported under not")
		}
		if err := validateForallConditionShape(ruleName, -1, condition.Domain, condition.Requirement); err != nil {
			return compiledConditionTreeShape{}, err
		}
		domain, err := flattenConditionTreeNode(ruleName, condition.Domain, conditions, appendConditionTreePath(path, 0), true, false)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		requirement, err := flattenConditionTreeNode(ruleName, condition.Requirement, conditions, appendConditionTreePath(path, 1), true, false)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		return compiledConditionTreeShape{
			kind:     ConditionTreeKindForall,
			children: []compiledConditionTreeShape{domain, requirement},
		}, nil
	case *ForallCondition:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenConditionTreeNode(ruleName, *condition, conditions, path, visible, negated)
	case Test:
		if negated {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "test condition is not supported under not",
			}
		}
		index := len(*conditions)
		*conditions = append(*conditions, normalizedRuleCondition{
			test:    cloneExpressionSpec(condition.Expression),
			isTest:  true,
			path:    cloneIntPath(path),
			visible: false,
			source:  condition.Source,
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindTest,
			conditionIndex: index,
			test:           cloneExpressionSpec(condition.Expression),
			source:         condition.Source,
		}, nil
	case *Test:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenConditionTreeNode(ruleName, *condition, conditions, path, visible, negated)
	case Match:
		index := len(*conditions)
		*conditions = append(*conditions, normalizedRuleCondition{
			spec:    cloneRuleConditionSpec(RuleConditionSpec(condition)),
			path:    cloneIntPath(path),
			visible: visible,
			negated: negated,
			source:  RuleConditionSpec(condition).Source,
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindMatch,
			conditionIndex: index,
		}, nil
	case *Match:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		index := len(*conditions)
		*conditions = append(*conditions, normalizedRuleCondition{
			spec:    cloneRuleConditionSpec(RuleConditionSpec(*condition)),
			path:    cloneIntPath(path),
			visible: visible,
			negated: negated,
			source:  RuleConditionSpec(*condition).Source,
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindMatch,
			conditionIndex: index,
		}, nil
	case AccumulateCondition:
		if negated {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "accumulate condition is not supported under not",
			}
		}
		index := len(*conditions)
		*conditions = append(*conditions, normalizedRuleCondition{
			aggregate:   cloneAccumulateCondition(condition),
			isAggregate: true,
			path:        cloneIntPath(path),
			visible:     true,
			source:      condition.Source,
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindAccumulate,
			conditionIndex: index,
			aggregate:      cloneAccumulateCondition(condition),
			source:         condition.Source,
		}, nil
	case *AccumulateCondition:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenConditionTreeNode(ruleName, *condition, conditions, path, visible, negated)
	default:
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "unsupported condition tree node",
		}
	}
}

func flattenAndConditionTreeNode(ruleName string, specs []ConditionSpec, conditions *[]normalizedRuleCondition, path []int, visible bool, negated bool) (compiledConditionTreeShape, error) {
	if len(specs) == 0 {
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "and condition requires at least one child",
		}
	}

	shape := compiledConditionTreeShape{
		kind:     ConditionTreeKindAnd,
		children: make([]compiledConditionTreeShape, 0, len(specs)),
	}
	for i, spec := range specs {
		child, err := flattenConditionTreeNode(ruleName, spec, conditions, appendConditionTreePath(path, i), visible, negated)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		shape.children = append(shape.children, child)
	}
	return shape, nil
}

func conditionTreeContainsAggregate(spec ConditionSpec) bool {
	switch condition := spec.(type) {
	case AccumulateCondition, *AccumulateCondition:
		return true
	case And:
		if slices.ContainsFunc(condition.Conditions, conditionTreeContainsAggregate) {
			return true
		}
	case *And:
		if condition != nil {
			return conditionTreeContainsAggregate(And{Conditions: condition.Conditions})
		}
	case Or:
		if slices.ContainsFunc(condition.Conditions, conditionTreeContainsAggregate) {
			return true
		}
	case *Or:
		if condition != nil {
			return conditionTreeContainsAggregate(Or{Conditions: condition.Conditions})
		}
	case Not:
		return conditionTreeContainsAggregate(condition.Condition)
	case *Not:
		if condition != nil {
			return conditionTreeContainsAggregate(condition.Condition)
		}
	case ExistsCondition:
		return conditionTreeContainsAggregate(condition.Condition)
	case *ExistsCondition:
		if condition != nil {
			return conditionTreeContainsAggregate(condition.Condition)
		}
	case ForallCondition:
		return conditionTreeContainsAggregate(condition.Domain) || conditionTreeContainsAggregate(condition.Requirement)
	case *ForallCondition:
		if condition != nil {
			return conditionTreeContainsAggregate(condition.Domain) || conditionTreeContainsAggregate(condition.Requirement)
		}
	}
	return false
}

func conditionTreeContainsHigherOrder(spec ConditionSpec) bool {
	switch condition := spec.(type) {
	case ExistsCondition, *ExistsCondition, ForallCondition, *ForallCondition:
		return true
	case And:
		return slices.ContainsFunc(condition.Conditions, conditionTreeContainsHigherOrder)
	case *And:
		return condition != nil && conditionTreeContainsHigherOrder(And{Conditions: condition.Conditions})
	case Or:
		return slices.ContainsFunc(condition.Conditions, conditionTreeContainsHigherOrder)
	case *Or:
		return condition != nil && conditionTreeContainsHigherOrder(Or{Conditions: condition.Conditions})
	case Not:
		return conditionTreeContainsHigherOrder(condition.Condition)
	case *Not:
		return condition != nil && conditionTreeContainsHigherOrder(condition.Condition)
	case AccumulateCondition:
		return conditionTreeContainsHigherOrder(condition.Input)
	case *AccumulateCondition:
		return condition != nil && conditionTreeContainsHigherOrder(condition.Input)
	default:
		return false
	}
}

func flattenOrConditionTreeNode(ruleName string, specs []ConditionSpec, conditions *[]normalizedRuleCondition, path []int, visible bool, negated bool) (compiledConditionTreeShape, error) {
	if len(specs) == 0 {
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "or condition requires at least one branch",
		}
	}
	if negated {
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "or condition is not supported under not",
		}
	}
	shape := compiledConditionTreeShape{
		kind:     ConditionTreeKindOr,
		children: make([]compiledConditionTreeShape, 0, len(specs)),
	}
	for i, spec := range specs {
		if conditionTreeContainsHigherOrder(spec) {
			return compiledConditionTreeShape{}, higherOrderValidationError(ruleName, -1, "higher-order condition is not supported inside or")
		}
		child, err := flattenConditionTreeNode(ruleName, spec, conditions, appendConditionTreePath(path, i), visible, negated)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		shape.children = append(shape.children, child)
	}
	return shape, nil
}

func flattenNotConditionTreeNode(ruleName string, spec ConditionSpec, conditions *[]normalizedRuleCondition, path []int) (compiledConditionTreeShape, error) {
	child, err := flattenConditionTreeNode(ruleName, spec, conditions, appendConditionTreePath(path, 0), false, true)
	if err != nil {
		return compiledConditionTreeShape{}, err
	}
	if child.kind != ConditionTreeKindMatch {
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "not condition currently supports a single match child",
		}
	}
	return compiledConditionTreeShape{
		kind:     ConditionTreeKindNot,
		children: []compiledConditionTreeShape{child},
	}, nil
}

func explicitMatchCondition(ruleName string, spec ConditionSpec, negated bool) (RuleConditionSpec, error) {
	if negated {
		return RuleConditionSpec{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "explicit condition requires a positive match",
		}
	}
	switch condition := spec.(type) {
	case Match:
		return cloneRuleConditionSpec(RuleConditionSpec(condition)), nil
	case *Match:
		if condition == nil {
			return RuleConditionSpec{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "explicit condition requires a positive match",
			}
		}
		return cloneRuleConditionSpec(RuleConditionSpec(*condition)), nil
	default:
		return RuleConditionSpec{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "explicit condition requires a positive match",
		}
	}
}

func compileRuleSpec(spec RuleSpec, ruleID RuleID, declarationOrder int, modules map[ModuleName]Module, templates templateResolver, actionsByName map[string]compiledAction, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledRule, error) {
	compiled, err := compileRuleSpecInternal(spec, ruleID, declarationOrder, modules, templates, actionsByName, functions, globals)
	return compiled, attachValidationErrorSource(err, spec.Source)
}

func compileRuleSpecInternal(spec RuleSpec, ruleID RuleID, declarationOrder int, modules map[ModuleName]Module, templates templateResolver, actionsByName map[string]compiledAction, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledRule, error) {
	normalized, err := normalizeRuleSpec(spec)
	if err != nil {
		return compiledRule{}, err
	}
	if ruleID.IsZero() {
		ruleID = RuleID(normalized.Name)
	}

	normalizedConditions, conditionTreeShape, err := normalizeRuleConditions(normalized)
	if err != nil {
		return compiledRule{}, err
	}
	normalizedBranches, err := normalizeRuleConditionBranches(normalized)
	if err != nil {
		return compiledRule{}, err
	}

	if len(normalizedBranches) == 0 {
		return compiledRule{}, &ValidationError{
			RuleName: normalized.Name,
			Reason:   "rule requires at least one condition",
		}
	}
	if len(normalized.Actions) == 0 {
		return compiledRule{}, &ValidationError{
			RuleName: normalized.Name,
			Reason:   "rule requires at least one action",
		}
	}

	inspectionSet, err := compileNormalizedRuleConditionBranchWithParams(normalized.Name, ruleID, normalized.Module, normalizedConditions, templates, true, nil, functions, globals)
	if err != nil {
		return compiledRule{}, err
	}
	publicBranches := make([]compiledRuleConditionSet, len(normalizedBranches))
	var representative compiledRuleConditionSet
	for branchIndex, branch := range normalizedBranches {
		publicBranchIR := newBranchPlanningIR(branchIndex, branch.conditions)
		publicBranch, err := compileBranchPlanningIR(normalized.Name, ruleID, normalized.Module, publicBranchIR, templates, false, nil, functions, globals)
		if err != nil {
			return compiledRule{}, err
		}
		if branchIndex == 0 {
			representative = publicBranch
		} else if err := validateBranchBindingContract(normalized.Name, representative.conditions, publicBranch.conditions); err != nil {
			return compiledRule{}, err
		}
		publicBranches[branchIndex] = publicBranch
	}
	executionBranches := expandRuleConditionBranchesForExecution(normalizedBranches)
	compiledBranches := make([]compiledConditionBranch, 0, len(executionBranches))
	for branchIndex, executionBranch := range executionBranches {
		publicBranch := publicBranches[executionBranch.source]
		plannedBranchIR := newReorderedBranchPlanningIR(branchIndex, executionBranch.branch.conditions)
		plannedBranch, err := compileBranchPlanningIR(normalized.Name, ruleID, normalized.Module, plannedBranchIR, templates, false, nil, functions, globals)
		if err != nil {
			return compiledRule{}, err
		}
		compiledBranches = append(compiledBranches, compiledConditionBranch{
			id:         branchIndex,
			conditions: publicBranch.branchConditions,
			plans:      remapCompiledConditionPlansToPublicBranch(plannedBranch.conditionPlans, publicBranch),
		})
	}
	conditions := representative.conditions
	conditionPlans := compiledBranches[0].plans
	treeConditions := inspectionSet.treeConditions
	conditionTree := buildRuleConditionTree(conditionTreeShape, treeConditions)
	conditionBranches := make([]RuleConditionBranch, len(publicBranches))
	for i, branch := range publicBranches {
		conditionBranches[i] = RuleConditionBranch{
			IDValue:         i,
			ConditionValues: cloneRuleConditionBranchConditions(branch.branchConditions),
		}
	}
	autoFocus, hasAutoFocus, effectiveAutoFocus := ruleAutoFocusValues(normalized, modules)

	actions := make([]RuleAction, 0, len(normalized.Actions))
	actionExecutions := make([]compiledRuleAction, 0, len(normalized.Actions))
	actionBindingSlots := bindingSlotsForRuleConditions(conditions)
	rhsBinds := make(map[string]struct{})
	allActionsSkipBindingFreeze := true
	for i, action := range normalized.Actions {
		if action.Name == "" {
			return compiledRule{}, &ValidationError{
				RuleName:       normalized.Name,
				Source:         action.Source,
				ActionIndex:    i,
				HasActionIndex: true,
				Reason:         "action name is required",
			}
		}
		compiledAction, ok := actionsByName[action.Name]
		if !ok {
			return compiledRule{}, &ValidationError{
				RuleName:       normalized.Name,
				Source:         action.Source,
				ActionIndex:    i,
				HasActionIndex: true,
				Reason:         "unknown action",
			}
		}
		if !compiledAction.skipBindingFreeze {
			allActionsSkipBindingFreeze = false
		}
		actionExecution, err := compileRuleActionExecution(normalized.Name, i, compiledAction, conditions, actionBindingSlots, modules, templates.byKey, functions, globals, rhsBinds)
		if err != nil {
			return compiledRule{}, attachValidationErrorSource(err, action.Source)
		}
		if actionExecution.kind == compiledRuleActionEffect && actionExecution.effect.kind == ActionEffectBind {
			rhsBinds[actionExecution.effect.target] = struct{}{}
		}
		actionExecution.source = action.Source
		actions = append(actions, RuleAction{
			NameValue:  action.Name,
			Order:      i,
			SourceSpan: action.Source,
		})
		actionExecutions = append(actionExecutions, actionExecution)
	}

	compiled := compiledRule{
		id:                          ruleID,
		name:                        normalized.Name,
		module:                      normalized.Module,
		description:                 normalized.Description,
		tags:                        append([]string(nil), normalized.Tags...),
		salience:                    normalized.Salience,
		autoFocus:                   autoFocus,
		hasAutoFocus:                hasAutoFocus,
		effectiveAutoFocus:          effectiveAutoFocus,
		declarationOrder:            declarationOrder,
		source:                      normalized.Source,
		gessSource:                  normalized.GessSource,
		conditions:                  conditions,
		treeConditions:              treeConditions,
		conditionTree:               conditionTree,
		conditionTreeShape:          conditionTreeShape.clone(),
		conditionPlans:              conditionPlans,
		conditionBranches:           compiledBranches,
		conditionBranchPlans:        conditionBranches,
		actions:                     actions,
		actionExecutions:            actionExecutions,
		allActionsSkipBindingFreeze: allActionsSkipBindingFreeze,
	}
	compiled.revisionID = ruleRevisionIDFor(compiled)
	compiled.identityScopeHash = candidateIdentityScopeHash(compiled.id, compiled.revisionID)
	return compiled, nil
}

func ruleAutoFocusValues(rule RuleSpec, modules map[ModuleName]Module) (bool, bool, bool) {
	if rule.AutoFocus != nil {
		autoFocus := *rule.AutoFocus
		return autoFocus, true, autoFocus
	}
	module, ok := modules[normalizeModuleName(rule.Module)]
	if !ok {
		return false, false, false
	}
	moduleAutoFocus, ok := module.AutoFocusDefault()
	if !ok {
		return false, false, false
	}
	return false, false, moduleAutoFocus
}

func bindingSlotsForRuleConditions(conditions []RuleCondition) map[string]int {
	out := make(map[string]int, len(conditions))
	for i, condition := range conditions {
		if condition.BindingName == "" {
			continue
		}
		if _, exists := out[condition.BindingName]; !exists {
			out[condition.BindingName] = i
		}
	}
	return out
}

type compiledRuleConditionSet struct {
	conditions       []RuleCondition
	treeConditions   []RuleCondition
	branchConditions []RuleConditionBranchCondition
	conditionPlans   []compiledConditionPlan
}

func compileNormalizedRuleConditionBranch(ruleName string, ruleID RuleID, author ModuleName, normalizedConditions []normalizedRuleCondition, templates templateResolver, allowDuplicateBindings bool) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithOuter(ruleName, ruleID, author, normalizedConditions, templates, allowDuplicateBindings, nil, nil, nil, nil)
}

func compileNormalizedRuleConditionBranchWithOuter(ruleName string, ruleID RuleID, author ModuleName, normalizedConditions []normalizedRuleCondition, templates templateResolver, allowDuplicateBindings bool, outerConditions []RuleCondition, outerBindingSlots map[string]int, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, author, normalizedConditions, templates, allowDuplicateBindings, outerConditions, outerBindingSlots, nil, functions, globals)
}

func compileNormalizedRuleConditionBranchWithParams(ruleName string, ruleID RuleID, author ModuleName, normalizedConditions []normalizedRuleCondition, templates templateResolver, allowDuplicateBindings bool, params map[string]ValueKind, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, author, normalizedConditions, templates, allowDuplicateBindings, nil, nil, params, functions, globals)
}

func compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName string, ruleID RuleID, author ModuleName, normalizedConditions []normalizedRuleCondition, templates templateResolver, allowDuplicateBindings bool, outerConditions []RuleCondition, outerBindingSlots map[string]int, params map[string]ValueKind, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledRuleConditionSet, error) {
	bindingSlots := make(map[string]int, len(normalizedConditions))
	maps.Copy(bindingSlots, outerBindingSlots)
	allBindingSlots := make(map[string]int, len(normalizedConditions))
	maps.Copy(allBindingSlots, outerBindingSlots)
	conditions := make([]RuleCondition, len(outerConditions), len(outerConditions)+len(normalizedConditions))
	copy(conditions, outerConditions)
	treeConditions := make([]RuleCondition, 0, len(normalizedConditions))
	branchConditions := make([]RuleConditionBranchCondition, 0, len(normalizedConditions))
	conditionPlans := make([]compiledConditionPlan, 0, len(normalizedConditions))
	for i, node := range normalizedConditions {
		if node.higherOrder.kind != conditionHigherOrderUnknown {
			if node.negated {
				return compiledRuleConditionSet{}, higherOrderValidationError(ruleName, i, string(node.higherOrder.kind)+" condition is not supported under not")
			}
			inputSpec, err := higherOrderCounterInputSpec(ruleName, i, node.higherOrder)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			inputNormalized, _, err := flattenConditionTreeSpec(ruleName, inputSpec)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			for _, inputNode := range inputNormalized {
				if inputNode.spec.Binding == "" {
					continue
				}
				if _, exists := bindingSlots[inputNode.spec.Binding]; exists {
					return compiledRuleConditionSet{}, higherOrderValidationError(ruleName, i, "higher-order input binding collides with an outer binding")
				}
			}
			inputSet, err := compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, author, inputNormalized, templates, false, conditions, bindingSlots, params, functions, globals)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			inputOnlyConditions := inputSet.conditions[len(conditions):]
			for _, inputCondition := range inputOnlyConditions {
				if _, exists := bindingSlots[inputCondition.BindingName]; exists {
					return compiledRuleConditionSet{}, higherOrderValidationError(ruleName, i, "higher-order input binding collides with an outer binding")
				}
			}

			specKind := aggregateExists
			if node.higherOrder.kind == conditionHigherOrderForall {
				specKind = aggregateForall
			}
			compiledSpecs := []compiledAggregateSpec{{kind: specKind}}
			conditionID := aggregateConditionIDFor(ruleID, i, compiledSpecs, inputSet.conditionPlans)
			treeCondition := RuleCondition{IDValue: conditionID, BindingName: string(node.higherOrder.kind), Order: i}
			treeConditions = append(treeConditions, treeCondition)
			branchConditions = append(branchConditions, RuleConditionBranchCondition{
				ConditionValue: treeCondition,
				PathValue:      cloneIntPath(node.path),
				VisibleValue:   false,
			})
			conditionPlans = append(conditionPlans, compiledConditionPlan{
				id:          conditionID,
				binding:     string(node.higherOrder.kind),
				bindingSlot: -1,
				path:        cloneIntPath(node.path),
				source:      node.source,
				aggregate: &compiledAggregatePlan{
					inputPlans:  inputSet.conditionPlans,
					specs:       compiledSpecs,
					higherOrder: node.higherOrder.kind,
				},
			})
			continue
		}
		if node.isAggregate {
			if node.negated {
				return compiledRuleConditionSet{}, &ValidationError{
					RuleName:          ruleName,
					Source:            node.source,
					ConditionIndex:    i,
					HasConditionIndex: true,
					Reason:            "accumulate condition is not supported under not",
				}
			}
			if err := validateAggregateInputShape(ruleName, i, node.aggregate.Input); err != nil {
				return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
			}
			inputNormalized, _, err := flattenConditionTreeSpec(ruleName, node.aggregate.Input)
			if err != nil {
				return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
			}
			for _, inputNode := range inputNormalized {
				if inputNode.spec.Binding == "" {
					continue
				}
				if _, exists := bindingSlots[inputNode.spec.Binding]; exists {
					return compiledRuleConditionSet{}, &ValidationError{
						RuleName:          ruleName,
						Source:            node.source,
						ConditionIndex:    i,
						HasConditionIndex: true,
						Reason:            "aggregate input binding collides with an outer binding",
						Err:               ErrAggregateValidation,
					}
				}
			}
			inputSet, err := compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, author, inputNormalized, templates, false, conditions, bindingSlots, params, functions, globals)
			if err != nil {
				return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
			}
			inputOnlyConditions := inputSet.conditions[len(conditions):]
			for _, inputCondition := range inputOnlyConditions {
				if _, exists := bindingSlots[inputCondition.BindingName]; exists {
					return compiledRuleConditionSet{}, &ValidationError{
						RuleName:          ruleName,
						Source:            node.source,
						ConditionIndex:    i,
						HasConditionIndex: true,
						Reason:            "aggregate input binding collides with an outer binding",
						Err:               ErrAggregateValidation,
					}
				}
			}
			inputBindingSlots := make(map[string]int, len(inputSet.conditions))
			for j, condition := range inputSet.conditions {
				inputBindingSlots[condition.BindingName] = j
			}
			compiledSpecs, resultConditions, err := compileAggregateSpecList(ruleName, i, node.aggregate.Specs, inputSet.conditions, inputBindingSlots, templates.byKey, functions, globals)
			if err != nil {
				return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
			}
			for _, result := range resultConditions {
				if _, exists := allBindingSlots[result.BindingName]; exists {
					return compiledRuleConditionSet{}, &ValidationError{
						RuleName:          ruleName,
						Source:            node.source,
						ConditionIndex:    i,
						HasConditionIndex: true,
						Reason:            "aggregate result binding collides with an existing binding",
						Err:               ErrAggregateValidation,
					}
				}
			}
			aggregateID := aggregateConditionIDFor(ruleID, i, compiledSpecs, inputSet.conditionPlans)
			firstSlot := len(conditions)
			for j, result := range resultConditions {
				result.IDValue = aggregateID
				result.Order = firstSlot + j
				conditions = append(conditions, result)
				bindingSlots[result.BindingName] = firstSlot + j
				allBindingSlots[result.BindingName] = firstSlot + j
			}
			treeCondition := RuleCondition{IDValue: aggregateID, BindingName: "accumulate", Order: i, SourceSpan: node.source}
			treeConditions = append(treeConditions, treeCondition)
			branchConditions = append(branchConditions, RuleConditionBranchCondition{
				ConditionValue: treeCondition,
				PathValue:      cloneIntPath(node.path),
				VisibleValue:   true,
			})
			conditionPlans = append(conditionPlans, compiledConditionPlan{
				id:          aggregateID,
				binding:     "accumulate",
				bindingSlot: firstSlot,
				path:        []int{i},
				source:      node.source,
				aggregate: &compiledAggregatePlan{
					inputPlans: inputSet.conditionPlans,
					specs:      compiledSpecs,
				},
			})
			continue
		}
		if node.isTest {
			if node.test == nil {
				return compiledRuleConditionSet{}, expressionValidationErrorAtSource(node.source, ruleName, i, 0, "", "test condition expression is required", nil)
			}
			if expressionSpecReferencesCurrentFact(node.test) {
				return compiledRuleConditionSet{}, expressionValidationErrorAtSource(node.source, ruleName, i, 0, "", "test condition cannot reference the current fact", nil)
			}
			if len(conditions) == 0 {
				return compiledRuleConditionSet{}, &ValidationError{
					RuleName:          ruleName,
					Source:            node.source,
					ConditionIndex:    i,
					HasConditionIndex: true,
					Reason:            "test condition requires an earlier visible binding",
				}
			}
			_, planPredicate, err := compileExpressionPredicateSpecWithParamsAndSource(node.test, node.source, ruleName, i, 0, nil, conditions, bindingSlots, templates.byKey, params, functions, globals)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			planPredicate.placement = ExpressionPredicatePlacementBetaResidual
			conditionPlans = append(conditionPlans, compiledConditionPlan{
				path:           []int{i},
				source:         node.source,
				isTest:         true,
				predicates:     []compiledExpressionPredicate{planPredicate},
				testPredicates: []compiledExpressionPredicate{planPredicate},
			})
			continue
		}
		condition := node.spec
		if condition.Binding == "" {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				Source:            node.source,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "condition binding is required",
			}
		}
		if !isValidBindingName(condition.Binding) {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				Source:            node.source,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "invalid binding name",
			}
		}
		if _, exists := allBindingSlots[condition.Binding]; exists && !allowDuplicateBindings {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				Source:            node.source,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "duplicate binding",
			}
		}
		if node.negated && len(bindingSlots) == 0 {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				Source:            node.source,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "not condition requires an earlier positive condition",
			}
		}

		target, indexKind, template, conditionName, conditionTemplateKey, err := templates.resolveFactTarget(ruleName, i, author, condition.Target)
		if err != nil {
			return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
		}

		constraintSpecs, returnValuePredicates := lowerReturnValueFieldConstraints(condition.FieldConstraints)
		fieldConstraints := make([]FieldConstraint, 0, len(constraintSpecs))
		compiledConstraints := make([]compiledFieldConstraint, 0, len(constraintSpecs))
		for constraintIndex, constraint := range constraintSpecs {
			compiledConstraint, planConstraint, err := compileFieldConstraintSpec(constraint, node.source, ruleName, i, constraintIndex, template)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			fieldConstraints = append(fieldConstraints, compiledConstraint)
			compiledConstraints = append(compiledConstraints, planConstraint)
		}

		if !node.visible && listPatternsHaveSegment(condition.ListPatterns) {
			return compiledRuleConditionSet{}, attachValidationErrorSource(listPatternValidationError(ruleName, i, -1, "list segment binding is not supported inside non-visible conditions", ErrInvalidListPattern), node.source)
		}
		listPatterns, compiledListPatterns, listPatternBindings, err := compileListPatternSpecs(condition.ListPatterns, ruleName, i, template, conditions, bindingSlots, params, functions, globals)
		if err != nil {
			return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
		}
		for _, result := range listPatternBindings {
			if result.BindingName == condition.Binding {
				return compiledRuleConditionSet{}, attachValidationErrorSource(listPatternValidationError(ruleName, i, -1, "list segment binding collides with condition binding", ErrInvalidListPattern), node.source)
			}
			if _, exists := allBindingSlots[result.BindingName]; exists {
				return compiledRuleConditionSet{}, attachValidationErrorSource(listPatternValidationError(ruleName, i, -1, "list segment binding collides with an existing binding", ErrInvalidListPattern), node.source)
			}
		}

		joinConstraints := make([]JoinConstraint, 0, len(condition.JoinConstraints))
		compiledJoins := make([]compiledJoinConstraint, 0, len(condition.JoinConstraints))
		for joinIndex, joinConstraint := range condition.JoinConstraints {
			compiledJoin, planJoin, err := compileJoinConstraintSpecWithSource(joinConstraint, node.source, ruleName, i, joinIndex, template, conditions, bindingSlots, templates.byKey)
			if err != nil {
				return compiledRuleConditionSet{}, attachValidationErrorSource(err, node.source)
			}
			joinConstraints = append(joinConstraints, compiledJoin)
			compiledJoins = append(compiledJoins, planJoin)
		}

		predicateSpecs := make([]ExpressionSpec, 0, len(returnValuePredicates)+len(condition.Predicates))
		predicateSpecs = append(predicateSpecs, returnValuePredicates...)
		for _, predicate := range condition.Predicates {
			predicateSpecs = append(predicateSpecs, cloneExpressionSpec(predicate))
		}
		predicates := make([]ExpressionPredicate, 0, len(predicateSpecs))
		compiledPredicates := make([]compiledExpressionPredicate, 0, len(predicateSpecs))
		for predicateIndex, predicate := range predicateSpecs {
			compiledPredicate, planPredicate, err := compileExpressionPredicateSpecWithParamsAndSource(predicate, node.source, ruleName, i, predicateIndex, template, conditions, bindingSlots, templates.byKey, params, functions, globals)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			predicates = append(predicates, compiledPredicate)
			compiledPredicates = append(compiledPredicates, splitCompiledExpressionPredicate(planPredicate)...)
		}

		conditionID := conditionIDFor(ruleID, i, condition.Binding, conditionName, conditionTemplateKey, fieldConstraints, compiledListPatterns, joinConstraints, compiledPredicates, node.negated)
		compiledCondition := RuleCondition{
			IDValue:               conditionID,
			BindingName:           condition.Binding,
			NameValue:             conditionName,
			TemplateKeyValue:      conditionTemplateKey,
			FieldConstraintValues: fieldConstraints,
			ListPatternValues:     listPatterns,
			JoinConstraintValues:  joinConstraints,
			PredicateValues:       predicates,
			ExplicitValue:         node.explicit,
			Order:                 i,
			SourceSpan:            node.source,
		}
		publicBindingSlot := -1
		if node.visible {
			if slot, ok := bindingSlots[condition.Binding]; ok && allowDuplicateBindings {
				publicBindingSlot = slot
			} else {
				publicBindingSlot = len(conditions)
				conditions = append(conditions, compiledCondition)
			}
		}
		setCompiledExpressionPredicatesCurrentBindingSlot(compiledPredicates, publicBindingSlot)
		for j := range listPatternBindings {
			listPatternBindings[j].IDValue = conditionID
			listPatternBindings[j].Order = len(conditions)
			conditions = append(conditions, listPatternBindings[j])
			bindingSlots[listPatternBindings[j].BindingName] = listPatternBindings[j].Order
			allBindingSlots[listPatternBindings[j].BindingName] = listPatternBindings[j].Order
			for patternIndex := range compiledListPatterns {
				for elementIndex := range compiledListPatterns[patternIndex].elements {
					if compiledListPatterns[patternIndex].elements[elementIndex].binding == listPatternBindings[j].BindingName {
						compiledListPatterns[patternIndex].elements[elementIndex].bindingSlot = listPatternBindings[j].Order
					}
				}
			}
		}
		treeConditions = append(treeConditions, cloneRuleCondition(compiledCondition))
		branchConditions = append(branchConditions, RuleConditionBranchCondition{
			ConditionValue: cloneRuleCondition(compiledCondition),
			PathValue:      cloneIntPath(node.path),
			VisibleValue:   node.visible,
			NegatedValue:   node.negated,
			ExplicitValue:  node.explicit,
		})
		conditionPlans = append(conditionPlans, compiledConditionPlan{
			id:               conditionID,
			binding:          condition.Binding,
			syntheticBinding: condition.SyntheticBinding,
			bindingSlot:      publicBindingSlot,
			path:             []int{i},
			source:           node.source,
			negated:          node.negated,
			explicit:         node.explicit,
			target:           target,
			constraints:      compiledConstraints,
			listPatterns:     compiledListPatterns,
			joins:            compiledJoins,
			predicates:       compiledPredicates,
			indexable:        true,
			indexKind:        indexKind,
		})
		allBindingSlots[condition.Binding] = i
		if node.visible {
			bindingSlots[condition.Binding] = publicBindingSlot
		}
	}
	return compiledRuleConditionSet{
		conditions:       conditions,
		treeConditions:   treeConditions,
		branchConditions: branchConditions,
		conditionPlans:   conditionPlans,
	}, nil
}

func validateAggregateInputShape(ruleName string, conditionIndex int, spec ConditionSpec) error {
	switch condition := spec.(type) {
	case nil:
		return aggregateValidationError(ruleName, conditionIndex, -1, "accumulate input is required", nil)
	case Match, *Match:
		return nil
	case Explicit:
		match, err := explicitMatchCondition(ruleName, condition.Condition, false)
		if err != nil {
			return aggregateValidationError(ruleName, conditionIndex, -1, "accumulate input supports only positive match conjunctions", nil)
		}
		return validateAggregateInputShape(ruleName, conditionIndex, Match(match))
	case *Explicit:
		if condition == nil {
			return aggregateValidationError(ruleName, conditionIndex, -1, "accumulate input is required", nil)
		}
		return validateAggregateInputShape(ruleName, conditionIndex, *condition)
	case And:
		for _, child := range condition.Conditions {
			if err := validateAggregateInputShape(ruleName, conditionIndex, child); err != nil {
				return err
			}
		}
		return nil
	case *And:
		if condition == nil {
			return aggregateValidationError(ruleName, conditionIndex, -1, "accumulate input is required", nil)
		}
		return validateAggregateInputShape(ruleName, conditionIndex, And{Conditions: condition.Conditions})
	default:
		return aggregateValidationError(ruleName, conditionIndex, -1, "accumulate input supports only positive match conjunctions", nil)
	}
}

func validateExistsConditionShape(ruleName string, conditionIndex int, spec ConditionSpec) error {
	if err := validateHigherOrderPositiveInputShape(ruleName, conditionIndex, spec, "exists input"); err != nil {
		return err
	}
	return nil
}

func validateForallConditionShape(ruleName string, conditionIndex int, domain ConditionSpec, requirement ConditionSpec) error {
	if err := validateHigherOrderPositiveInputShape(ruleName, conditionIndex, domain, "forall domain"); err != nil {
		return err
	}
	return validateForallRequirementShape(ruleName, conditionIndex, requirement)
}

func validateHigherOrderPositiveInputShape(ruleName string, conditionIndex int, spec ConditionSpec, label string) error {
	switch condition := spec.(type) {
	case nil:
		return higherOrderValidationError(ruleName, conditionIndex, label+" is required")
	case Match, *Match:
		return nil
	case Explicit:
		match, err := explicitMatchCondition(ruleName, condition.Condition, false)
		if err != nil {
			return higherOrderValidationError(ruleName, conditionIndex, label+" supports only positive match conjunctions")
		}
		return validateHigherOrderPositiveInputShape(ruleName, conditionIndex, Match(match), label)
	case *Explicit:
		if condition == nil {
			return higherOrderValidationError(ruleName, conditionIndex, label+" is required")
		}
		return validateHigherOrderPositiveInputShape(ruleName, conditionIndex, *condition, label)
	case And:
		if len(condition.Conditions) == 0 {
			return higherOrderValidationError(ruleName, conditionIndex, label+" requires at least one child")
		}
		for _, child := range condition.Conditions {
			if err := validateHigherOrderPositiveInputShape(ruleName, conditionIndex, child, label); err != nil {
				return err
			}
		}
		return nil
	case *And:
		if condition == nil {
			return higherOrderValidationError(ruleName, conditionIndex, label+" is required")
		}
		return validateHigherOrderPositiveInputShape(ruleName, conditionIndex, And{Conditions: condition.Conditions}, label)
	default:
		return higherOrderValidationError(ruleName, conditionIndex, label+" supports only positive match conjunctions")
	}
}

func validateForallRequirementShape(ruleName string, conditionIndex int, spec ConditionSpec) error {
	parts, err := forallRequirementPartsForSpec(spec)
	if err != nil {
		return higherOrderValidationError(ruleName, conditionIndex, err.Error())
	}
	if len(parts.matches) > 1 {
		return higherOrderValidationError(ruleName, conditionIndex, "forall requirement currently supports at most one positive match")
	}
	if len(parts.matches) == 0 && len(parts.tests) == 0 {
		return higherOrderValidationError(ruleName, conditionIndex, "forall requirement is required")
	}
	return nil
}

func higherOrderCounterInputSpec(ruleName string, conditionIndex int, spec compiledHigherOrderConditionSpec) (ConditionSpec, error) {
	switch spec.kind {
	case conditionHigherOrderExists:
		if err := validateExistsConditionShape(ruleName, conditionIndex, spec.input); err != nil {
			return nil, err
		}
		return cloneConditionSpec(spec.input), nil
	case conditionHigherOrderForall:
		if err := validateForallConditionShape(ruleName, conditionIndex, spec.input, spec.requirement); err != nil {
			return nil, err
		}
		parts, err := forallRequirementPartsForSpec(spec.requirement)
		if err != nil {
			return nil, higherOrderValidationError(ruleName, conditionIndex, err.Error())
		}
		if len(parts.matches) == 0 && len(parts.tests) == 0 {
			return nil, higherOrderValidationError(ruleName, conditionIndex, "forall requirement is required")
		}
		if len(parts.matches) > 1 {
			return nil, higherOrderValidationError(ruleName, conditionIndex, "forall requirement currently supports at most one positive match")
		}
		if len(parts.matches) == 1 {
			requirement := cloneRuleConditionSpec(parts.matches[0])
			for _, test := range parts.tests {
				requirement.Predicates = append(requirement.Predicates, rewriteBindingFieldExpressionsToCurrent(test, requirement.Binding))
			}
			return And{Conditions: []ConditionSpec{cloneConditionSpec(spec.input), Not{Condition: Match(requirement)}}}, nil
		}
		requirement := parts.tests[0]
		if len(parts.tests) > 1 {
			operands := make([]ExpressionSpec, len(parts.tests))
			for i, test := range parts.tests {
				operands[i] = cloneExpressionSpec(test)
			}
			requirement = BooleanExpr{Operator: ExpressionBoolAnd, Operands: operands}
		}
		counterexample := Test{Expression: BooleanExpr{Operator: ExpressionBoolNot, Operands: []ExpressionSpec{cloneExpressionSpec(requirement)}}}
		return And{Conditions: []ConditionSpec{cloneConditionSpec(spec.input), counterexample}}, nil
	default:
		return nil, higherOrderValidationError(ruleName, conditionIndex, "unknown higher-order condition")
	}
}

type forallRequirementParts struct {
	matches []RuleConditionSpec
	tests   []ExpressionSpec
}

func forallRequirementPartsForSpec(spec ConditionSpec) (forallRequirementParts, error) {
	var out forallRequirementParts
	if err := collectForallRequirementParts(spec, &out); err != nil {
		return forallRequirementParts{}, err
	}
	return out, nil
}

func collectForallRequirementParts(spec ConditionSpec, out *forallRequirementParts) error {
	switch condition := spec.(type) {
	case nil:
		return fmt.Errorf("forall requirement is required")
	case Test:
		out.tests = append(out.tests, cloneExpressionSpec(condition.Expression))
	case *Test:
		if condition != nil {
			out.tests = append(out.tests, cloneExpressionSpec(condition.Expression))
		}
	case Match:
		out.matches = append(out.matches, cloneRuleConditionSpec(RuleConditionSpec(condition)))
	case *Match:
		if condition == nil {
			return fmt.Errorf("forall requirement is required")
		}
		out.matches = append(out.matches, cloneRuleConditionSpec(RuleConditionSpec(*condition)))
	case Explicit:
		match, err := explicitMatchCondition("", condition.Condition, false)
		if err != nil {
			return fmt.Errorf("forall requirement supports positive match/test conjunctions")
		}
		out.matches = append(out.matches, match)
	case *Explicit:
		if condition == nil {
			return fmt.Errorf("forall requirement is required")
		}
		return collectForallRequirementParts(*condition, out)
	case And:
		if len(condition.Conditions) == 0 {
			return fmt.Errorf("forall requirement requires at least one child")
		}
		for _, child := range condition.Conditions {
			if err := collectForallRequirementParts(child, out); err != nil {
				return err
			}
		}
	case *And:
		if condition != nil {
			return collectForallRequirementParts(And{Conditions: condition.Conditions}, out)
		}
		return fmt.Errorf("forall requirement is required")
	default:
		return fmt.Errorf("forall requirement supports positive match/test conjunctions")
	}
	return nil
}

func rewriteBindingFieldExpressionsToCurrent(spec ExpressionSpec, binding string) ExpressionSpec {
	switch expression := spec.(type) {
	case nil:
		return nil
	case BindingFieldExpr:
		if expression.Binding == binding {
			return CurrentFieldExpr{Field: expression.Field, Path: clonePathSpec(expression.Path)}
		}
		return cloneExpressionSpec(expression)
	case *BindingFieldExpr:
		if expression == nil {
			return nil
		}
		return rewriteBindingFieldExpressionsToCurrent(*expression, binding)
	case CallExpr:
		out := cloneExpressionSpec(expression).(CallExpr)
		for i, arg := range out.Args {
			out.Args[i] = rewriteBindingFieldExpressionsToCurrent(arg, binding)
		}
		return out
	case *CallExpr:
		if expression == nil {
			return nil
		}
		out := cloneExpressionSpec(*expression).(CallExpr)
		for i, arg := range out.Args {
			out.Args[i] = rewriteBindingFieldExpressionsToCurrent(arg, binding)
		}
		return &out
	case CompareExpr:
		out := cloneExpressionSpec(expression).(CompareExpr)
		out.Left = rewriteBindingFieldExpressionsToCurrent(out.Left, binding)
		out.Right = rewriteBindingFieldExpressionsToCurrent(out.Right, binding)
		return out
	case *CompareExpr:
		if expression == nil {
			return nil
		}
		out := cloneExpressionSpec(*expression).(CompareExpr)
		out.Left = rewriteBindingFieldExpressionsToCurrent(out.Left, binding)
		out.Right = rewriteBindingFieldExpressionsToCurrent(out.Right, binding)
		return &out
	case BooleanExpr:
		out := cloneExpressionSpec(expression).(BooleanExpr)
		for i, operand := range out.Operands {
			out.Operands[i] = rewriteBindingFieldExpressionsToCurrent(operand, binding)
		}
		return out
	case *BooleanExpr:
		if expression == nil {
			return nil
		}
		out := cloneExpressionSpec(*expression).(BooleanExpr)
		for i, operand := range out.Operands {
			out.Operands[i] = rewriteBindingFieldExpressionsToCurrent(operand, binding)
		}
		return &out
	default:
		return cloneExpressionSpec(spec)
	}
}

func higherOrderValidationError(ruleName string, conditionIndex int, reason string) error {
	validation := &ValidationError{
		RuleName: ruleName,
		Reason:   reason,
		Err:      ErrInvalidHigherOrderCondition,
	}
	if conditionIndex >= 0 {
		validation.ConditionIndex = conditionIndex
		validation.HasConditionIndex = true
	}
	return validation
}

func validateBranchBindingContract(ruleName string, expected, actual []RuleCondition) error {
	if len(expected) != len(actual) {
		return &ValidationError{
			RuleName: ruleName,
			Reason:   "or branches must expose the same bindings",
		}
	}
	for i := range expected {
		if expected[i].BindingName != actual[i].BindingName || expected[i].NameValue != actual[i].NameValue || expected[i].TemplateKeyValue != actual[i].TemplateKeyValue {
			return &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "or branches must expose compatible bindings",
			}
		}
	}
	return nil
}

func buildRuleConditionTree(shape compiledConditionTreeShape, conditions []RuleCondition) RuleConditionTree {
	switch shape.kind {
	case ConditionTreeKindAnd:
		tree := RuleConditionTree{
			KindValue:     ConditionTreeKindAnd,
			ChildrenValue: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.ChildrenValue = append(tree.ChildrenValue, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindMatch:
		if shape.conditionIndex < 0 || shape.conditionIndex >= len(conditions) {
			return RuleConditionTree{KindValue: ConditionTreeKindUnknown}
		}
		return RuleConditionTree{
			KindValue:      ConditionTreeKindMatch,
			MatchCondition: cloneRuleCondition(conditions[shape.conditionIndex]),
			HasMatch:       true,
			SourceSpan:     conditions[shape.conditionIndex].SourceSpan,
		}
	case ConditionTreeKindTest:
		return RuleConditionTree{
			KindValue:      ConditionTreeKindTest,
			TestExpression: cloneExpressionSpec(shape.test),
			HasTest:        true,
			SourceSpan:     shape.source,
		}
	case ConditionTreeKindNot:
		tree := RuleConditionTree{
			KindValue:     ConditionTreeKindNot,
			ChildrenValue: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.ChildrenValue = append(tree.ChildrenValue, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindOr:
		tree := RuleConditionTree{
			KindValue:     ConditionTreeKindOr,
			ChildrenValue: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.ChildrenValue = append(tree.ChildrenValue, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindExists:
		tree := RuleConditionTree{
			KindValue:     ConditionTreeKindExists,
			ChildrenValue: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.ChildrenValue = append(tree.ChildrenValue, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindForall:
		tree := RuleConditionTree{
			KindValue:     ConditionTreeKindForall,
			ChildrenValue: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.ChildrenValue = append(tree.ChildrenValue, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindAccumulate:
		return RuleConditionTree{
			KindValue:      ConditionTreeKindAccumulate,
			AggregateValue: cloneAccumulateCondition(shape.aggregate),
			HasAggregate:   true,
			SourceSpan:     shape.source,
		}
	default:
		return RuleConditionTree{KindValue: ConditionTreeKindUnknown}
	}
}

func ruleRevisionIDFor(rule compiledRule) RuleRevisionID {
	// conditionPlans stays in planned execution order; plan.bindingSlot holds
	// the public condition order after the remap, so hash lookups must go
	// through it rather than indexing the slice positionally.
	plansByPublicOrder := make(map[int]*compiledConditionPlan, len(rule.conditionPlans))
	for i := range rule.conditionPlans {
		plansByPublicOrder[rule.conditionPlans[i].bindingSlot] = &rule.conditionPlans[i]
	}
	sum := sha256.New()
	sum.Write([]byte("gess/rule/v2\n"))
	sum.Write([]byte("id:"))
	sum.Write([]byte(rule.id.String()))
	sum.Write([]byte("\nmodule:"))
	sum.Write([]byte(rule.module.String()))
	sum.Write([]byte("\nname:"))
	sum.Write([]byte(rule.name))
	sum.Write([]byte("\nsalience:"))
	sum.Write(fmt.Appendf(nil, "%d", rule.salience))
	sum.Write([]byte("\nauto-focus:"))
	sum.Write(fmt.Appendf(nil, "%t:%t:%t", rule.hasAutoFocus, rule.autoFocus, rule.effectiveAutoFocus))
	sum.Write([]byte("\nall-actions-skip-binding-freeze:"))
	sum.Write(fmt.Appendf(nil, "%t", rule.allActionsSkipBindingFreeze))
	sum.Write([]byte("\nconditions:"))
	for _, condition := range rule.conditions {
		sum.Write(fmt.Appendf(nil, "%d:", condition.Order))
		sum.Write([]byte(condition.BindingName))
		sum.Write([]byte(":"))
		sum.Write([]byte(condition.NameValue))
		sum.Write([]byte(":"))
		sum.Write([]byte(condition.TemplateKeyValue.String()))
		sum.Write([]byte(":"))
		for _, constraint := range condition.FieldConstraintValues {
			sum.Write([]byte(constraint.Field))
			sum.Write([]byte("="))
			sum.Write([]byte(string(constraint.Operator)))
			sum.Write([]byte("="))
			sum.Write([]byte(constraint.Value.String()))
			sum.Write([]byte(","))
		}
		sum.Write([]byte("|"))
		for _, pattern := range condition.ListPatternValues {
			sum.Write([]byte(pathDisplay(pattern.Path())))
			sum.Write([]byte(":"))
			for _, element := range pattern.Elements() {
				sum.Write([]byte(string(element.Kind())))
				sum.Write([]byte(":"))
				sum.Write([]byte(element.Binding()))
				sum.Write([]byte(","))
			}
			sum.Write([]byte(";"))
		}
		if plan, ok := plansByPublicOrder[condition.Order]; ok && len(plan.listPatterns) > 0 {
			sum.Write([]byte(serializeCompiledListPatterns(plan.listPatterns)))
		}
		sum.Write([]byte("|"))
		for _, join := range condition.JoinConstraintValues {
			sum.Write([]byte(join.Field))
			sum.Write([]byte("="))
			sum.Write([]byte(string(join.Operator)))
			sum.Write([]byte("="))
			sum.Write([]byte(join.Ref.Binding))
			sum.Write([]byte("."))
			sum.Write([]byte(join.Ref.Field))
			sum.Write([]byte(","))
		}
		if plan, ok := plansByPublicOrder[condition.Order]; ok && len(plan.predicates) > 0 {
			sum.Write([]byte("|"))
			sum.Write([]byte(serializeCompiledExpressionPredicates(plan.predicates)))
		}
		if condition.ExplicitValue {
			sum.Write([]byte("|explicit"))
		}
		sum.Write([]byte(";"))
	}
	if ruleUsesConditionTreeIdentity(rule) {
		sum.Write([]byte("\ncondition-tree:"))
		writeCompiledConditionTreeShapeHash(sum, rule.conditionTreeShape)
		sum.Write([]byte("\ntree-conditions:"))
		for _, plan := range rule.conditionPlans {
			if plan.isTest {
				sum.Write([]byte("test:"))
				sum.Write([]byte(serializeCompiledExpressionPredicates(plan.testPredicates)))
				sum.Write([]byte(";"))
				continue
			}
			if plan.aggregate != nil && plan.aggregate.higherOrder != conditionHigherOrderUnknown {
				sum.Write([]byte("higher-order:"))
				sum.Write([]byte(plan.binding))
				sum.Write([]byte(":"))
				sum.Write([]byte(string(plan.aggregate.higherOrder)))
				sum.Write([]byte(":"))
				sum.Write([]byte(serializeCompiledAggregateSpecs(plan.aggregate.specs)))
				sum.Write([]byte(":"))
				for _, input := range plan.aggregate.inputPlans {
					sum.Write([]byte(input.id.String()))
					sum.Write([]byte(","))
				}
				sum.Write([]byte(";"))
				continue
			}
			condition, ok := ruleTreeConditionByOrder(rule.treeConditions, plan.path)
			if !ok {
				continue
			}
			sum.Write(fmt.Appendf(nil, "%d:", condition.Order))
			if plan.negated {
				sum.Write([]byte("not:"))
			}
			if plan.explicit {
				sum.Write([]byte("explicit:"))
			}
			sum.Write([]byte(condition.BindingName))
			sum.Write([]byte(":"))
			sum.Write([]byte(condition.NameValue))
			sum.Write([]byte(":"))
			sum.Write([]byte(condition.TemplateKeyValue.String()))
			sum.Write([]byte(":"))
			for _, constraint := range condition.FieldConstraintValues {
				sum.Write([]byte(constraint.Field))
				sum.Write([]byte("="))
				sum.Write([]byte(string(constraint.Operator)))
				sum.Write([]byte("="))
				sum.Write([]byte(constraint.Value.String()))
				sum.Write([]byte(","))
			}
			sum.Write([]byte("|"))
			for _, pattern := range condition.ListPatternValues {
				sum.Write([]byte(pathDisplay(pattern.Path())))
				sum.Write([]byte(":"))
				for _, element := range pattern.Elements() {
					sum.Write([]byte(string(element.Kind())))
					sum.Write([]byte(":"))
					sum.Write([]byte(element.Binding()))
					sum.Write([]byte(","))
				}
				sum.Write([]byte(";"))
			}
			sum.Write([]byte("|"))
			for _, join := range condition.JoinConstraintValues {
				sum.Write([]byte(join.Field))
				sum.Write([]byte("="))
				sum.Write([]byte(string(join.Operator)))
				sum.Write([]byte("="))
				sum.Write([]byte(join.Ref.Binding))
				sum.Write([]byte("."))
				sum.Write([]byte(join.Ref.Field))
				sum.Write([]byte(","))
			}
			if len(plan.predicates) > 0 {
				sum.Write([]byte("|"))
				sum.Write([]byte(serializeCompiledExpressionPredicates(plan.predicates)))
			}
			sum.Write([]byte(";"))
		}
		if len(rule.conditionBranches) > 1 {
			sum.Write([]byte("\ncondition-branches:"))
			for branchIndex, branch := range rule.conditionBranches {
				sum.Write(fmt.Appendf(nil, "%d:", branchIndex))
				for _, plan := range branch.plans {
					if plan.isTest {
						sum.Write([]byte("test:"))
						sum.Write([]byte(serializeCompiledExpressionPredicates(plan.testPredicates)))
						sum.Write([]byte(";"))
						continue
					}
					if plan.aggregate != nil && plan.aggregate.higherOrder != conditionHigherOrderUnknown {
						sum.Write([]byte("higher-order:"))
						sum.Write([]byte(plan.binding))
						sum.Write([]byte(":"))
						sum.Write([]byte(string(plan.aggregate.higherOrder)))
						sum.Write([]byte(":"))
						sum.Write([]byte(serializeCompiledAggregateSpecs(plan.aggregate.specs)))
						sum.Write([]byte(";"))
						continue
					}
					sum.Write([]byte(plan.binding))
					sum.Write([]byte(":"))
					if plan.negated {
						sum.Write([]byte("not:"))
					}
					if plan.explicit {
						sum.Write([]byte("explicit:"))
					}
					sum.Write([]byte(plan.target.name))
					sum.Write([]byte(":"))
					sum.Write([]byte(plan.target.templateKey.String()))
					sum.Write([]byte(":"))
					sum.Write([]byte(serializeCompiledFieldConstraints(plan.constraints)))
					sum.Write([]byte("|"))
					sum.Write([]byte(serializeCompiledListPatterns(plan.listPatterns)))
					sum.Write([]byte("|"))
					sum.Write([]byte(serializeCompiledJoinConstraints(plan.joins)))
					sum.Write([]byte("|"))
					sum.Write([]byte(serializeCompiledExpressionPredicates(plan.predicates)))
					sum.Write([]byte(";"))
				}
			}
		}
	}
	sum.Write([]byte("\nactions:"))
	for _, action := range rule.actions {
		sum.Write(fmt.Appendf(nil, "%d:", action.Order))
		sum.Write([]byte(action.NameValue))
		sum.Write([]byte(";"))
	}
	sum.Write([]byte("\naction-executions:"))
	for _, action := range rule.actionExecutions {
		sum.Write([]byte(serializeCompiledRuleAction(action)))
		sum.Write([]byte(";"))
	}
	return RuleRevisionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}

func ruleUsesConditionTreeIdentity(rule compiledRule) bool {
	if len(rule.conditionBranches) > 1 {
		return true
	}
	if conditionTreeShapeContainsTest(rule.conditionTreeShape) {
		return true
	}
	if len(rule.treeConditions) != len(rule.conditions) {
		return true
	}
	for _, plan := range rule.conditionPlans {
		if plan.negated || plan.explicit {
			return true
		}
	}
	return false
}

func conditionTreeShapeContainsTest(shape compiledConditionTreeShape) bool {
	if shape.kind == ConditionTreeKindTest {
		return true
	}
	return slices.ContainsFunc(shape.children, conditionTreeShapeContainsTest)
}

func writeCompiledConditionTreeShapeHash(sum hashWriter, shape compiledConditionTreeShape) {
	sum.Write([]byte(string(shape.kind)))
	if shape.conditionIndex >= 0 {
		sum.Write(fmt.Appendf(nil, ":%d", shape.conditionIndex))
	}
	sum.Write([]byte("["))
	for _, child := range shape.children {
		writeCompiledConditionTreeShapeHash(sum, child)
		sum.Write([]byte(","))
	}
	sum.Write([]byte("]"))
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func ruleTreeConditionByOrder(conditions []RuleCondition, path []int) (RuleCondition, bool) {
	if len(path) == 0 {
		return RuleCondition{}, false
	}
	order := path[0]
	for _, condition := range conditions {
		if condition.Order == order {
			return condition, true
		}
	}
	return RuleCondition{}, false
}
