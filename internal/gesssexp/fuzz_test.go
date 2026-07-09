package gesssexp

import (
	"bytes"
	"testing"
)

var fuzzSeeds = [][]byte{
	[]byte(""),
	[]byte("; comment only\n"),
	[]byte("(deftemplate item\n  (slot id (type STRING) (required TRUE)))\n"),
	[]byte("(deffacts seed\n  (item (id \"I-1\")))\n"),
	[]byte("(deffacts)\n"),
	[]byte(`(defrule route ; trailing comment
  ?o <- (order (lane ?lane) (amount ?amount))
  (not (hold (lane ?lane)))
  (test (> ?amount 10))
  =>
  (assert (routed (lane ?lane)))
  (emit "routed " ?lane))
`),
	[]byte("(defquery q\n  (declare (variables ?id))\n  (item (id ?id))\n  (return (id ?id)))\n"),
	[]byte("(a (b (c (d (e (f))))))"),
	[]byte(`(str "escaped \" quote \\ backslash \n newline")`),
	[]byte("(nums 1 -2 3.5 -4.75 2.0 1e10 TRUE FALSE nil)"),
	[]byte("()"),
	[]byte("(unterminated \"string"),
	[]byte("(((("),
	[]byte("))))"),
	[]byte("(atom . ?var ?binding:field =)"),
}

func FuzzParse(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		if _, err := Parse("fuzz.gess", source); err != nil {
			return
		}
		if _, err := Format("fuzz.gess", source); err != nil {
			t.Fatalf("Parse accepted input but Format failed: %v", err)
		}
	})
}

func FuzzFormatIdempotent(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		once, err := Format("fuzz.gess", source)
		if err != nil {
			return
		}
		twice, err := Format("fuzz.gess", once)
		if err != nil {
			t.Fatalf("formatted output does not reparse: %v\nonce: %q", err, once)
		}
		if !bytes.Equal(once, twice) {
			t.Fatalf("Format is not idempotent:\nonce:  %q\ntwice: %q", once, twice)
		}
	})
}
