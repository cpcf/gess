package gess

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type DuplicatePolicy int

const (
	DuplicateStructural DuplicatePolicy = iota
	DuplicateAllow
	DuplicateUniqueKey
)

type FieldPresence string

const (
	FieldPresenceOmitted  FieldPresence = "omitted"
	FieldPresenceDefault  FieldPresence = "default"
	FieldPresenceExplicit FieldPresence = "explicit"
)

type FieldSpec struct {
	Name          string
	Kind          ValueKind
	Required      bool
	Default       any
	HasDefault    bool
	AllowedValues []any
}

type TemplateSpec struct {
	Name              string
	Key               TemplateKey
	CompatibilityKey  TemplateKey
	Fields            []FieldSpec
	DuplicatePolicy   DuplicatePolicy
	DuplicateKeyNames []string
	Closed            bool
}

type Template struct {
	name               string
	key                TemplateKey
	compatibilityKey   TemplateKey
	fields             []FieldSpec
	fieldsByName       map[string]FieldSpec
	fieldIndexes       map[string]int
	fieldDefaults      map[string]Value
	fieldAllowed       map[string][]Value
	fieldValidation    []fieldValidationSpec
	duplicatePolicy    DuplicatePolicy
	duplicateKeyNames  []string
	duplicateKeySlots  []int
	duplicateIndexMode duplicateIndexKind
	closed             bool
}

type fieldValidationSpec struct {
	kind          ValueKind
	required      bool
	hasDefault    bool
	defaultValue  Value
	allowedValues []Value
}

func (s TemplateSpec) clone() TemplateSpec {
	out := s
	out.Fields = make([]FieldSpec, len(s.Fields))
	for i, field := range s.Fields {
		out.Fields[i] = cloneFieldSpec(field)
	}
	out.DuplicateKeyNames = append(out.DuplicateKeyNames[:0:0], s.DuplicateKeyNames...)
	return out
}

func cloneFieldSpec(field FieldSpec) FieldSpec {
	out := field
	out.Default = cloneSpecValue(field.Default)
	out.AllowedValues = make([]any, len(field.AllowedValues))
	for i, value := range field.AllowedValues {
		out.AllowedValues[i] = cloneSpecValue(value)
	}
	return out
}

func cloneSpecValue(value any) any {
	switch typed := value.(type) {
	case Value:
		return cloneValue(typed)
	default:
		return typed
	}
}

func (t Template) Name() string {
	return t.name
}

func (t Template) Key() TemplateKey {
	return t.key
}

func (t Template) CompatibilityKey() TemplateKey {
	return t.compatibilityKey
}

func (t Template) DuplicatePolicy() DuplicatePolicy {
	return t.duplicatePolicy
}

func (t Template) DuplicateKeys() []string {
	out := make([]string, len(t.duplicateKeyNames))
	copy(out, t.duplicateKeyNames)
	return out
}

func (t Template) Closed() bool {
	return t.closed
}

func (t Template) Fields() []FieldSpec {
	out := make([]FieldSpec, len(t.fields))
	for i, field := range t.fields {
		out[i] = cloneFieldSpec(field)
	}
	return out
}

func (t Template) fieldSlot(field string) (int, bool) {
	if len(t.fieldIndexes) == 0 {
		return 0, false
	}
	slot, ok := t.fieldIndexes[field]
	return slot, ok
}

func (t Template) buildFieldSlots(fields Fields, presence map[string]FieldPresence) []factSlot {
	if !t.closed || len(t.fields) == 0 {
		return nil
	}

	slots := make([]factSlot, len(t.fields))
	for i, field := range t.fields {
		slots[i].presence = fieldPresenceOmitted
		if presence != nil {
			if next, ok := presence[field.Name]; ok {
				slots[i].presence = encodeFieldPresence(next)
			}
		}
		value, ok := fields[field.Name]
		if !ok {
			continue
		}
		slots[i].value = cloneValue(value)
		slots[i].ok = true
	}
	return slots
}

