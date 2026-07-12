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

func TestBetaJoinBucketTableIdentityBucketsInlineAndReuseOverflow(t *testing.T) {
	arena := newTokenArena()
	token := testBetaBucketToken(t, arena, 1)

	var table betaJoinBucketTable
	refs := make([]int32, 4)
	for i := range refs {
		var ok bool
		refs[i], ok = table.insert(betaTokenRow{token: token})
		if !ok {
			t.Fatalf("insert %d returned false", i)
		}
	}
	table.ensureIdentityIndex(nil)
	state := token.identityState()
	bucket := table.byIdentity[state]
	if got, want := bucket.len(), 4; got != want {
		t.Fatalf("identity bucket length = %d, want %d", got, want)
	}
	if bucket.first == 0 || bucket.second == 0 || len(bucket.overflow) != 2 {
		t.Fatalf("identity bucket = %+v, want two inline refs and two overflow refs", bucket)
	}

	if !table.unlink(refs[0]) || !table.unlink(refs[1]) {
		t.Fatal("unlink inline refs returned false")
	}
	bucket = table.byIdentity[state]
	if got, want := bucket.len(), 2; got != want {
		t.Fatalf("identity bucket length after demotion = %d, want %d", got, want)
	}
	if bucket.first == 0 || bucket.second == 0 || bucket.overflow != nil {
		t.Fatalf("identity bucket after demotion = %+v, want two inline refs", bucket)
	}
	if got, want := len(table.identityOverflowPool), 1; got != want {
		t.Fatalf("overflow pool length = %d, want %d", got, want)
	}

	overflowRef, ok := table.insert(betaTokenRow{token: token})
	if !ok {
		t.Fatal("overflow reuse insert returned false")
	}
	bucket = table.byIdentity[state]
	if got, want := bucket.len(), 3; got != want {
		t.Fatalf("identity bucket length after reuse = %d, want %d", got, want)
	}
	if len(bucket.overflow) != 1 || len(table.identityOverflowPool) != 0 {
		t.Fatalf("overflow length = %d, pool length = %d, want 1 and 0", len(bucket.overflow), len(table.identityOverflowPool))
	}
	if !table.unlink(overflowRef) {
		t.Fatal("overflow unlink returned false")
	}
	bucket = table.byIdentity[state]
	if bucket.overflow != nil || len(table.identityOverflowPool) != 1 {
		t.Fatalf("overflow after direct removal = %v, pool length = %d, want nil and 1", bucket.overflow, len(table.identityOverflowPool))
	}

	table.clear()
	if table.byIdentity != nil {
		t.Fatal("identity index retained after clear")
	}
	if got, want := len(table.identityOverflowPool), 1; got != want {
		t.Fatalf("overflow pool length after clear = %d, want %d", got, want)
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
