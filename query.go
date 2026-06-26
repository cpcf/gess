package gess

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

type QueryArgs map[string]any

type QueryParameterSpec struct {
	Name string
	Kind ValueKind
}

func (s QueryParameterSpec) clone() QueryParameterSpec {
	s.Name = strings.TrimSpace(s.Name)
	if s.Kind == "" {
		s.Kind = ValueAny
	}
	return s
}

type QueryReturnSpec struct {
	Alias      string
	Binding    string
	Expression ExpressionSpec
}

func (s QueryReturnSpec) clone() QueryReturnSpec {
	s.Alias = strings.TrimSpace(s.Alias)
	s.Binding = strings.TrimSpace(s.Binding)
	s.Expression = cloneExpressionSpec(s.Expression)
	return s
}

func ReturnFact(alias, binding string) QueryReturnSpec {
	return QueryReturnSpec{Alias: alias, Binding: binding}
}

func ReturnValue(alias string, expression ExpressionSpec) QueryReturnSpec {
	return QueryReturnSpec{Alias: alias, Expression: cloneExpressionSpec(expression)}
}

type QuerySpec struct {
	Name          string
	Module        ModuleName
	Description   string
	Parameters    []QueryParameterSpec
	Conditions    []RuleConditionSpec
	ConditionTree ConditionSpec
	Returns       []QueryReturnSpec
}

func (s QuerySpec) clone() QuerySpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	out.Module = normalizeModuleName(out.Module)
	out.Parameters = make([]QueryParameterSpec, len(s.Parameters))
	for i, param := range s.Parameters {
		out.Parameters[i] = param.clone()
	}
	out.Conditions = make([]RuleConditionSpec, len(s.Conditions))
	for i, condition := range s.Conditions {
		out.Conditions[i] = condition.clone()
	}
	out.ConditionTree = cloneConditionSpec(s.ConditionTree)
	out.Returns = make([]QueryReturnSpec, len(s.Returns))
	for i, ret := range s.Returns {
		out.Returns[i] = ret.clone()
	}
	return out
}

type QueryParameter struct {
	name  string
	kind  ValueKind
	order int
}

func (p QueryParameter) Name() string {
	return p.name
}

func (p QueryParameter) Kind() ValueKind {
	return p.kind
}

func (p QueryParameter) DeclarationOrder() int {
	return p.order
}

type QueryReturn struct {
	alias      string
	binding    string
	expression ExpressionSpec
	fact       bool
	order      int
}

func (r QueryReturn) Alias() string {
	return r.alias
}

func (r QueryReturn) Binding() string {
	return r.binding
}

func (r QueryReturn) Expression() ExpressionSpec {
	return cloneExpressionSpec(r.expression)
}

func (r QueryReturn) Fact() bool {
	return r.fact
}

func (r QueryReturn) DeclarationOrder() int {
	return r.order
}

type Query struct {
	name              string
	module            ModuleName
	description       string
	parameters        []QueryParameter
	conditions        []RuleCondition
	conditionTree     RuleConditionTree
	conditionBranches []RuleConditionBranch
	returns           []QueryReturn
}

func (q Query) Name() string {
	return q.name
}

func (q Query) Module() ModuleName {
	return q.module
}

func (q Query) Description() string {
	return q.description
}

func (q Query) Parameters() []QueryParameter {
	out := make([]QueryParameter, len(q.parameters))
	copy(out, q.parameters)
	return out
}

func (q Query) Conditions() []RuleCondition {
	return cloneRuleConditions(q.conditions)
}

func (q Query) ConditionTree() RuleConditionTree {
	return q.conditionTree.clone()
}

func (q Query) ConditionBranches() []RuleConditionBranch {
	return cloneRuleConditionBranches(q.conditionBranches)
}

func (q Query) Returns() []QueryReturn {
	out := make([]QueryReturn, len(q.returns))
	for i, ret := range q.returns {
		out[i] = ret
		out[i].expression = cloneExpressionSpec(ret.expression)
	}
	return out
}

type compiledQuery struct {
	name                    string
	module                  ModuleName
	description             string
	parameters              []QueryParameter
	parameterTypes          map[string]ValueKind
	conditions              []RuleCondition
	treeConditions          []RuleCondition
	conditionTree           RuleConditionTree
	conditionBranches       []compiledConditionBranch
	graphConditionBranches  []compiledConditionBranch
	conditionBranchPlans    []RuleConditionBranch
	returns                 []compiledQueryReturn
	returnAliases           []string
	returnAliasIndex        map[string]int
	compactReturnAliasIndex map[string]int
	factReturnCount         int
	valueReturnCount        int
}

type compiledQueryReturn struct {
	alias       string
	binding     string
	bindingSlot int
	expression  compiledExpression
	projection  compiledQueryReturnProjection
	rawExpr     ExpressionSpec
	fact        bool
	order       int
}

type compiledQueryReturnProjectionKind uint8

const (
	compiledQueryReturnProjectionGeneric compiledQueryReturnProjectionKind = iota
	compiledQueryReturnProjectionBindingField
	compiledQueryReturnProjectionBindingValue
	compiledQueryReturnProjectionConst
)

type compiledQueryReturnProjection struct {
	kind        compiledQueryReturnProjectionKind
	bindingSlot int
	access      compiledPathAccess
	value       Value
}

func (q compiledQuery) inspect() Query {
	return Query{
		name:              q.name,
		module:            q.module,
		description:       q.description,
		parameters:        append([]QueryParameter(nil), q.parameters...),
		conditions:        cloneRuleConditions(q.conditions),
		conditionTree:     q.conditionTree.clone(),
		conditionBranches: cloneRuleConditionBranches(q.conditionBranchPlans),
		returns:           q.inspectReturns(),
	}
}

func (q compiledQuery) inspectReturns() []QueryReturn {
	out := make([]QueryReturn, len(q.returns))
	for i, ret := range q.returns {
		out[i] = QueryReturn{
			alias:      ret.alias,
			binding:    ret.binding,
			expression: cloneExpressionSpec(ret.rawExpr),
			fact:       ret.fact,
			order:      ret.order,
		}
	}
	return out
}

