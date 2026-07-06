package engine

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

func TestSnapshotExplainLogicalChain(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session := mustSession(t, revision, "explain-chain-session")

	if _, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	child := singleFact(t, snapshot, "child")
	derived := singleFact(t, snapshot, "derived")
	source := singleFact(t, snapshot, "source")

	derivation, ok := snapshot.Explain(child.ID())
	if !ok {
		t.Fatalf("Explain(child) ok = false, want true")
	}
	if derivation.Support != FactSupportLogical {
		t.Fatalf("child support = %q, want %q", derivation.Support, FactSupportLogical)
	}
	if derivation.ProducedBy == nil || derivation.ProducedBy.RuleName != "derive-child" {
		t.Fatalf("child ProducedBy = %+v, want rule derive-child", derivation.ProducedBy)
	}
	if len(derivation.DependsOn) != 1 {
		t.Fatalf("child DependsOn len = %d, want 1", len(derivation.DependsOn))
	}

	derivedNode := derivation.DependsOn[0]
	if derivedNode.Fact.ID() != derived.ID() {
		t.Fatalf("child depends on %v, want derived %v", derivedNode.Fact.ID(), derived.ID())
	}
	if derivedNode.Support != FactSupportLogical {
		t.Fatalf("derived support = %q, want %q", derivedNode.Support, FactSupportLogical)
	}
	if derivedNode.ProducedBy == nil || derivedNode.ProducedBy.RuleName != "derive" {
		t.Fatalf("derived ProducedBy = %+v, want rule derive", derivedNode.ProducedBy)
	}
	if len(derivedNode.DependsOn) != 1 {
		t.Fatalf("derived DependsOn len = %d, want 1", len(derivedNode.DependsOn))
	}

	sourceNode := derivedNode.DependsOn[0]
	if sourceNode.Fact.ID() != source.ID() {
		t.Fatalf("derived depends on %v, want source %v", sourceNode.Fact.ID(), source.ID())
	}
	if sourceNode.Support != FactSupportStated {
		t.Fatalf("source support = %q, want %q", sourceNode.Support, FactSupportStated)
	}
	if sourceNode.ProducedBy != nil {
		t.Fatalf("source ProducedBy = %+v, want nil at tier 1 for a stated fact", sourceNode.ProducedBy)
	}
	if len(sourceNode.DependsOn) != 0 || sourceNode.Truncated {
		t.Fatalf("source node = %+v, want leaf and not truncated", sourceNode)
	}
	if !reflect.DeepEqual(derivation, mustExplain(t, snapshot, child.ID())) {
		t.Fatalf("Explain not deterministic across calls")
	}
}

