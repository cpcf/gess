package server

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) assert(ctx context.Context, _ *mcp.CallToolRequest, input assertInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	templateName := strings.TrimSpace(input.Template)
	if templateName == "" {
		return nil, nil, fmt.Errorf("template is required")
	}
	template, ok := s.ruleset.Template(templateName)
	if !ok {
		return nil, nil, fmt.Errorf("unknown template %q", templateName)
	}
	fields, err := rules.NewFields(normalizeJSONObject(input.Fields))
	if err != nil {
		return nil, nil, err
	}
	result, err := s.session.Assert(ctx, template.Key(), fields)
	if err != nil {
		return mcpToolError(err), projectAssertResult(result), nil
	}
	return nil, projectAssertResult(result), nil
}

func (s *Server) modify(ctx context.Context, _ *mcp.CallToolRequest, input modifyInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	if len(input.Set) == 0 && len(input.Unset) == 0 {
		return nil, nil, fmt.Errorf("modify requires at least one set or unset field")
	}
	id, err := parseFactID(input.FactID)
	if err != nil {
		return nil, nil, err
	}
	set, err := rules.NewFields(normalizeJSONObject(input.Set))
	if err != nil {
		return nil, nil, err
	}
	unset := make([]string, len(input.Unset))
	for i, field := range input.Unset {
		unset[i] = strings.TrimSpace(field)
		if unset[i] == "" {
			return nil, nil, fmt.Errorf("unset field names must not be empty")
		}
	}
	result, err := s.session.Modify(ctx, id, rules.FactPatch{Set: set, Unset: unset})
	if err != nil {
		return mcpToolError(err), projectModifyResult(result), nil
	}
	return nil, projectModifyResult(result), nil
}

func (s *Server) retract(ctx context.Context, _ *mcp.CallToolRequest, input retractInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	id, err := parseFactID(input.FactID)
	if err != nil {
		return nil, nil, err
	}
	result, err := s.session.Retract(ctx, id)
	if err != nil {
		return mcpToolError(err), projectRetractResult(result), nil
	}
	return nil, projectRetractResult(result), nil
}

func (s *Server) run(ctx context.Context, _ *mcp.CallToolRequest, input runInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	limit, err := boundedRequestLimit("max firings", input.MaxFirings, s.maxFirings)
	if err != nil {
		return nil, nil, err
	}
	result, err := s.session.Run(ctx, sess.WithMaxFirings(limit))
	if err != nil {
		return mcpToolError(err), projectRunResult(result, limit), nil
	}
	return nil, projectRunResult(result, limit), nil
}

func projectRunResult(result sess.RunResult, limit int) map[string]any {
	return map[string]any{
		"gessMcpSchema": mcpToolSchemaVersion,
		"kind":          "run",
		"runId":         result.RunID.String(),
		"status":        string(result.Status),
		"fired":         result.Fired,
		"maxFirings":    limit,
	}
}

func (s *Server) query(ctx context.Context, _ *mcp.CallToolRequest, input queryInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, nil, fmt.Errorf("query name is required")
	}
	limit, err := boundedRequestLimit("max rows", input.MaxRows, s.maxQueryRows)
	if err != nil {
		return nil, nil, err
	}
	args := make(sess.QueryArgs, len(input.Args))
	for key, value := range input.Args {
		args[key] = normalizeJSONValue(value)
	}
	rows, err := s.session.QueryAll(ctx, name, args)
	if err != nil {
		return nil, nil, err
	}
	return nil, projectQueryRows(name, rows, limit), nil
}

func (s *Server) whatIf(ctx context.Context, _ *mcp.CallToolRequest, input whatIfInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSession(); err != nil {
		return nil, nil, err
	}
	if len(input.Operations) > s.maxWhatIfOperations {
		return nil, nil, fmt.Errorf("what-if operation count %d exceeds server ceiling %d", len(input.Operations), s.maxWhatIfOperations)
	}
	limit, err := boundedRequestLimit("max firings", input.MaxFirings, s.maxFirings)
	if err != nil {
		return nil, nil, err
	}
	scenario := func(ctx context.Context, fork *sess.Session) error {
		for i, operation := range input.Operations {
			if err := s.applyWhatIfOperation(ctx, fork, operation); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		}
		return nil
	}
	options := []sess.WhatIfOption{sess.WithWhatIfMaxFirings(limit)}
	if input.Explain {
		options = append(options, sess.WithWhatIfExplain(sess.WithExplainLogMaxEntries(s.explainLogMaxEntries)))
	}
	report, err := s.session.WhatIf(ctx, scenario, options...)
	if err != nil {
		return nil, nil, err
	}
	output, err := jsonObject(report)
	return nil, output, err
}

func (s *Server) applyWhatIfOperation(ctx context.Context, fork *sess.Session, operation whatIfOperation) error {
	switch strings.TrimSpace(operation.Kind) {
	case "assert":
		templateName := strings.TrimSpace(operation.Template)
		if templateName == "" {
			return fmt.Errorf("assert template is required")
		}
		template, ok := s.ruleset.Template(templateName)
		if !ok {
			return fmt.Errorf("unknown template %q", templateName)
		}
		fields, err := rules.NewFields(normalizeJSONObject(operation.Fields))
		if err != nil {
			return err
		}
		_, err = fork.Assert(ctx, template.Key(), fields)
		return err
	case "modify":
		if len(operation.Set) == 0 && len(operation.Unset) == 0 {
			return fmt.Errorf("modify requires at least one set or unset field")
		}
		id, err := parseFactID(operation.FactID)
		if err != nil {
			return err
		}
		set, err := rules.NewFields(normalizeJSONObject(operation.Set))
		if err != nil {
			return err
		}
		unset := make([]string, len(operation.Unset))
		for i, field := range operation.Unset {
			unset[i] = strings.TrimSpace(field)
			if unset[i] == "" {
				return fmt.Errorf("unset field names must not be empty")
			}
		}
		_, err = fork.Modify(ctx, id, rules.FactPatch{Set: set, Unset: unset})
		return err
	case "retract":
		id, err := parseFactID(operation.FactID)
		if err != nil {
			return err
		}
		_, err = fork.Retract(ctx, id)
		return err
	default:
		return fmt.Errorf("unsupported kind %q", operation.Kind)
	}
}

func boundedRequestLimit(name string, requested, ceiling int) (int, error) {
	if requested == 0 {
		return ceiling, nil
	}
	if requested < 1 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	if requested > ceiling {
		return 0, fmt.Errorf("%s %d exceeds server ceiling %d", name, requested, ceiling)
	}
	return requested, nil
}

func mcpToolError(err error) *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	result.SetError(err)
	return result
}

func normalizeJSONObject(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = normalizeJSONValue(item)
	}
	return out
}

func normalizeJSONValue(value any) any {
	switch value := value.(type) {
	case float64:
		if !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && value >= math.MinInt64 && value < math.MaxInt64 {
			return int64(value)
		}
		return value
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = normalizeJSONValue(item)
		}
		return out
	case map[string]any:
		return normalizeJSONObject(value)
	default:
		return value
	}
}
