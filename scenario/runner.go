package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

var (
	// ErrInvalidRunnerConfig identifies an invalid process-owned runner bound.
	ErrInvalidRunnerConfig = errors.New("invalid scenario runner configuration")
	// ErrScenarioLimit identifies a scenario that exceeds a process-owned bound.
	ErrScenarioLimit = errors.New("scenario exceeds runner limit")
	// ErrSourceResolution identifies a source that could not be resolved or whose digest did not match.
	ErrSourceResolution = errors.New("scenario source resolution failed")
	// ErrCallbackProfile identifies a scenario callback profile the runner does not provide.
	ErrCallbackProfile = errors.New("scenario callback profile unavailable")
	// ErrDeffactsSelection identifies a missing or ambiguous named deffacts selection.
	ErrDeffactsSelection = errors.New("scenario deffacts selection failed")
)

// ExecutionOutcome classifies the complete scenario operation.
type ExecutionOutcome string

const (
	OutcomeCompleted       ExecutionOutcome = "completed"
	OutcomeMaxFacts        ExecutionOutcome = "max_facts"
	OutcomeMaxFirings      ExecutionOutcome = "max_firings"
	OutcomeDeadline        ExecutionOutcome = "deadline"
	OutcomeCanceled        ExecutionOutcome = "canceled"
	OutcomeHalted          ExecutionOutcome = "halted"
	OutcomeValidationError ExecutionOutcome = "validation_error"
	OutcomeRuntimeError    ExecutionOutcome = "runtime_error"
)

// ExecutionStage identifies the runner phase that produced an error.
type ExecutionStage string

const (
	StageValidate ExecutionStage = "validate"
	StageResolve  ExecutionStage = "resolve"
	StageParse    ExecutionStage = "parse"
	StageLoad     ExecutionStage = "load"
	StageCompile  ExecutionStage = "compile"
	StageSession  ExecutionStage = "session"
	StageRun      ExecutionStage = "run"
	StageQuery    ExecutionStage = "query"
)

// ExecutionError preserves a stable phase while wrapping the public Gess error.
type ExecutionError struct {
	Stage ExecutionStage
	Err   error
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return "scenario execution failed"
	}
	return fmt.Sprintf("scenario %s: %v", e.Stage, e.Err)
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SourceResolver resolves a confined portable path to source bytes.
type SourceResolver interface {
	ResolveScenarioSource(context.Context, string) ([]byte, error)
}

// SourceResolverFunc adapts a function to SourceResolver.
type SourceResolverFunc func(context.Context, string) ([]byte, error)

func (f SourceResolverFunc) ResolveScenarioSource(ctx context.Context, path string) ([]byte, error) {
	return f(ctx, path)
}

// RunnerConfig contains process-owned ceilings and one exact callback profile.
type RunnerConfig struct {
	InputLimits           InputLimits
	RunLimits             RunOptions
	ReportLimits          ReportLimits
	CallbackProfile       CallbackProfile
	Registry              dsl.Registry
	MaxDemandCascadeSteps int64
}

// Runner executes scenarios using only public Gess compile and session APIs.
type Runner struct {
	config RunnerConfig
}

// DisabledCallbackProfile returns the canonical profile for runners that do
// not expose host actions, calls, or pure functions.
func DisabledCallbackProfile() CallbackProfile {
	return CallbackProfile{
		Name:    "disabled",
		Version: "1",
		Digest:  sourceDigest([]byte("gess.callback-profile.disabled.v1")),
	}
}

// FactCapture is a bounded, ordered final working-memory capture.
type FactCapture struct {
	Items     []session.FactSnapshot
	Total     int64
	Truncated bool
}

// EventCapture is a bounded, ordered event capture.
type EventCapture struct {
	Items     []session.Event
	Total     int64
	Truncated bool
}

// OutputCapture is a bounded prefix of emitted output with an exact byte total.
type OutputCapture struct {
	Text       string
	LimitBytes int64
	TotalBytes int64
	Truncated  bool
}

