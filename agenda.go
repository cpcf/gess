package gess

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
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
	publicOrdinal    uint64
	ruleID           RuleID
	ruleRevisionID   RuleRevisionID
	generation       Generation
	identity         candidateIdentity
	bindings         []bindingTupleEntry
	factIDs          []FactID
	factVersions     []FactVersion
	token            tokenRef
	path             []int
	salience         int
	maxRecency       Recency
	aggregateRecency Recency
	declarationOrder int
	status           activationStatus
}

func (a activation) mutationOrigin() mutationOrigin {
	return mutationOrigin{
		ActivationID:          a.id,
		RuleID:                a.ruleID,
		RuleRevisionID:        a.ruleRevisionID,
		activationIdentityKey: a.identity.key,
		activationOrdinal:     a.publicOrdinal,
	}
}

func (a activation) activationID() ActivationID {
	if !a.id.IsZero() {
		return a.id
	}
	return activationIDForIdentityKey(a.identity.key, a.publicOrdinal)
}

func (a *activation) ensureActivationID() ActivationID {
	if a == nil {
		return ""
	}
	if a.id.IsZero() {
		a.id = activationIDForIdentityKey(a.identity.key, a.publicOrdinal)
	}
	return a.id
}

func (a activation) clone() activation {
	out := a
	out.id = a.activationID()
	out.bindings = cloneBindingTupleEntries(a.bindings)
	out.factIDs = cloneActivationFactIDs(&a)
	out.factVersions = cloneActivationFactVersions(&a)
	out.token = tokenRef{}
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
		ActivationID:   c.activation.activationID(),
		FactIDs:        cloneActivationFactIDs(&c.activation),
	}
}

func (a *agenda) publicActivation(act *activation) activation {
	if act == nil {
		return activation{}
	}
	out := *act
	out.id = act.ensureActivationID()
	out.factIDs = cloneActivationFactIDs(act)
	out.factVersions = cloneActivationFactVersions(act)
	out.token = tokenRef{}
	if a == nil || a.revision == nil || len(out.factIDs) == 0 {
		out.bindings = nil
		out.path = nil
		return out
	}
	rule, ok := a.revision.rulesByRevisionID[out.ruleRevisionID]
	if !ok {
		out.bindings = nil
		out.path = nil
		return out
	}
	out.bindings = activationBindingTupleEntries(rule, out.factIDs, out.factVersions, true)
	out.path = activationPathForRule(rule)
	return out
}

func (a *agenda) compactChangeActivation(act *activation) activation {
	if act == nil {
		return activation{}
	}
	out := *act
	out.bindings = nil
	if act.token.isZero() {
		out.factIDs = cloneFactIDs(act.factIDs)
	} else {
		out.factIDs = nil
	}
	out.factVersions = nil
	out.path = nil
	return out
}

func activationBindingTupleEntries(rule compiledRule, factIDs []FactID, factVersions []FactVersion, includePath bool) []bindingTupleEntry {
	if len(factIDs) == 0 || len(factIDs) != len(factVersions) || len(rule.conditions) == 0 || len(rule.conditionPlans) == 0 {
		return nil
	}
	n := min(len(rule.conditionPlans), min(len(rule.conditions), len(factIDs)))
	entries := make([]bindingTupleEntry, n)
	for i := range n {
		condition := rule.conditions[i]
		plan := rule.conditionPlans[i]
		entries[i] = bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    i,
			conditionOrder: condition.order,
			conditionID:    condition.id,
			factID:         factIDs[i],
			factVersion:    factVersions[i],
		}
		if includePath {
			entries[i].conditionPath = cloneIntPath(plan.path)
		}
	}
	return entries
}

func activationBindingTupleEntriesForActivation(rule compiledRule, act *activation, includePath bool) []bindingTupleEntry {
	if act == nil || len(rule.conditions) == 0 || len(rule.conditionPlans) == 0 {
		return nil
	}
	count := activationFactCount(act)
	if count == 0 || count != activationFactVersionCount(act) {
		return nil
	}
	n := min(len(rule.conditionPlans), min(len(rule.conditions), count))
	if n != count {
		return nil
	}
	entries := make([]bindingTupleEntry, n)
	if !act.token.isZero() {
		if fillActivationBindingTupleEntriesFromTokenRef(entries, rule, act.token, includePath, 0) == n {
			return entries
		}
	}
	for i := range n {
		condition := rule.conditions[i]
		plan := rule.conditionPlans[i]
		entries[i] = bindingTupleEntry{
			binding:        condition.binding,
			bindingSlot:    i,
			conditionOrder: condition.order,
			conditionID:    condition.id,
			factID:         act.factIDs[i],
			factVersion:    act.factVersions[i],
		}
		if includePath {
			entries[i].conditionPath = cloneIntPath(plan.path)
		}
	}
	return entries
}

