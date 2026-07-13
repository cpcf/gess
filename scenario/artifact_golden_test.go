package scenario

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestScenarioGoldenFixtureIsCanonical(t *testing.T) {
	t.Parallel()

	path := filepath.Join("testdata", "v1", "order-lifecycle.gess-scenario.json")
	want, err := MarshalScenario(validScenarioArtifact())
	if err != nil {
		t.Fatal(err)
	}
	fixture := readGoldenFixture(t, path, want)
	document, err := UnmarshalScenario(fixture)
	if err != nil {
		t.Fatalf("UnmarshalScenario(%s) error = %v", path, err)
	}
	canonical, err := MarshalScenario(document)
	if err != nil {
		t.Fatalf("MarshalScenario(%s) error = %v", path, err)
	}
	if !bytes.Equal(canonical, fixture) {
		t.Fatalf("%s is not canonical:\n got: %s\nwant: %s", path, canonical, fixture)
	}
	if !bytes.Equal(fixture, want) {
		t.Fatalf("%s does not represent the expected scenario:\n got: %s\nwant: %s", path, fixture, want)
	}

	digest, err := ScenarioDigest(document)
	if err != nil {
		t.Fatalf("ScenarioDigest(%s) error = %v", path, err)
	}
	if len(digest) != len("sha256:")+64 || digest[:len("sha256:")] != "sha256:" {
		t.Fatalf("ScenarioDigest(%s) = %q, want lowercase sha256 digest", path, digest)
	}
}

func TestRunReportGoldenFixturesAreCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		document RunReport
	}{
		{name: "full", filename: "order-lifecycle.full.gess-report.json", document: validRunReportArtifact(false)},
		{name: "limited", filename: "order-lifecycle.limited.gess-report.json", document: validRunReportArtifact(true)},
		{name: "unavailable sections", filename: "order-lifecycle.unavailable.gess-report.json", document: unavailableRunReportArtifact()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("testdata", "v1", test.filename)
			want, err := MarshalRunReport(test.document)
			if err != nil {
				t.Fatalf("MarshalRunReport(expected) error = %v", err)
			}
			fixture := readGoldenFixture(t, path, want)
			document, err := UnmarshalRunReport(fixture)
			if err != nil {
				t.Fatalf("UnmarshalRunReport(%s) error = %v", path, err)
			}
			canonical, err := MarshalRunReport(document)
			if err != nil {
				t.Fatalf("MarshalRunReport(%s) error = %v", path, err)
			}
			if !bytes.Equal(canonical, fixture) {
				t.Fatalf("%s is not canonical:\n got: %s\nwant: %s", path, canonical, fixture)
			}
			if !bytes.Equal(fixture, want) {
				t.Fatalf("%s does not represent the expected report:\n got: %s\nwant: %s", path, fixture, want)
			}

			digest, err := RunReportDigest(document)
			if err != nil {
				t.Fatalf("RunReportDigest(%s) error = %v", path, err)
			}
			if len(digest) != len("sha256:")+64 || digest[:len("sha256:")] != "sha256:" {
				t.Fatalf("RunReportDigest(%s) = %q, want lowercase sha256 digest", path, digest)
			}
		})
	}
}

func readGoldenFixture(t *testing.T, path string, expected []byte) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\ncanonical fixture body: %s", path, err, expected)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("%s must end with exactly one newline", path)
	}
	withoutNewline := data[:len(data)-1]
	if len(withoutNewline) > 0 && withoutNewline[len(withoutNewline)-1] == '\n' {
		t.Fatalf("%s must end with exactly one newline", path)
	}
	return withoutNewline
}
