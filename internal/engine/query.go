package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	gessrules "github.com/cpcf/gess/rules"
)

type QueryArgs map[string]any

type QueryParameterSpec = gessrules.QueryParameterSpec

func cloneQueryParameterSpec(s QueryParameterSpec) QueryParameterSpec {
	return gessrules.CloneQueryParameterSpec(s)
}

type QueryReturnSpec = gessrules.QueryReturnSpec

func cloneQueryReturnSpec(s QueryReturnSpec) QueryReturnSpec {
	return gessrules.CloneQueryReturnSpec(s)
}

func ReturnFact(alias, binding string) QueryReturnSpec {
	return gessrules.ReturnFact(alias, binding)
}

func ReturnValue(alias string, expression ExpressionSpec) QueryReturnSpec {
	return gessrules.ReturnValue(alias, expression)
}

type QuerySpec = gessrules.QuerySpec

func cloneQuerySpec(s QuerySpec) QuerySpec {
	return gessrules.CloneQuerySpec(s)
}

type QueryParameter = gessrules.QueryParameter

func cloneQueryParameters(parameters []QueryParameter) []QueryParameter {
	return gessrules.CloneQueryParameters(parameters)
}

type QueryReturn = gessrules.QueryReturn

func cloneQueryReturn(ret QueryReturn) QueryReturn {
	return gessrules.CloneQueryReturn(ret)
}

func cloneQueryReturns(returns []QueryReturn) []QueryReturn {
	return gessrules.CloneQueryReturns(returns)
}

type Query = gessrules.Query

func cloneQuery(q Query) Query {
	return gessrules.CloneQuery(q)
}

type compiledQuery struct {
	name                    string
	module                  ModuleName
	description             string
	source                  SourceSpan
	gessSource              string
	triggerName             string
	triggerFieldSpecs       []FieldSpec
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

type compiledQueryArgs struct {
	values []Value
	byName map[string]Value
}

func (a *compiledQueryArgs) value(index int) (Value, bool) {
	if a == nil || index < 0 || index >= len(a.values) {
		return Value{}, false
	}
	return a.values[index], true
}

func (a *compiledQueryArgs) mapView(query compiledQuery) map[string]Value {
	if a == nil {
		return nil
	}
	if a.byName == nil {
		values := make(map[string]Value, len(query.parameters))
		for i, param := range query.parameters {
			if value, ok := a.value(i); ok {
				values[param.NameValue] = value
			}
		}
		a.byName = values
	}
	return a.byName
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
	source      SourceSpan
	evalMeta    *FunctionEvaluationError
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
		NameValue:             q.name,
		ModuleValue:           q.module,
		DescriptionText:       q.description,
		SourceSpan:            q.source,
		GessSourceText:        q.gessSource,
		ParameterValues:       cloneQueryParameters(q.parameters),
		ConditionValues:       cloneRuleConditions(q.conditions),
		ConditionTreeValue:    cloneRuleConditionTree(q.conditionTree),
		ConditionBranchValues: cloneRuleConditionBranches(q.conditionBranchPlans),
		ReturnValues:          q.inspectReturns(),
	}
}

func (q compiledQuery) inspectReturns() []QueryReturn {
	out := make([]QueryReturn, len(q.returns))
	for i, ret := range q.returns {
		out[i] = QueryReturn{
			AliasValue:     ret.alias,
			BindingName:    ret.binding,
			ExpressionSpec: cloneExpressionSpec(ret.rawExpr),
			FactValue:      ret.fact,
			Order:          ret.order,
			SourceSpan:     ret.source,
		}
	}
	return out
}

