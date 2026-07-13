package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
)

// Miss Manners: the classic OPS5 seating benchmark. Guests must be seated in
// a line so neighbouring guests have opposite sex and share a hobby. The
// search is driven by a context state machine over salience tiers, which
// exercises agenda ordering, negations with outer joins, and RHS
// assert/modify propagation.

const mannersHobbyCount = 8

type mannersGuest struct {
	name    string
	sex     string
	hobbies []int
}

// mannersGuests mirrors the generator in MannersJessRunner.java: alternating
// sex and hobbies {i, i+1, i+2} mod mannersHobbyCount, so neighbouring
// guests in generation order always share a hobby and a valid seating
// exists for any count that is a multiple of mannersHobbyCount.
func mannersGuests(count int) []mannersGuest {
	guests := make([]mannersGuest, count)
	for i := range count {
		sex := "m"
		if i%2 == 1 {
			sex = "f"
		}
		guests[i] = mannersGuest{
			name:    fmt.Sprintf("guest-%03d", i),
			sex:     sex,
			hobbies: []int{i % mannersHobbyCount, (i + 1) % mannersHobbyCount, (i + 2) % mannersHobbyCount},
		}
	}
	return guests
}

func mustCompileMannersRuleset(t testing.TB) *Ruleset {
	return mustCompileMannersRulesetWithOptions(t, nil, false, false)
}

func mustCompileMannersRulesetWithProfile(t testing.TB, profile *branchPlanningProfile) *Ruleset {
	return mustCompileMannersRulesetWithOptions(t, profile, false, false)
}

func mustCompileMannersRulesetWithVolatileFindSeatingContext(t testing.TB) *Ruleset {
	return mustCompileMannersRulesetWithOptions(t, nil, false, true)
}

func mustCompileMannersRulesetWithOptions(t testing.TB, profile *branchPlanningProfile, legacyCountBeforeNegations, volatileFindSeatingContext bool) *Ruleset {
	t.Helper()
	workspace := newMannersWorkspace(t, volatileFindSeatingContext)
	revision := mustCompileWorkspaceWithBranchPlanningProfile(t, workspace, profile)
	if legacyCountBeforeNegations {
		reorderMannersFindSeatingToLegacyOrder(t, revision)
	}
	return revision
}

