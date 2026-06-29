package engine

import "testing"

func TestTokenHashMemoryStoresNegativeBlockerCount(t *testing.T) {
	arena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	token := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())

	var memory tokenHashMemory
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

func TestTokenArenaCopiedRowsOwnCopiedMatchUntilRefresh(t *testing.T) {
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
	if got, want := copiedMatch.conditionID, ConditionID("event"); got != want {
		t.Fatalf("copied match condition ID = %q, want %q", got, want)
	}

	after := FactSnapshot{id: fact.ID(), version: 2, recency: 2, generation: 1}
	refreshed, ok := memory.refreshTokenFactRefInPlace(copied, fact.ID(), newConditionFactRefFromSnapshot(after))
	if !ok {
		t.Fatal("refreshTokenFactRefInPlace returned false")
	}
	refreshedRow, ok := refreshed.resolve()
	if !ok {
		t.Fatal("refreshed token did not resolve")
	}
	if refreshedRow.fact.Version() != after.Version() {
		t.Fatal("refreshed token row did not update owned fact")
	}
	refreshedMatch, ok := tokenRefAtSlot(refreshed, 0)
	if !ok {
		t.Fatal("refreshed token match did not resolve")
	}
	if got, want := refreshedMatch.fact.Version(), after.Version(); got != want {
		t.Fatalf("refreshed match version = %d, want %d", got, want)
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

func TestTokenHashMemoryRecordsRowMovementDuringIndexedRemoval(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory tokenHashMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(second) returned false")
	}
	memory.ensureFactRows()

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
		t.Fatal("moved token is missing from identity index")
	}
	if removed := memory.removeContainingFact(secondFact.ID(), counters); removed != 1 {
		t.Fatalf("removed moved row = %d, want 1", removed)
	}
}

func TestTokenHashMemoryRowHandlesSurviveSwapRemoval(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory tokenHashMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(second) returned false")
	}
	firstHandle := memory.rows[0].handle
	secondHandle := memory.rows[1].handle
	if firstHandle.isZero() || secondHandle.isZero() {
		t.Fatalf("row handles = %v %v, want non-zero", firstHandle, secondHandle)
	}

	if removed := memory.removeContainingFact(firstFact.ID(), nil); removed != 1 {
		t.Fatalf("removed rows = %d, want 1", removed)
	}
	if row := memory.rowByHandle(firstHandle); row != nil {
		t.Fatalf("removed row handle resolved to %#v", row)
	}
	moved := memory.rowByHandle(secondHandle)
	if moved == nil {
		t.Fatal("moved row handle did not resolve")
	}
	if got, want := moved.id, graphTokenRowID(0); got != want {
		t.Fatalf("moved row id = %d, want %d", got, want)
	}
	if !tokenRefEqual(moved.token, secondToken) {
		t.Fatal("moved row handle resolved the wrong token")
	}

	memory.clear()
	if row := memory.rowByHandle(secondHandle); row != nil {
		t.Fatalf("cleared row handle resolved to %#v", row)
	}
}

