package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGessSemanticValidationErrorsCarryAuthoredSource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		source     string
		wantLine   int
		wantColumn int
		wantReason string
	}{
		{
			name: "expression",
			source: `(deftemplate item
  (slot score (type INT)))
(defrule bad-expression
  ?item <- (item)
  (test (> ?item:missing 0))
  =>
  (halt))`,
			wantLine:   5,
			wantColumn: 3,
			wantReason: "unknown field",
		},
		{
			name: "field constraint",
			source: `(deftemplate item
  (slot score (type INT)))
(defrule bad-constraint
  ?item <- (item (score "high"))
  =>
  (halt))`,
			wantLine:   4,
			wantColumn: 12,
			wantReason: "constraint value kind string can never equal field \"score\" of kind int",
		},
		{
			name: "rule condition",
			source: `(deftemplate item
  (slot score (type INT)))
(defrule duplicate-binding
  ?item <- (item (score 1))
  ?item <- (item (score 2))
  =>
  (halt))`,
			wantLine:   5,
			wantColumn: 12,
			wantReason: "duplicate binding",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileGess(context.Background(), "semantic-errors.gess", []byte(tc.source), DSLRegistry{})
			if err == nil {
				t.Fatal("CompileGess succeeded, want semantic validation error")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("CompileGess error = %T, want *ValidationError: %v", err, err)
			}
			if got := validation.Source; got.Name != "semantic-errors.gess" || got.StartLine != tc.wantLine || got.StartColumn != tc.wantColumn {
				t.Fatalf("validation source = %+v, want semantic-errors.gess:%d:%d", got, tc.wantLine, tc.wantColumn)
			}
			if validation.Reason != tc.wantReason {
				t.Fatalf("validation reason = %q, want %q", validation.Reason, tc.wantReason)
			}
			if location := fmt.Sprintf("semantic-errors.gess:%d:%d", tc.wantLine, tc.wantColumn); !strings.Contains(err.Error(), location) {
				t.Fatalf("validation error = %q, want source location containing %q", err.Error(), location)
			}
		})
	}
}
