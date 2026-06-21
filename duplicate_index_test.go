package gess

import (
	"context"
	"math"
	"testing"
)

func TestDuplicateIndexTypedAndCanonicalStringPaths(t *testing.T) {
	cases := []struct {
		name      string
		spec      TemplateSpec
		fields    map[string]any
		wantIndex duplicateIndexKind
	}{
		{
			name: "single-scalar",
			spec: TemplateSpec{
				Name:              "event",
				DuplicatePolicy:   DuplicateUniqueKey,
				DuplicateKeyNames: []string{"id"},
				Fields: []FieldSpec{
					{Name: "id", Kind: ValueString, Required: true},
					{Name: "status", Kind: ValueString},
				},
			},
			fields: map[string]any{
				"id":     "evt-1",
				"status": "open",
			},
			wantIndex: duplicateIndexSingleScalar,
		},
		{
			name: "double-scalar",
			spec: TemplateSpec{
				Name:              "route",
				DuplicatePolicy:   DuplicateUniqueKey,
				DuplicateKeyNames: []string{"stream", "n"},
				Fields: []FieldSpec{
					{Name: "stream", Kind: ValueInt, Required: true},
					{Name: "n", Kind: ValueInt, Required: true},
				},
			},
			fields: map[string]any{
				"stream": 7,
				"n":      3,
			},
			wantIndex: duplicateIndexDoubleInt,
		},
		{
			name: "declared-non-scalar-string-index",
			spec: TemplateSpec{
				Name:              "payload",
				DuplicatePolicy:   DuplicateUniqueKey,
				DuplicateKeyNames: []string{"items"},
				Fields: []FieldSpec{
					{Name: "items", Kind: ValueList, Required: true},
				},
			},
			fields: map[string]any{
				"items": []any{"alpha", "beta"},
			},
			wantIndex: duplicateIndexString,
		},
		{
			name: "declared-template-defaults-to-fixed-slots",
			spec: TemplateSpec{
				Name:              "open-event",
				DuplicatePolicy:   DuplicateUniqueKey,
				DuplicateKeyNames: []string{"id"},
				Fields: []FieldSpec{
					{Name: "id", Kind: ValueString, Required: true},
				},
			},
			fields: map[string]any{
				"id": "evt-1",
			},
			wantIndex: duplicateIndexSingleScalar,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			revision := mustCompile(t, tc.spec)
			template, ok := revision.Template(tc.spec.Name)
			if !ok {
				t.Fatalf("expected template %q", tc.spec.Name)
			}
			session := mustSession(t, revision, SessionID(tc.name))

			first, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, tc.fields))
			if err != nil {
				t.Fatalf("AssertTemplate: %v", err)
			}
			if first.DuplicateKey == "" {
				t.Fatal("expected public duplicate key")
			}
			internal := mustWorkingFactByID(t, session, first.Fact.ID())
			if internal == nil {
				t.Fatal("missing stored fact")
			}
			if got := internal.dupIndex.kind; got != tc.wantIndex {
				t.Fatalf("duplicate index kind = %v, want %v", got, tc.wantIndex)
			}

			duplicate, err := session.AssertTemplate(context.Background(), template.Key(), mustFields(t, tc.fields))
			if err != nil {
				t.Fatalf("duplicate AssertTemplate: %v", err)
			}
			if duplicate.Status != AssertExisting {
				t.Fatalf("duplicate status = %v, want %v", duplicate.Status, AssertExisting)
			}
			if duplicate.Fact.ID() != first.Fact.ID() {
				t.Fatalf("duplicate fact ID = %q, want %q", duplicate.Fact.ID(), first.Fact.ID())
			}
			if duplicate.DuplicateKey != first.DuplicateKey {
				t.Fatalf("duplicate key = %q, want %q", duplicate.DuplicateKey, first.DuplicateKey)
			}
			if got, ok := session.factIDForDuplicateKey(first.DuplicateKey); !ok || got != first.Fact.ID() {
				t.Fatalf("public duplicate key lookup = (%q, %t), want (%q, true)", got, ok, first.Fact.ID())
			}
		})
	}
}