func (t Template) buildValidatedFieldSlots(fields Fields) ([]factSlot, error) {
	if !t.closed || len(t.fields) == 0 {
		return nil, nil
	}
	if len(t.fieldValidation) != len(t.fields) {
		return t.buildValidatedFieldSlotsFromSpecs(fields)
	}

	slots := make([]factSlot, len(t.fields))
	for i := range t.fields {
		slots[i].presence = fieldPresenceOmitted
	}

	for fieldName, value := range fields {
		slot, ok := t.fieldSlot(fieldName)
		if !ok {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "unknown field",
			}
		}

		validation := t.fieldValidation[slot]
		if validation.kind != ValueAny && !isValueCompatibleWithKind(validation.kind, value) {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "invalid type",
			}
		}
		if len(validation.allowedValues) > 0 && !valueAllowed(validation.allowedValues, value) {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "value not in allowed set",
			}
		}

		slots[slot].value = cloneValue(value)
		slots[slot].ok = true
		slots[slot].presence = fieldPresenceExplicit
	}

	for i, validation := range t.fieldValidation {
		if slots[i].ok {
			continue
		}

		if validation.hasDefault {
			slots[i].value = cloneValue(validation.defaultValue)
			slots[i].ok = true
			slots[i].presence = fieldPresenceDefault
			continue
		}

		slots[i].presence = fieldPresenceOmitted
		if validation.required {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    t.fields[i].Name,
				Reason:       "required field is missing",
			}
		}
	}

	return slots, nil
}

func (t Template) buildValidatedFieldSlotsFromSpecs(fields Fields) ([]factSlot, error) {
	slots := make([]factSlot, len(t.fields))
	for i := range t.fields {
		slots[i].presence = fieldPresenceOmitted
	}

	for fieldName, value := range fields {
		slot, ok := t.fieldSlot(fieldName)
		if !ok {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "unknown field",
			}
		}

		field := t.fields[slot]
		if field.Kind != ValueAny && !isValueCompatibleWithKind(field.Kind, value) {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "invalid type",
			}
		}
		if allowed, ok := t.fieldAllowed[fieldName]; ok && !valueAllowed(allowed, value) {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "value not in allowed set",
			}
		}

		slots[slot].value = cloneValue(value)
		slots[slot].ok = true
		slots[slot].presence = fieldPresenceExplicit
	}

	for i, field := range t.fields {
		if slots[i].ok {
			continue
		}

		if defaultValue, hasDefault := t.fieldDefaults[field.Name]; hasDefault {
			slots[i].value = cloneValue(defaultValue)
			slots[i].ok = true
			slots[i].presence = fieldPresenceDefault
			continue
		}

		slots[i].presence = fieldPresenceOmitted
		if field.Required {
			return nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    field.Name,
				Reason:       "required field is missing",
			}
		}
	}

	return slots, nil
}

func (t Template) clone() Template {
	out := t
	out.fields = make([]FieldSpec, len(t.fields))
	for i, field := range t.fields {
		out.fields[i] = cloneFieldSpec(field)
	}

	if t.fieldsByName != nil {
		out.fieldsByName = make(map[string]FieldSpec, len(t.fieldsByName))
		maps.Copy(out.fieldsByName, t.fieldsByName)
	}
	if t.fieldIndexes != nil {
		out.fieldIndexes = make(map[string]int, len(t.fieldIndexes))
		maps.Copy(out.fieldIndexes, t.fieldIndexes)
	}

	out.fieldDefaults = cloneFieldDefaults(t.fieldDefaults)
	out.fieldAllowed = cloneFieldAllowed(t.fieldAllowed)
	out.fieldValidation = cloneFieldValidation(t.fieldValidation)
	out.duplicateKeyNames = append(out.duplicateKeyNames[:0:0], t.duplicateKeyNames...)
	out.duplicateKeySlots = append(out.duplicateKeySlots[:0:0], t.duplicateKeySlots...)

	return out
}

