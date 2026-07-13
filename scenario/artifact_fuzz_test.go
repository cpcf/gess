package scenario

import (
	"bytes"
	"testing"
)

func FuzzScenarioJSONCanonicalStability(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schemaVersion":"gess.workbench.scenario.v1"}`))
	f.Add([]byte{0xff})
	valid, err := MarshalScenario(validScenarioArtifact())
	if err != nil {
		f.Fatalf("MarshalScenario(seed) error = %v", err)
	}
	f.Add(valid)

	f.Fuzz(func(t *testing.T, data []byte) {
		document, err := UnmarshalScenario(data)
		if err != nil {
			return
		}
		canonical, err := MarshalScenario(document)
		if err != nil {
			t.Fatalf("MarshalScenario(accepted document) error = %v", err)
		}
		redecoded, err := UnmarshalScenario(canonical)
		if err != nil {
			t.Fatalf("UnmarshalScenario(canonical document) error = %v; JSON = %s", err, canonical)
		}
		reencoded, err := MarshalScenario(redecoded)
		if err != nil {
			t.Fatalf("MarshalScenario(redecoded document) error = %v", err)
		}
		if !bytes.Equal(reencoded, canonical) {
			t.Fatalf("second canonical encoding = %s, want %s", reencoded, canonical)
		}
	})
}

func FuzzRunReportJSONCanonicalStability(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schemaVersion":"gess.workbench.report.v1"}`))
	f.Add([]byte{0xff})
	for _, document := range []RunReport{
		validRunReportArtifact(false),
		validRunReportArtifact(true),
		unavailableRunReportArtifact(),
	} {
		valid, err := MarshalRunReport(document)
		if err != nil {
			f.Fatalf("MarshalRunReport(seed) error = %v", err)
		}
		f.Add(valid)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		document, err := UnmarshalRunReport(data)
		if err != nil {
			return
		}
		canonical, err := MarshalRunReport(document)
		if err != nil {
			t.Fatalf("MarshalRunReport(accepted document) error = %v", err)
		}
		redecoded, err := UnmarshalRunReport(canonical)
		if err != nil {
			t.Fatalf("UnmarshalRunReport(canonical document) error = %v; JSON = %s", err, canonical)
		}
		reencoded, err := MarshalRunReport(redecoded)
		if err != nil {
			t.Fatalf("MarshalRunReport(redecoded document) error = %v", err)
		}
		if !bytes.Equal(reencoded, canonical) {
			t.Fatalf("second canonical encoding = %s, want %s", reencoded, canonical)
		}
	})
}
