package stage

import (
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
)

func TestDedupeDropsRepeatedIDsAndRepeatedText(t *testing.T) {
	doc, err := artifact.Parse("## REQ-009 — Interaction\n\nMouse.\n\n## REQ-010 — Save\n\nSave to disk.\n\n## REQ-011 — Save\n\nSave to disk.\n\n## REQ-010 — Other\n\nDifferent.\n", artifact.KindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	dropped := dedupeSections(doc)
	if len(dropped) != 2 {
		t.Fatalf("dropped = %v, want the repeated text and the repeated id", dropped)
	}
	if ids := doc.IDs(); len(ids) != 2 || ids[0] != "REQ-009" || ids[1] != "REQ-010" {
		t.Fatalf("kept ids = %v", ids)
	}
}
