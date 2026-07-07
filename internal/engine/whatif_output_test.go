package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// whatIfEmitSession builds a base session whose one rule emits when a signal
// fact is asserted, with the base's own output routed to base.
func whatIfEmitSession(t *testing.T, base *bytes.Buffer) (*Session, TemplateKey) {
	t.Helper()
	ctx := context.Background()
	ws := NewWorkspace()
	key := mustAddTemplate(t, ws, TemplateSpec{
		Name:   "signal",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	}).Key()
	if err := ws.AddAction(ActionSpec{
		Name: "announce",
		Effect: &ActionEffectSpec{
			Kind: ActionEffectEmit,
			Values: []ExpressionSpec{
				ConstExpr{Value: "fired:"},
				BindingFieldExpr{Binding: "s", Field: "id"},
			},
		},
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}
	if err := ws.AddRule(RuleSpec{
		Name:       "on-signal",
		Conditions: []RuleConditionSpec{{Binding: "s", Target: TemplateKeyFact(key)}},
		Actions:    []RuleActionSpec{{Name: "announce"}},
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	revision, err := ws.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithOutputWriter(base))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, key
}

// A what-if fork must not inherit the base session's output writer: a
// hypothetical emit must not pollute the real output sink.
func TestWhatIfDoesNotLeakEmitToBaseWriter(t *testing.T) {
	ctx := context.Background()
	var base bytes.Buffer
	session, key := whatIfEmitSession(t, &base)

	report, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, err := fork.Assert(ctx, key, mustFields(t, map[string]any{"id": "x"}))
		return err
	})
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if len(report.Firings) != 1 {
		t.Fatalf("Firings = %d, want 1 (the emit rule should fire in the fork)", len(report.Firings))
	}
	if base.Len() != 0 {
		t.Fatalf("base output = %q, want empty (the hypothetical emit must not leak)", base.String())
	}
}

// WithWhatIfOutputWriter captures the fork's emit output for inspection while
// still leaving the base writer untouched.
func TestWhatIfCapturesEmitOutput(t *testing.T) {
	ctx := context.Background()
	var base, capture bytes.Buffer
	session, key := whatIfEmitSession(t, &base)

	if _, err := session.WhatIf(ctx, func(ctx context.Context, fork *Session) error {
		_, err := fork.Assert(ctx, key, mustFields(t, map[string]any{"id": "x"}))
		return err
	}, WithWhatIfOutputWriter(&capture)); err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if got := capture.String(); !strings.Contains(got, "fired:x") {
		t.Fatalf("captured output = %q, want it to contain %q", got, "fired:x")
	}
	if base.Len() != 0 {
		t.Fatalf("base output = %q, want empty", base.String())
	}
}
