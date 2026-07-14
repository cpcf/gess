package scenario_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/scenario"
)

var reportMetadata = scenario.ReportMetadata{
	Producer: scenario.BuildInfo{Name: "gess-workbench", Version: "test"},
	Engine:   scenario.BuildInfo{Name: "gess", Version: "test"},
}

func TestBuildRunReportIsCompleteBoundedAndDeterministic(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := reportScenario()
	resolver := mapResolver{"rules/main.gess": []byte(runnerSource)}

	firstExecution, firstErr := runner.Run(context.Background(), document, resolver)
	if firstErr != nil {
		t.Fatalf("Run first: %v", firstErr)
	}
	first, err := scenario.BuildRunReport(document, firstExecution, firstErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport first: %v", err)
	}
	firstJSON, err := scenario.MarshalRunReport(first)
	if err != nil {
		t.Fatalf("MarshalRunReport first: %v", err)
	}

	secondExecution, secondErr := runner.Run(context.Background(), document, resolver)
	if secondErr != nil {
		t.Fatalf("Run second: %v", secondErr)
	}
	second, err := scenario.BuildRunReport(document, secondExecution, secondErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport second: %v", err)
	}
	secondJSON, err := scenario.MarshalRunReport(second)
	if err != nil {
		t.Fatalf("MarshalRunReport second: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("equal executions produced different reports:\n%s\n%s", firstJSON, secondJSON)
	}

	if first.Terminal.Status != scenario.TerminalQuiescent || first.Terminal.Fired != 2 {
		t.Fatalf("terminal = %+v, want quiescent with 2 firings", first.Terminal)
	}
	assertCollection(t, "facts", first.Facts.Total, first.Facts.Returned, first.Facts.Limit, first.Facts.Truncated, 4, 2, 2, true)
	assertCollection(t, "firings", first.Firings.Total, first.Firings.Returned, first.Firings.Limit, first.Firings.Truncated, 2, 1, 1, true)
	if first.Firings.Items[0].RuleName != "copy-input" || first.Firings.Items[0].Module != "MAIN" || first.Firings.Items[0].Source == nil {
		t.Fatalf("firing metadata = %+v", first.Firings.Items[0])
	}
	if first.Events.Total <= first.Events.Returned || !first.Events.Truncated {
		t.Fatalf("events = %+v, want bounded detail with exact total", first.Events)
	}
	if first.Output.TotalBytes != 2 || first.Output.ReturnedBytes != 1 || !first.Output.Truncated {
		t.Fatalf("output = %+v", first.Output)
	}
	if len(first.Queries) != 1 || first.Queries[0].Rows.Total != 1 || first.Queries[0].Rows.Returned != 1 {
		t.Fatalf("queries = %+v", first.Queries)
	}
	if first.Diagnostics.Status.Availability != scenario.SectionAvailable || first.Diagnostics.Total != 1 {
		t.Fatalf("diagnostics = %+v", first.Diagnostics)
	}
	assertCollection(t, "counters", first.Counters.Total, first.Counters.Returned, first.Counters.Limit, first.Counters.Truncated, 5, 1, 1, true)
	assertCollection(t, "checks", first.Checks.Total, first.Checks.Returned, first.Checks.Limit, first.Checks.Truncated, 4, 1, 1, true)
	if first.ExplanationRefs.Status.Availability != scenario.SectionUnsupported || first.ExplanationRefs.Status.Reason == "" {
		t.Fatalf("explanation refs = %+v", first.ExplanationRefs)
	}
	if err := scenario.ValidateRunReport(first); err != nil {
		t.Fatalf("ValidateRunReport: %v", err)
	}
}

func TestBuildRunReportDoesNotAliasExecutionOrScenario(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := reportScenario()
	execution, runErr := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(runnerSource)})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	report, err := scenario.BuildRunReport(document, execution, runErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport: %v", err)
	}
	before, err := scenario.MarshalRunReport(report)
	if err != nil {
		t.Fatalf("MarshalRunReport before: %v", err)
	}

	execution.Sources[0].Path = "mutated.gess"
	execution.Rules[0].NameValue = "mutated"
	execution.Queries[0].Query.Args["wanted"] = scenario.NewValue(rules.StringValue("mutated"))
	document.Globals["enabled"] = scenario.NewValue(rules.BoolValue(false))
	document.Queries[0].Args["wanted"] = scenario.NewValue(rules.StringValue("mutated"))

	after, err := scenario.MarshalRunReport(report)
	if err != nil {
		t.Fatalf("MarshalRunReport after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("projected report aliased mutable inputs:\n%s\n%s", before, after)
	}
}

