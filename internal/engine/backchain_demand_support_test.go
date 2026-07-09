package engine

import (
	"context"
	"testing"
)

func TestBackchainDemandSupportGenericPathStoresSupport(t *testing.T) {
	session := &Session{}
	demandFactID := newFactID(1, 1)
	supportFactID := newFactID(1, 2)
	request := backchainDemandRequest{
		templateKey: TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{
			{id: supportFactID, version: 1},
		},
		slots: []factSlot{
			{value: newStringValue("a"), ok: true},
			{value: newStringValue("b"), ok: true},
			{value: newStringValue("c"), ok: true},
			{value: newStringValue("d"), ok: true},
			{value: newStringValue("e"), ok: true},
		},
	}

	session.addBackchainDemandSupport(&workingFact{id: demandFactID}, request)

	if session.nextBackchainDemandSupportID != 1 {
		t.Fatalf("nextBackchainDemandSupportID = %d, want 1", session.nextBackchainDemandSupportID)
	}
	requestKey, ok := backchainDemandSupportKeyForRequest(request)
	if !ok {
		t.Fatal("backchainDemandSupportKeyForRequest failed")
	}
	supportBucket, _ := session.backchainDemandSupports.get(requestKey.key)
	if !supportBucket.contains(1) {
		t.Fatalf("generic support bucket does not contain support id 1: %#v", supportBucket)
	}
	factBucket, _ := session.backchainDemandByFact.get(supportFactID)
	if !factBucket.contains(1) {
		t.Fatalf("support fact bucket does not contain support id 1: %#v", factBucket)
	}
	demandBucket, _ := session.backchainDemandByDemand.get(demandFactID)
	if !demandBucket.contains(1) {
		t.Fatalf("demand fact bucket does not contain support id 1: %#v", demandBucket)
	}
}

func TestBackchainDemandSupportDiagnosticsExposeSlotsAndLiveRecords(t *testing.T) {
	session := &Session{}
	request := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{{id: newFactID(1, 2), version: 1}},
		slots:        []factSlot{{value: newStringValue("a"), ok: true}},
	}
	session.addBackchainDemandSupport(&workingFact{id: newFactID(1, 1)}, request)

	owner := session.backchainDemandSupportMemoryOwnerDiagnostics()
	if owner.Owner != runtimeMemoryOwnerBackchainDemandSupport {
		t.Fatalf("owner = %q, want %q", owner.Owner, runtimeMemoryOwnerBackchainDemandSupport)
	}
	if owner.Rows != 1 || owner.HighWater != 1 || owner.Tombstones != 0 {
		t.Fatalf("live diagnostics = %#v, want rows/high-water/tombstones 1/1/0", owner)
	}
	if owner.Bytes == 0 {
		t.Fatalf("retained bytes = 0, want record arena capacity: %#v", owner)
	}

	if _, err := session.removeBackchainDemandSupportForRequest(context.Background(), request, mutationOrigin{}); err != nil {
		t.Fatalf("removeBackchainDemandSupportForRequest: %v", err)
	}
	owner = session.backchainDemandSupportMemoryOwnerDiagnostics()
	if owner.Rows != 0 || owner.HighWater != 1 || owner.Tombstones != 1 {
		t.Fatalf("cleared diagnostics = %#v, want rows/high-water/tombstones 0/1/1", owner)
	}
	if owner.Indexes != 1 {
		t.Fatalf("free-list entries = %d, want 1", owner.Indexes)
	}
}

func TestBackchainDemandSupportRecordSlotsAreReusedUnderChurn(t *testing.T) {
	session := &Session{}
	request := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{{id: newFactID(1, 2), version: 1}},
		slots:        []factSlot{{value: newStringValue("a"), ok: true}},
	}

	for sequence := uint64(1); sequence <= 128; sequence++ {
		id := session.addBackchainDemandSupport(&workingFact{id: newFactID(1, sequence+10)}, request)
		if id != 1 {
			t.Fatalf("iteration %d support id = %d, want reused id 1", sequence, id)
		}
		if _, err := session.removeBackchainDemandSupportForRequest(context.Background(), request, mutationOrigin{}); err != nil {
			t.Fatalf("iteration %d remove support: %v", sequence, err)
		}
	}

	if got := len(session.backchainDemandSupportRecords); got != 1 {
		t.Fatalf("record slots after churn = %d, want 1", got)
	}
	owner := session.backchainDemandSupportMemoryOwnerDiagnostics()
	if owner.Rows != 0 || owner.HighWater != 1 || owner.Tombstones != 1 || owner.Indexes != 1 {
		t.Fatalf("diagnostics after churn = %#v, want bounded one-slot arena", owner)
	}
}