func fillActivationBindingTupleEntriesFromTokenRef(entries []bindingTupleEntry, rule compiledRule, token tokenRef, includePath bool, index int) int {
	if token.isZero() {
		return index
	}
	row, ok := token.resolve()
	if !ok {
		return index
	}
	index = fillActivationBindingTupleEntriesFromTokenRef(entries, rule, token.parent(), includePath, index)
	if index >= len(entries) {
		return index
	}
	condition := rule.conditions[index]
	plan := rule.conditionPlans[index]
	entries[index] = bindingTupleEntry{
		binding:        condition.binding,
		bindingSlot:    index,
		conditionOrder: condition.order,
		conditionID:    condition.id,
		factID:         row.match.fact.ID(),
		factVersion:    row.match.fact.Version(),
	}
	if includePath {
		entries[index].conditionPath = cloneIntPath(plan.path)
	}
	return index + 1
}

func activationPathForRule(rule compiledRule) []int {
	if len(rule.conditionPlans) == 0 {
		return nil
	}
	pathLen := 0
	for _, plan := range rule.conditionPlans {
		pathLen += len(plan.path)
	}
	path := make([]int, 0, pathLen)
	for _, plan := range rule.conditionPlans {
		path = append(path, plan.path...)
	}
	return path
}

type agenda struct {
	activations         map[activationFingerprint]activationBucket
	pending             []activationKey
	byFactID            map[FactID][]activationKey
	byRevision          map[RuleRevisionID][]activationKey
	nextOrdinal         uint64
	revision            *Ruleset
	propagationCounters *propagationCounterLedger

	reconcileSeen        map[activationKey]struct{}
	reconcileNextPending []activationKey
	reconcileChanges     []agendaChange
	reconcileActivated   []agendaChange

	deltaRemovedKeys map[activationKey]struct{}
	deltaNextPending []activationKey
	deltaChanges     []agendaChange
	deltaActivated   []agendaChange

	purgeActivations map[activationFingerprint]activationBucket
	purgeNextPending []activationKey
	purgeChanges     []agendaChange
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
	a.purgeActivations = nil
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
	a.revision = revision

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

			if _, seenBefore := seen[key]; !seenBefore {
				seen[key] = struct{}{}
				nextPending = append(nextPending, key)
			}
			activated = append(activated, agendaChange{
				kind:       agendaChangeActivated,
				activation: a.compactChangeActivation(&created),
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
			activation: a.compactChangeActivation(existing),
		})
	}

	changes = append(changes, activated...)

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	a.sortActivationKeys(nextPending)

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
	a.revision = revision

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
					activation: a.compactChangeActivation(existing),
				})
				continue
			}
			nextPending = append(nextPending, key)
		}
		a.deltaNextPending = a.pending[:0]
		a.pending = nextPending
	}

	activated := a.deltaActivated[:0]
	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	sortMatchCandidates(revision, added)
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
		a.pending = a.insertActivationKeySorted(a.pending, key, &created)
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: a.compactChangeActivation(&created),
		})
	}
	changes = append(changes, activated...)

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
	a.revision = revision

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
		if delta.token.isZero() {
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
					activation: a.compactChangeActivation(existing),
				})
				continue
			}
			nextPending = append(nextPending, key)
		}
		a.deltaNextPending = a.pending[:0]
		a.pending = nextPending
	}

	if len(added) > 1 {
		if a.propagationCounters != nil {
			a.propagationCounters.recordAgendaSort()
		}
		sortTerminalTokenDeltas(revision, added)
	}

	activated := a.deltaActivated[:0]
	var previous reteTerminalTokenDelta
	havePrevious := false
	for _, delta := range added {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token.isZero() {
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
		a.pending = a.insertActivationKeySorted(a.pending, key, &created)
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: a.compactChangeActivation(&created),
		})
	}
	changes = append(changes, activated...)

	a.deltaRemovedKeys = removedKeys
	a.deltaChanges = changes[:0]
	a.deltaActivated = activated[:0]
	return append([]agendaChange(nil), changes...), nil
}