func compileQuerySpec(spec QuerySpec, templates templateResolver, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledQuery, error) {
	normalized := cloneQuerySpec(spec)
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
	inspectionSet, err := compileNormalizedRuleConditionBranchWithParams(normalized.Name, queryRuleID, normalized.Module, normalizedConditions, templates, true, paramTypes, functions, globals)
	if err != nil {
		return compiledQuery{}, markQueryValidation(err)
	}
	compiledBranches := make([]compiledConditionBranch, 0, len(normalizedBranches))
	graphBranches := make([]compiledConditionBranch, 0, len(normalizedBranches))
	var representative compiledRuleConditionSet
	for branchIndex, branch := range normalizedBranches {
		branchIR := newReorderedBranchPlanningIR(branchIndex, branch.conditions)
		compiledBranch, err := compileBranchPlanningIR(normalized.Name, queryRuleID, normalized.Module, branchIR, templates, false, paramTypes, functions, globals)
		if err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		if branchIndex == 0 {
			representative = compiledBranch
		} else if err := validateBranchBindingContract(normalized.Name, representative.conditions, compiledBranch.conditions); err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		compiledBranches = append(compiledBranches, compiledConditionBranchFromPlanningIR(branchIR, compiledBranch))
		graphBranch, ok, err := compileQueryGraphBranch(normalized.Name, queryRuleID, normalized.Module, branchIndex, branch.conditions, templates, paramTypes, functions, globals)
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
			IDValue:         branch.id,
			ConditionValues: cloneRuleConditionBranchConditions(branch.conditions),
		}
	}
	bindingSlots := make(map[string]int, len(representative.conditions))
	for i, condition := range representative.conditions {
		bindingSlots[condition.BindingName] = i
	}
	returns, err := compileQueryReturns(normalized.Name, normalized.Returns, representative.conditions, bindingSlots, templates.byKey, paramTypes, functions, globals)
	if err != nil {
		return compiledQuery{}, err
	}

	returnAliases, returnAliasIndex := compileQueryReturnIndexes(returns)
	compactReturnAliasIndex, factReturnCount, valueReturnCount := compileQueryReturnCompactIndexes(returns)

	return compiledQuery{
		name:                    normalized.Name,
		module:                  normalized.Module,
		description:             normalized.Description,
		source:                  normalized.Source,
		gessSource:              normalized.GessSource,
		triggerName:             internalQueryTriggerName(normalized.Name),
		triggerFieldSpecs:       compileQueryTriggerFieldSpecs(params),
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
		param = cloneQueryParameterSpec(param)
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
		params = append(params, QueryParameter{NameValue: param.Name, KindValue: param.Kind, Order: i})
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

func compileQueryReturns(queryName string, specs []QueryReturnSpec, conditions []RuleCondition, bindingSlots map[string]int, templatesByKey map[TemplateKey]compiledTemplate, params map[string]ValueKind, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) ([]compiledQueryReturn, error) {
	returns := make([]compiledQueryReturn, 0, len(specs))
	aliases := make(map[string]struct{}, len(specs))
	for i, spec := range specs {
		spec = cloneQueryReturnSpec(spec)
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
				source:      spec.Source,
			})
			continue
		}
		if expressionContainsCurrentField(spec.Expression) {
			return nil, &ValidationError{RuleName: queryName, Reason: "query return value expressions cannot use current field references", Err: ErrQueryValidation}
		}
		expression, _, err := compileExpressionSpecWithParams(spec.Expression, queryName, -1, i, nil, conditions, bindingSlots, templatesByKey, params, functions, globals)
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
			source:      spec.Source,
			evalMeta: &FunctionEvaluationError{
				QueryName:      queryName,
				ConditionIndex: -1,
				PredicateIndex: i,
				Source:         spec.Source,
			},
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

func compileQueryTriggerFieldSpecs(params []QueryParameter) []FieldSpec {
	if len(params) == 0 {
		return nil
	}
	specs := make([]FieldSpec, len(params))
	for i, param := range params {
		specs[i] = FieldSpec{Name: param.NameValue, Kind: param.KindValue, Required: true}
	}
	return specs
}

func compileQueryGraphBranch(queryName string, queryRuleID RuleID, author ModuleName, branchIndex int, branch []normalizedRuleCondition, templates templateResolver, params map[string]ValueKind, functions map[string]compiledPureFunction, globals map[string]compiledGlobal) (compiledConditionBranch, bool, error) {
	branchIR, ok := newQueryGraphBranchPlanningIR(queryName, branchIndex, branch, params)
	if !ok {
		return compiledConditionBranch{}, false, nil
	}
	compiled, err := compileBranchPlanningIR(queryName, queryRuleID, author, branchIR, templates, false, nil, functions, globals)
	if err != nil {
		return compiledConditionBranch{}, false, err
	}
	return compiledConditionBranchFromPlanningIR(branchIR, compiled), true, nil
}

