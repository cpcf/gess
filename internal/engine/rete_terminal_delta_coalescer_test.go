package engine

import "testing"

func TestTerminalDeltaCoalescerScratchPreservesFirstMatchOrder(t *testing.T) {
	arena := newTokenArena()
	removedFirst := arena.addSeed(1)
	removedSecond := arena.addSeed(1)
	removedUnmatched := arena.addSeed(1)
	addedFirst := arena.addSeed(1)
	addedSecond := arena.addSeed(1)
	addedUnmatched := arena.addSeed(1)
	duplicateIdentity := candidateIdentity{
		generation: 1,
		count:      1,
		key:        candidateIdentityKey{scopeHash: 7, hash: 11},
	}
	unmatchedIdentity := candidateIdentity{
		generation: 1,
		count:      1,
		key:        candidateIdentityKey{scopeHash: 7, hash: 13},
	}
	added := []reteTerminalTokenDelta{
		{ruleRevisionID: "rule", terminalID: 1, token: addedFirst, identity: duplicateIdentity},
		{ruleRevisionID: "rule", terminalID: 1, token: addedSecond, identity: duplicateIdentity},
		{ruleRevisionID: "rule", terminalID: 1, token: addedUnmatched, identity: unmatchedIdentity},
	}
	removed := []reteTerminalTokenDelta{
		{ruleRevisionID: "rule", terminalID: 1, token: removedFirst, identity: duplicateIdentity},
		{ruleRevisionID: "rule", terminalID: 1, token: removedSecond, identity: duplicateIdentity},
		{ruleRevisionID: "other-rule", terminalID: 1, token: removedUnmatched, identity: unmatchedIdentity},
	}

	var scratch terminalDeltaCoalescerScratch
	keptAdded, keptRemoved, updates := scratch.coalesce(nil, added, removed, nil)
	if len(keptAdded) != 1 || keptAdded[0].token != addedUnmatched {
		t.Fatalf("kept added = %#v, want unmatched add", keptAdded)
	}
	if len(keptRemoved) != 1 || keptRemoved[0].token != removedUnmatched {
		t.Fatalf("kept removed = %#v, want unmatched remove", keptRemoved)
	}
	if len(updates) != 2 {
		t.Fatalf("updates = %d, want 2", len(updates))
	}
	if updates[0].before != removedFirst || updates[0].after != addedFirst {
		t.Fatalf("first update = (%v, %v), want first removed/added pair", updates[0].before, updates[0].after)
	}
	if updates[1].before != removedSecond || updates[1].after != addedSecond {
		t.Fatalf("second update = (%v, %v), want second removed/added pair", updates[1].before, updates[1].after)
	}
	assertTerminalDeltaCoalescerScratchReleased(t, &scratch)
}

