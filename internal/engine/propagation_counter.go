package engine

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
	Asserts                             int
	RHSAsserts                          int
	RuleMemoriesVisited                 int
	ConditionsTested                    int
	AlphaMatchesAdded                   int
	AlphaIndexProbes                    int
	AlphaIndexHits                      int
	AlphaIndexMisses                    int
	AlphaIndexFallbackScans             int
	ConditionPlansTested                int
	ConditionMatchesAdded               int
	PrefixesAdded                       int
	BetaSuccessorsReached               int
	TokensCreated                       int
	TerminalDeltasEmitted               int
	AgendaDeltaApplications             int
	ActivationsStored                   int
	RemovalIndexLookups                 int
	RemovalRowsTouched                  int
	RemovalRowsRemoved                  int
	RemovalRowsMoved                    int
	TerminalDeltasRemoved               int
	NegativePropagationEvents           int
	NegativeRowsRemoved                 int
	NegativeTerminalRowsRemoved         int
	TerminalRowsInserted                int
	TerminalRowsDeduped                 int
	TerminalRowsRemoved                 int
	BetaLeftInputInserts                int
	BetaRightInputInserts               int
	BetaBucketProbes                    int
	BetaJoinIndexHits                   int
	BetaJoinIndexMisses                 int
	BetaBucketDepthTotal                int
	BetaBucketDepthMax                  int
	BetaCandidateRowsScanned            int
	BetaResidualTests                   int
	BetaResidualFailures                int
	BetaJoinedTokensProduced            int
	ExpressionPredicateTests            int
	ExpressionPredicateFailures         int
	ExpressionPredicateErrors           int
	SilentEvaluationCoercions           int
	FunctionCalls                       int
	FunctionErrors                      int
	FunctionCancellations               int
	NestedPathEvaluations               int
	NestedPathMisses                    int
	ModifyFastPathSkips                 int
	ModifyFastPathFallbacks             int
	ModifyCascades                      int
	ModifyRawTerminalAdds               int
	ModifyRawTerminalRemoves            int
	ModifyKeptTerminalAdds              int
	ModifyKeptTerminalRemoves           int
	ModifyCoalescedPairs                int
	ModifyDistinctTokenUpdates          int
	ModifySameTokenCancellations        int
	CoalescerIdentityIndexProbes        int
	CoalescerIdentityIndexCandidates    int
	TokenRowsAllocated                  int
	BetaRowsRemoved                     int
	NegativeBetaRowsRemoved             int
	NegativeBlockerIncrements           int
	NegativeBlockerDecrements           int
	NegativeBlockerZeroToOne            int
	NegativeBlockerOneToZero            int
	FullAgendaReconciles                int
	InitialAgendaReconciles             int
	SteadyStateAgendaReconciles         int
	WholeTerminalScans                  int
	InitialWholeTerminalScans           int
	SteadyStateWholeTerminalScans       int
	GraphRebuilds                       int
	InitialGraphRebuilds                int
	SteadyStateGraphRebuilds            int
	UnsupportedAgendaDeltas             int
	OracleStyleMatchRequests            int
	InitialOracleStyleMatchRequests     int
	SteadyStateOracleStyleMatchRequests int
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
	t.AlphaIndexProbes += other.AlphaIndexProbes
	t.AlphaIndexHits += other.AlphaIndexHits
	t.AlphaIndexMisses += other.AlphaIndexMisses
	t.AlphaIndexFallbackScans += other.AlphaIndexFallbackScans
	t.ConditionPlansTested += other.ConditionPlansTested
	t.ConditionMatchesAdded += other.ConditionMatchesAdded
	t.PrefixesAdded += other.PrefixesAdded
	t.BetaSuccessorsReached += other.BetaSuccessorsReached
	t.TokensCreated += other.TokensCreated
	t.TerminalDeltasEmitted += other.TerminalDeltasEmitted
	t.AgendaDeltaApplications += other.AgendaDeltaApplications
	t.ActivationsStored += other.ActivationsStored
	t.RemovalIndexLookups += other.RemovalIndexLookups
	t.RemovalRowsTouched += other.RemovalRowsTouched
	t.RemovalRowsRemoved += other.RemovalRowsRemoved
	t.RemovalRowsMoved += other.RemovalRowsMoved
	t.TerminalDeltasRemoved += other.TerminalDeltasRemoved
	t.NegativePropagationEvents += other.NegativePropagationEvents
	t.NegativeRowsRemoved += other.NegativeRowsRemoved
	t.NegativeTerminalRowsRemoved += other.NegativeTerminalRowsRemoved
	t.TerminalRowsInserted += other.TerminalRowsInserted
	t.TerminalRowsDeduped += other.TerminalRowsDeduped
	t.TerminalRowsRemoved += other.TerminalRowsRemoved
	t.BetaLeftInputInserts += other.BetaLeftInputInserts
	t.BetaRightInputInserts += other.BetaRightInputInserts
	t.BetaBucketProbes += other.BetaBucketProbes
	t.BetaJoinIndexHits += other.BetaJoinIndexHits
	t.BetaJoinIndexMisses += other.BetaJoinIndexMisses
	t.BetaBucketDepthTotal += other.BetaBucketDepthTotal
	t.BetaBucketDepthMax = max(t.BetaBucketDepthMax, other.BetaBucketDepthMax)
	t.BetaCandidateRowsScanned += other.BetaCandidateRowsScanned
	t.BetaResidualTests += other.BetaResidualTests
	t.BetaResidualFailures += other.BetaResidualFailures
	t.BetaJoinedTokensProduced += other.BetaJoinedTokensProduced
	t.ExpressionPredicateTests += other.ExpressionPredicateTests
	t.ExpressionPredicateFailures += other.ExpressionPredicateFailures
	t.ExpressionPredicateErrors += other.ExpressionPredicateErrors
	t.SilentEvaluationCoercions += other.SilentEvaluationCoercions
	t.FunctionCalls += other.FunctionCalls
	t.FunctionErrors += other.FunctionErrors
	t.FunctionCancellations += other.FunctionCancellations
	t.NestedPathEvaluations += other.NestedPathEvaluations
	t.NestedPathMisses += other.NestedPathMisses
	t.ModifyFastPathSkips += other.ModifyFastPathSkips
	t.ModifyFastPathFallbacks += other.ModifyFastPathFallbacks
	t.ModifyCascades += other.ModifyCascades
	t.ModifyRawTerminalAdds += other.ModifyRawTerminalAdds
	t.ModifyRawTerminalRemoves += other.ModifyRawTerminalRemoves
	t.ModifyKeptTerminalAdds += other.ModifyKeptTerminalAdds
	t.ModifyKeptTerminalRemoves += other.ModifyKeptTerminalRemoves
	t.ModifyCoalescedPairs += other.ModifyCoalescedPairs
	t.ModifyDistinctTokenUpdates += other.ModifyDistinctTokenUpdates
	t.ModifySameTokenCancellations += other.ModifySameTokenCancellations
	t.CoalescerIdentityIndexProbes += other.CoalescerIdentityIndexProbes
	t.CoalescerIdentityIndexCandidates += other.CoalescerIdentityIndexCandidates
	t.TokenRowsAllocated += other.TokenRowsAllocated
	t.BetaRowsRemoved += other.BetaRowsRemoved
	t.NegativeBetaRowsRemoved += other.NegativeBetaRowsRemoved
	t.NegativeBlockerIncrements += other.NegativeBlockerIncrements
	t.NegativeBlockerDecrements += other.NegativeBlockerDecrements
	t.NegativeBlockerZeroToOne += other.NegativeBlockerZeroToOne
	t.NegativeBlockerOneToZero += other.NegativeBlockerOneToZero
	t.FullAgendaReconciles += other.FullAgendaReconciles
	t.InitialAgendaReconciles += other.InitialAgendaReconciles
	t.SteadyStateAgendaReconciles += other.SteadyStateAgendaReconciles
	t.WholeTerminalScans += other.WholeTerminalScans
	t.InitialWholeTerminalScans += other.InitialWholeTerminalScans
	t.SteadyStateWholeTerminalScans += other.SteadyStateWholeTerminalScans
	t.GraphRebuilds += other.GraphRebuilds
	t.InitialGraphRebuilds += other.InitialGraphRebuilds
	t.SteadyStateGraphRebuilds += other.SteadyStateGraphRebuilds
	t.UnsupportedAgendaDeltas += other.UnsupportedAgendaDeltas
	t.OracleStyleMatchRequests += other.OracleStyleMatchRequests
	t.InitialOracleStyleMatchRequests += other.InitialOracleStyleMatchRequests
	t.SteadyStateOracleStyleMatchRequests += other.SteadyStateOracleStyleMatchRequests
}

