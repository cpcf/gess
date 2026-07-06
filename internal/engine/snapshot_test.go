package engine

import (
	"context"
	"strings"
	"testing"
)

func TestSnapshotRemainsUnchangedAfterAssert(t *testing.T) {
	session := mustSession(t, mustCompile(t), "snapshot-assert-session")
	baselineInserted, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("baseline assert: %v", err)
	}

	baseline := mustSnapshot(t, context.Background(), session)

	_, err = session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Grace",
	}))
	if err != nil {
		t.Fatalf("follow-up assert: %v", err)
	}

	if got, want := baseline.Len(), 1; got != want {
		t.Fatalf("snapshot length changed after assert: got %d, want %d", got, want)
	}
	original, ok := baseline.Fact(baselineInserted.Fact.ID())
	if !ok {
		t.Fatalf("baseline no longer contains original fact %q", baselineInserted.Fact.ID())
	}
	if original.Version() != baselineInserted.Fact.Version() {
		t.Fatalf("snapshot preserved fact version = %d, want %d", original.Version(), baselineInserted.Fact.Version())
	}
	if got := baseline.Facts()[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("snapshot fact mutated after assert: %v", got)
	}
}

func TestSnapshotRemainsUnchangedAfterModify(t *testing.T) {
	session := mustSession(t, mustCompile(t), "snapshot-modify-session")
	baselineInserted, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name":  "Ada",
		"count": 1,
	}))
	if err != nil {
		t.Fatalf("insert baseline: %v", err)
	}

	baseline := mustSnapshot(t, context.Background(), session)

	_, err = session.Modify(context.Background(), baselineInserted.Fact.ID(), FactPatch{
		Set: mustFields(t, map[string]any{
			"count": 2,
		}),
	})
	if err != nil {
		t.Fatalf("modify: %v", err)
	}

	if got := baseline.Len(); got != 1 {
		t.Fatalf("snapshot length changed after modify: got %d, want %d", got, 1)
	}
	original, ok := baseline.Fact(baselineInserted.Fact.ID())
	if !ok {
		t.Fatalf("baseline no longer contains original fact id %q", baselineInserted.Fact.ID())
	}
	if original.Version() != baselineInserted.Fact.Version() {
		t.Fatalf("snapshot fact version changed after modify: got %d, want %d", original.Version(), baselineInserted.Fact.Version())
	}
	if got := original.Fields()["count"]; !got.Equal(mustValue(t, 1)) {
		t.Fatalf("snapshot fact value changed after modify: %v", got)
	}
}

func TestSnapshotRemainsUnchangedAfterRetract(t *testing.T) {
	session := mustSession(t, mustCompile(t), "snapshot-retract-session")
	first, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}
	second, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Grace",
	}))
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}

	baseline := mustSnapshot(t, context.Background(), session)

	_, err = session.Retract(context.Background(), first.Fact.ID())
	if err != nil {
		t.Fatalf("retract: %v", err)
	}

	if got := baseline.Len(); got != 2 {
		t.Fatalf("snapshot length changed after retract: got %d, want %d", got, 2)
	}
	if _, ok := baseline.Fact(first.Fact.ID()); !ok {
		t.Fatalf("baseline lost retracted fact %q", first.Fact.ID())
	}
	if _, ok := baseline.Fact(second.Fact.ID()); !ok {
		t.Fatalf("baseline lost surviving fact %q", second.Fact.ID())
	}
}

func TestSnapshotRemainsUnchangedAfterReset(t *testing.T) {
	session := mustSession(t, mustCompile(t), "snapshot-reset-session")
	baselineInserted, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("baseline assert: %v", err)
	}

	baseline := mustSnapshot(t, context.Background(), session)

	_, err = session.Reset(context.Background())
	if err != nil {
		t.Fatalf("reset: %v", err)
	}

	if got := baseline.Generation(); got != 1 {
		t.Fatalf("baseline generation changed after reset: got %d, want %d", got, 1)
	}
	if got := baseline.Len(); got != 1 {
		t.Fatalf("baseline length changed after reset: got %d, want %d", got, 1)
	}
	if original, ok := baseline.Fact(baselineInserted.Fact.ID()); !ok || original.Name() != "person" {
		t.Fatalf("baseline fact changed after reset: (%v, %v)", original.ID(), ok)
	}
}