func compileQuerySpec(spec QuerySpec, templatesByKey map[TemplateKey]Template, functions map[string]compiledPureFunction) (compiledQuery, error) {
	normalized := spec.clone()
	if normalized.Name == "" {
		return compiledQuery{}, &ValidationError{Reason: "query name is required", Err: ErrQueryValidation}
	}
	params, paramTypes, err := compileQueryParameters(normalized)
	if err != nil {
		return compiledQuery{}, err
	}
	pseudoRule := RuleSpec{
		Name:          normalized.Name,
		Module:        normalized.Module,
		Conditions:    normalized.Conditions,
		ConditionTree: normalized.ConditionTree,
	}
	normalizedConditions, conditionTreeShape, err := normalizeRuleConditions(pseudoRule)
	if err != nil {
		return compiledQuery{}, markQueryValidation(err)
	}
	normalizedBranches, err := normalizeRuleConditionBranches(pseudoRule)
	if err != nil {
		return compiledQuery{}, markQueryValidation(err)
	}
	if len(normalizedBranches) == 0 {
		return compiledQuery{}, &ValidationError{RuleName: normalized.Name, Reason: "query requires at least one condition", Err: ErrQueryValidation}
	}
	if len(normalized.Returns) == 0 {
		return compiledQuery{}, &ValidationError{RuleName: normalized.Name, Reason: "query requires at least one return", Err: ErrQueryValidation}
	}

	queryRuleID := RuleID("query:" + normalized.Name)
	inspectionSet, err := compileNormalizedRuleConditionBranchWithParams(normalized.Name, queryRuleID, normalizedConditions, templatesByKey, true, paramTypes, functions)
	if err != nil {
		return compiledQuery{}, markQueryValidation(err)
	}
	compiledBranches := make([]compiledConditionBranch, 0, len(normalizedBranches))
	graphBranches := make([]compiledConditionBranch, 0, len(normalizedBranches))
	var representative compiledRuleConditionSet
	for branchIndex, branch := range normalizedBranches {
		branchIR := newReorderedBranchPlanningIR(branchIndex, branch.conditions)
		compiledBranch, err := compileBranchPlanningIR(normalized.Name, queryRuleID, branchIR, templatesByKey, false, paramTypes, functions)
		if err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		if branchIndex == 0 {
			representative = compiledBranch
		} else if err := validateBranchBindingContract(normalized.Name, representative.conditions, compiledBranch.conditions); err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		compiledBranches = append(compiledBranches, compiledConditionBranchFromPlanningIR(branchIR, compiledBranch))
		graphBranch, ok, err := compileQueryGraphBranch(normalized.Name, queryRuleID, branchIndex, branch.conditions, templatesByKey, paramTypes, functions)
		if err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		if ok {
			graphBranches = append(graphBranches, graphBranch)
		}
	}
	if len(graphBranches) != len(normalizedBranches) {
		return compiledQuery{}, &ValidationError{
			RuleName: normalized.Name,
			Reason:   "query cannot be compiled to graph terminal memory",
			Err:      ErrQueryValidation,
		}
	}
	conditionBranches := make([]RuleConditionBranch, len(compiledBranches))
	for i, branch := range compiledBranches {
		conditionBranches[i] = RuleConditionBranch{
			id:         branch.id,
			conditions: cloneRuleConditionBranchConditions(branch.conditions),
		}
	}
	bindingSlots := make(map[string]int, len(representative.conditions))
	for i, condition := range representative.conditions {
		bindingSlots[condition.binding] = i
	}
	returns, err := compileQueryReturns(normalized.Name, normalized.Returns, representative.conditions, bindingSlots, templatesByKey, paramTypes, functions)
	if err != nil {
		return compiledQuery{}, err
	}

	returnAliases, returnAliasIndex := compileQueryReturnIndexes(returns)
	compactReturnAliasIndex, factReturnCount, valueReturnCount := compileQueryReturnCompactIndexes(returns)

	return compiledQuery{
		name:                    normalized.Name,
		module:                  normalized.Module,
		description:             normalized.Description,
		parameters:              params,
		parameterTypes:          paramTypes,
		conditions:              representative.conditions,
		treeConditions:          inspectionSet.treeConditions,
		conditionTree:           buildRuleConditionTree(conditionTreeShape, inspectionSet.treeConditions),
		conditionBranches:       compiledBranches,
		graphConditionBranches:  graphBranches,
		conditionBranchPlans:    conditionBranches,
		returns:                 returns,
		returnAliases:           returnAliases,
		returnAliasIndex:        returnAliasIndex,
		compactReturnAliasIndex: compactReturnAliasIndex,
		factReturnCount:         factReturnCount,
		valueReturnCount:        valueReturnCount,
	}, nil
}

func compileQueryParameters(spec QuerySpec) ([]QueryParameter, map[string]ValueKind, error) {
	params := make([]QueryParameter, 0, len(spec.Parameters))
	paramTypes := make(map[string]ValueKind, len(spec.Parameters))
	for i, param := range spec.Parameters {
		param = param.clone()
		if param.Name == "" {
			return nil, nil, &ValidationError{RuleName: spec.Name, Reason: "query parameter name is required", Err: ErrQueryValidation}
		}
		if !isValidBindingName(param.Name) {
			return nil, nil, &ValidationError{RuleName: spec.Name, Reason: "invalid query parameter name", Err: ErrQueryValidation}
		}
		if _, exists := paramTypes[param.Name]; exists {
			return nil, nil, &ValidationError{RuleName: spec.Name, Reason: "duplicate query parameter", Err: ErrQueryValidation}
		}
		if !validQueryParameterKind(param.Kind) {
			return nil, nil, &ValidationError{RuleName: spec.Name, Reason: "invalid query parameter kind", Err: ErrQueryValidation}
		}
		paramTypes[param.Name] = param.Kind
		params = append(params, QueryParameter{name: param.Name, kind: param.Kind, order: i})
	}
	return params, paramTypes, nil
}

func validQueryParameterKind(kind ValueKind) bool {
	switch kind {
	case ValueAny, ValueNull, ValueBool, ValueInt, ValueFloat, ValueString, ValueList, ValueMap:
		return true
	default:
		return false
	}
}