func TestDuplicateIndexFloatNaNFallsBackToPublicStringSemantics(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "reading",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"score"},
		Fields: []FieldSpec{
			{Name: "score", Kind: ValueFloat, Required: true},
		},
	})
	template, ok := revision.Template("reading")
	if !ok {
		t.Fatal("expected reading template")
	}
	session := mustSession(t, revision, "nan-duplicate-session")
	fields := Fields{"score": newFloatValue(math.NaN())}

	first, err := session.AssertTemplate(context.Background(), template.Key(), fields)
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	internal := mustWorkingFactByID(t, session, first.Fact.ID())
	if internal == nil {
		t.Fatal("missing stored fact")
	}
	if got := internal.dupIndex.kind; got != duplicateIndexString {
		t.Fatalf("NaN duplicate index kind = %v, want %v", got, duplicateIndexString)
	}

	duplicate, err := session.AssertTemplate(context.Background(), template.Key(), fields)
	if err != nil {
		t.Fatalf("duplicate AssertTemplate: %v", err)
	}
	if duplicate.Status != AssertExisting {
		t.Fatalf("duplicate status = %v, want %v", duplicate.Status, AssertExisting)
	}
	if duplicate.Fact.ID() != first.Fact.ID() {
		t.Fatalf("duplicate fact ID = %q, want %q", duplicate.Fact.ID(), first.Fact.ID())
	}
	if duplicate.DuplicateKey != first.DuplicateKey {
		t.Fatalf("duplicate key = %q, want %q", duplicate.DuplicateKey, first.DuplicateKey)
	}
}

func TestDuplicateIndexTypedPathPreservesPublicDuplicateResultsAndEvents(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:              "route",
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"stream", "n"},
		Fields: []FieldSpec{
			{Name: "stream", Kind: ValueInt, Required: true},
			{Name: "n", Kind: ValueInt, Required: true},
			{Name: "lane", Kind: ValueString, Required: true},
		},
	})
	template, ok := revision.Template("route")
	if !ok {
		t.Fatal("expected route template")
	}
	var events []Event
	session, err := NewSession(revision,
		WithSessionID("typed-duplicate-public-session"),
		WithEventListener(EventFunc(func(_ context.Context, event Event) error {
			events = append(events, event.clone())
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	firstFields := mustFields(t, map[string]any{"stream": 7, "n": 3, "lane": "north"})
	first, err := session.AssertTemplate(context.Background(), template.Key(), firstFields)
	if err != nil {
		t.Fatalf("AssertTemplate: %v", err)
	}
	firstKey := makeDuplicateKeyForTemplate("route", template, first.Fact.Fields())
	if first.DuplicateKey != firstKey {
		t.Fatalf("assert duplicate key = %q, want %q", first.DuplicateKey, firstKey)
	}
	if first.Delta == nil || first.Delta.NewDuplicate != firstKey {
		t.Fatalf("assert delta duplicate key = %#v, want %q", first.Delta, firstKey)
	}
	if len(events) != 1 || events[0].Type != EventFactAsserted || events[0].Delta == nil || events[0].Delta.NewDuplicate != firstKey {
		t.Fatalf("assert event duplicate metadata = %#v", events)
	}

	duplicate, err := session.AssertTemplate(context.Background(), template.Key(), firstFields)
	if err != nil {
		t.Fatalf("duplicate AssertTemplate: %v", err)
	}
	if duplicate.Status != AssertExisting || duplicate.DuplicateKey != firstKey || duplicate.Delta != nil {
		t.Fatalf("duplicate assert result = %#v, want existing with key %q and nil delta", duplicate, firstKey)
	}

	modified, err := session.Modify(context.Background(), first.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"n": 4, "lane": "south"}),
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	modifiedKey := makeDuplicateKeyForTemplate("route", template, modified.Fact.Fields())
	if modified.Delta == nil || modified.Delta.OldDuplicate != firstKey || modified.Delta.NewDuplicate != modifiedKey {
		t.Fatalf("modify delta duplicate keys = %#v, want old=%q new=%q", modified.Delta, firstKey, modifiedKey)
	}
	if len(events) != 2 || events[1].Type != EventFactModified || events[1].Delta == nil ||
		events[1].Delta.OldDuplicate != firstKey || events[1].Delta.NewDuplicate != modifiedKey {
		t.Fatalf("modify event duplicate metadata = %#v", events)
	}

	retracted, err := session.Retract(context.Background(), first.Fact.ID())
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if retracted.Delta == nil || retracted.Delta.OldDuplicate != modifiedKey {
		t.Fatalf("retract delta duplicate key = %#v, want %q", retracted.Delta, modifiedKey)
	}
	if len(events) != 3 || events[2].Type != EventFactRetracted || events[2].Delta == nil ||
		events[2].Delta.OldDuplicate != modifiedKey {
		t.Fatalf("retract event duplicate metadata = %#v", events)
	}
}
