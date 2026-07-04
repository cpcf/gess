package dsl

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

type (
	// Registry supplies the host-provided actions, call targets, and pure
	// functions that a .gess file's (call ...) forms and expressions
	// reference. [Load] and [Compile] fail if a .gess file names an action,
	// call, or function that Registry doesn't provide.
	Registry = engine.DSLRegistry
	// CallFunc is the signature host code implements to back a .gess
	// (call name arg...) action. It receives the action context and the
	// evaluated argument values, and returns an error to fail the action.
	CallFunc = engine.DSLCallFunc
	// MissingRegistrations lists the host action and call names a parsed
	// Document references that a Registry does not provide, as returned by
	// Document.MissingRegistrations.
	MissingRegistrations = engine.DSLMissingRegistrations
	// Document is a parsed .gess source file. [Parse] produces a Document;
	// [Load] lowers it into a Workspace and populates the facts returned by
	// [InitialFacts].
	Document = engine.GessDocument
	// SourceSpan is a source location range within a .gess file, used to
	// locate parse and lowering errors.
	SourceSpan = engine.SourceSpan
	// FileError reports a .gess parser or lowering error with the source
	// span where it occurred. It wraps an underlying error where one
	// exists, so errors.Is and errors.As work through it.
	FileError = engine.GessFileError
	// SourceFile is one .gess file passed to [GenerateGo], identified by
	// name (for error spans) and its raw source bytes.
	SourceFile = engine.GessSourceFile
	// GoGeneratorOptions controls the package and function name of Go
	// source emitted by [GenerateGo]. Empty fields default to "main" and
	// "BuildRuleset".
	GoGeneratorOptions = engine.GessGoGeneratorOptions
)

// Parse parses .gess source bytes into a Document without loading it into
// a workspace. name is stored as the document's identity and used in
// error spans. The result's [InitialFacts] are empty until [Load] runs.
func Parse(name string, source []byte) (*Document, error) {
	return engine.ParseGess(name, source)
}

// Load lowers a parsed Document into workspace, registering its modules,
// templates, rules, queries, and deffacts seed facts, and resolving its
// (call ...) forms and pure functions against registry. On success it
// populates doc's [InitialFacts]. Loading fails if the document references
// an action, call, or function name registry doesn't provide.
func Load(ctx context.Context, workspace *rules.Workspace, doc *Document, registry Registry) error {
	return engine.LoadGess(ctx, workspace, doc, registry)
}

// Compile parses and loads .gess source into a new workspace and compiles
// it in one step, equivalent to [Parse] followed by [Load] and
// Workspace.Compile. Use Parse and Load directly to load multiple .gess
// files into one shared workspace before compiling, or to keep the
// intermediate Document for [InitialFacts].
func Compile(ctx context.Context, name string, source []byte, registry Registry) (*rules.Ruleset, error) {
	return engine.CompileGess(ctx, name, source, registry)
}

// GenerateGo emits Go source that builds the same workspace as the given
// .gess sources, without parsing those files at application startup. The
// generated build function takes a [Registry] and validates every (call
// ...) name against it. If formatting the generated source fails,
// GenerateGo returns the unformatted bytes alongside the error.
func GenerateGo(ctx context.Context, sources []SourceFile, opts GoGeneratorOptions) ([]byte, error) {
	return engine.GenerateGessGo(ctx, sources, opts)
}

// RenderRuleset renders every module, template, function, rule, and query
// in a compiled ruleset back to canonical .gess source.
func RenderRuleset(revision *rules.Ruleset) ([]byte, error) {
	return engine.RenderGessRuleset(revision)
}

// RenderModule renders one module of a compiled ruleset, and everything
// defined in it, back to canonical .gess source.
func RenderModule(revision *rules.Ruleset, name rules.ModuleName) ([]byte, error) {
	return engine.RenderGessModule(revision, name)
}

// RenderTemplate renders one template definition back to canonical .gess
// source.
func RenderTemplate(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessTemplate(revision, name)
}

// RenderRule renders one rule definition back to canonical .gess source.
func RenderRule(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessRule(revision, name)
}

// RenderQuery renders one query definition back to canonical .gess source.
func RenderQuery(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessQuery(revision, name)
}

// RenderFunction renders one deffunction definition back to canonical
// .gess source.
func RenderFunction(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessFunction(revision, name)
}

// InitialFacts returns the deffacts-declared seed facts from doc for use
// with [session.WithInitialFacts]. It is safe to call with a nil doc, and
// returns nil if doc has not been through [Load] yet.
func InitialFacts(doc *Document) []session.InitialFact {
	if doc == nil {
		return nil
	}
	return doc.InitialFacts()
}
