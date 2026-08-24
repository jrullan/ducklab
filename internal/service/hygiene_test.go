package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/runlog"
	"github.com/jrullan/ducklab/internal/vcs"
)

func TestRecoverRunsHygieneReapsOrphansAndReattachesPausedWorktree(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	paused := &runlog.Run{ID: "r-paused", ProjectID: id, TaskID: "T-115", Stage: "build", Status: "paused", Branch: "ducklab/T-115-paused", WorktreePath: t.TempDir() + "/paused", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := git.WorktreeAdd(paused.WorktreePath, paused.Branch); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(paused.WorktreePath); err != nil {
		t.Fatal(err)
	}
	orphan := t.TempDir() + "/orphan"
	if err := git.WorktreeAdd(orphan, "ducklab/orphan"); err != nil {
		t.Fatal(err)
	}
	w, err := runlog.NewWriter(dir, paused)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paused.WorktreePath); err != nil {
		t.Fatalf("paused worktree was not reattached: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan worktree remains: %v", err)
	}
}

func TestRecoverRunsHygieneGCsDecidedWorktreeAndBranch(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, nil)
	git := gitProject(t, dir)
	run := &runlog.Run{ID: "r-decided", ProjectID: id, TaskID: "T-115", Stage: "build", Status: "done", Accepted: true, Branch: "ducklab/T-115-decided", WorktreePath: t.TempDir() + "/decided", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := git.WorktreeAdd(run.WorktreePath, run.Branch); err != nil {
		t.Fatal(err)
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("decided worktree remains: %v", err)
	}
	if vcs.New(dir).BranchExists(run.Branch) {
		t.Fatal("decided branch remains")
	}
}