func compileQueryReturns(queryName string, specs []QueryReturnSpec, conditions []RuleCondition, bindingSlots map[string]int, templatesByKey map[TemplateKey]Template, params map[string]ValueKind, functions map[string]compiledPureFunction) ([]compiledQueryReturn, error) {
	returns := make([]compiledQueryReturn, 0, len(specs))
	aliases := make(map[string]struct{}, len(specs))
	for i, spec := range specs {
		spec = spec.clone()
		if spec.Alias == "" {
			return nil, &ValidationError{RuleName: queryName, Reason: "query return alias is required", Err: ErrQueryValidation}
		}
		if !isValidBindingName(spec.Alias) {
			return nil, &ValidationError{RuleName: queryName, Reason: "invalid query return alias", Err: ErrQueryValidation}
		}
		if _, exists := aliases[spec.Alias]; exists {
			return nil, &ValidationError{RuleName: queryName, Reason: "duplicate query return alias", Err: ErrQueryValidation}
		}
		aliases[spec.Alias] = struct{}{}
		hasBinding := spec.Binding != ""
		hasExpression := spec.Expression != nil
		if hasBinding == hasExpression {
			return nil, &ValidationError{RuleName: queryName, Reason: "query return must declare exactly one binding or expression", Err: ErrQueryValidation}
		}
		if hasBinding {
			slot, ok := bindingSlots[spec.Binding]
			if !ok {
				return nil, &ValidationError{RuleName: queryName, Reason: "query return references unknown binding", Err: ErrQueryValidation}
			}
			returns = append(returns, compiledQueryReturn{
				alias:       spec.Alias,
				binding:     spec.Binding,
				bindingSlot: slot,
				fact:        true,
				order:       i,
			})
			continue
		}
		if expressionContainsCurrentField(spec.Expression) {
			return nil, &ValidationError{RuleName: queryName, Reason: "query return value expressions cannot use current field references", Err: ErrQueryValidation}
		}
		expression, _, err := compileExpressionSpecWithParams(spec.Expression, queryName, -1, i, nil, conditions, bindingSlots, templatesByKey, params, functions)
		if err != nil {
			return nil, markQueryValidation(err)
		}
		returns = append(returns, compiledQueryReturn{
			alias:       spec.Alias,
			expression:  expression,
			projection:  compileQueryReturnProjection(expression),
			rawExpr:     cloneExpressionSpec(spec.Expression),
			bindingSlot: -1,
			order:       i,
		})
	}
	return returns, nil
}

func compileQueryReturnProjection(expression compiledExpression) compiledQueryReturnProjection {
	switch expression.kind {
	case expressionNodeConst:
		return compiledQueryReturnProjection{
			kind:  compiledQueryReturnProjectionConst,
			value: expression.value,
		}
	case expressionNodeBindingField:
		if expression.bindingSlot >= 0 {
			return compiledQueryReturnProjection{
				kind:        compiledQueryReturnProjectionBindingField,
				bindingSlot: expression.bindingSlot,
				access:      expression.access,
			}
		}
	case expressionNodeBindingValue:
		if expression.bindingSlot >= 0 {
			return compiledQueryReturnProjection{
				kind:        compiledQueryReturnProjectionBindingValue,
				bindingSlot: expression.bindingSlot,
			}
		}
	}
	return compiledQueryReturnProjection{kind: compiledQueryReturnProjectionGeneric}
}

func compileQueryReturnIndexes(returns []compiledQueryReturn) ([]string, map[string]int) {
	aliases := make([]string, len(returns))
	index := make(map[string]int, len(returns))
	for i, ret := range returns {
		aliases[i] = ret.alias
		index[ret.alias] = i
	}
	return aliases, index
}

func compileQueryReturnCompactIndexes(returns []compiledQueryReturn) (map[string]int, int, int) {
	index := make(map[string]int, len(returns))
	factCount := 0
	valueCount := 0
	for _, ret := range returns {
		if ret.fact {
			index[ret.alias] = -factCount - 1
			factCount++
			continue
		}
		index[ret.alias] = valueCount
		valueCount++
	}
	return index, factCount, valueCount
}

const internalQueryTriggerBinding = "__gess_query_trigger"

func internalQueryTriggerName(queryName string) string {
	return "__gess_query_trigger:" + queryName
}

func compileQueryGraphBranch(queryName string, queryRuleID RuleID, branchIndex int, branch []normalizedRuleCondition, templatesByKey map[TemplateKey]Template, params map[string]ValueKind, functions map[string]compiledPureFunction) (compiledConditionBranch, bool, error) {
	branchIR, ok := newQueryGraphBranchPlanningIR(queryName, branchIndex, branch, params)
	if !ok {
		return compiledConditionBranch{}, false, nil
	}
	compiled, err := compileBranchPlanningIR(queryName, queryRuleID, branchIR, templatesByKey, false, nil, functions)
	if err != nil {
		return compiledConditionBranch{}, false, err
	}
	return compiledConditionBranchFromPlanningIR(branchIR, compiled), true, nil
}

func lowerQueryConditionParams(condition RuleConditionSpec, params map[string]ValueKind) RuleConditionSpec {
	out := condition.clone()
	if len(params) == 0 {
		return out
	}
	predicates := out.Predicates
	out.Predicates = make([]ExpressionSpec, 0, len(predicates))
	for _, predicate := range predicates {
		if join, ok := queryParamJoinFromPredicate(predicate, params); ok {
			out.JoinConstraints = append(out.JoinConstraints, join)
			continue
		}
		out.Predicates = append(out.Predicates, lowerQueryParamExpression(predicate, params))
	}
	return out
}

func lowerQueryAggregateConditionParams(condition AccumulateCondition, params map[string]ValueKind) AccumulateCondition {
	out := condition.clone()
	if len(params) == 0 {
		return out
	}
	out.Input = lowerQueryConditionTreeParams(out.Input, params)
	for i := range out.Specs {
		out.Specs[i].expression = lowerQueryParamExpression(out.Specs[i].expression, params)
	}
	return out
}

