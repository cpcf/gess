package gess

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

type activationKey struct {
	fingerprint activationFingerprint
	ordinal     uint64
}

type activationFingerprint uint64

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
	activations map[activationFingerprint]activationBucket
	pending     []activationKey
	byFactID    map[FactID][]activationKey
	byRevision  map[RuleRevisionID][]activationKey
	nextOrdinal uint64

	reconcileSeen        map[activationKey]struct{}
	reconcileNextPending []activationKey
	reconcileChanges     []agendaChange
	reconcileActivated   []agendaChange

	deltaRemovedKeys map[activationKey]struct{}
	deltaNextPending []activationKey
	deltaChanges     []agendaChange
	deltaActivated   []agendaChange
}

func newAgenda() *agenda {
	return &agenda{
		activations: make(map[activationFingerprint]activationBucket),
		byFactID:    make(map[FactID][]activationKey),
		byRevision:  make(map[RuleRevisionID][]activationKey),
	}
}

func (a *agenda) reset() {
	if a == nil {
		return
	}
	if a.activations == nil {
		a.activations = make(map[activationFingerprint]activationBucket)
	} else {
		clear(a.activations)
	}
	a.pending = a.pending[:0]
	if a.byFactID == nil {
		a.byFactID = make(map[FactID][]activationKey)
	} else {
		clear(a.byFactID)
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID][]activationKey)
	} else {
		clear(a.byRevision)
	}
	a.nextOrdinal = 0
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

	seen := a.reconcileSeen
	if seen == nil {
		seen = make(map[activationKey]struct{}, len(a.pending))
	} else {
		clear(seen)
	}
	nextPending := a.reconcileNextPending[:0]
	changes := a.reconcileChanges[:0]
	activated := a.reconcileActivated[:0]
	oldPending := a.pending

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

			created := activationFromCandidate(rule, candidate)

			key = a.storeActivation(&created)
			indexActivation(a.byFactID, a.byRevision, created)

			if _, seenBefore := seen[key]; !seenBefore {
				seen[key] = struct{}{}
				nextPending = append(nextPending, key)
			}
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: created.clone(),
			})
		}
	}

	for _, key := range oldPending {
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

	a.reconcileSeen = seen
	a.reconcileNextPending = oldPending[:0]
	a.reconcileChanges = changes[:0]
	a.reconcileActivated = activated[:0]
	a.pending = nextPending
	return append([]agendaChange(nil), changes...), nil
}

func (a *agenda) applyCandidateDeltas(ctx context.Context, revision *Ruleset, removed []matchCandidate, added []matchCandidate) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	removedKeys := a.deltaRemovedKeys
	if removedKeys == nil {
		removedKeys = make(map[activationKey]struct{}, len(removed))
	} else {
		clear(removedKeys)
	}
	for _, candidate := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		existing, key, ok := a.activationForCandidate(candidate)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		removedKeys[key] = struct{}{}
	}

	changes := a.deltaChanges[:0]
	if len(removedKeys) > 0 {
		nextPending := a.deltaNextPending[:0]
		for _, key := range a.pending {
			existing, ok := a.activationByKeyPtr(key)
			if !ok || existing.status != activationStatusPending {
				continue
			}
			if _, remove := removedKeys[key]; remove {
				existing.status = activationStatusDeactivated
				changes = append(changes, agendaChange{
					kind:       agendaChangeDeactivated,
					activation: existing.clone(),
				})
				continue
			}
			nextPending = append(nextPending, key)
		}
		a.deltaNextPending = a.pending[:0]
		a.pending = nextPending
	}

	activated := a.deltaActivated[:0]
	sort.SliceStable(added, func(i, j int) bool {
		return agendaDeltaCandidateLess(revision, added[i], added[j])
	})
	for _, candidate := range added {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rule, ok := revision.rulesByRevisionID[candidate.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, candidate.ruleRevisionID)
		}
		if candidate.ruleID != rule.id {
			return nil, fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, candidate.ruleRevisionID)
		}
		if _, _, ok := a.activationForCandidate(candidate); ok {
			continue
		}

		created := activationFromCandidate(rule, candidate)

		key := a.storeActivation(&created)
		indexActivation(a.byFactID, a.byRevision, created)
		a.pending = append(a.pending, key)
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: created.clone(),
		})
	}
	changes = append(changes, activated...)

	sort.SliceStable(a.pending, func(i, j int) bool {
		left, _ := a.activationByKeyPtr(a.pending[i])
		right, _ := a.activationByKeyPtr(a.pending[j])
		return activationLess(left, right)
	})

	a.deltaRemovedKeys = removedKeys
	a.deltaChanges = changes[:0]
	a.deltaActivated = activated[:0]
	return append([]agendaChange(nil), changes...), nil
}

