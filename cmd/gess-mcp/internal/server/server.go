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
	defaultExplainLogMaxEntries  = 4096
	defaultMaxFirings            = 10_000
	defaultMaxQueryRows          = 1_000
	defaultMaxDemandCascadeSteps = 10_000
	defaultMaxWhatIfOperations   = 100
	mcpToolSchemaVersion         = 1
)

type Config struct {
	RulesetRoot           string
	ExplainLogMaxEntries  int
	MaxFirings            int
	MaxQueryRows          int
	MaxDemandCascadeSteps int
	MaxWhatIfOperations   int
}

type Server struct {
	mu                    sync.Mutex
	rulesetRoot           string
	explainLogMaxEntries  int
	maxFirings            int
	maxQueryRows          int
	maxDemandCascadeSteps int
	maxWhatIfOperations   int
	ruleset               *rules.Ruleset
	session               *sess.Session
	mcp                   *mcp.Server
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

type assertInput struct {
	Template string         `json:"template" jsonschema:"compiled template name"`
	Fields   map[string]any `json:"fields,omitempty" jsonschema:"field values keyed by template slot name"`
}

type modifyInput struct {
	FactID string         `json:"factId" jsonschema:"fact identity returned by snapshot"`
	Set    map[string]any `json:"set,omitempty" jsonschema:"field values to set"`
	Unset  []string       `json:"unset,omitempty" jsonschema:"field names to restore to their defaults"`
}

type retractInput struct {
	FactID string `json:"factId" jsonschema:"fact identity returned by snapshot"`
}

type runInput struct {
	MaxFirings int `json:"maxFirings,omitempty" jsonschema:"positive firing limit no greater than the server ceiling; omit to use the ceiling"`
}

type queryInput struct {
	Name    string         `json:"name" jsonschema:"compiled query name"`
	Args    map[string]any `json:"args,omitempty" jsonschema:"query arguments keyed by parameter name without the leading question mark"`
	MaxRows int            `json:"maxRows,omitempty" jsonschema:"positive output row limit no greater than the server ceiling; omit to use the ceiling"`
}

type whatIfInput struct {
	Operations []whatIfOperation `json:"operations,omitempty" jsonschema:"ordered hypothetical mutations applied to the fork before its automatic run"`
	MaxFirings int               `json:"maxFirings,omitempty" jsonschema:"positive firing limit no greater than the server ceiling; omit to use the ceiling"`
	Explain    bool              `json:"explain,omitempty" jsonschema:"include bounded derivations for added facts"`
}

type whatIfOperation struct {
	Kind     string         `json:"kind" jsonschema:"operation kind: assert, modify, or retract"`
	Template string         `json:"template,omitempty" jsonschema:"template name for assert"`
	FactID   string         `json:"factId,omitempty" jsonschema:"fact identity for modify or retract"`
	Fields   map[string]any `json:"fields,omitempty" jsonschema:"field values for assert"`
	Set      map[string]any `json:"set,omitempty" jsonschema:"field values to set for modify"`
	Unset    []string       `json:"unset,omitempty" jsonschema:"field names to restore to defaults for modify"`
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
	if config.MaxFirings == 0 {
		config.MaxFirings = defaultMaxFirings
	}
	if config.MaxQueryRows == 0 {
		config.MaxQueryRows = defaultMaxQueryRows
	}
	if config.MaxDemandCascadeSteps == 0 {
		config.MaxDemandCascadeSteps = defaultMaxDemandCascadeSteps
	}
	if config.MaxWhatIfOperations == 0 {
		config.MaxWhatIfOperations = defaultMaxWhatIfOperations
	}
	if config.MaxFirings < 1 {
		return nil, fmt.Errorf("max firings must be positive")
	}
	if config.MaxQueryRows < 1 {
		return nil, fmt.Errorf("max query rows must be positive")
	}
	if config.MaxDemandCascadeSteps < 1 {
		return nil, fmt.Errorf("max demand cascade steps must be positive")
	}
	if config.MaxWhatIfOperations < 1 {
		return nil, fmt.Errorf("max what-if operations must be positive")
	}
	s := &Server{
		rulesetRoot:           root,
		explainLogMaxEntries:  config.ExplainLogMaxEntries,
		maxFirings:            config.MaxFirings,
		maxQueryRows:          config.MaxQueryRows,
		maxDemandCascadeSteps: config.MaxDemandCascadeSteps,
		maxWhatIfOperations:   config.MaxWhatIfOperations,
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
	mcp.AddTool[assertInput, any](s.mcp, statefulTool("assert", "Assert a fact", "Assert a fact into the live session.", false), s.assert)
	mcp.AddTool[modifyInput, any](s.mcp, statefulTool("modify", "Modify a fact", "Set or unset fields on one live fact.", true), s.modify)
	mcp.AddTool[retractInput, any](s.mcp, statefulTool("retract", "Retract a fact", "Remove stated support from one live fact.", true), s.retract)
	mcp.AddTool[runInput, any](s.mcp, statefulTool("run", "Run pending activations", "Fire pending activations up to the server-enforced ceiling.", false), s.run)
	mcp.AddTool[queryInput, any](s.mcp, statefulTool("query", "Run a query", "Run a compiled query and return a bounded row prefix. Backchain-reactive queries may persist proof facts.", false), s.query)
	mcp.AddTool[whatIfInput, any](s.mcp, readOnlyTool("what_if", "Run a counterfactual", "Apply bounded hypothetical mutations to a disposable fork, run it under the firing ceiling, and return the versioned WhatIf report."), s.whatIf)
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

func statefulTool(name, title, description string, idempotent bool) *mcp.Tool {
	closedWorld := false
	destructive := true
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:           title,
			DestructiveHint: &destructive,
			IdempotentHint:  idempotent,
			OpenWorldHint:   &closedWorld,
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
		sess.WithMaxDemandCascadeSteps(s.maxDemandCascadeSteps),
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
