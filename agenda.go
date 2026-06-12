package gess

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type activationKey string

type activationStatus uint8

const (
	activationStatusPending activationStatus = iota
	activationStatusConsumed
	activationStatusDeactivated
)

func (s activationStatus) String() string {
	switch s {
	case activationStatusPending:
		return "pending"
	case activationStatusConsumed:
		return "consumed"
	case activationStatusDeactivated:
		return "deactivated"
	default:
		return "unknown"
	}
}

type activation struct {
	id               ActivationID
	key              activationKey
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	generation       Generation
	bindings         []bindingTupleEntry
	factIDs          []FactID
	factVersions     []FactVersion
	path             []int
	salience         int
	maxRecency       Recency
	aggregateRecency Recency
	declarationOrder int
	status           activationStatus
}

func (a activation) mutationOrigin() mutationOrigin {
	return mutationOrigin{
		ActivationID:   a.id,
		RuleID:         a.ruleID,
		RuleRevisionID: a.ruleRevisionID,
	}
}

func (a activation) clone() activation {
	out := a
	out.bindings = cloneBindingTupleEntries(a.bindings)
	out.factIDs = cloneFactIDs(a.factIDs)
	out.factVersions = cloneFactVersions(a.factVersions)
	out.path = cloneIntPath(a.path)
	return out
}

type agendaChangeKind uint8

const (
	agendaChangeActivated agendaChangeKind = iota
	agendaChangeDeactivated
)

type agendaChange struct {
	kind       agendaChangeKind
	activation activation
}

func (c agendaChange) event(sessionID SessionID, rulesetID RulesetID, sequence uint64, timestamp time.Time) Event {
	eventType := EventRuleActivated
	if c.kind == agendaChangeDeactivated {
		eventType = EventRuleDeactivated
	}

	return Event{
		SessionID:      sessionID,
		RulesetID:      rulesetID,
		Sequence:       sequence,
		Timestamp:      timestamp,
		Type:           eventType,
		Generation:     c.activation.generation,
		Recency:        c.activation.maxRecency,
		RuleID:         c.activation.ruleID,
		RuleRevisionID: c.activation.ruleRevisionID,
		ActivationID:   c.activation.id,
		FactIDs:        cloneFactIDs(c.activation.factIDs),
	}
}

type agenda struct {
	activations map[activationKey]*activation
	pending     []activationKey
	byFactID    map[FactID]map[activationKey]struct{}
	byRevision  map[RuleRevisionID]map[activationKey]struct{}
}

func newAgenda() *agenda {
	return &agenda{
		activations: make(map[activationKey]*activation),
		byFactID:    make(map[FactID]map[activationKey]struct{}),
		byRevision:  make(map[RuleRevisionID]map[activationKey]struct{}),
	}
}

func (a *agenda) reset() {
	if a == nil {
		return
	}
	a.activations = make(map[activationKey]*activation)
	a.pending = nil
	a.byFactID = make(map[FactID]map[activationKey]struct{})
	a.byRevision = make(map[RuleRevisionID]map[activationKey]struct{})
}

func (a *agenda) reconcile(ctx context.Context, revision *Ruleset, results []ruleMatchResult) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	seen := make(map[activationKey]struct{}, len(a.pending))
	nextPending := make([]activationKey, 0, len(a.pending))
	changes := make([]agendaChange, 0)
	activated := make([]agendaChange, 0)

	for _, result := range results {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := revision.rulesByRevisionID[result.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, result.ruleRevisionID)
		}
		if result.ruleID != rule.id {
			return nil, fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, result.ruleRevisionID)
		}

		for _, candidate := range result.candidates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			key := activationKey(candidate.key)
			if existing, ok := a.activations[key]; ok {
				if existing.status == activationStatusPending {
					if _, seenBefore := seen[key]; !seenBefore {
						seen[key] = struct{}{}
						nextPending = append(nextPending, key)
					}
				}
				continue
			}

			created := activation{
				id:               activationIDForKey(key),
				key:              key,
				ruleID:           result.ruleID,
				ruleRevisionID:   result.ruleRevisionID,
				generation:       candidate.generation,
				bindings:         cloneBindingTupleEntries(candidate.bindingTuple),
				factIDs:          cloneFactIDs(candidate.factIDs),
				factVersions:     cloneFactVersions(candidate.factVersions),
				path:             cloneIntPath(candidate.path),
				salience:         rule.salience,
				maxRecency:       candidate.maxRecency,
				aggregateRecency: candidate.aggregateRecency,
				declarationOrder: rule.declarationOrder,
				status:           activationStatusPending,
			}

			copyActivation := created.clone()
			a.activations[key] = &copyActivation
			indexActivation(a.byFactID, a.byRevision, copyActivation)

			if _, seenBefore := seen[key]; !seenBefore {
				seen[key] = struct{}{}
				nextPending = append(nextPending, key)
			}
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: copyActivation.clone(),
			})
		}
	}

	for _, key := range a.pending {
		if _, ok := seen[key]; ok {
			continue
		}
		existing, ok := a.activations[key]
		if !ok || existing.status != activationStatusPending {
			continue
		}
		existing.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: existing.clone(),
		})
	}

	changes = append(changes, activated...)

	sort.SliceStable(nextPending, func(i, j int) bool {
		left := a.activations[nextPending[i]]
		right := a.activations[nextPending[j]]
		return activationLess(left, right)
	})

	a.pending = nextPending
	return changes, nil
}

