package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const exercisePackage = "./tutorial/vulnerability_response"

type checkpoint struct {
	Number   int
	Title    string
	Expected string
	Hint     string
}

var checkpoints = []checkpoint{
	{
		Number:   1,
		Title:    "Define templates",
		Expected: "setup: templates",
		Hint:     "Add deftemplate forms for vulnerability, asset, accepted-risk, remediation-action, and critical-vulnerability-summary.",
	},
	{
		Number:   2,
		Title:    "Add seed facts",
		Expected: "setup: facts",
		Hint:     "Add deffacts seed-vulnerabilities with assets, an accepted risk, and vulnerability records.",
	},
	{
		Number:   3,
		Title:    "Add queries",
		Expected: "setup: queries",
		Hint:     "Add actions-by-lane and critical-summaries queries. Queries are named read models used by Go code.",
	},
	{
		Number:   4,
		Title:    "Add an emergency rule",
		Expected: "emergency: VULN-100 critical-exploitable-internet",
		Hint:     "Match a critical exploitable vulnerability on a critical internet-facing asset, then assert a remediation action in the emergency lane.",
	},
	{
		Number:   5,
		Title:    "Route accepted risk",
		Expected: "accepted-risk: VULN-200 compensating-control",
		Hint:     "Join a vulnerability to an accepted-risk fact and copy the exception reason into a remediation action.",
	},
	{
		Number:   6,
		Title:    "Use and",
		Expected: "and: VULN-400 critical-nonexploited",
		Hint:     "Use (and ...) to group the vulnerability, asset, and score test conditions.",
	},
	{
		Number:   7,
		Title:    "Use or",
		Expected: "or: VULN-500 dependency-or-exposure-watch",
		Hint:     "Bind variables before the or, then use the or branches for alternative checks.",
	},
	{
		Number:   8,
		Title:    "Use exists",
		Expected: "exists: APP-100 asset-has-critical",
		Hint:     "Use exists to check whether any critical vulnerability is present on an asset, then assert an asset-level action.",
	},
	{
		Number:   9,
		Title:    "Use forall",
		Expected: "forall: APP-300 asset-under-limit",
		Hint:     "Use forall to require every vulnerability on an asset to have a score below the limit, then assert an asset-level action.",
	},
	{
		Number:   10,
		Title:    "Use negation",
		Expected: "standard: VULN-300 normal-remediation",
		Hint:     "Match a low-criticality asset and use not to exclude vulnerabilities with accepted risk.",
	},
	{
		Number:   11,
		Title:    "Add an aggregate",
		Expected: "summary: critical count=2 total=195",
		Hint:     "Use accumulate over critical vulnerabilities with count and sum over score.",
	},
	{
		Number:   12,
		Title:    "Call host code",
		Expected: "recorded: VULN-100/critical-exploitable-internet",
		Hint:     "Match emergency remediation actions and call record-emergency with the target and reason.",
	},
}

type progress struct {
	Output   string
	Complete []checkpoint
	Next     *checkpoint
}

type app struct {
	root string
	in   io.Reader
	out  io.Writer
	err  io.Writer
}

func main() {
	root, err := findModuleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	a := app{root: root, in: os.Stdin, out: os.Stdout, err: os.Stderr}
	if err := a.run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.serve(ctx, "127.0.0.1:8090")
	}
	switch args[0] {
	case "serve":
		addr := "127.0.0.1:8090"
		if len(args) > 1 {
			addr = args[1]
		}
		return a.serve(ctx, addr)
	case "prompt":
		return a.prompt(ctx)
	case "status":
		return a.status(ctx, true)
	case "run":
		return a.runExercise(ctx)
	case "hint":
		return a.hint(ctx)
	case "test":
		return a.test(ctx)
	case "reset":
		return a.copyRules(ctx, "tutorial/vulnerability_response/starter/rules.gess", "reset")
	case "solution":
		return a.copyRules(ctx, "tutorial/vulnerability_response/solution/rules.gess", "solution")
	case "help", "-h", "--help":
		a.help()
		return nil
	default:
		return fmt.Errorf("unknown command %q; run help for commands", args[0])
	}
}

func (a app) prompt(ctx context.Context) error {
	fmt.Fprintln(a.out, "Gess interactive tutorial")
	fmt.Fprintln(a.out, "Edit tutorial/vulnerability_response/rules.gess, then use commands here to check progress.")
	a.help()
	if err := a.status(ctx, false); err != nil {
		fmt.Fprintf(a.err, "%v\n", err)
	}
	scanner := bufio.NewScanner(a.in)
	for {
		fmt.Fprint(a.out, "gess-tutorial> ")
		if !scanner.Scan() {
			fmt.Fprintln(a.out)
			return scanner.Err()
		}
		command := strings.Fields(scanner.Text())
		if len(command) == 0 {
			continue
		}
		switch command[0] {
		case "quit", "exit":
			return nil
		default:
			if err := a.run(ctx, command); err != nil {
				fmt.Fprintf(a.err, "%v\n", err)
			}
		}
	}
}