func TestSnapshotReconstructsPublicFactsFromCompactSlots(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "device",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active"},
		},
	})
	template, ok := revision.Template("device")
	if !ok {
		t.Fatal("missing device template")
	}
	session, err := NewSession(
		revision,
		WithSessionID("snapshot-compact-materialization-session"),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: template.Key(),
			Fields: mustFields(t, map[string]any{
				"id": "api",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	snapshot := session.snapshotLocked()
	if got, want := snapshot.Len(), 1; got != want {
		t.Fatalf("snapshot length = %d, want %d", got, want)
	}
	stored := snapshot.facts[0]
	if len(stored.fieldSlots) != 0 {
		t.Fatalf("snapshot materialized broad field slots = %d, want zero", len(stored.fieldSlots))
	}
	if got, want := len(stored.compactSlots), len(template.fields); got != want {
		t.Fatalf("snapshot compact slots = %d, want %d", got, want)
	}
	if got, ok := stored.Field("id"); !ok || !got.Equal(mustValue(t, "api")) {
		t.Fatalf("id field = (%v, %v), want api", got, ok)
	}
	if got, ok := stored.Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("status field = (%v, %v), want active", got, ok)
	}
	if got, ok := stored.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("status presence = (%v, %v), want default", got, ok)
	}
	if got := stored.Fields()["id"]; !got.Equal(mustValue(t, "api")) {
		t.Fatalf("materialized fields id = %v, want api", got)
	}

	if _, err := session.Modify(context.Background(), stored.ID(), FactPatch{
		Set: mustFields(t, map[string]any{"status": "inactive"}),
	}); err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if got, ok := stored.Field("status"); !ok || !got.Equal(mustValue(t, "active")) {
		t.Fatalf("compact snapshot changed after modify = (%v, %v), want active", got, ok)
	}
}

func TestResetResultBeforeRemainsDefensiveAfterLaterReset(t *testing.T) {
	session, err := NewSession(
		mustCompile(t, TemplateSpec{
			Name:   "settings",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}),
		WithResetBeforeSnapshot(true),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: "settings",
			Fields: mustFields(t, map[string]any{
				"name": "Ada",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	result, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("first reset: %v", err)
	}
	before := result.Before
	if got, want := before.Generation(), Generation(1); got != want {
		t.Fatalf("before generation = %d, want %d", got, want)
	}
	if got, want := before.Len(), 1; got != want {
		t.Fatalf("before length = %d, want %d", got, want)
	}

	fact := before.Facts()[0]
	fields := fact.Fields()
	fields["name"] = mustValue(t, "mutated")
	if got, ok := before.Fact(fact.ID()); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("before snapshot changed through returned fields: (%v, %v)", got, ok)
	}

	session.initials = nil
	if _, err := session.Reset(context.Background()); err != nil {
		t.Fatalf("second reset: %v", err)
	}

	if got, want := before.Generation(), Generation(1); got != want {
		t.Fatalf("before generation after later reset = %d, want %d", got, want)
	}
	if got, want := before.Len(), 1; got != want {
		t.Fatalf("before length after later reset = %d, want %d", got, want)
	}
	if got, ok := before.Fact(fact.ID()); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("before snapshot changed after later reset: (%v, %v)", got, ok)
	}
}

