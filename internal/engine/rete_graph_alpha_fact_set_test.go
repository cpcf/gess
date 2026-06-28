package engine

import "testing"

func TestReteGraphAlphaFactSetInlineOverflowPromotion(t *testing.T) {
	var set reteGraphAlphaFactSet
	ids := []FactID{
		newFactID(1, 1),
		newFactID(1, 2),
		newFactID(1, 3),
		newFactID(1, 4),
		newFactID(1, 5),
	}
	for _, id := range ids {
		if !set.insert(id) {
			t.Fatalf("insert(%v) = false, want true", id)
		}
	}
	if set.insert(ids[4]) {
		t.Fatalf("duplicate insert(%v) = true, want false", ids[4])
	}
	for _, id := range ids {
		if !set.contains(id) {
			t.Fatalf("contains(%v) = false, want true", id)
		}
	}
	if !set.remove(ids[1]) {
		t.Fatalf("remove(%v) = false, want true", ids[1])
	}
	if set.contains(ids[1]) {
		t.Fatalf("contains(%v) = true after remove, want false", ids[1])
	}
	if !set.contains(ids[4]) {
		t.Fatalf("contains(%v) = false after overflow promotion, want true", ids[4])
	}
	if set.remove(ids[1]) {
		t.Fatalf("second remove(%v) = true, want false", ids[1])
	}

	set.clear()
	for _, id := range ids {
		if set.contains(id) {
			t.Fatalf("contains(%v) = true after clear, want false", id)
		}
	}
}
