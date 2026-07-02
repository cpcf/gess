package engine

import (
	"testing"
	"unsafe"
)

func TestBetaSideMemoryStoresNegativeBlockerCount(t *testing.T) {
	arena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	token := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())

	var memory betaSideMemory
	if !memory.insertWithNegativeBlockerCount(token, betaJoinKey{}, 2) {
		t.Fatal("insertWithNegativeBlockerCount returned false")
	}
	row := memory.row(0)
	if row == nil {
		t.Fatal("negative row was not retained")
	}
	if got, want := row.negativeBlockerCount(), 2; got != want {
		t.Fatalf("negative blocker count = %d, want %d", got, want)
	}
	if got, want := row.incrementNegativeBlockerCount(), 3; got != want {
		t.Fatalf("incremented blocker count = %d, want %d", got, want)
	}
	if got, want := row.decrementNegativeBlockerCount(), 2; got != want {
		t.Fatalf("decremented blocker count = %d, want %d", got, want)
	}
}

func TestTokenArenaCopiedRowsOwnCopiedMatch(t *testing.T) {
	arena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{conditionID: "event", bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	source := arena.add(tokenRef{}, entry, conditionMatch{conditionID: "event", bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	sourceRow, ok := source.resolve()
	if !ok {
		t.Fatal("source token did not resolve")
	}

	memory := &reteGraphBetaMemory{arena: arena}
	copied := memory.newTokenRowRefSource(tokenRef{}, source, sourceRow, fact.Recency(), fact.Generation(), nil)
	copiedRow, ok := copied.resolve()
	if !ok {
		t.Fatal("copied token did not resolve")
	}
	if copiedRow.fact.ID() != fact.ID() {
		t.Fatal("copied token row did not own copied fact")
	}
	copiedMatch, ok := tokenRefAtSlot(copied, 0)
	if !ok {
		t.Fatal("copied token match did not resolve")
	}
	if got, want := copiedMatch.fact.ID(), fact.ID(); got != want {
		t.Fatalf("copied match fact ID = %q, want %q", got, want)
	}
	if got, want := copiedMatch.bindingSlot, 0; got != want {
		t.Fatalf("copied match binding slot = %d, want %d", got, want)
	}

	sourceMatch, ok := tokenRefAtSlot(source, 0)
	if !ok {
		t.Fatal("source token match did not resolve")
	}
	if got, want := sourceMatch.fact.Version(), fact.Version(); got != want {
		t.Fatalf("source match version = %d, want %d", got, want)
	}
}

func TestTokenArenaCopiedRowsSurviveSourceArenaReset(t *testing.T) {
	sourceArena := newTokenArena()
	targetArena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	source := sourceArena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	sourceRow, ok := source.resolve()
	if !ok {
		t.Fatal("source token did not resolve")
	}

	memory := &reteGraphBetaMemory{arena: targetArena}
	copied := memory.newTokenRowRefSource(tokenRef{}, source, sourceRow, fact.Recency(), fact.Generation(), nil)
	if _, ok := tokenRefAtSlot(copied, 0); !ok {
		t.Fatal("copied token match did not resolve before source reset")
	}

	sourceArena.reset()
	reusedFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	reusedEntry := bindingTupleEntry{bindingSlot: 0, factID: reusedFact.ID(), factVersion: reusedFact.Version()}
	sourceArena.add(tokenRef{}, reusedEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(reusedFact)}, reusedFact.Recency(), reusedFact.Generation())

	match, ok := tokenRefAtSlot(copied, 0)
	if !ok {
		t.Fatal("copied token lost owned match after source reset")
	}
	if got, want := match.fact.ID(), fact.ID(); got != want {
		t.Fatalf("copied match fact ID after source reset = %q, want %q", got, want)
	}
}

func TestTokenRefIdentityKeyUsesArenaMetadata(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 1, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(firstToken, secondEntry, conditionMatch{bindingSlot: 1, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	identity := secondToken.identityKey()
	if got, want := identity.size, 2; got != want {
		t.Fatalf("identity size = %d, want %d", got, want)
	}
	if got, want := identity.generation, Generation(1); got != want {
		t.Fatalf("identity generation = %d, want %d", got, want)
	}
	if got, want := identity.identityState, secondToken.identityState(); got != want {
		t.Fatalf("identity state = %d, want %d", got, want)
	}
	joinKey, ok := betaJoinKeyForTokenIdentity(secondToken)
	if !ok {
		t.Fatal("betaJoinKeyForTokenIdentity returned false")
	}
	if got, want := joinKey.intValue, int64(identity.size); got != want {
		t.Fatalf("join key size = %d, want %d", got, want)
	}
	if got, want := joinKey.floatBits, uint64(identity.generation); got != want {
		t.Fatalf("join key generation = %d, want %d", got, want)
	}
	if got, want := joinKey.secondFloatBits, identity.identityState; got != want {
		t.Fatalf("join key identity state = %d, want %d", got, want)
	}
}

func TestBetaSideMemoryAppendsEquivalentReconstructedToken(t *testing.T) {
	arena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	firstToken := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	secondToken := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	if firstToken.handle == secondToken.handle {
		t.Fatal("test requires distinct token handles")
	}
	if firstToken.identityKey() != secondToken.identityKey() {
		t.Fatalf("equivalent token identity keys differ: %#v vs %#v", firstToken.identityKey(), secondToken.identityKey())
	}
	if !tokenRefEqual(firstToken, secondToken) {
		t.Fatal("equivalent reconstructed tokens should compare equal")
	}

	var memory betaSideMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(equivalent second) returned false")
	}
	if got, want := len(memory.rows), 2; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if removed, ok := memory.removeTokenWithJoinKey(secondToken, betaJoinKey{}, nil); !ok || !tokenRefEqual(removed.token, firstToken) {
		t.Fatalf("remove equivalent token = (%#v, %v), want an equivalent token", removed, ok)
	}
	if got, want := len(memory.rows), 1; got != want {
		t.Fatalf("rows after removal = %d, want %d", got, want)
	}
}

func TestBetaSideMemoryKeepsIdentityCollisionRowsDistinct(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	firstRow, ok := firstToken.resolve()
	if !ok {
		t.Fatal("first token did not resolve")
	}
	secondRow, ok := secondToken.resolve()
	if !ok {
		t.Fatal("second token did not resolve")
	}
	secondRow.identityState = firstRow.identityState
	if firstToken.identityKey() != secondToken.identityKey() {
		t.Fatalf("forced collision identity keys differ: %#v vs %#v", firstToken.identityKey(), secondToken.identityKey())
	}
	if tokenRefEqual(firstToken, secondToken) {
		t.Fatal("tokens with colliding identity key but different facts compared equal")
	}

	var memory betaSideMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(colliding second) returned false")
	}
	if got, want := len(memory.rows), 2; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if removed, ok := memory.removeToken(secondToken, nil); !ok || !tokenRefEqual(removed.token, secondToken) {
		t.Fatalf("remove colliding second = (%#v, %v), want second token", removed, ok)
	}
	if !memory.containsExactToken(firstToken) {
		t.Fatal("first token missing after removing colliding second")
	}
}

func TestTerminalTokenMemoryDedupesEquivalentReconstructedTokenSupport(t *testing.T) {
	arena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	firstToken := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	secondToken := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
	if firstToken.handle == secondToken.handle {
		t.Fatal("test requires distinct token handles")
	}

	var memory terminalTokenMemory
	firstHandle, inserted := memory.insertTerminalRow(firstToken, 10, candidateIdentityKey{})
	if !inserted {
		t.Fatal("insertTerminalRow(first) returned false")
	}
	secondHandle, inserted := memory.insertTerminalRow(secondToken, 20, candidateIdentityKey{})
	if inserted {
		t.Fatal("insertTerminalRow(equivalent second) returned true, want duplicate support")
	}
	if firstHandle != secondHandle {
		t.Fatalf("equivalent terminal handle = %#v, want %#v", secondHandle, firstHandle)
	}
	if got, want := len(memory.rows), 1; got != want {
		t.Fatalf("terminal rows = %d, want %d", got, want)
	}
	row := memory.rowByHandle(firstHandle)
	if row == nil {
		t.Fatal("terminal row missing")
	}
	rowID, ok := memory.rowIDByHandle(firstHandle)
	if !ok {
		t.Fatal("terminal row id missing")
	}
	if got, want := int(memory.terminalSupportCount(rowID, *row)), 2; got != want {
		t.Fatalf("support count = %d, want %d", got, want)
	}
	if !memory.hasTerminalBranchSupport(rowID, 10) || !memory.hasTerminalBranchSupport(rowID, 20) {
		t.Fatalf("branch support missing after duplicate insert: %#v", memory.terminalBranchIDs(rowID))
	}
	if removed, deleted, consumed := memory.removeTokenByHandle(firstHandle, nil, 20); !consumed || deleted || !removed.token.isZero() {
		t.Fatalf("remove duplicate support = removed=%#v deleted=%v consumed=%v, want support decrement", removed, deleted, consumed)
	}
	row = memory.rowByHandle(firstHandle)
	if row == nil {
		t.Fatal("terminal row missing after support decrement")
	}
	if got, want := int(memory.terminalSupportCount(rowID, *row)), 1; got != want {
		t.Fatalf("support count after decrement = %d, want %d", got, want)
	}
	if memory.hasTerminalBranchSupport(rowID, 20) {
		t.Fatalf("branch 20 still supported after decrement: %#v", memory.terminalBranchIDs(rowID))
	}
	if removed, ok := memory.removeToken(firstToken, nil, 10); !ok || !tokenRefEqual(removed.token, firstToken) {
		t.Fatalf("remove final support = (%#v, %v), want first token", removed, ok)
	}
}

func TestTerminalTokenMemoryKeepsIdentityCollisionRowsDistinct(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	firstRow, ok := firstToken.resolve()
	if !ok {
		t.Fatal("first token did not resolve")
	}
	secondRow, ok := secondToken.resolve()
	if !ok {
		t.Fatal("second token did not resolve")
	}
	secondRow.identityState = firstRow.identityState
	if tokenRefEqual(firstToken, secondToken) {
		t.Fatal("tokens with colliding identity key but different facts compared equal")
	}

	var memory terminalTokenMemory
	if _, inserted := memory.insertTerminalRow(firstToken, 0, candidateIdentityKey{}); !inserted {
		t.Fatal("insertTerminalRow(first) returned false")
	}
	if _, inserted := memory.insertTerminalRow(secondToken, 0, candidateIdentityKey{}); !inserted {
		t.Fatal("insertTerminalRow(colliding second) returned false")
	}
	if got, want := len(memory.rows), 2; got != want {
		t.Fatalf("terminal rows = %d, want %d", got, want)
	}
	if removed, ok := memory.removeToken(secondToken, nil, 0); !ok || !tokenRefEqual(removed.token, secondToken) {
		t.Fatalf("remove colliding second = (%#v, %v), want second token", removed, ok)
	}
	if removed, ok := memory.removeToken(firstToken, nil, 0); !ok || !tokenRefEqual(removed.token, firstToken) {
		t.Fatalf("remove first after collision = (%#v, %v), want first token", removed, ok)
	}
}

func TestBetaSideMemoryRecordsRowMovementDuringIndexedRemoval(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory betaSideMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(second) returned false")
	}

	counters := newPropagationCounterLedger()
	if removed := memory.removeContainingFact(firstFact.ID(), counters); removed != 1 {
		t.Fatalf("removed rows = %d, want 1", removed)
	}
	snapshot := counters.snapshot()
	if got, want := snapshot.Totals.RemovalRowsRemoved, 1; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsMoved, 1; got != want {
		t.Fatalf("removal rows moved = %d, want %d", got, want)
	}
	if got := len(memory.rows); got != 1 {
		t.Fatalf("rows after removal = %d, want 1", got)
	}
	if !memory.containsExactToken(secondToken) {
		t.Fatal("moved token is missing from beta memory")
	}
	if removed := memory.removeContainingFact(secondFact.ID(), counters); removed != 1 {
		t.Fatalf("removed moved row = %d, want 1", removed)
	}
}

func TestBetaSideMemoryRemoveTokenWithJoinKeySurvivesSwapRemoval(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	firstKey := betaJoinKey{intValue: 10}
	secondKey := betaJoinKey{intValue: 20}

	var memory betaSideMemory
	if !memory.insert(firstToken, firstKey) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, secondKey) {
		t.Fatal("insert(second) returned false")
	}

	counters := newPropagationCounterLedger()
	if removed, ok := memory.removeTokenWithJoinKey(firstToken, firstKey, counters); !ok || !tokenRefEqual(removed.token, firstToken) {
		t.Fatalf("remove first = (%#v, %v), want first token", removed, ok)
	}
	snapshot := counters.snapshot()
	if got, want := snapshot.Totals.RemovalRowsMoved, 1; got != want {
		t.Fatalf("removal rows moved = %d, want %d", got, want)
	}
	if removed, ok := memory.removeTokenWithJoinKey(secondToken, secondKey, counters); !ok || !tokenRefEqual(removed.token, secondToken) {
		t.Fatalf("remove moved second = (%#v, %v), want second token", removed, ok)
	}
	if got := len(memory.rows); got != 0 {
		t.Fatalf("rows after removing moved token = %d, want 0", got)
	}
}

