package gess

import (
	"context"
	"errors"
	"fmt"
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
//   - AgendaTerminalTokenPopulation tracks initial full agenda population from
//     terminal tokens without candidate materialization.
//   - AgendaTerminalTokenCollection and AgendaTerminalTokenReconcile split that
//     path into collection and agenda materialization for scope comparison.
//   - ReteAgendaDelta tracks incremental assert, modify, and retract after an
//     agenda has been populated from Rete-generated activations.
//   - AgendaTerminalTokenDelta tracks direct agenda application of prebuilt
//     Rete terminal-token deltas without session setup dominating the result.
var (
	benchmarkSnapshot            Snapshot
	benchmarkActionContext       ActionContext
	benchmarkDuplicateKey        DuplicateKey
	benchmarkTemplateFields      Fields
	benchmarkTemplatePresence    map[string]FieldPresence
	benchmarkTemplateSlots       []factSlot
	benchmarkAgendaChanges       []agendaChange
	benchmarkTerminalTokenDeltas []reteTerminalTokenDelta
	benchmarkAgendaByFactID      map[FactID][]activationKey
	benchmarkAgendaByRevision    map[RuleRevisionID][]activationKey
	benchmarkAssertResult        AssertResult
	benchmarkModifyResult        ModifyResult
	benchmarkRetractResult       RetractResult
	benchmarkRunResult           RunResult
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

func BenchmarkRunActionOriginMultiDelta(b *testing.B) {
	revision := mustCompileActionOriginMultiDeltaRuleset(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		session := mustActionOriginMultiDeltaSession(b, revision)
		b.StartTimer()
		result, err := session.Run(ctx)
		b.StopTimer()
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 3 {
			b.Fatalf("run result = (%v, %d), want (%v, 3)", result.Status, result.Fired, RunCompleted)
		}
		if session.agendaDirty || !session.agendaReady || session.runAgendaPending {
			b.Fatalf("agenda state = dirty %v ready %v pending %v, want clean ready no pending", session.agendaDirty, session.agendaReady, session.runAgendaPending)
		}
		benchmarkRunResult = result
	}
}

func mustCompileActionOriginMultiDeltaRuleset(t testing.TB) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	trigger := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "trigger",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "person",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	audit := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "audit",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	obsolete := mustAddTemplate(t, workspace, TemplateSpec{
		Name:   "obsolete",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})

	mustAddAction(t, workspace, ActionSpec{
		Name: "assert-audit",
		Fn: func(ctx ActionContext) error {
			_, err := ctx.AssertTemplate(audit.Key(), Fields{"id": mustValue(t, "audit-1")})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "promote-person",
		Fn: func(ctx ActionContext) error {
			binding, ok := ctx.Binding("person")
			if !ok {
				return errors.New("missing person binding")
			}
			_, err := ctx.Modify(binding.ID(), FactPatch{
				Set: Fields{"status": mustValue(t, "done")},
			})
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{
		Name: "retire-obsolete",
		Fn: func(ctx ActionContext) error {
			binding, ok := ctx.Binding("obsolete")
			if !ok {
				return errors.New("missing obsolete binding")
			}
			_, err := ctx.Retract(binding.ID())
			return err
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "record", Fn: func(ActionContext) error { return nil }})

	mustAddRule(t, workspace, RuleSpec{
		Name:     "advance",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{Binding: "trigger", TemplateKey: trigger.Key()},
			{
				Binding:     "person",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
				},
			},
			{Binding: "obsolete", TemplateKey: obsolete.Key()},
		},
		Actions: []RuleActionSpec{{Name: "assert-audit"}, {Name: "promote-person"}, {Name: "retire-obsolete"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "audit-created",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", TemplateKey: audit.Key()},
		},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "person-done",
		Conditions: []RuleConditionSpec{
			{
				Binding:     "person",
				TemplateKey: person.Key(),
				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "done"},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "record"}},
	})

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}

func mustActionOriginMultiDeltaSession(t testing.TB, revision *Ruleset) *Session {
	t.Helper()

	session, err := NewSession(revision, WithInitialFacts(
		SessionInitialFact{
			TemplateKey: TemplateKey("trigger"),
			Fields:      Fields{"id": mustValue(t, "trigger-1")},
		},
		SessionInitialFact{
			TemplateKey: TemplateKey("person"),
			Fields: Fields{
				"id":     mustValue(t, "person-1"),
				"status": mustValue(t, "pending"),
			},
		},
		SessionInitialFact{
			TemplateKey: TemplateKey("obsolete"),
			Fields:      Fields{"id": mustValue(t, "obsolete-1")},
		},
	))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
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

func BenchmarkAgendaTerminalTokenPopulationLoan(b *testing.B) {
	session := mustLoanUnderwritingBenchmarkSession(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokens, ok, err := session.rete.currentTerminalTokenDeltas(context.Background())
		if err != nil {
			b.Fatalf("currentTerminalTokenDeltas: %v", err)
		}
		if !ok {
			b.Fatal("currentTerminalTokenDeltas unexpectedly unavailable for beta-backed session")
		}
		agenda := newAgenda()
		changes, err := agenda.reconcileTerminalTokens(context.Background(), session.revision, tokens)
		if err != nil {
			b.Fatalf("reconcileTerminalTokens: %v", err)
		}
		benchmarkAgendaChanges = changes
	}
	if len(benchmarkAgendaChanges) == 0 {
		b.Fatal("expected agenda changes")
	}
}

func BenchmarkAgendaTerminalTokenCollectionLoan(b *testing.B) {
	session := mustLoanUnderwritingBenchmarkSession(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokens, ok, err := session.rete.currentTerminalTokenDeltas(context.Background())
		if err != nil {
			b.Fatalf("currentTerminalTokenDeltas: %v", err)
		}
		if !ok {
			b.Fatal("currentTerminalTokenDeltas unexpectedly unavailable for beta-backed session")
		}
		benchmarkTerminalTokenDeltas = tokens
	}
	if len(benchmarkTerminalTokenDeltas) == 0 {
		b.Fatal("expected terminal token deltas")
	}
}

func BenchmarkAgendaTerminalTokenReconcileLoan(b *testing.B) {
	session := mustLoanUnderwritingBenchmarkSession(b)
	tokens, ok, err := session.rete.currentTerminalTokenDeltas(context.Background())
	if err != nil {
		b.Fatalf("currentTerminalTokenDeltas: %v", err)
	}
	if !ok {
		b.Fatal("currentTerminalTokenDeltas unexpectedly unavailable for beta-backed session")
	}
	seed := cloneTerminalTokenDeltas(tokens)
	working := make([]reteTerminalTokenDelta, len(seed))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(working, seed)
		agenda := newAgenda()
		changes, err := agenda.reconcileTerminalTokens(context.Background(), session.revision, working)
		if err != nil {
			b.Fatalf("reconcileTerminalTokens: %v", err)
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

func BenchmarkReteGraphModifyHeavy(b *testing.B) {
	revision, employeeKey, departmentKey := mustGraphRemovalBenchmarkRuleset(b)
	patch := FactPatch{Set: mustFields(b, map[string]any{"id": "Sales"})}
	const joinedTokens = 256

	b.ReportAllocs()
	b.ReportMetric(joinedTokens, "joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustGraphRemovalBenchmarkSession(b, revision, employeeKey, departmentKey, joinedTokens)
		department := mustBenchmarkSessionFactByTemplateAndField(b, session, departmentKey, "id", "Engineering")
		b.StartTimer()
		result, err := session.Modify(context.Background(), department.ID(), patch)
		b.StopTimer()
		if err != nil {
			b.Fatalf("Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
		}
		benchmarkModifyResult = result
	}
}

func BenchmarkReteGraphRetractHeavy(b *testing.B) {
	revision, employeeKey, departmentKey := mustGraphRemovalBenchmarkRuleset(b)
	const joinedTokens = 256

	b.ReportAllocs()
	b.ReportMetric(joinedTokens, "joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustGraphRemovalBenchmarkSession(b, revision, employeeKey, departmentKey, joinedTokens)
		department := mustBenchmarkSessionFactByTemplateAndField(b, session, departmentKey, "id", "Engineering")
		b.StartTimer()
		result, err := session.Retract(context.Background(), department.ID())
		b.StopTimer()
		if err != nil {
			b.Fatalf("Retract: %v", err)
		}
		if result.Status != RetractRemoved {
			b.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
		}
		benchmarkRetractResult = result
	}
}

func BenchmarkReteGraphRetractSparse(b *testing.B) {
	revision, employeeKey, departmentKey := mustGraphRemovalBenchmarkRuleset(b)
	const retainedPairs = 256

	b.ReportAllocs()
	b.ReportMetric(retainedPairs, "retained-pairs/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustGraphRemovalSparseBenchmarkSession(b, revision, employeeKey, departmentKey, retainedPairs)
		department := mustBenchmarkSessionFactByTemplateAndField(b, session, departmentKey, "id", "Engineering")
		b.StartTimer()
		result, err := session.Retract(context.Background(), department.ID())
		b.StopTimer()
		if err != nil {
			b.Fatalf("Retract: %v", err)
		}
		if result.Status != RetractRemoved {
			b.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
		}
		benchmarkRetractResult = result
	}
}

func BenchmarkReteGraphResidualJoinHighCollision(b *testing.B) {
	revision, thresholdKey, candidateKey := mustGraphResidualJoinBenchmarkRuleset(b)
	const thresholds = 256
	candidateFields := mustFields(b, map[string]any{"group": "A", "score": thresholds + 1})
	reportGraphResidualJoinBenchmarkCounters(b, revision, thresholdKey, candidateKey, thresholds, true, candidateFields)

	b.ReportAllocs()
	b.ReportMetric(thresholds, "thresholds/op")
	b.ReportMetric(thresholds, "expected-joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustGraphResidualJoinBenchmarkSession(b, revision, thresholdKey, thresholds, true)
		b.StartTimer()
		result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
		b.StopTimer()
		if err != nil {
			b.Fatalf("AssertTemplate candidate: %v", err)
		}
		if result.Status != AssertInserted {
			b.Fatalf("assert status = %v, want %v", result.Status, AssertInserted)
		}
		benchmarkAssertResult = result
	}
}

func BenchmarkReteGraphResidualJoinHighCollisionReject(b *testing.B) {
	revision, thresholdKey, candidateKey := mustGraphResidualJoinBenchmarkRuleset(b)
	const thresholds = 256
	candidateFields := mustFields(b, map[string]any{"group": "A", "score": -1})
	reportGraphResidualJoinBenchmarkCounters(b, revision, thresholdKey, candidateKey, thresholds, true, candidateFields)

	b.ReportAllocs()
	b.ReportMetric(thresholds, "thresholds/op")
	b.ReportMetric(0, "expected-joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustGraphResidualJoinBenchmarkSession(b, revision, thresholdKey, thresholds, true)
		b.StartTimer()
		result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
		b.StopTimer()
		if err != nil {
			b.Fatalf("AssertTemplate candidate: %v", err)
		}
		if result.Status != AssertInserted {
			b.Fatalf("assert status = %v, want %v", result.Status, AssertInserted)
		}
		benchmarkAssertResult = result
	}
}

func BenchmarkReteGraphResidualJoinSparseKey(b *testing.B) {
	revision, thresholdKey, candidateKey := mustGraphResidualJoinBenchmarkRuleset(b)
	const thresholds = 256
	candidateFields := mustFields(b, map[string]any{"group": fmt.Sprintf("G%03d", thresholds-1), "score": thresholds + 1})
	reportGraphResidualJoinBenchmarkCounters(b, revision, thresholdKey, candidateKey, thresholds, false, candidateFields)

	b.ReportAllocs()
	b.ReportMetric(thresholds, "thresholds/op")
	b.ReportMetric(1, "expected-joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustGraphResidualJoinBenchmarkSession(b, revision, thresholdKey, thresholds, false)
		b.StartTimer()
		result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
		b.StopTimer()
		if err != nil {
			b.Fatalf("AssertTemplate candidate: %v", err)
		}
		if result.Status != AssertInserted {
			b.Fatalf("assert status = %v, want %v", result.Status, AssertInserted)
		}
		benchmarkAssertResult = result
	}
}

func BenchmarkReteRuntimePureResidualJoinFallbackReconcile(b *testing.B) {
	revision, thresholdKey, candidateKey := mustPureResidualJoinBenchmarkRuleset(b)
	const thresholds = 256
	candidateFields := mustFields(b, map[string]any{"age": thresholds + 1})
	reportPureResidualJoinFallbackBenchmarkCounters(b, revision, thresholdKey, candidateKey, thresholds, candidateFields)

	b.ReportAllocs()
	b.ReportMetric(thresholds, "thresholds/op")
	b.ReportMetric(thresholds, "expected-joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustPureResidualJoinBenchmarkSession(b, revision, thresholdKey, thresholds)
		b.StartTimer()
		result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
		if err == nil {
			_, err = session.reconcileAgendaInternal(context.Background())
		}
		b.StopTimer()
		if err != nil {
			b.Fatalf("assert/reconcile candidate: %v", err)
		}
		if result.Status != AssertInserted {
			b.Fatalf("assert status = %v, want %v", result.Status, AssertInserted)
		}
		if got := len(session.agenda.pendingActivations()); got != thresholds {
			b.Fatalf("pending activations = %d, want %d", got, thresholds)
		}
		benchmarkAssertResult = result
	}
}

func TestReteGraphRemovalCountersUseIndexedRows(t *testing.T) {
	revision, employeeKey, departmentKey := mustGraphRemovalBenchmarkRuleset(t)
	const joinedTokens = 256

	session := mustGraphRemovalBenchmarkSession(t, revision, employeeKey, departmentKey, joinedTokens)
	department := mustBenchmarkSessionFactByTemplateAndField(t, session, departmentKey, "id", "Engineering")
	session.attachPropagationCounters()

	result, err := session.Retract(context.Background(), department.ID())
	if err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if result.Status != RetractRemoved {
		t.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
	}

	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		t.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if got := snapshot.Totals.TerminalDeltasRemoved; got != joinedTokens {
		t.Fatalf("terminal deltas removed = %d, want %d", got, joinedTokens)
	}
	if got, want := snapshot.Totals.RemovalRowsRemoved, joinedTokens+1; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
	if got := snapshot.Totals.RemovalRowsTouched; got < snapshot.Totals.RemovalRowsRemoved {
		t.Fatalf("removal rows touched = %d, want at least rows removed %d", got, snapshot.Totals.RemovalRowsRemoved)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 2; got != want {
		t.Fatalf("removal index lookups = %d, want topology-limited %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsTouched, joinedTokens+1; got != want {
		t.Fatalf("removal rows touched = %d, want %d", got, want)
	}
}

func mustGraphRemovalSparseBenchmarkSession(tb testing.TB, revision *Ruleset, employeeKey, departmentKey TemplateKey, retainedPairs int) *Session {
	tb.Helper()

	initials := make([]SessionInitialFact, 0, retainedPairs*2+4)
	for i := range retainedPairs {
		deptID := fmt.Sprintf("Dept-%03d", i)
		initials = append(initials,
			SessionInitialFact{
				TemplateKey: employeeKey,
				Fields: mustFields(tb, map[string]any{
					"name": fmt.Sprintf("employee-%03d", i),
					"dept": deptID,
				}),
			},
			SessionInitialFact{
				TemplateKey: departmentKey,
				Fields:      mustFields(tb, map[string]any{"id": deptID}),
			},
		)
	}
	initials = append(initials,
		SessionInitialFact{
			TemplateKey: employeeKey,
			Fields: mustFields(tb, map[string]any{
				"name": "sales-employee",
				"dept": "Sales",
			}),
		},
		SessionInitialFact{
			TemplateKey: departmentKey,
			Fields:      mustFields(tb, map[string]any{"id": "Sales"}),
		},
		SessionInitialFact{
			TemplateKey: employeeKey,
			Fields: mustFields(tb, map[string]any{
				"name": "target-employee",
				"dept": "Engineering",
			}),
		},
		SessionInitialFact{
			TemplateKey: departmentKey,
			Fields:      mustFields(tb, map[string]any{"id": "Engineering"}),
		},
	)

	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		tb.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		tb.Fatalf("Snapshot: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		tb.Fatalf("initial reconcileAgenda: %v", err)
	}
	if got, want := len(session.agenda.pendingActivations()), retainedPairs+2; got != want {
		tb.Fatalf("pending activations = %d, want %d", got, want)
	}
	return session
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

func mustGraphRemovalBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	employee := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "employee",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "department",
		Closed: true,
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", TemplateKey: employee.Key()},
			{
				Binding:     "department",
				TemplateKey: department.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), employee.Key(), department.Key()
}

func mustGraphRemovalBenchmarkSession(tb testing.TB, revision *Ruleset, employeeKey, departmentKey TemplateKey, employees int) *Session {
	tb.Helper()

	initials := make([]SessionInitialFact, 0, employees+1)
	for i := range employees {
		initials = append(initials, SessionInitialFact{
			TemplateKey: employeeKey,
			Fields: mustFields(tb, map[string]any{
				"name": fmt.Sprintf("employee-%03d", i),
				"dept": "Engineering",
			}),
		})
	}
	initials = append(initials, SessionInitialFact{
		TemplateKey: departmentKey,
		Fields:      mustFields(tb, map[string]any{"id": "Engineering"}),
	})

	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		tb.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	snapshot, err := session.Snapshot(context.Background())
	if err != nil {
		tb.Fatalf("Snapshot: %v", err)
	}
	if _, err := session.reconcileAgenda(context.Background(), snapshot); err != nil {
		tb.Fatalf("initial reconcileAgenda: %v", err)
	}
	if got := len(session.agenda.pendingActivations()); got != employees {
		tb.Fatalf("pending activations = %d, want %d", got, employees)
	}
	return session
}

func mustGraphResidualJoinBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	threshold := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "threshold",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "candidate",
		Closed: true,
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "candidate-above-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: threshold.Key()},
			{
				Binding:     "candidate",
				TemplateKey: candidate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "score"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), threshold.Key(), candidate.Key()
}

func mustGraphResidualJoinBenchmarkSession(tb testing.TB, revision *Ruleset, thresholdKey TemplateKey, thresholds int, highCollision bool) *Session {
	tb.Helper()

	initials := make([]SessionInitialFact, 0, thresholds)
	for i := range thresholds {
		group := "A"
		if !highCollision {
			group = fmt.Sprintf("G%03d", i)
		}
		initials = append(initials, SessionInitialFact{
			TemplateKey: thresholdKey,
			Fields: mustFields(tb, map[string]any{
				"group": group,
				"score": i,
			}),
		})
	}
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta == nil {
		tb.Fatalf("Rete runtime = %#v, want graph beta", session.rete)
	}
	return session
}

func reportGraphResidualJoinBenchmarkCounters(tb testing.TB, revision *Ruleset, thresholdKey, candidateKey TemplateKey, thresholds int, highCollision bool, candidateFields Fields) {
	tb.Helper()

	reporter, ok := tb.(interface {
		ReportMetric(float64, string)
	})
	if !ok {
		return
	}
	session := mustGraphResidualJoinBenchmarkSession(tb, revision, thresholdKey, thresholds, highCollision)
	session.attachPropagationCounters()
	result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
	if err != nil {
		tb.Fatalf("diagnostic AssertTemplate candidate: %v", err)
	}
	if result.Status != AssertInserted {
		tb.Fatalf("diagnostic assert status = %v, want %v", result.Status, AssertInserted)
	}
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		tb.Fatalf("diagnostic runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	probes := max(1, snapshot.Totals.BetaBucketProbes)
	reporter.ReportMetric(float64(snapshot.Totals.BetaBucketProbes), "diagnostic-beta-bucket-probes")
	reporter.ReportMetric(float64(snapshot.Totals.BetaBucketDepthTotal), "diagnostic-beta-bucket-depth-total")
	reporter.ReportMetric(float64(snapshot.Totals.BetaBucketDepthMax), "diagnostic-beta-bucket-depth-max")
	reporter.ReportMetric(float64(snapshot.Totals.BetaBucketDepthTotal)/float64(probes), "diagnostic-beta-bucket-depth-mean")
	reporter.ReportMetric(float64(snapshot.Totals.BetaCandidateRowsScanned), "diagnostic-beta-candidate-rows-scanned")
	reporter.ReportMetric(float64(snapshot.Totals.BetaResidualTests), "diagnostic-beta-residual-tests")
	reporter.ReportMetric(float64(snapshot.Totals.BetaResidualFailures), "diagnostic-beta-residual-failures")
	reporter.ReportMetric(float64(snapshot.Totals.BetaJoinedTokensProduced), "diagnostic-beta-joined-tokens-produced")
	reporter.ReportMetric(float64(snapshot.TerminalRowsRetained), "diagnostic-terminal-rows-retained")
}

func mustPureResidualJoinBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	threshold := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "threshold",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	candidate := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "candidate",
		Closed: true,
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "older-than-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", TemplateKey: threshold.Key()},
			{
				Binding:     "candidate",
				TemplateKey: candidate.Key(),
				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				},
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), threshold.Key(), candidate.Key()
}

func mustPureResidualJoinBenchmarkSession(tb testing.TB, revision *Ruleset, thresholdKey TemplateKey, thresholds int) *Session {
	tb.Helper()

	initials := make([]SessionInitialFact, 0, thresholds)
	for i := range thresholds {
		initials = append(initials, SessionInitialFact{
			TemplateKey: thresholdKey,
			Fields:      mustFields(tb, map[string]any{"age": i}),
		})
	}
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	if session.rete == nil || session.rete.graphBeta != nil || session.rete.plan.betaSupported {
		tb.Fatalf("Rete runtime = %#v, want beta-unsupported fallback", session.rete)
	}
	return session
}

func reportPureResidualJoinFallbackBenchmarkCounters(tb testing.TB, revision *Ruleset, thresholdKey, candidateKey TemplateKey, thresholds int, candidateFields Fields) {
	tb.Helper()

	reporter, ok := tb.(interface {
		ReportMetric(float64, string)
	})
	if !ok {
		return
	}
	session := mustPureResidualJoinBenchmarkSession(tb, revision, thresholdKey, thresholds)
	session.attachPropagationCounters()
	result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
	if err == nil {
		_, err = session.reconcileAgendaInternal(context.Background())
	}
	if err != nil {
		tb.Fatalf("diagnostic assert/reconcile candidate: %v", err)
	}
	if result.Status != AssertInserted {
		tb.Fatalf("diagnostic assert status = %v, want %v", result.Status, AssertInserted)
	}
	if got := len(session.agenda.pendingActivations()); got != thresholds {
		tb.Fatalf("diagnostic pending activations = %d, want %d", got, thresholds)
	}
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphAlpha {
		tb.Fatalf("diagnostic runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphAlpha)
	}
	reporter.ReportMetric(propagationRuntimePathMetric(snapshot.RuntimePath, propagationRuntimeGraphAlpha), "diagnostic-runtime-graph-alpha-only")
	reporter.ReportMetric(float64(len(snapshot.FallbackReasons)), "diagnostic-fallback-reason-count")
	reporter.ReportMetric(float64(snapshot.FallbackReasons[propagationFallbackBetaUnsupported]), "diagnostic-fallback-beta-unsupported")
	reporter.ReportMetric(float64(snapshot.Totals.ConditionsTested), "diagnostic-conditions-tested")
	reporter.ReportMetric(float64(snapshot.Totals.AlphaMatchesAdded), "diagnostic-alpha-matches-added")
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
