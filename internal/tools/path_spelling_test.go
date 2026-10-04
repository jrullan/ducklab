package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func spellingProject(t *testing.T) (*Registry, *ExecContext, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"logic.mjs": "old\n", "tests/state.test.mjs": "test\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry()
	reg.Register(&FSRead{})
	reg.Register(&FSWrite{})
	reg.Register(&FSWriteLines{})
	reg.Register(&FSPatch{})
	reg.Register(&FSDelete{})
	ectx := &ExecContext{ProjectRoot: root}
	ectx.BeginTurn()
	return reg, ectx, root
}

// B-497: a file tool with no path says so, instead of acting on the project
// root and answering "is a directory" (seven repeats in TI-36X T-004).
func TestAFileToolWithNoPathSaysSo(t *testing.T) {
	reg, ectx, _ := spellingProject(t)
	for _, call := range []struct{ tool, args string }{
		{"fs_read", `{}`},
		{"fs_read", `{"start": 1200}`},
		{"fs_write_lines", `{"start": 1, "end": 2, "content": "x"}`},
		{"fs_patch", `{"edits": [{"search": "old", "replace": "new"}]}`},
		{"fs_delete", `{"path": "   "}`},
	} {
		res, _ := reg.Execute(context.Background(), ectx, call.tool, json.RawMessage(call.args))
		if !res.IsError || !strings.Contains(res.Content, `needs a "path"`) || strings.Contains(res.Content, "is a directory") {
			t.Errorf("%s %s: %+v", call.tool, call.args, res)
		}
	}
	// Past the research boundary too: the missing path is the actionable error.
	ectx.ExplorationCallLimit = 1
	ectx.BeginTurn()
	if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":"logic.mjs"}`)); res.IsError {
		t.Fatalf("first read refused: %s", res.Content)
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{}`)); !strings.Contains(res.Content, `needs a "path"`) {
		t.Errorf("the boundary hid the missing path: %+v", res)
	}
}

// B-497: padding around a path is the model's, not the tree's. A padded read
// reaches the file, and a padded write edits it rather than creating a new
// file whose name ends in a newline.
func TestAPaddedPathNamesTheProjectFile(t *testing.T) {
	reg, ectx, root := spellingProject(t)
	if res, _ := reg.Execute(context.Background(), ectx, "fs_read", json.RawMessage(`{"path":" logic.mjs"}`)); res.IsError || !strings.Contains(res.Content, "old") {
		t.Errorf("padded read: %+v", res)
	}
	if res, _ := reg.Execute(context.Background(), ectx, "fs_write", json.RawMessage(`{"path":"tests/state.test.mjs\n","content":"new test\n"}`)); res.IsError {
		t.Fatalf("padded write: %s", res.Content)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "tests", "state.test.mjs")); string(data) != "new test\n" {
		t.Errorf("the padded write did not reach the file: %q", data)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "tests"))
	if len(entries) != 1 {
		t.Errorf("a padded write created a second file: %v", entries)
	}
	if got := string(CanonicalToolArgs(ectx, "fs_read", json.RawMessage(`{"path":" logic.mjs\t"}`))); !strings.Contains(got, `"path":"logic.mjs"`) {
		t.Errorf("recorded args keep the padding: %s", got)
	}
}
