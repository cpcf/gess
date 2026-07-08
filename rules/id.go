package rules

import (
	"fmt"
	"strconv"
	"strings"
)

// RulesetID identifies one compiled ruleset revision.
type RulesetID string

func (id RulesetID) String() string {
	return string(id)
}

// ModuleName identifies a module. The zero value renders as MAIN, the default
// module.
type ModuleName string

const MainModule ModuleName = "MAIN"

func (name ModuleName) String() string {
	if name.IsZero() {
		return string(MainModule)
	}
	return string(name)
}

func (name ModuleName) IsZero() bool {
	return strings.TrimSpace(string(name)) == ""
}

// SessionID identifies a session.
type SessionID string

func (id SessionID) String() string {
	return string(id)
}

// RunID identifies one session Run call.
type RunID uint64

func (id RunID) String() string {
	if id.IsZero() {
		return "run:zero"
	}
	return "run:" + strconv.FormatUint(uint64(id), 10)
}

func (id RunID) IsZero() bool {
	return id == 0
}

// RuleID is a rule's stable identity, surviving ReplaceRule.
type RuleID string

func (id RuleID) String() string {
	if id.IsZero() {
		return "rule:zero"
	}
	return string(id)
}

func (id RuleID) IsZero() bool {
	return strings.TrimSpace(string(id)) == ""
}

// RuleRevisionID identifies one compiled revision of a rule.
type RuleRevisionID string

func (id RuleRevisionID) String() string {
	if id.IsZero() {
		return "rule-revision:zero"
	}
	return string(id)
}

func (id RuleRevisionID) IsZero() bool {
	return strings.TrimSpace(string(id)) == ""
}

// ActivationID identifies one activation: a rule paired with the specific facts
// that matched it.
type ActivationID string

func (id ActivationID) String() string {
	if id.IsZero() {
		return "activation:zero"
	}
	return string(id)
}

func (id ActivationID) IsZero() bool {
	return strings.TrimSpace(string(id)) == ""
}

// SupportID identifies one logical support edge.
type SupportID string

func (id SupportID) String() string {
	if id.IsZero() {
		return "support:zero"
	}
	return string(id)
}

func (id SupportID) IsZero() bool {
	return strings.TrimSpace(string(id)) == ""
}

// ConditionID is a compiled rule or query condition's stable identity.
type ConditionID string

func (id ConditionID) String() string {
	if id.IsZero() {
		return "condition:zero"
	}
	return string(id)
}

func (id ConditionID) IsZero() bool {
	return strings.TrimSpace(string(id)) == ""
}

// TemplateKey identifies a compiled template within a ruleset.
type TemplateKey string

func (key TemplateKey) String() string {
	return string(key)
}

// FactID identifies a fact, stable across modifies within a generation.
type FactID struct {
	generation Generation
	sequence   uint64
}

// NewFactID constructs a FactID for engine-owned fact allocation and tests.
func NewFactID(generation Generation, sequence uint64) FactID {
	return FactID{generation: generation, sequence: sequence}
}

func (id FactID) Generation() Generation {
	return id.generation
}

func (id FactID) Sequence() uint64 {
	return id.sequence
}

func (id FactID) IsZero() bool {
	return id.generation == 0 && id.sequence == 0
}

func (id FactID) String() string {
	if id.IsZero() {
		return "fact:zero"
	}
	return fmt.Sprintf("fact:g%d:%d", id.generation, id.sequence)
}

// FactVersion advances each time a fact is modified; a fact's FactID stays
// stable across modifies within a generation.
type FactVersion uint32

// Recency is a session-wide counter that advances on every fact mutation.
type Recency uint32

// Generation is the working-memory reset epoch.
type Generation uint64
