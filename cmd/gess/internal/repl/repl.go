package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	dsl "github.com/cpcf/gess/dsl"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

var ErrCommandFailed = errors.New("one or more repl commands failed")

type Options struct {
	StubCalls   bool
	Interactive bool
	Prompt      string
}

type replState struct {
	out        io.Writer
	stubCalls  bool
	watch      bool
	watchTypes []sess.EventType

	lastLoad string
	ruleset  *rules.Ruleset
	session  *sess.Session
}

func Run(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Prompt == "" {
		opts.Prompt = "gess> "
	}
	state := &replState{out: out, stubCalls: opts.StubCalls}
	defer func() {
		if state.session != nil {
			_ = state.session.Close()
		}
	}()
	if opts.Interactive {
		return runInteractive(ctx, state, in, out, opts)
	}
	return runScript(ctx, state, in, out)
}

func runScript(ctx context.Context, state *replState, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	failed := false
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			exit, err := state.exec(ctx, trimmed)
			if err != nil {
				failed = true
				fmt.Fprintf(out, "error: %v\n", err)
			}
			if exit {
				break
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	if failed {
		return ErrCommandFailed
	}
	return nil
}

func (s *replState) exec(ctx context.Context, line string) (bool, error) {
	fields, err := splitFields(line)
	if err != nil {
		return false, err
	}
	if len(fields) == 0 {
		return false, nil
	}
	switch fields[0] {
	case "exit", "quit":
		return true, nil
	case "help":
		printHelp(s.out)
	case "load":
		if len(fields) != 2 {
			return false, fmt.Errorf("usage: load <file.gess>")
		}
		return false, s.load(ctx, fields[1])
	case "reload":
		if s.lastLoad == "" {
			return false, fmt.Errorf("no previous load")
		}
		return false, s.load(ctx, s.lastLoad)
	case "assert":
		return false, s.assert(ctx, fields[1:])
	case "retract":
		return false, s.retract(ctx, fields[1:])
	case "modify":
		return false, s.modify(ctx, fields[1:])
	case "run":
		return false, s.run(ctx, fields[1:])
	case "facts":
		return false, s.facts(ctx, fields[1:])
	case "explain":
		return false, s.explain(ctx, fields[1:])
	case "whynot":
		return false, s.whyNot(ctx, fields[1:])
	case "agenda":
		return false, s.agenda(ctx, fields[1:])
	case "query":
		return false, s.query(ctx, fields[1:])
	case "rules":
		return false, s.rules(fields[1:])
	case "rule":
		return false, s.rule(fields[1:])
	case "watch":
		return false, s.watchCommand(fields[1:])
	case "focus":
		return false, s.focus(ctx, fields[1:])
	case "reset":
		return false, s.reset(ctx, fields[1:])
	default:
		return false, fmt.Errorf("unknown command %q", fields[0])
	}
	return false, nil
}

func (s *replState) load(ctx context.Context, path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	doc, err := dsl.Parse(path, source)
	if err != nil {
		return err
	}
	registry := dsl.Registry{}
	missing := doc.MissingRegistrations(registry)
	if len(missing.Actions) > 0 {
		return fmt.Errorf("missing registered actions: %s", strings.Join(missing.Actions, ", "))
	}
	if len(missing.Calls) > 0 {
		if !s.stubCalls {
			return fmt.Errorf("missing registered calls: %s (rerun with --stub-calls to stub calls)", strings.Join(missing.Calls, ", "))
		}
		registry.Calls = make(map[string]dsl.CallFunc, len(missing.Calls))
		for _, name := range missing.Calls {
			callName := name
			registry.Calls[callName] = func(_ rules.ActionContext, args []rules.Value) error {
				fmt.Fprintf(s.out, "stub call %s", callName)
				for _, arg := range args {
					fmt.Fprintf(s.out, " %s", formatValue(arg))
				}
				fmt.Fprintln(s.out)
				return nil
			}
		}
	}
	workspace := sess.NewWorkspace()
	if err := dsl.Load(ctx, workspace, doc, registry); err != nil {
		return err
	}
	ruleset, err := rules.Compile(ctx, workspace)
	if err != nil {
		return err
	}
	opts := []sess.Option{
		sess.WithInitialFacts(dsl.InitialFacts(doc)...),
		sess.WithExplainLog(),
	}
	if s.watch {
		listenerOpts := make([]sess.EventListenerOption, 0, 1)
		if len(s.watchTypes) > 0 {
			listenerOpts = append(listenerOpts, sess.ForEventTypes(s.watchTypes...))
		}
		opts = append(opts, sess.WithEventListener(sess.NewTraceListener(s.out), listenerOpts...))
	}
	next, err := sess.New(ruleset, opts...)
	if err != nil {
		return err
	}
	if s.session != nil {
		_ = s.session.Close()
	}
	s.session = next
	s.ruleset = ruleset
	s.lastLoad = path
	fmt.Fprintf(s.out, "loaded %s: templates=%d rules=%d queries=%d deffacts=%d\n", path, len(ruleset.Templates()), len(ruleset.Rules()), len(ruleset.Queries()), len(dsl.InitialFacts(doc)))
	return nil
}

func (s *replState) assert(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: assert <template> <field>=<value> ...")
	}
	template, ok := s.ruleset.Template(args[0])
	if !ok {
		return fmt.Errorf("unknown template %q", args[0])
	}
	fields, err := parseAssignments(args[1:])
	if err != nil {
		return err
	}
	result, err := s.session.Assert(ctx, template.Key(), fields)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "assert status=%s", result.Status)
	if !result.Fact.ID().IsZero() {
		fmt.Fprintf(s.out, " id=%s", result.Fact.ID())
	}
	fmt.Fprintln(s.out)
	return nil
}