type propagationCounterPhase uint8

const (
	propagationCounterPhaseInitial propagationCounterPhase = iota
	propagationCounterPhaseSteadyState
)

type propagationCounterKey struct {
	templateKey TemplateKey
	origin      propagationOrigin
}

type propagationBranchKey struct {
	ownerKind      reteGraphBranchOwnerKind
	ruleRevisionID RuleRevisionID
	queryName      string
	terminalID     reteGraphTerminalNodeID
	branchID       int
}

func (k propagationBranchKey) valid() bool {
	return k.ownerKind != "" && k.terminalID > 0 && k.branchID >= 0
}

func (k propagationBranchKey) String() string {
	parts := []string{
		"owner=" + string(k.ownerKind),
		"terminal=" + strconv.Itoa(int(k.terminalID)),
		"branch=" + strconv.Itoa(k.branchID),
	}
	switch k.ownerKind {
	case reteGraphBranchOwnerRule:
		parts = append(parts, "rule-revision="+k.ruleRevisionID.String())
	case reteGraphBranchOwnerQuery:
		parts = append(parts, "query="+k.queryName)
	}
	return strings.Join(parts, ",")
}

type propagationRuntimePath string

const (
	propagationRuntimeUnknown     propagationRuntimePath = "unknown"
	propagationRuntimeGraphBeta   propagationRuntimePath = "graph-beta"
	propagationRuntimeUnsupported propagationRuntimePath = "unsupported"
)

const (
	propagationUnsupportedNoGraph         = "no-graph"
	propagationUnsupportedBetaUnsupported = "beta-unsupported"
	propagationUnsupportedNonEqualityJoin = "non-equality-join"
)

type propagationCounterLedger struct {
	totals               propagationCounterTotals
	byTemplate           map[TemplateKey]*propagationCounterTotals
	byOrigin             map[propagationOrigin]*propagationCounterTotals
	byTemplateOrigin     map[propagationCounterKey]*propagationCounterTotals
	byBranch             map[propagationBranchKey]*propagationCounterTotals
	runtimePath          propagationRuntimePath
	unsupportedReasons   map[string]int
	terminalRowsRetained int
	graphBetaMemory      reteGraphBetaMemoryStats
}

type propagationCounterSpan struct {
	ledger      *propagationCounterLedger
	templateKey TemplateKey
	origin      propagationOrigin
	totals      propagationCounterTotals
	byBranch    map[propagationBranchKey]*propagationCounterTotals
}

type propagationCounterSnapshot struct {
	Totals               propagationCounterTotals
	TerminalRowsRetained int
	GraphBetaMemory      reteGraphBetaMemoryStats
	ByTemplate           map[TemplateKey]propagationCounterTotals
	ByOrigin             map[propagationOrigin]propagationCounterTotals
	ByTemplateOrigin     map[propagationCounterKey]propagationCounterTotals
	ByBranch             map[propagationBranchKey]propagationCounterTotals
	RuntimePath          propagationRuntimePath
	UnsupportedReasons   map[string]int
}

func newPropagationCounterLedger() *propagationCounterLedger {
	return &propagationCounterLedger{
		byTemplate:       make(map[TemplateKey]*propagationCounterTotals),
		byOrigin:         make(map[propagationOrigin]*propagationCounterTotals),
		byTemplateOrigin: make(map[propagationCounterKey]*propagationCounterTotals),
		byBranch:         make(map[propagationBranchKey]*propagationCounterTotals),
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
		Totals:               l.totals,
		TerminalRowsRetained: l.terminalRowsRetained,
		GraphBetaMemory:      l.graphBetaMemory,
		ByTemplate:           make(map[TemplateKey]propagationCounterTotals, len(l.byTemplate)),
		ByOrigin:             make(map[propagationOrigin]propagationCounterTotals, len(l.byOrigin)),
		ByTemplateOrigin:     make(map[propagationCounterKey]propagationCounterTotals, len(l.byTemplateOrigin)),
		ByBranch:             make(map[propagationBranchKey]propagationCounterTotals, len(l.byBranch)),
		RuntimePath:          l.runtimePath,
	}
	if len(l.unsupportedReasons) > 0 {
		out.UnsupportedReasons = make(map[string]int, len(l.unsupportedReasons))
		maps.Copy(out.UnsupportedReasons, l.unsupportedReasons)
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
	for key, totals := range l.byBranch {
		if totals != nil {
			out.ByBranch[key] = *totals
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

func (s *propagationCounterSpan) recordAlphaIndexProbe(hit bool) {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.AlphaIndexProbes++
	if hit {
		s.totals.AlphaIndexHits++
	} else {
		s.totals.AlphaIndexMisses++
	}
}

func (s *propagationCounterSpan) recordAlphaIndexFallbackScan() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.AlphaIndexFallbackScans++
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

func (s *propagationCounterSpan) recordBetaBucketProbe(depth int) {
	if s == nil || s.ledger == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	s.totals.BetaBucketProbes++
	if depth > 0 {
		s.totals.BetaJoinIndexHits++
	} else {
		s.totals.BetaJoinIndexMisses++
	}
	s.totals.BetaBucketDepthTotal += depth
	s.totals.BetaBucketDepthMax = max(s.totals.BetaBucketDepthMax, depth)
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

func (s *propagationCounterSpan) recordExpressionPredicateTest() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.ExpressionPredicateTests++
}

func (s *propagationCounterSpan) recordExpressionPredicateFailure() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.ExpressionPredicateFailures++
}

func (s *propagationCounterSpan) recordExpressionPredicateError() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.ExpressionPredicateErrors++
}

func (s *propagationCounterSpan) recordSilentEvaluationCoercion() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.SilentEvaluationCoercions++
}

func (s *propagationCounterSpan) recordFunctionCall() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.FunctionCalls++
}

func (s *propagationCounterSpan) recordFunctionError() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.FunctionErrors++
}

func (s *propagationCounterSpan) recordFunctionCancellation() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.FunctionCancellations++
}

func (s *propagationCounterSpan) recordNestedPathEvaluation(found bool) {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.NestedPathEvaluations++
	if !found {
		s.totals.NestedPathMisses++
	}
}

func (s *propagationCounterSpan) recordTerminalDeltaEmitted() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.TerminalDeltasEmitted++
}

func (s *propagationCounterSpan) recordTerminalDeltaEmittedForBranch(key propagationBranchKey) {
	totals := s.branchTotals(key)
	if totals == nil {
		return
	}
	totals.TerminalDeltasEmitted++
}

func (s *propagationCounterSpan) recordTerminalRowInserted() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.TerminalRowsInserted++
}

func (s *propagationCounterSpan) recordTerminalRowInsertedForBranch(key propagationBranchKey) {
	totals := s.branchTotals(key)
	if totals == nil {
		return
	}
	totals.TerminalRowsInserted++
}

