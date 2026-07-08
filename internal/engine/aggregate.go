package engine

import (
	"fmt"
	"math"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type AggregateKind = gessrules.AggregateKind

const (
	AggregateCount   = gessrules.AggregateCount
	AggregateSum     = gessrules.AggregateSum
	AggregateMin     = gessrules.AggregateMin
	AggregateMax     = gessrules.AggregateMax
	AggregateCollect = gessrules.AggregateCollect

	aggregateExists AggregateKind = "exists"
	aggregateForall AggregateKind = "forall"
)

type AggregateSpec = gessrules.AggregateSpec

func Count() AggregateSpec {
	return gessrules.Count()
}

func Sum(expression ExpressionSpec) AggregateSpec {
	return gessrules.Sum(expression)
}

func Min(expression ExpressionSpec) AggregateSpec {
	return gessrules.Min(expression)
}

func Max(expression ExpressionSpec) AggregateSpec {
	return gessrules.Max(expression)
}

func Collect(expression ExpressionSpec) AggregateSpec {
	return gessrules.Collect(expression)
}

func cloneAggregateSpec(s AggregateSpec) AggregateSpec {
	return gessrules.CloneAggregateSpec(s)
}

type AccumulateCondition = gessrules.AccumulateCondition

func Accumulate(input ConditionSpec, specs ...AggregateSpec) AccumulateCondition {
	return gessrules.Accumulate(input, specs...)
}

func cloneAccumulateCondition(s AccumulateCondition) AccumulateCondition {
	return gessrules.CloneAccumulateCondition(s)
}

type compiledAggregateSpec struct {
	kind       AggregateKind
	binding    string
	expression compiledExpression
	hasExpr    bool
	valueIndex int
}

type compiledAggregatePlan struct {
	inputPlans  []compiledConditionPlan
	specs       []compiledAggregateSpec
	higherOrder conditionHigherOrderKind
}

func compileAggregateSpecList(ruleName string, conditionIndex int, specs []AggregateSpec, inputConditions []RuleCondition, inputBindingSlots map[string]int, templatesByKey map[TemplateKey]compiledTemplate, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) ([]compiledAggregateSpec, []RuleCondition, error) {
	if len(specs) == 0 {
		return nil, nil, aggregateValidationError(ruleName, conditionIndex, -1, "accumulate requires at least one aggregate spec", nil)
	}
	out := make([]compiledAggregateSpec, 0, len(specs))
	resultConditions := make([]RuleCondition, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	valueIndexes := make(map[string]int)
	for i, spec := range specs {
		normalized := cloneAggregateSpec(spec)
		if !validAggregateKind(normalized.KindValue) {
			return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "invalid aggregate kind", nil)
		}
		if !isValidBindingName(normalized.BindingName) {
			return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "aggregate result binding is required", nil)
		}
		if _, exists := seen[normalized.BindingName]; exists {
			return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "duplicate aggregate result binding", nil)
		}
		seen[normalized.BindingName] = struct{}{}

		compiled := compiledAggregateSpec{
			kind:       normalized.KindValue,
			binding:    normalized.BindingName,
			valueIndex: -1,
		}
		if normalized.KindValue != AggregateCount {
			if normalized.ExpressionSpec == nil {
				return nil, nil, aggregateValidationError(ruleName, conditionIndex, i, "aggregate expression is required", nil)
			}
			expression, _, err := compileExpressionSpecWithParams(normalized.ExpressionSpec, ruleName, conditionIndex, i, nil, inputConditions, inputBindingSlots, templatesByKey, nil, functions, globals)
			if err != nil {
				return nil, nil, err
			}
			compiled.expression = expression
			compiled.hasExpr = true
			key := serializeCompiledExpression(expression)
			valueIndex, ok := valueIndexes[key]
			if !ok {
				valueIndex = len(valueIndexes)
				valueIndexes[key] = valueIndex
			}
			compiled.valueIndex = valueIndex
		}
		out = append(out, compiled)
		resultConditions = append(resultConditions, RuleCondition{
			BindingName: normalized.BindingName,
			Order:       conditionIndex + i,
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