func TestTerminalTokenMemoryHandlesUseRowGenerationWithoutMove(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	thirdFact := FactSnapshot{id: newFactID(1, 3), version: 1, recency: 3, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	thirdEntry := bindingTupleEntry{bindingSlot: 0, factID: thirdFact.ID(), factVersion: thirdFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())
	thirdToken := arena.add(tokenRef{}, thirdEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(thirdFact)}, thirdFact.Recency(), thirdFact.Generation())

	var memory terminalTokenMemory
	firstHandle, inserted := memory.insertTerminalRow(firstToken, 0, candidateIdentityKey{})
	if !inserted {
		t.Fatal("insertTerminalRow(first) returned false")
	}
	secondHandle, inserted := memory.insertTerminalRow(secondToken, 0, candidateIdentityKey{})
	if !inserted {
		t.Fatal("insertTerminalRow(second) returned false")
	}
	memory.ensureFactRows()

	counters := newPropagationCounterLedger()
	removed, deleted, consumed := memory.removeTokenByHandle(firstHandle, counters, 0)
	if !consumed || !deleted {
		t.Fatalf("removeTokenByHandle consumed=%v deleted=%v, want both true", consumed, deleted)
	}
	if !tokenRefEqual(removed.token, firstToken) {
		t.Fatal("removed terminal row has the wrong token")
	}
	if row := memory.rowByHandle(firstHandle); row != nil {
		t.Fatalf("removed terminal handle resolved to %#v", row)
	}
	remaining := memory.rowByHandle(secondHandle)
	if remaining == nil {
		t.Fatal("remaining terminal row handle did not resolve")
	}
	if got, ok := memory.rowIDByHandle(secondHandle); !ok || got != graphTokenRowID(1) {
		t.Fatalf("remaining terminal row id = %d, ok=%v, want 1 and true", got, ok)
	}
	thirdHandle, inserted := memory.insertTerminalRow(thirdToken, 0, candidateIdentityKey{})
	if !inserted {
		t.Fatal("insertTerminalRow(third) returned false")
	}
	if thirdHandle.id != firstHandle.id || thirdHandle.generation == firstHandle.generation {
		t.Fatalf("reused handle = %#v after removed handle %#v, want same id and new generation", thirdHandle, firstHandle)
	}
	if row := memory.rowByHandle(firstHandle); row != nil {
		t.Fatalf("stale removed terminal handle resolved after reuse to %#v", row)
	}
	if row := memory.rowByHandle(thirdHandle); row == nil || !tokenRefEqual(memory.rowToken(*row), thirdToken) {
		t.Fatalf("reused terminal handle resolved to %#v, want third token", row)
	}
	if removed, ok := memory.removeToken(secondToken, nil, 0); !ok || !tokenRefEqual(removed.token, secondToken) {
		t.Fatalf("remaining terminal row removal = (%#v, %v), want second token", removed, ok)
	}
	snapshot := counters.snapshot()
	if got, want := snapshot.Totals.RemovalRowsRemoved, 1; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsMoved, 0; got != want {
		t.Fatalf("removal rows moved = %d, want %d", got, want)
	}
}

