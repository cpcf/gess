package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	claimsTriageContractFactCount  = 24
	claimsTriageBenchmarkFactCount = 64
)

type claimsTriageRow struct {
	customerID     string
	policyID       string
	claimID        string
	tier           string
	region         string
	previousClaims int
	product        string
	status         string
	deductible     int
	claimType      string
	amount         int
	injury         string
	signalKind     string
	signalScore    int
}

func TestClaimsTriageFixtureMatchesContract(t *testing.T) {
	trace := runClaimsTriageFixture(t, claimsTriageContractFactCount)
	sort.Strings(trace)

	expected := expectedClaimsTriageTrace(claimsTriageContractFactCount)
	if !slices.Equal(trace, expected) {
		t.Fatalf("claims triage trace = %#v, want %#v", trace, expected)
	}
}

func TestClaimsTriageJessFixtureMatchesContract(t *testing.T) {
	trace := runClaimsTriageJessTrace(t, claimsTriageContractFactCount)
	sort.Strings(trace)

	expected := expectedClaimsTriageTrace(claimsTriageContractFactCount)
	if !slices.Equal(trace, expected) {
		t.Fatalf("Jess claims triage trace = %#v, want %#v", trace, expected)
	}
}

func TestReteRuntimeParityHarnessMatchesClaimsTriageOracle(t *testing.T) {
	ctx := context.Background()
	revision := mustCompileClaimsTriageRuleset(t, nil)
	runtime, err := newReteRuntime(revision)
	if err != nil {
		t.Fatalf("newReteRuntime: %v", err)
	}
	if got := len(runtime.plan.unsupported); got != 0 {
		t.Fatalf("unsupported plan reasons = %#v, want none", runtime.plan.unsupported)
	}

	session := mustSession(t, revision, "claims-triage-rete-parity")
	for _, fact := range claimsTriageInitialFacts(t, claimsTriageBenchmarkFactCount) {
		if _, err := session.AssertTemplate(ctx, fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	snapshot := mustSnapshot(t, ctx, session)
	if err := runtime.resetGraphBeta(ctx, snapshot.Facts()); err != nil {
		t.Fatalf("resetGraphBeta: %v", err)
	}

	assertMatcherParity(t, revision, snapshot, newNaiveMatcher(revision), runtime)
}

func BenchmarkClaimsTriageGessSessionCycle(b *testing.B) {
	revision := mustCompileClaimsTriageRuleset(b, nil)
	initials := claimsTriageInitialFacts(b, claimsTriageBenchmarkFactCount)
	expectedFired := claimsTriageFiredCount(claimsTriageBenchmarkFactCount)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustSession(b, revision, "claims-triage-session-cycle")
		for _, fact := range initials {
			if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
				b.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
			}
		}

		result, err := session.Run(context.Background())
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != expectedFired {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, expectedFired)
		}
	}
}

func BenchmarkClaimsTriageGessResetRun(b *testing.B) {
	revision := mustCompileClaimsTriageRuleset(b, nil)
	expectedFired := claimsTriageFiredCount(claimsTriageBenchmarkFactCount)
	session, err := NewSession(revision, WithInitialFacts(claimsTriageInitialFacts(b, claimsTriageBenchmarkFactCount)...))
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := session.Reset(context.Background()); err != nil {
			b.Fatalf("Reset: %v", err)
		}
		result, err := session.Run(context.Background())
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != expectedFired {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, expectedFired)
		}
	}
}

func BenchmarkClaimsTriageGessRunOnly(b *testing.B) {
	revision := mustCompileClaimsTriageRuleset(b, nil)
	initials := claimsTriageInitialFacts(b, claimsTriageBenchmarkFactCount)
	expectedFired := claimsTriageFiredCount(claimsTriageBenchmarkFactCount)

	b.ReportAllocs()
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustSession(b, revision, "claims-triage-run-only")
		for _, fact := range initials {
			if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
				b.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
			}
		}

		b.StartTimer()
		result, err := session.Run(context.Background())
		b.StopTimer()
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != expectedFired {
			b.Fatalf("run result = (%v, %d), want (%v, %d)", result.Status, result.Fired, RunCompleted, expectedFired)
		}
	}
}

func BenchmarkClaimsTriageGessAssertOnly(b *testing.B) {
	revision := mustCompileClaimsTriageRuleset(b, nil)
	initials := claimsTriageInitialFacts(b, claimsTriageBenchmarkFactCount)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustSession(b, revision, "claims-triage-assert-only")
		for _, fact := range initials {
			if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
				b.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
			}
		}
	}
}

