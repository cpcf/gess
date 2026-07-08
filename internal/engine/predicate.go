package engine

import gessrules "github.com/cpcf/gess/rules"

type FieldConstraintOperator = gessrules.FieldConstraintOperator

const (
	FieldConstraintOpUnknown                                = gessrules.FieldConstraintOpUnknown
	FieldConstraintOpExists                                 = gessrules.FieldConstraintOpExists
	FieldConstraintOpEqual                                  = gessrules.FieldConstraintOpEqual
	FieldConstraintOpNotEqual                               = gessrules.FieldConstraintOpNotEqual
	FieldConstraintOpLessThan                               = gessrules.FieldConstraintOpLessThan
	FieldConstraintOpLessOrEqual                            = gessrules.FieldConstraintOpLessOrEqual
	FieldConstraintOpGreaterThan                            = gessrules.FieldConstraintOpGreaterThan
	FieldConstraintOpGreaterOrEqual                         = gessrules.FieldConstraintOpGreaterOrEqual
	fieldConstraintOpIn             FieldConstraintOperator = "in"

	FieldConstraintExists         = gessrules.FieldConstraintExists
	FieldConstraintEqual          = gessrules.FieldConstraintEqual
	FieldConstraintNotEqual       = gessrules.FieldConstraintNotEqual
	FieldConstraintLessThan       = gessrules.FieldConstraintLessThan
	FieldConstraintLessOrEqual    = gessrules.FieldConstraintLessOrEqual
	FieldConstraintGreaterThan    = gessrules.FieldConstraintGreaterThan
	FieldConstraintGreaterOrEqual = gessrules.FieldConstraintGreaterOrEqual
)

type FieldConstraintSpec = gessrules.FieldConstraintSpec

type RuleFieldConstraintSpec = FieldConstraintSpec

func cloneFieldConstraintSpec(s FieldConstraintSpec) FieldConstraintSpec {
	return gessrules.CloneFieldConstraintSpec(s)
}

type FieldConstraint = gessrules.FieldConstraint

type RuleFieldConstraint = FieldConstraint

func cloneFieldConstraint(c FieldConstraint) FieldConstraint {
	return FieldConstraint{
		Field:    c.Field,
		Path:     clonePathSpec(c.Path),
		Operator: c.Operator,
		Value:    cloneValue(c.Value),
	}
}

type compiledFieldConstraint struct {
	operator FieldConstraintOperator
	value    Value
	values   []Value
	access   compiledPathAccess
}

func fieldConstraintOperatorValid(o FieldConstraintOperator) bool {
	switch o {
	case FieldConstraintOpExists, FieldConstraintOpEqual, FieldConstraintOpNotEqual,
		FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan,
		FieldConstraintOpGreaterOrEqual:
		return true
	default:
		return false
	}
}

