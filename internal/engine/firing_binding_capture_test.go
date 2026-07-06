package engine

import (
	"context"
	"testing"
)

func TestFiringBindingCaptureRecordsExactBindings(t *testing.T) {
	revision, triggerKey, _ := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("capture"), WithExplainLog())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.AssertTemplate(context.Background(), triggerKey, mustFields(t, map[string]any{"id": "t-1"})); err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot := mustSnapshot(t, context.Background(), session)
	record := singleFact(t, snapshot, "record")

	derivation, err := session.Explain(context.Background(), record.ID())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(derivation.History) != 2 {
		t.Fatalf("History len = %d, want 2", len(derivation.History))
	}
	assert := derivation.History[0]
	if assert.Firing == nil || assert.Firing.BindingsPartial {
		t.Fatalf("assert firing = %+v, want captured (not partial)", assert.Firing)
	}
	if !hasBinding(assert.Firing.Bindings, "?t") {
		t.Fatalf("assert firing bindings = %+v, want the ?t trigger binding", assert.Firing.Bindings)
	}
	if derivation.ProducedBy == nil || derivation.ProducedBy.BindingsPartial {
		t.Fatalf("ProducedBy = %+v, want captured bindings", derivation.ProducedBy)
	}
}

func TestFiringBindingCaptureDeepCopiesValues(t *testing.T) {
	list := mustValue(t, []any{"a", "b"})
	captured := cloneBindingValues([]BindingValue{{Name: "?xs", Value: list}})
	// Mutate the original backing slice; the captured copy must be unaffected.
	if raw, ok := list.data.([]Value); ok {
		raw[0] = newStringValue("mutated")
	}
	got, ok := captured[0].Value.data.([]Value)
	if !ok || len(got) != 2 {
		t.Fatalf("captured list = %+v, want a 2-element clone", captured[0].Value)
	}
	if first, _ := got[0].AsString(); first != "a" {
		t.Fatalf("captured list mutated to %q, want deep copy preserving \"a\"", first)
	}
}

func TestFiringBindingCaptureEvictionDegrades(t *testing.T) {
	revision, _, recordKey := lineageRuleset(t)
	session, err := NewSession(revision, WithSessionID("capture-evict"), WithExplainLog(WithExplainLogMaxEntries(1)))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Drive several external mutations to overflow the tiny ring.
	record, err := session.AssertTemplate(context.Background(), recordKey, mustFields(t, map[string]any{"id": "r-1", "status": "open"}))
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	for _, status := range []string{"active", "closed"} {
		if _, err := session.Modify(context.Background(), record.Fact.ID(), FactPatch{Set: Fields{"status": newStringValue(status)}}); err != nil {
			t.Fatalf("Modify(%s): %v", status, err)
		}
	}
	// Explain still succeeds (degraded), never panics.
	derivation, err := session.Explain(context.Background(), record.Fact.ID())
	if err != nil {
		t.Fatalf("Explain after eviction: %v", err)
	}
	if !derivation.Truncated {
		t.Fatalf("expected Truncated history after eviction")
	}
}

// BenchmarkSessionFiringBindingCapture measures per-firing capture overhead
// with a log attached. The no-log fire path is BenchmarkSessionRuleActionAttribution,
// which never allocates capture state (the capture is a single nil check).
func BenchmarkSessionFiringBindingCapture(b *testing.B) {
	revision, triggerKey, _ := lineageRuleset(b)
	session, err := NewSession(revision, WithSessionID("capture-bench"), WithExplainLog())
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	fields := mustFields(b, map[string]any{"id": "t-1"})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.AssertTemplate(ctx, triggerKey, fields); err != nil {
			b.Fatalf("AssertTemplate: %v", err)
		}
		if _, err := session.Run(ctx); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if _, err := session.Reset(ctx); err != nil {
			b.Fatalf("Reset: %v", err)
		}
	}
}

func hasBinding(bindings []BindingValue, name string) bool {
	for _, binding := range bindings {
		if binding.Name == name {
			return true
		}
	}
	return false
}
