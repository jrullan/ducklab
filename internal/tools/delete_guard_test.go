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

// fs_delete checked governance alone.
// A delete now meets the same rules as a write.
func TestADeleteMeetsEveryPathRule(t *testing.T) {
	root := t.TempDir()
	g := filepath.Join(root, ".git")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"logic.mjs", "secret.env", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry()
	reg.Register(&FSDelete{})
	cases := []struct {
		name string
		ectx *ExecContext
		args string
		want string
	}{
		{".git", &ExecContext{ProjectRoot: root}, `{"path":".git","recursive":true}`, "denylist"},
		{"protected glob", &ExecContext{ProjectRoot: root, ShellPolicy: config.ShellPolicy{Deny: []string{"*.env"}}}, `{"path":"secret.env"}`, "protected path"},
		{"out of lane", &ExecContext{ProjectRoot: root, Role: config.RoleImplementer, TaskWritableFiles: []string{"logic.mjs"}}, `{"path":"README.md"}`, "lane:"},
		{"test-only run", &ExecContext{ProjectRoot: root, TestPathsOnly: true}, `{"path":"logic.mjs"}`, "writes tests only"},
	}
	for _, c := range cases {
		c.ectx.BeginTurn()
		res, _ := reg.Execute(context.Background(), c.ectx, "fs_delete", json.RawMessage(c.args))
		if !res.IsError || !strings.Contains(res.Content, c.want) {
			t.Errorf("%s: delete not refused with %q: %+v", c.name, c.want, res)
		}
	}
	if _, err := os.Stat(g); err != nil {
		t.Fatalf(".git was deleted: %v", err)
	}
	inLane := &ExecContext{ProjectRoot: root, Role: config.RoleImplementer, TaskWritableFiles: []string{"logic.mjs"}}
	inLane.BeginTurn()
	if res, _ := reg.Execute(context.Background(), inLane, "fs_delete", json.RawMessage(`{"path":"logic.mjs"}`)); res.IsError {
		t.Errorf("an in-lane delete was refused: %s", res.Content)
	}
}
