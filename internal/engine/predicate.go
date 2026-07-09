package engine

import (
	"fmt"

	gessrules "github.com/cpcf/gess/rules"
)

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

func compileFieldConstraintSpec(spec FieldConstraintSpec, source SourceSpan, ruleName string, conditionIndex, constraintIndex int, template *compiledTemplate) (FieldConstraint, compiledFieldConstraint, error) {
	if hasAmbiguousFieldAndPath(spec.Field, spec.Path) {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			Source:             source,
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
			Source:             source,
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
			Source:             source,
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
				Source:             source,
				ConditionIndex:     conditionIndex,
				HasConditionIndex:  true,
				ConstraintIndex:    constraintIndex,
				HasConstraintIndex: true,
				Reason:             "exists constraint must not set a value",
			}
		}
		access, err := compileFieldConstraintPathAccess(normalized.Path, source, ruleName, conditionIndex, constraintIndex, template)
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
			Source:             source,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             "invalid constraint value",
			Err:                err,
		}
	}
	access, err := compileFieldConstraintPathAccess(normalized.Path, source, ruleName, conditionIndex, constraintIndex, template)
	if err != nil {
		return FieldConstraint{}, compiledFieldConstraint{}, err
	}
	if reason, ok := fieldConstraintKindMismatch(normalized, value, template); !ok {
		return FieldConstraint{}, compiledFieldConstraint{}, &ValidationError{
			RuleName:           ruleName,
			Source:             source,
			ConditionIndex:     conditionIndex,
			HasConditionIndex:  true,
			ConstraintIndex:    constraintIndex,
			HasConstraintIndex: true,
			Reason:             reason,
		}
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

// fieldConstraintKindMismatch reports whether a constraint over a declared,
// simple field can never hold because the constraint value's kind is not
// comparable with the field's kind. Nested path leaves and ANY-kinded fields
// are unchecked; a mismatch there stays a runtime non-match.
func fieldConstraintKindMismatch(spec FieldConstraintSpec, value Value, template *compiledTemplate) (string, bool) {
	if template == nil || len(spec.Path.Segments) != 1 {
		return "", true
	}
	fieldKind, declared := template.fieldKind(spec.Field)
	if !declared || fieldKind == ValueAny {
		return "", true
	}
	valueKind := value.Kind()
	numeric := func(kind ValueKind) bool { return kind == ValueInt || kind == ValueFloat }
	switch spec.Operator {
	case FieldConstraintOpEqual, FieldConstraintOpNotEqual:
		if valueKind == ValueNull || valueKind == fieldKind || (numeric(valueKind) && numeric(fieldKind)) {
			return "", true
		}
		return fmt.Sprintf("constraint value kind %s can never equal field %q of kind %s",
			valueKind, spec.Field, fieldKind), false
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual,
		FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if (numeric(valueKind) && numeric(fieldKind)) ||
			(valueKind == ValueString && fieldKind == ValueString) {
			return "", true
		}
		return fmt.Sprintf("constraint value kind %s cannot be ordered against field %q of kind %s",
			valueKind, spec.Field, fieldKind), false
	default:
		return "", true
	}
}

func compileFieldConstraintPathAccess(path PathSpec, source SourceSpan, ruleName string, conditionIndex, constraintIndex int, template *compiledTemplate) (compiledPathAccess, error) {
	if template != nil && template.closed && pathRoot(path) != "" {
		if _, ok := template.fieldSlot(pathRoot(path)); !ok {
			return compiledPathAccess{}, &ValidationError{
				RuleName:           ruleName,
				Source:             source,
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
			Source:             source,
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

func (c compiledFieldConstraint) matches(fact conditionFactRef) (bool, error) {
	value, ok := c.valueFromFact(fact)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesWithCounters(fact conditionFactRef, span *propagationCounterSpan) (bool, error) {
	value, ok := c.valueFromFactWithCounters(fact, span)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesValue(value Value, ok bool) (bool, error) {
	switch c.operator {
	case FieldConstraintOpExists:
		return ok, nil
	case FieldConstraintOpEqual:
		if err := c.validateComparisonOperands(value, ok); err != nil {
			return false, err
		}
		return value.Equal(c.value), nil
	case FieldConstraintOpNotEqual:
		if err := c.validateComparisonOperands(value, ok); err != nil {
			return false, err
		}
		return !value.Equal(c.value), nil
	case fieldConstraintOpIn:
		if !ok {
			return false, c.missingOperandError()
		}
		comparable := false
		for _, allowed := range c.values {
			if !valuesComparableForEquality(value, allowed) {
				continue
			}
			comparable = true
			if value.Equal(allowed) {
				return true, nil
			}
		}
		if len(c.values) > 0 && !comparable {
			return false, fmt.Errorf("%w: field constraint value of kind %s is not comparable with allowed values", ErrMatcher, value.Kind())
		}
		return false, nil
	case FieldConstraintOpLessThan, FieldConstraintOpLessOrEqual, FieldConstraintOpGreaterThan, FieldConstraintOpGreaterOrEqual:
		if !ok {
			return false, c.missingOperandError()
		}
		comparison, comparable := compareValues(value, c.value)
		if !comparable {
			return false, c.nonComparableOperandsError(value)
		}
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
	default:
		return false, fmt.Errorf("%w: unsupported field constraint operator %q", ErrMatcher, c.operator)
	}
	return false, fmt.Errorf("%w: unsupported field constraint operator %q", ErrMatcher, c.operator)
}

func (c compiledFieldConstraint) validateComparisonOperands(value Value, ok bool) error {
	if !ok {
		return c.missingOperandError()
	}
	if !valuesComparableForEquality(value, c.value) {
		return c.nonComparableOperandsError(value)
	}
	return nil
}

func (c compiledFieldConstraint) missingOperandError() error {
	if c.access.root != "" {
		return fmt.Errorf("%w: field constraint operand %q is missing", ErrMatcher, c.access.root)
	}
	return fmt.Errorf("%w: field constraint operand is missing", ErrMatcher)
}

func (c compiledFieldConstraint) nonComparableOperandsError(value Value) error {
	return fmt.Errorf("%w: field constraint operands have non-comparable kinds %s and %s", ErrMatcher, value.Kind(), c.value.Kind())
}

func (c compiledFieldConstraint) matchesWorking(fact *workingFact, compactSlotStore *factCompactSlotStore) (bool, error) {
	value, ok := c.valueFromWorkingFact(fact, compactSlotStore)
	return c.matchesValue(value, ok)
}

func (c compiledFieldConstraint) matchesWorkingWithCounters(fact *workingFact, compactSlotStore *factCompactSlotStore, span *propagationCounterSpan) (bool, error) {
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