func TestResetCanSkipBeforeSnapshot(t *testing.T) {
	session, err := NewSession(
		mustCompile(t, TemplateSpec{
			Name:   "settings",
			Fields: []FieldSpec{{Name: "name", Kind: ValueString, Required: true}},
		}),
		WithResetBeforeSnapshot(false),
		WithInitialFacts(SessionInitialFact{
			TemplateKey: "settings",
			Fields: mustFields(t, map[string]any{
				"name": "Ada",
			}),
		}),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	result, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if result.Status != ResetApplied {
		t.Fatalf("reset status = %v, want %v", result.Status, ResetApplied)
	}
	if got := result.Before.Len(); got != 0 {
		t.Fatalf("before snapshot length = %d, want empty snapshot", got)
	}
	if result.Before.Generation() != 0 {
		t.Fatalf("before snapshot generation = %d, want zero snapshot", result.Before.Generation())
	}
}

func TestSnapshotTemplateFilteringAndPresenceCopies(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "person",
		Fields: []FieldSpec{{Name: "name", Kind: ValueString}},
	}, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString}},
	})
	session := mustSession(t, revision, "snapshot-filter-session")

	personTemplate, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected person template")
	}
	eventTemplate, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected event template")
	}

	person, err := session.Assert(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"name": "Ada",
	}))
	if err != nil {
		t.Fatalf("insert person: %v", err)
	}
	if _, err = session.Assert(context.Background(), personTemplate.Key(), mustFields(t, map[string]any{
		"name": "Grace",
	})); err != nil {
		t.Fatalf("insert second person: %v", err)
	}
	if _, err = session.assertByName(context.Background(), "person", mustFields(t, map[string]any{
		"name": "Untyped",
	})); err != nil {
		t.Fatalf("insert dynamic fact: %v", err)
	}
	if _, err = session.Assert(context.Background(), eventTemplate.Key(), mustFields(t, map[string]any{
		"id": "evt-1",
	})); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	persons := snapshot.FactsByTemplateKey(personTemplate.Key())
	if len(persons) != 2 {
		t.Fatalf("template filtered person facts = %d, want 2", len(persons))
	}
	if persons[0].ID() != person.Fact.ID() {
		t.Fatalf("template fact order changed: first=%q want %q", persons[0].ID(), person.Fact.ID())
	}

	events := snapshot.FactsByTemplateKey(eventTemplate.Key())
	if len(events) != 1 || events[0].TemplateKey() != eventTemplate.Key() {
		t.Fatalf("template filter for event = %#v", events)
	}

	if got := snapshot.FactsByTemplateKey("unknown"); len(got) != 0 {
		t.Fatalf("unknown template key should return empty results, got %d", len(got))
	}

	fields := persons[0].Fields()
	fields["name"] = mustValue(t, "MUT")
	if got := snapshot.FactsByTemplateKey(personTemplate.Key())[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("snapshot person fields changed through copied result map")
	}

	presence := persons[0].FieldPresenceMap()
	presence["name"] = FieldPresenceDefault
	if got, ok := snapshot.FactsByTemplateKey(personTemplate.Key())[0].FieldPresence("name"); !ok || got != FieldPresenceExplicit {
		t.Fatalf("snapshot field presence changed through copied presence map: %v, %v", got, ok)
	}
}

func TestSnapshotRecencyAndGenerationMetadata(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name:   "event",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString}},
	})
	session := mustSession(t, revision, "snapshot-metadata-session")
	template, ok := revision.Template("event")
	if !ok {
		t.Fatal("expected event template")
	}

	first, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{"id": "evt-1"}))
	if err != nil {
		t.Fatalf("insert first: %v", err)
	}
	second, err := session.assertByName(context.Background(), "person", mustFields(t, map[string]any{"name": "Ada"}))
	if err != nil {
		t.Fatalf("insert second: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	if got, want := snapshot.Len(), 2; got != want {
		t.Fatalf("snapshot length = %d, want %d", got, want)
	}
	if got := snapshot.Generation(); got != 1 {
		t.Fatalf("snapshot generation = %d, want %d", got, 1)
	}
	firstSnapshot, ok := snapshot.Fact(first.Fact.ID())
	if !ok {
		t.Fatalf("snapshot missing first fact %q", first.Fact.ID())
	}
	if got, want := firstSnapshot.Recency(), Recency(1); got != want {
		t.Fatalf("first fact recency = %d, want %d", got, want)
	}
	if got, want := firstSnapshot.Generation(), Generation(1); got != want {
		t.Fatalf("first fact generation = %d, want %d", got, want)
	}
	secondSnapshot, ok := snapshot.Fact(second.Fact.ID())
	if !ok {
		t.Fatalf("snapshot missing second fact %q", second.Fact.ID())
	}
	if got, want := secondSnapshot.Recency(), Recency(2); got != want {
		t.Fatalf("second fact recency = %d, want %d", got, want)
	}

	before, err := session.Reset(context.Background())
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if before.Generation != 2 {
		t.Fatalf("reset generation = %d, want %d", before.Generation, 2)
	}
	snapshotAfterReset := mustSnapshot(t, context.Background(), session)
	if snapshotAfterReset.Generation() != 2 {
		t.Fatalf("post-reset snapshot generation = %d, want 2", snapshotAfterReset.Generation())
	}
	if _, ok := snapshotAfterReset.Fact(first.Fact.ID()); ok {
		t.Fatalf("post-reset snapshot should not contain pre-reset fact id %q", first.Fact.ID())
	}
	for _, fact := range snapshotAfterReset.Facts() {
		if fact.Generation() != 2 {
			t.Fatalf("post-reset fact generation = %d, want 2", fact.Generation())
		}
	}
}

