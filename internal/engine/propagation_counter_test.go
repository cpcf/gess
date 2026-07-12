package engine

import (
	"reflect"
	"testing"
)

func TestTokenRowAllocationSourceSnapshotIsDeterministic(t *testing.T) {
	ledger := newPropagationCounterLedger()
	alpha := reteGraphStageRef{kind: reteGraphStageAlpha, id: 2}
	beta := reteGraphStageRef{kind: reteGraphStageBeta, id: 1}

	ledger.recordTokenRowAllocated(beta, propagationAllocationSource{templateKey: "zeta", kind: propagationMutationModify})
	ledger.recordTokenRowAllocated(alpha, propagationAllocationSource{templateKey: "item", kind: propagationMutationRetract})
	ledger.recordTokenRowAllocated(alpha, propagationAllocationSource{templateKey: "item", kind: propagationMutationAssert})
	ledger.recordTokenRowAllocated(alpha, propagationAllocationSource{templateKey: "alpha", kind: propagationMutationModify})

	want := []propagationTokenRowSourceCount{
		{Source: propagationTokenRowSourceKey{Stage: alpha, TemplateKey: "alpha", Kind: propagationMutationModify}, Count: 1},
		{Source: propagationTokenRowSourceKey{Stage: alpha, TemplateKey: "item", Kind: propagationMutationAssert}, Count: 1},
		{Source: propagationTokenRowSourceKey{Stage: alpha, TemplateKey: "item", Kind: propagationMutationRetract}, Count: 1},
		{Source: propagationTokenRowSourceKey{Stage: beta, TemplateKey: "zeta", Kind: propagationMutationModify}, Count: 1},
	}
	first := ledger.snapshot()
	second := ledger.snapshot()
	if !reflect.DeepEqual(first.TokenRowsBySource, want) {
		t.Fatalf("token rows by source = %#v, want %#v", first.TokenRowsBySource, want)
	}
	if !reflect.DeepEqual(first.TokenRowsBySource, second.TokenRowsBySource) {
		t.Fatalf("source snapshots differ: first=%#v second=%#v", first.TokenRowsBySource, second.TokenRowsBySource)
	}
}

func TestTokenRowAllocationWithoutSourcePreservesStageTotals(t *testing.T) {
	ledger := newPropagationCounterLedger()
	stage := reteGraphStageRef{kind: reteGraphStageRoot}
	ledger.recordTokenRowAllocated(stage, propagationAllocationSource{})

	snapshot := ledger.snapshot()
	if snapshot.Totals.TokenRowsAllocated != 1 {
		t.Fatalf("token rows allocated = %d, want 1", snapshot.Totals.TokenRowsAllocated)
	}
	if got, want := snapshot.TokenRowsByStage, []propagationStageCount{{Stage: stage, Count: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("token rows by stage = %#v, want %#v", got, want)
	}
	if snapshot.TokenRowsBySource != nil {
		t.Fatalf("token rows by source = %#v, want nil", snapshot.TokenRowsBySource)
	}
}