// QueryExecution is one selected query and its bounded ordered rows.
type QueryExecution struct {
	Query     ScenarioQuery
	Rows      []session.QueryRow
	Total     int64
	Truncated bool
}

// ExecutionResult is the bounded semantic result of one scenario execution.
// It never exposes the mutable session and remains usable after Run returns.
type ExecutionResult struct {
	Outcome         ExecutionOutcome
	Stage           ExecutionStage
	ScenarioDigest  string
	Sources         []ResolvedSource
	RulesetID       rules.RulesetID
	CallbackProfile CallbackProfile
	Limits          AppliedLimits
	Run             session.RunResult
	Facts           FactCapture
	Firings         EventCapture
	Events          EventCapture
	Output          OutputCapture
	Queries         []QueryExecution
}

// NewRunner validates process-owned ceilings and clones registry collections.
func NewRunner(config RunnerConfig) (*Runner, error) {
	if err := validateInputLimits("inputLimits", config.InputLimits, invalidRunnerConfigf); err != nil {
		return nil, err
	}
	if err := validateRunCeilings(config.RunLimits); err != nil {
		return nil, err
	}
	if err := validateReportLimits("reportLimits", config.ReportLimits, invalidRunnerConfigf); err != nil {
		return nil, err
	}
	if err := validateCallbackProfile("callbackProfile", config.CallbackProfile, invalidRunnerConfigf); err != nil {
		return nil, err
	}
	if config.MaxDemandCascadeSteps <= 0 || config.MaxDemandCascadeSteps > maxJSONSafeInteger {
		return nil, invalidRunnerConfigf("maxDemandCascadeSteps must be a positive safe integer")
	}
	if config.RunLimits.DeadlineMS > int64((time.Duration(1<<63-1))/time.Millisecond) {
		return nil, invalidRunnerConfigf("runLimits.deadlineMs exceeds time.Duration")
	}
	if config.CallbackProfile == DisabledCallbackProfile() && (len(config.Registry.Actions) != 0 || len(config.Registry.Calls) != 0 || len(config.Registry.Functions) != 0) {
		return nil, invalidRunnerConfigf("disabled callback profile must use an empty registry")
	}
	config.Registry = cloneRegistry(config.Registry)
	return &Runner{config: config}, nil
}

