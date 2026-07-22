package prim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRepoGreenfield(t *testing.T) {
	dir := t.TempDir()
	// a lone file, no git
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>\n"), 0o644)

	init, err := EnsureRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !init {
		t.Fatal("expected greenfield to be initialized")
	}
	if !IsRepo(dir) {
		t.Error("dir is not a repo after EnsureRepo")
	}
	// base commit exists → CurrentBranch resolves and a HEAD is present
	if ok, _ := Git("rev-parse --verify -q HEAD", dir); !ok {
		t.Error("no initial commit created")
	}
	// runs/ is ignored
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), "runs/") {
		t.Errorf(".gitignore missing runs/: %q", gi)
	}
	// idempotent
	if init2, _ := EnsureRepo(dir); init2 {
		t.Error("second EnsureRepo should be a no-op")
	}
}

func TestIsDirtyIgnoresRuns(t *testing.T) {
	dir := t.TempDir()
	sh := func(c string) {
		if ok, out := Shell(c, dir); !ok {
			t.Fatalf("%s: %s", c, out)
		}
	}
	sh("git init -q && git config user.email t@t && git config user.name t")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644)
	sh("git add -A && git commit -qm base")

	// ducklab's own artifacts must not read as dirty user work
	os.MkdirAll(filepath.Join(dir, "runs", "task"), 0o755)
	os.WriteFile(filepath.Join(dir, "runs", "task", "state.json"), []byte("{}"), 0o644)
	if dirty, lines := IsDirty(dir); dirty {
		t.Errorf("runs/ artifacts tripped the dirty guard: %v", lines)
	}

	// real user changes still count
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644)
	if dirty, _ := IsDirty(dir); !dirty {
		t.Error("a real untracked file should read as dirty")
	}
}
