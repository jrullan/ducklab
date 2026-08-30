package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A sub-numbered id is never a section; the tool says so and names the
// parent instead of letting the seat search the tree for it.
func TestArtifactReadOfASubNumberedIDNamesTheParent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ducklab", "docs", "requirements.md"),
		[]byte("## REQ-003 — Functional\n\n### REQ-003.1 — Full screen\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (&ArtifactRead{}).Execute(context.Background(), &ExecContext{ProjectRoot: dir},
		json.RawMessage(`{"kind":"requirements","id":"REQ-003.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "sub-numbered") || !strings.Contains(res.Content, `"REQ-003"`) {
		t.Fatalf("the sub-numbered id was not explained: err=%v %.250s", res.IsError, res.Content)
	}
}
