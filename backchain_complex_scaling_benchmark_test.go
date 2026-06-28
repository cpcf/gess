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

const (
	complexBackchainMinEdges = 2
	complexBackchainMaxEdges = 7
)

var complexBackchainClasses = [...]string{"critical", "lateral", "privilege"}

type complexBackchainTemplates struct {
	host     TemplateKey
	link     TemplateKey
	vuln     TemplateKey
	suppress TemplateKey
	route    TemplateKey
}

func BenchmarkGessBackchainComplexScaling(b *testing.B) {
	ctx := context.Background()
	revision, templates := mustCompileComplexBackchainRuleset(b)
	cases := []struct {
		facts   int
		matches int
		misses  int
	}{
		{facts: 1000, matches: 100, misses: 25},
		{facts: 10000, matches: 1000, misses: 250},
		{facts: 50000, matches: 5000, misses: 1250},
	}
	for _, tc := range cases {
		b.Run(fmt.Sprintf("facts=%d/matches=%d/misses=%d", tc.facts, tc.matches, tc.misses), func(b *testing.B) {
			session, err := NewSession(revision, WithResetBeforeSnapshot(false))
			if err != nil {
				b.Fatalf("NewSession: %v", err)
			}
			actualFacts := complexBackchainSeedFactCount(tc.facts, tc.matches, tc.misses)
			b.ReportAllocs()
			b.ReportMetric(float64(actualFacts), "facts")
			b.ReportMetric(float64(tc.matches), "matches")
			b.ReportMetric(float64(tc.misses), "misses")
			b.ReportMetric(float64(complexBackchainDerivedRoutes(tc.matches)), "derived-route-facts")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Reset(ctx); err != nil {
					b.Fatalf("Reset: %v", err)
				}
				seedComplexBackchainFacts(b, ctx, session, templates, tc.facts, tc.matches, tc.misses)
				queryComplexBackchainRoutes(b, ctx, session, tc.matches, tc.misses)
			}
		})
	}
}

func TestBackchainComplexScalingHarness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_COMPLEX_RUNNER")) == "" {
		t.Skip("set GESS_BACKCHAIN_COMPLEX_RUNNER=1 to run benchmark harness")
	}
	iterations := complexBackchainHarnessEnvInt(t, "GESS_BACKCHAIN_COMPLEX_ITERATIONS", 10)
	warmup := complexBackchainHarnessEnvInt(t, "GESS_BACKCHAIN_COMPLEX_WARMUP", 2)
	facts := complexBackchainHarnessEnvInt(t, "GESS_BACKCHAIN_COMPLEX_FACTS", 10000)
	matches := complexBackchainHarnessEnvInt(t, "GESS_BACKCHAIN_COMPLEX_MATCHES", 1000)
	misses := complexBackchainHarnessEnvInt(t, "GESS_BACKCHAIN_COMPLEX_MISSES", matches/4)
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	if warmup < 0 {
		t.Fatalf("warmup must be non-negative, got %d", warmup)
	}
	actualFacts := complexBackchainSeedFactCount(facts, matches, misses)
	measurePhases := strings.TrimSpace(os.Getenv("GESS_BACKCHAIN_COMPLEX_PHASES")) != ""

	ctx := context.Background()
	revision, templates := mustCompileComplexBackchainRuleset(t)
	session, err := NewSession(revision, WithResetBeforeSnapshot(false))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for range warmup {
		runComplexBackchainCase(t, ctx, session, templates, facts, matches, misses)
	}

	var elapsed, resetElapsed, seedElapsed, proveElapsed time.Duration
	for range iterations {
		start := time.Now()
		resetStart := time.Now()
		if _, err := session.Reset(ctx); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		resetElapsed += time.Since(resetStart)
		seedStart := time.Now()
		seedComplexBackchainFacts(t, ctx, session, templates, facts, matches, misses)
		seedElapsed += time.Since(seedStart)
		proveStart := time.Now()
		queryComplexBackchainRoutes(t, ctx, session, matches, misses)
		proveElapsed += time.Since(proveStart)
		elapsed += time.Since(start)
	}
	nsPerOp := float64(elapsed.Nanoseconds()) / float64(iterations)
	fields := []string{
		fmt.Sprintf("GESS_RUNNER|backchain-complex-scaling|query|facts=%d|matches=%d|misses=%d|derived-route-facts=%d|iterations=%d|warmup=%d|elapsed-ns=%d|ns/op=%.0f",
			actualFacts, matches, misses, complexBackchainDerivedRoutes(matches), iterations, warmup, elapsed.Nanoseconds(), nsPerOp),
	}
	if measurePhases {
		fields = append(fields,
			fmt.Sprintf("reset-ns/op=%.0f", float64(resetElapsed.Nanoseconds())/float64(iterations)),
			fmt.Sprintf("seed-ns/op=%.0f", float64(seedElapsed.Nanoseconds())/float64(iterations)),
			fmt.Sprintf("prove-ns/op=%.0f", float64(proveElapsed.Nanoseconds())/float64(iterations)),
		)
	}
	fmt.Println(strings.Join(fields, "|"))
}

