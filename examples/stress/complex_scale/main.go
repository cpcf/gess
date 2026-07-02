package main

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"time"

	dsl "github.com/cpcf/gess/dsl"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

var shapes = []string{
	"join-cascade",
	"negation-exists",
	"higher-order",
	"aggregate",
	"query",
}

type config struct {
	Engine         string
	Shape          string
	Rules          int
	Facts          int
	Queries        int
	Buckets        int
	Run            bool
	QuerySamples   int
	WritePath      string
	WriteOnly      bool
	RunCPUProfile  string
	RunHeapProfile string
	JessJar        string
	Java           string
	Javac          string
}

type memoryMark struct {
	totalAlloc uint64
	mallocs    uint64
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.Engine, "engine", "gess", "engine shape to generate and run: gess or jess")
	flag.StringVar(&cfg.Shape, "shape", "join-cascade", "workload shape: join-cascade, negation-exists, higher-order, aggregate, query, or all")
	flag.IntVar(&cfg.Rules, "rules", 1000, "number of generated rules")
	flag.IntVar(&cfg.Facts, "facts", 10000, "number of generated vulnerability facts")
	flag.IntVar(&cfg.Queries, "queries", 100, "number of generated queries")
	flag.IntVar(&cfg.Buckets, "buckets", 100, "number of bucket values shared by rules and facts")
	flag.BoolVar(&cfg.Run, "run", true, "create a session and run activations after compiling")
	flag.IntVar(&cfg.QuerySamples, "query-samples", 3, "number of generated queries to execute after run")
	flag.StringVar(&cfg.WritePath, "write", "", "optional path to write the generated .gess source")
	flag.BoolVar(&cfg.WriteOnly, "write-only", false, "only write generated source; skip parse, compile, run, and query")
	flag.StringVar(&cfg.RunCPUProfile, "run-cpuprofile", "", "optional CPU profile path for session.Run")
	flag.StringVar(&cfg.RunHeapProfile, "run-heapprofile", "", "optional heap profile path after session.Run")
	flag.StringVar(&cfg.JessJar, "jess-jar", "../gess-design/jess.jar", "path to jess.jar when -engine jess")
	flag.StringVar(&cfg.Java, "java", "java", "Java executable when -engine jess")
	flag.StringVar(&cfg.Javac, "javac", "javac", "Javac executable when -engine jess")
	flag.Parse()

	if err := run(context.Background(), os.Stdout, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer, cfg config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if cfg.Shape == "all" {
		if cfg.WritePath != "" || cfg.WriteOnly {
			return fmt.Errorf("-shape all does not support -write or -write-only")
		}
		for i, shape := range shapes {
			if i > 0 {
				fmt.Fprintln(out)
			}
			next := cfg
			next.Shape = shape
			if err := runShape(ctx, out, next); err != nil {
				return err
			}
		}
		return nil
	}
	return runShape(ctx, out, cfg)
}

