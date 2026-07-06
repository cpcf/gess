package engine

import (
	"context"
	"testing"
)

func TestMutationEventAttributesAssertsToActions(t *testing.T) {
	workspace := NewWorkspace()
	srcKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "src",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	aKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "a",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	bKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "b",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	mustAddAction(t, workspace, ActionSpec{
		Name: "make-a",
		Fn: func(ctx ActionContext) error {
			id, _ := ctx.BindingScalarValue("s", "id")
			_, err := ctx.Assert(aKey, Fields{"id": id})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "make-b",
		Fn: func(ctx ActionContext) error {
			id, _ := ctx.BindingScalarValue("s", "id")
			_, err := ctx.Assert(bKey, Fields{"id": id})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "fan-out",
		Conditions: []RuleConditionSpec{{Binding: "s", Target: TemplateKeyFact(srcKey)}},
		Actions:    []RuleActionSpec{{Name: "make-a"}, {Name: "make-b"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("attr-asserts"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Assert(context.Background(), srcKey, mustFields(t, map[string]any{"id": "s-1"})); err != nil {
		t.Fatalf("Assert(src): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	type attribution struct {
		name  string
		index int
	}
	got := map[TemplateKey]attribution{}
	for _, event := range collector.Events() {
		if event.Type != EventFactAsserted || event.Delta == nil || event.Delta.After == nil {
			continue
		}
		got[event.Delta.After.TemplateKey()] = attribution{event.ActionName, event.ActionIndex}
	}

	if a := got[srcKey]; a.name != "" || a.index != 0 {
		t.Fatalf("external src assert attribution = %+v, want empty", a)
	}
	if a := got[aKey]; a.name != "make-a" || a.index != 0 {
		t.Fatalf("fact a attribution = %+v, want make-a/0", a)
	}
	if a := got[bKey]; a.name != "make-b" || a.index != 1 {
		t.Fatalf("fact b attribution = %+v, want make-b/1", a)
	}
}

func TestMutationEventAttributesModifyAndRetract(t *testing.T) {
	workspace := NewWorkspace()
	taskKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "task",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	}).Key()
	tempKey := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "temp",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	mustAddAction(t, workspace, ActionSpec{
		Name: "close",
		Fn: func(ctx ActionContext) error {
			task, _ := ctx.Binding("t")
			_, err := ctx.Modify(task.ID(), FactPatch{Set: Fields{"status": newStringValue("closed")}})
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "closer",
		Conditions: []RuleConditionSpec{{
			Binding: "t",
			Target:  TemplateKeyFact(taskKey),
			FieldConstraints: []FieldConstraintSpec{
				{Field: "status", Operator: FieldConstraintEqual, Value: "open"},
			},
		}},
		Actions: []RuleActionSpec{{Name: "close"}},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "purge",
		Fn: func(ctx ActionContext) error {
			temp, _ := ctx.Binding("x")
			_, err := ctx.Retract(temp.ID())
			return err
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:       "cleaner",
		Conditions: []RuleConditionSpec{{Binding: "x", Target: TemplateKeyFact(tempKey)}},
		Actions:    []RuleActionSpec{{Name: "purge"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	collector := &testEventCollector{}
	session, err := NewSession(revision, WithSessionID("attr-modret"), WithEventListener(collector))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Assert(context.Background(), taskKey, mustFields(t, map[string]any{"id": "task-1", "status": "open"})); err != nil {
		t.Fatalf("Assert(task): %v", err)
	}
	if _, err := session.Assert(context.Background(), tempKey, mustFields(t, map[string]any{"id": "temp-1"})); err != nil {
		t.Fatalf("Assert(temp): %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var modify, retract *Event
	for _, event := range collector.Events() {
		switch event.Type {
		case EventFactModified:
			modify = &event
		case EventFactRetracted:
			retract = &event
		}
	}
	if modify == nil || modify.ActionName != "close" || modify.ActionIndex != 0 {
		t.Fatalf("modify attribution = %+v, want close/0", modify)
	}
	if retract == nil || retract.ActionName != "purge" || retract.ActionIndex != 0 {
		t.Fatalf("retract attribution = %+v, want purge/0", retract)
	}
}

// BenchmarkSessionRuleActionAttribution exercises the firing loop's per-action
// identity threading on the default (no-listener) path, guarding that the two
// mutationOrigin field copies do not regress fire throughput.
func BenchmarkSessionRuleActionAttribution(b *testing.B) {
	workspace := NewWorkspace()
	srcKey := mustAddTemplate(b, workspace, TemplateSpec{
		Name:   "src",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	aKey := mustAddTemplate(b, workspace, TemplateSpec{
		Name:   "a",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	bKey := mustAddTemplate(b, workspace, TemplateSpec{
		Name:   "b",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	mustAddAction(b, workspace, ActionSpec{
		Name: "make-a",
		Fn: func(ctx ActionContext) error {
			id, _ := ctx.BindingScalarValue("s", "id")
			_, err := ctx.Assert(aKey, Fields{"id": id})
			return err
		},
	})
	mustAddAction(b, workspace, ActionSpec{
		Name: "make-b",
		Fn: func(ctx ActionContext) error {
			id, _ := ctx.BindingScalarValue("s", "id")
			_, err := ctx.Assert(bKey, Fields{"id": id})
			return err
		},
	})
	mustAddRule(b, workspace, RuleSpec{
		Name:       "fan-out",
		Conditions: []RuleConditionSpec{{Binding: "s", Target: TemplateKeyFact(srcKey)}},
		Actions:    []RuleActionSpec{{Name: "make-a"}, {Name: "make-b"}},
	})
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithSessionID("attr-bench"))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	ctx := context.Background()
	fields := mustFields(b, map[string]any{"id": "s-1"})

	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.Assert(ctx, srcKey, fields); err != nil {
			b.Fatalf("Assert: %v", err)
		}
		if _, err := session.Run(ctx); err != nil {
			b.Fatalf("Run: %v", err)
		}
		if _, err := session.Reset(ctx); err != nil {
			b.Fatalf("Reset: %v", err)
		}
	}
}
