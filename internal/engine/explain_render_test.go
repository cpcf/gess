package engine

import "testing"

func renderTestFact(id FactID, name string, state FactSupportState) FactSnapshot {
	return FactSnapshot{id: id, name: name, generation: id.Generation(), support: FactSupportProvenance{State: state}}
}

func sampleDerivation() Derivation {
	return Derivation{
		Fact:    renderTestFact(newFactID(1, 2), "record", FactSupportStatedAndLogical),
		Support: FactSupportStatedAndLogical,
		ProducedBy: &Firing{
			RuleName:        "advance",
			Action:          `(modify ?r (set (status "active")))`,
			BindingsPartial: true,
		},
		DependsOn: []Derivation{
			{
				Fact:    renderTestFact(newFactID(1, 1), "trigger", FactSupportStated),
				Support: FactSupportStated,
			},
		},
	}
}

func TestDerivationStringGolden(t *testing.T) {
	got := sampleDerivation().String()
	want := "record fact:g1:2 [stated_and_logical] <- rule advance action (modify ?r (set (status \"active\")))\n" +
		"  trigger fact:g1:1 [stated]\n"
	if got != want {
		t.Fatalf("String() mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDerivationStringRendersBindingFactReferences(t *testing.T) {
	d := Derivation{
		Fact:    renderTestFact(newFactID(1, 3), "record", FactSupportStated),
		Support: FactSupportStated,
		ProducedBy: &Firing{
			RuleName: "advance",
			Action:   `(assert (record (id ?id)))`,
			Bindings: []BindingValue{
				{Name: "?__gess1", FromFact: newFactID(1, 1)},
				{Name: "?id", FromFact: newFactID(1, 1), Value: newStringValue("R-100")},
				{Name: "?count", Value: newIntValue(2)},
			},
		},
	}

	want := "record fact:g1:3 [stated] <- rule advance action (assert (record (id ?id))) {?__gess1=fact:g1:1, ?id=fact:g1:1(\"R-100\"), ?count=2}\n"
	if got := d.String(); got != want {
		t.Fatalf("String() mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDerivationStringTruncated(t *testing.T) {
	d := Derivation{
		Fact:      renderTestFact(newFactID(1, 5), "loop", FactSupportLogical),
		Support:   FactSupportLogical,
		Truncated: true,
	}
	want := "loop fact:g1:5 [logical] ... (truncated)\n"
	if got := d.String(); got != want {
		t.Fatalf("truncated String() = %q, want %q", got, want)
	}
}

func TestDerivationDOTGolden(t *testing.T) {
	got := sampleDerivation().DOT()
	want := "digraph derivation {\n" +
		"  rankdir=LR;\n" +
		"  node [shape=box];\n" +
		"  \"fact:g1:2\" [label=\"record\\nfact:g1:2\\n[stated_and_logical]\"];\n" +
		"  \"fact:g1:1\" [label=\"trigger\\nfact:g1:1\\n[stated]\"];\n" +
		"  \"fact:g1:2\" -> \"fact:g1:1\" [label=\"advance\"];\n" +
		"}\n"
	if got != want {
		t.Fatalf("DOT() mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDerivationDOTDeduplicatesDiamond(t *testing.T) {
	a := newFactID(1, 1)
	b := newFactID(1, 2)
	c := newFactID(1, 3)
	d := newFactID(1, 4)
	derivation := Derivation{
		Fact:       renderTestFact(a, "a", FactSupportLogical),
		Support:    FactSupportLogical,
		ProducedBy: &Firing{RuleName: "ra"},
		DependsOn: []Derivation{
			{
				Fact:       renderTestFact(b, "b", FactSupportLogical),
				Support:    FactSupportLogical,
				ProducedBy: &Firing{RuleName: "rb"},
				DependsOn: []Derivation{{
					Fact:    renderTestFact(d, "d", FactSupportStated),
					Support: FactSupportStated,
				}},
			},
			{
				Fact:       renderTestFact(c, "c", FactSupportLogical),
				Support:    FactSupportLogical,
				ProducedBy: &Firing{RuleName: "rc"},
				DependsOn: []Derivation{{
					Fact:      renderTestFact(d, "d", FactSupportStated),
					Support:   FactSupportStated,
					Truncated: true,
				}},
			},
		},
	}
	out := derivation.DOT()
	// d must appear as exactly one node declaration despite two references
	// (node lines are indented with two spaces; edge lines have an arrow).
	if n := countSubstring(out, "\n  \"fact:g1:4\" [label="); n != 1 {
		t.Fatalf("diamond node fact:g1:4 emitted %d times, want 1\n%s", n, out)
	}
	// both edges into d are present (distinct rule labels).
	if !contains(out, "\"fact:g1:2\" -> \"fact:g1:4\"") || !contains(out, "\"fact:g1:3\" -> \"fact:g1:4\"") {
		t.Fatalf("missing diamond edges into d:\n%s", out)
	}
}

func contains(haystack, needle string) bool {
	return countSubstring(haystack, needle) > 0
}

func countSubstring(haystack, needle string) int {
	count := 0
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			count++
		}
	}
	return count
}
