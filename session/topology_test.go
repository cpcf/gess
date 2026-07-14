package session_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestTopologyIsStableBoundedAndFocusable(t *testing.T) {
	ctx := context.Background()
	workspace := session.NewWorkspace()
	if err := workspace.AddAction(rules.ActionSpec{Name: "noop", Fn: func(rules.ActionContext) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddRule(rules.RuleSpec{
		ID:   "rule:adult",
		Name: "adult",
		Conditions: []rules.RuleConditionSpec{
			{Binding: "person", Target: rules.DynamicFact("person"), Source: rules.SourceSpan{Name: "rules.gess", StartLine: 2, StartColumn: 3}},
			{Binding: "age", Target: rules.DynamicFact("age"), Source: rules.SourceSpan{Name: "rules.gess", StartLine: 3, StartColumn: 3}},
		},
		Actions: []rules.RuleActionSpec{{Name: "noop"}},
	}); err != nil {
		t.Fatal(err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	first, err := session.New(revision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.New(revision)
	if err != nil {
		t.Fatal(err)
	}
	fullA, err := first.Topology(context.Background(), session.TopologyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	fullB, err := second.Topology(context.Background(), session.TopologyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if fullA.Schema != session.TopologySchemaVersion || fullA.Mode != session.TopologyModeFull {
		t.Fatalf("unexpected topology header: %#v", fullA)
	}
	if !reflect.DeepEqual(fullA.Nodes, fullB.Nodes) || !reflect.DeepEqual(fullA.Edges, fullB.Edges) {
		t.Fatal("equal compiled rulesets produced unstable topology")
	}
	for i := 1; i < len(fullA.Nodes); i++ {
		previous, current := fullA.Nodes[i-1], fullA.Nodes[i]
		if previous.Rank > current.Rank || previous.Rank == current.Rank && previous.ID > current.ID && previous.Kind == current.Kind {
			t.Fatalf("nodes are not deterministically ordered: %q before %q", previous.ID, current.ID)
		}
	}

	summary, err := first.Topology(context.Background(), session.TopologyRequest{MaxNodes: 1, MaxEdges: 1})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Mode != session.TopologyModeSummary || !summary.Truncated || len(summary.Nodes) != 0 || summary.Totals.Nodes != fullA.Totals.Nodes {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	focused, err := first.Topology(context.Background(), session.TopologyRequest{
		Selector: session.TopologySelector{RuleID: "rule:adult"},
		Radius:   1,
		MaxNodes: 3,
		MaxEdges: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if focused.Mode != session.TopologyModeFocused || !focused.Availability || len(focused.Nodes) == 0 || len(focused.Nodes) > 3 {
		t.Fatalf("unexpected focused topology: %#v", focused)
	}
	for _, node := range focused.Nodes {
		if node.ID == "" || node.Kind == "" {
			t.Fatalf("node lacks stable identity: %#v", node)
		}
	}

	missing, err := first.Topology(context.Background(), session.TopologyRequest{Selector: session.TopologySelector{NodeID: "rete:beta:missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Availability || missing.Reason != "node not found" {
		t.Fatalf("missing selector was not explicit: available=%t reason=%q", missing.Availability, missing.Reason)
	}
}
