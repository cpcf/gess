package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot walks up from this test file to the module root (the directory with
// go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test file")
		}
		dir = parent
	}
}

// TestGenerateGessGoCompiles generates Go for a source exercising every RHS
// action verb (including focus/halt and function-call action values) and runs
// `go build` on the output against the real rules/dsl/session packages. Unlike
// the parse-only round-trip test, this catches undefined identifiers and type
// errors in the emitted code.
func TestGenerateGessGoCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go build in short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	source := []byte(`
(defmodule OTHER)

(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot a (type INTEGER) (required TRUE))
  (slot b (type INTEGER) (required TRUE))
  (slot total (type INTEGER) (required TRUE))
  (slot status (type STRING) (required TRUE))
  (slot note (type STRING)))

(deftemplate flag (slot id (type STRING) (required TRUE)))
(deftemplate cancellation (slot order (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (a 3) (b 4) (total 0) (status "new")))

(defrule ship
  ?order <- (order (id ?id) (a ?a) (b ?b) (status "new"))
  (not (cancellation (order ?id)))
  =>
  (bind ?total (+ ?a ?b))
  (assert (flag (id ?id)))
  (modify ?order (set (total ?total) (status (str-cat "shipped-" ?id))) (unset note))
  (emit "order " ?id " total " ?total)
  (call notify ?id ?total)
  (focus OTHER)
  (pop-focus)
  (clear-focus)
  (halt))

(defrule drop
  ?order <- (order (id ?id) (status "new"))
  (cancellation (order ?id))
  =>
  (retract ?order))
`)
	generated, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "all.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "genrules",
		FunctionName: "Build",
	})
	if err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}

	dir := t.TempDir()
	root := repoRoot(t)
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), generated, 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module gencheck\n\ngo 1.26\n\nrequire github.com/cpcf/gess v0.0.0\n\nreplace github.com/cpcf/gess => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code failed to build: %v\n%s\n--- generated ---\n%s", err, out, generated)
	}

	text := string(generated)
	for _, want := range []string{
		"gessrules.ActionEffectPushFocus",
		"gessrules.ActionEffectHalt",
		"gessrules.ActionEffectModify",
		"gessrules.ActionEffectBind",
		`gessrules.Call("+"`,
		"gessrules.ActionCallSpec",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source missing %q", want)
		}
	}
}
