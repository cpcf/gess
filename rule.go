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
	return out
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
	Conditions  []RuleConditionSpec
	Actions     []RuleActionSpec
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

func (c RuleCondition) DeclarationOrder() int {
	return c.order
}

func (c RuleCondition) clone() RuleCondition {
	out := c
	out.fieldConstraints = make([]FieldConstraint, len(c.fieldConstraints))
	for i, constraint := range c.fieldConstraints {
		out.fieldConstraints[i] = constraint.clone()
	}
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
	out.actions = make([]RuleAction, len(r.actions))
	for i, action := range r.actions {
		out.actions[i] = action.clone()
	}
	return out
}

type compiledRule struct {
	id               RuleID
	revisionID       RuleRevisionID
	name             string
	description      string
	tags             []string
	salience         int
	declarationOrder int
	conditions       []RuleCondition
	conditionPlans   []compiledConditionPlan
	actions          []RuleAction
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

func normalizeRuleSpec(spec RuleSpec) (RuleSpec, error) {
	normalized := spec.clone()
	if normalized.Name == "" {
		return RuleSpec{}, &ValidationError{
			Reason: "rule name is required",
		}
	}
	return normalized, nil
}

func compileRuleSpec(spec RuleSpec, ruleID RuleID, declarationOrder int, templatesByKey map[TemplateKey]Template, actionsByName map[string]compiledAction) (compiledRule, error) {
	normalized, err := normalizeRuleSpec(spec)
	if err != nil {
		return compiledRule{}, err
	}
	if ruleID.IsZero() {
		ruleID = RuleID(normalized.Name)
	}

	if len(normalized.Conditions) == 0 {
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

	seenBindings := make(map[string]struct{}, len(normalized.Conditions))
	conditions := make([]RuleCondition, 0, len(normalized.Conditions))
	conditionPlans := make([]compiledConditionPlan, 0, len(normalized.Conditions))
	for i, condition := range normalized.Conditions {
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
		if _, exists := seenBindings[condition.Binding]; exists {
			return compiledRule{}, &ValidationError{
				RuleName:          normalized.Name,
				ConditionIndex:    i,
				HasConditionIndex: true,
				Reason:            "duplicate binding",
			}
		}
		seenBindings[condition.Binding] = struct{}{}

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

		conditionID := conditionIDFor(ruleID, i, condition.Binding, condition.Name, condition.TemplateKey, fieldConstraints)
		conditions = append(conditions, RuleCondition{
			id:               conditionID,
			binding:          condition.Binding,
			name:             condition.Name,
			templateKey:      condition.TemplateKey,
			fieldConstraints: fieldConstraints,
			order:            i,
		})
		conditionPlans = append(conditionPlans, compiledConditionPlan{
			id:          conditionID,
			binding:     condition.Binding,
			bindingSlot: i,
			path:        []int{i},
			target:      target,
			constraints: compiledConstraints,
			indexable:   true,
			indexKind:   indexKind,
		})
	}

	actions := make([]RuleAction, 0, len(normalized.Actions))
	for i, action := range normalized.Actions {
		if action.Name == "" {
			return compiledRule{}, &ValidationError{
				RuleName:       normalized.Name,
				ActionIndex:    i,
				HasActionIndex: true,
				Reason:         "action name is required",
			}
		}
		if _, ok := actionsByName[action.Name]; !ok {
			return compiledRule{}, &ValidationError{
				RuleName:       normalized.Name,
				ActionIndex:    i,
				HasActionIndex: true,
				Reason:         "unknown action",
			}
		}
		actions = append(actions, RuleAction{
			name:  action.Name,
			order: i,
		})
	}

	compiled := compiledRule{
		id:               ruleID,
		name:             normalized.Name,
		description:      normalized.Description,
		tags:             append([]string(nil), normalized.Tags...),
		salience:         normalized.Salience,
		declarationOrder: declarationOrder,
		conditions:       conditions,
		conditionPlans:   conditionPlans,
		actions:          actions,
	}
	compiled.revisionID = ruleRevisionIDFor(compiled)
	return compiled, nil
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
