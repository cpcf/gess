package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateScenario reports whether document is a valid scenario artifact.
func ValidateScenario(document Scenario) error {
	_, err := normalizeScenario(document)
	return err
}

// ValidateRunReport reports whether document is a valid run-report artifact.
func ValidateRunReport(document RunReport) error {
	normalized, err := normalizeRunReport(document)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return invalidRunReportf("encode canonical report: %v", err)
	}
	return validateEncodedRunReportSize(normalized, encoded)
}

func normalizeScenario(document Scenario) (Scenario, error) {
	invalid := invalidScenariof
	if document.SchemaVersion != ScenarioSchemaVersion {
		return Scenario{}, invalid("schemaVersion must be %q", ScenarioSchemaVersion)
	}
	if err := validateIdentifier("name", document.Name, invalid); err != nil {
		return Scenario{}, err
	}

	out := document
	out.Sources = make([]ScenarioSource, len(document.Sources))
	seenSources := make(map[string]struct{}, len(document.Sources))
	for i, source := range document.Sources {
		itemPath := fmt.Sprintf("sources[%d]", i)
		if err := validatePortablePath(itemPath+".path", source.Path, invalid); err != nil {
			return Scenario{}, err
		}
		if _, duplicate := seenSources[source.Path]; duplicate {
			return Scenario{}, invalid("%s.path duplicates %q", itemPath, source.Path)
		}
		seenSources[source.Path] = struct{}{}
		if source.Digest != "" {
			if err := validateDigest(itemPath+".digest", source.Digest, invalid); err != nil {
				return Scenario{}, err
			}
		}
		out.Sources[i] = source
	}

	out.InitialFacts = make([]InitialFact, len(document.InitialFacts))
	for i, fact := range document.InitialFacts {
		itemPath := fmt.Sprintf("initialFacts[%d]", i)
		if err := validateIdentifier(itemPath+".template", fact.Template, invalid); err != nil {
			return Scenario{}, err
		}
		fields, err := normalizeValueMap(fact.Fields, itemPath+".fields", invalid)
		if err != nil {
			return Scenario{}, err
		}
		out.InitialFacts[i] = fact
		out.InitialFacts[i].Fields = fields
	}

	out.Deffacts = make([]string, len(document.Deffacts))
	seenDeffacts := make(map[string]struct{}, len(document.Deffacts))
	for i, name := range document.Deffacts {
		itemPath := fmt.Sprintf("deffacts[%d]", i)
		if err := validateIdentifier(itemPath, name, invalid); err != nil {
			return Scenario{}, err
		}
		if _, duplicate := seenDeffacts[name]; duplicate {
			return Scenario{}, invalid("%s duplicates %q", itemPath, name)
		}
		seenDeffacts[name] = struct{}{}
		out.Deffacts[i] = name
	}

	globals, err := normalizeValueMap(document.Globals, "globals", invalid)
	if err != nil {
		return Scenario{}, err
	}
	out.Globals = globals

	if err := validateCallbackProfile("callbackProfile", document.CallbackProfile, invalid); err != nil {
		return Scenario{}, err
	}
	if err := validateRunOptions("run", document.Run, invalid); err != nil {
		return Scenario{}, err
	}
	if err := validateReportLimits("reportLimits", document.ReportLimits, invalid); err != nil {
		return Scenario{}, err
	}

	out.Queries = make([]ScenarioQuery, len(document.Queries))
	seenQueries := make(map[string]struct{}, len(document.Queries))
	for i, query := range document.Queries {
		itemPath := fmt.Sprintf("queries[%d]", i)
		if err := validateIdentifier(itemPath+".name", query.Name, invalid); err != nil {
			return Scenario{}, err
		}
		if _, duplicate := seenQueries[query.Name]; duplicate {
			return Scenario{}, invalid("%s.name duplicates %q", itemPath, query.Name)
		}
		seenQueries[query.Name] = struct{}{}
		if err := validatePositiveSafeInteger(itemPath+".maxRows", query.MaxRows, invalid); err != nil {
			return Scenario{}, err
		}
		if query.MaxRows > document.ReportLimits.MaxQueryRows {
			return Scenario{}, invalid("%s.maxRows exceeds reportLimits.maxQueryRows", itemPath)
		}
		args, err := normalizeValueMap(query.Args, itemPath+".args", invalid)
		if err != nil {
			return Scenario{}, err
		}
		out.Queries[i] = query
		out.Queries[i].Args = args
	}

	if document.Expectations == nil {
		out.Expectations = nil
		return out, nil
	}
	expectations := *document.Expectations
	if err := validateTerminalStatus("expectations.terminalStatus", expectations.TerminalStatus, invalid); err != nil {
		return Scenario{}, err
	}
	if expectations.FactCount != nil {
		if err := validateNonnegativeSafeInteger("expectations.factCount", *expectations.FactCount, invalid); err != nil {
			return Scenario{}, err
		}
		value := *expectations.FactCount
		expectations.FactCount = &value
	}
	if expectations.FiringCount != nil {
		if err := validateNonnegativeSafeInteger("expectations.firingCount", *expectations.FiringCount, invalid); err != nil {
			return Scenario{}, err
		}
		value := *expectations.FiringCount
		expectations.FiringCount = &value
	}
	expectations.QueryRowCounts = make(map[string]int64, len(document.Expectations.QueryRowCounts))
	for name, count := range document.Expectations.QueryRowCounts {
		if err := validateIdentifier("expectations.queryRowCounts key", name, invalid); err != nil {
			return Scenario{}, err
		}
		if _, ok := seenQueries[name]; !ok {
			return Scenario{}, invalid("expectations.queryRowCounts names unselected query %q", name)
		}
		if err := validateNonnegativeSafeInteger(fmt.Sprintf("expectations.queryRowCounts[%q]", name), count, invalid); err != nil {
			return Scenario{}, err
		}
		expectations.QueryRowCounts[name] = count
	}
	out.Expectations = &expectations
	return out, nil
}

