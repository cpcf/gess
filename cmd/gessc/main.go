package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	dsl "github.com/cpcf/gess/dsl"
)

func main() {
	var outPath string
	var packageName string
	var functionName string
	flag.StringVar(&outPath, "o", "", "output Go file; stdout when empty")
	flag.StringVar(&packageName, "package", "main", "generated Go package name")
	flag.StringVar(&functionName, "func", "BuildRuleset", "generated build function name")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: gessc [-o file] [-package name] [-func name] rules.gess [...]")
		os.Exit(2)
	}
	sources := make([]dsl.SourceFile, 0, flag.NArg())
	for _, path := range flag.Args() {
		source, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			os.Exit(1)
		}
		sources = append(sources, dsl.SourceFile{Name: path, Source: source})
	}
	generated, err := dsl.GenerateGo(context.Background(), sources, dsl.GoGeneratorOptions{
		PackageName:  packageName,
		FunctionName: functionName,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}
	if outPath == "" {
		_, _ = os.Stdout.Write(generated)
		return
	}
	if err := os.WriteFile(outPath, generated, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
}
