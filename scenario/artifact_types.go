package scenario

import "errors"

// ScenarioSchemaVersion and RunReportSchemaVersion identify the v1 artifact formats.
const (
	ScenarioSchemaVersion  = "gess.workbench.scenario.v1"
	RunReportSchemaVersion = "gess.workbench.report.v1"
)

// ErrInvalidScenario and the related sentinels classify artifact validation failures.
var (
	ErrInvalidScenario             = errors.New("invalid scenario")
	ErrUnsupportedScenarioVersion  = errors.New("unsupported scenario version")
	ErrInvalidRunReport            = errors.New("invalid run report")
	ErrUnsupportedRunReportVersion = errors.New("unsupported run report version")
)

// Strategy selects agenda conflict resolution order.
type Strategy string

// StrategyDepth and StrategyBreadth are the supported agenda strategies.
const (
	StrategyDepth   Strategy = "depth"
	StrategyBreadth Strategy = "breadth"
)

// TerminalStatus describes why scenario execution stopped.
type TerminalStatus string

// TerminalQuiescent and the other terminal constants are valid execution outcomes.
const (
	TerminalQuiescent  TerminalStatus = "quiescent"
	TerminalMaxFacts   TerminalStatus = "max_facts"
	TerminalMaxFirings TerminalStatus = "max_firings"
	TerminalDeadline   TerminalStatus = "deadline"
	TerminalError      TerminalStatus = "error"
	TerminalCanceled   TerminalStatus = "canceled"
	TerminalHalted     TerminalStatus = "halted"
)

// Severity classifies diagnostic and event importance.
type Severity string

// SeverityInfo, SeverityWarning, and SeverityError are valid severity levels.
const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// FactSupport describes how a reported fact is supported.
type FactSupport string

// FactSupportStated and the related constants are valid fact support modes.
const (
	FactSupportStated           FactSupport = "stated"
	FactSupportLogical          FactSupport = "logical"
	FactSupportStatedAndLogical FactSupport = "stated_and_logical"
	FactSupportMetadataOnly     FactSupport = "metadata_only"
)

// FieldPresence describes how a reported template field obtained its value.
type FieldPresence string

// FieldPresenceOmitted, FieldPresenceDefault, and FieldPresenceExplicit are
// the valid template field-presence values.
const (
	FieldPresenceOmitted  FieldPresence = "omitted"
	FieldPresenceDefault  FieldPresence = "default"
	FieldPresenceExplicit FieldPresence = "explicit"
)

// EventType identifies a reported runtime event.
type EventType string

// EventFactAsserted and the related constants are valid runtime event types.
const (
	EventFactAsserted          EventType = "fact_asserted"
	EventFactModified          EventType = "fact_modified"
	EventFactRetracted         EventType = "fact_retracted"
	EventReset                 EventType = "reset"
	EventRuleActivated         EventType = "rule_activated"
	EventRuleDeactivated       EventType = "rule_deactivated"
	EventRuleFired             EventType = "rule_fired"
	EventActionFailed          EventType = "action_failed"
	EventLogicalSupportAdded   EventType = "logical_support_added"
	EventLogicalSupportRemoved EventType = "logical_support_removed"
)

// ScenarioSource identifies an ordered portable rule source.
type ScenarioSource struct {
	Path   string `json:"path"`
	Digest string `json:"digest,omitempty"`
}

// InitialFact describes a typed fact asserted before execution.
type InitialFact struct {
	Template string           `json:"template"`
	Fields   map[string]Value `json:"fields"`
}

// CallbackProfile identifies the callback surface used by a scenario or report.
type CallbackProfile struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// RunOptions contains bounded execution settings.
type RunOptions struct {
	Strategy   Strategy `json:"strategy"`
	MaxFacts   int64    `json:"maxFacts"`
	MaxFirings int64    `json:"maxFirings"`
	DeadlineMS int64    `json:"deadlineMs"`
}

// InputLimits bounds accepted scenario request content.
type InputLimits struct {
	MaxRequestBytes    int64 `json:"maxRequestBytes"`
	MaxSourceFiles     int64 `json:"maxSourceFiles"`
	MaxSourceFileBytes int64 `json:"maxSourceFileBytes"`
	MaxInitialFacts    int64 `json:"maxInitialFacts"`
}

// ReportLimits bounds report sections and encoded report size.
type ReportLimits struct {
	MaxFacts           int64 `json:"maxFacts"`
	MaxFirings         int64 `json:"maxFirings"`
	MaxEvents          int64 `json:"maxEvents"`
	MaxQueryRows       int64 `json:"maxQueryRows"`
	MaxDiagnostics     int64 `json:"maxDiagnostics"`
	MaxCounters        int64 `json:"maxCounters"`
	MaxChecks          int64 `json:"maxChecks"`
	MaxExplanationRefs int64 `json:"maxExplanationRefs"`
	MaxOutputBytes     int64 `json:"maxOutputBytes"`
	MaxReportBytes     int64 `json:"maxReportBytes"`
}

