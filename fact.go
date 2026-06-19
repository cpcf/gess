package gess

import (
	"maps"
	"math"
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

type conditionFactRef struct {
	id          FactID
	name        string
	templateKey TemplateKey
	version     FactVersion
	recency     Recency
	generation  Generation
	fields      Fields
	fieldSlots  []factSlot
	fieldSpecs  []FieldSpec
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

func (f *workingFact) compiledFieldValue(field string, slot int) (Value, bool) {
	if f == nil {
		return Value{}, false
	}
	if slot >= 0 && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}
	return f.fieldValue(field)
}

func (f FactSnapshot) Support() FactSupportProvenance {
	return f.support
}

func newConditionFactRefFromSnapshot(snapshot FactSnapshot) conditionFactRef {
	return conditionFactRef{
		id:          snapshot.id,
		name:        snapshot.name,
		templateKey: snapshot.templateKey,
		version:     snapshot.version,
		recency:     snapshot.recency,
		generation:  snapshot.generation,
		fields:      snapshot.fields,
		fieldSlots:  snapshot.fieldSlots,
		fieldSpecs:  snapshot.fieldSpecs,
	}
}

func newConditionFactRefFromWorkingFact(fact *workingFact) conditionFactRef {
	if fact == nil {
		return conditionFactRef{}
	}
	return conditionFactRef{
		id:          fact.id,
		name:        fact.name,
		templateKey: fact.templateKey,
		version:     fact.version,
		recency:     fact.recency,
		generation:  fact.id.Generation(),
		fieldSlots:  fact.fieldSlots,
	}
}

func (f conditionFactRef) ID() FactID {
	return f.id
}

func (f conditionFactRef) Name() string {
	return f.name
}

func (f conditionFactRef) TemplateKey() TemplateKey {
	return f.templateKey
}

func (f conditionFactRef) Version() FactVersion {
	return f.version
}

func (f conditionFactRef) Recency() Recency {
	return f.recency
}

func (f conditionFactRef) Generation() Generation {
	return f.generation
}

func (f conditionFactRef) compiledFieldValue(field string, slot int) (Value, bool) {
	if slot >= 0 && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}
	if f.fields != nil {
		value, ok := f.fields[field]
		if ok {
			return value, true
		}
	}
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.fieldSlots) {
		resolved := f.fieldSlots[slot]
		return resolved.value, resolved.ok
	}
	return Value{}, false
}

func (f conditionFactRef) Fields() Fields {
	if f.fields != nil {
		return cloneFields(f.fields)
	}
	return materializeFieldsFromSlots(f.fieldSlots, f.fieldSpecs)
}

func (f conditionFactRef) Field(name string) (Value, bool) {
	value, ok := f.compiledFieldValue(name, -1)
	if !ok {
		return Value{}, false
	}
	return cloneValue(value), true
}

func (f conditionFactRef) FieldPresence(field string) (FieldPresence, bool) {
	if slot, ok := f.fieldSlot(field); ok && slot < len(f.fieldSlots) {
		return f.fieldSlots[slot].presence.fieldPresence(), true
	}
	return FieldPresence(""), false
}

func (f conditionFactRef) FieldPresenceMap() map[string]FieldPresence {
	return materializePresenceFromSlots(f.fieldSlots, f.fieldSpecs)
}

func (f conditionFactRef) snapshot() FactSnapshot {
	return FactSnapshot{
		id:          f.id,
		name:        f.name,
		templateKey: f.templateKey,
		version:     f.version,
		recency:     f.recency,
		generation:  f.generation,
		fields:      cloneFields(f.fields),
		fieldSlots:  cloneFactSlots(f.fieldSlots),
		fieldSpecs:  f.fieldSpecs,
	}
}

func (f conditionFactRef) fieldSlot(name string) (int, bool) {
	for i, spec := range f.fieldSpecs {
		if spec.Name == name {
			return i, true
		}
	}
	return -1, false
}

type workingFact struct {
	id            FactID
	name          string
	templateKey   TemplateKey
	version       FactVersion
	recency       Recency
	fields        Fields
	fieldSlots    []factSlot
	fieldPresence map[string]FieldPresence
	dupIndex      duplicateIndexKey
}

