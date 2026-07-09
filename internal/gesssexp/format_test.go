package gesssexp

import "testing"

func TestFormatAlignsClosingParens(t *testing.T) {
	source := []byte(`(defrule route-priority-vip-medical
  (claim
    (id ?claim-id)
    (customer ?customer)
    (category "medical")
    (amount ?amount))
  (customer (id ?customer) (tier "vip"))
  (test (> ?amount 1000))
  =>
  (assert (manual-review
    (claim ?claim-id)
    (lane "priority")
    (reason "vip-high-value"))))
`)
	got, err := Format("rules.gess", source)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	const want = `(defrule route-priority-vip-medical
  (claim
    (id ?claim-id)
    (customer ?customer)
    (category "medical")
    (amount ?amount)
  )
  (customer (id ?customer) (tier "vip"))
  (test (> ?amount 1000))
  =>
  (assert (manual-review
    (claim ?claim-id)
    (lane "priority")
    (reason "vip-high-value")
  )
  )
)
`
	if string(got) != want {
		t.Fatalf("formatted =\n%s\nwant =\n%s", got, want)
	}
}

func TestFormatSeparatesTopLevelForms(t *testing.T) {
	source := []byte(`(deftemplate customer (slot id (type STRING) (required TRUE)))
(defquery customers (customer (id ?id)) (return (id ?id)))`)
	got, err := Format("rules.gess", source)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	const want = `(deftemplate customer
  (slot id (type STRING) (required TRUE))
)

(defquery customers
  (customer (id ?id))
  (return
    (id ?id)
  )
)
`
	if string(got) != want {
		t.Fatalf("formatted =\n%s\nwant =\n%s", got, want)
	}
}

func TestFormatKeepsBindingArrowWithPattern(t *testing.T) {
	source := []byte(`(defquery routes-by-lane
  (declare (variables ?lane))
  ?route <- (fulfillment-route
    (lane ?lane)
    (order ?order)
    (warehouse ?warehouse))
  (return
    (order ?order)
    (warehouse ?warehouse)))`)
	got, err := Format("rules.gess", source)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	const want = `(defquery routes-by-lane
  (declare (variables ?lane))
  ?route <- (fulfillment-route
    (lane ?lane)
    (order ?order)
    (warehouse ?warehouse)
  )
  (return
    (order ?order)
    (warehouse ?warehouse)
  )
)
`
	if string(got) != want {
		t.Fatalf("formatted =\n%s\nwant =\n%s", got, want)
	}
}

func TestFormatInlinesDeffunctionReturnKind(t *testing.T) {
	source := []byte(`(deffunction discounted (param ?price INT) (return INT) (- ?price 10))
`)
	got, err := Format("fn.gess", source)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	const want = `(deffunction discounted
  (param ?price INT)
  (return INT)
  (- ?price 10)
)
`
	if string(got) != want {
		t.Fatalf("formatted =\n%s\nwant =\n%s", got, want)
	}
}

func TestFormatKeepsQueryReturnMultiline(t *testing.T) {
	source := []byte(`(defquery routes ?route <- (route (lane ?lane)) (return (lane ?lane)))
`)
	got, err := Format("q.gess", source)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	const want = `(defquery routes ?route <-
  (route (lane ?lane))
  (return
    (lane ?lane)
  )
)
`
	if string(got) != want {
		t.Fatalf("formatted =\n%s\nwant =\n%s", got, want)
	}
}

func TestFormatPreservesCommentsByteIdentically(t *testing.T) {
	const golden = `; file header
; about the template
(deftemplate item
  ; about the id slot
  (slot id (type STRING) (required TRUE)) ; trailing on slot
  ; dangling before close
)

; about the rule
(defrule flag
  (item (id ?id)) ; trailing on condition
  =>
  (emit "flagged " ?id)
)

; tail comment one
; tail comment two
`
	got, err := Format("golden.gess", []byte(golden))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if string(got) != golden {
		t.Fatalf("golden comment file did not round-trip byte-identically:\ngot:\n%s\nwant:\n%s", got, golden)
	}
}
