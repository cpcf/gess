package engine

import "testing"

func BenchmarkDuplicateIndexLookupInsertDeclaredUniqueSingleScalar(b *testing.B) {
	benchmarkDuplicateIndexLookupInsert(b, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString},
		},
	}, map[string]any{
		"id":     "evt-1",
		"status": "open",
	}, duplicateIndexSingleScalar)
}

func BenchmarkDuplicateIndexLookupInsertDeclaredUniqueDoubleScalar(b *testing.B) {
	benchmarkDuplicateIndexLookupInsert(b, TemplateSpec{
		Name:              "route",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
		},
	}, map[string]any{
		"stream": 7,
		"n":      3,
	}, duplicateIndexDoubleInt)
}

func BenchmarkDuplicateIndexLookupInsertDeclaredUniqueStringIndex(b *testing.B) {
	benchmarkDuplicateIndexLookupInsert(b, TemplateSpec{
		Name:              "event",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	}, map[string]any{
		"id": "evt-1",
	}, duplicateIndexString)
}

var benchmarkDuplicateIndex duplicateIndexKey

func BenchmarkDuplicateIndexBuildDeclaredUniqueDoubleScalarTyped(b *testing.B) {
	benchmarkDuplicateIndexBuildDeclaredUniqueDoubleScalar(b, false)
}

func BenchmarkDuplicateIndexBuildDeclaredUniqueDoubleScalarStringIndex(b *testing.B) {
	benchmarkDuplicateIndexBuildDeclaredUniqueDoubleScalar(b, true)
}

func benchmarkDuplicateIndexBuildDeclaredUniqueDoubleScalar(b *testing.B, forceStringIndex bool) {
	b.Helper()

	definition := NewWorkspace()
	if err := definition.AddTemplate(TemplateSpec{
		Name:              "route",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
			{Name: "lane", Kind: ValueString},
		},
	}); err != nil {
		b.Fatalf("AddTemplate: %v", err)
	}
	revision := mustCompileWorkspace(b, definition)
	template, ok := revision.templateByKey("route")
	if !ok {
		b.Fatal("expected route template")
	}
	fields := mustFields(b, map[string]any{"stream": 7, "n": 3, "lane": "north"})
	slots, err := template.buildValidatedFieldSlots(fields)
	if err != nil {
		b.Fatalf("buildValidatedFieldSlots: %v", err)
	}
	if forceStringIndex {
		template.duplicateIndexMode = duplicateIndexString
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		index := makeDuplicateIndexForValidatedFact(template.Name(), template, nil, slots)
		if index.isZero() {
			b.Fatal("expected duplicate index")
		}
		benchmarkDuplicateIndex = index
	}
}

func benchmarkDuplicateIndexLookupInsert(b *testing.B, spec TemplateSpec, rawFields map[string]any, want duplicateIndexKind) {
	b.Helper()

	definition := NewWorkspace()
	if err := definition.AddTemplate(spec); err != nil {
		b.Fatalf("AddTemplate(%q): %v", spec.Name, err)
	}
	revision := mustCompileWorkspace(b, definition)
	template, ok := revision.Template(spec.Name)
	if !ok {
		b.Fatalf("expected template %q", spec.Name)
	}
	wantMode := want
	switch want {
	case duplicateIndexSingleInt:
		wantMode = duplicateIndexSingleScalar
	case duplicateIndexDoubleInt:
		wantMode = duplicateIndexDoubleScalar
	}
	if got := template.duplicateIndexMode; got != wantMode {
		b.Fatalf("duplicate index mode = %v, want %v", got, wantMode)
	}

	fields := mustFields(b, rawFields)
	factSpace := newFactWorkspace(1, 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		factSpace.reset(1, 2)
		b.StartTimer()

		first, firstKey, inserted, err := factSpace.insertFact(revision, 1, template.Name(), template.Key(), fields)
		if err != nil {
			b.Fatalf("first insert: %v", err)
		}
		if !inserted {
			b.Fatal("first insert reported duplicate")
		}
		if first.duplicateIndex().kind != want {
			b.Fatalf("first duplicate index kind = %v, want %v", first.duplicateIndex().kind, want)
		}

		second, secondKey, inserted, err := factSpace.insertFact(revision, 1, template.Name(), template.Key(), fields)
		if err != nil {
			b.Fatalf("duplicate insert: %v", err)
		}
		if inserted {
			b.Fatal("duplicate insert reported inserted")
		}
		if second == nil {
			b.Fatal("duplicate insert returned nil fact")
		}
		if firstKey != secondKey {
			b.Fatalf("duplicate keys differ: %q != %q", firstKey, secondKey)
		}
		if got, ok := factSpace.duplicateFactID(first.duplicateIndex()); !ok || got != first.id {
			b.Fatalf("duplicate index lookup = (%q, %t), want (%q, true)", got, ok, first.id)
		}
		benchmarkDuplicateKey = secondKey
	}
	if benchmarkDuplicateKey == "" {
		b.Fatal("expected duplicate key")
	}
}
