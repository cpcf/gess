package dsl_test

import (
	"context"
	"testing"

	dsl "github.com/cpcf/gess/dsl"
)

func FuzzCompile(f *testing.F) {
	seeds := []string{
		"",
		"(deffacts)",
		"(defrule)",
		"(deftemplate)",
		"(deftemplate item\n  (slot id (type STRING) (required TRUE)))",
		`(deftemplate order
  (slot lane (type STRING) (required TRUE))
  (slot amount (type FLOAT) (default 2.0)))

(deffacts seed
  (order (lane "expedite" ) (amount 12.5)))

(defrule route
  (declare (salience 5))
  ?o <- (order (lane ?lane) (amount ?amount))
  (not (hold (lane ?lane)))
  (test (> ?amount 10))
  =>
  (assert (routed (lane ?lane))))`,
		"(defquery q\n  (declare (variables ?id))\n  (item (id ?id))\n  (return (id ?id)))",
		"(defglobal ?*limit* 10)",
		"(defmodule triage)",
		"(deffunction twice (?x) (* ?x 2))",
		"(defrule r (or (a) (b)) (or (c) (d)) => (emit \"x\"))",
		"(deftemplate item (slott id (type STRING)))",
		"(defrule r (item (id ?b:field)) => (emit ?b:field))",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		_, _ = dsl.Compile(context.Background(), "fuzz.gess", []byte(source), dsl.Registry{})
	})
}