func normalizeRunReport(document RunReport) (RunReport, error) {
	invalid := invalidRunReportf
	if document.SchemaVersion != RunReportSchemaVersion {
		return RunReport{}, invalid("schemaVersion must be %q", RunReportSchemaVersion)
	}
	if err := validateBuildInfo("producer", document.Producer, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateBuildInfo("engine", document.Engine, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateInputLimits("limits.input", document.Limits.Input, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateRunOptions("limits.run", document.Limits.Run, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateReportLimits("limits.report", document.Limits.Report, invalid); err != nil {
		return RunReport{}, err
	}

	out := document
	if int64(len(document.Sources)) > document.Limits.Input.MaxSourceFiles {
		return RunReport{}, invalid("sources exceeds limits.input.maxSourceFiles")
	}
	out.Sources = make([]ResolvedSource, len(document.Sources))
	seenSources := make(map[string]struct{}, len(document.Sources))
	for i, source := range document.Sources {
		itemPath := fmt.Sprintf("sources[%d]", i)
		if err := validatePortablePath(itemPath+".path", source.Path, invalid); err != nil {
			return RunReport{}, err
		}
		if _, duplicate := seenSources[source.Path]; duplicate {
			return RunReport{}, invalid("%s.path duplicates %q", itemPath, source.Path)
		}
		seenSources[source.Path] = struct{}{}
		if err := validateDigest(itemPath+".digest", source.Digest, invalid); err != nil {
			return RunReport{}, err
		}
		out.Sources[i] = source
	}
	if err := validateDigest("scenarioDigest", document.ScenarioDigest, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateIdentifier("rulesetId", document.RulesetID, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateCallbackProfile("callbackProfile", document.CallbackProfile, invalid); err != nil {
		return RunReport{}, err
	}
	if err := validateTerminalResult(document.Terminal, invalid); err != nil {
		return RunReport{}, err
	}
	out.Terminal = cloneTerminalResult(document.Terminal)

	if err := validateOutput(document.Output, document.Limits.Report.MaxOutputBytes, invalid); err != nil {
		return RunReport{}, err
	}

	facts, err := normalizeFacts(document.Facts, document.Limits.Report.MaxFacts, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Facts = facts
	if facts.Status.Availability == SectionAvailable && facts.Total > document.Limits.Run.MaxFacts {
		return RunReport{}, invalid("facts.total must not exceed limits.run.maxFacts")
	}
	firings, err := normalizeFirings(document.Firings, document.Limits.Report.MaxFirings, document.Terminal.RunID, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Firings = firings
	if firings.Status.Availability == SectionAvailable && document.Terminal.Fired != firings.Total {
		return RunReport{}, invalid("terminal.fired must equal firings.total")
	}
	if document.Terminal.Fired > document.Limits.Run.MaxFirings {
		return RunReport{}, invalid("terminal.fired must not exceed limits.run.maxFirings")
	}
	events, err := normalizeEvents(document.Events, document.Limits.Report.MaxEvents, document.Terminal, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Events = events
	queries, err := normalizeQueryResults(document.Queries, document.Limits.Report.MaxQueryRows, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Queries = queries
	diagnostics, err := normalizeDiagnostics(document.Diagnostics, document.Limits.Report.MaxDiagnostics, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Diagnostics = diagnostics
	counters, err := normalizeCounters(document.Counters, document.Limits.Report.MaxCounters, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Counters = counters
	checks, err := normalizeChecks(document.Checks, document.Limits.Report.MaxChecks, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.Checks = checks
	references, err := normalizeArtifactReferences(document.ExplanationRefs, document.Limits.Report.MaxExplanationRefs, invalid)
	if err != nil {
		return RunReport{}, err
	}
	out.ExplanationRefs = references
	return out, nil
}

type artifactInvalidf func(string, ...any) error

const maxJSONSafeInteger int64 = 1<<53 - 1

func invalidScenariof(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidScenario, fmt.Sprintf(format, args...))
}

func invalidRunReportf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRunReport, fmt.Sprintf(format, args...))
}

func validatePositiveSafeInteger(name string, value int64, invalid artifactInvalidf) error {
	if value <= 0 || value > maxJSONSafeInteger {
		return invalid("%s must be between 1 and %d", name, maxJSONSafeInteger)
	}
	return nil
}

func validateNonnegativeSafeInteger(name string, value int64, invalid artifactInvalidf) error {
	if value < 0 || value > maxJSONSafeInteger {
		return invalid("%s must be between 0 and %d", name, maxJSONSafeInteger)
	}
	return nil
}

func validateSignedSafeInteger(name string, value int64, invalid artifactInvalidf) error {
	if value < -maxJSONSafeInteger || value > maxJSONSafeInteger {
		return invalid("%s must be between %d and %d", name, -maxJSONSafeInteger, maxJSONSafeInteger)
	}
	return nil
}

func validateIdentifier(name, value string, invalid artifactInvalidf) error {
	if !utf8.ValidString(value) {
		return invalid("%s is not valid UTF-8", name)
	}
	if strings.TrimSpace(value) == "" {
		return invalid("%s must not be empty", name)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return invalid("%s must not contain control characters", name)
		}
	}
	return nil
}

func validateOptionalText(name, value string, invalid artifactInvalidf) error {
	if !utf8.ValidString(value) {
		return invalid("%s is not valid UTF-8", name)
	}
	return nil
}

func validateRequiredText(name, value string, invalid artifactInvalidf) error {
	if err := validateOptionalText(name, value, invalid); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return invalid("%s must not be empty", name)
	}
	return nil
}

func validateOptionalIdentifier(name, value string, invalid artifactInvalidf) error {
	if value == "" {
		return nil
	}
	return validateIdentifier(name, value, invalid)
}

func validateDigest(name, value string, invalid artifactInvalidf) error {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return invalid("%s must be a lowercase sha256 digest", name)
	}
	for _, digit := range value[len("sha256:"):] {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f')) {
			return invalid("%s must be a lowercase sha256 digest", name)
		}
	}
	return nil
}

func validatePortablePath(name, value string, invalid artifactInvalidf) error {
	if err := validateIdentifier(name, value, invalid); err != nil {
		return err
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.IsAbs(value) || path.Clean(value) != value || value == "." {
		return invalid("%s must be a normalized relative slash path", name)
	}
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' {
		return invalid("%s must not be an absolute volume path", name)
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return invalid("%s must not contain empty or traversal segments", name)
		}
	}
	return nil
}

func validateCallbackProfile(name string, profile CallbackProfile, invalid artifactInvalidf) error {
	if err := validateIdentifier(name+".name", profile.Name, invalid); err != nil {
		return err
	}
	if err := validateIdentifier(name+".version", profile.Version, invalid); err != nil {
		return err
	}
	return validateDigest(name+".digest", profile.Digest, invalid)
}

func validateBuildInfo(name string, info BuildInfo, invalid artifactInvalidf) error {
	if err := validateIdentifier(name+".name", info.Name, invalid); err != nil {
		return err
	}
	return validateIdentifier(name+".version", info.Version, invalid)
}

func validateRunOptions(name string, options RunOptions, invalid artifactInvalidf) error {
	switch options.Strategy {
	case StrategyDepth, StrategyBreadth:
	default:
		return invalid("%s.strategy has unknown value %q", name, options.Strategy)
	}
	if err := validatePositiveSafeInteger(name+".maxFacts", options.MaxFacts, invalid); err != nil {
		return err
	}
	if err := validatePositiveSafeInteger(name+".maxFirings", options.MaxFirings, invalid); err != nil {
		return err
	}
	return validatePositiveSafeInteger(name+".deadlineMs", options.DeadlineMS, invalid)
}

func validateInputLimits(name string, limits InputLimits, invalid artifactInvalidf) error {
	values := []struct {
		name  string
		value int64
	}{
		{"maxRequestBytes", limits.MaxRequestBytes},
		{"maxSourceFiles", limits.MaxSourceFiles},
		{"maxSourceFileBytes", limits.MaxSourceFileBytes},
		{"maxInitialFacts", limits.MaxInitialFacts},
	}
	for _, item := range values {
		if err := validatePositiveSafeInteger(name+"."+item.name, item.value, invalid); err != nil {
			return err
		}
	}
	return nil
}

func validateReportLimits(name string, limits ReportLimits, invalid artifactInvalidf) error {
	values := []struct {
		name  string
		value int64
	}{
		{"maxFacts", limits.MaxFacts},
		{"maxFirings", limits.MaxFirings},
		{"maxEvents", limits.MaxEvents},
		{"maxQueryRows", limits.MaxQueryRows},
		{"maxDiagnostics", limits.MaxDiagnostics},
		{"maxCounters", limits.MaxCounters},
		{"maxChecks", limits.MaxChecks},
		{"maxExplanationRefs", limits.MaxExplanationRefs},
		{"maxOutputBytes", limits.MaxOutputBytes},
		{"maxReportBytes", limits.MaxReportBytes},
	}
	for _, item := range values {
		if err := validatePositiveSafeInteger(name+"."+item.name, item.value, invalid); err != nil {
			return err
		}
	}
	return nil
}

func validateTerminalStatus(name string, status TerminalStatus, invalid artifactInvalidf) error {
	switch status {
	case TerminalQuiescent, TerminalMaxFacts, TerminalMaxFirings, TerminalDeadline, TerminalError, TerminalCanceled, TerminalHalted:
		return nil
	default:
		return invalid("%s has unknown value %q", name, status)
	}
}

func validateTerminalResult(terminal TerminalResult, invalid artifactInvalidf) error {
	if err := validateTerminalStatus("terminal.status", terminal.Status, invalid); err != nil {
		return err
	}
	if err := validateRunID("terminal.runId", terminal.RunID, false, invalid); err != nil {
		return err
	}
	if err := validateNonnegativeSafeInteger("terminal.fired", terminal.Fired, invalid); err != nil {
		return err
	}
	if terminal.Status == TerminalError {
		if terminal.Error == nil {
			return invalid("terminal.error is required when status is error")
		}
		return validateErrorPayload("terminal.error", *terminal.Error, invalid)
	}
	if terminal.Error != nil {
		return invalid("terminal.error is only permitted when status is error")
	}
	return nil
}

func validateRunID(name, value string, allowZero bool, invalid artifactInvalidf) error {
	if value == "run:zero" {
		if allowZero {
			return nil
		}
		return invalid("%s must identify a nonzero run", name)
	}
	const prefix = "run:"
	if !strings.HasPrefix(value, prefix) {
		return invalid("%s must have canonical run:<decimal> form", name)
	}
	digits := value[len(prefix):]
	if digits == "" || digits[0] == '0' {
		return invalid("%s must have canonical run:<decimal> form", name)
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return invalid("%s must have canonical run:<decimal> form", name)
		}
	}
	if _, err := strconv.ParseUint(digits, 10, 64); err != nil {
		return invalid("%s is outside the uint64 run-ID range", name)
	}
	return nil
}

func validateFactID(name, value string, invalid artifactInvalidf) error {
	const prefix = "fact:g"
	if !strings.HasPrefix(value, prefix) {
		return invalid("%s must have canonical fact:g<generation>:<sequence> form", name)
	}
	generation, sequence, ok := strings.Cut(value[len(prefix):], ":")
	if !ok || !canonicalPositiveDecimal(generation) || !canonicalPositiveDecimal(sequence) {
		return invalid("%s must have canonical fact:g<generation>:<sequence> form", name)
	}
	if _, err := strconv.ParseUint(generation, 10, 64); err != nil {
		return invalid("%s generation is outside the uint64 range", name)
	}
	if _, err := strconv.ParseUint(sequence, 10, 64); err != nil {
		return invalid("%s sequence is outside the uint64 range", name)
	}
	return nil
}

func canonicalPositiveDecimal(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func validateErrorPayload(name string, payload ErrorPayload, invalid artifactInvalidf) error {
	if err := validateIdentifier(name+".code", payload.Code, invalid); err != nil {
		return err
	}
	if err := validateRequiredText(name+".message", payload.Message, invalid); err != nil {
		return err
	}
	if payload.Span != nil {
		return validateSourceSpan(name+".span", *payload.Span, invalid)
	}
	return nil
}

func validateSourceSpan(name string, span SourceSpan, invalid artifactInvalidf) error {
	if err := validatePortablePath(name+".path", span.Path, invalid); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"startLine", span.StartLine}, {"startColumn", span.StartColumn},
		{"endLine", span.EndLine}, {"endColumn", span.EndColumn},
	} {
		if err := validatePositiveSafeInteger(name+"."+field.name, field.value, invalid); err != nil {
			return err
		}
	}
	if span.EndLine < span.StartLine || (span.EndLine == span.StartLine && span.EndColumn < span.StartColumn) {
		return invalid("%s end must not precede start", name)
	}
	return nil
}

func validateOutput(output Output, appliedLimit int64, invalid artifactInvalidf) error {
	if err := validateSectionStatus("output.status", output.Status, invalid); err != nil {
		return err
	}
	if output.LimitBytes != appliedLimit {
		return invalid("output.limitBytes must equal limits.report.maxOutputBytes")
	}
	if !utf8.ValidString(output.Text) {
		return invalid("output.text is not valid UTF-8")
	}
	if output.Status.Availability != SectionAvailable {
		if output.TotalKnown || output.TotalBytes != 0 || output.ReturnedBytes != 0 || output.Truncated || output.Text != "" {
			return invalid("unavailable output must have unknown zero totals, no text, and no truncation")
		}
		return nil
	}
	if !output.TotalKnown {
		return invalid("available output must set totalKnown")
	}
	if err := validateNonnegativeSafeInteger("output.totalBytes", output.TotalBytes, invalid); err != nil {
		return err
	}
	if err := validateNonnegativeSafeInteger("output.returnedBytes", output.ReturnedBytes, invalid); err != nil {
		return err
	}
	if output.ReturnedBytes != int64(len([]byte(output.Text))) {
		return invalid("output.returnedBytes must equal the UTF-8 byte length of output.text")
	}
	if output.ReturnedBytes > output.TotalBytes || output.ReturnedBytes > output.LimitBytes {
		return invalid("output.returnedBytes exceeds totalBytes or limitBytes")
	}
	if output.Truncated != (output.ReturnedBytes < output.TotalBytes) {
		return invalid("output.truncated must equal returnedBytes < totalBytes")
	}
	return nil
}

func validateSectionStatus(name string, status SectionStatus, invalid artifactInvalidf) error {
	switch status.Availability {
	case SectionAvailable:
		if status.Reason != "" {
			return invalid("%s.reason must be empty when availability is available", name)
		}
		return nil
	case SectionOmitted, SectionUnsupported:
		if err := validateIdentifier(name+".reason", status.Reason, invalid); err != nil {
			return err
		}
		return nil
	default:
		return invalid("%s.availability has unknown value %q", name, status.Availability)
	}
}

func validateCollection(name string, status SectionStatus, limit, total int64, totalKnown bool, returned, itemCount, appliedLimit int64, truncated bool, invalid artifactInvalidf) error {
	if err := validateSectionStatus(name+".status", status, invalid); err != nil {
		return err
	}
	if limit != appliedLimit {
		return invalid("%s.limit must equal its applied report limit", name)
	}
	if status.Availability != SectionAvailable {
		if totalKnown || total != 0 || returned != 0 || itemCount != 0 || truncated {
			return invalid("unavailable %s must have unknown zero totals, no items, and no truncation", name)
		}
		return nil
	}
	if !totalKnown {
		return invalid("available %s must set totalKnown", name)
	}
	if err := validateNonnegativeSafeInteger(name+".total", total, invalid); err != nil {
		return err
	}
	if err := validateNonnegativeSafeInteger(name+".returned", returned, invalid); err != nil {
		return err
	}
	if returned != itemCount {
		return invalid("%s.returned must equal len(items)", name)
	}
	if returned > total || returned > limit {
		return invalid("%s.returned exceeds total or limit", name)
	}
	if truncated != (returned < total) {
		return invalid("%s.truncated must equal returned < total", name)
	}
	return nil
}

func normalizeValueMap(values map[string]Value, name string, invalid artifactInvalidf) (map[string]Value, error) {
	out := make(map[string]Value, len(values))
	for key, value := range values {
		if err := validateIdentifier(name+" key", key, invalid); err != nil {
			return nil, err
		}
		if _, err := MarshalValue(value.RulesValue()); err != nil {
			return nil, invalid("%s[%q] has invalid typed value: %v", name, key, err)
		}
		out[key] = NewValue(value.RulesValue())
	}
	return out, nil
}

func normalizeFacts(collection FactCollection, appliedLimit int64, invalid artifactInvalidf) (FactCollection, error) {
	if err := validateCollection("facts", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return FactCollection{}, err
	}
	out := collection
	out.Items = make([]Fact, len(collection.Items))
	seenIDs := make(map[string]struct{}, len(collection.Items))
	type factSequence struct {
		generation DecimalUint64
		sequence   DecimalUint64
	}
	seenSequences := make(map[factSequence]struct{}, len(collection.Items))
	for i, fact := range collection.Items {
		itemPath := fmt.Sprintf("facts.items[%d]", i)
		if err := validateIdentifier(itemPath+".id", fact.ID, invalid); err != nil {
			return FactCollection{}, err
		}
		if _, duplicate := seenIDs[fact.ID]; duplicate {
			return FactCollection{}, invalid("%s.id duplicates %q", itemPath, fact.ID)
		}
		seenIDs[fact.ID] = struct{}{}
		if fact.Version == 0 || fact.Recency == 0 || fact.Generation == 0 || fact.Sequence == 0 {
			return FactCollection{}, invalid("%s version, recency, generation, and sequence must be positive", itemPath)
		}
		expectedID := fmt.Sprintf("fact:g%d:%d", fact.Generation, fact.Sequence)
		if fact.ID != expectedID {
			return FactCollection{}, invalid("%s.id must equal %q for its generation and sequence", itemPath, expectedID)
		}
		identity := factSequence{generation: fact.Generation, sequence: fact.Sequence}
		if _, duplicate := seenSequences[identity]; duplicate {
			return FactCollection{}, invalid("%s generation/sequence duplicates %d/%d", itemPath, fact.Generation, fact.Sequence)
		}
		seenSequences[identity] = struct{}{}
		if fact.Name == "" && fact.Template == "" {
			return FactCollection{}, invalid("%s requires name or template", itemPath)
		}
		if fact.Name != "" {
			if err := validateIdentifier(itemPath+".name", fact.Name, invalid); err != nil {
				return FactCollection{}, err
			}
		}
		if fact.Template != "" {
			if err := validateIdentifier(itemPath+".template", fact.Template, invalid); err != nil {
				return FactCollection{}, err
			}
		}
		if fact.Name != "" && fact.Template != "" {
			return FactCollection{}, invalid("%s name and template are mutually exclusive", itemPath)
		}
		switch fact.Support {
		case FactSupportStated, FactSupportLogical, FactSupportStatedAndLogical, FactSupportMetadataOnly:
		default:
			return FactCollection{}, invalid("%s.support has unknown value %q", itemPath, fact.Support)
		}
		fields, err := normalizeValueMap(fact.Fields, itemPath+".fields", invalid)
		if err != nil {
			return FactCollection{}, err
		}
		presence := make(map[string]FieldPresence, len(fact.FieldPresence))
		for key, value := range fact.FieldPresence {
			if err := validateIdentifier(itemPath+".fieldPresence key", key, invalid); err != nil {
				return FactCollection{}, err
			}
			switch value {
			case FieldPresenceOmitted:
				if _, ok := fields[key]; ok {
					return FactCollection{}, invalid("%s.fieldPresence[%q] is omitted but the field is present", itemPath, key)
				}
			case FieldPresenceDefault, FieldPresenceExplicit:
				if _, ok := fields[key]; !ok {
					return FactCollection{}, invalid("%s.fieldPresence[%q] requires a field value", itemPath, key)
				}
			default:
				return FactCollection{}, invalid("%s.fieldPresence[%q] has unknown value %q", itemPath, key, value)
			}
			presence[key] = value
		}
		if fact.Template != "" {
			for key := range fields {
				if _, ok := presence[key]; !ok {
					return FactCollection{}, invalid("%s.fieldPresence is missing template field %q", itemPath, key)
				}
			}
		} else if len(presence) != 0 {
			return FactCollection{}, invalid("%s dynamic fact must not contain fieldPresence", itemPath)
		}
		out.Items[i] = fact
		out.Items[i].Fields = fields
		out.Items[i].FieldPresence = presence
	}
	sort.Slice(out.Items, func(i, j int) bool {
		left, right := out.Items[i], out.Items[j]
		if left.Generation != right.Generation {
			return left.Generation < right.Generation
		}
		if left.Sequence != right.Sequence {
			return left.Sequence < right.Sequence
		}
		return left.ID < right.ID
	})
	return out, nil
}

func normalizeFirings(collection FiringCollection, appliedLimit int64, runID string, invalid artifactInvalidf) (FiringCollection, error) {
	if err := validateCollection("firings", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return FiringCollection{}, err
	}
	out := collection
	out.Items = make([]Firing, len(collection.Items))
	seenSequences := make(map[DecimalUint64]struct{}, len(collection.Items))
	seenActivations := make(map[string]struct{}, len(collection.Items))
	for i, firing := range collection.Items {
		itemPath := fmt.Sprintf("firings.items[%d]", i)
		if firing.Sequence == 0 {
			return FiringCollection{}, invalid("%s.sequence must be positive", itemPath)
		}
		if _, duplicate := seenSequences[firing.Sequence]; duplicate {
			return FiringCollection{}, invalid("%s.sequence duplicates %d", itemPath, firing.Sequence)
		}
		seenSequences[firing.Sequence] = struct{}{}
		if firing.RunID != runID {
			return FiringCollection{}, invalid("%s.runId must equal terminal.runId", itemPath)
		}
		for _, field := range []struct{ name, value string }{
			{"activationId", firing.ActivationID},
			{"ruleId", firing.RuleID},
			{"ruleRevisionId", firing.RuleRevisionID},
			{"ruleName", firing.RuleName},
			{"module", firing.Module},
		} {
			if err := validateIdentifier(itemPath+"."+field.name, field.value, invalid); err != nil {
				return FiringCollection{}, err
			}
		}
		if _, duplicate := seenActivations[firing.ActivationID]; duplicate {
			return FiringCollection{}, invalid("%s.activationId duplicates %q", itemPath, firing.ActivationID)
		}
		seenActivations[firing.ActivationID] = struct{}{}
		if firing.Source != nil {
			if err := validateSourceSpan(itemPath+".source", *firing.Source, invalid); err != nil {
				return FiringCollection{}, err
			}
		}
		if err := validateSignedSafeInteger(itemPath+".salience", firing.Salience, invalid); err != nil {
			return FiringCollection{}, err
		}
		factIDs, err := normalizeIDList(firing.FactIDs, itemPath+".factIds", invalid)
		if err != nil {
			return FiringCollection{}, err
		}
		out.Items[i] = firing
		out.Items[i].Source = cloneSourceSpan(firing.Source)
		out.Items[i].FactIDs = factIDs
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Sequence < out.Items[j].Sequence })
	return out, nil
}

func normalizeEvents(collection EventCollection, appliedLimit int64, terminal TerminalResult, invalid artifactInvalidf) (EventCollection, error) {
	if err := validateCollection("events", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return EventCollection{}, err
	}
	out := collection
	out.Items = make([]Event, len(collection.Items))
	seenSequences := make(map[DecimalUint64]struct{}, len(collection.Items))
	for i, event := range collection.Items {
		itemPath := fmt.Sprintf("events.items[%d]", i)
		if event.Sequence == 0 {
			return EventCollection{}, invalid("%s.sequence must be positive", itemPath)
		}
		if _, duplicate := seenSequences[event.Sequence]; duplicate {
			return EventCollection{}, invalid("%s.sequence duplicates %d", itemPath, event.Sequence)
		}
		seenSequences[event.Sequence] = struct{}{}
		if err := validateRunID(itemPath+".runId", event.RunID, true, invalid); err != nil {
			return EventCollection{}, err
		}
		if event.RunID != "run:zero" && event.RunID != terminal.RunID {
			return EventCollection{}, invalid("%s.runId must be run:zero or terminal.runId", itemPath)
		}
		switch event.Type {
		case EventFactAsserted, EventFactModified, EventFactRetracted, EventReset, EventRuleActivated, EventRuleDeactivated, EventRuleFired, EventActionFailed, EventLogicalSupportAdded, EventLogicalSupportRemoved:
		default:
			return EventCollection{}, invalid("%s.type has unknown value %q", itemPath, event.Type)
		}
		switch event.Severity {
		case SeverityInfo, SeverityWarning, SeverityError:
		default:
			return EventCollection{}, invalid("%s.severity has unknown value %q", itemPath, event.Severity)
		}
		attribution := []struct{ name, value string }{
			{"ruleId", event.RuleID},
			{"ruleRevisionId", event.RuleRevisionID},
			{"activationId", event.ActivationID},
		}
		attributionCount := 0
		for _, field := range attribution {
			if err := validateOptionalIdentifier(itemPath+"."+field.name, field.value, invalid); err != nil {
				return EventCollection{}, err
			}
			if field.value != "" {
				attributionCount++
			}
		}
		if attributionCount != 0 && attributionCount != len(attribution) {
			return EventCollection{}, invalid("%s ruleId, ruleRevisionId, and activationId must be present together", itemPath)
		}
		switch event.Type {
		case EventRuleActivated, EventRuleDeactivated, EventRuleFired, EventActionFailed, EventLogicalSupportAdded, EventLogicalSupportRemoved:
			if attributionCount != len(attribution) {
				return EventCollection{}, invalid("%s requires ruleId, ruleRevisionId, and activationId", itemPath)
			}
		}
		if event.Source != nil {
			if err := validateSourceSpan(itemPath+".source", *event.Source, invalid); err != nil {
				return EventCollection{}, err
			}
		}
		if event.ActionIndex != nil {
			if err := validateNonnegativeSafeInteger(itemPath+".actionIndex", *event.ActionIndex, invalid); err != nil {
				return EventCollection{}, err
			}
		}
		if event.Error != nil {
			if err := validateErrorPayload(itemPath+".error", *event.Error, invalid); err != nil {
				return EventCollection{}, err
			}
		}
		if event.Type == EventActionFailed {
			if event.RunID != terminal.RunID {
				return EventCollection{}, invalid("%s action failure must belong to terminal.runId", itemPath)
			}
			switch terminal.Status {
			case TerminalError, TerminalMaxFacts, TerminalDeadline, TerminalCanceled:
			default:
				return EventCollection{}, invalid("%s action failure requires an error or interrupted terminal status", itemPath)
			}
			if event.Severity != SeverityError {
				return EventCollection{}, invalid("%s action failure severity must be error", itemPath)
			}
			if err := validateIdentifier(itemPath+".actionName", event.ActionName, invalid); err != nil {
				return EventCollection{}, err
			}
			if event.ActionIndex == nil {
				return EventCollection{}, invalid("%s action failure requires actionIndex", itemPath)
			}
			if event.Error == nil {
				return EventCollection{}, invalid("%s action failure requires error", itemPath)
			}
		} else if event.ActionName != "" || event.ActionIndex != nil || event.Error != nil {
			return EventCollection{}, invalid("%s actionName, actionIndex, and error are only permitted for action failures", itemPath)
		}
		factIDs, err := normalizeIDList(event.FactIDs, itemPath+".factIds", invalid)
		if err != nil {
			return EventCollection{}, err
		}
		out.Items[i] = event
		out.Items[i].Source = cloneSourceSpan(event.Source)
		out.Items[i].ActionIndex = cloneInt64(event.ActionIndex)
		out.Items[i].FactIDs = factIDs
		out.Items[i].Error = cloneErrorPayload(event.Error)
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Sequence < out.Items[j].Sequence })
	return out, nil
}

func normalizeQueryResults(queries []QueryResult, appliedLimit int64, invalid artifactInvalidf) ([]QueryResult, error) {
	out := make([]QueryResult, len(queries))
	seenQueries := make(map[string]struct{}, len(queries))
	for i, query := range queries {
		itemPath := fmt.Sprintf("queries[%d]", i)
		if err := validateIdentifier(itemPath+".name", query.Name, invalid); err != nil {
			return nil, err
		}
		if _, duplicate := seenQueries[query.Name]; duplicate {
			return nil, invalid("%s.name duplicates %q", itemPath, query.Name)
		}
		seenQueries[query.Name] = struct{}{}
		if err := validatePositiveSafeInteger(itemPath+".maxRows", query.MaxRows, invalid); err != nil {
			return nil, err
		}
		if query.MaxRows > appliedLimit {
			return nil, invalid("%s.maxRows must be no greater than limits.report.maxQueryRows", itemPath)
		}
		args, err := normalizeValueMap(query.Args, itemPath+".args", invalid)
		if err != nil {
			return nil, err
		}
		if err := validateCollection(itemPath+".rows", query.Rows.Status, query.Rows.Limit, query.Rows.Total, query.Rows.TotalKnown, query.Rows.Returned, int64(len(query.Rows.Items)), query.MaxRows, query.Rows.Truncated, invalid); err != nil {
			return nil, err
		}
		rows := make([]QueryRow, len(query.Rows.Items))
		for rowIndex, row := range query.Rows.Items {
			rowPath := fmt.Sprintf("%s.rows.items[%d]", itemPath, rowIndex)
			rows[rowIndex].Cells = make([]QueryCell, len(row.Cells))
			seenAliases := make(map[string]struct{}, len(row.Cells))
			for cellIndex, cell := range row.Cells {
				cellPath := fmt.Sprintf("%s.cells[%d]", rowPath, cellIndex)
				if err := validateIdentifier(cellPath+".alias", cell.Alias, invalid); err != nil {
					return nil, err
				}
				if _, duplicate := seenAliases[cell.Alias]; duplicate {
					return nil, invalid("%s.alias duplicates %q", cellPath, cell.Alias)
				}
				seenAliases[cell.Alias] = struct{}{}
				if (cell.FactID == nil) == (cell.Value == nil) {
					return nil, invalid("%s requires exactly one of factId or value", cellPath)
				}
				rows[rowIndex].Cells[cellIndex].Alias = cell.Alias
				if cell.FactID != nil {
					if err := validateFactID(cellPath+".factId", *cell.FactID, invalid); err != nil {
						return nil, err
					}
					factID := *cell.FactID
					rows[rowIndex].Cells[cellIndex].FactID = &factID
				} else {
					if _, err := MarshalValue(cell.Value.RulesValue()); err != nil {
						return nil, invalid("%s.value is invalid: %v", cellPath, err)
					}
					value := NewValue(cell.Value.RulesValue())
					rows[rowIndex].Cells[cellIndex].Value = &value
				}
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return bytes.Compare(queryRowKey(rows[i]), queryRowKey(rows[j])) < 0 })
		out[i] = query
		out[i].Args = args
		out[i].Rows = query.Rows
		out[i].Rows.Items = rows
	}
	return out, nil
}

func queryRowKey(row QueryRow) []byte {
	var key bytes.Buffer
	for _, cell := range row.Cells {
		fmt.Fprintf(&key, "%d:%s", len(cell.Alias), cell.Alias)
		if cell.FactID != nil {
			fmt.Fprintf(&key, "f%d:%s", len(*cell.FactID), *cell.FactID)
		} else {
			key.WriteByte('v')
			encoded, _ := MarshalValue(cell.Value.RulesValue())
			fmt.Fprintf(&key, "%d:", len(encoded))
			key.Write(encoded)
		}
	}
	return key.Bytes()
}

func normalizeDiagnostics(collection DiagnosticCollection, appliedLimit int64, invalid artifactInvalidf) (DiagnosticCollection, error) {
	if err := validateCollection("diagnostics", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return DiagnosticCollection{}, err
	}
	out := collection
	out.Items = make([]Diagnostic, len(collection.Items))
	seenIDs := make(map[string]struct{}, len(collection.Items))
	for i, diagnostic := range collection.Items {
		itemPath := fmt.Sprintf("diagnostics.items[%d]", i)
		for _, field := range []struct{ name, value string }{
			{"id", diagnostic.ID},
			{"phase", diagnostic.Phase},
			{"code", diagnostic.Code},
			{"target", diagnostic.Target},
		} {
			if err := validateIdentifier(itemPath+"."+field.name, field.value, invalid); err != nil {
				return DiagnosticCollection{}, err
			}
		}
		if err := validateRequiredText(itemPath+".message", diagnostic.Message, invalid); err != nil {
			return DiagnosticCollection{}, err
		}
		if _, duplicate := seenIDs[diagnostic.ID]; duplicate {
			return DiagnosticCollection{}, invalid("%s.id duplicates %q", itemPath, diagnostic.ID)
		}
		seenIDs[diagnostic.ID] = struct{}{}
		switch diagnostic.Severity {
		case SeverityInfo, SeverityWarning, SeverityError:
		default:
			return DiagnosticCollection{}, invalid("%s.severity has unknown value %q", itemPath, diagnostic.Severity)
		}
		if diagnostic.Span != nil {
			if err := validateSourceSpan(itemPath+".span", *diagnostic.Span, invalid); err != nil {
				return DiagnosticCollection{}, err
			}
		}
		out.Items[i] = diagnostic
		out.Items[i].Span = cloneSourceSpan(diagnostic.Span)
	}
	sort.Slice(out.Items, func(i, j int) bool { return diagnosticKey(out.Items[i]) < diagnosticKey(out.Items[j]) })
	return out, nil
}

func normalizeCounters(collection CounterCollection, appliedLimit int64, invalid artifactInvalidf) (CounterCollection, error) {
	if err := validateCollection("counters", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return CounterCollection{}, err
	}
	out := collection
	out.Items = make([]Counter, len(collection.Items))
	seen := make(map[string]struct{}, len(collection.Items))
	for i, counter := range collection.Items {
		itemPath := fmt.Sprintf("counters.items[%d]", i)
		if err := validateIdentifier(itemPath+".name", counter.Name, invalid); err != nil {
			return CounterCollection{}, err
		}
		if err := validateIdentifier(itemPath+".unit", counter.Unit, invalid); err != nil {
			return CounterCollection{}, err
		}
		if _, duplicate := seen[counter.Name]; duplicate {
			return CounterCollection{}, invalid("%s duplicates counter %q with unit %q", itemPath, counter.Name, counter.Unit)
		}
		seen[counter.Name] = struct{}{}
		out.Items[i] = counter
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Name != out.Items[j].Name {
			return out.Items[i].Name < out.Items[j].Name
		}
		if out.Items[i].Unit != out.Items[j].Unit {
			return out.Items[i].Unit < out.Items[j].Unit
		}
		return out.Items[i].Value < out.Items[j].Value
	})
	return out, nil
}

