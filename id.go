package gess

import "fmt"

type RulesetID string

func (id RulesetID) String() string {
	return string(id)
}

type SessionID string

func (id SessionID) String() string {
	return string(id)
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
