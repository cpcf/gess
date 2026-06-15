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
//     agenda.reconcile, indexActivation, and activation index map growth.
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
		byFactID := make(map[FactID][]activationKey, len(activations))
		byRevision := make(map[RuleRevisionID][]activationKey, len(activations))
		for _, activation := range activations {
			indexActivation(byFactID, byRevision, activation)
		}
		benchmarkAgendaByFactID = byFactID
		benchmarkAgendaByRevision = byRevision
	}
	if len(benchmarkAgendaByFactID) == 0 || len(benchmarkAgendaByRevision) == 0 {
		b.Fatal("expected agenda indexes")
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
