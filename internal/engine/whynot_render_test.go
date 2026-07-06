package engine

import "testing"

func TestWhyNotReportStringNeverMatched(t *testing.T) {
	report := WhyNotReport{
		RuleName: "ship-order",
		Outcome:  WhyNotNeverMatched,
		Branches: []WhyNotBranch{{
			BranchID:     0,
			FirstFailing: 1,
			Conditions: []WhyNotCondition{
				{Order: 0, Binding: "a", Satisfied: true, AlphaMatches: 1},
				{Order: 1, Binding: "b", AlphaMatches: 2, Reason: WhyNotReasonJoinMismatch},
			},
			PartialMatches: []WhyNotPartialMatch{{
				Facts:    []FactID{newFactID(1, 1)},
				Bindings: []BindingValue{{Name: "?a", FromFact: newFactID(1, 1)}},
			}},
		}},
	}
	want := "rule ship-order never matched: condition 1 ?b failed (join_mismatch)\n" +
		"  branch 0:\n" +
		"    [ok]      0 ?a (alpha 1)\n" +
		"    [FAIL]    1 ?b (alpha 2) -- join_mismatch\n" +
		"    nearest: ?a=fact:g1:1\n"
	if got := report.String(); got != want {
		t.Fatalf("String() mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestWhyNotReportStringBlocked(t *testing.T) {
	report := WhyNotReport{
		RuleName: "escalate",
		Outcome:  WhyNotBlocked,
		Branches: []WhyNotBranch{{
			BranchID:     0,
			FirstFailing: 1,
			Conditions: []WhyNotCondition{
				{Order: 0, Binding: "a", Satisfied: true, AlphaMatches: 1},
				{Order: 1, Binding: "alert", Negated: true, Reason: WhyNotReasonNegationBlocked, Blockers: []FactID{newFactID(1, 12)}, BlockerCount: 1},
			},
		}},
	}
	want := "rule escalate blocked by [fact:g1:12] at condition 1 not ?alert\n" +
		"  branch 0:\n" +
		"    [ok]      0 ?a (alpha 1)\n" +
		"    [blocked] 1 not ?alert -- negation_blocked by [fact:g1:12]\n"
	if got := report.String(); got != want {
		t.Fatalf("String() mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestWhyNotReportStringActivatedAndFired(t *testing.T) {
	activated := WhyNotReport{RuleName: "r", Outcome: WhyNotActivated, Activations: nil}
	if got := activated.String(); got != "rule r is activated: 0 pending activation(s)\n" {
		t.Fatalf("activated String() = %q", got)
	}
	fired := WhyNotReport{RuleName: "r", Outcome: WhyNotAlreadyFired}
	if got := fired.String(); got != "rule r already fired and is refracted\n" {
		t.Fatalf("fired String() = %q", got)
	}
}
