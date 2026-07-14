package scenario

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

var (
	// ErrRunReportProjection identifies an execution result that cannot be
	// represented by the run-report contract.
	ErrRunReportProjection = errors.New("run report projection failed")
	// ErrRunReportTooLarge identifies a report whose fixed envelope alone is
	// larger than maxReportBytes after all bounded detail has been removed.
	ErrRunReportTooLarge = errors.New("run report minimum envelope exceeds limit")
)

// ReportMetadata identifies the producer and Gess build recorded in a report.
type ReportMetadata struct {
	Producer BuildInfo
	Engine   BuildInfo
}

// BuildRunReport projects a completed runner result into the versioned report
// DTO. executionErr is the error returned alongside execution, if any.
func BuildRunReport(document Scenario, execution ExecutionResult, executionErr error, metadata ReportMetadata) (RunReport, error) {
	if err := ValidateScenario(document); err != nil {
		return RunReport{}, projectionError(err)
	}
	if execution.Run.RunID.IsZero() {
		return RunReport{}, projectionError(errors.New("execution has no run ID"))
	}
	if execution.ScenarioDigest == "" || execution.RulesetID == "" {
		return RunReport{}, projectionError(errors.New("execution is missing scenario or ruleset identity"))
	}
	expectedDigest, err := ScenarioDigest(document)
	if err != nil {
		return RunReport{}, projectionError(err)
	}
	if execution.ScenarioDigest != expectedDigest {
		return RunReport{}, projectionError(errors.New("execution scenario digest does not match document"))
	}
	if !validExecutionOutcome(execution.Outcome) {
		return RunReport{}, projectionError(fmt.Errorf("unknown execution outcome %q", execution.Outcome))
	}
	if execution.CallbackProfile != document.CallbackProfile || execution.Limits.Run != document.Run || execution.Limits.Report != document.ReportLimits {
		return RunReport{}, projectionError(errors.New("execution configuration does not match document"))
	}
	if len(execution.Sources) != len(document.Sources) {
		return RunReport{}, projectionError(errors.New("execution sources do not match document"))
	}
	for i, source := range document.Sources {
		if execution.Sources[i].Path != source.Path {
			return RunReport{}, projectionError(errors.New("execution source order does not match document"))
		}
	}

	terminal := projectTerminal(execution, executionErr)
	rulesByRevision := make(map[rules.RuleRevisionID]rules.Rule, len(execution.Rules))
	for _, rule := range execution.Rules {
		rulesByRevision[rule.RevisionID()] = rules.CloneRule(rule)
	}
	firings, err := projectFirings(execution, rulesByRevision)
	if err != nil {
		return RunReport{}, projectionError(err)
	}
	queries, err := projectQueries(execution)
	if err != nil {
		return RunReport{}, projectionError(err)
	}

	report := RunReport{
		SchemaVersion:   RunReportSchemaVersion,
		Producer:        metadata.Producer,
		Engine:          metadata.Engine,
		Sources:         append([]ResolvedSource(nil), execution.Sources...),
		ScenarioDigest:  execution.ScenarioDigest,
		RulesetID:       execution.RulesetID.String(),
		CallbackProfile: execution.CallbackProfile,
		Limits:          execution.Limits,
		Terminal:        terminal,
		Output:          projectOutput(execution),
		Facts:           projectFacts(execution),
		Firings:         firings,
		Events:          projectEvents(execution),
		Queries:         queries,
		Diagnostics:     projectDiagnostics(execution, executionErr),
		Counters:        projectCounters(execution),
		Checks:          projectChecks(document, execution, terminal),
		ExplanationRefs: ArtifactReferenceCollection{
			Status: SectionStatus{Availability: SectionUnsupported, Reason: "explanations_not_captured"},
			Limit:  execution.Limits.Report.MaxExplanationRefs,
			Items:  []ArtifactReference{},
		},
	}
	return fitRunReport(report)
}

