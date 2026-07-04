package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cpcf/gess/cmd/gess/internal/repl"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gess <command> [options]")
		fmt.Fprintln(os.Stderr, "commands: repl")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "repl":
		runRepl(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runRepl(args []string) {
	flags := flag.NewFlagSet("gess repl", flag.ExitOnError)
	stubCalls := flags.Bool("stub-calls", false, "stub missing registered call actions")
	noPrompt := flags.Bool("no-prompt", false, "suppress prompt even when stdin is a terminal")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gess repl [--stub-calls] [--no-prompt]")
		os.Exit(2)
	}
	interactive := isTerminal(os.Stdin) && !*noPrompt
	err := repl.Run(context.Background(), os.Stdin, os.Stdout, repl.Options{
		StubCalls:   *stubCalls,
		Interactive: interactive,
	})
	if errors.Is(err, repl.ErrCommandFailed) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