func lowerQueryConditionParams(condition RuleConditionSpec, params map[string]ValueKind) RuleConditionSpec {
	out := cloneRuleConditionSpec(condition)
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
	out := cloneAccumulateCondition(condition)
	if len(params) == 0 {
		return out
	}
	out.Input = lowerQueryConditionTreeParams(out.Input, params)
	for i := range out.Specs {
		out.Specs[i].ExpressionSpec = lowerQueryParamExpression(out.Specs[i].ExpressionSpec, params)
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
		out := cloneConditionSpec(condition).(And)
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
		out := cloneConditionSpec(condition).(Or)
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
		out := cloneConditionSpec(condition).(Not)
		out.Condition = lowerQueryConditionTreeParams(out.Condition, params)
		return out
	case *Not:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(Not)
		return &out
	case Explicit:
		out := cloneConditionSpec(condition).(Explicit)
		out.Condition = lowerQueryConditionTreeParams(out.Condition, params)
		return out
	case *Explicit:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(Explicit)
		return &out
	case ExistsCondition:
		out := cloneConditionSpec(condition).(ExistsCondition)
		out.Condition = lowerQueryConditionTreeParams(out.Condition, params)
		return out
	case *ExistsCondition:
		if condition == nil {
			return nil
		}
		out := lowerQueryConditionTreeParams(*condition, params).(ExistsCondition)
		return &out
	case ForallCondition:
		out := cloneConditionSpec(condition).(ForallCondition)
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
		out := cloneConditionSpec(condition).(Test)
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
	return pathRoot(path), true
}

func queryCurrentPathExpr(spec ExpressionSpec) (PathSpec, bool) {
	switch expression := spec.(type) {
	case CurrentFieldExpr:
		normalized := cloneExpressionSpec(expression).(CurrentFieldExpr)
		path := pathOrField(normalized.Path, normalized.Field)
		return path, !pathIsZero(path)
	case *CurrentFieldExpr:
		if expression == nil {
			return PathSpec{}, false
		}
		return queryCurrentPathExpr(*expression)
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
		return cloneExpressionSpec(expression)
	case *ParamExpr:
		if expression == nil {
			return nil
		}
		return lowerQueryParamExpression(*expression, params)
	case CompareExpr:
		out := cloneExpressionSpec(expression).(CompareExpr)
		out.Left = lowerQueryParamExpression(out.Left, params)
		out.Right = lowerQueryParamExpression(out.Right, params)
		return out
	case *CompareExpr:
		if expression == nil {
			return nil
		}
		out := cloneExpressionSpec(*expression).(CompareExpr)
		out.Left = lowerQueryParamExpression(out.Left, params)
		out.Right = lowerQueryParamExpression(out.Right, params)
		return &out
	case BooleanExpr:
		out := cloneExpressionSpec(expression).(BooleanExpr)
		for i := range out.Operands {
			out.Operands[i] = lowerQueryParamExpression(out.Operands[i], params)
		}
		return out
	case *BooleanExpr:
		if expression == nil {
			return nil
		}
		out := cloneExpressionSpec(*expression).(BooleanExpr)
		for i := range out.Operands {
			out.Operands[i] = lowerQueryParamExpression(out.Operands[i], params)
		}
		return &out
	case CallExpr:
		out := cloneExpressionSpec(expression).(CallExpr)
		for i := range out.Args {
			out.Args[i] = lowerQueryParamExpression(out.Args[i], params)
		}
		return out
	case *CallExpr:
		if expression == nil {
			return nil
		}
		out := cloneExpressionSpec(*expression).(CallExpr)
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
	runtime, err := newReteRuntime(s.revision, s.globalValues)
	if err != nil {
		return nil, err
	}
	if err := runtime.resetGraphBetaForGeneration(ctx, s.facts, s.generation); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQueryExecution, err)
	}
	trigger := snapshotQueryTriggerFact(s.generation, query, &compiledArgs)
	rows, handled, err := runtime.queryRows(ctx, query, &compiledArgs, newReteGraphQueryTriggerEvent(trigger), s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQueryExecution, err)
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
	mutationHeld := true
	defer func() {
		if mutationHeld {
			s.endMutation()
		}
	}()
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
	trigger := s.queryTriggerFact(query, &compiledArgs)
	if trigger.ID().IsZero() {
		return nil, false, nil
	}
	rows, handled, err := s.queryGraphRowsWithBackchain(ctx, query, &compiledArgs, trigger, &mutationHeld)
	if err != nil {
		return nil, true, fmt.Errorf("%w: %w", ErrQueryExecution, err)
	}
	if !handled {
		return nil, false, nil
	}
	return rows, true, nil
}

