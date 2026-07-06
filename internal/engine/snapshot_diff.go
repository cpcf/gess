package engine

import "sort"

// SnapshotDiff is the working-memory difference between two snapshots: facts
// added, retracted, or modified (by field value or support state). Slices are
// in fact-id order.
type SnapshotDiff struct {
	Added     []FactSnapshot
	Retracted []FactSnapshot
	Modified  []FactModification
}

// FactModification is one fact that exists in both snapshots but changed: its
// field changes (empty when only support changed) and its support-state
// transition.
type FactModification struct {
	Before        FactSnapshot
	After         FactSnapshot
	ChangedFields []FieldChange
	SupportBefore FactSupportState
	SupportAfter  FactSupportState
}

// Empty reports whether the diff has no added, retracted, or modified facts.
func (d SnapshotDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Retracted) == 0 && len(d.Modified) == 0
}

// DiffSnapshots returns the difference from before to after. It is a pure
// function over two snapshots; the result is deterministic in fact-id order. It
// works on any pair of snapshots — comparing snapshots from different rulesets
// is the caller's own risk.
func DiffSnapshots(before, after Snapshot) SnapshotDiff {
	beforeByID := snapshotIDIndex(before.facts)
	afterByID := snapshotIDIndex(after.facts)

	var diff SnapshotDiff
	for i := range after.facts {
		fact := after.facts[i]
		beforeIdx, ok := beforeByID[fact.id]
		if !ok {
			diff.Added = append(diff.Added, fact.clone())
			continue
		}
		beforeFact := before.facts[beforeIdx]
		changes := diffFactFields(beforeFact, fact)
		supportBefore := beforeFact.support.State
		supportAfter := fact.support.State
		if len(changes) > 0 || supportBefore != supportAfter {
			diff.Modified = append(diff.Modified, FactModification{
				Before:        beforeFact.clone(),
				After:         fact.clone(),
				ChangedFields: changes,
				SupportBefore: supportBefore,
				SupportAfter:  supportAfter,
			})
		}
	}
	for i := range before.facts {
		fact := before.facts[i]
		if _, ok := afterByID[fact.id]; !ok {
			diff.Retracted = append(diff.Retracted, fact.clone())
		}
	}

	sort.Slice(diff.Added, func(i, j int) bool { return factIDLess(diff.Added[i].id, diff.Added[j].id) })
	sort.Slice(diff.Retracted, func(i, j int) bool { return factIDLess(diff.Retracted[i].id, diff.Retracted[j].id) })
	sort.Slice(diff.Modified, func(i, j int) bool { return factIDLess(diff.Modified[i].After.id, diff.Modified[j].After.id) })
	return diff
}

func diffFactFields(before, after FactSnapshot) []FieldChange {
	beforeFields := before.Fields()
	afterFields := after.Fields()
	names := make(map[string]struct{}, len(beforeFields)+len(afterFields))
	for name := range beforeFields {
		names[name] = struct{}{}
	}
	for name := range afterFields {
		names[name] = struct{}{}
	}

	var changes []FieldChange
	for name := range names {
		beforeValue, beforeOK := beforeFields[name]
		afterValue, afterOK := afterFields[name]
		switch {
		case beforeOK && afterOK:
			if !beforeValue.Equal(afterValue) {
				changes = append(changes, FieldChange{Field: name, Old: cloneValue(beforeValue), New: cloneValue(afterValue)})
			}
		case beforeOK:
			changes = append(changes, FieldChange{Field: name, Old: cloneValue(beforeValue)})
		default:
			changes = append(changes, FieldChange{Field: name, New: cloneValue(afterValue)})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Field < changes[j].Field })
	return changes
}