func newMannersWorkspace(t testing.TB, volatileFindSeatingContext bool) *Workspace {
	t.Helper()
	workspace := NewWorkspace()
	guest := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "guest",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "sex", Kind: ValueString, Required: true},
			{Name: "hobby", Kind: ValueInt, Required: true},
		},
	})
	lastSeat := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "lastseat",
		Fields: []FieldSpec{
			{Name: "seat", Kind: ValueInt, Required: true},
		},
	})
	seating := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "seating",
		Fields: []FieldSpec{
			{Name: "seat1", Kind: ValueInt, Required: true},
			{Name: "name1", Kind: ValueString, Required: true},
			{Name: "name2", Kind: ValueString, Required: true},
			{Name: "seat2", Kind: ValueInt, Required: true},
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "pid", Kind: ValueInt, Required: true},
			{Name: "pathdone", Kind: ValueBool, Required: true},
		},
	})
	contextTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "context",
		Fields: []FieldSpec{
			{Name: "state", Kind: ValueString, Required: true},
		},
	})
	path := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "path",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "seat", Kind: ValueInt, Required: true},
		},
	})
	chosen := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "chosen",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueInt, Required: true},
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "hobby", Kind: ValueInt, Required: true},
		},
	})
	countTemplate := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "count",
		Fields: []FieldSpec{
			{Name: "c", Kind: ValueInt, Required: true},
		},
	})

	bindingInt := func(ctx ActionContext, binding, field string) (int64, error) {
		value, ok := ctx.BindingScalarValue(binding, field)
		if !ok || value.Kind() != ValueInt {
			return 0, fmt.Errorf("missing int field %q on binding %q", field, binding)
		}
		return valueInt64(value), nil
	}
	bindingString := func(ctx ActionContext, binding, field string) (string, error) {
		value, ok := ctx.BindingScalarValue(binding, field)
		if !ok || value.Kind() != ValueString {
			return "", fmt.Errorf("missing string field %q on binding %q", field, binding)
		}
		return valueString(value), nil
	}
	modifyBinding := func(ctx ActionContext, binding string, patch FactPatch) error {
		fact, ok := ctx.Binding(binding)
		if !ok {
			return fmt.Errorf("missing binding %q", binding)
		}
		_, err := ctx.Modify(fact.ID(), patch)
		return err
	}

	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "manners-assign-first-seat",
		Fn: func(ctx ActionContext) error {
			name, err := bindingString(ctx, "g", "name")
			if err != nil {
				return err
			}
			c, err := bindingInt(ctx, "cnt", "c")
			if err != nil {
				return err
			}
			if err := ctx.AssertTemplateValues(seating.Key(),
				newIntValue(c), newStringValue(name), newStringValue(name),
				newBoolValue(true), newIntValue(0), newIntValue(1), newIntValue(1)); err != nil {
				return err
			}
			if err := ctx.AssertTemplateValues(path.Key(),
				newIntValue(c), newStringValue(name), newIntValue(1)); err != nil {
				return err
			}
			if err := modifyBinding(ctx, "cnt", FactPatch{Set: Fields{"c": newIntValue(c + 1)}}); err != nil {
				return err
			}
			return modifyBinding(ctx, "ctx", FactPatch{Set: Fields{"state": newStringValue("assign_seats")}})
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "manners-find-seating",
		Fn: func(ctx ActionContext) error {
			seat2, err := bindingInt(ctx, "seat", "seat2")
			if err != nil {
				return err
			}
			id, err := bindingInt(ctx, "seat", "id")
			if err != nil {
				return err
			}
			name1, err := bindingString(ctx, "g1", "name")
			if err != nil {
				return err
			}
			hobby, err := bindingInt(ctx, "g1", "hobby")
			if err != nil {
				return err
			}
			name2, err := bindingString(ctx, "g2", "name")
			if err != nil {
				return err
			}
			c, err := bindingInt(ctx, "cnt", "c")
			if err != nil {
				return err
			}
			if err := ctx.AssertTemplateValues(seating.Key(),
				newIntValue(c), newStringValue(name1), newStringValue(name2),
				newBoolValue(false), newIntValue(id), newIntValue(seat2), newIntValue(seat2+1)); err != nil {
				return err
			}
			if err := ctx.AssertTemplateValues(path.Key(),
				newIntValue(c), newStringValue(name2), newIntValue(seat2+1)); err != nil {
				return err
			}
			if err := ctx.AssertTemplateValues(chosen.Key(),
				newIntValue(hobby), newIntValue(id), newStringValue(name2)); err != nil {
				return err
			}
			if err := modifyBinding(ctx, "cnt", FactPatch{Set: Fields{"c": newIntValue(c + 1)}}); err != nil {
				return err
			}
			return modifyBinding(ctx, "ctx", FactPatch{Set: Fields{"state": newStringValue("make_path")}})
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "manners-make-path",
		Fn: func(ctx ActionContext) error {
			id, err := bindingInt(ctx, "seat", "id")
			if err != nil {
				return err
			}
			name, err := bindingString(ctx, "p", "name")
			if err != nil {
				return err
			}
			seatNumber, err := bindingInt(ctx, "p", "seat")
			if err != nil {
				return err
			}
			return ctx.AssertTemplateValues(path.Key(),
				newIntValue(id), newStringValue(name), newIntValue(seatNumber))
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "manners-path-done",
		Fn: func(ctx ActionContext) error {
			if err := modifyBinding(ctx, "seat", FactPatch{Set: Fields{"pathdone": newBoolValue(true)}}); err != nil {
				return err
			}
			return modifyBinding(ctx, "ctx", FactPatch{Set: Fields{"state": newStringValue("check_done")}})
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "manners-are-we-done",
		Fn: func(ctx ActionContext) error {
			return modifyBinding(ctx, "ctx", FactPatch{Set: Fields{"state": newStringValue("done")}})
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "manners-continue",
		Fn: func(ctx ActionContext) error {
			return modifyBinding(ctx, "ctx", FactPatch{Set: Fields{"state": newStringValue("assign_seats")}})
		},
	})

	mustAddRule(t, workspace, RuleSpec{
		Name:     "assign-first-seat",
		Salience: 40,
		Conditions: []RuleConditionSpec{
			{
				Binding: "ctx",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "state", Operator: FieldConstraintEqual, Value: "start"},
				},
				Target: TemplateKeyFact(contextTemplate.Key()),
			},
			{
				Binding: "g",
				Target:  TemplateKeyFact(guest.Key()),
			},
			{
				Binding: "cnt",
				Target:  TemplateKeyFact(countTemplate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "manners-assign-first-seat"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "find-seating",
		Salience: 30,
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding:  "ctx",
				Volatile: volatileFindSeatingContext,
				FieldConstraints: []FieldConstraintSpec{
					{Field: "state", Operator: FieldConstraintEqual, Value: "assign_seats"},
				},
				Target: TemplateKeyFact(contextTemplate.Key()),
			},
			Match{
				Binding: "seat",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "pathdone", Operator: FieldConstraintEqual, Value: true},
				},
				Target: TemplateKeyFact(seating.Key()),
			},
			Match{
				Binding: "g1",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "name", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "seat", Field: "name2"}},
				},
				Target: TemplateKeyFact(guest.Key()),
			},
			Match{
				Binding: "g2",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "sex", Operator: FieldConstraintNotEqual, Ref: FieldRef{Binding: "g1", Field: "sex"}},
					{Field: "hobby", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "g1", Field: "hobby"}},
				},
				Target: TemplateKeyFact(guest.Key()),
			},
			Match{
				Binding: "cnt",
				Target:  TemplateKeyFact(countTemplate.Key()),
			},
			Not{Condition: Match{
				Binding: "blockedpath",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "seat", Field: "id"}},
					{Field: "name", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "g2", Field: "name"}},
				},
				Target: TemplateKeyFact(path.Key()),
			}},
			Not{Condition: Match{
				Binding: "blockedchoice",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "seat", Field: "id"}},
					{Field: "name", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "g2", Field: "name"}},
					{Field: "hobby", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "g1", Field: "hobby"}},
				},
				Target: TemplateKeyFact(chosen.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "manners-find-seating"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "make-path",
		Salience: 20,
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{
				Binding: "ctx",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "state", Operator: FieldConstraintEqual, Value: "make_path"},
				},
				Target: TemplateKeyFact(contextTemplate.Key()),
			},
			Match{
				Binding: "seat",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "pathdone", Operator: FieldConstraintEqual, Value: false},
				},
				Target: TemplateKeyFact(seating.Key()),
			},
			Match{
				Binding: "p",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "seat", Field: "pid"}},
				},
				Target: TemplateKeyFact(path.Key()),
			},
			Not{Condition: Match{
				Binding: "existingpath",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "seat", Field: "id"}},
					{Field: "name", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "p", Field: "name"}},
				},
				Target: TemplateKeyFact(path.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "manners-make-path"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "path-done",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{
				Binding: "ctx",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "state", Operator: FieldConstraintEqual, Value: "make_path"},
				},
				Target: TemplateKeyFact(contextTemplate.Key()),
			},
			{
				Binding: "seat",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "pathdone", Operator: FieldConstraintEqual, Value: false},
				},
				Target: TemplateKeyFact(seating.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "manners-path-done"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "are-we-done",
		Salience: 5,
		Conditions: []RuleConditionSpec{
			{
				Binding: "ctx",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "state", Operator: FieldConstraintEqual, Value: "check_done"},
				},
				Target: TemplateKeyFact(contextTemplate.Key()),
			},
			{
				Binding: "ls",
				Target:  TemplateKeyFact(lastSeat.Key()),
			},
			{
				Binding: "seat",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "seat2", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "ls", Field: "seat"}},
				},
				Target: TemplateKeyFact(seating.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "manners-are-we-done"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "continue",
		Salience: 0,
		Conditions: []RuleConditionSpec{
			{
				Binding: "ctx",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "state", Operator: FieldConstraintEqual, Value: "check_done"},
				},
				Target: TemplateKeyFact(contextTemplate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "manners-continue"}},
	})

	return workspace
}

