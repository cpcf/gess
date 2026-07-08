package rules

// ConditionSpec is a rule or query left-hand-side condition tree node.
type ConditionSpec interface {
	conditionSpecNode()
}

// And groups condition tree nodes into a conjunction.
type And struct {
	Conditions []ConditionSpec
	Source     SourceSpan
}

func (And) conditionSpecNode() {}

// Or groups condition tree branches into a disjunction.
type Or struct {
	Conditions []ConditionSpec
	Source     SourceSpan
}

func (Or) conditionSpecNode() {}

// Not negates a condition tree node.
type Not struct {
	Condition ConditionSpec
	Source    SourceSpan
}

func (Not) conditionSpecNode() {}

// Explicit marks a positive match as ineligible for backward-chaining demand
// generation while keeping ordinary match behavior unchanged.
type Explicit struct {
	Condition ConditionSpec
	Source    SourceSpan
}

func (Explicit) conditionSpecNode() {}

// ExistsCondition tests whether at least one tuple matching Condition exists.
type ExistsCondition struct {
	Condition ConditionSpec
	Source    SourceSpan
}

func (ExistsCondition) conditionSpecNode() {}

// Exists builds an ExistsCondition.
func Exists(condition ConditionSpec) ExistsCondition {
	return ExistsCondition{Condition: CloneConditionSpec(condition)}
}

// ForallCondition tests whether every tuple matching Domain also satisfies
// Requirement.
type ForallCondition struct {
	Domain      ConditionSpec
	Requirement ConditionSpec
	Source      SourceSpan
}

func (ForallCondition) conditionSpecNode() {}

// Forall builds a ForallCondition.
func Forall(domain ConditionSpec, requirement ConditionSpec) ForallCondition {
	return ForallCondition{
		Domain:      CloneConditionSpec(domain),
		Requirement: CloneConditionSpec(requirement),
	}
}

// Test evaluates a standalone boolean expression over earlier local bindings.
type Test struct {
	Expression ExpressionSpec
	Source     SourceSpan
}

func (Test) conditionSpecNode() {}

// Match is a positive fact match condition tree node.
type Match RuleConditionSpec

func (Match) conditionSpecNode() {}

// AccumulateCondition maintains aggregate results over matching input facts.
type AccumulateCondition struct {
	Input  ConditionSpec
	Specs  []AggregateSpec
	Source SourceSpan
}

func (AccumulateCondition) conditionSpecNode() {}

// Accumulate builds an AccumulateCondition.
func Accumulate(input ConditionSpec, specs ...AggregateSpec) AccumulateCondition {
	out := AccumulateCondition{
		Input: CloneConditionSpec(input),
		Specs: make([]AggregateSpec, len(specs)),
	}
	for i, spec := range specs {
		out.Specs[i] = CloneAggregateSpec(spec)
	}
	return out
}

// CloneConditionSpec returns a defensive copy of spec.
func CloneConditionSpec(spec ConditionSpec) ConditionSpec {
	switch condition := spec.(type) {
	case nil:
		return nil
	case And:
		return cloneAnd(condition)
	case *And:
		if condition == nil {
			return nil
		}
		cloned := cloneAnd(*condition)
		return &cloned
	case Or:
		return cloneOr(condition)
	case *Or:
		if condition == nil {
			return nil
		}
		cloned := cloneOr(*condition)
		return &cloned
	case Not:
		return cloneNot(condition)
	case *Not:
		if condition == nil {
			return nil
		}
		cloned := cloneNot(*condition)
		return &cloned
	case Explicit:
		return cloneExplicit(condition)
	case *Explicit:
		if condition == nil {
			return nil
		}
		cloned := cloneExplicit(*condition)
		return &cloned
	case ExistsCondition:
		return cloneExistsCondition(condition)
	case *ExistsCondition:
		if condition == nil {
			return nil
		}
		cloned := cloneExistsCondition(*condition)
		return &cloned
	case ForallCondition:
		return cloneForallCondition(condition)
	case *ForallCondition:
		if condition == nil {
			return nil
		}
		cloned := cloneForallCondition(*condition)
		return &cloned
	case Test:
		return cloneTest(condition)
	case *Test:
		if condition == nil {
			return nil
		}
		cloned := cloneTest(*condition)
		return &cloned
	case Match:
		return CloneMatch(condition)
	case *Match:
		if condition == nil {
			return nil
		}
		cloned := CloneMatch(*condition)
		return &cloned
	case AccumulateCondition:
		return CloneAccumulateCondition(condition)
	case *AccumulateCondition:
		if condition == nil {
			return nil
		}
		cloned := CloneAccumulateCondition(*condition)
		return &cloned
	default:
		return spec
	}
}

// CloneMatch returns a defensive copy of s.
func CloneMatch(s Match) Match {
	return Match(CloneRuleConditionSpec(RuleConditionSpec(s)))
}

// CloneAccumulateCondition returns a defensive copy of s.
func CloneAccumulateCondition(s AccumulateCondition) AccumulateCondition {
	out := s
	out.Input = CloneConditionSpec(s.Input)
	out.Specs = make([]AggregateSpec, len(s.Specs))
	for i, spec := range s.Specs {
		out.Specs[i] = CloneAggregateSpec(spec)
	}
	return out
}

func cloneAnd(s And) And {
	out := s
	out.Conditions = make([]ConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = CloneConditionSpec(condition)
	}
	return out
}

func cloneOr(s Or) Or {
	out := s
	out.Conditions = make([]ConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = CloneConditionSpec(condition)
	}
	return out
}

func cloneNot(s Not) Not {
	out := s
	out.Condition = CloneConditionSpec(s.Condition)
	return out
}

func cloneExplicit(s Explicit) Explicit {
	out := s
	out.Condition = CloneConditionSpec(s.Condition)
	return out
}

func cloneExistsCondition(s ExistsCondition) ExistsCondition {
	out := s
	out.Condition = CloneConditionSpec(s.Condition)
	return out
}

func cloneForallCondition(s ForallCondition) ForallCondition {
	out := s
	out.Domain = CloneConditionSpec(s.Domain)
	out.Requirement = CloneConditionSpec(s.Requirement)
	return out
}

func cloneTest(s Test) Test {
	s.Expression = CloneExpressionSpec(s.Expression)
	return s
}
