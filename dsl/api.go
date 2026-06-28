package dsl

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

type (
	Registry           = engine.DSLRegistry
	CallFunc           = engine.DSLCallFunc
	Document           = engine.GessDocument
	SourceSpan         = engine.SourceSpan
	FileError          = engine.GessFileError
	SourceFile         = engine.GessSourceFile
	GoGeneratorOptions = engine.GessGoGeneratorOptions
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

func InitialFacts(doc *Document) []session.InitialFact {
	if doc == nil {
		return nil
	}
	return doc.InitialFacts()
}
