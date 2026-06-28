package main

import "testing"

func TestEvaluateProgressFindsNextCheckpoint(t *testing.T) {
	output := "setup: templates\n" +
		"setup: facts\n" +
		"setup: queries\n" +
		"emergency: VULN-100 critical-exploitable-internet\n" +
		"accepted-risk: VULN-200 compensating-control\n"
	got := evaluateProgress(output, checkpoints)
	if len(got.Complete) != 5 {
		t.Fatalf("complete = %d, want 5", len(got.Complete))
	}
	if got.Next == nil || got.Next.Number != 6 {
		t.Fatalf("next = %+v, want checkpoint 6", got.Next)
	}
}

func TestEvaluateProgressComplete(t *testing.T) {
	output := "setup: templates\n" +
		"setup: facts\n" +
		"setup: queries\n" +
		"emergency: VULN-100 critical-exploitable-internet\n" +
		"accepted-risk: VULN-200 compensating-control\n" +
		"and: VULN-400 critical-nonexploited\n" +
		"or: VULN-500 dependency-or-exposure-watch\n" +
		"exists: APP-100 asset-has-critical\n" +
		"forall: APP-300 asset-under-limit\n" +
		"standard: VULN-300 normal-remediation\n" +
		"summary: critical count=2 total=195\n" +
		"recorded: VULN-100/critical-exploitable-internet\n"
	got := evaluateProgress(output, checkpoints)
	if len(got.Complete) != len(checkpoints) {
		t.Fatalf("complete = %d, want %d", len(got.Complete), len(checkpoints))
	}
	if got.Next != nil {
		t.Fatalf("next = %+v, want nil", got.Next)
	}
}
