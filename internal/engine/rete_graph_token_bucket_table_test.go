package engine

import "testing"

func TestBetaJoinBucketTableRecordsHolderAndIdentityIndexLifecycle(t *testing.T) {
	counters := newPropagationCounterLedger()
	arena := newTokenArena()
	arena.counters = counters
	token := testBetaBucketToken(t, arena, 1)

	var holderTable betaJoinBucketTable
	if _, ok := holderTable.insert(betaTokenRow{token: token}); !ok {
		t.Fatal("holder table insert returned false")
	}
	if _, ok := holderTable.removeIdentityToken(token, nil, nil); !ok {
		t.Fatal("holder removal returned false")
	}

	var first, second, multi betaJoinBucketTable
	for _, table := range []*betaJoinBucketTable{&first, &second, &multi} {
		if _, ok := table.insert(betaTokenRow{token: token}); !ok {
			t.Fatal("multi-holder insert returned false")
		}
	}
	if _, ok := multi.removeIdentityToken(token, nil, nil); !ok {
		t.Fatal("identity-index removal returned false")
	}
	if _, ok := multi.insert(betaTokenRow{token: testBetaBucketToken(t, arena, 2)}); !ok {
		t.Fatal("indexed insert returned false")
	}

	totals := counters.snapshot().Totals
	if got, want := totals.BetaHolderHits, 1; got != want {
		t.Fatalf("holder hits = %d, want %d", got, want)
	}
	if got, want := totals.BetaMultiHolderDemotions, 1; got != want {
		t.Fatalf("multi-holder demotions = %d, want %d", got, want)
	}
	if got, want := totals.BetaIdentityIndexBuilds, 1; got != want {
		t.Fatalf("identity-index builds = %d, want %d", got, want)
	}
	if got, want := totals.BetaIdentityIndexInserts, 2; got != want {
		t.Fatalf("identity-index inserts = %d, want %d", got, want)
	}
	if got, want := totals.BetaIdentityIndexProbes, 1; got != want {
		t.Fatalf("identity-index probes = %d, want %d", got, want)
	}
	if got, want := totals.BetaIdentityIndexCandidates, 1; got != want {
		t.Fatalf("identity-index candidates = %d, want %d", got, want)
	}
	if totals.BetaIdentityScanFallbacks != 0 || totals.BetaIdentityScanCandidates != 0 {
		t.Fatalf("unexpected scan fallback counters: %+v", totals)
	}
}

func TestBetaJoinBucketTableRecordsIdentityScanCandidates(t *testing.T) {
	counters := newPropagationCounterLedger()
	arena := newTokenArena()
	arena.counters = counters
	first := testBetaBucketToken(t, arena, 1)
	second := testBetaBucketToken(t, arena, 2)

	var table betaJoinBucketTable
	if _, ok := table.insert(betaTokenRow{token: first}); !ok {
		t.Fatal("first insert returned false")
	}
	if _, ok := table.insert(betaTokenRow{token: second}); !ok {
		t.Fatal("second insert returned false")
	}
	if _, ok := table.removeIdentityTokenScan(second, nil, nil); !ok {
		t.Fatal("scan removal returned false")
	}

	totals := counters.snapshot().Totals
	if got, want := totals.BetaIdentityScanFallbacks, 1; got != want {
		t.Fatalf("scan fallbacks = %d, want %d", got, want)
	}
	if got, want := totals.BetaIdentityScanCandidates, 2; got != want {
		t.Fatalf("scan candidates = %d, want %d", got, want)
	}
}

func testBetaBucketToken(t testing.TB, arena *tokenArena, sequence uint64) tokenRef {
	t.Helper()
	fact := FactSnapshot{id: newFactID(1, sequence), version: 1, recency: Recency(sequence), generation: 1}
	entry := bindingTupleEntry{bindingSlot: 0, factID: fact.ID(), factVersion: fact.Version()}
	return arena.add(tokenRef{}, entry, conditionMatch{
		bindingSlot: 0,
		fact:        newConditionFactRefFromSnapshot(fact),
	}, fact.Recency(), fact.Generation())
}
