package rules

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
)

type (
	// ActionFunc is the Go implementation of a rule or query action,
	// supplied as [ActionSpec].Fn.
	ActionFunc = engine.ActionFunc
	// ActionContext is passed to an [ActionFunc]. It exposes the
	// activation's identity and generation, read access to bound facts
	// and values, and the mutation API (Assert, AssertLogical,
	// AssertTemplateValues, Modify, Retract, Halt).
	ActionContext = engine.ActionContext
	// ActionSpec names one action implementation registered on a
	// [Workspace]. Exactly one of Fn or AssertTemplateValues must be set.
	ActionSpec = engine.ActionSpec
	// ActionBindingReadSetSpec declares which bindings an action
	// function reads, letting the runtime skip materializing the rest.
	ActionBindingReadSetSpec = engine.ActionBindingReadSetSpec
	// ActionBindingReadSpec declares one binding an action reads, and
	// optionally a specific field or path within it.
	ActionBindingReadSpec = engine.ActionBindingReadSpec
	// AssertTemplateValuesActionSpec describes a generated assert action
	// that emits a template's field values in declaration order. When
	// the compiler proves the target template isn't visible to
	// matching, queries, or mutation, the emitted fact is output-only:
	// validated and defaulted, then discarded unless consumed.
	AssertTemplateValuesActionSpec = engine.AssertTemplateValuesActionSpec
	// ActionEffectSpec is a declarative, expression-backed rule action for
	// the .gess mutation verbs (assert/modify/retract/emit/bind). Its
	// Values are [ExpressionSpec]s compiled once and evaluated against the
	// firing's frozen bindings, so function-call action values need no host
	// closure and survive Go code generation.
	ActionEffectSpec = engine.ActionEffectSpec
	// ActionEffectKind selects the mutation an [ActionEffectSpec] performs.
	ActionEffectKind = engine.ActionEffectKind
	// ActionCallSpec is a host-function call action: a registered call plus
	// expression-backed arguments.
	ActionCallSpec = engine.ActionCallSpec
	// Action is a compiled, inspectable action reference on a rule or
	// query.
	Action = engine.Action
	// AggregateKind identifies which aggregate an [AggregateSpec]
	// computes: count, sum, min, max, or collect.
	AggregateKind = engine.AggregateKind
	// AggregateSpec is one aggregate computed over the facts matching an
	// [AccumulateCondition]'s input, built with [Count], [Sum], [Min], [Max], or
	// [Collect] and bound to a result name with As.
	AggregateSpec = engine.AggregateSpec
	// AccumulateCondition maintains one or more [AggregateSpec] results
	// incrementally over the facts matching Input, built with
	// [Accumulate].
	AccumulateCondition = engine.AccumulateCondition
	// ValidationError reports a definition that failed structural or
	// compile-time validation, identifying the template, rule, field,
	// condition, constraint, predicate, join, or action at fault.
	ValidationError = engine.ValidationError
	// ExpressionSpec is a node in a predicate, test, aggregate-input, or
	// query-return expression tree. Implementations are [ConstExpr],
	// [CurrentFieldExpr], [BindingFieldExpr], [HasPathExpr], [BindingValueExpr],
	// [ParamExpr], [CallExpr], [CompareExpr], and [BooleanExpr].
	ExpressionSpec = engine.ExpressionSpec
	// ExpressionComparisonOperator is the operator in a [CompareExpr].
	ExpressionComparisonOperator = engine.ExpressionComparisonOperator
	// ExpressionBooleanOperator is the operator in a [BooleanExpr].
	ExpressionBooleanOperator = engine.ExpressionBooleanOperator
	// ConstExpr is a literal value in an expression tree.
	ConstExpr = engine.ConstExpr
	// CurrentFieldExpr references a field, or nested path, of the fact
	// matched by the current condition. Set exactly one of Field or
	// Path. Not valid in query returns.
	CurrentFieldExpr = engine.CurrentFieldExpr
	// BindingFieldExpr references a field, or nested path, of an earlier
	// condition's binding. Set exactly one of Field or Path; Binding
	// must name an earlier condition.
	BindingFieldExpr = engine.BindingFieldExpr
	// HasPathExpr evaluates to true when Path resolves to a present
	// value on the current fact, for testing optional nested structure.
	HasPathExpr = engine.HasPathExpr
	// BindingValueExpr references a value binding, such as an
	// aggregate's As result, from an earlier condition.
	BindingValueExpr = engine.BindingValueExpr
	// ParamExpr references a named query parameter. Valid only inside
	// query predicates and query returns.
	ParamExpr = engine.ParamExpr
	// GlobalExpr references a declared global by name, built with
	// [GlobalValue]. Valid in any condition, test, aggregate-input,
	// query, or action-argument expression.
	GlobalExpr = engine.GlobalExpr
	// RHSBindExpr references a right-hand-side local created by a bind
	// action earlier in the same rule firing. Valid only in action
	// values, never on a left-hand side.
	RHSBindExpr = engine.RHSBindExpr
	// CallExpr invokes a registered pure function by name with Args.
	CallExpr = engine.CallExpr
	// CompareExpr compares Left and Right with Operator; both operands
	// are required and must be type-comparable for the operator.
	CompareExpr = engine.CompareExpr
	// BooleanExpr combines Operands with Operator (and, or, not); not
	// requires exactly one operand, and and or require at least one.
	BooleanExpr = engine.BooleanExpr
	// ExpressionPredicatePlacement reports where the compiler placed a
	// predicate: alpha (evaluated once per fact, pre-join) or
	// beta-residual (evaluated post-join, once per joined pair).
	// Placement is determined automatically at compile time.
	ExpressionPredicatePlacement = engine.ExpressionPredicatePlacement
	// ExpressionPredicate wraps a compiled predicate for inspection: its
	// source expression, its placement, and its declaration order.
	ExpressionPredicate = engine.ExpressionPredicate
	// FactVersion advances each time a fact is modified; a fact's [FactID]
	// stays stable across modifies within a generation.
	FactVersion = engine.FactVersion
	// Recency is a session-wide counter that advances on every fact
	// mutation, used to order which pending activation fires first.
	Recency = engine.Recency
	// Generation is the working-memory reset epoch. FactIDs embed a
	// generation, so IDs from before a Reset are stale afterward.
	Generation = engine.Generation
	// FactSnapshot is an immutable, detached view of one fact's
	// identity, fields, and support state.
	FactSnapshot = engine.FactSnapshot
	// RulesetID identifies one compiled [Ruleset] revision.
	RulesetID = engine.RulesetID
	// ModuleName identifies a module. The zero value renders as MAIN,
	// the default module.
	ModuleName = engine.ModuleName
	// SessionID identifies a session.
	SessionID = engine.SessionID
	// RunID identifies one session Run call.
	RunID = engine.RunID
	// SourceSpan is a source location range within a .gess file, carried
	// into compiled definitions and runtime errors for rulesets loaded
	// from .gess source.
	SourceSpan = engine.SourceSpan
	// RuleID is a rule's stable identity, surviving ReplaceRule.
	RuleID = engine.RuleID
	// RuleRevisionID identifies one compiled revision of a rule; it
	// changes whenever the rule's definition is recompiled, even if its
	// [RuleID] stays the same.
	RuleRevisionID = engine.RuleRevisionID
	// ActivationID identifies one activation: a rule paired with the
	// specific facts that matched it.
	ActivationID = engine.ActivationID
	// SupportID identifies one logical support edge.
	SupportID = engine.SupportID
	// ConditionID is a compiled rule or query condition's stable,
	// content-addressed identity: semantically identical conditions
	// across compiles get the same ID.
	ConditionID = engine.ConditionID
	// TemplateKey identifies a compiled template within a ruleset. It
	// defaults to the template's name unless [TemplateSpec].Key is set.
	TemplateKey = engine.TemplateKey
	// FactID identifies a fact, stable across modifies within a
	// generation. IDs from an earlier generation are stale after Reset.
	FactID = engine.FactID
	// FactPatch describes a modify: Set overwrites the named fields and
	// Unset clears the named fields, restoring their template defaults.
	FactPatch = engine.FactPatch
	// FieldRef names a field, or nested path, of an earlier binding,
	// used as the right-hand side of a [JoinConstraintSpec].
	FieldRef = engine.FieldRef
	// JoinConstraintSpec compares a field of the matched fact against a
	// field of an earlier binding named by Ref. Equality joins compile
	// to indexed hash joins.
	JoinConstraintSpec = engine.JoinConstraintSpec
	// JoinConstraint is the compiled, inspectable form of a
	// [JoinConstraintSpec].
	JoinConstraint = engine.JoinConstraint
	// ListPatternElementKind identifies one element of a [ListPatternSpec]:
	// a fixed value, a wildcard, a captured variable-length segment, or
	// an uncaptured variable-length rest wildcard.
	ListPatternElementKind = engine.ListPatternElementKind
	// ListPatternElementSpec is one element of a [ListPatternSpec], built
	// with [ListElem], [ListWildcard], [ListSegment], or [ListRestWildcard].
	ListPatternElementSpec = engine.ListPatternElementSpec
	// ListPatternSpec is a structural pattern over a LIST-typed field,
	// matched positionally with at most one variable-length element.
	ListPatternSpec = engine.ListPatternSpec
	// ListPatternElement is the compiled, inspectable form of a
	// [ListPatternElementSpec].
	ListPatternElement = engine.ListPatternElement
	// RuleListPattern is the compiled, inspectable list pattern attached
	// to a [RuleCondition].
	RuleListPattern = engine.RuleListPattern
	// QualifiedName is a module-qualified name, rendered as
	// "MODULE.Name".
	QualifiedName = engine.QualifiedName
	// NameRef is an unresolved reference to a named definition, as
	// authored with [Ref] or [ModuleRef]. An empty Module resolves against
	// the referencing definition's own module.
	NameRef = engine.NameRef
	// ModuleSpec declares a module: its Name and optional default
	// auto-focus behavior. Redeclaring a module with identical fields is
	// idempotent; conflicting redeclaration is a validation error.
	ModuleSpec = engine.ModuleSpec
	// Module is the compiled, inspectable form of a [ModuleSpec].
	Module = engine.Module
	// PathSegmentKind identifies one step of a [PathSpec]: the root field,
	// a map-key step, or a list-index step.
	PathSegmentKind = engine.PathSegmentKind
	// PathSegment is one step of a [PathSpec], built with [MapKey] or
	// [ListIndex]; the root segment is synthesized by [Path].
	PathSegment = engine.PathSegment
	// PathSpec addresses a field, or a nested value within a LIST or MAP
	// field, built with [Path]. The root must name a declared field, and
	// only MAP fields may be followed by a map segment, only LIST fields
	// by an index segment.
	PathSpec = engine.PathSpec
	// FieldConstraintOperator is the comparison a [FieldConstraintSpec]
	// applies: exists, equal, not-equal, or an ordering comparison.
	FieldConstraintOperator = engine.FieldConstraintOperator
	// FieldConstraintSpec constrains one field, or nested path, of a
	// matched fact to Operator against Value. Value must be nil for the
	// exists operator.
	FieldConstraintSpec = engine.FieldConstraintSpec
	// RuleFieldConstraintSpec is an alias of [FieldConstraintSpec], used in
	// rule-condition contexts.
	RuleFieldConstraintSpec = engine.RuleFieldConstraintSpec
	// FieldConstraint is the compiled, inspectable form of a
	// [FieldConstraintSpec].
	FieldConstraint = engine.FieldConstraint
	// RuleFieldConstraint is an alias of [FieldConstraint].
	RuleFieldConstraint = engine.RuleFieldConstraint
	// PureFunction is a deterministic, side-effect-free variadic
	// function implementation available to condition and query
	// expressions.
	PureFunction = engine.PureFunction
	// PureFunction0 is a fixed-arity [PureFunction] taking no arguments.
	PureFunction0 = engine.PureFunction0
	// PureFunction1 is a fixed-arity [PureFunction] taking one argument.
	PureFunction1 = engine.PureFunction1
	// PureFunction2 is a fixed-arity [PureFunction] taking two arguments.
	PureFunction2 = engine.PureFunction2
	// PureFunction3 is a fixed-arity [PureFunction] taking three
	// arguments.
	PureFunction3 = engine.PureFunction3
	// PureFunctionSpec registers a pure function by Name, its Args and
	// Return kinds, and exactly one of Func (variadic) or the
	// fixed-arity Func0 through Func3 matching len(Args).
	PureFunctionSpec = engine.PureFunctionSpec
	// ExpressionFunctionParamSpec declares one named, typed parameter of
	// an [ExpressionFunctionSpec].
	ExpressionFunctionParamSpec = engine.ExpressionFunctionParamSpec
	// ExpressionFunctionSpec defines a pure function whose body is an
	// expression tree over its parameters, as authored with deffunction;
	// it needs no Go implementation.
	ExpressionFunctionSpec = engine.ExpressionFunctionSpec
	// PureFunctionDefinition is the compiled, inspectable form of a
	// [PureFunctionSpec].
	PureFunctionDefinition = engine.PureFunctionDefinition
	// GlobalSpec declares a named, typed global with a default value,
	// readable in expressions with [GlobalValue] and overridable per
	// session.
	GlobalSpec = engine.GlobalSpec
	// Global is the compiled, inspectable form of a [GlobalSpec].
	Global = engine.Global
	// FunctionEvaluationError reports a pure function that panicked,
	// returned an error, or returned a value of the wrong kind, wrapping
	// [ErrFunctionEvaluation].
	FunctionEvaluationError = engine.FunctionEvaluationError
	// QueryParameterSpec declares one named, typed query parameter,
	// referenced in the query with [ParamExpr].
	QueryParameterSpec = engine.QueryParameterSpec
	// QueryReturnSpec declares one named query result column: either a
	// whole matched fact ([ReturnFact]) or a computed value ([ReturnValue]).
	// Its expression can't use [CurrentFieldExpr] or [HasPathExpr], since a
	// query return isn't scoped to one current condition.
	QueryReturnSpec = engine.QueryReturnSpec
	// QuerySpec defines a named query: its parameters, its conditions
	// (flat or as a tree, matching [RuleSpec]), and its returns.
	QuerySpec = engine.QuerySpec
	// QueryParameter is the compiled, inspectable form of a
	// [QueryParameterSpec].
	QueryParameter = engine.QueryParameter
	// QueryReturn is the compiled, inspectable form of a
	// [QueryReturnSpec].
	QueryReturn = engine.QueryReturn
	// Query is a compiled, inspectable query, executed at runtime with
	// session.Query or session.QueryAll.
	Query = engine.Query
	// RuleConditionSpec is the field set behind [Match]: a binding, a
	// [FactTarget], field constraints, list patterns, join constraints,
	// and predicates.
	RuleConditionSpec = engine.RuleConditionSpec
	// FactTargetKind identifies what kind of fact a [FactTarget] matches:
	// dynamic (untemplated), a named template, or a template addressed
	// directly by TemplateKey.
	FactTargetKind = engine.FactTargetKind
	// FactTarget names the facts a condition matches, built with
	// [TemplateFact], [TemplateFactIn], or [TemplateKeyFact].
	FactTarget = engine.FactTarget
	// ConditionSpec is a node in a rule or query's left-hand-side tree.
	// Implementations are [And], [Or], [Not], [Explicit], [ExistsCondition],
	// [ForallCondition], [Test], [Match], and [AccumulateCondition].
	ConditionSpec = engine.ConditionSpec
	// And is a conjunction of Conditions; nested And nodes flatten at
	// compile time.
	And = engine.And
	// Or is a disjunction of Conditions. Bindings introduced in one
	// branch are local to that branch; every branch must expose the
	// same set of bindings to later conditions and actions. Or cannot
	// appear directly under [Not].
	Or = engine.Or
	// Not negates Condition, matched by maintaining a per-row blocker
	// count. Bindings inside are local and not visible outside. Exists,
	// Forall, Test, Accumulate, and Or conditions cannot appear directly
	// under Not.
	Not = engine.Not
	// Explicit marks a positive match ineligible for backward-chaining
	// demand generation, without changing its ordinary match behavior.
	Explicit = engine.Explicit
	// ExistsCondition tests whether at least one tuple matching
	// Condition exists, with bindings local to it. Condition must be a
	// positive conjunction of [Match] (optionally [Explicit]). Cannot appear
	// under [Not] or as an [Or] branch.
	ExistsCondition = engine.ExistsCondition
	// ForallCondition tests whether every tuple matching Domain also
	// satisfies Requirement, vacuously true for an empty domain. Domain
	// must be a positive match conjunction; Requirement reduces to at
	// most one positive [Match] plus any number of [Test] expressions.
	// Cannot appear under [Not] or as an [Or] branch.
	ForallCondition = engine.ForallCondition
	// Test evaluates a standalone boolean Expression over earlier local
	// bindings. Cannot appear directly under [Not].
	Test = engine.Test
	// Match is a positive fact match condition: it targets facts through
	// Target, applies FieldConstraints, ListPatterns, JoinConstraints,
	// and Predicates, and binds the result to Binding.
	Match = engine.Match
	// RuleActionSpec references, by Name, an action registered on the
	// workspace.
	RuleActionSpec = engine.RuleActionSpec
	// RuleSpec defines a rule: its identity, module, salience, optional
	// auto-focus, its conditions (flat Conditions or a structured
	// ConditionTree, not both), and its Actions.
	RuleSpec = engine.RuleSpec
	// RuleCondition is the compiled, inspectable form of a matched
	// condition, exposing its target, constraints, predicates, and
	// binding.
	RuleCondition = engine.RuleCondition
	// RuleConditionBranch is one compiled branch of a condition tree.
	// Flat rules and non-disjunctive trees expose one branch; trees with
	// Or expose one branch per expanded alternative.
	RuleConditionBranch = engine.RuleConditionBranch
	// RuleConditionBranchCondition describes one condition within an
	// expanded branch: its authored tree path, and whether its binding
	// is visible to actions (negated conditions are local, not visible).
	RuleConditionBranchCondition = engine.RuleConditionBranchCondition
	// ConditionTreeKind identifies the shape of a [RuleConditionTree]
	// node.
	ConditionTreeKind = engine.ConditionTreeKind
	// RuleConditionTree is the compiled, inspectable condition tree
	// mirroring a rule or query's authored ConditionSpec shape.
	RuleConditionTree = engine.RuleConditionTree
	// RuleAction is the compiled, inspectable form of a [RuleActionSpec].
	RuleAction = engine.RuleAction
	// Rule is a compiled, inspectable rule.
	Rule = engine.Rule
	// Workspace is a mutable collection of module, template, action,
	// function, rule, and query definitions. Definitions are validated
	// as they're added; [Compile] produces an immutable [Ruleset].
	Workspace = engine.Workspace
	// Ruleset is the immutable, compiled result of [Workspace].Compile,
	// including the compiled Rete graph. Rulesets are safe to share
	// across sessions.
	Ruleset = engine.Ruleset
	// DuplicatePolicy controls how a template deduplicates facts:
	// structural (the Go zero value), allow, or unique-key.
	DuplicatePolicy = engine.DuplicatePolicy
	// FieldPresence reports how a field on a matched or snapshotted fact
	// came to have its value: omitted, defaulted, or explicitly
	// supplied.
	FieldPresence = engine.FieldPresence
	// FieldSpec declares one template field: its name, kind, whether
	// it's required, an optional default, and an optional closed set of
	// allowed values.
	FieldSpec = engine.FieldSpec
	// TemplateSpec declares a fact template: its name, module, key,
	// fields, duplicate policy, and whether it's backward-chaining
	// reactive.
	TemplateSpec = engine.TemplateSpec
	// Template is the compiled, inspectable form of a [TemplateSpec].
	Template = engine.Template
	// ValueKind is a value's type tag: null, bool, int, float, string,
	// list, or map, plus any as a wildcard accepted in field, parameter,
	// and function declarations.
	ValueKind = engine.ValueKind
	// Value is an immutable, typed field or expression value.
	Value = engine.Value
	// Fields is a fact's field values, keyed by field name.
	Fields = engine.Fields
)

