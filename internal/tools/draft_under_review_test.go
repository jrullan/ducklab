package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The draft a council is judging exists only in the conversation; a seat
// that asks artifact_read for it is served the draft, not told it does not
// exist (nineteen refusals in a row on a spec review, benchmark run 4).
func TestArtifactReadServesTheDraftUnderReview(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	ectx := &ExecContext{ProjectRoot: dir, DraftUnderReview: map[string]string{
		"spec": "## SPEC-001 — Shell\n\n**Implements:** REQ-001\n\nGTK4.\n\n## SPEC-004 — Clipboard\n\n**Implements:** REQ-004\n\nwl_data_device.\n",
	}}
	whole, _ := (&ArtifactRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"kind":"spec"}`))
	if whole.IsError || !strings.Contains(whole.Content, "DRAFT UNDER REVIEW") || !strings.Contains(whole.Content, "SPEC-004") {
		t.Fatalf("the draft was not served: err=%v %.200s", whole.IsError, whole.Content)
	}
	one, _ := (&ArtifactRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"kind":"spec","id":"SPEC-004"}`))
	if one.IsError || !strings.Contains(one.Content, "wl_data_device") {
		t.Fatalf("the draft section was not served: err=%v %.200s", one.IsError, one.Content)
	}
	missing, _ := (&ArtifactRead{}).Execute(context.Background(), ectx, json.RawMessage(`{"kind":"spec","id":"SPEC-009"}`))
	if !missing.IsError || !strings.Contains(missing.Content, "has no section") {
		t.Fatalf("a missing draft section was not refused with the list: %.200s", missing.Content)
	}
}