func TestBuildRunReportNormalizesScenarioOperationEvents(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := reportScenario()
	execution, runErr := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(runnerSource)})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if len(execution.Firings.Items) == 0 || len(execution.Events.Items) == 0 {
		t.Fatal("execution did not capture events")
	}
	execution.Firings.Items[0].RunID = rules.RunID(99)
	execution.Firings.Items[0].FactIDs = append(execution.Firings.Items[0].FactIDs, rules.FactID{})
	execution.Firings.Total++
	execution.Events.Items[0].RunID = rules.RunID(99)
	execution.Events.Items[0].FactIDs = append(execution.Events.Items[0].FactIDs, rules.FactID{})

	report, err := scenario.BuildRunReport(document, execution, runErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport: %v", err)
	}
	if err := scenario.ValidateRunReport(report); err != nil {
		t.Fatalf("ValidateRunReport: %v", err)
	}
	wantRunID := execution.Run.RunID.String()
	if report.Firings.Items[0].RunID != wantRunID || report.Events.Items[0].RunID != wantRunID {
		t.Fatalf("event run IDs = %q / %q, want %q", report.Firings.Items[0].RunID, report.Events.Items[0].RunID, wantRunID)
	}
	if report.Terminal.Fired != execution.Firings.Total {
		t.Fatalf("terminal fired = %d, want captured total %d", report.Terminal.Fired, execution.Firings.Total)
	}
	for _, id := range append(report.Firings.Items[0].FactIDs, report.Events.Items[0].FactIDs...) {
		if id == "fact:zero" {
			t.Fatal("zero synthetic fact ID leaked into report")
		}
	}
}

func TestBuildRunReportProjectsFactLimitOutcome(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := reportScenario()
	document.InitialFacts = nil
	document.Run.MaxFacts = 1
	document.ReportLimits.MaxEvents = 100
	execution, runErr := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(runnerSource)})
	if !errors.Is(runErr, rules.ErrFactLimit) {
		t.Fatalf("Run error = %v, want ErrFactLimit", runErr)
	}
	report, err := scenario.BuildRunReport(document, execution, runErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport: %v", err)
	}
	if report.Terminal.Status != scenario.TerminalMaxFacts || report.Terminal.Error != nil {
		t.Fatalf("terminal = %+v, want max_facts without error payload", report.Terminal)
	}
	foundActionFailure := false
	for _, event := range report.Events.Items {
		if event.Type == scenario.EventActionFailed {
			foundActionFailure = true
			if event.Error == nil || event.Severity != scenario.SeverityError {
				t.Fatalf("action failure event = %+v", event)
			}
		}
	}
	if !foundActionFailure {
		t.Fatal("max-facts report did not retain the engine action-failure event")
	}
	if _, err := scenario.MarshalRunReport(report); err != nil {
		t.Fatalf("MarshalRunReport: %v", err)
	}
}

func TestBuildRunReportProjectsRemainingTerminalOutcomes(t *testing.T) {
	t.Run("max firings", func(t *testing.T) {
		runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
		document := reportScenario()
		document.Run.MaxFirings = 1
		assertTerminalReport(t, runner, document, runnerSource, context.Background(), scenario.TerminalMaxFirings, nil)
	})

	t.Run("halted", func(t *testing.T) {
		profile := callbackProfile("halt")
		runner := newTestRunner(t, profile, dsl.Registry{Actions: map[string]rules.ActionFunc{
			"stop": func(action rules.ActionContext) error { return action.Halt() },
		}})
		document := reportScenario()
		document.CallbackProfile = profile
		document.ReportLimits.MaxEvents = 100
		source := strings.Replace(runnerSource, "(emit ?id)", "(call stop)", 1)
		assertTerminalReport(t, runner, document, source, context.Background(), scenario.TerminalHalted, nil)
	})

	t.Run("runtime error", func(t *testing.T) {
		profile := callbackProfile("failure")
		runner := newTestRunner(t, profile, dsl.Registry{Actions: map[string]rules.ActionFunc{
			"fail": func(rules.ActionContext) error { return errors.New("callback failed") },
		}})
		document := reportScenario()
		document.CallbackProfile = profile
		document.ReportLimits.MaxEvents = 100
		source := strings.Replace(runnerSource, "(emit ?id)", "(call fail)", 1)
		assertTerminalReport(t, runner, document, source, context.Background(), scenario.TerminalError, errors.New("want execution error"))
	})

	t.Run("deadline", func(t *testing.T) {
		profile := callbackProfile("deadline")
		runner := newTestRunner(t, profile, dsl.Registry{Actions: map[string]rules.ActionFunc{
			"wait": func(action rules.ActionContext) error {
				<-action.Context().Done()
				return action.Context().Err()
			},
		}})
		document := reportScenario()
		document.CallbackProfile = profile
		document.Run.DeadlineMS = 1
		document.ReportLimits.MaxEvents = 100
		source := strings.Replace(runnerSource, "(emit ?id)", "(call wait)", 1)
		assertTerminalReport(t, runner, document, source, context.Background(), scenario.TerminalDeadline, errors.New("want execution error"))
	})

	t.Run("canceled", func(t *testing.T) {
		profile := callbackProfile("canceled")
		started := make(chan struct{})
		var once sync.Once
		runner := newTestRunner(t, profile, dsl.Registry{Actions: map[string]rules.ActionFunc{
			"wait": func(action rules.ActionContext) error {
				once.Do(func() { close(started) })
				<-action.Context().Done()
				return action.Context().Err()
			},
		}})
		document := reportScenario()
		document.CallbackProfile = profile
		document.ReportLimits.MaxEvents = 100
		source := strings.Replace(runnerSource, "(emit ?id)", "(call wait)", 1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-started
			cancel()
		}()
		assertTerminalReport(t, runner, document, source, ctx, scenario.TerminalCanceled, errors.New("want execution error"))
	})
}