func lowerQueryConditionTreeParams(spec ConditionSpec, params map[string]ValueKind) ConditionSpec {
	if len(params) == 0 {
		return cloneConditionSpec(spec)
	}
	switch condition := spec.(type) {
	case nil:
		return nil
	case And:
		out := condition.clone()
		for i := range out.Conditions {
			out.Conditions[i] = lowerQueryConditionTreeParams(out.Conditions[i], params)
		}
		return out
	case *And:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(And)
		return &out
	case Or:
		out := condition.clone()
		for i := range out.Conditions {
			out.Conditions[i] = lowerQueryConditionTreeParams(out.Conditions[i], params)
		}
		return out
	case *Or:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(Or)
		return &out
	case Not:
		out := condition.clone()
		out.Condition = lowerQueryConditionTreeParams(out.Condition, params)
		return out
	case *Not:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(Not)
		return &out
	case ExistsCondition:
		out := condition.clone()
		out.Condition = lowerQueryConditionTreeParams(out.Condition, params)
		return out
	case *ExistsCondition:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(ExistsCondition)
		return &out
	case ForallCondition:
		out := condition.clone()
		out.Domain = lowerQueryConditionTreeParams(out.Domain, params)
		out.Requirement = lowerQueryConditionTreeParams(out.Requirement, params)
		return out
	case *ForallCondition:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(ForallCondition)
		return &out
	case Test:
		out := condition.clone()
		out.Expression = lowerQueryParamExpression(out.Expression, params)
		return out
	case *Test:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(Test)
		return &out
	case Match:
		return Match(lowerQueryConditionParams(RuleConditionSpec(condition), params))
	case *Match:
		if condition == nil {
			return nil
		}
		out := Match(lowerQueryConditionParams(RuleConditionSpec(*condition), params))
		return &out
	case AccumulateCondition:
		return lowerQueryAggregateConditionParams(condition, params)
	case *AccumulateCondition:
		if condition == nil {
			return nil
		}
		out := lowerQueryAggregateConditionParams(*condition, params)
		return &out
	default:
		return cloneConditionSpec(spec)
	}
}

func queryParamJoinFromPredicate(spec ExpressionSpec, params map[string]ValueKind) (JoinConstraintSpec, bool) {
	compare, ok := queryCompareExpr(spec)
	if !ok {
		return JoinConstraintSpec{}, false
	}
	operator, ok := fieldConstraintOperatorFromExpression(compare.Operator)
	if !ok {
		return JoinConstraintSpec{}, false
	}
	if path, ok := queryCurrentPathExpr(compare.Left); ok {
		if param, ok := queryParamExpr(compare.Right, params); ok {
			return JoinConstraintSpec{
				Path:     path,
				Operator: operator,
				Ref: FieldRef{
					Binding: internalQueryTriggerBinding,
					Path:    Path(param),
				},
			}, true
		}
	}
	if path, ok := queryCurrentPathExpr(compare.Right); ok {
		if param, ok := queryParamExpr(compare.Left, params); ok {
			inverted, ok := invertFieldConstraintOperator(operator)
			if !ok {
				return JoinConstraintSpec{}, false
			}
			return JoinConstraintSpec{
				Path:     path,
				Operator: inverted,
				Ref: FieldRef{
					Binding: internalQueryTriggerBinding,
					Path:    Path(param),
				},
			}, true
		}
	}
	return JoinConstraintSpec{}, false
}

func queryCompareExpr(spec ExpressionSpec) (CompareExpr, bool) {
	switch expression := spec.(type) {
	case CompareExpr:
		return expression, true
	case *CompareExpr:
		if expression == nil {
			return CompareExpr{}, false
		}
		return *expression, true
	default:
		return CompareExpr{}, false
	}
}

func queryCurrentFieldExpr(spec ExpressionSpec) (string, bool) {
	path, ok := queryCurrentPathExpr(spec)
	if !ok {
		return "", false
	}
	return path.root(), true
}

func queryCurrentPathExpr(spec ExpressionSpec) (PathSpec, bool) {
	switch expression := spec.(type) {
	case CurrentFieldExpr:
		normalized := expression.clone()
		path := pathOrField(normalized.Path, normalized.Field)
		return path, !path.isZero()
	case *CurrentFieldExpr:
		if expression == nil {
			return PathSpec{}, false
		}
		normalized := expression.clone()
		path := pathOrField(normalized.Path, normalized.Field)
		return path, !path.isZero()
	default:
		return PathSpec{}, false
	}
}

func queryParamExpr(spec ExpressionSpec, params map[string]ValueKind) (string, bool) {
	switch expression := spec.(type) {
	case ParamExpr:
		name := strings.TrimSpace(expression.Name)
		_, ok := params[name]
		return name, name != "" && ok
	case *ParamExpr:
		if expression == nil {
			return "", false
		}
		name := strings.TrimSpace(expression.Name)
		_, ok := params[name]
		return name, name != "" && ok
	default:
		return "", false
	}
}

func fieldConstraintOperatorFromExpression(operator ExpressionComparisonOperator) (FieldConstraintOperator, bool) {
	switch operator {
	case ExpressionCompareEqual:
		return FieldConstraintEqual, true
	case ExpressionCompareNotEqual:
		return FieldConstraintNotEqual, true
	case ExpressionCompareLessThan:
		return FieldConstraintLessThan, true
	case ExpressionCompareLessOrEqual:
		return FieldConstraintLessOrEqual, true
	case ExpressionCompareGreaterThan:
		return FieldConstraintGreaterThan, true
	case ExpressionCompareGreaterOrEqual:
		return FieldConstraintGreaterOrEqual, true
	default:
		return FieldConstraintOpUnknown, false
	}
}

func invertFieldConstraintOperator(operator FieldConstraintOperator) (FieldConstraintOperator, bool) {
	switch operator {
	case FieldConstraintEqual:
		return FieldConstraintEqual, true
	case FieldConstraintNotEqual:
		return FieldConstraintNotEqual, true
	case FieldConstraintLessThan:
		return FieldConstraintGreaterThan, true
	case FieldConstraintLessOrEqual:
		return FieldConstraintGreaterOrEqual, true
	case FieldConstraintGreaterThan:
		return FieldConstraintLessThan, true
	case FieldConstraintGreaterOrEqual:
		return FieldConstraintLessOrEqual, true
	default:
		return FieldConstraintOpUnknown, false
	}
}

