package rules

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
)

type (
	ActionFunc                     = engine.ActionFunc
	ActionContext                  = engine.ActionContext
	ActionSpec                     = engine.ActionSpec
	ActionBindingReadSetSpec       = engine.ActionBindingReadSetSpec
	ActionBindingReadSpec          = engine.ActionBindingReadSpec
	AssertTemplateValuesActionSpec = engine.AssertTemplateValuesActionSpec
	Action                         = engine.Action
	AggregateKind                  = engine.AggregateKind
	AggregateSpec                  = engine.AggregateSpec
	AccumulateCondition            = engine.AccumulateCondition
	ValidationError                = engine.ValidationError
	ExpressionSpec                 = engine.ExpressionSpec
	ExpressionComparisonOperator   = engine.ExpressionComparisonOperator
	ExpressionBooleanOperator      = engine.ExpressionBooleanOperator
	ConstExpr                      = engine.ConstExpr
	CurrentFieldExpr               = engine.CurrentFieldExpr
	BindingFieldExpr               = engine.BindingFieldExpr
	HasPathExpr                    = engine.HasPathExpr
	BindingValueExpr               = engine.BindingValueExpr
	ParamExpr                      = engine.ParamExpr
	GlobalExpr                     = engine.GlobalExpr
	CallExpr                       = engine.CallExpr
	CompareExpr                    = engine.CompareExpr
	BooleanExpr                    = engine.BooleanExpr
	ExpressionPredicatePlacement   = engine.ExpressionPredicatePlacement
	ExpressionPredicate            = engine.ExpressionPredicate
	FactVersion                    = engine.FactVersion
	Recency                        = engine.Recency
	Generation                     = engine.Generation
	FactSnapshot                   = engine.FactSnapshot
	RulesetID                      = engine.RulesetID
	ModuleName                     = engine.ModuleName
	SessionID                      = engine.SessionID
	RunID                          = engine.RunID
	SourceSpan                     = engine.SourceSpan
	RuleID                         = engine.RuleID
	RuleRevisionID                 = engine.RuleRevisionID
	ActivationID                   = engine.ActivationID
	SupportID                      = engine.SupportID
	ConditionID                    = engine.ConditionID
	TemplateKey                    = engine.TemplateKey
	FactID                         = engine.FactID
	FieldRef                       = engine.FieldRef
	JoinConstraintSpec             = engine.JoinConstraintSpec
	JoinConstraint                 = engine.JoinConstraint
	ListPatternElementKind         = engine.ListPatternElementKind
	ListPatternElementSpec         = engine.ListPatternElementSpec
	ListPatternSpec                = engine.ListPatternSpec
	ListPatternElement             = engine.ListPatternElement
	RuleListPattern                = engine.RuleListPattern
	QualifiedName                  = engine.QualifiedName
	NameRef                        = engine.NameRef
	ModuleSpec                     = engine.ModuleSpec
	Module                         = engine.Module
	PathSegmentKind                = engine.PathSegmentKind
	PathSegment                    = engine.PathSegment
	PathSpec                       = engine.PathSpec
	FieldConstraintOperator        = engine.FieldConstraintOperator
	FieldConstraintSpec            = engine.FieldConstraintSpec
	RuleFieldConstraintSpec        = engine.RuleFieldConstraintSpec
	FieldConstraint                = engine.FieldConstraint
	RuleFieldConstraint            = engine.RuleFieldConstraint
	PureFunction                   = engine.PureFunction
	PureFunction0                  = engine.PureFunction0
	PureFunction1                  = engine.PureFunction1
	PureFunction2                  = engine.PureFunction2
	PureFunction3                  = engine.PureFunction3
	PureFunctionSpec               = engine.PureFunctionSpec
	ExpressionFunctionParamSpec    = engine.ExpressionFunctionParamSpec
	ExpressionFunctionSpec         = engine.ExpressionFunctionSpec
	PureFunctionDefinition         = engine.PureFunctionDefinition
	GlobalSpec                     = engine.GlobalSpec
	Global                         = engine.Global
	FunctionEvaluationError        = engine.FunctionEvaluationError
	QueryParameterSpec             = engine.QueryParameterSpec
	QueryReturnSpec                = engine.QueryReturnSpec
	QuerySpec                      = engine.QuerySpec
	QueryParameter                 = engine.QueryParameter
	QueryReturn                    = engine.QueryReturn
	Query                          = engine.Query
	RuleConditionSpec              = engine.RuleConditionSpec
	FactTargetKind                 = engine.FactTargetKind
	FactTarget                     = engine.FactTarget
	ConditionSpec                  = engine.ConditionSpec
	And                            = engine.And
	Or                             = engine.Or
	Not                            = engine.Not
	Explicit                       = engine.Explicit
	ExistsCondition                = engine.ExistsCondition
	ForallCondition                = engine.ForallCondition
	Test                           = engine.Test
	Match                          = engine.Match
	RuleActionSpec                 = engine.RuleActionSpec
	RuleSpec                       = engine.RuleSpec
	RuleCondition                  = engine.RuleCondition
	RuleConditionBranch            = engine.RuleConditionBranch
	RuleConditionBranchCondition   = engine.RuleConditionBranchCondition
	ConditionTreeKind              = engine.ConditionTreeKind
	RuleConditionTree              = engine.RuleConditionTree
	RuleAction                     = engine.RuleAction
	Rule                           = engine.Rule
	Workspace                      = engine.Workspace
	Ruleset                        = engine.Ruleset
	DuplicatePolicy                = engine.DuplicatePolicy
	FieldPresence                  = engine.FieldPresence
	FieldSpec                      = engine.FieldSpec
	TemplateSpec                   = engine.TemplateSpec
	Template                       = engine.Template
	ValueKind                      = engine.ValueKind
	Value                          = engine.Value
	Fields                         = engine.Fields
)

