package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseGessReportsSourceSpans(t *testing.T) {
	_, err := ParseGess("broken.gess", []byte("(deftemplate account\n  (slot id)\n"))
	if err == nil {
		t.Fatal("ParseGess succeeded, want error")
	}
	var gessErr *GessFileError
	if !errors.As(err, &gessErr) {
		t.Fatalf("error = %T, want *GessFileError", err)
	}
	if gessErr.Span.Name != "broken.gess" || gessErr.Span.StartLine != 1 || gessErr.Span.StartColumn != 1 {
		t.Fatalf("span = %+v, want broken.gess:1:1", gessErr.Span)
	}
}

func TestGessDSLCompilesTemplatesFactsRulesAndQueries(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate order
  (slot id (type STRING) (required TRUE))
  (slot customer (type STRING) (required TRUE))
  (slot sku (type STRING) (required TRUE)))

(deftemplate customer
  (slot id (type STRING) (required TRUE))
  (slot segment (type STRING) (required TRUE)))

(deftemplate inventory
  (slot sku (type STRING) (required TRUE))
  (slot warehouse (type STRING) (required TRUE))
  (slot available (type BOOLEAN) (required TRUE)))

(deftemplate route
  (declare (duplicate-policy unique-key) (duplicate-key order))
  (slot order (type STRING) (required TRUE))
  (slot lane (type STRING) (required TRUE))
  (slot warehouse (type STRING) (required TRUE)))

(deffacts seed
  (order (id "O-1") (customer "C-1") (sku "SKU-1"))
  (order (id "O-2") (customer "C-2") (sku "SKU-1"))
  (customer (id "C-1") (segment "vip"))
  (customer (id "C-2") (segment "standard"))
  (inventory (sku "SKU-1") (warehouse "W-1") (available TRUE)))

(defrule route-vip
  ?order <- (order (customer ?customer) (sku ?sku))
  (customer (id ?customer) (segment "vip"))
  (inventory (sku ?sku) (available TRUE) (warehouse ?warehouse))
  (test (= ?customer "C-1"))
  =>
  (assert (route (order ?order:id) (lane "expedite") (warehouse ?warehouse))))

(defquery routes-by-lane
  (declare (variables ?lane))
  ?route <- (route (lane ?lane) (order ?order) (warehouse ?warehouse))
  (return (order ?order) (warehouse ?warehouse)))
`)
	doc, err := ParseGess("routing.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunCompleted || run.Fired != 1 {
		t.Fatalf("run = (%v, %d), want (%v, 1)", run.Status, run.Fired, RunCompleted)
	}
	rows, err := session.QueryAll(ctx, "routes-by-lane", QueryArgs{"lane": "expedite"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	assertGessDSLRowString(t, rows[0], "order", "O-1")
	assertGessDSLRowString(t, rows[0], "warehouse", "W-1")
}

func TestGessDSLCompilesBackwardChainingDemandRules(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate edge
  (declare (duplicate-policy unique-key) (duplicate-key src dst))
  (slot src (type STRING) (required TRUE))
  (slot dst (type STRING) (required TRUE)))

(deftemplate reachable
  (declare (backchain-reactive TRUE) (duplicate-policy unique-key) (duplicate-key src dst))
  (slot src (type STRING) (required TRUE))
  (slot dst (type STRING) (required TRUE)))

(deffacts graph
  (edge (src "A") (dst "B"))
  (edge (src "B") (dst "C")))

(defrule direct-reachability
  ?need <- (need-reachable (src ?src) (dst ?dst))
  (edge (src ?src) (dst ?dst))
  =>
  (assert (reachable (src ?src) (dst ?dst))))

(defrule transitive-reachability
  ?need <- (need-reachable (src ?src) (dst ?dst))
  ?edge <- (edge (src ?src) (dst ?hop))
  (reachable (src ?hop) (dst ?dst))
  =>
  (assert (reachable (src ?src) (dst ?dst))))

(defquery reachable-paths
  (declare (variables ?src ?dst))
  ?reachable <- (reachable (src ?src) (dst ?dst))
  (return (src ?reachable:src) (dst ?reachable:dst)))
`)
	doc, err := ParseGess("reachability.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	rows, err := session.QueryAll(ctx, "reachable-paths", QueryArgs{"src": "A", "dst": "C"})
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	assertGessDSLRowString(t, rows[0], "src", "A")
	assertGessDSLRowString(t, rows[0], "dst", "C")
}

func TestGessDSLCompilesNegationAggregatesAndTests(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate account
  (slot id (type STRING) (required TRUE)))

(deftemplate transaction
  (slot account (type STRING) (required TRUE))
  (slot window (type STRING) (required TRUE))
  (slot amount (type INTEGER) (required TRUE)))

