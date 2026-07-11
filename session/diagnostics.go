package session

import (
	"context"
	"maps"

	"github.com/cpcf/gess/internal/engine"
)

// DiagnosticsSchemaVersion is the current machine-readable diagnostics
// document version. It changes only for breaking schema changes.
const DiagnosticsSchemaVersion = engine.DiagnosticsSchemaVersion

// DiagnosticsOption configures a diagnostics report.
type DiagnosticsOption = engine.DiagnosticsOption

// WithDiagnosticsFacts includes detached fact details in a diagnostics report.
// Fact details are omitted by default so inspection does not clone the working
// memory payloads.
func WithDiagnosticsFacts() DiagnosticsOption {
	return engine.WithDiagnosticsFacts()
}

// DiagnosticsReport is a versioned, machine-readable view of session runtime
// state. The JSON representation is the stable diagnostics contract.
type DiagnosticsReport struct {
	Schema           int                         `json:"gessDiagnosticsSchema"`
	Session          DiagnosticsSession          `json:"session"`
	Graph            DiagnosticsGraph            `json:"graph"`
	Memory           []DiagnosticsMemoryOwner    `json:"memory"`
	Agenda           DiagnosticsAgenda           `json:"agenda"`
	Terminals        DiagnosticsTerminals        `json:"terminals"`
	Aggregates       DiagnosticsAggregates       `json:"aggregates"`
	Queries          DiagnosticsQueries          `json:"queries"`
	TruthMaintenance DiagnosticsTruthMaintenance `json:"truthMaintenance"`
	Backchain        DiagnosticsBackchain        `json:"backchain"`
	Facts            []DiagnosticsFact           `json:"facts,omitempty"`
}

// DiagnosticsSession identifies the inspected session and working-memory epoch.
type DiagnosticsSession struct {
	SessionID  SessionID  `json:"sessionId"`
	RulesetID  RulesetID  `json:"rulesetId"`
	Generation Generation `json:"generation"`
	FactCount  int        `json:"factCount"`
}

// DiagnosticsGraph summarizes the immutable compiled Rete graph.
type DiagnosticsGraph struct {
	Runtime        string `json:"runtime"`
	AlphaNodes     int    `json:"alphaNodes"`
	BetaNodes      int    `json:"betaNodes"`
	UnionNodes     int    `json:"unionNodes"`
	AggregateNodes int    `json:"aggregateNodes"`
	RuleTerminals  int    `json:"ruleTerminals"`
	QueryTerminals int    `json:"queryTerminals"`
	RuleBranches   int    `json:"ruleBranches"`
	QueryBranches  int    `json:"queryBranches"`
}

// DiagnosticsMemoryOwner estimates retained state for one runtime owner.
type DiagnosticsMemoryOwner struct {
	Owner      string `json:"owner"`
	Rows       uint64 `json:"rows"`
	Buckets    uint64 `json:"buckets"`
	Indexes    uint64 `json:"indexes"`
	Tombstones uint64 `json:"tombstones"`
	Bytes      uint64 `json:"bytes"`
	HighWater  uint64 `json:"highWater"`
}

// DiagnosticsAgenda summarizes pending work, readiness, strategy, and focus.
type DiagnosticsAgenda struct {
	Pending      int          `json:"pending"`
	Ready        bool         `json:"ready"`
	Dirty        bool         `json:"dirty"`
	Strategy     string       `json:"strategy"`
	CurrentFocus ModuleName   `json:"currentFocus"`
	FocusStack   []ModuleName `json:"focusStack"`
}

// DiagnosticsTerminals summarizes rule and query terminal state.
type DiagnosticsTerminals struct {
	RuleNodes    int `json:"ruleNodes"`
	QueryNodes   int `json:"queryNodes"`
	QueryRows    int `json:"queryRows"`
	MaxQueryRows int `json:"maxQueryRows"`
}

// DiagnosticsAggregates summarizes compiled and retained aggregate state.
type DiagnosticsAggregates struct {
	Nodes     int    `json:"nodes"`
	Rows      uint64 `json:"rows"`
	Buckets   uint64 `json:"buckets"`
	Indexes   uint64 `json:"indexes"`
	Bytes     uint64 `json:"bytes"`
	HighWater uint64 `json:"highWater"`
}

// DiagnosticsQueries summarizes compiled queries and transient query state.
type DiagnosticsQueries struct {
	Definitions  int  `json:"definitions"`
	ActiveProof  bool `json:"activeProof"`
	TerminalRows int  `json:"terminalRows"`
}

// DiagnosticsTruthMaintenance summarizes logical support state and activity.
type DiagnosticsTruthMaintenance struct {
	LogicalFacts          int `json:"logicalFacts"`
	StatedAndLogicalFacts int `json:"statedAndLogicalFacts"`
	SupportEdges          int `json:"supportEdges"`
	SupportEdgesAdded     int `json:"supportEdgesAdded"`
	SupportEdgesRemoved   int `json:"supportEdgesRemoved"`
	CascadeRetractions    int `json:"cascadeRetractions"`
}