func (s *replState) retract(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: retract <fact-id>")
	}
	id, err := s.factID(ctx, args[0])
	if err != nil {
		return err
	}
	result, err := s.session.Retract(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "retract status=%s", result.Status)
	if !result.Fact.ID().IsZero() {
		fmt.Fprintf(s.out, " id=%s", result.Fact.ID())
	}
	fmt.Fprintln(s.out)
	return nil
}

func (s *replState) modify(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: modify <fact-id> <field>=<value> ...")
	}
	id, err := s.factID(ctx, args[0])
	if err != nil {
		return err
	}
	fields, err := parseAssignments(args[1:])
	if err != nil {
		return err
	}
	result, err := s.session.Modify(ctx, id, sess.FactPatch{Set: fields})
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "modify status=%s", result.Status)
	if !result.Fact.ID().IsZero() {
		fmt.Fprintf(s.out, " id=%s", result.Fact.ID())
	}
	fmt.Fprintln(s.out)
	return nil
}

func (s *replState) run(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: run [n]")
	}
	var opts []sess.RunOption
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			return fmt.Errorf("run limit must be a positive integer")
		}
		opts = append(opts, sess.WithMaxFirings(n))
	}
	result, err := s.session.Run(ctx, opts...)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "run status=%s fired=%d\n", result.Status, result.Fired)
	return nil
}

func (s *replState) facts(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) > 1 {
		return fmt.Errorf("usage: facts [template]")
	}
	snapshot, err := s.session.Snapshot(ctx)
	if err != nil {
		return err
	}
	facts := snapshot.Facts()
	if len(args) == 1 {
		facts = snapshot.FactsByName(args[0])
	}
	fmt.Fprintf(s.out, "facts count=%d\n", len(facts))
	for _, fact := range facts {
		fmt.Fprintln(s.out, formatFact(fact))
	}
	return nil
}

