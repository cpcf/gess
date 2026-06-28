package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatFileReportsChangeWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.gess")
	original := []byte("(deftemplate customer (slot id (type STRING) (required TRUE)))\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := formatFile(path, false, true)
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("file was modified without -w: %q", got)
	}
}

func TestFormatFileWritesFormattedSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.gess")
	if err := os.WriteFile(path, []byte("(deftemplate customer (slot id (type STRING) (required TRUE)))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := formatFile(path, true, false)
	if err != nil {
		t.Fatalf("formatFile: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = `(deftemplate customer
  (slot id (type STRING) (required TRUE))
)
`
	if string(got) != want {
		t.Fatalf("formatted =\n%s\nwant =\n%s", got, want)
	}
}
