package gess

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type activationKey struct {
	identity candidateIdentityKey
	index    int
}

type activationBucket struct {
	first    *activation
	overflow []*activation
}

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
	identity         candidateIdentity
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
	activations map[candidateIdentityKey]activationBucket
	pending     []activationKey
	byFactID    map[FactID]map[activationKey]struct{}
	byRevision  map[RuleRevisionID]map[activationKey]struct{}
}

func newAgenda() *agenda {
	return &agenda{
		activations: make(map[candidateIdentityKey]activationBucket),
		byFactID:    make(map[FactID]map[activationKey]struct{}),
		byRevision:  make(map[RuleRevisionID]map[activationKey]struct{}),
	}
}

func (a *agenda) reset() {
	if a == nil {
		return
	}
	a.activations = make(map[candidateIdentityKey]activationBucket)
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

			existing, key, ok := a.activationForCandidate(candidate)
			if ok {
				if existing.status == activationStatusPending {
					if _, seenBefore := seen[key]; !seenBefore {
						seen[key] = struct{}{}
						nextPending = append(nextPending, key)
					}
				}
				continue
			}

			created := activation{
				ruleID:           result.ruleID,
				ruleRevisionID:   result.ruleRevisionID,
				generation:       candidate.generation,
				identity:         candidate.identity,
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
			key = a.storeActivation(&copyActivation)
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
		existing, ok := a.activationByKeyPtr(key)
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
		left, _ := a.activationByKeyPtr(nextPending[i])
		right, _ := a.activationByKeyPtr(nextPending[j])
		return activationLess(left, right)
	})

	a.pending = nextPending
	return changes, nil
}

func (a *agenda) purgeRuleRevisions(revisionIDs map[RuleRevisionID]struct{}) []agendaChange {
	if a == nil || len(revisionIDs) == 0 {
		return nil
	}

	changes := make([]agendaChange, 0)
	removed := make(map[activationKey]struct{})
	for _, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current == nil {
			continue
		}
		if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
			continue
		}
		removed[key] = struct{}{}
		if current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: current.clone(),
		})
	}

	nextActivations := make(map[candidateIdentityKey]activationBucket, len(a.activations))
	nextByFactID := make(map[FactID]map[activationKey]struct{}, len(a.byFactID))
	nextByRevision := make(map[RuleRevisionID]map[activationKey]struct{}, len(a.byRevision))
	nextPending := make([]activationKey, 0, len(a.pending))

	for identityKey, bucket := range a.activations {
		nextBucket := activationBucket{}
		if current := bucket.first; current != nil {
			if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
				nextBucket.first = current
				indexActivation(nextByFactID, nextByRevision, *current)
			}
		}
		if len(bucket.overflow) > 0 {
			nextBucket.overflow = make([]*activation, len(bucket.overflow))
		}
		for i, current := range bucket.overflow {
			if current == nil {
				continue
			}
			if _, ok := revisionIDs[current.ruleRevisionID]; ok {
				continue
			}
			nextBucket.overflow[i] = current
			indexActivation(nextByFactID, nextByRevision, *current)
		}
		if nextBucket.first != nil || len(nextBucket.overflow) > 0 {
			nextActivations[identityKey] = nextBucket
		}
	}

	for _, key := range a.pending {
		if _, ok := removed[key]; ok {
			continue
		}
		current, ok := activationFromBuckets(nextActivations, key)
		if !ok || current.status != activationStatusPending {
			continue
		}
		nextPending = append(nextPending, key)
	}

	a.activations = nextActivations
	a.byFactID = nextByFactID
	a.byRevision = nextByRevision
	a.pending = nextPending

	return changes
}

func (a *agenda) next() (activation, bool) {
	if a == nil {
		return activation{}, false
	}
	for len(a.pending) > 0 {
		key := a.pending[0]
		a.pending = a.pending[1:]

		current, ok := a.activationByKeyPtr(key)
		if !ok || current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusConsumed
		return *current, true
	}
	return activation{}, false
}

func (a *agenda) clear() []agendaChange {
	if a == nil {
		return nil
	}
	changes := make([]agendaChange, 0, len(a.pending))
	for _, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
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
	current, ok := a.activationByKeyPtr(key)
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
		if current, ok := a.activationByKeyPtr(key); ok && current.status == activationStatusPending {
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
		if current, ok := a.activationByKeyPtr(key); ok {
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
		if current, ok := a.activationByKeyPtr(key); ok {
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

func (a *agenda) activationForCandidate(candidate matchCandidate) (*activation, activationKey, bool) {
	if a == nil {
		return nil, activationKey{}, false
	}
	bucket := a.activations[candidate.identity.key]
	if current := bucket.first; current != nil && activationMatchesCandidate(current, candidate) {
		return current, activationKey{identity: candidate.identity.key}, true
	}
	for i, current := range bucket.overflow {
		if current != nil && activationMatchesCandidate(current, candidate) {
			return current, activationKey{identity: candidate.identity.key, index: i + 1}, true
		}
	}
	return nil, activationKey{}, false
}

func activationMatchesCandidate(current *activation, candidate matchCandidate) bool {
	if current == nil {
		return false
	}
	if current.ruleID != candidate.ruleID || current.ruleRevisionID != candidate.ruleRevisionID {
		return false
	}
	return candidateIdentityEqual(current.identity, current.factIDs, current.factVersions, candidate.identity, candidate.factIDs, candidate.factVersions)
}

func (a *agenda) storeActivation(act *activation) activationKey {
	bucket := a.activations[act.identity.key]
	key := activationKey{
		identity: act.identity.key,
	}
	if bucket.first == nil {
		bucket.first = act
	} else {
		key.index = len(bucket.overflow) + 1
		bucket.overflow = append(bucket.overflow, act)
	}
	act.key = key
	act.id = activationIDForKey(key)
	a.activations[act.identity.key] = bucket
	return key
}

func (a *agenda) activationByKeyPtr(key activationKey) (*activation, bool) {
	if a == nil {
		return nil, false
	}
	return activationFromBuckets(a.activations, key)
}

func activationFromBuckets(buckets map[candidateIdentityKey]activationBucket, key activationKey) (*activation, bool) {
	bucket := buckets[key.identity]
	if key.index == 0 {
		if bucket.first == nil {
			return nil, false
		}
		return bucket.first, true
	}
	overflowIndex := key.index - 1
	if overflowIndex < 0 || overflowIndex >= len(bucket.overflow) {
		return nil, false
	}
	current := bucket.overflow[overflowIndex]
	if current == nil {
		return nil, false
	}
	return current, true
}

func activationIDForKey(key activationKey) ActivationID {
	if key.identity == (candidateIdentityKey{}) {
		return ""
	}
	var b strings.Builder
	b.Grow(64)
	b.WriteString("activation:v1:")
	writeUintToBuilder(&b, key.identity.scopeHash)
	b.WriteByte(':')
	writeUintToBuilder(&b, key.identity.hash)
	b.WriteByte(':')
	writeUintToBuilder(&b, uint64(key.index))
	return ActivationID(b.String())
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