func TestBackchainDemandInlineSupportRemovalUsesPayloadEntry(t *testing.T) {
	session := &Session{}
	demandFactID := newFactID(1, 10)
	support := backchainDemandSupportFact{id: newFactID(1, 11), version: 2}
	request := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{support},
		slots: []factSlot{
			{value: newStringValue("q1"), ok: true},
		},
	}

	session.addBackchainDemandSupport(&workingFact{id: demandFactID}, request)
	inlineKey, ok := backchainDemandInlineSupportKeyForRequest(request)
	if !ok {
		t.Fatal("backchainDemandInlineSupportKeyForRequest failed")
	}
	entry, ok := session.backchainDemandInlineSupports.get(inlineKey)
	if !ok {
		t.Fatal("inline support entry not stored")
	}
	if entry.id != 1 || entry.demandFactID != demandFactID || entry.support != support {
		t.Fatalf("inline entry = %#v, want id 1 demand %v support %#v", entry, demandFactID, support)
	}

	delta, err := session.removeBackchainDemandSupportForRequest(context.Background(), request, mutationOrigin{})
	if err != nil {
		t.Fatalf("removeBackchainDemandSupportForRequest: %v", err)
	}
	if !delta.supported {
		t.Fatalf("delta.supported = false, want true")
	}
	if _, ok := session.backchainDemandInlineSupports.get(inlineKey); ok {
		t.Fatal("inline support entry still present after removal")
	}
	if _, ok := session.backchainDemandSupportRecordByID(entry.id); ok {
		t.Fatal("support record still present after removal")
	}
	factBucket, _ := session.backchainDemandByFact.get(support.id)
	if !factBucket.empty() {
		t.Fatalf("support fact bucket = %#v, want empty", factBucket)
	}
	demandBucket, _ := session.backchainDemandByDemand.get(demandFactID)
	if !demandBucket.empty() {
		t.Fatalf("demand fact bucket = %#v, want empty", demandBucket)
	}
}

func TestBackchainDemandSupportRemovalUsesSingleSupportFactBucket(t *testing.T) {
	session := &Session{}
	demandFactID := newFactID(1, 20)
	support := backchainDemandSupportFact{id: newFactID(1, 21), version: 2}
	request := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{support},
		slots: []factSlot{
			{value: newStringValue("q1"), ok: true},
		},
	}
	session.addBackchainDemandSupport(&workingFact{id: demandFactID}, request)
	session.backchainDemandInlineSupports.clear()
	if id, ok := session.singleBackchainDemandSupportIDForRequest(request); !ok || id != 1 {
		t.Fatalf("single support id = (%d, %t), want (1, true)", id, ok)
	}

	delta, err := session.removeBackchainDemandSupportForRequest(context.Background(), request, mutationOrigin{})
	if err != nil {
		t.Fatalf("removeBackchainDemandSupportForRequest: %v", err)
	}
	if !delta.supported {
		t.Fatalf("delta.supported = false, want true")
	}
	if _, ok := session.backchainDemandSupportRecordByID(1); ok {
		t.Fatal("support record still present after single-bucket removal")
	}
}

