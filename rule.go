package gess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type RuleConditionSpec struct {
	Binding          string
	Name             string
	TemplateKey      TemplateKey
	FieldConstraints []FieldConstraintSpec
	ListPatterns     []ListPatternSpec
	JoinConstraints  []JoinConstraintSpec
	Predicates       []ExpressionSpec
}

func (s RuleConditionSpec) clone() RuleConditionSpec {
	out := s
	out.Binding = strings.TrimSpace(out.Binding)
	out.Name = strings.TrimSpace(out.Name)
	out.TemplateKey = TemplateKey(strings.TrimSpace(string(out.TemplateKey)))
	out.FieldConstraints = make([]FieldConstraintSpec, len(s.FieldConstraints))
	for i, constraint := range s.FieldConstraints {
		out.FieldConstraints[i] = constraint.clone()
	}
	out.ListPatterns = make([]ListPatternSpec, len(s.ListPatterns))
	for i, pattern := range s.ListPatterns {
		out.ListPatterns[i] = pattern.clone()
	}
	out.JoinConstraints = make([]JoinConstraintSpec, len(s.JoinConstraints))
	for i, constraint := range s.JoinConstraints {
		out.JoinConstraints[i] = constraint.clone()
	}
	out.Predicates = make([]ExpressionSpec, len(s.Predicates))
	for i, predicate := range s.Predicates {
		out.Predicates[i] = cloneExpressionSpec(predicate)
	}
	return out
}

// ConditionSpec is a rule left-hand-side condition tree node.
type ConditionSpec interface {
	conditionSpecNode()
}

// And groups condition tree nodes into a conjunction.
type And struct {
	Conditions []ConditionSpec
}

func (And) conditionSpecNode() {}

func (s And) clone() And {
	out := s
	out.Conditions = make([]ConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = cloneConditionSpec(condition)
	}
	return out
}

// Or groups condition tree branches into a disjunction.
type Or struct {
	Conditions []ConditionSpec
}

func (Or) conditionSpecNode() {}

func (s Or) clone() Or {
	out := s
	out.Conditions = make([]ConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = cloneConditionSpec(condition)
	}
	return out
}

// Not negates a condition tree node. Bindings declared inside Not are local to
// the negated condition and are not exposed to later conditions or actions.
type Not struct {
	Condition ConditionSpec
}

func (Not) conditionSpecNode() {}

func (s Not) clone() Not {
	out := s
	out.Condition = cloneConditionSpec(s.Condition)
	return out
}

// Exists tests whether at least one tuple matching Condition exists. Bindings
// introduced inside Exists are local to the condition.
type ExistsCondition struct {
	Condition ConditionSpec
}

func (ExistsCondition) conditionSpecNode() {}

func Exists(condition ConditionSpec) ExistsCondition {
	return ExistsCondition{Condition: cloneConditionSpec(condition)}
}

func (s ExistsCondition) clone() ExistsCondition {
	out := s
	out.Condition = cloneConditionSpec(s.Condition)
	return out
}

// Forall tests whether every tuple matching Domain also satisfies Requirement.
// Bindings introduced inside Forall are local to the condition.
type ForallCondition struct {
	Domain      ConditionSpec
	Requirement ConditionSpec
}

func (ForallCondition) conditionSpecNode() {}

func Forall(domain ConditionSpec, requirement ConditionSpec) ForallCondition {
	return ForallCondition{
		Domain:      cloneConditionSpec(domain),
		Requirement: cloneConditionSpec(requirement),
	}
}

func (s ForallCondition) clone() ForallCondition {
	out := s
	out.Domain = cloneConditionSpec(s.Domain)
	out.Requirement = cloneConditionSpec(s.Requirement)
	return out
}

// Test evaluates a standalone boolean condition over earlier local bindings.
type Test struct {
	Expression ExpressionSpec
}

func (Test) conditionSpecNode() {}

func (s Test) clone() Test {
	s.Expression = cloneExpressionSpec(s.Expression)
	return s
}

// Match is a positive fact match condition tree node.
type Match RuleConditionSpec

func (Match) conditionSpecNode() {}

func (s Match) clone() Match {
	return Match(RuleConditionSpec(s).clone())
}

func cloneConditionSpec(spec ConditionSpec) ConditionSpec {
	switch condition := spec.(type) {
	case nil:
		return nil
	case And:
		return condition.clone()
	case *And:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case Or:
		return condition.clone()
	case *Or:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case Not:
		return condition.clone()
	case *Not:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case ExistsCondition:
		return condition.clone()
	case *ExistsCondition:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case ForallCondition:
		return condition.clone()
	case *ForallCondition:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case Test:
		return condition.clone()
	case *Test:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case Match:
		return condition.clone()
	case *Match:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	case AccumulateCondition:
		return condition.clone()
	case *AccumulateCondition:
		if condition == nil {
			return nil
		}
		cloned := condition.clone()
		return &cloned
	default:
		return spec
	}
}

type RuleActionSpec struct {
	Name string
}

func (s RuleActionSpec) clone() RuleActionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	return out
}