const (
	// AggregateCount, AggregateSum, AggregateMin, AggregateMax, and
	// AggregateCollect identify which aggregate an [AggregateSpec]
	// computes.
	AggregateCount   = engine.AggregateCount
	AggregateSum     = engine.AggregateSum
	AggregateMin     = engine.AggregateMin
	AggregateMax     = engine.AggregateMax
	AggregateCollect = engine.AggregateCollect
	// ExpressionCompareEqual, ExpressionCompareNotEqual, and the
	// ordering variants are the operators a [CompareExpr] applies.
	// Equality requires matching kinds or both-numeric operands;
	// ordering requires both-numeric or both-string operands.
	// ExpressionCompareUnknown is the unset zero value.
	ExpressionCompareUnknown        = engine.ExpressionCompareUnknown
	ExpressionCompareEqual          = engine.ExpressionCompareEqual
	ExpressionCompareNotEqual       = engine.ExpressionCompareNotEqual
	ExpressionCompareLessThan       = engine.ExpressionCompareLessThan
	ExpressionCompareLessOrEqual    = engine.ExpressionCompareLessOrEqual
	ExpressionCompareGreaterThan    = engine.ExpressionCompareGreaterThan
	ExpressionCompareGreaterOrEqual = engine.ExpressionCompareGreaterOrEqual
	// ExpressionBoolAnd, ExpressionBoolOr, and ExpressionBoolNot are the
	// operators a [BooleanExpr] applies. ExpressionBoolUnknown is the
	// unset zero value.
	ExpressionBoolUnknown = engine.ExpressionBoolUnknown
	ExpressionBoolAnd     = engine.ExpressionBoolAnd
	ExpressionBoolOr      = engine.ExpressionBoolOr
	ExpressionBoolNot     = engine.ExpressionBoolNot
	// ExpressionPredicatePlacementAlpha and
	// ExpressionPredicatePlacementBetaResidual are the placements the
	// compiler assigns a predicate; ExpressionPredicatePlacementUnknown
	// is the unset zero value and ExpressionPredicatePlacementUnsupported
	// marks a predicate the graph runtime can't place.
	ExpressionPredicatePlacementUnknown      = engine.ExpressionPredicatePlacementUnknown
	ExpressionPredicatePlacementAlpha        = engine.ExpressionPredicatePlacementAlpha
	ExpressionPredicatePlacementBetaResidual = engine.ExpressionPredicatePlacementBetaResidual
	ExpressionPredicatePlacementUnsupported  = engine.ExpressionPredicatePlacementUnsupported
	// MainModule is the default module, used when a definition doesn't
	// declare one.
	MainModule = engine.MainModule
	// ListPatternElementValue, ListPatternElementWildcard,
	// ListPatternElementSegment, and ListPatternElementRestWildcard are
	// the element kinds a [ListPatternElementSpec] can take.
	// ListPatternElementUnknown is the unset zero value.
	ListPatternElementUnknown      = engine.ListPatternElementUnknown
	ListPatternElementValue        = engine.ListPatternElementValue
	ListPatternElementWildcard     = engine.ListPatternElementWildcard
	ListPatternElementSegment      = engine.ListPatternElementSegment
	ListPatternElementRestWildcard = engine.ListPatternElementRestWildcard
	// PathSegmentRoot, PathSegmentMap, and PathSegmentIndex are the
	// segment kinds a [PathSpec]'s steps can take.
	PathSegmentRoot  = engine.PathSegmentRoot
	PathSegmentMap   = engine.PathSegmentMap
	PathSegmentIndex = engine.PathSegmentIndex
	// FieldConstraintOpExists, FieldConstraintOpEqual, and the remaining
	// comparison operators are the operators a [FieldConstraintSpec]
	// applies. FieldConstraintOpUnknown is the unset zero value.
	FieldConstraintOpUnknown        = engine.FieldConstraintOpUnknown
	FieldConstraintOpExists         = engine.FieldConstraintOpExists
	FieldConstraintOpEqual          = engine.FieldConstraintOpEqual
	FieldConstraintOpNotEqual       = engine.FieldConstraintOpNotEqual
	FieldConstraintOpLessThan       = engine.FieldConstraintOpLessThan
	FieldConstraintOpLessOrEqual    = engine.FieldConstraintOpLessOrEqual
	FieldConstraintOpGreaterThan    = engine.FieldConstraintOpGreaterThan
	FieldConstraintOpGreaterOrEqual = engine.FieldConstraintOpGreaterOrEqual
	// FieldConstraintExists, FieldConstraintEqual, and the remaining
	// comparison operators are aliases of the FieldConstraintOp*
	// constants above, named for use in rule-condition contexts.
	FieldConstraintExists         = engine.FieldConstraintExists
	FieldConstraintEqual          = engine.FieldConstraintEqual
	FieldConstraintNotEqual       = engine.FieldConstraintNotEqual
	FieldConstraintLessThan       = engine.FieldConstraintLessThan
	FieldConstraintLessOrEqual    = engine.FieldConstraintLessOrEqual
	FieldConstraintGreaterThan    = engine.FieldConstraintGreaterThan
	FieldConstraintGreaterOrEqual = engine.FieldConstraintGreaterOrEqual
	// FactTargetDynamic, FactTargetTemplate, and FactTargetTemplateKey
	// are the kinds a [FactTarget] can take. FactTargetUnknown is the
	// unset zero value.
	FactTargetUnknown     = engine.FactTargetUnknown
	FactTargetDynamic     = engine.FactTargetDynamic
	FactTargetTemplate    = engine.FactTargetTemplate
	FactTargetTemplateKey = engine.FactTargetTemplateKey
	// ConditionTreeKindAnd, ConditionTreeKindMatch, and the remaining
	// kinds identify the shape of a [RuleConditionTree] node.
	// ConditionTreeKindUnknown is the unset zero value.
	ConditionTreeKindUnknown    = engine.ConditionTreeKindUnknown
	ConditionTreeKindAnd        = engine.ConditionTreeKindAnd
	ConditionTreeKindMatch      = engine.ConditionTreeKindMatch
	ConditionTreeKindTest       = engine.ConditionTreeKindTest
	ConditionTreeKindNot        = engine.ConditionTreeKindNot
	ConditionTreeKindOr         = engine.ConditionTreeKindOr
	ConditionTreeKindExists     = engine.ConditionTreeKindExists
	ConditionTreeKindForall     = engine.ConditionTreeKindForall
	ConditionTreeKindAccumulate = engine.ConditionTreeKindAccumulate
	// DuplicateStructural deduplicates facts with identical field
	// values, and is the Go zero value for [DuplicatePolicy] (the .gess
	// loader's own default is DuplicateAllow, not this). DuplicateAllow
	// permits duplicate facts. DuplicateUniqueKey deduplicates by the
	// template's DuplicateKeyNames.
	DuplicateStructural = engine.DuplicateStructural
	DuplicateAllow      = engine.DuplicateAllow
	DuplicateUniqueKey  = engine.DuplicateUniqueKey
	// FieldPresenceOmitted, FieldPresenceDefault, and
	// FieldPresenceExplicit report whether a field was left unset with
	// no default, took its declared default, or was explicitly supplied.
	FieldPresenceOmitted  = engine.FieldPresenceOmitted
	FieldPresenceDefault  = engine.FieldPresenceDefault
	FieldPresenceExplicit = engine.FieldPresenceExplicit
	// ValueAny is a wildcard kind accepted in field, parameter, and
	// function declarations; a [Value]'s own Kind is never ValueAny.
	// ValueNull, ValueBool, ValueInt, ValueFloat, ValueString,
	// ValueList, and ValueMap are the concrete value kinds.
	ValueAny    = engine.ValueAny
	ValueNull   = engine.ValueNull
	ValueBool   = engine.ValueBool
	ValueInt    = engine.ValueInt
	ValueFloat  = engine.ValueFloat
	ValueString = engine.ValueString
	ValueList   = engine.ValueList
	ValueMap    = engine.ValueMap

	// Action effect kinds select the mutation an [ActionEffectSpec]
	// performs.
	ActionEffectAssert        = engine.ActionEffectAssert
	ActionEffectAssertLogical = engine.ActionEffectAssertLogical
	ActionEffectModify        = engine.ActionEffectModify
	ActionEffectRetract       = engine.ActionEffectRetract
	ActionEffectEmit          = engine.ActionEffectEmit
	ActionEffectBind          = engine.ActionEffectBind
	ActionEffectPushFocus     = engine.ActionEffectPushFocus
	ActionEffectPopFocus      = engine.ActionEffectPopFocus
	ActionEffectClearFocus    = engine.ActionEffectClearFocus
	ActionEffectHalt          = engine.ActionEffectHalt
)

