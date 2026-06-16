package gess

import (
	"maps"
	"sort"
	"strconv"
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
	fieldSlots    []factSlot
	fieldSpecs    []FieldSpec
	fieldPresence map[string]FieldPresence
	support       FactSupportProvenance
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
	if f.fields != nil {
		return cloneFields(f.fields)
	}
	return materializeFieldsFromSlots(f.fieldSlots, f.fieldSpecs)
}

// Field returns a defensive copy of one fact field.
func (f FactSnapshot) Field(name string) (Value, bool) {
	value, ok := f.fieldValue(name)
	if !ok {
		return Value{}, false
	}
	return cloneValue(value), true
}

func (f FactSnapshot) compiledFieldValue(field string, slot int) (Value, bool) {
	if slot >= 0 && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}

	return f.fieldValue(field)
}

func (f FactSnapshot) Support() FactSupportProvenance {
	return f.support
}

type workingFact struct {
	id            FactID
	name          string
	templateKey   TemplateKey
	version       FactVersion
	recency       Recency
	generation    Generation
	fields        Fields
	fieldSlots    []factSlot
	fieldSpecs    []FieldSpec
	fieldPresence map[string]FieldPresence
	dupKey        DuplicateKey
	support       FactSupportProvenance
	isTransient   bool
}

type factSlot struct {
	value    Value
	presence fieldPresenceCode
	ok       bool
}

type fieldPresenceCode uint8

const (
	fieldPresenceOmitted fieldPresenceCode = iota
	fieldPresenceDefault
	fieldPresenceExplicit
)

func encodeFieldPresence(presence FieldPresence) fieldPresenceCode {
	switch presence {
	case FieldPresenceDefault:
		return fieldPresenceDefault
	case FieldPresenceExplicit:
		return fieldPresenceExplicit
	default:
		return fieldPresenceOmitted
	}
}

func (p fieldPresenceCode) fieldPresence() FieldPresence {
	switch p {
	case fieldPresenceDefault:
		return FieldPresenceDefault
	case fieldPresenceExplicit:
		return FieldPresenceExplicit
	default:
		return FieldPresenceOmitted
	}
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
		fieldSlots:    cloneFactSlots(f.fieldSlots),
		fieldSpecs:    f.fieldSpecs,
		fieldPresence: cloneFieldPresence(f.fieldPresence),
		support:       f.support,
	}
}

func (f *workingFact) detachedSnapshot() FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.name,
		templateKey:   f.templateKey,
		version:       f.version,
		recency:       f.recency,
		generation:    f.generation,
		fields:        f.fields,
		fieldSlots:    f.fieldSlots,
		fieldSpecs:    f.fieldSpecs,
		fieldPresence: f.fieldPresence,
		support:       f.support,
	}
}

func (f FactSnapshot) clone() FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.name,
		templateKey:   f.templateKey,
		version:       f.version,
		recency:       f.recency,
		generation:    f.generation,
		fields:        cloneFields(f.fields),
		fieldSlots:    cloneFactSlots(f.fieldSlots),
		fieldSpecs:    f.fieldSpecs,
		fieldPresence: cloneFieldPresence(f.fieldPresence),
		support:       f.support,
	}
}

func (f FactSnapshot) String() string {
	fields := f.Fields()
	presence := f.FieldPresenceMap()

	var b strings.Builder
	b.WriteString("Fact{")
	b.WriteString("id:")
	b.WriteString(f.id.String())
	b.WriteString(", name:")
	b.WriteString(f.name)
	b.WriteString(", template:")
	b.WriteString(f.templateKey.String())
	b.WriteString(", version:")
	b.WriteString(strconv.FormatUint(uint64(f.version), 10))
	b.WriteString(", recency:")
	b.WriteString(strconv.FormatUint(uint64(f.recency), 10))
	b.WriteString(", generation:")
	b.WriteString(strconv.FormatUint(uint64(f.generation), 10))
	b.WriteString(", fields:{")
	emitOrderedFieldsString(&b, fields)
	b.WriteString("}, presence:{")
	emitOrderedPresenceString(&b, presence)
	b.WriteString("}}")
	return b.String()
}

func emitOrderedFieldsString(b *strings.Builder, fields Fields) {
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

func emitOrderedPresenceString(b *strings.Builder, presence map[string]FieldPresence) {
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
	emitDuplicateKeyFields(&b, template, fields, nil)
	return DuplicateKey(b.String())
}

func makeDuplicateKeyForValidatedFact(name string, template Template, fields Fields, slots []factSlot) DuplicateKey {
	if template.duplicatePolicy == DuplicateAllow {
		return ""
	}
	if len(slots) > 0 {
		return makeDuplicateKeyForTemplateWithSlots(name, template, fields, slots)
	}
	return makeDuplicateKeyForTemplate(name, template, fields)
}

func makeDuplicateKeyForTemplateWithSlots(name string, template Template, fields Fields, slots []factSlot) DuplicateKey {
	var b strings.Builder
	if len(slots) > 0 {
		b.Grow(duplicateKeyCapacity(name, template, fields, slots))
	}
	b.WriteString("name:")
	b.WriteString(name)
	b.WriteString("|template:")
	b.WriteString(template.key.String())
	b.WriteString("|fields:")
	emitDuplicateKeyFields(&b, template, fields, slots)
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
	if f.fieldPresence != nil {
		presence, ok := f.fieldPresence[field]
		if ok {
			return presence, true
		}
	}
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.fieldSlots) {
		return f.fieldSlots[slot].presence.fieldPresence(), true
	}
	return FieldPresence(""), false
}