func lowerQueryParamExpression(spec ExpressionSpec, params map[string]ValueKind) ExpressionSpec {
	switch expression := spec.(type) {
	case ParamExpr:
		name := strings.TrimSpace(expression.Name)
		if _, ok := params[name]; ok {
			return BindingPath(internalQueryTriggerBinding, Path(name))
		}
		return expression.clone()
	case *ParamExpr:
		if expression == nil {
			return nil
		}
		return lowerQueryParamExpression(*expression, params)
	case CompareExpr:
		out := expression.clone()
		out.Left = lowerQueryParamExpression(out.Left, params)
		out.Right = lowerQueryParamExpression(out.Right, params)
		return out
	case *CompareExpr:
		if expression == nil {
			return nil
		}
		out := expression.clone()
		out.Left = lowerQueryParamExpression(out.Left, params)
		out.Right = lowerQueryParamExpression(out.Right, params)
		return &out
	case BooleanExpr:
		out := expression.clone()
		for i := range out.Operands {
			out.Operands[i] = lowerQueryParamExpression(out.Operands[i], params)
		}
		return out
	case *BooleanExpr:
		if expression == nil {
			return nil
		}
		out := expression.clone()
		for i := range out.Operands {
			out.Operands[i] = lowerQueryParamExpression(out.Operands[i], params)
		}
		return &out
	case CallExpr:
		out := expression.clone()
		for i := range out.Args {
			out.Args[i] = lowerQueryParamExpression(out.Args[i], params)
		}
		return out
	case *CallExpr:
		if expression == nil {
			return nil
		}
		out := expression.clone()
		for i := range out.Args {
			out.Args[i] = lowerQueryParamExpression(out.Args[i], params)
		}
		return &out
	default:
		return cloneExpressionSpec(spec)
	}
}

func expressionContainsCurrentField(spec ExpressionSpec) bool {
	switch expression := spec.(type) {
	case CurrentFieldExpr, *CurrentFieldExpr, HasPathExpr, *HasPathExpr:
		return true
	case CompareExpr:
		return expressionContainsCurrentField(expression.Left) || expressionContainsCurrentField(expression.Right)
	case *CompareExpr:
		return expression != nil && expressionContainsCurrentField(*expression)
	case BooleanExpr:
		if slices.ContainsFunc(expression.Operands, expressionContainsCurrentField) {
			return true
		}
	case *BooleanExpr:
		return expression != nil && expressionContainsCurrentField(*expression)
	case CallExpr:
		return slices.ContainsFunc(expression.Args, expressionContainsCurrentField)
	case *CallExpr:
		return expression != nil && expressionContainsCurrentField(*expression)
	}
	return false
}

func markQueryValidation(err error) error {
	var validation *ValidationError
	if strings.Contains(fmt.Sprint(err), "query") {
		return err
	}
	if err != nil && errors.As(err, &validation) {
		clone := *validation
		if clone.Err == nil || clone.Err == ErrValidation {
			clone.Err = ErrQueryValidation
		}
		return &clone
	}
	return err
}

type QueryRow struct {
	values     map[string]queryRowValue
	order      []string
	index      map[string]int
	items      []queryRowValue
	valueItems []Value
}

type queryRowValue struct {
	fact    *queryRowFact
	value   Value
	hasFact bool
}

type queryRowFact struct {
	ref   conditionFactRef
	owner *queryRowOwner
}

type queryRowFactKey struct {
	id      FactID
	version FactVersion
}

type queryRowOwner struct {
	source     Snapshot
	factChunk  []queryRowFact
	factChunks [][]queryRowFact
	mu         sync.Mutex
	facts      map[queryRowFactKey]FactSnapshot
}

func (r QueryRow) Aliases() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

func (r QueryRow) Fact(alias string) (FactSnapshot, bool) {
	if r.compactMixedItems() {
		idx, ok := r.index[alias]
		if !ok || idx >= 0 {
			return FactSnapshot{}, false
		}
		factIdx := -idx - 1
		if factIdx < 0 || factIdx >= len(r.items) {
			return FactSnapshot{}, false
		}
		item := r.items[factIdx]
		if !item.hasFact {
			return FactSnapshot{}, false
		}
		return r.factSnapshot(item.fact)
	}
	if len(r.valueItems) != 0 {
		return FactSnapshot{}, false
	}
	if len(r.items) != 0 {
		idx, ok := r.itemIndex(alias)
		if !ok {
			return FactSnapshot{}, false
		}
		item := r.items[idx]
		if !item.hasFact {
			return FactSnapshot{}, false
		}
		return r.factSnapshot(item.fact)
	}
	item, ok := r.values[alias]
	if !ok || !item.hasFact {
		return FactSnapshot{}, false
	}
	return r.factSnapshot(item.fact)
}

func (r QueryRow) Value(alias string) (Value, bool) {
	if r.compactMixedItems() {
		idx, ok := r.index[alias]
		if !ok || idx < 0 || idx >= len(r.valueItems) {
			return Value{}, false
		}
		return cloneValue(r.valueItems[idx]), true
	}
	if len(r.valueItems) != 0 {
		idx, ok := r.itemIndex(alias)
		if !ok {
			return Value{}, false
		}
		return cloneValue(r.valueItems[idx]), true
	}
	if len(r.items) != 0 {
		idx, ok := r.itemIndex(alias)
		if !ok {
			return Value{}, false
		}
		item := r.items[idx]
		if item.hasFact {
			return Value{}, false
		}
		return cloneValue(item.value), true
	}
	item, ok := r.values[alias]
	if !ok || item.hasFact {
		return Value{}, false
	}
	return cloneValue(item.value), true
}

func (r QueryRow) factSnapshot(fact *queryRowFact) (FactSnapshot, bool) {
	if fact == nil || fact.ref.ID().IsZero() {
		return FactSnapshot{}, false
	}
	if fact.owner != nil {
		return fact.owner.factSnapshot(fact.ref), true
	}
	return fact.ref.snapshot(), true
}

func (r QueryRow) compactMixedItems() bool {
	return len(r.valueItems) != 0 && len(r.items) != 0
}

func newQueryRowOwner(source Snapshot) *queryRowOwner {
	return &queryRowOwner{source: source}
}

func (o *queryRowOwner) newFact(ref conditionFactRef) *queryRowFact {
	if o == nil || ref.ID().IsZero() {
		return nil
	}
	if len(o.factChunk) >= cap(o.factChunk) {
		o.factChunk = make([]queryRowFact, 0, queryFactProjectionChunkRows)
		o.factChunks = append(o.factChunks, o.factChunk)
	}
	o.factChunk = append(o.factChunk, queryRowFact{ref: ref, owner: o})
	o.factChunks[len(o.factChunks)-1] = o.factChunk
	return &o.factChunk[len(o.factChunk)-1]
}

