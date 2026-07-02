package engine

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
//   - AgendaReconcile and AgendaInspectionScan track activation materialization,
//     agenda.reconcile, and scan-based agenda inspection.
//   - ReteAgendaDelta tracks incremental assert, modify, and retract after an
//     agenda has been populated from Rete-generated activations.
//   - AgendaTerminalTokenDelta tracks direct agenda application of prebuilt
//     Rete terminal-token deltas without session setup dominating the result.
var (
	benchmarkSnapshot          Snapshot
	benchmarkActionContext     ActionContext
	benchmarkDuplicateKey      DuplicateKey
	benchmarkTemplateFields    Fields
	benchmarkTemplatePresence  map[string]FieldPresence
	benchmarkTemplateSlots     []factSlot
	benchmarkAgendaChanges     []agendaChange
	benchmarkAgendaActivations []activation
	benchmarkAssertResult      AssertResult
	benchmarkModifyResult      ModifyResult
	benchmarkRetractResult     RetractResult
	benchmarkRunResult         RunResult
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
	if benchmarkActionContext.ActivationID() != activation.activationID() {
		b.Fatalf("activation context ID = %q, want %q", benchmarkActionContext.ActivationID(), activation.activationID())
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
	if benchmarkActionContext.ActivationID() != activation.activationID() {
		b.Fatalf("activation context ID = %q, want %q", benchmarkActionContext.ActivationID(), activation.activationID())
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
		Name: "trigger",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	person := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "person",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
		},
	})
	audit := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "audit",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
		},
	})
	obsolete := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "obsolete",
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
			{Binding: "trigger", Target: TemplateKeyFact(trigger.Key())},
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "pending"},
				}, Target: TemplateKeyFact(person.Key()),
			},
			{Binding: "obsolete", Target: TemplateKeyFact(obsolete.Key())},
		},
		Actions: []RuleActionSpec{{Name: "assert-audit"}, {Name: "promote-person"}, {Name: "retire-obsolete"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "audit-created",
		Conditions: []RuleConditionSpec{
			{Binding: "audit", Target: TemplateKeyFact(audit.Key())},
		},
		Actions: []RuleActionSpec{{Name: "record"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "person-done",
		Conditions: []RuleConditionSpec{
			{
				Binding: "person",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "done"},
				}, Target: TemplateKeyFact(person.Key()),
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

func BenchmarkAgendaInspectionScan(b *testing.B) {
	revision, results := mustClaimsTriageReteResults(b)
	activations := mustAgendaPendingActivations(b, revision, results)
	factID := activations[0].factIDs()[0]
	revisionID := activations[0].ruleRevisionID

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agenda := newAgenda()
		for _, activation := range activations {
			key := agenda.storeActivation(&activation)
			if stored, ok := agenda.activationByKeyPtr(key); ok {
				agenda.enqueueActivation(stored)
			}
		}
		benchmarkAgendaActivations = agenda.activationsByFactID(factID)
		benchmarkAgendaActivations = append(benchmarkAgendaActivations, agenda.activationsByRuleRevisionID(revisionID)...)
	}
	if len(benchmarkAgendaActivations) == 0 {
		b.Fatal("expected agenda inspection activations")
	}
}

func BenchmarkAgendaInspectionRepeatedScan(b *testing.B) {
	revision, results := mustClaimsTriageReteResults(b)
	activations := mustAgendaPendingActivations(b, revision, results)
	agenda := newAgenda()
	for _, activation := range activations {
		key := agenda.storeActivation(&activation)
		if stored, ok := agenda.activationByKeyPtr(key); ok {
			agenda.enqueueActivation(stored)
		}
	}
	factID := activations[0].factIDs()[0]
	revisionID := activations[0].ruleRevisionID

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkAgendaActivations = agenda.activationsByFactID(factID)
		benchmarkAgendaActivations = append(benchmarkAgendaActivations, agenda.activationsByRuleRevisionID(revisionID)...)
	}
	if len(benchmarkAgendaActivations) == 0 {
		b.Fatal("expected agenda inspection activations")
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

func BenchmarkReteGraphNegativePropagationRetractHeavy(b *testing.B) {
	revision, keys := mustGraphNegativePropagationBenchmarkRuleset(b)
	cases := graphNegativePropagationBenchmarkCases()

	for _, tc := range cases {
		name := fmt.Sprintf("beta-depth=%d/retained-groups=%d/affected-terminal=%d", tc.betaDepth, tc.retainedGroups, tc.affectedTerminalRows())
		b.Run(name, func(b *testing.B) {
			reportGraphNegativePropagationBenchmarkCounters(b, revision, keys, tc, graphNegativePropagationRetract)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.betaDepth), "beta-depth")
			b.ReportMetric(float64(tc.retainedGroups), "retained-groups")
			b.ReportMetric(float64(tc.affectedTerminalRows()), "affected-terminal-rows/op")
			b.StopTimer()
			for i := 0; i < b.N; i++ {
				session := mustGraphNegativePropagationBenchmarkSession(b, revision, keys, tc)
				target := mustGraphNegativePropagationTargetFact(b, session, keys, tc)
				b.StartTimer()
				result, err := session.Retract(context.Background(), target.ID())
				b.StopTimer()
				if err != nil {
					b.Fatalf("Retract: %v", err)
				}
				if result.Status != RetractRemoved {
					b.Fatalf("retract status = %v, want %v", result.Status, RetractRemoved)
				}
				benchmarkRetractResult = result
			}
		})
	}
}

func BenchmarkReteGraphNegativePropagationModifyHeavy(b *testing.B) {
	revision, keys := mustGraphNegativePropagationBenchmarkRuleset(b)
	cases := graphNegativePropagationBenchmarkCases()
	patch := FactPatch{Set: mustFields(b, map[string]any{"group": graphNegativePropagationDestinationGroup})}

	for _, tc := range cases {
		name := fmt.Sprintf("beta-depth=%d/retained-groups=%d/affected-terminal=%d", tc.betaDepth, tc.retainedGroups, tc.affectedTerminalRows())
		b.Run(name, func(b *testing.B) {
			reportGraphNegativePropagationBenchmarkCounters(b, revision, keys, tc, graphNegativePropagationModify)

			b.ReportAllocs()
			b.ReportMetric(float64(tc.betaDepth), "beta-depth")
			b.ReportMetric(float64(tc.retainedGroups), "retained-groups")
			b.ReportMetric(float64(tc.affectedTerminalRows()), "affected-terminal-rows/op")
			b.StopTimer()
			for i := 0; i < b.N; i++ {
				session := mustGraphNegativePropagationBenchmarkSession(b, revision, keys, tc)
				target := mustGraphNegativePropagationTargetFact(b, session, keys, tc)
				b.StartTimer()
				result, err := session.Modify(context.Background(), target.ID(), patch)
				b.StopTimer()
				if err != nil {
					b.Fatalf("Modify: %v", err)
				}
				if result.Status != ModifyChanged {
					b.Fatalf("modify status = %v, want %v", result.Status, ModifyChanged)
				}
				benchmarkModifyResult = result
			}
		})
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

func BenchmarkReteGraphCompoundEqualityResidualJoin(b *testing.B) {
	revision, thresholdKey, candidateKey := mustCompoundEqualityResidualJoinBenchmarkRuleset(b)
	const thresholds = 256
	candidateFields := mustFields(b, map[string]any{
		"group":  "A",
		"region": fmt.Sprintf("R%03d", thresholds-1),
		"meta":   map[string]any{"id": fmt.Sprintf("T%03d", thresholds-1)},
		"score":  thresholds + 1,
	})
	reportCompoundEqualityResidualJoinBenchmarkCounters(b, revision, thresholdKey, candidateKey, thresholds, candidateFields)

	b.ReportAllocs()
	b.ReportMetric(thresholds, "thresholds/op")
	b.ReportMetric(1, "expected-joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustCompoundEqualityResidualJoinBenchmarkSession(b, revision, thresholdKey, thresholds)
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

func BenchmarkReteGraphPureResidualJoin(b *testing.B) {
	revision, thresholdKey, candidateKey := mustPureResidualJoinBenchmarkRuleset(b)
	const thresholds = 256
	candidateFields := mustFields(b, map[string]any{"age": thresholds + 1})
	reportPureResidualJoinBenchmarkCounters(b, revision, thresholdKey, candidateKey, thresholds, candidateFields)

	b.ReportAllocs()
	b.ReportMetric(thresholds, "thresholds/op")
	b.ReportMetric(thresholds, "expected-joined-tokens/op")
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustPureResidualJoinBenchmarkSession(b, revision, thresholdKey, thresholds)
		b.StartTimer()
		result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
		b.StopTimer()
		if err != nil {
			b.Fatalf("AssertTemplate candidate: %v", err)
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
	if got, want := snapshot.Totals.RemovalRowsRemoved, 1; got != want {
		t.Fatalf("removal rows removed = %d, want %d", got, want)
	}
	if got := snapshot.Totals.RemovalRowsTouched; got < snapshot.Totals.RemovalRowsRemoved {
		t.Fatalf("removal rows touched = %d, want at least rows removed %d", got, snapshot.Totals.RemovalRowsRemoved)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, 1; got != want {
		t.Fatalf("removal index lookups = %d, want affected-token-limited %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsTouched, 1; got != want {
		t.Fatalf("removal rows touched = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativePropagationEvents, joinedTokens+1; got != want {
		t.Fatalf("negative propagation events = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeRowsRemoved, 1; got != want {
		t.Fatalf("negative rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeTerminalRowsRemoved, joinedTokens; got != want {
		t.Fatalf("negative terminal rows removed = %d, want %d", got, want)
	}
}

func TestReteGraphNegativePropagationHeavyCounters(t *testing.T) {
	revision, keys := mustGraphNegativePropagationBenchmarkRuleset(t)
	cases := graphNegativePropagationBenchmarkCases()
	for _, operation := range []graphNegativePropagationOperation{graphNegativePropagationRetract, graphNegativePropagationModify} {
		for _, tc := range cases {
			name := fmt.Sprintf("%s/beta-depth=%d", operation, tc.betaDepth)
			t.Run(name, func(t *testing.T) {
				session := mustGraphNegativePropagationBenchmarkSession(t, revision, keys, tc)
				snapshot := runGraphNegativePropagationBenchmarkMutation(t, session, keys, tc, operation)
				assertGraphNegativePropagationCounterSnapshot(t, snapshot, tc, operation)
			})
		}
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
		Name: "employee",
		Fields: []FieldSpec{
			{Name: "name", Kind: ValueString, Required: true},
			{Name: "dept", Kind: ValueString, Required: true},
		},
	})
	department := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "department",
		Fields: []FieldSpec{{Name: "id", Kind: ValueString, Required: true}},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "employee-department",
		Conditions: []RuleConditionSpec{
			{Binding: "employee", Target: TemplateKeyFact(employee.Key())},
			{
				Binding: "department",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "employee", Field: "dept"}},
				}, Target: TemplateKeyFact(department.Key()),
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

const (
	graphNegativePropagationTargetGroup      = "target"
	graphNegativePropagationDestinationGroup = "destination"
)

type graphNegativePropagationOperation string

const (
	graphNegativePropagationRetract graphNegativePropagationOperation = "retract"
	graphNegativePropagationModify  graphNegativePropagationOperation = "modify"
)

type graphNegativePropagationKeys struct {
	root   TemplateKey
	level1 TemplateKey
	level2 TemplateKey
	level3 TemplateKey
}

func (k graphNegativePropagationKeys) keyAtDepth(depth int) TemplateKey {
	switch depth {
	case 0:
		return k.root
	case 1:
		return k.level1
	case 2:
		return k.level2
	case 3:
		return k.level3
	default:
		return ""
	}
}

type graphNegativePropagationBenchmarkCase struct {
	betaDepth      int
	level1Width    int
	level2Width    int
	level3Width    int
	retainedGroups int
}

func graphNegativePropagationBenchmarkCases() []graphNegativePropagationBenchmarkCase {
	return []graphNegativePropagationBenchmarkCase{
		{betaDepth: 0, level1Width: 3, level2Width: 5, level3Width: 7, retainedGroups: 256},
		{betaDepth: 1, level1Width: 3, level2Width: 5, level3Width: 7, retainedGroups: 256},
		{betaDepth: 2, level1Width: 3, level2Width: 5, level3Width: 7, retainedGroups: 256},
		{betaDepth: 3, level1Width: 3, level2Width: 5, level3Width: 7, retainedGroups: 256},
	}
}

func (tc graphNegativePropagationBenchmarkCase) affectedTerminalRows() int {
	switch tc.betaDepth {
	case 0:
		return tc.level1Width * tc.level2Width * tc.level3Width
	case 1:
		return tc.level2Width * tc.level3Width
	case 2:
		return tc.level1Width * tc.level3Width
	case 3:
		return tc.level1Width * tc.level2Width
	default:
		return 0
	}
}

func (tc graphNegativePropagationBenchmarkCase) affectedBetaRows() int {
	switch tc.betaDepth {
	case 0:
		return 1 + tc.level1Width + tc.level1Width*tc.level2Width
	case 1:
		return 2 + tc.level2Width
	case 2:
		return 1 + tc.level1Width
	case 3:
		return 1
	default:
		return 0
	}
}

func (tc graphNegativePropagationBenchmarkCase) removalRowsTouched() int {
	touched := tc.affectedBetaRows() + tc.affectedTerminalRows()
	switch tc.betaDepth {
	case 0:
		touched += 2 * tc.level1Width * tc.level2Width
	case 1:
		touched += 4 * tc.level2Width
	}
	return touched
}

func (tc graphNegativePropagationBenchmarkCase) initialTerminalRows() int {
	return 2*tc.level1Width*tc.level2Width*tc.level3Width + tc.retainedGroups
}

func mustGraphNegativePropagationBenchmarkRuleset(tb testing.TB) (*Ruleset, graphNegativePropagationKeys) {
	tb.Helper()

	workspace := NewWorkspace()
	root := mustAddGraphNegativePropagationTemplate(tb, workspace, "graph-removal-root")
	level1 := mustAddGraphNegativePropagationTemplate(tb, workspace, "graph-removal-level1")
	level2 := mustAddGraphNegativePropagationTemplate(tb, workspace, "graph-removal-level2")
	level3 := mustAddGraphNegativePropagationTemplate(tb, workspace, "graph-removal-level3")
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "graph-removal-chain",
		Conditions: []RuleConditionSpec{
			{Binding: "root", Target: TemplateKeyFact(root.Key())},
			{
				Binding: "level1",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "root", Field: "group"}},
				}, Target: TemplateKeyFact(level1.Key()),
			},
			{
				Binding: "level2",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "level1", Field: "group"}},
				}, Target: TemplateKeyFact(level2.Key()),
			},
			{
				Binding: "level3",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "level2", Field: "group"}},
				}, Target: TemplateKeyFact(level3.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), graphNegativePropagationKeys{
		root:   root.Key(),
		level1: level1.Key(),
		level2: level2.Key(),
		level3: level3.Key(),
	}
}

func mustAddGraphNegativePropagationTemplate(tb testing.TB, workspace *Workspace, name string) Template {
	tb.Helper()

	return mustAddTemplate(tb, workspace, TemplateSpec{
		Name: name,
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "group", Kind: ValueString, Required: true},
		},
	})
}

func mustGraphNegativePropagationBenchmarkSession(tb testing.TB, revision *Ruleset, keys graphNegativePropagationKeys, tc graphNegativePropagationBenchmarkCase) *Session {
	tb.Helper()

	initials := graphNegativePropagationInitialFacts(tb, keys, tc)
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
	if got, want := len(session.agenda.pendingActivations()), tc.initialTerminalRows(); got != want {
		tb.Fatalf("pending activations = %d, want %d", got, want)
	}
	return session
}

func graphNegativePropagationInitialFacts(tb testing.TB, keys graphNegativePropagationKeys, tc graphNegativePropagationBenchmarkCase) []SessionInitialFact {
	tb.Helper()

	initials := make([]SessionInitialFact, 0, 2*(2+tc.level1Width+tc.level2Width+tc.level3Width)+tc.retainedGroups*4)
	initials = appendGraphNegativePropagationGroupFacts(tb, initials, keys, graphNegativePropagationTargetGroup, "target", tc.level1Width, tc.level2Width, tc.level3Width)
	initials = appendGraphNegativePropagationGroupFacts(tb, initials, keys, graphNegativePropagationDestinationGroup, "destination", tc.level1Width, tc.level2Width, tc.level3Width)
	for i := range tc.retainedGroups {
		group := fmt.Sprintf("retained-%03d", i)
		initials = appendGraphNegativePropagationGroupFacts(tb, initials, keys, group, group, 1, 1, 1)
	}
	return initials
}

func appendGraphNegativePropagationGroupFacts(tb testing.TB, initials []SessionInitialFact, keys graphNegativePropagationKeys, group, idPrefix string, level1Width, level2Width, level3Width int) []SessionInitialFact {
	tb.Helper()

	initials = append(initials, graphNegativePropagationInitialFact(tb, keys.root, idPrefix+"-root-000", group))
	for i := range level1Width {
		initials = append(initials, graphNegativePropagationInitialFact(tb, keys.level1, fmt.Sprintf("%s-level1-%03d", idPrefix, i), group))
	}
	for i := range level2Width {
		initials = append(initials, graphNegativePropagationInitialFact(tb, keys.level2, fmt.Sprintf("%s-level2-%03d", idPrefix, i), group))
	}
	for i := range level3Width {
		initials = append(initials, graphNegativePropagationInitialFact(tb, keys.level3, fmt.Sprintf("%s-level3-%03d", idPrefix, i), group))
	}
	return initials
}

func graphNegativePropagationInitialFact(tb testing.TB, templateKey TemplateKey, id, group string) SessionInitialFact {
	tb.Helper()

	return SessionInitialFact{
		TemplateKey: templateKey,
		Fields: mustFields(tb, map[string]any{
			"id":    id,
			"group": group,
		}),
	}
}

func mustGraphNegativePropagationTargetFact(tb testing.TB, session *Session, keys graphNegativePropagationKeys, tc graphNegativePropagationBenchmarkCase) FactSnapshot {
	tb.Helper()

	key := keys.keyAtDepth(tc.betaDepth)
	if key == "" {
		tb.Fatalf("unsupported beta depth %d", tc.betaDepth)
	}
	id := "target-root-000"
	if tc.betaDepth > 0 {
		id = fmt.Sprintf("target-level%d-000", tc.betaDepth)
	}
	return mustBenchmarkSessionFactByTemplateAndField(tb, session, key, "id", id)
}

func reportGraphNegativePropagationBenchmarkCounters(tb testing.TB, revision *Ruleset, keys graphNegativePropagationKeys, tc graphNegativePropagationBenchmarkCase, operation graphNegativePropagationOperation) {
	tb.Helper()

	reporter, ok := tb.(interface {
		ReportMetric(float64, string)
	})
	if !ok {
		return
	}
	session := mustGraphNegativePropagationBenchmarkSession(tb, revision, keys, tc)
	snapshot := runGraphNegativePropagationBenchmarkMutation(tb, session, keys, tc, operation)
	assertGraphNegativePropagationCounterSnapshot(tb, snapshot, tc, operation)
	reporter.ReportMetric(propagationRuntimePathMetric(snapshot.RuntimePath, propagationRuntimeGraphBeta), "diagnostic-runtime-graph-beta")
	reporter.ReportMetric(float64(len(snapshot.UnsupportedReasons)), "diagnostic-unsupported-reason-count")
	reporter.ReportMetric(float64(snapshot.Totals.RemovalIndexLookups), "diagnostic-removal-index-lookups")
	reporter.ReportMetric(float64(snapshot.Totals.RemovalRowsTouched), "diagnostic-removal-rows-touched")
	reporter.ReportMetric(float64(snapshot.Totals.RemovalRowsRemoved), "diagnostic-removal-rows-removed")
	reporter.ReportMetric(float64(snapshot.Totals.NegativePropagationEvents), "diagnostic-negative-propagation-events")
	reporter.ReportMetric(float64(snapshot.Totals.NegativeRowsRemoved), "diagnostic-negative-beta-rows-removed")
	reporter.ReportMetric(float64(snapshot.Totals.NegativeTerminalRowsRemoved), "diagnostic-negative-terminal-rows-removed")
	reporter.ReportMetric(float64(snapshot.Totals.TerminalDeltasRemoved), "diagnostic-terminal-deltas-removed")
	reporter.ReportMetric(float64(snapshot.TerminalRowsRetained), "diagnostic-terminal-rows-retained")
	reporter.ReportMetric(float64(snapshot.Totals.AgendaDeltaApplications), "diagnostic-agenda-delta-applications")
}

func runGraphNegativePropagationBenchmarkMutation(tb testing.TB, session *Session, keys graphNegativePropagationKeys, tc graphNegativePropagationBenchmarkCase, operation graphNegativePropagationOperation) propagationCounterSnapshot {
	tb.Helper()

	target := mustGraphNegativePropagationTargetFact(tb, session, keys, tc)
	session.attachPropagationCounters()
	switch operation {
	case graphNegativePropagationRetract:
		result, err := session.Retract(context.Background(), target.ID())
		if err != nil {
			tb.Fatalf("diagnostic Retract: %v", err)
		}
		if result.Status != RetractRemoved {
			tb.Fatalf("diagnostic retract status = %v, want %v", result.Status, RetractRemoved)
		}
	case graphNegativePropagationModify:
		result, err := session.Modify(context.Background(), target.ID(), FactPatch{
			Set: mustFields(tb, map[string]any{"group": graphNegativePropagationDestinationGroup}),
		})
		if err != nil {
			tb.Fatalf("diagnostic Modify: %v", err)
		}
		if result.Status != ModifyChanged {
			tb.Fatalf("diagnostic modify status = %v, want %v", result.Status, ModifyChanged)
		}
	default:
		tb.Fatalf("unsupported operation %q", operation)
	}
	return session.propagationCounterSnapshot()
}

func assertGraphNegativePropagationCounterSnapshot(tb testing.TB, snapshot propagationCounterSnapshot, tc graphNegativePropagationBenchmarkCase, operation graphNegativePropagationOperation) {
	tb.Helper()

	affectedBetaRows := tc.affectedBetaRows()
	affectedTerminals := tc.affectedTerminalRows()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		tb.Fatalf("runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	if len(snapshot.UnsupportedReasons) != 0 {
		tb.Fatalf("unsupported reasons = %#v, want none", snapshot.UnsupportedReasons)
	}
	if got, want := snapshot.Totals.TerminalDeltasRemoved, affectedTerminals; got != want {
		tb.Fatalf("terminal deltas removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.TerminalRowsRemoved, affectedTerminals; got != want {
		tb.Fatalf("terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeTerminalRowsRemoved, affectedTerminals; got != want {
		tb.Fatalf("negative terminal rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.NegativeRowsRemoved, affectedBetaRows; got != want {
		tb.Fatalf("negative beta rows removed = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.RemovalRowsRemoved, affectedBetaRows; got != want {
		tb.Fatalf("removal rows removed = %d, want %d", got, want)
	}
	if got, wantMin := snapshot.Totals.RemovalRowsTouched, affectedBetaRows; got < wantMin {
		tb.Fatalf("removal rows touched = %d, want at least affected join rows %d", got, wantMin)
	}
	if got, want := snapshot.Totals.RemovalIndexLookups, affectedBetaRows; got != want {
		tb.Fatalf("removal index lookups = %d, want topology-limited %d", got, want)
	}
	if got, want := snapshot.Totals.NegativePropagationEvents, affectedBetaRows+affectedTerminals; got != want {
		tb.Fatalf("negative propagation events = %d, want %d", got, want)
	}
	if got, want := snapshot.Totals.AgendaDeltaApplications, 1; got != want {
		tb.Fatalf("agenda delta applications = %d, want %d", got, want)
	}
	if operation != graphNegativePropagationRetract && operation != graphNegativePropagationModify {
		tb.Fatalf("unsupported operation %q", operation)
	}
	if got := snapshot.TerminalRowsRetained; got != 0 {
		tb.Fatalf("terminal rows retained = %d, want 0", got)
	}
}

func mustGraphResidualJoinBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	threshold := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "candidate",
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
			{Binding: "threshold", Target: TemplateKeyFact(threshold.Key())},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "group"}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "score"}},
				}, Target: TemplateKeyFact(candidate.Key()),
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

func mustCompoundEqualityResidualJoinBenchmarkRuleset(tb testing.TB) (*Ruleset, TemplateKey, TemplateKey) {
	tb.Helper()

	workspace := NewWorkspace()
	threshold := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "threshold",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "payload", Kind: ValueMap, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	candidate := mustAddTemplate(tb, workspace, TemplateSpec{
		Name: "candidate",
		Fields: []FieldSpec{
			{Name: "group", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "meta", Kind: ValueMap, Required: true},
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
			{Binding: "threshold", Target: TemplateKeyFact(threshold.Key())},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "group", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "group"}},
					{Field: "region", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Field: "region"}},
					{Path: Path("meta", MapKey("id")), Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "threshold", Path: Path("payload", MapKey("id"))}},
					{Field: "score", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "score"}},
				}, Target: TemplateKeyFact(candidate.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark"}},
	})
	return mustCompileWorkspace(tb, workspace), threshold.Key(), candidate.Key()
}