func TestSnapshotAccessorsReturnDefensiveCopies(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "profile", Kind: ValueMap},
		},
	})
	session := mustSession(t, revision, "snapshot-defensive-accessors-session")
	template, ok := revision.Template("person")
	if !ok {
		t.Fatal("expected person template")
	}

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{
		"name":    "Ada",
		"profile": map[string]any{"likes": "jazz"},
	}))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	fact, ok := snapshot.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatalf("snapshot missing inserted fact %q", inserted.Fact.ID())
	}
	facts := snapshot.Facts()
	if len(facts) != 1 {
		t.Fatalf("snapshot facts length = %d, want 1", len(facts))
	}
	byName := snapshot.FactsByName("person")
	if len(byName) != 1 {
		t.Fatalf("snapshot facts by name length = %d, want 1", len(byName))
	}
	byTemplate := snapshot.FactsByTemplateKey(template.Key())
	if len(byTemplate) != 1 {
		t.Fatalf("snapshot facts by template length = %d, want 1", len(byTemplate))
	}

	factFields := fact.Fields()
	factFields["name"] = mustValue(t, "MUT")
	factProfile, ok := fact.Field("profile")
	if !ok {
		t.Fatal("fact missing profile field")
	}
	factProfileMap := factProfile.data.(map[string]Value)
	factProfileMap["likes"] = mustValue(t, "rock")
	factPresence := fact.FieldPresenceMap()
	factPresence["name"] = FieldPresenceDefault

	facts[0].Fields()["name"] = mustValue(t, "MUT")
	byName[0].FieldPresenceMap()["name"] = FieldPresenceDefault
	byTemplate[0].Fields()["profile"] = NullValue()

	if got, ok := snapshot.Fact(inserted.Fact.ID()); !ok || !got.Fields()["name"].Equal(mustValue(t, "Ada")) {
		t.Fatalf("snapshot fact changed through copied result")
	}
	freshFact, ok := snapshot.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatalf("snapshot fact disappeared during verification")
	}
	if got := snapshot.Facts()[0].Fields()["name"]; !got.Equal(mustValue(t, "Ada")) {
		t.Fatalf("snapshot facts changed through copied result map")
	}
	if got, ok := snapshot.FactsByName("person")[0].FieldPresence("name"); !ok || got != FieldPresenceExplicit {
		t.Fatalf("snapshot facts by name changed through copied presence map: %v, %v", got, ok)
	}
	if got := snapshot.FactsByTemplateKey(template.Key())[0].Fields()["profile"]; !got.Equal(mustValue(t, map[string]any{"likes": "jazz"})) {
		t.Fatalf("snapshot facts by template changed through copied result map: %v", got)
	}
	if got, ok := freshFact.Field("profile"); !ok || !got.Equal(mustValue(t, map[string]any{"likes": "jazz"})) {
		t.Fatalf("snapshot fact field changed through copied value: (%v, %v)", got, ok)
	}
	if got, ok := freshFact.FieldPresence("name"); !ok || got != FieldPresenceExplicit {
		t.Fatalf("snapshot fact presence changed through copied presence map: %v, %v", got, ok)
	}
}

