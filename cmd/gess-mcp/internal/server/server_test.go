package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/scenario"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testRuleset = `(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot amount (type INTEGER) (required TRUE)))

(deffacts seed
  (order (id "O-100") (amount 25)))

(defrule route-order ?order <-
  (order (id ?id) (amount ?amount))
  (test (> ?amount 10))
  =>
  (emit ?id))

(defquery all-orders
  (order (id ?id) (amount ?amount))
  (return
    (id ?id)
    (amount ?amount)))

(defquery orders-at-amount
  (declare (variables ?amount))
  (order (id ?id) (amount ?amount))
  (return (id ?id)))
`

func TestRulesetPathConfinement(t *testing.T) {
	root, err := canonicalRulesetRoot(t.TempDir())
	if err != nil {
		t.Fatalf("canonical root: %v", err)
	}
	inside := filepath.Join(root, "rules.gess")
	writeTestFile(t, inside, testRuleset)
	resolved, relative, err := resolveRulesetPath(root, "rules.gess")
	if err != nil {
		t.Fatalf("resolve inside path: %v", err)
	}
	if resolved != inside || relative != "rules.gess" {
		t.Fatalf("resolved path = (%q, %q), want (%q, rules.gess)", resolved, relative, inside)
	}

	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "outside.gess")
	writeTestFile(t, outside, testRuleset)
	if _, _, err := resolveRulesetPath(root, outside); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside path error = %v, want confinement error", err)
	}

	symlink := filepath.Join(root, "linked.gess")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, _, err := resolveRulesetPath(root, symlink); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("symlink escape error = %v, want confinement error", err)
	}

	notGess := filepath.Join(root, "rules.txt")
	writeTestFile(t, notGess, testRuleset)
	if _, _, err := resolveRulesetPath(root, notGess); err == nil || !strings.Contains(err.Error(), ".gess") {
		t.Fatalf("non-gess path error = %v, want extension error", err)
	}
}

func TestConfigRejectsNonPositiveBounds(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{name: "explain log", config: Config{RulesetRoot: root, ExplainLogMaxEntries: -1}, want: "explain log max entries"},
		{name: "firings", config: Config{RulesetRoot: root, MaxFirings: -1}, want: "max firings"},
		{name: "query rows", config: Config{RulesetRoot: root, MaxQueryRows: -1}, want: "max query rows"},
		{name: "demand cascade", config: Config{RulesetRoot: root, MaxDemandCascadeSteps: -1}, want: "max demand cascade steps"},
		{name: "what-if operations", config: Config{RulesetRoot: root, MaxWhatIfOperations: -1}, want: "max what-if operations"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeJSONValue(t *testing.T) {
	decoded, err := decodeJSONValue(map[string]any{
		"integer":    float64(3),
		"fractional": float64(3.5),
		"tooLarge":   float64(math.MaxInt64),
		"list":       []any{float64(4)},
		"object":     map[string]any{"kind": "domain", "count": float64(5)},
	})
	if err != nil {
		t.Fatalf("decode ordinary JSON: %v", err)
	}
	got := decoded.(map[string]any)
	if got["integer"] != int64(3) || got["fractional"] != float64(3.5) {
		t.Fatalf("normalized scalars = %#v", got)
	}
	if _, ok := got["tooLarge"].(float64); !ok {
		t.Fatalf("too-large integer normalized to %T, want float64", got["tooLarge"])
	}
	if list := got["list"].([]any); list[0] != int64(4) {
		t.Fatalf("normalized list = %#v", list)
	}
	if object := got["object"].(map[string]any); object["kind"] != "domain" || object["count"] != int64(5) {
		t.Fatalf("unknown-kind object = %#v", object)
	}

	typed, err := decodeJSONValue(map[string]any{"kind": "int", "int": "9223372036854775807"})
	if err != nil {
		t.Fatalf("decode typed integer: %v", err)
	}
	integer, ok := typed.(rules.Value)
	if !ok {
		t.Fatalf("decoded typed integer = %T, want rules.Value", typed)
	}
	if value, ok := integer.AsInt64(); !ok || value != math.MaxInt64 {
		t.Fatalf("decoded typed integer = (%d, %t), want (%d, true)", value, ok, int64(math.MaxInt64))
	}
	if _, err := decodeJSONValue(map[string]any{"kind": "int", "int": "01"}); !errors.Is(err, scenario.ErrInvalidValueJSON) {
		t.Fatalf("non-canonical typed integer error = %v, want ErrInvalidValueJSON", err)
	}
}

func TestMCPInterrogationAndStatefulTools(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "rules.gess"), testRuleset)
	service, err := New(Config{
		RulesetRoot:           root,
		ExplainLogMaxEntries:  32,
		MaxFirings:            2,
		MaxQueryRows:          2,
		MaxDemandCascadeSteps: 20,
		MaxWhatIfOperations:   3,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer service.Close()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "gess-mcp-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer clientSession.Close()

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 12 {
		t.Fatalf("tool count = %d, want 12", len(tools.Tools))
	}
	readOnly := map[string]bool{"snapshot": true, "agenda": true, "diagnostics": true, "explain": true, "why_not": true, "what_if": true}
	idempotent := map[string]bool{"modify": true, "retract": true}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil {
			t.Fatalf("tool %q has no safety annotations", tool.Name)
		}
		if readOnly[tool.Name] {
			if !tool.Annotations.ReadOnlyHint {
				t.Fatalf("tool %q is missing read-only annotation", tool.Name)
			}
			continue
		}
		if tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Fatalf("stateful tool %q annotations = %#v", tool.Name, tool.Annotations)
		}
		if tool.Annotations.IdempotentHint != idempotent[tool.Name] {
			t.Fatalf("tool %q idempotent = %t, want %t", tool.Name, tool.Annotations.IdempotentHint, idempotent[tool.Name])
		}
	}

	before := callTool(t, ctx, clientSession, "snapshot", nil)
	if !before.IsError || !strings.Contains(toolText(before), "call load first") {
		t.Fatalf("snapshot before load = error %t content %q", before.IsError, toolText(before))
	}

	load := callTool(t, ctx, clientSession, "load", map[string]any{"path": "rules.gess"})
	loadOutput := toolObject(t, load)
	if loadOutput["gessMcpSchema"] != float64(1) || loadOutput["kind"] != "load" || loadOutput["status"] != "loaded" || loadOutput["path"] != "rules.gess" {
		t.Fatalf("load output = %#v", loadOutput)
	}

	snapshot := toolObject(t, callTool(t, ctx, clientSession, "snapshot", nil))
	if snapshot["gessMcpSchema"] != float64(1) || snapshot["kind"] != "snapshot" {
		t.Fatalf("snapshot envelope = %#v", snapshot)
	}
	facts, ok := snapshot["facts"].([]any)
	if !ok || len(facts) != 1 {
		t.Fatalf("snapshot facts = %#v, want one", snapshot["facts"])
	}
	fact, ok := facts[0].(map[string]any)
	if !ok || fact["templateKey"] != "order" {
		t.Fatalf("snapshot fact = %#v", facts[0])
	}
	factID, _ := fact["id"].(string)
	if factID == "" {
		t.Fatalf("snapshot fact ID = %#v", fact["id"])
	}
	if amount := fact["fields"].(map[string]any)["amount"]; projectedInt(amount) != "25" {
		t.Fatalf("snapshot amount = %#v, want typed integer 25", amount)
	}

	agenda := toolObject(t, callTool(t, ctx, clientSession, "agenda", nil))
	if agenda["gessMcpSchema"] != float64(1) || agenda["kind"] != "agenda" {
		t.Fatalf("agenda envelope = %#v", agenda)
	}
	activations, ok := agenda["activations"].([]any)
	if !ok || len(activations) != 1 {
		t.Fatalf("agenda activations = %#v, want one", agenda["activations"])
	}

	diagnostics := toolObject(t, callTool(t, ctx, clientSession, "diagnostics", nil))
	if diagnostics["gessDiagnosticsSchema"] != float64(1) {
		t.Fatalf("diagnostics schema = %#v, want 1", diagnostics["gessDiagnosticsSchema"])
	}
	if backchain := diagnostics["backchain"].(map[string]any); backchain["cascadeLimit"] != float64(20) {
		t.Fatalf("diagnostics cascade limit = %#v, want 20", backchain["cascadeLimit"])
	}

	explain := toolObject(t, callTool(t, ctx, clientSession, "explain", map[string]any{"factId": factID}))
	if explain["gessExplainSchema"] != float64(1) || explain["kind"] != "derivation" {
		t.Fatalf("explain envelope = %#v", explain)
	}

	whyNot := toolObject(t, callTool(t, ctx, clientSession, "why_not", map[string]any{"rule": "route-order"}))
	if whyNot["gessExplainSchema"] != float64(1) || whyNot["kind"] != "whynot" {
		t.Fatalf("why-not envelope = %#v", whyNot)
	}
	invalidAssert := callTool(t, ctx, clientSession, "assert", map[string]any{
		"template": "order",
		"fields":   map[string]any{"id": "BAD", "amount": "not-an-integer"},
	})
	if !invalidAssert.IsError {
		t.Fatal("invalid assert did not return a tool error")
	}
	invalidOutput, ok := invalidAssert.StructuredContent.(map[string]any)
	if !ok || invalidOutput["kind"] != "assert" || invalidOutput["status"] != "validation_failure" {
		t.Fatalf("invalid assert structured result = %#v", invalidAssert.StructuredContent)
	}
	nonCanonicalAssert := callTool(t, ctx, clientSession, "assert", map[string]any{
		"template": "order",
		"fields":   map[string]any{"id": "BAD-TYPED", "amount": map[string]any{"kind": "int", "int": "030"}},
	})
	if !nonCanonicalAssert.IsError || !strings.Contains(toolText(nonCanonicalAssert), "non-canonical int") {
		t.Fatalf("non-canonical typed assert = error %t content %q", nonCanonicalAssert.IsError, toolText(nonCanonicalAssert))
	}

	asserted := toolObject(t, callTool(t, ctx, clientSession, "assert", map[string]any{
		"template": "order",
		"fields":   map[string]any{"id": "O-200", "amount": map[string]any{"kind": "int", "int": "9223372036854775807"}},
	}))
	if asserted["kind"] != "assert" || asserted["status"] != "inserted" {
		t.Fatalf("assert output = %#v", asserted)
	}
	assertedFact := asserted["fact"].(map[string]any)
	assertedID := assertedFact["id"].(string)
	assertedFields := assertedFact["fields"].(map[string]any)
	if projectedInt(assertedFields["amount"]) != "9223372036854775807" {
		t.Fatalf("asserted amount = %#v, want typed max int64", assertedFields["amount"])
	}

	overLimit := callTool(t, ctx, clientSession, "run", map[string]any{"maxFirings": 3})
	if !overLimit.IsError || !strings.Contains(toolText(overLimit), "exceeds server ceiling 2") {
		t.Fatalf("over-limit run = error %t content %q", overLimit.IsError, toolText(overLimit))
	}
	firstRun := toolObject(t, callTool(t, ctx, clientSession, "run", map[string]any{"maxFirings": 1}))
	if firstRun["kind"] != "run" || firstRun["status"] != "fire_limit" || firstRun["fired"] != float64(1) || firstRun["maxFirings"] != float64(1) {
		t.Fatalf("first bounded run = %#v", firstRun)
	}
	secondRun := toolObject(t, callTool(t, ctx, clientSession, "run", nil))
	if secondRun["status"] != "completed" || secondRun["fired"] != float64(1) || secondRun["maxFirings"] != float64(2) {
		t.Fatalf("second bounded run = %#v", secondRun)
	}

	modified := toolObject(t, callTool(t, ctx, clientSession, "modify", map[string]any{
		"factId": assertedID,
		"set":    map[string]any{"amount": map[string]any{"kind": "int", "int": "5"}},
	}))
	if modified["kind"] != "modify" || modified["status"] != "changed" {
		t.Fatalf("modify output = %#v", modified)
	}
	modifiedAmount := modified["fact"].(map[string]any)["fields"].(map[string]any)["amount"]
	if projectedInt(modifiedAmount) != "5" {
		t.Fatalf("modified amount = %#v, want typed integer 5", modifiedAmount)
	}

	third := toolObject(t, callTool(t, ctx, clientSession, "assert", map[string]any{
		"template": "order",
		"fields":   map[string]any{"id": "O-300", "amount": 40},
	}))
	if third["status"] != "inserted" {
		t.Fatalf("third assert = %#v", third)
	}
	thirdID := third["fact"].(map[string]any)["id"].(string)

	queryOverLimit := callTool(t, ctx, clientSession, "query", map[string]any{
		"name": "all-orders", "maxRows": 3,
	})
	if !queryOverLimit.IsError || !strings.Contains(toolText(queryOverLimit), "exceeds server ceiling 2") {
		t.Fatalf("over-limit query = error %t content %q", queryOverLimit.IsError, toolText(queryOverLimit))
	}
	query := toolObject(t, callTool(t, ctx, clientSession, "query", map[string]any{
		"name": "all-orders", "maxRows": 1,
	}))
	if query["kind"] != "query" || query["rowCount"] != float64(1) || query["totalRows"] != float64(3) || query["truncated"] != true {
		t.Fatalf("bounded query = %#v", query)
	}
	rows := query["rows"].([]any)
	row := rows[0].(map[string]any)
	if len(row["aliases"].([]any)) != 2 || row["values"] == nil {
		t.Fatalf("query row = %#v", row)
	}
	if amount := row["values"].(map[string]any)["amount"]; projectedInt(amount) == "" {
		t.Fatalf("query amount = %#v, want typed integer", amount)
	}
	parameterizedQuery := toolObject(t, callTool(t, ctx, clientSession, "query", map[string]any{
		"name": "orders-at-amount", "args": map[string]any{"amount": modifiedAmount},
	}))
	if parameterizedQuery["totalRows"] != float64(1) || parameterizedQuery["truncated"] != false {
		t.Fatalf("parameterized query = %#v", parameterizedQuery)
	}

	retracted := toolObject(t, callTool(t, ctx, clientSession, "retract", map[string]any{"factId": assertedID}))
	if retracted["kind"] != "retract" || retracted["status"] != "removed" {
		t.Fatalf("retract output = %#v", retracted)
	}

	whatIfOverFirings := callTool(t, ctx, clientSession, "what_if", map[string]any{"maxFirings": 3})
	if !whatIfOverFirings.IsError || !strings.Contains(toolText(whatIfOverFirings), "exceeds server ceiling 2") {
		t.Fatalf("over-limit what-if firings = error %t content %q", whatIfOverFirings.IsError, toolText(whatIfOverFirings))
	}
	whatIfOverOperations := callTool(t, ctx, clientSession, "what_if", map[string]any{
		"operations": []any{
			map[string]any{"kind": "retract", "factId": factID},
			map[string]any{"kind": "retract", "factId": factID},
			map[string]any{"kind": "retract", "factId": factID},
			map[string]any{"kind": "retract", "factId": factID},
		},
	})
	if !whatIfOverOperations.IsError || !strings.Contains(toolText(whatIfOverOperations), "operation count 4 exceeds server ceiling 3") {
		t.Fatalf("over-limit what-if operations = error %t content %q", whatIfOverOperations.IsError, toolText(whatIfOverOperations))
	}
	failedWhatIf := callTool(t, ctx, clientSession, "what_if", map[string]any{
		"operations": []any{
			map[string]any{"kind": "assert", "template": "order", "fields": map[string]any{"id": "O-FAIL", "amount": 99}},
			map[string]any{"kind": "unsupported"},
		},
	})
	if !failedWhatIf.IsError || !strings.Contains(toolText(failedWhatIf), "operation 1") {
		t.Fatalf("failed what-if = error %t content %q", failedWhatIf.IsError, toolText(failedWhatIf))
	}
	if failedSnapshot := toolObject(t, callTool(t, ctx, clientSession, "snapshot", nil)); len(failedSnapshot["facts"].([]any)) != 2 {
		t.Fatalf("failed what-if mutated base: %#v", failedSnapshot)
	}
	whatIf := toolObject(t, callTool(t, ctx, clientSession, "what_if", map[string]any{
		"explain": true,
		"operations": []any{
			map[string]any{"kind": "assert", "template": "order", "fields": map[string]any{"id": "O-400", "amount": map[string]any{"kind": "int", "int": "50"}}},
			map[string]any{"kind": "modify", "factId": factID, "set": map[string]any{"amount": map[string]any{"kind": "int", "int": "5"}}},
			map[string]any{"kind": "retract", "factId": thirdID},
		},
	}))
	if whatIf["gessExplainSchema"] != float64(1) || whatIf["kind"] != "whatif" {
		t.Fatalf("what-if envelope = %#v", whatIf)
	}
	whatIfRun := whatIf["run"].(map[string]any)
	if whatIfRun["status"] != "completed" || whatIfRun["fired"] != float64(1) {
		t.Fatalf("what-if run = %#v", whatIfRun)
	}
	diff := whatIf["diff"].(map[string]any)
	if len(diff["added"].([]any)) != 1 || len(diff["modified"].([]any)) != 1 || len(diff["retracted"].([]any)) != 1 {
		t.Fatalf("what-if diff = %#v", diff)
	}
	if derivations := whatIf["derivations"].([]any); len(derivations) != 1 {
		t.Fatalf("what-if derivations = %#v, want one", derivations)
	}
	baseAfterWhatIf := toolObject(t, callTool(t, ctx, clientSession, "snapshot", nil))
	baseFacts := baseAfterWhatIf["facts"].([]any)
	if len(baseFacts) != 2 {
		t.Fatalf("base facts after what-if = %d, want 2", len(baseFacts))
	}
	baseByID := make(map[string]map[string]any, len(baseFacts))
	for _, raw := range baseFacts {
		projected := raw.(map[string]any)
		baseByID[projected["id"].(string)] = projected
	}
	if projectedInt(baseByID[factID]["fields"].(map[string]any)["amount"]) != "25" || baseByID[thirdID] == nil {
		t.Fatalf("what-if mutated base session: %#v", baseByID)
	}

	testConcurrentInspectionCalls(t, ctx, clientSession)
}

func TestMCPLoadRejectsMissingRegistrations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := `(deftemplate item (slot id (type STRING) (required TRUE)))
(deffacts seed (item (id "I-1")))
(defrule notify (item (id ?id)) => (call notify-host ?id))`
	writeTestFile(t, filepath.Join(root, "missing.gess"), source)
	service, err := New(Config{RulesetRoot: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer service.Close()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer serverSession.Close()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer clientSession.Close()

	result := callTool(t, ctx, clientSession, "load", map[string]any{"path": "missing.gess"})
	if !result.IsError || !strings.Contains(toolText(result), "calls: notify-host") {
		t.Fatalf("missing registration result = error %t content %q", result.IsError, toolText(result))
	}
}

func callTool(t *testing.T, ctx context.Context, client *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func toolObject(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool returned error: %s", toolText(result))
	}
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %T %#v, want object", result.StructuredContent, result.StructuredContent)
	}
	return out
}

func toolText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if text, ok := result.Content[0].(*mcp.TextContent); ok {
		return text.Text
	}
	return ""
}

func projectedInt(value any) string {
	projected, ok := value.(map[string]any)
	if !ok || projected["kind"] != "int" {
		return ""
	}
	encoded, _ := projected["int"].(string)
	return encoded
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func testConcurrentInspectionCalls(t *testing.T, ctx context.Context, client *mcp.ClientSession) {
	t.Helper()
	const calls = 24
	errors := make(chan error, calls)
	for i := range calls {
		go func() {
			name := []string{"snapshot", "agenda", "diagnostics"}[i%3]
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
			if err == nil && result.IsError {
				err = fmt.Errorf("%s: %s", name, toolText(result))
			}
			errors <- err
		}()
	}
	for range calls {
		if err := <-errors; err != nil {
			t.Errorf("concurrent inspection: %v", err)
		}
	}
}