func (t Template) spec() TemplateSpec {
	return TemplateSpec{
		Name:              t.name,
		Key:               t.key,
		CompatibilityKey:  t.compatibilityKey,
		Fields:            t.Fields(),
		DuplicatePolicy:   t.duplicatePolicy,
		DuplicateKeyNames: t.DuplicateKeys(),
		Closed:            t.closed,
	}
}

func (t Template) applyDefaultsAndValidate(raw Fields) (Fields, map[string]FieldPresence, error) {
	provided := cloneFields(raw)
	result := make(Fields, len(raw)+len(t.fields))
	maps.Copy(result, provided)
	presence := make(map[string]FieldPresence, len(provided)+len(t.fields))

	for fieldName, value := range provided {
		declared, known := t.fieldsByName[fieldName]
		if !known {
			if t.closed {
				return nil, nil, &ValidationError{TemplateName: t.name, FieldName: fieldName, Reason: "unknown field"}
			}
			presence[fieldName] = FieldPresenceExplicit
			continue
		}

		if declared.Kind != ValueAny {
			if !isValueCompatibleWithKind(declared.Kind, value) {
				return nil, nil, &ValidationError{
					TemplateName: t.name,
					FieldName:    fieldName,
					Reason:       "invalid type",
				}
			}
		}
		if allowed, ok := t.fieldAllowed[fieldName]; ok && !valueAllowed(allowed, value) {
			return nil, nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    fieldName,
				Reason:       "value not in allowed set",
			}
		}

		presence[fieldName] = FieldPresenceExplicit
	}

	for _, field := range t.fields {
		_, hasField := provided[field.Name]
		if hasField {
			continue
		}

		defaultValue, hasDefault := t.fieldDefaults[field.Name]
		if hasDefault {
			result[field.Name] = cloneValue(defaultValue)
			presence[field.Name] = FieldPresenceDefault
			continue
		}

		presence[field.Name] = FieldPresenceOmitted
		if field.Required {
			return nil, nil, &ValidationError{
				TemplateName: t.name,
				FieldName:    field.Name,
				Reason:       "required field is missing",
			}
		}
	}

	return result, presence, nil
}

