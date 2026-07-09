package session

import (
	"context"
	"io"
	"time"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/rules"
)

type (
	// Option configures a Session at construction time. Each With*
	// function below returns an Option.
	Option = engine.SessionOption
	// InitialFact describes one fact to assert when a session is
	// constructed and to re-assert on every Reset, matching deffacts in
	// generated .gess code. TemplateKey names the declared template and
	// Fields supplies its slot values.
	InitialFact = engine.SessionInitialFact
	// BackchainDemandDiagnostics summarizes active backward-chaining
	// demand facts visible in a [Snapshot], in total and per template.
	BackchainDemandDiagnostics = engine.BackchainDemandDiagnostics
	// QueryArgs maps query parameter names, without the leading '?', to
	// the Go values passed to Query or QueryAll.
	QueryArgs = engine.QueryArgs
	// FactVersion advances each time a fact is modified; a fact's FactID
	// stays stable across modifies within a generation.
	FactVersion = rules.FactVersion
	// Recency is a session-wide counter that advances on every fact
	// mutation, used to order which pending activation fires first.
	Recency = rules.Recency
	// Generation is the working-memory reset epoch. FactIDs embed a
	// generation, so IDs from before a Reset are stale afterward.
	Generation = rules.Generation
	// FactID identifies a fact, stable across modifies within a
	// generation. IDs from an earlier generation are stale after Reset.
	FactID = rules.FactID
	// SessionID identifies a session, set with WithSessionID.
	SessionID = rules.SessionID
	// RulesetID identifies one compiled ruleset revision.
	RulesetID = rules.RulesetID
	// ModuleName identifies an agenda focus module.
	ModuleName = rules.ModuleName
	// RunID is a per-session sequence number for a Run call.
	RunID = rules.RunID
	// SourceSpan is a source location range within a .gess file, carried
	// into runtime errors and events for rulesets loaded from .gess
	// source.
	SourceSpan = rules.SourceSpan
	// RuleID identifies a rule across revisions.
	RuleID = rules.RuleID
	// RuleRevisionID identifies a concrete compiled revision of a rule.
	RuleRevisionID = rules.RuleRevisionID
	// ActivationID identifies one activation: a rule paired with the
	// specific facts that matched it.
	ActivationID = rules.ActivationID
	// SupportID identifies one logical support edge.
	SupportID = rules.SupportID
	// TemplateKey identifies a compiled template within a ruleset.
	TemplateKey = rules.TemplateKey
	// PathSpec describes a path into a structured value.
	PathSpec = rules.PathSpec
	// Fields is a fact's field values, keyed by field name.
	Fields = rules.Fields
	// FieldPresence records whether a template field was defaulted, explicit,
	// or omitted.
	FieldPresence = rules.FieldPresence
	// Value is a typed field or expression value.
	Value = rules.Value
	// ValueKind is a Value's type tag.
	ValueKind = rules.ValueKind
	// EventType discriminates the kind of change an [Event] reports.
	EventType = engine.EventType
	// EventSeverity is an [Event]'s severity: info or error.
	EventSeverity = engine.EventSeverity
	// EventListenerOption configures a listener registered with
	// [WithEventListener], such as [ForEventTypes].
	EventListenerOption = engine.EventListenerOption
	// TraceOption configures a [NewTraceListener], such as
	// [TraceWithTimestamps].
	TraceOption = engine.TraceOption
	// Strategy selects how equal-salience activations are ordered:
	// [StrategyDepth] (recency, the default) or [StrategyBreadth]
	// (creation order). Set it with [WithStrategy].
	Strategy = engine.Strategy
	// MutationKind is the kind of change recorded in a [MutationDelta]:
	// assert, modify, retract, or reset.
	MutationKind = rules.MutationKind
	// RunStatus is the outcome of a Session.Run call.
	RunStatus = rules.RunStatus
	// RunOption configures one Session.Run call, such as
	// [WithMaxFirings].
	RunOption = engine.RunOption
	// ActionFailureError wraps the error an action returned during Run,
	// identifying which activation and action produced it. Unwrap
	// returns the underlying cause; Is matches [ErrActionFailed].
	ActionFailureError = engine.ActionFailureError
	// DuplicateKey is the computed duplicate-detection key for a fact
	// under its template's duplicate policy.
	DuplicateKey = rules.DuplicateKey
	// FactSupportState classifies how a fact is supported: stated,
	// logical, both, or metadata-only.
	FactSupportState = rules.FactSupportState
	// FactSupportProvenance is a fact's support classification together
	// with [FactSnapshot].Support.
	FactSupportProvenance = rules.FactSupportProvenance
	// FactPatch is the argument to Session.Modify: fields to set or
	// overwrite, and field names to remove.
	FactPatch = rules.FactPatch
	// AssertStatus is the outcome of a Session.Assert or
	// AssertTemplateValues call.
	AssertStatus = rules.AssertStatus
	// ModifyStatus is the outcome of a Session.Modify call.
	ModifyStatus = rules.ModifyStatus
	// RetractStatus is the outcome of a Session.Retract call.
	RetractStatus = rules.RetractStatus
	// ResetStatus is the outcome of a Session.Reset call.
	ResetStatus = rules.ResetStatus
	// ApplyRulesetStatus is the outcome of a Session.ApplyRuleset call.
	ApplyRulesetStatus = rules.ApplyRulesetStatus
	// ExplainOption configures an Explain call, bounding derivation depth
	// and node count.
	ExplainOption = engine.ExplainOption
	// ExplainLogOption configures the bounded explain log installed by
	// [WithExplainLog].
	ExplainLogOption = engine.ExplainLogOption
	// WhyNotOutcome is the top-level answer in a [WhyNotReport].
	WhyNotOutcome = rules.WhyNotOutcome
	// WhyNotConditionReason classifies why a condition failed to extend a
	// partial match.
	WhyNotConditionReason = rules.WhyNotConditionReason
	// WhyNotOption configures a Session.WhyNot probe.
	WhyNotOption = engine.WhyNotOption
	// WhatIfOption configures a Session.WhatIf run.
	WhatIfOption = engine.WhatIfOption
)

// EventListener receives Events as a session's state changes. Listener errors
// are ignored: they never fail the mutation that produced the event, and later
// listeners still run.
type EventListener interface {
	HandleEvent(context.Context, Event) error
}

// EventFunc adapts a function to [EventListener].
type EventFunc func(context.Context, Event) error

func (f EventFunc) HandleEvent(ctx context.Context, event Event) error {
	return f(ctx, event)
}

// LogicalSupportEdge is one edge from the facts an activation matched to the
// logical fact it supports.
type LogicalSupportEdge struct {
	SupportID       SupportID
	FactID          FactID
	RuleID          RuleID
	RuleRevisionID  RuleRevisionID
	ActivationID    ActivationID
	Generation      Generation
	SupportingFacts []FactID
}

func wrapLogicalSupportEdge(edge engine.LogicalSupportEdge) LogicalSupportEdge {
	return LogicalSupportEdge{
		SupportID:       edge.SupportID,
		FactID:          edge.FactID,
		RuleID:          edge.RuleID,
		RuleRevisionID:  edge.RuleRevisionID,
		ActivationID:    edge.ActivationID,
		Generation:      edge.Generation,
		SupportingFacts: cloneFactIDs(edge.SupportingFacts),
	}
}

func wrapLogicalSupportEdgePtr(edge *engine.LogicalSupportEdge) *LogicalSupportEdge {
	if edge == nil {
		return nil
	}
	out := wrapLogicalSupportEdge(*edge)
	return &out
}

func wrapLogicalSupportEdges(edges []engine.LogicalSupportEdge) []LogicalSupportEdge {
	out := make([]LogicalSupportEdge, len(edges))
	for i, edge := range edges {
		out[i] = wrapLogicalSupportEdge(edge)
	}
	return out
}

func (e LogicalSupportEdge) engineEdge() engine.LogicalSupportEdge {
	return engine.LogicalSupportEdge{
		SupportID:       e.SupportID,
		FactID:          e.FactID,
		RuleID:          e.RuleID,
		RuleRevisionID:  e.RuleRevisionID,
		ActivationID:    e.ActivationID,
		Generation:      e.Generation,
		SupportingFacts: cloneFactIDs(e.SupportingFacts),
	}
}

func engineLogicalSupportEdgePtr(edge *LogicalSupportEdge) *engine.LogicalSupportEdge {
	if edge == nil {
		return nil
	}
	out := edge.engineEdge()
	return &out
}

func engineLogicalSupportEdges(edges []LogicalSupportEdge) []engine.LogicalSupportEdge {
	out := make([]engine.LogicalSupportEdge, len(edges))
	for i, edge := range edges {
		out[i] = edge.engineEdge()
	}
	return out
}

// LogicalSupportCounters holds running totals for logical support: current
// logical facts and edges, and lifetime asserts, retracts, and cascades.
type LogicalSupportCounters struct {
	CurrentLogicalFacts          int
	CurrentStatedAndLogicalFacts int
	CurrentSupportEdges          int
	LogicalFactsAsserted         int
	LogicalFactsRetracted        int
	SupportEdgesAdded            int
	SupportEdgesRemoved          int
	MetadataOnlyTransitions      int
	CascadeRetractions           int
	CascadeBreadthMax            int
	CascadeDepthMax              int
}

func wrapLogicalSupportCounters(counters engine.LogicalSupportCounters) LogicalSupportCounters {
	return LogicalSupportCounters{
		CurrentLogicalFacts:          counters.CurrentLogicalFacts,
		CurrentStatedAndLogicalFacts: counters.CurrentStatedAndLogicalFacts,
		CurrentSupportEdges:          counters.CurrentSupportEdges,
		LogicalFactsAsserted:         counters.LogicalFactsAsserted,
		LogicalFactsRetracted:        counters.LogicalFactsRetracted,
		SupportEdgesAdded:            counters.SupportEdgesAdded,
		SupportEdgesRemoved:          counters.SupportEdgesRemoved,
		MetadataOnlyTransitions:      counters.MetadataOnlyTransitions,
		CascadeRetractions:           counters.CascadeRetractions,
		CascadeBreadthMax:            counters.CascadeBreadthMax,
		CascadeDepthMax:              counters.CascadeDepthMax,
	}
}