func (a app) help() {
	fmt.Fprintln(a.out, "")
	fmt.Fprintln(a.out, "Commands:")
	fmt.Fprintln(a.out, "  serve     start the browser tutorial on 127.0.0.1:8090")
	fmt.Fprintln(a.out, "  prompt    start the terminal tutorial prompt")
	fmt.Fprintln(a.out, "  status    validate the editor source and show completed checkpoints")
	fmt.Fprintln(a.out, "  run       validate the editor source and print raw exercise output")
	fmt.Fprintln(a.out, "  hint      show the next checkpoint hint")
	fmt.Fprintln(a.out, "  test      run the opt-in completion test")
	fmt.Fprintln(a.out, "  reset     restore the starter rules.gess")
	fmt.Fprintln(a.out, "  solution  copy the completed solution into rules.gess")
	fmt.Fprintln(a.out, "  help      show commands")
	fmt.Fprintln(a.out, "  quit      leave the prompt")
	fmt.Fprintln(a.out, "")
}

func (a app) status(ctx context.Context, showOutput bool) error {
	p, err := a.progress(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(p.Output) == "" {
		fmt.Fprintln(a.out, "Exercise output: <empty>")
	} else if showOutput {
		fmt.Fprintln(a.out, "Exercise output:")
		fmt.Fprint(a.out, p.Output)
	}
	fmt.Fprintf(a.out, "Completed checkpoints: %d/%d\n", len(p.Complete), len(checkpoints))
	for _, checkpoint := range p.Complete {
		fmt.Fprintf(a.out, "  %d. %s\n", checkpoint.Number, checkpoint.Title)
	}
	if p.Next != nil {
		fmt.Fprintf(a.out, "Next checkpoint: %d. %s\n", p.Next.Number, p.Next.Title)
		fmt.Fprintln(a.out, "Run `hint` for guidance.")
		return nil
	}
	fmt.Fprintln(a.out, "All checkpoints complete. Run `test` for the final check.")
	return nil
}

func (a app) runExercise(ctx context.Context) error {
	output, err := a.generateAndRun(ctx)
	if err != nil {
		return err
	}
	if output == "" {
		fmt.Fprintln(a.out, "<empty output>")
		return nil
	}
	fmt.Fprint(a.out, output)
	return nil
}

func (a app) hint(ctx context.Context) error {
	p, err := a.progress(ctx)
	if err != nil {
		return err
	}
	if p.Next == nil {
		fmt.Fprintln(a.out, "All checkpoints are complete. Run `test`.")
		return nil
	}
	fmt.Fprintf(a.out, "Checkpoint %d: %s\n", p.Next.Number, p.Next.Title)
	fmt.Fprintln(a.out, p.Next.Hint)
	fmt.Fprintln(a.out, "Full instructions are in tutorial/README.md.")
	return nil
}

func (a app) test(ctx context.Context) error {
	if err := a.runCommand(ctx, map[string]string{"GESS_TUTORIAL": "1"}, "go", "test", exercisePackage); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "Completion test passed.")
	return nil
}

func (a app) copyRules(ctx context.Context, source string, label string) error {
	from := filepath.Join(a.root, source)
	to := filepath.Join(a.root, "tutorial/vulnerability_response/rules.gess")
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		return err
	}
	if err := a.runCommand(ctx, nil, "go", "generate", exercisePackage); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Applied %s rules and regenerated tutorial/vulnerability_response/rules_generated.go.\n", label)
	return nil
}

func (a app) progress(ctx context.Context) (progress, error) {
	output, err := a.generateAndRun(ctx)
	if err != nil {
		return progress{}, err
	}
	return evaluateProgress(output, checkpoints), nil
}

func (a app) generateAndRun(ctx context.Context) (string, error) {
	source, err := os.ReadFile(filepath.Join(a.root, "tutorial/vulnerability_response/rules.gess"))
	if err != nil {
		return "", err
	}
	return runTutorialSource(ctx, source)
}

func (a app) runCommand(ctx context.Context, env map[string]string, name string, args ...string) error {
	return a.runCommandWithOutput(ctx, env, a.out, name, args...)
}

func (a app) runCommandWithOutput(ctx context.Context, env map[string]string, stdout io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = a.root
	cmd.Stdout = stdout
	cmd.Stderr = a.err
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func evaluateProgress(output string, checkpoints []checkpoint) progress {
	p := progress{Output: output}
	for _, checkpoint := range checkpoints {
		if strings.Contains(output, checkpoint.Expected) {
			p.Complete = append(p.Complete, checkpoint)
			continue
		}
		if p.Next == nil {
			next := checkpoint
			p.Next = &next
		}
	}
	return p
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod; run from inside the gess module")
		}
		dir = parent
	}
}