func validExecutionOutcome(outcome ExecutionOutcome) bool {
	switch outcome {
	case OutcomeCompleted, OutcomeMaxFacts, OutcomeMaxFirings, OutcomeDeadline, OutcomeCanceled, OutcomeHalted, OutcomeValidationError, OutcomeRuntimeError:
		return true
	default:
		return false
	}
}

func projectTerminal(execution ExecutionResult, executionErr error) TerminalResult {
	terminal := TerminalResult{
		Status: terminalStatus(execution.Outcome),
		RunID:  execution.Run.RunID.String(),
		Fired:  int64(execution.Run.Fired),
	}
	if terminal.Status == TerminalError {
		terminal.Error = &ErrorPayload{
			Code:    string(execution.Outcome),
			Message: executionErrorMessage(execution, executionErr),
		}
	}
	return terminal
}

func terminalStatus(outcome ExecutionOutcome) TerminalStatus {
	switch outcome {
	case OutcomeCompleted:
		return TerminalQuiescent
	case OutcomeMaxFacts:
		return TerminalMaxFacts
	case OutcomeMaxFirings:
		return TerminalMaxFirings
	case OutcomeDeadline:
		return TerminalDeadline
	case OutcomeCanceled:
		return TerminalCanceled
	case OutcomeHalted:
		return TerminalHalted
	default:
		return TerminalError
	}
}

func executionErrorMessage(execution ExecutionResult, executionErr error) string {
	if executionErr != nil {
		return safeReportText(executionErr.Error(), "scenario execution failed")
	}
	if execution.Outcome == OutcomeValidationError {
		return "scenario validation failed"
	}
	return "scenario runtime failed"
}

func projectOutput(execution ExecutionResult) Output {
	return Output{
		Status:        availableSection(),
		LimitBytes:    execution.Output.LimitBytes,
		TotalBytes:    execution.Output.TotalBytes,
		TotalKnown:    true,
		ReturnedBytes: int64(len([]byte(execution.Output.Text))),
		Truncated:     execution.Output.Truncated,
		Text:          strings.Clone(execution.Output.Text),
	}
}

func projectFacts(execution ExecutionResult) FactCollection {
	items := make([]Fact, len(execution.Facts.Items))
	for i, fact := range execution.Facts.Items {
		items[i] = projectFact(fact)
	}
	return FactCollection{
		Status:     availableSection(),
		Limit:      execution.Limits.Report.MaxFacts,
		Total:      execution.Facts.Total,
		TotalKnown: true,
		Returned:   int64(len(items)),
		Truncated:  execution.Facts.Total > int64(len(items)),
		Items:      items,
	}
}

func projectFact(fact session.FactSnapshot) Fact {
	fields := fact.Fields()
	values := make(map[string]Value, len(fields))
	for name, value := range fields {
		values[name] = NewValue(value)
	}
	presenceValues := fact.FieldPresenceMap()
	presence := make(map[string]FieldPresence, len(presenceValues))
	for name, value := range presenceValues {
		presence[name] = FieldPresence(value)
	}
	name := fact.Name()
	template := fact.TemplateKey().String()
	if template != "" {
		name = ""
	}
	return Fact{
		ID:            fact.ID().String(),
		Name:          name,
		Template:      template,
		Version:       NewDecimalUint64(uint64(fact.Version())),
		Recency:       NewDecimalUint64(uint64(fact.Recency())),
		Generation:    NewDecimalUint64(uint64(fact.Generation())),
		Sequence:      NewDecimalUint64(fact.ID().Sequence()),
		Support:       FactSupport(fact.Support().State),
		Fields:        values,
		FieldPresence: presence,
	}
}

