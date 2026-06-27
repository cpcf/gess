package gess

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