func (a *agenda) applyTerminalTokenDeltas(ctx context.Context, revision *Ruleset, removed []reteTerminalTokenDelta, added []reteTerminalTokenDelta) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	removedKeys := a.deltaRemovedKeys
	if removedKeys == nil {
		removedKeys = make(map[activationKey]struct{}, len(removed))
	} else {
		clear(removedKeys)
	}
	for _, delta := range removed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token == nil {
			continue
		}
		rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
		}
		existing, key, ok := a.activationForTerminalToken(rule, delta.token)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		removedKeys[key] = struct{}{}
	}

	changes := a.deltaChanges[:0]
	if len(removedKeys) > 0 {
		nextPending := a.deltaNextPending[:0]
		for _, key := range a.pending {
			existing, ok := a.activationByKeyPtr(key)
			if !ok || existing.status != activationStatusPending {
				continue
			}
			if _, remove := removedKeys[key]; remove {
				existing.status = activationStatusDeactivated
				changes = append(changes, agendaChange{
					kind:       agendaChangeDeactivated,
					activation: existing.clone(),
				})
				continue
			}
			nextPending = append(nextPending, key)
		}
		a.deltaNextPending = a.pending[:0]
		a.pending = nextPending
	}

	sort.SliceStable(added, func(i, j int) bool {
		return agendaDeltaTerminalTokenLess(revision, added[i], added[j])
	})

	activated := a.deltaActivated[:0]
	var previous reteTerminalTokenDelta
	havePrevious := false
	for _, delta := range added {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token == nil {
			continue
		}
		rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, delta.ruleRevisionID)
		}
		if havePrevious && terminalTokenDeltasEqual(revision, previous, delta) {
			continue
		}
		previous = delta
		havePrevious = true
		if _, _, ok := a.activationForTerminalToken(rule, delta.token); ok {
			continue
		}
		created, err := activationFromTerminalToken(rule, delta.token)
		if err != nil {
			return nil, err
		}
		key := a.storeActivation(&created)
		indexActivation(a.byFactID, a.byRevision, created)
		a.pending = append(a.pending, key)
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: created.clone(),
		})
	}
	changes = append(changes, activated...)

	sort.SliceStable(a.pending, func(i, j int) bool {
		left, _ := a.activationByKeyPtr(a.pending[i])
		right, _ := a.activationByKeyPtr(a.pending[j])
		return activationLess(left, right)
	})

	a.deltaRemovedKeys = removedKeys
	a.deltaChanges = changes[:0]
	a.deltaActivated = activated[:0]
	return append([]agendaChange(nil), changes...), nil
}

func agendaDeltaCandidateLess(revision *Ruleset, left, right matchCandidate) bool {
	if revision != nil {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.declarationOrder != rightRule.declarationOrder {
			return leftRule.declarationOrder < rightRule.declarationOrder
		}
	}
	for i := 0; i < len(left.factIDs) && i < len(right.factIDs); i++ {
		if left.factIDs[i] != right.factIDs[i] {
			return factIDLess(left.factIDs[i], right.factIDs[i])
		}
		if left.factVersions[i] != right.factVersions[i] {
			return left.factVersions[i] < right.factVersions[i]
		}
	}
	if len(left.factIDs) != len(right.factIDs) {
		return len(left.factIDs) < len(right.factIDs)
	}
	if left.ruleID != right.ruleID {
		return left.ruleID < right.ruleID
	}
	return left.ruleRevisionID < right.ruleRevisionID
}

func agendaDeltaTerminalTokenLess(revision *Ruleset, left, right reteTerminalTokenDelta) bool {
	if revision != nil {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.declarationOrder != rightRule.declarationOrder {
			return leftRule.declarationOrder < rightRule.declarationOrder
		}
	}
	compare := compareTerminalTokenFacts(left.token, right.token)
	if compare != 0 {
		return compare < 0
	}
	if revision != nil && left.ruleRevisionID != right.ruleRevisionID {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.id != rightRule.id {
			return leftRule.id < rightRule.id
		}
	}
	return left.ruleRevisionID < right.ruleRevisionID
}