var (
	// ErrInvalidRuleset is returned when an operation requires a
	// compiled [Ruleset] that is nil or otherwise unusable.
	ErrInvalidRuleset = engine.ErrInvalidRuleset
	// ErrIncompatibleRuleset is returned by ApplyRuleset when the target
	// ruleset is incompatible with the session's existing live facts.
	ErrIncompatibleRuleset = engine.ErrIncompatibleRuleset
	// ErrActionFailed represents a rule action that returned an error
	// during a run.
	ErrActionFailed = engine.ErrActionFailed
	// ErrValidation is the generic sentinel wrapped by a [ValidationError]
	// that doesn't set a more specific error.
	ErrValidation = engine.ErrValidation
	// ErrDuplicateFact is returned when a mutation collides with an
	// existing fact under a template's duplicate policy.
	ErrDuplicateFact = engine.ErrDuplicateFact
	// ErrMatcher marks an internal engine-consistency failure in the
	// compiled matcher, rather than a caller-input error.
	ErrMatcher = engine.ErrMatcher
	// ErrUnsupportedRuntime is returned when the compiled Rete graph
	// can't represent or execute the requested rule, query, or
	// mutation shape.
	ErrUnsupportedRuntime = engine.ErrUnsupportedRuntime
	// ErrInvalidPath is returned for a structurally invalid [PathSpec]: an
	// empty or malformed path, or a path whose segment kinds don't match
	// the target field's declared kind.
	ErrInvalidPath = engine.ErrInvalidPath
	// ErrInvalidListPattern is returned for an invalid [ListPatternSpec]:
	// a path that isn't list-typed, no elements, more than one
	// variable-length element, or an invalid segment binding.
	ErrInvalidListPattern = engine.ErrInvalidListPattern
	// ErrInvalidHigherOrderCondition is returned for an invalid Exists,
	// Forall, or [Not] shape, such as a domain or requirement that isn't a
	// supported positive conjunction, or a higher-order condition nested
	// under Not or as an Or branch where that's not allowed.
	ErrInvalidHigherOrderCondition = engine.ErrInvalidHigherOrderCondition
	// ErrAggregateValidation is returned for an invalid [AggregateSpec] or
	// [AccumulateCondition]: zero specs, a missing or duplicate result
	// binding, or a missing expression for a non-count aggregate.
	ErrAggregateValidation = engine.ErrAggregateValidation
	// ErrAggregateEvaluation is returned when aggregate evaluation fails
	// at runtime.
	ErrAggregateEvaluation = engine.ErrAggregateEvaluation
	// ErrFunctionValidation is returned for an invalid [PureFunctionSpec]
	// or [CallExpr]: a missing or invalid name, an arity or kind mismatch,
	// or an unknown function referenced by a Call.
	ErrFunctionValidation = engine.ErrFunctionValidation
	// ErrFunctionEvaluation is wrapped by [FunctionEvaluationError],
	// returned when a pure function panics, errors, or returns a value
	// of the wrong kind.
	ErrFunctionEvaluation = engine.ErrFunctionEvaluation
	// ErrQueryValidation is returned for an invalid [QuerySpec]: a missing
	// name, an invalid parameter or return declaration, no conditions,
	// or no returns.
	ErrQueryValidation = engine.ErrQueryValidation
	// ErrUnsupportedValue is returned by [NewValue], [NewFields], and
	// [NewFieldsFromPairs] for a Go value that can't convert to a [Value],
	// such as a struct, a function, NaN, or an out-of-range unsigned
	// integer.
	ErrUnsupportedValue = engine.ErrUnsupportedValue
)

