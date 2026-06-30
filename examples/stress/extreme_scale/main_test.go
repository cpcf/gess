package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dsl "github.com/cpcf/gess/dsl"
	rules "github.com/cpcf/gess/rules"
)

func TestGeneratedSourceCompiles(t *testing.T) {
	cfg := config{Rules: 6, Facts: 24, Queries: 3, Buckets: 4, Run: true, QuerySamples: 2}
	var source bytes.Buffer
	if err := writeGessSource(&source, cfg); err != nil {
		t.Fatalf("writeGessSource: %v", err)
	}
	doc, err := dsl.Parse("test.gess", source.Bytes())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	workspace := rules.NewWorkspace()
	if err := dsl.Load(t.Context(), workspace, doc, dsl.Registry{}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := workspace.Compile(t.Context()); err != nil {
		t.Fatalf("Compile: %v", err)
	}
}

func TestRunSmoke(t *testing.T) {
	cfg := config{Rules: 6, Facts: 24, Queries: 3, Buckets: 4, Run: true, QuerySamples: 2}
	var out bytes.Buffer
	if err := run(t.Context(), &out, cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"shape: engine=gess rules=6 facts=24 queries=3 buckets=4 run=true",
		"compile:",
		"run: fired=",
		"rete-memory: owner=alpha",
		"query: name=inputs-by-bucket-0000000",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestJessSourceAndDriverGeneration(t *testing.T) {
	cfg := config{Engine: "jess", Rules: 3, Facts: 8, Queries: 2, Buckets: 2, Run: true, QuerySamples: 2}
	var source bytes.Buffer
	if err := writeJessSource(&source, cfg); err != nil {
		t.Fatalf("writeJessSource: %v", err)
	}
	for _, want := range []string{
		"(deftemplate input",
		"(defrule route-bucket-0000002",
		"(defquery inputs-by-bucket-0000001",
	} {
		if !strings.Contains(source.String(), want) {
			t.Fatalf("Jess source missing %q:\n%s", want, source.String())
		}
	}

	driver, err := writeJessRunnerJava(t.TempDir())
	if err != nil {
		t.Fatalf("writeJessRunnerJava: %v", err)
	}
	driverSource, err := os.ReadFile(driver)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, want := range []string{
		"Rete engine = new Rete();",
		"engine.batch(script);",
		"writeMemory(\"after-load\");",
		"writeMemory(\"after-run\");",
		"writeMemory(\"after-query\");",
		"used=\" + used / 1024 / 1024 + \"MB\"",
		"QueryResult result = engine.runQueryStar(queryName(i), arguments);",
	} {
		if !strings.Contains(string(driverSource), want) {
			t.Fatalf("Jess runner missing %q:\n%s", want, string(driverSource))
		}
	}
}

func TestWriteOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extreme.gess")
	cfg := config{Rules: 3, Facts: 8, Queries: 2, Buckets: 2, WritePath: path, WriteOnly: true}
	var out bytes.Buffer
	if err := run(t.Context(), &out, cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(source), "(defrule route-bucket-0000002") {
		t.Fatalf("source missing generated rule:\n%s", string(source))
	}
	if !strings.Contains(out.String(), "write: path=") {
		t.Fatalf("output missing write line:\n%s", out.String())
	}
}
