package engine

import (
	"context"
	"sync"
)

// DefaultExplainLogMaxEntries bounds a session explain log when no explicit
// bound is given. The log retains this many of the most recent fact-mutation
// events; older entries are evicted and the affected fact's reconstructed
// history is reported as Truncated.
const DefaultExplainLogMaxEntries = 4096

type explainLogConfig struct {
	maxEntries int
}

// ExplainLogOption configures the bounded explain log installed by
// WithExplainLog.
type ExplainLogOption func(*explainLogConfig)

// WithExplainLogMaxEntries bounds the explain log to the most recent n
// fact-mutation entries (default DefaultExplainLogMaxEntries). A value <= 0
// keeps the default.
func WithExplainLogMaxEntries(n int) ExplainLogOption {
	return func(cfg *explainLogConfig) { cfg.maxEntries = n }
}

// WithExplainLog attaches a bounded, in-memory recorder of fact-mutation
// events so Session.Explain can report a fact's producing firing and its
// assert -> modify... lineage. Without it, Session.Explain returns
// ErrExplainLogUnavailable and callers fall back to Snapshot.Explain.
//
// A fork does not inherit listeners, so it does not inherit the log; pass
// WithExplainLog to Fork to record lineage on the fork (starting empty, with
// event sequences continuing from the parent). Reset clears the log.
func WithExplainLog(opts ...ExplainLogOption) SessionOption {
	return func(cfg *sessionConfig) {
		log := newExplainLog(opts)
		cfg.explainLog = log
		cfg.listeners = append(cfg.listeners, newEventListenerRegistration(log, []EventListenerOption{
			ForEventTypes(EventFactAsserted, EventFactModified, EventFactRetracted, EventReset),
		}))
	}
}

// explainLogEntry is one recorded fact mutation, retained in arrival order per
// fact. Values captured here are already deep-copied clones (listeners receive
// cloned events), so entries are safe to retain.
type explainLogEntry struct {
	sequence       uint64
	kind           MutationKind
	factID         FactID
	generation     Generation
	ruleID         RuleID
	ruleRevisionID RuleRevisionID
	activationID   ActivationID
	actionName     string
	actionIndex    int
	changedFields  []FieldChange
}

type explainLog struct {
	mu             sync.Mutex
	maxEntries     int
	byFact         map[FactID][]explainLogEntry
	arrivals       []FactID
	truncatedFacts map[FactID]struct{}
	total          int
	generation     Generation
}

func newExplainLog(opts []ExplainLogOption) *explainLog {
	cfg := explainLogConfig{maxEntries: DefaultExplainLogMaxEntries}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxEntries <= 0 {
		cfg.maxEntries = DefaultExplainLogMaxEntries
	}
	return &explainLog{
		maxEntries:     cfg.maxEntries,
		byFact:         make(map[FactID][]explainLogEntry),
		truncatedFacts: make(map[FactID]struct{}),
	}
}

func (l *explainLog) HandleEvent(_ context.Context, event Event) error {
	switch event.Type {
	case EventFactAsserted, EventFactModified, EventFactRetracted:
		l.record(event)
	case EventReset:
		l.clear(resetGeneration(event))
	}
	return nil
}