const (
	AggregateCount                           = engine.AggregateCount
	AggregateSum                             = engine.AggregateSum
	AggregateMin                             = engine.AggregateMin
	AggregateMax                             = engine.AggregateMax
	AggregateCollect                         = engine.AggregateCollect
	ExpressionCompareUnknown                 = engine.ExpressionCompareUnknown
	ExpressionCompareEqual                   = engine.ExpressionCompareEqual
	ExpressionCompareNotEqual                = engine.ExpressionCompareNotEqual
	ExpressionCompareLessThan                = engine.ExpressionCompareLessThan
	ExpressionCompareLessOrEqual             = engine.ExpressionCompareLessOrEqual
	ExpressionCompareGreaterThan             = engine.ExpressionCompareGreaterThan
	ExpressionCompareGreaterOrEqual          = engine.ExpressionCompareGreaterOrEqual
	ExpressionBoolUnknown                    = engine.ExpressionBoolUnknown
	ExpressionBoolAnd                        = engine.ExpressionBoolAnd
	ExpressionBoolOr                         = engine.ExpressionBoolOr
	ExpressionBoolNot                        = engine.ExpressionBoolNot
	ExpressionPredicatePlacementUnknown      = engine.ExpressionPredicatePlacementUnknown
	ExpressionPredicatePlacementAlpha        = engine.ExpressionPredicatePlacementAlpha
	ExpressionPredicatePlacementBetaResidual = engine.ExpressionPredicatePlacementBetaResidual
	ExpressionPredicatePlacementUnsupported  = engine.ExpressionPredicatePlacementUnsupported
	MainModule                               = engine.MainModule
	ListPatternElementUnknown                = engine.ListPatternElementUnknown
	ListPatternElementValue                  = engine.ListPatternElementValue
	ListPatternElementWildcard               = engine.ListPatternElementWildcard
	ListPatternElementSegment                = engine.ListPatternElementSegment
	ListPatternElementRestWildcard           = engine.ListPatternElementRestWildcard
	PathSegmentRoot                          = engine.PathSegmentRoot
	PathSegmentMap                           = engine.PathSegmentMap
	PathSegmentIndex                         = engine.PathSegmentIndex
	FieldConstraintOpUnknown                 = engine.FieldConstraintOpUnknown
	FieldConstraintOpExists                  = engine.FieldConstraintOpExists
	FieldConstraintOpEqual                   = engine.FieldConstraintOpEqual
	FieldConstraintOpNotEqual                = engine.FieldConstraintOpNotEqual
	FieldConstraintOpLessThan                = engine.FieldConstraintOpLessThan
	FieldConstraintOpLessOrEqual             = engine.FieldConstraintOpLessOrEqual
	FieldConstraintOpGreaterThan             = engine.FieldConstraintOpGreaterThan
	FieldConstraintOpGreaterOrEqual          = engine.FieldConstraintOpGreaterOrEqual
	FieldConstraintExists                    = engine.FieldConstraintExists
	FieldConstraintEqual                     = engine.FieldConstraintEqual
	FieldConstraintNotEqual                  = engine.FieldConstraintNotEqual
	FieldConstraintLessThan                  = engine.FieldConstraintLessThan
	FieldConstraintLessOrEqual               = engine.FieldConstraintLessOrEqual
	FieldConstraintGreaterThan               = engine.FieldConstraintGreaterThan
	FieldConstraintGreaterOrEqual            = engine.FieldConstraintGreaterOrEqual
	FactTargetUnknown                        = engine.FactTargetUnknown
	FactTargetDynamic                        = engine.FactTargetDynamic
	FactTargetTemplate                       = engine.FactTargetTemplate
	FactTargetTemplateKey                    = engine.FactTargetTemplateKey
	ConditionTreeKindUnknown                 = engine.ConditionTreeKindUnknown
	ConditionTreeKindAnd                     = engine.ConditionTreeKindAnd
	ConditionTreeKindMatch                   = engine.ConditionTreeKindMatch
	ConditionTreeKindTest                    = engine.ConditionTreeKindTest
	ConditionTreeKindNot                     = engine.ConditionTreeKindNot
	ConditionTreeKindOr                      = engine.ConditionTreeKindOr
	ConditionTreeKindExists                  = engine.ConditionTreeKindExists
	ConditionTreeKindForall                  = engine.ConditionTreeKindForall
	ConditionTreeKindAccumulate              = engine.ConditionTreeKindAccumulate
	DuplicateStructural                      = engine.DuplicateStructural
	DuplicateAllow                           = engine.DuplicateAllow
	DuplicateUniqueKey                       = engine.DuplicateUniqueKey
	FieldPresenceOmitted                     = engine.FieldPresenceOmitted
	FieldPresenceDefault                     = engine.FieldPresenceDefault
	FieldPresenceExplicit                    = engine.FieldPresenceExplicit
	ValueAny                                 = engine.ValueAny
	ValueNull                                = engine.ValueNull
	ValueBool                                = engine.ValueBool
	ValueInt                                 = engine.ValueInt
	ValueFloat                               = engine.ValueFloat
	ValueString                              = engine.ValueString
	ValueList                                = engine.ValueList
	ValueMap                                 = engine.ValueMap
)

