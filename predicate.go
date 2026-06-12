package gess

import "strings"

type FieldConstraintOperator string

const (
	FieldConstraintOpUnknown        FieldConstraintOperator = ""
	FieldConstraintOpExists         FieldConstraintOperator = "exists"
	FieldConstraintOpEqual          FieldConstraintOperator = "eq"
	FieldConstraintOpNotEqual       FieldConstraintOperator = "neq"
	FieldConstraintOpLessThan       FieldConstraintOperator = "lt"
	FieldConstraintOpLessOrEqual    FieldConstraintOperator = "lte"
	FieldConstraintOpGreaterThan    FieldConstraintOperator = "gt"
	FieldConstraintOpGreaterOrEqual FieldConstraintOperator = "gte"

	FieldConstraintExists         = FieldConstraintOpExists
	FieldConstraintEqual          = FieldConstraintOpEqual
	FieldConstraintNotEqual       = FieldConstraintOpNotEqual
	FieldConstraintLessThan       = FieldConstraintOpLessThan
	FieldConstraintLessOrEqual    = FieldConstraintOpLessOrEqual
	FieldConstraintGreaterThan    = FieldConstraintOpGreaterThan
	FieldConstraintGreaterOrEqual = FieldConstraintOpGreaterOrEqual
)

type FieldConstraintSpec struct {
	Field    string
	Operator FieldConstraintOperator
	Value    any
}

type RuleFieldConstraintSpec = FieldConstraintSpec

func (s FieldConstraintSpec) clone() FieldConstraintSpec {
	out := s
	out.Field = strings.TrimSpace(out.Field)
	out.Value = cloneSpecValue(out.Value)
	return out
}

type FieldConstraint struct {
	Field    string
	Operator FieldConstraintOperator
	Value    Value
}

type RuleFieldConstraint = FieldConstraint

func (c FieldConstraint) clone() FieldConstraint {
	return FieldConstraint{
		Field:    c.Field,
		Operator: c.Operator,
		Value:    cloneValue(c.Value),
	}
}

type compiledFieldConstraint struct {
	field    string
	operator FieldConstraintOperator
	value    Value
}

func (o FieldConstraintOperator) valid() bool {
	switch o {
	case FieldConstraintOpExists, FieldConstraintOpEqual, FieldConstraintOpNotEqual,
		FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan,
		FieldConstraintOpGreaterOrEqual:
		return true
	default:
		return false
	}
}

func compileFieldConstraintSpec(spec FieldConstraintSpec, ruleName string, conditionIndex, constraintIndex int, template *Template) (FieldConstraint, compiledFieldConstraint, error) {
	normalized := spec.clone()
	if normalized.Field == "" {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "field name is required",
		}
	}
	if !normalized.Operator.valid() {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "invalid field constraint operator",
		}
	}

	if normalized.Operator == FieldConstraintOpExists {
		if normalized.Value != nil {
			return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
				RuleName:           ruleName,
				ConditionIndex:     conditionIndex,
				HasConditionIndex:  true,
				ConstraintIndex:    constraintIndex,
				HasConstraintIndex: true,
				Reason:             "exists constraint must not set a value",
			}
		}
		if template != nil && template.closed {
			if _, ok := template.fieldsByName[normalized.Field]; !ok {
				return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
					RuleName:           ruleName,
					TemplateName:       template.name,
					FieldName:          normalized.Field,
					ConditionIndex:     conditionIndex,
					HasConditionIndex:  true,
					ConstraintIndex:    constraintIndex,
					HasConstraintIndex: true,
					Reason:             "unknown field",
				}
			}
		}

		return FieldConstraint{
				Field:    normalized.Field,
				Operator: normalized.Operator,
				Value:    NullValue(),
			}, compiledFieldConstraint{
				field:    normalized.Field,
				operator: normalized.Operator,
				value:    NullValue(),
			}, nil
	}

	value, err := canonicalValue(normalized.Value)
	if err != nil {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "invalid constraint value",
			Err:                err,
		}
	}
	if template != nil && template.closed {
		if _, ok := template.fieldsByName[normalized.Field]; !ok {
			return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
				RuleName:           ruleName,
				TemplateName:       template.name,
				FieldName:          normalized.Field,
				ConditionIndex:     conditionIndex,
				HasConditionIndex:  true,
				ConstraintIndex:    constraintIndex,
				HasConstraintIndex: true,
				Reason:             "unknown field",
			}
		}
	}

	return FieldConstraint{
			Field:    normalized.Field,
			Operator: normalized.Operator,
			Value:    cloneValue(value),
		}, compiledFieldConstraint{
			field:    normalized.Field,
			operator: normalized.Operator,
			value:    value,
		}, nil
}

func (c compiledFieldConstraint) matches(fact FactSnapshot) bool {
	value, ok := fact.fields[c.field]
	switch c.operator {
	case FieldConstraintOpExists:
		return ok
	case FieldConstraintOpEqual:
		return ok && valuesComparableForEquality(value, c.value) && value.Equal(c.value)
	case FieldConstraintOpNotEqual:
		return ok && valuesComparableForEquality(value, c.value) && !value.Equal(c.value)
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if !ok {
			return false
		}
		comparison, comparable := compareValues(value, c.value)
		if !comparable {
			return false
		}
		switch c.operator {
		case FieldConstraintOpLessThan:
			return comparison < 0
		case FieldConstraintOpLessOrEqual:
			return comparison <= 0
		case FieldConstraintOpGreaterThan:
			return comparison > 0
		case FieldConstraintOpGreaterOrEqual:
			return comparison >= 0
		}
	default:
		return false
	}
	return false
}

func valuesComparableForEquality(left, right Value) bool {
	if isNumericValue(left) && isNumericValue(right) {
		return true
	}
	return left.Kind() == right.Kind()
}
