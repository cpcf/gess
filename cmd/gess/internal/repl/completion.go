package repl

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var replCommands = []string{
	"agenda",
	"assert",
	"diag",
	"exit",
	"explain",
	"facts",
	"focus",
	"help",
	"load",
	"modify",
	"query",
	"quit",
	"reload",
	"reset",
	"retract",
	"rule",
	"rules",
	"run",
	"watch",
	"whynot",
}

var watchEventNames = []string{
	"action-failed",
	"fact-asserted",
	"fact-modified",
	"fact-retracted",
	"logical-support-added",
	"logical-support-removed",
	"reset",
	"rule-activated",
	"rule-deactivated",
	"rule-fired",
}

type completionToken struct {
	value string
	start int
	end   int
}

type completionContext struct {
	tokens       []completionToken
	current      string
	currentStart int
	argIndex     int
	trailing     bool
}

func completeLine(ctx context.Context, state *replState, line string) []string {
	c := analyzeCompletionLine(line)
	if len(c.tokens) == 0 || (!c.trailing && len(c.tokens) == 1) {
		return completeReplacements(line, c.currentStart, c.current, replCommands, " ")
	}
	cmd := strings.ToLower(c.tokens[0].value)
	switch cmd {
	case "load":
		if c.argIndex == 1 {
			return completePaths(line, c.currentStart, c.current)
		}
	case "watch":
		return completeWatch(line, c)
	case "focus":
		if c.argIndex == 1 {
			return completeReplacements(line, c.currentStart, c.current, focusNames(state), "")
		}
	case "assert":
		return completeAssert(line, c, state)
	case "facts":
		if c.argIndex == 1 {
			return completeReplacements(line, c.currentStart, c.current, templateNames(state), "")
		}
	case "rule", "whynot":
		if c.argIndex == 1 {
			return completeReplacements(line, c.currentStart, c.current, ruleNames(state), "")
		}
	case "query":
		return completeQuery(line, c, state)
	case "retract":
		if c.argIndex == 1 {
			return completeReplacements(line, c.currentStart, c.current, factIDs(ctx, state), "")
		}
	case "modify":
		return completeModify(ctx, line, c, state)
	case "explain":
		if c.argIndex == 1 {
			return completeReplacements(line, c.currentStart, c.current, factIDs(ctx, state), " ")
		}
		if c.argIndex == 2 {
			return completeReplacements(line, c.currentStart, c.current, []string{"dot"}, "")
		}
	}
	return nil
}

func completeWatch(line string, c completionContext) []string {
	switch c.argIndex {
	case 1:
		return completeReplacements(line, c.currentStart, c.current, []string{"off", "on"}, " ")
	case 2:
		if len(c.tokens) >= 2 && c.tokens[1].value == "on" {
			return completeCommaValues(line, c.currentStart, c.current, watchEventNames)
		}
	}
	return nil
}

func completeAssert(line string, c completionContext, state *replState) []string {
	if c.argIndex == 1 {
		return completeReplacements(line, c.currentStart, c.current, templateNames(state), " ")
	}
	if c.argIndex >= 2 && len(c.tokens) >= 2 {
		fields := templateFieldNames(state, c.tokens[1].value)
		fields = removeAssignedFields(fields, c.tokens[2:])
		return completeReplacements(line, c.currentStart, c.current, appendEquals(fields), "")
	}
	return nil
}

func completeModify(ctx context.Context, line string, c completionContext, state *replState) []string {
	if c.argIndex == 1 {
		return completeReplacements(line, c.currentStart, c.current, factIDs(ctx, state), " ")
	}
	if c.argIndex >= 2 && len(c.tokens) >= 2 {
		fields := factFieldNames(ctx, state, c.tokens[1].value)
		fields = removeAssignedFields(fields, c.tokens[2:])
		return completeReplacements(line, c.currentStart, c.current, appendEquals(fields), "")
	}
	return nil
}

func completeQuery(line string, c completionContext, state *replState) []string {
	if c.argIndex == 1 {
		return completeReplacements(line, c.currentStart, c.current, queryNames(state), " ")
	}
	if c.argIndex >= 2 && len(c.tokens) >= 2 {
		params := queryParamNames(state, c.tokens[1].value)
		params = removeAssignedFields(params, c.tokens[2:])
		return completeReplacements(line, c.currentStart, c.current, appendEquals(params), "")
	}
	return nil
}

func analyzeCompletionLine(line string) completionContext {
	out := completionContext{currentStart: len(line)}
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
			r, size = utf8.DecodeRuneInString(line[i:])
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
		out.tokens = append(out.tokens, completionToken{value: b.String(), start: start, end: i})
	}
	if len(line) > 0 {
		r, _ := utf8.DecodeLastRuneInString(line)
		out.trailing = unicode.IsSpace(r)
	}
	if len(out.tokens) == 0 || out.trailing {
		out.currentStart = len(line)
		out.argIndex = len(out.tokens)
		return out
	}
	last := out.tokens[len(out.tokens)-1]
	out.current = last.value
	out.currentStart = last.start
	out.argIndex = len(out.tokens) - 1
	return out
}