func markMannersFindSeatingContextVolatile(t testing.TB, workspace *Workspace) {
	t.Helper()
	for i := range workspace.rules {
		if workspace.rules[i].Name != "find-seating" {
			continue
		}
		tree, ok := cloneConditionSpec(workspace.rules[i].ConditionTree).(And)
		if !ok || len(tree.Conditions) == 0 {
			t.Fatalf("find-seating condition tree = %#v, want non-empty and", workspace.rules[i].ConditionTree)
		}
		contextMatch, ok := tree.Conditions[0].(Match)
		if !ok || contextMatch.Binding != "ctx" {
			t.Fatalf("find-seating first condition = %#v, want ctx match", tree.Conditions[0])
		}
		contextMatch.Volatile = true
		tree.Conditions[0] = contextMatch
		workspace.rules[i].ConditionTree = tree
		return
	}
	t.Fatal("find-seating rule not found")
}

func reorderMannersFindSeatingToLegacyOrder(t testing.TB, revision *Ruleset) {
	t.Helper()
	rule := revision.rules["find-seating"]
	if len(rule.conditionBranches) != 1 {
		t.Fatalf("find-seating condition branches = %d, want 1", len(rule.conditionBranches))
	}
	plansByBinding := make(map[string]compiledConditionPlan, len(rule.conditionBranches[0].plans))
	for _, plan := range rule.conditionBranches[0].plans {
		plansByBinding[plan.binding] = plan
	}
	bindings := []string{"ctx", "seat", "g1", "g2", "cnt", "blockedpath", "blockedchoice"}
	plans := make([]compiledConditionPlan, len(bindings))
	for i, binding := range bindings {
		plan, ok := plansByBinding[binding]
		if !ok {
			t.Fatalf("find-seating plan binding %q not found", binding)
		}
		plans[i] = plan
	}
	rule.conditionBranches[0].plans = plans
	revision.rules[rule.name] = rule
	revision.rulesByID[rule.id] = rule
	revision.rulesByRevisionID[rule.revisionID] = rule

	compiledRules := make([]compiledRule, 0, len(revision.ruleOrder))
	for _, name := range revision.ruleOrder {
		compiledRules = append(compiledRules, revision.rules[name])
	}
	compiledQueries := make([]compiledQuery, 0, len(revision.queryOrder))
	for _, name := range revision.queryOrder {
		compiledQueries = append(compiledQueries, revision.queries[name])
	}
	revision.graph = compileReteGraph(compiledRules, compiledQueries, revision.templatesByKey)
}

