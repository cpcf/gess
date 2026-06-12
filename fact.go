package gess

import (
	"maps"
	"strings"
)

type FactVersion uint64

type Recency uint64

// Generation is the working-memory reset epoch. Fact IDs include a generation
// component so IDs from before Reset cannot address post-reset facts.
type Generation uint64

type FactSnapshot struct {
	id            FactID
	name          string
	templateKey   TemplateKey
	version       FactVersion
	recency       Recency
	generation    Generation
	fields        Fields
	fieldPresence map[string]FieldPresence
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
	id            FactID
	name          string
	templateKey   TemplateKey
	version       FactVersion
	recency       Recency
	generation    Generation
	fields        Fields
	fieldPresence map[string]FieldPresence
	dupKey        DuplicateKey
}

func (f *workingFact) snapshot() FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.name,
		templateKey:   f.templateKey,
		version:       f.version,
		recency:       f.recency,
		generation:    f.generation,
		fields:        cloneFields(f.fields),
		fieldPresence: cloneFieldPresence(f.fieldPresence),
	}
}

func makeDuplicateKey(name string, templateKey TemplateKey, fields Fields) DuplicateKey {
	return makeDuplicateKeyForTemplate(name, Template{key: templateKey}, fields)
}

func makeDuplicateKeyForTemplate(name string, template Template, fields Fields) DuplicateKey {
	var b strings.Builder
	b.WriteString("name:")
	b.WriteString(name)
	b.WriteString("|template:")
	b.WriteString(template.key.String())
	b.WriteString("|fields:")
	b.WriteString(duplicateFields(fields, template).duplicateKey())
	return DuplicateKey(b.String())
}

func duplicateFields(values Fields, template Template) Fields {
	if template.duplicatePolicy == DuplicateAllow {
		return nil
	}
	if template.duplicatePolicy != DuplicateUniqueKey {
		return values
	}

	out := make(Fields, len(template.duplicateKeyNames))
	for _, fieldName := range template.duplicateKeyNames {
		if value, ok := values[fieldName]; ok {
			out[fieldName] = value
		}
	}
	return out
}

func (f FactSnapshot) FieldPresence(field string) (FieldPresence, bool) {
	presence, ok := f.fieldPresence[field]
	return presence, ok
}

func (f FactSnapshot) FieldPresenceMap() map[string]FieldPresence {
	out := make(map[string]FieldPresence, len(f.fieldPresence))
	maps.Copy(out, f.fieldPresence)
	return out
}

func cloneFieldPresence(in map[string]FieldPresence) map[string]FieldPresence {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]FieldPresence, len(in))
	maps.Copy(out, in)
	return out
}