func BenchmarkClaimsTriageGessMatchOnly(b *testing.B) {
	revision := mustCompileClaimsTriageRuleset(b, nil)
	initials := claimsTriageInitialFacts(b, claimsTriageBenchmarkFactCount)
	session := mustSession(b, revision, "claims-triage-match-only")
	for _, fact := range initials {
		if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			b.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	snapshot := session.indexedSnapshotLocked()
	expectedCandidates := claimsTriageFiredCount(claimsTriageBenchmarkFactCount)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := newNaiveMatcher(revision).match(context.Background(), snapshot)
		if err != nil {
			b.Fatalf("match: %v", err)
		}
		if got := countClaimsTriageCandidates(results); got != expectedCandidates {
			b.Fatalf("candidate count = %d, want %d", got, expectedCandidates)
		}
	}
}

func BenchmarkClaimsTriageGessReteMatchOnly(b *testing.B) {
	revision := mustCompileClaimsTriageRuleset(b, nil)
	initials := claimsTriageInitialFacts(b, claimsTriageBenchmarkFactCount)
	session := mustSession(b, revision, "claims-triage-rete-match-only")
	for _, fact := range initials {
		if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			b.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}
	snapshot := session.indexedSnapshotLocked()
	expectedCandidates := claimsTriageFiredCount(claimsTriageBenchmarkFactCount)
	if session.rete == nil {
		b.Fatal("session Rete runtime is nil")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := session.rete.match(context.Background(), snapshot)
		if err != nil {
			b.Fatalf("match: %v", err)
		}
		if got := countClaimsTriageCandidates(results); got != expectedCandidates {
			b.Fatalf("candidate count = %d, want %d", got, expectedCandidates)
		}
	}
}

func runClaimsTriageFixture(t testing.TB, count int) []string {
	t.Helper()

	trace := make([]string, 0, len(expectedClaimsTriageTrace(count)))
	revision := mustCompileClaimsTriageRuleset(t, &trace)
	session := mustSession(t, revision, "claims-triage-fixture")

	for _, fact := range claimsTriageInitialFacts(t, count) {
		if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
			t.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
		}
	}

	result, err := session.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("run status = %v, want %v", result.Status, RunCompleted)
	}
	expectedFired := claimsTriageFiredCount(count)
	if result.Fired != expectedFired {
		t.Fatalf("run fired = %d, want %d", result.Fired, expectedFired)
	}

	return trace
}

func runClaimsTriageJessTrace(t *testing.T, count int) []string {
	t.Helper()

	jarPath := filepath.Join("..", "..", "..", "gess-design", "jess.jar")
	if _, err := os.Stat(jarPath); err != nil {
		t.Skipf("Jess jar unavailable: %v", err)
	}
	scriptPath := filepath.Join("..", "..", "..", "gess-design", "rule-corpus", "claims-triage", "claims-triage.clp")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("Jess claims triage fixture unavailable: %v", err)
	}
	runnerPath := filepath.Join("..", "..", "..", "gess-design", "rule-corpus", "claims-triage", "ClaimsTriageJessRunner.java")
	if _, err := os.Stat(runnerPath); err != nil {
		t.Skipf("Jess claims triage runner unavailable: %v", err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Skipf("java unavailable: %v", err)
	}
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skipf("javac unavailable: %v", err)
	}

	classpath, err := loanUnderwritingJessClasspathWithClockShim(t, jarPath)
	if err != nil {
		t.Skipf("Jess clock shim unavailable: %v", err)
	}
	classDir := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		t.Fatalf("create Jess class dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	compile := exec.CommandContext(ctx, "javac", "-cp", classpath, "-d", classDir, runnerPath)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile Jess claims triage runner: %v\n%s", err, output)
	}

	runClasspath := classDir + string(os.PathListSeparator) + classpath
	run := exec.CommandContext(ctx, "java", "-cp", runClasspath, "ClaimsTriageJessRunner", "trace", scriptPath, strconv.Itoa(count))
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run Jess claims triage fixture: %v\n%s", err, output)
	}
	return filterClaimsTriageTrace(string(output))
}