func runComplexBackchainCase(t testing.TB, ctx context.Context, session *Session, templates complexBackchainTemplates, facts, matches, misses int) {
	t.Helper()
	if _, err := session.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	seedComplexBackchainFacts(t, ctx, session, templates, facts, matches, misses)
	queryComplexBackchainRoutes(t, ctx, session, matches, misses)
}

func queryComplexBackchainRoutes(t testing.TB, ctx context.Context, session *Session, matches, misses int) {
	t.Helper()
	for i := range matches {
		route := complexBackchainMatchRoute(i)
		rows, err := session.QueryAll(ctx, "complex-route", QueryArgs{
			"src":   route.src,
			"dst":   route.dst,
			"class": route.class,
		})
		if err != nil {
			t.Fatalf("QueryAll(match %d): %v", i, err)
		}
		if len(rows) != 1 {
			t.Fatalf("match %d rows = %d, want 1", i, len(rows))
		}
		assertQueryRowStringValue(t, rows[0], "src", route.src)
		assertQueryRowStringValue(t, rows[0], "dst", route.dst)
		assertQueryRowStringValue(t, rows[0], "class", route.class)
	}
	for i := range misses {
		route := complexBackchainMissRoute(i)
		rows, err := session.QueryAll(ctx, "complex-route", QueryArgs{
			"src":   route.src,
			"dst":   route.dst,
			"class": route.class,
		})
		if err != nil {
			t.Fatalf("QueryAll(miss %d): %v", i, err)
		}
		if len(rows) != 0 {
			t.Fatalf("miss %d rows = %d, want 0", i, len(rows))
		}
	}
}

