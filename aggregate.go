package gess

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type AggregateKind string

const (
	AggregateCount   AggregateKind = "count"
	AggregateSum     AggregateKind = "sum"
	AggregateMin     AggregateKind = "min"
	AggregateMax     AggregateKind = "max"
	AggregateCollect AggregateKind = "collect"
)

type AggregateSpec struct {
	kind       AggregateKind
	expression ExpressionSpec
	binding    string
}

func Count() AggregateSpec {
	return AggregateSpec{kind: AggregateCount}
}

func Sum(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{kind: AggregateSum, expression: cloneExpressionSpec(expression)}
}

func Min(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{kind: AggregateMin, expression: cloneExpressionSpec(expression)}
}

func Max(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{kind: AggregateMax, expression: cloneExpressionSpec(expression)}
}

func Collect(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{kind: AggregateCollect, expression: cloneExpressionSpec(expression)}
}

func (s AggregateSpec) As(binding string) AggregateSpec {
	s.binding = strings.TrimSpace(binding)
	return s
}

func (s AggregateSpec) Kind() AggregateKind {
	return s.kind
}

func (s AggregateSpec) Expression() ExpressionSpec {
	return cloneExpressionSpec(s.expression)
}

func (s AggregateSpec) Binding() string {
	return s.binding
}

func (s AggregateSpec) clone() AggregateSpec {
	s.expression = cloneExpressionSpec(s.expression)
	s.binding = strings.TrimSpace(s.binding)
	return s
}

type AccumulateCondition struct {
	Input ConditionSpec
	Specs []AggregateSpec
}

func (AccumulateCondition) conditionSpecNode() {}

func Accumulate(input ConditionSpec, specs ...AggregateSpec) AccumulateCondition {
	out := AccumulateCondition{
		Input: cloneConditionSpec(input),
		Specs: make([]AggregateSpec, len(specs)),
	}
	for i, spec := range specs {
		out.Specs[i] = spec.clone()
	}
	return out
}

func (s AccumulateCondition) clone() AccumulateCondition {
	out := s
	out.Input = cloneConditionSpec(s.Input)
	out.Specs = make([]AggregateSpec, len(s.Specs))
	for i, spec := range s.Specs {
		out.Specs[i] = spec.clone()
	}
	return out
}

type aggregateValueBinding struct {
	name  string
	value Value
}

type compiledAggregateSpec struct {
	kind       AggregateKind
	binding    string
	expression compiledExpression
	hasExpr    bool
}

type compiledAggregatePlan struct {
	inputPlans []compiledConditionPlan
	specs      []compiledAggregateSpec
}

func compileAggregateSpecList(ruleName string, conditionIndex int, specs []AggregateSpec, inputConditions []RuleCondition, inputBindingSlots map[string]int, templatesByKey map[TemplateKey]Template) ([]compiledAggregateSpec, []RuleCondition, error) {
	if len(specs) == 0 {
		return nil, nil, aggregateValidationError(ruleName, conditionIndex, -1, "accumulate requires at least one aggregate spec", nil)
	}
	out := make([]compiledAggregateSpec, 0, len(specs))
	resultConditions := make([]RuleCondition, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for i, spec := range specs {
		normalized := spec.clone()
		if !validAggregateKind(normalized.kind) {
			return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "invalid aggregate kind", nil)
		}
		if !isValidBindingName(normalized.binding) {
			return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "aggregate result binding is required", nil)
		}
		if _, exists := seen[normalized.binding]; exists {
			return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "duplicate aggregate result binding", nil)
		}
		seen[normalized.binding] = struct{}{}

		compiled := compiledAggregateSpec{
			kind:    normalized.kind,
			binding: normalized.binding,
		}
		if normalized.kind != AggregateCount {
			if normalized.expression == nil {
				return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "aggregate expression is required", nil)
			}
			expression, _, err := compileExpressionSpec(normalized.expression, ruleName, conditionIndex, i, nil, inputConditions, inputBindingSlots, templatesByKey)
			if err != nil {
				return nil, nil, err
			}
			compiled.expression = expression
			compiled.hasExpr = true
		}
		out = append(out, compiled)
		resultConditions = append(resultConditions, RuleCondition{
			binding: normalized.binding,
			order:   conditionIndex + i,
		})
	}
	return out, resultConditions, nil
}

func validAggregateKind(kind AggregateKind) bool {
	switch kind {
	case AggregateCount, AggregateSum, AggregateMin, AggregateMax, AggregateCollect:
		return true
	default:
		return false
	}
}

func aggregateValidationError(ruleName string, conditionIndex, specIndex int, reason string, err error) error {
	validation := &ValidationError{
		RuleName:          ruleName,
		ConditionIndex:    conditionIndex,
		HasConditionIndex: true,
		Reason:            reason,
		Err:               ErrAggregateValidation,
	}
	if specIndex >= 0 {
		validation.PredicateIndex = specIndex
		validation.HasPredicateIndex = true
	}
	if err != nil {
		validation.Err = fmt.Errorf("%w: %v", ErrAggregateValidation, err)
	}
	return validation
}