func TestTerminalTokenMemoryClearInvalidatesRowGeneration(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory terminalTokenMemory
	firstHandle, inserted := memory.insertTerminalRow(firstToken, 0, candidateIdentityKey{})
	if !inserted {
		t.Fatal("insertTerminalRow(first) returned false")
	}
	memory.clear()
	if got := memory.len(); got != 0 {
		t.Fatalf("terminal rows after clear = %d, want 0", got)
	}
	if row := memory.rowByHandle(firstHandle); row != nil {
		t.Fatalf("cleared terminal handle resolved to %#v", row)
	}
	secondHandle, inserted := memory.insertTerminalRow(secondToken, 0, candidateIdentityKey{})
	if !inserted {
		t.Fatal("insertTerminalRow(second) returned false")
	}
	if secondHandle.id != firstHandle.id || secondHandle.generation == firstHandle.generation {
		t.Fatalf("reused handle after clear = %#v after %#v, want same id and new generation", secondHandle, firstHandle)
	}
	if row := memory.rowByHandle(firstHandle); row != nil {
		t.Fatalf("stale cleared terminal handle resolved after reuse to %#v", row)
	}
	if row := memory.rowByHandle(secondHandle); row == nil || !tokenRefEqual(memory.rowToken(*row), secondToken) {
		t.Fatalf("reused terminal handle resolved to %#v, want second token", row)
	}
}

