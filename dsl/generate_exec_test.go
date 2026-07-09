package dsl_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	dsl "github.com/cpcf/gess/dsl"
)

const generateExecSource = `
(deftemplate order
  (slot lane (type STRING) (required TRUE))
  (slot amount (type FLOAT) (default 2.0))
  (slot note (type STRING) (default "line\nbreak \"q\" \\")))

(deftemplate routed
  (slot lane (type STRING) (required TRUE)))

(deffacts seed
  (order (lane "expedite")))

(defrule route
  (declare (salience 5))
  (order (lane ?lane) (amount ?amount))
  (test (> ?amount 1.5))
  =>
  (assert (routed (lane ?lane))))
`

const generateExecDriver = `package main

import (
	"context"
	"fmt"
	"os"

	gessdsl "github.com/cpcf/gess/dsl"
)

func main() {
	ruleset, initials, err := BuildRuleset(context.Background(), gessdsl.Registry{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(ruleset.ID().String())
	fmt.Println(len(initials))
}
`

// TestGeneratedBuildExecutesAndMatchesCompile runs generated code for real:
// the emitted BuildRuleset must succeed at startup and produce a ruleset with
// the same content-addressed ID as compiling the same source in process.
func TestGeneratedBuildExecutesAndMatchesCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping generated-code execution in -short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable")
	}

	ctx := context.Background()
	generated, err := dsl.GenerateGo(ctx,
		[]dsl.SourceFile{{Name: "genexec.gess", Source: []byte(generateExecSource)}},
		dsl.GoGeneratorOptions{PackageName: "main", FunctionName: "BuildRuleset"})
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	compiled, err := dsl.Compile(ctx, "genexec.gess", []byte(generateExecSource), dsl.Registry{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// The go tool skips testdata during package discovery, so a leftover
	// directory from a crashed run can never break ./... builds; go run with
	// an explicit path still works.
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dir, err := os.MkdirTemp("testdata", "genexec-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, content := range map[string][]byte{
		"gen.go":  generated,
		"main.go": []byte(generateExecDriver),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	cmd := exec.Command(goBin, "run", "./"+filepath.ToSlash(dir))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run of generated code failed: %v\nstderr:\n%s\ngenerated:\n%s", err, stderr.String(), generated)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("driver output = %q, want ruleset ID and initial-fact count", stdout.String())
	}
	if want := compiled.ID().String(); lines[0] != want {
		t.Fatalf("generated ruleset ID = %s, want %s from in-process Compile", lines[0], want)
	}
	if lines[1] != "1" {
		t.Fatalf("generated initial facts = %s, want 1", lines[1])
	}
}