func projectFirings(execution ExecutionResult, definitions map[rules.RuleRevisionID]rules.Rule) (FiringCollection, error) {
	items := make([]Firing, 0, len(execution.Firings.Items))
	for _, event := range execution.Firings.Items {
		definition, ok := definitions[event.RuleRevisionID]
		if !ok {
			return FiringCollection{}, fmt.Errorf("firing %d has no rule definition for %s", event.Sequence, event.RuleRevisionID)
		}
		items = append(items, Firing{
			Sequence:       NewDecimalUint64(event.Sequence),
			RunID:          event.RunID.String(),
			ActivationID:   event.ActivationID.String(),
			RuleID:         event.RuleID.String(),
			RuleRevisionID: event.RuleRevisionID.String(),
			RuleName:       definition.Name(),
			Module:         definition.Module().String(),
			Salience:       int64(definition.Salience()),
			Source:         projectSourceSpan(definition.Source()),
			FactIDs:        projectFactIDs(event.RelatedFactIDs()),
		})
	}
	return FiringCollection{
		Status:     availableSection(),
		Limit:      execution.Limits.Report.MaxFirings,
		Total:      execution.Firings.Total,
		TotalKnown: true,
		Returned:   int64(len(items)),
		Truncated:  execution.Firings.Total > int64(len(items)),
		Items:      items,
	}, nil
}

func projectEvents(execution ExecutionResult) EventCollection {
	items := make([]Event, len(execution.Events.Items))
	for i, event := range execution.Events.Items {
		severity := Severity(event.Severity)
		if severity == "" {
			severity = SeverityInfo
		}
		projected := Event{
			Sequence:       NewDecimalUint64(event.Sequence),
			RunID:          event.RunID.String(),
			Type:           EventType(event.Type),
			Severity:       severity,
			Generation:     NewDecimalUint64(uint64(event.Generation)),
			Recency:        NewDecimalUint64(uint64(event.Recency)),
			RuleID:         optionalRuleID(event.RuleID),
			RuleRevisionID: optionalRuleRevisionID(event.RuleRevisionID),
			ActivationID:   optionalActivationID(event.ActivationID),
			Source:         projectSourceSpan(event.Source),
			FactIDs:        projectFactIDs(event.RelatedFactIDs()),
		}
		if event.Type == session.EventActionFailed {
			actionIndex := int64(event.ActionIndex)
			projected.ActionName = event.ActionName
			projected.ActionIndex = &actionIndex
			projected.Error = &ErrorPayload{
				Code:    "action_failed",
				Message: safeReportText(errorString(event.Cause), "rule action failed"),
				Span:    projectSourceSpan(event.Source),
			}
		}
		items[i] = projected
	}
	return EventCollection{
		Status:     availableSection(),
		Limit:      execution.Limits.Report.MaxEvents,
		Total:      execution.Events.Total,
		TotalKnown: true,
		Returned:   int64(len(items)),
		Truncated:  execution.Events.Total > int64(len(items)),
		Items:      items,
	}
}

func projectQueries(execution ExecutionResult) ([]QueryResult, error) {
	queries := make([]QueryResult, len(execution.Queries))
	for i, query := range execution.Queries {
		args := make(map[string]Value, len(query.Query.Args))
		for name, value := range query.Query.Args {
			args[name] = NewValue(value.RulesValue())
		}
		rows := make([]QueryRow, len(query.Rows))
		for rowIndex, row := range query.Rows {
			aliases := row.Aliases()
			cells := make([]QueryCell, 0, len(aliases))
			for _, alias := range aliases {
				fact, hasFact := row.Fact(alias)
				value, hasValue := row.Value(alias)
				if hasFact == hasValue {
					return nil, fmt.Errorf("query %q row %d alias %q does not have exactly one fact or value", query.Query.Name, rowIndex, alias)
				}
				if hasFact {
					factID := fact.ID().String()
					cells = append(cells, QueryCell{Alias: alias, FactID: &factID})
					continue
				}
				if hasValue {
					projected := NewValue(value)
					cells = append(cells, QueryCell{Alias: alias, Value: &projected})
				}
			}
			rows[rowIndex] = QueryRow{Cells: cells}
		}
		queries[i] = QueryResult{
			Name:    query.Query.Name,
			Args:    args,
			MaxRows: query.Query.MaxRows,
			Rows: QueryRowCollection{
				Status:     availableSection(),
				Limit:      query.Query.MaxRows,
				Total:      query.Total,
				TotalKnown: true,
				Returned:   int64(len(rows)),
				Truncated:  query.Total > int64(len(rows)),
				Items:      rows,
			},
		}
	}
	return queries, nil
}