func mustCompileComplexBackchainRuleset(t testing.TB) (*Ruleset, complexBackchainTemplates) {
	t.Helper()
	workspace := NewWorkspace()
	host := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "complex-host",
		Fields: []FieldSpec{
			{Name: "id", Kind: ValueString, Required: true},
			{Name: "zone", Kind: ValueString, Required: true},
			{Name: "tier", Kind: ValueString, Required: true},
		},
	})
	link := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "complex-link",
		Fields: []FieldSpec{
			{Name: "src", Kind: ValueString, Required: true},
			{Name: "dst", Kind: ValueString, Required: true},
			{Name: "proto", Kind: ValueString, Required: true},
			{Name: "active", Kind: ValueString, Required: true},
		},
	})
	vuln := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "complex-vuln",
		Fields: []FieldSpec{
			{Name: "host", Kind: ValueString, Required: true},
			{Name: "class", Kind: ValueString, Required: true},
			{Name: "severity", Kind: ValueString, Required: true},
		},
	})
	suppress := mustAddTemplate(t, workspace, TemplateSpec{
		Name: "complex-suppress",
		Fields: []FieldSpec{
			{Name: "src", Kind: ValueString, Required: true},
			{Name: "dst", Kind: ValueString, Required: true},
		},
	})
	route := mustAddTemplate(t, workspace, TemplateSpec{
		Name:              "complex-route",
		BackchainReactive: true,
		DuplicatePolicy:   DuplicateUniqueKey,
		DuplicateKeyNames: []string{"src", "dst", "class"},
		Fields: []FieldSpec{
			{Name: "src", Kind: ValueString, Required: true},
			{Name: "dst", Kind: ValueString, Required: true},
			{Name: "class", Kind: ValueString, Required: true},
		},
	})
	templates := complexBackchainTemplates{
		host:     host.Key(),
		link:     link.Key(),
		vuln:     vuln.Key(),
		suppress: suppress.Key(),
		route:    route.Key(),
	}
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-complex-direct-route",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: route.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "need", Field: "class"},
				BindingFieldExpr{Binding: "need", Field: "dst"},
				BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	})
	mustAddInternalAction(t, workspace, ActionSpec{
		Name: "assert-complex-transitive-route",
		AssertTemplateValues: &AssertTemplateValuesActionSpec{
			TemplateKey: route.Key(),
			Values: []ExpressionSpec{
				BindingFieldExpr{Binding: "need", Field: "class"},
				BindingFieldExpr{Binding: "need", Field: "dst"},
				BindingFieldExpr{Binding: "need", Field: "src"},
			},
		},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "complex-direct-route",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "need", Target: TemplateKeyFact(TemplateKey("need-complex-route"))},
			Match{
				Binding: "link",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: "true"},
					{Field: "proto", Operator: FieldConstraintEqual, Value: "https"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: TemplateKeyFact(link.Key()),
			},
			Match{
				Binding: "host",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "zone", Operator: FieldConstraintEqual, Value: "prod"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: TemplateKeyFact(host.Key()),
			},
			Match{
				Binding: "vuln",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "severity", Operator: FieldConstraintEqual, Value: "high"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "host", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
					{Field: "class", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "class"}},
				},
				Target: TemplateKeyFact(vuln.Key()),
			},
			Not{Condition: Match{
				Binding: "suppress",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
				},
				Target: TemplateKeyFact(suppress.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "assert-complex-direct-route"}},
	})
	mustAddRule(t, workspace, RuleSpec{
		Name: "complex-transitive-route",
		ConditionTree: And{Conditions: []ConditionSpec{
			Match{Binding: "need", Target: TemplateKeyFact(TemplateKey("need-complex-route"))},
			Match{
				Binding: "link",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "active", Operator: FieldConstraintEqual, Value: "true"},
					{Field: "proto", Operator: FieldConstraintEqual, Value: "https"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "src"}},
				},
				Target: TemplateKeyFact(link.Key()),
			},
			Match{
				Binding: "hop",
				FieldConstraints: []FieldConstraintSpec{
					{Field: "zone", Operator: FieldConstraintEqual, Value: "prod"},
				},
				JoinConstraints: []JoinConstraintSpec{
					{Field: "id", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "link", Field: "dst"}},
				},
				Target: TemplateKeyFact(host.Key()),
			},
			Match{
				Binding: "tail",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "link", Field: "dst"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "dst"}},
					{Field: "class", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "class"}},
				},
				Target: TemplateKeyFact(route.Key()),
			},
			Not{Condition: Match{
				Binding: "suppress",
				JoinConstraints: []JoinConstraintSpec{
					{Field: "src", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "need", Field: "src"}},
					{Field: "dst", Operator: FieldConstraintEqual, Ref: FieldRef{Binding: "link", Field: "dst"}},
				},
				Target: TemplateKeyFact(suppress.Key()),
			}},
		}},
		Actions: []RuleActionSpec{{Name: "assert-complex-transitive-route"}},
	})
	if err := workspace.AddQuery(QuerySpec{
		Name: "complex-route",
		Parameters: []QueryParameterSpec{
			{Name: "src", Kind: ValueString},
			{Name: "dst", Kind: ValueString},
			{Name: "class", Kind: ValueString},
		},
		ConditionTree: Match{
			Binding: "route",
			Predicates: []ExpressionSpec{
				CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "src"}, Right: ParamExpr{Name: "src"}},
				CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "dst"}, Right: ParamExpr{Name: "dst"}},
				CompareExpr{Operator: ExpressionCompareEqual, Left: CurrentFieldExpr{Field: "class"}, Right: ParamExpr{Name: "class"}},
			},
			Target: TemplateKeyFact(route.Key()),
		},
		Returns: []QueryReturnSpec{
			ReturnValue("src", BindingFieldExpr{Binding: "route", Field: "src"}),
			ReturnValue("dst", BindingFieldExpr{Binding: "route", Field: "dst"}),
			ReturnValue("class", BindingFieldExpr{Binding: "route", Field: "class"}),
		},
	}); err != nil {
		t.Fatalf("AddQuery(complex-route): %v", err)
	}
	revision, err := workspace.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return revision, templates
}

type complexBackchainRouteCase struct {
	src   string
	dst   string
	class string
	nodes []string
}

