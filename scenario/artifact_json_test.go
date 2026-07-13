package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/cpcf/gess/rules"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestScenarioJSONRoundTripsCanonically(t *testing.T) {
	t.Parallel()

	document := validScenarioArtifact()
	encoded, err := MarshalScenario(document)
	if err != nil {
		t.Fatalf("MarshalScenario() error = %v", err)
	}
	decoded, err := UnmarshalScenario(encoded)
	if err != nil {
		t.Fatalf("UnmarshalScenario() error = %v; JSON = %s", err, encoded)
	}
	reencoded, err := MarshalScenario(decoded)
	if err != nil {
		t.Fatalf("MarshalScenario(decoded) error = %v", err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatalf("canonical re-encoding = %s, want %s", reencoded, encoded)
	}
	if err := ValidateScenario(decoded); err != nil {
		t.Fatalf("ValidateScenario(decoded) error = %v", err)
	}

	digest, err := ScenarioDigest(document)
	if err != nil {
		t.Fatalf("ScenarioDigest() error = %v", err)
	}
	sum := sha256.Sum256(encoded)
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if digest != wantDigest {
		t.Fatalf("ScenarioDigest() = %q, want %q", digest, wantDigest)
	}
}

func TestScenarioJSONPreservesSemanticOrderAndSortsMaps(t *testing.T) {
	t.Parallel()

	document := validScenarioArtifact()
	encoded, err := MarshalScenario(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	assertOrderedSubstrings(t, text,
		`"path":"rules/orders.gess"`,
		`"path":"rules/routing.gess"`,
	)
	assertOrderedSubstrings(t, text,
		`"template":"order"`,
		`"template":"customer"`,
	)
	assertOrderedSubstrings(t, text,
		`"deffacts":["bootstrap-orders","bootstrap-customers"]`,
	)
	assertOrderedSubstrings(t, text,
		`"name":"routes-by-lane"`,
		`"name":"orders-by-tier"`,
	)
	assertOrderedSubstrings(t, text,
		`"globals":{"minimum":`,
		`"threshold":`,
	)
	assertOrderedSubstrings(t, text,
		`"fields":{"id":`,
		`"priority":`,
	)
	assertOrderedSubstrings(t, text,
		`"args":{"lane":`,
		`"tier":`,
	)

	if document.Sources[0].Path != "rules/orders.gess" || document.InitialFacts[0].Template != "order" || document.Queries[0].Name != "routes-by-lane" {
		t.Fatal("MarshalScenario mutated caller-owned semantic ordering")
	}
}

func TestScenarioJSONNormalizesNilCollections(t *testing.T) {
	t.Parallel()

	document := validScenarioArtifact()
	document.Sources = nil
	document.InitialFacts = nil
	document.Deffacts = nil
	document.Globals = nil
	document.Queries = nil
	document.Expectations = nil

	encoded, err := MarshalScenario(document)
	if err != nil {
		t.Fatalf("MarshalScenario() error = %v", err)
	}
	for _, want := range []string{
		`"sources":[]`,
		`"initialFacts":[]`,
		`"deffacts":[]`,
		`"globals":{}`,
		`"queries":[]`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("canonical JSON %s does not contain %s", encoded, want)
		}
	}
	if strings.Contains(string(encoded), `"expectations"`) {
		t.Fatalf("canonical JSON unexpectedly contains omitted expectations: %s", encoded)
	}
}

func TestMarshalScenarioDefensivelyCopiesWithoutMutatingCaller(t *testing.T) {
	t.Parallel()

	document := validScenarioArtifact()
	want := validScenarioArtifact()
	if _, err := MarshalScenario(document); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document, want) {
		t.Fatalf("MarshalScenario mutated caller:\n got: %#v\nwant: %#v", document, want)
	}
}

func TestUnmarshalScenarioRejectsUnsupportedAndMalformedDocuments(t *testing.T) {
	t.Parallel()

	canonical, err := MarshalScenario(validScenarioArtifact())
	if err != nil {
		t.Fatal(err)
	}
	valid := string(canonical)
	withoutVersion := strings.Replace(valid, `"schemaVersion":"`+ScenarioSchemaVersion+`",`, "", 1)
	unsupported := strings.Replace(valid, ScenarioSchemaVersion, "gess.workbench.scenario.v2", 1)
	unknownTopLevel := strings.Replace(valid, `{`, `{"layout":{"x":1},`, 1)
	duplicateVersion := strings.Replace(valid, `{`, `{"schemaVersion":"`+ScenarioSchemaVersion+`",`, 1)
	duplicateGlobal := strings.Replace(valid, `"globals":{`, `"globals":{"minimum":{"kind":"int","int":"1"},`, 1)
	missingNestedLimit := strings.Replace(valid, `"maxFacts":10,`, "", 1)
	badValue := strings.Replace(valid, `"int":"2"`, `"int":"02"`, 1)
	badDigest := strings.Replace(valid, digestA, "sha256:ABC", 1)
	emptyOptionalDigest := strings.Replace(valid, `"path":"rules/routing.gess"}`, `"path":"rules/routing.gess","digest":""}`, 1)
	badPath := strings.Replace(valid, "rules/orders.gess", "../orders.gess", 1)
	badStrategy := strings.Replace(valid, `"strategy":"depth"`, `"strategy":"random"`, 1)
	unpairedSurrogate := strings.Replace(valid, "order-lifecycle", `\ud800`, 1)

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "top-level array", data: []byte(`[]`), want: ErrInvalidScenario},
		{name: "missing schema version", data: []byte(withoutVersion), want: ErrInvalidScenario},
		{name: "unsupported schema version", data: []byte(unsupported), want: ErrUnsupportedScenarioVersion},
		{name: "unknown UI member", data: []byte(unknownTopLevel), want: ErrInvalidScenario},
		{name: "duplicate member", data: []byte(duplicateVersion), want: ErrInvalidScenario},
		{name: "duplicate structural map key", data: []byte(duplicateGlobal), want: ErrInvalidScenario},
		{name: "missing required nested member", data: []byte(missingNestedLimit), want: ErrInvalidScenario},
		{name: "trailing document", data: append(append([]byte(nil), canonical...), []byte(` {}`)...), want: ErrInvalidScenario},
		{name: "invalid UTF-8", data: []byte{0xff}, want: ErrInvalidScenario},
		{name: "unpaired surrogate", data: []byte(unpairedSurrogate), want: ErrInvalidScenario},
		{name: "noncanonical nested typed value", data: []byte(badValue), want: ErrInvalidScenario},
		{name: "malformed digest", data: []byte(badDigest), want: ErrInvalidScenario},
		{name: "present empty optional digest", data: []byte(emptyOptionalDigest), want: ErrInvalidScenario},
		{name: "nonportable path", data: []byte(badPath), want: ErrInvalidScenario},
		{name: "unknown enum", data: []byte(badStrategy), want: ErrInvalidScenario},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := UnmarshalScenario(test.data)
			if !errors.Is(err, test.want) {
				t.Fatalf("UnmarshalScenario() error = %v, want errors.Is(%v)", err, test.want)
			}
			if test.want == ErrUnsupportedScenarioVersion && errors.Is(err, ErrInvalidScenario) {
				t.Fatalf("unsupported version error also matched ErrInvalidScenario: %v", err)
			}
		})
	}
}

func TestValidateScenarioRejectsContractViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Scenario)
	}{
		{name: "wrong version", mutate: func(value *Scenario) { value.SchemaVersion = "scenario.v2" }},
		{name: "empty name", mutate: func(value *Scenario) { value.Name = " " }},
		{name: "absolute source path", mutate: func(value *Scenario) { value.Sources[0].Path = "/rules.gess" }},
		{name: "volume source path", mutate: func(value *Scenario) { value.Sources[0].Path = "C:/rules.gess" }},
		{name: "backslash source path", mutate: func(value *Scenario) { value.Sources[0].Path = `rules\orders.gess` }},
		{name: "source traversal", mutate: func(value *Scenario) { value.Sources[0].Path = "rules/../orders.gess" }},
		{name: "malformed source digest", mutate: func(value *Scenario) { value.Sources[0].Digest = "sha256:ABC" }},
		{name: "duplicate source", mutate: func(value *Scenario) { value.Sources[1].Path = value.Sources[0].Path }},
		{name: "empty template", mutate: func(value *Scenario) { value.InitialFacts[0].Template = "" }},
		{name: "invalid typed field", mutate: func(value *Scenario) { value.InitialFacts[0].Fields["bad"] = NewValue(rules.FloatValue(math.NaN())) }},
		{name: "duplicate deffacts", mutate: func(value *Scenario) { value.Deffacts[1] = value.Deffacts[0] }},
		{name: "malformed callback digest", mutate: func(value *Scenario) { value.CallbackProfile.Digest = digestA + "0" }},
		{name: "unknown strategy", mutate: func(value *Scenario) { value.Run.Strategy = Strategy("random") }},
		{name: "zero max facts", mutate: func(value *Scenario) { value.Run.MaxFacts = 0 }},
		{name: "unsafe max facts", mutate: func(value *Scenario) { value.Run.MaxFacts = 1 << 53 }},
		{name: "zero max firings", mutate: func(value *Scenario) { value.Run.MaxFirings = 0 }},
		{name: "zero deadline", mutate: func(value *Scenario) { value.Run.DeadlineMS = 0 }},
		{name: "zero facts limit", mutate: func(value *Scenario) { value.ReportLimits.MaxFacts = 0 }},
		{name: "zero firings limit", mutate: func(value *Scenario) { value.ReportLimits.MaxFirings = 0 }},
		{name: "zero events limit", mutate: func(value *Scenario) { value.ReportLimits.MaxEvents = 0 }},
		{name: "zero query rows limit", mutate: func(value *Scenario) { value.ReportLimits.MaxQueryRows = 0 }},
		{name: "zero diagnostics limit", mutate: func(value *Scenario) { value.ReportLimits.MaxDiagnostics = 0 }},
		{name: "zero counters limit", mutate: func(value *Scenario) { value.ReportLimits.MaxCounters = 0 }},
		{name: "zero checks limit", mutate: func(value *Scenario) { value.ReportLimits.MaxChecks = 0 }},
		{name: "zero explanation refs limit", mutate: func(value *Scenario) { value.ReportLimits.MaxExplanationRefs = 0 }},
		{name: "zero output limit", mutate: func(value *Scenario) { value.ReportLimits.MaxOutputBytes = 0 }},
		{name: "zero report bytes limit", mutate: func(value *Scenario) { value.ReportLimits.MaxReportBytes = 0 }},
		{name: "unsafe report bytes limit", mutate: func(value *Scenario) { value.ReportLimits.MaxReportBytes = 1 << 53 }},
		{name: "duplicate query", mutate: func(value *Scenario) { value.Queries[1].Name = value.Queries[0].Name }},
		{name: "zero query max rows", mutate: func(value *Scenario) { value.Queries[0].MaxRows = 0 }},
		{name: "query exceeds report limit", mutate: func(value *Scenario) { value.Queries[0].MaxRows = value.ReportLimits.MaxQueryRows + 1 }},
		{name: "unknown expected query", mutate: func(value *Scenario) { value.Expectations.QueryRowCounts["missing-query"] = 1 }},
		{name: "negative expected fact count", mutate: func(value *Scenario) { count := int64(-1); value.Expectations.FactCount = &count }},
		{name: "negative expected firing count", mutate: func(value *Scenario) { count := int64(-1); value.Expectations.FiringCount = &count }},
		{name: "negative expected query rows", mutate: func(value *Scenario) { value.Expectations.QueryRowCounts["routes-by-lane"] = -1 }},
		{name: "unsafe expected query rows", mutate: func(value *Scenario) { value.Expectations.QueryRowCounts["routes-by-lane"] = 1 << 53 }},
		{name: "unknown expected terminal", mutate: func(value *Scenario) { value.Expectations.TerminalStatus = TerminalStatus("unknown") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validScenarioArtifact()
			test.mutate(&document)
			if err := ValidateScenario(document); !errors.Is(err, ErrInvalidScenario) {
				t.Fatalf("ValidateScenario() error = %v, want ErrInvalidScenario", err)
			}
			if _, err := MarshalScenario(document); !errors.Is(err, ErrInvalidScenario) {
				t.Fatalf("MarshalScenario() error = %v, want ErrInvalidScenario", err)
			}
		})
	}
}

func TestRunReportJSONRoundTripsCanonically(t *testing.T) {
	t.Parallel()

	for _, limited := range []bool{false, true} {
		name := "full"
		if limited {
			name = "limited"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			document := validRunReportArtifact(limited)
			encoded, err := MarshalRunReport(document)
			if err != nil {
				t.Fatalf("MarshalRunReport() error = %v", err)
			}
			decoded, err := UnmarshalRunReport(encoded)
			if err != nil {
				t.Fatalf("UnmarshalRunReport() error = %v; JSON = %s", err, encoded)
			}
			reencoded, err := MarshalRunReport(decoded)
			if err != nil {
				t.Fatalf("MarshalRunReport(decoded) error = %v", err)
			}
			if !bytes.Equal(reencoded, encoded) {
				t.Fatalf("canonical re-encoding = %s, want %s", reencoded, encoded)
			}
			if err := ValidateRunReport(decoded); err != nil {
				t.Fatalf("ValidateRunReport(decoded) error = %v", err)
			}

			digest, err := RunReportDigest(document)
			if err != nil {
				t.Fatalf("RunReportDigest() error = %v", err)
			}
			sum := sha256.Sum256(encoded)
			wantDigest := "sha256:" + hex.EncodeToString(sum[:])
			if digest != wantDigest {
				t.Fatalf("RunReportDigest() = %q, want %q", digest, wantDigest)
			}
		})
	}
}