func TestSnapshotSlotBackedAccessorsReturnDefensiveCopies(t *testing.T) {
	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "profile", Kind: ValueMap},
			{Name: "status", Kind: ValueString, Default: "active"},
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "match-person",
		Conditions: []RuleConditionSpec{
			{Binding: "person", Target: TemplateKeyFact(template.Key())},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session := mustSession(t, revision, "slot-snapshot-defensive-session")

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{
		"id":      "p-1",
		"profile": map[string]any{"likes": "jazz"},
	}))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	internal := mustWorkingFactByID(t, session, inserted.Fact.ID())
	if internal.fieldsMap() != nil || internal.fieldPresenceMap() != nil || len(internal.fieldSlotSlice()) == 0 {
		t.Fatalf("slot-backed storage = fields:%v presence:%v slots:%d", internal.fieldsMap(), internal.fieldPresenceMap(), len(internal.fieldSlotSlice()))
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	fact, ok := snapshot.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatalf("snapshot missing inserted fact %q", inserted.Fact.ID())
	}

	rendered := snapshot.String()
	if rendered != snapshot.String() {
		t.Fatalf("slot-backed snapshot rendering changed between reads: %q", rendered)
	}

	fields := fact.Fields()
	fields["id"] = mustValue(t, "MUT")
	profile, ok := fact.Field("profile")
	if !ok {
		t.Fatal("fact missing profile field")
	}
	profile.data.(map[string]Value)["likes"] = mustValue(t, "rock")
	presence := fact.FieldPresenceMap()
	presence["status"] = FieldPresenceExplicit
	snapshot.FactsByTemplateKey(template.Key())[0].Fields()["status"] = mustValue(t, "MUT")

	fresh, ok := snapshot.Fact(inserted.Fact.ID())
	if !ok {
		t.Fatalf("snapshot missing inserted fact after mutations")
	}
	if got, ok := fresh.Field("id"); !ok || !got.Equal(mustValue(t, "p-1")) {
		t.Fatalf("slot-backed id changed through returned fields: (%v, %v)", got, ok)
	}
	if got, ok := fresh.Field("profile"); !ok || !got.Equal(mustValue(t, map[string]any{"likes": "jazz"})) {
		t.Fatalf("slot-backed profile changed through returned value: (%v, %v)", got, ok)
	}
	if got, ok := fresh.FieldPresence("status"); !ok || got != FieldPresenceDefault {
		t.Fatalf("slot-backed presence changed through returned map: (%v, %v)", got, ok)
	}
	if got := snapshot.FactsByTemplateKey(template.Key())[0].Fields()["status"]; !got.Equal(mustValue(t, "active")) {
		t.Fatalf("slot-backed facts-by-template result changed snapshot: %v", got)
	}
}

func TestSnapshotRenderingIsDeterministic(t *testing.T) {
	revision := mustCompile(t, TemplateSpec{
		Name: "payload",
		Fields: []FieldSpec{
			{Name: "zeta", Kind: ValueInt},
			{Name: "alpha", Kind: ValueString},
			{Name: "nested", Kind: ValueMap},
		},
	})
	session := mustSession(t, revision, "snapshot-render-session")
	template, ok := revision.Template("payload")
	if !ok {
		t.Fatal("expected payload template")
	}

	inserted, err := session.Assert(context.Background(), template.Key(), mustFields(t, map[string]any{
		"zeta":   2,
		"alpha":  "done",
		"nested": map[string]any{"c": 3, "b": 2, "a": 1},
	}))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	snapshot := mustSnapshot(t, context.Background(), session)
	rendered := snapshot.String()
	reRendered := snapshot.String()
	if rendered != reRendered {
		t.Fatalf("snapshot rendering changed between reads: %q != %q", rendered, reRendered)
	}

	if !strings.Contains(rendered, inserted.Fact.ID().String()) {
		t.Fatalf("snapshot rendering missing fact id %q", inserted.Fact.ID())
	}
	expectedFields := "alpha=string:\"done\", nested=map{a=number:1,b=number:2,c=number:3}, zeta=number:2"
	if got := strings.Index(snapshot.Facts()[0].String(), expectedFields); got < 0 {
		t.Fatalf("snapshot field order is unstable: %q", snapshot.Facts()[0].String())
	}
}