// Count aggregates to the number of matching facts in the group; zero
// for an empty group.
func Count() AggregateSpec {
	return engine.Count()
}

// Sum aggregates to the sum of expression evaluated over each matching
// fact in the group.
func Sum(expression ExpressionSpec) AggregateSpec {
	return engine.Sum(expression)
}

// Min aggregates to the running minimum of expression over the group.
func Min(expression ExpressionSpec) AggregateSpec {
	return engine.Min(expression)
}

// Max aggregates to the running maximum of expression over the group.
func Max(expression ExpressionSpec) AggregateSpec {
	return engine.Max(expression)
}

// Collect aggregates expression's value from every matching fact into a
// [ValueList], in a deterministic but not insertion-preserving order.
func Collect(expression ExpressionSpec) AggregateSpec {
	return engine.Collect(expression)
}

// Accumulate builds an [AccumulateCondition] maintaining specs
// incrementally over the facts matching input. At least one spec is
// required, and each spec's As binding must be unique.
func Accumulate(input ConditionSpec, specs ...AggregateSpec) AccumulateCondition {
	return engine.Accumulate(input, specs...)
}

// CurrentPath builds a [CurrentFieldExpr] referencing the current
// condition's fact through path.
func CurrentPath(path PathSpec) CurrentFieldExpr {
	return engine.CurrentPath(path)
}