func runShape(ctx context.Context, out io.Writer, cfg config) error {
	fmt.Fprintf(out, "shape: engine=%s workload=%s rules=%d facts=%d queries=%d buckets=%d run=%t\n", cfg.Engine, cfg.Shape, cfg.Rules, cfg.Facts, cfg.Queries, cfg.Buckets, cfg.Run)
	if cfg.Engine == "jess" {
		return runJess(ctx, out, cfg)
	}
	memoryMark := readMemoryMark()

	if cfg.WriteOnly {
		if cfg.WritePath == "" {
			return fmt.Errorf("-write-only requires -write")
		}
		start := time.Now()
		file, err := os.Create(cfg.WritePath)
		if err != nil {
			return err
		}
		if err := writeGessSource(file, cfg); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", cfg.WritePath, time.Since(start))
		return nil
	}

	start := time.Now()
	var source bytes.Buffer
	if err := writeGessSource(&source, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "source: bytes=%d duration=%s\n", source.Len(), time.Since(start))

	if cfg.WritePath != "" {
		start = time.Now()
		if err := os.WriteFile(cfg.WritePath, source.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", cfg.WritePath, time.Since(start))
	}

	start = time.Now()
	doc, err := dsl.Parse("complex-scale.gess", source.Bytes())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "parse: duration=%s\n", time.Since(start))

	workspace := rules.NewWorkspace()
	start = time.Now()
	if err := dsl.Load(ctx, workspace, doc, dsl.Registry{}); err != nil {
		return err
	}
	fmt.Fprintf(out, "load: initialFacts=%d duration=%s\n", len(dsl.InitialFacts(doc)), time.Since(start))

	start = time.Now()
	ruleset, err := workspace.Compile(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "compile: duration=%s\n", time.Since(start))
	writeMemory(out, "after-compile", &memoryMark)

	if !cfg.Run {
		return nil
	}

	start = time.Now()
	session, err := sess.New(ruleset, sess.WithInitialFacts(dsl.InitialFacts(doc)...))
	if err != nil {
		return err
	}
	defer session.Close()
	fmt.Fprintf(out, "session: duration=%s\n", time.Since(start))

	stopCPU, err := startCPUProfile(cfg.RunCPUProfile)
	if err != nil {
		return err
	}
	start = time.Now()
	result, err := session.Run(ctx)
	stopErr := stopCPU()
	if err != nil {
		return err
	}
	if stopErr != nil {
		return stopErr
	}
	fmt.Fprintf(out, "run: fired=%d duration=%s\n", result.Fired, time.Since(start))
	writeMemory(out, "after-run", &memoryMark)
	if err := writeRuntimeDiagnostics(ctx, out, session); err != nil {
		return err
	}
	if err := writeHeapProfile(cfg.RunHeapProfile); err != nil {
		return err
	}

	samples := min(cfg.QuerySamples, cfg.Queries)
	for i := range samples {
		name := queryName(i)
		bucket := i % cfg.Buckets
		start = time.Now()
		rows, err := session.QueryAll(ctx, name, sess.QueryArgs{"bucket": int64(bucket)})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "query: name=%s bucket=%d rows=%d duration=%s\n", name, bucket, len(rows), time.Since(start))
	}
	return nil
}

