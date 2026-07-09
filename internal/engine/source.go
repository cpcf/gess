package engine

import (
	"errors"
	"fmt"
)

func sourceSpanIsZero(span SourceSpan) bool {
	return span.Name == "" && span.StartLine == 0 && span.StartColumn == 0 && span.EndLine == 0 && span.EndColumn == 0
}

func sourceSpanLocation(span SourceSpan) string {
	if sourceSpanIsZero(span) {
		return ""
	}
	if span.Name == "" {
		return fmt.Sprintf("%d:%d", span.StartLine, span.StartColumn)
	}
	return fmt.Sprintf("%s:%d:%d", span.Name, span.StartLine, span.StartColumn)
}

func firstSourceSpan(spans ...SourceSpan) SourceSpan {
	for _, span := range spans {
		if !sourceSpanIsZero(span) {
			return span
		}
	}
	return SourceSpan{}
}

func attachValidationErrorSource(err error, source SourceSpan) error {
	if err == nil || sourceSpanIsZero(source) {
		return err
	}
	var validation *ValidationError
	if errors.As(err, &validation) && sourceSpanIsZero(validation.Source) {
		validation.Source = source
	}
	return err
}