func (o *queryRowOwner) factSnapshot(ref conditionFactRef) FactSnapshot {
	if o == nil {
		return ref.snapshot()
	}
	key := queryRowFactKey{id: ref.ID(), version: ref.Version()}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.facts != nil {
		if fact, ok := o.facts[key]; ok {
			return fact.clone()
		}
	} else {
		o.facts = make(map[queryRowFactKey]FactSnapshot, 1)
	}
	fact, ok := o.source.Fact(ref.ID())
	if !ok || fact.Version() != ref.Version() {
		fact = ref.snapshot()
	}
	o.facts[key] = fact
	return fact.clone()
}

func (r QueryRow) itemIndex(alias string) (int, bool) {
	if r.index != nil {
		idx, ok := r.index[alias]
		itemCount := len(r.items)
		if len(r.valueItems) != 0 {
			itemCount = len(r.valueItems)
		}
		if ok && idx >= 0 && idx < itemCount {
			return idx, true
		}
		return -1, false
	}
	itemCount := len(r.items)
	if len(r.valueItems) != 0 {
		itemCount = len(r.valueItems)
	}
	for i, candidate := range r.order {
		if candidate == alias {
			return i, i < itemCount
		}
	}
	return -1, false
}

type QueryIterator struct {
	rows  []QueryRow
	index int
}

func (it *QueryIterator) Next(ctx context.Context) (QueryRow, bool, error) {
	if it == nil {
		return QueryRow{}, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return QueryRow{}, false, err
	}
	if it.index >= len(it.rows) {
		return QueryRow{}, false, nil
	}
	row := it.rows[it.index].clone()
	it.index++
	return row, true, nil
}

func (it *QueryIterator) All(ctx context.Context) ([]QueryRow, error) {
	var out []QueryRow
	for {
		row, ok, err := it.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, row)
	}
}

func (r QueryRow) clone() QueryRow {
	if r.compactMixedItems() {
		out := QueryRow{
			order:      append([]string(nil), r.order...),
			index:      r.index,
			items:      make([]queryRowValue, len(r.items)),
			valueItems: make([]Value, len(r.valueItems)),
		}
		copy(out.items, r.items)
		for i, value := range r.valueItems {
			out.valueItems[i] = cloneValue(value)
		}
		return out
	}
	if len(r.valueItems) != 0 {
		out := QueryRow{
			order:      append([]string(nil), r.order...),
			index:      r.index,
			valueItems: make([]Value, len(r.valueItems)),
		}
		for i, value := range r.valueItems {
			out.valueItems[i] = cloneValue(value)
		}
		return out
	}
	if len(r.items) != 0 {
		out := QueryRow{
			order: append([]string(nil), r.order...),
			index: r.index,
			items: make([]queryRowValue, len(r.items)),
		}
		for i, value := range r.items {
			value.value = cloneValue(value.value)
			out.items[i] = value
		}
		return out
	}
	out := QueryRow{
		values: make(map[string]queryRowValue, len(r.values)),
		order:  append([]string(nil), r.order...),
	}
	for key, value := range r.values {
		value.value = cloneValue(value.value)
		out.values[key] = value
	}
	return out
}

func (s Snapshot) Query(ctx context.Context, name string, args QueryArgs) (*QueryIterator, error) {
	rows, err := s.queryRows(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return &QueryIterator{rows: rows}, nil
}

func (s Snapshot) QueryAll(ctx context.Context, name string, args QueryArgs) ([]QueryRow, error) {
	return s.queryRows(ctx, name, args)
}

func (s Snapshot) queryRows(ctx context.Context, name string, args QueryArgs) ([]QueryRow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.revision == nil {
		return nil, ErrInvalidRuleset
	}
	query, ok := s.revision.query(strings.TrimSpace(name))
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrQueryNotFound, name)
	}
	compiledArgs, err := query.compileArgs(args)
	if err != nil {
		return nil, err
	}
	if len(query.graphConditionBranches) == 0 {
		return nil, fmt.Errorf("%w: query %q has no graph terminal plan", ErrUnsupportedRuntime, query.name)
	}
	runtime, err := newReteRuntime(s.revision)
	if err != nil {
		return nil, err
	}
	if err := runtime.resetGraphBetaForGeneration(ctx, s.facts, s.generation); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQueryExecution, err)
	}
	trigger := snapshotQueryTriggerFact(s.generation, query, compiledArgs)
	rows, handled, err := runtime.queryRows(ctx, query, compiledArgs, newReteGraphQueryTriggerEvent(trigger), s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQueryExecution, err)
	}
	if !handled {
		return nil, fmt.Errorf("%w: query %q has no graph terminal memory", ErrUnsupportedRuntime, query.name)
	}
	return rows, nil
}

func (s *Session) Query(ctx context.Context, name string, args QueryArgs) (*QueryIterator, error) {
	rows, ok, err := s.queryGraphRows(ctx, name, args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: query %q has no graph terminal memory", ErrUnsupportedRuntime, name)
	}
	return &QueryIterator{rows: rows}, nil
}

func (s *Session) QueryAll(ctx context.Context, name string, args QueryArgs) ([]QueryRow, error) {
	rows, ok, err := s.queryGraphRows(ctx, name, args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: query %q has no graph terminal memory", ErrUnsupportedRuntime, name)
	}
	return rows, nil
}

