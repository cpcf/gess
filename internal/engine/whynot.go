package engine

import (
	"context"
	"fmt"
)

// WhyNotOutcome is the top-level answer to "why has this rule not fired?".
type WhyNotOutcome string

const (
	// WhyNotActivated means the rule has a pending activation; it will fire
	// (see Activations). It is not "why not" at all — report it plainly.
	WhyNotActivated WhyNotOutcome = "activated"
	// WhyNotAlreadyFired means the rule matched and fired; the activation is
	// refracted and will not fire again until the match changes.
	WhyNotAlreadyFired WhyNotOutcome = "already_fired"
	// WhyNotNeverMatched means no branch produced a complete match; the
	// per-branch conditions locate the first failing condition.
	WhyNotNeverMatched WhyNotOutcome = "never_matched"
	// WhyNotBlocked means the closest branch failed on a negated condition
	// that is currently blocked by one or more facts.
	WhyNotBlocked WhyNotOutcome = "blocked"
)

// WhyNotConditionReason classifies why one condition did not extend a partial
// match. It is set only on a branch's first failing condition.
type WhyNotConditionReason string

const (
	WhyNotReasonNone            WhyNotConditionReason = ""
	WhyNotReasonNoAlphaMatches  WhyNotConditionReason = "no_alpha_matches"
	WhyNotReasonJoinMismatch    WhyNotConditionReason = "join_mismatch"
	WhyNotReasonPredicate       WhyNotConditionReason = "predicate_rejected"
	WhyNotReasonNegationBlocked WhyNotConditionReason = "negation_blocked"
)

// WhyNotReport is the structured diagnosis for one rule.
type WhyNotReport struct {
	RuleID         RuleID
	RuleName       string
	RuleRevisionID RuleRevisionID
	Outcome        WhyNotOutcome
	// Activations holds the pending activations when Outcome is
	// WhyNotActivated.
	Activations []AgendaActivation
	// Branches diagnoses every authored branch (an "or" rule has more than
	// one), in branch-id order.
	Branches []WhyNotBranch
	// Truncated is set when a partial-match or probe cap was reached, so the
	// report is a bounded sample rather than the full picture.
	Truncated bool
}

// WhyNotBranch diagnoses one condition branch of a rule.
type WhyNotBranch struct {
	BranchID int
	// Conditions is the branch's conditions in authored order.
	Conditions []WhyNotCondition
	// FirstFailing indexes Conditions at the first condition that stopped a
	// partial match from extending, or -1 when the branch has a complete
	// match (which the rule-level Outcome then explains).
	FirstFailing int
	// PartialMatches are the deepest surviving partial matches for the
	// branch, each a near-miss with bound values; capped and sorted.
	PartialMatches []WhyNotPartialMatch
}

// WhyNotCondition reports one condition's status within a branch.
type WhyNotCondition struct {
	Order        int
	PlannedOrder int
	Binding      string
	Negated      bool
	Test         bool
	Aggregate    bool
	Source       SourceSpan
	AlphaMatches int
	Satisfied    bool
	// Reason, RejectingSpan, Blockers, and BlockerCount are populated only
	// on the first failing condition.
	Reason        WhyNotConditionReason
	RejectingSpan SourceSpan
	Blockers      []FactID
	BlockerCount  int
}

// WhyNotPartialMatch is one near-miss: the facts and bound values that
// satisfied the conditions up to the failing one.
type WhyNotPartialMatch struct {
	Facts          []FactID
	Bindings       []BindingValue
	Satisfied      int
	RejectedBySpan SourceSpan
}

const (
	defaultWhyNotMaxPartialMatches = 3
	defaultWhyNotMaxBlockers       = 16
	defaultWhyNotMaxProbedRows     = 4096
)

type whyNotConfig struct {
	maxPartialMatches int
	maxBlockers       int
	maxProbedRows     int
}

// WhyNotOption configures a WhyNot probe.
type WhyNotOption func(*whyNotConfig)

func newWhyNotConfig(opts []WhyNotOption) whyNotConfig {
	cfg := whyNotConfig{
		maxPartialMatches: defaultWhyNotMaxPartialMatches,
		maxBlockers:       defaultWhyNotMaxBlockers,
		maxProbedRows:     defaultWhyNotMaxProbedRows,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxPartialMatches <= 0 {
		cfg.maxPartialMatches = defaultWhyNotMaxPartialMatches
	}
	if cfg.maxBlockers <= 0 {
		cfg.maxBlockers = defaultWhyNotMaxBlockers
	}
	if cfg.maxProbedRows <= 0 {
		cfg.maxProbedRows = defaultWhyNotMaxProbedRows
	}
	return cfg
}

// WithWhyNotMaxPartialMatches caps the near-miss partial matches reported per
// branch (default 3).
func WithWhyNotMaxPartialMatches(n int) WhyNotOption {
	return func(cfg *whyNotConfig) { cfg.maxPartialMatches = n }
}

// WithWhyNotMaxBlockers caps the blocking facts listed for a blocked negation
// (default 16); BlockerCount still reports the true total.
func WithWhyNotMaxBlockers(n int) WhyNotOption {
	return func(cfg *whyNotConfig) { cfg.maxBlockers = n }
}

// WithWhyNotMaxProbedRows bounds the beta rows the probe scans per node
// (default 4096); reaching it sets Truncated.
func WithWhyNotMaxProbedRows(n int) WhyNotOption {
	return func(cfg *whyNotConfig) { cfg.maxProbedRows = n }
}

// WhyNot diagnoses why the named rule has no pending activation: the closest
// partial match per branch, the first failing condition with its source
// location, the blocking facts for negations, and refraction. It reads live
// Rete state and re-executes predicates only along the probed chain; it adds
// no per-fact or per-token state and never mutates the runtime.
//
// It is idle-only under the same guard as Agenda and Snapshot: calling it
// during an active Run returns ErrConcurrencyMisuse; a closed session returns
// ErrClosedSession; an unknown rule returns ErrRuleNotFound.
func (s *Session) WhyNot(ctx context.Context, ruleName string, opts ...WhyNotOption) (WhyNotReport, error) {
	if s == nil || s.closed {
		return WhyNotReport{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WhyNotReport{}, err
	}
	if s.revision == nil {
		return WhyNotReport{}, ErrInvalidRuleset
	}
	rule, ok := s.revision.rules[ruleName]
	if !ok {
		return WhyNotReport{}, fmt.Errorf("%w: %q", ErrRuleNotFound, ruleName)
	}
	if s.runGuardHeld() {
		return WhyNotReport{}, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return WhyNotReport{}, ErrConcurrencyMisuse
	}
	defer s.unlock()

	if s.agendaDirty {
		return WhyNotReport{}, fmt.Errorf("%w: dirty agenda cannot be reconciled during run", ErrUnsupportedRuntime)
	}
	if !s.agendaReady {
		if _, err := s.reconcileAgendaInternal(ctx); err != nil {
			return WhyNotReport{}, err
		}
	}
	return s.whyNotLocked(rule, newWhyNotConfig(opts)), nil
}
