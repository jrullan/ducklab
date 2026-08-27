package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

// A chained red test lives only on its run branch until its build lands, so
// retiring it must not attempt a revert on the registered checkout — there is
// nothing there to undo. The unconditional revert died with "bad revision" on
// exactly the run it was most needed for: a mislaunched chain whose test
// auto-accepted (T-238, 2026-08-27).
func TestRetireOfABranchOnlyTestRevertsNothing(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	p, err := s.ProjectInit(context.Background(), InitRequest{Path: dir, Name: "T", GitInit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifact.Path(dir, artifact.KindPlan)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path(dir, artifact.KindPlan),
		[]byte("## M-001 — Core\n\n### T-022 — Sessions\n\nDo it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := vcs.New(dir)
	if err := git.AddAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := git.Commit("baseline"); err != nil {
		t.Fatal(err)
	}
	base, err := git.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}

	// The red test commit on its own run branch, never landed on main.
	wt := filepath.Join(t.TempDir(), "run-wt")
	if err := git.WorktreeAddAt(wt, "ducklab/T-022-test", base); err != nil {
		t.Fatal(err)
	}
	wtGit := vcs.New(wt)
	if err := os.WriteFile(filepath.Join(wt, "session_test.py"), []byte("def test_sessions(): assert False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wtGit.AddAll(); err != nil {
		t.Fatal(err)
	}
	sha, err := wtGit.Commit("chained: the test landed red")
	if err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-t22", ProjectID: p.ID, TaskID: "T-022", Stage: "test",
		Status: "done", Verdict: "PASSED", Accepted: true, CommitSHA: sha,
		Resolution: "accepted by auto:tdd",
		StartedAt:  "2026-08-06T15:14:00Z",
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	s.RecoverRuns(context.Background())

	got, err := s.TestRetire(context.Background(), p.ID, "T-022")
	if err != nil {
		t.Fatal(err)
	}
	if got.RevertSHA != "branch-only" {
		t.Fatalf("RevertSHA = %q, want branch-only", got.RevertSHA)
	}
	// Main is untouched: same HEAD, no revert commit.
	head, err := git.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if head != base {
		t.Fatalf("main HEAD moved from %s to %s; a branch-only retire must not touch it", base, head)
	}
}
