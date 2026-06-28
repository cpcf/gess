package engine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type explicitConditionCase struct {
	rules    int
	explicit int
}

var benchmarkExplicitConditionRulesetID RulesetID

func BenchmarkGessExplicitConditionCompile(b *testing.B) {
	cases := []explicitConditionCase{
		{rules: 64, explicit: 16},
		{rules: 256, explicit: 64},
		{rules: 1024, explicit: 256},
	}

	for _, tc := range cases {
		b.Run(fmt.Sprintf("rules=%d/explicit=%d", tc.rules, tc.explicit), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(tc.rules), "rules")
			b.ReportMetric(float64(tc.explicit), "explicit")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				revision := mustCompileExplicitConditionRuleset(b, tc)
				benchmarkExplicitConditionRulesetID = revision.ID()
			}
		})
	}
}

func TestExplicitConditionCompileHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("GESS_EXPLICIT_CONDITION_RUNNER")) == "" {
		t.Skip("set GESS_EXPLICIT_CONDITION_RUNNER=1 to run benchmark harness")
	}

	iterations := explicitConditionHarnessEnvInt(t, "GESS_EXPLICIT_CONDITION_ITERATIONS", 3)
	warmup := explicitConditionHarnessEnvInt(t, "GESS_EXPLICIT_CONDITION_WARMUP", 1)
	tc := explicitConditionCase{
		rules:    explicitConditionHarnessEnvInt(t, "GESS_EXPLICIT_CONDITION_RULES", 256),
		explicit: explicitConditionHarnessEnvInt(t, "GESS_EXPLICIT_CONDITION_EXPLICIT", 64),
	}
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("warmup must be non-negative, got %d", warmup)
	}

	for range warmup {
		validateExplicitConditionRuleset(t, mustCompileExplicitConditionRuleset(t, tc), tc)
	}

	var elapsed time.Duration
	for range iterations {
		start := time.Now()
		revision := mustCompileExplicitConditionRuleset(t, tc)
		elapsed += time.Since(start)
		validateExplicitConditionRuleset(t, revision, tc)
	}
	nsPerOp := float64(elapsed.Nanoseconds()) / float64(iterations)

	fmt.Printf(
		"GESS_RUNNER|explicit-condition|compile|rules=%d|explicit=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f\n",
		tc.rules, tc.explicit, iterations, warmup, elapsed.Nanoseconds(), nsPerOp)
}

func mustCompileExplicitConditionRuleset(t testing.TB, tc explicitConditionCase) *Ruleset {
	t.Helper()
	if tc.rules < 0 || tc.explicit < 0 || tc.explicit > tc.rules {
		t.Fatalf("invalid explicit condition case: %#v", tc)
	}

	workspace := NewWorkspace()
	template := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "explicit-person",
		Fields: []FieldSpec{
			{Name: "kind", Kind: ValueString, Required: true},
			{Name: "value", Kind: ValueInt, Required: true},
		},
	})
	mustAddAction(t, workspace, ActionSpec{Name: "mark", Fn: func(ActionContext) error { return nil }})

	for i := 0; i < tc.rules; i++ {
		match := Match{
			Binding: "person",
			FieldConstraints: []FieldConstraintSpec{
				{Field: "kind", Operator: FieldConstraintEqual, Value: explicitConditionKind(i)},
			},
			Target: TemplateKeyFact(template.Key()),
		}
		var condition ConditionSpec = match
		if i < tc.explicit {
			condition = Explicit{Condition: match}
		}
		mustAddRule(t, workspace, RuleSpec{
			Name:          fmt.Sprintf("explicit-condition-%04d", i),
			ConditionTree: condition,
			Actions:       []RuleActionSpec{{Name: "mark"}},
		})
	}

	return mustCompileWorkspace(t, workspace)
}

func validateExplicitConditionRuleset(t testing.TB, revision *Ruleset, tc explicitConditionCase) {
	t.Helper()

	if got := len(revision.Rules()); got != tc.rules {
		t.Fatalf("rules = %d, want %d", got, tc.rules)
	}
	explicit := 0
	for _, rule := range revision.Rules() {
		conditions := rule.Conditions()
		if len(conditions) != 1 {
			t.Fatalf("rule %q conditions = %d, want 1", rule.Name(), len(conditions))
		}
		if conditions[0].Explicit() {
			explicit++
		}
	}
	if explicit != tc.explicit {
		t.Fatalf("explicit conditions = %d, want %d", explicit, tc.explicit)
	}
}

func explicitConditionKind(index int) string {
	return fmt.Sprintf("kind-%04d", index)
}

func explicitConditionHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