func projectDiagnostics(execution ExecutionResult, executionErr error) DiagnosticCollection {
	severity := SeverityInfo
	message := "scenario completed"
	if execution.Outcome != OutcomeCompleted && execution.Outcome != OutcomeHalted && execution.Outcome != OutcomeMaxFirings {
		severity = SeverityError
		message = executionErrorMessage(execution, executionErr)
	}
	diagnostics := []Diagnostic{{
		ID:       "diagnostic:terminal",
		Phase:    string(execution.Stage),
		Severity: severity,
		Code:     string(execution.Outcome),
		Message:  message,
		Target:   execution.Run.RunID.String(),
	}}
	limit := execution.Limits.Report.MaxDiagnostics
	items := diagnostics[:min(int64(len(diagnostics)), limit)]
	return DiagnosticCollection{
		Status:     availableSection(),
		Limit:      limit,
		Total:      int64(len(diagnostics)),
		TotalKnown: true,
		Returned:   int64(len(items)),
		Truncated:  len(items) < len(diagnostics),
		Items:      append([]Diagnostic(nil), items...),
	}
}

func projectCounters(execution ExecutionResult) CounterCollection {
	var queryRows int64
	for _, query := range execution.Queries {
		queryRows += query.Total
	}
	counters := []Counter{
		{Name: "events", Value: NewDecimalUint64(uint64(execution.Events.Total)), Unit: "count"},
		{Name: "facts", Value: NewDecimalUint64(uint64(execution.Facts.Total)), Unit: "count"},
		{Name: "firings", Value: NewDecimalUint64(uint64(execution.Firings.Total)), Unit: "count"},
		{Name: "output_bytes", Value: NewDecimalUint64(uint64(execution.Output.TotalBytes)), Unit: "bytes"},
		{Name: "query_rows", Value: NewDecimalUint64(uint64(queryRows)), Unit: "count"},
	}
	sort.Slice(counters, func(i, j int) bool { return counters[i].Name < counters[j].Name })
	limit := execution.Limits.Report.MaxCounters
	items := counters[:min(int64(len(counters)), limit)]
	return CounterCollection{
		Status:     availableSection(),
		Limit:      limit,
		Total:      int64(len(counters)),
		TotalKnown: true,
		Returned:   int64(len(items)),
		Truncated:  len(items) < len(counters),
		Items:      append([]Counter(nil), items...),
	}
}

func projectChecks(document Scenario, execution ExecutionResult, terminal TerminalResult) CheckCollection {
	limit := execution.Limits.Report.MaxChecks
	if document.Expectations == nil {
		return CheckCollection{
			Status: SectionStatus{Availability: SectionOmitted, Reason: "no_expectations"},
			Limit:  limit,
			Items:  []CheckResult{},
		}
	}
	expectations := document.Expectations
	checks := []CheckResult{newCheck(
		"expectations.terminalStatus",
		string(expectations.TerminalStatus),
		string(terminal.Status),
	)}
	if expectations.FactCount != nil {
		checks = append(checks, newCheck("expectations.factCount", strconv.FormatInt(*expectations.FactCount, 10), strconv.FormatInt(execution.Facts.Total, 10)))
	}
	if expectations.FiringCount != nil {
		checks = append(checks, newCheck("expectations.firingCount", strconv.FormatInt(*expectations.FiringCount, 10), strconv.FormatInt(execution.Firings.Total, 10)))
	}
	queryTotals := make(map[string]int64, len(execution.Queries))
	for _, query := range execution.Queries {
		queryTotals[query.Query.Name] = query.Total
	}
	queryNames := make([]string, 0, len(expectations.QueryRowCounts))
	for name := range expectations.QueryRowCounts {
		queryNames = append(queryNames, name)
	}
	sort.Strings(queryNames)
	for _, name := range queryNames {
		checks = append(checks, newCheck(
			"expectations.queryRowCounts."+name,
			strconv.FormatInt(expectations.QueryRowCounts[name], 10),
			strconv.FormatInt(queryTotals[name], 10),
		))
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Path < checks[j].Path })
	items := checks[:min(int64(len(checks)), limit)]
	return CheckCollection{
		Status:     availableSection(),
		Limit:      limit,
		Total:      int64(len(checks)),
		TotalKnown: true,
		Returned:   int64(len(items)),
		Truncated:  len(items) < len(checks),
		Items:      append([]CheckResult(nil), items...),
	}
}

