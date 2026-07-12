package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestMCPReadOnlyInterrogation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "rules.gess"), testRuleset)
	service, err := New(Config{RulesetRoot: root, ExplainLogMaxEntries: 32})
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
	if len(tools.Tools) != 6 {
		t.Fatalf("tool count = %d, want 6", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Name == "load" {
			if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || tool.Annotations.IdempotentHint {
				t.Fatalf("load safety annotations = %#v", tool.Annotations)
			}
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %q is missing read-only annotation", tool.Name)
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

	explain := toolObject(t, callTool(t, ctx, clientSession, "explain", map[string]any{"factId": factID}))
	if explain["gessExplainSchema"] != float64(1) || explain["kind"] != "derivation" {
		t.Fatalf("explain envelope = %#v", explain)
	}

	whyNot := toolObject(t, callTool(t, ctx, clientSession, "why_not", map[string]any{"rule": "route-order"}))
	if whyNot["gessExplainSchema"] != float64(1) || whyNot["kind"] != "whynot" {
		t.Fatalf("why-not envelope = %#v", whyNot)
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
