package gess

import (
	"fmt"
	"strings"
)

type FieldRef struct {
	Binding string
	Field   string
}

func (r FieldRef) clone() FieldRef {
	out := r
	out.Binding = strings.TrimSpace(out.Binding)
	out.Field = strings.TrimSpace(out.Field)
	return out
}

type JoinConstraintSpec struct {
	Field    string
	Operator FieldConstraintOperator
	Ref      FieldRef
}

func (s JoinConstraintSpec) clone() JoinConstraintSpec {
	out := s
	out.Field = strings.TrimSpace(out.Field)
	out.Ref = out.Ref.clone()
	return out
}

type JoinConstraint struct {
	Field    string
	Operator FieldConstraintOperator
	Ref      FieldRef
}

func (c JoinConstraint) clone() JoinConstraint {
	return JoinConstraint{
		Field:    c.Field,
		Operator: c.Operator,
		Ref:      c.Ref.clone(),
	}
}

type joinIndexKind uint8

const (
	joinIndexUnknown joinIndexKind = iota
	joinIndexEquality
	joinIndexNumericComparison
)

type compiledJoinConstraint struct {
	path           []int
	bindingSlot    int
	field          string
	fieldSlot      int
	operator       FieldConstraintOperator
	refBinding     string
	refBindingSlot int
	refField       string
	refFieldSlot   int
	indexable      bool
	indexKind      joinIndexKind
}

func (c compiledJoinConstraint) isHashJoin() bool {
	return c.indexKind == joinIndexEquality
}

func (c compiledJoinConstraint) matchesToken(fact conditionFactRef, bindings tokenRef) (bool, error) {
	if c.refBindingSlot < 0 {
		return false, fmt.Errorf("%w: malformed join binding slot %d", ErrMatcher, c.refBindingSlot)
	}
	match, ok := tokenRefAtSlot(bindings, c.refBindingSlot)
	if !ok {
		return false, nil
	}

	left, ok := fact.compiledFieldValue(c.field, c.fieldSlot)
	if !ok {
		return false, nil
	}

	right, ok := match.fact.compiledFieldValue(c.refField, c.refFieldSlot)
	if !ok {
		return false, nil
	}

	switch c.operator {
	case FieldConstraintOpEqual:
		return valuesComparableForEquality(left, right) && left.Equal(right), nil
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if !isNumericValue(left) || !isNumericValue(right) {
			return false, nil
		}
		comparison := compareNumericValues(left, right)
		switch c.operator {
		case FieldConstraintOpLessThan:
			return comparison < 0, nil
		case FieldConstraintOpLessOrEqual:
			return comparison <= 0, nil
		case FieldConstraintOpGreaterThan:
			return comparison > 0, nil
		case FieldConstraintOpGreaterOrEqual:
			return comparison >= 0, nil
		}
	}

	return false, nil
}

func validJoinOperator(operator FieldConstraintOperator) bool {
	switch operator {
	case FieldConstraintOpEqual, FieldConstraintOpLessThan,
		FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		return true
	default:
		return false
	}
}