func TestRunReportJSONCanonicalOrderingAndLosslessCounters(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(false)
	want := validRunReportArtifact(false)
	encoded, err := MarshalRunReport(document)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document, want) {
		t.Fatalf("MarshalRunReport mutated caller:\n got: %#v\nwant: %#v", document, want)
	}
	text := string(encoded)

	assertOrderedSubstrings(t, text, `"path":"rules/orders.gess"`, `"path":"rules/routing.gess"`)
	assertOrderedSubstrings(t, text, `"id":"fact:g1:1"`, `"id":"fact:g1:2"`)
	assertOrderedSubstrings(t, text, `"activationId":"activation:1"`, `"activationId":"activation:2"`)
	assertOrderedSubstrings(t, text, `"type":"fact_asserted"`, `"type":"rule_fired"`)
	assertOrderedSubstrings(t, text, `"string":"express"`, `"string":"standard"`)
	assertOrderedSubstrings(t, text, `"id":"diag:1"`, `"id":"diag:2"`)
	assertOrderedSubstrings(t, text, `"name":"alpha_activations"`, `"name":"beta_rows"`)
	assertOrderedSubstrings(t, text, `"path":"expectations.factCount"`, `"path":"expectations.terminalStatus"`)
	assertOrderedSubstrings(t, text, `"kind":"explain"`, `"kind":"why-not"`)
	assertOrderedSubstrings(t, text, `"name":"routes-by-lane"`, `"name":"orders-by-tier"`)
	assertOrderedSubstrings(t, text, `"alias":"z-route"`, `"alias":"a-order"`)

	for _, want := range []string{
		`"version":"9007199254740993"`,
		`"value":"9007199254740993"`,
		`"factIds":["fact:g1:1","fact:g1:1"]`,
		`"optional":"omitted"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("canonical report does not contain %s: %s", want, text)
		}
	}
}

func TestLimitedRunReportExposesEveryAppliedLimit(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(true)
	assertLimited := func(name string, status SectionStatus, limit, total, returned int64, totalKnown, truncated bool, itemCount int) {
		t.Helper()
		if status.Availability != SectionAvailable || limit != 1 || total != 2 || !totalKnown || returned != 1 || !truncated || itemCount != 1 {
			t.Errorf("%s metadata = status %#v, limit %d, total %d/%t, returned %d, truncated %t, items %d", name, status, limit, total, totalKnown, returned, truncated, itemCount)
		}
	}
	assertLimited("facts", document.Facts.Status, document.Facts.Limit, document.Facts.Total, document.Facts.Returned, document.Facts.TotalKnown, document.Facts.Truncated, len(document.Facts.Items))
	assertLimited("firings", document.Firings.Status, document.Firings.Limit, document.Firings.Total, document.Firings.Returned, document.Firings.TotalKnown, document.Firings.Truncated, len(document.Firings.Items))
	assertLimited("events", document.Events.Status, document.Events.Limit, document.Events.Total, document.Events.Returned, document.Events.TotalKnown, document.Events.Truncated, len(document.Events.Items))
	assertLimited("query rows", document.Queries[0].Rows.Status, document.Queries[0].Rows.Limit, document.Queries[0].Rows.Total, document.Queries[0].Rows.Returned, document.Queries[0].Rows.TotalKnown, document.Queries[0].Rows.Truncated, len(document.Queries[0].Rows.Items))
	assertLimited("diagnostics", document.Diagnostics.Status, document.Diagnostics.Limit, document.Diagnostics.Total, document.Diagnostics.Returned, document.Diagnostics.TotalKnown, document.Diagnostics.Truncated, len(document.Diagnostics.Items))
	assertLimited("counters", document.Counters.Status, document.Counters.Limit, document.Counters.Total, document.Counters.Returned, document.Counters.TotalKnown, document.Counters.Truncated, len(document.Counters.Items))
	assertLimited("checks", document.Checks.Status, document.Checks.Limit, document.Checks.Total, document.Checks.Returned, document.Checks.TotalKnown, document.Checks.Truncated, len(document.Checks.Items))
	assertLimited("explanation references", document.ExplanationRefs.Status, document.ExplanationRefs.Limit, document.ExplanationRefs.Total, document.ExplanationRefs.Returned, document.ExplanationRefs.TotalKnown, document.ExplanationRefs.Truncated, len(document.ExplanationRefs.Items))
	if document.Output.LimitBytes != 6 || document.Output.TotalBytes != 11 || document.Output.ReturnedBytes != 6 || !document.Output.TotalKnown || !document.Output.Truncated || len(document.Output.Text) != 6 {
		t.Errorf("output limit metadata = %#v", document.Output)
	}
}

func TestRunReportOutputUsesUTF8ByteCounts(t *testing.T) {
	t.Parallel()

	document := emptyRunReportArtifact()
	document.Output.Text = "é"
	document.Output.TotalBytes = 2
	document.Output.ReturnedBytes = 2
	if err := ValidateRunReport(document); err != nil {
		t.Fatalf("ValidateRunReport(two-byte output) error = %v", err)
	}
	document.Output.ReturnedBytes = 1
	if err := ValidateRunReport(document); !errors.Is(err, ErrInvalidRunReport) {
		t.Fatalf("ValidateRunReport(rune count) error = %v, want ErrInvalidRunReport", err)
	}
}

func TestRunReportAcceptsEveryTerminalStatus(t *testing.T) {
	t.Parallel()

	statuses := []TerminalStatus{TerminalQuiescent, TerminalMaxFacts, TerminalMaxFirings, TerminalDeadline, TerminalError, TerminalCanceled, TerminalHalted}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			document := validRunReportArtifact(false)
			document.Terminal.Status = status
			if status == TerminalError {
				document.Terminal.Error = &ErrorPayload{Code: "run_failed", Message: "run failed"}
			}
			if err := ValidateRunReport(document); err != nil {
				t.Fatalf("ValidateRunReport() error = %v", err)
			}
		})
	}
}

func TestRunReportUnavailableSectionsRoundTrip(t *testing.T) {
	t.Parallel()

	document := unavailableRunReportArtifact()
	omitted := SectionStatus{Availability: SectionOmitted, Reason: "capture disabled"}
	unsupported := SectionStatus{Availability: SectionUnsupported, Reason: "engine does not expose this section"}

	encoded, err := MarshalRunReport(document)
	if err != nil {
		t.Fatalf("MarshalRunReport() error = %v", err)
	}
	decoded, err := UnmarshalRunReport(encoded)
	if err != nil {
		t.Fatalf("UnmarshalRunReport() error = %v", err)
	}
	if decoded.Output.Status != omitted || decoded.Facts.Status != unsupported || decoded.Queries[0].Rows.Status != unsupported {
		t.Fatalf("unavailable section statuses changed: %#v", decoded)
	}
	for _, want := range []string{
		`"availability":"omitted","reason":"capture disabled"`,
		`"availability":"unsupported","reason":"engine does not expose this section"`,
		`"total":0,"totalKnown":false,"returned":0,"truncated":false,"items":[]`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("unavailable report does not contain %s: %s", want, encoded)
		}
	}
}

func TestRunReportNilCollectionsNormalizeToEmptyJSON(t *testing.T) {
	t.Parallel()

	document := emptyRunReportArtifact()
	document.Sources = nil
	document.Queries = nil
	document.Facts.Items = nil
	document.Firings.Items = nil
	document.Events.Items = nil
	document.Diagnostics.Items = nil
	document.Counters.Items = nil
	document.Checks.Items = nil
	document.ExplanationRefs.Items = nil

	encoded, err := MarshalRunReport(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"sources":[]`) || !strings.Contains(text, `"queries":[]`) {
		t.Fatalf("nil top-level slices were not normalized: %s", text)
	}
	if count := strings.Count(text, `"items":[]`); count != 7 {
		t.Fatalf("canonical empty report has %d empty item arrays, want 7: %s", count, text)
	}
}

func TestRunReportSizeLimitIsExact(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(false)
	limit := document.Limits.Report.MaxReportBytes
	for range 10 {
		document.Limits.Report.MaxReportBytes = limit
		encoded, err := MarshalRunReport(document)
		if err != nil {
			t.Fatalf("MarshalRunReport() while finding fixed size error = %v", err)
		}
		next := int64(len(encoded))
		if next == limit {
			break
		}
		limit = next
	}
	document.Limits.Report.MaxReportBytes = limit
	encoded, err := MarshalRunReport(document)
	if err != nil {
		t.Fatalf("MarshalRunReport(exact limit) error = %v", err)
	}
	if int64(len(encoded)) != limit {
		t.Fatalf("fixed report size = %d, limit = %d", len(encoded), limit)
	}

	document.Limits.Report.MaxReportBytes = limit - 1
	if _, err := MarshalRunReport(document); !errors.Is(err, ErrInvalidRunReport) {
		t.Fatalf("MarshalRunReport(exceeded limit) error = %v, want ErrInvalidRunReport", err)
	}
	if err := ValidateRunReport(document); !errors.Is(err, ErrInvalidRunReport) {
		t.Fatalf("ValidateRunReport(exceeded limit) error = %v, want ErrInvalidRunReport", err)
	}
	target := `"maxReportBytes":` + strconv.FormatInt(limit, 10)
	replacement := `"maxReportBytes":` + strconv.FormatInt(limit-1, 10)
	exceededJSON := []byte(strings.Replace(string(encoded), target, replacement, 1))
	if bytes.Equal(exceededJSON, encoded) {
		t.Fatalf("exact-size report did not contain %s", target)
	}
	if _, err := UnmarshalRunReport(exceededJSON); !errors.Is(err, ErrInvalidRunReport) {
		t.Fatalf("UnmarshalRunReport(exceeded limit) error = %v, want ErrInvalidRunReport", err)
	}
}