// BindingPath builds a [BindingFieldExpr] referencing an earlier
// condition's binding through path.
func BindingPath(binding string, path PathSpec) BindingFieldExpr {
	return engine.BindingPath(binding, path)
}

// GlobalValue builds a [GlobalExpr] referencing the declared global
// named name.
func GlobalValue(name string) GlobalExpr {
	return engine.GlobalExpr{Name: name}
}

// HasPath builds a [HasPathExpr] testing whether path resolves to a
// present value on the current fact.
func HasPath(path PathSpec) HasPathExpr {
	return engine.HasPath(path)
}

// Call builds a [CallExpr] invoking the registered pure function name
// with args.
func Call(name string, args ...ExpressionSpec) CallExpr {
	return engine.Call(name, args...)
}

// ListPattern builds a [ListPatternSpec] matching elements positionally
// against the LIST-typed field at path.
func ListPattern(path PathSpec, elements ...ListPatternElementSpec) ListPatternSpec {
	return engine.ListPattern(path, elements...)
}

// ListElem builds a fixed-position list pattern element that must
// equal expression's constant value.
func ListElem(expression ExpressionSpec) ListPatternElementSpec {
	return engine.ListElem(expression)
}

// ListWildcard builds a list pattern element matching any one element,
// without a binding.
func ListWildcard() ListPatternElementSpec {
	return engine.ListWildcard()
}

