package engine

import (
	"reflect"
	"testing"

	gessrules "github.com/cpcf/gess/rules"
)

func TestMutationRulesAliases(t *testing.T) {
	assertSameBoundaryType[MutationKind, gessrules.MutationKind](t, "MutationKind")
	assertSameBoundaryType[DuplicateKey, gessrules.DuplicateKey](t, "DuplicateKey")
	assertSameBoundaryType[FactSupportState, gessrules.FactSupportState](t, "FactSupportState")
	assertSameBoundaryType[FactSupportProvenance, gessrules.FactSupportProvenance](t, "FactSupportProvenance")
	assertSameBoundaryType[FieldChange, gessrules.FieldChange](t, "FieldChange")
	assertSameBoundaryType[AssertStatus, gessrules.AssertStatus](t, "AssertStatus")
	assertSameBoundaryType[ModifyStatus, gessrules.ModifyStatus](t, "ModifyStatus")
	assertSameBoundaryType[RetractStatus, gessrules.RetractStatus](t, "RetractStatus")
}

func TestMutationBoundaryExportedFieldSetsMatchRules(t *testing.T) {
	assertExportedFieldSet(t, "FactSupportProvenance", reflect.TypeFor[FactSupportProvenance](), reflect.TypeFor[gessrules.FactSupportProvenance]())
	assertExportedFieldSet(t, "FieldChange", reflect.TypeFor[FieldChange](), reflect.TypeFor[gessrules.FieldChange]())
	assertExportedFieldSet(t, "MutationDelta", reflect.TypeFor[MutationDelta](), reflect.TypeFor[gessrules.MutationDelta]())
	assertExportedFieldSet(t, "AssertResult", reflect.TypeFor[AssertResult](), reflect.TypeFor[gessrules.AssertResult]())
	assertExportedFieldSet(t, "ModifyResult", reflect.TypeFor[ModifyResult](), reflect.TypeFor[gessrules.ModifyResult]())
	assertExportedFieldSet(t, "RetractResult", reflect.TypeFor[RetractResult](), reflect.TypeFor[gessrules.RetractResult]())
}

func assertSameBoundaryType[A, B any](t *testing.T, name string) {
	t.Helper()
	a := reflect.TypeFor[A]()
	b := reflect.TypeFor[B]()
	if a != b {
		t.Fatalf("%s type = %v, want %v", name, a, b)
	}
}

func assertExportedFieldSet(t *testing.T, name string, engineType, rulesType reflect.Type) {
	t.Helper()
	engineFields := exportedFieldNames(engineType)
	rulesFields := exportedFieldNames(rulesType)
	if !reflect.DeepEqual(engineFields, rulesFields) {
		t.Fatalf("%s exported fields = %v, want %v", name, engineFields, rulesFields)
	}
}

func exportedFieldNames(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for field := range typ.Fields() {
		if field.IsExported() {
			fields = append(fields, field.Name)
		}
	}
	return fields
}