type RuleSpec struct {
	Name        string
	Module      ModuleName
	ID          RuleID
	Description string
	Tags        []string
	Salience    int
	// Conditions is the flat positive conjunction form. When ConditionTree is
	// nil, compile normalizes Conditions to And(Match...) without changing
	// condition ordering, condition identity, graph topology, or agenda behavior.
	Conditions []RuleConditionSpec
	// ConditionTree is the structured left-hand side form. It is mutually
	// exclusive with Conditions in one RuleSpec.
	ConditionTree ConditionSpec
	Actions       []RuleActionSpec
}

func (s RuleSpec) clone() RuleSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	out.Module = normalizeModuleName(out.Module)
	out.ID = RuleID(strings.TrimSpace(string(out.ID)))
	out.Tags = append([]string(nil), s.Tags...)
	out.Conditions = make([]RuleConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = condition.clone()
	}
	out.ConditionTree = cloneConditionSpec(s.ConditionTree)
	out.Actions = make([]RuleActionSpec, len(s.Actions))
	for i, action := range s.Actions {
		out.Actions[i] = action.clone()
	}
	return out
}

type RuleCondition struct {
	id               ConditionID
	binding          string
	name             string
	templateKey      TemplateKey
	fieldConstraints []FieldConstraint
	listPatterns     []RuleListPattern
	joinConstraints  []JoinConstraint
	predicates       []ExpressionPredicate
	order            int
}

func (c RuleCondition) ID() ConditionID {
	return c.id
}

func (c RuleCondition) Binding() string {
	return c.binding
}

func (c RuleCondition) Name() string {
	return c.name
}

func (c RuleCondition) TemplateKey() TemplateKey {
	return c.templateKey
}

func (c RuleCondition) FieldConstraints() []FieldConstraint {
	out := make([]FieldConstraint, len(c.fieldConstraints))
	for i, constraint := range c.fieldConstraints {
		out[i] = constraint.clone()
	}
	return out
}

func (c RuleCondition) ListPatterns() []RuleListPattern {
	out := make([]RuleListPattern, len(c.listPatterns))
	for i, pattern := range c.listPatterns {
		out[i] = pattern.clone()
	}
	return out
}

func (c RuleCondition) JoinConstraints() []JoinConstraint {
	out := make([]JoinConstraint, len(c.joinConstraints))
	for i, constraint := range c.joinConstraints {
		out[i] = constraint.clone()
	}
	return out
}

func (c RuleCondition) Predicates() []ExpressionPredicate {
	return cloneExpressionPredicates(c.predicates)
}

func (c RuleCondition) DeclarationOrder() int {
	return c.order
}

func (c RuleCondition) clone() RuleCondition {
	out := c
	out.fieldConstraints = make([]FieldConstraint, len(c.fieldConstraints))
	for i, constraint := range c.fieldConstraints {
		out.fieldConstraints[i] = constraint.clone()
	}
	out.listPatterns = make([]RuleListPattern, len(c.listPatterns))
	for i, pattern := range c.listPatterns {
		out.listPatterns[i] = pattern.clone()
	}
	out.joinConstraints = make([]JoinConstraint, len(c.joinConstraints))
	for i, constraint := range c.joinConstraints {
		out.joinConstraints[i] = constraint.clone()
	}
	out.predicates = cloneExpressionPredicates(c.predicates)
	return out
}

// RuleConditionBranch is one compiled branch in a condition tree. Flat rules
// and tree rules without disjunction expose one branch; disjunctive trees expose
// one branch for each expanded alternative in source order.
type RuleConditionBranch struct {
	id         int
	conditions []RuleConditionBranchCondition
}

func (b RuleConditionBranch) ID() int {
	return b.id
}

func (b RuleConditionBranch) Conditions() []RuleConditionBranchCondition {
	out := make([]RuleConditionBranchCondition, len(b.conditions))
	for i, condition := range b.conditions {
		out[i] = condition.clone()
	}
	return out
}

func (b RuleConditionBranch) clone() RuleConditionBranch {
	out := b
	out.conditions = make([]RuleConditionBranchCondition, len(b.conditions))
	for i, condition := range b.conditions {
		out.conditions[i] = condition.clone()
	}
	return out
}

// RuleConditionBranchCondition describes a condition within an expanded branch.
// Path is the authored condition-tree path, while Visible indicates whether the
// binding is exposed to actions. Negated conditions are local and not visible.
type RuleConditionBranchCondition struct {
	condition RuleCondition
	path      []int
	visible   bool
	negated   bool
}

func (c RuleConditionBranchCondition) Condition() RuleCondition {
	return c.condition.clone()
}

func (c RuleConditionBranchCondition) Path() []int {
	return cloneIntPath(c.path)
}

func (c RuleConditionBranchCondition) Visible() bool {
	return c.visible
}

func (c RuleConditionBranchCondition) Negated() bool {
	return c.negated
}

func (c RuleConditionBranchCondition) clone() RuleConditionBranchCondition {
	out := c
	out.condition = c.condition.clone()
	out.path = cloneIntPath(c.path)
	return out
}

type ConditionTreeKind string