func TestBackchainDemandSupportSingleBucketFastPathRequiresUniqueSupport(t *testing.T) {
	session := &Session{}
	support := backchainDemandSupportFact{id: newFactID(1, 31), version: 2}
	first := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{support},
		slots: []factSlot{
			{value: newStringValue("q1"), ok: true},
		},
	}
	second := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		supportFacts: []backchainDemandSupportFact{support},
		slots: []factSlot{
			{value: newStringValue("q2"), ok: true},
		},
	}
	session.addBackchainDemandSupport(&workingFact{id: newFactID(1, 32)}, first)
	session.addBackchainDemandSupport(&workingFact{id: newFactID(1, 33)}, second)

	if id, ok := session.singleBackchainDemandSupportIDForRequest(first); ok {
		t.Fatalf("single support id = %d, want no fast path with multiple records", id)
	}
}

func TestBackchainDemandSupportRemovalUsesGraphOwnerHandle(t *testing.T) {
	session := &Session{}
	arena := newTokenArena()
	token := arena.addCompact(tokenRef{}, tokenRowEntry{bindingSlot: 0, factID: newFactID(1, 40), factVersion: 1}, conditionMatch{}, 1, 1)
	if token.isZero() {
		t.Fatal("token arena returned zero token")
	}
	support := backchainDemandSupportFact{id: newFactID(1, 41), version: 2}
	first := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		owner:        backchainDemandOwnerKey{nodeID: 1, planIndex: 0, token: token.handle},
		supportFacts: []backchainDemandSupportFact{support},
		slots: []factSlot{
			{value: newStringValue("q1"), ok: true},
		},
	}
	second := backchainDemandRequest{
		templateKey:  TemplateKey("answer"),
		owner:        backchainDemandOwnerKey{nodeID: 1, planIndex: 1, token: token.handle},
		supportFacts: []backchainDemandSupportFact{support},
		slots: []factSlot{
			{value: newStringValue("q2"), ok: true},
		},
	}
	firstID := session.addBackchainDemandSupport(&workingFact{id: newFactID(1, 42)}, first)
	secondID := session.addBackchainDemandSupport(&workingFact{id: newFactID(1, 43)}, second)
	if firstID == 0 || secondID == 0 || firstID == secondID {
		t.Fatalf("support ids = (%d, %d), want distinct non-zero", firstID, secondID)
	}
	if inlineKey, ok := backchainDemandInlineSupportKeyForRequest(first); !ok {
		t.Fatal("first request should be inline-key compatible")
	} else if _, exists := session.backchainDemandInlineSupports.get(inlineKey); exists {
		t.Fatal("graph-owned support was stored in inline request-key index")
	}
	if id, ok := session.singleBackchainDemandSupportIDForRequest(first); ok {
		t.Fatalf("single support id = %d, want owner path needed with ambiguous support fact bucket", id)
	}
	if _, ok := session.backchainDemandSupportRecordByID(firstID); ok {
		t.Fatalf("first owner support id %d was stored in generic support records", firstID)
	}
	if _, ok := session.backchainDemandSupportRecordByID(secondID); ok {
		t.Fatalf("second owner support id %d was stored in generic support records", secondID)
	}
	if _, ok := session.backchainDemandOwnerSupportRecordByID(firstID); !ok {
		t.Fatalf("first owner support id %d missing compact owner record", firstID)
	}
	if _, ok := session.backchainDemandOwnerSupportRecordByID(secondID); !ok {
		t.Fatalf("second owner support id %d missing compact owner record", secondID)
	}

	delta, err := session.removeBackchainDemandSupportForRequest(context.Background(), first, mutationOrigin{})
	if err != nil {
		t.Fatalf("removeBackchainDemandSupportForRequest: %v", err)
	}
	if !delta.supported {
		t.Fatalf("delta.supported = false, want true")
	}
	if _, ok := session.backchainDemandOwnerSupportRecordByID(firstID); ok {
		t.Fatalf("first owner support id %d still present", firstID)
	}
	if _, ok := session.backchainDemandOwnerSupportRecordByID(secondID); !ok {
		t.Fatalf("second owner support id %d was removed", secondID)
	}
	if id, ok := session.backchainDemandSupportByOwner(first.owner); ok {
		t.Fatalf("first owner index = %d, want removed", id)
	}
	if id, ok := session.backchainDemandSupportByOwner(second.owner); !ok || id != secondID {
		t.Fatalf("second owner index = (%d, %t), want (%d, true)", id, ok, secondID)
	}
}