func TestTerminalTokenRowIsCompact(t *testing.T) {
	if got, want := unsafe.Sizeof(terminalTokenRow{}), uintptr(32); got != want {
		t.Fatalf("terminal token row size = %d, want %d", got, want)
	}
}

func TestBetaSideMemoryScansFactRemovalWithoutReverseIndex(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory betaSideMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(second) returned false")
	}
	if got := memory.factIndexKeyCount(); got != 0 {
		t.Fatalf("beta fact index keys = %d, want no retained fact index", got)
	}
	if removed := memory.removeContainingFact(firstFact.ID(), nil); removed != 1 {
		t.Fatalf("removed rows = %d, want 1", removed)
	}
	if got, want := len(memory.rows), 1; got != want {
		t.Fatalf("rows after first fact removal = %d, want %d", got, want)
	}
	if removed := memory.removeContainingFact(secondFact.ID(), nil); removed != 1 {
		t.Fatalf("removed second rows = %d, want 1", removed)
	}
}

func TestBetaSideMemoryFactScanFindsParentFacts(t *testing.T) {
	arena := newTokenArena()
	parentFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	childFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	parentEntry := bindingTupleEntry{bindingSlot: 0, factID: parentFact.ID(), factVersion: parentFact.Version()}
	childEntry := bindingTupleEntry{bindingSlot: 1, factID: childFact.ID(), factVersion: childFact.Version()}
	parent := arena.add(tokenRef{}, parentEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(parentFact)}, parentFact.Recency(), parentFact.Generation())
	child := arena.add(parent, childEntry, conditionMatch{bindingSlot: 1, fact: newConditionFactRefFromSnapshot(childFact)}, childFact.Recency(), childFact.Generation())

	var memory betaSideMemory
	if !memory.insert(child, betaJoinKey{}) {
		t.Fatal("insert(child) returned false")
	}
	if got := memory.factRowCount(parentFact.ID()); got != 1 {
		t.Fatalf("parent fact scan rows = %d, want 1", got)
	}
	if got := memory.factRowCount(childFact.ID()); got != 1 {
		t.Fatalf("child fact scan rows = %d, want 1", got)
	}
	if removed := memory.removeContainingFact(parentFact.ID(), nil); removed != 1 {
		t.Fatalf("removed rows for parent fact = %d, want 1", removed)
	}
}
