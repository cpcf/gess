package gess

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

func TestTokenArenaCopiedRowsReferenceSourceMatchUntilRefresh(t *testing.T) {
	arena := newTokenArena()
	fact := FactSnapshot{id: newFactID(1, 1), version: 1, recency: 1, generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	source := arena.add(tokenRef{}, entry, conditionMatch{bindingSlot: 0, fact: newConditionFactRefFromSnapshot(fact)}, fact.Recency(), fact.Generation())
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
	if copiedRow.matchSource.isZero() {
		t.Fatal("copied token row did not retain source match handle")
	}
	copiedMatch, ok := tokenRefAtSlot(copied, 0)
	if !ok {
		t.Fatal("copied token match did not resolve")
	}
	if got, want := copiedMatch.fact.ID(), fact.ID(); got != want {
		t.Fatalf("copied match fact ID = %q, want %q", got, want)
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
	if !refreshedRow.matchSource.isZero() {
		t.Fatal("refreshed token row still references source match")
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

func TestTokenHashMemoryReusesBucketRestStorage(t *testing.T) {
	var memory tokenHashMemory
	memory.indexes = make(map[betaJoinKey]graphTokenRowIDBucket)

	key := betaJoinKey{}
	bucket := memory.indexes[key]
	for id := graphTokenRowID(1); id <= 5; id++ {
		memory.appendBucketRow(&bucket, id)
	}
	memory.indexes[key] = bucket
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
