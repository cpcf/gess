package dsl_test

import (
	"reflect"
	"testing"

	"github.com/cpcf/gess/dsl"
)

func TestDocumentSymbolsExposeTopLevelDeclarations(t *testing.T) {
	source := []byte(`(defmodule OTHER)
(defglobal *threshold* (type int) (default 10))
(deftemplate MAIN::order (slot id (type int)))
(deffunction ready (?value int) (returns bool) (> ?value 0))
(deffacts seed (MAIN.order (id 1)))
(defrule OTHER::route (MAIN::order (id ?id)) => (emit ?id))
(defquery MAIN::orders (MAIN::order (id ?id)) (return (id ?id)))
`)
	document, err := dsl.Parse("rules/symbols.gess", source)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	got := document.Symbols()
	want := []dsl.Symbol{
		{Kind: dsl.SymbolModule, Name: "OTHER", Module: "OTHER"},
		{Kind: dsl.SymbolGlobal, Name: "threshold"},
		{Kind: dsl.SymbolTemplate, Name: "order", Module: "MAIN"},
		{Kind: dsl.SymbolFunction, Name: "ready"},
		{Kind: dsl.SymbolDeffacts, Name: "seed"},
		{Kind: dsl.SymbolRule, Name: "route", Module: "OTHER"},
		{Kind: dsl.SymbolQuery, Name: "orders", Module: "MAIN"},
	}
	if len(got) != len(want) {
		t.Fatalf("Symbols() = %#v, want %d symbols", got, len(want))
	}
	for index := range want {
		if got[index].Kind != want[index].Kind || got[index].Name != want[index].Name || got[index].Module != want[index].Module {
			t.Fatalf("Symbols()[%d] = %#v, want %#v", index, got[index], want[index])
		}
		if !reflect.DeepEqual(got[index].Source.Name, "rules/symbols.gess") || got[index].Source.StartLine != index+1 || got[index].Source.StartColumn != 1 {
			t.Fatalf("Symbols()[%d].Source = %#v", index, got[index].Source)
		}
	}

	got[0].Name = "mutated"
	if document.Symbols()[0].Name != "OTHER" {
		t.Fatal("Symbols() returned shared mutable storage")
	}
}