func terminalTokenDeltasEqual(revision *Ruleset, left, right reteTerminalTokenDelta) bool {
	if left.ruleRevisionID != right.ruleRevisionID {
		return false
	}
	if revision != nil {
		leftRule, leftOK := revision.rulesByRevisionID[left.ruleRevisionID]
		rightRule, rightOK := revision.rulesByRevisionID[right.ruleRevisionID]
		if leftOK && rightOK && leftRule.id != rightRule.id {
			return false
		}
	}
	leftIdentity := candidateIdentityForTerminalTokenDelta(revision, left)
	rightIdentity := candidateIdentityForTerminalTokenDelta(revision, right)
	if leftIdentity != rightIdentity {
		return false
	}
	return terminalTokenFactVersionsEqual(left.token, right.token)
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

	nextActivations := make(map[activationFingerprint]activationBucket, len(a.activations))
	nextByFactID := make(map[FactID][]activationKey, len(a.byFactID))
	nextByRevision := make(map[RuleRevisionID][]activationKey, len(a.byRevision))
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
	for _, key := range keys {
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
	for _, key := range keys {
		if current, ok := a.activationByKeyPtr(key); ok {
			out = append(out, current.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return activationLess(&out[i], &out[j])
	})
	return out
}

func indexActivation(byFactID map[FactID][]activationKey, byRevision map[RuleRevisionID][]activationKey, act activation) {
	for _, factID := range act.factIDs {
		keys := byFactID[factID]
		if activationKeyInSlice(keys, act.key) {
			continue
		}
		byFactID[factID] = append(keys, act.key)
	}

	keys := byRevision[act.ruleRevisionID]
	if activationKeyInSlice(keys, act.key) {
		return
	}
	byRevision[act.ruleRevisionID] = append(keys, act.key)
}

func activationKeyInSlice(keys []activationKey, key activationKey) bool {
	return slices.Contains(keys, key)
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
	fingerprint := activationFingerprintForIdentityKey(candidate.identity.key)
	bucket := a.activations[fingerprint]
	if current := bucket.first; current != nil && activationMatchesCandidate(current, candidate) {
		return current, current.key, true
	}
	for _, current := range bucket.overflow {
		if current != nil && activationMatchesCandidate(current, candidate) {
			return current, current.key, true
		}
	}
	return nil, activationKey{}, false
}

func (a *agenda) activationForTerminalToken(rule compiledRule, token *matchToken) (*activation, activationKey, bool) {
	if a == nil {
		return nil, activationKey{}, false
	}
	identity := candidateIdentityForTerminalToken(rule, token)
	fingerprint := activationFingerprintForIdentityKey(identity.key)
	bucket := a.activations[fingerprint]
	if current := bucket.first; current != nil && activationMatchesTerminalToken(current, rule, identity, token) {
		return current, current.key, true
	}
	for _, current := range bucket.overflow {
		if current != nil && activationMatchesTerminalToken(current, rule, identity, token) {
			return current, current.key, true
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

func activationMatchesTerminalToken(current *activation, rule compiledRule, identity candidateIdentity, token *matchToken) bool {
	if current == nil {
		return false
	}
	if current.ruleID != rule.id || current.ruleRevisionID != rule.revisionID {
		return false
	}
	if current.identity.key != identity.key || current.identity.generation != identity.generation || current.identity.count != identity.count {
		return false
	}
	if len(current.factIDs) != tokenSize(token) || len(current.factVersions) != tokenSize(token) {
		return false
	}
	index, ok := activationTokenFactsEqual(current, token, 0)
	return ok && index == len(current.factIDs)
}

func (a *agenda) storeActivation(act *activation) activationKey {
	fingerprint := activationFingerprintForIdentityKey(act.identity.key)
	bucket := a.activations[fingerprint]
	key := activationKey{
		fingerprint: fingerprint,
		ordinal:     a.nextOrdinal,
	}
	a.nextOrdinal++
	publicIndex := activationIdentityIndex(bucket, act.identity.key)
	if bucket.first == nil {
		bucket.first = act
	} else {
		bucket.overflow = append(bucket.overflow, act)
	}
	act.key = key
	act.id = activationIDForIdentityKey(act.identity.key, publicIndex)
	a.activations[fingerprint] = bucket
	return key
}

func (a *agenda) activationByKeyPtr(key activationKey) (*activation, bool) {
	if a == nil {
		return nil, false
	}
	return activationFromBuckets(a.activations, key)
}

func activationFromBuckets(buckets map[activationFingerprint]activationBucket, key activationKey) (*activation, bool) {
	bucket := buckets[key.fingerprint]
	if bucket.first != nil && bucket.first.key == key {
		return bucket.first, true
	}
	for _, current := range bucket.overflow {
		if current != nil && current.key == key {
			return current, true
		}
	}
	return nil, false
}

func activationFingerprintForIdentityKey(key candidateIdentityKey) activationFingerprint {
	return activationFingerprint(key.hash)
}

func activationIdentityIndex(bucket activationBucket, identity candidateIdentityKey) uint64 {
	index := uint64(0)
	if bucket.first != nil && bucket.first.identity.key == identity {
		index++
	}
	for _, current := range bucket.overflow {
		if current != nil && current.identity.key == identity {
			index++
		}
	}
	return index
}

func activationIDForIdentityKey(identity candidateIdentityKey, index uint64) ActivationID {
	if identity == (candidateIdentityKey{}) {
		return ""
	}
	var b strings.Builder
	b.Grow(64)
	b.WriteString("activation:v1:")
	writeUintToBuilder(&b, identity.scopeHash)
	b.WriteByte(':')
	writeUintToBuilder(&b, identity.hash)
	b.WriteByte(':')
	writeUintToBuilder(&b, index)
	return ActivationID(b.String())
}

func cloneBindingTupleEntries(entries []bindingTupleEntry) []bindingTupleEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]bindingTupleEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
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

func activationFromCandidate(rule compiledRule, candidate matchCandidate) activation {
	return activation{
		ruleID:           candidate.ruleID,
		ruleRevisionID:   candidate.ruleRevisionID,
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
}

func activationFromTerminalToken(rule compiledRule, token *matchToken) (activation, error) {
	if token == nil {
		return activation{}, fmt.Errorf("%w: empty token for rule %q", ErrMatcher, rule.name)
	}
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return activation{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}
	entries := make([]bindingTupleEntry, token.size)
	factIDs := make([]FactID, token.size)
	factVersions := make([]FactVersion, token.size)
	path := make([]int, token.pathLen)
	fillMatchToken(entries, factIDs, factVersions, path, 0, 0, token)
	return activation{
		ruleID:           rule.id,
		ruleRevisionID:   rule.revisionID,
		generation:       matchTokenGeneration(token),
		identity:         candidateIdentityForTerminalToken(rule, token),
		bindings:         entries,
		factIDs:          factIDs,
		factVersions:     factVersions,
		path:             path,
		salience:         rule.salience,
		maxRecency:       token.maxRecency,
		aggregateRecency: token.aggregateRecency,
		declarationOrder: rule.declarationOrder,
		status:           activationStatusPending,
	}, nil
}

func candidateIdentityForTerminalToken(rule compiledRule, token *matchToken) candidateIdentity {
	identity := candidateIdentity{
		generation: matchTokenGeneration(token),
		count:      tokenSize(token),
		key: candidateIdentityKey{
			scopeHash: rule.identityScopeHash,
			hash:      candidateIdentityHashFinish(tokenIdentityState(token), tokenSize(token)),
		},
	}
	if identity.key.scopeHash == 0 {
		identity.key.scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
	}
	return identity
}

func candidateIdentityForTerminalTokenDelta(revision *Ruleset, delta reteTerminalTokenDelta) candidateIdentity {
	if revision == nil {
		return candidateIdentity{}
	}
	rule, ok := revision.rulesByRevisionID[delta.ruleRevisionID]
	if !ok {
		return candidateIdentity{}
	}
	return candidateIdentityForTerminalToken(rule, delta.token)
}

func tokenSize(token *matchToken) int {
	if token == nil {
		return 0
	}
	return token.size
}

func tokenIdentityState(token *matchToken) uint64 {
	if token == nil {
		return candidateIdentityHashStart(0)
	}
	return token.identityState
}

func activationTokenFactsEqual(current *activation, token *matchToken, index int) (int, bool) {
	if token == nil {
		return index, true
	}
	next, ok := activationTokenFactsEqual(current, token.parent, index)
	if !ok || next >= len(current.factIDs) || next >= len(current.factVersions) {
		return next, false
	}
	if current.factIDs[next] != token.entry.factID || current.factVersions[next] != token.entry.factVersion {
		return next, false
	}
	return next + 1, true
}

func terminalTokenFactVersionsEqual(left, right *matchToken) bool {
	if tokenSize(left) != tokenSize(right) {
		return false
	}
	return terminalTokenFactVersionsEqualAt(left, right)
}

func terminalTokenFactVersionsEqualAt(left, right *matchToken) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if !terminalTokenFactVersionsEqualAt(left.parent, right.parent) {
		return false
	}
	return left.entry.factID == right.entry.factID && left.entry.factVersion == right.entry.factVersion
}

func compareTerminalTokenFacts(left, right *matchToken) int {
	if left == nil || right == nil {
		switch {
		case left == nil && right == nil:
			return 0
		case left == nil:
			return -1
		default:
			return 1
		}
	}
	if compare := compareTerminalTokenFacts(left.parent, right.parent); compare != 0 {
		return compare
	}
	if left.entry.factID != right.entry.factID {
		if factIDLess(left.entry.factID, right.entry.factID) {
			return -1
		}
		return 1
	}
	if left.entry.factVersion != right.entry.factVersion {
		if left.entry.factVersion < right.entry.factVersion {
			return -1
		}
		return 1
	}
	return 0
}

func cloneFactVersions(versions []FactVersion) []FactVersion {
	if len(versions) == 0 {
		return nil
	}
	out := make([]FactVersion, len(versions))
	copy(out, versions)
	return out
}
