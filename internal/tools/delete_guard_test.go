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

// Review of #149: RemoveAll takes the whole subtree, so a recursive delete
// must check what it removes, not only the directory's own name.
func TestARecursiveDeleteChecksEveryPathItRemoves(t *testing.T) {
	setup := func(t *testing.T) string {
		root := t.TempDir()
		for _, name := range []string{".git/HEAD", "logic.mjs", "generated/keep.env", "generated/out.js", "build/a.txt", "build/b.txt"} {
			p := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	reg := NewRegistry()
	reg.Register(&FSDelete{})
	cases := []struct {
		name string
		ectx func(root string) *ExecContext
		path string
		want string
		kept string
	}{
		{"the project root", func(root string) *ExecContext { return &ExecContext{ProjectRoot: root} }, ".", "it contains .git", ".git/HEAD"},
		{"a parent of a protected file", func(root string) *ExecContext {
			return &ExecContext{ProjectRoot: root, ShellPolicy: config.ShellPolicy{Deny: []string{"generated/keep.env"}}}
		}, "generated", "it contains generated/keep.env", "generated/keep.env"},
		{"a directory holding an out-of-lane file", func(root string) *ExecContext {
			return &ExecContext{ProjectRoot: root, Role: config.RoleImplementer, TaskWritableFiles: []string{"build/a.txt"}}
		}, "build", "lane", "build/b.txt"},
	}
	for _, c := range cases {
		root := setup(t)
		ectx := c.ectx(root)
		ectx.BeginTurn()
		res, _ := reg.Execute(context.Background(), ectx, "fs_delete", json.RawMessage(`{"path":"`+c.path+`","recursive":true}`))
		if !res.IsError || !strings.Contains(res.Content, c.want) {
			t.Errorf("%s: delete not refused with %q: %+v", c.name, c.want, res)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.kept))); err != nil {
			t.Errorf("%s: %s was removed: %v", c.name, c.kept, err)
		}
	}
	root := setup(t)
	inLane := &ExecContext{ProjectRoot: root, Role: config.RoleImplementer, TaskWritableDirs: []string{"build"}}
	inLane.BeginTurn()
	if res, _ := reg.Execute(context.Background(), inLane, "fs_delete", json.RawMessage(`{"path":"build","recursive":true}`)); res.IsError {
		t.Errorf("a directory wholly in lane could not be deleted: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "build")); !os.IsNotExist(err) {
		t.Errorf("the in-lane directory survived: %v", err)
	}
}
