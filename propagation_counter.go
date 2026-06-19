package gess

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type propagationOrigin uint8

const (
	propagationOriginExternal propagationOrigin = iota
	propagationOriginRHS
)

func (o propagationOrigin) String() string {
	switch o {
	case propagationOriginExternal:
		return "external"
	case propagationOriginRHS:
		return "rhs"
	default:
		return "unknown"
	}
}

func propagationOriginFromMutation(origin mutationOrigin) propagationOrigin {
	if origin.isZero() {
		return propagationOriginExternal
	}
	return propagationOriginRHS
}

type propagationCounterTotals struct {
	Asserts                  int
	RHSAsserts               int
	RuleMemoriesVisited      int
	ConditionsTested         int
	AlphaMatchesAdded        int
	ConditionPlansTested     int
	ConditionMatchesAdded    int
	PrefixesAdded            int
	BetaSuccessorsReached    int
	TokensCreated            int
	TerminalDeltasEmitted    int
	AgendaDeltaApplications  int
	AgendaSorts              int
	ActivationsStored        int
	RemovalIndexLookups      int
	RemovalRowsTouched       int
	RemovalRowsRemoved       int
	TerminalDeltasRemoved    int
	BetaLeftInputInserts     int
	BetaRightInputInserts    int
	BetaBucketProbes         int
	BetaCandidateRowsScanned int
	BetaResidualTests        int
	BetaResidualFailures     int
	BetaJoinedTokensProduced int
}

func (t *propagationCounterTotals) add(other propagationCounterTotals) {
	if t == nil {
		return
	}
	t.Asserts += other.Asserts
	t.RHSAsserts += other.RHSAsserts
	t.RuleMemoriesVisited += other.RuleMemoriesVisited
	t.ConditionsTested += other.ConditionsTested
	t.AlphaMatchesAdded += other.AlphaMatchesAdded
	t.ConditionPlansTested += other.ConditionPlansTested
	t.ConditionMatchesAdded += other.ConditionMatchesAdded
	t.PrefixesAdded += other.PrefixesAdded
	t.BetaSuccessorsReached += other.BetaSuccessorsReached
	t.TokensCreated += other.TokensCreated
	t.TerminalDeltasEmitted += other.TerminalDeltasEmitted
	t.AgendaDeltaApplications += other.AgendaDeltaApplications
	t.AgendaSorts += other.AgendaSorts
	t.ActivationsStored += other.ActivationsStored
	t.RemovalIndexLookups += other.RemovalIndexLookups
	t.RemovalRowsTouched += other.RemovalRowsTouched
	t.RemovalRowsRemoved += other.RemovalRowsRemoved
	t.TerminalDeltasRemoved += other.TerminalDeltasRemoved
	t.BetaLeftInputInserts += other.BetaLeftInputInserts
	t.BetaRightInputInserts += other.BetaRightInputInserts
	t.BetaBucketProbes += other.BetaBucketProbes
	t.BetaCandidateRowsScanned += other.BetaCandidateRowsScanned
	t.BetaResidualTests += other.BetaResidualTests
	t.BetaResidualFailures += other.BetaResidualFailures
	t.BetaJoinedTokensProduced += other.BetaJoinedTokensProduced
}

type propagationCounterKey struct {
	templateKey TemplateKey
	origin      propagationOrigin
}

type propagationRuntimePath string

const (
	propagationRuntimeUnknown       propagationRuntimePath = "unknown"
	propagationRuntimeGraphBeta     propagationRuntimePath = "graph-beta"
	propagationRuntimeLegacyBeta    propagationRuntimePath = "legacy-beta"
	propagationRuntimeGraphAlpha    propagationRuntimePath = "graph-alpha-only"
	propagationRuntimeSemanticMatch propagationRuntimePath = "semantic-matcher"
)

const (
	propagationFallbackNoGraph         = "no-graph"
	propagationFallbackBetaUnsupported = "beta-unsupported"
	propagationFallbackNonEqualityJoin = "non-equality-join"
)

