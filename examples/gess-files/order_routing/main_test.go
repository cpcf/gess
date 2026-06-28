package main

import (
	"bytes"
	"testing"
)

func TestRunRoutesOrdersFromGessFile(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "O-100 -> W-1\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