func (s *Session) queryGraphRows(ctx context.Context, name string, args QueryArgs) ([]QueryRow, bool, error) {
	if s == nil || s.closed {
		return nil, true, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if s.runGuardHeld() {
		return nil, true, ErrConcurrencyMisuse
	}
	if !s.beginMutation() {
		return nil, true, ErrConcurrencyMisuse
	}
	defer s.endMutation()
	if s.closed {
		return nil, true, ErrClosedSession
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if s.revision == nil {
		return nil, true, ErrInvalidRuleset
	}
	query, ok := s.revision.query(strings.TrimSpace(name))
	if !ok {
		return nil, true, fmt.Errorf("%w: %s", ErrQueryNotFound, name)
	}
	compiledArgs, err := query.compileArgs(args)
	if err != nil {
		return nil, true, err
	}
	if s.rete == nil || s.rete.graphBeta == nil || len(query.graphConditionBranches) == 0 {
		return nil, false, nil
	}
	trigger := s.queryTriggerFact(query, compiledArgs)
	if trigger.ID().IsZero() {
		return nil, false, nil
	}
	rows, handled, err := s.rete.queryRows(ctx, query, compiledArgs, newReteGraphQueryTriggerEvent(trigger), Snapshot{revision: s.revision})
	if err != nil {
		return nil, true, fmt.Errorf("%w: %v", ErrQueryExecution, err)
	}
	if !handled {
		return nil, false, nil
	}
	return rows, true, nil
}

func (s *Session) queryTriggerFact(query compiledQuery, args map[string]Value) FactSnapshot {
	if s == nil {
		return FactSnapshot{}
	}
	return snapshotQueryTriggerFact(s.generation, query, args).withQueryTriggerRecency(s.nextRecency + 1)
}

func snapshotQueryTriggerFact(generation Generation, query compiledQuery, args map[string]Value) FactSnapshot {
	fields := make(Fields, len(args))
	for _, param := range query.parameters {
		if value, ok := args[param.name]; ok {
			fields[param.name] = cloneValue(value)
		}
	}
	return FactSnapshot{
		id:         newFactID(generation, ^uint64(0)),
		name:       internalQueryTriggerName(query.name),
		version:    1,
		generation: generation,
		fields:     fields,
	}
}

func (f FactSnapshot) withQueryTriggerRecency(recency Recency) FactSnapshot {
	f.recency = recency
	return f
}

func (q compiledQuery) compileArgs(args QueryArgs) (map[string]Value, error) {
	if args == nil {
		args = QueryArgs{}
	}
	values := make(map[string]Value, len(q.parameters))
	for key := range args {
		if _, ok := q.parameterTypes[key]; !ok {
			return nil, fmt.Errorf("%w: unknown argument %q", ErrQueryArgument, key)
		}
	}
	for _, param := range q.parameters {
		raw, ok := args[param.name]
		if !ok {
			return nil, fmt.Errorf("%w: missing argument %q", ErrQueryArgument, param.name)
		}
		value, err := canonicalValue(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrQueryArgument, err)
		}
		if param.kind != ValueAny && value.Kind() != param.kind {
			return nil, fmt.Errorf("%w: argument %q has kind %s, want %s", ErrQueryArgument, param.name, value.Kind(), param.kind)
		}
		values[param.name] = value
	}
	return values, nil
}

func (q compiledQuery) materializeRow(ctx context.Context, source Snapshot, matches []conditionMatch, args map[string]Value) (QueryRow, error) {
	if q.compactMixedReturns() {
		return q.materializeCompactRow(ctx, source, matches, args)
	}
	row := q.newQueryRow()
	owner := newQueryRowOwner(source)
	for i, ret := range q.returns {
		if ret.fact {
			if ret.bindingSlot < 0 || ret.bindingSlot >= len(matches) {
				return QueryRow{}, fmt.Errorf("%w: malformed query return binding %q", ErrQueryExecution, ret.binding)
			}
			match := matches[ret.bindingSlot]
			row.items[i] = queryRowValue{fact: owner.newFact(match.fact), hasFact: true}
			continue
		}
		value, ok, err := ret.expression.evaluateWithContextParamsAndCounters(ctx, conditionFactRef{}, matches, args, &FunctionEvaluationError{
			QueryName:      q.name,
			ConditionIndex: -1,
			PredicateIndex: ret.order,
		}, nil)
		if err != nil {
			return QueryRow{}, err
		}
		if !ok {
			value = NullValue()
		}
		row.items[i] = queryRowValue{value: value}
	}
	return row, nil
}

func (q compiledQuery) materializeTokenRow(ctx context.Context, source Snapshot, token tokenRef, args map[string]Value, bindingSlotOffset int) (QueryRow, error) {
	if q.valueReturnsOnly() {
		return q.materializeTokenValueRowInto(ctx, token, args, bindingSlotOffset, make([]Value, len(q.returns)))
	}
	if q.compactMixedReturns() {
		return q.materializeTokenCompactMixedRowInto(ctx, token, args, bindingSlotOffset, newQueryRowOwner(source), make([]queryRowValue, q.factReturnCount), make([]Value, q.valueReturnCount))
	}
	return q.materializeTokenRowInto(ctx, token, args, bindingSlotOffset, newQueryRowOwner(source), make([]queryRowValue, len(q.returns)))
}

func (q compiledQuery) valueReturnsOnly() bool {
	if len(q.returns) == 0 {
		return false
	}
	for _, ret := range q.returns {
		if ret.fact {
			return false
		}
	}
	return true
}

func (q compiledQuery) compactMixedReturns() bool {
	return q.factReturnCount != 0 && q.valueReturnCount != 0
}

func (q compiledQuery) materializeCompactRow(ctx context.Context, source Snapshot, matches []conditionMatch, args map[string]Value) (QueryRow, error) {
	row := q.newQueryCompactMixedRowWithItems(make([]queryRowValue, q.factReturnCount), make([]Value, q.valueReturnCount))
	owner := newQueryRowOwner(source)
	factIdx := 0
	valueIdx := 0
	for _, ret := range q.returns {
		if ret.fact {
			if ret.bindingSlot < 0 || ret.bindingSlot >= len(matches) {
				return QueryRow{}, fmt.Errorf("%w: malformed query return binding %q", ErrQueryExecution, ret.binding)
			}
			match := matches[ret.bindingSlot]
			row.items[factIdx] = queryRowValue{fact: owner.newFact(match.fact), hasFact: true}
			factIdx++
			continue
		}
		value, ok, err := ret.expression.evaluateWithContextParamsAndCounters(ctx, conditionFactRef{}, matches, args, &FunctionEvaluationError{
			QueryName:      q.name,
			ConditionIndex: -1,
			PredicateIndex: ret.order,
		}, nil)
		if err != nil {
			return QueryRow{}, err
		}
		if !ok {
			value = NullValue()
		}
		row.valueItems[valueIdx] = value
		valueIdx++
	}
	return row, nil
}

func (q compiledQuery) materializeTokenRowInto(ctx context.Context, token tokenRef, args map[string]Value, bindingSlotOffset int, owner *queryRowOwner, items []queryRowValue) (QueryRow, error) {
	if len(items) != len(q.returns) {
		return QueryRow{}, fmt.Errorf("%w: malformed query row item count %d", ErrQueryExecution, len(items))
	}
	row := q.newQueryRowWithItems(items)
	for i, ret := range q.returns {
		if ret.fact {
			tokenSlot := ret.bindingSlot + bindingSlotOffset
			match, ok := tokenRefAtSlot(token, tokenSlot)
			if !ok || match.hasValue {
				return QueryRow{}, fmt.Errorf("%w: malformed query return binding %q", ErrQueryExecution, ret.binding)
			}
			row.items[i] = queryRowValue{fact: owner.newFact(match.fact), hasFact: true}
			continue
		}
		if value, ok, err := ret.projectTokenValue(token, bindingSlotOffset); err != nil {
			return QueryRow{}, err
		} else if ok {
			row.items[i] = queryRowValue{value: value}
			continue
		}
		value, ok, err := ret.expression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, conditionFactRef{}, token, args, bindingSlotOffset, &FunctionEvaluationError{
			QueryName:      q.name,
			ConditionIndex: -1,
			PredicateIndex: ret.order,
		}, nil)
		if err != nil {
			return QueryRow{}, err
		}
		if !ok {
			value = NullValue()
		}
		row.items[i] = queryRowValue{value: value}
	}
	return row, nil
}