func (s *replState) explain(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[1] != "dot") {
		return fmt.Errorf("usage: explain <fact-id> [dot]")
	}
	id, err := s.factID(ctx, args[0])
	if err != nil {
		return err
	}

	lineageAvailable := true
	derivation, err := s.session.Explain(ctx, id)
	if err != nil {
		if !errors.Is(err, sess.ErrExplainLogUnavailable) {
			return err
		}
		lineageAvailable = false
		snapshot, serr := s.session.Snapshot(ctx)
		if serr != nil {
			return serr
		}
		tier1, ok := snapshot.Explain(id)
		if !ok {
			return fmt.Errorf("unknown fact id %q", args[0])
		}
		derivation = tier1
	}

	if len(args) == 2 {
		_, err := io.WriteString(s.out, derivation.DOT())
		return err
	}
	if _, err := io.WriteString(s.out, derivation.String()); err != nil {
		return err
	}
	if !lineageAvailable {
		fmt.Fprintln(s.out, "note: firing and mutation lineage need an explain log (this session has none)")
	}
	return nil
}

func (s *replState) whyNot(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: whynot <rule>")
	}
	report, err := s.session.WhyNot(ctx, args[0])
	if err != nil {
		if errors.Is(err, sess.ErrRuleNotFound) {
			return fmt.Errorf("unknown rule %q", args[0])
		}
		return err
	}
	_, err = io.WriteString(s.out, report.String())
	return err
}

func (s *replState) agenda(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: agenda")
	}
	agenda, err := s.session.Agenda(ctx)
	if err != nil {
		return err
	}
	activations := agenda.Activations()
	fmt.Fprintf(s.out, "agenda count=%d focus=%s\n", len(activations), agenda.FocusStack())
	for _, activation := range activations {
		fmt.Fprintf(s.out, "%s rule=%s module=%s salience=%d facts=%s\n", activation.ActivationID(), activation.RuleName(), activation.Module(), activation.Salience(), activation.FactIDs())
	}
	return nil
}

func (s *replState) query(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: query <name> [arg=value ...]")
	}
	queryArgs, err := parseQueryArgs(args[1:])
	if err != nil {
		return err
	}
	rows, err := s.session.QueryAll(ctx, args[0], queryArgs)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "query %s rows=%d\n", args[0], len(rows))
	for _, row := range rows {
		aliases := row.Aliases()
		parts := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			if value, ok := row.Value(alias); ok {
				parts = append(parts, alias+"="+formatValue(value))
				continue
			}
			if fact, ok := row.Fact(alias); ok {
				parts = append(parts, alias+"="+fact.ID().String())
			}
		}
		fmt.Fprintln(s.out, strings.Join(parts, " "))
	}
	return nil
}

func (s *replState) rules(args []string) error {
	if err := s.requireRuleset(); err != nil {
		return err
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: rules")
	}
	all := s.ruleset.Rules()
	fmt.Fprintf(s.out, "rules count=%d\n", len(all))
	for _, rule := range all {
		fmt.Fprintf(s.out, "%s module=%s salience=%d\n", rule.Name(), rule.Module(), rule.Salience())
	}
	return nil
}

func (s *replState) rule(args []string) error {
	if err := s.requireRuleset(); err != nil {
		return err
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: rule <name>")
	}
	rule, ok := s.ruleset.Rule(args[0])
	if !ok {
		return fmt.Errorf("unknown rule %q", args[0])
	}
	rendered, err := dsl.RenderRule(s.ruleset, rule.Name())
	if err != nil {
		return err
	}
	_, err = s.out.Write(rendered)
	return err
}

func (s *replState) watchCommand(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: watch on|off [types]")
	}
	switch args[0] {
	case "on":
		types, err := parseEventTypes(args[1:])
		if err != nil {
			return err
		}
		s.watch = true
		s.watchTypes = types
		fmt.Fprintln(s.out, "watch on")
		if s.session != nil {
			fmt.Fprintln(s.out, "watch will apply on next load or reload")
		}
	case "off":
		s.watch = false
		s.watchTypes = nil
		fmt.Fprintln(s.out, "watch off")
		if s.session != nil {
			fmt.Fprintln(s.out, "watch will apply on next load or reload")
		}
	default:
		return fmt.Errorf("usage: watch on|off [types]")
	}
	return nil
}