func TestBuildRunReportTrimsDetailToEncodedByteLimit(t *testing.T) {
	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	baselineDocument := reportScenario()
	resolver := mapResolver{"rules/main.gess": []byte(runnerSource)}
	baselineExecution, runErr := runner.Run(context.Background(), baselineDocument, resolver)
	if runErr != nil {
		t.Fatalf("Run baseline: %v", runErr)
	}
	baseline, err := scenario.BuildRunReport(baselineDocument, baselineExecution, runErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport baseline: %v", err)
	}
	baselineJSON, err := scenario.MarshalRunReport(baseline)
	if err != nil {
		t.Fatalf("MarshalRunReport baseline: %v", err)
	}

	document := reportScenario()
	document.ReportLimits.MaxReportBytes = int64(len(baselineJSON) - 200)
	execution, runErr := runner.Run(context.Background(), document, resolver)
	if runErr != nil {
		t.Fatalf("Run limited: %v", runErr)
	}
	report, err := scenario.BuildRunReport(document, execution, runErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport limited: %v", err)
	}
	encoded, err := scenario.MarshalRunReport(report)
	if err != nil {
		t.Fatalf("MarshalRunReport limited: %v", err)
	}
	if int64(len(encoded)) > document.ReportLimits.MaxReportBytes {
		t.Fatalf("encoded size = %d, limit %d", len(encoded), document.ReportLimits.MaxReportBytes)
	}
	if !report.Diagnostics.Truncated && !report.Counters.Truncated && !report.Checks.Truncated && !report.Events.Truncated && !report.Facts.Truncated && !report.Firings.Truncated && !report.Output.Truncated {
		t.Fatal("byte-limit fit did not mark any available section truncated")
	}
}

func TestBuildRunReportRejectsIncompleteExecutionAndImpossibleEnvelope(t *testing.T) {
	if _, err := scenario.BuildRunReport(reportScenario(), scenario.ExecutionResult{}, nil, reportMetadata); !errors.Is(err, scenario.ErrRunReportProjection) {
		t.Fatalf("incomplete execution error = %v, want ErrRunReportProjection", err)
	}

	runner := newTestRunner(t, scenario.DisabledCallbackProfile(), dsl.Registry{})
	document := reportScenario()
	document.ReportLimits.MaxReportBytes = 1
	execution, runErr := runner.Run(context.Background(), document, mapResolver{"rules/main.gess": []byte(runnerSource)})
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if _, err := scenario.BuildRunReport(document, execution, runErr, reportMetadata); !errors.Is(err, scenario.ErrRunReportTooLarge) {
		t.Fatalf("impossible envelope error = %v, want ErrRunReportTooLarge", err)
	}
}

func reportScenario() scenario.Scenario {
	document := testScenario()
	facts, firings := int64(4), int64(2)
	document.Expectations = &scenario.Expectations{
		TerminalStatus: scenario.TerminalQuiescent,
		FactCount:      &facts,
		FiringCount:    &firings,
		QueryRowCounts: map[string]int64{"result-by-id": 1},
	}
	return document
}

func assertTerminalReport(t *testing.T, runner *scenario.Runner, document scenario.Scenario, source string, ctx context.Context, want scenario.TerminalStatus, wantExecutionErr error) {
	t.Helper()
	execution, runErr := runner.Run(ctx, document, mapResolver{"rules/main.gess": []byte(source)})
	if (runErr != nil) != (wantExecutionErr != nil) {
		t.Fatalf("Run error = %v, want error presence %t", runErr, wantExecutionErr != nil)
	}
	report, err := scenario.BuildRunReport(document, execution, runErr, reportMetadata)
	if err != nil {
		t.Fatalf("BuildRunReport: %v", err)
	}
	if report.Terminal.Status != want {
		t.Fatalf("terminal status = %q, want %q", report.Terminal.Status, want)
	}
	if want == scenario.TerminalError && report.Terminal.Error == nil {
		t.Fatal("error terminal has no structured error payload")
	}
	if _, err := scenario.MarshalRunReport(report); err != nil {
		t.Fatalf("MarshalRunReport: %v", err)
	}
}

func assertCollection(t *testing.T, name string, total, returned, limit int64, truncated bool, wantTotal, wantReturned, wantLimit int64, wantTruncated bool) {
	t.Helper()
	if total != wantTotal || returned != wantReturned || limit != wantLimit || truncated != wantTruncated {
		t.Fatalf("%s = total %d returned %d limit %d truncated %t; want %d %d %d %t", name, total, returned, limit, truncated, wantTotal, wantReturned, wantLimit, wantTruncated)
	}
}