func TestRunReportAllowsGlobalByteBudgetTruncation(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(false)
	document.Output.Text = "routed"
	document.Output.ReturnedBytes = int64(len(document.Output.Text))
	document.Output.Truncated = true
	document.Facts.Items = document.Facts.Items[:1]
	document.Facts.Returned = 1
	document.Facts.Truncated = true

	if err := ValidateRunReport(document); err != nil {
		t.Fatalf("ValidateRunReport(global byte-budget truncation) error = %v", err)
	}
}

func TestRunReportEventErrorPayloadRoundTrips(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(false)
	actionIndex := int64(0)
	event := &document.Events.Items[0]
	event.Type = EventActionFailed
	event.Severity = SeverityError
	event.ActionName = "record-audit"
	event.ActionIndex = &actionIndex
	event.Error = &ErrorPayload{Code: "callback_failed", Message: "audit sink unavailable", Span: event.Source}
	document.Terminal.Status = TerminalError
	document.Terminal.Error = &ErrorPayload{Code: "callback_failed", Message: "audit sink unavailable", Span: event.Source}
	encoded, err := MarshalRunReport(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRunReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range decoded.Events.Items {
		if candidate.Error != nil && candidate.Error.Code == "callback_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("event error payload not preserved: %#v", decoded.Events.Items)
	}
}

func TestRunReportActionFailureEventContract(t *testing.T) {
	t.Parallel()

	if err := ValidateRunReport(validActionFailureRunReport()); err != nil {
		t.Fatalf("ValidateRunReport(valid action failure) error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*RunReport)
	}{
		{name: "zero run", mutate: func(value *RunReport) { value.Events.Items[0].RunID = "run:zero" }},
		{name: "non-error terminal", mutate: func(value *RunReport) {
			value.Terminal.Status = TerminalQuiescent
			value.Terminal.Error = nil
		}},
		{name: "non-error severity", mutate: func(value *RunReport) { value.Events.Items[0].Severity = SeverityInfo }},
		{name: "missing rule", mutate: func(value *RunReport) { value.Events.Items[0].RuleID = "" }},
		{name: "missing revision", mutate: func(value *RunReport) { value.Events.Items[0].RuleRevisionID = "" }},
		{name: "missing activation", mutate: func(value *RunReport) { value.Events.Items[0].ActivationID = "" }},
		{name: "missing action name", mutate: func(value *RunReport) { value.Events.Items[0].ActionName = "" }},
		{name: "missing action index", mutate: func(value *RunReport) { value.Events.Items[0].ActionIndex = nil }},
		{name: "missing error", mutate: func(value *RunReport) { value.Events.Items[0].Error = nil }},
		{name: "action payload on another event", mutate: func(value *RunReport) { value.Events.Items[0].Type = EventRuleFired }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validActionFailureRunReport()
			test.mutate(&document)
			if err := ValidateRunReport(document); !errors.Is(err, ErrInvalidRunReport) {
				t.Fatalf("ValidateRunReport() error = %v, want ErrInvalidRunReport", err)
			}
		})
	}
}

func TestRunReportAllowsOmittedTemplateFieldsAndRepeatedBindings(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(false)
	if err := ValidateRunReport(document); err != nil {
		t.Fatalf("ValidateRunReport() error = %v", err)
	}
	if _, present := document.Facts.Items[1].Fields["optional"]; present {
		t.Fatal("test precondition: omitted template field unexpectedly has a value")
	}
	if document.Facts.Items[1].FieldPresence["optional"] != FieldPresenceOmitted {
		t.Fatal("test precondition: omitted template field marker is missing")
	}
	if got := document.Firings.Items[0].FactIDs; len(got) != 2 || got[0] != got[1] {
		t.Fatalf("test precondition: repeated binding fact IDs = %v", got)
	}
	foundRunZero := false
	for _, event := range document.Events.Items {
		if event.RunID == "run:zero" {
			foundRunZero = true
		}
	}
	if !foundRunZero {
		t.Fatal("test precondition: report has no run:zero lifecycle event")
	}
}

func TestRunReportAcceptsMaximumCanonicalRunID(t *testing.T) {
	t.Parallel()

	document := validRunReportArtifact(false)
	const runID = "run:18446744073709551615"
	document.Terminal.RunID = runID
	for index := range document.Firings.Items {
		document.Firings.Items[index].RunID = runID
	}
	for index := range document.Events.Items {
		if document.Events.Items[index].RunID != "run:zero" {
			document.Events.Items[index].RunID = runID
		}
	}
	if err := ValidateRunReport(document); err != nil {
		t.Fatalf("ValidateRunReport(maximum run ID) error = %v", err)
	}
}

func TestValidateRunReportRejectsContractViolations(t *testing.T) {
	t.Parallel()

	tooLarge := int64(1 << 53)
	tests := []struct {
		name   string
		mutate func(*RunReport)
	}{
		{name: "wrong version", mutate: func(value *RunReport) { value.SchemaVersion = "report.v2" }},
		{name: "empty producer", mutate: func(value *RunReport) { value.Producer.Name = "" }},
		{name: "zero request limit", mutate: func(value *RunReport) { value.Limits.Input.MaxRequestBytes = 0 }},
		{name: "unsafe input limit", mutate: func(value *RunReport) { value.Limits.Input.MaxInitialFacts = tooLarge }},
		{name: "too many sources", mutate: func(value *RunReport) { value.Limits.Input.MaxSourceFiles = 1 }},
		{name: "nonportable source", mutate: func(value *RunReport) { value.Sources[0].Path = "/orders.gess" }},
		{name: "malformed source digest", mutate: func(value *RunReport) { value.Sources[0].Digest = "sha256:ABC" }},
		{name: "duplicate source", mutate: func(value *RunReport) { value.Sources[1].Path = value.Sources[0].Path }},
		{name: "malformed scenario digest", mutate: func(value *RunReport) { value.ScenarioDigest = digestA + "0" }},
		{name: "empty ruleset ID", mutate: func(value *RunReport) { value.RulesetID = "" }},
		{name: "malformed callback digest", mutate: func(value *RunReport) { value.CallbackProfile.Digest = "" }},
		{name: "zero run fact limit", mutate: func(value *RunReport) { value.Limits.Run.MaxFacts = 0 }},
		{name: "unsafe run firing limit", mutate: func(value *RunReport) { value.Limits.Run.MaxFirings = tooLarge }},
		{name: "zero report collection limit", mutate: func(value *RunReport) { value.Limits.Report.MaxEvents = 0 }},
		{name: "unsafe report byte limit", mutate: func(value *RunReport) { value.Limits.Report.MaxReportBytes = tooLarge }},
		{name: "unknown terminal", mutate: func(value *RunReport) { value.Terminal.Status = TerminalStatus("unknown") }},
		{name: "zero terminal run ID", mutate: func(value *RunReport) { value.Terminal.RunID = "run:zero" }},
		{name: "random terminal run ID", mutate: func(value *RunReport) { value.Terminal.RunID = "session-123" }},
		{name: "leading-zero terminal run ID", mutate: func(value *RunReport) { value.Terminal.RunID = "run:01" }},
		{name: "out-of-range terminal run ID", mutate: func(value *RunReport) { value.Terminal.RunID = "run:18446744073709551616" }},
		{name: "error terminal without payload", mutate: func(value *RunReport) { value.Terminal.Status = TerminalError }},
		{name: "non-error terminal with payload", mutate: func(value *RunReport) { value.Terminal.Error = &ErrorPayload{Code: "failed", Message: "failed"} }},
		{name: "negative fired count", mutate: func(value *RunReport) { value.Terminal.Fired = -1 }},
		{name: "fired count mismatches firings", mutate: func(value *RunReport) { value.Terminal.Fired = 1 }},
		{name: "fired count exceeds run limit", mutate: func(value *RunReport) { value.Limits.Run.MaxFirings = 1 }},
		{name: "fact total exceeds run limit", mutate: func(value *RunReport) { value.Limits.Run.MaxFacts = 1 }},
		{name: "output available reason", mutate: func(value *RunReport) { value.Output.Status.Reason = "unexpected" }},
		{name: "output unavailable without reason", mutate: func(value *RunReport) { value.Output.Status.Availability = SectionOmitted }},
		{name: "output unavailable with known total", mutate: func(value *RunReport) {
			value.Output.Status = SectionStatus{Availability: SectionOmitted, Reason: "disabled"}
		}},
		{name: "output limit mismatch", mutate: func(value *RunReport) { value.Output.LimitBytes-- }},
		{name: "output available unknown total", mutate: func(value *RunReport) { value.Output.TotalKnown = false }},
		{name: "output returned bytes mismatch", mutate: func(value *RunReport) { value.Output.ReturnedBytes-- }},
		{name: "output truncation mismatch", mutate: func(value *RunReport) { value.Output.Truncated = true }},
		{name: "facts limit mismatch", mutate: func(value *RunReport) { value.Facts.Limit-- }},
		{name: "facts returned mismatch", mutate: func(value *RunReport) { value.Facts.Returned-- }},
		{name: "firings total mismatch", mutate: func(value *RunReport) { value.Firings.Total++ }},
		{name: "events truncation mismatch", mutate: func(value *RunReport) { value.Events.Truncated = true }},
		{name: "query rows limit mismatch", mutate: func(value *RunReport) { value.Queries[0].Rows.Limit-- }},
		{name: "diagnostics unknown total", mutate: func(value *RunReport) { value.Diagnostics.TotalKnown = false }},
		{name: "counters returned mismatch", mutate: func(value *RunReport) { value.Counters.Returned-- }},
		{name: "checks item count mismatch", mutate: func(value *RunReport) { value.Checks.Items = nil }},
		{name: "references truncation mismatch", mutate: func(value *RunReport) { value.ExplanationRefs.Truncated = true }},
		{name: "unavailable facts retain data", mutate: func(value *RunReport) {
			value.Facts.Status = SectionStatus{Availability: SectionUnsupported, Reason: "not exposed"}
			value.Facts.TotalKnown = false
		}},
		{name: "duplicate fact ID", mutate: func(value *RunReport) { value.Facts.Items[1].ID = value.Facts.Items[0].ID }},
		{name: "fact ID disagrees with sequence", mutate: func(value *RunReport) { value.Facts.Items[0].Sequence = NewDecimalUint64(3) }},
		{name: "zero fact counter", mutate: func(value *RunReport) { value.Facts.Items[0].Version = 0 }},
		{name: "fact has name and template", mutate: func(value *RunReport) { value.Facts.Items[0].Template = "order" }},
		{name: "fact has neither name nor template", mutate: func(value *RunReport) { value.Facts.Items[0].Name = "" }},
		{name: "unknown fact support", mutate: func(value *RunReport) { value.Facts.Items[0].Support = FactSupport("unknown") }},
		{name: "omitted template field has value", mutate: func(value *RunReport) { value.Facts.Items[1].Fields["optional"] = NewValue(rules.NullValue()) }},
		{name: "explicit template field lacks value", mutate: func(value *RunReport) { value.Facts.Items[1].FieldPresence["missing"] = FieldPresenceExplicit }},
		{name: "template field lacks presence", mutate: func(value *RunReport) { delete(value.Facts.Items[1].FieldPresence, "id") }},
		{name: "dynamic fact has presence", mutate: func(value *RunReport) { value.Facts.Items[0].FieldPresence["order-id"] = FieldPresenceExplicit }},
		{name: "duplicate firing sequence", mutate: func(value *RunReport) { value.Firings.Items[1].Sequence = value.Firings.Items[0].Sequence }},
		{name: "duplicate firing activation", mutate: func(value *RunReport) { value.Firings.Items[1].ActivationID = value.Firings.Items[0].ActivationID }},
		{name: "firing run mismatch", mutate: func(value *RunReport) { value.Firings.Items[0].RunID = "run:other" }},
		{name: "malformed firing fact reference", mutate: func(value *RunReport) { value.Firings.Items[0].FactIDs[0] = "fact:bad" }},
		{name: "unsafe firing salience", mutate: func(value *RunReport) { value.Firings.Items[0].Salience = tooLarge }},
		{name: "duplicate event sequence", mutate: func(value *RunReport) { value.Events.Items[1].Sequence = value.Events.Items[0].Sequence }},
		{name: "event belongs to another run", mutate: func(value *RunReport) { value.Events.Items[0].RunID = "run:2" }},
		{name: "malformed event fact reference", mutate: func(value *RunReport) { value.Events.Items[0].FactIDs[0] = "fact:g01:1" }},
		{name: "partial event attribution", mutate: func(value *RunReport) { value.Events.Items[1].RuleID = "rule:partial" }},
		{name: "unknown event type", mutate: func(value *RunReport) { value.Events.Items[0].Type = EventType("unknown") }},
		{name: "unknown event severity", mutate: func(value *RunReport) { value.Events.Items[0].Severity = Severity("unknown") }},
		{name: "negative action index", mutate: func(value *RunReport) { index := int64(-1); value.Events.Items[0].ActionIndex = &index }},
		{name: "duplicate query", mutate: func(value *RunReport) { value.Queries[1].Name = value.Queries[0].Name }},
		{name: "zero query max rows", mutate: func(value *RunReport) { value.Queries[0].MaxRows = 0 }},
		{name: "query max rows exceeds report", mutate: func(value *RunReport) { value.Queries[0].MaxRows = value.Limits.Report.MaxQueryRows + 1 }},
		{name: "query cell has neither representation", mutate: func(value *RunReport) { value.Queries[0].Rows.Items[0].Cells[0].Value = nil }},
		{name: "query cell has both representations", mutate: func(value *RunReport) { id := "fact:g1:1"; value.Queries[0].Rows.Items[0].Cells[0].FactID = &id }},
		{name: "malformed query fact reference", mutate: func(value *RunReport) {
			id := "fact:g1:0"
			value.Queries[0].Rows.Items[0].Cells[1].FactID = &id
		}},
		{name: "duplicate query alias", mutate: func(value *RunReport) {
			value.Queries[0].Rows.Items[0].Cells[1].Alias = value.Queries[0].Rows.Items[0].Cells[0].Alias
		}},
		{name: "invalid query value", mutate: func(value *RunReport) {
			invalid := NewValue(rules.FloatValue(math.NaN()))
			value.Queries[0].Rows.Items[0].Cells[0].Value = &invalid
		}},
		{name: "duplicate diagnostic", mutate: func(value *RunReport) { value.Diagnostics.Items[1].ID = value.Diagnostics.Items[0].ID }},
		{name: "invalid diagnostic span", mutate: func(value *RunReport) {
			value.Diagnostics.Items[1].Span = &SourceSpan{Path: "rules.gess", StartLine: 2, StartColumn: 1, EndLine: 1, EndColumn: 1}
		}},
		{name: "duplicate counter", mutate: func(value *RunReport) { value.Counters.Items[1].Name = value.Counters.Items[0].Name }},
		{name: "duplicate check", mutate: func(value *RunReport) { value.Checks.Items[1].Path = value.Checks.Items[0].Path }},
		{name: "duplicate reference", mutate: func(value *RunReport) {
			value.ExplanationRefs.Items[1].Kind = value.ExplanationRefs.Items[0].Kind
			value.ExplanationRefs.Items[1].ID = value.ExplanationRefs.Items[0].ID
		}},
		{name: "malformed reference digest", mutate: func(value *RunReport) { value.ExplanationRefs.Items[0].Digest = "" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validRunReportArtifact(false)
			test.mutate(&document)
			if err := ValidateRunReport(document); !errors.Is(err, ErrInvalidRunReport) {
				t.Fatalf("ValidateRunReport() error = %v, want ErrInvalidRunReport", err)
			}
			if _, err := MarshalRunReport(document); !errors.Is(err, ErrInvalidRunReport) {
				t.Fatalf("MarshalRunReport() error = %v, want ErrInvalidRunReport", err)
			}
		})
	}
}

func TestUnmarshalRunReportRejectsFramingAndVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "top-level array", data: []byte(`[]`), want: ErrInvalidRunReport},
		{name: "missing schema version", data: []byte(`{}`), want: ErrInvalidRunReport},
		{name: "unsupported schema version", data: []byte(`{"schemaVersion":"gess.workbench.report.v2"}`), want: ErrUnsupportedRunReportVersion},
		{name: "duplicate schema version", data: []byte(`{"schemaVersion":"gess.workbench.report.v1","schemaVersion":"gess.workbench.report.v1"}`), want: ErrInvalidRunReport},
		{name: "trailing document", data: []byte(`{"schemaVersion":"gess.workbench.report.v1"}{}`), want: ErrInvalidRunReport},
		{name: "invalid UTF-8", data: []byte{0xff}, want: ErrInvalidRunReport},
		{name: "unpaired surrogate", data: []byte(`{"schemaVersion":"\ud800"}`), want: ErrInvalidRunReport},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := UnmarshalRunReport(test.data)
			if !errors.Is(err, test.want) {
				t.Fatalf("UnmarshalRunReport() error = %v, want errors.Is(%v)", err, test.want)
			}
			if test.want == ErrUnsupportedRunReportVersion && errors.Is(err, ErrInvalidRunReport) {
				t.Fatalf("unsupported version error also matched ErrInvalidRunReport: %v", err)
			}
		})
	}
}