func parseEventTypes(args []string) ([]sess.EventType, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, nil
	}
	names := strings.Split(args[0], ",")
	types := make([]sess.EventType, 0, len(names))
	for _, name := range names {
		eventType, ok := eventTypeByName(strings.TrimSpace(name))
		if !ok {
			return nil, fmt.Errorf("unknown watch event type %q", name)
		}
		types = append(types, eventType)
	}
	return types, nil
}

func eventTypeByName(name string) (sess.EventType, bool) {
	switch strings.ToLower(name) {
	case "fact-asserted", "asserted", "assert":
		return sess.EventFactAsserted, true
	case "fact-modified", "modified", "modify":
		return sess.EventFactModified, true
	case "fact-retracted", "retracted", "retract":
		return sess.EventFactRetracted, true
	case "reset":
		return sess.EventReset, true
	case "rule-activated", "activated", "activate":
		return sess.EventRuleActivated, true
	case "rule-deactivated", "deactivated", "deactivate":
		return sess.EventRuleDeactivated, true
	case "rule-fired", "fired", "fire":
		return sess.EventRuleFired, true
	case "action-failed", "failed", "failure":
		return sess.EventActionFailed, true
	case "logical-support-added", "support-added":
		return sess.EventLogicalSupportAdded, true
	case "logical-support-removed", "support-removed":
		return sess.EventLogicalSupportRemoved, true
	default:
		return "", false
	}
}

func (s *replState) focus(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	switch len(args) {
	case 0:
		fmt.Fprintf(s.out, "focus current=%s stack=%s\n", s.session.CurrentFocus(), s.session.FocusStack())
	case 1:
		switch args[0] {
		case "pop":
			module, err := s.session.PopFocus(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(s.out, "focus popped=%s current=%s\n", module, s.session.CurrentFocus())
		case "clear":
			if err := s.session.ClearFocusStack(ctx); err != nil {
				return err
			}
			fmt.Fprintf(s.out, "focus current=%s stack=%s\n", s.session.CurrentFocus(), s.session.FocusStack())
		default:
			if err := s.session.SetFocus(ctx, rules.ModuleName(args[0])); err != nil {
				return err
			}
			fmt.Fprintf(s.out, "focus current=%s stack=%s\n", s.session.CurrentFocus(), s.session.FocusStack())
		}
	default:
		return fmt.Errorf("usage: focus [module|pop|clear]")
	}
	return nil
}

func (s *replState) reset(ctx context.Context, args []string) error {
	if err := s.requireSession(); err != nil {
		return err
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: reset")
	}
	result, err := s.session.Reset(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.out, "reset status=%s generation=%d\n", result.Status, result.Generation)
	return nil
}

func (s *replState) factID(ctx context.Context, raw string) (sess.FactID, error) {
	snapshot, err := s.session.Snapshot(ctx)
	if err != nil {
		return sess.FactID{}, err
	}
	for _, fact := range snapshot.Facts() {
		if fact.ID().String() == raw {
			return fact.ID(), nil
		}
	}
	return sess.FactID{}, fmt.Errorf("unknown fact id %q", raw)
}

func (s *replState) requireSession() error {
	if s.session == nil {
		return fmt.Errorf("no rules loaded")
	}
	return nil
}

func (s *replState) requireRuleset() error {
	if s.ruleset == nil {
		return fmt.Errorf("no rules loaded")
	}
	return nil
}

func parseAssignments(args []string) (rules.Fields, error) {
	fields := make(rules.Fields, len(args))
	for _, arg := range args {
		name, raw, ok := strings.Cut(arg, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("bad field assignment %q; want field=value", arg)
		}
		value, err := parseValue(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		fields[name] = value
	}
	return fields, nil
}

func parseQueryArgs(args []string) (sess.QueryArgs, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make(sess.QueryArgs, len(args))
	for _, arg := range args {
		name, raw, ok := strings.Cut(arg, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("bad query argument %q; want arg=value", arg)
		}
		value, err := parseRawValue(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = value
	}
	return out, nil
}

func parseValue(raw string) (rules.Value, error) {
	value, err := parseRawValue(raw)
	if err != nil {
		return rules.Value{}, err
	}
	return rules.NewValue(value)
}

func parseRawValue(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty value")
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return strconv.Unquote(raw)
	}
	switch strings.ToLower(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return i, nil
	}
	if strings.ContainsAny(raw, ".eE") {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f, nil
		}
	}
	return raw, nil
}

func formatFact(fact sess.FactSnapshot) string {
	fields := fact.Fields()
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names)+2)
	parts = append(parts, "id="+fact.ID().String(), "template="+fact.Name())
	for _, name := range names {
		parts = append(parts, name+"="+formatValue(fields[name]))
	}
	return strings.Join(parts, " ")
}

