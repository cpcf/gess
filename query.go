package gess

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	Description   string
	Parameters    []QueryParameterSpec
	Conditions    []RuleConditionSpec
	ConditionTree ConditionSpec
	Returns       []QueryReturnSpec
}

func (s QuerySpec) clone() QuerySpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
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
	name                 string
	description          string
	parameters           []QueryParameter
	parameterTypes       map[string]ValueKind
	conditions           []RuleCondition
	treeConditions       []RuleCondition
	conditionTree        RuleConditionTree
	conditionBranches    []compiledConditionBranch
	conditionBranchPlans []RuleConditionBranch
	returns              []compiledQueryReturn
}

type compiledQueryReturn struct {
	alias       string
	binding     string
	bindingSlot int
	expression  compiledExpression
	rawExpr     ExpressionSpec
	fact        bool
	order       int
}

func (q compiledQuery) inspect() Query {
	return Query{
		name:              q.name,
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

func compileQuerySpec(spec QuerySpec, templatesByKey map[TemplateKey]Template) (compiledQuery, error) {
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
	inspectionSet, err := compileNormalizedRuleConditionBranchWithParams(normalized.Name, queryRuleID, normalizedConditions, templatesByKey, true, paramTypes)
	if err != nil {
		return compiledQuery{}, markQueryValidation(err)
	}
	compiledBranches := make([]compiledConditionBranch, 0, len(normalizedBranches))
	var representative compiledRuleConditionSet
	for branchIndex, branch := range normalizedBranches {
		compiledBranch, err := compileNormalizedRuleConditionBranchWithParams(normalized.Name, queryRuleID, branch.conditions, templatesByKey, false, paramTypes)
		if err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		if branchIndex == 0 {
			representative = compiledBranch
		} else if err := validateBranchBindingContract(normalized.Name, representative.conditions, compiledBranch.conditions); err != nil {
			return compiledQuery{}, markQueryValidation(err)
		}
		compiledBranches = append(compiledBranches, compiledConditionBranch{
			id:         branchIndex,
			conditions: compiledBranch.branchConditions,
			plans:      compiledBranch.conditionPlans,
		})
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
	returns, err := compileQueryReturns(normalized.Name, normalized.Returns, representative.conditions, bindingSlots, templatesByKey, paramTypes)
	if err != nil {
		return compiledQuery{}, err
	}

	return compiledQuery{
		name:                 normalized.Name,
		description:          normalized.Description,
		parameters:           params,
		parameterTypes:       paramTypes,
		conditions:           representative.conditions,
		treeConditions:       inspectionSet.treeConditions,
		conditionTree:        buildRuleConditionTree(conditionTreeShape, inspectionSet.treeConditions),
		conditionBranches:    compiledBranches,
		conditionBranchPlans: conditionBranches,
		returns:              returns,
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

func compileQueryReturns(queryName string, specs []QueryReturnSpec, conditions []RuleCondition, bindingSlots map[string]int, templatesByKey map[TemplateKey]Template, params map[string]ValueKind) ([]compiledQueryReturn, error) {
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
		expression, _, err := compileExpressionSpecWithParams(spec.Expression, queryName, -1, i, nil, conditions, bindingSlots, templatesByKey, params)
		if err != nil {
			return nil, markQueryValidation(err)
		}
		returns = append(returns, compiledQueryReturn{
			alias:       spec.Alias,
			expression:  expression,
			rawExpr:     cloneExpressionSpec(spec.Expression),
			bindingSlot: -1,
			order:       i,
		})
	}
	return returns, nil
}

func expressionContainsCurrentField(spec ExpressionSpec) bool {
	switch expression := spec.(type) {
	case CurrentFieldExpr, *CurrentFieldExpr:
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
	values map[string]queryRowValue
	order  []string
}

type queryRowValue struct {
	fact    FactSnapshot
	value   Value
	hasFact bool
}

func (r QueryRow) Aliases() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

func (r QueryRow) Fact(alias string) (FactSnapshot, bool) {
	item, ok := r.values[alias]
	if !ok || !item.hasFact {
		return FactSnapshot{}, false
	}
	return item.fact.clone(), true
}

func (r QueryRow) Value(alias string) (Value, bool) {
	item, ok := r.values[alias]
	if !ok || item.hasFact {
		return Value{}, false
	}
	return cloneValue(item.value), true
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
	out := QueryRow{
		values: make(map[string]queryRowValue, len(r.values)),
		order:  append([]string(nil), r.order...),
	}
	for key, value := range r.values {
		value.fact = value.fact.clone()
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
	it, err := s.Query(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return it.All(ctx)
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
	return query.materialize(ctx, s, compiledArgs)
}

func (s *Session) Query(ctx context.Context, name string, args QueryArgs) (*QueryIterator, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Query(ctx, name, args)
}

func (s *Session) QueryAll(ctx context.Context, name string, args QueryArgs) ([]QueryRow, error) {
	it, err := s.Query(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return it.All(ctx)
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

func (q compiledQuery) materialize(ctx context.Context, source Snapshot, args map[string]Value) ([]QueryRow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var rows []QueryRow
	for _, branch := range q.conditionBranches {
		plans := branch.plans
		if len(plans) == 0 {
			continue
		}
		var walk func(int, []conditionMatch) error
		walk = func(conditionIndex int, selected []conditionMatch) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if conditionIndex == len(plans) {
				row, err := q.materializeRow(source, selected, args)
				if err != nil {
					return err
				}
				rows = append(rows, row)
				return nil
			}

			plan := plans[conditionIndex]
			if plan.aggregate != nil {
				bindings, ok, err := plan.aggregate.evaluate(ctx, source, selected)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				next := make([]conditionMatch, len(selected)+len(bindings))
				copy(next, selected)
				for i, binding := range bindings {
					next[len(selected)+i] = conditionMatch{
						conditionID: plan.id,
						bindingSlot: plan.bindingSlot + i,
						value:       binding.value,
						hasValue:    true,
					}
				}
				return walk(conditionIndex+1, next)
			}
			if plan.negated {
				matches, err := plan.scanWithBindingsAndParams(ctx, source, selected, args)
				if err != nil {
					return err
				}
				if len(matches) == 0 {
					return walk(conditionIndex+1, selected)
				}
				return nil
			}
			matches, err := plan.scanWithBindingsAndParams(ctx, source, selected, args)
			if err != nil {
				return err
			}
			for _, match := range matches {
				next := make([]conditionMatch, len(selected)+1)
				copy(next, selected)
				next[len(selected)] = match
				if err := walk(conditionIndex+1, next); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(0, nil); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrQueryExecution, err)
		}
	}
	return rows, nil
}

func (q compiledQuery) materializeRow(source Snapshot, matches []conditionMatch, args map[string]Value) (QueryRow, error) {
	row := QueryRow{
		values: make(map[string]queryRowValue, len(q.returns)),
		order:  make([]string, 0, len(q.returns)),
	}
	for _, ret := range q.returns {
		row.order = append(row.order, ret.alias)
		if ret.fact {
			if ret.bindingSlot < 0 || ret.bindingSlot >= len(matches) {
				return QueryRow{}, fmt.Errorf("%w: malformed query return binding %q", ErrQueryExecution, ret.binding)
			}
			match := matches[ret.bindingSlot]
			fact, ok := source.Fact(match.fact.ID())
			if !ok {
				fact = match.fact.snapshot()
			}
			row.values[ret.alias] = queryRowValue{fact: fact, hasFact: true}
			continue
		}
		value, ok, err := ret.expression.evaluateWithParams(conditionFactRef{}, matches, args)
		if err != nil {
			return QueryRow{}, err
		}
		if !ok {
			value = NullValue()
		}
		row.values[ret.alias] = queryRowValue{value: value}
	}
	return row, nil
}
