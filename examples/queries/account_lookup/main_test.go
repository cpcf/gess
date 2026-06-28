package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"A-100: balance 120000\n",
		"A-200: balance 9000\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "A-300") {
		t.Fatalf("query should be scoped to EMEA:\n%s", out.String())
	}
}
