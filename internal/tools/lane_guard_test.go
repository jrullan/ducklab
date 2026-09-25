package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrullan/ducklab/internal/config"
)

func TestFilesystemMutationsRefusePathsOutsideTaskLane(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	var distress int
	ectx := &ExecContext{
		ProjectRoot: root, Role: config.RoleImplementer,
		TaskWritableFiles: []string{"README.md"},
		TaskWritableDirs:  []string{"src"},
		LaneEnforcement:   "write",
		OnDistress: func(reason string, _ map[string]interface{}) {
			if reason == "lane_violation" {
				distress++
			}
		},
	}

	tests := []struct {
		name string
		args map[string]interface{}
	}{
		{"fs_write", map[string]interface{}{"path": "outside.txt", "content": "new\n"}},
		{"fs_patch", map[string]interface{}{"path": "outside.txt", "edits": []map[string]string{{"search": "old", "replace": "new"}}}},
		{"fs_write_lines", map[string]interface{}{"path": "outside.txt", "start": 1, "end": 1, "first_line": "old", "content": "new"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args, err := json.Marshal(test.args)
			if err != nil {
				t.Fatal(err)
			}
			result, err := registry.Execute(context.Background(), ectx, test.name, args)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || !strings.Contains(result.Content, "outside this task's declared write lane") {
				t.Fatalf("result = %#v, want lane refusal", result)
			}
		})
	}
	if got, err := os.ReadFile(filepath.Join(root, "outside.txt")); err != nil || string(got) != "old\n" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
	if distress != len(tests) {
		t.Fatalf("lane distress calls = %d, want %d (service records the first)", distress, len(tests))
	}
}

func TestWriteLaneAllowsExactFilesAndTreeDescendants(t *testing.T) {
	ectx := &ExecContext{
		ProjectRoot: t.TempDir(), Role: config.RoleImplementer,
		TaskWritableFiles: []string{"README.md"}, TaskWritableDirs: []string{"src/core"},
	}
	for _, path := range []string{"README.md", "src/core/model.go", "src/core/nested/check.go"} {
		if result := WriteGuard(ectx, path, []byte("ok\n"), true); result != nil {
			t.Errorf("WriteGuard(%q) = %q", path, result.Content)
		}
	}
	if result := WriteGuard(ectx, "src/coreish/model.go", []byte("no\n"), true); result == nil {
		t.Fatal("path prefix escaped the tree lane boundary")
	}
}

func TestWriteLaneAcceptModeAndUnsafeWritesBypassEarlyBrake(t *testing.T) {
	for _, test := range []ExecContext{
		{LaneEnforcement: "accept"},
		{LaneEnforcement: "write", UnsafeWrites: true},
	} {
		test.ProjectRoot = t.TempDir()
		test.Role = config.RoleImplementer
		test.TaskWritableFiles = []string{"inside.txt"}
		if result := WriteGuard(&test, "outside.txt", []byte("ok\n"), true); result != nil {
			t.Fatalf("bypass refused: %q", result.Content)
		}
	}
}
