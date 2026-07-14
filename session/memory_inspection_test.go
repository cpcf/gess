package session_test

import (
	"context"
	"testing"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/session"
)

func TestMemoryInspectionSummariesAgreeAndDetailIsBounded(t *testing.T) {
	ctx := context.Background()
	workspace := session.NewWorkspace()
	if err := workspace.AddTemplate(rules.TemplateSpec{Name: "item", Key: "item", Fields: []rules.FieldSpec{{Name: "id", Kind: rules.ValueInt, Required: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddAction(rules.ActionSpec{Name: "noop", Fn: func(rules.ActionContext) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.AddRule(rules.RuleSpec{ID: "rule:item", Name: "item", Conditions: []rules.RuleConditionSpec{{Binding: "item", Target: rules.TemplateKeyFact("item")}}, Actions: []rules.RuleActionSpec{{Name: "noop"}}}); err != nil {
		t.Fatal(err)
	}
	revision, err := workspace.Compile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := session.New(revision)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3; i++ {
		if _, err := sess.Assert(ctx, "item", rules.MustFields("id", i)); err != nil {
			t.Fatal(err)
		}
	}

	report, err := sess.MemoryInspection(ctx, session.MemoryInspectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != session.MemoryInspectionSchemaVersion || !report.Availability || len(report.Nodes) == 0 {
		t.Fatalf("report = %#v", report)
	}
	diagnostics, err := sess.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Owners) != len(diagnostics.Memory) {
		t.Fatalf("owner count = %d, diagnostics = %d", len(report.Owners), len(diagnostics.Memory))
	}
	for i := range report.Owners {
		if report.Owners[i].Owner != diagnostics.Memory[i].Owner || report.Owners[i].Rows != diagnostics.Memory[i].Rows || report.Owners[i].Bytes != diagnostics.Memory[i].Bytes {
			t.Fatalf("owner %d disagrees: %#v vs %#v", i, report.Owners[i], diagnostics.Memory[i])
		}
	}

	var alphaID string
	for _, node := range report.Nodes {
		if node.Kind == "alpha" {
			alphaID = node.NodeID
			if node.Rows != 3 || !node.DetailSupported {
				t.Fatalf("alpha = %#v", node)
			}
			break
		}
	}
	first, err := sess.MemoryInspection(ctx, session.MemoryInspectionRequest{NodeID: alphaID, Limit: 2, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Detail.Availability || first.Detail.Returned != 2 || first.Detail.Total != 3 || !first.Detail.Truncated || first.Detail.NextCursor == nil {
		t.Fatalf("first detail = %#v", first.Detail)
	}
	second, err := sess.MemoryInspection(ctx, session.MemoryInspectionRequest{NodeID: alphaID, Cursor: *first.Detail.NextCursor, Limit: 2, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if second.Detail.Returned != 1 || second.Detail.Truncated {
		t.Fatalf("second detail = %#v", second.Detail)
	}

	unsupported, err := sess.MemoryInspection(ctx, session.MemoryInspectionRequest{NodeID: "rete:terminal:1"})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.Detail.Availability || unsupported.Detail.Reason == "" {
		t.Fatalf("unsupported detail = %#v", unsupported.Detail)
	}
	tiny, err := sess.MemoryInspection(ctx, session.MemoryInspectionRequest{NodeID: alphaID, Limit: 100, MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tiny.Detail.MaxBytes != 64<<10 {
		t.Fatalf("invalid max bytes was not clamped: %d", tiny.Detail.MaxBytes)
	}
}