// ListSegment builds a list pattern element matching a variable-length
// run, captured as a LIST value bound to binding.
func ListSegment(binding string) ListPatternElementSpec {
	return engine.ListSegment(binding)
}

// ListRestWildcard builds a list pattern element matching a
// variable-length run without capturing it.
func ListRestWildcard() ListPatternElementSpec {
	return engine.ListRestWildcard()
}

// Ref builds an unqualified [NameRef], resolved against whatever module
// authors the reference.
func Ref(name string) NameRef {
	return engine.Ref(name)
}

// ModuleRef builds a [NameRef] explicitly qualified to module.
func ModuleRef(module ModuleName, name string) NameRef {
	return engine.ModuleRef(module, name)
}

// Path builds a [PathSpec] rooted at the field root, followed by
// segments.
func Path(root string, segments ...PathSegment) PathSpec {
	return engine.Path(root, segments...)
}

// MapKey builds a [PathSegment] that steps into a MAP field by key.
func MapKey(key string) PathSegment {
	return engine.MapKey(key)
}

// ListIndex builds a [PathSegment] that steps into a LIST field at
// index, which must be non-negative.
func ListIndex(index int) PathSegment {
	return engine.ListIndex(index)
}

// ReturnFact builds a [QueryReturnSpec] returning the whole fact bound to
// binding under the column name alias.
func ReturnFact(alias, binding string) QueryReturnSpec {
	return engine.ReturnFact(alias, binding)
}

