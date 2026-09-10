package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// A shared-checkout run begins after board/document churn may already be
// dirty. Accept owns only the delta beyond its start snapshot, even when the
// older harness change was staged first.
func TestNonWorktreeAcceptLeavesPreexistingHarnessChangesForTheSweep(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	id, dir := projectWithDocs(t, s, map[artifact.Kind]string{artifact.KindPlan: planDoc})
	git := gitProject(t, dir)
	base := mustHead(t, git)

	planPath := filepath.Join(dir, ".ducklab", "docs", "plan.md")
	planBefore, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, append(planBefore, []byte("\n<!-- unrelated board churn -->\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.Add(filepath.ToSlash(filepath.Join(".ducklab", "docs", "plan.md"))); err != nil {
		t.Fatal(err)
	}
	snapshot, err := git.SnapshotTree()
	if err != nil {
		t.Fatal(err)
	}

	run := &runlog.Run{
		ID: "r-shared-scope", ProjectID: id, TaskID: "T-001", Stage: "build",
		Status: "paused", Verdict: "PASSED", PendingKind: "gate",
		TreeSnapshot: snapshot, TreeSnapshotHead: base,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	writer, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owned.txt"), []byte("written by this run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := s.RunAccept(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("accept scoped run: %v", err)
	}
	changed, err := git.ChangedPaths(base, result.CommitSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != "owned.txt" {
		t.Fatalf("landing commit paths = %v, want only owned.txt", changed)
	}
	staged, err := git.StagedPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 1 || staged[0] != ".ducklab/docs/plan.md" {
		t.Fatalf("preexisting harness change was not preserved for the sweep: %v", staged)
	}
}
