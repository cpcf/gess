package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// evalBuiltin loads a single-rule .gess source that binds a computed value into
// a result fact via the built-in under test, runs it, and returns the produced
// result fact's value slot.
func evalBuiltinExpr(t *testing.T, resultKind, expr string) Value {
	t.Helper()
	ctx := context.Background()
	source := `
(deftemplate seed
  (slot n (type INT) (required TRUE)))

(deftemplate result
  (slot value (type ` + resultKind + `) (required TRUE)))

(deftemplate observed
  (slot value (type ` + resultKind + `) (required TRUE)))

(deffacts facts
  (seed (n 3)))

(defrule compute
  ?s <- (seed (n ?n))
  =>
  (assert (result (value ` + expr + `))))

(defrule observe
  (result (value ?v))
  =>
  (assert (observed (value ?v))))
`
	session := newGessSession(t, source)
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run for %q: %v", expr, err)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	results := snap.FactsByName("result")
	if len(results) != 1 {
		t.Fatalf("result facts = %d, want 1 (expr %q)", len(results), expr)
	}
	value, ok := results[0].Field("value")
	if !ok {
		t.Fatalf("result missing value field (expr %q)", expr)
	}
	return value
}

func TestBuiltinArithmetic(t *testing.T) {
	cases := []struct {
		expr string
		kind string
		want any
	}{
		{"(+ ?n 2)", "INT", int64(5)},
		{"(- ?n 1)", "INT", int64(2)},
		{"(* ?n 4)", "INT", int64(12)},
		{"(+ ?n 1.5)", "FLOAT", 4.5},
		{"(/ ?n 2)", "FLOAT", 1.5},
		{"(mod 7 ?n)", "INT", int64(1)},
		{"(abs (- 0 ?n))", "INT", int64(3)},
		{"(min ?n 1)", "INT", int64(1)},
		{"(max ?n 10)", "INT", int64(10)},
		{"(integer 4.9)", "INT", int64(4)},
		{"(float ?n)", "FLOAT", 3.0},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got := evalBuiltinExpr(t, tc.kind, tc.expr)
			switch want := tc.want.(type) {
			case int64:
				v, ok := got.AsInt64()
				if !ok || v != want {
					t.Fatalf("%s = %v, want %d", tc.expr, got, want)
				}
			case float64:
				v, ok := got.AsFloat64()
				if !ok || v != want {
					t.Fatalf("%s = %v, want %g", tc.expr, got, want)
				}
			}
		})
	}
}

func TestBuiltinStringFunctions(t *testing.T) {
	cases := []struct {
		expr string
		kind string
		want any
	}{
		{`(str-cat "a" "b")`, "STRING", "ab"},
		{`(str-length "hello")`, "INT", int64(5)},
		{`(sub-string "hello" 1 4)`, "STRING", "ell"},
		{`(upcase "hi")`, "STRING", "HI"},
		{`(lowcase "HI")`, "STRING", "hi"},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got := evalBuiltinExpr(t, tc.kind, tc.expr)
			switch want := tc.want.(type) {
			case string:
				v, ok := got.AsString()
				if !ok || v != want {
					t.Fatalf("%s = %v, want %q", tc.expr, got, want)
				}
			case int64:
				v, ok := got.AsInt64()
				if !ok || v != want {
					t.Fatalf("%s = %v, want %d", tc.expr, got, want)
				}
			}
		})
	}
}

func TestBuiltinTypePredicates(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"(numberp ?n)", true},
		{"(integerp ?n)", true},
		{"(floatp ?n)", false},
		{`(stringp "s")`, true},
		{"(stringp ?n)", false},
		{"(booleanp TRUE)", true},
		{"(integerp 1.5)", false},
		{"(floatp 1.5)", true},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got := evalBuiltinExpr(t, "BOOL", tc.expr)
			v, ok := got.AsBool()
			if !ok || v != tc.want {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestBuiltinDivideByZeroIsTypedError(t *testing.T) {
	ctx := context.Background()
	session := newGessSession(t, `
(deftemplate seed (slot n (type INT) (required TRUE)))
(deftemplate result (slot value (type FLOAT) (required TRUE)))
(deffacts facts (seed (n 0)))
(defrule compute
  ?s <- (seed (n ?n))
  =>
  (assert (result (value (/ 1 ?n)))))
`)
	defer session.Close()
	_, err := session.Run(ctx)
	if !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("Run error = %v, want wrapping ErrDivideByZero", err)
	}
}