func normalizeChecks(collection CheckCollection, appliedLimit int64, invalid artifactInvalidf) (CheckCollection, error) {
	if err := validateCollection("checks", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return CheckCollection{}, err
	}
	out := collection
	out.Items = make([]CheckResult, len(collection.Items))
	seen := make(map[string]struct{}, len(collection.Items))
	for i, check := range collection.Items {
		itemPath := fmt.Sprintf("checks.items[%d]", i)
		if err := validateIdentifier(itemPath+".path", check.Path, invalid); err != nil {
			return CheckCollection{}, err
		}
		if _, duplicate := seen[check.Path]; duplicate {
			return CheckCollection{}, invalid("%s.path duplicates %q", itemPath, check.Path)
		}
		seen[check.Path] = struct{}{}
		for _, field := range []struct{ name, value string }{
			{"expected", check.Expected}, {"actual", check.Actual}, {"message", check.Message},
		} {
			if err := validateOptionalText(itemPath+"."+field.name, field.value, invalid); err != nil {
				return CheckCollection{}, err
			}
		}
		out.Items[i] = check
	}
	sort.Slice(out.Items, func(i, j int) bool { return checkKey(out.Items[i]) < checkKey(out.Items[j]) })
	return out, nil
}

func normalizeArtifactReferences(collection ArtifactReferenceCollection, appliedLimit int64, invalid artifactInvalidf) (ArtifactReferenceCollection, error) {
	if err := validateCollection("explanationRefs", collection.Status, collection.Limit, collection.Total, collection.TotalKnown, collection.Returned, int64(len(collection.Items)), appliedLimit, collection.Truncated, invalid); err != nil {
		return ArtifactReferenceCollection{}, err
	}
	out := collection
	out.Items = make([]ArtifactReference, len(collection.Items))
	seen := make(map[string]struct{}, len(collection.Items))
	for i, reference := range collection.Items {
		itemPath := fmt.Sprintf("explanationRefs.items[%d]", i)
		if err := validateIdentifier(itemPath+".kind", reference.Kind, invalid); err != nil {
			return ArtifactReferenceCollection{}, err
		}
		if err := validateIdentifier(itemPath+".id", reference.ID, invalid); err != nil {
			return ArtifactReferenceCollection{}, err
		}
		if err := validateIdentifier(itemPath+".schemaVersion", reference.SchemaVersion, invalid); err != nil {
			return ArtifactReferenceCollection{}, err
		}
		if err := validateDigest(itemPath+".digest", reference.Digest, invalid); err != nil {
			return ArtifactReferenceCollection{}, err
		}
		identity := reference.Kind + "\x00" + reference.ID
		if _, duplicate := seen[identity]; duplicate {
			return ArtifactReferenceCollection{}, invalid("%s duplicates reference %q/%q", itemPath, reference.Kind, reference.ID)
		}
		seen[identity] = struct{}{}
		out.Items[i] = reference
	}
	sort.Slice(out.Items, func(i, j int) bool { return artifactReferenceKey(out.Items[i]) < artifactReferenceKey(out.Items[j]) })
	return out, nil
}

