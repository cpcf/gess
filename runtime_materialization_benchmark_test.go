package gess

import (
	"context"
	"testing"
)

// These benchmarks are guardrails for P1C profile hotspots before changing
// runtime materialization internals:
//   - SnapshotConstruction tracks snapshotLockedWithOptions, cloneFields, and
//     snapshot index construction.
//   - ActionContextCreation tracks actionContextForActivation and binding
//     snapshot materialization.
//   - TemplateDefaultsValidationAndDuplicateKey tracks Template validation,
//     field/default copying, slot validation, and duplicate-key construction.
//   - AgendaReconcile and AgendaIndexInsertion track activation materialization,
//     agenda.reconcile, indexActivation, and cold activation index map growth.
//   - AgendaIndexRebuild tracks steady-state reuse of activation index storage.
//   - ReteAgendaDelta tracks incremental assert, modify, and retract after an
//     agenda has been populated from Rete-generated activations.
//   - AgendaTerminalTokenDelta tracks direct agenda application of prebuilt
//     Rete terminal-token deltas without session setup dominating the result.
var (
	benchmarkSnapshot         Snapshot
	benchmarkActionContext    ActionContext
	benchmarkDuplicateKey     DuplicateKey
	benchmarkTemplateFields   Fields
	benchmarkTemplatePresence map[string]FieldPresence
	benchmarkTemplateSlots    []factSlot
	benchmarkAgendaChanges    []agendaChange
	benchmarkAgendaByFactID   map[FactID][]activationKey
	benchmarkAgendaByRevision map[RuleRevisionID][]activationKey
	benchmarkAssertResult     AssertResult
	benchmarkModifyResult     ModifyResult
	benchmarkRetractResult    RetractResult
)

func BenchmarkSnapshotConstructionLoanPublic(b *testing.B) {
	session := mustLoanUnderwritingBenchmarkSession(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSnapshot = session.snapshotLocked()
	}
	if benchmarkSnapshot.Len() == 0 {
		b.Fatal("expected non-empty snapshot")
	}
}

func BenchmarkSnapshotConstructionLoanIndexed(b *testing.B) {
	session := mustLoanUnderwritingBenchmarkSession(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSnapshot = session.indexedSnapshotLocked()
	}
	if benchmarkSnapshot.Len() == 0 {
		b.Fatal("expected non-empty snapshot")
	}
}

func BenchmarkSnapshotConstructionClaimsPublic(b *testing.B) {
	session := mustClaimsTriageBenchmarkSession(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSnapshot = session.snapshotLocked()
	}
	if benchmarkSnapshot.Len() == 0 {
		b.Fatal("expected non-empty snapshot")
	}
}

func BenchmarkSnapshotConstructionClaimsIndexed(b *testing.B) {
	session := mustClaimsTriageBenchmarkSession(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkSnapshot = session.indexedSnapshotLocked()
	}
	if benchmarkSnapshot.Len() == 0 {
		b.Fatal("expected non-empty snapshot")
	}
}

func BenchmarkActionContextCreationLoan(b *testing.B) {
	session, activation := mustLoanUnderwritingActivation(b, "approve-prime-employed")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, err := session.actionContextForActivation(context.Background(), activation)
		if err != nil {
			b.Fatalf("actionContextForActivation: %v", err)
		}
		benchmarkActionContext = ctx
	}
	if benchmarkActionContext.ActivationID() != activation.id {
		b.Fatalf("activation context ID = %q, want %q", benchmarkActionContext.ActivationID(), activation.id)
	}
}

func BenchmarkActionContextCreationClaims(b *testing.B) {
	session, activation := mustClaimsTriageActivation(b, "escalate-fraud-watch")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, err := session.actionContextForActivation(context.Background(), activation)
		if err != nil {
			b.Fatalf("actionContextForActivation: %v", err)
		}
		benchmarkActionContext = ctx
	}
	if benchmarkActionContext.ActivationID() != activation.id {
		b.Fatalf("activation context ID = %q, want %q", benchmarkActionContext.ActivationID(), activation.id)
	}
}