func compileJoinConstraintSpec(
	spec JoinConstraintSpec,
	ruleName string,
	conditionIndex, joinIndex int,
	template *Template,
	conditions []RuleCondition,
	bindingSlots map[string]int,
	templatesByKey map[TemplateKey]Template,
) (JoinConstraint, compiledJoinConstraint, error) {
	normalized := spec.clone()
	if normalized.Field == "" {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join field name is required",
		}
	}
	if normalized.Ref.Binding == "" {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join binding reference is required",
		}
	}
	if normalized.Ref.Field == "" {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join field reference is required",
		}
	}
	if !validJoinOperator(normalized.Operator) {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "invalid join operator",
		}
	}

	fieldSlot := -1
	if template != nil && template.closed {
		slot, ok := template.fieldSlot(normalized.Field)
		if !ok {
			return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
				RuleName:          ruleName,
				TemplateName:      template.name,
				FieldName:         normalized.Field,
				ConditionIndex:    conditionIndex,
				HasConditionIndex: true,
				JoinIndex:         joinIndex,
				HasJoinIndex:      true,
				Reason:            "unknown field",
			}
		}
		fieldSlot = slot
	}

	refSlot, ok := bindingSlots[normalized.Ref.Binding]
	if !ok {
		return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
			RuleName:          ruleName,
			ConditionIndex:    conditionIndex,
			HasConditionIndex: true,
			JoinIndex:         joinIndex,
			HasJoinIndex:      true,
			Reason:            "join binding reference must refer to an earlier condition",
		}
	}
	if refSlot < 0 || refSlot >= len(conditions) {
		return JoinConstraint{}, compiledJoinConstraint{}, fmt.Errorf("%w: malformed join binding slot %d", ErrMatcher, refSlot)
	}

	refCondition := conditions[refSlot]
	refFieldSlot := -1
	if refCondition.templateKey != "" {
		refTemplate, ok := templatesByKey[refCondition.templateKey]
		if !ok {
			return JoinConstraint{}, compiledJoinConstraint{}, fmt.Errorf("%w: missing template for join binding %q", ErrMatcher, normalized.Ref.Binding)
		}
		if refTemplate.closed {
			slot, ok := refTemplate.fieldSlot(normalized.Ref.Field)
			if !ok {
				return JoinConstraint{}, compiledJoinConstraint{}, &ValidationError{
					RuleName:          ruleName,
					TemplateName:      refTemplate.name,
					FieldName:         normalized.Ref.Field,
					ConditionIndex:    conditionIndex,
					HasConditionIndex: true,
					JoinIndex:         joinIndex,
					HasJoinIndex:      true,
					Reason:            "unknown field",
				}
			}
			refFieldSlot = slot
		}
	}

	indexKind := joinIndexEquality
	switch normalized.Operator {
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		indexKind = joinIndexNumericComparison
	}

	return JoinConstraint{
			Field:    normalized.Field,
			Operator: normalized.Operator,
			Ref:      normalized.Ref.clone(),
		}, compiledJoinConstraint{
			path:           []int{conditionIndex, joinIndex},
			bindingSlot:    conditionIndex,
			field:          normalized.Field,
			fieldSlot:      fieldSlot,
			operator:       normalized.Operator,
			refBinding:     normalized.Ref.Binding,
			refBindingSlot: refSlot,
			refField:       normalized.Ref.Field,
			refFieldSlot:   refFieldSlot,
			indexable:      true,
			indexKind:      indexKind,
		}, nil
}

func (c compiledJoinConstraint) matches(fact conditionFactRef, bindings []conditionMatch) (bool, error) {
	if c.refBindingSlot < 0 {
		return false, fmt.Errorf("%w: malformed join binding slot %d", ErrMatcher, c.refBindingSlot)
	}
	if c.refBindingSlot >= len(bindings) {
		return false, nil
	}

	left, ok := fact.compiledFieldValue(c.field, c.fieldSlot)
	if !ok {
		return false, nil
	}

	rightFact := bindings[c.refBindingSlot].fact
	right, ok := rightFact.compiledFieldValue(c.refField, c.refFieldSlot)
	if !ok {
		return false, nil
	}

	switch c.operator {
	case FieldConstraintOpEqual:
		return valuesComparableForEquality(left, right) && left.Equal(right), nil
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if !isNumericValue(left) || !isNumericValue(right) {
			return false, nil
		}
		comparison := compareNumericValues(left, right)
		switch c.operator {
		case FieldConstraintOpLessThan:
			return comparison < 0, nil
		case FieldConstraintOpLessOrEqual:
			return comparison <= 0, nil
		case FieldConstraintOpGreaterThan:
			return comparison > 0, nil
		case FieldConstraintOpGreaterOrEqual:
			return comparison >= 0, nil
		}
	}

	return false, nil
}
