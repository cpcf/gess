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
	if got := out.String(); got != "A-100: 3 transactions totaling 1200\n" {
		t.Fatalf("output = %q", got)
	}
	if strings.Contains(out.String(), "A-200") {
		t.Fatalf("single high-value transaction should not meet velocity rule:\n%s", out.String())
	}
}
