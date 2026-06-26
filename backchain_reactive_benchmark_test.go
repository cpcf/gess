package gess

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type backchainReactiveCase struct {
	templates int
	reactive  int
}

var benchmarkBackchainReactiveRulesetID RulesetID

func BenchmarkGessBackchainReactiveCompile(b *testing.B) {
	cases := []backchainReactiveCase{
		{templates: 16, reactive: 4},
		{templates: 64, reactive: 16},
		{templates: 256, reactive: 64},
	}

	for _, tc := range cases {
		b.Run(fmt.Sprintf("templates=%d/reactive=%d", tc.templates, tc.reactive), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(tc.templates), "templates")
			b.ReportMetric(float64(tc.reactive), "reactive")
			b.ReportMetric(float64(tc.reactive), "generated-demand-templates")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				revision := mustCompileBackchainReactiveRuleset(b, tc)
				benchmarkBackchainReactiveRulesetID = revision.ID()
			}
		})
	}
}

func TestBackchainReactiveCompileHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_REACTIVE_RUNNER")) == "" {
		t.Skip("set GESS_BACKCHAIN_REACTIVE_RUNNER=1 to run benchmark harness")
	}

	iterations := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_ITERATIONS", 3)
	warmup := backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_WARMUP", 1)
	tc := backchainReactiveCase{
		templates: backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_TEMPLATES", 64),
		reactive:  backchainReactiveHarnessEnvInt(t, "GESS_BACKCHAIN_REACTIVE_REACTIVE", 16),
	}
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("warmup must be non-negative, got %d", warmup)
	}

	for range warmup {
		validateBackchainReactiveRuleset(t, mustCompileBackchainReactiveRuleset(t, tc), tc)
	}

	var elapsed time.Duration
	for range iterations {
		start := time.Now()
		revision := mustCompileBackchainReactiveRuleset(t, tc)
		elapsed += time.Since(start)
		validateBackchainReactiveRuleset(t, revision, tc)
	}
	nsPerOp := float64(elapsed.Nanoseconds()) / float64(iterations)

	fmt.Printf(
		"GESS_RUNNER|backchain-reactive|compile|templates=%d|reactive=%d|generated-demand-templates=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f\n",
		tc.templates, tc.reactive, tc.reactive, iterations, warmup, elapsed.Nanoseconds(), nsPerOp)
}

func mustCompileBackchainReactiveRuleset(t testing.TB, tc backchainReactiveCase) *Ruleset {
	t.Helper()
	if tc.templates < 0 || tc.reactive < 0 || tc.reactive > tc.templates {
		t.Fatalf("invalid backchain reactive case: %#v", tc)
	}

	workspace := NewWorkspace()
	for i := 0; i < tc.templates; i++ {
		if err := workspace.AddTemplate(TemplateSpec{
			Name:              fmt.Sprintf("bc-answer-%03d", i),
			BackchainReactive: i < tc.reactive,
			Fields: []FieldSpec{
				{Name: "question", Kind: ValueString, Required: true},
				{Name: "value", Kind: ValueInt, Required: true},
				{Name: "source", Kind: ValueString},
			},
		}); err != nil {
			t.Fatalf("AddTemplate(%d): %v", i, err)
		}
	}

	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision
}

func validateBackchainReactiveRuleset(t testing.TB, revision *Ruleset, tc backchainReactiveCase) {
	t.Helper()

	templates := revision.Templates()
	if got, want := len(templates), tc.templates+tc.reactive; got != want {
		t.Fatalf("template count = %d, want %d", got, want)
	}

	reactive := 0
	demand := 0
	for _, template := range templates {
		if template.BackchainReactive() {
			reactive++
			if _, ok := template.BackchainDemandTemplateKey(); !ok {
				t.Fatalf("reactive template %q missing demand key", template.Name())
			}
		}
		if template.IsBackchainDemandTemplate() {
			demand++
			if _, ok := template.BackchainSourceTemplateKey(); !ok {
				t.Fatalf("demand template %q missing source key", template.Name())
			}
		}
	}
	if reactive != tc.reactive {
		t.Fatalf("reactive templates = %d, want %d", reactive, tc.reactive)
	}
	if demand != tc.reactive {
		t.Fatalf("demand templates = %d, want %d", demand, tc.reactive)
	}
}

func backchainReactiveHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
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
