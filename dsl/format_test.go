package dsl_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/internal/gesssexp"
)

func TestFormatMatchesGessfmtFormatterByteForByte(t *testing.T) {
	sources := [][]byte{
		[]byte("(defmodule MAIN)\n(defglobal ?*threshold*=10)\n"),
		[]byte("; leading\n(defrule example ; same line\n(foo (bar baz))\n=>\n(printout t \"ok\"))\n"),
		[]byte("(deffacts seed (order (id 1)))\n\n; tail\n"),
	}
	for _, source := range sources {
		want, err := gesssexp.Format("rules/example.gess", source)
		if err != nil {
			t.Fatalf("internal formatter error = %v", err)
		}
		got, err := dsl.Format("rules/example.gess", source)
		if err != nil {
			t.Fatalf("Format() error = %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Format() = %q, want gessfmt bytes %q", got, want)
		}
	}
}

func TestFormatReturnsPublicFileError(t *testing.T) {
	_, err := dsl.Format("rules/broken.gess", []byte("(defrule broken\n"))
	if err == nil {
		t.Fatal("Format() error = nil, want syntax error")
	}
	var fileErr *dsl.FileError
	if !errors.As(err, &fileErr) {
		t.Fatalf("Format() error = %T %v, want *dsl.FileError", err, err)
	}
	if fileErr.Span.Name != "rules/broken.gess" || fileErr.Span.StartLine < 1 || fileErr.Span.StartColumn < 1 || fileErr.Reason == "" {
		t.Fatalf("file error = %#v", fileErr)
	}
}