func (a *agenda) reconcileTerminalTokens(ctx context.Context, revision *Ruleset, deltas []reteTerminalTokenDelta) ([]agendaChange, error) {
	if a == nil || revision == nil {
		return nil, ErrInvalidRuleset
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.revision = revision

	seen := a.reconcileSeen
	if seen == nil {
		seen = make(map[activationKey]struct{}, max(len(a.pending), len(deltas)))
	} else {
		clear(seen)
	}
	nextPending := a.reconcileNextPending[:0]
	changes := a.reconcileChanges[:0]
	activated := a.reconcileActivated[:0]

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	sortTerminalTokenDeltas(revision, deltas)

	var previous reteTerminalTokenDelta
	havePrevious := false
	for _, delta := range deltas {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if delta.token.isZero() {
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

		existing, key, ok := a.activationForTerminalToken(rule, delta.token)
		if ok {
			if existing.status == activationStatusPending {
				if _, seenBefore := seen[key]; !seenBefore {
					seen[key] = struct{}{}
					nextPending = append(nextPending, key)
				}
			}
			continue
		}

		created, err := activationFromTerminalToken(rule, delta.token)
		if err != nil {
			return nil, err
		}
		key = a.storeActivation(&created)
		if _, seenBefore := seen[key]; !seenBefore {
			seen[key] = struct{}{}
			nextPending = append(nextPending, key)
		}
		activated = append(activated, agendaChange{
			kind:       agendaChangeActivated,
			activation: a.compactChangeActivation(&created),
		})
	}

	for _, key := range a.pending {
		existing, ok := a.activationByKeyPtr(key)
		if !ok || existing.status != activationStatusPending {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		existing.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(existing),
		})
	}

	changes = append(changes, activated...)

	if a.propagationCounters != nil {
		a.propagationCounters.recordAgendaSort()
	}
	a.sortActivationKeys(nextPending)

	a.reconcileSeen = seen
	a.reconcileNextPending = a.pending[:0]
	a.reconcileChanges = changes[:0]
	a.reconcileActivated = activated[:0]
	a.pending = nextPending
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

func sortMatchCandidates(revision *Ruleset, candidates []matchCandidate) {
	slices.SortStableFunc(candidates, func(left, right matchCandidate) int {
		return compareLess(
			agendaDeltaCandidateLess(revision, left, right),
			agendaDeltaCandidateLess(revision, right, left),
		)
	})
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

func sortTerminalTokenDeltas(revision *Ruleset, deltas []reteTerminalTokenDelta) {
	slices.SortStableFunc(deltas, func(left, right reteTerminalTokenDelta) int {
		return compareLess(
			agendaDeltaTerminalTokenLess(revision, left, right),
			agendaDeltaTerminalTokenLess(revision, right, left),
		)
	})
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

func compareLess(leftLess, rightLess bool) int {
	switch {
	case leftLess:
		return -1
	case rightLess:
		return 1
	default:
		return 0
	}
}

func (a *agenda) purgeRuleRevisions(revisionIDs map[RuleRevisionID]struct{}) []agendaChange {
	if a == nil || len(revisionIDs) == 0 {
		return nil
	}

	changes := a.purgeChanges[:0]
	for _, key := range a.pending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current == nil {
			continue
		}
		if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
			continue
		}
		if current.status != activationStatusPending {
			continue
		}
		current.status = activationStatusDeactivated
		changes = append(changes, agendaChange{
			kind:       agendaChangeDeactivated,
			activation: a.compactChangeActivation(current),
		})
	}

	nextActivations := a.purgeActivations
	if nextActivations == nil {
		nextActivations = make(map[activationFingerprint]activationBucket, len(a.activations))
	} else {
		clear(nextActivations)
	}
	a.resetIndexesForRebuild()
	oldPending := a.pending
	oldActivationCount := len(a.activations)

	for identityKey, bucket := range a.activations {
		nextBucket := activationBucket{}
		overflow := bucket.overflow[:0]
		if current := bucket.first; current != nil {
			if _, ok := revisionIDs[current.ruleRevisionID]; !ok {
				nextBucket.first = current
				a.indexActivation(*current)
			}
		}
		for _, current := range bucket.overflow {
			if current == nil {
				continue
			}
			if _, ok := revisionIDs[current.ruleRevisionID]; ok {
				continue
			}
			if nextBucket.first == nil {
				nextBucket.first = current
			} else {
				overflow = append(overflow, current)
			}
			a.indexActivation(*current)
		}
		if len(bucket.overflow) > 0 {
			clear(bucket.overflow[len(overflow):])
		}
		if len(overflow) > 0 {
			nextBucket.overflow = overflow
		}
		if nextBucket.first != nil {
			nextActivations[identityKey] = nextBucket
		}
	}

	oldActivations := a.activations
	a.activations = nextActivations
	if len(nextActivations) == 0 || len(nextActivations)*4 < oldActivationCount {
		a.purgeActivations = nil
	} else {
		clear(oldActivations)
		a.purgeActivations = oldActivations
	}

	nextPending := a.purgeNextPending[:0]
	for _, key := range oldPending {
		current, ok := a.activationByKeyPtr(key)
		if !ok || current.status != activationStatusPending {
			continue
		}
		nextPending = append(nextPending, key)
	}

	a.pending = nextPending
	a.purgeNextPending = oldPending[:0]
	a.pruneEmptyIndexes()
	out := append([]agendaChange(nil), changes...)
	clear(changes)
	a.purgeChanges = changes[:0]

	return out
}

func (a *agenda) next() (activation, bool) {
	return a.nextActivation(true)
}

func (a *agenda) nextInternal() (activation, bool) {
	return a.nextActivation(false)
}

func (a *agenda) nextActivation(materializeID bool) (activation, bool) {
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
		out := *current
		if materializeID {
			out.id = current.ensureActivationID()
		}
		if current.token.isZero() {
			out.factIDs = cloneFactIDs(current.factIDs)
			out.factVersions = cloneFactVersions(current.factVersions)
		} else {
			out.factIDs = nil
			out.factVersions = nil
			out.bindings = nil
			out.path = nil
		}
		return out, true
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
			activation: a.compactChangeActivation(current),
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
	return a.publicActivation(current), true
}

func (a *agenda) pendingActivations() []activation {
	if a == nil || len(a.pending) == 0 {
		return nil
	}
	out := make([]activation, 0, len(a.pending))
	for _, key := range a.pending {
		if current, ok := a.activationByKeyPtr(key); ok && current.status == activationStatusPending {
			out = append(out, a.publicActivation(current))
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
			out = append(out, a.publicActivation(current))
		}
	}
	sortActivations(out)
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
			out = append(out, a.publicActivation(current))
		}
	}
	sortActivations(out)
	return out
}