func newCheck(path, expected, actual string) CheckResult {
	check := CheckResult{Path: path, Expected: expected, Actual: actual, Passed: expected == actual}
	if !check.Passed {
		check.Message = fmt.Sprintf("expected %s, got %s", expected, actual)
	}
	return check
}

func fitRunReport(report RunReport) (RunReport, error) {
	for {
		normalized, err := normalizeRunReport(report)
		if err != nil {
			return RunReport{}, projectionError(err)
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return RunReport{}, projectionError(err)
		}
		if int64(len(encoded)) <= normalized.Limits.Report.MaxReportBytes {
			return normalized, nil
		}
		report = normalized
		if !trimRunReport(&report, int64(len(encoded))-normalized.Limits.Report.MaxReportBytes) {
			return RunReport{}, fmt.Errorf("%w: encoded size %d, limit %d", ErrRunReportTooLarge, len(encoded), normalized.Limits.Report.MaxReportBytes)
		}
	}
}

func trimRunReport(report *RunReport, excess int64) bool {
	if trimArtifactReferences(&report.ExplanationRefs) || trimDiagnostics(&report.Diagnostics) || trimCounters(&report.Counters) || trimChecks(&report.Checks) || trimEvents(&report.Events) || trimFacts(&report.Facts) || trimFirings(&report.Firings) {
		return true
	}
	for i := len(report.Queries) - 1; i >= 0; i-- {
		if trimQueryRows(&report.Queries[i].Rows) {
			return true
		}
	}
	if report.Output.Text != "" {
		data := []byte(report.Output.Text)
		keep := max(0, len(data)-max(1, int(excess)))
		for keep > 0 && !utf8.Valid(data[:keep]) {
			keep--
		}
		report.Output.Text = string(data[:keep])
		report.Output.ReturnedBytes = int64(keep)
		report.Output.Truncated = report.Output.ReturnedBytes < report.Output.TotalBytes
		return true
	}
	return false
}

func trimFacts(collection *FactCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimFirings(collection *FiringCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimEvents(collection *EventCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimQueryRows(collection *QueryRowCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimDiagnostics(collection *DiagnosticCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimCounters(collection *CounterCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimChecks(collection *CheckCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func trimArtifactReferences(collection *ArtifactReferenceCollection) bool {
	if len(collection.Items) == 0 {
		return false
	}
	collection.Items = collection.Items[:len(collection.Items)-1]
	collection.Returned = int64(len(collection.Items))
	collection.Truncated = collection.Returned < collection.Total
	return true
}

func availableSection() SectionStatus {
	return SectionStatus{Availability: SectionAvailable}
}

func projectFactIDs(ids []rules.FactID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func projectSourceSpan(span rules.SourceSpan) *SourceSpan {
	if span.Name == "" || span.StartLine <= 0 || span.StartColumn <= 0 || span.EndLine <= 0 || span.EndColumn <= 0 {
		return nil
	}
	return &SourceSpan{
		Path:        span.Name,
		StartLine:   int64(span.StartLine),
		StartColumn: int64(span.StartColumn),
		EndLine:     int64(span.EndLine),
		EndColumn:   int64(span.EndColumn),
	}
}

func optionalRuleID(id rules.RuleID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

func optionalRuleRevisionID(id rules.RuleRevisionID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

func optionalActivationID(id rules.ActivationID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func safeReportText(value, fallback string) string {
	value = strings.ToValidUTF8(value, "�")
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func projectionError(err error) error {
	return fmt.Errorf("%w: %w", ErrRunReportProjection, err)
}