// Run resolves, compiles, and executes document under the runner's ceilings.
// Any returned error is also classified by result.Outcome and result.Stage.
func (r *Runner) Run(ctx context.Context, document Scenario, resolver SourceResolver) (ExecutionResult, error) {
	result := ExecutionResult{Outcome: OutcomeValidationError, Stage: StageValidate}
	if r == nil {
		return result, executionError(StageValidate, ErrInvalidRunnerConfig)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateScenario(document); err != nil {
		return result, executionError(StageValidate, err)
	}
	result.CallbackProfile = document.CallbackProfile
	result.Limits = AppliedLimits{Input: r.config.InputLimits, Run: document.Run, Report: document.ReportLimits}
	digest, err := ScenarioDigest(document)
	if err != nil {
		return result, executionError(StageValidate, err)
	}
	result.ScenarioDigest = digest
	if err := r.validateScenarioBounds(document); err != nil {
		return result, executionError(StageValidate, err)
	}
	if document.CallbackProfile != r.config.CallbackProfile {
		return result, executionError(StageValidate, fmt.Errorf("%w: requested %s@%s", ErrCallbackProfile, document.CallbackProfile.Name, document.CallbackProfile.Version))
	}
	if resolver == nil {
		result.Stage = StageResolve
		return result, executionError(StageResolve, fmt.Errorf("%w: nil resolver", ErrSourceResolution))
	}

	runCtx, cancel := context.WithTimeout(ctx, durationMilliseconds(document.Run.DeadlineMS))
	defer cancel()
	documents, resolved, err := r.resolveAndParse(runCtx, document.Sources, resolver)
	result.Sources = resolved
	if err != nil {
		return r.classifyContextFailure(result, StageResolve, err)
	}

	workspace := session.NewWorkspace()
	for _, parsed := range documents {
		if err := dsl.Load(runCtx, workspace, parsed, r.config.Registry); err != nil {
			return r.classifyContextFailure(result, StageLoad, err)
		}
	}
	ruleset, err := rules.Compile(runCtx, workspace)
	if err != nil {
		return r.classifyContextFailure(result, StageCompile, err)
	}
	result.RulesetID = ruleset.ID()

	initials, err := selectedInitialFacts(document, documents, ruleset)
	if err != nil {
		result.Stage = StageSession
		return result, executionError(StageSession, err)
	}
	if int64(len(initials)) > r.config.InputLimits.MaxInitialFacts {
		result.Stage = StageSession
		return result, executionError(StageSession, fmt.Errorf("%w: initial facts %d exceeds %d", ErrScenarioLimit, len(initials), r.config.InputLimits.MaxInitialFacts))
	}
	globals := scenarioValues(document.Globals)
	output := newBoundedOutput(document.ReportLimits.MaxOutputBytes)
	events := newBoundedEvents(document.ReportLimits.MaxEvents, document.ReportLimits.MaxFirings)
	strategy := session.StrategyDepth
	if document.Run.Strategy == StrategyBreadth {
		strategy = session.StrategyBreadth
	}
	sess, err := session.New(
		ruleset,
		session.WithInitialFacts(initials...),
		session.WithGlobals(globals),
		session.WithStrategy(strategy),
		session.WithOutputWriter(output),
		session.WithEventListener(events),
		session.WithEventClock(func() time.Time { return time.Time{} }),
		session.WithMaxFacts(int(document.Run.MaxFacts)),
		session.WithMaxDemandCascadeSteps(int(r.config.MaxDemandCascadeSteps)),
	)
	if err != nil {
		return r.classifySessionFailure(result, StageSession, err)
	}
	defer sess.Close()

	run, runErr := sess.Run(runCtx, session.WithMaxFirings(int(document.Run.MaxFirings)))
	result.Run = run
	if runErr != nil {
		result = r.captureResult(result, sess, output, events)
		return r.classifySessionFailure(result, StageRun, runErr)
	}
	result.Outcome = outcomeForRunStatus(run.Status)
	result.Stage = StageRun
	if result.Outcome == OutcomeRuntimeError {
		result = r.captureResult(result, sess, output, events)
		return result, executionError(StageRun, fmt.Errorf("unexpected run status %q", run.Status))
	}

	queries, err := executeQueries(runCtx, sess, document.Queries)
	if err != nil {
		result = r.captureResult(result, sess, output, events)
		return r.classifyContextFailure(result, StageQuery, err)
	}
	result.Queries = queries
	result = r.captureResult(result, sess, output, events)
	return result, nil
}

func (r *Runner) validateScenarioBounds(document Scenario) error {
	encoded, err := MarshalScenario(document)
	if err != nil {
		return err
	}
	checks := []struct {
		name string
		got  int64
		max  int64
	}{
		{"request bytes", int64(len(encoded)), r.config.InputLimits.MaxRequestBytes},
		{"source files", int64(len(document.Sources)), r.config.InputLimits.MaxSourceFiles},
		{"initial facts", int64(len(document.InitialFacts)), r.config.InputLimits.MaxInitialFacts},
		{"run max facts", document.Run.MaxFacts, r.config.RunLimits.MaxFacts},
		{"run max firings", document.Run.MaxFirings, r.config.RunLimits.MaxFirings},
		{"run deadline", document.Run.DeadlineMS, r.config.RunLimits.DeadlineMS},
		{"report max facts", document.ReportLimits.MaxFacts, r.config.ReportLimits.MaxFacts},
		{"report max firings", document.ReportLimits.MaxFirings, r.config.ReportLimits.MaxFirings},
		{"report max events", document.ReportLimits.MaxEvents, r.config.ReportLimits.MaxEvents},
		{"report max query rows", document.ReportLimits.MaxQueryRows, r.config.ReportLimits.MaxQueryRows},
		{"report max diagnostics", document.ReportLimits.MaxDiagnostics, r.config.ReportLimits.MaxDiagnostics},
		{"report max counters", document.ReportLimits.MaxCounters, r.config.ReportLimits.MaxCounters},
		{"report max checks", document.ReportLimits.MaxChecks, r.config.ReportLimits.MaxChecks},
		{"report max explanation refs", document.ReportLimits.MaxExplanationRefs, r.config.ReportLimits.MaxExplanationRefs},
		{"report max output bytes", document.ReportLimits.MaxOutputBytes, r.config.ReportLimits.MaxOutputBytes},
		{"report max report bytes", document.ReportLimits.MaxReportBytes, r.config.ReportLimits.MaxReportBytes},
	}
	for _, check := range checks {
		if check.got > check.max {
			return fmt.Errorf("%w: %s %d exceeds %d", ErrScenarioLimit, check.name, check.got, check.max)
		}
	}
	return nil
}

func (r *Runner) resolveAndParse(ctx context.Context, sources []ScenarioSource, resolver SourceResolver) ([]*dsl.Document, []ResolvedSource, error) {
	documents := make([]*dsl.Document, 0, len(sources))
	resolved := make([]ResolvedSource, 0, len(sources))
	for _, source := range sources {
		content, err := resolver.ResolveScenarioSource(ctx, source.Path)
		if err != nil {
			return nil, resolved, fmt.Errorf("%w for %q: %w", ErrSourceResolution, source.Path, err)
		}
		if int64(len(content)) > r.config.InputLimits.MaxSourceFileBytes {
			return nil, resolved, fmt.Errorf("%w: source %q has %d bytes, limit %d", ErrScenarioLimit, source.Path, len(content), r.config.InputLimits.MaxSourceFileBytes)
		}
		digest := sourceDigest(content)
		if source.Digest != "" && source.Digest != digest {
			return nil, resolved, fmt.Errorf("%w for %q: digest mismatch", ErrSourceResolution, source.Path)
		}
		resolved = append(resolved, ResolvedSource{Path: source.Path, Digest: digest})
		document, err := dsl.Parse(source.Path, content)
		if err != nil {
			return nil, resolved, &ExecutionError{Stage: StageParse, Err: err}
		}
		documents = append(documents, document)
	}
	return documents, resolved, nil
}

func selectedInitialFacts(document Scenario, documents []*dsl.Document, ruleset *rules.Ruleset) ([]session.InitialFact, error) {
	declarations := make(map[string][][]session.InitialFact)
	for _, parsed := range documents {
		for _, declaration := range parsed.Deffacts() {
			declarations[declaration.Name] = append(declarations[declaration.Name], declaration.Facts)
		}
	}
	initials := make([]session.InitialFact, 0, len(document.InitialFacts))
	for _, name := range document.Deffacts {
		matches := declarations[name]
		if len(matches) != 1 {
			return nil, fmt.Errorf("%w: %q matched %d declarations", ErrDeffactsSelection, name, len(matches))
		}
		initials = append(initials, matches[0]...)
	}
	for _, fact := range document.InitialFacts {
		template, ok := ruleset.TemplateByKey(rules.TemplateKey(fact.Template))
		if !ok {
			template, ok = ruleset.Template(fact.Template)
		}
		if !ok {
			return nil, fmt.Errorf("%w: initial fact template %q is not declared", ErrInvalidScenario, fact.Template)
		}
		initials = append(initials, session.InitialFact{TemplateKey: template.Key(), Fields: scenarioFields(fact.Fields)})
	}
	return initials, nil
}

func executeQueries(ctx context.Context, sess *session.Session, queries []ScenarioQuery) ([]QueryExecution, error) {
	result := make([]QueryExecution, 0, len(queries))
	for _, query := range queries {
		iterator, err := sess.Query(ctx, query.Name, session.QueryArgs(scenarioValues(query.Args)))
		if err != nil {
			return result, fmt.Errorf("query %q: %w", query.Name, err)
		}
		execution := QueryExecution{Query: cloneScenarioQuery(query), Rows: make([]session.QueryRow, 0, boundedCapacity(query.MaxRows))}
		for {
			row, ok, err := iterator.Next(ctx)
			if err != nil {
				return result, fmt.Errorf("query %q: %w", query.Name, err)
			}
			if !ok {
				break
			}
			execution.Total++
			if int64(len(execution.Rows)) < query.MaxRows {
				execution.Rows = append(execution.Rows, row)
			}
		}
		execution.Truncated = execution.Total > int64(len(execution.Rows))
		result = append(result, execution)
	}
	return result, nil
}

func (r *Runner) captureResult(result ExecutionResult, sess *session.Session, output *boundedOutput, events *boundedEvents) ExecutionResult {
	if snapshot, err := sess.Snapshot(context.Background()); err == nil {
		facts := snapshot.Facts()
		limit := int(result.Limits.Report.MaxFacts)
		if len(facts) > limit {
			result.Facts.Items = append([]session.FactSnapshot(nil), facts[:limit]...)
		} else {
			result.Facts.Items = facts
		}
		result.Facts.Total = int64(len(facts))
		result.Facts.Truncated = len(facts) > len(result.Facts.Items)
	}
	result.Output = output.capture()
	result.Events, result.Firings = events.captures()
	return result
}

func (r *Runner) classifyContextFailure(result ExecutionResult, stage ExecutionStage, err error) (ExecutionResult, error) {
	var executionErr *ExecutionError
	if errors.As(err, &executionErr) {
		stage = executionErr.Stage
		err = executionErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.Outcome = OutcomeDeadline
	} else if errors.Is(err, context.Canceled) {
		result.Outcome = OutcomeCanceled
	} else if stage == StageQuery {
		result.Outcome = OutcomeRuntimeError
	} else {
		result.Outcome = OutcomeValidationError
	}
	result.Stage = stage
	return result, executionError(stage, err)
}

func (r *Runner) classifySessionFailure(result ExecutionResult, stage ExecutionStage, err error) (ExecutionResult, error) {
	result.Stage = stage
	switch {
	case errors.Is(err, rules.ErrFactLimit):
		result.Outcome = OutcomeMaxFacts
	case errors.Is(err, context.DeadlineExceeded):
		result.Outcome = OutcomeDeadline
	case errors.Is(err, context.Canceled):
		result.Outcome = OutcomeCanceled
	case stage == StageSession:
		result.Outcome = OutcomeValidationError
	default:
		result.Outcome = OutcomeRuntimeError
	}
	return result, executionError(stage, err)
}

func outcomeForRunStatus(status session.RunStatus) ExecutionOutcome {
	switch status {
	case session.RunCompleted:
		return OutcomeCompleted
	case session.RunHalted:
		return OutcomeHalted
	case session.RunFireLimit:
		return OutcomeMaxFirings
	case session.RunCanceled:
		return OutcomeCanceled
	default:
		return OutcomeRuntimeError
	}
}

func executionError(stage ExecutionStage, err error) error {
	return &ExecutionError{Stage: stage, Err: err}
}

func invalidRunnerConfigf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRunnerConfig, fmt.Sprintf(format, args...))
}