// DiagnosticsBackchain summarizes demand facts, support, and cascade activity.
type DiagnosticsBackchain struct {
	ActiveDemands     int                 `json:"activeDemands"`
	DemandsByTemplate map[TemplateKey]int `json:"demandsByTemplate"`
	SupportRows       uint64              `json:"supportRows"`
	CascadeLimit      int                 `json:"cascadeLimit"`
	Cascades          int                 `json:"cascades"`
	CascadeSteps      int                 `json:"cascadeSteps"`
	CascadeLengthMax  int                 `json:"cascadeLengthMax"`
	CascadeLimitHits  int                 `json:"cascadeLimitHits"`
}

// DiagnosticsFact is an opt-in detached fact projection.
type DiagnosticsFact struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name,omitempty"`
	TemplateKey   TemplateKey              `json:"templateKey,omitempty"`
	Version       FactVersion              `json:"version"`
	Recency       Recency                  `json:"recency"`
	Generation    Generation               `json:"generation"`
	Support       FactSupportState         `json:"support"`
	Fields        map[string]any           `json:"fields"`
	FieldPresence map[string]FieldPresence `json:"fieldPresence,omitempty"`
}

// Diagnostics returns a point-in-time diagnostics report. By default it
// reports counts and retained-memory estimates without cloning fact payloads.
func (s *Session) Diagnostics(ctx context.Context, opts ...DiagnosticsOption) (DiagnosticsReport, error) {
	report, err := s.engineSession().Diagnostics(ctx, opts...)
	if err != nil {
		return DiagnosticsReport{}, err
	}
	return wrapDiagnosticsReport(report), nil
}

func wrapDiagnosticsReport(report engine.DiagnosticsReport) DiagnosticsReport {
	out := DiagnosticsReport{
		Schema: report.Schema,
		Session: DiagnosticsSession{
			SessionID:  report.Session.SessionID,
			RulesetID:  report.Session.RulesetID,
			Generation: report.Session.Generation,
			FactCount:  report.Session.FactCount,
		},
		Graph: DiagnosticsGraph(report.Graph),
		Agenda: DiagnosticsAgenda{
			Pending:      report.Agenda.Pending,
			Ready:        report.Agenda.Ready,
			Dirty:        report.Agenda.Dirty,
			Strategy:     report.Agenda.Strategy,
			CurrentFocus: report.Agenda.CurrentFocus,
			FocusStack:   append([]ModuleName(nil), report.Agenda.FocusStack...),
		},
		Terminals:        DiagnosticsTerminals(report.Terminals),
		Aggregates:       DiagnosticsAggregates(report.Aggregates),
		Queries:          DiagnosticsQueries(report.Queries),
		TruthMaintenance: DiagnosticsTruthMaintenance(report.TruthMaintenance),
		Backchain: DiagnosticsBackchain{
			ActiveDemands:     report.Backchain.ActiveDemands,
			DemandsByTemplate: cloneDiagnosticsDemandCounts(report.Backchain.DemandsByTemplate),
			SupportRows:       report.Backchain.SupportRows,
			CascadeLimit:      report.Backchain.CascadeLimit,
			Cascades:          report.Backchain.Cascades,
			CascadeSteps:      report.Backchain.CascadeSteps,
			CascadeLengthMax:  report.Backchain.CascadeLengthMax,
			CascadeLimitHits:  report.Backchain.CascadeLimitHits,
		},
	}
	out.Memory = make([]DiagnosticsMemoryOwner, len(report.Memory))
	for i, owner := range report.Memory {
		out.Memory[i] = DiagnosticsMemoryOwner(owner)
	}
	if report.Facts != nil {
		out.Facts = make([]DiagnosticsFact, len(report.Facts))
		for i, fact := range report.Facts {
			out.Facts[i] = DiagnosticsFact{
				ID:            fact.ID,
				Name:          fact.Name,
				TemplateKey:   fact.TemplateKey,
				Version:       fact.Version,
				Recency:       fact.Recency,
				Generation:    fact.Generation,
				Support:       fact.Support,
				Fields:        cloneDiagnosticsFields(fact.Fields),
				FieldPresence: cloneDiagnosticsPresence(fact.FieldPresence),
			}
		}
	}
	return out
}

func cloneDiagnosticsDemandCounts(in map[engine.TemplateKey]int) map[TemplateKey]int {
	out := make(map[TemplateKey]int, len(in))
	maps.Copy(out, in)
	return out
}

func cloneDiagnosticsFields(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func cloneDiagnosticsPresence(in map[string]engine.FieldPresence) map[string]FieldPresence {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]FieldPresence, len(in))
	maps.Copy(out, in)
	return out
}
