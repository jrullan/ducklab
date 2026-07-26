// Package vcs handles all git operations. Models never call these directly;
// only the orchestrator does.
package vcs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jrullan/ducklab/internal/xplat"
)

// Git wraps git operations for a repository.
type Git struct {
	Root string
}

// New creates a new Git for the given root.
func New(root string) *Git {
	return &Git{Root: root}
}

func (g *Git) run(args ...string) (string, error) {
	cmdLine := "git " + strings.Join(args, " ")
	cmd := xplat.Shell(g.Root, nil, cmdLine)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// HasGit returns whether the root is inside a git repository.
func (g *Git) HasGit() bool {
	_, err := g.run("rev-parse", "--git-dir")
	return err == nil
}

// Init initializes a git repository.
func (g *Git) Init() error {
	_, err := g.run("init")
	return err
}

// CurrentBranch returns the current branch name.
func (g *Git) CurrentBranch() (string, error) {
	out, err := g.run("rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// HeadSHA returns the HEAD commit SHA.
func (g *Git) HeadSHA() (string, error) {
	out, err := g.run("rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// IsClean returns whether the working tree is clean.
func (g *Git) IsClean() (bool, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// CreateBranch creates and checks out a new branch from the current HEAD.
func (g *Git) CreateBranch(name string) error {
	_, err := g.run("checkout", "-b", name)
	return err
}

// Checkout checks out a branch.
func (g *Git) Checkout(name string) error {
	_, err := g.run("checkout", name)
	return err
}

// Add stages files.
func (g *Git) Add(paths ...string) error {
	args := append([]string{"add"}, paths...)
	_, err := g.run(args...)
	return err
}

// AddAll stages all changes.
func (g *Git) AddAll() error {
	_, err := g.run("add", "-A")
	return err
}

// Commit creates a commit.
func (g *Git) Commit(message string) (string, error) {
	_, err := g.run("commit", "-m", message)
	if err != nil {
		return "", err
	}
	return g.HeadSHA()
}

// CommitWithTrailer creates a commit with trailers.
func (g *Git) CommitWithTrailer(message string, trailers map[string]string) (string, error) {
	args := []string{"commit", "-m", shellEscape(message)}
	for k, v := range trailers {
		args = append(args, "-m", shellEscape(fmt.Sprintf("%s: %s", k, v)))
	}
	_, err := g.run(args...)
	if err != nil {
		return "", err
	}
	return g.HeadSHA()
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// Diff returns the working tree diff.
func (g *Git) Diff() (string, error) {
	return g.run("diff", "HEAD")
}

// DiffStat returns the diff stat.
func (g *Git) DiffStat() (string, error) {
	return g.run("diff", "--stat", "HEAD")
}

// WorktreeAdd creates a worktree.
func (g *Git) WorktreeAdd(path, branch string) error {
	_, err := g.run("worktree", "add", path, "-b", branch)
	return err
}

// WorktreeRemove removes a worktree.
func (g *Git) WorktreeRemove(path string) error {
	_, err := g.run("worktree", "remove", "--force", path)
	return err
}

// WorktreeList lists worktrees.
func (g *Git) WorktreeList() ([]string, error) {
	out, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			result = append(result, strings.TrimPrefix(line, "worktree "))
		}
	}
	return result, nil
}

// FileTree returns a depth-limited tree of tracked files.
func (g *Git) FileTree(maxEntries int) (string, error) {
	out, err := g.run("ls-files")
	if err != nil {
		return "", err
	}
	files := strings.Split(strings.TrimSpace(out), "\n")
	if len(files) > maxEntries {
		return fmt.Sprintf("%d files (truncated)", len(files)), nil
	}
	return strings.Join(files, "\n"), nil
}

// EnsureGitignore ensures .gitignore contains the given entries.
func EnsureGitignore(root string, entries []string) error {
	path := filepath.Join(root, ".gitignore")
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	var toAdd []string
	for _, e := range entries {
		if !strings.Contains(existing, e) {
			toAdd = append(toAdd, e)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}
	content := existing
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(toAdd, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// ApplyPatch applies a unified diff to the working tree.
//
// This is how a tournament winner is applied: the candidate's patch is written
// to the main tree byte-for-byte. No model is involved, and nothing is
// regenerated — on modest models, free regeneration corrupts working code
// (I8).
func (g *Git) ApplyPatch(patch string) error {
	if strings.TrimSpace(patch) == "" {
		return fmt.Errorf("empty patch")
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = g.Root
	cmd.Stdin = strings.NewReader(ensureTrailingNewline(patch))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// DiffAgainst returns the diff of the working tree against a commit, including
// files git does not yet track. A contestant that creates a new file must have
// it captured, or its patch silently loses the work.
func (g *Git) DiffAgainst(ref string) (string, error) {
	if _, err := g.run("add", "-AN"); err != nil {
		return "", err
	}
	return g.run("diff", ref)
}

// PruneWorktrees removes worktree records whose directories are gone.
// Called at engine start: a killed engine leaves stale entries that make a
// later `worktree add` on the same path fail.
func (g *Git) PruneWorktrees() error {
	_, err := g.run("worktree", "prune")
	return err
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// DeleteBranch removes a branch, ignoring the case where it is already gone.
func (g *Git) DeleteBranch(name string) error {
	_, err := g.run("branch", "-D", name)
	return err
}
