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
	Path     PathSpec
	Operator FieldConstraintOperator
	Value    any
}

type RuleFieldConstraintSpec = FieldConstraintSpec

func (s FieldConstraintSpec) clone() FieldConstraintSpec {
	out := s
	out.Field = strings.TrimSpace(out.Field)
	out.Path = out.Path.clone()
	out.Value = cloneSpecValue(out.Value)
	return out
}

type FieldConstraint struct {
	Field    string
	Path     PathSpec
	Operator FieldConstraintOperator
	Value    Value
}

type RuleFieldConstraint = FieldConstraint

func (c FieldConstraint) clone() FieldConstraint {
	return FieldConstraint{
		Field:    c.Field,
		Path:     c.Path.clone(),
		Operator: c.Operator,
		Value:    cloneValue(c.Value),
	}
}

type compiledFieldConstraint struct {
	field     string
	operator  FieldConstraintOperator
	value     Value
	fieldSlot int
	access    compiledPathAccess
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
	normalized := spec.clone()
	normalized.Path = pathOrField(normalized.Path, normalized.Field)
	if normalized.Path.isZero() {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "field name is required",
		}
	}
	normalized.Field = normalized.Path.root()
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
		access, err := compileFieldConstraintPathAccess(normalized.Path, ruleName, conditionIndex, constraintIndex, template)
		if err != nil {
			return FieldConstraint{}, compiledFieldConstraint{}, err
		}

		return FieldConstraint{
				Field:    normalized.Field,
				Path:     normalized.Path.clone(),
				Operator: normalized.Operator,
				Value:    NullValue(),
			}, compiledFieldConstraint{
				field:     normalized.Field,
				operator:  normalized.Operator,
				value:     NullValue(),
				fieldSlot: access.rootSlot,
				access:    access,
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
			Path:     normalized.Path.clone(),
			Operator: normalized.Operator,
			Value:    cloneValue(value),
		}, compiledFieldConstraint{
			field:     normalized.Field,
			operator:  normalized.Operator,
			value:     value,
			fieldSlot: access.rootSlot,
			access:    access,
		}, nil
}

func compileFieldConstraintPathAccess(path PathSpec, ruleName string, conditionIndex, constraintIndex int, template *Template) (compiledPathAccess, error) {
	if template != nil && template.closed && path.root() != "" {
		if _, ok := template.fieldSlot(path.root()); !ok {
			return compiledPathAccess{}, &ValidationError{
				RuleName:           ruleName,
				TemplateName:       template.name,
				FieldName:          path.root(),
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
			FieldName:          path.root(),
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

func (c compiledFieldConstraint) matchesWorking(fact *workingFact) bool {
	value, ok := c.valueFromWorkingFact(fact)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesWorkingWithCounters(fact *workingFact, span *propagationCounterSpan) bool {
	value, ok := c.valueFromWorkingFactWithCounters(fact, span)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) valueFromFact(fact conditionFactRef) (Value, bool) {
	if !c.access.path.isZero() {
		return c.access.valueFromFact(fact)
	}
	return fact.compiledFieldValue(c.field, c.fieldSlot)
}

func (c compiledFieldConstraint) valueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	if !c.access.path.isZero() {
		return c.access.valueFromFactWithCounters(fact, span)
	}
	return fact.compiledFieldValue(c.field, c.fieldSlot)
}

func (c compiledFieldConstraint) valueFromWorkingFact(fact *workingFact) (Value, bool) {
	if !c.access.path.isZero() {
		return c.access.valueFromWorkingFact(fact)
	}
	return fact.compiledFieldValue(c.field, c.fieldSlot)
}

func (c compiledFieldConstraint) valueFromWorkingFactWithCounters(fact *workingFact, span *propagationCounterSpan) (Value, bool) {
	if !c.access.path.isZero() {
		return c.access.valueFromWorkingFactWithCounters(fact, span)
	}
	return fact.compiledFieldValue(c.field, c.fieldSlot)
}

func valuesComparableForEquality(left, right Value) bool {
	if isNumericValue(left) && isNumericValue(right) {
		return true
	}
	return left.Kind() == right.Kind()
}
