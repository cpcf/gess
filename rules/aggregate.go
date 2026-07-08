package rules

import "strings"

// AggregateSpec is one aggregate computed over facts matching an accumulate
// input.
type AggregateSpec struct {
	KindValue      AggregateKind
	ExpressionSpec ExpressionSpec
	BindingName    string
}

// Count aggregates to the number of matching facts.
func Count() AggregateSpec {
	return AggregateSpec{KindValue: AggregateCount}
}

// Sum aggregates to the running sum of expression over the group.
func Sum(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{KindValue: AggregateSum, ExpressionSpec: CloneExpressionSpec(expression)}
}

// Min aggregates to the running minimum of expression over the group.
func Min(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{KindValue: AggregateMin, ExpressionSpec: CloneExpressionSpec(expression)}
}

// Max aggregates to the running maximum of expression over the group.
func Max(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{KindValue: AggregateMax, ExpressionSpec: CloneExpressionSpec(expression)}
}

// Collect aggregates expression's value from every matching fact into a list.
func Collect(expression ExpressionSpec) AggregateSpec {
	return AggregateSpec{KindValue: AggregateCollect, ExpressionSpec: CloneExpressionSpec(expression)}
}

func (s AggregateSpec) As(binding string) AggregateSpec {
	s.BindingName = strings.TrimSpace(binding)
	return s
}

func (s AggregateSpec) Kind() AggregateKind {
	return s.KindValue
}

func (s AggregateSpec) Expression() ExpressionSpec {
	return CloneExpressionSpec(s.ExpressionSpec)
}

func (s AggregateSpec) Binding() string {
	return s.BindingName
}

// CloneAggregateSpec returns a defensive copy of s.
func CloneAggregateSpec(s AggregateSpec) AggregateSpec {
	s.ExpressionSpec = CloneExpressionSpec(s.ExpressionSpec)
	s.BindingName = strings.TrimSpace(s.BindingName)
	return s
}
