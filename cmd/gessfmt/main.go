package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cpcf/gess/internal/gesssexp"
)

func main() {
	var write bool
	var list bool
	var check bool
	flag.BoolVar(&write, "w", false, "write result to source files instead of stdout")
	flag.BoolVar(&list, "l", false, "list files whose formatting differs")
	flag.BoolVar(&check, "check", false, "exit non-zero if any file is not formatted")
	flag.Parse()

	if flag.NArg() == 0 {
		if write || list || check {
			fmt.Fprintln(os.Stderr, "gessfmt: -w, -l, and -check require file arguments")
			os.Exit(2)
		}
		if err := formatReader("<stdin>", os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	different := false
	for _, path := range flag.Args() {
		changed, err := formatFile(path, write, list || check)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if changed {
			different = true
			if list || check {
				fmt.Println(path)
			}
		}
	}
	if check && different {
		os.Exit(1)
	}
}

func formatReader(name string, r io.Reader, w io.Writer) error {
	source, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if gesssexp.SourceHasComments(source) {
		fmt.Fprintf(os.Stderr, "gessfmt: warning: %s contains ';' comments; formatted output omits them\n", name)
	}
	formatted, err := gesssexp.Format(name, source)
	if err != nil {
		return err
	}
	_, err = w.Write(formatted)
	return err
}

func formatFile(path string, write bool, quiet bool) (bool, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if gesssexp.SourceHasComments(source) {
		if write {
			return false, fmt.Errorf("%s: refusing -w: the file contains ';' comments and gessfmt does not preserve comments yet; format to stdout instead", path)
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "gessfmt: warning: %s contains ';' comments; formatted output omits them\n", path)
		}
	}
	formatted, err := gesssexp.Format(path, source)
	if err != nil {
		return false, err
	}
	changed := !bytes.Equal(source, formatted)
	if !changed {
		return false, nil
	}
	if write {
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			return false, fmt.Errorf("write %s: %w", path, err)
		}
		return true, nil
	}
	if quiet {
		return true, nil
	}
	_, err = os.Stdout.Write(formatted)
	return true, err
}
