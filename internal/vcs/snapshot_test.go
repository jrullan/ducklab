package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) *Git {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	g := New(root)
	write(t, root, "index.html", "original content\n")
	write(t, root, ".gitignore", "junk/\n")
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("init"); err != nil {
		t.Fatal(err)
	}
	return g
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return "<gone: " + err.Error() + ">"
	}
	return string(b)
}

// Runs edit the shared working tree live and commit only on accept, so a failed
// or rejected run left its half-made edits sitting there. The next attempt of
// the same task found them and concluded somebody had already fixed it —
// measured on a real task, whose retry said exactly that in its thinking.
func TestARestoredTreeIsThePreRunTree(t *testing.T) {
	g := repo(t)

	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}

	// The run: edits a file, creates another, deletes nothing.
	write(t, g.Root, "index.html", "half-made fix the run never finished\n")
	write(t, g.Root, "helper.js", "created by the failed run\n")

	if err := g.RestoreTree(snap); err != nil {
		t.Fatal(err)
	}
	if got := read(t, g.Root, "index.html"); got != "original content\n" {
		t.Errorf("the edit survived the restore: %q", got)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "helper.js")); !os.IsNotExist(err) {
		t.Error("the file the run created survived the restore")
	}
}

// A person's uncommitted work from BEFORE the run is part of the snapshot, so
// restoring a failed run must give it back, not reset to HEAD.
func TestRestoreKeepsPreRunUncommittedWork(t *testing.T) {
	g := repo(t)
	write(t, g.Root, "notes.md", "uncommitted but mine\n")
	write(t, g.Root, "index.html", "my own uncommitted edit\n")

	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	write(t, g.Root, "index.html", "the run scribbled over it\n")

	if err := g.RestoreTree(snap); err != nil {
		t.Fatal(err)
	}
	if got := read(t, g.Root, "index.html"); got != "my own uncommitted edit\n" {
		t.Errorf("restore reset to HEAD instead of to the pre-run tree: %q", got)
	}
	if got := read(t, g.Root, "notes.md"); got != "uncommitted but mine\n" {
		t.Errorf("untracked pre-run work was lost: %q", got)
	}
}

// The run log is appended to while the run executes. A restore that rewound
// .ducklab would destroy the evidence of the very failure being cleaned up.
func TestRestoreLeavesTheHarnessRecordAlone(t *testing.T) {
	g := repo(t)
	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	write(t, g.Root, ".ducklab/runs/r-1/events.jsonl", "the failure's own record\n")
	write(t, g.Root, "junk/cache.bin", "ignored, not ours to delete\n")

	if err := g.RestoreTree(snap); err != nil {
		t.Fatal(err)
	}
	if got := read(t, g.Root, ".ducklab/runs/r-1/events.jsonl"); !strings.Contains(got, "record") {
		t.Errorf("the run log was destroyed by the restore: %q", got)
	}
	if got := read(t, g.Root, "junk/cache.bin"); !strings.Contains(got, "ignored") {
		t.Errorf("a gitignored file was touched: %q", got)
	}
}

// The exact residue a failed run left in a real project: the run created a
// file, Diff() marked it intent-to-add in the REAL index, and the restore
// deleted the file but not the index entry — `git status` showed " D" forever
// on a file that was never committed and no longer existed, and every
// clean-tree guard refused on a tree that was actually clean.
func TestRestoreClearsIntentToAddGhosts(t *testing.T) {
	g := repo(t)
	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}

	// The run creates a file and a diff is taken (add -AN on the real index).
	write(t, g.Root, "migrations/004_seed.sql", "INSERT ...\n")
	if _, err := g.Diff(); err != nil {
		t.Fatal(err)
	}

	if err := g.RestoreTree(snap); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "migrations/004_seed.sql")); !os.IsNotExist(err) {
		t.Fatal("the created file survived the restore")
	}
	clean, err := g.IsClean()
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		out, _ := g.run("status", "--porcelain")
		t.Errorf("the restore left ghost index entries:\n%s", out)
	}
}
