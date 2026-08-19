package vcs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Worktree operations modify shared repository metadata. They must therefore
// serialize even when independent Git values name the same repository.
func TestConcurrentWorktreeOperationsShareRepositoryLock(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("fake git command is POSIX shell script")
	}

	repo := t.TempDir()
	bin := t.TempDir()
	lock := filepath.Join(t.TempDir(), "worktree-operation")
	script := "#!/bin/sh\n" +
		"if ! mkdir \"$DUCKLAB_WORKTREE_TEST_LOCK\" 2>/dev/null; then\n" +
		"  echo concurrent worktree metadata operation >&2\n" +
		"  exit 128\n" +
		"fi\n" +
		"sleep 0.1\n" +
		"rmdir \"$DUCKLAB_WORKTREE_TEST_LOCK\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DUCKLAB_WORKTREE_TEST_LOCK", lock)

	const workers = 9
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			g := New(repo) // callers do not share a Git receiver.
			path := filepath.Join(repo, fmt.Sprintf("worktree-%d", i))
			switch i % 3 {
			case 0:
				errs[i] = g.WorktreeAdd(path, fmt.Sprintf("branch-%d", i))
			case 1:
				errs[i] = g.WorktreeAddDetached(path, "HEAD")
			default:
				errs[i] = g.WorktreeRemove(path)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("worktree operation %d ran concurrently: %v", i, err)
		}
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

// I8: a winning candidate is applied byte-for-byte, never regenerated.
func TestApplyPatchIsByteIdentical(t *testing.T) {
	g, dir := newRepo(t)
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package x\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"), 0o644)
	g.AddAll()
	g.Commit("seed")

	// Produce a patch the way a contestant's worktree would.
	os.WriteFile(filepath.Join(dir, "add.go"), []byte("package x\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"), 0o644)
	patch, err := g.DiffAgainst("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(dir, "add.go"))

	// Reset and re-apply the captured patch.
	if _, err := g.run("checkout", "--", "."); err != nil {
		t.Fatal(err)
	}
	if err := g.ApplyPatch(patch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "add.go"))
	if string(got) != string(want) {
		t.Errorf("applied content differs from the candidate:\ngot:  %q\nwant: %q", got, want)
	}
}

// A contestant that creates a new file must have it captured in its patch.
func TestDiffAgainstIncludesUntrackedFiles(t *testing.T) {
	g, dir := newRepo(t)
	os.WriteFile(filepath.Join(dir, "brand_new.go"), []byte("package x\n"), 0o644)

	patch, err := g.DiffAgainst("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "brand_new.go") {
		t.Errorf("new file missing from the patch; a contestant's work would be silently lost:\n%s", patch)
	}
}

func TestApplyPatchRejectsEmpty(t *testing.T) {
	g, _ := newRepo(t)
	if err := g.ApplyPatch("   "); err == nil {
		t.Error("empty patch accepted")
	}
}

func TestPruneWorktreesClearsStaleRecords(t *testing.T) {
	g, dir := newRepo(t)
	wt := filepath.Join(filepath.Dir(dir), "stale-"+filepath.Base(dir))
	if err := g.WorktreeAdd(wt, "stale-branch"); err != nil {
		t.Skipf("worktree add: %v", err)
	}
	// Simulate a killed engine: the directory vanishes, the record remains.
	os.RemoveAll(wt)
	if err := g.PruneWorktrees(); err != nil {
		t.Fatal(err)
	}
	list, _ := g.WorktreeList()
	for _, w := range list {
		if strings.Contains(w, filepath.Base(wt)) {
			t.Errorf("stale worktree record survived prune: %v", list)
		}
	}
	// The path must be reusable afterwards, which is the point of pruning.
	if err := g.WorktreeAdd(wt, "stale-branch-2"); err != nil {
		t.Errorf("worktree path not reusable after prune: %v", err)
	}
	g.WorktreeRemove(wt)
}

// A repo with no commits has no HEAD, so every run that asks for a diff dies
// on "fatal: ambiguous argument 'HEAD'" — after the ducklings have already
// done the work. Measured: a real pair run patched add.go, verified it, and
// then failed at the reviewer's diff.
func TestInitLeavesARepoThatCanBeDiffed(t *testing.T) {
	dir := t.TempDir()
	g := New(dir)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.HeadSHA(); err != nil {
		t.Fatalf("no HEAD after Init: %v", err)
	}
	if _, err := g.Diff(); err != nil {
		t.Errorf("a freshly initialised repo cannot be diffed: %v", err)
	}
}

// run joins its arguments into one shell command line, so an unescaped
// multi-word message reached git as several pathspecs. "ducklab: release
// v0.1.0" failed with `pathspec 'release' did not match any file(s)`, and the
// tests that used Commit had been discarding its error.
func TestCommitKeepsAMultiWordMessage(t *testing.T) {
	dir := t.TempDir()
	g := New(dir)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	want := "ducklab: release v0.1.0"
	if _, err := g.Commit(want); err != nil {
		t.Fatalf("a multi-word commit message failed: %v", err)
	}
	out, err := g.run("log", "-1", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != want {
		t.Errorf("message = %q, want %q", strings.TrimSpace(out), want)
	}
}

// `git diff HEAD` does not show files git has never seen, so a run that
// created files rather than editing them recorded an empty diff. A split run
// integrated two new files, passed its gate, and left diff.patch at zero
// bytes; the desktop showed "No changes yet." on work that had changed
// everything.
//
// DiffAgainst already knew this — its comment says a contestant creating a new
// file must have it captured — and Diff did not.
func TestDiffIncludesFilesGitHasNeverSeen(t *testing.T) {
	dir := t.TempDir()
	g := New(dir)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brand-new.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := g.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "brand-new.go") {
		t.Errorf("a newly created file is missing from the diff:\n%q", diff)
	}
}

// The harness's own state never rides a task's diff: flipping the project's
// autonomy in settings dirtied the tracked project.toml, and T-097's
// reviewer flagged the "loosened safety constraints" as a critical finding
// on a task about dashboard tests.
func TestDiffExcludesTheHarnessDirectory(t *testing.T) {
	dir := t.TempDir()
	g := New(dir)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ducklab"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".ducklab/project.toml", "autonomy = \"guarded\"\n")
	write("app.py", "print(1)\n")
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("base"); err != nil {
		t.Fatal(err)
	}

	write(".ducklab/project.toml", "autonomy = \"yolo\"\n")
	write("app.py", "print(2)\n")

	diff, err := g.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "app.py") {
		t.Error("the task's own change is missing from the diff")
	}
	if strings.Contains(diff, "project.toml") {
		t.Error("harness state rode the task diff")
	}
}