func (s *propagationCounterSpan) recordTerminalRowDeduped() {
	if s == nil || s.ledger == nil {
		return
	}
	s.totals.TerminalRowsDeduped++
}

func (s *propagationCounterSpan) recordTerminalRowDedupedForBranch(key propagationBranchKey) {
	totals := s.branchTotals(key)
	if totals == nil {
		return
	}
	totals.TerminalRowsDeduped++
}

func (s *propagationCounterSpan) branchTotals(key propagationBranchKey) *propagationCounterTotals {
	if s == nil || s.ledger == nil || !key.valid() {
		return nil
	}
	if s.byBranch == nil {
		s.byBranch = make(map[propagationBranchKey]*propagationCounterTotals)
	}
	totals := s.byBranch[key]
	if totals == nil {
		totals = &propagationCounterTotals{}
		s.byBranch[key] = totals
	}
	return totals
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
	for key, totals := range s.byBranch {
		if totals != nil {
			ledger.addBranchTotals(key, *totals)
		}
	}
	s.ledger = nil
}

func (l *propagationCounterLedger) recordAgendaDeltaApplication() {
	if l == nil {
		return
	}
	l.totals.AgendaDeltaApplications++
}

func (l *propagationCounterLedger) recordActivationStored() {
	if l == nil {
		return
	}
	l.totals.ActivationsStored++
}

func (l *propagationCounterLedger) recordModifyFastPathSkip() {
	if l == nil {
		return
	}
	l.totals.ModifyFastPathSkips++
}

func (l *propagationCounterLedger) recordModifyFastPathFallback() {
	if l == nil {
		return
	}
	l.totals.ModifyFastPathFallbacks++
}

func (l *propagationCounterLedger) recordModifyCascade(rawAdds, rawRemoves, keptAdds, keptRemoves int) {
	if l == nil {
		return
	}
	l.totals.ModifyCascades++
	l.totals.ModifyRawTerminalAdds += rawAdds
	l.totals.ModifyRawTerminalRemoves += rawRemoves
	l.totals.ModifyKeptTerminalAdds += keptAdds
	l.totals.ModifyKeptTerminalRemoves += keptRemoves
}

func (l *propagationCounterLedger) recordModifyCoalescedPair(distinctTokens bool) {
	if l == nil {
		return
	}
	l.totals.ModifyCoalescedPairs++
	if distinctTokens {
		l.totals.ModifyDistinctTokenUpdates++
	} else {
		l.totals.ModifySameTokenCancellations++
	}
}

func (l *propagationCounterLedger) recordCoalescerIdentityIndexProbe(candidates int) {
	if l == nil {
		return
	}
	l.totals.CoalescerIdentityIndexProbes++
	l.totals.CoalescerIdentityIndexCandidates += candidates
}

func (l *propagationCounterLedger) recordTokenRowAllocated() {
	if l != nil {
		l.totals.TokenRowsAllocated++
	}
}

func (l *propagationCounterLedger) recordBetaRowRemoved() {
	if l != nil {
		l.totals.BetaRowsRemoved++
	}
}

func (l *propagationCounterLedger) recordNegativeBetaRowRemoved() {
	if l != nil {
		l.totals.NegativeBetaRowsRemoved++
	}
}

func (l *propagationCounterLedger) recordNegativeBlockerIncrement(wasZero bool) {
	if l == nil {
		return
	}
	l.totals.NegativeBlockerIncrements++
	if wasZero {
		l.totals.NegativeBlockerZeroToOne++
	}
}

func (l *propagationCounterLedger) recordNegativeBlockersInitialized(count int) {
	if l == nil || count <= 0 {
		return
	}
	l.totals.NegativeBlockerIncrements += count
	l.totals.NegativeBlockerZeroToOne++
}

func (l *propagationCounterLedger) recordNegativeBlockerDecrement(wasOne bool) {
	if l == nil {
		return
	}
	l.totals.NegativeBlockerDecrements++
	if wasOne {
		l.totals.NegativeBlockerOneToZero++
	}
}

func (l *propagationCounterLedger) recordFullAgendaReconcile(phase propagationCounterPhase) {
	if l == nil {
		return
	}
	l.totals.FullAgendaReconciles++
	switch phase {
	case propagationCounterPhaseSteadyState:
		l.totals.SteadyStateAgendaReconciles++
	default:
		l.totals.InitialAgendaReconciles++
	}
}

func (l *propagationCounterLedger) recordWholeTerminalScan(phase propagationCounterPhase) {
	if l == nil {
		return
	}
	l.totals.WholeTerminalScans++
	switch phase {
	case propagationCounterPhaseSteadyState:
		l.totals.SteadyStateWholeTerminalScans++
	default:
		l.totals.InitialWholeTerminalScans++
	}
}

func (l *propagationCounterLedger) recordGraphRebuild(phase propagationCounterPhase) {
	if l == nil {
		return
	}
	l.totals.GraphRebuilds++
	switch phase {
	case propagationCounterPhaseSteadyState:
		l.totals.SteadyStateGraphRebuilds++
	default:
		l.totals.InitialGraphRebuilds++
	}
}

func (l *propagationCounterLedger) recordUnsupportedAgendaDelta() {
	if l == nil {
		return
	}
	l.totals.UnsupportedAgendaDeltas++
}

func (l *propagationCounterLedger) recordOracleStyleMatchRequest(phase propagationCounterPhase) {
	if l == nil {
		return
	}
	l.totals.OracleStyleMatchRequests++
	switch phase {
	case propagationCounterPhaseSteadyState:
		l.totals.SteadyStateOracleStyleMatchRequests++
	default:
		l.totals.InitialOracleStyleMatchRequests++
	}
}

func (l *propagationCounterLedger) recordAlphaIndexProbe(hit bool) {
	if l == nil {
		return
	}
	l.totals.AlphaIndexProbes++
	if hit {
		l.totals.AlphaIndexHits++
	} else {
		l.totals.AlphaIndexMisses++
	}
}