func validateConfig(cfg config) error {
	switch cfg.Engine {
	case "gess", "jess":
	default:
		return fmt.Errorf("-engine must be gess or jess")
	}
	if cfg.Shape == "" {
		return fmt.Errorf("-shape is required")
	}
	if cfg.Shape != "all" && !contains(shapes, cfg.Shape) {
		return fmt.Errorf("-shape must be one of %s, or all", strings.Join(shapes, ", "))
	}
	if cfg.Rules < 0 {
		return fmt.Errorf("-rules must be >= 0")
	}
	if cfg.Facts < 0 {
		return fmt.Errorf("-facts must be >= 0")
	}
	if cfg.Queries < 0 {
		return fmt.Errorf("-queries must be >= 0")
	}
	if cfg.Buckets <= 0 {
		return fmt.Errorf("-buckets must be > 0")
	}
	if cfg.QuerySamples < 0 {
		return fmt.Errorf("-query-samples must be >= 0")
	}
	return nil
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func writeGessSource(w io.Writer, cfg config) error {
	if err := writeTemplates(w); err != nil {
		return err
	}
	if err := writeFacts(w, cfg); err != nil {
		return err
	}
	switch cfg.Shape {
	case "join-cascade":
		if err := writeJoinCascadeRules(w, cfg); err != nil {
			return err
		}
	case "negation-exists":
		if err := writeNegationExistsRules(w, cfg); err != nil {
			return err
		}
	case "higher-order":
		if err := writeHigherOrderRules(w, cfg); err != nil {
			return err
		}
	case "aggregate":
		if err := writeAggregateRules(w, cfg); err != nil {
			return err
		}
	case "query":
		if err := writeQueryRules(w, cfg); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported shape %q", cfg.Shape)
	}
	return writeQueries(w, cfg)
}

func runJess(ctx context.Context, out io.Writer, cfg config) error {
	if cfg.WriteOnly {
		if cfg.WritePath == "" {
			return fmt.Errorf("-write-only requires -write")
		}
		start := time.Now()
		file, err := os.Create(cfg.WritePath)
		if err != nil {
			return err
		}
		if err := writeJessSource(file, cfg); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", cfg.WritePath, time.Since(start))
		return nil
	}

	start := time.Now()
	var source bytes.Buffer
	if err := writeJessSource(&source, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "source: bytes=%d duration=%s\n", source.Len(), time.Since(start))

	sourcePath := cfg.WritePath
	cleanupSource := false
	if sourcePath == "" {
		file, err := os.CreateTemp("", "gess-complex-scale-*.clp")
		if err != nil {
			return err
		}
		sourcePath = file.Name()
		cleanupSource = true
		if _, err := file.Write(source.Bytes()); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	} else {
		start = time.Now()
		if err := os.WriteFile(sourcePath, source.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", sourcePath, time.Since(start))
	}
	if cleanupSource {
		defer os.Remove(sourcePath)
	}

	tmpDir, err := os.MkdirTemp("", "gess-complex-scale-jess-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	shimDir, err := createJessShim(tmpDir, cfg.JessJar)
	if err != nil {
		return err
	}
	classesDir := filepath.Join(tmpDir, "classes")
	if err := os.MkdirAll(classesDir, 0o755); err != nil {
		return err
	}
	runnerPath, err := writeJessRunnerJava(tmpDir)
	if err != nil {
		return err
	}
	classpath := strings.Join([]string{shimDir, cfg.JessJar}, string(os.PathListSeparator))
	compile := exec.CommandContext(ctx, cfg.Javac, "-cp", classpath, "-d", classesDir, runnerPath)
	if output, err := compile.CombinedOutput(); err != nil {
		return fmt.Errorf("compile Jess runner: %w\n%s", err, bytes.TrimSpace(output))
	}

	start = time.Now()
	runnerClasspath := strings.Join([]string{shimDir, classesDir, cfg.JessJar}, string(os.PathListSeparator))
	cmd := exec.CommandContext(ctx, cfg.Java, "-cp", runnerClasspath, "ComplexScaleJessRunner", sourcePath, strconv.FormatBool(cfg.Run), strconv.Itoa(min(cfg.QuerySamples, cfg.Queries)), strconv.Itoa(cfg.Buckets))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run Jess with %s: %w\n%s", cfg.JessJar, err, bytes.TrimSpace(output))
	}
	fmt.Fprintf(out, "jess: duration=%s\n", time.Since(start))
	if len(output) > 0 {
		fmt.Fprint(out, string(output))
	}
	return nil
}

func createJessShim(tmpDir string, jarPath string) (string, error) {
	shimDir := filepath.Join(tmpDir, "shim")
	outPath := filepath.Join(shimDir, "jess", "RU.class")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	reader, err := zip.OpenReader(jarPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	var ru []byte
	for _, file := range reader.File {
		if file.Name != "jess/RU.class" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		ru, err = io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		break
	}
	if ru == nil {
		return "", fmt.Errorf("%s does not contain jess/RU.class", jarPath)
	}
	pattern := []byte{0xB8, 0x00, 0x20, 0xAD}
	replacement := []byte{0x09, 0xAD, 0x00, 0x00}
	if count := bytes.Count(ru, pattern); count != 1 {
		return "", fmt.Errorf("expected one RU.a clock bytecode match, found %d", count)
	}
	ru = bytes.Replace(ru, pattern, replacement, 1)
	if err := os.WriteFile(outPath, ru, 0o644); err != nil {
		return "", err
	}
	return shimDir, nil
}

func writeJessSource(w io.Writer, cfg config) error {
	if err := writeJessTemplates(w); err != nil {
		return err
	}
	if err := writeFacts(w, cfg); err != nil {
		return err
	}
	switch cfg.Shape {
	case "join-cascade":
		if err := writeJoinCascadeRules(w, cfg); err != nil {
			return err
		}
	case "negation-exists":
		if err := writeNegationExistsRules(w, cfg); err != nil {
			return err
		}
	case "higher-order":
		if err := writeHigherOrderRules(w, cfg); err != nil {
			return err
		}
	case "aggregate":
		if err := writeJessAggregateRules(w, cfg); err != nil {
			return err
		}
	case "query":
		if err := writeQueryRules(w, cfg); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported shape %q", cfg.Shape)
	}
	return writeJessQueries(w, cfg)
}

func writeJessTemplates(w io.Writer) error {
	_, err := fmt.Fprintln(w, `(deftemplate asset
  (slot id (type INTEGER))
  (slot bucket (type INTEGER))
  (slot owner (type INTEGER))
  (slot criticality (type INTEGER))
)

(deftemplate vulnerability
  (slot id (type INTEGER))
  (slot asset (type INTEGER))
  (slot bucket (type INTEGER))
  (slot score (type INTEGER))
  (slot severity (type STRING))
  (slot status (type STRING))
)

(deftemplate evidence
  (slot vuln (type INTEGER))
  (slot source (type STRING))
  (slot weight (type INTEGER))
)

(deftemplate exception
  (slot vuln (type INTEGER))
  (slot asset (type INTEGER))
  (slot active (type SYMBOL))
)

(deftemplate policy
  (slot bucket (type INTEGER))
  (slot enabled (type SYMBOL))
  (slot level (type STRING))
)

(deftemplate signal
  (slot id (type INTEGER))
  (slot rule (type STRING))
  (slot bucket (type INTEGER))
  (slot asset (type INTEGER))
  (slot score (type INTEGER))
)

(deftemplate response
  (slot target (type INTEGER))
  (slot rule (type STRING))
  (slot lane (type STRING))
  (slot bucket (type INTEGER))
  (slot priority (type INTEGER))
)

(deftemplate bucket-summary
  (slot bucket (type INTEGER))
  (slot rule (type STRING))
  (slot count (type INTEGER))
  (slot total (type INTEGER))
  (slot max-score (type INTEGER))
)`)
	return err
}

func writeJessAggregateRules(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		name := ruleName("aggregate", i)
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (policy (bucket %d) (enabled TRUE))
  ?count <- (accumulate
    (bind ?n 0)
    (bind ?n (+ ?n 1))
    ?n
    (vulnerability
      (bucket %d)
    )
  )
  ?total <- (accumulate
    (bind ?sum 0)
    (bind ?sum (+ ?sum ?score))
    ?sum
    (vulnerability
      (bucket %d)
      (score ?score)
    )
  )
  ?max-score <- (accumulate
    (bind ?mx 0)
    (bind ?mx (max ?mx ?score))
    ?mx
    (vulnerability
      (bucket %d)
      (score ?score)
    )
  )
  =>
  (assert (bucket-summary
    (bucket %d)
    (rule %q)
    (count ?count)
    (total ?total)
    (max-score ?max-score))
  )
)
`, name, bucket, bucket, bucket, bucket, bucket, name); err != nil {
			return err
		}
	}
	return nil
}

func writeJessQueries(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Queries; i++ {
		if cfg.Shape == "aggregate" {
			if _, err := fmt.Fprintf(w, `
(defquery %s
  (declare (variables ?bucket))
  (bucket-summary
    (bucket ?bucket)
    (rule ?rule)
    (count ?count)
    (total ?total)
    (max-score ?max-score)
  )
)
`, queryName(i)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, `
(defquery %s
  (declare (variables ?bucket))
  (vulnerability
    (id ?vuln)
    (asset ?asset)
    (bucket ?bucket)
    (score ?score)
    (severity ?severity)
  )
  (asset
    (id ?asset)
    (bucket ?bucket)
    (criticality ?criticality)
  )
  (not
    (exception
      (vuln ?vuln)
      (active TRUE)
    )
  )
)
`, queryName(i)); err != nil {
			return err
		}
	}
	return nil
}

func writeJessRunnerJava(tmpDir string) (string, error) {
	path := filepath.Join(tmpDir, "ComplexScaleJessRunner.java")
	source := `import jess.QueryResult;
import jess.Rete;
import jess.ValueVector;

public final class ComplexScaleJessRunner {
  public static void main(String[] args) throws Exception {
    String script = args[0];
    boolean run = Boolean.parseBoolean(args[1]);
    int querySamples = Integer.parseInt(args[2]);
    int buckets = Integer.parseInt(args[3]);

    Rete engine = new Rete();
    long start = System.nanoTime();
    engine.batch(script);
    long loadElapsed = System.nanoTime() - start;
    System.out.println("load: duration=" + loadElapsed + "ns");
    writeMemory("after-load");

    if (!run) {
      return;
    }

    engine.reset();
    start = System.nanoTime();
    int fired = engine.run();
    long runElapsed = System.nanoTime() - start;
    System.out.println("run: fired=" + fired + " duration=" + runElapsed + "ns");
    writeMemory("after-run");

    for (int i = 0; i < querySamples; i++) {
      int bucket = i % buckets;
      ValueVector arguments = new ValueVector();
      arguments.add(bucket);
      start = System.nanoTime();
      QueryResult result = engine.runQueryStar(queryName(i), arguments);
      int rows = 0;
      while (result.next()) {
        rows++;
      }
      result.close();
      long queryElapsed = System.nanoTime() - start;
      System.out.println("query: name=" + queryName(i) + " bucket=" + bucket + " rows=" + rows + " duration=" + queryElapsed + "ns");
    }
    writeMemory("after-query");
  }

  private static String queryName(int i) {
    String raw = Integer.toString(i);
    StringBuilder out = new StringBuilder("complex-query-");
    for (int pad = raw.length(); pad < 7; pad++) {
      out.append('0');
    }
    out.append(raw);
    return out.toString();
  }

  private static void writeMemory(String label) {
    Runtime runtime = Runtime.getRuntime();
    runtime.gc();
    long used = runtime.totalMemory() - runtime.freeMemory();
    long committed = runtime.totalMemory();
    long max = runtime.maxMemory();
    System.out.println(
      "memory: label=" + label +
      " used=" + used / 1024 / 1024 + "MB" +
      " committed=" + committed / 1024 / 1024 + "MB" +
      " max=" + max / 1024 / 1024 + "MB" +
      " gc=true"
    );
  }
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func writeTemplates(w io.Writer) error {
	_, err := fmt.Fprintln(w, `(deftemplate asset
  (slot id (type INTEGER) (required TRUE))
  (slot bucket (type INTEGER) (required TRUE))
  (slot owner (type INTEGER) (required TRUE))
  (slot criticality (type INTEGER) (required TRUE))
)

(deftemplate vulnerability
  (slot id (type INTEGER) (required TRUE))
  (slot asset (type INTEGER) (required TRUE))
  (slot bucket (type INTEGER) (required TRUE))
  (slot score (type INTEGER) (required TRUE))
  (slot severity (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE))
)

(deftemplate evidence
  (slot vuln (type INTEGER) (required TRUE))
  (slot source (type STRING) (required TRUE))
  (slot weight (type INTEGER) (required TRUE))
)

(deftemplate exception
  (slot vuln (type INTEGER) (required TRUE))
  (slot asset (type INTEGER) (required TRUE))
  (slot active (type BOOLEAN) (required TRUE))
)

(deftemplate policy
  (slot bucket (type INTEGER) (required TRUE))
  (slot enabled (type BOOLEAN) (required TRUE))
  (slot level (type STRING) (required TRUE))
)

(deftemplate signal
  (declare (duplicate-policy unique-key) (duplicate-key id rule))
  (slot id (type INTEGER) (required TRUE))
  (slot rule (type STRING) (required TRUE))
  (slot bucket (type INTEGER) (required TRUE))
  (slot asset (type INTEGER) (required TRUE))
  (slot score (type INTEGER) (required TRUE))
)

(deftemplate response
  (declare (duplicate-policy unique-key) (duplicate-key target rule lane))
  (slot target (type INTEGER) (required TRUE))
  (slot rule (type STRING) (required TRUE))
  (slot lane (type STRING) (required TRUE))
  (slot bucket (type INTEGER) (required TRUE))
  (slot priority (type INTEGER) (required TRUE))
)

(deftemplate bucket-summary
  (declare (duplicate-policy unique-key) (duplicate-key bucket rule))
  (slot bucket (type INTEGER) (required TRUE))
  (slot rule (type STRING) (required TRUE))
  (slot count (type INTEGER) (required TRUE))
  (slot total (type INTEGER) (required TRUE))
  (slot max-score (type INTEGER) (required TRUE))
)`)
	return err
}

func writeFacts(w io.Writer, cfg config) error {
	assetCount := supportingAssetCount(cfg)
	if _, err := fmt.Fprintln(w, "\n(deffacts seed-complex-scale"); err != nil {
		return err
	}
	for bucket := range cfg.Buckets {
		level := "standard"
		if bucket%10 == 0 {
			level = "emergency"
		}
		if _, err := fmt.Fprintf(w, "  (policy (bucket %d) (enabled TRUE) (level %q))\n", bucket, level); err != nil {
			return err
		}
	}
	for asset := range assetCount {
		if _, err := fmt.Fprintf(w, "  (asset (id %d) (bucket %d) (owner %d) (criticality %d))\n", asset, asset%cfg.Buckets, asset%97, 1+asset%5); err != nil {
			return err
		}
	}
	for vuln := 0; vuln < cfg.Facts; vuln++ {
		asset := vuln % assetCount
		score := vuln % 100
		severity := severityForScore(score)
		status := "open"
		if vuln%29 == 0 {
			status = "accepted-risk"
		}
		if _, err := fmt.Fprintf(w, "  (vulnerability (id %d) (asset %d) (bucket %d) (score %d) (severity %q) (status %q))\n", vuln, asset, asset%cfg.Buckets, score, severity, status); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  (evidence (vuln %d) (source %q) (weight %d))\n", vuln, evidenceSource(vuln), 1+vuln%10); err != nil {
			return err
		}
		if vuln%17 == 0 {
			if _, err := fmt.Fprintf(w, "  (exception (vuln %d) (asset %d) (active TRUE))\n", vuln, asset); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w, ")")
	return err
}

func writeJoinCascadeRules(w io.Writer, cfg config) error {
	detectRules := max(1, (cfg.Rules+1)/2)
	for i := range detectRules {
		bucket := i % cfg.Buckets
		name := ruleName("detect", i)
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (vulnerability
    (id ?vuln)
    (asset ?asset)
    (bucket %d)
    (score ?score)
  )
  (asset
    (id ?asset)
    (bucket %d)
    (criticality ?criticality)
  )
  (evidence
    (vuln ?vuln)
    (source %q)
    (weight ?weight)
  )
  (policy (bucket %d) (enabled TRUE))
  (test (>= ?score %d))
  =>
  (assert (signal
    (id ?vuln)
    (rule %q)
    (bucket %d)
    (asset ?asset)
    (score ?score))
  )
)
`, name, bucket, bucket, evidenceSource(i), bucket, i%100, name, bucket); err != nil {
			return err
		}
	}
	for i := detectRules; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		source := ruleName("detect", i%detectRules)
		name := ruleName("respond", i)
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (signal
    (id ?vuln)
    (rule %q)
    (bucket %d)
    (asset ?asset)
    (score ?score)
  )
  (asset
    (id ?asset)
    (bucket %d)
    (criticality ?criticality)
  )
  (policy (bucket %d) (enabled TRUE))
  =>
  (assert (response
    (target ?vuln)
    (rule %q)
    (lane "join-cascade")
    (bucket %d)
    (priority ?criticality))
  )
)
`, name, source, bucket, bucket, bucket, name, bucket); err != nil {
			return err
		}
	}
	return nil
}

func writeNegationExistsRules(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		name := ruleName("negation-exists", i)
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (asset
    (id ?asset)
    (bucket %d)
    (criticality ?criticality)
  )
  (exists
    (vulnerability
      (asset ?asset)
      (bucket %d)
      (severity "critical")
    )
  )
  (not
    (exception
      (asset ?asset)
      (active TRUE)
    )
  )
  =>
  (assert (response
    (target ?asset)
    (rule %q)
    (lane "negation-exists")
    (bucket %d)
    (priority ?criticality))
  )
)
`, name, bucket, bucket, name, bucket); err != nil {
			return err
		}
	}
	return nil
}

func writeHigherOrderRules(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		name := ruleName("higher-order", i)
		switch i % 3 {
		case 1:
			if _, err := fmt.Fprintf(w, `
(defrule %s
  (policy (bucket %d) (enabled TRUE))
  (forall
    (vulnerability
      (bucket %d)
      (score ?score)
    )
    (test (< ?score 100))
  )
  =>
  (assert (response
    (target %d)
    (rule %q)
    (lane "higher-order-forall")
    (bucket %d)
    (priority 1))
  )
)
`, name, bucket, bucket, bucket, name, bucket); err != nil {
				return err
			}
			continue
		case 2:
			if _, err := fmt.Fprintf(w, `
(defrule %s
  (policy (bucket %d) (enabled TRUE))
  (exists
    (vulnerability
      (bucket %d)
      (severity "critical")
    )
  )
  =>
  (assert (response
    (target %d)
    (rule %q)
    (lane "higher-order-exists")
    (bucket %d)
    (priority 1))
  )
)
`, name, bucket, bucket, bucket, name, bucket); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (asset
    (id ?asset)
    (bucket %d)
    (criticality ?criticality)
  )
  (and
    (or
      (and
        ?policy <- (policy (bucket %d) (level "emergency"))
      )
      (and
        ?policy <- (policy (bucket %d) (level "standard"))
      )
    )
  )
  (not
    (exception
      (asset ?asset)
      (active TRUE)
    )
  )
  =>
  (assert (response
    (target ?asset)
    (rule %q)
    (lane "higher-order")
    (bucket %d)
    (priority ?criticality))
  )
)
`, name, bucket, bucket, bucket, name, bucket); err != nil {
			return err
		}
	}
	return nil
}