type propagationCounterLedger struct {
	totals           propagationCounterTotals
	byTemplate       map[TemplateKey]*propagationCounterTotals
	byOrigin         map[propagationOrigin]*propagationCounterTotals
	byTemplateOrigin map[propagationCounterKey]*propagationCounterTotals
	runtimePath      propagationRuntimePath
	fallbackReasons  map[string]int
}

type propagationCounterSpan struct {
	ledger      *propagationCounterLedger
	templateKey TemplateKey
	origin      propagationOrigin
	totals      propagationCounterTotals
}

type propagationCounterSnapshot struct {
	Totals           propagationCounterTotals
	ByTemplate       map[TemplateKey]propagationCounterTotals
	ByOrigin         map[propagationOrigin]propagationCounterTotals
	ByTemplateOrigin map[propagationCounterKey]propagationCounterTotals
	RuntimePath      propagationRuntimePath
	FallbackReasons  map[string]int
}

func newPropagationCounterLedger() *propagationCounterLedger {
	return &propagationCounterLedger{
		byTemplate:       make(map[TemplateKey]*propagationCounterTotals),
		byOrigin:         make(map[propagationOrigin]*propagationCounterTotals),
		byTemplateOrigin: make(map[propagationCounterKey]*propagationCounterTotals),
		runtimePath:      propagationRuntimeUnknown,
	}
}

func (l *propagationCounterLedger) beginAssert(templateKey TemplateKey, origin mutationOrigin) propagationCounterSpan {
	if l == nil {
		return propagationCounterSpan{}
	}
	return propagationCounterSpan{
		ledger:      l,
		templateKey: templateKey,
		origin:      propagationOriginFromMutation(origin),
	}
}

func (l *propagationCounterLedger) snapshot() propagationCounterSnapshot {
	if l == nil {
		return propagationCounterSnapshot{}
	}
	out := propagationCounterSnapshot{
		Totals:           l.totals,
		ByTemplate:       make(map[TemplateKey]propagationCounterTotals, len(l.byTemplate)),
		ByOrigin:         make(map[propagationOrigin]propagationCounterTotals, len(l.byOrigin)),
		ByTemplateOrigin: make(map[propagationCounterKey]propagationCounterTotals, len(l.byTemplateOrigin)),
		RuntimePath:      l.runtimePath,
	}
	if len(l.fallbackReasons) > 0 {
		out.FallbackReasons = make(map[string]int, len(l.fallbackReasons))
		maps.Copy(out.FallbackReasons, l.fallbackReasons)
	}
	for key, totals := range l.byTemplate {
		if totals != nil {
			out.ByTemplate[key] = *totals
		}
	}
	for key, totals := range l.byOrigin {
		if totals != nil {
			out.ByOrigin[key] = *totals
		}
	}
	for key, totals := range l.byTemplateOrigin {
		if totals != nil {
			out.ByTemplateOrigin[key] = *totals
		}
	}
	return out
}

func (s *propagationCounterSpan) recordRuleMemoryVisited() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.RuleMemoriesVisited++
}

func (s *propagationCounterSpan) recordConditionsTested() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.ConditionsTested++
}

func (s *propagationCounterSpan) recordAlphaMatchAdded() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.AlphaMatchesAdded++
}

func (s *propagationCounterSpan) recordConditionPlanTested() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.ConditionPlansTested++
}

func (s *propagationCounterSpan) recordConditionMatchAdded() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.ConditionMatchesAdded++
}

func (s *propagationCounterSpan) recordPrefixAdded() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.PrefixesAdded++
}

func (s *propagationCounterSpan) recordBetaSuccessorReached() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.BetaSuccessorsReached++
}

func (s *propagationCounterSpan) recordTokenCreated() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.TokensCreated++
}

func (s *propagationCounterSpan) recordBetaInputInsert(side reteGraphBetaInputSide) {
	if s == nil || s.ledger == nil {
		return
	}
	switch side {
	case reteGraphBetaInputLeft:
		s.totals.BetaLeftInputInserts++
	case reteGraphBetaInputRight:
		s.totals.BetaRightInputInserts++
	}
}

