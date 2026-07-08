package rules

import "fmt"

// SourceSpan is a source location range within a .gess file, carried into
// compiled definitions and runtime errors for rulesets loaded from .gess
// source.
type SourceSpan struct {
	Name        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// String returns the source name and starting line/column.
func (s SourceSpan) String() string {
	if s.Name == "" {
		return fmt.Sprintf("%d:%d", s.StartLine, s.StartColumn)
	}
	return fmt.Sprintf("%s:%d:%d", s.Name, s.StartLine, s.StartColumn)
}