func compileFieldConstraintSpec(spec FieldConstraintSpec, ruleName string, conditionIndex, constraintIndex int, template *compiledTemplate) (FieldConstraint, compiledFieldConstraint, error) {
	if hasAmbiguousFieldAndPath(spec.Field, spec.Path) {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "field constraint cannot set both field and path",
			Err:                ErrInvalidPath,
		}
	}
	normalized := cloneFieldConstraintSpec(spec)
	normalized.Path = pathOrField(normalized.Path, normalized.Field)
	if pathIsZero(normalized.Path) {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "field name is required",
		}
	}
	normalized.Field = pathRoot(normalized.Path)
	if !fieldConstraintOperatorValid(normalized.Operator) {
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
		access, err := compileFieldConstraintPathAccess(normalized.Path, ruleName, conditionIndex, constraintIndex, template)
		if err != nil {
			return FieldConstraint{}, compiledFieldConstraint{}, err
		}

		return FieldConstraint{
				Field:    normalized.Field,
				Path:     clonePathSpec(normalized.Path),
				Operator: normalized.Operator,
				Value:    NullValue(),
			}, compiledFieldConstraint{
				operator: normalized.Operator,
				value:    NullValue(),
				access:   access,
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
	access, err := compileFieldConstraintPathAccess(normalized.Path, ruleName, conditionIndex, constraintIndex, template)
	if err != nil {
		return FieldConstraint{}, compiledFieldConstraint{}, err
	}

	return FieldConstraint{
			Field:    normalized.Field,
			Path:     clonePathSpec(normalized.Path),
			Operator: normalized.Operator,
			Value:    cloneValue(value),
		}, compiledFieldConstraint{
			operator: normalized.Operator,
			value:    value,
			access:   access,
		}, nil
}

func compileFieldConstraintPathAccess(path PathSpec, ruleName string, conditionIndex, constraintIndex int, template *compiledTemplate) (compiledPathAccess, error) {
	if template != nil && template.closed && pathRoot(path) != "" {
		if _, ok := template.fieldSlot(pathRoot(path)); !ok {
			return compiledPathAccess{}, &ValidationError{
				RuleName:           ruleName,
				TemplateName:       template.name,
				FieldName:          pathRoot(path),
				ConditionIndex:     conditionIndex,
				HasConditionIndex:  true,
				ConstraintIndex:    constraintIndex,
				HasConstraintIndex: true,
				Reason:             "unknown field",
			}
		}
	}
	access, _, err := compilePathAccess(path, template)
	if err != nil {
		validation := &ValidationError{
			RuleName:           ruleName,
			FieldName:          pathRoot(path),
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "invalid path",
			Err:                err,
		}
		if template != nil {
			validation.TemplateName = template.name
		}
		return compiledPathAccess{}, validation
	}
	return access, nil
}

func (c compiledFieldConstraint) matches(fact conditionFactRef) bool {
	value, ok := c.valueFromFact(fact)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesWithCounters(fact conditionFactRef, span *propagationCounterSpan) bool {
	value, ok := c.valueFromFactWithCounters(fact, span)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesValue(value Value, ok bool) bool {
	switch c.operator {
	case FieldConstraintOpExists:
		return ok
	case FieldConstraintOpEqual:
		return ok && valuesComparableForEquality(value, c.value) && value.Equal(c.value)
	case FieldConstraintOpNotEqual:
		return ok && valuesComparableForEquality(value, c.value) && !value.Equal(c.value)
	case fieldConstraintOpIn:
		if !ok {
			return false
		}
		for _, allowed := range c.values {
			if valuesComparableForEquality(value, allowed) && value.Equal(allowed) {
				return true
			}
		}
		return false
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

func (c compiledFieldConstraint) matchesWorking(fact *workingFact, compactSlotStore *factCompactSlotStore) bool {
	value, ok := c.valueFromWorkingFact(fact, compactSlotStore)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesWorkingWithCounters(fact *workingFact, compactSlotStore *factCompactSlotStore, span *propagationCounterSpan) bool {
	value, ok := c.valueFromWorkingFactWithCounters(fact, compactSlotStore, span)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) valueFromFact(fact conditionFactRef) (Value, bool) {
	return c.access.valueFromFact(fact)
}

func (c compiledFieldConstraint) valueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	return c.access.valueFromFactWithCounters(fact, span)
}

func (c compiledFieldConstraint) valueFromWorkingFact(fact *workingFact, compactSlotStore *factCompactSlotStore) (Value, bool) {
	return c.access.valueFromWorkingFact(fact, compactSlotStore)
}

func (c compiledFieldConstraint) valueFromWorkingFactWithCounters(fact *workingFact, compactSlotStore *factCompactSlotStore, span *propagationCounterSpan) (Value, bool) {
	return c.access.valueFromWorkingFactWithCounters(fact, compactSlotStore, span)
}

func valuesComparableForEquality(left, right Value) bool {
	if isNumericValue(left) && isNumericValue(right) {
		return true
	}
	return left.Kind() == right.Kind()
}