func validateRunCeilings(limits RunOptions) error {
	values := []struct {
		name  string
		value int64
	}{{"maxFacts", limits.MaxFacts}, {"maxFirings", limits.MaxFirings}, {"deadlineMs", limits.DeadlineMS}}
	for _, value := range values {
		if value.value <= 0 || value.value > maxJSONSafeInteger {
			return invalidRunnerConfigf("runLimits.%s must be a positive safe integer", value.name)
		}
	}
	return nil
}

func cloneRegistry(registry dsl.Registry) dsl.Registry {
	out := dsl.Registry{Functions: make([]rules.PureFunctionSpec, len(registry.Functions))}
	for i, function := range registry.Functions {
		out.Functions[i] = rules.ClonePureFunctionSpec(function)
	}
	if len(registry.Actions) > 0 {
		out.Actions = make(map[string]rules.ActionFunc, len(registry.Actions))
		maps.Copy(out.Actions, registry.Actions)
	}
	if len(registry.Calls) > 0 {
		out.Calls = make(map[string]dsl.CallFunc, len(registry.Calls))
		maps.Copy(out.Calls, registry.Calls)
	}
	return out
}

func scenarioFields(values map[string]Value) rules.Fields {
	out := make(rules.Fields, len(values))
	for name, value := range values {
		out[name] = value.RulesValue()
	}
	return out
}