func (a *agenda) rebuildIndexes() {
	if a == nil {
		return
	}
	a.resetIndexesForRebuild()
	for _, bucket := range a.activations {
		if current := bucket.first; current != nil {
			a.indexActivation(*current)
		}
		for _, current := range bucket.overflow {
			if current != nil {
				a.indexActivation(*current)
			}
		}
	}
	a.pruneEmptyIndexes()
}

func (a *agenda) resetIndexesForRebuild() {
	if a.byFactID == nil {
		a.byFactID = make(map[FactID][]activationKey)
	} else {
		resetActivationIndex(a.byFactID)
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID][]activationKey)
	} else {
		resetActivationIndex(a.byRevision)
	}
}

func (a *agenda) pruneEmptyIndexes() {
	if a == nil {
		return
	}
	pruneEmptyActivationIndex(a.byFactID)
	pruneEmptyActivationIndex(a.byRevision)
}

func (a *agenda) indexActivation(act activation) {
	if a == nil {
		return
	}
	if a.byFactID == nil {
		a.byFactID = make(map[FactID][]activationKey)
	}
	if a.byRevision == nil {
		a.byRevision = make(map[RuleRevisionID][]activationKey)
	}

	if !act.token.isZero() {
		a.indexActivationTokenFacts(act.key, act.token)
	} else {
		for i, factID := range act.factIDs {
			if factIDSeenBefore(act.factIDs[:i], factID) {
				continue
			}
			a.byFactID[factID] = append(a.byFactID[factID], act.key)
		}
	}

	a.byRevision[act.ruleRevisionID] = append(a.byRevision[act.ruleRevisionID], act.key)
}

