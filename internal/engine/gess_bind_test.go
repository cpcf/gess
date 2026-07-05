package engine

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestGessDSLBindReusedAcrossActions(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot subtotal (type INT) (required TRUE))
  (slot tax (type INT) (required TRUE))
  (slot total (type INT) (required TRUE)))

(deftemplate observed (slot total (type INT) (required TRUE)))

(deffacts facts
  (order (id "O-1") (subtotal 100) (tax 8) (total 0)))

(defrule total-order
  ?order <- (order (subtotal ?subtotal) (tax ?tax) (total 0))
  =>
  (bind ?total (+ ?subtotal ?tax))
  (modify ?order (set (total ?total)))
  (emit "total=" ?total))

(defrule observe
  (order (total ?total))
  (test (> ?total 0))
  =>
  (assert (observed (total ?total))))
`, WithOutputWriter(&out))
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	order := snap.FactsByName("order")[0]
	total, _ := order.Field("total")
	if got, _ := total.AsInt64(); got != 108 {
		t.Fatalf("order total = %d, want 108", got)
	}
	if got := out.String(); got != "total=108" {
		t.Fatalf("emit output = %q, want %q", got, "total=108")
	}
}

func TestGessDSLBindLoaderErrors(t *testing.T) {
	ctx := context.Background()
	prelude := `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot subtotal (type INT) (required TRUE)))
`
	cases := []struct {
		name    string
		rule    string
		wantErr string
	}{
		{
			name:    "forward reference to later bind",
			rule:    "(defrule r ?order <- (order (subtotal ?s)) => (bind ?a ?b) (bind ?b ?s))",
			wantErr: "unknown variable",
		},
		{
			name:    "rebind",
			rule:    "(defrule r ?order <- (order (subtotal ?s)) => (bind ?a ?s) (bind ?a ?s))",
			wantErr: "already bound",
		},
		{
			name:    "self reference",
			rule:    "(defrule r ?order <- (order (subtotal ?s)) => (bind ?a ?a))",
			wantErr: "unknown variable",
		},
		{
			name:    "bad target",
			rule:    "(defrule r ?order <- (order (subtotal ?s)) => (bind order ?s))",
			wantErr: "bind target must be a ?name",
		},
		{
			name:    "wrong arity",
			rule:    "(defrule r ?order <- (order (subtotal ?s)) => (bind ?a))",
			wantErr: "bind requires a ?name and one expression",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseGess("bind.gess", []byte(prelude+tc.rule))
			if err != nil {
				t.Fatalf("ParseGess: %v", err)
			}
			err = LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{})
			if err == nil {
				t.Fatalf("LoadGess succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestGessDSLBindNotVisibleOnLHS(t *testing.T) {
	ctx := context.Background()
	// ?total is an RHS-local bind; referencing it in a later rule's LHS must
	// fail as an unknown variable, proving RHS binds do not leak to the LHS.
	source := []byte(`
(deftemplate order (slot subtotal (type INT) (required TRUE)))
(defrule r
  ?order <- (order (subtotal ?subtotal))
  (test (> ?total 0))
  =>
  (bind ?total ?subtotal))
`)
	doc, err := ParseGess("bind.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	err = LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{})
	if err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Fatalf("error = %v, want unknown-variable (RHS bind must not be visible on LHS)", err)
	}
}

func TestGessDSLBindDoesNotLeakAcrossFirings(t *testing.T) {
	ctx := context.Background()
	// Two orders each fire the rule; each firing's bind is independent. If
	// binds leaked across firings the second order would still compute its own
	// value correctly, so we assert both totals are their own subtotal+tax.
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot subtotal (type INT) (required TRUE))
  (slot tax (type INT) (required TRUE))
  (slot total (type INT) (required TRUE)))

(deffacts facts
  (order (id "O-1") (subtotal 10) (tax 1) (total 0))
  (order (id "O-2") (subtotal 20) (tax 2) (total 0)))

(defrule total-order
  ?order <- (order (subtotal ?subtotal) (tax ?tax) (total 0))
  =>
  (bind ?total (+ ?subtotal ?tax))
  (modify ?order (set (total ?total))))
`)
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	want := map[string]int64{"O-1": 11, "O-2": 22}
	for _, order := range snap.FactsByName("order") {
		id := factStringField(t, order, "id")
		total, _ := order.Field("total")
		got, _ := total.AsInt64()
		if got != want[id] {
			t.Fatalf("order %s total = %d, want %d", id, got, want[id])
		}
	}
}