const (
	ConditionTreeKindUnknown    ConditionTreeKind = ""
	ConditionTreeKindAnd        ConditionTreeKind = "and"
	ConditionTreeKindMatch      ConditionTreeKind = "match"
	ConditionTreeKindTest       ConditionTreeKind = "test"
	ConditionTreeKindNot        ConditionTreeKind = "not"
	ConditionTreeKindOr         ConditionTreeKind = "or"
	ConditionTreeKindExists     ConditionTreeKind = "exists"
	ConditionTreeKindForall     ConditionTreeKind = "forall"
	ConditionTreeKindAccumulate ConditionTreeKind = "accumulate"
)

type RuleConditionTree struct {
	kind     ConditionTreeKind
	children []RuleConditionTree
	match    RuleCondition
	hasMatch bool
	test     ExpressionSpec
	hasTest  bool
}

func (t RuleConditionTree) Kind() ConditionTreeKind {
	return t.kind
}

func (t RuleConditionTree) Children() []RuleConditionTree {
	out := make([]RuleConditionTree, len(t.children))
	for i, child := range t.children {
		out[i] = child.clone()
	}
	return out
}

func (t RuleConditionTree) Match() (RuleCondition, bool) {
	if !t.hasMatch {
		return RuleCondition{}, false
	}
	return t.match.clone(), true
}

func (t RuleConditionTree) Test() (ExpressionSpec, bool) {
	if !t.hasTest {
		return nil, false
	}
	return cloneExpressionSpec(t.test), true
}

func (t RuleConditionTree) clone() RuleConditionTree {
	out := t
	out.children = make([]RuleConditionTree, len(t.children))
	for i, child := range t.children {
		out.children[i] = child.clone()
	}
	out.match = t.match.clone()
	out.test = cloneExpressionSpec(t.test)
	return out
}

type RuleAction struct {
	name  string
	order int
}

func (a RuleAction) Name() string {
	return a.name
}

func (a RuleAction) DeclarationOrder() int {
	return a.order
}

func (a RuleAction) clone() RuleAction {
	return a
}

type Rule struct {
	id                RuleID
	revisionID        RuleRevisionID
	name              string
	module            ModuleName
	description       string
	tags              []string
	salience          int
	declarationOrder  int
	conditions        []RuleCondition
	conditionTree     RuleConditionTree
	conditionBranches []RuleConditionBranch
	actions           []RuleAction
}

func (r Rule) ID() RuleID {
	return r.id
}

func (r Rule) RevisionID() RuleRevisionID {
	return r.revisionID
}

func (r Rule) Name() string {
	return r.name
}

func (r Rule) Module() ModuleName {
	return r.module
}

func (r Rule) Description() string {
	return r.description
}

func (r Rule) Tags() []string {
	out := make([]string, len(r.tags))
	copy(out, r.tags)
	return out
}

func (r Rule) Salience() int {
	return r.salience
}

func (r Rule) DeclarationOrder() int {
	return r.declarationOrder
}

func (r Rule) Conditions() []RuleCondition {
	out := make([]RuleCondition, len(r.conditions))
	for i, condition := range r.conditions {
		out[i] = condition.clone()
	}
	return out
}

func (r Rule) ConditionTree() RuleConditionTree {
	return r.conditionTree.clone()
}

func (r Rule) ConditionBranches() []RuleConditionBranch {
	return cloneRuleConditionBranches(r.conditionBranches)
}

func (r Rule) Actions() []RuleAction {
	out := make([]RuleAction, len(r.actions))
	for i, action := range r.actions {
		out[i] = action.clone()
	}
	return out
}

func (r Rule) clone() Rule {
	out := r
	out.tags = append([]string(nil), r.tags...)
	out.conditions = make([]RuleCondition, len(r.conditions))
	for i, condition := range r.conditions {
		out.conditions[i] = condition.clone()
	}
	out.conditionTree = r.conditionTree.clone()
	out.conditionBranches = cloneRuleConditionBranches(r.conditionBranches)
	out.actions = make([]RuleAction, len(r.actions))
	for i, action := range r.actions {
		out.actions[i] = action.clone()
	}
	return out
}

type compiledRule struct {
	id                          RuleID
	revisionID                  RuleRevisionID
	name                        string
	module                      ModuleName
	description                 string
	tags                        []string
	salience                    int
	declarationOrder            int
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
		id:                r.id,
		revisionID:        r.revisionID,
		name:              r.name,
		module:            r.module,
		description:       r.description,
		tags:              append([]string(nil), r.tags...),
		salience:          r.salience,
		declarationOrder:  r.declarationOrder,
		conditions:        cloneRuleConditions(r.conditions),
		conditionTree:     r.conditionTree.clone(),
		conditionBranches: cloneRuleConditionBranches(r.conditionBranchPlans),
		actions:           append([]RuleAction(nil), r.actions...),
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
	out := make([]RuleCondition, len(conditions))
	for i, condition := range conditions {
		out[i] = condition.clone()
	}
	return out
}

func cloneRuleConditionBranches(branches []RuleConditionBranch) []RuleConditionBranch {
	out := make([]RuleConditionBranch, len(branches))
	for i, branch := range branches {
		out[i] = branch.clone()
	}
	return out
}

func cloneRuleConditionBranchConditions(conditions []RuleConditionBranchCondition) []RuleConditionBranchCondition {
	out := make([]RuleConditionBranchCondition, len(conditions))
	for i, condition := range conditions {
		out[i] = condition.clone()
	}
	return out
}