func normalizeIDList(values []string, name string, invalid artifactInvalidf) ([]string, error) {
	out := make([]string, len(values))
	for i, value := range values {
		if err := validateFactID(fmt.Sprintf("%s[%d]", name, i), value, invalid); err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

func cloneTerminalResult(terminal TerminalResult) TerminalResult {
	out := terminal
	out.Error = cloneErrorPayload(terminal.Error)
	return out
}

func cloneErrorPayload(payload *ErrorPayload) *ErrorPayload {
	if payload == nil {
		return nil
	}
	out := *payload
	out.Span = cloneSourceSpan(payload.Span)
	return &out
}

func cloneSourceSpan(span *SourceSpan) *SourceSpan {
	if span == nil {
		return nil
	}
	out := *span
	return &out
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func diagnosticKey(value Diagnostic) string {
	return value.ID + "\x00" + value.Phase + "\x00" + string(value.Severity) + "\x00" + value.Code + "\x00" + value.Target + "\x00" + value.Message + "\x00" + sourceSpanKey(value.Span)
}

func checkKey(value CheckResult) string {
	return value.Path + "\x00" + value.Expected + "\x00" + value.Actual + "\x00" + value.Message
}

func artifactReferenceKey(value ArtifactReference) string {
	return value.Kind + "\x00" + value.ID + "\x00" + value.SchemaVersion + "\x00" + value.Digest
}

func sourceSpanKey(value *SourceSpan) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%010d\x00%010d\x00%010d\x00%010d", value.Path, value.StartLine, value.StartColumn, value.EndLine, value.EndColumn)
}