func scenarioValues(values map[string]Value) map[string]any {
	out := make(map[string]any, len(values))
	for name, value := range values {
		out[name] = value.RulesValue()
	}
	return out
}

func cloneScenarioQuery(query ScenarioQuery) ScenarioQuery {
	out := query
	out.Args = make(map[string]Value, len(query.Args))
	for name, value := range query.Args {
		out.Args[name] = NewValue(value.RulesValue())
	}
	return out
}

func sourceDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func durationMilliseconds(value int64) time.Duration {
	return time.Duration(value) * time.Millisecond
}

type boundedOutput struct {
	limit int64
	total int64
	data  []byte
}

func newBoundedOutput(limit int64) *boundedOutput {
	return &boundedOutput{limit: limit, data: make([]byte, 0, boundedCapacity(limit))}
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	w.total += int64(len(data))
	remaining := w.limit - int64(len(w.data))
	if remaining > 0 {
		w.data = append(w.data, data[:min(int64(len(data)), remaining)]...)
	}
	return len(data), nil
}

func (w *boundedOutput) capture() OutputCapture {
	data := w.data
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return OutputCapture{
		Text:       string(data),
		LimitBytes: w.limit,
		TotalBytes: w.total,
		Truncated:  w.total > int64(len(data)),
	}
}