// ScenarioQuery describes an ordered query and its typed arguments.
type ScenarioQuery struct {
	Name    string           `json:"name"`
	Args    map[string]Value `json:"args"`
	MaxRows int64            `json:"maxRows"`
}

// Expectations contains optional assertions checked against a run report.
type Expectations struct {
	TerminalStatus TerminalStatus   `json:"terminalStatus"`
	FactCount      *int64           `json:"factCount,omitempty"`
	FiringCount    *int64           `json:"firingCount,omitempty"`
	QueryRowCounts map[string]int64 `json:"queryRowCounts"`
}

// Scenario is the portable, versioned execution input artifact.
type Scenario struct {
	SchemaVersion   string           `json:"schemaVersion"`
	Name            string           `json:"name"`
	Sources         []ScenarioSource `json:"sources"`
	InitialFacts    []InitialFact    `json:"initialFacts"`
	Deffacts        []string         `json:"deffacts"`
	Globals         map[string]Value `json:"globals"`
	CallbackProfile CallbackProfile  `json:"callbackProfile"`
	Run             RunOptions       `json:"run"`
	ReportLimits    ReportLimits     `json:"reportLimits"`
	Queries         []ScenarioQuery  `json:"queries"`
	Expectations    *Expectations    `json:"expectations,omitempty"`
}

// BuildInfo identifies a report producer or engine build.
type BuildInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ResolvedSource records a portable source path and required content digest.
type ResolvedSource struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// AppliedLimits echoes the input, execution, and report limits used for a run.
type AppliedLimits struct {
	Input  InputLimits  `json:"input"`
	Run    RunOptions   `json:"run"`
	Report ReportLimits `json:"report"`
}

// SourceSpan identifies a portable source range.
type SourceSpan struct {
	Path        string `json:"path"`
	StartLine   int64  `json:"startLine"`
	StartColumn int64  `json:"startColumn"`
	EndLine     int64  `json:"endLine"`
	EndColumn   int64  `json:"endColumn"`
}

// ErrorPayload describes a structured terminal or event error.
type ErrorPayload struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Span    *SourceSpan `json:"span,omitempty"`
}

// TerminalResult records the outcome of scenario execution.
type TerminalResult struct {
	Status TerminalStatus `json:"status"`
	RunID  string         `json:"runId"`
	Fired  int64          `json:"fired"`
	Error  *ErrorPayload  `json:"error,omitempty"`
}

// Fact is a stable reported fact snapshot.
type Fact struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Template      string                   `json:"template"`
	Version       DecimalUint64            `json:"version"`
	Recency       DecimalUint64            `json:"recency"`
	Generation    DecimalUint64            `json:"generation"`
	Sequence      DecimalUint64            `json:"sequence"`
	Support       FactSupport              `json:"support"`
	Fields        map[string]Value         `json:"fields"`
	FieldPresence map[string]FieldPresence `json:"fieldPresence"`
}

// Firing records one ordered rule firing.
type Firing struct {
	Sequence       DecimalUint64 `json:"sequence"`
	RunID          string        `json:"runId"`
	ActivationID   string        `json:"activationId"`
	RuleID         string        `json:"ruleId"`
	RuleRevisionID string        `json:"ruleRevisionId"`
	RuleName       string        `json:"ruleName"`
	Module         string        `json:"module"`
	Salience       int64         `json:"salience"`
	Source         *SourceSpan   `json:"source,omitempty"`
	FactIDs        []string      `json:"factIds"`
}

// Event records one ordered runtime event.
type Event struct {
	Sequence       DecimalUint64 `json:"sequence"`
	RunID          string        `json:"runId"`
	Type           EventType     `json:"type"`
	Severity       Severity      `json:"severity"`
	Generation     DecimalUint64 `json:"generation"`
	Recency        DecimalUint64 `json:"recency"`
	RuleID         string        `json:"ruleId"`
	RuleRevisionID string        `json:"ruleRevisionId"`
	ActivationID   string        `json:"activationId"`
	Source         *SourceSpan   `json:"source,omitempty"`
	ActionName     string        `json:"actionName"`
	ActionIndex    *int64        `json:"actionIndex,omitempty"`
	FactIDs        []string      `json:"factIds"`
	Error          *ErrorPayload `json:"error,omitempty"`
}

// QueryCell contains either a fact reference or a typed value under an alias.
type QueryCell struct {
	Alias  string  `json:"alias"`
	FactID *string `json:"factId,omitempty"`
	Value  *Value  `json:"value,omitempty"`
}

// QueryRow contains ordered, aliased query cells.
type QueryRow struct {
	Cells []QueryCell `json:"cells"`
}