func (a *agenda) indexActivationTokenFacts(key activationKey, token tokenRef) {
	if a == nil || token.isZero() {
		return
	}
	row, ok := token.resolve()
	if !ok {
		return
	}
	a.indexActivationTokenFacts(key, token.parent())
	factID := row.match.fact.ID()
	if tokenRefContainsFactID(token.parent(), factID) {
		return
	}
	a.byFactID[factID] = append(a.byFactID[factID], key)
}

func tokenRefContainsFactID(token tokenRef, id FactID) bool {
	return token.containsFact(id)
}

func resetActivationIndex[K comparable](index map[K][]activationKey) {
	for key, keys := range index {
		index[key] = keys[:0]
	}
}

func pruneEmptyActivationIndex[K comparable](index map[K][]activationKey) {
	for key, keys := range index {
		if len(keys) == 0 {
			delete(index, key)
		}
	}
}

func factIDSeenBefore(ids []FactID, id FactID) bool {
	return slices.Contains(ids, id)
}

func (a *agenda) sortActivationKeys(keys []activationKey) {
	slices.SortStableFunc(keys, func(leftKey, rightKey activationKey) int {
		left, _ := a.activationByKeyPtr(leftKey)
		right, _ := a.activationByKeyPtr(rightKey)
		return activationCompare(left, right)
	})
}

func (a *agenda) insertActivationKeySorted(keys []activationKey, key activationKey, act *activation) []activationKey {
	if a == nil || act == nil || len(keys) == 0 {
		return append(keys, key)
	}
	index := sort.Search(len(keys), func(i int) bool {
		existing, _ := a.activationByKeyPtr(keys[i])
		return activationLess(act, existing)
	})
	keys = append(keys, activationKey{})
	copy(keys[index+1:], keys[index:])
	keys[index] = key
	return keys
}

func sortActivations(activations []activation) {
	slices.SortStableFunc(activations, func(left, right activation) int {
		return activationCompare(&left, &right)
	})
}

func activationCompare(left, right *activation) int {
	return compareLess(activationLess(left, right), activationLess(right, left))
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
	if !left.id.IsZero() || !right.id.IsZero() {
		return left.activationID() < right.activationID()
	}
	if activationIDSegmentLess(left.identity.key.scopeHash, right.identity.key.scopeHash) {
		return true
	}
	if left.identity.key.scopeHash != right.identity.key.scopeHash {
		return false
	}
	if activationIDSegmentLess(left.identity.key.hash, right.identity.key.hash) {
		return true
	}
	if left.identity.key.hash != right.identity.key.hash {
		return false
	}
	if activationIDFinalSegmentLess(left.publicOrdinal, right.publicOrdinal) {
		return true
	}
	if left.publicOrdinal != right.publicOrdinal {
		return false
	}
	return false
}

func activationIDSegmentLess(left, right uint64) bool {
	return activationIDDecimalLess(left, right, true)
}

func activationIDFinalSegmentLess(left, right uint64) bool {
	return activationIDDecimalLess(left, right, false)
}

