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

// Project settings decide who may run unattended and what work is trusted. They
// are changed through PATCH /v1/projects, not by a run's filesystem tools.
// Shell writes to .ducklab/ are governed separately by ShellPolicy; this test
// covers the shared filesystem write guard used by fs_write, fs_patch, and
// fs_write_lines.
func TestImplementerCannotWriteProjectGovernance(t *testing.T) {
	root := t.TempDir()
	const original = "autonomy = \"guarded\"\n"
	path := filepath.Join(root, ".ducklab", "project.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	var distress []map[string]interface{}
	ectx := &ExecContext{
		ProjectRoot: root,
		Role:        config.RoleImplementer,
		OnDistress: func(reason string, data map[string]interface{}) {
			if reason == "governance_write_refused" {
				distress = append(distress, data)
			}
		},
	}

	attempts := []struct {
		name string
		tool Tool
		args string
	}{
		{
			name: "fs_write",
			tool: &FSWrite{},
			args: `{"path":".ducklab/project.toml","content":"autonomy = \"yolo\"\n"}`,
		},
		{
			name: "fs_patch",
			tool: &FSPatch{},
			args: `{"path":".ducklab/project.toml","edits":[{"search":"autonomy = \"guarded\"","replace":"autonomy = \"yolo\""}]}`,
		},
		{
			// Deleting the file is the same change wearing a different tool.
			name: "fs_delete",
			tool: &FSDelete{},
			args: `{"path":".ducklab/project.toml"}`,
		},
	}
	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := attempt.tool.Execute(context.Background(), ectx, json.RawMessage(attempt.args))
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || !strings.Contains(res.Content, "PATCH /v1/projects") {
				t.Fatalf("%s = %#v; want refusal naming PATCH /v1/projects", attempt.name, res)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != original {
				t.Errorf("project.toml changed despite %s refusal: %q", attempt.name, got)
			}
		})
	}
	if len(distress) != len(attempts) {
		t.Fatalf("OnDistress recorded %d governance refusals, want %d", len(distress), len(attempts))
	}
}