func formatValue(value rules.Value) string {
	switch value.Kind() {
	case rules.ValueNull:
		return "null"
	case rules.ValueBool:
		if v, ok := value.AsBool(); ok {
			return strconv.FormatBool(v)
		}
	case rules.ValueInt:
		if v, ok := value.AsInt64(); ok {
			return strconv.FormatInt(v, 10)
		}
	case rules.ValueFloat:
		if v, ok := value.AsFloat64(); ok {
			return strconv.FormatFloat(v, 'g', -1, 64)
		}
	case rules.ValueString:
		if v, ok := value.AsString(); ok {
			return v
		}
	}
	return value.String()
}

func splitFields(line string) ([]string, error) {
	var fields []string
	i := 0
	for i < len(line) {
		r, size := utf8.DecodeRuneInString(line[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		start := i
		var b strings.Builder
		quoted := false
		for i < len(line) {
			r, size := utf8.DecodeRuneInString(line[i:])
			if !quoted && unicode.IsSpace(r) {
				break
			}
			if r == '\\' && i+size < len(line) {
				b.WriteRune(r)
				i += size
				escaped, escapedSize := utf8.DecodeRuneInString(line[i:])
				b.WriteRune(escaped)
				i += escapedSize
				continue
			}
			if r == '"' {
				quoted = !quoted
			}
			b.WriteRune(r)
			i += size
		}
		if quoted {
			return nil, fmt.Errorf("unterminated quote near %q", line[start:])
		}
		fields = append(fields, b.String())
	}
	return fields, nil
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "commands:")
	fmt.Fprintln(out, "  load <file.gess>")
	fmt.Fprintln(out, "  reload")
	fmt.Fprintln(out, "  assert <template> <field>=<value> ...")
	fmt.Fprintln(out, "  retract <fact-id>")
	fmt.Fprintln(out, "  modify <fact-id> <field>=<value> ...")
	fmt.Fprintln(out, "  run [n]")
	fmt.Fprintln(out, "  facts [template]")
	fmt.Fprintln(out, "  explain <fact-id> [dot]")
	fmt.Fprintln(out, "  whynot <rule>")
	fmt.Fprintln(out, "  agenda")
	fmt.Fprintln(out, "  query <name> [arg=value ...]")
	fmt.Fprintln(out, "  rules")
	fmt.Fprintln(out, "  rule <name>")
	fmt.Fprintln(out, "  watch on|off [types]")
	fmt.Fprintln(out, "  focus [module|pop|clear]")
	fmt.Fprintln(out, "  reset")
	fmt.Fprintln(out, "  help")
	fmt.Fprintln(out, "  exit")
	fmt.Fprintln(out, "piped mode exits non-zero if any command reports an error; the loop continues after command errors.")
}
