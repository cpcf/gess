package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cpcf/gess"
	"github.com/cpcf/gess/examples/internal/exampleutil"
)

const (
	intakeModule   = gess.ModuleName("intake")
	responseModule = gess.ModuleName("response")
	eventTemplate  = "event"
	readyTemplate  = "ready-event"
	eventKey       = gess.TemplateKey("intake-event")
	readyKey       = gess.TemplateKey("response-ready-event")
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	ctx := context.Background()
	var handled []string
	ruleset, err := buildRuleset(ctx, &handled)
	if err != nil {
		return err
	}
	session, err := gess.NewSession(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	if _, err := session.AssertTemplate(ctx, eventKey, exampleutil.Fields("id", "E-100", "kind", "incident")); err != nil {
		return err
	}
	if err := session.SetFocus(ctx, intakeModule); err != nil {
		return err
	}
	fmt.Fprintf(out, "focus before run: %s\n", formatStack(session.FocusStack()))
	result, err := session.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "fired: %d\n", result.Fired)
	fmt.Fprintf(out, "handled: %s\n", strings.Join(handled, ","))
	fmt.Fprintf(out, "focus after run: %s\n", formatStack(session.FocusStack()))
	return nil
}

func buildRuleset(ctx context.Context, handled *[]string) (*gess.Ruleset, error) {
	autoFocus := false
	workspace := gess.NewWorkspace()
	for _, module := range []gess.ModuleSpec{
		{Name: intakeModule, AutoFocus: &autoFocus},
		{Name: responseModule, AutoFocus: &autoFocus},
	} {
		if err := workspace.AddModule(module); err != nil {
			return nil, err
		}
	}
	for _, spec := range []gess.TemplateSpec{
		{Module: intakeModule, Name: eventTemplate, Key: eventKey, Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "kind", Kind: gess.ValueString, Required: true},
		}},
		{Module: responseModule, Name: readyTemplate, Key: readyKey, DuplicatePolicy: gess.DuplicateUniqueKey, DuplicateKeyNames: []string{"id"}, Fields: []gess.FieldSpec{
			{Name: "id", Kind: gess.ValueString, Required: true},
			{Name: "kind", Kind: gess.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "promote-event",
		Fn: func(ctx gess.ActionContext) error {
			id, _ := ctx.BindingScalarValue("event", "id")
			kind, _ := ctx.BindingScalarValue("event", "kind")
			if _, err := ctx.AssertTemplate(readyKey, gess.Fields{"id": id, "kind": kind}); err != nil {
				return err
			}
			return ctx.PushFocus(responseModule)
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(gess.ActionSpec{
		Name: "handle-event",
		Fn: func(ctx gess.ActionContext) error {
			id, _ := ctx.BindingScalarValue("ready", "id")
			text, _ := id.AsString()
			*handled = append(*handled, text)
			_, err := ctx.PopFocus()
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name:   "validate-incident",
		Module: intakeModule,
		ConditionTree: gess.Match{
			Binding: "event",
			FieldConstraints: []gess.FieldConstraintSpec{
				{Field: "kind", Operator: gess.FieldConstraintEqual, Value: "incident"},
			},
			Target: gess.TemplateFactIn(intakeModule, eventTemplate),
		},
		Actions: []gess.RuleActionSpec{{Name: "promote-event"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(gess.RuleSpec{
		Name:   "handle-ready-event",
		Module: responseModule,
		ConditionTree: gess.Match{
			Binding: "ready",
			Target:  gess.TemplateFactIn(responseModule, readyTemplate),
		},
		Actions: []gess.RuleActionSpec{{Name: "handle-event"}},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func formatStack(stack []gess.ModuleName) string {
	if len(stack) == 0 {
		return "MAIN fallback"
	}
	parts := make([]string, len(stack))
	for i, module := range stack {
		parts[i] = string(module)
	}
	return strings.Join(parts, " > ")
}
