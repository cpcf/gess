package rules

import "strings"

// QueryParameterSpec declares one named, typed query parameter.
type QueryParameterSpec struct {
	Name string
	Kind ValueKind
}

// CloneQueryParameterSpec returns a defensive copy of s.
func CloneQueryParameterSpec(s QueryParameterSpec) QueryParameterSpec {
	s.Name = strings.TrimSpace(s.Name)
	if s.Kind == valueKindUnknown {
		s.Kind = ValueAny
	}
	return s
}

// QueryParameter is the compiled, inspectable form of a QueryParameterSpec.
type QueryParameter struct {
	NameValue string
	KindValue ValueKind
	Order     int
}

func (p QueryParameter) Name() string {
	return p.NameValue
}

func (p QueryParameter) Kind() ValueKind {
	return p.KindValue
}

func (p QueryParameter) DeclarationOrder() int {
	return p.Order
}

// CloneQueryParameters returns a defensive copy of parameters.
func CloneQueryParameters(parameters []QueryParameter) []QueryParameter {
	out := make([]QueryParameter, len(parameters))
	copy(out, parameters)
	return out
}

// QueryReturnSpec declares one named query result column.
type QueryReturnSpec struct {
	Alias      string
	Binding    string
	Expression ExpressionSpec
	Source     SourceSpan
}

// CloneQueryReturnSpec returns a defensive copy of s.
func CloneQueryReturnSpec(s QueryReturnSpec) QueryReturnSpec {
	s.Alias = strings.TrimSpace(s.Alias)
	s.Binding = strings.TrimSpace(s.Binding)
	s.Expression = CloneExpressionSpec(s.Expression)
	return s
}

// ReturnFact builds a QueryReturnSpec returning the whole fact bound to binding
// under alias.
func ReturnFact(alias, binding string) QueryReturnSpec {
	return QueryReturnSpec{Alias: alias, Binding: binding}
}

// ReturnValue builds a QueryReturnSpec returning expression under alias.
func ReturnValue(alias string, expression ExpressionSpec) QueryReturnSpec {
	return QueryReturnSpec{Alias: alias, Expression: CloneExpressionSpec(expression)}
}

// QueryReturn is the compiled, inspectable form of a QueryReturnSpec.
type QueryReturn struct {
	AliasValue     string
	BindingName    string
	ExpressionSpec ExpressionSpec
	FactValue      bool
	Order          int
	SourceSpan     SourceSpan
}

func (r QueryReturn) Alias() string {
	return r.AliasValue
}

func (r QueryReturn) Binding() string {
	return r.BindingName
}

func (r QueryReturn) Expression() ExpressionSpec {
	return CloneExpressionSpec(r.ExpressionSpec)
}

func (r QueryReturn) Fact() bool {
	return r.FactValue
}

func (r QueryReturn) DeclarationOrder() int {
	return r.Order
}

func (r QueryReturn) Source() SourceSpan {
	return r.SourceSpan
}

// CloneQueryReturn returns a defensive copy of r.
func CloneQueryReturn(r QueryReturn) QueryReturn {
	r.ExpressionSpec = CloneExpressionSpec(r.ExpressionSpec)
	return r
}

// CloneQueryReturns returns a defensive copy of returns.
func CloneQueryReturns(returns []QueryReturn) []QueryReturn {
	out := make([]QueryReturn, len(returns))
	for i, ret := range returns {
		out[i] = CloneQueryReturn(ret)
	}
	return out
}

// Query is the compiled, inspectable form of a QuerySpec.
type Query struct {
	NameValue                      string
	ModuleValue                    ModuleName
	DescriptionText                string
	SourceSpan                     SourceSpan
	GessSourceText                 string
	ParameterValues                []QueryParameter
	ConditionValues                []RuleCondition
	ConditionTreeValue             RuleConditionTree
	ConditionBranchValues          []RuleConditionBranch
	ConditionBranchesTruncatedFlag bool
	ReturnValues                   []QueryReturn
}

func (q Query) Name() string {
	return q.NameValue
}

func (q Query) Module() ModuleName {
	return q.ModuleValue
}

func (q Query) Description() string {
	return q.DescriptionText
}

func (q Query) Source() SourceSpan {
	return q.SourceSpan
}

func (q Query) GessSource() string {
	return q.GessSourceText
}

func (q Query) Parameters() []QueryParameter {
	return CloneQueryParameters(q.ParameterValues)
}

func (q Query) Conditions() []RuleCondition {
	return CloneRuleConditions(q.ConditionValues)
}

func (q Query) ConditionTree() RuleConditionTree {
	return CloneRuleConditionTree(q.ConditionTreeValue)
}

func (q Query) ConditionBranches() []RuleConditionBranch {
	return CloneRuleConditionBranches(q.ConditionBranchValues)
}

// ConditionBranchesTruncated reports whether ConditionBranches is a bounded
// inspection prefix rather than the complete Cartesian expansion.
func (q Query) ConditionBranchesTruncated() bool {
	return q.ConditionBranchesTruncatedFlag
}

func (q Query) Returns() []QueryReturn {
	return CloneQueryReturns(q.ReturnValues)
}

// CloneQuery returns a defensive copy of q.
func CloneQuery(q Query) Query {
	out := q
	out.ParameterValues = CloneQueryParameters(q.ParameterValues)
	out.ConditionValues = CloneRuleConditions(q.ConditionValues)
	out.ConditionTreeValue = CloneRuleConditionTree(q.ConditionTreeValue)
	out.ConditionBranchValues = CloneRuleConditionBranches(q.ConditionBranchValues)
	out.ReturnValues = CloneQueryReturns(q.ReturnValues)
	return out
}

// QuerySpec defines a named query.
type QuerySpec struct {
	Name          string
	Module        ModuleName
	Description   string
	Source        SourceSpan
	GessSource    string
	Parameters    []QueryParameterSpec
	Conditions    []RuleConditionSpec
	ConditionTree ConditionSpec
	Returns       []QueryReturnSpec
}

// CloneQuerySpec returns a defensive copy of s.
func CloneQuerySpec(s QuerySpec) QuerySpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	out.Module = normalizeModuleName(out.Module)
	out.Parameters = make([]QueryParameterSpec, len(s.Parameters))
	for i, param := range s.Parameters {
		out.Parameters[i] = CloneQueryParameterSpec(param)
	}
	out.Conditions = make([]RuleConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = CloneRuleConditionSpec(condition)
	}
	out.ConditionTree = CloneConditionSpec(s.ConditionTree)
	out.Returns = make([]QueryReturnSpec, len(s.Returns))
	for i, ret := range s.Returns {
		out.Returns[i] = CloneQueryReturnSpec(ret)
	}
	return out
}
