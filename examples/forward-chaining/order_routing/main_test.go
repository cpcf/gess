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
		"fired: 2\n",
		"O-100 -> expedite via east\n",
		"O-200 -> standard via east\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "O-300") {
		t.Fatalf("unavailable SKU was routed:\n%s", out.String())
	}
}
