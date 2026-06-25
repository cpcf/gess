package gess

import "testing"

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