func (a *agenda) next() (activation, bool) {
	if a == nil {
		return activation{}, false
	}
	for len(a.pending) > 0 {
		key := a.pending[0]
		a.pending = a.pending[1:]

		current, ok := a.activations[key]
		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusConsumed
		return current.clone(), true
	}
	return activation{}, false
}

func (a *agenda) clear() []agendaChange {
	if a == nil {
		return nil
	}
	changes := make([]agendaChange, 0, len(a.pending))
	for _, key := range a.pending {
		current, ok := a.activations[key]
		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: current.clone(),
		})
	}
	a.reset()
	return changes
}

func (a *agenda) activationByKey(key activationKey) (activation, bool) {
	if a == nil {
		return activation{}, false
	}
	current, ok := a.activations[key]
	if !ok {
		return activation{}, false
	}
	return current.clone(), true
}

func (a *agenda) pendingActivations() []activation {
	if a == nil || len(a.pending) == 0 {
		return nil
	}
	out := make([]activation, 0, len(a.pending))
	for _, key := range a.pending {
		if current, ok := a.activations[key]; ok && current.status == activationStatusPending {
			out = append(out, current.clone())
		}
	}
	return out
}

func (a *agenda) activationsByFactID(id FactID) []activation {
	if a == nil {
		return nil
	}
	keys := a.byFactID[id]
	if len(keys) == 0 {
		return nil
	}
	out := make([]activation, 0, len(keys))
	for key := range keys {
		if current, ok := a.activations[key]; ok {
			out = append(out, current.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return activationLess(&out[i], &out[j])
	})
	return out
}

func (a *agenda) activationsByRuleRevisionID(id RuleRevisionID) []activation {
	if a == nil {
		return nil
	}
	keys := a.byRevision[id]
	if len(keys) == 0 {
		return nil
	}
	out := make([]activation, 0, len(keys))
	for key := range keys {
		if current, ok := a.activations[key]; ok {
			out = append(out, current.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return activationLess(&out[i], &out[j])
	})
	return out
}

func indexActivation(byFactID map[FactID]map[activationKey]struct{}, byRevision map[RuleRevisionID]map[activationKey]struct{}, act activation) {
	for _, factID := range act.factIDs {
		keys := byFactID[factID]
		if keys == nil {
			keys = make(map[activationKey]struct{})
			byFactID[factID] = keys
		}
		keys[act.key] = struct{}{}
	}

	keys := byRevision[act.ruleRevisionID]
	if keys == nil {
		keys = make(map[activationKey]struct{})
		byRevision[act.ruleRevisionID] = keys
	}
	keys[act.key] = struct{}{}
}

func activationLess(left, right *activation) bool {
	if left == nil || right == nil {
		return right != nil
	}
	if left.salience != right.salience {
		return left.salience > right.salience
	}
	if left.maxRecency != right.maxRecency {
		return left.maxRecency > right.maxRecency
	}
	if left.aggregateRecency != right.aggregateRecency {
		return left.aggregateRecency > right.aggregateRecency
	}
	if left.declarationOrder != right.declarationOrder {
		return left.declarationOrder < right.declarationOrder
	}
	return left.id < right.id
}

func activationIDForKey(key activationKey) ActivationID {
	return ActivationID("activation:" + string(key))
}

func cloneBindingTupleEntries(entries []bindingTupleEntry) []bindingTupleEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]bindingTupleEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
		out[i].conditionPath = cloneIntPath(entry.conditionPath)
	}
	return out
}

func cloneFactIDs(ids []FactID) []FactID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

func cloneFactVersions(versions []FactVersion) []FactVersion {
	if len(versions) == 0 {
		return nil
	}
	out := make([]FactVersion, len(versions))
	copy(out, versions)
	return out
}
