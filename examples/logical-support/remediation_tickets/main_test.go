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
		"tickets before retract: 1\n",
		"support edges before retract: 1\n",
		"tickets after retract: 0\n",
		"cascade retractions: 1\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q\n%s", want, out.String())
		}
	}
}
