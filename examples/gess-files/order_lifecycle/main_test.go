package main

import (
	"bytes"
	"testing"
)

func TestRunModifiesRetractsAndEmits(t *testing.T) {
	var out bytes.Buffer
	if err := run(&out); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "order O-2 cancelled\n" +
		"order O-1 shipped from W-1 total 108\n" +
		"shipped: O-1 total 108\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