var (
	ErrInvalidRuleset              = engine.ErrInvalidRuleset
	ErrIncompatibleRuleset         = engine.ErrIncompatibleRuleset
	ErrActionFailed                = engine.ErrActionFailed
	ErrValidation                  = engine.ErrValidation
	ErrDuplicateFact               = engine.ErrDuplicateFact
	ErrMatcher                     = engine.ErrMatcher
	ErrUnsupportedRuntime          = engine.ErrUnsupportedRuntime
	ErrInvalidPath                 = engine.ErrInvalidPath
	ErrInvalidListPattern          = engine.ErrInvalidListPattern
	ErrInvalidHigherOrderCondition = engine.ErrInvalidHigherOrderCondition
	ErrAggregateValidation         = engine.ErrAggregateValidation
	ErrAggregateEvaluation         = engine.ErrAggregateEvaluation
	ErrFunctionValidation          = engine.ErrFunctionValidation
	ErrFunctionEvaluation          = engine.ErrFunctionEvaluation
	ErrQueryValidation             = engine.ErrQueryValidation
	ErrUnsupportedValue            = engine.ErrUnsupportedValue
)

func Count() AggregateSpec {
	return engine.Count()
}

func Sum(expression ExpressionSpec) AggregateSpec {
	return engine.Sum(expression)
}

