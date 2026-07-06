package engine

import (
	"fmt"
	"strings"
)

// String renders a why-not diagnosis as an answer: an outcome-first headline,
// then a per-branch condition table with [ok]/[FAIL]/[blocked] markers, alpha
// counts, the failing condition's reason and source location, and the nearest
// partial matches with their bound values.
func (r WhyNotReport) String() string {
	var b strings.Builder
	b.WriteString(whyNotHeadline(r))
	b.WriteByte('\n')

	switch r.Outcome {
	case WhyNotActivated:
		for _, act := range r.Activations {
			fmt.Fprintf(&b, "  activation %s facts=%s\n", act.ActivationID(), factIDList(act.FactIDs()))
		}
	case WhyNotAlreadyFired:
	default:
		for _, branch := range r.Branches {
			writeWhyNotBranch(&b, branch)
		}
	}
	if r.Truncated {
		b.WriteString("  ... (truncated: probe or partial-match cap reached)\n")
	}
	return b.String()
}

func whyNotHeadline(r WhyNotReport) string {
	name := r.RuleName
	if name == "" {
		name = r.RuleID.String()
	}
	switch r.Outcome {
	case WhyNotActivated:
		return fmt.Sprintf("rule %s is activated: %d pending activation(s)", name, len(r.Activations))
	case WhyNotAlreadyFired:
		return fmt.Sprintf("rule %s already fired and is refracted", name)
	case WhyNotBlocked:
		if cond, ok := primaryFailure(r); ok {
			return fmt.Sprintf("rule %s blocked by %s at condition %d %s%s",
				name, blockerLabel(cond), cond.Order, negationPrefix(cond), conditionLabel(cond))
		}
		return fmt.Sprintf("rule %s blocked", name)
	default:
		if cond, ok := primaryFailure(r); ok {
			line := fmt.Sprintf("rule %s never matched: condition %d %s%s failed (%s)",
				name, cond.Order, negationPrefix(cond), conditionLabel(cond), cond.Reason)
			if loc := sourceSpanLocation(cond.RejectingSpan); loc != "" {
				line += " at " + loc
			}
			return line
		}
		return fmt.Sprintf("rule %s never matched", name)
	}
}

func writeWhyNotBranch(b *strings.Builder, branch WhyNotBranch) {
	fmt.Fprintf(b, "  branch %d:\n", branch.BranchID)
	for i, cond := range branch.Conditions {
		marker := "[ ]"
		if cond.Satisfied {
			marker = "[ok]"
		}
		if i == branch.FirstFailing {
			if cond.Reason == WhyNotReasonNegationBlocked {
				marker = "[blocked]"
			} else {
				marker = "[FAIL]"
			}
		}
		fmt.Fprintf(b, "    %-9s %d %s%s", marker, cond.Order, negationPrefix(cond), conditionLabel(cond))
		if !cond.Negated && !cond.Test && !cond.Aggregate {
			fmt.Fprintf(b, " (alpha %d)", cond.AlphaMatches)
		}
		if i == branch.FirstFailing {
			fmt.Fprintf(b, " -- %s", cond.Reason)
			if loc := sourceSpanLocation(cond.RejectingSpan); loc != "" {
				b.WriteString(" at " + loc)
			}
			if len(cond.Blockers) > 0 {
				b.WriteString(" by " + blockerLabel(cond))
			}
		}
		b.WriteByte('\n')
	}
	for _, pm := range branch.PartialMatches {
		fmt.Fprintf(b, "    nearest: %s\n", partialMatchLabel(pm))
	}
}

func primaryFailure(r WhyNotReport) (WhyNotCondition, bool) {
	best := -1
	bestDepth := -1
	for i, branch := range r.Branches {
		if branch.FirstFailing < 0 || branch.FirstFailing >= len(branch.Conditions) {
			continue
		}
		depth := branchSatisfiedCount(branch)
		if depth > bestDepth {
			bestDepth = depth
			best = i
		}
	}
	if best < 0 {
		return WhyNotCondition{}, false
	}
	branch := r.Branches[best]
	return branch.Conditions[branch.FirstFailing], true
}

func conditionLabel(cond WhyNotCondition) string {
	if cond.Binding != "" {
		return "?" + cond.Binding
	}
	if cond.Test {
		return "(test)"
	}
	return "condition"
}

func negationPrefix(cond WhyNotCondition) string {
	if cond.Negated {
		return "not "
	}
	return ""
}

func blockerLabel(cond WhyNotCondition) string {
	label := factIDList(cond.Blockers)
	if cond.BlockerCount > len(cond.Blockers) {
		label += fmt.Sprintf(" (+%d more)", cond.BlockerCount-len(cond.Blockers))
	}
	return label
}

func partialMatchLabel(pm WhyNotPartialMatch) string {
	var b strings.Builder
	for i, binding := range pm.Bindings {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(binding.Name)
		b.WriteByte('=')
		if binding.Value.Kind() != ValueNull || binding.FromFact.IsZero() {
			traceWriteValue(&b, binding.Value)
		} else {
			b.WriteString(binding.FromFact.String())
		}
	}
	if b.Len() == 0 {
		b.WriteString("(no bound values)")
	}
	if loc := sourceSpanLocation(pm.RejectedBySpan); loc != "" {
		b.WriteString(" (rejected at " + loc + ")")
	}
	return b.String()
}

func factIDList(ids []FactID) string {
	if len(ids) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(id.String())
	}
	b.WriteByte(']')
	return b.String()
}