func TestUnmarshalRunReportRejectsMalformedDocuments(t *testing.T) {
	t.Parallel()

	canonical, err := MarshalRunReport(validRunReportArtifact(false))
	if err != nil {
		t.Fatal(err)
	}
	valid := string(canonical)
	replace := func(old, replacement string) []byte {
		t.Helper()
		if !strings.Contains(valid, old) {
			t.Fatalf("canonical report does not contain mutation target %q", old)
		}
		return []byte(strings.Replace(valid, old, replacement, 1))
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown UI member", data: replace(`{`, `{"selectedTab":"facts",`)},
		{name: "missing required build member", data: replace(`"producer":{"name":"gess-workbench","version":"0.1.0"},`, "")},
		{name: "duplicate nested map key", data: replace(`"args":{"lane":`, `"args":{"lane":{"kind":"string","string":"duplicate"},"lane":`)},
		{name: "malformed nested typed value", data: replace(`{"kind":"string","string":"new"}`, `{"kind":"int","int":"01"}`)},
		{name: "empty resolved source digest", data: replace(`"path":"rules/orders.gess","digest":"`+digestA+`"`, `"path":"rules/orders.gess","digest":""`)},
		{name: "nonportable resolved source path", data: replace(`"path":"rules/orders.gess"`, `"path":"../orders.gess"`)},
		{name: "unknown terminal enum", data: replace(`"status":"quiescent"`, `"status":"mystery"`)},
		{name: "unknown section availability", data: replace(`"availability":"available"`, `"availability":"mystery"`)},
		{name: "structural counter encoded as number", data: replace(`"version":"9007199254740993"`, `"version":9007199254740993`)},
		{name: "structural counter has leading zero", data: replace(`"sequence":"1"`, `"sequence":"01"`)},
		{name: "structural counter out of range", data: replace(`"value":"9007199254740993"`, `"value":"18446744073709551616"`)},
		{name: "bounded integer exceeds safe range", data: replace(`"maxRequestBytes":1048576`, `"maxRequestBytes":9007199254740992`)},
		{name: "fractional bounded count", data: replace(`"total":2`, `"total":2.5`)},
		{name: "missing total-known marker", data: replace(`"total":2,"totalKnown":true,`, `"total":2,`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := UnmarshalRunReport(test.data)
			if !errors.Is(err, ErrInvalidRunReport) {
				t.Fatalf("UnmarshalRunReport() error = %v, want ErrInvalidRunReport\nJSON: %s", err, test.data)
			}
		})
	}
}

