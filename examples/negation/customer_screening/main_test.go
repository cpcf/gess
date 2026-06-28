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
	if got := out.String(); got != "C-100: eligible\n" {
		t.Fatalf("output = %q, want only C-100 eligible", got)
	}
	if strings.Contains(out.String(), "C-200") || strings.Contains(out.String(), "C-300") {
		t.Fatalf("blocked or inactive customer was marked eligible:\n%s", out.String())
	}
}
