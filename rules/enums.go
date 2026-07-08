package rules

// AggregateKind identifies which aggregate an AggregateSpec computes.
type AggregateKind string

const (
	AggregateCount   AggregateKind = "count"
	AggregateSum     AggregateKind = "sum"
	AggregateMin     AggregateKind = "min"
	AggregateMax     AggregateKind = "max"
	AggregateCollect AggregateKind = "collect"
)

// ActionEffectKind identifies the mutation an ActionEffectSpec performs.
type ActionEffectKind uint8

const (
	ActionEffectAssert ActionEffectKind = iota
	ActionEffectAssertLogical
	ActionEffectModify
	ActionEffectRetract
	ActionEffectEmit
	ActionEffectBind
	ActionEffectPushFocus
	ActionEffectPopFocus
	ActionEffectClearFocus
	ActionEffectHalt
)

// ConditionTreeKind identifies the shape of a RuleConditionTree node.
type ConditionTreeKind string

const (
	ConditionTreeKindUnknown    ConditionTreeKind = ""
	ConditionTreeKindAnd        ConditionTreeKind = "and"
	ConditionTreeKindMatch      ConditionTreeKind = "match"
	ConditionTreeKindTest       ConditionTreeKind = "test"
	ConditionTreeKindNot        ConditionTreeKind = "not"
	ConditionTreeKindOr         ConditionTreeKind = "or"
	ConditionTreeKindExists     ConditionTreeKind = "exists"
	ConditionTreeKindForall     ConditionTreeKind = "forall"
	ConditionTreeKindAccumulate ConditionTreeKind = "accumulate"
)

// DuplicatePolicy controls how a template deduplicates facts.
type DuplicatePolicy int

const (
	DuplicateStructural DuplicatePolicy = iota
	DuplicateAllow
	DuplicateUniqueKey
)

// ExpressionComparisonOperator is the operator in a CompareExpr.
type ExpressionComparisonOperator string

const (
	ExpressionCompareUnknown        ExpressionComparisonOperator = ""
	ExpressionCompareEqual          ExpressionComparisonOperator = "eq"
	ExpressionCompareNotEqual       ExpressionComparisonOperator = "neq"
	ExpressionCompareLessThan       ExpressionComparisonOperator = "lt"
	ExpressionCompareLessOrEqual    ExpressionComparisonOperator = "lte"
	ExpressionCompareGreaterThan    ExpressionComparisonOperator = "gt"
	ExpressionCompareGreaterOrEqual ExpressionComparisonOperator = "gte"
)

// ExpressionBooleanOperator is the operator in a BooleanExpr.
type ExpressionBooleanOperator string

const (
	ExpressionBoolUnknown ExpressionBooleanOperator = ""
	ExpressionBoolAnd     ExpressionBooleanOperator = "and"
	ExpressionBoolOr      ExpressionBooleanOperator = "or"
	ExpressionBoolNot     ExpressionBooleanOperator = "not"
)

// ExpressionPredicatePlacement reports where the compiler placed a predicate.
type ExpressionPredicatePlacement string

const (
	ExpressionPredicatePlacementUnknown      ExpressionPredicatePlacement = ""
	ExpressionPredicatePlacementAlpha        ExpressionPredicatePlacement = "alpha"
	ExpressionPredicatePlacementBetaResidual ExpressionPredicatePlacement = "beta-residual"
	ExpressionPredicatePlacementUnsupported  ExpressionPredicatePlacement = "unsupported"
)

// FactTargetKind identifies what kind of fact a FactTarget matches.
type FactTargetKind uint8

const (
	FactTargetUnknown FactTargetKind = iota
	FactTargetDynamic
	FactTargetTemplate
	FactTargetTemplateKey
)

// FieldConstraintOperator is the comparison a FieldConstraintSpec applies.
type FieldConstraintOperator string

const (
	FieldConstraintOpUnknown        FieldConstraintOperator = ""
	FieldConstraintOpExists         FieldConstraintOperator = "exists"
	FieldConstraintOpEqual          FieldConstraintOperator = "eq"
	FieldConstraintOpNotEqual       FieldConstraintOperator = "neq"
	FieldConstraintOpLessThan       FieldConstraintOperator = "lt"
	FieldConstraintOpLessOrEqual    FieldConstraintOperator = "lte"
	FieldConstraintOpGreaterThan    FieldConstraintOperator = "gt"
	FieldConstraintOpGreaterOrEqual FieldConstraintOperator = "gte"

	FieldConstraintExists         = FieldConstraintOpExists
	FieldConstraintEqual          = FieldConstraintOpEqual
	FieldConstraintNotEqual       = FieldConstraintOpNotEqual
	FieldConstraintLessThan       = FieldConstraintOpLessThan
	FieldConstraintLessOrEqual    = FieldConstraintOpLessOrEqual
	FieldConstraintGreaterThan    = FieldConstraintOpGreaterThan
	FieldConstraintGreaterOrEqual = FieldConstraintOpGreaterOrEqual
)

// FieldPresence reports how a field came to have its value.
type FieldPresence string

const (
	FieldPresenceOmitted  FieldPresence = "omitted"
	FieldPresenceDefault  FieldPresence = "default"
	FieldPresenceExplicit FieldPresence = "explicit"
)

// ListPatternElementKind identifies one element of a ListPatternSpec.
type ListPatternElementKind string

const (
	ListPatternElementUnknown      ListPatternElementKind = ""
	ListPatternElementValue        ListPatternElementKind = "value"
	ListPatternElementWildcard     ListPatternElementKind = "wildcard"
	ListPatternElementSegment      ListPatternElementKind = "segment"
	ListPatternElementRestWildcard ListPatternElementKind = "rest-wildcard"
)

// PathSegmentKind identifies one step of a PathSpec.
type PathSegmentKind string

const (
	PathSegmentRoot  PathSegmentKind = "root"
	PathSegmentMap   PathSegmentKind = "map"
	PathSegmentIndex PathSegmentKind = "index"
)

// WhyNotOutcome is the top-level answer to "why has this rule not fired?".
type WhyNotOutcome string

const (
	WhyNotActivated    WhyNotOutcome = "activated"
	WhyNotAlreadyFired WhyNotOutcome = "already_fired"
	WhyNotNeverMatched WhyNotOutcome = "never_matched"
	WhyNotBlocked      WhyNotOutcome = "blocked"
)

// WhyNotConditionReason classifies why one condition did not extend a partial
// match.
type WhyNotConditionReason string

const (
	WhyNotReasonNone            WhyNotConditionReason = ""
	WhyNotReasonNoAlphaMatches  WhyNotConditionReason = "no_alpha_matches"
	WhyNotReasonJoinMismatch    WhyNotConditionReason = "join_mismatch"
	WhyNotReasonPredicate       WhyNotConditionReason = "predicate_rejected"
	WhyNotReasonNegationBlocked WhyNotConditionReason = "negation_blocked"
)
