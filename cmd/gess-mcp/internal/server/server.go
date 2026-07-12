package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultExplainLogMaxEntries = 4096
	mcpToolSchemaVersion        = 1
)

type Config struct {
	RulesetRoot          string
	ExplainLogMaxEntries int
}

type Server struct {
	mu                   sync.Mutex
	rulesetRoot          string
	explainLogMaxEntries int
	ruleset              *rules.Ruleset
	session              *sess.Session
	mcp                  *mcp.Server
}

type loadInput struct {
	Path string `json:"path" jsonschema:"path to a vetted .gess file within the configured ruleset root"`
}

type explainInput struct {
	FactID string `json:"factId" jsonschema:"fact identity returned by snapshot, for example fact:g1:3"`
}

type whyNotInput struct {
	Rule string `json:"rule" jsonschema:"compiled rule name to diagnose"`
}

func New(config Config) (*Server, error) {
	root, err := canonicalRulesetRoot(config.RulesetRoot)
	if err != nil {
		return nil, err
	}
	if config.ExplainLogMaxEntries == 0 {
		config.ExplainLogMaxEntries = defaultExplainLogMaxEntries
	}
	if config.ExplainLogMaxEntries < 1 {
		return nil, fmt.Errorf("explain log max entries must be positive")
	}
	s := &Server{
		rulesetRoot:          root,
		explainLogMaxEntries: config.ExplainLogMaxEntries,
	}
	s.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "gess-mcp", Version: "0.1.0"},
		&mcp.ServerOptions{
			Instructions: "Load one vetted .gess ruleset, then inspect its live Gess session. Paths are confined to the configured ruleset root.",
			Capabilities: &mcp.ServerCapabilities{},
		},
	)
	s.registerTools()
	return s, nil
}

func (s *Server) MCP() *mcp.Server {
	if s == nil {
		return nil
	}
	return s.mcp
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil
	}
	err := s.session.Close()
	s.session = nil
	s.ruleset = nil
	return err
}

func (s *Server) registerTools() {
	closedWorld := false
	destructive := true
	mcp.AddTool[loadInput, any](s.mcp, &mcp.Tool{
		Name:        "load",
		Title:       "Load Gess ruleset",
		Description: "Load a vetted .gess file inside the configured ruleset root and replace the process-owned session.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Load Gess ruleset",
			DestructiveHint: &destructive,
			OpenWorldHint:   &closedWorld,
		},
	}, s.load)
	mcp.AddTool[struct{}, any](s.mcp, readOnlyTool("snapshot", "Snapshot working memory", "Return every live fact in deterministic insertion order."), s.snapshot)
	mcp.AddTool[struct{}, any](s.mcp, readOnlyTool("agenda", "Inspect agenda", "Return pending activations in exact firing order."), s.agenda)
	mcp.AddTool[struct{}, any](s.mcp, readOnlyTool("diagnostics", "Inspect runtime diagnostics", "Return the versioned Gess runtime diagnostics document."), s.diagnostics)
	mcp.AddTool[explainInput, any](s.mcp, readOnlyTool("explain", "Explain a fact", "Return the versioned derivation document for one fact identity."), s.explain)
	mcp.AddTool[whyNotInput, any](s.mcp, readOnlyTool("why_not", "Diagnose a missing activation", "Return the versioned WhyNot document for one compiled rule."), s.whyNot)
}

func readOnlyTool(name, title, description string) *mcp.Tool {
	closedWorld := false
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:          title,
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  &closedWorld,
		},
	}
}

