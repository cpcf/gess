package dsl_test

import (
	"context"
	"testing"

	"github.com/cpcf/gess/dsl"
	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestDocumentDeffactsPreservesNamedGroupsAndOwnership(t *testing.T) {
	document, err := dsl.Parse("groups.gess", []byte(`
(deftemplate item
  (slot id (type STRING) (required TRUE)))
(deffacts first
  (item (id "A")))
(deffacts second
  (item (id "B"))
  (item (id "C")))
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	workspace := session.NewWorkspace()
	if err := dsl.Load(context.Background(), workspace, document, dsl.Registry{}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	groups := document.Deffacts()
	if len(groups) != 2 || groups[0].Name != "first" || len(groups[0].Facts) != 1 || groups[1].Name != "second" || len(groups[1].Facts) != 2 {
		t.Fatalf("Deffacts = %#v", groups)
	}
	groups[0].Name = "mutated"
	groups[0].Facts[0].Fields["id"] = rules.StringValue("mutated")
	again := document.Deffacts()
	value, ok := again[0].Facts[0].Fields["id"].AsString()
	if again[0].Name != "first" || !ok || value != "A" {
		t.Fatalf("Deffacts returned shared storage: %#v", again)
	}
}
