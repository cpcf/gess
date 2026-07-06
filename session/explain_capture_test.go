package session_test

import (
	"context"
	"os"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

// TestExplainCapturesComputedBinding drives the order_lifecycle .gess ruleset,
// whose ship-order rule binds a computed scalar with (bind ?total (+ ?subtotal
// ?tax)). Firing-time capture must report the exact value with
// BindingsPartial=false.
func TestExplainCapturesComputedBinding(t *testing.T) {
	ctx := context.Background()
	source, err := os.ReadFile("../examples/gess-files/order_lifecycle/rules.gess")
	if err != nil {
		t.Fatalf("read ruleset: %v", err)
	}
	doc, err := dsl.Parse("rules.gess", source)
	if err != nil {
		t.Fatalf("dsl.Parse: %v", err)
	}
	workspace := rules.NewWorkspace()
	if err := dsl.Load(ctx, workspace, doc, dsl.Registry{}); err != nil {
		t.Fatalf("dsl.Load: %v", err)
	}
	ruleset, err := rules.Compile(ctx, workspace)
	if err != nil {
		t.Fatalf("rules.Compile: %v", err)
	}

	sess, err := session.New(ruleset, session.WithInitialFacts(dsl.InitialFacts(doc)...), session.WithExplainLog())
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer sess.Close()
	if _, err := sess.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snapshot, err := sess.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	orders := snapshot.FactsByName("order")
	if len(orders) != 1 {
		t.Fatalf("orders = %d, want 1 shipped order", len(orders))
	}
	shipped := orders[0]

	derivation, err := sess.Explain(ctx, shipped.ID())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if derivation.ProducedBy == nil {
		t.Fatalf("ProducedBy nil, want ship-order firing")
	}
	if derivation.ProducedBy.BindingsPartial {
		t.Fatalf("BindingsPartial = true, want false with firing-time capture")
	}

	var total int64 = -1
	found := false
	for _, binding := range derivation.ProducedBy.Bindings {
		if binding.Name == "?total" {
			found = true
			total, _ = binding.Value.AsInt64()
		}
	}
	if !found {
		t.Fatalf("captured bindings %+v missing computed ?total", derivation.ProducedBy.Bindings)
	}
	if total != 108 {
		t.Fatalf("captured ?total = %d, want 108 (subtotal 100 + tax 8)", total)
	}
}
