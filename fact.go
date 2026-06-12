package gess

import (
	"strings"
)

type FactVersion uint64

type Recency uint64

// Generation is the working-memory reset epoch. Fact IDs include a generation
// component so IDs from before Reset cannot address post-reset facts.
type Generation uint64

type FactSnapshot struct {
	id          FactID
	name        string
	templateKey TemplateKey
	version     FactVersion
	recency     Recency
	generation  Generation
	fields      Fields
}

func (f FactSnapshot) ID() FactID {
	return f.id
}

func (f FactSnapshot) Name() string {
	return f.name
}

func (f FactSnapshot) TemplateKey() TemplateKey {
	return f.templateKey
}

func (f FactSnapshot) Version() FactVersion {
	return f.version
}

func (f FactSnapshot) Recency() Recency {
	return f.recency
}

func (f FactSnapshot) Generation() Generation {
	return f.generation
}

// Fields returns a defensive copy of the fact fields.
func (f FactSnapshot) Fields() Fields {
	return cloneFields(f.fields)
}

type workingFact struct {
	id          FactID
	name        string
	templateKey TemplateKey
	version     FactVersion
	recency     Recency
	generation  Generation
	fields      Fields
	dupKey      DuplicateKey
}

func (f *workingFact) snapshot() FactSnapshot {
	return FactSnapshot{
		id:          f.id,
		name:        f.name,
		templateKey: f.templateKey,
		version:     f.version,
		recency:     f.recency,
		generation:  f.generation,
		fields:      cloneFields(f.fields),
	}
}

func makeDuplicateKey(name string, templateKey TemplateKey, fields Fields) DuplicateKey {
	var b strings.Builder
	b.WriteString("name:")
	b.WriteString(name)
	b.WriteString("|template:")
	b.WriteString(templateKey.String())
	b.WriteString("|fields:")
	b.WriteString(fields.duplicateKey())
	return DuplicateKey(b.String())
}
