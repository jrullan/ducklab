package vcs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRepo(t *testing.T) (*Git, string) {
	t.Helper()
	dir := t.TempDir()
	g := New(dir)
	if err := g.Init(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	g.run("config", "user.email", "test@example.com")
	g.run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("init"); err != nil {
		t.Fatal(err)
	}
	return g, dir
}

func TestInitCommitAndHead(t *testing.T) {
	g, _ := newRepo(t)
	sha, err := g.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if len(sha) < 7 {
		t.Errorf("HeadSHA = %q, want a sha", sha)
	}
	clean, err := g.IsClean()
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("fresh repo reported dirty")
	}
}

func TestDiffSeesUncommittedChange(t *testing.T) {
	g, dir := newRepo(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644)

	diff, err := g.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "changed") {
		t.Errorf("diff does not show the change:\n%s", diff)
	}
	clean, _ := g.IsClean()
	if clean {
		t.Error("modified repo reported clean")
	}
}

func TestCreateBranchAndCurrentBranch(t *testing.T) {
	g, _ := newRepo(t)
	if err := g.CreateBranch("ducklab/T-001"); err != nil {
		t.Fatal(err)
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if branch != "ducklab/T-001" {
		t.Errorf("CurrentBranch = %q, want ducklab/T-001", branch)
	}
}

// Commit messages come from models and from users; a quote must not break the
// commit or, worse, let text reach the shell.
func TestCommitWithTrailerHandlesQuotesAndNewlines(t *testing.T) {
	g, dir := newRepo(t)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644)
	g.AddAll()

	msg := `fix: don't break on "quotes" or 'apostrophes'`
	sha, err := g.CommitWithTrailer(msg, map[string]string{
		"Ducklab-Run": "r-20260726-000000-abcd",
		"Duckling":    "implementer",
	})
	if err != nil {
		t.Fatalf("commit with quotes failed: %v", err)
	}
	if sha == "" {
		t.Fatal("empty sha")
	}
	out, err := g.run("log", "-1", "--format=%B")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"quotes"`) || !strings.Contains(out, "'apostrophes'") {
		t.Errorf("message mangled:\n%s", out)
	}
	if !strings.Contains(out, "Ducklab-Run: r-20260726-000000-abcd") {
		t.Errorf("trailer missing:\n%s", out)
	}
}

// Worktrees are how tournament and split isolate concurrent contestants; a
// leaked worktree corrupts the next run.
func TestWorktreeAddListRemove(t *testing.T) {
	g, dir := newRepo(t)
	wt := filepath.Join(filepath.Dir(dir), "wt-"+filepath.Base(dir))

	if err := g.WorktreeAdd(wt, "contestant-a"); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	t.Cleanup(func() { g.WorktreeRemove(wt) })

	list, err := g.WorktreeList()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range list {
		if strings.Contains(w, filepath.Base(wt)) {
			found = true
		}
	}
	if !found {
		t.Errorf("worktree not listed: %v", list)
	}

	if err := g.WorktreeRemove(wt); err != nil {
		t.Fatalf("worktree remove: %v", err)
	}
	list, _ = g.WorktreeList()
	for _, w := range list {
		if strings.Contains(w, filepath.Base(wt)) {
			t.Errorf("worktree still listed after removal: %v", list)
		}
	}
}

func TestEnsureGitignoreIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	entries := []string{".ducklab/runs/", ".ducklab/ducklab.db"}

	for i := 0; i < 3; i++ {
		if err := EnsureGitignore(dir, entries); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := strings.Count(string(data), e); n != 1 {
			t.Errorf("entry %q appears %d times, want 1:\n%s", e, n, data)
		}
	}
}

func TestEnsureGitignorePreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644)

	if err := EnsureGitignore(dir, []string{".ducklab/runs/"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(data), "node_modules/") {
		t.Errorf("existing entry lost:\n%s", data)
	}
	if !strings.Contains(string(data), ".ducklab/runs/") {
		t.Errorf("new entry missing:\n%s", data)
	}
}

func TestHasGitFalseOnPlainDirectory(t *testing.T) {
	if New(t.TempDir()).HasGit() {
		t.Error("HasGit true for a directory with no .git")
	}
}

func TestFileTreeRespectsMaxEntries(t *testing.T) {
	g, dir := newRepo(t)
	for i := 0; i < 20; i++ {
		os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644)
	}
	g.AddAll()
	g.Commit("more files")

	tree, err := g.FileTree(5)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(tree, "\n"); lines > 8 {
		t.Errorf("FileTree(5) produced %d lines; cap not applied:\n%s", lines, tree)
	}
}