func BenchmarkTemplateDefaultsValidationAndDuplicateKeyMap(b *testing.B) {
	template := mustBenchmarkTemplate(b)
	input := mustFields(b, map[string]any{
		"id":     "evt-1",
		"status": "pending",
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fields, presence, err := template.applyDefaultsAndValidate(input)
		if err != nil {
			b.Fatalf("applyDefaultsAndValidate: %v", err)
		}
		benchmarkTemplateFields = fields
		benchmarkTemplatePresence = presence
		benchmarkDuplicateKey = makeDuplicateKeyForValidatedFact(template.Name(), template, fields, nil)
	}
	if benchmarkDuplicateKey == "" {
		b.Fatal("expected duplicate key")
	}
}

func BenchmarkTemplateDefaultsValidationAndDuplicateKeySlots(b *testing.B) {
	template := mustBenchmarkTemplate(b)
	input := mustFields(b, map[string]any{
		"id":     "evt-1",
		"status": "pending",
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		slots, err := template.buildValidatedFieldSlots(input)
		if err != nil {
			b.Fatalf("buildValidatedFieldSlots: %v", err)
		}
		benchmarkTemplateSlots = slots
		benchmarkDuplicateKey = makeDuplicateKeyForValidatedFact(template.Name(), template, nil, slots)
	}
	if benchmarkDuplicateKey == "" {
		b.Fatal("expected duplicate key")
	}
}

func BenchmarkAgendaReconcileReteActivationsLoan(b *testing.B) {
	revision, results := mustLoanUnderwritingReteResults(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda := newAgenda()
		changes, err := agenda.reconcile(context.Background(), revision, results)
		if err != nil {
			b.Fatalf("reconcile: %v", err)
		}
		benchmarkAgendaChanges = changes
	}
	if len(benchmarkAgendaChanges) == 0 {
		b.Fatal("expected agenda changes")
	}
}

func BenchmarkAgendaReconcileReteActivationsClaims(b *testing.B) {
	revision, results := mustClaimsTriageReteResults(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda := newAgenda()
		changes, err := agenda.reconcile(context.Background(), revision, results)
		if err != nil {
			b.Fatalf("reconcile: %v", err)
		}
		benchmarkAgendaChanges = changes
	}
	if len(benchmarkAgendaChanges) == 0 {
		b.Fatal("expected agenda changes")
	}
}

func BenchmarkAgendaIndexInsertion(b *testing.B) {
	revision, results := mustClaimsTriageReteResults(b)
	activations := mustAgendaPendingActivations(b, revision, results)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda := &agenda{
			byFactID:   make(map[FactID][]activationKey, len(activations)),
			byRevision: make(map[RuleRevisionID][]activationKey, len(activations)),
		}
		for _, activation := range activations {
			agenda.indexActivation(activation)
		}
		benchmarkAgendaByFactID = agenda.byFactID
		benchmarkAgendaByRevision = agenda.byRevision
	}
	if len(benchmarkAgendaByFactID) == 0 || len(benchmarkAgendaByRevision) == 0 {
		b.Fatal("expected agenda indexes")
	}
}

func BenchmarkAgendaIndexRebuild(b *testing.B) {
	revision, results := mustClaimsTriageReteResults(b)
	activations := mustAgendaPendingActivations(b, revision, results)
	agenda := newAgenda()
	agenda.resetIndexesForRebuild()
	for _, activation := range activations {
		agenda.indexActivation(activation)
	}
	agenda.pruneEmptyIndexes()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda.resetIndexesForRebuild()
		for _, activation := range activations {
			agenda.indexActivation(activation)
		}
		agenda.pruneEmptyIndexes()
		benchmarkAgendaByFactID = agenda.byFactID
		benchmarkAgendaByRevision = agenda.byRevision
	}
	if len(benchmarkAgendaByFactID) == 0 || len(benchmarkAgendaByRevision) == 0 {
		b.Fatal("expected agenda indexes")
	}
}