func Min(expression ExpressionSpec) AggregateSpec {
	return engine.Min(expression)
}

func Max(expression ExpressionSpec) AggregateSpec {
	return engine.Max(expression)
}

func Collect(expression ExpressionSpec) AggregateSpec {
	return engine.Collect(expression)
}

func Accumulate(input ConditionSpec, specs ...AggregateSpec) AccumulateCondition {
	return engine.Accumulate(input, specs...)
}

func CurrentPath(path PathSpec) CurrentFieldExpr {
	return engine.CurrentPath(path)
}

func BindingPath(binding string, path PathSpec) BindingFieldExpr {
	return engine.BindingPath(binding, path)
}

func GlobalValue(name string) GlobalExpr {
	return engine.GlobalExpr{Name: name}
}

func HasPath(path PathSpec) HasPathExpr {
	return engine.HasPath(path)
}

func Call(name string, args ...ExpressionSpec) CallExpr {
	return engine.Call(name, args...)
}

func ListPattern(path PathSpec, elements ...ListPatternElementSpec) ListPatternSpec {
	return engine.ListPattern(path, elements...)
}

func ListElem(expression ExpressionSpec) ListPatternElementSpec {
	return engine.ListElem(expression)
}

func ListWildcard() ListPatternElementSpec {
	return engine.ListWildcard()
}

func ListSegment(binding string) ListPatternElementSpec {
	return engine.ListSegment(binding)
}

func ListRestWildcard() ListPatternElementSpec {
	return engine.ListRestWildcard()
}

func Ref(name string) NameRef {
	return engine.Ref(name)
}

func ModuleRef(module ModuleName, name string) NameRef {
	return engine.ModuleRef(module, name)
}

func Path(root string, segments ...PathSegment) PathSpec {
	return engine.Path(root, segments...)
}

func MapKey(key string) PathSegment {
	return engine.MapKey(key)
}

func ListIndex(index int) PathSegment {
	return engine.ListIndex(index)
}

func ReturnFact(alias, binding string) QueryReturnSpec {
	return engine.ReturnFact(alias, binding)
}

func ReturnValue(alias string, expression ExpressionSpec) QueryReturnSpec {
	return engine.ReturnValue(alias, expression)
}

func DynamicFact(name string) FactTarget {
	return engine.DynamicFact(name)
}

func DynamicFactIn(module ModuleName, name string) FactTarget {
	return engine.DynamicFactIn(module, name)
}

func TemplateFact(name string) FactTarget {
	return engine.TemplateFact(name)
}

func TemplateFactIn(module ModuleName, name string) FactTarget {
	return engine.TemplateFactIn(module, name)
}

func TemplateKeyFact(key TemplateKey) FactTarget {
	return engine.TemplateKeyFact(key)
}

func Exists(condition ConditionSpec) ExistsCondition {
	return engine.Exists(condition)
}

func Forall(domain ConditionSpec, requirement ConditionSpec) ForallCondition {
	return engine.Forall(domain, requirement)
}

func NewWorkspace() *Workspace {
	return engine.NewWorkspace()
}

func Compile(ctx context.Context, workspace *Workspace) (*Ruleset, error) {
	return workspace.Compile(ctx)
}

func NullValue() Value {
	return engine.NullValue()
}

func NewValue(raw any) (Value, error) {
	return engine.NewValue(raw)
}

func NewFields(raw map[string]any) (Fields, error) {
	return engine.NewFields(raw)
}

func NewFieldsFromPairs(pairs ...any) (Fields, error) {
	return engine.NewFieldsFromPairs(pairs...)
}

func MustFields(pairs ...any) Fields {
	return engine.MustFields(pairs...)
}