func mannersInitialFacts(guests []mannersGuest) []SessionInitialFact {
	initials := make([]SessionInitialFact, 0, len(guests)*3+3)
	for _, g := range guests {
		for _, hobby := range g.hobbies {
			initials = append(initials, SessionInitialFact{
				TemplateKey: TemplateKey("guest"),
				Fields: Fields{
					"name":  newStringValue(g.name),
					"sex":   newStringValue(g.sex),
					"hobby": newIntValue(int64(hobby)),
				},
			})
		}
	}
	initials = append(initials,
		SessionInitialFact{
			TemplateKey: TemplateKey("lastseat"),
			Fields:      Fields{"seat": newIntValue(int64(len(guests)))},
		},
		SessionInitialFact{
			TemplateKey: TemplateKey("count"),
			Fields:      Fields{"c": newIntValue(1)},
		},
		SessionInitialFact{
			TemplateKey: TemplateKey("context"),
			Fields:      Fields{"state": newStringValue("start")},
		},
	)
	return initials
}

func validateMannersSolution(t testing.TB, ctx context.Context, session *Session, guests []mannersGuest) {
	t.Helper()

	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	guestCount := len(guests)
	winningID := int64(-1)
	type pathRow struct {
		name string
		seat int64
	}
	var paths []pathRow
	for _, fact := range snapshot.Facts() {
		switch fact.Name() {
		case "seating":
			seat2, _ := fact.Field("seat2")
			if seat2.Kind() == ValueInt && valueInt64(seat2) == int64(guestCount) {
				id, _ := fact.Field("id")
				winningID = valueInt64(id)
			}
		}
	}
	if winningID < 0 {
		t.Fatalf("no complete seating found for %d guests", guestCount)
	}
	for _, fact := range snapshot.Facts() {
		if fact.Name() != "path" {
			continue
		}
		id, _ := fact.Field("id")
		if valueInt64(id) != winningID {
			continue
		}
		name, _ := fact.Field("name")
		seat, _ := fact.Field("seat")
		paths = append(paths, pathRow{name: valueString(name), seat: valueInt64(seat)})
	}
	if len(paths) != guestCount {
		t.Fatalf("winning seating %d has %d path facts, want %d", winningID, len(paths), guestCount)
	}
	byName := make(map[string]mannersGuest, guestCount)
	for _, g := range guests {
		byName[g.name] = g
	}
	arrangement := make([]mannersGuest, guestCount)
	seen := make(map[string]bool, guestCount)
	for _, row := range paths {
		if row.seat < 1 || row.seat > int64(guestCount) {
			t.Fatalf("path seat %d out of range", row.seat)
		}
		if seen[row.name] {
			t.Fatalf("guest %s seated twice", row.name)
		}
		seen[row.name] = true
		arrangement[row.seat-1] = byName[row.name]
	}
	for i := 0; i+1 < guestCount; i++ {
		left, right := arrangement[i], arrangement[i+1]
		if left.sex == right.sex {
			t.Fatalf("seats %d/%d share sex %s", i+1, i+2, left.sex)
		}
		if !mannersShareHobby(left, right) {
			t.Fatalf("seats %d/%d share no hobby", i+1, i+2)
		}
	}
}