func mustCompileClaimsTriageRuleset(t testing.TB, trace *[]string) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	customer := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "customer",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "tier", Kind: ValueString, Required: true},
			{Name: "region", Kind: ValueString, Required: true},
			{Name: "previous-claims", Kind: ValueInt, Required: true},
		},
	})
	policy := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "policy",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "product", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
			{Name: "deductible", Kind: ValueInt, Required: true},
		},
	})
	claim := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "claim",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "customer-id", Kind: ValueString, Required: true},
			{Name: "policy-id", Kind: ValueString, Required: true},
			{Name: "claim-type", Kind: ValueString, Required: true},
			{Name: "amount", Kind: ValueInt, Required: true},
			{Name: "injury", Kind: ValueString, Required: true},
		},
	})
	claimIDSlot, ok := claim.fieldSlot("id")
	if !ok {
		t.Fatal("claim template missing id slot")
	}
	signal := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "signal",
		Fields: []FieldSpec{
			{Name: "claim-id", Kind: ValueString, Required: true},
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "score", Kind: ValueInt, Required: true},
		},
	})
	triage := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "triage",
		Fields: []FieldSpec{
			{Name: "claim-id", Kind: ValueString, Required: true},
			{Name: "outcome", Kind: ValueString, Required: true},
			{Name: "reason", Kind: ValueString, Required: true},
			{Name: "queue", Kind: ValueString, Required: true},
		},
	})

	addClaimsTriageAction(t, workspace, "mark-fraud-watch", triage.Key(), claimIDSlot, trace, "escalate-fraud-watch", "investigate", "fraud-watch", "siu")
	addClaimsTriageAction(t, workspace, "mark-complex-injury", triage.Key(), claimIDSlot, trace, "route-complex-injury", "review", "injury", "senior-adjuster")
	addClaimsTriageAction(t, workspace, "mark-repeat-claimant", triage.Key(), claimIDSlot, trace, "review-repeat-claimant", "review", "repeat-claimant", "analyst")
	addClaimsTriageAction(t, workspace, "mark-high-exposure", triage.Key(), claimIDSlot, trace, "review-high-exposure", "review", "high-exposure", "large-loss")
	addClaimsTriageAction(t, workspace, "mark-gold-auto", triage.Key(), claimIDSlot, trace, "approve-gold-auto", "approve", "gold-auto-low-risk", "straight-through")
	addClaimsTriageAction(t, workspace, "mark-silver-property", triage.Key(), claimIDSlot, trace, "approve-silver-property", "approve", "silver-property-low-risk", "straight-through")

	mustAddRule(t, workspace, RuleSpec{
		Name:     "escalate-fraud-watch",
		Salience: 100,
		Conditions: []RuleConditionSpec{
			{Binding: "claim", Target: TemplateKeyFact(claim.Key())},
			{
				Binding: "customer",

				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "customer-id"}},
				}, Target: TemplateKeyFact(customer.Key()),
			},
			{
				Binding: "signal",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "kind", Operator: FieldConstraintEqual, Value: "fraud"},
					{Field: "score", Operator: FieldConstraintGreaterOrEqual, Value: 80},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "claim-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "id"}},
				}, Target: TemplateKeyFact(signal.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark-fraud-watch"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "route-complex-injury",
		Salience: 85,
		Conditions: []RuleConditionSpec{
			{
				Binding: "claim",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "injury", Operator: FieldConstraintEqual, Value: "yes"},
				}, Target: TemplateKeyFact(claim.Key()),
			},
			{
				Binding: "policy",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "product", Operator: FieldConstraintEqual, Value: "auto"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "policy-id"}},
				}, Target: TemplateKeyFact(policy.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark-complex-injury"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "review-repeat-claimant",
		Salience: 70,
		Conditions: []RuleConditionSpec{
			{Binding: "claim", Target: TemplateKeyFact(claim.Key())},
			{
				Binding: "customer",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "previous-claims", Operator: FieldConstraintGreaterOrEqual, Value: 3},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "customer-id"}},
				}, Target: TemplateKeyFact(customer.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark-repeat-claimant"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "review-high-exposure",
		Salience: 60,
		Conditions: []RuleConditionSpec{
			{
				Binding: "claim",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "amount", Operator: FieldConstraintGreaterThan, Value: 10000},
				}, Target: TemplateKeyFact(claim.Key()),
			},
			{
				Binding: "policy",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "policy-id"}},
				}, Target: TemplateKeyFact(policy.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark-high-exposure"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "approve-gold-auto",
		Salience: 20,
		Conditions: []RuleConditionSpec{
			{
				Binding: "claim",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "claim-type", Operator: FieldConstraintEqual, Value: "auto"},
					{Field: "amount", Operator: FieldConstraintLessThan, Value: 2500},
				}, Target: TemplateKeyFact(claim.Key()),
			},
			{
				Binding: "customer",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "tier", Operator: FieldConstraintEqual, Value: "gold"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "customer-id"}},
				}, Target: TemplateKeyFact(customer.Key()),
			},
			{
				Binding: "policy",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "policy-id"}},
				}, Target: TemplateKeyFact(policy.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark-gold-auto"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "approve-silver-property",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{
				Binding: "claim",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "claim-type", Operator: FieldConstraintEqual, Value: "property"},
					{Field: "amount", Operator: FieldConstraintLessOrEqual, Value: 4000},
				}, Target: TemplateKeyFact(claim.Key()),
			},
			{
				Binding: "customer",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "tier", Operator: FieldConstraintEqual, Value: "silver"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "customer-id"}},
				}, Target: TemplateKeyFact(customer.Key()),
			},
			{
				Binding: "policy",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "product", Operator: FieldConstraintEqual, Value: "home"},
					{Field: "status", Operator: FieldConstraintEqual, Value: "active"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "claim", Field: "policy-id"}},
				}, Target: TemplateKeyFact(policy.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "mark-silver-property"}},
	})

	return mustCompileWorkspace(t, workspace)
}