func completeReplacements(line string, start int, current string, replacements []string, suffix string) []string {
	replacements = filterPrefix(replacements, current)
	out := make([]string, 0, len(replacements))
	seen := make(map[string]struct{}, len(replacements))
	for _, replacement := range replacements {
		candidate := line[:start] + replacement + suffix
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

func completeCommaValues(line string, start int, current string, values []string) []string {
	prefix, item, ok := strings.Cut(current, ",")
	if ok {
		parts := strings.Split(current, ",")
		item = parts[len(parts)-1]
		prefix = strings.Join(parts[:len(parts)-1], ",") + ","
	} else {
		prefix = ""
		item = current
	}
	filtered := filterPrefix(values, item)
	out := make([]string, 0, len(filtered))
	for _, value := range filtered {
		out = append(out, line[:start]+prefix+value)
	}
	sort.Strings(out)
	return out
}

func completePaths(line string, start int, current string) []string {
	dir, base := filepath.Split(current)
	readDir := dir
	if readDir == "" {
		readDir = "."
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		return nil
	}
	var replacements []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
			continue
		}
		if !entry.IsDir() && !strings.EqualFold(filepath.Ext(name), ".gess") {
			continue
		}
		replacement := dir + name
		if entry.IsDir() {
			replacement += string(os.PathSeparator)
		}
		replacements = append(replacements, replacement)
	}
	return completeReplacements(line, start, current, replacements, "")
}

func filterPrefix(values []string, prefix string) []string {
	if len(values) == 0 {
		return nil
	}
	lowerPrefix := strings.ToLower(prefix)
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !strings.HasPrefix(strings.ToLower(value), lowerPrefix) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func templateNames(state *replState) []string {
	if state == nil || state.ruleset == nil {
		return nil
	}
	templates := state.ruleset.Templates()
	out := make([]string, 0, len(templates))
	for _, template := range templates {
		out = append(out, template.Name())
	}
	sort.Strings(out)
	return out
}

func templateFieldNames(state *replState, templateName string) []string {
	if state == nil || state.ruleset == nil {
		return nil
	}
	template, ok := state.ruleset.Template(templateName)
	if !ok {
		return nil
	}
	fields := template.Fields()
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.Name)
	}
	sort.Strings(out)
	return out
}

func ruleNames(state *replState) []string {
	if state == nil || state.ruleset == nil {
		return nil
	}
	rules := state.ruleset.Rules()
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.Name())
	}
	sort.Strings(out)
	return out
}

func queryNames(state *replState) []string {
	if state == nil || state.ruleset == nil {
		return nil
	}
	queries := state.ruleset.Queries()
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		out = append(out, query.Name())
	}
	sort.Strings(out)
	return out
}

func queryParamNames(state *replState, queryName string) []string {
	if state == nil || state.ruleset == nil {
		return nil
	}
	query, ok := state.ruleset.Query(queryName)
	if !ok {
		return nil
	}
	params := query.Parameters()
	out := make([]string, 0, len(params))
	for _, param := range params {
		out = append(out, param.Name())
	}
	sort.Strings(out)
	return out
}

func focusNames(state *replState) []string {
	out := []string{"clear", "pop"}
	if state == nil || state.ruleset == nil {
		return append(out, "MAIN")
	}
	modules := state.ruleset.Modules()
	for _, module := range modules {
		out = append(out, module.Name().String())
	}
	sort.Strings(out)
	return out
}

func factIDs(ctx context.Context, state *replState) []string {
	if state == nil || state.session == nil {
		return nil
	}
	snapshot, err := state.session.Snapshot(ctx)
	if err != nil {
		return nil
	}
	facts := snapshot.Facts()
	out := make([]string, 0, len(facts))
	for _, fact := range facts {
		out = append(out, fact.ID().String())
	}
	sort.Strings(out)
	return out
}

func factFieldNames(ctx context.Context, state *replState, id string) []string {
	if state == nil || state.session == nil {
		return nil
	}
	snapshot, err := state.session.Snapshot(ctx)
	if err != nil {
		return nil
	}
	for _, fact := range snapshot.Facts() {
		if fact.ID().String() != id {
			continue
		}
		if fields := templateFieldNames(state, fact.Name()); len(fields) > 0 {
			return fields
		}
		raw := fact.Fields()
		out := make([]string, 0, len(raw))
		for name := range raw {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func appendEquals(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value+"=")
	}
	return out
}

func removeAssignedFields(fields []string, tokens []completionToken) []string {
	if len(fields) == 0 || len(tokens) == 0 {
		return fields
	}
	assigned := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		name, _, ok := strings.Cut(token.value, "=")
		if ok && name != "" {
			assigned[name] = struct{}{}
		}
	}
	out := fields[:0]
	for _, field := range fields {
		if _, ok := assigned[field]; !ok {
			out = append(out, field)
		}
	}
	return out
}