func (l *explainLog) record(event Event) {
	fid := mutatedFactID(event)
	if fid.IsZero() {
		return
	}
	entry := explainLogEntry{
		sequence:       event.Sequence,
		kind:           mutationKindForEvent(event.Type),
		factID:         fid,
		generation:     event.Generation,
		ruleID:         event.RuleID,
		ruleRevisionID: event.RuleRevisionID,
		activationID:   event.ActivationID,
		actionName:     event.ActionName,
		actionIndex:    event.ActionIndex,
	}
	if event.Delta != nil && len(event.Delta.ChangedFields) > 0 {
		entry.changedFields = event.Delta.FieldChanges()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if event.Generation > l.generation {
		l.generation = event.Generation
	}
	l.byFact[fid] = append(l.byFact[fid], entry)
	l.arrivals = append(l.arrivals, fid)
	l.total++
	for l.total > l.maxEntries {
		l.evictOldestLocked()
	}
}

func (l *explainLog) evictOldestLocked() {
	if len(l.arrivals) == 0 {
		return
	}
	oldest := l.arrivals[0]
	l.arrivals = l.arrivals[1:]
	l.total--
	entries := l.byFact[oldest]
	if len(entries) > 0 {
		l.byFact[oldest] = entries[1:]
		if len(l.byFact[oldest]) == 0 {
			delete(l.byFact, oldest)
		}
	}
	l.truncatedFacts[oldest] = struct{}{}
}

func (l *explainLog) clear(generation Generation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byFact = make(map[FactID][]explainLogEntry)
	l.arrivals = nil
	l.truncatedFacts = make(map[FactID]struct{})
	l.total = 0
	l.generation = generation
}

// historyForFact returns a copy of a fact's recorded entries in arrival order
// and whether any of its earlier entries were evicted.
func (l *explainLog) historyForFact(id FactID) ([]explainLogEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := l.byFact[id]
	_, truncated := l.truncatedFacts[id]
	if len(entries) == 0 {
		return nil, truncated
	}
	out := make([]explainLogEntry, len(entries))
	copy(out, entries)
	return out, truncated
}

// enrich fills ProducedBy and History on a derivation and its supporters from
// the log, in place. It is called only from the idle-only Session.Explain.
func (l *explainLog) enrich(d *Derivation, revision *Ruleset) {
	entries, truncated := l.historyForFact(d.Fact.ID())
	if len(entries) > 0 {
		history := make([]MutationRecord, 0, len(entries))
		var latest *explainLogEntry
		for i := range entries {
			entry := entries[i]
			history = append(history, MutationRecord{
				Kind:          entry.kind,
				Firing:        entry.firing(revision),
				ChangedFields: cloneFieldChanges(entry.changedFields),
				Sequence:      entry.sequence,
			})
			if entry.kind == MutationAssert || entry.kind == MutationModify {
				latest = &entries[i]
			}
		}
		d.History = history
		if latest != nil {
			l.applyProducedBy(d, latest, revision)
		}
	}
	if truncated {
		d.Truncated = true
	}
	for i := range d.DependsOn {
		l.enrich(&d.DependsOn[i], revision)
	}
}

// applyProducedBy sets or enriches d.ProducedBy from the latest state-producing
// entry. A stated fact takes a fresh firing; a logically-supported fact keeps
// the support-edge firing Snapshot.Explain already built (with its supporting
// facts) and gains the rendered action source.
func (l *explainLog) applyProducedBy(d *Derivation, latest *explainLogEntry, revision *Ruleset) {
	firing := latest.firing(revision)
	if d.ProducedBy == nil {
		d.ProducedBy = firing
		return
	}
	if firing == nil {
		return
	}
	if firing.Action != "" {
		d.ProducedBy.Action = firing.Action
	}
	if d.ProducedBy.RuleName == "" {
		d.ProducedBy.RuleName = firing.RuleName
	}
	d.ProducedBy.BindingsPartial = firing.BindingsPartial
}

// firing reconstructs the rule firing behind a mutation. It returns nil for a
// non-rule (external API) mutation. Bindings cannot be recovered from the event
// stream alone, so BindingsPartial is always set for a reconstructed firing;
// firing-time capture supersedes this when available.
func (e explainLogEntry) firing(revision *Ruleset) *Firing {
	if e.ruleID.IsZero() && e.ruleRevisionID.IsZero() {
		return nil
	}
	firing := &Firing{
		RuleID:          e.ruleID,
		RuleRevisionID:  e.ruleRevisionID,
		ActivationID:    e.activationID,
		Generation:      e.generation,
		BindingsPartial: true,
	}
	if revision != nil {
		if rule, ok := revision.RuleByRevisionID(e.ruleRevisionID); ok {
			firing.RuleName = rule.Name()
		} else if rule, ok := revision.RuleByID(e.ruleID); ok {
			firing.RuleName = rule.Name()
		}
		if e.actionName != "" {
			if action, ok := revision.Action(e.actionName); ok {
				firing.Action = action.GessSource()
			}
		}
	}
	return firing
}

func mutatedFactID(event Event) FactID {
	if len(event.FactIDs) > 0 {
		return event.FactIDs[0]
	}
	if event.Delta != nil {
		return event.Delta.FactID
	}
	return FactID{}
}

func mutationKindForEvent(eventType EventType) MutationKind {
	switch eventType {
	case EventFactAsserted:
		return MutationAssert
	case EventFactModified:
		return MutationModify
	case EventFactRetracted:
		return MutationRetract
	default:
		return MutationKind("")
	}
}

func resetGeneration(event Event) Generation {
	if event.Delta != nil && event.Delta.Generation != 0 {
		return event.Delta.Generation
	}
	return event.Generation
}
