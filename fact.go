package gess

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
