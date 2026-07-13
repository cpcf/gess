package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// MarshalScenario returns the canonical JSON encoding of document.
func MarshalScenario(document Scenario) ([]byte, error) {
	normalized, err := normalizeScenario(document)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, invalidScenarioJSONf("encode: %v", err)
	}
	return encoded, nil
}

// UnmarshalScenario decodes one strict scenario JSON document.
func UnmarshalScenario(data []byte) (Scenario, error) {
	node, err := readArtifactJSON(data, ErrInvalidScenario)
	if err != nil {
		return Scenario{}, err
	}
	if err := checkArtifactVersion(node, "scenario", ScenarioSchemaVersion, ErrInvalidScenario, ErrUnsupportedScenarioVersion); err != nil {
		return Scenario{}, err
	}
	if err := validateScenarioJSONShape(node); err != nil {
		return Scenario{}, invalidScenarioJSONf("%v", err)
	}

	var document Scenario
	if err := decodeArtifactJSON(data, &document); err != nil {
		return Scenario{}, invalidScenarioJSONf("decode: %v", err)
	}
	normalized, err := normalizeScenario(document)
	if err != nil {
		return Scenario{}, err
	}
	return normalized, nil
}

// ScenarioDigest returns the SHA-256 digest of the canonical scenario JSON.
func ScenarioDigest(document Scenario) (string, error) {
	encoded, err := MarshalScenario(document)
	if err != nil {
		return "", err
	}
	return artifactDigest(encoded), nil
}

// MarshalRunReport returns the canonical JSON encoding of document.
func MarshalRunReport(document RunReport) ([]byte, error) {
	normalized, err := normalizeRunReport(document)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, invalidRunReportJSONf("encode: %v", err)
	}
	if err := validateEncodedRunReportSize(normalized, encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

// UnmarshalRunReport decodes one strict run-report JSON document.
func UnmarshalRunReport(data []byte) (RunReport, error) {
	node, err := readArtifactJSON(data, ErrInvalidRunReport)
	if err != nil {
		return RunReport{}, err
	}
	if err := checkArtifactVersion(node, "run report", RunReportSchemaVersion, ErrInvalidRunReport, ErrUnsupportedRunReportVersion); err != nil {
		return RunReport{}, err
	}
	if err := validateRunReportJSONShape(node); err != nil {
		return RunReport{}, invalidRunReportJSONf("%v", err)
	}

	var document RunReport
	if err := decodeArtifactJSON(data, &document); err != nil {
		return RunReport{}, invalidRunReportJSONf("decode: %v", err)
	}
	normalized, err := normalizeRunReport(document)
	if err != nil {
		return RunReport{}, err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return RunReport{}, invalidRunReportJSONf("canonical encode: %v", err)
	}
	if err := validateEncodedRunReportSize(normalized, canonical); err != nil {
		return RunReport{}, err
	}
	return normalized, nil
}

// RunReportDigest returns the SHA-256 digest of the canonical run-report JSON.
func RunReportDigest(document RunReport) (string, error) {
	encoded, err := MarshalRunReport(document)
	if err != nil {
		return "", err
	}
	return artifactDigest(encoded), nil
}

func artifactDigest(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateEncodedRunReportSize(document RunReport, encoded []byte) error {
	if int64(len(encoded)) > document.Limits.Report.MaxReportBytes {
		return invalidRunReportJSONf("canonical report size %d exceeds limits.report.maxReportBytes %d", len(encoded), document.Limits.Report.MaxReportBytes)
	}
	return nil
}

func readArtifactJSON(data []byte, invalid error) (jsonNode, error) {
	if !utf8.Valid(data) {
		return jsonNode{}, fmt.Errorf("%w: input is not valid UTF-8", invalid)
	}
	if err := validateJSONStringEscapes(data); err != nil {
		return jsonNode{}, fmt.Errorf("%w: %v", invalid, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	node, err := readJSONNode(decoder)
	if err != nil {
		return jsonNode{}, fmt.Errorf("%w: decode: %v", invalid, err)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return jsonNode{}, fmt.Errorf("%w: trailing JSON: %v", invalid, err)
		}
		return jsonNode{}, fmt.Errorf("%w: trailing JSON token %v", invalid, token)
	}
	return node, nil
}

func decodeArtifactJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func checkArtifactVersion(node jsonNode, artifact, expected string, invalid, unsupported error) error {
	if node.kind != jsonNodeObject {
		return fmt.Errorf("%w: %s must be an object", invalid, artifact)
	}
	version, ok := objectMember(node, "schemaVersion")
	if !ok {
		return fmt.Errorf("%w: %s is missing member %q", invalid, artifact, "schemaVersion")
	}
	if version.kind != jsonNodeString {
		return fmt.Errorf("%w: %s member %q must be a string", invalid, artifact, "schemaVersion")
	}
	if version.text != expected {
		return fmt.Errorf("%w: %q", unsupported, version.text)
	}
	return nil
}

func invalidScenarioJSONf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidScenario, fmt.Sprintf(format, args...))
}

func invalidRunReportJSONf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRunReport, fmt.Sprintf(format, args...))
}

