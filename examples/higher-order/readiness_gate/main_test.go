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
	if got := out.String(); got != "checkout 2026.06.1: gate open\n" {
		t.Fatalf("output = %q", got)
	}
	if strings.Contains(out.String(), "billing") {
		t.Fatalf("failed check should keep billing gate closed:\n%s", out.String())
	}
}
