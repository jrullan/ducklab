package artifact

import (
	"strings"
	"testing"
)

// A model that was asked for dependencies wrote "**Dependencies:**"; the
// parser knew only "Depends on" and dropped fifteen edges without a word.
func TestDependenciesIsReadAsDependsOn(t *testing.T) {
	doc, err := Parse("## M-001 — Core\n\n### T-001 — Scaffold\n\n**Dependencies:** none\n\nBody.\n\n### T-002 — Shell\n\n**Implements:** SPEC-001\n**Dependencies:** T-001\n\nBody.\n", KindPlan)
	if err != nil {
		t.Fatal(err)
	}
	t2 := doc.Section("T-002")
	if t2 == nil {
		t.Fatal("T-002 missing")
	}
	if got := strings.Join(splitIDs(t2.Field("depends on")), ","); got != "T-001" {
		t.Fatalf("T-002 depends on = %q, want T-001 (from a Dependencies: line)", got)
	}
	if t1 := doc.Section("T-001"); t1 == nil || len(splitIDs(t1.Field("depends on"))) != 0 {
		t.Fatalf("T-001 'none' produced edges: %+v", t1)
	}
}