func (s *Session) queryGraphRowsWithBackchain(ctx context.Context, query compiledQuery, args *compiledQueryArgs, trigger FactSnapshot, mutationHeld *bool) ([]QueryRow, bool, error) {
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return nil, false, nil
	}
	terminalIDs := s.rete.graphBeta.graph.queryTerminalIDs[query.name]
	if len(terminalIDs) == 0 {
		return nil, false, nil
	}

	s.rete.graphBeta.clearQueryTerminalRows(terminalIDs)
	proof := s.beginBackchainQueryProof()
	previousProof := s.activeBackchainQueryProof
	s.activeBackchainQueryProof = proof
	defer func() {
		s.activeBackchainQueryProof = previousProof
	}()

	cleanupTrigger := true
	defer func() {
		if cleanupTrigger {
			s.rete.graphBeta.clearQueryTerminalRows(terminalIDs)
			cleanupCtx := context.WithoutCancel(ctx)
			delta, err := s.cleanupQueryProofImmediate(cleanupCtx, trigger, proof)
			if err == nil && queryAgendaDeltaHasRuleChanges(delta) {
				_, _ = s.reconcileAgendaAfterMutation(cleanupCtx, delta)
			}
		}
	}()
	agendaDelta, needsProof, err := s.insertQueryTriggerForProofImmediate(ctx, trigger, proof)
	if err != nil {
		return nil, true, err
	}
	if queryAgendaDeltaHasRuleChanges(agendaDelta) {
		if _, err := s.reconcileAgendaAfterMutation(ctx, agendaDelta); err != nil {
			return nil, true, err
		}
	}
	if needsProof && !queryAgendaDeltaHasRuleChanges(agendaDelta) {
		needsProof = false
	}
	if needsProof {
		result, held, err := s.runAgendaWithMutationReleased(ctx, runConfig{})
		if mutationHeld != nil {
			*mutationHeld = held
		}
		if err != nil {
			return nil, true, err
		}
		if result.Status != RunCompleted {
			return nil, true, fmt.Errorf("%w: query %q proof run ended with status %s", ErrUnsupportedRuntime, query.name, result.Status)
		}
	}
	if mutationHeld != nil && !*mutationHeld {
		return nil, true, ErrConcurrencyMisuse
	}

	rows, err := s.rete.graphBeta.materializeQueryTerminalRows(ctx, query, args, Snapshot{revision: s.revision}, terminalIDs)
	if err != nil {
		return nil, true, err
	}
	s.rete.graphBeta.clearQueryTerminalRows(terminalIDs)
	// Cleanup must not be abortable by the caller's context: a cancellation
	// landing after row materialization would otherwise leak the query
	// trigger and transient demand facts into graph memory permanently.
	cleanupCtx := context.WithoutCancel(ctx)
	cleanupDelta, err := s.cleanupQueryProofImmediate(cleanupCtx, trigger, proof)
	if err != nil {
		return nil, true, err
	}
	cleanupTrigger = false
	if queryAgendaDeltaHasRuleChanges(cleanupDelta) {
		if _, err := s.reconcileAgendaAfterMutation(cleanupCtx, cleanupDelta); err != nil {
			return nil, true, err
		}
	}
	return rows, true, nil
}

func (s *Session) insertQueryTriggerForProofImmediate(ctx context.Context, trigger FactSnapshot, proof *backchainQueryProofContext) (reteAgendaDelta, bool, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return combined, false, ErrInvalidRuleset
	}
	_, _ = s.rete.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerRemoveEvent(trigger))
	delta, err := s.rete.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerEvent(trigger))
	if err != nil {
		return combined, false, err
	}
	combined = mergeReteAgendaDelta(combined, normalizeBackchainDemandNoopDelta(delta))
	demandDelta, err := proof.flushDemands(ctx, combined.demands, mutationOrigin{})
	if err != nil {
		return mergeReteAgendaDelta(combined, demandDelta), false, err
	}
	combined = mergeReteAgendaDelta(combined, demandDelta)
	combined.demands = nil
	combined.resolvedDemands = nil
	combined.resolvedOwners = nil
	return combined, queryAgendaDeltaHasRuleChanges(combined), nil
}

