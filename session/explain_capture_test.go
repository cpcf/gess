package session_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestWithWhatIfExplainAcceptsLogOptions(t *testing.T) {
	if option := session.WithWhatIfExplain(session.WithExplainLogMaxEntries(17)); option == nil {
		t.Fatal("WithWhatIfExplain returned nil")
	}
}

func TestExplainJSONThroughFacade(t *testing.T) {
	ctx := context.Background()
	source, err := os.ReadFile("../examples/gess-files/order_lifecycle/rules.gess")
	if err != nil {
		t.Fatalf("read ruleset: %v", err)
	}
	doc, err := dsl.Parse("rules.gess", source)
	if err != nil {
		t.Fatalf("dsl.Parse: %v", err)
	}
	workspace := session.NewWorkspace()
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
	order := snapshot.FactsByName("order")[0]

	derivation, err := sess.Explain(ctx, order.ID())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	raw, err := json.Marshal(derivation)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["gessExplainSchema"] != float64(session.ExplainSchemaVersion) {
		t.Fatalf("schema = %v, want %d", parsed["gessExplainSchema"], session.ExplainSchemaVersion)
	}
	if parsed["kind"] != "derivation" {
		t.Fatalf("kind = %v, want derivation", parsed["kind"])
	}
	if _, ok := parsed["fact"].(map[string]any)["id"]; !ok {
		t.Fatalf("fact.id missing in %s", raw)
	}
}

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
	workspace := session.NewWorkspace()
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

func TestRuntimeFacadeWrapsEventsQueriesAndReports(t *testing.T) {
	ctx := context.Background()
	source, err := os.ReadFile("../examples/gess-files/order_lifecycle/rules.gess")
	if err != nil {
		t.Fatalf("read ruleset: %v", err)
	}
	doc, err := dsl.Parse("rules.gess", source)
	if err != nil {
		t.Fatalf("dsl.Parse: %v", err)
	}
	workspace := session.NewWorkspace()
	if err := dsl.Load(ctx, workspace, doc, dsl.Registry{}); err != nil {
		t.Fatalf("dsl.Load: %v", err)
	}
	ruleset, err := rules.Compile(ctx, workspace)
	if err != nil {
		t.Fatalf("rules.Compile: %v", err)
	}

	var modified session.Event
	sess, err := session.New(
		ruleset,
		session.WithInitialFacts(dsl.InitialFacts(doc)...),
		session.WithEventListener(session.EventFunc(func(_ context.Context, event session.Event) error {
			if event.Type == session.EventFactModified && event.Delta != nil && event.Delta.After != nil {
				modified = event
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	defer sess.Close()

	run, err := sess.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Fired == 0 {
		t.Fatal("Run fired 0 activations, want fixture rules to fire")
	}
	if modified.Delta == nil || modified.Delta.After == nil {
		t.Fatalf("modified event did not expose public mutation delta: %+v", modified)
	}
	status, ok := modified.Delta.After.Field("status")
	if !ok {
		t.Fatalf("modified event after fact missing status field")
	}
	statusText, ok := status.AsString()
	if !ok || statusText != "shipped-W-1" {
		t.Fatalf("modified status = %s ok=%v, want shipped-W-1 true", status.String(), ok)
	}

	rows, err := sess.QueryAll(ctx, "shipped-orders", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 shipped order", len(rows))
	}
	id, ok := rows[0].Value("id")
	if !ok {
		t.Fatalf("query row missing id value; aliases=%v", rows[0].Aliases())
	}
	idText, ok := id.AsString()
	if !ok || idText != "O-1" {
		t.Fatalf("query id = %s ok=%v, want O-1 true", id.String(), ok)
	}
	total, ok := rows[0].Value("total")
	if !ok {
		t.Fatalf("query row missing total value; aliases=%v", rows[0].Aliases())
	}
	totalInt, ok := total.AsInt64()
	if !ok || totalInt != 108 {
		t.Fatalf("query total = %s ok=%v, want 108 true", total.String(), ok)
	}

	report, err := sess.WhyNot(ctx, "ship-order")
	if err != nil {
		t.Fatalf("WhyNot: %v", err)
	}
	if report.String() == "" {
		t.Fatal("WhyNotReport.String returned empty output")
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("json.Marshal(WhyNotReport): %v", err)
	}
}