func BenchmarkReteAgendaDeltaAssert(b *testing.B) {
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(b)
	initials := mustBetaMemoryInitialFacts(b, noiseKey, employeeKey, departmentKey)
	fields := mustFields(b, map[string]any{"name": "Ben", "dept": "Sales"})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustReteAgendaDeltaBenchmarkSession(b, revision, initials)
		result, err := session.AssertTemplate(context.Background(), employeeKey, fields)
		if err != nil {
			b.Fatalf("AssertTemplate: %v", err)
		}
		if result.Status != AssertInserted {
			b.Fatalf("assert status = %v, want %v", result.Status, AssertInserted)
		}
		benchmarkAssertResult = result
	}
}

func BenchmarkReteAgendaDeltaModify(b *testing.B) {
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(b)
	initials := mustBetaMemoryInitialFacts(b, noiseKey, employeeKey, departmentKey)
	patch := FactPatch{Set: mustFields(b, map[string]any{"dept": "Sales"})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustReteAgendaDeltaBenchmarkSession(b, revision, initials)
		employee := mustBenchmarkSessionFactByTemplateAndField(b, session, employeeKey, "name", "Ada")
		result, err := session.Modify(context.Background(), employee.ID(), patch)
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteAgendaDeltaRetract(b *testing.B) {
	revision, noiseKey, employeeKey, departmentKey := mustBetaMemoryRuleset(b)
	initials := mustBetaMemoryInitialFacts(b, noiseKey, employeeKey, departmentKey)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustReteAgendaDeltaBenchmarkSession(b, revision, initials)
		department := mustBenchmarkSessionFactByTemplateAndField(b, session, departmentKey, "id", "Engineering")
		result, err := session.Retract(context.Background(), department.ID())
		if err != nil {
			b.Fatalf("Retract: %v", err)
		}
		if result.Status != RetractRemoved {
			b.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
		}
		benchmarkRetractResult = result
	}
}

func BenchmarkAgendaTerminalTokenDeltaAssert(b *testing.B) {
	fixture := mustTerminalTokenDeltaBenchmarkFixture(b)
	agenda := newAgenda()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda.reset()
		changes, err := agenda.applyTerminalTokenDeltas(context.Background(), fixture.revision, nil, fixture.assertDelta.added)
		if err != nil {
			b.Fatalf("apply assert terminal delta: %v", err)
		}
		if got, want := len(changes), 1; got != want {
			b.Fatalf("assert terminal changes = %d, want %d", got, want)
		}
		benchmarkAgendaChanges = changes
	}
}

func BenchmarkAgendaTerminalTokenDeltaModify(b *testing.B) {
	fixture := mustTerminalTokenDeltaBenchmarkFixture(b)
	agenda := newAgenda()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda.reset()
		if _, err := agenda.applyTerminalTokenDeltas(context.Background(), fixture.revision, nil, fixture.assertDelta.added); err != nil {
			b.Fatalf("seed terminal delta: %v", err)
		}
		changes, err := agenda.applyTerminalTokenDeltas(context.Background(), fixture.revision, fixture.modifyDelta.removed, fixture.modifyDelta.added)
		if err != nil {
			b.Fatalf("apply modify terminal delta: %v", err)
		}
		if got, want := len(changes), 2; got != want {
			b.Fatalf("modify terminal changes = %d, want %d", got, want)
		}
		benchmarkAgendaChanges = changes
	}
}