func (s *propagationCounterSpan) recordBetaBucketProbe() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.BetaBucketProbes++
}

func (s *propagationCounterSpan) recordBetaCandidateRowScanned() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.BetaCandidateRowsScanned++
}

func (s *propagationCounterSpan) recordBetaResidualTest() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.BetaResidualTests++
}

func (s *propagationCounterSpan) recordBetaResidualFailure() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.BetaResidualFailures++
}

func (s *propagationCounterSpan) recordBetaJoinedTokenProduced() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.BetaJoinedTokensProduced++
}

func (s *propagationCounterSpan) recordTerminalDeltaEmitted() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.TerminalDeltasEmitted++
}

func (s *propagationCounterSpan) finish() {
	if s == nil || s.ledger == nil {
		return
	}
	ledger := s.ledger
	s.totals.Asserts++
	if s.origin == propagationOriginRHS {
		s.totals.RHSAsserts++
	}
	ledger.totals.add(s.totals)
	ledger.addTemplateTotals(s.templateKey, s.totals)
	ledger.addOriginTotals(s.origin, s.totals)
	ledger.addTemplateOriginTotals(s.templateKey, s.origin, s.totals)
	s.ledger = nil
}

func (l *propagationCounterLedger) recordAgendaDeltaApplication() {
	if l == nil {
		return
	}
	l.totals.AgendaDeltaApplications++
}

func (l *propagationCounterLedger) recordAgendaSort() {
	if l == nil {
		return
	}
	l.totals.AgendaSorts++
}

func (l *propagationCounterLedger) recordActivationStored() {
	if l == nil {
		return
	}
	l.totals.ActivationsStored++
}

func (l *propagationCounterLedger) recordRemovalIndexLookup() {
	if l == nil {
		return
	}
	l.totals.RemovalIndexLookups++
}

func (l *propagationCounterLedger) recordRemovalRowTouched() {
	if l == nil {
		return
	}
	l.totals.RemovalRowsTouched++
}

func (l *propagationCounterLedger) recordRemovalRowRemoved() {
	if l == nil {
		return
	}
	l.totals.RemovalRowsRemoved++
}

func (l *propagationCounterLedger) recordTerminalDeltaRemoved() {
	if l == nil {
		return
	}
	l.totals.TerminalDeltasRemoved++
}

func (l *propagationCounterLedger) setRuntimeDiagnostics(path propagationRuntimePath, fallbackReasons map[string]int) {
	if l == nil {
		return
	}
	if path == "" {
		path = propagationRuntimeUnknown
	}
	l.runtimePath = path
	if len(fallbackReasons) == 0 {
		clear(l.fallbackReasons)
		return
	}
	if l.fallbackReasons == nil {
		l.fallbackReasons = make(map[string]int, len(fallbackReasons))
	} else {
		clear(l.fallbackReasons)
	}
	for reason, count := range fallbackReasons {
		if count > 0 {
			l.fallbackReasons[reason] = count
		}
	}
}

func (l *propagationCounterLedger) addTemplateTotals(templateKey TemplateKey, totals propagationCounterTotals) {
	if l == nil {
		return
	}
	current := l.byTemplate[templateKey]
	if current == nil {
		current = &propagationCounterTotals{}
		l.byTemplate[templateKey] = current
	}
	current.add(totals)
}

func (l *propagationCounterLedger) addOriginTotals(origin propagationOrigin, totals propagationCounterTotals) {
	if l == nil {
		return
	}
	current := l.byOrigin[origin]
	if current == nil {
		current = &propagationCounterTotals{}
		l.byOrigin[origin] = current
	}
	current.add(totals)
}

func (l *propagationCounterLedger) addTemplateOriginTotals(templateKey TemplateKey, origin propagationOrigin, totals propagationCounterTotals) {
	if l == nil {
		return
	}
	key := propagationCounterKey{templateKey: templateKey, origin: origin}
	current := l.byTemplateOrigin[key]
	if current == nil {
		current = &propagationCounterTotals{}
		l.byTemplateOrigin[key] = current
	}
	current.add(totals)
}

