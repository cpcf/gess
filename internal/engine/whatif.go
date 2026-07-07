package engine

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// DefaultWhatIfMaxFirings bounds a what-if fork run when no explicit limit is
// given, guarding against a runaway hypothetical.
const DefaultWhatIfMaxFirings = 10_000

// WhatIfReport is the structured result of a counterfactual run: what fired,
// how working memory and the agenda changed, and — when requested —
// derivations for the facts the scenario produced. Every field is a value; the
// base session is untouched.
type WhatIfReport struct {
	// Base is working memory at fork time (identical to the base session,
	// which is never mutated).
	Base Snapshot
	// Fork is working memory after the scenario ran.
	Fork Snapshot
	// Run is the fork's bounded run result.
	Run RunResult
	// Firings lists the rules that fired, in order.
	Firings []WhatIfFiring
	// Diff is the working-memory difference from Base to Fork.
	Diff SnapshotDiff
	// AgendaBefore and AgendaAfter bracket the scenario.
	AgendaBefore Agenda
	AgendaAfter  Agenda
	// Derivations explains each added fact when WithWhatIfExplain was set.
	Derivations []Derivation
	// ForkSession holds the live fork when WithWhatIfRetainFork was set; the
	// caller then owns closing it. Otherwise it is nil and the fork is closed.
	ForkSession *Session
}

// WhatIfFiring is one rule firing during a what-if run.
type WhatIfFiring struct {
	RuleID         RuleID
	RuleName       string
	RuleRevisionID RuleRevisionID
	ActivationID   ActivationID
	FactIDs        []FactID
	Sequence       uint64
}

type whatIfConfig struct {
	maxFirings int
	explain    bool
	retainFork bool
	output     io.Writer
}

// WhatIfOption configures a Session.WhatIf run.
type WhatIfOption func(*whatIfConfig)

