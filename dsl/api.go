package dsl

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

type (
	Registry             = engine.DSLRegistry
	CallFunc             = engine.DSLCallFunc
	MissingRegistrations = engine.DSLMissingRegistrations
	Document             = engine.GessDocument
	SourceSpan           = engine.SourceSpan
	FileError            = engine.GessFileError
	SourceFile           = engine.GessSourceFile
	GoGeneratorOptions   = engine.GessGoGeneratorOptions
)

func Parse(name string, source []byte) (*Document, error) {
	return engine.ParseGess(name, source)
}

func Load(ctx context.Context, workspace *rules.Workspace, doc *Document, registry Registry) error {
	return engine.LoadGess(ctx, workspace, doc, registry)
}

func Compile(ctx context.Context, name string, source []byte, registry Registry) (*rules.Ruleset, error) {
	return engine.CompileGess(ctx, name, source, registry)
}

func GenerateGo(ctx context.Context, sources []SourceFile, opts GoGeneratorOptions) ([]byte, error) {
	return engine.GenerateGessGo(ctx, sources, opts)
}

func RenderRuleset(revision *rules.Ruleset) ([]byte, error) {
	return engine.RenderGessRuleset(revision)
}

func RenderModule(revision *rules.Ruleset, name rules.ModuleName) ([]byte, error) {
	return engine.RenderGessModule(revision, name)
}

func RenderTemplate(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessTemplate(revision, name)
}

func RenderRule(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessRule(revision, name)
}

func RenderQuery(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessQuery(revision, name)
}

func RenderFunction(revision *rules.Ruleset, name string) ([]byte, error) {
	return engine.RenderGessFunction(revision, name)
}

func InitialFacts(doc *Document) []session.InitialFact {
	if doc == nil {
		return nil
	}
	return doc.InitialFacts()
}
