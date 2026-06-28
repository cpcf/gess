package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTutorialSourceReportsStarterProgress(t *testing.T) {
	root := mustModuleRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "tutorial/vulnerability_response/starter/rules.gess"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := runTutorialSource(t.Context(), source)
	if err != nil {
		t.Fatalf("runTutorialSource: %v", err)
	}
	progress := evaluateProgress(output, checkpoints)
	if got := len(progress.Complete); got != 0 {
		t.Fatalf("complete = %d, want 0", got)
	}
	if progress.Next == nil || progress.Next.Number != 1 {
		t.Fatalf("next = %+v, want checkpoint 1", progress.Next)
	}
}

func TestRunTutorialSourceReportsSolutionProgress(t *testing.T) {
	root := mustModuleRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "tutorial/vulnerability_response/solution/rules.gess"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := runTutorialSource(t.Context(), source)
	if err != nil {
		t.Fatalf("runTutorialSource: %v", err)
	}
	progress := evaluateProgress(output, checkpoints)
	if got, want := len(progress.Complete), len(checkpoints); got != want {
		t.Fatalf("complete = %d, want %d", got, want)
	}
	if progress.Next != nil {
		t.Fatalf("next = %+v, want nil", progress.Next)
	}
}

func TestWebTutorialEndpointReturnsState(t *testing.T) {
	root := mustModuleRoot(t)
	a := app{root: root, out: bytes.NewBuffer(nil), err: bytes.NewBuffer(nil)}
	req := httptest.NewRequest(http.MethodGet, "/api/tutorial", nil)
	rec := httptest.NewRecorder()
	a.handleTutorial(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response tutorialStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Steps) != len(webSteps) {
		t.Fatalf("steps = %d, want %d", len(response.Steps), len(webSteps))
	}
	if response.Solution == "" {
		t.Fatalf("response has empty solution source")
	}
	if response.Steps[0].Number != 0 || response.Steps[0].Example != "" {
		t.Fatalf("first page = %+v, want non-coding overview", response.Steps[0])
	}
}

func TestWebStepExamplesCompleteTutorial(t *testing.T) {
	root := mustModuleRoot(t)
	starter, err := os.ReadFile(filepath.Join(root, "tutorial/vulnerability_response/starter/rules.gess"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(starter)
	completed := 0
	for _, step := range webSteps {
		if step.Number == 0 {
			if step.Example != "" {
				t.Fatalf("overview has example")
			}
			if len(step.Walkthrough) == 0 {
				t.Fatalf("overview has empty walkthrough")
			}
			continue
		}
		if step.Example == "" {
			t.Fatalf("step %d has empty example", step.Number)
		}
		if len(step.Walkthrough) == 0 {
			t.Fatalf("step %d has empty walkthrough", step.Number)
		}
		source = insertBeforeQueriesForTest(t, source, step.Example)
		output, err := runTutorialSource(t.Context(), []byte(source))
		if err != nil {
			t.Fatalf("run step %d: %v", step.Number, err)
		}
		progress := evaluateProgress(output, checkpoints)
		completed++
		if got, want := len(progress.Complete), completed; got != want {
			t.Fatalf("after step %d complete = %d, want %d\noutput:\n%s", step.Number, got, want, output)
		}
	}
}

func TestWebRunEndpointChecksSolution(t *testing.T) {
	root := mustModuleRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "tutorial/vulnerability_response/solution/rules.gess"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(runRequest{Source: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	a := app{root: root, out: bytes.NewBuffer(nil), err: bytes.NewBuffer(nil)}
	req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response runResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.OK {
		t.Fatalf("run failed: %s", response.Error)
	}
	if got, want := len(response.Complete), len(checkpoints); got != want {
		t.Fatalf("complete = %d, want %d", got, want)
	}
}

func insertBeforeQueriesForTest(t *testing.T, source string, example string) string {
	t.Helper()
	marker := "\n(defquery "
	index := strings.Index(source, marker)
	if strings.HasPrefix(example, "(defrule ") && index >= 0 {
		return source[:index] + "\n\n" + example + "\n" + source[index:]
	}
	return strings.TrimRight(source, "\n") + "\n\n" + example + "\n"
}

func mustModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}