func (c LogicalSupportCounters) engineCounters() engine.LogicalSupportCounters {
	return engine.LogicalSupportCounters{
		CurrentLogicalFacts:          c.CurrentLogicalFacts,
		CurrentStatedAndLogicalFacts: c.CurrentStatedAndLogicalFacts,
		CurrentSupportEdges:          c.CurrentSupportEdges,
		LogicalFactsAsserted:         c.LogicalFactsAsserted,
		LogicalFactsRetracted:        c.LogicalFactsRetracted,
		SupportEdgesAdded:            c.SupportEdgesAdded,
		SupportEdgesRemoved:          c.SupportEdgesRemoved,
		MetadataOnlyTransitions:      c.MetadataOnlyTransitions,
		CascadeRetractions:           c.CascadeRetractions,
		CascadeBreadthMax:            c.CascadeBreadthMax,
		CascadeDepthMax:              c.CascadeDepthMax,
	}
}

// SupportGraph is the logical support edges and counters visible in a
// [Snapshot], as of a given generation.
type SupportGraph struct {
	Generation Generation
	Edges      []LogicalSupportEdge
	Counters   LogicalSupportCounters
}

func wrapSupportGraph(graph engine.SupportGraph) SupportGraph {
	return SupportGraph{
		Generation: graph.Generation,
		Edges:      wrapLogicalSupportEdges(graph.Edges),
		Counters:   wrapLogicalSupportCounters(graph.Counters),
	}
}

func (g SupportGraph) engineGraph() engine.SupportGraph {
	return engine.SupportGraph{
		Generation: g.Generation,
		Edges:      engineLogicalSupportEdges(g.Edges),
		Counters:   g.Counters.engineCounters(),
	}
}

// BindingValue is one condition binding and the value an action saw for it, as
// reported in a [Firing].
type BindingValue struct {
	Name     string
	Value    Value
	FromFact FactID
}

func wrapBindingValue(value engine.BindingValue) BindingValue {
	return BindingValue{
		Name:     value.Name,
		Value:    value.Value,
		FromFact: value.FromFact,
	}
}

func wrapBindingValues(values []engine.BindingValue) []BindingValue {
	out := make([]BindingValue, len(values))
	for i, value := range values {
		out[i] = wrapBindingValue(value)
	}
	return out
}

func (b BindingValue) engineBinding() engine.BindingValue {
	return engine.BindingValue{
		Name:     b.Name,
		Value:    b.Value,
		FromFact: b.FromFact,
	}
}

func engineBindingValues(values []BindingValue) []engine.BindingValue {
	out := make([]engine.BindingValue, len(values))
	for i, value := range values {
		out[i] = value.engineBinding()
	}
	return out
}

// Firing identifies the rule activation that produced a fact state, with the
// concrete bound values its action evaluated against.
type Firing struct {
	RuleID          RuleID
	RuleName        string
	RuleRevisionID  RuleRevisionID
	ActivationID    ActivationID
	Generation      Generation
	Action          string
	Bindings        []BindingValue
	BindingsPartial bool
	SupportingFacts []FactID
}

func wrapFiring(firing engine.Firing) Firing {
	return Firing{
		RuleID:          firing.RuleID,
		RuleName:        firing.RuleName,
		RuleRevisionID:  firing.RuleRevisionID,
		ActivationID:    firing.ActivationID,
		Generation:      firing.Generation,
		Action:          firing.Action,
		Bindings:        wrapBindingValues(firing.Bindings),
		BindingsPartial: firing.BindingsPartial,
		SupportingFacts: cloneFactIDs(firing.SupportingFacts),
	}
}

func wrapFiringPtr(firing *engine.Firing) *Firing {
	if firing == nil {
		return nil
	}
	out := wrapFiring(*firing)
	return &out
}

func (f Firing) engineFiring() engine.Firing {
	return engine.Firing{
		RuleID:          f.RuleID,
		RuleName:        f.RuleName,
		RuleRevisionID:  f.RuleRevisionID,
		ActivationID:    f.ActivationID,
		Generation:      f.Generation,
		Action:          f.Action,
		Bindings:        engineBindingValues(f.Bindings),
		BindingsPartial: f.BindingsPartial,
		SupportingFacts: cloneFactIDs(f.SupportingFacts),
	}
}

func engineFiringPtr(firing *Firing) *engine.Firing {
	if firing == nil {
		return nil
	}
	out := firing.engineFiring()
	return &out
}

// MutationRecord is one entry in a fact's assert -> modify... lineage, as
// reconstructed from a retained explain log.
type MutationRecord struct {
	Kind          MutationKind
	Firing        *Firing
	ChangedFields []FieldChange
	Sequence      uint64
}

func wrapMutationRecord(record engine.MutationRecord) MutationRecord {
	return MutationRecord{
		Kind:          record.Kind,
		Firing:        wrapFiringPtr(record.Firing),
		ChangedFields: wrapFieldChanges(record.ChangedFields),
		Sequence:      record.Sequence,
	}
}

func wrapMutationRecords(records []engine.MutationRecord) []MutationRecord {
	out := make([]MutationRecord, len(records))
	for i, record := range records {
		out[i] = wrapMutationRecord(record)
	}
	return out
}

func (r MutationRecord) engineRecord() engine.MutationRecord {
	return engine.MutationRecord{
		Kind:          r.Kind,
		Firing:        engineFiringPtr(r.Firing),
		ChangedFields: engineFieldChanges(r.ChangedFields),
		Sequence:      r.Sequence,
	}
}

func engineMutationRecords(records []MutationRecord) []engine.MutationRecord {
	out := make([]engine.MutationRecord, len(records))
	for i, record := range records {
		out[i] = record.engineRecord()
	}
	return out
}

// Derivation is the recursive explanation of one fact, as returned by
// Snapshot.Explain and Session.Explain: its support state, the firing that
// produced it, its logical supporters, and its mutation lineage.
type Derivation struct {
	Fact       FactSnapshot
	Support    FactSupportState
	ProducedBy *Firing
	DependsOn  []Derivation
	History    []MutationRecord
	Truncated  bool
}

func wrapDerivation(derivation engine.Derivation) Derivation {
	return Derivation{
		Fact:       wrapFactSnapshot(derivation.Fact),
		Support:    derivation.Support,
		ProducedBy: wrapFiringPtr(derivation.ProducedBy),
		DependsOn:  wrapDerivations(derivation.DependsOn),
		History:    wrapMutationRecords(derivation.History),
		Truncated:  derivation.Truncated,
	}
}

func wrapDerivations(derivations []engine.Derivation) []Derivation {
	out := make([]Derivation, len(derivations))
	for i, derivation := range derivations {
		out[i] = wrapDerivation(derivation)
	}
	return out
}

func (d Derivation) engineDerivation() engine.Derivation {
	return engine.Derivation{
		Fact:       d.Fact.engineFact(),
		Support:    d.Support,
		ProducedBy: engineFiringPtr(d.ProducedBy),
		DependsOn:  engineDerivations(d.DependsOn),
		History:    engineMutationRecords(d.History),
		Truncated:  d.Truncated,
	}
}

func engineDerivations(derivations []Derivation) []engine.Derivation {
	out := make([]engine.Derivation, len(derivations))
	for i, derivation := range derivations {
		out[i] = derivation.engineDerivation()
	}
	return out
}

func (d Derivation) String() string {
	return d.engineDerivation().String()
}

// DOT renders the derivation as a Graphviz DOT graph.
func (d Derivation) DOT() string {
	return d.engineDerivation().DOT()
}

// MarshalJSON encodes the derivation as a versioned explain document.
func (d Derivation) MarshalJSON() ([]byte, error) {
	return d.engineDerivation().MarshalJSON()
}

// WhyNotReport is the structured diagnosis of why a rule has no pending
// activation, as returned by Session.WhyNot.
type WhyNotReport struct {
	RuleID         RuleID
	RuleName       string
	RuleRevisionID RuleRevisionID
	Outcome        WhyNotOutcome
	Activations    []AgendaActivation
	Branches       []WhyNotBranch
	Truncated      bool
}

func wrapWhyNotReport(report engine.WhyNotReport) WhyNotReport {
	return WhyNotReport{
		RuleID:         report.RuleID,
		RuleName:       report.RuleName,
		RuleRevisionID: report.RuleRevisionID,
		Outcome:        report.Outcome,
		Activations:    wrapAgendaActivations(report.Activations),
		Branches:       wrapWhyNotBranches(report.Branches),
		Truncated:      report.Truncated,
	}
}

func (r WhyNotReport) engineReport() engine.WhyNotReport {
	return engine.WhyNotReport{
		RuleID:         r.RuleID,
		RuleName:       r.RuleName,
		RuleRevisionID: r.RuleRevisionID,
		Outcome:        r.Outcome,
		Activations:    engineAgendaActivations(r.Activations),
		Branches:       engineWhyNotBranches(r.Branches),
		Truncated:      r.Truncated,
	}
}

func (r WhyNotReport) String() string {
	return r.engineReport().String()
}

// MarshalJSON encodes the report as a versioned explain document.
func (r WhyNotReport) MarshalJSON() ([]byte, error) {
	return r.engineReport().MarshalJSON()
}

// WhyNotBranch diagnoses one condition branch of a rule.
type WhyNotBranch struct {
	BranchID       int
	Conditions     []WhyNotCondition
	FirstFailing   int
	PartialMatches []WhyNotPartialMatch
}

func wrapWhyNotBranch(branch engine.WhyNotBranch) WhyNotBranch {
	return WhyNotBranch{
		BranchID:       branch.BranchID,
		Conditions:     wrapWhyNotConditions(branch.Conditions),
		FirstFailing:   branch.FirstFailing,
		PartialMatches: wrapWhyNotPartialMatches(branch.PartialMatches),
	}
}

func (b WhyNotBranch) engineBranch() engine.WhyNotBranch {
	return engine.WhyNotBranch{
		BranchID:       b.BranchID,
		Conditions:     engineWhyNotConditions(b.Conditions),
		FirstFailing:   b.FirstFailing,
		PartialMatches: engineWhyNotPartialMatches(b.PartialMatches),
	}
}

func wrapWhyNotBranches(branches []engine.WhyNotBranch) []WhyNotBranch {
	out := make([]WhyNotBranch, len(branches))
	for i, branch := range branches {
		out[i] = wrapWhyNotBranch(branch)
	}
	return out
}

func engineWhyNotBranches(branches []WhyNotBranch) []engine.WhyNotBranch {
	out := make([]engine.WhyNotBranch, len(branches))
	for i, branch := range branches {
		out[i] = branch.engineBranch()
	}
	return out
}

// WhyNotCondition reports one condition's status within a branch.
type WhyNotCondition struct {
	Order         int
	PlannedOrder  int
	Binding       string
	Negated       bool
	Test          bool
	Aggregate     bool
	Source        SourceSpan
	AlphaMatches  int
	Satisfied     bool
	Reason        WhyNotConditionReason
	RejectingSpan SourceSpan
	Blockers      []FactID
	BlockerCount  int
}