func compileTemplateSpec(spec TemplateSpec) (Template, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Template{}, &ValidationError{Reason: "template name is required"}
	}

	key := TemplateKey(strings.TrimSpace(string(spec.Key)))
	if key == "" {
		key = TemplateKey(name)
	}

	compatibilityKey := spec.CompatibilityKey
	compatibilityKey = TemplateKey(strings.TrimSpace(string(compatibilityKey)))
	if compatibilityKey == "" {
		compatibilityKey = key
	}

	switch spec.DuplicatePolicy {
	case DuplicateStructural, DuplicateAllow, DuplicateUniqueKey:
	default:
		return Template{}, &ValidationError{TemplateName: name, Reason: "invalid duplicate policy"}
	}

	fieldsByName := make(map[string]FieldSpec, len(spec.Fields))
	fieldDefaults := make(map[string]Value, len(spec.Fields))
	fieldAllowed := make(map[string][]Value, len(spec.Fields))
	fields := make([]FieldSpec, 0, len(spec.Fields))

	for _, field := range spec.Fields {
		field.Name = strings.TrimSpace(field.Name)
		if field.Name == "" {
			return Template{}, &ValidationError{
				TemplateName: name,
				Reason:       "field name is required",
			}
		}
		if _, exists := fieldsByName[field.Name]; exists {
			return Template{}, &ValidationError{
				TemplateName: name,
				FieldName:    field.Name,
				Reason:       "duplicate field",
			}
		}

		if field.Kind == "" {
			field.Kind = ValueAny
		}
		if !isSupportedKind(field.Kind) {
			return Template{}, &ValidationError{
				TemplateName: name,
				FieldName:    field.Name,
				Reason:       "unsupported field kind",
			}
		}

		hasDefault := field.HasDefault || field.Default != nil
		field.HasDefault = hasDefault
		if hasDefault {
			defaultValue, err := canonicalValue(field.Default)
			if err != nil {
				return Template{}, &ValidationError{
					TemplateName: name,
					FieldName:    field.Name,
					Reason:       "invalid default",
					Err:          err,
				}
			}
			if field.Kind != ValueAny && !isValueCompatibleWithKind(field.Kind, defaultValue) {
				return Template{}, &ValidationError{
					TemplateName: name,
					FieldName:    field.Name,
					Reason:       "invalid default type",
				}
			}
			fieldDefaults[field.Name] = defaultValue
			field.Default = cloneValue(defaultValue)
		}

		if len(field.AllowedValues) > 0 {
			allowedValues, err := normalizeAllowedValues(field.AllowedValues)
			if err != nil {
				return Template{}, &ValidationError{
					TemplateName: name,
					FieldName:    field.Name,
					Reason:       "invalid allowed value",
					Err:          err,
				}
			}
			for _, allowed := range allowedValues {
				if field.Kind != ValueAny && !isValueCompatibleWithKind(field.Kind, allowed) {
					return Template{}, &ValidationError{
						TemplateName: name,
						FieldName:    field.Name,
						Reason:       "invalid allowed value type",
					}
				}
			}

			if field.Default != nil {
				defaultValue := fieldDefaults[field.Name]
				if !valueAllowed(allowedValues, defaultValue) {
					return Template{}, &ValidationError{
						TemplateName: name,
						FieldName:    field.Name,
						Reason:       "default value not allowed",
					}
				}
			}

			fieldAllowed[field.Name] = allowedValues
			field.AllowedValues = valuesAsAny(allowedValues)
		}

		fields = append(fields, field)
		fieldsByName[field.Name] = field
	}

	slices.SortFunc(fields, func(a, b FieldSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	fieldIndexes := make(map[string]int, len(fields))
	for i, field := range fields {
		fieldIndexes[field.Name] = i
	}
	fieldValidation := make([]fieldValidationSpec, len(fields))
	for i, field := range fields {
		validation := fieldValidationSpec{
			kind:     field.Kind,
			required: field.Required,
		}
		if field.HasDefault {
			validation.hasDefault = true
			validation.defaultValue = fieldDefaults[field.Name]
		}
		if allowed := fieldAllowed[field.Name]; len(allowed) > 0 {
			validation.allowedValues = allowed
		}
		fieldValidation[i] = validation
	}

	duplicateKeyNames, err := normalizeTemplateDuplicateFields(spec.DuplicateKeyNames, fieldsByName)
	if err != nil {
		return Template{}, &ValidationError{
			TemplateName: name,
			Reason:       err.Error(),
		}
	}

	if spec.DuplicatePolicy != DuplicateUniqueKey && len(duplicateKeyNames) > 0 {
		return Template{}, &ValidationError{
			TemplateName: name,
			Reason:       "duplicate key fields require duplicate unique-key policy",
		}
	}

	if spec.DuplicatePolicy == DuplicateUniqueKey && len(duplicateKeyNames) == 0 {
		return Template{}, &ValidationError{
			TemplateName: name,
			Reason:       "duplicate unique-key policy requires duplicate key fields",
		}
	}
	var duplicateKeySlots []int
	if len(duplicateKeyNames) > 0 {
		duplicateKeySlots = make([]int, len(duplicateKeyNames))
		for i, fieldName := range duplicateKeyNames {
			duplicateKeySlots[i] = fieldIndexes[fieldName]
		}
	}
	duplicateIndexMode := duplicateIndexKeyMode(spec.Closed, spec.DuplicatePolicy, fields, duplicateKeyNames)

	return Template{
		name:               name,
		key:                key,
		compatibilityKey:   compatibilityKey,
		fields:             fields,
		fieldsByName:       fieldsByName,
		fieldIndexes:       fieldIndexes,
		fieldDefaults:      fieldDefaults,
		fieldAllowed:       fieldAllowed,
		fieldValidation:    fieldValidation,
		duplicatePolicy:    spec.DuplicatePolicy,
		duplicateKeyNames:  duplicateKeyNames,
		duplicateKeySlots:  duplicateKeySlots,
		duplicateIndexMode: duplicateIndexMode,
		closed:             spec.Closed,
	}, nil
}

func duplicateIndexKeyMode(closed bool, policy DuplicatePolicy, fields []FieldSpec, duplicateKeyNames []string) duplicateIndexKind {
	if !closed || policy != DuplicateUniqueKey {
		return duplicateIndexString
	}
	switch len(duplicateKeyNames) {
	case 1:
		if isScalarDuplicateFieldKind(fields, duplicateKeyNames[0]) {
			return duplicateIndexSingleScalar
		}
	case 2:
		if isScalarDuplicateFieldKind(fields, duplicateKeyNames[0]) && isScalarDuplicateFieldKind(fields, duplicateKeyNames[1]) {
			return duplicateIndexDoubleScalar
		}
	}
	return duplicateIndexString
}

func isScalarDuplicateFieldKind(fields []FieldSpec, name string) bool {
	for _, field := range fields {
		if field.Name != name {
			continue
		}
		switch field.Kind {
		case ValueNull, ValueBool, ValueInt, ValueFloat, ValueString:
			return true
		default:
			return false
		}
	}
	return false
}

func normalizeTemplateDuplicateFields(raw []string, fieldsByName map[string]FieldSpec) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(raw))
	names := make([]string, 0, len(raw))
	for _, rawName := range raw {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("empty duplicate key field")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate duplicate-key field: %q", name)
		}
		if _, known := fieldsByName[name]; !known {
			return nil, fmt.Errorf("duplicate key field %q not declared", name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	slices.Sort(names)
	return names, nil
}

func isSupportedKind(kind ValueKind) bool {
	switch kind {
	case ValueAny, ValueNull, ValueBool, ValueInt, ValueFloat, ValueString, ValueList, ValueMap:
		return true
	default:
		return false
	}
}

func isValueCompatibleWithKind(kind ValueKind, value Value) bool {
	switch kind {
	case ValueAny:
		return true
	case ValueNull:
		return value.Kind() == ValueNull
	case ValueBool:
		return value.Kind() == ValueBool
	case ValueInt:
		return value.Kind() == ValueInt
	case ValueFloat:
		return value.Kind() == ValueFloat
	case ValueString:
		return value.Kind() == ValueString
	case ValueList:
		return value.Kind() == ValueList
	case ValueMap:
		return value.Kind() == ValueMap
	default:
		return false
	}
}

func valueAllowed(allowed []Value, value Value) bool {
	return slices.ContainsFunc(allowed, value.Equal)
}

func canonicalValues(values []any) ([]Value, error) {
	out := make([]Value, len(values))
	for i, raw := range values {
		canonical, err := canonicalValue(raw)
		if err != nil {
			return nil, err
		}
		out[i] = canonical
	}
	return out, nil
}

func normalizeAllowedValues(values []any) ([]Value, error) {
	canonical, err := canonicalValues(values)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(canonical, func(a, b Value) int {
		return strings.Compare(a.canonicalKey(), b.canonicalKey())
	})

	out := canonical[:0]
	for _, value := range canonical {
		if len(out) == 0 || !out[len(out)-1].Equal(value) {
			out = append(out, value)
		}
	}
	return out, nil
}

func valuesAsAny(values []Value) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = cloneValue(value)
	}
	return out
}

func cloneFieldDefaults(in map[string]Value) map[string]Value {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]Value, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneFieldAllowed(in map[string][]Value) map[string][]Value {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]Value, len(in))
	for fieldName, values := range in {
		cloned := make([]Value, len(values))
		for i, value := range values {
			cloned[i] = cloneValue(value)
		}
		out[fieldName] = cloned
	}
	return out
}

func cloneFieldValidation(in []fieldValidationSpec) []fieldValidationSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]fieldValidationSpec, len(in))
	for i, validation := range in {
		out[i] = fieldValidationSpec{
			kind:          validation.kind,
			required:      validation.required,
			hasDefault:    validation.hasDefault,
			defaultValue:  cloneValue(validation.defaultValue),
			allowedValues: cloneValueSlice(validation.allowedValues),
		}
	}
	return out
}