func BenchmarkAgendaTerminalTokenDeltaRetract(b *testing.B) {
	fixture := mustTerminalTokenDeltaBenchmarkFixture(b)
	agenda := newAgenda()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda.reset()
		if _, err := agenda.applyTerminalTokenDeltas(context.Background(), fixture.revision, nil, fixture.modifyDelta.added); err != nil {
			b.Fatalf("seed terminal delta: %v", err)
		}
		changes, err := agenda.applyTerminalTokenDeltas(context.Background(), fixture.revision, fixture.retractDelta.removed, nil)
		if err != nil {
			b.Fatalf("apply retract terminal delta: %v", err)
		}
		if got, want := len(changes), 1; got != want {
			b.Fatalf("retract terminal changes = %d, want %d", got, want)
		}
		benchmarkAgendaChanges = changes
	}
}

func mustBenchmarkTemplate(tb testing.TB) Template {
	tb.Helper()

	workspace := NewWorkspace()
	return mustAddTemplate(tb, workspace, TemplateSpec{
		Name:              "event",
		Closed:            true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"id"},
		Fields: []FieldSpec{
			{Name: "count", Kind: ValueInt, Default: 1},
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Default: "active", AllowedValues: []any{"active", "pending"}},
		},
	})
}

func mustClaimsTriageBenchmarkSession(tb testing.TB) *Session {
	tb.Helper()

	revision := mustCompileClaimsTriageRuleset(tb, nil)
	return mustLoadedBenchmarkSession(tb, revision, "claims-triage-benchmark-session", claimsTriageInitialFacts(tb, claimsTriageBenchmarkFactCount))
}

func mustLoanUnderwritingBenchmarkSession(tb testing.TB) *Session {
	tb.Helper()

	revision := mustCompileLoanUnderwritingRuleset(tb, nil)
	return mustLoadedBenchmarkSession(tb, revision, "loan-underwriting-benchmark-session", loanUnderwritingInitialFacts(tb))
}

func mustLoadedBenchmarkSession(tb testing.TB, revision *Ruleset, id SessionID, facts []SessionInitialFact) *Session {
	tb.Helper()

	session := mustSession(tb, revision, id)
	for _, fact := range facts {
		if fact.TemplateKey != "" {
			if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
				tb.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
			}
			continue
		}
		if _, err := session.Assert(context.Background(), fact.Name, fact.Fields); err != nil {
			tb.Fatalf("Assert(%s): %v", fact.Name, err)
		}
	}
	return session
}

func mustReteAgendaDeltaBenchmarkSession(tb testing.TB, revision *Ruleset, facts []SessionInitialFact) *Session {
	tb.Helper()

	session, err := NewSession(revision, WithInitialFacts(facts...))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || !session.rete.supportsIncrementalAgenda() {
		tb.Fatalf("Rete runtime = %#v, want incremental agenda support", session.rete)
	}
	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		tb.Fatalf("Snapshot: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		tb.Fatalf("initial reconcileAgenda: %v", err)
	}
	return session
}

type terminalTokenDeltaBenchmarkFixture struct {
	revision     *Ruleset
	assertDelta  reteAgendaDelta
	modifyDelta  reteAgendaDelta
	retractDelta reteAgendaDelta
}

func mustTerminalTokenDeltaBenchmarkFixture(tb testing.TB) terminalTokenDeltaBenchmarkFixture {
	tb.Helper()

	revision, templateKey := mustAgendaRevision(tb, 10)
	session := mustSession(tb, revision, "terminal-token-delta-benchmark-session")
	asserted, assertDelta, err := session.insertFactImmediate(context.Background(), "", templateKey, mustFields(tb, map[string]any{
		"name": "Ada",
	}), mutationOrigin{})
	if err != nil {
		tb.Fatalf("insertFactImmediate: %v", err)
	}
	_, modifyDelta, err := session.modifyImmediate(context.Background(), asserted.Fact.ID(), FactPatch{
		Set: mustFields(tb, map[string]any{"name": "Grace"}),
	}, mutationOrigin{})
	if err != nil {
		tb.Fatalf("modifyImmediate: %v", err)
	}
	_, retractDelta, err := session.retractImmediate(context.Background(), asserted.Fact.ID(), mutationOrigin{})
	if err != nil {
		tb.Fatalf("retractImmediate: %v", err)
	}
	return terminalTokenDeltaBenchmarkFixture{
		revision:     revision,
		assertDelta:  assertDelta,
		modifyDelta:  modifyDelta,
		retractDelta: retractDelta,
	}
}