func activationIDDecimalLess(left, right uint64, followedByColon bool) bool {
	var leftBuf [20]byte
	var rightBuf [20]byte
	leftBytes := strconv.AppendUint(leftBuf[:0], left, 10)
	rightBytes := strconv.AppendUint(rightBuf[:0], right, 10)
	for i := 0; i < len(leftBytes) && i < len(rightBytes); i++ {
		if leftBytes[i] != rightBytes[i] {
			return leftBytes[i] < rightBytes[i]
		}
	}
	if followedByColon {
		return len(leftBytes) > len(rightBytes)
	}
	return len(leftBytes) < len(rightBytes)
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

func (a *agenda) activationForTerminalToken(rule compiledRule, token tokenRef) (*activation, activationKey, bool) {
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
	if current.identity.key != candidate.identity.key || current.identity.generation != candidate.identity.generation || current.identity.count != candidate.identity.count {
		return false
	}
	if !current.token.isZero() {
		return matchTokenFactsEqualSlices(current.token, candidate.factIDs, candidate.factVersions)
	}
	return candidateIdentityEqual(current.identity, current.factIDs, current.factVersions, candidate.identity, candidate.factIDs, candidate.factVersions)
}

func activationMatchesTerminalToken(current *activation, rule compiledRule, identity candidateIdentity, token tokenRef) bool {
	if current == nil {
		return false
	}
	if current.ruleID != rule.id || current.ruleRevisionID != rule.revisionID {
		return false
	}
	if current.identity.key != identity.key || current.identity.generation != identity.generation || current.identity.count != identity.count {
		return false
	}
	if !current.token.isZero() {
		return terminalTokenFactVersionsEqual(current.token, token)
	}
	return matchTokenFactsEqualSlices(token, current.factIDs, current.factVersions)
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
	act.publicOrdinal = publicIndex
	if bucket.first == nil {
		bucket.first = act
	} else {
		bucket.overflow = append(bucket.overflow, act)
	}
	act.key = key
	a.activations[fingerprint] = bucket
	a.indexActivation(*act)
	if a.propagationCounters != nil {
		a.propagationCounters.recordActivationStored()
	}
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

func cloneActivationFactIDs(act *activation) []FactID {
	if act == nil {
		return nil
	}
	if act.token.isZero() {
		return cloneFactIDs(act.factIDs)
	}
	out := make([]FactID, act.token.size())
	fillTokenRefFactIDs(out, 0, act.token)
	return out
}

func cloneActivationFactVersions(act *activation) []FactVersion {
	if act == nil {
		return nil
	}
	if act.token.isZero() {
		return cloneFactVersions(act.factVersions)
	}
	out := make([]FactVersion, act.token.size())
	fillTokenRefFactVersions(out, 0, act.token)
	return out
}

func activationFactCount(act *activation) int {
	if act == nil {
		return 0
	}
	if !act.token.isZero() {
		return tokenRefSize(act.token)
	}
	return len(act.factIDs)
}

func activationFactVersionCount(act *activation) int {
	if act == nil {
		return 0
	}
	if !act.token.isZero() {
		return tokenRefSize(act.token)
	}
	return len(act.factVersions)
}

func fillTokenRefFactIDs(factIDs []FactID, index int, token tokenRef) int {
	if token.isZero() {
		return index
	}
	row, ok := token.resolve()
	if !ok {
		return index
	}
	index = fillTokenRefFactIDs(factIDs, index, token.parent())
	factIDs[index] = row.match.fact.ID()
	return index + 1
}

func fillTokenRefFactVersions(factVersions []FactVersion, index int, token tokenRef) int {
	if token.isZero() {
		return index
	}
	row, ok := token.resolve()
	if !ok {
		return index
	}
	index = fillTokenRefFactVersions(factVersions, index, token.parent())
	factVersions[index] = row.match.fact.Version()
	return index + 1
}

func activationFromCandidate(rule compiledRule, candidate matchCandidate) activation {
	return activation{
		ruleID:           candidate.ruleID,
		ruleRevisionID:   candidate.ruleRevisionID,
		generation:       candidate.generation,
		identity:         candidate.identity,
		factIDs:          cloneFactIDs(candidate.factIDs),
		factVersions:     cloneFactVersions(candidate.factVersions),
		salience:         rule.salience,
		maxRecency:       candidate.maxRecency,
		aggregateRecency: candidate.aggregateRecency,
		declarationOrder: rule.declarationOrder,
		status:           activationStatusPending,
	}
}

func activationFromTerminalToken(rule compiledRule, token tokenRef) (activation, error) {
	if token.isZero() {
		return activation{}, fmt.Errorf("%w: empty token for rule %q", ErrMatcher, rule.name)
	}
	if len(rule.conditionPlans) == 0 || len(rule.conditions) == 0 {
		return activation{}, fmt.Errorf("%w: malformed compiled rule %q", ErrMatcher, rule.name)
	}
	return activation{
		ruleID:           rule.id,
		ruleRevisionID:   rule.revisionID,
		generation:       tokenRefGeneration(token),
		identity:         candidateIdentityForTerminalToken(rule, token),
		token:            token,
		salience:         rule.salience,
		maxRecency:       token.maxRecency(),
		aggregateRecency: token.aggregateRecency(),
		declarationOrder: rule.declarationOrder,
		status:           activationStatusPending,
	}, nil
}

func candidateIdentityForTerminalToken(rule compiledRule, token tokenRef) candidateIdentity {
	identityState, count, ok := terminalTokenIdentityStateForRule(rule, token, candidateIdentityHashStart(tokenRefGeneration(token)), 0)
	if ok {
		identity := candidateIdentity{
			generation: tokenRefGeneration(token),
			count:      count,
			key: candidateIdentityKey{
				scopeHash: rule.identityScopeHash,
				hash:      candidateIdentityHashFinish(identityState, count),
			},
		}
		if identity.key.scopeHash == 0 {
			identity.key.scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
		}
		return identity
	}
	identity := candidateIdentity{
		generation: tokenRefGeneration(token),
		count:      tokenRefSize(token),
		key: candidateIdentityKey{
			scopeHash: rule.identityScopeHash,
			hash:      candidateIdentityHashFinish(tokenRefIdentityState(token), tokenRefSize(token)),
		},
	}
	if identity.key.scopeHash == 0 {
		identity.key.scopeHash = candidateIdentityScopeHash(rule.id, rule.revisionID)
	}
	return identity
}

func terminalTokenIdentityStateForRule(rule compiledRule, token tokenRef, state uint64, count int) (uint64, int, bool) {
	if token.isZero() {
		return state, count, true
	}
	row, ok := token.resolve()
	if !ok {
		return state, count, false
	}
	state, count, ok = terminalTokenIdentityStateForRule(rule, token.parent(), state, count)
	if !ok {
		return state, count, false
	}
	entry, err := bindingTupleEntryForMatch(rule, row.match)
	if err != nil {
		return state, count, false
	}
	return candidateIdentityHashStep(state, entry), count + 1, true
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

func tokenRefSize(token tokenRef) int {
	if token.isZero() {
		return 0
	}
	return token.size()
}

func tokenRefIdentityState(token tokenRef) uint64 {
	if token.isZero() {
		return candidateIdentityHashStart(0)
	}
	return token.identityState()
}

func tokenRefGeneration(token tokenRef) Generation {
	if token.isZero() {
		return 0
	}
	return token.generation()
}

func matchTokenFactsEqualSlices(token tokenRef, factIDs []FactID, factVersions []FactVersion) bool {
	if tokenRefSize(token) != len(factIDs) || len(factIDs) != len(factVersions) {
		return false
	}
	index, ok := matchTokenFactsEqualSlicesAt(token, factIDs, factVersions, 0)
	return ok && index == len(factIDs)
}

func matchTokenFactsEqualSlicesAt(token tokenRef, factIDs []FactID, factVersions []FactVersion, index int) (int, bool) {
	if token.isZero() {
		return index, true
	}
	row, ok := token.resolve()
	if !ok {
		return index, false
	}
	next, ok := matchTokenFactsEqualSlicesAt(token.parent(), factIDs, factVersions, index)
	if !ok || next >= len(factIDs) || next >= len(factVersions) {
		return next, false
	}
	if factIDs[next] != row.match.fact.ID() || factVersions[next] != row.match.fact.Version() {
		return next, false
	}
	return next + 1, true
}

func terminalTokenFactVersionsEqual(left, right tokenRef) bool {
	if tokenRefSize(left) != tokenRefSize(right) {
		return false
	}
	return terminalTokenFactVersionsEqualAt(left, right)
}

func terminalTokenFactVersionsEqualAt(left, right tokenRef) bool {
	if left.isZero() || right.isZero() {
		return left.isZero() && right.isZero()
	}
	leftRow, leftOK := left.resolve()
	rightRow, rightOK := right.resolve()
	if !leftOK || !rightOK {
		return false
	}
	if !terminalTokenFactVersionsEqualAt(left.parent(), right.parent()) {
		return false
	}
	return leftRow.match.fact.ID() == rightRow.match.fact.ID() && leftRow.match.fact.Version() == rightRow.match.fact.Version()
}

func compareTerminalTokenFacts(left, right tokenRef) int {
	if left.isZero() || right.isZero() {
		switch {
		case left.isZero() && right.isZero():
			return 0
		case left.isZero():
			return -1
		default:
			return 1
		}
	}
	leftRow, leftOK := left.resolve()
	rightRow, rightOK := right.resolve()
	if !leftOK || !rightOK {
		return 0
	}
	if compare := compareTerminalTokenFacts(left.parent(), right.parent()); compare != 0 {
		return compare
	}
	if leftRow.match.fact.ID() != rightRow.match.fact.ID() {
		if factIDLess(leftRow.match.fact.ID(), rightRow.match.fact.ID()) {
			return -1
		}
		return 1
	}
	if leftRow.match.fact.Version() != rightRow.match.fact.Version() {
		if leftRow.match.fact.Version() < rightRow.match.fact.Version() {
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
