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
