package gess

import (
	"strconv"
	"strings"
)

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
		copied[i] = fact.clone()
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
	for i, fact := range s.facts {
		out[i] = fact.clone()
	}
	return out
}

func (s Snapshot) Fact(id FactID) (FactSnapshot, bool) {
	idx, ok := s.byID[id]
	if !ok {
		return FactSnapshot{}, false
	}
	return s.facts[idx].clone(), true
}

func (s Snapshot) FactsByName(name string) []FactSnapshot {
	var out []FactSnapshot
	for _, fact := range s.facts {
		if fact.Name() == name {
			out = append(out, fact.clone())
		}
	}
	return out
}

func (s Snapshot) FactsByTemplateKey(templateKey TemplateKey) []FactSnapshot {
	var out []FactSnapshot
	for _, fact := range s.facts {
		if fact.TemplateKey() == templateKey {
			out = append(out, fact.clone())
		}
	}
	return out
}

func (s Snapshot) String() string {
	var b strings.Builder
	b.WriteString("Snapshot{session:")
	b.WriteString(s.sessionID.String())
	b.WriteString(", ruleset:")
	b.WriteString(s.rulesetID.String())
	b.WriteString(", generation:")
	b.WriteString(strconv.FormatUint(uint64(s.generation), 10))
	b.WriteString(", facts:[")
	for i, fact := range s.facts {
		if i > 0 {
			b.WriteByte(',')
			b.WriteByte(' ')
		}
		b.WriteString(fact.String())
	}
	b.WriteString("]}")
	return b.String()
}
