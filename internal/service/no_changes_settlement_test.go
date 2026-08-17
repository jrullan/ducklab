package service

import (
	"context"
	"testing"
	"time"

	"github.com/jrullan/ducklab/internal/artifact"
	"github.com/jrullan/ducklab/internal/runlog"
)

// An accepted no_changes build is the acceptance record that the task's work
// already exists. It must settle the task just like an accepted build that
// produced a commit; otherwise the queue can launch the same empty build again.
func TestAcceptedNoChangesBuildSettlesTaskAndLeavesItOutOfAutopilotQueue(t *testing.T) {
	s := serviceWithDucklings(t, "pato-uno")
	projectID, dir := projectWithDocs(t, s, map[artifact.Kind]string{
		artifact.KindRequirements: "## REQ-001 — Existing work\n",
		artifact.KindSpec:         "## SPEC-001 — Existing work\n\n**Implements:** REQ-001\n",
		artifact.KindPlan:         "## M-001 — Existing work\n\n### T-001 — Already done\n\n**Implements:** SPEC-001\n\nThe implementation is already in the tree.\n",
	})

	run := &runlog.Run{
		ID: "r-no-changes", ProjectID: projectID, TaskID: "T-001", Stage: "build",
		Status: "done", Verdict: "PASSED", Accepted: true, NoChanges: true,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	w, err := runlog.NewWriter(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := s.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.TaskList(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want one", len(tasks))
	}
	if tasks[0].Status != "accepted" {
		t.Fatalf("accepted no_changes task status = %q, want accepted", tasks[0].Status)
	}

	// The same derived state must reject a direct relaunch as well as the queue
	// path; autopilot and the task action cannot disagree about settlement.
	if _, err := s.RunStart(context.Background(), projectID, RunRequest{TaskID: "T-001", Mode: "solo"}); err == nil {
		t.Fatal("accepted no_changes task was relaunchable")
	}

	// TaskNext is the mechanical queue consumed by autopilot. Once the accepted
	// judgement is recorded, there is no lawful automatic relaunch; a human can
	// still deliberately reopen it through the normal task actions.
	next, err := s.TaskNext(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("autopilot queue returned %s after accepted no_changes run", next.ID)
	}
}