(deftemplate hold
  (slot account (type STRING) (required TRUE)))

(deftemplate velocity-alert
  (declare (duplicate-policy unique-key) (duplicate-key account))
  (slot account (type STRING) (required TRUE))
  (slot count (type INTEGER) (required TRUE))
  (slot total (type INTEGER) (required TRUE)))

(deffacts seed
  (account (id "A-1"))
  (account (id "A-2"))
  (transaction (account "A-1") (window "5m") (amount 450))
  (transaction (account "A-1") (window "5m") (amount 400))
  (transaction (account "A-1") (window "5m") (amount 300))
  (transaction (account "A-2") (window "5m") (amount 900))
  (hold (account "A-2")))

(defrule alert-on-velocity
  ?account <- (account (id ?account-id))
  (not (hold (account ?account-id)))
  (accumulate
    (transaction (account ?account-id) (window "5m") (amount ?amount))
    (bind ?count (count))
    (bind ?total (sum ?amount)))
  =>
  (assert (velocity-alert (account ?account-id) (count ?count) (total ?total))))

(defquery velocity-alerts
  ?alert <- (velocity-alert (account ?account) (count ?count) (total ?total))
  (return (account ?account) (count ?count) (total ?total)))
`)
	doc, err := ParseGess("aggregate.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunCompleted || run.Fired != 1 {
		t.Fatalf("run = (%v, %d), want (%v, 1)", run.Status, run.Fired, RunCompleted)
	}
	rows, err := session.QueryAll(ctx, "velocity-alerts", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	assertGessDSLRowString(t, rows[0], "account", "A-1")
	assertGessDSLRowInt(t, rows[0], "count", 3)
	assertGessDSLRowInt(t, rows[0], "total", 1150)
}

func TestGessDSLAndConditionSupportsBindingArrows(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate item
  (slot id (type STRING) (required TRUE))
  (slot status (type STRING) (required TRUE)))

(deftemplate flagged
  (declare (duplicate-policy unique-key) (duplicate-key id))
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (item (id "I-1") (status "new"))
  (item (id "I-2") (status "done")))

(defrule flag-new-items
  (and ?item <- (item (status "new"))
       (not (flagged (id ?item:id))))
  =>
  (assert (flagged (id ?item:id))))

(defrule single-child-and
  (and ?item <- (item (status "done")))
  =>
  (assert (flagged (id ?item:id))))

(defquery flagged-items
  ?flag <- (flagged (id ?id))
  (return (id ?id)))
`)
	doc, err := ParseGess("and-binding.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunCompleted || run.Fired != 2 {
		t.Fatalf("run = (%v, %d), want (%v, 2)", run.Status, run.Fired, RunCompleted)
	}
	rows, err := session.QueryAll(ctx, "flagged-items", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
}

func TestGessDSLRejectsAggregateResultStandaloneTest(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate item
  (slot id (type STRING) (required TRUE)))

(defrule unsupported-aggregate-test
  (accumulate
    (item (id ?id))
    (bind ?count (count)))
  (test (> ?count 0))
  =>
  (halt))
`)
	doc, err := ParseGess("unsupported.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	err = LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{})
	if err == nil {
		t.Fatal("LoadGess succeeded, want unsupported aggregate-result test error")
	}
	var gessErr *GessFileError
	if !errors.As(err, &gessErr) {
		t.Fatalf("error = %T, want *GessFileError", err)
	}
	if got := gessErr.Reason; got != "test over aggregate result ?count is not supported by the graph runtime" {
		t.Fatalf("reason = %q", got)
	}
}

func TestGessDSLRejectsDeffactsForUndeclaredTemplate(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate item
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (item (id "I-1"))
  (gadget (id "G-1")))
`)
	doc, err := ParseGess("undeclared-deffacts.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	err = LoadGess(ctx, NewWorkspace(), doc, DSLRegistry{})
	if err == nil {
		t.Fatal("LoadGess succeeded, want an error for the undeclared deffacts template")
	}
	var gessErr *GessFileError
	if !errors.As(err, &gessErr) {
		t.Fatalf("error = %T, want *GessFileError", err)
	}
	if got := gessErr.Reason; got != `deffacts fact "gadget" is not a declared template` {
		t.Fatalf("reason = %q", got)
	}
}

func TestGessDSLCallSupportsRegisteredActionsAndArgumentCalls(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deftemplate item
  (slot id (type STRING) (required TRUE)))

(deffacts seed
  (item (id "I-1")))

(defrule call-host
  ?item <- (item (id ?id))
  =>
  (call mark)
  (call record ?item:id "seen"))