func wrapWhyNotCondition(condition engine.WhyNotCondition) WhyNotCondition {
	return WhyNotCondition{
		Order:         condition.Order,
		PlannedOrder:  condition.PlannedOrder,
		Binding:       condition.Binding,
		Negated:       condition.Negated,
		Test:          condition.Test,
		Aggregate:     condition.Aggregate,
		Source:        condition.Source,
		AlphaMatches:  condition.AlphaMatches,
		Satisfied:     condition.Satisfied,
		Reason:        condition.Reason,
		RejectingSpan: condition.RejectingSpan,
		Blockers:      cloneFactIDs(condition.Blockers),
		BlockerCount:  condition.BlockerCount,
	}
}

func (c WhyNotCondition) engineCondition() engine.WhyNotCondition {
	return engine.WhyNotCondition{
		Order:         c.Order,
		PlannedOrder:  c.PlannedOrder,
		Binding:       c.Binding,
		Negated:       c.Negated,
		Test:          c.Test,
		Aggregate:     c.Aggregate,
		Source:        c.Source,
		AlphaMatches:  c.AlphaMatches,
		Satisfied:     c.Satisfied,
		Reason:        c.Reason,
		RejectingSpan: c.RejectingSpan,
		Blockers:      cloneFactIDs(c.Blockers),
		BlockerCount:  c.BlockerCount,
	}
}

func wrapWhyNotConditions(conditions []engine.WhyNotCondition) []WhyNotCondition {
	out := make([]WhyNotCondition, len(conditions))
	for i, condition := range conditions {
		out[i] = wrapWhyNotCondition(condition)
	}
	return out
}

func engineWhyNotConditions(conditions []WhyNotCondition) []engine.WhyNotCondition {
	out := make([]engine.WhyNotCondition, len(conditions))
	for i, condition := range conditions {
		out[i] = condition.engineCondition()
	}
	return out
}

// WhyNotPartialMatch is one near-miss with its bound values.
type WhyNotPartialMatch struct {
	Facts          []FactID
	Bindings       []BindingValue
	Satisfied      int
	RejectedBySpan SourceSpan
}

func wrapWhyNotPartialMatch(match engine.WhyNotPartialMatch) WhyNotPartialMatch {
	return WhyNotPartialMatch{
		Facts:          cloneFactIDs(match.Facts),
		Bindings:       wrapBindingValues(match.Bindings),
		Satisfied:      match.Satisfied,
		RejectedBySpan: match.RejectedBySpan,
	}
}

func (m WhyNotPartialMatch) enginePartialMatch() engine.WhyNotPartialMatch {
	return engine.WhyNotPartialMatch{
		Facts:          cloneFactIDs(m.Facts),
		Bindings:       engineBindingValues(m.Bindings),
		Satisfied:      m.Satisfied,
		RejectedBySpan: m.RejectedBySpan,
	}
}

func wrapWhyNotPartialMatches(matches []engine.WhyNotPartialMatch) []WhyNotPartialMatch {
	out := make([]WhyNotPartialMatch, len(matches))
	for i, match := range matches {
		out[i] = wrapWhyNotPartialMatch(match)
	}
	return out
}

func engineWhyNotPartialMatches(matches []WhyNotPartialMatch) []engine.WhyNotPartialMatch {
	out := make([]engine.WhyNotPartialMatch, len(matches))
	for i, match := range matches {
		out[i] = match.enginePartialMatch()
	}
	return out
}

// WhatIfFiring is one rule firing during a what-if run.
type WhatIfFiring struct {
	RuleID         RuleID
	RuleName       string
	RuleRevisionID RuleRevisionID
	ActivationID   ActivationID
	FactIDs        []FactID
	Sequence       uint64
}

func wrapWhatIfFiring(firing engine.WhatIfFiring) WhatIfFiring {
	return WhatIfFiring{
		RuleID:         firing.RuleID,
		RuleName:       firing.RuleName,
		RuleRevisionID: firing.RuleRevisionID,
		ActivationID:   firing.ActivationID,
		FactIDs:        cloneFactIDs(firing.FactIDs),
		Sequence:       firing.Sequence,
	}
}

func wrapWhatIfFirings(firings []engine.WhatIfFiring) []WhatIfFiring {
	out := make([]WhatIfFiring, len(firings))
	for i, firing := range firings {
		out[i] = wrapWhatIfFiring(firing)
	}
	return out
}

func (f WhatIfFiring) engineFiring() engine.WhatIfFiring {
	return engine.WhatIfFiring{
		RuleID:         f.RuleID,
		RuleName:       f.RuleName,
		RuleRevisionID: f.RuleRevisionID,
		ActivationID:   f.ActivationID,
		FactIDs:        cloneFactIDs(f.FactIDs),
		Sequence:       f.Sequence,
	}
}

func engineWhatIfFirings(firings []WhatIfFiring) []engine.WhatIfFiring {
	out := make([]engine.WhatIfFiring, len(firings))
	for i, firing := range firings {
		out[i] = firing.engineFiring()
	}
	return out
}

// Event is one state change delivered to an [EventListener]: a fact mutation,
// rule activation or firing, action failure, or logical support change.
// Listeners receive a cloned copy per event.
type Event struct {
	SessionID      SessionID
	RulesetID      RulesetID
	RunID          RunID
	Sequence       uint64
	Timestamp      time.Time
	Type           EventType
	Severity       EventSeverity
	Generation     Generation
	Recency        Recency
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	ActivationID   ActivationID
	Source         SourceSpan
	ActionName     string
	ActionIndex    int
	Cause          error
	FactIDs        []FactID
	Delta          *MutationDelta
	SupportEdge    *LogicalSupportEdge
}

func wrapEvent(event engine.Event) Event {
	return Event{
		SessionID:      event.SessionID,
		RulesetID:      event.RulesetID,
		RunID:          event.RunID,
		Sequence:       event.Sequence,
		Timestamp:      event.Timestamp,
		Type:           event.Type,
		Severity:       event.Severity,
		Generation:     event.Generation,
		Recency:        event.Recency,
		RuleID:         event.RuleID,
		RuleRevisionID: event.RuleRevisionID,
		ActivationID:   event.ActivationID,
		Source:         event.Source,
		ActionName:     event.ActionName,
		ActionIndex:    event.ActionIndex,
		Cause:          event.Cause,
		FactIDs:        cloneFactIDs(event.FactIDs),
		Delta:          wrapMutationDeltaPtr(event.Delta),
		SupportEdge:    wrapLogicalSupportEdgePtr(event.SupportEdge),
	}
}

func (e Event) engineEvent() engine.Event {
	var delta *engine.MutationDelta
	if e.Delta != nil {
		value := e.Delta.engineDelta()
		delta = &value
	}
	return engine.Event{
		SessionID:      e.SessionID,
		RulesetID:      e.RulesetID,
		RunID:          e.RunID,
		Sequence:       e.Sequence,
		Timestamp:      e.Timestamp,
		Type:           e.Type,
		Severity:       e.Severity,
		Generation:     e.Generation,
		Recency:        e.Recency,
		RuleID:         e.RuleID,
		RuleRevisionID: e.RuleRevisionID,
		ActivationID:   e.ActivationID,
		Source:         e.Source,
		ActionName:     e.ActionName,
		ActionIndex:    e.ActionIndex,
		Cause:          e.Cause,
		FactIDs:        cloneFactIDs(e.FactIDs),
		Delta:          delta,
		SupportEdge:    engineLogicalSupportEdgePtr(e.SupportEdge),
	}
}

// RelatedFactIDs returns a copy of the fact IDs related to the event.
func (e Event) RelatedFactIDs() []FactID {
	return cloneFactIDs(e.FactIDs)
}

func cloneFactIDs(ids []FactID) []FactID {
	if ids == nil {
		return nil
	}
	out := make([]FactID, len(ids))
	copy(out, ids)
	return out
}

type eventListenerAdapter struct {
	listener EventListener
}

func (a eventListenerAdapter) HandleEvent(ctx context.Context, event engine.Event) error {
	return a.listener.HandleEvent(ctx, wrapEvent(event))
}

type traceListenerAdapter struct {
	listener engine.EventListener
}

func (a traceListenerAdapter) HandleEvent(ctx context.Context, event Event) error {
	return a.listener.HandleEvent(ctx, event.engineEvent())
}

// Session is the mutable runtime for one compiled ruleset: working memory, the
// agenda, the focus stack, logical support, backchain demand, and event
// delivery. A session has one logical owner; overlapping operations from other
// goroutines fail fast with [ErrConcurrencyMisuse] rather than blocking, except
// that mutations from other goroutines queue during an active Run.
type Session struct {
	rt *engine.Session
}

func wrapSession(rt *engine.Session) *Session {
	if rt == nil {
		return nil
	}
	return &Session{rt: rt}
}

func (s *Session) engineSession() *engine.Session {
	if s == nil {
		return nil
	}
	return s.rt
}

// FactSnapshot is an immutable view of one fact's identity, fields, and support
// state as of the snapshot or event that produced it.
type FactSnapshot struct {
	value engine.FactSnapshot
}

func wrapFactSnapshot(value engine.FactSnapshot) FactSnapshot {
	return FactSnapshot{value: value}
}

func wrapFactSnapshotPtr(value *engine.FactSnapshot) *FactSnapshot {
	if value == nil {
		return nil
	}
	out := wrapFactSnapshot(*value)
	return &out
}

func wrapFactSnapshots(values []engine.FactSnapshot) []FactSnapshot {
	out := make([]FactSnapshot, len(values))
	for i, value := range values {
		out[i] = wrapFactSnapshot(value)
	}
	return out
}

func (f FactSnapshot) engineFact() engine.FactSnapshot {
	return f.value
}

func engineFactSnapshotPtr(value *FactSnapshot) *engine.FactSnapshot {
	if value == nil {
		return nil
	}
	out := value.engineFact()
	return &out
}

func engineFactSnapshots(values []FactSnapshot) []engine.FactSnapshot {
	out := make([]engine.FactSnapshot, len(values))
	for i, value := range values {
		out[i] = value.engineFact()
	}
	return out
}

// ID returns the fact identity.
func (f FactSnapshot) ID() FactID {
	return f.value.ID()
}

// Name returns the dynamic fact name, if the fact is not template-backed.
func (f FactSnapshot) Name() string {
	return f.value.Name()
}

// TemplateKey returns the template identity, if the fact is template-backed.
func (f FactSnapshot) TemplateKey() TemplateKey {
	return f.value.TemplateKey()
}

// Version returns the fact version.
func (f FactSnapshot) Version() FactVersion {
	return f.value.Version()
}

