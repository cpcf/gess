package scenario_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/scenario"
)

const runnerSource = `(deftemplate input
  (declare (duplicate-policy unique-key) (duplicate-key id))
  (slot id (type STRING) (required TRUE)))

(defglobal *enabled* (type BOOLEAN) (default FALSE))

(deftemplate result
  (declare (duplicate-policy unique-key) (duplicate-key id))
  (slot id (type STRING) (required TRUE)))

(deffacts selected
  (input (id "A")))

(deffacts ignored
  (input (id "X")))

(defrule copy-input
  (input (id ?id))
  (test (= *enabled* TRUE))
  =>
  (assert (result (id ?id)))
  (emit ?id))

(defquery result-by-id
  (declare (variables ?wanted))
  (result (id ?wanted))
  (return (id ?wanted)))
`

func TestRunnerExecutesSelectedScenarioDeterministically(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := testScenario()
	resolver := mapResolver{"rules/main.gess": []byte(runnerSource)}

	first, err := runner.Run(context.Background(), document, resolver)
	if err != nil {
		t.Fatalf("Run first: %v", err)
	}
	second, err := runner.Run(context.Background(), document, resolver)
	if err != nil {
		t.Fatalf("Run second: %v", err)
	}
	if first.Outcome != scenario.OutcomeCompleted || first.Run.Fired != 2 {
		t.Fatalf("outcome = %q, fired = %d, want completed and 2", first.Outcome, first.Run.Fired)
	}
	if first.Facts.Total != 4 || len(first.Facts.Items) != 2 || !first.Facts.Truncated {
		t.Fatalf("facts = %+v, want total 4 returned 2 truncated", first.Facts)
	}
	for _, fact := range first.Facts.Items {
		id, ok := fact.Field("id")
		if !ok {
			t.Fatalf("captured fact %s has no id", fact.ID())
		}
		text, _ := id.AsString()
		if text == "X" {
			t.Fatal("unselected deffacts declaration was asserted")
		}
	}
	if first.Firings.Total != 2 || len(first.Firings.Items) != 1 || !first.Firings.Truncated {
		t.Fatalf("firings = %+v, want total 2 returned 1 truncated", first.Firings)
	}
	if first.Events.Total <= 1 || len(first.Events.Items) != 1 || !first.Events.Truncated {
		t.Fatalf("events = %+v, want a bounded truncated capture", first.Events)
	}
	if first.Output.TotalBytes != 2 || len(first.Output.Text) != 1 || !first.Output.Truncated {
		t.Fatalf("output = %+v, want one of two bytes", first.Output)
	}
	if len(first.Queries) != 1 || first.Queries[0].Total != 1 || len(first.Queries[0].Rows) != 1 {
		t.Fatalf("queries = %+v, want one result-by-id row", first.Queries)
	}
	rowValue, ok := first.Queries[0].Rows[0].Value("id")
	if !ok || !rowValue.Equal(rules.StringValue("A")) {
		t.Fatalf("query id = %v, %t, want A", rowValue, ok)
	}
	if len(first.Sources) != 1 || first.Sources[0].Digest != digest([]byte(runnerSource)) {
		t.Fatalf("resolved sources = %+v", first.Sources)
	}
	assertEquivalentExecution(t, first, second)
}

func TestRunnerAppliesGlobalsAndAgendaStrategy(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	resolver := mapResolver{"rules/main.gess": []byte(runnerSource)}

	disabled := testScenario()
	disabled.Globals["enabled"] = scenario.NewValue(rules.BoolValue(false))
	disabled.ReportLimits.MaxOutputBytes = 10
	result, err := runner.Run(context.Background(), disabled, resolver)
	if err != nil {
		t.Fatalf("Run disabled: %v", err)
	}
	if result.Run.Fired != 0 || result.Output.Text != "" {
		t.Fatalf("disabled global run = fired %d, output %q", result.Run.Fired, result.Output.Text)
	}

	breadth := testScenario()
	breadth.Run.Strategy = scenario.StrategyBreadth
	breadth.ReportLimits.MaxOutputBytes = 10
	result, err = runner.Run(context.Background(), breadth, resolver)
	if err != nil {
		t.Fatalf("Run breadth: %v", err)
	}
	if result.Output.Text != "AB" {
		t.Fatalf("breadth output = %q, want AB", result.Output.Text)
	}
}