type compiledConditionTreeShape struct {
	kind           ConditionTreeKind
	children       []compiledConditionTreeShape
	conditionIndex int
	test           ExpressionSpec
}

func (s compiledConditionTreeShape) clone() compiledConditionTreeShape {
	out := s
	out.children = make([]compiledConditionTreeShape, len(s.children))
	for i, child := range s.children {
		out.children[i] = child.clone()
	}
	out.test = cloneExpressionSpec(s.test)
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
		out.conditions[i] = condition.clone()
	}
	out.plans = make([]compiledConditionPlan, len(b.plans))
	for i, plan := range b.plans {
		out.plans[i] = cloneCompiledConditionPlan(plan)
	}
	return out
}

func normalizeRuleSpec(spec RuleSpec) (RuleSpec, error) {
	normalized := spec.clone()
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
			spec:    condition.clone(),
			path:    []int{i},
			visible: true,
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
			spec:    condition.clone(),
			path:    []int{i},
			visible: true,
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
			out := constraint.clone()
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
	if !ok || !currentPath.topLevel() {
		return false
	}
	bindingPath, ok := bindingFieldExpressionPath(bindingSpec)
	return ok && bindingPath.topLevel()
}

func currentFieldExpressionPath(spec ExpressionSpec) (PathSpec, bool) {
	switch expression := spec.(type) {
	case CurrentFieldExpr:
		path := pathOrField(expression.Path, expression.Field)
		return path, path.root() != ""
	case *CurrentFieldExpr:
		if expression == nil {
			return PathSpec{}, false
		}
		path := pathOrField(expression.Path, expression.Field)
		return path, path.root() != ""
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
		return path, path.root() != ""
	case *BindingFieldExpr:
		if expression == nil || strings.TrimSpace(expression.Binding) == "" {
			return PathSpec{}, false
		}
		path := pathOrField(expression.Path, expression.Field)
		return path, path.root() != ""
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
				spec:    RuleConditionSpec(condition).clone(),
				path:    cloneIntPath(path),
				visible: visible,
				negated: negated,
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
				spec:    RuleConditionSpec(*condition).clone(),
				path:    cloneIntPath(path),
				visible: visible,
				negated: negated,
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
				aggregate:   condition.clone(),
				isAggregate: true,
				path:        cloneIntPath(path),
				visible:     true,
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
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindTest,
			conditionIndex: index,
			test:           cloneExpressionSpec(condition.Expression),
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
			spec:    RuleConditionSpec(condition).clone(),
			path:    cloneIntPath(path),
			visible: visible,
			negated: negated,
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
			spec:    RuleConditionSpec(*condition).clone(),
			path:    cloneIntPath(path),
			visible: visible,
			negated: negated,
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
			aggregate:   condition.clone(),
			isAggregate: true,
			path:        cloneIntPath(path),
			visible:     true,
		})
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindAccumulate,
			conditionIndex: index,
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