// Recency returns the mutation recency that ordered this fact.
func (f FactSnapshot) Recency() Recency {
	return f.value.Recency()
}

// Generation returns the working-memory generation that owns this fact.
func (f FactSnapshot) Generation() Generation {
	return f.value.Generation()
}

// Fields returns a copy of the fact's field values.
func (f FactSnapshot) Fields() Fields {
	return f.value.Fields()
}

// Field returns one field value by name.
func (f FactSnapshot) Field(name string) (Value, bool) {
	return f.value.Field(name)
}

// Path returns the value at path, if present.
func (f FactSnapshot) Path(path PathSpec) (Value, bool, error) {
	return f.value.Path(path)
}

// Support returns the fact's logical support classification.
func (f FactSnapshot) Support() FactSupportProvenance {
	return f.value.Support()
}

func (f FactSnapshot) String() string {
	return f.value.String()
}

// FieldPresence returns whether a template field was defaulted, explicit, or
// omitted.
func (f FactSnapshot) FieldPresence(field string) (FieldPresence, bool) {
	return f.value.FieldPresence(field)
}

// FieldPresenceMap returns template field-presence metadata keyed by field
// name.
func (f FactSnapshot) FieldPresenceMap() map[string]FieldPresence {
	return f.value.FieldPresenceMap()
}

// Snapshot is an immutable, point-in-time view of working memory: every fact
// plus the support graph, as of when Snapshot was called. It does not change as
// the session mutates afterward.
type Snapshot struct {
	value engine.Snapshot
}

func wrapSnapshot(value engine.Snapshot) Snapshot {
	return Snapshot{value: value}
}

func (s Snapshot) engineSnapshot() engine.Snapshot {
	return s.value
}

// SessionID returns the session that produced the snapshot.
func (s Snapshot) SessionID() SessionID {
	return s.value.SessionID()
}

// RulesetID returns the compiled ruleset revision captured by the snapshot.
func (s Snapshot) RulesetID() RulesetID {
	return s.value.RulesetID()
}

// Generation returns the working-memory generation captured by the snapshot.
func (s Snapshot) Generation() Generation {
	return s.value.Generation()
}

// Len returns the number of facts in the snapshot.
func (s Snapshot) Len() int {
	return s.value.Len()
}

// Facts returns snapshot facts in deterministic order.
func (s Snapshot) Facts() []FactSnapshot {
	return wrapFactSnapshots(s.value.Facts())
}

// SupportGraph returns the logical support graph captured by the snapshot.
func (s Snapshot) SupportGraph() SupportGraph {
	return wrapSupportGraph(s.value.SupportGraph())
}

// BackchainDemandDiagnostics returns active generated backward-chaining demand
// fact counts for the snapshot.
func (s Snapshot) BackchainDemandDiagnostics() BackchainDemandDiagnostics {
	return s.value.BackchainDemandDiagnostics()
}

// Fact returns the fact with id, if present.
func (s Snapshot) Fact(id FactID) (FactSnapshot, bool) {
	fact, ok := s.value.Fact(id)
	return wrapFactSnapshot(fact), ok
}

// FactsByName returns facts with the given dynamic fact name.
func (s Snapshot) FactsByName(name string) []FactSnapshot {
	return wrapFactSnapshots(s.value.FactsByName(name))
}

// FactsByTemplateKey returns facts with templateKey.
func (s Snapshot) FactsByTemplateKey(templateKey TemplateKey) []FactSnapshot {
	return wrapFactSnapshots(s.value.FactsByTemplateKey(templateKey))
}

// Explain returns a derivation for fact id from the snapshot's support graph.
func (s Snapshot) Explain(id FactID, opts ...ExplainOption) (Derivation, bool) {
	derivation, ok := s.value.Explain(id, opts...)
	return wrapDerivation(derivation), ok
}