func seedComplexBackchainFacts(t testing.TB, ctx context.Context, session *Session, templates complexBackchainTemplates, requestedFacts, matches, misses int) {
	t.Helper()
	actualFacts := 0
	for i := range matches {
		route := complexBackchainMatchRoute(i)
		for nodeIndex, hostID := range route.nodes {
			tier := "app"
			if nodeIndex == len(route.nodes)-1 {
				tier = "db"
			}
			assertComplexHost(t, ctx, session, templates.host, hostID, "prod", tier)
			actualFacts++
		}
		for edgeIndex := 0; edgeIndex < len(route.nodes)-1; edgeIndex++ {
			assertComplexLink(t, ctx, session, templates.link, route.nodes[edgeIndex], route.nodes[edgeIndex+1], "https", true)
			actualFacts++
		}
		assertComplexVuln(t, ctx, session, templates.vuln, route.dst, route.class, "high")
		actualFacts++
	}
	for i := range misses {
		route := complexBackchainMissRoute(i)
		assertComplexHost(t, ctx, session, templates.host, route.dst, "prod", "db")
		assertComplexLink(t, ctx, session, templates.link, route.src, route.dst, "https", true)
		assertComplexVuln(t, ctx, session, templates.vuln, route.dst, route.class, "high")
		assertComplexSuppress(t, ctx, session, templates.suppress, route.src, route.dst)
		actualFacts += 4
	}

	for id := 0; actualFacts < requestedFacts; id++ {
		host := fmt.Sprintf("x%06d", id)
		next := fmt.Sprintf("x%06d", id+1)
		switch id % 5 {
		case 0:
			assertComplexHost(t, ctx, session, templates.host, host, "dev", "app")
		case 1:
			assertComplexLink(t, ctx, session, templates.link, host, next, "https", false)
		case 2:
			assertComplexLink(t, ctx, session, templates.link, host, next, "ssh", true)
		case 3:
			assertComplexVuln(t, ctx, session, templates.vuln, host, "low", "medium")
		default:
			assertComplexSuppress(t, ctx, session, templates.suppress, host, next)
		}
		actualFacts++
	}
}

func complexBackchainMatchRoute(index int) complexBackchainRouteCase {
	edges := complexBackchainMinEdges + index%(complexBackchainMaxEdges-complexBackchainMinEdges+1)
	position := 0
	nodes := make([]string, 0, edges+1)
	nodes = append(nodes, complexBackchainMatchHostID(index, position))
	for edge := range edges {
		position += 1 + (index+edge)%4
		nodes = append(nodes, complexBackchainMatchHostID(index, position))
	}
	class := complexBackchainClasses[index%len(complexBackchainClasses)]
	return complexBackchainRouteCase{
		src:   nodes[0],
		dst:   nodes[len(nodes)-1],
		class: class,
		nodes: nodes,
	}
}

func complexBackchainMissRoute(index int) complexBackchainRouteCase {
	class := complexBackchainClasses[index%len(complexBackchainClasses)]
	return complexBackchainRouteCase{
		src:   fmt.Sprintf("b%06d_src", index),
		dst:   fmt.Sprintf("b%06d_dst", index),
		class: class,
	}
}

func complexBackchainMatchHostID(routeIndex, position int) string {
	return fmt.Sprintf("m%06d_%03d", routeIndex, position)
}

func complexBackchainSeedFactCount(requestedFacts, matches, misses int) int {
	minimum := complexBackchainMinimumFacts(matches, misses)
	if requestedFacts < minimum {
		return minimum
	}
	return requestedFacts
}

func complexBackchainMinimumFacts(matches, misses int) int {
	total := misses * 4
	for i := range matches {
		edges := complexBackchainMinEdges + i%(complexBackchainMaxEdges-complexBackchainMinEdges+1)
		total += (edges + 1) + edges + 1
	}
	return total
}

func complexBackchainDerivedRoutes(matches int) int {
	total := 0
	for i := range matches {
		total += complexBackchainMinEdges + i%(complexBackchainMaxEdges-complexBackchainMinEdges+1)
	}
	return total
}

func assertComplexHost(t testing.TB, ctx context.Context, session *Session, key TemplateKey, id, zone, tier string) {
	t.Helper()
	if err := session.AssertTemplateValues(ctx, key, newStringValue(id), newStringValue(tier), newStringValue(zone)); err != nil {
		t.Fatalf("AssertTemplateValues(complex-host): %v", err)
	}
}

func assertComplexLink(t testing.TB, ctx context.Context, session *Session, key TemplateKey, src, dst, proto string, active bool) {
	t.Helper()
	activeValue := "false"
	if active {
		activeValue = "true"
	}
	if err := session.AssertTemplateValues(ctx, key, newStringValue(activeValue), newStringValue(dst), newStringValue(proto), newStringValue(src)); err != nil {
		t.Fatalf("AssertTemplateValues(complex-link): %v", err)
	}
}

func assertComplexVuln(t testing.TB, ctx context.Context, session *Session, key TemplateKey, host, class, severity string) {
	t.Helper()
	if err := session.AssertTemplateValues(ctx, key, newStringValue(class), newStringValue(host), newStringValue(severity)); err != nil {
		t.Fatalf("AssertTemplateValues(complex-vuln): %v", err)
	}
}

func assertComplexSuppress(t testing.TB, ctx context.Context, session *Session, key TemplateKey, src, dst string) {
	t.Helper()
	if err := session.AssertTemplateValues(ctx, key, newStringValue(dst), newStringValue(src)); err != nil {
		t.Fatalf("AssertTemplateValues(complex-suppress): %v", err)
	}
}

func complexBackchainHarnessEnvInt(t testing.TB, name string, defaultValue int) int {
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