func newWhatIfConfig(opts []WhatIfOption) whatIfConfig {
	cfg := whatIfConfig{maxFirings: DefaultWhatIfMaxFirings}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// WithWhatIfMaxFirings bounds the fork run at n firings (default
// DefaultWhatIfMaxFirings). Pass n <= 0 to opt out of the bound explicitly.
func WithWhatIfMaxFirings(n int) WhatIfOption {
	return func(cfg *whatIfConfig) { cfg.maxFirings = n }
}

// WithWhatIfExplain attaches an explain log to the fork so the report includes
// a derivation for every added fact.
func WithWhatIfExplain() WhatIfOption {
	return func(cfg *whatIfConfig) { cfg.explain = true }
}

// WithWhatIfRetainFork keeps the fork open and returns it in the report for
// follow-up interaction; the caller owns closing it. By default the fork is
// closed before WhatIf returns.
func WithWhatIfRetainFork() WhatIfOption {
	return func(cfg *whatIfConfig) { cfg.retainFork = true }
}

// WithWhatIfOutputWriter captures the emit output of the counterfactual run to
// w. By default the fork's output is discarded so a hypothetical run never
// writes to the base session's live output sink; pass this to inspect what the
// scenario would have emitted.
func WithWhatIfOutputWriter(w io.Writer) WhatIfOption {
	return func(cfg *whatIfConfig) { cfg.output = w }
}

// WhatIf forks the session, applies the scenario's hypothetical mutations, runs
// the fork bounded, and returns a structured report — the fired rules, the
// working-memory diff, the agenda delta, and (with WithWhatIfExplain)
// derivations for new facts. The base session is untouched in every path,
// including scenario error and cancellation: the base is locked only for the
// fork step, then released before the scenario runs.
//
// The scenario receives the fork and uses the normal Assert/Modify/Retract/
// Focus API. A scenario error aborts, closes the fork, and is returned wrapped.
// WhatIf during an active base Run returns ErrConcurrencyMisuse (from Fork).
// With WithWhatIfExplain, a failure to derive any added fact (e.g. a canceled
// context) aborts and is returned wrapped rather than yielding a report with
// derivations silently missing.
func (s *Session) WhatIf(ctx context.Context, scenario func(ctx context.Context, fork *Session) error, opts ...WhatIfOption) (WhatIfReport, error) {
	if s == nil || s.closed {
		return WhatIfReport{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WhatIfReport{}, err
	}
	if scenario == nil {
		return WhatIfReport{}, fmt.Errorf("gess: what-if requires a scenario")
	}
	cfg := newWhatIfConfig(opts)

	recorder := &whatIfFiringRecorder{}
	// Isolate the fork's emit output: without this the fork inherits the base
	// session's live writer, so a hypothetical emit would pollute real output
	// and could race a concurrent base Run. Default to discarding; a caller can
	// capture it with WithWhatIfOutputWriter.
	output := cfg.output
	if output == nil {
		output = io.Discard
	}
	forkOpts := []SessionOption{
		WithEventListener(recorder, ForEventTypes(EventRuleFired)),
		WithOutputWriter(output),
	}
	if cfg.explain {
		forkOpts = append(forkOpts, WithExplainLog())
	}
	fork, err := s.Fork(ctx, forkOpts...)
	if err != nil {
		return WhatIfReport{}, err
	}
	closeFork := true
	defer func() {
		if closeFork {
			_ = fork.Close()
		}
	}()

	base, err := fork.Snapshot(ctx)
	if err != nil {
		return WhatIfReport{}, err
	}
	agendaBefore, err := fork.Agenda(ctx)
	if err != nil {
		return WhatIfReport{}, err
	}

	if err := scenario(ctx, fork); err != nil {
		return WhatIfReport{}, fmt.Errorf("gess: what-if scenario failed: %w", err)
	}

	var runOpts []RunOption
	if cfg.maxFirings > 0 {
		runOpts = append(runOpts, WithMaxFirings(cfg.maxFirings))
	}
	runResult, err := fork.Run(ctx, runOpts...)
	if err != nil {
		return WhatIfReport{}, fmt.Errorf("gess: what-if run failed: %w", err)
	}

	forkSnapshot, err := fork.Snapshot(ctx)
	if err != nil {
		return WhatIfReport{}, err
	}
	agendaAfter, err := fork.Agenda(ctx)
	if err != nil {
		return WhatIfReport{}, err
	}

	report := WhatIfReport{
		Base:         base,
		Fork:         forkSnapshot,
		Run:          runResult,
		Firings:      recorder.collect(fork.revision),
		Diff:         DiffSnapshots(base, forkSnapshot),
		AgendaBefore: agendaBefore,
		AgendaAfter:  agendaAfter,
	}
	if cfg.explain {
		report.Derivations, err = whatIfDerivations(fork, forkSnapshot, report.Diff.Added)
		if err != nil {
			return WhatIfReport{}, err
		}
	}
	if cfg.retainFork {
		report.ForkSession = fork
		closeFork = false
	}
	return report, nil
}

// whatIfDerivations explains each added fact against the single fork snapshot
// already taken for the report, reusing it rather than re-snapshotting working
// memory per fact (the fork is idle, so every derivation sees the same state).
// It mirrors Session.Explain: a support-only derivation from the snapshot,
// enriched with lineage from the fork's explain log.
func whatIfDerivations(fork *Session, snapshot Snapshot, added []FactSnapshot) ([]Derivation, error) {
	derivations := make([]Derivation, 0, len(added))
	for _, fact := range added {
		derivation, ok := snapshot.Explain(fact.ID())
		if !ok {
			return nil, fmt.Errorf("gess: what-if explain failed for fact %s: %w", fact.ID(), ErrFactNotFound)
		}
		if fork.explainLog != nil {
			fork.explainLog.enrich(&derivation, snapshot.revision)
		}
		derivations = append(derivations, derivation)
	}
	return derivations, nil
}

type whatIfFiringRecorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *whatIfFiringRecorder) HandleEvent(_ context.Context, event Event) error {
	if event.Type != EventRuleFired {
		return nil
	}
	r.mu.Lock()
	r.events = append(r.events, event.clone())
	r.mu.Unlock()
	return nil
}

func (r *whatIfFiringRecorder) collect(revision *Ruleset) []WhatIfFiring {
	r.mu.Lock()
	defer r.mu.Unlock()
	firings := make([]WhatIfFiring, 0, len(r.events))
	for _, event := range r.events {
		firing := WhatIfFiring{
			RuleID:         event.RuleID,
			RuleRevisionID: event.RuleRevisionID,
			ActivationID:   event.ActivationID,
			FactIDs:        event.RelatedFactIDs(),
			Sequence:       event.Sequence,
		}
		if revision != nil {
			if rule, ok := revision.RuleByRevisionID(event.RuleRevisionID); ok {
				firing.RuleName = rule.Name()
			}
		}
		firings = append(firings, firing)
	}
	return firings
}