func compileRuleSpec(spec RuleSpec, ruleID RuleID, declarationOrder int, templatesByKey map[TemplateKey]Template, actionsByName map[string]compiledAction, functions map[string]compiledPureFunction) (compiledRule, error) {
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

	inspectionSet, err := compileNormalizedRuleConditionBranchWithParams(normalized.Name, ruleID, normalizedConditions, templatesByKey, true, nil, functions)
	if err != nil {
		return compiledRule{}, err
	}
	publicBranches := make([]compiledRuleConditionSet, len(normalizedBranches))
	var representative compiledRuleConditionSet
	for branchIndex, branch := range normalizedBranches {
		publicBranchIR := newBranchPlanningIR(branchIndex, branch.conditions)
		publicBranch, err := compileBranchPlanningIR(normalized.Name, ruleID, publicBranchIR, templatesByKey, false, nil, functions)
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
		plannedBranch, err := compileBranchPlanningIR(normalized.Name, ruleID, plannedBranchIR, templatesByKey, false, nil, functions)
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
			id:         i,
			conditions: cloneRuleConditionBranchConditions(branch.branchConditions),
		}
	}

	actions := make([]RuleAction, 0, len(normalized.Actions))
	actionExecutions := make([]compiledRuleAction, 0, len(normalized.Actions))
	actionBindingSlots := bindingSlotsForRuleConditions(conditions)
	allActionsSkipBindingFreeze := true
	for i, action := range normalized.Actions {
		if action.Name == "" {
			return compiledRule{}, &ValidationError{
				RuleName:       normalized.Name,
				ActionIndex:    i,
				HasActionIndex: true,
				Reason:         "action name is required",
			}
		}
		compiledAction, ok := actionsByName[action.Name]
		if !ok {
			return compiledRule{}, &ValidationError{
				RuleName:       normalized.Name,
				ActionIndex:    i,
				HasActionIndex: true,
				Reason:         "unknown action",
			}
		}
		if !compiledAction.skipBindingFreeze {
			allActionsSkipBindingFreeze = false
		}
		actionExecution, err := compileRuleActionExecution(normalized.Name, i, compiledAction, conditions, actionBindingSlots, templatesByKey, functions)
		if err != nil {
			return compiledRule{}, err
		}
		actions = append(actions, RuleAction{
			name:  action.Name,
			order: i,
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
		declarationOrder:            declarationOrder,
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

func bindingSlotsForRuleConditions(conditions []RuleCondition) map[string]int {
	out := make(map[string]int, len(conditions))
	for i, condition := range conditions {
		if condition.binding == "" {
			continue
		}
		if _, exists := out[condition.binding]; !exists {
			out[condition.binding] = i
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

func compileNormalizedRuleConditionBranch(ruleName string, ruleID RuleID, normalizedConditions []normalizedRuleCondition, templatesByKey map[TemplateKey]Template, allowDuplicateBindings bool) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithOuter(ruleName, ruleID, normalizedConditions, templatesByKey, allowDuplicateBindings, nil, nil, nil)
}

func compileNormalizedRuleConditionBranchWithOuter(ruleName string, ruleID RuleID, normalizedConditions []normalizedRuleCondition, templatesByKey map[TemplateKey]Template, allowDuplicateBindings bool, outerConditions []RuleCondition, outerBindingSlots map[string]int, functions map[string]compiledPureFunction) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, normalizedConditions, templatesByKey, allowDuplicateBindings, outerConditions, outerBindingSlots, nil, functions)
}

func compileNormalizedRuleConditionBranchWithParams(ruleName string, ruleID RuleID, normalizedConditions []normalizedRuleCondition, templatesByKey map[TemplateKey]Template, allowDuplicateBindings bool, params map[string]ValueKind, functions map[string]compiledPureFunction) (compiledRuleConditionSet, error) {
	return compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, normalizedConditions, templatesByKey, allowDuplicateBindings, nil, nil, params, functions)
}

func compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName string, ruleID RuleID, normalizedConditions []normalizedRuleCondition, templatesByKey map[TemplateKey]Template, allowDuplicateBindings bool, outerConditions []RuleCondition, outerBindingSlots map[string]int, params map[string]ValueKind, functions map[string]compiledPureFunction) (compiledRuleConditionSet, error) {
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
			inputSet, err := compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, inputNormalized, templatesByKey, false, conditions, bindingSlots, params, functions)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			inputOnlyConditions := inputSet.conditions[len(conditions):]
			for _, inputCondition := range inputOnlyConditions {
				if _, exists := bindingSlots[inputCondition.binding]; exists {
					return compiledRuleConditionSet{}, higherOrderValidationError(ruleName, i, "higher-order input binding collides with an outer binding")
				}
			}

			specKind := aggregateExists
			if node.higherOrder.kind == conditionHigherOrderForall {
				specKind = aggregateForall
			}
			compiledSpecs := []compiledAggregateSpec{{kind: specKind}}
			conditionID := aggregateConditionIDFor(ruleID, i, compiledSpecs, inputSet.conditionPlans)
			treeCondition := RuleCondition{id: conditionID, binding: string(node.higherOrder.kind), order: i}
			treeConditions = append(treeConditions, treeCondition)
			branchConditions = append(branchConditions, RuleConditionBranchCondition{
				condition: treeCondition,
				path:      cloneIntPath(node.path),
				visible:   false,
			})
			conditionPlans = append(conditionPlans, compiledConditionPlan{
				id:          conditionID,
				binding:     string(node.higherOrder.kind),
				bindingSlot: -1,
				path:        cloneIntPath(node.path),
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
					ConditionIndex:    i,
					HasConditionIndex: true,
					Reason:            "accumulate condition is not supported under not",
				}
			}
			if err := validateAggregateInputShape(ruleName, i, node.aggregate.Input); err != nil {
				return compiledRuleConditionSet{}, err
			}
			inputNormalized, _, err := flattenConditionTreeSpec(ruleName, node.aggregate.Input)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			for _, inputNode := range inputNormalized {
				if inputNode.spec.Binding == "" {
					continue
				}
				if _, exists := bindingSlots[inputNode.spec.Binding]; exists {
					return compiledRuleConditionSet{}, &ValidationError{
						RuleName:          ruleName,
						ConditionIndex:    i,
						HasConditionIndex: true,
						Reason:            "aggregate input binding collides with an outer binding",
						Err:               ErrAggregateValidation,
					}
				}
			}
			inputSet, err := compileNormalizedRuleConditionBranchWithOuterAndParams(ruleName, ruleID, inputNormalized, templatesByKey, false, conditions, bindingSlots, params, functions)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			inputOnlyConditions := inputSet.conditions[len(conditions):]
			for _, inputCondition := range inputOnlyConditions {
				if _, exists := bindingSlots[inputCondition.binding]; exists {
					return compiledRuleConditionSet{}, &ValidationError{
						RuleName:          ruleName,
						ConditionIndex:    i,
						HasConditionIndex: true,
						Reason:            "aggregate input binding collides with an outer binding",
						Err:               ErrAggregateValidation,
					}
				}
			}
			inputBindingSlots := make(map[string]int, len(inputSet.conditions))
			for j, condition := range inputSet.conditions {
				inputBindingSlots[condition.binding] = j
			}
			compiledSpecs, resultConditions, err := compileAggregateSpecList(ruleName, i, node.aggregate.Specs, inputSet.conditions, inputBindingSlots, templatesByKey, functions)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			for _, result := range resultConditions {
				if _, exists := allBindingSlots[result.binding]; exists {
					return compiledRuleConditionSet{}, &ValidationError{
						RuleName:          ruleName,
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
				result.id = aggregateID
				result.order = firstSlot + j
				conditions = append(conditions, result)
				bindingSlots[result.binding] = firstSlot + j
				allBindingSlots[result.binding] = firstSlot + j
			}
			treeCondition := RuleCondition{id: aggregateID, binding: "accumulate", order: i}
			treeConditions = append(treeConditions, treeCondition)
			branchConditions = append(branchConditions, RuleConditionBranchCondition{
				condition: treeCondition,
				path:      cloneIntPath(node.path),
				visible:   true,
			})
			conditionPlans = append(conditionPlans, compiledConditionPlan{
				id:          aggregateID,
				binding:     "accumulate",
				bindingSlot: firstSlot,
				path:        []int{i},
				aggregate: &compiledAggregatePlan{
					inputPlans: inputSet.conditionPlans,
					specs:      compiledSpecs,
				},
			})
			continue
		}
		if node.isTest {
			if node.test == nil {
				return compiledRuleConditionSet{}, expressionValidationError(ruleName, i, 0, "", "test condition expression is required", nil)
			}
			if expressionSpecReferencesCurrentFact(node.test) {
				return compiledRuleConditionSet{}, expressionValidationError(ruleName, i, 0, "", "test condition cannot reference the current fact", nil)
			}
			if len(conditions) == 0 {
				return compiledRuleConditionSet{}, &ValidationError{
					RuleName:          ruleName,
					ConditionIndex:    i,
					HasConditionIndex: true,
					Reason:            "test condition requires an earlier visible binding",
				}
			}
			_, planPredicate, err := compileExpressionPredicateSpecWithParams(node.test, ruleName, i, 0, nil, conditions, bindingSlots, templatesByKey, params, functions)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			planPredicate.placement = ExpressionPredicatePlacementBetaResidual
			conditionPlans = append(conditionPlans, compiledConditionPlan{
				path:           []int{i},
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
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "condition binding is required",
			}
		}
		if !isValidBindingName(condition.Binding) {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "invalid binding name",
			}
		}
		if _, exists := allBindingSlots[condition.Binding]; exists && !allowDuplicateBindings {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "duplicate binding",
			}
		}
		if node.negated && len(bindingSlots) == 0 {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "not condition requires an earlier positive condition",
			}
		}

		hasName := condition.Name != ""
		hasTemplateKey := condition.TemplateKey != ""
		if hasName == hasTemplateKey {
			return compiledRuleConditionSet{}, &ValidationError{
				RuleName:          ruleName,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "condition target must be either a name or a template key",
			}
		}
		if hasTemplateKey {
			if _, ok := templatesByKey[condition.TemplateKey]; !ok {
				return compiledRuleConditionSet{}, &ValidationError{
					RuleName:          ruleName,
					ConditionIndex:    i,
					HasConditionIndex: true,
					Reason:            "unknown template key",
				}
			}
		}

		target := conditionTarget{}
		indexKind := conditionIndexUnknown
		var template *Template
		if hasName {
			target = conditionTarget{
				kind: conditionTargetName,
				name: condition.Name,
			}
			indexKind = conditionIndexName
		} else {
			target = conditionTarget{
				kind:        conditionTargetTemplateKey,
				templateKey: condition.TemplateKey,
			}
			indexKind = conditionIndexTemplateKey
			templateValue := templatesByKey[condition.TemplateKey]
			template = &templateValue
		}

		constraintSpecs, returnValuePredicates := lowerReturnValueFieldConstraints(condition.FieldConstraints)
		fieldConstraints := make([]FieldConstraint, 0, len(constraintSpecs))
		compiledConstraints := make([]compiledFieldConstraint, 0, len(constraintSpecs))
		for constraintIndex, constraint := range constraintSpecs {
			compiledConstraint, planConstraint, err := compileFieldConstraintSpec(constraint, ruleName, i, constraintIndex, template)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			fieldConstraints = append(fieldConstraints, compiledConstraint)
			compiledConstraints = append(compiledConstraints, planConstraint)
		}

		if !node.visible && listPatternsHaveSegment(condition.ListPatterns) {
			return compiledRuleConditionSet{}, listPatternValidationError(ruleName, i, -1, "list segment binding is not supported inside non-visible conditions", ErrInvalidListPattern)
		}
		listPatterns, compiledListPatterns, listPatternBindings, err := compileListPatternSpecs(condition.ListPatterns, ruleName, i, template, conditions, bindingSlots, params, functions)
		if err != nil {
			return compiledRuleConditionSet{}, err
		}
		for _, result := range listPatternBindings {
			if result.binding == condition.Binding {
				return compiledRuleConditionSet{}, listPatternValidationError(ruleName, i, -1, "list segment binding collides with condition binding", ErrInvalidListPattern)
			}
			if _, exists := allBindingSlots[result.binding]; exists {
				return compiledRuleConditionSet{}, listPatternValidationError(ruleName, i, -1, "list segment binding collides with an existing binding", ErrInvalidListPattern)
			}
		}

		joinConstraints := make([]JoinConstraint, 0, len(condition.JoinConstraints))
		compiledJoins := make([]compiledJoinConstraint, 0, len(condition.JoinConstraints))
		for joinIndex, joinConstraint := range condition.JoinConstraints {
			compiledJoin, planJoin, err := compileJoinConstraintSpec(joinConstraint, ruleName, i, joinIndex, template, conditions, bindingSlots, templatesByKey)
			if err != nil {
				return compiledRuleConditionSet{}, err
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
			compiledPredicate, planPredicate, err := compileExpressionPredicateSpecWithParams(predicate, ruleName, i, predicateIndex, template, conditions, bindingSlots, templatesByKey, params, functions)
			if err != nil {
				return compiledRuleConditionSet{}, err
			}
			predicates = append(predicates, compiledPredicate)
			compiledPredicates = append(compiledPredicates, splitCompiledExpressionPredicate(planPredicate)...)
		}

		conditionID := conditionIDFor(ruleID, i, condition.Binding, condition.Name, condition.TemplateKey, fieldConstraints, compiledListPatterns, joinConstraints, compiledPredicates, node.negated)
		compiledCondition := RuleCondition{
			id:               conditionID,
			binding:          condition.Binding,
			name:             condition.Name,
			templateKey:      condition.TemplateKey,
			fieldConstraints: fieldConstraints,
			listPatterns:     listPatterns,
			joinConstraints:  joinConstraints,
			predicates:       predicates,
			order:            i,
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
			listPatternBindings[j].id = conditionID
			listPatternBindings[j].order = len(conditions)
			conditions = append(conditions, listPatternBindings[j])
			bindingSlots[listPatternBindings[j].binding] = listPatternBindings[j].order
			allBindingSlots[listPatternBindings[j].binding] = listPatternBindings[j].order
			for patternIndex := range compiledListPatterns {
				for elementIndex := range compiledListPatterns[patternIndex].elements {
					if compiledListPatterns[patternIndex].elements[elementIndex].binding == listPatternBindings[j].binding {
						compiledListPatterns[patternIndex].elements[elementIndex].bindingSlot = listPatternBindings[j].order
					}
				}
			}
		}
		treeConditions = append(treeConditions, compiledCondition.clone())
		branchConditions = append(branchConditions, RuleConditionBranchCondition{
			condition: compiledCondition.clone(),
			path:      cloneIntPath(node.path),
			visible:   node.visible,
			negated:   node.negated,
		})
		conditionPlans = append(conditionPlans, compiledConditionPlan{
			id:           conditionID,
			binding:      condition.Binding,
			bindingSlot:  publicBindingSlot,
			path:         []int{i},
			negated:      node.negated,
			target:       target,
			constraints:  compiledConstraints,
			listPatterns: compiledListPatterns,
			joins:        compiledJoins,
			predicates:   compiledPredicates,
			indexable:    true,
			indexKind:    indexKind,
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
			requirement := parts.matches[0].clone()
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
		out.matches = append(out.matches, RuleConditionSpec(condition).clone())
	case *Match:
		if condition == nil {
			return fmt.Errorf("forall requirement is required")
		}
		out.matches = append(out.matches, RuleConditionSpec(*condition).clone())
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
			return CurrentFieldExpr{Field: expression.Field, Path: expression.Path.clone()}
		}
		return expression.clone()
	case *BindingFieldExpr:
		if expression == nil {
			return nil
		}
		return rewriteBindingFieldExpressionsToCurrent(*expression, binding)
	case CallExpr:
		out := expression.clone()
		for i, arg := range out.Args {
			out.Args[i] = rewriteBindingFieldExpressionsToCurrent(arg, binding)
		}
		return out
	case *CallExpr:
		if expression == nil {
			return nil
		}
		out := expression.clone()
		for i, arg := range out.Args {
			out.Args[i] = rewriteBindingFieldExpressionsToCurrent(arg, binding)
		}
		return &out
	case CompareExpr:
		out := expression.clone()
		out.Left = rewriteBindingFieldExpressionsToCurrent(out.Left, binding)
		out.Right = rewriteBindingFieldExpressionsToCurrent(out.Right, binding)
		return out
	case *CompareExpr:
		if expression == nil {
			return nil
		}
		out := expression.clone()
		out.Left = rewriteBindingFieldExpressionsToCurrent(out.Left, binding)
		out.Right = rewriteBindingFieldExpressionsToCurrent(out.Right, binding)
		return &out
	case BooleanExpr:
		out := expression.clone()
		for i, operand := range out.Operands {
			out.Operands[i] = rewriteBindingFieldExpressionsToCurrent(operand, binding)
		}
		return out
	case *BooleanExpr:
		if expression == nil {
			return nil
		}
		out := expression.clone()
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
		if expected[i].binding != actual[i].binding || expected[i].name != actual[i].name || expected[i].templateKey != actual[i].templateKey {
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
			kind:     ConditionTreeKindAnd,
			children: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.children = append(tree.children, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindMatch:
		if shape.conditionIndex < 0 || shape.conditionIndex >= len(conditions) {
			return RuleConditionTree{kind: ConditionTreeKindUnknown}
		}
		return RuleConditionTree{
			kind:     ConditionTreeKindMatch,
			match:    conditions[shape.conditionIndex].clone(),
			hasMatch: true,
		}
	case ConditionTreeKindTest:
		return RuleConditionTree{
			kind:    ConditionTreeKindTest,
			test:    cloneExpressionSpec(shape.test),
			hasTest: true,
		}
	case ConditionTreeKindNot:
		tree := RuleConditionTree{
			kind:     ConditionTreeKindNot,
			children: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.children = append(tree.children, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindOr:
		tree := RuleConditionTree{
			kind:     ConditionTreeKindOr,
			children: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.children = append(tree.children, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindExists:
		tree := RuleConditionTree{
			kind:     ConditionTreeKindExists,
			children: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.children = append(tree.children, buildRuleConditionTree(child, conditions))
		}
		return tree
	case ConditionTreeKindForall:
		tree := RuleConditionTree{
			kind:     ConditionTreeKindForall,
			children: make([]RuleConditionTree, 0, len(shape.children)),
		}
		for _, child := range shape.children {
			tree.children = append(tree.children, buildRuleConditionTree(child, conditions))
		}
		return tree
	default:
		return RuleConditionTree{kind: ConditionTreeKindUnknown}
	}
}

func ruleRevisionIDFor(rule compiledRule) RuleRevisionID {
	sum := sha256.New()
	sum.Write([]byte("gess/rule/v1\n"))
	sum.Write([]byte("id:"))
	sum.Write([]byte(rule.id.String()))
	sum.Write([]byte("\nmodule:"))
	sum.Write([]byte(rule.module.String()))
	sum.Write([]byte("\nname:"))
	sum.Write([]byte(rule.name))
	sum.Write([]byte("\nsalience:"))
	sum.Write(fmt.Appendf(nil, "%d", rule.salience))
	sum.Write([]byte("\nall-actions-skip-binding-freeze:"))
	sum.Write(fmt.Appendf(nil, "%t", rule.allActionsSkipBindingFreeze))
	sum.Write([]byte("\nconditions:"))
	for _, condition := range rule.conditions {
		sum.Write(fmt.Appendf(nil, "%d:", condition.order))
		sum.Write([]byte(condition.binding))
		sum.Write([]byte(":"))
		sum.Write([]byte(condition.name))
		sum.Write([]byte(":"))
		sum.Write([]byte(condition.templateKey.String()))
		sum.Write([]byte(":"))
		for _, constraint := range condition.fieldConstraints {
			sum.Write([]byte(constraint.Field))
			sum.Write([]byte("="))
			sum.Write([]byte(string(constraint.Operator)))
			sum.Write([]byte("="))
			sum.Write([]byte(constraint.Value.String()))
			sum.Write([]byte(","))
		}
		sum.Write([]byte("|"))
		for _, pattern := range condition.listPatterns {
			sum.Write([]byte(pattern.Path().display()))
			sum.Write([]byte(":"))
			for _, element := range pattern.Elements() {
				sum.Write([]byte(string(element.Kind())))
				sum.Write([]byte(":"))
				sum.Write([]byte(element.Binding()))
				sum.Write([]byte(","))
			}
			sum.Write([]byte(";"))
		}
		if condition.order >= 0 && condition.order < len(rule.conditionPlans) && len(rule.conditionPlans[condition.order].listPatterns) > 0 {
			sum.Write([]byte(serializeCompiledListPatterns(rule.conditionPlans[condition.order].listPatterns)))
		}
		sum.Write([]byte("|"))
		for _, join := range condition.joinConstraints {
			sum.Write([]byte(join.Field))
			sum.Write([]byte("="))
			sum.Write([]byte(string(join.Operator)))
			sum.Write([]byte("="))
			sum.Write([]byte(join.Ref.Binding))
			sum.Write([]byte("."))
			sum.Write([]byte(join.Ref.Field))
			sum.Write([]byte(","))
		}
		if condition.order >= 0 && condition.order < len(rule.conditionPlans) && len(rule.conditionPlans[condition.order].predicates) > 0 {
			sum.Write([]byte("|"))
			sum.Write([]byte(serializeCompiledExpressionPredicates(rule.conditionPlans[condition.order].predicates)))
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
			sum.Write(fmt.Appendf(nil, "%d:", condition.order))
			if plan.negated {
				sum.Write([]byte("not:"))
			}
			sum.Write([]byte(condition.binding))
			sum.Write([]byte(":"))
			sum.Write([]byte(condition.name))
			sum.Write([]byte(":"))
			sum.Write([]byte(condition.templateKey.String()))
			sum.Write([]byte(":"))
			for _, constraint := range condition.fieldConstraints {
				sum.Write([]byte(constraint.Field))
				sum.Write([]byte("="))
				sum.Write([]byte(string(constraint.Operator)))
				sum.Write([]byte("="))
				sum.Write([]byte(constraint.Value.String()))
				sum.Write([]byte(","))
			}
			sum.Write([]byte("|"))
			for _, pattern := range condition.listPatterns {
				sum.Write([]byte(pattern.Path().display()))
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
			for _, join := range condition.joinConstraints {
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
		sum.Write(fmt.Appendf(nil, "%d:", action.order))
		sum.Write([]byte(action.name))
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
		if plan.negated {
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
		if condition.order == order {
			return condition, true
		}
	}
	return RuleCondition{}, false
}