func mustCompoundEqualityResidualJoinBenchmarkSession(tb testing.TB, revision *Ruleset, thresholdKey TemplateKey, thresholds int) *Session {
	tb.Helper()

	initials := make([]SessionInitialFact, 0, thresholds)
	for i := range thresholds {
		initials = append(initials, SessionInitialFact{
			TemplateKey: thresholdKey,
			Fields: mustFields(tb, map[string]any{
				"group":   "A",
				"region":  fmt.Sprintf("R%03d", i),
				"payload": map[string]any{"id": fmt.Sprintf("T%03d", i)},
				"score":   i,
			}),
		})
	}
	session, err := NewSession(revision, WithInitialFacts(initials...))
	if err != nil {
		tb.Fatalf("NewSession: %v", err)
	}
	return session
}

func reportCompoundEqualityResidualJoinBenchmarkCounters(tb testing.TB, revision *Ruleset, thresholdKey, candidateKey TemplateKey, thresholds int, candidateFields Fields) {
	tb.Helper()

	reporter, ok := tb.(interface {
		ReportMetric(float64, string)
	})
	if !ok {
		return
	}
	session := mustCompoundEqualityResidualJoinBenchmarkSession(tb, revision, thresholdKey, thresholds)
	session.attachPropagationCounters()
	result, err := session.AssertTemplate(context.Background(), candidateKey, candidateFields)
	if err != nil {
		tb.Fatalf("diagnostic AssertTemplate candidate: %v", err)
	}
	if result.Status != AssertInserted {
		tb.Fatalf("diagnostic assert status = %v, want %v", result.Status, AssertInserted)
	}
	snapshot := session.propagationCounterSnapshot()
	probes := max(1, snapshot.Totals.BetaBucketProbes)
	reporter.ReportMetric(propagationRuntimePathMetric(snapshot.RuntimePath, propagationRuntimeGraphBeta), "diagnostic-runtime-graph-beta")
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
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	candidate := mustAddTemplate(tb, workspace, TemplateSpec{
		Name:   "candidate",
		Fields: []FieldSpec{{Name: "age", Kind: ValueInt, Required: true}},
	})
	mustAddAction(tb, workspace, ActionSpec{
		Name: "mark",
		Fn:   func(ActionContext) error { return nil },
	})
	mustAddRule(tb, workspace, RuleSpec{
		Name: "older-than-threshold",
		Conditions: []RuleConditionSpec{
			{Binding: "threshold", Target: TemplateKeyFact(threshold.Key())},
			{
				Binding: "candidate",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "age", Operator: FieldConstraintGreaterThan, Ref: FieldRef{Binding: "threshold", Field: "age"}},
				}, Target: TemplateKeyFact(candidate.Key()),
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
	if session.rete == nil || session.rete.graphBeta == nil || !session.rete.plan.betaSupported {
		tb.Fatalf("Rete runtime = %#v, want graph-beta residual support", session.rete)
	}
	return session
}

func reportPureResidualJoinBenchmarkCounters(tb testing.TB, revision *Ruleset, thresholdKey, candidateKey TemplateKey, thresholds int, candidateFields Fields) {
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
	if err != nil {
		tb.Fatalf("diagnostic AssertTemplate candidate: %v", err)
	}
	if result.Status != AssertInserted {
		tb.Fatalf("diagnostic assert status = %v, want %v", result.Status, AssertInserted)
	}
	if got := len(session.agenda.pendingActivations()); got != thresholds {
		tb.Fatalf("diagnostic pending activations = %d, want %d", got, thresholds)
	}
	snapshot := session.propagationCounterSnapshot()
	if snapshot.RuntimePath != propagationRuntimeGraphBeta {
		tb.Fatalf("diagnostic runtime path = %q, want %q", snapshot.RuntimePath, propagationRuntimeGraphBeta)
	}
	reporter.ReportMetric(propagationRuntimePathMetric(snapshot.RuntimePath, propagationRuntimeGraphBeta), "diagnostic-runtime-graph-beta")
	reporter.ReportMetric(float64(len(snapshot.UnsupportedReasons)), "diagnostic-unsupported-reason-count")
	reporter.ReportMetric(float64(snapshot.Totals.BetaBucketProbes), "diagnostic-beta-bucket-probes")
	reporter.ReportMetric(float64(snapshot.Totals.BetaCandidateRowsScanned), "diagnostic-beta-candidate-rows-scanned")
	reporter.ReportMetric(float64(snapshot.Totals.BetaResidualTests), "diagnostic-beta-residual-tests")
	reporter.ReportMetric(float64(snapshot.Totals.BetaJoinedTokensProduced), "diagnostic-beta-joined-tokens-produced")
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
	assertDelta = cloneReteAgendaDelta(assertDelta)
	_, modifyDelta, err := session.modifyImmediate(context.Background(), asserted.Fact.ID(), FactPatch{
		Set: mustFields(tb, map[string]any{"name": "Grace"}),
	}, mutationOrigin{})
	if err != nil {
		tb.Fatalf("modifyImmediate: %v", err)
	}
	modifyDelta = cloneReteAgendaDelta(modifyDelta)
	_, retractDelta, err := session.retractImmediate(context.Background(), asserted.Fact.ID(), mutationOrigin{})
	if err != nil {
		tb.Fatalf("retractImmediate: %v", err)
	}
	retractDelta = cloneReteAgendaDelta(retractDelta)
	return terminalTokenDeltaBenchmarkFixture{
		revision:     revision,
		assertDelta:  assertDelta,
		modifyDelta:  modifyDelta,
		retractDelta: retractDelta,
	}
}

func cloneReteAgendaDelta(delta reteAgendaDelta) reteAgendaDelta {
	return reteAgendaDelta{
		supported: delta.supported,
		added:     cloneTerminalTokenDeltas(delta.added),
		removed:   cloneTerminalTokenDeltas(delta.removed),
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
