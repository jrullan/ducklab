package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
