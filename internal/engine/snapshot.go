package engine

import (
	"strconv"
	"strings"
)

// Snapshot is an immutable working-memory view.
type Snapshot struct {
	sessionID      SessionID
	rulesetID      RulesetID
	revision       *Ruleset
	generation     Generation
	globalValues   []Value
	facts          []FactSnapshot
	support        SupportGraph
	byID           map[FactID]int
	byName         map[string][]int
	byTemplate     map[TemplateKey][]int
	demandCounters backchainDemandCascadeCounters
}

// BackchainDemandDiagnostics summarizes active generated backward-chaining
// demand facts and lifetime cascade activity visible in a snapshot.
type BackchainDemandDiagnostics struct {
	// Active is the number of demand facts currently retained.
	Active int
	// ByTemplate counts active demand facts by demand template key.
	ByTemplate map[TemplateKey]int
	// Cascades is the number of demand cascades that processed at least one step.
	Cascades int
	// CascadeSteps is the lifetime number of processed demand requests.
	CascadeSteps int
	// CascadeLengthMax is the largest number of steps processed by one cascade.
	CascadeLengthMax int
	// CascadeLimitHits is the number of cascades stopped by the configured bound.
	CascadeLimitHits int
}

// Count returns the active demand fact count for a demand template key.
func (d BackchainDemandDiagnostics) Count(templateKey TemplateKey) int {
	if len(d.ByTemplate) == 0 {
		return 0
	}
	return d.ByTemplate[templateKey]
}

func (s Snapshot) sourceGeneration() Generation {
	return s.generation
}

func (s Snapshot) factsForTarget(target conditionTarget) ([]FactSnapshot, bool) {
	switch target.kind {
	case conditionTargetName:
		indexes := s.indexesForTarget(target)
		if len(indexes) == 0 {
			return nil, true
		}
		out := make([]FactSnapshot, 0, len(indexes))
		for _, idx := range indexes {
			if idx < 0 || idx >= len(s.facts) {
				continue
			}
			out = append(out, s.facts[idx])
		}
		return out, true
	case conditionTargetTemplateKey:
		indexes := s.indexesForTarget(target)
		if len(indexes) == 0 {
			return nil, true
		}
		out := make([]FactSnapshot, 0, len(indexes))
		for _, idx := range indexes {
			if idx < 0 || idx >= len(s.facts) {
				continue
			}
			out = append(out, s.facts[idx])
		}
		return out, true
	default:
		return nil, false
	}
}

func newSnapshot(sessionID SessionID, rulesetID RulesetID, generation Generation, facts []FactSnapshot) Snapshot {
	copied := make([]FactSnapshot, len(facts))
	for i, fact := range facts {
		copied[i] = fact.clone()
	}

	byID := snapshotIDIndex(copied)

	return Snapshot{
		sessionID:    sessionID,
		rulesetID:    rulesetID,
		generation:   generation,
		globalValues: nil,
		facts:        copied,
		support:      SupportGraph{Generation: generation},
		byID:         byID,
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

func (s Snapshot) SupportGraph() SupportGraph {
	return s.support.clone()
}

// BackchainDemandDiagnostics returns active generated backward-chaining demand
// fact counts and lifetime cascade counters for the snapshot's session.
func (s Snapshot) BackchainDemandDiagnostics() BackchainDemandDiagnostics {
	out := BackchainDemandDiagnostics{
		Cascades:         s.demandCounters.Cascades,
		CascadeSteps:     s.demandCounters.Steps,
		CascadeLengthMax: s.demandCounters.LengthMax,
		CascadeLimitHits: s.demandCounters.LimitHits,
	}
	if s.revision == nil || len(s.facts) == 0 {
		return out
	}
	for _, fact := range s.facts {
		template, ok := s.revision.templateByKey(fact.TemplateKey())
		if !ok || !template.backchainDemand {
			continue
		}
		if out.ByTemplate == nil {
			out.ByTemplate = make(map[TemplateKey]int, 1)
		}
		out.Active++
		out.ByTemplate[fact.TemplateKey()]++
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
	if s.byName == nil {
		return s.factsByNameScan(name)
	}
	return s.factsByIndexes(s.byName[name])
}

func (s Snapshot) FactsByTemplateKey(templateKey TemplateKey) []FactSnapshot {
	if s.byTemplate == nil {
		return s.factsByTemplateKeyScan(templateKey)
	}
	return s.factsByIndexes(s.byTemplate[templateKey])
}

func (s Snapshot) factsByNameScan(name string) []FactSnapshot {
	var out []FactSnapshot
	for _, fact := range s.facts {
		if fact.Name() == name {
			out = append(out, fact.clone())
		}
	}
	return out
}

func (s Snapshot) factsByTemplateKeyScan(templateKey TemplateKey) []FactSnapshot {
	var out []FactSnapshot
	for _, fact := range s.facts {
		if fact.TemplateKey() == templateKey {
			out = append(out, fact.clone())
		}
	}
	return out
}

func (s Snapshot) factsByIndexes(indexes []int) []FactSnapshot {
	if len(indexes) == 0 {
		return nil
	}
	out := make([]FactSnapshot, 0, len(indexes))
	for _, idx := range indexes {
		if idx < 0 || idx >= len(s.facts) {
			continue
		}
		out = append(out, s.facts[idx].clone())
	}
	return out
}

func (s Snapshot) indexesForTarget(target conditionTarget) []int {
	switch target.kind {
	case conditionTargetName:
		if s.byName == nil {
			return s.indexesForNameScan(target.name)
		}
		return s.byName[target.name]
	case conditionTargetTemplateKey:
		if s.byTemplate == nil {
			return s.indexesForTemplateKeyScan(target.templateKey)
		}
		return s.byTemplate[target.templateKey]
	default:
		return nil
	}
}

func (s Snapshot) indexesForNameScan(name string) []int {
	var out []int
	for i, fact := range s.facts {
		if fact.Name() == name {
			out = append(out, i)
		}
	}
	return out
}

func (s Snapshot) indexesForTemplateKeyScan(templateKey TemplateKey) []int {
	var out []int
	for i, fact := range s.facts {
		if fact.TemplateKey() == templateKey {
			out = append(out, i)
		}
	}
	return out
}

func snapshotIndexes(facts []FactSnapshot) (map[FactID]int, map[string][]int, map[TemplateKey][]int) {
	byID := snapshotIDIndex(facts)
	byName := make(map[string][]int)
	byTemplate := make(map[TemplateKey][]int)
	for i, fact := range facts {
		if fact.name != "" {
			byName[fact.name] = append(byName[fact.name], i)
		}
		if fact.templateKey != "" {
			byTemplate[fact.templateKey] = append(byTemplate[fact.templateKey], i)
		}
	}
	return byID, byName, byTemplate
}

func snapshotIDIndex(facts []FactSnapshot) map[FactID]int {
	byID := make(map[FactID]int, len(facts))
	for i, fact := range facts {
		byID[fact.ID()] = i
	}
	return byID
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