func (p compiledConditionPlan) forEachAggregateMatch(ctx context.Context, source factSource, outer []conditionMatch, yield func(conditionMatch) error) error {
	if p.aggregate == nil {
		return fmt.Errorf("%w: missing aggregate plan", ErrAggregateEvaluation)
	}
	bindings, ok, err := p.aggregate.evaluate(ctx, source, outer)
	if err != nil || !ok {
		return err
	}
	for i, binding := range bindings {
		if err := yield(conditionMatch{
			conditionID: p.id,
			bindingSlot: p.bindingSlot + i,
			value:       binding.value,
			hasValue:    true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p compiledAggregatePlan) evaluate(ctx context.Context, source factSource, outer []conditionMatch) ([]aggregateValueBinding, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	states := make([]aggregateState, len(p.specs))
	for i, spec := range p.specs {
		states[i] = aggregateState{spec: spec}
	}
	var walk func(int, []conditionMatch) error
	walk = func(index int, selected []conditionMatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index == len(p.inputPlans) {
			var current conditionFactRef
			if len(selected) > len(outer) {
				current = selected[len(selected)-1].fact
			}
			for i := range states {
				if err := states[i].add(current, selected); err != nil {
					return err
				}
			}
			return nil
		}
		plan := p.inputPlans[index]
		return plan.forEachMatchWithBindings(ctx, source, selected, func(match conditionMatch) error {
			return walk(index+1, append(selected, match))
		})
	}
	selected := make([]conditionMatch, len(outer), len(outer)+len(p.inputPlans))
	copy(selected, outer)
	if err := walk(0, selected); err != nil {
		return nil, false, err
	}

	out := make([]aggregateValueBinding, 0, len(states))
	for i := range states {
		value, ok, err := states[i].result()
		if err != nil || !ok {
			return nil, false, err
		}
		out = append(out, aggregateValueBinding{name: states[i].spec.binding, value: value})
	}
	return out, true, nil
}

type aggregateState struct {
	spec       compiledAggregateSpec
	count      int64
	intSum     int64
	floatSum   float64
	floaty     bool
	minMax     Value
	haveMinMax bool
	values     []Value
}

func (s *aggregateState) add(current conditionFactRef, bindings []conditionMatch) error {
	s.count++
	if s.spec.kind == AggregateCount {
		return nil
	}
	value, ok, err := s.spec.expression.evaluate(current, bindings)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAggregateEvaluation, err)
	}
	if !ok {
		return fmt.Errorf("%w: missing aggregate input value", ErrAggregateEvaluation)
	}
	switch s.spec.kind {
	case AggregateSum:
		return s.addSum(value)
	case AggregateMin:
		return s.addMinMax(value, true)
	case AggregateMax:
		return s.addMinMax(value, false)
	case AggregateCollect:
		s.values = append(s.values, cloneValue(value))
		return nil
	default:
		return fmt.Errorf("%w: unsupported aggregate kind %q", ErrAggregateEvaluation, s.spec.kind)
	}
}

func (s *aggregateState) addSum(value Value) error {
	switch value.Kind() {
	case ValueInt:
		if s.floaty {
			s.floatSum += float64(value.intValue)
			return nil
		}
		next, overflow := safeAddInt64(s.intSum, value.intValue)
		if overflow {
			return fmt.Errorf("%w: integer sum overflow", ErrAggregateEvaluation)
		}
		s.intSum = next
	case ValueFloat:
		s.floaty = true
		s.floatSum += float64(s.intSum) + value.floatValue
		s.intSum = 0
	default:
		return fmt.Errorf("%w: sum input must be numeric", ErrAggregateEvaluation)
	}
	return nil
}

func safeAddInt64(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, true
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, true
	}
	return left + right, false
}

func (s *aggregateState) addMinMax(value Value, min bool) error {
	if !s.haveMinMax {
		s.minMax = cloneValue(value)
		s.haveMinMax = true
		return nil
	}
	comparison, ok := compareValues(value, s.minMax)
	if !ok {
		return fmt.Errorf("%w: min/max input is not comparable", ErrAggregateEvaluation)
	}
	if (min && comparison < 0) || (!min && comparison > 0) {
		s.minMax = cloneValue(value)
	}
	return nil
}

func (s aggregateState) result() (Value, bool, error) {
	switch s.spec.kind {
	case AggregateCount:
		return newIntValue(s.count), true, nil
	case AggregateSum:
		if s.floaty {
			value, err := canonicalFloat(s.floatSum)
			return value, err == nil, err
		}
		return newIntValue(s.intSum), true, nil
	case AggregateMin, AggregateMax:
		if !s.haveMinMax {
			return Value{}, false, nil
		}
		return cloneValue(s.minMax), true, nil
	case AggregateCollect:
		value, err := canonicalValue(s.values)
		return value, err == nil, err
	default:
		return Value{}, false, fmt.Errorf("%w: unsupported aggregate kind %q", ErrAggregateEvaluation, s.spec.kind)
	}
}
