package rules

import "testing"

func TestFactSnapshotStringUsesRawFieldKeys(t *testing.T) {
	fact := FactSnapshot{
		IDValue:          NewFactID(7, 42),
		NameValue:        "person",
		TemplateKeyValue: TemplateKey("people/person"),
		VersionValue:     3,
		RecencyValue:     11,
		GenerationValue:  7,
		FieldValues: MustFields(
			"name", "Ada",
			"age", 42,
		),
		FieldPresenceValues: map[string]FieldPresence{
			"name": FieldPresenceDefault,
			"age":  FieldPresenceExplicit,
		},
	}

	want := `Fact{id:fact:g7:42, name:person, template:people/person, version:3, recency:11, generation:7, fields:{age=number:42, name=string:"Ada"}, presence:{age=explicit, name=default}}`
	if got := fact.String(); got != want {
		t.Fatalf("FactSnapshot.String() = %q, want %q", got, want)
	}
}
