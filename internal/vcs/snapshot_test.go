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
// Reversed by B-074: a landed commit used to make the restore refuse
// outright; now it restores AROUND the commit — the committed file keeps
// its content and the restore succeeds, because the run left nothing else.
func TestRestoreAtHeadRestoresAroundLandedCommits(t *testing.T) {
	g := repo(t)
	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	head, err := g.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	write(t, g.Root, "landed.go", "package project\n")
	if err := g.Add("landed.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("land work"); err != nil {
		t.Fatal(err)
	}

	if err := g.RestoreTreeAtHead(snap, head); err != nil {
		t.Fatalf("RestoreTreeAtHead = %v, want a surgical restore around the commit", err)
	}
	if got := read(t, g.Root, "landed.go"); got != "package project\n" {
		t.Errorf("restore changed committed file: %q", got)
	}
}

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

// repoWith is repo() plus named starting files, handing back the root too.
func repoWith(t *testing.T, files map[string]string) (*Git, string) {
	t.Helper()
	g := repo(t)
	for name, body := range files {
		write(t, g.Root, name, body)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("seed"); err != nil {
		t.Fatal(err)
	}
	return g, g.Root
}

func mustHeadSHA(t *testing.T, g *Git) string {
	t.Helper()
	head, err := g.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// B-074: three unrelated commits landed while a run waited at its gate, and
// reject died on "restoring would rewind them". The restore is surgical now:
// the run's own edits are undone, files it created are removed, and the
// landed commits' files keep their committed content.
func TestRestoreAroundLandedCommits(t *testing.T) {
	g, dir := repoWith(t, map[string]string{
		"run-edits.txt": "original\n",
		"landed.txt":    "before\n",
	})
	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	head := mustHeadSHA(t, g)
	// The run's work: edit one file, create another.
	os.WriteFile(filepath.Join(dir, "run-edits.txt"), []byte("the run's change\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "run-created.txt"), []byte("new\n"), 0o644)
	// A foreign commit lands.
	os.WriteFile(filepath.Join(dir, "landed.txt"), []byte("committed after\n"), 0o644)
	if err := g.Add("landed.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("landed"); err != nil {
		t.Fatal(err)
	}

	if err := g.RestoreTreeAtHead(snap, head); err != nil {
		t.Fatalf("surgical restore refused: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "run-edits.txt")); string(got) != "original\n" {
		t.Errorf("run edit not undone: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "run-created.txt")); !os.IsNotExist(err) {
		t.Errorf("run-created file survived")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "landed.txt")); string(got) != "committed after\n" {
		t.Errorf("landed commit rewound: %q", got)
	}
}

// A run whose work was committed by hand leaves nothing of its own: the
// restore succeeds restoring nothing, instead of refusing forever.
func TestRestoreAroundCommitsWithNothingLeftIsANoOp(t *testing.T) {
	g, dir := repoWith(t, map[string]string{"a.txt": "one\n"})
	snap, _ := g.SnapshotTree()
	head := mustHeadSHA(t, g)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644)
	if err := g.Add("a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("landed the run's own work"); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreTreeAtHead(snap, head); err != nil {
		t.Fatalf("no-op restore refused: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "two\n" {
		t.Errorf("committed content rewound: %q", got)
	}
}

// A path both the run and a landed commit touched cannot be settled by a
// machine picking a side; the refusal names it.
func TestRestoreAroundCommitsRefusesAGenuineOverlap(t *testing.T) {
	g, dir := repoWith(t, map[string]string{"shared.txt": "base\n"})
	snap, _ := g.SnapshotTree()
	head := mustHeadSHA(t, g)
	os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("committed\n"), 0o644)
	if err := g.Add("shared.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("landed"); err != nil {
		t.Fatal(err)
	}
	// The run's residue on top of the committed content.
	os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("run residue\n"), 0o644)
	err := g.RestoreTreeAtHead(snap, head)
	if err == nil || !strings.Contains(err.Error(), "shared.txt") || !strings.Contains(err.Error(), "resolve by hand") {
		t.Fatalf("overlap not refused by name: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "shared.txt")); string(got) != "run residue\n" {
		t.Errorf("refusal was not read-only: %q", got)
	}
}