`)
	doc, err := ParseGess("call.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	var marked int
	var recorded []string
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{
		Actions: map[string]ActionFunc{
			"mark": func(ActionContext) error {
				marked++
				return nil
			},
		},
		Calls: map[string]DSLCallFunc{
			"record": func(_ ActionContext, args []Value) error {
				if len(args) != 2 {
					t.Fatalf("args len = %d, want 2", len(args))
				}
				id, _ := args[0].AsString()
				status, _ := args[1].AsString()
				recorded = append(recorded, id+":"+status)
				return nil
			},
		},
	}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	run, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != RunCompleted || run.Fired != 1 {
		t.Fatalf("run = (%v, %d), want (%v, 1)", run.Status, run.Fired, RunCompleted)
	}
	if marked != 1 {
		t.Fatalf("marked = %d, want 1", marked)
	}
	if got, want := strings.Join(recorded, ","), "I-1:seen"; got != want {
		t.Fatalf("recorded = %q, want %q", got, want)
	}
}

func TestGessDSLCompilesExpressionFunctions(t *testing.T) {
	ctx := context.Background()
	source := []byte(`
(deffunction high-score
  (description "score threshold")
  (param ?score INT)
  (return BOOL)
  (>= ?score 90))

(deffunction publishable
  (param ?score INT)
  (return BOOL)
  (high-score ?score))

(deffunction go-high-score
  (param ?score INT)
  (return BOOL)
  (gte ?score 90))

(deftemplate finding
  (slot id (type STRING) (required TRUE))
  (slot score (type INT) (required TRUE)))

(deffacts seed
  (finding (id "F-1") (score 95))
  (finding (id "F-2") (score 10)))

(defrule publish-high
  ?finding <- (finding (id ?id) (score ?score))
  (test (publishable ?score))
  =>
  (call record ?id))

(defquery scores
  ?finding <- (finding (id ?id) (score ?score))
  (return (score ?score) (high (high-score ?score)) (go_high (go-high-score ?score))))
`)
	var recorded []string
	revision, err := CompileGess(ctx, "functions.gess", source, DSLRegistry{
		Calls: map[string]DSLCallFunc{
			"record": func(_ ActionContext, args []Value) error {
				id, _ := args[0].AsString()
				recorded = append(recorded, id)
				return nil
			},
		},
		Functions: []PureFunctionSpec{{
			Name:   "gte",
			Args:   []ValueKind{ValueInt, ValueInt},
			Return: ValueBool,
			Func2: func(_ context.Context, left, right Value) (Value, error) {
				leftInt, _ := left.AsInt64()
				rightInt, _ := right.AsInt64()
				return NewValue(leftInt >= rightInt)
			},
		}},
	})
	if err != nil {
		t.Fatalf("CompileGess: %v", err)
	}
	definition, ok := revision.Function("high-score")
	if !ok || !definition.ExpressionBacked() || definition.Description() != "score threshold" {
		t.Fatalf("high-score definition = (%#v, %v), want expression-backed with description", definition, ok)
	}
	doc, err := ParseGess("functions.gess", source)
	if err != nil {
		t.Fatalf("ParseGess: %v", err)
	}
	workspace := NewWorkspace()
	if err := LoadGess(ctx, workspace, doc, DSLRegistry{
		Calls: map[string]DSLCallFunc{
			"record": func(ActionContext, []Value) error { return nil },
		},
		Functions: []PureFunctionSpec{{
			Name:   "gte",
			Args:   []ValueKind{ValueInt, ValueInt},
			Return: ValueBool,
			Func2: func(_ context.Context, left, right Value) (Value, error) {
				leftInt, _ := left.AsInt64()
				rightInt, _ := right.AsInt64()
				return NewValue(leftInt >= rightInt)
			},
		}},
	}); err != nil {
		t.Fatalf("LoadGess: %v", err)
	}
	session, err := NewSession(revision, WithInitialFacts(doc.InitialFacts()...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	result, err := session.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Fired != 1 || len(recorded) != 1 || recorded[0] != "F-1" {
		t.Fatalf("run/recorded = %d/%#v, want one F-1 record", result.Fired, recorded)
	}
	rows, err := session.QueryAll(ctx, "scores", nil)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
}

func TestGessDSLExpressionFunctionValidation(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		registry  DSLRegistry
		wantGess  bool
		wantError error
	}{
		{
			name: "unknown parameter",
			source: `(deffunction bad
  (param ?score INT)
  (return BOOL)
  (> ?missing 10))`,
			wantGess: true,
		},
		{
			name: "kind mismatch",
			source: `(deffunction bad
  (param ?score STRING)
  (return BOOL)
  (> ?score 10))`,
			wantError: ErrFunctionValidation,
		},
		{
			name: "wrong arity",
			source: `(deffunction ok
  (param ?score INT)
  (return BOOL)
  (> ?score 10))
