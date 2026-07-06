package engine

import "strings"

// String renders a derivation as an indented text tree, one fact per line, with
// its support state and — when a firing is known — the producing rule, the
// rendered action source, and any captured bindings. Truncated nodes are
// marked explicitly; children are indented under their fact.
func (d Derivation) String() string {
	var b strings.Builder
	d.writeTree(&b, 0)
	return b.String()
}

func (d Derivation) writeTree(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
	b.WriteString(derivationLine(d))
	b.WriteByte('\n')
	for _, child := range d.DependsOn {
		child.writeTree(b, depth+1)
	}
}

func derivationLine(d Derivation) string {
	var b strings.Builder
	if name := d.Fact.Name(); name != "" {
		b.WriteString(name)
		b.WriteByte(' ')
	}
	b.WriteString(d.Fact.ID().String())
	if d.Support != "" {
		b.WriteString(" [")
		b.WriteString(string(d.Support))
		b.WriteByte(']')
	}
	if firing := d.ProducedBy; firing != nil {
		if rule := firingRuleLabel(firing); rule != "" {
			b.WriteString(" <- rule ")
			b.WriteString(rule)
		}
		if firing.Action != "" {
			b.WriteString(" action ")
			b.WriteString(firing.Action)
		}
		if len(firing.Bindings) > 0 {
			b.WriteString(" {")
			writeBindingValues(&b, firing.Bindings)
			b.WriteByte('}')
			if firing.BindingsPartial {
				b.WriteString(" (partial)")
			}
		}
	}
	if d.Truncated {
		b.WriteString(" ... (truncated)")
	}
	return b.String()
}

// DOT renders a derivation as a Graphviz digraph: one node per distinct fact
// (deduplicated across diamonds) labeled with its name, id, and support state,
// and one edge per supporter labeled with the producing rule. Output is
// deterministic in pre-order traversal.
func (d Derivation) DOT() string {
	var b strings.Builder
	b.WriteString("digraph derivation {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box];\n")

	seenNode := make(map[FactID]struct{})
	seenEdge := make(map[string]struct{})
	var nodes []Derivation
	type dotEdge struct {
		from, to FactID
		label    string
	}
	var edges []dotEdge

	var walk func(n Derivation)
	walk = func(n Derivation) {
		id := n.Fact.ID()
		if _, ok := seenNode[id]; !ok {
			seenNode[id] = struct{}{}
			nodes = append(nodes, n)
		}
		rule := ""
		if n.ProducedBy != nil {
			rule = firingRuleLabel(n.ProducedBy)
		}
		for _, child := range n.DependsOn {
			key := id.String() + "\x00" + child.Fact.ID().String() + "\x00" + rule
			if _, ok := seenEdge[key]; !ok {
				seenEdge[key] = struct{}{}
				edges = append(edges, dotEdge{from: id, to: child.Fact.ID(), label: rule})
			}
			walk(child)
		}
	}
	walk(d)

	for _, n := range nodes {
		b.WriteString("  ")
		b.WriteString(dotQuote(n.Fact.ID().String()))
		b.WriteString(" [label=")
		b.WriteString(dotQuote(dotNodeLabel(n)))
		b.WriteString("];\n")
	}
	for _, e := range edges {
		b.WriteString("  ")
		b.WriteString(dotQuote(e.from.String()))
		b.WriteString(" -> ")
		b.WriteString(dotQuote(e.to.String()))
		if e.label != "" {
			b.WriteString(" [label=")
			b.WriteString(dotQuote(e.label))
			b.WriteString("]")
		}
		b.WriteString(";\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func dotNodeLabel(d Derivation) string {
	parts := make([]string, 0, 4)
	if name := d.Fact.Name(); name != "" {
		parts = append(parts, name)
	}
	parts = append(parts, d.Fact.ID().String())
	if d.Support != "" {
		parts = append(parts, "["+string(d.Support)+"]")
	}
	if d.Truncated {
		parts = append(parts, "(truncated)")
	}
	return strings.Join(parts, "\n")
}

func firingRuleLabel(firing *Firing) string {
	if firing.RuleName != "" {
		return firing.RuleName
	}
	if !firing.RuleID.IsZero() {
		return firing.RuleID.String()
	}
	return ""
}

func writeBindingValues(b *strings.Builder, bindings []BindingValue) {
	for i, binding := range bindings {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(binding.Name)
		b.WriteByte('=')
		traceWriteValue(b, binding.Value)
	}
}

func dotQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