func writeAggregateRules(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		name := ruleName("aggregate", i)
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (policy (bucket %d) (enabled TRUE))
  (accumulate
    (vulnerability
      (bucket %d)
      (score ?score)
    )
    (bind ?count (count))
    (bind ?total (sum ?score))
    (bind ?max-score (max ?score))
  )
  =>
  (assert (bucket-summary
    (bucket %d)
    (rule %q)
    (count ?count)
    (total ?total)
    (max-score ?max-score))
  )
)
`, name, bucket, bucket, bucket, name); err != nil {
			return err
		}
	}
	return nil
}

func writeQueryRules(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		name := ruleName("query-seed", i)
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (vulnerability
    (id ?vuln)
    (asset ?asset)
    (bucket %d)
    (score ?score)
  )
  (asset
    (id ?asset)
    (bucket %d)
    (criticality ?criticality)
  )
  (not
    (exception
      (vuln ?vuln)
      (active TRUE)
    )
  )
  =>
  (assert (response
    (target ?vuln)
    (rule %q)
    (lane "query")
    (bucket %d)
    (priority ?criticality))
  )
)
`, name, bucket, bucket, name, bucket); err != nil {
			return err
		}
	}
	return nil
}

func writeQueries(w io.Writer, cfg config) error {
	for i := 0; i < cfg.Queries; i++ {
		if cfg.Shape == "aggregate" {
			if _, err := fmt.Fprintf(w, `
(defquery %s
  (declare (variables ?bucket))
  (bucket-summary
    (bucket ?bucket)
    (rule ?rule)
    (count ?count)
    (total ?total)
    (max-score ?max-score)
  )
  (return
    (rule ?rule)
    (count ?count)
    (total ?total)
    (max ?max-score)
  )
)
`, queryName(i)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, `
(defquery %s
  (declare (variables ?bucket))
  (vulnerability
    (id ?vuln)
    (asset ?asset)
    (bucket ?bucket)
    (score ?score)
    (severity ?severity)
  )
  (asset
    (id ?asset)
    (bucket ?bucket)
    (criticality ?criticality)
  )
  (not
    (exception
      (vuln ?vuln)
      (active TRUE)
    )
  )
  (return
    (vuln ?vuln)
    (asset ?asset)
    (score ?score)
    (severity ?severity)
    (criticality ?criticality)
  )
)
`, queryName(i)); err != nil {
			return err
		}
	}
	return nil
}

func startCPUProfile(path string) (func() error, error) {
	if path == "" {
		return func() error { return nil }, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() error {
		pprof.StopCPUProfile()
		return file.Close()
	}, nil
}

func writeHeapProfile(path string) error {
	if path == "" {
		return nil
	}
	runtime.GC()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pprof.WriteHeapProfile(file); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readMemoryMark() memoryMark {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return memoryMark{totalAlloc: stats.TotalAlloc, mallocs: stats.Mallocs}
}

func writeMemory(w io.Writer, label string, previous *memoryMark) {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	mark := memoryMark{totalAlloc: stats.TotalAlloc, mallocs: stats.Mallocs}
	var totalAllocDelta uint64
	var mallocsDelta uint64
	if previous != nil {
		totalAllocDelta = mark.totalAlloc - previous.totalAlloc
		mallocsDelta = mark.mallocs - previous.mallocs
		*previous = mark
	}
	fmt.Fprintf(
		w,
		"memory: label=%s alloc=%dMB sys=%dMB totalAllocDelta=%dMB mallocsDelta=%d numGC=%d gc=true\n",
		label,
		stats.Alloc/1024/1024,
		stats.Sys/1024/1024,
		totalAllocDelta/1024/1024,
		mallocsDelta,
		stats.NumGC,
	)
}

func writeRuntimeDiagnostics(ctx context.Context, w io.Writer, session *sess.Session) error {
	diagnostics, err := session.RuntimeDiagnostics(ctx)
	if err != nil {
		return err
	}
	for _, owner := range diagnostics.MemoryOwners {
		fmt.Fprintf(
			w,
			"rete-memory: owner=%s rows=%d buckets=%d indexes=%d tombstones=%d bytes=%d highWater=%d\n",
			owner.Owner,
			owner.Rows,
			owner.Buckets,
			owner.Indexes,
			owner.Tombstones,
			owner.Bytes,
			owner.HighWater,
		)
	}
	return nil
}

func supportingAssetCount(cfg config) int {
	if cfg.Facts <= 0 {
		return cfg.Buckets
	}
	count := min(max(cfg.Facts/8, cfg.Buckets), cfg.Facts)
	return count
}

func severityForScore(score int) string {
	switch {
	case score >= 90:
		return "critical"
	case score >= 70:
		return "high"
	case score >= 40:
		return "medium"
	default:
		return "low"
	}
}

func evidenceSource(i int) string {
	return fmt.Sprintf("scanner-%02d", i%8)
}

func ruleName(prefix string, i int) string {
	return fmt.Sprintf("%s-%07d", prefix, i)
}

func queryName(i int) string {
	return fmt.Sprintf("complex-query-%07d", i)
}