(deftemplate finding (slot score (type INT)))
(defrule bad
  ?finding <- (finding (score ?score))
  (test (ok ?score 10))
  =>
  (call noop))`,
			registry:  DSLRegistry{Actions: map[string]ActionFunc{"noop": func(ActionContext) error { return nil }}},
			wantError: ErrFunctionValidation,
		},
		{
			name: "recursion",
			source: `(deffunction loop
  (param ?score INT)
  (return BOOL)
  (loop ?score))`,
			wantError: ErrFunctionValidation,
		},
		{
			name: "registered collision",
			source: `(deffunction exists
  (param ?score INT)
  (return BOOL)
  (> ?score 10))`,
			registry: DSLRegistry{Functions: []PureFunctionSpec{{
				Name:   "exists",
				Args:   []ValueKind{ValueInt},
				Return: ValueBool,
				Func1:  func(context.Context, Value) (Value, error) { return NewValue(true) },
			}}},
			wantGess:  true,
			wantError: ErrFunctionValidation,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileGess(context.Background(), "bad-functions.gess", []byte(tc.source), tc.registry)
			if err == nil {
				t.Fatal("CompileGess succeeded, want error")
			}
			if tc.wantError != nil && !errors.Is(err, tc.wantError) {
				t.Fatalf("error = %v, want %v", err, tc.wantError)
			}
			var gessErr *GessFileError
			if tc.wantGess && !errors.As(err, &gessErr) {
				t.Fatalf("error = %T, want *GessFileError", err)
			}
		})
	}
}

func assertGessDSLRowString(t *testing.T, row QueryRow, alias, want string) {
	t.Helper()
	value, ok := row.Value(alias)
	if !ok {
		t.Fatalf("row missing alias %q", alias)
	}
	got, ok := value.AsString()
	if !ok {
		t.Fatalf("row alias %q = %v, want string", alias, value)
	}
	if got != want {
		t.Fatalf("row alias %q = %q, want %q", alias, got, want)
	}
}

func assertGessDSLRowInt(t *testing.T, row QueryRow, alias string, want int64) {
	t.Helper()
	value, ok := row.Value(alias)
	if !ok {
		t.Fatalf("row missing alias %q", alias)
	}
	got, ok := value.AsInt64()
	if !ok {
		t.Fatalf("row alias %q = %v, want int", alias, value)
	}
	if got != want {
		t.Fatalf("row alias %q = %d, want %d", alias, got, want)
	}
}

func TestGessDSLExpressionFunctionBodyErrorsCarrySpanFunctionAndCause(t *testing.T) {
	ctx := context.Background()
	source := []byte(`(deffunction bad
  (param ?score INT)
  (return BOOL)
  (missing-helper ?score))`)
	_, err := CompileGess(ctx, "body-error.gess", source, DSLRegistry{})
	if err == nil {
		t.Fatal("CompileGess succeeded, want error")
	}
	if !errors.Is(err, ErrFunctionValidation) {
		t.Fatalf("errors.Is(ErrFunctionValidation) = false for %v", err)
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T, want *ValidationError", err)
	}
	if validation.FunctionName != "bad" {
		t.Fatalf("FunctionName = %q, want %q", validation.FunctionName, "bad")
	}
	if validation.Source.Name != "body-error.gess" || validation.Source.StartLine != 1 {
		t.Fatalf("Source = %+v, want body-error.gess:1", validation.Source)
	}
	msg := err.Error()
	for _, fragment := range []string{"body-error.gess:1:1", `for function "bad"`, "missing-helper"} {
		if !strings.Contains(msg, fragment) {
			t.Fatalf("error message %q missing %q", msg, fragment)
		}
	}
}

func TestGessDSLExpressionFunctionCollisionNamesFunction(t *testing.T) {
	ctx := context.Background()
	source := []byte(`(deffunction exists
  (param ?score INT)
  (return BOOL)
  (> ?score 10))`)
	registry := DSLRegistry{Functions: []PureFunctionSpec{{
		Name:   "exists",
		Args:   []ValueKind{ValueInt},
		Return: ValueBool,
		Func1:  func(context.Context, Value) (Value, error) { return NewValue(true) },
	}}}
	_, err := CompileGess(ctx, "collision.gess", source, registry)
	if err == nil {
		t.Fatal("CompileGess succeeded, want error")
	}
	if !strings.Contains(err.Error(), `for function "exists"`) {
		t.Fatalf("error message %q does not name the function", err.Error())
	}
}