// Query returns an iterator over rows produced by a compiled query against the
// snapshot.
//
// Each call builds a fresh matching runtime and re-propagates every snapshot
// fact, so cost grows with snapshot size; for repeated queries over large
// working memories prefer session queries. Snapshot queries never generate
// backchain demand: a query that would need backward chaining fails with
// ErrUnsupportedRuntime. Row order is unspecified.
func (s Snapshot) Query(ctx context.Context, name string, args QueryArgs) (*QueryIterator, error) {
	it, err := s.value.Query(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return wrapQueryIterator(it), nil
}

// QueryAll materializes all rows produced by a compiled query against the
// snapshot.
//
// Each call builds a fresh matching runtime and re-propagates every snapshot
// fact, so cost grows with snapshot size; for repeated queries over large
// working memories prefer session queries. Snapshot queries never generate
// backchain demand: a query that would need backward chaining fails with
// ErrUnsupportedRuntime. Row order is unspecified.
func (s Snapshot) QueryAll(ctx context.Context, name string, args QueryArgs) ([]QueryRow, error) {
	rows, err := s.value.QueryAll(ctx, name, args)
	return wrapQueryRows(rows), err
}

func (s Snapshot) String() string {
	return s.value.String()
}

// Agenda is an ordered, immutable view of pending activations, as returned by
// Session.Agenda in the exact order Run would fire them.
type Agenda struct {
	value engine.Agenda
}

func wrapAgenda(value engine.Agenda) Agenda {
	return Agenda{value: value}
}

func (a Agenda) engineAgenda() engine.Agenda {
	return a.value
}

// AgendaActivation describes one pending activation in an [Agenda]: its rule,
// module, salience, and matched facts.
type AgendaActivation struct {
	value engine.AgendaActivation
}

func wrapAgendaActivation(value engine.AgendaActivation) AgendaActivation {
	return AgendaActivation{value: value}
}

func wrapAgendaActivations(values []engine.AgendaActivation) []AgendaActivation {
	out := make([]AgendaActivation, len(values))
	for i, value := range values {
		out[i] = wrapAgendaActivation(value)
	}
	return out
}

func (a AgendaActivation) engineActivation() engine.AgendaActivation {
	return a.value
}

func engineAgendaActivations(values []AgendaActivation) []engine.AgendaActivation {
	out := make([]engine.AgendaActivation, len(values))
	for i, value := range values {
		out[i] = value.engineActivation()
	}
	return out
}

// ActivationID returns the activation identity.
func (a AgendaActivation) ActivationID() ActivationID {
	return a.value.ActivationID()
}

// RuleID returns the rule that produced the activation.
func (a AgendaActivation) RuleID() RuleID {
	return a.value.RuleID()
}

// RuleRevisionID returns the concrete rule revision behind the activation.
func (a AgendaActivation) RuleRevisionID() RuleRevisionID {
	return a.value.RuleRevisionID()
}

// RuleName returns the rule name.
func (a AgendaActivation) RuleName() string {
	return a.value.RuleName()
}

// Module returns the agenda module that owns the activation.
func (a AgendaActivation) Module() ModuleName {
	return a.value.Module()
}

// Salience returns the activation salience.
func (a AgendaActivation) Salience() int {
	return a.value.Salience()
}

// FactIDs returns the fact identities matched by the activation.
func (a AgendaActivation) FactIDs() []FactID {
	return a.value.FactIDs()
}

// FocusStack returns a copy of the captured focus stack from bottom to top.
func (a Agenda) FocusStack() []ModuleName {
	return a.value.FocusStack()
}

// Activations returns pending activations in the order Run would fire them.
func (a Agenda) Activations() []AgendaActivation {
	return wrapAgendaActivations(a.value.Activations())
}

// ActivationsForModule returns pending activations for module in module-local
// firing order.
func (a Agenda) ActivationsForModule(module ModuleName) []AgendaActivation {
	return wrapAgendaActivations(a.value.ActivationsForModule(module))
}

// Len reports the number of activations in fire order.
func (a Agenda) Len() int {
	return a.value.Len()
}

// QueryIterator iterates the pre-materialized rows produced by Session.Query
// or Snapshot.Query.
type QueryIterator struct {
	it *engine.QueryIterator
}

func wrapQueryIterator(it *engine.QueryIterator) *QueryIterator {
	if it == nil {
		return nil
	}
	return &QueryIterator{it: it}
}

func (it *QueryIterator) engineIterator() *engine.QueryIterator {
	if it == nil {
		return nil
	}
	return it.it
}

// QueryRow is one result row from a query, holding fact bindings and computed
// values under their declared aliases.
type QueryRow struct {
	value engine.QueryRow
}

func wrapQueryRow(value engine.QueryRow) QueryRow {
	return QueryRow{value: value}
}

func wrapQueryRows(values []engine.QueryRow) []QueryRow {
	out := make([]QueryRow, len(values))
	for i, value := range values {
		out[i] = wrapQueryRow(value)
	}
	return out
}

// Aliases returns the query result aliases in declared order.
func (r QueryRow) Aliases() []string {
	return r.value.Aliases()
}

// Fact returns the fact bound to alias, if alias is a fact binding.
func (r QueryRow) Fact(alias string) (FactSnapshot, bool) {
	fact, ok := r.value.Fact(alias)
	return wrapFactSnapshot(fact), ok
}

// Value returns the value bound to alias, if alias is a computed value.
func (r QueryRow) Value(alias string) (Value, bool) {
	return r.value.Value(alias)
}

// Next returns the next query row, or ok=false when iteration is complete.
func (it *QueryIterator) Next(ctx context.Context) (QueryRow, bool, error) {
	row, ok, err := it.engineIterator().Next(ctx)
	return wrapQueryRow(row), ok, err
}

// All returns every remaining query row.
func (it *QueryIterator) All(ctx context.Context) ([]QueryRow, error) {
	rows, err := it.engineIterator().All(ctx)
	return wrapQueryRows(rows), err
}

// RunResult reports the outcome of a Run: its status and how many activations
// fired.
type RunResult struct {
	RunID  RunID
	Status RunStatus
	Fired  int
}

func wrapRunResult(result engine.RunResult) RunResult {
	return RunResult{
		RunID:  result.RunID,
		Status: result.Status,
		Fired:  result.Fired,
	}
}

func (r RunResult) engineResult() engine.RunResult {
	return engine.RunResult{
		RunID:  r.RunID,
		Status: r.Status,
		Fired:  r.Fired,
	}
}

// FieldChange describes one field's before and after value from a Modify, as
// recorded in [MutationDelta].ChangedFields.
type FieldChange struct {
	Field string
	Old   Value
	New   Value
}

func wrapFieldChange(change engine.FieldChange) FieldChange {
	return FieldChange{
		Field: change.Field,
		Old:   change.Old,
		New:   change.New,
	}
}

func wrapFieldChanges(changes []engine.FieldChange) []FieldChange {
	out := make([]FieldChange, len(changes))
	for i, change := range changes {
		out[i] = wrapFieldChange(change)
	}
	return out
}

func (c FieldChange) engineChange() engine.FieldChange {
	return engine.FieldChange{
		Field: c.Field,
		Old:   c.Old,
		New:   c.New,
	}
}

func engineFieldChanges(changes []FieldChange) []engine.FieldChange {
	out := make([]engine.FieldChange, len(changes))
	for i, change := range changes {
		out[i] = change.engineChange()
	}
	return out
}

// MutationDelta describes one mutation's effect: its kind, the fact before and
// after, version and support changes, and which activation or rule caused it,
// if any.
type MutationDelta struct {
	Kind           MutationKind
	Generation     Generation
	OldGeneration  Generation
	ActivationID   ActivationID
	RuleID         RuleID
	RuleRevisionID RuleRevisionID
	SupportBefore  FactSupportProvenance
	SupportAfter   FactSupportProvenance
	Recency        Recency
	FactID         FactID
	OldVersion     FactVersion
	NewVersion     FactVersion
	Before         *FactSnapshot
	After          *FactSnapshot
	OldDuplicate   DuplicateKey
	NewDuplicate   DuplicateKey
	ChangedFields  []FieldChange
}

func wrapMutationDelta(delta engine.MutationDelta) MutationDelta {
	return MutationDelta{
		Kind:           delta.Kind,
		Generation:     delta.Generation,
		OldGeneration:  delta.OldGeneration,
		ActivationID:   delta.ActivationID,
		RuleID:         delta.RuleID,
		RuleRevisionID: delta.RuleRevisionID,
		SupportBefore:  delta.SupportBefore,
		SupportAfter:   delta.SupportAfter,
		Recency:        delta.Recency,
		FactID:         delta.FactID,
		OldVersion:     delta.OldVersion,
		NewVersion:     delta.NewVersion,
		Before:         wrapFactSnapshotPtr(delta.Before),
		After:          wrapFactSnapshotPtr(delta.After),
		OldDuplicate:   delta.OldDuplicate,
		NewDuplicate:   delta.NewDuplicate,
		ChangedFields:  wrapFieldChanges(delta.ChangedFields),
	}
}

func wrapMutationDeltaPtr(delta *engine.MutationDelta) *MutationDelta {
	if delta == nil {
		return nil
	}
	out := wrapMutationDelta(*delta)
	return &out
}

func (d MutationDelta) engineDelta() engine.MutationDelta {
	return engine.MutationDelta{
		Kind:           d.Kind,
		Generation:     d.Generation,
		OldGeneration:  d.OldGeneration,
		ActivationID:   d.ActivationID,
		RuleID:         d.RuleID,
		RuleRevisionID: d.RuleRevisionID,
		SupportBefore:  d.SupportBefore,
		SupportAfter:   d.SupportAfter,
		Recency:        d.Recency,
		FactID:         d.FactID,
		OldVersion:     d.OldVersion,
		NewVersion:     d.NewVersion,
		Before:         engineFactSnapshotPtr(d.Before),
		After:          engineFactSnapshotPtr(d.After),
		OldDuplicate:   d.OldDuplicate,
		NewDuplicate:   d.NewDuplicate,
		ChangedFields:  engineFieldChanges(d.ChangedFields),
	}
}

// FieldChanges returns a cloned copy of changed fields.
func (d MutationDelta) FieldChanges() []FieldChange {
	return wrapFieldChanges(d.engineDelta().FieldChanges())
}

// AssertResult reports the outcome of an assert: its status, the resulting
// fact, and the mutation delta.
type AssertResult struct {
	Status       AssertStatus
	Fact         FactSnapshot
	DuplicateKey DuplicateKey
	Delta        *MutationDelta
}

func wrapAssertResult(result engine.AssertResult) AssertResult {
	return AssertResult{
		Status:       result.Status,
		Fact:         wrapFactSnapshot(result.Fact),
		DuplicateKey: result.DuplicateKey,
		Delta:        wrapMutationDeltaPtr(result.Delta),
	}
}

// Inserted reports whether the assert inserted a new fact.
func (r AssertResult) Inserted() bool {
	return r.Status == AssertInserted
}

// ModifyResult reports the outcome of a modify: its status, the resulting fact,
// and the mutation delta.
type ModifyResult struct {
	Status ModifyStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

func wrapModifyResult(result engine.ModifyResult) ModifyResult {
	return ModifyResult{
		Status: result.Status,
		Fact:   wrapFactSnapshot(result.Fact),
		Delta:  wrapMutationDeltaPtr(result.Delta),
	}
}

// Changed reports whether the modify changed the fact.
func (r ModifyResult) Changed() bool {
	return r.Status == ModifyChanged
}

// RetractResult reports the outcome of a retract: its status, the removed fact,
// and the mutation delta.
type RetractResult struct {
	Status RetractStatus
	Fact   FactSnapshot
	Delta  *MutationDelta
}

func wrapRetractResult(result engine.RetractResult) RetractResult {
	return RetractResult{
		Status: result.Status,
		Fact:   wrapFactSnapshot(result.Fact),
		Delta:  wrapMutationDeltaPtr(result.Delta),
	}
}

// Removed reports whether the retract removed the fact.
func (r RetractResult) Removed() bool {
	return r.Status == RetractRemoved
}

// RuleRevisionSummary identifies one rule revision by rule ID and revision ID,
// as reported in an [ApplyRulesetResult].
type RuleRevisionSummary struct {
	RuleID     RuleID
	RevisionID RuleRevisionID
}

func wrapRuleRevisionSummary(summary engine.RuleRevisionSummary) RuleRevisionSummary {
	return RuleRevisionSummary{
		RuleID:     summary.RuleID,
		RevisionID: summary.RevisionID,
	}
}

func wrapRuleRevisionSummaries(summaries []engine.RuleRevisionSummary) []RuleRevisionSummary {
	out := make([]RuleRevisionSummary, len(summaries))
	for i, summary := range summaries {
		out[i] = wrapRuleRevisionSummary(summary)
	}
	return out
}

func (s RuleRevisionSummary) engineSummary() engine.RuleRevisionSummary {
	return engine.RuleRevisionSummary{
		RuleID:     s.RuleID,
		RevisionID: s.RevisionID,
	}
}

func engineRuleRevisionSummaries(summaries []RuleRevisionSummary) []engine.RuleRevisionSummary {
	out := make([]engine.RuleRevisionSummary, len(summaries))
	for i, summary := range summaries {
		out[i] = summary.engineSummary()
	}
	return out
}

// RuleReplacement identifies a rule whose compiled revision changed across an
// ApplyRuleset call, from OldRevisionID to NewRevisionID.
type RuleReplacement struct {
	RuleID        RuleID
	OldRevisionID RuleRevisionID
	NewRevisionID RuleRevisionID
}

func wrapRuleReplacement(replacement engine.RuleReplacement) RuleReplacement {
	return RuleReplacement{
		RuleID:        replacement.RuleID,
		OldRevisionID: replacement.OldRevisionID,
		NewRevisionID: replacement.NewRevisionID,
	}
}

func wrapRuleReplacements(replacements []engine.RuleReplacement) []RuleReplacement {
	out := make([]RuleReplacement, len(replacements))
	for i, replacement := range replacements {
		out[i] = wrapRuleReplacement(replacement)
	}
	return out
}

func (r RuleReplacement) engineReplacement() engine.RuleReplacement {
	return engine.RuleReplacement{
		RuleID:        r.RuleID,
		OldRevisionID: r.OldRevisionID,
		NewRevisionID: r.NewRevisionID,
	}
}

func engineRuleReplacements(replacements []RuleReplacement) []engine.RuleReplacement {
	out := make([]engine.RuleReplacement, len(replacements))
	for i, replacement := range replacements {
		out[i] = replacement.engineReplacement()
	}
	return out
}

// ApplyRulesetResult reports the outcome of swapping in a new ruleset: its
// status and which rule revisions were added, removed, replaced, or left
// unchanged.
type ApplyRulesetResult struct {
	Status                 ApplyRulesetStatus
	PreviousRulesetID      RulesetID
	CurrentRulesetID       RulesetID
	AddedRuleRevisions     []RuleRevisionSummary
	RemovedRuleRevisions   []RuleRevisionSummary
	ReplacedRuleRevisions  []RuleReplacement
	UnchangedRuleRevisions []RuleRevisionSummary
}

func wrapApplyRulesetResult(result engine.ApplyRulesetResult) ApplyRulesetResult {
	return ApplyRulesetResult{
		Status:                 result.Status,
		PreviousRulesetID:      result.PreviousRulesetID,
		CurrentRulesetID:       result.CurrentRulesetID,
		AddedRuleRevisions:     wrapRuleRevisionSummaries(result.AddedRuleRevisions),
		RemovedRuleRevisions:   wrapRuleRevisionSummaries(result.RemovedRuleRevisions),
		ReplacedRuleRevisions:  wrapRuleReplacements(result.ReplacedRuleRevisions),
		UnchangedRuleRevisions: wrapRuleRevisionSummaries(result.UnchangedRuleRevisions),
	}
}

func (r ApplyRulesetResult) engineResult() engine.ApplyRulesetResult {
	return engine.ApplyRulesetResult{
		Status:                 r.Status,
		PreviousRulesetID:      r.PreviousRulesetID,
		CurrentRulesetID:       r.CurrentRulesetID,
		AddedRuleRevisions:     engineRuleRevisionSummaries(r.AddedRuleRevisions),
		RemovedRuleRevisions:   engineRuleRevisionSummaries(r.RemovedRuleRevisions),
		ReplacedRuleRevisions:  engineRuleReplacements(r.ReplacedRuleRevisions),
		UnchangedRuleRevisions: engineRuleRevisionSummaries(r.UnchangedRuleRevisions),
	}
}

// Applied reports whether the ruleset swap was applied.
func (r ApplyRulesetResult) Applied() bool {
	return r.Status == ApplyRulesetApplied
}

// WhatIfReport is the structured result of a Session.WhatIf run.
type WhatIfReport struct {
	Base         Snapshot
	Fork         Snapshot
	Run          RunResult
	Firings      []WhatIfFiring
	Diff         SnapshotDiff
	AgendaBefore Agenda
	AgendaAfter  Agenda
	Derivations  []Derivation
	ForkSession  *Session
}

func wrapWhatIfReport(report engine.WhatIfReport) WhatIfReport {
	return WhatIfReport{
		Base:         wrapSnapshot(report.Base),
		Fork:         wrapSnapshot(report.Fork),
		Run:          wrapRunResult(report.Run),
		Firings:      wrapWhatIfFirings(report.Firings),
		Diff:         wrapSnapshotDiff(report.Diff),
		AgendaBefore: wrapAgenda(report.AgendaBefore),
		AgendaAfter:  wrapAgenda(report.AgendaAfter),
		Derivations:  wrapDerivations(report.Derivations),
		ForkSession:  wrapSession(report.ForkSession),
	}
}

func (r WhatIfReport) engineReport() engine.WhatIfReport {
	var fork *engine.Session
	if r.ForkSession != nil {
		fork = r.ForkSession.engineSession()
	}
	return engine.WhatIfReport{
		Base:         r.Base.engineSnapshot(),
		Fork:         r.Fork.engineSnapshot(),
		Run:          r.Run.engineResult(),
		Firings:      engineWhatIfFirings(r.Firings),
		Diff:         r.Diff.engineDiff(),
		AgendaBefore: r.AgendaBefore.engineAgenda(),
		AgendaAfter:  r.AgendaAfter.engineAgenda(),
		Derivations:  engineDerivations(r.Derivations),
		ForkSession:  fork,
	}
}

// MarshalJSON encodes a what-if report as a versioned explain document.
func (r WhatIfReport) MarshalJSON() ([]byte, error) {
	return r.engineReport().MarshalJSON()
}

// ResetResult reports the outcome of a reset: its status, the new generation,
// and, if WithResetBeforeSnapshot was set, a snapshot of working memory as it
// stood immediately before the reset.
type ResetResult struct {
	Status     ResetStatus
	Generation Generation
	Before     Snapshot
	Delta      MutationDelta
}

func wrapResetResult(result engine.ResetResult) ResetResult {
	return ResetResult{
		Status:     result.Status,
		Generation: result.Generation,
		Before:     wrapSnapshot(result.Before),
		Delta:      wrapMutationDelta(result.Delta),
	}
}

// SnapshotDiff is the working-memory difference between two snapshots.
type SnapshotDiff struct {
	Added     []FactSnapshot
	Retracted []FactSnapshot
	Modified  []FactModification
}

func wrapSnapshotDiff(diff engine.SnapshotDiff) SnapshotDiff {
	modified := make([]FactModification, len(diff.Modified))
	for i, mod := range diff.Modified {
		modified[i] = FactModification{
			Before:        wrapFactSnapshot(mod.Before),
			After:         wrapFactSnapshot(mod.After),
			ChangedFields: wrapFieldChanges(mod.ChangedFields),
			SupportBefore: mod.SupportBefore,
			SupportAfter:  mod.SupportAfter,
		}
	}
	return SnapshotDiff{
		Added:     wrapFactSnapshots(diff.Added),
		Retracted: wrapFactSnapshots(diff.Retracted),
		Modified:  modified,
	}
}

func (d SnapshotDiff) engineDiff() engine.SnapshotDiff {
	modified := make([]engine.FactModification, len(d.Modified))
	for i, mod := range d.Modified {
		modified[i] = engine.FactModification{
			Before:        mod.Before.engineFact(),
			After:         mod.After.engineFact(),
			ChangedFields: engineFieldChanges(mod.ChangedFields),
			SupportBefore: mod.SupportBefore,
			SupportAfter:  mod.SupportAfter,
		}
	}
	return engine.SnapshotDiff{
		Added:     engineFactSnapshots(d.Added),
		Retracted: engineFactSnapshots(d.Retracted),
		Modified:  modified,
	}
}

// Empty reports whether the diff has no added, retracted, or modified facts.
func (d SnapshotDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Retracted) == 0 && len(d.Modified) == 0
}

// FactModification is one changed fact in a [SnapshotDiff].
type FactModification struct {
	Before        FactSnapshot
	After         FactSnapshot
	ChangedFields []FieldChange
	SupportBefore FactSupportState
	SupportAfter  FactSupportState
}

// DiffSnapshots returns the difference from before to after: facts added,
// retracted, and modified (by field value or support state), in fact-id order.
func DiffSnapshots(before, after Snapshot) SnapshotDiff {
	return wrapSnapshotDiff(engine.DiffSnapshots(before.engineSnapshot(), after.engineSnapshot()))
}

// WithWhatIfMaxFirings bounds a Session.WhatIf fork run at n firings (default
// [engine.DefaultWhatIfMaxFirings]). Pass n <= 0 to opt out of the bound.
func WithWhatIfMaxFirings(n int) WhatIfOption {
	return engine.WithWhatIfMaxFirings(n)
}

// WithWhatIfExplain attaches an explain log to the what-if fork so the report
// includes a derivation for every added fact.
func WithWhatIfExplain() WhatIfOption {
	return engine.WithWhatIfExplain()
}

// WithWhatIfRetainFork keeps the what-if fork open and returns it in the
// report; the caller then owns closing it.
func WithWhatIfRetainFork() WhatIfOption {
	return engine.WithWhatIfRetainFork()
}

// WithWhatIfOutputWriter captures the emit output of the what-if run to w. By
// default the fork's output is discarded so a hypothetical run never writes to
// the base session's live output sink.
func WithWhatIfOutputWriter(w io.Writer) WhatIfOption {
	return engine.WithWhatIfOutputWriter(w)
}

const (
	// WhyNotActivated means the rule has a pending activation.
	WhyNotActivated = rules.WhyNotActivated
	// WhyNotAlreadyFired means the rule matched and fired (refraction).
	WhyNotAlreadyFired = rules.WhyNotAlreadyFired
	// WhyNotNeverMatched means no branch produced a complete match.
	WhyNotNeverMatched = rules.WhyNotNeverMatched
	// WhyNotBlocked means the closest branch failed on a blocked negation.
	WhyNotBlocked = rules.WhyNotBlocked

	// WhyNotReasonNone is the zero condition reason.
	WhyNotReasonNone = rules.WhyNotReasonNone
	// WhyNotReasonNoAlphaMatches means the condition's pattern matched no
	// fact.
	WhyNotReasonNoAlphaMatches = rules.WhyNotReasonNoAlphaMatches
	// WhyNotReasonJoinMismatch means facts exist but their join keys do not
	// align with the partial match.
	WhyNotReasonJoinMismatch = rules.WhyNotReasonJoinMismatch
	// WhyNotReasonPredicate means a residual predicate or test rejected the
	// candidate.
	WhyNotReasonPredicate = rules.WhyNotReasonPredicate
	// WhyNotReasonNegationBlocked means a negated condition is blocked by
	// one or more facts.
	WhyNotReasonNegationBlocked = rules.WhyNotReasonNegationBlocked

	// ExplainSchemaVersion is the version of the machine-readable JSON
	// explain contract emitted by MarshalJSON on Derivation, WhyNotReport,
	// and WhatIfReport.
	ExplainSchemaVersion = engine.ExplainSchemaVersion
)

type (
	// RuntimeDiagnostics is a point-in-time diagnostic of retained
	// runtime memory, returned by Session.RuntimeDiagnostics.
	RuntimeDiagnostics = engine.RuntimeDiagnostics
	// RuntimeMemoryOwnerDiagnostics summarizes one Rete runtime memory
	// owner's retained rows, indexes, and bytes.
	RuntimeMemoryOwnerDiagnostics = engine.RuntimeMemoryOwnerDiagnostics
)

const (
	// EventFactAsserted, EventFactModified, and EventFactRetracted report
	// working-memory changes; EventReset reports a session Reset.
	EventFactAsserted  = engine.EventFactAsserted
	EventFactModified  = engine.EventFactModified
	EventFactRetracted = engine.EventFactRetracted
	EventReset         = engine.EventReset
	// EventRuleActivated and EventRuleDeactivated report a match
	// entering or leaving the agenda.
	EventRuleActivated   = engine.EventRuleActivated
	EventRuleDeactivated = engine.EventRuleDeactivated
	// EventRuleFired reports one activation firing during a Run.
	EventRuleFired = engine.EventRuleFired
	// EventActionFailed reports a rule action returning an error; its
	// [Event] carries ActionName, ActionIndex, and Cause, with severity
	// [EventSeverityError].
	EventActionFailed = engine.EventActionFailed
	// EventLogicalSupportAdded and EventLogicalSupportRemoved report a
	// logical support edge being created or removed.
	EventLogicalSupportAdded   = engine.EventLogicalSupportAdded
	EventLogicalSupportRemoved = engine.EventLogicalSupportRemoved
	// EventSeverityInfo is the default event severity.
	EventSeverityInfo = engine.EventSeverityInfo
	// EventSeverityError marks [EventActionFailed] events.
	EventSeverityError = engine.EventSeverityError
	// StrategyDepth orders equal-salience activations by recency, most
	// recent first. It is the default [Strategy].
	StrategyDepth = engine.StrategyDepth
	// StrategyBreadth orders equal-salience activations by creation
	// order, oldest first.
	StrategyBreadth = engine.StrategyBreadth
	// MutationAssert, MutationModify, MutationRetract, and MutationReset
	// tag a [MutationDelta]'s Kind.
	MutationAssert  = rules.MutationAssert
	MutationModify  = rules.MutationModify
	MutationRetract = rules.MutationRetract
	MutationReset   = rules.MutationReset
	// RunCompleted reports that Run emptied the agenda.
	RunCompleted = rules.RunCompleted
	// RunHalted reports that an action called Halt; the halting
	// activation's remaining actions still ran, and a later Run
	// continues with the activations left pending.
	RunHalted = rules.RunHalted
	// RunFireLimit reports that Run stopped at the [WithMaxFirings]
	// limit with activations still pending; a later Run continues them.
	RunFireLimit = rules.RunFireLimit
	// RunCanceled reports that ctx was canceled during the run.
	RunCanceled = rules.RunCanceled
	// RunActionFailed reports that an action returned an error; Run
	// returns a non-nil error that is an *[ActionFailureError].
	RunActionFailed = rules.RunActionFailed
	// RunClosed reports that the session was already closed.
	RunClosed = rules.RunClosed
	// RunConcurrencyMisuse reports that this Run overlapped another Run
	// on the same session.
	RunConcurrencyMisuse = rules.RunConcurrencyMisuse
	// RunFailed reports an internal engine failure that prevented the
	// run from completing.
	RunFailed = rules.RunFailed
	// FactSupportStated marks a fact asserted by host code and not
	// logically supported.
	FactSupportStated = rules.FactSupportStated
	// FactSupportLogical marks a fact asserted only through rule-driven
	// logical support; it is retracted automatically when that support
	// is removed, and can't be modified or retracted directly.
	FactSupportLogical = rules.FactSupportLogical
	// FactSupportStatedAndLogical marks a fact that carries both stated
	// and logical support at once.
	FactSupportStatedAndLogical = rules.FactSupportStatedAndLogical
	// FactSupportMetadataOnly marks a fact support classification used
	// internally for metadata-only support transitions.
	FactSupportMetadataOnly = rules.FactSupportMetadataOnly
	// AssertInserted reports that a new fact entered working memory.
	AssertInserted = rules.AssertInserted
	// AssertExisting reports that the template's duplicate policy
	// matched an existing fact with identical fields, so no new fact
	// was inserted.
	AssertExisting = rules.AssertExisting
	// AssertReplaced reports that a unique-key template matched an
	// existing fact whose non-key fields differed, so the old fact was
	// retracted and a new fact (with a new fact ID) was inserted in its
	// place.
	AssertReplaced = rules.AssertReplaced
	// AssertValidationFailure reports that the fields failed template
	// validation; the error wraps [rules.ErrValidation].
	AssertValidationFailure = rules.AssertValidationFailure
	// AssertClosed reports that the session was already closed.
	AssertClosed = rules.AssertClosed
	// AssertConcurrencyMisuse reports concurrent misuse of the session.
	AssertConcurrencyMisuse = rules.AssertConcurrencyMisuse
	// ModifyChanged reports that fields changed and propagated through
	// the Rete network.
	ModifyChanged = rules.ModifyChanged
	// ModifyNoOp reports that the patch made no difference to the
	// fact's fields.
	ModifyNoOp = rules.ModifyNoOp
	// ModifyMissing reports that no such fact exists in the current
	// generation.
	ModifyMissing = rules.ModifyMissing
	// ModifyStale reports that the fact ID's generation predates the
	// session's current generation, typically from before a Reset.
	ModifyStale = rules.ModifyStale
	// ModifyValidationFailure reports that the patched fact fails
	// template validation.
	ModifyValidationFailure = rules.ModifyValidationFailure
	// ModifyDuplicate reports that the patch collides with another
	// existing fact under the template's duplicate policy.
	ModifyDuplicate = rules.ModifyDuplicate
	// ModifyLogicalSupport reports that the fact currently has logical
	// support and can't be modified directly.
	ModifyLogicalSupport = rules.ModifyLogicalSupport
	// ModifyClosed reports that the session was already closed.
	ModifyClosed = rules.ModifyClosed
	// ModifyConcurrencyMisuse reports concurrent misuse of the session.
	ModifyConcurrencyMisuse = rules.ModifyConcurrencyMisuse
	// RetractRemoved reports that the fact left working memory; any
	// logical facts it alone supported cascade away in the same
	// operation.
	RetractRemoved = rules.RetractRemoved
	// RetractStatedSupportRemoved reports that the fact had both stated
	// and logical support, its stated support was removed, and it
	// survives as logical-only.
	RetractStatedSupportRemoved = rules.RetractStatedSupportRemoved
	// RetractLogicalOnly reports that the fact exists only through
	// logical support and can't be retracted directly; retract its
	// supporting facts instead.
	RetractLogicalOnly = rules.RetractLogicalOnly
	// RetractMissing reports that no such fact exists in the current
	// generation.
	RetractMissing = rules.RetractMissing
	// RetractStale reports that the fact ID's generation predates the
	// session's current generation, typically from before a Reset.
	RetractStale = rules.RetractStale
	// RetractClosed reports that the session was already closed.
	RetractClosed = rules.RetractClosed
	// RetractValidationFailure reports an internal propagation error
	// during retraction.
	RetractValidationFailure = rules.RetractValidationFailure
	// RetractConcurrencyMisuse reports concurrent misuse of the session.
	RetractConcurrencyMisuse = rules.RetractConcurrencyMisuse
	// ResetApplied reports that the reset succeeded: the generation
	// advanced, working memory was cleared and reseeded with initial
	// facts, logical support and backchain demand were cleared, and the
	// focus stack and agenda were rebuilt.
	ResetApplied = rules.ResetApplied
	// ResetValidationFailure reports that rebuilding initial facts or
	// the Rete graph for the new generation failed.
	ResetValidationFailure = rules.ResetValidationFailure
	// ResetClosed reports that the session was already closed.
	ResetClosed = rules.ResetClosed
	// ResetConcurrencyMisuse reports concurrent misuse of the session.
	ResetConcurrencyMisuse = rules.ResetConcurrencyMisuse
	// ApplyRulesetApplied reports that the new ruleset was swapped in
	// with working memory kept, and lists which rule revisions were
	// added, removed, replaced, or unchanged.
	ApplyRulesetApplied = rules.ApplyRulesetApplied
	// ApplyRulesetUnchanged reports that the target ruleset has the
	// same RulesetID as the current one, so no work was performed.
	ApplyRulesetUnchanged = rules.ApplyRulesetUnchanged
	// ApplyRulesetIncompatible reports that the target ruleset is nil,
	// doesn't define the templates live facts depend on with an
	// identical spec, or the configured initial facts no longer
	// validate against it.
	ApplyRulesetIncompatible = rules.ApplyRulesetIncompatible
	// ApplyRulesetClosed reports that the session was already closed.
	ApplyRulesetClosed = rules.ApplyRulesetClosed
	// ApplyRulesetConcurrencyMisuse reports concurrent misuse of the
	// session.
	ApplyRulesetConcurrencyMisuse = rules.ApplyRulesetConcurrencyMisuse
)

var (
	// ErrClosedSession is returned by any session operation called after
	// Close, paired with the operation's ...Closed status.
	ErrClosedSession = rules.ErrClosedSession
	// ErrConcurrencyMisuse is returned when an operation can't acquire
	// the session's single-owner lock: overlapping calls from other
	// goroutines, or Snapshot/queries attempted while a Run is active.
	ErrConcurrencyMisuse = rules.ErrConcurrencyMisuse
	// ErrActionFailed is the sentinel [ActionFailureError].Is matches,
	// representing a rule action that returned an error during Run.
	ErrActionFailed = rules.ErrActionFailed
	// ErrFactNotFound is returned by Modify or Retract when the given
	// FactID doesn't exist in the current generation.
	ErrFactNotFound = rules.ErrFactNotFound
	// ErrStaleFactID is returned by Modify or Retract when the given
	// FactID's generation predates the session's current generation,
	// typically because a Reset happened since the ID was obtained.
	ErrStaleFactID = rules.ErrStaleFactID
	// ErrDuplicateFact is returned by Modify when the patched fact
	// collides with another existing fact under a deduplicating
	// duplicate policy.
	ErrDuplicateFact = rules.ErrDuplicateFact
	// ErrQueryNotFound is returned by Query or QueryAll for an unknown
	// query name.
	ErrQueryNotFound = rules.ErrQueryNotFound
	// ErrQueryArgument is returned when [QueryArgs] names an unknown
	// parameter, omits a required parameter, or supplies a value whose
	// kind doesn't match the declared parameter kind.
	ErrQueryArgument = rules.ErrQueryArgument
	// ErrQueryExecution is returned when running a compiled query fails
	// internally, independent of the caller's arguments.
	ErrQueryExecution = rules.ErrQueryExecution
	// ErrLogicalSupportUnavailable is returned when asserting a logical
	// fact without a valid activation to support it.
	ErrLogicalSupportUnavailable = rules.ErrLogicalSupportUnavailable
	// ErrLogicalOnlyRetract is returned by Retract when the fact exists
	// only through logical support; retract its supporting facts
	// instead.
	ErrLogicalOnlyRetract = rules.ErrLogicalOnlyRetract
	// ErrLogicalFactModify is returned by Modify when the fact currently
	// carries logical support, whether logical-only or stated-and-
	// logical.
	ErrLogicalFactModify = rules.ErrLogicalFactModify
	// ErrExplainLogUnavailable is returned by Session.Explain when the
	// session was built without [WithExplainLog]; fall back to
	// Snapshot.Explain for a support-only derivation.
	ErrExplainLogUnavailable = rules.ErrExplainLogUnavailable
	// ErrRuleNotFound is returned by Session.WhyNot when the named rule is
	// not in the session's ruleset.
	ErrRuleNotFound = rules.ErrRuleNotFound
)

// WithSessionID sets the [SessionID] returned later by [Session].ID.
func WithSessionID(id SessionID) Option {
	return engine.WithSessionID(id)
}

// WithEventListener registers a listener to receive the session's Events.
// Repeat the option to register more than one listener; a nil listener is
// ignored. Pass [ForEventTypes] to subscribe the listener to a subset of
// event types; envelopes for unsubscribed types are never constructed.
func WithEventListener(listener EventListener, opts ...EventListenerOption) Option {
	if listener == nil {
		return engine.WithEventListener(nil, opts...)
	}
	return engine.WithEventListener(eventListenerAdapter{listener: listener}, opts...)
}

// ForEventTypes restricts a listener registered with [WithEventListener]
// to the given event types.
func ForEventTypes(types ...EventType) EventListenerOption {
	return engine.ForEventTypes(types...)
}

// NewTraceListener returns a listener that prints one line per event to w,
// suitable for tracing a session's activity.
func NewTraceListener(w io.Writer, opts ...TraceOption) EventListener {
	listener := engine.NewTraceListener(w, opts...)
	if listener == nil {
		return nil
	}
	return traceListenerAdapter{listener: listener}
}

// TraceWithTimestamps prefixes each trace line printed by a
// [NewTraceListener] with the event's timestamp.
func TraceWithTimestamps() TraceOption {
	return engine.TraceWithTimestamps()
}

// WithEventClock overrides the timestamp source used to stamp
// [Event].Timestamp. A nil clock is ignored, leaving the default time.Now.
func WithEventClock(clock func() time.Time) Option {
	return engine.WithEventClock(clock)
}

// WithInitialFacts appends facts to assert at construction and re-assert
// on every Reset, matching deffacts in generated .gess code. The option
// can be given more than once; facts accumulate across calls.
func WithInitialFacts(initials ...InitialFact) Option {
	return engine.WithInitialFacts(initials...)
}

// WithGlobals binds per-session values for globals declared on the
// ruleset, keyed by global name without the '*' markers. Unnamed globals
// keep their declared defaults; naming an undeclared global or supplying
// a value of the wrong kind fails session construction.
func WithGlobals(values map[string]any) Option {
	return engine.WithGlobals(values)
}

// WithStrategy sets the session's conflict-resolution [Strategy] for
// equal-salience activations. The default is [StrategyDepth].
func WithStrategy(strategy Strategy) Option {
	return engine.WithStrategy(strategy)
}

// WithResetBeforeSnapshot controls whether a successful Reset populates
// [ResetResult].Before with a snapshot of working memory immediately before
// the reset. It defaults to false, since materializing that snapshot has
// a cost most sessions don't need to pay.
func WithResetBeforeSnapshot(enabled bool) Option {
	return engine.WithResetBeforeSnapshot(enabled)
}

// WithOutputWriter sets the destination for the .gess emit action. When
// unset, emitted output is discarded. A fork inherits the parent's writer
// unless it supplies its own.
func WithOutputWriter(w io.Writer) Option {
	return engine.WithOutputWriter(w)
}

// WithMaxFirings caps one Run call at n activation firings. A run that
// stops at the cap with work remaining returns [RunFireLimit]; n <= 0
// fails the run.
func WithMaxFirings(n int) RunOption {
	return engine.WithMaxFirings(n)
}

// WithExplainMaxDepth caps derivation recursion depth for an Explain call
// (default [engine.DefaultExplainMaxDepth]). Depth beyond the cap surfaces
// as a Truncated derivation node.
func WithExplainMaxDepth(depth int) ExplainOption {
	return engine.WithExplainMaxDepth(depth)
}

// WithExplainMaxNodes caps the total derivation node count for one Explain
// call (default [engine.DefaultExplainMaxNodes]). Reaching the cap surfaces
// as a Truncated derivation node.
func WithExplainMaxNodes(nodes int) ExplainOption {
	return engine.WithExplainMaxNodes(nodes)
}

// WithExplainLog attaches a bounded, in-memory recorder of fact-mutation
// events so Session.Explain can report a fact's producing firing and its
// assert -> modify... lineage. Without it, Session.Explain returns
// [ErrExplainLogUnavailable]. A fork does not inherit the log; pass this
// option to Fork to record lineage on the fork. Reset clears the log.
func WithExplainLog(opts ...ExplainLogOption) Option {
	return engine.WithExplainLog(opts...)
}

// WithExplainLogMaxEntries bounds the explain log to the most recent n
// fact-mutation entries (default [engine.DefaultExplainLogMaxEntries]).
// Evicting a fact's earliest entries marks its reconstructed history
// Truncated.
func WithExplainLogMaxEntries(n int) ExplainLogOption {
	return engine.WithExplainLogMaxEntries(n)
}

// WithWhyNotMaxPartialMatches caps the near-miss partial matches Session.WhyNot
// reports per branch (default 3).
func WithWhyNotMaxPartialMatches(n int) WhyNotOption {
	return engine.WithWhyNotMaxPartialMatches(n)
}

// WithWhyNotMaxBlockers caps the blocking facts Session.WhyNot lists for a
// blocked negation (default 16); the reported BlockerCount is still the true
// total.
func WithWhyNotMaxBlockers(n int) WhyNotOption {
	return engine.WithWhyNotMaxBlockers(n)
}

// WithWhyNotMaxProbedRows bounds the beta rows Session.WhyNot scans per node
// (default 4096); reaching it sets the report's Truncated flag.
func WithWhyNotMaxProbedRows(n int) WhyNotOption {
	return engine.WithWhyNotMaxProbedRows(n)
}

// New builds a session from revision: it compiles the configured initial
// facts against revision, builds the Rete runtime, seeds working memory,
// and computes the initial agenda. It returns an error if revision is nil
// or the initial facts fail to validate against it.
func New(revision *rules.Ruleset, opts ...Option) (*Session, error) {
	engineRevision, err := engine.RulesetFromPublic(revision)
	if err != nil {
		return nil, err
	}
	rt, err := engine.NewSession(engineRevision, opts...)
	if err != nil {
		return nil, err
	}
	return wrapSession(rt), nil
}

// ID returns the session identifier configured with WithSessionID.
func (s *Session) ID() SessionID {
	return s.engineSession().ID()
}

// RulesetID returns the compiled ruleset revision currently installed in the
// session.
func (s *Session) RulesetID() RulesetID {
	return s.engineSession().RulesetID()
}

// Generation returns the current working-memory generation.
func (s *Session) Generation() Generation {
	return s.engineSession().Generation()
}

// Snapshot returns an immutable, point-in-time view of working memory.
func (s *Session) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.engineSession().Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return wrapSnapshot(snapshot), nil
}