var _ io.Writer = (*boundedOutput)(nil)

type boundedEvents struct {
	mu          sync.Mutex
	eventLimit  int64
	firingLimit int64
	eventTotal  int64
	firingTotal int64
	events      []session.Event
	firings     []session.Event
}

func newBoundedEvents(eventLimit, firingLimit int64) *boundedEvents {
	return &boundedEvents{
		eventLimit:  eventLimit,
		firingLimit: firingLimit,
		events:      make([]session.Event, 0, boundedCapacity(eventLimit)),
		firings:     make([]session.Event, 0, boundedCapacity(firingLimit)),
	}
}

func (c *boundedEvents) HandleEvent(_ context.Context, event session.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eventTotal++
	if int64(len(c.events)) < c.eventLimit {
		c.events = append(c.events, event)
	}
	if event.Type == session.EventRuleFired {
		c.firingTotal++
		if int64(len(c.firings)) < c.firingLimit {
			c.firings = append(c.firings, event)
		}
	}
	return nil
}

func (c *boundedEvents) captures() (EventCapture, EventCapture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := EventCapture{
		Items:     append([]session.Event(nil), c.events...),
		Total:     c.eventTotal,
		Truncated: c.eventTotal > int64(len(c.events)),
	}
	firings := EventCapture{
		Items:     append([]session.Event(nil), c.firings...),
		Total:     c.firingTotal,
		Truncated: c.firingTotal > int64(len(c.firings)),
	}
	return events, firings
}

var _ session.EventListener = (*boundedEvents)(nil)

func boundedCapacity(limit int64) int {
	return int(min(limit, 1024))
}
