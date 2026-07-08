package engine

import (
	"github.com/cpcf/gess/internal/gesssexp"
	gessrules "github.com/cpcf/gess/rules"
)

type SourceSpan = gessrules.SourceSpan

// GessFileError reports a Gess file parser or lowering error with source location.
type GessFileError = gesssexp.FileError

type gessSExpr = gesssexp.Expr

func parseGessSExprs(name string, source []byte) ([]gessSExpr, error) {
	return gesssexp.Parse(name, source)
}

func gessAtomValue(expr gessSExpr) (any, error) {
	return gesssexp.AtomValue(expr)
}