func (l *propagationCounterLedger) recordAlphaIndexFallbackScan() {
	if l == nil {
		return
	}
	l.totals.AlphaIndexFallbackScans++
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

func (l *propagationCounterLedger) recordRemovalRowMoved() {
	if l == nil {
		return
	}
	l.totals.RemovalRowsMoved++
}

func (l *propagationCounterLedger) recordTerminalDeltaRemoved() {
	if l == nil {
		return
	}
	l.totals.TerminalDeltasRemoved++
}

func (l *propagationCounterLedger) recordTerminalDeltaRemovedForBranch(key propagationBranchKey) {
	totals := l.branchTotals(key)
	if totals == nil {
		return
	}
	totals.TerminalDeltasRemoved++
}

func (l *propagationCounterLedger) recordNegativePropagationEvent() {
	if l == nil {
		return
	}
	l.totals.NegativePropagationEvents++
}

func (l *propagationCounterLedger) recordNegativeRowRemoved() {
	if l == nil {
		return
	}
	l.totals.NegativeRowsRemoved++
}

func (l *propagationCounterLedger) recordNegativeTerminalRowRemoved() {
	if l == nil {
		return
	}
	l.totals.NegativeTerminalRowsRemoved++
}

func (l *propagationCounterLedger) recordTerminalRowRemoved() {
	if l == nil {
		return
	}
	l.totals.TerminalRowsRemoved++
}

func (l *propagationCounterLedger) recordTerminalRowRemovedForBranch(key propagationBranchKey) {
	totals := l.branchTotals(key)
	if totals == nil {
		return
	}
	totals.TerminalRowsRemoved++
}

func (l *propagationCounterLedger) setTerminalRowsRetained(retained int) {
	if l == nil {
		return
	}
	if retained < 0 {
		retained = 0
	}
	l.terminalRowsRetained = retained
}

func (l *propagationCounterLedger) setGraphBetaMemoryStats(stats reteGraphBetaMemoryStats) {
	if l == nil {
		return
	}
	l.graphBetaMemory = stats
}

func (l *propagationCounterLedger) setRuntimeDiagnostics(path propagationRuntimePath, unsupportedReasons map[string]int) {
	if l == nil {
		return
	}
	if path == "" {
		path = propagationRuntimeUnknown
	}
	l.runtimePath = path
	if len(unsupportedReasons) == 0 {
		clear(l.unsupportedReasons)
		return
	}
	if l.unsupportedReasons == nil {
		l.unsupportedReasons = make(map[string]int, len(unsupportedReasons))
	} else {
		clear(l.unsupportedReasons)
	}
	for reason, count := range unsupportedReasons {
		if count > 0 {
			l.unsupportedReasons[reason] = count
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

func (l *propagationCounterLedger) addBranchTotals(key propagationBranchKey, totals propagationCounterTotals) {
	current := l.branchTotals(key)
	if current == nil {
		return
	}
	current.add(totals)
}

func (l *propagationCounterLedger) branchTotals(key propagationBranchKey) *propagationCounterTotals {
	if l == nil || !key.valid() {
		return nil
	}
	if l.byBranch == nil {
		l.byBranch = make(map[propagationBranchKey]*propagationCounterTotals)
	}
	current := l.byBranch[key]
	if current == nil {
		current = &propagationCounterTotals{}
		l.byBranch[key] = current
	}
	return current
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
	report("propagation-alpha-index-probes", float64(s.Totals.AlphaIndexProbes))
	report("propagation-alpha-index-hits", float64(s.Totals.AlphaIndexHits))
	report("propagation-alpha-index-misses", float64(s.Totals.AlphaIndexMisses))
	report("propagation-alpha-index-fallback-scans", float64(s.Totals.AlphaIndexFallbackScans))
	report("propagation-condition-plans-tested", float64(s.Totals.ConditionPlansTested))
	report("propagation-condition-matches-added", float64(s.Totals.ConditionMatchesAdded))
	report("propagation-prefixes-added", float64(s.Totals.PrefixesAdded))
	report("propagation-beta-successors-reached", float64(s.Totals.BetaSuccessorsReached))
	report("propagation-tokens-created", float64(s.Totals.TokensCreated))
	report("propagation-terminal-deltas-emitted", float64(s.Totals.TerminalDeltasEmitted))
	report("propagation-terminal-rows-inserted", float64(s.Totals.TerminalRowsInserted))
	report("propagation-terminal-rows-deduped", float64(s.Totals.TerminalRowsDeduped))
	report("propagation-terminal-rows-removed", float64(s.Totals.TerminalRowsRemoved))
	report("propagation-terminal-rows-retained", float64(s.TerminalRowsRetained))
	report("propagation-graph-token-memories", float64(s.GraphBetaMemory.TokenMemories))
	report("propagation-graph-beta-token-memories", float64(s.GraphBetaMemory.BetaTokenMemories))
	report("propagation-graph-terminal-token-memories", float64(s.GraphBetaMemory.TerminalTokenMemories))
	report("propagation-graph-token-rows", float64(s.GraphBetaMemory.TokenRows))
	report("propagation-graph-token-row-capacity", float64(s.GraphBetaMemory.TokenRowCapacity))
	report("propagation-graph-token-row-reserve", float64(s.GraphBetaMemory.TokenRowReserve))
	report("propagation-graph-token-row-capacity-max", float64(s.GraphBetaMemory.TokenRowCapacityMax))
	report("propagation-graph-token-row-reserve-max", float64(s.GraphBetaMemory.TokenRowReserveMax))
	report("propagation-graph-join-index-keys", float64(s.GraphBetaMemory.JoinIndexKeys))
	report("propagation-graph-join-index-reserve", float64(s.GraphBetaMemory.JoinIndexReserve))
	report("propagation-graph-join-index-keys-max", float64(s.GraphBetaMemory.JoinIndexKeysMax))
	report("propagation-graph-join-index-reserve-max", float64(s.GraphBetaMemory.JoinIndexReserveMax))
	report("propagation-graph-identity-index-keys", float64(s.GraphBetaMemory.IdentityIndexKeys))
	report("propagation-graph-identity-index-reserve", float64(s.GraphBetaMemory.IdentityIndexReserve))
	report("propagation-graph-identity-index-keys-max", float64(s.GraphBetaMemory.IdentityIndexKeysMax))
	report("propagation-graph-identity-index-reserve-max", float64(s.GraphBetaMemory.IdentityIndexReserveMax))
	report("propagation-graph-fact-index-keys", float64(s.GraphBetaMemory.FactIndexKeys))
	report("propagation-graph-fact-index-reserve", float64(s.GraphBetaMemory.FactIndexReserve))
	report("propagation-graph-fact-index-keys-max", float64(s.GraphBetaMemory.FactIndexKeysMax))
	report("propagation-graph-fact-index-reserve-max", float64(s.GraphBetaMemory.FactIndexReserveMax))
	report("propagation-agenda-delta-applications", float64(s.Totals.AgendaDeltaApplications))
	report("propagation-activations-stored", float64(s.Totals.ActivationsStored))
	report("propagation-removal-index-lookups", float64(s.Totals.RemovalIndexLookups))
	report("propagation-removal-rows-touched", float64(s.Totals.RemovalRowsTouched))
	report("propagation-removal-rows-removed", float64(s.Totals.RemovalRowsRemoved))
	report("propagation-removal-rows-moved", float64(s.Totals.RemovalRowsMoved))
	report("propagation-terminal-deltas-removed", float64(s.Totals.TerminalDeltasRemoved))
	report("propagation-negative-propagation-events", float64(s.Totals.NegativePropagationEvents))
	report("propagation-negative-rows-removed", float64(s.Totals.NegativeRowsRemoved))
	report("propagation-negative-terminal-rows-removed", float64(s.Totals.NegativeTerminalRowsRemoved))
	report("propagation-beta-left-input-inserts", float64(s.Totals.BetaLeftInputInserts))
	report("propagation-beta-right-input-inserts", float64(s.Totals.BetaRightInputInserts))
	report("propagation-beta-bucket-probes", float64(s.Totals.BetaBucketProbes))
	report("propagation-beta-join-index-hits", float64(s.Totals.BetaJoinIndexHits))
	report("propagation-beta-join-index-misses", float64(s.Totals.BetaJoinIndexMisses))
	report("propagation-beta-bucket-depth-total", float64(s.Totals.BetaBucketDepthTotal))
	report("propagation-beta-bucket-depth-max", float64(s.Totals.BetaBucketDepthMax))
	report("propagation-beta-bucket-depth-mean", float64(s.Totals.BetaBucketDepthTotal)/float64(max(1, s.Totals.BetaBucketProbes)))
	report("propagation-beta-candidate-rows-scanned", float64(s.Totals.BetaCandidateRowsScanned))
	report("propagation-beta-residual-tests", float64(s.Totals.BetaResidualTests))
	report("propagation-beta-residual-failures", float64(s.Totals.BetaResidualFailures))
	report("propagation-beta-joined-tokens-produced", float64(s.Totals.BetaJoinedTokensProduced))
	report("propagation-expression-predicate-tests", float64(s.Totals.ExpressionPredicateTests))
	report("propagation-expression-predicate-failures", float64(s.Totals.ExpressionPredicateFailures))
	report("propagation-expression-predicate-errors", float64(s.Totals.ExpressionPredicateErrors))
	report("propagation-silent-evaluation-coercions", float64(s.Totals.SilentEvaluationCoercions))
	report("propagation-function-calls", float64(s.Totals.FunctionCalls))
	report("propagation-function-errors", float64(s.Totals.FunctionErrors))
	report("propagation-function-cancellations", float64(s.Totals.FunctionCancellations))
	report("propagation-nested-path-evaluations", float64(s.Totals.NestedPathEvaluations))
	report("propagation-nested-path-misses", float64(s.Totals.NestedPathMisses))
	report("propagation-modify-fast-path-skips", float64(s.Totals.ModifyFastPathSkips))
	report("propagation-modify-fast-path-fallbacks", float64(s.Totals.ModifyFastPathFallbacks))
	report("propagation-modify-cascades", float64(s.Totals.ModifyCascades))
	report("propagation-modify-raw-terminal-adds", float64(s.Totals.ModifyRawTerminalAdds))
	report("propagation-modify-raw-terminal-removes", float64(s.Totals.ModifyRawTerminalRemoves))
	report("propagation-modify-kept-terminal-adds", float64(s.Totals.ModifyKeptTerminalAdds))
	report("propagation-modify-kept-terminal-removes", float64(s.Totals.ModifyKeptTerminalRemoves))
	report("propagation-modify-coalesced-pairs", float64(s.Totals.ModifyCoalescedPairs))
	report("propagation-modify-distinct-token-updates", float64(s.Totals.ModifyDistinctTokenUpdates))
	report("propagation-modify-same-token-cancellations", float64(s.Totals.ModifySameTokenCancellations))
	report("propagation-coalescer-identity-index-probes", float64(s.Totals.CoalescerIdentityIndexProbes))
	report("propagation-coalescer-identity-index-candidates", float64(s.Totals.CoalescerIdentityIndexCandidates))
	report("propagation-token-rows-allocated", float64(s.Totals.TokenRowsAllocated))
	report("propagation-beta-rows-removed", float64(s.Totals.BetaRowsRemoved))
	report("propagation-negative-beta-rows-removed", float64(s.Totals.NegativeBetaRowsRemoved))
	report("propagation-negative-blocker-increments", float64(s.Totals.NegativeBlockerIncrements))
	report("propagation-negative-blocker-decrements", float64(s.Totals.NegativeBlockerDecrements))
	report("propagation-negative-blocker-zero-to-one", float64(s.Totals.NegativeBlockerZeroToOne))
	report("propagation-negative-blocker-one-to-zero", float64(s.Totals.NegativeBlockerOneToZero))
	report("propagation-full-agenda-reconciles", float64(s.Totals.FullAgendaReconciles))
	report("propagation-initial-agenda-reconciles", float64(s.Totals.InitialAgendaReconciles))
	report("propagation-steady-state-agenda-reconciles", float64(s.Totals.SteadyStateAgendaReconciles))
	report("propagation-whole-terminal-scans", float64(s.Totals.WholeTerminalScans))
	report("propagation-initial-whole-terminal-scans", float64(s.Totals.InitialWholeTerminalScans))
	report("propagation-steady-state-whole-terminal-scans", float64(s.Totals.SteadyStateWholeTerminalScans))
	report("propagation-graph-rebuilds", float64(s.Totals.GraphRebuilds))
	report("propagation-initial-graph-rebuilds", float64(s.Totals.InitialGraphRebuilds))
	report("propagation-steady-state-graph-rebuilds", float64(s.Totals.SteadyStateGraphRebuilds))
	report("propagation-unsupported-agenda-deltas", float64(s.Totals.UnsupportedAgendaDeltas))
	report("propagation-oracle-style-match-requests", float64(s.Totals.OracleStyleMatchRequests))
	report("propagation-initial-oracle-style-match-requests", float64(s.Totals.InitialOracleStyleMatchRequests))
	report("propagation-steady-state-oracle-style-match-requests", float64(s.Totals.SteadyStateOracleStyleMatchRequests))

	rhsAsserts := float64(max(1, s.Totals.RHSAsserts))
	report("propagation-rule-memories-visited/rhs-assert", float64(s.Totals.RuleMemoriesVisited)/rhsAsserts)
	report("propagation-conditions-tested/rhs-assert", float64(s.Totals.ConditionsTested)/rhsAsserts)
	report("propagation-alpha-matches-added/rhs-assert", float64(s.Totals.AlphaMatchesAdded)/rhsAsserts)
	report("propagation-alpha-index-probes/rhs-assert", float64(s.Totals.AlphaIndexProbes)/rhsAsserts)
	report("propagation-alpha-index-hits/rhs-assert", float64(s.Totals.AlphaIndexHits)/rhsAsserts)
	report("propagation-alpha-index-misses/rhs-assert", float64(s.Totals.AlphaIndexMisses)/rhsAsserts)
	report("propagation-alpha-index-fallback-scans/rhs-assert", float64(s.Totals.AlphaIndexFallbackScans)/rhsAsserts)
	report("propagation-condition-plans-tested/rhs-assert", float64(s.Totals.ConditionPlansTested)/rhsAsserts)
	report("propagation-condition-matches-added/rhs-assert", float64(s.Totals.ConditionMatchesAdded)/rhsAsserts)
	report("propagation-prefixes-added/rhs-assert", float64(s.Totals.PrefixesAdded)/rhsAsserts)
	report("propagation-beta-successors-reached/rhs-assert", float64(s.Totals.BetaSuccessorsReached)/rhsAsserts)
	report("propagation-tokens-created/rhs-assert", float64(s.Totals.TokensCreated)/rhsAsserts)
	report("propagation-terminal-deltas-emitted/rhs-assert", float64(s.Totals.TerminalDeltasEmitted)/rhsAsserts)
	report("propagation-terminal-rows-inserted/rhs-assert", float64(s.Totals.TerminalRowsInserted)/rhsAsserts)
	report("propagation-terminal-rows-deduped/rhs-assert", float64(s.Totals.TerminalRowsDeduped)/rhsAsserts)
	report("propagation-terminal-rows-removed/rhs-assert", float64(s.Totals.TerminalRowsRemoved)/rhsAsserts)
	report("propagation-agenda-delta-applications/rhs-assert", float64(s.Totals.AgendaDeltaApplications)/rhsAsserts)
	report("propagation-activations-stored/rhs-assert", float64(s.Totals.ActivationsStored)/rhsAsserts)
	report("propagation-beta-left-input-inserts/rhs-assert", float64(s.Totals.BetaLeftInputInserts)/rhsAsserts)
	report("propagation-beta-right-input-inserts/rhs-assert", float64(s.Totals.BetaRightInputInserts)/rhsAsserts)
	report("propagation-beta-bucket-probes/rhs-assert", float64(s.Totals.BetaBucketProbes)/rhsAsserts)
	report("propagation-beta-join-index-hits/rhs-assert", float64(s.Totals.BetaJoinIndexHits)/rhsAsserts)
	report("propagation-beta-join-index-misses/rhs-assert", float64(s.Totals.BetaJoinIndexMisses)/rhsAsserts)
	report("propagation-beta-bucket-depth-total/rhs-assert", float64(s.Totals.BetaBucketDepthTotal)/rhsAsserts)
	report("propagation-beta-candidate-rows-scanned/rhs-assert", float64(s.Totals.BetaCandidateRowsScanned)/rhsAsserts)
	report("propagation-beta-residual-tests/rhs-assert", float64(s.Totals.BetaResidualTests)/rhsAsserts)
	report("propagation-beta-residual-failures/rhs-assert", float64(s.Totals.BetaResidualFailures)/rhsAsserts)
	report("propagation-beta-joined-tokens-produced/rhs-assert", float64(s.Totals.BetaJoinedTokensProduced)/rhsAsserts)
	report("propagation-expression-predicate-tests/rhs-assert", float64(s.Totals.ExpressionPredicateTests)/rhsAsserts)
	report("propagation-expression-predicate-failures/rhs-assert", float64(s.Totals.ExpressionPredicateFailures)/rhsAsserts)
	report("propagation-expression-predicate-errors/rhs-assert", float64(s.Totals.ExpressionPredicateErrors)/rhsAsserts)
	report("propagation-silent-evaluation-coercions/rhs-assert", float64(s.Totals.SilentEvaluationCoercions)/rhsAsserts)
	report("propagation-nested-path-evaluations/rhs-assert", float64(s.Totals.NestedPathEvaluations)/rhsAsserts)
	report("propagation-nested-path-misses/rhs-assert", float64(s.Totals.NestedPathMisses)/rhsAsserts)
	report("propagation-template-count", float64(len(s.ByTemplate)))
	report("propagation-origin-count", float64(len(s.ByOrigin)))
	report("propagation-template-origin-count", float64(len(s.ByTemplateOrigin)))
	report("propagation-branch-count", float64(len(s.ByBranch)))
	report("propagation-runtime-graph-beta", propagationRuntimePathMetric(s.RuntimePath, propagationRuntimeGraphBeta))
	report("propagation-runtime-unsupported", propagationRuntimePathMetric(s.RuntimePath, propagationRuntimeUnsupported))
	report("propagation-unsupported-reason-count", float64(len(s.UnsupportedReasons)))
}

func (s propagationCounterSnapshot) runnerFields() []string {
	if s.Totals.Asserts == 0 && s.Totals.RHSAsserts == 0 && s.Totals.AlphaIndexProbes == 0 && s.Totals.AlphaIndexFallbackScans == 0 && s.Totals.FullAgendaReconciles == 0 && s.Totals.WholeTerminalScans == 0 && s.Totals.GraphRebuilds == 0 && s.Totals.UnsupportedAgendaDeltas == 0 && s.Totals.OracleStyleMatchRequests == 0 && s.TerminalRowsRetained == 0 && len(s.ByTemplate) == 0 && len(s.ByOrigin) == 0 && s.RuntimePath == "" {
		return nil
	}
	fields := []string{
		"propagation-runtime-path=" + string(s.runtimePath()),
		"propagation-unsupported-reasons=" + s.unsupportedReasonSummary(),
		"propagation-asserts=" + strconv.Itoa(s.Totals.Asserts),
		"propagation-rhs-asserts=" + strconv.Itoa(s.Totals.RHSAsserts),
		"propagation-rule-memories-visited=" + strconv.Itoa(s.Totals.RuleMemoriesVisited),
		"propagation-conditions-tested=" + strconv.Itoa(s.Totals.ConditionsTested),
		"propagation-alpha-matches-added=" + strconv.Itoa(s.Totals.AlphaMatchesAdded),
		"propagation-alpha-index-probes=" + strconv.Itoa(s.Totals.AlphaIndexProbes),
		"propagation-alpha-index-hits=" + strconv.Itoa(s.Totals.AlphaIndexHits),
		"propagation-alpha-index-misses=" + strconv.Itoa(s.Totals.AlphaIndexMisses),
		"propagation-alpha-index-fallback-scans=" + strconv.Itoa(s.Totals.AlphaIndexFallbackScans),
		"propagation-condition-plans-tested=" + strconv.Itoa(s.Totals.ConditionPlansTested),
		"propagation-condition-matches-added=" + strconv.Itoa(s.Totals.ConditionMatchesAdded),
		"propagation-prefixes-added=" + strconv.Itoa(s.Totals.PrefixesAdded),
		"propagation-beta-successors-reached=" + strconv.Itoa(s.Totals.BetaSuccessorsReached),
		"propagation-tokens-created=" + strconv.Itoa(s.Totals.TokensCreated),
		"propagation-terminal-deltas-emitted=" + strconv.Itoa(s.Totals.TerminalDeltasEmitted),
		"propagation-terminal-rows-inserted=" + strconv.Itoa(s.Totals.TerminalRowsInserted),
		"propagation-terminal-rows-deduped=" + strconv.Itoa(s.Totals.TerminalRowsDeduped),
		"propagation-terminal-rows-removed=" + strconv.Itoa(s.Totals.TerminalRowsRemoved),
		"propagation-terminal-rows-retained=" + strconv.Itoa(s.TerminalRowsRetained),
		"propagation-graph-token-memories=" + strconv.Itoa(s.GraphBetaMemory.TokenMemories),
		"propagation-graph-beta-token-memories=" + strconv.Itoa(s.GraphBetaMemory.BetaTokenMemories),
		"propagation-graph-terminal-token-memories=" + strconv.Itoa(s.GraphBetaMemory.TerminalTokenMemories),
		"propagation-graph-token-rows=" + strconv.Itoa(s.GraphBetaMemory.TokenRows),
		"propagation-graph-token-row-capacity=" + strconv.Itoa(s.GraphBetaMemory.TokenRowCapacity),
		"propagation-graph-token-row-reserve=" + strconv.Itoa(s.GraphBetaMemory.TokenRowReserve),
		"propagation-graph-token-row-capacity-max=" + strconv.Itoa(s.GraphBetaMemory.TokenRowCapacityMax),
		"propagation-graph-token-row-reserve-max=" + strconv.Itoa(s.GraphBetaMemory.TokenRowReserveMax),
		"propagation-graph-join-index-keys=" + strconv.Itoa(s.GraphBetaMemory.JoinIndexKeys),
		"propagation-graph-join-index-reserve=" + strconv.Itoa(s.GraphBetaMemory.JoinIndexReserve),
		"propagation-graph-join-index-keys-max=" + strconv.Itoa(s.GraphBetaMemory.JoinIndexKeysMax),
		"propagation-graph-join-index-reserve-max=" + strconv.Itoa(s.GraphBetaMemory.JoinIndexReserveMax),
		"propagation-graph-identity-index-keys=" + strconv.Itoa(s.GraphBetaMemory.IdentityIndexKeys),
		"propagation-graph-identity-index-reserve=" + strconv.Itoa(s.GraphBetaMemory.IdentityIndexReserve),
		"propagation-graph-identity-index-keys-max=" + strconv.Itoa(s.GraphBetaMemory.IdentityIndexKeysMax),
		"propagation-graph-identity-index-reserve-max=" + strconv.Itoa(s.GraphBetaMemory.IdentityIndexReserveMax),
		"propagation-graph-fact-index-keys=" + strconv.Itoa(s.GraphBetaMemory.FactIndexKeys),
		"propagation-graph-fact-index-reserve=" + strconv.Itoa(s.GraphBetaMemory.FactIndexReserve),
		"propagation-graph-fact-index-keys-max=" + strconv.Itoa(s.GraphBetaMemory.FactIndexKeysMax),
		"propagation-graph-fact-index-reserve-max=" + strconv.Itoa(s.GraphBetaMemory.FactIndexReserveMax),
		"propagation-agenda-delta-applications=" + strconv.Itoa(s.Totals.AgendaDeltaApplications),
		"propagation-activations-stored=" + strconv.Itoa(s.Totals.ActivationsStored),
		"propagation-modify-fast-path-skips=" + strconv.Itoa(s.Totals.ModifyFastPathSkips),
		"propagation-modify-fast-path-fallbacks=" + strconv.Itoa(s.Totals.ModifyFastPathFallbacks),
		"propagation-modify-cascades=" + strconv.Itoa(s.Totals.ModifyCascades),
		"propagation-modify-raw-terminal-adds=" + strconv.Itoa(s.Totals.ModifyRawTerminalAdds),
		"propagation-modify-raw-terminal-removes=" + strconv.Itoa(s.Totals.ModifyRawTerminalRemoves),
		"propagation-modify-kept-terminal-adds=" + strconv.Itoa(s.Totals.ModifyKeptTerminalAdds),
		"propagation-modify-kept-terminal-removes=" + strconv.Itoa(s.Totals.ModifyKeptTerminalRemoves),
		"propagation-modify-coalesced-pairs=" + strconv.Itoa(s.Totals.ModifyCoalescedPairs),
		"propagation-modify-distinct-token-updates=" + strconv.Itoa(s.Totals.ModifyDistinctTokenUpdates),
		"propagation-modify-same-token-cancellations=" + strconv.Itoa(s.Totals.ModifySameTokenCancellations),
		"propagation-coalescer-identity-index-probes=" + strconv.Itoa(s.Totals.CoalescerIdentityIndexProbes),
		"propagation-coalescer-identity-index-candidates=" + strconv.Itoa(s.Totals.CoalescerIdentityIndexCandidates),
		"propagation-token-rows-allocated=" + strconv.Itoa(s.Totals.TokenRowsAllocated),
		"propagation-beta-rows-removed=" + strconv.Itoa(s.Totals.BetaRowsRemoved),
		"propagation-negative-beta-rows-removed=" + strconv.Itoa(s.Totals.NegativeBetaRowsRemoved),
		"propagation-negative-blocker-increments=" + strconv.Itoa(s.Totals.NegativeBlockerIncrements),
		"propagation-negative-blocker-decrements=" + strconv.Itoa(s.Totals.NegativeBlockerDecrements),
		"propagation-negative-blocker-zero-to-one=" + strconv.Itoa(s.Totals.NegativeBlockerZeroToOne),
		"propagation-negative-blocker-one-to-zero=" + strconv.Itoa(s.Totals.NegativeBlockerOneToZero),
		"propagation-full-agenda-reconciles=" + strconv.Itoa(s.Totals.FullAgendaReconciles),
		"propagation-initial-agenda-reconciles=" + strconv.Itoa(s.Totals.InitialAgendaReconciles),
		"propagation-steady-state-agenda-reconciles=" + strconv.Itoa(s.Totals.SteadyStateAgendaReconciles),
		"propagation-whole-terminal-scans=" + strconv.Itoa(s.Totals.WholeTerminalScans),
		"propagation-initial-whole-terminal-scans=" + strconv.Itoa(s.Totals.InitialWholeTerminalScans),
		"propagation-steady-state-whole-terminal-scans=" + strconv.Itoa(s.Totals.SteadyStateWholeTerminalScans),
		"propagation-graph-rebuilds=" + strconv.Itoa(s.Totals.GraphRebuilds),
		"propagation-initial-graph-rebuilds=" + strconv.Itoa(s.Totals.InitialGraphRebuilds),
		"propagation-steady-state-graph-rebuilds=" + strconv.Itoa(s.Totals.SteadyStateGraphRebuilds),
		"propagation-unsupported-agenda-deltas=" + strconv.Itoa(s.Totals.UnsupportedAgendaDeltas),
		"propagation-oracle-style-match-requests=" + strconv.Itoa(s.Totals.OracleStyleMatchRequests),
		"propagation-initial-oracle-style-match-requests=" + strconv.Itoa(s.Totals.InitialOracleStyleMatchRequests),
		"propagation-steady-state-oracle-style-match-requests=" + strconv.Itoa(s.Totals.SteadyStateOracleStyleMatchRequests),
		"propagation-removal-index-lookups=" + strconv.Itoa(s.Totals.RemovalIndexLookups),
		"propagation-removal-rows-touched=" + strconv.Itoa(s.Totals.RemovalRowsTouched),
		"propagation-removal-rows-removed=" + strconv.Itoa(s.Totals.RemovalRowsRemoved),
		"propagation-removal-rows-moved=" + strconv.Itoa(s.Totals.RemovalRowsMoved),
		"propagation-terminal-deltas-removed=" + strconv.Itoa(s.Totals.TerminalDeltasRemoved),
		"propagation-negative-propagation-events=" + strconv.Itoa(s.Totals.NegativePropagationEvents),
		"propagation-negative-rows-removed=" + strconv.Itoa(s.Totals.NegativeRowsRemoved),
		"propagation-negative-terminal-rows-removed=" + strconv.Itoa(s.Totals.NegativeTerminalRowsRemoved),
		"propagation-beta-left-input-inserts=" + strconv.Itoa(s.Totals.BetaLeftInputInserts),
		"propagation-beta-right-input-inserts=" + strconv.Itoa(s.Totals.BetaRightInputInserts),
		"propagation-beta-bucket-probes=" + strconv.Itoa(s.Totals.BetaBucketProbes),
		"propagation-beta-join-index-hits=" + strconv.Itoa(s.Totals.BetaJoinIndexHits),
		"propagation-beta-join-index-misses=" + strconv.Itoa(s.Totals.BetaJoinIndexMisses),
		"propagation-beta-bucket-depth-total=" + strconv.Itoa(s.Totals.BetaBucketDepthTotal),
		"propagation-beta-bucket-depth-max=" + strconv.Itoa(s.Totals.BetaBucketDepthMax),
		"propagation-beta-bucket-depth-mean=" + s.betaBucketDepthMeanField(),
		"propagation-beta-candidate-rows-scanned=" + strconv.Itoa(s.Totals.BetaCandidateRowsScanned),
		"propagation-beta-residual-tests=" + strconv.Itoa(s.Totals.BetaResidualTests),
		"propagation-beta-residual-failures=" + strconv.Itoa(s.Totals.BetaResidualFailures),
		"propagation-beta-joined-tokens-produced=" + strconv.Itoa(s.Totals.BetaJoinedTokensProduced),
		"propagation-expression-predicate-tests=" + strconv.Itoa(s.Totals.ExpressionPredicateTests),
		"propagation-expression-predicate-failures=" + strconv.Itoa(s.Totals.ExpressionPredicateFailures),
		"propagation-expression-predicate-errors=" + strconv.Itoa(s.Totals.ExpressionPredicateErrors),
		"propagation-silent-evaluation-coercions=" + strconv.Itoa(s.Totals.SilentEvaluationCoercions),
		"propagation-function-calls=" + strconv.Itoa(s.Totals.FunctionCalls),
		"propagation-function-errors=" + strconv.Itoa(s.Totals.FunctionErrors),
		"propagation-function-cancellations=" + strconv.Itoa(s.Totals.FunctionCancellations),
		"propagation-nested-path-evaluations=" + strconv.Itoa(s.Totals.NestedPathEvaluations),
		"propagation-nested-path-misses=" + strconv.Itoa(s.Totals.NestedPathMisses),
		"propagation-rule-memories-visited/rhs-assert=" + s.perRHSAssertField(s.Totals.RuleMemoriesVisited),
		"propagation-conditions-tested/rhs-assert=" + s.perRHSAssertField(s.Totals.ConditionsTested),
		"propagation-alpha-matches-added/rhs-assert=" + s.perRHSAssertField(s.Totals.AlphaMatchesAdded),
		"propagation-alpha-index-probes/rhs-assert=" + s.perRHSAssertField(s.Totals.AlphaIndexProbes),
		"propagation-alpha-index-hits/rhs-assert=" + s.perRHSAssertField(s.Totals.AlphaIndexHits),
		"propagation-alpha-index-misses/rhs-assert=" + s.perRHSAssertField(s.Totals.AlphaIndexMisses),
		"propagation-alpha-index-fallback-scans/rhs-assert=" + s.perRHSAssertField(s.Totals.AlphaIndexFallbackScans),
		"propagation-condition-plans-tested/rhs-assert=" + s.perRHSAssertField(s.Totals.ConditionPlansTested),
		"propagation-condition-matches-added/rhs-assert=" + s.perRHSAssertField(s.Totals.ConditionMatchesAdded),
		"propagation-prefixes-added/rhs-assert=" + s.perRHSAssertField(s.Totals.PrefixesAdded),
		"propagation-beta-successors-reached/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaSuccessorsReached),
		"propagation-tokens-created/rhs-assert=" + s.perRHSAssertField(s.Totals.TokensCreated),
		"propagation-terminal-deltas-emitted/rhs-assert=" + s.perRHSAssertField(s.Totals.TerminalDeltasEmitted),
		"propagation-terminal-rows-inserted/rhs-assert=" + s.perRHSAssertField(s.Totals.TerminalRowsInserted),
		"propagation-terminal-rows-deduped/rhs-assert=" + s.perRHSAssertField(s.Totals.TerminalRowsDeduped),
		"propagation-terminal-rows-removed/rhs-assert=" + s.perRHSAssertField(s.Totals.TerminalRowsRemoved),
		"propagation-beta-left-input-inserts/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaLeftInputInserts),
		"propagation-beta-right-input-inserts/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaRightInputInserts),
		"propagation-beta-bucket-probes/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaBucketProbes),
		"propagation-beta-join-index-hits/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaJoinIndexHits),
		"propagation-beta-join-index-misses/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaJoinIndexMisses),
		"propagation-beta-bucket-depth-total/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaBucketDepthTotal),
		"propagation-beta-candidate-rows-scanned/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaCandidateRowsScanned),
		"propagation-beta-residual-tests/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaResidualTests),
		"propagation-beta-residual-failures/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaResidualFailures),
		"propagation-beta-joined-tokens-produced/rhs-assert=" + s.perRHSAssertField(s.Totals.BetaJoinedTokensProduced),
		"propagation-expression-predicate-tests/rhs-assert=" + s.perRHSAssertField(s.Totals.ExpressionPredicateTests),
		"propagation-expression-predicate-failures/rhs-assert=" + s.perRHSAssertField(s.Totals.ExpressionPredicateFailures),
		"propagation-expression-predicate-errors/rhs-assert=" + s.perRHSAssertField(s.Totals.ExpressionPredicateErrors),
		"propagation-nested-path-evaluations/rhs-assert=" + s.perRHSAssertField(s.Totals.NestedPathEvaluations),
		"propagation-nested-path-misses/rhs-assert=" + s.perRHSAssertField(s.Totals.NestedPathMisses),
		"propagation-by-template=" + s.templateSummary(),
		"propagation-by-origin=" + s.originSummary(),
		"propagation-branch-count=" + strconv.Itoa(len(s.ByBranch)),
	}
	if summary := s.templateOriginSummary(); summary != "" {
		fields = append(fields, "propagation-by-template-origin="+summary)
	}
	if summary := s.branchSummary(); summary != "" {
		fields = append(fields, "propagation-by-branch="+summary)
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

func (s propagationCounterSnapshot) unsupportedReasonSummary() string {
	if len(s.UnsupportedReasons) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(s.UnsupportedReasons))
	for key := range s.UnsupportedReasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strconv.Itoa(s.UnsupportedReasons[key]))
	}
	return strings.Join(parts, ";")
}

func (s propagationCounterSnapshot) perRHSAssertField(value int) string {
	rhsAsserts := max(1, s.Totals.RHSAsserts)
	return strconv.FormatFloat(float64(value)/float64(rhsAsserts), 'f', 3, 64)
}

func (s propagationCounterSnapshot) betaBucketDepthMeanField() string {
	probes := max(1, s.Totals.BetaBucketProbes)
	return strconv.FormatFloat(float64(s.Totals.BetaBucketDepthTotal)/float64(probes), 'f', 3, 64)
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

func (s propagationCounterSnapshot) branchSummary() string {
	if len(s.ByBranch) == 0 {
		return ""
	}
	keys := make([]propagationBranchKey, 0, len(s.ByBranch))
	for key := range s.ByBranch {
		keys = append(keys, key)
	}
	slicesSortBranchKeys(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		totals := s.ByBranch[key]
		parts = append(parts, formatPropagationDistributionEntry(key.String(), totals))
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
		"alpha-index-probes=" + strconv.Itoa(totals.AlphaIndexProbes) + "," +
		"alpha-index-hits=" + strconv.Itoa(totals.AlphaIndexHits) + "," +
		"alpha-index-misses=" + strconv.Itoa(totals.AlphaIndexMisses) + "," +
		"alpha-index-fallback-scans=" + strconv.Itoa(totals.AlphaIndexFallbackScans) + "," +
		"condition-plans-tested=" + strconv.Itoa(totals.ConditionPlansTested) + "," +
		"condition-matches-added=" + strconv.Itoa(totals.ConditionMatchesAdded) + "," +
		"prefixes-added=" + strconv.Itoa(totals.PrefixesAdded) + "," +
		"beta-successors-reached=" + strconv.Itoa(totals.BetaSuccessorsReached) + "," +
		"tokens-created=" + strconv.Itoa(totals.TokensCreated) + "," +
		"terminal-deltas-emitted=" + strconv.Itoa(totals.TerminalDeltasEmitted) + "," +
		"terminal-rows-inserted=" + strconv.Itoa(totals.TerminalRowsInserted) + "," +
		"terminal-rows-deduped=" + strconv.Itoa(totals.TerminalRowsDeduped) + "," +
		"terminal-rows-removed=" + strconv.Itoa(totals.TerminalRowsRemoved) + "," +
		"agenda-delta-applications=" + strconv.Itoa(totals.AgendaDeltaApplications) + "," +
		"activations-stored=" + strconv.Itoa(totals.ActivationsStored) + "," +
		"terminal-rows-inserted=" + strconv.Itoa(totals.TerminalRowsInserted) + "," +
		"terminal-rows-deduped=" + strconv.Itoa(totals.TerminalRowsDeduped) + "," +
		"terminal-rows-removed=" + strconv.Itoa(totals.TerminalRowsRemoved) + "," +
		"beta-left-input-inserts=" + strconv.Itoa(totals.BetaLeftInputInserts) + "," +
		"beta-right-input-inserts=" + strconv.Itoa(totals.BetaRightInputInserts) + "," +
		"beta-bucket-probes=" + strconv.Itoa(totals.BetaBucketProbes) + "," +
		"beta-join-index-hits=" + strconv.Itoa(totals.BetaJoinIndexHits) + "," +
		"beta-join-index-misses=" + strconv.Itoa(totals.BetaJoinIndexMisses) + "," +
		"beta-bucket-depth-total=" + strconv.Itoa(totals.BetaBucketDepthTotal) + "," +
		"beta-bucket-depth-max=" + strconv.Itoa(totals.BetaBucketDepthMax) + "," +
		"beta-candidate-rows-scanned=" + strconv.Itoa(totals.BetaCandidateRowsScanned) + "," +
		"beta-residual-tests=" + strconv.Itoa(totals.BetaResidualTests) + "," +
		"beta-residual-failures=" + strconv.Itoa(totals.BetaResidualFailures) + "," +
		"beta-joined-tokens-produced=" + strconv.Itoa(totals.BetaJoinedTokensProduced) + "," +
		"expression-predicate-tests=" + strconv.Itoa(totals.ExpressionPredicateTests) + "," +
		"expression-predicate-failures=" + strconv.Itoa(totals.ExpressionPredicateFailures) + "," +
		"expression-predicate-errors=" + strconv.Itoa(totals.ExpressionPredicateErrors) + "," +
		"function-calls=" + strconv.Itoa(totals.FunctionCalls) + "," +
		"function-errors=" + strconv.Itoa(totals.FunctionErrors) + "," +
		"function-cancellations=" + strconv.Itoa(totals.FunctionCancellations) + "," +
		"nested-path-evaluations=" + strconv.Itoa(totals.NestedPathEvaluations) + "," +
		"nested-path-misses=" + strconv.Itoa(totals.NestedPathMisses) + "}"
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

func slicesSortBranchKeys(keys []propagationBranchKey) {
	if len(keys) < 2 {
		return
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})
}
