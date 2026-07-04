package repl

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptedOrderRoutingSession(t *testing.T) {
	rulesPath := rootPath("examples/gess-files/order_routing/rules.gess")
	script := strings.Join([]string{
		"load " + rulesPath,
		"rules",
		"rule route-vip-order",
		"agenda",
		"run 1",
		"facts fulfillment-route",
		"query routes-by-lane lane=expedite",
		"assert inventory sku=SKU-3 warehouse=W-3 available=true",
		"assert order id=O-400 customer=C-100 sku=SKU-3",
		"run",
		"query routes-by-lane lane=expedite",
		"focus",
		"focus MAIN",
		"focus pop",
		"focus clear",
		"diag",
		"reset",
		"facts fulfillment-route",
		"reload",
		"exit",
	}, "\n")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(script), &out, Options{}); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	got := out.String()
	checks := []string{
		"loaded " + rulesPath + ": templates=4 rules=1 queries=1 deffacts=8\n",
		"rules count=1\nroute-vip-order module=MAIN salience=0\n",
		"rule route-vip-order module=MAIN salience=0 conditions=3 actions=1\n",
		"agenda count=1",
		"run status=completed fired=1\n",
		"facts count=1\nid=fact:g1:9 template=fulfillment-route lane=expedite order=O-100 warehouse=W-1\n",
		"query routes-by-lane rows=1\norder=O-100 warehouse=W-1\n",
		"assert status=inserted id=fact:g1:10\n",
		"assert status=inserted id=fact:g1:11\n",
		"run status=completed fired=0\n",
		"query routes-by-lane rows=1\n",
		"focus current=MAIN stack=[MAIN]\n",
		"focus popped=MAIN current=MAIN\n",
		"diag memory-owners=",
		"reset status=applied generation=2\n",
		"facts count=0\n",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Fatalf("output missing %q\nfull output:\n%s", check, got)
		}
	}
}

func TestOrderRoutingGoldenOutput(t *testing.T) {
	rulesPath := rootPath("examples/gess-files/order_routing/rules.gess")
	script := strings.Join([]string{
		"load " + rulesPath,
		"run",
		"facts fulfillment-route",
		"query routes-by-lane lane=expedite",
		"reset",
		"facts fulfillment-route",
		"exit",
	}, "\n")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(script), &out, Options{}); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	want := "loaded " + rulesPath + ": templates=4 rules=1 queries=1 deffacts=8\n" +
		"run status=completed fired=1\n" +
		"facts count=1\n" +
		"id=fact:g1:9 template=fulfillment-route lane=expedite order=O-100 warehouse=W-1\n" +
		"query routes-by-lane rows=1\n" +
		"order=O-100 warehouse=W-1\n" +
		"reset status=applied generation=2\n" +
		"facts count=0\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestScriptedTutorialStubsMissingCalls(t *testing.T) {
	rulesPath := rootPath("tutorial/vulnerability_response/solution/rules.gess")
	var failed bytes.Buffer
	err := Run(context.Background(), strings.NewReader("load "+rulesPath+"\nexit\n"), &failed, Options{})
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("Run without stubs error = %v, want ErrCommandFailed", err)
	}
	if got := failed.String(); !strings.Contains(got, "error: missing registered calls: record-emergency") {
		t.Fatalf("missing call output = %q", got)
	}

	var out bytes.Buffer
	script := strings.Join([]string{
		"load " + rulesPath,
		"run",
		"query actions-by-lane lane=emergency",
		"query critical-summaries",
		"exit",
	}, "\n")
	if err := Run(context.Background(), strings.NewReader(script), &out, Options{StubCalls: true}); err != nil {
		t.Fatalf("Run with stubs: %v\n%s", err, out.String())
	}
	want := "loaded " + rulesPath + ": templates=5 rules=9 queries=2 deffacts=11\n" +
		"stub call record-emergency VULN-100 critical-exploitable-internet\n" +
		"run status=completed fired=9\n" +
		"query actions-by-lane rows=1\n" +
		"target=VULN-100 reason=critical-exploitable-internet\n" +
		"query critical-summaries rows=1\n" +
		"severity=critical count=2 total=195\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestMissingFunctionLoadErrorNamesFunction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-function.gess")
	source := []byte(`(deftemplate item
  (slot score (type INTEGER) (required TRUE))
)

(defrule needs-function
  (item (score ?score))
  (test (host-ok ?score))
  =>
  (assert (item (score ?score)))
)
`)
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("load "+path+"\nexit\n"), &out, Options{StubCalls: true})
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("Run error = %v, want ErrCommandFailed", err)
	}
	if got := out.String(); !strings.Contains(got, "unknown function \"host-ok\"") {
		t.Fatalf("missing function output = %q", got)
	}
}

func TestMalformedCommandsContinueAndReturnFailure(t *testing.T) {
	rulesPath := rootPath("examples/gess-files/order_routing/rules.gess")
	script := strings.Join([]string{
		"unknown",
		"facts",
		"load " + rulesPath,
		"assert order badfield",
		"assert missing id=X",
		"modify fact:g9:9 field=value",
		"retract fact:g9:9",
		"run nope",
		"query routes-by-lane badarg",
		"watch maybe",
		"watch on rule-fired",
		"watch on nope",
		"watch off",
		"help",
		"exit",
	}, "\n")
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader(script), &out, Options{})
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("Run error = %v, want ErrCommandFailed\n%s", err, out.String())
	}
	got := out.String()
	for _, check := range []string{
		"error: unknown command \"unknown\"\n",
		"error: no rules loaded\n",
		"error: bad field assignment \"badfield\"; want field=value\n",
		"error: unknown template \"missing\"\n",
		"error: unknown fact id \"fact:g9:9\"\n",
		"error: run limit must be a non-negative integer\n",
		"error: bad query argument \"badarg\"; want arg=value\n",
		"error: usage: watch on|off [types]\n",
		"error: unknown watch event type \"nope\"\n",
		"watch on\nwatch will apply on next load or reload\n",
		"watch off\nwatch will apply on next load or reload\n",
		"  load <file.gess>\n",
		"  exit\n",
		"piped mode exits non-zero if any command reports an error; the loop continues after command errors.\n",
	} {
		if !strings.Contains(got, check) {
			t.Fatalf("output missing %q\nfull output:\n%s", check, got)
		}
	}
}

func rootPath(path string) string {
	return filepath.Join("..", "..", "..", "..", path)
}