// Diagnostic is a stable structured report diagnostic.
type Diagnostic struct {
	ID        string      `json:"id"`
	Phase     string      `json:"phase"`
	Severity  Severity    `json:"severity"`
	Code      string      `json:"code"`
	Message   string      `json:"message"`
	Target    string      `json:"target"`
	Span      *SourceSpan `json:"span,omitempty"`
	Retryable bool        `json:"retryable"`
}

// Counter is a named non-negative runtime measurement.
type Counter struct {
	Name  string        `json:"name"`
	Value DecimalUint64 `json:"value"`
	Unit  string        `json:"unit"`
}

// CheckResult records one evaluated scenario expectation.
type CheckResult struct {
	Path     string `json:"path"`
	Passed   bool   `json:"passed"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Message  string `json:"message"`
}

// ArtifactReference identifies a related content-addressed artifact.
type ArtifactReference struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	SchemaVersion string `json:"schemaVersion"`
	Digest        string `json:"digest"`
}

// SectionAvailability describes whether a report section was produced.
type SectionAvailability string

// SectionAvailable, SectionOmitted, and SectionUnsupported are valid availability states.
const (
	SectionAvailable   SectionAvailability = "available"
	SectionOmitted     SectionAvailability = "omitted"
	SectionUnsupported SectionAvailability = "unsupported"
)

// SectionStatus explains the availability of a bounded report section.
type SectionStatus struct {
	Availability SectionAvailability `json:"availability"`
	Reason       string              `json:"reason"`
}

// FactCollection is a bounded report fact section.
type FactCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []Fact        `json:"items"`
}

// FiringCollection is a bounded report firing section.
type FiringCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []Firing      `json:"items"`
}

// EventCollection is a bounded report event section.
type EventCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []Event       `json:"items"`
}

// QueryRowCollection is a bounded query result row section.
type QueryRowCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []QueryRow    `json:"items"`
}

// DiagnosticCollection is a bounded report diagnostic section.
type DiagnosticCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []Diagnostic  `json:"items"`
}

// CounterCollection is a bounded report counter section.
type CounterCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []Counter     `json:"items"`
}

// CheckCollection is a bounded expectation check section.
type CheckCollection struct {
	Status     SectionStatus `json:"status"`
	Limit      int64         `json:"limit"`
	Total      int64         `json:"total"`
	TotalKnown bool          `json:"totalKnown"`
	Returned   int64         `json:"returned"`
	Truncated  bool          `json:"truncated"`
	Items      []CheckResult `json:"items"`
}

// ArtifactReferenceCollection is a bounded explanation reference section.
type ArtifactReferenceCollection struct {
	Status     SectionStatus       `json:"status"`
	Limit      int64               `json:"limit"`
	Total      int64               `json:"total"`
	TotalKnown bool                `json:"totalKnown"`
	Returned   int64               `json:"returned"`
	Truncated  bool                `json:"truncated"`
	Items      []ArtifactReference `json:"items"`
}

// QueryResult records one ordered scenario query result.
type QueryResult struct {
	Name    string             `json:"name"`
	Args    map[string]Value   `json:"args"`
	MaxRows int64              `json:"maxRows"`
	Rows    QueryRowCollection `json:"rows"`
}

// Output is a byte-bounded captured output section.
type Output struct {
	Status        SectionStatus `json:"status"`
	LimitBytes    int64         `json:"limitBytes"`
	TotalBytes    int64         `json:"totalBytes"`
	TotalKnown    bool          `json:"totalKnown"`
	ReturnedBytes int64         `json:"returnedBytes"`
	Truncated     bool          `json:"truncated"`
	Text          string        `json:"text"`
}

// RunReport is the portable, versioned execution result artifact.
type RunReport struct {
	SchemaVersion   string                      `json:"schemaVersion"`
	Producer        BuildInfo                   `json:"producer"`
	Engine          BuildInfo                   `json:"engine"`
	Sources         []ResolvedSource            `json:"sources"`
	ScenarioDigest  string                      `json:"scenarioDigest"`
	RulesetID       string                      `json:"rulesetId"`
	CallbackProfile CallbackProfile             `json:"callbackProfile"`
	Limits          AppliedLimits               `json:"limits"`
	Terminal        TerminalResult              `json:"terminal"`
	Output          Output                      `json:"output"`
	Facts           FactCollection              `json:"facts"`
	Firings         FiringCollection            `json:"firings"`
	Events          EventCollection             `json:"events"`
	Queries         []QueryResult               `json:"queries"`
	Diagnostics     DiagnosticCollection        `json:"diagnostics"`
	Counters        CounterCollection           `json:"counters"`
	Checks          CheckCollection             `json:"checks"`
	ExplanationRefs ArtifactReferenceCollection `json:"explanationRefs"`
}
