package gess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type conditionTargetKind uint8

const (
	conditionTargetUnknown conditionTargetKind = iota
	conditionTargetName
	conditionTargetTemplateKey
)

type conditionIndexKind uint8

const (
	conditionIndexUnknown conditionIndexKind = iota
	conditionIndexName
	conditionIndexTemplateKey
)

type conditionTarget struct {
	kind        conditionTargetKind
	name        string
	templateKey TemplateKey
}

type compiledConditionPlan struct {
	id             ConditionID
	binding        string
	bindingSlot    int
	path           []int
	negated        bool
	explicit       bool
	aggregate      *compiledAggregatePlan
	isTest         bool
	target         conditionTarget
	constraints    []compiledFieldConstraint
	listPatterns   []compiledListPattern
	joins          []compiledJoinConstraint
	predicates     []compiledExpressionPredicate
	testPredicates []compiledExpressionPredicate
	indexable      bool
	indexKind      conditionIndexKind
}

type conditionMatch struct {
	conditionID ConditionID
	bindingSlot int
	fact        conditionFactRef
	value       Value
	hasValue    bool
}

func cloneCompiledConditionPlan(plan compiledConditionPlan) compiledConditionPlan {
	out := plan
	out.path = cloneIntPath(plan.path)
	out.constraints = cloneCompiledFieldConstraints(plan.constraints)
	out.listPatterns = cloneCompiledListPatterns(plan.listPatterns)
	out.joins = cloneCompiledJoinConstraints(plan.joins)
	out.predicates = cloneCompiledExpressionPredicates(plan.predicates)
	out.testPredicates = cloneCompiledExpressionPredicates(plan.testPredicates)
	if plan.aggregate != nil {
		aggregate := *plan.aggregate
		aggregate.inputPlans = make([]compiledConditionPlan, len(plan.aggregate.inputPlans))
		for i, input := range plan.aggregate.inputPlans {
			aggregate.inputPlans[i] = cloneCompiledConditionPlan(input)
		}
		aggregate.specs = append([]compiledAggregateSpec(nil), plan.aggregate.specs...)
		out.aggregate = &aggregate
	}
	return out
}

func isValidBindingName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case i == 0 && (r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z')):
		case i > 0 && (r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9')):
		default:
			return false
		}
	}
	return true
}

func conditionIDFor(ruleID RuleID, order int, binding string, name string, templateKey TemplateKey, constraints []FieldConstraint, listPatterns []compiledListPattern, joins []JoinConstraint, predicates []compiledExpressionPredicate, negated bool) ConditionID {
	sum := sha256.New()
	sum.Write([]byte("gess/condition/v1\n"))
	sum.Write([]byte("rule:"))
	sum.Write([]byte(ruleID.String()))
	sum.Write([]byte("\norder:"))
	sum.Write(fmt.Appendf(nil, "%d", order))
	sum.Write([]byte("\nbinding:"))
	sum.Write([]byte(binding))
	if negated {
		sum.Write([]byte("\nnegated:true"))
	}
	sum.Write([]byte("\nname:"))
	sum.Write([]byte(name))
	sum.Write([]byte("\ntemplate-key:"))
	sum.Write([]byte(templateKey.String()))
	sum.Write([]byte("\nconstraints:"))
	for _, constraint := range constraints {
		sum.Write([]byte(constraint.Path.display()))
		sum.Write([]byte(":"))
		sum.Write([]byte(string(constraint.Operator)))
		sum.Write([]byte(":"))
		sum.Write([]byte(constraint.Value.String()))
		sum.Write([]byte(";"))
	}
	sum.Write([]byte("\nlist-patterns:"))
	sum.Write([]byte(serializeCompiledListPatterns(listPatterns)))
	sum.Write([]byte("\njoins:"))
	for _, join := range joins {
		sum.Write([]byte(join.Path.display()))
		sum.Write([]byte(":"))
		sum.Write([]byte(string(join.Operator)))
		sum.Write([]byte(":"))
		sum.Write([]byte(join.Ref.Binding))
		sum.Write([]byte("."))
		sum.Write([]byte(join.Ref.Path.display()))
		sum.Write([]byte(";"))
	}
	if len(predicates) > 0 {
		sum.Write([]byte("\npredicates:"))
		sum.Write([]byte(serializeCompiledExpressionPredicates(predicates)))
	}
	return ConditionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}

func aggregateConditionIDFor(ruleID RuleID, order int, specs []compiledAggregateSpec, input []compiledConditionPlan) ConditionID {
	sum := sha256.New()
	sum.Write([]byte("gess/aggregate-condition/v1\n"))
	sum.Write([]byte("rule:"))
	sum.Write([]byte(ruleID.String()))
	sum.Write([]byte("\norder:"))
	sum.Write(fmt.Appendf(nil, "%d", order))
	for _, spec := range specs {
		sum.Write([]byte("\nspec:"))
		sum.Write([]byte(spec.binding))
		sum.Write([]byte(":"))
		sum.Write([]byte(spec.kind))
		if spec.hasExpr {
			sum.Write([]byte(":"))
			sum.Write([]byte(serializeCompiledExpression(spec.expression)))
		}
	}
	for _, plan := range input {
		sum.Write([]byte("\ninput:"))
		sum.Write([]byte(plan.id.String()))
	}
	return ConditionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}
