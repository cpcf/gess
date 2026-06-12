package gess

import "strings"

type DuplicatePolicy int

const (
	DuplicateStructural DuplicatePolicy = iota
	DuplicateAllow
	DuplicateUniqueKey
)

type FieldSpec struct {
	Name     string
	Kind     ValueKind
	Required bool
}

type TemplateSpec struct {
	Name            string
	Key             TemplateKey
	Fields          []FieldSpec
	DuplicatePolicy DuplicatePolicy
	Closed          bool
}

func (s TemplateSpec) clone() TemplateSpec {
	out := s
	out.Fields = make([]FieldSpec, len(s.Fields))
	copy(out.Fields, s.Fields)
	return out
}

type Template struct {
	name            string
	key             TemplateKey
	fields          []FieldSpec
	duplicatePolicy DuplicatePolicy
	closed          bool
}

func (t Template) Name() string {
	return t.name
}

func (t Template) Key() TemplateKey {
	return t.key
}

func (t Template) DuplicatePolicy() DuplicatePolicy {
	return t.duplicatePolicy
}

func (t Template) Closed() bool {
	return t.closed
}

func (t Template) Fields() []FieldSpec {
	out := make([]FieldSpec, len(t.fields))
	copy(out, t.fields)
	return out
}

func (t Template) clone() Template {
	out := t
	out.fields = make([]FieldSpec, len(t.fields))
	copy(out.fields, t.fields)
	return out
}

func compileTemplateSpec(spec TemplateSpec) (Template, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Template{}, &ValidationError{Reason: "template name is required"}
	}

	key := spec.Key
	if key == "" {
		key = TemplateKey(name)
	}

	seenFields := make(map[string]struct{}, len(spec.Fields))
	fields := make([]FieldSpec, len(spec.Fields))
	for i, field := range spec.Fields {
		field.Name = strings.TrimSpace(field.Name)
		if field.Name == "" {
			return Template{}, &ValidationError{
				TemplateName: name,
				Reason:       "field name is required",
			}
		}
		if _, exists := seenFields[field.Name]; exists {
			return Template{}, &ValidationError{
				TemplateName: name,
				FieldName:    field.Name,
				Reason:       "duplicate field",
			}
		}
		if field.Kind == "" {
			field.Kind = ValueAny
		}
		seenFields[field.Name] = struct{}{}
		fields[i] = field
	}

	return Template{
		name:            name,
		key:             key,
		fields:          fields,
		duplicatePolicy: spec.DuplicatePolicy,
		closed:          spec.Closed,
	}, nil
}