func TestRunnerRejectsBoundsDigestsProfilesAndDeffacts(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	resolver := mapResolver{"rules/main.gess": []byte(runnerSource)}
	tests := []struct {
		name   string
		mutate func(*scenario.Scenario)
		want   error
		stage  scenario.ExecutionStage
	}{
		{
			name: "run ceiling",
			mutate: func(document *scenario.Scenario) {
				document.Run.MaxFacts = 101
			},
			want:  scenario.ErrScenarioLimit,
			stage: scenario.StageValidate,
		},
		{
			name: "source digest",
			mutate: func(document *scenario.Scenario) {
				document.Sources[0].Digest = digest([]byte("different"))
			},
			want:  scenario.ErrSourceResolution,
			stage: scenario.StageResolve,
		},
		{
			name: "callback profile",
			mutate: func(document *scenario.Scenario) {
				document.CallbackProfile = callbackProfile("other")
			},
			want:  scenario.ErrCallbackProfile,
			stage: scenario.StageValidate,
		},
		{
			name: "missing deffacts",
			mutate: func(document *scenario.Scenario) {
				document.Deffacts = []string{"missing"}
			},
			want:  scenario.ErrDeffactsSelection,
			stage: scenario.StageSession,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			document := testScenario()
			tc.mutate(&document)
			result, err := runner.Run(context.Background(), document, resolver)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run error = %v, want %v", err, tc.want)
			}
			if result.Outcome != scenario.OutcomeValidationError || result.Stage != tc.stage {
				t.Fatalf("result = (%q, %q), error = %v, want validation_error at %q", result.Outcome, result.Stage, err, tc.stage)
			}
		})
	}
}

func TestRunnerClassifiesFireFactDeadlineAndCancellationLimits(t *testing.T) {
	t.Run("fire limit", func(t *testing.T) {
		runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
		document := testScenario()
		document.Run.MaxFirings = 1
		result, err := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(runnerSource)})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Outcome != scenario.OutcomeMaxFirings || result.Run.Fired != 1 {
			t.Fatalf("result = (%q, %d), want max_firings and 1", result.Outcome, result.Run.Fired)
		}
	})

	t.Run("fact limit", func(t *testing.T) {
		runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
		document := testScenario()
		document.InitialFacts = nil
		document.Run.MaxFacts = 1
		result, err := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(runnerSource)})
		if !errors.Is(err, rules.ErrFactLimit) {
			t.Fatalf("Run error = %v, want ErrFactLimit", err)
		}
		if result.Outcome != scenario.OutcomeMaxFacts || result.Facts.Total != 1 {
			t.Fatalf("result = (%q, facts %d), want max_facts and 1 retained", result.Outcome, result.Facts.Total)
		}
	})

	t.Run("deadline error during run", func(t *testing.T) {
		profile := callbackProfile("blocking")
		registry := dsl.Registry{Actions: map[string]rules.ActionFunc{
			"wait": func(action rules.ActionContext) error {
				return context.DeadlineExceeded
			},
		}}
		runner := newTestRunner(t, profile, registry)
		document := testScenario()
		document.CallbackProfile = profile
		source := strings.Replace(runnerSource, "(emit ?id)", "(call wait)", 1)
		result, err := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(source)})
		if err == nil || result.Outcome != scenario.OutcomeDeadline || result.Stage != scenario.StageRun {
			t.Fatalf("result = (%q, %q), error = %v, want deadline at run", result.Outcome, result.Stage, err)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := runner.Run(ctx, testScenario(), mapResolver{"rules/main.gess": []byte(runnerSource)})
		if !errors.Is(err, context.Canceled) || result.Outcome != scenario.OutcomeCanceled {
			t.Fatalf("result = %q, error = %v, want canceled", result.Outcome, err)
		}
	})
}

func TestRunnerReportsMissingCallbackAtLoad(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := testScenario()
	source := strings.Replace(runnerSource, "(emit ?id)", "(call missing)", 1)
	result, err := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(source)})
	if err == nil || result.Outcome != scenario.OutcomeValidationError || result.Stage != scenario.StageLoad {
		t.Fatalf("result = (%q, %q), error = %v, want validation error at load", result.Outcome, result.Stage, err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v, want missing callback name", err)
	}
}

