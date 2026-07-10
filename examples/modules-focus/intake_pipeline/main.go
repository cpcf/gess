package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cpcf/gess/examples/internal/exampleutil"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

const (
	intakeModule   = rules.ModuleName("intake")
	responseModule = rules.ModuleName("response")
	eventTemplate  = "event"
	readyTemplate  = "ready-event"
	eventKey       = rules.TemplateKey("intake-event")
	readyKey       = rules.TemplateKey("response-ready-event")
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
	session, err := sess.New(ruleset)
	if err != nil {
		return err
	}
	defer session.Close()

	if _, err := session.Assert(ctx, eventKey, exampleutil.Fields("id", "E-100", "kind", "incident")); err != nil {
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

func buildRuleset(ctx context.Context, handled *[]string) (*rules.Ruleset, error) {
	autoFocus := false
	workspace := sess.NewWorkspace()
	for _, module := range []rules.ModuleSpec{
		{Name: intakeModule, AutoFocus: &autoFocus},
		{Name: responseModule, AutoFocus: &autoFocus},
	} {
		if err := workspace.AddModule(module); err != nil {
			return nil, err
		}
	}
	for _, spec := range []rules.TemplateSpec{
		{Module: intakeModule, Name: eventTemplate, Key: eventKey, Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "kind", Kind: rules.ValueString, Required: true},
		}},
		{Module: responseModule, Name: readyTemplate, Key: readyKey, DuplicatePolicy: rules.DuplicateUniqueKey, DuplicateKeyNames: []string{"id"}, Fields: []rules.FieldSpec{
			{Name: "id", Kind: rules.ValueString, Required: true},
			{Name: "kind", Kind: rules.ValueString, Required: true},
		}},
	} {
		if err := workspace.AddTemplate(spec); err != nil {
			return nil, err
		}
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "promote-event",
		Fn: func(ctx rules.ActionContext) error {
			id, _ := ctx.BindingScalarValue("event", "id")
			kind, _ := ctx.BindingScalarValue("event", "kind")
			if _, err := ctx.Assert(readyKey, rules.Fields{"id": id, "kind": kind}); err != nil {
				return err
			}
			return ctx.PushFocus(responseModule)
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddAction(rules.ActionSpec{
		Name: "handle-event",
		Fn: func(ctx rules.ActionContext) error {
			id, _ := ctx.BindingScalarValue("ready", "id")
			text, _ := id.AsString()
			*handled = append(*handled, text)
			_, err := ctx.PopFocus()
			return err
		},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name:   "validate-incident",
		Module: intakeModule,
		ConditionTree: rules.Match{
			Binding: "event",
			FieldConstraints: []rules.FieldConstraintSpec{
				{Field: "kind", Operator: rules.FieldConstraintEqual, Value: "incident"},
			},
			Target: rules.TemplateFactIn(intakeModule, eventTemplate),
		},
		Actions: []rules.RuleActionSpec{{Name: "promote-event"}},
	}); err != nil {
		return nil, err
	}
	if err := workspace.AddRule(rules.RuleSpec{
		Name:   "handle-ready-event",
		Module: responseModule,
		ConditionTree: rules.Match{
			Binding: "ready",
			Target:  rules.TemplateFactIn(responseModule, readyTemplate),
		},
		Actions: []rules.RuleActionSpec{{Name: "handle-event"}},
	}); err != nil {
		return nil, err
	}
	return workspace.Compile(ctx)
}

func formatStack(stack []rules.ModuleName) string {
	if len(stack) == 0 {
		return "MAIN fallback"
	}
	parts := make([]string, len(stack))
	for i, module := range stack {
		parts[i] = string(module)
	}
	return strings.Join(parts, " > ")
}