func (s propagationCounterSnapshot) reportMetrics(report func(name string, value float64)) {
	if report == nil {
		return
	}
	report("propagation-asserts", float64(s.Totals.Asserts))
	report("propagation-rhs-asserts", float64(s.Totals.RHSAsserts))
	report("propagation-rule-memories-visited", float64(s.Totals.RuleMemoriesVisited))
	report("propagation-conditions-tested", float64(s.Totals.ConditionsTested))
	report("propagation-alpha-matches-added", float64(s.Totals.AlphaMatchesAdded))
	report("propagation-condition-plans-tested", float64(s.Totals.ConditionPlansTested))
	report("propagation-condition-matches-added", float64(s.Totals.ConditionMatchesAdded))
	report("propagation-prefixes-added", float64(s.Totals.PrefixesAdded))
	report("propagation-beta-successors-reached", float64(s.Totals.BetaSuccessorsReached))
	report("propagation-tokens-created", float64(s.Totals.TokensCreated))
	report("propagation-terminal-deltas-emitted", float64(s.Totals.TerminalDeltasEmitted))
	report("propagation-agenda-delta-applications", float64(s.Totals.AgendaDeltaApplications))
	report("propagation-agenda-sorts", float64(s.Totals.AgendaSorts))
	report("propagation-activations-stored", float64(s.Totals.ActivationsStored))
	report("propagation-removal-index-lookups", float64(s.Totals.RemovalIndexLookups))
	report("propagation-removal-rows-touched", float64(s.Totals.RemovalRowsTouched))
	report("propagation-removal-rows-removed", float64(s.Totals.RemovalRowsRemoved))
	report("propagation-terminal-deltas-removed", float64(s.Totals.TerminalDeltasRemoved))
	report("propagation-beta-left-input-inserts", float64(s.Totals.BetaLeftInputInserts))
	report("propagation-beta-right-input-inserts", float64(s.Totals.BetaRightInputInserts))
	report("propagation-beta-bucket-probes", float64(s.Totals.BetaBucketProbes))
	report("propagation-beta-candidate-rows-scanned", float64(s.Totals.BetaCandidateRowsScanned))
	report("propagation-beta-residual-tests", float64(s.Totals.BetaResidualTests))
	report("propagation-beta-residual-failures", float64(s.Totals.BetaResidualFailures))
	report("propagation-beta-joined-tokens-produced", float64(s.Totals.BetaJoinedTokensProduced))

	rhsAsserts := float64(max(1, s.Totals.RHSAsserts))
	report("propagation-rule-memories-visited/rhs-assert", float64(s.Totals.RuleMemoriesVisited)/rhsAsserts)
	report("propagation-conditions-tested/rhs-assert", float64(s.Totals.ConditionsTested)/rhsAsserts)
	report("propagation-alpha-matches-added/rhs-assert", float64(s.Totals.AlphaMatchesAdded)/rhsAsserts)
	report("propagation-condition-plans-tested/rhs-assert", float64(s.Totals.ConditionPlansTested)/rhsAsserts)
	report("propagation-condition-matches-added/rhs-assert", float64(s.Totals.ConditionMatchesAdded)/rhsAsserts)
	report("propagation-prefixes-added/rhs-assert", float64(s.Totals.PrefixesAdded)/rhsAsserts)
	report("propagation-beta-successors-reached/rhs-assert", float64(s.Totals.BetaSuccessorsReached)/rhsAsserts)
	report("propagation-tokens-created/rhs-assert", float64(s.Totals.TokensCreated)/rhsAsserts)
	report("propagation-terminal-deltas-emitted/rhs-assert", float64(s.Totals.TerminalDeltasEmitted)/rhsAsserts)
	report("propagation-agenda-delta-applications/rhs-assert", float64(s.Totals.AgendaDeltaApplications)/rhsAsserts)
	report("propagation-agenda-sorts/rhs-assert", float64(s.Totals.AgendaSorts)/rhsAsserts)
	report("propagation-activations-stored/rhs-assert", float64(s.Totals.ActivationsStored)/rhsAsserts)
	report("propagation-beta-left-input-inserts/rhs-assert", float64(s.Totals.BetaLeftInputInserts)/rhsAsserts)
	report("propagation-beta-right-input-inserts/rhs-assert", float64(s.Totals.BetaRightInputInserts)/rhsAsserts)
	report("propagation-beta-bucket-probes/rhs-assert", float64(s.Totals.BetaBucketProbes)/rhsAsserts)
	report("propagation-beta-candidate-rows-scanned/rhs-assert", float64(s.Totals.BetaCandidateRowsScanned)/rhsAsserts)
	report("propagation-beta-residual-tests/rhs-assert", float64(s.Totals.BetaResidualTests)/rhsAsserts)
	report("propagation-beta-residual-failures/rhs-assert", float64(s.Totals.BetaResidualFailures)/rhsAsserts)
	report("propagation-beta-joined-tokens-produced/rhs-assert", float64(s.Totals.BetaJoinedTokensProduced)/rhsAsserts)
	report("propagation-template-count", float64(len(s.ByTemplate)))
	report("propagation-origin-count", float64(len(s.ByOrigin)))
	report("propagation-template-origin-count", float64(len(s.ByTemplateOrigin)))
	report("propagation-runtime-graph-beta", propagationRuntimePathMetric(s.RuntimePath, propagationRuntimeGraphBeta))
	report("propagation-runtime-legacy-beta", propagationRuntimePathMetric(s.RuntimePath, propagationRuntimeLegacyBeta))
	report("propagation-runtime-graph-alpha-only", propagationRuntimePathMetric(s.RuntimePath, propagationRuntimeGraphAlpha))
	report("propagation-runtime-semantic-matcher", propagationRuntimePathMetric(s.RuntimePath, propagationRuntimeSemanticMatch))
	report("propagation-fallback-reason-count", float64(len(s.FallbackReasons)))
}