type duplicateIndexKind uint8

const (
	duplicateIndexString duplicateIndexKind = iota
	duplicateIndexSingleInt
	duplicateIndexDoubleInt
	duplicateIndexSingleScalar
	duplicateIndexDoubleScalar
)

type duplicateScalarKind uint8

const (
	duplicateScalarNull duplicateScalarKind = iota
	duplicateScalarBool
	duplicateScalarInt
	duplicateScalarFloat
	duplicateScalarString
)

type duplicateScalarKey struct {
	kind        duplicateScalarKind
	bits        uint64
	stringValue string
}

func duplicateScalarKeyFromValue(value Value) (duplicateScalarKey, bool) {
	switch value.Kind() {
	case ValueNull:
		return duplicateScalarKey{kind: duplicateScalarNull}, true
	case ValueBool:
		var bits uint64
		if value.boolValue {
			bits = 1
		}
		return duplicateScalarKey{kind: duplicateScalarBool, bits: bits}, true
	case ValueInt:
		return duplicateScalarKey{kind: duplicateScalarInt, bits: uint64(value.intValue)}, true
	case ValueFloat:
		floating := value.floatValue
		if math.IsNaN(floating) {
			return duplicateScalarKey{}, false
		}
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return duplicateScalarKey{kind: duplicateScalarInt, bits: uint64(int64(floating))}, true
		}
		return duplicateScalarKey{kind: duplicateScalarFloat, bits: math.Float64bits(floating)}, true
	case ValueString:
		return duplicateScalarKey{kind: duplicateScalarString, stringValue: value.stringValue}, true
	default:
		return duplicateScalarKey{}, false
	}
}

func (k duplicateScalarKey) value() Value {
	switch k.kind {
	case duplicateScalarNull:
		return NullValue()
	case duplicateScalarBool:
		return newBoolValue(k.bits != 0)
	case duplicateScalarInt:
		return newIntValue(int64(k.bits))
	case duplicateScalarFloat:
		return newFloatValue(math.Float64frombits(k.bits))
	case duplicateScalarString:
		return newStringValue(k.stringValue)
	default:
		return Value{}
	}
}

type duplicateIndexKey struct {
	kind        duplicateIndexKind
	templateKey TemplateKey
	firstInt    int64
	secondInt   int64
	first       duplicateScalarKey
	second      duplicateScalarKey
	stringKey   DuplicateKey
}

func (k duplicateIndexKey) isZero() bool {
	return k.kind == duplicateIndexString && k.templateKey == "" && k.stringKey == ""
}

func (k duplicateIndexKey) publicKeyForTemplate(name string, template Template) DuplicateKey {
	switch k.kind {
	case duplicateIndexSingleInt:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[0], k.firstInt)
		return DuplicateKey(b.String())
	case duplicateIndexDoubleInt:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[0], k.firstInt)
		writeDuplicateIntKeyEntry(&b, template.duplicateKeyNames[1], k.secondInt)
		return DuplicateKey(b.String())
	case duplicateIndexSingleScalar:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateScalarKeyEntry(&b, template.duplicateKeyNames[0], k.first)
		return DuplicateKey(b.String())
	case duplicateIndexDoubleScalar:
		var b strings.Builder
		b.Grow(k.publicKeyCapacity(name, template))
		b.WriteString("name:")
		b.WriteString(name)
		b.WriteString("|template:")
		b.WriteString(k.templateKey.String())
		b.WriteString("|fields:")
		writeDuplicateScalarKeyEntry(&b, template.duplicateKeyNames[0], k.first)
		writeDuplicateScalarKeyEntry(&b, template.duplicateKeyNames[1], k.second)
		return DuplicateKey(b.String())
	default:
		return k.stringKey
	}
}

