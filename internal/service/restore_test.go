package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// gitProject turns a bare test dir into a repo with one committed file.
func gitProject(t *testing.T, dir string) *vcs.Git {
	t.Helper()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	g := vcs.New(dir)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("init"); err != nil {
		t.Fatal(err)
	}
	return g
}

// Reject meant "no, but keep everything anyway": the record said FAILED while
// the half-made edits stayed in the tree. The next attempt of the same task
// found them and said, in its thinking, that somebody had already fixed it —
// and the person watching knew nobody had accepted anything.
func TestRejectPutsTheTreeBack(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	g := gitProject(t, dir)

	snap, err := g.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}
	run := &runlog.Run{
		ID: "r-1", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		TreeSnapshot: snap,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	// The run's half-made work, sitting uncommitted.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("half-made fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.RunReject(context.Background(), "r-1", "not what I asked for"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Errorf("the rejected run's edits are still in the tree: %q", got)
	}
}

// An accepted run's work is the point of the run. Restore must never touch it.
func TestAcceptDoesNotRestore(t *testing.T) {
	rs := &runState{run: &runlog.Run{ID: "r-2", TreeSnapshot: "abc", Accepted: true}}
	// Must be a no-op — reaching for git with a fake path would error loudly.
	restoreAfterUnaccepted(rs)
}

// Aborting is the cleanup path for a build that stopped before acceptance. It
// must restore both kinds of model residue, including when no run goroutine is
// left to unwind (the paused case), so the next test-first/retire action sees
// the same tree the build started with.
func TestAbortRestoresPausedBuildTreeAndAllowsCleanRecovery(t *testing.T) {
	for _, status := range []string{"paused", "failed"} {
		t.Run(status, func(t *testing.T) {
			s := serviceWithDucklings(t, "pato-uno")
			id, dir := projectWithDocs(t, s, nil)
			g := gitProject(t, dir)

			snap, err := g.SnapshotTree()
			if err != nil {
				t.Fatal(err)
			}
			run := &runlog.Run{
				ID: "r-abort-" + status, ProjectID: id, TaskID: "T-001", Stage: "build",
				Status: status, Verdict: "PASSED", PendingKind: "gate",
				TreeSnapshot: snap, StartedAt: time.Now().UTC().Format(time.RFC3339),
			}
			w, err := runlog.NewWriter(dir, run)
			if err != nil {
				t.Fatal(err)
			}
			w.Close()
			s.RecoverRuns(context.Background())

			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("model edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			created := filepath.Join(dir, "created-by-model.txt")
			if err := os.WriteFile(created, []byte("model file\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			if err := s.RunAbort(context.Background(), run.ID); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, "index.html")); err != nil || string(got) != "original\n" {
				t.Errorf("tracked model edit survived abort: %q, %v", got, err)
			}
			if _, err := os.Stat(created); !os.IsNotExist(err) {
				t.Errorf("untracked model file survived abort: %v", err)
			}
			if dirty := g.DirtyPaths(); len(dirty) != 0 {
				for _, path := range dirty {
					if path != ".ducklab" && !strings.HasPrefix(path, ".ducklab/") {
						t.Errorf("abort left task files dirty: %v", dirty)
						break
					}
				}
			}
		})
	}
}
