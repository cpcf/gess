package rules

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
)

// FactSnapshot is an immutable, detached view of one fact's identity, fields,
// and support state.
type FactSnapshot struct {
	IDValue             FactID
	NameValue           string
	TemplateKeyValue    TemplateKey
	VersionValue        FactVersion
	RecencyValue        Recency
	GenerationValue     Generation
	FieldValues         Fields
	FieldPresenceValues map[string]FieldPresence
	SupportValue        FactSupportProvenance
}

// ID returns the fact identity.
func (f FactSnapshot) ID() FactID {
	return f.IDValue
}

// Name returns the dynamic fact name, if the fact is not template-backed.
func (f FactSnapshot) Name() string {
	return f.NameValue
}

// TemplateKey returns the template identity, if the fact is template-backed.
func (f FactSnapshot) TemplateKey() TemplateKey {
	return f.TemplateKeyValue
}

// Version returns the fact version.
func (f FactSnapshot) Version() FactVersion {
	return f.VersionValue
}

// Recency returns the mutation recency that ordered this fact.
func (f FactSnapshot) Recency() Recency {
	return f.RecencyValue
}

// Generation returns the working-memory generation that owns this fact.
func (f FactSnapshot) Generation() Generation {
	return f.GenerationValue
}

// Fields returns a copy of the fact's field values.
func (f FactSnapshot) Fields() Fields {
	return CloneFields(f.FieldValues)
}

// Field returns one field value by name.
func (f FactSnapshot) Field(name string) (Value, bool) {
	value, ok := f.FieldValues[name]
	if !ok {
		return Value{}, false
	}
	return CloneValue(value), true
}

// Path returns the value at path, if present.
func (f FactSnapshot) Path(path PathSpec) (Value, bool, error) {
	normalized := clonePathSpec(path)
	if len(normalized.Segments) > 0 && normalized.Segments[0].Kind == PathSegmentRoot {
		normalized.Segments[0].Key = strings.TrimSpace(normalized.Segments[0].Key)
	}
	if err := validatePathSpec(normalized); err != nil {
		return Value{}, false, err
	}
	value, ok := f.Field(normalized.Segments[0].Key)
	if !ok {
		return Value{}, false, nil
	}
	value, ok = resolveValuePathTail(value, normalized.Segments[1:])
	if !ok {
		return Value{}, false, nil
	}
	return CloneValue(value), true, nil
}

// Support returns the fact's logical support classification.
func (f FactSnapshot) Support() FactSupportProvenance {
	return f.SupportValue
}

func (f FactSnapshot) String() string {
	fields := f.Fields()
	presence := f.FieldPresenceMap()

	var b strings.Builder
	b.WriteString("Fact{")
	b.WriteString("id:")
	b.WriteString(f.IDValue.String())
	b.WriteString(", name:")
	b.WriteString(f.NameValue)
	b.WriteString(", template:")
	b.WriteString(f.TemplateKeyValue.String())
	b.WriteString(", version:")
	b.WriteString(strconv.FormatUint(uint64(f.VersionValue), 10))
	b.WriteString(", recency:")
	b.WriteString(strconv.FormatUint(uint64(f.RecencyValue), 10))
	b.WriteString(", generation:")
	b.WriteString(strconv.FormatUint(uint64(f.GenerationValue), 10))
	b.WriteString(", fields:{")
	writeOrderedFields(&b, fields)
	b.WriteString("}, presence:{")
	writeOrderedPresence(&b, presence)
	b.WriteString("}}")
	return b.String()
}

// FieldPresence returns whether a template field was defaulted, explicit, or
// omitted.
func (f FactSnapshot) FieldPresence(field string) (FieldPresence, bool) {
	presence, ok := f.FieldPresenceValues[field]
	return presence, ok
}

// FieldPresenceMap returns template field-presence metadata keyed by field name.
func (f FactSnapshot) FieldPresenceMap() map[string]FieldPresence {
	return cloneFieldPresence(f.FieldPresenceValues)
}

// CloneFactSnapshot returns a defensive copy of f.
func CloneFactSnapshot(f FactSnapshot) FactSnapshot {
	out := f
	out.FieldValues = CloneFields(f.FieldValues)
	out.FieldPresenceValues = cloneFieldPresence(f.FieldPresenceValues)
	return out
}

// CloneFactSnapshotPtr returns a defensive copy of f when non-nil.
func CloneFactSnapshotPtr(f *FactSnapshot) *FactSnapshot {
	if f == nil {
		return nil
	}
	out := CloneFactSnapshot(*f)
	return &out
}

// CloneFactSnapshots returns a defensive copy of facts.
func CloneFactSnapshots(facts []FactSnapshot) []FactSnapshot {
	out := make([]FactSnapshot, len(facts))
	for i, fact := range facts {
		out[i] = CloneFactSnapshot(fact)
	}
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

func writeOrderedFields(b *strings.Builder, fields Fields) {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(fields[key].String())
	}
}

func writeOrderedPresence(b *strings.Builder, presence map[string]FieldPresence) {
	keys := make([]string, 0, len(presence))
	for key := range presence {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(string(presence[key]))
	}
}

func validatePathSpec(p PathSpec) error {
	if len(p.Segments) == 0 {
		return fmt.Errorf("%w: path requires a root segment", ErrInvalidPath)
	}
	for i, segment := range p.Segments {
		switch segment.Kind {
		case PathSegmentRoot:
			if i != 0 {
				return fmt.Errorf("%w: root segment must be first", ErrInvalidPath)
			}
			if strings.TrimSpace(segment.Key) == "" {
				return fmt.Errorf("%w: root field is required", ErrInvalidPath)
			}
		case PathSegmentMap:
			if i == 0 {
				return fmt.Errorf("%w: map key cannot be the root segment", ErrInvalidPath)
			}
		case PathSegmentIndex:
			if i == 0 {
				return fmt.Errorf("%w: list index cannot be the root segment", ErrInvalidPath)
			}
			if segment.Index < 0 {
				return fmt.Errorf("%w: list index must be non-negative", ErrInvalidPath)
			}
		default:
			return fmt.Errorf("%w: unknown path segment kind %q", ErrInvalidPath, segment.Kind)
		}
	}
	return nil
}

func resolveValuePathTail(value Value, segments []PathSegment) (Value, bool) {
	current := value
	for _, segment := range segments {
		switch segment.Kind {
		case PathSegmentMap:
			if current.Kind() != ValueMap {
				return Value{}, false
			}
			next, ok := current.MapGet(segment.Key)
			if !ok {
				return Value{}, false
			}
			current = next
		case PathSegmentIndex:
			if current.Kind() != ValueList {
				return Value{}, false
			}
			next, ok := current.ListAt(segment.Index)
			if !ok {
				return Value{}, false
			}
			current = next
		default:
			return Value{}, false
		}
	}
	return current, true
}