// ReturnValue builds a [QueryReturnSpec] returning expression's computed
// value under the column name alias.
func ReturnValue(alias string, expression ExpressionSpec) QueryReturnSpec {
	return engine.ReturnValue(alias, expression)
}

// TemplateFact builds a [FactTarget] matching facts of the named
// template, in the condition's own module.
func TemplateFact(name string) FactTarget {
	return engine.TemplateFact(name)
}

// TemplateFactIn builds a [FactTarget] matching facts of the named
// template in an explicit module.
func TemplateFactIn(module ModuleName, name string) FactTarget {
	return engine.TemplateFactIn(module, name)
}

// TemplateKeyFact builds a [FactTarget] matching facts of the template
// identified directly by key, bypassing name and module resolution.
func TemplateKeyFact(key TemplateKey) FactTarget {
	return engine.TemplateKeyFact(key)
}

// Exists builds an [ExistsCondition] testing whether at least one tuple
// matching condition exists.
func Exists(condition ConditionSpec) ExistsCondition {
	return engine.Exists(condition)
}

// Forall builds a [ForallCondition] testing whether every tuple matching
// domain also satisfies requirement.
func Forall(domain ConditionSpec, requirement ConditionSpec) ForallCondition {
	return engine.Forall(domain, requirement)
}