func TestTokenHashMemoryTerminalHandleRemovalRepairsMovedRow(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory tokenHashMemory
	firstHandle, inserted := memory.insertTerminalRow(firstToken, candidateIdentity{generation: 1, count: 1}, 0)
	if !inserted {
		t.Fatal("insertTerminalRow(first) returned false")
	}
	secondHandle, inserted := memory.insertTerminalRow(secondToken, candidateIdentity{generation: 1, count: 1}, 0)
	if !inserted {
		t.Fatal("insertTerminalRow(second) returned false")
	}
	if got := memory.indexes.keyCount(); got != 0 {
		t.Fatalf("terminal memory join index keys = %d, want 0", got)
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
	moved := memory.rowByHandle(secondHandle)
	if moved == nil {
		t.Fatal("moved terminal row handle did not resolve")
	}
	if got, want := moved.id, graphTokenRowID(0); got != want {
		t.Fatalf("moved terminal row id = %d, want %d", got, want)
	}
	if !memory.containsExactToken(secondToken) {
		t.Fatal("moved terminal row is missing from identity index")
	}
	if got := memory.indexes.keyCount(); got != 0 {
		t.Fatalf("terminal memory join index keys after removal = %d, want 0", got)
	}
	snapshot := counters.snapshot()
	if got, want := snapshot.Totals.RemovalRowsRemoved, 1; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsMoved, 1; got != want {
		t.Fatalf("removal rows moved = %d, want %d", got, want)
	}
}

func TestTokenHashMemoryRowHandlesReuseWithGeneration(t *testing.T) {
	arena := newTokenArena()
	firstFact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	secondFact := FactSnapshot{id: newFactID(1, 2), version: 1, recency: 2, generation: 1}
	firstEntry := bindingTupleEntry{bindingSlot: 0, factID: firstFact.ID(), factVersion: firstFact.Version()}
	secondEntry := bindingTupleEntry{bindingSlot: 0, factID: secondFact.ID(), factVersion: secondFact.Version()}
	firstToken := arena.add(tokenRef{}, firstEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(firstFact)}, firstFact.Recency(), firstFact.Generation())
	secondToken := arena.add(tokenRef{}, secondEntry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(secondFact)}, secondFact.Recency(), secondFact.Generation())

	var memory tokenHashMemory
	if !memory.insert(firstToken, betaJoinKey{}) {
		t.Fatal("insert(first) returned false")
	}
	firstHandle := memory.rows[0].handle
	if removed := memory.removeContainingFact(firstFact.ID(), nil); removed != 1 {
		t.Fatalf("removed rows = %d, want 1", removed)
	}
	if !memory.insert(secondToken, betaJoinKey{}) {
		t.Fatal("insert(second) returned false")
	}
	secondHandle := memory.rows[0].handle
	if firstHandle.id != secondHandle.id {
		t.Fatalf("reused handle id = %d, want %d", secondHandle.id, firstHandle.id)
	}
	if firstHandle.generation == secondHandle.generation {
		t.Fatalf("reused handle generation = %d, want different from stale generation", secondHandle.generation)
	}
	if row := memory.rowByHandle(firstHandle); row != nil {
		t.Fatalf("stale row handle resolved to %#v", row)
	}
	if row := memory.rowByHandle(secondHandle); row == nil || !tokenRefEqual(row.token, secondToken) {
		t.Fatalf("fresh row handle resolved to %#v, want second token", row)
	}
}

func TestTokenHashMemoryReusesBucketRestStorage(t *testing.T) {
	var memory tokenHashMemory

	key := betaJoinKey{}
	bucket, _ := memory.indexes.get(key)
	for id := graphTokenRowID(1); id <= 5; id++ {
		memory.appendBucketRow(&bucket, id)
	}
	memory.indexes.set(key, bucket)
	if got := bucket.len(); got != 5 {
		t.Fatalf("bucket length = %d, want 5", got)
	}
	recycledCap := cap(bucket.rest)
	if recycledCap == 0 {
		t.Fatal("bucket rest capacity = 0, want overflow storage")
	}

	memory.clear()
	if got := len(memory.bucketRestFree); got != 1 {
		t.Fatalf("free bucket rests after clear = %d, want 1", got)
	}
	if got := cap(memory.bucketRestFree[0]); got != recycledCap {
		t.Fatalf("free bucket rest capacity = %d, want %d", got, recycledCap)
	}

	reused := graphTokenRowIDBucket{}
	for id := graphTokenRowID(10); id <= 12; id++ {
		memory.appendBucketRow(&reused, id)
	}
	if got := len(memory.bucketRestFree); got != 0 {
		t.Fatalf("free bucket rests after reuse = %d, want 0", got)
	}
	if got := cap(reused.rest); got != recycledCap {
		t.Fatalf("reused bucket rest capacity = %d, want %d", got, recycledCap)
	}
	for i, want := range []graphTokenRowID{10, 11, 12} {
		got, ok := reused.at(i)
		if !ok {
			t.Fatalf("reused bucket row %d missing", i)
		}
		if got != want {
			t.Fatalf("reused bucket row %d = %d, want %d", i, got, want)
		}
	}
}