func (s propagationCounterSnapshot) runnerFields() []string {
	if s.Totals.Asserts == 0 && s.Totals.RHSAsserts == 0 && len(s.ByTemplate) == 0 && len(s.ByOrigin) == 0 && s.RuntimePath == "" {
		return nil
	}
	fields := []string{
		"propagation-runtime-path=" + string(s.runtimePath()),
		"propagation-fallback-reasons=" + s.fallbackReasonSummary(),
		"propagation-asserts=" + strconv.Itoa(s.Totals.Asserts),
		"propagation-rhs-asserts=" + strconv.Itoa(s.Totals.RHSAsserts),
		"propagation-rule-memories-visited=" + strconv.Itoa(s.Totals.RuleMemoriesVisited),
		"propagation-conditions-tested=" + strconv.Itoa(s.Totals.ConditionsTested),
		"propagation-alpha-matches-added=" + strconv.Itoa(s.Totals.AlphaMatchesAdded),
		"propagation-condition-plans-tested=" + strconv.Itoa(s.Totals.ConditionPlansTested),
		"propagation-condition-matches-added=" + strconv.Itoa(s.Totals.ConditionMatchesAdded),
		"propagation-prefixes-added=" + strconv.Itoa(s.Totals.PrefixesAdded),
		"propagation-beta-successors-reached=" + strconv.Itoa(s.Totals.BetaSuccessorsReached),
		"propagation-tokens-created=" + strconv.Itoa(s.Totals.TokensCreated),
		"propagation-terminal-deltas-emitted=" + strconv.Itoa(s.Totals.TerminalDeltasEmitted),
		"propagation-agenda-delta-applications=" + strconv.Itoa(s.Totals.AgendaDeltaApplications),
		"propagation-agenda-sorts=" + strconv.Itoa(s.Totals.AgendaSorts),
		"propagation-activations-stored=" + strconv.Itoa(s.Totals.ActivationsStored),
		"propagation-removal-index-lookups=" + strconv.Itoa(s.Totals.RemovalIndexLookups),
		"propagation-removal-rows-touched=" + strconv.Itoa(s.Totals.RemovalRowsTouched),
		"propagation-removal-rows-removed=" + strconv.Itoa(s.Totals.RemovalRowsRemoved),
		"propagation-terminal-deltas-removed=" + strconv.Itoa(s.Totals.TerminalDeltasRemoved),
		"propagation-beta-left-input-inserts=" + strconv.Itoa(s.Totals.BetaLeftInputInserts),
		"propagation-beta-right-input-inserts=" + strconv.Itoa(s.Totals.BetaRightInputInserts),
		"propagation-beta-bucket-probes=" + strconv.Itoa(s.Totals.BetaBucketProbes),
		"propagation-beta-candidate-rows-scanned=" + strconv.Itoa(s.Totals.BetaCandidateRowsScanned),
		"propagation-beta-residual-tests=" + strconv.Itoa(s.Totals.BetaResidualTests),
		"propagation-beta-residual-failures=" + strconv.Itoa(s.Totals.BetaResidualFailures),
		"propagation-beta-joined-tokens-produced=" + strconv.Itoa(s.Totals.BetaJoinedTokensProduced),
		"propagation-rule-memories-visited/rhs-assert=" + s.perRHSAssertField(s.Totals.RuleMemoriesVisited),
		"propagation-conditions-tested/rhs-assert=" + s.perRHSAssertField(s.Totals.ConditionsTested),
		"propagation-alpha-matches-added/rhs-assert=" + s.perRHSAssertField(s.Totals.AlphaMatchesAdded),
		"propagation-condition-plans-tested/rhs-assert=" + s.perRHSAssertField(s.Totals.ConditionPlansTested),
		"propagation-condition-matches-added/rhs-assert=" + s.perRHSAssertField(s.Totals.ConditionMatchesAdded),
		"propagation-prefixes-added/rhs-assert=" + s.perRHSAssertField(s.Totals.PrefixesAdded),
		"propagation-beta-successors-reached/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaSuccessorsReached),
		"propagation-tokens-created/rhs-assert=" + s.perRHSAssertField(s.Totals.TokensCreated),
		"propagation-terminal-deltas-emitted/rhs-assert=" + s.perRHSAssertField(s.Totals.TerminalDeltasEmitted),
		"propagation-beta-left-input-inserts/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaLeftInputInserts),
		"propagation-beta-right-input-inserts/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaRightInputInserts),
		"propagation-beta-bucket-probes/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaBucketProbes),
		"propagation-beta-candidate-rows-scanned/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaCandidateRowsScanned),
		"propagation-beta-residual-tests/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaResidualTests),
		"propagation-beta-residual-failures/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaResidualFailures),
		"propagation-beta-joined-tokens-produced/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaJoinedTokensProduced),
		"propagation-by-template=" + s.templateSummary(),
		"propagation-by-origin=" + s.originSummary(),
	}
	if summary := s.templateOriginSummary(); summary != "" {
		fields = append(fields, "propagation-by-template-origin="+summary)
	}
	return fields
}