func (q compiledQuery) materializeTokenCompactMixedRowInto(ctx context.Context, token tokenRef, args map[string]Value, bindingSlotOffset int, owner *queryRowOwner, items []queryRowValue, values []Value) (QueryRow, error) {
	if len(items) != q.factReturnCount {
		return QueryRow{}, fmt.Errorf("%w: malformed query row fact count %d", ErrQueryExecution, len(items))
	}
	if len(values) != q.valueReturnCount {
		return QueryRow{}, fmt.Errorf("%w: malformed query row value count %d", ErrQueryExecution, len(values))
	}
	row := q.newQueryCompactMixedRowWithItems(items, values)
	factIdx := 0
	valueIdx := 0
	for _, ret := range q.returns {
		if ret.fact {
			tokenSlot := ret.bindingSlot + bindingSlotOffset
			match, ok := tokenRefAtSlot(token, tokenSlot)
			if !ok || match.hasValue {
				return QueryRow{}, fmt.Errorf("%w: malformed query return binding %q", ErrQueryExecution, ret.binding)
			}
			row.items[factIdx] = queryRowValue{fact: owner.newFact(match.fact), hasFact: true}
			factIdx++
			continue
		}
		if value, ok, err := ret.projectTokenValue(token, bindingSlotOffset); err != nil {
			return QueryRow{}, err
		} else if ok {
			row.valueItems[valueIdx] = value
			valueIdx++
			continue
		}
		value, ok, err := ret.expression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, conditionFactRef{}, token, args, bindingSlotOffset, &FunctionEvaluationError{
			QueryName:      q.name,
			ConditionIndex: -1,
			PredicateIndex: ret.order,
		}, nil)
		if err != nil {
			return QueryRow{}, err
		}
		if !ok {
			value = NullValue()
		}
		row.valueItems[valueIdx] = value
		valueIdx++
	}
	return row, nil
}

func (q compiledQuery) materializeTokenValueRowInto(ctx context.Context, token tokenRef, args map[string]Value, bindingSlotOffset int, values []Value) (QueryRow, error) {
	if len(values) != len(q.returns) {
		return QueryRow{}, fmt.Errorf("%w: malformed query row value count %d", ErrQueryExecution, len(values))
	}
	row := q.newQueryValueRowWithItems(values)
	for i, ret := range q.returns {
		if ret.fact {
			return QueryRow{}, fmt.Errorf("%w: malformed value-only query return binding %q", ErrQueryExecution, ret.binding)
		}
		if value, ok, err := ret.projectTokenValue(token, bindingSlotOffset); err != nil {
			return QueryRow{}, err
		} else if ok {
			row.valueItems[i] = value
			continue
		}
		value, ok, err := ret.expression.evaluateTokenWithContextParamsOffsetAndCounters(ctx, conditionFactRef{}, token, args, bindingSlotOffset, &FunctionEvaluationError{
			QueryName:      q.name,
			ConditionIndex: -1,
			PredicateIndex: ret.order,
		}, nil)
		if err != nil {
			return QueryRow{}, err
		}
		if !ok {
			value = NullValue()
		}
		row.valueItems[i] = value
	}
	return row, nil
}

func (r compiledQueryReturn) projectTokenValue(token tokenRef, bindingSlotOffset int) (Value, bool, error) {
	switch r.projection.kind {
	case compiledQueryReturnProjectionConst:
		return r.projection.value, true, nil
	case compiledQueryReturnProjectionBindingField:
		match, ok := tokenRefAtSlot(token, r.projection.bindingSlot+bindingSlotOffset)
		if !ok {
			return NullValue(), true, nil
		}
		if match.hasValue {
			return NullValue(), true, nil
		}
		value, ok := r.projection.access.valueFromFact(match.fact)
		if !ok {
			return NullValue(), true, nil
		}
		return value, true, nil
	case compiledQueryReturnProjectionBindingValue:
		match, ok := tokenRefAtSlot(token, r.projection.bindingSlot+bindingSlotOffset)
		if !ok || !match.hasValue {
			return NullValue(), true, nil
		}
		return match.value, true, nil
	case compiledQueryReturnProjectionGeneric:
		return Value{}, false, nil
	default:
		return Value{}, false, fmt.Errorf("%w: malformed query return projection", ErrMatcher)
	}
}

func (q compiledQuery) newQueryRow() QueryRow {
	return q.newQueryRowWithItems(make([]queryRowValue, len(q.returns)))
}

func (q compiledQuery) newQueryRowWithItems(items []queryRowValue) QueryRow {
	return QueryRow{
		order: q.returnAliases,
		index: q.returnAliasIndex,
		items: items,
	}
}

func (q compiledQuery) newQueryCompactMixedRowWithItems(items []queryRowValue, values []Value) QueryRow {
	return QueryRow{
		order:      q.returnAliases,
		index:      q.compactReturnAliasIndex,
		items:      items,
		valueItems: values,
	}
}

func (q compiledQuery) newQueryValueRowWithItems(values []Value) QueryRow {
	return QueryRow{
		order:      q.returnAliases,
		index:      q.returnAliasIndex,
		valueItems: values,
	}
}
