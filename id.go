package gess

import (
	"fmt"
	"strings"
)

type RulesetID string

func (id RulesetID) String() string {
	return string(id)
}

type SessionID string

func (id SessionID) String() string {
	return string(id)
}

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

type TemplateKey string

func (key TemplateKey) String() string {
	return string(key)
}

type FactID struct {
	generation Generation
	sequence   uint64
}

func newFactID(generation Generation, sequence uint64) FactID {
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