// Fork returns an independent session with the same working state as s.
//
// The fork inherits the parent's initial facts, globals, agenda strategy, and
// output writer — rule emits in the fork write to the parent's writer unless
// the fork is created with WithOutputWriter. Event listeners and the explain
// log are not inherited.
func (s *Session) Fork(ctx context.Context, opts ...Option) (*Session, error) {
	fork, err := s.engineSession().Fork(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return wrapSession(fork), nil
}

// Close marks the session closed. Later operations fail with ErrClosedSession.
func (s *Session) Close() error {
	return s.engineSession().Close()
}

// Assert asserts a fact for templateKey using field values keyed by field name.
func (s *Session) Assert(ctx context.Context, templateKey TemplateKey, fields Fields) (AssertResult, error) {
	result, err := s.engineSession().Assert(ctx, templateKey, fields)
	return wrapAssertResult(result), err
}

// AssertTemplateValues asserts a fact using values in template field order.
func (s *Session) AssertTemplateValues(ctx context.Context, templateKey TemplateKey, values ...Value) error {
	return s.engineSession().AssertTemplateValues(ctx, templateKey, values...)
}

// Modify applies patch to an existing fact.
func (s *Session) Modify(ctx context.Context, id FactID, patch FactPatch) (ModifyResult, error) {
	result, err := s.engineSession().Modify(ctx, id, patch)
	return wrapModifyResult(result), err
}

// Retract removes stated support for a fact.
func (s *Session) Retract(ctx context.Context, id FactID) (RetractResult, error) {
	result, err := s.engineSession().Retract(ctx, id)
	return wrapRetractResult(result), err
}

// Reset clears working memory, advances the generation, and reapplies initial
// facts.
func (s *Session) Reset(ctx context.Context) (ResetResult, error) {
	result, err := s.engineSession().Reset(ctx)
	return wrapResetResult(result), err
}

// ApplyRuleset swaps in a compatible compiled ruleset while preserving working
// memory.
func (s *Session) ApplyRuleset(ctx context.Context, next *rules.Ruleset) (ApplyRulesetResult, error) {
	var engineRevision *engine.Ruleset
	if next != nil {
		var err error
		engineRevision, err = engine.RulesetFromPublic(next)
		if err != nil {
			return ApplyRulesetResult{}, err
		}
	}
	result, err := s.engineSession().ApplyRuleset(ctx, engineRevision)
	return wrapApplyRulesetResult(result), err
}

// Run fires pending activations until the agenda drains, a halt is requested,
// a run option stops it, or an error occurs.
func (s *Session) Run(ctx context.Context, opts ...RunOption) (RunResult, error) {
	result, err := s.engineSession().Run(ctx, opts...)
	return wrapRunResult(result), err
}

// Agenda returns a point-in-time view of pending activations.
func (s *Session) Agenda(ctx context.Context) (Agenda, error) {
	agenda, err := s.engineSession().Agenda(ctx)
	if err != nil {
		return Agenda{}, err
	}
	return wrapAgenda(agenda), nil
}

// CurrentFocus returns the current agenda focus.
func (s *Session) CurrentFocus() ModuleName {
	return s.engineSession().CurrentFocus()
}

// FocusStack returns a copy of the focus stack from bottom to top.
func (s *Session) FocusStack() []ModuleName {
	return s.engineSession().FocusStack()
}

// PushFocus places module on top of the focus stack unless it is already
// current.
func (s *Session) PushFocus(ctx context.Context, module ModuleName) error {
	return s.engineSession().PushFocus(ctx, module)
}

// SetFocus has the same practical behavior as PushFocus.
func (s *Session) SetFocus(ctx context.Context, module ModuleName) error {
	return s.engineSession().SetFocus(ctx, module)
}

// PopFocus removes and returns the current focus frame.
func (s *Session) PopFocus(ctx context.Context) (ModuleName, error) {
	return s.engineSession().PopFocus(ctx)
}

// ClearFocusStack empties the session focus stack.
func (s *Session) ClearFocusStack(ctx context.Context) error {
	return s.engineSession().ClearFocusStack(ctx)
}

// Query returns an iterator over rows produced by a compiled query.
//
// A query whose conditions need backchain-reactive facts generates demand and
// runs the agenda to quiescence, exactly like Run: pending activations
// unrelated to the query may fire with full side effects, and facts derived
// during the proof persist after the query returns. Queries that generate no
// demand have no side effects. Row order is unspecified.
func (s *Session) Query(ctx context.Context, name string, args QueryArgs) (*QueryIterator, error) {
	it, err := s.engineSession().Query(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return wrapQueryIterator(it), nil
}

// QueryAll materializes all rows produced by a compiled query.
//
// A query whose conditions need backchain-reactive facts generates demand and
// runs the agenda to quiescence, exactly like Run: pending activations
// unrelated to the query may fire with full side effects, and facts derived
// during the proof persist after the query returns. Queries that generate no
// demand have no side effects. Row order is unspecified.
func (s *Session) QueryAll(ctx context.Context, name string, args QueryArgs) ([]QueryRow, error) {
	rows, err := s.engineSession().QueryAll(ctx, name, args)
	return wrapQueryRows(rows), err
}

// Explain returns a derivation for fact id using the session explain log.
func (s *Session) Explain(ctx context.Context, id FactID, opts ...ExplainOption) (Derivation, error) {
	derivation, err := s.engineSession().Explain(ctx, id, opts...)
	return wrapDerivation(derivation), err
}

// WhyNot diagnoses why ruleName has no pending activation.
func (s *Session) WhyNot(ctx context.Context, ruleName string, opts ...WhyNotOption) (WhyNotReport, error) {
	report, err := s.engineSession().WhyNot(ctx, ruleName, opts...)
	return wrapWhyNotReport(report), err
}

// WhatIf runs scenario against a fork and returns the counterfactual result.
func (s *Session) WhatIf(ctx context.Context, scenario func(ctx context.Context, fork *Session) error, opts ...WhatIfOption) (WhatIfReport, error) {
	var engineScenario func(context.Context, *engine.Session) error
	if scenario != nil {
		engineScenario = func(ctx context.Context, fork *engine.Session) error {
			return scenario(ctx, wrapSession(fork))
		}
	}
	report, err := s.engineSession().WhatIf(ctx, engineScenario, opts...)
	return wrapWhatIfReport(report), err
}

// RuntimeDiagnostics returns a point-in-time diagnostic of retained runtime
// memory.
func (s *Session) RuntimeDiagnostics(ctx context.Context) (RuntimeDiagnostics, error) {
	return s.engineSession().RuntimeDiagnostics(ctx)
}