func (f FactSnapshot) FieldPresenceMap() map[string]FieldPresence {
	if f.fieldPresence != nil {
		return cloneFieldPresence(f.fieldPresence)
	}
	return materializePresenceFromSlots(f.fieldSlots, f.fieldSpecs)
}

func cloneFieldPresence(in map[string]FieldPresence) map[string]FieldPresence {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]FieldPresence, len(in))
	maps.Copy(out, in)
	return out
}

func (f FactSnapshot) fieldValue(name string) (Value, bool) {
	if f.fields != nil {
		value, ok := f.fields[name]
		if ok {
			return value, true
		}
	}
	if slot, ok := f.fieldSlot(name); ok && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		if resolved.ok {
			return resolved.value, true
		}
	}
	return Value{}, false
}

func (f FactSnapshot) fieldSlot(name string) (int, bool) {
	for i, spec := range f.fieldSpecs {
		if spec.Name == name {
			return i, true
		}
	}
	return -1, false
}

func materializeFieldsFromSlots(slots []factSlot, specs []FieldSpec) Fields {
	if len(slots) == 0 || len(specs) == 0 {
		return nil
	}

	out := make(Fields, len(specs))
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		slot := slots[i]
		if !slot.ok {
			continue
		}
		out[spec.Name] = cloneValue(slot.value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func materializePresenceFromSlots(slots []factSlot, specs []FieldSpec) map[string]FieldPresence {
	if len(slots) == 0 || len(specs) == 0 {
		return nil
	}

	out := make(map[string]FieldPresence, len(specs))
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		out[spec.Name] = slots[i].presence.fieldPresence()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneFactSlots(in []factSlot) []factSlot {
	if len(in) == 0 {
		return nil
	}
	out := make([]factSlot, len(in))
	for i, slot := range in {
		out[i] = factSlot{
			value:    cloneValue(slot.value),
			presence: slot.presence,
			ok:       slot.ok,
		}
	}
	return out
}

func emitDuplicateKeyFields(b *strings.Builder, template Template, fields Fields, slots []factSlot) {
	if template.duplicatePolicy == DuplicateAllow {
		return
	}

	if template.duplicatePolicy == DuplicateUniqueKey {
		emitDuplicateKeyFieldsByNames(b, template, fields, slots)
		return
	}

	if len(slots) > 0 && len(template.fields) > 0 {
		emitDuplicateKeyFieldsByTemplateOrder(b, template.fields, slots)
		return
	}

	if fields == nil {
		return
	}
	b.WriteString(fields.duplicateKey())
}

func emitDuplicateKeyFieldsByNames(b *strings.Builder, template Template, fields Fields, slots []factSlot) {
	if len(template.duplicateKeyNames) == 0 {
		return
	}

	if len(slots) > 0 && len(template.duplicateKeySlots) == len(template.duplicateKeyNames) {
		for i, slot := range template.duplicateKeySlots {
			if slot < 0 || slot >= len(slots) {
				continue
			}
			resolved := slots[slot]
			if resolved.ok {
				writeDuplicateKeyEntry(b, template.duplicateKeyNames[i], resolved.value)
			}
		}
		return
	}

	for _, fieldName := range template.duplicateKeyNames {
		if value, ok := duplicateFieldValue(fieldName, template, fields, slots); ok {
			writeDuplicateKeyEntry(b, fieldName, value)
		}
	}
}

func emitDuplicateKeyFieldsByTemplateOrder(b *strings.Builder, specs []FieldSpec, slots []factSlot) {
	for i, spec := range specs {
		if i >= len(slots) {
			break
		}
		slot := slots[i]
		if !slot.ok {
			continue
		}
		writeDuplicateKeyEntry(b, spec.Name, slot.value)
	}
}

func duplicateFieldValue(fieldName string, template Template, fields Fields, slots []factSlot) (Value, bool) {
	if len(slots) > 0 {
		if slot, ok := template.fieldSlot(fieldName); ok && slot >= 0 && slot < len(slots) {
			resolved := slots[slot]
			if resolved.ok {
				return resolved.value, true
			}
			return Value{}, false
		}
	}
	value, ok := fields[fieldName]
	return value, ok
}

func writeDuplicateKeyEntry(b *strings.Builder, fieldName string, value Value) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	encodeValueForDuplicateKey(b, value)
	b.WriteByte(';')
}

func duplicateKeyCapacity(name string, template Template, fields Fields, slots []factSlot) int {
	size := len("name:") + len(name) + len("|template:") + len(template.key) + len("|fields:")
	if template.duplicatePolicy == DuplicateAllow {
		return size
	}
	if len(slots) > 0 {
		if template.duplicatePolicy == DuplicateUniqueKey {
			if len(template.duplicateKeySlots) == len(template.duplicateKeyNames) {
				for i, slot := range template.duplicateKeySlots {
					if slot < 0 || slot >= len(slots) {
						continue
					}
					resolved := slots[slot]
					if resolved.ok {
						size += len(template.duplicateKeyNames[i]) + 2 + duplicateKeyValueCapacity(resolved.value)
					}
				}
				return size
			}
		} else if len(template.fields) > 0 {
			for i, spec := range template.fields {
				if i >= len(slots) {
					break
				}
				if !slots[i].ok {
					continue
				}
				size += len(spec.Name) + 2 + duplicateKeyValueCapacity(slots[i].value)
			}
			return size
		}
	}
	if template.duplicatePolicy == DuplicateUniqueKey {
		for _, fieldName := range template.duplicateKeyNames {
			if value, ok := fields[fieldName]; ok {
				size += len(fieldName) + 2 + duplicateKeyValueCapacity(value)
			}
		}
		return size
	}
	if fields == nil {
		return size
	}
	for key, value := range fields {
		size += len(key) + 2 + duplicateKeyValueCapacity(value)
	}
	return size
}
