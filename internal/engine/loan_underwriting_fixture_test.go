package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoanUnderwritingFixtureMatchesContract(t *testing.T) {
	trace := runLoanUnderwritingFixture(t)

	if !slices.Equal(trace, expectedLoanUnderwritingTrace()) {
		t.Fatalf("loan underwriting trace = %#v, want %#v", trace, expectedLoanUnderwritingTrace())
	}
}

func TestLoanUnderwritingJessFixtureMatchesContract(t *testing.T) {
	jarPath := filepath.Join("..", "..", "..", "gess-design", "jess.jar")
	if _, err := os.Stat(jarPath); err != nil {
		t.Skipf("Jess jar unavailable: %v", err)
	}

	scriptPath := filepath.Join("..", "..", "..", "gess-design", "rule-corpus", "loan-underwriting", "loan-underwriting.clp")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("Jess loan underwriting fixture unavailable: %v", err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Skipf("java unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := runLoanUnderwritingJessProcess(ctx, jarPath, scriptPath)
	if err != nil {
		if strings.Contains(string(output), "This copy of Jess has expired") {
			shimmedClasspath, shimErr := loanUnderwritingJessClasspathWithClockShim(t, jarPath)
			if shimErr != nil {
				t.Skipf("Jess jar expired and clock shim is unavailable: %v", shimErr)
			}
			output, err = runLoanUnderwritingJessProcess(ctx, shimmedClasspath, scriptPath)
			if err != nil {
				t.Fatalf("run Jess fixture with clock shim: %v\n%s", err, output)
			}
		} else {
			t.Fatalf("run Jess fixture: %v\n%s", err, output)
		}
	}

	trace := filterLoanUnderwritingTrace(string(output))
	if !slices.Equal(trace, expectedLoanUnderwritingTrace()) {
		t.Fatalf("Jess loan underwriting trace = %#v, want %#v\nfull output:\n%s", trace, expectedLoanUnderwritingTrace(), output)
	}
}

func BenchmarkLoanUnderwritingGessSessionCycle(b *testing.B) {
	revision := mustCompileLoanUnderwritingRuleset(b, nil)
	initials := loanUnderwritingInitialFacts(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session := mustLoanUnderwritingSession(b, revision)
		for _, fact := range initials {
			if _, err := session.AssertTemplate(context.Background(), fact.TemplateKey, fact.Fields); err != nil {
				b.Fatalf("AssertTemplate(%s): %v", fact.TemplateKey, err)
			}
		}

		result, err := session.Run(context.Background())
		if err != nil {
			b.Fatalf("Run: %v", err)
		}
		if result.Status != RunCompleted || result.Fired != 5 {
			b.Fatalf("run result = (%v, %d), want (%v, 5)", result.Status, result.Fired, RunCompleted)
		}
	}
}

func BenchmarkLoanUnderwritingGessResetRun(b *testing.B) {
	revision := mustCompileLoanUnderwritingRuleset(b, nil)
	session, err := NewSession(revision, WithInitialFacts(loanUnderwritingTemplateInitialFacts(b)...))
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
		if result.Status != RunCompleted || result.Fired != 5 {
			b.Fatalf("run result = (%v, %d), want (%v, 5)", result.Status, result.Fired, RunCompleted)
		}
	}
}

func BenchmarkLoanUnderwritingGessRunOnly(b *testing.B) {
	revision := mustCompileLoanUnderwritingRuleset(b, nil)
	initials := loanUnderwritingInitialFacts(b)

	b.ReportAllocs()
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		session := mustLoanUnderwritingSession(b, revision)
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
		if result.Status != RunCompleted || result.Fired != 5 {
			b.Fatalf("run result = (%v, %d), want (%v, 5)", result.Status, result.Fired, RunCompleted)
		}
	}
}

