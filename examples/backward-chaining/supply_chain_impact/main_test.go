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
		"checkout affected by CVE-2026-4242: yes\n",
		"billing affected by CVE-2026-4242: no\n",
		"billing affected by CVE-2026-9000: no\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q\n%s", want, out.String())
		}
	}
}