func (k duplicateIndexKey) publicKeyCapacity(name string, template Template) int {
	size := len("name:") + len(name) + len("|template:") + len(k.templateKey) + len("|fields:")
	switch k.kind {
	case duplicateIndexSingleInt:
		size += len(template.duplicateKeyNames[0]) + 2 + len("number:") + int64Len(k.firstInt)
	case duplicateIndexDoubleInt:
		size += len(template.duplicateKeyNames[0]) + 2 + len("number:") + int64Len(k.firstInt)
		size += len(template.duplicateKeyNames[1]) + 2 + len("number:") + int64Len(k.secondInt)
	case duplicateIndexSingleScalar:
		size += len(template.duplicateKeyNames[0]) + 2 + duplicateScalarKeyValueCapacity(k.first)
	case duplicateIndexDoubleScalar:
		size += len(template.duplicateKeyNames[0]) + 2 + duplicateScalarKeyValueCapacity(k.first)
		size += len(template.duplicateKeyNames[1]) + 2 + duplicateScalarKeyValueCapacity(k.second)
	}
	return size
}

func writeDuplicateIntKeyEntry(b *strings.Builder, fieldName string, value int64) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	b.WriteString("number:")
	var buf [20]byte
	b.Write(strconv.AppendInt(buf[:0], value, 10))
	b.WriteByte(';')
}

func writeDuplicateScalarKeyEntry(b *strings.Builder, fieldName string, value duplicateScalarKey) {
	b.WriteString(fieldName)
	b.WriteByte('=')
	encodeDuplicateScalarKey(b, value)
	b.WriteByte(';')
}

func encodeDuplicateScalarKey(b *strings.Builder, value duplicateScalarKey) {
	switch value.kind {
	case duplicateScalarNull:
		b.WriteString("null")
	case duplicateScalarBool:
		b.WriteString("bool:")
		if value.bits != 0 {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case duplicateScalarInt:
		b.WriteString("number:")
		var buf [20]byte
		b.Write(strconv.AppendInt(buf[:0], int64(value.bits), 10))
	case duplicateScalarFloat:
		b.WriteString("number:")
		floating := math.Float64frombits(value.bits)
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			var buf [20]byte
			b.Write(strconv.AppendInt(buf[:0], int64(floating), 10))
			return
		}
		var buf [32]byte
		b.Write(strconv.AppendFloat(buf[:0], floating, 'g', -1, 64))
	case duplicateScalarString:
		b.WriteString("string:")
		b.WriteString(strconv.Quote(value.stringValue))
	}
}

func duplicateScalarKeyValueCapacity(value duplicateScalarKey) int {
	switch value.kind {
	case duplicateScalarNull:
		return len("null")
	case duplicateScalarBool:
		if value.bits != 0 {
			return len("bool:true")
		}
		return len("bool:false")
	case duplicateScalarInt:
		return len("number:") + int64Len(int64(value.bits))
	case duplicateScalarFloat:
		floating := math.Float64frombits(value.bits)
		if math.Trunc(floating) == floating &&
			floating <= float64(maxExactFloatInt) &&
			floating >= float64(-maxExactFloatInt) {
			return len("number:") + int64Len(int64(floating))
		}
		var buf [32]byte
		return len("number:") + len(strconv.AppendFloat(buf[:0], floating, 'g', -1, 64))
	case duplicateScalarString:
		return len("string:") + len(value.stringValue) + len(`""`) + len(`\u0000`)
	default:
		return len("any")
	}
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
	return f.snapshotForRevision(nil)
}

func (f *workingFact) snapshotForRevision(revision *Ruleset) FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.name,
		templateKey:   f.templateKey,
		version:       f.version,
		recency:       f.recency,
		generation:    f.id.Generation(),
		fields:        cloneFields(f.fields),
		fieldSlots:    cloneFactSlots(f.fieldSlots),
		fieldSpecs:    f.fieldSpecsForRevision(revision),
		fieldPresence: cloneFieldPresence(f.fieldPresence),
		support:       FactSupportProvenance{State: FactSupportStated},
	}
}

func (f *workingFact) detachedSnapshot() FactSnapshot {
	return f.detachedSnapshotForRevision(nil)
}

func (f *workingFact) detachedSnapshotForRevision(revision *Ruleset) FactSnapshot {
	return FactSnapshot{
		id:            f.id,
		name:          f.name,
		templateKey:   f.templateKey,
		version:       f.version,
		recency:       f.recency,
		generation:    f.id.Generation(),
		fields:        f.fields,
		fieldSlots:    f.fieldSlots,
		fieldSpecs:    f.fieldSpecsForRevision(revision),
		fieldPresence: f.fieldPresence,
		support:       FactSupportProvenance{State: FactSupportStated},
	}
}