func TestDecimalUint64JSONIsCanonicalAndLossless(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value uint64
		want  string
	}{
		{name: "zero", value: 0, want: `"0"`},
		{name: "JavaScript unsafe integer", value: 9007199254740993, want: `"9007199254740993"`},
		{name: "maximum", value: ^uint64(0), want: `"18446744073709551615"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(NewDecimalUint64(test.value))
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if string(encoded) != test.want {
				t.Fatalf("json.Marshal() = %s, want %s", encoded, test.want)
			}
			var decoded DecimalUint64
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if decoded.Uint64() != test.value || decoded.String() != test.want[1:len(test.want)-1] {
				t.Fatalf("decoded = %d / %q, want %d", decoded.Uint64(), decoded.String(), test.value)
			}
		})
	}

	invalid := []string{
		`0`, `null`, `""`, `"00"`, `"01"`, `"-1"`, `"+1"`, `"1.0"`,
		`" 1"`, `"1 "`, `"18446744073709551616"`,
	}
	for _, input := range invalid {
		t.Run("reject "+input, func(t *testing.T) {
			t.Parallel()
			decoded := NewDecimalUint64(7)
			if err := json.Unmarshal([]byte(input), &decoded); err == nil {
				t.Fatalf("json.Unmarshal(%s) unexpectedly succeeded", input)
			}
			if decoded.Uint64() != 7 {
				t.Fatalf("failed decode changed receiver to %d", decoded.Uint64())
			}
		})
	}
	var nilValue *DecimalUint64
	if err := nilValue.UnmarshalJSON([]byte(`"1"`)); err == nil {
		t.Fatal("nil DecimalUint64 receiver unexpectedly accepted JSON")
	}
}

func validScenarioArtifact() Scenario {
	factCount := int64(2)
	firingCount := int64(2)
	return Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		Name:          "order-lifecycle",
		Sources: []ScenarioSource{
			{Path: "rules/orders.gess", Digest: digestA},
			{Path: "rules/routing.gess"},
		},
		InitialFacts: []InitialFact{
			{
				Template: "order",
				Fields: map[string]Value{
					"priority": NewValue(rules.IntValue(2)),
					"id":       NewValue(rules.StringValue("O-1")),
				},
			},
			{
				Template: "customer",
				Fields: map[string]Value{
					"tier": NewValue(rules.StringValue("vip")),
					"id":   NewValue(rules.StringValue("C-1")),
				},
			},
		},
		Deffacts: []string{"bootstrap-orders", "bootstrap-customers"},
		Globals: map[string]Value{
			"threshold": NewValue(rules.FloatValue(0.75)),
			"minimum":   NewValue(rules.IntValue(1)),
		},
		CallbackProfile: CallbackProfile{Name: "pure", Version: "1", Digest: digestB},
		Run:             RunOptions{Strategy: StrategyDepth, MaxFacts: 1000, MaxFirings: 100, DeadlineMS: 5000},
		ReportLimits: ReportLimits{
			MaxFacts:           10,
			MaxFirings:         10,
			MaxEvents:          20,
			MaxQueryRows:       10,
			MaxDiagnostics:     10,
			MaxCounters:        10,
			MaxChecks:          10,
			MaxExplanationRefs: 10,
			MaxOutputBytes:     1024,
			MaxReportBytes:     65536,
		},
		Queries: []ScenarioQuery{
			{
				Name: "routes-by-lane",
				Args: map[string]Value{
					"tier": NewValue(rules.StringValue("vip")),
					"lane": NewValue(rules.StringValue("expedite")),
				},
				MaxRows: 5,
			},
			{Name: "orders-by-tier", Args: map[string]Value{"tier": NewValue(rules.StringValue("vip"))}, MaxRows: 5},
		},
		Expectations: &Expectations{
			TerminalStatus: TerminalQuiescent,
			FactCount:      &factCount,
			FiringCount:    &firingCount,
			QueryRowCounts: map[string]int64{"routes-by-lane": 1, "orders-by-tier": 1},
		},
	}
}

func validRunReportArtifact(limited bool) RunReport {
	scenarioDigest, err := ScenarioDigest(validScenarioArtifact())
	if err != nil {
		panic(err)
	}
	limits := ReportLimits{
		MaxFacts:           10,
		MaxFirings:         10,
		MaxEvents:          10,
		MaxQueryRows:       10,
		MaxDiagnostics:     10,
		MaxCounters:        10,
		MaxChecks:          10,
		MaxExplanationRefs: 10,
		MaxOutputBytes:     64,
		MaxReportBytes:     65536,
	}
	if limited {
		limits.MaxFacts = 1
		limits.MaxFirings = 1
		limits.MaxEvents = 1
		limits.MaxQueryRows = 1
		limits.MaxDiagnostics = 1
		limits.MaxCounters = 1
		limits.MaxChecks = 1
		limits.MaxExplanationRefs = 1
		limits.MaxOutputBytes = 6
	}

	span := &SourceSpan{Path: "rules/orders.gess", StartLine: 3, StartColumn: 1, EndLine: 5, EndColumn: 2}
	factOneID := "fact:g1:1"
	factTwoID := "fact:g1:2"
	express := NewValue(rules.StringValue("express"))
	standard := NewValue(rules.StringValue("standard"))

	factOne := Fact{
		ID:         factOneID,
		Template:   "order",
		Version:    NewDecimalUint64(9007199254740993),
		Recency:    NewDecimalUint64(1),
		Generation: NewDecimalUint64(1),
		Sequence:   NewDecimalUint64(1),
		Support:    FactSupportStated,
		Fields: map[string]Value{
			"status": NewValue(rules.StringValue("new")),
			"id":     NewValue(rules.StringValue("O-1")),
		},
		FieldPresence: map[string]FieldPresence{
			"status":   FieldPresenceDefault,
			"optional": FieldPresenceOmitted,
			"id":       FieldPresenceExplicit,
		},
	}
	factTwo := Fact{
		ID:         factTwoID,
		Name:       "urgent-order",
		Version:    NewDecimalUint64(1),
		Recency:    NewDecimalUint64(2),
		Generation: NewDecimalUint64(1),
		Sequence:   NewDecimalUint64(2),
		Support:    FactSupportLogical,
		Fields: map[string]Value{
			"order-id": NewValue(rules.StringValue("O-1")),
		},
		FieldPresence: map[string]FieldPresence{},
	}

	firingOne := Firing{
		Sequence:       NewDecimalUint64(1),
		RunID:          "run:1",
		ActivationID:   "activation:1",
		RuleID:         "rule:routing",
		RuleRevisionID: "revision:routing:1",
		RuleName:       "route-urgent-order",
		Module:         "MAIN",
		Salience:       10,
		Source:         span,
		FactIDs:        []string{factOneID, factTwoID},
	}
	firingTwo := Firing{
		Sequence:       NewDecimalUint64(2),
		RunID:          "run:1",
		ActivationID:   "activation:2",
		RuleID:         "rule:audit",
		RuleRevisionID: "revision:audit:1",
		RuleName:       "audit-route",
		Module:         "MAIN",
		Salience:       -5,
		FactIDs:        []string{factOneID, factOneID},
	}

	eventOne := Event{
		Sequence:   NewDecimalUint64(1),
		RunID:      "run:zero",
		Type:       EventFactAsserted,
		Severity:   SeverityInfo,
		Generation: NewDecimalUint64(1),
		Recency:    NewDecimalUint64(1),
		Source:     span,
		FactIDs:    []string{factOneID},
	}
	eventTwo := Event{
		Sequence:       NewDecimalUint64(2),
		RunID:          "run:1",
		Type:           EventRuleFired,
		Severity:       SeverityInfo,
		Generation:     NewDecimalUint64(1),
		Recency:        NewDecimalUint64(2),
		RuleID:         "rule:audit",
		RuleRevisionID: "revision:audit:1",
		ActivationID:   "activation:2",
		Source:         span,
		FactIDs:        []string{factOneID},
	}

	rowExpress := QueryRow{Cells: []QueryCell{
		{Alias: "z-route", Value: &express},
		{Alias: "a-order", FactID: &factOneID},
	}}
	rowStandard := QueryRow{Cells: []QueryCell{
		{Alias: "z-route", Value: &standard},
		{Alias: "a-order", FactID: &factTwoID},
	}}

	diagnosticOne := Diagnostic{ID: "diag:1", Phase: "compile", Severity: SeverityWarning, Code: "unused_global", Message: "global is unused", Target: "minimum", Span: span}
	diagnosticTwo := Diagnostic{ID: "diag:2", Phase: "run", Severity: SeverityInfo, Code: "completed", Message: "run completed", Target: "run:1"}
	counterOne := Counter{Name: "alpha_activations", Value: NewDecimalUint64(9007199254740993), Unit: "count"}
	counterTwo := Counter{Name: "beta_rows", Value: NewDecimalUint64(2), Unit: "count"}
	checkOne := CheckResult{Path: "expectations.factCount", Passed: true, Expected: "2", Actual: "2", Message: ""}
	checkTwo := CheckResult{Path: "expectations.terminalStatus", Passed: true, Expected: "quiescent", Actual: "quiescent", Message: ""}
	referenceOne := ArtifactReference{Kind: "explain", ID: "explain:1", SchemaVersion: "gess.explain.v1", Digest: digestA}
	referenceTwo := ArtifactReference{Kind: "why-not", ID: "why-not:1", SchemaVersion: "gess.why-not.v1", Digest: digestB}

	facts := []Fact{factTwo, factOne}
	firings := []Firing{firingTwo, firingOne}
	events := []Event{eventTwo, eventOne}
	rows := []QueryRow{rowStandard, rowExpress}
	diagnostics := []Diagnostic{diagnosticTwo, diagnosticOne}
	counters := []Counter{counterTwo, counterOne}
	checks := []CheckResult{checkTwo, checkOne}
	references := []ArtifactReference{referenceTwo, referenceOne}
	returned := int64(2)
	truncated := false
	output := "routed O-1\n"
	if limited {
		facts = []Fact{factOne}
		firings = []Firing{firingOne}
		events = []Event{eventOne}
		rows = []QueryRow{rowExpress}
		diagnostics = []Diagnostic{diagnosticOne}
		counters = []Counter{counterOne}
		checks = []CheckResult{checkOne}
		references = []ArtifactReference{referenceOne}
		returned = 1
		truncated = true
		output = "routed"
	}

	available := SectionStatus{Availability: SectionAvailable}
	return RunReport{
		SchemaVersion: RunReportSchemaVersion,
		Producer:      BuildInfo{Name: "gess-workbench", Version: "0.1.0"},
		Engine:        BuildInfo{Name: "gess", Version: "0.4.0"},
		Sources: []ResolvedSource{
			{Path: "rules/orders.gess", Digest: digestA},
			{Path: "rules/routing.gess", Digest: digestB},
		},
		ScenarioDigest:  scenarioDigest,
		RulesetID:       "ruleset:orders:v1",
		CallbackProfile: CallbackProfile{Name: "pure", Version: "1", Digest: digestB},
		Limits: AppliedLimits{
			Input:  InputLimits{MaxRequestBytes: 1048576, MaxSourceFiles: 10, MaxSourceFileBytes: 262144, MaxInitialFacts: 1000},
			Run:    RunOptions{Strategy: StrategyDepth, MaxFacts: 1000, MaxFirings: 100, DeadlineMS: 5000},
			Report: limits,
		},
		Terminal: TerminalResult{Status: TerminalQuiescent, RunID: "run:1", Fired: 2},
		Output: Output{
			Status:        available,
			LimitBytes:    limits.MaxOutputBytes,
			TotalBytes:    int64(len("routed O-1\n")),
			TotalKnown:    true,
			ReturnedBytes: int64(len(output)),
			Truncated:     limited,
			Text:          output,
		},
		Facts:   FactCollection{Status: available, Limit: limits.MaxFacts, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: facts},
		Firings: FiringCollection{Status: available, Limit: limits.MaxFirings, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: firings},
		Events:  EventCollection{Status: available, Limit: limits.MaxEvents, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: events},
		Queries: []QueryResult{
			{Name: "routes-by-lane", Args: map[string]Value{"lane": NewValue(rules.StringValue("expedite"))}, MaxRows: limits.MaxQueryRows, Rows: QueryRowCollection{Status: available, Limit: limits.MaxQueryRows, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: rows}},
			{Name: "orders-by-tier", Args: map[string]Value{"tier": NewValue(rules.StringValue("vip"))}, MaxRows: limits.MaxQueryRows, Rows: QueryRowCollection{Status: available, Limit: limits.MaxQueryRows, Total: 0, TotalKnown: true, Returned: 0, Items: []QueryRow{}}},
		},
		Diagnostics:     DiagnosticCollection{Status: available, Limit: limits.MaxDiagnostics, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: diagnostics},
		Counters:        CounterCollection{Status: available, Limit: limits.MaxCounters, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: counters},
		Checks:          CheckCollection{Status: available, Limit: limits.MaxChecks, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: checks},
		ExplanationRefs: ArtifactReferenceCollection{Status: available, Limit: limits.MaxExplanationRefs, Total: 2, TotalKnown: true, Returned: returned, Truncated: truncated, Items: references},
	}
}

func validActionFailureRunReport() RunReport {
	document := validRunReportArtifact(false)
	actionIndex := int64(0)
	payload := &ErrorPayload{Code: "callback_failed", Message: "audit sink unavailable", Span: document.Events.Items[0].Source}
	document.Terminal.Status = TerminalError
	document.Terminal.Error = cloneErrorPayload(payload)
	document.Events.Items[0].Type = EventActionFailed
	document.Events.Items[0].Severity = SeverityError
	document.Events.Items[0].ActionName = "record-audit"
	document.Events.Items[0].ActionIndex = &actionIndex
	document.Events.Items[0].Error = cloneErrorPayload(payload)
	return document
}

func emptyRunReportArtifact() RunReport {
	document := validRunReportArtifact(false)
	available := SectionStatus{Availability: SectionAvailable}
	document.Terminal.Fired = 0
	document.Output = Output{Status: available, LimitBytes: document.Limits.Report.MaxOutputBytes, TotalKnown: true}
	document.Facts = FactCollection{Status: available, Limit: document.Limits.Report.MaxFacts, TotalKnown: true, Items: []Fact{}}
	document.Firings = FiringCollection{Status: available, Limit: document.Limits.Report.MaxFirings, TotalKnown: true, Items: []Firing{}}
	document.Events = EventCollection{Status: available, Limit: document.Limits.Report.MaxEvents, TotalKnown: true, Items: []Event{}}
	document.Queries = []QueryResult{}
	document.Diagnostics = DiagnosticCollection{Status: available, Limit: document.Limits.Report.MaxDiagnostics, TotalKnown: true, Items: []Diagnostic{}}
	document.Counters = CounterCollection{Status: available, Limit: document.Limits.Report.MaxCounters, TotalKnown: true, Items: []Counter{}}
	document.Checks = CheckCollection{Status: available, Limit: document.Limits.Report.MaxChecks, TotalKnown: true, Items: []CheckResult{}}
	document.ExplanationRefs = ArtifactReferenceCollection{Status: available, Limit: document.Limits.Report.MaxExplanationRefs, TotalKnown: true, Items: []ArtifactReference{}}
	return document
}

func unavailableRunReportArtifact() RunReport {
	document := emptyRunReportArtifact()
	omitted := SectionStatus{Availability: SectionOmitted, Reason: "capture disabled"}
	unsupported := SectionStatus{Availability: SectionUnsupported, Reason: "engine does not expose this section"}
	document.Output.Status = omitted
	document.Output.TotalKnown = false
	document.Facts.Status = unsupported
	document.Facts.TotalKnown = false
	document.Firings.Status = omitted
	document.Firings.TotalKnown = false
	document.Events.Status = unsupported
	document.Events.TotalKnown = false
	document.Diagnostics.Status = omitted
	document.Diagnostics.TotalKnown = false
	document.Counters.Status = unsupported
	document.Counters.TotalKnown = false
	document.Checks.Status = omitted
	document.Checks.TotalKnown = false
	document.ExplanationRefs.Status = unsupported
	document.ExplanationRefs.TotalKnown = false
	document.Queries = []QueryResult{{
		Name:    "routes-by-lane",
		Args:    map[string]Value{},
		MaxRows: document.Limits.Report.MaxQueryRows,
		Rows: QueryRowCollection{
			Status: unsupported,
			Limit:  document.Limits.Report.MaxQueryRows,
			Items:  []QueryRow{},
		},
	}}
	return document
}

func assertOrderedSubstrings(t *testing.T, text string, values ...string) {
	t.Helper()
	position := -1
	for _, value := range values {
		next := strings.Index(text[position+1:], value)
		if next < 0 {
			t.Fatalf("%q not found after byte %d in %s", value, position, text)
		}
		position += next + 1
	}
}
