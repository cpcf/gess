package gess

import "fmt"

// Snapshot is an immutable working-memory view.
type Snapshot struct {
	sessionID  SessionID
	rulesetID  RulesetID
	generation Generation
	facts      []FactSnapshot
	byID       map[FactID]int
}

func newSnapshot(sessionID SessionID, rulesetID RulesetID, generation Generation, facts []FactSnapshot) Snapshot {
	copied := make([]FactSnapshot, len(facts))
	for i, fact := range facts {
		copied[i] = fact
		copied[i].fields = cloneFields(fact.fields)
	}

	byID := make(map[FactID]int, len(copied))
	for i, fact := range copied {
		byID[fact.ID()] = i
	}

	return Snapshot{
		sessionID:  sessionID,
		rulesetID:  rulesetID,
		generation: generation,
		facts:      copied,
		byID:       byID,
	}
}

func (s Snapshot) SessionID() SessionID {
	return s.sessionID
}

func (s Snapshot) RulesetID() RulesetID {
	return s.rulesetID
}

func (s Snapshot) Generation() Generation {
	return s.generation
}

func (s Snapshot) Len() int {
	return len(s.facts)
}

// Facts returns snapshot facts in deterministic order.
func (s Snapshot) Facts() []FactSnapshot {
	out := make([]FactSnapshot, len(s.facts))
	copy(out, s.facts)
	return out
}

func (s Snapshot) Fact(id FactID) (FactSnapshot, bool) {
	idx, ok := s.byID[id]
	if !ok {
		return FactSnapshot{}, false
	}
	return s.facts[idx], true
}

func (s Snapshot) FactsByName(name string) []FactSnapshot {
	var out []FactSnapshot
	for _, fact := range s.facts {
		if fact.Name() == name {
			out = append(out, fact)
		}
	}
	return out
}

func (s Snapshot) String() string {
	return fmt.Sprintf("Snapshot{session:%s ruleset:%s generation:%d facts:%d}", s.sessionID, s.rulesetID, s.generation, len(s.facts))
}