func TestBuiltinModByZeroIsTypedError(t *testing.T) {
	ctx := context.Background()
	session := newGessSession(t, `
(deftemplate seed (slot n (type INT) (required TRUE)))
(deftemplate result (slot value (type INT) (required TRUE)))
(deffacts facts (seed (n 0)))
(defrule compute
  ?s <- (seed (n ?n))
  =>
  (assert (result (value (mod 1 ?n)))))
`)
	defer session.Close()
	_, err := session.Run(ctx)
	if !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("Run error = %v, want wrapping ErrDivideByZero", err)
	}
}

func TestBuiltinStringArgTypeMismatchRejectedAtCompile(t *testing.T) {
	ctx := context.Background()
	// str-length declares a STRING arg, so passing an INT binding is a
	// compile-time type error, not a runtime failure.
	source := []byte(`
(deftemplate seed (slot n (type INT) (required TRUE)))
(defrule bad
  ?s <- (seed (n ?n))
  (test (> (str-length ?n) 0))
  =>
  (halt))
`)
	doc, err := ParseGess("mismatch.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	// The kind mismatch is caught when the expression is compiled.
	_, err = workspace.Compile(ctx)
	if err == nil {
		t.Fatalf("Compile succeeded, want a type error")
	}
	if !strings.Contains(err.Error(), "incompatible type") {
		t.Fatalf("error = %q, want an incompatible-type error", err.Error())
	}
}

func TestBuiltinShadowingRejected(t *testing.T) {
	ctx := context.Background()
	t.Run("host function", func(t *testing.T) {
		workspace := NewWorkspace()
		if err := workspace.AddFunction(PureFunctionSpec{
			Name:   "+",
			Args:   []ValueKind{ValueInt, ValueInt},
			Return: ValueInt,
			Func2: func(_ context.Context, a, b Value) (Value, error) {
				return NewValue(int64(0))
			},
		}); err != nil {
			t.Fatalf("AddFunction: %v", err)
		}
		_, err := workspace.Compile(ctx)
		if err == nil || !strings.Contains(err.Error(), "shadows a built-in") {
			t.Fatalf("Compile error = %v, want shadows-a-built-in", err)
		}
	})
	t.Run("deffunction", func(t *testing.T) {
		source := []byte(`
(deffunction str-cat
  (param ?a STRING)
  (return STRING)
  ?a)
`)
		doc, err := ParseGess("shadow.gess", source)
		if err != nil {
			t.Fatalf("ParseGess: %v", err)
		}
		err = LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{})
		if err == nil {
			// deffunction registration may defer collision to Compile; try both.
			workspace := NewWorkspace()
			if loadErr := LoadGess(ctx, workspace, doc, DSLRegistry{}); loadErr == nil {
				_, err = workspace.Compile(ctx)
			} else {
				err = loadErr
			}
		}
		if err == nil || !strings.Contains(err.Error(), "shadows a built-in") {
			t.Fatalf("error = %v, want shadows-a-built-in", err)
		}
	})
}

func TestBuiltinUsableInModifyAndTest(t *testing.T) {
	ctx := context.Background()
	// A built-in in both a test guard and a modify set value.
	session := newGessSession(t, `
(deftemplate account
  (slot id (type STRING) (required TRUE))
  (slot a (type INT) (required TRUE))
  (slot b (type INT) (required TRUE))
  (slot total (type INT) (required TRUE)))

(deftemplate observed (slot total (type INT) (required TRUE)))

(deffacts facts
  (account (id "A-1") (a 4) (b 6) (total 0)))

(defrule total-when-large
  ?acct <- (account (a ?a) (b ?b) (total 0))
  (test (> (+ ?a ?b) 5))
  =>
  (modify ?acct (set (total (+ ?a ?b)))))

(defrule observe
  (account (total ?total) (id ?id))
  (test (> ?total 0))
  =>
  (assert (observed (total ?total))))
`)
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	acct := snap.FactsByName("account")[0]
	total, _ := acct.Field("total")
	if v, _ := total.AsInt64(); v != 10 {
		t.Fatalf("total = %v, want 10", total)
	}
}