func propagationRuntimePathMetric(got, want propagationRuntimePath) float64 {
	if got == want {
		return 1
	}
	return 0
}

func (s propagationCounterSnapshot) runtimePath() propagationRuntimePath {
	if s.RuntimePath == "" {
		return propagationRuntimeUnknown
	}
	return s.RuntimePath
}

func (s propagationCounterSnapshot) fallbackReasonSummary() string {
	if len(s.FallbackReasons) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(s.FallbackReasons))
	for key := range s.FallbackReasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strconv.Itoa(s.FallbackReasons[key]))
	}
	return strings.Join(parts, ";")
}

func (s propagationCounterSnapshot) perRHSAssertField(value int) string {
	rhsAsserts := max(1, s.Totals.RHSAsserts)
	return strconv.FormatFloat(float64(value)/float64(rhsAsserts), 'f', 3, 64)
}

func (s propagationCounterSnapshot) templateSummary() string {
	if len(s.ByTemplate) == 0 {
		return "-"
	}
	keys := make([]TemplateKey, 0, len(s.ByTemplate))
	for key := range s.ByTemplate {
		keys = append(keys, key)
	}
	slicesSortTemplateKeys(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		totals := s.ByTemplate[key]
		parts = append(parts, formatPropagationDistributionEntry(key.String(), totals))
	}
	return strings.Join(parts, ";")
}