func mannersShareHobby(left, right mannersGuest) bool {
	for _, lh := range left.hobbies {
		if slices.Contains(right.hobbies, lh) {
			return true
		}
	}
	return false
}

func TestMannersSolvesSeating(t *testing.T) {
	ctx := context.Background()
	for _, guestCount := range []int{8, 16} {
		guests := mannersGuests(guestCount)
		revision := mustCompileMannersRuleset(t)
		session, err := NewSession(revision, WithInitialFacts(mannersInitialFacts(guests)...))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		result, err := session.Run(ctx)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
		}
		validateMannersSolution(t, ctx, session, guests)
	}
}

func TestMannersComparableHarness(t *testing.T) {
	if os.Getenv("GESS_MANNERS_RUNNER") == "" {
		t.Skip("set GESS_MANNERS_RUNNER=1 to run the comparable manners harness")
	}

	iterations := mannersHarnessEnvInt(t, "GESS_MANNERS_ITERATIONS", 5)
	warmup := mannersHarnessEnvInt(t, "GESS_MANNERS_WARMUP", 1)
	guestCount := mannersHarnessEnvInt(t, "GESS_MANNERS_GUESTS", 32)
	if iterations <= 0 || warmup < 0 || guestCount <= 0 {
		t.Fatalf("invalid manners harness parameters: iterations=%d warmup=%d guests=%d", iterations, warmup, guestCount)
	}

	ctx := context.Background()
	guests := mannersGuests(guestCount)
	revision := mustCompileMannersRuleset(t)
	if os.Getenv("GESS_MANNERS_VOLATILE") != "" {
		revision = mustCompileMannersRulesetWithVolatileFindSeatingContext(t)
	}
	initials := mannersInitialFacts(guests)

	runOnce := func(label string) (RunResult, time.Duration) {
		session, err := NewSession(revision, WithInitialFacts(initials...))
		if err != nil {
			t.Fatalf("%s NewSession: %v", label, err)
		}
		start := time.Now()
		result, err := session.Run(ctx)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("%s Run: %v", label, err)
		}
		if result.Status != RunCompleted {
			t.Fatalf("%s run status = %v", label, result.Status)
		}
		validateMannersSolution(t, ctx, session, guests)
		return result, elapsed
	}

	for i := range warmup {
		runOnce(fmt.Sprintf("warmup-%d", i))
	}

	var elapsed time.Duration
	fired := 0
	for i := range iterations {
		result, iterationElapsed := runOnce(fmt.Sprintf("iteration-%d", i))
		elapsed += iterationElapsed
		fired = result.Fired
	}

	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	fmt.Printf("GESS_RUNNER|manners|session-run|guests=%d|fired=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f|retained-heap-bytes=%d\n",
		guestCount, fired, iterations, warmup, elapsed.Nanoseconds(),
		float64(elapsed.Nanoseconds())/float64(iterations), stats.HeapAlloc)
}

func mannersHarnessEnvInt(t testing.TB, name string, fallback int) int {
	t.Helper()
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