func addClaimsTriageAction(t testing.TB, workspace *Workspace, name string, triageKey TemplateKey, claimIDSlot int, trace *[]string, ruleName, outcome, reason, queue string) {
	t.Helper()

	outcomeValue := mustValue(t, outcome)
	reasonValue := mustValue(t, reason)
	queueValue := mustValue(t, queue)
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: name,
		Fn: func(ctx ActionContext) error {
			idValue, ok := ctx.bindingScalarValueAtSlot(0, claimIDSlot)
			if !ok {
				t.Fatalf("action %s missing claim id", name)
			}
			if idValue.Kind() != ValueString {
				t.Fatalf("action %s claim id kind = %s, want %s", name, idValue.Kind(), ValueString)
			}

			claimID := idValue.stringValue
			if trace != nil {
				*trace = append(*trace,
					"FIRED|"+ruleName+"|"+claimID,
					"TRIAGE|"+claimID+"|"+outcome+"|"+reason+"|"+queue,
				)
			}
			_, err := ctx.AssertTemplate(triageKey, Fields{
				"claim-id": idValue,
				"outcome":  outcomeValue,
				"reason":   reasonValue,
				"queue":    queueValue,
			})
			return err
		},
	})
}

func claimsTriageInitialFacts(t testing.TB, count int) []SessionInitialFact {
	t.Helper()

	rows := claimsTriageRows(count)
	facts := make([]SessionInitialFact, 0, len(rows)*4)
	for _, row := range rows {
		facts = append(facts,
			SessionInitialFact{TemplateKey: "customer", Fields: mustFields(t, map[string]any{
				"id":              row.customerID,
				"tier":            row.tier,
				"region":          row.region,
				"previous-claims": row.previousClaims,
			})},
			SessionInitialFact{TemplateKey: "policy", Fields: mustFields(t, map[string]any{
				"id":          row.policyID,
				"customer-id": row.customerID,
				"product":     row.product,
				"status":      row.status,
				"deductible":  row.deductible,
			})},
			SessionInitialFact{TemplateKey: "claim", Fields: mustFields(t, map[string]any{
				"id":          row.claimID,
				"customer-id": row.customerID,
				"policy-id":   row.policyID,
				"claim-type":  row.claimType,
				"amount":      row.amount,
				"injury":      row.injury,
			})},
			SessionInitialFact{TemplateKey: "signal", Fields: mustFields(t, map[string]any{
				"claim-id": row.claimID,
				"kind":     row.signalKind,
				"score":    row.signalScore,
			})},
		)
	}
	return facts
}

func claimsTriageRows(count int) []claimsTriageRow {
	rows := make([]claimsTriageRow, 0, count)
	for i := range count {
		rows = append(rows, claimsTriageRow{
			customerID:     fmt.Sprintf("C-%03d", i),
			policyID:       fmt.Sprintf("P-%03d", i),
			claimID:        fmt.Sprintf("CL-%03d", i),
			tier:           claimsTriageTier(i),
			region:         claimsTriageRegion(i),
			previousClaims: i % 5,
			product:        claimsTriageProduct(i),
			status:         claimsTriageStatus(i),
			deductible:     claimsTriageDeductible(i),
			claimType:      claimsTriageClaimType(i),
			amount:         claimsTriageAmount(i),
			injury:         claimsTriageInjury(i),
			signalKind:     claimsTriageSignalKind(i),
			signalScore:    claimsTriageSignalScore(i),
		})
	}
	return rows
}

