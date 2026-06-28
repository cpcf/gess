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
	"strconv"
	"strings"
	"time"

	dsl "github.com/cpcf/gess/dsl"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

type config struct {
	Engine         string
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

func main() {
	cfg := config{}
	flag.StringVar(&cfg.Engine, "engine", "gess", "engine shape to generate and run: gess or jess")
	flag.IntVar(&cfg.Rules, "rules", 1000, "number of generated rules")
	flag.IntVar(&cfg.Facts, "facts", 10000, "number of generated input facts")
	flag.IntVar(&cfg.Queries, "queries", 100, "number of generated queries")
	flag.IntVar(&cfg.Buckets, "buckets", 100, "number of bucket values shared by rules and facts")
	flag.BoolVar(&cfg.Run, "run", true, "create a session and run activations after compiling")
	flag.IntVar(&cfg.QuerySamples, "query-samples", 3, "number of generated queries to execute after run")
	flag.StringVar(&cfg.WritePath, "write", "", "optional path to write the generated .gess or .clp source")
	flag.BoolVar(&cfg.WriteOnly, "write-only", false, "only write generated source; skip parse, compile, run, and query")
	flag.StringVar(&cfg.RunCPUProfile, "run-cpuprofile", "", "optional CPU profile path for Gess session.Run")
	flag.StringVar(&cfg.RunHeapProfile, "run-heapprofile", "", "optional heap profile path after Gess session.Run")
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
	if cfg.Engine == "" {
		cfg.Engine = "gess"
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "shape: engine=%s rules=%d facts=%d queries=%d buckets=%d run=%t\n", cfg.Engine, cfg.Rules, cfg.Facts, cfg.Queries, cfg.Buckets, cfg.Run)
	if cfg.Engine == "jess" {
		return runJess(ctx, out, cfg)
	}

	if cfg.WriteOnly {
		if cfg.WritePath == "" {
			return fmt.Errorf("-write-only requires -write")
		}
		elapsed, err := writeSourceFile(cfg.WritePath, cfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", cfg.WritePath, elapsed)
		return nil
	}

	start := time.Now()
	var source bytes.Buffer
	if err := writeGessSource(&source, cfg); err != nil {
		return err
	}
	sourceElapsed := time.Since(start)
	fmt.Fprintf(out, "source: bytes=%d duration=%s\n", source.Len(), sourceElapsed)

	if cfg.WritePath != "" {
		start = time.Now()
		if err := os.WriteFile(cfg.WritePath, source.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", cfg.WritePath, time.Since(start))
	}

	start = time.Now()
	doc, err := dsl.Parse("extreme-scale.gess", source.Bytes())
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
	writeMemory(out, "after-compile")

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
	writeMemory(out, "after-run")
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

func validateConfig(cfg config) error {
	switch cfg.Engine {
	case "gess", "jess":
	default:
		return fmt.Errorf("-engine must be gess or jess")
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

func writeSourceFile(path string, cfg config) (time.Duration, error) {
	start := time.Now()
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	if err := writeSourceForEngine(file, cfg); err != nil {
		_ = file.Close()
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func writeSourceForEngine(w io.Writer, cfg config) error {
	if cfg.Engine == "jess" {
		return writeJessSource(w, cfg)
	}
	return writeGessSource(w, cfg)
}

func writeGessSource(w io.Writer, cfg config) error {
	if _, err := fmt.Fprintln(w, `(deftemplate input
  (slot id (type INTEGER) (required TRUE))
  (slot bucket (type INTEGER) (required TRUE))
  (slot score (type INTEGER) (required TRUE))
  (slot kind (type STRING) (required TRUE))
)

(deftemplate bucket-policy
  (slot bucket (type INTEGER) (required TRUE))
  (slot enabled (type BOOLEAN) (required TRUE))
)

(deftemplate derived
  (declare (duplicate-policy unique-key) (duplicate-key id rule))
  (slot id (type INTEGER) (required TRUE))
  (slot rule (type STRING) (required TRUE))
  (slot bucket (type INTEGER) (required TRUE))
  (slot score (type INTEGER) (required TRUE))
)`); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "\n(deffacts seed-extreme"); err != nil {
		return err
	}
	for i := 0; i < cfg.Buckets; i++ {
		if _, err := fmt.Fprintf(w, "  (bucket-policy (bucket %d) (enabled TRUE))\n", i); err != nil {
			return err
		}
	}
	for i := 0; i < cfg.Facts; i++ {
		if _, err := fmt.Fprintf(w, "  (input (id %d) (bucket %d) (score %d) (kind %q))\n", i, i%cfg.Buckets, i%100, fmt.Sprintf("kind-%03d", i%100)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, ")"); err != nil {
		return err
	}

	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		threshold := i % 100
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (input (id ?id) (bucket %d) (score ?score))
  (bucket-policy (bucket %d) (enabled TRUE))
  (test (>= ?score %d))
  =>
  (assert (derived
    (id ?id)
    (rule %q)
    (bucket %d)
    (score ?score)
  )
  )
)
`, ruleName(i), bucket, bucket, threshold, ruleName(i), bucket); err != nil {
			return err
		}
	}

	for i := 0; i < cfg.Queries; i++ {
		if _, err := fmt.Fprintf(w, `
(defquery %s
  (declare (variables ?bucket))
  ?input <- (input (id ?id) (bucket ?bucket) (score ?score) (kind ?kind))
  (return
    (id ?id)
    (score ?score)
    (kind ?kind)
  )
)
`, queryName(i)); err != nil {
			return err
		}
	}
	return nil
}

func runJess(ctx context.Context, out io.Writer, cfg config) error {
	if cfg.WriteOnly {
		if cfg.WritePath == "" {
			return fmt.Errorf("-write-only requires -write")
		}
		elapsed, err := writeSourceFile(cfg.WritePath, cfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "write: path=%s duration=%s\n", cfg.WritePath, elapsed)
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
		file, err := os.CreateTemp("", "gess-extreme-scale-*.clp")
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

	tmpDir, err := os.MkdirTemp("", "gess-extreme-scale-jess-*")
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
	cmd := exec.CommandContext(ctx, cfg.Java, "-cp", runnerClasspath, "ExtremeScaleJessRunner", sourcePath, strconv.FormatBool(cfg.Run), strconv.Itoa(min(cfg.QuerySamples, cfg.Queries)), strconv.Itoa(cfg.Buckets))
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
	if _, err := fmt.Fprintln(w, `(deftemplate input
  (slot id (type INTEGER))
  (slot bucket (type INTEGER))
  (slot score (type INTEGER))
  (slot kind (type STRING))
)

(deftemplate bucket-policy
  (slot bucket (type INTEGER))
  (slot enabled (type SYMBOL))
)

(deftemplate derived
  (slot id (type INTEGER))
  (slot rule (type STRING))
  (slot bucket (type INTEGER))
  (slot score (type INTEGER))
)`); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "\n(deffacts seed-extreme"); err != nil {
		return err
	}
	for i := 0; i < cfg.Buckets; i++ {
		if _, err := fmt.Fprintf(w, "  (bucket-policy (bucket %d) (enabled TRUE))\n", i); err != nil {
			return err
		}
	}
	for i := 0; i < cfg.Facts; i++ {
		if _, err := fmt.Fprintf(w, "  (input (id %d) (bucket %d) (score %d) (kind %q))\n", i, i%cfg.Buckets, i%100, fmt.Sprintf("kind-%03d", i%100)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, ")"); err != nil {
		return err
	}

	for i := 0; i < cfg.Rules; i++ {
		bucket := i % cfg.Buckets
		threshold := i % 100
		if _, err := fmt.Fprintf(w, `
(defrule %s
  (input (id ?id) (bucket %d) (score ?score))
  (bucket-policy (bucket %d) (enabled TRUE))
  (test (>= ?score %d))
  =>
  (assert (derived
    (id ?id)
    (rule %q)
    (bucket %d)
    (score ?score)
  ))
)
`, ruleName(i), bucket, bucket, threshold, ruleName(i), bucket); err != nil {
			return err
		}
	}

	for i := 0; i < cfg.Queries; i++ {
		if _, err := fmt.Fprintf(w, `
(defquery %s
  (declare (variables ?bucket))
  (input (id ?id) (bucket ?bucket) (score ?score) (kind ?kind))
)
`, queryName(i)); err != nil {
			return err
		}
	}
	return nil
}

func writeJessRunnerJava(tmpDir string) (string, error) {
	path := filepath.Join(tmpDir, "ExtremeScaleJessRunner.java")
	source := `import jess.QueryResult;
import jess.Rete;
import jess.ValueVector;

public final class ExtremeScaleJessRunner {
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

    if (!run) {
      return;
    }

    engine.reset();
    start = System.nanoTime();
    int fired = engine.run();
    long runElapsed = System.nanoTime() - start;
    System.out.println("run: fired=" + fired + " duration=" + runElapsed + "ns");

    for (int i = 0; i < querySamples; i++) {
      int bucket = i % buckets;
      ValueVector arguments = new ValueVector();
      arguments.add(bucket);
      start = System.nanoTime();
      QueryResult result = engine.runQueryStar(queryName(i), arguments);
      int rows = 0;
      while (result.next()) {
        rows++;
        result.getString("id");
        result.getString("kind");
      }
      result.close();
      long queryElapsed = System.nanoTime() - start;
      System.out.println("query: name=" + queryName(i) + " bucket=" + bucket + " rows=" + rows + " duration=" + queryElapsed + "ns");
    }
  }

  private static String queryName(int i) {
    String raw = Integer.toString(i);
    StringBuilder out = new StringBuilder("inputs-by-bucket-");
    for (int pad = raw.length(); pad < 7; pad++) {
      out.append('0');
    }
    out.append(raw);
    return out.toString();
  }
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func ruleName(i int) string {
	return fmt.Sprintf("route-bucket-%07d", i)
}

func queryName(i int) string {
	return fmt.Sprintf("inputs-by-bucket-%07d", i)
}

func writeMemory(out io.Writer, label string) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	fmt.Fprintf(out, "memory: label=%s alloc=%dMB sys=%dMB numGC=%d\n", label, stats.Alloc/1024/1024, stats.Sys/1024/1024, stats.NumGC)
}
