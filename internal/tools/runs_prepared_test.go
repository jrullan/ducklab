package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B-521: a revision whose parent is not yet claimed is recorded "prepared"
// and may never run (recovery discards it). It is not a run to list.
func TestRunListHidesAPreparedRevision(t *testing.T) {
	root := t.TempDir()
	for id, status := range map[string]string{"r-real": "done", "r-prepared": "prepared"} {
		dir := filepath.Join(root, ".ducklab", "runs", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		state := `{"id":"` + id + `","stage":"intake","status":"` + status + `","started_at":"2026-10-11T00:00:00Z"}`
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := (&RunListTool{}).Execute(context.Background(), &ExecContext{ProjectRoot: root}, json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("run_list: %v %+v", err, res)
	}
	if !strings.Contains(res.Content, "r-real") || strings.Contains(res.Content, "r-prepared") {
		t.Fatalf("run_list = %q, want the real run only", res.Content)
	}
}