func TestGessDSLReadBindingFieldAfterModify(t *testing.T) {
	ctx := context.Background()
	// After a modify mutates the bound fact, a later action reading an
	// unchanged field of the same binding must still resolve it from the
	// frozen snapshot — including when the fact uses compact slot storage.
	var out bytes.Buffer
	session := newGessSession(t, `
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot subtotal (type INTEGER) (required TRUE))
  (slot tax (type INTEGER) (required TRUE))
  (slot total (type INTEGER) (required TRUE))
  (slot status (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (subtotal 100) (tax 8) (total 0) (status "new")))

(defrule finalize
  ?order <- (order (subtotal ?s) (tax ?t) (status "new"))
  =>
  (bind ?total (+ ?s ?t))
  (modify ?order (set (total ?total) (status "final")))
  (emit "order " ?order:id " total=" ?total))
`, WithOutputWriter(&out))
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "order O-1 total=108" {
		t.Fatalf("emit output = %q, want %q", got, "order O-1 total=108")
	}
	snap, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	order := snap.FactsByName("order")[0]
	total, _ := order.Field("total")
	if got, _ := total.AsInt64(); got != 108 {
		t.Fatalf("total = %d, want 108", got)
	}
	if got := factStringField(t, order, "status"); got != "final" {
		t.Fatalf("status = %q, want final", got)
	}
}

func TestGessDSLFunctionCallActionValueCodegen(t *testing.T) {
	source := []byte(`
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot a (type INT) (required TRUE))
  (slot b (type INT) (required TRUE))
  (slot total (type INT) (required TRUE)))

(deffacts seed
  (order (id "O-1") (a 3) (b 4) (total 0)))

(defrule total-order
  ?order <- (order (a ?a) (b ?b) (total 0))
  =>
  (modify ?order (set (total (+ ?a ?b)))))
`)
	// A function-call action value now compiles to a name-based expression, so
	// Go codegen emits it as an ExpressionSpec literal.
	generated, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "calls.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "rules",
		FunctionName: "BuildOrders",
	})
	if err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "rules_generated.go", generated, parser.AllErrors); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, generated)
	}
	text := string(generated)
	for _, want := range []string{
		"gessrules.ActionEffectModify",
		`gessrules.Call("+"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source missing %q:\n%s", want, text)
		}
	}
}

func TestGenerateGessGoEmitsBind(t *testing.T) {
	source := []byte(`
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot subtotal (type INT) (required TRUE))
  (slot tax (type INT) (required TRUE))
  (slot total (type INT) (required TRUE)))

(deffacts seed
  (order (id "O-1") (subtotal 100) (tax 8) (total 0)))

(defrule total-order
  ?order <- (order (subtotal ?subtotal) (tax ?tax) (total 0))
  =>
  (bind ?total (+ ?subtotal ?tax))
  (modify ?order (set (total ?total))))
`)
	generated, err := GenerateGessGo(context.Background(), []GessSourceFile{{Name: "orders.gess", Source: source}}, GessGoGeneratorOptions{
		PackageName:  "rules",
		FunctionName: "BuildOrders",
	})
	if err != nil {
		t.Fatalf("GenerateGessGo: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "rules_generated.go", generated, parser.AllErrors); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, generated)
	}
	text := string(generated)
	for _, want := range []string{
		"gessrules.ActionEffectBind",
		"gessrules.RHSBindExpr{Name: \"total\"}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated source missing %q:\n%s", want, text)
		}
	}
}