func TestSnapshotExplainRetractCascade(t *testing.T) {
	revision, sourceKey, _, _ := mustLogicalSupportRuleset(t, false)
	session := mustSession(t, revision, "explain-cascade-session")

	source, err := session.AssertTemplate(context.Background(), sourceKey, mustFields(t, map[string]any{"id": "s-1"}))
	if err != nil {
		t.Fatalf("AssertTemplate(source): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before := mustSnapshot(t, context.Background(), session)
	childID := singleFact(t, before, "child").ID()

	if _, err := session.Retract(context.Background(), source.Fact.ID()); err != nil {
		t.Fatalf("Retract(source): %v", err)
	}
	after := mustSnapshot(t, context.Background(), session)
	if _, ok := after.Explain(childID); ok {
		t.Fatalf("Explain(child) after cascade retract ok = true, want false")
	}
}

func TestSnapshotExplainMissingFact(t *testing.T) {
	snapshot := explainTestSnapshot(nil, nil)
	if _, ok := snapshot.Explain(newFactID(1, 999)); ok {
		t.Fatalf("Explain(missing) ok = true, want false")
	}
}

func TestSnapshotExplainCycleTerminates(t *testing.T) {
	a := newFactID(1, 1)
	b := newFactID(1, 2)
	facts := []FactSnapshot{
		explainTestFact(a, "a", FactSupportLogical),
		explainTestFact(b, "b", FactSupportLogical),
	}
	edges := []LogicalSupportEdge{
		{SupportID: "s1", FactID: a, SupportingFacts: []FactID{b}},
		{SupportID: "s2", FactID: b, SupportingFacts: []FactID{a}},
	}
	snapshot := explainTestSnapshot(facts, edges)

	derivation, ok := snapshot.Explain(a)
	if !ok {
		t.Fatalf("Explain(a) ok = false, want true")
	}
	// a -> b -> a(truncated, cycle revisit)
	if len(derivation.DependsOn) != 1 {
		t.Fatalf("a DependsOn len = %d, want 1", len(derivation.DependsOn))
	}
	bNode := derivation.DependsOn[0]
	if len(bNode.DependsOn) != 1 {
		t.Fatalf("b DependsOn len = %d, want 1", len(bNode.DependsOn))
	}
	revisit := bNode.DependsOn[0]
	if revisit.Fact.ID() != a || !revisit.Truncated {
		t.Fatalf("cycle revisit = %+v, want a truncated", revisit)
	}
	if len(revisit.DependsOn) != 0 {
		t.Fatalf("cycle revisit recursed: %+v", revisit)
	}
}

func TestSnapshotExplainDiamondTruncatesRevisit(t *testing.T) {
	a := newFactID(1, 1)
	b := newFactID(1, 2)
	c := newFactID(1, 3)
	d := newFactID(1, 4)
	facts := []FactSnapshot{
		explainTestFact(a, "a", FactSupportLogical),
		explainTestFact(b, "b", FactSupportLogical),
		explainTestFact(c, "c", FactSupportLogical),
		explainTestFact(d, "d", FactSupportStated),
	}
	edges := []LogicalSupportEdge{
		{SupportID: "s1", FactID: a, SupportingFacts: []FactID{b}},
		{SupportID: "s2", FactID: a, SupportingFacts: []FactID{c}},
		{SupportID: "s3", FactID: b, SupportingFacts: []FactID{d}},
		{SupportID: "s4", FactID: c, SupportingFacts: []FactID{d}},
	}
	snapshot := explainTestSnapshot(facts, edges)

	derivation, ok := snapshot.Explain(a)
	if !ok {
		t.Fatalf("Explain(a) ok = false")
	}
	if len(derivation.DependsOn) != 2 {
		t.Fatalf("a DependsOn len = %d, want 2 (b, c)", len(derivation.DependsOn))
	}
	bNode := derivation.DependsOn[0]
	cNode := derivation.DependsOn[1]
	dUnderB := bNode.DependsOn[0]
	dUnderC := cNode.DependsOn[0]
	if dUnderB.Truncated {
		t.Fatalf("first visit of d truncated: %+v", dUnderB)
	}
	if !dUnderC.Truncated || len(dUnderC.DependsOn) != 0 {
		t.Fatalf("second visit of d = %+v, want truncated shallow", dUnderC)
	}
}

func TestSnapshotExplainCaps(t *testing.T) {
	// Straight chain a -> b -> c -> d, all logical except the stated leaf.
	ids := []FactID{newFactID(1, 1), newFactID(1, 2), newFactID(1, 3), newFactID(1, 4)}
	facts := []FactSnapshot{
		explainTestFact(ids[0], "a", FactSupportLogical),
		explainTestFact(ids[1], "b", FactSupportLogical),
		explainTestFact(ids[2], "c", FactSupportLogical),
		explainTestFact(ids[3], "d", FactSupportStated),
	}
	edges := []LogicalSupportEdge{
		{SupportID: "s1", FactID: ids[0], SupportingFacts: []FactID{ids[1]}},
		{SupportID: "s2", FactID: ids[1], SupportingFacts: []FactID{ids[2]}},
		{SupportID: "s3", FactID: ids[2], SupportingFacts: []FactID{ids[3]}},
	}
	snapshot := explainTestSnapshot(facts, edges)

	depthCapped, _ := snapshot.Explain(ids[0], WithExplainMaxDepth(2))
	// depth 0: a, depth 1: b, depth 2: c -> truncated (has an edge, not expanded).
	node := depthCapped
	for range [2]struct{}{} {
		if len(node.DependsOn) != 1 {
			t.Fatalf("depth-capped chain broke early at %+v", node)
		}
		node = node.DependsOn[0]
	}
	if !node.Truncated || len(node.DependsOn) != 0 {
		t.Fatalf("depth cap node = %+v, want truncated shallow", node)
	}

	nodeCapped, _ := snapshot.Explain(ids[0], WithExplainMaxNodes(2))
	if !hasTruncated(nodeCapped) {
		t.Fatalf("node cap did not surface Truncated: %+v", nodeCapped)
	}
}

func TestSnapshotExplainEdgelessSnapshot(t *testing.T) {
	id := newFactID(1, 1)
	snapshot := newSnapshot("s", "r", 1, []FactSnapshot{explainTestFact(id, "a", FactSupportStated)})
	derivation, ok := snapshot.Explain(id)
	if !ok {
		t.Fatalf("Explain(edgeless) ok = false")
	}
	if len(derivation.DependsOn) != 0 || derivation.ProducedBy != nil {
		t.Fatalf("edgeless derivation = %+v, want empty DependsOn and nil ProducedBy", derivation)
	}
}

func explainTestFact(id FactID, name string, state FactSupportState) FactSnapshot {
	return FactSnapshot{
		id:         id,
		name:       name,
		generation: id.generation,
		support:    FactSupportProvenance{State: state},
	}
}

func explainTestSnapshot(facts []FactSnapshot, edges []LogicalSupportEdge) Snapshot {
	return Snapshot{
		facts:   facts,
		byID:    snapshotIDIndex(facts),
		support: SupportGraph{Edges: edges},
	}
}

func hasTruncated(d Derivation) bool {
	return d.Truncated || slices.ContainsFunc(d.DependsOn, hasTruncated)
}

func singleFact(t *testing.T, snapshot Snapshot, name string) FactSnapshot {
	t.Helper()
	facts := snapshot.FactsByName(name)
	if len(facts) != 1 {
		t.Fatalf("facts named %q = %d, want 1", name, len(facts))
	}
	return facts[0]
}

func mustExplain(t *testing.T, snapshot Snapshot, id FactID) Derivation {
	t.Helper()
	derivation, ok := snapshot.Explain(id)
	if !ok {
		t.Fatalf("Explain(%v) ok = false", id)
	}
	return derivation
}