func newTestRunner(t *testing.T, profile scenario.CallbackProfile, registry dsl.Registry) *scenario.Runner {
	t.Helper()
	runner, err := scenario.NewRunner(scenario.RunnerConfig{
		InputLimits: scenario.InputLimits{
			MaxRequestBytes:    1 << 20,
			MaxSourceFiles:     4,
			MaxSourceFileBytes: 1 << 16,
			MaxInitialFacts:    10,
		},
		RunLimits: scenario.RunOptions{
			MaxFacts:   100,
			MaxFirings: 100,
			DeadlineMS: 1000,
		},
		ReportLimits: scenario.ReportLimits{
			MaxFacts:           100,
			MaxFirings:         100,
			MaxEvents:          100,
			MaxQueryRows:       100,
			MaxDiagnostics:     100,
			MaxCounters:        100,
			MaxChecks:          100,
			MaxExplanationRefs: 100,
			MaxOutputBytes:     100,
			MaxReportBytes:     1 << 20,
		},
		CallbackProfile:       profile,
		Registry:              registry,
		MaxDemandCascadeSteps: 100,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return runner
}

func testScenario() scenario.Scenario {
	return scenario.Scenario{
		SchemaVersion: scenario.ScenarioSchemaVersion,
		Name:          "runner-test",
		Sources:       []scenario.ScenarioSource{{Path: "rules/main.gess"}},
		InitialFacts: []scenario.InitialFact{{
			Template: "input",
			Fields:   map[string]scenario.Value{"id": scenario.NewValue(rules.StringValue("B"))},
		}},
		Deffacts: []string{"selected"},
		Globals: map[string]scenario.Value{
			"enabled": scenario.NewValue(rules.BoolValue(true)),
		},
		CallbackProfile: scenario.DisabledCallbackProfile(),
		Run: scenario.RunOptions{
			Strategy:   scenario.StrategyDepth,
			MaxFacts:   10,
			MaxFirings: 10,
			DeadlineMS: 500,
		},
		ReportLimits: scenario.ReportLimits{
			MaxFacts:           2,
			MaxFirings:         1,
			MaxEvents:          1,
			MaxQueryRows:       1,
			MaxDiagnostics:     1,
			MaxCounters:        1,
			MaxChecks:          1,
			MaxExplanationRefs: 1,
			MaxOutputBytes:     1,
			MaxReportBytes:     1 << 16,
		},
		Queries: []scenario.ScenarioQuery{{
			Name:    "result-by-id",
			Args:    map[string]scenario.Value{"wanted": scenario.NewValue(rules.StringValue("A"))},
			MaxRows: 1,
		}},
	}
}

type mapResolver map[string][]byte

func (r mapResolver) ResolveScenarioSource(_ context.Context, path string) ([]byte, error) {
	content, ok := r[path]
	if !ok {
		return nil, fmt.Errorf("not found: %s", path)
	}
	return append([]byte(nil), content...), nil
}

func callbackProfile(name string) scenario.CallbackProfile {
	return scenario.CallbackProfile{Name: name, Version: "1", Digest: digest([]byte("profile:" + name))}
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assertEquivalentExecution(t *testing.T, left, right scenario.ExecutionResult) {
	t.Helper()
	if left.Outcome != right.Outcome || left.Run.Status != right.Run.Status || left.Run.Fired != right.Run.Fired || left.Output != right.Output || left.Facts.Total != right.Facts.Total || left.Firings.Total != right.Firings.Total || left.Events.Total != right.Events.Total {
		t.Fatalf("execution summaries differ:\nleft:  %+v\nright: %+v", left, right)
	}
	leftFactIDs := make([]string, len(left.Facts.Items))
	rightFactIDs := make([]string, len(right.Facts.Items))
	for i, fact := range left.Facts.Items {
		leftFactIDs[i] = fact.ID().String()
	}
	for i, fact := range right.Facts.Items {
		rightFactIDs[i] = fact.ID().String()
	}
	if !reflect.DeepEqual(leftFactIDs, rightFactIDs) {
		t.Fatalf("fact order differs: %v != %v", leftFactIDs, rightFactIDs)
	}
	leftFiringSequences := make([]uint64, len(left.Firings.Items))
	rightFiringSequences := make([]uint64, len(right.Firings.Items))
	for i, event := range left.Firings.Items {
		leftFiringSequences[i] = event.Sequence
	}
	for i, event := range right.Firings.Items {
		rightFiringSequences[i] = event.Sequence
	}
	if !reflect.DeepEqual(leftFiringSequences, rightFiringSequences) {
		t.Fatalf("firing order differs: %v != %v", leftFiringSequences, rightFiringSequences)
	}
}
