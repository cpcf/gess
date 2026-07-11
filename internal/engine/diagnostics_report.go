package engine

import "context"

// DiagnosticsSchemaVersion is the current machine-readable diagnostics
// document version. It changes only for breaking schema changes.
const DiagnosticsSchemaVersion = 1

// DiagnosticsOption configures a diagnostics report.
type DiagnosticsOption func(*diagnosticsConfig)

type diagnosticsConfig struct {
	includeFacts bool
}

// WithDiagnosticsFacts includes detached fact details in a diagnostics report.
// Fact details are omitted by default so inspection does not clone the working
// memory payloads.
func WithDiagnosticsFacts() DiagnosticsOption {
	return func(cfg *diagnosticsConfig) {
		cfg.includeFacts = true
	}
}

// DiagnosticsReport is a versioned, machine-readable view of session runtime
// state. It contains stable ownership concepts rather than internal graph or
// memory structs.
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
	if s == nil || s.closed {
		return DiagnosticsReport{}, ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return DiagnosticsReport{}, err
	}
	if s.runGuardHeld() {
		return DiagnosticsReport{}, ErrConcurrencyMisuse
	}
	if !s.lock() {
		return DiagnosticsReport{}, ErrConcurrencyMisuse
	}
	defer s.unlock()

	cfg := diagnosticsConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return s.diagnosticsReportLocked(cfg), nil
}

func (s *Session) diagnosticsReportLocked(cfg diagnosticsConfig) DiagnosticsReport {
	workspace := s.activeFactWorkspace()
	report := DiagnosticsReport{
		Schema: DiagnosticsSchemaVersion,
		Session: DiagnosticsSession{
			SessionID:  s.id,
			RulesetID:  s.revision.ID(),
			Generation: s.factStore.generation,
			FactCount:  workspace.factCount(),
		},
		Graph: DiagnosticsGraph{Runtime: "rete"},
		Agenda: DiagnosticsAgenda{
			Ready:        s.agendaDriver.ready,
			Dirty:        s.agendaDriver.dirty,
			Strategy:     diagnosticsStrategyName(s.agendaDriver.strategy),
			CurrentFocus: s.agendaDriver.currentFocus(),
			FocusStack:   cloneModuleNames(s.agendaDriver.focusStack),
		},
		Queries:          DiagnosticsQueries{Definitions: len(s.revision.queries), ActiveProof: s.backchain.activeQueryProof != nil},
		TruthMaintenance: diagnosticsTruthMaintenance(s.tms.logicalSupportCounters),
		Backchain: DiagnosticsBackchain{
			DemandsByTemplate: make(map[TemplateKey]int),
			CascadeLimit:      s.backchain.demandLimit,
			Cascades:          s.backchain.demandCounters.Cascades,
			CascadeSteps:      s.backchain.demandCounters.Steps,
			CascadeLengthMax:  s.backchain.demandCounters.LengthMax,
			CascadeLimitHits:  s.backchain.demandCounters.LimitHits,
		},
	}
	if s.agendaDriver.agenda != nil {
		report.Agenda.Pending = s.agendaDriver.agenda.pendingActivationCount()
	}
	if s.revision != nil && s.revision.graph != nil {
		plan := s.revision.graph.inspectPlan()
		report.Graph.AlphaNodes = len(plan.AlphaNodes)
		report.Graph.BetaNodes = len(plan.BetaNodes)
		report.Graph.UnionNodes = len(s.revision.graph.unionNodes)
		report.Graph.AggregateNodes = len(plan.AggregateNodes)
		for _, terminal := range plan.TerminalNodes {
			if terminal.Kind == reteGraphTerminalQuery {
				report.Graph.QueryTerminals++
			} else {
				report.Graph.RuleTerminals++
			}
		}
		for _, branch := range plan.Branches {
			if branch.OwnerKind == reteGraphBranchOwnerQuery {
				report.Graph.QueryBranches++
			} else {
				report.Graph.RuleBranches++
			}
		}
	}
	report.Terminals.RuleNodes = report.Graph.RuleTerminals
	report.Terminals.QueryNodes = report.Graph.QueryTerminals
	report.Aggregates.Nodes = report.Graph.AggregateNodes

	runtimeDiagnostics := s.runtimeDiagnosticsLocked()
	report.Memory = make([]DiagnosticsMemoryOwner, 0, len(runtimeDiagnostics.MemoryOwners))
	for _, owner := range runtimeDiagnostics.MemoryOwners {
		row := DiagnosticsMemoryOwner(owner)
		report.Memory = append(report.Memory, row)
		switch owner.Owner {
		case runtimeMemoryOwnerAggregate:
			report.Aggregates.Rows = owner.Rows
			report.Aggregates.Buckets = owner.Buckets
			report.Aggregates.Indexes = owner.Indexes
			report.Aggregates.Bytes = owner.Bytes
			report.Aggregates.HighWater = owner.HighWater
		case runtimeMemoryOwnerBackchainDemandSupport:
			report.Backchain.SupportRows = owner.Rows
		}
	}
	if s.propagation.runtime != nil && s.propagation.runtime.graphBeta != nil {
		memory := s.propagation.runtime.graphBeta.diagnostics()
		for _, terminal := range memory.Terminals {
			if terminal.Kind != reteGraphTerminalQuery {
				continue
			}
			report.Terminals.QueryRows += terminal.Rows
			report.Terminals.MaxQueryRows = max(report.Terminals.MaxQueryRows, terminal.Rows)
		}
		report.Queries.TerminalRows = report.Terminals.QueryRows
	}

	for _, id := range s.factStore.insertionOrder {
		fact, ok := s.workingFactByID(id)
		if !ok {
			continue
		}
		template, templateOK := fact.templateForRevision(s.revision)
		if templateOK && template.backchainDemand {
			report.Backchain.ActiveDemands++
			report.Backchain.DemandsByTemplate[template.key]++
		}
		if cfg.includeFacts {
			snapshot := fact.snapshotForRevision(s.revision, s.factStore.compactSlotStore)
			report.Facts = append(report.Facts, diagnosticsFact(snapshot))
		}
	}
	return report
}

func diagnosticsStrategyName(strategy Strategy) string {
	if strategy == StrategyBreadth {
		return "breadth"
	}
	return "depth"
}

func diagnosticsTruthMaintenance(counters LogicalSupportCounters) DiagnosticsTruthMaintenance {
	return DiagnosticsTruthMaintenance{
		LogicalFacts:          counters.CurrentLogicalFacts,
		StatedAndLogicalFacts: counters.CurrentStatedAndLogicalFacts,
		SupportEdges:          counters.CurrentSupportEdges,
		SupportEdgesAdded:     counters.SupportEdgesAdded,
		SupportEdgesRemoved:   counters.SupportEdgesRemoved,
		CascadeRetractions:    counters.CascadeRetractions,
	}
}

func diagnosticsFact(fact FactSnapshot) DiagnosticsFact {
	fields := fact.Fields()
	jsonFields := make(map[string]any, len(fields))
	for name, value := range fields {
		jsonFields[name] = valueToJSON(value)
	}
	return DiagnosticsFact{
		ID:            fact.ID().String(),
		Name:          fact.Name(),
		TemplateKey:   fact.TemplateKey(),
		Version:       fact.Version(),
		Recency:       fact.Recency(),
		Generation:    fact.Generation(),
		Support:       fact.Support().State,
		Fields:        jsonFields,
		FieldPresence: fact.FieldPresenceMap(),
	}
}
