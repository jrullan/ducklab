package strategy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jrullan/ducklab/internal/vcs"
)

// gitWorkspace is a contestant's isolated git worktree.
type gitWorkspace struct {
	root     string
	branch   string
	base     string
	parent   *vcs.Git
	once     sync.Once
	closeErr error
}

func (w *gitWorkspace) Root() string { return w.root }

// Patch returns the contestant's work as a diff against the base commit,
// including files it created.
func (w *gitWorkspace) Patch() (string, error) {
	return vcs.New(w.root).DiffAgainst(w.base)
}

// Close removes the worktree and its scratch branch. Idempotent: it is called
// from a defer and may also be called by a cleanup sweep.
func (w *gitWorkspace) Close() error {
	w.once.Do(func() {
		if err := w.parent.WorktreeRemove(w.root); err != nil {
			// The directory may already be gone; prune reconciles the record
			// either way, and leaving a stale record breaks the next run.
			w.closeErr = err
		}
		w.parent.PruneWorktrees()
		os.RemoveAll(w.root)
		w.parent.DeleteBranch(w.branch)
	})
	return w.closeErr
}

// NewGitWorkspaceFactory returns a factory that isolates each contestant in a
// git worktree under dir.
//
// Isolation is what makes concurrent contestants possible at all: two models
// editing one working tree would interleave writes and produce a diff that
// belongs to neither.
func NewGitWorkspaceFactory(repoRoot, scratchDir, runID string) WorkspaceFactory {
	return func(ctx context.Context, label string) (Workspace, error) {
		parent := vcs.New(repoRoot)
		base, err := parent.HeadSHA()
		if err != nil {
			return nil, fmt.Errorf("resolve base commit: %w", err)
		}
		// Prune first: a killed engine leaves records that make add fail on a
		// path that looks free.
		parent.PruneWorktrees()

		if err := os.MkdirAll(scratchDir, 0o755); err != nil {
			return nil, err
		}
		root := filepath.Join(scratchDir, fmt.Sprintf("%s-%s", runID, label))
		branch := fmt.Sprintf("ducklab/wt/%s-%s", runID, label)

		os.RemoveAll(root)
		if err := parent.WorktreeAdd(root, branch); err != nil {
			return nil, fmt.Errorf("create worktree for %s: %w", label, err)
		}
		return &gitWorkspace{root: root, branch: branch, base: base, parent: parent}, nil
	}
}

// ReapWorktrees removes every ducklab scratch worktree left behind by a dead
// engine. Called at engine start (05 §4.3, AC-19).
func ReapWorktrees(repoRoot, scratchDir string) error {
	g := vcs.New(repoRoot)
	list, err := g.WorktreeList()
	if err != nil {
		return err
	}
	for _, path := range list {
		if path == repoRoot {
			continue
		}
		if scratchDir != "" && !isUnder(path, scratchDir) {
			continue
		}
		g.WorktreeRemove(path)
		os.RemoveAll(path)
	}
	return g.PruneWorktrees()
}

func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) &&
		(len(rel) < 2 || rel[:2] != "..")
}
