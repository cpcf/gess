package dsl

import (
	"context"
	"errors"
	"fmt"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/internal/gesssexp"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

// Registry supplies the host-provided actions, call targets, and pure functions
// that a .gess file's (call ...) forms and expressions reference. [Load] and
// [Compile] fail if a .gess file names an action, call, or function that
// Registry doesn't provide.
type Registry struct {
	Actions   map[string]rules.ActionFunc
	Calls     map[string]CallFunc
	Functions []rules.PureFunctionSpec
}

func (r Registry) engineRegistry() engine.DSLRegistry {
	out := engine.DSLRegistry{
		Functions: r.Functions,
	}
	if len(r.Actions) > 0 {
		out.Actions = make(map[string]engine.ActionFunc, len(r.Actions))
		for name, action := range r.Actions {
			out.Actions[name] = engine.ActionFunc(action)
		}
	}
	if len(r.Calls) > 0 {
		out.Calls = make(map[string]engine.DSLCallFunc, len(r.Calls))
		for name, call := range r.Calls {
			out.Calls[name] = engine.DSLCallFunc(call)
		}
	}
	return out
}

// CallFunc is the signature host code implements to back a .gess
// (call name arg...) action. It receives the action context and the evaluated
// argument values, and returns an error to fail the action.
type CallFunc func(rules.ActionContext, []rules.Value) error

// SourceSpan is a source location range within a .gess file, used to locate
// parse and lowering errors.
type SourceSpan struct {
	Name        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

func wrapSourceSpan(span engine.SourceSpan) SourceSpan {
	return SourceSpan{
		Name:        span.Name,
		StartLine:   span.StartLine,
		StartColumn: span.StartColumn,
		EndLine:     span.EndLine,
		EndColumn:   span.EndColumn,
	}
}

// String returns the source name and starting line/column.
func (s SourceSpan) String() string {
	if s.Name == "" {
		return fmt.Sprintf("%d:%d", s.StartLine, s.StartColumn)
	}
	return fmt.Sprintf("%s:%d:%d", s.Name, s.StartLine, s.StartColumn)
}

// FileError reports a .gess parser or lowering error with the source span where
// it occurred. It wraps an underlying error where one exists, so errors.Is and
// errors.As work through it.
type FileError struct {
	Span   SourceSpan
	Reason string
	Err    error
}

func (e *FileError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Span.String(), e.Reason, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Span.String(), e.Reason)
}

func (e *FileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func wrapError(err error) error {
	if err == nil {
		return nil
	}
	var fileErr *engine.GessFileError
	if errors.As(err, &fileErr) {
		return &FileError{
			Span:   wrapSourceSpan(fileErr.Span),
			Reason: fileErr.Reason,
			Err:    fileErr.Err,
		}
	}
	var sexpErr *gesssexp.FileError
	if errors.As(err, &sexpErr) {
		return &FileError{
			Span: SourceSpan{
				Name:        sexpErr.Span.Name,
				StartLine:   sexpErr.Span.StartLine,
				StartColumn: sexpErr.Span.StartColumn,
				EndLine:     sexpErr.Span.EndLine,
				EndColumn:   sexpErr.Span.EndColumn,
			},
			Reason: sexpErr.Reason,
			Err:    sexpErr.Err,
		}
	}
	return err
}

// MissingRegistrations lists the host action and call names a parsed Document
// references that a Registry does not provide, as returned by
// Document.MissingRegistrations.
type MissingRegistrations struct {
	Calls   []string
	Actions []string
}

// SymbolKind identifies a named top-level .gess declaration.
type SymbolKind string

const (
	SymbolModule   SymbolKind = "module"
	SymbolTemplate SymbolKind = "template"
	SymbolRule     SymbolKind = "rule"
	SymbolQuery    SymbolKind = "query"
	SymbolFunction SymbolKind = "function"
	SymbolGlobal   SymbolKind = "global"
	SymbolDeffacts SymbolKind = "deffacts"
)

// Symbol is one syntactic top-level declaration from a parsed Document.
type Symbol struct {
	Kind   SymbolKind
	Name   string
	Module string
	Source SourceSpan
}

func wrapMissingRegistrations(missing engine.DSLMissingRegistrations) MissingRegistrations {
	return MissingRegistrations{
		Calls:   missing.Calls,
		Actions: missing.Actions,
	}
}

// SourceFile is one .gess file passed to [GenerateGo], identified by name (for
// error spans) and its raw source bytes.
type SourceFile struct {
	Name   string
	Source []byte
}

func engineSourceFiles(sources []SourceFile) []engine.GessSourceFile {
	out := make([]engine.GessSourceFile, len(sources))
	for i, source := range sources {
		out[i] = engine.GessSourceFile{Name: source.Name, Source: source.Source}
	}
	return out
}

// GoGeneratorOptions controls the package and function name of Go source
// emitted by [GenerateGo]. Empty fields default to "main" and "BuildRuleset".
type GoGeneratorOptions struct {
	PackageName  string
	FunctionName string
}

func (o GoGeneratorOptions) engineOptions() engine.GessGoGeneratorOptions {
	return engine.GessGoGeneratorOptions{
		PackageName:  o.PackageName,
		FunctionName: o.FunctionName,
	}
}

// Document is a parsed .gess source file. [Parse] produces a Document; [Load]
// lowers it into a Workspace and populates the facts returned by
// [InitialFacts].
type Document struct {
	doc *engine.GessDocument
}

func wrapDocument(doc *engine.GessDocument) *Document {
	if doc == nil {
		return nil
	}
	return &Document{doc: doc}
}

func (d *Document) engineDocument() *engine.GessDocument {
	if d == nil {
		return nil
	}
	return d.doc
}

// Name returns the source name supplied to Parse.
func (d *Document) Name() string {
	return d.engineDocument().Name()
}

// InitialFacts returns facts declared by deffacts after Load has lowered the
// document.
func (d *Document) InitialFacts() []session.InitialFact {
	return d.engineDocument().InitialFacts()
}

// Symbols returns named top-level declarations in source order. Loading and
// compilation remain authoritative for semantic validity.
func (d *Document) Symbols() []Symbol {
	if d == nil {
		return nil
	}
	engineSymbols := d.engineDocument().Symbols()
	symbols := make([]Symbol, len(engineSymbols))
	for index, symbol := range engineSymbols {
		symbols[index] = Symbol{
			Kind:   SymbolKind(symbol.Kind),
			Name:   symbol.Name,
			Module: symbol.Module,
			Source: wrapSourceSpan(symbol.Source),
		}
	}
	return symbols
}

// MissingRegistrations returns host action and call registrations referenced by
// the parsed source but absent from registry.
func (d *Document) MissingRegistrations(registry Registry) MissingRegistrations {
	return wrapMissingRegistrations(d.engineDocument().MissingRegistrations(registry.engineRegistry()))
}

// Parse parses .gess source bytes into a Document without loading it into
// a workspace. name is stored as the document's identity and used in
// error spans. The result's [InitialFacts] are empty until [Load] runs.
func Parse(name string, source []byte) (*Document, error) {
	doc, err := engine.ParseGess(name, source)
	if err != nil {
		return nil, wrapError(err)
	}
	return wrapDocument(doc), nil
}

// Format parses source and returns the canonical byte layout used by gessfmt,
// preserving comments and reporting syntax failures as FileError values.
func Format(name string, source []byte) ([]byte, error) {
	formatted, err := gesssexp.Format(name, source)
	if err != nil {
		return nil, wrapError(err)
	}
	return formatted, nil
}

// Load lowers a parsed Document into workspace, registering its modules,
// templates, rules, queries, and deffacts seed facts, and resolving its
// (call ...) forms and pure functions against registry. On success it
// populates doc's [InitialFacts]. Loading fails if the document references
// an action, call, or function name registry doesn't provide.
func Load(ctx context.Context, workspace *rules.Workspace, doc *Document, registry Registry) error {
	var engineWorkspace *engine.Workspace
	if workspace != nil {
		var err error
		engineWorkspace, err = engine.WorkspaceFromPublic(workspace)
		if err != nil {
			return wrapError(err)
		}
	}
	return wrapError(engine.LoadGess(ctx, engineWorkspace, doc.engineDocument(), registry.engineRegistry()))
}

// Compile parses and loads .gess source into a new workspace and compiles
// it in one step, equivalent to [Parse] followed by [Load] and
// Workspace.Compile. Use Parse and Load directly to load multiple .gess
// files into one shared workspace before compiling, or to keep the
// intermediate Document for [InitialFacts].
func Compile(ctx context.Context, name string, source []byte, registry Registry) (*rules.Ruleset, error) {
	ruleset, err := engine.CompileGess(ctx, name, source, registry.engineRegistry())
	if err != nil {
		return nil, wrapError(err)
	}
	return engine.PublicRuleset(ruleset), nil
}

// GenerateGo emits Go source that builds the same workspace as the given
// .gess sources, without parsing those files at application startup. The
// generated build function takes a [Registry] and validates every (call
// ...) name against it. If formatting the generated source fails,
// GenerateGo returns the unformatted bytes alongside the error.
func GenerateGo(ctx context.Context, sources []SourceFile, opts GoGeneratorOptions) ([]byte, error) {
	out, err := engine.GenerateGessGo(ctx, engineSourceFiles(sources), opts.engineOptions())
	if err != nil {
		return out, wrapError(err)
	}
	return out, nil
}

// RenderRuleset renders every module, template, function, rule, and query
// in a compiled ruleset back to canonical .gess source.
func RenderRuleset(revision *rules.Ruleset) ([]byte, error) {
	engineRevision, err := engineRuleset(revision)
	if err != nil {
		return nil, err
	}
	return engine.RenderGessRuleset(engineRevision)
}

// RenderModule renders one module of a compiled ruleset, and everything
// defined in it, back to canonical .gess source.
func RenderModule(revision *rules.Ruleset, name rules.ModuleName) ([]byte, error) {
	engineRevision, err := engineRuleset(revision)
	if err != nil {
		return nil, err
	}
	return engine.RenderGessModule(engineRevision, name)
}

// RenderTemplate renders one template definition back to canonical .gess
// source.
func RenderTemplate(revision *rules.Ruleset, name string) ([]byte, error) {
	engineRevision, err := engineRuleset(revision)
	if err != nil {
		return nil, err
	}
	return engine.RenderGessTemplate(engineRevision, name)
}

// RenderRule renders one rule definition back to canonical .gess source.
func RenderRule(revision *rules.Ruleset, name string) ([]byte, error) {
	engineRevision, err := engineRuleset(revision)
	if err != nil {
		return nil, err
	}
	return engine.RenderGessRule(engineRevision, name)
}

// RenderQuery renders one query definition back to canonical .gess source.
func RenderQuery(revision *rules.Ruleset, name string) ([]byte, error) {
	engineRevision, err := engineRuleset(revision)
	if err != nil {
		return nil, err
	}
	return engine.RenderGessQuery(engineRevision, name)
}

// RenderFunction renders one deffunction definition back to canonical
// .gess source.
func RenderFunction(revision *rules.Ruleset, name string) ([]byte, error) {
	engineRevision, err := engineRuleset(revision)
	if err != nil {
		return nil, err
	}
	return engine.RenderGessFunction(engineRevision, name)
}

func engineRuleset(revision *rules.Ruleset) (*engine.Ruleset, error) {
	if revision == nil {
		return nil, nil
	}
	return engine.RulesetFromPublic(revision)
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
