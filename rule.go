package gess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type RuleConditionSpec struct {
	Binding          string
	Name             string
	TemplateKey      TemplateKey
	FieldConstraints []FieldConstraintSpec
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
	case Match:
		return condition.clone()
	case *Match:
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
	out.joinConstraints = make([]JoinConstraint, len(c.joinConstraints))
	for i, constraint := range c.joinConstraints {
		out.joinConstraints[i] = constraint.clone()
	}
	out.predicates = cloneExpressionPredicates(c.predicates)
	return out
}

type ConditionTreeKind string

const (
	ConditionTreeKindUnknown ConditionTreeKind = ""
	ConditionTreeKindAnd     ConditionTreeKind = "and"
	ConditionTreeKindMatch   ConditionTreeKind = "match"
)

type RuleConditionTree struct {
	kind     ConditionTreeKind
	children []RuleConditionTree
	match    RuleCondition
	hasMatch bool
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

func (t RuleConditionTree) clone() RuleConditionTree {
	out := t
	out.children = make([]RuleConditionTree, len(t.children))
	for i, child := range t.children {
		out.children[i] = child.clone()
	}
	out.match = t.match.clone()
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
	id               RuleID
	revisionID       RuleRevisionID
	name             string
	description      string
	tags             []string
	salience         int
	declarationOrder int
	conditions       []RuleCondition
	conditionTree    RuleConditionTree
	actions          []RuleAction
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
	description                 string
	tags                        []string
	salience                    int
	declarationOrder            int
	identityScopeHash           uint64
	conditions                  []RuleCondition
	conditionTree               RuleConditionTree
	conditionTreeShape          compiledConditionTreeShape
	conditionPlans              []compiledConditionPlan
	actions                     []RuleAction
	allActionsSkipBindingFreeze bool
}

func (r compiledRule) inspect() Rule {
	return Rule{
		id:               r.id,
		revisionID:       r.revisionID,
		name:             r.name,
		description:      r.description,
		tags:             append([]string(nil), r.tags...),
		salience:         r.salience,
		declarationOrder: r.declarationOrder,
		conditions:       cloneRuleConditions(r.conditions),
		conditionTree:    r.conditionTree.clone(),
		actions:          append([]RuleAction(nil), r.actions...),
	}
}

func cloneRuleConditions(conditions []RuleCondition) []RuleCondition {
	out := make([]RuleCondition, len(conditions))
	for i, condition := range conditions {
		out[i] = condition.clone()
	}
	return out
}

type compiledConditionTreeShape struct {
	kind           ConditionTreeKind
	children       []compiledConditionTreeShape
	conditionIndex int
}

func (s compiledConditionTreeShape) clone() compiledConditionTreeShape {
	out := s
	out.children = make([]compiledConditionTreeShape, len(s.children))
	for i, child := range s.children {
		out.children[i] = child.clone()
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

func normalizeRuleConditions(spec RuleSpec) ([]RuleConditionSpec, compiledConditionTreeShape, error) {
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

	conditions := make([]RuleConditionSpec, len(spec.Conditions))
	children := make([]compiledConditionTreeShape, len(spec.Conditions))
	for i, condition := range spec.Conditions {
		conditions[i] = condition.clone()
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

func flattenConditionTreeSpec(ruleName string, spec ConditionSpec) ([]RuleConditionSpec, compiledConditionTreeShape, error) {
	conditions := make([]RuleConditionSpec, 0)
	shape, err := flattenConditionTreeNode(ruleName, spec, &conditions)
	if err != nil {
		return nil, compiledConditionTreeShape{}, err
	}
	return conditions, shape, nil
}

func flattenConditionTreeNode(ruleName string, spec ConditionSpec, conditions *[]RuleConditionSpec) (compiledConditionTreeShape, error) {
	switch condition := spec.(type) {
	case nil:
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "condition tree node is required",
		}
	case And:
		return flattenAndConditionTreeNode(ruleName, condition.Conditions, conditions)
	case *And:
		if condition == nil {
			return compiledConditionTreeShape{}, &ValidationError{
				RuleName: ruleName,
				Reason:   "condition tree node is required",
			}
		}
		return flattenAndConditionTreeNode(ruleName, condition.Conditions, conditions)
	case Match:
		index := len(*conditions)
		*conditions = append(*conditions, RuleConditionSpec(condition).clone())
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
		*conditions = append(*conditions, RuleConditionSpec(*condition).clone())
		return compiledConditionTreeShape{
			kind:           ConditionTreeKindMatch,
			conditionIndex: index,
		}, nil
	default:
		return compiledConditionTreeShape{}, &ValidationError{
			RuleName: ruleName,
			Reason:   "unsupported condition tree node",
		}
	}
}

func flattenAndConditionTreeNode(ruleName string, specs []ConditionSpec, conditions *[]RuleConditionSpec) (compiledConditionTreeShape, error) {
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
	for _, spec := range specs {
		child, err := flattenConditionTreeNode(ruleName, spec, conditions)
		if err != nil {
			return compiledConditionTreeShape{}, err
		}
		shape.children = append(shape.children, child)
	}
	return shape, nil
}

func compileRuleSpec(spec RuleSpec, ruleID RuleID, declarationOrder int, templatesByKey map[TemplateKey]Template, actionsByName map[string]compiledAction) (compiledRule, error) {
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

	if len(normalizedConditions) == 0 {
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

	bindingSlots := make(map[string]int, len(normalizedConditions))
	conditions := make([]RuleCondition, 0, len(normalizedConditions))
	conditionPlans := make([]compiledConditionPlan, 0, len(normalizedConditions))
	for i, condition := range normalizedConditions {
		if condition.Binding == "" {
			return compiledRule{}, &ValidationError{
				RuleName:          normalized.Name,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "condition binding is required",
			}
		}
		if !isValidBindingName(condition.Binding) {
			return compiledRule{}, &ValidationError{
				RuleName:          normalized.Name,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "invalid binding name",
			}
		}
		if _, exists := bindingSlots[condition.Binding]; exists {
			return compiledRule{}, &ValidationError{
				RuleName:          normalized.Name,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "duplicate binding",
			}
		}

		hasName := condition.Name != ""
		hasTemplateKey := condition.TemplateKey != ""
		if hasName == hasTemplateKey {
			return compiledRule{}, &ValidationError{
				RuleName:          normalized.Name,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "condition target must be either a name or a template key",
			}
		}
		if hasTemplateKey {
			if _, ok := templatesByKey[condition.TemplateKey]; !ok {
				return compiledRule{}, &ValidationError{
					RuleName:          normalized.Name,
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

		fieldConstraints := make([]FieldConstraint, 0, len(condition.FieldConstraints))
		compiledConstraints := make([]compiledFieldConstraint, 0, len(condition.FieldConstraints))
		for constraintIndex, constraint := range condition.FieldConstraints {
			compiledConstraint, planConstraint, err := compileFieldConstraintSpec(constraint, normalized.Name, i, constraintIndex, template)
			if err != nil {
				return compiledRule{}, err
			}
			fieldConstraints = append(fieldConstraints, compiledConstraint)
			compiledConstraints = append(compiledConstraints, planConstraint)
		}

		joinConstraints := make([]JoinConstraint, 0, len(condition.JoinConstraints))
		compiledJoins := make([]compiledJoinConstraint, 0, len(condition.JoinConstraints))
		for joinIndex, joinConstraint := range condition.JoinConstraints {
			compiledJoin, planJoin, err := compileJoinConstraintSpec(joinConstraint, normalized.Name, i, joinIndex, template, conditions, bindingSlots, templatesByKey)
			if err != nil {
				return compiledRule{}, err
			}
			joinConstraints = append(joinConstraints, compiledJoin)
			compiledJoins = append(compiledJoins, planJoin)
		}

		predicates := make([]ExpressionPredicate, 0, len(condition.Predicates))
		compiledPredicates := make([]compiledExpressionPredicate, 0, len(condition.Predicates))
		for predicateIndex, predicate := range condition.Predicates {
			compiledPredicate, planPredicate, err := compileExpressionPredicateSpec(predicate, normalized.Name, i, predicateIndex, template, conditions, bindingSlots, templatesByKey)
			if err != nil {
				return compiledRule{}, err
			}
			predicates = append(predicates, compiledPredicate)
			compiledPredicates = append(compiledPredicates, planPredicate)
		}

		conditionID := conditionIDFor(ruleID, i, condition.Binding, condition.Name, condition.TemplateKey, fieldConstraints, joinConstraints, compiledPredicates)
		conditions = append(conditions, RuleCondition{
			id:               conditionID,
			binding:          condition.Binding,
			name:             condition.Name,
			templateKey:      condition.TemplateKey,
			fieldConstraints: fieldConstraints,
			joinConstraints:  joinConstraints,
			predicates:       predicates,
			order:            i,
		})
		conditionPlans = append(conditionPlans, compiledConditionPlan{
			id:          conditionID,
			binding:     condition.Binding,
			bindingSlot: i,
			path:        []int{i},
			target:      target,
			constraints: compiledConstraints,
			joins:       compiledJoins,
			predicates:  compiledPredicates,
			indexable:   true,
			indexKind:   indexKind,
		})
		bindingSlots[condition.Binding] = i
	}
	conditionTree := buildRuleConditionTree(conditionTreeShape, conditions)

	actions := make([]RuleAction, 0, len(normalized.Actions))
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
		actions = append(actions, RuleAction{
			name:  action.Name,
			order: i,
		})
	}

	compiled := compiledRule{
		id:                          ruleID,
		name:                        normalized.Name,
		description:                 normalized.Description,
		tags:                        append([]string(nil), normalized.Tags...),
		salience:                    normalized.Salience,
		declarationOrder:            declarationOrder,
		conditions:                  conditions,
		conditionTree:               conditionTree,
		conditionTreeShape:          conditionTreeShape.clone(),
		conditionPlans:              conditionPlans,
		actions:                     actions,
		allActionsSkipBindingFreeze: allActionsSkipBindingFreeze,
	}
	compiled.revisionID = ruleRevisionIDFor(compiled)
	compiled.identityScopeHash = candidateIdentityScopeHash(compiled.id, compiled.revisionID)
	return compiled, nil
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
	default:
		return RuleConditionTree{kind: ConditionTreeKindUnknown}
	}
}

func ruleRevisionIDFor(rule compiledRule) RuleRevisionID {
	sum := sha256.New()
	sum.Write([]byte("gess/rule/v1\n"))
	sum.Write([]byte("id:"))
	sum.Write([]byte(rule.id.String()))
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
	sum.Write([]byte("\nactions:"))
	for _, action := range rule.actions {
		sum.Write(fmt.Appendf(nil, "%d:", action.order))
		sum.Write([]byte(action.name))
		sum.Write([]byte(";"))
	}
	return RuleRevisionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}