func runLoanUnderwritingFixture(t testing.TB) []string {
	t.Helper()

	trace := make([]string, 0, len(expectedLoanUnderwritingTrace()))
	revision := mustCompileLoanUnderwritingRuleset(t, &trace)
	session := mustSession(t, revision, "loan-underwriting-fixture")

	for _, fact := range loanUnderwritingInitialFacts(t) {
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
	if result.Fired != 5 {
		t.Fatalf("run fired = %d, want 5", result.Fired)
	}

	return trace
}

func mustLoanUnderwritingSession(t testing.TB, revision *Ruleset) *Session {
	t.Helper()
	session, err := NewSession(revision)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func runLoanUnderwritingJessProcess(ctx context.Context, classpath, scriptPath string) ([]byte, error) {
	return exec.CommandContext(ctx, "java", "-cp", classpath, "jess.Main", "-nologo", scriptPath).CombinedOutput()
}

func loanUnderwritingJessClasspathWithClockShim(t *testing.T, jarPath string) (string, error) {
	t.Helper()

	reader, err := zip.OpenReader(jarPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	var ruClass []byte
	for _, file := range reader.File {
		if file.Name != "jess/RU.class" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		ruClass, err = io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		break
	}
	if len(ruClass) == 0 {
		return "", fmt.Errorf("jess/RU.class not found in %s", jarPath)
	}

	patched, err := patchLoanUnderwritingJessRUClock(ruClass)
	if err != nil {
		return "", err
	}

	classDir := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(filepath.Join(classDir, "jess"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(classDir, "jess", "RU.class"), patched, 0o644); err != nil {
		return "", err
	}
	return classDir + string(os.PathListSeparator) + jarPath, nil
}

func patchLoanUnderwritingJessRUClock(classData []byte) ([]byte, error) {
	// The bundled Jess jar is an expired evaluation build. Patch only the
	// temporary RU.a() class bytes used by this test run; never rewrite the jar.
	pattern := []byte{0xb8, 0x00, 0x20, 0xad}
	replacement := []byte{0x09, 0xad, 0x00, 0x00}

	offset := 0
	matchAt := -1
	matches := 0
	for {
		idx := bytes.Index(classData[offset:], pattern)
		if idx < 0 {
			break
		}
		matchAt = offset + idx
		matches++
		offset = matchAt + 1
	}
	if matches != 1 {
		return nil, fmt.Errorf("expected one RU.a clock bytecode match, found %d", matches)
	}

	patched := slices.Clone(classData)
	copy(patched[matchAt:matchAt+len(replacement)], replacement)
	return patched, nil
}

func mustCompileLoanUnderwritingRuleset(t testing.TB, trace *[]string) *Ruleset {
	t.Helper()

	workspace := NewWorkspace()
	applicant := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "applicant",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "age", Kind: ValueInt, Required: true},
			{Name: "country", Kind: ValueString, Required: true},
		},
	})
	applicantIDSlot, ok := applicant.fieldSlot("id")
	if !ok {
		t.Fatal("applicant template missing id slot")
	}
	financial := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "financial",
		Fields: []FieldSpec{
			{Name: "applicant-id", Kind: ValueString, Required: true},
			{Name: "annual-income", Kind: ValueInt, Required: true},
			{Name: "monthly-debt", Kind: ValueInt, Required: true},
			{Name: "credit-score", Kind: ValueInt, Required: true},
		},
	})
	employment := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "employment",
		Fields: []FieldSpec{
			{Name: "applicant-id", Kind: ValueString, Required: true},
			{Name: "status", Kind: ValueString, Required: true},
			{Name: "months", Kind: ValueInt, Required: true},
		},
	})
	riskFlag := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "risk-flag",
		Fields: []FieldSpec{
			{Name: "applicant-id", Kind: ValueString, Required: true},
			{Name: "code", Kind: ValueString, Required: true},
			{Name: "severity", Kind: ValueString, Required: true},
		},
	})
	decision := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "decision",
		Fields: []FieldSpec{
			{Name: "applicant-id", Kind: ValueString, Required: true},
			{Name: "outcome", Kind: ValueString, Required: true},
			{Name: "reason", Kind: ValueString, Required: true},
			{Name: "tier", Kind: ValueString, Required: true},
		},
	})

	addDecisionAction(t, workspace, "decline-sanctions", decision.Key(), applicantIDSlot, trace, "hard-stop-sanctions", "decline", "sanctions", "ineligible")
	addDecisionAction(t, workspace, "decline-underage", decision.Key(), applicantIDSlot, trace, "hard-stop-underage", "decline", "underage", "ineligible")
	addDecisionAction(t, workspace, "review-low-credit", decision.Key(), applicantIDSlot, trace, "manual-review-low-credit", "review", "low-credit", "analyst")
	addDecisionAction(t, workspace, "review-high-debt", decision.Key(), applicantIDSlot, trace, "manual-review-high-debt", "review", "high-debt", "analyst")
	addDecisionAction(t, workspace, "approve-prime", decision.Key(), applicantIDSlot, trace, "approve-prime-employed", "approve", "prime-employed", "prime")

	mustAddRule(t, workspace, RuleSpec{
		Name:     "hard-stop-sanctions",
		Salience: 100,
		Conditions: []RuleConditionSpec{
			{Binding: "applicant", Target: TemplateKeyFact(applicant.Key())},
			{
				Binding: "risk",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "code", Operator: FieldConstraintEqual, Value: "sanctions"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "applicant-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "applicant", Field: "id"}},
				}, Target: TemplateKeyFact(riskFlag.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "decline-sanctions"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "hard-stop-underage",
		Salience: 90,
		Conditions: []RuleConditionSpec{
			{
				Binding: "applicant",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "age", Operator: FieldConstraintLessThan, Value: 18},
				}, Target: TemplateKeyFact(applicant.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "decline-underage"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "manual-review-low-credit",
		Salience: 50,
		Conditions: []RuleConditionSpec{
			{Binding: "applicant", Target: TemplateKeyFact(applicant.Key())},
			{
				Binding: "financial",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "credit-score", Operator: FieldConstraintLessThan, Value: 620},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "applicant-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "applicant", Field: "id"}},
				}, Target: TemplateKeyFact(financial.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "review-low-credit"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "manual-review-high-debt",
		Salience: 45,
		Conditions: []RuleConditionSpec{
			{Binding: "applicant", Target: TemplateKeyFact(applicant.Key())},
			{
				Binding: "financial",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "monthly-debt", Operator: FieldConstraintGreaterThan, Value: 3500},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "applicant-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "applicant", Field: "id"}},
				}, Target: TemplateKeyFact(financial.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "review-high-debt"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name:     "approve-prime-employed",
		Salience: 10,
		Conditions: []RuleConditionSpec{
			{Binding: "applicant", Target: TemplateKeyFact(applicant.Key())},
			{
				Binding: "financial",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "credit-score", Operator: FieldConstraintGreaterOrEqual, Value: 720},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "applicant-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "applicant", Field: "id"}},
				}, Target: TemplateKeyFact(financial.Key()),
			},
			{
				Binding: "employment",

				FieldConstraints: []FieldConstraintSpec{
					{Field: "status", Operator: FieldConstraintEqual, Value: "employed"},
					{Field: "months", Operator: FieldConstraintGreaterOrEqual, Value: 12},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "applicant-id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "applicant", Field: "id"}},
				}, Target: TemplateKeyFact(employment.Key()),
			},
		},
		Actions: []RuleActionSpec{{Name: "approve-prime"}},
	})

	return mustCompileWorkspace(t, workspace)
}

func addDecisionAction(t testing.TB, workspace *Workspace, name string, decisionKey TemplateKey, applicantIDSlot int, trace *[]string, ruleName, outcome, reason, tier string) {
	t.Helper()

	outcomeValue := mustValue(t, outcome)
	reasonValue := mustValue(t, reason)
	tierValue := mustValue(t, tier)
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: name,
		Fn: func(ctx ActionContext) error {
			idValue, ok := ctx.bindingScalarValueAtSlot(0, applicantIDSlot)
			if !ok {
				t.Fatalf("action %s missing applicant id", name)
			}
			if idValue.Kind() != ValueString {
				t.Fatalf("action %s applicant id kind = %s, want %s", name, idValue.Kind(), ValueString)
			}

			if trace != nil {
				id := idValue.stringValue
				*trace = append(*trace,
					"FIRED|"+ruleName+"|"+id,
					"DECISION|"+id+"|"+outcome+"|"+reason+"|"+tier,
				)
			}
			_, err := ctx.AssertTemplate(decisionKey, Fields{
				"applicant-id": idValue,
				"outcome":      outcomeValue,
				"reason":       reasonValue,
				"tier":         tierValue,
			})
			return err
		},
	})
}