func (s *Server) load(ctx context.Context, _ *mcp.CallToolRequest, input loadInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, relative, err := resolveRulesetPath(s.rulesetRoot, input.Path)
	if err != nil {
		return nil, nil, err
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read ruleset: %w", err)
	}
	doc, err := dsl.Parse(relative, source)
	if err != nil {
		return nil, nil, err
	}
	registry := dsl.Registry{}
	missing := doc.MissingRegistrations(registry)
	if len(missing.Actions) > 0 || len(missing.Calls) > 0 {
		return nil, nil, missingRegistrationsError(missing)
	}
	workspace := sess.NewWorkspace()
	if err := dsl.Load(ctx, workspace, doc, registry); err != nil {
		return nil, nil, err
	}
	ruleset, err := rules.Compile(ctx, workspace)
	if err != nil {
		return nil, nil, err
	}
	next, err := sess.New(
		ruleset,
		sess.WithInitialFacts(dsl.InitialFacts(doc)...),
		sess.WithExplainLog(sess.WithExplainLogMaxEntries(s.explainLogMaxEntries)),
	)
	if err != nil {
		return nil, nil, err
	}
	if s.session != nil {
		if err := s.session.Close(); err != nil {
			_ = next.Close()
			return nil, nil, fmt.Errorf("close previous session: %w", err)
		}
	}
	s.session = next
	s.ruleset = ruleset
	return nil, map[string]any{
		"gessMcpSchema": mcpToolSchemaVersion,
		"kind":          "load",
		"status":        "loaded",
		"path":          filepath.ToSlash(relative),
		"rulesetId":     ruleset.ID().String(),
		"templates":     len(ruleset.Templates()),
		"rules":         len(ruleset.Rules()),
		"queries":       len(ruleset.Queries()),
		"initialFacts":  len(dsl.InitialFacts(doc)),
	}, nil
}

func (s *Server) snapshot(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	snapshot, err := s.session.Snapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nil, projectSnapshot(snapshot), nil
}

func (s *Server) agenda(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	agenda, err := s.session.Agenda(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nil, projectAgenda(agenda), nil
}

func (s *Server) diagnostics(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	report, err := s.session.Diagnostics(ctx)
	if err != nil {
		return nil, nil, err
	}
	output, err := jsonObject(report)
	return nil, output, err
}

func (s *Server) explain(ctx context.Context, _ *mcp.CallToolRequest, input explainInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	id, err := parseFactID(input.FactID)
	if err != nil {
		return nil, nil, err
	}
	report, err := s.session.Explain(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	output, err := jsonObject(report)
	return nil, output, err
}

func (s *Server) whyNot(ctx context.Context, _ *mcp.CallToolRequest, input whyNotInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	rule := strings.TrimSpace(input.Rule)
	if rule == "" {
		return nil, nil, fmt.Errorf("rule is required")
	}
	report, err := s.session.WhyNot(ctx, rule)
	if err != nil {
		return nil, nil, err
	}
	output, err := jsonObject(report)
	return nil, output, err
}

func (s *Server) requireSession() error {
	if s.session == nil {
		return errors.New("no ruleset loaded; call load first")
	}
	return nil
}

func canonicalRulesetRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("ruleset root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve ruleset root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve ruleset root symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat ruleset root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("ruleset root is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func resolveRulesetPath(root, requested string) (resolved string, relative string, err error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", "", fmt.Errorf("path is required")
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve ruleset path: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("resolve ruleset path symlinks: %w", err)
	}
	relative, err = filepath.Rel(root, resolved)
	if err != nil {
		return "", "", fmt.Errorf("compare ruleset path to root: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("ruleset path is outside the configured root")
	}
	if !strings.EqualFold(filepath.Ext(resolved), ".gess") {
		return "", "", fmt.Errorf("ruleset path must name a .gess file")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("stat ruleset path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("ruleset path is not a regular file")
	}
	return resolved, relative, nil
}

func missingRegistrationsError(missing dsl.MissingRegistrations) error {
	parts := make([]string, 0, 2)
	if len(missing.Actions) > 0 {
		parts = append(parts, "actions: "+strings.Join(missing.Actions, ", "))
	}
	if len(missing.Calls) > 0 {
		parts = append(parts, "calls: "+strings.Join(missing.Calls, ", "))
	}
	return fmt.Errorf("ruleset requires unavailable host registrations (%s)", strings.Join(parts, "; "))
}

func parseFactID(raw string) (rules.FactID, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 || parts[0] != "fact" || !strings.HasPrefix(parts[1], "g") {
		return rules.FactID{}, fmt.Errorf("invalid fact ID %q", raw)
	}
	generation, err := strconv.ParseUint(strings.TrimPrefix(parts[1], "g"), 10, 64)
	if err != nil || generation == 0 {
		return rules.FactID{}, fmt.Errorf("invalid fact ID %q", raw)
	}
	sequence, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || sequence == 0 {
		return rules.FactID{}, fmt.Errorf("invalid fact ID %q", raw)
	}
	return rules.NewFactID(rules.Generation(generation), sequence), nil
}

func jsonObject(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	out := make(map[string]any)
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
