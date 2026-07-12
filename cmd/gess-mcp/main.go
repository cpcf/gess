package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/cpcf/gess/cmd/gess-mcp/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	flags := flag.NewFlagSet("gess-mcp", flag.ExitOnError)
	rulesetRoot := flags.String("ruleset-root", "", "required root containing vetted .gess rulesets")
	explainLogMaxEntries := flags.Int("explain-log-max-entries", 4096, "maximum retained explain-log entries")
	maxFirings := flags.Int("max-firings", 10_000, "maximum activations fired by one run tool call")
	maxQueryRows := flags.Int("max-query-rows", 1_000, "maximum query rows returned by one tool call")
	maxDemandCascadeSteps := flags.Int("max-demand-cascade-steps", 10_000, "maximum backchain demand steps per cascade")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gess-mcp --ruleset-root <dir> [--explain-log-max-entries <n>] [--max-firings <n>] [--max-query-rows <n>] [--max-demand-cascade-steps <n>]")
		os.Exit(2)
	}
	service, err := server.New(server.Config{
		RulesetRoot:           *rulesetRoot,
		ExplainLogMaxEntries:  *explainLogMaxEntries,
		MaxFirings:            *maxFirings,
		MaxQueryRows:          *maxQueryRows,
		MaxDemandCascadeSteps: *maxDemandCascadeSteps,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer service.Close()
	if err := service.MCP().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