func (s propagationCounterSnapshot) originSummary() string {
	if len(s.ByOrigin) == 0 {
		return "-"
	}
	keys := make([]propagationOrigin, 0, len(s.ByOrigin))
	for key := range s.ByOrigin {
		keys = append(keys, key)
	}
	slicesSortOrigins(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		totals := s.ByOrigin[key]
		parts = append(parts, formatPropagationDistributionEntry(key.String(), totals))
	}
	return strings.Join(parts, ";")
}

func (s propagationCounterSnapshot) templateOriginSummary() string {
	if len(s.ByTemplateOrigin) == 0 {
		return ""
	}
	keys := make([]propagationCounterKey, 0, len(s.ByTemplateOrigin))
	for key := range s.ByTemplateOrigin {
		keys = append(keys, key)
	}
	slicesSortTemplateOriginKeys(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		totals := s.ByTemplateOrigin[key]
		parts = append(parts, formatPropagationDistributionEntry(key.templateKey.String()+"/"+key.origin.String(), totals))
	}
	return strings.Join(parts, ";")
}

func formatPropagationDistributionEntry(name string, totals propagationCounterTotals) string {
	return name + "{" +
		"asserts=" + strconv.Itoa(totals.Asserts) + "," +
		"rhs-asserts=" + strconv.Itoa(totals.RHSAsserts) + "," +
		"rules-visited=" + strconv.Itoa(totals.RuleMemoriesVisited) + "," +
		"conditions-tested=" + strconv.Itoa(totals.ConditionsTested) + "," +
		"alpha-matches-added=" + strconv.Itoa(totals.AlphaMatchesAdded) + "," +
		"condition-plans-tested=" + strconv.Itoa(totals.ConditionPlansTested) + "," +
		"condition-matches-added=" + strconv.Itoa(totals.ConditionMatchesAdded) + "," +
		"prefixes-added=" + strconv.Itoa(totals.PrefixesAdded) + "," +
		"beta-successors-reached=" + strconv.Itoa(totals.BetaSuccessorsReached) + "," +
		"tokens-created=" + strconv.Itoa(totals.TokensCreated) + "," +
		"terminal-deltas-emitted=" + strconv.Itoa(totals.TerminalDeltasEmitted) + "," +
		"agenda-delta-applications=" + strconv.Itoa(totals.AgendaDeltaApplications) + "," +
		"agenda-sorts=" + strconv.Itoa(totals.AgendaSorts) + "," +
		"activations-stored=" + strconv.Itoa(totals.ActivationsStored) + "," +
		"beta-left-input-inserts=" + strconv.Itoa(totals.BetaLeftInputInserts) + "," +
		"beta-right-input-inserts=" + strconv.Itoa(totals.BetaRightInputInserts) + "," +
		"beta-bucket-probes=" + strconv.Itoa(totals.BetaBucketProbes) + "," +
		"beta-candidate-rows-scanned=" + strconv.Itoa(totals.BetaCandidateRowsScanned) + "," +
		"beta-residual-tests=" + strconv.Itoa(totals.BetaResidualTests) + "," +
		"beta-residual-failures=" + strconv.Itoa(totals.BetaResidualFailures) + "," +
		"beta-joined-tokens-produced=" + strconv.Itoa(totals.BetaJoinedTokensProduced) + "}"
}

func slicesSortTemplateKeys(keys []TemplateKey) {
	if len(keys) < 2 {
		return
	}
	slices.Sort(keys)
}

func slicesSortOrigins(keys []propagationOrigin) {
	if len(keys) < 2 {
		return
	}
	slices.Sort(keys)
}

func slicesSortTemplateOriginKeys(keys []propagationCounterKey) {
	if len(keys) < 2 {
		return
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].templateKey != keys[j].templateKey {
			return keys[i].templateKey < keys[j].templateKey
		}
		return keys[i].origin < keys[j].origin
	})
}