func loanUnderwritingInitialFacts(t testing.TB) []SessionInitialFact {
	t.Helper()

	return []SessionInitialFact{
		{Name: "applicant", TemplateKey: "applicant", Fields: mustFields(t, map[string]any{"id": "A-100", "age": 42, "country": "US"})},
		{Name: "financial", TemplateKey: "financial", Fields: mustFields(t, map[string]any{"applicant-id": "A-100", "annual-income": 148000, "monthly-debt": 1800, "credit-score": 755})},
		{Name: "employment", TemplateKey: "employment", Fields: mustFields(t, map[string]any{"applicant-id": "A-100", "status": "employed", "months": 86})},
		{Name: "applicant", TemplateKey: "applicant", Fields: mustFields(t, map[string]any{"id": "A-200", "age": 37, "country": "US"})},
		{Name: "financial", TemplateKey: "financial", Fields: mustFields(t, map[string]any{"applicant-id": "A-200", "annual-income": 92000, "monthly-debt": 4100, "credit-score": 675})},
		{Name: "employment", TemplateKey: "employment", Fields: mustFields(t, map[string]any{"applicant-id": "A-200", "status": "employed", "months": 28})},
		{Name: "applicant", TemplateKey: "applicant", Fields: mustFields(t, map[string]any{"id": "A-300", "age": 19, "country": "GB"})},
		{Name: "financial", TemplateKey: "financial", Fields: mustFields(t, map[string]any{"applicant-id": "A-300", "annual-income": 44000, "monthly-debt": 600, "credit-score": 580})},
		{Name: "employment", TemplateKey: "employment", Fields: mustFields(t, map[string]any{"applicant-id": "A-300", "status": "student", "months": 3})},
		{Name: "applicant", TemplateKey: "applicant", Fields: mustFields(t, map[string]any{"id": "A-400", "age": 55, "country": "US"})},
		{Name: "financial", TemplateKey: "financial", Fields: mustFields(t, map[string]any{"applicant-id": "A-400", "annual-income": 210000, "monthly-debt": 900, "credit-score": 690})},
		{Name: "employment", TemplateKey: "employment", Fields: mustFields(t, map[string]any{"applicant-id": "A-400", "status": "employed", "months": 140})},
		{Name: "risk-flag", TemplateKey: "risk-flag", Fields: mustFields(t, map[string]any{"applicant-id": "A-400", "code": "sanctions", "severity": "high"})},
		{Name: "applicant", TemplateKey: "applicant", Fields: mustFields(t, map[string]any{"id": "A-500", "age": 17, "country": "US"})},
		{Name: "financial", TemplateKey: "financial", Fields: mustFields(t, map[string]any{"applicant-id": "A-500", "annual-income": 28000, "monthly-debt": 200, "credit-score": 710})},
		{Name: "employment", TemplateKey: "employment", Fields: mustFields(t, map[string]any{"applicant-id": "A-500", "status": "employed", "months": 14})},
	}
}

func loanUnderwritingTemplateInitialFacts(t testing.TB) []SessionInitialFact {
	t.Helper()

	initials := loanUnderwritingInitialFacts(t)
	for i := range initials {
		initials[i].Name = ""
	}
	return initials
}

func expectedLoanUnderwritingTrace() []string {
	return []string{
		"FIRED|hard-stop-sanctions|A-400",
		"DECISION|A-400|decline|sanctions|ineligible",
		"FIRED|hard-stop-underage|A-500",
		"DECISION|A-500|decline|underage|ineligible",
		"FIRED|manual-review-low-credit|A-300",
		"DECISION|A-300|review|low-credit|analyst",
		"FIRED|manual-review-high-debt|A-200",
		"DECISION|A-200|review|high-debt|analyst",
		"FIRED|approve-prime-employed|A-100",
		"DECISION|A-100|approve|prime-employed|prime",
	}
}

func filterLoanUnderwritingTrace(output string) []string {
	lines := strings.Split(output, "\n")
	trace := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "FIRED|") || strings.HasPrefix(line, "DECISION|") {
			trace = append(trace, line)
		}
	}
	return trace
}