func (s *Session) cleanupQueryProofImmediate(ctx context.Context, trigger FactSnapshot, proof *backchainQueryProofContext) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return combined, nil
	}
	proofDelta, err := proof.cleanup(ctx)
	if err != nil {
		return combined, err
	}
	combined = mergeReteAgendaDelta(combined, proofDelta)
	graphDelta, err := s.rete.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerRemoveEvent(trigger))
	if err != nil {
		return combined, err
	}
	combined = mergeReteAgendaDelta(combined, normalizeBackchainDemandNoopDelta(graphDelta))
	s.clearBackchainDemandRequestArena()
	combined.demands = nil
	combined.resolvedDemands = nil
	combined.resolvedOwners = nil
	return combined, nil
}

func (s *Session) insertQueryTriggerImmediate(ctx context.Context, trigger FactSnapshot) (reteAgendaDelta, bool, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return combined, false, ErrInvalidRuleset
	}
	_, _ = s.rete.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerRemoveEvent(trigger))
	delta, err := s.rete.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerEvent(trigger))
	if err != nil {
		return combined, false, err
	}
	combined = mergeReteAgendaDelta(combined, delta)
	needsProof := len(delta.demands) > 0 || len(delta.resolvedDemands) > 0 || len(delta.resolvedOwners) > 0
	completed, err := s.completeBackchainQueryDeltaImmediate(ctx, combined, mutationOrigin{})
	if err != nil {
		return mergeReteAgendaDelta(combined, completed), needsProof, err
	}
	if len(completed.added) > 0 || len(completed.removed) > 0 || len(completed.updated) > 0 {
		needsProof = true
	}
	return completed, needsProof, nil
}

func (s *Session) cleanupQueryTriggerImmediate(ctx context.Context, trigger FactSnapshot) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || s.rete == nil || s.rete.graphBeta == nil {
		return combined, nil
	}
	supportDelta, err := s.removeBackchainDemandSupportsForFact(ctx, trigger.ID(), mutationOrigin{})
	if err != nil {
		return combined, err
	}
	combined = mergeReteAgendaDelta(combined, supportDelta)
	graphDelta, err := s.rete.graphBeta.propagateEvent(ctx, newReteGraphQueryTriggerRemoveEvent(trigger))
	if err != nil {
		return combined, err
	}
	combined = mergeReteAgendaDelta(combined, graphDelta)
	completed, err := s.completeBackchainQueryDeltaImmediate(ctx, combined, mutationOrigin{})
	if err != nil {
		return mergeReteAgendaDelta(combined, completed), err
	}
	return completed, nil
}

func queryAgendaDeltaHasRuleChanges(delta reteAgendaDelta) bool {
	return len(delta.added) > 0 || len(delta.removed) > 0 || len(delta.updated) > 0
}

func (s *Session) completeBackchainQueryDeltaImmediate(ctx context.Context, delta reteAgendaDelta, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := normalizeBackchainDemandNoopDelta(delta)
	resolvedDelta, err := s.resolveBackchainDemandRequestsImmediate(ctx, combined.resolvedDemands, combined.resolvedOwners, origin)
	if err != nil {
		return mergeReteAgendaDelta(combined, resolvedDelta), err
	}
	combined = mergeReteAgendaDelta(combined, resolvedDelta)
	state := s.activeFactWorkspace()
	demandDelta, err := s.flushBackchainDemandRequestsImmediate(ctx, &state, combined.demands, origin)
	if err != nil {
		return mergeReteAgendaDelta(combined, demandDelta), err
	}
	s.commitFactWorkspace(state)
	combined = mergeReteAgendaDelta(combined, demandDelta)
	combined.demands = nil
	combined.resolvedDemands = nil
	combined.resolvedOwners = nil
	return combined, nil
}

func (s *Session) queryTriggerFact(query compiledQuery, args *compiledQueryArgs) FactSnapshot {
	if s == nil {
		return FactSnapshot{}
	}
	return snapshotQueryTriggerFact(s.generation, query, args).withQueryTriggerRecency(s.nextRecency + 1)
}

func snapshotQueryTriggerFact(generation Generation, query compiledQuery, args *compiledQueryArgs) FactSnapshot {
	slots := make([]factSlot, len(query.parameters))
	for i := range query.parameters {
		if value, ok := args.value(i); ok {
			slots[i] = factSlot{
				value:    cloneValue(value),
				ok:       true,
				presence: fieldPresenceExplicit,
			}
		}
	}
	return FactSnapshot{
		id:         newFactID(generation, ^uint64(0)),
		name:       query.triggerName,
		version:    1,
		generation: generation,
		fieldSlots: slots,
		fieldSpecs: query.triggerFieldSpecs,
	}
}