func mustBenchmarkSessionFactByTemplateAndField(tb testing.TB, session *Session, templateKey TemplateKey, field string, want any) FactSnapshot {
	tb.Helper()

	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		tb.Fatalf("Snapshot: %v", err)
	}
	expected := mustValue(tb, want)
	for _, fact := range snapshot.Facts() {
		if fact.TemplateKey() != templateKey {
			continue
		}
		got, ok := fact.Field(field)
		if !ok {
			continue
		}
		if got.Equal(expected) {
			return fact
		}
	}
	tb.Fatalf("fact not found for template %q field %q = %v", templateKey, field, want)
	return FactSnapshot{}
}

func mustClaimsTriageReteResults(tb testing.TB) (*Ruleset, []ruleMatchResult) {
	tb.Helper()

	session := mustClaimsTriageBenchmarkSession(tb)
	revision := session.revision
	results := mustBenchmarkReteResults(tb, session)
	return revision, results
}

func mustLoanUnderwritingReteResults(tb testing.TB) (*Ruleset, []ruleMatchResult) {
	tb.Helper()

	session := mustLoanUnderwritingBenchmarkSession(tb)
	revision := session.revision
	results := mustBenchmarkReteResults(tb, session)
	return revision, results
}

func mustBenchmarkReteResults(tb testing.TB, session *Session) []ruleMatchResult {
	tb.Helper()

	if session.rete == nil {
		tb.Fatal("session Rete runtime is nil")
	}

	results, err := session.rete.match(context.Background(), session.indexedSnapshotLocked())
	if err != nil {
		tb.Fatalf("match: %v", err)
	}
	if len(results) == 0 {
		tb.Fatal("expected Rete-generated activations")
	}
	return results
}

func mustAgendaPendingActivations(tb testing.TB, revision *Ruleset, results []ruleMatchResult) []activation {
	tb.Helper()

	agenda := newAgenda()
	changes, err := agenda.reconcile(context.Background(), revision, results)
	if err != nil {
		tb.Fatalf("reconcile: %v", err)
	}
	if len(changes) == 0 {
		tb.Fatal("expected agenda changes")
	}
	activations := agenda.pendingActivations()
	if len(activations) == 0 {
		tb.Fatal("expected pending activations")
	}
	return activations
}

func mustClaimsTriageActivation(tb testing.TB, ruleName string) (*Session, activation) {
	tb.Helper()

	session := mustClaimsTriageBenchmarkSession(tb)
	revision := session.revision
	rule, ok := revision.Rule(ruleName)
	if !ok {
		tb.Fatalf("missing rule %q", ruleName)
	}

	activations := mustAgendaPendingActivations(tb, revision, mustBenchmarkReteResults(tb, session))
	for _, activation := range activations {
		if activation.ruleRevisionID == rule.RevisionID() {
			return session, activation
		}
	}
	tb.Fatalf("missing activation for rule %q", ruleName)
	return nil, activation{}
}

func mustLoanUnderwritingActivation(tb testing.TB, ruleName string) (*Session, activation) {
	tb.Helper()

	session := mustLoanUnderwritingBenchmarkSession(tb)
	revision := session.revision
	rule, ok := revision.Rule(ruleName)
	if !ok {
		tb.Fatalf("missing rule %q", ruleName)
	}

	activations := mustAgendaPendingActivations(tb, revision, mustBenchmarkReteResults(tb, session))
	for _, activation := range activations {
		if activation.ruleRevisionID == rule.RevisionID() {
			return session, activation
		}
	}
	tb.Fatalf("missing activation for rule %q", ruleName)
	return nil, activation{}
}