func TestTerminalDeltaCoalescerScratchReusesStorageWithoutStaleCollisions(t *testing.T) {
	arena := newTokenArena()
	key := candidateIdentityKey{scopeHash: 17, hash: 19}
	firstIdentity := candidateIdentity{generation: 1, count: 1, key: key}
	secondIdentity := candidateIdentity{generation: 2, count: 1, key: key}
	firstRemoved := arena.addSeed(1)
	firstRemovedDuplicate := arena.addSeed(1)
	firstAdded := arena.addSeed(1)
	firstAddedDuplicate := arena.addSeed(1)

	var scratch terminalDeltaCoalescerScratch
	firstKeptAdded, firstKeptRemoved, firstUpdates := scratch.coalesce(nil,
		[]reteTerminalTokenDelta{
			{ruleRevisionID: "rule", terminalID: 1, token: firstAdded, identity: firstIdentity},
			{ruleRevisionID: "rule", terminalID: 1, token: firstAddedDuplicate, identity: firstIdentity},
		},
		[]reteTerminalTokenDelta{
			{ruleRevisionID: "rule", terminalID: 1, token: firstRemoved, identity: firstIdentity},
			{ruleRevisionID: "rule", terminalID: 1, token: firstRemovedDuplicate, identity: firstIdentity},
		},
		nil,
	)
	if len(firstKeptAdded) != 0 || len(firstKeptRemoved) != 0 || len(firstUpdates) != 2 {
		t.Fatalf("first coalesce = +%d -%d ~%d, want +0 -0 ~2", len(firstKeptAdded), len(firstKeptRemoved), len(firstUpdates))
	}
	heads := scratch.heads
	nextCapacity := cap(scratch.next)
	consumedCapacity := cap(scratch.consumed)
	assertTerminalDeltaCoalescerScratchReleased(t, &scratch)

	collisionRemoved := arena.addSeed(1)
	matchingRemoved := arena.addSeed(1)
	matchingAdded := arena.addSeed(1)
	unmatchedAdded := arena.addSeed(1)
	counters := newPropagationCounterLedger()
	keptAdded, keptRemoved, updates := scratch.coalesce(nil,
		[]reteTerminalTokenDelta{
			{ruleRevisionID: "rule", terminalID: 1, token: matchingAdded, identity: secondIdentity},
			{ruleRevisionID: "rule", terminalID: 1, token: unmatchedAdded, identity: candidateIdentity{generation: 3, count: 1, key: key}},
		},
		[]reteTerminalTokenDelta{
			{ruleRevisionID: "rule", terminalID: 1, token: collisionRemoved, identity: firstIdentity},
			{ruleRevisionID: "rule", terminalID: 1, token: matchingRemoved, identity: secondIdentity},
		},
		counters,
	)
	if scratch.heads == nil || heads == nil {
		t.Fatal("scratch heads map was not retained")
	}
	sentinel := terminalDeltaCoalesceKey{ruleRevisionID: "sentinel"}
	heads[sentinel] = 1
	if got := scratch.heads[sentinel]; got != 1 {
		t.Fatal("scratch heads map storage was replaced")
	}
	delete(scratch.heads, sentinel)
	if cap(scratch.next) != nextCapacity || cap(scratch.consumed) != consumedCapacity {
		t.Fatalf("scratch capacities = (%d, %d), want reused (%d, %d)", cap(scratch.next), cap(scratch.consumed), nextCapacity, consumedCapacity)
	}
	if len(keptAdded) != 1 || keptAdded[0].token != unmatchedAdded {
		t.Fatalf("kept added = %#v, want collision-unmatched add", keptAdded)
	}
	if len(keptRemoved) != 1 || keptRemoved[0].token != collisionRemoved {
		t.Fatalf("kept removed = %#v, want non-equal collision", keptRemoved)
	}
	if len(updates) != 1 || updates[0].before != matchingRemoved || updates[0].after != matchingAdded {
		t.Fatalf("updates = %#v, want matching collision candidate", updates)
	}
	totals := counters.snapshot().Totals
	if got, want := totals.CoalescerIdentityIndexProbes, 2; got != want {
		t.Fatalf("identity index probes = %d, want %d", got, want)
	}
	if got, want := totals.CoalescerIdentityIndexCandidates, 3; got != want {
		t.Fatalf("identity index candidates = %d, want %d", got, want)
	}
	assertTerminalDeltaCoalescerScratchReleased(t, &scratch)
}

func assertTerminalDeltaCoalescerScratchReleased(t *testing.T, scratch *terminalDeltaCoalescerScratch) {
	t.Helper()
	if len(scratch.heads) != 0 {
		t.Fatalf("scratch retained %d identity heads", len(scratch.heads))
	}
	for i, next := range scratch.next {
		if next != 0 {
			t.Fatalf("scratch next[%d] = %d, want cleared", i, next)
		}
	}
	for i, consumed := range scratch.consumed {
		if consumed {
			t.Fatalf("scratch consumed[%d] remained set", i)
		}
	}
}