// NewWorkspace returns an empty [Workspace] ready for Add calls.
func NewWorkspace() *Workspace {
	return engine.NewWorkspace()
}

// Compile compiles workspace into an immutable [Ruleset], equivalent to
// calling workspace.Compile directly.
func Compile(ctx context.Context, workspace *Workspace) (*Ruleset, error) {
	return workspace.Compile(ctx)
}

// NullValue returns a [Value] of kind [ValueNull].
func NullValue() Value {
	return engine.NullValue()
}

// NewValue converts a Go value into a [Value]: booleans, strings,
// integer types, floats, []any and typed slices, and map[string]any.
// Unsupported inputs, such as structs, functions, NaN, or unsigned
// values past the signed range, return an error wrapping
// [ErrUnsupportedValue].
func NewValue(raw any) (Value, error) {
	return engine.NewValue(raw)
}

// NewFields converts each entry of raw into a [Value], failing on the
// first value [NewValue] can't convert.
func NewFields(raw map[string]any) (Fields, error) {
	return engine.NewFields(raw)
}

// NewFieldsFromPairs builds fields from alternating string keys and raw
// values. It requires an even number of arguments and a non-empty
// string key in each pair.
func NewFieldsFromPairs(pairs ...any) (Fields, error) {
	return engine.NewFieldsFromPairs(pairs...)
}

// MustFields builds fields from alternating string keys and raw values,
// panicking when the inputs cannot be converted. It suits tests and
// fixed data.
func MustFields(pairs ...any) Fields {
	return engine.MustFields(pairs...)
}