func expectedClaimsTriageTrace(count int) []string {
	trace := make([]string, 0)
	for _, row := range claimsTriageRows(count) {
		if claimsTriageFraudWatch(row) {
			trace = appendClaimsTriageTrace(trace, "escalate-fraud-watch", row.claimID, "investigate", "fraud-watch", "siu")
		}
		if claimsTriageComplexInjury(row) {
			trace = appendClaimsTriageTrace(trace, "route-complex-injury", row.claimID, "review", "injury", "senior-adjuster")
		}
		if claimsTriageRepeatClaimant(row) {
			trace = appendClaimsTriageTrace(trace, "review-repeat-claimant", row.claimID, "review", "repeat-claimant", "analyst")
		}
		if claimsTriageHighExposure(row) {
			trace = appendClaimsTriageTrace(trace, "review-high-exposure", row.claimID, "review", "high-exposure", "large-loss")
		}
		if claimsTriageGoldAuto(row) {
			trace = appendClaimsTriageTrace(trace, "approve-gold-auto", row.claimID, "approve", "gold-auto-low-risk", "straight-through")
		}
		if claimsTriageSilverProperty(row) {
			trace = appendClaimsTriageTrace(trace, "approve-silver-property", row.claimID, "approve", "silver-property-low-risk", "straight-through")
		}
	}
	sort.Strings(trace)
	return trace
}

func appendClaimsTriageTrace(trace []string, ruleName, claimID, outcome, reason, queue string) []string {
	return append(trace,
		"FIRED|"+ruleName+"|"+claimID,
		"TRIAGE|"+claimID+"|"+outcome+"|"+reason+"|"+queue,
	)
}

func claimsTriageFiredCount(count int) int {
	return len(expectedClaimsTriageTrace(count)) / 2
}

func countClaimsTriageCandidates(results []ruleMatchResult) int {
	count := 0
	for _, result := range results {
		count += len(result.candidates)
	}
	return count
}

func claimsTriageFraudWatch(row claimsTriageRow) bool {
	return row.signalKind == "fraud" && row.signalScore >= 80
}

func claimsTriageComplexInjury(row claimsTriageRow) bool {
	return row.injury == "yes" && row.product == "auto"
}

func claimsTriageRepeatClaimant(row claimsTriageRow) bool {
	return row.previousClaims >= 3
}

func claimsTriageHighExposure(row claimsTriageRow) bool {
	return row.amount > 10000 && row.status == "active"
}

func claimsTriageGoldAuto(row claimsTriageRow) bool {
	return row.claimType == "auto" && row.amount < 2500 && row.tier == "gold" && row.status == "active"
}

func claimsTriageSilverProperty(row claimsTriageRow) bool {
	return row.claimType == "property" && row.amount <= 4000 && row.tier == "silver" && row.product == "home" && row.status == "active"
}

func claimsTriageTier(index int) string {
	switch index % 4 {
	case 0:
		return "gold"
	case 1:
		return "silver"
	default:
		return "bronze"
	}
}

func claimsTriageRegion(index int) string {
	switch index % 3 {
	case 0:
		return "north"
	case 1:
		return "south"
	default:
		return "west"
	}
}

func claimsTriageProduct(index int) string {
	if index%2 == 0 {
		return "auto"
	}
	return "home"
}

func claimsTriageStatus(index int) string {
	if index%13 == 0 {
		return "lapsed"
	}
	return "active"
}

func claimsTriageDeductible(index int) int {
	if index%3 == 0 {
		return 250
	}
	return 500
}

func claimsTriageClaimType(index int) string {
	if index%2 == 0 {
		return "auto"
	}
	return "property"
}

func claimsTriageAmount(index int) int {
	amount := 1200 + (index%8)*700
	if index%10 == 0 {
		amount += 12000
	}
	return amount
}

func claimsTriageInjury(index int) string {
	if index%9 == 0 {
		return "yes"
	}
	return "no"
}

func claimsTriageSignalKind(index int) string {
	if index%11 == 0 {
		return "fraud"
	}
	return "identity"
}

func claimsTriageSignalScore(index int) int {
	if index%11 == 0 {
		return 90
	}
	return 20 + index%30
}

func filterClaimsTriageTrace(output string) []string {
	lines := strings.Split(output, "\n")
	trace := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "FIRED|") || strings.HasPrefix(line, "TRIAGE|") {
			trace = append(trace, line)
		}
	}
	return trace
}
