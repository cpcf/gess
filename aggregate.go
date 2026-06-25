package gess

import (
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

	aggregateExists AggregateKind = "exists"
	aggregateForall AggregateKind = "forall"
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

type compiledAggregateSpec struct {
	kind       AggregateKind
	binding    string
	expression compiledExpression
	hasExpr    bool
}

type compiledAggregatePlan struct {
	inputPlans  []compiledConditionPlan
	specs       []compiledAggregateSpec
	higherOrder conditionHigherOrderKind
}

func compileAggregateSpecList(ruleName string, conditionIndex int, specs []AggregateSpec, inputConditions []RuleCondition, inputBindingSlots map[string]int, templatesByKey map[TemplateKey]Template, functions map[string]compiledPureFunction) ([]compiledAggregateSpec, []RuleCondition, error) {
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
			expression, _, err := compileExpressionSpecWithParams(normalized.expression, ruleName, conditionIndex, i, nil, inputConditions, inputBindingSlots, templatesByKey, nil, functions)
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

func serializeCompiledAggregateSpecs(specs []compiledAggregateSpec) string {
	if len(specs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, spec := range specs {
		b.WriteString(spec.binding)
		b.WriteByte(':')
		b.WriteString(string(spec.kind))
		if spec.hasExpr {
			b.WriteByte(':')
			b.WriteString(serializeCompiledExpression(spec.expression))
		}
		b.WriteByte(';')
	}
	return b.String()
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

func safeAddInt64(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, true
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, true
	}
	return left + right, false
}