type artifactNodeValidator func(jsonNode, string) error

type artifactObjectField struct {
	name     string
	optional bool
	validate artifactNodeValidator
}

func requiredArtifactField(name string, validate artifactNodeValidator) artifactObjectField {
	return artifactObjectField{name: name, validate: validate}
}

func optionalArtifactField(name string, validate artifactNodeValidator) artifactObjectField {
	return artifactObjectField{name: name, optional: true, validate: validate}
}

func validateArtifactObject(node jsonNode, path string, fields ...artifactObjectField) error {
	if node.kind != jsonNodeObject {
		return fmt.Errorf("%s must be an object", path)
	}
	wanted := make(map[string]artifactObjectField, len(fields))
	seen := make(map[string]struct{}, len(node.object))
	for _, field := range fields {
		wanted[field.name] = field
	}
	for _, member := range node.object {
		field, ok := wanted[member.name]
		if !ok {
			return fmt.Errorf("%s has unknown member %q", path, member.name)
		}
		seen[member.name] = struct{}{}
		if err := field.validate(member.value, artifactMemberPath(path, member.name)); err != nil {
			return err
		}
	}
	for _, field := range fields {
		if _, ok := seen[field.name]; !ok && !field.optional {
			return fmt.Errorf("%s is missing member %q", path, field.name)
		}
	}
	return nil
}

func artifactMemberPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func artifactString(node jsonNode, path string) error {
	if node.kind != jsonNodeString {
		return fmt.Errorf("%s must be a string", path)
	}
	return nil
}

func artifactNumber(node jsonNode, path string) error {
	if node.kind != jsonNodeNumber {
		return fmt.Errorf("%s must be a number", path)
	}
	return nil
}

func artifactBool(node jsonNode, path string) error {
	if node.kind != jsonNodeBool {
		return fmt.Errorf("%s must be a bool", path)
	}
	return nil
}

func artifactDigestString(node jsonNode, path string) error {
	if err := artifactString(node, path); err != nil {
		return err
	}
	if len(node.text) != len("sha256:")+64 || !strings.HasPrefix(node.text, "sha256:") {
		return fmt.Errorf("%s must be a lowercase sha256 digest", path)
	}
	for _, digit := range node.text[len("sha256:"):] {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f')) {
			return fmt.Errorf("%s must be a lowercase sha256 digest", path)
		}
	}
	return nil
}

func artifactArray(validate artifactNodeValidator) artifactNodeValidator {
	return func(node jsonNode, path string) error {
		if node.kind != jsonNodeArray {
			return fmt.Errorf("%s must be an array", path)
		}
		for index, item := range node.array {
			if err := validate(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	}
}

func artifactMap(validate artifactNodeValidator) artifactNodeValidator {
	return func(node jsonNode, path string) error {
		if node.kind != jsonNodeObject {
			return fmt.Errorf("%s must be an object", path)
		}
		for _, member := range node.object {
			memberPath := fmt.Sprintf("%s[%q]", path, member.name)
			if err := validate(member.value, memberPath); err != nil {
				return err
			}
		}
		return nil
	}
}

func artifactValue(node jsonNode, path string) error {
	_, err := decodeValueEnvelope(node, path)
	return err
}

func validateScenarioJSONShape(node jsonNode) error {
	return validateArtifactObject(node, "scenario",
		requiredArtifactField("schemaVersion", artifactString),
		requiredArtifactField("name", artifactString),
		requiredArtifactField("sources", artifactArray(validateScenarioSourceJSON)),
		requiredArtifactField("initialFacts", artifactArray(validateInitialFactJSON)),
		requiredArtifactField("deffacts", artifactArray(artifactString)),
		requiredArtifactField("globals", artifactMap(artifactValue)),
		requiredArtifactField("callbackProfile", validateCallbackProfileJSON),
		requiredArtifactField("run", validateRunOptionsJSON),
		requiredArtifactField("reportLimits", validateReportLimitsJSON),
		requiredArtifactField("queries", artifactArray(validateScenarioQueryJSON)),
		optionalArtifactField("expectations", validateExpectationsJSON),
	)
}

func validateScenarioSourceJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("path", artifactString),
		optionalArtifactField("digest", artifactDigestString),
	)
}

func validateInitialFactJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("template", artifactString),
		requiredArtifactField("fields", artifactMap(artifactValue)),
	)
}

func validateCallbackProfileJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("name", artifactString),
		requiredArtifactField("version", artifactString),
		requiredArtifactField("digest", artifactDigestString),
	)
}

func validateRunOptionsJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("strategy", artifactString),
		requiredArtifactField("maxFacts", artifactNumber),
		requiredArtifactField("maxFirings", artifactNumber),
		requiredArtifactField("deadlineMs", artifactNumber),
	)
}

func validateReportLimitsJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("maxFacts", artifactNumber),
		requiredArtifactField("maxFirings", artifactNumber),
		requiredArtifactField("maxEvents", artifactNumber),
		requiredArtifactField("maxQueryRows", artifactNumber),
		requiredArtifactField("maxDiagnostics", artifactNumber),
		requiredArtifactField("maxCounters", artifactNumber),
		requiredArtifactField("maxChecks", artifactNumber),
		requiredArtifactField("maxExplanationRefs", artifactNumber),
		requiredArtifactField("maxOutputBytes", artifactNumber),
		requiredArtifactField("maxReportBytes", artifactNumber),
	)
}

func validateInputLimitsJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("maxRequestBytes", artifactNumber),
		requiredArtifactField("maxSourceFiles", artifactNumber),
		requiredArtifactField("maxSourceFileBytes", artifactNumber),
		requiredArtifactField("maxInitialFacts", artifactNumber),
	)
}

func validateScenarioQueryJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("name", artifactString),
		requiredArtifactField("args", artifactMap(artifactValue)),
		requiredArtifactField("maxRows", artifactNumber),
	)
}

func validateExpectationsJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("terminalStatus", artifactString),
		optionalArtifactField("factCount", artifactNumber),
		optionalArtifactField("firingCount", artifactNumber),
		requiredArtifactField("queryRowCounts", artifactMap(artifactNumber)),
	)
}

func validateRunReportJSONShape(node jsonNode) error {
	return validateArtifactObject(node, "runReport",
		requiredArtifactField("schemaVersion", artifactString),
		requiredArtifactField("producer", validateBuildInfoJSON),
		requiredArtifactField("engine", validateBuildInfoJSON),
		requiredArtifactField("sources", artifactArray(validateResolvedSourceJSON)),
		requiredArtifactField("scenarioDigest", artifactString),
		requiredArtifactField("rulesetId", artifactString),
		requiredArtifactField("callbackProfile", validateCallbackProfileJSON),
		requiredArtifactField("limits", validateAppliedLimitsJSON),
		requiredArtifactField("terminal", validateTerminalResultJSON),
		requiredArtifactField("output", validateOutputJSON),
		requiredArtifactField("facts", validateFactCollectionJSON),
		requiredArtifactField("firings", validateFiringCollectionJSON),
		requiredArtifactField("events", validateEventCollectionJSON),
		requiredArtifactField("queries", artifactArray(validateQueryResultJSON)),
		requiredArtifactField("diagnostics", validateDiagnosticCollectionJSON),
		requiredArtifactField("counters", validateCounterCollectionJSON),
		requiredArtifactField("checks", validateCheckCollectionJSON),
		requiredArtifactField("explanationRefs", validateArtifactReferenceCollectionJSON),
	)
}

func validateBuildInfoJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("name", artifactString),
		requiredArtifactField("version", artifactString),
	)
}

func validateResolvedSourceJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("path", artifactString),
		requiredArtifactField("digest", artifactDigestString),
	)
}

func validateAppliedLimitsJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("input", validateInputLimitsJSON),
		requiredArtifactField("run", validateRunOptionsJSON),
		requiredArtifactField("report", validateReportLimitsJSON),
	)
}

func validateSourceSpanJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("path", artifactString),
		requiredArtifactField("startLine", artifactNumber),
		requiredArtifactField("startColumn", artifactNumber),
		requiredArtifactField("endLine", artifactNumber),
		requiredArtifactField("endColumn", artifactNumber),
	)
}

func validateErrorPayloadJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("code", artifactString),
		requiredArtifactField("message", artifactString),
		optionalArtifactField("span", validateSourceSpanJSON),
	)
}

func validateTerminalResultJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("status", artifactString),
		requiredArtifactField("runId", artifactString),
		requiredArtifactField("fired", artifactNumber),
		optionalArtifactField("error", validateErrorPayloadJSON),
	)
}

func validateFactJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("id", artifactString),
		requiredArtifactField("name", artifactString),
		requiredArtifactField("template", artifactString),
		requiredArtifactField("version", artifactString),
		requiredArtifactField("recency", artifactString),
		requiredArtifactField("generation", artifactString),
		requiredArtifactField("sequence", artifactString),
		requiredArtifactField("support", artifactString),
		requiredArtifactField("fields", artifactMap(artifactValue)),
		requiredArtifactField("fieldPresence", artifactMap(artifactString)),
	)
}

func validateFiringJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("sequence", artifactString),
		requiredArtifactField("runId", artifactString),
		requiredArtifactField("activationId", artifactString),
		requiredArtifactField("ruleId", artifactString),
		requiredArtifactField("ruleRevisionId", artifactString),
		requiredArtifactField("ruleName", artifactString),
		requiredArtifactField("module", artifactString),
		requiredArtifactField("salience", artifactNumber),
		optionalArtifactField("source", validateSourceSpanJSON),
		requiredArtifactField("factIds", artifactArray(artifactString)),
	)
}

func validateEventJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("sequence", artifactString),
		requiredArtifactField("runId", artifactString),
		requiredArtifactField("type", artifactString),
		requiredArtifactField("severity", artifactString),
		requiredArtifactField("generation", artifactString),
		requiredArtifactField("recency", artifactString),
		requiredArtifactField("ruleId", artifactString),
		requiredArtifactField("ruleRevisionId", artifactString),
		requiredArtifactField("activationId", artifactString),
		optionalArtifactField("source", validateSourceSpanJSON),
		requiredArtifactField("actionName", artifactString),
		optionalArtifactField("actionIndex", artifactNumber),
		requiredArtifactField("factIds", artifactArray(artifactString)),
		optionalArtifactField("error", validateErrorPayloadJSON),
	)
}

func validateQueryCellJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("alias", artifactString),
		optionalArtifactField("factId", artifactString),
		optionalArtifactField("value", artifactValue),
	)
}

func validateQueryRowJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("cells", artifactArray(validateQueryCellJSON)),
	)
}

func validateDiagnosticJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("id", artifactString),
		requiredArtifactField("phase", artifactString),
		requiredArtifactField("severity", artifactString),
		requiredArtifactField("code", artifactString),
		requiredArtifactField("message", artifactString),
		requiredArtifactField("target", artifactString),
		optionalArtifactField("span", validateSourceSpanJSON),
		requiredArtifactField("retryable", artifactBool),
	)
}

func validateCounterJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("name", artifactString),
		requiredArtifactField("value", artifactString),
		requiredArtifactField("unit", artifactString),
	)
}

func validateCheckResultJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("path", artifactString),
		requiredArtifactField("passed", artifactBool),
		requiredArtifactField("expected", artifactString),
		requiredArtifactField("actual", artifactString),
		requiredArtifactField("message", artifactString),
	)
}

func validateArtifactReferenceJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("kind", artifactString),
		requiredArtifactField("id", artifactString),
		requiredArtifactField("schemaVersion", artifactString),
		requiredArtifactField("digest", artifactDigestString),
	)
}

func validateCollectionJSON(node jsonNode, path string, validateItem artifactNodeValidator) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("status", validateSectionStatusJSON),
		requiredArtifactField("limit", artifactNumber),
		requiredArtifactField("total", artifactNumber),
		requiredArtifactField("totalKnown", artifactBool),
		requiredArtifactField("returned", artifactNumber),
		requiredArtifactField("truncated", artifactBool),
		requiredArtifactField("items", artifactArray(validateItem)),
	)
}

func validateSectionStatusJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("availability", artifactString),
		requiredArtifactField("reason", artifactString),
	)
}

func validateFactCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateFactJSON)
}

func validateFiringCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateFiringJSON)
}

func validateEventCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateEventJSON)
}

func validateQueryRowCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateQueryRowJSON)
}

func validateDiagnosticCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateDiagnosticJSON)
}

func validateCounterCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateCounterJSON)
}

func validateCheckCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateCheckResultJSON)
}

func validateArtifactReferenceCollectionJSON(node jsonNode, path string) error {
	return validateCollectionJSON(node, path, validateArtifactReferenceJSON)
}

func validateQueryResultJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("name", artifactString),
		requiredArtifactField("args", artifactMap(artifactValue)),
		requiredArtifactField("maxRows", artifactNumber),
		requiredArtifactField("rows", validateQueryRowCollectionJSON),
	)
}

func validateOutputJSON(node jsonNode, path string) error {
	return validateArtifactObject(node, path,
		requiredArtifactField("status", validateSectionStatusJSON),
		requiredArtifactField("limitBytes", artifactNumber),
		requiredArtifactField("totalBytes", artifactNumber),
		requiredArtifactField("totalKnown", artifactBool),
		requiredArtifactField("returnedBytes", artifactNumber),
		requiredArtifactField("truncated", artifactBool),
		requiredArtifactField("text", artifactString),
	)
}