func (f FactSnapshot) withQueryTriggerRecency(recency Recency) FactSnapshot {
	f.recency = recency
	return f
}

func (q compiledQuery) compileArgs(args QueryArgs) (compiledQueryArgs, error) {
	if args == nil {
		args = QueryArgs{}
	}
	for key := range args {
		if _, ok := q.parameterTypes[key]; !ok {
			return compiledQueryArgs{}, fmt.Errorf("%w: unknown argument %q", ErrQueryArgument, key)
		}
	}
	values := make([]Value, len(q.parameters))
	for _, param := range q.parameters {
		raw, ok := args[param.NameValue]
		if !ok {
			return compiledQueryArgs{}, fmt.Errorf("%w: missing argument %q", ErrQueryArgument, param.NameValue)
		}
		value, err := canonicalValue(raw)
		if err != nil {
			return compiledQueryArgs{}, fmt.Errorf("%w: %v", ErrQueryArgument, err)
		}
		if param.KindValue != ValueAny && value.Kind() != param.KindValue {
			return compiledQueryArgs{}, fmt.Errorf("%w: argument %q has kind %s, want %s", ErrQueryArgument, param.NameValue, value.Kind(), param.KindValue)
		}
		values[param.Order] = value
	}
	return compiledQueryArgs{values: values}, nil
}

func (q compiledQuery) materializeRow(ctx context.Context, source Snapshot, matches []conditionMatch, args *compiledQueryArgs, globals []Value) (QueryRow, error) {
	if q.compactMixedReturns() {
		return q.materializeCompactRow(ctx, source, matches, args, globals)
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
		value, ok, err := ret.expression.evaluateWithContextParamsGlobalsAndCounters(ctx, conditionFactRef{}, matches, args.mapView(q), globals, ret.evalMeta, nil)
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

func (q compiledQuery) materializeTokenRow(ctx context.Context, source Snapshot, token tokenRef, args *compiledQueryArgs, bindingSlotOffset int, globals []Value) (QueryRow, error) {
	if q.valueReturnsOnly() {
		return q.materializeTokenValueRowInto(ctx, token, args, bindingSlotOffset, globals, make([]Value, len(q.returns)))
	}
	if q.compactMixedReturns() {
		return q.materializeTokenCompactMixedRowInto(ctx, token, args, bindingSlotOffset, globals, newQueryRowOwner(source), make([]queryRowValue, q.factReturnCount), make([]Value, q.valueReturnCount))
	}
	return q.materializeTokenRowInto(ctx, token, args, bindingSlotOffset, globals, newQueryRowOwner(source), make([]queryRowValue, len(q.returns)))
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

func (q compiledQuery) materializeCompactRow(ctx context.Context, source Snapshot, matches []conditionMatch, args *compiledQueryArgs, globals []Value) (QueryRow, error) {
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
		value, ok, err := ret.expression.evaluateWithContextParamsGlobalsAndCounters(ctx, conditionFactRef{}, matches, args.mapView(q), globals, ret.evalMeta, nil)
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

func (q compiledQuery) materializeTokenRowInto(ctx context.Context, token tokenRef, args *compiledQueryArgs, bindingSlotOffset int, globals []Value, owner *queryRowOwner, items []queryRowValue) (QueryRow, error) {
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
		value, ok, err := ret.expression.evaluateTokenWithContextParamsGlobalsOffsetAndCounters(ctx, conditionFactRef{}, token, args.mapView(q), globals, bindingSlotOffset, ret.evalMeta, nil)
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

func (q compiledQuery) materializeTokenCompactMixedRowInto(ctx context.Context, token tokenRef, args *compiledQueryArgs, bindingSlotOffset int, globals []Value, owner *queryRowOwner, items []queryRowValue, values []Value) (QueryRow, error) {
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
		value, ok, err := ret.expression.evaluateTokenWithContextParamsGlobalsOffsetAndCounters(ctx, conditionFactRef{}, token, args.mapView(q), globals, bindingSlotOffset, ret.evalMeta, nil)
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

func (q compiledQuery) materializeTokenValueRowInto(ctx context.Context, token tokenRef, args *compiledQueryArgs, bindingSlotOffset int, globals []Value, values []Value) (QueryRow, error) {
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
		value, ok, err := ret.expression.evaluateTokenWithContextParamsGlobalsOffsetAndCounters(ctx, conditionFactRef{}, token, args.mapView(q), globals, bindingSlotOffset, ret.evalMeta, nil)
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