func (f *workingFact) fieldSpecsForRevision(revision *Ruleset) []FieldSpec {
	if f == nil || len(f.fieldSlots) == 0 || revision == nil {
		return nil
	}
	template, ok := revision.templateByKey(f.templateKey)
	if !ok {
		return nil
	}
	return template.fields
}

func (f *workingFact) publicDuplicateKey(revision *Ruleset) DuplicateKey {
	if f == nil || f.dupIndex.isZero() {
		return ""
	}
	template := Template{key: f.templateKey}
	if revision != nil {
		if resolved, ok := revision.templateByKey(f.templateKey); ok {
			template = resolved
		}
	}
	return f.dupIndex.publicKeyForTemplate(f.name, template)
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
	_, duplicateKey := makeDuplicateIdentityForValidatedFact(name, template, fields, slots)
	return duplicateKey
}

func makeDuplicateIdentityForValidatedFact(name string, template Template, fields Fields, slots []factSlot) (duplicateIndexKey, DuplicateKey) {
	index := makeDuplicateIndexForValidatedFact(name, template, fields, slots)
	return index, index.publicKeyForTemplate(name, template)
}

func makeDuplicateIndexForValidatedFact(name string, template Template, fields Fields, slots []factSlot) duplicateIndexKey {
	if template.duplicatePolicy == DuplicateAllow {
		return duplicateIndexKey{}
	}

	if index, ok := makeTypedDuplicateIndexForValidatedFact(name, template, fields, slots); ok {
		return index
	}

	if len(slots) > 0 {
		duplicateKey := makeDuplicateKeyForTemplateWithSlots(name, template, fields, slots)
		return duplicateIndexKey{
			kind:        duplicateIndexString,
			templateKey: template.key,
			stringKey:   duplicateKey,
		}
	}

	duplicateKey := makeDuplicateKeyForTemplate(name, template, fields)
	return duplicateIndexKey{
		kind:        duplicateIndexString,
		templateKey: template.key,
		stringKey:   duplicateKey,
	}
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

func makeTypedDuplicateIndexForValidatedFact(name string, template Template, fields Fields, slots []factSlot) (duplicateIndexKey, bool) {
	switch template.duplicateIndexMode {
	case duplicateIndexSingleScalar:
		fieldName := template.duplicateKeyNames[0]
		value, ok := duplicateFieldValue(fieldName, template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		scalar, ok := duplicateScalarKeyFromValue(value)
		if !ok {
			return duplicateIndexKey{}, false
		}
		if scalar.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexSingleInt,
				templateKey: template.key,
				firstInt:    int64(scalar.bits),
			}, true
		}
		return duplicateIndexKey{
			kind:        duplicateIndexSingleScalar,
			templateKey: template.key,
			first:       scalar,
		}, true
	case duplicateIndexDoubleScalar:
		firstField := template.duplicateKeyNames[0]
		secondField := template.duplicateKeyNames[1]
		firstValue, ok := duplicateFieldValue(firstField, template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		secondValue, ok := duplicateFieldValue(secondField, template, fields, slots)
		if !ok {
			return duplicateIndexKey{}, false
		}
		firstScalar, ok := duplicateScalarKeyFromValue(firstValue)
		if !ok {
			return duplicateIndexKey{}, false
		}
		secondScalar, ok := duplicateScalarKeyFromValue(secondValue)
		if !ok {
			return duplicateIndexKey{}, false
		}
		if firstScalar.kind == duplicateScalarInt && secondScalar.kind == duplicateScalarInt {
			return duplicateIndexKey{
				kind:        duplicateIndexDoubleInt,
				templateKey: template.key,
				firstInt:    int64(firstScalar.bits),
				secondInt:   int64(secondScalar.bits),
			}, true
		}
		return duplicateIndexKey{
			kind:        duplicateIndexDoubleScalar,
			templateKey: template.key,
			first:       firstScalar,
			second:      secondScalar,
		}, true
	default:
		return duplicateIndexKey{}, false
	}
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

func (f *workingFact) fieldValue(name string) (Value, bool) {
	if f == nil {
		return Value{}, false
	}
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

func (f *workingFact) fieldSlot(name string) (int, bool) {
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
